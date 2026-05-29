package slack_test

// client_lookup_test.go — Phase 72 Wave 0 stubs for LookupUserByEmail.
// Plan 72-01 (Wave 1) flips the t.Skip calls to real assertions and
// implements the production method on *slack.Client.
//
// When Wave 1 lands:
//   - Add: func (c *Client) LookupUserByEmail(ctx context.Context, email string) (userID string, found bool, err error)
//   - Replace each t.Skip with the assertions described in the comment below it.

import (
	"testing"
)

// TestClient_LookupUserByEmail_Found — server returns ok=true with user.id="U123ABC".
// Wave 1 assertion: (id="U123ABC", found=true, err=nil).
func TestClient_LookupUserByEmail_Found(t *testing.T) {
	t.Skip("TODO Wave 1: implement LookupUserByEmail")
}

// TestClient_LookupUserByEmail_NotFound — server returns users_not_found.
// Wave 1 assertion: ("", false, nil) — typed boolean miss, NOT an error.
func TestClient_LookupUserByEmail_NotFound(t *testing.T) {
	t.Skip("TODO Wave 1: implement LookupUserByEmail")
}

// TestClient_LookupUserByEmail_MissingScope — server returns missing_scope.
// Wave 1 assertion: *slack.SlackAPIError returned with Code == "missing_scope".
func TestClient_LookupUserByEmail_MissingScope(t *testing.T) {
	t.Skip("TODO Wave 1: implement LookupUserByEmail")
}
