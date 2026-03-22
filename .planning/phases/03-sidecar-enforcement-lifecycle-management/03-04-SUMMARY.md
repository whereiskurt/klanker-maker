---
phase: 03-sidecar-enforcement-lifecycle-management
plan: "04"
subsystem: lifecycle-scheduler-compiler
tags: [eventbridge, scheduler, lifecycle, idle-detection, teardown, ec2, ecs, sidecars, metadata]
dependency_graph:
  requires: [03-01, 03-02, 03-03]
  provides: [TTL schedule creation/deletion, sandbox metadata.json, sidecar injection into EC2 userdata and ECS task, IdleDetector, ExecuteTeardown]
  affects: [internal/app/cmd/create.go, internal/app/cmd/destroy.go, pkg/compiler/userdata.go, pkg/compiler/service_hcl.go]
tech_stack:
  added:
    - github.com/aws/aws-sdk-go-v2/service/scheduler v1.17.21
  patterns:
    - SchedulerAPI narrow interface (same pattern as CWLogsAPI, TagAPI, S3RunAPI)
    - EventBridge at() one-time schedule with ActionAfterCompletion=DELETE
    - IdleDetector with injectable clock (SetNowFn) for test determinism
    - SandboxMetadata written to S3 after apply; read by km list/status
key_files:
  created:
    - pkg/compiler/lifecycle.go
    - pkg/aws/scheduler.go
    - pkg/aws/metadata.go
    - pkg/lifecycle/idle.go
    - pkg/lifecycle/teardown.go
  modified:
    - pkg/aws/scheduler_test.go
    - pkg/lifecycle/idle_test.go
    - pkg/compiler/userdata.go
    - pkg/compiler/service_hcl.go
    - pkg/compiler/compiler.go
    - internal/app/cmd/create.go
    - internal/app/cmd/destroy.go
    - internal/app/config/config.go
    - pkg/aws/sandbox.go
decisions:
  - "SandboxMetadata defined in pkg/aws/metadata.go (not sandbox.go) — sandbox.go had a pre-existing stub reference expecting Plan 03-04 to create it"
  - "DeleteTTLSchedule called BEFORE terragrunt destroy in km destroy — schedule cancelled even if destroy partially fails"
  - "TTL schedule creation is non-fatal in km create — sandbox is provisioned even if EventBridge call fails"
  - "IdleDetector fires OnIdle at most once and returns — no re-fire loop"
  - "KM_ARTIFACTS_BUCKET env var passed to generateUserData from compiler.go (not config struct — avoids breaking existing tests)"
metrics:
  duration: "568s (~9.5min)"
  completed_date: "2026-03-22"
  tasks_completed: 3
  files_modified: 14
requirements_satisfied: [PROV-05, PROV-06, PROV-07]
---

# Phase 03 Plan 04: Lifecycle, Scheduler, and Sidecar Injection Summary

**One-liner:** EventBridge TTL scheduling with at() rules, IdleDetector polling CW logs, and sidecar injection into EC2 user-data (iptables DNAT) and ECS task definitions.

## Tasks Completed

| Task | Description | Commit |
|------|-------------|--------|
| 1a | EventBridge TTL scheduler package (PROV-05) | 19ff15a |
| 1b | Idle lifecycle package (PROV-06, PROV-07) | aff1ea3 |
| 2 | Inject sidecars into EC2 user-data and ECS service.hcl | c560a0e |
| 3 | Wire EventBridge into km create and km destroy | f500a76 |

## What Was Built

### Task 1a: EventBridge TTL Scheduler Package

`pkg/compiler/lifecycle.go` — `BuildTTLScheduleInput(sandboxID, ttlExpiry, lambdaARN, schedulerRoleARN)` produces a `CreateScheduleInput` with `at(YYYY-MM-DDTHH:MM:SS)` expression and `ActionAfterCompletion=DELETE` (self-cleaning rule). Returns nil if `ttlExpiry.IsZero()`.

`pkg/aws/scheduler.go` — `SchedulerAPI` interface over EventBridge Scheduler SDK. `CreateTTLSchedule()` no-ops on nil input. `DeleteTTLSchedule()` is idempotent (ignores ResourceNotFoundException).

