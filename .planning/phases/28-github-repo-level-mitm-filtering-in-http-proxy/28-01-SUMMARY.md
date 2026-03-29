---
phase: 28-github-repo-level-mitm-filtering-in-http-proxy
plan: "01"
subsystem: http-proxy
tags: [proxy, github, mitm, repo-filter, tdd]
dependency_graph:
  requires: []
  provides: [github-repo-mitm-filter]
  affects: [sidecars/http-proxy]
tech_stack:
  added: []
  patterns: [functional-options, table-driven-tests, goproxy-handler-chain, custom-dialer-test-pattern]
key_files:
  created:
    - sidecars/http-proxy/httpproxy/github.go
    - sidecars/http-proxy/httpproxy/github_test.go
  modified:
    - sidecars/http-proxy/httpproxy/proxy.go
    - sidecars/http-proxy/httpproxy/http_proxy_test.go
decisions:
  - "Implicit GitHub host allow: when githubRepos is non-empty, plain-HTTP general handler skips GitHub hosts via githubHostsRegex check — avoids double-block without changing goproxy handler ordering semantics"
  - "Custom test dialer: integration tests use proxy.Tr with custom DialContext to redirect github.com TCP connections to local test server — avoids real DNS/TLS in unit tests while fully exercising handler chain"
  - "Plain-HTTP testing only: GitHub MITM CONNECT handler registered but integration tests exercise plain-HTTP path only; MITM TLS would require WithCustomCA in tests"
metrics:
  duration: 524s
  completed_date: "2026-03-28"
  tasks_completed: 1
  files_changed: 4
---

# Phase 28 Plan 01: GitHub Repo-Level MITM Filtering Summary

**One-liner:** GitHub repo-level MITM filtering with `ExtractRepoFromPath`/`IsRepoAllowed`/`GitHubBlockedResponse` helpers and `WithGitHubRepoFilter` ProxyOption that implicitly allows GitHub hosts when configured.

## What Was Built

Added GitHub repo-level filtering to the HTTP proxy sidecar. When `WithGitHubRepoFilter(allowedRepos)` is configured:

1. A `HandleConnectFunc` for `githubHostsRegex` returns `goproxy.MitmConnect` — registered before the general CONNECT handler so goproxy's first-match semantics route GitHub HTTPS through MITM
2. An `OnRequest.DoFunc` for `githubHostsRegex` calls `ExtractRepoFromPath` + `IsRepoAllowed` and returns `GitHubBlockedResponse` for unlisted repos
3. The plain-HTTP general `OnRequest` handler skips GitHub hosts when `githubRepos` is configured (implicit allow)
4. When `githubRepos` is nil/empty, no GitHub handlers are registered — GitHub falls through to normal `IsHostAllowed` checks (backward compatible)

## Files

**Created:**
- `/Users/khundeck/working/klankrmkr/sidecars/http-proxy/httpproxy/github.go` — `ExtractRepoFromPath`, `IsRepoAllowed`, `GitHubBlockedResponse`, `WithGitHubRepoFilter`, `githubHostsRegex`
- `/Users/khundeck/working/klankrmkr/sidecars/http-proxy/httpproxy/github_test.go` — Unit tests for all helper functions

**Modified:**
- `/Users/khundeck/working/klankrmkr/sidecars/http-proxy/httpproxy/proxy.go` — Added `githubRepos []string` to `proxyConfig`; added GitHub handler registration block before general CONNECT handler; plain-HTTP handler skips GitHub hosts when filter configured
- `/Users/khundeck/working/klankrmkr/sidecars/http-proxy/httpproxy/http_proxy_test.go` — Added 4 GitHub integration tests + `startGitHubFilterProxy` helper with custom dialer

## Decisions Made

1. **Implicit GitHub allow via plain-HTTP handler guard:** When `githubRepos` is non-empty, the general `OnRequest().DoFunc` skips GitHub hosts (`githubHostsRegex.MatchString(req.Host)`). This is simpler than trying to order OnRequest handlers — the GitHub-specific handler fires first and allows/blocks; the general handler just skips GitHub entirely.

2. **Custom test dialer:** Integration tests set `proxy.Tr` with a custom `DialContext` that redirects all connections to GitHub hostnames to a local `httptest.Server`. This fully exercises the OnRequest handler chain without needing real DNS, TLS, or network access.

3. **Plain-HTTP test path:** The integration tests exercise the plain-HTTP (`OnRequest`) path rather than the MITM CONNECT path. The CONNECT handler is tested implicitly (it's registered and would fire for HTTPS), but full MITM TLS testing would require `WithCustomCA` + a local TLS server.

## Test Results

All tests pass, `go vet` clean:

```
TestExtractRepoFromPath       PASS
TestIsRepoAllowed             PASS
TestGitHubBlockedResponse     PASS
TestHTTPProxy_GitHubAllowedRepo      PASS  (allowed repo 200, implicit host allow)
TestHTTPProxy_GitHubBlockedRepo      PASS  (blocked repo 403 JSON)
TestHTTPProxy_GitHubNonRepoPassthrough PASS (rate_limit URL passes through)
TestHTTPProxy_GitHubNoFilter         PASS  (no filter = GitHub blocked by IsHostAllowed)
```

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Integration test approach required custom dialer**
- **Found during:** Implementation
- **Issue:** Setting `req.Host = "github.com"` on requests to local test servers caused goproxy to forward to real github.com (via DNS), triggering TLS errors
- **Fix:** Used `proxy.Tr = &http.Transport{DialContext: ...}` to redirect github.com TCP connections to local test server; updated all integration tests to use `startGitHubFilterProxy` helper
- **Files modified:** `http_proxy_test.go`
- **Commit:** 734b83c

**2. [Rule 1 - Bug] Plain-HTTP general handler double-blocked GitHub hosts**
- **Found during:** First test run
- **Issue:** When `WithGitHubRepoFilter` allows a repo, goproxy GitHub OnRequest handler returns `(req, nil)` but the subsequent general `OnRequest().DoFunc` still called `IsHostAllowed("github.com", ...)` and blocked
- **Fix:** Added `githubHostsRegex` guard in general plain-HTTP handler: skip GitHub hosts when `cfg.githubRepos` is non-empty
- **Files modified:** `proxy.go`
- **Commit:** 734b83c

## Self-Check: PASSED
