---
phase: 55-learn-mode-command-capture
plan: 01
subsystem: allowlistgen
tags: [go, learn-mode, shell-commands, recorder, generator, tdd]

# Dependency graph
requires:
  - phase: 55-learn-mode-command-capture
    provides: existing Recorder/Generator for DNS/hosts/repos/refs observations
provides:
  - RecordCommand/Commands API on Recorder with dedup and first-seen order
  - Generator populates Spec.Execution.InitCommands from recorded commands
  - GenerateAnnotatedYAML emits command comment block with suggested initCommands
affects:
  - 55-02 (EC2/Docker integration will use RecordCommand/Commands API)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Order-preserving dedup using dual data structure (map for O(1) lookup + slice for order)"
    - "TDD Red-Green cycle for each task with per-task atomic commits"

key-files:
  created:
    - pkg/allowlistgen/recorder_test.go (extended with 6 TestRecordCommand_* tests)
    - pkg/allowlistgen/generator_test.go (extended with 5 TestGenerate*Command* tests)
  modified:
    - pkg/allowlistgen/recorder.go
    - pkg/allowlistgen/generator.go

key-decisions:
  - "Commands() does NOT sort — first-seen order is semantically meaningful for shell command sequences"
  - "commandSeen map + commandOrdered slice dual structure for O(1) dedup with preserved order"
  - "InitCommands not populated when empty (omitempty honored) — no zero-length array in YAML"

patterns-established:
  - "Order-preserving dedup: map[string]struct{} for set membership + []string for insertion order"

requirements-completed: [LEARN-CMD-01, LEARN-CMD-02, LEARN-CMD-03]

# Metrics
duration: 1min
completed: 2026-04-18
---

# Phase 55 Plan 01: Learn Mode Command Capture — Recorder and Generator Summary

**RecordCommand/Commands API added to allowlistgen Recorder with dedup and first-seen order; Generator now populates InitCommands and annotated YAML comment blocks from recorded commands**

## Performance

- **Duration:** ~8 min (including research and TDD cycles)
- **Started:** 2026-04-18T12:35:14Z
- **Completed:** 2026-04-18T12:37:06Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments
- Added RecordCommand/Commands to Recorder with mutex-safe dedup using map + slice dual structure
- Commands() preserves first-seen order (unlike DNS/hosts/repos which sort alphabetically) because command sequence is semantically meaningful
- Generator.Generate() populates Spec.Execution.InitCommands from recorded commands when non-empty
- GenerateAnnotatedYAML() emits "# Commands observed (suggested initCommands — review before use)" comment block
- Updated header summary line to include command count: "Observed: N DNS domains, N hosts, N repos, N refs, N commands"
- 11 new tests (6 recorder + 5 generator), all 39 allowlistgen tests pass

## Task Commits

Each task was committed atomically:

1. **Task 1: Add RecordCommand/Commands to Recorder with tests** - `b1b73c6` (feat)
2. **Task 2: Extend Generator to emit commands in profile and annotations** - `d0834a0` (feat)

## Files Created/Modified
- `pkg/allowlistgen/recorder.go` - Added commandSeen/commandOrdered fields, RecordCommand(), Commands()
- `pkg/allowlistgen/recorder_test.go` - Added 6 TestRecordCommand_* tests
- `pkg/allowlistgen/generator.go` - Populate InitCommands in Generate(), add command block in GenerateAnnotatedYAML(), update header count
- `pkg/allowlistgen/generator_test.go` - Added 5 TestGenerate*Command* tests

## Decisions Made
- Commands() does not sort — command insertion order is semantically meaningful (setup steps must run in order)
- Used map + slice dual structure for O(1) dedup lookup while preserving first-seen order
- InitCommands omitted when no commands recorded (omitempty in struct tag handles this naturally)
- Command annotation comment uses "suggested initCommands — review before use" phrasing to match existing review-before-use tone

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- RecordCommand/Commands API is ready for Plan 02 integration (EC2/Docker km shell --learn sessions)
- Both EC2 and Docker integration paths can call RecordCommand() directly on the shared Recorder
- All allowlistgen tests pass; build succeeds

---
*Phase: 55-learn-mode-command-capture*
*Completed: 2026-04-18*
