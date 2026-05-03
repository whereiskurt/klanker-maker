---
phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
plan: 09
subsystem: compiler
tags: [slack, transcript, hooks, claude-code, posttooluse, stop, jsonl, streaming, gzip, s3, ed25519, dynamodb, bash, heredoc]

# Dependency graph
requires:
  - phase: 68
    provides: "ActionUpload + record-mapping subcommands in cmd/km-slack (Plan 68-05); SlackStreamMessagesTableName field in userDataParams (Plan 68-06); Wave 0 stubs + km-slack stub script + posttooluse fixture + multitool transcript fixture (Plan 68-00)"
  - phase: 67
    provides: "Heredoc-based km-notify-hook structure with email + slack-root branches; KM_SLACK_THREAD_TS poller-driven gate semantics; '# 6a.' / '# 6b.' marker convention asserted by TestUserdata_StopHookReferencesThreadTSGate"
  - phase: 63
    provides: "km-slack post subcommand + KM_NOTIFY_SLACK_ENABLED env-driven gating; cooldown semantics for the email/slack-root branch"
  - phase: 62
    provides: "/opt/km/bin/km-notify-hook heredoc + Notification + Stop event branches"
provides:
  - "PostToolUse event branch in /opt/km/bin/km-notify-hook (gated by KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=1)"
  - "_km_stream_drain() shell function — auto-thread-parent + per-session offset tracking + tool-call rendering (Edit/Bash + generic fallback)"
  - "Stop branch transcript upload — gzip + aws s3 cp + km-slack upload + cleanup of /tmp state"
  - "PostToolUse hook entry registered in mergeNotifyHookIntoSettings (alongside Notification + Stop)"
  - "11 PASS-ing notify-hook tests (10 PostToolUse + Stop + 1 settings.json registration)"
affects:
  - "Plan 68-10 — wires KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED + KM_SLACK_STREAM_TABLE into NotifyEnv map (3 remaining stubs in userdata_transcript_test.go)"
  - "Plan 68-11 — runtime SSM-fetch step that resolves KM_SLACK_BRIDGE_URL inside the sandbox so the hook's km-slack post / upload calls have a target"
  - "Plan 68-12 — e2e validation against live infra (PostToolUse fires on real Claude turns; transcript artifact lands in S3 + Slack thread)"

# Tech tracking
tech-stack:
  added: []  # No new Go imports; pure bash/heredoc additions + Go test harness extension
  patterns:
    - "Pattern A: shell-helper function inside the heredoc — _km_stream_drain() called by both PostToolUse and Stop branches avoids duplicating ~100 lines of streaming logic"
    - "Pattern B: cooldown becomes a soft block flag (cooldown_block) gated by event type — Notification + plain Stop preserve hard-exit; PostToolUse + Stop+transcript bypass it (transcript completeness is non-negotiable)"
    - "Pattern C: counter-sidecar in test stubs — notify-hook-stub-km-slack.sh writes a per-call incrementing counter so each post/upload call returns a distinct ts; required for parent-vs-streaming pair tests"
    - "Pattern D: stderr ts-format mirroring — test stub emits 'km-slack: posted ts=<...>' on stderr matching real binary output, so the hook's `grep -oE 'ts=[0-9.]+'` capture path is identical under stub vs. real binary"

key-files:
  created: []
  modified:
    - "pkg/compiler/userdata.go (+291 / -60 in heredoc body + 1 line in mergeNotifyHookIntoSettings + 4 lines of comments)"
    - "pkg/compiler/notify_hook_post_tool_use_test.go (10 t.Skip stubs → 10 PASS-ing tests; +617 lines of harness + assertions)"
    - "pkg/compiler/notify_hook_script_test.go (added /opt/km/bin/km-slack → km-slack PATH substitution alongside the existing km-send substitution)"
    - "pkg/compiler/userdata_transcript_test.go (1 t.Skip stub → 1 PASS-ing test; 3 stubs deferred to Plan 68-10)"
    - "pkg/compiler/testdata/notify-hook-stub-km-slack.sh (added stderr 'ts=...' emission + counter sidecar for distinct ts per call)"
    - "pkg/compiler/testdata/notify-hook-fixture-posttooluse.json (replaced hardcoded /tmp/test-transcript.jsonl with __TRANSCRIPT_PATH__ placeholder for harness substitution)"
    - ".planning/phases/68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload/deferred-items.md (re-confirmed pre-existing TestUserDataNotifyEnv_* failures)"

