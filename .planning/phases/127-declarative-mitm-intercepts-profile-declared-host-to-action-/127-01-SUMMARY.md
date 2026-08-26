---
phase: 127-declarative-mitm-intercepts-profile-declared-host-to-action-
plan: 01
subsystem: profile-schema
tags: [go, yaml, json-schema, profile-inheritance, mitm]

requires: []
provides:
  - "spec.network.mitm.intercepts profile surface (types + schema)"
  - "CollapseIntercepts by-name last-wins collapse wired into Resolve()"
  - "Three inheritance fixtures proving disable + whole-entry-replace survive Phase 117's list union"
affects: [127-02, 127-03, 127-04]

tech-stack:
  added: []
  patterns:
    - "Post-merge normaliser wired into fromMap (pkg/profile/inherit.go), mirroring the existing applyInitCommandsAppend/clearAbstractFromMetadata pattern"
    - "*bool field for a serialisable tri-state (nil=default-true, pointer-false=explicit override) alongside omitempty, so a disable-only override never emits stray keys"

key-files:
  created:
    - pkg/profile/mitm.go
    - pkg/profile/mitm_test.go
    - pkg/profile/mitm_schema_test.go
    - testdata/profiles/base/mitm-fixture-base.yaml
    - testdata/profiles/mitm-fixture-disable.yaml
    - testdata/profiles/mitm-fixture-replace.yaml
  modified:
    - pkg/profile/types.go
    - pkg/profile/inherit.go
    - pkg/profile/schemas/sandbox_profile.schema.json

key-decisions:
  - "CollapseIntercepts wired into fromMap (not resolveMap) — the single point every Resolve() output passes through, matching the plan's stated integration point"
  - "Whole-entry replacement (not field-level merge) for by-name override, so a leaf's narrower hosts list actually wins instead of being unioned with the base's"
  - "JSON schema requires only name on an intercept item; hosts/action left unenforced by schema since a disable-only override legitimately omits both (plan 04 owns the semantic 'enabled rule must have hosts+action' check)"

patterns-established:
  - "MITM intercept fixtures live under testdata/profiles/{base/mitm-fixture-base.yaml, mitm-fixture-disable.yaml, mitm-fixture-replace.yaml} as the canonical disable/replace override pair for future MITM-related tests"

requirements-completed: [REQ-127-SCHEMA, REQ-127-OVERRIDE]

coverage:
  - id: D1
    description: "A profile can declare spec.network.mitm.intercepts[] with name/enabled/hosts/action and the JSON schema accepts it (including a name+enabled-only disable override), rejecting unknown keys and any 'block' action key"
    requirement: "REQ-127-SCHEMA"
    verification:
      - kind: unit
        ref: "pkg/profile/mitm_schema_test.go#TestValidateSchema_MITM"
        status: pass
      - kind: unit
        ref: "pkg/profile/mitm_test.go#TestMITM_RoundTrip_RedirectAndRespond"
        status: pass
      - kind: unit
        ref: "pkg/profile/mitm_test.go#TestMITM_RoundTrip_DisableOnlyOmitsHostsAndAction"
        status: pass
      - kind: unit
        ref: "pkg/profile/mitm_test.go#TestMITM_RoundTrip_NoMITMBlockOmitsKey"
        status: pass
    human_judgment: false
  - id: D2
    description: "A leaf that extends a fragment and restates an intercept by name overrides the inherited rule wholesale (last-wins, whole-entry), instead of unioning with it — proven both at the unit level and through a real Resolve() over extends: fixtures"
    requirement: "REQ-127-OVERRIDE"
    verification:
      - kind: unit
        ref: "pkg/profile/mitm_test.go#TestCollapseIntercepts_LastWinsWholeEntry"
        status: pass
      - kind: unit
        ref: "pkg/profile/mitm_test.go#TestCollapseIntercepts_OrderIsFirstAppearanceIndexLastAppearanceBody"
        status: pass
      - kind: integration
        ref: "pkg/profile/mitm_test.go#TestResolve_MITMFixtureReplace"
        status: pass
      - kind: integration
        ref: "pkg/profile/mitm_test.go#TestResolve_MITMFixtureBase_NeverDuplicated"
        status: pass
    human_judgment: false
  - id: D3
    description: "A leaf carrying only name + enabled:false resolves to a disabled rule through a real extends: chain, and EnabledIntercepts on the resolved profile is empty"
    requirement: "REQ-127-OVERRIDE"
    verification:
      - kind: integration
        ref: "pkg/profile/mitm_test.go#TestResolve_MITMFixtureDisable"
        status: pass
      - kind: unit
        ref: "pkg/profile/mitm_test.go#TestEnabledIntercepts_DisabledDropped"
        status: pass
    human_judgment: false
  - id: D4
    description: "A profile that declares no mitm block yields a nil NetworkMITMSpec and zero intercepts from every accessor (ProfileIntercepts, EnabledIntercepts) without panicking"
    verification:
      - kind: unit
        ref: "pkg/profile/mitm_test.go#TestProfileIntercepts_NilSafety"
        status: pass
      - kind: unit
        ref: "pkg/profile/mitm_test.go#TestMITM_RoundTrip_NoMITMBlockOmitsKey"
        status: pass
    human_judgment: false

