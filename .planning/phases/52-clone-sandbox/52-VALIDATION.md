---
phase: 52
slug: clone-sandbox
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-13
---

# Phase 52 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing + testify (existing) |
| **Config file** | none — standard `go test ./...` |
| **Quick run command** | `go test ./internal/app/cmd/ -run TestClone -v` |
| **Full suite command** | `go test ./internal/app/cmd/ ./pkg/aws/ -count=1` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/ -run TestClone -v`
- **After every plan wave:** Run `go test ./internal/app/cmd/ ./pkg/aws/ -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 52-01-01 | 01 | 1 | CLONE-04 | unit | `go test ./pkg/aws/ -run TestClonedFromMarshal -v` | ❌ W0 | ⬜ pending |
| 52-02-01 | 02 | 1 | CLONE-01 | unit | `go test ./internal/app/cmd/ -run TestCloneCmd -v` | ❌ W0 | ⬜ pending |
| 52-02-02 | 02 | 1 | CLONE-02 | unit | `go test ./internal/app/cmd/ -run TestBuildWorkspaceStagingCmd -v` | ❌ W0 | ⬜ pending |
| 52-02-03 | 02 | 1 | CLONE-03 | unit | `go test ./internal/app/cmd/ -run TestCloneFlags -v` | ❌ W0 | ⬜ pending |
| 52-02-04 | 02 | 1 | CLONE-05 | unit | `go test ./internal/app/cmd/ -run TestCloneSourceNotRunning -v` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/app/cmd/clone_test.go` — stubs for CLONE-01, CLONE-02, CLONE-03, CLONE-05
- [ ] `pkg/aws/sandbox_dynamo_test.go` (extend) — stubs for CLONE-04

*Existing test infrastructure covers framework and fixtures.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end clone of live sandbox | CLONE-01 | Requires running EC2 instance + SSM | `km clone <running-sandbox> --alias test-clone` and verify workspace contents match |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
