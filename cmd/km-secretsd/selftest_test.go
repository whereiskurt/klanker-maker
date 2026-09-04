package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

func opts(t *testing.T, shimDir string, consumers []string, resolve func(string) (string, error)) SelftestOpts {
	t.Helper()
	return SelftestOpts{
		ShimDir:    shimDir,
		Consumers:  consumers,
		SocketPath: filepath.Join(t.TempDir(), "s.sock"),
		LookPathAs: resolve,
	}
}

func find(checks []Check, name string) *Check {
	for i := range checks {
		if checks[i].Name == name {
			return &checks[i]
		}
	}
	return nil
}

// realShimTemplate is the EXACT text the shim generator writes
// (pkg/compiler/userdata.go, section "7.8. Consumer shims"), byte-for-byte,
// with the consumer name and the baked target as the only variables. Tests
// exercise this literal artifact — not an idealized stand-in — so shimTarget
// and the generator cannot silently drift apart the way they did once
// already (a Task 7 fixture that didn't match Task 8's real output).
const realShimTemplate = `#!/bin/sh
# Phase 133: exec an ABSOLUTE path, never the bare name — resolving by name here
# would re-find this shim on PATH and recurse. If the baked target has since
# moved (userdata re-runs Claude's install.cjs idempotently), fall back to a
# PATH search with the shim directory removed.
# NOTE: km-secretsd selftest (shimTarget) parses this KM_REAL= line to verify the target exists. Keep the literal path here.
KM_REAL="%s"
if [ ! -x "$KM_REAL" ]; then
  KM_REAL="$(PATH="$(echo "$PATH" | tr ':' '\n' | grep -v '^/opt/km/shims$' | paste -sd: -)" command -v %s 2>/dev/null)"
fi
[ -x "$KM_REAL" ] || { echo "km-shim: cannot locate the real %s" >&2; exit 127; }
exec /opt/km/bin/km-env exec --as %s -- "$KM_REAL" "$@"
`

// realShim renders realShimTemplate for consumer with target baked into the
// KM_REAL= assignment, matching what a real boot writes to
// /opt/km/shims/<consumer>.
func realShim(consumer, target string) string {
	return fmt.Sprintf(realShimTemplate, target, consumer, consumer, consumer)
}

func TestSelftest_LiveUnsealFailureIsFatal(t *testing.T) {
	// The class that aborts boot today: KMS 403, wrong alias, missing grant.
	stubDecrypt(t, "", errDecryptStub)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	c := find(s.Selftest(opts(t, t.TempDir(), nil, nil)), "unseal")
	if c == nil {
		t.Fatal("no unseal check ran")
	}
	if c.OK || !c.Fatal {
		t.Errorf("unseal check = %+v, want failed and fatal", c)
	}
}

