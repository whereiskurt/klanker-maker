// Package httpproxy — github.go
// GitHub repo-level MITM filtering helpers.
// When WithGitHubRepoFilter is configured, the proxy intercepts HTTPS to
// GitHub hosts, extracts the owner/repo from the URL, and blocks requests to
// repos that are not in the allowlist. Non-repo GitHub URLs pass through.
package httpproxy

import (
	"encoding/json"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/elazarl/goproxy"
)

// githubHostsRegex matches all GitHub-related hostnames with optional port.
var githubHostsRegex = regexp.MustCompile(`^(github\.com|api\.github\.com|raw\.githubusercontent\.com|codeload\.githubusercontent\.com)(:\d+)?$`)

// githubBlockedBody is the JSON shape returned for blocked repo requests.
type githubBlockedBody struct {
	Error  string `json:"error"`
	Repo   string `json:"repo"`
	Reason string `json:"reason"`
}

// ExtractRepoFromPath derives the canonical "owner/repo" string from a
// GitHub host + URL path. It returns "" for non-repo URLs (root, single
// segment, or paths that do not carry a repo identity).
//
// Rules:
//   - api.github.com: repo is at /repos/{owner}/{repo}/... — other paths are non-repo.
//   - All other GitHub hosts: first two path segments are owner/repo.
//     A trailing ".git" suffix on the repo segment is stripped.
//
// The returned string is always lower-cased. The host port is stripped before
// matching.
func ExtractRepoFromPath(host, urlPath string) string {
	// Strip port.
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	h = strings.ToLower(h)

	switch h {
	case "api.github.com":
		// Expect /repos/{owner}/{repo}/...
		const prefix = "/repos/"
		if !strings.HasPrefix(urlPath, prefix) {
			return ""
		}
		rest := strings.TrimPrefix(urlPath, prefix)
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return ""
		}
		return strings.ToLower(parts[0]) + "/" + strings.ToLower(parts[1])

	default:
		// github.com, raw.githubusercontent.com, codeload.githubusercontent.com
		// All use /{owner}/{repo}/... as the first two segments.
		trimmed := strings.TrimPrefix(urlPath, "/")
		parts := strings.SplitN(trimmed, "/", 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return ""
		}
		owner := strings.ToLower(parts[0])
		repo := strings.ToLower(parts[1])
		// Strip .git suffix (e.g. /owner/repo.git/info/refs → owner/repo)
		repo = strings.TrimSuffix(repo, ".git")
		if repo == "" {
			return ""
		}
		return owner + "/" + repo
	}
}

// IsRepoAllowed reports whether repo is in the allowed list. Matching is
// case-insensitive. Entries may use:
//   - "owner/repo"          — exact match
//   - "owner/*"             — all repos under that org
//   - "github.com/owner/repo" — github.com/ prefix is stripped before comparison
func IsRepoAllowed(repo string, allowed []string) bool {
	repo = strings.ToLower(repo)
	for _, a := range allowed {
		a = strings.ToLower(a)
		// Normalise allowlist entries that include the github.com/ prefix.
		a = strings.TrimPrefix(a, "github.com/")
		// Org wildcard: "org/*" matches any "org/anything".
		if strings.HasSuffix(a, "/*") {
			orgPrefix := strings.TrimSuffix(a, "/*") + "/"
			if strings.HasPrefix(repo, orgPrefix) {
				return true
			}
			continue
		}
		if a == repo {
			return true
		}
	}
	return false
}

// GitHubBlockedResponse returns a goproxy-compatible 403 http.Response that
// informs the client their request was blocked because the target repo is not
// in the allowlist.
func GitHubBlockedResponse(req *http.Request, sandboxID, repo string) *http.Response {
	body := githubBlockedBody{
		Error:  "repo_not_allowed",
		Repo:   repo,
		Reason: "repo is not in the sandbox allowedRepos list",
	}
	encoded, _ := json.Marshal(body)
	return goproxy.NewResponse(req, "application/json", http.StatusForbidden, string(encoded))
}

// WithGitHubRepoFilter configures repo-level MITM filtering for GitHub hosts.
// When allowedRepos is non-empty, the proxy intercepts requests to GitHub
// and blocks any that target a repo not in the list. Non-repo GitHub URLs
// (e.g. /rate_limit, /login) pass through unconditionally.
//
// When allowedRepos is nil or empty, no GitHub MITM handlers are registered
// and GitHub hosts are subject to the normal allowedHosts check.
func WithGitHubRepoFilter(allowedRepos []string) ProxyOption {
	return func(_ *goproxy.ProxyHttpServer, cfg *proxyConfig) {
		cfg.githubRepos = allowedRepos
	}
}
