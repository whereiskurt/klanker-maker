package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ============================================================
// Per-turn idempotency marker (github-bridge double-post fix)
// ============================================================
//
// A single @-mention can drive the sandbox agent to invoke `km-github comment`
// (or `review`) twice with the same body — once to satisfy the poller's hard
// "post your reply (REQUIRED)" mandate, once to satisfy an invoked skill's own
// "post a PR review" instruction (or as a self-retry of a call that secretly
// succeeded). GitHub issue comments and reviews are NOT idempotent, so the
// requester sees two byte-identical posts seconds apart.
//
// The fix is a per-turn idempotency guard at the helper layer (the single
// chokepoint every post flows through): embed an invisible HTML-comment marker
// keyed to the current turn in every posted body, and before posting, scan the
// existing comments/reviews for that same marker. If it is already present, the
// post is a duplicate and is suppressed. The marker is per-turn (keyed on the
// poller's RUN_ID via KM_GITHUB_TURN_ID), so two *separate* legitimate mentions
// on the same PR still each post.

// TurnMarker returns the hidden HTML-comment idempotency marker for a turn id.
// It is appended (invisibly — HTML comments do not render in GitHub markdown) to
// agent-posted comment/review bodies so a re-post of the same turn can be
// detected and suppressed. An empty turnID yields an empty marker (feature off).
func TurnMarker(turnID string) string {
	if turnID == "" {
		return ""
	}
	return fmt.Sprintf("<!-- km-turn:%s -->", turnID)
}

// CommentMarkerExists reports whether any existing comment on issue/PR #number of
// repo ("owner/repo") already contains marker. It paginates the issue-comments
// list (per_page=100, following the Link rel="next" header) so a comment posted
// seconds earlier — which sorts last — is still found.
//
// A transport error or non-2xx response is returned as a non-nil error so the
// caller can fail open (post anyway rather than strand a legitimate reply when a
// read fails). token is the per-sandbox GitHub installation token.
func CommentMarkerExists(ctx context.Context, repo string, number int, token, marker string) (bool, error) {
	url := fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=100", GitHubAPIBaseURL, repo, number)
	return markerExists(ctx, url, token, marker)
}

// ReviewMarkerExists is the pull-request-reviews analog of CommentMarkerExists.
// It scans POST /repos/{owner}/{repo}/pulls/{number}/reviews response bodies for
// marker. Same fail-open contract.
func ReviewMarkerExists(ctx context.Context, repo string, number int, token, marker string) (bool, error) {
	url := fmt.Sprintf("%s/repos/%s/pulls/%d/reviews?per_page=100", GitHubAPIBaseURL, repo, number)
	return markerExists(ctx, url, token, marker)
}

// markerBody is the minimal shape we decode from a comment/review list element —
// only the body field matters for the marker scan.
type markerBody struct {
	Body string `json:"body"`
}

// markerExists GETs firstURL, scans each element's body for marker, and follows
// the Link rel="next" header to the end of the list (or until the marker is
// found). Any non-2xx page returns an error so the caller can fail open.
func markerExists(ctx context.Context, firstURL, token, marker string) (bool, error) {
	url := firstURL
	for url != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return false, fmt.Errorf("github: build marker-check request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false, fmt.Errorf("github: marker-check request: %w", err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return false, fmt.Errorf("github: marker-check returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		if readErr != nil {
			return false, fmt.Errorf("github: read marker-check response: %w", readErr)
		}

		var items []markerBody
		if err := json.Unmarshal(body, &items); err != nil {
			return false, fmt.Errorf("github: unmarshal marker-check response: %w", err)
		}
		for _, it := range items {
			if strings.Contains(it.Body, marker) {
				return true, nil
			}
		}
		url = parseNextLink(resp.Header.Get("Link"))
	}
	return false, nil
}

// parseNextLink extracts the rel="next" URL from an RFC 5988 Link header, or ""
// when there is no next page. Example header:
//
//	<https://api.github.com/...&page=2>; rel="next", <...&page=9>; rel="last"
func parseNextLink(linkHeader string) string {
	if linkHeader == "" {
		return ""
	}
	for _, part := range strings.Split(linkHeader, ",") {
		segments := strings.Split(part, ";")
		if len(segments) < 2 {
			continue
		}
		isNext := false
		for _, seg := range segments[1:] {
			if strings.Contains(seg, `rel="next"`) {
				isNext = true
				break
			}
		}
		if !isNext {
			continue
		}
		urlPart := strings.TrimSpace(segments[0])
		urlPart = strings.TrimPrefix(urlPart, "<")
		urlPart = strings.TrimSuffix(urlPart, ">")
		return urlPart
	}
	return ""
}
