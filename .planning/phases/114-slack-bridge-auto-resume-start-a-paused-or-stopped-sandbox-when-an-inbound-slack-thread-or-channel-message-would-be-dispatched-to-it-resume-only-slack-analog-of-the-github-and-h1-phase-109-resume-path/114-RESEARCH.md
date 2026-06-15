# Phase 114: Slack Bridge Auto-Resume — Research

**Researched:** 2026-06-15
**Domain:** AWS Lambda (Go), EC2 StartInstances, DynamoDB UpdateItem, Slack bridge EventsHandler
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Scope:**
- Resume-only. Both `paused` (hibernated) and `stopped` instances are resumable (`FetchByChannel` already collapses both into `info.Paused == true`).
- No cold-create. An orphaned row (instance terminated, row still `paused`/`stopped`) degrades to an informative hint and leaves the row in place.
- No budget logic.

**Trigger gate:**
- Resume fires ONLY at the existing step-9 paused branch — i.e. only after the message passed the mention-only / thread-bypass filter (step 5b) and was enqueued (step 8). Idle channel chatter that wouldn't dispatch never wakes the box.

**Resume mechanism (copy GitHub Phase-109 pattern into `pkg/slack/bridge`):**
- New `SandboxResumer` interface + `EC2Resumer.StartSandbox(ctx, sandboxID)`. Near-verbatim port of `pkg/github/bridge/aws_adapters.go`. MUST filter on `tag:km:sandbox-id` (carries the Phase-109 / commit `e6b9ca75` / `d8007920` fix — NOT `{prefix}:sandbox-id`).
- New `var ErrNoResumableInstance` sentinel (slack-bridge namespaced). Wrapped only on the terminal `len(found)==0` path; transient `DescribeInstances`/`StartInstances` errors are returned plain.
- New `SandboxStatusWriter` interface + `SetStatusRunning(ctx, sandboxID)` via `UpdateItem` on `km-sandboxes` (NEVER `PutItem` — SandboxMetadata lossy round-trip footgun). The status flip is the real idempotency guard: next message sees `info.Paused == false` → warm path, no re-resume.

**Handler change (`events_handler.go` step 9):**
```
if info.Paused && h.Resumer != nil {
    err := h.Resumer.StartSandbox(ctx, info.SandboxID)
    if err != nil && errors.Is(err, ErrNoResumableInstance) {
        → degraded hint ("couldn't auto-resume; ask an operator"); leave row; no status flip
    } else {
        → SetStatusRunning (fail-soft); "waking up" hint
    }
}
```
- Message already enqueued at step 8 in all cases — resume failure NEVER strands the prompt (same fail-soft contract as GitHub: transient error still enqueues + optimistically flips).
- Keep the existing fire-and-forget goroutine + bounded context so `Handle` returns 200 within Slack's 3s ack window.

**Back-compat invariant:**
- `h.Resumer == nil` → byte-identical to today (pause-hint only). Tests and pre-deploy Lambda images must keep working.

**Wake UX:**
- Repurpose `DDBPauseHinter`. Resume-triggered → "Sandbox is waking up — your message is queued and will be answered shortly." Orphan/degraded → distinct "couldn't auto-resume; ask an operator to recreate." Exact strings finalized in the plan.

**Deploy surface:**
- `make build-lambdas` + `km init --slack` (or `--dry-run=false`). NOT `--sidecars`.
- One additive IAM grant on `infra/modules/lambda-slack-bridge/v1.0.0` (edit in place): `ec2:DescribeInstances` on `*`, `ec2:StartInstances` on `instance/*` conditioned on `aws:ResourceTag/km:resource-prefix == var.resource_prefix`. Mirror the GitHub `ec2_resume` policy. Confirm `var.resource_prefix` exists in the slack module (add if not).
- DDB `UpdateItem` on `km-sandboxes` ALREADY granted (pause-hinter) → no new DDB grant.
- No sandbox recreate — existing paused sandboxes gain resume-on-message after bridge deploy.

### Claude's Discretion
- Exact hint message strings.
- Whether `SetStatusRunning` is a standalone adapter or a method on the existing pause-hinter's `DDBUpdateItemAPI` adapter.
- Test file layout (mirror `pkg/github/bridge/webhook_handler_phase109_test.go`).

