---
phase: 5
slug: configui
status: draft
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-22
---

# Phase 5 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing + net/http/httptest (stdlib) |
| **Config file** | none — `go test ./...` |
| **Quick run command** | `go test ./cmd/configui/... -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~10 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./cmd/configui/... -count=1`
- **After every plan wave:** Run `go test ./... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 10 seconds

---

## Nyquist Approach

All plans use **TDD inline** (`tdd="true"` on tasks). Each task's `<behavior>` block defines test expectations, and the task creates both tests and implementation in a single pass. No separate Wave 0 stub plan is needed because:

1. Every `<automated>` verify command references real test functions created by the task itself
2. TDD tasks write failing tests first (RED), then implement (GREEN) within the same execution
3. Test files are listed in each task's `<files>` field alongside production code

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | Status |
|---------|------|------|-------------|-----------|-------------------|--------|
| 05-XX-T1 | XX | X | CFUI-01 | unit (TDD) | `go test ./cmd/configui/... -run TestHandleValidate -count=1` | pending |
| 05-XX-T2 | XX | X | CFUI-01 | unit (TDD) | `go test ./cmd/configui/... -run TestHandleProfileSave -count=1` | pending |
| 05-XX-T3 | XX | X | CFUI-01 | unit (TDD) | `go test ./cmd/configui/... -run TestHandleSchema -count=1` | pending |
| 05-XX-T4 | XX | X | CFUI-02 | unit (TDD) | `go test ./cmd/configui/... -run TestHandleDashboard -count=1` | pending |
| 05-XX-T5 | XX | X | CFUI-02 | unit (TDD) | `go test ./cmd/configui/... -run TestHTMXPartialSwap -count=1` | pending |
| 05-XX-T6 | XX | X | CFUI-03 | unit (TDD) | `go test ./cmd/configui/... -run TestHandleSandboxDetail -count=1` | pending |
| 05-XX-T7 | XX | X | CFUI-04 | unit (TDD) | `go test ./cmd/configui/... -run TestHandleSecretsList -count=1` | pending |
| 05-XX-T8 | XX | X | CFUI-04 | unit (TDD) | `go test ./cmd/configui/... -run TestHandleSecretDecrypt -count=1` | pending |

*Status: pending / green / red / flaky*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Monaco editor loads with YAML highlighting and autocomplete | CFUI-01 | Browser JS rendering — no Go test | Open http://localhost:8080/editor, verify Monaco loads, type YAML, see autocomplete |
| Dashboard table auto-refreshes every 10s | CFUI-02 | HTMX polling requires browser | Open dashboard, watch Network tab for 10s polling requests |
| PII blur toggle on secret values | CFUI-04 | CSS visual behavior | Open secrets page, verify values are blurred, click to reveal |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify commands (TDD inline creates tests within task)
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] No separate Wave 0 needed — TDD inline is the adopted approach
- [x] No watch-mode flags
- [x] Feedback latency < 10s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
