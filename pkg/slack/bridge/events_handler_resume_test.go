// events_handler_resume_test.go — Phase 114 unit tests for the EventsHandler
// step-9 resume-or-hint branch.
//
// Six scenarios:
//   1. TestEventsHandler_PausedSandbox_Resumes — happy path: StartSandbox succeeds,
//      SetStatusRunning called, PauseHinter (waking-up) posted, SQS enqueued, 200.
//   2. TestEventsHandler_PausedSandbox_OrphanDegrades — StartSandbox returns
//      ErrNoResumableInstance: OrphanHinter posted, SetStatusRunning NOT called,
//      PauseHinter NOT posted, SQS still enqueued, 200.
//   3. TestEventsHandler_PausedSandbox_TransientError — StartSandbox returns a plain
//      (non-sentinel) error: SetStatusRunning called (optimistic), PauseHinter posted,
//      SQS enqueued, no panic, 200.
//   4. TestEventsHandler_RunningSandbox_NoResume — info.Paused==false: StartSandbox
//      NOT called, SetStatusRunning NOT called, SQS enqueued.
//   5. TestEventsHandler_NilResumer_PauseHintOnly — Resumer==nil: PauseHinter called,
//      no crash, SQS enqueued, 200 (byte-identical to pre-Phase-114 behavior).
//   6. TestEventsHandler_PausedSandbox_ResumeIsSynchronous — StartSandbox and
//      SetStatusRunning call-counts are non-zero immediately after Handle returns,
//      with NO sleep/poll. This is the guard against the Phase-75.2 goroutine-freeze
//      regression: if the work were in a goroutine, the counter would still be 0 at
//      assertion time.
//
// All tests reuse fakes from events_handler_test.go (same package: bridge).
package bridge

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ---- new mocks for Phase 114 ----

// mockSandboxResumer records StartSandbox calls and returns a configurable error.
type mockSandboxResumer struct {
	mu         sync.Mutex
	startCalls int
	startedID  string
	err        error
}

func (m *mockSandboxResumer) StartSandbox(_ context.Context, sandboxID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCalls++
	m.startedID = sandboxID
	return m.err
}

func (m *mockSandboxResumer) snapshot() (calls int, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startCalls, m.startedID
}

// mockSandboxStatusWriter records SetStatusRunning calls and returns a configurable error.
type mockSandboxStatusWriter struct {
	mu              sync.Mutex
	setRunningCalls int
	runningID       string
	err             error
}

func (m *mockSandboxStatusWriter) SetStatusRunning(_ context.Context, sandboxID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setRunningCalls++
	m.runningID = sandboxID
	return m.err
}

func (m *mockSandboxStatusWriter) snapshot() (calls int, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.setRunningCalls, m.runningID
}

// ---- tests ----

