# Phase 67: Slack inbound — per-sandbox channel as bidirectional chat with km agent run dispatch — Context

**Gathered:** 2026-05-02
**Status:** Ready for planning

<domain>
## Phase Boundary

Closes the loop deferred from Phase 63: Slack messages from the operator
into a sandbox's per-sandbox channel become Claude turns inside that
sandbox via SQS-driven dispatch. Outbound replies (Phase 63's `Stop` /
`Notification` hook → `km-slack post`) are reused unchanged, plus
threading-aware so Claude's reply lands inside the user's thread.

**v1 scope:**
- Slack Events API → bridge Lambda → per-sandbox SQS FIFO queue → sandbox-side
  systemd poller → `km agent run --resume <session-id>` per turn.
- Outbound replies thread under the inbound message (Phase 63's
  `thread_ts` field is already wired — finally consumed in v1 of this
  phase).
- "Sandbox ready" announcement posted by `km create` becomes the default
  thread anchor for first conversations in the channel.
- Per-sandbox channel mode only. Shared and override modes stay
  outbound-only.
- Stateless turns: each Slack message spawns a fresh `km agent run`
  process; Claude session continuity comes from `--resume` keyed by
  `(channel_id, thread_ts)`.
- Survives `km pause` cleanly: SQS retains for 14 days; poller drains
  on resume.

**Out of scope (deferred to later phases):**
- Mention-based sandbox spawning (`@km-bot create profile=foo prompt=...`)
  — its own phase.
- Slack interactive features (Block Kit buttons, slash commands, modals).
- Permission-prompt round-trip (Slack reply → Claude permission decision)
  — needs a Claude SDK runtime-decision API that doesn't exist yet.
- Auto-resume of paused sandboxes on inbound activity.
- Block Kit / rich formatting for Claude replies.
- Inbound on shared channel (`#km-notifications`) or override-mode channels.
- DM delivery, multi-recipient routing.
- CLI flag overrides for the new profile field.

**Dependencies (all complete):**
- Phase 14 — sandbox identity / Ed25519 signed payloads.
- Phase 23 — credential rotation (covers Slack bot token + signing key
  rotation).
- Phase 27 — OTEL integration (extends to inbound bridge metrics).
- Phase 39 — DynamoDB sandbox metadata (where `slack_channel_id` lives;
  new `slack_inbound_queue_url` joins it).
- Phase 45 — `km-send`/`km-recv` precedent for sandbox-side daemons.
- Phase 50/51 — `km agent run` (with `--dangerously-skip-permissions`)
  and tmux session support.
- Phase 62 — operator-notify hook (`km-notify-hook` script).
- Phase 63 — Slack-notify hook with bridge Lambda, envelope schema with
  `version`/`action`/`thread_ts`/`nonce`, per-sandbox channels, DDB
  nonce table.
- Phase 66 — multi-instance support (resource_prefix / email_subdomain;
  new SQS queue names and DDB table name must respect the prefix).

</domain>

<decisions>
## Implementation Decisions

### Queue Topology (LOCKED)

- **One SQS FIFO queue per sandbox.** Created at `km create` (joins the
  existing per-sandbox channel creation flow, before user-data
  finalizes), deleted at `km destroy`. URL stored in DDB sandbox
  metadata as `slack_inbound_queue_url`, injected into sandbox env as
  `KM_SLACK_INBOUND_QUEUE_URL` alongside the existing
  `KM_SLACK_CHANNEL_ID` / `KM_SLACK_BRIDGE_URL`.
- **Naming:** `{resource_prefix}-slack-inbound-{sandbox-id}.fifo` (Phase 66 prefix-aware).
- **Type:** FIFO — strict ordering of conversation turns; exactly-once
  delivery within the dedup window. Standard queues are wrong fit
  (out-of-order replies, manual dedup needed).
- **MessageGroupId:** sandbox-id (single conversation stream per
  sandbox; all messages in the same FIFO group). MessageDeduplicationId
  uses Slack `event_id` (already unique per Slack delivery; doubles as
  cross-Lambda-invocation idempotency).
