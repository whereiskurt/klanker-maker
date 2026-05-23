---
phase: 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile
verified: 2026-05-22T22:00:00Z
status: passed
score: 13/13 must-haves verified
re_verification:
  previous_status: gaps_found
  previous_score: 11/13
  gaps_closed:
    - "BDM gate not extended for snapshots-only profiles on remote create path (line 1975)"
    - "ValidateSnapshotsAWS not wired into remote dispatch path (runCreateRemote)"
  gaps_remaining: []
  regressions: []
  fix_commit: "085a6fb — fix(87): wire snapshot pre-flight + extend BDM gate in remote dispatch path"
---

# Phase 87: additionalSnapshots Verification Report

**Phase Goal:** Add `spec.runtime.additionalSnapshots` to SandboxProfile — a list of snapshot-backed EBS volumes that materialise into fresh `aws_ebs_volume` resources at sandbox creation, attached on `/dev/sd[f-p]`, mounted via blkid-detected filesystem. EC2-only. Backward-compatible with existing `additionalVolume`.
**Verified:** 2026-05-22
**Status:** passed
**Re-verification:** Yes — after gap closure (commit 085a6fb)

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | A SandboxProfile YAML with `spec.runtime.additionalSnapshots` parses into Go struct | VERIFIED | `AdditionalSnapshotSpec` struct at `pkg/profile/types.go:142`; `RuntimeSpec.AdditionalSnapshots []AdditionalSnapshotSpec` at line 176; `TestAdditionalSnapshotSpec_YAMLParse` GREEN (9 subtests) |
| 2 | JSON schema rejects malformed snapshotId, bad device, size 0, unknown property | VERIFIED | `additionalSnapshots` array at schema line 220 with `^snap-[0-9a-f]{8,17}$` pattern, `^/dev/sd[f-p]$` device pattern, `minimum: 1` size, `additionalProperties: false`; `TestAdditionalSnapshotSpec_JSONSchemaValidation` GREEN (11 subtests) |
| 3 | Omitted `encrypted` field leaves `Encrypted` as nil (not false) — pointer semantics | VERIFIED | `Encrypted *bool` at types.go:153; test case "encrypted omitted sets *bool nil" PASS |
| 4 | Layer 1 validation (km validate) enforces EC2-only, regex, reserved mountpoints, collision, device uniqueness, size >= 1 | VERIFIED | `validateAdditionalSnapshots` in validate.go:418, called from validate.go:411; `TestValidateAdditionalSnapshots_Layer1` GREEN |
| 5 | Layer 2 ValidateSnapshotsAWS fires before compiler on LOCAL km create path | VERIFIED | create.go:638-645 calls `profile.ValidateSnapshotsAWS` before the AZ-retry loop and `compiler.Compile`; 7 GREEN unit tests in `pkg/profile/aws_validate_test.go` |
| 6 | Layer 2 ValidateSnapshotsAWS fires on the REMOTE km create path | VERIFIED | 085a6fb wires `profile.ValidateSnapshotsAWS` at create.go:1992-1999 before `compiler.Compile` at line 2021; UAT `km create profiles/uat/87/uat-5.yaml` (no --local) exits 1 with snap ID in error, zero artifacts, no Lambda dispatch |
| 7 | BDM gate fires for snapshots-only profiles on the LOCAL create path | VERIFIED | create.go:623-625: `compiler.IsRawAMIID(...) && (resolvedProfile.Spec.Runtime.AdditionalVolume != nil \|\| len(resolvedProfile.Spec.Runtime.AdditionalSnapshots) > 0)` |
| 8 | BDM gate fires for snapshots-only profiles on the REMOTE create path | VERIFIED | 085a6fb extends create.go:1977-1979 with identical OR condition: `AdditionalVolume != nil \|\| len(...AdditionalSnapshots) > 0`; confirmed in HEAD at lines 1977-1979 |
| 9 | `pickAdditionalVolumeDevice` extended with claimed map; cross-entry dedup correct | VERIFIED | service_hcl.go:54 new signature; claimed map accumulated across snapshot entries (lines 841-858); `TestPickAdditionalVolumeDevice_WithClaimedMap` GREEN (7 subtests) |
| 10 | Compiler renders `additional_snapshots` HCL block with boolPtrHCL for encrypted | VERIFIED | Template at service_hcl.go:149-160 emits block; `boolPtrHCL` at line 656; `TestAdditionalSnapshotsHCLRender` GREEN (7 subtests including nil/true/false encrypted, pool exhaustion) |
| 11 | Userdata uses blkid-based FSTYPE detection, unified range loop for all mount entries | VERIFIED | userdata.go:96 `range .AdditionalVolumeMounts`; blkid at lines 122-128; `TestUserdataAdditionalSnapshots_LoopOrder` GREEN; `TestUserdataAdditionalVolumeOnly_GoldenByteIdentical` GREEN |
| 12 | ec2spot/v1.1.0 terraform module exists with additional_snapshots variable and resources | VERIFIED | `infra/modules/ec2spot/v1.1.0/` exists; variables.tf:159 `additional_snapshots` variable; main.tf:717 `aws_ebs_volume.snapshot` for_each + main.tf:735 `aws_volume_attachment.snapshot` |
| 13 | Sandbox template and new sandboxes reference ec2spot/v1.1.0 | VERIFIED | `infra/templates/sandbox/terragrunt.hcl:43` has `v1.1.0`; UAT sandbox `uat2-cc63c927/terragrunt.hcl:43` confirms `v1.1.0` |