duration: 2min
completed: 2026-08-26
status: complete
---

# Phase 127 Plan 01: Declarative MITM intercepts — profile surface + by-name collapse Summary

**Added `spec.network.mitm.intercepts[]` to the Go profile types and JSON schema, plus the by-name last-wins collapse (`CollapseIntercepts`) wired into `Resolve()` so `enabled: false` on a leaf can actually switch off a rule inherited through `extends:`.**

## Performance

- **Started:** 2026-08-26T01:22:00-04:00 (approx, first read)
- **Completed:** 2026-08-26T01:25:12-04:00
- **Duration:** ~2 min of tool time (plan execution, excluding file-read overhead)
- **Tasks:** 3
- **Files modified:** 6 (3 created new: mitm.go, mitm_test.go, 3 fixtures; 3 modified: types.go, inherit.go, schema.json; 1 extra test file added beyond plan spec: mitm_schema_test.go)

## Accomplishments

- Four new Go types (`NetworkMITMSpec`, `MITMIntercept`, `MITMAction`, `MITMRespond`) plus `NetworkSpec.MITM *NetworkMITMSpec`, all pointer+omitempty per the locked design so a disable-only override never emits a stray `hosts:` or `action:` key.
- `spec.network.mitm` JSON-schema subtree: `additionalProperties: false` throughout, `required: ["name"]` only on an intercept item (hosts/action are semantic requirements deferred to plan 04), no `block` action key anywhere, no `apiVersion` bump.
- `pkg/profile/mitm.go`: `InterceptEnabled`, `CollapseIntercepts` (by-name last-wins, whole-entry, first-appearance-index/last-appearance-body ordering), `EnabledIntercepts`, `DuplicateInterceptNames`, `ProfileIntercepts` — all nil-safe.
- `CollapseIntercepts` wired into `fromMap` (`pkg/profile/inherit.go`), the single point every `Resolve()` output passes through, guaranteeing the merged bytes `km validate` inspects can never contain a duplicate intercept name.
- Three inheritance fixtures (`testdata/profiles/base/mitm-fixture-base.yaml`, `mitm-fixture-disable.yaml`, `mitm-fixture-replace.yaml`) plus resolve-level tests proving both the disable-through-extends path and the whole-entry-replace path survive Phase 117's list-union `deepMerge`.

## Task Commits

1. **Task 1: Add the four MITM types, the NetworkSpec.MITM field, and the JSON-schema subtree** - `82e258c1` (feat)
2. **Task 2: Implement by-name collapse plus the enabled/duplicate accessors in pkg/profile/mitm.go** - `3e3e4316` (feat)
3. **Task 3: Add inheritance fixtures and a resolve-level override test** - `4d9d450a` (test)

**Plan metadata:** (this commit, docs)

## Files Created/Modified

