---
phase: 44-km-at-schedule-eventbridge-scheduler-command-for-deferred-and-recurring-sandbox-operations
plan: 01
subsystem: scheduling
tags: [eventbridge, scheduler, natural-language, time-parsing, cron, olebedev-when, tdd]

# Dependency graph
requires: []
provides:
  - "pkg/at/parser.go: Parse(), ValidateCron(), SanitizeScheduleName(), GenerateScheduleName()"
  - "ScheduleSpec type with Expression, IsRecurring, HumanExpr fields"
  - "Natural language -> EventBridge at()/cron()/rate() conversion"
affects:
  - "44-02 onwards: km at command implementation will import pkg/at"

# Tech tracking
tech-stack:
  added:
    - "github.com/olebedev/when v1.1.0 — natural language date/time parsing for Go"
    - "github.com/AlekSi/pointer v1.0.0 — transitive dependency of olebedev/when"
  patterns:
    - "TDD: failing test commit -> implementation commit -> refactor commit"
    - "EventBridge DOW mapping table (1=SUN..7=SAT) defined once, referenced by all cron formatting"
    - "Recurring detection via keyword prefix list before falling through to olebedev/when"

key-files:
  created:
    - "pkg/at/parser.go"
    - "pkg/at/parser_test.go"
  modified:
    - "go.mod"
    - "go.sum"

key-decisions:
  - "Use olebedev/when v1.1.0 for one-time natural language parsing (not chrono or naturaldate)"
  - "Recurring expressions handled by custom regex before olebedev/when to avoid misclassification"
  - "EventBridge cron day-of-week: 1=SUN through 7=SAT (never unix 0-based convention)"
  - "ValidateCron enforces DOM/DOW mutual exclusion: exactly one of day-of-month or day-of-week must be ?"

patterns-established:
  - "Recurring keyword detection: check for 'every', 'each', 'weekly', 'daily', 'monthly' before invoking NLP parser"
  - "Time string parsing: multi-format fallback (15:04, 3:04pm, 3pm) with noon/midnight aliases"
  - "SanitizeScheduleName: spaces->dashes, strip [^0-9a-zA-Z-_.], collapse multi-dash, trim edges, truncate to 64"
  - "GenerateScheduleName: prefix km-at-, join non-empty parts, sanitize result"

requirements-completed: [SCHED-PARSE]

# Metrics
duration: 3min
completed: 2026-04-03
---

# Phase 44 Plan 01: Natural Language Time Parser Summary

**olebedev/when-backed parser converts human time expressions into EventBridge at()/cron()/rate() with correct DOW mapping and name sanitization**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-04-03T06:53:54Z
- **Completed:** 2026-04-03T06:56:12Z
- **Tasks:** 1 (TDD: 3 commits — RED/GREEN/REFACTOR)
- **Files modified:** 4 (parser.go, parser_test.go, go.mod, go.sum)

## Accomplishments

- 28 tests covering all Parse() cases (one-time and recurring), ValidateCron(), SanitizeScheduleName(), GenerateScheduleName(), and day-of-week mapping
- Full EventBridge cron expression generation with correct 6-field format and DOW 1=SUN convention
- rate() expressions for "every N hours/minutes" patterns
- Robust sanitization: spaces to dashes, invalid char stripping, 64-char truncation, empty result detection

## Task Commits

1. **RED: Failing tests** - `d3ae35a` (test)
2. **GREEN + REFACTOR: Implementation** - `20a1533` (feat)

## Files Created/Modified

- `pkg/at/parser.go` — ScheduleSpec struct; Parse(), ValidateCron(), SanitizeScheduleName(), GenerateScheduleName() (289 lines)
- `pkg/at/parser_test.go` — 28 comprehensive tests covering all behavior specs (347 lines)
- `go.mod` — added github.com/olebedev/when v1.1.0, github.com/AlekSi/pointer v1.0.0
- `go.sum` — updated checksums

## Decisions Made

- olebedev/when chosen over alternatives: well-maintained, supports "tomorrow", "next tuesday", "in 30 minutes", "tonight"
- Custom recurring regex parser runs before olebedev/when to prevent misclassification of "every thursday" as a one-time event
- EventBridge DOW convention documented explicitly in code with comment pointing out the unix-cron pitfall

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `pkg/at` package is fully tested and ready for import by the `km at` command implementation
- ScheduleSpec type is the contract between the parser and EventBridge Scheduler API calls
- No blockers

---
*Phase: 44-km-at-schedule*
*Completed: 2026-04-03*
