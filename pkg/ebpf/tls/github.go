//go:build linux

package tls

import (
	"strings"

	"github.com/rs/zerolog"
)

// EventHandler is a callback invoked for each captured TLS event.
// Handlers inspect the plaintext payload and may log or meter traffic.
type EventHandler func(event *TLSEvent) error

// ExtractGitHubRepo extracts the owner and repo name from a GitHub request path.
// For api.github.com it expects paths like /repos/{owner}/{repo}/...
// For github.com it expects paths like /{owner}/{repo}[.git]/...
// Returns empty strings if the host is not GitHub or the path does not contain a repo.
func ExtractGitHubRepo(host, path string) (owner, repo string) {
	if !isGitHubHost(host) {
		return "", ""
	}

	// Split path into segments, filtering empty strings from leading/trailing slashes.
	parts := strings.Split(strings.Trim(path, "/"), "/")

	h := strings.ToLower(host)
	switch {
	case h == "api.github.com":
		// /repos/{owner}/{repo}/...
		if len(parts) >= 3 && strings.ToLower(parts[0]) == "repos" {
			return parts[1], parts[2]
		}
		return "", ""

	case h == "github.com":
		// /{owner}/{repo}[.git]/...
		if len(parts) >= 2 {
			r := parts[1]
			r = strings.TrimSuffix(r, ".git")
			return parts[0], r
		}
		return "", ""

	default:
		// *.github.com subdomains — no standard repo path format
		return "", ""
	}
}

// GitHubAuditHandler logs audit events when TLS-captured HTTP requests
// target GitHub repositories. It is observability-only; enforcement
// remains in the MITM proxy.
type GitHubAuditHandler struct {
	AllowedRepos map[string]bool
	AllowAll     bool
	Logger       zerolog.Logger
}

// NewGitHubAuditHandler creates a handler that audits GitHub repo access.
// allowedRepos is a list of "owner/repo" strings (case-insensitive).
// A single "*" entry means all repos are allowed.
func NewGitHubAuditHandler(allowedRepos []string, logger zerolog.Logger) *GitHubAuditHandler {
	allowAll := false
	allowed := make(map[string]bool, len(allowedRepos))
	for _, r := range allowedRepos {
		if r == "*" {
			allowAll = true
			continue
		}
		allowed[strings.ToLower(r)] = true
	}
	return &GitHubAuditHandler{
		AllowedRepos: allowed,
		AllowAll:     allowAll,
		Logger:       logger,
	}
}

// Handle inspects a TLS event for GitHub repo access and logs audit events.
// It implements the EventHandler signature.
func (h *GitHubAuditHandler) Handle(event *TLSEvent) error {
	// Only inspect outbound requests (SSL_write direction).
	if event.Direction != DirWrite {
		return nil
	}

	req, err := ParseHTTPRequest(event.PayloadBytes())
	if err != nil {
		// Not an HTTP request — nothing to inspect.
		return nil
	}

	owner, repo := ExtractGitHubRepo(req.Host, req.Path)
	if owner == "" && repo == "" {
		// Not a GitHub repo request — skip.
		return nil
	}

	repoKey := strings.ToLower(owner + "/" + repo)
	if h.AllowAll || h.AllowedRepos[repoKey] {
		h.Logger.Debug().
			Str("owner", owner).
			Str("repo", repo).
			Str("method", req.Method).
			Str("path", req.Path).
			Uint32("pid", event.Pid).
			Str("remote_addr", event.RemoteAddr().String()).
			Str("sandbox_event", "github_repo_access").
			Msg("allowed GitHub repo access")
	} else {
		h.Logger.Warn().
			Str("owner", owner).
			Str("repo", repo).
			Str("method", req.Method).
			Str("path", req.Path).
			Uint32("pid", event.Pid).
			Str("remote_addr", event.RemoteAddr().String()).
			Str("sandbox_event", "github_repo_violation").
			Msg("GitHub repo not in allowlist")
	}

	return nil
}

// BedrockAuditHandler logs audit events when TLS-captured HTTP requests
// target AWS Bedrock or Anthropic API endpoints. This is a stub that logs
// URL and method only -- actual token extraction requires HTTP/2 DATA frame
// parsing which is not possible via uprobes (per EBPF-TLS-10 research).
type BedrockAuditHandler struct {
	Logger zerolog.Logger
}

// NewBedrockAuditHandler creates a handler that audits Bedrock/Anthropic API access.
func NewBedrockAuditHandler(logger zerolog.Logger) *BedrockAuditHandler {
	return &BedrockAuditHandler{Logger: logger}
}

// Handle inspects a TLS event for Bedrock/Anthropic API requests.
func (h *BedrockAuditHandler) Handle(event *TLSEvent) error {
	if event.Direction != DirWrite {
		return nil
	}

	req, err := ParseHTTPRequest(event.PayloadBytes())
	if err != nil {
		return nil
	}

	host := strings.ToLower(req.Host)
	isBedrock := strings.HasPrefix(host, "bedrock-runtime.") && strings.HasSuffix(host, ".amazonaws.com")
	isAnthropic := host == "api.anthropic.com"

	if !isBedrock && !isAnthropic {
		return nil
	}

	h.Logger.Info().
		Str("host", req.Host).
		Str("method", req.Method).
		Str("path", req.Path).
		Uint32("pid", event.Pid).
		Str("remote_addr", event.RemoteAddr().String()).
		Str("sandbox_event", "ai_api_request").
		Msg("AI API request observed")

	return nil
}
