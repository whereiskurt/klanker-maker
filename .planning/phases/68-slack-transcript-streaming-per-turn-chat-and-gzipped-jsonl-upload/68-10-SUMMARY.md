---
phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
plan: 10
subsystem: compiler+cli
tags: [slack, transcript, env-injection, operator-warning, audience-containment, notify-env, channel-info]

# Dependency graph
requires:
  - phase: 68
    provides: "SlackStreamMessagesTableName field on userDataParams (Plan 68-06); km-notify-hook PostToolUse + Stop transcript branches that gate on KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED (Plan 68-09); 3 stubs in userdata_transcript_test.go and 3 stubs in create_slack_transcript_test.go (Plan 68-00)"
  - phase: 67
    provides: "SlackAPI.ChannelInfo(ctx, channelID) → (memberCount int, isMember bool, err error) helper used for member-count fetch"
  - phase: 63
    provides: "NotifyEnv map population pattern (KM_NOTIFY_EMAIL_ENABLED, KM_NOTIFY_SLACK_ENABLED) — mirrored for the new transcript env vars"
provides:
  - "KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED env var emitted to /etc/profile.d/km-notify-env.sh + /etc/km/notify.env when profile.spec.cli.notifySlackTranscriptEnabled=true"
  - "KM_SLACK_STREAM_TABLE env var emitted alongside, propagating Config.GetSlackStreamMessagesTableName() into the sandbox-side hook"
  - "printTranscriptWarning helper in internal/app/cmd/create_slack.go — non-blocking stderr warning with channel id + member count"
  - "km create warning wired in at the right place (after channel resolution, before terragrunt apply) so operators can abort with Ctrl-C before a sandbox provisions"
affects:
  - "Plan 68-12 — runtime e2e validation now has the env vars in place to drive the hook end-to-end on a real sandbox"

# Tech tracking
tech-stack:
  added: []  # No new Go imports; reused fmt.Fprintf to os.Stderr + existing context/SlackAPI types
  patterns:
    - "Pattern A: Phase 62/63 NotifyEnv map mirroring — additive `if profile.flag { params.NotifyEnv[KEY]=VALUE }` block placed AFTER existing field assignments (params.SlackStreamMessagesTableName = streamTable). Avoids re-shaping the established structure."
    - "Pattern B: late-stage warning emission — printTranscriptWarning fires inside the existing Slack-resolution if-block, after slackChannelID+slackPerSandbox are set. Uses the same slackClient already in scope; no extra plumbing."
    - "Pattern C: graceful degradation on ChannelInfo failure — single nil-check + zero-value early-out gives 'Audience: unknown' fallback. Never aborts km create."
    - "Pattern D: precise env-file substring assertion — `export KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=` (with `export` prefix) matches the env-file template only, NOT the heredoc-body comments that reference the same key. Pre-existing tests use bare-key checks; the new tests are tighter to avoid false positives from comments added in Plan 68-09."

key-files:
  created: []
  modified:
    - "pkg/compiler/userdata.go (+13 lines — Phase 68 NotifyEnv block immediately after SlackStreamMessagesTableName assignment)"
    - "pkg/compiler/userdata_transcript_test.go (3 t.Skip stubs replaced with full assertion bodies; +1 helper transcriptProfile; +profile import; +strings import)"
    - "internal/app/cmd/create.go (+8 lines — printTranscriptWarning call inside the existing Slack-resolution if-block)"
    - "internal/app/cmd/create_slack.go (+23 lines — printTranscriptWarning helper function)"
    - "internal/app/cmd/create_slack_transcript_test.go (3 t.Skip stubs replaced with table-driven tests + fakeSlackAPIWithMembers fake; reuses captureStderr from testhelpers_test.go)"

