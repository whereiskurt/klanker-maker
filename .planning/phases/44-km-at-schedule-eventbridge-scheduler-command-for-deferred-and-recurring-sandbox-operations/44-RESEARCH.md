# Phase 44: km at/schedule — Research

**Researched:** 2026-04-02
**Domain:** EventBridge Scheduler CLI commands, natural language time parsing, DynamoDB schedule state
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Command Syntax**
- `km at '<time-expr>' <command> [args...]` — primary interface
- `km schedule` as alias
- Examples: `km at '10pm tomorrow' create dev.yaml`, `km at 'every thursday at 3PM' kill gg1 --remote`

**Time Expression Parsing**
- Natural language parsing via Go library (e.g. `olebedev/when` or similar) → converted to EventBridge `at()` and `cron()` expressions
- `--cron '<expr>'` flag for raw EventBridge cron expressions as alternative to natural language
- One-time schedules use `at(YYYY-MM-DDThh:mm:ss)` format (already used by TTL handler)
- Recurring schedules use EventBridge `cron()` format

**Supported Commands**
- ALL remote-capable commands: create, destroy/kill, stop, pause, resume, extend
- Commands dispatch via existing EventBridge event patterns (SandboxCreate, SandboxIdle)

**Schedule Naming**
- Auto-generated names from command + target + time (e.g., `kill-gg1-thu-3pm`)
- Optional `--name <name>` override for user-friendly names
- Names must be unique within the EventBridge Scheduler namespace

**Schedule Management**
- `km at list` — show all scheduled operations (from DynamoDB)
- `km at cancel <name>` — cancel a scheduled operation (delete from EventBridge Scheduler + DynamoDB)
- Anyone can list/cancel any schedule (no per-operator scoping)