// TestEventsHandler_PausedSandbox_Resumes: happy path — paused sandbox is resumed,
// status flipped, waking-up hint posted, SQS enqueued.
func TestEventsHandler_PausedSandbox_Resumes(t *testing.T) {
	now := fixedNow()
	h, sqs, _, _, sandboxes, pauseHinter, _ := newHandler(now)
	sandboxes.info = SandboxRoutingInfo{
		SandboxID: "sb-resume-01",
		QueueURL:  "https://sqs.example.com/q.fifo",
		Paused:    true,
	}

	resumer := &mockSandboxResumer{}     // success
	statusWriter := &mockSandboxStatusWriter{}
	orphanHinter := &fakePauseHinter{}  // should NOT be called on success path

	h.Resumer = resumer
	h.StatusWriter = statusWriter
	h.OrphanHinter = orphanHinter

	body := `{"type":"event_callback","event_id":"ERES1","event":{"type":"message","channel":"CRES1","user":"U1","text":"wake up","ts":"100.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{
			"x-slack-request-timestamp": tsHdr,
			"x-slack-signature":         sigHdr,
		},
		Body: body,
	})

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// SQS must be enqueued (message already enqueued at step 8 in all cases).
	if len(sqs.sends) != 1 {
		t.Fatalf("expected 1 SQS send, got %d", len(sqs.sends))
	}

	// StartSandbox must be called once with the sandbox ID.
	startCalls, startedID := resumer.snapshot()
	if startCalls != 1 {
		t.Fatalf("StartSandbox: expected 1 call, got %d", startCalls)
	}
	if startedID != "sb-resume-01" {
		t.Fatalf("StartSandbox: expected id 'sb-resume-01', got %q", startedID)
	}

	// SetStatusRunning must be called once.
	setRunningCalls, runningID := statusWriter.snapshot()
	if setRunningCalls != 1 {
		t.Fatalf("SetStatusRunning: expected 1 call, got %d", setRunningCalls)
	}
	if runningID != "sb-resume-01" {
		t.Fatalf("SetStatusRunning: expected id 'sb-resume-01', got %q", runningID)
	}

	// PauseHinter (waking-up hint) must be called once.
	hintCalls := pauseHinter.snapshot()
	if len(hintCalls) != 1 {
		t.Fatalf("PauseHinter: expected 1 call (waking-up hint), got %d", len(hintCalls))
	}
	if hintCalls[0].ch != "CRES1" {
		t.Fatalf("PauseHinter: expected channel 'CRES1', got %q", hintCalls[0].ch)
	}

	// OrphanHinter must NOT be called on the success path.
	if len(orphanHinter.snapshot()) != 0 {
		t.Fatalf("OrphanHinter must NOT be called on success path, got %d calls", len(orphanHinter.snapshot()))
	}
}

// TestEventsHandler_PausedSandbox_OrphanDegrades: StartSandbox returns a wrapped
// ErrNoResumableInstance → degraded orphan hint, no status flip, SQS still enqueued.
func TestEventsHandler_PausedSandbox_OrphanDegrades(t *testing.T) {
	now := fixedNow()
	h, sqs, _, _, sandboxes, pauseHinter, _ := newHandler(now)
	sandboxes.info = SandboxRoutingInfo{
		SandboxID: "sb-orphan-01",
		QueueURL:  "https://sqs.example.com/q.fifo",
		Paused:    true,
	}

	// Wrap ErrNoResumableInstance exactly as the adapter does.
	orphanErr := fmt.Errorf("slack-bridge: no stopped/stopping EC2 instances found: %w", ErrNoResumableInstance)
	resumer := &mockSandboxResumer{err: orphanErr}
	statusWriter := &mockSandboxStatusWriter{}
	orphanHinter := &fakePauseHinter{}

	h.Resumer = resumer
	h.StatusWriter = statusWriter
	h.OrphanHinter = orphanHinter
	// pauseHinter is already wired from newHandler; it must NOT be called on the orphan path.

	body := `{"type":"event_callback","event_id":"EORPH1","event":{"type":"message","channel":"CORPH1","user":"U1","text":"is anyone there","ts":"200.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{
			"x-slack-request-timestamp": tsHdr,
			"x-slack-signature":         sigHdr,
		},
		Body: body,
	})

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 on orphan path, got %d", resp.StatusCode)
	}

	// SQS must still be enqueued — orphan does NOT strand the message.
	if len(sqs.sends) != 1 {
		t.Fatalf("expected 1 SQS send even on orphan path, got %d", len(sqs.sends))
	}

	// StartSandbox was attempted.
	startCalls, _ := resumer.snapshot()
	if startCalls != 1 {
		t.Fatalf("StartSandbox: expected 1 attempt on orphan path, got %d", startCalls)
	}

	// SetStatusRunning must NOT be called — we don't know the instance started.
	setRunningCalls, _ := statusWriter.snapshot()
	if setRunningCalls != 0 {
		t.Fatalf("SetStatusRunning must NOT be called on orphan path, got %d calls", setRunningCalls)
	}

	// OrphanHinter must be called once with the channel.
	orphanCalls := orphanHinter.snapshot()
	if len(orphanCalls) != 1 {
		t.Fatalf("OrphanHinter: expected 1 call, got %d", len(orphanCalls))
	}
	if orphanCalls[0].ch != "CORPH1" {
		t.Fatalf("OrphanHinter: expected channel 'CORPH1', got %q", orphanCalls[0].ch)
	}

	// PauseHinter (waking-up hint) must NOT be called on the orphan path.
	if len(pauseHinter.snapshot()) != 0 {
		t.Fatalf("PauseHinter must NOT be called on orphan path, got %d calls", len(pauseHinter.snapshot()))
	}
}