**Score:** 13/13 truths verified

### Required Artifacts

| Artifact | Status | Details |
|----------|--------|---------|
| `pkg/profile/types.go` | VERIFIED | `AdditionalSnapshotSpec` struct with `Encrypted *bool` at line 142; `RuntimeSpec.AdditionalSnapshots` at line 176 |
| `pkg/profile/schemas/sandbox_profile.schema.json` | VERIFIED | `additionalSnapshots` array at line 220 with snapshotId regex, device regex, size minimum 1, additionalProperties: false |
| `pkg/profile/validate.go` | VERIFIED | `validateAdditionalSnapshots` at line 418, wired at line 411; EC2-only, regex, reserved mountpoints, collision, device checks all implemented |
| `pkg/profile/aws_validate.go` | VERIFIED | 76-line substantive file; `ValidateSnapshotsAWS` + `EC2SnapshotAPI` interface; handles NotFound, pending state, size-too-small, UnauthorizedOperation graceful WARN |
| `internal/app/cmd/create.go` (local path) | VERIFIED | ValidateSnapshotsAWS at lines 638-645; BDM gate extended at lines 623-625 |
| `internal/app/cmd/create.go` (remote path) | VERIFIED | 085a6fb: BDM gate at lines 1977-1979 (OR condition for AdditionalSnapshots); ValidateSnapshotsAWS at lines 1992-1999; both before compiler.Compile at line 2021 |
| `pkg/compiler/service_hcl.go` | VERIFIED | pickAdditionalVolumeDevice with claimed map at line 54; AdditionalSnapshotEntry struct at line 462; ec2ServiceHCLTemplate emits additional_snapshots block at lines 149-160; boolPtrHCL at line 656; EC2-only guard at line 748 |
| `pkg/compiler/userdata.go` | VERIFIED | range .AdditionalVolumeMounts at line 96; blkid FSTYPE detection at lines 122-128; AdditionalVolumeMounts populated at line 3757 |
| `infra/modules/ec2spot/v1.1.0/` | VERIFIED | New module directory; variables.tf has additional_snapshots variable; main.tf has aws_ebs_volume.snapshot and aws_volume_attachment.snapshot with for_each |
| `infra/templates/sandbox/terragrunt.hcl` | VERIFIED | Line 43 references `v1.1.0` |
| `CLAUDE.md` | VERIFIED | Line 27 pointer to OPERATOR-GUIDE.md; lines 83 documents additionalSnapshots field |
| `OPERATOR-GUIDE.md` | VERIFIED | Full section at line 1336 "Phase 87 — additionalSnapshots" with schema reference, usage examples, UAT link |
| `profiles/example-additional-snapshots.yaml` | VERIFIED | File exists, substantive (5 lines mentioning additionalSnapshots/snapshotId/snap-) |
| `profiles/uat/87/uat-{1..8}.yaml` | VERIFIED | All 8 files exist, each substantive (164-177 lines) |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `pkg/profile/types.go AdditionalSnapshotSpec` | `sandbox_profile.schema.json additionalSnapshots` | yaml tag matches JSON schema property name | VERIFIED | `yaml:"snapshotId"` at types.go:143 matches schema property `snapshotId` |
| `RuntimeSpec.AdditionalSnapshots` | `pkg/profile/validate.go validateAdditionalSnapshots` | field lookup | VERIFIED | validate.go:411 `errs = append(errs, validateAdditionalSnapshots(p)...)` |
| `create.go (local)` | `profile.ValidateSnapshotsAWS` | function call before compiler | VERIFIED | Lines 638-645 confirmed; runs with region-overridden EC2 client |
| `create.go (remote)` | `profile.ValidateSnapshotsAWS` | function call before compiler | VERIFIED | 085a6fb: lines 1992-1999 before compiler.Compile at 2021; UAT exit-1 confirmed |
| `create.go (local) BDM gate` | `AdditionalSnapshots length check` | OR condition | VERIFIED | Lines 624-625: `\|\| len(resolvedProfile.Spec.Runtime.AdditionalSnapshots) > 0` |
| `create.go (remote) BDM gate` | `AdditionalSnapshots length check` | OR condition | VERIFIED | 085a6fb: lines 1978-1979: identical OR condition added to runCreateRemote |
| `pkg/compiler/service_hcl.go pickAdditionalVolumeDevice` | `claimed map[string]bool` | new second parameter | VERIFIED | Function signature at line 54 confirmed |
| `ec2ServiceHCLTemplate` | `ec2HCLParams.AdditionalSnapshots` | range loop emitting HCL list | VERIFIED | Lines 149-160: `range .AdditionalSnapshots` with snapshot_id, device_name, encrypted, size_gb |
| `ec2ServiceHCLTemplate` | `boolPtrHCL template func` | `{{ boolPtrHCL .Encrypted }}` call | VERIFIED | Line 154 confirmed; func at line 656 |
| `infra/templates/sandbox/terragrunt.hcl` | `infra/modules/ec2spot/v1.1.0` | module source path | VERIFIED | Line 43: `substrate_module/v1.1.0` |

