// events_handler_allowlist_test.go — Phase 118 Wave-0 RED tests for the
// install-level and per-sandbox trigger allowlist feature (AC2/AC3/AC4/AC5/AC8).
//
// These tests are written BEFORE the enforcement logic exists (Plan 05). They
// define the acceptance-criteria contract and are EXPECTED to fail partially:
//
//   - AC4 (empty allowlist → everyone allowed) and AC8 (no allowlist → byte-identical)
//     PASS immediately because without enforcement every user dispatches.
//   - AC2 (install-level allow), AC3 (per-sandbox allow REPLACES install), and
//     AC5 (thread-bypass does NOT exempt from allowlist) are RED until Plan 05.
//
// Sentinel user IDs used throughout:
//
//	U_OP    = "U0OPERATOR"   — listed in the install-level allowlist
//	U_X     = "U0XUSER"      — listed in a per-sandbox allowlist override
//	U_OTHER = "U0OTHER"      — not in any allowlist
//
// Harness: reuses all mocks (fakeSigningSecret, fakeBotUserID, fakeNonces,
// fakeSandboxes, fakeThreads, fakeSQS, fakeReactor) and helpers (newHandler,
// signSlackPayload) declared in events_handler_test.go (same package).
package bridge

import (
	"context"
	"fmt"
	"testing"
	"time"
)

const (
	uOp    = "U0OPERATOR"
	uX     = "U0XUSER"
	uOther = "U0OTHER"
)

// buildSignedRequest creates a signed EventsRequest for the given user and
// unique event-id suffix (to avoid nonce collisions between sub-tests).
func buildSignedRequest(t *testing.T, now time.Time, user, eventID string) EventsRequest {
	t.Helper()
	body := fmt.Sprintf(
		`{"type":"event_callback","event_id":%q,"event":{"type":"message","channel":"C1","user":%q,"text":"hello","ts":"1.0"}}`,
		eventID, user,
	)
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	return EventsRequest{
		Headers: map[string]string{
			"x-slack-request-timestamp": tsHdr,
			"x-slack-signature":         sigHdr,
		},
		Body: body,
	}
}

// TestEventsHandler_Allowlist — AC2.
//
// Install-level Allow=[U_OP]:
//   - A message from U_OP is dispatched (SQS enqueued, reaction posted).
//   - A message from U_OTHER is silently dropped (no SQS, no reaction, 200 OK).
//
// This test is RED until Plan 05 implements the allow check in Handle().
func TestEventsHandler_Allowlist(t *testing.T) {
	tests := []struct {
		name         string
		user         string
		wantDispatched bool
	}{
		{"operator in allowlist → dispatched", uOp, true},
		{"other user not in allowlist → silent drop", uOther, false},
	}

	now := time.Now()

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, sqs, _, _, _, _, reactor := newHandler(now)
			h.Allow = []string{uOp} // install-level allowlist

			req := buildSignedRequest(t, now, tc.user, fmt.Sprintf("AC2-%d", i))
			resp := h.Handle(context.Background(), req)
			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, resp.Body)
			}

			if tc.wantDispatched {
				if len(sqs.sends) == 0 {
					t.Error("expected SQS dispatch for listed user, got none")
				}
			} else {
				// RED: until Plan 05, enforcement is absent → U_OTHER still dispatches.
				// This assertion will fail (red) until Plan 05 turns it green.
				if len(sqs.sends) != 0 {
					t.Errorf("[RED until Plan 05] expected NO SQS for unlisted user, got %d sends", len(sqs.sends))
				}
				if len(reactor.snapshot()) != 0 {
					t.Errorf("[RED until Plan 05] expected NO reaction for unlisted user, got %d reactions", len(reactor.snapshot()))
				}
			}
		})
	}
}

// TestEventsHandler_PerSandboxAllowOverrides — AC3.
//
// Install-level Allow=[U_OP]; per-sandbox SandboxRoutingInfo.Allow=[U_X].
// When per-sandbox is non-empty it REPLACES install-level entirely:
//   - U_X is dispatched (in per-sandbox list).
//   - U_OP is dropped (in install list only, NOT in per-sandbox list).
//
// This test is RED until Plan 05.
func TestEventsHandler_PerSandboxAllowOverrides(t *testing.T) {
	tests := []struct {
		name           string
		user           string
		wantDispatched bool
	}{
		{"U_X in per-sandbox list → dispatched", uX, true},
		{"U_OP only in install list → per-sandbox replaces → dropped", uOp, false},
	}

	now := time.Now()

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, sqs, _, _, sandboxes, _, reactor := newHandler(now)
			h.Allow = []string{uOp}                // install-level
			sandboxes.info.Allow = []string{uX}   // per-sandbox REPLACES install

			req := buildSignedRequest(t, now, tc.user, fmt.Sprintf("AC3-%d", i))
			resp := h.Handle(context.Background(), req)
			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, resp.Body)
			}

			if tc.wantDispatched {
				if len(sqs.sends) == 0 {
					t.Error("expected SQS dispatch for per-sandbox-listed user, got none")
				}
			} else {
				// RED: until Plan 05 enforces the per-sandbox override.
				if len(sqs.sends) != 0 {
					t.Errorf("[RED until Plan 05] expected NO SQS when per-sandbox replaces install list, got %d sends", len(sqs.sends))
				}
				if len(reactor.snapshot()) != 0 {
					t.Errorf("[RED until Plan 05] expected NO reaction for non-per-sandbox user, got %d", len(reactor.snapshot()))
				}
			}
		})
	}
}

