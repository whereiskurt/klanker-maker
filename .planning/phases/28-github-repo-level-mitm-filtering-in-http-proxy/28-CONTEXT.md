# Phase 28: GitHub Repo-Level MITM Filtering in HTTP Proxy - Context

**Gathered:** 2026-03-28
**Status:** Ready for planning
**Source:** Conversation context (design discussion)

<domain>
## Phase Boundary

The HTTP proxy currently filters at **host level** — `github.com` is either allowed or denied entirely. The `sourceAccess.github.allowedRepos` config only scopes the GitHub App installation token (via Lambda refresh), but nothing at the network layer prevents sandbox access to public repos outside the allowlist.

This phase adds **repo-level path inspection via MITM** for GitHub hosts, so the proxy can extract `owner/repo` from URL paths and enforce the allowedRepos list at the network layer. This mirrors the existing Bedrock/Anthropic MITM pattern already in `proxy.go`.

</domain>

<decisions>
## Implementation Decisions

### MITM Pattern
- Mirror the Bedrock/Anthropic MITM pattern in `proxy.go` — register GitHub-specific `HandleConnectFunc` (MITM) and `OnRequest.DoFunc` (path inspection + allowlist check) before the general CONNECT handler
- Use the existing custom CA infrastructure already in place for Bedrock MITM

### GitHub Host Coverage
- MITM the following hosts to inspect URL paths:
  - `github.com` — web UI and git-over-HTTPS
  - `api.github.com` — REST API
  - `raw.githubusercontent.com` — raw file access
  - `codeload.githubusercontent.com` — archive/tarball downloads

### Repo Extraction from URL Paths
- `github.com/{owner}/{repo}[.git]/*` — first two path segments
- `api.github.com/repos/{owner}/{repo}/*` — segments after `/repos/`
- `raw.githubusercontent.com/{owner}/{repo}/*` — first two path segments
- `codeload.githubusercontent.com/{owner}/{repo}/*` — first two path segments
- Non-repo URLs (e.g., `api.github.com/rate_limit`, `github.com/login`) pass through — only enforce when a repo is identifiable

### Allowlist Format
- `allowedRepos` uses `owner/repo` format (already defined in profile schema)
- Case-insensitive matching
- Consider supporting org wildcards (`whereiskurt/*`) for org-wide access

### Token Handling
- **Passthrough only** — allow existing tokens (gh client, git credential helper) on requests
- **No injection** — proxy does NOT inject GitHub App tokens into requests (deferred to future phase)
- Tokens already on the sandbox filesystem via SSM + git credential helper continue to work as-is

### Blocked Request Behavior
- Return 403 with a clear message identifying the blocked `owner/repo` (similar to budget enforcement blocked responses)
- Log blocked repo access for audit trail

### Implicit Host Allowlisting
- When `sourceAccess.github.allowedRepos` is non-empty, the proxy **implicitly allows GitHub hosts** (github.com, api.github.com, *.githubusercontent.com) and routes them through MITM for repo-level filtering
- Profile authors do NOT need to add GitHub hosts to `network.egress.allowedHosts` or `allowedDNSSuffixes` — the presence of `allowedRepos` is sufficient
- If `allowedRepos` is empty/nil, GitHub hosts are NOT implicitly allowed (current behavior preserved — they'd need to be in the host allowlist)
- The MITM filter becomes the sole gatekeeper for GitHub traffic when sourceAccess is configured

### Configuration
- The proxy receives `allowedRepos` list via environment variable (similar to how `ALLOWED_HOSTS` is passed today)
- Compiled from `spec.sourceAccess.github.allowedRepos` in the profile

### Claude's Discretion
- Internal code structure (separate file vs inline in proxy.go)
- Exact regex patterns for host matching
- Test structure and coverage approach
- Error message formatting details

</decisions>

<specifics>
## Specific Ideas

- The proxy already has `bedrockHostRegex` and `anthropicHostRegex` — add `githubHostRegex`, `githubAPIHostRegex`, `githubContentHostRegex` etc.
- The `IsHostAllowed` function pattern can inform a new `IsRepoAllowed` function
- Budget enforcement uses `ExtractModelID` from URL path — similarly create `ExtractRepoFromPath` for GitHub URLs
- Profile schema `GitHubAccess.AllowedRepos` already exists in `pkg/profile/types.go`
- Compiler needs to pass `allowedRepos` to the proxy sidecar environment (similar to `AllowedHTTPHosts` in `userdata.go`)

</specifics>

<deferred>
## Deferred Ideas

- **Token injection** — proxy injects GitHub App token into requests for app-authenticated repos (future phase)
- **Two-class repos** — distinguishing `auth: app` vs `auth: public` in profile schema (future, tied to token injection)
- **Ref filtering** — enforcing `allowedRefs` at the proxy level (currently only enforced via git hooks)
- **Method filtering** — restricting HTTP methods per repo (e.g., read-only = GET only)

</deferred>

---

*Phase: 28-github-repo-level-mitm-filtering-in-http-proxy*
*Context gathered: 2026-03-28 via conversation context*
