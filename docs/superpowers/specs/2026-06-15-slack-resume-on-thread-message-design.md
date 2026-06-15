# Design: Auto-resume a paused/stopped sandbox on inbound Slack thread message

**Date:** 2026-06-15
**Status:** Approved (brainstorming) — pending GSD phase plan
**Target phase:** 114
**Scope:** `pkg/slack/bridge` + `cmd/km-slack-bridge` + `infra/modules/lambda-slack-bridge` IAM. No SandboxProfile schema change, no DDB schema change.

## Problem

Today, when a Slack message arrives in a channel/thread bound to a sandbox that is
`paused` (hibernated) or `stopped`, the bridge:

1. Resolves the channel → sandbox via `FetchByChannel` (already returns `info.Paused`
   and `info.SandboxID`).
2. **Unconditionally enqueues** the message to the per-sandbox FIFO queue (step 8 of
   `EventsHandler.Handle`).
3. If `info.Paused`, fires a fire-and-forget `PauseHinter` goroutine that posts a
   one-time "sandbox is paused; message queued" hint (1h cooldown).

The message is **not lost** — it sits in the FIFO. But nothing starts the instance, so
the on-box poller never boots to drain it. The user gets a "paused" hint and silence
until an operator manually `km resume`s the box.

## Goal

When an inbound Slack message targets a `paused`/`stopped` sandbox **and the message
would otherwise be dispatched** (passes the existing mention-only / thread-bypass
filter), the bridge **starts the instance**. The already-enqueued message is drained by
the poller on boot, and the agent's reply lands naturally.

This is the Slack analog of the GitHub/H1 Phase-109 resume-or-cold-create path, but
**resume-only** (Slack has no cold-create publisher).

## Non-goals (v1)

- **Cold-create.** Slack has no `SandboxCreate` EventBridge publisher. An orphaned row
  (instance terminated, row still says `paused`/`stopped`) degrades to an informative
  hint; it does not provision a fresh sandbox. Deferred.
- **Budget/spend awareness.** Out of scope per operator direction. A budget-suspended
  box may resume and re-suspend; v1 does not distinguish.
- **Any SandboxProfile / DDB schema change.** None required.

## Why this is a small change

The Slack events handler already does the hard parts. Unlike GitHub/H1 (which resolve by
**alias** and need a status-aware resolver + cold-create publisher), the Slack bridge
resolves by **channel** and already has `info.Paused` + `info.SandboxID` in hand at the
existing step-9 paused branch. The message is already enqueued at step 8. **The only
missing action is `StartInstances`.**

Key existing facts (verified):
- `FetchByChannel` sets `info.Paused = (state == "paused" || state == "stopped")`
  (`pkg/slack/bridge/aws_adapters.go:1058`). Both states flow into the same branch.
- The message is enqueued at step 8 **before** the paused branch
  (`events_handler.go:445`), so resume failure never strands the prompt.
- `DDBPauseHinter` already performs `GetItem` + `UpdateItem` on `km-sandboxes`
  (`aws_adapters.go:1273`), so the Slack bridge IAM role **already grants
  `dynamodb:UpdateItem` on `km-sandboxes`** — `SetStatusRunning` needs no new DDB grant.
- The GitHub `EC2Resumer.StartSandbox` filters on `tag:km:sandbox-id` (the
  Phase-109/`e6b9ca75`/`d8007920` fix). Copying it carries that fix.

## Components

### 1. `SandboxResumer` interface + `EC2Resumer` (new, `pkg/slack/bridge`)
Near-verbatim copy of `pkg/github/bridge/aws_adapters.go`:
- `var ErrNoResumableInstance = errors.New("slack-bridge: no resumable EC2 instance")`
- `EC2Resumer.StartSandbox(ctx, sandboxID)` — `DescribeInstances` filtered on
  `tag:km:sandbox-id == sandboxID` and state in `{stopped, stopping}`; `StartInstances`
  on the matches. Returns `ErrNoResumableInstance` (wrapped) when zero instances found;
  transient AWS errors are returned plain (not wrapped).

### 2. `SandboxStatusWriter` interface + adapter (new, mirrors GitHub)
- `SetStatusRunning(ctx, sandboxID)` — `UpdateItem` on `km-sandboxes` setting
  `state = "running"`. **Never `PutItem`** (SandboxMetadata lossy round-trip footgun).
- This status flip — not the hint cooldown — is the real idempotency guard: once the row
  is `running`, the next message's `FetchByChannel` returns `info.Paused == false` → warm
  path, no re-resume.

### 3. `EventsHandler.Handle` step-9 change
Replace the bare pause-hint branch with status-aware resume:

```
if info.Paused && h.Resumer != nil {
    err := h.Resumer.StartSandbox(ctx, info.SandboxID)
    if err != nil && errors.Is(err, ErrNoResumableInstance) {
        // Orphan: instance gone. v1 has no cold-create.
        // Post the degraded hint ("couldn't auto-resume; ask an operator"). Leave row.
    } else {
        // Success OR transient error: enqueue already happened at step 8.
        if h.StatusWriter != nil { h.StatusWriter.SetStatusRunning(ctx, info.SandboxID) }
        // Post the "waking up; message queued" hint.
    }
}
```

