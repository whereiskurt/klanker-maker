---
phase: 79-km-presence-daemon
plan: "01"
subsystem: km-presence-daemon
tags:
  - daemon
  - tdd
  - go
  - signal-checks
dependency_graph:
  requires:
    - 79-00 (commandRunner seam, 13 failing test stubs)
  provides:
    - cmd/km-presence/runner.go: all 5 signal check implementations + tick() + emit()
    - cmd/km-presence/main.go: 60s ticker daemon loop with SIGTERM/SIGINT shutdown
    - 14 GREEN tests (13 Wave 0 stubs + fakeRunner sanity)
    - /tmp/km-presence-linux ELF binary for Plan 79-03 S3 upload
  affects:
    - Plan 79-03 (Makefile): binary at cmd/km-presence/ builds with CGO_ENABLED=0
    - Plan 79-04 (doctor): runner.go tick/emit contract unchanged (source:"presence")
tech_stack:
  added: []
  patterns:
    - emitFn var seam for tick() test injection (no subprocess in tick unit tests)
    - os.OpenFile + os.Chtimes for stamp touch (no subprocess, testable)
    - pgrep -afE for ERE alternation on AL2023 pgrep (BRE default)
    - tmux list-clients without -t (lists all sessions, default socket)
key_files:
  created: []
  modified:
    - cmd/km-presence/runner.go
    - cmd/km-presence/main.go
    - cmd/km-presence/main_test.go
decisions:
  - "pgrep -afE: AL2023 pgrep defaults to BRE; -E enables ERE so | alternation works in single subprocess call"
  - "tmux list-clients without -t: lists clients across all sessions on default socket; matches internal/app/cmd/agent.go:423 convention"
  - "emitFn var seam: allows tick() tests to intercept emit without writing to /run/km/audit-pipe"
  - "os.Chtimes for stamp touch: avoids exec subprocess, fully testable in TempDir"
metrics:
  duration: "~12min"
  completed_date: "2026-05-11"
  tasks_completed: 2
  files_created: 0
  files_modified: 3
---

# Phase 79 Plan 01: km-presence Daemon Implementation Summary

Implemented the km-presence daemon body: 5 signal checks, tick(), emit(), and the 60s ticker main loop. All 13 Wave 0 test stubs are GREEN; cross-compiled Linux ELF binary is 2.4 MB.

## Objective

Fill in runner.go signal check stubs and wire main.go daemon loop so the binary, when run with SANDBOX_ID set, ticks every 60s, evaluates all 5 presence signals, and emits heartbeat events to /run/km/audit-pipe iff any signal is positive.

## Implementation Files

| Path | Purpose | LoC |
|------|---------|-----|
| `cmd/km-presence/runner.go` | 5 signal checks + tick() + emit() + touchStamp() | 180 |
| `cmd/km-presence/main.go` | Daemon loop: 60s ticker, immediate first tick, SIGTERM/SIGINT | 79 |
| `cmd/km-presence/main_test.go` | 13 signal/tick tests updated with correct fakeRunner keys | ~200 |

**Total daemon LoC (main.go + runner.go): 259** (target was ~150 LoC; overage is comments and blank lines — substantive code is ~150 LoC).

## Test Results — All 14 GREEN

| Test Name | Signal/Behavior | Result |
|-----------|----------------|--------|
| `TestSignal_LoginShell_Positive` | Signal 1: `who` non-empty → true | PASS |
| `TestSignal_LoginShell_Negative` | Signal 1: `who` empty → false | PASS |
| `TestSignal_TmuxClients_Positive` | Signal 2: tmux list-clients output → true | PASS |
| `TestSignal_TmuxClients_NegativeNoServer` | Signal 2: tmux exit 1 → false | PASS |
| `TestSignal_Email_Positive` | Signal 3: mail newer than stamp → true | PASS |
| `TestSignal_Email_NegativeNoNewerFile` | Signal 3: no new mail → false | PASS |
| `TestSignal_Slack_Positive` | Signal 4: slack stamp newer → true | PASS |
| `TestSignal_Slack_NegativeStampMissing` | Signal 4: slack stamp missing → false | PASS |
| `TestSignal_AgentProcess_Positive` | Signal 5: pgrep returns PIDs → true | PASS |
| `TestSignal_AgentProcess_NegativeEmpty` | Signal 5: pgrep exit 1 → false | PASS |
| `TestTick_NoEmitWhenAllNegative` | All negative → emitFn not called | PASS |
| `TestTick_EmitWhenAnyPositive` | Signal 1 positive → emitFn called once | PASS |
| `TestTick_StampAlwaysTouched` | Stamp created/updated unconditionally | PASS |
| `TestFakeRunner_ReturnsConfiguredOutput` | fakeRunner seam sanity | PASS |

