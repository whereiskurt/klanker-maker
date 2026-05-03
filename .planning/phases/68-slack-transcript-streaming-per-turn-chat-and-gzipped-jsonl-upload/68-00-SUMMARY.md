---
phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
plan: 00
subsystem: testing
tags: [test-stubs, fixtures, slack-transcript, wave-0, scaffolding]

# Dependency graph
requires:
  - phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
    provides: notify-hook-stub-km-send.sh pattern; PostToolUse hook precedent; 67-00 stub-seeding workflow
provides:
  - 13 compileable t.Skip(...) stub test files covering every name in 68-VALIDATION.md per-task verify map
  - PostToolUse stdin JSON fixture (sess-abc123, Edit tool against /workspace/main.go)
  - Multi-tool transcript JSONL fixture (12 entries, two assistant turns, multiple tool_use + tool_result)
  - km-slack stub script that records subcommand+args, returns deterministic ts for `post`, exits 0 for upload/record-mapping
affects: [68-01, 68-02, 68-03, 68-04, 68-05, 68-06, 68-07, 68-08, 68-09, 68-10, 68-11, 68-12]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Wave-0 stub pattern: t.Skip(\"Wave 0 stub — Plan 68-NN\") in package-aligned _test.go files; future plans replace bodies via Edit not Create"
    - "testdata stub helper pattern: bash script that records subcommand/args/stdin to KM_*_STUB_LOG and emits deterministic JSON for the call site that needs a value"

key-files:
  created:
    - pkg/profile/validate_slack_transcript_test.go
    - pkg/slack/payload_transcript_test.go
    - pkg/slack/client_upload_test.go
    - pkg/slack/bridge/upload_handler_test.go
    - pkg/compiler/notify_hook_post_tool_use_test.go
    - pkg/compiler/userdata_transcript_test.go
    - cmd/km-slack/main_dispatch_test.go
    - cmd/km-slack-bridge/main_upload_test.go
    - internal/app/cmd/agent_transcript_test.go
    - internal/app/cmd/shell_transcript_test.go
    - internal/app/cmd/doctor_slack_transcript_test.go
    - internal/app/cmd/create_slack_transcript_test.go
    - internal/app/config/config_stream_table_test.go
    - pkg/compiler/testdata/notify-hook-fixture-posttooluse.json
    - pkg/compiler/testdata/notify-hook-fixture-multitool-transcript.jsonl
    - pkg/compiler/testdata/notify-hook-stub-km-slack.sh
    - .planning/phases/68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload/deferred-items.md
  modified: []

key-decisions:
  - "Mirrored Phase 67-00 stub-seeding exactly — every Wave-0 stub uses the same `t.Skip(\"Wave 0 stub — Plan 68-NN\")` form so implementing plans replace bodies via Edit rather than create new tests"
  - "Created stub km-slack helper script as a sibling of the existing notify-hook-stub-km-send.sh rather than extending the km-send stub — keeps the post/upload/record-mapping subcommand surface explicit and lets Plan 68-09 swap PATH stubs independently"
  - "Multi-tool transcript fixture deliberately spans two assistant turns with multiple tool_use entries each — gives Plan 68-09 offset-tracking tests something concrete to count against between PostToolUse fires"
  - "Logged 16 pre-existing test failures (pkg/compiler, cmd/km-slack, internal/app/cmd) to deferred-items.md after confirming via stash test that none are caused by Plan 68-00 stub additions"

patterns-established:
  - "VALIDATION.md as the contract for stub names — every per-task verify map entry has a matching t.Skip stub before Wave 1 begins"
  - "deferred-items.md per phase — out-of-scope test failures land here rather than auto-fix attempts that risk scope creep"

requirements-completed: []

# Metrics
duration: 7min
completed: 2026-05-03
---

# Phase 68 Plan 00: Wave-0 Stub Seeding Summary

**13 compileable t.Skip stub test files + 3 testdata fixtures covering every per-task verify name in 68-VALIDATION.md so Plans 68-01 through 68-12 can replace bodies via Edit rather than create tests from scratch.**

## Performance

- **Duration:** 7 min (441 s)
- **Started:** 2026-05-03T19:44:56Z
- **Completed:** 2026-05-03T19:52:17Z
- **Tasks:** 2
- **Files modified:** 17 created (13 stub _test.go + 3 testdata fixtures + 1 deferred-items doc)

## Accomplishments

- Seeded 13 stub test files across 7 packages — `go build ./...` clean
- 63 named SKIP results across `TestValidate_SlackTranscript|TestCanonicalJSON_*|TestUploadFile|TestHandler_ActionUpload|TestNotifyHook_*|TestUserData_*|TestDispatch_*|TestActionUploadRouting|TestAgentRun_*|TestShell_*|TestDoctor_Slack*|TestCreate_TranscriptWarning|TestConfig_GetSlackStreamMessages*` (>2× the plan's `>= 25` minimum)
- Created the 3 testdata fixtures Plan 68-09 needs: PostToolUse stdin JSON, 12-line multi-tool transcript JSONL, and an executable `notify-hook-stub-km-slack.sh` mirroring the existing km-send stub
- Logged 16 pre-existing test failures to `deferred-items.md` after stash-confirm that none originate in Plan 68-00 changes

## Task Commits

1. **Task 1: Create 13 stub test files** — `dd32348` (test)
2. **Task 2: Create 3 testdata fixtures** — `5da4fe6` (test)

**Plan metadata commit:** _to be added by `node gsd-tools.cjs commit ...`_

## Files Created/Modified

### Stub test files (Task 1)

- `pkg/profile/validate_slack_transcript_test.go` — 5 stubs for Plan 68-01 (notifySlackTranscriptEnabled validation rules)
- `pkg/slack/payload_transcript_test.go` — 4 stubs for Plan 68-02 (canonical-JSON envelope tests)
- `pkg/slack/client_upload_test.go` — 7 stubs for Plan 68-04 (UploadFile 3-step flow)
- `pkg/slack/bridge/upload_handler_test.go` — 9 stubs for Plan 68-08 (ActionUpload validation)
- `pkg/compiler/notify_hook_post_tool_use_test.go` — 10 stubs for Plan 68-09 (PostToolUse + Stop upload)
- `pkg/compiler/userdata_transcript_test.go` — 4 stubs for Plans 68-09/68-10 (env injection + hook registration)
- `cmd/km-slack/main_dispatch_test.go` — 5 stubs for Plan 68-05 (post/upload/record-mapping dispatch)
- `cmd/km-slack-bridge/main_upload_test.go` — 1 stub for Plan 68-08 (ActionUpload routing)
- `internal/app/cmd/agent_transcript_test.go` — 3 stubs for Plan 68-07 (km agent run flag plumbing)
- `internal/app/cmd/shell_transcript_test.go` — 3 stubs for Plan 68-07 (km shell flag plumbing)
- `internal/app/cmd/doctor_slack_transcript_test.go` — 5 stubs for Plan 68-11 (three doctor checks)
- `internal/app/cmd/create_slack_transcript_test.go` — 3 stubs for Plan 68-10 (operator warning)
- `internal/app/config/config_stream_table_test.go` — 4 stubs for Plan 68-03 (table-name helper)

### Testdata fixtures (Task 2)

- `pkg/compiler/testdata/notify-hook-fixture-posttooluse.json` — 8-line PostToolUse stdin JSON (`session_id: sess-abc123`, Edit tool against `/workspace/main.go`)
- `pkg/compiler/testdata/notify-hook-fixture-multitool-transcript.jsonl` — exactly 12 JSONL entries spanning two assistant turns (3 tool_use + 3 tool_result + multiple text entries)
- `pkg/compiler/testdata/notify-hook-stub-km-slack.sh` — executable bash stub that records `subcommand: post|upload|record-mapping` + args to `$KM_SLACK_STUB_LOG`; emits deterministic `{"ok":true,"ts":"1700000000.000100"}` for `post`

### Documentation

- `.planning/phases/68-.../deferred-items.md` — 16 pre-existing failures logged out-of-scope per scope-boundary rule

## Decisions Made

- **Mirrored 67-00 exactly** for the stub pattern. The `t.Skip("Wave 0 stub — Plan 68-NN")` body keeps the file compileable and lets each implementing plan replace the body in place.
- **Separate km-slack stub** from km-send stub. The existing `notify-hook-stub-km-send.sh` only covers the email path; transcript streaming needs `post`/`upload`/`record-mapping` subcommands. A new stub keeps the surface explicit so Plan 68-09 can choose which stubs to drop on PATH.
- **Multi-tool transcript spans two assistant turns** intentionally — Plan 68-09's offset-tracking test must distinguish "what got streamed last fire vs this fire", which requires multiple stops mid-transcript. Two turns × 3 tool_use entries provides that.
- **Out-of-scope failures logged, not fixed.** Stash-test confirmed 16 failures across `pkg/compiler`, `cmd/km-slack`, `internal/app/cmd` reproduce on the unmodified baseline (commit `36f263b`). Per the GSD scope-boundary rule, these were logged to `deferred-items.md` rather than auto-fixed.

## Deviations from Plan

None — plan executed exactly as written. Both tasks ran their verify gates clean:

- Task 1: `go build ./...` succeeded, ≥25 named tests show SKIP (actual: 63 across the targeted regex).
- Task 2: All three fixtures exist, JSON parses with `sess-` id, JSONL has exactly 12 lines, stub is executable.

## Issues Encountered

- **Pre-existing test failures on baseline (out of scope).** Running the full test suite during Task 1 verification surfaced 16 FAILs in `pkg/compiler`, `cmd/km-slack`, and `internal/app/cmd`. Investigated by stashing the new stubs and rerunning — same failures reproduce on commit `36f263b` (no Plan 68-00 changes). Per scope-boundary rule, logged to `deferred-items.md` and continued. They do not affect Plan 68-00 verify gates because (a) `go build ./...` is clean, (b) the targeted SKIP-count regex returns 63, and (c) none of the failing test names overlap with the 13 stubs Plan 68-00 added.

## User Setup Required

None — Wave-0 scaffolding only; no operator-facing changes.

## Next Phase Readiness

- All 12 implementing plans (68-01..68-12) can run their `go test -run …` automated verification on day one. Initial run reports SKIP (compiles, has-name); the implementing plan flips bodies in place to PASS.
- All 3 fixture references in Plan 68-09 resolve to real files.
- No blockers for Wave 1 startup. Recommended order per phase plan dependencies: 68-01 → 68-02 → 68-03 (foundation) → 68-04..68-08 → 68-09 → 68-10..68-12.

## Self-Check: PASSED

All claims in this summary verified:

- 13 stub test files exist on disk and compile (`go build ./...` clean).
- 3 testdata fixtures exist; JSON parses; JSONL has 12 lines; stub is executable.
- Both task commits exist: `dd32348` (Task 1), `5da4fe6` (Task 2).
- 63 SKIP count confirmed via `-v -run` and `grep -cE "^--- SKIP:"`.
- `deferred-items.md` exists at the documented path.

---
*Phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload*
*Completed: 2026-05-03*
