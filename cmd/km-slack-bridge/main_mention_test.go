package main

import (
	"context"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// fakeSlackAuthTest91 is a minimal SlackAuthTestAPI that counts AuthTest calls.
type fakeSlackAuthTest91 struct {
	calls int
	uid   string
}

func (f *fakeSlackAuthTest91) AuthTest(_ context.Context, _ string) (string, error) {
	f.calls++
	return f.uid, nil
}

// fakeBotToken91 returns a fixed token for use in CachedBotUserIDFetcher.
type fakeBotToken91 struct{ token string }

func (f *fakeBotToken91) Fetch(_ context.Context) (string, error) { return f.token, nil }

// TestWireEventsHandler_BotUserIDPrime verifies POL-09:
//  1. KM_SLACK_MENTION_ONLY="true"  → h.MentionOnly == true
//  2. KM_SLACK_MENTION_ONLY missing → h.MentionOnly == false
//  3. KM_SLACK_MENTION_ONLY="false" → h.MentionOnly == false
//  4. KM_SLACK_BOT_USER_ID="UBOT123" → Fetch returns "UBOT123" without calling AuthTest
//  5. KM_SLACK_BOT_USER_ID unset    → cache stays cold; Fetch calls AuthTest live
func TestWireEventsHandler_BotUserIDPrime(t *testing.T) {
	t.Run("mention_only_true", func(t *testing.T) {
		t.Setenv("KM_SLACK_MENTION_ONLY", "true")
		t.Setenv("KM_SLACK_BOT_USER_ID", "")

		authAPI := &fakeSlackAuthTest91{uid: "ULIVE"}
		tokenFetcher := &fakeBotToken91{token: "xoxb-test"}
		fetcher := &bridge.CachedBotUserIDFetcher{
			SlackAPI:     authAPI,
			TokenFetcher: tokenFetcher,
		}
		h := &bridge.EventsHandler{}
		WireMentionOnly(h, fetcher)

		if !h.MentionOnly {
			t.Errorf("expected MentionOnly=true when KM_SLACK_MENTION_ONLY=true")
		}
	})

	t.Run("mention_only_unset", func(t *testing.T) {
		t.Setenv("KM_SLACK_BOT_USER_ID", "")

		authAPI := &fakeSlackAuthTest91{uid: "ULIVE"}
		tokenFetcher := &fakeBotToken91{token: "xoxb-test"}
		fetcher := &bridge.CachedBotUserIDFetcher{
			SlackAPI:     authAPI,
			TokenFetcher: tokenFetcher,
		}
		h := &bridge.EventsHandler{}
		WireMentionOnly(h, fetcher)

		if h.MentionOnly {
			t.Errorf("expected MentionOnly=false when KM_SLACK_MENTION_ONLY unset")
		}
	})

	t.Run("mention_only_false", func(t *testing.T) {
		t.Setenv("KM_SLACK_MENTION_ONLY", "false")
		t.Setenv("KM_SLACK_BOT_USER_ID", "")

		authAPI := &fakeSlackAuthTest91{uid: "ULIVE"}
		tokenFetcher := &fakeBotToken91{token: "xoxb-test"}
		fetcher := &bridge.CachedBotUserIDFetcher{
			SlackAPI:     authAPI,
			TokenFetcher: tokenFetcher,
		}
		h := &bridge.EventsHandler{}
		WireMentionOnly(h, fetcher)

		if h.MentionOnly {
			t.Errorf("expected MentionOnly=false when KM_SLACK_MENTION_ONLY=false")
		}
	})

	t.Run("bot_user_id_primed", func(t *testing.T) {
		t.Setenv("KM_SLACK_MENTION_ONLY", "true")
		t.Setenv("KM_SLACK_BOT_USER_ID", "UBOT123")

		authAPI := &fakeSlackAuthTest91{uid: "SHOULD_NOT_BE_CALLED"}
		tokenFetcher := &fakeBotToken91{token: "xoxb-test"}
		fetcher := &bridge.CachedBotUserIDFetcher{
			SlackAPI:     authAPI,
			TokenFetcher: tokenFetcher,
		}
		h := &bridge.EventsHandler{}
		WireMentionOnly(h, fetcher)

		uid, err := fetcher.Fetch(context.Background())
		if err != nil {
			t.Fatalf("unexpected Fetch error: %v", err)
		}
		if uid != "UBOT123" {
			t.Errorf("expected primed uid=UBOT123, got %q", uid)
		}
		if authAPI.calls != 0 {
			t.Errorf("expected NO AuthTest call when KM_SLACK_BOT_USER_ID was set, got %d calls", authAPI.calls)
		}
	})

	t.Run("bot_user_id_unset_cache_cold", func(t *testing.T) {
		t.Setenv("KM_SLACK_MENTION_ONLY", "false")
		t.Setenv("KM_SLACK_BOT_USER_ID", "")

		authAPI := &fakeSlackAuthTest91{uid: "ULIVE123"}
		tokenFetcher := &fakeBotToken91{token: "xoxb-test"}
		fetcher := &bridge.CachedBotUserIDFetcher{
			SlackAPI:     authAPI,
			TokenFetcher: tokenFetcher,
		}
		h := &bridge.EventsHandler{}
		WireMentionOnly(h, fetcher)

		uid, err := fetcher.Fetch(context.Background())
		if err != nil {
			t.Fatalf("unexpected Fetch error: %v", err)
		}
		if uid != "ULIVE123" {
			t.Errorf("expected live auth.test uid=ULIVE123, got %q", uid)
		}
		if authAPI.calls != 1 {
			t.Errorf("expected 1 AuthTest call when KM_SLACK_BOT_USER_ID unset, got %d", authAPI.calls)
		}
	})
}