## Resolved Open Questions

### 1. pgrep -E flag (RESEARCH Open Question 2)

**Resolution:** Use `pgrep -afE '(^|/)claude( |$)|(^|/)codex( |$)|km-agent-run\.sh'`.

AL2023's pgrep defaults to BRE (Basic Regular Expressions) where `|` is a literal character, not alternation. The `-E` flag switches to ERE (Extended Regular Expressions) where `|` is alternation. Without `-E`, the regex would never match.

**Documented in code:** `runner.go` — comment above `checkAgentProcess` body: "Decision: pgrep -E for ERE alternation. AL2023's pgrep defaults to BRE and would not match | in the regex without -E."

**Test key updated:** Wave 0 stubs used `"pgrep -af ..."` — updated to `"pgrep -afE ..."` to match the actual implementation.

### 2. tmux list-clients target syntax (RESEARCH Open Question 3)

**Resolution:** Use `runuser -u sandbox -- tmux list-clients` (NO `-t` flag).

Without `-t`, `list-clients` lists all clients across all sessions on the default socket — exactly what Signal 2 wants. Adding `-t ''` (empty target) would fail with tmux error; adding `-t '*'` would require a specific session.

**Documented in code:** `runner.go` — comment on `checkTmuxClients`: "No -t flag — list-clients without target lists clients across all sessions on default socket. Convention from internal/app/cmd/agent.go:423."

**Test key updated:** Wave 0 stubs used `"runuser -u sandbox -- tmux list-clients -t "` — updated to `"runuser -u sandbox -- tmux list-clients"`.

## Cross-Compile Confirmation

```
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o /tmp/km-presence-linux ./cmd/km-presence/
```

Output: `/tmp/km-presence-linux` — ELF 64-bit LSB executable, x86-64, statically linked, stripped, 2,470,072 bytes (2.4 MB).

## Notes for Plan 79-03 (Makefile)

- Binary builds with `CGO_ENABLED=0` (statically linked, no glibc dep).
- Entrypoint: `./cmd/km-presence/` (standard `go build` target).
- No build tags required.
- No new dependencies added to `go.mod` (zerolog was already present at v1.33.0).
- The `-trimpath -ldflags '-s -w'` flags match the existing sidecar build pattern (see Makefile `build-sidecars` target).

## Deviations from Plan

### 1. [Rule 2 - Missing functionality] Added emitFn var seam to tick()

- **Found during:** Task 1 — plan mentioned making emit injectable for `TestTick_*` tests but did not specify the exact mechanism
- **Issue:** Without injection, `TestTick_EmitWhenAnyPositive` would call the real `emit()` which runs bash, introducing a subprocess dependency in tick unit tests
- **Fix:** Added `var emitFn = emit` at file scope in `runner.go`; `tick()` calls `emitFn()` instead of `emit()` directly; tests swap `emitFn` in-place and restore via `defer`
- **Files modified:** `cmd/km-presence/runner.go`, `cmd/km-presence/main_test.go`
- **Commit:** 403bc06

### 2. [Rule 2 - Missing functionality] Added touchStamp() helper

- **Found during:** Task 1 — plan mentioned `os.OpenFile + os.Chtimes` approach but as inline code; extracting to a named helper makes `tick()` body fit in < 30 LoC target and improves readability
- **Fix:** Extracted `func touchStamp(path string)` in `runner.go`
- **Files modified:** `cmd/km-presence/runner.go`
- **Commit:** 403bc06

### 3. Test key updates (not a deviation — expected by plan)

Wave 0 test stubs used placeholder keys for tmux (`-t `) and pgrep (`-af`). Updated to match the correct implementation: `"runuser -u sandbox -- tmux list-clients"` and `"pgrep -afE ..."`. The plan explicitly directed updating test stubs.

## Commits

| Task | Name | Commit | Key Files |
|------|------|--------|-----------|
| 1 | 5 signal checks + tick() + emit() + 13 tests GREEN | 403bc06 | cmd/km-presence/runner.go, cmd/km-presence/main_test.go |
| 2 | main() loop: 60s ticker, SIGTERM/SIGINT, zerolog | f10d055 | cmd/km-presence/main.go |

## Self-Check: PASSED
