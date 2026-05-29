package cmd_test

// slack_invite_test.go — Phase 72 Plan 72-05: km slack invite Layer 3 tests.
//
// Tests cover:
//   - HappyPath: workspace member, invited via conversations.invite, exit 0.
//   - ExternalFlag: --external skips lookup, goes straight to InviteShared.
//   - ChannelByName: --channel km-notifications resolves via FindChannelByName.
//   - ChannelByID: --channel C012ABCDE3F bypasses FindChannelByName.
//   - DefaultChannelFromSSM: no --channel reads SSM shared-channel-id.
//   - SkippedExternalExitCode: lookup miss, non-interactive → ExitCodeError{Code:2}.
//   - DryRun: zero writes, lookup read-only, exit 0.
//   - FailedExitCode: invite error → plain error (exit 1).

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// ─────────────────────────────────────────────
// Fake SlackAPI implementation for invite tests
// ─────────────────────────────────────────────

// fakeSlackAPIForInvite implements cmd.SlackAPI for testing RunSlackInvite.
// It records calls so assertions can verify orchestrator interactions.
type fakeSlackAPIForInvite struct {
	// Configure lookup behaviour
	lookupID    string
	lookupFound bool
	lookupErr   error
	// Configure invite behaviour
	inviteStrictErr error
	inviteSharedErr error
	// Configure channel resolution behaviour
	findChannelID string
	joinErr       error

	// Recorded calls
	lookupCalls       []string
	inviteStrictCalls [][2]string // [channelID, userID]
	inviteSharedCalls [][2]string // [channelID, email]
	joinCalls         []string
	findChannelCalls  []string
}

// Phase 72 invite methods.
func (f *fakeSlackAPIForInvite) LookupUserByEmail(_ context.Context, email string) (string, bool, error) {
	f.lookupCalls = append(f.lookupCalls, email)
	return f.lookupID, f.lookupFound, f.lookupErr
}

func (f *fakeSlackAPIForInvite) InviteUserToChannelStrict(_ context.Context, channelID, userID string) error {
	f.inviteStrictCalls = append(f.inviteStrictCalls, [2]string{channelID, userID})
	return f.inviteStrictErr
}

func (f *fakeSlackAPIForInvite) InviteShared(_ context.Context, channelID, email string) error {
	f.inviteSharedCalls = append(f.inviteSharedCalls, [2]string{channelID, email})
	return f.inviteSharedErr
}

func (f *fakeSlackAPIForInvite) FindChannelByName(_ context.Context, name string) (string, error) {
	f.findChannelCalls = append(f.findChannelCalls, name)
	return f.findChannelID, nil
}

func (f *fakeSlackAPIForInvite) JoinChannel(_ context.Context, channelID string) error {
	f.joinCalls = append(f.joinCalls, channelID)
	return f.joinErr
}

