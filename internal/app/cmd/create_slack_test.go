package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
	"github.com/whereiskurt/klankrmkr/pkg/slack"
)

// ────────────────────────────────────────────────────────────────────────────
// Fake implementations
// ────────────────────────────────────────────────────────────────────────────

// fakeSlackAPI implements SlackAPI for tests.
type fakeSlackAPI struct {
	createChannelResult string
	createChannelErr    error
	inviteSharedErr     error
	channelInfoMember   bool
	channelInfoCount    int
	channelInfoErr      error

	// capture calls
	createChannelName  string
	inviteSharedCalled bool
	channelInfoCalled  bool
}

func (f *fakeSlackAPI) CreateChannel(_ context.Context, name string) (string, error) {
	f.createChannelName = name
	return f.createChannelResult, f.createChannelErr
}

func (f *fakeSlackAPI) InviteShared(_ context.Context, _, _ string) error {
	f.inviteSharedCalled = true
	return f.inviteSharedErr
}

func (f *fakeSlackAPI) ChannelInfo(_ context.Context, _ string) (int, bool, error) {
	f.channelInfoCalled = true
	return f.channelInfoCount, f.channelInfoMember, f.channelInfoErr
}

// fakeSSMParamStore implements SSMParamStore for tests.
// Shared across create_slack_test.go and destroy_slack_test.go (same package).
type fakeSSMParamStore struct {
	params map[string]string
}

func (f *fakeSSMParamStore) Get(_ context.Context, name string, _ bool) (string, error) {
	if v, ok := f.params[name]; ok {
		return v, nil
	}
	return "", nil
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func boolPtrCreate(b bool) *bool { return &b }

func profileWithSlack(enabled *bool, perSandbox bool, override string) *profile.SandboxProfile {
	p := &profile.SandboxProfile{}
	p.Spec.CLI = &profile.CLISpec{
		NotifySlackEnabled:         enabled,
		NotifySlackPerSandbox:      perSandbox,
		NotifySlackChannelOverride: override,
	}
	return p
}

// ────────────────────────────────────────────────────────────────────────────
// sanitizeChannelName tests
// ────────────────────────────────────────────────────────────────────────────

func TestSanitizeChannelName_Cases(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"research.team-A", "research-team-a"},
		{"ALL CAPS WITH SPACES", "all-caps-with-spaces"},
		{"  -leading-and-trailing-  ", "leading-and-trailing"},
		{"demo", "demo"},
		{"sb-abc-123", "sb-abc-123"},
		{"", ""},
		{"__underscores__", "__underscores__"},
		{strings.Repeat("a", 100), strings.Repeat("a", 80)},
	}

	for _, tc := range cases {
		got := sanitizeChannelName(tc.input)
		if got != tc.want {
			t.Errorf("sanitizeChannelName(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}

func TestSanitizeChannelName_CapAt80(t *testing.T) {
	input := strings.Repeat("a", 100)
	got := sanitizeChannelName(input)
	if len(got) != 80 {
		t.Errorf("sanitizeChannelName(100 chars) length = %d; want 80", len(got))
	}
}

func TestSanitizeChannelName_EmptyInputReturnsEmpty(t *testing.T) {
	if got := sanitizeChannelName(""); got != "" {
		t.Errorf("sanitizeChannelName(%q) = %q; want %q", "", got, "")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// resolveSlackChannel tests
// ────────────────────────────────────────────────────────────────────────────

func TestResolveSlack_NotEnabled_Skipped(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(false), false, "")
	api := &fakeSlackAPI{}
	ssmStore := &fakeSSMParamStore{}

	chID, perSb, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "myalias", api, ssmStore)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if chID != "" || perSb {
		t.Errorf("expected empty channelID and perSandbox=false, got %q/%v", chID, perSb)
	}
}

func TestResolveSlack_NilEnabled_Skipped(t *testing.T) {
	p := profileWithSlack(nil, false, "")
	api := &fakeSlackAPI{}
	ssmStore := &fakeSSMParamStore{}

	chID, perSb, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "", api, ssmStore)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if chID != "" || perSb {
		t.Errorf("expected empty channelID and perSandbox=false, got %q/%v", chID, perSb)
	}
}

func TestResolveSlack_SharedMode_HappyPath(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), false, "")
	api := &fakeSlackAPI{}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{
			"/km/slack/shared-channel-id": "C0SHARED",
		},
	}

	chID, perSb, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "", api, ssmStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chID != "C0SHARED" {
		t.Errorf("channelID = %q; want %q", chID, "C0SHARED")
	}
	if perSb {
		t.Error("perSandbox = true; want false for shared mode")
	}
	if api.createChannelName != "" {
		t.Error("CreateChannel was called for shared mode; should not be")
	}
}

