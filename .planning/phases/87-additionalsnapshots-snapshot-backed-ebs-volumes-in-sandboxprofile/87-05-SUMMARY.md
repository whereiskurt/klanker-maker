---
phase: 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile
plan: 05
subsystem: compiler
tags: [ebs, snapshots, userdata, tdd, blkid, fstype, golden-file, range-loop]

requires:
  - "87-01 (AdditionalSnapshotSpec Go type + RED stubs)"
  - "87-04 (AdditionalSnapshotEntry render struct + device allocation)"

provides:
  - "AdditionalVolumeMountEntry struct in pkg/compiler/userdata.go"
  - "lastLetter() helper in pkg/compiler/userdata.go"
  - "{{- range .AdditionalVolumeMounts }} loop in userDataTemplate (replaces if .AdditionalVolumeMountPoint)"
  - "FSTYPE blkid detection in every mount block — snapshot-restored xfs/btrfs never reformatted"
  - "Golden file pkg/compiler/testdata/userdata_additional_volume_only.golden.sh"
  - "3 GREEN tests: GoldenByteIdentical, LoopOrder, BackwardCompat"

affects:
  - "87-06 (Terraform module v1.1.0 — independent; consumes additional_snapshots HCL from plan 04)"

tech-stack:
  added: []
  patterns:
    - "range .AdditionalVolumeMounts — unified loop over legacy additionalVolume + additionalSnapshots entries"
    - "lastLetter(devicePath) — extracts trailing char from /dev/sdX for bash device probe list"
    - "FSTYPE=$(blkid -s TYPE -o value $DEVICE) with ext4 fallback — preserves snapshot FS"
    - "mkfs.ext4 -F gated on ! blkid — idempotent, never reformats snapshot-restored volumes"
    - "Golden-file test pattern: generate once, compare byte-identical on subsequent runs"

key-files:
  created:
    - pkg/compiler/testdata/userdata_additional_volume_only.golden.sh
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go

key-decisions:
  - "Legacy additionalVolume always maps to device letter 'f' (hardcoded to historical /dev/sdf default) — no Device field in AdditionalVolumeSpec"
  - "additionalSnapshots entries use their explicit Device field (from profile spec) or sequential auto-assignment starting at 'g' when additionalVolume is present, 'f' otherwise"
  - "No nvmeAlias Go template func added — matches critical_research_corrections #2 locked decision; bash-side for-dev loop handles aliasing"
  - "AdditionalVolumeMountPoint field preserved in userDataParams for backward compat (other callers that might reference it)"
  - "Golden file generated from post-refactor render; only diff vs pre-Phase-87 baseline is ext4 → ${FSTYPE} in fstab line and updated wait/warning log messages"

metrics:
  duration: 281s
  completed: 2026-05-22
  tasks: 1
  files_modified: 3
---

# Phase 87 Plan 05: Wave 3 Userdata Loop Refactor + blkid FS Detection Summary

**Refactored userdata mount block from single-entry if-gate to range loop with blkid-based FSTYPE detection; golden test enforces byte-identical legacy output (modulo ${FSTYPE}); snapshot-restored ext4/xfs/btrfs never reformatted**

## Performance

- **Duration:** 281s (~5 min)
- **Started:** 2026-05-22T21:52:56Z
- **Completed:** 2026-05-22T21:57:37Z
- **Tasks:** 1
- **Files modified:** 3 (2 modified + 1 created)

## Accomplishments

- Replaced single-entry `{{- if .AdditionalVolumeMountPoint }}` block (lines 96–138) with `{{- range .AdditionalVolumeMounts }}` loop
- Added `AdditionalVolumeMountEntry` struct with `MountPoint`, `DeviceLetter`, `Label` fields
- Added `lastLetter()` helper that extracts trailing character from `/dev/sdX` device path
- Added `AdditionalVolumeMounts []AdditionalVolumeMountEntry` field to `userDataParams`
- Wiring in `generateUserData` builds unified list:
  - Legacy `additionalVolume` → device letter `f`, label `"additional volume"`
  - Each `additionalSnapshots[i]` → uses explicit Device field letter or sequential auto-assign starting at `g`/`f`
- Each mount block now:
  - Detects FS type via `FSTYPE=$(blkid -s TYPE -o value "$DEVICE")` with `ext4` fallback
  - Uses `${FSTYPE}` (bash variable expansion) in the fstab line — not hardcoded ext4
  - Still gates `mkfs.ext4 -F` on `! blkid "$DEVICE"` — snapshot-restored volumes never reformatted
  - Probes `/dev/xvd{letter}` and `/dev/sd{letter}` for AL2023/Ubuntu compatibility
- Generated golden file `testdata/userdata_additional_volume_only.golden.sh` from post-refactor render
- Flipped all 3 RED stubs to GREEN:
  - `TestUserdataAdditionalVolumeOnly_GoldenByteIdentical` — byte-identical golden test + explicit `${FSTYPE}` assertion
  - `TestUserdataAdditionalSnapshots_LoopOrder` — 3 blocks in declaration order, device letters f/g/h each once
  - `TestUserdataBackwardCompat_ZeroDiffNoSnapshots` — zero mount blocks when no volumes/snapshots configured
