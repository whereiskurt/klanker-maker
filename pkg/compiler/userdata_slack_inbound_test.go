package compiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// minimalSlackInboundProfile returns a SandboxProfile with the minimum fields
// required for Slack inbound tests. inbound controls NotifySlackInboundEnabled.
func minimalSlackInboundProfile(t *testing.T, inbound bool) *profile.SandboxProfile {
	t.Helper()
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{}
	p.Spec.Notification = &profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtr(true),
			PerSandbox: boolPtr(true),
			Inbound:    &profile.NotificationSlackInboundSpec{Enabled: boolPtr(inbound)},
		},
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

// TestUserdata_SlackInboundPoller_KMArtifactsBucket_InSystemdUnit — Phase 75.3 regression.
// The poller bash mirrors S3 attachments using ${KM_ARTIFACTS_BUCKET}; without an
// explicit Environment= line in the systemd unit, the bash `set -u` check fires with
// "KM_ARTIFACTS_BUCKET: unbound variable" the first time a file_share SQS message
// reaches the poller. This regression test asserts the env line is present in the
// inbound-poller unit when inbound is enabled.
func TestUserdata_SlackInboundPoller_KMArtifactsBucket_InSystemdUnit(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)

	// Find the inbound-poller unit block and assert KM_ARTIFACTS_BUCKET appears inside it.
	start := strings.Index(out, "km-slack-inbound-poller.service << 'SLACKINBOUNDUNIT'")
	if start < 0 {
		t.Fatalf("did not find km-slack-inbound-poller systemd unit\n%s", abbreviateUD(out))
	}
	end := strings.Index(out[start:], "SLACKINBOUNDUNIT\n")
	if end < 0 {
		t.Fatalf("unit block has no closing SLACKINBOUNDUNIT delimiter")
	}
	block := out[start : start+end]
	if !strings.Contains(block, "Environment=KM_ARTIFACTS_BUCKET=") {
		t.Fatalf("km-slack-inbound-poller unit missing Environment=KM_ARTIFACTS_BUCKET — Phase 75 attachment mirror will fail with 'unbound variable'\nunit block:\n%s", block)
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

// TestUserdata_StopHookReferencesThreadTSGate — Phase 67-11 Gap A follow-up.
// The Stop hook's Slack branch must reference KM_SLACK_THREAD_TS in its gate
// (so it can detect "poller is driving this turn — skip post"). When the gate
// evaluates true, KM_SLACK_THREAD_TS is empty by construction, so any --thread
// argument MUST come from a transcript-streaming auto-thread cache lookup
// (auto_thread_ts), not from KM_SLACK_THREAD_TS itself. That keeps the
// Phase 67 invariant intact while letting Phase 68 transcript streaming
// route the idle marker into the streaming thread.
//
// Replaces the obsolete TestUserdata_SlackInboundThreadFlag whose premise
// (Stop hook passes --thread when KM_SLACK_THREAD_TS is set) was the source
// of Gap A: it caused the Stop hook to post the unreliable transcript-JSONL
// fallback BEFORE the poller posted the real .result.
func TestUserdata_StopHookReferencesThreadTSGate(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)

	// Bound to the # 6b. Slack branch.
	sec6bIdx := strings.Index(out, "# 6b.")
	if sec6bIdx < 0 {
		t.Fatalf("# 6b. marker not found")
	}
	var section6b string
	if rel := strings.Index(out[sec6bIdx:], "# 7."); rel > 0 {
		section6b = out[sec6bIdx : sec6bIdx+rel]
	} else {
		section6b = out[sec6bIdx:]
	}

	if !strings.Contains(section6b, "KM_SLACK_THREAD_TS") {
		t.Fatalf("# 6b. Slack branch must reference KM_SLACK_THREAD_TS in its gate\n%s", abbreviateUD(section6b))
	}
	// When --thread appears in # 6b, it MUST be sourced from auto_thread_ts
	// (transcript-streaming cache), not from KM_SLACK_THREAD_TS — the gate
	// guarantees KM_SLACK_THREAD_TS is empty in this branch.
	if strings.Contains(section6b, "--thread") {
		if !strings.Contains(section6b, "auto_thread_ts") {
			t.Fatalf("# 6b. Slack branch passes --thread but does not derive it from auto_thread_ts; KM_SLACK_THREAD_TS is empty by gate construction\n%s", abbreviateUD(section6b))
		}
		if strings.Contains(section6b, `--thread "$KM_SLACK_THREAD_TS"`) {
			t.Fatalf("# 6b. Slack branch must NOT pass --thread \"$KM_SLACK_THREAD_TS\" — that var is empty by gate construction\n%s", abbreviateUD(section6b))
		}
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

	// Phase 70 (Plan 70-05): the claude path now uses '.result // .response // ""' (with
	// per-agent branching). The assertion checks for the core .result extraction pattern
	// that guarantees the Phase 67 result-to-Slack path still works for claude sandboxes.
	if !strings.Contains(poller, `.result`) {
		t.Fatalf("poller missing .result extraction from output.json\n%s", abbreviateUD(poller))
	}
	if !strings.Contains(poller, `/opt/km/bin/km-slack post`) {
		t.Fatalf("poller missing km-slack post invocation\n%s", abbreviateUD(poller))
	}
	if !strings.Contains(poller, `--thread "$THREAD_TS"`) {
		t.Fatalf("poller missing --thread \"$THREAD_TS\" flag\n%s", abbreviateUD(poller))
	}
}

// TestUserdata_PollerAgentRunsInWorkspace — the inbound poller dispatches the
// agent via `sudo -u sandbox bash -lc`, which inherits the root systemd cwd (/).
// Every other agent path (queue-runner, interactive `km agent run`) cd's to
// /workspace first; the poller must do the same so Slack-triggered turns start
// in the work tree, not /. Asserts the cd appears in all three dispatch blocks
// (claude, codex resume, codex first-turn).
func TestUserdata_PollerAgentRunsInWorkspace(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)
	poller := extractSlackInboundPoller(t, out)

	const cd = "cd /workspace 2>/dev/null || true"
	if n := strings.Count(poller, cd); n != 3 {
		t.Fatalf("expected %q before each of the 3 agent dispatch blocks (claude + codex resume + codex first-turn); got %d occurrence(s)\n%s",
			cd, n, abbreviateUD(poller))
	}
}

