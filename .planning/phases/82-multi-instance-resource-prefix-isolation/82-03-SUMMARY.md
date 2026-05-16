---
phase: 82-multi-instance-resource-prefix-isolation
plan: 03
subsystem: infra
tags: [ec2, ami, tagging, multi-instance, resource-prefix]

requires:
  - phase: 82-01
    provides: KM_RESOURCE_PREFIX config plumbing and cfg.GetResourcePrefix()
  - phase: 82-02
    provides: AMIName prefix threading pattern established

provides:
  - KMBakeTags accepts resourcePrefix string parameter and emits km:resource-prefix tag on all baked AMIs
  - Single call site in internal/app/cmd/ami.go passes cfg.GetResourcePrefix()

affects:
  - 82-04 (ListBakedAMIs tag filter — depends on this tag existing)
  - km doctor checkStaleAMIs and checkOrphanedEC2 (filter by km:resource-prefix)

tech-stack:
  added: []
  patterns:
    - "resourcePrefix threading: injected at call site via cfg.GetResourcePrefix(), not read globally"
    - "TDD: RED commit (test fails) then GREEN commit (implementation), full pkg/aws suite stays green"

key-files:
  created: []
  modified:
    - pkg/aws/ec2_ami.go
    - pkg/aws/ec2_ami_test.go
    - internal/app/cmd/ami.go

key-decisions:
  - "KMBakeTags trailing parameter: resourcePrefix added as the last positional arg to avoid breaking the existing positional call site order"
  - "Tags alphabetized in the returned slice: km:resource-prefix inserted alphabetically after km:profile and before km:sandbox-id for consistency with the existing sorted convention"

patterns-established:
  - "Every baked AMI carries km:resource-prefix=${prefix} alongside km:sandbox-id — plan 04 reads this to filter per-install"

requirements-completed: []

duration: ~8min
completed: 2026-05-16
---

# Phase 82 Plan 03: AMI Bake-Time km:resource-prefix Tag Summary

**`KMBakeTags` extended with `resourcePrefix` trailing parameter; every baked AMI now carries `km:resource-prefix=${prefix}` tag stamped atomically at CreateImage time**

## Performance

- **Duration:** ~8 min
- **Started:** 2026-05-16T12:31:01Z
- **Completed:** 2026-05-16T12:38:34Z
- **Tasks:** 1 (TDD: RED + GREEN commits)
- **Files modified:** 3

## Accomplishments

- `KMBakeTags` signature extended with trailing `resourcePrefix string`; new `km:resource-prefix` tag appended alphabetically in the returned slice
- Single non-test call site (`internal/app/cmd/ami.go:719`) updated to pass `cfg.GetResourcePrefix()` — confirmed `cfg` was already in scope (shared line with `AMIName` call)
- `TestKMBakeTags_IncludesAllRequiredKeys` updated: passes `"rg"` prefix, asserts tag key present and value matches; `TestKMBakeTags_EmptyAlias_OmitsAliasOrLeavesBlank` updated similarly
- Full `go build ./...` and `go test ./pkg/aws/...` clean after both commits

## Task Commits

Each task committed via TDD protocol:

1. **RED — failing tests** - `04fe5aa` (test)
2. **GREEN — KMBakeTags + ami.go caller** - `6a4c57a` (feat)

## Files Created/Modified

- `pkg/aws/ec2_ami.go` — `KMBakeTags` signature + new `km:resource-prefix` tag in returned slice
- `pkg/aws/ec2_ami_test.go` — `TestKMBakeTags_IncludesAllRequiredKeys` asserts new tag and value; `TestKMBakeTags_EmptyAlias_OmitsAliasOrLeavesBlank` updated to pass prefix
- `internal/app/cmd/ami.go` — bake call site passes `cfg.GetResourcePrefix()` as new last argument

## Decisions Made

- `resourcePrefix` added as the **last positional parameter** to `KMBakeTags` — minimises diff at the call site and matches the established pattern in `AMIName` (which also uses a trailing variadic prefix)
- Tags in `KMBakeTags` return slice **re-ordered alphabetically** to place `km:resource-prefix` in the correct position; all existing keys preserved, only order changed
- Call site uses `cfg.GetResourcePrefix()` directly — no wrapper needed, `cfg` is already in scope on the same line as the existing `AMIName(... cfg.GetResourcePrefix()+"-")` call

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None — the worktrees copy under `.claude/worktrees/phase-74-02/` also has the old signature but is an archived worktree, not the live codebase; it was correctly excluded from the build.

## Self-Check

- `pkg/aws/ec2_ami.go` contains `km:resource-prefix`: confirmed
- `pkg/aws/ec2_ami_test.go` asserts `km:resource-prefix` with value check: confirmed
- `internal/app/cmd/ami.go` passes `cfg.GetResourcePrefix()`: confirmed
- `go build ./...` exits 0: confirmed
- `go test ./pkg/aws/... -count=1` exits 0: confirmed
- Commits 04fe5aa and 6a4c57a present in git log: confirmed

## Self-Check: PASSED

## Next Phase Readiness

- Plan 04 (`ListBakedAMIs` / `km doctor` tag-filter) can now depend on `km:resource-prefix` being present on all newly baked AMIs
- Pre-Phase-82 AMIs baked before this change will NOT have the tag — Plan 04 should treat absence of `km:resource-prefix` as "belongs to this install" (backward-compat default per 82-CONTEXT.md decision D1)

---
*Phase: 82-multi-instance-resource-prefix-isolation*
*Completed: 2026-05-16*
