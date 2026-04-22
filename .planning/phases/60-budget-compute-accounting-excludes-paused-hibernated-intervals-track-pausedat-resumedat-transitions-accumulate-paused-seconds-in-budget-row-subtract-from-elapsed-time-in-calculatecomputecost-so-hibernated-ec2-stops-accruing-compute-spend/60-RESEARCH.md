# Phase 60: Budget Compute Accounting Excludes Paused/Hibernated Intervals — Research

**Researched:** 2026-04-21
**Domain:** Go / DynamoDB budget accounting / EC2 pause-resume lifecycle / Lambda cost calculation
**Confidence:** HIGH — all findings are from direct code inspection of the codebase; no external libraries required

---

## Summary

The current compute cost calculation in `cmd/budget-enforcer/main.go:calculateComputeCost` computes:

```
cost = spotRate * (time.Since(createdAt).Minutes() / 60.0)
```

This is pure wall-clock since creation. It is **unaware of paused intervals**, so hibernated or stopped EC2 instances continue accruing compute budget as if they were running. AWS does not charge for stopped EC2 instances (only EBS), so the budget is overcounting.

The fix tracks cumulative paused time as a `pausedSeconds` (int64, atomic ADD) field on the `BUDGET#compute` DynamoDB row. Every pause transition writes `pausedAt` (open interval start), and every resume transition closes the interval by adding `(now - pausedAt)` to `pausedSeconds` and clearing `pausedAt`. The budget-enforcer then reads `pausedSeconds` from DynamoDB and subtracts it from elapsed before multiplying by rate.

**Primary recommendation:** Store `pausedAt` and `pausedSeconds` in the `BUDGET#compute` row (same row as `spentUSD`). On pause, SET `pausedAt = now`. On resume, ADD the closed interval to `pausedSeconds` and REMOVE `pausedAt`. In `calculateComputeCost`, accept `pausedSeconds int64` as a parameter (injected from the fetched `BUDGET#compute` row) and subtract it before the rate multiplication.

---

## Transition Points Inventory (ALL Code Paths That Pause/Resume)

This is the most critical finding. Every path must write `pausedAt` / update `pausedSeconds`.

### Pause Paths

| Path | File | What happens today | Hook needed |
|------|------|--------------------|-------------|
| `km pause` (operator CLI) | `internal/app/cmd/pause.go:170` | Calls `EC2.StopInstances`, then `UpdateSandboxStatusAndClearTTL(ctx, dynamo, table, sandboxID, "paused")` | **ADD**: call `RecordPauseStart(ctx, budgetClient, budgetTable, sandboxID, now)` after EC2 stop succeeds |
| `km at 'time' pause` (scheduled) | Routes through `cmd/ttl-handler/main.go:handleStop` (event_type="stop" maps to `pause` in at.go:45) | Same as handleStop below | Same hook in handleStop |
| TTL-handler idle-hibernate (audit-log fires IDLE_ACTION=hibernate → EventBridge "stop") | `cmd/ttl-handler/main.go:handleStop` | `StopInstances` (hibernate), then `UpdateSandboxStatusAndClearTTL(ctx, dynamo, table, id, "paused")` at line 250 | **ADD**: call `RecordPauseStart` after EC2 stop succeeds |
| Budget-enforcer exhaustion (compute or AI) | `cmd/budget-enforcer/main.go:enforceBudgetCompute` | `StopInstances`, then `UpdateSandboxStatusDynamo(ctx, dynamo, table, id, "paused")` at line 456 | **ADD**: call `RecordPauseStart` after EC2 stop succeeds. **CRITICAL**: must NOT double-count — budget-enforcer called at 100% means cost is already at limit; recording `pausedAt` prevents further accrual on subsequent Lambda invocations. |
| `km shell --learn` exit or explicit km pause from within? | No special path found — uses same `km pause` CLI | N/A | N/A |

### Resume Paths