// TestSlackInboundPoller_ReplyPost_RenderFlag — Phase 74 Task 6 (HOOK-01 inbound).
// Plan 02 Task 4 flipped the Phase 68 transcript-streaming hook
// (_km_stream_drain) to --render "${KM_SLACK_RENDER:-blocks}" so per-turn
// streaming renders as Block Kit. But the Slack-inbound poller has its OWN
// km-slack post call that posts the final .result from `claude -p` back into
// the Slack thread — and prior to this task that call had no --render flag,
// so it defaulted to plain. Symptom: operators chatting in #sb-<id> via Slack
// (the most-used path) saw literal markdown (**bold**, # heading) even though
// the streaming hook was correctly flipped.
//
// Assertions:
//  1. The poller's reply post call carries --render "${KM_SLACK_RENDER:-blocks}"
//     so it inherits the same Block Kit default + operator safety valve as
//     _km_stream_drain.
//  2. The --render flag appears BEFORE --body inside the same km-slack post
//     invocation (so it's a sibling of --channel/--thread, not a stray match
//     elsewhere in the heredoc).
func TestSlackInboundPoller_ReplyPost_RenderFlag(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)
	poller := extractSlackInboundPoller(t, out)

	wantSubstr := `--render "${KM_SLACK_RENDER:-blocks}"`
	if !strings.Contains(poller, wantSubstr) {
		t.Fatalf("inbound poller reply post must carry %q so Slack-initiated chat renders as Block Kit (HOOK-01 inbound coverage)\n--- poller excerpt ---\n%s", wantSubstr, abbreviateUD(poller))
	}

	// Bound to the km-slack post invocation in the success branch. The earlier
	// occurrence of the literal "km-slack post" in a comment must be skipped —
	// anchor on the actual `if /opt/km/bin/km-slack post` shell statement.
	postStmtIdx := strings.Index(poller, "if /opt/km/bin/km-slack post")
	if postStmtIdx < 0 {
		t.Fatalf("`if /opt/km/bin/km-slack post` shell statement not found in poller — structural assumption broken")
	}
	// The post call spans until the closing `; then` of the if-statement.
	postSegmentEnd := strings.Index(poller[postStmtIdx:], "; then")
	if postSegmentEnd < 0 {
		t.Fatalf("km-slack post call has no terminating `; then` — structural assumption broken")
	}
	postCall := poller[postStmtIdx : postStmtIdx+postSegmentEnd]

	renderIdx := strings.Index(postCall, wantSubstr)
	bodyIdx := strings.Index(postCall, `--body "$POST_FILE"`)
	if renderIdx < 0 {
		t.Fatalf("--render flag missing from km-slack post call body — found elsewhere in poller but not in the reply-post invocation\n--- post call ---\n%s", postCall)
	}
	if bodyIdx < 0 {
		t.Fatalf("--body \"$POST_FILE\" missing from km-slack post call — structural assumption broken\n--- post call ---\n%s", postCall)
	}
	if renderIdx >= bodyIdx {
		t.Fatalf("--render flag (idx=%d) must appear BEFORE --body (idx=%d) inside the same post call\n--- post call ---\n%s", renderIdx, bodyIdx, postCall)
	}
}

