package aws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
)

// --------------------------------------------------------------------------
// Mocks
// --------------------------------------------------------------------------

type mockEventBridgeAPI struct {
	capturedInput *eventbridge.PutEventsInput
	err           error
}

func (m *mockEventBridgeAPI) PutEvents(ctx context.Context, params *eventbridge.PutEventsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error) {
	m.capturedInput = params
	if m.err != nil {
		return nil, m.err
	}
	return &eventbridge.PutEventsOutput{}, nil
}

// --------------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------------

// TestPublishSandboxIdleEvent: PublishSandboxIdleEvent calls PutEvents with correct
// source, detail-type, detail JSON containing sandbox_id, and bus name "default".
func TestPublishSandboxIdleEvent(t *testing.T) {
	mockEB := &mockEventBridgeAPI{}
	err := PublishSandboxIdleEvent(context.Background(), mockEB, "sb-aabbccdd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mockEB.capturedInput == nil {
		t.Fatal("expected PutEvents to be called")
	}
	if len(mockEB.capturedInput.Entries) != 1 {
		t.Fatalf("expected 1 event entry, got %d", len(mockEB.capturedInput.Entries))
	}
	entry := mockEB.capturedInput.Entries[0]

	// Verify source
	if entry.Source == nil || *entry.Source != "km.sandbox" {
		t.Errorf("expected source 'km.sandbox', got: %v", entry.Source)
	}

	// Verify detail-type
	if entry.DetailType == nil || *entry.DetailType != "SandboxIdle" {
		t.Errorf("expected detail-type 'SandboxIdle', got: %v", entry.DetailType)
	}

	// Verify detail JSON contains sandbox_id and event_type
	if entry.Detail == nil {
		t.Fatal("expected detail to be non-nil")
	}
	var detail map[string]string
	if err := json.Unmarshal([]byte(*entry.Detail), &detail); err != nil {
		t.Fatalf("detail is not valid JSON: %v", err)
	}
	if detail["sandbox_id"] != "sb-aabbccdd" {
		t.Errorf("expected detail.sandbox_id='sb-aabbccdd', got: %q", detail["sandbox_id"])
	}
	if detail["event_type"] != "idle" {
		t.Errorf("expected detail.event_type='idle', got: %q", detail["event_type"])
	}

	// Verify event bus name
	if entry.EventBusName == nil || *entry.EventBusName != "default" {
		t.Errorf("expected event bus 'default', got: %v", entry.EventBusName)
	}
}

// TestPublishSandboxIdleEvent_Error: PublishSandboxIdleEvent returns PutEvents errors.
func TestPublishSandboxIdleEvent_Error(t *testing.T) {
	mockEB := &mockEventBridgeAPI{
		err: errors.New("eventbridge unavailable"),
	}
	err := PublishSandboxIdleEvent(context.Background(), mockEB, "sb-aabbccdd")
	if err == nil {
		t.Fatal("expected error when PutEvents fails")
	}
}
