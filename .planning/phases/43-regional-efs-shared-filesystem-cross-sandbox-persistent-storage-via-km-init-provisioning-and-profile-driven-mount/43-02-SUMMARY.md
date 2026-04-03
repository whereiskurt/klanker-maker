---
phase: 43-regional-efs-shared-filesystem-cross-sandbox-persistent-storage-via-km-init-provisioning-and-profile-driven-mount
plan: 02
subsystem: infra
tags: [efs, ec2, userdata, bootstrap, filesystem, compiler, networking]

# Dependency graph
requires:
  - phase: 43-01
    provides: "MountEFS/EFSMountPoint fields on RuntimeSpec in pkg/profile/types.go"
  - phase: 43-00
    provides: "efs Terragrunt module (infra/live/{region}/efs/) writing efs/outputs.json"
provides:
  - "LoadEFSOutputs() in internal/app/cmd/init.go: reads filesystem_id from efs/outputs.json"
  - "NetworkConfig.EFSFilesystemID field in pkg/compiler/service_hcl.go"
  - "EFS mount block in EC2 userdata template (amazon-efs-utils, fstab, _netdev,nofail,tls)"
  - "create.go wires EFS ID through full data path and validates mountEFS+EFS init"
affects:
  - "future phases that generate or test EC2 userdata"
  - "future EFS features that need the filesystem ID in compiler context"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "TF output loading pattern: LoadEFSOutputs mirrors LoadNetworkOutputs — IsNotExist returns (\"\", nil) for optional infra"
    - "Optional network parameter added to generateUserData before variadic emailDomainOverride"
    - "EFS mount uses _netdev,nofail,tls fstab options for resilient boot-time mounting"

key-files:
  created: []
  modified:
    - "internal/app/cmd/init.go — LoadEFSOutputs() added after LoadNetworkOutputs"
    - "internal/app/cmd/create.go — Step 6a-efs: LoadEFSOutputs + network.EFSFilesystemID + mountEFS validation"
    - "pkg/compiler/service_hcl.go — EFSFilesystemID string field on NetworkConfig"
    - "pkg/compiler/userdata.go — EFSFilesystemID/EFSMountPoint on userDataParams, EFS template section 2.7, network param on generateUserData"
    - "pkg/compiler/compiler.go — pass network to generateUserData"
    - "internal/app/cmd/init_test.go — TestLoadEFSOutputs_Success/NotExist/MalformedJSON"
    - "pkg/compiler/userdata_test.go — TestUserDataEFSMount/NoEFSMount/EFSCustomMountPoint/EFSMountWithNoNetwork/TestDestroyNoEFSReference"

key-decisions:
  - "generateUserData signature extended with network *NetworkConfig before variadic emailDomainOverride — avoids breaking 40+ test callers by accepting nil"
  - "LoadEFSOutputs returns (\"\", nil) when efs/outputs.json missing — EFS is optional infrastructure, not an error like missing network"
  - "EFS mount block rendered conditionally on EFSFilesystemID non-empty — zero overhead when EFS not used"
  - "km destroy has zero EFS awareness — EFS is persistent regional infra, destroy only tears down per-sandbox resources"

patterns-established:
  - "Optional infra outputs pattern: LoadXOutputs returns empty string (not error) when file missing"
  - "TDD pattern: RED commit of failing tests, then GREEN implementation commit"

requirements-completed: [EFS-02, EFS-04, EFS-06]

# Metrics
duration: 7min
completed: 2026-04-03
---

# Phase 43 Plan 02: EFS Data Path Wiring Summary

**Full EFS data path wired: LoadEFSOutputs -> NetworkConfig.EFSFilesystemID -> userdata template installs amazon-efs-utils and mounts with _netdev,nofail,tls at boot**

## Performance

- **Duration:** 7 min
- **Started:** 2026-04-03T01:59:47Z
- **Completed:** 2026-04-03T02:06:35Z
- **Tasks:** 2 (both TDD)
- **Files modified:** 7