| Path | File | What happens today | Hook needed |
|------|------|--------------------|-------------|
| `km resume` (operator CLI) | `internal/app/cmd/resume.go:117` | `StartInstances`, then `UpdateSandboxStatusDynamo(ctx, dynamo, table, id, "running")` | **ADD**: call `RecordResumeClose(ctx, budgetClient, budgetTable, sandboxID, now)` after EC2 start succeeds |
| `km at 'time' resume` (scheduled) | Routes to `cmd/ttl-handler/main.go:handleResume` | `StartInstances`, then `UpdateSandboxStatusDynamo(ctx, dynamo, table, id, "running")` | Same hook in handleResume |
| `km budget add` (top-up auto-resume) | `internal/app/cmd/budget.go:resumeEC2Sandbox` | `StartInstances` via `ec2Client` | **ADD**: call `RecordResumeClose` after start |
| TTL-handler `handleResume` (event_type="resume") | `cmd/ttl-handler/main.go:handleResume:310-313` | `UpdateSandboxStatusDynamo(ctx, dynamo, table, id, "running")` | Same hook in handleResume |
| TTL-handler agent-run auto-start | `cmd/ttl-handler/main.go:handleAgentRun` line ~464 | `StartInstances` inline, then `UpdateSandboxStatusDynamo` at ~494 | **ADD**: call `RecordResumeClose` after start |

### Paths Confirmed Out of Scope

- **Spot interruption**: AWS reclaims the spot instance by terminating it, not stopping it. The instance goes to `terminated` state. This is not a pause — the sandbox is gone. The budget enforcer handles this via `ErrSandboxNotFound` self-delete. No pause tracking applies.
- **ECS Fargate stop**: ECS tasks are ephemeral (terminated, not stopped). No pause accounting for ECS — this phase only addresses EC2.
- **Docker substrate**: Docker pause routes through `docker-compose pause` in `runDockerCompose`. No EC2/DynamoDB involvement. Out of scope — Docker sandboxes have no compute budget.

---

## Field Placement Decision

### Option A: `BUDGET#compute` row (RECOMMENDED)

Add to the existing `BUDGET#compute` item (`PK=SANDBOX#{id}, SK=BUDGET#compute`):
- `pausedSeconds` (Number) — cumulative seconds paused so far (closed intervals only)
- `pausedAt` (String, RFC3339) — open interval start; present only while currently paused; absent when running

**Why this row**: `setComputeSpend` already touches this row every minute. `calculateComputeCost` needs these values at the same time it writes `spentUSD`. Fetching both in one item read is cheaper and simpler than a cross-table join.

**Current key:** `PK = SANDBOX#{sandboxID}`, `SK = "BUDGET#compute"`, field `spentUSD`

**Extended key stays identical:** Add `pausedSeconds` (Number, default 0) and `pausedAt` (String, optional).

**Why not sandbox metadata row (km-sandboxes table):** The budget enforcer Lambda only has `BudgetAPI` (DynamoDB client on km-budgets table) and `SandboxMetadataAPI` (for lock check and status). Adding a cross-table read for pause accounting introduces complexity. The budget table is already the source of truth for all spend data.

**Why not a separate `BUDGET#pause` row:** The accumulator and the open interval timestamp need to be readable together in one query. `GetBudget` already reads all `BUDGET#*` rows with a begins_with query — a separate row would be automatically included. Either approach works, but co-locating with `BUDGET#compute` makes the read path in `calculateComputeCost` simpler (already reading that row to write spentUSD).

---

## DynamoDB Write Patterns

### Recording a Pause Start (pause transition)

```go
// Source: pattern derived from existing setComputeSpend (cmd/budget-enforcer/main.go:263)
// Called from ALL pause paths after EC2.StopInstances or docker-compose pause succeeds.
func RecordPauseStart(ctx context.Context, client BudgetAPI, tableName, sandboxID string, now time.Time) error {
    pk := fmt.Sprintf("SANDBOX#%s", sandboxID)
    _, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
        TableName: aws.String(tableName),
        Key: map[string]dynamodbtypes.AttributeValue{
            "PK": &dynamodbtypes.AttributeValueMemberS{Value: pk},
            "SK": &dynamodbtypes.AttributeValueMemberS{Value: "BUDGET#compute"},
        },
        // SET pausedAt only if it's not already set (idempotent: double-pause is a no-op)
        UpdateExpression: aws.String("SET pausedAt = if_not_exists(pausedAt, :now)"),
        ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
            ":now": &dynamodbtypes.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339)},
        },
    })
    return err
}
```

