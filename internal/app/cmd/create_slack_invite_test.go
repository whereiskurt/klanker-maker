package cmd

// create_slack_invite_test.go — Phase 72 Plan 07: Layer 7 tests for
// operator-invite refactor and additional-folks loop in resolveSlackChannel.
//
// These tests exercise the two changes added in create_slack.go Mode 2:
//   1. Operator invite routed through EnsureMemberByEmail (AutoConnect=true unconditional)
//   2. Additional-folks loop driven by notifySlackInviteEmails + useSlackConnect

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// ─────────────────────────────────────────────────────────────────────────────
// Extended fake: fakeSlackAPIForInvite
//
// Extends SlackAPI with LookupUserByEmail + InviteUserToChannelStrict.
// fakeSlackAPI (create_slack_test.go) does NOT have these methods — we
// introduce a distinct type here to keep backward compatibility.
// ─────────────────────────────────────────────────────────────────────────────

type fakeSlackAPIForInvite struct {
	// Channel lifecycle (required by SlackAPI for Mode 2 setup)
	createChannelResult string
	createChannelErr    error
	joinChannelErr      error
	channelInfoMember   bool
	channelInfoCount    int
	channelInfoErr      error

	// Email→userID mapping: absent key ⇒ miss (found=false)
	lookupByEmail map[string]string

	// Per-userID forced error for InviteUserToChannelStrict (nil ⇒ success)
	inviteStrictErrFor map[string]error

	// Recorded calls
	lookupByEmailCalls []string
	inviteStrictCalls  [][2]string // (channelID, userID)
	inviteSharedCalls  [][2]string // (channelID, email)
}

func newFakeSlackAPIForInvite() *fakeSlackAPIForInvite {
	return &fakeSlackAPIForInvite{
		createChannelResult: "CNEW",
		lookupByEmail:       map[string]string{},
	}
}

func (f *fakeSlackAPIForInvite) CreateChannel(_ context.Context, _ string) (string, error) {
	return f.createChannelResult, f.createChannelErr
}

func (f *fakeSlackAPIForInvite) FindChannelByName(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}

func (f *fakeSlackAPIForInvite) JoinChannel(_ context.Context, _ string) error {
	return f.joinChannelErr
}

func (f *fakeSlackAPIForInvite) ChannelInfo(_ context.Context, _ string) (int, bool, error) {
	return f.channelInfoCount, f.channelInfoMember, f.channelInfoErr
}

func (f *fakeSlackAPIForInvite) InviteShared(_ context.Context, channelID, email string) error {
	f.inviteSharedCalls = append(f.inviteSharedCalls, [2]string{channelID, email})
	return nil
}

func (f *fakeSlackAPIForInvite) LookupUserByEmail(_ context.Context, email string) (string, bool, error) {
	f.lookupByEmailCalls = append(f.lookupByEmailCalls, email)
	if uid, ok := f.lookupByEmail[email]; ok {
		return uid, true, nil
	}
	return "", false, nil
}

