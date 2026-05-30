package cmd

import "testing"

// TestRunSlackInit_BotUserIDCached stubs the Wave 0 contract for POL-07.
// Plan 91-04 will implement the real test once RunSlackInit calls AuthTestWithUserID
// and writes {prefix}slack/bot-user-id to SSM — fake SSMStore captures the Put call,
// fake SlackInitAPI returns UID.
func TestRunSlackInit_BotUserIDCached(t *testing.T) {
	t.Skip("TODO Plan 91-04: implement once RunSlackInit calls AuthTestWithUserID and writes {prefix}slack/bot-user-id — fake SSMStore captures the Put call, fake SlackInitAPI returns UID")
}

// TestRotateToken_BotUserIDCached stubs the Wave 0 contract for POL-08.
// Plan 91-04 will implement the real test once RunSlackRotateToken re-caches
// bot_user_id — fake SSMStore captures the Put call after AuthTestWithUserID returns.
func TestRotateToken_BotUserIDCached(t *testing.T) {
	t.Skip("TODO Plan 91-04: implement once RunSlackRotateToken re-caches bot_user_id — fake SSMStore captures the Put call after AuthTestWithUserID returns")
}
