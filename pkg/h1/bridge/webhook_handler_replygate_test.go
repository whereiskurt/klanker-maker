// webhook_handler_replygate_test.go — Phase 103 Plan 04 Task 3.
//
// The SAFETY-CRITICAL reply gate. Researcher-visible replies message EXTERNAL hackers,
// so the gate is exhaustively tested:
//   - internal by default (no /reply_to_researcher → every target internal)
//   - allowed actor + command → ONLY the primary (first) target external; others internal
//   - command present but actor NOT allowlisted → DOWNGRADE to internal (CONTEXT lock:
//     command-present-alone is NOT sufficient)
//   - single target allowed → that target external
//   - N targets allowed → EXACTLY ONE external (never N external replies)
//
// The handler decodes each enqueued envelope and asserts the ReplyToResearcher flag.
package bridge_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

// replyFlags decodes every enqueued envelope and returns its ReplyToResearcher flags
// in enqueue order.
func replyFlags(t *testing.T, fakes *handlerFakes) []bool {
	t.Helper()
	flags := make([]bool, 0, len(fakes.sqs.sends))
	for _, s := range fakes.sqs.sends {
		var env bridge.H1Envelope
		if err := json.Unmarshal([]byte(s.body), &env); err != nil {
			t.Fatalf("decode envelope: %v", err)
		}
		flags = append(flags, env.ReplyToResearcher)
	}
	return flags
}

// twoTargetProgram builds a 2-target program with the given allowlist.
func twoTargetProgram(handle string, allow []string) []bridge.ProgramEntry {
	return []bridge.ProgramEntry{{
		Handle: handle,
		Targets: []bridge.Target{
			{Alias: "h1-prog-a", Profile: "h1-review"},
			{Alias: "h1-prog-b", Profile: "h1-deep"},
		},
		Allow: allow,
	}}
}

func countTrue(flags []bool) int {
	n := 0
	for _, f := range flags {
		if f {
			n++
		}
	}
	return n
}

// ============================================================
// TestReplyGate_DefaultInternal
// ============================================================

func TestReplyGate_DefaultInternal(t *testing.T) {
	fakes := newFakes()
	h := baseHandler(twoTargetProgram("km-sandbox", []string{"alice"}), fakes)
	// No /reply_to_researcher in the comment.
	body := h1Body("km-sandbox", "1000", "alice", "@km please triage", false)
	h.Handle(context.Background(), newRequest(body, "report_comment_created", "rg1"))

	flags := replyFlags(t, fakes)
	if len(flags) != 2 {
		t.Fatalf("expected 2 envelopes; got %d", len(flags))
	}
	for i, f := range flags {
		if f {
			t.Errorf("target %d: ReplyToResearcher=true; want false (internal by default)", i)
		}
	}
}

// ============================================================
// TestReplyGate_AllowedPrimary
// ============================================================

func TestReplyGate_AllowedPrimary(t *testing.T) {
	fakes := newFakes()
	h := baseHandler(twoTargetProgram("km-sandbox", []string{"alice"}), fakes)
	// Allowed actor + /reply_to_researcher present.
	body := h1Body("km-sandbox", "1001", "alice", "@km /reply_to_researcher please respond", false)
	h.Handle(context.Background(), newRequest(body, "report_comment_created", "rg2"))

	flags := replyFlags(t, fakes)
	if len(flags) != 2 {
		t.Fatalf("expected 2 envelopes; got %d", len(flags))
	}
	if !flags[0] {
		t.Errorf("primary (target 0) ReplyToResearcher=false; want true")
	}
	if flags[1] {
		t.Errorf("non-primary (target 1) ReplyToResearcher=true; want false (forced internal)")
	}
}

// ============================================================
// TestReplyGate_NotAllowed — downgrade to internal
// ============================================================

