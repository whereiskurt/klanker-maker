---
phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels
verified: 2026-05-15T16:00:00Z
status: passed
score: 14/14 must-haves verified
re_verification: false
human_verification:
  - test: "Drag 26 files into a live #sb-{id} channel"
    expected: "Warning thread reply posted; first 25 files dispatched to Claude; Claude receives master-prompt wrapper listing 25 paths"
    why_human: "UAT step 9 was deferred to unit tests (TestFileDownloader_Over25Files_Truncated). Unit tests are substantive and pass, but live Slack behavior has not been manually confirmed against a deployed Lambda."
  - test: "Drag a >100 MB file into a live #sb-{id} channel"
    expected: "Warning thread reply posted; no S3 PUT attempted; Claude receives text-only prompt"
    why_human: "UAT step 10 was deferred to unit tests (TestFileDownloader_Over100MB_Dropped). Unit tests are substantive and pass, but live Slack behavior has not been manually confirmed."
  - test: "Drag a file with no caption (empty text) into a live #sb-{id} channel"
    expected: "Message admitted (not dropped by malformed guard); Claude prompt includes '[no text — file-only]' fallback text and the master-prompt wrapper"
    why_human: "UAT step 11 was deferred to TestUserdata_SlackInbound_AllowsEmptyTextWhenAttachments. Unit test passes, but live Slack delivery of a file-only message has not been manually confirmed end-to-end."
  - test: "Verify ROADMAP.md Phase 75 plan checkboxes are ticked and count reads 6/6"
    expected: "All six plan entries show [x] and 'Plans: 6/6 plans executed'"
    why_human: "ROADMAP.md still shows '5/6 plans executed' and all plan entries unchecked ([  ]) even after Plan 06 was executed and UAT passed. This is metadata — not a functional gap — but it should be reconciled."
  - test: "Verify REQUIREMENTS.md contains REQ-FILES-* entries"
    expected: "A '### Slack Inbound File Attachments (Phase 75)' section listing all nine REQ-FILES-* requirement IDs"
    why_human: "Plan 06 task list included 'Update REQUIREMENTS.md to add REQ-FILES-* entries (Phase: 75, Status: Complete)'. This task was not executed — REQUIREMENTS.md has no REQ-FILES-* entries. The requirement IDs used in plans (REQ-FILES-ALLOWLIST, REQ-FILES-SQS-PAYLOAD, REQ-FILES-DOWNLOAD, REQ-FILES-CAPS, REQ-FILES-S3-LAYOUT, REQ-FILES-FAILURE-WARNINGS, REQ-FILES-POLLER, REQ-FILES-WRAPPER-FORMAT, REQ-FILES-IAM-LIFECYCLE, REQ-FILES-SCOPE, REQ-FILES-DEPLOY) are orphaned — they exist only in the plan frontmatter and VALIDATION.md, not in the traceability register."
---

# Phase 75: Slack Inbound File Attachments Verification Report