// TestEventsHandler_PausedSandbox_TransientError: StartSandbox returns a plain
// (non-sentinel) error → optimistic SetStatusRunning + waking-up hint, SQS enqueued, no panic.
func TestEventsHandler_PausedSandbox_TransientError(t *testing.T) {
	now := fixedNow()
	h, sqs, _, _, sandboxes, pauseHinter, _ := newHandler(now)
	sandboxes.info = SandboxRoutingInfo{
		SandboxID: "sb-transient-01",
		QueueURL:  "https://sqs.example.com/q.fifo",
		Paused:    true,
	}

	// Plain error — NOT wrapping ErrNoResumableInstance.
	resumer := &mockSandboxResumer{err: errors.New("ec2: RequestLimitExceeded")}
	statusWriter := &mockSandboxStatusWriter{}
	orphanHinter := &fakePauseHinter{}

	h.Resumer = resumer
	h.StatusWriter = statusWriter
	h.OrphanHinter = orphanHinter

	body := `{"type":"event_callback","event_id":"ETRANS1","event":{"type":"message","channel":"CTRANS1","user":"U1","text":"throttled box","ts":"300.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{
			"x-slack-request-timestamp": tsHdr,
			"x-slack-signature":         sigHdr,
		},
		Body: body,
	})

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 on transient error, got %d", resp.StatusCode)
	}

	// SQS must be enqueued — transient error must never strand the message.
	if len(sqs.sends) != 1 {
		t.Fatalf("expected 1 SQS send on transient error path, got %d", len(sqs.sends))
	}

	// SetStatusRunning must be called optimistically.
	setRunningCalls, runningID := statusWriter.snapshot()
	if setRunningCalls != 1 {
		t.Fatalf("SetStatusRunning: expected 1 optimistic call on transient error, got %d", setRunningCalls)
	}
	if runningID != "sb-transient-01" {
		t.Fatalf("SetStatusRunning: expected id 'sb-transient-01', got %q", runningID)
	}

	// PauseHinter (waking-up hint) must be called even on transient error.
	hintCalls := pauseHinter.snapshot()
	if len(hintCalls) != 1 {
		t.Fatalf("PauseHinter: expected 1 call on transient error, got %d", len(hintCalls))
	}

	// OrphanHinter must NOT be called (transient error is not the orphan path).
	if len(orphanHinter.snapshot()) != 0 {
		t.Fatalf("OrphanHinter must NOT be called on transient error, got %d calls", len(orphanHinter.snapshot()))
	}
}

// TestEventsHandler_RunningSandbox_NoResume: when info.Paused==false, step 9 must
// not call StartSandbox, SetStatusRunning, or OrphanHinter.
func TestEventsHandler_RunningSandbox_NoResume(t *testing.T) {
	now := fixedNow()
	h, sqs, _, _, sandboxes, pauseHinter, _ := newHandler(now)
	// Default sandboxes.info from newHandler has Paused=false — confirm.
	sandboxes.info = SandboxRoutingInfo{
		SandboxID: "sb-running-01",
		QueueURL:  "https://sqs.example.com/q.fifo",
		Paused:    false,
	}

	resumer := &mockSandboxResumer{}
	statusWriter := &mockSandboxStatusWriter{}
	orphanHinter := &fakePauseHinter{}

	h.Resumer = resumer
	h.StatusWriter = statusWriter
	h.OrphanHinter = orphanHinter

	body := `{"type":"event_callback","event_id":"ERUNNING1","event":{"type":"message","channel":"CRUN1","user":"U1","text":"already running","ts":"400.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{
			"x-slack-request-timestamp": tsHdr,
			"x-slack-signature":         sigHdr,
		},
		Body: body,
	})

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 on running sandbox, got %d", resp.StatusCode)
	}

	// SQS must be enqueued — running path unchanged.
	if len(sqs.sends) != 1 {
		t.Fatalf("expected 1 SQS send on running path, got %d", len(sqs.sends))
	}

	// StartSandbox must NOT be called on the running path.
	startCalls, _ := resumer.snapshot()
	if startCalls != 0 {
		t.Fatalf("StartSandbox must NOT be called on running sandbox, got %d calls", startCalls)
	}

	// SetStatusRunning must NOT be called.
	setRunningCalls, _ := statusWriter.snapshot()
	if setRunningCalls != 0 {
		t.Fatalf("SetStatusRunning must NOT be called on running sandbox, got %d calls", setRunningCalls)
	}

	// PauseHinter must NOT be called on the running path.
	if len(pauseHinter.snapshot()) != 0 {
		t.Fatalf("PauseHinter must NOT be called on running sandbox, got %d calls", len(pauseHinter.snapshot()))
	}

	// OrphanHinter must NOT be called.
	if len(orphanHinter.snapshot()) != 0 {
		t.Fatalf("OrphanHinter must NOT be called on running sandbox, got %d calls", len(orphanHinter.snapshot()))
	}
}

