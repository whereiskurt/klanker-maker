---
phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
plan: "06"
subsystem: slack-inbound
tags:
  - sqs
  - km-create
  - iam
  - dynamodb
  - terraform

dependency_graph:
  requires:
    - 67-02  # DynamoDB modules + Config helpers (UpdateSandboxStringAttrDynamo pattern)
    - 67-04  # Bridge Lambda SQS routing (SQSClient interface + adapter precedent)
  provides:
    - per-sandbox SQS FIFO queue at km create time
    - slack_inbound_queue_url in km-sandboxes DDB row
    - KM_SLACK_INBOUND_QUEUE_URL env var in sandbox /etc/profile.d/km-notify-env.sh
    - sandbox EC2 IAM grants 5 SQS actions scoped to own queue ARN
  affects:
    - 67-07  # destroy drain (reads queue URL from DDB to delete at km destroy)
    - 67-08  # sandbox-side poller (relies on KM_SLACK_INBOUND_QUEUE_URL env var)
    - 67-09  # integration tests (verifies last_pause_hint_ts absent after km create)

tech_stack:
  added:
    - github.com/aws/aws-sdk-go-v2/service/sqs v1.42.27 (promoted from indirect to direct)
  patterns:
    - SQSClient interface for mockable SDK wrapping (matches SandboxMetadataAPI pattern)
    - slackInboundDeps struct for dependency injection in tests (matches Phase 63 style)
    - UpdateSandboxStringAttrDynamo for single-attr SET/REMOVE (avoids PutItem overwrite)

key_files:
  created:
    - pkg/aws/sqs.go
    - internal/app/cmd/create_slack_inbound.go
    - internal/app/cmd/create_slack_inbound_test.go (replaced Wave 0 stubs)
  modified:
    - internal/app/cmd/create.go (Step 11e block)
    - pkg/aws/sandbox_dynamo.go (UpdateSandboxStringAttrDynamo)
    - infra/modules/ec2spot/v1.0.0/main.tf (ec2spot_slack_inbound_sqs IAM policy)
    - infra/modules/ec2spot/v1.0.0/variables.tf (resource_prefix variable)
    - go.mod (sqs promoted from indirect)

decisions:
  - "SQS creation is FATAL (not non-fatal): unlike Step 11d Slack env injection, queue provisioning failure aborts km create and archives the per-sandbox Slack channel. Rationale: without the queue the inbound path is permanently broken for this sandbox."
  - "last_pause_hint_ts intentionally absent from km create: DDBPauseHinter (Plan 67-05) treats absent as cooldown-expired, enabling the first paused-message hint to fire immediately. Pre-populating with now() would suppress it."
  - "UpdateSandboxStringAttrDynamo uses REMOVE when value is empty: keeps DDB items clean on rollback, consistent with absent=not-set convention for Phase 67 optional attributes."
  - "Sandbox IAM includes sqs:GetQueueUrl (5 actions not 4): needed by the poller to resolve queue URL from name, consistent with CONTEXT.md spirit even though plan said 4."

metrics:
  duration: "699s"
  completed_date: "2026-05-03"
  tasks_completed: 2
  files_changed: 8
---

# Phase 67 Plan 06: SQS Queue Provisioning at km create — Summary

Per-sandbox SQS FIFO queue lifecycle wired into km create: queue created, URL persisted to DDB, env var injected into sandbox, sandbox EC2 IAM grants read access to own queue only.

## What Was Built

### pkg/aws/sqs.go

New SQS helper module mirroring the `SandboxMetadataAPI` interface pattern from `sandbox_dynamo.go`:

- `SQSClient` interface — subset of `*sqs.Client` for mockability
- `NewSQSClient(ctx, region)` — factory for production use
- `SlackInboundQueueName(prefix, sandboxID)` — Phase 66 prefix-aware naming: `{prefix}-slack-inbound-{id}.fifo`
- `CreateSlackInboundQueue` — idempotent (handles `QueueNameExists` via ListQueues fallback); all CONTEXT.md-mandated attributes: `FifoQueue=true`, `ContentBasedDeduplication=false`, `VisibilityTimeout=30`, `MessageRetentionPeriod=1209600`
- `DeleteSlackInboundQueue` — best-effort, no-op on `QueueDoesNotExist`
- `QueueDepth` — for km status / km doctor backlog display (Plans 67-08/67-09)

### pkg/aws/sandbox_dynamo.go

`UpdateSandboxStringAttrDynamo` — new exported helper for single-attribute SET/REMOVE:
- `SET` when value is non-empty (persists `slack_inbound_queue_url`)
- `REMOVE` when value is empty (cleans up rollback leftovers, preserves absent=not-set semantics)

### internal/app/cmd/create_slack_inbound.go

Three orchestration helpers:

**`provisionSlackInboundQueue(ctx, slackInboundDeps)`** — main entry point:
1. Returns `("", nil)` immediately when `NotifySlackInboundEnabled=false`
2. Creates SQS queue via `CreateSlackInboundQueue`
3. Persists URL to DDB via `UpdateSandboxAttr` callback
4. Injects `KM_SLACK_INBOUND_QUEUE_URL` via `InjectEnvVar` callback (SSM SendCommand)
5. On DDB failure: deletes queue (best-effort rollback), returns error
6. On SSM failure: deletes queue (best-effort rollback), returns error

