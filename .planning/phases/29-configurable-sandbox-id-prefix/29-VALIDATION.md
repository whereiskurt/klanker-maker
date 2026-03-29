---
phase: 29
slug: configurable-sandbox-id-prefix
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-28
---

# Phase 29 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none -- existing Go test infrastructure |
| **Quick run command** | `go test ./pkg/compiler/ ./pkg/profile/ ./internal/app/cmd/ -run Prefix -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/compiler/ ./pkg/profile/ ./internal/app/cmd/ -run Prefix -count=1`
- **After every plan wave:** Run `go test ./... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 29-01-01 | 01 | 1 | PREFIX-01 | unit | `go test ./pkg/profile/ -run TestPrefix` | W0 | pending |
| 29-01-02 | 01 | 1 | PREFIX-02 | unit | `go test ./pkg/compiler/ -run TestGenerateSandboxID` | yes | pending |
| 29-02-01 | 02 | 2 | PREFIX-03 | unit | `go test ./internal/app/cmd/ -run TestSandboxRef` | W0 (sandbox_ref_test.go must be created during plan execution) | pending |
| 29-02-02 | 02 | 2 | PREFIX-03 | unit | `go test ./cmd/email-create-handler/ -run TestExtractSandboxID` | yes | pending |
| 29-02-03 | 02 | 2 | PREFIX-04 | unit | `go test ./pkg/compiler/ -run TestCompile` | yes | pending |
| 29-02-04 | 02 | 2 | PREFIX-05 | unit | `go test ./pkg/profile/ -run TestBuiltin` | yes | pending |
| 29-03-01 | 03 | 2 | ALIAS-01, ALIAS-02 | unit | `go test ./pkg/aws/... -run TestAlias\|TestResolveAlias\|TestNextAlias` | W0 | pending |
| 29-03-02 | 03 | 2 | ALIAS-03, ALIAS-04 | unit | `go test ./internal/app/cmd/... -run TestCreate\|TestResolve\|TestList` | yes | pending |

*Status: pending / green / red / flaky*

---

## Wave 0 Requirements

- **29-01-01:** New test cases added to existing `pkg/profile/validate_test.go` (file exists, cases are new)
- **29-02-01:** `internal/app/cmd/sandbox_ref_test.go` does NOT exist and must be created as part of Plan 02 Task 1 execution
- **29-03-01:** New test cases for `ResolveSandboxAlias` and `NextAliasFromTemplate` added to `pkg/aws/sandbox_test.go`

*If none: "Existing infrastructure covers all phase requirements."*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Built-in profiles render correct prefix in compiled output | PREFIX-05 | End-to-end check with real profile | Run `km validate profiles/claude-dev.yaml` and `km validate profiles/open-dev.yaml` |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
