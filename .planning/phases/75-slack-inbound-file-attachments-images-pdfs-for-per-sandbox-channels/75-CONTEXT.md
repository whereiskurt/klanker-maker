# Phase 75: Slack inbound file attachments — Context

**Gathered:** 2026-05-15
**Status:** Ready for planning
**Source:** PRD Express Path (`docs/superpowers/specs/2026-05-15-slack-inbound-file-attachments-design.md`)

<domain>
## Phase Boundary

Extend the Phase 67 Slack inbound flow so users can paste files
(images, PDFs, etc.) into a per-sandbox `#sb-{id}` channel and
reference them conversationally. Today, file_share messages are
silently dropped at `pkg/slack/bridge/events_handler.go:265` because
the Phase 67-12 allow-list filter only admits `""` and
`"thread_broadcast"` subtypes — the user sees no 👀, no agent reply.

The phase adds a file-handling fork to the bridge inbound path:
admit `file_share` subtype, fire-and-forget download from
`files.slack.com` using the bot token, S3-stage to
`slack-inbound/<sandbox-id>/<thread_ts>/<file_id>-<sanitized_name>`,
extend the SQS message body with `Attachments[]`, mirror to
`/workspace/.km-slack/attachments/<thread_ts>/` on the sandbox side,
and prepend a natural-language master-prompt wrapper to `claude -p`
listing absolute paths + MIME types.

**In scope:**
- Bridge code: `slackMessageEvent.Files` parsing, `isBotLoop`
  allow-list extension, file-downloader adapter, fork in `Handle()`
  on `len(Files) > 0`, warning thread-replies on partial failures
- SQS payload: `InboundQueueBody.Attachments[]` extension
- Sandbox poller (`pkg/compiler/userdata.go:1259-1499`): S3 mirror,
  master-prompt wrapper, cross-turn file persistence
- S3 layout + IAM (bridge gains `s3:PutObject` on `slack-inbound/*`)
- S3 lifecycle: 30-day expiration on `slack-inbound/` prefix
- Slack scope addition: `files:read` (`km slack init` and
  `km doctor` `required` slices both extended)
- Test moat: ~14 new unit tests across bridge + compiler + cmd
- New `km doctor` check `slack_files_read_scope`
- Manual UAT gate (drag image/PDF, verify Claude reads it)

**Out of scope** (explicitly deferred — see `<deferred>`):
- Outbound files (Claude attaching files to its Slack reply)
- Long-lived attachment GC inside running sandboxes
- `file_revoked` / `file_deleted` Slack event handling
- Per-MIME special handling beyond Read-tool defaults
- Bridge-side virus scanning
- `slack_inbound_stale_attachments` doctor check (deferred to follow-up)

</domain>

<decisions>
## Implementation Decisions

### Architecture (locked)

- **Fire-and-forget download goroutine**, mirroring the Phase 67.1
  reaction-add pattern. Bridge returns 200 within ~100ms; goroutine
  downloads files, stages to S3, then writes SQS. Slack 3s ack
  deadline honored even for 100 MB files. If Slack retries before
  the goroutine completes, existing nonce dedup blocks the retry.

- **Fork on `len(Files) > 0`** in `EventsHandler.Handle`. Files-empty
  path is unchanged from today (synchronous SQS write + reactor
  goroutine + 200). Files-present path: skip synchronous SQS write,
  fire downloader goroutine that does (download → S3 → SQS write),
  reactor goroutine still fires, return 200.

- **Bridge admits `file_share` via allow-list extension.**
  `events_handler.go:265` allow-list becomes
  `["", "thread_broadcast", "file_share"]`. Single-line additive
  change; matches Slack's stable subtype convention.

- **Master-prompt wrapper is natural-language**, prepended only when
  files are present:

  ```
  The user attached the following file(s) to this Slack message.
  Read them with your Read tool when relevant to the question:
    - /workspace/.km-slack/attachments/<thread_ts>/<file_id>-<original_name> (<mimetype>)
    - ...

  User's message: <original text, or "[no text — file-only]" if empty>
  ```

  Trusts Claude to decide whether to read each file based on the
  question. Avoids imperative directives that produce mechanical
  responses for non-file follow-ups in the same thread.

- **Cross-turn persistence:** files stay in
  `/workspace/.km-slack/attachments/<thread_ts>/` for the lifetime
  of the sandbox. Subsequent turns in the same thread don't
  re-download (already on disk) and don't re-prepend the wrapper
  (only new files in the current message get listed).