**Critical invariant documented in code:**
```
// INVARIANT — last_pause_hint_ts is NOT pre-populated:
// Plan 67-05's DDBPauseHinter treats "attribute absent" as
// "cooldown expired — post the first hint immediately."
```

**`rollbackSlackInboundQueue(ctx, deps, queueURL)`** — called by create.go when a subsequent step fails. Always attempts both delete + DDB clear, returns first error.

**`injectSlackInboundQueueURLIntoSandbox(ctx, runner, instanceID, queueURL)`** — concrete SSM inject implementation using idempotent grep/sed pattern matching Phase 63 Step 11d.

### internal/app/cmd/create.go — Step 11e

New step immediately after Step 11d (Slack channel/bridge env injection):
- Only fires when `NotifySlackInboundEnabled=true`
- Reads EC2 instance ID from Terraform outputs (same path as Step 11d)
- Creates SQS client for the sandbox region
- On `provisionSlackInboundQueue` failure: archives per-sandbox Slack channel (mirror Phase 63 rollback) and returns fatal error

### infra/modules/ec2spot/v1.0.0

`aws_iam_role_policy.ec2spot_slack_inbound_sqs` — new IAM policy on sandbox EC2 instance role:
```json
{
  "Sid": "SQSReadOwnInboundQueue",
  "Effect": "Allow",
  "Action": ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes", "sqs:GetQueueUrl", "sqs:ChangeMessageVisibility"],
  "Resource": "arn:aws:sqs:*:{account_id}:{resource_prefix}-slack-inbound-{sandbox_id}.fifo"
}
```

`variable "resource_prefix"` — new variable (default `"km"`) for Phase 66 multi-instance naming.

### Tests (create_slack_inbound_test.go)

Four tests using `fakeSQS` in-memory mock (no real AWS):

| Test | Scenario | Assertions |
|------|----------|------------|
| `TestCreate_SlackInboundQueueProvisioned` | Happy path | 1 CreateQueue, correct FIFO attrs, DDB attr set, SSM env injected |
| `TestCreate_SlackInboundEnvVarInjection` | `inboundEnabled=false` | 0 SQS/DDB/SSM calls, returns ("", nil) |
| `TestCreate_SlackInboundQueueRollback` | SSM inject fails | 1 CreateQueue + 1 DeleteQueue rollback, error returned |
| `TestCreate_SlackInboundDDBPersistFailure` | DDB write fails | 1 CreateQueue + 1 DeleteQueue rollback, error returned |

`TestCreate_SlackInboundReadyAnnouncement` remains skipped (Plan 67-07 scope).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing functionality] Added sqs:GetQueueUrl to sandbox IAM policy**
- **Found during:** Task 1 IAM authoring
- **Issue:** Plan specified 4 SQS actions; the poller (Plan 67-08) uses GetQueueUrl to resolve queue URL from name at runtime
- **Fix:** Added `sqs:GetQueueUrl` to the 4 required actions (5 total)
- **Files modified:** `infra/modules/ec2spot/v1.0.0/main.tf`

**2. [Rule 1 - Bug] Used `Actions` (plural) in Terraform IAM Statement**
- **Found during:** Task 1, terraform fmt/validate
- **Issue:** IAM policy `Statement` block used `Actions` (invalid) instead of `Action` (valid AWS IAM key)
- **Fix:** Renamed to `Action` before commit
- **Files modified:** `infra/modules/ec2spot/v1.0.0/main.tf`

**3. [Rule 3 - Blocking] pkg/aws/sandbox_dynamo.go needed new function**
- **Found during:** Task 1 implementation
- **Issue:** No generic single-attribute DDB update helper existed; create.go needed to persist `slack_inbound_queue_url` without overwriting the full metadata row
- **Fix:** Added `UpdateSandboxStringAttrDynamo` with SET/REMOVE semantics
- **Files modified:** `pkg/aws/sandbox_dynamo.go`

**4. [Rule 3 - Blocking] go.mod SQS indirect → direct**
- **Found during:** Task 1, `go mod tidy`
- **Issue:** `aws-sdk-go-v2/service/sqs` was `// indirect` since pkg/aws/sqs.go now directly imports it
- **Fix:** `go mod tidy` promoted it to a direct dependency automatically
- **Files modified:** `go.mod`

## Self-Check: PASSED

Verified files exist:
- FOUND: pkg/aws/sqs.go
- FOUND: internal/app/cmd/create_slack_inbound.go
- FOUND: internal/app/cmd/create_slack_inbound_test.go
- FOUND commit: b93b257 (Task 1 — implementation)
- FOUND commit: 6814d49 (Task 2 — tests)
- FOUND: create.go has notifySlackInboundEnabled (Step 11e wired)
- FOUND: ec2spot main.tf has slack-inbound IAM policy
- FOUND: last_pause_hint_ts invariant comment in create_slack_inbound.go
