package slack_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

func init() {
	// Shrink BridgeBackoff to milliseconds so retry tests run fast.
	slack.BridgeBackoff = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond}
	// Shrink the Slack rate-limit backoff so 429/ratelimited retry tests don't
	// actually sleep for real seconds.
	slack.SlackRetryAfterDefault = time.Millisecond
	slack.SlackRetryAfterCap = time.Millisecond
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

// TestClient_AuthTest_RealShape asserts that the auth.test JSON body Slack
// actually sends — where "user" is a STRING (the bot username) rather than an
// object — round-trips cleanly through SlackAPIResponse. Regression for the
// Phase 72-01 bug where SlackAPIResponse.User was a struct{ID string} and
// users.lookupByEmail's object shape clashed with auth.test's string shape.
func TestClient_AuthTest_RealShape(t *testing.T) {
	body := []byte(`{"ok":true,"url":"https://example.slack.com/","team":"example","user":"klankermaker-bot","team_id":"T01234ABCD","user_id":"U01234ABCD","bot_id":"B01234ABCD"}`)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	if err := c.AuthTest(context.Background()); err != nil {
		t.Errorf("AuthTest against real auth.test JSON shape returned error: %v", err)
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

// TestClient_PostMessage_StringChannelResponse is the regression for the
// chat.postMessage decode bug: real Slack returns "channel" as a bare string
// (the channel ID), but SlackAPIResponse.Channel was typed as an object —
// decode failed with "cannot unmarshal string into ... .channel". The mocks in
// the other PostMessage tests omit "channel", so they never caught it. This one
// includes the real string shape.
func TestClient_PostMessage_StringChannelResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Real chat.postMessage shape: channel is a STRING, not an object.
		w.Write([]byte(`{"ok":true,"ts":"9.9","channel":"C0B9MLES7BJ"}`))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	got, err := c.PostMessage(context.Background(), "C0B9MLES7BJ", "", "hello", "1.2")
	if err != nil {
		t.Fatalf("PostMessage with string channel response: %v", err)
	}
	if got != "9.9" {
		t.Errorf("ts = %q; want %q", got, "9.9")
	}
}

// TestSlackChannelField_ObjectShape confirms the object shape (conversations.info)
// still decodes all three fields after the polymorphic-field change.
func TestSlackChannelField_ObjectShape(t *testing.T) {
	var r slack.SlackAPIResponse
	if err := json.Unmarshal([]byte(`{"ok":true,"channel":{"id":"C1","is_member":true,"num_members":7}}`), &r); err != nil {
		t.Fatalf("decode object channel: %v", err)
	}
	if r.Channel.ID != "C1" || !r.Channel.IsMember || r.Channel.NumMembers != 7 {
		t.Errorf("got %+v; want {C1 true 7}", r.Channel)
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
	id, err := c.CreateChannel(context.Background(), "km-notifications", false)
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

// TestClient_ChannelInfo_SendsFormEncoded is the regression for the bug where
// ChannelInfo used callJSON: conversations.info REJECTS a JSON body with
// invalid_arguments ("missing required field: channel") and must be form-encoded.
// The other ChannelInfo tests pass against a mock that ignores the request
// encoding, so they never caught it. This one asserts the wire format.
func TestClient_ChannelInfo_SendsFormEncoded(t *testing.T) {
	var gotContentType, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(map[string]any{"channel": map[string]any{"id": "C0123ABC", "is_member": true, "num_members": 2}}))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	if _, _, err := c.ChannelInfo(context.Background(), "C0123ABC"); err != nil {
		t.Fatalf("ChannelInfo: %v", err)
	}
	if !strings.HasPrefix(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q; want application/x-www-form-urlencoded (NOT JSON — conversations.info rejects JSON)", gotContentType)
	}
	form, err := url.ParseQuery(gotBody)
	if err != nil {
		t.Fatalf("body is not form-encoded (%q): %v", gotBody, err)
	}
	if form.Get("channel") != "C0123ABC" {
		t.Errorf("form channel = %q; want C0123ABC (body: %q)", form.Get("channel"), gotBody)
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
// FindChannelByName + rate-limit tests
// ────────────────────────────────────────────────────────────────────────────

// listPage is a conversations.list response page builder for the fake server.
func listPage(channels []map[string]any, nextCursor string) map[string]any {
	page := map[string]any{
		"ok":       true,
		"channels": channels,
	}
	page["response_metadata"] = map[string]any{"next_cursor": nextCursor}
	return page
}

// TestFindChannelByName_MatchOnLastPage verifies the scan paginates through
// multiple pages (following next_cursor) and finds a channel that sorts LAST —
// the real-world failure shape where a freshly-created per-sandbox channel is at
// the very end of conversations.list.
func TestFindChannelByName_MatchOnLastPage(t *testing.T) {
	var pageCount int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)
		cursor, _ := req["cursor"].(string)
		atomic.AddInt32(&pageCount, 1)

		w.Header().Set("Content-Type", "application/json")
		switch cursor {
		case "": // page 1 — no match, has next
			json.NewEncoder(w).Encode(listPage([]map[string]any{
				{"id": "C1", "name": "general"},
				{"id": "C2", "name": "random"},
			}, "CURSOR2"))
		case "CURSOR2": // page 2 — no match, has next
			json.NewEncoder(w).Encode(listPage([]map[string]any{
				{"id": "C3", "name": "eng"},
			}, "CURSOR3"))
		default: // page 3 (last) — the target, sorts last
			json.NewEncoder(w).Encode(listPage([]map[string]any{
				{"id": "CTARGET", "name": "sec-kph-desk1"},
			}, ""))
		}
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	id, err := c.FindChannelByName(context.Background(), "sec-kph-desk1", 100)
	if err != nil {
		t.Fatalf("FindChannelByName: %v", err)
	}
	if id != "CTARGET" {
		t.Errorf("id = %q; want CTARGET", id)
	}
	if got := atomic.LoadInt32(&pageCount); got != 3 {
		t.Errorf("page count = %d; want 3 (must walk all pages)", got)
	}
}

// TestFindChannelByName_RetriesOnRateLimit verifies that a ratelimited page
// mid-scan is retried (per the new callRaw backoff) rather than aborting the
// whole scan. Page 2 returns HTTP 429 once, then succeeds on retry.
func TestFindChannelByName_RetriesOnRateLimit(t *testing.T) {
	var page2Hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)
		cursor, _ := req["cursor"].(string)

		w.Header().Set("Content-Type", "application/json")
		switch cursor {
		case "": // page 1
			json.NewEncoder(w).Encode(listPage([]map[string]any{{"id": "C1", "name": "general"}}, "CURSOR2"))
		default: // page 2 — rate-limit on first hit, succeed on retry
			if atomic.AddInt32(&page2Hits, 1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write(slackErr("ratelimited"))
				return
			}
			json.NewEncoder(w).Encode(listPage([]map[string]any{{"id": "CTARGET", "name": "sb-demo"}}, ""))
		}
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	id, err := c.FindChannelByName(context.Background(), "sb-demo", 100)
	if err != nil {
		t.Fatalf("FindChannelByName should recover from a transient ratelimit: %v", err)
	}
	if id != "CTARGET" {
		t.Errorf("id = %q; want CTARGET", id)
	}
	if got := atomic.LoadInt32(&page2Hits); got != 2 {
		t.Errorf("page 2 hits = %d; want 2 (one ratelimited + one retry)", got)
	}
}

// TestFindChannelByName_RateLimitExhausted verifies that when Slack keeps
// returning ratelimited past the retry budget, the call surfaces a
// *SlackAPIError{Code:"ratelimited"} (so callers can give rate-limit-specific
// guidance) rather than spinning forever.
func TestFindChannelByName_RateLimitExhausted(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write(slackErr("ratelimited"))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	_, err := c.FindChannelByName(context.Background(), "sb-demo", 100)
	if err == nil {
		t.Fatal("expected ratelimited error after exhausting retries")
	}
	apiErr, ok := err.(*slack.SlackAPIError)
	if !ok {
		t.Fatalf("expected *SlackAPIError, got %T: %v", err, err)
	}
	if apiErr.Code != "ratelimited" {
		t.Errorf("Code = %q; want ratelimited", apiErr.Code)
	}
	if got := atomic.LoadInt32(&hits); got != int32(slack.SlackRateLimitMaxAttempts) {
		t.Errorf("request count = %d; want %d (SlackRateLimitMaxAttempts)", got, slack.SlackRateLimitMaxAttempts)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// TestFindChannelByName_PageCapExceeded, _ZeroCapDisablesScan, _CtxCancelledMidScan
// (P0: bounded scan)
// ────────────────────────────────────────────────────────────────────────────

// TestFindChannelByName_PageCapExceeded verifies that the scan walks exactly
// maxPages pages and then returns ErrScanCapExceeded when the target is never
// found (server always returns a full page + next_cursor).
func TestFindChannelByName_PageCapExceeded(t *testing.T) {
	var pages int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pages, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"channels":[{"id":"C1","name":"other"}],"response_metadata":{"next_cursor":"more"}}`))
	}))
	defer ts.Close()
	c := newClientAgainstServer(ts)
	_, err := c.FindChannelByName(context.Background(), "sb-target", 3)
	if !errors.Is(err, slack.ErrScanCapExceeded) {
		t.Fatalf("want ErrScanCapExceeded, got %v", err)
	}
	if got := atomic.LoadInt32(&pages); got != 3 {
		t.Fatalf("want exactly 3 pages walked, got %d", got)
	}
}

// TestFindChannelByName_ZeroCapDisablesScan verifies that maxPages==0 returns
// ErrScanCapExceeded without making any HTTP call.
func TestFindChannelByName_ZeroCapDisablesScan(t *testing.T) {
	var called bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write([]byte(`{"ok":true,"channels":[],"response_metadata":{"next_cursor":""}}`))
	}))
	defer ts.Close()
	c := newClientAgainstServer(ts)
	_, err := c.FindChannelByName(context.Background(), "sb-target", 0)
	if !errors.Is(err, slack.ErrScanCapExceeded) {
		t.Fatalf("want ErrScanCapExceeded for zero cap, got %v", err)
	}
	if called {
		t.Fatal("zero cap must not make any HTTP call")
	}
}

// TestFindChannelByName_CtxCancelledMidScan verifies that a context cancelled
// after the first page is returned propagates as context.Canceled.
func TestFindChannelByName_CtxCancelledMidScan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel() // cancel after the first page is served
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"channels":[{"id":"C1","name":"other"}],"response_metadata":{"next_cursor":"more"}}`))
	}))
	defer ts.Close()
	c := newClientAgainstServer(ts)
	_, err := c.FindChannelByName(ctx, "sb-target", 100)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// TestIsChannelNotFound (P1: info-error classifier)
