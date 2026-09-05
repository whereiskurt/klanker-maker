package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

// opts builds the UNBOUND-BY-DESIGN case: SocketPath "" means this caller
// never binds a socket, so the socket assertion is skipped and assertion 3
// falls back to an in-process LoadBundle. It is the one and only skip the
// selftest allows, and it is explicit rather than ambient — a socket path that
// merely does not exist is FATAL (see TestSelftest_DeadBrokerIsFatal), because
// that is exactly what a broker that failed to start looks like.
func opts(t *testing.T, shimDir string, consumers []string, resolve func(string) (string, error)) SelftestOpts {
	t.Helper()
	return SelftestOpts{
		ShimDir:    shimDir,
		Consumers:  consumers,
		SocketPath: "",
		SocketWait: 200 * time.Millisecond,
		LookPathAs: resolve,
	}
}

// serveOnSocket starts a real broker on a fresh unix socket and returns its
// path, chmod'ed 0660 exactly as km-secretsd.service's ExecStartPost does.
// Callers get the HEALTHY case: a live daemon that answers the protocol.
func serveOnSocket(t *testing.T, s *Server) string {
	t.Helper()
	// Unix socket paths are capped near 104 bytes on darwin; t.TempDir() under
	// a long test name can exceed that, so use a short base.
	dir, err := os.MkdirTemp("", "kmsd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sock, 0o660); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Serve(ctx, ln) }()
	t.Cleanup(func() { cancel(); <-done })
	return sock
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
	if got := runSelftest(s, ""); got == 0 {
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
	if got := runSelftest(s, ""); got != 0 {
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
	_ = runSelftest(s, "")

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

	_ = runSelftest(s, "")

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

func TestWriteSelftestResult_WritesExpectedShapeWithoutLeakingValues(t *testing.T) {
	// The third of the design spec's three result destinations (§7.2): a
	// file the klanker:sandbox self-census skill reads directly.
	stubDecrypt(t, "API_KEY: supersecret\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}
	checks := s.Selftest(opts(t, t.TempDir(), nil, nil))

	out := filepath.Join(t.TempDir(), "result.json")
	writeSelftestResult(out, checks)

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("result file not written: %v", err)
	}
	if contains(string(data), "supersecret") {
		t.Error("selftest result file leaked a secret VALUE")
	}
	if !contains(string(data), "API_KEY") {
		t.Error("result file should name the key")
	}

	var parsed struct {
		Timestamp string `json:"timestamp"`
		Failed    int    `json:"failed"`
		Checks    []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("result file is not valid JSON: %v", err)
	}
	if parsed.Timestamp == "" {
		t.Error("result file missing a timestamp")
	}
	var sawUnseal bool
	for _, c := range parsed.Checks {
		if c.Name == "unseal" {
			sawUnseal = true
			if c.Status != "ok" {
				t.Errorf("unseal status = %q, want ok", c.Status)
			}
		}
	}
	if !sawUnseal {
		t.Errorf("result file missing the unseal check: %+v", parsed)
	}
}

func TestWriteSelftestResult_EmptyPathIsNoop(t *testing.T) {
	// The zero value every prior Selftest-only test gets (they never set
	// ResultPath) — must stay side-effect free, no panic, no I/O attempted.
	writeSelftestResult("", []Check{{Name: "x", OK: true, Fatal: true}})
}

func TestWriteSelftestResult_MissingDirectoryIsBestEffort(t *testing.T) {
	// The expected off-box shape of failure — a report file must never fail
	// the selftest itself.
	writeSelftestResult(filepath.Join(t.TempDir(), "no-such-dir", "result.json"),
		[]Check{{Name: "x", OK: true, Fatal: true}})
}

// --- Finding 2: the socket check must be able to FAIL ----------------------

// TestSelftest_DeadBrokerIsFatal is the whole point of the socket assertion.
//
// A broker that never started — bad KM_SECRETS_GRANTS JSON (main exits 1), a
// bind failure, any crash — leaves no socket. An earlier revision appended the
// socket Check only when os.Stat SUCCEEDED, so this exact case was silently
// omitted rather than failed, and the boot printed "secrets self-test passed"
// over a permanently dead broker while every agent turn failed at km-env
// connect time.
func TestSelftest_DeadBrokerIsFatal(t *testing.T) {
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	o := opts(t, t.TempDir(), nil, nil)
	o.SocketPath = filepath.Join(t.TempDir(), "never-bound.sock")

	start := time.Now()
	checks := s.Selftest(o)
	if waited := time.Since(start); waited > 5*time.Second {
		t.Errorf("socket check waited %v, far past its %v bound", waited, o.SocketWait)
	}

	c := find(checks, "socket")
	if c == nil {
		t.Fatal("no socket check ran at all — a dead broker must FAIL, never be skipped")
	}
	if c.OK || !c.Fatal {
		t.Errorf("socket check = %+v, want failed and fatal", c)
	}

	// And assertion 3 must not paper over it by decrypting in-process: with a
	// socket path set, the unseal goes over the wire and there is nobody there.
	u := find(checks, "unseal")
	if u == nil || u.OK {
		t.Errorf("unseal check = %+v, want failure — a dead broker cannot serve an unseal", u)
	}
}

// TestSelftest_WrongSocketModeIsFatal: 0666 would let any local uid speak the
// protocol, not just the sandbox user.
func TestSelftest_WrongSocketModeIsFatal(t *testing.T) {
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	sock := serveOnSocket(t, s)
	if err := os.Chmod(sock, 0o666); err != nil {
		t.Fatal(err)
	}
	o := opts(t, t.TempDir(), nil, nil)
	o.SocketPath = sock

	c := find(s.Selftest(o), "socket")
	if c == nil || c.OK || !c.Fatal {
		t.Errorf("socket check = %+v, want failed and fatal on a 0666 socket", c)
	}
}

// TestSelftest_HealthyBrokerPassesEndToEnd proves the positive half: against a
// LIVE daemon the socket check passes and the unseal really does travel over
// the wire — the same protocol km-env speaks — rather than being decrypted
// in-process. Key NAMES only, never values.
func TestSelftest_HealthyBrokerPassesEndToEnd(t *testing.T) {
	stubDecrypt(t, "API_KEY: supersecret\nOTHER: v\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	o := opts(t, t.TempDir(), nil, nil)
	o.SocketPath = serveOnSocket(t, s)

	checks := s.Selftest(o)

	if c := find(checks, "socket"); c == nil || !c.OK {
		t.Errorf("socket check = %+v, want OK against a live 0660 socket", c)
	}
	u := find(checks, "unseal")
	if u == nil || !u.OK {
		t.Fatalf("unseal check = %+v, want OK against a live broker", u)
	}
	if !strings.Contains(u.Detail, "API_KEY") || !strings.Contains(u.Detail, "OTHER") {
		t.Errorf("unseal detail should name every key, got %q", u.Detail)
	}
	if strings.Contains(u.Detail, "supersecret") {
		t.Error("selftest detail leaked a secret VALUE")
	}
}

// TestRunSelftest_DeadBrokerExitsNonZero is the boot-abort path: under
// `set -euo pipefail` in userdata §7.9 a non-zero exit stops the boot rather
// than letting a half-working box come up and fail at its first turn.
func TestRunSelftest_DeadBrokerExitsNonZero(t *testing.T) {
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	if got := runSelftest(s, filepath.Join(t.TempDir(), "never-bound.sock")); got == 0 {
		t.Error("runSelftest() = 0 with no broker listening: the boot would proceed onto a dead broker")
	}
}

// TestRunSelftest_HealthyBrokerExitsZero is the same verb's positive control —
// without it the test above would also pass if the verb simply always failed.
func TestRunSelftest_HealthyBrokerExitsZero(t *testing.T) {
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	// No Grants ⇒ secrets.DefaultConsumers against the real secrets.ShimDir,
	// which does not exist off-box: that degrades to non-fatal warnings.
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	if got := runSelftest(s, serveOnSocket(t, s)); got != 0 {
		t.Errorf("runSelftest() = %d against a live broker, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// Assertion 6 — the fence (Phase 133 Wave 2)
// ---------------------------------------------------------------------------

// fenceSelftestServer builds a Server whose bundle decrypts cleanly, so the only
// thing under test is the fence assertion.
func fenceSelftestServer(t *testing.T, fenceEnabled bool) *Server {
	t.Helper()
	stubDecrypt(t, "API_KEY: v\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	return &Server{CiphertextPath: p, Audit: NopAudit{}, FenceEnabled: fenceEnabled}
}

// No fence, no assertion: a profile that never opted in must not gain a check it
// can only fail.
func TestSelftest_NoFenceCheckWhenFenceOff(t *testing.T) {
	s := fenceSelftestServer(t, false)
	o := opts(t, t.TempDir(), nil, nil)
	o.FenceProbe = func() (bool, bool, bool, string) { return true, true, true, "unused" }
	if c := find(s.Selftest(o), "fence"); c != nil {
		t.Errorf("a fence check appeared with the fence off: %+v", c)
	}
}

func TestSelftest_FencePassesWhenAllThreeClausesHold(t *testing.T) {
	s := fenceSelftestServer(t, true)
	o := opts(t, t.TempDir(), nil, nil)
	o.FenceProbe = func() (bool, bool, bool, string) { return true, true, true, "all good" }
	c := find(s.Selftest(o), "fence")
	if c == nil {
		t.Fatal("no fence check with the fence on")
	}
	if !c.OK {
		t.Fatalf("fence check failed with every clause holding: %s", c.Detail)
	}
}

// Each clause must be able to fail the check ALONE, and the detail must name
// which one — a fence failure reading only "fence: FAIL" costs an operator an
// hour. Clause 3 is the negative control: the narrowed credentials must FAIL to
// decrypt, and nothing else in the system can detect it when they do not.
func TestSelftest_EachFenceClauseFailsIndependently(t *testing.T) {
	cases := []struct {
		name              string
		imds, sts, denied bool
		wantDetail        string
	}{
		{"imds still reachable", false, true, true, "imds"},
		{"helpers broken", true, false, true, "sts:getcalleridentity"},
		{"narrowed creds still decrypt", true, true, false, "decrypt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := fenceSelftestServer(t, true)
			o := opts(t, t.TempDir(), nil, nil)
			o.FenceProbe = func() (bool, bool, bool, string) {
				return tc.imds, tc.sts, tc.denied, "probe detail"
			}
			c := find(s.Selftest(o), "fence")
			if c == nil {
				t.Fatal("no fence check")
			}
			if c.OK {
				t.Fatal("fence check passed with a clause failing")
			}
			if !c.Fatal {
				t.Error("fence check is not fatal; a broken fence must abort the boot")
			}
			if !strings.Contains(strings.ToLower(c.Detail), tc.wantDetail) {
				t.Errorf("detail %q does not name the failing clause (%q)", c.Detail, tc.wantDetail)
			}
		})
	}
}

// roleARNFromCallerARN is exercised in credentials_test.go; this pins that the
// fence check reaches runFenceProbe when no probe is injected, rather than
// silently passing. Running the real probe off-box fails (no runuser, no
// sandbox user), which is the correct outcome and what this asserts.
func TestSelftest_FenceWithNoInjectedProbeRunsTheRealOne(t *testing.T) {
	s := fenceSelftestServer(t, true)
	c := find(s.Selftest(opts(t, t.TempDir(), nil, nil)), "fence")
	if c == nil {
		t.Fatal("no fence check ran with a nil FenceProbe: the real probe was skipped")
	}
	if c.OK {
		t.Error("the real probe reported a healthy fence on a dev machine with no " +
			"sandbox user and no iptables; it cannot have actually run")
	}
}
