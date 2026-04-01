//go:build linux

package tls

import (
	"bytes"
	"testing"

	"github.com/rs/zerolog"
)

func TestExtractGitHubRepo_APIReposPath(t *testing.T) {
	owner, repo := ExtractGitHubRepo("api.github.com", "/repos/octocat/hello-world/git/refs")
	if owner != "octocat" || repo != "hello-world" {
		t.Errorf("got (%q, %q), want (octocat, hello-world)", owner, repo)
	}
}

func TestExtractGitHubRepo_APIReposPathShort(t *testing.T) {
	owner, repo := ExtractGitHubRepo("api.github.com", "/repos/octocat/hello-world")
	if owner != "octocat" || repo != "hello-world" {
		t.Errorf("got (%q, %q), want (octocat, hello-world)", owner, repo)
	}
}

func TestExtractGitHubRepo_GitSmartHTTP(t *testing.T) {
	owner, repo := ExtractGitHubRepo("github.com", "/octocat/hello-world.git/info/refs")
	if owner != "octocat" || repo != "hello-world" {
		t.Errorf("got (%q, %q), want (octocat, hello-world)", owner, repo)
	}
}

func TestExtractGitHubRepo_GitHubComBrowse(t *testing.T) {
	owner, repo := ExtractGitHubRepo("github.com", "/octocat/hello-world/tree/main")
	if owner != "octocat" || repo != "hello-world" {
		t.Errorf("got (%q, %q), want (octocat, hello-world)", owner, repo)
	}
}

func TestExtractGitHubRepo_NonRepoPath(t *testing.T) {
	owner, repo := ExtractGitHubRepo("api.github.com", "/user")
	if owner != "" || repo != "" {
		t.Errorf("got (%q, %q), want empty strings for non-repo path", owner, repo)
	}
}

func TestExtractGitHubRepo_NonRepoNotifications(t *testing.T) {
	owner, repo := ExtractGitHubRepo("api.github.com", "/notifications")
	if owner != "" || repo != "" {
		t.Errorf("got (%q, %q), want empty strings for /notifications", owner, repo)
	}
}

func TestExtractGitHubRepo_NonGitHubHost(t *testing.T) {
	owner, repo := ExtractGitHubRepo("example.com", "/foo/bar")
	if owner != "" || repo != "" {
		t.Errorf("got (%q, %q), want empty strings for non-GitHub host", owner, repo)
	}
}

func TestGitHubAuditHandler_ViolationLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).With().Logger()
	h := NewGitHubAuditHandler([]string{"allowed/repo"}, logger)

	event := &TLSEvent{
		Direction:  DirWrite,
		PayloadLen: 0, // will be set below
	}
	// Write a valid HTTP request to the payload
	req := []byte("GET /repos/octocat/hello-world/git/refs HTTP/1.1\r\nHost: api.github.com\r\n\r\n")
	copy(event.Payload[:], req)
	event.PayloadLen = uint32(len(req))

	if err := h.Handle(event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logged := buf.String()
	if !bytes.Contains([]byte(logged), []byte("github_repo_violation")) {
		t.Errorf("expected violation log, got: %s", logged)
	}
	if !bytes.Contains([]byte(logged), []byte("octocat")) {
		t.Errorf("expected owner in log, got: %s", logged)
	}
}

func TestGitHubAuditHandler_AllowedRepoSilent(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).Level(zerolog.InfoLevel).With().Logger()
	h := NewGitHubAuditHandler([]string{"octocat/hello-world"}, logger)

	event := &TLSEvent{
		Direction:  DirWrite,
		PayloadLen: 0,
	}
	req := []byte("GET /repos/octocat/hello-world/git/refs HTTP/1.1\r\nHost: api.github.com\r\n\r\n")
	copy(event.Payload[:], req)
	event.PayloadLen = uint32(len(req))

	if err := h.Handle(event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// At Info level, Debug messages should not appear
	logged := buf.String()
	if bytes.Contains([]byte(logged), []byte("github_repo_violation")) {
		t.Errorf("should not log violation for allowed repo, got: %s", logged)
	}
}

func TestGitHubAuditHandler_IgnoresReadDirection(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).With().Logger()
	h := NewGitHubAuditHandler(nil, logger)

	event := &TLSEvent{
		Direction:  DirRead, // inbound — should be skipped
		PayloadLen: 0,
	}
	req := []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
	copy(event.Payload[:], req)
	event.PayloadLen = uint32(len(req))

	if err := h.Handle(event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if buf.Len() > 0 {
		t.Errorf("should not log anything for read direction, got: %s", buf.String())
	}
}

func TestGitHubAuditHandler_IgnoresNonHTTP(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).With().Logger()
	h := NewGitHubAuditHandler(nil, logger)

	event := &TLSEvent{
		Direction:  DirWrite,
		PayloadLen: 5,
	}
	// Binary data, not HTTP
	copy(event.Payload[:], []byte{0x16, 0x03, 0x01, 0x00, 0x05})

	if err := h.Handle(event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if buf.Len() > 0 {
		t.Errorf("should not log anything for non-HTTP data, got: %s", buf.String())
	}
}
