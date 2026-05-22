---
phase: 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile
plan: 02
subsystem: profile
tags: [ebs, snapshots, validation, layer1, tdd, semantic-validation]

requires:
  - "87-01 (AdditionalSnapshotSpec Go type + RED-state stubs)"

provides:
  - "validateAdditionalSnapshots helper in pkg/profile/validate.go (Layer 1 offline rules)"
  - "EC2-only substrate gate in pkg/compiler/service_hcl.go validateEC2StorageFields"
  - "11 rejection cases + 7 happy-path cases in TestValidateAdditionalSnapshots_Layer1"

affects:
  - "87-03 (SNAP-03 AWS pre-flight — Layer 2 size vs snapshot.VolumeSize check)"
  - "87-04 (SNAP-04/05 HCL rendering)"

tech-stack:
  added: []
  patterns:
    - "validateAdditionalSnapshots function-scope regexp.MustCompile — compiled once per call (acceptable; not in hot path). Future: promote to package-level var if profiling shows concern."
    - "reserved mountpoint blocklist as map[string]bool for O(1) lookup with exact-match semantics (not prefix match)"
    - "seenMountPoints/seenDevices maps for O(n) duplicate detection across array entries"
    - "Defense-in-depth: substrate gate lives in BOTH validate.go (km validate offline) AND service_hcl.go (km create compile-time)"

key-files:
  created: []
  modified:
    - pkg/profile/validate.go
    - pkg/profile/validate_test.go
    - pkg/compiler/service_hcl.go

key-decisions:
  - "Regexp compiled at function scope (not package-level var) — acceptable given validation is not a hot path; avoids package-init complexity for a single helper function"
  - "seenMountPoints only tracks non-empty mountPoint values — empty string skipped to avoid false duplicate alerts when mountPoint is accidentally omitted (schema should catch that separately)"
  - "Size == 0 is valid (inherit snapshot size) — only negative values are rejected at Layer 1; Layer 2 enforces size >= snapshot.VolumeSize after AWS DescribeSnapshots call"
  - "Reserved list uses exact string match (map[string]bool) — /opt is reserved but /opt/models is allowed; prefix-match would be too broad"
  - "EC2-only gate added to service_hcl.go immediately after existing additionalVolume check (line 681 area) — parity with existing pattern, identical wording style"

metrics:
  duration: 165s
  completed: 2026-05-22
  tasks: 2
  files_modified: 3
---

# Phase 87 Plan 02: Layer 1 AdditionalSnapshots Validation Summary

**validateAdditionalSnapshots helper in validate.go (EC2-only, regex, reserved mountpoints, collision, device uniqueness, size) + matching EC2 substrate gate in service_hcl.go for compile-time defense-in-depth**

## Performance

- **Duration:** 165s (~3 min)
- **Started:** 2026-05-22T21:36:06Z
- **Completed:** 2026-05-22T21:38:51Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments

- Replaced RED-state `TestValidateAdditionalSnapshots_Layer1` stub with a full table-driven test (11 rejection cases, 7 happy-path cases — all GREEN)
- Implemented `validateAdditionalSnapshots` package-level helper called from `ValidateSemantic`
- Rules enforced: EC2-only substrate, snapshotId regex `^snap-[0-9a-f]{8,17}$`, mountPoint absolute-path requirement, reserved-mountpoint blocklist (15 entries, exact match), `additionalVolume.mountPoint` collision, cross-entry mountPoint duplicate, explicit device duplicate, `size < 0` rejection
- Added EC2-only substrate check to `validateEC2StorageFields` in `service_hcl.go` as defense-in-depth (same wording: `"additionalSnapshots is not supported for %s substrate"`)
- Zero regressions in all existing `pkg/profile/...` tests

## validateAdditionalSnapshots Location

- **File:** `pkg/profile/validate.go`
- **Helper:** `func validateAdditionalSnapshots(p *SandboxProfile) []ValidationError`
- **Called from:** `ValidateSemantic` at the bottom of the function body (after Phase 68 Slack rules)
- **Path convention:** `spec.runtime.additionalSnapshots[i].<field>` consistently across all error paths

## Regexp Decision

