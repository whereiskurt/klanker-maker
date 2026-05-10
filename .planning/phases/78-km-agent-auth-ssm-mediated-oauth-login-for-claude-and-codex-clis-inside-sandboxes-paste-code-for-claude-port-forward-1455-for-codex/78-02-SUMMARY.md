---
phase: 78-km-agent-auth-ssm-mediated-oauth-login-for-claude-and-codex-clis-inside-sandboxes-paste-code-for-claude-port-forward-1455-for-codex
plan: 02
subsystem: km-agent-auth
tags: [cli, oauth, ssm, port-forward, codex, cobra]
dependency_graph:
  requires:
    - phase: 78-plan-01
      provides: "runAgentAuthCodexFn dispatch var, verifyCredentialsWritten, checkAgentSessionConflict, resolveAuthDeps"
  provides:
    - "km agent auth <sandbox> --codex: real SSM port-forward + codex login lifecycle"
    - "probeCodexPort: 1455/1457 local port probe with clear error on collision"
  affects: [km-agent-auth, km-shell, km-agent-run]
tech-stack:
  added: []
  patterns: ["net.Listen probe-and-close for local port availability", "buildPortForwardCmd background subprocess with deferred Kill", "foreground SSM session after background port-forward warmup"]
key-files:
  created: []
  modified:
    - internal/app/cmd/agent_auth.go
    - internal/app/cmd/agent_auth_test.go
key-decisions:
  - "localPort:remotePort are both set to the probed port (1455 or 1457) because codex binds the same port number on both the sandbox side and expects the client callback at the same port — no mismatch needed"
  - "deferred pfCmd.Process.Kill() placed immediately after pfCmd.Start() success to cover all exit paths including panics, because runSSMInteractiveSubprocess masks SIGINT process-wide"
  - "1s sleep after pfCmd.Start() gives session-manager-plugin time to bind the local port before codex login starts (same pattern as runVSCodeStart)"
  - "URL relay deferred — codex prints OAuth URL to stdout already visible in the operator's SSM terminal; no auto-open added in v1 (plan recommendation followed)"
patterns-established:
  - "Background port-forward with deferred Kill before foreground interactive session: establishes cleanup guarantee on all exit paths"
  - "probeCodexPort: probe-and-close idiom for sequential port candidates with a unified error on exhaustion"
requirements-completed: [AUTH-08, AUTH-09, AUTH-10, INT-01]
duration: ~8min
completed: 2026-05-10
---

# Phase 78 Plan 02: km agent auth --codex Wave 2 Summary

**SSM port-forward codex OAuth flow: probeCodexPort (1455/1457), background AWS-StartPortForwardingSession, deferred Kill, foreground SSM codex login session, verifyCredentialsWritten to ~/.codex/auth.json**

## Performance

- **Duration:** ~8 min
- **Started:** 2026-05-10T21:54:20Z
- **Completed:** 2026-05-10T22:02:12Z (Tasks 1+2; Task 3 awaiting manual UAT)
- **Tasks:** 2 of 3 (Task 3 is checkpoint:human-verify — pending operator UAT)
- **Files modified:** 2

## Accomplishments

- `probeCodexPort` implemented: tries 1455, falls back to 1457, returns clear error when both occupied
- `runAgentAuthCodex` stub fully replaced with real lifecycle: probe → background pfCmd → deferred Kill → 1s sleep → foreground SSM codex login → verifyCredentialsWritten
- 3 new unit tests: AUTH-08 (Primary), AUTH-09 (Fallback), AUTH-10 (BothInUse) all GREEN
- All Plan 01 regression tests still green (AUTH-01..07, AUTH-11..13)
- `make build` succeeds with version v0.2.583

## Task Commits

1. **Task 1: Add probeCodexPort + AUTH-08..10 unit tests** - `a0dc0d7` (feat)
2. **Task 2: Replace runAgentAuthCodex stub with real lifecycle** - `020eb8f` (feat)
3. **Task 3: Manual UAT — Scenarios B and D** - PENDING (checkpoint:human-verify)

## Files Created/Modified

- `internal/app/cmd/agent_auth.go` (452 lines, +92 net) — probeCodexPort function; runAgentAuthCodex real implementation replacing Wave-1 stub; strconv + net imports added
- `internal/app/cmd/agent_auth_test.go` (775 lines, +92 net) — TestProbeCodexPort_Primary/Fallback/BothInUse added; net import added

## Decisions Made

1. **localPort == remotePort for SSM port-forward** — The plan says 1:1 (1455:1455 or 1457:1457). codex on the sandbox binds whichever of {1455, 1457} is free; the SSM tunnel uses the same port on both ends. No mismatch needed. Empirical confirmation expected from UAT Scenario B.

2. **No URL auto-open for codex path** — Unlike the --claude path (which added a parallel SSM poller goroutine + browserOpener), codex already prints the OAuth URL to the operator's SSM session stdout where they can click it. Adding auto-open would require another parallel goroutine and tee path; plan says "skip for v1, let operator click from terminal". Followed plan recommendation.

3. **deferred Kill before runSSMInteractiveSubprocess** — Placed `defer func() { pfCmd.Process.Kill() }()` immediately after `pfCmd.Start()` succeeds, before the 1s sleep and before the foreground session. This is the safest placement: covers all return paths including errors during the sleep or foreground session startup.

## Deviations from Plan

None — plan executed exactly as written. Implementation matches the Example 5 skeleton from RESEARCH.md and mirrors the vscode.go runVSCodeStart pattern.

## Manual UAT Status

**PENDING** — Task 3 (checkpoint:human-verify) requires operator to run:
- **Scenario B** (happy path): `km agent auth <sandbox> --codex`, complete OAuth via browser, verify `~/.codex/auth.json` written
- **Scenario D** (port collision): bind 1455 + 1457 locally, verify graceful error with expected wording
- **Fallback scenario**: bind only 1455, verify km uses 1457

**Open question for UAT**: Does codex on the sandbox also try 1457 when 1455 is taken (i.e., does it bind 1457 on the sandbox side when we forward 1457:1457)? The implementation uses 1:1 localPort:remotePort. If codex always binds only 1455 on the sandbox side regardless of what port the operator's laptop is using, then the 1457 fallback path would need `localPort=1457, remotePort=1455`. This must be confirmed empirically during UAT Scenario (Fallback port).

## Requirements Covered

| Requirement | Test | Status |
|-------------|------|--------|
| AUTH-08 | TestProbeCodexPort_Primary | PASS |
| AUTH-09 | TestProbeCodexPort_Fallback | PASS |
| AUTH-10 | TestProbeCodexPort_BothInUse | PASS |
| INT-01 | Manual UAT Scenario B | PENDING |

## Self-Check: PARTIAL (Tasks 1+2 complete, Task 3 pending)

- `internal/app/cmd/agent_auth.go` — FOUND (452 lines)
- `internal/app/cmd/agent_auth_test.go` — FOUND (775 lines)
- Commit a0dc0d7 (Task 1) — FOUND
- Commit 020eb8f (Task 2) — FOUND
- `probeCodexPort` function in agent_auth.go — VERIFIED
- `runAgentAuthCodex` stub error message removed — VERIFIED (no "ships in Plan 02" string)
- `defer pfCmd.Process.Kill()` present in runAgentAuthCodex — VERIFIED
- `make build` — PASSED (v0.2.583)
- AUTH-08, AUTH-09, AUTH-10 tests — PASSED
- All Plan 01 regression tests — PASSED
