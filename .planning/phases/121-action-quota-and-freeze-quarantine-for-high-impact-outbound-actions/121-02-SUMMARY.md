---
phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions
plan: 02
subsystem: quota
tags: [dynamodb, atomic-add, counter, quota, tdd]

# Dependency graph
requires:
  - phase: 121-01
    provides: pkg/quota skeleton (types, interfaces, bucket math helpers, RED test stubs)

provides:
  - pkg/quota/quota.go: Record — atomic multi-window UpdateItem ADD counter + Decision verdict
  - pkg/quota/resolve.go: ResolveLimits — per-(action,window) profile→default→unlimited precedence merge
  - Full GREEN test suite for QUO-01..05 (all five tests passing, no skips)

affects:
  - 121-03 (proxy chokepoint uses Record + ResolveLimits)
  - 121-04 (bridge chokepoints use Record + ResolveLimits)
  - 121-05 (alerter Lambda reads Decision result)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Atomic DDB counter: UpdateItem ADD #c :one ReturnValues ALL_NEW (mirrors pkg/aws/budget.go IncrementAISpend)"
    - "Fixed-bucket TTL: hour rows expire now+2h; day rows expire now+2d; lifetime rows carry no TTL"
    - "Dormant path: zero DDB calls when no windows configured — byte-identical to pre-Phase-121"
    - "per-(action,window) precedence merge: profile value → install default → nil (unlimited)"

key-files:
  created:
    - pkg/quota/resolve.go
  modified:
    - pkg/quota/quota.go
    - pkg/quota/quota_test.go

key-decisions:
  - "Implement TTL via SET #ttl = if_not_exists(#ttl, :ttl) in the same UpdateItem as the ADD — single round-trip per window, no overwrite on repeat increments in the same bucket"
  - "WorstWindow is the first exceeded window in declaration order (lifetime → hour → day) — aligns with CONTEXT.md worst=most-severe semantics"
  - "OnBreach returned in Decision only when Tripped=true; callers of a non-tripped Decision never see the policy"
  - "ResolveLimits returns empty OnBreach (not BreachWarn) for unlimited actions — callers apply BreachWarn default at runtime, keeping ResolveLimits pure"

patterns-established:
  - "QuotaAPI narrow interface (UpdateItem only) — matches BudgetAPI narrow-interface pattern; mockable without full DDB client"
  - "atomicIncrement internal helper isolates UpdateItem construction from Record's window-dispatch logic"
  - "multiCountClient test helper enables per-call count differentiation without capturing call index externally"

requirements-completed: [QUO-01, QUO-02, QUO-03, QUO-04, QUO-05]

# Metrics
duration: 7min
completed: 2026-06-27
---

# Phase 121 Plan 02: pkg/quota Core Summary

**Atomic multi-window DynamoDB counter (ADD count :one, fixed hourly/daily/lifetime buckets) plus per-(action,window) ResolveLimits — turns Wave 0 RED stubs GREEN for all five QUO requirements**

## Performance

- **Duration:** ~7 min
- **Started:** 2026-06-27T12:55:00Z
- **Completed:** 2026-06-27T13:02:10Z
- **Tasks:** 3 (RED test update, GREEN implementation, verification)
- **Files modified:** 3

## Accomplishments
- `Record` issues one atomic `UpdateItem ADD #c :one` per configured window (≤3 calls: lifetime + hour + day); zero calls for dormant actions
- Hour/day rows include `SET #ttl = if_not_exists(#ttl, :ttl)` in the same expression (TTL ~2h / ~2d respectively); lifetime rows carry no TTL
- `Decision` carries per-window `{Count, Limit, Exceeded}` + `Tripped` + `WorstWindow` + resolved `OnBreach`
- `ResolveLimits` merges profile and install-default maps per (action, window): profile wins, else install default, else nil (unlimited); a profile setting only `PerHour` still inherits the install's `Lifetime` and `PerDay`
- All five QUO tests pass (5/5 PASS, 0 skips)

## Task Commits

Each task was committed atomically:

1. **RED — failing tests** - `4c1c5899` (test)
2. **GREEN — Record + ResolveLimits implementation** - `98a7ac19` (feat)

_TDD: RED → GREEN (no refactor needed — code was clean at GREEN)_

## Files Created/Modified
- `pkg/quota/quota.go` — Full `Record` implementation replacing skeleton; `atomicIncrement` helper; `hourBucket`/`dayBucket` helpers unchanged; removed dead `_ = awssdk.String("")` suppression
- `pkg/quota/resolve.go` — New file: `ResolveLimits(profileLimits, installDefaults Limits) Limits` with per-window precedence logic
- `pkg/quota/quota_test.go` — Removed `t.Skip` guards from QUO-02..05; wrote real assertions; added `multiCountClient` + `checkTTL` helpers

## Decisions Made
- TTL set via `if_not_exists` in the same `UpdateItem` as the `ADD` — avoids a second round-trip and prevents overwriting an existing TTL on repeat increments within the same bucket window (same approach as the hourly/daily design intent)
- `WorstWindow` is the first exceeded window in declaration order (lifetime, then hour, then day). This matches CONTEXT.md §2 "lifetime worst, then day, then hour" semantics — lifetime is declared first so it appears first in the exceeded list when it breaches
- `ResolveLimits` returns empty `OnBreach` for actions present in neither map (rather than `BreachWarn`) so callers can distinguish "unresolved" from "explicitly warn"

## Deviations from Plan
None — plan executed exactly as written.

## Issues Encountered
None.

## User Setup Required
None — no external service configuration required.

## Next Phase Readiness
- `pkg/quota` is fully tested and ready for import by Plans 03/04 (proxy + bridge chokepoints)
- `ResolveLimits` signature (`Limits → Limits`) is stable; callers pass resolved maps from config/profile
- The `QuotaAPI` interface is narrow (UpdateItem only) — matches the pattern used by BudgetAPI; no DDB client coupling

---
*Phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions*
*Completed: 2026-06-27*
