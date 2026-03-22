---
phase: 04-lifecycle-hardening-artifacts-email
plan: "04"
subsystem: email
tags: [ses, s3, iam, lifecycle, terraform, notifications, email, artifacts]

# Dependency graph
requires:
  - phase: 04-01
    provides: UploadArtifacts function (S3PutAPI interface) and ArtifactSkippedEvent
  - phase: 04-02
    provides: ProvisionSandboxEmail, CleanupSandboxEmail, SendLifecycleNotification (SESV2API interface)
  - phase: 04-03
    provides: TeardownCallbacks struct with UploadArtifacts field, ExecuteTeardown dispatch, ECS filesystem enforcement in service_hcl.go
provides:
  - "km create provisions SES email identity per sandbox and outputs the address"
  - "km create stores profile YAML in S3 for destroy-time artifact upload"
  - "km create sends 'created' lifecycle notification when KM_OPERATOR_EMAIL is set"
  - "km destroy loads profile from S3, uploads artifacts via ExecuteTeardown before destroy"
  - "km destroy cleans up SES identity (idempotent) and sends 'destroyed' notification"
  - "ECS service.hcl includes ses:SendEmail+ses:SendRawEmail IAM scoped by ses:FromAddress condition"
  - "ECS service.hcl includes s3:ListObjectsV2+s3:GetObject IAM scoped to mail/{sandbox-id}/* prefix (MAIL-05)"
  - "KM_EMAIL_ADDRESS env var exposed in both ECS main container and EC2 user-data"
  - "EC2 spot poll loop sends SES notification via AWS CLI on spot interruption"
  - "S3 replication Terraform module creates replica bucket, enables versioning, configures artifacts/ prefix replication"
affects: [phase-05, future-plans-using-email]

# Tech tracking
tech-stack:
  added:
    - "sesv2 SDK import in create.go and destroy.go"
    - "S3 replication Terraform module at infra/modules/s3-replication/v1.0.0"
  patterns:
    - "Profile stored in S3 at artifacts/{sandbox-id}/.km-profile.yaml during create for retrieval at destroy"
    - "Email provisioning/cleanup is non-fatal — sandbox remains functional without email"
    - "Lifecycle notifications use SendLifecycleNotification with KM_OPERATOR_EMAIL env gate"
    - "SES IAM uses ses:FromAddress condition to scope each sandbox to its own address"
    - "S3 inbox read IAM scoped to mail/{sandbox-id}/* via prefix condition for MAIL-05"
    - "S3 replication scoped to artifacts/ prefix only — mail/ inbox data excluded"

key-files:
  created:
    - pkg/compiler/service_hcl_email_test.go
    - infra/modules/s3-replication/v1.0.0/main.tf
    - infra/modules/s3-replication/v1.0.0/variables.tf
    - infra/modules/s3-replication/v1.0.0/outputs.tf
  modified:
    - internal/app/cmd/create.go
    - internal/app/cmd/destroy.go
    - pkg/compiler/service_hcl.go
    - pkg/compiler/userdata.go

key-decisions:
  - "Profile YAML stored in S3 at artifacts/{sandbox-id}/.km-profile.yaml to enable destroy-path artifact upload without passing profile through command args"
  - "Email provisioning and cleanup are non-fatal — sandbox is still usable without email (non-fatal pattern matches TTL schedule creation)"
  - "SES IAM uses ses:FromAddress StringEquals condition — each sandbox can only send from its own address, preventing cross-sandbox email abuse"
  - "S3 inbox read IAM uses s3:ListBucket with s3:prefix condition + object-level ListObjectsV2+GetObject on mail/{sandbox-id}/* — complete least-privilege read access"
  - "S3 replication excludes mail/ prefix — inbox objects are ephemeral; only artifacts/ is replicated for durability"
  - "TDD approach: failing tests committed first (abbd7d8), then GREEN implementation (c683e87)"

patterns-established:
  - "Non-fatal post-apply steps: SES provisioning, profile storage, TTL schedule all follow same non-fatal log-warn-and-continue pattern"
  - "downloadProfileFromS3 private function in destroy.go isolates S3 retrieval with nil-safe profile handling in destroy path"
  - "Lifecycle notifications always sent last (after fmt.Printf success message) so notification failure does not affect exit code"

requirements-completed:
  - OBSV-05
  - OBSV-06
  - MAIL-02
  - MAIL-03
  - MAIL-04
  - MAIL-05

# Metrics
duration: 6min
completed: 2026-03-22
---

# Phase 04 Plan 04: SES Email + Artifacts + IAM Wiring Summary

