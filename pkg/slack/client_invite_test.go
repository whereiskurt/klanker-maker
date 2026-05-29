package slack_test

// client_invite_test.go — Phase 72 Wave 0 stubs for InviteUserToChannel.
// Plan 72-01 (Wave 1) flips the t.Skip calls to real assertions and
// implements the production method on *slack.Client.
//
// When Wave 1 lands:
//   - Add: func (c *Client) InviteUserToChannel(ctx context.Context, channelID, userID string) error
//   - Replace each t.Skip with the assertions described in the comment below it.

import (
	"testing"
)

// TestClient_InviteUserToChannel_OK — server returns ok=true with channel.
// Wave 1 assertion: err == nil.
func TestClient_InviteUserToChannel_OK(t *testing.T) {
	t.Skip("TODO Wave 1: implement InviteUserToChannel")
}

// TestClient_InviteUserToChannel_AlreadyMember — server returns already_in_channel.
// Wave 1 assertion: err == nil (idempotent — treated as success).
func TestClient_InviteUserToChannel_AlreadyMember(t *testing.T) {
	t.Skip("TODO Wave 1: implement InviteUserToChannel")
}

// TestClient_InviteUserToChannel_CantInviteSelf — server returns cant_invite_self.
// Wave 1 assertion: *slack.SlackAPIError with Code == "cant_invite_self".
func TestClient_InviteUserToChannel_CantInviteSelf(t *testing.T) {
	t.Skip("TODO Wave 1: implement InviteUserToChannel")
}

// TestClient_InviteUserToChannel_NotInChannel — server returns not_in_channel.
// Wave 1 assertion: *slack.SlackAPIError with Code == "not_in_channel".
func TestClient_InviteUserToChannel_NotInChannel(t *testing.T) {
	t.Skip("TODO Wave 1: implement InviteUserToChannel")
}