func (f *fakeSlackAPIForInvite) InviteUserToChannelStrict(_ context.Context, channelID, userID string) error {
	f.inviteStrictCalls = append(f.inviteStrictCalls, [2]string{channelID, userID})
	if f.inviteStrictErrFor != nil {
		if err, ok := f.inviteStrictErrFor[userID]; ok {
			return err
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// fakeSSM — lightweight SSMParamStore backed by a plain map.
// Used instead of fakeSSMParamStore to avoid ambiguity; both satisfy the
// SSMParamStore interface.
// ─────────────────────────────────────────────────────────────────────────────

type fakeSSM struct {
	vals map[string]string
}

func (f fakeSSM) Get(_ context.Context, name string, _ bool) (string, error) {
	return f.vals[name], nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// ptrBool returns a *bool. ptrBool is the plan-specified name; boolPtrCreate
// already exists in create_slack_test.go — keep both to avoid conflict.
func ptrBool(b bool) *bool { return &b }

// perSandboxProfile builds a SandboxProfile with notifySlackEnabled=true,
// notifySlackPerSandbox=true, the given invite list, and the given
// UseSlackConnect pointer (nil ⇒ unset / default-true).
func perSandboxProfile(inviteEmails []string, useSlackConnect *bool) *profile.SandboxProfile {
	p := &profile.SandboxProfile{}
	enabled := true
	p.Spec.Notification = &profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    &enabled,
			PerSandbox: ptrBool(true),
			Invites: &profile.NotificationSlackInvitesSpec{
				Emails:     inviteEmails,
				UseConnect: useSlackConnect,
			},
		},
	}
	return p
}

// containsStrict returns true when inviteStrictCalls contains a call for userID.
func containsStrict(calls [][2]string, userID string) bool {
	for _, c := range calls {
		if c[1] == userID {
			return true
		}
	}
	return false
}

// containsShared returns true when inviteSharedCalls contains a call for email.
func containsShared(calls [][2]string, email string) bool {
	for _, c := range calls {
		if c[1] == email {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// OPERATOR INVITE tests (refactor of the raw InviteShared call)
// ─────────────────────────────────────────────────────────────────────────────

// TestCreateSlack_OperatorInvite_NativeMember — SSM invite-email is a workspace
// member. Operator invited via conversations.invite (AutoConnect=true on a hit).
// No warning.
func TestCreateSlack_OperatorInvite_NativeMember(t *testing.T) {
	api := newFakeSlackAPIForInvite()
	api.lookupByEmail = map[string]string{"operator@corp.example": "U-OP"}
	ssm := fakeSSM{vals: map[string]string{"km/slack/invite-email": "operator@corp.example"}}

	var stderrOut string
	p := perSandboxProfile(nil, nil)
	stderrOut = captureStderr(t, func() {
		_, _, err := resolveSlackChannel(context.Background(), p, "test123", "", api, nil, ssm, "km/")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
	})

	if !containsStrict(api.inviteStrictCalls, "U-OP") {
		t.Errorf("expected operator invited via conversations.invite (U-OP); got strict=%v", api.inviteStrictCalls)
	}
	if strings.Contains(stderrOut, "[warn]") {
		t.Errorf("expected no warning for native operator; got:\n%s", stderrOut)
	}
}

// TestCreateSlack_OperatorInvite_ExternalConnect — SSM invite-email is external
// (lookup miss). AutoConnect=true (unconditional for operator) → Connect invite.
// No warning.
func TestCreateSlack_OperatorInvite_ExternalConnect(t *testing.T) {
	api := newFakeSlackAPIForInvite()
	api.lookupByEmail = map[string]string{} // miss → external
	ssm := fakeSSM{vals: map[string]string{"km/slack/invite-email": "operator@external.example"}}

	var stderrOut string
	p := perSandboxProfile(nil, nil)
	stderrOut = captureStderr(t, func() {
		_, _, err := resolveSlackChannel(context.Background(), p, "test123", "", api, nil, ssm, "km/")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
	})

	if !containsShared(api.inviteSharedCalls, "operator@external.example") {
		t.Errorf("expected Connect invite for external operator; got shared=%v", api.inviteSharedCalls)
	}
	if strings.Contains(stderrOut, "[warn]") {
		t.Errorf("expected no warning for external operator (auto-connect); got:\n%s", stderrOut)
	}
}

// TestCreateSlack_OperatorInvite_MissingEmail — SSM invite-email empty.
// Existing warn-and-skip path unchanged; orchestrator NOT called for operator.
// km create returns nil.
func TestCreateSlack_OperatorInvite_MissingEmail(t *testing.T) {
	api := newFakeSlackAPIForInvite()
	ssm := fakeSSM{vals: map[string]string{}} // invite-email absent

	p := perSandboxProfile(nil, nil)
	_, _, err := resolveSlackChannel(context.Background(), p, "test123", "", api, nil, ssm, "km/")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(api.inviteStrictCalls) != 0 || len(api.inviteSharedCalls) != 0 {
		t.Errorf("expected no operator invite when invite-email empty; strict=%v shared=%v",
			api.inviteStrictCalls, api.inviteSharedCalls)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ADDITIONAL-FOLKS LOOP tests
// ─────────────────────────────────────────────────────────────────────────────

// TestCreateSlack_InvitesEmails — notifySlackInviteEmails: [alice@example.com].
// alice is a workspace member → InvitedDirect. No warnings.
func TestCreateSlack_InvitesEmails(t *testing.T) {
	api := newFakeSlackAPIForInvite()
	api.lookupByEmail = map[string]string{
		"operator@corp.example": "U-OP",
		"alice@example.com":     "U-ALICE",
	}
	ssm := fakeSSM{vals: map[string]string{"km/slack/invite-email": "operator@corp.example"}}

	var stderrOut string
	p := perSandboxProfile([]string{"alice@example.com"}, nil)
	stderrOut = captureStderr(t, func() {
		_, _, err := resolveSlackChannel(context.Background(), p, "test123", "", api, nil, ssm, "km/")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
	})

	if !containsStrict(api.inviteStrictCalls, "U-ALICE") {
		t.Errorf("expected alice invited via conversations.invite; got strict=%v", api.inviteStrictCalls)
	}
	if strings.Contains(stderrOut, "[warn]") {
		t.Errorf("expected no warnings; got:\n%s", stderrOut)
	}
}

// TestCreateSlack_AutoConnectsExternalWhenEnabled — useSlackConnect unset (nil)
// → AutoConnect=true for additional folks. External address auto-Connected.
// No warning.
func TestCreateSlack_AutoConnectsExternalWhenEnabled(t *testing.T) {
	api := newFakeSlackAPIForInvite()
	// operator found, bob is external (miss)
	api.lookupByEmail = map[string]string{"operator@corp.example": "U-OP"}
	ssm := fakeSSM{vals: map[string]string{"km/slack/invite-email": "operator@corp.example"}}

	var stderrOut string
	// useSlackConnect unset (nil) ⇒ true ⇒ AutoConnect.
	p := perSandboxProfile([]string{"bob@external.com"}, nil)
	stderrOut = captureStderr(t, func() {
		_, _, err := resolveSlackChannel(context.Background(), p, "test123", "", api, nil, ssm, "km/")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
	})

	if !containsShared(api.inviteSharedCalls, "bob@external.com") {
		t.Errorf("expected auto-Connect for bob; got shared=%v", api.inviteSharedCalls)
	}
	if strings.Contains(stderrOut, "[warn]") {
		t.Errorf("expected NO warning when useSlackConnect true; got:\n%s", stderrOut)
	}
}

// TestCreateSlack_SkipsExternalWhenConnectDisabled — useSlackConnect: false.
// External address → SkippedExternal. Stderr contains the follow-up hint.
// km create returns nil (fail-soft).
func TestCreateSlack_SkipsExternalWhenConnectDisabled(t *testing.T) {
	api := newFakeSlackAPIForInvite()
	api.lookupByEmail = map[string]string{"operator@corp.example": "U-OP"}
	ssm := fakeSSM{vals: map[string]string{"km/slack/invite-email": "operator@corp.example"}}

	var stderrOut string
	// useSlackConnect: false ⇒ AutoConnect=false ⇒ SkippedExternal.
	p := perSandboxProfile([]string{"bob@external.com"}, ptrBool(false))
	stderrOut = captureStderr(t, func() {
		_, _, err := resolveSlackChannel(context.Background(), p, "test123", "", api, nil, ssm, "km/")
		if err != nil {
			t.Fatalf("err = %v (should NOT fail create on SkippedExternal)", err)
		}
	})

	if containsShared(api.inviteSharedCalls, "bob@external.com") {
		t.Errorf("expected NO Connect invite for bob when useSlackConnect false; got %v", api.inviteSharedCalls)
	}
	if !strings.Contains(stderrOut, "bob@external.com") ||
		!strings.Contains(stderrOut, "km slack invite --external") ||
		!strings.Contains(stderrOut, "sb-test123") {
		t.Errorf("expected skip warning with follow-up hint and channel; got:\n%s", stderrOut)
	}
}

// TestCreateSlack_WarnsOnInviteFailure — InviteUserToChannelStrict returns an
// error for guest@example.com. Stderr contains a non-fatal warning.
// km create returns nil (fail-soft).
func TestCreateSlack_WarnsOnInviteFailure(t *testing.T) {
	api := newFakeSlackAPIForInvite()
	api.lookupByEmail = map[string]string{
		"operator@corp.example": "U-OP",
		"guest@example.com":     "U-GUEST",
	}
	api.inviteStrictErrFor = map[string]error{
		"U-GUEST": &slack.SlackAPIError{Method: "conversations.invite", Code: "user_is_restricted"},
	}
	ssm := fakeSSM{vals: map[string]string{"km/slack/invite-email": "operator@corp.example"}}

	var stderrOut string
	p := perSandboxProfile([]string{"guest@example.com"}, nil)
	stderrOut = captureStderr(t, func() {
		_, _, err := resolveSlackChannel(context.Background(), p, "test123", "", api, nil, ssm, "km/")
		if err != nil {
			t.Fatalf("err = %v (Failed must be fail-soft)", err)
		}
	})

	if !strings.Contains(stderrOut, "Slack invite failed") || !strings.Contains(stderrOut, "non-fatal") {
		t.Errorf("expected non-fatal failure warning; got:\n%s", stderrOut)
	}
}

// TestCreateSlack_EmptyInviteList — empty notifySlackInviteEmails.
// Additional-folks loop makes NO orchestrator calls; operator invite still
// runs once (1 lookup).
func TestCreateSlack_EmptyInviteList(t *testing.T) {
	api := newFakeSlackAPIForInvite()
	api.lookupByEmail = map[string]string{"operator@corp.example": "U-OP"}
	ssm := fakeSSM{vals: map[string]string{"km/slack/invite-email": "operator@corp.example"}}

	p := perSandboxProfile(nil, nil) // no additional folks
	_, _, err := resolveSlackChannel(context.Background(), p, "test123", "", api, nil, ssm, "km/")
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	// Operator was invited (1 lookup), but the additional-folks loop made none.
	if len(api.lookupByEmailCalls) != 1 {
		t.Errorf("expected exactly 1 lookup (operator only); got %d: %v",
			len(api.lookupByEmailCalls), api.lookupByEmailCalls)
	}
}

// ensure fakeSlackAPIForInvite satisfies SlackAPI at compile time.
// This will fail to compile until we extend SlackAPI with the new methods.
var _ SlackAPI = (*fakeSlackAPIForInvite)(nil)

// Ensure ErrAlreadyInChannel is importable here (suppress unused import if
// EnsureMemberByEmail moves the sentinel to invite.go — already exported).
var _ = errors.New
var _ = slack.ErrAlreadyInChannel
