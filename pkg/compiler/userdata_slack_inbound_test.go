package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// minimalSlackInboundProfile returns a SandboxProfile with the minimum fields
// required for Slack inbound tests. inbound controls NotifySlackInboundEnabled.
func minimalSlackInboundProfile(t *testing.T, inbound bool) *profile.SandboxProfile {
	t.Helper()
	slackEnabled := true
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{
		NotifySlackEnabled:        &slackEnabled,
		NotifySlackPerSandbox:     true,
		NotifySlackInboundEnabled: inbound,
	}
	return p
}

// compileInboundUserData is a thin wrapper around generateUserData for inbound tests.
// It always uses "my-bucket" and "sb-si-test" as stable test inputs.
func compileInboundUserData(t *testing.T, p *profile.SandboxProfile) string {
	t.Helper()
	out, err := generateUserData(p, "sb-si-test", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	return out
}

// abbreviateUD trims user-data output for failure message readability.
func abbreviateUD(s string) string {
	if len(s) > 1500 {
		return s[:750] + "\n...[truncated]...\n" + s[len(s)-750:]
	}
	return s
}

// TestUserdata_SlackInboundPollerEmitted verifies that when notifySlackInboundEnabled=true
// the user-data string contains all four required Slack-inbound substrings.
func TestUserdata_SlackInboundPollerEmitted(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)

	must := []string{
		"/opt/km/bin/km-slack-inbound-poller",
		"/etc/systemd/system/km-slack-inbound-poller.service",
		"KM_SLACK_INBOUND_QUEUE_URL",
		"km-slack-inbound-poller", // systemctl enable list
	}
	for _, s := range must {
		if !strings.Contains(out, s) {
			t.Fatalf("expected user-data to contain %q\n--- output excerpt ---\n%s", s, abbreviateUD(out))
		}
	}
}

// TestUserdata_SlackInboundPollerSkipped verifies that when notifySlackInboundEnabled=false
// the user-data string contains NONE of the Slack-inbound substrings.
func TestUserdata_SlackInboundPollerSkipped(t *testing.T) {
	p := minimalSlackInboundProfile(t, false)
	out := compileInboundUserData(t, p)

	forbidden := []string{
		"km-slack-inbound-poller",
		"KM_SLACK_INBOUND_QUEUE_URL",
		"km-slack-inbound-poller.service",
	}
	for _, s := range forbidden {
		if strings.Contains(out, s) {
			t.Fatalf("user-data must not contain %q when inbound disabled\n--- excerpt ---\n%s", s, abbreviateUD(out))
		}
	}
}

// TestUserdata_SlackInboundEnvVar verifies that when inbound is enabled
// /etc/profile.d/km-notify-env.sh exports both KM_SLACK_INBOUND_QUEUE_URL and
// KM_SLACK_THREADS_TABLE, and that neither appears when inbound is disabled.
func TestUserdata_SlackInboundEnvVar(t *testing.T) {
	// Enabled path
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)

	if !strings.Contains(out, "KM_SLACK_INBOUND_QUEUE_URL=") {
		t.Fatalf("env file must export KM_SLACK_INBOUND_QUEUE_URL when inbound enabled\n%s", abbreviateUD(out))
	}
	// KM_SLACK_THREADS_TABLE must be exported — Phase 66 multi-instance override
	// propagates only via this export; the bash poller's default can't see a non-default prefix.
	if !strings.Contains(out, "KM_SLACK_THREADS_TABLE=") {
		t.Fatalf("env file must export KM_SLACK_THREADS_TABLE when inbound enabled\n%s", abbreviateUD(out))
	}

	// Disabled path
	p2 := minimalSlackInboundProfile(t, false)
	out2 := compileInboundUserData(t, p2)
	if strings.Contains(out2, "KM_SLACK_INBOUND_QUEUE_URL") {
		t.Fatalf("disabled inbound must not export KM_SLACK_INBOUND_QUEUE_URL")
	}
	if strings.Contains(out2, "KM_SLACK_THREADS_TABLE") {
		t.Fatalf("disabled inbound must not export KM_SLACK_THREADS_TABLE")
	}
}

