package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// Cgroup-enrollment gap-fix (Phase 131 follow-up): every site that dispatches an
// agent turn (claude/codex, or the tmux session that runs them) as the sandbox
// user from a root-owned poller/daemon must join the km eBPF cgroup scope BEFORE
// dropping privileges, using runuser rather than sudo — sudo/su open a PAM
// session and pam_systemd's logind integration silently re-migrates the process
// into user.slice, undoing the join (measured live). A bare `sudo -u sandbox`
// dispatch site is a REGRESSION of this fix: it means the turn's traffic is
// unenrolled and unenforced/unattributed again.
//
// heredocBlock extracts the body of a `cat > <path> << 'MARKER' ... MARKER`
// heredoc from rendered userdata, so assertions are scoped to one poller's own
// script and can't be satisfied by a DIFFERENT poller/daemon that also mentions
// "sudo -u sandbox" or "dispatch_as_sandbox" (mirrors execlogUnitBlock's
// scoping style in userdata_execlog_test.go).
func heredocBlock(t *testing.T, out, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(out, startMarker)
	if start == -1 {
		t.Fatalf("heredoc start marker %q not found in rendered userdata", startMarker)
	}
	rest := out[start:]
	end := strings.Index(rest, "\n"+endMarker)
	if end == -1 {
		t.Fatalf("heredoc end marker %q not found after start marker %q", endMarker, startMarker)
	}
	return rest[:end]
}

// assertDispatchAsSandboxJoinsBeforeRunuser is the shared assertion for every
// poller/daemon block: dispatch_as_sandbox() must exist, must write the
// (sub)shell's own PID into $CGROUP_PROCS via $BASHPID (never the bare $$,
// which inside a `( ... )` subshell reports the INVOKING shell's PID, not the
// forked child's — see the comment at each site), BEFORE it execs into
// runuser, and the block must carry no leftover bare `sudo -u sandbox`
// dispatch of an agent turn.
func assertDispatchAsSandboxJoinsBeforeRunuser(t *testing.T, label, block string, wantCallCount int, regressionPattern string) {
	t.Helper()

	fnStart := strings.Index(block, "dispatch_as_sandbox() {")
	if fnStart == -1 {
		t.Fatalf("%s: dispatch_as_sandbox() helper not found in block:\n%s", label, block)
	}
	fnEnd := strings.Index(block[fnStart:], "\n}")
	if fnEnd == -1 {
		t.Fatalf("%s: dispatch_as_sandbox() helper has no closing brace", label)
	}
	fnBody := block[fnStart : fnStart+fnEnd]

	joinIdx := strings.Index(fnBody, `echo "$BASHPID" > "$CGROUP_PROCS"`)
	if joinIdx == -1 {
		t.Fatalf("%s: dispatch_as_sandbox() does not join $CGROUP_PROCS via $BASHPID:\n%s", label, fnBody)
	}
	runuserIdx := strings.Index(fnBody, "exec runuser -u sandbox --")
	if runuserIdx == -1 {
		t.Fatalf("%s: dispatch_as_sandbox() does not exec runuser -u sandbox:\n%s", label, fnBody)
	}
	if joinIdx > runuserIdx {
		t.Errorf("%s: cgroup join must happen BEFORE exec runuser (join must run while still root), got join at %d, runuser at %d", label, joinIdx, runuserIdx)
	}

	// A bare $$ anywhere in the function body (outside the BASHPID line already
	// checked above) would be the exact subshell-PID bug this fix avoids.
	if strings.Contains(fnBody, "echo $$ >") {
		t.Errorf("%s: dispatch_as_sandbox() must use $BASHPID, not bare $$, inside its ( ... ) subshell:\n%s", label, fnBody)
	}

	if got := strings.Count(block, "dispatch_as_sandbox \""); got != wantCallCount {
		t.Errorf("%s: expected %d dispatch_as_sandbox call sites, got %d", label, wantCallCount, got)
	}

	if strings.Contains(block, regressionPattern) {
		t.Errorf("%s: found a leftover bare %q dispatch — this bypasses the cgroup join and is a regression", label, regressionPattern)
	}
}

func TestUserData_SlackPollerDispatchJoinsCgroupViaRunuser(t *testing.T) {
	p := baseProfile()
	enabled := true
	p.Spec.Notification = &profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Inbound: &profile.NotificationSlackInboundSpec{Enabled: &enabled},
		},
	}
	out, err := generateUserData(p, "sb-cgroup-slack", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	block := heredocBlock(t, out, "cat > /opt/km/bin/km-slack-inbound-poller << 'SLACKINBOUND'", "SLACKINBOUND")
	// 3 dispatch sites: codex resume, codex first turn, claude.
	assertDispatchAsSandboxJoinsBeforeRunuser(t, "slack poller", block, 3, `sudo -u sandbox bash -lc "`)
}

