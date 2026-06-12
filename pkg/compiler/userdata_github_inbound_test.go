package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// minimalGitHubInboundProfile returns a SandboxProfile with the minimum fields
// required for GitHub inbound tests. inbound controls NotificationGitHubInboundSpec.Enabled.
func minimalGitHubInboundProfile(t *testing.T, inbound bool) *profile.SandboxProfile {
	t.Helper()
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{}
	p.Spec.Notification = &profile.NotificationSpec{
		Github: &profile.NotificationGitHubSpec{
			Inbound: &profile.NotificationGitHubInboundSpec{Enabled: boolPtr(inbound)},
		},
	}
	return p
}

// compileGitHubInboundUserData is a thin wrapper around generateUserData for GitHub inbound tests.
func compileGitHubInboundUserData(t *testing.T, p *profile.SandboxProfile) string {
	t.Helper()
	out, err := generateUserData(p, "sb-gh-test", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	return out
}

// extractGitHubInboundPoller returns the GITHUBINBOUND heredoc body from rendered userdata.
func extractGitHubInboundPoller(t *testing.T, out string) string {
	t.Helper()
	startMarker := "<< 'GITHUBINBOUND'"
	endMarker := "\nGITHUBINBOUND\n"
	start := strings.Index(out, startMarker)
	end := strings.Index(out, endMarker)
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("GITHUBINBOUND heredoc markers not found in rendered userdata\n--- excerpt ---\n%s", abbreviateUD(out))
	}
	return out[start:end]
}

// TestUserdata_GitHubInboundPollerEmitted verifies that when github-inbound is enabled
// the user-data string contains all required GitHub-inbound substrings.
func TestUserdata_GitHubInboundPollerEmitted(t *testing.T) {
	p := minimalGitHubInboundProfile(t, true)
	out := compileGitHubInboundUserData(t, p)

	must := []string{
		"/opt/km/bin/km-github-inbound-poller",
		"/etc/systemd/system/km-github-inbound-poller.service",
		"KM_GITHUB_INBOUND_QUEUE_URL",
		"km-github-inbound-poller",
	}
	for _, s := range must {
		if !strings.Contains(out, s) {
			t.Fatalf("expected user-data to contain %q\n--- output excerpt ---\n%s", s, abbreviateUD(out))
		}
	}
}

// TestUserdata_GitHubInboundPollerSkipped verifies that when github-inbound is disabled
// the user-data string contains NONE of the GitHub-inbound substrings.
func TestUserdata_GitHubInboundPollerSkipped(t *testing.T) {
	p := minimalGitHubInboundProfile(t, false)
	out := compileGitHubInboundUserData(t, p)

	forbidden := []string{
		"km-github-inbound-poller",
		"KM_GITHUB_INBOUND_QUEUE_URL",
		"km-github-inbound-poller.service",
	}
	for _, s := range forbidden {
		if strings.Contains(out, s) {
			t.Fatalf("user-data must not contain %q when github-inbound disabled\n--- excerpt ---\n%s", s, abbreviateUD(out))
		}
	}
}

// TestUserdata_GitHubInboundPoller_Preamble verifies that when github-inbound is enabled
// the poller builds a GitHub context preamble (repo, PR number, branch, head_sha,
// worktree-per-PR guidance) from the envelope.
func TestUserdata_GitHubInboundPoller_Preamble(t *testing.T) {
	p := minimalGitHubInboundProfile(t, true)
	out := compileGitHubInboundUserData(t, p)
	poller := extractGitHubInboundPoller(t, out)

	// Envelope fields parsed from the SQS message body (jq .field syntax).
	envFields := []string{
		`.repo`,
		`.number`,
		`.branch`,
		`.head_sha`,
		`.body`,
		`.sender`,
	}
	for _, f := range envFields {
		if !strings.Contains(poller, f) {
			t.Fatalf("poller missing envelope field %q in preamble construction\n%s", f, abbreviateUD(poller))
		}
	}

	// Preamble must contain worktree guidance.
	worktreeGuidance := []string{
		"worktree",
		"PR",
	}
	for _, g := range worktreeGuidance {
		if !strings.Contains(poller, g) {
			t.Fatalf("poller missing worktree guidance substring %q\n%s", g, abbreviateUD(poller))
		}
	}
}

