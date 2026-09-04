//go:build unix

package main

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPipeAudit_RealFIFODoesNotHang(t *testing.T) {
	// A real FIFO with no reader should not block indefinitely.
	// Without O_NONBLOCK, the open() syscall blocks until a reader is attached.
	// This test verifies that Emit returns (nil) in reasonable time.
	p := filepath.Join(t.TempDir(), "audit-pipe")

	// Create a real FIFO.
	if err := syscall.Mkfifo(p, 0o666); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	a := &PipeAudit{Path: p, SandboxID: "sb-fifo-test"}

	// Set a timeout so if Emit hangs, the test fails instead of hanging forever.
	done := make(chan error, 1)
	go func() {
		done <- a.Emit("secret_unseal", map[string]any{"keys": []string{"API_KEY"}})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Emit returned an error for a FIFO with no reader: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Emit blocked for >2s on a FIFO with no reader (should use O_NONBLOCK)")
	}
}
