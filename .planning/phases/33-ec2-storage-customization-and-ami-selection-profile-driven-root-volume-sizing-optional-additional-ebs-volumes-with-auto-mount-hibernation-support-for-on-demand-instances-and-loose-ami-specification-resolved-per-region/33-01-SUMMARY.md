---
phase: 33-ec2-storage-and-ami
plan: 01
subsystem: profile
tags: [ec2, storage, ebs, ami, hibernation, schema, types, validation]

requires: []
provides:
  - RuntimeSpec with RootVolumeSize, AdditionalVolume, Hibernation, AMI fields
  - AdditionalVolumeSpec type with Size, MountPoint, Encrypted
  - JSON schema properties for rootVolumeSize, additionalVolume, hibernation, ami
  - Semantic validation: hibernation+spot conflict, hibernation+ecs conflict, additionalVolume+ecs conflict
affects:
  - 33-02 (HCL compiler for storage/AMI)
  - 33-03 (Terraform module for storage resources)
  - any plan reading RuntimeSpec

tech-stack:
  added: []
  patterns:
    - "TDD: write failing tests first, then implement types/schema/validation"
    - "validateEC2StorageFields() helper called at top of generateEC2ServiceHCL before template execution"
    - "strings.HasPrefix(substrate, 'ec2') used to match ec2/ec2spot/ec2demand variants"

key-files:
  created:
    - pkg/profile/schema_storage_test.go
    - pkg/compiler/ec2_storage_test.go
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/compiler/service_hcl.go

key-decisions:
  - "rootVolumeSize minimum=0 in JSON schema (0 means use AMI default, not rejected)"
  - "ami enum includes empty string to allow omitted values; empty defaults to amazon-linux-2023"
  - "validateEC2StorageFields() called before template execution so errors are clear, not template panics"
  - "strings.HasPrefix(substrate, ec2) for hibernation/additionalVolume checks to cover future ec2spot/ec2demand variants"

patterns-established:
  - "Cross-field validation extracted to named helper (validateEC2StorageFields) not inline in generate function"
  - "New RuntimeSpec fields use omitempty so profiles without storage config are backward compatible"

requirements-completed: [P33-01, P33-02, P33-04, P33-05, P33-06, P33-07]

duration: 3min
completed: 2026-04-02
---

# Phase 33 Plan 01: EC2 Storage Schema, Types, and Semantic Validation Summary

**RuntimeSpec extended with RootVolumeSize, AdditionalVolume, Hibernation, AMI; JSON schema updated; compiler rejects hibernation+spot and ECS storage conflicts.**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-04-02T00:39:42Z
- **Completed:** 2026-04-02T00:42:30Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments

- Added `AdditionalVolumeSpec` type and four new fields to `RuntimeSpec` (RootVolumeSize, AdditionalVolume, Hibernation, AMI)
- Updated JSON schema with rootVolumeSize (minimum 0), additionalVolume object (required size+mountPoint), hibernation bool, ami enum
- Added `validateEC2StorageFields()` helper called at top of `generateEC2ServiceHCL` to reject invalid combinations before template execution
- 12 new tests covering all parsing, schema validation, and semantic constraint cases

## Task Commits

1. **Task 1: RuntimeSpec fields, AdditionalVolumeSpec type, JSON schema** - `92ffd18` (feat)
2. **Task 2: EC2 storage semantic validation** - `4b5a4fe` (feat)

**Plan metadata:** (docs commit follows)

_Note: TDD tasks — tests written first, then implementation in each task._

## Files Created/Modified

- `pkg/profile/types.go` - Added AdditionalVolumeSpec type and four fields to RuntimeSpec
- `pkg/profile/schemas/sandbox_profile.schema.json` - Added rootVolumeSize, additionalVolume, hibernation, ami properties under runtime
- `pkg/profile/schema_storage_test.go` - YAML parsing tests and JSON schema boundary tests
- `pkg/compiler/service_hcl.go` - Added validateEC2StorageFields() helper and call at top of generateEC2ServiceHCL
- `pkg/compiler/ec2_storage_test.go` - 5 semantic validation tests (3 error cases, 2 valid cases)

## Decisions Made

- `rootVolumeSize` minimum is 0 (not 1) because 0 means "use AMI default" — zero is a valid sentinel
- `ami` enum includes `""` to allow YAML profiles that omit the field to pass schema validation
- `validateEC2StorageFields()` runs before template parsing so invalid configs return clear domain errors rather than nil pointer panics in the template engine
- `strings.HasPrefix(substrate, "ec2")` used to future-proof for `ec2spot`/`ec2demand` substrate label variants

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

During RED phase for Task 2, the error tests were failing with a template nil pointer error (IAMPolicy not set in test fixtures) rather than the expected validation error. This was the correct RED behavior — the validation wasn't in place yet. Once `validateEC2StorageFields()` was added as the first thing in `generateEC2ServiceHCL`, the errors were caught before the template ran.

## Next Phase Readiness

- `RuntimeSpec` contract is established — Plan 02 can reference all four new fields when generating HCL
- `AdditionalVolumeSpec` type is available for template parameters struct
- Semantic validation is in place — no need to re-validate in Plan 02 or 03
- All existing profile and compiler tests continue to pass (no regressions)

---
*Phase: 33-ec2-storage-and-ami*
*Completed: 2026-04-02*
