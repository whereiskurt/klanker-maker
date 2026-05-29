package slack_test

// invite_test.go — Phase 72 Layer 2 tests for EnsureMemberByEmail orchestrator.
// Plan 72-04 (Wave 2) implementation.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// ────────────────────────────────────────────────────────────────────────────
// Test fakes
// ────────────────────────────────────────────────────────────────────────────

type fakeInviteAPI struct {
	lookupErr       error
	lookupID        string
	lookupFound     bool
	inviteStrictErr error
	inviteSharedErr error
	// recorded calls
	lookupCalls       []string
	inviteStrictCalls [][2]string // channelID, userID
	inviteSharedCalls [][2]string // channelID, email
}

func (f *fakeInviteAPI) LookupUserByEmail(ctx context.Context, email string) (string, bool, error) {
	f.lookupCalls = append(f.lookupCalls, email)
	return f.lookupID, f.lookupFound, f.lookupErr
}

func (f *fakeInviteAPI) InviteUserToChannelStrict(ctx context.Context, channelID, userID string) error {
	f.inviteStrictCalls = append(f.inviteStrictCalls, [2]string{channelID, userID})
	return f.inviteStrictErr
}

func (f *fakeInviteAPI) InviteShared(ctx context.Context, channelID, email string) error {
	f.inviteSharedCalls = append(f.inviteSharedCalls, [2]string{channelID, email})
	return f.inviteSharedErr
}

type fakePrompter struct {
	confirmReturn bool
	confirmErr    error
	calls         []string
}

func (f *fakePrompter) ConfirmConnect(email string) (bool, error) {
	f.calls = append(f.calls, email)
	return f.confirmReturn, f.confirmErr
}

// ────────────────────────────────────────────────────────────────────────────
// EnsureMemberByEmail tests (10 truths from 72-VALIDATION Layer 2)
// ────────────────────────────────────────────────────────────────────────────

