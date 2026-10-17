package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// SlackReplyMessage is the subset of a conversations.replies message the
// context builder needs.
type SlackReplyMessage struct {
	User    string      `json:"user,omitempty"`
	BotID   string      `json:"bot_id,omitempty"`
	Subtype string      `json:"subtype,omitempty"`
	Text    string      `json:"text,omitempty"`
	TS      string      `json:"ts"`
	Files   []SlackFile `json:"files,omitempty"`
}

// SlackThreadReader fetches a thread's replies newer than oldest (exclusive).
type SlackThreadReader interface {
	Replies(ctx context.Context, channel, threadTS, oldest string) ([]SlackReplyMessage, error)
}

// SlackThreadReaderAdapter calls conversations.replies with the bot token.
// Single page, limit 200 — MaxContextMessages (100) fits. Scopes:
// channels:history / groups:history / im:history / mpim:history.
type SlackThreadReaderAdapter struct {
	HTTPClient *http.Client
	BaseURL    string          // defaults to "https://slack.com/api"; tests override
	Tokens     BotTokenFetcher // shared with the other Slack adapters
}

func (a *SlackThreadReaderAdapter) baseURL() string {
	if a.BaseURL != "" {
		return a.BaseURL
	}
	return "https://slack.com/api"
}

// Replies returns the thread's messages (root included) newer than oldest.
// A Slack ok:false is surfaced as an error naming Slack's error string.
func (a *SlackThreadReaderAdapter) Replies(ctx context.Context, channel, threadTS, oldest string) ([]SlackReplyMessage, error) {
	token, err := a.Tokens.Fetch(ctx)
	if err != nil {
		return nil, fmt.Errorf("conversations.replies: fetch bot token: %w", err)
	}
	form := url.Values{"channel": {channel}, "ts": {threadTS}, "limit": {"200"}}
	if oldest != "" {
		form.Set("oldest", oldest)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL()+"/conversations.replies", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("conversations.replies: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	hc := a.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("conversations.replies: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		OK       bool                `json:"ok"`
		Error    string              `json:"error,omitempty"`
		Messages []SlackReplyMessage `json:"messages"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("conversations.replies: decode (http %d): %w", resp.StatusCode, err)
	}
	if !out.OK {
		return nil, fmt.Errorf("conversations.replies: slack error %q (http %d)", out.Error, resp.StatusCode)
	}
	return out.Messages, nil
}