// TestUserdata_GitHubInboundPoller_Dispatch verifies that the GitHub inbound poller
// dispatches to the agent via the existing tmux agent-run path (via claude -p or codex exec).
func TestUserdata_GitHubInboundPoller_Dispatch(t *testing.T) {
	p := minimalGitHubInboundProfile(t, true)
	out := compileGitHubInboundUserData(t, p)
	poller := extractGitHubInboundPoller(t, out)

	// Must dispatch via agent (claude path as default).
	if !strings.Contains(poller, `claude -p`) {
		t.Fatalf("poller missing claude -p dispatch\n%s", abbreviateUD(poller))
	}
	// Must cd to /workspace before dispatch.
	if !strings.Contains(poller, "cd /workspace") {
		t.Fatalf("poller missing cd /workspace before dispatch\n%s", abbreviateUD(poller))
	}
}

// TestUserdata_GitHubInboundPoller_QueueDrain verifies that the poller drains
// the FIFO queue: polls SQS, deletes the message, and handles empty queues.
func TestUserdata_GitHubInboundPoller_QueueDrain(t *testing.T) {
	p := minimalGitHubInboundProfile(t, true)
	out := compileGitHubInboundUserData(t, p)
	poller := extractGitHubInboundPoller(t, out)

	must := []string{
		"aws sqs receive-message",
		"aws sqs delete-message",
		"QUEUE_URL",
		"RECEIPT",
	}
	for _, s := range must {
		if !strings.Contains(poller, s) {
			t.Fatalf("poller missing queue drain subprocess %q\n%s", s, abbreviateUD(poller))
		}
	}
}

// TestUserdata_GitHubInboundPoller_SSMFallback verifies that the poller falls back to
// SSM Parameter Store when KM_GITHUB_INBOUND_QUEUE_URL is empty.
func TestUserdata_GitHubInboundPoller_SSMFallback(t *testing.T) {
	p := minimalGitHubInboundProfile(t, true)
	out := compileGitHubInboundUserData(t, p)
	poller := extractGitHubInboundPoller(t, out)

	// The SSM path for the queue URL.
	if !strings.Contains(poller, "github-inbound-queue-url") {
		t.Fatalf("poller missing SSM fallback path github-inbound-queue-url\n%s", abbreviateUD(poller))
	}
	// SSM retry loop.
	if !strings.Contains(poller, "attempt") {
		t.Fatalf("poller missing SSM retry loop\n%s", abbreviateUD(poller))
	}
}

// TestUserdata_GitHubInboundPoller_SystemdUnit verifies the systemd unit
// is emitted when github-inbound is enabled.
func TestUserdata_GitHubInboundPoller_SystemdUnit(t *testing.T) {
	p := minimalGitHubInboundProfile(t, true)
	out := compileGitHubInboundUserData(t, p)

	unitStart := strings.Index(out, "<< 'GITHUBINBOUNDUNIT'")
	if unitStart < 0 {
		t.Fatalf("GITHUBINBOUNDUNIT systemd unit heredoc not found")
	}
	unitEnd := strings.Index(out[unitStart:], "GITHUBINBOUNDUNIT\n")
	if unitEnd < 0 {
		t.Fatalf("GITHUBINBOUNDUNIT unit block has no closing delimiter")
	}
	unit := out[unitStart : unitStart+unitEnd]

	// Must reference EnvironmentFile with notify.env (systemd-format).
	if !strings.Contains(unit, "EnvironmentFile=-/etc/km/notify.env") {
		t.Fatalf("km-github-inbound-poller.service must reference EnvironmentFile=-/etc/km/notify.env\n%s", unit)
	}
	if !strings.Contains(unit, "ExecStart=/opt/km/bin/km-github-inbound-poller") {
		t.Fatalf("km-github-inbound-poller.service must ExecStart the poller binary\n%s", unit)
	}
}

