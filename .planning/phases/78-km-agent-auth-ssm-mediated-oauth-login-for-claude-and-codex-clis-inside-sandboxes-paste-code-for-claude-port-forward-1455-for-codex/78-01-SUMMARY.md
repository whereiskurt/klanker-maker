---
phase: 78
plan: 01
subsystem: km-agent-auth
tags: [cli, oauth, ssm, authentication, cobra]
dependency_graph:
  requires: []
  provides: [km-agent-auth-claude, noBedrock-credentials-hint]
  affects: [km-shell, km-agent-run]
tech_stack:
  added: []
  patterns: [resolveVSCodeDeps-mirror, package-level-dispatch-vars, newShellCmdWithSSM-injection]
key_files:
  created:
    - internal/app/cmd/agent_auth.go
    - internal/app/cmd/agent_auth_test.go
  modified:
    - internal/app/cmd/agent.go
    - internal/app/cmd/shell.go
decisions:
  - "Used Option 2 for noBedrock pre-check in shell.go: check inside runShell (after extractResourceID), not inside execSSMSession, to keep the injected ssmClient in scope for tests without touching the production AWS credential path"
  - "newShellCmdWithSSM is a test-only internal constructor; production path (NewShellCmdWithFetcher) uses nil ssmClient and the pre-check is silently skipped — the interactive session surfaces auth failures organically"
  - "runAgentAuthClaudeFn / runAgentAuthCodexFn package-level vars enable test stubbing without exporting internal functions"
  - "agent_auth_test.go uses package cmd (internal) to access unexported helpers directly, matching vscode_test.go convention"
metrics:
  duration_minutes: 32
  completed_date: "2026-05-10"
  tasks_completed: 2
  tasks_total: 3
  files_created: 2
  files_modified: 2
---

# Phase 78 Plan 01: km agent auth --claude Command + helpers Summary

Wave 1 of `km agent auth`: SSM-mediated OAuth login for the claude CLI inside sandboxes, plus missing-credentials hints in `km shell --no-bedrock` and `km agent run --no-bedrock`.

## What Was Built

### Task 1: agent_auth.go + agent_auth_test.go + agent.go

New file `internal/app/cmd/agent_auth.go` (258 lines):

- `newAgentAuthCmd` — Cobra command registered under `km agent` via `cmd.AddCommand`
  - Flags: `--claude`, `--codex`, `--console`, `--sso`, `--claudeai`, `--email`
  - Default: `--claude` when neither `--claude` nor `--codex` is set
  - Mutual exclusion: `--claude + --codex` returns error containing "mutually exclusive"
- `runAgentAuthClaude` — Interactive SSM session running `claude auth login [flags]` on sandbox
  - Pre-flight: sandbox must be `running`; refuses with clear error + `km resume` hint otherwise
  - Conflict: `checkAgentSessionConflict` refuses if active `km-agent-*` tmux session
  - Builds inner command via `buildClaudeAuthArgs` + prefixes with profile env source
  - Calls `runSSMInteractiveSubprocess` (same pattern as `km shell`, `km agent`)
  - Verifies `~/.claude/.credentials.json` via `verifyCredentialsWritten` post-session
- `runAgentAuthCodex` — Wave-1 stub pointing at Plan 02
- `buildClaudeAuthArgs` — Builds exact `claude auth login ...` command from flag combination
  - Error on `--console + --sso` combination
  - Default branch: `--claudeai` (with optional `--email`)
- `verifyCredentialsWritten` — SSM `stat` check post-session; prints `✓ ... credentials written`
- `checkAgentSessionConflict` — tmux list-sessions check via `sendSSMAndWait`
- `resolveAuthDeps` — Mirrors `resolveVSCodeDeps` pattern for AWS client init

