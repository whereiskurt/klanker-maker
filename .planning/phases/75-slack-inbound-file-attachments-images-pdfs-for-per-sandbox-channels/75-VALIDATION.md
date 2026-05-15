---
phase: 75
slug: slack-inbound-file-attachments
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-15
---

# Phase 75 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Derived from `75-RESEARCH.md` § Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go stdlib `testing` + `net/http/httptest` + `testify` (already in `go.mod`) |
| **Config file** | `go.mod` only — no special config |
| **Quick run command** | `go test -count=1 -race -run '<TestName>' ./pkg/slack/bridge/ ./pkg/compiler/ ./internal/app/cmd/` |
| **Full suite command** | `go test ./... -count=1 -race -timeout 2m` |
| **Estimated runtime** | ~10s for focused; ~90s for full suite |

---

## Sampling Rate

- **After every task commit:** focused test on the new function/test — `go test -count=1 -race -run '<NewTest>' ./pkg/slack/bridge/` (~1s)
- **After every plan wave:** `go test -count=1 -race ./pkg/slack/bridge/ ./pkg/compiler/ ./internal/app/cmd/` (~10s)
- **Before `/gsd:verify-work`:** `go test ./... -count=1` must be green, PLUS manual UAT (image drag + PDF drag in live `#sb-{id}`)
- **Max feedback latency:** ~10s for inner loop, ~90s for full suite

---

## Phase Requirements

