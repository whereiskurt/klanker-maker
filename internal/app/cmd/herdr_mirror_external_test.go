package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

// TestFetchAndUploadHerdr exercises the fetchAndUploadHerdr helper using PATH
// shims (executable shell scripts named aws/curl) so tests run without real
// network access or AWS credentials.
//
// Pattern mirrors TestFetchAndUploadSops.
func TestFetchAndUploadHerdr(t *testing.T) {
	t.Run("UsesCacheWhenPresent", func(t *testing.T) {
		buildDir := t.TempDir()
		shimDir := t.TempDir()

		// Pre-create build/herdr to simulate a cached binary.
		binaryPath := filepath.Join(buildDir, "herdr")
		if err := os.WriteFile(binaryPath, []byte("fakeherdrbinary"), 0o755); err != nil {
			t.Fatalf("pre-create herdr: %v", err)
		}

		// aws shim: records invocation args to a log file; exits 0.
		logFile := filepath.Join(t.TempDir(), "aws-calls.log")
		awsShim := "#!/bin/sh\necho \"$@\" >> \"" + logFile + "\"\n"
		if err := os.WriteFile(filepath.Join(shimDir, "aws"), []byte(awsShim), 0o755); err != nil {
			t.Fatalf("write aws shim: %v", err)
		}
		// curl shim: should NOT be called when cache is present.
		curlShim := "#!/bin/sh\necho 'curl called unexpectedly' >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(shimDir, "curl"), []byte(curlShim), 0o755); err != nil {
			t.Fatalf("write curl shim: %v", err)
		}

		t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		if err := cmd.FetchAndUploadHerdr(buildDir, "fake-bucket"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify aws was called exactly once with s3 cp, binaries/herdr, and
		// --profile klanker-terraform.
		logContent, err := os.ReadFile(logFile)
		if err != nil {
			t.Fatalf("read aws log: %v", err)
		}
		calls := strings.TrimSpace(string(logContent))
		if calls == "" {
			t.Fatal("expected one aws s3 cp call but aws shim was not invoked")
		}
		lines := strings.Split(calls, "\n")
		if len(lines) != 1 {
			t.Errorf("expected exactly 1 aws invocation, got %d: %q", len(lines), calls)
		}
		if !strings.Contains(calls, "s3 cp") {
			t.Errorf("expected aws s3 cp call, got: %q", calls)
		}
		if !strings.Contains(calls, "binaries/herdr") {
			t.Errorf("expected binaries/herdr in upload target, got: %q", calls)
		}
		if !strings.Contains(calls, "--profile klanker-terraform") {
			t.Errorf("expected --profile klanker-terraform, got: %q", calls)
		}

		// The cached file must survive the (successful) upload path too —
		// this is the no-delete-on-failure property the original test was
		// protecting, still worth asserting here.
		if _, statErr := os.Stat(binaryPath); statErr != nil {
			t.Fatalf("cached binary was removed: %v", statErr)
		}
	})

	t.Run("DownloadFailureReturnsError", func(t *testing.T) {
		buildDir := t.TempDir()
		shimDir := t.TempDir()

		// curl shim exits 1 to simulate download failure.
		curlShim := "#!/bin/sh\necho 'download failed' >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(shimDir, "curl"), []byte(curlShim), 0o755); err != nil {
			t.Fatalf("write curl shim: %v", err)
		}
		// aws shim: should NOT be called if curl fails.
		logFile := filepath.Join(t.TempDir(), "aws-calls.log")
		awsShim := "#!/bin/sh\necho \"$@\" >> \"" + logFile + "\"\n"
		if err := os.WriteFile(filepath.Join(shimDir, "aws"), []byte(awsShim), 0o755); err != nil {
			t.Fatalf("write aws shim: %v", err)
		}

		t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		err := cmd.FetchAndUploadHerdr(buildDir, "fake-bucket")
		if err == nil {
			t.Fatal("expected error from curl failure, got nil")
		}
		if !strings.Contains(err.Error(), "download herdr") {
			t.Errorf("expected error to mention %q, got: %v", "download herdr", err)
		}

		// aws should NOT have been called.
		if _, statErr := os.Stat(logFile); statErr == nil {
			logContent, _ := os.ReadFile(logFile)
			if strings.TrimSpace(string(logContent)) != "" {
				t.Errorf("aws was called after curl failure, expected no aws call: %q", string(logContent))
			}
		}
	})

	t.Run("UploadFailureReturnsError", func(t *testing.T) {
		buildDir := t.TempDir()
		shimDir := t.TempDir()

		// Pre-stage build/herdr so the download path is skipped.
		binaryPath := filepath.Join(buildDir, "herdr")
		if err := os.WriteFile(binaryPath, []byte("fakeherdrbinary"), 0o755); err != nil {
			t.Fatalf("pre-create herdr: %v", err)
		}

		// aws shim exits 1 to simulate upload failure.
		awsShim := "#!/bin/sh\necho 'upload failed' >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(shimDir, "aws"), []byte(awsShim), 0o755); err != nil {
			t.Fatalf("write aws shim: %v", err)
		}

		t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		err := cmd.FetchAndUploadHerdr(buildDir, "fake-bucket")
		if err == nil {
			t.Fatal("expected error from aws upload failure, got nil")
		}
		if !strings.Contains(err.Error(), "upload herdr") {
			t.Errorf("expected error to mention %q, got: %v", "upload herdr", err)
		}

		// The cached binary must survive a failed upload — deleting a cached
		// artifact on the failure path is the buildLambdaZips defect (see
		// CLAUDE.md Phase 126 operator-image findings) pointed at a different
		// file.
		if _, statErr := os.Stat(binaryPath); statErr != nil {
			t.Fatalf("cached binary was removed on the upload-failure path: %v", statErr)
		}
	})
}