- **Retention:** 14 days (SQS max). No DLQ — poller never drops
  messages: `DeleteMessage` is called only after `km agent run` accepts
  the prompt. Failed turns return to queue after VisibilityTimeout
  (30s) and retry naturally. Poison messages (malformed) are logged
  and acked to avoid infinite retry; metrics counter increments.
- **IAM:** sandbox instance role gets `sqs:ReceiveMessage`,
  `DeleteMessage`, `GetQueueAttributes` scoped to its own queue ARN
  (`arn:aws:sqs:*:*:{prefix}-slack-inbound-{sandbox-id}.fifo`).
  Bridge Lambda gets `sqs:SendMessage` to all
  `{prefix}-slack-inbound-*.fifo` queues. Cross-sandbox read/write
  prevented by IAM, not just convention.

### Delivery Semantics (LOCKED)

**Turn model:** Stateless `km agent run` per Slack message. Each inbound
SQS message:

1. Poller reads message body (JSON: `channel`, `thread_ts`, `text`,
   `user`, `event_ts`).
2. Poller looks up `(channel_id, thread_ts)` in DDB
   `km_slack_threads` table → gets `claude_session_id` (or empty if
   new thread).
3. Poller invokes
   `km agent run --prompt "<text>" --resume <session-id>` (omit
   `--resume` if no prior session — first turn in this thread).
4. On `km agent run` exit, poller parses output JSON, extracts the
   new/continuing `session_id`, writes `(channel_id, thread_ts,
   session_id, last_turn_ts)` back to DDB.
5. `DeleteMessage` from SQS only after step 4 succeeds.

**Why stateless wins:** survives `km pause`/`km resume` cleanly; reuses
all of Phase 50/51's agent-run pipeline; no tmux liveness tracking;
~3-5s startup per turn is acceptable for human-paced chat.

**Session map:** new DDB table `{prefix}-km_slack_threads`:
- Partition key: `channel_id` (S)
- Sort key: `thread_ts` (S)
- Attributes: `claude_session_id` (S), `sandbox_id` (S),
  `last_turn_ts` (S, ISO8601), `turn_count` (N), `created_at` (S).
- TTL: 30 days from `last_turn_ts` (auto-cleanup of stale threads).
- Bridge writes on inbound (creates row if absent); poller writes back
  with new `claude_session_id` after each turn.

**Top-level posts (no thread):** Treated as start of new thread. Poller
invokes `km agent run` without `--resume`. Reply posts as a thread
reply on the user's top-level message — that message's `ts` becomes
the new thread root and is recorded in `km_slack_threads`.

**Ready announcement as default thread:** `km create` posts
"Sandbox `sb-abc123` ready. Reply here or in any thread to give it a
task. Profile: ... Region: ... Idle: ... TTL: ..." via the existing
operator-signed `post` action through the bridge Lambda. Its `ts` is
recorded in `km_slack_threads` with empty `claude_session_id` so the
first reply directly under it cleanly starts a new conversation.

**Outbound threading:** Poller exports `KM_SLACK_THREAD_TS=<ts>` before
invoking `km agent run`. The Phase 63 `Stop` / `Notification` hook
(`/opt/km/bin/km-notify-hook`) reads this env var and passes
`--thread <ts>` to `km-slack post`. The envelope's `thread_ts` field
(already wired in Phase 63) is now consumed; bridge passes it to
`chat.postMessage` so replies land in-thread.

**Concurrency:** FIFO queue + single-threaded systemd poller naturally
serializes. A second Slack message arriving while the first turn is
running waits in SQS until the in-flight `km agent run` exits. No
interrupt semantics in v1.

### Inbound Enablement (LOCKED)

**New profile field (under `spec.cli`):**

| Field | Type | Default | Effect |
|---|---|---|---|
| `notifySlackInboundEnabled` | bool | `false` | Provision SQS queue at create, install poller systemd unit, subscribe to channel events |

