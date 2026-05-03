---
phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
plan: 11
subsystem: doctor
tags: [doctor, slack, transcript-streaming, dynamodb, s3, oauth-scopes, observability]

# Dependency graph
requires:
  - phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
    provides: GetSlackStreamMessagesTableName helper (Plan 68-03), Plan 67-08 doctor patterns to mirror, Plan 68-08 bridge ActionUpload that motivates the files:write scope check
provides:
  - Three new km doctor checks for Phase 68 transcript-streaming health (slack_transcript_table_exists, slack_files_write_scope, slack_transcript_stale_objects)
  - Operator-facing visibility on whether the stream-messages DDB table is provisioned
  - Operator-facing visibility on whether the Slack bot has files:write scope (required for transcript upload via bridge ActionUpload)
  - Cleanup advisory for orphan transcripts/{sandbox-id}/ S3 prefixes left behind by destroyed sandboxes
affects: [Plan 68-12 (UAT), Phase 68 close-out]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Phase 67 doctor closure-based dep injection extended to transcript-streaming health: nil deps -> SKIPPED; never CheckError so transcript health issues never fail km doctor"
    - "Cross-plan getScopes callback reuse: checkSlackFilesWriteScope shares the existing SlackAuthTestScopes closure (production wired in initRealDepsWithExisting) with Phase 67 checkSlackAppEventsScopes"
    - "S3 ListObjectsV2 with Delimiter='/' Prefix='transcripts/' to enumerate sandbox-id sub-prefixes for orphan detection (set difference against live DDB sandbox IDs)"

key-files:
  created:
    - internal/app/cmd/doctor_slack_transcript.go
  modified:
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go
    - internal/app/cmd/doctor_slack_transcript_test.go
    - .planning/phases/68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload/deferred-items.md

key-decisions:
  - "Reused the existing Phase 67 SlackAuthTestScopes closure for files:write scope detection rather than spinning up a separate raw HTTP probe — gives the new check identical caching/auth-handling semantics as checkSlackAppEventsScopes and avoids duplicating fetchSlackBotScopes wiring"
  - "Sandbox-ID list for stale-object detection sourced from the existing SandboxLister (DDB-backed via doctorSandboxLister) instead of a fresh DDB Scan — keeps the doctor surface narrow and reuses the cached lister already attached to DoctorDeps"
  - "checkSlackTranscriptStaleObjects always returns CheckWarn (never CheckError) for orphans — cleanup is advisory, not a failure mode (Phase 68 is opt-in and orphans are inherent in destroy lifecycles)"
  - "Adapted the plan's Doctor-struct pseudo-code to the actual codebase pattern (closure-based dep injection in DoctorDeps); plan-checker flagged SlackBaseURL and Doctor as not present on the existing struct — confirmed and pivoted to closure shape mirroring Phase 67 doctor_slack.go"

patterns-established:
  - "Pattern: Phase 68 transcript-streaming health checks always demote CheckError to CheckWarn at registration time so opt-in transcript features cannot break km doctor for non-opted-in deployments"
  - "Pattern: New DoctorConfigProvider methods (e.g. GetSlackStreamMessagesTableName) require updating all test config stubs (testConfig, testDoctorConfig) — the embedded doctorStaleAMIConfig inherits via composition"

requirements-completed: []

# Metrics
duration: 7min
completed: 2026-05-03
---

# Phase 68 Plan 11: km doctor checks for Slack transcript streaming Summary

**Three new km doctor checks (slack_transcript_table_exists, slack_files_write_scope, slack_transcript_stale_objects) following the Phase 67 closure-based dep pattern, plus 12 mock-backed tests promoted from 5 Wave-0 stubs.**

## Performance

- **Duration:** 7 min
- **Started:** 2026-05-03T20:39:37Z
- **Completed:** 2026-05-03T20:46:41Z
- **Tasks:** 3
- **Files modified:** 4 (1 created + 3 edited)

## Accomplishments

- Three Phase 68 transcript-streaming doctor checks registered alongside the existing Phase 67 inbound checks; healthy state -> all OK; degraded state -> WARN with actionable remediation (run `km init` for missing table, OAuth/reinstall for missing scope, `aws s3 rm` for stale prefixes).
- Five Wave-0 t.Skip stubs in `doctor_slack_transcript_test.go` replaced with 12 mock-backed table-driven tests (5 original names + 7 added coverage cases). All 12 PASS; full Phase 67 inbound suite still green (no regression — the new files:write check shares the existing `getScopes` callback that the inbound suite drives).
- DoctorConfigProvider extended with `GetSlackStreamMessagesTableName()` and DoctorDeps extended with `SlackTranscriptS3` (`kmaws.S3ListAPI`) and `SlackListSandboxIDs` (`func(ctx) ([]string, error)`); production wiring lives in `initRealDepsWithExisting`.

