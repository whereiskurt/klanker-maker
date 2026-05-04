# Phase 68: Slack transcript streaming — per-turn chat + gzipped JSONL upload (Phase A) - Context

**Gathered:** 2026-05-03
**Status:** Ready for planning
**Source:** PRD Express Path (`docs/superpowers/specs/2026-05-03-slack-transcript-streaming-design.md`)

<domain>
## Phase Boundary

This phase makes a Slack-connected sandbox a faithful real-time view of its Claude session. Two coordinated behaviors:

1. **Per-turn streaming.** Every time Claude completes a tool call (`PostToolUse` hook), the new assistant text + tool one-liners since the last fire are posted to the Slack thread. Hash-deduped via on-disk offset tracking.
2. **Final transcript upload.** When Claude finishes responding (`Stop` hook), the full transcript JSONL is gzipped, uploaded to S3, and posted as a Slack file in the same thread via a new bridge action.

Replaces Phase 63's "one truncated `tail -1` message at idle" for sandboxes that opt in. Existing Phase 63 / Phase 67 behavior is unaffected for non-opted-in sandboxes.

This phase ALSO provisions a stream-message → transcript-position mapping table (DDB `km-slack-stream-messages`) with no consumer in this phase. That table is the integration seam for a future "Phase B" (reaction-triggered session fork), which is explicitly deferred.

</domain>

<decisions>
## Implementation Decisions

### Profile schema

- **New field:** `spec.cli.notifySlackTranscriptEnabled` (bool, default `false`)
- **Validation rule (hard):** Requires both `notifySlackEnabled: true` AND `notifySlackPerSandbox: true`. Fail with: `notifySlackTranscriptEnabled requires notifySlackEnabled and notifySlackPerSandbox`
- **Validation rule (hard):** Incompatible with `notifySlackChannelOverride`. Fail validation if both set.
- **No `EnvelopeVersion` bump.** Stays at `1`. New envelope fields are additive and zero-defaulted.

### CLI surface

- **`km agent run`:** add `--transcript-stream` and `--no-transcript-stream` flags. Mirror the existing `--notify-on-permission` / `--notify-on-idle` pattern in `internal/app/cmd/agent.go`.
- **`km shell`:** add the same two flags. Mirror the same pattern in `internal/app/cmd/shell.go`.
- **Env var injected:** `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=1` (or `=0`) into the SSM session env, taking precedence over profile default.

### Sandbox env vars

- `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED` — master gate, sourced from `/etc/profile.d/km-notify-env.sh`
- `KM_SLACK_STREAM_TABLE` — DDB table name for stream-mapping writes, runtime-injected by `km create`
- All other env vars (`KM_ARTIFACTS_BUCKET`, `KM_SLACK_CHANNEL_ID`, `KM_SLACK_BRIDGE_URL`, `KM_SLACK_THREAD_TS`) already exist; no changes.

### Hook script changes (`pkg/compiler/userdata.go`)

- **Register `PostToolUse`** in `mergeNotifyHookIntoSettings` at `pkg/compiler/userdata.go:2546-2557`. Existing `Notification` and `Stop` registrations stay.
- **Identifier convention:** Use Claude Code's `session_id` (from hook stdin JSON) as `{sid}` for `/tmp/` files. NOT `KM_AGENT_RUN_ID` (which is unset for `km shell`).
- **Streaming logic (`PostToolUse` branch):** Read `transcript_path` and `session_id` from stdin; tail the JSONL from `/tmp/km-slack-stream.{sid}.offset`; collect new `assistant.message.content[].text` and `tool_use` entries; render to chat body; post via `km-slack post`; write returned ts + transcript offset to DDB; update offset file.
- **Tool-call rendering:** `🔧 {tool_name}: {one-line-input}`. Inputs only, no outputs (the JSONL has the full record). Tool-specific renderers in a small map keyed by tool name; generic fallback prints first 80 chars of `jq -c .`.
- **Auto-thread-parent:** If `KM_SLACK_THREAD_TS` is unset and `/tmp/km-slack-thread.{sid}` doesn't exist, post a parent message `🤖 [sb-id] turn started — {prompt[:80]}` to the channel root, capture the returned ts, cache to `/tmp/km-slack-thread.{sid}`. All subsequent fires read from the cache.
- **Upload logic (`Stop` branch):** First run the streaming logic for any final assistant text not yet streamed. Then `gzip $transcript_path → /tmp/transcript.{sid}.jsonl.gz`, `aws s3 cp` to `s3://${KM_ARTIFACTS_BUCKET}/transcripts/{sandbox_id}/{sid}.jsonl.gz`, then `km-slack upload --channel ... --thread ... --s3-key ... --filename "claude-transcript-{sid}.jsonl.gz" --content-type application/gzip --size-bytes ...`.
- **Cleanup:** `rm /tmp/km-slack-thread.{sid} /tmp/km-slack-stream.{sid}.offset` at end of `Stop` branch.
- **Hook timeout:** wrap with existing `timeout 10s`. On timeout, exit 0.
- **Exit semantics unchanged:** Hook always exits 0 — Claude is never blocked by Slack issues.

