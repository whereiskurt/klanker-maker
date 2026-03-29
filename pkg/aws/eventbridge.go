// Package aws — eventbridge.go
// PutSandboxCreateEvent wraps the EventBridge SDK for publishing sandbox
// creation dispatch events from km create --remote.
//
// Note: EventBridgeAPI interface is defined in idle_event.go and shared by
// both PublishSandboxCommand and PutSandboxCreateEvent.
package aws

import (
	"context"
	"encoding/json"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

// SandboxCreateDetail is the EventBridge event detail payload for a SandboxCreate event.
// The Lambda create handler unmarshals this struct from the EventBridge event.
type SandboxCreateDetail struct {
	SandboxID      string `json:"sandbox_id"`
	ArtifactBucket string `json:"artifact_bucket"`
	ArtifactPrefix string `json:"artifact_prefix"`
	OperatorEmail  string `json:"operator_email,omitempty"`
	OnDemand       bool   `json:"on_demand"`
	CreatedBy      string `json:"created_by,omitempty"` // "cli", "email", "api", "remote"
	Alias          string `json:"alias,omitempty"`       // --alias override, forwarded to create subprocess
}

// PutSandboxCreateEvent publishes a SandboxCreate event to EventBridge.
//
// The event uses source "km.sandbox" and detail-type "SandboxCreate".
// Returns an error if the SDK call fails or if any entries are rejected
// (FailedEntryCount > 0).
func PutSandboxCreateEvent(ctx context.Context, client EventBridgeAPI, detail SandboxCreateDetail) error {
	detailBytes, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("marshal SandboxCreateDetail: %w", err)
	}
	detailStr := string(detailBytes)

	out, err := client.PutEvents(ctx, &eventbridge.PutEventsInput{
		Entries: []ebtypes.PutEventsRequestEntry{
			{
				Source:     awssdk.String("km.sandbox"),
				DetailType: awssdk.String("SandboxCreate"),
				Detail:     awssdk.String(detailStr),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("eventbridge PutEvents: %w", err)
	}
	if out.FailedEntryCount > 0 {
		return fmt.Errorf("eventbridge PutEvents: %d entries failed", out.FailedEntryCount)
	}
	return nil
}