func TestResolveSlack_SharedMode_NotConfigured_Error(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), false, "")
	api := &fakeSlackAPI{}
	ssmStore := &fakeSSMParamStore{} // no /km/slack/shared-channel-id

	_, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "", api, ssmStore)
	if err == nil {
		t.Fatal("expected error when shared channel not configured")
	}
	if !strings.Contains(err.Error(), "km slack init") {
		t.Errorf("error %q should mention 'km slack init'", err.Error())
	}
}

func TestResolveSlack_PerSandbox_HappyPath_WithAlias(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{
		createChannelResult: "CNEWCHANNEL",
	}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{
			"/km/slack/invite-email": "invite@example.com",
		},
	}

	chID, perSb, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "demo", api, ssmStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chID != "CNEWCHANNEL" {
		t.Errorf("channelID = %q; want %q", chID, "CNEWCHANNEL")
	}
	if !perSb {
		t.Error("perSandbox = false; want true for per-sandbox mode")
	}
	if api.createChannelName != "sb-demo" {
		t.Errorf("CreateChannel called with %q; want %q", api.createChannelName, "sb-demo")
	}
	if !api.inviteSharedCalled {
		t.Error("InviteShared was not called")
	}
}

func TestResolveSlack_PerSandbox_HappyPath_NoAlias(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{
		createChannelResult: "CNEWCHANNEL2",
	}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{
			"/km/slack/invite-email": "invite@example.com",
		},
	}

	chID, perSb, err := resolveSlackChannel(context.Background(), p, "sb-abc12345", "", api, ssmStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chID != "CNEWCHANNEL2" {
		t.Errorf("channelID = %q; want %q", chID, "CNEWCHANNEL2")
	}
	if !perSb {
		t.Error("perSandbox = false; want true")
	}
	// channel name should be sb-<sanitized sandboxID>
	if !strings.HasPrefix(api.createChannelName, "sb-") {
		t.Errorf("channel name %q should start with sb-", api.createChannelName)
	}
}

func TestResolveSlack_PerSandbox_AliasWithDots(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{
		createChannelResult: "CNEWCHANNEL3",
	}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{
			"/km/slack/invite-email": "invite@example.com",
		},
	}

	_, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "research.team-A", api, ssmStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if api.createChannelName != "sb-research-team-a" {
		t.Errorf("CreateChannel called with %q; want %q", api.createChannelName, "sb-research-team-a")
	}
}

func TestResolveSlack_PerSandbox_NameTaken_Error(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{
		createChannelErr: &slack.SlackAPIError{Method: "conversations.create", Code: "name_taken"},
	}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{
			"/km/slack/invite-email": "invite@example.com",
		},
	}

	_, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "demo", api, ssmStore)
	if err == nil {
		t.Fatal("expected error for name_taken")
	}
	if !strings.Contains(err.Error(), "name_taken") {
		t.Errorf("error %q should mention name_taken", err.Error())
	}
	// Should provide actionable guidance
	if !strings.Contains(err.Error(), "--alias") && !strings.Contains(err.Error(), "notifySlackChannelOverride") {
		t.Errorf("error %q should mention --alias or notifySlackChannelOverride", err.Error())
	}
	if api.inviteSharedCalled {
		t.Error("InviteShared should not be called after name_taken error")
	}
}

func TestResolveSlack_PerSandbox_InviteFails_Error(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{
		createChannelResult: "CNEW",
		inviteSharedErr:     fmt.Errorf("invite failed"),
	}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{
			"/km/slack/invite-email": "invite@example.com",
		},
	}

	_, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "demo", api, ssmStore)
	if err == nil {
		t.Fatal("expected error when invite fails")
	}
	if !strings.Contains(err.Error(), "invite") {
		t.Errorf("error %q should mention invite", err.Error())
	}
}

