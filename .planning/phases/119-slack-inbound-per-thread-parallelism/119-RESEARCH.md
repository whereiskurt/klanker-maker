# Phase 119: Slack inbound per-thread parallelism - Research

**Researched:** 2026-06-24
**Domain:** SQS FIFO MessageGroupId semantics, bash counting-semaphore concurrency, SQS visibility heartbeating, profile-field plumbing, golden byte-identity discipline
**Confidence:** HIGH (every load-bearing claim verified against current source at file:line; AMI bash versions verified against distro defaults)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Layer 1 — Bridge: group by thread**
- Change `MessageGroupId` from `info.SandboxID` → `threadTS` at BOTH `h.SQS.Send(...)` sites in `events_handler.go` (files path + no-files path).
- `threadTS` is already in scope (computed ~L403 as `msg.ThreadTS`, falling back to `msg.TS` for a new top-level message). No new data plumbing — swap the `groupID` arg.
- `MessageDeduplicationId` (the Slack `event_id`) is UNCHANGED.
- **Unconditional rollout** — no new bridge env flag. Safe for cap=1 sandboxes.

**Layer 2 — Poller: bounded concurrent dispatch**
- New profile field `spec.notification.slack.inbound.maxConcurrentThreads` (`*int`, default **1**) → env var `KM_SLACK_MAX_CONCURRENCY` via the existing `.NotifyEnv` map → `/etc/km/notify.env` + `/etc/profile.d/km-notify-env.sh`.
- Poller loop: `receive-message --max-number-of-messages N` (N from cap, SQS max 10); dispatch each agent turn in a backgrounded subshell behind a counting semaphore (bash `wait -n` + in-flight counter) so ≤ cap turns run at once.
- **ACK (delete) the message AFTER the turn completes**, inside the subshell. REVERSES today's ack-first delete and is REQUIRED for per-thread ordering.
- **Visibility heartbeat:** per in-flight message, a background ticker calls `ChangeMessageVisibility` every ~T/3 until the turn finishes.
- cap=1 ⇒ degenerates to today's serial loop.

**Ack-ordering dup-window — DECISION REQUIRED IN PLANNING**
- Recommendation: add a Slack per-turn idempotency guard mirroring the Phase 108 GitHub `<!-- km-turn:$ID -->` marker. Alternative: accept the rare crash-only dup. Planner picks one and justifies.

**Schema**
- Add `MaxConcurrentThreads *int` to `NotificationSlackInboundSpec` (`pkg/profile/types.go:238`), mirror the `Allow []string` / `ReactAlways *bool` pattern.
- JSON schema: integer, `minimum: 1`, `additionalProperties:false` preserved.
- `km validate` WARNS when `maxConcurrentThreads > 1` but `perSandbox` / `inbound.enabled` not set.

### Claude's Discretion (planner decides)
- Exact bash counting-semaphore idiom and heartbeat-ticker implementation.
- New base `VisibilityTimeout` value for the Slack inbound queue (currently 30s; H1 uses 1800s) and whether to raise the static base in addition to heartbeating.
- Whether `KM_SLACK_MAX_CONCURRENCY` also bounds `--max-number-of-messages` (min(cap,10)) vs always fetch a small batch.
- Run-output dir collision mitigation (pid/random suffix) if needed under concurrency.
- Wave/plan breakdown.

### Deferred Ideas (OUT OF SCOPE)
- Per-thread git-worktree isolation for repo-mutating parallel turns (follow-up phase).
- GitHub / HackerOne bridge parity for thread/per-target parallelism.
</user_constraints>

## Summary

This phase has an unusually clean recon already done by the design spec; every claimed file:line was re-verified against live source and **all check out**. The work splits into a one-line-each bridge change (Layer 1), a profile-field/schema/validation addition (standard "mirror the Phase 118 `allow` pattern" plumbing), and the genuinely novel piece: rewriting the sandbox-side Slack inbound poller loop in `pkg/compiler/userdata.go` from a serial blocking executor into a bounded-concurrent dispatcher with ack-after-completion and a visibility heartbeat (Layer 2).

The single highest-risk surface is **golden byte-identity**: `profiles/learn.v2.yaml` enables `slack.inbound.enabled: true`, so the inbound poller bash IS embedded in the FROZEN `userdata_learn_v2_pre92_baseline.golden.sh` (27 references confirmed). ANY poller-bash change drifts that frozen baseline, which must be **HAND-PATCHED, never re-captured** (the SubagentStop capture trap). The `h1_byte_identity` and `additional_volume_only` goldens do NOT contain the poller (0 references) and are unaffected.

**Primary recommendation:** Swap `info.SandboxID` → `threadTS` at both Send sites (Layer 1, trivially testable). For Layer 2, use a `wait -n`-based counting semaphore (bash ≥ 4.3 is guaranteed on every supported AMI — AL2023 ships 5.2, Ubuntu 22.04 ships 5.1, Ubuntu 24.04 ships 5.2), with each subshell carrying its own `RECEIPT` and `RUN_ID` so it deletes its OWN message after its OWN turn. Raise the static base `VisibilityTimeout` to a meaningful value (match H1's 1800s) AND add a per-message heartbeat (defense-in-depth; cheaper than betting a turn never exceeds the base). Adopt the Slack per-turn idempotency guard, but implement it as a **thread-history scan in the poller** (NOT a GitHub-style marker in the agent's own post path — the Slack reply path differs structurally; see Pitfall 3).

<phase_requirements>
## Phase Requirements

No REQUIREMENTS.md IDs are mapped to this phase (additive feature). The must-haves below are derived from the approved design spec / CONTEXT.md and stand in for requirement IDs in the validation map.

