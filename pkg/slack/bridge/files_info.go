package bridge

// files_info.go — Phase 75.1 SlackFilesInfoAdapter.
//
// Modern Slack Apps receive stub file objects in file_share event payloads:
// only `id` is guaranteed. To download a file the bridge must call
// https://slack.com/api/files.info first to retrieve url_private_download,
// name, mimetype, and size. This adapter implements FilesInfoFetcher.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// SlackFilesInfoAdapter implements FilesInfoFetcher via direct HTTP to
// Slack's files.info Web API. Mirrors SlackPosterAdapter / SlackReactorAdapter
// shape for consistency. Shares HTTPClient + Tokens with the rest of the
// bridge so the 15-min SSM token cache and the redirect-disabled client
// are reused (no extra cold-start cost).
type SlackFilesInfoAdapter struct {
	HTTPClient *http.Client
	BaseURL    string          // defaults to "https://slack.com/api"; tests override
	Tokens     BotTokenFetcher // shared with poster/reactor for token-cache reuse
}

func (a *SlackFilesInfoAdapter) getBaseURL() string {
	if a.BaseURL != "" {
		return a.BaseURL
	}
	return "https://slack.com/api"
}

// filesInfoResponse is the subset of the files.info response we consume.
// Slack returns many more fields (timestamps, thumbnails, shares, etc.) — Go's
// json package silently ignores unknown keys, so this minimal shape is safe.
type filesInfoResponse struct {
	OK    bool      `json:"ok"`
	Error string    `json:"error,omitempty"`
	File  SlackFile `json:"file"`
}

// FilesInfo calls Slack files.info for the given file ID and returns the
// enriched SlackFile. Returns a wrapped error on network failure, non-200
// HTTP status, JSON decode failure, or `ok:false` from Slack.
func (a *SlackFilesInfoAdapter) FilesInfo(ctx context.Context, fileID string) (SlackFile, error) {
	if fileID == "" {
		return SlackFile{}, fmt.Errorf("files.info: empty file ID")
	}

	token, err := a.Tokens.Fetch(ctx)
	if err != nil {
		return SlackFile{}, fmt.Errorf("files.info: fetch bot token: %w", err)
	}

	form := url.Values{}
	form.Set("file", fileID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.getBaseURL()+"/files.info", strings.NewReader(form.Encode()))
	if err != nil {
		return SlackFile{}, fmt.Errorf("files.info: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")

	hc := a.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return SlackFile{}, fmt.Errorf("files.info: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return SlackFile{}, fmt.Errorf("files.info: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return SlackFile{}, fmt.Errorf("files.info: HTTP %d", resp.StatusCode)
	}
	var parsed filesInfoResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return SlackFile{}, fmt.Errorf("files.info: decode: %w", err)
	}
	if !parsed.OK {
		return SlackFile{}, fmt.Errorf("files.info: slack error: %s", parsed.Error)
	}
	return parsed.File, nil
}
