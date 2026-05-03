---
phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
plan: 08
subsystem: slack
tags: [slack, lambda, s3, file-upload, ed25519, scope-probe, streaming]

# Dependency graph
requires:
  - phase: 68
    provides: Plan 04 pkg/slack.Client.UploadFile (3-step Slack file upload, streaming)
  - phase: 68
    provides: Plan 02 SlackEnvelope upload fields (s3_key, filename, content_type, size_bytes)
  - phase: 68
    provides: Plan 00 Wave-0 stubs (9 upload_handler_test + 1 main_upload_test)
  - phase: 67
    provides: bridge.Handler post/archive/test dispatcher, SSMBotTokenFetcher, SlackPosterAdapter
provides:
  - bridge.Handler ActionUpload dispatch case with 5-rule validation (scope, prefix, filename, content-type, size, channel)
  - bridge.S3ObjectGetter + bridge.SlackFileUploader interfaces (handler dependency injection)
  - bridge.S3GetterAdapter + bridge.SlackFileUploaderAdapter (production wiring)
  - cmd/km-slack-bridge probeFilesWriteScope cold-start helper (RESEARCH OQ 2 resolution)
  - 9 PASSing upload validation tests + 1 PASSing routing test + 1 PASSing scope-probe test (3 sub-cases)
affects: [68-09, 68-11, 68-12]

# Tech tracking
tech-stack:
  added: [aws-sdk-go-v2/service/s3 (bridge package — was previously cmd/km only)]
  patterns:
    - "Cold-start raw HTTP probe for one-shot Slack scope inspection (avoids extending Phase 63 SlackPosterAdapter.call to capture response headers)"
    - "Validation order: cold-start gate → cheap envelope checks → AWS calls (fail-fast before any I/O)"
    - "Streaming S3 → Slack via S3ObjectGetter.GetObject(io.ReadCloser) → SlackFileUploader.UploadFile(io.Reader); zero buffering in Lambda memory"
    - "Adapter pattern keeps Plan 04 pkg/slack.Client.UploadFile API stable while exposing it through the bridge's narrow SlackFileUploader interface"

key-files:
  created: []
  modified:
    - "pkg/slack/bridge/interfaces.go — adds S3ObjectGetter + SlackFileUploader interfaces (io import)"
    - "pkg/slack/bridge/handler.go — Handler struct gains S3Getter/FileUploader/MissingFilesWrite; allowed-actions check admits ActionUpload; ~110 lines of new dispatch case + validation"
    - "pkg/slack/bridge/aws_adapters.go — adds S3GetterAdapter + SlackFileUploaderAdapter (s3 + pkgslack imports)"
    - "pkg/slack/bridge/upload_handler_test.go — 9 stubs replaced; mockS3Getter/mockUploader; reuses bridge_test fakes from handler_test.go"
    - "cmd/km-slack-bridge/main.go — wires S3 + uploader adapters, runs probeFilesWriteScope at cold start, KM_ARTIFACTS_BUCKET in env-var doc"
    - "cmd/km-slack-bridge/main_upload_test.go — TestActionUploadRouting (1 case) + TestProbeFilesWriteScope (3 sub-cases)"

key-decisions:
  - "Raw HTTP probe for X-OAuth-Scopes (RESEARCH OQ 2): dedicated 5s-timeout HTTP client at cold start hits POST https://slack.com/api/auth.test, captures X-OAuth-Scopes response header, caches MissingFilesWrite for the Lambda's lifetime. Avoids extending Phase 63 SlackPosterAdapter.call() to expose response headers."
  - "Fail-open on probe failure: empty X-OAuth-Scopes header or transport error → MissingFilesWrite=false. Per-request Slack response surfaces the real error rather than blocking all uploads on a flaky probe."
  - "ChannelEmpty surfaces missing_fields (early envelope check), not channel_empty: the upload-specific channel_empty branch in the dispatch case is defense-in-depth and unreachable through Handle's public entry — documented in test comment with explicit assertion of 'missing_fields'."
  - "Cold-start token fetch is best-effort: on failure, log a warning and skip both the FileUploader adapter and the scope probe rather than log.Fatalf. Phase 63 paths (post/archive/test) keep working; the upload path returns bot_token_unavailable through the existing Step 7 of Handle."
  - "RESEARCH OQ 4 (provisioned concurrency) closed without changes: scope probe is one-shot at cold start (~50ms), runs in init() outside the request hot path. No cold-start budget impact requiring provisioned concurrency."
  - "VERSION bumped to 0.2.486 by `make build` (per feedback_rebuild_km): bridge Lambda zip will be repackaged with embedded version ldflags; operator deploys via `km init --sidecars`."

patterns-established:
  - "Cold-start scope probe pattern: raw HTTP one-shot at init time, captures Slack response headers that the production HTTP path (SlackPosterAdapter) doesn't surface. Reusable for future scope checks (Plan 11 doctor slack_files_write_scope duplicates this exact probe)."
  - "Streaming bridge dispatch: S3 GetObject → io.ReadCloser → io.Reader → Slack 3-step upload, with defer body.Close() and zero in-memory buffering. Sustains 100MB cap on 256MB Lambda."
  - "Three-stage validation order in handler dispatch: (1) cold-start cached gates, (2) envelope-level cheap checks, (3) AWS-side calls. Tests assert mocks NOT called when earlier stages fail."

