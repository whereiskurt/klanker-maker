package aws

// github_inbound_test.go — Phase 97 Plan 03 tests
//
// Tests for:
//   - GitHubInboundQueueName format
//   - CreateGitHubInboundQueue + DeleteGitHubInboundQueue (mocked SQS)
//   - GithubInboundQueueURL round-trip through metadata marshal/unmarshal

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

// testGitHubSQS is a minimal in-memory implementation of SQSClient for tests.
type testGitHubSQS struct {
	createCalled int
	createName   string
	createAttrs  map[string]string
	createErr    error

	deleteCalled int
	deleteURL    string
	deleteErr    error
}

func (f *testGitHubSQS) CreateQueue(_ context.Context, in *sqs.CreateQueueInput, _ ...func(*sqs.Options)) (*sqs.CreateQueueOutput, error) {
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

func (f *testGitHubSQS) DeleteQueue(_ context.Context, in *sqs.DeleteQueueInput, _ ...func(*sqs.Options)) (*sqs.DeleteQueueOutput, error) {
	f.deleteCalled++
	if in.QueueUrl != nil {
		f.deleteURL = *in.QueueUrl
	}
	return &sqs.DeleteQueueOutput{}, f.deleteErr
}

func (f *testGitHubSQS) GetQueueAttributes(_ context.Context, _ *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	return &sqs.GetQueueAttributesOutput{
		Attributes: map[string]string{"ApproximateNumberOfMessages": "0"},
	}, nil
}

func (f *testGitHubSQS) ListQueues(_ context.Context, _ *sqs.ListQueuesInput, _ ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error) {
	return &sqs.ListQueuesOutput{}, nil
}

// ============================================================
// GitHubInboundQueueName tests
// ============================================================

// TestGitHubInboundQueueName verifies the queue name format:
// {resource_prefix}-github-inbound-{sandbox-id}.fifo
func TestGitHubInboundQueueName(t *testing.T) {
	tests := []struct {
		prefix    string
		sandboxID string
		want      string
	}{
		{"km", "sb-abc123", "km-github-inbound-sb-abc123.fifo"},
		{"km2", "sb-xyz789", "km2-github-inbound-sb-xyz789.fifo"},
		{"myprefix", "sb-testid", "myprefix-github-inbound-sb-testid.fifo"},
	}
	for _, tc := range tests {
		got := GitHubInboundQueueName(tc.prefix, tc.sandboxID)
		if got != tc.want {
			t.Errorf("GitHubInboundQueueName(%q, %q) = %q; want %q", tc.prefix, tc.sandboxID, got, tc.want)
		}
	}
}

// ============================================================
// CreateGitHubInboundQueue tests
// ============================================================

// TestCreateGitHubInboundQueue_FIFO verifies that CreateGitHubInboundQueue creates
// a FIFO queue with the required attributes (mirrors CreateSlackInboundQueue behavior):
//   - FifoQueue=true
//   - ContentBasedDeduplication=false
//   - VisibilityTimeout=30
//   - MessageRetentionPeriod=1209600 (14 days)
func TestCreateGitHubInboundQueue_FIFO(t *testing.T) {
	fs := &testGitHubSQS{}
	queueName := GitHubInboundQueueName("km", "sb-abc123")

	url, err := CreateGitHubInboundQueue(context.Background(), fs, queueName)
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
	// Verify mandated FIFO attributes match SlackInboundQueue spec.
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
}

// TestDeleteGitHubInboundQueue_EmptyURL verifies that DeleteGitHubInboundQueue is a
// no-op when the queue URL is empty.
func TestDeleteGitHubInboundQueue_EmptyURL(t *testing.T) {
	fs := &testGitHubSQS{}
	if err := DeleteGitHubInboundQueue(context.Background(), fs, ""); err != nil {
		t.Fatalf("empty URL should be no-op, got %v", err)
	}
	if fs.deleteCalled != 0 {
		t.Fatalf("expected 0 DeleteQueue calls on empty URL, got %d", fs.deleteCalled)
	}
}

// ============================================================
// GithubInboundQueueURL metadata round-trip tests
// ============================================================

// minimalGithubMeta returns a SandboxMetadata with required fields filled
// (used by GitHub inbound round-trip tests to avoid zero-value panic on marshal).
func minimalGithubMeta() *SandboxMetadata {
	return &SandboxMetadata{
		SandboxID:   "sb-abc123",
		ProfileName: "github-review",
		Substrate:   "ec2",
		Region:      "us-east-1",
		CreatedAt:   time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
	}
}

// TestGithubInboundQueueURL_MarshalRoundTrip verifies that GithubInboundQueueURL
// survives a struct → marshalSandboxItem → unmarshalGitHubFields cycle.
// This is the project_sandboxmetadata_lossy_roundtrip invariant: every full-row
// PutItem (resume/extend/ttl-handler) must not silently drop the queue URL.
func TestGithubInboundQueueURL_MarshalRoundTrip(t *testing.T) {
	const queueURL = "https://sqs.us-east-1.amazonaws.com/123456789012/km-github-inbound-sb-abc123.fifo"

	meta := minimalGithubMeta()
	meta.GithubInboundQueueURL = queueURL

	// marshal → unmarshal cycle.
	item := marshalSandboxItem(meta)

	// Verify the attribute is present in the marshaled item.
	if _, ok := item["github_inbound_queue_url"]; !ok {
		t.Fatal("marshalSandboxItem: github_inbound_queue_url key absent from DDB item")
	}

	// Reconstruct via unmarshal path.
	out := minimalGithubMeta()
	out.GithubInboundQueueURL = "" // reset
	unmarshalGitHubFields(item, out)
	if out.GithubInboundQueueURL != queueURL {
		t.Errorf("after unmarshal: got %q, want %q", out.GithubInboundQueueURL, queueURL)
	}
}

// TestGithubInboundQueueURL_OmittedWhenEmpty verifies that when GithubInboundQueueURL
// is empty, the marshalSandboxItem output does NOT include the key (dormant invariant).
func TestGithubInboundQueueURL_OmittedWhenEmpty(t *testing.T) {
	meta := minimalGithubMeta()
	meta.GithubInboundQueueURL = ""

	item := marshalSandboxItem(meta)
	if _, ok := item["github_inbound_queue_url"]; ok {
		t.Fatal("marshalSandboxItem: github_inbound_queue_url should be absent when empty (dormant invariant)")
	}
}

// TestGithubInboundQueueURL_MetadataToRecord verifies that metadataToRecord
// copies GithubInboundQueueURL from SandboxMetadata to SandboxRecord (the
// "copy :139" spot in the plan — used by km list / km status).
func TestGithubInboundQueueURL_MetadataToRecord(t *testing.T) {
	const queueURL = "https://sqs.us-east-1.amazonaws.com/123456789012/km-github-inbound-sb-abc123.fifo"
	meta := minimalGithubMeta()
	meta.GithubInboundQueueURL = queueURL

	rec := metadataToRecord(meta)
	if rec.GithubInboundQueueURL != queueURL {
		t.Errorf("metadataToRecord: GithubInboundQueueURL got %q, want %q", rec.GithubInboundQueueURL, queueURL)
	}
}
