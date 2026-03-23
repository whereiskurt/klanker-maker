---
phase: 18-loose-ends
plan: 04
subsystem: doctor-checks
tags: [doctor, lambda, ses, kms, testing, bootstrap]
dependency_graph:
  requires: [18-01]
  provides: [extended-doctor-checks, bootstrap-kms-di]
  affects: [internal/app/cmd/doctor.go, internal/app/cmd/bootstrap.go]
tech_stack:
  added: [aws-sdk-go-v2/service/lambda v1.88.4]
  patterns: [DI interface injection, TDD red-green-refactor]
key_files:
  created: [internal/app/cmd/bootstrap_kms_test.go]
  modified:
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go
    - internal/app/cmd/bootstrap.go
    - go.mod
    - go.sum
decisions:
  - "Lambda and SES checks use CheckWarn (not CheckError) on missing infra — consistent with optional regional components"
  - "ensureKMSPlatformKey uses variadic KMSEnsureAPI parameter to allow DI without breaking existing callers"
  - "site.hcl is not stale — it is the canonical locals file; root.hcl reads it. The rename was adding root.hcl as include target, not eliminating site.hcl"
metrics:
  duration: "~8 minutes"
  completed: "2026-03-23"
  tasks_completed: 2
  files_changed: 6
requirements: [LE-05, LE-06, LE-10, LE-11, LE-12]
---

# Phase 18 Plan 04: Extended Doctor Checks and Bootstrap KMS Verification Summary

Extended `km doctor` with Lambda and SES domain identity checks using TDD, added KMS DI interface to bootstrap for testability, and verified infra directory state.

## Tasks Completed

| Task | Description | Commit | Status |
|------|-------------|--------|--------|
| 1 | Add Lambda and SES checks to km doctor (TDD) | a97d3bd | DONE |
| 2 | Verify bootstrap KMS, root.hcl rename, clean stale dirs | a430400 | DONE |

## What Was Built

### Task 1: Lambda and SES Doctor Checks (TDD)

Added two new check functions to `internal/app/cmd/doctor.go`:

**`checkLambdaFunction`** — calls `Lambda.GetFunction("km-ttl-handler")`:
- `CheckOK` when function found
- `CheckWarn` with "run 'km init'" remediation on `ResourceNotFoundException`
- `CheckSkipped` when client is nil

**`checkSESIdentity`** — calls `SES.GetEmailIdentity("sandboxes.{domain}")`:
- `CheckOK` when `VerificationStatus == SUCCESS`
- `CheckWarn` with "run 'km init'" remediation on `NotFoundException` or non-SUCCESS status
- `CheckSkipped` when client is nil

Both checks are wired into `buildChecks()` after per-region VPC checks, and both clients are initialized in `initRealDeps()`.

New interfaces added to doctor.go:
- `LambdaGetFunctionAPI` — wraps `lambda.GetFunction`
- `SESGetEmailIdentityAPI` — wraps `sesv2.GetEmailIdentity`

`DoctorDeps` gains `LambdaClient` and `SESClient` fields (nil = skip).

New go.mod dependency: `github.com/aws/aws-sdk-go-v2/service/lambda v1.88.4`

TDD tests (7 new, all pass):
- `TestDoctorLambda_OK`, `TestDoctorLambda_NotFound`, `TestDoctorLambda_NilClient`
- `TestCheckSESIdentity_OK`, `TestCheckSESIdentity_NotFound`, `TestCheckSESIdentity_NilClient`
- `TestBuildChecks_IncludesLambdaAndSES`

### Task 2: Bootstrap KMS, root.hcl Rename, Stale Directory Cleanup

**Bootstrap KMS testability:**
- Added `KMSEnsureAPI` interface to `bootstrap.go` (covers DescribeKey, CreateKey, CreateAlias)
- Refactored `ensureKMSPlatformKey` to accept optional `KMSEnsureAPI` variadic parameter
- Created `bootstrap_kms_test.go` with:
  - `TestEnsureKMSPlatformKey_KeyAlreadyExists` — verifies "already exists" output when DescribeKey succeeds
  - `TestEnsureKMSPlatformKey_CreatesKey` — verifies CreateKey/CreateAlias path and output

**root.hcl rename status:**
`site.hcl` still exists and is actively referenced by 16 locations. Investigation confirmed this is correct architecture:
- `root.hcl` is the new include target (`find_in_parent_folders("root.hcl")`) providing remote_state + provider generation
- `root.hcl` itself reads `site.hcl` for locals via `read_terragrunt_config`
- `site.hcl` is the canonical locals-only file and is NOT being eliminated
- The "rename" was adding `root.hcl` as the standard include file; `site.hcl` continues as a locals source

The `management/scp/terragrunt.hcl` correctly reads `site.hcl` directly (it has a custom cross-account provider, so it cannot use root.hcl's provider generation).

**Stale directory check:**
No stale top-level `infra/live/network/` directory exists. Only `infra/live/use1/network/` exists (correct). The `?? infra/live/use1/network/` in git status is an untracked directory with `outputs.json` and `terragrunt.hcl` — this is normal (untracked outputs from a prior apply).

## Verification

```
go test ./... -count=1
# All 18 packages pass (or skip with no test files)
```

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing testability] Added KMSEnsureAPI DI interface to bootstrap**
- **Found during:** Task 2
- **Issue:** `ensureKMSPlatformKey` created its own KMS client internally with no injection point, making it untestable
- **Fix:** Added `KMSEnsureAPI` interface and variadic client parameter; existing call site unchanged
- **Files modified:** `internal/app/cmd/bootstrap.go`, `internal/app/cmd/bootstrap_kms_test.go`
- **Commit:** a430400

### Informational Findings (not deviations)

**site.hcl is NOT stale** — The plan expected no site.hcl references after root.hcl rename, but site.hcl is the canonical Terragrunt locals file that root.hcl reads internally. The rename was adding root.hcl as the include target, not removing site.hcl. 16 references to site.hcl remain and are correct.

## Self-Check: PASSED

Files verified:
- `internal/app/cmd/doctor.go` — FOUND
- `internal/app/cmd/bootstrap_kms_test.go` — FOUND
- Commit a97d3bd — FOUND
- Commit a430400 — FOUND
