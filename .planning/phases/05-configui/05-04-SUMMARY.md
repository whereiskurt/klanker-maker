---
phase: 05-configui
plan: "04"
subsystem: ui
tags: [go, htmx, cobra, eventbridge, ssm, template-cloning]

requires:
  - phase: 05-configui-03
    provides: secrets management handlers and SSM integration wired into configui
  - phase: 05-configui-02
    provides: profile editor with Monaco and profile list handlers

provides:
  - Destroy handler (POST /api/sandboxes/{id}/destroy) with HTMX HX-Trigger response
  - Extend TTL handler (PUT /api/sandboxes/{id}/ttl) via EventBridge Scheduler
  - Quick-create handler (POST /api/sandboxes/create) with profilesDir validation
  - Narrow interfaces: Destroyer, TTLExtender, SandboxCreator for testability
  - Per-page template cloning to prevent content block collision across dashboard/editor/secrets
  - Graceful dashboard error handling (warning banner instead of 500 on AWS failure)

affects: [06-e2e-hardening, any phase extending configui]

tech-stack:
  added: []
  patterns:
    - "Narrow interface per action (Destroyer/TTLExtender/SandboxCreator) — production impls shell to km CLI or call pkg/aws directly; tests inject mocks"
    - "ErrDestroyNotFound sentinel for typed 404 handling"
    - "Per-page template clone: clone base template, then parse page template into clone — prevents {{define content}} collision in shared parse set"
    - "BasePage.ActivePage field drives nav highlighting without per-handler logic"

key-files:
  created:
    - cmd/configui/handlers_actions_test.go
  modified:
    - cmd/configui/handlers.go
    - cmd/configui/main.go
    - cmd/configui/handlers_editor.go
    - cmd/configui/handlers_editor_test.go
    - cmd/configui/handlers_secrets.go
    - cmd/configui/handlers_secrets_test.go
    - cmd/configui/static/style.css
    - cmd/configui/templates/dashboard.html

key-decisions:
  - "kmDestroyerImpl/kmCreatorImpl shell out to km CLI subprocess — avoids importing internal packages with Cobra deps; matches Phase 02 destroy-path pattern"
  - "schedulerTTLExtender calls DeleteTTLSchedule then CreateTTLSchedule directly via pkg/aws — same EventBridge pattern as Phase 03-04"
  - "Template cloning: clone base template per page to avoid content block redeclaration — Go html/template parse set treats all named blocks as global"
  - "Dashboard graceful degradation: ListSandboxes failure renders warning banner, not HTTP 500 — operators can still navigate the UI when AWS is unreachable"

patterns-established:
  - "Pattern: per-page template clone — copy base, parse page into copy; prevents block collision across independent pages"
  - "Pattern: HTMX HX-Trigger response header on mutation endpoints — table row auto-refresh without full page reload"

requirements-completed: [CFUI-01, CFUI-02, CFUI-03, CFUI-04]

duration: ~60min (including visual verification)
completed: "2026-03-22"
---

# Phase 5 Plan 04: Dashboard Actions and Visual Verification Summary

**Destroy, extend-TTL, and quick-create action handlers wired with narrow interfaces; all four CFUI requirements verified in-browser after template cloning fix resolved content block collision**

## Performance

- **Duration:** ~60 min (including visual verification checkpoint)
- **Started:** 2026-03-22T17:51:00Z
- **Completed:** 2026-03-22T18:51:00Z
- **Tasks:** 2 (Task 1 auto, Task 2 human-verify)
- **Files modified:** 9

## Accomplishments

- Implemented destroy, extend-TTL, and quick-create handlers with narrow injectable interfaces — 6 unit tests all pass
- Production implementations shell out to km CLI or call pkg/aws EventBridge directly, matching Phase 03-04 pattern
- Post-checkpoint fix resolved Go html/template content block collision using per-page template cloning; dashboard now shows warning banner instead of HTTP 500 on AWS failure
- All four CFUI requirements (CFUI-01 through CFUI-04) confirmed working in a real browser — dashboard, editor, secrets pages all render correctly

## Task Commits

1. **Task 1 RED: failing action handler tests** - `2fe3b39` (test)
2. **Task 1 GREEN: action handler implementation + routes** - `ea14f87` (feat)
3. **Task 2 post-checkpoint fix: template cloning + graceful errors** - `cf606e5` (fix)