// TestEnsureMemberByEmail_Direct — lookup hit, regular invite OK.
// Expected result: InvitedDirect.
func TestEnsureMemberByEmail_Direct(t *testing.T) {
	api := &fakeInviteAPI{lookupID: "U123", lookupFound: true}
	res, err := slack.EnsureMemberByEmail(context.Background(), api, "C1", "alice@x.com", slack.EnsureMemberOpts{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res != slack.InvitedDirect {
		t.Errorf("res = %v; want InvitedDirect", res)
	}
	if len(api.inviteStrictCalls) != 1 {
		t.Errorf("expected 1 invite call; got %d", len(api.inviteStrictCalls))
	}
}

// TestEnsureMemberByEmail_AlreadyMember — lookup hit, invite returns already_in_channel.
// Expected result: AlreadyMember (orchestrator distinguishes from InvitedDirect).
func TestEnsureMemberByEmail_AlreadyMember(t *testing.T) {
	api := &fakeInviteAPI{lookupID: "U123", lookupFound: true, inviteStrictErr: slack.ErrAlreadyInChannel}
	res, err := slack.EnsureMemberByEmail(context.Background(), api, "C1", "alice@x.com", slack.EnsureMemberOpts{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res != slack.AlreadyMember {
		t.Errorf("res = %v; want AlreadyMember", res)
	}
}

// TestEnsureMemberByEmail_InvitedConnect — lookup miss, Interactive=true, prompter approves.
// Expected result: InvitedConnect.
func TestEnsureMemberByEmail_InvitedConnect(t *testing.T) {
	api := &fakeInviteAPI{lookupFound: false}
	p := &fakePrompter{confirmReturn: true}
	res, err := slack.EnsureMemberByEmail(context.Background(), api, "C1", "ext@x.com", slack.EnsureMemberOpts{Interactive: true, Prompter: p})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res != slack.InvitedConnect {
		t.Errorf("res = %v; want InvitedConnect", res)
	}
	if len(p.calls) != 1 {
		t.Errorf("expected 1 prompt call; got %d", len(p.calls))
	}
	if len(api.inviteSharedCalls) != 1 {
		t.Errorf("expected 1 inviteShared call; got %d", len(api.inviteSharedCalls))
	}
}

// TestEnsureMemberByEmail_SkippedNonInteractive — lookup miss, Interactive=false, AutoConnect=false.
// Expected result: SkippedExternal (no prompt, no Connect call).
func TestEnsureMemberByEmail_SkippedNonInteractive(t *testing.T) {
	api := &fakeInviteAPI{lookupFound: false}
	// Interactive=false, AutoConnect=false (zero value) → skip, no Connect.
	res, err := slack.EnsureMemberByEmail(context.Background(), api, "C1", "ext@x.com", slack.EnsureMemberOpts{Interactive: false})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res != slack.SkippedExternal {
		t.Errorf("res = %v; want SkippedExternal", res)
	}
	if len(api.inviteSharedCalls) != 0 {
		t.Errorf("Connect should NOT have been called; got %d calls", len(api.inviteSharedCalls))
	}
}

// TestEnsureMemberByEmail_AutoConnectNonInteractive — lookup miss, Interactive=false, AutoConnect=true.
// Expected result: InvitedConnect (no prompt — km create useSlackConnect path).
func TestEnsureMemberByEmail_AutoConnectNonInteractive(t *testing.T) {
	api := &fakeInviteAPI{lookupFound: false}
	// Interactive=false, AutoConnect=true → Connect with NO prompt.
	// Prompter is nil and must never be consulted.
	res, err := slack.EnsureMemberByEmail(context.Background(), api, "C1", "ext@x.com", slack.EnsureMemberOpts{Interactive: false, AutoConnect: true})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res != slack.InvitedConnect {
		t.Errorf("res = %v; want InvitedConnect", res)
	}
	if len(api.inviteSharedCalls) != 1 {
		t.Errorf("expected 1 inviteShared call; got %d", len(api.inviteSharedCalls))
	}
}

// TestEnsureMemberByEmail_DryRun — read-only classify: hit→InvitedDirect, miss→SkippedExternal,
// ForceExternal→InvitedConnect. Asserts NO invite/Connect/prompter calls.
func TestEnsureMemberByEmail_DryRun(t *testing.T) {
	// Member → would invite natively, no write.
	hit := &fakeInviteAPI{lookupID: "U1", lookupFound: true}
	res, err := slack.EnsureMemberByEmail(context.Background(), hit, "C1", "a@x.com", slack.EnsureMemberOpts{DryRun: true})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res != slack.InvitedDirect {
		t.Errorf("hit dry-run res = %v; want InvitedDirect", res)
	}
	if len(hit.inviteStrictCalls) != 0 || len(hit.inviteSharedCalls) != 0 {
		t.Errorf("dry-run must not write; got strict=%v shared=%v", hit.inviteStrictCalls, hit.inviteSharedCalls)
	}

	// Non-member → would require Connect; no write/prompt (Prompter must not be called).
	miss := &fakeInviteAPI{lookupFound: false}
	p := &fakePrompter{confirmReturn: true}
	res, err = slack.EnsureMemberByEmail(context.Background(), miss, "C1", "ext@x.com", slack.EnsureMemberOpts{DryRun: true, Interactive: true, Prompter: p})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res != slack.SkippedExternal {
		t.Errorf("miss dry-run res = %v; want SkippedExternal", res)
	}
	if len(miss.inviteSharedCalls) != 0 || len(p.calls) != 0 {
		t.Errorf("dry-run must not Connect or prompt; got shared=%v promptCalls=%v", miss.inviteSharedCalls, p.calls)
	}

	// ForceExternal → would Connect, no write, no lookup.
	fe := &fakeInviteAPI{}
	res, _ = slack.EnsureMemberByEmail(context.Background(), fe, "C1", "ext@x.com", slack.EnsureMemberOpts{DryRun: true, ForceExternal: true})
	if res != slack.InvitedConnect {
		t.Errorf("force dry-run res = %v; want InvitedConnect", res)
	}
	if len(fe.inviteSharedCalls) != 0 || len(fe.lookupCalls) != 0 {
		t.Errorf("force dry-run must not write or look up; got shared=%v lookups=%v", fe.inviteSharedCalls, fe.lookupCalls)
	}
}

// TestEnsureMemberByEmail_SkippedInteractiveNo — lookup miss, Interactive=true, prompter declines.
// Expected result: SkippedExternal.
func TestEnsureMemberByEmail_SkippedInteractiveNo(t *testing.T) {
	api := &fakeInviteAPI{lookupFound: false}
	p := &fakePrompter{confirmReturn: false}
	res, _ := slack.EnsureMemberByEmail(context.Background(), api, "C1", "ext@x.com", slack.EnsureMemberOpts{Interactive: true, Prompter: p})
	if res != slack.SkippedExternal {
		t.Errorf("res = %v; want SkippedExternal", res)
	}
	if len(api.inviteSharedCalls) != 0 {
		t.Errorf("Connect should NOT have been called after decline; got %d calls", len(api.inviteSharedCalls))
	}
}

// TestEnsureMemberByEmail_ForceExternal — ForceExternal=true skips lookup, calls InviteShared directly.
// Expected result: InvitedConnect.
func TestEnsureMemberByEmail_ForceExternal(t *testing.T) {
	api := &fakeInviteAPI{}
	res, err := slack.EnsureMemberByEmail(context.Background(), api, "C1", "ext@x.com", slack.EnsureMemberOpts{ForceExternal: true})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res != slack.InvitedConnect {
		t.Errorf("res = %v; want InvitedConnect", res)
	}
	if len(api.lookupCalls) != 0 {
		t.Errorf("ForceExternal should skip lookup; got %d lookup calls", len(api.lookupCalls))
	}
	if len(api.inviteSharedCalls) != 1 {
		t.Errorf("expected 1 inviteShared call; got %d", len(api.inviteSharedCalls))
	}
}

// TestEnsureMemberByEmail_FreeTierConnect — Connect returns not_allowed_token_type (free-tier workspace).
// Expected result: Failed with error wrapping Pro-tier hint.
func TestEnsureMemberByEmail_FreeTierConnect(t *testing.T) {
	api := &fakeInviteAPI{
		inviteSharedErr: &slack.SlackAPIError{Method: "conversations.inviteShared", Code: "not_allowed_token_type"},
	}
	res, err := slack.EnsureMemberByEmail(context.Background(), api, "C1", "ext@x.com", slack.EnsureMemberOpts{ForceExternal: true})
	if res != slack.Failed {
		t.Errorf("res = %v; want Failed", res)
	}
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Pro") {
		t.Errorf("error should mention Pro tier; got: %v", err)
	}
}

// TestEnsureMemberByEmail_InteractiveNilPrompter — Interactive=true with nil Prompter.
// Expected result: Failed with explicit error.
func TestEnsureMemberByEmail_InteractiveNilPrompter(t *testing.T) {
	api := &fakeInviteAPI{lookupFound: false}
	res, err := slack.EnsureMemberByEmail(context.Background(), api, "C1", "ext@x.com", slack.EnsureMemberOpts{Interactive: true, Prompter: nil})
	if res != slack.Failed {
		t.Errorf("res = %v; want Failed", res)
	}
	if err == nil {
		t.Fatal("expected error")
	}
}

// Compile-time verification that errors package is used in tests.
var _ = errors.New
