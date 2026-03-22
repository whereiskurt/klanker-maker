# Klanker Maker Budget Guide

Phase 6 budget enforcement for per-sandbox compute and AI spend control.

## Table of Contents

- [Overview](#overview)
- [Profile Configuration](#profile-configuration)
- [DynamoDB Schema](#dynamodb-schema)
- [Compute Metering](#compute-metering)
- [AI Token Metering](#ai-token-metering)
- [Warning Flow](#warning-flow)
- [Enforcement Flow](#enforcement-flow)
- [Top-Up](#top-up)
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

Budget records live in the same DynamoDB global table used for sandbox state, following the defcon.run.34 single-table design pattern.

### Table Configuration

| Property | Value |
|----------|-------|
| Table name | `km-sandbox` |
| Billing mode | PAY_PER_REQUEST |
| Replication | Global table, replicated to all active regions |
| Partition key | `pk` (String) |
| Sort key | `sk` (String) |
| TTL attribute | `ttl_expire` |

### Budget Item

For a sandbox `sb-7f3a9e12`, the budget record uses:

- **pk**: `SANDBOX#sb-7f3a9e12`
- **sk**: `BUDGET`

```json
{
  "pk": { "S": "SANDBOX#sb-7f3a9e12" },
  "sk": { "S": "BUDGET" },
  "compute_limit": { "N": "2.00" },
  "compute_spend": { "N": "0.47" },
  "ai_limit": { "N": "5.00" },
  "ai_spend": { "N": "1.23" },
  "per_model_spend": {
    "M": {
      "anthropic.claude-3-haiku-20240307-v1:0": {
        "M": {
          "input_tokens": { "N": "4521600" },
          "output_tokens": { "N": "389200" },
          "spend": { "N": "0.23" }
        }
      },
      "anthropic.claude-sonnet-4-20250514-v1:0": {
        "M": {
          "input_tokens": { "N": "287400" },
          "output_tokens": { "N": "53100" },
          "spend": { "N": "1.00" }
        }
      }
    }
  },
  "compute_rate_hr": { "N": "0.0104" },
  "instance_type": { "S": "t3.medium" },
  "region": { "S": "us-east-1" },
  "created_at": { "S": "2026-03-22T14:30:00Z" },
  "updated_at": { "S": "2026-03-22T15:12:47Z" },
  "ttl_expire": { "N": "1711468200" }
}
```

### Key Design Decisions

**Single-table pattern**: The budget item sits alongside other sandbox items (state, config, session) under the same partition key. This means a single `Query` on `pk = SANDBOX#sb-7f3a9e12` returns everything about the sandbox, including budget.

**TTL cleanup**: `ttl_expire` is set to the sandbox TTL plus a 24-hour grace period. After the sandbox is destroyed, DynamoDB automatically removes the budget record. This prevents table bloat without requiring a cleanup Lambda.

**Atomic increments**: AI spend updates use `UpdateExpression` with `ADD` to atomically increment `ai_spend` and per-model token counts. No read-modify-write races.

## Compute Metering

Compute spend is tracked as the sandbox's spot rate multiplied by elapsed minutes, billed per-minute.

### Rate Sourcing

At sandbox creation, `km create` queries the AWS Price List API for the current spot price of the configured instance type and region:

```
GET https://api.pricing.us-east-1.amazonaws.com/...
    ServiceCode=AmazonEC2
    instanceType=t3.medium
    operatingSystem=Linux
    regionCode=us-east-1
    capacityStatus=SpotInstance
```

The returned rate is stored in the DynamoDB budget record as `compute_rate_hr` (USD per hour). This rate is cached for the lifetime of the sandbox -- spot price fluctuations after creation do not affect the budget calculation.

### Periodic Polling

An EventBridge-triggered Lambda runs every 60 seconds for each active sandbox:

1. Reads `compute_rate_hr` and `compute_spend` from DynamoDB.
2. Calculates spend since last update: `rate_hr / 60 * minutes_elapsed`.
3. Atomically increments `compute_spend` in DynamoDB.
4. Checks `compute_spend` against `compute_limit` for warning/enforcement thresholds.

### Rate Differences

Spot rates vary by instance type and region. Examples at time of writing:

| Instance Type | us-east-1 | us-west-2 | eu-west-1 |
|--------------|-----------|-----------|-----------|
| t3.small | ~$0.0052/hr | ~$0.0052/hr | ~$0.0058/hr |
| t3.medium | ~$0.0104/hr | ~$0.0104/hr | ~$0.0116/hr |
| t3.large | ~$0.0208/hr | ~$0.0208/hr | ~$0.0232/hr |
| m5.large | ~$0.0260/hr | ~$0.0270/hr | ~$0.0290/hr |

These rates are looked up dynamically -- the table above is for estimation only.

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

After extracting usage, the proxy issues an atomic update:

```
UpdateExpression:
  ADD ai_spend :cost,
      per_model_spend.#model.input_tokens :in_tok,
      per_model_spend.#model.output_tokens :out_tok,
      per_model_spend.#model.spend :cost
  SET updated_at = :now

ExpressionAttributeNames:
  #model = "anthropic.claude-sonnet-4-20250514-v1:0"

ExpressionAttributeValues:
  :cost = 0.0042
  :in_tok = 1200
  :out_tok = 340
  :now = "2026-03-22T15:12:47Z"
```

This is a single atomic operation. No locks, no read-before-write. Multiple concurrent Bedrock calls from the same sandbox (e.g., parallel agent tasks) increment correctly without races.

## Warning Flow

When either spend pool crosses the `warningThreshold` (default 80%), the operator receives an email.

### Trigger

- **Compute**: The periodic Lambda checks after each spend increment.
- **AI**: The HTTP proxy checks after each DynamoDB update.

Both check: `current_spend / limit >= warningThreshold`.

A `warning_sent_compute` or `warning_sent_ai` boolean flag on the DynamoDB record prevents duplicate emails.

### Email Content

The warning is sent via SES to `KM_OPERATOR_EMAIL` using the existing `SendLifecycleNotification` pattern:

```
Subject: [Klanker Maker] Budget Warning — sb-7f3a9e12

Sandbox sb-7f3a9e12 has reached 82% of its AI budget.

  Pool:       AI (Bedrock)
  Spent:      $4.10 of $5.00
  Threshold:  80%

  Per-model breakdown:
    Claude Haiku 3      $0.31  (4.5M input / 389K output tokens)
    Claude Sonnet 4     $3.79  (287K input / 53K output tokens)

  Sandbox:    sb-7f3a9e12
  Region:     us-east-1
  Profile:    dev-with-budget
  Created:    2026-03-22T14:30:00Z

To add more budget:
  km budget add sb-7f3a9e12 --ai 5.00

To destroy the sandbox:
  km destroy sb-7f3a9e12
```

A separate email fires for compute warnings:

```
Subject: [Klanker Maker] Budget Warning — sb-7f3a9e12

Sandbox sb-7f3a9e12 has reached 83% of its compute budget.

  Pool:       Compute (EC2 spot)
  Spent:      $1.66 of $2.00
  Rate:       $0.0104/hr (t3.medium spot, us-east-1)
  Threshold:  80%

  Estimated time remaining: ~3.3 hours

To add more budget:
  km budget add sb-7f3a9e12 --compute 2.00
```

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

The EventBridge-triggered Lambda also monitors AI spend. When `ai_spend >= ai_limit`, it revokes Bedrock permissions from the sandbox IAM role by attaching a deny policy:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Sid": "BudgetDenyBedrock",
    "Effect": "Deny",
    "Action": "bedrock:InvokeModel*",
    "Resource": "*"
  }]
}
```

This catches any Bedrock calls made via the AWS SDK or CLI directly, bypassing the proxy. The agent receives an IAM `AccessDenied` error from AWS.

**Why two layers?** The proxy layer gives instant feedback with a clear error message. The IAM layer is a backstop for cases where the agent might call Bedrock through a path that does not traverse the proxy (e.g., using the AWS CLI directly with credentials from the instance metadata).

### Compute Budget Enforcement

When `compute_spend >= compute_limit`, the Lambda suspends the sandbox:

**EC2 substrate:**
1. Calls `StopInstances` on the sandbox EC2 instance.
2. EBS volumes are preserved -- no data loss.
3. Spot compute charges stop immediately.
4. The instance can be started again on top-up.

**ECS Fargate substrate:**
1. Triggers artifact upload (workspace tarball to S3).
2. Stops the ECS task.
3. Fargate tasks are ephemeral -- top-up re-provisions from the stored profile.

### Enforcement Email

```
Subject: [Klanker Maker] Budget Exhausted — sb-7f3a9e12

Sandbox sb-7f3a9e12 has exhausted its compute budget and has been suspended.

  Pool:       Compute (EC2 spot)
  Spent:      $2.01 of $2.00
  Action:     EC2 instance stopped (EBS preserved)

  The sandbox is suspended. Data is preserved.

To resume:
  km budget add sb-7f3a9e12 --compute 2.00

To collect artifacts and destroy:
  km destroy sb-7f3a9e12
```

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
Budget updated for sb-7f3a9e12:
  AI:      $5.00 → $8.00 (+$3.00)
  Spent:   $5.00 of $8.00 (62.5%)
  Status:  proxy unblocked, IAM restored
```

Add $1.00 to compute and $5.00 to AI:

```bash
$ km budget add sb-7f3a9e12 --compute 1.00 --ai 5.00
Budget updated for sb-7f3a9e12:
  Compute: $2.00 → $3.00 (+$1.00)
  AI:      $5.00 → $10.00 (+$5.00)
  Compute: EC2 instance starting...
  Status:  running
```

### What Top-Up Does

The command performs these steps in order:

1. **DynamoDB update**: Atomically increments `compute_limit` and/or `ai_limit`.
2. **Proxy unblock (AI)**: The proxy re-reads budget from DynamoDB on each request, so unblocking is automatic once limits are updated.
3. **IAM restore (AI)**: If the `BudgetDenyBedrock` policy was attached, it is detached from the sandbox role.
4. **EC2 start (compute)**: If the instance was stopped, calls `StartInstances`. The instance resumes from its stopped state with all EBS data intact.
5. **ECS re-provision (compute)**: If the Fargate task was stopped, re-provisions a new task from the sandbox profile stored in S3. The workspace is restored from the artifact tarball.

### Top-Up Idempotency

Running `km budget add` on a sandbox that is not suspended simply increases the limit. The sandbox continues running without interruption.

## km status Budget View

`km status` includes a budget section when the sandbox has budget configured.

### Example Output

```bash
$ km status sb-7f3a9e12
Sandbox: sb-7f3a9e12
Profile: dev-with-budget
Region:  us-east-1
State:   running
Uptime:  1h 42m

Compute Budget:
  Spent:     $1.06 of $2.00 (53.0%)
  Rate:      $0.0104/hr (t3.medium spot)
  Remaining: ~$0.94 (~90.4 hours at current rate)
  ████████████░░░░░░░░░░░░ 53%

AI Budget:
  Spent:     $3.21 of $5.00 (64.2%)
  Remaining: ~$1.79
  ████████████████░░░░░░░░ 64%

  Per-Model Breakdown:
  MODEL                          INPUT TOKENS   OUTPUT TOKENS   SPEND
  claude-3-haiku-20240307        4,521,600      389,200         $0.23
  claude-sonnet-4-20250514       287,400        53,100          $2.98
  ─────────────────────────────────────────────────────────────────────
  Total                          4,809,000      442,300         $3.21

Resources (3):
  - arn:aws:ec2:us-east-1:...:instance/i-0abc123def456
  - arn:aws:iam::...:role/km-sb-7f3a9e12
  - arn:aws:dynamodb:us-east-1:...:table/km-sandbox
```

### Suspended Sandbox

When a sandbox is suspended due to budget exhaustion:

```bash
$ km status sb-7f3a9e12
Sandbox: sb-7f3a9e12
Profile: dev-with-budget
Region:  us-east-1
State:   suspended (compute budget exhausted)
Uptime:  192h 18m (stopped)

Compute Budget:
  Spent:     $2.01 of $2.00 (100.5%)  ** EXHAUSTED **
  Rate:      $0.0104/hr (t3.medium spot)
  EC2:       stopped (EBS preserved)
  ████████████████████████ 100%

AI Budget:
  Spent:     $3.21 of $5.00 (64.2%)
  Remaining: ~$1.79
  ████████████████░░░░░░░░ 64%

  [per-model breakdown omitted for brevity]

To resume: km budget add sb-7f3a9e12 --compute 2.00
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

The `km-sandbox` DynamoDB table is a global table, replicated to every region where sandbox agents run.

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
                    │   km-sandbox     │◄── km CLI writes here
                    └────────┬─────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
    ┌─────────▼──────┐ ┌────▼────────┐ ┌──▼──────────────┐
    │  us-west-2     │ │  eu-west-1  │ │  ap-southeast-1 │
    │  km-sandbox    │ │  km-sandbox │ │  km-sandbox     │
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
# infra/modules/dynamodb-global/v1.0.0/main.tf
resource "aws_dynamodb_table" "km_sandbox" {
  name         = "km-sandbox"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "pk"
  range_key    = "sk"

  attribute {
    name = "pk"
    type = "S"
  }

  attribute {
    name = "sk"
    type = "S"
  }

  ttl {
    attribute_name = "ttl_expire"
    enabled        = true
  }

  replica {
    region_name = "us-west-2"
  }

  replica {
    region_name = "eu-west-1"
  }

  replica {
    region_name = "ap-southeast-1"
  }
}
```

Add a new `replica` block and apply. DynamoDB handles the initial backfill automatically.
