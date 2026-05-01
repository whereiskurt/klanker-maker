package slack_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/whereiskurt/klankrmkr/pkg/slack"
)

func init() {
	// Shrink BridgeBackoff to milliseconds so retry tests run fast.
	slack.BridgeBackoff = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond}
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func newClientAgainstServer(ts *httptest.Server) *slack.Client {
	c := slack.NewClient("xoxb-test", ts.Client())
	c.SetBaseURL(ts.URL)
	return c
}

func slackOK(extra map[string]any) []byte {
	m := map[string]any{"ok": true}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return b
}

func slackErr(code string) []byte {
	b, _ := json.Marshal(map[string]any{"ok": false, "error": code})
	return b
}

// ────────────────────────────────────────────────────────────────────────────
// Client method tests
// ────────────────────────────────────────────────────────────────────────────

func TestClient_AuthTest_OK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(nil))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	if err := c.AuthTest(context.Background()); err != nil {
		t.Errorf("AuthTest returned error: %v", err)
	}
}

func TestClient_AuthTest_NotOK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackErr("invalid_auth"))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	err := c.AuthTest(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr, ok := err.(*slack.SlackAPIError)
	if !ok {
		t.Fatalf("expected *SlackAPIError, got %T: %v", err, err)
	}
	if apiErr.Code != "invalid_auth" {
		t.Errorf("Code = %q; want %q", apiErr.Code, "invalid_auth")
	}
}

func TestClient_PostMessage_BoldHeader(t *testing.T) {
	var capturedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(map[string]any{"ts": "123.456"}))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	ts2, err := c.PostMessage(context.Background(), "C0123", "subject", "body", "")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if ts2 != "123.456" {
		t.Errorf("ts = %q; want %q", ts2, "123.456")
	}

	var payload map[string]any
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("decode captured body: %v", err)
	}
	text, _ := payload["text"].(string)
	want := "*subject*\n\nbody"
	if text != want {
		t.Errorf("text = %q; want %q", text, want)
	}
	if v, ok := payload["unfurl_links"]; !ok || v.(bool) {
		t.Errorf("unfurl_links = %v; want false", v)
	}
	if v, ok := payload["unfurl_media"]; !ok || v.(bool) {
		t.Errorf("unfurl_media = %v; want false", v)
	}
}

func TestClient_PostMessage_WithThreadTS(t *testing.T) {
	var capturedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(map[string]any{"ts": "456.789"}))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	_, err := c.PostMessage(context.Background(), "C0123", "subj", "body", "123.456")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v, ok := payload["thread_ts"]; !ok || v != "123.456" {
		t.Errorf("thread_ts = %v; want 123.456", v)
	}
}

func TestClient_CreateChannel_ReturnsID(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(map[string]any{"channel": map[string]any{"id": "C0123ABC"}}))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	id, err := c.CreateChannel(context.Background(), "km-notifications")
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if id != "C0123ABC" {
		t.Errorf("channel ID = %q; want %q", id, "C0123ABC")
	}
}

func TestClient_InviteShared_OK(t *testing.T) {
	var capturedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(nil))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	if err := c.InviteShared(context.Background(), "C0123ABC", "a@b"); err != nil {
		t.Fatalf("InviteShared: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	emails, _ := payload["emails"].([]any)
	if len(emails) != 1 || emails[0] != "a@b" {
		t.Errorf("emails = %v; want [a@b]", emails)
	}
	if v, ok := payload["external_limited"]; !ok || !v.(bool) {
		t.Errorf("external_limited = %v; want true", v)
	}
}

func TestClient_ChannelInfo_BotIsMember(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(map[string]any{
			"channel": map[string]any{
				"id":          "C0123ABC",
				"is_member":   true,
				"num_members": 7,
			},
		}))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	count, isMember, err := c.ChannelInfo(context.Background(), "C0123ABC")
	if err != nil {
		t.Fatalf("ChannelInfo: %v", err)
	}
	if !isMember {
		t.Error("isMember = false; want true")
	}
	if count != 7 {
		t.Errorf("memberCount = %d; want 7", count)
	}
}

func TestClient_ChannelInfo_BotNotMember(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(map[string]any{
			"channel": map[string]any{
				"id":          "C0123ABC",
				"is_member":   false,
				"num_members": 3,
			},
		}))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	_, isMember, err := c.ChannelInfo(context.Background(), "C0123ABC")
	if err != nil {
		t.Fatalf("ChannelInfo: %v", err)
	}
	if isMember {
		t.Error("isMember = true; want false")
	}
}

func TestClient_ChannelInfo_ChannelNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackErr("channel_not_found"))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	_, _, err := c.ChannelInfo(context.Background(), "C9NOTFOUND")
	if err == nil {
		t.Fatal("expected error for channel_not_found, got nil")
	}
	apiErr, ok := err.(*slack.SlackAPIError)
	if !ok {
		t.Fatalf("expected *SlackAPIError, got %T: %v", err, err)
	}
	if apiErr.Code != "channel_not_found" {
		t.Errorf("Code = %q; want %q", apiErr.Code, "channel_not_found")
	}
}

func TestClient_ArchiveChannel_OK(t *testing.T) {
	var capturedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(nil))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	if err := c.ArchiveChannel(context.Background(), "C0123ABC"); err != nil {
		t.Fatalf("ArchiveChannel: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["channel"] != "C0123ABC" {
		t.Errorf("channel = %v; want C0123ABC", payload["channel"])
	}
}

