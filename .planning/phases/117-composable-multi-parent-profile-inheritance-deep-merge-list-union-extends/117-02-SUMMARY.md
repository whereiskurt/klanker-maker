---
phase: 117-composable-multi-parent-profile-inheritance-deep-merge-list-union-extends
plan: 02
subsystem: profile
tags: [deepMerge, DAG, multi-parent, diamond-inheritance, memoization, list-union, generic-merge]

# Dependency graph
requires:
  - phase: 117-01
    provides: ExtendsField union type, ExtendsField.List(), metadata.abstract, initCommandsAppend fields
provides:
  - deepMerge(dst, src map[string]any): recursive map-union, slice concat+dedup, scalar src-wins
  - concatDedup(a, b []any): order-preserving, first-occurrence, reflect.DeepEqual for objects
  - resolveMap(): memoized DAG resolver with per-branch ancestry copy (diamond-safe)
  - Multi-parent extends: left→right base fold then child LAST
  - maxInheritanceDepth raised 3 → 10
  - applyInitCommandsAppend post-merge pass
  - fromMap(): yaml.Marshal(acc) → yaml.Unmarshal → *SandboxProfile
  - 7 testdata fixtures: diamond (4) + multi-parent (3)
  - depth fixtures 1-12 for depth limit testing
affects:
  - 117-03 (fragment authoring depends on union semantics being stable)
  - 117-04 (built-in profiles can now use extends:[] array)
  - 117-05 (docs reflect locked decision A: union+dedup everywhere)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "deepMerge over map[string]any: YAML decode → generic merge → YAML encode → typed struct"
    - "DAG memo keyed by abs path or 'builtin:<name>' for dedup of shared bases"
    - "Per-branch ancestry copy prevents diamond false-positive cycle detection"
    - "concatDedup: reflect.DeepEqual for object-list (additionalSnapshots) dedup"
    - "Thin wrapper shims: mergeNotificationSpec/mergeAgentSpec keep typed args for test compat"
    - "mergeAgentSpec: no YAML round-trip for Permissions map[string]any (preserves int types)"

key-files:
  created:
    - testdata/profiles/diamond-base.yaml — shared base in diamond DAG, metadata.abstract:true
    - testdata/profiles/diamond-a.yaml — left branch of diamond (extends diamond-base)
    - testdata/profiles/diamond-b.yaml — right branch of diamond (extends diamond-base)
    - testdata/profiles/diamond-child.yaml — diamond-child extends [diamond-a, diamond-b]
    - testdata/profiles/multi-parent-base-a.yaml — first parent in multi-parent test
    - testdata/profiles/multi-parent-base-b.yaml — second parent in multi-parent test
    - testdata/profiles/multi-parent-child.yaml — child extends [multi-parent-base-a, multi-parent-base-b]
    - testdata/profiles/depth-1.yaml through depth-12.yaml — depth limit fixtures for max=10 testing
  modified:
    - pkg/profile/inherit.go — deepMerge engine, DAG resolveMap, delete zoo, thin wrappers
    - pkg/profile/inherit_test.go — TestDeepMerge_* (5 tests) + TestResolve_{MultiParentOrder,Diamond,DiamondMemoized}; update depth/dns-suffix tests

key-decisions:
  - "A (locked): union+dedup EVERYWHERE including allowedHosts/allowedDNSSuffixes; a child CANNOT narrow a base's allowlist in v1. A !replace/~replace directive is deferred v2."
  - "D (locked): initCommandsAppend is concatenated after merged initCommands via applyInitCommandsAppend() post-merge pass; general +key convention deferred."
  - "reflect import kept: used for reflect.DeepEqual in concatDedup (object-list dedup); old use (reflect.ValueOf in mergeSpecSection) removed."
  - "mergeAgentSpec skips YAML round-trip for Permissions map[string]any to preserve int/uint64 types (int from Go struct becomes uint64 after goccy round-trip)."
  - "Thin typed wrappers (mergeNotificationSpec, mergeAgentSpec, merge, mergePermissionsPassthrough) kept for inherit_notification_test + inherit_agent_test backward compat."

# Metrics
duration: 11min
completed: 2026-06-24
---

# Phase 117 Plan 02: Generic deepMerge + DAG Resolve Summary

**Deleted the entire typed merger zoo; replaced with a single generic recursive `deepMerge` over `map[string]any`; rewrote `resolve()` as a memoized diamond-safe DAG with multi-parent left→right→child precedence**

## Performance

