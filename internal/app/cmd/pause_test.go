package cmd_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// ---- Helpers ----

func runPauseRemote(t *testing.T, pub cmd.RemoteCommandPublisher, sandboxID string) error {
	t.Helper()
	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	pauseCmd := cmd.NewPauseCmdWithPublisher(cfg, pub)
	root.AddCommand(pauseCmd)
	root.SetArgs([]string{"pause", "--remote", sandboxID})
	return root.Execute()
}

// ---- Tests ----

// TestPauseCmd_RemotePublishesCorrectEvent verifies that km pause --remote dispatches
// an EventBridge event with eventType "pause" and the correct sandbox ID.
func TestPauseCmd_RemotePublishesCorrectEvent(t *testing.T) {
	pub := &fakePublisher{}
	err := runPauseRemote(t, pub, "sb-aabbccdd")
	if err != nil {
		t.Fatalf("pause --remote returned error: %v", err)
	}
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publisher call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.sandboxID != "sb-aabbccdd" {
		t.Errorf("sandboxID = %q, want %q", call.sandboxID, "sb-aabbccdd")
	}
	if call.eventType != "pause" {
		t.Errorf("eventType = %q, want %q", call.eventType, "pause")
	}
}

// TestPauseCmd_RemotePublishFailure verifies that when EventBridge publish fails,
// km pause --remote returns a clear error message.
func TestPauseCmd_RemotePublishFailure(t *testing.T) {
	pub := &fakePublisher{err: errors.New("eventbridge unavailable")}
	err := runPauseRemote(t, pub, "sb-aabbccdd")
	if err == nil {
		t.Fatal("expected error when publisher fails, got nil")
	}
	if !strings.Contains(err.Error(), "pause") && !strings.Contains(err.Error(), "eventbridge") {
		t.Errorf("error should mention pause or eventbridge, got: %v", err)
	}
}

// TestPauseCmd_RemoteInvalidSandboxID verifies that an invalid sandbox ID returns
// an error before calling the publisher.
func TestPauseCmd_RemoteInvalidSandboxID(t *testing.T) {
	pub := &fakePublisher{}
	err := runPauseRemote(t, pub, "NOT-VALID")
	if err == nil {
		t.Fatal("expected error for invalid sandbox ID, got nil")
	}
	if len(pub.calls) != 0 {
		t.Errorf("expected 0 publisher calls for invalid ID, got %d", len(pub.calls))
	}
}
