# Interactive Session Enforcement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make km's eBPF network enforcement apply to interactive sessions (`km shell`, `km herdr`, `km vscode`, direct `ssh`, `sudo -u sandbox`), which have never been inside the cgroup the programs are attached to.

**Architecture:** Stop migrating processes into `km.slice` and instead attach the four programs at the **root cgroup**, gating each on `uid == const_sandbox_uid || cgroup_id == const_km_cgid`. The uid clause covers interactive sessions wherever logind or the SSM agent puts them; the cgroup clause keeps a root process that was deliberately placed in the scope enforced, preserving EBPF-NET-12. Nothing is migrated, so the silent-join-failure class disappears rather than being repaired.

**Tech Stack:** C (BPF, clang via `make generate-ebpf`), Go 1.25, `github.com/cilium/ebpf` v0.21.0, cobra, terraform/terragrunt for deploy.

**Spec:** `docs/superpowers/specs/2026-09-06-ssh-session-cgroup-gap-design.md` — read §4a (the spike) before starting; it contains the verified kernel behaviour every task here depends on.

## Global Constraints

- **Fail open, never closed.** `const_sandbox_uid` defaults to `0xFFFFFFFF` (matches no real user) and `const_km_cgid` to `0`. A loader that fails to set them must enforce *nothing*. Once these programs sit at the root cgroup, a mis-set constant is the difference between a working sandbox and an unreachable box.
- **The cgroup id is a directory inode and MUST be read at attach time.** It changes across reboots. Never persist it, never pass it through userdata.
- **`km.slice` stays.** `CreateSandboxCgroup` / `RemoveSandboxCgroup` keep working, Phase 132's `dispatch_as_sandbox` sites keep joining it, and the cgroup clause depends on it existing.
- **All four programs must be gated.** `connect4`, `sendmsg4`, `sockops`, `egress_filter`. A fifth program added later without a gate would run for every process on the box — Task 2 adds a mechanical guard so that cannot happen silently.
- **`bpf_get_current_uid_gid()` is forbidden in `cgroup_skb/egress`** (softirq, no current task — `bpf.c:118`). That program uses `bpf_get_socket_uid(skb)` and `bpf_skb_cgroup_id(skb)`. Verified working on AL2023 kernel 6.1.182 in spec §4a.
- **Every program's "pass" return value is `1`.** Confirmed for all four.
- `pkg/ebpf` is `//go:build linux`; its tests do not run on macOS. Anything that must be testable from a dev machine belongs in a portable file or in `pkg/compiler` / `internal/app/cmd`.

---

### Task 1: A portable cgroup-id helper

The enforcer needs the scope's cgroup v2 id, which is the cgroup directory's inode. Putting it in its own build-tag-free file makes it the one piece of this change that is unit-testable from macOS.

**Files:**
- Create: `pkg/ebpf/cgroupid.go`
- Test: `pkg/ebpf/cgroupid_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func CgroupID(path string) (uint64, error)` — returns the inode of `path`, which is the cgroup v2 id the kernel reports from `bpf_get_current_cgroup_id()` / `bpf_skb_cgroup_id()`.

- [ ] **Step 1: Write the failing test**

```go
package ebpf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCgroupID_ReturnsDirectoryInode(t *testing.T) {
	dir := t.TempDir()
	got, err := CgroupID(dir)
	if err != nil {
		t.Fatalf("CgroupID(%q): %v", dir, err)
	}
	if got == 0 {
		t.Fatal("CgroupID returned 0; a real cgroup id is never 0, and 0 is the sentinel meaning 'unset'")
	}

	// Two different directories must not collide, or the cgroup clause of the
	// enforcement predicate would match the wrong scope.
	other := filepath.Join(t.TempDir(), "sibling")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	got2, err := CgroupID(other)
	if err != nil {
		t.Fatal(err)
	}
	if got == got2 {
		t.Fatalf("distinct directories returned the same id (%d)", got)
	}
}

func TestCgroupID_MissingPathIsAnError(t *testing.T) {
	// A missing scope must be a loud error, never a silent 0 — 0 is the
	// "no cgroup clause" sentinel, so swallowing this would quietly disable
	// half the enforcement predicate.
	if _, err := CgroupID(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected an error for a missing path, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/ebpf/ -run TestCgroupID -v`