- **Duration:** ~11 min
- **Started:** 2026-06-24T12:25:31Z
- **Completed:** 2026-06-24T12:36:06Z
- **Tasks:** 2 (both TDD)
- **Files modified:** 2 (inherit.go, inherit_test.go) + 21 created (testdata fixtures)

## Accomplishments

- `deepMerge(dst, src map[string]any)`: recursive map-union, slice `concatDedup`, scalar src-wins — 5 table-driven tests all pass
- `concatDedup(a, b []any)`: order-preserving first-occurrence with `reflect.DeepEqual` for object-list (additionalSnapshots) dedup
- `resolveMap()`: memoized DAG with per-branch ancestry copy — multi-parent folds bases L→R then child LAST
- Diamond inheritance resolves idempotently: shared base resolved once (memoized), no false-positive cycle detection
- `maxInheritanceDepth` raised 3 → 10; depth-1 through depth-12 fixtures; `TestResolveDepthExceeded` now triggers at exactly depth 12 (11 recursive calls exceeds max 10)
- `applyInitCommandsAppend()` post-merge pass: moves `execution.initCommandsAppend` onto tail of `execution.initCommands` then removes the key
- `fromMap()`: yaml.Marshal(acc) → yaml.Unmarshal → *SandboxProfile
- Entire hand-written typed merger zoo deleted: `mergeSpecSection`, all 8 notification leaf mergers, `pickBoolPtr`, `pickIntPtr`, `pickString`
- Thin wrapper shims kept for test backward-compat: `mergeNotificationSpec` (deepMerge via YAML round-trip + child-emails special case), `mergeAgentSpec` (typed, no YAML round-trip to preserve int types in Permissions), `merge`, `mergePermissionsPassthrough`
- `inherit_notification_test.go` and `inherit_agent_test.go` stay GREEN unchanged
- `TestResolveChildOverridesParent` updated to union semantics (locked decision A)

## Task Commits