- Fixed pre-existing `TestUserDataAdditionalVolumeWaitMessage` to match new Phase 87 log message format

## Deleted Block Location

Old single-entry block: `userdata.go` lines 96–138 (pre-commit), `{{- if .AdditionalVolumeMountPoint }}` through `{{- end }}`

New range loop starts at same position (line 96) with `{{- range .AdditionalVolumeMounts }}`.

## New Type + Helper Locations

- `AdditionalVolumeMountEntry` struct: `pkg/compiler/userdata.go`, immediately after `otpSecret` struct (~line 3344 post-commit)
- `lastLetter()` helper: same file, immediately after `AdditionalVolumeMountEntry` struct definition
- `AdditionalVolumeMounts` field: in `userDataParams` struct, immediately after `AdditionalVolumeMountPoint` field

## Golden File

- **Path:** `pkg/compiler/testdata/userdata_additional_volume_only.golden.sh`
- **Size:** 85222 bytes
- **Generated from:** `baseProfile()` + `AdditionalVolumeSpec{Size: 30, MountPoint: "/data"}`, rendered via `generateUserData(p, "test-sb", nil, "my-bucket", false, nil)`
- **Key diff vs pre-Phase-87:** fstab line uses `${FSTYPE}` instead of `ext4`; wait message says `"Waiting for additional volume to attach (target /dev/sdf)..."` instead of `"Waiting for additional EBS volume to attach..."`
- **SNAP-07 assertion:** `strings.Contains(got, "${FSTYPE}")` explicitly tested

## Confirmation: No nvmeAlias Go Template Func

No `nvmeAlias` template function was added. The bash-side device probe loop `for dev in /dev/xvd{{ .DeviceLetter }} /dev/sd{{ .DeviceLetter }} /dev/nvme1n1 /dev/nvme2n1` handles device aliasing inline, per critical_research_corrections #2 locked decision (option (b) minimal-diff).

## Note for Plan 06

TF module v1.1.0 (plan 06) must consume the `additional_snapshots` HCL block emitted by plan 04 (`generateEC2ServiceHCL`). The userdata changes here are fully independent of module-side changes — the mount scripts run inside the EC2 instance at boot time, while the TF module provisions the EBS volumes before boot.

## Task Commits

1. **Task 1: Refactor userdata mount block + blkid FS detection + golden tests** — `5256940` (feat)

## Files Modified

- `pkg/compiler/userdata.go` — Replaced if-gate with range loop, added `AdditionalVolumeMountEntry` struct, `lastLetter()` helper, `AdditionalVolumeMounts` field + wiring
- `pkg/compiler/userdata_test.go` — Replaced 3 RED stubs with GREEN tests; updated `TestUserDataAdditionalVolumeWaitMessage`; added `diffStrings()` helper; added `"fmt"` import
- `pkg/compiler/testdata/userdata_additional_volume_only.golden.sh` — NEW: committed golden output for byte-identical legacy test

## Decisions Made

- Legacy `additionalVolume` always uses device letter `f` (no Device field in AdditionalVolumeSpec — historical /dev/sdf)
- `additionalSnapshots` entries use explicit `Device` field from profile spec, or sequential auto-assign in generateUserData (g, h, ... when additionalVolume takes f)
- `AdditionalVolumeMountPoint` field preserved in userDataParams (backward compat)

## Deviations from Plan

**1. [Rule 1 - Bug] Fixed TestUserDataAdditionalVolumeWaitMessage for new log message format**
- **Found during:** Task 1 GREEN verification
- **Issue:** Existing test expected `"[km-bootstrap] Waiting for additional EBS volume"` but new template emits `"[km-bootstrap] Waiting for additional volume to attach"`
- **Fix:** Updated the expected substring in the test to match the Phase 87 template text
- **Files modified:** `pkg/compiler/userdata_test.go`
- **Commit:** `5256940`

**2. [Rule 1 - Bug] Fixed section marker in TestUserdataAdditionalSnapshots_LoopOrder**
- **Found during:** Task 1 RED phase
- **Issue:** Test used `"# Additional EBS volume: format and mount"` but actual template text is `"# 2.6. Additional EBS volume: format and mount ("` — the `"2.6."` prefix breaks the substring match
- **Fix:** Updated marker to `"Additional EBS volume: format and mount ("` (matches the actual template)
- **Files modified:** `pkg/compiler/userdata_test.go`
- **Commit:** `5256940`

## Pre-existing Failures (Out-of-Scope)

6 pre-existing compiler test failures unchanged before and after this plan:
`TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock`, `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`,
`TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime`, `TestUserDataKMTracingServicectlStart`,
`TestAuditHookNonBlocking`, `TestGitHubUserDataGITASKPASS`.

## Self-Check

- `pkg/compiler/userdata.go` — FOUND
- `pkg/compiler/userdata_test.go` — FOUND
- `pkg/compiler/testdata/userdata_additional_volume_only.golden.sh` — FOUND
- Commit `5256940` — FOUND

---
*Phase: 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile*
*Completed: 2026-05-22*