### Deferred Ideas (OUT OF SCOPE)
- Cold-create on orphaned/terminated instance (needs a Slack-side SandboxCreate publisher).
- Budget-suspend awareness (don't resume a budget-exhausted box).
- SQS retention tuning for very stale threads.
</user_constraints>

---

## Summary

Phase 114 is a targeted, low-risk extension: add `StartInstances` to the one place the Slack bridge already knows a sandbox is paused and would otherwise just post a hint. The hard parts (channel→sandboxID resolution, state detection, SQS enqueue, pause hint cooldown) are all already done. The GitHub Phase-109 implementation is a near-perfect port target — the primary structural difference is that Slack does not need the cold-create branch (no `EventBridgePublisher`), making the Slack version simpler.

The research confirmed all five key assumptions in the design spec: `var.resource_prefix` already exists in the Slack TF module (line 61 of variables.tf), `dynamodb:UpdateItem` on `km-sandboxes` is already granted (the pause-hinter policy), no EC2 client is currently constructed in `cmd/km-slack-bridge/main.go` (one needs adding, following the GitHub bridge pattern at `cmd/km-github-bridge/main.go:85`), the Slack simplification holds (FetchByChannel already returns `info.Paused + info.SandboxID` — no status-aware resolver needed), and the `DDBUpdateItemAPI` interface in `pkg/slack/bridge/aws_adapters.go:1233` already covers `UpdateItem` (the `SetStatusRunning` adapter can reuse it directly without widening any interface).

**Primary recommendation:** Port `EC2Resumer` + `DynamoSandboxStatusWriter` from `pkg/github/bridge/aws_adapters.go` into `pkg/slack/bridge/aws_adapters.go`, add `SandboxResumer` + `SandboxStatusWriter` to `pkg/slack/bridge/events_interfaces.go`, replace the bare `if info.Paused && h.PauseHinter != nil` block in `events_handler.go` with the status-aware resume branch (keeping PauseHinter as the hint poster), wire the new adapters in `wireEventsHandler()`, and add the `ec2_resume` IAM policy to the Slack bridge TF module. The result is a 5-file change with no schema changes and no sandbox recreate required.

---

## Standard Stack

### Core (already in use — no new dependencies)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/ec2` | (project-pinned) | DescribeInstances + StartInstances | Used identically in `pkg/github/bridge`; AWS SDK v2 is the project standard |
| `github.com/aws/aws-sdk-go-v2/service/dynamodb` | (project-pinned) | UpdateItem on km-sandboxes | Already imported in `pkg/slack/bridge/aws_adapters.go` |

### New addition to `cmd/km-slack-bridge/main.go`

```go
"github.com/aws/aws-sdk-go-v2/service/ec2"
```

The `ec2.NewFromConfig(cfg)` call follows the identical pattern already used in `cmd/km-github-bridge/main.go:85`. The shared `cfg` from `awsconfig.LoadDefaultConfig(ctx)` is captured at `init()` in `cmd/km-slack-bridge/main.go:75`; `wireEventsHandler()` runs after `init()` and has access to the package-level `cfg` (as used for `initDDB`, `initSSMC`, `initSQSClient`, `initS3Client`).

**Wait**: `cfg` is a local var in `init()`. The pattern in the slack bridge is to construct AWS clients in `init()` and store them in package-level vars. The EC2 client must be constructed in `init()` and stored as a new package-level `var initEC2Client *ec2.Client`, then referenced in `wireEventsHandler()`. Compare with github-bridge where the entire wiring is in one `main()`.

---

## Architecture Patterns

### Confirmed: Slack vs GitHub Structural Difference (Simplification)

The Slack bridge resolves by CHANNEL, not ALIAS:

- `FetchByChannel(ctx, channelID)` returns `SandboxRoutingInfo{SandboxID, QueueURL, Paused, ...}`
- `info.Paused` is already set to `true` when `state == "paused" || state == "stopped"` (`aws_adapters.go:1058-1059`)
- `info.SandboxID` is already in hand

This means the Slack bridge does NOT need `SandboxAliasResolverWithStatus`. The resume can call `h.Resumer.StartSandbox(ctx, info.SandboxID)` directly with `info.SandboxID` from the existing `FetchByChannel` result. No new resolver interface; no new DDB query path.

The GitHub bridge needs `ResolveByAliasWithStatus` because it resolves `alias → (id, status)` in one call (the alias GSI needs to return status too). The Slack bridge's `FetchByChannel` already returns both. This is the key simplification documented in the design spec — confirmed by reading the actual code.

### Orphan Handling Difference (No Cold-Create)

GitHub Phase-109: `ErrNoResumableInstance` → `DeleteSandboxRow` → `PutSandboxCreate` (cold-create).
Slack Phase-114: `ErrNoResumableInstance` → post degraded hint, leave row. No delete, no create. The row stays as `paused`/`stopped` and `km resume` or `km destroy && km create` is the operator path.

This means `SandboxStatusWriter` for Slack needs only `SetStatusRunning`. No `DeleteSandboxRow`. The interface is a strict subset of the GitHub `SandboxStatusWriter`.

### Pattern: EventsHandler Extension via Optional Fields

Every feature added to `EventsHandler` since Phase 91 uses optional/nil fields:

```go
// Example from EventsHandler struct:
PauseHinter   PauseHintPoster    // optional; if nil, paused-hint branch is skipped
Relayer        PeerRelayer        // nil => federation off
DefaultRouter  bool               // false => dormant, byte-identical
```

Phase 114 adds two new fields:
```go
Resumer       SandboxResumer     // optional; nil => byte-identical to today (pause-hint only)
StatusWriter  SandboxStatusWriter // optional; nil => status not flipped (graceful degradation)
```

Both optional so the back-compat invariant holds: nil Resumer + existing nil-PauseHinter test at `events_handler_test.go:694` is the model.

### Wiring Pattern: `wireEventsHandler()` in `cmd/km-slack-bridge/main.go`

The `wireEventsHandler()` function at `main.go:204` is where all EventsHandler adapters are attached after cold-start. The PauseHinter is wired at lines 316-324. The EC2Resumer and DynamoSandboxStatusWriter wiring go after the PauseHinter block, unconditionally (no env guard needed — the IAM grant is always present after the TF change):

```go
// Phase 114: auto-resume wiring.
// EC2Resumer calls StartInstances on paused/stopped sandbox instances tagged
// tag:km:sandbox-id == sandboxID (the Phase-109/e6b9ca75 fix — never {prefix}:sandbox-id).
eventsHandler.Resumer = &bridge.EC2Resumer{
    Client:         initEC2Client,
    ResourcePrefix: prefix, // inert (see EC2Resumer.sandboxIDTagKey), kept for wiring compat
}
eventsHandler.StatusWriter = &bridge.DynamoSandboxStatusWriter{
    Client:    initDDB,
    TableName: sandboxesTable,
}
```

The `initEC2Client` is a new package-level var of type `*ec2.Client`, initialized in `init()` via `ec2.NewFromConfig(cfg)`. The existing `initDDB` and `sandboxesTable` are already available in `wireEventsHandler()`.

### Pattern: `stopping` State Handling in EC2Resumer

The GitHub `EC2Resumer.StartSandbox` implementation (verified at `pkg/github/bridge/aws_adapters.go:521-626`) handles both `stopped` AND `stopping` states with a bounded poll loop (2s interval, 8s max). When all matched instances are still `stopping`, it waits for them to reach `stopped` before calling `StartInstances`. This same behavior should be copied verbatim — a quick `km pause` followed by an immediate Slack message could hit the `stopping` transitional state.

### Pattern: `DDBUpdateItemAPI` Reuse for `SetStatusRunning`

The Slack bridge already defines `DDBUpdateItemAPI` at `aws_adapters.go:1233`:

```go
type DDBUpdateItemAPI interface {
    DDBQueryGetPutAPI
    UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, ...) (...)
}
```

The Slack-bridge `DynamoSandboxStatusWriter` can use this same `DDBUpdateItemAPI` interface (which `*dynamodb.Client` satisfies). In `wireEventsHandler()`, `initDDB` (which is a `*dynamodb.Client`) satisfies `DDBUpdateItemAPI` already. No new interface needed.

**Decision for Claude's Discretion:** `SetStatusRunning` should be a standalone `DynamoSandboxStatusWriter` struct (mirroring the GitHub bridge struct name and shape), not a method on `DDBPauseHinter`. Reasons: (1) separation of concerns — the hinter enforces cooldown, the status writer just does UpdateItem; (2) tests can inject mock independently; (3) mirrors the GitHub pattern exactly, making the code easier to reason about across both bridges.

### Pattern: Step-9 Goroutine Budget

The existing step-9 PauseHinter uses `context.WithTimeout(context.Background(), 5*time.Second)`. The new resume branch should use the same bounded-context pattern. The goroutine body executes:
1. `h.Resumer.StartSandbox(bgCtx, info.SandboxID)` — may block up to stoppingPollTimeout (8s) inside the EC2Resumer; the 5s goroutine context cancels it early in worst case, but the message is already enqueued so this is acceptable. Consider bumping to 15s for the resume goroutine to allow the stopping→stopped poll to complete.
2. `h.StatusWriter.SetStatusRunning(bgCtx, info.SandboxID)` — one DDB UpdateItem call, fast.
3. `h.PauseHinter.PostIfCooldownExpired(bgCtx, ch, ts)` with the appropriate hint text.

All three in sequence, in the goroutine. The goroutine returns 200 immediately (same contract as today).

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| EC2 instance start + stopping poll | Custom EC2 client logic | Copy `EC2Resumer` from `pkg/github/bridge/aws_adapters.go` | Already has the `stopping` → poll → `stopped` logic, correct tag key, sentinel error |
| DDB status flip | Custom UpdateItem | Copy `DynamoSandboxStatusWriter` from `pkg/github/bridge/aws_adapters.go` | Already uses expression attribute names for reserved word `status`, UpdateItem not PutItem |
| Pause hint posting | New Slack posting code | Reuse `DDBPauseHinter.PostIfCooldownExpired` with new `HintText` values | Cooldown logic, DDB LWT race handling already battle-tested |

---

## Common Pitfalls

### Pitfall 1: Wrong EC2 Tag Key (`{prefix}:sandbox-id` instead of `km:sandbox-id`)

**What goes wrong:** `DescribeInstances` finds 0 instances → `ErrNoResumableInstance` → degraded hint for every paused sandbox, even genuinely paused ones.

**Why it happens:** Phase 109 `e6b9ca75` / `d8007920` fixed the GitHub bridge after this exact bug. The km CLI hardcodes `tag:km:sandbox-id` regardless of `resource_prefix`; the bridge must mirror this. The `EC2Resumer.ResourcePrefix` field is now inert on the GitHub struct (the `sandboxIDTagKey()` method always returns `"km:sandbox-id"`).

**How to avoid:** Copy the GitHub `EC2Resumer` struct including `sandboxIDTagKey()` verbatim. Do NOT derive the tag key from `ResourcePrefix`. The `ResourcePrefix` field is kept inert for wiring compatibility (main.go still sets it for documentation).

**Warning signs:** Every paused sandbox gets the degraded hint; CloudWatch shows `EC2Resumer.DescribeInstances for <id>` returning 0 reservations despite the instance existing.

### Pitfall 2: PutItem Instead of UpdateItem for Status Flip

**What goes wrong:** `SetStatusRunning` using `PutItem` strips all DynamoDB attributes not present in the struct being marshalled — removes `slack_channel_id`, `slack_inbound_queue_url`, `last_pause_hint_ts`, `sandbox_id`, etc. The next `FetchByChannel` finds no channel match; the sandbox appears to not exist.

**Why it happens:** The `SandboxMetadata` struct in `pkg/aws` doesn't include all attributes the bridge writes. A full-row `PutItem` is destructive. This is the "SandboxMetadata lossy round-trip" footgun documented in project memory.

**How to avoid:** Always `UpdateItem` with an `UpdateExpression` targeting only `#st = :running`. Use `ExpressionAttributeNames: {"#st": "status"}` because `status` is a DynamoDB reserved word. Mirror `DynamoSandboxStatusWriter.SetStatusRunning` exactly.

### Pitfall 3: Resume on Non-Dispatched Messages (Spurious Wakeups)

**What goes wrong:** A non-mention message in a mention-only channel wakes the box.

**Why it happens:** If the resume branch is placed BEFORE step 5b (mention filter) or BEFORE step 8 (enqueue), it fires on messages that wouldn't be dispatched.

**How to avoid:** The existing code structure already places step 9 AFTER step 8 (`events_handler.go:455-471`). The resume replaces the existing step-9 block. As long as it stays in that position, only dispatchable messages trigger resume. The design is correct by structure.

### Pitfall 4: Missing EC2 Client in `init()` vs `wireEventsHandler()`

**What goes wrong:** `wireEventsHandler()` is called from `main()` after `init()`. If `ec2.NewFromConfig(cfg)` is called inside `wireEventsHandler()`, the `cfg` variable from `init()` is not in scope (it's a local var in `init()`). The build fails or requires passing `cfg` as a parameter.

**How to avoid:** Add `var initEC2Client *ec2.Client` as a package-level variable alongside `initDDB`, `initSQSClient`, etc. (`main.go:62-70`). Initialize it in `init()` with `initEC2Client = ec2.NewFromConfig(cfg)` alongside the other AWS client constructions at lines 80-82. Then reference `initEC2Client` in `wireEventsHandler()`.

### Pitfall 5: `--sidecars` vs `km init --slack` Deploy Confusion

**What goes wrong:** Operator runs `km init --sidecars` expecting the new IAM grant to take effect. It doesn't. Bridge Lambda still fails `ec2:StartInstances` with `UnauthorizedOperation`.

**Why it happens:** `--sidecars` rebuilds binaries and forces Lambda cold-start, but does NOT update the Lambda's IAM role policy (the `ec2_resume` policy). The IAM grant requires a full terragrunt apply of the `lambda-slack-bridge` module, which is what `km init --slack` or `km init --dry-run=false` performs.

**How to avoid:** Document in PLAN.md and operator notes: deploy = `make build-lambdas` + `km init --slack`. The EC2 client in the binary won't be called if the IAM grant is absent — it will just log `UnauthorizedOperation` errors and fall through to the existing enqueue path (fail-soft). But the feature won't work until IAM is applied.

### Pitfall 6: Two `DynamoUpdateItemClient` Interfaces — Wrong One Used

**What goes wrong:** `DynamoSandboxStatusWriter` in `pkg/slack/bridge` is accidentally wired with the GitHub bridge's `DynamoUpdateItemClient` interface, causing a compile error or import cycle.

**How to avoid:** The Slack bridge's `DDBUpdateItemAPI` (`aws_adapters.go:1233`) already has `UpdateItem`. Define `DynamoSandboxStatusWriter` in `pkg/slack/bridge/aws_adapters.go` using `DDBUpdateItemAPI` (not `DynamoUpdateItemClient` from the GitHub bridge). The `*dynamodb.Client` satisfies both; only the interface name differs.

---

## Code Examples

### EC2Resumer Port Target (from `pkg/github/bridge/aws_adapters.go:480-626`)

```go
// Source: pkg/github/bridge/aws_adapters.go:480-508 (interface + struct)
// Port verbatim to pkg/slack/bridge/aws_adapters.go with "slack-bridge:" prefix in error strings.

var ErrNoResumableInstance = errors.New("slack-bridge: no resumable EC2 instance")

type EC2StartAPI interface {
    DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
    StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
}

type EC2Resumer struct {
    Client          EC2StartAPI
    SandboxIDTagKey string // default "km:sandbox-id" when empty
    ResourcePrefix  string // INERT: retained for wiring compat, never read
}
```

Critical: the `sandboxIDTagKey()` method ALWAYS returns `"km:sandbox-id"` regardless of `ResourcePrefix`. The `stopping` poll loop (stoppingPollInterval=2s, stoppingPollTimeout=8s) must be copied — a fast pause → mention hits this path.

### DynamoSandboxStatusWriter Port Target (from `pkg/github/bridge/aws_adapters.go:637-668`)

```go
// Source: pkg/github/bridge/aws_adapters.go:637-668
// Port to pkg/slack/bridge/aws_adapters.go.
// ONLY SetStatusRunning — no DeleteSandboxRow (no cold-create in Slack bridge).

type DynamoSandboxStatusWriter struct {
    Client    DDBUpdateItemAPI  // slack bridge already has this interface
    TableName string            // e.g. "km-sandboxes"
}

func (w *DynamoSandboxStatusWriter) SetStatusRunning(ctx context.Context, sandboxID string) error {
    _, err := w.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
        TableName: awssdk.String(w.TableName),
        Key: map[string]dynamodbtypes.AttributeValue{
            "sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
        },
        UpdateExpression: awssdk.String("SET #st = :running"),
        ExpressionAttributeNames: map[string]string{"#st": "status"},
        ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
            ":running": &dynamodbtypes.AttributeValueMemberS{Value: "running"},
        },
    })
    if err != nil {
        return fmt.Errorf("slack-bridge: SetStatusRunning for %s: %w", sandboxID, err)
    }
    return nil
}
```

Note: the GitHub `DynamoSandboxStatusWriter` also has `DeleteSandboxRow`. The Slack version omits it — the Slack `SandboxStatusWriter` interface has only `SetStatusRunning`.

### EventsHandler Step-9 Replacement

```go
// Source: events_handler.go:455-471 (current step 9) — REPLACE with:

// 9. Resume-or-hint: if sandbox is paused and we have a Resumer, start the instance.
//    Message is already enqueued at step 8 — resume failure never strands the prompt.
//    Fire-and-forget goroutine so Handle returns 200 within Slack's 3s ack window.
if info.Paused {
    ch, ts, sid := msg.Channel, threadTS, info.SandboxID
    go func() {
        bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
        defer cancel()
        if h.Resumer != nil {
            err := h.Resumer.StartSandbox(bgCtx, sid)
            if err != nil && errors.Is(err, ErrNoResumableInstance) {
                // Orphan: instance gone. v1 has no cold-create. Post degraded hint.
                h.log().Warn("events: auto-resume: no resumable instance (orphaned row)",
                    "sandbox", sid, "channel", ch)
                if h.PauseHinter != nil {
                    // Use a distinct hint text for the orphan path.
                    // The HintText field is set at wire time; two hinter instances
                    // or a switchable-text variant handles the two messages.
                    _ = h.OrphanHinter.PostIfCooldownExpired(bgCtx, ch, ts)
                }
            } else {
                if err != nil {
                    h.log().Warn("events: auto-resume: transient StartInstances error (enqueue continues)",
                        "sandbox", sid, "err", err)
                }
                // Success OR transient error: flip status optimistically (fail-soft).
                if h.StatusWriter != nil {
                    if werr := h.StatusWriter.SetStatusRunning(bgCtx, sid); werr != nil {
                        h.log().Warn("events: auto-resume: SetStatusRunning failed (non-fatal)",
                            "sandbox", sid, "err", werr)
                    }
                }
                // Wake hint.
                if h.PauseHinter != nil {
                    if herr := h.PauseHinter.PostIfCooldownExpired(bgCtx, ch, ts); herr != nil {
                        h.log().Warn("events: auto-resume: wake hint post failed",
                            "err", herr, "channel", ch, "thread_ts", ts)
                    }
                }
            }
        } else {
            // Nil Resumer: byte-identical to today (pause-hint only).
            if h.PauseHinter != nil {
                if err := h.PauseHinter.PostIfCooldownExpired(bgCtx, ch, ts); err != nil {
                    h.log().Warn("events: pause hint post failed", "err", err, "channel", ch, "thread_ts", ts)
                }
            }
        }
    }()
}
```

**Discretion note on two hint variants:** Two `DDBPauseHinter` instances with different `HintText` is the cleanest approach — no new fields, each instance is independently configurable, and the cooldown is shared at the DDB row level (both write `last_pause_hint_ts` on the same sandbox row, so the first to win suppresses the other — correct behavior). `PauseHinter` carries the "waking up" text; `OrphanHinter` (a new optional field of the same type) carries the degraded text.

Alternatively: one `DDBPauseHinter` with a method that accepts the text per call. But the interface is `PostIfCooldownExpired(ctx, channelID, threadTS string) error` — no text parameter. Extending the interface breaks existing adapters. Two instances is cleaner.

### IAM Policy for Slack Bridge TF Module

```hcl
# Source: infra/modules/lambda-github-bridge/v1.1.0/main.tf:205-239 (MIRROR)
# Add to: infra/modules/lambda-slack-bridge/v1.0.0/main.tf

resource "aws_iam_role_policy" "ec2_resume" {
  name = "${local.function_name}-ec2-resume"
  role = aws_iam_role.slack_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "EC2DescribeInstances"
        Effect   = "Allow"
        Action   = ["ec2:DescribeInstances"]
        Resource = "*"
      },
      {
        Sid      = "EC2StartInstances"
        Effect   = "Allow"
        Action   = ["ec2:StartInstances"]
        Resource = "arn:aws:ec2:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:instance/*"
        Condition = {
          StringEquals = {
            "aws:ResourceTag/km:resource-prefix" = var.resource_prefix
          }
        }
      }
    ]
  })
}
```

`var.resource_prefix` is CONFIRMED to exist at `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf:61`. No new variable needed. The `data.aws_region.current` and `data.aws_caller_identity.current` data sources are already declared at lines 1-2 of `main.tf`.

---

## Confirmed Findings (Resolving Design Spec "Open Items")

### Confirmed: `var.resource_prefix` Exists in Slack Module

**Evidence:** `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf:61` declares `variable "resource_prefix"`. Already used in `main.tf` for the SQS resource pattern (`${var.resource_prefix}-slack-inbound-*.fifo` at line 148) and for the function name (`${var.resource_prefix}-slack-bridge` in locals). No addition needed.

### Confirmed: No EC2 Client in `cmd/km-slack-bridge/main.go` Today

**Evidence:** The package-level vars at `main.go:62-70` are `initDDB`, `initSSMC`, `initS3Client`, `initSQSClient`, `initPoster`, `initToken`, `initHTTPClient`, `initNonces`. No EC2 client. `init()` constructs only DDB, SSM, S3, SQS clients. A new `var initEC2Client *ec2.Client` must be added and initialized in `init()`.

### Confirmed: Slack Simplification Holds

**Evidence:** `aws_adapters.go:1051-1059` shows `FetchByChannel` already reads `info.SandboxID` from `item["sandbox_id"]` AND sets `info.Paused = v.Value == "paused" || v.Value == "stopped"`. Both values are in hand at the existing step-9 branch. The `events_handler.go:459` already uses `info.SandboxID` in the PauseHinter call (indirectly via `DDBPauseHinter.PostIfCooldownExpired` which re-queries by channelID). For the resume, use `info.SandboxID` directly — no re-query needed.

### Confirmed: `dynamodb:UpdateItem` on `km-sandboxes` Already Granted

**Evidence:** `infra/modules/lambda-slack-bridge/v1.0.0/main.tf:187-215`, the `dynamodb_sandboxes_pause_hint` policy, explicitly grants `dynamodb:UpdateItem` on `var.sandboxes_table_arn`. This is the grant used by `DDBPauseHinter` for writing `last_pause_hint_ts`. `SetStatusRunning` issues a different `UpdateItem` (updates `status`) on the same table with the same key — no new DDB grant needed.

### Confirmed: `DDBUpdateItemAPI` Is Sufficient for `DynamoSandboxStatusWriter`

**Evidence:** `aws_adapters.go:1233-1236` defines `DDBUpdateItemAPI` with `UpdateItem`. The Slack `DynamoSandboxStatusWriter` only needs `UpdateItem` (no `DeleteItem` — no orphan delete in the Slack bridge). `DDBUpdateItemAPI` is exactly the right interface. `initDDB` (`*dynamodb.Client`) satisfies it.

### Confirmed: Back-Compat Test Pattern Exists

**Evidence:** `events_handler_test.go:694-715` (`TestEventsHandler_PausedSandbox_NilHinter_IsNoop`) is the existing pattern for the nil-field back-compat invariant. The new test `TestEventsHandler_NilResumer_PauseHintOnly` mirrors this: set `h.Resumer = nil`, set `h.PauseHinter` to a mock, post a paused-sandbox message, verify: 200 response, SQS write happened, PauseHinter called, no crash. This locks the byte-identical invariant.

---

## Files Touched

| File | Change | Notes |
|------|--------|-------|
| `pkg/slack/bridge/events_interfaces.go` | Add `SandboxResumer` interface + `SandboxStatusWriter` interface | Slack-scoped subset of GitHub interfaces (no `DeleteSandboxRow`) |
| `pkg/slack/bridge/aws_adapters.go` | Add `EC2StartAPI` interface, `var ErrNoResumableInstance`, `EC2Resumer`, `DynamoSandboxStatusWriter` | Near-verbatim port; "slack-bridge:" error prefixes; `DDBUpdateItemAPI` for status writer |
| `pkg/slack/bridge/events_handler.go` | Replace step-9 with resume-or-hint branch; add `Resumer SandboxResumer`, `StatusWriter SandboxStatusWriter`, `OrphanHinter PauseHintPoster` fields | All optional/nil-safe; nil Resumer → byte-identical today |
| `cmd/km-slack-bridge/main.go` | Add `var initEC2Client *ec2.Client`; initialize in `init()`; wire `Resumer`/`StatusWriter`/`OrphanHinter` in `wireEventsHandler()` | Add `"github.com/aws/aws-sdk-go-v2/service/ec2"` import |
| `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` | Add `aws_iam_role_policy.ec2_resume` (additive, in-place edit) | No version bump needed (additive policy); `var.resource_prefix` already exists |
| `pkg/slack/bridge/events_handler_resume_test.go` (new) | Unit tests for resume branch (5 scenarios) | Mirror `pkg/github/bridge/webhook_handler_phase109_test.go` |
| `docs/slack-notifications.md` | Add § Phase 114 | Operator runbook + UAT steps |

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go standard `testing` package (project-wide) |
| Config file | none (no pytest.ini/jest equivalent) |
| Quick run command | `go test ./pkg/slack/bridge/... -count=1 -timeout 600s -run Resume` |
| Full suite command | `go test ./... -count=1 -timeout 600s` |
| Exit code discipline | Capture `$?` or use `pipefail`; never pipe into `tail` (masks FAIL per project memory `feedback_check_go_test_exit_not_pipe`) |

### Phase Requirements → Test Map

| Behavior | Test Name | Test Type | Automated Command |
|----------|-----------|-----------|-------------------|
| Paused sandbox: `StartSandbox` called + `SetStatusRunning` called + SQS enqueued | `TestEventsHandler_PausedSandbox_Resumes` | unit | `go test ./pkg/slack/bridge/... -run TestEventsHandler_PausedSandbox_Resumes -count=1` |
| `ErrNoResumableInstance` → degraded hint + no status flip + still enqueued | `TestEventsHandler_PausedSandbox_OrphanDegrades` | unit | `go test ./pkg/slack/bridge/... -run TestEventsHandler_PausedSandbox_OrphanDegrades -count=1` |
| Transient `StartInstances` error → enqueue + status flip attempted + no crash | `TestEventsHandler_PausedSandbox_TransientError` | unit | `go test ./pkg/slack/bridge/... -run TestEventsHandler_PausedSandbox_TransientError -count=1` |
| Running sandbox → no `StartSandbox` called (warm path unchanged) | `TestEventsHandler_RunningSandbox_NoResume` | unit | `go test ./pkg/slack/bridge/... -run TestEventsHandler_RunningSandbox_NoResume -count=1` |
| `Resumer == nil` → byte-identical to today (pause-hint only, no crash) | `TestEventsHandler_NilResumer_PauseHintOnly` | unit | `go test ./pkg/slack/bridge/... -run TestEventsHandler_NilResumer_PauseHintOnly -count=1` |
| `EC2Resumer.StartSandbox` tag filter = `km:sandbox-id` (not `{prefix}:sandbox-id`) | `TestEC2Resumer_UsesKmSandboxIdTag` | unit | `go test ./pkg/slack/bridge/... -run TestEC2Resumer_UsesKmSandboxIdTag -count=1` |
| `EC2Resumer.StartSandbox` returns `ErrNoResumableInstance` when 0 instances found | `TestEC2Resumer_NoInstances_ReturnsErrNoResumable` | unit | `go test ./pkg/slack/bridge/... -run TestEC2Resumer_NoInstances -count=1` |
| `DynamoSandboxStatusWriter.SetStatusRunning` uses `UpdateItem` not `PutItem` | `TestDynamoSandboxStatusWriter_UsesUpdateItem` | unit | `go test ./pkg/slack/bridge/... -run TestDynamoSandboxStatusWriter -count=1` |
| Build compiles with new imports | compile gate | build | `make build-lambdas` |
| Whole-repo regression: suite stays green | regression | build | `go test ./... -count=1 -timeout 600s` |
| Live: `km pause` + Slack message → `StartInstances` in CloudWatch + row flip to `running` | E2E UAT | manual | See UAT steps below |

### E2E UAT (Operator-Driven, Requires AWS Login)

The Go goldens cannot verify IAM grants or real EC2 behavior. The following is the ONLY validation path for the full feature (cf. project memory: skill-bash live UAT requirement extends to any Lambda path touching real AWS APIs).

```
Pre-condition: deploy complete (make build-lambdas + km init --slack applied)

1. Identify a Slack-enabled sandbox (has slack_inbound_queue_url in km-sandboxes).
   km list --wide  →  pick one with a Slack channel bound

2. PAUSED path:
   km pause <id>
   km list  →  confirm state=paused
   Post a Slack message in the sandbox channel (top-level @-mention if mention-only enabled)
   Observe: bridge CloudWatch logs show "events: auto-resume" + StartInstances call
   Observe: "Sandbox is waking up" hint appears in Slack thread
   km list  →  confirm state transitions to running (or pending/starting briefly)
   Wait ~90s for poller to boot and drain the FIFO
   Observe: agent reply lands in Slack thread

3. STOPPED path:
   km stop <id>
   km list  →  confirm state=stopped
   Post another Slack message
   Observe same signals as step 2

4. ORPHAN path (optional, destructive):
   km stop <id>
   Terminate the EC2 instance out from under km (AWS Console or aws ec2 terminate-instances)
   DO NOT km destroy (leaves the DDB row in paused/stopped state)
   Post a Slack message
   Observe: "couldn't auto-resume; ask an operator to recreate" hint in Slack
   Observe: DDB row still present (km list still shows the sandbox)

5. WARM path regression (no spurious resume):
   km resume <id>  →  confirm state=running
   Post a Slack message
   Observe: NO StartInstances call in CloudWatch (warm path unchanged)

6. MENTION-ONLY guard (no spurious wakeup):
   km pause <id>  on a mention-only channel
   Post a non-mention message in the channel
   Observe: NO StartInstances call, NO hint (message filtered at step 5b before step 9)
```

### Sampling Rate

- **Per task commit:** `go test ./pkg/slack/bridge/... -count=1 -timeout 600s`
- **Per wave merge:** `go test ./... -count=1 -timeout 600s`
- **Phase gate (before `/gsd:verify-work`):** Full suite green + E2E UAT steps 2-5 complete

### Wave 0 Gaps

- [ ] `pkg/slack/bridge/events_handler_resume_test.go` — new file; covers all 5 unit scenarios + EC2Resumer + DynamoSandboxStatusWriter unit tests
- [ ] `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` — `aws_iam_role_policy.ec2_resume` block

*(No new framework install needed — Go standard library.)*

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|---|---|---|---|
| Slack paused sandbox → hint only, never starts | Slack paused sandbox → auto-resume (Phase 114) | Now | Operators no longer need to manually `km resume` after a Slack message |
| GitHub/H1 resume: alias → status-aware resolver → StartInstances | Slack resume: channel → FetchByChannel (already has status) → StartInstances | Phase 114 | Simpler; no new resolver needed |
| EC2 tag `{prefix}:sandbox-id` (broken on non-km prefix) | EC2 tag `km:sandbox-id` (fixed in Phase-109 e6b9ca75) | Phase 109 merge d8007920 | Copy fix is baked into the port target |

---

## Open Questions

1. **Goroutine timeout for resume path**
   - What we know: existing PauseHinter goroutine uses 5s (`context.WithTimeout(context.Background(), 5*time.Second)` at `events_handler.go:465`). The `stopping` poll in `EC2Resumer` is up to 8s (stoppingPollTimeout).
   - What's unclear: if the goroutine context cancels at 5s while the stopping-poll is mid-loop, `StartInstances` is never called. The message is still enqueued (safe), but the resume intent is lost for this event (the next message retries).
   - Recommendation: bump the resume goroutine context to 15s. The existing `aws_lambda_function.slack_bridge` timeout is 60s; 15s is well within budget. The Slack 3s ack window is already satisfied by returning 200 synchronously; the goroutine timeout is unconstrained by Slack.

2. **`OrphanHinter` field name and implementation choice**
   - What we know: two hint texts needed ("waking up" vs "couldn't auto-resume"). Two `DDBPauseHinter` instances with different `HintText` is the cleanest approach given the existing interface.
   - What's unclear: whether `OrphanHinter` should be a separate field on `EventsHandler` or whether the degraded path should just log and skip the hint.
   - Recommendation: add `OrphanHinter PauseHintPoster` as a separate optional field on `EventsHandler`. Wire a second `DDBPauseHinter` in `wireEventsHandler()` with orphan text. If nil, the orphan path logs only (still correct behavior). This mirrors the clarity of `PauseHinter`/`OrphanHinter` being distinctly configured.

---

## Sources

### Primary (HIGH confidence)

- `pkg/github/bridge/aws_adapters.go:480-626` — `EC2Resumer` implementation (port source)
- `pkg/github/bridge/aws_adapters.go:637-695` — `DynamoSandboxStatusWriter` implementation (port source)
- `pkg/github/bridge/interfaces.go:34-63` — `SandboxResumer` + `SandboxStatusWriter` interface definitions (port source)
- `pkg/github/bridge/webhook_handler_phase109_test.go` — test patterns to mirror
- `pkg/github/bridge/webhook_handler_phase98_04_test.go:21-63` — mock struct patterns (`mockSandboxResumer`, `mockSandboxStatusWriter`)
- `pkg/slack/bridge/events_handler.go:27-471` — EventsHandler struct, step-9 block at L455-471
- `pkg/slack/bridge/events_interfaces.go` — existing Slack bridge interfaces (no `SandboxResumer` yet)
- `pkg/slack/bridge/aws_adapters.go:1040-1098` — `FetchByChannel` paused detection at L1058-1059
- `pkg/slack/bridge/aws_adapters.go:1224-1350` — `DDBUpdateItemAPI` + `DDBPauseHinter`
- `cmd/km-slack-bridge/main.go:62-70,204-325` — package-level vars, `wireEventsHandler()`, PauseHinter wiring
- `cmd/km-github-bridge/main.go:85,195-198` — EC2 client construction pattern
- `infra/modules/lambda-slack-bridge/v1.0.0/main.tf:1-407` — full TF module (confirmed `data.aws_region.current` L2, `aws_iam_role.slack_bridge.id` L14, no `ec2_resume` policy yet)
- `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf:61` — `variable "resource_prefix"` confirmed
- `infra/modules/lambda-github-bridge/v1.1.0/main.tf:205-239` — `ec2_resume` IAM policy to mirror

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — no new libraries; all existing AWS SDK v2 patterns
- Architecture: HIGH — verified file:line references, confirmed all 5 design spec assumptions
- Pitfalls: HIGH — Phase-109 bug history is documented in project memory and commit history; lossy round-trip is documented in multiple project memory entries
- IAM: HIGH — `var.resource_prefix` confirmed at variables.tf:61; existing `dynamodb:UpdateItem` grant confirmed at main.tf:187-215

**Research date:** 2026-06-15
**Valid until:** 2026-07-15 (stable AWS SDK v2 APIs; no fast-moving ecosystem)
