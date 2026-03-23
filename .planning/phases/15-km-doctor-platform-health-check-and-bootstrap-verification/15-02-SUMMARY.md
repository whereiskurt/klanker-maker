---
phase: 15-km-doctor-platform-health-check-and-bootstrap-verification
plan: "02"
subsystem: configure-github
tags: [github-app, manifest-flow, ssm, oauth, cobra]
dependency_graph:
  requires:
    - internal/app/cmd/configure_github.go (pre-existing --non-interactive flow)
    - pkg/github/token.go (GenerateGitHubAppJWT, GitHubAPIBaseURL pattern)
  provides:
    - km configure github --setup (one-click GitHub App manifest flow)
    - BuildManifestJSON (exported for tests)
    - ReceiveManifestCodeWithPortCb (exported for tests)
    - ExchangeManifestCode (exported for tests)
    - RunConfigureGitHubSetup (exported for tests)
  affects:
    - internal/app/cmd/configure_github.go
    - internal/app/cmd/configure_github_test.go
tech_stack:
  added:
    - net/http local callback server (random port via net.Listen tcp 0)
    - encoding/json for manifest construction and GitHub API response parsing
    - os/exec + runtime.GOOS for cross-platform browser open
  patterns:
    - githubManifestBaseURL package-level var (follows GitHubAPIBaseURL pattern from pkg/github/token.go)
    - ReceiveManifestCodeWithPortCb port callback for test synchronization
    - httptest.NewServer mocking both manifest exchange and installations APIs
    - real RSA key generation (crypto/rand + rsa.GenerateKey) for JWT-dependent tests
key_files:
  modified:
    - internal/app/cmd/configure_github.go (extended with --setup flag and full manifest flow)
    - internal/app/cmd/configure_github_test.go (added 8 new tests for manifest flow)
decisions:
  - githubManifestBaseURL package-level var for test injection — same pattern as GitHubAPIBaseURL in pkg/github/token.go
  - ReceiveManifestCodeWithPortCb port callback variant — allows tests to send HTTP request before timeout fires without polling
  - redirect_url in manifest JSON body (not query param) — GitHub manifest flow requires it in the body
  - fetchInstallations uses App ID (int64 as string) as JWT issuer — GitHub manifest flow returns numeric ID not client_id
  - real RSA key generated in TestRunSetup_FullFlow — validTestPEM is not a parseable key; JWT generation requires real RSA key
  - openBrowser is non-fatal — operator always gets printed URL as fallback
metrics:
  duration: "506s"
  completed_date: "2026-03-23"
  tasks_completed: 2
  files_modified: 2
---

# Phase 15 Plan 02: GitHub App Manifest Flow Summary

**One-liner:** GitHub App manifest flow via `km configure github --setup` — browser-based one-click App creation, local callback server on random port, code exchange via POST to GitHub API, automatic SSM storage of App ID, PEM, and installation ID.

## What Was Built

Extended `internal/app/cmd/configure_github.go` with a complete `--setup` manifest flow:

1. `--setup` flag registered on `km configure github`
2. `BuildManifestJSON(redirectURL)` — constructs manifest JSON with name, url, public=false, default_permissions.contents=write, hook_attributes.active=false, redirect_url
3. `openBrowser(url)` — cross-platform browser opener (darwin: open, linux: xdg-open, windows: cmd /c start), non-blocking via .Start()
4. `ReceiveManifestCode` / `ReceiveManifestCodeWithPortCb` — local HTTP server on random port (net.Listen tcp 127.0.0.1:0), serves /github-app-setup, extracts `code` query param, port callback for test synchronization
5. `ExchangeManifestCode` — POST to `{baseURL}/app-manifests/{code}/conversions`, parses manifestConversionResponse (id, client_id, pem, webhook_secret, html_url)
6. `fetchInstallations` — GET `/app/installations` with GitHub App JWT, returns []installationInfo
7. `RunConfigureGitHubSetup` — exported orchestrator: exchange code → write app-client-id + private-key → fetch installations → if found: write installation-id; if not: print install instructions
8. `runConfigureGitHubSetupInteractive` — production entry point that starts callback server and opens browser before calling RunConfigureGitHubSetup
9. Updated inline Long help text to document --setup flag, browser fallback, and no-installation instructions

## Tests Added

| Test | Description |
|------|-------------|
| TestConfigureGitHubSetup_FlagRegistered | --setup flag registered with default=false |
| TestManifestJSON_Structure | manifest JSON has required fields, correct types |
| TestReceiveManifestCode_DirectHit | local server receives code via HTTP, returns it |
| TestReceiveManifestCode_Timeout | 1-second timeout returns error containing "timeout" |
| TestExchangeManifestCode_Success | httptest mock returns 201 with credentials |
| TestExchangeManifestCode_APIError | 422 response returns error containing "422" |
| TestRunSetup_FullFlow | full flow writes 3 SSM params (real RSA key for JWT) |
| TestRunSetup_NoInstallations | empty installations → prints install instructions, skips installation-id |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] ReceiveManifestCode needs port callback for test synchronization**
- **Found during:** Task 1
- **Issue:** Plan specified `receiveManifestCode` returns `(code, port, error)` blocking until code received. Tests cannot send HTTP request without knowing the port first — a race condition.
- **Fix:** Added `ReceiveManifestCodeWithPortCb(ctx, timeout, portCb func(int))` variant that calls `portCb(port)` immediately after the listener is bound, before waiting for the code. Base `ReceiveManifestCode` delegates to this with a no-op callback.
- **Files modified:** internal/app/cmd/configure_github.go, internal/app/cmd/configure_github_test.go

**2. [Rule 1 - Bug] TestRunSetup_FullFlow failed with fake PEM**
- **Found during:** Task 1 (GREEN phase)
- **Issue:** The `validTestPEM` test constant is not a parseable RSA key. `fetchInstallations` calls `GenerateGitHubAppJWT` which requires a real RSA key. Test was taking the "no installations" path despite mock returning one installation.
- **Fix:** Added `realTestPEM(t)` helper that generates a real 2048-bit RSA key on the fly for tests requiring JWT generation. Used in `TestRunSetup_FullFlow`.
- **Files modified:** internal/app/cmd/configure_github_test.go

**3. [Rule 3 - Info] Pre-existing TestDestroyCmd_InvalidSandboxID failures**
- **Found during:** Full suite run
- **Status:** Out of scope — pre-existing failures present before this plan's changes. Logged for deferred handling.
- **Committed to deferred-items:** No (not blocking)

## Self-Check

### Files exist

- [x] `/Users/khundeck/working/klankrmkr/internal/app/cmd/configure_github.go` — FOUND
- [x] `/Users/khundeck/working/klankrmkr/internal/app/cmd/configure_github_test.go` — FOUND

### Commits exist

- [x] `0d0e05d` — test(15-02): add failing tests for --setup manifest flow
- [x] `7fa9d38` — feat(15-02): implement --setup manifest flow for km configure github

## Self-Check: PASSED