key-decisions:
  - "Used `export KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=` (with the export keyword) as the negative-assertion substring rather than bare `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=`. Plan 68-09 added comments in the heredoc body that reference 'KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=1' — a bare-key check would false-positive against those comments. The env-file template is the only place 'export KEY=' appears, so the tighter prefix is the right boundary."
  - "Reused the existing captureStderr helper from internal/app/cmd/testhelpers_test.go rather than declaring a duplicate in the new test file. My initial draft duplicated it (verbatim from the plan template); the package-level test build failed with 'captureStderr redeclared'. Removed the duplicate and added a comment pointing readers at testhelpers_test.go."
  - "Placed printTranscriptWarning INSIDE the existing 'if NotifySlackEnabled' block in create.go (immediately after slackChannelID is set), gated by 'if NotifySlackTranscriptEnabled && slackChannelID != \"\"'. The slackClient variable is already in scope there — no extra plumbing. Calling it later (e.g. just before Step 11d) would require re-creating the slack client."
  - "Kept member-count formatting as 'Audience: %s Slack users.' with %s rather than %d so the unknown-fallback path can substitute the literal string 'unknown' without branching the format string. Trade-off: the int branch must do its own Sprintf('%d', members) before the outer Fprintf; tiny duplication, much simpler control flow."
  - "Did NOT modify doctor.go or doctor_test.go even though the working tree contained Plan 68-11's WIP modifications to those files. Per the concurrency note in the prompt: 'Stage files explicitly by path. Only commit files listed in this plan's files_modified frontmatter.' My package compiled and tests passed because 68-11 had also staged the matching test-stub fixes (testConfig + testDoctorConfig methods adding GetSlackStreamMessagesTableName). The compile path went green by virtue of 68-11's staged edits being on disk during my test runs — not because I committed them."

patterns-established:
  - "Concurrent-executor scope discipline: when working in a multi-executor branch, identify which files belong to OTHER executors (here: doctor.go, doctor_test.go owned by 68-11) and explicitly stage only your own. Even if the working tree contains other in-flight edits that make YOUR tests pass, commit only your declared files. The other executor will commit their own changes separately."
  - "Negative env-file assertions should use the format prefix (`export KEY=` for /etc/profile.d, bare `KEY=` for /etc/km/notify.env) not just the bare key — comments and shell-default expansions (`${KEY:-0}`) in the heredoc body can otherwise produce false positives."
  - "Non-blocking warning helpers in km create: take a typed dependency (here: SlackAPI), nil-check at the top, then degrade gracefully on any error from the dependency. Never propagate errors out — the warning is informational only and must not abort the user's create flow."

requirements-completed: []  # Plan frontmatter declares no requirements (Phase 68 is spec-driven)

# Metrics
duration: ~13min
completed: 2026-05-03
---

# Phase 68 Plan 10: km create env injection + transcript-streaming warning Summary

**Wired KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED + KM_SLACK_STREAM_TABLE into the NotifyEnv map (compiler) and added a non-blocking operator audience-containment warning at km create (channel id + Slack member count via the Phase 67 ChannelInfo helper). Promoted 6 Wave 0 stubs (3 compiler + 3 cli) to PASS-ing tests.**

## Performance

- **Duration:** ~13 min (single execution session, ~12m 54s wall clock)
- **Started:** 2026-05-03T20:39Z (approx)
- **Completed:** 2026-05-03T20:52Z
- **Tasks:** 2 → 2 commits
- **Files modified:** 5 (3 test files, 2 production code files)

## Accomplishments

- The compiler's NotifyEnv population block now emits both transcript env vars when `notifySlackTranscriptEnabled: true`. When false (or unset), neither is emitted — the hook's `:-0` shell-default keeps the feature off, matching the Phase 62 convention used for KM_NOTIFY_SLACK_ENABLED + KM_NOTIFY_EMAIL_ENABLED.
- The km create flow now surfaces an audience-containment warning to stderr immediately after Slack channel resolution, BEFORE terragrunt apply runs. Operators see the channel id and current member count and can Ctrl-C if the audience is wider than expected.
- Member-count fetch via the existing Phase 67 SlackAPI.ChannelInfo helper (returns `(int, bool, error)`). Failures degrade to "Audience: unknown Slack users." — never abort km create.
- 3/3 PASS in `pkg/compiler/userdata_transcript_test.go` for the env-var tests (TestUserData_NotifySlackTranscriptEnabledEnvVar, TestUserData_KMSlackStreamTableEnvVar, TestUserData_TranscriptDisabledOmitsEnvVar). Plus the 1 already-PASS-ing TestUserData_PostToolUseHookRegistered from Plan 68-09 — 4/4 in the file.
- 3/3 PASS in `internal/app/cmd/create_slack_transcript_test.go` for the operator-warning tests.
- `go build ./...` clean, `make build` produces a fresh `km` binary with version ldflags embedded (v0.2.489).
- `pkg/compiler/...` full suite: 248 PASS, 2 pre-existing baseline failures unchanged.
- `internal/app/cmd/...` package builds clean; the 13 pre-existing test failures listed in deferred-items.md remain (no new regressions caused by this plan).

## Task Commits

Each task was committed atomically using TDD (RED → GREEN):