## Files Created/Modified

- `cmd/configui/handlers_actions_test.go` - Unit tests for destroy, extend TTL, quick-create (199 lines, 6 test cases)
- `cmd/configui/handlers.go` - Destroyer/TTLExtender/SandboxCreator interfaces; handleDestroy/handleExtendTTL/handleQuickCreate; production impls; template clone helper
- `cmd/configui/main.go` - New action routes wired; destroy stub replaced; scheduler client injected; ActivePage population
- `cmd/configui/handlers_editor.go` - Updated to use template clone helper
- `cmd/configui/handlers_editor_test.go` - Updated for ActivePage field
- `cmd/configui/handlers_secrets.go` - Updated to use template clone helper
- `cmd/configui/handlers_secrets_test.go` - Updated for ActivePage field
- `cmd/configui/static/style.css` - alert-warning CSS class
- `cmd/configui/templates/dashboard.html` - Warning banner partial added

## Decisions Made

- **km CLI subprocess for destroy/create:** Production Destroyer and SandboxCreator implementations shell out to `km destroy {id}` and `km create profiles/{profile}`. This avoids importing internal packages with Cobra initialization side-effects, consistent with Phase 02 destroy-path precedent.
- **Direct pkg/aws for TTL extension:** schedulerTTLExtender calls DeleteTTLSchedule then CreateTTLSchedule directly — same EventBridge pattern established in Phase 03-04. No subprocess needed since pkg/aws has no Cobra deps.
- **Template cloning:** Go's html/template parse set treats all `{{define "content"}}` blocks as global — parsing dashboard.html and editor.html into the same set causes the second to overwrite the first. Fix: clone the base template per request, then parse the page-specific template into the clone.
- **Graceful dashboard errors:** ListSandboxes AWS failure renders a warning banner with the error message; the rest of the UI (nav, empty table) remains functional. Operators can still navigate to profiles and secrets even when AWS credentials are unavailable.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Template content block collision between dashboard and editor pages**
- **Found during:** Task 2 (visual verification — editor page rendered dashboard content)
- **Issue:** Go html/template parses all `{{define "content"}}` blocks into a shared namespace; loading dashboard.html + editor.html into one parse set caused the editor to overwrite the dashboard's content block
- **Fix:** Added `cloneBaseTemplate()` helper that clones the base template per page render, then parses only the current page's template into the clone
- **Files modified:** cmd/configui/handlers.go, cmd/configui/main.go, cmd/configui/handlers_editor.go, cmd/configui/handlers_secrets.go
- **Verification:** All three pages render correct content; all tests pass
- **Committed in:** cf606e5

**2. [Rule 1 - Bug] Dashboard returned HTTP 500 on AWS credential errors**
- **Found during:** Task 2 (visual verification — dashboard shows 500 before AWS credentials configured)
- **Issue:** handleDashboard propagated ListSandboxes errors to HTTP 500; operators couldn't reach editor or secrets without AWS access
- **Fix:** Catch ListSandboxes error, populate BasePage with warning message, render template with empty sandbox list
- **Files modified:** cmd/configui/handlers.go, cmd/configui/templates/dashboard.html, cmd/configui/static/style.css
- **Verification:** Dashboard loads with warning banner when AWS unreachable; editor and secrets pages still accessible
- **Committed in:** cf606e5

---

**Total deviations:** 2 auto-fixed (both Rule 1 - Bug)
**Impact on plan:** Both fixes discovered during the human-verify checkpoint and required to satisfy CFUI requirements. Template cloning is a Go html/template constraint; graceful errors are required for usable UX. No scope creep.

## Issues Encountered

- Go's html/template global define namespace was not anticipated in the plan. The fix required a structural pattern change (per-page clone instead of shared parse set) that touched all three page handlers. The pattern is now established and documented for any future page additions.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- ConfigUI (Phase 5) is complete — all four CFUI requirements fulfilled across plans 01-04
- Feature-complete ConfigUI ready for real AWS end-to-end testing in Phase 6
- Template clone pattern documented for any future page additions to configui

---
*Phase: 05-configui*
*Completed: 2026-03-22*
