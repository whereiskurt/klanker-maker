---
phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels
plan: 05
subsystem: infra
tags: [slack, lambda, s3, file-downloader, cold-start, wiring]

# Dependency graph
requires:
  - phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels
    plan: 02
    provides: "S3FileDownloader implementation + FileDownloader interface on EventsHandler"
  - phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels
    plan: 04
    provides: "IAM S3 write permission + 1024MB Lambda memory for file download runtime"
provides:
  - "S3FileDownloader wired at Lambda cold start in cmd/km-slack-bridge/main.go"
  - "CheckRedirect=ErrUseLastResponse on shared httpClient for Authorization header preservation across cross-host 302 redirects"
  - "Conditional construction: downloader built only when KM_ARTIFACTS_BUCKET is non-empty (nil = text-only fallback)"
  - "eventsHandler.Slack = poster wired for per-file warning thread replies"
  - "Bridge zip rebuilt cleanly for linux/arm64"
affects:
  - "75-06 (deployment UAT: this build artifact is what km init deploys)"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Redirect-disabled shared httpClient: single http.Client with CheckRedirect=ErrUseLastResponse shared across all adapters (Poster/Reactor/Downloader); safe because Slack API endpoints never 302"
    - "Conditional feature wiring: nil FileDownloader = feature off; non-nil = feature on; guarded by KM_ARTIFACTS_BUCKET check"
    - "Shared resource reuse: same s3Client, tokenFetcher, and httpClient instances reused — no duplicate SSM or S3 client construction"

key-files:
  created: []
  modified:
    - cmd/km-slack-bridge/main.go

key-decisions:
  - "Shared httpClient with CheckRedirect=ErrUseLastResponse: Slack API methods (chat.postMessage, reactions.add, auth.test) have no redirects, so disabling auto-redirect globally is safe — simplifies code, avoids two http.Client instances"
  - "KM_ARTIFACTS_BUCKET nil guard: preserve graceful degradation for installs without bucket configured; Warn log on empty bucket path"
  - "eventsHandler.Slack = poster: wired alongside FileDownloader so warning thread replies use the same already-constructed SlackPosterAdapter"

patterns-established:
  - "All Phase 75 adapters share tokenFetcher, httpClient, and s3Client initialized earlier in init(); no new AWS clients constructed"

requirements-completed:
  - REQ-FILES-DOWNLOAD
  - REQ-FILES-DEPLOY

# Metrics
duration: 8min
completed: 2026-05-15
---

# Phase 75 Plan 05: Slack Inbound File Attachments — Lambda Cold Start Wiring Summary

**S3FileDownloader wired into km-slack-bridge Lambda cold start with shared redirect-disabled httpClient; bridge zip rebuilt cleanly for linux/arm64**

## Performance

- **Duration:** 8 min
- **Started:** 2026-05-15T15:15:00Z
- **Completed:** 2026-05-15T15:23:00Z
- **Tasks:** 1
- **Files modified:** 2 (main.go + VERSION bump from make build)

## Accomplishments
- Added `CheckRedirect: func(...) error { return http.ErrUseLastResponse }` to the shared `httpClient` so S3FileDownloader can manually preserve the `Authorization` header on cross-host 302 redirects
- Constructed `bridge.S3FileDownloader{HTTPClient, S3, Bucket, Tokens}` conditionally on `KM_ARTIFACTS_BUCKET != ""` and assigned to `eventsHandler.FileDownloader`
- Wired `eventsHandler.Slack = poster` for per-file download warning thread replies
- `go vet ./cmd/km-slack-bridge/` clean; `go test ./pkg/slack/bridge/` green; bridge zip rebuilt for linux/arm64

## Task Commits

Each task was committed atomically:

1. **Task 05-01: Wire S3FileDownloader at Lambda cold start + configure redirect policy** - `22c4868` (feat)

**Plan metadata:** (pending)

## Files Created/Modified
- `cmd/km-slack-bridge/main.go` — Added CheckRedirect to httpClient; S3FileDownloader construction; eventsHandler.FileDownloader and Slack wiring

## Decisions Made
- Shared httpClient: Rather than a second dedicated http.Client for the downloader, the existing client (now with redirect disabled) is shared with Poster/Reactor. Slack API calls don't redirect, so this is safe and avoids redundant instances.
- Conditional construction guard: `if artifactsBucket != ""` block matches Phase 68's pattern for the same env var, keeping consistent fail-open behavior.
- eventsHandler.Slack added in this plan (not in Plan 02 struct init) because the `poster` variable is only available in main.go's cold-start scope.

## Deviations from Plan

None — plan executed exactly as written. The `eventsHandler.Slack = poster` assignment was implicitly required by the EventsHandler struct definition (Plan 02 added the field) but not explicitly called out in the task text; it was added as the correct completion of the wiring.

## Issues Encountered
None.

## User Setup Required
None — deployment happens in Plan 06 via `km init`.

## Next Phase Readiness
- Bridge zip built and ready for deployment
- Plan 06 (UAT) runs `km init` to deploy the updated Lambda
- The nil-guard means existing deployed Lambdas without the Phase 75 IAM grants degrade gracefully (text-only dispatch) rather than panicking

---
*Phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels*
*Completed: 2026-05-15*