## Task Commits

Each task was committed atomically:

1. **Task 1: Implement three doctor check functions** — `500650e` (feat)
2. **Task 2: Register checks in doctor.go + DI plumbing** — `0fee099` (feat)
3. **Task 3: Promote 5 Wave-0 stubs to mock-backed tests** — `e056ac3` (test)

**Plan metadata:** _(commit pending — final docs commit lands after this SUMMARY is created)_

## Files Created/Modified

- `internal/app/cmd/doctor_slack_transcript.go` (created, 252 lines) — three check functions: `checkSlackTranscriptTableExists`, `checkSlackFilesWriteScope`, `checkSlackTranscriptStaleObjects`. Closure-based dep injection mirroring `internal/app/cmd/doctor_slack.go`.
- `internal/app/cmd/doctor.go` (modified, +69 lines) — extended `DoctorConfigProvider` with `GetSlackStreamMessagesTableName()`; added `SlackTranscriptS3` and `SlackListSandboxIDs` fields to `DoctorDeps`; registered the three new checks in `assembleChecks` (each demotes `CheckError` to `CheckWarn`); wired production deps in `initRealDepsWithExisting` (S3 client typed as `kmaws.S3ListAPI`, sandbox-ID lister sourced from the existing `SandboxLister`).
- `internal/app/cmd/doctor_test.go` (modified, +6 lines) — added `GetSlackStreamMessagesTableName()` to `testConfig` and `testDoctorConfig` stubs to satisfy the extended `DoctorConfigProvider` interface (`doctorStaleAMIConfig` inherits via embedding).
- `internal/app/cmd/doctor_slack_transcript_test.go` (rewritten, 256 lines added / 9 lines deleted) — five `t.Skip` stubs replaced with 12 mock-backed tests using a local `fakeS3List` (satisfies `kmaws.S3ListAPI`) and the existing `mockDynamoClient` from `doctor_test.go`.
- `.planning/phases/68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload/deferred-items.md` (modified) — Plan 68-11 entry documenting the transient cross-plan compile conflict with Plan 68-10 (resolved during execution).

## Decisions Made

