---
phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions
plan: 01
subsystem: testing
tags: [quota, tdd, wave-0, stubs, dynamodb, slack-bridge, http-proxy]

# Dependency graph
requires: []
provides:
  - "pkg/quota package: Action, OnBreach, ActionLimit, Limits, WindowResult, Decision, QuotaAPI types + Record skeleton"
  - "QUO-01..05 test stubs (TestRecord PASS; QUO-02..05 Skip Wave 1 plan 02)"
  - "PRX-01..03 test stubs in httpproxy (Skip plan 05)"
  - "BRG-02 frozen-dispatch stub in pkg/slack/bridge (Skip plan 06)"
  - "ALR-01 idempotent-alert stub in cmd/km-quota-alerter (Skip plan 09)"
  - "CLI-01/02 freeze/unlock-latch stubs in internal/app/cmd (Skip plan 10)"
  - "Module-count assertion bumped 24→26 (dynamodb-action-quota + lambda-quota-alerter)"
  - "cmd/km-quota-alerter/main.go skeleton (compile target for Wave 3)"
affects:
  - 121-02 (Wave 1 pkg/quota Record implementation)
  - 121-05 (proxy classifier fills PRX-01..03)
  - 121-06 (bridge frozen gate fills BRG-02)
  - 121-08 (dynamodb-action-quota module satisfies INIT-01 count)
  - 121-09 (alerter Lambda fills ALR-01 + satisfies count)
  - 121-10 (km freeze/unlock commands fill CLI-01/02)

# Tech tracking
tech-stack:
  added: ["pkg/quota (new package)"]
  patterns:
    - "QuotaAPI narrow-interface pattern mirrors BudgetAPI in pkg/aws"
    - "t.Skip('plan N') guards for Wave 0 stubs (not commented-out code)"
    - "fakeQuotaClient mock mirrors fakeBudgetClient pattern"

key-files:
  created:
    - "pkg/quota/quota.go"
    - "pkg/quota/quota_test.go"
    - "internal/app/cmd/freeze_test.go"
    - "sidecars/http-proxy/httpproxy/quota_classify_test.go"
    - "pkg/slack/bridge/events_handler_frozen_test.go"
    - "cmd/km-quota-alerter/main.go"
    - "cmd/km-quota-alerter/alerter_test.go"
  modified:
    - "internal/app/cmd/init_plan_test.go"

key-decisions:
  - "pkg/quota uses QuotaAPI narrow interface (UpdateItem-only) — mirrors BudgetAPI pattern, mocked identically"
  - "hourBucket/dayBucket are package-level (not exported) helpers; QUO-01 tests them directly (same package test)"
  - "TestRecord (bucket math) goes PASS immediately; QUO-02..05 are Skip-guarded (Wave 1) — tests build clean on RED wave"
  - "cmd/km-quota-alerter/main.go added as empty skeleton so the test package builds before plan 09 arrives"
  - "BRG-02 stub lives in package bridge (not bridge_test) to access unexported types in plan 06"

patterns-established:
  - "Wave 0 stub pattern: t.Skip('plan N: description') — never commented code, always discoverable by go test -run"
  - "pkg/quota interface contract frozen here; downstream waves import without circular risk"

requirements-completed: [QUO-01, QUO-02, QUO-03, QUO-04, QUO-05, PRX-01, PRX-02, PRX-03, BRG-02, ALR-01, INIT-01, CLI-01, CLI-02]

# Metrics
duration: 5min
completed: 2026-06-27
---

# Phase 121 Plan 01: Wave 0 Test Scaffolding + pkg/quota Contract Summary

**pkg/quota skeleton (Record/Decision/QuotaAPI contract) + 8 Wave 0 test stubs + module-count bump 24→26 so TDD red→green flows downstream without surprise test failures**

## Performance

