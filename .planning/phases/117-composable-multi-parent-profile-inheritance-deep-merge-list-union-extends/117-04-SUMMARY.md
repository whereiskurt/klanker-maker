---
phase: 117-composable-multi-parent-profile-inheritance-deep-merge-list-union-extends
plan: 04
subsystem: profile
tags: [extends, inheritance, base-fragments, abstract, byte-identity, refactor, learn.v2, dc34]

# Dependency graph
requires:
  - phase: 117-03
    provides: km validate/create resolve full DAG; abstract-fragment skip; validate-all-profiles.sh skips base/
  - phase: 117-02
    provides: deepMerge + memoized DAG resolveMap (union+dedup everywhere)
  - phase: 117-01
    provides: ExtendsField union + metadata.abstract + IsAbstractFragment

provides:
  - profiles/base/ fragment library — 8 abstract (metadata.abstract:true) partial fragments for the quantified overlap
  - learn.v2.yaml + chatty/polite/codex refactored onto 6 base fragments (~80 lines dedup each)
  - dc34.yaml refactored onto the same 6 base fragments, keeping only its true deltas
  - resolveMap clears metadata.abstract from the merged leaf (clearAbstractFromMetadata) — abstract never propagates to a concrete profile
  - byte-identity gate (userdata_phase92_byte_identity_test) compiles the MERGED resolved spec via profile.Resolve()
  - agent_claude_golden_test resolves the extends DAG (Parse->Resolve) so refactored leaves synthesize settings correctly