Expected: FAIL — `undefined: CgroupID`

- [ ] **Step 3: Write minimal implementation**

```go
package ebpf

import (
	"fmt"
	"os"
	"syscall"
)

// CgroupID returns the cgroup v2 id of the cgroup directory at path.
//
// A cgroup v2 id IS the cgroup directory's inode number — the same value the
// kernel reports from bpf_get_current_cgroup_id() and bpf_skb_cgroup_id().
// It is assigned when the directory is created, so it changes across reboots
// and must be read at attach time rather than persisted anywhere.
//
// Deliberately has no build tag: the enforcer it serves is linux-only, but
// keeping this portable is what makes it testable from a dev machine.
func CgroupID(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat cgroup %s: %w", path, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("stat cgroup %s: unexpected stat type %T", path, info.Sys())
	}
	return uint64(st.Ino), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/ebpf/ -run TestCgroupID -v`
Expected: PASS (both tests)

- [ ] **Step 5: Commit**

```bash
git add pkg/ebpf/cgroupid.go pkg/ebpf/cgroupid_test.go
git commit -m "feat(ebpf): read a cgroup v2 id from its directory inode"
```

---

### Task 2: The enforcement predicate in BPF, and a guard that no program escapes it

**Files:**
- Modify: `pkg/ebpf/headers/common.h` (constants block, after `const_mitm_proxy_address`)
- Modify: `pkg/ebpf/bpf.c` (add two gate helpers; add one gate line to each of the four programs)
- Create: `pkg/ebpf/predicate_guard_test.go`
- Regenerate: `pkg/ebpf/bpf_x86_bpfel.go`, `pkg/ebpf/bpf_x86_bpfel.o`

**Interfaces:**
- Consumes: nothing.
- Produces: two BPF constants the Go loader must set — `const_sandbox_uid` (`__u32`) and `const_km_cgid` (`__u64`). Task 3 sets both.

- [ ] **Step 1: Write the failing guard test**

This is the mechanical pairing guard, in the same spirit as `pkg/netpolicy/wiring_guard_test.go` and `pkg/secrets/wiring_guard_test.go`. It is name-agnostic: a fifth program added later is covered without editing the test. It runs on macOS because it only reads source text.

```go
package ebpf

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every cgroup-attached BPF program must consult the enforcement predicate
// before doing anything else. Since Phase 135 the programs are attached at the
// ROOT cgroup, so an ungated program runs for every process on the box —
// including the SSM agent, whose traffic must never be enforced.
//
// Name-agnostic on purpose: a program added later is covered with no edit here.
func TestEveryCgroupProgramConsultsThePredicate(t *testing.T) {
	src, err := os.ReadFile("bpf.c")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	// Split into per-program bodies keyed by their SEC() attachment.
	secRE := regexp.MustCompile(`(?m)^SEC\("([^"]+)"\)\n[^\n]*\n`)
	locs := secRE.FindAllStringSubmatchIndex(body, -1)
	if len(locs) < 4 {
		t.Fatalf("found %d SEC() programs in bpf.c, expected at least 4", len(locs))
	}

	for i, loc := range locs {
		sec := body[loc[2]:loc[3]]
		start := loc[1]
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		prog := body[start:end]

		// The license stanza is a SEC() but not a program.
		if strings.HasPrefix(sec, "license") {
			continue
		}

		wantGate := "km_enforced_task"
		if strings.HasPrefix(sec, "cgroup_skb/") {
			// No current task in packet context — bpf.c:118.
			wantGate = "km_enforced_skb"
		}
		if !strings.Contains(prog, wantGate) {
			t.Errorf("SEC(%q) does not call %s(); at the root cgroup an ungated "+
				"program runs for every process on the box", sec, wantGate)
		}
	}
}

