---
phase: 86-km-create-prompt-queue
plan: "04"
subsystem: cmd/create-prompt-queue
tags: [golang, ssm, polling, typed-error, cobra, pq-05, pq-06, tdd, green-state]
dependency_graph:
  requires:
    - 86-02 (doStep16PromptPush base + --wait flag registered)
    - 86-03 (on-box runner writes meta.json status transitions)
  provides:
    - waitForQueueDrain polling helper + ExitCodeError typed-error pattern
    - PQ-05 + PQ-06 GREEN
    - Single os.Exit(exitErr.Code) at outermost cobra boundary (root.go Execute)
  affects:
    - 86-05 (km agent list --queue — independent wave, no dependency)
    - 86-06 (full UAT with real sandbox)
tech_stack:
  added: []
  patterns:
    - ExitCodeError typed-error pattern (Cobra-community idiom; preserves RunE deferred cleanup)
    - Package-level var for test-overridable poll interval (QueuePollInterval)
    - Exported wrappers for internal helpers (WaitForQueueDrain) to enable cmd_test external package access
    - errors.As outermost-boundary detection (single os.Exit call site in Execute())
key_files:
  created: []
  modified:
    - internal/app/cmd/create_prompt.go (+~180 lines: ExitCodeError type + waitForQueueDrain + parseQueueStatuses + fetchFailedExitCode + WaitForQueueDrain export + QueuePollInterval var + doStep16PromptPush wait param)
    - internal/app/cmd/create_prompt_test.go (~340 lines replaced: PQ-05 + PQ-06 stubs → PASS + 2 new tests GREEN)
    - internal/app/cmd/create.go (~8 lines: pass wait through to doStep16PromptPush; drop _ = wait suppression)
    - internal/app/cmd/root.go (+12 lines: errors.As(*ExitCodeError) detection in Execute(); add "errors" import)
decisions:
  - "ExitCodeError typed error (not inline os.Exit): preserves RunE/Cobra deferred cleanup; matches Cobra-community idioms (kubectl pattern)"
  - "Single os.Exit(exitErr.Code) at outermost Execute() boundary in root.go — one call site only"
  - "QueuePollInterval exported (PascalCase) so cmd_test package can override it without package-internal access"
  - "WaitForQueueDrain exported wrapper for cmd_test access to internal helper"
  - "SilenceErrors NOT set on create cmd: Cobra prints the typed error message once via its normal error path; Execute() boundary does NOT double-print (no fmt.Fprintf before os.Exit)"
  - "fetchFailedExitCode SSM shell: reads most recent runs/<runID>/exit_code among failed entries; falls back to 1 if unparseable"
  - "QueuePollInterval first-poll-immediately pattern: time.NewTimer(0) fires in select before ticker, so first read is instant (no 5s wait before first status check)"
metrics:
  duration: "~15 minutes"
  completed: "2026-05-20T03:25:52Z"
  tasks: 1
  files_created: 0
  files_modified: 4
---

# Phase 86 Plan 04: --wait Polling + ExitCodeError Pattern (Wave 2) Summary

Wave 2 closes the `--wait` loop: `km create --prompt foo --wait` now blocks until the on-box queue runner marks all entries terminal. Exit codes propagate from the first-failed entry via a typed `*ExitCodeError` that round-trips cleanly through Cobra's RunE error path. PQ-05 and PQ-06 flip from SKIP to GREEN.

## One-liner

waitForQueueDrain polls meta.json every 5s via SSM; first-failure exit code propagates via typed ExitCodeError detected at outermost cobra boundary (root.go Execute) via errors.As.

## What Was Done

### Task 1: ExitCodeError type + waitForQueueDrain + doStep16PromptPush + root.go boundary

**create_prompt.go changes (+~180 lines):**

1. **`ExitCodeError` type** — `Code int`, `Inner error`; `Error()` emits `"queue chain failed (exit code N): <inner>"` (or without inner if nil); `Unwrap()` returns `Inner` for `errors.Is` chain traversal.

2. **`QueuePollInterval` exported var** — `5 * time.Second` default. `var` (not `const`) enables cmd_test package to override to `10ms` for test speed. Named with PascalCase to export across the `cmd` → `cmd_test` boundary.

