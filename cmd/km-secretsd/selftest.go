package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

// SelftestResultPath is where runSelftest writes its result for the
// klanker:sandbox self-census skill to read (design spec §7.2) — the third
// of the three places a selftest result lands, alongside the systemd unit's
// own exit status and the secret_selftest audit event. World-readable like
// /opt/km/.km-profile.yaml (Phase 113) for the same reason: the agent's own
// self-census reads it directly, and it never carries a secret value — only
// check names, statuses, and the same key-NAME-only Detail strings the
// audit event already carries.
const SelftestResultPath = "/opt/km/.km-secrets-check.json"

// Check is one selftest assertion.
type Check struct {
	Name   string
	OK     bool
	Fatal  bool // a failed Fatal check aborts the boot
	Detail string
}

// SelftestOpts injects the filesystem and PATH facts so the assertions are
// testable off-box.
type SelftestOpts struct {
	ShimDir    string
	Consumers  []string
	SocketPath string
	// LookPathAs reports what `command -v <consumer>` resolves to for the
	// sandbox user. Nil means run it for real via runuser.
	LookPathAs func(consumer string) (string, error)
}

// Selftest answers one question: will agents fail?
//
// It runs in two places off this one verb. At boot it is called from userdata,
// which runs under set -euo pipefail, so a non-zero exit aborts the boot exactly
// as the Phase 89 sops-decrypt FATAL did. On resume it is called by
// km-secrets-check.service — userdata does not re-run on stop/start, and a
// resumed box can meet a rotated bundle or a revoked grant. There is no boot to
// abort on resume, so the unit simply fails red and emits the audit event.
func (s *Server) Selftest(o SelftestOpts) []Check {
	var checks []Check

	// 1. Ciphertext present, 0400 root, sops-shaped.
	if fi, err := os.Stat(s.CiphertextPath); err != nil {
		checks = append(checks, Check{"ciphertext", false, true, err.Error()})
	} else if perm := fi.Mode().Perm(); perm != 0o400 {
		checks = append(checks, Check{"ciphertext", false, true,
			fmt.Sprintf("%s is %04o, want 0400", s.CiphertextPath, perm)})
	} else {
		checks = append(checks, Check{"ciphertext", true, true, s.CiphertextPath})
	}

	// 2. Socket present with the right mode (skipped when it has not been bound,
	//    e.g. under test).
	if fi, err := os.Stat(o.SocketPath); err == nil {
		ok := fi.Mode().Perm() == 0o660
		checks = append(checks, Check{"socket", ok, true,
			fmt.Sprintf("%s is %04o, want 0660", o.SocketPath, fi.Mode().Perm())})
	}

	// 3. Live end-to-end unseal. NAMES only in the detail, never values.
	bundle, err := LoadBundle(s.CiphertextPath)
	if err != nil {
		checks = append(checks, Check{"unseal", false, true, err.Error()})
	} else {
		names := bundle.Keys()
		bundle.Zero()
		checks = append(checks, Check{"unseal", true, true,
			fmt.Sprintf("%d keys: %s", len(names), strings.Join(names, ", "))})
	}

	// 4 and 5. Per consumer: the shim's target exists and is not itself, and
	//          the shim wins the PATH race for the sandbox user.
	for _, c := range o.Consumers {
		shim := filepath.Join(o.ShimDir, c)
		body, err := os.ReadFile(shim)
		if err != nil {
			// No shim generated. Warn: initCommandsAppend may install the binary
			// later, and because no shim exists nothing is silently broken.
			checks = append(checks, Check{"shim:" + c, false, false,
				"no shim generated (consumer binary absent at boot)"})
			continue
		}

		target := shimTarget(string(body))
		switch {
		case target == "":
			checks = append(checks, Check{"shim:" + c, false, true, "cannot parse shim target"})
			continue
		case target == shim:
			// Catches only a self-referencing BAKED target (KM_REAL= itself is
			// the shim's own path). The shim's actual anti-recursion property at
			// runtime rests on its fallback branch's hardcoded
			// `grep -v '^/opt/km/shims$'` (pkg/compiler/userdata.go) excluding
			// the shim directory from its PATH search — that pattern and
			// secrets.ShimDir (o.ShimDir here) must always name the same path.
			// This check cannot see a runtime fallback resolution back to the
			// shim; it only pins the boot-time literal.
			checks = append(checks, Check{"shim:" + c, false, true, "shim targets itself: would recurse"})
			continue
		}
		if _, err := os.Stat(target); err != nil {
			checks = append(checks, Check{"shim:" + c, false, true,
				fmt.Sprintf("target %s missing: %v", target, err)})
			continue
		}
		checks = append(checks, Check{"shim:" + c, true, true, target})

		resolved, err := resolveForSandbox(o, c)
		if err != nil {
			checks = append(checks, Check{"path:" + c, false, true, err.Error()})
			continue
		}
		if resolved != shim {
			checks = append(checks, Check{"path:" + c, false, true,
				fmt.Sprintf("%s resolves to %s, not %s: the shim lost the PATH race and "+
					"%s would run with no secrets", c, resolved, shim, c)})
			continue
		}
		checks = append(checks, Check{"path:" + c, true, true, resolved})
	}

	return checks
}

