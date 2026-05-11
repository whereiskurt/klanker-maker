---
phase: 79-km-presence-daemon
plan: "00"
subsystem: km-presence-daemon
tags:
  - stub-seeding
  - tdd
  - go
dependency_graph:
  requires: []
  provides:
    - cmd/km-presence package skeleton with commandRunner seam
    - 13 failing test stubs for Wave 1 daemon implementation
    - doctor_presence.go stub for Wave 2 doctor check
  affects:
    - internal/app/cmd/doctor.go (registration deferred to Plan 79-04)
tech_stack:
  added:
    - cmd/km-presence: new Go binary package (sandbox-side liveness daemon skeleton)
  patterns:
    - commandRunner interface injection for subprocess mocking (per CONTEXT.md "prefer testability")
    - CWLogsFilterAPI narrow interface mirroring cmd/configui/handlers.go pattern
    - fakeSandboxLister renamed to fakeRunningSandboxLister to avoid conflict with existing fake in doctor_ebs_test.go
key_files:
  created:
    - cmd/km-presence/main.go
    - cmd/km-presence/runner.go
    - cmd/km-presence/main_test.go
    - cmd/km-presence/runner_test.go
    - internal/app/cmd/doctor_presence.go
    - internal/app/cmd/doctor_presence_test.go
  modified: []
decisions:
  - "Named fake runningSandboxLister as fakeRunningSandboxLister to avoid redeclaration conflict with existing fakeSandboxLister (uses kmaws.SandboxRecord) in doctor_ebs_test.go"
metrics:
  duration: "181s"
  completed_date: "2026-05-11"
  tasks_completed: 3
  files_created: 6
  files_modified: 0
---

# Phase 79 Plan 00: km-presence Daemon Wave 0 Stub Seeding Summary

Wave 0 stub seeding for Phase 79 (km-presence daemon): 6 files with commandRunner injection seam, 13 failing signal/tick/emit tests, and doctor_presence.go stub returning hard-coded WARN.

## Objective

Create the package skeleton, commandRunner injection seam, and failing test stubs so that all downstream plans (Wave 1 daemon implementation, Wave 2 doctor check) have something to compile and link against from the moment they begin.

## Stub Files Created

| Path | Purpose |
|------|---------|
| `cmd/km-presence/main.go` | `package main` skeleton with empty `main()` and testable `run() int` entrypoint |
| `cmd/km-presence/runner.go` | `commandRunner` interface, `realRunner`, 5 signal stubs, `tick()`, `emit()` |
| `cmd/km-presence/main_test.go` | 13 failing test stubs covering all 5 signals + emit logic + stamp semantics |
| `cmd/km-presence/runner_test.go` | `fakeRunner` test double + sanity test |
| `internal/app/cmd/doctor_presence.go` | `checkPresenceDaemonHealthy` stub returning hard-coded WARN; `CWLogsFilterAPI` interface |
| `internal/app/cmd/doctor_presence_test.go` | 3 failing test stubs: OK/Stale/Skipped cases |

## Locked Function Signatures (Plan 79-01 must not drift)

```go
// commandRunner interface (runner.go)
type commandRunner interface {
    Output(name string, args ...string) ([]byte, error)
}

// 5 signal checks (runner.go) — stubs return false/nil
func checkLoginShells(r commandRunner) bool                            // signal 1
func checkTmuxClients(r commandRunner) bool                            // signal 2
func checkInboundEmail(mailDir, stampPath string) bool                 // signal 3
func checkInboundSlack(slackStampPath, presenceStampPath string) bool  // signal 4
func checkAgentProcess(r commandRunner) bool                           // signal 5

// Daemon helpers (runner.go) — stubs return false/false and nil
func tick(r commandRunner, sandboxID, mailDir, slackStampPath, presenceStampPath string) (bool, bool)
func emit(sandboxID string) error
```

## 13 Test Stubs in main_test.go

| Test Name | Signal/Behavior Covered |
|-----------|------------------------|
| `TestSignal_LoginShell_Positive` | Signal 1: `who` returns non-empty → active |
| `TestSignal_LoginShell_Negative` | Signal 1: `who` returns empty → inactive |
| `TestSignal_TmuxClients_Positive` | Signal 2: tmux list-clients returns output → active |
| `TestSignal_TmuxClients_NegativeNoServer` | Signal 2: tmux exit code 1 (no server) → inactive |
| `TestSignal_Email_Positive` | Signal 3: mail file newer than stamp → active |
| `TestSignal_Email_NegativeNoNewerFile` | Signal 3: no new mail since stamp → inactive |
| `TestSignal_Slack_Positive` | Signal 4: last-slack-inbound newer than presence stamp → active |
| `TestSignal_Slack_NegativeStampMissing` | Signal 4: slack stamp missing → inactive |
| `TestSignal_AgentProcess_Positive` | Signal 5: pgrep returns PIDs → active |
| `TestSignal_AgentProcess_NegativeEmpty` | Signal 5: pgrep exit 1 (no match) → inactive |
| `TestTick_NoEmitWhenAllNegative` | Emit logic: all signals negative → no heartbeat |
| `TestTick_EmitWhenAnyPositive` | Emit logic: any signal positive → heartbeat emitted |
| `TestTick_StampAlwaysTouched` | Stamp semantics: unconditional touch end-of-tick |

## 3 Test Stubs in doctor_presence_test.go

| Test Name | Case Covered |
|-----------|-------------|
| `TestDoctor_PresenceDaemonHealthy_OK` | CW returns recent event → CheckOK |
| `TestDoctor_PresenceDaemonHealthy_Stale` | CW returns no events → CheckWarn with sandbox ID |
| `TestDoctor_PresenceDaemonHealthy_Skipped` | nil CW client → CheckSkipped |

## Verification Results

- `go build ./...`: GREEN (full repo builds)
- `go vet ./cmd/km-presence/... ./internal/app/cmd/...`: GREEN (no issues)
- `go test ./cmd/km-presence/...`: RED (7 tests fail — stubs return false/false; desired Wave 0 state)
- `go test ./internal/app/cmd/... -run 'TestDoctor_PresenceDaemonHealthy'`: RED (2 of 3 fail — stub always returns CheckWarn; desired Wave 0 state)

## Deviations from Plan

**1. [Rule 1 - Bug] Renamed fakeRunningSandboxLister to avoid redeclaration**

- **Found during:** Task 3 — `go vet` flagged `fakeSandboxLister redeclared in this block`
- **Issue:** `doctor_ebs_test.go:69` declares `fakeSandboxLister` for `kmaws.SandboxRecord`-based listing; plan used the same name for the presence check's `runningSandboxLister` interface
- **Fix:** Renamed the presence-test fake from `fakeSandboxLister` to `fakeRunningSandboxLister`
- **Files modified:** `internal/app/cmd/doctor_presence_test.go`
- **Commit:** 2f20a79

## Commits

| Task | Name | Commit | Key Files |
|------|------|--------|-----------|
| 1 | Package skeleton + commandRunner seam | 777df47 | cmd/km-presence/main.go, cmd/km-presence/runner.go |
| 2 | Failing test stubs (5 signals + emit + stamp) | c097d09 | cmd/km-presence/main_test.go, cmd/km-presence/runner_test.go |
| 3 | doctor_presence.go stub + test stubs | 2f20a79 | internal/app/cmd/doctor_presence.go, internal/app/cmd/doctor_presence_test.go |

## Self-Check: PASSED
