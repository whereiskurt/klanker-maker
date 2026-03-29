---
phase: 29-configurable-sandbox-id-prefix
plan: 01
subsystem: profile
tags: [sandbox-id, profile-schema, json-schema, compiler, go]

# Dependency graph
requires:
  - phase: 01-schema-compiler-aws-foundation
    provides: profile types.go, JSON Schema, compiler package, create.go
provides:
  - Metadata.Prefix field in SandboxProfile types
  - JSON Schema prefix property with pattern validation
  - Parameterized GenerateSandboxID(prefix string)
  - IsValidSandboxID(id string) helper
  - create.go wired to pass Metadata.Prefix at both call sites
affects: [29-02, compiler, create-handler, email-create-handler]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Sandbox ID format: {prefix}-{8hex} where prefix defaults to 'sb' when empty"
    - "JSON Schema pattern validation: ^[a-z][a-z0-9]{0,11}$ for prefix field"
    - "IsValidSandboxID as validation helper for override inputs"

key-files:
  created: []
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/validate_test.go
    - pkg/compiler/sandbox_id.go
    - pkg/compiler/compiler_test.go
    - internal/app/cmd/create.go

key-decisions:
  - "GenerateSandboxID signature changed from () to (prefix string) — empty string defaults to 'sb' for backwards compatibility"
  - "IsValidSandboxID validates ^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$ — same constraint as prefix + hex"
  - "sandboxIDOverride in runCreate validated with IsValidSandboxID before use"
  - "cmd/email-create-handler deferred to Plan 02 — passes '' for now"

patterns-established:
  - "TDD RED/GREEN per task: failing tests committed before implementation"
  - "Prefix pattern established as single source of truth in JSON Schema regex"

requirements-completed: [PREFIX-01, PREFIX-02, PREFIX-05]

# Metrics
duration: 5min
completed: 2026-03-28
---

# Phase 29 Plan 01: Configurable Sandbox ID Prefix Summary

**Metadata.Prefix field wired end-to-end: JSON Schema validates prefix pattern, GenerateSandboxID(prefix) generates prefixed IDs, create.go passes Metadata.Prefix at both runCreate and runCreateRemote call sites**

## Performance

- **Duration:** 5 min
- **Started:** 2026-03-28T00:39:33Z
- **Completed:** 2026-03-28T00:44:42Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments

- Added `Prefix string` field to `Metadata` struct with `yaml:"prefix,omitempty"` tag
- Added `prefix` property to JSON Schema `metadata.properties` with pattern `^[a-z][a-z0-9]{0,11}$` — enforces lowercase alpha start, 1-12 chars, alphanumeric only
- Changed `GenerateSandboxID()` to `GenerateSandboxID(prefix string)` — empty prefix defaults to `"sb"` for backwards compatibility
- Added `IsValidSandboxID(id string) bool` that validates `^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$`
- Wired `resolvedProfile.Metadata.Prefix` into both `runCreate` and `runCreateRemote` in create.go; `sandboxIDOverride` now validated with `IsValidSandboxID`

## Task Commits

Each task was committed atomically with RED then GREEN:

1. **Task 1 RED: Failing prefix schema tests** - `e5173cb` (test)
2. **Task 1 GREEN: Metadata.Prefix + JSON Schema** - `fbd3a2f` (feat)
3. **Task 2 RED: Failing GenerateSandboxID/IsValidSandboxID tests** - `21906d3` (test)
4. **Task 2 GREEN: Parameterized ID generation + wired create.go** - `9a55337` (feat)

_Note: TDD tasks produced two commits per task (test → feat)_

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/profile/types.go` - Added `Prefix string` to Metadata struct
- `/Users/khundeck/working/klankrmkr/pkg/profile/schemas/sandbox_profile.schema.json` - Added prefix property with pattern validation
- `/Users/khundeck/working/klankrmkr/pkg/profile/validate_test.go` - Added `TestValidateSchema_MetadataPrefix` with 9 table-driven cases and `minimalProfileWithPrefix` helper
- `/Users/khundeck/working/klankrmkr/pkg/compiler/sandbox_id.go` - `GenerateSandboxID(prefix)`, `IsValidSandboxID(id)`, `validSandboxIDPattern` regex
- `/Users/khundeck/working/klankrmkr/pkg/compiler/compiler_test.go` - Updated `TestGenerateSandboxID` with sub-tests; added `TestIsValidSandboxID`
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/create.go` - Both GenerateSandboxID calls pass Metadata.Prefix; override validated

## Decisions Made

- `GenerateSandboxID` signature changed to accept `prefix string` — empty defaults to `"sb"` for strict backwards compatibility with existing `sb-XXXXXXXX` IDs
- `IsValidSandboxID` uses the same character class as the schema prefix pattern, generalized to match the full `prefix-hex` format
- `sandboxIDOverride` (passed from Lambda create-handler) is now validated with `IsValidSandboxID` before use — rejects malformed overrides with a clear error
- `cmd/email-create-handler/main.go` deferred to Plan 02 as documented in the plan action: passes `""` to maintain compilation

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Plan 02 can wire `email-create-handler` to pass a profile prefix once it has profile resolution
- All callers of `GenerateSandboxID` compile cleanly
- `km create <profile.yaml>` with `metadata.prefix: claude` will generate `claude-XXXXXXXX` sandbox IDs

## Self-Check: PASSED

All created/modified files confirmed present. All task commits confirmed in git log.

---
*Phase: 29-configurable-sandbox-id-prefix*
*Completed: 2026-03-28*
