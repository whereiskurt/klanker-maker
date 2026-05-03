// Package cmd — doctor_slack_inbound_test.go
// Plan 67-08 — tests for the three Slack-inbound doctor checks:
//   - checkSlackInboundQueueExists
//   - checkSlackInboundStaleQueues
//   - checkSlackAppEventsScopes
//
// All tests use the local fakeSQS (defined in create_slack_inbound_test.go)
// and inline closures for the listInbound / getScopes deps. No real AWS calls.
package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// =============================================================================
// checkSlackInboundQueueExists
// =============================================================================

// TestDoctor_SlackInboundQueueExists_AllHealthy: every inbound row has a
// reachable queue → OK.
func TestDoctor_SlackInboundQueueExists_AllHealthy(t *testing.T) {
	sqsCli := &fakeSQS{} // GetQueueAttributes returns no error
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{
			{SandboxID: "sb-a", QueueURL: "https://sqs.us-east-1.amazonaws.com/1/km-slack-inbound-sb-a.fifo"},
			{SandboxID: "sb-b", QueueURL: "https://sqs.us-east-1.amazonaws.com/1/km-slack-inbound-sb-b.fifo"},
		}, nil
	}
	r := checkSlackInboundQueueExists(context.Background(), listInbound, sqsCli)
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "2 sandbox(es)") {
		t.Errorf("expected message to mention 2 sandboxes, got: %s", r.Message)
	}
}

// TestDoctor_SlackInboundQueueExists_OneMissing: GetQueueAttributes fails for
// the only queue → FAIL with the queue URL surfaced in the message.
func TestDoctor_SlackInboundQueueExists_OneMissing(t *testing.T) {
	sqsCli := &fakeSQS{getAttrsErr: errors.New("queue does not exist")}
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{
			{SandboxID: "sb-a", QueueURL: "https://sqs/orphan.fifo"},
		}, nil
	}
	r := checkSlackInboundQueueExists(context.Background(), listInbound, sqsCli)
	if r.Status != CheckError {
		t.Fatalf("expected FAIL when queue unreachable, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "sb-a") {
		t.Errorf("expected message to mention sb-a, got: %s", r.Message)
	}
}

// TestDoctor_SlackInboundQueueExists_NoSandboxes: empty inbound list → OK with
// "no sandboxes" message (does not call SQS).
func TestDoctor_SlackInboundQueueExists_NoSandboxes(t *testing.T) {
	sqsCli := &fakeSQS{}
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{}, nil
	}
	r := checkSlackInboundQueueExists(context.Background(), listInbound, sqsCli)
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s", r.Status)
	}
	if !strings.Contains(r.Message, "no sandboxes have inbound enabled") {
		t.Errorf("expected 'no sandboxes have inbound enabled' message, got: %s", r.Message)
	}
}

// TestDoctor_SlackInboundQueueExists_NilDeps: nil deps → SKIPPED.
func TestDoctor_SlackInboundQueueExists_NilDeps(t *testing.T) {
	r := checkSlackInboundQueueExists(context.Background(), nil, nil)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s", r.Status)
	}
}

// =============================================================================
// checkSlackInboundStaleQueues
// =============================================================================

// TestDoctor_SlackInboundStaleQueues_FoundOrphans: SQS lists more queues than
// DDB knows about → WARN with the orphan URL surfaced in the message.
func TestDoctor_SlackInboundStaleQueues_FoundOrphans(t *testing.T) {
	sqsCli := &fakeSQS{
		listResult: []string{
			"https://sqs/km-slack-inbound-sb-a.fifo",
			"https://sqs/km-slack-inbound-orphan.fifo",
		},
	}
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{
			{SandboxID: "sb-a", QueueURL: "https://sqs/km-slack-inbound-sb-a.fifo"},
		}, nil
	}
	r := checkSlackInboundStaleQueues(context.Background(), listInbound, sqsCli, "km")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "orphan") {
		t.Errorf("expected message to mention orphan queue, got: %s", r.Message)
	}
}

// TestDoctor_SlackInboundStaleQueues_AllAccountedFor: every SQS queue has a
// matching DDB row → OK.
func TestDoctor_SlackInboundStaleQueues_AllAccountedFor(t *testing.T) {
	sqsCli := &fakeSQS{
		listResult: []string{
			"https://sqs/km-slack-inbound-sb-a.fifo",
			"https://sqs/km-slack-inbound-sb-b.fifo",
		},
	}
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{
			{SandboxID: "sb-a", QueueURL: "https://sqs/km-slack-inbound-sb-a.fifo"},
			{SandboxID: "sb-b", QueueURL: "https://sqs/km-slack-inbound-sb-b.fifo"},
		}, nil
	}
	r := checkSlackInboundStaleQueues(context.Background(), listInbound, sqsCli, "km")
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "all 2 inbound queue(s) accounted for") {
		t.Errorf("expected 'all 2 ... accounted for' message, got: %s", r.Message)
	}
}

