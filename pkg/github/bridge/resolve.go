package bridge

import (
	"fmt"
	"path"
	"strings"
)

// RepoEntry is a single entry in the github.repos config block.
// It maps a repository name pattern (exact or glob) to a sandbox alias,
// profile, and per-repo login allowlist.
//
// JSON field names mirror the km-config.yaml surface so json.Unmarshal
// from KM_GITHUB_REPOS works directly.
type RepoEntry struct {
	// Match is an exact "owner/repo" or glob "owner/*" pattern.
	// Exact matches always win over globs regardless of declaration order.
	Match string `json:"match"`

	// Alias is the sandbox alias to use for this repo. When empty, defaults
	// to "gh-{owner}-{repo}" (hyphens replacing slash).
	Alias string `json:"alias,omitempty"`

	// Profile is the SandboxProfile name to use on cold create. When empty,
	// falls back to the top-level github.default_profile.
	Profile string `json:"profile,omitempty"`

	// Allow is the explicit GitHub login allowlist. Deny-by-default:
	// a comment from a login not in this list is silently ignored.
	Allow []string `json:"allow,omitempty"`
}

// Resolve maps a repository full name (e.g. "myorg/myrepo") to its
// {alias, profile, allow} tuple by scanning entries.
//
// Resolution order (RESEARCH Pattern 3):
//  1. Collect exact matches (entry.Match == fullName) — first exact wins.
//  2. If no exact match, collect glob matches (entry.Match contains a wildcard)
//     in declaration order — first glob wins.
//  3. alias defaults to "gh-{owner}-{repo}" when the matched entry has no Alias.
//  4. profile defaults to defaultProfile when the matched entry has no Profile.
//  5. No match → matched=false; caller should 200-drop (no config for this repo).
//
// This is a pure function with no AWS dependency, making it exhaustively
// table-testable without any mock infrastructure.
func Resolve(fullName string, entries []RepoEntry, defaultProfile string) (alias, profile string, allow []string, matched bool) {
	// Pass 1: exact matches only.
	for _, e := range entries {
		if e.Match == fullName {
			return buildResult(fullName, e, defaultProfile)
		}
	}

	// Pass 2: glob matches, first-wins.
	for _, e := range entries {
		if isGlob(e.Match) {
			ok, err := path.Match(e.Match, fullName)
			if err == nil && ok {
				return buildResult(fullName, e, defaultProfile)
			}
		}
	}

	return "", "", nil, false
}

// buildResult applies alias and profile defaults for a matched entry.
func buildResult(fullName string, e RepoEntry, defaultProfile string) (alias, profile string, allow []string, matched bool) {
	a := e.Alias
	if a == "" {
		a = defaultAlias(fullName)
	}
	p := e.Profile
	if p == "" {
		p = defaultProfile
	}
	return a, p, e.Allow, true
}

// defaultAlias derives the canonical alias for a repo when no explicit alias
// is configured: "gh-{owner}-{repo}" (slash replaced by hyphen).
func defaultAlias(fullName string) string {
	return "gh-" + strings.ReplaceAll(fullName, "/", "-")
}

// isGlob returns true when s contains path.Match wildcard characters.
func isGlob(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// ContainsMention reports whether body contains "@{botLogin}" (case-insensitive).
// Used by WebhookHandler.Handle step 6 to gate dispatch.
func ContainsMention(body, botLogin string) bool {
	return strings.Contains(
		strings.ToLower(body),
		"@"+strings.ToLower(botLogin),
	)
}

// ExtractMentionBody returns the free-form text that follows the first
// "@{botLogin}" token in body, trimmed of leading/trailing whitespace.
// If no mention is found, returns the entire body trimmed.
// The result is the agent prompt sent to the sandbox.
func ExtractMentionBody(body, botLogin string) string {
	lower := strings.ToLower(body)
	mention := "@" + strings.ToLower(botLogin)
	idx := strings.Index(lower, mention)
	if idx == -1 {
		return strings.TrimSpace(body)
	}
	after := body[idx+len(mention):]
	return strings.TrimSpace(after)
}

// OwnerFromFullName extracts the owner part of "owner/repo".
func OwnerFromFullName(fullName string) string {
	if idx := strings.Index(fullName, "/"); idx >= 0 {
		return fullName[:idx]
	}
	return fullName
}

// RepoFromFullName extracts the repo part of "owner/repo".
func RepoFromFullName(fullName string) string {
	if idx := strings.Index(fullName, "/"); idx >= 0 {
		return fullName[idx+1:]
	}
	return fullName
}

// InstallIDString formats an int64 installation ID as a string for API calls.
func InstallIDString(id int64) string {
	return fmt.Sprintf("%d", id)
}
