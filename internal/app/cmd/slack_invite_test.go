package cmd_test

// slack_invite_test.go — Phase 72 Wave 0 stubs for km slack invite cobra command.
// Plan 72-05 (Wave 3) flips the t.Skip calls to real assertions and implements
// cmd.RunSlackInvite(ctx, deps, opts cmd.SlackInviteOpts).
//
// When Wave 3 lands:
//   - Add slack_invite.go with RunSlackInvite + SlackInviteOpts
//   - Replace each t.Skip with mock-orchestrator assertions

import (
	"testing"
)

// TestSlackInvite_HappyPath — orchestrator returns InvitedDirect, exit code 0.
// Wave 3 assertion: RunSlackInvite returns nil.
func TestSlackInvite_HappyPath(t *testing.T) {
	t.Skip("TODO Wave 3: implement km slack invite in 72-05")
}

// TestSlackInvite_ExternalFlag — --external skips lookup; orchestrator called with ForceExternal=true.
// Wave 3 assertion: opts.ForceExternal propagated to orchestrator.
func TestSlackInvite_ExternalFlag(t *testing.T) {
	t.Skip("TODO Wave 3: implement km slack invite in 72-05")
}

// TestSlackInvite_ChannelByName — --channel km-notifications resolves via FindChannelByName.
// Wave 3 assertion: channel name passed to FindChannelByName before orchestrator call.
func TestSlackInvite_ChannelByName(t *testing.T) {
	t.Skip("TODO Wave 3: implement km slack invite in 72-05")
}

// TestSlackInvite_DefaultChannelFromSSM — no --channel flag uses {prefix}slack/shared-channel-id from SSM.
// Wave 3 assertion: SSM key {prefix}/slack/shared-channel-id is read and used as channelID.
func TestSlackInvite_DefaultChannelFromSSM(t *testing.T) {
	t.Skip("TODO Wave 3: implement km slack invite in 72-05")
}
