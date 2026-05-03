package compiler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// Wave 0 stubs for Plans 68-09 / 68-10 (userdata env injection + hook registration).
// Bodies will be replaced by the implementing plans.

// TestUserData_PostToolUseHookRegistered (Plan 68-09): mergeNotifyHookIntoSettings
// must register a PostToolUse hook entry pointing at /opt/km/bin/km-notify-hook
// PostToolUse, alongside the existing Notification + Stop entries. The
// registration is unconditional (matches Phase 62/63's pattern — runtime gating
// happens inside the script via KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED).
func TestUserData_PostToolUseHookRegistered(t *testing.T) {
	p := baseProfile()

	ud, err := generateUserData(p, "sb-test-pthook", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	body := extractHeredocBody(t, ud, "/home/sandbox/.claude/settings.json", "KM_CONFIG_EOF")
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("merged settings.json is not valid JSON: %v\n%s", err, body)
	}

	cmd := drillHookCmd(t, got, "PostToolUse")
	want := "/opt/km/bin/km-notify-hook PostToolUse"
	if cmd != want {
		t.Errorf("PostToolUse hook cmd = %q, want %q", cmd, want)
	}

	// Sanity: Notification + Stop still registered (regression guard).
	if drillHookCmd(t, got, "Notification") != "/opt/km/bin/km-notify-hook Notification" {
		t.Errorf("Notification hook missing or wrong after PostToolUse addition")
	}
	if drillHookCmd(t, got, "Stop") != "/opt/km/bin/km-notify-hook Stop" {
		t.Errorf("Stop hook missing or wrong after PostToolUse addition")
	}
}

// transcriptProfile builds a Slack-enabled profile with notifySlackTranscriptEnabled
// set to the requested value. Mirrors profileWithSlack from userdata_notify_test.go
// but lets the caller toggle the new Phase 68 transcript flag.
func transcriptProfile(transcriptOn bool) *profile.SandboxProfile {
	p := baseProfile()
	tru := true
	p.Spec.CLI = &profile.CLISpec{
		NotifyOnPermission:           true,
		NotifySlackEnabled:           &tru,
		NotifySlackTranscriptEnabled: transcriptOn,
	}
	return p
}

// TestUserData_NotifySlackTranscriptEnabledEnvVar verifies that a profile with
// notifySlackTranscriptEnabled: true causes /etc/profile.d/km-notify-env.sh to
// contain KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED="1".
//
// Phase 68 Plan 10.
func TestUserData_NotifySlackTranscriptEnabledEnvVar(t *testing.T) {
	p := transcriptProfile(true)
	out, err := generateUserData(p, "sb-test-p68-01", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	if !strings.Contains(out, `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED="1"`) {
		t.Errorf("expected KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=\"1\" in user-data when notifySlackTranscriptEnabled=true")
	}
}

// TestUserData_KMSlackStreamTableEnvVar verifies that when transcript streaming
// is enabled, the env file contains KM_SLACK_STREAM_TABLE pointing at the
// table name resolved via Config.GetSlackStreamMessagesTableName() (default:
// "km-slack-stream-messages").
//
// Phase 68 Plan 10.
func TestUserData_KMSlackStreamTableEnvVar(t *testing.T) {
	p := transcriptProfile(true)
	out, err := generateUserData(p, "sb-test-p68-02", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	if !strings.Contains(out, `KM_SLACK_STREAM_TABLE="km-slack-stream-messages"`) {
		t.Errorf("expected KM_SLACK_STREAM_TABLE=\"km-slack-stream-messages\" in user-data when notifySlackTranscriptEnabled=true")
	}
}

// TestUserData_TranscriptDisabledOmitsEnvVar verifies that when transcript
// streaming is OFF (default / explicit false), neither env var is emitted in
// the env file. Phase 62 convention: omit env lines for unset features so the
// hook's :-default takes effect.
//
// Phase 68 Plan 10.
func TestUserData_TranscriptDisabledOmitsEnvVar(t *testing.T) {
	p := transcriptProfile(false)
	out, err := generateUserData(p, "sb-test-p68-03", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	// Match the env file format only ("export KEY=" / systemd "KEY=" inside notify.env)
	// — not the comment in the heredoc body that says "KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=1".
	if strings.Contains(out, `export KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=`) {
		t.Errorf("env file should NOT export KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED when notifySlackTranscriptEnabled=false")
	}
	if strings.Contains(out, `export KM_SLACK_STREAM_TABLE=`) {
		t.Errorf("env file should NOT export KM_SLACK_STREAM_TABLE when notifySlackTranscriptEnabled=false")
	}
}