`pkg/aws/scheduler_test.go` — 5 tests with `mockSchedulerAPI`: Success, NoTTL, DeleteSuccess, NotFound (idempotent), OtherError (propagated).

### Task 1b: Idle Lifecycle Package

`pkg/lifecycle/idle.go` — `IdleDetector` polls CW `GetLogEvents`, compares most recent event timestamp to `now - IdleTimeout`. `SetNowFn()` exported for test clock injection. Fires `OnIdle(sandboxID)` at most once.

`pkg/lifecycle/teardown.go` — `ExecuteTeardown(ctx, policy, sandboxID, callbacks)` dispatches: destroy → callbacks.Destroy, stop → callbacks.Stop, retain → log at info + nil, unknown → error.

### Task 2: Sidecar Injection into Compiler Outputs

`pkg/compiler/userdata.go` — Added Section 5 (sidecar install: aws s3 cp + systemd units for km-dns-proxy, km-http-proxy, km-audit-log) and Section 6 (iptables DNAT). Critical ordering: IMDS exemption inserted FIRST with `-I OUTPUT`, then DNS UDP+TCP port 53 redirected to :5353, then HTTP/HTTPS ports 80/443 redirected to :3128. All rules exempt `km-sidecar` user to prevent redirect loops. New params: `AllowedDNSSuffixes`, `AllowedHTTPHosts`, `KMArtifactsBucket`.

`pkg/compiler/service_hcl.go` — ECS template updated: main container gains `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` env vars (Fargate has no iptables). Sidecar containers renamed to `km-dns-proxy` (essential=false), `km-http-proxy` (essential=false), `km-audit-log` (essential=true per CONTEXT.md), `km-tracing` (essential=false). Real env vars added (ALLOWED_SUFFIXES, ALLOWED_HOSTS, CW_LOG_GROUP, OTEL_S3_BUCKET). Images reference Terraform variables (`${var.dns_proxy_image}` etc.) for ECR URI injection.

### Task 3: km create and km destroy Wiring

`internal/app/config/config.go` — Added `StateBucket` (KM_STATE_BUCKET), `TTLLambdaARN` (KM_TTL_LAMBDA_ARN), `SchedulerRoleARN` (KM_SCHEDULER_ROLE_ARN) fields.

`internal/app/cmd/create.go` — After successful apply: (1) parse TTL duration from `profile.Spec.Lifecycle.TTL`; (2) write `SandboxMetadata` as JSON to `s3://<StateBucket>/tf-km/sandboxes/<id>/metadata.json` (non-fatal, skipped if StateBucket empty); (3) create EventBridge TTL schedule if TTL set and TTLLambdaARN configured (non-fatal).

`internal/app/cmd/destroy.go` — Before terragrunt destroy: calls `DeleteTTLSchedule(ctx, schedulerClient, sandboxID)`. Logs warn on failure but does not abort destroy.

`pkg/aws/metadata.go` — `SandboxMetadata` struct (moved from sandbox.go stub to its own file as designed).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Duplicate SandboxMetadata declaration**
- **Found during:** Task 3
- **Issue:** `pkg/aws/sandbox.go` already had a stub `SandboxMetadata` type definition referencing Plan 03-04 creating it in `metadata.go`. Creating `metadata.go` caused a redeclaration compile error.
- **Fix:** Removed the stub from `sandbox.go`; `metadata.go` is the canonical definition as planned. The sandbox.go comment ("SandboxMetadata is written by km create (Plan 03-04)") confirms this was the intended split.
- **Files modified:** `pkg/aws/sandbox.go`, `pkg/aws/metadata.go`
- **Commit:** 80e5fbc

**2. [Rule 2 - Missing functionality] TTL field on LifecycleSpec not RuntimeSpec**
- **Found during:** Task 3
- **Issue:** Plan's sample code referenced `profile.Spec.Runtime.TTL` but the TTL field is on `profile.Spec.Lifecycle.TTL` per the actual types.go definition.
- **Fix:** Used the correct field path in create.go.
- **Files modified:** `internal/app/cmd/create.go`

## Self-Check: PASSED

All 5 key files found. All 5 task commits found.
