package slack

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SlackAPIBase is the Slack Web API root. Override only in tests via Client.SetBaseURL.
const SlackAPIBase = "https://slack.com/api"

// Client is a thin Slack Web API client. Construct with NewClient(botToken, httpClient).
type Client struct {
	httpClient *http.Client
	baseURL    string
	botToken   string
}

// NewClient builds a Client. Pass an httpClient for testing; nil falls back to
// http.DefaultClient with a 10s timeout.
func NewClient(botToken string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{httpClient: httpClient, baseURL: SlackAPIBase, botToken: botToken}
}

// SetBaseURL is for tests — point the client at httptest.NewServer.URL.
func (c *Client) SetBaseURL(u string) { c.baseURL = u }

// SlackAPIResponse covers the subset of Slack response fields used in Phase 63.
type SlackAPIResponse struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	TS        string `json:"ts,omitempty"`
	Permalink string `json:"permalink,omitempty"` // Phase 70 — chat.getPermalink response
	Channel   struct {
		ID         string `json:"id"`
		IsMember   bool   `json:"is_member"`
		NumMembers int    `json:"num_members"`
	} `json:"channel,omitempty"`
	// User is populated by users.lookupByEmail (object shape). The same field
	// in auth.test responses is a string (the bot username), so SlackUserField
	// tolerates both — string shape decodes to an empty User with ID == "".
	User SlackUserField `json:"user,omitempty"`

	// Channels and ResponseMetadata are populated by conversations.list.
	// JSON-decode-safe to leave them empty on responses that don't include them.
	Channels         []SlackChannelSummary `json:"channels,omitempty"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor,omitempty"`
	} `json:"response_metadata,omitempty"`
}

// SlackUserField decodes the polymorphic "user" field returned by Slack:
//   - auth.test returns "user": "bot-username" (a string — the bot's display name)
//   - users.lookupByEmail returns "user": {"id": "U...", ...} (an object)
//
// Only the ID is needed downstream; the bot-username string shape decodes to
// SlackUserField{ID: ""} so the parent SlackAPIResponse decode succeeds for both.
type SlackUserField struct {
	ID string
}

func (u *SlackUserField) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		// String shape (auth.test) — username string, no ID to extract.
		return nil
	}
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	u.ID = obj.ID
	return nil
}

// SlackChannelSummary is the per-channel shape returned by conversations.list.
// We only need ID + Name for lookup; the API returns many more fields.
type SlackChannelSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsArchived bool   `json:"is_archived"`
}

// SlackAPIError carries a non-OK Slack response. Lambda surfaces the Error
// field in its 502 response so operators see the upstream code.
type SlackAPIError struct {
	Method string
	Code   string
}

func (e *SlackAPIError) Error() string {
	return fmt.Sprintf("slack %s: %s", e.Method, e.Code)
}

// callForm dispatches to a Slack Web API method using
// application/x-www-form-urlencoded body. Required for legacy methods that
// reject JSON bodies with invalid_arguments (notably users.lookupByEmail and
// users.info). Returns the parsed SlackAPIResponse and a *SlackAPIError on
// non-OK responses.
func (c *Client) callForm(ctx context.Context, method string, form url.Values) (*SlackAPIResponse, error) {
	var encoded string
	if form != nil {
		encoded = form.Encode()
	}
	newReq := func() (*http.Request, error) {
		var rdr io.Reader
		if form != nil {
			rdr = strings.NewReader(encoded)
		}
		req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/"+method, rdr)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.botToken)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		return req, nil
	}
	raw, err := c.callRaw(ctx, method, newReq)
	if err != nil {
		return nil, err
	}
	return decodeSlackResponse(method, raw)
}

// callJSON is the shared JSON-body method dispatcher.
func (c *Client) callJSON(ctx context.Context, method string, body any) (*SlackAPIResponse, error) {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("slack: marshal %s: %w", method, err)
		}
		payload = b
	}
	newReq := func() (*http.Request, error) {
		var rdr io.Reader
		if payload != nil {
			rdr = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/"+method, rdr)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.botToken)
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		return req, nil
	}
	raw, err := c.callRaw(ctx, method, newReq)
	if err != nil {
		return nil, err
	}
	return decodeSlackResponse(method, raw)
}

