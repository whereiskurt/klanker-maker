---
phase: 62-claude-code-operator-notify-hook-for-permission-and-idle-events
plan: 03
subsystem: compiler/testing
tags: [hook, bash, testing, km-send, HOOK-05]
dependency_graph:
  requires: [62-02]
  provides: [HOOK-05-verified]
  affects: [pkg/compiler]
tech_stack:
  added: []
  patterns:
    - Go shell-out tests for bash scripts (exec.Command + stdin injection)
    - Stub via PATH prepend with per-test KM_NOTIFY_LAST_FILE isolation
    - Heredoc extraction from rendered user-data string
key_files:
  created:
    - pkg/compiler/notify_hook_script_test.go
    - pkg/compiler/testdata/notify-hook-fixture-notification.json
    - pkg/compiler/testdata/notify-hook-fixture-stop.json
    - pkg/compiler/testdata/notify-hook-fixture-transcript.jsonl
  modified:
    - pkg/compiler/userdata.go (Rule 1 auto-fix: empty array bash -u fix)
decisions:
  - "Use Go shell-out tests (not bats) — consistent with existing pkg/compiler test infrastructure"
  - "extractNotifyHookScript substitutes /opt/km/bin/km-send -> km-send for PATH injection without root"
  - "KM_NOTIFY_LAST_FILE env override (already in Plan 02 hook) provides cooldown isolation per test"
metrics:
  duration: 3 minutes
  completed_date: "2026-04-28"
  tasks_completed: 2
  files_changed: 5
---

# Phase 62 Plan 03: Hook Script Runtime Tests Summary

**One-liner:** Go shell-out test suite executing the km-notify-hook bash script through 7 scenarios covering all HOOK-05 invariants (gate, notification, stop/transcript, cooldown, failure tolerance, --body file).

## What Was Built

### Task 1: Test Fixtures

Three fixture files in `pkg/compiler/testdata/`:

- `notify-hook-fixture-notification.json` — Claude Code Notification payload with `__TRANSCRIPT_PATH__` placeholder for runtime path substitution.
- `notify-hook-fixture-stop.json` — Claude Code Stop payload with same placeholder.
- `notify-hook-fixture-transcript.jsonl` — 5-line JSONL with two `assistant` entries; the LAST entry contains "I've finished refactoring the auth middleware..." which the hook's `jq | tail -n 1` must select.

### Task 2: Test Implementation

`pkg/compiler/notify_hook_script_test.go` — Go-based test driver for the hook script.

**Key helpers:**

| Helper | Purpose |
|--------|---------|
| `extractNotifyHookScript` | Renders user-data via `generateUserData`, locates the `KM_NOTIFY_HOOK_EOF` heredoc, extracts body, substitutes `/opt/km/bin/km-send` → `km-send` |
| `setupHookEnv` | Creates per-test tmpdir, writes hook + stub, sets `KM_NOTIFY_TEST_LOG`, `KM_NOTIFY_LAST_FILE`, PATH |
| `runHook` | `exec.Command("bash", hookPath, event)` with stdin injection and env layering |
| `readStubLog` | Parses `=== km-send call ===` / `=== end ===` blocks from stub log into `stubCall` structs |
| `argListContains` | Checks individual `arg:` lines in stub log |
| `writeTranscript` | Copies transcript JSONL into tmpdir, returns path |
| `loadFixture` | Reads fixture JSON and substitutes `__TRANSCRIPT_PATH__` |

**Test coverage (HOOK-05 sub-requirements):**

| Test | Requirement | Assertion |
|------|------------|-----------|
| `TestNotifyHook_GatedOff` | HOOK-05a | `KM_NOTIFY_ON_PERMISSION=0` → exit 0, log absent/empty |
| `TestNotifyHook_Notification` | HOOK-05b | `=1` → 1 km-send call, subject `[sb-test] needs permission`, body has message text + footer |
| `TestNotifyHook_Notification_RecipientOverride` | HOOK-05 override | `KM_NOTIFY_EMAIL` → `--to team@example.com` in argList |
| `TestNotifyHook_Stop` | HOOK-05c | Stop + transcript → subject `[sb-test] idle`, body has LAST assistant text |
| `TestNotifyHook_Cooldown` | HOOK-05d | Two calls within 10s → only first calls km-send |
| `TestNotifyHook_SendFailure_StillExitsZero` | HOOK-05e | `KM_NOTIFY_TEST_FAIL=1` → stub logs call but hook exits 0 |
| `TestNotifyHook_BodyViaFile_NotStdin` | HOOK-05 invariant | `--body` flag present, `body_file` matches `/tmp/km-notify-body.*`, content non-empty |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] bash -u nounset error on empty to_args array**

- **Found during:** Task 2 (TestNotifyHook_Notification failing with "to_args[@]: unbound variable")
- **Issue:** The hook script uses `set -euo pipefail`. When `KM_NOTIFY_EMAIL` is unset, `to_args=()` creates an empty array. Expanding `"${to_args[@]}"` under bash `-u` (nounset) throws "unbound variable" on macOS bash (which is strict about empty arrays), causing the hook to exit non-zero — violating the "never blocks Claude" invariant.
- **Fix:** Changed `"${to_args[@]}"` → `${to_args[@]+"${to_args[@]}"}` in `pkg/compiler/userdata.go` line 421. This is the standard bash idiom for nounset-safe empty array expansion.
- **Files modified:** `pkg/compiler/userdata.go`
- **Commit:** a488b71

## Coverage Gap (Acknowledged)

The following are exercised only in Plan 05 UAT (not this plan):

- Real SES delivery via km-send
- Real Claude Code subprocess hook invocation (env inheritance from Claude process)
- NTP-skew clock edge cases for cooldown
- Transcript files produced by actual Claude Code sessions

## Self-Check: PASSED
