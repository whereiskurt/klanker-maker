package cmd

// create_slack_invite_test.go — Phase 72 Wave 0 stubs for km create operator-invite
// refactor and additional-folks loop.
// Plan 72-07 (Wave 3) flips the t.Skip calls to real assertions.
//
// When Wave 3 lands:
//   - Extend create_slack.go to call EnsureMemberByEmail for the operator (AutoConnect=true)
//     and for each notifySlackInviteEmails entry
//   - Replace each t.Skip with mock-orchestrator assertions

import (
	"testing"
)

// TestCreateSlack_OperatorInvite_NativeMember — SSM invite-email is a workspace member.
// Operator invited via regular invite (AutoConnect=true), no warning.
// Wave 3 assertion: orchestrator called with AutoConnect=true; result InvitedDirect; no stderr warn.
func TestCreateSlack_OperatorInvite_NativeMember(t *testing.T) {
	t.Skip("TODO Wave 3: implement km create operator-invite refactor + additional-folks loop in 72-07")
}

// TestCreateSlack_OperatorInvite_ExternalConnect — SSM invite-email is external.
// Operator auto-Connected (AutoConnect=true unconditional), no warning.
// Wave 3 assertion: orchestrator called with AutoConnect=true; result InvitedConnect; no stderr warn.
func TestCreateSlack_OperatorInvite_ExternalConnect(t *testing.T) {
	t.Skip("TODO Wave 3: implement km create operator-invite refactor + additional-folks loop in 72-07")
}

// TestCreateSlack_OperatorInvite_MissingEmail — SSM invite-email empty.
// Existing warn-and-skip path; no operator invite call.
// Wave 3 assertion: orchestrator NOT called; stderr contains warning.
func TestCreateSlack_OperatorInvite_MissingEmail(t *testing.T) {
	t.Skip("TODO Wave 3: implement km create operator-invite refactor + additional-folks loop in 72-07")
}

// TestCreateSlack_InvitesEmails — notifySlackInviteEmails: [alice@example.com] (member).
// Orchestrator returns InvitedDirect, no warning.
// Wave 3 assertion: orchestrator called once for alice@example.com; no stderr warn.
func TestCreateSlack_InvitesEmails(t *testing.T) {
	t.Skip("TODO Wave 3: implement km create operator-invite refactor + additional-folks loop in 72-07")
}

// TestCreateSlack_AutoConnectsExternalWhenEnabled — useSlackConnect unset/true + external addr.
// Orchestrator auto-Connects (InvitedConnect), no warning.
// Wave 3 assertion: AutoConnect=true; result InvitedConnect; no stderr warn.
func TestCreateSlack_AutoConnectsExternalWhenEnabled(t *testing.T) {
	t.Skip("TODO Wave 3: implement km create operator-invite refactor + additional-folks loop in 72-07")
}

// TestCreateSlack_SkipsExternalWhenConnectDisabled — useSlackConnect: false + external addr.
// SkippedExternal; stderr contains "km slack invite --external ... --channel sb-{id}" hint.
// Create still returns nil.
// Wave 3 assertion: result SkippedExternal; stderr contains hint; err==nil.
func TestCreateSlack_SkipsExternalWhenConnectDisabled(t *testing.T) {
	t.Skip("TODO Wave 3: implement km create operator-invite refactor + additional-folks loop in 72-07")
}

// TestCreateSlack_WarnsOnInviteFailure — orchestrator returns Failed.
// Stderr contains warning; create still returns nil (fail-soft).
// Wave 3 assertion: result Failed; err==nil (fail-soft); stderr contains warning.
func TestCreateSlack_WarnsOnInviteFailure(t *testing.T) {
	t.Skip("TODO Wave 3: implement km create operator-invite refactor + additional-folks loop in 72-07")
}

// TestCreateSlack_EmptyInviteList — empty notifySlackInviteEmails.
// Additional-folks loop makes no calls (operator invite still happens).
// Wave 3 assertion: additional-folks orchestrator called 0 times; operator call happens once.
func TestCreateSlack_EmptyInviteList(t *testing.T) {
	t.Skip("TODO Wave 3: implement km create operator-invite refactor + additional-folks loop in 72-07")
}
