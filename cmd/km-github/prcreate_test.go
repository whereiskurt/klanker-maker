//go:build phase98_wave0

// prcreate_test.go — RED unit tests for the km-github pr create subcommand.
//
// BUILD TAG: phase98_wave0
// This file is excluded from normal builds. It references runPRCreate and
// runPRCreateWith which are NOT YET IMPLEMENTED (98-01 adds them). This is
// the Wave 0 Nyquist gate: the tests exist and fail to compile until 98-01
// implements the functions.
//
// HANDOFF TO 98-01:
//   1. Implement runPRCreate(args []string, stderr io.Writer) int in main.go.
//   2. Implement runPRCreateWith(repo, title, head, base, body, token string,
//      stderr io.Writer, stdout io.Writer) int in main.go. The function MUST
//      print html_url to the stdout writer (and it also receives stderr for errors).
//      This matches the *With pattern extended with stdout for testability.
//   3. Add "pr" sub-subcommand to the dispatch table in dispatch().
//   4. Remove the `//go:build phase98_wave0` constraint from THIS file.
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github"
)

// TestPRCreate verifies that runPRCreateWith POSTs to /repos/{owner}/{repo}/pulls
// with the correct body, returns 0, and prints html_url to stdout.
func TestPRCreate(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/owner/repo/pull/7","number":7}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	var stdout bytes.Buffer
	code := runPRCreateWith(
		"owner/repo",
		"My new feature",
		"feature-branch",
		"main",
		"This PR adds a new feature",
		"test-token",
		io.Discard,
		&stdout,
	)
	if code != 0 {
		t.Fatalf("runPRCreateWith returned %d; want 0", code)
	}

	if capturedMethod != http.MethodPost {
		t.Errorf("method = %q; want POST", capturedMethod)
	}
	wantPath := "/repos/owner/repo/pulls"
	if capturedPath != wantPath {
		t.Errorf("path = %q; want %q", capturedPath, wantPath)
	}

	// Verify required body fields per GitHub Pull Requests API.
	if capturedBody["title"] != "My new feature" {
		t.Errorf("body.title = %v; want 'My new feature'", capturedBody["title"])
	}
	if capturedBody["head"] != "feature-branch" {
		t.Errorf("body.head = %v; want 'feature-branch'", capturedBody["head"])
	}
	if capturedBody["base"] != "main" {
		t.Errorf("body.base = %v; want 'main'", capturedBody["base"])
	}
	if capturedBody["body"] != "This PR adds a new feature" {
		t.Errorf("body.body = %v; want 'This PR adds a new feature'", capturedBody["body"])
	}

	// html_url must appear in stdout.
	out := stdout.String()
	if !strings.Contains(out, "https://github.com/owner/repo/pull/7") {
		t.Errorf("stdout should contain html_url; got: %q", out)
	}
}

// TestPRCreate_EmptyBody verifies that runPRCreateWith works when body is empty
// (PR body is optional per GitHub API).
func TestPRCreate_EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/o/r/pull/1","number":1}`))
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	var stdout bytes.Buffer
	code := runPRCreateWith("o/r", "title", "head", "base", "", "tok", io.Discard, &stdout)
	if code != 0 {
		t.Fatalf("runPRCreateWith(empty body) = %d; want 0", code)
	}
}

// TestPRCreate_MissingRequired verifies that runPRCreate with missing required
// flags returns 2 (usage error) without making any HTTP call.
func TestPRCreate_MissingRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("HTTP server called but should not be for missing required flags")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	// No --title.
	code := runPRCreate([]string{"--repo", "org/repo", "--head", "feat", "--base", "main"}, io.Discard)
	if code != 2 {
		t.Errorf("pr create without --title = %d; want 2", code)
	}

	// No --head.
	code = runPRCreate([]string{"--repo", "org/repo", "--title", "t", "--base", "main"}, io.Discard)
	if code != 2 {
		t.Errorf("pr create without --head = %d; want 2", code)
	}

	// No --base.
	code = runPRCreate([]string{"--repo", "org/repo", "--title", "t", "--head", "feat"}, io.Discard)
	if code != 2 {
		t.Errorf("pr create without --base = %d; want 2", code)
	}

	// No --repo.
	code = runPRCreate([]string{"--title", "t", "--head", "feat", "--base", "main"}, io.Discard)
	if code != 2 {
		t.Errorf("pr create without --repo = %d; want 2", code)
	}
}