**Validation rules:**
- `notifySlackInboundEnabled: true` requires `notifySlackEnabled: true`
  → validation error if not (no transport configured).
- `notifySlackInboundEnabled: true` requires `notifySlackPerSandbox: true`
  → validation error if not (channel→sandbox routing requires 1:1
  mapping in v1).
- `notifySlackInboundEnabled: true` AND `notifySlackChannelOverride: <set>`
  → validation error (override mode unsupported for inbound in v1).

**No CLI flag overrides.** Profile-only, matches Phase 63 precedent.
Inbound is a sandbox-lifecycle feature — the queue and poller are
provisioned at create, not per-shell.

**Backward compatibility:** Existing Phase 63 sandboxes do NOT gain
inbound retroactively. `km destroy && km create` is required, same as
Phase 62/63 hook installation.

**Operator visibility:**
- `km status sb-abc123` adds: queue URL, queue depth
  (`ApproximateNumberOfMessages`), last-receive timestamp, active
  thread count.
- `km list --wide` gets a 💬 column (number of active threads).
- `km doctor` adds three checks:
  - `slack_inbound_queue_exists` — for each DDB sandbox flagged
    `notifySlackInboundEnabled`, verify SQS queue present and
    accessible.
  - `slack_inbound_stale_queues` — find SQS queues matching
    `{prefix}-slack-inbound-*.fifo` whose sandbox-id has no DDB
    record (cleanup leak detection).
  - `slack_app_events_subscription` — verify Slack App has Events
    API URL configured and required scopes (`channels:history`,
    `groups:history`, `chat:write`, plus existing v1 scopes).

### Edge Cases (LOCKED)

**Paused/hibernated sandbox + inbound message:**
- **Queue-and-wait.** SQS retains 14d. When user runs `km resume`, the
  poller starts and drains the queue.
- Bridge optionally posts a one-time "Sandbox is paused; message
  queued. Run `km resume sb-abc123` to wake it up." reply on the
  thread when it detects pause via DDB metadata. Subsequent queued
  messages do NOT re-trigger this hint (cooldown via DDB last-pause-hint
  timestamp).
- No auto-resume in v1.

**Notification hook behavior in inbound mode:**
- Unchanged from Phase 63: outbound only. The hook posts permission /
  idle notifications to Slack via existing path.
- Permission round-trip (Slack reply → permission decision) is NOT
  implemented. `km agent run` already uses
  `--dangerously-skip-permissions`, so most permissions don't fire as
  blocking prompts. Profile-level `allowedTools` remains the auth
  surface.
- Inbound replies are always treated as new prompts, never as
  permission decisions.

