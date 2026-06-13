package bridge_test

// Phase 110 Plan 02: tests for the lookup-thread bridge action.
// Test names are verbatim from the validation strategy.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// fakeThreadStore is a test double for SlackThreadStore that supports
// LookupBySession in addition to the existing interface methods.
// It is declared here (not in handler_test.go) to avoid duplicate declaration.
// The handler_test.go fakeThreads only covers events paths, not the bridge.Handler.
type fakeThreadStore struct {
	// lookupBySessionFn is called for LookupBySession; return values are configurable.
	lookupBySessionFn func(ctx context.Context, sessionID, sandboxID string) (string, string, string, error)
}

func (f *fakeThreadStore) Get(_ context.Context, _, _ string) (string, error) { return "", nil }
func (f *fakeThreadStore) Upsert(_ context.Context, _, _, _ string) error     { return nil }
func (f *fakeThreadStore) LookupSandbox(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (f *fakeThreadStore) LookupBySession(ctx context.Context, sessionID, sandboxID string) (string, string, string, error) {
	if f.lookupBySessionFn != nil {
		return f.lookupBySessionFn(ctx, sessionID, sandboxID)
	}
	return "", "", "", nil
}

// handlerWithThreads returns a Handler wired with the given thread store and
// a pre-registered sandbox key for "sb-abc123". lookup-thread envelopes do NOT
// carry a channel, so the channel ownership check is bypassed — we set a
// non-empty Channel on the envelope to pass the missing_fields guard, and the
// Channels fake returns the same channel to prevent the channel_mismatch guard
// from blocking. But see the handler change: lookup-thread must bypass step 6.
func handlerWithThreads(pub ed25519.PublicKey, store bridge.SlackThreadStore) *bridge.Handler {
	return &bridge.Handler{
		Now: func() time.Time { return time.Unix(1714280400, 0) },
		Keys: &fakeKeys{keys: map[string]ed25519.PublicKey{
			"sb-abc123": pub,
		}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:    &fakeToken{tok: "xoxb-test"},
		Slack:    &fakeSlack{returnTS: "1714280400.000001"},
		Threads:  store,
	}
}

// makeLookupEnv builds a lookup-thread envelope. Note: the Channel field is set
// to satisfy the bridge envelope missing_fields guard. The handler bypass for
// lookup-thread must not require channel-ownership.
func makeLookupEnv(senderID, sessionID string) *slack.SlackEnvelope {
	return &slack.SlackEnvelope{
		Action:    slack.ActionLookupThread,
		Body:      "",
		Channel:   "ignored", // required by missing_fields guard; ownership check bypassed
		Nonce:     "aabbccddeeff00112233445566778810",
		SenderID:  senderID,
		SessionID: sessionID,
		Timestamp: 1714280400,
		Version:   slack.EnvelopeVersion,
	}
}

// TestHandler_LookupThread: signed lookup-thread envelope with a valid
// session_id returns 200 {ok:true, found:true, channel_id, thread_ts, agent_type}.
func TestHandler_LookupThread(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	store := &fakeThreadStore{
		lookupBySessionFn: func(_ context.Context, sessionID, sandboxID string) (string, string, string, error) {
			if sessionID == "sess-abc" && sandboxID == "sb-abc123" {
				return "C0123ABC", "1234567890.123456", "claude", nil
			}
			return "", "", "", nil
		},
	}

	h := handlerWithThreads(pub, store)
	env := makeLookupEnv("sb-abc123", "sess-abc")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode = %d; want 200. Body: %s", resp.StatusCode, resp.Body)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(resp.Body), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["ok"] != true {
		t.Errorf("ok = %v; want true", body["ok"])
	}
	if body["found"] != true {
		t.Errorf("found = %v; want true", body["found"])
	}
	if body["channel_id"] != "C0123ABC" {
		t.Errorf("channel_id = %v; want C0123ABC", body["channel_id"])
	}
	if body["thread_ts"] != "1234567890.123456" {
		t.Errorf("thread_ts = %v; want 1234567890.123456", body["thread_ts"])
	}
	if body["agent_type"] != "claude" {
		t.Errorf("agent_type = %v; want claude", body["agent_type"])
	}
}

// TestHandler_LookupThread_MissingSessionID: empty session_id returns 400.
func TestHandler_LookupThread_MissingSessionID(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	store := &fakeThreadStore{}
	h := handlerWithThreads(pub, store)

	env := makeLookupEnv("sb-abc123", "") // empty session_id
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 400 {
		t.Fatalf("StatusCode = %d; want 400. Body: %s", resp.StatusCode, resp.Body)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(resp.Body), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error"] != "missing_session_id" {
		t.Errorf("error = %v; want missing_session_id", body["error"])
	}
}

// TestHandler_LookupThread_WrongSandbox: session owned by another sandbox
// returns 200 {ok:true, found:false} (NOT an error, NOT the other channel).
func TestHandler_LookupThread_WrongSandbox(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	store := &fakeThreadStore{
		lookupBySessionFn: func(_ context.Context, sessionID, sandboxID string) (string, string, string, error) {
			// The session exists but belongs to "sb-other", not the requester "sb-abc123".
			// LookupBySession already filters by sandboxID — returns empty for cross-sandbox.
			return "", "", "", nil
		},
	}

	h := handlerWithThreads(pub, store)
	env := makeLookupEnv("sb-abc123", "sess-belongs-to-other")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode = %d; want 200. Body: %s", resp.StatusCode, resp.Body)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(resp.Body), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["ok"] != true {
		t.Errorf("ok = %v; want true", body["ok"])
	}
	if body["found"] != false {
		t.Errorf("found = %v; want false", body["found"])
	}
	// Must NOT contain channel_id in the response.
	if _, hasChannel := body["channel_id"]; hasChannel {
		t.Errorf("channel_id must not be present in found:false response; body: %s", resp.Body)
	}
}
