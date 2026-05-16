---
phase: 82-multi-instance-resource-prefix-isolation
plan: "04"
subsystem: infra
tags: [aws, ec2, ami, doctor, resource-prefix, multi-instance, tagging]

# Dependency graph
requires:
  - phase: 82-03
    provides: "KMBakeTags writes km:resource-prefix tag on newly baked AMIs"
provides:
  - "ListBakedAMIs(ctx, client, resourcePrefix) filters by km:resource-prefix tag when non-empty"
  - "checkOrphanedEC2 skips foreign-install instances (tagged km:resource-prefix!=currentPrefix)"
  - "checkOrphanedEC2 warns on pre-Phase-82 instances missing km:resource-prefix with --backfill-tags pointer"
affects:
  - 82-05-backfill-tags
  - 82-06-dynamodb-table-isolation
  - doctor-checks

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "ListBakedAMIs accepts empty string to suppress prefix filter (backward compat for --all-installs diagnostics)"
    - "checkOrphanedEC2 accepts currentPrefix param captured at closure time from cfg.GetResourcePrefix()"
    - "WARN-not-ERROR policy for pre-Phase-82 untagged resources; remediation pointer to --backfill-tags"

key-files:
  created: []
  modified:
    - pkg/aws/ec2_ami.go
    - pkg/aws/ec2_ami_test.go
    - internal/app/cmd/ami.go
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go

key-decisions:
  - "ListBakedAMIs takes explicit string param (not functional option) for simplicity — empty string means no prefix filter"
  - "checkOrphanedEC2 does NOT add prefix filter to DescribeInstances call — it reads all km:sandbox-id instances and discriminates post-fetch via tag inspection"
  - "Untagged instances (no km:resource-prefix) trigger WARN with --backfill-tags pointer, not ERROR and not silent-skip"
  - "checkStaleAMIs also updated to accept + forward resourcePrefix so it only reports this install's stale AMIs"

patterns-established:
  - "Pattern: prefix-aware doctor checks capture cfg.GetResourcePrefix() as a local variable before the closure, consistent with kmsResourcePrefix, iamResourcePrefix etc."

requirements-completed: []

# Metrics
duration: 12min
completed: 2026-05-16
---

# Phase 82 Plan 04: ListBakedAMIs + checkOrphanedEC2 prefix discrimination Summary

**`ListBakedAMIs` scoped by `km:resource-prefix` tag filter; `checkOrphanedEC2` skips foreign-install instances and warns on pre-Phase-82 untagged instances with `--backfill-tags` remediation pointer**

## Performance

- **Duration:** 12 min
- **Started:** 2026-05-16T12:58:30Z
- **Completed:** 2026-05-16T13:10:00Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments

- `ListBakedAMIs(ctx, client, resourcePrefix string)` — new third parameter; when non-empty, a third `tag:km:resource-prefix=<prefix>` filter is appended to the EC2 `DescribeImages` call so each install only sees its own baked AMIs
- All callers updated: `ami.go` passes `cfg.GetResourcePrefix()`, `doctor.go`'s `checkStaleAMIs` accepts and forwards the prefix parameter
- `checkOrphanedEC2` augmented with `currentPrefix string` parameter: instances tagged with a foreign `km:resource-prefix` value are silently skipped; instances missing the tag entirely produce a WARN with `--backfill-tags` remediation
- Four new tests: `TestListBakedAMIs_PrefixFilter` (2 sub-tests), `TestCheckOrphanedEC2_SkipsForeignPrefix`, `TestCheckOrphanedEC2_WarnsUntagged`

## Task Commits

Each task was committed atomically:

1. **Task 1: Add prefix filter to ListBakedAMIs + unit test** - `a89f80c` (feat)
2. **Task 2: checkOrphanedEC2 prefix discrimination** - `a9c7a5d` (feat — implemented as part of Plan 82-05 wave execution)

**Plan metadata:** see final commit in this plan

_Note: Task 2 implementation was incorporated into commit `a9c7a5d` during wave-2 execution. Both tasks are fully implemented and verified._

## Files Created/Modified

- `pkg/aws/ec2_ami.go` — `ListBakedAMIs` signature changed to accept `resourcePrefix string`; prefix filter added to `DescribeImages` when non-empty
- `pkg/aws/ec2_ami_test.go` — `TestListBakedAMIs_PrefixFilter` with two sub-tests (prefix set → 3 filters; empty prefix → 2 filters); existing tests updated to pass `""`
- `internal/app/cmd/ami.go` — `ListBakedAMIs` caller passes `cfg.GetResourcePrefix()`
- `internal/app/cmd/doctor.go` — `checkStaleAMIs` gains `resourcePrefix string` param and forwards it to `ListBakedAMIs`; `checkOrphanedEC2` gains `currentPrefix string` param with skip-foreign + warn-untagged logic
- `internal/app/cmd/doctor_test.go` — `mockEC2DescribeInstances`, `makeEC2Instance` helper, `TestCheckOrphanedEC2_SkipsForeignPrefix`, `TestCheckOrphanedEC2_WarnsUntagged`

## Decisions Made

- `ListBakedAMIs` takes an explicit string parameter rather than a functional option — simpler for callers, and empty string cleanly means "no filter" for the all-installs diagnostic path
- `checkOrphanedEC2` does NOT add a prefix filter to the `DescribeInstances` API call — it reads all `km:sandbox-id`-tagged instances and discriminates post-fetch. This ensures the function can produce the "untagged instances WARN" (pre-Phase-82 resources without `km:resource-prefix`) which an API-level filter would silently hide
- WARN-not-ERROR for untagged instances: per CONTEXT.md Decision §Tag-based discrimination — pre-Phase-82 resources are not this install's fault; remediation is Plan 05's `--backfill-tags`
- `checkStaleAMIs` signature also extended with `resourcePrefix` — stale AMI detection should only report this install's AMIs just like baked AMI listing

## Deviations from Plan

None — plan executed exactly as written. Note: Task 2 changes were incorporated in the 82-05 commit during wave-2 execution; the implementation is identical to what the plan specified.

## Issues Encountered

None. Pre-existing unrelated test failures exist in the `internal/app/cmd` package (`TestProbeCodexPort_Primary`, `TestAtList_WithRecords`, etc.) and are not caused by this plan's changes — confirmed by reproducing the failures on the pre-Task-2 state.

## User Setup Required

None — no external service configuration required. All changes are compile-time API changes in Go; `make build` produces the updated binary.

## Next Phase Readiness

- Plan 82-05 (`km doctor --backfill-tags`) is ready — it consumes the `untaggedCount` WARN from this plan as the motivating symptom
- Plan 82-06 (DynamoDB table isolation) proceeds independently
- `ListBakedAMIs` callers outside the main codebase that passed 2 args will fail to compile — this is intentional (forces explicit prefix awareness at every call site)

---
*Phase: 82-multi-instance-resource-prefix-isolation*
*Completed: 2026-05-16*