**Note:** Using `if_not_exists(pausedAt, :now)` makes the write idempotent. If `km pause` is called twice (e.g., retried), the first `pausedAt` is preserved and the interval length is correctly calculated from the first pause event.

### Recording a Resume (close the open interval)

```go
// Source: pattern derived from IncrementAISpend (pkg/aws/budget.go:76) which uses ADD.
// Called from ALL resume paths after EC2.StartInstances succeeds.
func RecordResumeClose(ctx context.Context, client BudgetAPI, tableName, sandboxID string, now time.Time) error {
    pk := fmt.Sprintf("SANDBOX#%s", sandboxID)

    // Step 1: Read current pausedAt (conditional on its existence).
    // If pausedAt is absent, this sandbox was not tracked (pre-Phase-60 or already running).
    item, err := client.GetItem(ctx, &dynamodb.GetItemInput{
        TableName: aws.String(tableName),
        Key: map[string]dynamodbtypes.AttributeValue{
            "PK": &dynamodbtypes.AttributeValueMemberS{Value: pk},
            "SK": &dynamodbtypes.AttributeValueMemberS{Value: "BUDGET#compute"},
        },
        ProjectionExpression: aws.String("pausedAt"),
    })
    if err != nil || item.Item == nil {
        return nil // non-fatal: safe to skip on read failure
    }

    pausedAtAV, ok := item.Item["pausedAt"]
    if !ok {
        return nil // no open interval — sandbox was not paused (or legacy sandbox)
    }
    var pausedAtStr string
    if err := attributevalue.Unmarshal(pausedAtAV, &pausedAtStr); err != nil {
        return nil
    }
    pausedAt, err := time.Parse(time.RFC3339, pausedAtStr)
    if err != nil {
        return nil
    }

    intervalSeconds := int64(now.Sub(pausedAt).Seconds())
    if intervalSeconds < 0 {
        intervalSeconds = 0
    }

    // Step 2: ADD interval to pausedSeconds, REMOVE pausedAt (close the interval).
    intervalAV, _ := attributevalue.Marshal(intervalSeconds)
    _, err = client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
        TableName: aws.String(tableName),
        Key: map[string]dynamodbtypes.AttributeValue{
            "PK": &dynamodbtypes.AttributeValueMemberS{Value: pk},
            "SK": &dynamodbtypes.AttributeValueMemberS{Value: "BUDGET#compute"},
        },
        UpdateExpression: aws.String("ADD pausedSeconds :interval REMOVE pausedAt"),
        ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
            ":interval": intervalAV,
        },
    })
    return err
}
```

**DynamoDB ADD semantics**: If `pausedSeconds` attribute is absent (new sandbox or legacy), ADD initializes it to the value (`:interval`). This is the same semantics as `IncrementAISpend` and `IncrementComputeSpend` — consistent with the project's ADD pattern.

---

## Cost Calculation Change

### Current (line 241-255, main.go)

```go
func (h *BudgetHandler) calculateComputeCost(event BudgetCheckEvent) (float64, error) {
    createdAt, err := time.Parse(time.RFC3339, event.CreatedAt)
    ...
    elapsedMinutes := time.Since(createdAt).Minutes()
    cost := event.SpotRate * (elapsedMinutes / 60.0)
    return cost, nil
}
```

### Required Change

`calculateComputeCost` needs to accept `pausedSeconds int64` (or read it from DynamoDB). Two approaches:

**Approach A (recommended):** Accept `pausedSeconds` as a parameter. The handler fetches the `BUDGET#compute` row to read `pausedSeconds` before calling `calculateComputeCost`. This keeps the function pure and easily testable.

