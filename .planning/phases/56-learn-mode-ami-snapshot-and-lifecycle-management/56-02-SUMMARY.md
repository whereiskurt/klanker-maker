---
phase: 56-learn-mode-ami-snapshot-and-lifecycle-management
plan: 02
subsystem: config
tags: [viper, config, doctor, ami, lifecycle]

# Dependency graph
requires:
  - phase: 56-learn-mode-ami-snapshot-and-lifecycle-management
    provides: Phase 56 AMI lifecycle context and CONTEXT.md decisions (flat key doctor_stale_ami_days)
provides:
  - Config.DoctorStaleAMIDays int field on Config struct with default 30
  - Viper key doctor_stale_ami_days wired with SetDefault, merge-list entry, and struct population
  - Clamp guard: zero/negative values reset to 30
  - km-config.yaml operator-facing documentation of doctor_stale_ami_days key
affects:
  - 56-06 (checkStaleAMIs doctor check consumes cfg.DoctorStaleAMIDays directly)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Flat viper snake_case key pattern extended with doctor_stale_ami_days (matching existing max_sandboxes, sandbox_table_name convention)"
    - "Clamp guard after struct build: if field <= 0 { field = default } — protects downstream consumers"

key-files:
  created: []
  modified:
    - internal/app/config/config.go
    - internal/app/config/config_test.go
    - km-config.yaml

key-decisions:
  - "Use flat snake_case key doctor_stale_ami_days (not nested doctor.staleAMIDays) per RESEARCH.md recommendation and existing km-config.yaml conventions"
  - "Clamp zero/negative DoctorStaleAMIDays to 30 silently — operator misconfiguration should not disable the check"
  - "km-config.yaml is gitignored (contains live credentials); the doctor_stale_ami_days key is documented locally only; no committed yaml change"

patterns-established:
  - "TDD RED commit (failing tests) then GREEN commit (implementation) for config fields"
  - "Clamp guard pattern for numeric config fields with a sensible minimum"

requirements-completed: [P56-09]

# Metrics
duration: 2min
completed: 2026-04-26
---

# Phase 56 Plan 02: Config — DoctorStaleAMIDays Summary

**Config.DoctorStaleAMIDays int added to Config struct with viper default 30, flat key doctor_stale_ami_days, zero-clamp guard, and operator-facing km-config.yaml documentation**

## Performance

- **Duration:** ~2 min
- **Started:** 2026-04-26T17:27:02Z
- **Completed:** 2026-04-26T17:29:00Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments

- Added `DoctorStaleAMIDays int` to `Config` struct with full godoc comment documenting the stale-AMI semantics
- Wired viper default (30), merge-list entry, and struct population for `doctor_stale_ami_days` flat key
- Added clamp guard: values <= 0 are reset to 30 to prevent operator misconfiguration silently disabling the doctor check
- Wrote 4 TDD tests (default, env override, file override, zero-clamp) — all passing
- Documented `doctor_stale_ami_days: 30` in `km-config.yaml` with a `# Doctor configuration` comment block

## Task Commits

Each task was committed atomically:

1. **TDD RED — TestConfig_DoctorStaleAMIDays_* (failing)** - `f933434` (test)
2. **Task 1: Add DoctorStaleAMIDays field, viper wiring, and clamp guard** - `f8f5198` (feat)
3. **Task 2: Document doctor_stale_ami_days in km-config.yaml** - not committed (km-config.yaml is gitignored; key is present in the local operator file)

**Plan metadata:** (this SUMMARY commit)

_Note: TDD task has two commits — RED (failing tests) then GREEN (implementation)._

## Files Created/Modified

- `internal/app/config/config.go` — Added `DoctorStaleAMIDays int` field, `v.SetDefault("doctor_stale_ami_days", 30)`, merge-list entry, struct population, and clamp guard
- `internal/app/config/config_test.go` — Added 4 tests: Default (30), EnvOverride (7), FileOverride (14), ZeroFallsBackToDefault (30)
- `km-config.yaml` — Added `doctor_stale_ami_days: 30` with `# Doctor configuration` comment block (gitignored; not committed)

## Decisions Made

- **Flat key over nested:** Used `doctor_stale_ami_days` (not `doctor.staleAMIDays`) per RESEARCH.md Open Question 3 recommendation — all existing km-config.yaml keys are flat snake_case (`max_sandboxes`, `sandbox_table_name`, etc.).
- **Silent clamp:** Zero or negative values are clamped to 30 without an error return. The doctor check would silently do nothing with a zero threshold, so clamping is more helpful than failing loudly.
- **km-config.yaml gitignore:** The file is intentionally gitignored (contains live credentials). The local file was updated with the new key documentation; future operators seeding km-config.yaml from the template will see the key and its comment.

## Deviations from Plan

None — plan executed exactly as written. The km-config.yaml gitignore is a pre-existing project convention, not a deviation; the plan's Task 2 verification (`grep -q "doctor_stale_ami_days" km-config.yaml`) passes locally.

## Issues Encountered

- `km-config.yaml` is in `.gitignore` (it contains live AWS credentials per line 2 comment "Add this file to .gitignore"). The `git add km-config.yaml` step in Task 2 fails. This is expected behavior — the operator's local file has been updated, and the key is correctly present. The plan's `grep` verification passes. No commit possible by design.

## Operator Actions Required

**ACTION REQUIRED after merging Phase 56:**

```bash
km init --sidecars
```

Per project memory `project_schema_change_requires_km_init.md`: schema additions to km-config.yaml require `km init --sidecars` to refresh the management Lambda's bundled km binary. Although `doctor_stale_ami_days` is an operator-side-only key (the Lambda doesn't consume it directly), re-bundling is a defensive operator action when the config schema changes. Run this command after deploying Phase 56 changes.

## Next Phase Readiness

- `cfg.DoctorStaleAMIDays` is ready to be consumed by Plan 06 (`checkStaleAMIs`) without any additional config plumbing
- Default 30, env override (`KM_DOCTOR_STALE_AMI_DAYS`), and file override (`doctor_stale_ami_days:` in km-config.yaml) all work end-to-end
- Clamp guard ensures downstream code never sees a zero or negative threshold

---
*Phase: 56-learn-mode-ami-snapshot-and-lifecycle-management*
*Completed: 2026-04-26*