### Envelope schema (`pkg/slack/payload.go`)

Add four additive fields to `SlackEnvelope`:
- `S3Key string \`json:"s3_key"\``
- `Filename string \`json:"filename"\``
- `ContentType string \`json:"content_type"\``
- `SizeBytes int64 \`json:"size_bytes"\``

Add new const `ActionUpload = "upload"`. Existing `BuildEnvelope` signature unchanged for `post`/`archive`/`test`. Add a new constructor or extend with optional fields for upload envelopes — implementer's choice.

### Bridge handler (`pkg/slack/bridge/`, `cmd/km-slack-bridge/`)

- New action route `ActionUpload` in the existing dispatcher.
- **Validation order (before any AWS work):**
  1. `S3Key` non-empty AND prefix matches `transcripts/{envelope.SenderID}/...` (cross-sandbox isolation)
  2. `Filename` non-empty, ≤ 255 bytes, no `/`, no NUL
  3. `ContentType` in allow-list: `application/gzip`, `application/json`, `text/plain`
  4. `SizeBytes > 0` AND `SizeBytes ≤ 100 MB`
  5. `Channel` non-empty
  6. `ThreadTS` may be empty
- **Slack 3-step flow:**
  1. `POST /api/files.getUploadURLExternal` with `{filename, length: SizeBytes}` → returns `{upload_url, file_id}`
  2. Bridge `GetObject` from S3 (streamed `io.ReadCloser`) → `PUT` to `upload_url` with envelope `Content-Type`
  3. `POST /api/files.completeUploadExternal` with `{files: [{id: file_id, title: filename}], channel_id: Channel, thread_ts: ThreadTS}`
- **Streaming:** Pass S3 `GetObject` body straight into the `PUT` reader. Peak memory stays at Go default (~30 MB) regardless of file size.
- **Scope check:** On bridge cold start, call `auth.test`; if `files:write` is missing AND any envelope arrives with `ActionUpload`, return 400 with error: `bot lacks files:write — operator must re-auth Slack App`.

### `km-slack` sandbox binary (`cmd/km-slack/`)

- New subcommand `upload`: signs an `ActionUpload` envelope with the existing Ed25519 key (loaded from SSM), POSTs to `KM_SLACK_BRIDGE_URL`, retries via existing `BridgeBackoff` on network errors (1s/2s/4s).
- New subcommand `record-mapping`: writes `(channel_id, slack_ts) → {sandbox_id, session_id, transcript_offset, ttl_expiry}` to DDB `{prefix}-slack-stream-messages` (RESOLVED 2026-05-03 — see "DDB table" decision below). Uses sandbox IAM `dynamodb:PutItem`.
- Existing `post` subcommand unchanged.

### DDB table

