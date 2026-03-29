---
phase: 29-configurable-sandbox-id-prefix
plan: "02"
subsystem: cli
tags: [sandbox-id, prefix, regex, email-handler, profile]

requires:
  - phase: 29-configurable-sandbox-id-prefix
    plan: "01"
    provides: GenerateSandboxID and IsValidSandboxID in pkg/compiler/sandbox_id.go

provides:
  - Generalized sandbox ID validation pattern in destroy.go accepting any {prefix}-{8hex} form
  - Generalized sandbox ID detection in sandbox_ref.go via sandboxIDLike regex
  - Email handler extractSandboxID returns full {prefix}-{8hex} ID (no sb- repair)
  - claude-dev.yaml built-in profile with metadata.prefix: claude

affects:
  - km destroy command (accepts claude-* and other custom prefix IDs)
  - sandbox_ref.go ResolveSandboxID (recognizes any valid prefix ID as ID, not numeric ref)
  - email-create-handler handleStatus (uses full sandbox ID as provided in subject)

tech-stack:
  added: []
  patterns:
    - "Generalized sandbox ID pattern: ^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$ used consistently across destroy.go and sandbox_ref.go"
    - "Email handler pattern captures full {prefix}-{8hex} group 1 — no post-extraction repair"

key-files:
  created:
    - internal/app/cmd/sandbox_ref_test.go
  modified:
    - internal/app/cmd/destroy.go
    - internal/app/cmd/destroy_test.go
    - internal/app/cmd/sandbox_ref.go
    - cmd/email-create-handler/main.go
    - cmd/email-create-handler/main_test.go
    - profiles/claude-dev.yaml

key-decisions:
  - "Use inline regex in destroy.go and sandbox_ref.go rather than importing compiler.IsValidSandboxID — avoids import cycle and keeps cmd package self-contained"
  - "Email handler pattern uses (?i) flag so mixed-case input (SB-AbCd1234) is tolerated at match time, but returns original casing from group 1"
  - "Remove sb- prefix repair block entirely rather than conditionalizing — repair was wrong for custom prefixes and masked misconfigurations"

patterns-established:
  - "Sandbox ID regex: ^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$ — used in destroy.go (sandboxIDPattern) and sandbox_ref.go (sandboxIDLike)"
  - "Email handler regex: (?i)\\b([a-z][a-z0-9]{0,11}-[0-9a-f]{8})\\b — captures full prefix-hex ID in group 1"

requirements-completed: [PREFIX-03, PREFIX-04, PREFIX-05]

duration: 6min
completed: 2026-03-29
---

# Phase 29 Plan 02: Generalize Sandbox ID Validation Summary

**Generalized sandbox ID regex ^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$ in destroy.go and sandbox_ref.go; email handler captures full {prefix}-{8hex} ID and sb- repair block removed; claude-dev.yaml gains metadata.prefix: claude**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-03-29T02:50:42Z
- **Completed:** 2026-03-29T02:53:07Z
- **Tasks:** 2 (both TDD)
- **Files modified:** 6

## Accomplishments
- Both destroy.go and sandbox_ref.go now accept `claude-abc12345`, `build-abc12345`, `a-abc12345` in addition to legacy `sb-` IDs
- ResolveSandboxID treats any `{prefix}-{8hex}` form as an ID, falling through to numeric lookup only for non-matching strings
- Email handler extractSandboxID returns the full compound ID (prefix + hex) so downstream status lookup uses the correct ID without sb- corruption
- claude-dev.yaml built-in profile declares `prefix: claude` so `km create profiles/claude-dev.yaml` will generate `claude-*` IDs

## Task Commits

Each task was committed atomically:

1. **Task 1: Generalize destroy.go and sandbox_ref.go validation patterns** - `7c6c863` (feat)
2. **Fix: Remove spurious s3 import added by linter** - `15b2054` (fix)
3. **Task 2: Fix email handler extraction and update built-in profiles** - `b0fdc79` (feat)

## Files Created/Modified
- `internal/app/cmd/destroy.go` - Updated sandboxIDPattern and error message to use generalized regex
- `internal/app/cmd/destroy_test.go` - Added TestSandboxIDPattern and TestDestroyCmd_GeneralizedPatternAcceptsCustomPrefix; updated invalid IDs list
- `internal/app/cmd/sandbox_ref.go` - Replaced strings.HasPrefix check with sandboxIDLike regex; removed strings import
- `internal/app/cmd/sandbox_ref_test.go` - New file; TestResolveSandboxID_CustomPrefix with claude-, build-, a- and sb- cases
- `cmd/email-create-handler/main.go` - Updated sandboxIDPattern to capture full prefix-hex; removed sb- prefix repair block; updated help text
- `cmd/email-create-handler/main_test.go` - Updated TestExtractSandboxID to assert full ID returned; added TestExtractSandboxID_NoPrefixRepair
- `profiles/claude-dev.yaml` - Added `prefix: claude` to metadata section

## Decisions Made
- Kept inline regex in cmd package rather than using `compiler.IsValidSandboxID()` to avoid import coupling between cmd and compiler packages at validation time.
- Email handler captures full ID in regex group 1 (not just hex portion) — simpler and more correct than two-step extract+repair.

## Deviations from Plan

**1. [Rule 3 - Blocking] Removed spurious s3 import added by background linter/tool in sandbox_ref.go**
- **Found during:** Task 1 post-commit
- **Issue:** A background process (concurrent plan execution) injected an `s3` import into sandbox_ref.go that was not yet needed (alias resolution feature in progress)
- **Fix:** Removed the unused import; the file later received the alias resolution block which correctly uses it
- **Files modified:** internal/app/cmd/sandbox_ref.go
- **Verification:** `go build ./internal/app/cmd/...` passes
- **Committed in:** 15b2054

---

**Total deviations:** 1 auto-fixed (1 blocking — spurious import)
**Impact on plan:** Minimal. Required one additional cleanup commit. No scope change.

## Issues Encountered
- Pre-existing test failures in `TestBudgetAdd_*` (budget_test.go): test data uses invalid IDs like `sb-001` (only 3 hex chars, not 8). These failures existed BEFORE this plan's changes (verified via git stash). Deferred to `deferred-items.md`.
- Concurrent plan execution (Plan 01 alias feature) was updating sandbox_ref.go during execution, causing import drift. Handled via Rule 3 auto-fix.

## Next Phase Readiness
- Phase 29 complete: configurable sandbox ID prefix feature fully implemented end-to-end
- All three consumer components (destroy, sandbox_ref, email handler) accept any valid {prefix}-{8hex} ID
- Built-in profiles can now declare a prefix for branded sandbox IDs

---
*Phase: 29-configurable-sandbox-id-prefix*
*Completed: 2026-03-29*
