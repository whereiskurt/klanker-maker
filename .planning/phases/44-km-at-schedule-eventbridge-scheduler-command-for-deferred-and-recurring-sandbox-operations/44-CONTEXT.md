# Phase 44: km at/schedule — Context

**Gathered:** 2026-04-02
**Status:** Ready for planning
**Source:** Brainstorming session

<domain>
## Phase Boundary

Add a `km at` command (alias: `km schedule`) that lets operators schedule any remote-capable sandbox command for future or recurring execution via EventBridge Scheduler. Supports natural language time expressions and raw cron. Includes schedule management (list, cancel) and a max-sandbox guardrail.

</domain>

<decisions>
## Implementation Decisions

### Command Syntax
- `km at '<time-expr>' <command> [args...]` — primary interface
- `km schedule` as alias
- Examples: `km at '10pm tomorrow' create dev.yaml`, `km at 'every thursday at 3PM' kill gg1 --remote`

### Time Expression Parsing
- Natural language parsing via Go library (e.g. `olebedev/when` or similar) → converted to EventBridge `at()` and `cron()` expressions
- `--cron '<expr>'` flag for raw EventBridge cron expressions as alternative to natural language
- One-time schedules use `at(YYYY-MM-DDThh:mm:ss)` format (already used by TTL handler)
- Recurring schedules use EventBridge `cron()` format

### Supported Commands
- ALL remote-capable commands: create, destroy/kill, stop, pause, resume, extend
- Commands dispatch via existing EventBridge event patterns (SandboxCreate, SandboxIdle)

### Schedule Naming
- Auto-generated names from command + target + time (e.g., `kill-gg1-thu-3pm`)
- Optional `--name <name>` override for user-friendly names
- Names must be unique within the EventBridge Scheduler namespace

### Schedule Management
- `km at list` — show all scheduled operations (from DynamoDB)
- `km at cancel <name>` — cancel a scheduled operation (delete from EventBridge Scheduler + DynamoDB)
- Anyone can list/cancel any schedule (no per-operator scoping)

### Recurring + Sandbox Resolution
- Recurring commands that reference a sandbox ID resolve gracefully at fire-time
- If sandbox is gone, the scheduled execution fails gracefully (log warning, don't error-loop)
- Recurring create commands spin up new sandbox each trigger

### State Tracking
- Store schedule metadata in DynamoDB (schedule name, command, time expression, creator, created-at, status)
- Enables `km at list` without calling EventBridge ListSchedules API
- Sync state: DynamoDB is source of truth for listing; EventBridge Scheduler is source of truth for execution

### Max Sandbox Guardrail
- Max active sandbox count configured as `max_sandboxes` in `km-config.yaml`
- Read and validated during `km init` — must be present in the config file
- Adjustable by re-running `km init` or editing `km-config.yaml` directly
- Recurring create schedules check max before provisioning; skip with warning if at limit
- This protects against runaway recurring creates

### E2E Validation
- 10-minute E2E test that exercises `km at` with real remote commands (create, kill, pause, resume, extend, stop)
- Covers the full lifecycle: schedule one-time creates, schedule destroys, schedule lifecycle commands, verify schedules fire via EventBridge
- Uses real AWS infrastructure (not mocked) — validates the full dispatch path through Lambda handlers
- Test should demonstrate meaningful coverage of all remote-capable command types within the 10-minute window

### Infrastructure
- Reuse existing EventBridge Scheduler patterns from TTL handler (`pkg/aws/scheduler.go`)
- Reuse existing remote publisher pattern for command dispatch
- Schedule target: existing create-handler Lambda (for creates) and TTL handler Lambda (for lifecycle commands)
- Schedule IAM role: reuse existing scheduler role (KM_SCHEDULER_ROLE_ARN)

</decisions>

<specifics>
## Specific Ideas

- The `at()` expression format is already proven in `pkg/compiler/lifecycle.go` for TTL schedules
- Remote publisher in `internal/app/cmd/remote_publisher.go` already handles SandboxIdle events for destroy/stop/extend
- DynamoDB sandbox table pattern in `pkg/aws/` can be extended or a new `km-schedules` table created
- Config system in `internal/app/config/config.go` already supports `km-config.yaml` for platform-level settings like max sandboxes

</specifics>

<deferred>
## Deferred Ideas

- Schedule notifications (email/Slack when a scheduled command fires)
- Schedule dry-run mode
- Schedule approval workflows (require confirmation before firing)
- Calendar view of upcoming schedules in ConfigUI

</deferred>

---

*Phase: 44-km-at-schedule*
*Context gathered: 2026-04-02 via brainstorming session*
