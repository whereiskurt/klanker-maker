---
phase: 117-composable-multi-parent-profile-inheritance-deep-merge-list-union-extends
plan: 01
subsystem: profile
tags: [goccy-go-yaml, json-schema, extends, inheritance, union-type, fragment]

# Dependency graph
requires:
  - phase: 92-sandboxprofile-spec-restructure
    provides: Phase 92 cleaned up spec structure (spec.iam, spec.agent, spec.notification) that this extends
provides:
  - ExtendsField []string union type with IsSet()/List() accessors and goccy UnmarshalYAML
  - IsAbstractFragment(raw []byte) bool detector in pkg/profile/validate.go
  - Metadata.Abstract bool field + ExecutionSpec.InitCommandsAppend []string in types.go
  - JSON schema: extends oneOf[string,array]; metadata.abstract boolean; execution.initCommandsAppend array
  - All three original extends call sites (validate.go, create.go budget.go, allowlistgen) updated to IsSet()/List()
affects:
  - 117-02 (multi-parent DAG merge depends on ExtendsField type)
  - 117-03 (Resolve() wiring depends on ExtendsField.List() iteration)
  - Any future code that reads SandboxProfile.Extends

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "goccy/go-yaml context-aware custom unmarshaler: func (e *T) UnmarshalYAML(ctx context.Context, unmarshal func(interface{}) error) error"
    - "ExtendsField: type alias over []string with IsSet()/List() — avoids string comparison traps"
    - "IsAbstractFragment: yaml.Unmarshal into map[string]any, fail-open on any error"

key-files:
  created: []
  modified:
    - pkg/profile/types.go — ExtendsField type, UnmarshalYAML, accessors; Metadata.Abstract; ExecutionSpec.InitCommandsAppend
    - pkg/profile/inherit.go — resolve() adapted: !IsSet(), List()[0] single-parent chain, result.Extends = nil
    - pkg/profile/validate.go — IsAbstractFragment() added
    - pkg/profile/schemas/sandbox_profile.schema.json — extends oneOf; metadata.abstract; execution.initCommandsAppend
    - pkg/profile/inherit_test.go — TestExtendsUnmarshal (scalar/sequence/absent/accessors); TestResolveExtendsCleared adapted
    - pkg/profile/validate_test.go — TestIsAbstractFragment; TestValidateSchemaExtendsArrayForm; TestValidateSchemaExtendsStringForm
    - internal/app/cmd/validate.go — IsSet()/List()[0] at line 76; strings.Join for logging
    - internal/app/cmd/create.go — IsSet()/List()[0] at lines 342 and 2094
    - internal/app/cmd/budget.go — additional consumer found and fixed (auto-fix Rule 1)
    - pkg/allowlistgen/generator.go — Extends string→ExtendsField constructor

key-decisions:
  - "B (locked): fragment marker = metadata.abstract: true (not a separate file extension or top-level key)"
  - "D (locked): explicit execution.initCommandsAppend field; general +key append convention DEFERRED (out of scope v1)"
  - "Single-parent chain still uses List()[0] in Plan 01; Plan 02 wires DAG multi-parent"
  - "goccy context-aware UnmarshalYAML signature confirmed working at v1.19.2 (RESEARCH MEDIUM confidence resolved)"

patterns-established:
  - "ExtendsField.IsSet() pattern: callers must never use != \"\" on the field"
  - "allowlistgen.Generate: string → ExtendsField{s} constructor pattern for callers that build profiles programmatically"

requirements-completed: []

# Metrics
duration: 7min
completed: 2026-06-24
---

# Phase 117 Plan 01: Schema/Types Foundation Summary

**ExtendsField union type (string|[]string) with goccy UnmarshalYAML, IsAbstractFragment detector, metadata.abstract + initCommandsAppend fields, and oneOf JSON schema — foundation for composable multi-parent inheritance**

## Performance

- **Duration:** 7 min
- **Started:** 2026-06-24T12:14:48Z
- **Completed:** 2026-06-24T12:21:52Z
- **Tasks:** 3 (all TDD)
- **Files modified:** 10

