---
phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
plan: 04
subsystem: slack
tags: [slack, files-upload-external, streaming, httptest, tdd, ed25519]

requires:
  - phase: 68-00
    provides: 7 Wave-0 t.Skip stubs in pkg/slack/client_upload_test.go
  - phase: 68-02
    provides: SlackEnvelope ActionUpload variants (transcript metadata fields the upload path eventually carries)

provides:
  - "pkg/slack.Client.UploadFile method implementing Slack's 3-step files.getUploadURLExternal + completeUploadExternal flow"
  - "UploadFileResult{FileID, Permalink} return type"
  - "7 PASSing httptest.Server-backed tests covering happy path, per-step failures, streaming pass-through, and omit-empty thread_ts"

affects:
  - 68-08-bridge-handler  # bridge Lambda calls Client.UploadFile when handling action="upload"
  - 68-05-km-slack-cli    # sandbox-side km-slack will sign envelopes and POST to bridge (does NOT call UploadFile directly, but its envelope shape needs to match what the bridge feeds to UploadFile)

tech-stack:
  added: []
  patterns:
    - "io.Reader streaming with explicit http.Request.ContentLength (avoids chunked transfer-encoding which Slack rejects on signed upload URLs)"
    - "Single httptest.Server hosts all 3 Slack endpoints (upload_url is rewritten to point back at the same server's /upload path)"
    - "Per-step error wrapping (each return path identifies which Slack call failed)"

key-files:
  created: []
  modified:
    - pkg/slack/client.go
    - pkg/slack/client_upload_test.go

key-decisions:
  - "UploadFile does NOT retry internally — retry/backoff stays at the BridgeBackoff envelope layer, matching the existing PostToBridge pattern (one nonce per attempt, no replayed_nonce masking)"
  - "Step 1 uses application/x-www-form-urlencoded (Slack's documented content-type for files.getUploadURLExternal); Step 3 uses application/json (matches existing callJSON helper but bypasses it because the response shape carries a files[] array not in SlackAPIResponse)"
  - "thread_ts key is omitted from Step 3 JSON when empty (Slack rejects empty-string thread_ts) — verified by sub-test that decodes the captured request body"
  - "Streaming is proven by hashing 1 MiB of payload bytes server-side and comparing to source SHA-256 (no buffering or corruption between caller's io.Reader and the wire)"

patterns-established:
  - "Pattern: Bridge-internal Slack API calls return wrapped errors naming the failed step (\"slack: files.getUploadURLExternal: <code>\") so the bridge log line carries enough context to operators without parsing"
  - "Pattern: httptest stub multiplexes all 3 upload endpoints by URL path, capturing Content-Length, body hash, and JSON payloads as side-effects for assertion"

requirements-completed: []

duration: 3min
completed: 2026-05-03
---

# Phase 68 Plan 04: UploadFile (Slack 3-step file upload) Summary

**pkg/slack.Client gains UploadFile — io.Reader-streamed Slack files.getUploadURLExternal + completeUploadExternal flow with explicit Content-Length framing and per-step error wrapping; 7 Wave-0 stubs promoted to PASS via single multiplexed httptest.Server.**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-05-03T20:04:52Z
- **Completed:** 2026-05-03T20:07:17Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- `Client.UploadFile(ctx, channel, threadTS, filename, contentType, sizeBytes, body) → (*UploadFileResult, error)` lands in `pkg/slack/client.go` (126 LOC).
- Streaming guarantee: the io.Reader body is passed straight to `http.NewRequestWithContext`; `req.ContentLength = sizeBytes` forces non-chunked framing that Slack accepts.
- Per-step error messages identify which Slack call failed (`slack: files.getUploadURLExternal: <code>` / `slack: PUT upload: status %d` / `slack: files.completeUploadExternal: <code>`).
- 7 PASSing tests covering: HappyPath (call order + IDs), Step1Failure, Step2NetworkFailure (500), Step2ChunkedRejected (Content-Length asserted), Step3Failure, StreamingPassThrough (1 MiB SHA-256 round-trip), OmitEmptyThreadTS (subtests for both branches).
- `go test ./pkg/slack/...` runs clean (zero regressions on the existing 14 client/payload tests).

## Task Commits

Each task was committed atomically:

