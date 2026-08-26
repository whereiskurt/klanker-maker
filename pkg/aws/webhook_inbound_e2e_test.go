package aws_test

// webhook_inbound_e2e_test.go — Phase 127
//
// End-to-end round-trip test for WebhookInboundQueueURL through the real
// WriteSandboxMetadataDynamo -> (mock DynamoDB) -> ReadSandboxMetadataDynamo
// chokepoint (not just the marshal/unmarshal helper pair in isolation) — the
// same functions every read-modify-write path (resume.go, extend.go,
// ttl-handler) calls on a full-row PutItem/GetItem.

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

func TestWebhookInboundQueueURL_WriteThenReadDynamo_RoundTrip(t *testing.T) {
	queueURL := "https://sqs.us-east-1.amazonaws.com/052251888500/km-webhook-inbound-sb-e2e.fifo"
	meta := &kmaws.SandboxMetadata{
		SandboxID:              "sb-e2e",
		ProfileName:            "webhook-ingress",
		Substrate:              "ec2",
		Region:                 "us-east-1",
		CreatedAt:              time.Now().UTC(),
		WebhookInboundQueueURL: queueURL,
	}

	mock := &mockSandboxMetadataAPI{putItemOutput: &dynamodb.PutItemOutput{}}
	if err := kmaws.WriteSandboxMetadataDynamo(context.Background(), mock, "km-sandbox-metadata", meta); err != nil {
		t.Fatalf("write: unexpected error: %v", err)
	}

	// Simulate the real DynamoDB round trip: what PutItem stored is what the
	// next GetItem returns.
	mock.getItemOutput = &dynamodb.GetItemOutput{Item: mock.putItemInput.Item}

	out, err := kmaws.ReadSandboxMetadataDynamo(context.Background(), mock, "km-sandbox-metadata", "sb-e2e")
	if err != nil {
		t.Fatalf("read: unexpected error: %v", err)
	}
	if out.WebhookInboundQueueURL != queueURL {
		t.Errorf("WebhookInboundQueueURL dropped across write-then-read: got %q, want %q",
			out.WebhookInboundQueueURL, queueURL)
	}
}