| Req ID | Description |
|--------|-------------|
| **REQ-FILES-ALLOWLIST** | `pkg/slack/bridge/events_handler.go:265` `isBotLoop` allow-list adds `"file_share"`; existing handler test asserting file_share drops is removed and replaced with a positive admission test. |
| **REQ-FILES-DOWNLOAD** | New `S3FileDownloader` in `pkg/slack/bridge/file_downloader.go`. Downloads from `url_private_download` with bot token + S3 stage. Bridge `Handle` forks on `len(Files) > 0` and fires fire-and-forget goroutine that returns 200 within ~100ms. Survives Slack file-CDN 302 redirects with Authorization header preserved (Pitfall 1: Go stdlib strips auth on redirect; mitigation via `CheckRedirect = ErrUseLastResponse` + manual re-issue). |
| **REQ-FILES-CAPS** | 25-file/100MB-per-file caps enforced in `S3FileDownloader`. Over-cap files dropped; bridge posts thread-reply warning via existing `SlackPosterAdapter.PostMessage` before dispatching turn with what succeeded. |
| **REQ-FILES-S3-LAYOUT** | S3 key format `slack-inbound/<sandbox-id>/<thread_ts>/<file_id>-<sanitized_name>` with filename sanitization stripping `/`, `\`, `..`, `\0`, non-printable bytes and 255-byte truncation. |
| **REQ-FILES-IAM-LIFECYCLE** | `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` gains `s3:PutObject` on `arn:aws:s3:::${bucket}/slack-inbound/*`. New S3 lifecycle rule (30-day expiration on `slack-inbound/` prefix) added via new `infra/modules/s3-artifacts-lifecycle/v1.0.0/` module (artifacts bucket is created imperatively at `bootstrap.go:870-895`; no existing module owner). |
| **REQ-FILES-SQS-PAYLOAD** | `InboundQueueBody` gains `Attachments []Attachment` field; new `Attachment` struct `{S3Key, OriginalName, Mimetype}`. Back-compat preserved: older bridges with empty field don't break existing parsing. |
| **REQ-FILES-POLLER** | `pkg/compiler/userdata.go:1259-1499` extension — bash block that parses `.attachments[]?` from SQS body, `aws s3 cp` each to `/workspace/.km-slack/attachments/<thread_ts>/<file_id>-<sanitized_name>`, `chown sandbox:sandbox`. Existing malformed-message guard (`userdata.go:1369-1376`) gated on `ATTACH_COUNT == 0` so file-only uploads (empty `text`) are NOT dropped (Pitfall 4 regression). |
| **REQ-FILES-WRAPPER-FORMAT** | Natural-language master-prompt wrapper prepended to `claude -p` prompt file only when `len(attachments) > 0`: "The user attached the following file(s)... Read them with your Read tool when relevant... `User's message: <text or '[no text — file-only]'>`". |
| **REQ-FILES-FAILURE-WARNINGS** | Each failure class (per-file download fail, all-fail, S3-put-fail, 403-auth-fail, panic) posts a thread-reply warning before agent reply. |
| **REQ-FILES-SCOPE** | `internal/app/cmd/slack.go:836` `VerifyEventsAPIScopes` and `internal/app/cmd/doctor_slack.go:484` `checkSlackAppEventsScopes` both gain `"files:read"` in their `required` slices. Doctor success message at `doctor_slack.go:507` updated to reflect new scope. |
| **REQ-FILES-DEPLOY** | Full `make build && km init` operator path (NOT `km init --lambdas` — see `project_km_init_lambdas_doesnt_deploy` memory). Bridge zip + IAM + lifecycle all applied. Lambda `memory_size` bumped 256 → 1024 to accommodate 100MB in-memory buffering (AWS SDK PutObject requires re-readable body for retries — Pitfall 2). Operator UAT: drag image + drag PDF in live `#sb-{id}` channel, confirm 👀 + Claude reply describes content. |

---

## Per-Task Verification Map

*Per-task IDs (e.g. `75-01-01`) will be assigned by `gsd-planner`.
This table maps each requirement to the test that verifies it.*

| Req ID | Test | Test Type | Automated Command | File Exists | Status |
|--------|------|-----------|-------------------|-------------|--------|
| REQ-FILES-ALLOWLIST | `TestEventsHandler_FileShareSubtype_Allowed` (also: existing `subtype_file_share` row at `events_handler_test.go:257` must be REMOVED) | unit | `go test -count=1 -race -run 'TestEventsHandler_FileShareSubtype' ./pkg/slack/bridge/` | ❌ W0 — modify existing `events_handler_test.go` (remove drop-case row + add positive admission test mirroring `TestEventsHandler_ThreadBroadcastPasses` at line 291) | ⬜ pending |
| REQ-FILES-DOWNLOAD (happy path) | `TestFileDownloader_HappyPath` | unit (recordingTransport + mock S3) | `go test -count=1 -race -run 'TestFileDownloader_HappyPath' ./pkg/slack/bridge/` | ❌ W0 — NEW `pkg/slack/bridge/file_downloader_test.go` | ⬜ pending |
| REQ-FILES-DOWNLOAD (goroutine timing) | `TestEventsHandler_WithFiles_FiresGoroutine_Returns200Fast` | unit (mocked slow downloader, assert <100ms) | `go test -count=1 -race -run 'TestEventsHandler_WithFiles_FiresGoroutine' ./pkg/slack/bridge/` | ❌ W0 — new test in existing `events_handler_test.go` | ⬜ pending |
| REQ-FILES-DOWNLOAD (redirect auth preservation) | `TestFileDownloader_FilesSlackComRedirect_PreservesAuthHeader` (Pitfall 1 regression) | unit | `go test -count=1 -race -run 'TestFileDownloader_FilesSlackCom' ./pkg/slack/bridge/` | ❌ W0 — `file_downloader_test.go` | ⬜ pending |
| REQ-FILES-CAPS (>100MB) | `TestFileDownloader_Over100MB_Dropped` | unit | `go test -count=1 -race -run 'TestFileDownloader_Over100MB' ./pkg/slack/bridge/` | ❌ W0 | ⬜ pending |
| REQ-FILES-CAPS (>25 files) | `TestFileDownloader_Over25Files_Truncated` | unit | `go test -count=1 -race -run 'TestFileDownloader_Over25Files' ./pkg/slack/bridge/` | ❌ W0 | ⬜ pending |
| REQ-FILES-S3-LAYOUT (filename sanitize) | `TestFileDownloader_FilenameSanitization` (table-driven, ~15 cases) | unit | `go test -count=1 -race -run 'TestFileDownloader_FilenameSanitization' ./pkg/slack/bridge/` | ❌ W0 | ⬜ pending |
| REQ-FILES-S3-LAYOUT (key format) | Asserted inside `TestFileDownloader_HappyPath` via mock `S3PutObjectAPI` capture | unit | included above | ❌ W0 | ⬜ pending |
| REQ-FILES-IAM-LIFECYCLE (IAM) | Bridge IAM policy includes `s3:PutObject` on `slack-inbound/*` | terraform plan diff (manual UAT) | `cd infra/live/use1/lambda-slack-bridge && terragrunt plan` → expect new policy attachment | ⚠️ UAT-only — no automated Terraform plan-diff test in this repo | ⬜ pending |
| REQ-FILES-IAM-LIFECYCLE (lifecycle) | S3 lifecycle rule on `slack-inbound/` prefix, 30-day expiration | terraform plan diff (manual UAT) | `cd infra/live/use1/s3-artifacts-lifecycle && terragrunt plan` → expect lifecycle config | ⚠️ UAT-only | ⬜ pending |
| REQ-FILES-SQS-PAYLOAD | `TestSlackMessageEvent_FilesField_ParsesCorrectly` + back-compat: existing `TestEventsHandler_ValidMessage_HappyPath` continues passing | unit | `go test -count=1 -race -run 'TestSlackMessageEvent_FilesField' ./pkg/slack/bridge/` | ❌ W0 — NEW `pkg/slack/bridge/events_types_test.go` | ⬜ pending |
| REQ-FILES-POLLER (mirror bash) | `TestUserdata_SlackInbound_AttachmentMirrorBlock` | unit (template render assertion) | `go test -count=1 -race -run 'TestUserdata_SlackInbound_AttachmentMirror' ./pkg/compiler/` | ❌ W0 — new test in existing `userdata_slack_inbound_test.go` | ⬜ pending |
| REQ-FILES-POLLER (empty-text guard fix) | `TestUserdata_SlackInbound_AllowsEmptyTextWhenAttachments` (Pitfall 4 regression) | unit | `go test -count=1 -race -run 'TestUserdata_SlackInbound_AllowsEmptyText' ./pkg/compiler/` | ❌ W0 — new test in `userdata_slack_inbound_test.go` | ⬜ pending |
| REQ-FILES-WRAPPER-FORMAT | `TestUserdata_SlackInbound_MasterPromptWrapper` | unit | `go test -count=1 -race -run 'TestUserdata_SlackInbound_MasterPromptWrapper' ./pkg/compiler/` | ❌ W0 | ⬜ pending |
| REQ-FILES-FAILURE-WARNINGS (per-file fail) | `TestFileDownloader_DownloadFails_Continues` | unit | `go test -count=1 -race -run 'TestFileDownloader_DownloadFails' ./pkg/slack/bridge/` | ❌ W0 | ⬜ pending |
| REQ-FILES-FAILURE-WARNINGS (all fail) | `TestFileDownloader_AllFail_ReturnsEmpty` | unit | `go test -count=1 -race -run 'TestFileDownloader_AllFail' ./pkg/slack/bridge/` | ❌ W0 | ⬜ pending |
| REQ-FILES-FAILURE-WARNINGS (S3 fail) | `TestFileDownloader_S3PutFails_TreatedAsDownloadFail` | unit | `go test -count=1 -race -run 'TestFileDownloader_S3PutFails' ./pkg/slack/bridge/` | ❌ W0 | ⬜ pending |
| REQ-FILES-FAILURE-WARNINGS (403 auth) | `TestFileDownloader_403_LogsErrorAndDrops` | unit (captured logger) | `go test -count=1 -race -run 'TestFileDownloader_403' ./pkg/slack/bridge/` | ❌ W0 | ⬜ pending |
| REQ-FILES-SCOPE (init) | `TestSlackInit_FilesReadScope_Required` | unit | `go test -count=1 -race -run 'TestSlackInit_FilesReadScope' ./internal/app/cmd/` | ❌ W0 — new test in existing `slack_test.go` | ⬜ pending |
| REQ-FILES-SCOPE (doctor) | `TestDoctor_FilesReadScope_Missing_Reports` (mirrors existing `TestDoctor_SlackInboundEventsSubscription_MissingReactionsWrite` at `doctor_slack_inbound_test.go:193`) | unit | `go test -count=1 -race -run 'TestDoctor_FilesReadScope' ./internal/app/cmd/` | ❌ W0 — new test in existing `doctor_slack_inbound_test.go` | ⬜ pending |
| REQ-FILES-DEPLOY (build) | `make build` succeeds; km binary + bridge zip ldflags-stamped | runtime smoke | `make build && ls -la build/km-slack-bridge.zip` | ✅ exists | ⬜ pending |
| REQ-FILES-DEPLOY (UAT) | Drag image into `#sb-{id}` → 👀 in <2s → Claude describes image. Drag PDF → Claude reads PDF. Drag 26 files → warning + first-25 dispatched. | manual UAT | live Slack channel + sandbox | ⚠️ Manual gate | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/slack/bridge/file_downloader_test.go` — NEW file (~280 LoC) with 8 tests covering REQ-FILES-DOWNLOAD, REQ-FILES-CAPS, REQ-FILES-S3-LAYOUT, REQ-FILES-FAILURE-WARNINGS. Reuses existing `recordingTransport`, `canned()`, `captureBridgeLogger` fixtures from `aws_adapters_test.go`.
- [ ] `pkg/slack/bridge/events_types_test.go` — NEW file (~50 LoC) with `TestSlackMessageEvent_FilesField_ParsesCorrectly` for REQ-FILES-SQS-PAYLOAD.
- [ ] `pkg/slack/bridge/events_handler_test.go` — MODIFY existing: (1) remove `subtype_file_share` drop-case row at line 257; (2) add positive `TestEventsHandler_FileShareSubtype_Allowed` mirroring `TestEventsHandler_ThreadBroadcastPasses` at line 291; (3) add `TestEventsHandler_WithFiles_FiresGoroutine_Returns200Fast` (mock slow downloader, assert <100ms).
- [ ] `pkg/compiler/userdata_slack_inbound_test.go` — MODIFY existing: add 3 new tests (`TestUserdata_SlackInbound_AttachmentMirrorBlock`, `TestUserdata_SlackInbound_MasterPromptWrapper`, `TestUserdata_SlackInbound_AllowsEmptyTextWhenAttachments`). Reuses existing `extractSlackInboundPoller` helper.
- [ ] `internal/app/cmd/slack_test.go` — MODIFY existing: add `TestSlackInit_FilesReadScope_Required`.
- [ ] `internal/app/cmd/doctor_slack_inbound_test.go` — MODIFY existing: add `TestDoctor_FilesReadScope_Missing_Reports`.
- [x] **No framework install** — Go testing + testify already present.
- [x] **No new test fixtures** — all helpers (recordingTransport, canned, captureBridgeLogger, extractSlackInboundPoller) already exist.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Bridge IAM policy contains `s3:PutObject` on `slack-inbound/*` after deploy | REQ-FILES-IAM-LIFECYCLE | Requires AWS Terraform plan/apply | `cd infra/live/use1/lambda-slack-bridge && terragrunt plan` → expect new IAM statement. After apply: `aws iam get-role-policy --role-name {prefix}-slack-bridge --policy-name slack_bridge_files_s3_write` |
| S3 lifecycle rule on `slack-inbound/` prefix | REQ-FILES-IAM-LIFECYCLE | Requires Terraform apply | `aws s3api get-bucket-lifecycle-configuration --bucket $KM_ARTIFACTS_BUCKET` → expect 30d-Expiration rule with `Prefix: slack-inbound/` |
| End-to-end inbound file flow (image) | REQ-FILES-DEPLOY | Live Slack + live AWS required | Re-install Slack app with `files:read` scope → `km slack rotate-token --bot-token <new>` → `make build && km init` → drag screenshot into `#sb-{sandbox-id}` channel with comment "describe this image" → confirm 👀 within ~1s → Claude reply describes image content |
| End-to-end inbound file flow (PDF) | REQ-FILES-DEPLOY | Same as above | Drag a PDF into channel with comment "what's in this document?" → confirm Claude reads PDF content |
| Cap warning (26 files at once) | REQ-FILES-CAPS | Live Slack | Drag 26 files at once → expect thread-reply `⚠️ Only first 25 of 26 files attached; rest skipped` before agent reply |
| Master-prompt wrapper does not appear in text-only turns | REQ-FILES-WRAPPER-FORMAT | Live Slack | Post text-only message in `#sb-{sandbox-id}` → confirm agent reply does NOT mention attachments and no wrapper string leaks |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies, OR are explicit manual UAT gates
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 10s for inner loop, < 90s for full suite
- [ ] `nyquist_compliant: true` set in frontmatter (gsd-planner / gsd-plan-checker flip after plans pass verification)

**Approval:** pending