// TestUserdata_GitHubInboundPoller_SystemctlEnable verifies that when github-inbound is enabled
// the systemctl enable line contains km-github-inbound-poller.
func TestUserdata_GitHubInboundPoller_SystemctlEnable(t *testing.T) {
	p := minimalGitHubInboundProfile(t, true)
	out := compileGitHubInboundUserData(t, p)

	found := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "systemctl enable") &&
			strings.Contains(line, "km-github-inbound-poller") {
			if strings.Contains(line, "  ") {
				t.Fatalf("malformed systemctl line (double space): %q", line)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("did not find systemctl enable line containing km-github-inbound-poller\n%s", abbreviateUD(out))
	}
}

// TestUserdata_SlackPollerUnaffectedByGitHubInbound verifies that enabling github-inbound
// does NOT affect the Slack poller (dormant byte-identity for Slack when not configured).
func TestUserdata_SlackPollerUnaffectedByGitHubInbound(t *testing.T) {
	// Profile with github-inbound only (no Slack inbound).
	p := minimalGitHubInboundProfile(t, true)
	out := compileGitHubInboundUserData(t, p)

	// The Slack poller heredoc SLACKINBOUND must NOT be emitted when Slack is not configured.
	// (The string "km-slack-inbound-poller" may appear in comments, but the heredoc itself
	// must be absent — absence of the heredoc marker is the definitive check.)
	if strings.Contains(out, "<< 'SLACKINBOUND'") {
		t.Fatalf("Slack poller heredoc (SLACKINBOUND) must not be emitted when only github-inbound is enabled\n%s", abbreviateUD(out))
	}
	// Also verify the Slack service unit is not emitted.
	if strings.Contains(out, "<< 'SLACKINBOUNDUNIT'") {
		t.Fatalf("Slack poller systemd unit (SLACKINBOUNDUNIT) must not be emitted when only github-inbound is enabled\n%s", abbreviateUD(out))
	}
}

// TestUserdata_GitHubInboundPoller_EnvVar verifies that the github-inbound env var
// KM_GITHUB_INBOUND_QUEUE_URL is emitted when github-inbound is enabled.
func TestUserdata_GitHubInboundPoller_EnvVar(t *testing.T) {
	// Enabled path
	p := minimalGitHubInboundProfile(t, true)
	out := compileGitHubInboundUserData(t, p)

	if !strings.Contains(out, "KM_GITHUB_INBOUND_QUEUE_URL=") {
		t.Fatalf("env file must export KM_GITHUB_INBOUND_QUEUE_URL when github-inbound enabled\n%s", abbreviateUD(out))
	}

	// Disabled path
	p2 := minimalGitHubInboundProfile(t, false)
	out2 := compileGitHubInboundUserData(t, p2)
	if strings.Contains(out2, "KM_GITHUB_INBOUND_QUEUE_URL") {
		t.Fatalf("disabled github-inbound must not export KM_GITHUB_INBOUND_QUEUE_URL")
	}
}

// TestUserdata_GitHubInboundPoller_ExportsAWSRegion verifies the poller exports AWS_REGION
// before the while-loop so subprocesses inherit it.
func TestUserdata_GitHubInboundPoller_ExportsAWSRegion(t *testing.T) {
	p := minimalGitHubInboundProfile(t, true)
	out := compileGitHubInboundUserData(t, p)
	poller := extractGitHubInboundPoller(t, out)

	if !strings.Contains(poller, `export AWS_REGION="$REGION"`) {
		t.Fatalf("poller missing 'export AWS_REGION=$REGION'\n%s", abbreviateUD(poller))
	}

	loopIdx := strings.Index(poller, "while true")
	if loopIdx < 0 {
		t.Fatalf("poller while-loop not found")
	}
	if !strings.Contains(poller[:loopIdx], `export AWS_REGION="$REGION"`) {
		t.Fatalf("AWS_REGION export must occur BEFORE while-loop (startup, not per-turn)")
	}
}

// TestUserdata_GitHubInboundPoller_KmGithubBinary verifies that km-github binary is
// downloaded from S3 and symlinked to /usr/local/bin when github-inbound is enabled.
func TestUserdata_GitHubInboundPoller_KmGithubBinary(t *testing.T) {
	p := minimalGitHubInboundProfile(t, true)
	out := compileGitHubInboundUserData(t, p)

	if !strings.Contains(out, "km-github") {
		t.Fatalf("user-data must reference km-github binary when github-inbound enabled\n%s", abbreviateUD(out))
	}
}

