---
phase: 48-profile-override-flags-for-km-create-targeted-budget-flags-and-generic-set
plan: "01"
subsystem: cli
tags: [cobra, profile, lifecycle, ttl, idle-timeout, flags, s3]

# Dependency graph
requires:
  - phase: 11-sandbox-auto-destroy-metadata-wiring
    provides: TTL EventBridge schedule creation in runCreate
  - phase: 01-schema-compiler-aws-foundation
    provides: SandboxProfile lifecycle types (TTL, IdleTimeout), ValidateSemantic

provides:
  - "--ttl flag on km create: overrides spec.lifecycle.ttl at invocation time"
  - "--idle flag on km create: overrides spec.lifecycle.idleTimeout at invocation time"
  - "--ttl 0 sentinel: disables EventBridge TTL schedule (sets TTL to empty string)"
  - "applyLifecycleOverrides() helper: reusable override logic with re-validation"
  - "S3 profile upload uses mutated profile when overrides applied"

affects:
  - create.go downstream consumers (create-handler Lambda reads .km-profile.yaml)
  - ttl-handler Lambda (uses TTL from stored profile)

# Tech tracking
tech-stack:
  added: [github.com/goccy/go-yaml (already in go.mod, now imported in create.go)]
  patterns:
    - "TTL=0 sentinel: empty string disables auto-destroy schedule without a separate bool flag"
    - "Override-then-revalidate: apply flag overrides after profile resolution, re-run ValidateSemantic before compilation"
    - "Mutated profile S3 upload: serialize resolvedProfile via yaml.Marshal when overrides applied"

key-files:
  created:
    - internal/app/cmd/create_override_test.go
  modified:
    - internal/app/cmd/create.go
    - pkg/profile/validate.go

key-decisions:
  - "TTL=0 uses empty string as sentinel (not a bool flag) — aligns with existing TTL expiry check that guards on TTL != \"\""
  - "applyLifecycleOverrides extracted as a standalone helper for testability and to keep both runCreate and runCreateRemote DRY"
  - "go-yaml (goccy) used for mutated profile serialization — already in project, consistent with validate.go and allowlistgen"
  - "runCreateRemote signature extended with ttlOverride/idleOverride parameters — consistent with existing aliasOverride pattern"

patterns-established:
  - "Profile mutation pattern: StringVar flag → applyXxxOverrides() after resolution, before compilation"
  - "S3 upload fork: when CLI overrides present, marshal resolvedProfile; else upload raw file bytes"

requirements-completed:
  - "--ttl flag on km create"
  - "--idle flag on km create"
  - "--ttl 0 disables auto-destroy"
  - "S3 profile upload uses mutated profile"

# Metrics
duration: 8min
completed: "2026-04-08"
---

# Phase 48 Plan 01: Profile Override Flags Summary

**--ttl and --idle CLI flags for km create that mutate resolvedProfile lifecycle fields before compilation, with TTL=0 sentinel disabling EventBridge schedule and S3 upload of mutated YAML**

## Performance

- **Duration:** 8 min
- **Started:** 2026-04-08T01:11:45Z
- **Completed:** 2026-04-08T01:19:19Z
- **Tasks:** 1 (TDD)
- **Files modified:** 3

## Accomplishments
- `--ttl` flag overrides `spec.lifecycle.ttl` at invocation; `--idle` overrides `spec.lifecycle.idleTimeout`
- `applyLifecycleOverrides()` helper validates durations, applies TTL=0 sentinel (empty string disables EventBridge), and re-runs `ValidateSemantic` to catch conflicts (e.g., `--idle 48h` with profile `ttl=24h`)
- Both `runCreate` and `runCreateRemote` apply overrides identically after profile resolution, before `compiler.Compile`
- S3 profile upload in `runCreate` serializes mutated `resolvedProfile` via `yaml.Marshal` when overrides present; `runCreateRemote` does the same for the remote artifact upload path
- `ValidateSemantic` Rule 1 gains clarifying comment: TTL="" (--ttl 0 sentinel) skips TTL >= idle check

## Task Commits

1. **Task 1: Add --ttl/--idle flags, applyLifecycleOverrides helper, S3 upload fix, and validate.go comment** - `1aa1247` (feat)

**Plan metadata:** (pending)

_Note: TDD — tests written first (RED), then implementation (GREEN), then test adjusted for exact pattern match._

## Files Created/Modified
- `internal/app/cmd/create.go` — flag declarations, applyLifecycleOverrides call sites, S3 upload fork, runCreate/runCreateRemote signature updates, new applyLifecycleOverrides helper function, go-yaml import
- `internal/app/cmd/create_override_test.go` — 6 source-level and binary integration tests covering all override scenarios
- `pkg/profile/validate.go` — TTL=0 sentinel comment added to ValidateSemantic Rule 1

## Decisions Made
- TTL=0 uses empty string as sentinel — aligns with the existing TTL expiry guard (`if resolvedProfile.Spec.Lifecycle.TTL != ""`) so no EventBridge schedule is created; no new bool field needed
- `applyLifecycleOverrides` extracted as helper so both `runCreate` and `runCreateRemote` share identical logic without duplication
- `github.com/goccy/go-yaml` used for serialization — already in `go.mod` and used in `pkg/profile/validate.go`; no new dependency

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- `--ttl` and `--idle` flags are live on `km create`
- Plan 48-02 or subsequent generic `--set` flag work can build on the same `applyLifecycleOverrides` pattern

---
*Phase: 48-profile-override-flags-for-km-create-targeted-budget-flags-and-generic-set*
*Completed: 2026-04-08*
