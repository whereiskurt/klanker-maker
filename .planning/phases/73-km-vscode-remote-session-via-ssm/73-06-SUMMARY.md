---
phase: 73-km-vscode-remote-session-via-ssm
plan: 06
subsystem: cli
tags: [vscode, ssh, ssm, port-forward, sshconfig, cobra]

requires:
  - phase: 73-01
    provides: sshkey generation (GenerateAndWrite) used at sandbox create time
  - phase: 73-03
    provides: UpsertHost / HostOptions for ~/.ssh/config management
  - phase: 73-05
    provides: keypair generation wired into km create; ~/.km/keys/<id> exists on local machine

provides:
  - "km vscode start <sb> — SSM port-forward opener with ssh-config upsert and connection instructions"
  - "km vscode status <sb> — single-SSM-round-trip health check with 4-case error discrimination"
  - "km vscode registered as a top-level command in root.go"
  - "Six passing vscode unit tests (Wave 0 stubs activated)"

affects: [vscode, km-vscode, operator-ux, phase-73]

tech-stack:
  added: []
  patterns:
    - "resolveVSCodeDeps: nil-safe dep injection returning error, mirrors runAgentNonInteractive inline pattern"
    - "parseVSCodeStatus: shared helper decouples SSM output parsing from callers (start + status reuse)"
    - "captureStdout pipe helper: os.Pipe redirect pattern for white-box tests that write to os.Stdout"
    - "vsCodeSSMMock: fixed-output SSMSendAPI mock for white-box cmd package tests"

key-files:
  created: []
  modified:
    - internal/app/cmd/vscode.go
    - internal/app/cmd/vscode_test.go
    - internal/app/cmd/root.go

key-decisions:
  - "parseVSCodeStatus extracted as shared helper so both runVSCodeStart and runVSCodeStatus use identical 4-case discrimination logic without duplication"
  - "resolveVSCodeDeps returns (SandboxFetcher, ShellExecFunc, SSMSendAPI, error) matching the inline nil-check pattern from agent.go; no blank interface{}  tricks needed"
  - "vsCodeStatusScript defined as package-level const (not inline) so tests can assert on consistent output parsing"
  - "vscode_test.go kept in package cmd (white-box) to allow direct runVSCodeStart/runVSCodeStatus calls without exporting them"

patterns-established:
  - "Single combined SSM script (vsCodeStatusScript) for both pre-flight and status — one round-trip covers sshd state + authkeys existence + content"
  - "4-case error discrimination: (!sshd && !authkeys) = vscodeEnabled=false; (!authkeys only) = unexpected; (!sshd only) = sshd crashed; (both) = healthy"

requirements-completed: [GOAL-2, GOAL-3, GOAL-4, GOAL-5]

duration: 15min
completed: 2026-05-08
---

# Phase 73 Plan 06: km vscode start/status Implementation Summary

**`km vscode start` and `km vscode status` implemented as fully-functional cobra commands with SSM pre-flight, ssh-config upsert, 4-case error discrimination, and 6 passing unit tests**

## Performance

- **Duration:** 15 min
- **Started:** 2026-05-08T00:22:04Z
- **Completed:** 2026-05-08T00:37:04Z
- **Tasks:** 3
- **Files modified:** 3

## Accomplishments

- `km vscode start <sb>` resolves sandbox, checks `~/.km/keys/<id>`, runs single-round-trip SSM pre-flight, upserts `~/.ssh/config`, prints connection block, then opens blocking SSM port-forward
- `km vscode status <sb>` sends the same combined SSM script and returns a 4-case diagnostic (vscodeEnabled=false / unexpected / sshd-down / healthy)
- `km vscode` registered in `root.go` immediately after `NewSlackCmd`; operators can now run `km vscode --help`
- Six Wave-0 stub tests activated: all 6 TestVSCode* tests pass, no SKIPs

## Task Commits

1. **Task 1+2: Implement vscode.go with start and status** - `9b5d6e0` (feat)
2. **Task 2: Register NewVSCodeCmd in root.go** - `46a0b03` (feat)
3. **Task 3: Activate six vscode tests** - `23500c4` (test)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/internal/app/cmd/vscode.go` - Production `runVSCodeStart`, `runVSCodeStatus`, `parseVSCodeStatus`, `resolveVSCodeDeps`; `newVSCodeStartCmd` with `--local-port` flag; `newVSCodeStatusCmd`
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/vscode_test.go` - `vsCodeSSMMock`, `vsCodeFetcherMock`, `captureStdout` helper; 6 activated tests
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/root.go` - `root.AddCommand(NewVSCodeCmd(cfg))` after `NewSlackCmd`

## Decisions Made

- `parseVSCodeStatus` extracted as a shared helper so both `runVSCodeStart` (pre-flight path) and `runVSCodeStatus` (status path) share identical 4-case output parsing without code duplication
- `resolveVSCodeDeps` follows the exact inline pattern from `runAgentNonInteractive` in agent.go: loads AWS config if fetcher==nil, constructs real SSM client from same config
- vscode_test.go kept in `package cmd` (white-box) so tests directly call `runVSCodeStart` / `runVSCodeStatus` without requiring exported symbols; `vsCodeSSMMock` defined locally since agent_test.go's mocks are in `package cmd_test` (inaccessible)
- `--local-port` flag defaults to 2222 per plan spec; `UpsertHost` Port field receives the same value so the ssh-config entry is always consistent with the active port-forward

## Operator Flows Implemented

### start flow (happy path)
1. `fetcher.FetchSandbox` → EC2 instance ID + region
2. `os.Stat(~/.km/keys/<id>)` → fails fast with portability hint if missing
3. `sendSSMAndWait(vsCodeStatusScript)` → 4-case parse
4. `UpsertHost(~/.ssh/config, "km-<id>", {localhost, <port>, sandbox, <privPath>})`
5. Print connection block to stdout
6. `buildPortForwardCmd` → `execFn(pfCmd)` (blocks until Ctrl-C)

### status flow
1-3 same as start (minus key check)
4. Return nil (exit 0) or descriptive error (non-zero exit)

### 4-case error discrimination
| sshd | authkeys | Error |
|------|----------|-------|
| inactive | absent | "VS Code not enabled...vscodeEnabled: true...recreate" |
| active | absent | "unexpected state: sshd running but authorized_keys absent" |
| inactive | present | "sshd is not running...sudo systemctl start sshd" |
| active | present | nil (healthy) |

## Deviations from Plan

None - plan executed exactly as written. The only consolidation was writing `runVSCodeStatus` into the same file write as `runVSCodeStart` (they're both in vscode.go per plan), committed as Task 1 to avoid a spurious intermediate state.

## Issues Encountered

Pre-existing `pkg/compiler` test failures (4 tests) unrelated to this plan — confirmed by stashing changes and re-running. These failures are in scope for a separate plan. Added to deferred-items.

## Next Phase Readiness

Phase 73 Wave 4 complete. All `km vscode` operator-facing commands are production-ready:
- `km vscode start <sb>` — opens SSM tunnel for VS Code Remote-SSH
- `km vscode status <sb>` — reports health with clear per-case errors
- `--local-port N` respected in both ssh-config and port-forward args
- Six unit tests confirm all error paths and the happy path

---
*Phase: 73-km-vscode-remote-session-via-ssm*
*Completed: 2026-05-08*
