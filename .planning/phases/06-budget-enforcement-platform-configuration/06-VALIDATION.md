---
phase: 6
slug: budget-enforcement-platform-configuration
status: draft
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-22
---

# Phase 6 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (`go test ./...`) |
| **Config file** | none — `go test` standard |
| **Quick run command** | `go test ./pkg/aws/... ./pkg/profile/... ./sidecars/http-proxy/... ./internal/app/... -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~20 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/aws/... ./pkg/profile/... ./sidecars/http-proxy/... ./internal/app/... -count=1`
- **After every plan wave:** Run `go test ./... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 20 seconds

---

## Nyquist Approach

All plans use **TDD inline** (`tdd="true"` on tasks). Each task's `<behavior>` block defines test expectations, and the task creates both tests and implementation in a single pass.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | Status |
|---------|------|------|-------------|-----------|-------------------|--------|
| 06-XX-T1 | XX | X | CONF-01, CONF-03 | unit (TDD) | `go test ./internal/app/config/... -count=1` | pending |
| 06-XX-T2 | XX | X | CONF-02 | unit (TDD) | `go test ./pkg/aws/... ./pkg/profile/... ./pkg/compiler/... -count=1` | pending |
| 06-XX-T3 | XX | X | CONF-04 | unit (TDD) | `go test ./internal/app/cmd/... -run TestConfigure -count=1` | pending |
| 06-XX-T4 | XX | X | BUDG-01 | unit (TDD) | `go test ./pkg/profile/... -run TestBudget -count=1` | pending |
| 06-XX-T5 | XX | X | BUDG-02, BUDG-03, BUDG-06 | unit (TDD) | `go test ./pkg/aws/... -run TestBudget -count=1` | pending |
| 06-XX-T6 | XX | X | BUDG-04, BUDG-07 | unit (TDD) | `go test ./sidecars/http-proxy/httpproxy/... -run TestBedrock -count=1` | pending |
| 06-XX-T7 | XX | X | BUDG-05 | unit (TDD) | `go test ./pkg/aws/... -run TestPricing -count=1` | pending |
| 06-XX-T8 | XX | X | BUDG-08 | unit (TDD) | `go test ./internal/app/cmd/... -run TestBudgetAdd -count=1` | pending |
| 06-XX-T9 | XX | X | BUDG-09 | unit (TDD) | `go test ./internal/app/cmd/... -run TestStatus -count=1` | pending |

*Status: pending / green / red / flaky*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| DynamoDB global table replication across regions | BUDG-02 | AWS infrastructure — no Go test | Run `terraform plan` on DynamoDB module, verify `replica` blocks |
| Bedrock MITM with proxy CA certificate in live sandbox | BUDG-04 | Requires real Bedrock API call through proxy | Create sandbox, run a Bedrock call, verify token count in DynamoDB |
| km configure DNS delegation wizard with real AWS | CONF-03 | Multi-account Route53 operations | Run `km configure` with real accounts, verify zone delegation |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify commands (TDD inline creates tests within task)
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] No separate Wave 0 needed — TDD inline is the adopted approach
- [x] No watch-mode flags
- [x] Feedback latency < 20s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
