// commit_test.go — unit tests for the km-github commit subcommand.
//
// km-github commit creates a verified, bot-attributed commit via the GraphQL
// createCommitOnBranch mutation (local `git commit` is unsigned; the low-level
// REST POST /git/commits is also unsigned; only createCommitOnBranch auto-signs).
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github"
)

// commitTestServer wires an httptest server that plays the three endpoints the
// commit flow touches: the branch-ref GET, the GraphQL mutation, and the
// verification GET. Captured request state is returned via the closure vars.
func commitTestServer(t *testing.T, newOID string) (*httptest.Server, *capturedCommit) {
	t.Helper()
	cap := &capturedCommit{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/repos/") && strings.Contains(r.URL.Path, "/git/refs/heads/"):
			cap.patchCalled = true
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &cap.patchBody)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"object":{"sha":"resetsha"}}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/git/ref/heads/"):
			cap.refGetPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":{"sha":"headoid123"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			cap.graphqlCalled = true
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &cap.graphqlBody)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"createCommitOnBranch":{"commit":{"oid":"` + newOID + `"}}}}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/git/commits/"):
			cap.verifyGetPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"verification":{"verified":true,"reason":"valid"},"author":{"name":"klanker-maker[bot]"},"committer":{"name":"GitHub"}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	return srv, cap
}

type capturedCommit struct {
	patchCalled   bool
	patchBody     map[string]any
	refGetPath    string
	graphqlCalled bool
	graphqlBody   map[string]any
	verifyGetPath string
}

// TestCommit_Success verifies the happy path: no --parent, fetch head OID, POST
// the GraphQL mutation with base64 file additions + expectedHeadOid, print the
// new oid to stdout, and print the verification line to stderr.
func TestCommit_Success(t *testing.T) {
	srv, cap := commitTestServer(t, "newcommitoid999")
	defer srv.Close()

	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	files := []commitFile{
		{Path: "docs/a.md", Content: []byte("hello a\n")},
		{Path: "docs/b.md", Content: []byte("hello b\n")},
	}
	var stdout, stderr bytes.Buffer
	code := runCommitWith("owner/repo", "main", "", "Add docs", "body line one\nbody line two", "test-token", files, &stderr, &stdout)
	if code != 0 {
		t.Fatalf("runCommitWith returned %d; want 0\nstderr: %s", code, stderr.String())
	}

	// No --parent → no PATCH.
	if cap.patchCalled {
		t.Errorf("PATCH should not be called without --parent")
	}
	// Head OID fetched from the branch ref.
	if cap.refGetPath != "/repos/owner/repo/git/ref/heads/main" {
		t.Errorf("ref GET path = %q; want /repos/owner/repo/git/ref/heads/main", cap.refGetPath)
	}
	if !cap.graphqlCalled {
		t.Fatal("GraphQL mutation was not called")
	}

	// Dig into the captured GraphQL variables.input.
	vars, _ := cap.graphqlBody["variables"].(map[string]any)
	input, _ := vars["input"].(map[string]any)
	if input == nil {
		t.Fatalf("graphql body missing variables.input: %v", cap.graphqlBody)
	}
	if got := input["expectedHeadOid"]; got != "headoid123" {
		t.Errorf("expectedHeadOid = %v; want headoid123", got)
	}
	branch, _ := input["branch"].(map[string]any)
	if branch["repositoryNameWithOwner"] != "owner/repo" || branch["branchName"] != "main" {
		t.Errorf("branch = %v; want {owner/repo, main}", branch)
	}
	msg, _ := input["message"].(map[string]any)
	if msg["headline"] != "Add docs" {
		t.Errorf("headline = %v; want 'Add docs'", msg["headline"])
	}
	if msg["body"] != "body line one\nbody line two" {
		t.Errorf("body = %v; want the two body lines", msg["body"])
	}
	// Additions: base64-encoded contents, order preserved.
	fc, _ := input["fileChanges"].(map[string]any)
	adds, _ := fc["additions"].([]any)
	if len(adds) != 2 {
		t.Fatalf("additions len = %d; want 2", len(adds))
	}
	a0, _ := adds[0].(map[string]any)
	if a0["path"] != "docs/a.md" {
		t.Errorf("additions[0].path = %v; want docs/a.md", a0["path"])
	}
	wantB64 := base64.StdEncoding.EncodeToString([]byte("hello a\n"))
	if a0["contents"] != wantB64 {
		t.Errorf("additions[0].contents = %v; want %v", a0["contents"], wantB64)
	}

	// Stdout is the new commit oid.
	if strings.TrimSpace(stdout.String()) != "newcommitoid999" {
		t.Errorf("stdout = %q; want newcommitoid999", stdout.String())
	}
	// Verification line surfaced to stderr.
	if !strings.Contains(stderr.String(), "verified=true") || !strings.Contains(stderr.String(), "klanker-maker[bot]") {
		t.Errorf("stderr should carry the verification line; got: %q", stderr.String())
	}
	if cap.verifyGetPath != "/repos/owner/repo/git/commits/newcommitoid999" {
		t.Errorf("verify GET path = %q; want the new oid commit", cap.verifyGetPath)
	}
}

