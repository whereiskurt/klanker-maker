package slack_test

// invite_test.go — Phase 72 Wave 0 stubs for EnsureMemberByEmail orchestrator.
// Plan 72-04 (Wave 2) flips the t.Skip calls to real assertions and implements
// EnsureMemberByEmail, EnsureMemberOpts, EnsureMemberResult, and Prompter.
//
// Symbols to be created in pkg/slack/invite.go (Wave 2):
//   - EnsureMemberByEmail(ctx, api InviteAPI, channelID, email string, opts EnsureMemberOpts) (EnsureMemberResult, error)
//   - EnsureMemberOpts { ForceExternal bool; Interactive bool; AutoConnect bool; Prompter Prompter }
//   - EnsureMemberResult enum: InvitedDirect, InvitedConnect, AlreadyMember, SkippedExternal, Failed
//   - InviteAPI interface (wraps LookupUserByEmail + InviteUserToChannel + InviteShared)
//   - Prompter interface (AskConnectFallback(ctx, email) (bool, error))

import (
	"testing"
)

// fakeInviteAPI placeholder — Wave 2 replaces with a real interface implementation.
// type fakeInviteAPI struct{}

// TestEnsureMemberByEmail_Direct — lookup hit, regular invite OK.
// Expected result: InvitedDirect.
func TestEnsureMemberByEmail_Direct(t *testing.T) {
	t.Skip("TODO Wave 2: implement EnsureMemberByEmail")
}

// TestEnsureMemberByEmail_AlreadyMember — lookup hit, invite returns already_in_channel.
// Expected result: AlreadyMember (orchestrator distinguishes from InvitedDirect).
func TestEnsureMemberByEmail_AlreadyMember(t *testing.T) {
	t.Skip("TODO Wave 2: implement EnsureMemberByEmail")
}

// TestEnsureMemberByEmail_InvitedConnect — lookup miss, interactive=true, prompter approves.
// Expected result: InvitedConnect.
func TestEnsureMemberByEmail_InvitedConnect(t *testing.T) {
	t.Skip("TODO Wave 2: implement EnsureMemberByEmail")
}

// TestEnsureMemberByEmail_SkippedNonInteractive — lookup miss, Interactive=false, AutoConnect=false.
// Expected result: SkippedExternal (no prompt, no Connect call).
func TestEnsureMemberByEmail_SkippedNonInteractive(t *testing.T) {
	t.Skip("TODO Wave 2: implement EnsureMemberByEmail")
}

// TestEnsureMemberByEmail_AutoConnectNonInteractive — lookup miss, Interactive=false, AutoConnect=true.
// Expected result: InvitedConnect (no prompt — km create path when useSlackConnect=true).
func TestEnsureMemberByEmail_AutoConnectNonInteractive(t *testing.T) {
	t.Skip("TODO Wave 2: implement EnsureMemberByEmail")
}

// TestEnsureMemberByEmail_SkippedInteractiveNo — lookup miss, Interactive=true, prompter declines.
// Expected result: SkippedExternal.
func TestEnsureMemberByEmail_SkippedInteractiveNo(t *testing.T) {
	t.Skip("TODO Wave 2: implement EnsureMemberByEmail")
}

// TestEnsureMemberByEmail_ForceExternal — ForceExternal=true skips lookup, calls InviteShared directly.
// Expected result: InvitedConnect.
func TestEnsureMemberByEmail_ForceExternal(t *testing.T) {
	t.Skip("TODO Wave 2: implement EnsureMemberByEmail")
}

// TestEnsureMemberByEmail_FreeTierConnect — Connect returns not_allowed_token_type (free tier).
// Expected result: Failed with error wrapping not_allowed_token_type.
func TestEnsureMemberByEmail_FreeTierConnect(t *testing.T) {
	t.Skip("TODO Wave 2: implement EnsureMemberByEmail")
}

// TestEnsureMemberByEmail_DryRun — read-only classify: hit→InvitedDirect, miss→SkippedExternal,
// ForceExternal→InvitedConnect. Asserts NO invite/Connect/prompter calls.
func TestEnsureMemberByEmail_DryRun(t *testing.T) {
	t.Skip("TODO Wave 2: implement EnsureMemberByEmail")
}
