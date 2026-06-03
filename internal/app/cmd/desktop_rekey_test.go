// Package cmd — desktop_rekey_test.go
// Tests for `km desktop rekey` (rotate the per-sandbox KasmVNC password).
package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// combined SSM output that satisfies BOTH the pre-flight (parseDesktopStatus) and
// the rekey readback in a single mock response (vsCodeSSMMock returns one output).
const rekeyOKSSMOutput = healthyDesktopSSMOutput + "=== READBACK ===\nkasmpasswd-updated\n"

func readDesktopCred(t *testing.T, home, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".km", "desktop", id))
	if err != nil {
		t.Fatalf("read credential: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func TestDesktopRekey(t *testing.T) {
	t.Run("happy path rotates password and rewrites local credential", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		_, oldPass := seedDesktopCredential(t, home, "sb-rk-1")

		ssm := &vsCodeSSMMock{output: rekeyOKSSMOutput}
		err := runDesktopRekey(context.Background(), &config.Config{}, newDesktopEC2Sandbox("sb-rk-1"),
			newRunningEC2Mock(), ssm, "sb-rk-1", false, true)
		if err != nil {
			t.Fatalf("want nil, got %v", err)
		}
		got := readDesktopCred(t, home, "sb-rk-1")
		parts := strings.SplitN(got, ":", 2)
		if len(parts) != 2 || parts[0] != "kasm" {
			t.Fatalf("want kasm:<pass>, got %q", got)
		}
		if parts[1] == oldPass {
			t.Fatal("password was not rotated")
		}
		if len(parts[1]) != 16 {
			t.Fatalf("want 16-char password, got %d chars", len(parts[1]))
		}
	})

	t.Run("errors when the sandbox is not running", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		seedDesktopCredential(t, home, "sb-rk-2")
		ssm := &vsCodeSSMMock{output: rekeyOKSSMOutput}
		err := runDesktopRekey(context.Background(), &config.Config{}, newDesktopEC2Sandbox("sb-rk-2"),
			newStoppedEC2Mock(), ssm, "sb-rk-2", false, true)
		if err == nil || !strings.Contains(err.Error(), "not running") {
			t.Fatalf("want not-running error, got %v", err)
		}
	})

	t.Run("readback failure preserves the old local credential", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		_, oldPass := seedDesktopCredential(t, home, "sb-rk-3")
		// No READBACK marker → rekey verification fails after pre-flight passes.
		ssm := &vsCodeSSMMock{output: healthyDesktopSSMOutput}
		err := runDesktopRekey(context.Background(), &config.Config{}, newDesktopEC2Sandbox("sb-rk-3"),
			newRunningEC2Mock(), ssm, "sb-rk-3", false, true)
		if err == nil || !strings.Contains(err.Error(), "verification failed") {
			t.Fatalf("want verification-failed error, got %v", err)
		}
		if got := readDesktopCred(t, home, "sb-rk-3"); !strings.HasSuffix(got, oldPass) {
			t.Fatalf("local credential changed despite remote failure: %q", got)
		}
	})

	t.Run("lock blocks without --force, proceeds with --force", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		seedDesktopCredential(t, home, "sb-rk-4")

		orig := checkSandboxLock
		defer func() { checkSandboxLock = orig }()
		checkSandboxLock = func(_ context.Context, _ *config.Config, _ string) error {
			return context.DeadlineExceeded // any non-nil → locked
		}
		ssm := &vsCodeSSMMock{output: rekeyOKSSMOutput}

		// Without --force: blocked.
		err := runDesktopRekey(context.Background(), &config.Config{}, newDesktopEC2Sandbox("sb-rk-4"),
			newRunningEC2Mock(), ssm, "sb-rk-4", false, true)
		if err == nil || !strings.Contains(err.Error(), "locked") {
			t.Fatalf("want locked error without --force, got %v", err)
		}

		// With --force: proceeds to success.
		if err := runDesktopRekey(context.Background(), &config.Config{}, newDesktopEC2Sandbox("sb-rk-4"),
			newRunningEC2Mock(), ssm, "sb-rk-4", true, true); err != nil {
			t.Fatalf("want success with --force, got %v", err)
		}
	})
}
