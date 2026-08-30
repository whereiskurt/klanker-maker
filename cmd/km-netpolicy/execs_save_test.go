package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExecsKey_IsScopedToTheSandboxAndTimestamped(t *testing.T) {
	got := execsKey("sb-abc123", time.Date(2026, 8, 29, 15, 4, 5, 0, time.UTC), "")
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

func TestExecsKey_RotatedGenerationIsDistinguishableFromLive(t *testing.T) {
	ts := time.Date(2026, 8, 29, 15, 4, 5, 0, time.UTC)
	live := execsKey("sb-abc123", ts, "")
	rotated := execsKey("sb-abc123", ts, ".1")
	if live == rotated {
		t.Fatalf("live and rotated generations must land at different keys, both got %q", live)
	}
	if !strings.Contains(rotated, ".1") {
		t.Errorf("rotated generation's key must be distinguishable from the live one: %q", rotated)
	}
	if !strings.HasSuffix(rotated, ".jsonl") {
		t.Errorf("rotated key must keep the store's extension: %q", rotated)
	}
}

func TestOpenIfPresent_MissingFileIsNotAnError(t *testing.T) {
	f, size, err := openIfPresent(t.TempDir() + "/execs.jsonl.1")
	if err != nil {
		t.Fatalf("a missing rotated generation must not error: %v", err)
	}
	if f != nil {
		t.Errorf("want a nil file for a missing path, got %v", f)
	}
	if size != 0 {
		t.Errorf("want size 0 for a missing path, got %d", size)
	}
}

func TestOpenIfPresent_ExistingFileReturnsItsSize(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/execs.jsonl.1"
	body := []byte(`{"ts":"2026-08-29T10:00:00Z","kind":"exec","pid":1,"uid":0,"comm":"old","ret":0}` + "\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	f, size, err := openIfPresent(path)
	if err != nil {
		t.Fatalf("openIfPresent: %v", err)
	}
	if f == nil {
		t.Fatal("want a non-nil file for an existing path")
	}
	defer f.Close()
	if size != int64(len(body)) {
		t.Errorf("want size %d, got %d", len(body), size)
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
	// A silent non-zero exit here would show up in the journal (this runs on
	// ExecStop) as nothing but "status=1" — the cause has to reach stderr too,
	// not just the returned error.
	if !strings.Contains(errb.String(), "KM_ARTIFACTS_BUCKET") {
		t.Errorf("want stderr to name the missing variable, got %q", errb.String())
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
	if !strings.Contains(errb.String(), "KM_SANDBOX_ID") {
		t.Errorf("want stderr to name the missing variable, got %q", errb.String())
	}
}

func TestRunExecsSave_UnreadableStoreIsReportedOnStderr(t *testing.T) {
	// Stands in for the "upload could not proceed" shape of failure without
	// needing AWS credentials or a network call: any error that stops the
	// save before a successful upload must be visible on stderr, not just in
	// the returned error, since this runs unattended off ExecStop.
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the permission bits this test relies on")
	}
	t.Setenv("KM_ARTIFACTS_BUCKET", "bucket")
	t.Setenv("KM_SANDBOX_ID", "sb-abc123")
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/execs.jsonl", []byte(`{"ts":"2026-08-29T12:00:00Z"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // let t.TempDir() clean up
	var out, errb bytes.Buffer
	err := runExecsSave(opts{execDir: dir, stdout: &out, stderr: &errb})
	if err == nil {
		t.Fatal("want an error when the store cannot be opened")
	}
	if errb.Len() == 0 {
		t.Fatal("want the failure reported on stderr, got nothing")
	}
	if !strings.Contains(errb.String(), dir) {
		t.Errorf("want stderr to name the store path so an operator can find the kept trace, got %q", errb.String())
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

func TestRunExecsSave_UnreadableRotatedGenerationIsReportedButDoesNotAbort(t *testing.T) {
	// The live generation is present but empty (as it is right after a
	// rotation), and the retained ".1" generation cannot be opened at all.
	// Both ending up with nothing to upload must still be a clean, AWS-free
	// return — this is the branch that would otherwise dial out to a real S3
	// endpoint from a unit test.
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the permission bits this test relies on")
	}
	t.Setenv("KM_ARTIFACTS_BUCKET", "bucket")
	t.Setenv("KM_SANDBOX_ID", "sb-abc123")
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/execs.jsonl", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/execs.jsonl.1", []byte(`{"ts":"2026-08-29T10:00:00Z"}`+"\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir+"/execs.jsonl.1", 0o600) })

	var out, errb bytes.Buffer
	if err := runExecsSave(opts{execDir: dir, stdout: &out, stderr: &errb}); err != nil {
		t.Fatalf("want no error, got %v", err)
	}
	if !strings.Contains(out.String(), "nothing to save") {
		t.Errorf("want the live generation's nothing-to-save line, got %q", out.String())
	}
	if !strings.Contains(errb.String(), "cannot read retained exec store") {
		t.Errorf("want the retained-generation read failure reported on stderr, got %q", errb.String())
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
