// Package cmd — destroy_slack_inbound.go
// Phase 67 Plan 07: km destroy drain sequence for Slack-inbound sandboxes.
//
// Sequence (CONTEXT.md):
//  1. Stop systemd poller — prevent new turns from starting.
//  2. Wait up to 30s for any in-flight km agent run to finish.
//  3. Delete SQS queue (drops unprocessed messages; bounded by drain wait).
//  4. Delete km-slack-threads rows for this channel_id (cascade cleanup).
//
// Each step is best-effort: failures are logged but do NOT block km destroy.
// The caller in destroy.go MUST call drainSlackInbound BEFORE the existing
// Phase 63 destroySlackChannel flow so the final "destroyed" message lands
// while the channel is still active.
package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// drainTimeout caps the wait for in-flight km agent run before km destroy
// proceeds. CONTEXT.md spec: best-effort, ~30s.
const drainTimeout = 30 * time.Second

// DDBQueryDeleteAPI is the narrow DynamoDB interface used by drainSlackInbound.
// Satisfied by *dynamodb.Client and by fakeDDB in tests.
type DDBQueryDeleteAPI interface {
	Query(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

// destroyInboundDeps bundles the pieces needed by drainSlackInbound.
// All fields are optional: nil pointers / empty strings cause the corresponding
// step to be skipped (e.g. no QueueURL = drain no-op; nil StopPoller = skip step 1).
type destroyInboundDeps struct {
	SandboxID  string
	InstanceID string
	// QueueURL is the SQS FIFO queue URL. Empty → drain is a no-op.
	QueueURL  string
	ChannelID string

	// SQS client for queue deletion (required when QueueURL is non-empty).
	SQS awspkg.SQSClient
	// DDB client for km-slack-threads cascade delete.
	DDB DDBQueryDeleteAPI
	// SlackThreadsTableName is the DDB table name for km-slack-threads rows.
	SlackThreadsTableName string

	// StopPoller stops km-slack-inbound-poller on the sandbox via SSM SendCommand.
	// nil skips this step (e.g. the instance is already terminated).
	StopPoller func(ctx context.Context, instanceID string) error

	// WaitForAgentRunIdle returns nil when no km-agent tmux session is active.
	// Returns an error if the deadline elapsed before idle.
	// nil skips this step.
	WaitForAgentRunIdle func(ctx context.Context, instanceID string, deadline time.Time) error

	// DeleteSSMParameter removes the SSM Parameter Store entry that holds the
	// inbound queue URL (km create writes /sandbox/{id}/slack-inbound-queue-url
	// when SSM SendCommand is denied by SCP). nil skips this step.
	DeleteSSMParameter func(ctx context.Context, name string) error
}

// drainSlackInbound is the orchestrator for km destroy's Slack-inbound path.
// Sequence (all steps best-effort):
//  1. Stop systemd poller — prevent new turns from starting.
//  2. Wait up to 30s for any in-flight km agent run to finish.
//  3. Delete SQS queue.
//  4. Delete km-slack-threads rows for this channel_id.
//
// Caller must invoke drainSlackInbound BEFORE Phase 63's destroySlackChannel
// so the final "destroyed" message lands while the channel still exists.
func drainSlackInbound(ctx context.Context, deps destroyInboundDeps) {
	if deps.QueueURL == "" {
		// Sandbox has no inbound queue — no-op.
		return
	}
	fmt.Fprintf(os.Stderr, "  Slack inbound drain: starting (queue=%s)\n", deps.QueueURL)

	// Step 1: stop the systemd poller to prevent new turns from starting.
	if deps.StopPoller != nil && deps.InstanceID != "" {
		stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := deps.StopPoller(stopCtx, deps.InstanceID); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ Slack inbound drain: stop poller failed: %v (continuing)\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "  ✓ Slack inbound drain: poller stopped\n")
		}
	}

	// Step 2: wait for any in-flight km agent run to finish.
	if deps.WaitForAgentRunIdle != nil && deps.InstanceID != "" {
		deadline := time.Now().Add(drainTimeout)
		waitCtx, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		if err := deps.WaitForAgentRunIdle(waitCtx, deps.InstanceID, deadline); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ Slack inbound drain: agent-run still active after %s (continuing)\n", drainTimeout)
		} else {
			fmt.Fprintf(os.Stderr, "  ✓ Slack inbound drain: agent-run idle\n")
		}
	}

	// Step 3: delete the SQS queue.
	if deps.SQS != nil {
		if err := awspkg.DeleteSlackInboundQueue(ctx, deps.SQS, deps.QueueURL); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ Slack inbound drain: queue delete failed: %v (continuing)\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "  ✓ Slack inbound drain: queue deleted\n")
		}
	}

	// Step 4: delete km-slack-threads rows for this channel_id.
	if deps.ChannelID != "" && deps.DDB != nil && deps.SlackThreadsTableName != "" {
		if err := cleanupSlackThreads(ctx, deps); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ Slack inbound drain: threads cleanup failed: %v\n", err)
		}
	}

	// Step 5: delete the SSM Parameter Store entry holding the queue URL.
	if deps.DeleteSSMParameter != nil && deps.SandboxID != "" {
		paramName := "/sandbox/" + deps.SandboxID + "/slack-inbound-queue-url"
		if err := deps.DeleteSSMParameter(ctx, paramName); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ Slack inbound drain: SSM parameter delete failed: %v (continuing)\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "  ✓ Slack inbound drain: SSM parameter deleted\n")
		}
	}
}

