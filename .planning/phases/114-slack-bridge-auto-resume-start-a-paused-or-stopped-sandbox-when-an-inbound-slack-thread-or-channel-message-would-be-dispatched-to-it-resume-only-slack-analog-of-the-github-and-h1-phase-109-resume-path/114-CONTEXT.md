# Phase 114: Slack bridge auto-resume — Context

**Gathered:** 2026-06-15
**Status:** Ready for planning
**Source:** Approved brainstorming design spec (`docs/superpowers/specs/2026-06-15-slack-resume-on-thread-message-design.md`)

<domain>
## Phase Boundary

**Delivers:** When an inbound Slack message targets a `paused`/`stopped` sandbox AND that
message would otherwise be dispatched (it already passed the mention-only / thread-bypass
filter and was enqueued to the per-sandbox FIFO), the `km-slack-bridge` Lambda starts the
EC2 instance so the on-box poller boots and drains the already-queued message. This is the
Slack analog of the GitHub/H1 Phase-109 resume path, **resume-only**.

**Does NOT deliver (out of scope):**
- Cold-create of a fresh sandbox (Slack has no `SandboxCreate` EventBridge publisher).
- Budget/spend awareness (explicitly deconfirmed by operator).
- Any SandboxProfile or DynamoDB schema change.
- Any change to the GitHub/H1/email bridges.
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Scope
- Resume-only. Both `paused` (hibernated) and `stopped` instances are resumable
  (`FetchByChannel` already collapses both into `info.Paused == true`).
- No cold-create. An orphaned row (instance terminated, row still `paused`/`stopped`)
  degrades to an informative hint and leaves the row in place.
- No budget logic.

### Trigger gate
- Resume fires ONLY at the existing step-9 paused branch — i.e. only after the message
  passed the mention-only / thread-bypass filter (step 5b) and was enqueued (step 8). Idle
  channel chatter that wouldn't dispatch never wakes the box.

### Resume mechanism (copy GitHub Phase-109 pattern into `pkg/slack/bridge`)
- New `SandboxResumer` interface + `EC2Resumer.StartSandbox(ctx, sandboxID)`. Near-verbatim
  port of `pkg/github/bridge/aws_adapters.go`. MUST filter on `tag:km:sandbox-id` (carries
  the Phase-109 / commit `e6b9ca75` / `d8007920` fix — NOT `{prefix}:sandbox-id`).
- New `var ErrNoResumableInstance` sentinel (slack-bridge namespaced). Wrapped only on the
  terminal `len(found)==0` path; transient `DescribeInstances`/`StartInstances` errors are
  returned plain.
- New `SandboxStatusWriter` interface + `SetStatusRunning(ctx, sandboxID)` via `UpdateItem`
  on `km-sandboxes` (NEVER `PutItem` — SandboxMetadata lossy round-trip footgun). The
  status flip is the real idempotency guard: next message sees `info.Paused == false` →
  warm path, no re-resume.

### Handler change (`events_handler.go` step 9)
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
- Message already enqueued at step 8 in all cases — resume failure NEVER strands the prompt
  (same fail-soft contract as GitHub: transient error still enqueues + optimistically flips).
- `StartSandbox` + `SetStatusRunning` run **SYNCHRONOUSLY** in `Handle` (bounded context,
  ~15s), NOT in the fire-and-forget goroutine — the Phase 75.2 Lambda-freeze lesson (a
  goroutine still mid-flight when `Handle` returns has its context elapse during the freeze).
  The 3s ack window is protected by the step-6 `event_id` dedup: if `StartInstances` pushes
  past 3s, Slack's retry hits the dedup and returns 200 immediately (same pattern as the
  existing step-10 reactor).

### Back-compat invariant
- `h.Resumer == nil` → byte-identical to today (pause-hint only). Tests and pre-deploy
  Lambda images must keep working.

### Wake UX
- Repurpose `DDBPauseHinter`. Resume-triggered → "⏳ Sandbox is waking up — your message is
  queued and will be answered shortly." Orphan/degraded → distinct "couldn't auto-resume;
  ask an operator to recreate." Exact strings finalized in the plan.

### Deploy surface
- `make build-lambdas` + `km init --slack` (or `--dry-run=false`). NOT `--sidecars`.
- One additive IAM grant on `infra/modules/lambda-slack-bridge/v1.0.0` (edit in place):
  `ec2:DescribeInstances` on `*`, `ec2:StartInstances` on `instance/*` conditioned on
  `aws:ResourceTag/km:resource-prefix == var.resource_prefix`. Mirror the GitHub
  `ec2_resume` policy. Confirm `var.resource_prefix` exists in the slack module (add if not).
- DDB `UpdateItem` on `km-sandboxes` ALREADY granted (pause-hinter) → no new DDB grant.
- No sandbox recreate — existing paused sandboxes gain resume-on-message after bridge deploy.

### Claude's Discretion
- Exact hint message strings.
- Whether `SetStatusRunning` is a standalone adapter or a method on the existing
  pause-hinter `DDBUpdateItemAPI` adapter.
- Test file layout (mirror `pkg/github/bridge/webhook_handler_phase109_test.go`).
</decisions>

<specifics>
## Specific References

- Slack handler: `pkg/slack/bridge/events_handler.go` (step 8 enqueue ~L445; step 9 paused
  branch ~L459).
- `FetchByChannel` paused detection: `pkg/slack/bridge/aws_adapters.go:1058`.
- `DDBPauseHinter` + `DDBUpdateItemAPI` (already does GetItem/UpdateItem on km-sandboxes):
  `pkg/slack/bridge/aws_adapters.go:1225+`.
- GitHub resume pattern to port: `pkg/github/bridge/aws_adapters.go` (`EC2Resumer` ~L490,
  `ErrNoResumableInstance` L92, `DynamoSandboxStatusWriter` ~L632) + interfaces
  `pkg/github/bridge/interfaces.go:34-77`.
- GitHub IAM to mirror: `infra/modules/lambda-github-bridge/v1.1.0/main.tf:205-239`
  (`ec2_resume` policy).
- Slack bridge IAM to extend: `infra/modules/lambda-slack-bridge/v1.0.0/main.tf`.
- Wiring: `cmd/km-slack-bridge/main.go` (pause-hinter wired ~L311-324).
- Reference tests: `pkg/github/bridge/webhook_handler_phase109_test.go`.
- Docs to update: `docs/slack-notifications.md` (§ Phase 114).
</specifics>

<deferred>
## Deferred Ideas

- Cold-create on orphaned/terminated instance (needs a Slack-side SandboxCreate publisher).
- Budget-suspend awareness (don't resume a budget-exhausted box).
- SQS retention tuning for very stale threads.
</deferred>

---

*Phase: 114-slack-bridge-auto-resume*
*Context gathered: 2026-06-15 via approved design spec*
