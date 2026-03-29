---
phase: 28-github-repo-level-mitm-filtering-in-http-proxy
verified: 2026-03-28T21:10:00-04:00
status: passed
score: 11/11 must-haves verified
re_verification: false
---

# Phase 28: GitHub Repo-Level MITM Filtering Verification Report

**Phase Goal:** GitHub repo-level MITM filtering in HTTP proxy — MITM GitHub hosts to inspect URL paths and enforce allowedRepos at the network layer, mirroring Bedrock/Anthropic pattern
**Verified:** 2026-03-28T21:10:00-04:00
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| #  | Truth | Status | Evidence |
|----|-------|--------|----------|
| 1  | Proxy extracts owner/repo from github.com, api.github.com, raw.githubusercontent.com, codeload.githubusercontent.com URL paths | VERIFIED | `ExtractRepoFromPath` in `github.go` handles all four hosts; `TestExtractRepoFromPath` covers all 12 cases and passes |
| 2  | Proxy allows requests to repos in the allowedRepos list (case-insensitive) | VERIFIED | `IsRepoAllowed` normalises to lowercase; `TestHTTPProxy_GitHubAllowedRepo` PASS; `TestIsRepoAllowed` case-insensitive case PASS |
| 3  | Proxy blocks requests to repos NOT in the allowedRepos list with 403 JSON response | VERIFIED | `GitHubBlockedResponse` returns 403 with `{"error":"repo_not_allowed",...}`; `TestHTTPProxy_GitHubBlockedRepo` PASS |
| 4  | Non-repo GitHub URLs (login, rate_limit, session) pass through unblocked | VERIFIED | `ExtractRepoFromPath` returns `""` for single-segment paths; `OnRequest.DoFunc` returns `(req, nil)` on empty repo; `TestHTTPProxy_GitHubNonRepoPassthrough` PASS |
| 5  | Org wildcard patterns (owner/*) match all repos under that org | VERIFIED | `IsRepoAllowed` checks `strings.HasSuffix(a, "/*")` + org prefix; `TestIsRepoAllowed` wildcard cases PASS |
| 6  | When githubRepos is non-empty, GitHub hosts are implicitly allowed through the proxy (no need for them in allowedHosts) | VERIFIED | Plain-HTTP `OnRequest` guard at `proxy.go:495` skips GitHub hosts when `cfg.githubRepos` non-empty; `TestHTTPProxy_GitHubAllowedRepo` passes without github.com in allowedHosts |
| 7  | When githubRepos is nil/empty, no GitHub MITM handlers are registered and GitHub hosts are NOT implicitly allowed (backward compat) | VERIFIED | Handler registration gated on `len(cfg.githubRepos) > 0` at `proxy.go:426`; `TestHTTPProxy_GitHubNoFilter` shows GitHub blocked by `IsHostAllowed` PASS |
| 8  | Proxy main.go reads KM_GITHUB_ALLOWED_REPOS env var and passes it to NewProxy via WithGitHubRepoFilter | VERIFIED | `main.go:75-90` reads env var, parses CSV, calls `httpproxy.WithGitHubRepoFilter(githubAllowedRepos)` when non-empty |
| 9  | EC2 userdata systemd unit includes KM_GITHUB_ALLOWED_REPOS environment variable for the http-proxy service | VERIFIED | `userdata.go:294` has `Environment=KM_GITHUB_ALLOWED_REPOS={{ .GitHubAllowedRepos }}`; `TestUserDataGitHubAllowedRepos` and `TestUserDataGitHubAllowedReposEmpty` PASS |
| 10 | ECS service.hcl km-http-proxy container environment includes KM_GITHUB_ALLOWED_REPOS | VERIFIED | `service_hcl.go:207` has `{ name = "KM_GITHUB_ALLOWED_REPOS", value = "{{ .GitHubAllowedReposCSV }}" }`; `TestECSServiceHCLGitHubAllowedRepos` and `TestECSServiceHCLGitHubAllowedReposEmpty` PASS |
| 11 | When profile has no sourceAccess.github, the env var is empty and no GitHub filtering is active | VERIFIED | `joinGitHubAllowedRepos` and `joinGitHubAllowedReposCSV` are nil-safe, returning `""` when GitHub is nil; backward compat tests PASS |

**Score:** 11/11 truths verified

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `sidecars/http-proxy/httpproxy/github.go` | ExtractRepoFromPath, IsRepoAllowed, GitHubBlockedResponse, WithGitHubRepoFilter, githubHostsRegex | VERIFIED | File exists, 131 lines, all five exports present and substantive |
| `sidecars/http-proxy/httpproxy/github_test.go` | Unit tests for all github.go functions | VERIFIED | File exists, contains TestExtractRepoFromPath, TestIsRepoAllowed, TestGitHubBlockedResponse — all pass |
| `sidecars/http-proxy/httpproxy/proxy.go` | GitHub MITM handler registration in NewProxy, githubRepos field in proxyConfig | VERIFIED | `githubRepos []string` at line 55; MITM block at lines 426-465; general handler guard at line 495 |
| `sidecars/http-proxy/httpproxy/http_proxy_test.go` | Integration tests for GitHub MITM blocking | VERIFIED | Contains TestHTTPProxy_GitHubAllowedRepo, TestHTTPProxy_GitHubBlockedRepo, TestHTTPProxy_GitHubNonRepoPassthrough, TestHTTPProxy_GitHubNoFilter — all pass |
| `sidecars/http-proxy/main.go` | KM_GITHUB_ALLOWED_REPOS env var reading and WithGitHubRepoFilter option construction | VERIFIED | Lines 75-90 implement full env var reading, CSV parsing, and conditional option append |
| `pkg/compiler/userdata.go` | GitHubAllowedRepos field in userDataParams and Environment line in systemd unit template | VERIFIED | `GitHubAllowedRepos string` field at line 670; template line at 294; `joinGitHubAllowedRepos` helper at line 729 |
| `pkg/compiler/service_hcl.go` | KM_GITHUB_ALLOWED_REPOS in ECS proxy container environment block | VERIFIED | `GitHubAllowedReposCSV string` field at line 382; template entry at line 207; `joinGitHubAllowedReposCSV` helper at line 476 |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `proxy.go` | `github.go` | NewProxy calls ExtractRepoFromPath, IsRepoAllowed, GitHubBlockedResponse in OnRequest.DoFunc | WIRED | All three functions called at lines 444, 449, 462 |
| `proxy.go` | `goproxy.MitmConnect` | HandleConnectFunc registered BEFORE general CONNECT handler | WIRED | GitHub HandleConnectFunc at lines 430-439; general CONNECT handler at line 470 — correct ordering |
| `main.go` | `httpproxy/github.go` | main.go calls httpproxy.WithGitHubRepoFilter with parsed repos | WIRED | `httpproxy.WithGitHubRepoFilter(githubAllowedRepos)` at line 84 |
| `pkg/compiler/userdata.go` | `pkg/profile/types.go` | Reads p.Spec.SourceAccess.GitHub.AllowedRepos to populate GitHubAllowedRepos | WIRED | `p.Spec.SourceAccess.GitHub.AllowedRepos` read at lines 730, 733, 761 |
| `pkg/compiler/service_hcl.go` | `pkg/profile/types.go` | Reads p.Spec.SourceAccess.GitHub.AllowedRepos to populate proxy env var | WIRED | `p.Spec.SourceAccess.GitHub.AllowedRepos` read at lines 477, 480, 558, 561, 691, 694 |

---

### Requirements Coverage

| Requirement | Source Plans | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| NETW-08 | 28-01, 28-02 | GitHub source access controls allowlist repos, refs, and permissions (clone/fetch/push) | SATISFIED | Network-layer repo enforcement implemented: `ExtractRepoFromPath` + `IsRepoAllowed` in MITM proxy; KM_GITHUB_ALLOWED_REPOS wired through both EC2 and ECS compiler substrates; all 11 tests pass |

Note: REQUIREMENTS.md records NETW-08 as "Phase 2 | Complete" — this reflects the pre-existing GitHub App token and ref allowlist work from Phase 2. Phase 28 extends NETW-08 coverage by adding network-layer repo enforcement in the HTTP proxy, completing the requirement's network enforcement dimension.

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `sidecars/http-proxy/main.go` | 50 | TODO comment: wire real pricing.Client | Info | Pre-existing in budget enforcement block; unrelated to Phase 28 |

No anti-patterns in Phase 28 code.

---

### Human Verification Required

None — all observable behaviors are verifiable programmatically via unit and integration tests.

---

### Test Results Summary

```
TestExtractRepoFromPath              PASS  (12 cases: all four GitHub hosts, port stripping, case normalization)
TestIsRepoAllowed                    PASS  (7 cases: exact, not-in-list, case-insensitive, wildcard, wrong-org, empty, github.com/ prefix)
TestGitHubBlockedResponse            PASS  (403 status, JSON body with error/repo/reason fields)
TestHTTPProxy_GitHubAllowedRepo      PASS  (allowed repo 200, implicit host allow without github.com in allowedHosts)
TestHTTPProxy_GitHubBlockedRepo      PASS  (blocked repo 403 JSON)
TestHTTPProxy_GitHubNonRepoPassthrough PASS (api.github.com/rate_limit passes through)
TestHTTPProxy_GitHubNoFilter         PASS  (no filter = GitHub blocked by IsHostAllowed)
TestUserDataGitHubAllowedRepos       PASS  (systemd unit contains KM_GITHUB_ALLOWED_REPOS=myorg/myrepo,other/repo)
TestUserDataGitHubAllowedReposEmpty  PASS  (no non-empty value when GitHub config absent)
TestECSServiceHCLGitHubAllowedRepos  PASS  (container env contains KM_GITHUB_ALLOWED_REPOS with CSV value)
TestECSServiceHCLGitHubAllowedReposEmpty PASS (backward compat without GitHub config)
go vet ./sidecars/http-proxy/... ./pkg/compiler/...  CLEAN
```

---

### Gaps Summary

No gaps. All must-haves from Plans 01 and 02 are verified against the actual codebase. The implementation matches the plan specification exactly.

---

_Verified: 2026-03-28T21:10:00-04:00_
_Verifier: Claude (gsd-verifier)_
