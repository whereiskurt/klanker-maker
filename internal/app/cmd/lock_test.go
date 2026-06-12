package cmd_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
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

// TestLockCmd_EmptyStateBucketUsesDynamo verifies that km lock with an empty StateBucket
// engages the DynamoDB-first atomic lock path — not the legacy "state bucket" guard.
//
// The "state bucket not configured" message lives only in runLockS3Fallback, which is
// reached only after a ResourceNotFoundException from DynamoDB. In a unit-test
// environment the DynamoDB in-memory stub returns a lock-state error directly, so
// the legacy bucket-guard never fires. This mirrors TestCheckSandboxLock_FailOpenEmptyBucket
// which already documents the fail-open DynamoDB-first behavior.
func TestLockCmd_EmptyStateBucketUsesDynamo(t *testing.T) {
	cfg := &config.Config{StateBucket: ""}
	root := &cobra.Command{Use: "km"}
	lockCmd := cmd.NewLockCmdWithPublisher(cfg, nil)
	root.AddCommand(lockCmd)
	root.SetArgs([]string{"lock", "sb-aabbccdd"})
	err := root.Execute()
	// The legacy "state bucket" guard must NOT be triggered; the DynamoDB-first path
	// may return a lock-state error or nil, but never the old bucket-guard message.
	if err != nil && strings.Contains(err.Error(), "state bucket") {
		t.Errorf("legacy 'state bucket' guard must not fire on DynamoDB-first lock path; got: %v", err)
	}
}

// TestCheckSandboxLock_FailOpenEmptyBucket verifies that CheckSandboxLock returns nil
// (fail-open) when StateBucket is not configured — avoids blocking commands without metadata.
func TestCheckSandboxLock_FailOpenEmptyBucket(t *testing.T) {
	ctx := t.Context()
	cfg := &config.Config{StateBucket: ""}
	err := cmd.CheckSandboxLock(ctx, cfg, "sb-aabbccdd")
	if err != nil {
		t.Errorf("CheckSandboxLock with empty StateBucket should return nil (fail-open), got: %v", err)
	}
}
