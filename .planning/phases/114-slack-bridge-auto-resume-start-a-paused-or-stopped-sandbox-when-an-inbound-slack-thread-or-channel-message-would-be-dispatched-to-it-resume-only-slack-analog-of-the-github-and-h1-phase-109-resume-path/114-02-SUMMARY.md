---
phase: 114-slack-bridge-auto-resume
plan: "02"
subsystem: pkg/slack/bridge
tags: [ec2-resume, slack-bridge, phase-114, events-handler, synchronous, tdd]
dependency_graph:
  requires:
    - Plan 01: SandboxResumer, SandboxStatusWriter, ErrNoResumableInstance, EC2Resumer, DynamoSandboxStatusWriter (pkg/slack/bridge)
  provides:
    - EventsHandler.Resumer/StatusWriter/OrphanHinter fields (Phase 114 handler wiring points)
    - Synchronous step-9 resume-or-hint branch in EventsHandler.Handle
    - Six resume-branch unit tests locking all behavioral invariants
  affects:
    - pkg/slack/bridge/events_handler.go (step-9 branch replaced; three fields added)
    - pkg/slack/bridge/events_handler_resume_test.go (new file, 449 lines)
tech_stack:
  added: []
  patterns:
    - Synchronous call pattern (no goroutine) mirroring step-10 reactor — Phase 75.2 Lambda-freeze lesson
    - errors.Is(err, ErrNoResumableInstance) for orphan vs transient discrimination
    - Optimistic SetStatusRunning on transient error (same fail-soft contract as GitHub Phase-109)
    - nil-field invariant: nil Resumer => byte-identical to pre-Phase-114 (pause-hint only)
key_files:
  modified:
    - pkg/slack/bridge/events_handler.go
  created:
    - pkg/slack/bridge/events_handler_resume_test.go
decisions:
  - "Step-9 runs SYNCHRONOUSLY (not in a goroutine) — Phase 75.2 Lambda-freeze lesson: goroutines mid-StartInstances have context elapse during Lambda freeze; 3s ack window protected by event_id dedup at step 6"
  - "nil Resumer path is byte-identical in behavior to pre-Phase-114 (only PauseHinter posted), now also synchronous — consistent with step-10 reactor precedent"
  - "ErrNoResumableInstance → OrphanHinter (degraded, no SetStatusRunning, no cold-create); transient error → optimistic SetStatusRunning + PauseHinter"
  - "Message already enqueued at step 8 in ALL branches — resume failure NEVER strands the prompt"
  - "OrphanHinter is a distinct PauseHintPoster instance from PauseHinter — wired separately so the two hint texts can differ without a new interface"
  - "resumeCtx uses 15s sub-timeout from the request ctx (not context.Background()) to stay within the 60s Lambda budget"
metrics:
  duration: "~10min"
  completed_date: "2026-06-15"
  tasks_completed: 2
  files_modified: 1
  files_created: 1
---

# Phase 114 Plan 02: EventsHandler Resume-or-Hint Branch Summary

**One-liner:** Replaced the goroutine-based pause-hint step-9 with a synchronous resume-or-hint branch in EventsHandler.Handle, adding Resumer/StatusWriter/OrphanHinter fields and six unit tests locking all behavioral paths including the Phase-75.2 synchronous-not-goroutine invariant.

## What Was Built

### Task 1: Resumer/StatusWriter/OrphanHinter fields + synchronous step-9 (7c671dc1)

**`pkg/slack/bridge/events_handler.go`** — three new optional fields added to `EventsHandler`:

```go
Resumer      SandboxResumer    // Phase 114 auto-resume; nil => byte-identical to pre-114
StatusWriter  SandboxStatusWriter // flip km-sandboxes status after resume; nil => no flip
OrphanHinter  PauseHintPoster  // degraded hint on ErrNoResumableInstance path
```

The existing goroutine-based step-9 block was replaced with a synchronous resume-or-hint branch:

```
if info.Paused {
    resumeCtx (15s sub-context)
    if h.Resumer != nil {
        err := h.Resumer.StartSandbox(resumeCtx, sid)
        if errors.Is(err, ErrNoResumableInstance) {
            → OrphanHinter (degraded, no SetStatusRunning)
        } else {
            → SetStatusRunning (optimistic, fail-soft) + PauseHinter (waking-up)
        }
    } else if h.PauseHinter != nil {
        → PauseHinter only (nil-Resumer back-compat path)
    }
    cancel()
}
```

The Lambda-freeze rationale comment mirrors the existing step-10 reactor comment verbatim (Phase 75.2 lesson).

### Task 2: Resume-branch unit tests (02cf54a1)

**`pkg/slack/bridge/events_handler_resume_test.go`** — 449 lines, package `bridge`, six scenarios:

| Test | Scenario | Key assertions |
|------|----------|---------------|
| `TestEventsHandler_PausedSandbox_Resumes` | Happy path | StartSandbox + SetStatusRunning + PauseHinter all called; OrphanHinter NOT called; SQS enqueued; 200 |
| `TestEventsHandler_PausedSandbox_OrphanDegrades` | ErrNoResumableInstance | OrphanHinter called; SetStatusRunning NOT called; PauseHinter NOT called; SQS enqueued; 200 |
| `TestEventsHandler_PausedSandbox_TransientError` | Plain error (throttled) | SetStatusRunning called optimistically; PauseHinter called; OrphanHinter NOT called; SQS enqueued; no panic; 200 |
| `TestEventsHandler_RunningSandbox_NoResume` | info.Paused==false | StartSandbox NOT called; SetStatusRunning NOT called; SQS enqueued |
| `TestEventsHandler_NilResumer_PauseHintOnly` | Resumer==nil | PauseHinter called (byte-identical pre-114); OrphanHinter NOT called; SQS enqueued; 200 |
| `TestEventsHandler_PausedSandbox_ResumeIsSynchronous` | Synchronous guard | StartSandbox + SetStatusRunning call-counts asserted with NO sleep immediately after Handle returns |

New mocks: `mockSandboxResumer` (records StartSandbox calls), `mockSandboxStatusWriter` (records SetStatusRunning calls). Both have thread-safe `snapshot()` helpers.

## Deviations from Plan

None — plan executed exactly as written. The `buildPausedEventRequest` helper stub in an intermediate draft was removed in favor of directly building signed event bodies inline (simpler, matches the existing test pattern in events_handler_test.go).

## Verification

```
go build ./pkg/slack/bridge/...  → ok (no output)
go vet ./pkg/slack/bridge/...    → ok (no output)
go test ./pkg/slack/bridge/ -run 'Resume|Orphan|NilResumer|RunningSandbox_NoResume|TransientError|Synchronous' -count=1 -timeout 300s → ok
go test ./pkg/slack/bridge/... -count=1 -timeout 300s → ok  5.372s (all tests, incl. pre-existing paused-hint tests)
```

The pre-existing paused-hint tests (`TestEventsHandler_PausedSandbox_FirstMessage`, `TestEventsHandler_PausedSandbox_WithinCooldown`) use a goroutine-polling pattern (500ms deadline loop). They continue to pass because the synchronous step-9 posts the hint before Handle returns — the poll completes immediately on the first check.

## Self-Check: PASSED

- FOUND: pkg/slack/bridge/events_handler.go (Resumer/StatusWriter/OrphanHinter fields + step-9 branch)
- FOUND: pkg/slack/bridge/events_handler_resume_test.go (449 lines, 6 tests)
- FOUND commit: 7c671dc1 (Task 1 — handler fields + synchronous step-9)
- FOUND commit: 02cf54a1 (Task 2 — resume-branch unit tests)
