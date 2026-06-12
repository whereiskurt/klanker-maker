package github_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github"
)

// TestTurnMarker covers the hidden HTML-comment idempotency marker format. The
// marker is appended (invisibly) to agent-posted bodies so a re-post of the same
// turn can be detected. An empty turn id yields an empty marker (no marker).
func TestTurnMarker(t *testing.T) {
	if got := github.TurnMarker("ABC123"); got != "<!-- km-turn:ABC123 -->" {
		t.Errorf("TurnMarker(ABC123) = %q; want %q", got, "<!-- km-turn:ABC123 -->")
	}
	if got := github.TurnMarker(""); got != "" {
		t.Errorf("TurnMarker(\"\") = %q; want empty", got)
	}
}

// TestCommentMarkerExists_Found verifies the duplicate check returns true when an
// existing issue comment body already carries the marker, and that it queries the
// issue-comments endpoint.
func TestCommentMarkerExists_Found(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"body":"unrelated"},{"body":"my review\n\n<!-- km-turn:ABC -->"}]`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	exists, err := github.CommentMarkerExists(context.Background(), "owner/repo", 42, "tok", "<!-- km-turn:ABC -->")
	if err != nil {
		t.Fatalf("CommentMarkerExists err = %v; want nil", err)
	}
	if !exists {
		t.Errorf("CommentMarkerExists = false; want true (marker present)")
	}
	if capturedPath != "/repos/owner/repo/issues/42/comments" {
		t.Errorf("path = %q; want /repos/owner/repo/issues/42/comments", capturedPath)
	}
}

// TestCommentMarkerExists_NotFound verifies the check returns false when no
// existing comment carries the marker.
func TestCommentMarkerExists_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"body":"nothing here"}]`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	exists, err := github.CommentMarkerExists(context.Background(), "owner/repo", 42, "tok", "<!-- km-turn:ABC -->")
	if err != nil {
		t.Fatalf("CommentMarkerExists err = %v; want nil", err)
	}
	if exists {
		t.Errorf("CommentMarkerExists = true; want false (marker absent)")
	}
}

// TestCommentMarkerExists_Paginated verifies the check follows the Link rel="next"
// header so a just-posted comment on a later page is still found.
func TestCommentMarkerExists_Paginated(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"body":"tail <!-- km-turn:ABC -->"}]`))
			return
		}
		// First page: no marker, but advertise a next page.
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos/owner/repo/issues/42/comments?page=2>; rel="next"`, srv.URL))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"body":"head"}]`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	exists, err := github.CommentMarkerExists(context.Background(), "owner/repo", 42, "tok", "<!-- km-turn:ABC -->")
	if err != nil {
		t.Fatalf("CommentMarkerExists err = %v; want nil", err)
	}
	if !exists {
		t.Errorf("CommentMarkerExists = false; want true (marker on page 2)")
	}
}

// TestCommentMarkerExists_ErrorFailsOpen verifies a non-2xx response surfaces as a
// non-nil error so the caller can fail open (post anyway).
func TestCommentMarkerExists_ErrorFailsOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	exists, err := github.CommentMarkerExists(context.Background(), "owner/repo", 42, "tok", "<!-- km-turn:ABC -->")
	if err == nil {
		t.Errorf("CommentMarkerExists err = nil; want non-nil on HTTP 500")
	}
	if exists {
		t.Errorf("CommentMarkerExists = true on error; want false")
	}
}

// TestReviewMarkerExists_Found verifies the review-side duplicate check queries the
// pull-request reviews endpoint and detects an existing marker.
func TestReviewMarkerExists_Found(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"body":"approved\n\n<!-- km-turn:XYZ -->"}]`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	exists, err := github.ReviewMarkerExists(context.Background(), "owner/repo", 7, "tok", "<!-- km-turn:XYZ -->")
	if err != nil {
		t.Fatalf("ReviewMarkerExists err = %v; want nil", err)
	}
	if !exists {
		t.Errorf("ReviewMarkerExists = false; want true")
	}
	if capturedPath != "/repos/owner/repo/pulls/7/reviews" {
		t.Errorf("path = %q; want /repos/owner/repo/pulls/7/reviews", capturedPath)
	}
}

// TestReviewMarkerExists_NotFound verifies false when no review carries the marker.
func TestReviewMarkerExists_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	exists, err := github.ReviewMarkerExists(context.Background(), "owner/repo", 7, "tok", "<!-- km-turn:XYZ -->")
	if err != nil {
		t.Fatalf("ReviewMarkerExists err = %v; want nil", err)
	}
	if exists {
		t.Errorf("ReviewMarkerExists = true; want false")
	}
}