key-decisions:
  - "Extracted streaming logic into a single _km_stream_drain() shell function (rather than duplicating ~100 lines across PostToolUse and Stop branches). The function is defined inside the heredoc once, then called by both branches with $sid and $transcript_path."
  - "Cooldown becomes a soft block (cooldown_block flag) gated by event type. Notification + plain Stop preserve the existing hard-exit-on-cooldown behavior (Phase 62 TestNotifyHook_Cooldown still passes). PostToolUse + Stop+transcript ignore it — transcript completeness is non-negotiable; missing a slack post would leave the operator with a partial thread."
  - "Outer 'if [[ $do_email_branch ... ]]; then' wrapper preserves the existing # 6a. / # 6b. marker numbering inside, so Phase 67's structural assertions (TestUserdata_StopHookReferencesThreadTSGate + TestUserdata_StopHookSkipsSlackWhenPollerDriving) continue to pass without test changes. The new transcript-upload section is # 8. — outside the email branch's '# 7.' bound."
  - "Kept the # 6a. / # 6b. markers verbatim (didn't try to rename them when wrapping in do_email_branch). Renumbering would have required updating the slack-inbound tests; preserving them is a smaller, safer diff."
  - "Updated notify-hook-stub-km-slack.sh to emit 'km-slack: posted ts=<n>' on stderr (mirrors real binary) instead of (or in addition to) the legacy stdout JSON. This makes the hook's `grep -oE 'ts=[0-9.]+'` pattern identical under stub and real km-slack binaries — tests now exercise the same capture code path that production runs."
  - "Counter-sidecar pattern in the stub (KM_SLACK_STUB_LOG.counter) — each call returns a distinct ts so auto-parent vs. streaming-post pairs can be distinguished by the test (test 2 'AutoParent' asserts streaming post --thread = cached parent ts)."
  - "Updated notify-hook-fixture-posttooluse.json to use __TRANSCRIPT_PATH__ placeholder (matching the existing Phase 62/63 stop fixture convention) instead of the original hardcoded /tmp/test-transcript.jsonl. This lets the harness's loadFixture() substitute a tmp transcript path so the hook's [[ -f \"$transcript_path\" ]] check succeeds."
  - "Wrapped the email/slack-root path in do_email_branch=1 (computed from event + KM_NOTIFY_ON_IDLE) instead of duplicating logic. When event=Stop AND KM_NOTIFY_ON_IDLE=0 AND KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=1, the entire email branch is skipped and only the # 8. transcript-upload section runs."

patterns-established:
  - "Stub script counter-sidecar pattern: tests that need each invocation to return a distinct value (timestamp, request id, etc.) can write a per-test counter file at ${LOG_PATH}.counter; the stub increments it per call. Avoids harness-side state."
  - "Heredoc shell function for cross-branch logic reuse: when two event branches need the same multi-line flow (offset tracking + auto-thread + post + record-mapping), define a single `_km_xxx()` function inside the heredoc, after the gate check but before the branch dispatch."
  - "Soft-block flag instead of early-exit for cooldowns: when adding a new event type to a hook that already has cooldown semantics, replace the early `exit 0` with a `cooldown_block=1` flag and let each branch decide whether to honor it. Preserves backward-compat for old branches."

requirements-completed: []  # Plan frontmatter declares no requirements

# Metrics
duration: ~30min
completed: 2026-05-03
---

# Phase 68 Plan 09: km-notify-hook PostToolUse + Stop transcript upload Summary

**Extended the inlined `/opt/km/bin/km-notify-hook` heredoc with a PostToolUse branch (per-turn streaming with auto-thread-parent + offset tracking + record-mapping) and a Stop branch transcript upload (gzip + S3 cp + km-slack upload + cleanup), registered the PostToolUse hook in settings.json, and promoted 11 Wave 0 stubs to PASS-ing tests covering gating, auto-thread-parent caching, env-ts override, multi-fire offset tracking, tool-call rendering, transcript upload args, drain-then-upload ordering, /tmp cleanup, and Phase 63 email-only regression.**

## Performance