- **Name:** `{prefix}-slack-stream-messages` (follows existing `{prefix}-slack-threads` pattern from Phase 67 — RESOLVED 2026-05-03 during Plan 03; the originally-spec'd `{prefix}-km-...` would have produced a double-prefixed name with the default prefix; corrected to match Phase 67's `{prefix}-slack-threads` convention)
- **Schema:** PK `channel_id` (String), SK `slack_ts` (String). Attributes: `sandbox_id`, `session_id`, `transcript_offset` (Number), `ttl_expiry` (Number, Unix epoch seconds, 30 days from write).
- **Billing:** on-demand
- **TTL:** enabled on `ttl_expiry`
- **Provisioning:** new module `infra/modules/dynamodb-slack-stream-messages/` (mirroring Phase 67's `dynamodb-slack-threads/`).
- **Config helper:** `Config.GetSlackStreamMessagesTableName()` mirroring `GetSlackThreadsTableName()`.

### IAM additions

**Bridge Lambda role:**
- `s3:GetObject` on `${KM_ARTIFACTS_BUCKET}/transcripts/*`
- `s3:HeadObject` on the same prefix (defense-in-depth size check)

**Sandbox EC2 instance role:**
- `s3:PutObject` on `${KM_ARTIFACTS_BUCKET}/transcripts/{sandbox_id}/*` — alongside existing email-attachment prefix grants
- `dynamodb:PutItem` on `arn:aws:dynamodb:*:*:table/{prefix}-slack-stream-messages` (RESOLVED 2026-05-03)

### S3 layout

- **Prefix:** `transcripts/{sandbox_id}/{session_id}.jsonl.gz`
- **Bucket:** existing `KM_ARTIFACTS_BUCKET`
- **Lifecycle:** same retention as existing email attachments (no new lifecycle rule)
- **No object deletion by sandbox or bridge** — bucket lifecycle handles cleanup

### Error handling philosophy

`km-notify-hook` always exits 0. Every failure path silently logs to stderr.

| Failure | Behavior |
|---|---|
| Bridge unreachable (network) | Retry per `BridgeBackoff`; on full failure, log + exit 0 |
| Bridge returns 4xx / 5xx | No retry, log, exit 0 |
| `files:write` missing | Bridge 400; hook stderr `WARN: operator must re-auth Slack App with files:write`; stream still works |
| S3 upload fails | Skip `km-slack upload` entirely; log; exit 0 |
| Transcript file missing/corrupt at Stop | Skip upload, log; streaming already happened |
| DDB `PutItem` fails | Log, continue; Slack message exists; fork-resolution would fail for that ts (Phase B problem) |
| Hook timeout | Existing `timeout 10s` wrapper handles; exit 0 |

### `km doctor` checks (three new)

1. `slack_transcript_table_exists` — DDB `{prefix}-slack-stream-messages` exists and is reachable (RESOLVED 2026-05-03)
2. `slack_files_write_scope` — bot has `files:write` (probe `auth.test` response → scopes list contains `files:write`)
3. `slack_transcript_stale_objects` — S3 prefix `transcripts/{sandbox_id}/` for sandboxes that no longer exist in DDB sandboxes table (cleanup advisory)

### Operator warning at `km create`

When `notifySlackTranscriptEnabled: true` resolves true after profile + CLI overrides, print to stderr:

```
⚠ Slack transcript streaming enabled — full Claude transcripts (including tool I/O) will be posted to channel <id>. Audience: <member-count> Slack users.
```

Member count via `conversations.info.num_members` (already used in Phase 67 channel-info checks). Non-blocking.

### Observability

**Sandbox-side OTEL spans (gated by existing `KM_OTEL_ENABLED`):**
- `km.notify_hook.stream` — per `PostToolUse` fire. Attributes: `event.type=PostToolUse`, `messages.posted`, `bytes.streamed`
- `km.notify_hook.upload` — per `Stop` fire when upload runs. Attributes: `transcript.bytes_compressed`, `transcript.bytes_raw`, `s3.put.duration_ms`, `bridge.duration_ms`

**Bridge CloudWatch metrics (existing namespace):**
- `BridgeUploadCount` (count, dimensions: `Action=upload`, `Result=success|failure`)
- `BridgeUploadBytes` (sum)
- `BridgeUploadDurationMillis` (avg/p50/p95/p99)

### Deploy convention

- `km init --sidecars` is REQUIRED after Phase 68 ships:
  - New `km-slack` binary with `upload` + `record-mapping` subcommands
  - New bridge Lambda zip with `ActionUpload`
  - New profile schema
  - New DDB table (Terraform apply)
- Existing sandboxes do NOT get the new behavior retroactively. `km destroy` + `km create` to pick up new env vars + user-data.
- Operator deploy order: bridge first, then sidecar binary upload. Reverse order produces 400s during the gap, but hooks always exit 0 → file missing, no failures.

### Backwards compatibility

- Existing `post` envelopes serialize identically (additive fields are zero-valued; canonical JSON ordering preserved by alphabetical struct tags). No `EnvelopeVersion` bump.
- Old `km-slack` binary (no `upload` subcommand) on existing sandboxes: unaffected — hook never calls upload.
- Old bridge Lambda (no `ActionUpload`): returns `400 unknown action`; sandbox treats as non-fatal warning; hook exits 0.
- Phase 63 idle-ping behavior unchanged for sandboxes that don't opt into transcript streaming.
- Phase 67 inbound poller / SQS dispatch unaffected — transcript streaming runs alongside, doesn't replace.

### Testing strategy

**Unit:**
- `pkg/slack/payload_test.go` — extend with `ActionUpload` envelope canonical-JSON tests; verify backwards-compat (post envelopes serialize unchanged with new fields zero-valued)
- `pkg/slack/client_test.go` — table-driven tests for `UploadFile` covering 3-step flow, mid-step failures, stream pass-through correctness
- `pkg/slack/bridge/` — handler tests for `ActionUpload`: prefix validation, size cap, content-type allow-list, scope-missing path
- `pkg/compiler/notify_hook_script_test.go` — extend with `PostToolUse` fixture covering: gate-off, gate-on with no thread (auto-parent), gate-on with thread, multi-tool-call response, thread-cache cleanup, offset-tracking correctness across multiple `PostToolUse` fires

**Integration (real EC2):**
- `make build && km init --sidecars && km create profiles/test-transcript.yaml`
- UAT scenario "long autonomous run": `km agent run sb-X --prompt "audit and fix the failing tests in this repo"`. Verify per-turn messages stream, DDB rows appear, Stop posts file, file is openable + valid gzip + parses as JSONL.

**E2E regression:**
- Phase 67 inbound UAT scenarios with `notifySlackTranscriptEnabled: true` — verify Phase 67 still works
- Phase 63 outbound UAT scenarios with `notifySlackTranscriptEnabled: false` — verify no behavior change for non-opted-in sandboxes

**Failure injection:**
- Bridge with `files:write` removed → confirm 400 path + graceful sandbox handling
- S3 prefix mismatch → confirm bridge rejects
- 100 MB transcript → Lambda memory + timeout headroom

### Claude's Discretion

- Exact wave / plan partitioning of the 10 work units in the spec roadmap section. The spec lists likely units; the planner refines sequencing.
- Whether the `UploadFile` client method takes `(channel, threadTS, filename, contentType, sizeBytes, body)` as positional args vs an options struct — implementer's call.
- Whether `record-mapping` is a flag on `km-slack post` or a separate subcommand — spec implies separate; planner can choose.
- Tool-call one-liner rendering format for complex tools (`Edit` with multi-line `new_string`, `Bash` with embedded newlines). Spec suggests tool-specific renderers + generic fallback; planner chooses the exact renderer set.
- File naming on Slack — spec says `claude-transcript-{session_id}.jsonl.gz`. Planner can refine if a more readable form is preferred (e.g. include sandbox-id), as long as it's deterministic per session.
- Lambda provisioned concurrency adjustment if needed for the new upload action — spec marks as "verify in implementation."
- DDB module structure mirroring Phase 67's `dynamodb-slack-threads/` pattern is recommended; implementer confirms exact convention.

</decisions>

<specifics>
## Specific Ideas

### Concrete file references in the codebase

- `pkg/compiler/userdata.go:399-491` — existing `km-notify-hook` heredoc, `Notification` and `Stop` branches. `PostToolUse` branch + transcript upload logic added here.
- `pkg/compiler/userdata.go:2522` — `mergeNotifyHookIntoSettings`. Add `appendKMHook("PostToolUse", ...)` alongside existing `Notification` and `Stop`.
- `pkg/compiler/userdata.go:185, 525` — sidecar binary deploy pattern; `km-slack` binary already deployed; new subcommands ship in the same binary.
- `pkg/slack/payload.go:21` — existing `MaxBodyBytes = 40 * 1024` (40KB). For streaming chat, this still applies. For uploads, `SizeBytes ≤ 100 MB` enforced by bridge.
- `pkg/slack/payload.go:29-32` — existing `ActionPost`/`ActionArchive`/`ActionTest` consts. Add `ActionUpload`.
- `pkg/slack/payload.go:44-54` — `SlackEnvelope` struct. Add four fields: `S3Key`, `Filename`, `ContentType`, `SizeBytes`. Alphabetical struct tag order preserved.
- `pkg/slack/client.go:101-118` — existing `PostMessage`. New `UploadFile` method alongside.
- `pkg/slack/client.go:174` — `BridgeBackoff` retry schedule. Reused for upload.
- `pkg/slack/bridge/aws_adapters.go:351` — existing post-message wiring. New upload wiring follows same pattern.
- `internal/app/cmd/agent.go` and `internal/app/cmd/shell.go` — existing `--notify-on-permission` flag handling. New `--transcript-stream` / `--no-transcript-stream` flags follow same env-injection pattern.
- `pkg/profile/validation.go` — existing validation rules. New rule: `notifySlackTranscriptEnabled` requires `notifySlackEnabled + notifySlackPerSandbox`, incompatible with `notifySlackChannelOverride`.
- `internal/app/cmd/doctor*.go` — existing doctor checks. Add three new checks following existing convention (Phase 67 added similar in `doctor_slack_inbound.go`).
- `internal/app/cmd/create.go`, `create_slack.go` — operator warning printed when transcript streaming resolves to true after profile + CLI overrides. Follow Phase 67's per-sandbox channel pattern for member-count fetch.

### Convention precedents to follow

- **Phase 62 hook script structure** (`pkg/compiler/userdata.go:399-491`) — same heredoc, same `set -euo pipefail`, same gate check at top, same exit-0 contract.
- **Phase 63 envelope sign/verify** (`pkg/slack/`) — Ed25519, canonical JSON, nonce, 5-minute timestamp window. New `ActionUpload` rides the same path; the bridge already validates timestamp + nonce + signature.
- **Phase 67 SSM Parameter Store pattern** for queue URL injection — `km create` writes the value, sandbox poller reads on first boot if env var empty. Phase 68 follows the same pattern if the DDB stream table name needs late-binding (likely it doesn't, since the table name is deterministic from prefix; can be hard-coded in `/etc/profile.d/km-notify-env.sh`).
- **Phase 67 DDB module structure** — `infra/modules/dynamodb-slack-threads/` mirrors what `dynamodb-slack-stream-messages/` should look like.
- **Phase 67 SDK-not-Terraform pattern** — Phase 67's per-sandbox SQS queues are provisioned via AWS SDK (not Terraform). Phase 68's DDB table is project-wide (one table, all sandboxes), so it IS Terraform-managed. Don't confuse the two.
- **`make build` requirement** — per `feedback_rebuild_km.md`: always `make build` (not bare `go build`) after editing CLI source — includes ldflags for version.
- **Profile schema → Lambda toolchain refresh** — per `project_schema_change_requires_km_init.md`: profile schema additions fail remote creates until management Lambda's toolchain/km is refreshed via `km init --sidecars`.
- **Lambda role replace_triggered_by** — per `project_lambda_role_replace_trigger.md`: management Lambdas need `replace_triggered_by = [role]` to avoid stale aws/lambda KMS grants when IAM role is recreated. Apply if Phase 68 modifies bridge Lambda IAM (it does — `s3:GetObject` + `s3:HeadObject` additions).

### Project conventions captured in CLAUDE.md and memory

- `make build` after every CLI source edit (not bare `go build`)
- `km destroy` uses `--remote --yes` (not `--force`)
- This project uses GSD (`.planning/`) for tracking; never offer `beads` for issue creation
- Plugin / file pre-seeding during create isn't always sufficient; diff post-install state to find missing pieces (relevant for sidecar binary install verification)

</specifics>

<deferred>
## Deferred Ideas

The following are explicitly out of scope for Phase 68 (Phase A). Each will be addressed by a future phase or remain permanently out of scope:

### Deferred to Phase B (next phase, separate spec)

- Slack scope `reactions:read` and bridge `/events` route handling `reaction_added`
- New SQS event-type for fork dispatch
- Sandbox-side transcript surgery (copy JSONL up to fork offset, mint new Claude session-id, register in `km-slack-threads`)
- Phase 67 poller extension to route by `(channel_id, thread_ts)` rather than `channel_id` alone
- New thread parent in same channel: `🍴 forked from <message-link>`
- Trigger emoji configuration

### Permanently out of scope (call out + document, don't build)

- Transcript redaction / secret-stripping. Transcripts contain whatever Claude saw — the operator opted in.
- Mid-run threshold-triggered uploads (e.g. upload when transcript size > N KB)
- Reaction-triggered on-demand uploads (not the same as fork — this would be an upload-only reaction)
- Full tool I/O in streamed messages (we ship text + tool-name one-liner; the file has the full record)
- Stream-without-upload or upload-without-stream toggles (one flag controls both)
- Streaming on the shared channel — per-sandbox required
- Replacing Phase 63 idle-ping behavior — this is *additional*, gated on opt-in
- This is NOT a logging/observability replacement for OTEL. `km otel` remains canonical for cost/event analytics.
- This is NOT bidirectional. Slack reactions/replies do not drive any new sandbox behavior.
- This is NOT a replacement for `km agent results` / `km agent list` — those remain authoritative for programmatic access.

</deferred>

---

*Phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload*
*Context gathered: 2026-05-03 via PRD Express Path from `docs/superpowers/specs/2026-05-03-slack-transcript-streaming-design.md`*
