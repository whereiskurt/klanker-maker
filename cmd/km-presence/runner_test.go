package main

import (
	"errors"
	"testing"
)

// fakeRunner is a test double for commandRunner. It returns pre-configured
// stdout bytes and errors keyed by the command + args string.
// Looking up an unknown command returns (nil, nil) — treat as empty stdout.
type fakeRunner struct {
	// responses maps "name args[0] args[1]..." → stdout bytes.
	responses map[string][]byte
	// errors maps same keys → error. Allows simulating non-zero exit codes.
	errors map[string]error
}

func (f *fakeRunner) Output(name string, args ...string) ([]byte, error) {
	key := name
	for _, a := range args {
		key += " " + a
	}
	if err, ok := f.errors[key]; ok {
		return f.responses[key], err
	}
	return f.responses[key], nil
}

// TestFakeRunner_ReturnsConfiguredOutput verifies the fake works correctly for
// both positive (returns data) and error (returns configured error) cases.
func TestFakeRunner_ReturnsConfiguredOutput(t *testing.T) {
	t.Run("returns configured stdout", func(t *testing.T) {
		f := &fakeRunner{
			responses: map[string][]byte{
				"who": []byte("sandbox pts/0 2026-05-10\n"),
			},
		}
		got, err := f.Output("who")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(got) != "sandbox pts/0 2026-05-10\n" {
			t.Fatalf("got %q, want %q", got, "sandbox pts/0 2026-05-10\n")
		}
	})

	t.Run("returns configured error", func(t *testing.T) {
		sentinel := errors.New("exit status 1")
		f := &fakeRunner{
			responses: map[string][]byte{},
			errors: map[string]error{
				"pgrep -af clause": sentinel,
			},
		}
		_, err := f.Output("pgrep", "-af", "clause")
		if err != sentinel {
			t.Fatalf("got error %v, want sentinel %v", err, sentinel)
		}
	})

	t.Run("returns nil for unknown command", func(t *testing.T) {
		f := &fakeRunner{}
		got, err := f.Output("unknown-cmd")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected empty output, got %q", got)
		}
	})
}
