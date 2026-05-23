---
phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
plan: 03
subsystem: compiler/notify-hook
tags: [codex, hooks, notification, bash-heredoc, tdd]
dependency_graph:
  requires: [70-01]
  provides: [PermissionRequest-event-branch, last_assistant_message-Stop-fast-path]
  affects: [70-09-UAT, pkg/compiler/userdata.go]
tech_stack:
  added: []
  patterns: [bash-heredoc-extension, go-wrapped-bash-smoke-test]
key_files:
  created:
    - pkg/compiler/notify_hook_test.go
  modified:
    - pkg/compiler/userdata.go
decisions:
  - "PermissionRequest and Notification share the same KM_NOTIFY_ON_PERMISSION gate via Notification|PermissionRequest OR-pattern in case statement"
  - "PermissionRequest body reads .tool_name // .message // fallback per Plan 70-00 spike field name assumption (MEDIUM confidence; UAT Plan 70-09 verifies)"
  - "last_assistant_message field name used per SPEC.md research notes (MEDIUM confidence; 1-line fix if UAT finds different name)"
  - "transcript-tail pipeline preserved byte-for-byte inside the if [[ -z body_text ]] fallback block"
  - "Tests use existing setupHookEnv/runHook/readStubLog harness from notify_hook_script_test.go (same package)"
metrics:
  duration: 269s
  completed: 2026-05-23
  tasks: 2
  files: 2
---

# Phase 70 Plan 03: km-notify-hook Codex Parity Summary

**One-liner:** km-notify-hook bash heredoc gains a Codex PermissionRequest event branch (alias of Notification) and a last_assistant_message Stop fast-path (with Claude transcript-tail fallback), covered by 4 new Go-wrapped bash smoke tests.

## What Was Built

### Hook Script Changes (4 edited regions in userdata.go heredoc)

**Region 1 — Event dispatch case (~line 489):**
`Notification|PermissionRequest)` — both event names share the `KM_NOTIFY_ON_PERMISSION` gate in a single case clause. Codex agents get permission notifications with one env var toggle, same as Claude.

**Region 2 — Cooldown gate (~line 519):**
Extended `if [[ "$cooldown_block" -eq 1 && ( "$event" == "Notification" || "$event" == "PermissionRequest" ) ]]` — rapid Codex tool approvals don't spam operator under cooldown.

**Region 3 — do_email_branch gate (~line 683):**
`if [[ "$event" == "Notification" || "$event" == "PermissionRequest" ]]` — PermissionRequest routes into the email+Slack dispatch block alongside Notification.

**Region 4 — Body-building case (~line 711):**
- New `PermissionRequest)` case: subject = `[$sandbox_id] needs permission`, body reads `.tool_name // .message // "(permission request)"` from the Codex payload.
- Extended `Stop)` case: try `.last_assistant_message // ""` first (Codex path). If empty, fall through to the existing `transcript_path` JSONL tail-extraction pipeline (Claude path) byte-for-byte preserved.

### Test File (pkg/compiler/notify_hook_test.go)

4 Go-wrapped bash smoke tests using the existing `setupHookEnv` / `runHook` / `readStubLog` harness:

| Test | Assertion | SC |
|------|-----------|-----|
| `TestNotifyHook_CodexPermissionRequest_Fires` | km-send invoked, subject = "needs permission", body contains `apply_patch` | SC-3 |
| `TestNotifyHook_CodexPermissionRequest_GatedOff` | No km-send call when `KM_NOTIFY_ON_PERMISSION=0` | SC-3 |
| `TestNotifyHook_Stop_LastAssistantMessageFastPath` | Body contains `The answer is 4.` (NOT fallback); transcript_path nonexistent — proves fast-path fired | SC-2 |
| `TestNotifyHook_Stop_TranscriptFallbackWhenLastAssistantMissing` | Body contains `Hello from Claude tail.` from real JSONL fixture; no `last_assistant_message` in payload | SC-2 |

## Field Name Assumptions

Per Plan 70-00 spike, the Codex hook payload field names could not be verified due to ChatGPT OAuth model gating. The implementation proceeds with documented field names:

| Field | Assumed name | Where used | UAT verification |
|-------|-------------|-----------|-----------------|
| Tool name in PermissionRequest payload | `tool_name` | PermissionRequest body case | Plan 70-09 UAT step 3 |
| Last assistant message in Stop payload | `last_assistant_message` | Stop fast-path | Plan 70-09 UAT step 2 |

If UAT discovers a different field name, the fix is a 1-line change in `pkg/compiler/userdata.go` (in the applicable jq path). No re-architecture required.

## Verification Results

```
go test ./pkg/compiler/... -run 'TestNotifyHook_(CodexPermissionRequest|Stop_LastAssistantMessage|Stop_TranscriptFallback)' -count=1 -v
--- PASS: TestNotifyHook_CodexPermissionRequest_Fires (0.27s)
--- PASS: TestNotifyHook_CodexPermissionRequest_GatedOff (0.00s)
--- PASS: TestNotifyHook_Stop_LastAssistantMessageFastPath (0.15s)
--- PASS: TestNotifyHook_Stop_TranscriptFallbackWhenLastAssistantMissing (0.16s)

go test ./pkg/compiler/... -run 'TestNotifyHook' -count=1
ok  github.com/whereiskurt/klanker-maker/pkg/compiler  5.181s  (all 17 TestNotifyHook_* pass)
```

No regressions on Phase 62/63/68 hook tests.

## Deviations from Plan

None — plan executed exactly as written.

The plan's "enhanced stub" suggestion (cat `--body` file contents into sentinel) was implemented via the existing `notify-hook-stub-km-send.sh` which already captures body file contents via `body_contents_begin` / `body_contents_end` markers. No new stub variant was needed.

## SC-6 Unchanged

The existing `KM_SLACK_THREAD_TS` gate on the `do_email_branch` (line 700: `if [[ -n "${KM_SLACK_THREAD_TS:-}" ]]; then do_email_branch=0; fi`) applies to all events including PermissionRequest. Codex poller-driven runs (which set `KM_SLACK_THREAD_TS`) will silence the hook's Slack post; operator-driven `km agent run --codex` (no `KM_SLACK_THREAD_TS`) still posts as usual.

## Self-Check: PASSED

- [x] `pkg/compiler/userdata.go` modified — PermissionRequest in heredoc confirmed
- [x] `pkg/compiler/notify_hook_test.go` created — 4 real tests confirmed
- [x] Commit `83af685` — Task 1 stubs
- [x] Commit `d09d1c8` — Task 2 implementation
- [x] `go test ./pkg/compiler/... -run 'TestNotifyHook' -count=1` — all PASS
- [x] `make build` — km v0.3.703 built clean
