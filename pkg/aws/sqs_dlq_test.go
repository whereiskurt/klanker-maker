package aws

// sqs_dlq_test.go — Phase 99.1 Plan 01 (Wave 0 RED → Wave 1 GREEN)
//
// Unit tests for the shared-DLQ poison-message fix primitives:
//   - DLQ name helpers + deterministic ARN derivation
//   - GetQueueARN (one GetQueueAttributes call for QueueArn)
//   - CreateSharedInboundDLQ (FIFO, idempotent)
//   - dlqARN parameter on Create{GitHub,Slack}InboundQueue → RedrivePolicy injection
//
// All assertions run against an in-memory fake SQSClient (no real AWS).
// The fake mirrors the shape of internal/app/cmd/create_slack_inbound_test.go's
// fakeSQS, defined locally here because that one lives in package cmd.

import (
	"context"
	"encoding/json"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// ============================================================
// In-memory fake SQSClient
// ============================================================

type fakeDLQSQS struct {
	createCalled int
	createName   string
	createAttrs  map[string]string
	createErr    error

	// getAttrs is returned verbatim from GetQueueAttributes (for GetQueueARN).
	getAttrs    map[string]string
	getAttrsErr error

	// listResult controls the queue URLs returned by ListQueues (idempotency lookup).
	listResult []string
}

func (f *fakeDLQSQS) CreateQueue(_ context.Context, in *sqs.CreateQueueInput, _ ...func(*sqs.Options)) (*sqs.CreateQueueOutput, error) {
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

func (f *fakeDLQSQS) DeleteQueue(_ context.Context, _ *sqs.DeleteQueueInput, _ ...func(*sqs.Options)) (*sqs.DeleteQueueOutput, error) {
	return &sqs.DeleteQueueOutput{}, nil
}

func (f *fakeDLQSQS) GetQueueAttributes(_ context.Context, _ *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	if f.getAttrsErr != nil {
		return nil, f.getAttrsErr
	}
	return &sqs.GetQueueAttributesOutput{Attributes: f.getAttrs}, nil
}

func (f *fakeDLQSQS) ListQueues(_ context.Context, _ *sqs.ListQueuesInput, _ ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error) {
	return &sqs.ListQueuesOutput{QueueUrls: f.listResult}, nil
}

// redrivePolicy is the decoded shape of the RedrivePolicy attribute.
type redrivePolicy struct {
	DeadLetterTargetArn string `json:"deadLetterTargetArn"`
	MaxReceiveCount     int    `json:"maxReceiveCount"`
}

// ============================================================
// Name + ARN helpers
// ============================================================

func TestGitHubInboundDLQName(t *testing.T) {
	if got := GitHubInboundDLQName("km"); got != "km-github-inbound-dlq.fifo" {
		t.Fatalf("GitHubInboundDLQName: got %q, want %q", got, "km-github-inbound-dlq.fifo")
	}
}

func TestSlackInboundDLQName(t *testing.T) {
	if got := SlackInboundDLQName("km"); got != "km-slack-inbound-dlq.fifo" {
		t.Fatalf("SlackInboundDLQName: got %q, want %q", got, "km-slack-inbound-dlq.fifo")
	}
}

func TestDLQArn(t *testing.T) {
	got := DLQArn("us-east-1", "123456789012", "km-github-inbound-dlq.fifo")
	want := "arn:aws:sqs:us-east-1:123456789012:km-github-inbound-dlq.fifo"
	if got != want {
		t.Fatalf("DLQArn: got %q, want %q", got, want)
	}
}

// ============================================================
// GetQueueARN
// ============================================================

func TestGetQueueARN(t *testing.T) {
	arn := "arn:aws:sqs:us-east-1:123456789012:km-github-inbound-dlq.fifo"
	fs := &fakeDLQSQS{getAttrs: map[string]string{"QueueArn": arn}}

	got, err := GetQueueARN(context.Background(), fs, "https://sqs/.../km-github-inbound-dlq.fifo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != arn {
		t.Fatalf("GetQueueARN: got %q, want %q", got, arn)
	}

	// Missing attribute → error.
	fsMissing := &fakeDLQSQS{getAttrs: map[string]string{}}
	if _, err := GetQueueARN(context.Background(), fsMissing, "https://sqs/.../x.fifo"); err == nil {
		t.Fatal("expected error when QueueArn attribute is missing")
	}
}

// ============================================================
// CreateSharedInboundDLQ
// ============================================================

func TestCreateSharedInboundDLQ_Attributes(t *testing.T) {
	fs := &fakeDLQSQS{}
	url, err := CreateSharedInboundDLQ(context.Background(), fs, "km-github-inbound-dlq.fifo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url == "" {
		t.Fatal("expected non-empty DLQ URL")
	}
	if fs.createCalled != 1 {
		t.Fatalf("expected 1 CreateQueue call, got %d", fs.createCalled)
	}
	if got := fs.createAttrs[string(sqstypes.QueueAttributeNameFifoQueue)]; got != "true" {
		t.Errorf("FifoQueue attr: got %q, want %q", got, "true")
	}
	if got := fs.createAttrs[string(sqstypes.QueueAttributeNameContentBasedDeduplication)]; got != "false" {
		t.Errorf("ContentBasedDeduplication attr: got %q, want %q", got, "false")
	}
}

func TestCreateSharedInboundDLQ_Idempotent(t *testing.T) {
	existsErr := &sqstypes.QueueNameExists{}
	fs := &fakeDLQSQS{
		createErr:  existsErr,
		listResult: []string{"https://sqs.us-east-1.amazonaws.com/123456789012/km-github-inbound-dlq.fifo"},
	}
	url, err := CreateSharedInboundDLQ(context.Background(), fs, "km-github-inbound-dlq.fifo")
	if err != nil {
		t.Fatalf("idempotent path: unexpected error: %v", err)
	}
	if url != "https://sqs.us-east-1.amazonaws.com/123456789012/km-github-inbound-dlq.fifo" {
		t.Fatalf("idempotent path: got URL %q", url)
	}
}

// ============================================================
// RedrivePolicy injection — GitHub
// ============================================================

func TestCreateGitHubInboundQueue_WithDLQ(t *testing.T) {
	const dlqARN = "arn:aws:sqs:us-east-1:123456789012:km-github-inbound-dlq.fifo"
	fs := &fakeDLQSQS{}
	if _, err := CreateGitHubInboundQueue(context.Background(), fs, "km-github-inbound-sb-x.fifo", dlqARN); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	raw, ok := fs.createAttrs["RedrivePolicy"]
	if !ok {
		t.Fatalf("expected RedrivePolicy attr present; attrs=%v", fs.createAttrs)
	}
	var rp redrivePolicy
	if err := json.Unmarshal([]byte(raw), &rp); err != nil {
		t.Fatalf("RedrivePolicy not valid JSON string: %v (raw=%q)", err, raw)
	}
	if rp.DeadLetterTargetArn != dlqARN {
		t.Errorf("deadLetterTargetArn: got %q, want %q", rp.DeadLetterTargetArn, dlqARN)
	}
	if rp.MaxReceiveCount != 3 {
		t.Errorf("maxReceiveCount: got %d, want 3", rp.MaxReceiveCount)
	}
}

func TestCreateGitHubInboundQueue_NoDLQ(t *testing.T) {
	fs := &fakeDLQSQS{}
	if _, err := CreateGitHubInboundQueue(context.Background(), fs, "km-github-inbound-sb-x.fifo", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, present := fs.createAttrs["RedrivePolicy"]; present {
		t.Fatalf("dormancy invariant violated: RedrivePolicy present when dlqARN empty; attrs=%v", fs.createAttrs)
	}
	assertByteIdenticalInboundAttrs(t, fs.createAttrs)
}

// ============================================================
// RedrivePolicy injection — Slack
// ============================================================

func TestCreateSlackInboundQueue_WithDLQ(t *testing.T) {
	const dlqARN = "arn:aws:sqs:us-east-1:123456789012:km-slack-inbound-dlq.fifo"
	fs := &fakeDLQSQS{}
	if _, err := CreateSlackInboundQueue(context.Background(), fs, "km-slack-inbound-sb-x.fifo", dlqARN); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	raw, ok := fs.createAttrs["RedrivePolicy"]
	if !ok {
		t.Fatalf("expected RedrivePolicy attr present; attrs=%v", fs.createAttrs)
	}
	var rp redrivePolicy
	if err := json.Unmarshal([]byte(raw), &rp); err != nil {
		t.Fatalf("RedrivePolicy not valid JSON string: %v (raw=%q)", err, raw)
	}
	if rp.DeadLetterTargetArn != dlqARN {
		t.Errorf("deadLetterTargetArn: got %q, want %q", rp.DeadLetterTargetArn, dlqARN)
	}
	if rp.MaxReceiveCount != 3 {
		t.Errorf("maxReceiveCount: got %d, want 3", rp.MaxReceiveCount)
	}
}

func TestCreateSlackInboundQueue_NoDLQ(t *testing.T) {
	fs := &fakeDLQSQS{}
	if _, err := CreateSlackInboundQueue(context.Background(), fs, "km-slack-inbound-sb-x.fifo", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, present := fs.createAttrs["RedrivePolicy"]; present {
		t.Fatalf("dormancy invariant violated: RedrivePolicy present when dlqARN empty; attrs=%v", fs.createAttrs)
	}
	assertByteIdenticalInboundAttrs(t, fs.createAttrs)
}

// assertByteIdenticalInboundAttrs verifies the 4 locked attributes are present
// and exactly match today's values, and that no extra keys leaked in. This is
// the dormancy invariant: when dlqARN is empty, the Attributes map must be
// byte-identical to the pre-Phase-99.1 4-attr map.
func assertByteIdenticalInboundAttrs(t *testing.T, attrs map[string]string) {
	t.Helper()
	want := map[string]string{
		string(sqstypes.QueueAttributeNameFifoQueue):                 "true",
		string(sqstypes.QueueAttributeNameContentBasedDeduplication): "false",
		string(sqstypes.QueueAttributeNameVisibilityTimeout):         "1800", // Phase 119: raised from 30s
		string(sqstypes.QueueAttributeNameMessageRetentionPeriod):    "1209600",
	}
	if len(attrs) != len(want) {
		t.Fatalf("attr count: got %d (%v), want %d (%v)", len(attrs), attrs, len(want), want)
	}
	for k, v := range want {
		if got := attrs[k]; got != v {
			t.Errorf("attr %q: got %q, want %q", k, got, v)
		}
	}
}
