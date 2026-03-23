---
phase: 13-github-app-token-integration-scoped-repo-access-for-sandboxes
verified: 2026-03-22T00:00:00Z
status: passed
score: 22/22 must-haves verified
re_verification: false
---

# Phase 13: GitHub App Token Integration Verification Report

**Phase Goal:** GitHub App-based scoped repository access for sandboxes — each sandbox gets a short-lived installation token with only the permissions its profile declares, refreshed automatically by a Lambda on an EventBridge schedule.
**Verified:** 2026-03-22
**Status:** PASSED
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

All truths derived from PLAN frontmatter must_haves across the four plans (01-04).

| #  | Truth | Status | Evidence |
|----|-------|--------|----------|
| 1  | GenerateGitHubAppJWT produces a valid RS256 JWT with correct iss/iat/exp claims | VERIFIED | pkg/github/token.go:54-92 — RS256 JWT with iat=now-60s, exp=now+10min, iss=appClientID |
| 2  | ExchangeForInstallationToken sends correctly scoped POST to GitHub API | VERIFIED | pkg/github/token.go:119-167 — POST to /app/installations/{id}/access_tokens with all required headers |
| 3  | CompilePermissions maps clone/fetch to contents:read and push to contents:write | VERIFIED | pkg/github/token.go:188-202 — write supersedes read in single pass |
| 4  | repoShortName strips org prefix from org/repo format | VERIFIED | pkg/github/token.go:171-176 — strips via LastIndex("/") |
| 5  | PKCS#1 and PKCS#8 private key formats both parse correctly | VERIFIED | pkg/github/token.go:62-77 — PKCS#1 tried first, PKCS#8 fallback |
| 6  | Lambda entry point calls token refresh pipeline and writes to SSM | VERIFIED | cmd/github-token-refresher/main.go:121 — lambda.Start(h.HandleTokenRefresh); handler calls WriteTokenToSSM |
| 7  | Lambda logs structured token generation event (sandbox_id, allowed_repos) to CloudWatch | VERIFIED | pkg/github/token.go:320-327 — slog.Info with event, sandbox_id, allowed_repos, permissions, success fields |
| 8  | github-token Terraform module creates Lambda + EventBridge Scheduler + SSM + KMS resources | VERIFIED | infra/modules/github-token/v1.0.0/main.tf — 10 resources: aws_kms_key, aws_kms_alias, 2x aws_iam_role, 2x aws_iam_role_policy, aws_lambda_function, aws_cloudwatch_log_group, aws_scheduler_schedule, aws_lambda_permission |
| 9  | EventBridge schedule fires every 45 minutes for token refresh | VERIFIED | infra/modules/github-token/v1.0.0/main.tf:229 — schedule_expression = "rate(45 minutes)" |
| 10 | Lambda IAM role can read /km/config/github/* SSM params and write /sandbox/{sandbox-id}/github-token | VERIFIED | infra/modules/github-token/v1.0.0/main.tf:96-133 — ReadGitHubAppConfig + WriteGitHubToken statements |
| 11 | KMS key policy grants Lambda encrypt and sandbox IAM role decrypt | VERIFIED | infra/modules/github-token/v1.0.0/main.tf:19-56 — AllowLambdaEncrypt (Encrypt/Decrypt/GenerateDataKey) + AllowSandboxDecrypt (Decrypt only) |
| 12 | SCP trusted_arns_ssm allows github-token-refresher Lambda through | VERIFIED | infra/modules/scp/v1.0.0/main.tf:31 — "arn:aws:iam::${var.application_account_id}:role/km-github-token-refresher-*" |
| 13 | Makefile build-lambdas target produces github-token-refresher.zip | VERIFIED | Makefile:72-78 — GOOS=linux GOARCH=arm64 go build + zip -j github-token-refresher.zip bootstrap |
| 14 | userdata.go section 4 writes GIT_ASKPASS credential helper script instead of exporting GITHUB_TOKEN env var | VERIFIED | pkg/compiler/userdata.go:105-126 — km-git-askpass script reads /sandbox/${SANDBOX_ID}/github-token at git time; no GITHUB_TOKEN export anywhere |
| 15 | Sandbox reads GitHub token from SSM at git-operation time via GIT_ASKPASS — token never in environment | VERIFIED | pkg/compiler/userdata.go:112-116 — SSM get-parameter call inside km-git-askpass heredoc |
| 16 | security.go no longer injects /km/github/app-token into SecretPaths for GitHub profiles | VERIFIED | pkg/compiler/security.go — grep finds only comment mentioning /sandbox/{id}/github-token path, not the old /km/github/app-token injection |
| 17 | service_hcl.go emits github_token_inputs block in both EC2 and ECS templates when sourceAccess.github is set | VERIFIED | pkg/compiler/service_hcl.go:85-89, 295-299 — github_token_inputs block in both templates, guarded by HasGitHub |
| 18 | compiler.go populates GitHubTokenHCL field in CompiledArtifacts when sourceAccess.github is set | VERIFIED | pkg/compiler/compiler.go:40-43, 122, 136, 170, 184 — GitHubTokenHCL field + generateGitHubTokenHCL called in compileEC2 and compileECS |
| 19 | km configure github prompts for App Client ID, private key PEM file, installation ID and writes to SSM | VERIFIED | internal/app/cmd/configure_github.go:165-183 — PutParameter calls for /km/config/github/app-client-id (String), /km/config/github/private-key (SecureString), /km/config/github/installation-id (String) |
| 20 | km create generates GitHub App installation token at sandbox creation and writes to SSM | VERIFIED | internal/app/cmd/create.go:449-491 — generateAndStoreGitHubToken reads SSM, calls GenerateGitHubAppJWT + ExchangeForInstallationToken + WriteTokenToSSM |
| 21 | km destroy cleans up SSM parameter, EventBridge schedule, and github-token/ Terragrunt directory before main destroy | VERIFIED | internal/app/cmd/destroy.go:187-202 — Step 7c terragrunt destroy + SSM DeleteParameter for /sandbox/{id}/github-token |
| 22 | Cleanup in destroy is non-fatal — proceeds with main destroy even if github-token cleanup fails | VERIFIED | internal/app/cmd/destroy.go:196 — "github-token destroy failed (non-fatal — proceeding with main sandbox destroy)" |

**Score:** 22/22 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/github/token.go` | JWT generation, token exchange, permission mapping, SSM write | VERIFIED | 364 lines, fully implemented, all 4 exported functions present |
| `pkg/github/token_test.go` | Unit tests (min 100 lines) | VERIFIED | 474 lines, 17 tests covering all code paths |
| `cmd/github-token-refresher/main.go` | Lambda entry point (min 40 lines) | VERIFIED | 122 lines, reads SSM config, starts lambda.Start |
| `infra/modules/github-token/v1.0.0/main.tf` | Lambda + EventBridge + SSM IAM + KMS | VERIFIED | 272 lines, 10 resources |
| `infra/modules/github-token/v1.0.0/variables.tf` | Module input variables | VERIFIED | sandbox_id, lambda_zip_path, installation_id, ssm_parameter_name, allowed_repos, permissions, sandbox_iam_role_arn |
| `infra/modules/github-token/v1.0.0/outputs.tf` | Lambda ARN, KMS key ARN, KMS key ID | VERIFIED | 3 outputs present |
| `infra/modules/scp/v1.0.0/main.tf` | SCP with github-token-refresher carve-out | VERIFIED | km-github-token-refresher-* in trusted_arns_ssm only |
| `Makefile` | Lambda build target for github-token-refresher | VERIFIED | build lines on lines 72-73, output listed on line 78 |
| `pkg/compiler/userdata.go` | GIT_ASKPASS credential helper injection (EC2 only) | VERIFIED | km-git-askpass script at section 4, no GITHUB_TOKEN export |
| `pkg/compiler/security.go` | No /km/github/app-token stub | VERIFIED | Old path removed, updated comment only |
| `pkg/compiler/service_hcl.go` | github_token_inputs template block | VERIFIED | Block in both EC2 and ECS templates |
| `pkg/compiler/compiler.go` | GitHubTokenHCL field + generation logic | VERIFIED | Field added to CompiledArtifacts, called in compileEC2/compileECS |
| `pkg/compiler/github_token_hcl.go` | generateGitHubTokenHCL (new file) | VERIFIED | Created, mirrors budget-enforcer HCL pattern |
| `internal/app/cmd/configure_github.go` | km configure github subcommand | VERIFIED | NewConfigureGitHubCmd + NewConfigureGitHubCmdWithDeps (DI) |
| `internal/app/cmd/configure_github_test.go` | Tests for configure github (min 40 lines) | VERIFIED | 293 lines, 8 tests |
| `internal/app/cmd/root.go` | configure github subcommand registration | VERIFIED | configureCmd.AddCommand(NewConfigureGitHubCmd(cfg)) |
| `internal/app/cmd/create.go` | GitHub token generation + github-token/ deploy (Steps 13a + 13b) | VERIFIED | generateAndStoreGitHubToken helper + github-token/ Terragrunt deploy, both non-fatal |
| `internal/app/cmd/destroy.go` | GitHub token cleanup (Step 7c) | VERIFIED | terragrunt destroy on github-token/ + SSM DeleteParameter, non-fatal |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| cmd/github-token-refresher/main.go | pkg/github/token.go | import + TokenRefreshHandler struct | WIRED | main.go imports githubpkg and uses TokenRefreshHandler{} + lambda.Start(h.HandleTokenRefresh) |
| infra/modules/github-token/v1.0.0/main.tf | cmd/github-token-refresher/ | lambda_zip_path variable | WIRED | variables.tf defines lambda_zip_path; main.tf uses it at filename = var.lambda_zip_path |
| infra/modules/scp/v1.0.0/main.tf | infra/modules/github-token/v1.0.0/main.tf | trusted_arns_ssm carve-out | WIRED | "km-github-token-refresher-*" in trusted_arns_ssm; role name in main.tf is "km-github-token-refresher-${var.sandbox_id}" |
| pkg/compiler/userdata.go | /sandbox/{sandbox-id}/github-token SSM path | GIT_ASKPASS script reads SSM at git time | WIRED | km-git-askpass reads --name "/sandbox/${SANDBOX_ID}/github-token" |
| pkg/compiler/service_hcl.go | infra/modules/github-token/v1.0.0/ | github_token_inputs block consumed by Terragrunt | WIRED | github_token_inputs block in both EC2/ECS templates; github_token_hcl.go terraform source = ".../infra/modules/github-token/v1.0.0" |
| internal/app/cmd/configure_github.go | SSM /km/config/github/* | PutParameter calls | WIRED | Three PutParameter calls for app-client-id, private-key, installation-id |
| internal/app/cmd/create.go | pkg/github/token.go | generateAndStoreGitHubToken helper | WIRED | Calls githubpkg.GenerateGitHubAppJWT + githubpkg.ExchangeForInstallationToken + githubpkg.WriteTokenToSSM |
| internal/app/cmd/create.go | pkg/compiler/compiler.go | CompiledArtifacts.GitHubTokenHCL written to github-token/terragrunt.hcl | WIRED | artifacts.GitHubTokenHCL != "" guard + os.WriteFile to github-token/terragrunt.hcl |
| internal/app/cmd/destroy.go | SSM /sandbox/{sandbox-id}/github-token | DeleteParameter cleanup | WIRED | /sandbox/%s/github-token format string used in cleanup |

### Requirements Coverage

GH-xx requirement IDs are defined inline in ROADMAP.md Phase 13 (not in REQUIREMENTS.md, which has no GH-prefix section). All 13 unique GH requirement IDs from plan frontmatter are satisfied:

| Requirement | Source Plan(s) | Description (inferred from ROADMAP) | Status |
|-------------|---------------|--------------------------------------|--------|
| GH-01 | 13-04 | km configure github subcommand | SATISFIED |
| GH-02 | 13-03 | GIT_ASKPASS credential helper in userdata (no env var export) | SATISFIED |
| GH-03 | 13-01, 13-04 | JWT generation + CLI wiring at create time | SATISFIED |
| GH-04 | 13-03 | Compiler emits github_token_inputs block | SATISFIED |
| GH-05 | 13-03, 13-04 | permission mapping clone/fetch/push in compiler + create | SATISFIED |
| GH-06 | 13-02 | Terraform module for Lambda + EventBridge infra | SATISFIED |
| GH-07 | 13-02 | EventBridge Scheduler 45-minute rate | SATISFIED |
| GH-08 | 13-01 | PKCS#1 and PKCS#8 key parsing | SATISFIED |
| GH-09 | 13-04 | km destroy github-token cleanup | SATISFIED |
| GH-10 | 13-02 | SCP carve-out for github-token-refresher Lambda | SATISFIED |
| GH-11 | 13-03 | GitHubTokenHCL field in CompiledArtifacts | SATISFIED |
| GH-12 | 13-03 | security.go removes /km/github/app-token stub | SATISFIED |
| GH-13 | 13-01, 13-02 | Token audit logging to CloudWatch via slog JSON | SATISFIED |

Note: REQUIREMENTS.md does not define GH-prefix IDs — these are phase-local requirement identifiers defined in the ROADMAP.md Phase 13 section. NETW-08 in REQUIREMENTS.md ("GitHub source access controls allowlist repos, refs, and permissions") is the closest top-level requirement and is now satisfied by Phase 13.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| pkg/compiler/github_token_hcl.go | 65 | `installation_id = ""` (comment: "populated at apply time from SSM or operator config") | Info | By design — the HCL template does not embed the installation_id at compile time; it is resolved at `terragrunt apply` time. This is an intentional design decision, not a stub. |

No blockers. No placeholder implementations. No TODO/FIXME markers in phase-modified files. No GITHUB_TOKEN env var pattern anywhere in new code.

### Test Suite Results

All three affected test packages pass cleanly:

- `go test ./pkg/github/... -count=1` — PASS (17 tests)
- `go test ./pkg/compiler/... -count=1` — PASS
- `go test ./internal/app/cmd/... -count=1` — PASS
- `go build ./cmd/github-token-refresher/` — BUILD OK

### Human Verification Required

The following items require human/deployment verification and cannot be checked programmatically:

#### 1. End-to-end token flow against real GitHub API

**Test:** Run `km configure github` with a real GitHub App, then `km create` with a profile containing `sourceAccess.github`. Check CloudWatch logs for the token_generated structured event.
**Expected:** Token appears in SSM at /sandbox/{id}/github-token; Lambda deploys and writes a fresh token every 45 minutes.
**Why human:** Requires a real GitHub App registration and live AWS environment.

#### 2. GIT_ASKPASS credential helper works inside a running sandbox

**Test:** SSH into a provisioned EC2 sandbox and run `git clone https://github.com/org/repo` for a repo in allowedRepos.
**Expected:** Clone succeeds without password prompt; `echo $GIT_ASKPASS` returns `/opt/km/bin/km-git-askpass`; `km-git-askpass Password` returns the installation token.
**Why human:** Requires a live EC2 sandbox with GitHub token provisioned.

#### 3. Token scoping is enforced by GitHub

**Test:** Try to clone a repo NOT in allowedRepos using the generated token.
**Expected:** GitHub returns 403 Forbidden — token is scoped to the declared repos only.
**Why human:** Requires live GitHub API call with a real installation token.

#### 4. km destroy clean-up is idempotent

**Test:** Run `km destroy` on a sandbox, then run it again.
**Expected:** Second destroy succeeds without error even though github-token SSM parameter is gone (ParameterNotFound swallowed).
**Why human:** Requires a live sandbox destroy cycle.

### Gaps Summary

No gaps found. All 22 observable truths are verified against actual codebase artifacts. All test suites pass. The Lambda binary compiles cleanly. No placeholder or stub implementations were found.

The one intentional design choice that could appear stub-like — `installation_id = ""` in the generated Terragrunt HCL — is explicitly documented in the SUMMARY as a design decision: the installation ID is resolved at `terragrunt apply` time from the EventBridge Scheduler payload, not baked into the HCL at compile time.

---

_Verified: 2026-03-22_
_Verifier: Claude (gsd-verifier)_
