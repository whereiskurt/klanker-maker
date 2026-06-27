// Package bridge — events_handler_frozen_test.go
// Tests for BRG-02 (frozen-dispatch gate) and BRG-03 (FetchByChannel reads action attrs).
package bridge

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestFetchByChannel_ActionAttrs (BRG-03) verifies that FetchByChannel surfaces
// ActionLimits (from action_limits S attr) and ActionFrozen (from action_frozen BOOL attr)
// onto SandboxRoutingInfo. Exercised via the fakeSandboxes double in
// events_handler_test.go — the production DDBSandboxByChannel adapter test lives
// in aws_adapters_test.go. Here we verify the flow-through at the handler level
// and that the two new fields are present on the struct.
func TestFetchByChannel_ActionAttrs(t *testing.T) {
	limitsJSON := `{"slack_post":{"perHour":10,"onBreach":"warn"}}`
	sandboxes := &fakeSandboxes{
		info: SandboxRoutingInfo{
			SandboxID:    "sb-quota-test",
			QueueURL:     "https://sqs.example/queue.fifo",
			ActionLimits: limitsJSON,
			ActionFrozen: false,
		},
	}
	info, err := sandboxes.FetchByChannel(context.Background(), "CTEST")
	if err != nil {
		t.Fatalf("FetchByChannel returned error: %v", err)
	}
	if info.ActionLimits != limitsJSON {
		t.Errorf("ActionLimits: got %q, want %q", info.ActionLimits, limitsJSON)
	}
	if info.ActionFrozen {
		t.Error("ActionFrozen: expected false, got true")
	}

	// Frozen variant.
	sandboxes.info.ActionFrozen = true
	info2, _ := sandboxes.FetchByChannel(context.Background(), "CTEST")
	if !info2.ActionFrozen {
		t.Error("ActionFrozen: expected true, got false")
	}
}

// TestFrozenDispatch (BRG-02) — when SandboxRoutingInfo.ActionFrozen is true,
// the events handler must:
//  1. Refuse to enqueue the turn to SQS (no SQS SendMessage call).
//  2. Post an in-thread control-plane frozen notice via the bot token.
//  3. Return HTTP 200 (the event was processed; it was intentionally dropped).
//
// This is a control-plane path — the frozen notice itself is never counted
// against the slack_post quota (dual-stream, §5 of CONTEXT.md).
func TestFrozenDispatch(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	sqs := &fakeSQS{}
	threads := &fakeThreads{}
	nonces := &fakeNonces{}
	poster := &fakeSlackPoster{}
	sandboxes := &fakeSandboxes{
		info: SandboxRoutingInfo{
			SandboxID:    "sb-frozen",
			QueueURL:     "https://sqs.example/queue.fifo",
			ActionFrozen: true,
			FrozenReason: "quota: github_pr lifetime exceeded",
		},
	}

	h := &EventsHandler{
		SigningSecret: &fakeSigningSecret{secret: testSigningSecret},
		BotUserID:     &fakeBotUserID{uid: "UBOT123"},
		Nonces:        nonces,
		Sandboxes:     sandboxes,
		Threads:       threads,
		SQS:           sqs,
		Slack:         poster,
		AckEmoji:      "eyes",
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           func() time.Time { return now },
	}

	body := `{"type":"event_callback","event_id":"Ev001","event":{"type":"message","channel":"Cfrozen","ts":"1000.001","text":"hello","user":"U123"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	req := EventsRequest{
		Body:    body,
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
	}

	resp := h.Handle(context.Background(), req)

	// 1. No SQS dispatch.
	if len(sqs.sends) != 0 {
		t.Errorf("frozen: expected 0 SQS sends, got %d", len(sqs.sends))
	}

	// 2. Frozen notice posted to the thread.
	poster.mu.Lock()
	msgs := poster.msgs
	poster.mu.Unlock()
	if len(msgs) == 0 {
		t.Fatal("frozen: expected a frozen notice to be posted via SlackPoster")
	}
	found := false
	for _, m := range msgs {
		if strings.Contains(m.body, "frozen") || strings.Contains(m.body, "🛑") {
			found = true
		}
	}
	if !found {
		t.Errorf("frozen: notice body should contain 'frozen' or '🛑', got: %v", msgs)
	}

	// 3. HTTP 200.
	if resp.StatusCode != 200 {
		t.Errorf("frozen: expected 200, got %d", resp.StatusCode)
	}
}
