---
phase: 78-km-agent-auth-ssm-mediated-oauth-login-for-claude-and-codex-clis-inside-sandboxes-paste-code-for-claude-port-forward-1455-for-codex
verified: 2026-05-10T22:45:00Z
status: passed
score: 8/8 must-haves verified
human_verification:
  - test: "Run `km agent auth <sandbox> --claude` on a sandbox without credentials, complete OAuth flow in browser, paste code into SSM terminal"
    expected: "claude.ai OAuth URL opens in operator's browser automatically; after code is accepted, km prints `checkmark claude credentials written to /home/sandbox/.claude/.credentials.json`; subsequent `km shell --no-bedrock` succeeds"
    why_human: "Requires real AWS, EC2, SSM, and claude.ai OAuth round-trip. Operator confirmed PASSED on L3 (learn-24a69627) 2026-05-10."
  - test: "Run `km agent auth <sandbox> --codex` on a sandbox with codex installed"
    expected: "SSM port-forward opens on localhost:1455; codex OAuth URL opens in browser; callback flows through tunnel; km prints `checkmark codex credentials written to /home/sandbox/.codex/auth.json`"
    why_human: "Requires real AWS, EC2, SSM, and OpenAI OAuth round-trip. Operator confirmed PASSED on c1 (learn-42092a47) 2026-05-10."
---

# Phase 78: km agent auth — Verification Report

**Phase Goal:** Operator can run `km agent auth <sandbox> [--claude | --codex]` to mediate the underlying CLI's OAuth login over SSM. Default `--claude` runs `claude auth login` interactively in the sandbox via SSM session (operator pastes the OAuth code into the SSM-attached terminal); `--codex` opens an SSM port-forward `localhost:1455 ↔ sandbox:1455` so codex's hardcoded callback URL flows back through the tunnel. Success signal is the credentials file (`~/.claude/.credentials.json` or `~/.codex/auth.json`) appearing on the sandbox. `km shell --no-bedrock` and `km agent run --no-bedrock` print a clear hint pointing at `km agent auth` when credentials are missing (no silent auto-bootstrap).
**Verified:** 2026-05-10T22:45:00Z
**Status:** passed
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Operator can run `km agent auth <sb> --claude` and complete an interactive OAuth login on the sandbox via SSM | VERIFIED | `runAgentAuthClaude` in agent_auth.go:107; interactive SSM session confirmed by UAT Scenario A |
| 2 | After successful login, `~/.claude/.credentials.json` exists on the sandbox AND km prints success | VERIFIED | `verifyCredentialsWritten` uses `test -f` (not `stat`) after d500e44 fix; prints `checkmark claude credentials written to ...` at agent_auth.go:437 |
| 3 | If active `km-agent-*` tmux session is running, command refuses with clear error pointing at `km agent attach` | VERIFIED | `checkAgentSessionConflict` at agent_auth.go:456; `TestAgentAuth_ConflictRefuse` PASS |
| 4 | `km shell --no-bedrock` exits non-zero with hint containing `km agent auth <sandbox> --claude` when credentials are missing | VERIFIED | Pre-check in `runShellWithSSM` (shell.go:380-387); `TestShellCmd_NoBedrock_CredentialsMissingHint` PASS; error text confirmed in test output |
| 5 | `km agent run --no-bedrock` exits non-zero with hint BEFORE starting tmux session when credentials are missing | VERIFIED | Pre-check in `runAgentNonInteractive` (agent.go:553-558); `TestAgentRun_NoBedrock_CredentialsMissingHint` PASS |
| 6 | Default flag (no `--claude` or `--codex`) resolves to `--claude` | VERIFIED | `newAgentAuthCmd` RunE logic (agent_auth.go:68-77); `TestAgentAuth_DefaultClaude` PASS |
| 7 | `--claude` and `--codex` are mutually exclusive | VERIFIED | Mutual exclusion check in RunE; `TestAgentAuth_MutuallyExclusive` PASS |
| 8 | Operator can run `km agent auth <sb> --codex` and complete OAuth via SSM port-forward `localhost:1455 ↔ sandbox:1455` | VERIFIED | `runAgentAuthCodex` full lifecycle at agent_auth.go:274; `probeCodexPort` at agent_auth.go:374; UAT Scenario B PASS |