func TestUserData_GitHubPollerDispatchJoinsCgroupViaRunuser(t *testing.T) {
	p := baseProfile()
	enabled := true
	p.Spec.Notification = &profile.NotificationSpec{
		Github: &profile.NotificationGitHubSpec{
			Inbound: &profile.NotificationGitHubInboundSpec{Enabled: &enabled},
		},
	}
	out, err := generateUserData(p, "sb-cgroup-github", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	block := heredocBlock(t, out, "cat > /opt/km/bin/km-github-inbound-poller << 'GITHUBINBOUND'", "GITHUBINBOUND")
	// 4 dispatch sites: codex resume, codex first turn, claude, claude retry.
	assertDispatchAsSandboxJoinsBeforeRunuser(t, "github poller", block, 4, `sudo -u sandbox bash -lc "`)
}

func TestUserData_H1PollerDispatchJoinsCgroupViaRunuser(t *testing.T) {
	p := baseProfile()
	enabled := true
	p.Spec.Notification = &profile.NotificationSpec{
		H1: &profile.NotificationH1Spec{
			Inbound: &profile.NotificationH1InboundSpec{Enabled: &enabled},
		},
	}
	out, err := generateUserData(p, "sb-cgroup-h1", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	block := heredocBlock(t, out, "cat > /opt/km/bin/km-h1-inbound-poller << 'H1INBOUND'", "H1INBOUND")
	// 4 dispatch sites: codex resume, codex first turn, claude, claude retry.
	assertDispatchAsSandboxJoinsBeforeRunuser(t, "h1 poller", block, 4, `sudo -u sandbox bash -lc "`)
}

func TestUserData_WebhookPollerDispatchJoinsCgroupViaRunuser(t *testing.T) {
	p := baseProfile()
	enabled := true
	p.Spec.Notification = &profile.NotificationSpec{
		Webhook: &profile.NotificationWebhookSpec{
			Inbound: &profile.NotificationWebhookInboundSpec{Enabled: &enabled},
		},
	}
	out, err := generateUserData(p, "sb-cgroup-webhook", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	block := heredocBlock(t, out, "cat > /opt/km/bin/km-webhook-inbound-poller << 'WEBHOOKINBOUND'", "WEBHOOKINBOUND")
	// 2 dispatch sites: codex, claude.
	assertDispatchAsSandboxJoinsBeforeRunuser(t, "webhook poller", block, 2, `sudo -u sandbox bash -lc "`)
}

// TestUserData_QueueRunnerTmuxDispatchJoinsCgroupViaRunuser covers the `km agent
// run` / `km at agent run` path (km-queue-runner), which is seeded
// unconditionally on every sandbox (no notification.* gate). It uses the
// bash -c (non-login) twin of dispatch_as_sandbox, wrapping `tmux new-session`
// rather than a direct claude/codex invocation — the tmux server (and the pane
// process it spawns to run the agent) inherits whatever cgroup this dispatch
// call was in at fork time.
func TestUserData_QueueRunnerTmuxDispatchJoinsCgroupViaRunuser(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-cgroup-queue", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	block := heredocBlock(t, out, "cat > /opt/km/bin/km-queue-runner << 'KMQUEUE'", "KMQUEUE")

	if !strings.Contains(block, `dispatch_as_sandbox "tmux new-session`) {
		t.Errorf("km-queue-runner: tmux new-session dispatch must route through dispatch_as_sandbox, got:\n%s", block)
	}
	if strings.Contains(block, `sudo -u sandbox bash -c "tmux new-session`) {
		t.Errorf("km-queue-runner: found a leftover bare 'sudo -u sandbox bash -c \"tmux new-session' dispatch — this bypasses the cgroup join and is a regression")
	}
	assertDispatchAsSandboxJoinsBeforeRunuser(t, "queue runner", block, 1, `sudo -u sandbox bash -c "tmux new-session`)

	// The dispatch_as_sandbox() helper here is the bash -c (non-login) variant —
	// it must NOT silently regress to -lc, which would change tmux's working
	// environment from the original bash -c invocation.
	fnStart := strings.Index(block, "dispatch_as_sandbox() {")
	fnEnd := strings.Index(block[fnStart:], "\n}")
	fnBody := block[fnStart : fnStart+fnEnd]
	if !strings.Contains(fnBody, `exec runuser -u sandbox -- bash -c "$1"`) {
		t.Errorf("km-queue-runner: dispatch_as_sandbox() must exec 'runuser -u sandbox -- bash -c \"$1\"' (non-login, matching the original bash -c), got:\n%s", fnBody)
	}

	// The synchronization-only tmux wait-for call does not dispatch an agent
	// turn (it just blocks on a signal from the already-cgroup-joined session)
	// and is deliberately left as a plain sudo -u sandbox call.
	if !strings.Contains(block, `sudo -u sandbox bash -c "tmux wait-for`) {
		t.Errorf("km-queue-runner: expected the tmux wait-for synchronization call to remain a plain sudo -u sandbox dispatch")
	}
}
