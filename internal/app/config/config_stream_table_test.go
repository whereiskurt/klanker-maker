package config

import "testing"

// Tests for Plan 68-03: Config.GetSlackStreamMessagesTableName helper.
//
// Mirrors the Phase 67 GetSlackThreadsTableName pattern: nil-safe, default
// prefix "km", custom prefix overrides default, explicit table name field
// wins over prefix-derived name.
//
// Decision (Plan 68-03 Open Question 1): table name is
// "{prefix}-slack-stream-messages" (NOT "{prefix}-km-slack-stream-messages")
// for consistency with Phase 67's "{prefix}-slack-threads" pattern.

func TestConfig_GetSlackStreamMessagesTableName_DefaultPrefix(t *testing.T) {
	c := &Config{}
	got := c.GetSlackStreamMessagesTableName()
	if got != "km-slack-stream-messages" {
		t.Fatalf("expected default 'km-slack-stream-messages', got %q", got)
	}
}

func TestConfig_GetSlackStreamMessagesTableName_CustomPrefix(t *testing.T) {
	c := &Config{ResourcePrefix: "stg"}
	got := c.GetSlackStreamMessagesTableName()
	if got != "stg-slack-stream-messages" {
		t.Fatalf("expected 'stg-slack-stream-messages', got %q", got)
	}
}

func TestConfig_GetSlackStreamMessagesTableName_ExplicitOverride(t *testing.T) {
	c := &Config{
		ResourcePrefix:               "stg",
		SlackStreamMessagesTableName: "custom-table",
	}
	got := c.GetSlackStreamMessagesTableName()
	if got != "custom-table" {
		t.Fatalf("explicit override should win, got %q", got)
	}
}

func TestConfig_GetSlackStreamMessagesTableName_NilReceiver(t *testing.T) {
	var c *Config
	got := c.GetSlackStreamMessagesTableName()
	if got != "km-slack-stream-messages" {
		t.Fatalf("nil receiver: want 'km-slack-stream-messages', got %q", got)
	}
}