// cleanupSlackThreads queries km-slack-threads for all rows with the given
// channel_id and deletes them individually (DynamoDB DeleteItem per row).
// Failures on individual deletes are logged but do not abort the cleanup loop.
func cleanupSlackThreads(ctx context.Context, deps destroyInboundDeps) error {
	out, err := deps.DDB.Query(ctx, &dynamodb.QueryInput{
		TableName:              awssdk.String(deps.SlackThreadsTableName),
		KeyConditionExpression: awssdk.String("channel_id = :cid"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":cid": &ddbtypes.AttributeValueMemberS{Value: deps.ChannelID},
		},
	})
	if err != nil {
		return fmt.Errorf("query threads for channel %s: %w", deps.ChannelID, err)
	}

	deleted := 0
	for _, item := range out.Items {
		ts, _ := item["thread_ts"].(*ddbtypes.AttributeValueMemberS)
		if ts == nil {
			continue
		}
		_, derr := deps.DDB.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName: awssdk.String(deps.SlackThreadsTableName),
			Key: map[string]ddbtypes.AttributeValue{
				"channel_id": &ddbtypes.AttributeValueMemberS{Value: deps.ChannelID},
				"thread_ts":  ts,
			},
		})
		if derr != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ Slack inbound drain: failed to delete thread %s/%s: %v\n",
				deps.ChannelID, ts.Value, derr)
			continue
		}
		deleted++
	}
	fmt.Fprintf(os.Stderr, "  ✓ Slack inbound drain: cleaned up %d thread row(s)\n", deleted)
	return nil
}

// makeStopPoller returns a StopPoller callback that stops km-slack-inbound-poller
// on the sandbox via SSM SendCommand.
func makeStopPoller(ssmClient *ssm.Client) func(ctx context.Context, instanceID string) error {
	runner := &productionSSMRunner{client: ssmClient}
	return func(ctx context.Context, instanceID string) error {
		return runner.RunShell(ctx, instanceID, "sudo systemctl stop km-slack-inbound-poller 2>/dev/null || true")
	}
}

// makeWaitForAgentRunIdle returns a WaitForAgentRunIdle callback that polls
// every 2s for the absence of a running km-agent tmux session on the sandbox.
// Returns nil when no session is found. Returns an error if the deadline elapses.
func makeWaitForAgentRunIdle(ssmClient *ssm.Client) func(ctx context.Context, instanceID string, deadline time.Time) error {
	runner := &productionSSMRunner{client: ssmClient}
	return func(ctx context.Context, instanceID string, deadline time.Time) error {
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("wait for agent-run idle: deadline exceeded")
			}
			// Count active km-agent tmux sessions; "0" means idle.
			err := runner.RunShell(ctx, instanceID,
				`count=$(sudo -u sandbox tmux list-sessions 2>/dev/null | grep -c km-agent || echo 0); [ "$count" -eq 0 ] || exit 1`)
			if err == nil {
				// No active session — idle.
				return nil
			}
			// Still active — wait 2s and retry.
			t := time.NewTimer(2 * time.Second)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		}
	}
}
