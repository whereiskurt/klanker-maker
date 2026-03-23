package aws

import (
	"context"
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
