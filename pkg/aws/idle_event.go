package aws

import (
	"context"
	"encoding/json"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

// EventBridgeAPI is the narrow interface needed to publish events to EventBridge.
// Defined here to enable mock-based unit testing without real AWS calls.
type EventBridgeAPI interface {
	PutEvents(
		ctx context.Context,
		params *eventbridge.PutEventsInput,
		optFns ...func(*eventbridge.Options),
	) (*eventbridge.PutEventsOutput, error)
}

// PublishSandboxIdleEvent publishes a SandboxIdle event to the default EventBridge
// event bus so the TTL Lambda can pick it up and destroy the sandbox resources.
//
// The event has:
//   - source:      "km.sandbox"
//   - detail-type: "SandboxIdle"
//   - detail:      {"sandbox_id":"<id>","event_type":"idle"}
//   - bus:         "default"
// PublishSandboxCommand publishes a command event to EventBridge for the TTL Lambda.
// eventType can be: "destroy", "stop", "extend", "schedule-create", etc.
// Extra fields are passed as key-value string pairs. Values that look like
// booleans ("true"/"false") or numbers are preserved as native JSON types.
func PublishSandboxCommand(ctx context.Context, client EventBridgeAPI, sandboxID, eventType string, extra ...string) error {
	m := map[string]interface{}{
		"sandbox_id": sandboxID,
		"event_type": eventType,
	}
	for i := 0; i+1 < len(extra); i += 2 {
		val := extra[i+1]
		switch val {
		case "true":
			m[extra[i]] = true
		case "false":
			m[extra[i]] = false
		default:
			m[extra[i]] = val
		}
	}
	detailBytes, _ := json.Marshal(m)
	detail := string(detailBytes)

	input := &eventbridge.PutEventsInput{
		Entries: []ebtypes.PutEventsRequestEntry{
			{
				Source:       awssdk.String("km.sandbox"),
				DetailType:   awssdk.String("SandboxIdle"),
				Detail:       awssdk.String(detail),
				EventBusName: awssdk.String("default"),
			},
		},
	}

	if _, err := client.PutEvents(ctx, input); err != nil {
		return fmt.Errorf("publish %s event for sandbox %s: %w", eventType, sandboxID, err)
	}
	return nil
}

func PublishSandboxIdleEvent(ctx context.Context, client EventBridgeAPI, sandboxID string) error {
	detail := fmt.Sprintf(`{"sandbox_id":%q,"event_type":"idle"}`, sandboxID)

	input := &eventbridge.PutEventsInput{
		Entries: []ebtypes.PutEventsRequestEntry{
			{
				Source:       awssdk.String("km.sandbox"),
				DetailType:   awssdk.String("SandboxIdle"),
				Detail:       awssdk.String(detail),
				EventBusName: awssdk.String("default"),
			},
		},
	}

	if _, err := client.PutEvents(ctx, input); err != nil {
		return fmt.Errorf("publish SandboxIdle event for sandbox %s: %w", sandboxID, err)
	}
	return nil
}