// decodeSlackResponse parses the standard {ok,error,...} envelope and converts a
// non-OK body into a *SlackAPIError, preserving the decoded response so callers
// that inspect typed misses (e.g. users_not_found) keep working.
func decodeSlackResponse(method string, raw []byte) (*SlackAPIResponse, error) {
	var apiResp SlackAPIResponse
	if err := json.Unmarshal(raw, &apiResp); err != nil {
		return nil, fmt.Errorf("slack: decode %s: %w", method, err)
	}
	if !apiResp.OK {
		return &apiResp, &SlackAPIError{Method: method, Code: apiResp.Error}
	}
	return &apiResp, nil
}

// Rate-limit handling for Slack Web API methods.
//
// Slack throttles per-method (Tier 1–4) and throttles non-Marketplace apps
// harder still. conversations.list (Tier 2) is the painful one: enumerating a
// large workspace (thousands of channels, many pages) drains the per-minute
// budget and Slack starts answering with HTTP 429 — or, for some tiered methods,
// an HTTP 200 body carrying ok:false, error:"ratelimited". Before this handling
// the very first throttled page aborted the whole scan (see FindChannelByName).
//
// These knobs bound the retry so a wedged call can never blow the Lambda
// timeout. Exposed (like BridgeBackoff) so tests can shrink them.
var (
	// SlackRateLimitMaxAttempts is the total attempt count (initial + retries)
	// for a rate-limited Slack call.
	SlackRateLimitMaxAttempts = 3
	// SlackRetryAfterCap caps how long a single Retry-After sleep may be. Slack
	// can advise tens of seconds; we never sleep longer than this to stay
	// Lambda-safe.
	SlackRetryAfterCap = 30 * time.Second
	// SlackRetryAfterDefault is used when a 429/ratelimited response carries no
	// usable Retry-After header.
	SlackRetryAfterDefault = time.Second
)

// slackSleep performs the inter-attempt sleep, honoring ctx so a cancelled
// context aborts promptly. Package-level so tests can swap it for a no-op.
var slackSleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// callRaw sends a freshly-built request (via newReq) and returns the response
// body bytes, retrying on Slack rate-limiting (HTTP 429, or ok:false
// error:"ratelimited") up to SlackRateLimitMaxAttempts with a Retry-After-aware,
// capped backoff. ctx is honored during sleeps. A fresh request is built per
// attempt because the body reader is consumed on each send.
//
// On exhausted retries it returns a *SlackAPIError{Code:"ratelimited"} so
// callers can detect the rate-limit case (and avoid emitting misleading
// "grant channels:read" guidance).
func (c *Client) callRaw(ctx context.Context, method string, newReq func() (*http.Request, error)) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		req, err := newReq()
		if err != nil {
			return nil, err
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("slack: read %s: %w", method, readErr)
		}

		if !isSlackRateLimited(resp.StatusCode, body) {
			return body, nil
		}
		if attempt >= SlackRateLimitMaxAttempts-1 {
			return nil, &SlackAPIError{Method: method, Code: "ratelimited"}
		}
		if err := slackSleep(ctx, retryAfterDelay(resp.Header.Get("Retry-After"))); err != nil {
			return nil, err
		}
	}
}

// isSlackRateLimited reports whether a response indicates Slack rate-limiting,
// covering both the HTTP 429 signal and the ok:false/error:"ratelimited" body
// some tiered methods return with a 200 status.
func isSlackRateLimited(status int, body []byte) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	var probe struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	return !probe.OK && probe.Error == "ratelimited"
}

// retryAfterDelay parses a Retry-After header (seconds) into a capped backoff,
// falling back to SlackRetryAfterDefault when absent or unparseable.
func retryAfterDelay(header string) time.Duration {
	d := SlackRetryAfterDefault
	if secs, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && secs > 0 {
		d = time.Duration(secs) * time.Second
	}
	if d > SlackRetryAfterCap {
		d = SlackRetryAfterCap
	}
	return d
}

// AuthTest validates the bot token. Used by km slack init and km doctor.
func (c *Client) AuthTest(ctx context.Context) error {
	_, err := c.callJSON(ctx, "auth.test", nil)
	return err
}

