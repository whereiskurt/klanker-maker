---
phase: 55
slug: learn-mode-command-capture
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-18
---

# Phase 55 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — existing test infrastructure |
| **Quick run command** | `go test ./pkg/allowlistgen/...` |
| **Full suite command** | `go test ./pkg/allowlistgen/... ./internal/app/cmd/...` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/allowlistgen/...`
- **After every plan wave:** Run `go test ./pkg/allowlistgen/... ./internal/app/cmd/...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 55-01-01 | 01 | 1 | CMD-01 | unit | `go test ./pkg/allowlistgen/ -run TestRecordCommand` | ❌ W0 | ⬜ pending |
| 55-01-02 | 01 | 1 | CMD-02 | unit | `go test ./pkg/allowlistgen/ -run TestGenerateAnnotatedYAML` | ❌ W0 | ⬜ pending |
| 55-02-01 | 02 | 2 | CMD-03 | unit | `go test ./internal/app/cmd/ -run TestLearnObservedState` | ❌ W0 | ⬜ pending |
| 55-02-02 | 02 | 2 | CMD-04 | integration | `go test ./internal/app/cmd/ -run TestFlushObservedState` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/allowlistgen/recorder_test.go` — add RecordCommand/Commands test stubs
- [ ] `pkg/allowlistgen/generator_test.go` — add command annotation test stubs

*Existing infrastructure covers test framework requirements.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Commands captured on EC2 sandbox | CMD-03 | Requires live EC2 instance with learn mode | 1. `km create profiles/learn.yaml` 2. `km shell --learn <id>` 3. Run commands 4. Exit 5. Check observed-profile.yaml |
| Commands captured on Docker sandbox | CMD-04 | Requires Docker substrate | 1. `km create profiles/learn.yaml --docker` 2. `km shell --learn <id>` 3. Run commands 4. Exit 5. Check observed-profile.yaml |
| Root vs sandbox user attribution | CMD-02 | Requires SSM session as root | 1. `km shell --root --learn <id>` 2. Run commands 3. Verify user attribution in output |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
