---
phase: 76-km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox
plan: "01"
subsystem: cli
tags: [vscode, ssh, ec2, ssm, cobra, tdd]

requires:
  - phase: 76-00
    provides: "Wave 0 test stubs (16 t.Skip skeletons) for km vscode rekey"
  - phase: 73-km-vscode-remote-ssh
    provides: "vsCodeStatusScript, parseVSCodeStatus, extractResourceID, sendSSMAndWait, resolveVSCodeDeps"

provides:
  - "km vscode rekey <sandbox-id> cobra subcommand with --force and --yes flags"
  - "runVSCodeRekey pre-flight scaffold: EC2 running-state + lock + SSM probe gates"
  - "ec2DescribeAPI interface for test injection (locked for Plan 76-02)"
  - "checkSandboxLock package-level var indirection in lock.go for test injection"
  - "8 pre-flight tests passing (CommandRegistered, FlagsExist, NotRunning, Locked_NoForce, Locked_WithForce, VSCodeDisabled, Inconsistent, SSHDDown)"

affects:
  - 76-02-km-vscode-rekey-key-generation-ssm-install-atomic-commit

tech-stack:
  added: []
  patterns:
    - "ec2DescribeAPI interface injection: cobra RunE initializes real client, tests inject mock — no build tags or network needed"
    - "checkSandboxLock package-level var indirection: same pattern as resolveVSCodeDeps for lock state testing"
    - "captureStdout wrapper suppresses pre-flight success prints and TODO marker in tests"

key-files:
  created: []
  modified:
    - "internal/app/cmd/lock.go"
    - "internal/app/cmd/vscode.go"
    - "internal/app/cmd/vscode_test.go"

key-decisions:
  - "ec2DescribeAPI interface declared in vscode.go (not a separate file) to keep the dependency surface minimal alongside SSMSendAPI"
  - "checkSandboxLock var lives in lock.go (not vscode.go) to keep the indirection co-located with CheckSandboxLock"
  - "runVSCodeRekey returns nil after pre-flight with a fmt.Printf TODO marker — Plan 76-02 deletes this line as the test seam"
  - "Gate 3 (lock check) uses checkSandboxLock variable, not CheckSandboxLock function — enables test injection without real DDB"

patterns-established:
  - "vsCodeEC2Mock struct with output/err fields implements ec2DescribeAPI — follow this shape in Plan 76-02"
  - "newRunningEC2Mock / newStoppedEC2Mock helpers — Plan 76-02 adds newSequencedSSMMock for install+verify calls"

requirements-completed:
  - REKEY-CLI-SURFACE
  - REKEY-PREFLIGHT-EC2
  - REKEY-PREFLIGHT-LOCK
  - REKEY-PREFLIGHT-SSM
  - PHASE-73-DEPENDENCY

duration: 4min
completed: "2026-05-10"
---

# Phase 76 Plan 01: km vscode rekey — CLI Surface + Pre-flight Gates Summary

**cobra `rekey` subcommand registered under `km vscode` with four ordered pre-flight gates (EC2 running, lock check with --force bypass, SSM probe via vsCodeStatusScript+parseVSCodeStatus) and test injection seams locked for Plan 76-02**

## Performance

- **Duration:** 4 min
- **Started:** 2026-05-10T01:49:16Z
- **Completed:** 2026-05-10T01:53:34Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments
- Registered `km vscode rekey <sandbox-id>` as a cobra subcommand with `--force` and `--yes` bool flags
- Implemented `runVSCodeRekey` with four pre-flight gates: fetch sandbox, EC2 running-state via DescribeInstances, lock check with --force override, SSM probe via reused vsCodeStatusScript+parseVSCodeStatus
- Added `var checkSandboxLock = CheckSandboxLock` indirection in lock.go enabling test injection without a real DynamoDB
- Added `ec2DescribeAPI` interface enabling mock injection for all EC2-dependent tests
- Turned 8 Wave 0 t.Skip stubs green; 8 Wave 2 stubs remain SKIP for Plan 76-02

## Task Commits

Each task was committed atomically:

1. **Task 1: Add lockChecker indirection + newVSCodeRekeyCmd + runVSCodeRekey pre-flight** - `6a537ff` (feat)
2. **Task 2: Turn 8 Wave 0 stubs green** - `f5cdb2f` (test)

**Plan metadata:** (docs commit follows)

## Files Created/Modified
- `internal/app/cmd/lock.go` — added `var checkSandboxLock = CheckSandboxLock` package-level indirection
- `internal/app/cmd/vscode.go` — added `ec2DescribeAPI` interface, `newVSCodeRekeyCmd` constructor, `runVSCodeRekey` pre-flight scaffold, registered rekey under `newVSCodeCmdInternal`
- `internal/app/cmd/vscode_test.go` — added `vsCodeEC2Mock`, `newRunningEC2Mock`, `newStoppedEC2Mock`; uncommented 8 pre-flight test bodies

## Decisions Made
- `ec2DescribeAPI` interface in vscode.go alongside `SSMSendAPI` — keeps EC2/SSM dependency surface co-located
- `checkSandboxLock` var in lock.go (not vscode.go) — indirection stays adjacent to `CheckSandboxLock` definition
- `runVSCodeRekey` prints `(Plan 76-02 wires up keygen + push + commit here)` as a TODO marker; Plan 76-02 deletes this exact line
- Gate 3 lock check uses `checkSandboxLock` variable (not `CheckSandboxLock` function directly) — critical for Locked_WithForce test to verify zero calls

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

`go vet ./...` reports a pre-existing IPv6 format error in `sidecars/http-proxy/httpproxy/transparent.go:204` — confirmed pre-existing via git stash check; `go vet ./internal/app/cmd/` is clean.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- Pre-flight gates locked and tested; Plan 76-02 can implement keygen + SSM install + atomic commit by extending `runVSCodeRekey` at the TODO marker
- Test injection patterns established: `vsCodeEC2Mock` (ec2DescribeAPI) + `checkSandboxLock` var override
- Plan 76-02 adds a `newSequencedSSMMock` for the two-call SSM flow (pre-flight + readback verify)
- The `(Plan 76-02 wires up keygen + push + commit here)` printf line in `runVSCodeRekey` is the exact deletion target

## Self-Check

**Files exist:**
- `internal/app/cmd/lock.go` — contains `var checkSandboxLock = CheckSandboxLock`
- `internal/app/cmd/vscode.go` — contains `newVSCodeRekeyCmd`, `runVSCodeRekey`, `ec2DescribeAPI`
- `internal/app/cmd/vscode_test.go` — contains `vsCodeEC2Mock`, `newRunningEC2Mock`, `newStoppedEC2Mock`

**Commits exist:**
- `6a537ff` feat(76-01): add km vscode rekey command + pre-flight scaffold
- `f5cdb2f` test(76-01): turn 8 Wave 0 stubs green for vscode rekey pre-flight

## Self-Check: PASSED

---
*Phase: 76-km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox*
*Completed: 2026-05-10*
