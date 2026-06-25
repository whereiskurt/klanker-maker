package aws

// sqs_test.go — Phase 119 Plan 01 (Wave 0 RED)
//
// RED assertions for the inboundQueueAttrs VisibilityTimeout change (P119-E).
// Phase 119 bumps the Slack inbound queue VisibilityTimeout from "30" to "1800"
// (matching the H1/GitHub precedent) so a long-running turn is not
// redelivered mid-flight.
//
// This test is RED until Wave 1 edits inboundQueueAttrs to return "1800".

import (
	"testing"

	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// TestInboundQueueAttrs_VisibilityTimeout asserts that the Slack inbound queue's
// base VisibilityTimeout is 1800 seconds (Phase 119 target), not "30" (current).
// RED: inboundQueueAttrs currently returns "30"; Wave 1 changes it to "1800".
func TestInboundQueueAttrs_VisibilityTimeout(t *testing.T) {
	attrs, err := inboundQueueAttrs("")
	if err != nil {
		t.Fatalf("inboundQueueAttrs returned unexpected error: %v", err)
	}
	got := attrs[string(sqstypes.QueueAttributeNameVisibilityTimeout)]
	const want = "1800"
	if got != want {
		t.Fatalf("inboundQueueAttrs VisibilityTimeout=%q, want %q (Phase 119: bump Slack inbound timeout to match H1/GitHub precedent so a long-running turn is not redelivered mid-flight)", got, want)
	}
}
