---
phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests
plan: 03
subsystem: test-hygiene
tags: [test, uninit, module-order, regional-modules]
dependency_graph:
  requires: []
  provides: [uninit-tests-22-module-aligned]
  affects: [internal/app/cmd/uninit_test.go]
tech_stack:
  added: []
  patterns: [table-driven-count-assertion, reverse-order-slice-verification]
key_files:
  created: []
  modified:
    - internal/app/cmd/uninit_test.go
decisions:
  - "wantOrder slice is the exact reverse of regionalModules() — verified against init.go source"
  - "Two count literals (wantCalls, != N) annotated with regionalModules()==22 comment to ease future additions"
metrics:
  duration: 51s
  completed: 2026-06-12
  tasks_completed: 1
  files_changed: 1
---

# Phase 107 Plan 03: Reconcile Uninit Tests to 22-Module Inventory Summary

Reconciled 3 stale uninit tests to the current 22-entry `regionalModules()` inventory. The wantOrder slice (19 entries) and two hardcoded count literals were left behind when Phases 98/100/103/104 added `dynamodb-slack-channels`, `dynamodb-github-threads`, `dynamodb-h1-threads`, and `lambda-h1-bridge`. Pure test hygiene; no production code touched.

## What Was Done

**Task 1: Update wantOrder to 22-entry reverse + bump two count consts to 22**

Cross-checked `regionalModules()` in `internal/app/cmd/init.go` (22 entries, apply order confirmed). Made three targeted edits to `internal/app/cmd/uninit_test.go`:

1. `TestUninitDestroyOrder.wantOrder` — replaced 19-entry slice with the exact 22-entry reverse-of-apply order:
   - Added `lambda-h1-bridge` (position 2, right after `ses`)
   - Added `dynamodb-h1-threads` (position 3)
   - Reordered bridges: `lambda-github-bridge` (4) precedes `lambda-slack-bridge` (5) — matches apply order 19→18 reversed
   - Added `dynamodb-slack-channels` (position 9, between `dynamodb-slack-stream-messages` and `dynamodb-slack-threads`)
   - Updated comment to "Reverse of the 22-module regionalModules() apply order (init.go)"
   - Added per-entry phase annotations for the non-obvious entries

2. `TestUninitContinuesPastModuleErrors.wantCalls` — `const wantCalls = 19` → `22`

3. `TestUninitDetectsBackendDrift` — `!= 19` → `!= 22`; inline error format string updated to use `%d, 22` instead of hardcoded `"19"`

All three sites annotated with `regionalModules()==22` comment so the next module addition is easy to find.

## Verification

```
go test ./internal/app/cmd/ -run 'TestUninit' -count=1 -timeout 600s; echo "EXIT=$?"
ok      github.com/whereiskurt/klanker-maker/internal/app/cmd   0.749s
EXIT=0
```

`git diff --stat` shows only `internal/app/cmd/uninit_test.go` changed.

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| 1 | eb8118c5 | test(107-03): reconcile uninit tests — wantOrder 19→22 + bump two count consts |

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- [x] `internal/app/cmd/uninit_test.go` modified (not created — correct)
- [x] Commit eb8118c5 exists: `git log --oneline | grep eb8118c5` → confirmed
- [x] No production `.go` file touched
- [x] `TestUninitDestroyOrder`, `TestUninitContinuesPastModuleErrors`, `TestUninitDetectsBackendDrift` all PASS
- [x] Full `TestUninit*` suite: `ok EXIT=0`
