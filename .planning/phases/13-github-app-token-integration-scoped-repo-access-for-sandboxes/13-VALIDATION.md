---
phase: 13
slug: github-app-token-integration-scoped-repo-access-for-sandboxes
status: draft
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-22
---

# Phase 13 -- Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none -- standard Go test infrastructure |
| **Quick run command** | `go test ./pkg/github/... ./internal/app/cmd/... ./pkg/compiler/...` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/github/... ./internal/app/cmd/... ./pkg/compiler/...`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 13-01-01 | 01 | 1 | GitHub App JWT + token generation + audit logging | unit (TDD) | `go test ./pkg/github/... -run TestGitHub` | TDD creates | pending |
| 13-02-01 | 02 | 1 | Terraform module | shell | `grep -c resource infra/modules/github-token/v1.0.0/main.tf && terraform fmt -check infra/modules/github-token/v1.0.0/` | n/a | pending |
| 13-02-02 | 02 | 1 | SCP + Makefile | shell | `grep km-github-token-refresher infra/modules/scp/v1.0.0/main.tf && grep github-token-refresher Makefile` | n/a | pending |
| 13-03-01 | 03 | 2 | GIT_ASKPASS credential helper (EC2) | unit (TDD) | `go test ./pkg/compiler/... -run "TestGitHub\|TestUserData\|TestSecret"` | TDD creates | pending |
| 13-03-02 | 03 | 2 | github_token_inputs + GitHubTokenHCL | unit (TDD) | `go test ./pkg/compiler/... -run "TestGitHub\|TestServiceHCL"` | TDD creates | pending |
| 13-04-01 | 04 | 3 | km configure github | unit (TDD) | `go test ./internal/app/cmd/... -run TestConfigureGitHub` | TDD creates | pending |
| 13-04-02 | 04 | 3 | km create/destroy token wiring | unit | `go test ./internal/app/cmd/... -run "TestCreate\|TestDestroy"` | TDD creates | pending |

*Status: pending -- green -- red -- flaky*

---

## Nyquist Compliance Notes

All plans use TDD (`tdd="true"` on tasks or `type: tdd` on plans). Tests are created before implementation within the same task's RED-GREEN-REFACTOR cycle. No separate Wave 0 plan is required because:

1. Plan 01 is `type: tdd` -- tests are written first by definition
2. Plans 03 and 04 have `tdd="true"` on code-producing tasks -- tests precede implementation
3. Plan 02 is infrastructure (Terraform HCL) verified by structural checks, not Go tests

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| GitHub App token scoped to specific repos | Token scoping | Requires real GitHub App installation | Create GitHub App, install on test org, generate token, verify repo scope |
| Token refresh before 1hr expiry | Lambda refresh | Requires real AWS Lambda + EventBridge | Deploy Lambda, wait 45min, verify new token in SSM |
| GIT_ASKPASS reads from SSM at git time | Credential helper | Requires real EC2 instance with SSM access | SSM start-session, run `git clone`, verify token not in env |

*GitHub App integration is an external API -- unit tests verify JWT/token logic, real enforcement requires deployment.*

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or TDD creates tests inline
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Nyquist compliance via TDD pattern (tests created before implementation)
- [x] No watch-mode flags
- [x] Feedback latency < 30s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
