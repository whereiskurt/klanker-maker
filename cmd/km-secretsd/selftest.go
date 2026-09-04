package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

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
// The shim generator (pkg/compiler/userdata.go, section "7.8. Consumer shims")
// bakes the target into a KM_REAL="..." assignment, not the exec line itself
// — the exec line reads "exec ... -- "$KM_REAL" "$@"", a shell variable
// reference, so parsing it directly returns the literal string "$KM_REAL"
// rather than a path. The generator's heredoc is unquoted, so on generation
// KM_REAL="$KM_SHIM_TARGET" is expanded to the real literal path while every
// other "$" in the shim is escaped ("\$") and survives as a literal dollar
// sign for the shim to evaluate later at runtime.
//
// The shim's own fallback branch (baked target has since moved) reassigns
// KM_REAL too, but via a "$(...)" command substitution — its value starts
// with "$", never a path — so that assignment must be skipped, or this
// returns a shell expression instead of a path.
func shimTarget(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `KM_REAL="`) || !strings.HasSuffix(line, `"`) {
			continue
		}
		val := strings.TrimSuffix(strings.TrimPrefix(line, `KM_REAL="`), `"`)
		if strings.HasPrefix(val, "$") {
			// The fallback's command-substitution assignment, not a literal.
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
		status := "ok"
		switch {
		case c.OK:
		case c.Fatal:
			status = "FAIL"
			failed++
		default:
			status = "warn"
		}
		fmt.Fprintf(os.Stderr, "[km-secrets-check] %-16s %-4s %s\n", c.Name, status, c.Detail)
		detail[c.Name] = status
	}
	detail["failed"] = failed
	_ = s.Audit.Emit("secret_selftest", detail)

	if failed > 0 {
		fmt.Fprintf(os.Stderr, "[km-secrets-check] %d fatal check(s) failed: agents would fail\n", failed)
		return 1
	}
	return 0
}