func TestClient_AuthHeader_Bearer(t *testing.T) {
	var capturedAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(nil))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	_ = c.AuthTest(context.Background())

	if !strings.HasPrefix(capturedAuth, "Bearer xoxb-test") {
		t.Errorf("Authorization = %q; want 'Bearer xoxb-test'", capturedAuth)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// PostToBridge tests
// ────────────────────────────────────────────────────────────────────────────

func bridgeEnvelope() *slack.SlackEnvelope {
	return &slack.SlackEnvelope{
		Action:    slack.ActionPost,
		Body:      "hello",
		Channel:   "C0123ABC",
		Nonce:     "00000000000000000000000000000000",
		SenderID:  "sb-abc123",
		Subject:   "test",
		ThreadTS:  "",
		Timestamp: 1714280400,
		Version:   1,
	}
}

func fakeSig() []byte { return []byte("fakesignaturebytes") }

func TestPostToBridge_200_HappyPath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(slack.PostResponse{OK: true, TS: "123"})
	}))
	defer ts.Close()

	resp, err := slack.PostToBridge(context.Background(), ts.URL, bridgeEnvelope(), fakeSig())
	if err != nil {
		t.Fatalf("PostToBridge: %v", err)
	}
	if !resp.OK {
		t.Error("PostResponse.OK = false; want true")
	}
	if resp.TS != "123" {
		t.Errorf("PostResponse.TS = %q; want %q", resp.TS, "123")
	}
}

func TestPostToBridge_4xx_FailFast(t *testing.T) {
	var count int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(401)
		w.Write([]byte("unauthorized"))
	}))
	defer ts.Close()

	_, err := slack.PostToBridge(context.Background(), ts.URL, bridgeEnvelope(), fakeSig())
	if err == nil {
		t.Fatal("expected error on 4xx, got nil")
	}
	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("request count = %d; want 1 (no retry on 4xx)", count)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error %q does not mention status 401", err.Error())
	}
}

// TestPostToBridge_5xxNotRetried verifies that a 5xx response is treated as
// fail-fast (same as 4xx), NOT retried. This prevents the nonce-replay masking
// bug where a retried 5xx envelope would return 401 replayed_nonce from the bridge.
func TestPostToBridge_5xxNotRetried(t *testing.T) {
	var count int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(502)
		w.Write([]byte(`{"ok":false,"error":"not_in_channel"}`))
	}))
	defer ts.Close()

	_, err := slack.PostToBridge(context.Background(), ts.URL, bridgeEnvelope(), fakeSig())
	if err == nil {
		t.Fatal("expected error on 5xx, got nil")
	}
	// Must NOT retry: exactly 1 request.
	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("request count = %d; want 1 (5xx must not retry)", count)
	}
	// Error must surface the HTTP status and body verbatim.
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error %q does not mention status 502", err.Error())
	}
	if !strings.Contains(err.Error(), "not_in_channel") {
		t.Errorf("error %q does not contain bridge error body", err.Error())
	}
}

// TestPostToBridge_503NotRetried verifies 503 is also fail-fast (not just 502).
func TestPostToBridge_503NotRetried(t *testing.T) {
	var count int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(503)
		w.Write([]byte("service unavailable"))
	}))
	defer ts.Close()

	_, err := slack.PostToBridge(context.Background(), ts.URL, bridgeEnvelope(), fakeSig())
	if err == nil {
		t.Fatal("expected error on 503, got nil")
	}
	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("request count = %d; want 1 (5xx must not retry)", count)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error %q does not mention 503", err.Error())
	}
}

func TestPostToBridge_NetworkError_Retries(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No response, just close — tests server close behavior
	}))
	url := ts.URL
	ts.Close() // close immediately so all attempts get network errors

	_, err := slack.PostToBridge(context.Background(), url, bridgeEnvelope(), fakeSig())
	if err == nil {
		t.Fatal("expected error after network errors, got nil")
	}
}

func TestPostToBridge_HeadersSet(t *testing.T) {
	var capturedSenderID, capturedSig string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSenderID = r.Header.Get("X-KM-Sender-ID")
		capturedSig = r.Header.Get("X-KM-Signature")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(slack.PostResponse{OK: true})
	}))
	defer ts.Close()

	env := bridgeEnvelope()
	sig := []byte("mysig")
	_, err := slack.PostToBridge(context.Background(), ts.URL, env, sig)
	if err != nil {
		t.Fatalf("PostToBridge: %v", err)
	}
	if capturedSenderID != env.SenderID {
		t.Errorf("X-KM-Sender-ID = %q; want %q", capturedSenderID, env.SenderID)
	}
	expectedSig := base64.StdEncoding.EncodeToString(sig)
	if capturedSig != expectedSig {
		t.Errorf("X-KM-Signature = %q; want %q", capturedSig, expectedSig)
	}
}

func TestPostToBridge_ContextCancel_ReturnsCtxErr(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Signal the test that the first request arrived, then block until test unblocks
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
		w.WriteHeader(503)
	}))
	defer func() {
		close(block) // unblock the handler so it can exit cleanly
		ts.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())

	// Run PostToBridge in a goroutine because it will block on the first request
	errCh := make(chan error, 1)
	go func() {
		_, err := slack.PostToBridge(ctx, ts.URL, bridgeEnvelope(), fakeSig())
		errCh <- err
	}()

	// Wait for first request to start, then cancel the context
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first request")
	}
	cancel()

	// The function should return ctx.Err() (context.Canceled)
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error after context cancel, got nil")
		}
		// Accept either context.Canceled or a wrapped version
		if !strings.Contains(err.Error(), "context canceled") && err != context.Canceled {
			t.Logf("got error (acceptable): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for PostToBridge to return after cancel")
	}
}
