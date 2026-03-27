package aws_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// mockEventBridgeAPI implements EventBridgeAPI for unit tests.
type mockEventBridgeAPI struct {
	putEventsCalled bool
	putEventsInput  *eventbridge.PutEventsInput
	putEventsOutput *eventbridge.PutEventsOutput
	putEventsErr    error
}

func (m *mockEventBridgeAPI) PutEvents(ctx context.Context, input *eventbridge.PutEventsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error) {
	m.putEventsCalled = true
	m.putEventsInput = input
	if m.putEventsOutput != nil {
		return m.putEventsOutput, m.putEventsErr
	}
	return &eventbridge.PutEventsOutput{FailedEntryCount: 0}, m.putEventsErr
}

// TestPutSandboxCreateEvent_Success verifies the happy path: event published with correct fields.
func TestPutSandboxCreateEvent_Success(t *testing.T) {
	mock := &mockEventBridgeAPI{}
	detail := kmaws.SandboxCreateDetail{
		SandboxID:      "sb-test123",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-test123",
		OperatorEmail:  "operator@example.com",
		OnDemand:       false,
	}

	err := kmaws.PutSandboxCreateEvent(context.Background(), mock, detail)
	if err != nil {
		t.Fatalf("PutSandboxCreateEvent returned unexpected error: %v", err)
	}
	if !mock.putEventsCalled {
		t.Fatal("expected PutEvents to be called on the mock")
	}
	if mock.putEventsInput == nil || len(mock.putEventsInput.Entries) == 0 {
		t.Fatal("PutEvents called with no entries")
	}

	entry := mock.putEventsInput.Entries[0]

	if entry.Source == nil || *entry.Source != "km.sandbox" {
		t.Errorf("expected Source=%q, got %v", "km.sandbox", entry.Source)
	}
	if entry.DetailType == nil || *entry.DetailType != "SandboxCreate" {
		t.Errorf("expected DetailType=%q, got %v", "SandboxCreate", entry.DetailType)
	}
	if entry.Detail == nil || *entry.Detail == "" {
		t.Error("expected non-empty Detail JSON")
	}
	// Verify detail JSON contains sandbox_id
	if entry.Detail != nil {
		detail := *entry.Detail
		if !containsStr(detail, "sb-test123") {
			t.Errorf("Detail JSON missing sandbox_id: %s", detail)
		}
		if !containsStr(detail, "km-artifacts") {
			t.Errorf("Detail JSON missing artifact_bucket: %s", detail)
		}
	}
}

// TestPutSandboxCreateEvent_FailedEntry verifies error returned when FailedEntryCount > 0.
func TestPutSandboxCreateEvent_FailedEntry(t *testing.T) {
	failedCount := int32(1)
	mock := &mockEventBridgeAPI{
		putEventsOutput: &eventbridge.PutEventsOutput{
			FailedEntryCount: failedCount,
		},
	}
	detail := kmaws.SandboxCreateDetail{
		SandboxID:      "sb-fail",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-fail",
	}

	err := kmaws.PutSandboxCreateEvent(context.Background(), mock, detail)
	if err == nil {
		t.Fatal("expected error when FailedEntryCount > 0, got nil")
	}
}

// TestPutSandboxCreateEvent_ClientError verifies error propagated from SDK call.
func TestPutSandboxCreateEvent_ClientError(t *testing.T) {
	sdkErr := errors.New("throttled by EventBridge")
	mock := &mockEventBridgeAPI{
		putEventsErr: sdkErr,
	}
	detail := kmaws.SandboxCreateDetail{
		SandboxID:      "sb-err",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-err",
	}

	err := kmaws.PutSandboxCreateEvent(context.Background(), mock, detail)
	if err == nil {
		t.Fatal("expected error when client returns error, got nil")
	}
	if !errors.Is(err, sdkErr) && err.Error() != sdkErr.Error() {
		// Allow wrapped errors
		if !containsStr(err.Error(), sdkErr.Error()) {
			t.Errorf("expected error to contain %q, got: %v", sdkErr.Error(), err)
		}
	}
}

// containsStr is a simple helper for substring checks.
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || findStr(s, sub))
}

func findStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
