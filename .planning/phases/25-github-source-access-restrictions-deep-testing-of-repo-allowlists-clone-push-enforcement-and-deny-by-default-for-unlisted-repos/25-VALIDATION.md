---
phase: 25
slug: github-source-access-restrictions-deep-testing-of-repo-allowlists-clone-push-enforcement-and-deny-by-default-for-unlisted-repos
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-26
---

# Phase 25 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` (stdlib) |
| **Config file** | `go.mod` — no separate test config |
| **Quick run command** | `go test ./pkg/github/... ./pkg/compiler/... -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/github/... ./pkg/compiler/... -count=1`
- **After every plan wave:** Run `go test ./... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 25-01-01 | 01 | 1 | deny-by-default | unit | `go test ./pkg/compiler/... -run TestServiceHCL` | ✅ | ⬜ pending |
| 25-01-02 | 01 | 1 | empty-repos-deny | unit | `go test ./pkg/compiler/... -run TestCompileEC2EmptyAllowedRepos` | ❌ W0 | ⬜ pending |
| 25-01-03 | 01 | 1 | clone-permissions | unit | `go test ./pkg/github/... -run TestCompilePermissions` | ✅ | ⬜ pending |
| 25-01-04 | 01 | 1 | push-permissions | unit | `go test ./pkg/github/... -run TestCompilePermissions` | ✅ | ⬜ pending |
| 25-02-01 | 02 | 1 | ref-enforcement-hook | unit | `go test ./pkg/compiler/... -run TestUserData` | ❌ W0 | ⬜ pending |
| 25-02-02 | 02 | 1 | ref-hook-wildcards | unit | `go test ./pkg/compiler/... -run TestRefHook` | ❌ W0 | ⬜ pending |
| 25-02-03 | 02 | 1 | refs-env-var | unit | `go test ./pkg/compiler/... -run TestUserDataAllowedRefs` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `testdata/profiles/ec2-empty-repos.yaml` — profile with `allowedRepos: []`
- [ ] `testdata/profiles/ec2-with-allowed-refs.yaml` — profile with `allowedRefs: ["main", "feature/*"]`
- [ ] New production code in `pkg/compiler/userdata.go` — inject `KM_ALLOWED_REFS` env var and pre-push hook when `AllowedRefs` non-empty
- [ ] Compiler must set `core.hooksPath` in gitconfig and write `/opt/km/hooks/pre-push`

*Existing test infrastructure covers `pkg/github/` and `pkg/compiler/` — no framework install needed.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| ECS sandbox git clone works | ECS credential gap | Requires live ECS task with GitHub App token | Deploy ECS sandbox with sourceAccess.github, exec into task, run `git clone` |
| Wildcard repo pattern API behavior | wildcard-repos | Requires live GitHub API call with App installation | Create test profile with `org/*`, run `km create`, verify token scope |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