// TestUserdata_GitHubInboundPoller_Phase102AgentVerbs verifies that the Phase 102
// agent-verb (D1-D6) tokens are present in the rendered GitHub inbound poller.
// These tokens are the only automated signal for the bash blocks; runtime behaviour
// (D4 precedence, D5 cross-agent switch, D6 codex guard, D3 persistence) is verified
// manually in Plan 102-05's E2E UAT — the same pattern used in Phases 97/98/99.
func TestUserdata_GitHubInboundPoller_Phase102AgentVerbs(t *testing.T) {
	p := minimalGitHubInboundProfile(t, true)
	out := compileGitHubInboundUserData(t, p)
	poller := extractGitHubInboundPoller(t, out)

	must := []string{
		"AGENT_OVERRIDE",            // D1: parse .agent from envelope
		"THREAD_AGENT_TYPE",         // D3: thread-pinned agent read from DDB
		"command -v codex",          // D6: codex-missing helpful-error guard
		"agent_type = :at",          // D3: agent_type write-back in update-expression
		"/codex is unavailable here", // Follow-up #2: D6 message uses the /codex slash verb (not 'codex:')
		"codex exec resume '$GITHUB_SESSION'", // Follow-up #1: Codex thread resume on GitHub (mirrors Slack)
		"KM_GITHUB_REPLY_AGENT='codex'",       // Follow-up #3: reply-agent env for the km-github attribution footer
		"KM_GITHUB_REPLY_AGENT='claude'",      // Follow-up #3: same on the Claude dispatch path
	}
	for _, s := range must {
		if !strings.Contains(poller, s) {
			t.Fatalf("Phase 102 token missing from GitHub inbound poller: %q\n%s", s, abbreviateUD(poller))
		}
	}

	// Follow-up #2 regression: the old Slack-style 'codex:' colon wording must be gone.
	if strings.Contains(poller, "'codex:' verb") {
		t.Errorf("stale 'codex:' colon wording still present in codex-missing guard; expected /codex slash verb")
	}
}

// TestUserdata_GitHubInboundPoller_ResumeHint verifies Phase 106's post-on-mint resume
// hint behavior inside the GitHub inbound poller. After a qualifying agent turn the
// poller posts a collapsed <details> comment carrying the operator-facing resume command,
// run-from directory, and sandbox id — exactly once per newly-minted session id.
//
// Contract (post-on-mint): the hint is posted ONLY when
//   "$NEW_GITHUB_SESSION" != "${GITHUB_SESSION:-}"
//
// meaning a fresh mint or a Gap-E re-mint. Every assertion below is SCOPED to the
// extracted GITHUBINBOUND heredoc body to prove the hint lives inside the GitHub poller
// rather than anywhere else in the rendered userdata.
//
// This test is RED at Phase 106 Wave 0 (plan 01) — it will go GREEN when Wave 1
// (plan 02) adds the hint block to userdata.go.
func TestUserdata_GitHubInboundPoller_ResumeHint(t *testing.T) {
	p := minimalGitHubInboundProfile(t, true)
	out := compileGitHubInboundUserData(t, p)
	poller := extractGitHubInboundPoller(t, out)

	must := []string{
		`<details>`,                                               // collapsible fold opener
		`🔧 Resume`,                                               // locked summary emoji+wording
		`claude --resume`,                                         // Claude resume branch
		`codex exec resume`,                                       // Codex resume branch
		`/workspace`,                                              // run-from directory (NOT /home/sandbox)
		`SANDBOX_ID`,                                              // sandbox id referenced in the hint body
		`|| true`,                                                 // best-effort non-blocking guard on the hint post
		`"$NEW_GITHUB_SESSION" != "${GITHUB_SESSION:-}"`,          // post-on-mint condition (exact guard)
	}
	for _, s := range must {
		if !strings.Contains(poller, s) {
			t.Fatalf("Phase 106 resume-hint substring missing from GitHub inbound poller: %q\n%s", s, abbreviateUD(poller))
		}
	}
}