- **Duration:** ~30 min (single execution session)
- **Started:** 2026-05-03T20:30Z (approx)
- **Completed:** 2026-05-03T20:34Z
- **Tasks:** 5 (combined into 5 commits — feat(68-09) main heredoc, test(68-09) 10-test harness, test(68-09) settings test, docs deferred-items)
- **Files modified:** 7 (1 production code, 4 test files, 1 testdata stub, 1 testdata fixture, 1 docs)

## Accomplishments

- The km-notify-hook heredoc now contains a complete PostToolUse + Stop+transcript flow: stdin parse → _km_stream_drain (auto-thread-parent if needed → tail JSONL from offset → render assistant text + tool one-liners → km-slack post → record-mapping → advance offset) → for Stop also gzip+s3 cp+km-slack upload + cleanup. ~100 lines of bash, all gated by env vars, never blocks Claude (always exits 0).
- mergeNotifyHookIntoSettings registers PostToolUse alongside Notification + Stop unconditionally — runtime gating happens via KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED inside the script.
- Test harness (`setupHookEnvWithSlack`) extends the existing `setupHookEnv` with km-slack and aws stubs on PATH so the heredoc's `/opt/km/bin/km-slack` and `aws s3 cp` calls resolve in unit tests. The km-slack stub now emits the same `ts=<...>` line on stderr as the real binary.
- 10/10 PostToolUse + Stop tests PASS (TestNotifyHook_PostToolUse_GateOff, _AutoParent, _CachedThread, _ExistingKMSlackThreadTS, _OffsetTracking, _RendersToolOneLiner, TestNotifyHook_Stop_TranscriptUpload, _FinalStreamThenUpload, _CleansUpTmpFiles, _EmailOnlyRegression).
- 1/1 settings.json registration test PASS (TestUserData_PostToolUseHookRegistered).
- 8/8 pre-existing Phase 62/63 NotifyHook tests still PASS (no regression).
- 2/2 pre-existing Phase 67 TestUserdata_StopHook* tests still PASS (preserved by keeping the # 6a. / # 6b. marker numbering inside the email branch wrapper).
- `make build` clean.
- `go build ./...` clean.

## Task Commits

Each task was committed atomically:

1. **Task 1 + 2 + 3: km-notify-hook heredoc extension + PostToolUse settings registration** — `4a02a09` (feat)
   Combined into one commit because all three modify pkg/compiler/userdata.go and the changes are tightly coupled (PostToolUse branch + Stop transcript section + settings.json appendKMHook line all reference each other and share the heredoc structure).

2. **Task 4: 10 PostToolUse + Stop tests promoted from stubs to PASS** — `83d574f` (test)
   Adds setupHookEnvWithSlack harness, slackCall parser, slackArgValue helper, 10 test bodies covering all behavior described in the plan's success criteria. Also updates testdata/notify-hook-stub-km-slack.sh and notify-hook-fixture-posttooluse.json so the harness can drive realistic flows.

3. **Task 5: TestUserData_PostToolUseHookRegistered + # 6a./# 6b. marker preservation** — `e6d3d65` (test)
   Replaces the t.Skip stub with assertions that drill into the merged settings.json (using the existing extractHeredocBody + drillHookCmd helpers from Phase 62 tests). Also restores '# 6a.' / '# 6b.' marker numbering inside the email-branch wrapper so the Phase 67 slack-inbound structural tests continue to pass.

4. **Deferred-items confirmation** — `2ea75f2` (docs)

## Files Created/Modified

- `pkg/compiler/userdata.go` — extended km-notify-hook heredoc (PostToolUse branch, _km_stream_drain function, Stop transcript upload section); appendKMHook("PostToolUse", ...) in mergeNotifyHookIntoSettings.
- `pkg/compiler/notify_hook_post_tool_use_test.go` — 10 t.Skip stubs replaced with full harness + assertions (~620 lines of test code).
- `pkg/compiler/notify_hook_script_test.go` — extractNotifyHookScript() now also substitutes /opt/km/bin/km-slack → km-slack so PATH-based stubs resolve in tests.
- `pkg/compiler/userdata_transcript_test.go` — TestUserData_PostToolUseHookRegistered stub replaced with real assertions; 3 other stubs (env-var injection) remain t.Skip for Plan 68-10.
- `pkg/compiler/testdata/notify-hook-stub-km-slack.sh` — emits stderr `ts=<n>` for post and upload subcommands, counter sidecar for distinct ts per call.
- `pkg/compiler/testdata/notify-hook-fixture-posttooluse.json` — uses __TRANSCRIPT_PATH__ placeholder (harness convention).
- `.planning/phases/68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload/deferred-items.md` — re-confirmed two pre-existing baseline failures are unchanged.

## Decisions Made

See `key-decisions:` in frontmatter. Highlights:
- **Single _km_stream_drain() helper** instead of duplicating streaming logic across branches.
- **Soft-block cooldown flag** so PostToolUse + Stop+transcript bypass cooldown without breaking Phase 62/63 hard-exit semantics for Notification + plain Stop.
- **Preserved # 6a. / # 6b. markers** to keep Phase 67 slack-inbound tests passing without modification — chose smaller diff over renumbering.
- **Stub stderr ts-format mirroring** so production capture path (`grep -oE 'ts=[0-9.]+'`) is exercised identically under stub and real binary.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] notify-hook-fixture-posttooluse.json had hardcoded /tmp/test-transcript.jsonl**
- **Found during:** Task 4 (running 10 new tests — first 4 failed with 0 km-slack post calls because the hook bailed out on `[[ ! -f "$transcript_path" ]]`)
- **Issue:** The Plan 68-00 fixture had `"transcript_path": "/tmp/test-transcript.jsonl"` instead of the harness's `__TRANSCRIPT_PATH__` placeholder convention (which the Phase 62/63 stop fixture uses). loadFixture() couldn't substitute → hook saw a non-existent transcript path → exited early.
- **Fix:** Replaced the hardcoded path with `__TRANSCRIPT_PATH__` so loadFixture() substitutes the test's tmp transcript path.
- **Files modified:** pkg/compiler/testdata/notify-hook-fixture-posttooluse.json
- **Verification:** All 10 PostToolUse + Stop tests now pass.
- **Committed in:** 83d574f (Task 4 commit)

