---
phase: 11-sandbox-auto-destroy-metadata-wiring
verified: 2026-03-23T02:11:02Z
status: passed
score: 9/9 must-haves verified
re_verification: false
---

# Phase 11: Sandbox Auto-Destroy & Metadata Wiring Verification Report

**Phase Goal:** TTL expiry and idle timeout actually destroy sandbox resources instead of just exiting sidecars; km list and km status read metadata from the correct bucket
**Verified:** 2026-03-23T02:11:02Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| #  | Truth                                                                                  | Status     | Evidence                                                                                                  |
|----|----------------------------------------------------------------------------------------|------------|-----------------------------------------------------------------------------------------------------------|
| 1  | TTL handler Lambda calls TeardownFunc after artifact upload and schedule deletion      | VERIFIED   | `cmd/ttl-handler/main.go` Step 6 invokes `h.TeardownFunc(ctx, sandboxID)` after DeleteTTLSchedule        |
| 2  | TTL handler Lambda skips teardown when TeardownFunc is nil (backward compatible)       | VERIFIED   | `if h.TeardownFunc != nil` guard in HandleTTLEvent; nil path tested explicitly                            |
| 3  | TTL handler Lambda returns error when teardown fails                                   | VERIFIED   | Returns `fmt.Errorf("teardown sandbox %s: %w", sandboxID, err)`; tested in TestHandleTTLEvent_TeardownFailureReturnsError |
| 4  | Idle sidecar publishes an EventBridge SandboxIdle event before calling cancel()        | VERIFIED   | `sidecars/audit-log/cmd/main.go` OnIdle callback calls `kmaws.PublishSandboxIdleEvent(ctx, ebClient, id)` then `cancel()` |
| 5  | SandboxIdle event reaches TTL Lambda via EventBridge rule routing                      | VERIFIED   | `infra/modules/ttl-handler/v1.0.0/main.tf` contains `aws_cloudwatch_event_rule.sandbox_idle`, target, and `aws_lambda_permission.eventbridge_events` |
| 6  | km list reads sandbox metadata from cfg.StateBucket, not a hardcoded constant         | VERIFIED   | `internal/app/cmd/list.go` passes `cfg.StateBucket` to `newRealLister`; `defaultStateBucket` constant deleted |
| 7  | km status reads sandbox metadata from cfg.StateBucket, not a hardcoded constant       | VERIFIED   | `internal/app/cmd/status.go` passes `cfg.StateBucket` to `newRealFetcher`                                |
| 8  | km list returns an actionable error when StateBucket is empty                          | VERIFIED   | Returns `"state bucket not configured: set KM_STATE_BUCKET or state_bucket in km-config.yaml"` |
| 9  | km status returns an actionable error when StateBucket is empty                        | VERIFIED   | Same guard and error message in `runStatus`                                                               |

**Score:** 9/9 truths verified

### Required Artifacts

#### Plan 01 Artifacts

| Artifact                              | Expected                                | Status     | Details                                                                                  |
|---------------------------------------|-----------------------------------------|------------|------------------------------------------------------------------------------------------|
| `internal/app/cmd/list.go`            | Config-driven bucket for list command   | VERIFIED   | `cfg.StateBucket` used at line 68; `defaultStateBucket` fully absent from file           |
| `internal/app/cmd/status.go`          | Config-driven bucket for status command | VERIFIED   | `cfg.StateBucket` guard at line 78–79; passed to `newRealFetcher` at line 86             |

#### Plan 02 Artifacts

| Artifact                                              | Expected                                               | Status     | Details                                                                             |
|-------------------------------------------------------|--------------------------------------------------------|------------|-------------------------------------------------------------------------------------|
| `cmd/ttl-handler/main.go`                             | TeardownFunc DI field + Step 6 invocation              | VERIFIED   | `TeardownFunc func(ctx context.Context, sandboxID string) error` field at line 70; invoked at line 128–134; main() wires `DestroySandboxResources` closure |
| `pkg/aws/idle_event.go`                               | PublishSandboxIdleEvent + EventBridgeAPI interface      | VERIFIED   | Both exported; `source: "km.sandbox"`, `detail-type: "SandboxIdle"`, bus: `"default"` |
| `pkg/aws/teardown.go`                                 | DestroySandboxResources via tag discovery + EC2 stop   | VERIFIED   | Calls `FindSandboxByID` then `TerminateSpotInstance`; idempotent on `ErrSandboxNotFound`; ECS ARNs deferred with warning log |
| `sidecars/audit-log/cmd/main.go`                      | OnIdle publishes EventBridge event before cancel()     | VERIFIED   | EventBridge client created when `IDLE_TIMEOUT_MINUTES` set; `PublishSandboxIdleEvent` called before `cancel()` at lines 91–96 |
| `infra/modules/ttl-handler/v1.0.0/main.tf`            | EventBridge rule routing SandboxIdle to TTL Lambda     | VERIFIED   | `aws_cloudwatch_event_rule.sandbox_idle` (line 163), target with input_transformer (line 178), `aws_lambda_permission.eventbridge_events` (line 196) |