// shimTarget extracts the absolute path a generated shim execs.
//
// The shim generator (pkg/compiler/userdata.go, section "7.8. Consumer shims",
// which itself names shimTarget as this line's parser) bakes the target into a
// KM_REAL="..." assignment at COLUMN 0, not the exec line itself — the exec
// line reads "exec ... -- "$KM_REAL" "$@"", a shell variable reference, so
// parsing it directly returns the literal string "$KM_REAL" rather than a
// path. The generator's heredoc is unquoted, so on generation
// KM_REAL="$KM_SHIM_TARGET" is expanded to the real literal path while every
// other "$" in the shim is escaped ("\$") and survives as a literal dollar
// sign for the shim to evaluate later at runtime.
//
// The shim's own fallback branch (baked target has since moved) reassigns
// KM_REAL too, but that reassignment lives inside "if ... fi" and is
// INDENTED — anchoring on column 0 excludes it structurally, which is more
// robust than matching on its value (a "$(...)" command substitution) alone,
// since a future edit could change what that value looks like without
// changing where the line sits. The "$"-prefix check below is kept as a
// second, defensive line, not the primary rule.
func shimTarget(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, `KM_REAL="`) || !strings.HasSuffix(line, `"`) {
			continue
		}
		val := strings.TrimSuffix(strings.TrimPrefix(line, `KM_REAL="`), `"`)
		if strings.HasPrefix(val, "$") {
			// Defensive: even at column 0, a value that is itself a shell
			// expansion is never a literal path.
			continue
		}
		return val
	}
	return ""
}

func resolveForSandbox(o SelftestOpts, consumer string) (string, error) {
	if o.LookPathAs != nil {
		return o.LookPathAs(consumer)
	}
	// A login shell, because that is exactly how dispatch_as_sandbox invokes it.
	out, err := exec.Command("runuser", "-u", "sandbox", "--",
		"bash", "-lc", "command -v "+consumer).Output()
	if err != nil {
		return "", fmt.Errorf("resolving %s as the sandbox user: %w", consumer, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func runSelftest(s *Server) int {
	// secrets.DefaultConsumers is a package-level var. Never reslice it and
	// append — consumers[:0] aliases its backing array, so the appends would
	// overwrite the package's own defaults for the rest of the process. Always
	// build a fresh slice.
	consumers := secrets.DefaultConsumers
	if len(s.Grants) > 0 {
		consumers = make([]string, 0, len(s.Grants))
		for c := range s.Grants {
			consumers = append(consumers, c)
		}
		sort.Strings(consumers) // deterministic check order and audit output
	}

	checks := s.Selftest(SelftestOpts{
		ShimDir:    secrets.ShimDir,
		Consumers:  consumers,
		SocketPath: secrets.SocketPath,
	})

	failed := 0
	detail := map[string]any{}
	for _, c := range checks {
		status := checkStatus(c)
		if status == "FAIL" {
			failed++
		}
		fmt.Fprintf(os.Stderr, "[km-secrets-check] %-16s %-4s %s\n", c.Name, status, c.Detail)
		detail[c.Name] = status
	}
	detail["failed"] = failed
	_ = s.Audit.Emit("secret_selftest", detail)

	// Third destination per the design spec (§7.2). Best-effort: see
	// writeSelftestResult — a report file that could not be written must
	// never fail the selftest itself.
	writeSelftestResult(SelftestResultPath, checks)

	if failed > 0 {
		fmt.Fprintf(os.Stderr, "[km-secrets-check] %d fatal check(s) failed: agents would fail\n", failed)
		return 1
	}
	return 0
}

// checkStatus renders a Check's pass/fail/warn state as the single word used
// both on the stderr report line and in the result artifacts (audit detail,
// the result file below).
func checkStatus(c Check) string {
	switch {
	case c.OK:
		return "ok"
	case c.Fatal:
		return "FAIL"
	default:
		return "warn"
	}
}

// selftestResult is the shape written to SelftestResultPath.
type selftestResult struct {
	Timestamp string                `json:"timestamp"`
	Failed    int                   `json:"failed"`
	Checks    []selftestResultCheck `json:"checks"`
}

type selftestResultCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// writeSelftestResult writes the third of the selftest's three result
// destinations (design spec §7.2), for the klanker:sandbox self-census skill
// to read directly rather than shelling out to re-run the selftest itself.
//
// BEST-EFFORT BY CONSTRUCTION: an empty path (the zero value tests get by
// default) is a no-op, and any write or chown failure — the parent directory
// missing being the expected case off-box — is swallowed rather than
// propagated. Failing a boot because a convenience report file could not be
// written would be absurd; the boot-abort mechanism is runSelftest's exit
// code, not this file.
//
// Carries only check NAMES, statuses, and each check's existing Detail
// string — the same key-NAME-only rule the audit event and the unseal
// check's own Detail already follow. Nothing here ever sees a secret value.
func writeSelftestResult(path string, checks []Check) {
	if path == "" {
		return
	}

	out := selftestResult{Timestamp: time.Now().UTC().Format(time.RFC3339)}
	for _, c := range checks {
		status := checkStatus(c)
		if status == "FAIL" {
			out.Failed++
		}
		out.Checks = append(out.Checks, selftestResultCheck{Name: c.Name, Status: status, Detail: c.Detail})
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	// 0644: world-readable like /opt/km/.km-profile.yaml (Phase 113) — the
	// sandbox user's own self-census skill reads this directly, and it
	// carries nothing more sensitive than that skill already gathers itself.
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return
	}
	chownToSandboxUser(path)
}

// chownToSandboxUser gives the result file to sandbox:sandbox, mirroring the
// Phase 113 precedent for /opt/km/.km-profile.yaml. Best-effort: a dev
// machine or test sandbox with no "sandbox" user, or a non-root process,
// just leaves the file root-owned.
func chownToSandboxUser(path string) {
	u, err := user.Lookup("sandbox")
	if err != nil {
		return
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return
	}
	_ = os.Chown(path, uid, gid)
}