```go
// Source: modified from current calculateComputeCost
func (h *BudgetHandler) calculateComputeCost(event BudgetCheckEvent, pausedSeconds int64) (float64, error) {
    createdAt, err := time.Parse(time.RFC3339, event.CreatedAt)
    if err != nil {
        return 0, fmt.Errorf("parse created_at %q: %w", event.CreatedAt, err)
    }
    elapsedSecs := time.Since(createdAt).Seconds()
    if elapsedSecs < 0 {
        elapsedSecs = 0
    }
    // Subtract accumulated pause time. Open-interval adjustment handled separately.
    billableSecs := elapsedSecs - float64(pausedSeconds)
    if billableSecs < 0 {
        billableSecs = 0
    }
    cost := event.SpotRate * (billableSecs / 3600.0)
    return cost, nil
}
```

**Open-interval adjustment:** When `HandleBudgetCheck` fires while the sandbox is currently paused (`pausedAt` is set, `pausedSeconds` reflects only closed intervals), we need to also subtract the open interval `(now - pausedAt)`. This prevents the budget from ticking during a current pause:

```go
// In HandleBudgetCheck, before calling calculateComputeCost:
computeRow := h.fetchComputeRow(ctx, sandboxID) // reads BUDGET#compute item
pausedSecs := computeRow.PausedSeconds
if computeRow.PausedAt != nil {
    // Sandbox is currently paused — add the open interval
    pausedSecs += int64(time.Since(*computeRow.PausedAt).Seconds())
}
elapsedCost, err := h.calculateComputeCost(event, pausedSecs)
```

This ensures the budget enforcer never charges for the current pause window even before `resumeClose` is called.

---

## Idempotency Preservation

The current `setComputeSpend` uses `SET spentUSD = :cost` — this is a full overwrite and is already idempotent (recalculate from CreatedAt each invocation). The new design preserves this:

- `setComputeSpend` continues to SET `spentUSD` each invocation.
- `pausedSeconds` and `pausedAt` are only written at **transition points** (pause/resume hooks), not by the budget enforcer Lambda.
- The budget enforcer Lambda **reads** `pausedSeconds` from DynamoDB each invocation and uses it in `calculateComputeCost`. Since `calculateComputeCost` is called from scratch with `time.Since(createdAt)`, the SET of `spentUSD` remains idempotent.

The split works: accumulator writes (ADD on transitions) are independent from the cost overwrite (SET on schedule). No race condition between them — they write different attributes on the same item.

---

## BudgetAPI Interface Extension

The current `BudgetAPI` interface in `pkg/aws/budget.go:27` has:

```go
type BudgetAPI interface {
    UpdateItem(...)
    GetItem(...)
    Query(...)
}
```

`RecordResumeClose` requires a `GetItem` call to read `pausedAt` before computing the interval. `BudgetAPI` already includes `GetItem`, so no interface change is needed.

However, the pause/resume hooks are called from **non-budget-enforcer code** (pause.go, resume.go, ttl-handler). These callers need access to a `BudgetAPI` client pointed at the **km-budgets** DynamoDB table. Currently they only use a DynamoDB client for the km-sandboxes table (`SandboxMetadataAPI`).

**Field additions needed:**
1. `internal/app/cmd/pause.go`: add `budgetClient awspkg.BudgetAPI` and `budgetTable string` (or derive from cfg)
2. `internal/app/cmd/resume.go`: same
3. `cmd/ttl-handler/main.go:TTLHandler`: add `BudgetClient awspkg.BudgetAPI` and `BudgetTable string` fields
4. `cmd/budget-enforcer/main.go`: add `fetchComputeRow` helper; pass `pausedSeconds` to `calculateComputeCost`

The `cfg.BudgetTableName` is already available in config (seen in budget.go) and the DynamoDB client can be shared with or separate from the sandbox metadata client (both are just `*dynamodb.Client`).

---

## Race Conditions and Worst-Case Error Bounds

### Pause race: status written, EC2 not yet stopped

1. `runPause` calls `StopInstances` — request accepted (async EC2 state change)
2. `runPause` calls `RecordPauseStart` — writes `pausedAt = now`
3. Budget-enforcer Lambda fires 0-60 seconds later
4. Lambda reads `pausedAt`, subtracts open interval — charges stop as of step 2

