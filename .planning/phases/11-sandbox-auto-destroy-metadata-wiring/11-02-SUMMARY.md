---
phase: 11-sandbox-auto-destroy-metadata-wiring
plan: 02
subsystem: lifecycle
tags: [ttl, idle-destroy, eventbridge, teardown, lambda, iam]
dependency_graph:
  requires: []
  provides: [TTLHandler.TeardownFunc, DestroySandboxResources, PublishSandboxIdleEvent, EventBridgeRule.SandboxIdle]
  affects: [cmd/ttl-handler, pkg/aws, sidecars/audit-log, infra/modules/ttl-handler, infra/modules/ec2spot, infra/modules/ecs-task]
tech_stack:
  added: [github.com/aws/aws-sdk-go-v2/service/eventbridge v1.45.22]
  patterns: [DI-callback teardown, narrow-interface EventBridgeAPI, EventBridge input_transformer]
key_files:
  created:
    - pkg/aws/teardown.go
    - pkg/aws/teardown_test.go
    - pkg/aws/idle_event.go
    - pkg/aws/idle_event_test.go
  modified:
    - cmd/ttl-handler/main.go
    - cmd/ttl-handler/main_test.go
    - sidecars/audit-log/cmd/main.go
    - infra/modules/ttl-handler/v1.0.0/main.tf
    - infra/modules/ec2spot/v1.0.0/main.tf
    - infra/modules/ecs-task/v1.0.0/main.tf
    - go.mod
    - go.sum
decisions:
  - "TeardownFunc uses func(ctx, sandboxID) error signature — two params only; closure captures AWS clients from main() to keep DI interface simple"
  - "DestroySandboxResources uses AWS SDK (not terragrunt subprocess) — Lambda runtime has no km binary"
  - "ECS task teardown skipped in v1 with warning log — Phase 12 enhancement; EC2 is primary substrate"
  - "EventBridge publish in sidecar is best-effort — failure logged but sidecar still exits via cancel()"
  - "events:PutEvents added to ECS execution role (module manages execution role, not task role) — compiler must also ensure task_role_arn has events:PutEvents"
metrics:
  duration: 406s
  completed: 2026-03-22
  tasks_completed: 2
  files_changed: 12
requirements_satisfied: [PROV-05, PROV-06]
---

# Phase 11 Plan 02: TTL/Idle Auto-Destroy Wiring Summary

**One-liner:** TTL Lambda calls TeardownFunc DI callback (DestroySandboxResources via AWS SDK) after TTL/idle expiry; idle sidecar publishes SandboxIdle to EventBridge rule routing to TTL Lambda.

## What Was Built

### Task 1: TeardownFunc DI + DestroySandboxResources + PublishSandboxIdleEvent (TDD)

**pkg/aws/teardown.go** — `DestroySandboxResources(ctx, tagClient, ec2Client, sandboxID)` discovers EC2 instances via tag query (`km:sandbox-id=<id>`) and terminates them. Idempotent: returns nil on ErrSandboxNotFound. ECS tasks logged as warning and skipped (Phase 12).

**pkg/aws/idle_event.go** — `PublishSandboxIdleEvent(ctx, client, sandboxID)` + narrow `EventBridgeAPI` interface. Publishes `{source: "km.sandbox", detail-type: "SandboxIdle", detail: {sandbox_id, event_type: "idle"}}` to the default event bus.

**cmd/ttl-handler/main.go** — Added `EventType` field to TTLEvent, `TeardownFunc func(ctx, sandboxID) error` field to TTLHandler (nil-guarded for backward compat), Step 6 teardown invocation after schedule deletion, and main() wiring with `DestroySandboxResources` closure capturing tag+EC2 clients.

### Task 2: EventBridge Wiring + IAM Permissions

**sidecars/audit-log/cmd/main.go** — OnIdle callback now creates an EventBridge client when IDLE_TIMEOUT_MINUTES is set, publishes SandboxIdle event before calling `cancel()`. Best-effort: publish failure is logged but sidecar still exits.

**infra/modules/ttl-handler/v1.0.0/main.tf** — Added:
- `aws_cloudwatch_event_rule.sandbox_idle` routing SandboxIdle events from `km.sandbox` source
- `aws_cloudwatch_event_target.idle_to_ttl` with input_transformer converting EventBridge envelope to `{sandbox_id, event_type: "idle"}` TTLEvent shape
- `aws_lambda_permission.eventbridge_events` allowing `events.amazonaws.com` to invoke the Lambda
- `aws_iam_role_policy.tag_discovery` — `tag:GetResources`
- `aws_iam_role_policy.ec2_teardown` — `ec2:TerminateInstances`, `ec2:DescribeInstances`

**infra/modules/ec2spot/v1.0.0/main.tf** — Added `aws_iam_role_policy.ec2spot_eventbridge` granting `events:PutEvents` to the sandbox EC2 IAM role.

**infra/modules/ecs-task/v1.0.0/main.tf** — Added `aws_iam_role_policy.ecs_task_eventbridge` granting `events:PutEvents` to the ECS execution role.

## Test Results

All 14 new tests pass:

| Test | Package | Result |
|------|---------|--------|
| TestHandleTTLEvent_CallsTeardownFunc | cmd/ttl-handler | PASS |
| TestHandleTTLEvent_NoTeardownWhenNil | cmd/ttl-handler | PASS |
| TestHandleTTLEvent_TeardownFailureReturnsError | cmd/ttl-handler | PASS |
| TestPublishSandboxIdleEvent | pkg/aws | PASS |
| TestPublishSandboxIdleEvent_Error | pkg/aws | PASS |
| TestDestroySandboxResources_EC2 | pkg/aws | PASS |
| TestDestroySandboxResources_NoResources | pkg/aws | PASS |
| TestDestroySandboxResources_TagAPIError | pkg/aws | PASS |
| All 6 pre-existing TTL handler tests | cmd/ttl-handler | PASS |

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

All files verified present. All commits verified:
- 6fdf7d1: feat(11-02): add TeardownFunc DI to TTLHandler + DestroySandboxResources + PublishSandboxIdleEvent
- c711f7a: feat(11-02): wire idle EventBridge publish, add EventBridge rule routing + IAM permissions
