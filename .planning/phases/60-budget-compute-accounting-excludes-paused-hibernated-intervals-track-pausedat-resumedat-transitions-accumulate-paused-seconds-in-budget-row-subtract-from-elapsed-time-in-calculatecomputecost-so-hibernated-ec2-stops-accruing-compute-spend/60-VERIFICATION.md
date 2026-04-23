---
phase: 60-budget-compute-accounting-excludes-paused-hibernated-intervals
verified: 2026-04-23T02:15:00Z
status: passed
score: 13/13 must-haves verified
re_verification: false
---

# Phase 60: Budget Compute Accounting Excludes Paused/Hibernated Intervals — Verification Report

**Phase Goal:** Paused/hibernated EC2 sandboxes stop accruing compute budget — calculateComputeCost subtracts accumulated pausedSeconds (closed intervals) plus any open interval (now - pausedAt) from elapsed time before multiplying by spot rate, while preserving the existing SET-based idempotent spend recompute. Every pause/resume transition writes pausedAt/pausedSeconds on the BUDGET#compute DynamoDB row.
**Verified:** 2026-04-23T02:15:00Z
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| #  | Truth | Status | Evidence |
|----|-------|--------|----------|
| 1  | BudgetSummary carries PausedSeconds (int64) and PausedAt (*time.Time) populated from BUDGET#compute | VERIFIED | `pkg/aws/budget.go:43-48` — both fields present; GetBudget parses them at lines 279-289 |
| 2  | RecordPauseStart writes pausedAt using if_not_exists, so a double-pause preserves the original timestamp | VERIFIED | `pkg/aws/budget.go:165` — UpdateExpression `SET pausedAt = if_not_exists(pausedAt, :now)`; TestRecordPauseStart_Idempotent passes |
| 3  | RecordResumeClose adds the elapsed interval (now - pausedAt) to pausedSeconds and REMOVEs pausedAt atomically | VERIFIED | `pkg/aws/budget.go:221` — UpdateExpression `ADD pausedSeconds :interval REMOVE pausedAt`; TestRecordResumeClose_AccumulatesInterval passes |
| 4  | RecordResumeClose is a safe no-op when pausedAt is absent (legacy sandboxes) | VERIFIED | `pkg/aws/budget.go:194-199` — early return nil when GetItem fails or pausedAt key absent; TestRecordResumeClose_NoPausedAtIsNoop passes |
| 5  | km pause records pausedAt on BUDGET#compute after StopInstances succeeds (EC2 only) | VERIFIED | `internal/app/cmd/pause.go:190` — RecordPauseForEC2 called before UpdateSandboxStatusAndClearTTL; EC2 gated by StopInstances loop |
| 6  | km resume calls RecordResumeClose after StartInstances succeeds | VERIFIED | `internal/app/cmd/resume.go:123` — RecordResumeClose called before UpdateSandboxStatusDynamo |
| 7  | km budget add auto-resume path calls RecordResumeClose after starting EC2 | VERIFIED | `internal/app/cmd/budget.go:265` — RecordResumeClose in resumeEC2Sandbox after StartInstances |
| 8  | TTL-handler idle-hibernate (handleStop) records pausedAt after StopInstances, when status=="paused" | VERIFIED | `cmd/ttl-handler/main.go:256-260` — guarded on `status == "paused" && BudgetClient != nil && BudgetTable != ""` |
| 9  | TTL-handler handleResume closes open interval after StartInstances | VERIFIED | `cmd/ttl-handler/main.go:325-329` — RecordResumeClose before UpdateSandboxStatusDynamo |
| 10 | TTL-handler handleAgentRun auto-start closes open interval after StartInstances | VERIFIED | `cmd/ttl-handler/main.go:515-519` — RecordResumeClose before UpdateSandboxStatusDynamo |
| 11 | calculateComputeCost accepts pausedSeconds and subtracts from elapsed, clamped to >= 0 | VERIFIED | `cmd/budget-enforcer/main.go:263-280` — billableSecs = max(0, elapsedSecs - pausedSeconds); TestCalculateComputeCost_NeverNegative passes |
| 12 | HandleBudgetCheck computes effective pausedSeconds (closed + open interval) before calling calculateComputeCost | VERIFIED | `cmd/budget-enforcer/main.go:150-181` — GetBudget first, then PausedSeconds + (now-PausedAt) open interval, then calculateComputeCost(event, pausedSecs) |
| 13 | enforceBudgetCompute records pausedAt after StopInstances so the budget does not tick on subsequent Lambda invocations | VERIFIED | `cmd/budget-enforcer/main.go:480-484` — RecordPauseStart called EC2 branch only, before UpdateSandboxStatusDynamo |