**Phase Goal:** Enable bidirectional file attachments in per-sandbox Slack channels — users drag images/PDFs into a `#sb-{id}` channel, the bridge stages them to S3, the sandbox poller mirrors them to `/workspace/.km-slack/attachments/<thread_ts>/`, and Claude reads them with its Read tool. Caps: 25 files/msg, 100 MB/file.
**Verified:** 2026-05-15T16:00:00Z
**Status:** passed
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Bridge admits `file_share` subtype events instead of silently dropping them | VERIFIED | `isBotLoop` allow-list at `events_handler.go:350` adds `case "", "thread_broadcast", "file_share":`; `TestEventsHandler_FileShareSubtype_Allowed` passes |
| 2 | `slackMessageEvent` JSON unmarshal populates `Files []SlackFile` on `file_share` events | VERIFIED | `events_types.go:24` — `Files []SlackFile \`json:"files,omitempty"\``; `SlackFile` struct with `ID, Name, Mimetype, URLPrivateDownload, Size`; `TestSlackMessageEvent_FilesField_ParsesCorrectly` passes |
| 3 | `InboundQueueBody` carries `Attachments []Attachment` across bridge → SQS → poller; back-compat preserved | VERIFIED | `events_types.go:45` — `Attachments []Attachment \`json:"attachments,omitempty"\``; `omitempty` ensures absent key (not null) for older consumers using `jq .attachments[]?` |
| 4 | Bridge downloads files from files.slack.com with bot token, stages to S3, handles 302 redirects preserving Authorization header | VERIFIED | `file_downloader.go:234` — `downloadOneFile` with `ErrUseLastResponse` + manual re-issue; `TestFileDownloader_FilesSlackComRedirect_PreservesAuthHeader` passes |
| 5 | Stub Slack file objects (id-only, no `url_private_download`) are enriched via `files.info` before download | VERIFIED | `files_info.go` — `SlackFilesInfoAdapter` implements `FilesInfoFetcher`; `file_downloader.go:141-174` enriches stub files; `TestFileDownloader_EmptyURL_EnrichesViaFilesInfo` passes; hotfix 75.1 commit `9ee8c0c` |
| 6 | `file_share` handling is synchronous within Lambda handler (not goroutine) | VERIFIED | `events_handler.go:209-259` — synchronous `bgCtx := context.WithTimeout(ctx, 90s)`; `TestEventsHandler_WithFiles_Synchronous` verifies `Download` called before `Handle` returns; Lambda timeout bumped to 60s; hotfix 75.2 commit `3351bdf` |
| 7 | Per-file cap (100 MB) and per-message cap (25 files) enforced before HTTP calls | VERIFIED | `file_downloader.go:121-127` (25-file cap); `file_downloader.go:176-183` (100 MB cap); `TestFileDownloader_Over25Files_Truncated` and `TestFileDownloader_Over100MB_Dropped` pass |
| 8 | Thread-reply warnings posted for each failure class before agent sees message | VERIFIED | `events_handler.go:230-239` — posts `"Warning: "+fe.Reason` via `h.Slack.PostMessage` sorted by `OriginalName`; covered by `events_handler_test.go` handler tests |
| 9 | Sandbox poller mirrors S3 objects to `/workspace/.km-slack/attachments/<thread_ts>/`, chowned `sandbox:sandbox` | VERIFIED | `userdata.go:1415-1434` — `ATTACH_DIR`, `aws s3 cp`, `chown sandbox:sandbox`; `TestUserdata_SlackInbound_AttachmentMirrorBlock` passes |
| 10 | File-only uploads (empty text, non-empty attachments) admitted — malformed guard gated on `ATTACH_COUNT==0` | VERIFIED | `userdata.go:1374` — guard condition `{ [ -z "$TEXT" ] && [ "$ATTACH_COUNT" -eq 0 ]; }`; `TestUserdata_SlackInbound_AllowsEmptyTextWhenAttachments` passes |
| 11 | Master-prompt wrapper prepended when attachments present, with exact required phrasing | VERIFIED | `userdata.go:1438-1458` — "The user attached the following file(s)... Read them with your Read tool when relevant"; `[no text — file-only]` fallback; `TestUserdata_SlackInbound_MasterPromptWrapper` passes |
| 12 | `km-slack-inbound-poller.service` systemd unit includes `Environment=KM_ARTIFACTS_BUCKET=...` | VERIFIED | `userdata.go:1841` — `Environment=KM_ARTIFACTS_BUCKET={{ .KMArtifactsBucket }}`; `TestUserdata_SlackInboundPoller_KMArtifactsBucket_InSystemdUnit` passes; hotfix 75.3 commit `9c9fb6c` |
| 13 | `files:read` scope required by both `km slack init` and `km doctor` | VERIFIED | `slack.go:837` and `doctor_slack.go:484` both include `"files:read"` in required slices; `TestSlackInit_FilesReadScope_Required` and `TestDoctor_FilesReadScope_Missing_Reports` pass |
| 14 | UAT confirms end-to-end behavior for image (top-thread) and PDF (in-thread) | VERIFIED (with deferred items) | `75-06-SUMMARY.md` steps 7 and 8 both PASS after hotfixes; steps 9/10/11 deferred to unit tests (see Human Verification) |