| ID (derived) | Description | Research Support |
|----|-------------|-----------------|
| P119-A | Bridge groups inbound messages by Slack thread, not sandbox | Swap `groupID` arg at `events_handler.go:470` + `:490`; `threadTS` in scope from `:403` |
| P119-B | Poller dispatches up to N concurrent turns, serial within a thread | `wait -n` counting semaphore + backgrounded subshell in `userdata.go` poller loop (~L1670–2122) |
| P119-C | Operator caps concurrency via a profile field; dormant default=1 | `MaxConcurrentThreads *int` → `KM_SLACK_MAX_CONCURRENCY` via NotifyEnv (`userdata.go:5459–5523`) |
| P119-D | Per-thread ordering preserved (ack-after-completion) | Move delete from `userdata.go:2072` to after the turn, inside the subshell |
| P119-E | Long turns do not redeliver mid-turn (visibility heartbeat) | `ChangeMessageVisibility` ticker; base `VisibilityTimeout` at `sqs.go:127` |
| P119-F | Crash-redelivery dup suppressed (idempotency) | Poller thread-history scan (Phase-108 marker analog; see Pitfall 3) |
| P119-G | `km validate` WARNS on cap>1 without perSandbox/inbound | Mirror `validate.go:343–362` Phase-118 WARN rules |
</phase_requirements>

## Standard Stack

This is an internal-platform phase — no new external libraries. The "stack" is the existing in-repo primitives that the implementation must reuse verbatim.

### Core (reuse these exact seams)
| Component | Location | Purpose | Why Standard |
|-----------|----------|---------|--------------|
| `SQSSender.Send(ctx, url, body, groupID, dedupID)` | `pkg/slack/bridge/events_interfaces.go:73`; impl `aws_adapters.go:835` | The bridge FIFO write; `groupID` is the 4th arg | This is the ONLY change point for Layer 1 |
| `inboundQueueAttrs(dlqARN)` | `pkg/aws/sqs.go:123` | Builds the Slack/GitHub FIFO attr map; `VisibilityTimeout:"30"` at `:127` | The base-timeout knob for the queue |
| `h1InboundVisibilityTimeout = "1800"` | `pkg/aws/sqs.go:77` | H1's 30-min timeout precedent | Proven pattern for "agent turns run minutes" |
| `.NotifyEnv` map → `notify.env` + `profile.d` | `userdata.go:5459–5523` (populate), `:1096–1121` (render) | Sandbox-side env plumbing | `KM_SLACK_MAX_CONCURRENCY` rides this exact path |
| Phase 108 idempotency marker | `pkg/github/marker.go` | `TurnMarker`/`CommentMarkerExists` precedent | The idempotency-guard reference design |
| `deepMerge(map[string]any)` | `pkg/profile/inherit.go:27` | Phase-117 profile inheritance | Handles the new `*int` scalar for free (see Pitfall 5) |

### Supporting (the AMIs / runtime)
| Component | Version | Purpose | When relevant |
|-----------|---------|---------|---------------|
| bash on AL2023 | 5.2.x | `wait -n` semaphore | AL2023 default shell |
| bash on Ubuntu 24.04 | 5.2.x | `wait -n` semaphore | Ubuntu desktop/learn AMIs |
| bash on Ubuntu 22.04 | 5.1.x | `wait -n` semaphore | Older Ubuntu AMIs |
| AWS CLI v2 `sqs change-message-visibility` | bundled | heartbeat call | already used at `userdata.go:1702` |

**`wait -n` requires bash ≥ 4.3** (released 2014). Every supported AMI ships ≥ 5.1, so `wait -n` is **safe on all targets** — no fallback idiom needed. (HIGH confidence: AL2023 and Ubuntu 22.04/24.04 default-shell bash versions are well-established distro facts; the existing poller already uses bash-4+ features like `${BASH_REMATCH[1],,}` lowercase expansion at `userdata.go:1732`, proving the runtime is ≥ 4.0.)

## Architecture Patterns

### Pattern 1: Layer-1 bridge swap (the trivial half)

**What:** Change the 4th positional arg to `h.SQS.Send` at both call sites.

