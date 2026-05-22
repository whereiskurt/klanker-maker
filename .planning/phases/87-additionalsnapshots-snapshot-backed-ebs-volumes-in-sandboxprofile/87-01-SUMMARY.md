---
phase: 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile
plan: 01
subsystem: profile
tags: [ebs, snapshots, schema, types, tdd, json-schema, validation]

requires: []

provides:
  - "AdditionalSnapshotSpec Go struct with Encrypted *bool (Phase 87 SNAP-01)"
  - "RuntimeSpec.AdditionalSnapshots []AdditionalSnapshotSpec field"
  - "additionalSnapshots array in JSON schema with snapshotId/device/size regex enforcement"
  - "RED-state test stubs in 6 Wave-0 target files for SNAP-02 through SNAP-07"

affects:
  - "87-02 (SNAP-02 Layer 1 validate — flips TestValidateAdditionalSnapshots_Layer1 GREEN)"
  - "87-03 (SNAP-03 AWS pre-flight — implements aws_validate_test.go stubs)"
  - "87-04 (SNAP-04/SNAP-05 HCL rendering — implements service_hcl_test.go stubs)"
  - "87-05 (SNAP-06/SNAP-07 userdata generation — implements userdata_test.go stubs)"

tech-stack:
  added: []
  patterns:
    - "Encrypted *bool (pointer, not plain bool) for tri-state nil/true/false semantics — AWS inherits snapshot encryption when nil"
    - "additionalSnapshots is []AdditionalSnapshotSpec with omitempty — zero entries leaves field nil, backward compat"
    - "JSON schema additionalProperties: false on each array item — unknown fields (e.g. kmsKeyId) rejected at schema layer"

key-files:
  created:
    - pkg/profile/aws_validate_test.go
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/types_test.go
    - pkg/profile/validate_test.go
    - pkg/compiler/service_hcl_test.go
    - pkg/compiler/userdata_test.go
    - pkg/compiler/ec2_storage_test.go

key-decisions:
  - "Encrypted MUST be *bool (pointer), NOT plain bool — distinguishes omitted (nil→terraform null, inherit snapshot encryption) from explicit false (force-unencrypted), different from AdditionalVolumeSpec.Encrypted which is plain bool"
  - "snapshotId regex ^snap-[0-9a-f]{8,17}$ — 8 chars matches real AWS IDs, 17 chars matches canonical form; uppercase excluded per AWS reality"
  - "device regex ^/dev/sd[f-p]$ — constrains to non-root pool, consistent with existing additionalVolume device allocation"
  - "size minimum 1 in JSON schema — enforces positivity at Layer 0 (schema), Wave 1 plan-02 will add size-vs-snapshot cross-check at Layer 2 (AWS)"

patterns-established:
  - "Wave 0 RED-state stubs: create skip-only test functions in all downstream target files so Wave N+ plans have valid automated verify commands"
  - "TDD for type additions: test → types.go + schema → verify GREEN, then commit atomically"

requirements-completed: [SNAP-01]

duration: 5min
completed: 2026-05-22
---

# Phase 87 Plan 01: Wave 0 Schema Scaffolding + RED-State Test Stubs Summary

**AdditionalSnapshotSpec Go type with Encrypted *bool, additionalSnapshots JSON schema array (snapshotId/device/size regex), and RED-state test stubs in 6 target files for Wave 1-3 implementation**

## Performance

- **Duration:** 5 min
- **Started:** 2026-05-22T03:28:31Z
- **Completed:** 2026-05-22T03:33:38Z
- **Tasks:** 2
- **Files modified:** 7 (6 modified + 1 created)

## Accomplishments

- Declared `AdditionalSnapshotSpec` struct with correct `Encrypted *bool` (pointer — not plain bool per CONTEXT.md lock)
- Added `RuntimeSpec.AdditionalSnapshots []AdditionalSnapshotSpec` field with `omitempty` (backward compat preserved)
- Added `additionalSnapshots` array to JSON schema with `snapshotId` regex `^snap-[0-9a-f]{8,17}$`, `device` regex `^/dev/sd[f-p]$`, `size` minimum 1, `additionalProperties: false`
- 17 new tests: 8 YAML parse sub-tests + 9 JSON schema validation sub-tests — all GREEN
- 10 RED-state stubs across 5 files (2 existing + 1 new) so Wave 1-3 have automated `<verify>` targets