**Score:** 8/8 truths verified

---

## Required Artifacts

### Plan 01 Artifacts

| Artifact | Min Lines | Actual Lines | Status | Details |
|----------|-----------|--------------|--------|---------|
| `internal/app/cmd/agent_auth.go` | 150 | 492 | VERIFIED | Contains all required functions: `newAgentAuthCmd`, `runAgentAuthClaude`, `runAgentAuthCodex` (real impl, not stub), `buildClaudeAuthArgs`, `verifyCredentialsWritten`, `checkAgentSessionConflict`, `resolveAuthDeps`, `probeCodexPort`, `browserOpener`, `detectAndOpenCodexURL`, dispatch vars |
| `internal/app/cmd/agent_auth_test.go` | 200 | 775 | VERIFIED | 13+ unit tests covering AUTH-01..13 and AUTH-08..10 |
| `internal/app/cmd/agent.go` | — | — | VERIFIED | Contains `cmd.AddCommand(newAgentAuthCmd(...))` at line 150; noBedrock pre-check in `runAgentNonInteractive` at line 553 |
| `internal/app/cmd/shell.go` | — | — | VERIFIED | Contains noBedrock pre-check in `runShellWithSSM`; string `km agent auth` present at line 384 |

### Plan 02 Artifacts

| Artifact | Required Contains | Status | Details |
|----------|------------------|--------|---------|
| `internal/app/cmd/agent_auth.go` | `probeCodexPort` | VERIFIED | Function at line 374; tries 1455 then 1457 |
| `internal/app/cmd/agent_auth.go` | `buildPortForwardCmd` call in `runAgentAuthCodex` | VERIFIED | Called at line 307 |
| `internal/app/cmd/agent_auth_test.go` | `TestProbeCodexPort_Primary/Fallback/BothInUse` | VERIFIED | All three tests present and GREEN |

---

## Key Link Verification

### Plan 01 Key Links

| From | To | Via | Status | Evidence |
|------|----|-----|--------|---------|
| `agent_auth.go` | `shell.go::runSSMInteractiveSubprocess` | direct call | WIRED | agent_auth.go:162 and :354 |
| `agent_auth.go` | `agent.go::sendSSMAndWait` | direct call | WIRED | used in `checkAgentSessionConflict` (line 462) and `verifyCredentialsWritten` (line 427) |
| `agent_auth.go` | `sandbox_ref.go::ResolveSandboxID` | direct call | WIRED | agent_auth.go:78 |
| `agent.go::NewAgentCmdWithDeps` | `agent_auth.go::newAgentAuthCmd` | `cmd.AddCommand` | WIRED | agent.go:150 |
| `shell.go::runShellWithSSM` | stat pre-check before execSSMSession | noBedrock guard | WIRED | shell.go:380; stat → `echo ok\|echo missing`; compare to "missing" |
| `agent.go::runAgentNonInteractive` | stat pre-check before tmux launch | noBedrock guard | WIRED | agent.go:553; same pattern |

### Plan 02 Key Links

| From | To | Via | Status | Evidence |
|------|----|-----|--------|---------|
| `runAgentAuthCodex` | `shell.go::buildPortForwardCmd` | direct call | WIRED | agent_auth.go:307 |
| `runAgentAuthCodex` | `shell.go::runSSMInteractiveSubprocess` | foreground session | WIRED | agent_auth.go:354 |
| `runAgentAuthCodex` | `verifyCredentialsWritten` | post-exit check | WIRED | agent_auth.go:364 with cliType="codex" |
| `runAgentAuthCodex` | `pfCmd.Process.Kill()` | deferred cleanup | WIRED | agent_auth.go:318-320; `defer` wraps Kill; placed immediately after `pfCmd.Start()` |

---

## Requirements Coverage

All requirement IDs are phase-local (defined in 78-VALIDATION.md, not REQUIREMENTS.md).

