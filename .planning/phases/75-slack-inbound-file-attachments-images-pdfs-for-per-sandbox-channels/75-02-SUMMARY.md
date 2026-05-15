---
phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels
plan: 02
subsystem: slack
tags: [slack, s3, file-download, bridge, sqs, goroutine, tdd]

# Dependency graph
requires:
  - phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels
    provides: SlackFile/Attachment types + isBotLoop file_share allow-list (Plan 01)

provides:
  - S3FileDownloader adapter implementing FileDownloader interface
  - S3PutObjectAPI narrow interface (mirrors S3GetObjectAPI from Phase 68)
  - FileError type for per-file failure tracking and thread-reply warnings
  - sanitizeFilename helper with 255-byte rune-aligned truncation
  - EventsHandler.FileDownloader field + files-fork goroutine in Handle
  - EventsHandler.Slack field for posting thread-reply warnings from goroutine
  - 9 unit tests for S3FileDownloader (happy path, redirect, caps, sanitization)
  - 1 goroutine-timing test asserting Handle returns <200ms with 2s mock downloader

affects: [75-05-main-wiring, 75-04-iam-memory]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - S3FileDownloader: narrow-interface adapter shape (mirrors S3GetterAdapter from Phase 68)
    - Fire-and-forget goroutine (mirrors Phase 67.1 reactor goroutine pattern)
    - CheckRedirect=ErrUseLastResponse + manual redirect re-issue (Pitfall 1 mitigation)
    - io.ReadAll → bytes.NewReader for S3 PutObject body (Pitfall 2 mitigation)
    - Package-level bridge.logger for structured logs captured in tests via SetLogger

key-files:
  created:
    - pkg/slack/bridge/file_downloader.go
    - pkg/slack/bridge/file_downloader_test.go
  modified:
    - pkg/slack/bridge/events_handler.go
    - pkg/slack/bridge/events_handler_test.go

key-decisions:
  - "Pitfall 1 mitigation: HTTPClient.CheckRedirect=ErrUseLastResponse + manual single-hop redirect re-issue with Bearer token header preserved"
  - "Pitfall 2 mitigation: io.ReadAll into []byte buffer before bytes.NewReader for S3 PutObject.Body (SDK retries need re-readable io.Reader)"
  - "SanitizeFilenameForTest exported shim gives external test package access to unexported sanitizeFilename without breaking encapsulation"
  - "FileDownloader field on EventsHandler is nullable — nil means feature off (back-compat for pre-Phase-75 Lambda images)"
  - "fire-and-forget goroutine uses context.WithTimeout(context.Background(), 90s); never the request ctx that Lambda may cancel after 200 response"
  - "Function-level error from Download is nil even on per-file failures; failures accumulate in []FileError (CONTEXT.md mandated: continue with what succeeded)"

patterns-established:
  - "Narrow-interface adapter: S3PutObjectAPI mirrors S3GetObjectAPI for consistency"
  - "Test helpers reused from aws_adapters_test.go: recordingTransport, canned(), captureBridgeLogger()"
  - "Goroutine panic recovery: recover() → log Error + post thread-reply via Slack if wired"

requirements-completed:
  - REQ-FILES-DOWNLOAD
  - REQ-FILES-CAPS
  - REQ-FILES-S3-LAYOUT
  - REQ-FILES-FAILURE-WARNINGS

# Metrics
duration: 8min
completed: 2026-05-15
---

# Phase 75 Plan 02: S3FileDownloader + EventsHandler files-fork Summary

**S3FileDownloader adapter with 9 unit tests + EventsHandler fire-and-forget goroutine for sub-100ms file_share ack**

## Performance

- **Duration:** 8 min
- **Started:** 2026-05-15T15:01:47Z
- **Completed:** 2026-05-15T15:09:55Z
- **Tasks:** 2 (TDD: RED→GREEN for each)
- **Files modified:** 4

