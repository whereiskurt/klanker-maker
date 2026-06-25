# Design: Slack inbound per-thread parallelism

**Date:** 2026-06-24
**Phase:** 119
**Status:** Approved design — pending `/gsd:plan-phase 119`

## Problem

A single sandbox serializes **every** inbound Slack turn for two independent reasons:

1. **Bridge groups by sandbox.** The `km-slack-bridge` stamps `MessageGroupId = info.SandboxID`
   at both `h.SQS.Send(...)` sites (`pkg/slack/bridge/events_handler.go` ~L470, L490). One FIFO
   group per box ⇒ SQS delivers one message at a time for the whole sandbox.
2. **Poller is a serial executor.** The sandbox-side poller does
   `receive-message --max-number-of-messages 1` (`pkg/compiler/userdata.go` ~L1674) and **blocks**
   on the agent dispatch (`~L1955` codex / `~L1992` claude) — no backgrounding, no fan-out.

Result: 5–6 concurrent Slack threads to one sandbox answer one-after-another; wall-clock latency =
**sum** of all turn durations.

## Goal

Different Slack **threads** to the same sandbox run in **parallel**, while messages **within** a
thread stay **serial and ordered**, bounded by an operator-set concurrency cap.
**Dormant by default:** cap = 1 ⇒ byte-identical-behaviour to Phase 118 (serial).

The mental model the operator asked for: *"each thread needs its own queue — kinda, but not."*
SQS FIFO `MessageGroupId` is exactly that primitive: serial within a group, parallel across groups,
on one physical queue.

## Design — two layers + a knob

### Layer 1 — Bridge: group by thread (unconditional)

- Change `MessageGroupId` from `info.SandboxID` → **`threadTS`** at both `Send` sites in
  `events_handler.go`. `threadTS` is already in scope (computed ~L403 as `msg.ThreadTS`, falling
  back to `msg.TS` for a new top-level message), so **no new data plumbing** — just swap the
  `groupID` argument.
- `MessageDeduplicationId` (the Slack `event_id`) is **unchanged**.
- FIFO semantics then give us, for free: parallel **across** threads, strict serial + ordered
  **within** a thread. SQS will not release a thread's next message until the in-flight one is
  deleted (or its visibility times out).
- Unconditional rollout (no new bridge env flag). Safe even for cap=1 sandboxes: per-thread
  ordering is preserved; global FIFO ordering was never meaningful for a serial bot.

### Layer 2 — Poller: bounded concurrent dispatch

- New profile field **`spec.notification.slack.inbound.maxConcurrentThreads`** (`*int`, default
  **1**) → bridged to the sandbox as **`KM_SLACK_MAX_CONCURRENCY`** via the existing `.NotifyEnv`
  map → `/etc/km/notify.env` + `/etc/profile.d/km-notify-env.sh` (`userdata.go` ~L1096–1121).
- Poller loop changes:
  - `receive-message --max-number-of-messages N` (N derived from the cap; SQS max 10).
  - Dispatch each agent turn in a **backgrounded subshell** guarded by a **counting semaphore**
    (bash `wait -n` + an in-flight job counter; AL2023 / Ubuntu ship bash ≥ 4.3) so never more
    than `cap` turns run at once.
  - **ACK (delete) the message AFTER the turn completes**, inside the subshell — this is what
    preserves per-thread ordering (FIFO won't deliver the thread's next message until the current
    one is deleted). See the ack-ordering reversal below.
  - **Visibility heartbeat:** per in-flight message, a background ticker calls
    `ChangeMessageVisibility` every ~T/3 until the turn finishes, because the queue's
    `VisibilityTimeout` is hardcoded to **30s** (`pkg/aws/sqs.go:127`) and agent turns run minutes.
- cap = 1 ⇒ degenerates to today's serial loop.

### Ack-ordering reversal (important refinement)

Today the poller **deletes the message *first*, then posts** to Slack (`userdata.go` ~L2069), an
"ack-first" trick that trades a lost reply for never duplicating on a host crash. Using FIFO groups
as our per-thread serializer **requires the opposite** — the message must stay in-flight until the
turn finishes, or SQS releases the thread's *next* message mid-turn and breaks in-thread ordering.

So we flip to **ack-after-completion + visibility heartbeat**. This reintroduces the
crash-redelivery duplicate window the old code dodged.

**Open decision for planning:** add a **Slack per-turn idempotency guard** (mirror the Phase 108
GitHub `<!-- km-turn:$ID -->` marker, scanning the thread for a prior reply with the same run id
before posting) **or** accept the rare crash-only dup. Recommendation: add the guard — it is cheap
and consistent with the GitHub bridge.

### Schema

