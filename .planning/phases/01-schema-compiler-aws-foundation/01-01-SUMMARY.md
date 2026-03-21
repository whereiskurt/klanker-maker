---
phase: 01-schema-compiler-aws-foundation
plan: "01"
subsystem: schema
tags: [go, yaml, jsonschema, validation, types, tdd]

# Dependency graph
requires: []
provides:
  - SandboxProfile Go struct hierarchy with all 10 spec sections
  - Parse([]byte) function for YAML unmarshaling via goccy/go-yaml
  - JSON Schema Draft 2020-12 at schemas/sandbox_profile.schema.json
  - ValidateSchema([]byte) with JSON-path-formatted errors
  - ValidateSemantic(*SandboxProfile) for logical constraint checks
  - Validate([]byte) combined schema + semantic validation
  - ValidationError type with "path: message" Error() format
  - Test fixtures for valid, missing-spec, unknown-field, bad-substrate profiles
affects:
  - 01-02-profile-compiler (depends on SandboxProfile types and Validate)
  - 01-03-builtin-profiles (depends on types and validation)
  - 01-04-km-validate-cli (depends on Validate() function signature)
  - all subsequent phases (root data model)

# Tech tracking
tech-stack:
  added:
    - github.com/goccy/go-yaml v1.19.2 (YAML parsing)
    - github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 (JSON Schema Draft 2020-12 validation)
  patterns:
    - go:embed for schema loading from pkg/profile/schemas/ (schema lives in two places: schemas/ root for tooling, pkg/profile/schemas/ for embedding)
    - sync.Once compiled schema singleton
    - YAML->JSON->schema-validate pipeline in ValidateSchema
    - TDD: failing tests committed before implementation

key-files:
  created:
    - go.mod (module github.com/whereiskurt/klankrmkr)
    - go.sum
    - cmd/km/main.go (placeholder entry point)
    - pkg/profile/types.go (SandboxProfile + all 10 spec section structs + Parse)
    - pkg/profile/types_test.go (4 parse/struct tests)
    - pkg/profile/schema.go (embedded schema + compiled singleton)
    - pkg/profile/validate.go (ValidateSchema, ValidateSemantic, Validate, ValidationError)
    - pkg/profile/validate_test.go (7 validation tests)
    - pkg/profile/schemas/sandbox_profile.schema.json (embedded copy for go:embed)
    - schemas/sandbox_profile.schema.json (root canonical schema)
    - testdata/profiles/valid-minimal.yaml
    - testdata/profiles/invalid-missing-spec.yaml
    - testdata/profiles/invalid-unknown-field.yaml
    - testdata/profiles/invalid-bad-substrate.yaml
  modified: []

key-decisions:
  - "go:embed requires schema inside package directory tree — schema is kept at schemas/ root for tooling/documentation and copied to pkg/profile/schemas/ for embedding"
  - "ValidateSchema uses YAML->JSON->jsonschema pipeline to leverage strict JSON Schema Draft 2020-12 with additionalProperties: false at every level"
  - "Semantic validation only adds substrate and TTL<idleTimeout checks — additional logical rules can be added incrementally as spec evolves"
  - "ValidationError.Error() produces 'path: message' format (e.g. spec.runtime.substrate: substrate docker is not supported)"

patterns-established:
  - "TDD pattern: write failing tests, commit as RED, implement, verify GREEN, commit"
  - "JSON path notation for errors: spec.runtime.substrate, spec.lifecycle.ttl"
  - "Kubernetes-style apiVersion/kind/metadata/spec structure at klankermaker.ai/v1alpha1"
  - "All 10 spec sections required in every profile — no implicit defaults"

requirements-completed: [SCHM-01, SCHM-02, SCHM-03]

# Metrics
duration: 5min
completed: 2026-03-21
---

# Phase 1 Plan 01: Go Scaffold + SandboxProfile Types + Validation Summary

**SandboxProfile Go types with all 10 spec sections, JSON Schema Draft 2020-12 strict validation, and semantic checks — the root data model for all subsequent plans**

## Performance

- **Duration:** ~5 minutes
- **Started:** 2026-03-21T20:57:16Z
- **Completed:** 2026-03-21T21:02:16Z
- **Tasks:** 2 of 2
- **Files modified:** 14 created

## Accomplishments