// TestDoctor_SlackInboundStaleQueues_NoQueues: SQS returns an empty list → OK
// (does not need to call DDB).
func TestDoctor_SlackInboundStaleQueues_NoQueues(t *testing.T) {
	sqsCli := &fakeSQS{listResult: nil}
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{}, nil
	}
	r := checkSlackInboundStaleQueues(context.Background(), listInbound, sqsCli, "km")
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s", r.Status)
	}
	if !strings.Contains(r.Message, "no inbound queues exist") {
		t.Errorf("expected 'no inbound queues exist' message, got: %s", r.Message)
	}
}

// TestDoctor_SlackInboundStaleQueues_NilDeps: nil deps → SKIPPED.
func TestDoctor_SlackInboundStaleQueues_NilDeps(t *testing.T) {
	r := checkSlackInboundStaleQueues(context.Background(), nil, nil, "km")
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s", r.Status)
	}
}

// =============================================================================
// checkSlackAppEventsScopes
// =============================================================================

// TestDoctor_SlackInboundEventsSubscription_HasAllScopes: bot has all required
// scopes including reactions:write → OK with success message naming all three.
func TestDoctor_SlackInboundEventsSubscription_HasAllScopes(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "channels:history", "groups:history", "channels:read", "reactions:write"}, nil
	}
	r := checkSlackAppEventsScopes(context.Background(), getScopes)
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
	// Verify success message names the new scope (operators read this output).
	if !strings.Contains(r.Message, "reactions:write") {
		t.Errorf("expected success message to mention reactions:write, got: %s", r.Message)
	}
}

// TestDoctor_SlackInboundEventsSubscription_MissingScope: bot is missing all
// three required scopes → FAIL with all three scope names surfaced in the message.
func TestDoctor_SlackInboundEventsSubscription_MissingScope(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write"}, nil // missing all three required scopes
	}
	r := checkSlackAppEventsScopes(context.Background(), getScopes)
	if r.Status != CheckError {
		t.Fatalf("expected FAIL, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "channels:history") ||
		!strings.Contains(r.Message, "groups:history") ||
		!strings.Contains(r.Message, "reactions:write") {
		t.Errorf("expected message to list all three missing scopes, got: %s", r.Message)
	}
	if r.Remediation == "" {
		t.Error("expected non-empty remediation hint")
	}
}

// TestDoctor_SlackInboundEventsSubscription_PartialScopes: bot has channels:history
// and reactions:write but is missing groups:history → FAIL listing only the missing one.
func TestDoctor_SlackInboundEventsSubscription_PartialScopes(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "channels:history", "reactions:write"}, nil // missing groups:history
	}
	r := checkSlackAppEventsScopes(context.Background(), getScopes)
	if r.Status != CheckError {
		t.Fatalf("expected FAIL, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "groups:history") {
		t.Errorf("expected message to mention groups:history, got: %s", r.Message)
	}
}

// TestDoctor_SlackInboundEventsSubscription_MissingReactionsWrite: realistic Phase 67.1
// upgrade scenario — operator already has channels:history + groups:history from Phase 63
// inbound deployment but forgot to add reactions:write when upgrading.
// Should FAIL with only reactions:write surfaced in the message.
func TestDoctor_SlackInboundEventsSubscription_MissingReactionsWrite(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "channels:history", "groups:history", "channels:read"}, nil
	}
	r := checkSlackAppEventsScopes(context.Background(), getScopes)
	if r.Status != CheckError {
		t.Fatalf("expected FAIL when only reactions:write missing, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "reactions:write") {
		t.Errorf("expected message to mention reactions:write specifically, got: %s", r.Message)
	}
	// Should NOT mention the scopes that are present (avoid noise in the operator-facing message).
	if strings.Contains(r.Message, "channels:history") || strings.Contains(r.Message, "groups:history") {
		t.Errorf("expected message to ONLY list missing scopes, got: %s", r.Message)
	}
	if r.Remediation == "" {
		t.Error("expected non-empty remediation hint")
	}
}

// TestDoctor_SlackInboundEventsSubscription_AuthTestError: getScopes returns an
// error → WARN (do not fail doctor).
func TestDoctor_SlackInboundEventsSubscription_AuthTestError(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return nil, errors.New("invalid_auth")
	}
	r := checkSlackAppEventsScopes(context.Background(), getScopes)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN on auth.test error, got %s", r.Status)
	}
}

// TestDoctor_SlackInboundEventsSubscription_NilDeps: nil getScopes → SKIPPED.
func TestDoctor_SlackInboundEventsSubscription_NilDeps(t *testing.T) {
	r := checkSlackAppEventsScopes(context.Background(), nil)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s", r.Status)
	}
}