// TestUserdata_SlackInboundSystemctlEnable verifies that when inbound is enabled
// the systemctl enable line contains km-slack-inbound-poller and has no double spaces.
func TestUserdata_SlackInboundSystemctlEnable(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)

	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "systemctl enable") &&
			strings.Contains(line, "km-slack-inbound-poller") {
			if strings.Contains(line, "  ") {
				t.Fatalf("malformed systemctl line (double space): %q", line)
			}
			return
		}
	}
	t.Fatalf("did not find systemctl enable line containing km-slack-inbound-poller\n%s", abbreviateUD(out))
}

// TestUserdata_SlackInboundThreadFlag verifies that the km-notify-hook Slack branch
// reads KM_SLACK_THREAD_TS and passes --thread to km-slack post (Phase 67 consumes
// the flag that was wired but unused in Phase 63).
func TestUserdata_SlackInboundThreadFlag(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)

	if !strings.Contains(out, "KM_SLACK_THREAD_TS") {
		t.Fatalf("km-notify-hook must reference KM_SLACK_THREAD_TS\n%s", abbreviateUD(out))
	}
	if !strings.Contains(out, "THREAD_FLAG") {
		t.Fatalf("km-notify-hook must define THREAD_FLAG variable\n%s", abbreviateUD(out))
	}
	if !strings.Contains(out, "$THREAD_FLAG") {
		t.Fatalf("km-notify-hook must pass $THREAD_FLAG to km-slack post\n%s", abbreviateUD(out))
	}
}

// extractSlackInboundPoller returns the SLACKINBOUND heredoc body from rendered
// userdata. Used by Phase 67-11 Gap A tests to bound substring assertions to
// the inbound poller and avoid matching unrelated km-notify-hook content.
func extractSlackInboundPoller(t *testing.T, out string) string {
	t.Helper()
	startMarker := "<< 'SLACKINBOUND'"
	endMarker := "\nSLACKINBOUND\n"
	start := strings.Index(out, startMarker)
	end := strings.Index(out, endMarker)
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("SLACKINBOUND heredoc markers not found in rendered userdata\n--- excerpt ---\n%s", abbreviateUD(out))
	}
	return out[start:end]
}

// TestUserdata_PollerPostsResultToSlack — Phase 67-11 Gap A.
// The inbound poller must read .result from output.json and post it to Slack
// directly (replacing the Stop hook's unreliable transcript-JSONL scrape).
func TestUserdata_PollerPostsResultToSlack(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)
	poller := extractSlackInboundPoller(t, out)

	if !strings.Contains(poller, `jq -r '.result // empty'`) {
		t.Fatalf("poller missing .result extraction from output.json\n%s", abbreviateUD(poller))
	}
	if !strings.Contains(poller, `/opt/km/bin/km-slack post`) {
		t.Fatalf("poller missing km-slack post invocation\n%s", abbreviateUD(poller))
	}
	if !strings.Contains(poller, `--thread "$THREAD_TS"`) {
		t.Fatalf("poller missing --thread \"$THREAD_TS\" flag\n%s", abbreviateUD(poller))
	}
	if !strings.Contains(poller, "KM_SLACK_INBOUND_REPLY_HANDLED=1") {
		t.Fatalf("poller missing KM_SLACK_INBOUND_REPLY_HANDLED=1 export\n%s", abbreviateUD(poller))
	}
}

// TestUserdata_PollerResolvesChannelAndBridgeFromSSM — Phase 67-11 Gap A.
// The new ROOT-side post block runs OUTSIDE the existing `sudo -u sandbox bash -c`
// invocation that re-sources /etc/profile.d/*.sh, and the systemd unit's
// EnvironmentFile only loads /etc/profile.d/km-notify-env.sh — NOT
// km-slack-runtime.sh. Therefore the poller must resolve KM_SLACK_CHANNEL_ID
// (/sandbox/{id}/slack-channel-id) and KM_SLACK_BRIDGE_URL (/km/slack/bridge-url)
// from SSM at startup, BEFORE the per-turn while-loop, and cache them for the
// service lifetime.
func TestUserdata_PollerResolvesChannelAndBridgeFromSSM(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)
	poller := extractSlackInboundPoller(t, out)

	if !strings.Contains(poller, `/sandbox/${SANDBOX_ID}/slack-channel-id`) {
		t.Fatalf("poller missing /sandbox/${SANDBOX_ID}/slack-channel-id SSM path\n%s", abbreviateUD(poller))
	}
	if !strings.Contains(poller, "/km/slack/bridge-url") {
		t.Fatalf("poller missing /km/slack/bridge-url SSM path\n%s", abbreviateUD(poller))
	}

	// SSM resolution must happen BEFORE the per-turn while-loop (one-time at
	// startup, not per-turn).
	loopIdx := strings.Index(poller, "while true")
	if loopIdx < 0 {
		t.Fatalf("poller while-loop not found")
	}
	prefix := poller[:loopIdx]
	if !strings.Contains(prefix, `/sandbox/${SANDBOX_ID}/slack-channel-id`) {
		t.Fatalf("channel-id SSM resolution must occur BEFORE while-loop (one-time startup, not per-turn)")
	}
	if !strings.Contains(prefix, "/km/slack/bridge-url") {
		t.Fatalf("bridge-url SSM resolution must occur BEFORE while-loop")
	}
}