`agent_auth_test.go` (583 lines, package cmd internal):
- `TestAgentAuth_FlagParsing` (AUTH-01)
- `TestAgentAuth_MutuallyExclusive` (AUTH-02)
- `TestAgentAuth_DefaultClaude` (AUTH-03)
- `TestAgentAuth_SandboxIDResolution` (AUTH-04)
- `TestAgentAuth_ConflictRefuse` (AUTH-05)
- `TestVerifyCredentialsWritten_Success` (AUTH-06)
- `TestVerifyCredentialsWritten_Missing` (AUTH-07, two subtests)
- `TestBuildClaudeAuthArgs` (AUTH-13, 7 table-driven cases)
- `TestShellCmd_NoBedrock_CredentialsMissingHint` (AUTH-11)
- `TestShellCmd_NoBedrock_CredentialsPresent` (AUTH-11 happy path)
- `TestAgentRun_NoBedrock_CredentialsMissingHint` (AUTH-12)
- `TestAgentRun_NoBedrock_CredentialsPresent` (AUTH-12 happy path)

`agent.go` modification: added `cmd.AddCommand(newAgentAuthCmd(...))` inside `NewAgentCmdWithDeps`.

### Task 2: shell.go + agent.go noBedrock pre-check wiring

`shell.go` modifications:
- Refactored `runShell` into `runShell` (thin wrapper, nil ssmClient) + `runShellWithSSM` (full impl)
- Added noBedrock pre-check in `runShellWithSSM` ec2 branch: `stat ~/.claude/.credentials.json` before `execSSMSession`; returns error with `km agent auth` hint if missing
- Added `newShellCmdWithSSM` — internal test constructor for SSM client injection

`agent.go` modification: added noBedrock pre-check in `runAgentNonInteractive` after `extractResourceID` and before `BuildAgentShellCommands` (included in Task 1 commit).

## Requirements Covered

| Requirement | Test | Status |
|-------------|------|--------|
| AUTH-01 | TestAgentAuth_FlagParsing | PASS |
| AUTH-02 | TestAgentAuth_MutuallyExclusive | PASS |
| AUTH-03 | TestAgentAuth_DefaultClaude | PASS |
| AUTH-04 | TestAgentAuth_SandboxIDResolution | PASS |
| AUTH-05 | TestAgentAuth_ConflictRefuse | PASS |
| AUTH-06 | TestVerifyCredentialsWritten_Success | PASS |
| AUTH-07 | TestVerifyCredentialsWritten_Missing | PASS |
| AUTH-11 | TestShellCmd_NoBedrock_CredentialsMissingHint | PASS |
| AUTH-12 | TestAgentRun_NoBedrock_CredentialsMissingHint | PASS |
| AUTH-13 | TestBuildClaudeAuthArgs | PASS |

## Commits

| Hash | Description |
|------|-------------|
| d26838b | feat(78-01): add km agent auth --claude command + helpers |
| 9b342cd | feat(78-01): hint at km agent auth when --no-bedrock credentials are missing |

## Deviations from Plan

### Auto-fixed Issues

None — plan executed exactly as written.

### Implementation Choices Made

**1. [Option 2] noBedrock pre-check in runShell, not execSSMSession**
- Found during: Task 2
- Plan offered Option 1 (thread ssmClient through execSSMSession) or Option 2 (do check in runShell/runShellWithSSM)
- Chose Option 2: smaller diff, ssmClient stays in scope via `newShellCmdWithSSM` injection
- Files modified: shell.go
- Commit: 9b342cd

**2. [Auth test package] Used package cmd (internal) for agent_auth_test.go**
- Found during: Task 1
- Plan described reusing `mockAgentSSM` and `fakeFetcher` from agent_test.go (package cmd_test)
- Those mocks are in external package; the plan requires stubbing package-level vars (unexported)
- Chose internal package `cmd` to match `vscode_test.go` pattern; created `authTestSSM` and `authTestFetcher` mirrors in the internal package
- No behavior difference; test coverage is equivalent

**3. [captureAuthStdout] Renamed captureStdout to avoid conflict**
- Found during: Task 1
- vscode_test.go already defines `captureStdout(fn func()) string` (no `*testing.T`)
- Renamed to `captureAuthStdout` in agent_auth_test.go to avoid redeclaration in package cmd

## Manual UAT