// TestUserdata_PollerExportsAWSRegion — Phase 67-11 Gap A follow-up.
// AWS_REGION is not a NotifyEnv field, so it's never in the systemd
// EnvironmentFile. The poller must explicitly export AWS_REGION so
// subprocesses (km-slack post, km-send) see it — otherwise km-slack post
// fails with "AWS_REGION (or AWS_DEFAULT_REGION) not set".
func TestUserdata_PollerExportsAWSRegion(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)
	poller := extractSlackInboundPoller(t, out)

	if !strings.Contains(poller, `export AWS_REGION="$REGION"`) {
		t.Fatalf("poller missing 'export AWS_REGION=$REGION' — km-slack post will fail with 'AWS_REGION not set'\n%s", abbreviateUD(poller))
	}

	// Export must happen BEFORE the while-loop so every km-slack post call
	// inherits it (one-time at startup, not per-turn).
	loopIdx := strings.Index(poller, "while true")
	if loopIdx < 0 {
		t.Fatalf("poller while-loop not found")
	}
	if !strings.Contains(poller[:loopIdx], `export AWS_REGION="$REGION"`) {
		t.Fatalf("AWS_REGION export must occur BEFORE while-loop (startup, not per-turn)")
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

// TestUserdata_StopHookSkipsAllNotifyWhenPollerDriving — Phase 67-11 Gap A
// follow-up, refined per operator feedback (2026-05-08): when the inbound
// poller is driving the turn the operator is already on Slack and the poller
// will deliver Claude's reply into that thread. A "Claude is waiting"-style
// email or channel-root post is noise. Both the email branch (6a) AND the
// Slack-root branch (6b) must be suppressed in that case. Terminal-initiated
// sessions (KM_SLACK_THREAD_TS unset) keep the legacy behavior.
//
// Structural assertions:
//   - The # 5a. top-level suppression gate sets do_email_branch=0 when
//     KM_SLACK_THREAD_TS is non-empty — this is the single source of truth
//     and short-circuits both 6a and 6b.
//   - The # 6b. Slack branch keeps its own `-z "${KM_SLACK_THREAD_TS:-}"`
//     check as a defensive local invariant (so a future refactor that moves
//     or removes the 5a gate can't silently re-introduce the double-post bug).
//
// The earlier KM_SLACK_INBOUND_REPLY_HANDLED gate failed because that env var
// was set by the poller AFTER claude exits, so it was never visible inside
// the Claude process when the Stop hook fired.
func TestUserdata_StopHookSkipsAllNotifyWhenPollerDriving(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)

	sec5aIdx := strings.Index(out, "# 5a.")
	sec6aIdx := strings.Index(out, "# 6a.")
	sec6bIdx := strings.Index(out, "# 6b.")
	if sec5aIdx < 0 || sec6aIdx < 0 || sec6bIdx < 0 || sec5aIdx >= sec6aIdx || sec6bIdx <= sec6aIdx {
		t.Fatalf("# 5a., # 6a., # 6b. markers not found in expected order — structural assumption broken")
	}
	section5a := out[sec5aIdx:sec6aIdx]
	// section6b extends from "# 6b." to "# 7." or end-of-string.
	var section6b string
	if rel := strings.Index(out[sec6bIdx:], "# 7."); rel > 0 {
		section6b = out[sec6bIdx : sec6bIdx+rel]
	} else {
		section6b = out[sec6bIdx:]
	}

	// (1) The # 5a. top-level gate MUST suppress do_email_branch when
	//     KM_SLACK_THREAD_TS is set. This is what makes Slack-initiated turns
	//     skip BOTH the email post and the channel-root post.
	if !strings.Contains(section5a, `-n "${KM_SLACK_THREAD_TS:-}"`) {
		t.Fatalf("# 5a. top-level gate missing `-n \"${KM_SLACK_THREAD_TS:-}\"` check — Slack-initiated turns will still send 'Claude is waiting' notifications\n%s", abbreviateUD(section5a))
	}
	if !strings.Contains(section5a, "do_email_branch=0") {
		t.Fatalf("# 5a. top-level gate must set do_email_branch=0 when KM_SLACK_THREAD_TS is set\n%s", abbreviateUD(section5a))
	}

	// (2) Slack branch (6b) keeps its defensive `-z "${KM_SLACK_THREAD_TS:-}"`
	//     check so removing the 5a gate later can't silently regress double-posting.
	if !strings.Contains(section6b, `-z "${KM_SLACK_THREAD_TS:-}"`) {
		t.Fatalf("Stop hook Slack branch (# 6b.) missing defensive `-z \"${KM_SLACK_THREAD_TS:-}\"` gate — needed even with 5a in place\n%s", abbreviateUD(section6b))
	}

	// (3) Dead KM_SLACK_INBOUND_REPLY_HANDLED gate must stay removed.
	if strings.Contains(out, "KM_SLACK_INBOUND_REPLY_HANDLED") {
		t.Fatalf("KM_SLACK_INBOUND_REPLY_HANDLED still present in userdata — was set after Claude exits so it was never visible inside the Stop hook; remove entirely")
	}
}