## Accomplishments
- ExtendsField union type compiles and round-trips both scalar and sequence YAML with a goccy context-aware unmarshaler
- All five consumers of SandboxProfile.Extends updated to IsSet()/List() — zero string comparison traps remain
- IsAbstractFragment() detects metadata.abstract: true, fail-open on any error
- JSON schema extended: extends now accepts string OR array; metadata.abstract boolean declared; execution.initCommandsAppend declared
- Full pkg/profile suite (including existing inherit/notification/agent tests) stays green

## Task Commits

1. **Task 1: ExtendsField union type + UnmarshalYAML + accessors (TDD)** - `f4a131b2` (feat)
2. **Task 2: Fix all call sites + inherit.go single-parent path** - `b46b476f` (feat)
3. **Task 3: IsAbstractFragment + initCommandsAppend + JSON schema (TDD)** - `d4f78f1c` (feat)

## Files Created/Modified
- `/Users/khundeck/working/klankrmkr/pkg/profile/types.go` — ExtendsField type + UnmarshalYAML + accessors; Metadata.Abstract; ExecutionSpec.InitCommandsAppend
- `/Users/khundeck/working/klankrmkr/pkg/profile/inherit.go` — resolve() uses !IsSet(), List()[0], result.Extends = nil
- `/Users/khundeck/working/klankrmkr/pkg/profile/validate.go` — IsAbstractFragment() function
- `/Users/khundeck/working/klankrmkr/pkg/profile/schemas/sandbox_profile.schema.json` — extends oneOf; metadata.abstract; initCommandsAppend
- `/Users/khundeck/working/klankrmkr/pkg/profile/inherit_test.go` — TestExtendsUnmarshal; existing TestResolveExtendsCleared adapted
- `/Users/khundeck/working/klankrmkr/pkg/profile/validate_test.go` — TestIsAbstractFragment; TestValidateSchemaExtendsArrayForm; TestValidateSchemaExtendsStringForm
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/validate.go` — IsSet()/List()[0] + strings import
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/create.go` — IsSet()/List()[0] at both extends checks
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/budget.go` — additional consumer auto-fixed
- `/Users/khundeck/working/klankrmkr/pkg/allowlistgen/generator.go` — Extends field constructor updated

## Decisions Made
- Fragment marker: `metadata.abstract: true` (locked decision B from VALIDATION.md)
- `execution.initCommandsAppend` declared as an explicit typed field; general `+key` convention deferred (locked decision D)
- Plan 01 single-parent chain: `List()[0]` with `// TODO(Plan 02)` comment; Plan 02 owns the DAG walk
- goccy UnmarshalYAML context-aware signature (2-arg) confirmed at v1.19.2 — RESEARCH MEDIUM confidence resolved to HIGH

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed additional Extends consumers not in the plan**
- **Found during:** Task 2 (call site fixes)
- **Issue:** `go build ./...` revealed two additional consumers: `internal/app/cmd/budget.go` (line 358) and `pkg/allowlistgen/generator.go` (line 50) — neither listed in the plan's files_modified
- **Fix:** Applied IsSet()/List()[0] pattern to budget.go; used ExtendsField{base} constructor in allowlistgen/generator.go
- **Files modified:** internal/app/cmd/budget.go, pkg/allowlistgen/generator.go
- **Verification:** `go build ./...` succeeds
- **Committed in:** b46b476f (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 — additional call sites discovered at compile time)
**Impact on plan:** Required for correctness; no scope creep.

## Issues Encountered
None — goccy UnmarshalYAML signature worked first try; the only surprise was the two undocumented consumers caught by `go build ./...`.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- ExtendsField type, IsAbstractFragment, schema updates: complete foundation for Plan 02
- Plan 02 can safely depend on ExtendsField.List() returning all parents in declaration order
- inherit.go has a `// TODO(Plan 02): DAG multi-parent` comment marking the replacement point
- The existing inherit_test / inherit_notification_test / inherit_agent_test regression net is green and guards single-parent behavior

---
*Phase: 117-composable-multi-parent-profile-inheritance-deep-merge-list-union-extends*
*Completed: 2026-06-24*
