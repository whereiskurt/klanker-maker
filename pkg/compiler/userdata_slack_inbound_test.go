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
