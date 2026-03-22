---
phase: 05-configui
plan: 01
subsystem: ui
tags: [go, html/template, htmx, cloudwatchlogs, s3, resourcegroupstaggingapi, zerolog]

requires:
  - phase: 04-lifecycle-hardening-artifacts-email
    provides: SandboxRecord, ListAllSandboxesByS3, FindSandboxByID, ErrSandboxNotFound, CWLogsAPI established patterns
  - phase: 03-sidecar-enforcement-lifecycle-management
    provides: CloudWatch audit log group /km/sandboxes

provides:
  - cmd/configui binary serving live sandbox dashboard at :8080
  - Handler struct with SandboxLister/SandboxFinder/CWLogsFilterAPI narrow interfaces
  - Dashboard with 10-second HTMX polling, sandbox detail view with resource ARNs
  - Profile YAML rendering in detail view via profilesDir file read
  - Collapsible audit log section lazy-loaded via hx-trigger=revealed
  - SchemaJSON() accessor in pkg/profile for schema endpoint
  - Operational dark-theme CSS with status/substrate badges, PII blur, toast area

affects:
  - 05-02: editor plan — builds on Handler struct, adds profile editor routes
  - 05-03: secrets plan — uses PII blur CSS classes already defined

tech-stack:
  added:
    - html/template (stdlib) with go:embed for server-side HTML rendering
    - HTMX 2.x via jsDelivr CDN for dynamic partial updates and polling
  patterns:
    - Narrow interface adapters: SandboxLister/SandboxFinder/CWLogsFilterAPI wrap AWS SDK clients
    - buildTestTemplates() for handler unit testing without filesystem or AWS
    - Handler struct DI: all dependencies injected via struct fields, no global state
    - template.FuncMap registered before ParseFS for custom helpers (truncateID)

key-files:
  created:
    - cmd/configui/main.go
    - cmd/configui/handlers.go
    - cmd/configui/handlers_test.go
    - cmd/configui/templates/base.html
    - cmd/configui/templates/dashboard.html
    - cmd/configui/templates/partials/sandbox_row.html
    - cmd/configui/templates/partials/sandbox_detail.html
    - cmd/configui/static/style.css
    - pkg/profile/schema_export.go
  modified: []

key-decisions:
  - "Handler struct + narrow interfaces: SandboxLister/SandboxFinder/CWLogsFilterAPI — follows project pattern from Phase 03-05 SandboxLister/SandboxFetcher"
  - "package main for all cmd/configui files — Go disallows two packages per directory; consistent with Phase 03-02 cmd/ pattern"
  - "buildTestTemplates() in handlers.go with inline template strings — test isolation without filesystem dependency; truncateID registered as no-op"
  - "handleSandboxLogs returns graceful placeholder (not error) when cwClient is nil or call fails — logs are informational, not critical path"
  - "sandbox_rows template in partials/sandbox_row.html — HTMX polling returns tbody innerHTML partial, not full page"

patterns-established:
  - "ConfigUI handler testing: newTestHandler(lister, finder, cwClient, profilesDir) with mock implementations + inline templates"
  - "HTMX partial vs full-page: isHTMXRequest(r) checks HX-Request header; dashboard returns sandbox_rows partial or full dashboard.html"

requirements-completed:
  - CFUI-02
  - CFUI-03

duration: 7min
completed: 2026-03-22
---

# Phase 5 Plan 01: ConfigUI Server Scaffold Summary

**Go HTTP server at cmd/configui/ with HTMX-polled sandbox dashboard, detail view showing resource ARNs and profile YAML, collapsible CloudWatch audit log section, and dark-theme CSS**

## Performance

- **Duration:** 7 min
- **Started:** 2026-03-22T17:34:18Z
- **Completed:** 2026-03-22T17:41:18Z
- **Tasks:** 2
- **Files modified:** 9

## Accomplishments