## Accomplishments
- `LoadEFSOutputs(repoRoot, regionLabel)` reads `efs/outputs.json`, returns `("", nil)` when file missing (EFS not yet init'd)
- `NetworkConfig.EFSFilesystemID` carries the filesystem ID from init outputs to the compiler
- `create.go` loads EFS outputs in the non-docker path and errors descriptively when `mountEFS:true` but EFS uninitialized
- Userdata template section 2.7 installs `amazon-efs-utils`, adds fstab entry with `_netdev,nofail,tls`, mounts via `mount -a`, verifies with `mountpoint -q`, and logs graceful failure
- `km destroy` has zero EFS references (EFS-06 satisfied, verified by `TestDestroyNoEFSReference`)

## Task Commits

Each task was committed atomically:

1. **Task 1 RED: LoadEFSOutputs tests** - `c90858e` (test)
2. **Task 1 GREEN: LoadEFSOutputs + NetworkConfig + create.go** - `1fbc31d` (feat)
3. **Task 2 RED: Userdata EFS tests** - `a99fbe8` (test)
4. **Task 2 GREEN: Userdata template + generateUserData network param** - `75b0cc7` (feat)

## Files Created/Modified
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/init.go` - Added `LoadEFSOutputs()` function
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/create.go` - Step 6a-efs: EFS wiring and validation
- `/Users/khundeck/working/klankrmkr/pkg/compiler/service_hcl.go` - `EFSFilesystemID` on `NetworkConfig`
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go` - EFS fields on params, template section 2.7, `network` param on `generateUserData`
- `/Users/khundeck/working/klankrmkr/pkg/compiler/compiler.go` - Pass `network` to `generateUserData`
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/init_test.go` - Three `TestLoadEFSOutputs_*` tests
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata_test.go` - Five EFS-related tests
- `/Users/khundeck/working/klankrmkr/pkg/compiler/service_hcl_email_test.go` - Updated `generateUserData` call to pass `nil` network

## Decisions Made
- `generateUserData` signature extended with `network *NetworkConfig` before the variadic `emailDomainOverride ...string`. All existing test callers pass `nil`; the non-test caller in `compiler.go` passes the real network. This preserves backward compatibility without changing 40+ call sites.
- `LoadEFSOutputs` returns `("", nil)` on missing file (unlike `LoadNetworkOutputs` which errors). EFS is optional regional infra; callers decide whether to error based on profile settings.

## Deviations from Plan

None — plan executed exactly as written. The `generateUserData` signature change (adding `network *NetworkConfig`) was the intended approach described in the plan's NOTE section.

## Issues Encountered
- Pre-existing `TestUnlockCmd_RequiresStateBucket` failure in `internal/app/cmd` (unrelated to EFS). Verified it existed before these changes.

## Next Phase Readiness
- Full EFS data path complete: `efs/outputs.json -> LoadEFSOutputs() -> NetworkConfig.EFSFilesystemID -> userDataParams -> template`
- Phase 43 (EFS) is now functionally complete: provisioning (Phase 43-00), profile schema (Phase 43-01), and data wiring (this plan)
- EFS shared filesystem mounts automatically at EC2 boot when `mountEFS: true` in profile

## Self-Check: PASSED

- FOUND: internal/app/cmd/init.go (LoadEFSOutputs)
- FOUND: internal/app/cmd/create.go (EFS wiring)
- FOUND: pkg/compiler/service_hcl.go (EFSFilesystemID on NetworkConfig)
- FOUND: pkg/compiler/userdata.go (EFS template + params)
- FOUND: 43-02-SUMMARY.md
- Commits verified: c90858e, 1fbc31d, a99fbe8, 75b0cc7, cae58e2

---
*Phase: 43-regional-efs-shared-filesystem-cross-sandbox-persistent-storage-via-km-init-provisioning-and-profile-driven-mount*
*Completed: 2026-04-03*
