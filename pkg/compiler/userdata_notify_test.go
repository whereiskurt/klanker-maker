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

// ============================================================
// Phase 63 (Plan 04): sent_any dispatch + Slack env var tests
// ============================================================

// profileWithSlack is a test helper that builds a minimal profile with Slack fields set.
// emailEnabled / slackEnabled are *bool (nil = not set). channelOverride is optional.
func profileWithSlack(slackEnabled *bool, emailEnabled *bool, channelOverride string) *profile.SandboxProfile {
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{
		NotifyOnPermission:         true,
		NotifySlackEnabled:         slackEnabled,
		NotifyEmailEnabled:         emailEnabled,
		NotifySlackChannelOverride: channelOverride,
	}
	return p
}

// TestUserDataNotifyHook_HasSentAnyDispatch verifies that the km-notify-hook heredoc
// contains the sent_any multi-channel dispatch pattern (Phase 63 Plan 04).
func TestUserDataNotifyHook_HasSentAnyDispatch(t *testing.T) {
	p := baseProfile()
	ud, err := generateUserData(p, "sb-test-p63-01", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"sent_any=0",
		`KM_NOTIFY_EMAIL_ENABLED:-1`,
		`KM_NOTIFY_SLACK_ENABLED:-0`,
		`[[ $sent_any -eq 1 ]]`,
		"/opt/km/bin/km-slack post",
	} {
		if !strings.Contains(ud, want) {
			t.Errorf("missing %q in user-data heredoc", want)
		}
	}
}

// TestUserDataNotifyEnv_SlackEnabledTrue verifies that notifySlackEnabled: true
// emits KM_NOTIFY_SLACK_ENABLED="1" in the env file.
func TestUserDataNotifyEnv_SlackEnabledTrue(t *testing.T) {
	tru := true
	p := profileWithSlack(&tru, nil, "")
	ud, err := generateUserData(p, "sb-test-p63-02", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ud, `KM_NOTIFY_SLACK_ENABLED="1"`) {
		t.Errorf("expected KM_NOTIFY_SLACK_ENABLED=\"1\" in user-data")
	}
	if !strings.Contains(ud, "/etc/profile.d/km-notify-env.sh") {
		t.Errorf("expected /etc/profile.d/km-notify-env.sh to be referenced")
	}
}

// TestUserDataNotifyEnv_SlackEnabledFalse verifies that notifySlackEnabled: false
// (explicit) emits KM_NOTIFY_SLACK_ENABLED="0".
func TestUserDataNotifyEnv_SlackEnabledFalse(t *testing.T) {
	fal := false
	p := profileWithSlack(&fal, nil, "")
	ud, err := generateUserData(p, "sb-test-p63-03", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ud, `KM_NOTIFY_SLACK_ENABLED="0"`) {
		t.Errorf("expected KM_NOTIFY_SLACK_ENABLED=\"0\" in user-data")
	}
}

// TestUserDataNotifyEnv_SlackEnabledNilPointer verifies that when notifySlackEnabled
// is omitted from the profile (nil pointer), the env file does NOT contain
// KM_NOTIFY_SLACK_ENABLED — hook uses its :-0 default (Phase 62 backward compat).
// Same expectation for KM_NOTIFY_EMAIL_ENABLED when notifyEmailEnabled is nil.
func TestUserDataNotifyEnv_SlackEnabledNilPointer(t *testing.T) {
	p := profileWithSlack(nil, nil, "") // both nil
	ud, err := generateUserData(p, "sb-test-p63-04", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ud, "KM_NOTIFY_SLACK_ENABLED=") {
		t.Errorf("user-data should NOT contain KM_NOTIFY_SLACK_ENABLED when notifySlackEnabled is nil")
	}
	if strings.Contains(ud, "KM_NOTIFY_EMAIL_ENABLED=") {
		t.Errorf("user-data should NOT contain KM_NOTIFY_EMAIL_ENABLED when notifyEmailEnabled is nil")
	}
}

// TestUserDataNotifyEnv_EmailEnabledFalse verifies that notifyEmailEnabled: false
// emits KM_NOTIFY_EMAIL_ENABLED="0" in the env file.
func TestUserDataNotifyEnv_EmailEnabledFalse(t *testing.T) {
	fal := false
	p := profileWithSlack(nil, &fal, "")
	ud, err := generateUserData(p, "sb-test-p63-05", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ud, `KM_NOTIFY_EMAIL_ENABLED="0"`) {
		t.Errorf("expected KM_NOTIFY_EMAIL_ENABLED=\"0\" in user-data")
	}
}

// TestUserDataNotifyEnv_ChannelOverrideBaked verifies that notifySlackChannelOverride
// bakes KM_SLACK_CHANNEL_ID into the env file at compile time.
func TestUserDataNotifyEnv_ChannelOverrideBaked(t *testing.T) {
	tru := true
	p := profileWithSlack(&tru, nil, "C0123ABC")
	ud, err := generateUserData(p, "sb-test-p63-06", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ud, `KM_SLACK_CHANNEL_ID="C0123ABC"`) {
		t.Errorf("expected KM_SLACK_CHANNEL_ID=\"C0123ABC\" in user-data")
	}
}

