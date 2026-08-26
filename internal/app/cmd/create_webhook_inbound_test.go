package cmd

// create_webhook_inbound_test.go — Phase 127 tests
//
// Exercises provisionWebhookInboundQueue via local mocks — no real AWS connection.
// Covers: happy path, disabled no-op, DDB persist failure, SSM inject failure,
// DLQ threading, and the km destroy drain.
//
// Structure mirrors create_github_inbound_test.go (deps-struct DI pattern).

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// ============================================================
// Helpers
// ============================================================

// webhookInboundDepsEnabled returns a webhookInboundDeps with inbound
// enabled=enabled and the given fake SQS, DDB error, SSM error wired up.
func webhookInboundDepsEnabled(t *testing.T, enabled bool, fSQS *fakeSQS,
	ddbErr, ssmErr error) (webhookInboundDeps, *testState) {
	t.Helper()
	state := &testState{
		ddbAttrs:  make(map[string]string),
		ssmParams: make(map[string]string),
	}
	p := &profile.SandboxProfile{}
	p.Spec.Notification = &profile.NotificationSpec{
		Webhook: &profile.NotificationWebhookSpec{
			Inbound: &profile.NotificationWebhookInboundSpec{Enabled: &enabled},
		},
	}
	return webhookInboundDeps{
		Profile:   p,
		Cfg:       &config.Config{ResourcePrefix: "km"},
		SandboxID: "sb-abc123",
		SQS:       fSQS,
		UpdateSandboxAttr: func(_ context.Context, _, attr, val string) error {
			if ddbErr != nil {
				return ddbErr
			}
			state.ddbAttrs[attr] = val
			return nil
		},
		PutSSMParameter: func(_ context.Context, name, val string) error {
			if ssmErr != nil {
				return ssmErr
			}
			state.ssmParams[name] = val
			return nil
		},
	}, state
}

// ============================================================
// Tests
// ============================================================

