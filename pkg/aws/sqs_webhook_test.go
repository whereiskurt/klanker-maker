package aws

// sqs_webhook_test.go — Phase 127 tests
//
// Tests for:
//   - WebhookInboundQueueName / WebhookInboundDLQName format
//   - webhookInboundQueueAttrs (long 1800s visibility, optional RedrivePolicy)
//   - CreateWebhookInboundQueue + DeleteWebhookInboundQueue (mocked SQS)
//   - WebhookInboundQueueURL round-trip through metadata marshal/unmarshal

import (
	"context"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// ============================================================
// Mocks
// ============================================================

// testWebhookSQS is a minimal in-memory implementation of SQSClient for tests.
type testWebhookSQS struct {
	createCalled int
	createName   string
	createAttrs  map[string]string
	createErr    error

	deleteCalled int
	deleteURL    string
	deleteErr    error
}

func (f *testWebhookSQS) CreateQueue(_ context.Context, in *sqs.CreateQueueInput, _ ...func(*sqs.Options)) (*sqs.CreateQueueOutput, error) {
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

func (f *testWebhookSQS) DeleteQueue(_ context.Context, in *sqs.DeleteQueueInput, _ ...func(*sqs.Options)) (*sqs.DeleteQueueOutput, error) {
	f.deleteCalled++
	if in.QueueUrl != nil {
		f.deleteURL = *in.QueueUrl
	}
	return &sqs.DeleteQueueOutput{}, f.deleteErr
}

func (f *testWebhookSQS) GetQueueAttributes(_ context.Context, _ *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	return &sqs.GetQueueAttributesOutput{
		Attributes: map[string]string{"ApproximateNumberOfMessages": "0"},
	}, nil
}

func (f *testWebhookSQS) ListQueues(_ context.Context, _ *sqs.ListQueuesInput, _ ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error) {
	return &sqs.ListQueuesOutput{}, nil
}

// ============================================================
// Name format tests
// ============================================================

func TestWebhookInboundQueueName(t *testing.T) {
	got := WebhookInboundQueueName("km", "sb-abc123")
	want := "km-webhook-inbound-sb-abc123.fifo"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWebhookInboundDLQName(t *testing.T) {
	got := WebhookInboundDLQName("km")
	want := "km-webhook-inbound-dlq.fifo"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ============================================================
// webhookInboundQueueAttrs tests
// ============================================================

// Visibility MUST be the long (1800s) H1 value, not the 30s Slack/GitHub value:
// a triage turn routinely outlives 30s, and a redelivered in-flight message
// would run the same triage twice.
func TestWebhookInboundQueueAttrs_LongVisibility(t *testing.T) {
	attrs, err := webhookInboundQueueAttrs("")
	if err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs["VisibilityTimeout"] != h1InboundVisibilityTimeout {
		t.Errorf("VisibilityTimeout: got %q, want %q",
			attrs["VisibilityTimeout"], h1InboundVisibilityTimeout)
	}
	if attrs["FifoQueue"] != "true" {
		t.Error("must be a FIFO queue")
	}
	if _, ok := attrs["RedrivePolicy"]; ok {
		t.Error("empty dlqARN must not attach a RedrivePolicy (dormancy)")
	}
}

func TestWebhookInboundQueueAttrs_RedrivePolicy(t *testing.T) {
	attrs, err := webhookInboundQueueAttrs("arn:aws:sqs:us-east-1:1:km-webhook-inbound-dlq.fifo")
	if err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs["RedrivePolicy"] == "" {
		t.Error("non-empty dlqARN must attach a RedrivePolicy")
	}
}

// ============================================================
// CreateWebhookInboundQueue / DeleteWebhookInboundQueue tests
// ============================================================

func TestCreateWebhookInboundQueue_FIFO(t *testing.T) {
	fs := &testWebhookSQS{}
	queueName := WebhookInboundQueueName("km", "sb-abc123")

	url, err := CreateWebhookInboundQueue(context.Background(), fs, queueName, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url == "" {
		t.Fatal("expected non-empty queue URL")
	}
	if fs.createCalled != 1 {
		t.Fatalf("expected 1 CreateQueue call, got %d", fs.createCalled)
	}
	if fs.createName != queueName {
		t.Errorf("queue name: got %q, want %q", fs.createName, queueName)
	}
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
}

func TestDeleteWebhookInboundQueue_EmptyURL(t *testing.T) {
	fs := &testWebhookSQS{}
	if err := DeleteWebhookInboundQueue(context.Background(), fs, ""); err != nil {
		t.Fatalf("empty URL should be no-op, got %v", err)
	}
	if fs.deleteCalled != 0 {
		t.Fatalf("expected 0 DeleteQueue calls on empty URL, got %d", fs.deleteCalled)
	}
}

// ============================================================
// WebhookInboundQueueURL metadata round-trip tests
// ============================================================

// minimalWebhookMeta returns a SandboxMetadata with required fields filled
// (used by webhook inbound round-trip tests to avoid zero-value panic on marshal).
func minimalWebhookMeta() *SandboxMetadata {
	return &SandboxMetadata{
		SandboxID:   "sb-abc123",
		ProfileName: "webhook-ingress",
		Substrate:   "ec2",
		Region:      "us-east-1",
		CreatedAt:   time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC),
	}
}

// TestSandboxMetadata_WebhookQueueURLRoundTrip is the lossy-round-trip
// regression test: a new per-sandbox attribute must survive
// marshalSandboxItem -> unmarshalWebhookFields — the exact functions every
// read-modify-write path (resume.go, extend.go, ttl-handler) goes through on a
// full-row PutItem/GetItem — or km pause/resume/extend silently drops it.
func TestSandboxMetadata_WebhookQueueURLRoundTrip(t *testing.T) {
	const queueURL = "https://sqs.us-east-1.amazonaws.com/1/km-webhook-inbound-sb-1.fifo"

	meta := minimalWebhookMeta()
	meta.WebhookInboundQueueURL = queueURL

	// marshal -> unmarshal cycle through the real production chokepoint.
	item := marshalSandboxItem(meta)

	if _, ok := item["webhook_inbound_queue_url"]; !ok {
		t.Fatal("marshalSandboxItem: webhook_inbound_queue_url key absent from DDB item")
	}

	out := minimalWebhookMeta()
	out.WebhookInboundQueueURL = "" // reset
	unmarshalWebhookFields(item, out)
	if out.WebhookInboundQueueURL != queueURL {
		t.Errorf("dropped in round-trip: got %q, want %q", out.WebhookInboundQueueURL, queueURL)
	}
}

// TestWebhookInboundQueueURL_OmittedWhenEmpty verifies that when
// WebhookInboundQueueURL is empty, marshalSandboxItem does NOT include the key
// (dormant invariant).
func TestWebhookInboundQueueURL_OmittedWhenEmpty(t *testing.T) {
	meta := minimalWebhookMeta()
	meta.WebhookInboundQueueURL = ""

	item := marshalSandboxItem(meta)
	if _, ok := item["webhook_inbound_queue_url"]; ok {
		t.Fatal("marshalSandboxItem: webhook_inbound_queue_url should be absent when empty (dormant invariant)")
	}
}

// TestWebhookInboundQueueURL_MetadataToRecord verifies that metadataToRecord
// copies WebhookInboundQueueURL from SandboxMetadata to SandboxRecord (used by
// km list / km status).
func TestWebhookInboundQueueURL_MetadataToRecord(t *testing.T) {
	const queueURL = "https://sqs.us-east-1.amazonaws.com/123456789012/km-webhook-inbound-sb-abc123.fifo"
	meta := minimalWebhookMeta()
	meta.WebhookInboundQueueURL = queueURL

	rec := metadataToRecord(meta)
	if rec.WebhookInboundQueueURL != queueURL {
		t.Errorf("metadataToRecord: WebhookInboundQueueURL got %q, want %q", rec.WebhookInboundQueueURL, queueURL)
	}
}
