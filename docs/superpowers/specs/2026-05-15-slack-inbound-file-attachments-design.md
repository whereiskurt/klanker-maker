# Slack inbound file attachments — design note

**Status:** Proposal — pending operator sign-off, then `/gsd:plan-phase 75`.
**Author:** Brainstorm session, 2026-05-15.
**Date:** 2026-05-15.

## Problem

Phase 67's inbound flow turns Slack messages into Claude turns inside a
sandbox. Today, when a user drags a file into a `#sb-{id}` channel
(screenshot, PDF, log, etc.), the bridge silently drops the message:
Slack delivers it with `subtype: "file_share"`, and the Phase 67-12
allow-list filter at `pkg/slack/bridge/events_handler.go:265` only
admits `""` (regular message) and `"thread_broadcast"`. The user sees
no 👀 reaction, no agent reply — just silence.

User intent is clear ("what's in this picture?", "summarize this PDF")
but there's no path for it to reach Claude.

## Proposed approach

Add a file-handling fork to the bridge inbound path:

1. **Bridge admits `file_share`.** Add `"file_share"` to the
   `isBotLoop` allow-list. Single-line change at
   `events_handler.go:265`.
2. **Bridge downloads + S3-stages files asynchronously.** When the
   parsed message has `len(Files) > 0`, the bridge fires a goroutine
   (mirroring the Phase 67.1 reaction-add fire-and-forget shape) that
   downloads each file from `files.slack.com` using the bot token,
   stages to S3, and only then writes the SQS message — replacing the
   sync SQS write on this branch. The 200 response still ships within
   ~100ms; Slack's 3s ack deadline is honored even for 100 MB files.
   If Slack retries during the goroutine, existing nonce dedup blocks
   the retry.
3. **Sandbox poller mirrors S3 → local.** The poller in
   `pkg/compiler/userdata.go:1259-1499` already builds the
   `claude -p` prompt from a `mktemp` file. Extend it to: (a) parse
   the new `Attachments[]` field from the SQS body, (b) `aws s3 cp`
   each attachment to `/workspace/.km-slack/attachments/<thread_ts>/`
   with `chown sandbox:sandbox`, (c) prepend a master-prompt wrapper
   to the prompt file enumerating the file paths and MIME types, (d)
   leave the `claude -p` invocation otherwise unchanged.
4. **Master-prompt wrapper is natural-language**, prepended only when
   files are present:

   ```
   The user attached the following file(s) to this Slack message.
   Read them with your Read tool when relevant to the question:
     - /workspace/.km-slack/attachments/<thread_ts>/F012345-screenshot.png (image/png)
     - /workspace/.km-slack/attachments/<thread_ts>/F012346-report.pdf (application/pdf)

   User's message: <original text, or "[no text — file-only]" if empty>
   ```

   Low ceremony. Trusts Claude to decide whether to read each file
   based on the question. The Read tool handles images natively
   (multimodal) and PDFs natively in current Claude Code versions.

## Components and data flow

```
                       Slack /events POST
                              │
                              ▼
            ┌──────────────────────────────────────────┐
            │ EventsHandler.Handle (existing)          │
            │  1. Sig verify                           │
            │  2. Nonce dedup                          │
            │  3. Parse slackMessageEvent (NEW: Files) │
            │  4. isBotLoop filter (NEW: file_share OK)│
            └──────────────┬───────────────────────────┘
                           │
            ┌──────────────┴──────────────┐
            │                             │
       len(Files)==0                len(Files)>0  (NEW PATH)
            │                             │
            ▼                             ▼
       SQS write (sync)         Goroutine (fire-and-forget):
       Reactor goroutine          a. Cap validation (>25 files / >100MB → drop+warn)
       Return 200                 b. Download from url_private_download
                                     using bot token
                                  c. PutObject to s3://$BUCKET/slack-inbound/
                                     <sandbox-id>/<thread_ts>/<file_id>-<sanitized_name>
                                  d. Build InboundQueueBody with Attachments[]
                                  e. SQS write
                                  f. On any per-file failure: post warning
                                     thread-reply, dispatch turn with
                                     successful attachments (or text-only
                                     if all failed)
                                Reactor goroutine fires regardless
                                Return 200 immediately
```

