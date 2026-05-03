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
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	TS      string `json:"ts,omitempty"`
	Channel struct {
		ID         string `json:"id"`
		IsMember   bool   `json:"is_member"`
		NumMembers int    `json:"num_members"`
	} `json:"channel,omitempty"`
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

// callJSON is the shared JSON-body method dispatcher.
func (c *Client) callJSON(ctx context.Context, method string, body any) (*SlackAPIResponse, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("slack: marshal %s: %w", method, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/"+method, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.botToken)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var apiResp SlackAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("slack: decode %s: %w", method, err)
	}
	if !apiResp.OK {
		return &apiResp, &SlackAPIError{Method: method, Code: apiResp.Error}
	}
	return &apiResp, nil
}

// AuthTest validates the bot token. Used by km slack init and km doctor.
func (c *Client) AuthTest(ctx context.Context) error {
	_, err := c.callJSON(ctx, "auth.test", nil)
	return err
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
	OK    bool   `json:"ok"`
	TS    string `json:"ts,omitempty"`
	Error string `json:"error,omitempty"`
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