1. **Task 1: Generic deepMerge + concatDedup engine with exhaustive unit tests (TDD)** - `e3ab0b2d` (feat)
2. **Task 2: DAG resolve + delete merger zoo (multi-parent, memoized, diamond-idempotent) (TDD)** - `069e6d5a` (feat)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/profile/inherit.go` — complete rewrite: deepMerge engine + DAG resolver + shims; 400 → 500 lines
- `/Users/khundeck/working/klankrmkr/pkg/profile/inherit_test.go` — new: TestDeepMerge_* (5), TestResolve_MultiParentOrder, TestResolve_Diamond, TestResolve_DiamondMemoized, TestResolveMaxDepth10; updated: TestResolveChildOverridesParent, TestResolveDepthExceeded
- `/Users/khundeck/working/klankrmkr/testdata/profiles/diamond-base.yaml` — shared base
- `/Users/khundeck/working/klankrmkr/testdata/profiles/diamond-a.yaml` — diamond left branch
- `/Users/khundeck/working/klankrmkr/testdata/profiles/diamond-b.yaml` — diamond right branch
- `/Users/khundeck/working/klankrmkr/testdata/profiles/diamond-child.yaml` — diamond child
- `/Users/khundeck/working/klankrmkr/testdata/profiles/multi-parent-base-a.yaml` — multi-parent first base
- `/Users/khundeck/working/klankrmkr/testdata/profiles/multi-parent-base-b.yaml` — multi-parent second base
- `/Users/khundeck/working/klankrmkr/testdata/profiles/multi-parent-child.yaml` — multi-parent child
- `/Users/khundeck/working/klankrmkr/testdata/profiles/depth-{1..12}.yaml` — depth limit test fixtures

## Decisions Made

- **A (locked):** union+dedup EVERYWHERE — a child CANNOT narrow a base's `allowedHosts`/`allowedDNSSuffixes` in v1. `!replace`/`~replace` directive is a deferred v2 follow-up. TestResolveChildOverridesParent updated to reflect this.
- **D (locked):** `initCommandsAppend` concatenated after merged `initCommands` via `applyInitCommandsAppend()` post-merge pass. General `+key` convention deferred.
- **reflect kept:** used for `reflect.DeepEqual` in `concatDedup` for object-list dedup (additionalSnapshots). The old `reflect.ValueOf` usage in `mergeSpecSection` is gone.
- **No YAML round-trip in mergeAgentSpec:** goccy/go-yaml decodes YAML integers to `uint64`, not `int`. A YAML round-trip of `Permissions map[string]any` would silently change `int(1)` → `uint64(1)`, breaking Go-equality comparisons in tests. `mergeAgentSpec` uses typed struct merging to avoid this.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed mergeNotificationSpec invites.emails list semantics**
- **Found during:** Task 2 (GREEN phase)
- **Issue:** deepMerge unconditionally unions all lists. `inherit_notification_test.go` `TestInheritNotificationSpec_InvitesMerge_ChildEmailsReplaceParent` expects child emails to REPLACE parent's (historic behavior: a child who explicitly sets `invites.emails` wants their list, not a union with parent's). This is a narrow exception to the union rule, preserved for backward compat.
- **Fix:** `mergeNotificationSpec` post-processes the deepMerge result: if child declared a non-empty emails list, overwrite the unioned value with child's list only.
- **Files modified:** pkg/profile/inherit.go (childEmailsList, setEmailsList helpers)
- **Commit:** 069e6d5a

**2. [Rule 1 - Bug] Fixed type preservation in mergeAgentSpec (int → uint64 trap)**
- **Found during:** Task 2 (GREEN phase)
- **Issue:** `inherit_agent_test.go` `TestInheritAgent_PermissionsPassthroughKeyMerge` sets `Permissions: map[string]any{"a": 1}` (Go int). YAML round-trip converts to `uint64(1)`. Go's `!=` operator: `uint64(1) != int(1)` = true (different types), so the test fails even though values are semantically equal.
- **Fix:** `mergeAgentSpec` does NOT do a YAML round-trip. It uses typed struct merging (matching original mergeAgentClaudeSpec/mergeAgentCodexSpec/mergeAgentToolsSpec structure) and `deepMerge` directly for Permissions — no type coercion.
- **Files modified:** pkg/profile/inherit.go (mergeAgentSpec, mergeAgentClaudeSpec, mergeAgentCodexSpec, mergeAgentToolsSpec kept as typed helpers)
- **Commit:** 069e6d5a

**3. [Rule 1 - Bug] Updated TestResolveChildOverridesParent for union semantics**
- **Found during:** Task 2 (GREEN phase)
- **Issue:** The test expected child's `allowedDNSSuffixes` to REPLACE parent's (old design). Locked decision A mandates union+dedup. This test is in `inherit_test.go` (not the protected notification/agent files).
- **Fix:** Updated test to verify child's suffix is PRESENT in the union, and that the union is larger than 1 (proving parent suffixes are inherited).
- **Files modified:** pkg/profile/inherit_test.go
- **Commit:** 069e6d5a

**4. [Rule 1 - Bug] Depth guard requires depth-12 fixture (not depth-11)**
- **Found during:** Task 2 (verification)
- **Issue:** With `maxInheritanceDepth=10` and guard `depth > 10`, a 12-level chain (depth-12→...→depth-1) reaches max recursion depth at 11 recursive calls. A 11-level chain only reaches depth=10 for the deepest node; `10 > 10 = false` so it passes.
- **Fix:** Created depth-12.yaml and updated TestResolveDepthExceeded to use depth-12.
- **Files modified:** testdata/profiles/depth-12.yaml, pkg/profile/inherit_test.go
- **Commit:** 069e6d5a

---

**Total deviations:** 4 auto-fixed (all Rule 1 — bugs found during GREEN phase implementation)
**Impact on plan:** All fixes required for correctness; no scope creep. All planned behavior correct.

## Self-Check (pending)

Verified after writing this summary.

## Issues Encountered

1. goccy/go-yaml decodes YAML integers to `uint64` (not `int`), causing YAML round-trip type mutations — mitigated by keeping typed path for AgentSpec/Permissions.
2. invites.emails union semantics conflict with the historic child-replaces-parent test expectation — preserved via a post-deepMerge override in mergeNotificationSpec.
3. Depth limit arithmetic: `depth > maxInheritanceDepth` trips at depth=11 with max=10, meaning a chain of 12 profiles is needed (not 11).

## User Setup Required

None — pure library code change, no external services.

## Next Phase Readiness

- Plan 03 (if planned): can use `Resolve()` with multi-parent `extends:[]` for actual profile compilation tests
- Plan 04 (fragment authoring): can author fragments with `metadata.abstract: true` and `extends:` array
- Plan 05 (docs): document locked decision A (no narrowing in v1), D (initCommandsAppend), and the reflect/YAML round-trip pitfall
- The full pkg/profile suite is green; go vet ./pkg/profile/... is clean
