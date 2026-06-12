// webhook_handler_phase109_test.go — Phase 109 (H1 parity) tests for the orphaned
// stopped-row self-heal: when the resume target has no resumable EC2 instance
// (ErrNoResumableInstance), the bridge deletes the stale km-sandboxes row and
// cold-creates instead of enqueuing to a dead per-sandbox h1-inbound FIFO queue.
//
// Mirrors pkg/github/bridge/webhook_handler_phase109_test.go. Reuses the fakes
// from webhook_handler_test.go (fakeResolver/fakeResumer/fakeSQS/fakePublisher/
// fakeThreadStore/fakeStatusWriter).
package bridge_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

// TestHandle_OrphanedStoppedRow_ColdCreates verifies the core Phase 109 behavior on
// the H1 bridge: a status=stopped alias whose EC2 instance is gone (StartSandbox
// returns a wrapped ErrNoResumableInstance) must delete the stale row and cold-create
// — NOT enqueue to the dead queue, and NOT upsert a thread row.
func TestHandle_OrphanedStoppedRow_ColdCreates(t *testing.T) {
	fakes := newFakes()
	fakes.resolver.statuses["h1-km-sandbox"] = "stopped"
	// The instance is gone → StartSandbox returns the terminal sentinel (wrapped).
	fakes.resumer.err = fmt.Errorf("h1-bridge: no stopped/stopping EC2 instances found: %w", bridge.ErrNoResumableInstance)

	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, nil), fakes)
	body := h1Body("km-sandbox", "900", "alice", "@km go", false)
	r := h.Handle(context.Background(), newRequest(body, "report_comment_created", "p109-1"))
	if r.StatusCode != 200 {
		t.Fatalf("status=%d; want 200", r.StatusCode)
	}

	// Resumer was attempted.
	if len(fakes.resumer.started) != 1 {
		t.Errorf("StartSandbox started=%d; want 1", len(fakes.resumer.started))
	}
	// Stale row MUST be deleted (so the alias becomes absent → no ambiguous-alias trap).
	if len(fakes.statusWriter.deleteCalls) != 1 {
		t.Fatalf("DeleteSandboxRow calls=%d; want 1", len(fakes.statusWriter.deleteCalls))
	}
	if fakes.statusWriter.deleteCalls[0] != "sb-h1-km-sandbox" {
		t.Errorf("DeleteSandboxRow sandbox_id=%q; want sb-h1-km-sandbox", fakes.statusWriter.deleteCalls[0])
	}
	// Cold-create MUST fire.
	if len(fakes.publisher.calls) != 1 {
		t.Errorf("PutSandboxCreate calls=%d; want 1 (cold-create fallback)", len(fakes.publisher.calls))
	}
	// Enqueue MUST NOT happen — the per-sandbox queue has no live poller.
	if len(fakes.sqs.sends) != 0 {
		t.Errorf("SQS sends=%d; want 0 (dead queue, no enqueue)", len(fakes.sqs.sends))
	}
	// No thread upsert — there is no live sandbox_id to record.
	if len(fakes.threads.upserts) != 0 {
		t.Errorf("Threads.Upsert calls=%d; want 0", len(fakes.threads.upserts))
	}
	// SetStatusRunning must NOT be called (resume did not succeed).
	if len(fakes.statusWriter.setCalls) != 0 {
		t.Errorf("SetStatusRunning calls=%d; want 0 (instance gone)", len(fakes.statusWriter.setCalls))
	}
}

// TestHandle_TransientResumeError_EnqueueContinues verifies no regression: a transient
// StartSandbox error (NOT wrapping the sentinel) keeps the existing log-non-fatal +
// enqueue behavior so the FIFO redelivers once the box recovers.
func TestHandle_TransientResumeError_EnqueueContinues(t *testing.T) {
	fakes := newFakes()
	fakes.resolver.statuses["h1-km-sandbox"] = "stopped"
	fakes.resumer.err = errString("throttled") // NOT the sentinel

	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, nil), fakes)
	body := h1Body("km-sandbox", "901", "alice", "@km go", false)
	r := h.Handle(context.Background(), newRequest(body, "report_comment_created", "p109-2"))
	if r.StatusCode != 200 {
		t.Fatalf("status=%d; want 200", r.StatusCode)
	}

	// Enqueue continues (transient error → FIFO redelivers later).
	if len(fakes.sqs.sends) != 1 {
		t.Errorf("SQS sends=%d; want 1 (enqueue continues on transient error)", len(fakes.sqs.sends))
	}
	if len(fakes.publisher.calls) != 0 {
		t.Errorf("PutSandboxCreate calls=%d; want 0 on transient error", len(fakes.publisher.calls))
	}
	if len(fakes.statusWriter.deleteCalls) != 0 {
		t.Errorf("DeleteSandboxRow calls=%d; want 0 on transient error", len(fakes.statusWriter.deleteCalls))
	}
}