```
                       SQS message (with Attachments[])
                              │
                              ▼
            ┌──────────────────────────────────────────┐
            │ km-slack-inbound-poller (sandbox-side)   │
            │  1. Parse body — NEW: extract attachments│
            │  2. NEW: for each attachment:            │
            │       aws s3 cp → /workspace/.km-slack/  │
            │       attachments/<thread_ts>/           │
            │       chown sandbox:sandbox              │
            │  3. NEW: build wrapper, prepend to       │
            │       PROMPT_FILE                        │
            │  4. claude -p "$(cat $PROMPT_FILE)"      │
            │       (existing — unchanged)             │
            └──────────────────────────────────────────┘
```

## Bridge code changes

`pkg/slack/bridge/events_types.go`:
- `slackMessageEvent` gains `Files []SlackFile` field
- New `SlackFile` struct with the fields Slack provides on file_share:
  - `ID` (e.g. `F012345`)
  - `Name` (original filename)
  - `Mimetype` (e.g. `image/png`)
  - `URLPrivateDownload` (the URL the bridge fetches with bot token)
  - `Size` (bytes — for cap check)
- `InboundQueueBody` gains `Attachments []Attachment` field
- New `Attachment` struct: `S3Key`, `OriginalName`, `Mimetype`

`pkg/slack/bridge/events_handler.go`:
- `isBotLoop` allow-list at line 265 adds `"file_share"`
- New branch in `Handle`: when `len(msg.Files) > 0`, fire the
  download-and-dispatch goroutine instead of the synchronous SQS
  write

`pkg/slack/bridge/file_downloader.go` (new):
- `FileDownloader` interface for testability
- `S3FileDownloader` adapter struct that holds `HTTPClient`,
  `S3PutObjectAPI`, `Tokens` (shares `BotTokenFetcher` with
  `SlackPosterAdapter`/`SlackReactorAdapter` for cache reuse)
- `Download(ctx, files []SlackFile, sandboxID, threadTS string)
  ([]Attachment, []FileError, error)`:
  - Returns successfully-staged attachments and per-file errors
  - Caller decides whether to dispatch the turn based on what
    succeeded
