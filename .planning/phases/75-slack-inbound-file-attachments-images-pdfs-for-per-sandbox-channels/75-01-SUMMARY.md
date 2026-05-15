---
phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels
plan: 01
subsystem: slack
tags: [slack, bridge, sqs, go, file-attachments, events-api]

requires:
  - phase: 67-slack-inbound
    provides: SQS FIFO queue, InboundQueueBody struct, isBotLoop allow-list, km-slack-inbound-poller
  - phase: 67.1-ack-reaction
    provides: Reactor interface, fire-and-forget goroutine pattern in EventsHandler

provides:
  - SlackFile struct with ID/Name/Mimetype/URLPrivateDownload/Size fields
  - Attachment struct with S3Key/OriginalName/Mimetype fields
  - slackMessageEvent.Files []SlackFile field (json:"files,omitempty")
  - InboundQueueBody.Attachments []Attachment field (json:"attachments,omitempty")
  - isBotLoop allow-list extended with "file_share" subtype
  - TestSlackMessageEvent_FilesField_ParsesCorrectly (4 sub-cases)
  - TestEventsHandler_FileShareSubtype_Allowed (positive gate test)

affects:
  - 75-02: S3FileDownloader can now assert against SlackFile and Files[]
  - 75-03: Sandbox poller can now reference Attachment struct in SQS body
  - 75-04: IAM + scope checks need the new types as context
  - 75-05: Bridge cold-start wiring uses these types in main.go

tech-stack:
  added: []
  patterns:
    - "omitempty on Attachments field ensures absent key (not null) for back-compat with older SQS consumers"
    - "isBotLoop uses allow-list (not deny-list) — file_share added additive to the case clause"

key-files:
  created:
    - pkg/slack/bridge/events_types_test.go
  modified:
    - pkg/slack/bridge/events_types.go
    - pkg/slack/bridge/events_handler.go
    - pkg/slack/bridge/events_handler_test.go

key-decisions:
  - "json:\"files,omitempty\" on slackMessageEvent.Files — additive, no-op for all existing Phase 67 paths"
  - "json:\"attachments,omitempty\" on InboundQueueBody.Attachments — omitempty is load-bearing for back-compat (absent vs null)"
  - "isBotLoop case clause extended to include file_share — allow-list semantics preserved"
  - "ROADMAP was already in correct state (Phase 67, Phase 67.1 dependency + 6 plans) — Task 01-03 was a no-op verification"

patterns-established:
  - "Phase 75 struct naming: SlackFile (bridge-side Slack shape) vs Attachment (SQS payload shape) — two distinct layers"

requirements-completed:
  - REQ-FILES-ALLOWLIST
  - REQ-FILES-SQS-PAYLOAD

duration: 3min
completed: 2026-05-15
---

# Phase 75 Plan 01: SQS Payload Types + isBotLoop Allow-list + ROADMAP Fix Summary

**SlackFile/Attachment structs added to bridge type system and file_share subtype admitted through isBotLoop allow-list — Wave 1 gate that unblocks Plans 02-05**

## Performance

- **Duration:** 3 min
- **Started:** 2026-05-15T14:55:46Z
- **Completed:** 2026-05-15T14:58:41Z
- **Tasks:** 3 (2 TDD + 1 ROADMAP verify)
- **Files modified:** 4

## Accomplishments

- Added `SlackFile` struct and `Files []SlackFile` field to `slackMessageEvent` — bridge now captures Slack file metadata from `file_share` events instead of discarding it
- Added `Attachment` struct and `Attachments []Attachment` field to `InboundQueueBody` — SQS payload now carries per-file records for the sandbox poller (Plans 02+03 can assert against this)
- Extended `isBotLoop` allow-list with `"file_share"` case — user file uploads now flow past the subtype gate to SQS dispatch
- Added 5 new tests covering type round-trips and the file_share positive path; removed the now-incorrect `subtype_file_share` drop-case row from `TestEventsHandler_BotSelfMessageFiltered`

## Task Commits

Each task was committed atomically:

1. **Task 01-01: Add SlackFile and Attachment structs + Files/Attachments fields** - `7b72aa8` (feat)
2. **Task 01-02: Extend isBotLoop allow-list to admit file_share + flip drop-case test** - `89d7ba4` (feat)
3. **Task 01-03: Fix ROADMAP Phase 75 dependency line** — no-op; ROADMAP was already correct (Phase 67, Phase 67.1 + 6 plans already in place from planning phase)

**Plan metadata:** (final docs commit below)

_Note: TDD tasks have two logical phases (RED then GREEN) committed atomically per task_

## Files Created/Modified

- `pkg/slack/bridge/events_types.go` — Added `SlackFile` struct, `Attachment` struct, `Files []SlackFile` field on `slackMessageEvent`, `Attachments []Attachment` field on `InboundQueueBody`
- `pkg/slack/bridge/events_types_test.go` — New file: `TestSlackMessageEvent_FilesField_ParsesCorrectly` with 4 sub-cases (files populated, no-files back-compat, Attachment round-trip, nil-attachments omitempty)
- `pkg/slack/bridge/events_handler.go` — Extended `isBotLoop` allow-list case clause to include `"file_share"`
- `pkg/slack/bridge/events_handler_test.go` — Added `TestEventsHandler_FileShareSubtype_Allowed`; removed `subtype_file_share` drop-case row from `TestEventsHandler_BotSelfMessageFiltered`

## Decisions Made

- `omitempty` on `Attachments []Attachment` is load-bearing for back-compat: older sandbox pollers use `jq '.attachments[]?'` — the `?` operator returns empty for absent keys but may error on `null`. `omitempty` ensures the key is absent (not null) when no attachments are present.
- `SlackFile` and `Attachment` are deliberately separate types: `SlackFile` mirrors the Slack Events API shape (bridge-side); `Attachment` is the SQS payload schema (poller-side). This two-layer separation prevents Slack API shape changes from leaking into the SQS contract.
- ROADMAP Task 01-03 was a no-op: the planning phase (`gsd:plan-phase`) had already written the correct dependency line and 6-plan list before this execution began. No file change needed.

## Deviations from Plan

None - plan executed exactly as written. ROADMAP was already in desired state (pre-updated by the planning phase); Task 01-03 verified and confirmed correct rather than modifying.

## Issues Encountered

None

## User Setup Required

None - no external service configuration required. Bridge-only type changes; no Lambda deploy needed until Plans 02+05 complete.

## Next Phase Readiness

- Plans 02, 03, 04 can all start in parallel (Wave 2) — every type and gate they need now exists:
  - Plan 02 (S3FileDownloader) can import `SlackFile` and assert against `Files []SlackFile`
  - Plan 03 (sandbox poller) can reference `Attachment.S3Key`/`.OriginalName`/`.Mimetype`
  - Plan 04 (IAM + S3 lifecycle) needs no bridge types; can proceed independently
- Intermediate state is intentionally non-functional end-to-end: `file_share` events now reach SQS with `Attachments: nil` (no downloader yet); sandbox poller's `.attachments[]?` returns empty

---
*Phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels*
*Completed: 2026-05-15*