**Worst case:** EC2 remains running up to 30 seconds after `pausedAt` is recorded (typical EC2 stop latency). Budget overcounts 30 seconds × spotRate. At $0.10/hr this is $0.0000833 (< $0.0001). **Acceptable.**

### Resume race: EC2 started, status written, budget-enforcer fires mid-transition

1. `runResume` calls `StartInstances` — async
2. `runResume` calls `RecordResumeClose` — writes `pausedSeconds += interval`, REMOVE `pausedAt`
3. Budget-enforcer fires 0-60 seconds later — no `pausedAt`, charges from step 2 onward

**Worst case:** EC2 starts billing 30 seconds before `RecordResumeClose` is written. Budget undercounts 30 seconds. At $0.10/hr this is $0.0000833. **Acceptable.**

### Double-pause

If `km pause` is called twice: `if_not_exists(pausedAt, :now)` preserves the first `pausedAt`. Second call is a no-op. **Safe.**

### Budget-enforcer triggers pause (enforceBudgetCompute)

The Lambda calls `StopInstances` then `UpdateSandboxStatusDynamo(..., "paused")`. We add `RecordPauseStart` at this same point. On the **next Lambda invocation** (1 minute later), `pausedAt` is present, open interval is subtracted, and `spentUSD` is correctly computed at the budget-exhaustion level. This prevents the spend from continuing to climb past the limit due to elapsed time during the stopped period. **Correct.**

---

## Migration / Backfill

Existing sandboxes have no `pausedSeconds` or `pausedAt` attribute on `BUDGET#compute`. Behavior with missing fields:

- `pausedSeconds` absent → `ADD` initializes to 0 on first resume. Before first resume, `fetchComputeRow` returns 0 (attribute missing defaults to 0). **Current behavior preserved.**
- `pausedAt` absent → `RecordResumeClose` reads no `pausedAt`, returns nil early. No interval accumulation. **Safe.**
- `calculateComputeCost(event, pausedSecs=0)` → same as current behavior. **Backward compatible.**

No DynamoDB table schema migration required. DynamoDB's schemaless nature handles missing attributes as 0/nil naturally.

---

## BudgetSummary and GetBudget Changes

`GetBudget` queries all `BUDGET#*` items. The `BUDGET#compute` case currently reads only `spentUSD`. It needs to also read `pausedSeconds` and `pausedAt`:

```go
// In pkg/aws/budget.go GetBudget, BUDGET#compute case (currently line 188-191):
case sk == "BUDGET#compute":
    var spend float64
    if av, ok := item["spentUSD"]; ok {
        _ = attributevalue.Unmarshal(av, &spend)
    }
    summary.ComputeSpent = spend
    // NEW:
    if av, ok := item["pausedSeconds"]; ok {
        _ = attributevalue.Unmarshal(av, &summary.PausedSeconds)
    }
    if av, ok := item["pausedAt"]; ok {
        var s string
        if _ = attributevalue.Unmarshal(av, &s); s != "" {
            t, _ := time.Parse(time.RFC3339, s)
            summary.PausedAt = &t
        }
    }
```

And `BudgetSummary` gains two fields:

```go
type BudgetSummary struct {
    ComputeSpent     float64
    ComputeLimit     float64
    AISpent          float64
    AILimit          float64
    WarningThreshold float64
    AIByModel        map[string]ModelSpend
    LastAIActivity   *time.Time
    // NEW:
    PausedSeconds    int64      // cumulative closed pause intervals
    PausedAt         *time.Time // non-nil when currently paused (open interval)
}
```

These fields flow to `HandleBudgetCheck` which already calls `GetBudget`. No separate fetch needed.

---

## Common Pitfalls

### Pitfall 1: Budget-enforcer reading stale pausedSeconds

**What goes wrong:** Lambda reads `BUDGET#compute` item, sees `pausedSeconds=0`, then pause hook writes `pausedAt` 50ms later. Lambda uses `pausedSecs=0` for this invocation.
**Impact:** One Lambda tick (1 minute) charges for a moment that's actually paused.
**Mitigation:** The open-interval adjustment (`if pausedAt != nil { pausedSecs += now - pausedAt }`) means the Lambda reads the open interval from the same item atomically. Race window is only between `StopInstances` succeeding and `RecordPauseStart` writing — bounded to ~30s maximum. Cost impact < $0.0001. Acceptable.