- Add `MaxConcurrentThreads *int` to `NotificationSlackInboundSpec` (`pkg/profile/types.go:238`),
  mirroring the `Allow []string` / `ReactAlways *bool` pattern: `json:"maxConcurrentThreads,omitempty"`
  / `yaml:"maxConcurrentThreads,omitempty"`, doc comment for the nil-default.
- JSON schema: integer, `minimum: 1`, `additionalProperties:false` preserved.
- `km validate` **WARNS** when `maxConcurrentThreads > 1` but `perSandbox` / `inbound.enabled` are
  not set (the parallelism only applies to a per-sandbox inbound channel).

## Safety / scope boundary

- **Per-thread session state is safe.** `km-slack-threads` session IDs are keyed per thread ⇒
  concurrent different-thread turns resume different sessions; DDB writes hit distinct keys.
- **Shared `/workspace` is NOT isolated.** Concurrent turns that mutate the same repo/filesystem can
  corrupt each other (two agents, one git working tree). **Out of scope:** per-thread git-worktree
  isolation. The cap is documented as an operator responsibility — it is intended for
  **conversational / read-mostly** fan-out (Q&A, status, advice). Repo-mutating parallelism is a
  potential follow-up phase (worktree-per-thread).
- **Run-output dir collision.** Runs write `/workspace/.km-agent/runs/<ts>/output.json`; confirm
  uniqueness under concurrency (add pid/random suffix if timestamps can collide).

## Out of scope

- Per-thread git-worktree isolation (follow-up).
- GitHub / HackerOne bridge parity (Slack only).
- Separate physical queues per thread (MessageGroupId already provides the isolation).

## Deploy surface

- **Bridge `MessageGroupId` change** → `make build-lambdas` + `km init --slack`
  (or full `km init --dry-run=false`); effective on the next webhook delivery.
- **Profile field + poller userdata** → `make build-lambdas` + `km init --dry-run=false`
  (the create-handler zip renders userdata). Existing sandboxes need `km destroy && km create`.
- **Queue `VisibilityTimeout`** (if adjusted in `sqs.go`) applies to **newly created** per-sandbox
  queues only; existing sandboxes' queues are unchanged until recreate.
- **Userdata goldens move.** Regenerate full-output goldens via the sanctioned capture flags, and
  **HAND-PATCH the frozen pre-92 baseline** (`userdata_learn_v2_pre92_baseline.golden.sh`) — do NOT
  re-capture it (re-capture folds the post-baseline SubagentStop script into the frozen baseline).
- **No apiVersion bump** (additive optional field).

## E2E test plan

### Go / unit

1. `events_handler`: assert `MessageGroupId == threadTS` for both `Send` paths (files + no-files),
   and `== msg.TS` when there is no `thread_ts` (new top-level message). Regression guard.
2. Schema: `maxConcurrentThreads` accepted; default 1; `minimum:1` range guard rejects 0 / negative;
   `additionalProperties:false` still holds; `km validate` WARN when `>1` without perSandbox+inbound.
3. `compiler/userdata` golden: `KM_SLACK_MAX_CONCURRENCY` written to `notify.env`; regenerate via
   capture flags + hand-patch the frozen baseline.
4. `pkg/aws/sqs.go`: VisibilityTimeout attribute present (and whatever new base value we pick).

### Live E2E (driven by the synthetic HMAC-signed `/events` POST helper — no humans)

Reuse the Phase-114 self-drive technique (`/tmp/km*_send_event.sh`) to POST `event_callback` bodies
across distinct `thread_ts` values, with prompts that reliably take ~30–60s.

- **Parallelism:** fire threads A/B/C simultaneously → `km agent list` / `km otel --timeline` show 3
  **overlapping** runs; wall-clock ≈ max(turn), not sum.
- **Per-thread ordering:** two back-to-back messages in thread A → 2nd turn starts only after the
  1st completes; replies land in order in the thread.
- **Cap enforcement:** 5 threads with cap=3 → never more than 3 concurrent runs; 4th/5th queue.
- **Heartbeat:** one turn longer than the base visibility timeout → **no duplicate reply** (no
  redelivery).
- **Dormant regression:** cap=1 sandbox → serial, unchanged from Phase 118.
- **Dedup:** replayed `event_id` → still single-processed.

## Items the GSD research pass will confirm

- `thread_ts` exact field name + in-scope availability at both `Send` sites (recon: confirmed,
  `threadTS` local var, computed ~L403).
- Current queue `VisibilityTimeout` (recon: `"30"` at `sqs.go:127`; H1 uses `"1800"`) and the new
  base value to pick for Slack.
- Bash version on the AL2023 / Ubuntu AMIs (`wait -n` needs ≥ 4.3) + the cleanest counting-semaphore
  idiom for the poller.
- Run-output-dir collision risk under concurrency.
- Final decision on the Slack per-turn idempotency guard (Phase-108-style marker vs accept).
