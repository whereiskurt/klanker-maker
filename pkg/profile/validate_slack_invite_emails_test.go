package profile_test

// validate_slack_invite_emails_test.go — Phase 72 Wave 0 stubs for
// notifySlackInviteEmails and useSlackConnect profile field validation.
// Plan 72-02 (Wave 1) flips the t.Skip calls to real assertions and adds
// CLISpec.NotifySlackInviteEmails []string and CLISpec.UseSlackConnect *bool
// to pkg/profile/types.go.
//
// NOTE: No references to CLISpec.NotifySlackInviteEmails or CLISpec.UseSlackConnect
// appear in this file — those fields don't exist until Wave 1 lands. Named test funcs
// serve as the contract: Wave 1 turns each t.Skip into real assertions.

import (
	"testing"
)

// TestParse_NotifySlackInviteEmails — YAML with spec.cli.notifySlackInviteEmails: [a@b.com, c@d.com]
// round-trips through Parse; the slice is intact.
// Wave 1 assertion: parsed CLISpec.NotifySlackInviteEmails == []string{"a@b.com", "c@d.com"}.
func TestParse_NotifySlackInviteEmails(t *testing.T) {
	t.Skip("TODO Wave 1: requires CLISpec.NotifySlackInviteEmails added in 72-02")
}

// TestParse_UseSlackConnect — useSlackConnect: false round-trips; omitted ⇒ nil pointer.
// Wave 1 assertion: *CLISpec.UseSlackConnect == false when set; nil when omitted.
func TestParse_UseSlackConnect(t *testing.T) {
	t.Skip("TODO Wave 1: requires CLISpec.UseSlackConnect *bool added in 72-02")
}

// TestValidate_InviteEmails_RequiresSlackEnabled — non-empty notifySlackInviteEmails
// with notifySlackEnabled: false produces a validation error (Rule SE1).
// Wave 1 assertion: Validate returns error containing "notifySlackInviteEmails".
func TestValidate_InviteEmails_RequiresSlackEnabled(t *testing.T) {
	t.Skip("TODO Wave 1: requires CLISpec.NotifySlackInviteEmails added in 72-02")
}

// TestValidate_InviteEmails_InvalidEmail — non-RFC-5322 entry in notifySlackInviteEmails
// produces a validation error.
// Wave 1 assertion: Validate returns error for the malformed entry.
func TestValidate_InviteEmails_InvalidEmail(t *testing.T) {
	t.Skip("TODO Wave 1: requires CLISpec.NotifySlackInviteEmails added in 72-02")
}

// TestSchema_InviteEmails — sandbox_profile.schema.json accepts both
// notifySlackInviteEmails (type array, items string) and useSlackConnect (type boolean).
// Wave 1 assertion: schema validation passes for a profile with both fields set.
func TestSchema_InviteEmails(t *testing.T) {
	t.Skip("TODO Wave 1: requires schema additions in 72-02")
}
