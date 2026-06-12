package cmd_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
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

// TestUnlockCmd_EmptyStateBucketUsesDynamo verifies that km unlock with an empty StateBucket
// engages the DynamoDB-first atomic unlock path — not the legacy "state bucket" guard.
//
// The "state bucket not configured" message lives only in runLockS3Fallback, which is
// reached only after a ResourceNotFoundException from DynamoDB. In a unit-test
// environment the DynamoDB in-memory stub returns a lock-state error directly, so
// the legacy bucket-guard never fires. Symmetric to TestLockCmd_EmptyStateBucketUsesDynamo.
func TestUnlockCmd_EmptyStateBucketUsesDynamo(t *testing.T) {
	cfg := &config.Config{StateBucket: ""}
	root := &cobra.Command{Use: "km"}
	unlockCmd := cmd.NewUnlockCmdWithPublisher(cfg, nil)
	root.AddCommand(unlockCmd)
	root.SetArgs([]string{"unlock", "--yes", "sb-aabbccdd"})
	err := root.Execute()
	// The legacy "state bucket" guard must NOT be triggered; the DynamoDB-first path
	// may return a lock-state error or nil, but never the old bucket-guard message.
	if err != nil && strings.Contains(err.Error(), "state bucket") {
		t.Errorf("legacy 'state bucket' guard must not fire on DynamoDB-first unlock path; got: %v", err)
	}
}
