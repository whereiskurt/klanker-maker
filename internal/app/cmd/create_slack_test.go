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

// ─────────────────────────────────────────────────────────────────────────
// runStep11dInject tests — Phase 67 SSM Parameter Store path
//
// Replaces the prior SendCommand tests after the SCP-bypass refactor. The new
// step writes /sandbox/{id}/slack-channel-id and reads /km/slack/bridge-url
// from SSM Parameter Store; the sandbox bootstrap (pkg/compiler/userdata.go)
// is responsible for picking these up at boot.
// ─────────────────────────────────────────────────────────────────────────

type fakePutParam struct {
	calls []struct{ Name, Value string }
	err   error
}

func (f *fakePutParam) put(_ context.Context, name, value string) error {
	f.calls = append(f.calls, struct{ Name, Value string }{name, value})
	return f.err
}

func TestStep11d_NoBridgeURL_SkipsPut(t *testing.T) {
	store := &fakeSSMParamStore{params: map[string]string{}}
	put := &fakePutParam{}
	got := captureStderr(t, func() {
		runStep11dInject(context.Background(), store, put.put, "sb-test", "CTEST", 1, time.Microsecond)
	})
	if !strings.Contains(got, "⚠ Slack: /km/slack/bridge-url not configured") {
		t.Errorf("stderr %q should contain bridge-url not-configured message", got)
	}
	if len(put.calls) != 0 {
		t.Errorf("expected no PutParameter calls when bridge URL is missing, got %d", len(put.calls))
	}
}

func TestStep11d_PutFailure_LogsWarn(t *testing.T) {
	store := &fakeSSMParamStore{params: map[string]string{"/km/slack/bridge-url": "https://bridge.example.com"}}
	put := &fakePutParam{err: errors.New("ssm putparam failed")}
	got := captureStderr(t, func() {
		runStep11dInject(context.Background(), store, put.put, "sb-test", "CTEST", 1, time.Microsecond)
	})
	if !strings.Contains(got, "⚠ Slack: SSM PutParameter failed") {
		t.Errorf("stderr %q should contain SSM PutParameter failed warn", got)
	}
	if len(put.calls) != 1 {
		t.Errorf("expected exactly 1 PutParameter attempt, got %d", len(put.calls))
	}
}

func TestStep11d_Success_WritesChannelIDParam(t *testing.T) {
	store := &fakeSSMParamStore{params: map[string]string{"/km/slack/bridge-url": "https://bridge.example.com"}}
	put := &fakePutParam{}
	got := captureStderr(t, func() {
		runStep11dInject(context.Background(), store, put.put, "sb-test", "C-TEST", 1, time.Microsecond)
	})
	if !strings.Contains(got, "✓ Slack: channel C-TEST published to SSM Parameter Store") {
		t.Errorf("stderr %q should announce successful publish", got)
	}
	if len(put.calls) != 1 {
		t.Fatalf("expected 1 PutParameter call, got %d", len(put.calls))
	}
	wantName := "/sandbox/sb-test/slack-channel-id"
	if put.calls[0].Name != wantName {
		t.Errorf("PutParameter Name: got %q, want %q", put.calls[0].Name, wantName)
	}
	if put.calls[0].Value != "C-TEST" {
		t.Errorf("PutParameter Value: got %q, want %q", put.calls[0].Value, "C-TEST")
	}
}