3. **`waitForQueueDrain`** — internal function (unexported), drives the polling loop:
   - Fires first poll immediately via `time.NewTimer(0)` in `select` (no 5s wait before first read)
   - Polls `QueuePollInterval` thereafter via `time.NewTicker`
   - Runs SSM shell: `ls -1 /workspace/.km-agent/queue/*.meta.json | jq -r .status`; output format `NNN|status` per line
   - Calls `parseQueueStatuses` (pure function — no SSM, unit-testable in isolation)
   - When all entries are in `{done, skipped, failed}` and at least one is `failed`: calls `fetchFailedExitCode` for the actual exit code
   - Respects `ctx.Done()` — exits within one poll cycle on cancellation

4. **`parseQueueStatuses`** — pure function over string output; splits on `|`; skips blank/malformed lines.

5. **`fetchFailedExitCode`** — SSM shell: walks `runs/<runID>/status` for most recent `failed` entry and reads `runs/<runID>/exit_code`; falls back to `1` if unparseable (ensures non-zero is always propagated).

6. **`WaitForQueueDrain` exported wrapper** — delegates to `waitForQueueDrain`; enables `cmd_test` (external package) to call the helper directly without import tricks.

7. **`doStep16PromptPush` signature extended** — added `wait bool` parameter:
   - When `wait=false` (default): prints "queue armed with N prompt(s); returning (use --wait to block)" and returns nil
   - When `wait=true`: calls `waitForQueueDrain`; on exit code 0 prints "queue drained (all N prompt(s) done)" and returns nil; on non-zero returns `&ExitCodeError{Code: exitCode}` — **never calls `os.Exit` inside this function**

**create.go changes (~8 lines):**
- Removed `_ = wait` (suppression comment → active variable)
- Both `doStep16PromptPush` call sites now pass `wait` as the last argument
- Both return the error unchanged (`return err` not `return fmt.Errorf("prompt queue push failed: %w", err)`) so the `*ExitCodeError` flows up without losing its type through `%w` wrapping would have been fine, but direct return is cleaner

**root.go changes (+12 lines):**
- Added `"errors"` import
- In `Execute()`: after `root.Execute()` returns an error, detect `*ExitCodeError` via `errors.As(err, &exitErr)` and call `os.Exit(exitErr.Code)`. Other errors fall through to `os.Exit(1)`.
- This is the ONE place `os.Exit` is called for typed exit codes — preserving all defer cleanup registered by RunE and Cobra middleware.

### Tests flipped GREEN

| Test | Status | What it verifies |
|------|--------|-----------------|
| `TestCreatePromptWait` (PQ-05) | SKIP → PASS | 3-poll mock converges to all-done; returns (0, nil) |
| `TestCreatePromptWaitFail` (PQ-06) | SKIP → PASS | 1-poll mock returns failed|skipped; fetchFailedExitCode returns "42"; returns (42, nil) |
| `TestExitCodeError_ErrorAndUnwrap` | NEW → PASS | Error() contains code + inner; errors.Is unwraps Inner; no-inner case doesn't panic |
| `TestDoStep16PromptPush_ExitCodeError_RoundTrips` | NEW → PASS | WaitForQueueDrain → *ExitCodeError → errors.As extracts Code 99; plain error does NOT match |

## Design Decisions Explained

### Why `*ExitCodeError` (not inline `os.Exit`)

`os.Exit` inside `doStep16PromptPush` would skip:
- Any `defer` in the `RunE` function body (close channels, log flushes, etc.)
- Cobra's own post-RunE cleanup (signal handlers, usage suppression)
- AWS SDK connection cleanup (if any goroutines are tracked)

The typed error round-trips through `RunE`'s `error` return cleanly. Cobra's normal error path runs all defers. The outermost layer (just outside `root.Execute()`) inspects with `errors.As` and only *then* calls `os.Exit(exitErr.Code)`.

This matches the kubectl/Cobra community idiom (`cmdutil.CheckErr` + typed errors).

### Where the single `os.Exit(exitErr.Code)` lives

`internal/app/cmd/root.go` — the `Execute()` function, immediately after `root.Execute()` returns. This is the outermost point in the process lifecycle. By the time `os.Exit` fires, RunE's entire call stack (including all defers) has already unwound normally.

### `SilenceErrors` choice

`SilenceErrors` is NOT set on the create command. Cobra's default behavior prints the error message (via `cmd.PrintErrln`) when `RunE` returns a non-nil error. The `Execute()` boundary does NOT additionally print the error message before calling `os.Exit(exitErr.Code)`. Result: the typed error message prints exactly once, via Cobra's normal error formatting path.

