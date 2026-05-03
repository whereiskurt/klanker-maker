package cmd

// create_slack_inbound_test.go — Phase 67 Plan 06 tests
//
// Exercises provisionSlackInboundQueue via local mocks — no real AWS connection.
// Covers: happy path, disabled no-op, DDB persist failure, SSM inject failure.

import (
	"context"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// ============================================================
// Mocks
// ============================================================

// fakeSQS is an in-memory implementation of awspkg.SQSClient for tests.
type fakeSQS struct {
	createCalled int
	createName   string
	createAttrs  map[string]string
	createErr    error

	deleteCalled int
	deleteURL    string
	deleteErr    error
}

func (f *fakeSQS) CreateQueue(_ context.Context, in *sqs.CreateQueueInput, _ ...func(*sqs.Options)) (*sqs.CreateQueueOutput, error) {
	f.createCalled++
	if in.QueueName != nil {
		f.createName = *in.QueueName
	}
	f.createAttrs = in.Attributes
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &sqs.CreateQueueOutput{
		QueueUrl: awssdk.String("https://sqs.us-east-1.amazonaws.com/123456789012/" + *in.QueueName),
	}, nil
}

func (f *fakeSQS) DeleteQueue(_ context.Context, in *sqs.DeleteQueueInput, _ ...func(*sqs.Options)) (*sqs.DeleteQueueOutput, error) {
	f.deleteCalled++
	if in.QueueUrl != nil {
		f.deleteURL = *in.QueueUrl
	}
	return &sqs.DeleteQueueOutput{}, f.deleteErr
}

func (f *fakeSQS) GetQueueAttributes(_ context.Context, _ *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	return &sqs.GetQueueAttributesOutput{
		Attributes: map[string]string{"ApproximateNumberOfMessages": "0"},
	}, nil
}

func (f *fakeSQS) ListQueues(_ context.Context, _ *sqs.ListQueuesInput, _ ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error) {
	return &sqs.ListQueuesOutput{}, nil
}

// ============================================================
// Test helpers
// ============================================================

// testState captures DDB attribute writes and SSM env injections.
type testState struct {
	ddbAttrs map[string]string // attr → value
	ssmEnv   map[string]string // env var name → value
}

// makeDeps builds a slackInboundDeps wired to the given fakeSQS and error
// controls. inboundEnabled controls NotifySlackInboundEnabled on the profile.
func makeDeps(t *testing.T, inboundEnabled bool, fSQS *fakeSQS,
	ddbErr, ssmErr error) (slackInboundDeps, *testState) {
	t.Helper()

	state := &testState{
		ddbAttrs: make(map[string]string),
		ssmEnv:   make(map[string]string),
	}

	t.Helper()
	cli := &profile.CLISpec{
		NotifySlackInboundEnabled: inboundEnabled,
	}
	p := &profile.SandboxProfile{}
	p.Spec.CLI = cli

	return slackInboundDeps{
		Profile:    p,
		Cfg:        &config.Config{ResourcePrefix: "km"},
		SandboxID:  "sb-abc123",
		InstanceID: "i-0abc1234def567890",
		SQS:        fSQS,
		UpdateSandboxAttr: func(_ context.Context, _, attr, val string) error {
			if ddbErr != nil {
				return ddbErr
			}
			state.ddbAttrs[attr] = val
			return nil
		},
		InjectEnvVar: func(_ context.Context, _, name, val string) error {
			if ssmErr != nil {
				return ssmErr
			}
			state.ssmEnv[name] = val
			return nil
		},
	}, state
}

// ============================================================
// Tests
// ============================================================

// TestCreate_SlackInboundQueueProvisioned verifies the happy path:
// - profile has notifySlackInboundEnabled=true
// - CreateQueue is called exactly once with correct FIFO attributes
// - DDB attr slack_inbound_queue_url is written with the returned URL
// - SSM env var KM_SLACK_INBOUND_QUEUE_URL is injected with the same URL
// - provisionSlackInboundQueue returns the non-empty queue URL
func TestCreate_SlackInboundQueueProvisioned(t *testing.T) {
	fs := &fakeSQS{}
	deps, state := makeDeps(t, true, fs, nil, nil)

	url, err := provisionSlackInboundQueue(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url == "" {
		t.Fatal("expected non-empty queue URL on success")
	}
	if fs.createCalled != 1 {
		t.Fatalf("expected 1 CreateQueue call, got %d", fs.createCalled)
	}
	// Queue name must follow {prefix}-slack-inbound-{sandbox-id}.fifo
	expectedName := "km-slack-inbound-sb-abc123.fifo"
	if fs.createName != expectedName {
		t.Fatalf("queue name: got %q, want %q", fs.createName, expectedName)
	}
	// Verify CONTEXT.md-mandated FIFO attributes
	if got := fs.createAttrs["FifoQueue"]; got != "true" {
		t.Errorf("FifoQueue attr: got %q, want %q", got, "true")
	}
	if got := fs.createAttrs["ContentBasedDeduplication"]; got != "false" {
		t.Errorf("ContentBasedDeduplication attr: got %q, want %q", got, "false")
	}
	if got := fs.createAttrs["VisibilityTimeout"]; got != "30" {
		t.Errorf("VisibilityTimeout attr: got %q, want %q", got, "30")
	}
	if got := fs.createAttrs["MessageRetentionPeriod"]; got != "1209600" {
		t.Errorf("MessageRetentionPeriod attr: got %q, want %q", got, "1209600")
	}
	// DDB must have the queue URL persisted
	if got := state.ddbAttrs["slack_inbound_queue_url"]; got != url {
		t.Fatalf("DDB slack_inbound_queue_url: got %q, want %q", got, url)
	}
	// SSM must have the env var injected
	if got := state.ssmEnv["KM_SLACK_INBOUND_QUEUE_URL"]; got != url {
		t.Fatalf("SSM KM_SLACK_INBOUND_QUEUE_URL: got %q, want %q", got, url)
	}
}

// TestCreate_SlackInboundEnvVarInjection verifies the no-op path:
// - profile has notifySlackInboundEnabled=false
// - provisionSlackInboundQueue returns ("", nil)
// - zero SQS API calls
// - zero DDB or SSM mutations
func TestCreate_SlackInboundEnvVarInjection(t *testing.T) {
	fs := &fakeSQS{}
	deps, state := makeDeps(t, false /* inbound off */, fs, nil, nil)

	url, err := provisionSlackInboundQueue(context.Background(), deps)
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
	if len(state.ssmEnv) != 0 {
		t.Fatalf("inbound off: expected 0 SSM injections, got %v", state.ssmEnv)
	}
}

// TestCreate_SlackInboundQueueRollback verifies SSM inject failure triggers rollback:
// - CreateQueue succeeds (1 call)
// - DDB UpdateAttr succeeds
// - InjectEnvVar fails
// - DeleteQueue is called exactly once (best-effort rollback)
// - provisionSlackInboundQueue returns an error with empty URL
func TestCreate_SlackInboundQueueRollback(t *testing.T) {
	fs := &fakeSQS{}
	ssmErr := errors.New("ssm send-command timeout")
	deps, _ := makeDeps(t, true, fs, nil, ssmErr)

	url, err := provisionSlackInboundQueue(context.Background(), deps)
	if err == nil {
		t.Fatal("expected error from SSM injection failure")
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

// TestCreate_SlackInboundDDBPersistFailure verifies DDB write failure triggers rollback:
// - CreateQueue succeeds (1 call)
// - UpdateSandboxAttr fails
// - DeleteQueue is called exactly once (rollback delete)
// - provisionSlackInboundQueue returns a wrapped error
func TestCreate_SlackInboundDDBPersistFailure(t *testing.T) {
	fs := &fakeSQS{}
	ddbErr := errors.New("ddb conditional check failed")
	deps, _ := makeDeps(t, true, fs, ddbErr, nil)

	_, err := provisionSlackInboundQueue(context.Background(), deps)
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

// TestCreate_SlackInboundReadyAnnouncement is scoped to Plan 67-07.
func TestCreate_SlackInboundReadyAnnouncement(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 67-07")
}