## Accomplishments
- S3FileDownloader: downloads Slack files with Bearer token, handles cross-host 302 redirects (Pitfall 1), buffers body before S3 PutObject (Pitfall 2), enforces 25-file count cap and 100 MB per-file cap, produces FileError records for every failure class
- sanitizeFilename: rune-aware, 255-byte truncation, replaces `..`/`/`/`\` with `_`, strips non-printable bytes, defensive `_` for empty/unsafe names
- EventsHandler.Handle forks on `h.FileDownloader != nil && len(msg.Files) > 0`: files-present path uses 90s-budget goroutine, files-empty path unchanged (synchronous SQS write preserved bit-for-bit)
- 10 new tests: 9 FileDownloader unit tests + 1 goroutine-timing test; all existing bridge tests continue green

## Task Commits

Each task committed atomically:

1. **Task 02-01: S3FileDownloader + interfaces + sanitizer with 9 unit tests** - `9a875a1` (feat)
2. **Task 02-02: Fork EventsHandler.Handle on len(Files)>0 + goroutine-timing test** - `26c2dfe` (feat)

**Plan metadata:** committed with state update (docs)

## Files Created/Modified
- `pkg/slack/bridge/file_downloader.go` — FileDownloader interface, S3FileDownloader struct, S3PutObjectAPI interface, FileError type, sanitizeFilename helper, SanitizeFilenameForTest shim, MaxFilesPerMessage/MaxFileSizeBytes/DownloadTimeoutTotal constants
- `pkg/slack/bridge/file_downloader_test.go` — 9 unit tests covering all failure classes (happy path, Pitfall 1 redirect, over-100MB, over-25-files, partial failure, all-fail, S3-put fail, 403, sanitization table)
- `pkg/slack/bridge/events_handler.go` — FileDownloader FileDownloader + Slack SlackPoster fields added; Handle fork with fire-and-forget goroutine; sort import added
- `pkg/slack/bridge/events_handler_test.go` — fakeSlackPoster mock, slowDownloader mock, TestEventsHandler_WithFiles_FiresGoroutine_Returns200Fast

## Decisions Made
- Used `bridge.logger` (package-level slog logger set by SetLogger) instead of `slog.Error` directly, so captureBridgeLogger tests can capture downloader log lines
- Mock S3Put records calls before returning error so test can assert `len(s3mock.calls) == 1` even on S3 failure
- Kept `SanitizeFilenameForTest` as a thin export shim rather than making sanitizeFilename exported, preserving Go naming convention that lowercase = package-internal

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Mock S3Put was recording calls only on success path**
- **Found during:** Task 02-01 (TestFileDownloader_S3PutFails_TreatedAsDownloadFail)
- **Issue:** mockS3Put returned error before recording the call; test asserted 1 call and got 0
- **Fix:** Moved call recording before the error return in mockS3Put.PutObject
- **Files modified:** pkg/slack/bridge/file_downloader_test.go
- **Committed in:** 9a875a1 (Task 02-01 commit)

**2. [Rule 1 - Bug] file_downloader.go used slog.Error instead of bridge.logger**
- **Found during:** Task 02-01 (TestFileDownloader_403_LogsErrorAndDrops)
- **Issue:** captureBridgeLogger captures bridge.logger (package-level), not slog.Default(); 403 log wasn't captured
- **Fix:** Replaced slog.Error/slog.Warn calls with logger.Error/logger.Warn (package-level variable)
- **Files modified:** pkg/slack/bridge/file_downloader.go
- **Committed in:** 9a875a1 (Task 02-01 commit)

**3. [Rule 1 - Bug] events_handler.go: two len(msg.Files)>0 matches instead of required 1**
- **Found during:** Task 02-02 (post-implementation verification check)
- **Issue:** Plan verification check mandated exactly 1 grep match; an extra warn-log branch added a second
- **Fix:** Removed the superfluous nil-downloader warn log from the else branch
- **Files modified:** pkg/slack/bridge/events_handler.go
- **Committed in:** 26c2dfe (Task 02-02 commit)

---

**Total deviations:** 3 auto-fixed (all Rule 1 bugs found during TDD iterations)
**Impact on plan:** All fixes necessary for correctness and test assertions. No scope creep.

## Issues Encountered
- `atomic` import cleanup: initial test draft included `sync/atomic` for an unused variable; cleaned up immediately before GREEN run

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Plan 02 bridge changes are complete; Plan 04 (IAM + Lambda memory bump) and Plan 05 (main.go wiring of S3FileDownloader into EventsHandler) are required before live deployment
- The `S3FileDownloader` will be constructed in main.go (Plan 05) with an `*s3.Client` and `CheckRedirect=ErrUseLastResponse` policy on the http.Client

## Failure-Handling Matrix Coverage (CONTEXT.md)

| Failure class | Test | Status |
|---|---|---|
| Per-file oversize (>100 MB) | TestFileDownloader_Over100MB_Dropped | Covered |
| Count cap (>25 files) | TestFileDownloader_Over25Files_Truncated | Covered |
| Per-file download fail (non-200) | TestFileDownloader_DownloadFails_Continues | Covered |
| All files fail | TestFileDownloader_AllFail_ReturnsEmpty | Covered |
| S3 PutObject fail | TestFileDownloader_S3PutFails_TreatedAsDownloadFail | Covered |
| 403 auth fail (files:read scope) | TestFileDownloader_403_LogsErrorAndDrops | Covered |
| Cross-host 302 redirect (Pitfall 1) | TestFileDownloader_FilesSlackComRedirect_PreservesAuthHeader | Covered |

---
*Phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels*
*Completed: 2026-05-15*