// TestUserdata_SystemdEnvFileNoExport — Phase 67-11 follow-up.
// systemd's EnvironmentFile= directive does NOT accept the 'export VAR=val'
// shell-keyword prefix used by /etc/profile.d/km-notify-env.sh. The compiler
// writes a parallel /etc/km/notify.env in systemd-native format (no export
// prefix) for systemd-managed services. The km-slack-inbound-poller.service
// unit's EnvironmentFile= must point at the systemd-format file, NOT the
// shell file.
func TestUserdata_SystemdEnvFileNoExport(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)

	// (1) Systemd-format env file is written.
	if !strings.Contains(out, "cat > /etc/km/notify.env") {
		t.Fatalf("compiler must write /etc/km/notify.env (systemd-format) when NotifyEnv is non-empty\n%s", abbreviateUD(out))
	}

	// (2) Inside the /etc/km/notify.env heredoc, NO line starts with 'export'.
	startMarker := "cat > /etc/km/notify.env << 'KM_NOTIFY_SYSTEMD_EOF'"
	endMarker := "\nKM_NOTIFY_SYSTEMD_EOF\n"
	startIdx := strings.Index(out, startMarker)
	endIdx := strings.Index(out, endMarker)
	if startIdx < 0 || endIdx < 0 || endIdx <= startIdx {
		t.Fatalf("KM_NOTIFY_SYSTEMD_EOF heredoc markers missing/misordered")
	}
	body := out[startIdx+len(startMarker) : endIdx]
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "export ") {
			t.Fatalf("/etc/km/notify.env must NOT contain 'export' prefix (systemd rejects it): %q", line)
		}
	}

	// (3) The km-slack-inbound-poller.service unit references the new file
	//     and does NOT reference the broken shell file.
	unitStart := strings.Index(out, "<< 'SLACKINBOUNDUNIT'")
	unitEnd := strings.Index(out, "\nSLACKINBOUNDUNIT\n")
	if unitStart < 0 || unitEnd < 0 || unitEnd <= unitStart {
		t.Fatalf("SLACKINBOUNDUNIT heredoc markers missing/misordered")
	}
	unit := out[unitStart:unitEnd]
	if !strings.Contains(unit, "EnvironmentFile=-/etc/km/notify.env") {
		t.Fatalf("km-slack-inbound-poller.service must reference EnvironmentFile=-/etc/km/notify.env (with leading '-' for missing-file tolerance)\n%s", unit)
	}
	if strings.Contains(unit, "EnvironmentFile=/etc/profile.d/km-notify-env.sh") ||
		strings.Contains(unit, "EnvironmentFile=-/etc/profile.d/km-notify-env.sh") {
		t.Fatalf("km-slack-inbound-poller.service must NOT reference shell-format /etc/profile.d/km-notify-env.sh (systemd rejects 'export' prefix in that file)\n%s", unit)
	}
}