// TestUserdata_PollerPostsAfterDeleteMessage — Phase 67-11 Gap A.
// Ack-ordering structural test: the new km-slack post call must come AFTER the
// success-branch sqs delete-message so a host crash between them can't cause
// the message to redeliver after the user already saw a reply.
func TestUserdata_PollerPostsAfterDeleteMessage(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)
	poller := extractSlackInboundPoller(t, out)

	// Find the success branch (the `if [ -n "$NEW_SESSION" ]` block).
	successBranch := strings.Index(poller, `if [ -n "$NEW_SESSION" ]`)
	if successBranch < 0 {
		t.Fatalf("success branch (NEW_SESSION) not found in poller")
	}
	successSegment := poller[successBranch:]

	deleteIdx := strings.Index(successSegment, "aws sqs delete-message")
	if deleteIdx < 0 {
		t.Fatalf("aws sqs delete-message not found in success branch")
	}
	postIdx := strings.Index(successSegment, "/opt/km/bin/km-slack post")
	if postIdx < 0 {
		t.Fatalf("km-slack post not found in success branch")
	}
	if postIdx <= deleteIdx {
		t.Fatalf("km-slack post (idx=%d) must come AFTER aws sqs delete-message (idx=%d) — host-crash between post and ack would cause SQS redelivery and duplicate replies", postIdx, deleteIdx)
	}
}

// TestUserdata_StopHookSkipsSlackWhenInboundHandled — Phase 67-11 Gap A.
// Structural assertion (per plan): split rendered userdata on the # 6a. (email)
// and # 6b. (Slack) markers and assert
//   - email branch (6a) does NOT mention the new gate (Slack-only suppression)
//   - Slack branch (6b) DOES contain the KM_SLACK_INBOUND_REPLY_HANDLED guard
func TestUserdata_StopHookSkipsSlackWhenInboundHandled(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)

	sec6aIdx := strings.Index(out, "# 6a.")
	sec6bIdx := strings.Index(out, "# 6b.")
	if sec6aIdx < 0 || sec6bIdx < 0 || sec6bIdx <= sec6aIdx {
		t.Fatalf("# 6a. and # 6b. markers not found in expected order — structural assumption broken")
	}
	section6a := out[sec6aIdx:sec6bIdx]
	// section6b extends from "# 6b." to "# 7." or end-of-string.
	var section6b string
	if rel := strings.Index(out[sec6bIdx:], "# 7."); rel > 0 {
		section6b = out[sec6bIdx : sec6bIdx+rel]
	} else {
		section6b = out[sec6bIdx:]
	}

	// Email branch (6a) must NOT mention the new gate — gate is Slack-only.
	if strings.Contains(section6a, "KM_SLACK_INBOUND_REPLY_HANDLED") {
		t.Fatalf("KM_SLACK_INBOUND_REPLY_HANDLED leaked into the email branch (# 6a.) — gate must be Slack-only to preserve email idle notifications\n%s", abbreviateUD(section6a))
	}
	// Slack branch (6b) MUST contain the gate guard.
	if !strings.Contains(section6b, `"${KM_SLACK_INBOUND_REPLY_HANDLED:-0}" != "1"`) {
		t.Fatalf("Stop hook Slack branch (# 6b.) missing KM_SLACK_INBOUND_REPLY_HANDLED guard\n%s", abbreviateUD(section6b))
	}
}