// TestUserDataNotifyEnv_NoChannelOverride_NoChannelID verifies that when no
// notifySlackChannelOverride is set, KM_SLACK_CHANNEL_ID is NOT emitted in the
// env file at compile time (Plan 08 injects it at runtime).
func TestUserDataNotifyEnv_NoChannelOverride_NoChannelID(t *testing.T) {
	tru := true
	p := profileWithSlack(&tru, nil, "") // enabled but no override
	ud, err := generateUserData(p, "sb-test-p63-07", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ud, `KM_SLACK_CHANNEL_ID=`) {
		t.Errorf("user-data should NOT contain KM_SLACK_CHANNEL_ID when notifySlackChannelOverride is empty")
	}
}

// TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime verifies that
// KM_SLACK_BRIDGE_URL is never written to the env file at compile time —
// it requires a runtime SSM lookup performed by Plan 08 km create.
func TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime(t *testing.T) {
	tru := true
	p := profileWithSlack(&tru, nil, "C0123ABC")
	ud, err := generateUserData(p, "sb-test-p63-08", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ud, `KM_SLACK_BRIDGE_URL=`) {
		t.Errorf("user-data must NOT contain KM_SLACK_BRIDGE_URL at compile time (Plan 08 injects at runtime)")
	}
}

// TestUserDataNotifyHook_Phase62Profile_NoRegression verifies that a profile
// using only Phase 62 fields (no Slack fields) produces an env file with no
// KM_SLACK_* entries — Phase 62 backward compatibility end-to-end.
func TestUserDataNotifyHook_Phase62Profile_NoRegression(t *testing.T) {
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{
		NotifyOnPermission:       true,
		NotifyOnIdle:             true,
		NotifyCooldownSeconds:    60,
		NotificationEmailAddress: "ops@example.com",
		// No Slack fields — all nil/zero
	}
	ud, err := generateUserData(p, "sb-test-p63-09", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The env file section must not contain any Slack keys.
	// The heredoc itself references KM_SLACK_CHANNEL_ID as a runtime var; we need
	// to check only the env file emission block. A simple grep for the assignment
	// form KEY="..." is sufficient because runtime references use ${...} form.
	if strings.Contains(ud, `KM_NOTIFY_SLACK_ENABLED="`) {
		t.Errorf("Phase 62 profile must not emit KM_NOTIFY_SLACK_ENABLED in env file")
	}
	if strings.Contains(ud, `KM_NOTIFY_EMAIL_ENABLED="`) {
		t.Errorf("Phase 62 profile must not emit KM_NOTIFY_EMAIL_ENABLED in env file")
	}
	if strings.Contains(ud, `KM_SLACK_CHANNEL_ID="`) {
		t.Errorf("Phase 62 profile must not emit KM_SLACK_CHANNEL_ID in env file")
	}
	if strings.Contains(ud, `KM_SLACK_BRIDGE_URL="`) {
		t.Errorf("Phase 62 profile must not emit KM_SLACK_BRIDGE_URL in env file")
	}
	// Phase 62 fields still present.
	if !strings.Contains(ud, `KM_NOTIFY_ON_PERMISSION="1"`) {
		t.Errorf("Phase 62 KM_NOTIFY_ON_PERMISSION should still be emitted")
	}
	if !strings.Contains(ud, `KM_NOTIFY_EMAIL="ops@example.com"`) {
		t.Errorf("Phase 62 KM_NOTIFY_EMAIL should still be emitted")
	}
}

// TestUserDataNotifyHook_HookScriptStillExitsZero verifies that the km-notify-hook
// heredoc always ends with exit 0 (Phase 62 HOOK-05 invariant: hook never blocks Claude).
func TestUserDataNotifyHook_HookScriptStillExitsZero(t *testing.T) {
	p := baseProfile()
	ud, err := generateUserData(p, "sb-test-p63-10", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Find the heredoc body between the open and close delimiters.
	startMarker := "cat > /opt/km/bin/km-notify-hook << 'KM_NOTIFY_HOOK_EOF'"
	endMarker := "KM_NOTIFY_HOOK_EOF"
	startIdx := strings.Index(ud, startMarker)
	if startIdx < 0 {
		t.Fatal("km-notify-hook heredoc open not found")
	}
	afterOpen := ud[startIdx+len(startMarker):]
	closeIdx := strings.Index(afterOpen, endMarker)
	if closeIdx < 0 {
		t.Fatal("km-notify-hook heredoc close not found")
	}
	heredocBody := strings.TrimSpace(afterOpen[:closeIdx])
	lines := strings.Split(heredocBody, "\n")
	lastLine := strings.TrimSpace(lines[len(lines)-1])
	if lastLine != "exit 0" {
		t.Errorf("last line of km-notify-hook heredoc = %q, want \"exit 0\"", lastLine)
	}
}