Notes:
- Runs **only after** the message passed the mention-only / thread-bypass filter (step
  5b) and was enqueued (step 8) — i.e. only messages we'd actually dispatch trigger a
  resume. No spurious wakeups on idle channel chatter.
- Fail-soft: a transient `StartInstances` error still leaves the message queued and flips
  status optimistically (mirrors GitHub: enqueue proceeds regardless). The next message
  retries the resume if the box truly didn't start.
- Keep the existing fire-and-forget goroutine + bounded context so `Handle` still returns
  200 within Slack's 3s ack window.

### 4. Wake UX
Repurpose the existing `DDBPauseHinter`:
- Resume-triggered path → "⏳ Sandbox is waking up — your message is queued and will be
  answered shortly." (1h cooldown; harmless because the status flip means follow-ups take
  the warm path anyway).
- Orphan/degraded path → a distinct "couldn't auto-resume; ask an operator to recreate"
  line.

Exact copy to be finalized in the plan. Two hint texts; decide whether to use one hinter
with a passed text or a second hinter instance.

### 5. Wiring (`cmd/km-slack-bridge/main.go`)
Construct `EC2Resumer` (EC2 client) + `SandboxStatusWriter` (reuse the DDB client already
wired for the pause-hinter) and assign to `eventsHandler.Resumer` / `.StatusWriter`. Both
optional/nil-safe for tests and back-compat (nil `Resumer` → byte-identical to today:
pause-hint only).

## Deploy surface

- **Bridge Lambda code** → `make build-lambdas`.
- **One new IAM grant** on `infra/modules/lambda-slack-bridge/v1.0.0` (edit in place,
  additive — mirror the GitHub `ec2_resume` policy):
  - `ec2:DescribeInstances` on `*` (Describe has no resource-level conditions).
  - `ec2:StartInstances` on `instance/*` conditioned on
    `aws:ResourceTag/km:resource-prefix == var.resource_prefix`.
  - Confirm the slack module exposes `var.resource_prefix` (add if absent).
- **DDB `UpdateItem` on `km-sandboxes` already granted** (pause-hinter) → no new DDB grant.
- Deploy = `km init --slack` (tier-1 env+IAM fast-path) **or** `km init --dry-run=false`.
  **NOT `--sidecars`** (env/IAM only update on a full terragrunt apply).
- **No SandboxProfile schema change, no DDB schema change → no sandbox recreate.**
  Existing paused sandboxes gain resume-on-message immediately after the bridge deploy.

## Testing

### Unit
Mirror `pkg/github/bridge/webhook_handler_phase109_test.go` with mock `Resumer` /
`StatusWriter`:
- `info.Paused == true` → `StartSandbox` called, `SetStatusRunning` called, message still
  enqueued.
- `StartSandbox` returns `ErrNoResumableInstance` → degraded hint, no `SetStatusRunning`,
  message still enqueued.
- `StartSandbox` returns transient error → message enqueued, status flip attempted (fail-
  soft), no crash.
- `info.Paused == false` (running) → no `StartSandbox`, warm path unchanged.
- `Resumer == nil` → byte-identical to today (pause-hint only). Back-compat invariant.

### E2E (operator-driven, requires AWS login)
1. Identify/create a Slack-enabled sandbox; confirm it's bound to a channel.
2. `km pause <id>` (and a separate `km stop <id>` run to cover both states).
3. Post a message in the channel/thread (top-level @-mention and an in-thread reply to
   exercise the thread-bypass path).
4. Observe: `StartInstances` in CloudWatch bridge logs, the "waking up" Slack note, the
   `km-sandboxes` row flip to `running` (`km list`), and the agent reply landing once the
   poller drains the FIFO.
5. Verify no resume on a running sandbox (warm path unchanged) and no resume on a
   non-dispatched message (mention-only channel, non-mention).

## Files touched

- `pkg/slack/bridge/events_interfaces.go` — add `SandboxResumer`, `SandboxStatusWriter`.
- `pkg/slack/bridge/aws_adapters.go` — `EC2Resumer`, `ErrNoResumableInstance`,
  `DynamoSandboxStatusWriter` (or extend the pause-hinter adapter).
- `pkg/slack/bridge/events_handler.go` — step-9 status-aware resume branch.
- `cmd/km-slack-bridge/main.go` — wire `Resumer` + `StatusWriter`.
- `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` — additive `ec2_resume` policy
  (+ `resource_prefix` var if absent).
- Tests: new `pkg/slack/bridge/events_handler_resume_test.go` (mirror Phase-109 tests).
- Docs: `docs/slack-notifications.md` § Phase 114.

## Open items for the plan

- Finalize the two hint message strings and whether to use one hinter or two.
- Confirm `var.resource_prefix` presence in the slack-bridge module.
- Decide whether `SetStatusRunning` is a standalone adapter or a method added to the
  existing pause-hinter's `DDBUpdateItemAPI` adapter.
