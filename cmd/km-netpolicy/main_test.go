package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newEnv(t *testing.T, seed string) (opts, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deny.list")
	if err := os.WriteFile(path, []byte(seed), 0o666); err != nil {
		t.Fatalf("seed deny file: %v", err)
	}
	var out, errBuf bytes.Buffer
	return opts{
		denyFile:    path,
		staticDNS:   []string{"static-dns.example.com"},
		staticHosts: []string{"static-host.example.com"},
		stdout:      &out,
		stderr:      &errBuf,
	}, &out, &errBuf
}

func TestDeny_AppendsPattern(t *testing.T) {
	o, _, errBuf := newEnv(t, "")

	if rc := run([]string{"deny", "evil.example.com"}, o); rc != 0 {
		t.Fatalf("run = %d, want 0; stderr=%s", rc, errBuf)
	}

	body, err := os.ReadFile(o.denyFile)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(body), "evil.example.com") {
		t.Errorf("deny file = %q, want it to contain the pattern", body)
	}
}

func TestDeny_AppendsRatherThanReplacing(t *testing.T) {
	o, _, _ := newEnv(t, "first.example.com\n")

	if rc := run([]string{"deny", "second.example.com"}, o); rc != 0 {
		t.Fatalf("run = %d, want 0", rc)
	}

	body, _ := os.ReadFile(o.denyFile)
	for _, want := range []string{"first.example.com", "second.example.com"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("deny file lost %q: %s", want, body)
		}
	}
}

func TestDeny_MultiplePatternsInOneCall(t *testing.T) {
	o, _, _ := newEnv(t, "")

	if rc := run([]string{"deny", "a.example.com", "b.example.com"}, o); rc != 0 {
		t.Fatalf("run = %d, want 0", rc)
	}

	body, _ := os.ReadFile(o.denyFile)
	for _, want := range []string{"a.example.com", "b.example.com"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("deny file missing %q: %s", want, body)
		}
	}
}

// A malformed pattern must be rejected BEFORE anything is written, so a bad
// argument in a multi-pattern call cannot leave the file half-updated.
func TestDeny_RejectsMalformedWithoutWritingAnything(t *testing.T) {
	o, _, errBuf := newEnv(t, "")

	rc := run([]string{"deny", "good.example.com", "https://bad"}, o)
	if rc == 0 {
		t.Fatal("run = 0, want non-zero for a malformed pattern")
	}
	if !strings.Contains(errBuf.String(), "https://bad") {
		t.Errorf("stderr should name the offending pattern, got %q", errBuf)
	}

	body, _ := os.ReadFile(o.denyFile)
	if strings.Contains(string(body), "good.example.com") {
		t.Errorf("nothing may be written when any pattern is invalid, got %q", body)
	}
}

func TestDeny_RequiresAtLeastOnePattern(t *testing.T) {
	o, _, _ := newEnv(t, "")
	if rc := run([]string{"deny"}, o); rc == 0 {
		t.Error("run = 0, want non-zero when no pattern is given")
	}
}

// An absent file means the profile did not enable runtime narrowing. That has to
// be a clear refusal naming the profile field, not a silently-created file that
// no proxy is reading.
func TestDeny_AbsentFileRefusesWithGuidance(t *testing.T) {
	o, _, errBuf := newEnv(t, "")
	o.denyFile = filepath.Join(t.TempDir(), "missing", "deny.list")

	rc := run([]string{"deny", "evil.example.com"}, o)
	if rc == 0 {
		t.Fatal("run = 0, want non-zero when runtime narrowing is not enabled")
	}
	if !strings.Contains(errBuf.String(), "runtimeDeny") {
		t.Errorf("stderr should name the profile field to set, got %q", errBuf)
	}
	if _, err := os.Stat(o.denyFile); err == nil {
		t.Error("must not create the file itself — the kernel append-only attribute is set at boot")
	}
}

func TestList_ShowsStaticAndRuntimeSeparately(t *testing.T) {
	o, out, _ := newEnv(t, "runtime.example.net\n")

	if rc := run([]string{"list"}, o); rc != 0 {
		t.Fatalf("run = %d, want 0", rc)
	}

	got := out.String()
	for _, want := range []string{"static-dns.example.com", "static-host.example.com", "runtime.example.net"} {
		if !strings.Contains(got, want) {
			t.Errorf("list output missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "runtime") || !strings.Contains(got, "profile") {
		t.Errorf("list must distinguish profile-baked from runtime denies:\n%s", got)
	}
}

func TestList_EmptyRuntimeIsNotAnError(t *testing.T) {
	o, out, _ := newEnv(t, "")
	if rc := run([]string{"list"}, o); rc != 0 {
		t.Fatalf("run = %d, want 0", rc)
	}
	if out.Len() == 0 {
		t.Error("list must still report the profile-baked denies")
	}
}

func TestRun_UnknownVerb(t *testing.T) {
	o, _, _ := newEnv(t, "")
	if rc := run([]string{"widen", "example.com"}, o); rc == 0 {
		t.Error("run = 0, want non-zero for an unknown verb")
	}
}

// There is deliberately no verb that removes an entry. Its absence is the
// narrow-only guarantee at the CLI layer, mirroring chattr +a at the kernel
// layer, so it is worth a test that fails if someone adds one.
func TestRun_HasNoWideningVerb(t *testing.T) {
	o, _, _ := newEnv(t, "evil.example.com\n")

	for _, verb := range []string{"allow", "undeny", "remove", "rm", "delete", "clear", "reset"} {
		if rc := run([]string{verb, "evil.example.com"}, o); rc == 0 {
			t.Errorf("verb %q must not exist — it would widen the policy", verb)
		}
	}
}
