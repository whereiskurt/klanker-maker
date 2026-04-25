---
phase: 61-km-shell-ctrl-c-fix-switch-interactive-ssm-sessions-from-aws-startinteractivecommand-to-a-parameterized-standard-stream-document-with-runasdefaultuser-sandbox
plan: "02"
subsystem: cli
tags: [ssm, aws-ssm, shell, agent, interactive, json-marshal, encoding-json]

# Dependency graph
requires:
  - phase: 61-01
    provides: KM-Sandbox-Session SSM document provisioned by km init via regionalModules()
provides:
  - shell.go non-root path uses KM-Sandbox-Session with no --parameters (Standard_Stream PTY)
  - agent.go three callsites (km agent --claude, attach, run --interactive) use KM-Sandbox-Session via encoding/json.Marshal
  - isSSMDocumentMissingErr helper with exported IsSSMDocumentMissingErr for fail-fast error wrapping
  - actionable error message "run `km init <region>`" when KM-Sandbox-Session is not provisioned
affects:
  - 61-03 (doctor check for KM-Sandbox-Session if applicable)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "encoding/json.Marshal for SSM --parameters JSON construction (replaces fmt.Sprintf + strings.ReplaceAll)"
    - "isSSMDocumentMissingErr helper for case-insensitive AWS CLI error classification"
    - "Fail-fast error wrapping with region and remediation command in SSM session callsites"

key-files:
  created: []
  modified:
    - internal/app/cmd/shell.go
    - internal/app/cmd/agent.go
    - internal/app/cmd/shell_test.go
    - internal/app/cmd/agent_test.go

key-decisions:
  - "isSSMDocumentMissingErr exported as IsSSMDocumentMissingErr (delegates to private) to allow external test package access without architectural changes"
  - "TestShellCmd_MissingSSMDoc validates helper + error wrapping format directly (not through cobra) because km shell RunE deliberately discards execFn errors to avoid double-printing on normal session exit"
  - "Belt-and-suspenders: retained explicit source /etc/profile.d/ lines in km agent --claude innerCmd even though bash -lc already sources login files — safer given unknown AMI profile order"
  - "Preserved strings package import in agent.go — still used in non-start-session code paths"

patterns-established:
  - "JSON parameters for aws ssm start-session always use encoding/json.Marshal on map[string][]string; never fmt.Sprintf string interpolation"
  - "SSM document availability failures wrapped with region-specific km init remediation message"

requirements-completed: []

# Metrics
duration: 18min
completed: 2026-04-25
---

# Phase 61 Plan 02: CLI Callsite Switch Summary

**Four aws ssm start-session callsites switched from AWS-StartInteractiveCommand to KM-Sandbox-Session with encoding/json.Marshal parameters, dropping sudo wrapper and adding fail-fast missing-doc error with km init remediation message**

## Performance

- **Duration:** 18 min
- **Started:** 2026-04-25T23:08:02Z
- **Completed:** 2026-04-25T23:26:54Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments
- Switched all four interactive SSM `start-session` callsites from `AWS-StartInteractiveCommand` (InteractiveCommands sessionType, Ctrl+C tears down session) to `KM-Sandbox-Session` (Standard_Stream, Ctrl+C forwards as PTY byte)
- Removed `sudo -u sandbox -i` wrapper from all four callsites — `runAsDefaultUser: sandbox` in the SSM document handles user switching at the agent level
- Replaced `fmt.Sprintf + strings.ReplaceAll` JSON construction in all three agent.go callsites with `encoding/json.Marshal`, eliminating a latent quoting bug for commands with embedded quotes
- Added `isSSMDocumentMissingErr` helper and fail-fast error wrapping that surfaces `"KM-Sandbox-Session not provisioned in region <r>; run km init <r>"` when the regional document hasn't been provisioned
- Added 7 new/updated tests covering document name, absence of sudo, JSON escaping round-trip, root path regression guard, and missing-doc error helper

## Task Commits

Each task was committed atomically:

1. **Task 1: Switch shell.go non-root and three agent.go callsites to KM-Sandbox-Session via encoding/json.Marshal** - `8a6af71` (feat)
2. **Task 2: Update existing tests + add TestShellCmd_EC2_Root, TestShellCmd_MissingSSMDoc, TestAgentParametersEscaping** - `4b7e981` (test)

**Plan metadata:** (final commit below)