### Pitfall 2: Forgetting the open-interval adjustment

**What goes wrong:** Lambda only uses `pausedSeconds` (closed intervals). Sandbox paused 2 hours ago. Budget-enforcer fires. `pausedAt` is set but not read. Charges 2 hours of pause time.
**Prevention:** Always check `pausedAt` in `HandleBudgetCheck` and add `now - pausedAt` to `pausedSeconds` before passing to `calculateComputeCost`. This is the most important logic in the whole feature.

### Pitfall 3: Resume hook missing from one code path

**What goes wrong:** Agent-run auto-start (`handleAgentRun`) calls `StartInstances` but `RecordResumeClose` is never called. Budget resumes ticking but `pausedAt` stays set, so the open-interval adjustment continuously over-subtracts.
**Prevention:** Audit ALL code paths in the transition inventory above. Each one must call the appropriate hook.

### Pitfall 4: ECS sandboxes treated same as EC2

**What goes wrong:** Pause hook is called for ECS task stop (task is ephemeral, terminated, never resumed the same way).
**Prevention:** The pause hook must only fire for EC2 substrate. Guard with `if substrate == "ec2"` checks at each call site. ECS pause accounting is out of scope for this phase.

### Pitfall 5: Negative billableSecs causes negative cost

**What goes wrong:** Clock drift or a future `pausedAt` causes `billableSecs < 0`.
**Prevention:** `billableSecs = max(0, billableSecs)` guard in `calculateComputeCost`. Already present for `elapsedMinutes < 0` check — extend the same guard.

### Pitfall 6: BudgetTableName not configured in pause/resume callers

**What goes wrong:** `runPause` in `pause.go` doesn't have `cfg.BudgetTableName` wired; budget hook silently uses wrong table name.
**Prevention:** `cfg.BudgetTableName` already exists (used in `budget.go:138`). Pass it through or default to `"km-budgets"` the same way the budget enforcer does.

---

## Architecture Patterns

### Existing Patterns to Follow

| Pattern | Location | Apply in Phase 60 |
|---------|----------|-------------------|
| Narrow DI interface | `pkg/aws/budget.go:BudgetAPI` | Pause/resume hooks accept `BudgetAPI` not `*dynamodb.Client` |
| ADD for accumulation | `pkg/aws/budget.go:IncrementAISpend` | `ADD pausedSeconds :interval` in RecordResumeClose |
| SET for idempotent overwrite | `cmd/budget-enforcer/main.go:setComputeSpend` | SET spentUSD stays unchanged |
| if_not_exists for idempotent init | N/A (new pattern) | `SET pausedAt = if_not_exists(pausedAt, :now)` |
| Non-fatal warn-and-continue | All lifecycle paths | Pause/resume hooks are non-fatal |
| isMetaFlagFn / setMetaFlagFn injection | `cmd/budget-enforcer/main.go` | `calculateComputeCost` accepts `pausedSeconds` param for test injection |
| getBudgetFn injection | `cmd/budget-enforcer/main.go:115` | Add parallel `getComputeRowFn` for test injection of pause state |

### Recommended Project Structure Changes

No new files required. Changes are additive to existing files:

```
pkg/aws/budget.go                    — BudgetSummary fields + GetBudget BUDGET#compute case
cmd/budget-enforcer/main.go          — calculateComputeCost signature + fetchComputeRow + RecordPauseStart call
internal/app/cmd/pause.go            — RecordPauseStart hook after StopInstances
internal/app/cmd/resume.go           — RecordResumeClose hook after StartInstances
internal/app/cmd/budget.go           — RecordResumeClose hook in resumeEC2Sandbox
cmd/ttl-handler/main.go              — RecordPauseStart in handleStop, RecordResumeClose in handleResume + handleAgentRun
```