// TestEventsHandler_NilResumer_PauseHintOnly: when Resumer==nil and PauseHinter is wired,
// the behavior is byte-identical to pre-Phase-114 — only PauseHinter is called.
// This is the nil-invariant back-compat guard.
func TestEventsHandler_NilResumer_PauseHintOnly(t *testing.T) {
	now := fixedNow()
	h, sqs, _, _, sandboxes, pauseHinter, _ := newHandler(now)
	sandboxes.info = SandboxRoutingInfo{
		SandboxID: "sb-nilresumer",
		QueueURL:  "https://sqs.example.com/q.fifo",
		Paused:    true,
	}

	// nil Resumer — byte-identical to pre-Phase-114.
	h.Resumer = nil
	orphanHinter := &fakePauseHinter{}
	h.OrphanHinter = orphanHinter
	// StatusWriter nil too — consistent with nil Resumer pre-114 state.
	h.StatusWriter = nil

	body := `{"type":"event_callback","event_id":"ENILRES1","event":{"type":"message","channel":"CNILRES1","user":"U1","text":"still paused","ts":"500.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{
			"x-slack-request-timestamp": tsHdr,
			"x-slack-signature":         sigHdr,
		},
		Body: body,
	})

	if resp.StatusCode != 200 {
		t.Fatalf("nil Resumer must not affect response, got %d", resp.StatusCode)
	}

	// SQS must be enqueued.
	if len(sqs.sends) != 1 {
		t.Fatalf("nil Resumer must not block SQS write, got %d sends", len(sqs.sends))
	}

	// PauseHinter must be called (byte-identical to pre-Phase-114).
	hintCalls := pauseHinter.snapshot()
	if len(hintCalls) != 1 {
		t.Fatalf("PauseHinter must be called when Resumer==nil, got %d calls", len(hintCalls))
	}

	// OrphanHinter must NOT be called (nil Resumer takes the else branch, not the orphan branch).
	if len(orphanHinter.snapshot()) != 0 {
		t.Fatalf("OrphanHinter must NOT be called when Resumer==nil, got %d calls", len(orphanHinter.snapshot()))
	}
}

// TestEventsHandler_PausedSandbox_ResumeIsSynchronous: this test asserts that
// StartSandbox and SetStatusRunning are both called by the time Handle returns,
// with NO sleep or poll. This is the regression guard against the Phase-75.2
// goroutine-freeze anti-pattern: if the work were in a goroutine, the counters
// would still be 0 immediately after Handle returns and this test would fail.
//
// Do NOT add time.Sleep here. The entire point of this test is that it works
// without sleeping.
func TestEventsHandler_PausedSandbox_ResumeIsSynchronous(t *testing.T) {
	now := fixedNow()
	h, _, _, _, sandboxes, _, _ := newHandler(now)
	sandboxes.info = SandboxRoutingInfo{
		SandboxID: "sb-sync-01",
		QueueURL:  "https://sqs.example.com/q.fifo",
		Paused:    true,
	}

	resumer := &mockSandboxResumer{}     // success (no error)
	statusWriter := &mockSandboxStatusWriter{}

	h.Resumer = resumer
	h.StatusWriter = statusWriter

	body := `{"type":"event_callback","event_id":"ESYNC1","event":{"type":"message","channel":"CSYNC1","user":"U1","text":"sync test","ts":"600.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)

	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{
			"x-slack-request-timestamp": tsHdr,
			"x-slack-signature":         sigHdr,
		},
		Body: body,
	})

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Assert IMMEDIATELY after Handle returns — NO sleep, NO poll.
	// If StartSandbox or SetStatusRunning were in a goroutine, these would be 0.
	startCalls, _ := resumer.snapshot()
	if startCalls != 1 {
		t.Fatalf("[Phase-75.2 goroutine regression guard] StartSandbox must be called SYNCHRONOUSLY: expected call count 1 immediately after Handle returns, got %d. A count of 0 means the work is in a goroutine (Lambda-freeze bug).", startCalls)
	}

	setRunningCalls, _ := statusWriter.snapshot()
	if setRunningCalls != 1 {
		t.Fatalf("[Phase-75.2 goroutine regression guard] SetStatusRunning must be called SYNCHRONOUSLY: expected call count 1 immediately after Handle returns, got %d. A count of 0 means the work is in a goroutine (Lambda-freeze bug).", setRunningCalls)
	}
}

// fixedNow returns a fixed time.Time for deterministic signing in tests.
// Distinct from time.Now() to make tests independent of wall-clock drift.
func fixedNow() time.Time {
	return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
}
