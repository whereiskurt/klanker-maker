// check_test.go — unit tests for the km-github check subcommand.
//
// GREEN: build tag removed in 98-01 after runCheck + runCheckWith implemented in main.go.
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

// TestCheck verifies that runCheckWith POSTs a valid check-run to
// /repos/{owner}/{repo}/check-runs and returns 0 on success.
func TestCheck(t *testing.T) {
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
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	code := runCheckWith(
		"owner/repo",
		7,
		"my-check",
		"success",
		"All checks passed",
		"abc1234567890abc",
		"test-token",
		io.Discard,
	)
	if code != 0 {
		t.Fatalf("runCheckWith returned %d; want 0", code)
	}

	if capturedMethod != http.MethodPost {
		t.Errorf("method = %q; want POST", capturedMethod)
	}
	wantPath := "/repos/owner/repo/check-runs"
	if capturedPath != wantPath {
		t.Errorf("path = %q; want %q", capturedPath, wantPath)
	}

	// Verify required body fields per GitHub Check Runs API.
	if capturedBody["name"] != "my-check" {
		t.Errorf("body.name = %v; want my-check", capturedBody["name"])
	}
	if capturedBody["head_sha"] != "abc1234567890abc" {
		t.Errorf("body.head_sha = %v; want abc1234567890abc", capturedBody["head_sha"])
	}
	if capturedBody["status"] != "completed" {
		t.Errorf("body.status = %v; want completed", capturedBody["status"])
	}
	if capturedBody["conclusion"] != "success" {
		t.Errorf("body.conclusion = %v; want success", capturedBody["conclusion"])
	}
	// output block must have title and summary.
	output, ok := capturedBody["output"].(map[string]any)
	if !ok {
		t.Fatalf("body.output is not an object: %v", capturedBody["output"])
	}
	if output["title"] == nil || output["title"] == "" {
		t.Error("body.output.title must be non-empty")
	}
	if output["summary"] != "All checks passed" {
		t.Errorf("body.output.summary = %v; want 'All checks passed'", output["summary"])
	}
}

// TestCheck_AllValidConclusions verifies that each valid conclusion value
// produces a successful HTTP call.
func TestCheck_AllValidConclusions(t *testing.T) {
	validConclusions := []string{"success", "failure", "neutral"}

	for _, conclusion := range validConclusions {
		t.Run(conclusion, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1}`))
			}))
			defer srv.Close()

			original := github.GitHubAPIBaseURL
			github.GitHubAPIBaseURL = srv.URL
			defer func() { github.GitHubAPIBaseURL = original }()

			code := runCheckWith("owner/repo", 1, "test-check", conclusion, "summary", "deadbeef", "tok", io.Discard)
			if code != 0 {
				t.Errorf("runCheckWith(%q) = %d; want 0", conclusion, code)
			}
		})
	}
}

// TestCheck_BadConclusion verifies that an invalid conclusion returns non-zero
// without making any HTTP call (fail-fast validation).
func TestCheck_BadConclusion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Must never be called.
		t.Error("HTTP server called but should not be for bad conclusion")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	var stderr strings.Builder
	code := runCheckWith("owner/repo", 1, "test-check", "invalid-conclusion", "summary", "deadbeef", "tok", &stderr)
	if code == 0 {
		t.Fatal("runCheckWith(invalid-conclusion) should return non-zero; got 0")
	}
}

// TestCheck_MissingHeadSHA verifies that an empty head_sha returns non-zero.
// Contract: km-github check requires head_sha — without it, the check run
// cannot be associated with a specific commit.
func TestCheck_MissingHeadSHA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Must never be called when head_sha is empty.
		t.Error("HTTP server called but should not be for missing head_sha")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	var stderr strings.Builder
	code := runCheckWith("owner/repo", 1, "test-check", "success", "summary", "", "tok", &stderr)
	if code == 0 {
		t.Fatal("runCheckWith with empty head_sha should return non-zero; got 0")
	}
	// The error message should mention head_sha or sha so the caller knows what's missing.
	if !strings.Contains(strings.ToLower(stderr.String()), "sha") &&
		!strings.Contains(strings.ToLower(stderr.String()), "head") {
		t.Errorf("stderr should mention 'sha' or 'head'; got: %q", stderr.String())
	}
}

// TestCheck_MissingRequired verifies that runCheck with missing required flags
// returns 2 (usage error) without making any HTTP call.
func TestCheck_MissingRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("HTTP server called but should not be for missing required flags")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	// No --repo.
	code := runCheck([]string{"--number", "1", "--name", "test", "--conclusion", "success", "--head-sha", "abc"}, io.Discard)
	if code != 2 {
		t.Errorf("check without --repo = %d; want 2", code)
	}

	// No --name.
	code = runCheck([]string{"--repo", "org/repo", "--number", "1", "--conclusion", "success", "--head-sha", "abc"}, io.Discard)
	if code != 2 {
		t.Errorf("check without --name = %d; want 2", code)
	}

	// No --conclusion.
	code = runCheck([]string{"--repo", "org/repo", "--number", "1", "--name", "test", "--head-sha", "abc"}, io.Discard)
	if code != 2 {
		t.Errorf("check without --conclusion = %d; want 2", code)
	}
}