## Type details

- Exact insertion location: `types.go` line 138 (immediately after `AdditionalVolumeSpec` closing brace)
- `Encrypted *bool` (pointer) — intentionally different from `AdditionalVolumeSpec.Encrypted bool` (plain bool)
  - `nil` → terraform `null` → AWS inherits snapshot's encryption state
  - `*false` → terraform `false` → explicitly unencrypted
  - `*true` → terraform `true` → explicitly encrypted
- JSON schema entry location: `sandbox_profile.schema.json` inside `spec.runtime.properties`, sibling of `additionalVolume`

## Task Commits

1. **Task 1: Add AdditionalSnapshotSpec Go type + JSON schema + TDD tests** - `73bbde5` (feat)
2. **Task 2: Stand up RED-state test stubs in 5 remaining target files** - `29322a7` (feat)

## Files Created/Modified

- `pkg/profile/types.go` — Added `AdditionalSnapshotSpec` struct (line 139) and `RuntimeSpec.AdditionalSnapshots` field (after line 173)
- `pkg/profile/schemas/sandbox_profile.schema.json` — Added `additionalSnapshots` array with regex enforcement inside `spec.runtime.properties`
- `pkg/profile/types_test.go` — Added `TestAdditionalSnapshotSpec_YAMLParse` (8 sub-tests) and `TestAdditionalSnapshotSpec_JSONSchemaValidation` (9 sub-tests)
- `pkg/profile/validate_test.go` — Added `TestValidateAdditionalSnapshots_Layer1` stub (SNAP-02)
- `pkg/profile/aws_validate_test.go` — NEW FILE: 6 AWS pre-flight test stubs (SNAP-03: happy path, not found, pending state, size override, UnauthorizedOperation warn, AccessDenied fail)
- `pkg/compiler/service_hcl_test.go` — Added `TestAdditionalSnapshotsHCLRender` and `TestBoolPtrHCLTemplateFunc` stubs (SNAP-04/05)
- `pkg/compiler/userdata_test.go` — Added 3 userdata generation stubs (SNAP-06/07)
- `pkg/compiler/ec2_storage_test.go` — Added `TestPickAdditionalVolumeDevice_WithClaimedMap` stub (SNAP-04 extended device-picker)

## Decisions Made

- Used `*bool` for `Encrypted` in `AdditionalSnapshotSpec` to match Terraform's `null` semantics for inherited snapshot encryption — this is the critical difference from `AdditionalVolumeSpec.Encrypted bool`
- Placed `additionalSnapshots` as a sibling of `additionalVolume` in both Go types and JSON schema for structural symmetry
- Wave 0 stubs use `t.Skip` with explicit downstream-wave handoff comments to satisfy the Nyquist rule: every downstream plan's `<automated>` command references an existing test function

## Deviations from Plan

None - plan executed exactly as written. The test file package was `profile_test` (not `profile`), so `Parse` and `Validate` calls needed `profile.` prefix — this was a minor code adaptation, not a deviation.

## Issues Encountered

The test file `pkg/profile/types_test.go` is in `package profile_test` which required using `profile.Parse` and `profile.Validate` instead of unqualified names. The plan showed unqualified names but the correct form was straightforward to derive from the existing test patterns.

6 pre-existing test failures in `pkg/compiler` (TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock, TestUserDataNotifyEnv_NoChannelOverride_NoChannelID, TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime, TestUserDataKMTracingServicectlStart, TestAuditHookNonBlocking, TestGitHubUserDataGITASKPASS) were confirmed pre-existing on the main branch before this plan's execution — out of scope per deviation rules scope boundary.

## Next Phase Readiness

- Wave 1 (plan-02 SNAP-02): Implement `TestValidateAdditionalSnapshots_Layer1` — Layer 1 semantic validation in `validate.go`
- Wave 1 (plan-03 SNAP-03): Implement `aws_validate_test.go` stubs — Layer 2 AWS pre-flight with mock EC2SnapshotAPI
- Wave 2 (plan-04 SNAP-04/05): Implement HCL rendering stubs — `additional_snapshots` block + `boolPtrHCL` template func + extended device-picker
- Wave 3 (plan-05 SNAP-06/07): Implement userdata generation — mount scripts for each snapshot entry

---
*Phase: 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile*
*Completed: 2026-05-22*