### Key Link Verification

#### Plan 01 Key Links

| From                          | To                                     | Via                   | Status     | Details                                                                  |
|-------------------------------|----------------------------------------|-----------------------|------------|--------------------------------------------------------------------------|
| `internal/app/cmd/list.go`    | `internal/app/config/config.go`        | `cfg.StateBucket`     | WIRED      | `cfg.StateBucket` appears at lines 60 and 68; passed through RunE closure |
| `internal/app/cmd/status.go`  | `internal/app/config/config.go`        | `cfg.StateBucket`     | WIRED      | `cfg.StateBucket` appears at lines 78 and 86                              |

#### Plan 02 Key Links

| From                              | To                             | Via                                    | Status     | Details                                                                                    |
|-----------------------------------|--------------------------------|----------------------------------------|------------|--------------------------------------------------------------------------------------------|
| `cmd/ttl-handler/main.go`         | `TTLHandler.TeardownFunc`      | DI callback after Step 5               | WIRED      | `if h.TeardownFunc != nil { h.TeardownFunc(ctx, sandboxID) }` at line 128                 |
| `cmd/ttl-handler/main.go`         | `pkg/aws/teardown.go`          | `DestroySandboxResources` in closure    | WIRED      | `main()` creates tagClient+ec2Client; `TeardownFunc` closure calls `awspkg.DestroySandboxResources` at line 193 |
| `sidecars/audit-log/cmd/main.go`  | `pkg/aws/idle_event.go`        | `PublishSandboxIdleEvent` in OnIdle     | WIRED      | `kmaws.PublishSandboxIdleEvent(ctx, ebClient, id)` at line 92                              |
| `infra/modules/ttl-handler/v1.0.0/main.tf` | `aws_lambda_function.ttl_handler` | EventBridge rule routes SandboxIdle events | WIRED | `aws_cloudwatch_event_target.idle_to_ttl` targets `aws_lambda_function.ttl_handler.arn`  |
| `infra/modules/ec2spot/v1.0.0/main.tf`    | EventBridge default bus         | `events:PutEvents` IAM permission       | WIRED      | `aws_iam_role_policy.ec2spot_eventbridge` granting `events:PutEvents` at line 247          |

### Requirements Coverage

| Requirement | Source Plan | Description                                                       | Status    | Evidence                                                                                         |
|-------------|-------------|-------------------------------------------------------------------|-----------|--------------------------------------------------------------------------------------------------|
| PROV-03     | 11-01       | Operator can run `km list` to see all running sandboxes with status | SATISFIED | `list.go` reads from `cfg.StateBucket`; 7 tests pass including `TestListCmd_EmptyStateBucketError` and `TestListCmd_RealBucketFromConfig` |
| PROV-04     | 11-01       | Operator can run `km status <sandbox-id>` to see detailed state   | SATISFIED | `status.go` reads from `cfg.StateBucket`; `TestStatusCmd_EmptyStateBucketError` and `TestStatusCmd_RealBucketFromConfig` pass |
| PROV-05     | 11-02       | Sandbox auto-destroys after TTL expires                           | SATISFIED | TTLHandler.TeardownFunc calls `DestroySandboxResources`; `TestHandleTTLEvent_CallsTeardownFunc` and `TestDestroySandboxResources_EC2` pass |
| PROV-06     | 11-02       | Sandbox auto-destroys after idle timeout                          | SATISFIED | Idle sidecar publishes `SandboxIdle` to EventBridge; `aws_cloudwatch_event_rule.sandbox_idle` routes it to TTL Lambda; `TestPublishSandboxIdleEvent` passes |