Function-scope `regexp.MustCompile` — compiled each time `validateAdditionalSnapshots` is called. Acceptable because validation is not a hot path (called once per `km validate` / `km create`). This avoids a package-level `var` for a narrow single-function concern. Future readers: promote to `var snapIDRe = regexp.MustCompile(...)` at package top if profiling shows measurable allocation overhead.

## Reserved Mountpoint Blocklist

15 entries (exact-match, no prefix matching):
`/`, `/shared`, `/workspace`, `/proc`, `/sys`, `/dev`, `/etc`, `/usr`, `/var`, `/root`, `/home`, `/boot`, `/tmp`, `/run`, `/opt`

`/opt` is in the blocklist; `/opt/models` is NOT — sub-paths are fine. This matches the CONTEXT.md locked decision.

## Coverage Tally

| Category | Count |
|---|---|
| Rejection cases | 11 |
| Happy-path cases | 7 |
| Total sub-tests | 18 |

Rejection cases:
1. `bad_snapshot_id_regex_uppercase` — uppercase in snapshotId
2. `bad_snapshot_id_too_short` — only 3 hex chars (need 8–17)
3. `mountpoint_not_absolute` — relative path
4. `mountpoint_reserved_root` — `/`
5. `mountpoint_reserved_workspace` — `/workspace`
6. `mountpoint_reserved_opt_exact` — `/opt` (exact)
7. `mountpoint_collision_with_additional_volume` — collides with `additionalVolume.mountPoint`
8. `mountpoint_collision_across_snapshots` — two entries share a mountPoint
9. `explicit_device_duplicate` — two entries share explicit device `/dev/sdh`
10. `non_ec2_substrate_docker` — docker substrate with additionalSnapshots
11. `size_negative` — `size: -1`

Happy-path cases:
1. `empty_snapshots_no_validation_overhead` — empty slice
2. `nil_snapshots_no_errors` — nil slice
3. `single_minimal_entry_valid` — minimal valid entry
4. `canonical_17char_snapshot_id_valid` — 17-char hex snapshotId
5. `mountpoint_subpath_of_opt_ok` — `/opt/models` allowed
6. `size_zero_is_ok_inherit` — size=0 means inherit
7. `three_entries_distinct` — three valid entries, no collisions

## service_hcl.go:681 Area Change

Added immediately after the existing `additionalVolume` check at line 681:

```go
if len(p.Spec.Runtime.AdditionalSnapshots) > 0 && !strings.HasPrefix(substrate, "ec2") {
    return fmt.Errorf("additionalSnapshots is not supported for %s substrate", substrate)
}
```

This is the "compile-time defense-in-depth" layer (reached via `km create`) that complements the `validate.go` offline layer (reached via `km validate`).

## Task Commits

1. **Task 1: Layer 1 validation implementation** — `67aeb0d` (feat)
2. **Task 2: Compiler substrate gate** — `3e90553` (feat)

## Files Modified

- `pkg/profile/validate.go` — Added `validateAdditionalSnapshots` helper + call from `ValidateSemantic`
- `pkg/profile/validate_test.go` — Replaced RED stub with 18-case table-driven test
- `pkg/compiler/service_hcl.go` — Added additionalSnapshots substrate gate in `validateEC2StorageFields`

## Decisions Made

- `regexp.MustCompile` at function scope (not package-level) — acceptable for non-hot-path validation
- `size == 0` valid (inherit semantics); only `size < 0` rejected at Layer 1
- Exact-match reserved-mountpoint list (not prefix match)
- Both `validate.go` and `service_hcl.go` carry the EC2-only gate (defense-in-depth)

## Deviations from Plan

None — plan executed exactly as written. Test structure matched the spec: table-driven cases, programmatic profile construction, `ValidateSemantic` entry point, `spec.runtime.additionalSnapshots[N].field` error path format.

## Pre-existing Failures (Out-of-Scope)

6 pre-existing compiler test failures remain unchanged from before this plan:
`TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock`, `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`,
`TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime`, `TestUserDataKMTracingServicectlStart`,
`TestAuditHookNonBlocking`, `TestGitHubUserDataGITASKPASS`. Confirmed pre-existing per 87-01 SUMMARY.

## Self-Check

---
*Phase: 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile*
*Completed: 2026-05-22*
