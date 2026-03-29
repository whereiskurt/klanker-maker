package cmd_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// ---- Helpers ----

func runUnlockRemote(t *testing.T, pub cmd.RemoteCommandPublisher, sandboxID string) error {
	t.Helper()
	cfg := &config.Config{StateBucket: "test-bucket"}
	root := &cobra.Command{Use: "km"}
	unlockCmd := cmd.NewUnlockCmdWithPublisher(cfg, pub)
	root.AddCommand(unlockCmd)
	// --yes skips confirmation prompt in tests
	root.SetArgs([]string{"unlock", "--remote", "--yes", sandboxID})
	return root.Execute()
}

// ---- Tests ----

// TestUnlockCmd_RemotePublishesCorrectEvent verifies that km unlock --remote dispatches
// an EventBridge event with eventType "unlock" and the correct sandbox ID.
func TestUnlockCmd_RemotePublishesCorrectEvent(t *testing.T) {
	pub := &fakePublisher{}
	err := runUnlockRemote(t, pub, "sb-aabbccdd")
	if err != nil {
		t.Fatalf("unlock --remote returned error: %v", err)
	}
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publisher call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.sandboxID != "sb-aabbccdd" {
		t.Errorf("sandboxID = %q, want %q", call.sandboxID, "sb-aabbccdd")
	}
	if call.eventType != "unlock" {
		t.Errorf("eventType = %q, want %q", call.eventType, "unlock")
	}
}

// TestUnlockCmd_RemoteInvalidSandboxID verifies that an invalid sandbox ID returns
// an error before calling the publisher.
func TestUnlockCmd_RemoteInvalidSandboxID(t *testing.T) {
	pub := &fakePublisher{}
	err := runUnlockRemote(t, pub, "NOT-VALID")
	if err == nil {
		t.Fatal("expected error for invalid sandbox ID, got nil")
	}
	if len(pub.calls) != 0 {
		t.Errorf("expected 0 publisher calls for invalid ID, got %d", len(pub.calls))
	}
}

// TestUnlockCmd_RequiresStateBucket verifies that km unlock without a configured
// StateBucket returns a "state bucket" error (not a silent skip).
func TestUnlockCmd_RequiresStateBucket(t *testing.T) {
	cfg := &config.Config{StateBucket: ""}
	root := &cobra.Command{Use: "km"}
	unlockCmd := cmd.NewUnlockCmdWithPublisher(cfg, nil)
	root.AddCommand(unlockCmd)
	root.SetArgs([]string{"unlock", "--yes", "sb-aabbccdd"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when StateBucket is empty, got nil")
	}
	if !strings.Contains(err.Error(), "state bucket") {
		t.Errorf("error should mention 'state bucket', got: %v", err)
	}
}
