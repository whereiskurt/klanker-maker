---
phase: 04-lifecycle-hardening-artifacts-email
plan: "05"
subsystem: infra
tags: [lambda, go, terraform, ses, eventbridge, lifecycle, notifications, ttl, artifacts]

requires:
  - phase: 04-lifecycle-hardening-artifacts-email
    provides: SES SendLifecycleNotification, UploadArtifacts, pkg/aws interfaces (SESV2API, S3PutAPI, SchedulerAPI)
  - phase: 04-lifecycle-hardening-artifacts-email
    provides: TeardownCallbacks struct, ExecuteTeardown, IdleDetector — all from prior plans
provides:
  - TTL handler Lambda (Go binary + Terraform module at infra/modules/ttl-handler/v1.0.0)
  - TeardownCallbacks.OnNotify callback wired in ExecuteTeardown for success and error paths
  - IdleDetector.OnIdleNotify callback for idle-timeout notification
  - destroy.go OnNotify wired with SendLifecycleNotification (removes duplicate notification block)
affects:
  - phase-05-any (uses TeardownCallbacks, should set OnNotify)
  - infra-live (deploys ttl-handler module to wire TTL scheduler Lambda ARN)

tech-stack:
  added:
    - github.com/aws/aws-lambda-go v1.53.0
  patterns:
    - TTLHandler struct with injected S3GetPutAPI + SESV2API + SchedulerAPI for Lambda testability
    - OnNotify/OnIdleNotify as optional callbacks — nil-safe, best-effort, backward-compatible
    - Go Lambda: provided.al2023 runtime, arm64 architecture, bootstrap binary name

key-files:
  created:
    - cmd/ttl-handler/main.go
    - cmd/ttl-handler/main_test.go
    - infra/modules/ttl-handler/v1.0.0/main.tf
    - infra/modules/ttl-handler/v1.0.0/variables.tf
    - infra/modules/ttl-handler/v1.0.0/outputs.tf
  modified:
    - pkg/lifecycle/teardown.go
    - pkg/lifecycle/teardown_test.go
    - pkg/lifecycle/idle.go
    - pkg/lifecycle/idle_test.go
    - internal/app/cmd/destroy.go
    - go.mod
    - go.sum

key-decisions:
  - "TTL Lambda is focused on artifact upload + notification + schedule self-cleanup only; actual terragrunt destroy is out of scope (Lambda context constraint)"
  - "Profile download failure in TTL handler is non-fatal: artifact upload skipped with warning, notification and schedule cleanup still proceed"
  - "OnNotify uses past-tense event names (destroyed/stopped/retained/error) matching ses.go comment convention"
  - "OnIdleNotify is separate from OnIdle: teardown action and notification are decoupled concerns"
  - "destroy.go OnNotify replaces the duplicate explicit Step 11 notification block, ensuring error paths also get notifications"

patterns-established:
  - "Lifecycle notification callbacks are optional (nil-safe) and best-effort (errors logged, not propagated)"
  - "TTLHandler DI struct pattern mirrors other pkg/aws consumer patterns (injected interfaces for testability)"

requirements-completed:
  - OBSV-04
  - OBSV-05
  - OBSV-06
  - OBSV-07
  - PROV-13
  - MAIL-01
  - MAIL-02
  - MAIL-03
  - MAIL-04
  - MAIL-05

duration: 5min
completed: 2026-03-22
---

# Phase 04 Plan 05: Gap Closure — TTL Lambda and Lifecycle Notification Wiring Summary

**Go Lambda for TTL artifact upload (cmd/ttl-handler) and OnNotify/OnIdleNotify callbacks closing idle-timeout and error notification gaps**

## Performance

- **Duration:** ~5 min
- **Started:** 2026-03-22T14:26:31Z
- **Completed:** 2026-03-22T14:31:13Z
- **Tasks:** 2
- **Files modified:** 11 (5 created, 6 modified)

## Accomplishments

- TTL handler Lambda: Go binary at `cmd/ttl-handler/main.go` with dependency-injected struct; downloads profile from S3, uploads artifacts, sends "ttl-expired" SES notification, deletes EventBridge schedule; 6 tests pass
- Terraform module at `infra/modules/ttl-handler/v1.0.0`: provided.al2023 arm64 Lambda, IAM policies for S3/SES/scheduler/CloudWatch, outputs `lambda_function_arn` for `KM_TTL_LAMBDA_ARN`
- `TeardownCallbacks.OnNotify` added to `ExecuteTeardown`: called with "destroyed"/"stopped"/"retained" on success, "error" on callback failure; nil-safe, best-effort
- `IdleDetector.OnIdleNotify` added: fired after `OnIdle` when idle detected; nil-safe, best-effort
- `destroy.go` OnNotify wired with `SendLifecycleNotification`; duplicate explicit Step 11 notification block removed (OnNotify covers both success and error paths)

## Task Commits

1. **Task 1: TTL handler Lambda — Go binary + Terraform module** - `7d7bce9` (feat)
2. **Task 2: Wire lifecycle notifications for idle-timeout and error/crash paths** - `b928fb2` (feat)

## Files Created/Modified

- `cmd/ttl-handler/main.go` — TTLHandler struct + HandleTTLEvent Lambda handler
- `cmd/ttl-handler/main_test.go` — 6 tests with mocks for S3/SES/Scheduler
- `infra/modules/ttl-handler/v1.0.0/main.tf` — Lambda function + IAM roles + CW log group
- `infra/modules/ttl-handler/v1.0.0/variables.tf` — artifact_bucket_name/arn, email_domain, operator_email, lambda_zip_path
- `infra/modules/ttl-handler/v1.0.0/outputs.tf` — lambda_function_arn, lambda_function_name, lambda_role_arn
- `pkg/lifecycle/teardown.go` — OnNotify field added to TeardownCallbacks; ExecuteTeardown wired
- `pkg/lifecycle/teardown_test.go` — 4 new OnNotify tests
- `pkg/lifecycle/idle.go` — OnIdleNotify field added to IdleDetector; Run() wired
- `pkg/lifecycle/idle_test.go` — 2 new OnIdleNotify tests
- `internal/app/cmd/destroy.go` — OnNotify wired, duplicate notification block removed
- `go.mod` / `go.sum` — github.com/aws/aws-lambda-go v1.53.0 added

## Decisions Made

- TTL Lambda scope is artifact upload + notification + schedule self-cleanup only. Actual `terragrunt destroy` is out of scope: Lambda runs in AWS without a bundled `km` binary or Terragrunt installation. This is the correct gap closure — the missing piece was artifact preservation before TTL expiry, not a duplicate destroy path.
- Profile download failure is non-fatal in the TTL handler. If the profile was never stored or S3 is temporarily unreachable, the handler still sends the notification and cleans up the schedule (primary value preserved).
- OnNotify callback uses past-tense event names matching the `ses.go` comment convention: "destroyed", "stopped", "retained", "error".

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Phase 04 gap closure complete: all VERIFICATION.md gaps addressed
- TTL Lambda ready to deploy; requires `infra/live/use1/ttl-handler/` live module configuration and `KM_TTL_LAMBDA_ARN` set in create flow
- OnNotify callbacks available for all future callers of `ExecuteTeardown` — operators receive error notifications automatically

---
*Phase: 04-lifecycle-hardening-artifacts-email*
*Completed: 2026-03-22*
