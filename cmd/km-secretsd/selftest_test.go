package main

import (
	"os"
	"path/filepath"
	"testing"
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
	// claude reinstall relocates the real binary.
	if err := os.WriteFile(filepath.Join(shims, "claude"),
		[]byte("#!/bin/sh\nexec /opt/km/bin/km-env exec --as claude -- /gone/claude \"$@\"\n"), 0o755); err != nil {
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
	_ = os.WriteFile(filepath.Join(shims, "claude"),
		[]byte("#!/bin/sh\nexec /opt/km/bin/km-env exec --as claude -- "+target+" \"$@\"\n"), 0o755)
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