- Handler struct with testable narrow interfaces (SandboxLister, SandboxFinder, CWLogsFilterAPI) and 9 passing unit tests
- HTMX dashboard with `hx-trigger="every 10s"` polling, sandbox row click loading detail partial into `#sandbox-detail`
- Sandbox detail view renders: resource ARNs from AWS tag discovery, profile YAML from profilesDir in `<pre><code>`, collapsible audit log section lazy-loaded via `hx-trigger="revealed"`
- Operational dark-theme CSS (~260 lines): status badges (running/stopped/unknown), substrate badges (ec2/ecs), PII blur classes, toast area, responsive table

## Task Commits

1. **TDD RED: Failing tests** - `1a113c6` (test)
2. **Task 1: Server scaffold — Handler, handlers, main.go, SchemaJSON()** - `c6cdfc4` (feat)
3. **Task 2: HTML templates, CSS, HTMX polling** - `de7730a` (feat)

## Files Created/Modified

- `cmd/configui/main.go` - HTTP server entrypoint: go:embed, mux routing, AWS client wiring, templateFuncs
- `cmd/configui/handlers.go` - Handler struct, all HTTP handlers, narrow interfaces, buildTestTemplates()
- `cmd/configui/handlers_test.go` - 9 unit tests covering all handler behaviors with mock implementations
- `cmd/configui/templates/base.html` - Nav (Dashboard/Profiles/Secrets), HTMX CDN, block content, footer
- `cmd/configui/templates/dashboard.html` - Sandbox table with HTMX polling, detail panel placeholder
- `cmd/configui/templates/partials/sandbox_row.html` - sandbox_rows partial + sandbox_row template
- `cmd/configui/templates/partials/sandbox_detail.html` - Resource ARNs, profile YAML pre/code, collapsible audit log
- `cmd/configui/static/style.css` - Dark-theme operational CSS with all badge/layout/component styles
- `pkg/profile/schema_export.go` - SchemaJSON() accessor exposing embedded schema bytes

## Decisions Made

- **package main for all files**: Go prohibits two packages in one directory. The project pattern (Phase 03-02) uses `cmd/main.go` separation; here all files are `package main` with handler logic co-located since there's no separate library consumer yet.
- **buildTestTemplates() in handlers.go**: Inline template strings in the same package as handlers enable unit testing without filesystem access. The `truncateID` function is registered as a no-op to match production registration.
- **handleSandboxLogs graceful degradation**: When cwClient is nil or FilterLogEvents fails, the handler returns `<p>No audit logs available</p>` rather than an error — logs are supplementary information, not critical to sandbox operations.
- **sandbox_rows template in partials**: HTMX tbody polling replaces innerHTML; the partial template contains only `<tr>` elements, not a full table.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Package conflict resolved — handlers.go changed from package configui to package main**
- **Found during:** Task 1 (main.go creation)
- **Issue:** `handlers.go` was `package configui` but `main.go` must be `package main`; Go reported "found packages configui and main in same directory"
- **Fix:** Changed `handlers.go` and `handlers_test.go` from `package configui` to `package main`
- **Files modified:** cmd/configui/handlers.go, cmd/configui/handlers_test.go
- **Verification:** `go build ./cmd/configui/` succeeded after fix
- **Committed in:** c6cdfc4 (Task 1 feat commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - Bug)
**Impact on plan:** Necessary fix; no scope change. The single-binary `package main` pattern is consistent with `cmd/km/` and `cmd/ttl-handler/`.

## Issues Encountered

- `truncateID` template function referenced in `sandbox_row.html` required registration via `template.FuncMap` before `ParseFS`; added `templateFuncs` in `main.go` and a no-op version in `buildTestTemplates()` for test isolation.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- Handler struct and all routes ready; Plans 02-03 add editor and secrets handlers
- Stub routes registered: `GET /editor`, `GET /secrets`, `POST /api/validate`, `GET /api/profiles`, `POST /api/sandboxes/{id}/destroy`
- PII blur CSS classes (`.pii-blur`, `.pii-revealed`) pre-defined for Plan 03 secrets management
- `go test ./cmd/configui/...` passes; `go build ./cmd/configui/` succeeds

## Self-Check: PASSED

All created files verified to exist. All task commits (1a113c6, c6cdfc4, de7730a) confirmed in git log.

---
*Phase: 05-configui*
*Completed: 2026-03-22*