**Note on PROV-06 dual traceability:** REQUIREMENTS.md shows PROV-06 mapped to both Phase 7 (IdleDetector wired into sidecar) and Phase 11 (idle events route to actual teardown Lambda). Phase 11 closes the remaining gap: Phase 7 wired the detector into the sidecar but the sidecar only called `cancel()`. Phase 11 adds the EventBridge publish and Lambda routing so idle detection now results in resource destruction.

**Note on ECS idle teardown:** `teardown.go` logs a warning and skips ECS task ARNs (`:task/` pattern). EC2 teardown is fully implemented. ECS teardown via Lambda is explicitly deferred to Phase 12 per plan decision. PROV-06 is considered satisfied for the EC2 substrate; ECS teardown is a Phase 12 enhancement.

### Anti-Patterns Found

No blockers or warnings found.

| File                               | Line | Pattern                 | Severity | Impact |
|------------------------------------|------|-------------------------|----------|--------|
| `pkg/aws/teardown.go`              | 43   | ECS teardown skipped    | INFO     | Phase 12 planned enhancement; EC2 path fully implemented; not a blocker for v1 EC2 sandboxes |

The ECS skip is documented in the plan decision log, logs a `Warn`-level message, and is an explicit v1 scope boundary — not an accidental omission.

### Human Verification Required

The following behaviors require real AWS infrastructure to confirm end-to-end:

#### 1. TTL Lambda Actually Destroys EC2 on Expiry

**Test:** Create an EC2 sandbox with a 2-minute TTL. Let the EventBridge schedule fire. Inspect the sandbox EC2 instance state in the AWS console after Lambda execution.
**Expected:** EC2 instance is in `terminated` state. Lambda CloudWatch logs show "sandbox resources destroyed".
**Why human:** Unit tests mock `TerminateInstances`; real destruction requires actual EC2 instance + Lambda execution role permissions.

#### 2. Idle Sidecar EventBridge Publish Reaches TTL Lambda

**Test:** Run the audit-log sidecar with `IDLE_TIMEOUT_MINUTES=1` and a CloudWatch-backed destination in a real sandbox. Wait for idle timeout. Check EventBridge CloudWatch metrics and Lambda invocation logs.
**Expected:** EventBridge receives a `SandboxIdle` event from source `km.sandbox`; TTL Lambda is invoked with `event_type: "idle"` payload; EC2 instance is terminated.
**Why human:** End-to-end EventBridge routing (rule → target → Lambda invoke → EC2 terminate) requires deployed AWS infrastructure.

#### 3. km list and km status Read from Correct Bucket

**Test:** Configure `KM_STATE_BUCKET=<real-bucket>` where `km create` wrote sandbox metadata. Run `km list` and `km status <sandbox-id>`.
**Expected:** Both commands return accurate sandbox data from the configured bucket without errors.
**Why human:** Real S3 read requires a provisioned bucket with actual sandbox metadata objects.

### Gaps Summary

No gaps. All must-haves from both Plan 01 and Plan 02 frontmatter are satisfied.

**Plan 01 (PROV-03, PROV-04):** The `defaultStateBucket` constant `"tf-km-state"` is fully deleted from the codebase. Both `list.go` and `status.go` use `cfg.StateBucket` exclusively, with empty-bucket guards that return actionable error messages. All 12 list/status tests pass including the 4 new tests for empty-bucket and config-threading paths.

**Plan 02 (PROV-05, PROV-06):** The TTL Lambda now completes a full 6-step lifecycle: validate → profile download → artifact upload → SES notification → schedule deletion → **resource teardown**. The teardown uses AWS SDK (tag discovery + EC2 terminate), not a subprocess, which is correct for the Lambda runtime. The idle path is fully wired: sidecar publishes EventBridge event → EventBridge rule routes to Lambda → Lambda runs Step 6 teardown. IAM permissions are present on the Lambda role (tag:GetResources, ec2:TerminateInstances) and sandbox roles (events:PutEvents on both ec2spot and ecs-task modules).

All 3 referenced git commits (`e8a63e8`, `6fdf7d1`, `c711f7a`) exist and their diffs match the claimed changes. All tests pass. `go vet` passes on all modified packages. Sidecar binary compiles successfully.

---

_Verified: 2026-03-23T02:11:02Z_
_Verifier: Claude (gsd-verifier)_