- **Bridge-only deploy via full `km init`.** NOT `km init --lambdas`
  (that path only builds zips, doesn't actually deploy them — see
  the `km-init-lambdas-doesnt-deploy` memory from Phase 67.2).
  Operator path: `make build && km init`.

### S3 layout (locked)

- **Key format:** `slack-inbound/<sandbox-id>/<thread_ts>/<file_id>-<sanitized_name>`
  - `<file_id>` is Slack's `F012345` ID (unique → no collisions
    even when two files share a name in the same thread)
  - `<sanitized_name>` strips `/`, `\`, `..`, `\0`, non-printable
    bytes; truncates to 255 bytes (POSIX path limit)
- **Lifecycle:** 30-day expiration on `slack-inbound/` prefix
  (matches `km-slack-threads` DDB TTL)
- **Sandbox-side mirror name:** `<file_id>-<sanitized_name>`
  (matches the S3 leaf for traceability)

### Caps + failure handling (locked from spec)

- **Per-message cap:** 25 files. Bridge takes first 25, drops rest,
  posts thread-reply: `⚠️ Only first 25 of N files attached; rest skipped`
- **Per-file cap:** 100 MB. Bridge drops oversize, posts thread-reply:
  `⚠️ Skipped <name> (<N> MB > 100 MB cap)`
- **Single file download fails:** drop that file, dispatch turn with
  what succeeded, post thread-reply mentioning the failure
- **All files fail:** dispatch turn with text-only + warning
- **S3 PutObject fails:** treated as download fail
- **`files:read` scope missing (401 from files.slack.com):** treated
  as download fail; logged at Error level for operator
- **Goroutine panics:** `recover()`, log Error, post thread-reply
  about operator notification

Warnings ALWAYS posted to the same thread BEFORE the agent's reply
(via existing `SlackPosterAdapter.PostMessage`).

### Bridge code changes (locked from spec)

`pkg/slack/bridge/events_types.go`:
- `slackMessageEvent` gains `Files []SlackFile` field
- New `SlackFile` struct: `ID` (e.g. `F012345`), `Name` (original
  filename), `Mimetype` (e.g. `image/png`), `URLPrivateDownload`
  (the URL the bridge fetches with bot token), `Size` (bytes)
- `InboundQueueBody` gains `Attachments []Attachment` field
- New `Attachment` struct: `S3Key`, `OriginalName`, `Mimetype`

`pkg/slack/bridge/events_handler.go`:
- `isBotLoop` allow-list at line 265 adds `"file_share"`
- New branch in `Handle`: when `len(msg.Files) > 0`, fire the
  download-and-dispatch goroutine instead of the synchronous SQS write

`pkg/slack/bridge/file_downloader.go` (new file):
- `FileDownloader` interface for testability
- `S3FileDownloader` adapter struct holding `HTTPClient`,
  `S3PutObjectAPI`, `Tokens` (shares `BotTokenFetcher` with
  `SlackPosterAdapter`/`SlackReactorAdapter` for cache reuse)
- `Download(ctx, files []SlackFile, sandboxID, threadTS string)
  ([]Attachment, []FileError, error)` — returns successfully-staged
  attachments and per-file errors; caller decides whether to dispatch
  the turn based on what succeeded
- Filename sanitization helper (strip + truncate)

`cmd/km-slack-bridge/main.go`:
- Wire `S3FileDownloader` adapter (mirrors existing
  `SlackPosterAdapter` and `SlackReactorAdapter` shape)
- Add `KM_ARTIFACTS_BUCKET` env var read at cold start (already used
  by sandbox-side; new to bridge Lambda)

### Sandbox-side changes (locked from spec)

`pkg/compiler/userdata.go` (the inbound poller bash, lines ~1259-1499):
- Extract `attachments` array from SQS body via jq (`.attachments[]?`
  for safe degradation when older bridges send the field absent)
- For each attachment:
  ```
  ATTACH_DIR="/workspace/.km-slack/attachments/$THREAD_TS"
  mkdir -p "$ATTACH_DIR"
  aws s3 cp "s3://$KM_ARTIFACTS_BUCKET/$S3_KEY" "$ATTACH_DIR/$LOCAL_NAME"
  chown sandbox:sandbox "$ATTACH_DIR/$LOCAL_NAME"
  ```
- Build wrapper text and prepend to `PROMPT_FILE` only when
  `len(attachments) > 0`
- Cleanup happens via the existing 30-day S3 lifecycle (staging side)
  and `km destroy` (sandbox-side files go with the EC2 instance)

### IAM + Terraform (locked)

- `infra/modules/lambda-slack-bridge/v1.0.0/main.tf`: add
  `s3:PutObject` on `arn:aws:s3:::${bucket}/slack-inbound/*` to the
  inline policy. Single-line additive change.
- S3 lifecycle rule: prefix `slack-inbound/`, expiration 30 days.
  Implementation home TBD during research — if no clean owner module
  exists, plan adds an inline rule on the bucket directly. Mirrors
  the Phase 68 transcript-cleanup pattern.

### Slack scope addition (locked)

- New required scope: `files:read`
- `internal/app/cmd/slack.go:768` — `VerifyEventsAPIScopes`
  `required` slice gains `"files:read"`
- `internal/app/cmd/doctor_slack.go:375` — `checkSlackAppEventsScopes`
  `required` slice gains `"files:read"`
- Operator path: re-install app + `km slack rotate-token --bot-token <new>`

### Test moat (locked from spec)

| Layer | Test |
|---|---|
| `pkg/slack/bridge/events_handler_test.go` | `TestEventsHandler_FileShareSubtype_Allowed` |
| `pkg/slack/bridge/events_types_test.go` (new) | `TestSlackMessageEvent_FilesField_ParsesCorrectly` |
| `pkg/slack/bridge/file_downloader_test.go` (new) | `TestFileDownloader_HappyPath` |
| | `TestFileDownloader_Over100MB_Dropped` |
| | `TestFileDownloader_Over25Files_Truncated` |
| | `TestFileDownloader_DownloadFails_Continues` |
| | `TestFileDownloader_AllFail_ReturnsEmpty` |
| | `TestFileDownloader_S3PutFails_TreatedAsDownloadFail` |
| | `TestFileDownloader_403_LogsErrorAndDrops` |
| | `TestFileDownloader_FilenameSanitization` |
| `pkg/slack/bridge/events_handler_test.go` | `TestEventsHandler_WithFiles_FiresGoroutine_Returns200Fast` |
| `pkg/compiler/userdata_slack_inbound_test.go` | `TestUserdata_SlackInbound_AttachmentMirrorBlock` |
| | `TestUserdata_SlackInbound_MasterPromptWrapper` |
| `internal/app/cmd/slack_test.go` | `TestSlackInit_FilesReadScope_Required` |
| `internal/app/cmd/doctor_slack_test.go` | `TestDoctor_FilesReadScope_Missing_Reports` |

Existing handler-side tests (`TestEventsHandler_Reactor_*`,
`TestEventsHandler_HappyPath_TextOnly`, etc.) MUST continue passing
— files-empty path is unchanged.

### Doctor check (locked, v1)

`slack_files_read_scope` — extends the existing scope-check loop in
`internal/app/cmd/doctor_slack.go`. Pattern matches
`slack_app_events_subscription`. Bot must have `files:read`.

### Roadmap dependency fix (locked)

The ROADMAP currently says "Phase 75 depends on Phase 74." That's
incorrect — Phase 74 is the output renderer
(`pkg/slack/mrkdwn.go`); Phase 75 is the inbound file path. They
touch different files in different directions. Fix the ROADMAP
`Depends on:` line during planning to read "Phase 67, Phase 67.1".

### Claude's Discretion

These were not pinned in the spec — the planner may decide:

- **Where the `S3FileDownloader` lives within the bridge package.**
  Recommendation: new file `pkg/slack/bridge/file_downloader.go`
  alongside `aws_adapters.go`. The `S3PutObjectAPI` interface
  pattern mirrors the existing narrow-interface adapter shape.

- **How to test the `FilenameSanitization`.** Recommendation:
  table-driven test with ~15 input-output pairs covering each
  forbidden character class plus length truncation.

- **Whether to use `aws s3 cp` or `aws s3api get-object` in the
  sandbox poller.** Recommendation: `aws s3 cp` — already used
  elsewhere in userdata, simpler error semantics.

- **S3 lifecycle module home.** Recommendation: research the
  current artifacts-bucket lifecycle config during research phase.
  If no clean owner, add an inline `aws_s3_bucket_lifecycle_configuration`
  rule on the bucket. Worst case: a new `infra/modules/s3-slack-inbound-lifecycle/v1.0.0/`
  module, but YAGNI unless the bucket already has multiple lifecycle
  consumers competing.

- **Warning thread-reply text wording.** Recommendation: use the
  exact warnings from the failure-handling matrix in the spec; they
  cover all the documented cases concisely.

</decisions>

<specifics>
## Specific Ideas

### Files modified

**Bridge:**
- `pkg/slack/bridge/events_types.go` — new fields on
  `slackMessageEvent` and `InboundQueueBody`; new `SlackFile` and
  `Attachment` structs
- `pkg/slack/bridge/events_handler.go` — `isBotLoop` allow-list
  extension + `Handle` fork on `len(Files) > 0`
- `pkg/slack/bridge/file_downloader.go` — NEW (interface + S3
  adapter + sanitization helper)
- `pkg/slack/bridge/aws_adapters.go` — possibly add
  `S3PutObjectAPI` interface if not already present
- `cmd/km-slack-bridge/main.go` — wire downloader at cold start

**Sandbox:**
- `pkg/compiler/userdata.go` — inbound poller bash extension
  (S3 mirror block + master-prompt wrapper)

**Infra:**
- `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` — `s3:PutObject`
  on `slack-inbound/*` added to inline policy
- S3 lifecycle config — module home determined during research

**Operator CLI:**
- `internal/app/cmd/slack.go` — `VerifyEventsAPIScopes` adds
  `files:read` to required slice
- `internal/app/cmd/doctor_slack.go` — `checkSlackAppEventsScopes`
  adds `files:read`; new `slack_files_read_scope` check

**Docs:**
- `docs/slack-notifications.md` — new subsection covering the
  inbound file flow, scope addition, and operator UAT
- `CLAUDE.md` — short Slack-inbound subsection mentioning files
  support, pointing at the spec

### Reference patterns

- **Existing fire-and-forget shape:** Phase 67.1's reactor goroutine
  at `events_handler.go:228-241` is the exact pattern the file
  downloader goroutine mirrors.
- **Adapter narrow-interface shape:** `SlackPosterAdapter` /
  `SlackReactorAdapter` in `pkg/slack/bridge/aws_adapters.go` —
  `S3FileDownloader` should mirror the constructor + `Tokens` cache
  sharing pattern.
- **Test fixture pattern:** Phase 67.2's `recordingTransport` and
  `captureBridgeLogger` helpers in
  `pkg/slack/bridge/aws_adapters_test.go` are the right fixtures
  for the downloader tests.
- **Compiler userdata test pattern:** existing
  `userdata_slack_inbound_test.go` already asserts bash blocks
  appear in the rendered template; same pattern for the new
  attachment block + wrapper.

### Interface contracts preserved

- `Reactor` interface (`events_interfaces.go:73-79`): unchanged
- `SlackPosterAdapter`: unchanged
- Existing `InboundQueueBody` fields (`Channel`, `ThreadTS`, `Text`,
  `User`, `EventTS`): unchanged — `Attachments` is additive
- `slackMessageEvent` existing fields: unchanged — `Files` is
  additive

### Documentation

- Update `docs/slack-notifications.md` § Slack inbound (Phase 67)
  with a new subsection covering Phase 75 file attachments
- Update `CLAUDE.md` § Slack Notifications → Slack inbound (Phase 67)
  subsection; point at spec for full design
- Authoritative design lives in
  `docs/superpowers/specs/2026-05-15-slack-inbound-file-attachments-design.md`
  — do not duplicate its content into user-facing docs

</specifics>

<deferred>
## Deferred Ideas

These were discussed during brainstorming and explicitly cut for scope:

- **Outbound files (Claude attaching files to Slack replies).**
  Different flow (bridge would call `files.uploadV2`); deferred to
  a future phase if user demand surfaces.

- **Long-lived attachment GC inside running sandboxes.** S3 lifecycle
  handles the staging side; sandbox-side files only clean up at
  destroy. If a sandbox runs long enough to accumulate problematic
  attachment volume, that's a separate concern for a follow-up.

- **Slack `file_revoked` / `file_deleted` events.** We have our own
  S3 copy; no point chasing Slack-side deletions. The 30-day
  lifecycle cleans up regardless.

- **Per-MIME special handling beyond Read-tool defaults.** Claude's
  Read tool already handles images (multimodal) and PDFs natively.
  No bridge-side OCR or PDF text extraction.

- **Bridge-side virus scanning.** Files come from authenticated
  Slack workspace members; not a separate trust boundary.

- **`slack_inbound_stale_attachments` doctor check.** Advisory check
  for S3 prefixes older than 30 days that didn't expire (lifecycle
  misconfigured). Mirrors `slack_transcript_stale_objects` from
  Phase 68. Worth adding in a follow-up if drift is observed.

- **`km destroy` cleanup of `slack-inbound/<sandbox-id>/` S3
  prefix.** Lifecycle handles it within 30 days regardless. If
  faster cleanup is desired, a future phase can add explicit
  prefix-delete on destroy.

</deferred>

---

*Phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels*
*Context gathered: 2026-05-15 via PRD Express Path*