// Stub remaining SlackAPI methods — not exercised by invite tests.
func (f *fakeSlackAPIForInvite) CreateChannel(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (f *fakeSlackAPIForInvite) ChannelInfo(_ context.Context, _ string) (int, bool, error) {
	return 0, false, nil
}

// ─────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────

func TestSlackInvite_HappyPath(t *testing.T) {
	api := &fakeSlackAPIForInvite{lookupID: "U123", lookupFound: true}
	deps := &cmd.SlackCmdDeps{
		Slack:     api,
		SSM:       newFakeSSM(map[string]string{"/km/slack/shared-channel-id": "C-DEFAULT"}),
		SsmPrefix: "/km/",
	}
	err := cmd.RunSlackInvite(context.Background(), deps, &config.Config{}, cmd.SlackInviteOpts{
		Email: "alice@example.com",
	})
	if err != nil {
		t.Fatalf("err = %v; want nil (exit 0)", err)
	}
	if len(api.inviteStrictCalls) != 1 {
		t.Errorf("expected 1 InviteUserToChannelStrict call; got %d", len(api.inviteStrictCalls))
	}
	if api.inviteStrictCalls[0][0] != "C-DEFAULT" {
		t.Errorf("invite channel = %q; want C-DEFAULT (from SSM)", api.inviteStrictCalls[0][0])
	}
}

func TestSlackInvite_ExternalFlag(t *testing.T) {
	api := &fakeSlackAPIForInvite{}
	deps := &cmd.SlackCmdDeps{
		Slack:     api,
		SSM:       newFakeSSM(map[string]string{"/km/slack/shared-channel-id": "C-DEFAULT"}),
		SsmPrefix: "/km/",
	}
	err := cmd.RunSlackInvite(context.Background(), deps, &config.Config{}, cmd.SlackInviteOpts{
		Email:    "ext@example.com",
		External: true,
	})
	if err != nil {
		t.Fatalf("err = %v; want nil (exit 0)", err)
	}
	if len(api.lookupCalls) != 0 {
		t.Errorf("ForceExternal should skip LookupUserByEmail; got calls: %v", api.lookupCalls)
	}
	if len(api.inviteSharedCalls) != 1 {
		t.Errorf("expected 1 InviteShared call; got %d", len(api.inviteSharedCalls))
	}
}

func TestSlackInvite_ChannelByName(t *testing.T) {
	api := &fakeSlackAPIForInvite{lookupID: "U1", lookupFound: true, findChannelID: "C-NAMED"}
	deps := &cmd.SlackCmdDeps{
		Slack:     api,
		SSM:       newFakeSSM(nil),
		SsmPrefix: "/km/",
	}
	err := cmd.RunSlackInvite(context.Background(), deps, &config.Config{}, cmd.SlackInviteOpts{
		Email:   "alice@x.com",
		Channel: "km-notifications",
	})
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if len(api.findChannelCalls) != 1 {
		t.Errorf("expected 1 FindChannelByName call; got %d", len(api.findChannelCalls))
	}
	if api.findChannelCalls[0] != "km-notifications" {
		t.Errorf("FindChannelByName called with %q; want %q", api.findChannelCalls[0], "km-notifications")
	}
	if len(api.inviteStrictCalls) == 0 {
		t.Fatal("expected InviteUserToChannelStrict call")
	}
	if api.inviteStrictCalls[0][0] != "C-NAMED" {
		t.Errorf("invite channel = %q; want C-NAMED", api.inviteStrictCalls[0][0])
	}
}

func TestSlackInvite_ChannelByID(t *testing.T) {
	api := &fakeSlackAPIForInvite{lookupID: "U1", lookupFound: true}
	deps := &cmd.SlackCmdDeps{
		Slack:     api,
		SSM:       newFakeSSM(nil),
		SsmPrefix: "/km/",
	}
	err := cmd.RunSlackInvite(context.Background(), deps, &config.Config{}, cmd.SlackInviteOpts{
		Email:   "alice@x.com",
		Channel: "C012ABCDE3F", // ID format — should NOT trigger FindChannelByName
	})
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if len(api.findChannelCalls) != 0 {
		t.Errorf("ID format should skip FindChannelByName; got calls: %v", api.findChannelCalls)
	}
	if len(api.inviteStrictCalls) == 0 {
		t.Fatal("expected InviteUserToChannelStrict call")
	}
	if api.inviteStrictCalls[0][0] != "C012ABCDE3F" {
		t.Errorf("invite channel = %q; want C012ABCDE3F", api.inviteStrictCalls[0][0])
	}
}

func TestSlackInvite_DefaultChannelFromSSM(t *testing.T) {
	api := &fakeSlackAPIForInvite{lookupID: "U1", lookupFound: true}
	deps := &cmd.SlackCmdDeps{
		Slack:     api,
		SSM:       newFakeSSM(map[string]string{"/km/slack/shared-channel-id": "C-FROM-SSM"}),
		SsmPrefix: "/km/",
	}
	err := cmd.RunSlackInvite(context.Background(), deps, &config.Config{}, cmd.SlackInviteOpts{
		Email: "alice@x.com",
		// Channel not set — should use SSM default
	})
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if len(api.inviteStrictCalls) == 0 {
		t.Fatal("expected InviteUserToChannelStrict call")
	}
	if api.inviteStrictCalls[0][0] != "C-FROM-SSM" {
		t.Errorf("invite channel = %q; want C-FROM-SSM (from SSM)", api.inviteStrictCalls[0][0])
	}
}

func TestSlackInvite_SkippedExternalExitCode(t *testing.T) {
	// Lookup miss + non-interactive (test runs with non-TTY stdin) → SkippedExternal → ExitCodeError{Code:2}
	api := &fakeSlackAPIForInvite{lookupFound: false}
	deps := &cmd.SlackCmdDeps{
		Slack:     api,
		SSM:       newFakeSSM(map[string]string{"/km/slack/shared-channel-id": "C-DEFAULT"}),
		SsmPrefix: "/km/",
	}
	err := cmd.RunSlackInvite(context.Background(), deps, &config.Config{}, cmd.SlackInviteOpts{
		Email: "ext@example.com",
		// No External flag — lookup misses, non-interactive → SkippedExternal
	})
	if err == nil {
		t.Fatal("expected error (SkippedExternal → ExitCodeError); got nil")
	}
	var exitErr *cmd.ExitCodeError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v (%T); want *cmd.ExitCodeError", err, err)
	}
	if exitErr.Code != 2 {
		t.Errorf("exit code = %d; want 2", exitErr.Code)
	}
}