// TestUserdata_SlackInbound_AttachmentMirrorBlock — Phase 75.
// The inbound poller heredoc must contain the S3-to-local mirror block that
// extracts .attachments[]? from the SQS body and copies each file to
// /workspace/.km-slack/attachments/<thread_ts>/. The mirror block must
// occur BEFORE the claude -p invocation in bash control flow.
func TestUserdata_SlackInbound_AttachmentMirrorBlock(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)
	poller := extractSlackInboundPoller(t, out)

	must := []string{
		`jq -c '.attachments[]?'`,
		"/workspace/.km-slack/attachments/",
		"mkdir -p",
		`aws s3 cp "s3://$KM_ARTIFACTS_BUCKET/`,
		"chown sandbox:sandbox",
	}
	for _, s := range must {
		if !strings.Contains(poller, s) {
			t.Fatalf("attachment mirror block missing substring %q\n--- poller excerpt ---\n%s", s, abbreviateUD(poller))
		}
	}

	// Mirror block must occur BEFORE claude -p invocation.
	claudeIdx := strings.Index(poller, "claude -p")
	mirrorIdx := strings.Index(poller, "/workspace/.km-slack/attachments/")
	if mirrorIdx < 0 || claudeIdx < 0 || mirrorIdx >= claudeIdx {
		t.Fatalf("mirror block must precede claude -p invocation (mirrorIdx=%d, claudeIdx=%d)\n%s", mirrorIdx, claudeIdx, abbreviateUD(poller))
	}
}

// TestUserdata_SlackInbound_MasterPromptWrapper — Phase 75.
// When attachments are present, the inbound poller must prepend a
// master-prompt wrapper to the prompt file before invoking claude -p.
// The wrapper must include exact phrasing from CONTEXT.md (including em-dash).
// The wrapper must be gated on ATTACH_COUNT > 0.
func TestUserdata_SlackInbound_MasterPromptWrapper(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)
	poller := extractSlackInboundPoller(t, out)

	// Exact phrasing from CONTEXT.md.
	must := []string{
		"The user attached the following file(s)",
		"Read them with your Read tool when relevant",
		"User's message:",
		"[no text \xe2\x80\x94 file-only]", // em-dash U+2014 = UTF-8 0xE2 0x80 0x94
	}
	for _, s := range must {
		if !strings.Contains(poller, s) {
			t.Fatalf("master-prompt wrapper missing substring %q\n--- poller excerpt ---\n%s", s, abbreviateUD(poller))
		}
	}

	// Wrapper must be gated on ATTACH_COUNT > 0.
	if !strings.Contains(poller, "ATTACH_COUNT") || !strings.Contains(poller, "-gt 0") {
		t.Fatalf("wrapper must be gated on ATTACH_COUNT -gt 0\n%s", abbreviateUD(poller))
	}
}

