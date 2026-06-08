---
phase: 102
plan: 01
subsystem: github-bridge
tags: [tdd, github, agent-verb, parser, envelope]
dependency_graph:
  requires: []
  provides: [ParseResult.AgentVerb, ParseResult.AgentVerbConflict, ExtractArgsWithAgent, GitHubEnvelope.Agent]
  affects: [pkg/github/bridge/commands.go, pkg/github/bridge/payload.go, pkg/github/bridge/webhook_handler.go]
tech_stack:
  added: []
  patterns: [reserved-token-intercept, composable-verb-axis, agent-verb-strip, tdd-red-green-refactor]
key_files:
  created:
    - pkg/github/bridge/webhook_handler_phase102_test.go
  modified:
    - pkg/github/bridge/commands.go
    - pkg/github/bridge/commands_test.go
    - pkg/github/bridge/payload.go
    - pkg/github/bridge/webhook_handler.go
decisions:
  - "AgentVerb cleared to empty string when AgentVerbConflict=true — callers see one non-empty value at a time; conflict takes precedence"
  - "ExtractArgs refactored to delegate to ExtractArgsWithAgent('') — single stripping implementation, no duplication"
  - "agent-verb conflict check applied on BOTH active-commands and dormant paths in Handle() — symmetric behavior"
  - "Restored thread_store_test.go to HEAD; Plan 02 RED tests already committed there (interfaces.go + aws_adapters.go already updated by Plan 02)"
metrics:
  duration: 470s
  completed_date: "2026-06-08"
  tasks_completed: 3
  files_modified: 5
---

# Phase 102 Plan 01: Agent-Verb Parser Extension + GitHubEnvelope.Agent Summary

**One-liner:** Reserved `/claude` and `/codex` verb tokens parsed from PR comments, stripped from args, conflict-checked, and carried to sandbox via `GitHubEnvelope.Agent`.

## What Was Built

Extended the Phase 99 GitHub comment-verb parser to recognize `/claude` and `/codex` as reserved composable agent-verb tokens on a separate axis from template commands. The parsed verb is carried to the sandbox via a new `GitHubEnvelope.Agent` field.

### ParseResult Extensions (commands.go)

Added `AgentVerb string` and `AgentVerbConflict bool` to `ParseResult`. The `ParseCommands` scanner intercepts `/claude` and `/codex` as reserved built-ins right after the `/help` intercept — before the command-map lookup. Same verb twice = deduped (no conflict). Two distinct verbs = `AgentVerbConflict = true`, `AgentVerb = ""`.

### ExtractArgsWithAgent (commands.go)

New `ExtractArgsWithAgent(body, botLogin, commandToken, agentVerbToken string) string` strips the agent verb token alongside the mention and command token so the sandbox agent never receives `/claude` or `/codex` as literal prompt text. `ExtractArgs` refactored to delegate to `ExtractArgsWithAgent("")` — single stripping implementation.

### GitHubEnvelope.Agent (payload.go)

Added `Agent string \`json:"agent,omitempty"\`` — `"claude"`, `"codex"`, or `""`. `omitempty` ensures old envelope shapes (no field) decode cleanly as `""` (profile default).

### Handle() wiring (webhook_handler.go)

- Agent-verb conflict check added before envelope construction on both active-commands and dormant paths.
- Two distinct verbs → `PostComment("🤖 Specify one agent — found /claude and /codex.")` + return 200, NO dispatch.
- `Agent: agentVerb` set on `GitHubEnvelope`.
- Helper closures `parseAgentVerbs` and `postConflictReply` reduce duplication between active and dormant paths.
- Fixed variable scoping: outer `var agentVerb string` correctly populated by active-commands path using `var agentVerbConflict bool` + assignment (not `:=`).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Plan 02 interface changes already committed prevented compilation**
- **Found during:** Task 2 (GREEN)
- **Issue:** `pkg/github/bridge/thread_store_test.go` had Plan 02 RED tests already committed (commit `fde9bb84`) referencing the extended `LookupSandbox` (4 return values) and `UpdateSession` (with agentType). These caused compile failures.
- **Fix:** Checked the production files — `interfaces.go`, `aws_adapters.go`, and `webhook_handler.go` all already had Plan 02's production changes committed. The tests compiled against the production code as expected. No additional changes needed.
- **Files modified:** none (pre-existing committed state)

**2. [Rule 1 - Bug] AgentVerb not cleared on conflict**
- **Found during:** Task 2 GREEN (test failure `TestCommandParse//claude_AND_/codex_→_AgentVerbConflict=true`)
- **Issue:** When `AgentVerbConflict=true`, `AgentVerb` still held the first-seen verb ("claude"), but the test (and the plan spec) expected `AgentVerb=""` when conflict is set.
- **Fix:** Added post-loop clear: `if result.AgentVerbConflict { result.AgentVerb = "" }`. Callers see one non-empty value at a time; conflict takes precedence.
- **Files modified:** `pkg/github/bridge/commands.go`

**3. [Rule 1 - Bug] agentVerb scope shadowing in active-commands Handle() path**
- **Found during:** Task 3 (REFACTOR code review)
- **Issue:** `agentVerb, agentVerbConflict := parseAgentVerbs(...)` (`:=`) created a new inner-scope `agentVerb`, shadowing the outer `var agentVerb string` used for the envelope, so `env.Agent` would always be `""` on the active-commands path.
- **Fix:** Changed to `var agentVerbConflict bool` + `agentVerb, agentVerbConflict = parseAgentVerbs(...)` (assignment, not declare).
- **Files modified:** `pkg/github/bridge/webhook_handler.go`

## Test Coverage

| Test | Status | Notes |
|------|--------|-------|
| TestCommandParse — /claude → AgentVerb=claude | PASS | Anywhere in body |
| TestCommandParse — /codex → AgentVerb=codex | PASS | |
| TestCommandParse — /claude /codex → conflict | PASS | AgentVerb="" when conflict |
| TestCommandParse — /codex /codex → deduped | PASS | Same verb twice = no conflict |
| TestCommandParse — /codex /patch compose | PASS | Both axes resolved |
| TestCommandParse — verb in code block | PASS | StripCode suppresses |
| TestExtractArgs_AgentVerbStripped — /codex /patch | PASS | args clean of /codex |
| TestExtractArgs_AgentVerbStripped — /claude alone | PASS | args clean of /claude |
| TestHandle_AgentVerbConflict | PASS | PostComment, no SQS |
| TestHandle_EnvelopeCarriesAgent | PASS | env.Agent=="claude", body clean |
| Full pkg/github/bridge suite (Phase 97-101 regression) | PASS | No regressions |

## Task Commits

| Task | Commit | Description |
|------|--------|-------------|
| 1 — RED | ff5901c2 | test(102-01): RED — failing tests for agent-verb parsing + envelope.Agent |
| 2 — GREEN | 7c043d2c | feat(102-01): agent-verb parser extension + GitHubEnvelope.Agent |
| 3 — REFACTOR | fd3497d2 | refactor(102-01): deduplicate ExtractArgs logic + fix agentVerb scope |

## Self-Check: PASSED

- pkg/github/bridge/commands.go: FOUND
- pkg/github/bridge/payload.go: FOUND
- pkg/github/bridge/webhook_handler.go: FOUND
- pkg/github/bridge/commands_test.go: FOUND
- pkg/github/bridge/webhook_handler_phase102_test.go: FOUND
- Commit ff5901c2: FOUND
- Commit 7c043d2c: FOUND
- Commit fd3497d2: FOUND
- `go test ./pkg/github/bridge/... -count=1`: PASS
- `go build ./...`: PASS
