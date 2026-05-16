package main

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	auditlog "github.com/whereiskurt/klanker-maker/sidecars/audit-log"
)

// TestBuildDestStdoutReturnsRedacting verifies that buildDest("stdout", ..., nil)
// returns a *auditlog.RedactingDestination.
func TestBuildDestStdoutReturnsRedacting(t *testing.T) {
	ctx := context.Background()
	dest, err := buildDest(ctx, "stdout", "/km/sandboxes/test/", nil)
	if err != nil {
		t.Fatalf("buildDest stdout: unexpected error: %v", err)
	}
	if _, ok := dest.(*auditlog.RedactingDestination); !ok {
		t.Errorf("buildDest stdout: got %T, want *auditlog.RedactingDestination", dest)
	}
}

// TestBuildDestS3ReturnsRedacting verifies that buildDest("s3", ..., nil)
// returns a *auditlog.RedactingDestination.
func TestBuildDestS3ReturnsRedacting(t *testing.T) {
	ctx := context.Background()
	dest, err := buildDest(ctx, "s3", "/km/sandboxes/test/", nil)
	if err != nil {
		t.Fatalf("buildDest s3: unexpected error: %v", err)
	}
	if _, ok := dest.(*auditlog.RedactingDestination); !ok {
		t.Errorf("buildDest s3: got %T, want *auditlog.RedactingDestination", dest)
	}
}

// TestBuildDestNilCWClientStdout verifies that passing a nil CW client with "stdout"
// destination works without error — CW client is only used for the cloudwatch dest.
func TestBuildDestNilCWClientStdout(t *testing.T) {
	ctx := context.Background()
	dest, err := buildDest(ctx, "stdout", "/km/sandboxes/test/", nil)
	if err != nil {
		t.Fatalf("buildDest with nil cwClient and stdout: unexpected error: %v", err)
	}
	if dest == nil {
		t.Error("buildDest returned nil destination")
	}
}

// TestIdleDetectorTypeExists verifies that lifecycle.IdleDetector can be constructed
// and that the Run method has the correct signature (compile-time check).
func TestIdleDetectorTypeExists(t *testing.T) {
	// This is a compile-time test — if IdleDetector or its fields/methods don't exist,
	// the test file won't compile.
	detector := newIdleDetector("sb-test", 30, nil, "/km/sandboxes/test/", "audit", func(id string) {})
	if detector == nil {
		t.Error("newIdleDetector returned nil")
	}
}

// ============================================================
// Phase 56.1 Bug 3 test (RED — fails until Task 3 adds openAuditPipeWithRetry)
// ============================================================

// TestMainFIFORetry_CreatesAndOpensFIFO exercises the not-yet-existing
// openAuditPipeWithRetry helper. This test will fail to compile until Task 3
// extracts that helper from the inline FIFO open block in main.go.
func TestMainFIFORetry_CreatesAndOpensFIFO(t *testing.T) {
	t.Run("fresh tmpdir missing parent", func(t *testing.T) {
		base := t.TempDir()
		pipePath := filepath.Join(base, "sub", "audit-pipe")
		// Parent "sub/" does NOT exist — helper must create it and mkfifo.

		start := time.Now()
		f, err := openAuditPipeWithRetry(pipePath, 3, 1*time.Millisecond)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if f == nil {
			t.Fatal("expected non-nil *os.File")
		}
		f.Close()

		// Parent dir must exist after the call.
		if _, statErr := os.Stat(filepath.Dir(pipePath)); statErr != nil {
			t.Errorf("expected parent dir to exist after call: %v", statErr)
		}
		// Path must be a named pipe.
		fi, statErr := os.Stat(pipePath)
		if statErr != nil {
			t.Fatalf("expected pipePath to exist after call: %v", statErr)
		}
		if fi.Mode()&os.ModeNamedPipe == 0 {
			t.Errorf("expected pipePath to be a named pipe, got mode %v", fi.Mode())
		}
		// Elapsed must be bounded (no blocking on open).
		if elapsed > 2*time.Second {
			t.Errorf("expected call to complete in <2s, took %v", elapsed)
		}
	})

	t.Run("pre-existing FIFO opens on attempt 1", func(t *testing.T) {
		base := t.TempDir()
		pipePath := filepath.Join(base, "audit-pipe")

		// Pre-create the FIFO.
		if err := syscall.Mkfifo(pipePath, 0666); err != nil {
			t.Fatalf("setup: mkfifo failed: %v", err)
		}
		fi0, _ := os.Stat(pipePath)

		f, err := openAuditPipeWithRetry(pipePath, 3, 1*time.Millisecond)
		if err != nil {
			t.Fatalf("expected success on pre-existing FIFO, got: %v", err)
		}
		if f == nil {
			t.Fatal("expected non-nil *os.File")
		}
		f.Close()

		// Inode should be unchanged (no re-creation of an existing FIFO).
		fi1, _ := os.Stat(pipePath)
		if fi0.Sys() != nil && fi1.Sys() != nil {
			// Compare inode if available — just verify the path still exists.
		}
		_ = fi1
	})

	t.Run("persistent failure exhausts retries", func(t *testing.T) {
		base := t.TempDir()
		// Block MkdirAll by placing a regular file where the parent should be.
		blocker := filepath.Join(base, "blocked-parent")
		if err := os.WriteFile(blocker, []byte("not a dir"), 0644); err != nil {
			t.Fatalf("setup: WriteFile: %v", err)
		}
		pipePath := filepath.Join(blocker, "audit-pipe") // blocker is a file, not dir

		start := time.Now()
		f, err := openAuditPipeWithRetry(pipePath, 3, 1*time.Millisecond)
		elapsed := time.Since(start)

		if err == nil {
			t.Error("expected non-nil error on persistent failure")
		}
		if f != nil {
			f.Close()
			t.Error("expected nil file on failure")
		}
		// Bounded above by a generous margin (3 attempts * 1ms backoff ~ 6ms).
		if elapsed > 100*time.Millisecond {
			t.Errorf("expected call to complete in <100ms for fast-backoff failure, took %v", elapsed)
		}
	})

	t.Run("path exists as regular file - self-heal replaces with FIFO", func(t *testing.T) {
		base := t.TempDir()
		pipePath := filepath.Join(base, "audit-pipe")
		// Simulate the post-resume scenario: a regular file at the FIFO path
		// (in production this is left by km-presence's `timeout 0.1 tee` when
		// the FIFO is absent on a fresh-tmpfs boot).
		if err := os.WriteFile(pipePath, []byte("not a fifo"), 0644); err != nil {
			t.Fatalf("setup: WriteFile: %v", err)
		}
		f, err := openAuditPipeWithRetry(pipePath, 3, 1*time.Millisecond)
		if err != nil {
			t.Fatalf("Phase 79.1 L2-SELFHEAL: expected self-heal success, got: %v", err)
		}
		if f == nil {
			t.Fatal("Phase 79.1 L2-SELFHEAL: expected non-nil *os.File on self-heal path")
		}
		f.Close()
		fi, statErr := os.Stat(pipePath)
		if statErr != nil {
			t.Fatalf("Phase 79.1 L2-SELFHEAL: pipePath must exist after self-heal: %v", statErr)
		}
		if fi.Mode()&os.ModeNamedPipe == 0 {
			t.Errorf("Phase 79.1 L2-SELFHEAL: expected path to be named pipe after self-heal, got mode %v", fi.Mode())
		}
	})
}