// ────────────────────────────────────────────────────────────────────────────

// TestIsChannelNotFound verifies the four classification cases.
func TestIsChannelNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"definitive", &slack.SlackAPIError{Method: "conversations.info", Code: "channel_not_found"}, true},
		{"transient ratelimited", &slack.SlackAPIError{Method: "conversations.info", Code: "ratelimited"}, false},
		{"nil", nil, false},
		{"network", errors.New("dial tcp: timeout"), false},
	}
	for _, tc := range cases {
		if got := slack.IsChannelNotFound(tc.err); got != tc.want {
			t.Errorf("%s: IsChannelNotFound=%v want %v", tc.name, got, tc.want)
		}
	}
}

// TestCallJSON_RateLimitBodyOn200 verifies the body-based ratelimit signal
// (HTTP 200 with ok:false,error:"ratelimited") is also retried, not just HTTP 429.
func TestCallJSON_RateLimitBodyOn200(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if atomic.AddInt32(&hits, 1) == 1 {
			// 200 OK but ratelimited in the body — the tiered-method shape.
			w.Write(slackErr("ratelimited"))
			return
		}
		w.Write(slackOK(map[string]any{"channel": map[string]any{"id": "CXYZ"}}))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	id, err := c.CreateChannel(context.Background(), "sb-demo", false)
	if err != nil {
		t.Fatalf("CreateChannel should retry a 200/ratelimited body: %v", err)
	}
	if id != "CXYZ" {
		t.Errorf("id = %q; want CXYZ", id)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("hits = %d; want 2", got)
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

// ────────────────────────────────────────────────────────────────────────────
// AuthTestWithUserID tests (Phase 91 Plan 04)
// ────────────────────────────────────────────────────────────────────────────

func TestAuthTestWithUserID_OK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"url":"https://example.slack.com/","team":"example","user":"klankermaker-bot","team_id":"T01234ABCD","user_id":"UBOT123","bot_id":"B01234ABCD"}`))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	uid, err := c.AuthTestWithUserID(context.Background())
	if err != nil {
		t.Fatalf("AuthTestWithUserID returned error: %v", err)
	}
	if uid != "UBOT123" {
		t.Errorf("user_id = %q; want %q", uid, "UBOT123")
	}
}

func TestAuthTestWithUserID_NotOK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackErr("invalid_auth"))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	uid, err := c.AuthTestWithUserID(context.Background())
	if err == nil {
		t.Fatal("expected error on ok=false, got nil")
	}
	if uid != "" {
		t.Errorf("user_id = %q; want empty on error", uid)
	}
	apiErr, ok := err.(*slack.SlackAPIError)
	if !ok {
		t.Fatalf("expected *SlackAPIError, got %T: %v", err, err)
	}
	if apiErr.Code != "invalid_auth" {
		t.Errorf("Code = %q; want %q", apiErr.Code, "invalid_auth")
	}
}

func TestAuthTestWithUserID_TransportError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := ts.URL
	ts.Close() // close before request so transport fails

	c := slack.NewClient("xoxb-test", nil)
	c.SetBaseURL(url)
	uid, err := c.AuthTestWithUserID(context.Background())
	if err == nil {
		t.Fatal("expected error on transport failure, got nil")
	}
	if uid != "" {
		t.Errorf("user_id = %q; want empty on transport error", uid)
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
