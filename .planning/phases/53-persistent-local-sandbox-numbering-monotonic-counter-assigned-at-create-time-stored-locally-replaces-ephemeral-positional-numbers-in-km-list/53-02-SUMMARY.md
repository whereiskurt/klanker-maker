---
phase: 53-persistent-local-sandbox-numbering
plan: "02"
subsystem: cli
tags: [localnumber, sandbox, create, destroy, list, sandbox_ref, persistent-numbering]

requires:
  - phase: 53-01
    provides: pkg/localnumber package with Assign/Remove/Resolve/Reconcile API

provides:
  - Persistent local number assignment in km create (EC2 and docker paths)
  - Persistent number display in km list with reconcile-on-list
  - Local number-based resolution in km destroy/shell/agent (numeric refs)
  - Local number cleanup on km destroy (both EC2 and Docker paths)

affects:
  - any phase touching create.go, list.go, sandbox_ref.go, destroy.go

tech-stack:
  added: []
  patterns:
    - "localnumber.Load/Assign/Save pattern: non-fatal warn-on-error wrapping around all local state ops"
    - "printSandboxTable accepts numbers map with nil-safe positional fallback"
    - "Reconcile-on-list: km list prunes stale and assigns new before display"

key-files:
  created: []
  modified:
    - internal/app/cmd/create.go
    - internal/app/cmd/destroy.go
    - internal/app/cmd/list.go
    - internal/app/cmd/sandbox_ref.go

key-decisions:
  - "Assign local number once at sandboxID generation (line ~233) covers both EC2 and docker paths via single assignment point"
  - "Pass assignedNum as extra int parameter to runCreateDocker rather than re-resolving inside it"
  - "listSandboxes retained in sandbox_ref.go — still used by status.go"
  - "printSandboxTable signature updated to accept numbers map[string]int with nil-safe positional fallback for test compat"
  - "Remote create path (runCreateRemote) intentionally excluded — remote sandboxes assigned on next km list via Reconcile"

patterns-established:
  - "Non-fatal localnumber pattern: all Load/Save ops wrapped in if err == nil or warn-on-error, never block create/destroy"
  - "Reconcile before display: km list always prunes stale + assigns numbers to new sandboxes before rendering table"

requirements-completed:
  - LOCAL-07
  - LOCAL-08

duration: 9min
completed: "2026-04-13"
---

# Phase 53 Plan 02: Persistent Local Sandbox Numbering CLI Wiring Summary

**Wired `pkg/localnumber` into all four CLI touchpoints so sandbox numbers survive create/destroy cycles and are consistent across km create, km list, km destroy, and numeric refs**

## Performance

- **Duration:** 9 min
- **Started:** 2026-04-13T21:37:27Z
- **Completed:** 2026-04-13T21:46:00Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments

- `km create` assigns persistent local number after sandboxID generation and includes `#N` in both EC2 and docker success messages
- `km list` reconciles local numbers with live DynamoDB state before display; `printSandboxTable` shows persistent numbers with positional fallback
- Numeric refs like `km destroy 1` resolve from local file (no AWS call needed) via `localnumber.Resolve`
- `km destroy` removes local number entry after DynamoDB delete on both EC2 and Docker destroy paths

## Task Commits

1. **Task 1: Wire create.go and destroy.go** - `8dfd48c` (feat)
2. **Task 2: Wire list.go and sandbox_ref.go** - `f25b28f` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified

- `internal/app/cmd/create.go` - Added localnumber import, Assign call after sandboxID generation, #N in success messages, assignedNum param threaded to runCreateDocker
- `internal/app/cmd/destroy.go` - Added localnumber import, Remove + Save calls in both EC2 and Docker destroy paths
- `internal/app/cmd/list.go` - Added localnumber import, Reconcile block before display, updated printSandboxTable signature with numbers map
- `internal/app/cmd/sandbox_ref.go` - Added localnumber import, replaced positional list lookup with localnumber.Resolve

## Decisions Made

- Assign local number once at sandboxID generation (covers both local EC2 and docker paths via single assignment point) — cleaner than assigning inside each substrate path
- Pass `assignedNum` as extra `int` parameter to `runCreateDocker` rather than re-resolving inside it
- Remote create path (`runCreateRemote`) intentionally excluded — remote sandboxes get assigned on next `km list` via Reconcile
- `listSandboxes` retained in sandbox_ref.go — still called by status.go
- `printSandboxTable` accepts `map[string]int` with nil-safe positional fallback to maintain test compatibility

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

Two pre-existing test failures confirmed out-of-scope (both fail on `main` before this plan's changes):
- `TestCreateDockerWritesComposeFile` — missing `PLACEHOLDER_OPERATOR_KEY` pattern in create.go
- `TestListCmd_EmptyStateBucketError` — networking issue in test setup

Both were failing before this plan and are unrelated to localnumber wiring.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- All four CLI touchpoints wired to `pkg/localnumber`
- `make build` succeeds; all previously-passing tests still pass
- Persistent sandbox numbering fully functional end-to-end

---
*Phase: 53-persistent-local-sandbox-numbering*
*Completed: 2026-04-13*