### Requirements Coverage

| Requirement | Plans | Description | Status | Evidence |
|-------------|-------|-------------|--------|---------|
| SNAP-01 | 87-01 | Go types + JSON schema | SATISFIED | AdditionalSnapshotSpec with *bool Encrypted; schema array with all patterns |
| SNAP-02 | 87-02 | Layer 1 validation (km validate) | SATISFIED | validateAdditionalSnapshots with all prescribed rules; TestValidateAdditionalSnapshots_Layer1 GREEN |
| SNAP-03 | 87-03 | Layer 2 AWS pre-flight (km create) | SATISFIED | LOCAL: create.go:638-645. REMOTE: 085a6fb wires create.go:1992-1999. Both paths pre-flight before compiler.Compile. UAT confirms exit-1 with snap ID in error and zero artifacts on remote path. |
| SNAP-04 | 87-04 | Compiler device allocation + HCL render | SATISFIED | pickAdditionalVolumeDevice with claimed map; HCL template renders additional_snapshots block; tests GREEN |
| SNAP-05 | 87-05 | Userdata FS-aware mount loop | SATISFIED | blkid-detected FSTYPE; range over AdditionalVolumeMounts; TestUserdataAdditionalSnapshots_LoopOrder GREEN |
| SNAP-06 | 87-06 | ec2spot/v1.1.0 terraform module | SATISFIED | Module created; additional_snapshots variable; aws_ebs_volume.snapshot + aws_volume_attachment.snapshot resources; sandbox template bumped |
| SNAP-07 | 87-05,06,07 | Backward compatibility — zero diff for legacy profiles | SATISFIED | TestUserdataBackwardCompat_ZeroDiffNoSnapshots GREEN; TestAdditionalSnapshotsHCLRender "zero entries" PASS; ec2spot/v1.0.0 untouched |
| SNAP-08 | 87-07 | Testing — Go unit + UAT | SATISFIED (with deferred) | All Phase 87 unit tests GREEN; UAT 8/9 PASS, 1 DEFERRED (UAT-4 needs baked AMI; covered by TestPickAdditionalVolumeDevice_WithClaimedMap). Post-fix re-run of UAT-5 (no --local) confirms exit-1 behavior on remote path. |

