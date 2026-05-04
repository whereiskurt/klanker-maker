---
phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
plan: 07
subsystem: cli
tags: [cli, cobra, slack, transcript, env-injection, ssm, agent-run, shell]

requires:
  - phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
    provides: Wave 0 stubs in agent_transcript_test.go + shell_transcript_test.go (Plan 68-00)
  - phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
    provides: notifySlackTranscriptEnabled profile field + validation (Plan 68-01)

provides:
  - --transcript-stream / --no-transcript-stream flags on km agent run
  - --transcript-stream / --no-transcript-stream flags on km shell
  - AgentRunOptions.TranscriptStream *bool field threaded through BuildAgentShellCommands
  - resolveTranscriptFlag(cmd) *bool helper in shell.go (mirrors resolveNotifyFlags)
  - Extended buildNotifySendCommands(perm, idle, transcript) to emit KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED into /etc/profile.d/zz-km-notify.sh
  - 6 promoted Wave 0 stubs (3 agent + 3 shell) — all PASS

affects:
  - 68-09 (hook-script reader for KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED)
  - Operators using km agent run / km shell with profile-default override semantics

tech-stack:
  added: []
  patterns:
    - "Phase 62 (HOOK-04) tri-state flag pattern: --positive / --negative / nil for transcript-stream"
    - "Per-invocation env-var override emitted into the same /etc/profile.d/zz-km-notify.sh file (shell.go) and inline before agent invocation (agent.go)"

key-files:
  created: []
  modified:
    - internal/app/cmd/agent.go
    - internal/app/cmd/shell.go
    - internal/app/cmd/agent_transcript_test.go
    - internal/app/cmd/shell_transcript_test.go
    - internal/app/cmd/shell_notify_test.go

key-decisions:
  - "Mirrored existing Phase 62 (HOOK-04) shapes exactly — added a parallel resolveTranscriptFlag helper rather than changing resolveNotifyFlags' signature, so existing tests stay green"
  - "Extended buildNotifySendCommands with a third (transcript) arg rather than introducing a new helper — single SSM SendCommand still writes one /etc/profile.d/zz-km-notify.sh file with all overrides; cleaner and avoids two SendCommands per session"
  - "Tri-state semantics preserved: nil pointer = no env line emitted, profile-derived /etc/profile.d/km-notify-env.sh value applies; non-nil = explicit override (\"0\" or \"1\")"

patterns-established:
  - "Per-invocation transcript-stream override: --transcript-stream / --no-transcript-stream / unset on both km agent run and km shell"

requirements-completed: []

duration: 22min
completed: 2026-05-03
---

# Phase 68 Plan 07: km agent run + km shell --transcript-stream flag plumbing Summary

**Two new tri-state flags (--transcript-stream / --no-transcript-stream) on km agent run and km shell that inject KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=0|1 into the SSM session env, mirroring the Phase 62 (HOOK-04) NotifyOnPermission pattern exactly.**

## Performance

- **Duration:** 22 min
- **Started:** 2026-05-03T19:58:00Z (approx — based on Task 1 RED phase first edit)
- **Completed:** 2026-05-03T20:20:32Z
- **Tasks:** 3 (all autonomous TDD)
- **Files modified:** 5

## Accomplishments

- `km agent run --transcript-stream` / `--no-transcript-stream` registered, resolved via `resolveNotifyFlagAgent`, and threaded through `AgentRunOptions.TranscriptStream` into the script that `BuildAgentShellCommands` builds. Emits `export KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED="0"|"1"` before the agent invocation when the pointer is non-nil; emits nothing when nil (so the profile-derived `/etc/profile.d/km-notify-env.sh` value applies).
- `km shell --transcript-stream` / `--no-transcript-stream` registered, resolved via a new `resolveTranscriptFlag(cmd) *bool` helper, and threaded through `runShell` -> `execSSMSession` -> `buildNotifySendCommands`. Same tri-state semantics; the bracketing SSM SendCommand writes the override into `/etc/profile.d/zz-km-notify.sh` before the session and `rm -f`s it after.
- 6 Wave 0 stubs promoted to passing tests: 3 in `agent_transcript_test.go` (testing `BuildAgentShellCommands` directly) and 3 in `shell_transcript_test.go` (testing `resolveTranscriptFlag` + `buildNotifySendCommands`). All 6 PASS.
- Existing notify-flag tests (`TestBuildAgentShellCommands_Notify*`, `TestBuildNotifySendCommands_*`, `TestResolveNotifyFlags_*`) still pass — no regression.

