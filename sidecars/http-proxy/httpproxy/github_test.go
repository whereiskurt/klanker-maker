package httpproxy_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/whereiskurt/klankrmkr/sidecars/http-proxy/httpproxy"
)

func TestExtractRepoFromPath(t *testing.T) {
	tests := []struct {
		host     string
		urlPath  string
		expected string
	}{
		// github.com
		{"github.com", "/owner/repo", "owner/repo"},
		{"github.com", "/owner/repo.git/info/refs", "owner/repo"},
		{"github.com", "/owner/repo/tree/main", "owner/repo"},
		{"github.com", "/login", ""},
		{"github.com", "/", ""},
		// github.com with port (should be stripped)
		{"github.com:443", "/owner/repo", "owner/repo"},
		// case normalisation
		{"github.com", "/Owner/Repo", "owner/repo"},

		// api.github.com
		{"api.github.com", "/repos/owner/repo/commits", "owner/repo"},
		{"api.github.com", "/rate_limit", ""},
		{"api.github.com", "/user", ""},

		// raw.githubusercontent.com
		{"raw.githubusercontent.com", "/owner/repo/main/README.md", "owner/repo"},

		// codeload.githubusercontent.com
		{"codeload.githubusercontent.com", "/owner/repo/tar.gz/main", "owner/repo"},
	}

	for _, tc := range tests {
		got := httpproxy.ExtractRepoFromPath(tc.host, tc.urlPath)
		if got != tc.expected {
			t.Errorf("ExtractRepoFromPath(%q, %q) = %q, want %q", tc.host, tc.urlPath, got, tc.expected)
		}
	}
}

func TestIsRepoAllowed(t *testing.T) {
	tests := []struct {
		repo     string
		allowed  []string
		expected bool
	}{
		// exact match
		{"owner/repo", []string{"owner/repo"}, true},
		// not in list
		{"owner/repo", []string{"other/repo"}, false},
		// case-insensitive
		{"Owner/Repo", []string{"owner/repo"}, true},
		// org wildcard match
		{"org/anyrepo", []string{"org/*"}, true},
		// org wildcard wrong org
		{"otherorg/repo", []string{"org/*"}, false},
		// empty list
		{"owner/repo", []string{}, false},
		// github.com/ prefix stripped in allowlist
		{"owner/repo", []string{"github.com/owner/repo"}, true},
	}

	for _, tc := range tests {
		got := httpproxy.IsRepoAllowed(tc.repo, tc.allowed)
		if got != tc.expected {
			t.Errorf("IsRepoAllowed(%q, %v) = %v, want %v", tc.repo, tc.allowed, got, tc.expected)
		}
	}
}

func TestGitHubBlockedResponse(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://github.com/evil/repo", nil)

	resp := httpproxy.GitHubBlockedResponse(req, "sandbox-123", "evil/repo")

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("body is not valid JSON: %v\nbody: %s", err, body)
	}

	if m["error"] != "repo_not_allowed" {
		t.Errorf("expected error=repo_not_allowed, got %v", m["error"])
	}
	if m["repo"] != "evil/repo" {
		t.Errorf("expected repo=evil/repo, got %v", m["repo"])
	}
	if _, ok := m["reason"]; !ok {
		t.Error("expected reason field in JSON response")
	}
}
