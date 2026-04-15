---
phase: 53-persistent-local-sandbox-numbering
plan: "01"
subsystem: cli
tags: [go, local-state, json, file-io, tdd]

requires: []
provides:
  - "pkg/localnumber package: State, Load, LoadFrom, Save, SaveTo, Assign, Remove, Resolve, Reconcile, StateFilePath"
  - "Persistent local sandbox numbering stored at ~/.config/km/local-numbers.json"
affects:
  - create.go (assign number at create time)
  - list.go (reconcile + display numbers)
  - sandbox_ref.go (resolve numeric ref from local file)
  - destroy.go (remove entry on destroy)

tech-stack:
  added: []
  patterns:
    - "Atomic file write via tmp+rename (os.Rename) — no partial-write corruption"
    - "XDG-compliant config dir via os.UserConfigDir() with home fallback"
    - "Exported testable helpers (LoadFrom/SaveTo) so tests use t.TempDir() without touching real config"
    - "Idempotent Assign — same ID always returns same number"

key-files:
  created:
    - pkg/localnumber/localnumber.go
    - pkg/localnumber/localnumber_test.go
  modified: []

key-decisions:
  - "Exposed LoadFrom/SaveTo as exported helpers (not internal) so tests are clean and future callers can use explicit paths"
  - "Reconcile resets Next only when map is empty after pruning — never when live IDs remain"
  - "Corrupt JSON silently returns fresh state; missing file also returns fresh state — no user-visible errors for first-run"

patterns-established:
  - "LoadFrom/SaveTo pattern: exported path-explicit helpers alongside Load/Save for testability"

requirements-completed:
  - LOCAL-01
  - LOCAL-02
  - LOCAL-03
  - LOCAL-04
  - LOCAL-05
  - LOCAL-06

duration: 2min
completed: 2026-04-13
---

# Phase 53 Plan 01: Local Number State Management Summary

**Self-contained `pkg/localnumber` package implementing monotonic local sandbox numbering via JSON state file at `~/.config/km/local-numbers.json` with atomic writes and full TDD coverage.**

## Performance

- **Duration:** ~2 min
- **Started:** 2026-04-13T21:32:56Z
- **Completed:** 2026-04-13T21:34:20Z
- **Tasks:** 1 (TDD: RED + GREEN)
- **Files modified:** 2

## Accomplishments

- Implemented full `pkg/localnumber` package with 7 exported symbols (State, Load, LoadFrom, Save, SaveTo, Assign, Remove, Resolve, Reconcile, StateFilePath)
- 7 test functions pass covering all 6 required behaviours plus nil-map edge case
- Atomic write pattern (tmp+rename) prevents state corruption on crash mid-write
- XDG-compliant path via `os.UserConfigDir()` with fallback to `$HOME/.config`

## Task Commits

Each TDD step was committed atomically:

1. **RED - Failing tests** - `5fd0da6` (test)
2. **GREEN - Implementation** - `12d0a21` (feat)

## Files Created/Modified

- `pkg/localnumber/localnumber.go` - State type, Load/LoadFrom, Save/SaveTo, Assign, Remove, Resolve, Reconcile, StateFilePath
- `pkg/localnumber/localnumber_test.go` - 7 test functions using t.TempDir() for full isolation

## Decisions Made

- Exported `LoadFrom`/`SaveTo` as public helpers (not unexported) so tests can use them cleanly without reflection or build tags. Future callers in other packages can also use explicit paths if needed.
- `Reconcile` resets `Next` to 1 only when the map is empty after pruning — matches spec's "counter resets only when all sandboxes are gone".
- Missing or corrupt JSON both return a fresh empty state with no error returned to caller — first-run experience is seamless.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `pkg/localnumber` is ready to wire into `create.go`, `list.go`, `sandbox_ref.go`, and `destroy.go` (plan 53-02 through 53-04).
- All exports are stable; no breaking changes anticipated in wiring plans.

---
*Phase: 53-persistent-local-sandbox-numbering*
*Completed: 2026-04-13*