func TestResolveSlack_Override_HappyPath_BotIsMember(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), false, "C0OVERRIDE")
	api := &fakeSlackAPI{
		channelInfoMember: true,
		channelInfoCount:  5,
	}
	ssmStore := &fakeSSMParamStore{}

	chID, perSb, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "", api, ssmStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chID != "C0OVERRIDE" {
		t.Errorf("channelID = %q; want %q", chID, "C0OVERRIDE")
	}
	if perSb {
		t.Error("perSandbox = true; want false for override mode")
	}
	if api.createChannelName != "" {
		t.Error("CreateChannel should not be called for override mode")
	}
	if api.inviteSharedCalled {
		t.Error("InviteShared should not be called for override mode")
	}
}

func TestResolveSlack_Override_BotNotMember_Error(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), false, "C0OVERRIDE")
	api := &fakeSlackAPI{
		channelInfoMember: false,
		channelInfoCount:  3,
	}
	ssmStore := &fakeSSMParamStore{}

	_, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "", api, ssmStore)
	if err == nil {
		t.Fatal("expected error when bot is not a member")
	}
	if !strings.Contains(err.Error(), "not a member") {
		t.Errorf("error %q should mention 'not a member'", err.Error())
	}
}

func TestResolveSlack_Override_ChannelNotFound_Error(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), false, "C0NOTFOUND")
	api := &fakeSlackAPI{
		channelInfoErr: &slack.SlackAPIError{Method: "conversations.info", Code: "channel_not_found"},
	}
	ssmStore := &fakeSSMParamStore{}

	_, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "", api, ssmStore)
	if err == nil {
		t.Fatal("expected error when channel not found")
	}
	if !strings.Contains(err.Error(), "C0NOTFOUND") {
		t.Errorf("error %q should mention the channel ID", err.Error())
	}
}