### Anti-Patterns Found

None. The two WARNING-class anti-patterns from the initial verification are resolved by commit 085a6fb. No new anti-patterns introduced.

Pre-existing test failures in `pkg/compiler` (`TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock`, `TestUserDataKMTracingServicectlStart`, `TestAuditHookNonBlocking`, `TestUserDataNotifyEnv_*`, `TestGitHubUserDataGITASKPASS`) are confirmed pre-Phase-87 regressions reproducing against parent commit `16a5114`. Not caused by Phase 87 changes.

### Human Verification Required

None. All items were human-verified during UAT or covered by unit tests:

1. Single snapshot auto-device mount at /opt/uat1 — PASS (UAT-1)
2. Three-mount coexistence (additionalVolume + 2 snapshots) — PASS (UAT-2)
3. Explicit device pin honored at HCL + AWS attach layer — PASS (UAT-3)
4. AMI BDM auto-pick collision avoidance — DEFERRED (UAT-4), covered by TestPickAdditionalVolumeDevice_WithClaimedMap unit test
5. Pre-flight rejection of bad snap ID (exit 1, zero artifacts) on local path — PASS (UAT-5 with --local)
6. Pre-flight rejection of bad snap ID on remote path — PASS (post-fix re-run, `km create profiles/uat/87/uat-5.yaml` no --local; exit 1, snap ID in error, zero artifacts, no Lambda dispatch)
7. Pre-flight rejection (wrong region path) — PASS (UAT-6)
8. Size override larger than snapshot materializes correct volume size — PASS (UAT-7)
9. Pre-flight rejection of size < snapshot.VolumeSize with both sizes in error — PASS (UAT-8)

### Gap Closure Summary

Both gaps identified in the initial verification shared a root cause: `runCreateRemote` (the default `km create` path) was not updated in sync with `runCreate` when Phase 87 wired the BDM gate extension and `ValidateSnapshotsAWS` pre-flight.

Commit `085a6fb` closed both in a single 20-line change to `internal/app/cmd/create.go`:

**Gap 1 — Remote BDM gate (CLOSED):** `runCreateRemote` line 1977 now reads `IsRawAMIID(...) && (AdditionalVolume != nil || len(AdditionalSnapshots) > 0)`, matching the local path gate at line 623. Snapshots-only profiles with a raw AMI trigger BDM lookup on both paths.

**Gap 2 — Remote pre-flight (CLOSED):** `runCreateRemote` lines 1992-1999 call `profile.ValidateSnapshotsAWS` with a region-scoped EC2 client, mirroring the local path at lines 638-645. The call precedes `compiler.Compile` at line 2021, guaranteeing zero terragrunt artifacts on failure. UAT-5 re-run without `--local` confirmed exit-1 behavior with the snapshot ID named in the error and no Lambda dispatch.

All 13 truths now verified. SNAP-01 through SNAP-08 fully satisfied.

---

_Initial verification: 2026-05-22_
_Re-verification: 2026-05-22 (after commit 085a6fb)_
_Verifier: Claude (gsd-verifier)_