func TestSelftest_ReportsKeyNamesNeverValues(t *testing.T) {
	stubDecrypt(t, "API_KEY: supersecret\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	c := find(s.Selftest(opts(t, t.TempDir(), nil, nil)), "unseal")
	if !c.OK {
		t.Fatalf("unseal check failed: %s", c.Detail)
	}
	if !contains(c.Detail, "API_KEY") {
		t.Errorf("detail should name the key, got %q", c.Detail)
	}
	if contains(c.Detail, "supersecret") {
		t.Error("selftest detail leaked a secret VALUE")
	}
}

func TestSelftest_ShimPointingAtMissingTargetIsFatal(t *testing.T) {
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	shims := t.TempDir()
	// A shim whose baked target no longer exists — the stale-path case after a
	// claude reinstall relocates the real binary. This is the REAL rendered
	// shim format (realShimTemplate), not a simplified stand-in.
	if err := os.WriteFile(filepath.Join(shims, "claude"),
		[]byte(realShim("claude", "/gone/claude")), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	c := find(s.Selftest(opts(t, shims, []string{"claude"}, nil)), "shim:claude")
	if c == nil || c.OK || !c.Fatal {
		t.Errorf("shim check = %+v, want failed and fatal", c)
	}
}

func TestSelftest_ShimTargetingItselfIsFatal(t *testing.T) {
	// A shim whose baked KM_REAL is the shim's own path would recurse forever
	// if ever exec'd.
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	shims := t.TempDir()
	self := filepath.Join(shims, "claude")
	if err := os.WriteFile(self, []byte(realShim("claude", self)), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	c := find(s.Selftest(opts(t, shims, []string{"claude"}, nil)), "shim:claude")
	if c == nil || c.OK || !c.Fatal {
		t.Errorf("shim check = %+v, want failed and fatal", c)
	}
}

func TestSelftest_ShimLosingThePATHRaceIsFatal(t *testing.T) {
	// THE highest-value assertion. If nvm's bin dir wins, claude runs with no
	// key and dies on a 401, and nothing else in the system notices.
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	shims := t.TempDir()
	target := filepath.Join(t.TempDir(), "claude")
	_ = os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755)
	_ = os.WriteFile(filepath.Join(shims, "claude"), []byte(realShim("claude", target)), 0o755)
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	// Resolution returns nvm's copy, not the shim: the race is lost.
	lost := s.Selftest(opts(t, shims, []string{"claude"}, func(string) (string, error) {
		return "/home/sandbox/.nvm/versions/node/v22/bin/claude", nil
	}))
	c := find(lost, "path:claude")
	if c == nil || c.OK || !c.Fatal {
		t.Errorf("path check = %+v, want failed and fatal", c)
	}

	// Resolution returns the shim: the race is won.
	won := s.Selftest(opts(t, shims, []string{"claude"}, func(string) (string, error) {
		return filepath.Join(shims, "claude"), nil
	}))
	if c := find(won, "path:claude"); c == nil || !c.OK {
		t.Errorf("path check = %+v, want OK when the shim resolves first", c)
	}
}

func TestSelftest_GrantedConsumerWithNoBinaryWarnsNotFails(t *testing.T) {
	// initCommandsAppend may install it later; no shim is generated, so nothing
	// is silently broken.
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	s := &Server{CiphertextPath: p, Grants: map[string][]string{"latertool": {"A"}}, Audit: NopAudit{}}

	c := find(s.Selftest(opts(t, t.TempDir(), []string{"latertool"}, nil)), "shim:latertool")
	if c == nil {
		t.Fatal("no check for the ungenerated consumer")
	}
	if c.Fatal {
		t.Errorf("check = %+v, want non-fatal warning", c)
	}
}

func TestSelftest_CiphertextPermissionsChecked(t *testing.T) {
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = os.WriteFile(p, []byte("sops: {}\n"), 0o644) // world-readable
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	c := find(s.Selftest(opts(t, t.TempDir(), nil, nil)), "ciphertext")
	if c == nil || c.OK {
		t.Errorf("ciphertext check = %+v, want failure on 0644", c)
	}
}

func TestShimTarget_ParsesTheRealGeneratorOutput(t *testing.T) {
	// Pins shimTarget against the actual artifact the shim generator
	// (pkg/compiler/userdata.go) writes, not an idealized stand-in — the
	// defect this fixes was exactly that mismatch.
	body := realShim("claude", "/home/sandbox/.nvm/versions/node/v22.14.0/bin/claude")
	if got := shimTarget(body); got != "/home/sandbox/.nvm/versions/node/v22.14.0/bin/claude" {
		t.Errorf("shimTarget() = %q, want the baked KM_REAL literal", got)
	}
}

func TestShimTarget_SkipsTheFallbackCommandSubstitution(t *testing.T) {
	// The fallback branch also assigns KM_REAL, but to a "$(...)" expression,
	// not a literal — shimTarget must never return that as if it were a path.
	body := realShim("codex", "/opt/codex/bin/codex")
	if got := shimTarget(body); got == "" || got[0] == '$' {
		t.Errorf("shimTarget() = %q, picked up the fallback command substitution instead of the literal", got)
	}
}

func TestShimTarget_MalformedBodyReturnsEmpty(t *testing.T) {
	for _, body := range []string{
		"",
		"#!/bin/sh\necho not a shim\n",
		"#!/bin/sh\nexec /opt/km/bin/km-env exec --as claude -- \"$KM_REAL\" \"$@\"\n", // exec line, no KM_REAL= assignment at all
	} {
		if got := shimTarget(body); got != "" {
			t.Errorf("shimTarget(%q) = %q, want empty for an unparseable shim", body, got)
		}
	}
}

func TestRunSelftest_ExitsNonZeroOnFatalFailure(t *testing.T) {
	// Ciphertext missing entirely: the "ciphertext" check is fatal, so the
	// verb must fail loudly — this is the boot-abort path under
	// set -euo pipefail.
	s := &Server{CiphertextPath: filepath.Join(t.TempDir(), "absent.enc.yaml"), Audit: NopAudit{}}
	if got := runSelftest(s); got == 0 {
		t.Errorf("runSelftest() = %d, want non-zero when a fatal check fails", got)
	}
}

func TestRunSelftest_ExitsZeroWhenChecksPassOrWarnOnly(t *testing.T) {
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	// No Grants ⇒ the default consumer set (secrets.DefaultConsumers) is used
	// against the real secrets.ShimDir, which does not exist on this dev/CI
	// machine — that degrades to a non-fatal "no shim generated" warning per
	// consumer, not a failure.
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}
	if got := runSelftest(s); got != 0 {
		t.Errorf("runSelftest() = %d, want 0 when every check passes or only warns", got)
	}
}

func TestRunSelftest_GrantsProduceConsumerListWithoutMutatingDefaults(t *testing.T) {
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	before := append([]string(nil), secrets.DefaultConsumers...)

	aud := &recordingAudit{}
	s := &Server{CiphertextPath: p, Grants: map[string][]string{"latertool": {"A"}}, Audit: aud}
	_ = runSelftest(s)

	if len(aud.events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(aud.events))
	}
	detail := aud.events[0]
	if _, ok := detail["shim:latertool"]; !ok {
		t.Errorf("expected a shim:latertool check driven by Grants, got %v", detail)
	}
	if _, ok := detail["shim:claude"]; ok {
		t.Errorf("runSelftest used secrets.DefaultConsumers instead of the grants map: %v", detail)
	}
	// Never resliced-and-appended from the package var: DefaultConsumers must
	// come back exactly as it went in.
	if !reflect.DeepEqual([]string(secrets.DefaultConsumers), before) {
		t.Errorf("runSelftest mutated secrets.DefaultConsumers: got %v, want %v", secrets.DefaultConsumers, before)
	}
}

func TestRunSelftest_EmitsSecretSelftestAuditEvent(t *testing.T) {
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	aud := &recordingAudit{}
	s := &Server{CiphertextPath: p, Audit: aud}

	_ = runSelftest(s)

	if len(aud.events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(aud.events))
	}
	detail := aud.events[0]
	if detail["event_type"] != "secret_selftest" {
		t.Errorf("audit event type = %v, want secret_selftest", detail["event_type"])
	}
	if _, ok := detail["unseal"]; !ok {
		t.Errorf("audit detail missing the unseal check status: %v", detail)
	}
	if _, ok := detail["failed"]; !ok {
		t.Errorf("audit detail missing the failed count: %v", detail)
	}
}