// TestUserdata_SlackInbound_AllowsEmptyTextWhenAttachments — Phase 75 Pitfall 4.
// The malformed-message guard must be updated to admit file-only uploads
// (empty text + non-empty attachments). The OLD standalone form
// `[ -z "$TEXT" ]` alone must be replaced by the compound
// `{ [ -z "$TEXT" ] && [ "$ATTACH_COUNT" -eq 0 ]; }`.
func TestUserdata_SlackInbound_AllowsEmptyTextWhenAttachments(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)
	poller := extractSlackInboundPoller(t, out)

	// The new compound expression must be present (Pitfall 4 fix).
	compound := `[ -z "$TEXT" ] && [ "$ATTACH_COUNT" -eq 0 ]`
	if !strings.Contains(poller, compound) {
		t.Fatalf("malformed-message guard missing Pitfall-4 compound expression %q\n--- poller excerpt ---\n%s", compound, abbreviateUD(poller))
	}
}

// pollerWithAgentCodex returns a compiled poller heredoc for a profile with
// agent: codex and notifySlackInboundEnabled: true.
// Used by Plan 70-05 Task 2 dispatch-fork tests.
func pollerWithAgentCodex(t *testing.T) string {
	t.Helper()
	p := baseProfile()
	// Phase 92 (Wave 4): agent default moved to spec.agent.default; KM_AGENT
	// emission still gates on Spec.CLI != nil, so keep a present CLI block.
	p.Spec.CLI = &profile.CLISpec{}
	p.Spec.Agent = &profile.AgentSpec{Default: "codex"}
	p.Spec.Notification = &profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtr(true),
			PerSandbox: boolPtr(true),
			Inbound:    &profile.NotificationSlackInboundSpec{Enabled: boolPtr(true)},
		},
	}
	out := compileInboundUserData(t, p)
	return extractSlackInboundPoller(t, out)
}

// pollerWithAgentClaude returns a compiled poller heredoc for a profile with
// no agent field (defaults to claude) and notifySlackInboundEnabled: true.
// Used by Plan 70-05 Task 2 regression-guard tests.
func pollerWithAgentClaude(t *testing.T) string {
	t.Helper()
	p := baseProfile()
	// Agent intentionally omitted — defaults to claude
	p.Spec.CLI = &profile.CLISpec{}
	p.Spec.Notification = &profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtr(true),
			PerSandbox: boolPtr(true),
			Inbound:    &profile.NotificationSlackInboundSpec{Enabled: boolPtr(true)},
		},
	}
	out := compileInboundUserData(t, p)
	return extractSlackInboundPoller(t, out)
}

// TestPoller_CodexDispatch_FirstTurn confirms the poller bash contains the
// Codex first-turn dispatch markers when the profile has agent: codex. Checks:
// - dispatch fork guard: if [ "$EFFECTIVE_AGENT" = "codex" ]
// - first-turn command: codex exec --json --dangerously-bypass-approvals-and-sandbox
// - inline KM_CODEX_RUN_ID export (Pitfall 3 per 70-RESEARCH.md)
// - JSONL stream parse for thread_id (Plan 70-10 Path B; replaced 70-05's hook-file path)
// SC-4: Plans 70-05 + 70-10.
func TestPoller_CodexDispatch_FirstTurn(t *testing.T) {
	poller := pollerWithAgentCodex(t)

	must := []string{
		`if [ "$EFFECTIVE_AGENT" = "codex" ]; then`,
		`codex exec --json --dangerously-bypass-approvals-and-sandbox`,
		`export KM_CODEX_RUN_ID='$RUN_ID'`,
		// Plan 70-10 Path B: JSONL stream parsing replaces hook-file. The session ID
		// comes from the thread.started event in $RUN_DIR/output.json.
		`select(.type=="thread.started") | .thread_id`,
	}
	for _, m := range must {
		if !strings.Contains(poller, m) {
			t.Errorf("poller missing expected fragment:\n  want: %q\n%s", m, abbreviateUD(poller))
		}
	}

	// Plan 70-10 Path B: ensure the dead hook-file path is GONE.
	dead := []string{
		`SESSION_FILE="/tmp/km-codex-session.$RUN_ID"`,
		`for _w in 1 2 3 4 5; do`,
	}
	for _, d := range dead {
		if strings.Contains(poller, d) {
			t.Errorf("poller still contains dead hook-file marker (Path B should have replaced): %q\n%s", d, abbreviateUD(poller))
		}
	}
}

