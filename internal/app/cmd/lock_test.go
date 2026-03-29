package cmd_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// ---- Helpers ----

func runLockRemote(t *testing.T, pub cmd.RemoteCommandPublisher, sandboxID string) error {
	t.Helper()
	cfg := &config.Config{StateBucket: "test-bucket"}
	root := &cobra.Command{Use: "km"}
	lockCmd := cmd.NewLockCmdWithPublisher(cfg, pub)
	root.AddCommand(lockCmd)
	root.SetArgs([]string{"lock", "--remote", sandboxID})
	return root.Execute()
}

// ---- Tests ----

// TestLockCmd_RemotePublishesCorrectEvent verifies that km lock --remote dispatches
// an EventBridge event with eventType "lock" and the correct sandbox ID.
func TestLockCmd_RemotePublishesCorrectEvent(t *testing.T) {
	pub := &fakePublisher{}
	err := runLockRemote(t, pub, "sb-aabbccdd")
	if err != nil {
		t.Fatalf("lock --remote returned error: %v", err)
	}
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publisher call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.sandboxID != "sb-aabbccdd" {
		t.Errorf("sandboxID = %q, want %q", call.sandboxID, "sb-aabbccdd")
	}
	if call.eventType != "lock" {
		t.Errorf("eventType = %q, want %q", call.eventType, "lock")
	}
}

// TestLockCmd_RemoteInvalidSandboxID verifies that an invalid sandbox ID returns
// an error before calling the publisher.
func TestLockCmd_RemoteInvalidSandboxID(t *testing.T) {
	pub := &fakePublisher{}
	err := runLockRemote(t, pub, "NOT-VALID")
	if err == nil {
		t.Fatal("expected error for invalid sandbox ID, got nil")
	}
	if len(pub.calls) != 0 {
		t.Errorf("expected 0 publisher calls for invalid ID, got %d", len(pub.calls))
	}
}

// TestLockCmd_RequiresStateBucket verifies that km lock without a configured
// StateBucket returns a "state bucket" error (not a silent skip).
func TestLockCmd_RequiresStateBucket(t *testing.T) {
	cfg := &config.Config{StateBucket: ""}
	root := &cobra.Command{Use: "km"}
	lockCmd := cmd.NewLockCmdWithPublisher(cfg, nil)
	root.AddCommand(lockCmd)
	root.SetArgs([]string{"lock", "sb-aabbccdd"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when StateBucket is empty, got nil")
	}
	if !strings.Contains(err.Error(), "state bucket") {
		t.Errorf("error should mention 'state bucket', got: %v", err)
	}
}