**Recurring + Sandbox Resolution**
- Recurring commands that reference a sandbox ID resolve gracefully at fire-time
- If sandbox is gone, the scheduled execution fails gracefully (log warning, don't error-loop)
- Recurring create commands spin up new sandbox each trigger

**State Tracking**
- Store schedule metadata in DynamoDB (schedule name, command, time expression, creator, created-at, status)
- Enables `km at list` without calling EventBridge ListSchedules API
- Sync state: DynamoDB is source of truth for listing; EventBridge Scheduler is source of truth for execution

**Max Sandbox Guardrail**
- Max active sandbox count set during `km init`, adjustable via re-init
- Stored in platform config (DynamoDB or km-config.yaml)
- Recurring create schedules check max before provisioning; skip with warning if at limit
- This protects against runaway recurring creates

**Infrastructure**
- Reuse existing EventBridge Scheduler patterns from TTL handler (`pkg/aws/scheduler.go`)
- Reuse existing remote publisher pattern for command dispatch
- Schedule target: existing create-handler Lambda (for creates) and TTL handler Lambda (for lifecycle commands)
- Schedule IAM role: reuse existing scheduler role (KM_SCHEDULER_ROLE_ARN)

### Claude's Discretion
(None specified — all major decisions locked)

### Deferred Ideas (OUT OF SCOPE)
- Schedule notifications (email/Slack when a scheduled command fires)
- Schedule dry-run mode
- Schedule approval workflows (require confirmation before firing)
- Calendar view of upcoming schedules in ConfigUI
</user_constraints>

---

## Summary

Phase 44 adds `km at '<time-expr>' <command> [args...]` — a command scheduler that converts natural language time expressions to EventBridge Scheduler at()/cron() rules. The architecture is a thin CLI layer over existing infrastructure: the EventBridge Scheduler SDK already in `pkg/aws/scheduler.go`, the TTL handler Lambda (handles destroy/stop/extend via SandboxIdle events), and the create-handler Lambda (handles creates via SandboxCreate events). The main new work is: (1) natural language time parsing, (2) a new DynamoDB table `km-schedules` for schedule metadata, (3) the `km at` Cobra command with `list` and `cancel` subcommands, and (4) extending the `SchedulerAPI` interface with `ListSchedules` and `GetSchedule`.

The critical architectural insight is that **`km at ... create`** dispatches a `SandboxCreate` EventBridge event (same as `km create --remote`), while **all other commands** dispatch a `SandboxIdle` event with the appropriate `event_type` (same as `km destroy --remote`, `km stop --remote`, etc.). The existing Lambda handlers already process these event shapes without modification.

**Primary recommendation:** Use `olebedev/when` for natural language one-time date parsing and implement a custom lightweight recurring-phrase parser (e.g., "every thursday at 3pm") that emits EventBridge `cron()` expressions. The `--cron` flag bypasses parsing entirely for power users.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/scheduler` | v1.17.21 (already in go.mod) | EventBridge Scheduler CRUD | Already used for TTL schedules |
| `github.com/aws/aws-sdk-go-v2/service/dynamodb` | v1.57.0 (already in go.mod) | Schedule metadata table | Already used for sandbox metadata |
| `github.com/aws/aws-sdk-go-v2/service/eventbridge` | v1.45.22 (already in go.mod) | Publish events to existing Lambdas | Already used for PublishSandboxCommand |
| `github.com/olebedev/when` | v1.1.0 | Natural language date/time → time.Time | Best-maintained Go NL date parser; November 2024 release |
| `github.com/spf13/cobra` | v1.9.1 (already in go.mod) | CLI subcommand tree | Standard for km |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/tj/go-naturaldate` | unversioned | Alternative NL parser | If `olebedev/when` is insufficient; lighter weight |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `olebedev/when` | `tj/go-naturaldate` | tj's library has fewer supported expressions; no recurring support either |
| Custom recurring parser | LLM/AI for parsing | LLM adds latency and cost; simple regex covers all real cases |
| DynamoDB for schedule listing | EventBridge `ListSchedules` API | API requires pagination + IAM; DynamoDB is faster and already wired |

**Installation:**
```bash
go get github.com/olebedev/when@v1.1.0
```

---

## Architecture Patterns

### Recommended Project Structure
```
internal/app/cmd/
├── at.go                  # km at command + list/cancel subcommands
├── at_test.go             # unit tests with mock scheduler + DynamoDB
pkg/aws/
├── scheduler.go           # extend SchedulerAPI with ListSchedules/GetSchedule
├── schedules_dynamo.go    # NEW: km-schedules DynamoDB table CRUD
├── schedules_dynamo_test.go
pkg/at/
├── parser.go              # NEW: natural language time → ScheduleSpec (at/cron expression)
├── parser_test.go
```

### Pattern 1: Command Registration (mirrors other commands)
**What:** Register `km at` with `km schedule` alias; nest `list` and `cancel` as subcommands.
**When to use:** All new commands follow this pattern per `root.go`.
**Example:**
```go
// internal/app/cmd/root.go additions:
atCmd := NewAtCmd(cfg)
atCmd.AddCommand(NewAtListCmd(cfg))
atCmd.AddCommand(NewAtCancelCmd(cfg))
root.AddCommand(atCmd)
root.AddCommand(&cobra.Command{
    Use:   "schedule",
    Short: "Alias for 'km at'",
    RunE:  atCmd.RunE,
})
```

### Pattern 2: Injected Publisher (same as destroy/extend/stop)
**What:** Decouple AWS calls via `RemoteCommandPublisher` interface for testability.
**When to use:** Any command that publishes to EventBridge.
```go
func NewAtCmdWithPublisher(cfg *config.Config, pub RemoteCommandPublisher) *cobra.Command
```

### Pattern 3: EventBridge Scheduler CreateScheduleInput for km at
**What:** Build `scheduler.CreateScheduleInput` targeting existing Lambdas.
**When to use:** All `km at` schedule creation.

For **lifecycle commands** (destroy, stop, pause, resume, extend), target the TTL handler Lambda with a `SandboxIdle` event payload embedded in the schedule `Target.Input`:
```go
// Source: pkg/compiler/lifecycle.go pattern (BuildTTLScheduleInput)
&scheduler.CreateScheduleInput{
    Name:                       aws.String("km-at-kill-gg1-20260410-150000"),
    ScheduleExpression:         aws.String("at(2026-04-10T15:00:00)"),  // or cron(...)
    ScheduleExpressionTimezone: aws.String("UTC"),
    Target: &types.Target{
        Arn:     aws.String(ttlLambdaARN),   // km-ttl-handler ARN
        RoleArn: aws.String(schedulerRoleARN),
        Input:   aws.String(`{"sandbox_id":"gg1","event_type":"destroy"}`),
        RetryPolicy: &types.RetryPolicy{MaximumRetryAttempts: aws.Int32(0)},
    },
    FlexibleTimeWindow: &types.FlexibleTimeWindow{Mode: types.FlexibleTimeWindowModeOff},
    // One-time: ActionAfterCompletion = DELETE
    // Recurring: ActionAfterCompletion = NONE (EventBridge ignores DELETE for recurring)
    ActionAfterCompletion: types.ActionAfterCompletionDelete,
}
```

For **create commands**, target the create-handler Lambda with `SandboxCreate` event payload:
```go
// Input JSON mirrors SandboxCreateDetail from pkg/aws/eventbridge.go
Input: aws.String(`{"sandbox_id":"...","artifact_bucket":"...","artifact_prefix":"...","on_demand":false}`)
```

For **recurring create schedules**, a new sandbox ID must be generated at fire-time. The simplest approach: the create-handler Lambda already accepts `sandbox_id` in the payload — for recurring creates, generate a fresh ID at schedule creation time and embed it. **However**: recurring creates must generate a fresh ID each trigger. This means the TTL handler Lambda needs a thin new event type or the create-handler Lambda needs to generate the sandbox ID when `sandbox_id` is absent. See Open Questions.

### Pattern 4: SchedulerAPI Interface Extension
**What:** Extend the existing `SchedulerAPI` interface in `pkg/aws/scheduler.go` with `GetSchedule` and `ListSchedules`.
**When to use:** `km at list` can call EventBridge for live schedule data (if needed); DynamoDB is the primary listing path.

```go
// pkg/aws/scheduler.go — extended interface
type SchedulerAPI interface {
    CreateSchedule(ctx context.Context, input *scheduler.CreateScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.CreateScheduleOutput, error)
    DeleteSchedule(ctx context.Context, input *scheduler.DeleteScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.DeleteScheduleOutput, error)
    ListSchedules(ctx context.Context, input *scheduler.ListSchedulesInput, optFns ...func(*scheduler.Options)) (*scheduler.ListSchedulesOutput, error)
    GetSchedule(ctx context.Context, input *scheduler.GetScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error)
}
// *scheduler.Client already satisfies all four methods.
// Existing mockSchedulerAPI in tests must add stub implementations for the two new methods.
```

**PITFALL:** Adding methods to the existing `SchedulerAPI` interface breaks the mock in `pkg/aws/scheduler_test.go`. The mock must be updated with stub `ListSchedules` and `GetSchedule` methods.

### Pattern 5: DynamoDB Schedule Metadata Table
**What:** New table `km-schedules` (or extend `km-sandboxes` with a sort key prefix `schedule#`). Separate table is cleaner given distinct access patterns.
**Key design:**
```
schedule_name (S) — hash key
created_at (S) — for sorting in list output
status (S) — "active", "completed", "cancelled"
command (S) — "kill", "create", "stop", etc.
sandbox_id (S, omitempty) — target sandbox (absent for create commands)
time_expr (S) — original human expression (for display)
cron_expr (S) — resolved EventBridge expression
creator (S, omitempty) — optional, for audit
```

DynamoDB CRUD follows existing `pkg/aws/sandbox_dynamo.go` patterns: manual `attributevalue` marshalling, no json tag fallback, explicit `AttributeValueMemberS/N/BOOL`.

### Anti-Patterns to Avoid
- **Calling `ListSchedules` API for `km at list`:** Slow, paginated, requires IAM; use DynamoDB scan instead.
- **Using `ActionAfterCompletion: DELETE` for recurring schedules:** EventBridge ignores it for cron/rate expressions — no harm but misleading; use `NONE` for recurring.
- **Nesting `km schedule` as a full Cobra command tree duplicate:** Instead, alias by creating a minimal cobra.Command that delegates to the same RunE. Don't duplicate the entire subtree.
- **Storing original natural language expressions in EventBridge schedule description field:** The description field on EventBridge Scheduler is optional and not returned by `ListSchedules` summary — store in DynamoDB instead.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Natural language date parsing | Custom regex/parser | `olebedev/when` | Handles "tonight", "next tuesday 14:00", timezone awareness, edge cases |
| EventBridge Scheduler API calls | Raw HTTP | `aws-sdk-go-v2/service/scheduler` | Already imported, versioned, type-safe |
| Schedule uniqueness enforcement | Custom mutex/lock | EventBridge: 64-char name uniqueness is enforced at API level; return 409 ConflictException | AWS enforces uniqueness within schedule group |
| Max sandbox check | New logic | Existing `checkSandboxLimit` in `internal/app/cmd/create.go` | Already handles max_sandboxes from config |

**Key insight:** The hard parts (event dispatch, Lambda invocation, sandbox lifecycle) are already solved. This phase is glue code that converts time expressions to EventBridge schedule inputs and tracks them in DynamoDB.

---

## Common Pitfalls

### Pitfall 1: Recurring Creates Need Fresh Sandbox IDs
**What goes wrong:** If a recurring `km at 'every thursday' create dev.yaml` embeds a fixed sandbox ID in the Target.Input, the second Thursday fires with the same ID — either colliding with an existing sandbox or (if destroyed) re-using a deleted ID.
**Why it happens:** The current create flow pre-generates sandbox ID on the CLI before passing to EventBridge.
**How to avoid:** For recurring create schedules, the Target.Input must NOT include `sandbox_id` (or include a sentinel value). The create-handler Lambda must be updated to generate a fresh sandbox ID when `sandbox_id` is missing or empty.
**Warning signs:** `km create` fails on the second invocation of a recurring create schedule with "sandbox already exists".

### Pitfall 2: EventBridge Cron Field Ordering (Not Standard Unix Cron)
**What goes wrong:** EventBridge Scheduler cron has 6 fields: `cron(minutes hours day-of-month month day-of-week year)`. Standard unix cron has 5 fields. Day-of-week in EventBridge uses `1=SUN, 2=MON, ... 5=THU, 7=SAT` (1-indexed Sunday). Standard cron uses `0=SUN`.
**Why it happens:** EventBridge uses AWS EventBridge cron syntax, not unix crontab syntax.
**How to avoid:** In the natural language → cron converter: Thursday = `5` (not `4`), and day-of-week field is 5th position, year is 6th. "Every thursday at 3PM" = `cron(0 15 ? * 5 *)`.
**Warning signs:** Schedules fire on the wrong day of week.

### Pitfall 3: Cannot Combine Day-of-Month and Day-of-Week
**What goes wrong:** `cron(0 15 15 * THU *)` is invalid. EventBridge requires `?` in whichever of day-of-month/day-of-week you don't intend to use.
**Why it happens:** AWS EventBridge cron specification.
**How to avoid:** When specifying day-of-week, always use `?` for day-of-month: `cron(0 15 ? * 5 *)`.
**Warning signs:** `ValidationException: Parameter ScheduleExpression is not valid.`

### Pitfall 4: Schedule Name Must Match `[0-9a-zA-Z-_.]+`, Max 64 Characters
**What goes wrong:** Auto-generated schedule names from `command + sandbox_id + timestamp` may contain characters not in the allowed set or exceed 64 characters.
**Why it happens:** EventBridge Scheduler name constraint.
**How to avoid:** Sanitize auto-generated names: replace spaces with `-`, strip disallowed characters, truncate to 64 chars. Sandbox IDs like `sb-a1b2c3d4` are safe; user-provided `--name` must be validated.
**Warning signs:** `ValidationException: Member must satisfy regular expression pattern: [0-9a-zA-Z-_.]+`

### Pitfall 5: `olebedev/when` Does Not Parse Recurring Expressions
**What goes wrong:** `when.Parse("every thursday at 3pm", time.Now())` returns nil — the library handles specific date/time references only, not recurring patterns.
**Why it happens:** `olebedev/when` is designed for single date/time extraction, not schedule recurrence.
**How to avoid:** Split parsing into two paths: (1) use `olebedev/when` for one-time expressions; (2) for recurring expressions (detected by "every", "each", "weekly", "daily", etc. keywords), implement a lightweight custom parser that emits EventBridge `cron()` expressions. The `--cron` flag bypasses both paths.
**Warning signs:** NL parser returns nil for recurring inputs but no error is shown.

### Pitfall 6: SchedulerAPI Interface Addition Breaks Existing Mocks
**What goes wrong:** Adding `ListSchedules` and `GetSchedule` to the `SchedulerAPI` interface in `pkg/aws/scheduler.go` causes `mockSchedulerAPI` in `scheduler_test.go` to fail compilation.
**Why it happens:** Go interface satisfaction is compile-time.
**How to avoid:** Update `mockSchedulerAPI` in `scheduler_test.go` with no-op stub implementations for the two new methods before adding them to the interface.
**Warning signs:** `does not implement SchedulerAPI (missing method ListSchedules)` compile error.

### Pitfall 7: `ActionAfterCompletion` for Recurring Schedules
**What goes wrong:** Setting `ActionAfterCompletion: DELETE` on a recurring (cron/rate) schedule has no effect — EventBridge Scheduler ignores it. The schedule persists after fire.
**Why it happens:** AWS API behavior: DELETE only applies to one-time schedules that have truly "completed".
**How to avoid:** For recurring schedules, use `ActionAfterCompletion: NONE`. For one-time (at()) schedules, use `DELETE` (same as TTL handler pattern).
**Warning signs:** Recurring schedules not being cleaned up when expected.

### Pitfall 8: max_sandboxes Check for Recurring Creates Must Happen at Lambda Fire-Time
**What goes wrong:** Checking `max_sandboxes` at `km at` schedule creation time won't prevent limit violations at the actual fire time (sandbox count may be different Thursday at 3PM).
**Why it happens:** Scheduling is deferred execution; create count check must happen when the create actually runs.
**How to avoid:** The create-handler Lambda (and existing `km create` flow) already calls `checkSandboxLimit`. For scheduled creates, this check runs at invocation time via the Lambda — no additional work needed. The CONTEXT.md guardrail is already satisfied by the existing Lambda flow.

---

## Code Examples

Verified patterns from official sources and codebase analysis:

### at() Expression (one-time schedule) — from compiler/lifecycle.go
```go
// Source: pkg/compiler/lifecycle.go (BuildTTLScheduleInput)
atExpr := "at(" + targetTime.UTC().Format("2006-01-02T15:04:05") + ")"
// Result: at(2026-04-10T15:00:00)
```

### cron() Expression for "Every Thursday at 3PM UTC"
```go
// Source: AWS EventBridge Scheduler docs (1=SUN, 2=MON, 3=TUE, 4=WED, 5=THU, 6=FRI, 7=SAT)
// format: cron(minutes hours day-of-month month day-of-week year)
cronExpr := "cron(0 15 ? * 5 *)"  // every Thursday at 15:00 UTC
```

### CreateScheduleInput for a lifecycle command (destroy)
```go
// Source: derived from pkg/compiler/lifecycle.go + pkg/aws/scheduler.go patterns
&scheduler.CreateScheduleInput{
    Name:                       aws.String("km-at-kill-sb-a1b2c3d4"),
    ScheduleExpression:         aws.String("at(2026-04-10T15:00:00)"),
    ScheduleExpressionTimezone: aws.String("UTC"),
    Target: &types.Target{
        Arn:     aws.String(ttlLambdaARN),
        RoleArn: aws.String(schedulerRoleARN),
        Input:   aws.String(`{"sandbox_id":"sb-a1b2c3d4","event_type":"destroy"}`),
        RetryPolicy: &types.RetryPolicy{MaximumRetryAttempts: aws.Int32(0)},
    },
    FlexibleTimeWindow: &types.FlexibleTimeWindow{Mode: types.FlexibleTimeWindowModeOff},
    ActionAfterCompletion: types.ActionAfterCompletionDelete,
}
```

### CreateScheduleInput for a recurring lifecycle command
```go
&scheduler.CreateScheduleInput{
    Name:                       aws.String("km-at-kill-sb-a1b2c3d4-thu-3pm"),
    ScheduleExpression:         aws.String("cron(0 15 ? * 5 *)"),
    ScheduleExpressionTimezone: aws.String("UTC"),
    Target: &types.Target{
        Arn:     aws.String(ttlLambdaARN),
        RoleArn: aws.String(schedulerRoleARN),
        Input:   aws.String(`{"sandbox_id":"sb-a1b2c3d4","event_type":"destroy"}`),
        RetryPolicy: &types.RetryPolicy{MaximumRetryAttempts: aws.Int32(0)},
    },
    FlexibleTimeWindow: &types.FlexibleTimeWindow{Mode: types.FlexibleTimeWindowModeOff},
    ActionAfterCompletion: types.ActionAfterCompletionNone,  // NOT DELETE for recurring
}
```

### olebedev/when usage (one-time date parsing)
```go
// Source: github.com/olebedev/when README
import (
    "github.com/olebedev/when"
    "github.com/olebedev/when/rules/common"
    "github.com/olebedev/when/rules/en"
)

w := when.New(nil)
w.Add(en.All...)
w.Add(common.All...)
r, err := w.Parse("10pm tomorrow", time.Now())
if err != nil || r == nil {
    return fmt.Errorf("could not parse time expression")
}
// r.Time is the parsed time.Time
targetTime := r.Time
```

### DynamoDB schedule record (follows sandbox_dynamo.go patterns)
```go
// Manually built map — no json tag fallback (per project pitfall)
item := map[string]dynamodbtypes.AttributeValue{
    "schedule_name": &dynamodbtypes.AttributeValueMemberS{Value: scheduleName},
    "command":       &dynamodbtypes.AttributeValueMemberS{Value: command},   // "kill", "create", etc.
    "time_expr":     &dynamodbtypes.AttributeValueMemberS{Value: humanExpr}, // "10pm tomorrow"
    "cron_expr":     &dynamodbtypes.AttributeValueMemberS{Value: awsExpr},   // "at(...)" or "cron(...)"
    "status":        &dynamodbtypes.AttributeValueMemberS{Value: "active"},
    "created_at":    &dynamodbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
}
// sandbox_id omitted when empty (create without fixed ID)
if sandboxID != "" {
    item["sandbox_id"] = &dynamodbtypes.AttributeValueMemberS{Value: sandboxID}
}
```

### ListSchedules with pagination (extended SchedulerAPI)
```go
// Source: pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/scheduler (ListSchedulesPaginator)
paginator := scheduler.NewListSchedulesPaginator(client, &scheduler.ListSchedulesInput{
    NamePrefix: aws.String("km-at-"),
})
for paginator.HasMorePages() {
    page, err := paginator.NextPage(ctx)
    if err != nil { return err }
    for _, s := range page.Schedules {
        // s.Name, s.ScheduleExpression, s.State
    }
}
```

---

## EventBridge Scheduler Cron Field Reference

| Field | Position | Values | Notes |
|-------|----------|--------|-------|
| minutes | 1 | 0-59 | |
| hours | 2 | 0-23 | UTC by default |
| day-of-month | 3 | 1-31 or `?` | Must be `?` when day-of-week is set |
| month | 4 | 1-12 or JAN-DEC | |
| day-of-week | 5 | 1-7 (1=SUN) or SUN-SAT | Must be `?` when day-of-month is set |
| year | 6 | 1970-2199 or `*` | Use `*` for recurring |

**Common day-of-week mappings (EventBridge notation):**
- SUN=1, MON=2, TUE=3, WED=4, THU=5, FRI=6, SAT=7
- Note: This differs from standard unix cron (0=SUN)

**Examples:**
- `cron(0 9 ? * 2 *)` — Every Monday at 9:00 AM
- `cron(30 14 ? * 6 *)` — Every Friday at 2:30 PM
- `cron(0 0 1 * ? *)` — First of every month at midnight

---

## Natural Language Parsing Strategy

Since `olebedev/when` handles one-time expressions but not recurring ones, use a split strategy:

```
input expression
    │
    ├─ contains "every" / "each" / "weekly" / "daily" / "monthly"?
    │         YES → custom recurring parser → cron() expression
    │
    ├─ --cron flag provided?
    │         YES → pass through directly → validate against EventBridge pattern
    │
    └─ default → olebedev/when → time.Time → at() expression
```

**Custom recurring parser** needs to handle a small well-defined set of patterns:
- `every <day-of-week> at <time>` → `cron(M H ? * DOW *)`
- `every day at <time>` → `cron(M H * * ? *)`
- `daily at <time>` → `cron(M H * * ? *)`
- `every <N> hours` → `rate(<N> hours)`
- `every <N> minutes` → `rate(<N> minutes)`

This is ~50 lines of Go regex matching — no additional library needed for the recurring case.

---

## Lambda Target Routing Summary

| km at command | Target Lambda | Event Shape |
|---------------|--------------|-------------|
| `create <profile>` | create-handler Lambda (km-create-handler) | `SandboxCreate` detail |
| `destroy <id>` / `kill <id>` | ttl-handler Lambda (km-ttl-handler) | `{"sandbox_id":"...","event_type":"destroy"}` |
| `stop <id>` | ttl-handler Lambda | `{"sandbox_id":"...","event_type":"stop"}` |
| `pause <id>` | ttl-handler Lambda | `{"sandbox_id":"...","event_type":"stop"}` (pause = stop for EC2) |
| `resume <id>` | ttl-handler Lambda | TBD — ttl-handler does not currently have resume event type |
| `extend <id> <dur>` | ttl-handler Lambda | `{"sandbox_id":"...","event_type":"extend","duration":"2h"}` |

**Note:** `pause` and `resume` may need Lambda-side handling. The current `TTLEvent.EventType` switch handles `stop`, `extend`, `destroy`/`idle`/`ttl` — but not `resume`. This needs investigation (see Open Questions).

---

## Existing Infrastructure Reuse

| Component | Location | How km at Uses It |
|-----------|----------|-------------------|
| `SchedulerAPI` interface | `pkg/aws/scheduler.go` | Extended with `ListSchedules`, `GetSchedule` |
| `CreateTTLSchedule` | `pkg/aws/scheduler.go` | Not used directly; `km at` calls `client.CreateSchedule` directly with richer input |
| `DeleteTTLSchedule` | `pkg/aws/scheduler.go` | Used by `km at cancel` (reuses same delete pattern) |
| `BuildTTLScheduleInput` | `pkg/compiler/lifecycle.go` | Inspiration for `BuildAtScheduleInput` in new `pkg/compiler/at.go` or inline in `pkg/aws/scheduler.go` |
| `RemoteCommandPublisher` | `internal/app/cmd/remote_publisher.go` | Pattern for testability; `km at` has different dispatch so implements its own publisher-like pattern |
| `PublishSandboxCommand` | `pkg/aws/idle_event.go` | Used when `km at ... destroy/stop/extend` needs to dispatch immediately (not via scheduler) |
| `PutSandboxCreateEvent` | `pkg/aws/eventbridge.go` | Used when `km at ... create` needs to dispatch immediately |
| `SandboxMetadataAPI` | `pkg/aws/sandbox_dynamo.go` | DynamoDB interface reused for schedule table CRUD |
| `checkSandboxLimit` | `internal/app/cmd/create.go` | Existing function; recurring creates use it at Lambda fire-time |
| `cfg.MaxSandboxes` | `internal/app/config/config.go` | Already loaded from `km-config.yaml`; used by `checkSandboxLimit` |
| `cfg.SchedulerRoleARN` | `internal/app/config/config.go` | Already in Config struct; used for schedule Target.RoleArn |

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| cron(5 fields) | cron(6 fields, year as 6th) | EventBridge Scheduler GA (2022) | Must include year field |
| EventBridge Events Rules (rate/cron) | EventBridge Scheduler | 2022 | Scheduler has per-target invocation, ActionAfterCompletion |
| Direct Lambda invocation | EventBridge Scheduler → Lambda | n/a | Consistent with existing TTL pattern |

---

## Open Questions

1. **Resume command support in ttl-handler Lambda**
   - What we know: `TTLEvent.EventType` handles `stop`, `extend`, `destroy`/`idle`/`ttl`
   - What's unclear: Does `resume` need Lambda support? `km resume` locally calls EC2 `StartInstances` — the Lambda would need to do the same
   - Recommendation: Either add `resume` case to ttl-handler Lambda (Phase 44 scope), or exclude `resume` from `km at` supported commands and document the limitation

2. **Recurring create: sandbox ID generation at fire-time**
   - What we know: create-handler Lambda currently requires `sandbox_id` in the event payload (validated: `if event.SandboxID == ""`).
   - What's unclear: Must the Lambda be modified to allow empty sandbox_id and generate one?
   - Recommendation: Add a `generate_id: true` field to `CreateEvent`. When present, the Lambda calls `GenerateSandboxID()` (already in the codebase) to create a fresh ID at fire-time. This is a small Lambda change.

3. **Schedule group: default vs dedicated km-at group**
   - What we know: EventBridge Scheduler supports schedule groups (max 64-char name). TTL schedules use the default group.
   - What's unclear: Should `km at` schedules go in a dedicated group (e.g., `km-at`) for easier listing/deletion?
   - Recommendation: Use a dedicated `km-at` schedule group. This allows `ListSchedules(GroupName: "km-at")` instead of name prefix filtering. Requires creating the group at `km init` time (single `CreateScheduleGroup` call).

4. **Config key `SchedulesTableName`**
   - What we know: `config.go` has `SandboxTableName`, `BudgetTableName`, `IdentityTableName`
   - What's unclear: Should schedules go in their own table or use a prefix in `km-sandboxes`?
   - Recommendation: New table `km-schedules` with `schedule_name` as hash key. Add `SchedulesTableName` to `config.go` with default `"km-schedules"`. Table created at `km init` time.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (no external test framework) |
| Config file | none |
| Quick run command | `go test ./internal/app/cmd/ ./pkg/at/... -run TestAt -v` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map
| Behavior | Test Type | Automated Command |
|----------|-----------|-------------------|
| NL time parsing → at() expression | unit | `go test ./pkg/at/... -run TestParseOneTime -v` |
| Recurring phrase → cron() expression | unit | `go test ./pkg/at/... -run TestParseRecurring -v` |
| km at command creates EventBridge schedule | unit (mock) | `go test ./internal/app/cmd/ -run TestAtCmd -v` |
| km at list shows DynamoDB records | unit (mock) | `go test ./internal/app/cmd/ -run TestAtList -v` |
| km at cancel deletes schedule + DynamoDB | unit (mock) | `go test ./internal/app/cmd/ -run TestAtCancel -v` |
| Schedule name sanitization (64-char, valid chars) | unit | `go test ./pkg/at/... -run TestScheduleName -v` |
| cron day-of-week mapping (THU=5 not 4) | unit | `go test ./pkg/at/... -run TestCronDayOfWeek -v` |
| SchedulerAPI mock compiles after interface extension | compile | `go build ./...` |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/cmd/ ./pkg/at/... -count=1`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/at/parser.go` — NL time parser (new package)
- [ ] `pkg/at/parser_test.go` — covers one-time and recurring cases
- [ ] `pkg/aws/schedules_dynamo.go` — km-schedules table CRUD
- [ ] `pkg/aws/schedules_dynamo_test.go`
- [ ] `internal/app/cmd/at.go` — km at command
- [ ] `internal/app/cmd/at_test.go`
- [ ] Update `mockSchedulerAPI` in `pkg/aws/scheduler_test.go` with stubs for new interface methods

*(No existing test infrastructure covers these — all new files)*

---

## Sources

### Primary (HIGH confidence)
- `pkg/aws/scheduler.go` — existing SchedulerAPI interface and TTL schedule patterns (read directly)
- `pkg/compiler/lifecycle.go` — BuildTTLScheduleInput / at() expression format (read directly)
- `internal/app/cmd/remote_publisher.go` — RemoteCommandPublisher pattern (read directly)
- `internal/app/config/config.go` — Config struct, MaxSandboxes, SchedulerRoleARN fields (read directly)
- `cmd/ttl-handler/main.go` — TTLEvent event shape and supported event_type values (read directly)
- `cmd/create-handler/main.go` — CreateEvent event shape and Lambda dispatch path (read directly)
- `pkg/aws/sandbox_dynamo.go` — DynamoDB CRUD patterns for manual attribute marshalling (read directly)
- [pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/scheduler](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/scheduler) — ListSchedules, GetSchedule, CreateSchedule API signatures
- [AWS EventBridge Scheduler CreateSchedule API reference](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_CreateSchedule.html) — cron format, name constraints, ActionAfterCompletion behavior

### Secondary (MEDIUM confidence)
- [github.com/olebedev/when](https://github.com/olebedev/when) — v1.1.0, November 2024, natural language date/time parsing, confirmed no recurring support
- [AWS EventBridge Scheduler schedule types documentation](https://docs.aws.amazon.com/scheduler/latest/UserGuide/schedule-types.html) — cron(6 fields), day-of-week 1=SUN mapping

### Tertiary (LOW confidence)
- [github.com/tj/go-naturaldate](https://github.com/tj/go-naturaldate) — alternative parser, unversioned; excluded in favor of olebedev/when due to narrower expression coverage

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries already in go.mod except olebedev/when; EventBridge Scheduler API verified from official docs
- Architecture: HIGH — all integration points read directly from source (scheduler.go, lifecycle.go, ttl-handler/main.go, create-handler/main.go)
- Pitfalls: HIGH for items 1-6 (verified from AWS docs + code); MEDIUM for item 8 (Lambda behavior inferred from existing code)

**Research date:** 2026-04-02
**Valid until:** 2026-07-01 (EventBridge Scheduler API stable; olebedev/when library active)