1. **Task 1: Wire transcript env vars into NotifyEnv** — `8872438` (feat)
   - Added `if p.Spec.CLI.NotifySlackTranscriptEnabled { params.NotifyEnv["KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED"]="1"; params.NotifyEnv["KM_SLACK_STREAM_TABLE"]=params.SlackStreamMessagesTableName }` immediately after the existing SlackStreamMessagesTableName assignment in pkg/compiler/userdata.go.
   - Replaced 3 t.Skip stubs in userdata_transcript_test.go with full assertion bodies. Added a transcriptProfile() helper.
   - 3 new tests PASS; full pkg/compiler suite green except the 2 documented pre-existing baseline failures.

2. **Task 2: Operator transcript-streaming warning at km create** — `de50dd0` (feat)
   - Added `printTranscriptWarning(ctx, api, channelID)` helper in internal/app/cmd/create_slack.go. Non-blocking: ChannelInfo errors degrade to "Audience: unknown Slack users."
   - Wired the call into the existing `if NotifySlackEnabled` block in internal/app/cmd/create.go, gated by `if NotifySlackTranscriptEnabled && slackChannelID != ""`. Uses the slackClient already in scope.
   - Replaced 3 t.Skip stubs in create_slack_transcript_test.go with table-driven mock-backed tests using fakeSlackAPIWithMembers + the existing captureStderr helper from testhelpers_test.go.
   - 3 new tests PASS; `make build` produces fresh km binary v0.2.489.

## Files Created/Modified

- `pkg/compiler/userdata.go` — +13 lines (Phase 68 NotifyEnv block).
- `pkg/compiler/userdata_transcript_test.go` — 3 stubs replaced with assertion bodies + transcriptProfile helper + 2 new imports (strings, profile).
- `internal/app/cmd/create.go` — +8 lines (printTranscriptWarning call).
- `internal/app/cmd/create_slack.go` — +23 lines (printTranscriptWarning helper function).
- `internal/app/cmd/create_slack_transcript_test.go` — 3 stubs replaced with full bodies; declares fakeSlackAPIWithMembers fake; reuses captureStderr from testhelpers_test.go.

## Decisions Made

See `key-decisions:` in frontmatter. Highlights:
- **`export KEY=` substring for negative assertions** — bare-key would false-positive against the heredoc-body comments Plan 68-09 added.
- **Reused captureStderr helper** instead of duplicating (the plan template suggested defining one inline; the package already had one in testhelpers_test.go).
- **Placed warning inside the existing Slack-resolution if-block** — slackClient already in scope, no extra plumbing.
- **`Audience: %s Slack users.`** with %s — lets the unknown-fallback path substitute a literal string without branching the format string.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Duplicate captureStderr declaration**
- **Found during:** Task 2 (initial test compile)
- **Issue:** I followed the plan's example code which declared a captureStderr helper inline in create_slack_transcript_test.go. The package already had a captureStderr helper in testhelpers_test.go (used by step-11d Plan 63.1-01 and destroySlackChannel Plan 63.1-02 tests). The package-level test build failed with `captureStderr redeclared in this block`.
- **Fix:** Removed my duplicate declaration; added a comment pointing readers at testhelpers_test.go. The existing helper has identical semantics.
- **Files modified:** internal/app/cmd/create_slack_transcript_test.go
- **Verification:** `go test -run TestCreate_TranscriptWarning` — 3/3 PASS.
- **Committed in:** de50dd0 (Task 2 commit)

**2. [Rule 3 - Blocking] Negative-assertion substring matched heredoc-body comments**
- **Found during:** Task 1 (RED phase — TestUserData_TranscriptDisabledOmitsEnvVar failed even though the env vars were correctly omitted from the env file)
- **Issue:** My initial assertion `strings.Contains(out, "KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=")` matched the comment line `#      - PostToolUse fires only when KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=1.` that Plan 68-09 added at userdata.go line 414. The pre-existing TestUserDataNotifyEnv_SlackEnabledNilPointer pattern (using bare `KM_NOTIFY_SLACK_ENABLED=`) works only because the heredoc references KM_NOTIFY_SLACK_ENABLED with `:-` syntax (not `=`). The new key was unlucky enough to appear with `=` in a comment.
- **Fix:** Tightened both negative assertions to `export KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=` and `export KM_SLACK_STREAM_TABLE=` — the `export` prefix is unique to the env-file template's `export KEY="VALUE"` lines.
- **Files modified:** pkg/compiler/userdata_transcript_test.go (test-only fix; production code was already correct)
- **Verification:** TestUserData_TranscriptDisabledOmitsEnvVar PASS; positive-case tests still PASS (they assert `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED="1"` with the value, which matches both the env file and would NOT match the comment).
- **Committed in:** 8872438 (Task 1 commit)

