---
phase: 32
slug: profile-scoped-rsync-paths-with-external-file-lists-and-shell-wildcards
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-29
---

# Phase 32 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib) + testify |
| **Config file** | none (standard `go test`) |
| **Quick run command** | `go test ./pkg/profile/... ./internal/app/cmd/... -run TestRsync -v` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/profile/... ./internal/app/cmd/... -run TestRsync -v`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 32-01-01 | 01 | 0 | RSYNC-01 | unit | `go test ./pkg/profile/... -run TestRsyncPaths -v` | ❌ W0 | ⬜ pending |
| 32-01-02 | 01 | 0 | RSYNC-05 | unit | `go test ./pkg/profile/... -run TestRsyncSchema -v` | ❌ W0 | ⬜ pending |
| 32-01-03 | 01 | 0 | RSYNC-02 | unit | `go test ./internal/app/cmd/... -run TestLoadFileList -v` | ❌ W0 | ⬜ pending |
| 32-01-04 | 01 | 0 | RSYNC-03 | unit | `go test ./internal/app/cmd/... -run TestValidateRsyncPath -v` | ❌ W0 | ⬜ pending |
| 32-01-05 | 01 | 0 | RSYNC-04 | unit | `go test ./internal/app/cmd/... -run TestRsyncPathFallback -v` | ❌ W0 | ⬜ pending |
| 32-01-06 | 01 | 0 | RSYNC-06 | unit | `go test ./internal/app/cmd/... -run TestRsyncSaveCmd -v` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/app/cmd/rsync_test.go` — stubs for RSYNC-02, RSYNC-03, RSYNC-04, RSYNC-06
- [ ] `pkg/profile/types_test.go` — add TestRsyncPaths, TestRsyncSchema for RSYNC-01, RSYNC-05

*No new test framework needed — `go test` and testify already available.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Wildcard expansion on live sandbox | RSYNC-06 | Requires actual sandbox with matching directories | `km rsync save` with a profile containing `projects/*/config`, verify tar includes expanded paths |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
