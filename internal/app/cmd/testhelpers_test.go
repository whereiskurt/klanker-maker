package cmd

import (
	"io"
	"os"
	"testing"
)

// captureStderr redirects os.Stderr through a pipe for the duration of fn,
// then restores the original stderr and returns whatever fn wrote.
// Shared helper used by step-11d (Plan 63.1-01) and destroySlackChannel
// (Plan 63.1-02) tests.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		buf, _ := io.ReadAll(r)
		done <- string(buf)
	}()
	fn()
	w.Close()
	os.Stderr = old
	return <-done
}
