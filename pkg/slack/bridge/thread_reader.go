package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// SlackReplyMessage is the subset of a conversations.replies message the
// context builder needs.
type SlackReplyMessage struct {
	User     string      `json:"user,omitempty"`
	BotID    string      `json:"bot_id,omitempty"`
	Subtype  string      `json:"subtype,omitempty"`
	Text     string      `json:"text,omitempty"`
	TS       string      `json:"ts"`
	ThreadTS string      `json:"thread_ts,omitempty"`
	Files    []SlackFile `json:"files,omitempty"`
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

// History returns up to limit messages in (oldest, latest) newest-first via
// conversations.history, and whether older ones remain. limit is clamped to
// 1..MaxHistoryPageSize (0 means DefaultHistoryPageSize). A 429 or a
// "ratelimited" reply surfaces as *ErrSlackRateLimited so the caller can wait.
func (a *SlackThreadReaderAdapter) History(ctx context.Context, channel, oldest, latest string, limit int) ([]SlackReplyMessage, bool, error) {
	if limit <= 0 {
		limit = DefaultHistoryPageSize
	}
	if limit > MaxHistoryPageSize {
		limit = MaxHistoryPageSize
	}
	token, err := a.Tokens.Fetch(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("conversations.history: fetch bot token: %w", err)
	}
	form := url.Values{"channel": {channel}, "limit": {strconv.Itoa(limit)}, "inclusive": {"false"}}
	if oldest != "" {
		form.Set("oldest", oldest)
	}
	if latest != "" {
		form.Set("latest", latest)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL()+"/conversations.history", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, false, fmt.Errorf("conversations.history: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	hc := a.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("conversations.history: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		ra, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		if ra <= 0 {
			ra = 60
		}
		return nil, false, &ErrSlackRateLimited{RetryAfterSeconds: ra, Method: "conversations.history"}
	}
	var out struct {
		OK       bool                `json:"ok"`
		Error    string              `json:"error,omitempty"`
		Messages []SlackReplyMessage `json:"messages"`
		HasMore  bool                `json:"has_more"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, false, fmt.Errorf("conversations.history: decode (http %d): %w", resp.StatusCode, err)
	}
	if !out.OK {
		if out.Error == "ratelimited" {
			return nil, false, &ErrSlackRateLimited{RetryAfterSeconds: 60, Method: "conversations.history"}
		}
		return nil, false, fmt.Errorf("conversations.history: slack error %q (http %d)", out.Error, resp.StatusCode)
	}
	return out.Messages, out.HasMore, nil
}
