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
	// UsesCacheWhenPresent and UploadFailureReturnsError (cache-content variants)
	// moved to herdr_mirror_test.go (package cmd) — they need to override the
	// unexported herdrSHA256 var to make an arbitrary fake cached binary pass
	// digest verification, which this external test package cannot reach.

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

}
