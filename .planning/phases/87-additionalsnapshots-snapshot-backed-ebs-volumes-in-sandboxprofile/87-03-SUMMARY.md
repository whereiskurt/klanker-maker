---
phase: 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile
plan: 03
subsystem: profile/aws-preflight
tags: [ebs, snapshots, aws-preflight, tdd, ec2, iam-graceful-degradation, bdm-gate]

requires:
  - "87-01 (AdditionalSnapshotSpec Go type + RuntimeSpec.AdditionalSnapshots field)"

provides:
  - "ValidateSnapshotsAWS — single batched DescribeSnapshots call before terragrunt runs"
  - "EC2SnapshotAPI narrow interface — first AWS-calling code in pkg/profile/"
  - "boolPtrHCL template func in service_hcl.go templateFuncs (nil→null, *true→true, *false→false)"
  - "BDM gate broadened: triggers when AdditionalVolume != nil OR len(AdditionalSnapshots) > 0"

affects:
  - "87-04 (Wave 2 HCL rendering — boolPtrHCL already registered)"
  - "UAT-4 (snapshots-only profile with raw AMI — BDM gate fix prevents silent failure)"

tech-stack:
  added: []
  patterns:
    - "smithy.APIError interface via errors.As — preferred over strings.Contains per configure.go pattern"
    - "UnauthorizedOperation (EC2-specific) → WARN+nil; AccessDenied → surface error (different code path)"
    - "Narrow interface EC2SnapshotAPI — injectable mock, mirrors doctor_ebs.go EC2VolumeAPI shape"
    - "Pre-flight wired after region lock (line 467), before retry loop (line 638) — zero artifacts on failure"

key-files:
  created:
    - pkg/profile/aws_validate.go
  modified:
    - pkg/profile/aws_validate_test.go
    - internal/app/cmd/create.go
    - pkg/compiler/service_hcl_test.go

key-decisions:
  - "UnauthorizedOperation (EC2-specific IAM-missing code) → graceful WARN+nil; AccessDenied is a DIFFERENT code path and surfaces as error — per 87-VALIDATION.md aliasing risk SNAP-03"
  - "InvalidSnapshot.NotFound hint message includes region/sharing/deleted (3 explicit hints for operator remediation)"
  - "Pre-flight uses ec2svc.NewFromConfig with explicit o.Region override — ensures sandbox target region, not operator default"
  - "boolPtrHCL already committed by parallel plan 87-02 (3e90553); Task 2 test flip confirmed it was registered correctly"

metrics:
  duration: 342s
  completed: 2026-05-22T21:42:16Z
  tasks: 2
  files: 4
---

# Phase 87 Plan 03: Layer 2 AWS Pre-flight + boolPtrHCL + BDM Gate Fix Summary

**ValidateSnapshotsAWS single-call DescribeSnapshots pre-flight with UnauthorizedOperation graceful degradation, 7 GREEN tests, BDM gate broadened for snapshots-only AMI profiles**

## Performance

- **Duration:** 342s (~6 min)
- **Started:** 2026-05-22T21:36:34Z
- **Completed:** 2026-05-22T21:42:16Z
- **Tasks:** 2
- **Files modified:** 4 (1 created, 3 modified)

## Accomplishments

