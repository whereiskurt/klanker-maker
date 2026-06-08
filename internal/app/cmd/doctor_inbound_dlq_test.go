package cmd

// doctor_inbound_dlq_test.go — Phase 99.1 Plan 04 (Wave-0 RED → GREEN)
//
// Exercises checkInboundDLQDepth, the km doctor check that surfaces poison
// messages stranded in the shared per-install FIFO dead-letter queues
// ({prefix}-github-inbound-dlq.fifo / {prefix}-slack-inbound-dlq.fifo).
//
// Four branches (RESEARCH Finding 6, VALIDATION Wave-0):
//   - nil SQS client          ⇒ CheckSkipped (dormant — no inbound configured)
//   - DLQ does not exist       ⇒ CheckSkipped (dormant — DLQs never provisioned)
//   - both DLQs depth 0        ⇒ CheckOK
//   - any DLQ depth > 0        ⇒ CheckWarn (count + remediation hint)

import (
	"context"
	"strings"
	"testing"

	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// dlqURLs returns a fakeSQS pre-seeded so ListQueues resolves both shared DLQ
// URLs by their QueueNamePrefix. resourcePrefix "km" ⇒
// km-github-inbound-dlq.fifo and km-slack-inbound-dlq.fifo.
func dlqURLs(resourcePrefix string) *fakeSQS {
	gh := kmaws.GitHubInboundDLQName(resourcePrefix)
	sl := kmaws.SlackInboundDLQName(resourcePrefix)
	return &fakeSQS{
		listByPrefix: true,
		listResult: []string{
			"https://sqs.us-east-1.amazonaws.com/123456789012/" + gh,
			"https://sqs.us-east-1.amazonaws.com/123456789012/" + sl,
		},
	}
}

func TestCheckInboundDLQDepth_Nil(t *testing.T) {
	r := checkInboundDLQDepth(context.Background(), nil, "km")
	if r.Status != CheckSkipped {
		t.Fatalf("nil SQS client: want CheckSkipped, got %s (%s)", r.Status, r.Message)
	}
}

func TestCheckInboundDLQDepth_NotExist(t *testing.T) {
	// Empty ListQueues result ⇒ neither DLQ URL resolves ⇒ check is dormant.
	fs := &fakeSQS{listByPrefix: true, listResult: nil}
	r := checkInboundDLQDepth(context.Background(), fs, "km")
	if r.Status != CheckSkipped {
		t.Fatalf("absent DLQs (empty ListQueues): want CheckSkipped, got %s (%s)", r.Status, r.Message)
	}

	// QueueDoesNotExist from GetQueueAttributes is also dormant, not an error.
	fs2 := dlqURLs("km")
	fs2.getAttrsErr = &sqstypes.QueueDoesNotExist{}
	r2 := checkInboundDLQDepth(context.Background(), fs2, "km")
	if r2.Status != CheckSkipped {
		t.Fatalf("QueueDoesNotExist: want CheckSkipped, got %s (%s)", r2.Status, r2.Message)
	}
}

func TestCheckInboundDLQDepth_OK(t *testing.T) {
	fs := dlqURLs("km")
	// depthByName nil ⇒ every queue reports depth 0.
	r := checkInboundDLQDepth(context.Background(), fs, "km")
	if r.Status != CheckOK {
		t.Fatalf("both DLQs empty: want CheckOK, got %s (%s)", r.Status, r.Message)
	}
}

func TestCheckInboundDLQDepth_Warn(t *testing.T) {
	fs := dlqURLs("km")
	// GitHub DLQ has 2 poison messages; Slack DLQ stays empty.
	fs.depthByName = map[string]string{
		kmaws.GitHubInboundDLQName("km"): "2",
	}
	r := checkInboundDLQDepth(context.Background(), fs, "km")
	if r.Status != CheckWarn {
		t.Fatalf("non-empty DLQ: want CheckWarn, got %s (%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "2") {
		t.Errorf("WARN message should mention the message count, got %q", r.Message)
	}
	hint := strings.ToLower(r.Message + " " + r.Remediation)
	if !strings.Contains(hint, "receive-message") && !strings.Contains(hint, "purge") && !strings.Contains(hint, "redrive") {
		t.Errorf("WARN should carry a remediation hint (receive-message / purge / redrive), got message=%q remediation=%q", r.Message, r.Remediation)
	}
}