**Verified call sites (`pkg/slack/bridge/events_handler.go`):**
```go
// L470 (files path):
if err := h.SQS.Send(bgCtx, info.QueueURL, string(sqsBodyBytes), info.SandboxID, dedupID); err != nil {
// L490 (no-files path):
if err := h.SQS.Send(ctx, info.QueueURL, string(bodyBytes), info.SandboxID, dedupID); err != nil {
```
Change `info.SandboxID` → `threadTS` at BOTH. `threadTS` is computed at L402–406:
```go
threadTS := msg.ThreadTS
if threadTS == "" {
    threadTS = msg.TS
}
```
**`threadTS` is GUARANTEED non-empty at both Send sites** — `msg.TS` is always set on a real `message` event (it is the message's own Slack timestamp), and the fallback at L404–405 ensures a brand-new top-level mention gets `threadTS = msg.TS`. So FIFO's hard requirement (non-empty `MessageGroupId`) is satisfied. **If `threadTS` were ever empty, `SendMessage` would fail with `InvalidParameterValue` (FIFO requires MessageGroupId)** — but that path is unreachable given the L404 fallback.

**Also update the doc comment** at `aws_adapters.go:827–828` which currently reads "MessageGroupId is the sandboxID" — leaving it stale is a documentation regression.

### Pattern 2: Counting semaphore with per-job receipt tracking

**What:** Run up to N background jobs; block when full; each job deletes its own message.

**Why this idiom:** `wait -n` (bash ≥ 4.3) blocks until ANY ONE background job exits, vs `wait` (blocks for ALL). Combined with an in-flight counter you get a true counting semaphore on one physical queue.

**Concrete, copy-pasteable skeleton (planner refines):**
```bash
MAX="${KM_SLACK_MAX_CONCURRENCY:-1}"
# Clamp: SQS receive max is 10; cap must be >= 1.
[ "$MAX" -lt 1 ] && MAX=1
BATCH=$MAX; [ "$BATCH" -gt 10 ] && BATCH=10

inflight=0
while true; do
  MSGS=$(aws sqs receive-message --queue-url "$QUEUE_URL" \
    --wait-time-seconds 20 --max-number-of-messages "$BATCH" \
    --region "$REGION" --output json 2>/dev/null || true)

  COUNT=$(echo "$MSGS" | jq -r '.Messages | length' 2>/dev/null || echo 0)
  [ "$COUNT" -eq 0 ] && continue

  for i in $(seq 0 $((COUNT-1))); do
    BODY=$(echo "$MSGS" | jq -r ".Messages[$i].Body // empty")
    RECEIPT=$(echo "$MSGS" | jq -r ".Messages[$i].ReceiptHandle // empty")
    [ -z "$BODY" ] && continue

    # Block until a slot frees (counting semaphore).
    while [ "$inflight" -ge "$MAX" ]; do
      wait -n 2>/dev/null || true      # any one subshell finished
      inflight=$((inflight-1))
    done

    # Each subshell carries its OWN $RECEIPT and computes its OWN $RUN_ID
    # so it deletes its OWN message after its OWN turn completes.
    (
      handle_one_turn "$BODY" "$RECEIPT"   # see Pattern 3 + 4
    ) &
    inflight=$((inflight+1))
  done
done
```

**CRITICAL — receipt-handle scoping:** because the subshell is `( ... ) &`, it gets a COPY of `$BODY` and `$RECEIPT` at fork time. There is NO shared-variable hazard between concurrent jobs — each job's `RECEIPT` is frozen at the moment of backgrounding. `RUN_ID` MUST be computed INSIDE the subshell (or made unique per job) — `date -u +%Y%m%dT%H%M%SZ` at second granularity (current `userdata.go:1759`) WILL collide under concurrency. **Add a uniqueness suffix** (e.g. `$RUN_ID-$$-$RANDOM` or `$RUN_ID-$i`) to `RUN_DIR=/workspace/.km-agent/runs/$RUN_ID` (current `:1760`) — this is the run-output-dir collision the spec flagged.

**Anti-pattern — `set -e` interaction:** the current poller uses `set -euo pipefail` (`userdata.go` heredoc header). `wait -n` returns the exit status of the reaped job; under `set -e` a non-zero job exit would kill the poller. Guard every `wait -n` with `|| true` (as in the skeleton) and keep the existing `... || true` on each `sudo -u sandbox bash -lc "..."` dispatch so an agent failure never propagates `set -e` into the loop.

### Pattern 3: Visibility heartbeat ticker

**What:** Per in-flight message, a background ticker extends visibility until the turn finishes.

**Current state:** the poller already does a ONE-SHOT extension to 300s BEFORE the turn (`userdata.go:1701–1706`):
```bash
aws sqs change-message-visibility --queue-url "$QUEUE_URL" \
  --receipt-handle "$RECEIPT" --visibility-timeout 300 --region "$REGION" 2>/dev/null || true
```
This is insufficient for parallel + ack-after-completion: a turn can exceed 300s, and there is no renewal. Replace with a heartbeat loop inside each subshell:
```bash
HEARTBEAT_INTERVAL=120   # ~T/3 if base/extend is ~360
HEARTBEAT_TIMEOUT=360
(
  while true; do
    sleep "$HEARTBEAT_INTERVAL"
    aws sqs change-message-visibility --queue-url "$QUEUE_URL" \
      --receipt-handle "$RECEIPT" --visibility-timeout "$HEARTBEAT_TIMEOUT" \
      --region "$REGION" 2>/dev/null || true
  done
) &
HB_PID=$!
# ... run the agent turn ...
kill "$HB_PID" 2>/dev/null || true   # teardown when the turn finishes
wait "$HB_PID" 2>/dev/null || true
```
**Error handling:** `ChangeMessageVisibility` fails harmlessly once the message is deleted (`ReceiptHandleIsInvalid`) — the `|| true` swallows it, and the ticker is `kill`ed immediately after the turn anyway. The heartbeat extends to an ABSOLUTE timeout each call (SQS resets the clock to now+timeout), so a single dropped heartbeat call does not redeliver as long as the next one lands within the window.

**Base timeout recommendation (Claude's discretion, but with a clear answer):** raise the static base at `sqs.go:127` from `"30"` to match H1's `1800` (or a dedicated `slackInboundVisibilityTimeout` const) AND keep the heartbeat. Rationale: the static raise covers the common case (turns < 30 min) with zero heartbeat dependency; the heartbeat covers the long tail. Belt-and-suspenders is cheap and the H1 precedent already exists. **Caveat (deploy surface):** the base raise applies to **newly created per-sandbox queues only** — existing sandboxes keep their 30s queues until `km destroy && km create`. So the heartbeat is REQUIRED regardless (it is the only mechanism that helps already-provisioned 30s queues). This argues for shipping the heartbeat as the primary mechanism and treating the base raise as a future-sandbox optimization.

### Pattern 4: Ack-after-completion (the ordering reversal)

**What:** Move the SQS delete from its current position to AFTER the turn AND the Slack post complete, inside the subshell.

**Current ordering (`userdata.go`):** the message is deleted at L2072–2075 (inside the `if [ -n "$NEW_SESSION" ]` success block, AFTER the agent run and DDB put-item, but BEFORE the `km-slack post` at L2100). The L2069 comment calls this "Ack first" — but note it is ack-first relative to the SLACK POST, NOT relative to the agent run (the agent has already completed by L2072). On a host crash between L2075 (delete) and L2106 (post-confirmed) the user gets NO reply but no dup either (the "lost reply" trade the old code chose).

**Required change for FIFO per-thread ordering:** the message must remain IN-FLIGHT (undeleted) for the ENTIRE turn so SQS does not release the thread's NEXT message mid-turn. Move the `delete-message` to the very END of the subshell, after the `km-slack post` succeeds. This is what the design spec calls the "reversal."

**Resulting crash-redelivery dup window:** with delete-last, a host crash AFTER the `km-slack post` succeeds but BEFORE the `delete-message` lands leaves the message in-flight; when its visibility times out, SQS redelivers → the poller runs the agent AGAIN and posts a SECOND reply. This is the dup window the old ack-first code dodged. It is **crash-only** (not a normal-path event) and bounded by the visibility timeout. Note: the agent RESUME (`--resume $CLAUDE_SESSION`) means the redelivered turn resumes the same session, so it is not a from-scratch dup — but it IS a second visible Slack reply.

### Anti-Patterns to Avoid
- **Computing `RUN_ID` outside the subshell** → run-dir collision under concurrency (Pattern 2).
- **Using `wait` instead of `wait -n`** → blocks for ALL jobs, serializing the batch (defeats the purpose).
- **Leaving the one-shot 300s extension** as the only visibility mechanism → redelivers on a >300s turn.
- **Re-capturing the frozen golden** → SubagentStop trap (Pitfall 6).
- **Adding a DDB row attr / SandboxMetadata field for the cap** → unnecessary; cap is poller-only (Pitfall 5).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Per-thread serialization | A second physical SQS queue per thread | FIFO `MessageGroupId = threadTS` | One physical queue already gives serial-within-group, parallel-across-group |
| Bounded concurrency | A custom job-tracker with PID arrays + polling | bash `wait -n` + integer counter | `wait -n` is the canonical counting-semaphore primitive on bash ≥ 4.3 |
| Env plumbing for the cap | A new DDB attr + SandboxMetadata round-trip + FetchByChannel read | `.NotifyEnv` map (`userdata.go:5459`) | The cap is consumed SANDBOX-SIDE only; the bridge never reads it (Pitfall 5) |
| Idempotency marker | A bespoke Slack metadata scheme | Thread-history scan keyed on `RUN_ID` (Phase-108 analog) | Reuses the proven per-turn-marker mental model (Pitfall 3) |
| `*int` profile merge | A typed merger for the new field | Phase-117 `deepMerge` (`inherit.go:27`) | A scalar merges as "right-wins" for free via the YAML round-trip |

**Key insight:** every primitive this phase needs already exists in-repo and is battle-tested. The novelty is purely in COMPOSING them in the poller bash — which is why the live-E2E pass matters more than the unit goldens (skill/poller bash is invisible to Go goldens; see Pitfall 6).

## Common Pitfalls

### Pitfall 1: Run-output-dir collision under concurrency
**What goes wrong:** `RUN_ID=$(date -u +%Y%m%dT%H%M%SZ)` (`userdata.go:1759`) is second-granular. Two threads dispatched in the same second share `/workspace/.km-agent/runs/$RUN_ID` → `output.json` / `exit_code` / `stderr.log` clobber each other → wrong reply posted to a thread, or a turn reads the other's session ID.
**Why it happens:** the serial poller never had two turns in the same second; concurrency breaks the implicit uniqueness.
**How to avoid:** add a per-job suffix — `RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$-$RANDOM"` (compute inside the subshell). `km agent list` / `km agent results --run <id>` still work (they glob the runs dir).
**Warning signs:** in live E2E, a reply lands in the wrong thread, or two threads get identical replies.

### Pitfall 2: `set -e` killing the poller on a backgrounded-job non-zero exit
**What goes wrong:** `set -euo pipefail` + `wait -n` returning a failed job's exit code → poller exits, all Slack inbound silently dies.
**Why it happens:** `wait -n`'s exit status IS the reaped job's status; `set -e` treats non-zero as fatal.
**How to avoid:** `wait -n 2>/dev/null || true` on every reap; keep `... || true` on every `sudo` dispatch (already present at `:1966/:1979/:2004`).
**Warning signs:** `journalctl -u km-slack-inbound-poller` shows the loop exiting after the first failed turn.

### Pitfall 3: A GitHub-style marker does NOT drop into the Slack reply path unchanged
**What goes wrong:** the Phase-108 GitHub guard works because the AGENT itself invokes `km-github comment`, and `km-github` (the helper, `pkg/github/marker.go`) scans existing comments for `<!-- km-turn:$KM_GITHUB_TURN_ID -->` before posting. In Slack, the reply is posted by the POLLER (`km-slack post` at `userdata.go:2100`), not by the agent — `km-slack post` has no marker/scan logic (verified: only a `--thread` flag exists, no `--meta`/marker). So there is no helper chokepoint to graft the GitHub design onto.
**Why it happens:** different post architectures — GitHub agent-posts, Slack poller-posts.
**How to avoid (recommended design):** implement the idempotency guard IN THE POLLER, before the `km-slack post` call. Options for "where the marker lives":
  - **(A) Slack thread-history scan (recommended):** before posting, `conversations.history?channel=$CHANNEL&latest=...&inclusive=true` (or `conversations.replies?ts=$THREAD_TS`) via the bot token, and check whether a bot reply carrying an invisible `RUN_ID` sentinel already exists. The sentinel can be a zero-width or HTML-comment-style suffix appended to the posted body and grepped on re-scan — mirrors the GitHub marker exactly but on Slack's own message store. Fail-OPEN on a scan error (post anyway), matching `markerExists`' contract (`pkg/github/marker.go:49`).
  - **(B) Reaction marker:** add a reaction (e.g. a custom emoji) keyed per turn — clumsier (reactions are not per-turn-keyable cleanly), NOT recommended.
  - **(C) DDB last-posted-run guard:** record `last_posted_run_id` on the `km-slack-threads` row and skip the post if the redelivered turn's `RUN_ID` matches. **Cleanest of the three** — the row is already written per turn (`userdata.go:2055–2067`), keyed `(channel, thread_ts)`, and the poller already reads it at turn start (`:1709`). A redelivered message reuses the SAME body/session, so the poller can detect "I already posted a reply for this exact inbound message" via the dedup `event_id` (which is the SQS `MessageDeduplicationId`) rather than `RUN_ID`. **Recommendation: option (C) keyed on the inbound `event_id`/`EventTS`**, because the redelivered SQS message carries the SAME `event_id` — store `last_processed_event_ts` on the thread row and skip a turn whose `event_ts` was already fully processed+posted.
**Trade-off vs accepting the dup:** option (C) is ~2 lines of jq + one DDB attr, no extra Slack API call, no fail-open ambiguity. **Recommendation: ADD the guard (option C).** It is cheaper than the GitHub thread-scan, consistent with the "per-turn idempotency" intent, and the dup it prevents (a duplicate visible Slack reply on crash-redelivery) is exactly the kind of user-visible glitch this platform avoids elsewhere.
**Warning signs:** heartbeat E2E (turn > base visibility) shows TWO identical replies.

### Pitfall 4: Frozen golden contains the poller — it WILL drift
**What goes wrong:** `userdata_learn_v2_pre92_baseline.golden.sh` contains the Slack inbound poller (27 references confirmed) because `profiles/learn.v2.yaml` sets `slack.inbound.enabled: true` (`learn.v2.yaml:140–141`). Any poller-bash edit changes the rendered userdata → `TestUserdataLearnV2Phase92ByteIdentity` (`userdata_phase92_byte_identity_test.go:108`) fails on the "rest" comparison (everything outside the settings.json blob must stay byte-identical).
**Why it happens:** the byte-identity contract pins the WHOLE userdata except the settings.json blob.
**How to avoid:** HAND-PATCH the frozen golden (git-checkout the file, surgically apply the same bash edits with Edit/sed). Do NOT run `CAPTURE_PRE92_BASELINE=1` — that folds the post-baseline SubagentStop script into the frozen baseline and corrupts it (the test STRIPS SubagentStop via `stripSubagentStopScript` at `:118`; re-capture defeats that). See memory `project_frozen_byte_identity_golden_capture_trap` and `project_skill_bash_needs_live_uat`.
**Warning signs:** `TestUserdataLearnV2Phase92ByteIdentity` fails with a diff in the poller region after a `CAPTURE_*` run.

### Pitfall 5: The cap does NOT need a DDB attr (contrast Phase 91.5/118)
**What goes wrong:** copying the Phase-91.5 `reactAlways` or Phase-118 `allow` plumbing wholesale would add a `km-sandboxes` row attr + `SandboxMetadata` round-trip + `FetchByChannel` read — all unnecessary.
**Why it happens:** those fields needed DDB because the BRIDGE reads them (`create_slack_inbound.go:148/184` write `slack_react_always`/`slack_allow`; `pkg/aws/metadata.go:71/74` round-trip; bridge `FetchByChannel` reads). `maxConcurrentThreads` is consumed by the POLLER (sandbox-side), never the bridge.
**How to avoid:** add `MaxConcurrentThreads *int` ONLY to the schema + `NotifyEnv` emission (`userdata.go:5459–5523`, gated on `slackInboundEnabled(p)` or `slackEnabled(p)` like the neighbors). No DDB attr, no `SandboxMetadata` field, no `create_slack_inbound.go` write. **The Phase-117 `deepMerge` (`inherit.go:27`) handles the `*int` automatically** — it is a plain scalar in the YAML round-trip, so right-parent/child wins with no typed-merger code (contrast the de-dup logic lists need).
**Warning signs:** an over-built plan adds a DDB migration or a `SandboxMetadata` field for a poller-only knob.

### Pitfall 6: Poller bash bugs are invisible to Go goldens — live E2E is mandatory
**What goes wrong:** the counting-semaphore + heartbeat + ack-reversal logic lives entirely in a bash heredoc string. Go goldens only assert the bash TEXT is present/byte-stable — they cannot execute it. A semaphore off-by-one, a `wait -n` that serializes, or a heartbeat that redelivers is GREEN in `go test`.
**Why it happens:** `pkg/compiler` renders bash as data; the runtime behavior is untestable in Go.
**How to avoid:** the live synthetic-HMAC E2E (below) is the ONLY gate that exercises the actual concurrency. Memory `project_skill_bash_needs_live_uat` codifies this exact lesson (4 bugs caught only in live UAT in Phase 113).

## Code Examples

### The exact Layer-1 swap (verified positions)
```go
// pkg/slack/bridge/events_handler.go:470 (files path) — BEFORE:
//   h.SQS.Send(bgCtx, info.QueueURL, string(sqsBodyBytes), info.SandboxID, dedupID)
// AFTER:
//   h.SQS.Send(bgCtx, info.QueueURL, string(sqsBodyBytes), threadTS, dedupID)
//
// pkg/slack/bridge/events_handler.go:490 (no-files path) — BEFORE:
//   h.SQS.Send(ctx, info.QueueURL, string(bodyBytes), info.SandboxID, dedupID)
// AFTER:
//   h.SQS.Send(ctx, info.QueueURL, string(bodyBytes), threadTS, dedupID)
```

### Schema struct addition (mirror the Allow/ReactAlways pattern)
```go
// pkg/profile/types.go — append to NotificationSlackInboundSpec (after Allow at :258):
// MaxConcurrentThreads bounds how many distinct Slack threads this sandbox's
// inbound poller dispatches in PARALLEL (Phase 119). nil/absent = 1 (serial,
// byte-identical to Phase 118). Different threads run concurrently up to this
// cap; messages WITHIN a thread stay strictly serial+ordered (FIFO group =
// thread). Only meaningful with perSandbox=true + inbound.enabled=true; km
// validate WARNS otherwise. Drives KM_SLACK_MAX_CONCURRENCY (sandbox-side only;
// the bridge does not read it).
MaxConcurrentThreads *int `json:"maxConcurrentThreads,omitempty" yaml:"maxConcurrentThreads,omitempty"`
```

### JSON schema (mirror the inbound block at schema:748–773)
```json
"maxConcurrentThreads": {
  "type": "integer",
  "minimum": 1,
  "description": "Max distinct Slack threads dispatched in parallel by this sandbox's inbound poller (Phase 119). Default 1 (serial). Threads run concurrently up to this cap; messages within a thread stay serial+ordered. Only meaningful with perSandbox=true and inbound.enabled=true. Drives KM_SLACK_MAX_CONCURRENCY (sandbox-side)."
}
```
(`additionalProperties:false` is already set on the `inbound` object at `schema:750`; do not remove it.)

### Validation WARN (mirror validate.go:353–362 Phase-118 rule)
```go
// pkg/profile/validate.go — after the S-allow rule (~:362):
if slack.Inbound != nil && slack.Inbound.MaxConcurrentThreads != nil &&
    *slack.Inbound.MaxConcurrentThreads > 1 {
    if !perSandbox || slack.Inbound.Enabled == nil || !*slack.Inbound.Enabled {
        errs = append(errs, ValidationError{
            Path:      "spec.notification.slack.inbound.maxConcurrentThreads",
            Message:   "notification.slack.inbound.maxConcurrentThreads > 1 has no effect without notification.slack.perSandbox: true and notification.slack.inbound.enabled: true",
            IsWarning: true,
        })
    }
}
```

### NotifyEnv emission (mirror the KM_SLACK_REACT_ALWAYS block at userdata.go:5474–5483)
```go
// pkg/compiler/userdata.go — inside the slackEnabled(p) block (~:5468), or the
// slackInboundEnabled(p) block (~:5499) since the cap is inbound-only:
if sl := notifySlackInbound(p); sl != nil && sl.MaxConcurrentThreads != nil && *sl.MaxConcurrentThreads > 1 {
    notifyEnv["KM_SLACK_MAX_CONCURRENCY"] = strconv.Itoa(*sl.MaxConcurrentThreads)
}
// (Emitting only when >1 keeps the default-1 case byte-identical: no new env line.
//  Verify against the dormancy invariant — a default-1 profile must render
//  byte-identical userdata to Phase 118. If the planner prefers ALWAYS emitting
//  the key, the frozen golden must be hand-patched to include KM_SLACK_MAX_CONCURRENCY=1.)
```
**Dormancy decision for the planner:** emitting the env var ONLY when cap>1 means a default-1 (== every existing profile) renders byte-identical userdata → the frozen golden does NOT move for the ENV change (it still moves for the POLLER-BASH change). Emitting it ALWAYS (=1) moves the golden for both. Recommend **emit-only-when->1** to minimize golden churn, with the poller defaulting `KM_SLACK_MAX_CONCURRENCY:-1`.

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `MessageGroupId = info.SandboxID` (one FIFO group per box) | `MessageGroupId = threadTS` (one group per thread) | This phase | Parallel across threads, serial within |
| Serial `receive-message --max-number-of-messages 1` + blocking dispatch | Batched receive + `wait -n` bounded fan-out | This phase | wall-clock ≈ max(turn), not sum |
| Ack-first (delete before post) | Ack-after-completion + visibility heartbeat | This phase | per-thread FIFO ordering; reintroduces crash-dup window (guarded) |
| One-shot 300s visibility extension (`userdata.go:1702`) | Per-message heartbeat ticker | This phase | survives turns > base timeout |

**Deprecated/outdated:**
- The L2069 "Ack first" comment and its rationale become obsolete — the delete moves and the trade-off inverts (now we guard against dup instead of accepting lost-reply). Update the comment.

## Open Questions

1. **Base `VisibilityTimeout` value (Claude's discretion).**
   - What we know: current 30s (`sqs.go:127`); H1 uses 1800s (`sqs.go:77`); base raise only affects NEW queues.
   - What's unclear: whether to add a dedicated `slackInboundVisibilityTimeout` const or reuse `h1InboundVisibilityTimeout`.
   - Recommendation: raise to 1800 (own const, parallel to H1) AND ship the heartbeat. The heartbeat is mandatory anyway for already-provisioned 30s queues.

2. **Idempotency-guard mechanism (recommended option C).**
   - What we know: GitHub's helper-marker doesn't transplant (Pitfall 3); the thread row is already read/written per turn.
   - What's unclear: whether to key on `event_id` (SQS dedup id) or `RUN_ID`.
   - Recommendation: key on the inbound `event_ts`/`event_id` (redelivery reuses it); store `last_processed_event_ts` on the `km-slack-threads` row; skip post if already processed. Fail-open.

3. **`--max-number-of-messages` batch size (Claude's discretion).**
   - What we know: SQS max is 10; cap drives how many can run.
   - Recommendation: `min(cap, 10)`. Fetching more than cap just makes them wait in the semaphore (extra in-flight visibility load for no throughput gain).

## Validation Architecture

> nyquist_validation is `true` in `.planning/config.json` — this section is REQUIRED.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go standard `testing` (table-driven), in-repo |
| Config file | none (go test) |
| Quick run command | `go test ./pkg/slack/bridge/ ./pkg/profile/ -count=1` |
| Full suite command | `go test ./... -count=1 -timeout 600s` |

**Note:** capture the command's OWN exit code, not a piped `tail` (memory `feedback_check_go_test_exit_not_pipe`). `internal/app/cmd` tests may show `InvalidGrantException` on expired AWS SSO (environmental, not a regression — memory `project_cmd_suite_pre_existing_failures`).

### Phase Requirements → Test Map
| Req | Behavior | Test Type | Automated Command | File Exists? |
|-----|----------|-----------|-------------------|-------------|
| P119-A | Bridge sets `MessageGroupId == threadTS` (both paths); `== msg.TS` when no thread | unit | `go test ./pkg/slack/bridge/ -run TestEventsHandler -x` | ❌ Wave 0 (extend `events_handler_test.go`; `fakeSQS.Send` at `:165` already captures `group`) |
| P119-C / G | Schema accepts `maxConcurrentThreads`; default 1; `minimum:1` rejects 0/neg; `additionalProperties:false`; validate WARN cap>1 w/o perSandbox+inbound | unit | `go test ./pkg/profile/ -run TestValidate -x` | ❌ Wave 0 (extend `validate_slack_inbound_test.go`) |
| P119-C | `KM_SLACK_MAX_CONCURRENCY` written to `notify.env`/`profile.d` when cap>1 | unit (substring) + golden | `go test ./pkg/compiler/ -run TestUserData -x` | ❌ Wave 0 (new substring test) + hand-patch frozen golden |
| P119-E | `inboundQueueAttrs` carries the chosen base `VisibilityTimeout` | unit | `go test ./pkg/aws/ -run TestInboundQueueAttrs -x` | ❌ Wave 0 |
| P119-B/D/E/F | parallelism / ordering / cap / heartbeat / dedup (bash runtime) | live E2E | synthetic HMAC `/events` POST (below) | ❌ Wave 0 (helper script) |
| dormancy | default-1 profile renders byte-identical userdata to Phase 118 | golden | `go test ./pkg/compiler/ -run TestUserdataLearnV2Phase92ByteIdentity -x` | ✅ (`userdata_phase92_byte_identity_test.go:108`) — must stay green via hand-patch |

### Testable seams (Go/unit)
- **Bridge MessageGroupId:** `events_handler_test.go` already has `fakeSQS.Send(ctx,url,body,group,dedup)` (`:165`) capturing `group`. Add cases asserting `group == threadTS` for files + no-files paths, and `group == msg.TS` for a top-level (no `thread_ts`) message.
- **Schema/validation:** `validate_slack_inbound_test.go` already exercises perSandbox WARN rules — add the cap>1 WARN case + a `minimum:1` schema-reject case.
- **Userdata env:** new substring assertion that `KM_SLACK_MAX_CONCURRENCY=N` appears in the rendered `notify.env` block when cap>1, and ABSENT when cap=1 (dormancy).
- **Queue attrs:** assert the new base `VisibilityTimeout` in `inboundQueueAttrs` (`sqs.go:123`).

### Live-E2E assertions (synthetic HMAC `/events` POST — no humans)
Reuse the Phase-114 self-drive technique (memory `project_slack_bridge_inbound_e2e_and_status_attr`, `/tmp/km114_send_event.sh`):
- **Signing:** secret = SSM `/km/slack/signing-secret` (`--with-decryption`); base string `v0:{ts}:{body}`; header `x-slack-signature: v0=<openssl dgst -sha256 -hmac "$SECRET">`; `x-slack-request-timestamp` within 300s. POST to `{bridge-url}/events`.
- **Bot-loop bypass (confirmed safe):** `isBotLoop` (`events_handler.go:632`) drops only when `BotID != ""`, the subtype is not in `{"", "thread_broadcast", "file_share"}`, `User == ""`, or `User == bot_user_id`. So a synthetic body with `bot_id` ABSENT, empty `subtype`, and `user = U0HUMANTEST01` (a non-bot id) PASSES. For mention-only channels, include `<@{bot-user-id}>` (SSM `/km/slack/bot-user-id`) in `text`.
- **N distinct threads:** craft N `event_callback` bodies with distinct `thread_ts` (or distinct top-level `ts`) targeting the SAME sandbox's channel, each with a prompt that reliably takes ~30–60s ("count slowly to 30 then summarize").
- **Observe concurrency:** `km agent list <sb>` (overlapping run timestamps), `km otel <sb> --timeline`, `km logs <sb> --follow` (poller journal: "Turn complete" interleaving), `/workspace/.km-agent/runs/*/output.json` (distinct RUN_DIRs — verify the Pitfall-1 suffix made them unique).

**Assertions:**
1. **Parallelism:** fire A/B/C simultaneously → 3 overlapping runs; wall-clock ≈ max(turn), not sum.
2. **Per-thread ordering:** 2 back-to-back in thread A → 2nd starts only after 1st completes; replies in order.
3. **Cap enforcement:** 5 threads, cap=3 → never >3 concurrent runs (count overlapping RUN_DIRs / `km agent list`).
4. **Heartbeat / no-dup:** one turn longer than the base visibility timeout → exactly ONE reply (no redelivery).
5. **Dormant regression:** cap=1 sandbox → serial, identical to Phase 118.
6. **Dedup:** replay the SAME `event_id` → single-processed (existing nonce dedup at `events_handler.go:392` + the new poller idempotency guard).

**E2E caveat (harness artifact):** a fabricated `ts` is not a real Slack message, so the 👀 reaction + threaded reply may fail with `message_not_found`/`thread_not_found` — harmless. Verify via side effects (run dirs, DDB row, channel history via `conversations.history`/`conversations.replies`), not the reaction.

### Sampling Rate
- **Per task commit:** `go test ./pkg/slack/bridge/ ./pkg/profile/ ./pkg/compiler/ ./pkg/aws/ -count=1`
- **Per wave merge:** `go test ./... -count=1 -timeout 600s`
- **Phase gate:** full suite green + the live synthetic-HMAC E2E (assertions 1–6) before `/gsd:verify-work`. The live E2E is NON-OPTIONAL — the concurrency logic is bash, invisible to Go goldens (Pitfall 6).

### Wave 0 Gaps
- [ ] `pkg/slack/bridge/events_handler_test.go` — add MessageGroupId==threadTS cases (P119-A)
- [ ] `pkg/profile/validate_slack_inbound_test.go` — add cap>1 WARN + minimum:1 reject (P119-C/G)
- [ ] `pkg/compiler/userdata_test.go` — add `KM_SLACK_MAX_CONCURRENCY` substring test (P119-C)
- [ ] `pkg/aws/sqs_test.go` (or existing) — assert new base VisibilityTimeout (P119-E)
- [ ] Hand-patch `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` for the poller-bash change (Pitfall 4) — NEVER re-capture
- [ ] E2E helper script (Phase-114-style `/tmp/km119_send_event.sh`) — multi-thread variant
- [ ] No framework install needed (Go testing in place)

## Deploy Surface (from CLAUDE.md discipline)

- **Bridge `MessageGroupId` change** → `make build-lambdas` + `km init --slack` (env+IAM apply also re-uploads the freshly-built zip — verify code SHA via `aws lambda get-function`) or full `km init --dry-run=false`. Effective on the next webhook.
- **Profile field + poller userdata** → `make build-lambdas` + `km init --dry-run=false` (the create-handler zip renders userdata; NOT `--sidecars`, which does not rebuild that zip). Existing sandboxes need `km destroy && km create`.
- **Queue `VisibilityTimeout`** (if raised in `sqs.go`) → applies to NEWLY created per-sandbox queues only; existing sandboxes' queues stay at 30s until recreate (⇒ heartbeat is mandatory regardless).
- **`make build`** the km binary if any operator-side path changes (validate is operator-side).
- **No `apiVersion` bump** (additive optional field).

## Sources

### Primary (HIGH confidence — verified in-repo at file:line)
- `pkg/slack/bridge/events_handler.go` — Send sites (:470, :490), threadTS computation (:402–406), isBotLoop (:632), dedup (:392)
- `pkg/slack/bridge/aws_adapters.go` — SQSAdapter.Send signature (:835), stale doc comment (:827)
- `pkg/slack/bridge/events_interfaces.go` — SQSSender interface (:73)
- `pkg/aws/sqs.go` — inboundQueueAttrs/VisibilityTimeout (:123–127), h1InboundVisibilityTimeout (:77)
- `pkg/compiler/userdata.go` — poller loop (:1670–2122), one-shot visibility (:1702), dispatch (:1955/:1992), ack/delete (:2072), km-slack post (:2100), NotifyEnv populate (:5459–5523) + render (:1096–1121), GitHub KM_GITHUB_TURN_ID injection (:2379–2440)
- `pkg/profile/types.go` — NotificationSlackInboundSpec (:238–258)
- `pkg/profile/validate.go` — Phase-118 WARN rules (:343–362)
- `pkg/profile/schemas/sandbox_profile.schema.json` — inbound block (:748–773)
- `pkg/profile/inherit.go` — deepMerge (:27), resolve (:106)
- `pkg/github/marker.go` — Phase-108 idempotency precedent (full file)
- `pkg/compiler/userdata_phase92_byte_identity_test.go` — byte-identity test + capture trap (:108, :118)
- `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` — contains poller (27 refs, confirmed)
- `profiles/learn.v2.yaml` — slack.inbound.enabled:true (:140–141)
- `internal/app/cmd/create_slack_inbound.go` — DDB-attr write pattern (:148, :184) [contrast — NOT needed for the cap]
- `pkg/aws/metadata.go` — SandboxMetadata DDB round-trip (:71, :74) [contrast — NOT needed]

### Secondary (MEDIUM confidence — project memory, cross-verified against code)
- `project_slack_bridge_inbound_e2e_and_status_attr` — Phase-114 synthetic-HMAC self-drive technique, signing recipe, bot-loop bypass
- `project_frozen_byte_identity_golden_capture_trap` — hand-patch vs re-capture
- `project_skill_bash_needs_live_uat` — poller bash invisible to Go goldens
- `feedback_check_go_test_exit_not_pipe`, `project_cmd_suite_pre_existing_failures`

### Tertiary (LOW confidence — needs no validation here)
- AMI bash versions (AL2023 5.2, Ubuntu 22.04 5.1, 24.04 5.2): distro defaults, corroborated by the poller's existing bash-4+ `${var,,}` usage at `userdata.go:1732`. All ≥ 4.3 ⇒ `wait -n` safe.

## Metadata

**Confidence breakdown:**
- Layer-1 bridge swap: HIGH — exact call sites + threadTS guarantee verified.
- Schema/validation/NotifyEnv plumbing: HIGH — direct mirror of Phase-118 `allow` and Phase-91.5 `reactAlways`, lines confirmed.
- Bash semaphore / `wait -n` availability: HIGH — distro facts + in-repo bash-4 usage proof.
- Visibility heartbeat: HIGH — mechanics standard; H1 precedent at sqs.go:77.
- Idempotency guard (option C): MEDIUM — recommended design is sound and cheaper than GitHub's, but the exact DDB key (`event_ts` vs `RUN_ID`) is a planner decision; no existing Slack-side idempotency code to copy verbatim.
- Golden surface: HIGH — confirmed the frozen baseline contains the poller (27 refs) and the other goldens do not (0 refs).

**Research date:** 2026-06-24
**Valid until:** 2026-07-24 (stable internal surface; the only external dependency is SQS FIFO semantics, which are GA and unchanging)
