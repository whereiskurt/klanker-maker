# Slack Transcript Streaming Design

**Date:** 2026-05-03
**Status:** Draft — pending implementation plan
**Predecessors:** Phase 62 (operator-notify hook), Phase 63 (Slack notify hook), Phase 67 (Slack inbound bidirectional chat)
**Successor (separate spec, future):** Reaction-triggered session fork

## Summary

Make a Slack-connected sandbox a faithful real-time view of its Claude session. Every assistant turn streams to a Slack thread as it happens, and the full session transcript (gzipped JSONL) lands as a downloadable file in the same thread when the run ends. Replaces the current Phase 63 behavior of "one truncated `tail -1` message at idle" for sandboxes that opt in.

This spec covers Phase A only. Phase B (reaction-triggered session fork) is intentionally deferred but is enabled by a stream-message → transcript-position mapping table that Phase A provisions and writes to.

## Motivation

Operators running long-form autonomous Claude sessions in a sandbox (e.g. "go execute this GSD task overnight") want progress visibility in Slack and a durable artifact at the end. Current Phase 63 produces a single-line idle ping containing only the last assistant text block — useless for monitoring multi-step runs and useless for auditing what happened.

Three concrete use cases:

1. **Long-running GSD task with progress monitoring.** Operator dispatches a multi-hour run via `km agent run`; wants to glance at Slack periodically and see what's happening without SSH'ing into the sandbox.
2. **Postmortem inspection.** After a run completes (success or failure), operator wants the full transcript JSONL accessible without invoking `km agent results` or SSH.
3. **Phase B prerequisite.** Reaction-triggered session fork (a future phase) requires a stream-message → transcript-position addressing scheme. This spec lays the foundation by writing to a mapping table during streaming, even though no consumer exists yet.

## In Scope

- Per-turn streaming of assistant text + tool-call one-liners (`🔧 Edit /path/to/file`) to a per-sandbox Slack channel thread
- Final upload of full gzipped transcript JSONL via S3 → bridge → Slack files API at end of Claude response
- Auto-thread-parent creation for operator-initiated runs (when no `KM_SLACK_THREAD_TS` is supplied)
- Single profile flag `notifySlackTranscriptEnabled` + per-invocation CLI override flags on `km agent run` and `km shell`
- New bridge action (`upload`) + new Slack scope (`files:write`) + new envelope fields carrying an S3 reference
- New DDB table `km-slack-stream-messages` mapping `(channel_id, slack_ts) → (sandbox_id, session_id, transcript_offset)`, written by every stream post (Phase B integration seam, no consumer in this phase)
- Three new `km doctor` checks
- Operator warning at `km create` time when transcript streaming is enabled

## Out of Scope (explicit YAGNI)

- Reaction event subscription, transcript surgery, new-session minting, fork-aware poller routing — all deferred to Phase B
- Mid-run threshold-triggered uploads
- Reaction-triggered on-demand uploads
- Full tool I/O in streamed messages (we ship text + one-liner; the file has the full record)
- Stream-without-upload or upload-without-stream toggles (one flag controls both behaviors)
- Streaming on the shared channel (rejected — per-sandbox channel required)
- Replacing the existing Phase 63 idle-ping behavior; this is *additional*, gated on opt-in
- Transcript redaction / secret-stripping (separate concern, separate phase if it materializes)

## Non-Goals

- This is not a logging/observability replacement for OTEL. `km otel` remains the canonical path for cost/event analytics; OTEL spans for the new code are observability *for* the streaming subsystem itself.
- This is not bidirectional. Slack reactions/replies do not drive any new sandbox behavior beyond what Phase 67 already provides.
- This is not a replacement for `km agent results` / `km agent list` — those remain authoritative for programmatic access.

## Architecture

### Components: reused vs. modified vs. new

**Reused unchanged:**