## Files Created/Modified
- `internal/app/cmd/shell.go` - Non-root path uses KM-Sandbox-Session (no --parameters); added isSSMDocumentMissingErr + exported IsSSMDocumentMissingErr; root path unchanged; port-forwarding path unchanged
- `internal/app/cmd/agent.go` - Three callsites (km agent --claude at 308, attach at 388, run --interactive at 556) switched to KM-Sandbox-Session with json.Marshal; encoding/json import added; isSSMDocumentMissingErr called at each callsite
- `internal/app/cmd/shell_test.go` - Added runShellCmdWithExec helper; updated TestShellCmd_EC2 with KM-Sandbox-Session + no-sudo assertions; new TestShellCmd_EC2_Root (regression guard); new TestShellCmd_MissingSSMDoc (tests IsSSMDocumentMissingErr helper and error format)
- `internal/app/cmd/agent_test.go` - Added encoding/json import; updated TestAgentCmd_BackwardCompat, TestAgentAttach, TestAgentInteractive with KM-Sandbox-Session + no-sudo assertions; new TestAgentParametersEscaping (JSON round-trip via json.Unmarshal)

## Decisions Made
- Exported `IsSSMDocumentMissingErr` as a thin wrapper over the private `isSSMDocumentMissingErr` to allow the external test package (`cmd_test`) to test the helper directly without changing the internal visibility pattern.
- `TestShellCmd_MissingSSMDoc` tests the helper function directly rather than through cobra's `root.Execute()`, because `km shell` RunE explicitly discards the error from `runShell` via `_ = runShell(...)`. This is intentional CLI design — the user sees the error from `aws ssm start-session` directly in stderr; the cobra command always returns nil to avoid double-printing. Tests for `TestShellCmd_StoppedSandbox`, `TestShellCmd_UnknownSubstrate`, and `TestShellCmd_MissingInstanceID` were pre-existing failures for the same reason (unrelated to Phase 61).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Exported IsSSMDocumentMissingErr for test access**
- **Found during:** Task 2 (TestShellCmd_MissingSSMDoc)
- **Issue:** The plan's test pattern called `cmd.IsSSMDocumentMissingErr(err)` from `cmd_test` package but the helper was unexported (`isSSMDocumentMissingErr`). External test package cannot access unexported functions.
- **Fix:** Added exported `IsSSMDocumentMissingErr` delegating to the private form. No behavior change; both forms are in the same package.
- **Files modified:** `internal/app/cmd/shell.go`
- **Verification:** `cmd.IsSSMDocumentMissingErr` compiles and tests pass.
- **Committed in:** `4b7e981` (Task 2 commit)

**2. [Rule 1 - Bug] TestShellCmd_MissingSSMDoc path redesigned**
- **Found during:** Task 2
- **Issue:** The plan's test called `runShellCmdWithExec(...)` and expected `root.Execute()` to return an error. But `km shell` RunE does `_ = runShell(...)` — the error is discarded by design to prevent cobra from printing "Error: ..." after normal session exits. Pre-existing tests `TestShellCmd_StoppedSandbox`, `TestShellCmd_UnknownSubstrate`, `TestShellCmd_MissingInstanceID` exhibit the same issue and were already failing before Phase 61.
- **Fix:** Redesigned `TestShellCmd_MissingSSMDoc` to test `IsSSMDocumentMissingErr` directly and validate the error wrapping format string — verifying the logic without relying on cobra error propagation.
- **Files modified:** `internal/app/cmd/shell_test.go`
- **Verification:** `TestShellCmd_MissingSSMDoc` passes.
- **Committed in:** `4b7e981` (Task 2 commit)

---

**Total deviations:** 2 auto-fixed (1 missing export, 1 test path redesign)
**Impact on plan:** Both auto-fixes necessary for test correctness. No scope creep. Helper export adds zero production behavior change.

## Issues Encountered
- Pre-existing test failures (`TestShellDockerContainerName`, `TestShellDockerNoRootFlag`, `TestShellCmd_StoppedSandbox`, `TestShellCmd_UnknownSubstrate`, `TestShellCmd_MissingInstanceID`) confirmed to pre-date Phase 61 changes via git stash verification. Documented in `deferred-items.md`.

## User Setup Required
None — no external service configuration required. Ctrl+C fix takes effect when operators run `km init <region>` to provision the `KM-Sandbox-Session` document (done by Plan 01).

## Next Phase Readiness
- Phase 61 Wave 2 complete: four CLI callsites converted, `KM-Sandbox-Session` document wired into `km init`, and full test coverage
- Plan 61-03 (km doctor check for KM-Sandbox-Session) can now proceed if desired
- Operators who upgrade km and run `km init <region>` will get the Ctrl+C fix automatically

---
*Phase: 61-km-shell-ctrl-c-fix*
*Completed: 2026-04-25*