**Score:** 13/13 truths verified

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/aws/budget.go` | BudgetSummary.PausedSeconds, BudgetSummary.PausedAt, RecordPauseStart, RecordResumeClose, GetBudget extension | VERIFIED | All four exports present; GetBudget parses pausedSeconds and pausedAt in BUDGET#compute case |
| `pkg/aws/budget_test.go` | Tests: TestRecordPauseStart*, TestRecordResumeClose*, TestMultiplePauseResumeCycles, TestGetBudget_PopulatesPausedFields | VERIFIED | All 9 test functions present and passing |
| `internal/app/cmd/pause.go` | RecordPauseStart call after StopInstances via RecordPauseForEC2 | VERIFIED | RecordPauseForEC2 exported helper at line 28; called at line 190 in runPause |
| `internal/app/cmd/resume.go` | RecordResumeClose after StartInstances | VERIFIED | Line 123; non-fatal warn pattern |
| `internal/app/cmd/budget.go` | RecordResumeClose in resumeEC2Sandbox | VERIFIED | Line 265; budgetClient/budgetTable signature extended |
| `internal/app/cmd/pause_test.go` | TestPauseRecordsTimestamp | VERIFIED | Line 77; tests RecordPauseForEC2 helper via fake BudgetAPI |
| `cmd/ttl-handler/main.go` | BudgetClient+BudgetTable on TTLHandler struct; hooks in handleStop, handleResume, handleAgentRun | VERIFIED | Struct fields at lines 119-121; hooks at lines 257, 326, 516; wired from KM_BUDGET_TABLE env in main() at lines 1409-1434 |
| `cmd/budget-enforcer/main.go` | calculateComputeCost(event, pausedSeconds) + open-interval in HandleBudgetCheck + RecordPauseStart in enforceBudgetCompute | VERIFIED | Signature at line 263; HandleBudgetCheck reorder+threading at lines 150-181; RecordPauseStart at line 481 |
| `cmd/budget-enforcer/main_test.go` | TestCalculateComputeCost*, TestBudgetHandler_Paused*, TestBudgetHandler_MultipleCycles, TestEnforceBudgetCompute_* (8 tests) | VERIFIED | All 8 test functions present and passing |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `pkg/aws/budget.go:RecordPauseStart` | DynamoDB BUDGET#compute | `SET pausedAt = if_not_exists(pausedAt, :now)` | WIRED | UpdateExpression at line 165; pattern confirmed |
| `pkg/aws/budget.go:RecordResumeClose` | DynamoDB BUDGET#compute | `ADD pausedSeconds :interval REMOVE pausedAt` | WIRED | UpdateExpression at line 221; single atomic UpdateItem |
| `pkg/aws/budget.go:GetBudget BUDGET#compute case` | BudgetSummary.PausedSeconds / PausedAt | attributevalue.Unmarshal of pausedSeconds (N) and pausedAt (S, RFC3339) | WIRED | Lines 279-289 in GetBudget switch |
| `internal/app/cmd/pause.go:runPause` | pkg/aws.RecordPauseStart | RecordPauseForEC2 exported helper call at line 190 | WIRED | `awspkg.RecordPauseStart` called inside helper; helper confirmed at line 29 |
| `internal/app/cmd/resume.go:runResume` | pkg/aws.RecordResumeClose | Direct call at line 123 after StartInstances | WIRED | `awspkg.RecordResumeClose` confirmed |
| `cmd/ttl-handler/main.go:TTLHandler` | pkg/aws.BudgetAPI on km-budgets | BudgetClient+BudgetTable fields wired from KM_BUDGET_TABLE env in main() | WIRED | Lines 1409-1434 in main(); fields at 119-121 |
| `cmd/budget-enforcer/main.go:HandleBudgetCheck` | BudgetSummary.PausedSeconds / PausedAt | GetBudget called first; pausedSecs computed at lines 170-177 | WIRED | effectivePausedSecs threaded into calculateComputeCost at line 181 |
| `cmd/budget-enforcer/main.go:calculateComputeCost` | billable seconds | `billableSecs = elapsedSecs - float64(pausedSeconds); if < 0 { billableSecs = 0 }` | WIRED | Lines 275-278 |
| `cmd/budget-enforcer/main.go:enforceBudgetCompute` | pkg/aws.RecordPauseStart on km-budgets | Call at line 481, EC2 branch only, non-fatal | WIRED | Guarded by `h.DynamoDB != nil && h.BudgetTable != ""` |