- `pkg/slack` envelope, canonical JSON, Ed25519 sign/verify primitives
- `KM_ARTIFACTS_BUCKET` S3 bucket (gains a `transcripts/` prefix; existing lifecycle applies)
- `~/.claude/settings.json` hook merger at `pkg/compiler/userdata.go:2522` (gains a `PostToolUse` registration alongside existing `Notification` and `Stop`)

**Reused, modified:**

- `km-slack` (sandbox binary, `cmd/km-slack/`) — gains `upload` and `record-mapping` subcommands alongside existing `post`
- `km-slack-bridge` Lambda (`cmd/km-slack-bridge/`) — gains `ActionUpload` handler implementing Slack's 3-step file upload
- `km-notify-hook` (`pkg/compiler/userdata.go:399-491`) — gains a `PostToolUse` event branch; `Stop` event branch gains transcript-upload logic
- `pkg/slack/payload.go` — `SlackEnvelope` gains `S3Key`, `Filename`, `ContentType`, `SizeBytes` fields; adds `ActionUpload` constant

**New:**

- DDB table `{prefix}-km-slack-stream-messages` — `(channel_id PK, slack_ts SK) → {sandbox_id, session_id, transcript_offset, ttl_expiry}`, on-demand billing, TTL on `ttl_expiry` (30 days from write)
- Slack bot scope: `files:write` (one-time re-auth)
- Profile field: `spec.cli.notifySlackTranscriptEnabled` (bool, default `false`)
- CLI flags: `--transcript-stream` / `--no-transcript-stream` on `km agent run` and `km shell`
- Sandbox env vars: `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED` (0/1), `KM_SLACK_STREAM_TABLE` (DDB table name)
- Three `km doctor` checks: `slack_transcript_table_exists`, `slack_files_write_scope`, `slack_transcript_stale_objects`

### Data flow — streaming path (PostToolUse, fires N times per response)

**Identifier convention:** `{sid}` below denotes Claude Code's `session_id` field from the hook payload's stdin JSON. This is unique per Claude session and persists across all `PostToolUse` and `Stop` fires within one response, so per-`{sid}` `/tmp/` files don't collide and naturally clean up at session end. (`KM_AGENT_RUN_ID` is *not* used because `km shell` interactive sessions don't set it.)

```
Claude finishes a tool call
  └─ ~/.claude/settings.json fires hooks.PostToolUse
       └─ /opt/km/bin/km-notify-hook PostToolUse
            ├─ Gate check: KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED == 1 ?
            ├─ Read stdin JSON → extract transcript_path, session_id ({sid})
            ├─ Read /tmp/km-slack-stream.{sid}.offset (last byte position)
            ├─ Tail transcript JSONL from offset; collect new entries:
            │     • assistant.message.content[].text  → chat body lines
            │     • tool_use entries                  → "🔧 {name}: {one-line-input}"
            ├─ Resolve thread:
            │     • If KM_SLACK_THREAD_TS set         → use it
            │     • Else if /tmp/km-slack-thread.{sid} exists → read it
            │     • Else → post parent "🤖 [sb-id] turn started — {prompt[:80]}"
            │             → cache returned ts to /tmp/km-slack-thread.{sid}
            ├─ km-slack post --channel $KM_SLACK_CHANNEL_ID --thread <ts> --body <file>
            ├─ On 200 OK with returned ts:
            │     km-slack record-mapping --channel C --slack-ts T --offset N --session {sid}
            │     (uses sandbox IAM PutItem on km-slack-stream-messages)
            └─ Update /tmp/km-slack-stream.{sid}.offset
```

### Data flow — upload path (Stop, fires once per response)