- Created `pkg/profile/aws_validate.go` — `EC2SnapshotAPI` narrow interface + `ValidateSnapshotsAWS` function
- Replaced 6 RED-state skip stubs in `aws_validate_test.go` with 7 GREEN tests (added `EmptySnapshotsIsNoOp`)
- Wired pre-flight in `create.go` AFTER region lock (line 467), BEFORE retry loop (line 638)
- Pre-flight client uses explicit `o.Region = region` override — uses sandbox target region, not operator default
- Extended BDM gate (Risk #4 fix): `AdditionalVolume != nil || len(AdditionalSnapshots) > 0`
- `boolPtrHCL` template func registered in `templateFuncs` (was already committed by parallel plan 87-02)
- `TestBoolPtrHCLTemplateFunc` flipped GREEN (3 cases: nil→null, *true→true, *false→false)

## Pre-flight Location in create.go

Pre-flight inserted at lines 638–645 (after BDM gate extension, before `for attempt := 0; attempt < maxAttempts`):

```go
// Phase 87 — Layer 2 AWS pre-flight: validate snapshot IDs, state, region, size.
// MUST run before compiler so zero terragrunt artifacts hit disk on failure.
// Uses the sandbox's resolved target region (not operator default profile region).
if len(resolvedProfile.Spec.Runtime.AdditionalSnapshots) > 0 {
    snapPreflightClient := ec2svc.NewFromConfig(awsCfg, func(o *ec2svc.Options) {
        o.Region = region
    })
    if err := profile.ValidateSnapshotsAWS(ctx, snapPreflightClient, resolvedProfile); err != nil {
        return fmt.Errorf("snapshot pre-flight failed: %w", err)
    }
}
```

## BDM Gate Diff (Risk #4 Fix)

```go
// BEFORE:
if compiler.IsRawAMIID(resolvedProfile.Spec.Runtime.AMI) && resolvedProfile.Spec.Runtime.AdditionalVolume != nil {

// AFTER:
if compiler.IsRawAMIID(resolvedProfile.Spec.Runtime.AMI) &&
    (resolvedProfile.Spec.Runtime.AdditionalVolume != nil ||
        len(resolvedProfile.Spec.Runtime.AdditionalSnapshots) > 0) {
```

Without this fix, UAT-4 (snapshots-only profile with a raw AMI ID) would silently fail to detect BDM device collisions.

## boolPtrHCL Status

`boolPtrHCL` was already committed in parallel plan 87-02 (`3e90553`). `TestBoolPtrHCLTemplateFunc` confirmed registration with 3 assertions:
- `nil` → `"null"` (inherit snapshot encryption from AWS)
- `*true` → `"true"` (explicitly encrypted)
- `*false` → `"false"` (explicitly unencrypted)

Wave 2 plan-04 will use this in `additional_snapshots = [{ ..., encrypted = {{ boolPtrHCL .Encrypted }}, ... }]`.

## Task Commits

1. **Task 1: ValidateSnapshotsAWS + 7 GREEN tests** — `01214fa` (feat)
2. **Task 2: Wire pre-flight + extend BDM gate + boolPtrHCL test** — `a1ade36` (feat)

## Test Results

```
pkg/profile: 7/7 TestValidateSnapshotsAWS_* PASS
pkg/compiler: TestBoolPtrHCLTemplateFunc PASS (3 cases)
go build ./... CLEAN
```

6 pre-existing failures in `pkg/compiler` userdata/compiler tests confirmed pre-existing from before plan 87-01.

## Decisions Made

- Used `UnauthorizedOperation` (EC2-specific) for graceful degradation, not `AccessDenied` — per 87-VALIDATION.md SNAP-03 aliasing risk; AccessDenied surfaces as error (different code path)
- NotFound hint contains "region", "shared", "deleted" (satisfies test assertion on 3 operator-facing remediation hints)
- Empty `AdditionalSnapshots` returns nil immediately without calling DescribeSnapshots (verified by `calls == 0` assertion)
- `errors.As(err, &apiErr)` pattern — preferred over `strings.Contains` per configure.go established pattern

## Deviations from Plan

None — plan executed exactly as written.

Note: `boolPtrHCL` was found already committed in `service_hcl.go` by parallel plan 87-02 (`3e90553`). Task 2 Part B only needed to flip the test stub GREEN — the implementation was already in place.

## Next Phase Readiness

- Wave 2 (plan-04 SNAP-04/05): HCL rendering — `additional_snapshots = [...]` block in service.hcl template; `boolPtrHCL` already available; extended device-picker needed
- Wave 3 (plan-05 SNAP-06/07): Userdata generation — mount scripts for each snapshot entry

## Self-Check: PASSED

- `pkg/profile/aws_validate.go` — FOUND
- `pkg/profile/aws_validate_test.go` — FOUND
- `internal/app/cmd/create.go` — FOUND
- `pkg/compiler/service_hcl.go` — FOUND
- Commit `01214fa` — FOUND
- Commit `a1ade36` — FOUND

---
*Phase: 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile*
*Completed: 2026-05-22*