**2. [Rule 3 - Blocking] notify-hook-stub-km-slack.sh emitted only stdout JSON, not stderr ts=...**
- **Found during:** Task 4 (designing the test harness — realized the hook's `grep -oE 'ts=[0-9.]+'` pattern wouldn't match the stub's stdout JSON-only output)
- **Issue:** Real `km-slack post` writes "km-slack: posted ts=<n>" to stderr. The hook captures both streams (`2>&1`) and greps for `ts=`. The Plan 68-00 stub only echoed JSON `{"ok":true,"ts":"..."}` to stdout — `ts=` substring wouldn't match without quotes/colon, and even if it did, all calls returned the same ts so the auto-parent vs. streaming-post pair test couldn't distinguish them.
- **Fix:** Updated stub to (a) emit `km-slack: posted ts=<n>` on stderr matching the real binary, and (b) use a counter sidecar so each call returns a distinct ts.
- **Files modified:** pkg/compiler/testdata/notify-hook-stub-km-slack.sh
- **Verification:** TestNotifyHook_PostToolUse_AutoParent now correctly distinguishes parent post (no --thread) from streaming post (--thread = cached parent ts).
- **Committed in:** 83d574f (Task 4 commit)

**3. [Rule 3 - Blocking] extractNotifyHookScript() didn't substitute /opt/km/bin/km-slack**
- **Found during:** Task 4 (km-slack stub on PATH not getting hit — the heredoc still had the absolute path)
- **Issue:** extractNotifyHookScript() substituted `/opt/km/bin/km-send` → `km-send` so test stubs resolve via PATH, but the new Phase 68 hook code calls `/opt/km/bin/km-slack` which was NOT substituted. Tests' PATH-prepended stub was unreachable.
- **Fix:** Added a parallel `strings.ReplaceAll(body, "/opt/km/bin/km-slack", "km-slack")` line.
- **Files modified:** pkg/compiler/notify_hook_script_test.go
- **Verification:** km-slack stub log now records calls; tests pass.
- **Committed in:** 83d574f (Task 4 commit)

**4. [Rule 3 - Blocking] Initial '# 7.' renumbering broke Phase 67 slack-inbound structural tests**
- **Found during:** Final pre-commit `go test ./pkg/compiler/...` run (Task 5 cleanup)
- **Issue:** When wrapping the existing email/slack-root branch in `if [[ $do_email_branch ... ]]; then`, I initially renumbered the inner '# 6a.' / '# 6b.' / '# 7.' markers to '# 7a.' / '# 7b.' / '# 8.'. Phase 67's TestUserdata_StopHookReferencesThreadTSGate and TestUserdata_StopHookSkipsSlackWhenPollerDriving structurally bound on '# 6a.' / '# 6b.' / '# 7.' as boundaries. Both tests broke ('# 6b. marker not found').
- **Fix:** Reverted the inner markers to '# 6a.' / '# 6b.' / '# 7.' (cooldown comment); only the new transcript-upload section gets '# 8.'. Phase 67 tests pass.
- **Files modified:** pkg/compiler/userdata.go
- **Verification:** TestUserdata_StopHookReferencesThreadTSGate + TestUserdata_StopHookSkipsSlackWhenPollerDriving both PASS.
- **Committed in:** e6d3d65 (Task 5 commit)

---

**Total deviations:** 4 auto-fixed (4 blocking)
**Impact on plan:** All 4 auto-fixes were necessary to make Plan 68-09's own tests pass and preserve existing test contracts. No scope creep — fixes were strictly to make the new code work and not break previously-passing tests. The two test-data fixes (fixture placeholder + stub stderr) are improvements to Plan 68-00 wiring that were not anticipated when those fixtures were authored.

## Issues Encountered

- **Pre-existing baseline failures unchanged** (`TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`, `TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime`): these fail on the unmodified tree (verified by stashing 68-09's changes and rerunning). Out-of-scope per GSD scope-boundary rule. Re-documented in deferred-items.md.

## User Setup Required

None — Plan 68-09 changes are entirely compile-time. Plan 68-11 will introduce the runtime SSM-fetch step that resolves KM_SLACK_BRIDGE_URL inside sandboxes; Plan 68-10 will inject KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED + KM_SLACK_STREAM_TABLE into NotifyEnv. No operator action needed for this plan.

## Next Phase Readiness

- Plan 68-10 can now extend `userDataParams.NotifyEnv` with KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED and KM_SLACK_STREAM_TABLE; the hook's runtime checks are in place and the 3 remaining `TestUserData_*EnvVar` stubs in `userdata_transcript_test.go` can be promoted.
- Plan 68-11 can wire `KM_SLACK_BRIDGE_URL` runtime resolution; the hook expects it to be present (km-slack subcommands fail at flag-validation if it's missing — non-fatal because hook always exits 0).
- Plan 68-12 (e2e) has working hook code to validate against live infra.

## Self-Check: PASSED

Verified after writing this SUMMARY.md:

**Files exist:**
- `pkg/compiler/userdata.go` — FOUND (modified, contains "PostToolUse", "_km_stream_drain", "km-slack record-mapping", "transcripts/").
- `pkg/compiler/notify_hook_post_tool_use_test.go` — FOUND (10 PASS).
- `pkg/compiler/notify_hook_script_test.go` — FOUND (km-slack PATH substitution added).
- `pkg/compiler/userdata_transcript_test.go` — FOUND (1 PASS, 3 t.Skip for Plan 68-10).
- `pkg/compiler/testdata/notify-hook-stub-km-slack.sh` — FOUND (stderr ts emission added).
- `pkg/compiler/testdata/notify-hook-fixture-posttooluse.json` — FOUND (__TRANSCRIPT_PATH__ placeholder).
- `.planning/phases/68-.../deferred-items.md` — FOUND (Plan 68-09 confirmation appended).

**Commits exist:**
- `4a02a09` — FOUND (feat(68-09): extend km-notify-hook with PostToolUse + Stop transcript upload).
- `83d574f` — FOUND (test(68-09): replace 10 PostToolUse + Stop transcript-streaming stubs).
- `e6d3d65` — FOUND (test(68-09): add PostToolUseHookRegistered test + preserve # 6a./# 6b. markers).
- `2ea75f2` — FOUND (docs(68-09): re-confirm pre-existing TestUserDataNotifyEnv_* failures).

---
*Phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload*
*Completed: 2026-05-03*
