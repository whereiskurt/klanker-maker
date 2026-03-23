---
phase: 13
slug: github-app-token-integration-scoped-repo-access-for-sandboxes
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-22
---

# Phase 13 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — standard Go test infrastructure |
| **Quick run command** | `go test ./pkg/aws/... ./internal/app/cmd/... ./pkg/compiler/...` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/aws/... ./internal/app/cmd/... ./pkg/compiler/...`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 13-01-01 | 01 | 1 | GitHub App JWT + token generation | unit | `go test ./pkg/aws/... -run TestGitHub` | ❌ W0 | ⬜ pending |
| 13-01-02 | 01 | 1 | SSM token storage | unit | `go test ./pkg/aws/... -run TestGitHub` | ❌ W0 | ⬜ pending |
| 13-02-01 | 02 | 1 | Token refresh Lambda | unit | `go test ./cmd/github-token-refresher/...` | ❌ W0 | ⬜ pending |
| 13-02-02 | 02 | 1 | Terraform module | shell | `test -f infra/modules/github-token/v1.0.0/main.tf` | ❌ W0 | ⬜ pending |
| 13-03-01 | 03 | 2 | Compiler integration | unit | `go test ./pkg/compiler/... -run TestGitHub` | ❌ W0 | ⬜ pending |
| 13-03-02 | 03 | 2 | GIT_ASKPASS helper | shell | `test -f scripts/km-git-askpass` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] GitHub App API client package stubs in `pkg/aws/`
- [ ] Token refresh Lambda entry point stub

*Test files created by tasks themselves (TDD pattern).*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| GitHub App token scoped to specific repos | Token scoping | Requires real GitHub App installation | Create GitHub App, install on test org, generate token, verify repo scope |
| Token refresh before 1hr expiry | Lambda refresh | Requires real AWS Lambda + EventBridge | Deploy Lambda, wait 45min, verify new token in SSM |
| GIT_ASKPASS reads from SSM at git time | Credential helper | Requires real EC2 instance with SSM access | SSM start-session, run `git clone`, verify token not in env |

*GitHub App integration is an external API — unit tests verify JWT/token logic, real enforcement requires deployment.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