## Task Commits

1. **Task 1: Add --transcript-stream / --no-transcript-stream to km agent run** — committed in `b1dadc8` (see Deviations below)
2. **Task 2 + 3: Add --transcript-stream / --no-transcript-stream to km shell + replace 6 Wave 0 stubs** — `77c8b8a` (feat)

**Plan metadata:** (this commit, after summary creation)

_Note: Tasks 2 and 3 were combined into a single commit because the test file (shell_transcript_test.go) referenced the unexported helpers `resolveTranscriptFlag` and `buildNotifySendCommands(perm, idle, transcript)` that only exist after the production-code changes — splitting them would have left a non-compiling intermediate state._

## Files Created/Modified

- `internal/app/cmd/agent.go` — Added `transcriptStream` flag var, `--transcript-stream`/`--no-transcript-stream` registrations, `resolveNotifyFlagAgent("transcript-stream", "no-transcript-stream", transcriptStream)` resolution, `transcriptStreamPtr` arg threaded through `runAgentNonInteractive` and into `AgentRunOptions.TranscriptStream`. Extended `BuildAgentShellCommands`'s notify-env builder to emit `export KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED="0"|"1"` when `opts.TranscriptStream != nil`.
- `internal/app/cmd/shell.go` — Added `resolveTranscriptFlag(cmd) *bool` helper, registered both flags, threaded the resolved `*bool` through `runShell` and `execSSMSession`, extended `buildNotifySendCommands(perm, idle, transcript)` to emit `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED` into the heredoc that writes `/etc/profile.d/zz-km-notify.sh`.
- `internal/app/cmd/agent_transcript_test.go` — Replaced 3 `t.Skip` Wave 0 stubs with real tests that exercise `BuildAgentShellCommands` directly with `TranscriptStream: ptr(true)`, `ptr(false)`, and `nil`.
- `internal/app/cmd/shell_transcript_test.go` — Replaced 3 `t.Skip` Wave 0 stubs with real tests that exercise `resolveTranscriptFlag` (via `cobra.Command.ParseFlags`) and `buildNotifySendCommands` for all three states (force-on / force-off / unset).
- `internal/app/cmd/shell_notify_test.go` — Updated 3 existing `buildNotifySendCommands` call sites to pass `nil` as the new third (transcript) arg — preserves Phase 62 semantics for those tests.

## Decisions Made

- **Parallel helper, not signature change for `resolveNotifyFlags`** — Adding a new `resolveTranscriptFlag(cmd) *bool` keeps the existing `resolveNotifyFlags` signature stable (and `TestResolveNotifyFlags_*` tests untouched). Symmetric with `resolveNotifyFlagAgent` in agent.go which is already a closure factory shared across all three flag pairs.
- **Single helper, signature-extended for `buildNotifySendCommands`** — The build-and-write helper produces a single heredoc + chmod for the profile.d file, so adding `transcript *bool` as a third arg keeps it producing one SSM SendCommand even when all three pointers are set. Splitting into two helpers would have meant two SendCommands per session.
- **Tri-state preserved end-to-end** — `nil` flows from CLI flag (no `Changed`) → `*bool` (no env line emitted) → profile-default `/etc/profile.d/km-notify-env.sh` wins. `&true` / `&false` produce explicit `="1"` / `="0"` lines that override the profile default. Identical to the Phase 62 NotifyOnPermission contract documented at agent.go:1148-1153 and shell.go:108-119.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Updated existing `buildNotifySendCommands` test call sites for new 3-arg signature**
- **Found during:** Task 2 (extending `buildNotifySendCommands` to accept a transcript pointer)
- **Issue:** Three existing call sites in `shell_notify_test.go` (`TestBuildNotifySendCommands_BothNil`, `_PermissionOnly`, `_BothExplicit`) passed only `(perm, idle)`. Adding the third `transcript *bool` arg broke their compilation.
- **Fix:** Updated all three calls to pass `nil` as the new third arg. Preserves Phase 62 test semantics (no transcript override when pointer nil).
- **Files modified:** internal/app/cmd/shell_notify_test.go
- **Verification:** `go test ./internal/app/cmd/... -run "TestBuildNotifySendCommands"` — all 3 still PASS.
- **Committed in:** 77c8b8a (Task 2/3 commit)