---

**Total deviations:** 2 auto-fixed (both blocking)
**Impact on plan:** Both fixes were strictly necessary to make the new tests pass against the current tree. Neither expanded scope; both are test-quality improvements that make assertions tighter without weakening coverage.

## Issues Encountered

- **Concurrent executor (Plan 68-11) had WIP modifications to doctor.go and doctor_test.go** that initially appeared in my working tree (uncommitted changes adding `GetSlackStreamMessagesTableName()` to DoctorConfigProvider + the matching test stubs). The interface change had to be matched by both the production adapter AND the testConfig/testDoctorConfig fakes, otherwise the package wouldn't compile. 68-11's working tree had both edits in flight; when the 68-11 executor eventually committed (during my Task 2 work), those files left my git status. Per the concurrency note in my prompt I never staged them — only my own files (pkg/compiler/userdata.go, pkg/compiler/userdata_transcript_test.go, internal/app/cmd/create.go, internal/app/cmd/create_slack.go, internal/app/cmd/create_slack_transcript_test.go) went into my commits. My tests still ran green because 68-11 had staged the doctor_test.go fix simultaneously, so the package compiled. This is the expected concurrent-executor pattern; no action required.
- **Pre-existing baseline failures unchanged:** TestUserDataNotifyEnv_NoChannelOverride_NoChannelID + TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime in pkg/compiler (already documented). 13 pre-existing failures in internal/app/cmd (TestAtList_WithRecords, TestConfigureInteractivePromptsUseNewNames, TestCreateDockerWritesComposeFile, TestApplyLifecycleOverrides_RunCreateRemoteSignature, TestListCmd_EmptyStateBucketError, TestLockCmd_RequiresStateBucket, TestShellDockerContainerName, TestShellDockerNoRootFlag, TestLearnOutputPath, TestShellCmd_StoppedSandbox, TestShellCmd_UnknownSubstrate, TestShellCmd_MissingInstanceID, TestUnlockCmd_RequiresStateBucket — all match the Plan 68-00 baseline list). Out-of-scope per GSD scope-boundary rule.

## User Setup Required

None — Plan 68-10 changes are entirely compile-time + warning-emission. Operators get the new env vars on their next sandbox create when they enable `notifySlackTranscriptEnabled` in a profile, and they get the warning automatically. No SSM updates, no Lambda redeploy, no AMI rebake.

## Next Phase Readiness

- Plan 68-11 (in flight, parallel) can independently land its three doctor checks; nothing in 68-10 conflicts.
- Plan 68-12 (e2e UAT) now has end-to-end coverage: profile flag → NotifyEnv → /etc/profile.d/km-notify-env.sh + /etc/km/notify.env → hook gate → km-slack post / record-mapping / upload → S3 + Slack thread. The full stack is wired.
- The km binary at `./km` (v0.2.489) contains both the compiler env-injection and the operator warning.

## Self-Check: PASSED

Verified after writing this SUMMARY.md:

**Files exist:**
- `pkg/compiler/userdata.go` — FOUND (Phase 68 NotifyEnv block at line ~3109).
- `pkg/compiler/userdata_transcript_test.go` — FOUND (4 PASS tests, 0 t.Skip remaining).
- `internal/app/cmd/create.go` — FOUND (printTranscriptWarning call inside Slack-resolution if-block).
- `internal/app/cmd/create_slack.go` — FOUND (printTranscriptWarning helper definition).
- `internal/app/cmd/create_slack_transcript_test.go` — FOUND (3 PASS tests, fakeSlackAPIWithMembers fake).

**Commits exist:**
- `8872438` — FOUND (feat(68-10): wire transcript env vars into NotifyEnv).
- `de50dd0` — FOUND (feat(68-10): operator transcript-streaming warning at km create).

**Build green:**
- `go build ./...` — clean.
- `make build` — produces km v0.2.489 (de50dd0).

**Test counts (final):**
- pkg/compiler — 3/3 new env-var tests PASS; 248 total PASS in package; 2 pre-existing failures unchanged.
- internal/app/cmd — 3/3 new warning tests PASS; pre-existing failures unchanged.

---
*Phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload*
*Completed: 2026-05-03*