1. **Task 1: Implement Client.UploadFile (3-step streaming flow)** — `e20cf03` (feat)
2. **Task 2: Replace 7 Wave 0 stubs with httptest-backed tests** — `ac707f4` (test)

**Plan metadata:** _pending_ (final commit appended below by docs commit)

## Files Created/Modified

- `pkg/slack/client.go` — added `UploadFileResult` struct + `UploadFile` method; new imports `net/url`, `strconv`, `strings` (`bytes`/`io`/`encoding/json` already present).
- `pkg/slack/client_upload_test.go` — replaced 7 `t.Skip()` stubs with 303 LOC of httptest-backed test bodies sharing a single `uploadStub` helper that multiplexes all 3 Slack endpoints.

## Decisions Made

- **No internal retry inside UploadFile.** Matches the explicit must-have ("retry policy used at the km-slack call site, NOT inside UploadFile"). Avoids the PostToBridge replayed-nonce masking class of bug.
- **Single httptest.Server for all 3 endpoints.** Rather than spinning up two servers (Slack API + signed upload host), the stub rewrites Step 1's `upload_url` response to `http://r.Host/upload` so the same server handles the PUT. Lets one helper assert call order + capture all per-step state.
- **Hash-comparison for streaming proof.** Plan called out that we cannot directly assert "no chunked encoding"; we test the observable consequence (Content-Length header equals sizeBytes) AND a 1 MiB SHA-256 round-trip that catches any silent buffering/truncation.
- **`OmitEmptyThreadTS` as a 2-subtest table.** Asserts both branches (omit when "" / forward when set) in one test name to keep the 7-test count stable.

## Deviations from Plan

None — plan executed exactly as written. The skeleton in the plan's `<action>` was followed almost verbatim. One micro-deviation worth noting (not a Rule-N fix, just a hygiene call):

- The plan's skeleton used `step3JSON, _ := json.Marshal(step3Body)` (errcheck-discarded). Final code wraps the marshal error with `fmt.Errorf("slack: files.completeUploadExternal: marshal: %w", err)` for parity with the rest of the function's error-wrapping discipline. Zero behavior change (json.Marshal of map[string]any with string/string-slice values cannot fail in practice), but keeps the codepath uniform.

## Issues Encountered

None. Initial implementation compiled clean on first try; all 7 tests passed first run.

## User Setup Required

None — bridge-internal change. Plan 08 will wire UploadFile into the Lambda handler (no operator action until that ships and a fresh `make build && km init --lambdas` rolls out the bridge image).

## Next Phase Readiness

- **Plan 68-08 (bridge handler) is unblocked.** The handler can now call `c.UploadFile(ctx, env.Channel, env.ThreadTS, env.Filename, env.ContentType, env.SizeBytes, base64.NewDecoder(...))` (or equivalent) when handling `action: "upload"` envelopes.
- **Plan 68-05 (km-slack CLI) does NOT consume UploadFile directly.** It signs an envelope and POSTs to the bridge — the contract Plan 68-08 honors. No coupling created here.
- **Existing pkg/slack tests untouched and passing.** Phase 63/67 Slack code paths (PostMessage, PostToBridge, channel ops) are unaffected.

## Verification

- `go build ./...` — clean
- `go test ./pkg/slack/... -count=1 -run TestUploadFile -v` — 7 PASS, 0 FAIL, 0 SKIP
- `grep -c "ContentLength = sizeBytes" pkg/slack/client.go` — 1
- `grep -c "files.getUploadURLExternal" pkg/slack/client.go` — 8 (URL build, doc reference, multiple error wraps)
- `grep -c "files.completeUploadExternal" pkg/slack/client.go` — 8 (URL build, doc reference, multiple error wraps)
- Full slack package suite (`go test ./pkg/slack/...`) — PASS (no regressions on existing 14 tests)

## Self-Check: PASSED

- Files exist:
  - `/Users/khundeck/working/klankrmkr/pkg/slack/client.go` — FOUND (UploadFile method present)
  - `/Users/khundeck/working/klankrmkr/pkg/slack/client_upload_test.go` — FOUND (real test bodies)
- Commits exist:
  - `e20cf03` — FOUND (Task 1: feat UploadFile)
  - `ac707f4` — FOUND (Task 2: test 7 PASS)

---
*Phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload*
*Completed: 2026-05-03*
