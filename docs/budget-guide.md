# Klanker Maker Budget Guide

Phase 6 budget enforcement for per-sandbox compute and AI spend control.

## Table of Contents

- [Overview](#overview)
- [Profile Configuration](#profile-configuration)
- [DynamoDB Schema](#dynamodb-schema)
- [Budget-Enforcer Lambda Architecture](#budget-enforcer-lambda-architecture)
- [Compute Metering](#compute-metering)
- [Compute Budget Enforcement Details](#compute-budget-enforcement-details)
- [AI Token Metering](#ai-token-metering)
- [AI Budget Dual-Layer Enforcement](#ai-budget-dual-layer-enforcement)
- [Warning Flow](#warning-flow)
- [Enforcement Flow](#enforcement-flow)
- [Top-Up](#top-up)
- [Top-Up Flow Details](#top-up-flow-details)
- [Per-Model AI Breakdown](#per-model-ai-breakdown)
- [km status Budget View](#km-status-budget-view)
- [Cost Examples](#cost-examples)
- [Global Table Replication](#global-table-replication)

---

## Overview

Every sandbox has two independent spend pools:

| Pool | What it tracks | Source |
|------|---------------|--------|
| **Compute** | EC2 spot instance time, ECS Fargate vCPU/memory time | Spot rate x elapsed minutes |
| **AI** | Bedrock InvokeModel calls (Haiku, Sonnet, Opus) | Input/output tokens x model rate |

Both pools are tracked in a DynamoDB global table that replicates to every region where agents run. Budget limits are set in the sandbox profile YAML and written to DynamoDB at sandbox creation time.

Each pool operates independently: a sandbox can exhaust its AI budget while compute keeps running, or vice versa. Enforcement is per-pool -- hitting 100% on AI blocks Bedrock calls but does not stop the instance, and hitting 100% on compute suspends the instance but does not block remaining AI budget.

## Profile Configuration

Budget is configured under `spec.budget` in a SandboxProfile:

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: dev-with-budget
  labels:
    tier: development

spec:
  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.medium
    region: us-east-1

  budget:
    compute:
      maxSpendUSD: 2.00
    ai:
      maxSpendUSD: 5.00
    warningThreshold: 0.80
```

### Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `spec.budget.compute.maxSpendUSD` | float | Yes | -- | Maximum USD for compute resources |
| `spec.budget.ai.maxSpendUSD` | float | Yes | -- | Maximum USD for Bedrock AI calls |
| `spec.budget.warningThreshold` | float | No | `0.80` | Fraction (0-1) at which a warning email fires |

### Minimal Budget Profile

If you only want to cap AI spend and let compute run unbounded for the TTL:

```yaml
spec:
  budget:
    ai:
      maxSpendUSD: 10.00
```

When `compute.maxSpendUSD` is omitted, compute runs until the sandbox TTL expires or the operator destroys it. When `ai.maxSpendUSD` is omitted, AI calls are unlimited.

### Tight Budget for Exploratory Work

```yaml
spec:
  lifecycle:
    ttl: "2h"
  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.small
    region: us-east-1
  budget:
    compute:
      maxSpendUSD: 0.50
    ai:
      maxSpendUSD: 1.00
    warningThreshold: 0.90
```

This creates a short-lived sandbox where the operator gets a warning at 90% spend instead of the default 80%.

## DynamoDB Schema

Budget records live in a dedicated DynamoDB table (`km-budgets`), separate from the sandbox metadata table.

### Table Configuration

| Property | Value |
|----------|-------|
| Table name | `km-budgets` |
| Billing mode | PAY_PER_REQUEST |
| Replication | Global table, replicated to all active regions |
| Partition key | `PK` (String) |
| Sort key | `SK` (String) |
| TTL attribute | `expiresAt` |

### Multi-Row Budget Design

For a sandbox `sb-7f3a9e12`, budget data is stored across multiple sort key rows under a single partition key:

- **PK**: `SANDBOX#sb-7f3a9e12`
- **SK**: `BUDGET#compute` -- compute spend row
- **SK**: `BUDGET#ai#{modelID}` -- per-model AI spend rows
- **SK**: `BUDGET#limits` -- budget limits configuration row
- **SK**: `BUDGET#meta` -- warning notification state

#### Limits row (`BUDGET#limits`)

```json
{
  "PK": { "S": "SANDBOX#sb-7f3a9e12" },
  "SK": { "S": "BUDGET#limits" },
  "computeLimit": { "N": "2.00" },
  "aiLimit": { "N": "5.00" },
  "warningThreshold": { "N": "0.80" }
}
```

#### Compute spend row (`BUDGET#compute`)

```json
{
  "PK": { "S": "SANDBOX#sb-7f3a9e12" },
  "SK": { "S": "BUDGET#compute" },
  "spentUSD": { "N": "0.47" }
}
```

#### Per-model AI spend row (`BUDGET#ai#{modelID}`)

```json
{
  "PK": { "S": "SANDBOX#sb-7f3a9e12" },
  "SK": { "S": "BUDGET#ai#anthropic.claude-sonnet-4-20250514-v1:0" },
  "spentUSD": { "N": "1.00" },
  "inputTokens": { "N": "287400" },
  "outputTokens": { "N": "53100" },
  "last_updated": { "S": "2026-03-22T15:12:47Z" }
}
```

#### Warning meta row (`BUDGET#meta`)

```json
{
  "PK": { "S": "SANDBOX#sb-7f3a9e12" },
  "SK": { "S": "BUDGET#meta" },
  "warningNotified": { "BOOL": true }
}
```

### Key Design Decisions

**Separate table**: Budget data lives in its own `km-budgets` table, not in the sandbox metadata table. The `GetBudget` function uses a `Query` with `begins_with(SK, "BUDGET#")` to read all budget rows for a sandbox in a single call.

**TTL cleanup**: `expiresAt` is set to the sandbox teardown time plus a 30-day retention window. After the sandbox is destroyed, DynamoDB automatically removes the budget records. This prevents table bloat without requiring a cleanup Lambda.

**Atomic increments**: AI spend updates use `UpdateExpression` with `ADD` to atomically increment `spentUSD` and per-model token counts. No read-modify-write races. Compute spend uses `SET` with a pre-calculated absolute value (see Compute Spend Calculation below).

## Budget-Enforcer Lambda Architecture

Every sandbox with a compute budget gets its own dedicated Lambda function named `km-budget-enforcer-{sandbox-id}`. This per-sandbox isolation model means each Lambda has IAM permissions scoped strictly to its sandbox's resources.

### Lambda Runtime

The budget enforcer is a Go binary compiled for `provided.al2023` on `arm64` (Graviton), deployed to AWS Lambda. It has a 60-second timeout and 128 MB memory allocation. A separate CloudWatch Log Group (`/aws/lambda/km-budget-enforcer-{sandbox-id}`) retains execution logs for 30 days.

### EventBridge Scheduler Trigger

An EventBridge Scheduler schedule (`km-budget-{sandbox-id}`) triggers the Lambda every minute with `schedule_expression = "rate(1 minute)"`. The schedule uses `flexible_time_window { mode = "OFF" }` for precise timing. It runs continuously until explicitly deleted at sandbox destruction.

The scheduler passes a JSON payload to each Lambda invocation at sandbox creation time. This payload contains everything the Lambda needs:

```json
{
  "sandbox_id":     "sb-7f3a9e12",
  "instance_type":  "t3.medium",
  "spot_rate":      0.0104,
  "substrate":      "ec2",
  "created_at":     "2026-03-22T14:30:00Z",
  "role_arn":       "arn:aws:iam::123456789012:role/km-ec2spot-ssm-sb-7f3a9e12-use1",
  "instance_id":    "i-0abc123def456",
  "task_arn":       "",
  "cluster_arn":    "",
  "operator_email": "operator@example.com"
}
```

The `spot_rate` is embedded in the payload at sandbox creation time from a static lookup table (`staticSpotRate()` in `spot_rate.go`). It is not re-fetched on each Lambda invocation, so spot price fluctuations after sandbox creation do not affect budget calculations. The payload is immutable for the lifetime of the sandbox.

### Lambda IAM Scope

The Lambda role (`km-budget-enforcer-{sandbox-id}`) has nine IAM policies:

| Policy | Permissions |
|--------|-------------|
| CloudWatch Logs | `logs:CreateLogGroup`, `logs:CreateLogStream`, `logs:PutLogEvents` |
| DynamoDB (budget) | `GetItem`, `UpdateItem`, `Query` on the budget table (and its indexes) |
| EC2 control | `StopInstances`, `StartInstances`, `DescribeInstances` -- tag-condition: `km:sandbox_id = {sandbox-id}` |
| ECS control | `StopTask`, `DescribeTasks` |
| IAM policy control | `AttachRolePolicy`, `DetachRolePolicy`, `ListAttachedRolePolicies` on the sandbox role ARN |
| SES | `SendEmail`, `SendRawEmail` for budget notification emails |
| DynamoDB (sandbox metadata) | `GetItem`, `UpdateItem`, `PutItem` on the sandbox metadata table (lock check, status update) |
| EventBridge Scheduler | `scheduler:DeleteSchedule` for TTL schedule cleanup on budget enforcement |
| S3 profiles | `s3:GetObject` on `artifacts/{sandbox-id}/*` for reading stored sandbox profiles (hibernation check, ECS teardown) |

The EC2 condition (`StringEquals: aws:ResourceTag/km:sandbox_id`) ensures the Lambda can only stop/start instances belonging to its specific sandbox.

### Compute Spend Calculation

The Lambda reads `spot_rate` and `created_at` from the EventBridge event payload (not from DynamoDB). It calculates absolute cost from the sandbox creation timestamp:

```
elapsed_hours = (now - created_at) / 3600
absolute_cost = spot_rate * elapsed_hours
```

It then writes using `SET spentUSD = :cost` (not `ADD`). This is idempotent: each invocation recalculates from the beginning rather than adding an increment. If the Lambda is retried or runs late, the spend value converges to the correct absolute amount instead of accumulating errors.

---

## Compute Metering

Compute spend is tracked as the sandbox's spot rate multiplied by elapsed minutes, billed per-minute.

### Rate Sourcing

At sandbox creation, `km create` looks up the hourly rate from a static fallback table (`staticSpotRate()` in `spot_rate.go`). The static table contains approximate us-east-1 spot prices for common instance types. The AWS Price List API (`GetProducts`) can also be queried, but it returns on-demand pricing as an approximation -- actual spot prices would require `DescribeSpotPriceHistory`.

The rate is embedded in the EventBridge Scheduler payload as `spot_rate` at sandbox creation time. It is not stored in the DynamoDB budget record and is not re-fetched on each Lambda invocation, so price fluctuations after creation do not affect budget calculations.

### Periodic Polling

An EventBridge-triggered Lambda runs every 60 seconds for each active sandbox:

1. Reads `spot_rate` and `created_at` from the EventBridge event payload (not from DynamoDB).
2. Calculates absolute cost from creation: `spot_rate * (elapsed_minutes / 60)`.
3. Writes the compute spend to DynamoDB using `SET spentUSD = :cost` (idempotent, not incremental).
4. Reads the full `BudgetSummary` from DynamoDB and checks thresholds for warning/enforcement.

### Rate Differences

Spot rates vary by instance type and region. Examples at time of writing:

| Instance Type | us-east-1 | us-west-2 | eu-west-1 |
|--------------|-----------|-----------|-----------|
| t3.small | ~$0.0052/hr | ~$0.0052/hr | ~$0.0058/hr |
| t3.medium | ~$0.0104/hr | ~$0.0104/hr | ~$0.0116/hr |
| t3.large | ~$0.0208/hr | ~$0.0208/hr | ~$0.0232/hr |
| m5.large | ~$0.0350/hr | ~$0.0270/hr | ~$0.0290/hr |

These rates come from a static fallback table and are approximate -- the table above is for estimation only.

## Compute Budget Enforcement Details

When the Lambda's compute spend calculation shows `compute_spend >= compute_limit`, it immediately suspends the sandbox. The exact behavior differs by substrate.

### EC2 Substrate: Suspend-and-Resume

For EC2-based sandboxes, the Lambda performs several checks before stopping the instance:

- **Lock check.** The Lambda reads sandbox metadata from DynamoDB and checks if the sandbox is locked (`km lock`). If locked, compute budget enforcement is skipped entirely -- the sandbox continues running regardless of spend.
- **Hibernation support.** The Lambda downloads the sandbox profile from S3 and checks `spec.runtime.hibernation`. If hibernation is enabled, it calls `StopInstances` with `Hibernate: true` first, which preserves in-memory state. If hibernation fails (e.g., instance type does not support it), it falls back to a plain stop.
- **EBS volumes are preserved.** All data written to the EBS root volume and any attached volumes survives the stop. The agent's filesystem state is intact.
- **Compute charges stop immediately.** Spot instance billing stops when the instance transitions to `stopped` state. The sandbox incurs no further compute cost.
- **DynamoDB status update.** The Lambda updates the sandbox status in DynamoDB to `paused` (if hibernated) or `stopped`.
- **TTL schedule cleanup.** The Lambda deletes the sandbox TTL schedule to prevent a stopped sandbox from being destroyed on TTL expiry.
- **Instance is resumable.** When the operator adds budget, `km budget add` calls `ec2:StartInstances`. The instance boots from the same EBS state, resumes processes, and continues from where it stopped. This is a suspend-and-resume model, not a teardown.

### ECS Substrate: Stop-and-Reprovision

For ECS Fargate sandboxes, tasks are ephemeral -- there is no persistent EBS volume. The Lambda:

1. Stops the ECS task via `ecs:StopTask`. (Artifact upload before stop is planned but not yet implemented -- currently handled by the TTL handler path.)

When the operator adds budget, re-provisioning creates a new Fargate task from the sandbox profile stored in S3 at `artifacts/{sandbox-id}/.km-profile.yaml`. The workspace is restored from the artifact tarball. This is a stop-and-reprovision model: each resume starts a fresh task container from the preserved workspace snapshot.

### Key Difference

| Aspect | EC2 (suspend) | ECS (stop-and-reprovision) |
|--------|---------------|---------------------------|
| Data preservation | EBS volume in-place | S3 artifact tarball |
| Resume time | ~30 seconds (instance start) | ~2-3 minutes (task provisioning) |
| Memory state | Lost (OS shutdown) | Lost (container stop) |
| Filesystem state | Fully preserved | Preserved via artifact upload |

---

## AI Token Metering

The HTTP proxy sidecar intercepts every Bedrock `InvokeModel` response to extract token usage and price it in real time.

### Interception Flow

```
Agent  ──>  HTTP Proxy Sidecar  ──>  Bedrock API
                  │
                  ├── Forward request to Bedrock
                  ├── Read response body
                  ├── Extract usage.input_tokens, usage.output_tokens
                  ├── Look up model rate (cached)
                  ├── Atomic DynamoDB increment
                  └── Return response to agent (unmodified)
```

### Response Parsing

Bedrock `InvokeModel` responses for Anthropic models include a `usage` block:

```json
{
  "id": "msg_01XFDUDYJgAACzvnptvVoYEL",
  "type": "message",
  "role": "assistant",
  "content": [{"type": "text", "text": "Hello!"}],
  "usage": {
    "input_tokens": 42,
    "output_tokens": 8
  }
}
```

The proxy extracts `usage.input_tokens` and `usage.output_tokens` from every response. For streaming responses (`InvokeModelWithResponseStream`), the proxy accumulates the final `message_delta` event which contains the total usage.

### Model Pricing

Model rates are sourced from the AWS Price List API and cached locally in the proxy, refreshed once per day:

| Model | Input (per 1M tokens) | Output (per 1M tokens) |
|-------|----------------------|----------------------|
| Claude Haiku 3 | ~$0.25 | ~$1.25 |
| Claude Sonnet 4 | ~$3.00 | ~$15.00 |
| Claude Opus 4 | ~$15.00 | ~$75.00 |

### DynamoDB Update

After extracting usage, the proxy issues an atomic update against the per-model AI spend row (`BUDGET#ai#{modelID}`):

```
Key:
  PK = "SANDBOX#sb-7f3a9e12"
  SK = "BUDGET#ai#anthropic.claude-sonnet-4-20250514-v1:0"

UpdateExpression:
  ADD spentUSD :cost, inputTokens :inputTokens, outputTokens :outputTokens
  SET last_updated = :now

ExpressionAttributeValues:
  :cost = 0.0042
  :inputTokens = 1200
  :outputTokens = 340
  :now = "2026-03-22T15:12:47Z"
```

This is a single atomic operation. No locks, no read-before-write. Multiple concurrent Bedrock calls from the same sandbox (e.g., parallel agent tasks) increment correctly without races.

## AI Budget Dual-Layer Enforcement

AI budget enforcement uses two independent mechanisms: the HTTP proxy sidecar (real-time) and the budget-enforcer Lambda (backstop). Both layers must be bypassed for an agent to make Bedrock calls after the AI budget is exhausted.

### Layer 1: HTTP Proxy — Real-Time 403

Before forwarding any Bedrock `InvokeModel` request, the HTTP proxy reads the budget record from the local DynamoDB replica. If `ai_spend >= ai_limit`, the proxy immediately returns HTTP 403 without making any Bedrock API call:

```
HTTP/1.1 403 Forbidden
Content-Type: application/json

{
  "error":   "ai_budget_exhausted",
  "message": "AI budget exhausted. Spent $5.00 of $5.00. Run: km budget add sb-7f3a9e12 --ai <amount>",
  "spend":   5.00,
  "limit":   5.00
}
```

No tokens are consumed. No Bedrock API call is made. The agent sees an immediate rejection.

### Layer 2: Lambda IAM Revocation — Backstop

The same budget-enforcer Lambda that tracks compute spend also checks AI spend on each invocation. When `ai_spend >= ai_limit`, it detaches the `AmazonBedrockFullAccess` managed policy (`arn:aws:iam::aws:policy/AmazonBedrockFullAccess`) from the sandbox IAM role using `iam:DetachRolePolicy`. Without this policy, any direct Bedrock API calls from the sandbox receive an IAM `AccessDenied` error.

### Why Two Layers?

The proxy layer provides instant feedback with a clear error message for the common path: agents using the AWS SDK for Python, Go, or JavaScript routed through the proxy.

The IAM revocation layer catches calls that bypass the proxy:

- **Direct AWS CLI calls** using instance credentials from IMDS (the CLI makes direct HTTPS calls; iptables DNAT redirects them to the proxy, but if the agent has a path around iptables it would reach Bedrock directly).
- **Any SDK call that does not traverse the proxy** due to network configuration edge cases.

The two-layer design ensures exhausted AI budget is enforced regardless of how the agent calls Bedrock.

---

## Warning Flow

When either spend pool crosses the `warningThreshold` (default 80%), the operator receives an email.

### Trigger

- **Compute**: The periodic Lambda checks after each spend increment.
- **AI**: The HTTP proxy checks after each DynamoDB update.

Both check: `current_spend / limit >= warningThreshold`.

A single `warningNotified` boolean flag on the `BUDGET#meta` row in DynamoDB prevents duplicate emails. Once a warning is sent for either pool, no further warnings are sent.

### Email Content

The warning is sent via SES to the operator email using the existing `SendLifecycleNotification` helper. The details string is a single line summarizing both pools:

```
budget-warning: compute=83% ai=64%
```

This is a simple lifecycle notification, not a rich template. A single warning email covers both pools.

## Enforcement Flow

At 100% of a budget pool, enforcement is immediate and dual-layered.

### AI Budget Enforcement

**Layer 1 -- Proxy (real-time):**

Before forwarding any Bedrock `InvokeModel` request, the HTTP proxy reads the budget record from DynamoDB (local replica, sub-ms latency):

```
if ai_spend >= ai_limit:
    return HTTP 403 Forbidden
    {
      "error": "ai_budget_exhausted",
      "message": "AI budget exhausted. Spent $5.00 of $5.00. Run: km budget add sb-7f3a9e12 --ai <amount>",
      "spend": 5.00,
      "limit": 5.00
    }
```

The agent sees a 403 immediately. No Bedrock call is made, so no additional tokens are consumed.

**Layer 2 -- IAM (backstop):**

The EventBridge-triggered Lambda also monitors AI spend. When `ai_spend >= ai_limit`, it detaches the `AmazonBedrockFullAccess` managed policy from the sandbox IAM role using `iam:DetachRolePolicy`. This catches any Bedrock calls made via the AWS SDK or CLI directly, bypassing the proxy. The agent receives an IAM `AccessDenied` error from AWS.

**Why two layers?** The proxy layer gives instant feedback with a clear error message. The IAM layer is a backstop for cases where the agent might call Bedrock through a path that does not traverse the proxy (e.g., using the AWS CLI directly with credentials from the instance metadata).

### Compute Budget Enforcement

When `compute_spend >= compute_limit`, the Lambda suspends the sandbox:

**EC2 substrate:**
1. Calls `StopInstances` on the sandbox EC2 instance.
2. EBS volumes are preserved -- no data loss.
3. Spot compute charges stop immediately.
4. The instance can be started again on top-up.

**ECS Fargate substrate:**
1. Stops the ECS task via `ecs:StopTask`. (Artifact upload before stop is planned but not yet implemented.)
2. Fargate tasks are ephemeral -- top-up re-provisions from the stored profile.

### Enforcement Email

Enforcement emails are sent via `SendLifecycleNotification` with a simple details string:

- **Compute**: `compute-budget-exhausted: spent=0.4700 limit=0.5000`
- **AI**: `ai-budget-exhausted: spent=5.0042 limit=5.0000`

These are lifecycle notifications, not rich templates.

### Enforcement Timeline

```
 0%                    80%                   100%
  |----------------------|---------------------|
  normal operation       warning email          enforcement
                                                 - proxy 403 (AI)
                                                 - IAM deny (AI)
                                                 - StopInstances (compute)
                                                 - email notification
```

## Top-Up

Operators can add budget to a running or suspended sandbox.

### Command

```bash
km budget add <sandbox-id> --compute <amount> --ai <amount>
```

Both flags are optional -- you can top up one pool without affecting the other.

### Examples

Add $3.00 to AI budget:

```bash
$ km budget add sb-7f3a9e12 --ai 3.00
Budget updated: compute $5.0000/$5.0000, AI $5.0000/$8.0000
```

Add $1.00 to compute and $5.00 to AI (with auto-resume):

```bash
$ km budget add sb-7f3a9e12 --compute 1.00 --ai 5.00
Budget updated: compute $2.0100/$3.0000, AI $5.0000/$10.0000
Sandbox sb-7f3a9e12 resumed.
```

### What Top-Up Does

The command performs these steps in order:

1. **DynamoDB update**: Reads the current limits, calculates the new values (additive top-up), and writes new limits using `SET` with pre-calculated values.
2. **Proxy unblock (AI)**: The proxy re-reads budget from DynamoDB on each request, so unblocking is automatic once limits are updated.
3. **IAM restore (AI)**: If the `AmazonBedrockFullAccess` policy was detached by the enforcer, it is re-attached to the sandbox role.
4. **EC2 start (compute)**: If the instance was stopped, calls `StartInstances`. The instance resumes from its stopped state with all EBS data intact.
5. **ECS re-provision (compute)**: If the Fargate task was stopped, re-provisions a new task from the sandbox profile stored in S3. The workspace is restored from the artifact tarball.

### Top-Up Idempotency

Running `km budget add` on a sandbox that is not suspended simply increases the limit. The sandbox continues running without interruption.

## Top-Up Flow Details

Top-up touches each enforcement layer in the correct order to restore the sandbox to full operation.

### AI Top-Up Sequence

```
km budget add sb-7f3a9e12 --ai 3.00
```

1. **DynamoDB update**: Reads the current `aiLimit`, adds $3.00, and writes the new limit using `SET aiLimit = :newLimit` (pre-calculated, not `ADD`).
2. **Proxy unblock**: The HTTP proxy reads budget from DynamoDB on every request. Once the limit is updated, the next proxy read sees `ai_spend < ai_limit` and allows Bedrock calls immediately. No proxy restart required.
3. **IAM restore**: If the `AmazonBedrockFullAccess` managed policy was detached by the Lambda, `km budget add` checks with `ListAttachedRolePolicies` and re-attaches it with `iam:AttachRolePolicy`. The sandbox IAM role can once again invoke Bedrock.

### Compute Top-Up Sequence

```
km budget add sb-7f3a9e12 --compute 2.00
```

1. **DynamoDB update**: Reads the current `computeLimit`, adds $2.00, and writes the new limit using `SET computeLimit = :newLimit` (pre-calculated, not `ADD`).
2. **EC2 start (if stopped)**: If the substrate is EC2 and the instance is in `stopped` state, `km budget add` calls `ec2:StartInstances`. The instance starts from the preserved EBS state. All data is intact. The agent's processes do not automatically restart — the agent must be re-initiated by the operator or a startup script.
3. **ECS re-provision (if stopped)**: If the substrate is ECS and the task is stopped, `km budget add` reads the profile from S3 (`artifacts/{sandbox-id}/.km-profile.yaml`) and provisions a new Fargate task. The artifact tarball is restored to `/workspace` on task startup.

### Full Top-Up Command Reference

```bash
# Add to AI budget only
km budget add <sandbox-id> --ai <amount>

# Add to compute budget only
km budget add <sandbox-id> --compute <amount>

# Add to both pools simultaneously
km budget add <sandbox-id> --compute <amount> --ai <amount>
```

Both flags are optional. Each flag independently tops up its pool without affecting the other.

## Per-Model AI Breakdown

The `km status` command shows per-model token usage and cost, sourced from the `BUDGET#ai#{modelID}` rows in the DynamoDB budget table.

### Model Pricing Source

Model rates are sourced from the AWS Price List API at sandbox creation time and cached in the proxy. The proxy refreshes rates once per day. When the Pricing API is unreachable (e.g., `pricing:GetProducts` API outage, or a sandbox running in a region without API access), a static fallback table (`GetBedrockModelRates` in the budget package) provides conservative default rates.

```
$ km status sb-7f3a9e12
...
Budget:
  AI:      $3.4300 / $5.0000 (68.6%)
    claude-3-haiku-20240307:       $0.2300 (4521K in / 389K out)
    claude-sonnet-4-20250514:      $2.9800 (287K in / 53K out)
    claude-opus-4-20250514:        $0.2200 (12K in / 3K out)
  Warning threshold: 80%
```

Each row shows the model ID, cumulative USD cost, and cumulative input/output tokens (in thousands) since sandbox creation.

The per-model breakdown is updated atomically by the HTTP proxy on each Bedrock response using `ADD spentUSD :cost, inputTokens :in, outputTokens :out` on the `BUDGET#ai#{modelID}` row. The `km status` command reads all budget rows with a single `Query` using `begins_with(SK, "BUDGET#")`.

---

## km status Budget View

`km status` includes a budget section when the sandbox has budget configured.

### Example Output

The actual `km status` output uses a flat key-value format:

```bash
$ km status sb-7f3a9e12
Sandbox ID:  sb-7f3a9e12
Profile:     dev-with-budget
Substrate:   ec2-spot
Region:      us-east-1
Status:      running
Created At:  2026-03-22 2:30:00 PM UTC
TTL Expiry:  2026-03-23 2:30:00 PM UTC
Resources (3):
  - arn:aws:ec2:us-east-1:...:instance/i-0abc123def456
  - arn:aws:iam::...:role/km-sb-7f3a9e12
  - arn:aws:dynamodb:us-east-1:...:table/km-budgets
Budget:
  Compute: $1.0600 / $2.0000 (53.0%)
  AI:      $3.2100 / $5.0000 (64.2%)
    claude-3-haiku-20240307:       $0.2300 (4521K in / 389K out)
    claude-sonnet-4-20250514:      $2.9800 (287K in / 53K out)
  Warning threshold: 80%
```

Percentages are color-coded in TTY output: green below 80%, yellow from 80-99%, red at 100% or above.

### Suspended Sandbox

When a sandbox is stopped due to budget exhaustion, the `Status` field reflects the stopped state:

```bash
$ km status sb-7f3a9e12
Sandbox ID:  sb-7f3a9e12
Profile:     dev-with-budget
Substrate:   ec2-spot
Region:      us-east-1
Status:      stopped
Created At:  2026-03-22 2:30:00 PM UTC
Budget:
  Compute: $2.0100 / $2.0000 (100.5%)
  AI:      $3.2100 / $5.0000 (64.2%)
  Warning threshold: 80%
```

## Cost Examples

Practical budget sizing for common scenarios.

### Compute Costs

A `t3.medium` spot instance in us-east-1 at ~$0.0104/hr:

| Budget | Runtime |
|--------|---------|
| $0.50 | ~48 hours |
| $1.00 | ~96 hours |
| $2.00 | ~192 hours (~8 days) |
| $5.00 | ~480 hours (~20 days) |

A `t3.small` spot instance at ~$0.0052/hr:

| Budget | Runtime |
|--------|---------|
| $0.50 | ~96 hours |
| $1.00 | ~192 hours |
| $2.00 | ~384 hours (~16 days) |

### AI Token Costs

**Claude Haiku 3** at ~$0.25/MTok input, ~$1.25/MTok output:

| Budget | Input Tokens (approx) | Output Tokens (approx) |
|--------|----------------------|----------------------|
| $1.00 | ~4M input | ~800K output |
| $5.00 | ~20M input | ~4M output |
| $10.00 | ~40M input | ~8M output |

**Claude Sonnet 4** at ~$3.00/MTok input, ~$15.00/MTok output:

| Budget | Input Tokens (approx) | Output Tokens (approx) |
|--------|----------------------|----------------------|
| $1.00 | ~333K input | ~67K output |
| $5.00 | ~1.7M input | ~333K output |
| $10.00 | ~3.3M input | ~667K output |

**Claude Opus 4** at ~$15.00/MTok input, ~$75.00/MTok output:

| Budget | Input Tokens (approx) | Output Tokens (approx) |
|--------|----------------------|----------------------|
| $5.00 | ~333K input | ~67K output |
| $10.00 | ~667K input | ~133K output |
| $25.00 | ~1.7M input | ~333K output |

### Mixed Workload Example

A typical coding agent session might use a mix of Haiku (for fast lookups and simple edits) and Sonnet (for complex reasoning). A realistic daily budget:

```yaml
spec:
  budget:
    compute:
      maxSpendUSD: 0.25    # t3.medium for ~24h
    ai:
      maxSpendUSD: 5.00    # ~1.7M Sonnet input tokens
```

At the end of a day, `km status` might show:

```
Compute Budget:
  Spent:     $0.21 of $0.25 (84.0%)
AI Budget:
  Spent:     $3.47 of $5.00 (69.4%)

  Per-Model Breakdown:
  MODEL                          INPUT TOKENS   OUTPUT TOKENS   SPEND
  claude-3-haiku-20240307        8,200,000      1,100,000       $3.43
  claude-sonnet-4-20250514       12,000         2,400           $0.04
```

In this example, the agent used Haiku heavily for routine tasks and only called Sonnet twice for complex decisions.

## Global Table Replication

The `km-budgets` DynamoDB table is a global table, replicated to every region where sandbox agents run.

### Why Global Replication

The HTTP proxy sidecar runs inside the sandbox, in the same region as the agent. On every Bedrock request, it must:

1. Read the budget record to check if the pool is exhausted.
2. Write the updated spend after getting the response.

Without replication, a sandbox in `us-west-2` would need to read from a table in `us-east-1` -- adding 60-80ms of cross-region latency to every AI call. With a local replica, reads are sub-millisecond.

### Replication Topology

```
                    ┌──────────────────┐
                    │   us-east-1      │
                    │   (home region)  │
                    │   km-budgets     │◄── km CLI writes here
                    └────────┬─────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
    ┌─────────▼──────┐ ┌────▼────────┐ ┌──▼──────────────┐
    │  us-west-2     │ │  eu-west-1  │ │  ap-southeast-1 │
    │  km-budgets    │ │  km-budgets │ │  km-budgets     │
    │  (replica)     │ │  (replica)  │ │  (replica)      │
    └────────────────┘ └─────────────┘ └─────────────────┘
         ▲                   ▲                  ▲
         │                   │                  │
    http-proxy          http-proxy         http-proxy
    reads local         reads local        reads local
```

### Consistency Model

DynamoDB global tables use eventual consistency for cross-region replication, typically converging within 1-2 seconds. For budget tracking, this is acceptable:

- **Reads are local**: The proxy reads from the local replica. A brief replication lag means the proxy might see a spend value that is 1-2 seconds stale -- negligible for budget enforcement.
- **Writes go to the local replica**: The proxy writes spend increments to the nearest replica. DynamoDB propagates the write to all other replicas automatically.
- **Conflict resolution**: DynamoDB global tables use last-writer-wins for concurrent writes to the same attribute. Since `ADD` operations are commutative and each sandbox has a single proxy writer, conflicts do not arise in practice.
- **Enforcement overshoot**: In the worst case, a sandbox could overshoot its budget by the cost of one Bedrock call (the call that was in-flight when the budget was crossed). This is by design -- the alternative (pre-flight budget reservation) adds latency and complexity for marginal benefit.

### Adding Regions

When a new region is added to `spec.identity.allowedRegions` across profiles, the global table must be extended:

```hcl
# infra/modules/dynamodb-budget/v1.0.0/main.tf
resource "aws_dynamodb_table" "budget" {
  name         = "km-budgets"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "PK"
  range_key    = "SK"

  attribute {
    name = "PK"
    type = "S"
  }

  attribute {
    name = "SK"
    type = "S"
  }

  ttl {
    attribute_name = "expiresAt"
    enabled        = true
  }

  dynamic "replica" {
    for_each = var.replica_regions
    content {
      region_name = replica.value
    }
  }
}
```

Add a new `replica` block and apply. DynamoDB handles the initial backfill automatically.