requirements-completed: []

# Metrics
duration: 8 min
completed: 2026-05-03
---

# Phase 68 Plan 08: Bridge ActionUpload Handler Summary

**Bridge Lambda streams transcripts from S3 → Slack via 3-step file upload with cold-start files:write scope probe, 5-rule validation, and 13 PASSing tests**

## Performance

- **Duration:** 8 min
- **Started:** 2026-05-03T20:23:59Z
- **Completed:** 2026-05-03T20:32:10Z
- **Tasks:** 5
- **Files modified:** 6

## Accomplishments

- Bridge handler now accepts `ActionUpload` envelopes alongside post/archive/test, with full 5-rule validation (s3_key prefix, filename, content-type, size cap, channel) executed in fail-fast order before any AWS work.
- Cold-start `probeFilesWriteScope` queries `auth.test` once and caches `MissingFilesWrite`. When the bot is missing `files:write`, every upload returns 400 with `"bot lacks files:write — operator must re-auth Slack App"` instead of failing inside Slack with a confusing error code.
- `S3GetterAdapter` + `SlackFileUploaderAdapter` connect the bridge to `KM_ARTIFACTS_BUCKET` and Plan 04's `pkg/slack.Client.UploadFile` via dependency injection — handler unit tests use in-memory mocks; production wiring happens once at cold start in `main.go`.
- 13 tests pass: 9 ActionUpload validation cases (handler-level), 1 routing case (Lambda Function URL → bridge.Handler), 3 scope-probe sub-cases (httptest.Server with X-OAuth-Scopes header).
- `make build` repackaged the km binary with embedded `v0.2.486` ldflags (per `feedback_rebuild_km`); the bridge Lambda zip is now ready for `km init --sidecars` deploy.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add S3ObjectGetter + SlackFileUploader interfaces; extend Handler struct** — `6ff77d1` (feat)
2. **Task 2: Implement ActionUpload dispatcher case + validation + S3-stream-to-Slack flow** — `b1699bf` (feat)
3. **Task 3: Add S3GetterAdapter + SlackFileUploaderAdapter implementations** — `a207c71` (feat)
4. **Task 4: Replace 9 Wave 0 stubs with full ActionUpload validation suite** — `e91ab5f` (test)
5. **Task 5: Cold-start scope check in cmd/km-slack-bridge/main.go (RESEARCH OQ 2)** — `9d85be0` (feat — bundles main.go + test + VERSION bump)

**Plan metadata commit:** to be created after this SUMMARY is staged.

## Files Created/Modified

- `pkg/slack/bridge/interfaces.go` — adds `S3ObjectGetter` + `SlackFileUploader` interfaces (`io.ReadCloser` body return for streaming).
- `pkg/slack/bridge/handler.go` — `Handler` struct gains `S3Getter`, `FileUploader`, `MissingFilesWrite` fields; allowed-actions admits `ActionUpload`; new dispatch case enforces scope-gate → prefix → filename → content-type → size → channel → S3 GetObject → FileUploader.UploadFile → 200 with `{ok, file_id, permalink}`.
- `pkg/slack/bridge/aws_adapters.go` — `S3GetterAdapter` (wraps `S3GetObjectAPI`, adds `KM_ARTIFACTS_BUCKET`) + `SlackFileUploaderAdapter` (wraps `*pkgslack.Client`, unwraps `UploadFileResult`).
- `pkg/slack/bridge/upload_handler_test.go` — 9 stubs replaced; `mockS3Getter`/`mockUploader` track `.called` so tests assert mocks NOT invoked when validation fails first.
- `cmd/km-slack-bridge/main.go` — `init()` wires S3 + uploader adapters and runs `probeFilesWriteScope` after token fetch; `KM_ARTIFACTS_BUCKET` documented in package env-var list; `probeFilesWriteScope` helper added at file end.
- `cmd/km-slack-bridge/main_upload_test.go` — `TestActionUploadRouting` (signs an upload envelope, asserts the Lambda Function URL handler hits the new dispatch case via `MissingFilesWrite=true` short-circuit) + `TestProbeFilesWriteScope` (3 sub-cases against httptest.Server: scope present, scope missing, empty header fail-open).
- `VERSION` — `0.2.485` → `0.2.486` (auto-bumped by `make build`).

## Decisions Made