// TestCreate_WebhookInboundQueueProvisioned verifies the happy path:
//   - profile has notification.webhook.inbound.enabled=true
//   - CreateQueue is called exactly once with correct FIFO attributes
//   - DDB attr webhook_inbound_queue_url is written with the returned URL
//   - SSM parameter /{prefix}/sandbox/{id}/webhook-inbound-queue-url is written
//   - provisionWebhookInboundQueue returns the non-empty queue URL
func TestCreate_WebhookInboundQueueProvisioned(t *testing.T) {
	fs := &fakeSQS{}
	deps, state := webhookInboundDepsEnabled(t, true, fs, nil, nil)

	url, err := provisionWebhookInboundQueue(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url == "" {
		t.Fatal("expected non-empty queue URL on success")
	}
	if fs.createCalled != 1 {
		t.Fatalf("expected 1 CreateQueue call, got %d", fs.createCalled)
	}
	// Queue name must follow {prefix}-webhook-inbound-{sandbox-id}.fifo
	expectedName := "km-webhook-inbound-sb-abc123.fifo"
	if fs.createName != expectedName {
		t.Fatalf("queue name: got %q, want %q", fs.createName, expectedName)
	}
	// Verify FIFO attributes — long (1800s) visibility, NOT the 30s Slack/GitHub value.
	if got := fs.createAttrs["FifoQueue"]; got != "true" {
		t.Errorf("FifoQueue attr: got %q, want %q", got, "true")
	}
	if got := fs.createAttrs["ContentBasedDeduplication"]; got != "false" {
		t.Errorf("ContentBasedDeduplication attr: got %q, want %q", got, "false")
	}
	if got := fs.createAttrs["VisibilityTimeout"]; got != "1800" {
		t.Errorf("VisibilityTimeout attr: got %q, want %q", got, "1800")
	}
	if got := fs.createAttrs["MessageRetentionPeriod"]; got != "1209600" {
		t.Errorf("MessageRetentionPeriod attr: got %q, want %q", got, "1209600")
	}
	// DDB must have the queue URL persisted
	if got := state.ddbAttrs["webhook_inbound_queue_url"]; got != url {
		t.Fatalf("DDB webhook_inbound_queue_url: got %q, want %q", got, url)
	}
	// SSM Parameter Store must have the queue URL
	expectedParam := "/km/sandbox/sb-abc123/webhook-inbound-queue-url"
	if got := state.ssmParams[expectedParam]; got != url {
		t.Fatalf("SSM param %s: got %q, want %q", expectedParam, got, url)
	}
}

// TestCreate_WebhookInboundDisabledZeroArtifacts verifies the no-op path:
//   - profile has notification.webhook.inbound.enabled=false
//   - provisionWebhookInboundQueue returns ("", nil)
//   - zero SQS API calls, zero DDB writes, zero SSM writes
func TestCreate_WebhookInboundDisabledZeroArtifacts(t *testing.T) {
	fs := &fakeSQS{}
	deps, state := webhookInboundDepsEnabled(t, false, fs, nil, nil)

	url, err := provisionWebhookInboundQueue(context.Background(), deps)
	if err != nil {
		t.Fatalf("inbound off: unexpected error: %v", err)
	}
	if url != "" {
		t.Fatalf("inbound off: expected empty URL, got %q", url)
	}
	if fs.createCalled != 0 {
		t.Fatalf("inbound off: expected 0 SQS calls, got %d", fs.createCalled)
	}
	if len(state.ddbAttrs) != 0 {
		t.Fatalf("inbound off: expected 0 DDB writes, got %v", state.ddbAttrs)
	}
	if len(state.ssmParams) != 0 {
		t.Fatalf("inbound off: expected 0 SSM parameter writes, got %v", state.ssmParams)
	}
}

// TestCreate_WebhookInboundNilProfile verifies the no-op path when profile is nil:
//   - provisionWebhookInboundQueue returns ("", nil)
func TestCreate_WebhookInboundNilProfile(t *testing.T) {
	fs := &fakeSQS{}
	deps := webhookInboundDeps{
		Profile:           nil,
		Cfg:               &config.Config{ResourcePrefix: "km"},
		SandboxID:         "sb-abc123",
		SQS:               fs,
		UpdateSandboxAttr: func(_ context.Context, _, _, _ string) error { return nil },
		PutSSMParameter:   func(_ context.Context, _, _ string) error { return nil },
	}
	url, err := provisionWebhookInboundQueue(context.Background(), deps)
	if err != nil {
		t.Fatalf("nil profile: unexpected error: %v", err)
	}
	if url != "" {
		t.Fatalf("nil profile: expected empty URL, got %q", url)
	}
}

// TestCreate_WebhookInboundSSMFailureRollback verifies SSM Parameter Store write
// failure triggers best-effort rollback:
//   - CreateQueue succeeds (1 call)
//   - DDB UpdateAttr succeeds
//   - PutSSMParameter fails
//   - DeleteQueue is called exactly once (best-effort rollback)
//   - provisionWebhookInboundQueue returns a non-nil error
func TestCreate_WebhookInboundSSMFailureRollback(t *testing.T) {
	fs := &fakeSQS{}
	ssmErr := errors.New("ssm put-parameter timeout")
	deps, _ := webhookInboundDepsEnabled(t, true, fs, nil, ssmErr)

	url, err := provisionWebhookInboundQueue(context.Background(), deps)
	if err == nil {
		t.Fatal("expected error from SSM Parameter Store write failure")
	}
	if url != "" {
		t.Fatalf("expected empty URL on failure, got %q", url)
	}
	if fs.createCalled != 1 {
		t.Fatalf("expected 1 CreateQueue call before failure, got %d", fs.createCalled)
	}
	if fs.deleteCalled != 1 {
		t.Fatalf("expected 1 DeleteQueue rollback call, got %d", fs.deleteCalled)
	}
}

// TestCreate_WebhookInboundDDBPersistFailureRollback verifies DDB write failure
// triggers best-effort rollback:
//   - CreateQueue succeeds (1 call)
//   - UpdateSandboxAttr fails
//   - DeleteQueue is called exactly once (rollback delete)
//   - provisionWebhookInboundQueue returns a wrapped error
func TestCreate_WebhookInboundDDBPersistFailureRollback(t *testing.T) {
	fs := &fakeSQS{}
	ddbErr := errors.New("ddb conditional check failed")
	deps, _ := webhookInboundDepsEnabled(t, true, fs, ddbErr, nil)

	_, err := provisionWebhookInboundQueue(context.Background(), deps)
	if err == nil {
		t.Fatal("expected error from DDB write failure")
	}
	if fs.createCalled != 1 {
		t.Fatalf("expected 1 CreateQueue before DDB failure, got %d", fs.createCalled)
	}
	if fs.deleteCalled != 1 {
		t.Fatalf("expected 1 DeleteQueue rollback call after DDB failure, got %d", fs.deleteCalled)
	}
}

// ============================================================
// DLQ-ARN threading + teardown guard
// ============================================================

const testWebhookDLQArn = "arn:aws:sqs:us-east-1:123456789012:km-webhook-inbound-dlq.fifo"

// TestCreate_WebhookInboundQueueWithDLQ verifies that a non-empty DLQArn on the
// deps struct injects a RedrivePolicy (maxReceiveCount=3 + the exact
// deadLetterTargetArn) into the CreateQueue Attributes map.
func TestCreate_WebhookInboundQueueWithDLQ(t *testing.T) {
	fs := &fakeSQS{}
	deps, _ := webhookInboundDepsEnabled(t, true, fs, nil, nil)
	deps.DLQArn = testWebhookDLQArn

	if _, err := provisionWebhookInboundQueue(context.Background(), deps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rp, ok := fs.createAttrs["RedrivePolicy"]
	if !ok {
		t.Fatalf("expected RedrivePolicy attribute; attrs=%v", fs.createAttrs)
	}
	var got struct {
		DeadLetterTargetArn string `json:"deadLetterTargetArn"`
		MaxReceiveCount     int    `json:"maxReceiveCount"`
	}
	if err := json.Unmarshal([]byte(rp), &got); err != nil {
		t.Fatalf("RedrivePolicy is not valid JSON: %v (%q)", err, rp)
	}
	if got.MaxReceiveCount != 3 {
		t.Errorf("maxReceiveCount: got %d, want 3", got.MaxReceiveCount)
	}
	if got.DeadLetterTargetArn != testWebhookDLQArn {
		t.Errorf("deadLetterTargetArn: got %q, want %q", got.DeadLetterTargetArn, testWebhookDLQArn)
	}
}

// TestCreate_WebhookInboundQueueNoDLQ verifies that an empty DLQArn leaves NO
// RedrivePolicy key (dormancy invariant).
func TestCreate_WebhookInboundQueueNoDLQ(t *testing.T) {
	fs := &fakeSQS{}
	deps, _ := webhookInboundDepsEnabled(t, true, fs, nil, nil)
	deps.DLQArn = "" // explicit: no shared DLQ resolvable

	if _, err := provisionWebhookInboundQueue(context.Background(), deps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := fs.createAttrs["RedrivePolicy"]; ok {
		t.Fatalf("expected NO RedrivePolicy when DLQArn empty (dormancy); attrs=%v", fs.createAttrs)
	}
}

// TestDrainWebhookInbound_NoSharedDLQDelete verifies km destroy deletes ONLY the
// per-sandbox source queue and never a *-dlq.fifo (shared DLQ is install-scoped).
func TestDrainWebhookInbound_NoSharedDLQDelete(t *testing.T) {
	fs := &fakeSQS{}
	sourceURL := "https://sqs.us-east-1.amazonaws.com/123456789012/km-webhook-inbound-sb-abc123.fifo"
	deps := webhookDestroyInboundDeps{
		SandboxID:      "sb-abc123",
		ResourcePrefix: "km",
		QueueURL:       sourceURL,
		SQS:            fs,
	}
	drainWebhookInbound(context.Background(), deps)

	if fs.deleteCalled != 1 {
		t.Fatalf("expected exactly 1 DeleteQueue (source only), got %d", fs.deleteCalled)
	}
	if fs.deleteURL != sourceURL {
		t.Fatalf("deleted queue URL: got %q, want per-sandbox source %q", fs.deleteURL, sourceURL)
	}
	if strings.Contains(fs.deleteURL, "-dlq.fifo") {
		t.Fatalf("km destroy deleted the shared DLQ %q — it must be install-scoped", fs.deleteURL)
	}
}

// TestDrainWebhookInbound_EmptyURLNoOp verifies the drain is a no-op when the
// sandbox never provisioned a webhook inbound queue.
func TestDrainWebhookInbound_EmptyURLNoOp(t *testing.T) {
	fs := &fakeSQS{}
	drainWebhookInbound(context.Background(), webhookDestroyInboundDeps{
		SandboxID: "sb-abc123",
		QueueURL:  "",
		SQS:       fs,
	})
	if fs.deleteCalled != 0 {
		t.Fatalf("expected 0 DeleteQueue calls when QueueURL is empty, got %d", fs.deleteCalled)
	}
}
