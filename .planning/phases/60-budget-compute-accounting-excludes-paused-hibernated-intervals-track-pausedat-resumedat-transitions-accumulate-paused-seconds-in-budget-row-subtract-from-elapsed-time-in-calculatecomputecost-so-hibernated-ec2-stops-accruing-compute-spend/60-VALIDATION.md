---
phase: 60
slug: budget-compute-accounting-excludes-paused-hibernated-intervals
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-22
---

# Phase 60 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — repo-wide go.mod |
| **Quick run command** | `go test ./pkg/aws/... ./cmd/budget-enforcer/...` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~30 seconds quick, ~3 min full |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/aws/... ./cmd/budget-enforcer/...`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 60-01-01 | 01 | 1 | core | unit | `go test ./pkg/aws/ -run TestBudget` | ❌ W0 | ⬜ pending |
| 60-01-02 | 01 | 1 | core | unit | `go test ./cmd/budget-enforcer/ -run TestCalculateComputeCost` | ❌ W0 | ⬜ pending |
| 60-02-01 | 02 | 2 | hooks | unit | `go test ./internal/app/cmd/ -run TestPauseRecordsTimestamp` | ❌ W0 | ⬜ pending |
| 60-02-02 | 02 | 2 | hooks | unit | `go test ./internal/app/cmd/ -run TestResumeClosesInterval` | ❌ W0 | ⬜ pending |
| 60-02-03 | 02 | 2 | hooks | unit | `go test ./cmd/ttl-handler/ -run TestIdleHibernate` | ❌ W0 | ⬜ pending |
| 60-02-04 | 02 | 2 | hooks | unit | `go test ./cmd/budget-enforcer/ -run TestEnforceRecordsPause` | ❌ W0 | ⬜ pending |
| 60-03-01 | 03 | 3 | integ | integration | `go test ./cmd/budget-enforcer/ -run TestComputeCostExcludesPausedInterval` | ❌ W0 | ⬜ pending |
| 60-03-02 | 03 | 3 | integ | integration | `go test ./cmd/budget-enforcer/ -run TestOpenPauseInterval` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/aws/budget_pause_test.go` — fixture stubs for RecordPauseStart / RecordResumeClose
- [ ] `cmd/budget-enforcer/main_pause_test.go` — fixture for calculateComputeCost paused-interval cases
- [ ] `internal/app/cmd/pause_test.go` extension — pause writes pausedAt; resume removes it & adds to pausedSeconds
- [ ] DynamoDB fake or interface mock supports `ADD pausedSeconds` and `REMOVE pausedAt`

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Real EC2 hibernate→wait→resume→cost stays flat | end-to-end | Requires live AWS account, ~10 min wall clock | `km create profiles/learn.yaml --on-demand`, note `km otel <id>` cost, `km pause`, wait 10 min, `km resume`, verify `km otel` compute cost did not advance by ~10 min × spot rate |
| Idle-hibernate Lambda fires & records pause | end-to-end | Requires triggering audit-log idle path | Create sandbox with short idle threshold, leave idle, observe DynamoDB `pausedAt` populated and `pausedSeconds` accumulated on resume |
| Budget-exhausted hibernate path records pause | end-to-end | Requires hitting compute limit | Create sandbox with $0.01 limit, wait for budget-enforcer to exhaust + hibernate, verify `pausedAt` written |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
