# Phase 119: Slack inbound per-thread parallelism - Context

**Gathered:** 2026-06-24
**Status:** Ready for planning
**Source:** Approved design spec (`docs/superpowers/specs/2026-06-24-slack-inbound-per-thread-parallelism-design.md`) via brainstorming session

<domain>
## Phase Boundary

**Delivers:** Different Slack threads to the same sandbox run in PARALLEL while
messages within a thread stay serial+ordered, bounded by an operator-set
concurrency cap. Dormant by default (cap=1 == today's serial behaviour, Phase 118).

Two layers + a knob:
1. **Bridge** (`pkg/slack/bridge/events_handler.go`): change the FIFO
   `MessageGroupId` from `info.SandboxID` to the Slack thread id (`threadTS`).
2. **Poller** (`pkg/compiler/userdata.go`): new profile field
   `notification.slack.inbound.maxConcurrentThreads` drives bounded concurrent
   agent dispatch with ack-after-completion + a visibility heartbeat.

**Does NOT deliver (out of scope):**
- Per-thread git-worktree isolation (shared `/workspace` mutation hazard is
  documented as operator responsibility; the cap is for conversational /
  read-mostly fan-out). A worktree-per-thread phase is a possible follow-up.
- GitHub / HackerOne bridge parity (Slack only this phase).
- Separate physical SQS queues per thread (MessageGroupId already isolates).
- No `apiVersion` bump (additive optional profile field).
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Layer 1 — Bridge: group by thread
- Change `MessageGroupId` from `info.SandboxID` → `threadTS` at BOTH `h.SQS.Send(...)`
  sites in `events_handler.go` (~L470 files path, ~L490 no-files path).
- `threadTS` is already in scope (computed ~L403 as `msg.ThreadTS`, falling back to
  `msg.TS` for a new top-level message). No new data plumbing — swap the `groupID` arg.
- `MessageDeduplicationId` (the Slack `event_id`) is UNCHANGED.
- **Unconditional rollout** — no new bridge env flag. Safe for cap=1 sandboxes:
  per-thread ordering preserved; global FIFO ordering was never meaningful for a
  serial bot.

### Layer 2 — Poller: bounded concurrent dispatch
- New profile field **`spec.notification.slack.inbound.maxConcurrentThreads`**
  (`*int`, default **1**) → env var **`KM_SLACK_MAX_CONCURRENCY`** via the existing
  `.NotifyEnv` map → `/etc/km/notify.env` + `/etc/profile.d/km-notify-env.sh`.
- Poller loop: `receive-message --max-number-of-messages N` (N from cap, SQS max 10);
  dispatch each agent turn in a **backgrounded subshell** behind a **counting
  semaphore** (bash `wait -n` + in-flight counter) so ≤ cap turns run at once.
- **ACK (delete) the message AFTER the turn completes**, inside the subshell. This
  REVERSES today's ack-first delete (`userdata.go` ~L2069) and is REQUIRED for
  per-thread ordering (FIFO won't deliver a thread's next message until the current
  is deleted).
- **Visibility heartbeat:** per in-flight message, a background ticker calls
  `ChangeMessageVisibility` every ~T/3 until the turn finishes, because the queue
  `VisibilityTimeout` is hardcoded to 30s (`pkg/aws/sqs.go:127`) and turns run minutes.
- cap=1 ⇒ degenerates to today's serial loop.

### Ack-ordering dup-window — DECISION REQUIRED IN PLANNING
- Ack-after-completion reintroduces the crash-redelivery duplicate window the
  ack-first code avoided.
- **Recommendation:** add a Slack per-turn idempotency guard mirroring the Phase 108
  GitHub `<!-- km-turn:$ID -->` marker (scan the thread for a prior reply with the
  same run id before posting). Cheap, consistent with the GitHub bridge.
- Alternative: accept the rare crash-only dup. Planner should pick one and justify.

### Schema
- Add `MaxConcurrentThreads *int` to `NotificationSlackInboundSpec`
  (`pkg/profile/types.go:238`), mirror the `Allow []string` / `ReactAlways *bool`
  pattern: `json:"maxConcurrentThreads,omitempty"` / `yaml:"...,omitempty"` + doc comment.
- JSON schema: integer, `minimum: 1`, `additionalProperties:false` preserved.
- `km validate` **WARNS** when `maxConcurrentThreads > 1` but `perSandbox` /
  `inbound.enabled` are not set.

### Claude's Discretion (planner decides)
- Exact bash counting-semaphore idiom and the heartbeat-ticker implementation.
- New base `VisibilityTimeout` value for the Slack inbound queue (currently 30s;
  H1 uses 1800s) and whether to raise the static base in addition to heartbeating.
- Whether `KM_SLACK_MAX_CONCURRENCY` also needs to bound `--max-number-of-messages`
  (e.g. min(cap,10)) vs always fetch a small batch.
- Run-output dir collision mitigation (pid/random suffix) if needed under concurrency.
- Wave/plan breakdown.
</decisions>

<specifics>
## Specific References

- Bridge Send sites + `threadTS`: `pkg/slack/bridge/events_handler.go` ~L403, L460–470, L482–490.
- `InboundQueueBody` struct carries `ThreadTS`; `SQSSender.Send(ctx, url, body, groupID, dedupID)`.
- Queue attrs: `pkg/aws/sqs.go:123–138` (`VisibilityTimeout: "30"`, FIFO, ContentBasedDeduplication:false);
  queue name `SlackInboundQueueName` ~L179; H1 uses `h1InboundVisibilityTimeout="1800"`.
- Env plumbing: `pkg/compiler/userdata.go` ~L1096–1121 (`.NotifyEnv` → notify.env + profile.d).
- Poller loop start ~L1670–1699 (`--max-number-of-messages 1`, parses `.thread_ts`); ack/delete ~L2069–2076.
- Schema struct: `pkg/profile/types.go:238–259` (`NotificationSlackInboundSpec`, `Allow`/`ReactAlways` pattern).
- Idempotency-guard precedent: Phase 108 GitHub `<!-- km-turn:$KM_GITHUB_TURN_ID -->`
  (`pkg/github/marker.go`, exported into the github poller dispatch blocks).
- Self-drive E2E helper: Phase-114 synthetic HMAC-signed `/events` POST
  (memory: `project_slack_bridge_inbound_e2e_and_status_attr`, `/tmp/km114_send_event.sh`).
- Golden trap: `userdata_learn_v2_pre92_baseline.golden.sh` must be HAND-PATCHED, not
  re-captured (memory: `project_frozen_byte_identity_golden_capture_trap`).
- Config merge-list: new km-config keys need the v2→v merge entry — N/A here (this is a
  profile field, not a km-config key; profile fields ride Phase-117 deepMerge).

## E2E test plan (from spec)
**Go/unit:** (1) `events_handler` asserts `MessageGroupId==threadTS` for both Send paths
and `==msg.TS` when no thread; (2) schema accepts `maxConcurrentThreads`, default 1,
`minimum:1` guard, `additionalProperties:false`, validate WARN >1 w/o perSandbox+inbound;
(3) userdata golden writes `KM_SLACK_MAX_CONCURRENCY` (regen via capture flags + hand-patch
frozen baseline); (4) `sqs.go` visibility attr present.
**Live E2E** (synthetic HMAC `/events` POST, no humans): parallelism (3 threads simultaneous
slow prompts → 3 overlapping runs, wall-clock ~max not sum); per-thread ordering (2 back-to-back
in one thread → 2nd after 1st, ordered replies); cap enforcement (5 threads cap=3 → never >3);
heartbeat (turn > base visibility → no dup reply); dormant regression (cap=1 → serial); dedup
(replayed event_id → single-processed).
</specifics>

<deferred>
## Deferred Ideas

- Per-thread git-worktree isolation for repo-mutating parallel turns (follow-up phase).
- GitHub / HackerOne bridge parity for thread/per-target parallelism.
</deferred>

---

*Phase: 119-slack-inbound-per-thread-parallelism*
*Context gathered: 2026-06-24 from approved design spec*
