---
phase: 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile
plan: 04
subsystem: compiler
tags: [ebs, snapshots, compiler, hcl, tdd, device-allocation, cross-entry-dedup]

requires:
  - "87-01 (AdditionalSnapshotSpec Go type)"
  - "87-02 (Layer 1 validation)"
  - "87-03 (boolPtrHCL template func already registered)"

provides:
  - "pickAdditionalVolumeDevice(amiDevices []string, claimed map[string]bool) string — extended signature"
  - "AdditionalSnapshotEntry render struct in pkg/compiler/service_hcl.go"
  - "ec2HCLParams.AdditionalSnapshots []AdditionalSnapshotEntry field"
  - "additional_snapshots = [...] HCL block in ec2ServiceHCLTemplate using boolPtrHCL for encrypted"
  - "Allocation loop in generateEC2ServiceHCL: cross-entry claimed map, pool exhaustion error"

affects:
  - "87-05 (Wave 3 userdata — AdditionalSnapshotEntry.MountPoint field ready for consumption)"
  - "87-06 (Wave 4 module variable — additional_snapshots HCL block structure)"

tech-stack:
  added: []
  patterns:
    - "claimed map[string]bool passed to pickAdditionalVolumeDevice for cross-entry device deduplication"
    - "nil claimed = back-compat (additionalVolume single-entry callers unchanged)"
    - "Pool exhaustion: caller detects collision by checking if returned device is already in claimed (string-only return, no error from picker)"
    - "additional_snapshots = [] (compact empty form) when no snapshots; brace-form when entries present"
    - "boolPtrHCL template func: nil→null, *true→true, *false→false — inherited from 87-03"

key-files:
  created: []
  modified:
    - pkg/compiler/service_hcl.go
    - pkg/compiler/service_hcl_test.go
    - pkg/compiler/ec2_storage_test.go

key-decisions:
  - "Pool exhaustion detection: pickAdditionalVolumeDevice returns string only (unchanged); the allocation loop in generateEC2ServiceHCL detects collision by checking if returned device is in claimed map. This is the minimal-diff approach — no function signature change to (string, error)"
  - "Empty list renders as `additional_snapshots = []` (compact), not `additional_snapshots = [\n    ]` — diff-stable across profile versions; module default = [] accepts both"
  - "Template uses {{- if .AdditionalSnapshots }} branch to emit compact empty vs populated list — avoids whitespace-only difference between 0-entry and non-zero-entry renders"

metrics:
  duration: 242s
  completed: 2026-05-22
  tasks: 2
  files_modified: 3
---

# Phase 87 Plan 04: Wave 2 Compiler — Device Allocation + HCL Render Summary

**Extended pickAdditionalVolumeDevice with claimed map for cross-entry dedup, AdditionalSnapshotEntry render struct, additional_snapshots HCL block with boolPtrHCL encrypted, pool exhaustion error naming offending index**

## Performance

- **Duration:** 242s (~4 min)
- **Started:** 2026-05-22T21:45:34Z
- **Completed:** 2026-05-22T21:49:36Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments

- Extended `pickAdditionalVolumeDevice` signature: `(amiDevices []string, claimed map[string]bool) string`
  - `nil` claimed = back-compat for all pre-Phase-87 callers
  - claimed entries + their xvd/sd aliases added to occupied set for cross-entry dedup
  - Production call at `AdditionalVolumeDeviceName` updated to pass `nil`
- Added `AdditionalSnapshotEntry` render struct with `Encrypted *bool` (pointer, not plain bool)
- Extended `ec2HCLParams` with `AdditionalSnapshots []AdditionalSnapshotEntry`
- Added `additional_snapshots` HCL block to `ec2ServiceHCLTemplate`:
  - Empty profile → `additional_snapshots = []` (compact, diff-stable)
  - N entries → one object per entry with `snapshot_id`, `device_name`, `encrypted = {{ boolPtrHCL .Encrypted }}`, `size_gb`
- Added allocation loop in `generateEC2ServiceHCL` (SNAP-04 cross-entry dedup):
  - Seeds `claimed` with `AdditionalVolumeDeviceName` if set
  - For each snapshot entry: explicit device → add to claimed; auto device → `pickAdditionalVolumeDevice(amiBDMDeviceNames, claimed)` → detect pool exhaustion → add to claimed
  - Pool exhaustion error: `spec.runtime.additionalSnapshots[%d]: device pool /dev/sd[f-p] exhausted`
