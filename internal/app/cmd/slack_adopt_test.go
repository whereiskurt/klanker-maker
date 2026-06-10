package cmd

import (
	"context"
	"strings"
	"testing"
)

// TestSlackAdopt_RejectsBadChannelID verifies that a channel ID not matching
// ^C[A-Z0-9]+$ is rejected with a format-hint error before any API calls.
func TestSlackAdopt_RejectsBadChannelID(t *testing.T) {
	err := runSlackAdopt(context.Background(), nil, nil, "github-bot", "not-an-id", "/sec/slack/")
	if err == nil || !strings.Contains(err.Error(), "^C[A-Z0-9]+$") {
		t.Fatalf("want format error mentioning regex, got %v", err)
	}
}

// TestSlackAdopt_RequiresBotMembership verifies that when the bot is NOT a
// member of the channel, the error says "not a member".
func TestSlackAdopt_RequiresBotMembership(t *testing.T) {
	api := &fakeSlackAPI{channelInfoMember: false}
	err := runSlackAdopt(context.Background(), api, &fakeChannelStore{m: map[string]string{}}, "github-bot", "C0X", "/sec/slack/")
	if err == nil || !strings.Contains(err.Error(), "not a member") {
		t.Fatalf("want membership error, got %v", err)
	}
}

// TestSlackAdopt_WritesThrough verifies that when the bot is a member the
// alias→channelID mapping is written to the DDB store.
func TestSlackAdopt_WritesThrough(t *testing.T) {
	api := &fakeSlackAPI{channelInfoMember: true}
	store := &fakeChannelStore{m: map[string]string{}}
	if err := runSlackAdopt(context.Background(), api, store, "github-bot", "C0X", "/sec/slack/"); err != nil {
		t.Fatal(err)
	}
	if store.m["github-bot"] != "C0X" {
		t.Fatalf("adopt must write DDB store, got %q", store.m["github-bot"])
	}
}
