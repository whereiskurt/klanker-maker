package compiler

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// ============================================================
// Task 1: Hook script emission + km-notify-env.sh (HOOK-01, HOOK-03)
// ============================================================

// TestUserDataNotifyHookAlwaysPresent verifies that EVERY sandbox user-data
// unconditionally contains the km-notify-hook script heredoc (HOOK-01).
// The profile has no notify fields set.
func TestUserDataNotifyHookAlwaysPresent(t *testing.T) {
	p := baseProfile()
	// Ensure no CLI block is set — hook must be present regardless.
	p.Spec.CLI = nil

	ud, err := generateUserData(p, "sb-test01", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	must := []string{
		"cat > /opt/km/bin/km-notify-hook << 'KM_NOTIFY_HOOK_EOF'",
		"chmod +x /opt/km/bin/km-notify-hook",
		`case "$event" in`,
		"KM_NOTIFY_ON_PERMISSION:-0",
		"KM_NOTIFY_ON_IDLE:-0",
		"/opt/km/bin/km-send",
		`--body "$body_file"`,
	}
	for _, want := range must {
		if !strings.Contains(ud, want) {
			t.Errorf("user-data missing expected substring %q", want)
		}
	}
}

// TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock verifies that when Spec.CLI is nil,
// NO /etc/profile.d/km-notify-env.sh block is written (HOOK-03 negative).
func TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock(t *testing.T) {
	p := baseProfile()
	p.Spec.CLI = nil // explicit — no cli block

	ud, err := generateUserData(p, "sb-test02", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	if strings.Contains(ud, "/etc/profile.d/km-notify-env.sh") {
		t.Errorf("user-data should NOT write km-notify-env.sh when Spec.CLI is nil")
	}
}

// TestUserDataNotifyEnvVars_PermissionOnly verifies that when only notifyOnPermission
// is set, KM_NOTIFY_ON_PERMISSION="1" is written and the other optional vars are absent.
// (HOOK-03 positive — partial)
func TestUserDataNotifyEnvVars_PermissionOnly(t *testing.T) {
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{
		NotifyOnPermission: true,
		// NotifyOnIdle, NotifyCooldownSeconds, NotificationEmailAddress: all zero/empty
	}

	ud, err := generateUserData(p, "sb-test03", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	if !strings.Contains(ud, `KM_NOTIFY_ON_PERMISSION="1"`) {
		t.Errorf("expected KM_NOTIFY_ON_PERMISSION=\"1\" in user-data")
	}
	if !strings.Contains(ud, `KM_NOTIFY_ON_IDLE="0"`) {
		t.Errorf("expected KM_NOTIFY_ON_IDLE=\"0\" in user-data (CLI block is set)")
	}
	// No cooldown or email vars when those fields are zero/empty.
	if strings.Contains(ud, "KM_NOTIFY_COOLDOWN_SECONDS=") {
		t.Errorf("user-data should NOT contain KM_NOTIFY_COOLDOWN_SECONDS when notifyCooldownSeconds==0")
	}
	if strings.Contains(ud, "KM_NOTIFY_EMAIL=") {
		t.Errorf("user-data should NOT contain KM_NOTIFY_EMAIL when notificationEmailAddress is unset")
	}
}

// TestUserDataNotifyEnvVars_IdleAndCooldown verifies that notifyOnIdle and
// notifyCooldownSeconds both appear in the env file (HOOK-03).
func TestUserDataNotifyEnvVars_IdleAndCooldown(t *testing.T) {
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{
		NotifyOnIdle:          true,
		NotifyCooldownSeconds: 30,
	}

	ud, err := generateUserData(p, "sb-test04", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	if !strings.Contains(ud, `KM_NOTIFY_ON_IDLE="1"`) {
		t.Errorf("expected KM_NOTIFY_ON_IDLE=\"1\" in user-data")
	}
	if !strings.Contains(ud, `KM_NOTIFY_COOLDOWN_SECONDS="30"`) {
		t.Errorf("expected KM_NOTIFY_COOLDOWN_SECONDS=\"30\" in user-data")
	}
}

// TestUserDataNotifyEnvVars_RecipientOverride verifies that notificationEmailAddress
// produces a KM_NOTIFY_EMAIL env var in the env file (HOOK-03).
func TestUserDataNotifyEnvVars_RecipientOverride(t *testing.T) {
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{
		NotificationEmailAddress: "team@example.com",
	}

	ud, err := generateUserData(p, "sb-test05", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	if !strings.Contains(ud, `KM_NOTIFY_EMAIL="team@example.com"`) {
		t.Errorf("expected KM_NOTIFY_EMAIL=\"team@example.com\" in user-data")
	}
}

// TestUserDataNotifyEnvVars_ExplicitFalseStillEmitsZero verifies that when
// Spec.CLI is set but both booleans are false, the env vars are still written
// as "0". (Pragmatic v1 behavior: bool zero value + omitempty means we cannot
// distinguish unset-from-YAML vs explicit-false; we emit the var whenever
// Spec.CLI != nil.)  Documents the CONTEXT.md deviation (see SUMMARY.md).
func TestUserDataNotifyEnvVars_ExplicitFalseStillEmitsZero(t *testing.T) {
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{
		NotifyOnPermission: false,
		NotifyOnIdle:       false,
	}

	ud, err := generateUserData(p, "sb-test06", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	if !strings.Contains(ud, `KM_NOTIFY_ON_PERMISSION="0"`) {
		t.Errorf("expected KM_NOTIFY_ON_PERMISSION=\"0\" in user-data when CLI block set but false")
	}
	if !strings.Contains(ud, `KM_NOTIFY_ON_IDLE="0"`) {
		t.Errorf("expected KM_NOTIFY_ON_IDLE=\"0\" in user-data when CLI block set but false")
	}
}

// ============================================================
// Task 2: settings.json compile-time merge (HOOK-02)
// ============================================================

// extractHeredocBody scans ud for a line matching
//
//	cat > '<path>' << '<delim>'
//
// and returns the text between that line (exclusive) and the next bare <delim> line.
func extractHeredocBody(t *testing.T, ud, path, delim string) string {
	t.Helper()
	lines := strings.Split(ud, "\n")
	marker := fmt.Sprintf("cat > '%s' << '%s'", path, delim)
	start := -1
	for i, l := range lines {
		if strings.Contains(l, marker) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("heredoc marker not found for path=%q delim=%q", path, delim)
	}
	var body strings.Builder
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == delim {
			break
		}
		if i > start {
			body.WriteByte('\n')
		}
		body.WriteString(lines[i])
	}
	return body.String()
}

// drillHookCmd drills into the nested settings.json structure:
// hooks.<event>[0].hooks[0].command and returns the command string.
func drillHookCmd(t *testing.T, m map[string]interface{}, event string) string {
	t.Helper()
	hooks, ok := m["hooks"].(map[string]interface{})
	if !ok {
		t.Fatalf("hooks is not a map")
	}
	eventArr, ok := hooks[event].([]interface{})
	if !ok || len(eventArr) == 0 {
		t.Fatalf("hooks.%s is not a non-empty array", event)
	}
	group, ok := eventArr[0].(map[string]interface{})
	if !ok {
		t.Fatalf("hooks.%s[0] is not a map", event)
	}
	innerArr, ok := group["hooks"].([]interface{})
	if !ok || len(innerArr) == 0 {
		t.Fatalf("hooks.%s[0].hooks is not a non-empty array", event)
	}
	entry, ok := innerArr[0].(map[string]interface{})
	if !ok {
		t.Fatalf("hooks.%s[0].hooks[0] is not a map", event)
	}
	cmd, _ := entry["command"].(string)
	return cmd
}

// TestUserDataNotifySettingsJSON_NoUserSettings verifies that a profile with
// no user-supplied settings.json still produces a valid merged settings.json
// containing km-notify-hook entries for both Notification and Stop (HOOK-02).
func TestUserDataNotifySettingsJSON_NoUserSettings(t *testing.T) {
	p := baseProfile()
	// No configFiles set.

	ud, err := generateUserData(p, "sb-test07", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	body := extractHeredocBody(t, ud, "/home/sandbox/.claude/settings.json", "KM_CONFIG_EOF")

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("merged settings.json is not valid JSON: %v\n%s", err, body)
	}

	notifCmd := drillHookCmd(t, got, "Notification")
	if notifCmd != "/opt/km/bin/km-notify-hook Notification" {
		t.Errorf("Notification hook cmd = %q, want %q", notifCmd, "/opt/km/bin/km-notify-hook Notification")
	}

	stopCmd := drillHookCmd(t, got, "Stop")
	if stopCmd != "/opt/km/bin/km-notify-hook Stop" {
		t.Errorf("Stop hook cmd = %q, want %q", stopCmd, "/opt/km/bin/km-notify-hook Stop")
	}
}

// TestUserDataNotifySettingsJSON_PreservesUserHooks verifies that when the profile
// supplies a settings.json with an existing Notification hook, the km hook is
// appended (not replaced) and a fresh Stop array is created (HOOK-02 merge).
func TestUserDataNotifySettingsJSON_PreservesUserHooks(t *testing.T) {
	p := baseProfile()
	if p.Spec.Execution.ConfigFiles == nil {
		p.Spec.Execution.ConfigFiles = map[string]string{}
	}
	p.Spec.Execution.ConfigFiles["/home/sandbox/.claude/settings.json"] =
		`{"hooks":{"Notification":[{"hooks":[{"type":"command","command":"/usr/local/bin/user-thing"}]}]}}`

	ud, err := generateUserData(p, "sb-test08", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	body := extractHeredocBody(t, ud, "/home/sandbox/.claude/settings.json", "KM_CONFIG_EOF")
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("merged settings.json is not valid JSON: %v\n%s", err, body)
	}

	hooks, _ := got["hooks"].(map[string]interface{})
	notifArr, _ := hooks["Notification"].([]interface{})
	if len(notifArr) != 2 {
		t.Errorf("expected 2 entries in hooks.Notification (user + km), got %d", len(notifArr))
	}
	if len(notifArr) >= 1 {
		cmd0 := notifArr[0].(map[string]interface{})["hooks"].([]interface{})[0].(map[string]interface{})["command"]
		if cmd0 != "/usr/local/bin/user-thing" {
			t.Errorf("user hook at index 0 was clobbered: got %v", cmd0)
		}
	}
	if len(notifArr) >= 2 {
		cmd1 := notifArr[1].(map[string]interface{})["hooks"].([]interface{})[0].(map[string]interface{})["command"]
		if cmd1 != "/opt/km/bin/km-notify-hook Notification" {
			t.Errorf("km hook at index 1 is wrong: got %v", cmd1)
		}
	}

	stopArr, _ := hooks["Stop"].([]interface{})
	if len(stopArr) != 1 {
		t.Errorf("expected 1 entry in hooks.Stop, got %d", len(stopArr))
	}
}

// TestUserDataNotifySettingsJSON_PreservesNonHooksKeys verifies that a user-supplied
// settings.json with trustedDirectories is preserved after hook merge (HOOK-02).
func TestUserDataNotifySettingsJSON_PreservesNonHooksKeys(t *testing.T) {
	p := baseProfile()
	if p.Spec.Execution.ConfigFiles == nil {
		p.Spec.Execution.ConfigFiles = map[string]string{}
	}
	p.Spec.Execution.ConfigFiles["/home/sandbox/.claude/settings.json"] =
		`{"trustedDirectories":["/workspace"],"hooks":{}}`

	ud, err := generateUserData(p, "sb-test09", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	body := extractHeredocBody(t, ud, "/home/sandbox/.claude/settings.json", "KM_CONFIG_EOF")
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("merged settings.json is not valid JSON: %v\n%s", err, body)
	}
	if _, ok := got["trustedDirectories"]; !ok {
		t.Errorf("trustedDirectories key was clobbered by hook merge")
	}
}

// TestUserDataNotifySettingsJSON_InvalidUserJSON_FailsFast verifies that a
// malformed user-supplied settings.json causes generateUserData() to return
// an error containing "settings.json" — no partial user-data is produced (HOOK-02).
func TestUserDataNotifySettingsJSON_InvalidUserJSON_FailsFast(t *testing.T) {
	p := baseProfile()
	if p.Spec.Execution.ConfigFiles == nil {
		p.Spec.Execution.ConfigFiles = map[string]string{}
	}
	p.Spec.Execution.ConfigFiles["/home/sandbox/.claude/settings.json"] = "{not valid json}"

	_, err := generateUserData(p, "sb-test10", nil, "my-bucket", false, nil)
	if err == nil {
		t.Fatalf("expected error for invalid settings.json JSON, got nil")
	}
	if !strings.Contains(err.Error(), "settings.json") {
		t.Errorf("error message should mention settings.json, got %q", err.Error())
	}
}