// TestPoller_CodexDispatch_Resume confirms the poller uses the subcommand form
// "codex exec resume" (NOT the legacy "--resume" flag) for resume turns.
// Per Plan 70-00 spike: canonical 2026 Codex resume syntax is the subcommand.
// SC-5: Plan 70-05.
func TestPoller_CodexDispatch_Resume(t *testing.T) {
	poller := pollerWithAgentCodex(t)

	if !strings.Contains(poller, `codex exec resume `) {
		t.Errorf("poller missing 'codex exec resume' subcommand form\n%s", abbreviateUD(poller))
	}
	// Must NOT contain the legacy flag form.
	if strings.Contains(poller, `codex exec --resume`) {
		t.Errorf("poller contains legacy 'codex exec --resume' flag form — must use subcommand per Plan 70-00 spike\n%s", abbreviateUD(poller))
	}
}

// TestPoller_AgentTypeWriteback confirms the DDB put-item always carries the
// agent_type attribute AND uses $LAST_MSG_JSON (jq-escaped) without extra quotes.
// SC-4/SC-5: Plan 70-05.
func TestPoller_AgentTypeWriteback(t *testing.T) {
	// Verify for both claude and codex profiles.
	for _, tc := range []struct {
		name   string
		poller func(*testing.T) string
	}{
		{"codex", pollerWithAgentCodex},
		{"claude", pollerWithAgentClaude},
	} {
		t.Run(tc.name, func(t *testing.T) {
			poller := tc.poller(t)

			// agent_type attribute must be present. The rendered heredoc uses bash
			// quoting so double-quotes are backslash-escaped in the Go raw string:
			// \"agent_type\":{\"S\":\"$EFFECTIVE_AGENT\"}
			if !strings.Contains(poller, `\"agent_type\":{\"S\":\"$EFFECTIVE_AGENT\"}`) {
				t.Errorf("poller DDB put-item missing agent_type attribute\n%s", abbreviateUD(poller))
			}
			// last_assistant_msg must use $LAST_MSG_JSON WITHOUT extra quotes
			// (jq -Rs . already wraps it in double quotes per Pitfall 2).
			// Rendered form: \"last_assistant_msg\":{\"S\":$LAST_MSG_JSON}
			if !strings.Contains(poller, `\"last_assistant_msg\":{\"S\":$LAST_MSG_JSON}`) {
				t.Errorf("poller DDB put-item must contain \\\"last_assistant_msg\\\":{\\\"S\\\":$LAST_MSG_JSON} (no extra quotes per Pitfall 2)\n%s", abbreviateUD(poller))
			}
			// Must NOT contain the double-quoted (broken) form.
			if strings.Contains(poller, `\"last_assistant_msg\":{\"S\":\"$LAST_MSG_JSON\"}`) {
				t.Errorf("poller DDB put-item has extra quotes around $LAST_MSG_JSON — will double-encode per Pitfall 2\n%s", abbreviateUD(poller))
			}
		})
	}
}

// TestPoller_LastAssistantMsg_JQEscaping_RoundTrip verifies that the jq -Rs .
// pipeline used for last_assistant_msg correctly handles pathological input
// containing embedded double-quotes, backslashes, and actual newlines.
//
// This is an EMPIRICAL test — it runs a real bash subprocess to exercise the
// exact pipeline from the poller heredoc. It does NOT just grep the template
// for the correct strings: it actually invokes bash+jq to confirm the encoding
// works at runtime. Guards 70-RESEARCH.md Pitfall 2.
// SC-4/SC-5: Plan 70-05.
func TestPoller_LastAssistantMsg_JQEscaping_RoundTrip(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available")
	}

	// Pathological input: double-quotes, backslash, actual newline.
	pathological := "Line1 with \"double quotes\" and \\backslash and\nactual newline\nand more text"

	// The exact pipeline from the poller heredoc (Edit E + Edit F):
	//   LAST_MSG_JSON=$(echo "$RESULT_TEXT" | head -c 2000 | jq -Rs .)
	//   echo "$LAST_MSG_JSON" | jq -e .   ← round-trip: must exit 0
	script := `
RESULT_TEXT="$1"
LAST_MSG_JSON=$(printf '%s' "$RESULT_TEXT" | head -c 2000 | jq -Rs .)
[ -z "$LAST_MSG_JSON" ] && LAST_MSG_JSON='""'
# Round-trip: pipe the produced JSON string through jq to confirm it parses.
echo "$LAST_MSG_JSON" | jq -e . >/dev/null
exit $?
`
	cmd := exec.Command("bash", "-c", script, "_", pathological)
	if err := cmd.Run(); err != nil {
		t.Fatalf("jq -Rs . round-trip FAILED for pathological input %q: %v\n(This means embedded quotes/backslashes/newlines would corrupt the DDB JSON)", pathological, err)
	}
}