affects:
  - Any future profile can compose from profiles/base/*.yaml via multi-parent extends
  - 117-05 (documentation of the fragment library + composition pattern)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Abstract base fragment: apiVersion v1alpha2 + metadata.abstract:true + a single narrow spec block"
    - "Multi-parent compose: leaf extends: [base/a, base/b, ...] — left->right->child fold, union+dedup"
    - "resolveMap clears metadata.abstract post-merge: a resolved leaf is always concrete"
    - "Golden/byte-identity tests resolve the DAG (profile.Resolve) instead of Parse(raw)"

key-files:
  created:
    - profiles/base/safenetwork.yaml — shared spec.network allowlist fragment
    - profiles/base/sidecars-all.yaml — shared spec.sidecars fragment
    - profiles/base/observability-learn.yaml — shared observability/learnMode fragment
    - profiles/base/budget-standard.yaml — shared budget fragment
    - profiles/base/artifacts-workspace.yaml — shared artifacts/workspace fragment
    - profiles/base/iam-us-east-1.yaml — shared spec.iam region fragment
    - profiles/base/agent-claude-all-tools.yaml — shared spec.agent.claude tool-gating fragment (library; not yet consumed by these leaves)
    - profiles/base/email-strict.yaml — shared email-policy fragment (library; not yet consumed by these leaves)
  modified:
    - profiles/learn.v2.yaml — extends [base/safenetwork, sidecars-all, observability-learn, budget-standard, artifacts-workspace, iam-us-east-1]
    - profiles/learn.v2.chatty.yaml — same 6-fragment compose, keeps chatty delta
    - profiles/learn.v2.polite.yaml — same 6-fragment compose, keeps polite delta
    - profiles/learn.v2.codex.yaml — same 6-fragment compose, keeps codex delta
    - profiles/dc34.yaml — same 6-fragment compose, keeps dc34 deltas
    - pkg/profile/inherit.go — clearAbstractFromMetadata: strip metadata.abstract from merged leaf
    - pkg/compiler/agent_claude_golden_test.go — Parse(raw) -> Resolve(leaf, searchPaths)
    - pkg/compiler/userdata_phase92_byte_identity_test.go — helper compiles MERGED spec via profile.Resolve("learn.v2", ...)

key-decisions:
  - "metadata.abstract must be cleared from the resolved leaf: deepMerge would otherwise propagate a base fragment's abstract:true onto the concrete result, breaking km create"
  - "Golden + byte-identity tests resolve the DAG, not Parse(raw): once a leaf gains extends:, the raw bytes are a partial profile; only the merged spec is the real compile input"
  - "6 of 8 fragments consumed by these leaves; agent-claude-all-tools + email-strict ship as library fragments for future/other profiles"

patterns-established:
  - "Author a base fragment as metadata.abstract:true + one narrow spec block; leaves compose several via extends: list"
  - "Byte-identity is the hard gate for any inline->fragment refactor: the frozen pre-92 golden must stay byte-identical"

requirements-completed: []

# Metrics
duration: ~40min (executor died mid-Task-2; orchestrator finished + verified)
completed: 2026-06-24
---

# Phase 117 Plan 04: Base Fragment Library + Profile Refactor Summary

**Authored the `profiles/base/` abstract fragment library and refactored `learn.v2.*` + `dc34.yaml` to compose 6 fragments via multi-parent `extends:` — ~430 lines of inline duplication removed with byte-identical compiled output.**

## Performance

- **Tasks:** 2 (auto)
- **Files:** 8 created (fragments) + 8 modified (5 profiles + 3 Go files)
- **Net:** +121 / −432 lines across the refactored profiles

## Accomplishments

- 8 abstract base fragments authored under `profiles/base/` (each `apiVersion v1alpha2`, `metadata.abstract: true`, one narrow spec block): safenetwork, sidecars-all, observability-learn, budget-standard, artifacts-workspace, iam-us-east-1, agent-claude-all-tools, email-strict
- `learn.v2.yaml` + `learn.v2.{chatty,polite,codex}` + `dc34.yaml` each refactored to `extends: [base/safenetwork, base/sidecars-all, base/observability-learn, base/budget-standard, base/artifacts-workspace, base/iam-us-east-1]`, keeping only their true per-profile deltas
- `resolveMap` clears `metadata.abstract` from the merged leaf (`clearAbstractFromMetadata`) so a base fragment's abstract flag never propagates to a concrete resolved profile
- `userdata_phase92_byte_identity_test` helper now compiles the **merged** spec via `profile.Resolve("learn.v2", ...)` — the byte-identity gate proves the refactor is output-equivalent
- `agent_claude_golden_test` switched from `profile.Parse(raw)` to `profile.Resolve(leaf, searchPaths)` so refactored leaves synthesize Claude settings from the merged spec
- **Byte-identity PRESERVED**: `userdata_phase92_byte_identity_test` green; full `pkg/compiler` + `pkg/profile` suites green; all 22 profiles validate

## Task Commits

1. **Task 1: Author profiles/base/ fragments + switch byte-identity helper to Resolve()** — `1c292310` (feat)
2. **Task 2: Refactor learn.v2.* + dc34 onto base fragments + clearAbstractFromMetadata + golden-test Resolve** — `82866d01` (feat) [committed by orchestrator after executor death]

## Decisions Made

- **Clear `metadata.abstract` from the resolved leaf**: the generic `deepMerge` would otherwise carry a base fragment's `abstract: true` into the merged result, which would make every composed leaf fail the `km create` abstract-fragment guard. `clearAbstractFromMetadata` runs in `resolveMap` right after the `extends` key is deleted.
- **Resolve, don't Parse, in golden/byte-identity tests**: once a leaf declares `extends:`, its raw bytes are a partial profile. The real compile input is the merged spec, so the test fixtures must call `profile.Resolve`.
- **6-of-8 fragments consumed here**: `agent-claude-all-tools` and `email-strict` are authored as reusable library fragments but are not extended by these particular leaves (they keep their own agent/email config).

## Deviations from Plan

### Executor death mid-Task-2 (handled by orchestrator)

- **What happened:** The Wave-4 executor committed Task 1 (`1c292310`, 09:00) then went silent — its transcript stopped updating for >40 min with no completion notification and no SUMMARY, while leaving Task 2 work uncommitted in the working tree (refactored profiles, `inherit.go` `clearAbstractFromMetadata`, golden-test `Resolve` switch).
- **Orchestrator action:** Confirmed the executor was dead (stale transcript, no new commits over two wake cycles). Inspected the uncommitted diff — found it coherent and sensible. Verified the full gate set BEFORE committing: `go build ./...`, `go vet ./...` (test compile), byte-identity + golden tests, full `pkg/profile` + `pkg/compiler` suites, and `scripts/validate-all-profiles.sh` (all 22 green). Committed Task 2 as a single atomic commit and authored this SUMMARY.
- **Impact:** None on deliverables — all plan must-haves met and verified. The only cost was wall-clock time spent confirming executor death.

**Total deviations:** 1 (process — executor death, recovered by orchestrator). No scope change.

## Issues Encountered

1. Executor died/hung mid-Task-2 without emitting a completion signal (see Deviations). Recovered by finishing + verifying the in-progress working tree.
2. `VERSION` left modified by `make build` (dev-build counter churn) — intentionally NOT committed, matching prior 117 commits.

## User Setup Required

None — pure profile/library + test change, no external services.

## Next Phase Readiness

- Plan 05 (documentation) can now document a real, shipped fragment library: `profiles/base/*.yaml` + the multi-parent compose pattern on `learn.v2.*` / `dc34.yaml`
- Byte-identity is proven, so docs can state the refactor is output-equivalent with confidence
- Future profiles compose from `profiles/base/` via `extends: [base/...]`

---
*Phase: 117-composable-multi-parent-profile-inheritance-deep-merge-list-union-extends*
*Completed: 2026-06-24*
