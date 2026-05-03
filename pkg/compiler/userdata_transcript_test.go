package compiler

import (
	"encoding/json"
	"testing"
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

func TestUserData_NotifySlackTranscriptEnabledEnvVar(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 68-10")
}

func TestUserData_KMSlackStreamTableEnvVar(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 68-10")
}

func TestUserData_TranscriptDisabledOmitsEnvVar(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 68-10")
}