// TestEventsHandler_AllowlistEmpty_EveryoneAllowed — AC4.
//
// Both install-level Allow and per-sandbox Allow are nil/empty. Any user is
// dispatched. This passes immediately (no enforcement = no block).
func TestEventsHandler_AllowlistEmpty_EveryoneAllowed(t *testing.T) {
	tests := []struct {
		name string
		user string
	}{
		{"U_OP allowed when lists empty", uOp},
		{"U_X allowed when lists empty", uX},
		{"U_OTHER allowed when lists empty", uOther},
	}

	now := time.Now()

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, sqs, _, _, _, _, _ := newHandler(now)
			// Explicitly ensure both lists are nil/empty (default from newHandler).
			h.Allow = nil
			// sandboxes.info.Allow is nil by default from newHandler.

			req := buildSignedRequest(t, now, tc.user, fmt.Sprintf("AC4-%d", i))
			resp := h.Handle(context.Background(), req)
			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, resp.Body)
			}
			if len(sqs.sends) == 0 {
				t.Errorf("expected SQS dispatch for any user when allowlist empty, got none")
			}
		})
	}
}

// TestEventsHandler_Allowlist_ThreadBypassDoesNotExempt — AC5.
//
// MentionOnly=true, active thread (Phase 91.3 thread-bypass would skip the
// mention requirement). But U_OTHER is NOT in the install-level allowlist.
//
// Expected: U_OTHER is still silently dropped even though thread-bypass would
// let it skip the mention check. The allowlist is the OUTER gate; it runs
// before mention/thread logic.
//
// This test is RED until Plan 05 implements the outer allow check.
func TestEventsHandler_Allowlist_ThreadBypassDoesNotExempt(t *testing.T) {
	now := time.Now()

	h, sqs, threads, _, _, _, reactor := newHandler(now)
	h.Allow = []string{uOp}   // install-level allowlist
	h.MentionOnly = true

	// Seed a known thread so Phase 91.3 thread-bypass would skip mention check.
	// threads.sandboxByThread["C1|1.0"] = "sb-abc" → bypass kicks in for replies in this thread.
	threads.sandboxByThread = map[string]string{"C1|1.0": "sb-abc"}

	// U_OTHER sends a thread reply — would normally bypass mention filter via Phase 91.3,
	// but allowlist should drop it before that.
	body := fmt.Sprintf(
		`{"type":"event_callback","event_id":"AC5-1","event":{"type":"message","channel":"C1","user":%q,"text":"reply without mention","ts":"2.0","thread_ts":"1.0"}}`,
		uOther,
	)
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{
			"x-slack-request-timestamp": tsHdr,
			"x-slack-signature":         sigHdr,
		},
		Body: body,
	})

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, resp.Body)
	}

	// RED: until Plan 05, U_OTHER is dispatched (no enforcement yet).
	if len(sqs.sends) != 0 {
		t.Errorf("[RED until Plan 05] expected NO SQS for unlisted user even with thread-bypass active, got %d sends", len(sqs.sends))
	}
	if len(reactor.snapshot()) != 0 {
		t.Errorf("[RED until Plan 05] expected NO reaction for unlisted user even with thread-bypass, got %d reactions", len(reactor.snapshot()))
	}
}

// TestEventsHandler_NoAllowlistSet_ByteIdentical — AC8.
//
// With no Allow field set anywhere (nil on both handler and sandbox routing
// info), behavior must be byte-identical to pre-Phase-118. Any user is
// dispatched, the existing test suite remains green.
//
// This passes immediately and serves as the non-regression anchor.
func TestEventsHandler_NoAllowlistSet_ByteIdentical(t *testing.T) {
	now := time.Now()

	h, sqs, threads, _, _, _, reactor := newHandler(now)
	// Ensure no allowlist is set (zero values).
	if h.Allow != nil {
		t.Fatal("precondition: h.Allow must be nil for byte-identical check")
	}

	req := buildSignedRequest(t, now, uOther, "AC8-1")
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, resp.Body)
	}
	if len(sqs.sends) == 0 {
		t.Error("expected SQS dispatch with no allowlist (everyone allowed), got none")
	}
	if len(threads.upserts) == 0 {
		t.Error("expected threads upsert with no allowlist, got none")
	}
	if len(reactor.snapshot()) == 0 {
		t.Error("expected reaction with no allowlist, got none")
	}
}
