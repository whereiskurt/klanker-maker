package cmd_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// ---- Fake EventBridge publisher ----

type fakePublisher struct {
	calls []publishCall
	err   error
}

type publishCall struct {
	sandboxID string
	eventType string
	extra     []string
}

func (f *fakePublisher) PublishSandboxCommand(_ context.Context, sandboxID, eventType string, extra ...string) error {
	f.calls = append(f.calls, publishCall{sandboxID: sandboxID, eventType: eventType, extra: extra})
	return f.err
}

// ---- Helpers ----

func runStopRemote(t *testing.T, pub cmd.RemoteCommandPublisher, sandboxID string) error {
	t.Helper()
	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	stopCmd := cmd.NewStopCmdWithPublisher(cfg, pub)
	root.AddCommand(stopCmd)
	root.SetArgs([]string{"stop", "--remote", sandboxID})
	return root.Execute()
}

// ---- Tests ----

// TestStopCmd_RemotePublishesCorrectEvent verifies that km stop --remote dispatches
// an EventBridge event with eventType "stop" and the correct sandbox ID.
func TestStopCmd_RemotePublishesCorrectEvent(t *testing.T) {
	pub := &fakePublisher{}
	err := runStopRemote(t, pub, "sb-aabbccdd")
	if err != nil {
		t.Fatalf("stop --remote returned error: %v", err)
	}
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publisher call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.sandboxID != "sb-aabbccdd" {
		t.Errorf("sandboxID = %q, want %q", call.sandboxID, "sb-aabbccdd")
	}
	if call.eventType != "stop" {
		t.Errorf("eventType = %q, want %q", call.eventType, "stop")
	}
}

// TestStopCmd_RemotePublishFailure verifies that when EventBridge publish fails,
// km stop --remote returns a clear error message.
func TestStopCmd_RemotePublishFailure(t *testing.T) {
	pub := &fakePublisher{err: errors.New("eventbridge unavailable")}
	err := runStopRemote(t, pub, "sb-aabbccdd")
	if err == nil {
		t.Fatal("expected error when publisher fails, got nil")
	}
	if !strings.Contains(err.Error(), "stop") && !strings.Contains(err.Error(), "eventbridge") {
		t.Errorf("error should mention stop or eventbridge, got: %v", err)
	}
}

// TestStopCmd_RemoteInvalidSandboxID verifies that an invalid sandbox ID returns
// an error before calling the publisher.
func TestStopCmd_RemoteInvalidSandboxID(t *testing.T) {
	pub := &fakePublisher{}
	err := runStopRemote(t, pub, "NOT-VALID")
	if err == nil {
		t.Fatal("expected error for invalid sandbox ID, got nil")
	}
	if len(pub.calls) != 0 {
		t.Errorf("expected 0 publisher calls for invalid ID, got %d", len(pub.calls))
	}
}