func TestSlackInvite_DryRun(t *testing.T) {
	// Lookup hit → "would invite", zero writes, exit 0.
	api := &fakeSlackAPIForInvite{lookupID: "U1", lookupFound: true}
	deps := &cmd.SlackCmdDeps{
		Slack:     api,
		SSM:       newFakeSSM(map[string]string{"/km/slack/shared-channel-id": "C-DEFAULT"}),
		SsmPrefix: "/km/",
	}
	err := cmd.RunSlackInvite(context.Background(), deps, &config.Config{}, cmd.SlackInviteOpts{
		Email:  "alice@example.com",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry-run err = %v; want nil (exit 0)", err)
	}
	if len(api.inviteStrictCalls) != 0 || len(api.inviteSharedCalls) != 0 || len(api.joinCalls) != 0 {
		t.Errorf("dry-run must perform no writes or join; strict=%v shared=%v join=%v",
			api.inviteStrictCalls, api.inviteSharedCalls, api.joinCalls)
	}
	if len(api.lookupCalls) != 1 {
		t.Errorf("dry-run should perform the read-only lookup; got %d calls", len(api.lookupCalls))
	}

	// Lookup miss → "NOT a workspace member", zero writes, exit 0.
	api2 := &fakeSlackAPIForInvite{lookupFound: false}
	deps2 := &cmd.SlackCmdDeps{
		Slack:     api2,
		SSM:       newFakeSSM(map[string]string{"/km/slack/shared-channel-id": "C-DEFAULT"}),
		SsmPrefix: "/km/",
	}
	if err2 := cmd.RunSlackInvite(context.Background(), deps2, &config.Config{}, cmd.SlackInviteOpts{
		Email:  "ext@example.com",
		DryRun: true,
	}); err2 != nil {
		t.Fatalf("dry-run miss err = %v; want nil", err2)
	}
	if len(api2.inviteSharedCalls) != 0 || len(api2.joinCalls) != 0 {
		t.Errorf("dry-run miss must not Connect or join; shared=%v join=%v", api2.inviteSharedCalls, api2.joinCalls)
	}
}

func TestSlackInvite_FailedExitCode(t *testing.T) {
	// Lookup hit but InviteUserToChannelStrict returns a Slack API error → Failed.
	api := &fakeSlackAPIForInvite{
		lookupID:        "U1",
		lookupFound:     true,
		inviteStrictErr: &slack.SlackAPIError{Method: "conversations.invite", Code: "user_is_restricted"},
	}
	deps := &cmd.SlackCmdDeps{
		Slack:     api,
		SSM:       newFakeSSM(map[string]string{"/km/slack/shared-channel-id": "C-DEFAULT"}),
		SsmPrefix: "/km/",
	}
	err := cmd.RunSlackInvite(context.Background(), deps, &config.Config{}, cmd.SlackInviteOpts{
		Email: "guest@example.com",
	})
	if err == nil {
		t.Fatal("expected error (Failed); got nil")
	}
	if !strings.Contains(err.Error(), "invite failed") {
		t.Errorf("error message should mention invite failure; got: %v", err)
	}
}