// TestCommit_ParentForceReset verifies that --parent issues a PATCH force-reset
// of the branch ref before fetching the head OID.
func TestCommit_ParentForceReset(t *testing.T) {
	srv, cap := commitTestServer(t, "oid2")
	defer srv.Close()
	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	files := []commitFile{{Path: "f.txt", Content: []byte("x")}}
	var stdout, stderr bytes.Buffer
	code := runCommitWith("o/r", "feat", "basesha", "Head only", "", "tok", files, &stderr, &stdout)
	if code != 0 {
		t.Fatalf("runCommitWith = %d; want 0\nstderr: %s", code, stderr.String())
	}
	if !cap.patchCalled {
		t.Fatal("PATCH force-reset should be called when --parent is set")
	}
	if cap.patchBody["sha"] != "basesha" {
		t.Errorf("PATCH sha = %v; want basesha", cap.patchBody["sha"])
	}
	if cap.patchBody["force"] != true {
		t.Errorf("PATCH force = %v; want true", cap.patchBody["force"])
	}
}

// TestCommit_GraphQLErrors verifies that a GraphQL errors array yields exit 1.
func TestCommit_GraphQLErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/git/ref/heads/"):
			_, _ = w.Write([]byte(`{"object":{"sha":"h"}}`))
		case r.URL.Path == "/graphql":
			_, _ = w.Write([]byte(`{"errors":[{"message":"expectedHeadOid mismatch"}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	var stdout, stderr bytes.Buffer
	code := runCommitWith("o/r", "main", "", "hl", "", "tok", []commitFile{{Path: "f", Content: []byte("y")}}, &stderr, &stdout)
	if code != 1 {
		t.Errorf("runCommitWith with GraphQL errors = %d; want 1", code)
	}
	if !strings.Contains(stderr.String(), "expectedHeadOid mismatch") {
		t.Errorf("stderr should surface the GraphQL error; got: %q", stderr.String())
	}
}

// TestCommit_MissingRequired verifies flag validation in runCommit returns 2
// without any HTTP call.
func TestCommit_MissingRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("HTTP server called but should not be for missing required flags")
	}))
	defer srv.Close()
	original := github.GitHubAPIBaseURL
	github.GitHubAPIBaseURL = srv.URL
	defer func() { github.GitHubAPIBaseURL = original }()

	// No --repo.
	if code := runCommit([]string{"--branch", "main", "--message-file", "/nonexistent", "f"}, io.Discard); code != 2 {
		t.Errorf("commit without --repo = %d; want 2", code)
	}
	// No --branch.
	if code := runCommit([]string{"--repo", "o/r", "--message-file", "/nonexistent", "f"}, io.Discard); code != 2 {
		t.Errorf("commit without --branch = %d; want 2", code)
	}
	// No --message-file.
	if code := runCommit([]string{"--repo", "o/r", "--branch", "main", "f"}, io.Discard); code != 2 {
		t.Errorf("commit without --message-file = %d; want 2", code)
	}
	// No files.
	if code := runCommit([]string{"--repo", "o/r", "--branch", "main", "--message-file", "/nonexistent"}, io.Discard); code != 2 {
		t.Errorf("commit without files = %d; want 2", code)
	}
}

// TestSplitCommitMessage verifies headline/body splitting: first line is the
// headline, remaining lines (leading blanks stripped) are the body.
func TestSplitCommitMessage(t *testing.T) {
	cases := []struct {
		raw, headline, body string
	}{
		{"just a headline\n", "just a headline", ""},
		{"headline\n\nbody para\n", "headline", "body para"},
		{"headline\nline2\nline3", "headline", "line2\nline3"},
		{"headline\n\n\n  indented body\n", "headline", "  indented body"},
	}
	for _, c := range cases {
		hl, body := splitCommitMessage(c.raw)
		if hl != c.headline {
			t.Errorf("splitCommitMessage(%q) headline = %q; want %q", c.raw, hl, c.headline)
		}
		if body != c.body {
			t.Errorf("splitCommitMessage(%q) body = %q; want %q", c.raw, body, c.body)
		}
	}
}
