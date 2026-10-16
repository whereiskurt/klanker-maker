package compiler

import (
	"os/exec"
	"strings"
	"testing"
)

const replyChannelStart = "# --- km-slack-reply-channel ---"
const replyChannelEnd = "# --- end km-slack-reply-channel ---"

// runReplyChannelSnippet executes the marker-delimited snippet under bash with
// BODY set and returns the value of REPLY_CHANNEL it produced.
func runReplyChannelSnippet(t *testing.T, block, body string) string {
	t.Helper()
	start := strings.Index(block, replyChannelStart)
	end := strings.Index(block, replyChannelEnd)
	if start == -1 || end == -1 || end < start {
		t.Fatalf("reply-channel markers not found in slack poller block")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	script := "set -euo pipefail\nKM_SLACK_CHANNEL_ID=CHOME\nBODY=" + shellQuote(body) + "\n" + block[start:end] + "\nprintf '%s' \"$REPLY_CHANNEL\"\n"
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("snippet failed: %v\n%s", err, out)
	}
	return string(out)
}

func TestUserdata_SlackInbound_ReplyChannelComesFromEnvelope(t *testing.T) {
	block := slackPollerBlock(t)
	if got := runReplyChannelSnippet(t, block, `{"channel":"CFOREIGN","thread_ts":"1.0","text":"hi","user":"U1"}`); got != "CFOREIGN" {
		t.Fatalf("REPLY_CHANNEL=%q want CFOREIGN", got)
	}
	// Mapped channel: envelope channel equals the box's own → unchanged behaviour.
	if got := runReplyChannelSnippet(t, block, `{"channel":"CHOME","thread_ts":"1.0","text":"hi","user":"U1"}`); got != "CHOME" {
		t.Fatalf("REPLY_CHANNEL=%q want CHOME", got)
	}
}

func TestUserdata_SlackInbound_EveryPostUsesReplyChannel(t *testing.T) {
	block := slackPollerBlock(t)
	// After the parse, no post/permalink/upload inside the poller may name the
	// boot-time channel directly.
	tail := block[strings.Index(block, replyChannelEnd):]
	for _, bad := range []string{`--channel "$KM_SLACK_CHANNEL_ID"`} {
		if strings.Contains(tail, bad) {
			t.Fatalf("poller still posts to the boot-time channel: %s", bad)
		}
	}
	if strings.Count(tail, `--channel "$REPLY_CHANNEL"`) < 3 {
		t.Fatalf("expected the handoff + final-reply posts to use REPLY_CHANNEL; block:\n%s", abbreviateUD(tail))
	}
}

func TestUserdata_SlackInbound_DispatchExportsReplyChannel(t *testing.T) {
	block := slackPollerBlock(t)
	n := strings.Count(block, "export KM_SLACK_CHANNEL_ID='$REPLY_CHANNEL'")
	if n != 3 {
		t.Fatalf("want the per-turn channel exported in all 3 dispatch blocks, found %d", n)
	}
}

func TestUserdata_SlackInbound_SenderPreambleNamesChannel(t *testing.T) {
	block := slackPollerBlock(t)
	got := runSenderPreamble(t, block, `{"channel":"CFOREIGN","thread_ts":"1.2","text":"hi","user":"U0KURT"}`, "hi")
	if !strings.HasPrefix(got, "[Slack] From: <@U0KURT> in <#CFOREIGN>") {
		t.Fatalf("preamble must name the channel:\n%s", got)
	}
}

func TestUserdata_SlackInbound_HandoffSkippedInRoutedChannel(t *testing.T) {
	block := slackPollerBlock(t)
	if !strings.Contains(block, `if [ "$DO_SWITCH" -eq 1 ] && [ "$REPLY_CHANNEL" != "$KM_SLACK_CHANNEL_ID" ]; then`) {
		t.Fatalf("handoff must be skipped when the turn is in a routed (foreign) channel")
	}
}

func TestUserdata_NoSlackInbound_ByteIdentical(t *testing.T) {
	p := minimalSlackInboundProfile(t, false)
	out := compileInboundUserData(t, p)
	if strings.Contains(out, "REPLY_CHANNEL") {
		t.Fatal("profiles without Slack inbound must not render the reply-channel plumbing")
	}
}
