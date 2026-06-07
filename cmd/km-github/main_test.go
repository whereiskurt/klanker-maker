package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github"
)

// TestDispatch_NoArgs verifies that dispatch with no args returns exit code 2.
func TestDispatch_NoArgs(t *testing.T) {
	code := dispatch(nil, io.Discard)
	if code != 2 {
		t.Errorf("dispatch(nil) = %d; want 2", code)
	}
}

// TestDispatch_UnknownSubcommand verifies that an unknown subcommand returns exit code 2.
func TestDispatch_UnknownSubcommand(t *testing.T) {
	code := dispatch([]string{"notaverb"}, io.Discard)
	if code != 2 {
		t.Errorf("dispatch(notaverb) = %d; want 2", code)
	}
}

// TestDispatch_Help verifies that -h / --help / help subcommands return 0.
func TestDispatch_Help(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		code := dispatch([]string{arg}, io.Discard)
		if code != 0 {
			t.Errorf("dispatch(%q) = %d; want 0", arg, code)
		}
	}
}

// TestRunComment_RequestShape verifies that "km-github comment" sends a POST to
// /repos/{owner}/{repo}/issues/{N}/comments with the correct body and headers.
func TestRunComment_RequestShape(t *testing.T) {
	var (
		capturedMethod  string
		capturedPath    string
		capturedBody    map[string]any
		capturedAuth    string
		capturedAccept  string
		capturedVersion string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")
		capturedAccept = r.Header.Get("Accept")
		capturedVersion = r.Header.Get("X-GitHub-Api-Version")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1,"body":"ok"}`))
	}))
	defer srv.Close()

	// Override the base URL so the binary hits our httptest server.
	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	code := runCommentWith("owner/repo", 42, "test comment body", "test-token", io.Discard)
	if code != 0 {
		t.Fatalf("runCommentWith returned %d; want 0", code)
	}

	if capturedMethod != http.MethodPost {
		t.Errorf("method = %q; want POST", capturedMethod)
	}
	wantPath := "/repos/owner/repo/issues/42/comments"
	if capturedPath != wantPath {
		t.Errorf("path = %q; want %q", capturedPath, wantPath)
	}
	if capturedAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q; want %q", capturedAuth, "Bearer test-token")
	}
	if capturedAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q; want %q", capturedAccept, "application/vnd.github+json")
	}
	if capturedVersion != "2022-11-28" {
		t.Errorf("X-GitHub-Api-Version = %q; want 2022-11-28", capturedVersion)
	}
	if capturedBody["body"] != "test comment body" {
		t.Errorf("body.body = %v; want %q", capturedBody["body"], "test comment body")
	}
}

// TestRunReview_RequestShape verifies that "km-github review" sends a POST to
// /repos/{owner}/{repo}/pulls/{N}/reviews with the correct body and headers.
func TestRunReview_RequestShape(t *testing.T) {
	var (
		capturedMethod string
		capturedPath   string
		capturedBody   map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	code := runReviewWith("owner/repo", 7, "COMMENT", "LGTM in general", "", "test-token", io.Discard)
	if code != 0 {
		t.Fatalf("runReviewWith returned %d; want 0", code)
	}

	if capturedMethod != http.MethodPost {
		t.Errorf("method = %q; want POST", capturedMethod)
	}
	wantPath := "/repos/owner/repo/pulls/7/reviews"
	if capturedPath != wantPath {
		t.Errorf("path = %q; want %q", capturedPath, wantPath)
	}
	if capturedBody["event"] != "COMMENT" {
		t.Errorf("body.event = %v; want COMMENT", capturedBody["event"])
	}
	if capturedBody["body"] != "LGTM in general" {
		t.Errorf("body.body = %v; want LGTM", capturedBody["body"])
	}
}

// TestRunReview_ApproveEvent verifies that APPROVE is a valid event and doesn't require body.
func TestRunReview_ApproveEvent(t *testing.T) {
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":2}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	// APPROVE without a body should succeed.
	code := runReviewWith("org/repo", 1, "APPROVE", "", "", "test-token", io.Discard)
	if code != 0 {
		t.Fatalf("runReviewWith(APPROVE) returned %d; want 0", code)
	}
	if capturedBody["event"] != "APPROVE" {
		t.Errorf("body.event = %v; want APPROVE", capturedBody["event"])
	}
}

// TestRunReview_RequestChanges verifies REQUEST_CHANGES requires a body.
func TestRunReview_RequestChanges(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":3}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	// REQUEST_CHANGES without body → error.
	var stderr strings.Builder
	code := runReviewWith("org/repo", 1, "REQUEST_CHANGES", "", "", "test-token", &stderr)
	if code == 0 {
		t.Fatalf("runReviewWith(REQUEST_CHANGES, emptyBody) should fail but returned 0")
	}
	if !strings.Contains(stderr.String(), "body") {
		t.Errorf("stderr should mention 'body' requirement; got: %q", stderr.String())
	}
}

// TestRunReview_CommentRequiresBody verifies COMMENT requires a non-empty body.
func TestRunReview_CommentRequiresBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":4}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	var stderr strings.Builder
	code := runReviewWith("org/repo", 1, "COMMENT", "", "", "test-token", &stderr)
	if code == 0 {
		t.Fatalf("runReviewWith(COMMENT, emptyBody) should fail but returned 0")
	}
}

// TestRunReview_InvalidEvent verifies that an invalid event returns a non-zero exit code.
func TestRunReview_InvalidEvent(t *testing.T) {
	var stderr strings.Builder
	code := runReviewWith("org/repo", 1, "MERGE", "some body", "", "test-token", &stderr)
	if code == 0 {
		t.Fatalf("runReviewWith(MERGE) should fail but returned 0")
	}
	if !strings.Contains(stderr.String(), "APPROVE") {
		t.Errorf("stderr should list valid events; got: %q", stderr.String())
	}
}

// TestRunReview_CommitIDOptional verifies that commit_id is omitted from the request
// body when empty, and included when provided.
func TestRunReview_CommitIDOptional(t *testing.T) {
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":5}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	// Without commit_id.
	capturedBody = nil
	code := runReviewWith("org/repo", 5, "APPROVE", "", "", "test-token", io.Discard)
	if code != 0 {
		t.Fatalf("runReviewWith without commit_id = %d; want 0", code)
	}
	if _, ok := capturedBody["commit_id"]; ok {
		t.Errorf("commit_id should be absent when empty; got body: %v", capturedBody)
	}

	// With commit_id.
	capturedBody = nil
	code = runReviewWith("org/repo", 5, "APPROVE", "", "abc123", "test-token", io.Discard)
	if code != 0 {
		t.Fatalf("runReviewWith with commit_id = %d; want 0", code)
	}
	if capturedBody["commit_id"] != "abc123" {
		t.Errorf("commit_id = %v; want abc123", capturedBody["commit_id"])
	}
}

// TestRunComment_MissingArgs verifies that comment without required flags returns exit 2.
func TestRunComment_MissingArgs(t *testing.T) {
	// No --repo flag.
	code := runComment([]string{"--number", "1", "--body", "hi"}, io.Discard)
	if code != 2 {
		t.Errorf("comment without --repo = %d; want 2", code)
	}

	// No --number flag.
	code = runComment([]string{"--repo", "org/repo", "--body", "hi"}, io.Discard)
	if code != 2 {
		t.Errorf("comment without --number = %d; want 2", code)
	}

	// No --body flag.
	code = runComment([]string{"--repo", "org/repo", "--number", "1"}, io.Discard)
	if code != 2 {
		t.Errorf("comment without --body = %d; want 2", code)
	}
}

// TestRunReview_MissingArgs verifies that review without required flags returns exit 2.
func TestRunReview_MissingArgs(t *testing.T) {
	// No --repo flag.
	code := runReview([]string{"--number", "1", "--event", "APPROVE"}, io.Discard)
	if code != 2 {
		t.Errorf("review without --repo = %d; want 2", code)
	}

	// No --event flag.
	code = runReview([]string{"--repo", "org/repo", "--number", "1", "--body", "ok"}, io.Discard)
	if code != 2 {
		t.Errorf("review without --event = %d; want 2", code)
	}
}
