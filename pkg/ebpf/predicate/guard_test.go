package predicate

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every cgroup-attached BPF program must consult the enforcement predicate
// before doing anything else. Since Phase 135 the programs attach at the ROOT
// cgroup, so an ungated program runs for every process on the box — including
// amazon-ssm-agent, whose traffic must never be subject to the sandbox's
// allowlist. Getting that wrong does not fail a test or log a warning; it
// strands the instance.
//
// Name-agnostic on purpose: a fifth program added later is covered with no
// edit here. That is the same shape as pkg/netpolicy's and pkg/secrets'
// wiring guards, and for the same reason — the pairing has to be mechanical
// rather than remembered.
func TestEveryCgroupProgramConsultsThePredicate(t *testing.T) {
	body := readSibling(t, "../bpf.c")

	secRE := regexp.MustCompile(`(?m)^SEC\("([^"]+)"\)\n`)
	locs := secRE.FindAllStringSubmatchIndex(body, -1)
	if len(locs) < 4 {
		t.Fatalf("found %d SEC() declarations in bpf.c, expected at least 4", len(locs))
	}

	seen := 0
	for i, loc := range locs {
		sec := body[loc[2]:loc[3]]
		if strings.HasPrefix(sec, "license") || strings.HasPrefix(sec, ".maps") {
			continue
		}
		start := loc[1]
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		prog := body[start:end]
		seen++

		// cgroup_skb/* fires in softirq with no current task, so
		// bpf_get_current_uid_gid() is rejected by the verifier (bpf.c:118).
		// It reads the uid off the socket instead.
		wantGate := "km_enforced_task"
		if strings.HasPrefix(sec, "cgroup_skb/") {
			wantGate = "km_enforced_skb"
		}
		if !strings.Contains(prog, wantGate) {
			t.Errorf("SEC(%q) never calls %s(): attached at the root cgroup it would "+
				"run for every process on the box", sec, wantGate)
		}
	}
	if seen < 4 {
		t.Errorf("only inspected %d programs, expected at least 4", seen)
	}
}

// The predicate must fail OPEN. These programs now sit in the path of every
// packet on the box, so a loader that fails to set a constant must enforce
// NOTHING rather than deny everything — the difference between a sandbox that
// is merely unenforced and an instance nobody can reach.
func TestPredicateConstantsDefaultToUnset(t *testing.T) {
	body := readSibling(t, "../headers/common.h")
	for _, want := range []string{
		"#define KM_UID_UNSET 0xFFFFFFFF",
		"volatile const __u32 const_sandbox_uid = KM_UID_UNSET;",
		"volatile const __u64 const_km_cgid = 0;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("common.h must contain %q verbatim (fail-open defaults)", want)
		}
	}
}

// Both clauses must be guarded by their sentinel. Without the guard, an unset
// const_km_cgid of 0 would match any caller whose cgroup id read back as 0,
// and an unset uid would match uid 0xFFFFFFFF.
func TestPredicateClausesAreSentinelGuarded(t *testing.T) {
	body := readSibling(t, "../bpf.c")
	for _, want := range []string{
		"const_sandbox_uid != KM_UID_UNSET",
		"const_km_cgid != 0",
	} {
		if strings.Count(body, want) < 2 {
			t.Errorf("bpf.c must guard %q in BOTH predicate variants (task and skb)", want)
		}
	}
}

// The enforcer's CLI must actually arm both clauses of the predicate.
//
// This guard lives here rather than beside ebpf_attach.go because that file is
// //go:build linux && amd64 and so is invisible to `go test ./...` on a dev
// machine — the same blind spot that let the original gap survive for months.
// Same cross-tree scanning pattern as pkg/secrets' wiring guard.
func TestEbpfAttachArmsBothPredicateClauses(t *testing.T) {
	src := readSibling(t, "../../../internal/app/cmd/ebpf_attach.go")

	// Before Phase 135 --cgroup was a parameter nothing read.
	if !strings.Contains(src, "CgroupAttachPath: cgroupOverride") {
		t.Error("cgroupOverride must reach Config.CgroupAttachPath; a flag that " +
			"silently does nothing is worse than no flag")
	}
	if !strings.Contains(src, "SandboxUID:") {
		t.Error("Config.SandboxUID must be set from the resolved sandbox uid, or the " +
			"uid clause is never armed and interactive sessions stay unenforced " +
			"on a box that looks entirely healthy")
	}

	// The uid lookup must be fatal. Warning and continuing would boot a box
	// whose enforcer is running and whose programs are attached, enforcing
	// nothing interactive — the exact silent-success shape this phase ends.
	idx := strings.Index(src, `user.Lookup("sandbox")`)
	if idx < 0 {
		t.Fatal(`expected user.Lookup("sandbox") to resolve the predicate uid`)
	}
	end := idx + 400
	if end > len(src) {
		end = len(src)
	}
	if !strings.Contains(src[idx:end], "return fmt.Errorf") {
		t.Error("a failed sandbox-uid lookup must return an error, not warn and continue")
	}
}

func readSibling(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