- **Duration:** ~5 min
- **Started:** 2026-06-27T12:50:21Z
- **Completed:** 2026-06-27T12:55:35Z
- **Tasks:** 3
- **Files modified:** 8 (7 created, 1 modified)

## Accomplishments

- Created `pkg/quota` package with the exact type contract from the plan interfaces block: Action constants (6 actions), OnBreach (warn/block/freeze), ActionLimit, Limits, WindowResult, Decision, QuotaAPI, Record skeleton, hourBucket/dayBucket helpers
- Created QUO-01..05 test stubs: TestRecord (bucket math assertions PASS immediately), QUO-02..05 Skip-guarded for Wave 1
- Created cmd/km-quota-alerter skeleton (main.go compile target + ALR-01 stub) + 5 other Wave 0 stubs (PRX-01..03, BRG-02, CLI-01/02)
- Bumped regionalModules count assertion 24→26 with comment naming both new modules

## Task Commits

1. **Task 1: Create pkg/quota skeleton with Record/Decision contract** - `73d93d79` (feat)
2. **Task 2: Write pkg/quota RED test stubs (QUO-01..05)** - `71d08f54` (test)
3. **Task 3: Bump module-count test + create remaining Wave 0 stubs** - `23f35e4a` (test)

## Files Created/Modified

- `pkg/quota/quota.go` — Action/OnBreach/ActionLimit/Limits/WindowResult/Decision/QuotaAPI types + Record skeleton + hourBucket/dayBucket helpers
- `pkg/quota/quota_test.go` — QUO-01 TestRecord (PASS: bucket math), QUO-02..05 (Skip Wave 1 plan 02)
- `internal/app/cmd/init_plan_test.go` — module count bumped 24→26 (INIT-01)
- `internal/app/cmd/freeze_test.go` — CLI-01 TestRunFreeze + CLI-02 TestRunUnlockLatchAware stubs (Skip plan 10)
- `sidecars/http-proxy/httpproxy/quota_classify_test.go` — PRX-01 TestClassifyGitHub + PRX-02 TestClassifySES + PRX-03 TestNoDoubleCount stubs (Skip plan 05)
- `pkg/slack/bridge/events_handler_frozen_test.go` — BRG-02 TestFrozenDispatch stub (Skip plan 06)
- `cmd/km-quota-alerter/main.go` — empty main() skeleton (compile target)
- `cmd/km-quota-alerter/alerter_test.go` — ALR-01 TestIdempotentAlert stub (Skip plan 09)

## Decisions Made

- Used same-package (`package quota`) for quota_test.go so TestRecord can call unexported hourBucket/dayBucket directly without exporting them
- BRG-02 stub lives in `package bridge` (not `bridge_test`) for the same reason — plan 06 will access unexported handler internals
- TestRecord fully asserts (not skipped) because the bucket-math helpers are implemented in the skeleton; all others Skip to avoid false reds
- cmd/km-quota-alerter/main.go is a minimal skeleton with empty main() — enough to give the test package a compile target

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None.

## Next Phase Readiness

- Plan 02 (Wave 1): can import `pkg/quota` immediately; QUO-02..05 will go GREEN when Record logic is implemented
- Plan 05 (proxy classifier): PRX-01..03 stubs ready; implement `ClassifyAction` and remove Skip guards
- Plan 06 (bridge frozen gate): BRG-02 stub ready; add `ActionFrozen` to `SandboxRoutingInfo` and implement gate
- Plan 08 (DynamoDB table): adding `dynamodb-action-quota` to `regionalModules()` will make INIT-01 go GREEN (count 24→25)
- Plan 09 (alerter Lambda): adding `lambda-quota-alerter` to `regionalModules()` + `lambdaBuilds()` will satisfy remaining count increment (25→26)
- Plan 10 (CLI): implementing RunFreeze + latch-aware RunUnlock will make CLI-01/02 go GREEN
- No blockers.

---
*Phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions*
*Completed: 2026-06-27*