Helper functions `RecordPauseStart` and `RecordResumeClose` belong in `pkg/aws/budget.go` (consistent with `IncrementAISpend`, `IncrementComputeSpend` etc.).

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Atomic accumulation | Custom read-modify-write | DynamoDB ADD expression | Already used for AI spend; handles concurrent writes |
| Idempotent open-interval start | Custom deduplication | `if_not_exists` in UpdateExpression | Native DynamoDB — no extra read needed |
| Clock injection for tests | Global time.Now() | Parameter injection (pausedSeconds as param) | Already established pattern in tests; `budgetCheckEvent()` helper sets CreatedAt offset |

---

## Validation Architecture

Nyquist validation is enabled (`workflow.nyquist_validation: true` in `.planning/config.json`).

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./cmd/budget-enforcer/... ./pkg/aws/... ./internal/app/cmd/... -count=1` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map

| Req | Behavior | Test Type | Automated Command | File Exists? |
|-----|----------|-----------|-------------------|--------------|
| PAUSE-ACCT-01 | `calculateComputeCost` with `pausedSeconds=30m` returns cost only for non-paused time | unit | `go test ./cmd/budget-enforcer/... -run TestCalculateComputeCost_SubtractsPausedTime` | No — Wave 0 |
| PAUSE-ACCT-02 | `calculateComputeCost` with `pausedSeconds=0` returns same as current behavior (backward compat) | unit | `go test ./cmd/budget-enforcer/... -run TestCalculateComputeCost_ZeroPausedSecsUnchanged` | No — Wave 0 |
| PAUSE-ACCT-03 | `calculateComputeCost` with open-interval (`pausedAt` set, `pausedSeconds=0`) returns 0 (sandbox currently paused) | unit | `go test ./cmd/budget-enforcer/... -run TestCalculateComputeCost_OpenIntervalReturnsZeroBillable` | No — Wave 0 |
| PAUSE-ACCT-04 | `RecordPauseStart` writes `pausedAt` with `if_not_exists` semantics | unit | `go test ./pkg/aws/... -run TestRecordPauseStart_WritesIfNotExists` | No — Wave 0 |
| PAUSE-ACCT-05 | `RecordPauseStart` called twice preserves original `pausedAt` | unit | `go test ./pkg/aws/... -run TestRecordPauseStart_Idempotent` | No — Wave 0 |
| PAUSE-ACCT-06 | `RecordResumeClose` with `pausedAt=1h ago` ADDs 3600 to `pausedSeconds` and REMOVEs `pausedAt` | unit | `go test ./pkg/aws/... -run TestRecordResumeClose_AccumulatesInterval` | No — Wave 0 |
| PAUSE-ACCT-07 | `RecordResumeClose` with no `pausedAt` is a no-op (legacy sandbox safe) | unit | `go test ./pkg/aws/... -run TestRecordResumeClose_NoPausedAtIsNoop` | No — Wave 0 |
| PAUSE-ACCT-08 | Multiple pause/resume cycles accumulate correctly | unit | `go test ./pkg/aws/... -run TestMultiplePauseResumeCycles` | No — Wave 0 |
| PAUSE-ACCT-09 | `GetBudget` populates `BudgetSummary.PausedSeconds` and `PausedAt` from DynamoDB item | unit | `go test ./pkg/aws/... -run TestGetBudget_PopulatesPausedFields` | No — Wave 0 |
| PAUSE-ACCT-10 | Budget enforcer `HandleBudgetCheck` uses `pausedSeconds` from DynamoDB in cost calculation | integration (mock) | `go test ./cmd/budget-enforcer/... -run TestBudgetHandler_PausedSandboxDoesNotAccrueSpend` | No — Wave 0 |
| PAUSE-ACCT-11 | `billableSecs` never goes negative (guard) | unit | `go test ./cmd/budget-enforcer/... -run TestCalculateComputeCost_NeverNegative` | No — Wave 0 |

### Sampling Rate

- **Per task commit:** `go test ./cmd/budget-enforcer/... ./pkg/aws/... -count=1`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `cmd/budget-enforcer/main_test.go` — add `TestCalculateComputeCost_*` tests (pausedSeconds parameter injection)
- [ ] `pkg/aws/budget_test.go` — add `TestRecordPauseStart_*`, `TestRecordResumeClose_*`, `TestMultiplePauseResumeCycles`, `TestGetBudget_PopulatesPausedFields`
- [ ] `cmd/budget-enforcer/main_test.go` — add `TestBudgetHandler_PausedSandboxDoesNotAccrueSpend` (uses `getBudgetFn` injection with `PausedAt` set)

No new framework install required — stdlib `testing` is already used throughout.

---

## Open Questions

1. **`km budget add` resume path in ttl-handler vs budget.go**
   - What we know: `km budget add` calls `resumeEC2Sandbox` in `budget.go` locally. Scheduled `budget-add` events go to `handleBudgetAdd` in `ttl-handler` which does NOT start EC2 (only updates limits). The EC2 resume for scheduled budget-add is deferred to the operator running `km resume` manually.
   - What's unclear: Should scheduled `budget-add` auto-resume EC2 and call `RecordResumeClose`? Currently it does not start EC2. If the feature is added later, the hook should be added there too.
   - Recommendation: Do not add auto-resume to scheduled `budget-add` in this phase. Only wire the hook where EC2 start actually happens.

2. **`km resume` uses S3 fallback for metadata updates but not for budget hooks**
   - What we know: `resume.go:117-135` has an S3 fallback for `UpdateSandboxStatusDynamo` if the DynamoDB table doesn't exist (`ResourceNotFoundException`). The budget hook would target the budget table (km-budgets), not the sandbox table — there's no S3 fallback for budget.
   - What's unclear: Should `RecordResumeClose` also be skipped if the sandbox table is gone?
   - Recommendation: `RecordResumeClose` targets km-budgets, not km-sandboxes. If km-budgets doesn't exist, the UpdateItem will fail. Make the hook non-fatal (log warn + continue), same as all other budget operations.

---

## Sources

### Primary (HIGH confidence)

- Direct code inspection of `cmd/budget-enforcer/main.go` (full file read)
- Direct code inspection of `pkg/aws/budget.go` (full file read — BudgetSummary, UpdateExpression patterns)
- Direct code inspection of `internal/app/cmd/pause.go` (full file read — pause transition)
- Direct code inspection of `internal/app/cmd/resume.go` (full file read — resume transition)
- Direct code inspection of `cmd/ttl-handler/main.go` (lines 1-700 read — handleStop, handleResume, handleAgentRun, budget-add)
- Direct code inspection of `internal/app/cmd/budget.go` (full file read — resumeEC2Sandbox)
- Direct code inspection of `cmd/budget-enforcer/main_test.go` (full file read — existing mock patterns)
- Direct code inspection of `pkg/aws/metadata.go` (SandboxMetadata schema)
- Direct code inspection of `pkg/aws/sandbox_dynamo.go` (UpdateSandboxStatusDynamo, UpdateSandboxStatusAndClearTTL)
- DynamoDB `ADD` expression behavior with missing attributes: initialized to value (AWS SDK behavior, consistent with existing `IncrementAISpend` and `IncrementComputeSpend` patterns in codebase)
- DynamoDB `if_not_exists()` function for conditional SET: standard DynamoDB UpdateExpression function

### Secondary (MEDIUM confidence)

- `.planning/STATE.md` decisions log — confirmed existing design decisions (SET vs ADD for budget enforcer, DynamoDB key design)
- `.planning/ROADMAP.md` — Phase 60 description confirms agreed-upon approach (Option A)

---

## Metadata

**Confidence breakdown:**
- Transition point inventory: HIGH — direct code inspection of all 5 files containing pause/resume paths
- DynamoDB write patterns: HIGH — derived from existing patterns in same files (IncrementAISpend, setComputeSpend)
- Open-interval handling: HIGH — derived from clear code path analysis
- Race condition bounds: HIGH — EC2 stop/start latency is well-understood
- Migration safety: HIGH — DynamoDB missing-attribute behavior is consistent with existing ADD usage in codebase

**Research date:** 2026-04-21
**Valid until:** 2026-05-21 (codebase changes would invalidate transition point inventory)