**Score:** 14/14 truths verified (3 cap/edge-case scenarios deferred to unit tests; functionally covered)

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/slack/bridge/events_types.go` | `SlackFile` struct; `Attachments []Attachment` on `InboundQueueBody` | VERIFIED | 72 lines; `type SlackFile struct` at line 30; `Attachments []Attachment` at line 45 |
| `pkg/slack/bridge/events_types_test.go` | `TestSlackMessageEvent_FilesField_ParsesCorrectly` | VERIFIED | Test present at line 14; passes |
| `pkg/slack/bridge/events_handler.go` | `isBotLoop` admit `file_share`; `FileDownloader` field; files-fork in `Handle` | VERIFIED | `file_share` at line 350; `FileDownloader FileDownloader` at line 52; fork at line 208 |
| `pkg/slack/bridge/events_handler_test.go` | `TestEventsHandler_FileShareSubtype_Allowed`; `TestEventsHandler_WithFiles_Synchronous` | VERIFIED | Both present; note: Plan 02's `TestEventsHandler_WithFiles_FiresGoroutine_Returns200Fast` was intentionally renamed to `TestEventsHandler_WithFiles_Synchronous` by hotfix 75.2 — semantically equivalent |
| `pkg/slack/bridge/file_downloader.go` | `FileDownloader` interface; `S3FileDownloader`; `sanitizeFilename`; `FileError` | VERIFIED | 356 lines; all types present and substantive |
| `pkg/slack/bridge/file_downloader_test.go` | 9+ unit tests covering all failure classes | VERIFIED | 13 test functions present including all required cap/redirect/auth tests |
| `pkg/slack/bridge/files_info.go` | `SlackFilesInfoAdapter` implementing `FilesInfoFetcher` | VERIFIED | 97 lines; hotfix 75.1; `FilesInfo()` method calls `files.info` API |
| `pkg/compiler/userdata.go` | Attachment mirror block + wrapper + `ATTACH_COUNT`-gated malformed guard | VERIFIED | Lines 1369-1458; all three components present |
| `pkg/compiler/userdata_slack_inbound_test.go` | `TestUserdata_SlackInbound_AttachmentMirrorBlock`; `TestUserdata_SlackInbound_MasterPromptWrapper`; `TestUserdata_SlackInbound_AllowsEmptyTextWhenAttachments`; `TestUserdata_SlackInboundPoller_KMArtifactsBucket_InSystemdUnit` | VERIFIED | All four tests present and pass |
| `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` | `aws_iam_role_policy.slack_bridge_files_s3_write`; `memory_size = 1024`; `timeout = 60` | VERIFIED | IAM policy at line 241 scoped to `slack-inbound/*`; memory_size 1024 at line 305; timeout 60 at line 301 |
| `infra/modules/s3-artifacts-lifecycle/v1.0.0/main.tf` | 30-day lifecycle on `slack-inbound/` prefix | VERIFIED | `aws_s3_bucket_lifecycle_configuration` with `prefix = "slack-inbound/"` and `days = 30` |
| `infra/modules/s3-artifacts-lifecycle/v1.0.0/variables.tf` | `variable "bucket_name"` | VERIFIED | File exists with `bucket_name` variable |
| `infra/live/use1/s3-artifacts-lifecycle/terragrunt.hcl` | Wires module to regional artifacts bucket | VERIFIED | `source = ".../s3-artifacts-lifecycle/v1.0.0"`; `bucket_name = get_env("KM_ARTIFACTS_BUCKET", "")` |
| `internal/app/cmd/slack.go` | `files:read` in `VerifyEventsAPIScopes` required slice | VERIFIED | Line 837 |
| `internal/app/cmd/slack_test.go` | `TestSlackInit_FilesReadScope_Required` | VERIFIED | Line 914; passes |
| `internal/app/cmd/doctor_slack.go` | `files:read` in `checkSlackAppEventsScopes` required slice; success message updated | VERIFIED | Line 484; success message at line 507 lists `files:read` |
| `internal/app/cmd/doctor_slack_inbound_test.go` | `TestDoctor_FilesReadScope_Missing_Reports` | VERIFIED | Line 293; passes |
| `cmd/km-slack-bridge/main.go` | `S3FileDownloader` constructed at cold start; assigned to `EventsHandler.FileDownloader`; `FilesInfo` wired | VERIFIED | Lines 257-287; `S3FileDownloader{...FilesInfo: &bridge.SlackFilesInfoAdapter{...}}` |
| `docs/slack-notifications.md` | Phase 75 subsection with operator setup, troubleshooting table, spec link | VERIFIED | "## Slack inbound file attachments (Phase 75)" section at line 816; spec link present at line 915 |
| `CLAUDE.md` | Phase 75 paragraph with hotfix lessons; points at operator doc + spec | VERIFIED | Lines 316-378; "### Slack inbound file attachments (Phase 75)" and "### Phase 75 hotfix lessons" sections |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `events_handler.go` (Handle) | `file_downloader.go` (S3FileDownloader.Download) | `h.FileDownloader.Download(bgCtx, msg.Files, info.SandboxID, threadTS)` | WIRED | Line 226; conditional on `h.FileDownloader != nil && len(msg.Files) > 0` |
| `events_handler.go` (isBotLoop) | `file_share` allow-list | `case "", "thread_broadcast", "file_share":` | WIRED | Line 350 |
| `file_downloader.go` (downloadOneFile) | Redirect handling | `ErrUseLastResponse` + manual re-issue | WIRED | Lines 239-273; Pitfall 1 mitigation |
| `cmd/km-slack-bridge/main.go` | `bridge.S3FileDownloader` | `eventsHandler.FileDownloader = &bridge.S3FileDownloader{...}` | WIRED | Lines 269-279 |
| `cmd/km-slack-bridge/main.go` | `bridge.SlackFilesInfoAdapter` | `FilesInfo: &bridge.SlackFilesInfoAdapter{HTTPClient: httpClient, Tokens: tokenFetcher}` | WIRED | Lines 275-278 |
| `userdata.go` (poller bash) | `s3://$KM_ARTIFACTS_BUCKET/$S3_KEY` | `aws s3 cp "s3://$KM_ARTIFACTS_BUCKET/$S3_KEY" "$LOCAL_PATH"` | WIRED | Line 1428 |
| `userdata.go` (mirror block) | `claude -p` invocation | Mirror block at lines 1412-1435 BEFORE wrapper at 1437-1458 BEFORE `claude -p` at 1475 | WIRED | Correct bash control-flow ordering |
| `infra/live/use1/s3-artifacts-lifecycle/terragrunt.hcl` | `infra/modules/s3-artifacts-lifecycle/v1.0.0` | `source = "${local.repo_root}/infra/modules/s3-artifacts-lifecycle/v1.0.0"` | WIRED | Present in file |
| `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` | `slack-inbound/*` IAM | `Resource = "arn:aws:s3:::${var.artifacts_bucket}/slack-inbound/*"` at line 255 | WIRED | Scoped to prefix only (not bucket-wide); `gated on var.artifacts_bucket` per plan |
| `docs/slack-notifications.md` | spec file | `docs/superpowers/specs/2026-05-15-slack-inbound-file-attachments-design.md` | WIRED | Line 915; spec file exists |

---

### Requirements Coverage

The `REQ-FILES-*` requirement IDs referenced across the six plans were never added to `.planning/REQUIREMENTS.md`. This is an administrative gap from Plan 06's deferred task ("Update REQUIREMENTS.md to add REQ-FILES-* entries"). The IDs are defined in `75-VALIDATION.md` and the PLAN frontmatter only. Coverage is assessed against the VALIDATION.md definitions:

| Requirement ID | Source Plan(s) | Description (from VALIDATION.md) | Status | Evidence |
|---------------|----------------|----------------------------------|--------|----------|
| REQ-FILES-ALLOWLIST | 75-01 | `isBotLoop` allow-list adds `file_share` | SATISFIED | `events_handler.go:350`; test passes |
| REQ-FILES-SQS-PAYLOAD | 75-01 | `InboundQueueBody.Attachments []Attachment`; back-compat | SATISFIED | `events_types.go:45`; test passes |
| REQ-FILES-DOWNLOAD | 75-02, 75-05 | `S3FileDownloader` downloads with bot token + S3 stage; Pitfall 1/2 | SATISFIED | `file_downloader.go`; 13 tests pass |
| REQ-FILES-CAPS | 75-02 | 25-file/100MB caps enforced before HTTP; thread-reply warnings | SATISFIED | `file_downloader.go:121,176`; cap tests pass |
| REQ-FILES-S3-LAYOUT | 75-02 | Key format `slack-inbound/<sandbox>/<thread_ts>/<file_id>-<sanitized>`; filename sanitization | SATISFIED | `file_downloader.go:195`; sanitization tests pass |
| REQ-FILES-FAILURE-WARNINGS | 75-02 | All failure classes post thread-reply before agent dispatch | SATISFIED | `events_handler.go:230-239`; handler tests cover warning paths |
| REQ-FILES-POLLER | 75-03 | Poller bash parses `.attachments[]?`; S3 mirror; `chown sandbox` | SATISFIED | `userdata.go:1412-1434`; test passes |
| REQ-FILES-WRAPPER-FORMAT | 75-03 | Master-prompt wrapper with "Read them with your Read tool" phrasing; `[no text — file-only]` fallback | SATISFIED | `userdata.go:1437-1458`; test passes |
| REQ-FILES-IAM-LIFECYCLE | 75-04 | IAM `s3:PutObject` scoped to `slack-inbound/*`; memory_size 1024; S3 30-day lifecycle | SATISFIED | `main.tf:241,305`; `s3-artifacts-lifecycle/v1.0.0/main.tf` |
| REQ-FILES-SCOPE | 75-04 | `files:read` in both `km slack init` and `km doctor` scope checks | SATISFIED | `slack.go:837`, `doctor_slack.go:484`; tests pass |
| REQ-FILES-DEPLOY | 75-05, 75-06 | Cold-start wiring in `main.go`; docs + UAT | SATISFIED | `main.go:269-287`; docs present; UAT steps 7+8 PASS |

**Orphaned requirements:** All 11 REQ-FILES-* IDs are defined only in plan frontmatter and `75-VALIDATION.md`, not in `.planning/REQUIREMENTS.md`. The REQUIREMENTS.md register is missing a "### Slack Inbound File Attachments (Phase 75)" section. This is a documentation-only gap — no implementation is missing — but it breaks traceability for future audits.

---

### Anti-Patterns Found

| File | Pattern | Severity | Impact |
|------|---------|----------|--------|
| `.planning/ROADMAP.md:1627` | "Plans: 5/6 plans executed" — Plan 06 was executed but ROADMAP count not updated | Info | No functional impact; metadata staleness only |
| `.planning/ROADMAP.md:1630-1635` | All six plan checkboxes show `[ ]` (unchecked) | Info | No functional impact; ROADMAP tracking incomplete |
| `.planning/REQUIREMENTS.md` | REQ-FILES-* IDs not registered | Info | No functional impact; traceability gap |

No blocker or warning anti-patterns in implementation code. No TODO/FIXME/stub patterns found in any Phase 75 source files.

---

### Human Verification Required

#### 1. Cap tests — 26-file limit

**Test:** Drag 26 files simultaneously into a live `#sb-{id}` Slack channel.
**Expected:** Thread reply warning "Only first 25 of 26 files attached; rest skipped"; 👀 ACK reaction; 25 file paths listed in Claude's master-prompt wrapper; Claude's reply references file content.
**Why human:** UAT step 9 deferred to `TestFileDownloader_Over25Files_Truncated`. Unit test passes with mock transport; live Slack behavior (including Slack's own file count handling and the warning thread reply appearing in the channel UI) is unconfirmed.

#### 2. Cap tests — >100 MB file

**Test:** Drag a file larger than 100 MB into a live `#sb-{id}` channel.
**Expected:** Thread reply warning "Skipped <filename> (N MB > 100 MB cap)"; no 👀 ACK (S3 PUT never happens so SQS never written); no master-prompt wrapper for that file.
**Why human:** UAT step 10 deferred to `TestFileDownloader_Over100MB_Dropped`. Unit test passes. Live delivery behavior not confirmed.

#### 3. File-only upload (no caption)

**Test:** Drag a file with no accompanying text (empty message body) into a live `#sb-{id}` channel.
**Expected:** Message admitted (not dropped); Claude prompt includes `[no text — file-only]` placeholder and the master-prompt wrapper listing the file path; Claude reads the file and responds.
**Why human:** UAT step 11 deferred to `TestUserdata_SlackInbound_AllowsEmptyTextWhenAttachments`. Unit test passes. Slack's actual delivery of a file-only (zero-text) `file_share` event through the full pipeline (bridge → SQS → poller → claude) is unconfirmed.

#### 4. ROADMAP and REQUIREMENTS.md metadata reconciliation

**Test:** Update `.planning/ROADMAP.md` Phase 75 to mark 6/6 plans executed and tick all plan checkboxes. Add REQ-FILES-* entries to `.planning/REQUIREMENTS.md`.
**Expected:** ROADMAP shows "Plans: 6/6 plans executed" with all `[x]` checkboxes. REQUIREMENTS.md has a "### Slack Inbound File Attachments (Phase 75)" section with all 11 REQ-FILES-* IDs marked complete.
**Why human:** These are documentation edits that require human judgment about correct placement and wording. No code changes needed.

---

### Gaps Summary

No implementation gaps. All 14 observable truths are verified by code evidence. All key links are wired. All tests pass.

Two categories of minor follow-up exist, neither blocking the feature goal:

1. **Deferred UAT cap steps (9/10/11):** The unit tests covering these scenarios are substantive and pass. The deferral was a deliberate operator decision to keep iteration tight after three in-flight hotfixes. The risk is acceptably low given the unit test coverage. Re-testing live is recommended when the next deployment cycle occurs.

2. **ROADMAP/REQUIREMENTS metadata:** Plan 06 tasked the operator with updating ROADMAP.md plan checkboxes and adding REQ-FILES-* entries to REQUIREMENTS.md. Neither was done. This is a traceability gap only — no implementation is affected.

---

_Verified: 2026-05-15T16:00:00Z_
_Verifier: Claude (gsd-verifier)_