### Concurrency-induced commit attribution loss

**[Concurrent-executor artifact] Task 1 work landed inside `b1dadc8 feat(68-06)` rather than its own `feat(68-07)` commit**
- **Found during:** Task 1 commit attempt
- **Issue:** A parallel executor agent on the same branch ran a wide `git commit` with hooks/staging that swept my already-`git add`-staged Task 1 files (`internal/app/cmd/agent.go`, `internal/app/cmd/agent_transcript_test.go`) into commit `b1dadc8 feat(68-06): add ec2spot transcript IAM policies (S3 PutObject + DDB PutItem)`. My subsequent `git commit -m "feat(68-07)..."` errored with "no changes added" — the staged work had already been picked up under the wrong message.
- **Fix:** None — the code is in HEAD, just under a misleading commit message. Per the project's safety protocol (no rebases / no `--amend` of others' commits), I did not rewrite history. Continued with Tasks 2 + 3 normally; their commit (`77c8b8a`) is clean.
- **Files affected:** internal/app/cmd/agent.go, internal/app/cmd/agent_transcript_test.go (in `b1dadc8`)
- **Verification:** `grep -q "TranscriptStream" internal/app/cmd/agent.go && go test ./internal/app/cmd/... -run TestAgentRun_TranscriptStream` — all 3 agent tests PASS against HEAD.
- **Impact:** Cosmetic only — `git log --grep "68-07"` will not surface Task 1, but `git log -p -- internal/app/cmd/agent.go` will show the Plan 68-07 changes attributed to commit `b1dadc8`.

---

**Total deviations:** 2 (1 auto-fix, 1 concurrency-induced commit attribution issue)
**Impact on plan:** All success criteria met. No scope creep, no logic regressions. The concurrency artifact is documented for traceability but does not affect functionality or future plan execution.

## Issues Encountered

- **Multi-executor commit collision** — A parallel executor on the same branch swept staged Task 1 files into its own commit. Documented above. The concurrency note in this plan's prompt warned against `git add .` / `-A` — that warning was honored by THIS executor, but parallel executors evidently used a wider staging strategy. No code lost; only commit attribution.
- **Pre-existing test failures in `internal/app/cmd/`** — `TestShellCmd_StoppedSandbox`, `TestShellCmd_UnknownSubstrate`, `TestShellCmd_MissingInstanceID`, plus the Plan 68-00 baseline failures (`TestAtList_WithRecords`, etc.). Verified these are NOT caused by Plan 68-07 changes (`git stash --keep-index` test confirms identical failures on the pre-change tree). Already tracked in `deferred-items.md`; appended a Plan 68-07 confirmation entry. The pre-existing `_ = runShell(...)` swallow at shell.go:209 is the root cause for the 3 ShellCmd_* failures — out of scope.

## Self-Check: PASSED

Verified:
- `[ -f internal/app/cmd/agent.go ]` — modifications present (TranscriptStream field, --transcript-stream flag, KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED export wiring)
- `[ -f internal/app/cmd/shell.go ]` — modifications present (resolveTranscriptFlag helper, --transcript-stream flag, extended buildNotifySendCommands)
- `[ -f internal/app/cmd/agent_transcript_test.go ]` — 3 real tests, 0 t.Skip stubs
- `[ -f internal/app/cmd/shell_transcript_test.go ]` — 3 real tests, 0 t.Skip stubs
- `[ -f internal/app/cmd/shell_notify_test.go ]` — updated for new 3-arg signature
- Commit `77c8b8a` exists in `git log` and contains the Task 2/3 changes
- Commit `b1dadc8` exists in `git log` and contains the Task 1 changes (under `feat(68-06)` message — see Deviations)
- `go build ./...` clean
- `go test ./internal/app/cmd/... -run "TestAgentRun_TranscriptStream|TestShell_TranscriptStream|..."` reports 6 PASS / 0 SKIP

## Next Phase Readiness

- Plan 68-09 (hook script) can now read `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED` knowing operators have a per-invocation override path.
- No blockers. The CLI surface for transcript opt-in/opt-out is complete and matches the Phase 62 precedent.

---
*Phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload*
*Completed: 2026-05-03*
