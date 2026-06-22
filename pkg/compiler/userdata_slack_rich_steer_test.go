package compiler

import (
	"strings"
	"testing"
)

// TestSlackInboundSteerEncouragesRichFormatting pins the Phase 112 follow-up
// decision: the Slack inbound-poller reply steer (the --append-system-prompt the
// poller hands Claude) must encourage native blocks-rich formatting — GFM pipe
// tables and Markdown headings — rather than the pre-Phase-111 steer that fenced
// all tabular data into flat monospace and told the agent to avoid tables.
//
// The reply path posts with --render blocks-rich (Phase 112), which only upgrades
// RAW GFM pipe tables (outside code fences) into native Slack `table` blocks and
// promotes Markdown headings into header/markdown blocks. Steering the agent to
// fence tables defeats that renderer; this test guards against re-introducing it.
func TestSlackInboundSteerEncouragesRichFormatting(t *testing.T) {
	ud := generateLearnV2Userdata(t) // learn.v2 enables Slack inbound

	// Locate the inbound-poller append-system-prompt steer.
	const anchor = "--append-system-prompt 'When replying you are posting into a Slack channel"
	idx := strings.Index(ud, anchor)
	if idx < 0 {
		t.Fatalf("Slack inbound steer prompt not found in generated userdata")
	}
	end := idx + 700
	if end > len(ud) {
		end = len(ud)
	}
	steer := ud[idx:end]

	// Must encourage the constructs blocks-rich renders natively.
	for _, want := range []string{"Markdown pipe table", "headings"} {
		if !strings.Contains(steer, want) {
			t.Errorf("Slack inbound steer should mention %q to encourage rich rendering; got:\n%s", want, steer)
		}
	}

	// Must NOT re-introduce the pre-Phase-111 fence-the-tables / avoid-tables steer.
	for _, bad := range []string{
		"tabular or columnar data in triple-backtick",
		"Prefer short bullet lists over wide Markdown tables",
	} {
		if strings.Contains(steer, bad) {
			t.Errorf("Slack inbound steer must not contain the pre-rich fence-the-tables instruction %q; got:\n%s", bad, steer)
		}
	}
}
