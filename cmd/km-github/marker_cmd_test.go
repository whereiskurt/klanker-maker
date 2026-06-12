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

// TestRunCommentWith_SkipsDuplicate is the core github-bridge double-post fix:
// when an existing issue comment already carries this turn's marker, a second
// km-github comment with the same turn id must NOT POST again — it exits 0 and
// no-ops.
func TestRunCommentWith_SkipsDuplicate(t *testing.T) {
	var postCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"body":"earlier post\n\n<!-- km-turn:ABC -->"}]`))
			return
		}
		postCalled = true
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	code := runCommentWith("owner/repo", 42, "duplicate body", "tok", "ABC", io.Discard)
	if code != 0 {
		t.Fatalf("runCommentWith(dup) = %d; want 0", code)
	}
	if postCalled {
		t.Errorf("POST was called; want suppressed (marker already present)")
	}
}

// TestRunCommentWith_PostsWhenNoDuplicate verifies a fresh turn id posts, and that
// the hidden marker is appended to the body so the next call can detect it.
func TestRunCommentWith_PostsWhenNoDuplicate(t *testing.T) {
	var (
		postCalled   bool
		capturedBody map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		}
		postCalled = true
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	code := runCommentWith("owner/repo", 42, "fresh body", "tok", "ABC", io.Discard)
	if code != 0 {
		t.Fatalf("runCommentWith(fresh) = %d; want 0", code)
	}
	if !postCalled {
		t.Fatalf("POST not called; want posted")
	}
	got, _ := capturedBody["body"].(string)
	if !strings.Contains(got, "fresh body") {
		t.Errorf("posted body = %q; want to contain original text", got)
	}
	if !strings.Contains(got, "<!-- km-turn:ABC -->") {
		t.Errorf("posted body = %q; want to contain turn marker", got)
	}
}

// TestRunCommentWith_FailOpenOnCheckError verifies that when the duplicate-check
// GET errors, the helper proceeds with the POST rather than stranding the reply.
func TestRunCommentWith_FailOpenOnCheckError(t *testing.T) {
	var postCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
			return
		}
		postCalled = true
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	code := runCommentWith("owner/repo", 42, "body", "tok", "ABC", io.Discard)
	if code != 0 {
		t.Fatalf("runCommentWith(check-error) = %d; want 0 (fail-open)", code)
	}
	if !postCalled {
		t.Errorf("POST not called; want fail-open POST despite check error")
	}
}

// TestRunCommentWith_NoTurnID_NoCheck verifies back-compat: with an empty turn id
// (manual km-github use), there is no duplicate-check GET and the body is posted
// byte-identical (no marker appended).
func TestRunCommentWith_NoTurnID_NoCheck(t *testing.T) {
	var (
		getCalled    bool
		capturedBody map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			getCalled = true
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	code := runCommentWith("owner/repo", 42, "manual body", "tok", "", io.Discard)
	if code != 0 {
		t.Fatalf("runCommentWith(no turn) = %d; want 0", code)
	}
	if getCalled {
		t.Errorf("duplicate-check GET was called with empty turn id; want skipped")
	}
	if capturedBody["body"] != "manual body" {
		t.Errorf("body = %v; want byte-identical %q", capturedBody["body"], "manual body")
	}
}

// TestRunReviewWith_SkipsDuplicate verifies the same idempotency guard on the
// review verb: an existing review carrying the turn marker suppresses the POST.
func TestRunReviewWith_SkipsDuplicate(t *testing.T) {
	var postCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"body":"prior review\n\n<!-- km-turn:XYZ -->"}]`))
			return
		}
		postCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	code := runReviewWith("owner/repo", 7, "COMMENT", "review text", "", "tok", "XYZ", io.Discard)
	if code != 0 {
		t.Fatalf("runReviewWith(dup) = %d; want 0", code)
	}
	if postCalled {
		t.Errorf("POST was called; want suppressed (marker already present)")
	}
}

// TestRunReviewWith_PostsWhenNoDuplicate verifies a fresh turn id posts a review
// with the marker appended to the body.
func TestRunReviewWith_PostsWhenNoDuplicate(t *testing.T) {
	var (
		postCalled   bool
		capturedBody map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		}
		postCalled = true
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	code := runReviewWith("owner/repo", 7, "COMMENT", "review text", "", "tok", "XYZ", io.Discard)
	if code != 0 {
		t.Fatalf("runReviewWith(fresh) = %d; want 0", code)
	}
	if !postCalled {
		t.Fatalf("POST not called; want posted")
	}
	got, _ := capturedBody["body"].(string)
	if !strings.Contains(got, "review text") || !strings.Contains(got, "<!-- km-turn:XYZ -->") {
		t.Errorf("posted body = %q; want original text + turn marker", got)
	}
}

// TestRunReviewWith_FailOpenOnCheckError verifies the review verb fails open: a
// failed duplicate-check GET still posts.
func TestRunReviewWith_FailOpenOnCheckError(t *testing.T) {
	var postCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		postCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	code := runReviewWith("owner/repo", 7, "COMMENT", "review text", "", "tok", "XYZ", io.Discard)
	if code != 0 {
		t.Fatalf("runReviewWith(check-error) = %d; want 0 (fail-open)", code)
	}
	if !postCalled {
		t.Errorf("POST not called; want fail-open POST despite check error")
	}
}
