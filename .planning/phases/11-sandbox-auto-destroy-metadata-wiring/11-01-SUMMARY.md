---
phase: 11-sandbox-auto-destroy-metadata-wiring
plan: "01"
subsystem: cmd
tags: [metadata, state-bucket, config, tdd]
dependency_graph:
  requires: []
  provides: [cfg.StateBucket wired into list, status, shell, budget commands]
  affects: [internal/app/cmd/list.go, internal/app/cmd/status.go, internal/app/cmd/shell.go, internal/app/cmd/budget.go]
tech_stack:
  added: []
  patterns: [DI guard pattern — empty-bucket check before real client construction]
key_files:
  modified:
    - internal/app/cmd/list.go
    - internal/app/cmd/status.go
    - internal/app/cmd/shell.go
    - internal/app/cmd/budget.go
    - internal/app/cmd/list_test.go
    - internal/app/cmd/status_test.go
decisions:
  - "Deleted defaultStateBucket constant ('tf-km-state'); cfg.StateBucket is the sole source of truth for the state bucket in all command paths"
  - "Empty-bucket guard placed before AWS config load — fast, cheap check at the top of the nil-client branch; returns actionable error pointing to KM_STATE_BUCKET env var and km-config.yaml"
  - "Rule 3 auto-fix: budget.go and shell.go also referenced defaultStateBucket — fixed in same commit to keep the package buildable"
metrics:
  duration: 185s
  completed_date: "2026-03-22"
  tasks_completed: 1
  files_changed: 6
---

# Phase 11 Plan 01: Thread cfg.StateBucket into CLI Commands Summary

Config-driven S3 bucket for km list, status, shell, and budget commands — hardcoded 'tf-km-state' constant deleted; empty-bucket produces actionable operator error.

## Objective

Fix km list and km status (and, as auto-discovered, km shell and km budget add) to read sandbox metadata from `cfg.StateBucket` (loaded from `KM_STATE_BUCKET` env var or `km-config.yaml`) instead of the hardcoded `"tf-km-state"` constant.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| RED | Add failing tests for empty StateBucket and config threading | e8a63e8 | list_test.go, status_test.go |
| GREEN | Thread cfg.StateBucket through all affected commands | 6fdf7d1 | list.go, status.go, shell.go, budget.go |

## Decisions Made

- Deleted `defaultStateBucket` constant from `list.go` entirely — no replacement constant; `cfg.StateBucket` is the sole source.
- Empty-bucket check runs before AWS credential loading, so operators get an immediate and clear error without SDK overhead.
- Error message references both `KM_STATE_BUCKET` and `km-config.yaml` — operators know both configuration paths.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] budget.go and shell.go also referenced defaultStateBucket**

- **Found during:** GREEN phase build — `go test` failed with `undefined: defaultStateBucket` in two additional files
- **Issue:** `budget.go:114` and `shell.go:59` both used the now-deleted constant
- **Fix:** Applied the same `cfg.StateBucket` empty-check + usage pattern to both files
- **Files modified:** `internal/app/cmd/budget.go`, `internal/app/cmd/shell.go`
- **Commit:** 6fdf7d1

## Verification

- `go test ./internal/app/cmd/... -v -count=1` — PASS (all existing + 4 new tests)
- `go vet ./internal/app/cmd/...` — PASS
- `grep -r "defaultStateBucket" internal/app/cmd/` — no results (constant fully removed)
- `grep "cfg.StateBucket" internal/app/cmd/list.go internal/app/cmd/status.go` — 2 usage sites per file confirmed

## Self-Check: PASSED

- list.go: FOUND
- status.go: FOUND
- shell.go: FOUND
- budget.go: FOUND
- Commit e8a63e8 (RED): FOUND
- Commit 6fdf7d1 (GREEN): FOUND