- Filename sanitization: strip `/`, `\`, `..`, `\0`, non-printable
  bytes; truncate to 255 bytes

`cmd/km-slack-bridge/main.go`:
- Wire `S3FileDownloader` adapter (mirrors `SlackPosterAdapter` and
  `SlackReactorAdapter` shape)
- Add `KM_ARTIFACTS_BUCKET` env var read at cold start

## Sandbox-side changes

`pkg/compiler/userdata.go` (the inbound poller bash, lines ~1259-1499):
- Extract `attachments` array from SQS body via jq
- For each attachment: `aws s3 cp s3://...` to local path; `chown
  sandbox:sandbox`
- Build wrapper text and prepend to `PROMPT_FILE` only when
  `attachments` non-empty
- Cross-turn persistence: files stay in
  `/workspace/.km-slack/attachments/<thread_ts>/` for the lifetime
  of the sandbox; cleaned up automatically on `km destroy` (workspace
  goes with the EC2 instance) or by S3 lifecycle on the staging
  prefix (30 days, matches the DDB TTL)

## S3 layout, IAM, lifecycle

**S3 key format:**

```
slack-inbound/<sandbox-id>/<thread_ts>/<file_id>-<sanitized_name>
```

The `<file_id>` prefix prevents collisions when two files share a name
in the same thread. `<sanitized_name>` strips `/`, `\`, `..`, `\0`,
and non-printable bytes; truncates to 255 bytes.

**IAM:**
- **Bridge Lambda role**
  (`infra/modules/lambda-slack-bridge/v1.0.0/main.tf`): add
  `s3:PutObject` on
  `arn:aws:s3:::${bucket}/slack-inbound/*` to the inline policy.
  Single-line additive change.
- **Sandbox role**: already has `s3:GetObject` on the artifacts
  bucket via the existing transcript/agent-output flow; no change.

**S3 lifecycle:**
- New rule on the artifacts bucket: prefix `slack-inbound/`,
  expiration 30 days from creation. Matches the
  `km-slack-threads` DDB TTL.
- Implementation home TBD during planning. If the lifecycle config
  doesn't have a clean owner module, the plan adds an inline rule
  on the bucket directly. (Mirrors the Phase 68 transcript-cleanup
  pattern.)

## Slack scope addition

**New required scope:** `files:read`

Operator one-time path:
```bash
# 1. Re-install Slack app with files:read added
# 2. Rotate token to pick up the new scope
km slack rotate-token --bot-token <new>
# 3. Deploy bridge code + IAM update
make build && km init
```

`km slack init` and `km doctor` already check required scopes
(Phase 67.1 added `reactions:write`). Extend the `required` slices in
both files to include `files:read`:
- `internal/app/cmd/slack.go:768` — `VerifyEventsAPIScopes`
- `internal/app/cmd/doctor_slack.go:375` — `checkSlackAppEventsScopes`

## Failure handling matrix

| Failure | Behavior | User signal |
|---|---|---|
| File over 100 MB | Drop file, dispatch turn with the rest | Thread-reply: `⚠️ Skipped large-file.pdf (152 MB > 100 MB cap)` |
| >25 files in one message | Take first 25, drop rest | Thread-reply: `⚠️ Only first 25 of 32 files attached; rest skipped` |
| Single file download fails (network, 403, etc.) | Drop that file, dispatch turn with what succeeded | Thread-reply: `⚠️ Failed to fetch foo.png (403); other files attached as usual` |
| All files fail download | Dispatch turn with text-only + warning | Thread-reply: `⚠️ Could not fetch any of the 3 attached files; processing your text only` |
| S3 PutObject fails for any file | Treated same as download fail (S3 is part of the staging path) | Same warning shape |
| Bot token lacks `files:read` scope | Detected on first 401 from `files.slack.com`; same as download fail but log at Error level | Thread-reply mentions scope; operator sees Error in CloudWatch |
| `slackMessageEvent.Files` empty (race: file deleted before bridge could read) | Dispatch turn as text-only (existing path) | None — invisible to user |
| Bridge goroutine crashes (panic) | `recover()` in goroutine, log Error, post thread-reply: `⚠️ Failed to process attachments — operator notified` | Recovers; user knows |

Warnings always go BEFORE the agent's reply (so the user sees what
was dropped, then the answer). Implementation: bridge posts the
warning via existing `SlackPosterAdapter.PostMessage` to the same
thread, then writes SQS with whatever attachments succeeded.

## Test plan

| Layer | Test | What it covers |
|---|---|---|
| `pkg/slack/bridge/events_handler_test.go` | `TestEventsHandler_FileShareSubtype_Allowed` | `file_share` subtype passes `isBotLoop` |
| `pkg/slack/bridge/events_types_test.go` (new) | `TestSlackMessageEvent_FilesField_ParsesCorrectly` | JSON unmarshal of real Slack file_share payload |
| `pkg/slack/bridge/file_downloader_test.go` (new) | `TestFileDownloader_HappyPath` | Single file download + S3 put + Attachment returned |
| | `TestFileDownloader_Over100MB_Dropped` | Cap enforcement |
| | `TestFileDownloader_Over25Files_Truncated` | Per-message cap enforcement |
| | `TestFileDownloader_DownloadFails_Continues` | One file fails, others succeed |
| | `TestFileDownloader_AllFail_ReturnsEmpty` | Caller dispatches text-only |
| | `TestFileDownloader_S3PutFails_TreatedAsDownloadFail` | S3 errors handled |
| | `TestFileDownloader_403_LogsErrorAndDrops` | Auth failure path |
| | `TestFileDownloader_FilenameSanitization` | `/`, `..`, non-printable stripped |
| `pkg/slack/bridge/events_handler_test.go` | `TestEventsHandler_WithFiles_FiresGoroutine_Returns200Fast` | 200 returns within ~100ms even with mocked slow downloader |
| `pkg/compiler/userdata_slack_inbound_test.go` | `TestUserdata_SlackInbound_AttachmentMirrorBlock` | Bash block present in rendered userdata |
| | `TestUserdata_SlackInbound_MasterPromptWrapper` | Wrapper format matches spec |
| `internal/app/cmd/slack_test.go` | `TestSlackInit_FilesReadScope_Required` | `km slack init` checks for `files:read` |
| `internal/app/cmd/doctor_slack_test.go` | `TestDoctor_FilesReadScope_Missing_Reports` | `km doctor` flags missing scope |

**Manual UAT (one item, gated checkpoint similar to Phase 67.2):**

1. Re-install Slack app with `files:read` scope; `km slack rotate-token --bot-token <new>`
2. `make build && km init` (full — bridge zip + IAM + lifecycle apply)
3. Drag a screenshot into a `#sb-{id}` channel with the comment
   "describe this image". Confirm 👀 within ~1s, then Claude's
   reply describes the image (proves multimodal Read worked).
4. Drag a PDF, ask "what's in this document?". Confirm Claude reads
   the PDF.
5. Drag 26 files at once; confirm the truncation warning thread-reply
   appears before the turn.

## Doctor checks

**v1 (in scope):**
- `slack_files_read_scope` — extends the existing scope-check loop;
  bot must have `files:read`. Pattern matches `slack_app_events_subscription`.

**Deferred (not blocking):**
- `slack_inbound_stale_attachments` — advisory check that flags S3
  `slack-inbound/` prefixes older than 30 days that didn't expire
  (lifecycle misconfigured). Mirrors `slack_transcript_stale_objects`
  from Phase 68. Worth adding in a follow-up if drift is observed.

## Deployment

```bash
make build       # rebuild km CLI + bridge zip via cross-compile
km init          # full — applies bridge IAM + S3 lifecycle + uploads bridge zip
```

(Note: `km init --lambdas` alone does NOT deploy the bridge zip — see
the `km-init-lambdas-doesnt-deploy` memory from Phase 67.2. Full
`km init` is required.)

**Existing sandboxes:** the userdata change requires `km destroy &&
km create` to take effect. Same caveat as Phases 67/68/79. The bridge
side ships first (so existing sandboxes ignore file_share messages
gracefully — the SQS message has Attachments[] populated but the old
poller's jq doesn't read that field, so files just sit in S3 until
lifecycle expires them).

**Rollback:** revert PR + `make build && km init`. Bridge reverts to
old code. S3 contents harmless to leave. Already-mirrored files in
sandbox `/workspace/.km-slack/attachments/` stay until the sandbox is
destroyed.

## Out of scope

- **Outbound files (Claude attaching files to its Slack reply).**
  Different flow (bridge would call `files.uploadV2`); deferred.
- **Long-lived attachment garbage collection inside running
  sandboxes.** S3 lifecycle handles the staging side; sandbox-side
  files only clean up at destroy. If a sandbox runs long enough to
  accumulate problematic attachment volume, that's a separate
  concern.
- **Slack `file_revoked` / `file_deleted` events.** We have our own
  S3 copy; no point chasing Slack-side deletions. The 30-day
  lifecycle cleans up regardless.
- **Per-MIME special handling beyond Read tool defaults.** Claude's
  Read tool already handles images (multimodal) and PDFs
  natively. We don't pre-extract OCR or PDF text in the bridge.
- **Bridge-side virus scanning.** Files come from authenticated
  Slack workspace members; not a separate trust boundary.

## Roll-forward note: dependency mistatement

The ROADMAP currently says "Phase 75 depends on Phase 74." That's
incorrect — Phase 74 is the output renderer (`pkg/slack/mrkdwn.go`),
Phase 75 is the inbound file path. They touch different files,
different directions. Phase 75 only depends on Phase 67 (inbound flow)
and Phase 67.1 (ACK reaction). Fix the ROADMAP `Depends on:` line
during planning.

## Rollout

- Phase 75 already in roadmap; run `/gsd:plan-phase 75` to break down.
- Single PR. ~150 LoC bridge + ~80 LoC sandbox bash + ~250 LoC tests
  + small Terraform additions (IAM statement, lifecycle rule).
- 4-5 plans likely: bridge types/handler, file downloader, userdata
  poller changes, IAM/lifecycle/scope-check infra, docs+UAT.