// TestResolveSlack_InvalidOverrideID_Error verifies defense-in-depth regex check.
func TestResolveSlack_InvalidOverrideID_Error(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), false, "not-a-channel-id")
	api := &fakeSlackAPI{}
	ssmStore := &fakeSSMParamStore{}

	_, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "", api, ssmStore)
	if err == nil {
		t.Fatal("expected error for invalid channel ID format")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// injectSlackEnvIntoSandbox tests
// ────────────────────────────────────────────────────────────────────────────

// fakeSSMRunner implements SSMRunner for tests.
// callCount tracks how many times RunShell has been called.
// failTimes specifies how many initial calls should return err before succeeding.
// If failTimes is negative or larger than callCount, err is always returned.
type fakeSSMRunner struct {
	capturedScript string
	lastInstance   string
	err            error
	callCount      int
	failTimes      int // fail this many times before succeeding (0 = always succeed)
}

func (f *fakeSSMRunner) RunShell(_ context.Context, instanceID string, script string) error {
	f.callCount++
	f.capturedScript = script
	f.lastInstance = instanceID
	// failTimes > 0: fail that many times then succeed.
	// failTimes == 0 and err != nil: always fail (backward compat with existing tests).
	if f.failTimes > 0 {
		if f.callCount <= f.failTimes {
			return f.err
		}
		return nil
	}
	return f.err
}

func TestInjectSlackEnvIntoSandbox_ScriptContainsEnvVars(t *testing.T) {
	runner := &fakeSSMRunner{}

	err := injectSlackEnvIntoSandbox(context.Background(), runner, "i-abc123", "C0CHANNEL", "https://bridge.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(runner.capturedScript, "KM_SLACK_CHANNEL_ID") {
		t.Error("script should contain KM_SLACK_CHANNEL_ID")
	}
	if !strings.Contains(runner.capturedScript, "KM_SLACK_BRIDGE_URL") {
		t.Error("script should contain KM_SLACK_BRIDGE_URL")
	}
	if !strings.Contains(runner.capturedScript, "C0CHANNEL") {
		t.Error("script should contain channel ID value C0CHANNEL")
	}
	if !strings.Contains(runner.capturedScript, "https://bridge.example.com") {
		t.Error("script should contain bridge URL value")
	}
}

func TestInjectSlackEnvIntoSandbox_PropagatesRunnerError(t *testing.T) {
	runner := &fakeSSMRunner{err: errors.New("ssm failed")}

	err := injectSlackEnvIntoSandbox(context.Background(), runner, "i-abc123", "C0CHANNEL", "https://bridge.example.com")
	if err == nil {
		t.Fatal("expected error from runner failure")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// fakeOutputter — used by runStep11dInject tests
// ────────────────────────────────────────────────────────────────────────────

type fakeOutputter struct {
	outputs map[string]any
	err     error
}

func (f *fakeOutputter) run(_ context.Context, _ string) (map[string]any, error) {
	return f.outputs, f.err
}

// ────────────────────────────────────────────────────────────────────────────
// TestStep11d_VisibleOutput — 5 stderr-branch tests (Wave 0 RED)
// ────────────────────────────────────────────────────────────────────────────

// errInvalidInstance simulates an SSM InvalidInstanceId error.
var errInvalidInstance = errors.New("InvalidInstanceId: instance not yet registered")

func TestStep11d_VisibleOutput_NoBridgeURL(t *testing.T) {
	store := &fakeSSMParamStore{params: map[string]string{}} // no bridge-url
	runner := &fakeSSMRunner{}
	out := &fakeOutputter{
		outputs: map[string]any{
			"ec2spot_instances": map[string]any{
				"i-abc": map[string]any{"instance_id": "i-abc123"},
			},
		},
	}
	got := captureStderr(t, func() {
		runStep11dInject(context.Background(), store, runner, "/tmp/sandbox", out.run, extractOutputInstanceID, "sb-test", "CTEST", 1, time.Microsecond)
	})
	if !strings.Contains(got, "⚠ Slack: /km/slack/bridge-url not configured") {
		t.Errorf("stderr %q should contain bridge-url not-configured message", got)
	}
}

func TestStep11d_VisibleOutput_OutputsErr(t *testing.T) {
	store := &fakeSSMParamStore{params: map[string]string{"/km/slack/bridge-url": "https://bridge.example.com"}}
	runner := &fakeSSMRunner{}
	outErr := errors.New("terraform output failed")
	out := &fakeOutputter{err: outErr}
	got := captureStderr(t, func() {
		runStep11dInject(context.Background(), store, runner, "/tmp/sandbox", out.run, extractOutputInstanceID, "sb-test", "CTEST", 1, time.Microsecond)
	})
	if !strings.Contains(got, "⚠ Slack: failed to read terraform outputs") {
		t.Errorf("stderr %q should contain terraform outputs failure message", got)
	}
	if !strings.Contains(got, outErr.Error()) {
		t.Errorf("stderr %q should contain error text %q", got, outErr.Error())
	}
}

func TestStep11d_VisibleOutput_NoInstanceID(t *testing.T) {
	store := &fakeSSMParamStore{params: map[string]string{"/km/slack/bridge-url": "https://bridge.example.com"}}
	runner := &fakeSSMRunner{}
	out := &fakeOutputter{
		outputs: map[string]any{}, // no ec2spot_instances → extractOutputInstanceID returns ""
	}
	got := captureStderr(t, func() {
		runStep11dInject(context.Background(), store, runner, "/tmp/sandbox", out.run, extractOutputInstanceID, "sb-test", "CTEST", 1, time.Microsecond)
	})
	if !strings.Contains(got, "⚠ Slack: no EC2 instance ID in outputs") {
		t.Errorf("stderr %q should contain no-instance-ID message", got)
	}
}

func TestStep11d_VisibleOutput_SSMFail(t *testing.T) {
	store := &fakeSSMParamStore{params: map[string]string{"/km/slack/bridge-url": "https://bridge.example.com"}}
	runner := &fakeSSMRunner{err: errInvalidInstance, failTimes: 0} // always fail (failTimes=0 + err != nil)
	out := &fakeOutputter{
		outputs: map[string]any{
			"ec2spot_instances": map[string]any{
				"i-abc": map[string]any{"instance_id": "i-abc123"},
			},
		},
	}
	got := captureStderr(t, func() {
		runStep11dInject(context.Background(), store, runner, "/tmp/sandbox", out.run, extractOutputInstanceID, "sb-test", "CTEST", 1, time.Microsecond)
	})
	if !strings.Contains(got, "⚠ Slack: SSM SendCommand failed") {
		t.Errorf("stderr %q should contain SSM SendCommand failed message", got)
	}
	if !strings.Contains(got, errInvalidInstance.Error()) {
		t.Errorf("stderr %q should contain error text %q", got, errInvalidInstance.Error())
	}
}

func TestStep11d_VisibleOutput_Success(t *testing.T) {
	store := &fakeSSMParamStore{params: map[string]string{"/km/slack/bridge-url": "https://bridge.example.com"}}
	runner := &fakeSSMRunner{} // no err — succeeds
	out := &fakeOutputter{
		outputs: map[string]any{
			"ec2spot_instances": map[string]any{
				"i-abc": map[string]any{"instance_id": "i-abc123"},
			},
		},
	}
	got := captureStderr(t, func() {
		runStep11dInject(context.Background(), store, runner, "/tmp/sandbox", out.run, extractOutputInstanceID, "sb-test", "C-TEST", 1, time.Microsecond)
	})
	if !strings.Contains(got, "✓ Slack: channel C-TEST wired into sandbox env") {
		t.Errorf("stderr %q should contain success message with channel ID", got)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// TestInjectSlackEnv retry-loop tests (Wave 0 RED)
// ────────────────────────────────────────────────────────────────────────────

func TestInjectSlackEnv_RetryUntilAgentReady(t *testing.T) {
	store := &fakeSSMParamStore{params: map[string]string{"/km/slack/bridge-url": "https://bridge.example.com"}}
	// failTimes=2: fails on attempts 1 and 2, succeeds on attempt 3
	runner := &fakeSSMRunner{err: errInvalidInstance, failTimes: 2}
	out := &fakeOutputter{
		outputs: map[string]any{
			"ec2spot_instances": map[string]any{
				"i-abc": map[string]any{"instance_id": "i-abc123"},
			},
		},
	}
	captureStderr(t, func() {
		runStep11dInject(context.Background(), store, runner, "/tmp/sandbox", out.run, extractOutputInstanceID, "sb-test", "CTEST", 6, time.Microsecond)
	})
	if runner.callCount != 3 {
		t.Errorf("expected 3 attempts (fail 2, succeed 1), got %d", runner.callCount)
	}
}

func TestInjectSlackEnv_MaxRetryThenFail(t *testing.T) {
	store := &fakeSSMParamStore{params: map[string]string{"/km/slack/bridge-url": "https://bridge.example.com"}}
	// failTimes=0 with err != nil: always fails
	runner := &fakeSSMRunner{err: errInvalidInstance, failTimes: 0}
	out := &fakeOutputter{
		outputs: map[string]any{
			"ec2spot_instances": map[string]any{
				"i-abc": map[string]any{"instance_id": "i-abc123"},
			},
		},
	}
	got := captureStderr(t, func() {
		runStep11dInject(context.Background(), store, runner, "/tmp/sandbox", out.run, extractOutputInstanceID, "sb-test", "CTEST", 6, time.Microsecond)
	})
	if runner.callCount != 6 {
		t.Errorf("expected exactly 6 attempts, got %d", runner.callCount)
	}
	if !strings.Contains(got, "⚠ Slack: SSM SendCommand failed") {
		t.Errorf("stderr %q should contain SSM failure message after 6 attempts", got)
	}
}

func TestInjectSlackEnv_RetryRespectsBound(t *testing.T) {
	store := &fakeSSMParamStore{params: map[string]string{"/km/slack/bridge-url": "https://bridge.example.com"}}
	runner := &fakeSSMRunner{err: errInvalidInstance, failTimes: 0}
	out := &fakeOutputter{
		outputs: map[string]any{
			"ec2spot_instances": map[string]any{
				"i-abc": map[string]any{"instance_id": "i-abc123"},
			},
		},
	}
	start := time.Now()
	captureStderr(t, func() {
		// Use time.Microsecond so wall-clock stays well under 1s while still testing
		// that retryDelay is honored (delay > 0 means at least some pause occurs).
		runStep11dInject(context.Background(), store, runner, "/tmp/sandbox", out.run, extractOutputInstanceID, "sb-test", "CTEST", 6, time.Microsecond)
	})
	elapsed := time.Since(start)
	// With time.Microsecond delay, 6 attempts should complete in far less than 1s.
	if elapsed > 5*time.Second {
		t.Errorf("retry loop with time.Microsecond delay took too long: %v (retryDelay override not honored)", elapsed)
	}
	if runner.callCount != 6 {
		t.Errorf("expected 6 attempts, got %d", runner.callCount)
	}
}