**`km destroy` with in-flight messages:**
- **Drain best-effort, up to ~30s.** Sequence:
  1. Stop the systemd poller (`systemctl stop km-slack-inbound-poller`)
     to prevent new turns from starting.
  2. Wait up to 30s for any in-flight `km agent run` to finish (poll
     for the run's tmux session to exit).
  3. Post final "Sandbox `sb-abc123` destroyed at `<ts>`." (existing
     Phase 63 flow).
  4. Delete the SQS queue (drops any unprocessed messages — bounded
     by drain wait).
  5. Archive channel (existing Phase 63 flow, gated by
     `slackArchiveOnDestroy`).
  6. Delete DDB rows in `km_slack_threads` for this `channel_id`
     (cascade-on-destroy).

**Bot loop prevention:**
- Bridge Slack Events handler filters out:
  - `event.user == <workspace bot user_id>` (cached at Lambda warm
    time from `auth.test`).
  - `event.subtype == "bot_message"`.
  - `event.subtype == "message_changed"` and `"message_deleted"`.
  - `event.bot_id` present.
- Belt-and-suspenders: only forward inbound from users explicitly
  invited to the per-sandbox channel via Slack Connect (existing
  invite mechanism from Phase 63).

### Bridge Lambda Extensions

The existing `km-slack-bridge` Lambda gains a second URL path / route
for Slack Events API. Single Function URL, dispatches by path:

- `POST /` — existing Phase 63 envelope (Ed25519-signed outbound from
  sandboxes / operator). Unchanged.
- `POST /events` — Slack Events API webhook. New.

**Slack Events handler flow:**

1. Verify Slack signing secret (HMAC-SHA256 of timestamp + raw body
   against shared secret). Reject 401 on mismatch. Stale timestamp
   (>5min) → 401.
2. Parse event JSON. Handle `url_verification` challenge response
   (one-time during Slack App setup).
3. Dedup `event_id` via existing `km_slack_bridge_nonces` DDB table
   (TTL 24h).
4. Filter bot/self messages (rules above).
5. Resolve channel → sandbox: query DDB sandbox metadata GSI by
   `slack_channel_id` (new GSI required, or scan with cache; planner
   decides — GSI preferred).
6. Look up `(channel_id, thread_ts)` in `km_slack_threads`. Insert
   row if missing. (For top-level posts, `thread_ts = event.ts`.)
7. Write SQS message to that sandbox's queue with body:
   ```json
   {
     "channel": "C0123ABC",
     "thread_ts": "1714280400.001",
     "text": "<message text>",
     "user": "U0123ABC",
     "event_ts": "1714280450.002"
   }
   ```
   `MessageGroupId = sandbox-id`, `MessageDeduplicationId = event_id`.
8. If sandbox is paused (DDB metadata) AND no last-pause-hint within
   1h, post one-time "paused; message queued" via existing operator
   `post` action. Update last-pause-hint timestamp.
9. Return 200 immediately (Slack requires <3s ack).

**New IAM additions for bridge Lambda:**
- `sqs:SendMessage` on `{prefix}-slack-inbound-*.fifo`
- `dynamodb:Query`, `PutItem`, `GetItem` on `{prefix}-km_slack_threads`
- `dynamodb:Query` on the new `slack_channel_id` GSI of sandbox
  metadata.

### Sandbox-Side Poller (`km-slack-inbound-poller`)

New systemd unit + bash script, mirrors `km-mail-poller` pattern from
Phase 45:

- `/opt/km/bin/km-slack-inbound-poller` — bash script:
  - SQS long-poll (WaitTimeSeconds=20) on `KM_SLACK_INBOUND_QUEUE_URL`.
  - On message: parse JSON, look up session-id in DDB
    `km_slack_threads` (via AWS CLI; cached for 30s).
  - Export `KM_SLACK_THREAD_TS=<ts>` and invoke `km agent run --prompt
    <body-file> [--resume <session-id>] --wait`.
  - Capture output JSON, extract new `session_id`, update
    `km_slack_threads` via DDB CLI.
  - `DeleteMessage` only on successful turn capture.
  - Loop forever; restart on crash via systemd.
- `/etc/systemd/system/km-slack-inbound-poller.service` — systemd unit
  that depends on the network and is enabled iff
  `KM_SLACK_INBOUND_QUEUE_URL` is non-empty (compiler conditionally
  generates the unit).
- Service is started by the same compiler block that handles
  `km-mail-poller` (search for "km-mail-poller" in
  `pkg/compiler/userdata.go` ~line 1107 / 1667).

**Why bash + systemd, not a Go binary:** matches `km-mail-poller`
precedent, no new sidecar binary to ship via `km init --sidecars`,
trivially debuggable on the sandbox. Performance is irrelevant —
human-paced chat.

### Profile Schema Additions

`pkg/profile/types.go` and
`pkg/profile/schemas/sandbox_profile.schema.json` add one field under
`spec.cli`:

```yaml
spec:
  cli:
    notifySlackInboundEnabled: false  # default
```

Validation rules (above) added to `pkg/profile/validate.go`.

### `km create` / `km destroy` Changes

- `km create`:
  - When `notifySlackInboundEnabled: true`:
    - After per-sandbox channel creation, before user-data finalization,
      create the SQS queue via `sqs:CreateQueue` with FIFO attributes,
      14d retention, 30s VisibilityTimeout.
    - Store URL in sandbox metadata as `slack_inbound_queue_url`.
    - Inject `KM_SLACK_INBOUND_QUEUE_URL` into
      `/etc/profile.d/km-notify-env.sh` (joins existing `KM_SLACK_*`
      vars).
    - Failure aborts `km create`, tears down channel and infra (mirror
      Phase 63 channel-failure rollback).
  - After provisioning succeeds: post operator-signed "ready"
    announcement via existing bridge `post` action; record its `ts` in
    `km_slack_threads` with empty `claude_session_id`.

- `km destroy`:
  - Drain sequence (above) before existing channel-archive flow.

### Test Surface

**Profile schema tests (`pkg/profile/validate_test.go`):**
- `notifySlackInboundEnabled: true` without `notifySlackEnabled` → error.
- `notifySlackInboundEnabled: true` without `notifySlackPerSandbox` → error.
- `notifySlackInboundEnabled: true` with `notifySlackChannelOverride` → error.
- `notifySlackInboundEnabled: false` (default) → no validation impact.

**Compiler tests (`pkg/compiler/userdata_test.go`):**
- `notifySlackInboundEnabled: false` → no `km-slack-inbound-poller`
  systemd unit, no `KM_SLACK_INBOUND_QUEUE_URL`.
- `notifySlackInboundEnabled: true` → poller script written, systemd
  unit present and enabled, env var injected.

**Poller script tests:**
- Stub SQS → verify long-poll, parse, env-var export, agent run
  invocation, DDB write, message delete sequence.
- Failure paths: agent run fails → message returns to queue, no DDB
  write, no message delete.

**Bridge `/events` handler tests (`pkg/slack/bridge/handler_test.go`
extensions):**
- Valid Slack Events `message` event → SQS write + DDB upsert.
- Bot's own message → filtered, no SQS write.
- `subtype: bot_message` → filtered.
- Replayed `event_id` → 200 (Slack expects ack), no SQS write.
- Bad signing secret → 401.
- Stale timestamp → 401.
- `url_verification` challenge → echoed.
- Unknown channel (no sandbox match) → 200 + log warning, no SQS write.
- Top-level post → `thread_ts = event.ts`, new DDB row.
- In-thread reply → existing DDB row hit.
- Paused sandbox + first message → SQS write succeeds, "paused; queued"
  post triggered.
- Paused sandbox + second message within 1h → SQS write only, no
  duplicate hint.

**CLI command tests (`internal/app/cmd/`):**
- `km create` with `notifySlackInboundEnabled: true` happy path → SQS
  queue created, URL in metadata, env var injected, ready message
  posted.
- `km create` with SQS-create failure → infra rolled back including
  channel.
- `km destroy` with messages in queue → drain attempted, queue deleted,
  channel archived.
- `km status` shows queue depth, last-receive, thread count.
- `km doctor --all-regions` flags stale queues.

**E2E (manual / opt-in CI):**
- Real workspace; create sandbox with `notifySlackInboundEnabled:
  true`. Reply in `#sb-<id>` with a benign prompt. Confirm:
  Claude responds in-thread within ~10s. Multi-turn reply continues
  same Claude session. Top-level new post starts a separate thread
  with separate session.
- Pause sandbox; post message; confirm "paused; queued" hint and
  message persists. Resume; confirm Claude responds.
- Destroy with active queue → drain + archive.

### Implementation Footprint (Files Touched)

| Area | Files |
|---|---|
| Profile schema | `pkg/profile/types.go`, `pkg/profile/schemas/sandbox_profile.schema.json`, `pkg/profile/validate.go` |
| Compiler | `pkg/compiler/userdata.go` (poller script + systemd unit + env var), `pkg/compiler/userdata_test.go` |
| Poller | `/opt/km/bin/km-slack-inbound-poller` (inlined heredoc in compiler), `/etc/systemd/system/km-slack-inbound-poller.service` (inlined) |
| Bridge Lambda | `cmd/km-slack-bridge/main.go`, `pkg/slack/bridge/events_handler.go` (new), `pkg/slack/bridge/handler.go` (router by path), `pkg/slack/bridge/aws_adapters.go` (SQS adapter) |
| Bridge IAM | `infra/modules/lambda-slack-bridge/main.tf` |
| DDB table | `infra/modules/dynamodb-slack-threads/` (new), `infra/live/.../management/dynamodb/terragrunt.hcl` |
| Sandbox metadata GSI | `infra/modules/dynamodb-sandboxes/main.tf` (new GSI on `slack_channel_id`) |
| Sandbox SQS queue | provisioned at runtime via SDK in `internal/app/cmd/create.go` (no Terraform — per-sandbox lifecycle) |
| Operator CLI | `internal/app/cmd/create.go` (queue + ready msg), `internal/app/cmd/destroy.go` (drain + delete), `internal/app/cmd/status.go`, `internal/app/cmd/list.go`, `internal/app/cmd/doctor.go`, `internal/app/cmd/slack.go` (init flow extended for Events API URL + scopes) |
| Documentation | `docs/slack-notifications.md` (extend), `CLAUDE.md` (new env var, profile field) |

**No new sandbox sidecar binaries.** The poller is bash + systemd,
matching `km-mail-poller`. Bridge stays single-Lambda.

### Suggested Wave Decomposition (Hint to Planner)

This is a hint — planner can repartition.

**Wave 1 — Foundations (parallelizable):**
- 1A: Profile schema + validation (`spec.cli.notifySlackInboundEnabled` +
  three validation rules + tests).
- 1B: DDB table module for `km_slack_threads` + sandbox-metadata GSI on
  `slack_channel_id` (Terraform; deployable independently).
- 1C: Bridge Slack Events handler (`/events` route, signing-secret
  verify, bot-loop filter, top-level vs threaded post handling — pure
  Go with mocked SQS/DDB).

**Wave 2 — Integration (depends on Wave 1):**
- 2A: Compiler changes (poller script + systemd unit, env var,
  conditional generation; depends on 1A).
- 2B: Bridge SQS adapter + DDB session-map writes (depends on 1B + 1C).
- 2C: Sandbox SDK provisioning in `km create` (depends on 1A + 2B).

**Wave 3 — Operator surface (depends on Wave 2):**
- 3A: `km create` ready announcement + thread record (depends on 2C +
  existing Phase 63 operator-signed `post`).
- 3B: `km destroy` drain + queue delete + thread cleanup.
- 3C: `km status` / `km list --wide` / `km doctor` extensions.
- 3D: `km slack init` extensions (Events API URL setup, scope check).

**Wave 4 — End-to-end + docs (depends on Wave 3):**
- 4A: E2E test (real workspace, opt-in CI flag).
- 4B: `docs/slack-notifications.md` updates (operator inbound guide).
- 4C: `CLAUDE.md` updates.

### Claude's Discretion

- Sandbox-metadata GSI vs. table scan with cache for `slack_channel_id`
  → planner decides based on existing patterns. GSI preferred for
  consistency.
- Bash poller exact polling cadence (WaitTimeSeconds, retry backoff
  on transient SQS errors).
- DDB cache TTL on the sandbox poller side (probably 30s for thread
  lookups).
- Whether to extract the poller heredoc to a separate Go-embedded
  asset file or inline (follow Phase 62/63 precedent — inline).
- Exact wording of the "ready" announcement and "paused; queued" hint.

</decisions>

<specifics>
## Specific References

- **Phase 63 envelope schema** — `pkg/slack/payload.go:23-67`. Already
  includes `version`, `action`, `thread_ts`, `nonce` fields. v1
  consumed `version=1` and three actions (`post`/`archive`/`test`).
  Phase 67 adds NO new envelope fields — `/events` is a separate
  Lambda route with its own webhook payload (Slack Events API), not
  the bridge envelope.
- **Phase 63 Slack hook integration** —
  `pkg/compiler/userdata.go:343-448`. Phase 67 extends the same hook
  block: env var `KM_SLACK_THREAD_TS` is read (if set) and passed as
  `--thread "$KM_SLACK_THREAD_TS"` to `km-slack post` (the `--thread`
  flag is already wired in `cmd/km-slack/main.go:47` but unused in v1).
- **`km-mail-poller` precedent** — `pkg/compiler/userdata.go:739-856`
  for the script, `pkg/compiler/userdata.go:1107` for the systemd
  unit, line 1667 for systemctl enable/start. Mirror this pattern
  exactly for `km-slack-inbound-poller`.
- **Bridge envelope nonce DDB table** — already provisioned for replay
  protection. New `km_slack_threads` table is separate (different
  access pattern — composite key, longer TTL).
- **Per-sandbox channel creation** — `internal/app/cmd/create.go`
  Phase 63 flow (search "Slack channel"). Queue creation joins this
  flow at the same point.
- **Slack Events API setup:** Slack App needs Events API URL
  configured at `https://{bridge-fn-url}/events`. Required scopes
  beyond Phase 63's `chat:write`/`channels:manage`/`conversations.connect:write`/`groups:write`:
  `channels:history` (read messages in public channels),
  `groups:history` (private channels). `app_mentions:read` not needed
  in v1 (no @bot spawning) — captured in deferred for next phase.
- **Slack signing secret:** stored at `/km/slack/signing-secret` (new
  SSM SecureString). Operator captures during `km slack init` (extend
  the prompt) when configuring the Slack App.
- **`km agent run --resume` output JSON shape** — see
  `internal/app/cmd/agent.go` and `cmd/agent/...` for the actual
  field name; planner should grep current code for `session_id` /
  `--resume` to confirm. Capture session-id from JSON output for the
  DDB write-back.
- **OpenSSL 3.5+ body-via-file constraint** — applies if the poller
  ever signs anything. Currently it doesn't — SQS is internal AWS
  transport — but if a future v2 needs sandbox-side signing of
  outbound responses through Slack, follow the precedent.

</specifics>

<code_context>
## Existing Code Insights

### Reusable Assets

- **`km-slack-bridge` Lambda** (`cmd/km-slack-bridge/main.go`,
  `pkg/slack/bridge/`) — extend with new `/events` path. Existing
  `version`/`action` envelope discriminator preserved; the new path is
  separate (Slack's webhook format is fixed by Slack, not our
  envelope).
- **`km_slack_bridge_nonces` DDB table** — reuse for Slack Events
  `event_id` dedup (separate prefix or just the event_id string is
  fine since collision space is distinct).
- **`/etc/profile.d/km-notify-env.sh` env injection pattern**
  (`pkg/compiler/userdata.go:460`) — add `KM_SLACK_INBOUND_QUEUE_URL`
  to the `NotifyEnv` template data.
- **`km-mail-poller` systemd + bash poller pattern** (lines 739-856,
  1107, 1667) — clone for the SQS poller. Same lifecycle hooks
  (enable, start, restart on failure).
- **Phase 63 hook env var → flag pass-through pattern**
  (`pkg/compiler/userdata.go:434-440`) — extend with one more
  conditional: read `KM_SLACK_THREAD_TS`, pass as `--thread "$ts"` if
  non-empty.
- **`km-slack post --thread <ts>`** — flag already wired
  (`cmd/km-slack/main.go:47`), unused in v1. Phase 67 finally consumes
  it.
- **Existing operator-signed `post` action** — used by `km slack test`
  and `km destroy` final post. `km create` ready announcement reuses
  this exact path: operator signs envelope, bridge posts to channel.
  No new bridge action needed for the announcement.
- **`km agent run` JSON output capture path** — Phase 50/51 already
  writes run output to `/workspace/.km-agent/runs/<ts>/output.json`.
  Poller reads this same file to extract session-id.
- **`km doctor --all-regions`** — extension point already exists for
  per-region resource checks (Phase 64); add SQS queue + DDB
  `km_slack_threads` checks here.

### Established Patterns

- **Profile-only feature flags, no CLI override** — Phase 63's five
  Slack fields. Phase 67 follows same pattern with one new field.
- **Fail-fast at km create on Slack-side provisioning failure** —
  Phase 63 channel-create failure aborts and rolls back. Queue-create
  failure follows same rule.
- **Inline bash heredocs in `pkg/compiler/userdata.go`** — Phase 62's
  hook script, Phase 45's mail poller. New SQS poller follows
  precedent; do NOT extract to `pkg/compiler/assets/*.sh`.
- **DDB table naming with resource_prefix** — Phase 66 introduces
  `cfg.GetResourcePrefix()` and `cfg.GetSsmPrefix()` for multi-instance
  support. New `km_slack_threads` table and SQS queue names MUST use
  these helpers, not hardcoded names.
- **Per-sandbox AWS resource lifecycle** — Phase 63 creates Slack
  channels per-sandbox via SDK at runtime (not Terraform). SQS queue
  follows the same pattern: SDK call from `km create`, NOT a
  Terraform module.
- **Single Lambda Function URL, multiple routes** — bridge already
  uses path-based dispatch in spirit; formalize with explicit `/`
  vs `/events` routing.

### Integration Points

- `internal/app/cmd/create.go` — extend Slack-channel-creation block
  to also create SQS queue. Inject env var into compiler input.
- `internal/app/cmd/destroy.go` — extend drain/archive sequence.
- `pkg/compiler/userdata.go` (~line 343 for hook block, ~line 460
  for env file, ~line 739 for mail poller, ~line 1107 for systemd
  unit, ~line 1667 for systemctl enable list) — three insertion points.
- `pkg/slack/bridge/handler.go` — split into router + per-route
  handlers (`/` keeps existing logic, `/events` is new).
- `pkg/profile/types.go` — add one field.
- `cmd/km-slack/main.go` — no change (the `--thread` flag works
  today).
- `infra/live/.../management/dynamodb/` — add `km_slack_threads`
  module instance.

</code_context>

<deferred>
## Deferred Ideas (out of scope for Phase 67)

- **Mention-based sandbox spawning** (`@km-bot create profile=foo
  prompt="..."` in any channel → calls `km create` + first agent
  run). Its own phase. Requires `app_mentions:read` Slack scope and
  a separate Lambda action that authenticates against operator
  identity.
- **Permission-prompt round-trip** (Slack reply → Claude permission
  decision). Needs Claude SDK runtime-decision API that doesn't
  exist; for now `--dangerously-skip-permissions` and profile
  `allowedTools` cover this.
- **Slack interactive features** — Block Kit buttons, slash commands,
  modals. Would enable better UX for permission decisions, sandbox
  controls (pause/resume buttons in Slack), turn cancellation.
- **Auto-resume of paused sandboxes on inbound** — bridge Lambda
  triggers `km resume` when message arrives for paused sandbox.
  Possible profile field `notifySlackInboundAutoResume` for v2.
- **Inbound on shared channel `#km-notifications`** — would require
  bot-mention parsing to disambiguate target sandbox.
- **Inbound on `notifySlackChannelOverride` mode** — multi-sandbox
  pinning to a single channel makes routing ambiguous.
- **DM delivery / inbound via DMs** — different scope, different
  routing.
- **Block Kit / rich formatting for outbound replies** — same as
  Phase 63 deferred.
- **Multi-recipient routing per event-type** — e.g., permission goes
  to channel A, idle to channel B.
- **CLI flag overrides** for `notifySlackInboundEnabled`.
- **Retroactive inbound on Phase 63 sandboxes** — destroy/recreate
  required, same as Phase 63's hook rule.
- **Turn cancellation / interrupt** — currently FIFO + serialize. v2
  could add "stop" word handling or Block Kit cancel button.
- **Multi-sandbox cross-talk** — sandbox A's reply mentions sandbox B,
  bridge routes the mention. Belongs to a multi-agent orchestration
  phase.
- **Slack-thread → Claude transcript mirroring** — saving full
  threads back to the sandbox for replay/audit. Possible OTEL phase
  enhancement.

</deferred>

---

*Phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch*
*Context gathered: 2026-05-02*