```
Claude finishes responding
  └─ ~/.claude/settings.json fires hooks.Stop
       └─ /opt/km/bin/km-notify-hook Stop
            ├─ Gate check (same)
            ├─ Run the streaming logic above for any final assistant text not yet streamed
            ├─ gzip $transcript_path → /tmp/transcript.{sid}.jsonl.gz
            ├─ aws s3 cp ... s3://$KM_ARTIFACTS_BUCKET/transcripts/{sandbox_id}/{sid}.jsonl.gz
            ├─ km-slack upload \
            │     --channel $KM_SLACK_CHANNEL_ID \
            │     --thread <cached-ts> \
            │     --s3-key transcripts/{sandbox_id}/{sid}.jsonl.gz \
            │     --filename "claude-transcript-{sid}.jsonl.gz" \
            │     --content-type application/gzip \
            │     --size-bytes <bytes>
            │     │
            │     └─ Sign envelope (ActionUpload) → POST bridge URL
            │          └─ Bridge: GetObject (streamed) → files.getUploadURLExternal →
            │                    PUT bytes → files.completeUploadExternal(thread_ts)
            └─ Cleanup: rm /tmp/km-slack-thread.{sid}, /tmp/km-slack-stream.{sid}.offset
```

### Trust boundary (unchanged from Phase 63/67)

- Sandbox holds Ed25519 private key (SSM, KMS-encrypted, sandbox IAM only)
- Bridge holds Slack bot token (SSM, KMS-encrypted, bridge Lambda IAM only)
- Sandbox never sees bot token; bridge never sees private key
- New `upload` action rides the same sign/verify path as existing `post` action

## User-Visible API

### Profile field (`spec.cli`)

| Field | Type | Default | Effect |
|---|---|---|---|
| `notifySlackTranscriptEnabled` | bool | `false` | Stream per-turn assistant text + tool one-liners to thread; upload gzipped transcript to thread at Stop |

**Validation rules** (added to `pkg/profile/validation.go`):

- Requires `notifySlackEnabled: true` AND `notifySlackPerSandbox: true` — fail validation otherwise with: `notifySlackTranscriptEnabled requires notifySlackEnabled and notifySlackPerSandbox`
- Incompatible with `notifySlackChannelOverride` — same rationale as Phase 67's inbound flag (transcript audience must be operator-controlled, not pinned to a foreign channel)

### CLI overrides (per-invocation)

```bash
km agent run <sandbox> --prompt "..." --transcript-stream      # force enable
km agent run <sandbox> --prompt "..." --no-transcript-stream   # force disable
km shell <sandbox> --transcript-stream
km shell <sandbox> --no-transcript-stream
```

Implementation mirrors the existing `--notify-on-permission` env-var injection mechanism (`internal/app/cmd/agent.go` and `internal/app/cmd/shell.go`). Sets `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=1` or `=0` in the SSM session env, taking precedence over the profile default.

### Sandbox env vars (set by `km create`, sourced from `/etc/profile.d/km-notify-env.sh`)

| Variable | Source | Purpose |
|---|---|---|
| `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED` | profile `spec.cli.notifySlackTranscriptEnabled` (omit ⇒ `0`) | Master gate read by `km-notify-hook` |
| `KM_SLACK_STREAM_TABLE` | runtime, injected by `km create` | DDB table name for stream→transcript mapping |
| `KM_ARTIFACTS_BUCKET` | already exists | Upload destination for transcripts |
| `KM_SLACK_CHANNEL_ID` | already exists | Target channel for stream + upload |
| `KM_SLACK_BRIDGE_URL` | already exists | Bridge endpoint |
| `KM_SLACK_THREAD_TS` | already exists | Phase 67 inbound thread parent (when applicable) |

No new secrets, no new SSM params at the sandbox level.

### `km init --sidecars` requirement

Per the existing convention from Phases 63 and 67, this phase requires `km init --sidecars` after deploy:

- New `km-slack` binary (gains `upload` and `record-mapping` subcommands)
- New bridge Lambda zip (gains `ActionUpload` handler)
- New profile schema (gains `notifySlackTranscriptEnabled`)
- New DDB table provisioned via Terraform (`infra/modules/dynamodb/`)