### Why `QueuePollInterval` is a `var`, not `const`

Go `const` values cannot be overridden at runtime. Tests in `cmd_test` (external package) need to set `cmd.QueuePollInterval = 10 * time.Millisecond` to avoid 5s-per-poll latency (3 polls × 5s = 15s for PQ-05). The `var` pattern is standard for test-overridable timing in Go.

### First-poll-immediately pattern

`waitForQueueDrain` uses `time.NewTimer(0)` in the `select` alongside the `ticker.C`. `Timer(0)` fires immediately on the first select iteration. This avoids a mandatory 5s wait before the first status read — operators running `km create --prompt foo --wait` see output promptly.

## Verification Results

```
make build                         → Built km v0.2.705 (7f33a30) — exit 0
TestCreatePromptWait (PQ-05)       → PASS (3.01s — 3 SSM calls × 1s sendSSMAndWait initial delay)
TestCreatePromptWaitFail (PQ-06)   → PASS (2.00s — 2 SSM calls × 1s)
TestExitCodeError_ErrorAndUnwrap   → PASS (0.00s)
TestDoStep16PromptPush_ExitCodeError_RoundTrips → PASS (2.00s)
grep os.Exit create_prompt.go (non-comment) → 0 matches — PASS
errors.As(*ExitCodeError) in root.go → confirmed
Full test suite (./internal/app/cmd/)
  → FAIL: TestUnlockCmd_RequiresStateBucket (pre-existing AWS SSO expiry — confirmed present before Phase 86)
  → All other tests PASS; 0 new regressions
```

Pre-existing failure `TestUnlockCmd_RequiresStateBucket` confirmed via `git stash` + re-run: fails identically without Phase 86 changes (AWS SSO OIDC `InvalidGrantException`; operator-environment credential issue).

## Deviations from Plan

### Auto-fixed Issues

None — plan executed exactly as written.

### Scope Notes

- `sendSSMAndWait`'s internal 1-second initial poll delay (in agent.go) means each queue status read costs ~1s of wall time in tests. This is a pre-existing constraint (agent.go is Phase 86-05 territory). Tests still pass within reasonable time (7.8s total for the 4 targeted tests). Not changed.
- `queuePollInterval` was upgraded to exported `QueuePollInterval` (plan spec said `queuePollInterval` lowercase but the test example used `cmd.QueuePollInterval` — exported form chosen to satisfy cmd_test external package access).

## Commits

| Hash | Message |
|------|---------|
| 9483530 | feat(86-04): implement --wait polling + ExitCodeError typed-error pattern |

## Setup for Next Plans

- **Plan 86-05** (`km agent list --queue`): independent wave; can land in parallel. The `waitForQueueDrain` helper and `km-queue.service` unit are already on-box (86-03). The `--queue` flag on `km agent list` is the only remaining CLI surface.
- **Plan 86-06** (full UAT): All 6 PQ requirements now GREEN (PQ-01..PQ-06, PQ-08). PQ-07 pending 86-05. UAT can begin after 86-05 lands.

PQ requirements status:
- PQ-01: GREEN (86-02 — flag registration)
- PQ-02: GREEN (86-02 — resolvePrompts)
- PQ-03: GREEN (86-02 — docker reject)
- PQ-04: GREEN (86-02 — pushQueueFiles SSM batch)
- PQ-05: GREEN (86-04 — this plan)
- PQ-06: GREEN (86-04 — this plan)
- PQ-07: PENDING (86-05 — km agent list --queue)
- PQ-08: GREEN (86-03 — runner state machine)

## Self-Check: PASSED

Files verified:
- `internal/app/cmd/create_prompt.go` — exists, contains ExitCodeError, WaitForQueueDrain, waitForQueueDrain, QueuePollInterval, doStep16PromptPush with wait param
- `internal/app/cmd/create_prompt_test.go` — exists, TestCreatePromptWait + TestCreatePromptWaitFail PASS (no t.Skip), new tests present
- `internal/app/cmd/create.go` — exists, both doStep16PromptPush calls pass wait
- `internal/app/cmd/root.go` — exists, errors.As(*ExitCodeError) detection + os.Exit(exitErr.Code)

Commits:
- `9483530` — verified in git log
