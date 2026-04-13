---
phase: 53
slug: persistent-local-sandbox-numbering
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-13
---

# Phase 53 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib) |
| **Config file** | none |
| **Quick run command** | `go test ./pkg/localnumber/... -v` |
| **Full suite command** | `go test ./internal/app/cmd/... ./pkg/localnumber/...` |
| **Estimated runtime** | ~5 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/localnumber/... -v`
- **After every plan wave:** Run `go test ./internal/app/cmd/... ./pkg/localnumber/...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 10 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 53-01-01 | 01 | 1 | LOCAL-01 | unit | `go test ./pkg/localnumber/... -run TestAssign` | ❌ W0 | ⬜ pending |
| 53-01-02 | 01 | 1 | LOCAL-02 | unit | `go test ./pkg/localnumber/... -run TestRemove` | ❌ W0 | ⬜ pending |
| 53-01-03 | 01 | 1 | LOCAL-03 | unit | `go test ./pkg/localnumber/... -run TestResolve` | ❌ W0 | ⬜ pending |
| 53-01-04 | 01 | 1 | LOCAL-04 | unit | `go test ./pkg/localnumber/... -run TestReconcile` | ❌ W0 | ⬜ pending |
| 53-01-05 | 01 | 1 | LOCAL-05 | unit | `go test ./pkg/localnumber/... -run TestLoad` | ❌ W0 | ⬜ pending |
| 53-01-06 | 01 | 1 | LOCAL-06 | unit | `go test ./pkg/localnumber/... -run TestSave` | ❌ W0 | ⬜ pending |
| 53-02-01 | 02 | 2 | LOCAL-07 | unit | `go test ./internal/app/cmd/... -run TestListCmd` | ❌ W0 | ⬜ pending |
| 53-02-02 | 02 | 2 | LOCAL-08 | unit | `go test ./internal/app/cmd/... -run TestResolveSandboxID` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/localnumber/localnumber_test.go` — stubs for LOCAL-01 through LOCAL-06
- [ ] `internal/app/cmd/list_test.go` — stubs for LOCAL-07 (or extend existing)
- [ ] `internal/app/cmd/sandbox_ref_test.go` — stubs for LOCAL-08 (or extend existing)

*Existing Go test infrastructure covers framework needs — no new tooling required.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Numbers display correctly in terminal | LOCAL-07 | Visual alignment with ANSI codes | Run `km list` with 2+ sandboxes, verify numbers are persistent across invocations |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 10s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