**Status: PASSED (Scenario A)** — Operator verified on L3 (`learn-24a69627`)
2026-05-10: OAuth round-trip completed, credentials written, follow-up
`km agent run --no-bedrock` worked. Browser auto-opened without intervention.

### Post-checkpoint fixes (commits d500e44 + 83112ee)

Two operator-facing defects surfaced during UAT and were fixed before close:

**1. Verification false-negative (`stat` bug)** — commit d500e44

`verifyCredentialsWritten` used `stat '%s' 2>/dev/null && echo ok || echo missing`
and compared `strings.TrimSpace(out) == "ok"`. `stat` writes file metadata to
stdout BEFORE the `&& echo ok`, so the output is multi-line (`metadata\nok`).
TrimSpace yields the full block, never equal to `"ok"`. Operator saw
`Login successful` followed by "session exited but claude credentials not
found" on the happy path. Fixed by switching to
`test -f '%s' && echo ok || echo missing` (no metadata on stdout).

The two existing pre-checks in `shell.go`/`agent.go` use the same broken
pattern but compare against `"missing"` — they work by accident (file-present
yields metadata != "missing", proceeds correctly). Left untouched to keep
the fix surgical.

**2. Browser auto-open** — commit d500e44

claude on a headless EC2 can't spawn a browser; it prints the OAuth URL.
Operator had to copy/paste into their laptop browser. Added a parallel
SSM-poller goroutine that watches a tee'd stdout file
(`/tmp/km-claude-auth-<sb>.out`) for the OAuth URL, then opens it on the
operator's laptop via `open` / `xdg-open` / `rundll32`. `browserOpener` is
a package var so tests stub it. Polls 50 × 0.5s then gives up silently.

**3. Paste UX polish** — commit 83112ee

claude reads the OAuth code with stdin echo disabled, so paste appears
invisible. Added a framed step-by-step instructions block before the SSM
session opens, and a "✓ OAuth code accepted" line above the credentials
confirmation.

### Deferred (not blocking)

- **Truly paste-less `--claude` flow.** claude's OAuth client is registered
  with redirect_uri `https://platform.claude.com/oauth/code/callback` (manual-
  paste URL, no localhost endpoint). Browser isn't trying to reach localhost
  so the SSM tunnel pattern (codex Wave 2 / vscode Phase 73) can't apply.
  Options for the future: (a) token transfer from operator's Keychain,
  (b) macOS-only AppleScript browser-URL observation. Both add scope.
- **Real `****abcd` masked echo during paste.** Would require wrapping SSM
  in a PTY layer (creack/pty dep). Operator opted for the cheap framed-
  instructions UX instead.

### Scenarios remaining for operator (optional, not blocking)

- Scenario C: `km shell --no-bedrock <fresh-sb>` without credentials → hint error
- Scenario E: paused sandbox → clear "not running" error
- Bonus: active agent run → conflict refuse

## Pre-existing Test Failures (Out of Scope)

Two cmd package tests fail before Phase 78 work (pre-existing):
- `TestStep11d_Success_WritesChannelIDParam` — SSM path mismatch in create_slack_test.go
- `TestAtList_WithRecords` — at_test.go regression

Five pkg/compiler tests also fail pre-Phase-78. Documented in `deferred-items.md`.

## Open Follow-ups (Plan 02)

- `runAgentAuthCodex` stub → replace with real port-forward + `codex login` implementation
- AUTH-08 (TestProbeCodexPort_Primary), AUTH-09 (TestProbeCodexPort_Fallback), AUTH-10 (TestProbeCodexPort_BothInUse) — all ship in Plan 02

## Self-Check: PASSED

- `internal/app/cmd/agent_auth.go` — FOUND (258 lines, min 150)
- `internal/app/cmd/agent_auth_test.go` — FOUND (583 lines, min 200)
- Commit d26838b — FOUND
- Commit 9b342cd — FOUND
- `agent.go` contains `newAgentAuthCmd` — VERIFIED
- `shell.go` contains `km agent auth` hint — VERIFIED
- `make build` — PASSED
- All 12 target tests — PASSED
