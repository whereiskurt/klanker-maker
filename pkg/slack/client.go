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
		ID string `json:"id"`
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
// Returns the message ts on success.
func (c *Client) PostMessage(ctx context.Context, channel, subject, body, threadTS string) (string, error) {
	text := fmt.Sprintf("*%s*\n\n%s", subject, body)
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

// BridgeBackoff is the retry schedule used by PostToBridge. Exposed for tests
// so they can shrink it to milliseconds.
var BridgeBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// PostToBridge submits a signed envelope to the bridge Lambda Function URL.
// Retries on 5xx and network errors per BridgeBackoff. Fails fast on 4xx.
// Uses http.DefaultClient for the actual transport (configurable via BridgeBackoff
// for tests; production code uses the default client).
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
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, fmt.Errorf("slack: bridge returned %d: %s",
				resp.StatusCode, string(body))
		}
		// 5xx → retry
		lastErr = fmt.Errorf("slack: bridge %d: %s", resp.StatusCode, string(body))
	}
	if lastErr == nil {
		lastErr = errors.New("slack: bridge unreachable after retries")
	}
	return nil, lastErr
}