// AuthTestWithUserID validates the bot token and returns the bot's Slack user_id
// (e.g. "UBOT123"). Phase 91 — km slack init / rotate-token cache this in SSM
// at {prefix}slack/bot-user-id so the bridge Lambda can prime its mention-scan
// bot ID without a live auth.test round-trip on cold-start.
//
// The wider auth.test response shape includes ok, team, user, user_id, team_id,
// bot_id, etc. — we only need user_id. Returns ("", err) on transport error or
// ok=false; caller decides whether to fail the whole flow or warn and continue.
func (c *Client) AuthTestWithUserID(ctx context.Context) (string, error) {
	// Use a dedicated struct that captures user_id directly. We cannot use the
	// existing callJSON path because SlackAPIResponse.User is a polymorphic
	// SlackUserField that captures the username string (not user_id) from auth.test.
	// callJSONRaw gives us the raw bytes so we can decode into our own struct.
	raw, err := c.callJSONRaw(ctx, "auth.test", nil)
	if err != nil {
		return "", err
	}
	var resp struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error,omitempty"`
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("slack: decode auth.test user_id: %w", err)
	}
	if !resp.OK {
		return "", &SlackAPIError{Method: "auth.test", Code: resp.Error}
	}
	return resp.UserID, nil
}

// callJSONRaw is like callJSON but returns the raw response body bytes instead
// of a decoded SlackAPIResponse. Used by methods that need fields beyond the
// standard OK/Error envelope (e.g. AuthTestWithUserID needing user_id).
// Transport and non-2xx HTTP errors are returned as errors; Slack ok=false is
// NOT checked here — callers decode and check the ok field themselves.
func (c *Client) callJSONRaw(ctx context.Context, method string, body any) ([]byte, error) {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("slack: marshal %s: %w", method, err)
		}
		payload = b
	}
	newReq := func() (*http.Request, error) {
		var rdr io.Reader
		if payload != nil {
			rdr = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/"+method, rdr)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.botToken)
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		return req, nil
	}
	return c.callRaw(ctx, method, newReq)
}

// PostMessage posts to channel with the bold-header format from CONTEXT.md.
// An empty subject renders the body alone (no bold header) — useful for
// per-sandbox threaded replies where the channel already conveys context.
// Returns the message ts on success.
func (c *Client) PostMessage(ctx context.Context, channel, subject, body, threadTS string) (string, error) {
	text := body
	if subject != "" {
		text = fmt.Sprintf("*%s*\n\n%s", subject, body)
	}
	payload := map[string]any{
		"channel":      channel,
		"text":         text,
		"unfurl_links": false,
		"unfurl_media": false,
		"mrkdwn":       true,
	}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}
	resp, err := c.callJSON(ctx, "chat.postMessage", payload)
	if err != nil {
		return "", err
	}
	return resp.TS, nil
}

// GetPermalink returns a Slack permalink URL for the given channel + message ts.
// Wraps chat.getPermalink. Phase 70 — used by Plan 70-06 cross-agent switch
// to embed permalinks in handoff posts.
//
// Uses GET with query-string args (NOT POST + JSON like the other methods on this
// client): chat.getPermalink is one of Slack's older read-only methods that
// silently returns an empty permalink when given an application/json body. Matches
// the slack-go SDK convention and the SlackPosterAdapter equivalent in pkg/slack/bridge.
func (c *Client) GetPermalink(ctx context.Context, channel, messageTS string) (string, error) {
	q := url.Values{}
	q.Set("channel", channel)
	q.Set("message_ts", messageTS)

	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/chat.getPermalink?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.botToken)

	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer httpResp.Body.Close()

	var apiResp SlackAPIResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&apiResp); err != nil {
		return "", fmt.Errorf("slack: decode chat.getPermalink: %w", err)
	}
	if !apiResp.OK {
		return "", &SlackAPIError{Method: "chat.getPermalink", Code: apiResp.Error}
	}
	return apiResp.Permalink, nil
}