Existing sandboxes do NOT get the new behavior retroactively — `km destroy` + `km create` to pick up the schema field and updated user-data.

### `km doctor` additions

Three new checks:

- `slack_transcript_table_exists` — DDB `km-slack-stream-messages` exists and is reachable
- `slack_files_write_scope` — bot has `files:write` (probes `auth.test` response → scopes list)
- `slack_transcript_stale_objects` — S3 prefix `transcripts/{sandbox_id}/` for sandboxes that no longer exist (cleanup advisory)

## Bridge Changes & Slack Upload Flow

### Envelope schema extension (`pkg/slack/payload.go`)

```go
const (
    ActionPost    = "post"
    ActionArchive = "archive"
    ActionTest    = "test"
    ActionUpload  = "upload"  // NEW
)

type SlackEnvelope struct {
    Action      string `json:"action"`
    Body        string `json:"body"`
    Channel     string `json:"channel"`
    Nonce       string `json:"nonce"`
    SenderID    string `json:"sender_id"`
    Subject     string `json:"subject"`
    ThreadTS    string `json:"thread_ts"`
    Timestamp   int64  `json:"timestamp"`
    Version     int    `json:"version"`
    // NEW — populated only when Action == ActionUpload; empty/zero otherwise.
    S3Key       string `json:"s3_key"`
    Filename    string `json:"filename"`
    ContentType string `json:"content_type"`
    SizeBytes   int64  `json:"size_bytes"`
}
```

**Backwards compatibility:** keeping one struct preserves canonical JSON ordering (alphabetical struct tags), which the existing sign/verify primitives in `pkg/slack/payload.go:CanonicalJSON` depend on. Existing post envelopes serialize identically — the new fields are zero-valued and serialize as `""` / `0`, which the bridge's existing post handler ignores. `EnvelopeVersion` stays at `1`.

### Bridge handler additions (`pkg/slack/bridge/`)

New action route in the existing dispatcher:

```
ActionPost     → existing PostMessage path                    (unchanged)
ActionArchive  → existing ArchiveChannel path                 (unchanged)
ActionTest     → existing test path                           (unchanged)
ActionUpload   → NEW: upload handler
```

**Validation on `ActionUpload` (before any AWS work):**

