---
phase: 93-km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward
plan: "00"
subsystem: testing
tags: [tdd, wave-0, stubs, desktop, kasmvnc, go-testing]

requires: []
provides:
  - "12 skipped Desktop test stubs across 4 files (pkg/profile/types_test.go, pkg/profile/validate_test.go, pkg/compiler/userdata_test.go, internal/app/cmd/desktop_test.go)"
  - "Fixed RED→GREEN targets for each DSK requirement (DSK-02, DSK-03, DSK-05/06/07/08/09/10/11)"
affects:
  - 93-01-PLAN (Wave 1 — RuntimeDesktopSpec type + IsDesktopEnabled; flips TestIsDesktopEnabled)
  - 93-02-PLAN (Wave 2 — semantic validation; flips TestDesktopValidate*)
  - 93-03-PLAN (Wave 2 — userdata compiler; flips TestUserDataDesktop*)
  - 93-04-PLAN (Wave 3 — credential write; flips TestDesktopCredential)
  - 93-05-PLAN (Wave 3 — CLI start/status; flips TestDesktopStart, TestDesktopStatus)

tech-stack:
  added: []
  patterns:
    - "Wave 0 TDD discipline: all stubs use t.Skip as first/only statement so they compile before referenced types exist"
    - "desktop_test.go mirrors vscode_test.go structure without redeclaring package-level mocks (Wave 0 bodies are skip-only)"

key-files:
  created:
    - internal/app/cmd/desktop_test.go
  modified:
    - pkg/profile/types_test.go
    - pkg/profile/validate_test.go
    - pkg/compiler/userdata_test.go

key-decisions:
  - "Wave 0 stubs use t.Skip as first and only statement — no type references — so the package compiles before 93-01 lands RuntimeDesktopSpec"
  - "desktop_test.go bodies are skip-only in Wave 0 to avoid duplicate mock declarations with vscode_test.go (same package); real mock wiring lands in 93-04/93-05"

patterns-established:
  - "Each stub comment cites the Wave/Plan that will implement it plus the DSK requirement it targets"

requirements-completed: [DSK-15-TESTS]

duration: 8min
completed: 2026-06-02
---

# Phase 93 Plan 00: Wave 0 Desktop TDD Stubs Summary

**12 skipped Desktop test stubs landed across profile, compiler, and CLI packages — giving Wave 1–3 a fixed RED→GREEN target per DSK requirement before any production code changes**

## Performance

- **Duration:** ~8 min
- **Started:** 2026-06-02T20:19:00Z
- **Completed:** 2026-06-02T20:27:06Z
- **Tasks:** 2
- **Files modified:** 4 (3 modified, 1 created)

## Accomplishments

- Appended `TestIsDesktopEnabled` stub (DSK-02) to `pkg/profile/types_test.go`
- Appended 4 `TestDesktopValidate*` stubs (DSK-03) to `pkg/profile/validate_test.go`
- Appended 6 `TestUserDataDesktop*` stubs (DSK-05/06/07/08/11) to `pkg/compiler/userdata_test.go`
- Created `internal/app/cmd/desktop_test.go` with `TestDesktopStart`, `TestDesktopStatus`, `TestDesktopCredential` stubs (DSK-08/09/10)
- All 12 stubs discoverable by `go test -run Desktop`, compile clean, pass as SKIP

## Task Commits

1. **Task 1: profile package Desktop test stubs** - `6dfeae24` (test)
2. **Task 2: compiler + CLI Desktop test stubs** - `be840290` (test)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/profile/types_test.go` - Added `TestIsDesktopEnabled` stub (DSK-02)
- `/Users/khundeck/working/klankrmkr/pkg/profile/validate_test.go` - Added 4 `TestDesktopValidate*` stubs (DSK-03)
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata_test.go` - Added 6 `TestUserDataDesktop*` stubs (DSK-05/06/07/08/11)
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/desktop_test.go` - New file; 3 CLI stubs (DSK-08/09/10)

## Decisions Made

- Wave 0 stubs use `t.Skip` as first and only statement — no type references in bodies — so the package compiles before `93-01` lands `RuntimeDesktopSpec`. This matches the Phase 92 Wave 0 precedent.
- `desktop_test.go` keeps bodies skip-only in Wave 0 to avoid duplicate mock type declarations with `vscode_test.go` (same `package cmd`). Real mock wiring and test bodies land in `93-04`/`93-05`.

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- Wave 0 complete: all 12 named Desktop test stubs exist and are SKIP-green
- Wave 1 (93-01) can now add `RuntimeDesktopSpec` to `pkg/profile/types.go` and flip `TestIsDesktopEnabled` from SKIP to GREEN
- Each subsequent wave has a fixed named test(s) to flip RED→GREEN per DSK requirement

## Self-Check: PASSED

All files verified present. Both task commits verified in git log.

---
*Phase: 93-km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward*
*Completed: 2026-06-02*