- SandboxProfile Go struct hierarchy with all 10 spec sections (lifecycle, runtime, execution, sourceAccess, network, identity, sidecars, observability, policy, agent) with goccy/go-yaml tags
- JSON Schema Draft 2020-12 document with additionalProperties: false at every object level, substrate enum [ec2, ecs], teardownPolicy enum [destroy, stop, retain]
- Schema + semantic validation pipeline producing JSON-path-formatted errors ("spec.runtime.substrate: substrate docker is not supported")
- Semantic checks: TTL shorter than idleTimeout, invalid substrate values, spot on ECS
- 11 tests passing in < 1 second, go vet clean

## Task Commits

Each task was committed atomically:

1. **Task 1: Go scaffold + SandboxProfile types + test fixtures** - `5e0cdfe` (feat)
2. **Task 2: JSON Schema + schema validation + semantic validation** - `aafa942` (feat)

_Note: TDD tasks had test-then-implementation flow within each commit_

## Files Created/Modified

- `go.mod` — Module definition: github.com/whereiskurt/klankrmkr
- `cmd/km/main.go` — Placeholder CLI entry point (CLI wired in Plan 04)
- `pkg/profile/types.go` — SandboxProfile + 10 section structs + Parse()
- `pkg/profile/types_test.go` — 4 struct parsing tests
- `pkg/profile/schema.go` — Embedded schema singleton (sync.Once compiled)
- `pkg/profile/validate.go` — ValidateSchema, ValidateSemantic, Validate, ValidationError
- `pkg/profile/validate_test.go` — 7 validation tests
- `pkg/profile/schemas/sandbox_profile.schema.json` — Embedded schema copy
- `schemas/sandbox_profile.schema.json` — Root canonical schema for tooling
- `testdata/profiles/valid-minimal.yaml` — Valid complete profile fixture
- `testdata/profiles/invalid-missing-spec.yaml` — Missing spec section fixture
- `testdata/profiles/invalid-unknown-field.yaml` — Typo field "lifecylce" fixture
- `testdata/profiles/invalid-bad-substrate.yaml` — substrate: docker fixture

## Decisions Made

- Schema is stored in two places: `schemas/` root (canonical, for tooling/documentation) and `pkg/profile/schemas/` (embedded copy for `go:embed`). This is because Go's `go:embed` directive cannot reference paths outside the package directory tree.
- Semantic validation is kept minimal in v1 (substrate, TTL < idleTimeout, spot on ECS). Additional checks can be added incrementally as the spec matures.
- `ValidateSchema` converts YAML to JSON before passing to the jsonschema library, which expects JSON-typed values. This is the correct pipeline for YAML-first documents.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] go:embed cannot reference paths with `../` traversal**
- **Found during:** Task 2 (schema embedding)
- **Issue:** Plan specified `//go:embed ../../schemas/sandbox_profile.schema.json` in `pkg/profile/schema.go`. Go's embed directive prohibits `../` path traversal — only paths within or below the package directory are allowed.
- **Fix:** Copied schema to `pkg/profile/schemas/sandbox_profile.schema.json` as the embedded source. The canonical `schemas/sandbox_profile.schema.json` at the repo root remains for external tooling.
- **Files modified:** `pkg/profile/schema.go` (embed path), `pkg/profile/schemas/sandbox_profile.schema.json` (new embedded copy)
- **Verification:** `go build ./...` succeeded; schema embedded and compiled correctly
- **Committed in:** `aafa942` (Task 2 commit)

**2. [Rule 3 - Blocking] jsonschema/v6 AddResource requires parsed JSON value, not []byte**
- **Found during:** Task 2 (schema compilation)
- **Issue:** Initial implementation passed raw `[]byte` to `c.AddResource()`. The v6 API requires the `doc` parameter to be a parsed JSON value (not raw bytes), causing a panic at compile time.
- **Fix:** Used `jsonschema.UnmarshalJSON(bytes.NewReader(sandboxProfileSchemaJSON))` to parse the embedded bytes before passing to `AddResource`.
- **Files modified:** `pkg/profile/schema.go`
- **Verification:** Schema compiled successfully; `TestValidateSchemaValid` passes
- **Committed in:** `aafa942` (Task 2 commit)

---

**Total deviations:** 2 auto-fixed (both Rule 3 - blocking issues)
**Impact on plan:** Both required for correct compilation. No scope creep. Schema structure and validation behavior unchanged from plan intent.

## Issues Encountered

None beyond the auto-fixed deviations documented above.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- SandboxProfile types and Validate() function are ready for Plan 02 (profile compiler)
- JSON Schema is ready for Plan 03 (built-in profiles — will validate against it)
- Validate() function signature is stable for Plan 04 (km validate CLI)
- No blockers for subsequent plans in Phase 1

---
*Phase: 01-schema-compiler-aws-foundation*
*Completed: 2026-03-21*