- Flipped both SNAP-04 RED stubs to GREEN:
  - `TestPickAdditionalVolumeDevice_WithClaimedMap` — 7 cases including CRITICAL cross-entry dedup
  - `TestAdditionalSnapshotsHCLRender` — 7 cases: zero, minimal, full, 3-entry order, with additionalVolume, with AMI BDM, pool exhaustion

## Final pickAdditionalVolumeDevice Signature

```go
func pickAdditionalVolumeDevice(amiDevices []string, claimed map[string]bool) string
```

Located: `pkg/compiler/service_hcl.go` line 54.

## Pool Exhaustion Detection Strategy

**Decision: string-return + caller-checks** (minimal-diff approach).

The function returns the fallback `"/dev/sdf"` when all 11 candidates are occupied. The allocation loop in `generateEC2ServiceHCL` checks `if claimed[device]` after an auto-pick — if true, the returned device was already in claimed (pool wrapped to fallback), so the loop emits an error naming the offending entry index. This avoids changing the return type to `(string, error)`.

## Render Function + Allocation Loop Location

- **Function:** `generateEC2ServiceHCL` in `pkg/compiler/service_hcl.go`
- **Allocation loop starts at:** line ~840 (after `params` struct initialization, before Phase 68 ArtifactsBucket wiring)
- **HCL template block:** inside `ec2ServiceHCLTemplate` after `additional_volume_device_name` line (~line 148)

## Note for Plan 05 (Userdata)

`AdditionalSnapshotEntry.MountPoint` is set during device allocation (copied from `spec.runtime.additionalSnapshots[i].mountPoint`). Plan 05 userdata generation should consume `params.AdditionalSnapshots` (the slice of render-ready entries) via a unified `AdditionalVolumeMounts` list — it already has the post-allocation `DeviceName` and `MountPoint` fields needed for the mount-and-format script.

## Task Commits

1. **Task 1: Extend pickAdditionalVolumeDevice + claimed map + GREEN tests** — `b801c99` (feat)
2. **Task 2: Render additional_snapshots HCL block + allocation loop** — `79122e2` (feat)

## Files Modified

- `pkg/compiler/service_hcl.go` — Extended `pickAdditionalVolumeDevice`, added `AdditionalSnapshotEntry` struct, extended `ec2HCLParams`, added HCL template block, added allocation loop
- `pkg/compiler/service_hcl_test.go` — Replaced RED stub `TestAdditionalSnapshotsHCLRender` with 7-case table test; added `fmt` import
- `pkg/compiler/ec2_storage_test.go` — Updated `TestPickAdditionalVolumeDevice` call to `nil` claimed; replaced RED stub `TestPickAdditionalVolumeDevice_WithClaimedMap` with 7-case table test

## Decisions Made

- Pool exhaustion: string-return + caller-checks (minimal diff vs returning `(string, error)`)
- Compact empty list `additional_snapshots = []` vs padded form — compact chosen for diff-stability
- Template branch (`{{- if .AdditionalSnapshots }}`) avoids spurious whitespace in zero-entry output

## Deviations from Plan

None — plan executed exactly as written. One minor template fix applied: the initial implementation rendered `additional_snapshots = [\n    ]` for the empty case; switched to `{{- if }}` branch to emit the compact `additional_snapshots = []` form as specified by the plan's zero_entries behavior.

## Pre-existing Failures (Out-of-Scope)

6 pre-existing compiler test failures unchanged from before plan 87-01:
`TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock`, `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`,
`TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime`, `TestUserDataKMTracingServicectlStart`,
`TestAuditHookNonBlocking`, `TestGitHubUserDataGITASKPASS`.

## Self-Check

- `pkg/compiler/service_hcl.go` — FOUND
- `pkg/compiler/service_hcl_test.go` — FOUND
- `pkg/compiler/ec2_storage_test.go` — FOUND
- Commit `b801c99` — FOUND
- Commit `79122e2` — FOUND

---
*Phase: 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile*
*Completed: 2026-05-22*