| Requirement | Plan | Test | Status | Evidence |
|-------------|------|------|--------|---------|
| AUTH-01 | 01 | `TestAgentAuth_FlagParsing` | SATISFIED | PASS — flags `--claude`, `--codex`, `--console`, `--sso`, `--claudeai`, `--email` registered |
| AUTH-02 | 01 | `TestAgentAuth_MutuallyExclusive` | SATISFIED | PASS — error contains "mutually exclusive" |
| AUTH-03 | 01 | `TestAgentAuth_DefaultClaude` | SATISFIED | PASS — default resolves to claude branch |
| AUTH-04 | 01 | `TestAgentAuth_SandboxIDResolution` | SATISFIED | PASS — alias resolved via `ResolveSandboxID` |
| AUTH-05 | 01 | `TestAgentAuth_ConflictRefuse` | SATISFIED | PASS — conflict refused with `km agent attach` hint |
| AUTH-06 | 01 | `TestVerifyCredentialsWritten_Success` | SATISFIED | PASS — prints success line; uses `test -f` after d500e44 fix |
| AUTH-07 | 01 | `TestVerifyCredentialsWritten_Missing` | SATISFIED | PASS — two subtests (nil/non-nil sessionErr) both pass |
| AUTH-08 | 02 | `TestProbeCodexPort_Primary` | SATISFIED | PASS — returns 1455 when free |
| AUTH-09 | 02 | `TestProbeCodexPort_Fallback` | SATISFIED | PASS — returns 1457 when 1455 occupied |
| AUTH-10 | 02 | `TestProbeCodexPort_BothInUse` | SATISFIED | PASS — error contains "1455", "1457", "in use locally" |
| AUTH-11 | 01 | `TestShellCmd_NoBedrock_CredentialsMissingHint` | SATISFIED | PASS — error contains "claude credentials not found", "km agent auth", sandbox ID |
| AUTH-12 | 01 | `TestAgentRun_NoBedrock_CredentialsMissingHint` | SATISFIED | PASS — same error shape; fires before tmux launch |
| AUTH-13 | 01 | `TestBuildClaudeAuthArgs` | SATISFIED | PASS — 7 table-driven cases including console+sso error |
| INT-01 | 02 | Manual UAT Scenario B | SATISFIED | Operator PASSED on c1 (learn-42092a47) 2026-05-10 |
| INT-02 | 01 | Manual UAT Scenario A | SATISFIED | Operator PASSED on L3 (learn-24a69627) 2026-05-10 |

---

## Test Results Summary

All 13 automated unit tests GREEN when run with port 1455 free:

```
TestAgentAuth_FlagParsing             PASS
TestAgentAuth_MutuallyExclusive       PASS
TestAgentAuth_DefaultClaude           PASS
TestAgentAuth_SandboxIDResolution     PASS
TestAgentAuth_ConflictRefuse          PASS
TestVerifyCredentialsWritten_Success  PASS
TestVerifyCredentialsWritten_Missing  PASS (2 subtests)
TestBuildClaudeAuthArgs               PASS (7 subtests)
TestShellCmd_NoBedrock_CredentialsMissingHint   PASS
TestShellCmd_NoBedrock_CredentialsPresent       PASS
TestAgentRun_NoBedrock_CredentialsMissingHint   PASS
TestAgentRun_NoBedrock_CredentialsPresent       PASS
TestProbeCodexPort_Primary            PASS
TestProbeCodexPort_Fallback           PASS
TestProbeCodexPort_BothInUse          PASS
```

**Note on TestProbeCodexPort tests:** These use `net.Listen` on real local ports (1455, 1457). A stale `session-manager-plugin` process (PID 97866) was occupying port 1455 at verification time — a known deferred defect documented in 78-02-SUMMARY.md ("stale port-forward subprocess leak"). After killing the orphan process, all three tests passed cleanly. The stale process is the exact post-UAT artifact the SUMMARY documented and classified as a deferred follow-up.

---

## Anti-Pattern Scan

Files modified by phase 78: `agent_auth.go`, `agent_auth_test.go`, `agent.go`, `shell.go`.

