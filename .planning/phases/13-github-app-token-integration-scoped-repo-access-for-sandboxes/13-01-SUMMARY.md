---
phase: 13-github-app-token-integration-scoped-repo-access-for-sandboxes
plan: "01"
subsystem: github-token
tags: [github, jwt, lambda, ssm, tdd, auth]
dependency_graph:
  requires: []
  provides: [pkg/github, cmd/github-token-refresher]
  affects: [pkg/compiler, internal/app/cmd]
tech_stack:
  added: [github.com/golang-jwt/jwt/v5@v5.3.1]
  patterns: [narrow-interface, tdd-red-green, injectable-base-url, slog-json-audit]
key_files:
  created:
    - pkg/github/token.go
    - pkg/github/token_test.go
    - cmd/github-token-refresher/main.go
  modified:
    - go.mod
    - go.sum
decisions:
  - "[Phase 13-01]: GitHubAPIBaseURL is a package-level var in pkg/github — tests inject httptest.Server URL without modifying function signatures"
  - "[Phase 13-01]: MockSSMClient exported from pkg/github (not in _test file) — external test packages (cmd/github-token-refresher) can use it without duplicating"
  - "[Phase 13-01]: PKCS#1 tried first, PKCS#8 as fallback — matches GitHub's own key export behavior (older App keys are PKCS#1, newer are PKCS#8)"
  - "[Phase 13-01]: write supersedes read in CompilePermissions — single pass, write flag set on push, clone/fetch check before overwriting write"
  - "[Phase 13-01]: Lambda reads private key at startup from SSM (not per-invocation) — cold start cost acceptable; key rarely changes"
  - "[Phase 13-01]: slog/log JSON handler writes to os.Stdout — CloudWatch automatically captures Lambda stdout as structured log events"
metrics:
  duration: 499s
  completed: "2026-03-23T03:06:53Z"
  tasks_completed: 2
  files_created: 3
  files_modified: 2
---

# Phase 13 Plan 01: GitHub App Token Library Summary

**One-liner:** RS256 JWT generation with PKCS#1/PKCS#8 key parsing, installation token exchange via httptest-injectable GitHub API client, and SSM-backed Lambda refresher with structured slog audit logging.

## What Was Built

### pkg/github/token.go (364 lines)

Core library implementing all token operations:

- **`GenerateGitHubAppJWT(appClientID string, privateKeyPEM []byte) (string, error)`** — Mints RS256 JWT with `iss=appClientID`, `iat=now-60s`, `exp=now+10min`. Tries PKCS#1 (`ParsePKCS1PrivateKey`), falls back to PKCS#8 (`ParsePKCS8PrivateKey`). Returns typed error for non-RSA keys and invalid PEM.

- **`ExchangeForInstallationToken(ctx, jwt, installationID, repos, perms) (string, error)`** — POSTs to `{GitHubAPIBaseURL}/app/installations/{id}/access_tokens` with correct headers (`Authorization: Bearer`, `Accept: application/vnd.github+json`, `X-GitHub-Api-Version: 2022-11-28`). Strips org prefix from repo names. Returns 201 token or error with HTTP status code.

- **`CompilePermissions(permissions []string) map[string]string`** — Maps `clone`/`fetch` to `contents:read`, `push` to `contents:write`; write supersedes read in a single pass.

- **`WriteTokenToSSM(ctx, client, sandboxID, token, kmsKeyARN, overwrite) error`** — Writes to `/sandbox/{sandbox-id}/github-token` as SecureString. Narrow `SSMAPI` interface (PutParameter only) follows S3PutAPI/CWLogsAPI pattern.

- **`TokenRefreshHandler`** — Struct with `SSMClient`, `Logger`, `AppClientID`, `PrivateKeyPEM`, and injectable `GenerateJWTFn`. `HandleTokenRefresh()` runs the full pipeline and logs `token_generated` (INFO) or `token_generation_failed` (ERROR) as structured JSON.

- **`MockSSMClient`** — Exported test double tracking `PutParameterCallCount`, `LastName`, `LastValue`, `LastKeyID`, `LastOverwrite`.

### pkg/github/token_test.go (474 lines)

17 unit tests covering:
- PKCS#1 JWT generation
- PKCS#8 JWT generation
- Non-RSA key rejection
- Invalid PEM rejection
- Claims verification (iss, iat within 60s±10s, exp within 600s±50s)
- All 5 CompilePermissions scenarios
- ExchangeForInstallationToken success (header/body verification)
- Non-201 response error with status code
- Org-prefix stripping (short repo names in request body)
- WriteTokenToSSM success and error paths
- Lambda handler audit log on success (contains sandbox_id, token_generated, allowed_repos)
- Lambda handler audit log on failure (contains token_generation_failed, sandbox_id)

### cmd/github-token-refresher/main.go (122 lines)

Lambda entry point following budget-enforcer pattern:
- Reads `KM_AWS_PROFILE` env var (empty in Lambda, uses execution role)
- Reads private key PEM from SSM at `/km/config/github/private-key`
- Reads App client ID from SSM at `/km/config/github/app-client-id`
- Constructs `slog.NewJSONHandler(os.Stdout)` logger for CloudWatch capture
- Starts Lambda runtime with `h.HandleTokenRefresh`

## Verification

```
go test ./pkg/github/... -v -count=1   → 17/17 PASS
go build ./cmd/github-token-refresher/ → BUILD OK
grep GITHUB_TOKEN pkg/github/ cmd/github-token-refresher/ → no matches (SSM-only)
```

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

| Item | Status |
|------|--------|
| pkg/github/token.go | FOUND |
| pkg/github/token_test.go | FOUND |
| cmd/github-token-refresher/main.go | FOUND |
| Commit 8c7e3a6 (RED: failing tests) | FOUND |
| Commit d88d357 (GREEN: implementation) | FOUND |
