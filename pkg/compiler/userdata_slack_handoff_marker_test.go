package compiler

import (
	"strings"
	"testing"
)

// TestUserdata_SlackHandoffThreadedPostsSkipPostedMarker pins that the Phase 70
// cross-agent handoff's threaded posts run with KM_SLACK_POSTED_MARKER=0.
//
// The handoff runs as ROOT inside the poller, so a threaded km-slack post there
// would touch /tmp/km-slack-posted.<old thread> as root. The sandbox user's later
// posts into that thread then fail to write the marker (permission denied, seen
// live 2026-10-18). The handoff's posts are not the agent's answer, so they must
// not set the marker at all.
func TestUserdata_SlackHandoffThreadedPostsSkipPostedMarker(t *testing.T) {
	out := compileInboundUserData(t, minimalSlackInboundProfile(t, true))

	const startAnchor = "# Phase 70 Plan 70-06: cross-agent switch sequence"
	const endAnchor = "# Step 8"
	start := strings.Index(out, startAnchor)
	if start < 0 {
		t.Fatalf("handoff block start anchor %q not found", startAnchor)
	}
	end := strings.Index(out[start:], endAnchor)
	if end < 0 {
		t.Fatalf("handoff block end anchor %q not found after start", endAnchor)
	}
	block := out[start : start+end]

	// Join backslash-continued lines so each command is one logical line.
	joined := strings.ReplaceAll(block, "\\\n", " ")

	threaded := 0
	for _, line := range strings.Split(joined, "\n") {
		if !strings.Contains(line, "km-slack post") || !strings.Contains(line, "--thread") {
			continue
		}
		threaded++
		if !strings.Contains(line, "KM_SLACK_POSTED_MARKER=0 /opt/km/bin/km-slack post") {
			t.Errorf("threaded km-slack post in the handoff block lacks KM_SLACK_POSTED_MARKER=0:\n%s", strings.TrimSpace(line))
		}
	}
	if threaded < 2 {
		t.Fatalf("expected at least 2 threaded km-slack posts in the handoff block (Step 4 error, Step 6 handoff), found %d", threaded)
	}
}
