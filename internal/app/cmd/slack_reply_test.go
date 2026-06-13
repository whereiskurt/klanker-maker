package cmd

import (
	"context"
	"strings"
	"testing"
)

// ──────────────────────────────────────────────
// Fakes for slack_reply.go tests
// ──────────────────────────────────────────────

// fakeSlackPostAPI records PostMessage calls.
type fakeSlackPostAPI struct {
	postCalls   []slackPostCall
	postErr     error
	postTS      string
}

type slackPostCall struct {
	channel  string
	subject  string
	body     string
	threadTS string
}

func (f *fakeSlackPostAPI) PostMessage(_ context.Context, channel, subject, body, threadTS string) (string, error) {
	f.postCalls = append(f.postCalls, slackPostCall{channel: channel, subject: subject, body: body, threadTS: threadTS})
	ts := f.postTS
	if ts == "" {
		ts = "1234567890.000001"
	}
	return ts, f.postErr
}

// fakeThreadLookup records LookupBySession calls.
type fakeThreadLookup struct {
	sessionID string // the session ID this fake is expecting
	channelID string // returned channel
	threadTS  string // returned thread ts
	agentType string
	err       error
}

func (f *fakeThreadLookup) LookupBySession(_ context.Context, sessionID, _ string) (channelID, threadTS, agentType string, err error) {
	if f.err != nil {
		return "", "", "", f.err
	}
	if sessionID == f.sessionID {
		return f.channelID, f.threadTS, f.agentType, nil
	}
	// miss
	return "", "", "", nil
}

// ──────────────────────────────────────────────
// Tests — TestRunSlackReply
// ──────────────────────────────────────────────

// TestRunSlackReply_VerbatimThreadChannel tests that --thread+--channel posts
// directly without any GSI query.
func TestRunSlackReply_VerbatimThreadChannel(t *testing.T) {
	poster := &fakeSlackPostAPI{}
	lookup := &fakeThreadLookup{}

	opts := SlackReplyOpts{
		Thread:  "1234567890.000001",
		Channel: "C0CHAN001",
		Body:    "hello from operator",
		Render:  "plain",
	}
	err := RunSlackReply(context.Background(), poster, lookup, opts)
	if err != nil {
		t.Fatalf("RunSlackReply verbatim: unexpected error: %v", err)
	}
	if len(poster.postCalls) != 1 {
		t.Fatalf("want 1 PostMessage call, got %d", len(poster.postCalls))
	}
	call := poster.postCalls[0]
	if call.channel != "C0CHAN001" {
		t.Errorf("channel = %q; want C0CHAN001", call.channel)
	}
	if call.threadTS != "1234567890.000001" {
		t.Errorf("thread_ts = %q; want 1234567890.000001", call.threadTS)
	}
	if call.body != "hello from operator" {
		t.Errorf("body = %q; want %q", call.body, "hello from operator")
	}
}

// TestRunSlackReply_SessionGSIHit tests --session resolution: GSI returns a
// row, PostMessage is called with (channel_id, thread_ts) from the row.
func TestRunSlackReply_SessionGSIHit(t *testing.T) {
	poster := &fakeSlackPostAPI{}
	lookup := &fakeThreadLookup{
		sessionID: "sess-abc123",
		channelID: "C0SESS001",
		threadTS:  "9876543210.000001",
		agentType: "claude",
	}

	opts := SlackReplyOpts{
		Session: "sess-abc123",
		Body:    "reply to session",
		Render:  "plain",
	}
	err := RunSlackReply(context.Background(), poster, lookup, opts)
	if err != nil {
		t.Fatalf("RunSlackReply session hit: unexpected error: %v", err)
	}
	if len(poster.postCalls) != 1 {
		t.Fatalf("want 1 PostMessage call, got %d", len(poster.postCalls))
	}
	call := poster.postCalls[0]
	if call.channel != "C0SESS001" {
		t.Errorf("channel = %q; want C0SESS001", call.channel)
	}
	if call.threadTS != "9876543210.000001" {
		t.Errorf("thread_ts = %q; want 9876543210.000001", call.threadTS)
	}
}

// TestRunSlackReply_SessionGSIMiss_FallbackSandboxChannel tests that when the
// GSI returns no row, RunSlackReply falls back to the root channel (top-level
// post, no thread_ts) when --sandbox-channel is set.
func TestRunSlackReply_SessionGSIMiss_FallbackSandboxChannel(t *testing.T) {
	poster := &fakeSlackPostAPI{}
	lookup := &fakeThreadLookup{
		sessionID: "OTHER-SESSION", // won't match "sess-missing"
		channelID: "C0NEVER",
	}

	opts := SlackReplyOpts{
		Session:        "sess-missing",
		SandboxChannel: "C0ROOT001",
		Body:           "root fallback",
		Render:         "plain",
	}
	err := RunSlackReply(context.Background(), poster, lookup, opts)
	if err != nil {
		t.Fatalf("RunSlackReply fallback: unexpected error: %v", err)
	}
	if len(poster.postCalls) != 1 {
		t.Fatalf("want 1 PostMessage call, got %d", len(poster.postCalls))
	}
	call := poster.postCalls[0]
	if call.channel != "C0ROOT001" {
		t.Errorf("channel = %q; want C0ROOT001", call.channel)
	}
	if call.threadTS != "" {
		t.Errorf("thread_ts = %q; want empty (top-level post)", call.threadTS)
	}
}

// TestRunSlackReply_NoResolution tests that when no --thread+--channel, no
// --session match, and no --sandbox-channel, an error is returned.
func TestRunSlackReply_NoResolution(t *testing.T) {
	poster := &fakeSlackPostAPI{}
	lookup := &fakeThreadLookup{} // empty: always miss

	opts := SlackReplyOpts{
		Body:   "orphan",
		Render: "plain",
	}
	err := RunSlackReply(context.Background(), poster, lookup, opts)
	if err == nil {
		t.Fatal("want error for no resolution, got nil")
	}
	if !strings.Contains(err.Error(), "no thread or channel resolved") {
		t.Errorf("want 'no thread or channel resolved' in error, got: %v", err)
	}
}

// TestRunSlackReply_VerbatimRequiresBothFlags tests that --thread without
// --channel returns a validation error.
func TestRunSlackReply_VerbatimRequiresBothFlags(t *testing.T) {
	poster := &fakeSlackPostAPI{}
	lookup := &fakeThreadLookup{}

	opts := SlackReplyOpts{
		Thread: "1234567890.000001",
		// Channel intentionally missing
		Body:   "missing channel",
		Render: "plain",
	}
	err := RunSlackReply(context.Background(), poster, lookup, opts)
	if err == nil {
		t.Fatal("want error when --thread set but --channel missing, got nil")
	}
	if !strings.Contains(err.Error(), "--channel") {
		t.Errorf("want --channel in error message, got: %v", err)
	}
}

// TestRunSlackReply_BodyRequired tests that an empty body returns an error.
func TestRunSlackReply_BodyRequired(t *testing.T) {
	poster := &fakeSlackPostAPI{}
	lookup := &fakeThreadLookup{}

	opts := SlackReplyOpts{
		Thread:  "1234567890.000001",
		Channel: "C0CHAN001",
		Body:    "", // empty
		Render:  "plain",
	}
	err := RunSlackReply(context.Background(), poster, lookup, opts)
	if err == nil {
		t.Fatal("want error for empty body, got nil")
	}
	if !strings.Contains(err.Error(), "body") {
		t.Errorf("want 'body' in error message, got: %v", err)
	}
}
