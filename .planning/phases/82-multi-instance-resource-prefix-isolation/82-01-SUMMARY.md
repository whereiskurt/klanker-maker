---
phase: 82-multi-instance-resource-prefix-isolation
plan: "01"
subsystem: cli
tags: [configure, resource_prefix, multi-instance, cobra, yaml]

# Dependency graph
requires:
  - phase: 66-resource-prefix-email-subdomain
    provides: "resource_prefix field in platformConfig + km configure wizard"
provides:
  - "Preserve-on-re-run logic for resource_prefix in km configure"
  - "--reset-prefix flag for explicit re-defaulting"
  - "Two new test cases proving both behaviors"
affects:
  - 82-02
  - 82-03
  - 82-04

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Read-before-write config guard: read existing km-config.yaml and use its values as prompt defaults"
    - "Explicit opt-in reset flag pattern for one-time choices that must not silently change"

key-files:
  created: []
  modified:
    - internal/app/cmd/configure.go
    - internal/app/cmd/configure_test.go

key-decisions:
  - "Changed --resource-prefix flag default from 'km' to '' so Cobra cannot distinguish 'user passed km' from 'nothing passed'; defaulting now happens inside runConfigure after checking disk"
  - "Preserve logic is scoped to non-empty outputDir only (same path as the write side, per RESEARCH pitfall #5)"
  - "existingPrefix is captured before the interactive/non-interactive branch so both code paths share one defaultPrefix"

patterns-established:
  - "Configure preserve-on-re-run: read existing config before prompting; use existing value as default"
  - "Explicit --reset-prefix opt-in flag for potentially destructive one-time-choice resets"

requirements-completed: []

# Metrics
duration: 11min
completed: "2026-05-16"
---

# Phase 82 Plan 01: Configure Footgun Fix Summary

**km configure re-run now preserves an existing non-default resource_prefix instead of silently resetting it to "km"; --reset-prefix flag provides explicit opt-in re-defaulting**

## Performance

- **Duration:** 11 min
- **Started:** 2026-05-16T12:43:52Z
- **Completed:** 2026-05-16T12:55:43Z
- **Tasks:** 2 (TDD: RED → GREEN)
- **Files modified:** 2

## Accomplishments

- Fixed three unconditional `resourcePrefix = "km"` sites in `runConfigure` — two in the interactive branch, one in the non-interactive branch — to use `defaultPrefix` which is populated from the existing `km-config.yaml` when present
- Added `--reset-prefix` bool Cobra flag (default false) that bypasses the preserve logic and restores the old "km" hard-default behaviour
- Added `TestConfigureRerunPreservesResourcePrefix` and `TestConfigureResetPrefixFlag` integration tests using the existing `buildKM` + `runKMArgs` pattern; both pass green
- All pre-existing configure tests remain green; the only full-suite failure (`TestUnlockCmd_RequiresStateBucket`) is a pre-existing issue unrelated to this plan

## Task Commits

Each task was committed atomically:

1. **Task 1 + Task 2: Preserve-on-re-run + --reset-prefix flag + tests** - `2f02000` (feat)

## Files Created/Modified

- `internal/app/cmd/configure.go` — Added `resetPrefix` var, `--reset-prefix` flag, existingPrefix read-before-prompt block, `defaultPrefix` logic replaces three hardcoded `"km"` defaults; `runConfigure` signature gains `resetPrefix bool` parameter
- `internal/app/cmd/configure_test.go` — Added `TestConfigureRerunPreservesResourcePrefix` and `TestConfigureResetPrefixFlag` tests

## Decisions Made

- Changed `--resource-prefix` flag default from `"km"` to `""` so the preserve logic can detect "nothing passed" vs "user explicitly passed km". The `"km"` default now lives in `defaultPrefix` inside `runConfigure`, applied only when no existing config is found. This is a subtle but required change — if the flag default remained `"km"`, there would be no way to distinguish user intent from Cobra's zero-value fill.
- Preserve logic is guarded by `outputDir != ""` only. When `outputDir` is empty, `findRepoRoot()` is used for the write path; same guard would apply there if needed in a future iteration. Keeping it `outputDir`-scoped matches the plan's pitfall #5 guidance.

## Deviations from Plan

None — plan executed exactly as written. The one deviation worth noting: the plan listed Tasks 1 and 2 separately (implementation then tests), but TDD flow reverses the order (tests RED first, then implementation GREEN). Both tasks were committed in a single atomic commit since they are mutually dependent.

## Issues Encountered

None. The pre-existing `TestUnlockCmd_RequiresStateBucket` failure was confirmed via `git stash` to predate this plan.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Plan 82-01 complete. The configure footgun (C1 in CONTEXT.md decisions) is closed.
- Plans 82-02 (configui/bridge hard-fail), 82-03 (AMI tag at bake), and subsequent Wave 2/3 Terraform plans can proceed independently.
- No `km init --sidecars` or AWS changes required for this plan — `make build` is sufficient.

---
*Phase: 82-multi-instance-resource-prefix-isolation*
*Completed: 2026-05-16*