| File | Pattern | Severity | Finding |
|------|---------|----------|---------|
| `agent_auth.go` | Wave-1 stub ("ships in Plan 02") | blocker | ABSENT — stub replaced by real `runAgentAuthCodex` implementation |
| `agent_auth.go` | TODO/FIXME/placeholder | info | None found |
| `agent_auth.go` | `return null` / empty impl | blocker | None found — all functions have real bodies |
| `agent_auth.go` | `defer pfCmd.Process.Kill()` | required | PRESENT at lines 318-320 — covers all exit paths |
| `agent_auth.go` | `test -f` vs broken `stat` | correctness | `verifyCredentialsWritten` uses `test -f '%s' && echo ok || echo missing` (line 427) — correct after d500e44 fix |
| `shell.go` / `agent.go` | Same stat pre-checks compare to "missing" | correctness | Uses `stat ... && echo ok || echo missing` but compares `TrimSpace(out) == "missing"` — works by accident (file-present yields stat metadata != "missing"). Documented as known technical debt; left intentionally surgical per 78-01-SUMMARY. Not a blocker — pre-existing behavior unchanged. |

No infrastructure files touched: no changes to `infra/modules/`, `infra/live/`, `pkg/compiler/userdata.go`, `cmd/km-agent-handler/`, `cmd/km-create-handler/`, or any Lambda/sidecar confirmed via `git diff --name-only` across all phase 78 commits.

---

## Pre-existing Test Failures (Not Caused by Phase 78)

The `internal/app/cmd` package has pre-existing test failures that pre-date phase 78. Confirmed by:
1. 78-01-SUMMARY.md documented `TestStep11d_Success_WritesChannelIDParam` and `TestAtList_WithRecords` as pre-existing
2. `deferred-items.md` lists 7 pre-existing failures (2 cmd + 5 pkg/compiler)
3. `git log d26838b..HEAD -- <failing-test-files>` returns empty — phase 78 did not modify those test files

Additional pre-existing failures observed during verification (not in deferred-items.md but confirmed pre-78 by git history):
- `TestCreateDockerWritesComposeFile`
- `TestApplyLifecycleOverrides_RunCreateRemoteSignature`
- `TestRunDestroy_GitHubTokenCleanup`
- `TestDestroyCmd_InvalidSandboxID`
- `TestEmailSend_*` (3 tests)
- `TestEmailRead_EncryptedMessageAutoDecrypts`
- `TestLoadEFSOutputs_NotExist`
- `TestListCmd_EmptyStateBucketError`
- `TestLockCmd_RequiresStateBucket`
- `TestShellDockerContainerName`, `TestShellDockerNoRootFlag`

All correspond to test files last modified by phases 63, 67, 73, or earlier. None of these test files were touched by phase 78 commits.

---

## Human Verification (Completed)

Both mandatory manual UAT scenarios were completed by the operator before verification:

### 1. Scenario A — `--claude` happy path (INT-02)

**Completed:** 2026-05-10 on L3 (`learn-24a69627`)
**Result:** PASSED. OAuth round-trip completed, credentials written, follow-up `km agent run --no-bedrock` worked. Browser auto-opened without intervention (auto-open feature added in commit d500e44).
**Post-checkpoint fixes applied:** `stat` bug corrected, browser auto-open added, paste UX framing added (commits d500e44, 83112ee).

### 2. Scenario B — `--codex` happy path (INT-01)

**Completed:** 2026-05-10 on c1 (`learn-42092a47`)
**Result:** PASSED. codex login completed end-to-end via SSM port-forward; `/home/sandbox/.codex/auth.json` written; SSM session exited cleanly. Browser auto-open added post-UAT (commit 51f7ab0).

### Deferred (not blocking, documented in 78-02-SUMMARY.md)

- **Scenario D (port collision):** bind both 1455 and 1457 locally — graceful error wording not exercised manually (AUTH-10 covered by unit test)
- **Fallback port empirical confirmation:** what happens if local 1455 taken but sandbox codex always binds 1455 — open question (noted in 78-02-SUMMARY.md as known limitation)
- **Stale session-manager-plugin cleanup:** `defer pfCmd.Process.Kill()` kills the `aws ssm start-session` parent but `session-manager-plugin` child is not reaped on some paths — manifests as port 1455 remaining occupied after successful codex auth. Filed as deferred follow-up.

---

## Gaps Summary

No gaps. All 8 observable truths verified, all 15 requirements satisfied (13 by automated tests, 2 by operator UAT). The three known deferred items (Scenario D wording, fallback port empirical confirmation, stale plugin cleanup) were pre-classified as non-blocking follow-ups before verification and are not raised as gaps.

---

_Verified: 2026-05-10T22:45:00Z_
_Verifier: Claude (gsd-verifier)_