---

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| BUDG-PAUSE-01 | 60-01 | BudgetSummary.PausedSeconds/PausedAt primitives; RecordPauseStart/RecordResumeClose helpers with if_not_exists idempotency, ADD+REMOVE atomicity, and non-fatal GetItem swallow | SATISFIED | All four exports in `pkg/aws/budget.go`; 9 tests passing |
| BUDG-PAUSE-02 | 60-02 | All pause/resume transition sites wire RecordPauseStart/RecordResumeClose — km pause, km resume, km budget add auto-resume, km at (via ttl-handler handleStop/handleResume/handleAgentRun) | SATISFIED | 6 wiring sites confirmed across 4 files |
| BUDG-PAUSE-03 | 60-03 | calculateComputeCost subtracts effective pausedSeconds (closed+open) from elapsed; billableSecs clamped to >=0; enforceBudgetCompute records pausedAt after StopInstances | SATISFIED | `cmd/budget-enforcer/main.go` lines 150-181, 263-280, 480-484; 8 tests passing |

Note: BUDG-PAUSE-01/02/03 are phase-scoped requirements not listed in `.planning/REQUIREMENTS.md`. They are defined in the plan frontmatter only. BUDG-03 (the base compute tracking requirement, Phase 6) remains complete; Phase 60 fixes the bug where paused intervals were included in the elapsed time calculation.

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `cmd/ttl-handler/main.go` | 959 | `TODO: Read substrate from metadata.json to handle ECS sandboxes` | Info | Pre-existing TODO in destroy path; unrelated to pause/resume budget accounting |
| `cmd/ttl-handler/main.go` | 996 | `vpc_id = "destroy-placeholder"` | Info | Pre-existing terraform template literal in destroy path; not a code stub |
| `cmd/budget-enforcer/main.go` | 513 | `TODO: Trigger artifact upload before stopping task` | Info | Pre-existing TODO in ECS stop path; unrelated to EC2 pause accounting |

No blockers or warnings. All three flagged items are pre-existing and in code paths unrelated to Phase 60.

---

### Test Suite Results

| Suite | Result | Notes |
|-------|--------|-------|
| `go test ./pkg/aws/... -count=1` | PASS | Includes all 9 new pause/resume tests |
| `go test ./cmd/budget-enforcer/... -count=1` | PASS | Includes all 8 new paused-accounting tests |
| `go test ./internal/app/cmd/... -count=1` | FAIL (pre-existing) | `TestUnlockCmd_RequiresStateBucket` fails — created in Phase 30, last modified in Phase 30 commit `22366b1`, untouched by Phase 60; confirmed pre-existing failure |
| `go build ./...` | PASS | No compile errors |

---

### Human Verification Required

None. All behavioral requirements are verified programmatically through unit tests and source inspection. The key behaviors (pause-interval subtraction, clamp to zero, open-interval adjustment, idempotency) are covered by the test suites.

---

### Gaps Summary

No gaps. All 13 observable truths are verified. All artifacts exist, are substantive, and are wired. All three requirement IDs are satisfied. The test suite for the affected packages passes cleanly. The pre-existing `TestUnlockCmd_RequiresStateBucket` failure in Phase 30 code is out of scope for Phase 60.

---

_Verified: 2026-04-23T02:15:00Z_
_Verifier: Claude (gsd-verifier)_
