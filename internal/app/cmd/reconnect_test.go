package cmd

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"testing"
)

// TestRunReconnectingPortForward covers the auto-reconnect loop that wraps the SSM
// port-forward for `km desktop start` / `km vscode start`. probe is nil here — the
// loop's reconnect/quit decision logic is what matters.
func TestRunReconnectingPortForward(t *testing.T) {
	build := func(c context.Context) *exec.Cmd { return exec.CommandContext(c, "true") }

	t.Run("clean exit returns nil after one call", func(t *testing.T) {
		calls := 0
		execFn := func(c *exec.Cmd) error { calls++; return nil }
		if err := runReconnectingPortForward(context.Background(), execFn, build, nil, true, io.Discard); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
		if calls != 1 {
			t.Fatalf("want 1 call, got %d", calls)
		}
	})

	t.Run("reconnect disabled returns the error after one call", func(t *testing.T) {
		calls := 0
		execFn := func(c *exec.Cmd) error { calls++; return errors.New("boom") }
		err := runReconnectingPortForward(context.Background(), execFn, build, nil, false, io.Discard)
		if err == nil {
			t.Fatal("want error, got nil")
		}
		if calls != 1 {
			t.Fatalf("want 1 call, got %d", calls)
		}
	})

	t.Run("reconnects on drop then succeeds", func(t *testing.T) {
		calls := 0
		execFn := func(c *exec.Cmd) error {
			calls++
			if calls == 1 {
				return errors.New("tunnel dropped")
			}
			return nil
		}
		if err := runReconnectingPortForward(context.Background(), execFn, build, nil, true, io.Discard); err != nil {
			t.Fatalf("want nil after reconnect, got %v", err)
		}
		if calls != 2 {
			t.Fatalf("want 2 calls (drop then reconnect), got %d", calls)
		}
	})

	t.Run("stops without reconnect when the context is cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		execFn := func(c *exec.Cmd) error {
			calls++
			cancel() // operator Ctrl-C equivalent: parent ctx cancelled
			return errors.New("interrupted")
		}
		if err := runReconnectingPortForward(ctx, execFn, build, nil, true, io.Discard); err != nil {
			t.Fatalf("want nil on cancel, got %v", err)
		}
		if calls != 1 {
			t.Fatalf("want 1 call (no reconnect after cancel), got %d", calls)
		}
	})
}