- **Raw HTTP probe (RESEARCH OQ 2):** Chose a dedicated 5s-timeout HTTP probe at cold start over extending `SlackPosterAdapter.call()` to surface the `X-OAuth-Scopes` response header. Keeps Phase 63's adapter API stable; the probe is one function isolated to `main.go` rather than rippling header-capture into every Slack call.
- **Fail-open on probe failure:** Network blip or empty `X-OAuth-Scopes` header → `MissingFilesWrite=false`. The per-request Slack response then surfaces the real error. Hard-failing on a flaky probe would block all uploads and turn a transient infrastructure issue into a production outage.
- **Best-effort token fetch:** When `tokenFetcher.Fetch(ctx)` fails at cold start (e.g. transient SSM unavailability), log warn and skip both the FileUploader adapter wiring AND the scope probe — but do NOT `log.Fatalf`. Phase 63 paths must keep working; upload requests return `bot_token_unavailable` through Step 7 of `Handle`, matching the existing pattern.
- **`ChannelEmpty` test asserts `missing_fields`, not `channel_empty`:** The upload-specific `channel_empty` branch in the dispatch case is defense-in-depth — unreachable through `Handle`'s public entry because the early envelope-level check fires first when `Channel == ""`. Documented in the test comment with explicit assertion to make the layered behavior visible.
- **`pkg/slack.Client` token baked at cold start:** `SlackFileUploaderAdapter` wraps `pkgslack.NewClient(token, httpClient)` constructed once in `init()`. Token rotation requires a Lambda cold start — which `km slack rotate-token` already triggers. This matches `SlackPosterAdapter`'s 15-min token cache window without adding a per-request token-injection layer.

## Deviations from Plan

None - plan executed exactly as written.

The plan's verification commands all passed without modification:

- `go build ./...` — clean throughout all 5 tasks.
- `go test ./pkg/slack/bridge/... -count=1 -run TestHandler_ActionUpload -v` — 9 PASS (matches the awk verification floor).
- `go test ./cmd/km-slack-bridge/... -count=1 -run TestActionUploadRouting -v` — 1 PASS.
- `make build` — built v0.2.486 with `-X version.GitCommit=e91ab5f`.
- Full bridge + slack + km-slack-bridge test suites green (~25 pre-existing tests + 13 new) with no regressions.

The single sub-test addition (`TestProbeFilesWriteScope` with 3 sub-cases) is in scope per the plan's `<action>` step 6 — exercises the probe helper independently of the routing test, which gave high signal during Task 5 implementation.

## Issues Encountered

- A second executor was running Plan 68-09 in parallel on the same branch (commits `83d574f`, `4a02a09` interleaved between my Task 3 and Task 4 commits). Per the concurrency note in the prompt, this is expected — I staged files explicitly by path for every commit (never `git add .`) so there was no cross-plan contamination. Verified all 5 of my Task commits (`6ff77d1`, `b1699bf`, `a207c71`, `e91ab5f`, `9d85be0`) landed cleanly with only the files declared in this plan's `files_modified` allow-list (plus `VERSION`, which is a `make build` side effect explicitly required by the plan's verification step).

## RESEARCH Open Questions Resolved

- **OQ 2 — How does the bridge detect missing `files:write` scope?** Resolved by `probeFilesWriteScope` in `cmd/km-slack-bridge/main.go`: cold-start raw HTTP probe to `https://slack.com/api/auth.test`, captures `X-OAuth-Scopes` response header, caches `Handler.MissingFilesWrite` for the Lambda's lifetime. Plan 11's `doctor slack_files_write_scope` check will reuse the same probe shape.
- **OQ 4 — Does the cold-start probe require provisioned concurrency?** Resolved without changes: the probe is one-shot in `init()` (off the request hot path), runs in <100ms with a 5s timeout cap, and the result is cached for the Lambda's warm lifetime. No measurable impact on cold-start latency budget.

## Next Phase Readiness

- Plan 09 (sandbox-side `km-slack upload`) can now signal envelopes through `BuildEnvelopeUpload` and expect the bridge to validate, stream from S3, and post to Slack. The Wave 4 integration test (Plan 12) verifies end-to-end.
- Plan 11 (`km doctor` adds `slack_files_write_scope`) can copy `probeFilesWriteScope` verbatim — the helper is intentionally side-effect-free and dependency-light.
- Bridge zip needs `km init --sidecars` deploy to land on production. `KM_ARTIFACTS_BUCKET` env var must be present in the Lambda's terragrunt config (already set per Phase 12 S3 replication) — Plan 11's doctor check should also assert it.

## Self-Check: PASSED

Verified all SUMMARY claims:

- `pkg/slack/bridge/interfaces.go` — modified, contains `S3ObjectGetter` + `SlackFileUploader`.
- `pkg/slack/bridge/handler.go` — modified, contains `MissingFilesWrite` + `ActionUpload` + `s3_key_prefix_mismatch` + `filename_invalid` + `size_invalid`.
- `pkg/slack/bridge/aws_adapters.go` — modified, contains `S3GetterAdapter` + `SlackFileUploaderAdapter`.
- `pkg/slack/bridge/upload_handler_test.go` — 9 PASS confirmed via `grep -cE "^--- PASS"`.
- `cmd/km-slack-bridge/main.go` — modified, contains `MissingFilesWrite` + `X-OAuth-Scopes`.
- `cmd/km-slack-bridge/main_upload_test.go` — `TestActionUploadRouting` PASS + `TestProbeFilesWriteScope` 3 sub-cases PASS.
- `git log --oneline | grep "68-08"` returns 5 commits matching all task hashes above.

---
*Phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload*
*Completed: 2026-05-03*
