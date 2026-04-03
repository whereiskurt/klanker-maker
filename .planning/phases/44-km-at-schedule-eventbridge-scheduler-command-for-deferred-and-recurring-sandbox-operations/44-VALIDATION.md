---
phase: 44
slug: km-at-schedule-eventbridge-scheduler-command-for-deferred-and-recurring-sandbox-operations
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-02
---

# Phase 44 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go stdlib `testing` (no external test framework) |
| **Config file** | none |
| **Quick run command** | `go test ./internal/app/cmd/ ./pkg/at/... -run TestAt -v` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/ ./pkg/at/... -count=1`
- **After every plan wave:** Run `go test ./... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 44-01-01 | 01 | 1 | NL time parsing | unit | `go test ./pkg/at/... -run TestParseOneTime -v` | ❌ W0 | ⬜ pending |
| 44-01-02 | 01 | 1 | Recurring phrase parsing | unit | `go test ./pkg/at/... -run TestParseRecurring -v` | ❌ W0 | ⬜ pending |
| 44-01-03 | 01 | 1 | Schedule name sanitization | unit | `go test ./pkg/at/... -run TestScheduleName -v` | ❌ W0 | ⬜ pending |
| 44-01-04 | 01 | 1 | Cron day-of-week mapping | unit | `go test ./pkg/at/... -run TestCronDayOfWeek -v` | ❌ W0 | ⬜ pending |
| 44-02-01 | 02 | 1 | km at creates schedule | unit (mock) | `go test ./internal/app/cmd/ -run TestAtCmd -v` | ❌ W0 | ⬜ pending |
| 44-02-02 | 02 | 1 | km at list DynamoDB | unit (mock) | `go test ./internal/app/cmd/ -run TestAtList -v` | ❌ W0 | ⬜ pending |
| 44-02-03 | 02 | 1 | km at cancel deletes | unit (mock) | `go test ./internal/app/cmd/ -run TestAtCancel -v` | ❌ W0 | ⬜ pending |
| 44-03-01 | 03 | 2 | SchedulerAPI mock compiles | compile | `go build ./...` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/at/parser.go` — NL time parser (new package)
- [ ] `pkg/at/parser_test.go` — covers one-time and recurring cases
- [ ] `pkg/aws/schedules_dynamo.go` — km-schedules table CRUD
- [ ] `pkg/aws/schedules_dynamo_test.go`
- [ ] `internal/app/cmd/at.go` — km at command
- [ ] `internal/app/cmd/at_test.go`
- [ ] Update `mockSchedulerAPI` in `pkg/aws/scheduler_test.go` with stubs for new interface methods

*(No existing test infrastructure covers these — all new files)*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| E2E schedule fires in AWS | EventBridge integration | Requires real AWS account | Create one-time schedule 2min ahead, verify Lambda invoked |
| Recurring create respects max-sandbox | Max sandbox guardrail | Requires running sandboxes | Set max=1, have 1 active, trigger recurring create, verify skip |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