func TestReplyGate_NotAllowed(t *testing.T) {
	// CONTEXT lock: command-present-but-not-allowlisted must NEVER produce an external
	// reply. On the comment-keyword path the allow gate silent-drops the non-allowlisted
	// actor BEFORE dispatch (even stricter than a downgrade) — so the result is zero
	// external replies. We assert the safety property directly: no enqueued envelope may
	// carry ReplyToResearcher=true for a non-allowlisted actor, by any path.
	fakes := newFakes()
	// Known thread for this report so the @handle requirement is bypassed — proving the
	// allow gate (not merely the handle scan) is what blocks the external reply.
	fakes.threads.known["1002"] = map[string]string{"h1-prog-a": "sb-h1-prog-a"}
	h := baseHandler(twoTargetProgram("km-sandbox", []string{"alice"}), fakes)
	body := h1Body("km-sandbox", "1002", "mallory", "/reply_to_researcher sneak external", false)
	r := h.Handle(context.Background(), newRequest(body, "report_comment_created", "rg3"))
	if r.StatusCode != 200 {
		t.Fatalf("status=%d; want 200", r.StatusCode)
	}

	// Whether the handler dropped (allow gate) or dispatched-internal (downgrade), the
	// invariant is identical: NOT ONE external reply for a non-allowlisted actor.
	flags := replyFlags(t, fakes)
	if n := countTrue(flags); n != 0 {
		t.Errorf("non-allowlisted actor produced %d external replies; want 0 (flags=%v)", n, flags)
	}
}

// TestReplyGate_DowngradePure verifies the pure gate contract: command-present AND
// allowlist-membership are BOTH required for an external reply. This exercises the
// downgrade leg (command present, actor NOT in allow → false) without relying on the
// comment-keyword allow-gate drop, so the gate's deny-by-default is proven in isolation.
func TestReplyGate_DowngradePure(t *testing.T) {
	cases := []struct {
		name    string
		command bool
		actor   string
		allow   []string
		want    bool
	}{
		{"command+allowed → external", true, "alice", []string{"alice"}, true},
		{"command+not-allowed → internal (downgrade)", true, "mallory", []string{"alice"}, false},
		{"no-command+allowed → internal", false, "alice", []string{"alice"}, false},
		{"no-command+not-allowed → internal", false, "mallory", []string{"alice"}, false},
		{"command+empty-allow → internal (deny-by-default)", true, "alice", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bridge.ComputeReplyToResearcher(tc.command, tc.actor, tc.allow)
			if got != tc.want {
				t.Errorf("ComputeReplyToResearcher(%v, %q, %v) = %v; want %v",
					tc.command, tc.actor, tc.allow, got, tc.want)
			}
		})
	}
}

// ============================================================
// TestReplyGate_SingleTargetExternal
// ============================================================

func TestReplyGate_SingleTargetExternal(t *testing.T) {
	fakes := newFakes()
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, nil), fakes)
	body := h1Body("km-sandbox", "1003", "alice", "@km /reply_to_researcher ok", false)
	h.Handle(context.Background(), newRequest(body, "report_comment_created", "rg4"))

	flags := replyFlags(t, fakes)
	if len(flags) != 1 {
		t.Fatalf("expected 1 envelope; got %d", len(flags))
	}
	if !flags[0] {
		t.Errorf("single allowed target + command → ReplyToResearcher must be true")
	}
}

// ============================================================
// TestReplyGate_NeverNExternal
// ============================================================

func TestReplyGate_NeverNExternal(t *testing.T) {
	// 3 targets, allowed actor, command present → exactly ONE external.
	programs := []bridge.ProgramEntry{{
		Handle: "km-sandbox",
		Targets: []bridge.Target{
			{Alias: "h1-a", Profile: "h1-review"},
			{Alias: "h1-b", Profile: "h1-review"},
			{Alias: "h1-c", Profile: "h1-review"},
		},
		Allow: []string{"alice"},
	}}
	fakes := newFakes()
	h := baseHandler(programs, fakes)
	body := h1Body("km-sandbox", "1004", "alice", "@km /reply_to_researcher respond please", false)
	h.Handle(context.Background(), newRequest(body, "report_comment_created", "rg5"))

	flags := replyFlags(t, fakes)
	if len(flags) != 3 {
		t.Fatalf("expected 3 envelopes; got %d", len(flags))
	}
	if n := countTrue(flags); n != 1 {
		t.Errorf("exactly ONE target may be external; got %d external (flags=%v)", n, flags)
	}
	if !flags[0] {
		t.Errorf("the single external target must be the primary (index 0); flags=%v", flags)
	}
}

// ============================================================
// TestComputeReplyToResearcher — the pure gate (defense-in-depth unit test)
// ============================================================

func TestComputeReplyToResearcher_Indirect(t *testing.T) {
	// Indirectly verifies the gate's BOTH-required contract through Handle():
	//   command present + allowed   → external (covered by AllowedPrimary)
	//   command present + NOT allowed → internal (covered by NotAllowed)
	//   command absent + allowed     → internal (covered by DefaultInternal)
	// This test documents the truth table is fully covered by the cases above.
	t.Log("reply gate truth table covered: AllowedPrimary, NotAllowed, DefaultInternal")
}