- `S3Key` non-empty, must match prefix pattern `transcripts/{sandbox_id}/...` where `{sandbox_id}` equals envelope `SenderID` — prevents one sandbox uploading another's S3 objects via crafted envelope
- `Filename` non-empty, sanitized (no `/`, no NUL, ≤ 255 bytes)
- `ContentType` allow-listed: `application/gzip`, `application/json`, `text/plain`
- `SizeBytes > 0` and `SizeBytes ≤ 100 MB` (configurable cap; safety bound below Slack's 1 GB ceiling)
- `Channel` non-empty
- `ThreadTS` may be empty (uploads to channel root, but in practice always populated by hook)

### Slack 3-step upload flow

`files.upload` is deprecated. The replacement is a 3-step flow:

```
1. files.getUploadURLExternal
   ┌─ POST /api/files.getUploadURLExternal
   │   { filename, length: SizeBytes }
   └─ ← { upload_url, file_id }

2. PUT bytes
   ┌─ Bridge GetObject from S3 (streamed, no full buffer in Lambda memory)
   │  → PUT to upload_url with Content-Type from envelope
   └─ ← 200 OK

3. files.completeUploadExternal
   ┌─ POST /api/files.completeUploadExternal
   │   { files: [{id: file_id, title: filename}],
   │     channel_id: Channel,
   │     thread_ts: ThreadTS }
   └─ ← { ok: true, files: [{id, permalink, ...}] }
```

**Implementation in `pkg/slack/client.go`:**

- New method `UploadFile(ctx, channel, threadTS, filename, contentType string, sizeBytes int64, body io.Reader) (*UploadResponse, error)`
- Streams `body` into the PUT request — no full buffering. (S3 GetObject returns `io.ReadCloser`; pass through.)
- Returns the file's `permalink` so the bridge response carries it back to the sandbox for stderr log lines.

**Lambda memory & timeout bounds:**

- Streaming means peak memory stays at Go default (~30 MB) regardless of file size
- Default Lambda timeout is sufficient for files ≤ 100 MB on a warm container; worst-case ~60s end-to-end
- Cold-start adds ~3s; acceptable

### IAM additions

**Bridge Lambda role gains:**

- `s3:GetObject` on `${KM_ARTIFACTS_BUCKET}/transcripts/*`
- `s3:HeadObject` on the same (for size validation against envelope `SizeBytes` — defense in depth)

**Sandbox EC2 instance role gains:**

- `s3:PutObject` on `${KM_ARTIFACTS_BUCKET}/transcripts/{sandbox_id}/*` — scoped via existing per-sandbox IAM template that already grants `s3:PutObject` on the sandbox's email-attachment prefix; transcripts prefix added alongside
- `dynamodb:PutItem` on `arn:aws:dynamodb:*:*:table/{prefix}-km-slack-stream-messages` — required for the `record-mapping` subcommand of `km-slack`

The bot token is already in SSM; no new secret, no new SSM read.

### Slack App scope addition

One-time operator action: re-authorize the Slack App with `files:write` scope at the Slack App admin page. (Adding a scope does not require token rotation if you keep the existing token.) Captured by:

- A new doctor check (`slack_files_write_scope`)
- An early validation: bridge calls `auth.test` on cold start; if `files:write` is missing AND any envelope arrives with `ActionUpload`, returns 400 with a clear error: `bot lacks files:write — operator must re-auth Slack App`. Sandbox treats as non-fatal; logs to stderr; hook exits 0.

### What the sandbox does NOT do

- Does NOT upload directly to Slack — it has no bot token. Always indirects through bridge.
- Does NOT generate Slack-side presigned URLs — the bridge calls `files.getUploadURLExternal` itself.
- Does NOT delete the S3 object after upload — bucket lifecycle handles cleanup. (Same convention as email attachments.)

### Backwards compat & rollout

- Existing sandboxes with the old `km-slack` binary (no `upload` subcommand) are unaffected — they won't try to upload.
- Existing bridge Lambda without `ActionUpload` returns `400 unknown action`; sandbox-side `km-slack upload` treats that as a non-fatal warning logged to stderr (same exit-0 contract as the parent hook).
- Operator deploy order via `km init --sidecars`: new bridge first (gains action), then sidecar binary upload (sandboxes start sending it). Reverse order would produce 400s during the gap, but the hook always exits 0, so user-visible impact = transcript file missing, no failures.

## Error Handling, Security, Observability, Testing

### Error handling philosophy (unchanged from Phase 62/63)

`km-notify-hook` always exits 0. Every failure path along stream + upload is silent-but-logged-to-stderr. Claude is never blocked by Slack issues.

| Failure | Sandbox behavior | User-visible result |
|---|---|---|
| Bridge unreachable (network) | `km-slack post/upload` retries per existing `BridgeBackoff` (1s/2s/4s); on full failure, log to stderr, hook exits 0 | Slack thread missing some/all messages; transcript still in S3 |
| Bridge returns 4xx | No retry (existing semantic), log, exit 0 | Same as above |
| Bridge returns 5xx | No retry (existing semantic; rationale: `pkg/slack/client.go:174`) | Same as above |
| `files:write` scope missing | Bridge returns 400 `scope_missing`; hook stderr `WARN: operator must re-auth Slack App with files:write` | Stream works, upload silently absent |
| S3 upload fails (sandbox-side) | Skip the `km-slack upload` call entirely; log; exit 0 | Stream complete, file missing |
| Transcript file missing/corrupt at Stop | Skip upload, log; streaming already happened | Stream complete, file missing |
| DDB `PutItem` for stream-mapping fails | Log, continue (stream message already posted) | Slack message exists; fork-resolution would fail for that ts (Phase B problem); recovery: next streamed message succeeds |
| Hook timeout (long tail/jq) | Hook wrapped with `timeout 10s`; on timeout, exit 0 | Streaming may skip a turn; transcript still uploaded at Stop |

### Security & privacy guarantees

**1. Audience containment.**

- Profile validation rejects `notifySlackTranscriptEnabled: true` without `notifySlackPerSandbox: true`. Transcripts cannot land in the shared `#sandboxes-all` channel.
- Profile validation rejects combination with `notifySlackChannelOverride`. Transcripts cannot land in an externally-pinned channel.

**2. Cross-sandbox isolation on uploads.**

- Bridge enforces `S3Key` prefix matches `transcripts/{envelope.sender_id}/` before `GetObject`. A compromised sandbox cannot upload another sandbox's transcript via crafted envelope.

**3. Trust boundary.**

Unchanged from Phase 63/67 (sandbox: signing key only; bridge: bot token only).

**4. Operator awareness.**

`km create` prints a one-line warning when `notifySlackTranscriptEnabled: true`:

> ⚠ Slack transcript streaming enabled — full Claude transcripts (including tool I/O) will be posted to channel `<id>`. Audience: `<member-count>` Slack users.

Logged to operator stderr. Requires no acknowledgement (so it doesn't break automation) but visible in CI/CLI output.

**5. Slack Connect external members.**

`km doctor` already checks per-sandbox channel membership (Phase 67). The transcript phase reuses that check; no new doctor logic needed for external-member detection. The operator warning above includes member count via `conversations.info.num_members`.

**6. Secret leakage in transcripts (call out, not solved).**

Transcripts contain whatever Claude saw: `Bash` outputs, `Read` contents, env dumps, API responses. By design, this is what the operator opted into. Out of scope: transcript redaction. Documented loudly in `docs/slack-notifications.md`.

### Observability

**Sandbox-side:**

- All hook stderr lands in journald (existing convention). Tags: `[km-notify-hook]`, `[km-slack-stream]`, `[km-slack-upload]`.
- New OTEL spans (gated by existing `KM_OTEL_ENABLED`):
  - `km.notify_hook.stream` (per `PostToolUse` fire) — attributes: `event.type=PostToolUse`, `messages.posted`, `bytes.streamed`
  - `km.notify_hook.upload` (per `Stop` fire when upload runs) — attributes: `transcript.bytes_compressed`, `transcript.bytes_raw`, `s3.put.duration_ms`, `bridge.duration_ms`

**Bridge-side:**

- Existing CloudWatch Logs gain new structured lines: `action=upload sender=sb-X channel=C... s3_key=... size=... duration_ms=...`
- New CloudWatch metrics (namespace already exists for the bridge):
  - `BridgeUploadCount` (count, dimensions: `Action=upload`, `Result=success|failure`)
  - `BridgeUploadBytes` (sum, dimensions: `Action=upload`)
  - `BridgeUploadDurationMillis` (avg, p50, p95, p99)

**Operator-side:**

- `km otel <sandbox>` already aggregates spans. New spans appear automatically.

### Testing strategy

**Unit:**

- `pkg/slack/payload_test.go` — extend with `ActionUpload` envelope canonical-JSON tests; verify backwards-compat (post envelopes serialize unchanged with the additive fields).
- `pkg/slack/client_test.go` — table-driven tests for `UploadFile` covering 3-step flow, mid-step failures, stream pass-through correctness.
- `pkg/slack/bridge/` — handler tests for `ActionUpload`: prefix validation, size cap, content-type allow-list, scope-missing path.
- `pkg/compiler/notify_hook_script_test.go` — extend with `PostToolUse` fixture covering: gate-off, gate-on with no thread (auto-parent), gate-on with thread, multi-tool-call response, thread-cache cleanup, offset-tracking correctness across multiple `PostToolUse` fires.

**Integration (sandbox-side, on a real EC2):**

- `make build && km init --sidecars && km create profiles/test-transcript.yaml`
- UAT scenario "long autonomous run": `km agent run sb-X --prompt "audit and fix the failing tests in this repo"`. Verify:
  - `PostToolUse` messages stream into the per-sandbox channel
  - DDB rows appear in `km-slack-stream-messages` keyed by `(channel_id, slack_ts)`
  - `Stop` posts `claude-transcript-{session_id}.jsonl.gz` as a file in the same thread
  - File is openable, valid gzip, parses as JSONL

**End-to-end:**

- Re-run Phase 67's existing UAT scenarios with `notifySlackTranscriptEnabled: true` to verify Phase 67 inbound still works (regression).
- Re-run Phase 63's existing UAT scenarios with `notifySlackTranscriptEnabled: false` to verify no behavior change for sandboxes that don't opt in (regression).

**Failure-injection:**

- Smoke test bridge with `files:write` scope removed → confirm 400 path and graceful sandbox handling.
- Smoke test S3 prefix mismatch → confirm bridge rejects.
- Smoke test 100 MB transcript → confirm Lambda memory and timeout headroom.

## Implementation Roadmap (informational; GSD planning will refine)

Likely decomposition into roughly these work units, in dependency order. The actual GSD phase plan will refine sequencing.

1. **Schema & infra** — profile field, validation, Terraform DDB table, IAM additions
2. **Envelope extension** — `pkg/slack/payload.go` additive fields, canonical JSON tests
3. **Bridge upload action** — `ActionUpload` handler, `UploadFile` client method, 3-step flow, scope check
4. **Sandbox `km-slack` extensions** — `upload` and `record-mapping` subcommands
5. **Hook script changes** — `PostToolUse` branch, transcript tailing with offset tracking, auto-thread-parent, `Stop` upload logic
6. **CLI surface** — `--transcript-stream` / `--no-transcript-stream` on `km agent run` and `km shell`
7. **`km doctor` checks** — three new validators
8. **Operator warning at `km create`** — channel member count + warning line
9. **Documentation** — update `docs/slack-notifications.md` with the new flag, security notes, and usage examples
10. **UAT + regression** — long-run scenario, Phase 67 regression, Phase 63 regression, failure-injection

## Open Questions (none blocking — for implementation discovery)

- Exact format of the `🔧 {tool_name}: {one-line-input}` rendering for tools whose input is structurally complex (e.g. `Edit` with multi-line `new_string`). Likely: tool-specific renderers in a small map keyed by tool name, with a generic fallback that prints first 80 chars of `jq -c .`.
- Lambda provisioned concurrency status — verify whether the bridge already runs warm; if not, factor cold-start into the worst-case upload latency budget.
- DDB table name prefix convention — confirm existing pattern (`{prefix}-km-slack-threads`) and reuse identically for `{prefix}-km-slack-stream-messages`.

## Phase B Preview (not part of this spec)

The follow-up phase, "Reaction-triggered session fork," consumes the `km-slack-stream-messages` table provisioned here. High-level shape (subject to its own brainstorming):

- New Slack scope: `reactions:read`
- New bridge `/events` route handling `reaction_added`
- New SQS event-type for fork dispatch
- Sandbox-side transcript surgery (copy JSONL up to fork offset, mint new Claude session-id, register in `km-slack-threads`)
- Phase 67 poller extension to route by `(channel_id, thread_ts)` rather than `channel_id` alone (verify whether already so)
- New thread parent in same channel: `🍴 forked from <message-link>`
- Trigger emoji likely `🍴`; configurable via SSM parameter

By the time Phase B begins, every streamed message will already have a fork-resolvable `(channel_id, slack_ts) → (sandbox_id, session_id, transcript_offset)` row, so Phase B is purely additive.
