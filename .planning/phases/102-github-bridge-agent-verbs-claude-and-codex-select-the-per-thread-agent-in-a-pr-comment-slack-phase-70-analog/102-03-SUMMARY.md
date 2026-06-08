---
phase: 102
plan: "03"
subsystem: compiler/userdata
tags: [github-bridge, agent-verbs, poller, compiler, tdd]
dependency_graph:
  requires: [102-01, 102-02]
  provides: [GH-AGENT-POLLER, GH-AGENT-SWITCH, GH-AGENT-PROFILE]
  affects: [pkg/compiler/userdata.go, pkg/compiler/userdata_github_inbound_test.go, pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh]
tech_stack:
  added: []
  patterns: [bash-in-Go-raw-string-heredoc, golden-file-update, render-and-grep-guard]
key_files:
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_github_inbound_test.go
    - pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh
decisions:
  - "Codex-missing error uses single quotes around 'codex:' verb (not markdown backticks) — Go raw string literals cannot contain backtick characters"
  - "Golden file (userdata_learn_v2_pre92_baseline.golden.sh) re-captured via CAPTURE_PRE92_BASELINE=1 to include Phase 102 poller changes — this is the standard per-phase pattern"
  - "THREAD_AGENT_TYPE defaults to $AGENT (profile default) immediately after the DDB block, before RESUME_ARG computation, mirroring Slack Phase 70 Pitfall 2 guard"
  - "Precedence block runs after RESUME_ARG is computed so D5 cross-agent reset (clear GITHUB_SESSION + RESUME_ARG) takes full effect before the dispatch fork"
metrics:
  duration: "348s"
  completed_date: "2026-06-08"
  tasks_completed: 3
  files_modified: 3
---

# Phase 102 Plan 03: GitHub Poller Agent Precedence + Persistence Summary

GitHub inbound poller now selects the per-thread agent via D4 precedence (verb > thread agent_type > profile default) with D5 cross-agent session reset, D3 agent_type write-back, and D6 codex-missing guard.

## What Was Built

The GitHub inbound poller (`pkg/compiler/userdata.go` heredoc) gained the runtime agent-selection machinery that Phase 102 Plans 01 and 02 prepared foundations for:

**Task 1 — AGENT_OVERRIDE parse + THREAD_AGENT_TYPE DDB read:**
- Added `AGENT_OVERRIDE=$(echo "$BODY" | jq -r '.agent // empty' ...)` after the envelope-field parses (D1: reads the agent verb produced by Plan 01's bridge)
- Extended `km-github-threads` `get-item` projection from `agent_session_id` alone to `agent_session_id, agent_type`
- Extracted `THREAD_AGENT_TYPE` from the DDB row; defaults to `$AGENT` (profile default) when absent (Pitfall 2 guard)

**Task 2 — Precedence block + cross-agent switch + codex guard + write-back:**
- Replaced `EFFECTIVE_AGENT="$AGENT"` hardwire with D4 precedence block:
  - `AGENT_OVERRIDE` present → use it; if it differs from `THREAD_AGENT_TYPE` and a session exists, clear `GITHUB_SESSION` + `RESUME_ARG` together (D5 cross-agent reset, Pitfall 3)
  - `AGENT_OVERRIDE` absent → use `THREAD_AGENT_TYPE` (thread-pinned agent, or profile default for first turn)
- Added D6 codex-missing guard immediately before the dispatch fork: `command -v codex` check → post helpful `km-github comment` + ack message → `continue`
- Extended success-path `update-item` from `SET agent_session_id = :sid` to `SET agent_session_id = :sid, agent_type = :at` (D3 persistence, pins the thread for future turns)

**Task 3 — Render-and-grep guard:**
- Added `TestUserdata_GitHubInboundPoller_Phase102AgentVerbs` to `userdata_github_inbound_test.go` — greps the rendered GITHUBINBOUND heredoc for `AGENT_OVERRIDE`, `THREAD_AGENT_TYPE`, `command -v codex`, `agent_type = :at`
- Re-captured `userdata_learn_v2_pre92_baseline.golden.sh` (learn.v2.yaml has GitHub inbound enabled) to include Phase 102 changes — standard per-phase golden update pattern

## Verification

- `make build` clean (km v0.4.891)
- `go vet ./pkg/compiler/...` clean
- `go test ./pkg/compiler/... -count=1` passes (including the new Phase 102 token test)
- Rendered poller confirmed to contain all four Phase 102 tokens: `AGENT_OVERRIDE`, `THREAD_AGENT_TYPE`, `command -v codex`, `agent_type = :at`

## Runtime Behavior (Deferred to Plan 05 E2E UAT)

The following behaviors are encoded in bash within the Go heredoc and have NO unit harness (same pattern as Phases 97/98/99). They are verified manually in Plan 102-05's E2E UAT:

- **D4 Precedence**: `claude:` verb → EFFECTIVE_AGENT=claude; `codex:` verb → EFFECTIVE_AGENT=codex; no verb → thread-pinned agent; first turn, no verb → profile default
- **D5 Cross-agent switch**: `codex:` in a claude-pinned thread clears `GITHUB_SESSION` + `RESUME_ARG` so the new agent starts fresh
- **D3 Persistence**: after a successful turn, `agent_type` is written to `km-github-threads` so the next turn without a verb defaults to the same agent
- **D6 Codex-missing guard**: `codex:` verb on a profile without Codex installed posts a helpful comment and acks the message without stranding the turn
- **Back-compat**: no verb + no stored `agent_type` (pre-Phase-102 rows) dispatches to profile default — byte-identical to pre-Phase-102 behavior

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Backtick in codex-missing error message breaks Go raw string literal**
- **Found during:** Task 2 — `make build` failed with `invalid character U+005C '\'` at the backtick in `\`/codex\` is unavailable here.`
- **Issue:** Go raw string literals (backtick-delimited) cannot contain backtick characters; the markdown code formatting `\`/codex\`` ended the string literal early
- **Fix:** Changed the error message to use single quotes: `'codex:' verb is unavailable here.` — functionally equivalent, readable in the GitHub comment
- **Files modified:** `pkg/compiler/userdata.go`
- **Commit:** 961c9582 (included in Task 2 commit)

## Self-Check

Files created/modified:
- `pkg/compiler/userdata.go` — modified (Tasks 1 + 2)
- `pkg/compiler/userdata_github_inbound_test.go` — modified (Task 3)
- `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` — updated (Task 3)

Commits:
- a966bc95: feat(102-03): AGENT_OVERRIDE parse + THREAD_AGENT_TYPE DDB read (Task 1)
- 961c9582: feat(102-03): precedence block + cross-agent switch + codex guard + write-back (Task 2)
- df14ad5c: test(102-03): render-and-grep guard for Phase 102 poller tokens (Task 3)