// The predicate must fail OPEN. If a loader forgets to set either constant the
// programs must enforce nothing, because these now sit in the path of every
// packet on the box.
func TestPredicateConstantsDefaultToUnset(t *testing.T) {
	src, err := os.ReadFile("headers/common.h")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	for _, want := range []string{
		"volatile const __u32 const_sandbox_uid = KM_UID_UNSET;",
		"volatile const __u64 const_km_cgid = 0;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("common.h must declare %q verbatim (fail-open defaults)", want)
		}
	}
	if !strings.Contains(body, "#define KM_UID_UNSET 0xFFFFFFFF") {
		t.Error("common.h must define KM_UID_UNSET as 0xFFFFFFFF")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/ebpf/ -run 'TestEveryCgroupProgramConsultsThePredicate|TestPredicateConstantsDefaultToUnset' -v`
Expected: FAIL — no `km_enforced_task` in any program, no constants in `common.h`

Note: on macOS these two tests compile because they read source text only; the rest of `pkg/ebpf` is `//go:build linux` and will not build. Run them on Linux (or in the ebpf-generate container) if the package fails to compile locally.

- [ ] **Step 3: Add the constants to `pkg/ebpf/headers/common.h`**

Append to the volatile-constants block, after `const_mitm_proxy_address`:

```c
/* ── Phase 135: the enforcement predicate ───────────────────────────
 * Since Phase 135 the programs attach at the ROOT cgroup rather than the
 * per-sandbox scope, because no interactive session ever reached that scope
 * (cgroup v2 requires write access to the common ancestor, which is the root
 * cgroup — see docs/operational-gotchas.md).
 *
 * Enforcement therefore selects on identity rather than placement:
 *
 *     enforced = (uid == const_sandbox_uid) || (cgroup_id == const_km_cgid)
 *
 * The uid clause covers interactive sessions wherever logind or the SSM agent
 * puts them. The cgroup clause keeps a root process that was deliberately
 * placed in the scope enforced, which is what preserves EBPF-NET-12 — a bare
 * uid filter would make sudo a way OUT of enforcement.
 *
 * Both defaults are sentinels meaning "unset", so a loader that fails to set
 * them enforces NOTHING. That disposition is load-bearing: these programs are
 * now in the path of every packet on the box.
 */
#define KM_UID_UNSET 0xFFFFFFFF
volatile const __u32 const_sandbox_uid = KM_UID_UNSET;
volatile const __u64 const_km_cgid = 0;
```

- [ ] **Step 4: Add the gate helpers to `pkg/ebpf/bpf.c`**

Insert immediately after the `emit_event_skb` helper (before `PROGRAM 1`):

```c
/* ════════════════════════════════════════════════════════════════════
 * HELPER: the Phase 135 enforcement predicate
 *
 * Two variants because the available helpers differ by context:
 *   - km_enforced_task() for connect4 / sendmsg4 / sockops (process context)
 *   - km_enforced_skb()  for cgroup_skb/egress, which fires in softirq with no
 *     current task, so bpf_get_current_uid_gid() is rejected by the verifier
 *     (see the emit_event note above). bpf_get_socket_uid() reads the uid off
 *     the socket instead and is valid there — verified on AL2023 kernel 6.1.
 *
 * Returns 1 when this caller is subject to km enforcement, 0 to pass through.
 * ════════════════════════════════════════════════════════════════════ */
static __always_inline int km_enforced_task(void)
{
    if (const_sandbox_uid != KM_UID_UNSET &&
        (__u32)bpf_get_current_uid_gid() == const_sandbox_uid)
        return 1;
    if (const_km_cgid != 0 && bpf_get_current_cgroup_id() == const_km_cgid)
        return 1;
    return 0;
}

static __always_inline int km_enforced_skb(struct __sk_buff *skb)
{
    if (const_sandbox_uid != KM_UID_UNSET &&
        bpf_get_socket_uid(skb) == const_sandbox_uid)
        return 1;
    if (const_km_cgid != 0 && bpf_skb_cgroup_id(skb) == const_km_cgid)
        return 1;
    return 0;
}
```

- [ ] **Step 5: Gate all four programs**

Add as the FIRST statement in each program body — before the proxy-pid exemptions, before any parsing, so the cost for an unenforced caller is two compares.

In `connect4`, `sendmsg4` and `bpf_sockops`:

```c
    /* Phase 135: attached at the root cgroup — pass anything not ours. */
    if (!km_enforced_task())
        return 1;
```

In `egress_filter`:

```c
    /* Phase 135: attached at the root cgroup — pass anything not ours. */
    if (!km_enforced_skb(skb))
        return 1;
```

- [ ] **Step 6: Run the guard test to verify it passes**

Run: `go test ./pkg/ebpf/ -run 'TestEveryCgroupProgramConsultsThePredicate|TestPredicateConstantsDefaultToUnset' -v`
Expected: PASS

- [ ] **Step 7: Regenerate the BPF objects**

Run: `make generate-ebpf`
Expected: succeeds; `git status` shows `pkg/ebpf/bpf_x86_bpfel.go` and `pkg/ebpf/bpf_x86_bpfel.o` modified. If clang errors on an undeclared helper, the image's libbpf headers are older than expected — `bpf_get_socket_uid`, `bpf_skb_cgroup_id` and `bpf_get_current_cgroup_id` are all declared in `bpf_helper_defs.h` and were confirmed compiling in the spike.

- [ ] **Step 8: Commit**

```bash
git add pkg/ebpf/bpf.c pkg/ebpf/headers/common.h pkg/ebpf/predicate_guard_test.go pkg/ebpf/bpf_x86_bpfel.go pkg/ebpf/bpf_x86_bpfel.o
git commit -m "feat(ebpf): gate every cgroup program on uid or km.slice membership"
```

---

### Task 3: Attach at the root cgroup and set the predicate constants

**Files:**
- Modify: `pkg/ebpf/types.go` (`Config` struct)
- Modify: `pkg/ebpf/enforcer.go` (`NewEnforcer`: constants + attach path)

**Interfaces:**
- Consumes: `CgroupID(path string) (uint64, error)` from Task 1; `const_sandbox_uid` / `const_km_cgid` from Task 2.
- Produces: `Config.SandboxUID uint32` and `Config.CgroupAttachPath string`, both set by Task 4.

- [ ] **Step 1: Extend `Config` in `pkg/ebpf/types.go`**

Add to the struct, after `MITMProxyAddr`:

```go
	// SandboxUID is the uid enforcement selects on. Since Phase 135 the BPF
	// programs attach at the root cgroup, so identity — not cgroup placement —
	// decides who is enforced. 0 or math.MaxUint32 means "no uid clause".
	SandboxUID uint32
	// CgroupAttachPath is where the programs attach. Empty means the root
	// cgroup (the Phase 135 default). The per-sandbox scope is still created
	// and still forms the second half of the enforcement predicate; this is
	// only about where the programs hang.
	CgroupAttachPath string
```

- [ ] **Step 2: Set the constants in `NewEnforcer`**

After the `const_mitm_proxy_address` block in `pkg/ebpf/enforcer.go`:

```go
	// Phase 135 enforcement predicate. The cgroup id is the scope directory's
	// inode, so it MUST be read now rather than persisted — it changes across
	// reboots. A failure here is fatal: leaving the constant at its 0 sentinel
	// would silently drop the cgroup clause and with it EBPF-NET-12.
	kmCgroupID, err := CgroupID(cgroupPath)
	if err != nil {
		return nil, fmt.Errorf("read km.slice scope cgroup id: %w", err)
	}
	if err := spec.Variables["const_km_cgid"].Set(kmCgroupID); err != nil {
		return nil, fmt.Errorf("set const_km_cgid: %w", err)
	}
	sandboxUID := cfg.SandboxUID
	if sandboxUID == 0 {
		// 0 is root; enforcing on root would take the box out. Treat it as
		// "unset" and fall through to the cgroup clause alone.
		sandboxUID = math.MaxUint32
	}
	if err := spec.Variables["const_sandbox_uid"].Set(sandboxUID); err != nil {
		return nil, fmt.Errorf("set const_sandbox_uid: %w", err)
	}
```

Add `"math"` to the imports.

- [ ] **Step 3: Attach at the root cgroup**

Immediately before `// Step 5: attach all four programs`, insert:

```go
	// Phase 135: attach above every slice rather than inside km.slice. No
	// interactive session ever reached that scope — cgroup v2 requires write
	// access to the common ancestor of source and destination, which is the
	// root cgroup — so the programs saw agent turns and nothing else. The
	// predicate compiled into them, not the attach point, now decides who is
	// enforced.
	attachPath := cfg.CgroupAttachPath
	if attachPath == "" {
		attachPath = detectCgroup2Mount()
	}
```

Then change all four `link.AttachCgroup` calls from `Path: cgroupPath` to `Path: attachPath`, and change the struct literal at the end from `cgroupPath: cgroupPath` to keep storing `cgroupPath` (teardown still removes the scope, not the attach point).

- [ ] **Step 4: Verify it compiles for linux**

Run: `GOOS=linux GOARCH=amd64 go build ./pkg/ebpf/ && go build ./...`
Expected: both succeed with no output.

- [ ] **Step 5: Commit**

```bash
git add pkg/ebpf/types.go pkg/ebpf/enforcer.go
git commit -m "feat(ebpf): attach at the root cgroup, select by uid or scope"
```

---

### Task 4: Make `--cgroup` real and add `--sandbox-uid`

`--cgroup` is currently accepted, threaded into `runEbpfAttach` as `cgroupOverride`, and **never referenced in the body** — the userdata has been passing a value that does nothing. This task makes it real and adds the uid flag beside it.

**Files:**
- Modify: `internal/app/cmd/ebpf_attach.go`
- Create: `internal/app/cmd/ebpf_attach_predicate_test.go`

**Interfaces:**
- Consumes: `Config.SandboxUID`, `Config.CgroupAttachPath` from Task 3.
- Produces: flags `--sandbox-uid` (default 0 = look up the `sandbox` user) and a now-functional `--cgroup`.

- [ ] **Step 1: Write the failing test**

```go
package cmd

import (
	"strings"
	"testing"
)

// --cgroup was accepted and silently ignored before Phase 135: it was threaded
// into runEbpfAttach as cgroupOverride and never referenced. A flag that lies
// about what it does is worse than no flag.
func TestEbpfAttachFlags_CgroupAndSandboxUIDExist(t *testing.T) {
	cmd := newEbpfAttachCmd()
	for _, name := range []string{"cgroup", "sandbox-uid"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s flag is missing", name)
		}
	}
}

func TestEbpfAttachCgroupOverrideIsWiredIn(t *testing.T) {
	src := readSourceFile(t, "ebpf_attach.go")
	// cgroupOverride must reach the Config, not just the signature.
	if !strings.Contains(src, "CgroupAttachPath: cgroupOverride") {
		t.Error("cgroupOverride must be assigned to Config.CgroupAttachPath; " +
			"before Phase 135 it was a parameter that nothing read")
	}
	if !strings.Contains(src, "SandboxUID:") {
		t.Error("Config.SandboxUID must be set from the resolved sandbox uid")
	}
}
```

Add this helper to the same file if `readSourceFile` does not already exist in the package:

```go
func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
```

(imports: `os`, `strings`, `testing`)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/cmd/ -run 'TestEbpfAttach.*(Cgroup|SandboxUID)' -v`
Expected: FAIL — `--sandbox-uid` missing, `CgroupAttachPath:` not found

If `newEbpfAttachCmd` is not the constructor's name, use the actual one from `ebpf_attach.go` and adjust the test.

- [ ] **Step 3: Add the flag and resolve the uid**

Add to the `var (...)` block: `sandboxUID uint32`.

Add the flag beside `--cgroup`:

```go
	cmd.Flags().Uint32Var(&sandboxUID, "sandbox-uid", 0,
		"uid enforcement selects on (0 = look up the 'sandbox' user)")
```

Thread it through the `RunE` call and the `runEbpfAttach` signature (after `cgroupOverride string`), then in the body, before `cfg := ebpf.Config{`:

```go
	// Resolve the uid the enforcement predicate selects on. Looked up rather
	// than hardcoded so a profile that provisions the user differently still
	// enforces. A failure is fatal: falling back to "no uid clause" would boot
	// a box that looks enforced and silently is not for every interactive
	// session, which is the exact failure Phase 135 exists to end.
	resolvedUID := sandboxUID
	if resolvedUID == 0 {
		u, err := user.Lookup("sandbox")
		if err != nil {
			return fmt.Errorf("look up sandbox user for the enforcement predicate: %w", err)
		}
		n, err := strconv.ParseUint(u.Uid, 10, 32)
		if err != nil {
			return fmt.Errorf("parse sandbox uid %q: %w", u.Uid, err)
		}
		resolvedUID = uint32(n)
	}
	logger.Info().Uint32("sandbox_uid", resolvedUID).Str("cgroup_attach", cgroupOverride).
		Msg("enforcement predicate")
```

Add `SandboxUID: resolvedUID,` and `CgroupAttachPath: cgroupOverride,` to the `ebpf.Config` literal. Add `os/user` and `strconv` to the imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/app/cmd/ -run 'TestEbpfAttach' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/cmd/ebpf_attach.go internal/app/cmd/ebpf_attach_predicate_test.go
git commit -m "feat(ebpf-attach): wire --cgroup for real and add --sandbox-uid"
```

---

### Task 5: Userdata stops pinning the enforcer to the scope

**Files:**
- Modify: `pkg/compiler/userdata.go` (the `km-ebpf-enforcer` unit's `--cgroup` line, ~line 5296; and the `km-sandbox-shell` / `km-cgroup.sh` comments, ~5330-5385)
- Create: `pkg/compiler/ebpf_root_cgroup_test.go`
- Regenerate: affected goldens in `pkg/compiler/testdata/`

**Interfaces:**
- Consumes: the `--cgroup` and `--sandbox-uid` flags from Task 4.
- Produces: rendered userdata that attaches at the root cgroup.

- [ ] **Step 1: Write the failing test**

```go
package compiler

import (
	"strings"
	"testing"
)

// The enforcer must no longer pin itself to the per-sandbox scope: since
// Phase 135 it attaches at the root cgroup and selects by uid. Passing the
// scope path here would restore exactly the gap the phase closes — and would
// do it silently, because the box still boots and the unit still runs.
func TestUserdata_EnforcerAttachesAtRootCgroup(t *testing.T) {
	p := testProfileWithEnforcement(t, "both")
	out, err := generateUserData(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "--cgroup /sys/fs/cgroup/km.slice/") {
		t.Error("enforcer is still pinned to the per-sandbox scope; " +
			"interactive sessions never reach it")
	}
	if !strings.Contains(out, "--cgroup /sys/fs/cgroup") {
		t.Error("enforcer must attach at the root cgroup")
	}
}
```

Use whatever profile helper the neighbouring tests in `pkg/compiler` already use for an `enforcement: both` profile — `TestUserDataEnforcementEbpf` in `userdata_test.go:723` shows the established pattern. Do not invent a new helper.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/compiler/ -run TestUserdata_EnforcerAttachesAtRootCgroup -v`
Expected: FAIL — still contains `--cgroup /sys/fs/cgroup/km.slice/`

- [ ] **Step 3: Change the enforcer unit**

Replace the last line of the `ExecStart` block:

```
  --cgroup /sys/fs/cgroup
```

- [ ] **Step 4: Correct the now-misleading shell comments**

`km-sandbox-shell`'s cgroup join and `/etc/profile.d/km-cgroup.sh` have never worked for any interactive session (EPERM, swallowed). Leave the writes in place — they are harmless and they still succeed for anything already inside the scope — but stop the comments claiming they enforce. Replace the comment above `CGROUP_PROCS=` in `km-sandbox-shell` with:

```bash
# Best-effort. This join FAILS with EPERM for every interactive session and
# always has: cgroup v2 requires write access to the common ancestor of the
# source and destination cgroups, which is the root cgroup (root:root 0644).
# Enforcement does not depend on it — since Phase 135 the BPF programs attach
# at the root cgroup and select by uid. Kept only so a caller already inside
# the scope stays there. See docs/operational-gotchas.md.
```

Replace the comment block above the `whoami` test in `/etc/profile.d/km-cgroup.sh` with:

```bash
# Best-effort, and EPERM for every interactive session — see km-sandbox-shell.
# Enforcement does not depend on this; the BPF predicate selects by uid.
```

- [ ] **Step 5: Run the test and regenerate goldens**

Run: `go test ./pkg/compiler/ -run TestUserdata_EnforcerAttachesAtRootCgroup -v`
Expected: PASS

Then: `go test ./pkg/compiler/ 2>&1 | head -40`

Golden failures are expected for every `ebpf`/`both` profile. Regenerate with the sanctioned capture flags used by the neighbouring golden tests. **Do NOT use `CAPTURE_PRE92_BASELINE=1`** — that flag writes unstripped output and corrupts the frozen pre-92 baseline (see `docs/operational-gotchas.md`). If that frozen golden needs the change, hand-patch the two affected lines.

- [ ] **Step 6: Run the full compiler suite**

Run: `go test ./pkg/compiler/`
Expected: `ok` — confirm the exit code, not just the tail of the output.

- [ ] **Step 7: Commit**

```bash
git add pkg/compiler/
git commit -m "feat(userdata): attach the enforcer at the root cgroup"
```

---

### Task 6: Make the spike a permanent integration test

**Files:**
- Modify: `pkg/ebpf/enforcer_integration_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1-4.
- Produces: `TestPredicateEnforcesInteractiveSessions`, the regression test for this whole phase.

- [ ] **Step 1: Write the test**

Add to `enforcer_integration_test.go` (already `//go:build linux && integration`, already root-gated). It is the spike's 8-trigger matrix, minus the marker-IP scaffolding: it runs against a real enforcer, so the profile's own allowlist decides.

```go
// TestPredicateEnforcesInteractiveSessions is the Phase 135 regression test.
//
// Before Phase 135 the programs attached to the per-sandbox scope, which no
// interactive session ever entered — km shell landed in
// system.slice/amazon-ssm-agent.service, ssh in user.slice/user-N.slice, and
// the wrapper's join failed EPERM with the error swallowed. Enforcement
// applied to agent dispatch alone.
//
// The two rows that matter:
//   - sandbox-uid OUTSIDE km.slice must be denied  (the phase's whole point)
//   - root INSIDE km.slice must be denied          (EBPF-NET-12 survives)
//
// A bare uid filter passes the first and fails the second, which is why the
// predicate has two clauses.
func TestPredicateEnforcesInteractiveSessions(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("integration test requires root (uid=0)")
	}
	blocked := blockedTestIP(t) // an IP absent from the allow trie

	cases := []struct {
		name       string
		asSandbox  bool
		inKmSlice  bool
		wantDenied bool
	}{
		{"sandbox outside km.slice (interactive session)", true, false, true},
		{"sandbox inside km.slice (agent dispatch)", true, true, true},
		{"root inside km.slice (EBPF-NET-12)", false, true, true},
		{"root outside km.slice (the box itself)", false, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			denied := dialIsDenied(t, blocked, tc.asSandbox, tc.inKmSlice)
			if denied != tc.wantDenied {
				t.Errorf("denied=%v, want %v", denied, tc.wantDenied)
			}
		})
	}
}
```

Implement `blockedTestIP` and `dialIsDenied` beside it, following the style of the existing `TestRootDirectConnectBlocked` in the same file (which already dials and classifies). `dialIsDenied` runs the dial via `runuser -u sandbox` when `asSandbox`, and inside a forked subshell that writes `$BASHPID` to the scope's `cgroup.procs` when `inKmSlice` — the Phase 132 pattern. Classify `EPERM` ("operation not permitted") and a connect timeout as denied; a completed handshake as allowed. **`connect4` denials surface as `EPERM` immediately, `egress` denials as a silent drop and a timeout** — both count as denied.

- [ ] **Step 2: Verify it compiles**

Run: `GOOS=linux GOARCH=amd64 go vet -tags integration ./pkg/ebpf/`
Expected: no output

It cannot be run locally; Task 8 runs it on a real box.

- [ ] **Step 3: Commit**

```bash
git add pkg/ebpf/enforcer_integration_test.go
git commit -m "test(ebpf): pin interactive-session enforcement and EBPF-NET-12"
```

---

### Task 7: Documentation to the end state

The docs committed with the finding describe the gap as open. They must now describe it as closed, with the honest caveat that existing sandboxes keep the old behaviour until recreated.

**Files:**
- Modify: `docs/operational-gotchas.md` (the canonical section)
- Modify: `docs/vscode.md`, `docs/herdr-remote-attach.md`, `docs/egress-census.md`, `docs/egress-deny-lists.md`
- Modify: `CLAUDE.md` (a Phase 135 block at the top; the Phase 132 correction stays)

- [ ] **Step 1: Rewrite the canonical section**

Retitle to `### Interactive sessions and the eBPF enforcement cgroup`, keep the whole mechanism explanation (it is why the fix looks the way it does), and reframe the entry-path table as *before Phase 135*. State plainly:

- What changed: attach point moved to the root cgroup, selection is now `uid == sandbox_uid || cgroup_id == km_scope_id`.
- Why the cgroup clause exists: EBPF-NET-12 — without it `sudo` becomes a way out.
- **Deploy surface:** `make build` + `make build-lambdas` + `km init --dry-run=false`. NOT `--sidecars` — the enforcer binary ships in the `km` sidecar but the userdata carrying `--cgroup /sys/fs/cgroup` rides in the create-handler zip. **Do not split the deploy:** a new binary with old userdata attaches at the scope and enforces nothing interactive; old binary with new userdata rejects nothing but attaches at the root cgroup with no predicate set — which, because the constants fail open, also enforces nothing. Both halves are quiet.
- **Existing sandboxes keep the old behaviour until `km destroy && km create`.**
- The behaviour change: on a locked profile an interactive session is now subject to the allow-trie for the first time.

- [ ] **Step 2: Update the four referring docs**

Each currently says the allowlist does not apply interactively. Change to: it does, as of Phase 135, on sandboxes created after the deploy. `docs/herdr-remote-attach.md` needs the most care — it went from an outright false claim, to a correction, to this.

- [ ] **Step 3: Add the CLAUDE.md phase block**

Follow the house style of the surrounding phase entries: what shipped, the non-obvious findings (the common-ancestor rule; `bpf_get_socket_uid` being the only uid source in softirq; the two-clause predicate and why; fail-open constants; the cgroup id being an inode read at attach time), the deploy surface, and the recreate requirement. Add a row to the "Where to look" table.

- [ ] **Step 4: Commit**

```bash
git add docs/ CLAUDE.md
git commit -m "docs: interactive sessions are enforced as of Phase 135"
```

---

### Task 8: Live UAT

Nothing in Tasks 1-7 proves this works on a real kernel. The spike proved the mechanism; this proves the shipped wiring.

**Files:**
- Create: `.planning/phases/135-interactive-cgroup-enforcement/135-UAT.md`

- [ ] **Step 1: Build and deploy**

```bash
make build && make build-lambdas
AWS_PROFILE=klanker-application km init --dry-run=false
```

- [ ] **Step 2: Create a locked, on-demand box**

A wide-open profile cannot show enforcement — everything is allowed at every layer, so a passing test proves nothing. Use a profile extending `base/network/locked`, `spot: false`.

- [ ] **Step 3: Assert the programs attach at the root cgroup**

```bash
# via km shell --root or SSM
journalctl -u km-ebpf-enforcer | grep 'enforcement predicate'   # logs uid + attach path
bpftool cgroup tree /sys/fs/cgroup | head                        # four programs at the root
```

- [ ] **Step 4: Run the enforcement matrix**

For a host absent from the profile's allowlist, confirm from a **real `km shell`** and a **real `km herdr` pane** that it is refused — before this phase both connected. Then confirm an allowed host still works from both, and that `km shell` itself, SSM, and the sidecars are unaffected.

- [ ] **Step 5: Run the integration test on the box**

```bash
sudo go test -v -tags integration -run 'TestPredicate|TestRoot' ./pkg/ebpf/
```

Or cross-compile with `go test -c` and ship the binary (the technique in `docs/operational-gotchas.md`), since the box has no Go toolchain.

- [ ] **Step 6: Record and tear down**

Write the transcript to `135-UAT.md` — including anything that failed and what changed as a result. Then `km destroy <id> --remote --yes` and confirm the instance is gone.

- [ ] **Step 7: Commit**

```bash
git add .planning/phases/135-interactive-cgroup-enforcement/
git commit -m "docs(uat): live verification of interactive session enforcement"
```