- **Closure-based deps over Doctor struct.** The plan's pseudo-code referenced a `Doctor` struct with `SlackBaseURL`, `DDB`, `S3`, `SandboxIDListFn` fields. The plan-checker flagged this for verification, and inspection of the existing codebase confirmed the actual pattern (Phase 67 `doctor_slack.go`) is closure-based dep injection where each check function takes its concrete dependencies as positional parameters. Adapted the new functions to the existing pattern for consistency and to avoid introducing a parallel architecture.
- **Reuse the Phase 67 `SlackAuthTestScopes` closure for files:write detection.** Rather than creating a separate raw HTTP probe (per the plan's pseudo-code), the new `checkSlackFilesWriteScope` accepts the same `func(ctx) ([]string, error)` callback that drives `checkSlackAppEventsScopes`. Production wiring of that closure (via `fetchSlackBotScopes` reading `/km/slack/bot-token` from SSM) is already established in `initRealDepsWithExisting` — the new check inherits identical auth-handling and a single source of truth for scope retrieval.
- **`SandboxLister` for live-sandbox enumeration in stale-object detection.** Rather than a fresh DDB Scan, the production wiring of `SlackListSandboxIDs` adapts the existing `DoctorDeps.Lister` (the same lister used by half a dozen other doctor checks) by mapping `SandboxRecord.SandboxID` over `lister.ListSandboxes(ctx, false)`. This keeps the doctor surface narrow and avoids a parallel scan.
- **Always-WARN-never-FAIL on transcript health.** Phase 68 is opt-in (most deployments will not have transcript streaming enabled). A missing stream-messages table or absent files:write scope must not turn the doctor red — these are advisory signals for opt-in operators, not platform-correctness gates. All three new checks demote `CheckError` to `CheckWarn` at registration time, mirroring the existing Phase 63/67 Slack-check policy.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Plan-checker concern about `Doctor` / `SlackBaseURL` confirmed; pivoted to closure-based deps**

- **Found during:** Task 1 (implementing the three check functions)
- **Issue:** The plan referenced a `Doctor` struct with fields `Config`, `DDB`, `S3`, `SlackBotToken`, `SlackBaseURL`, `SandboxIDListFn`. The plan-checker concurrency note flagged this for verification. Inspection of the existing `internal/app/cmd/doctor.go` and `doctor_slack.go` confirmed there is NO `Doctor` struct; the established pattern is `DoctorDeps` (a flat struct of clients) plus closure-based dep injection at the check-function call site (each check takes its concrete deps as positional parameters).
- **Fix:** Adapted the plan's pseudo-code to the actual codebase pattern. Each new check function accepts only the deps it needs (e.g. `checkSlackTranscriptTableExists(ctx, client DynamoDescribeAPI, tableName string)`); the production wiring lives in `initRealDepsWithExisting`. The `files:write` HTTP probe was redesigned to reuse the existing `SlackAuthTestScopes` closure rather than spinning up a parallel HTTP path.
- **Files modified:** `internal/app/cmd/doctor_slack_transcript.go`, `internal/app/cmd/doctor.go`
- **Verification:** All three new checks compile, register, and run. 12 tests PASS. Phase 67 inbound suite still green.
- **Committed in:** `500650e` (Task 1) + `0fee099` (Task 2)

**2. [Rule 3 - Blocking] DoctorConfigProvider interface change broke existing test config stubs**

- **Found during:** Task 2 (registering checks in `doctor.go`)
- **Issue:** Adding `GetSlackStreamMessagesTableName()` to the `DoctorConfigProvider` interface broke compilation of `testConfig` and `testDoctorConfig` (used by half a dozen unrelated doctor tests in `doctor_test.go`).
- **Fix:** Added `GetSlackStreamMessagesTableName() string` returning `"km-slack-stream-messages"` to both stubs. `doctorStaleAMIConfig` embeds `testDoctorConfig` so it inherits the new method via composition (no edit needed there).
- **Files modified:** `internal/app/cmd/doctor_test.go` (+6 lines, two methods)
- **Verification:** Full doctor test suite compiles and passes; no regression.
- **Committed in:** `0fee099` (Task 2)

**3. [Rule 3 - Blocking] `kmaws.S3ListAPI` requires `GetObject` — added no-op to `fakeS3List`**

- **Found during:** Task 3 (writing the stale-objects test)
- **Issue:** `kmaws.S3ListAPI` is defined as `ListObjectsV2 + GetObject` (the same interface is shared with the sandbox-listing code path). The first compile of `fakeS3List` (with only `ListObjectsV2`) failed.
- **Fix:** Added a `GetObject` method to `fakeS3List` that returns `errors.New("not implemented in fakeS3List")` — the new check never calls `GetObject`, so the no-op is safe.
- **Files modified:** `internal/app/cmd/doctor_slack_transcript_test.go`
- **Verification:** Test file compiles and all 12 tests PASS.
- **Committed in:** `e056ac3` (Task 3)

---

**Total deviations:** 3 auto-fixed (3 blocking)
**Impact on plan:** All deviations were necessary adaptations to the actual codebase shape. The plan's intent (three check functions registered, 5 stubs promoted to PASS) was achieved unchanged; only the implementation strategy for dep injection differed from the plan's pseudo-code. No scope creep.

## Issues Encountered

- **Transient cross-plan test-link conflict (resolved during execution).** Plan 68-10 (running in parallel on the same branch) had not yet landed `printTranscriptWarning` and was carrying a duplicate `captureStderr` declaration in `create_slack_transcript_test.go`. The cmd test binary briefly failed to link. Plan 68-11 did NOT modify any of those files (scope boundary). The conflict resolved itself during Plan 68-11 execution as Plan 68-10's executor advanced its tasks. Final verification was run on a clean tree and all 12 Plan 68-11 tests PASS. Documented in `deferred-items.md`.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Plan 68-12 (UAT) can now manually verify these checks against a real environment using:
  - `km doctor` (no flags) — should report three new check rows alongside the Phase 63/67 Slack rows.
  - `km doctor --all-regions` — same three rows; multi-region behaviour inherits from the existing per-region fan-out (transcript checks themselves are region-agnostic).
- Healthy environment expected output: all three rows OK with messages `"table km-slack-stream-messages ACTIVE"`, `"Slack bot has files:write scope"`, `"N transcript prefix(es); none stale"`.
- Degraded environment expected output: WARN rows with the remediation hints documented in `internal/app/cmd/doctor_slack_transcript.go`.

## Self-Check: PASSED

- `internal/app/cmd/doctor_slack_transcript.go` exists on disk
- `internal/app/cmd/doctor.go` modified (registrations present)
- `internal/app/cmd/doctor_test.go` modified (test stubs extended)
- `internal/app/cmd/doctor_slack_transcript_test.go` modified (5 stubs promoted to 12 PASS tests)
- All three task commits exist (`500650e`, `0fee099`, `e056ac3`) verified via `git log --oneline`
- `go build ./...` clean
- `go test ./internal/app/cmd/... -count=1 -run "TestDoctor_SlackTranscript|TestDoctor_SlackFilesWrite" -v` reports 12 PASS

---
*Phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload*
*Completed: 2026-05-03*
