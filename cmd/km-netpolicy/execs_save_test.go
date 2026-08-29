package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExecsKey_IsScopedToTheSandboxAndTimestamped(t *testing.T) {
	got := execsKey("sb-abc123", time.Date(2026, 8, 29, 15, 4, 5, 0, time.UTC))
	if !strings.HasPrefix(got, "execs/sb-abc123/") {
		t.Errorf("key must be scoped under the sandbox's own prefix: %q", got)
	}
	if !strings.Contains(got, "20260829T150405Z") {
		t.Errorf("key must carry a timestamp so repeat saves do not overwrite: %q", got)
	}
	if !strings.HasSuffix(got, ".jsonl") {
		t.Errorf("key must keep the store's extension: %q", got)
	}
}

func TestRunExecsSave_MissingBucketIsAClearError(t *testing.T) {
	t.Setenv("KM_ARTIFACTS_BUCKET", "")
	t.Setenv("KM_SANDBOX_ID", "sb-abc123")
	var out, errb bytes.Buffer
	err := runExecsSave(opts{execDir: t.TempDir(), stdout: &out, stderr: &errb})
	if err == nil {
		t.Fatal("want an error when no bucket is configured")
	}
	if !strings.Contains(err.Error(), "KM_ARTIFACTS_BUCKET") {
		t.Errorf("error must name the missing variable, got %v", err)
	}
}

func TestRunExecsSave_MissingSandboxIDIsAClearError(t *testing.T) {
	t.Setenv("KM_ARTIFACTS_BUCKET", "bucket")
	t.Setenv("KM_SANDBOX_ID", "")
	var out, errb bytes.Buffer
	err := runExecsSave(opts{execDir: t.TempDir(), stdout: &out, stderr: &errb})
	if err == nil {
		t.Fatal("want an error when no sandbox id is configured")
	}
	if !strings.Contains(err.Error(), "KM_SANDBOX_ID") {
		t.Errorf("error must name the missing variable, got %v", err)
	}
}

func TestRunExecsSave_MissingStoreIsNotAnError(t *testing.T) {
	// ExecStop runs this on every graceful shutdown, including boxes that
	// executed nothing and so never created a store file at all. A non-zero
	// exit there would make every clean stop look like a failure in the
	// journal.
	t.Setenv("KM_ARTIFACTS_BUCKET", "bucket")
	t.Setenv("KM_SANDBOX_ID", "sb-abc123")
	var out, errb bytes.Buffer
	if err := runExecsSave(opts{execDir: t.TempDir(), stdout: &out, stderr: &errb}); err != nil {
		t.Fatalf("missing store must not error: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to save") {
		t.Errorf("want an explicit nothing-to-save line, got %q", out.String())
	}
}

func TestRunExecsSave_EmptyStoreIsNotAnError(t *testing.T) {
	// A store file that exists but holds zero bytes (e.g. just rotated,
	// or created but never written to) must be treated the same as no store
	// at all — not an error.
	t.Setenv("KM_ARTIFACTS_BUCKET", "bucket")
	t.Setenv("KM_SANDBOX_ID", "sb-abc123")
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/execs.jsonl", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := runExecsSave(opts{execDir: dir, stdout: &out, stderr: &errb}); err != nil {
		t.Fatalf("empty store must not error: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to save") {
		t.Errorf("want an explicit nothing-to-save line, got %q", out.String())
	}
}