**SES email provisioning, artifact upload via lifecycle.ExecuteTeardown, ses:SendEmail+S3-inbox IAM in ECS service.hcl, KM_EMAIL_ADDRESS in both substrates, and S3 cross-region replication Terraform module**

## Performance

- **Duration:** 6 min
- **Started:** 2026-03-22T14:12:05Z
- **Completed:** 2026-03-22T14:18:16Z
- **Tasks:** 2
- **Files modified:** 8 (4 created, 4 modified)

## Accomplishments
- Wired SES email provisioning into km create (ProvisionSandboxEmail) and cleanup into km destroy (CleanupSandboxEmail), both non-fatal
- Wired lifecycle notifications for created/destroyed events in CLI; spot-interruption via sesv2 CLI call in EC2 user-data spot poll loop
- km destroy now loads sandbox profile from S3 and uploads artifacts via lifecycle.ExecuteTeardown before terragrunt destroy
- ECS service.hcl gets ses:SendEmail+ses:SendRawEmail IAM (ses:FromAddress condition) and s3:ListObjectsV2+s3:GetObject IAM (mail prefix), satisfying MAIL-05
- KM_EMAIL_ADDRESS env var exposed in ECS main container and EC2 user-data
- Created S3 cross-region replication Terraform module that validates successfully

## Task Commits

Each task was committed atomically:

1. **Task 1 (TDD RED): Failing tests for SES IAM, S3 inbox IAM, KM_EMAIL_ADDRESS** - `abbd7d8` (test)
2. **Task 1 (TDD GREEN): Wire SES email, artifact upload, lifecycle notifications, IAM** - `c683e87` (feat)
3. **Task 2: S3 cross-region replication Terraform module** - `38aaa6b` (feat)

**Plan metadata:** (docs commit — recorded at plan close)

_Note: TDD task has two commits (test RED then feat GREEN)_

## Files Created/Modified
- `internal/app/cmd/create.go` — sesv2 import, SES provisioning after apply, profile stored in S3, created notification
- `internal/app/cmd/destroy.go` — s3/sesv2 imports, profile download from S3, artifact upload via TeardownCallbacks, SES cleanup, destroyed notification
- `pkg/compiler/service_hcl.go` — ecsHCLParams email fields (HasEmail, SandboxEmail, ArtifactBucket), ECS template: KM_EMAIL_ADDRESS env var + ses:SendEmail+s3 inbox IAM blocks
- `pkg/compiler/userdata.go` — userDataParams email fields (SandboxEmail, OperatorEmail, AWSRegion), KM_EMAIL_ADDRESS export in user-data, spot notification via AWS CLI
- `pkg/compiler/service_hcl_email_test.go` — TDD tests: TestECSSESIAMPermission, TestECSS3InboxReadPermission, TestECSKMEmailAddressEnvVar, TestEC2UserDataKMEmailAddress
- `infra/modules/s3-replication/v1.0.0/main.tf` — replica bucket, versioning on both buckets, IAM replication role, replication config scoped to artifacts/
- `infra/modules/s3-replication/v1.0.0/variables.tf` — source_bucket_name/arn, destination_region/bucket_name variables
- `infra/modules/s3-replication/v1.0.0/outputs.tf` — replica_bucket_arn, replica_bucket_name, replication_role_arn outputs

## Decisions Made
- Profile YAML stored in S3 at `artifacts/{sandbox-id}/.km-profile.yaml` during create — enables destroy to upload artifacts without requiring the profile path argument
- Email provisioning/cleanup is non-fatal following the same pattern as TTL schedule creation — sandbox remains functional without email
- SES IAM uses `ses:FromAddress` StringEquals condition — scopes each sandbox to sending from its own address only, preventing cross-sandbox abuse
- S3 replication excludes `mail/` prefix — inbox objects are ephemeral; only `artifacts/` is replicated

## Deviations from Plan

None - plan executed exactly as written. The plan specified all steps clearly and the implementation followed the interfaces defined in the plan's `<interfaces>` block.

## Issues Encountered

None. All four new compiler tests (RED) failed as expected before implementation, then passed after implementing the GREEN changes.

## User Setup Required

None - no external service configuration required beyond what Plans 01-03 established.

## Next Phase Readiness
- Phase 04 plans 01-04 are complete: artifact upload, SES helpers, filesystem enforcement, and CLI wiring all done
- Phase 05 can proceed with email integration testing and any remaining hardening
- KM_OPERATOR_EMAIL env var must be set in production for lifecycle notifications to fire
- S3 replication module at `infra/modules/s3-replication/v1.0.0/` is ready to be applied when operators set `replicationRegion` in a profile

---
*Phase: 04-lifecycle-hardening-artifacts-email*
*Completed: 2026-03-22*