// TestPoller_ClaudePath_Unchanged is a regression guard confirming that when the
// effective agent is claude (default — no agent field in profile), the Phase 67
// claude dispatch path is preserved verbatim and no codex exec invocation appears
// OUTSIDE the Codex-guarded branch.
// SC-6: Plan 70-05.
func TestPoller_ClaudePath_Unchanged(t *testing.T) {
	poller := pollerWithAgentClaude(t)

	// Phase 67 claude invocation marker must still be present.
	phase67Marker := `claude -p \"\$(cat '$PROMPT_FILE')\" --output-format json`
	if !strings.Contains(poller, phase67Marker) {
		t.Errorf("Phase 67 claude -p invocation missing from default-agent poller\n  want: %q\n%s", phase67Marker, abbreviateUD(poller))
	}

	// The dispatch fork (if/else/fi) must exist — claude is in the else branch.
	if !strings.Contains(poller, `if [ "$EFFECTIVE_AGENT" = "codex" ]; then`) {
		t.Errorf("dispatch fork guard missing from poller\n%s", abbreviateUD(poller))
	}
	if !strings.Contains(poller, `else`) || !strings.Contains(poller, `fi`) {
		t.Errorf("dispatch fork if/else/fi structure missing\n%s", abbreviateUD(poller))
	}

	// codex exec MAY appear inside the guarded branch — that is expected.
	// But the guard must wrap it so claude-agent profiles still use claude.
	// Verify that: the if-codex guard appears BEFORE any codex exec invocation.
	guardIdx := strings.Index(poller, `if [ "$EFFECTIVE_AGENT" = "codex" ]; then`)
	codexExecIdx := strings.Index(poller, `codex exec`)
	if codexExecIdx >= 0 && guardIdx >= 0 && codexExecIdx < guardIdx {
		t.Errorf("codex exec appears before the dispatch guard — not properly gated\n%s", abbreviateUD(poller))
	}
}

// TestUserdata_ShellEnvFileStillWritten — Phase 67-11 follow-up.
// The original shell-format /etc/profile.d/km-notify-env.sh must STILL be
// written — interactive SSM sessions, the km-notify-hook bash script, and
// any other shell consumers depend on it being sourced via profile.d. The
// systemd-format file is added in PARALLEL, not as a replacement.
func TestUserdata_ShellEnvFileStillWritten(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)

	if !strings.Contains(out, "cat > /etc/profile.d/km-notify-env.sh") {
		t.Fatalf("compiler must continue writing /etc/profile.d/km-notify-env.sh (shell consumers depend on it)")
	}
	// Sanity: shell file SHOULD still use 'export' (that's why we needed a
	// systemd-format parallel — the shell file's format is correct for shells).
	startMarker := "cat > /etc/profile.d/km-notify-env.sh << 'KM_NOTIFY_ENV_EOF'"
	endMarker := "\nKM_NOTIFY_ENV_EOF\n"
	s := strings.Index(out, startMarker)
	e := strings.Index(out, endMarker)
	if s < 0 || e < 0 || e <= s {
		t.Fatalf("KM_NOTIFY_ENV_EOF heredoc markers missing/misordered")
	}
	body := out[s+len(startMarker) : e]
	if !strings.Contains(body, "export ") {
		t.Fatalf("/etc/profile.d/km-notify-env.sh must use 'export VAR=val' for shell sourcing (regression vs Phase 62)")
	}
}