// UpdateMessage edits a previously-posted bot message. Subject to Slack's
// 10-minute edit window for bot messages. Phase 70 — used by Plan 70-06's
// optional handoff-edit path.
func (c *Client) UpdateMessage(ctx context.Context, channel, ts, text string) (string, error) {
	payload := map[string]any{
		"channel": channel,
		"ts":      ts,
		"text":    text,
	}
	resp, err := c.callJSON(ctx, "chat.update", payload)
	if err != nil {
		return "", err
	}
	return resp.TS, nil
}

// UploadFileResult carries Slack's response identifiers from the 3-step file
// upload flow (files.getUploadURLExternal + completeUploadExternal).
type UploadFileResult struct {
	FileID    string
	Permalink string
}

// UploadFile uploads a file to Slack via the 3-step flow that replaced the
// deprecated files.upload endpoint:
//
//  1. POST files.getUploadURLExternal → returns upload_url + file_id.
//  2. PUT bytes to upload_url with explicit Content-Length (streamed; Slack
//     rejects chunked transfer-encoding).
//  3. POST files.completeUploadExternal with channel_id (and thread_ts when
//     non-empty — Slack rejects "" for thread_ts).
//
// body is streamed directly to Slack — no full-buffering of file content into
// memory. Peak bridge memory stays at the Go HTTP client baseline regardless
// of upload size. threadTS may be ""; when set the file appears in the thread.
//
// Failures at any step return descriptive errors identifying which step failed.
// Callers (km-slack via the bridge) handle retry policy via BridgeBackoff at
// the envelope level — UploadFile itself does NOT retry.
func (c *Client) UploadFile(ctx context.Context, channel, threadTS, filename, contentType string, sizeBytes int64, body io.Reader) (*UploadFileResult, error) {
	if channel == "" || filename == "" || sizeBytes <= 0 {
		return nil, fmt.Errorf("slack: UploadFile invalid args (channel=%q filename=%q size=%d)", channel, filename, sizeBytes)
	}

	// Step 1: files.getUploadURLExternal — application/x-www-form-urlencoded.
	step1Form := url.Values{}
	step1Form.Set("filename", filename)
	step1Form.Set("length", strconv.FormatInt(sizeBytes, 10))
	step1URL := c.baseURL + "/files.getUploadURLExternal"
	req1, err := http.NewRequestWithContext(ctx, "POST", step1URL, strings.NewReader(step1Form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("slack: files.getUploadURLExternal: build req: %w", err)
	}
	req1.Header.Set("Authorization", "Bearer "+c.botToken)
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp1, err := c.httpClient.Do(req1)
	if err != nil {
		return nil, fmt.Errorf("slack: files.getUploadURLExternal: %w", err)
	}
	defer resp1.Body.Close()
	var s1 struct {
		OK        bool   `json:"ok"`
		Error     string `json:"error,omitempty"`
		UploadURL string `json:"upload_url"`
		FileID    string `json:"file_id"`
	}
	if err := json.NewDecoder(resp1.Body).Decode(&s1); err != nil {
		return nil, fmt.Errorf("slack: files.getUploadURLExternal: decode: %w", err)
	}
	if !s1.OK {
		return nil, fmt.Errorf("slack: files.getUploadURLExternal: %s", s1.Error)
	}

	// Step 2: PUT bytes (streamed). Set ContentLength explicitly so the Go
	// HTTP client uses Content-Length framing rather than chunked encoding,
	// which Slack rejects for these signed upload URLs.
	req2, err := http.NewRequestWithContext(ctx, "PUT", s1.UploadURL, body)
	if err != nil {
		return nil, fmt.Errorf("slack: PUT upload: build req: %w", err)
	}
	req2.ContentLength = sizeBytes
	req2.Header.Set("Content-Type", contentType)
	resp2, err := c.httpClient.Do(req2)
	if err != nil {
		return nil, fmt.Errorf("slack: PUT upload: %w", err)
	}
	io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
		return nil, fmt.Errorf("slack: PUT upload: status %d", resp2.StatusCode)
	}

	// Step 3: files.completeUploadExternal — application/json.
	step3Body := map[string]any{
		"files": []map[string]string{
			{"id": s1.FileID, "title": filename},
		},
		"channel_id": channel,
	}
	if threadTS != "" {
		step3Body["thread_ts"] = threadTS
	}
	step3JSON, err := json.Marshal(step3Body)
	if err != nil {
		return nil, fmt.Errorf("slack: files.completeUploadExternal: marshal: %w", err)
	}
	step3URL := c.baseURL + "/files.completeUploadExternal"
	req3, err := http.NewRequestWithContext(ctx, "POST", step3URL, bytes.NewReader(step3JSON))
	if err != nil {
		return nil, fmt.Errorf("slack: files.completeUploadExternal: build req: %w", err)
	}
	req3.Header.Set("Authorization", "Bearer "+c.botToken)
	req3.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp3, err := c.httpClient.Do(req3)
	if err != nil {
		return nil, fmt.Errorf("slack: files.completeUploadExternal: %w", err)
	}
	defer resp3.Body.Close()
	var s3 struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
		Files []struct {
			ID        string `json:"id"`
			Permalink string `json:"permalink"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp3.Body).Decode(&s3); err != nil {
		return nil, fmt.Errorf("slack: files.completeUploadExternal: decode: %w", err)
	}
	if !s3.OK {
		return nil, fmt.Errorf("slack: files.completeUploadExternal: %s", s3.Error)
	}
	res := &UploadFileResult{FileID: s1.FileID}
	if len(s3.Files) > 0 {
		res.Permalink = s3.Files[0].Permalink
	}
	return res, nil
}

// CreateChannel calls conversations.create. Slack returns the new channel ID.
func (c *Client) CreateChannel(ctx context.Context, name string) (string, error) {
	resp, err := c.callJSON(ctx, "conversations.create", map[string]any{
		"name":       name,
		"is_private": false,
	})
	if err != nil {
		return "", err
	}
	return resp.Channel.ID, nil
}

// JoinChannel calls conversations.join. Idempotent: Slack returns ok=true
// when the bot is already a member, so callers can call this unconditionally
// without checking membership first.
//
// Used by km slack init to guarantee the bot is in the shared channel after
// name_taken recovery — Slack app reinstalls drop bots out of channels they
// previously joined, so a channel ID stored from a prior install can become
// stale. Without this, chat.postMessage from the bridge fails with
// not_in_channel even though the channel exists and the bot has chat:write.
//
// Requires the bot's `channels:join` scope (default for most Slack apps but
// not guaranteed). Slack returns "missing_scope" via SlackAPIError if not
// granted.
func (c *Client) JoinChannel(ctx context.Context, channelID string) error {
	_, err := c.callJSON(ctx, "conversations.join", map[string]any{
		"channel": channelID,
	})
	return err
}

// FindChannelByName scans public channels via conversations.list and returns
// the first channel whose name exactly matches. Returns ("", nil) if no
// match exists (caller decides whether that's an error). Errors are returned
// untouched so callers can inspect SlackAPIError for missing_scope etc.
//
// Used by km slack init to recover from CreateChannel's name_taken — the
// channel exists in Slack but its ID isn't in SSM (e.g. fresh install after
// km unbootstrap, or first run on a new operator workstation).
//
// Requires the bot's `channels:read` scope. Slack returns "missing_scope" via
// SlackAPIError if the bot wasn't granted it; the caller should surface that
// as actionable guidance.
//
// Archived channels are excluded by default — name_taken on a name reserved
// by an archived channel is a Slack-side 30-day reservation that no API call
// can clear, so reuse of the archived ID isn't viable; the operator must
// pick a new name or wait out the reservation.
func (c *Client) FindChannelByName(ctx context.Context, name string) (string, error) {
	cursor := ""
	for {
		body := map[string]any{
			"types": "public_channel",
			// Slack max page size. conversations.list returns channels in roughly
			// creation order, so a freshly-created per-sandbox channel sorts LAST —
			// the scan may have to walk EVERY page to find it. Use the max page
			// size (1000, not 200) to minimise the number of Tier-2 calls and the
			// rate-limit exposure; callRaw backs off if Slack throttles anyway.
			"limit":            1000,
			"exclude_archived": true,
		}
		if cursor != "" {
			body["cursor"] = cursor
		}
		resp, err := c.callJSON(ctx, "conversations.list", body)
		if err != nil {
			return "", err
		}
		for _, ch := range resp.Channels {
			if ch.Name == name {
				return ch.ID, nil
			}
		}
		if resp.ResponseMetadata.NextCursor == "" {
			return "", nil
		}
		cursor = resp.ResponseMetadata.NextCursor
	}
}

// LookupUserByEmail wraps users.lookupByEmail. Returns (id, true, nil) on hit;
// ("", false, nil) on users_not_found (typed boolean miss); ("", false, *SlackAPIError)
// on any other Slack error including missing_scope.
//
// Requires the bot's users:read.email scope. When the scope is absent Slack
// returns missing_scope; callers (km slack init) should surface that with an
// actionable remediation pointing at km slack manifest. Phase 72 adds the
// scope to the manifest template; existing PoC installs need a one-time
// reinstall + token rotation (see docs/slack-notifications.md).
//
// Email is lowercased before sending — Slack matches on the email Slack stores
// in the user profile, which is case-sensitive in some scenarios.
//
// Rate limit: Tier 3 (50+/min). One call per email; no batch.
func (c *Client) LookupUserByEmail(ctx context.Context, email string) (string, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	// users.lookupByEmail is a legacy method that rejects application/json
	// with invalid_arguments — it only accepts form-urlencoded bodies.
	resp, err := c.callForm(ctx, "users.lookupByEmail", url.Values{"email": {email}})
	if err != nil {
		var apierr *SlackAPIError
		if errors.As(err, &apierr) && apierr.Code == "users_not_found" {
			return "", false, nil
		}
		return "", false, err
	}
	return resp.User.ID, true, nil
}

// InviteUserToChannel wraps conversations.invite for a single user.
// Idempotent: treats already_in_channel as success (matching JoinChannel's
// contract). All other Slack errors (cant_invite_self, user_is_restricted,
// not_in_channel, missing_scope, etc.) are surfaced as *SlackAPIError so
// callers can present audience-specific guidance.
//
// Single-user invocation only; bulk invites are explicitly deferred (see
// CONTEXT.md). The Slack API supports a comma-list of up to 100 user IDs,
// but Phase 72 wires this method as a one-at-a-time primitive.
//
// Bot scopes required (already in km Slack App): channels:manage (public),
// groups:write (private). Phase 72 adds users:read.email for the lookup
// step that produces userID.
//
// Common errors:
//   - already_in_channel:  TREATED AS SUCCESS (returns nil)
//   - not_in_channel:      bot is not in target channel — caller must JoinChannel first
//   - cant_invite_self:    bot trying to invite itself
//   - user_is_restricted:  target is a Slack guest — caller may want a typed warning
//   - missing_scope:       install drift — surface remediation
//   - channel_not_found / user_not_found: invalid IDs
//
// Rate limit: Tier 3 (50+/min).
func (c *Client) InviteUserToChannel(ctx context.Context, channelID, userID string) error {
	_, err := c.callJSON(ctx, "conversations.invite", map[string]any{
		"channel": channelID,
		"users":   userID, // Slack accepts single ID or comma-list; single is fine.
	})
	if err != nil {
		var apierr *SlackAPIError
		if errors.As(err, &apierr) && apierr.Code == "already_in_channel" {
			return nil
		}
		return err
	}
	return nil
}

// ErrAlreadyInChannel is returned by InviteUserToChannelStrict when Slack
// responds with "already_in_channel". Public callers prefer the idempotent
// InviteUserToChannel, which swallows this signal as nil. The Strict variant
// exists so the EnsureMemberByEmail orchestrator (pkg/slack/invite.go) can
// distinguish "we just invited them" (return InvitedDirect) from "they were
// already in" (return AlreadyMember).
//
// Use errors.Is(err, ErrAlreadyInChannel) to detect.
var ErrAlreadyInChannel = errors.New("slack: user already in channel")

// InviteUserToChannelStrict wraps conversations.invite without the idempotent
// already_in_channel swallow. On already_in_channel returns ErrAlreadyInChannel
// (use errors.Is); on any other Slack error returns *SlackAPIError; on success
// returns nil.
//
// Prefer InviteUserToChannel for general use; this strict variant exists for
// orchestrators that need to differentiate "freshly invited" from "no-op".
func (c *Client) InviteUserToChannelStrict(ctx context.Context, channelID, userID string) error {
	_, err := c.callJSON(ctx, "conversations.invite", map[string]any{
		"channel": channelID,
		"users":   userID,
	})
	if err != nil {
		var apierr *SlackAPIError
		if errors.As(err, &apierr) && apierr.Code == "already_in_channel" {
			return ErrAlreadyInChannel
		}
		return err
	}
	return nil
}

// InviteShared sends a Slack Connect invite to email.
func (c *Client) InviteShared(ctx context.Context, channelID, email string) error {
	_, err := c.callJSON(ctx, "conversations.inviteShared", map[string]any{
		"channel":          channelID,
		"emails":           []string{email},
		"external_limited": true,
	})
	return err
}

// ChannelInfo returns the channel's member count and whether the bot itself is
// a member (is_member field). Used by km create override-mode validation to
// give early feedback before infra is provisioned.
func (c *Client) ChannelInfo(ctx context.Context, channelID string) (int, bool, error) {
	resp, err := c.callJSON(ctx, "conversations.info", map[string]any{
		"channel":             channelID,
		"include_num_members": true,
	})
	if err != nil {
		return 0, false, err
	}
	return resp.Channel.NumMembers, resp.Channel.IsMember, nil
}

// ArchiveChannel calls conversations.archive.
func (c *Client) ArchiveChannel(ctx context.Context, channelID string) error {
	_, err := c.callJSON(ctx, "conversations.archive", map[string]any{
		"channel": channelID,
	})
	return err
}

// PostResponse is the bridge Lambda's 200-path response shape.
type PostResponse struct {
	OK        bool   `json:"ok"`
	TS        string `json:"ts,omitempty"`
	Error     string `json:"error,omitempty"`
	Permalink string `json:"permalink,omitempty"` // Phase 70 — populated by ActionPermalink response
}

// BridgeBackoff is the retry schedule for network-level errors in PostToBridge.
// Exposed for tests so they can shrink it to milliseconds.
// Note: 5xx HTTP responses are NOT retried (see PostToBridge for rationale).
var BridgeBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// PostToBridge submits a signed envelope to the bridge Lambda Function URL.
//
// Retry policy:
//   - Network errors (pre-HTTP, connection refused, timeout) → retry per BridgeBackoff.
//   - 4xx responses → fail fast (same nonce must not be reused).
//   - 5xx responses → fail fast (same rationale: the bridge has already reserved the
//     nonce in DynamoDB; retrying the same envelope triggers "replayed_nonce" 401 and
//     masks the real upstream error from the operator).
//
// Callers that need idempotent retry on transient bridge errors should build a
// fresh envelope (new nonce) and call PostToBridge again.
func PostToBridge(ctx context.Context, bridgeURL string, env *SlackEnvelope, sig []byte) (*PostResponse, error) {
	canonical, err := CanonicalJSON(env)
	if err != nil {
		return nil, err
	}
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	var lastErr error
	attempts := len(BridgeBackoff) + 1
	for attempt := 0; attempt < attempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt > 0 {
			// Sleep before retry; honor ctx during sleep.
			t := time.NewTimer(BridgeBackoff[attempt-1])
			select {
			case <-ctx.Done():
				t.Stop()
				return nil, ctx.Err()
			case <-t.C:
			}
		}
		req, err := http.NewRequestWithContext(ctx, "POST", bridgeURL, bytes.NewReader(canonical))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-KM-Sender-ID", env.SenderID)
		req.Header.Set("X-KM-Signature", sigB64)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			// Check ctx before retrying
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue // network error: retry
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			var pr PostResponse
			if jerr := json.Unmarshal(body, &pr); jerr != nil {
				return nil, fmt.Errorf("slack: bridge decode: %w", jerr)
			}
			return &pr, nil
		}
		// 4xx or 5xx: fail fast — do NOT retry.
		// For 5xx: the bridge has already reserved the nonce; retrying the same
		// envelope causes "replayed_nonce" 401, masking the real Slack error.
		return nil, fmt.Errorf("slack: bridge returned %d: %s",
			resp.StatusCode, string(body))
	}
	if lastErr == nil {
		lastErr = errors.New("slack: bridge unreachable after retries")
	}
	return nil, lastErr
}
