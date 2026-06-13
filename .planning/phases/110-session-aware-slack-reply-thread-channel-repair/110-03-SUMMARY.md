---
phase: 110-session-aware-slack-reply-thread-channel-repair
plan: "03"
subsystem: slack-sandbox-helper
tags:
  - slack
  - sandbox
  - session-aware
  - km-slack
  - reply
  - ed25519
dependency_graph:
  requires:
    - 110-02 (ActionLookupThread bridge action + SessionID envelope field)
  provides:
    - km-slack reply subcommand (sandbox-side)
    - runReplyWith testable inner entry point
    - autoDetectClaudeSession (newest *.jsonl by mtime under claudeProjectsRoot)
    - autoDetectCodexSession (best-effort; WARN+fallback on miss)
    - lookupThreadBySession (HTTP-only; no DDB access from sandbox)
    - PostResponse.Found/ChannelID/ThreadTS/AgentType fields
  affects:
    - cmd/km-slack/main.go
    - pkg/slack/client.go
tech_stack:
  added: []
  patterns:
    - TDD (RED/GREEN per task)
    - First-hit-wins resolution chain (explicit > env > session-lookup > channel-root)
    - Sandbox-never-reads-DDB boundary (session resolved via bridge HTTP action)
    - claudeProjectsRoot overridable var for test injection (no env indirection)
key_files:
  created:
    - cmd/km-slack/main_reply_test.go
  modified:
    - cmd/km-slack/main.go
    - cmd/km-slack/main_dispatch_test.go
    - pkg/slack/client.go
decisions:
  - "lookupThreadBySession uses raw http.DefaultClient.Do (not PostToBridge) to parse Found/ChannelID/ThreadTS from the lookup-thread response shape without conflating it with the post-response retry policy"
  - "PostResponse extended with Found/ChannelID/ThreadTS/AgentType for lookup-thread response (not a new response type) — all fields omitempty so existing callers are unaffected"
  - "claudeProjectsRoot is a package-level var (not env var) so tests can override it without os.Setenv race conditions"
  - "Codex WARN+fallback is a hard invariant: autoDetectCodexSession returns '' on any miss; caller WARNs to stderr and drops to channel-root (never errors)"
  - "runReply imports are additive only: bytes, encoding/json, net/http, path/filepath added; DynamoDB is NOT imported in the reply path (record-mapping kept its existing DDB import)"
requirements-completed: []
duration: "325s"
completed: "2026-06-13"
---

# Phase 110 Plan 03: km-slack reply Subcommand Summary

**Sandbox-side `km-slack reply` with 4-step session-aware thread resolution chain — explicit thread > env TS > bridge lookup-thread (Ed25519-signed, no DDB) > channel-root fallback.**

## Performance

- **Duration:** 325s (~5.5 min)
- **Started:** 2026-06-13T03:16:15Z
- **Completed:** 2026-06-13T03:21:40Z
- **Tasks:** 2 (Task 1: TDD implementation; Task 2: dispatch + guard tests)
- **Files modified:** 4

## Accomplishments

- `km-slack reply` subcommand wired into dispatch table; reuses `runWith` signed-post path
- 4-step resolution chain with exact first-hit-wins semantics, tested end-to-end with a fake bridge HTTP server
- Claude session auto-detect scans `~/.claude/projects/**/*.jsonl` by mtime; root overridable in tests
- Codex path WARNs and falls through to channel root on any miss (never errors — OQ1 invariant)
- Sandbox-never-reads-DDB boundary enforced: `runReply` / `runReplyWith` / `lookupThreadBySession` have no DynamoDB import or call

## Task Commits

1. **Tasks 1+2: runReply + dispatch tests** - `15cd1481` (feat)

## Files Created/Modified

- `cmd/km-slack/main.go` — `case "reply"` dispatch; `runReply` (flag-parse entry), `runReplyWith` (testable inner), `lookupThreadBySession` (HTTP-only lookup), `autoDetectSession`, `autoDetectClaudeSession`, `autoDetectCodexSession`; `claudeProjectsRoot`/`codexStoreRoot` package vars; imports: bytes, encoding/json, net/http, path/filepath
- `cmd/km-slack/main_reply_test.go` — `TestRunReply` (3 sub-tests: explicit-thread, env-ts, session-lookup-found), `TestAutoDetectClaudeSession`, `TestRunReply_FallbackToChannelRoot`, `TestRunReply_NoBridgeCallsWhenEnvTSSet`, `TestRunReplyWith_SlackNotConfigured`
- `cmd/km-slack/main_dispatch_test.go` — `TestDispatch_Reply`, `TestDispatch_Reply_Routes`
- `pkg/slack/client.go` — `PostResponse` extended with `Found`, `ChannelID`, `ThreadTS`, `AgentType` (omitempty; backward-compatible)

## Decisions Made

- `lookupThreadBySession` uses `http.DefaultClient.Do` directly instead of `slack.PostToBridge` because `PostToBridge`'s fail-fast-on-5xx retry semantics are not appropriate for lookup (a lookup failure should fall through to channel root, not return an error to the user), and the response shape (`found`, `channel_id`, `thread_ts`) differs from the normal `ts` post response. The raw HTTP call avoids conflating the two response models.
- `PostResponse` extended (not a new type) because callers already decode it and the new fields are `omitempty` — existing callers see no change.
- `claudeProjectsRoot` is a package-level variable (not reading from an env var) so tests can inject a temp dir without `t.Setenv` races when tests run in parallel.

## Deviations from Plan

None — plan executed exactly as written. The only pre-existing issue (out-of-scope): `TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0` fails because `PostToBridge` deliberately does not retry 5xx, contradicting an old test assumption. Documented in `deferred-items.md`.

## Issues Encountered

**Pre-existing test failure (out-of-scope):** `TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0` in `cmd/km-slack/main_test.go` fails because `PostToBridge` is fail-fast on 5xx (documented behavior), but the test expects 503 to trigger retry. This conflict predates Phase 110. Logged to `deferred-items.md`. All new tests and all other existing tests pass.

## User Setup Required

None — sandbox-side binary change only. Existing sandboxes need `km destroy && km create` to receive the new `km-slack reply` subcommand (binary is baked at create time from `s3://{bucket}/sidecars/km-slack`). No new infrastructure, no IAM change, no schema change.

## Next Phase Readiness

- Plan 04 (operator-side `km slack reply`) can now call the same `ActionLookupThread` bridge action; the `PostResponse` extension it needs (`Found`, `ChannelID`, `ThreadTS`) is already in place.
- Deploy: `make build-lambdas` (rebuild km-slack sidecar) + `km init --dry-run=false` (upload sidecar via `buildAndUploadSidecars`) + `km destroy && km create` per sandbox.

---
*Phase: 110-session-aware-slack-reply-thread-channel-repair*
*Completed: 2026-06-13*

## Self-Check: PASSED

All created/modified files verified present. Task commit 15cd1481 verified in git history.