- `pkg/profile/types.go` - `NetworkSpec.MITM` field + `NetworkMITMSpec`/`MITMIntercept`/`MITMAction`/`MITMRespond` structs, all pointer+omitempty
- `pkg/profile/schemas/sandbox_profile.schema.json` - `spec.network.mitm.intercepts` subtree, `additionalProperties: false`, `required: ["name"]` only
- `pkg/profile/mitm.go` - the five collapse/accessor functions
- `pkg/profile/inherit.go` - `CollapseIntercepts` wired into `fromMap` with an explanatory comment
- `pkg/profile/mitm_test.go` - round-trip, collapse, accessor, and resolve-level fixture tests
- `pkg/profile/mitm_schema_test.go` - JSON-schema acceptance/rejection tests (name+enabled-only validates, unknown keys rejected, no block action)
- `testdata/profiles/base/mitm-fixture-base.yaml` - abstract fragment with one `fixture-egg` redirect intercept
- `testdata/profiles/mitm-fixture-disable.yaml` - leaf that disables `fixture-egg` via `enabled: false`
- `testdata/profiles/mitm-fixture-replace.yaml` - leaf that restates `fixture-egg` in full with a different host list and a `respond` action

## Decisions Made

- Wired `CollapseIntercepts` into `fromMap` (called at every `Resolve()` exit point and both legacy typed-merge shim exits), not into `resolveMap`'s per-branch recursion — this matches the plan's explicit instruction and avoids collapsing intermediate, not-yet-fully-merged branch state.
- Added `pkg/profile/mitm_schema_test.go` (not explicitly named as an artifact in the plan, but required by Task 1's `<behavior>` block to prove the schema-level assertions) in package `profile_test` alongside the existing `TestValidateSchema_*` convention, reusing `minimalProfileWithNetworkYAML`.
- Fixture leaves (`mitm-fixture-disable.yaml`, `mitm-fixture-replace.yaml`) carry only `metadata.name` + `extends` + `spec.lifecycle.ttl` + `spec.network.mitm` — matching the minimal-skeleton style of `diamond-a.yaml`/`diamond-b.yaml`, since `Resolve()` (unlike `km validate`) does not require full-spec completeness.

## Deviations from Plan

### Auto-fixed Issues

None — no bugs, missing functionality, or blocking issues were encountered that required Rule 1/2/3 fixes.

**Note (not a deviation, a plan-acceptance-criteria imprecision):** Task 1's acceptance criteria states `grep -c '"block"' pkg/profile/schemas/sandbox_profile.schema.json` should return `0`. The schema file already contains an unrelated pre-existing `"block"` string at the `spec.limits.onBreach` enum (`["warn", "block", "freeze"]`, from Phase 121's action-quota feature), so the literal grep returns `1` regardless of this plan's changes. Verified manually that the *new* `spec.network.mitm.action` schema subtree contains no `block` key (only `redirect`/`respond`), which is the actual intent of the criterion. No action taken — this is a pre-existing string outside this plan's scope, not something to fix.

---

**Total deviations:** 0 auto-fixed
**Impact on plan:** None. Plan executed exactly as written; the one acceptance-criteria note above is documented for the next reader's benefit, not a code change.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required. This plan is code-only (Go types, JSON schema, test fixtures); no `km init`, `km create`, or AWS-touching command was run, per the plan's constraints.

## Next Phase Readiness

- `pkg/profile/mitm.go` and the new types are ready for plan 03 (compiler drop-in, `EnabledIntercepts` is the accessor it should call) and plan 04 (validation, `DuplicateInterceptNames` and `ProfileIntercepts` are the accessors it should call for the semantic "hosts/action required when enabled" checks and the same-file-duplicate error).
- No blockers. `spec.network.mitm` is fully additive and dormant by default (a profile with no `mitm:` block resolves to a nil `NetworkSpec.MITM`, verified by `TestMITM_RoundTrip_NoMITMBlockOmitsKey` and `TestProfileIntercepts_NilSafety`).
- `pkg/profile/validate.go` was deliberately NOT touched in this plan (owned by plan 04) — a profile declaring `mitm:` today passes `km validate`'s JSON-schema check but has no semantic validation yet (empty hosts, zero/two actions, bad redirect URL, etc. are not yet errors). This is expected and by design per the plan's prohibitions.

---
*Phase: 127-declarative-mitm-intercepts-profile-declared-host-to-action-*
*Completed: 2026-08-26*
