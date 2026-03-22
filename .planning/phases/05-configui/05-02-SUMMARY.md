---
phase: 05-configui
plan: 02
subsystem: ui
tags: [go, html/template, htmx, monaco-editor, monaco-yaml, json-schema, yaml-validation]

requires:
  - phase: 05-configui
    plan: 01
    provides: Handler struct, SandboxLister/SandboxFinder interfaces, base.html, profilesDir field, buildTestTemplates(), SchemaJSON()
  - phase: 01-schema-compiler-aws-foundation
    provides: profile.Validate(), ValidationError{Path, Message}, profile.SchemaJSON()

provides:
  - handleEditorPage: GET /editor — Monaco YAML editor page with optional ?profile= preload
  - handleValidate: POST /api/validate — calls profile.Validate(), returns JSON error array
  - handleProfileList: GET /api/profiles — returns [{name, hasExtends}] JSON; HTMX returns profile_list partial
  - handleProfileGet: GET /api/profiles/{name} — raw YAML with text/yaml content type
  - handleProfileSave: PUT /api/profiles/{name} — writes file, returns validation warnings
  - editor.html template with Monaco ESM import map, debounced validation, profile sidebar
  - profile_list.html partial with extends badge
  - isSafeName() path traversal guard

affects:
  - 05-03: secrets plan — all editor routes and Handler patterns established

tech-stack:
  added:
    - Monaco Editor 0.52.0 (CDN ESM import map) for YAML editing
    - monaco-yaml 5.2.0 (CDN ESM) for JSON Schema autocomplete
  patterns:
    - TDD RED/GREEN: failing tests committed first, then implementation
    - handleProfileList dual-mode: HTMX returns HTML partial, plain API returns JSON
    - isSafeName() for profile name sanitization (rejects ".." and "/" characters)
    - Hidden textarea pattern for Go template -> Monaco content injection (avoids html/template escaping)
    - Profile list HTMX partial loaded via hx-get="/api/profiles" hx-trigger="load"

key-files:
  created:
    - cmd/configui/handlers_editor.go
    - cmd/configui/handlers_editor_test.go
    - cmd/configui/templates/editor.html
    - cmd/configui/templates/partials/profile_list.html
  modified:
    - cmd/configui/main.go
    - cmd/configui/handlers.go

key-decisions:
  - "handleProfileList dual-mode response: HTMX (HX-Request header) returns profile_list HTML partial; plain API returns JSON array — avoids two separate routes for same data"
  - "Hidden textarea for initial YAML content: avoids Go html/template HTML-escaping of YAML special characters when injecting into Monaco editor value"
  - "Debounced validation at 500ms per plan spec: POST /api/validate returns empty array for valid input, errors mapped to Monaco markers at line 1 with path in message"
  - "isSafeName() rejects '..' and '/' in profile names — path traversal protection for GET and PUT profile routes"
  - "handleProfileSave: validation errors are warnings (save proceeds regardless) — operator may intentionally save work-in-progress profiles"

patterns-established:
  - "Editor handler testing: newEditorTestHandler(t) creates temp dir with sample profiles + buildEditorTestTemplates() with inline template strings"
  - "Profile CRUD: isSafeName() guard + os.ReadFile/WriteFile with 0644 perms — consistent with profiles/ directory convention"

requirements-completed:
  - CFUI-01

duration: 4min
completed: 2026-03-22
---

# Phase 5 Plan 02: Profile Editor Summary

**Monaco YAML editor at /editor with JSON Schema autocomplete via /api/schema, debounced /api/validate inline error markers, and profile CRUD via filesystem handlers**

## Performance

- **Duration:** 4 min
- **Started:** 2026-03-22T17:44:10Z
- **Completed:** 2026-03-22T17:49:09Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments

- Profile editor handlers with 10 passing tests: validate, list, get, save, editor page — all path traversal protected
- Monaco editor page with ESM import map (monaco-editor@0.52.0, monaco-yaml@5.2.0), JSON Schema autocomplete, and debounced validation using monaco.editor.setModelMarkers()
- HTMX sidebar loading profile list via `hx-get="/api/profiles"` on page load; new/save buttons with status bar
- Dual-mode handleProfileList: HTMX requests get profile_list HTML partial; plain API requests get JSON array

## Task Commits

1. **TDD RED: Failing editor handler tests** - `1c0507e` (test)
2. **Task 1: Profile editor handlers + tests** - `1cc9a62` (feat)
3. **Task 2: Monaco editor template, profile list partial, route wiring** - `8fd388a` (feat)

## Files Created/Modified

- `cmd/configui/handlers_editor.go` - handleEditorPage, handleValidate, handleProfileList, handleProfileGet, handleProfileSave, isSafeName()
- `cmd/configui/handlers_editor_test.go` - 10 unit tests covering all editor handler behaviors with temp dir fixtures
- `cmd/configui/templates/editor.html` - Monaco editor page: ESM import map, configureMonacoYaml, debounced validation, profile sidebar, extends panel
- `cmd/configui/templates/partials/profile_list.html` - Profile list partial with name and extends badge
- `cmd/configui/main.go` - Wired GET /editor, POST /api/validate, GET/PUT /api/profiles/{name}; removed stub handlers
- `cmd/configui/handlers.go` - Added ssmClient SSMAPI field to Handler struct (pre-wired by handlers_secrets.go)

## Decisions Made

- **handleProfileList dual-mode**: HTMX request (HX-Request header) returns profile_list HTML partial for the sidebar; plain API returns JSON array for JavaScript fetch calls. Single handler, no route duplication.
- **Hidden textarea for initial content**: Go html/template escapes `<`, `>`, `&` in YAML which would corrupt Monaco initial content. The textarea element is raw text, not parsed as HTML attributes, so template values pass through safely.
- **Validation warnings on save**: `handleProfileSave` runs `profile.Validate()` but saves regardless of errors — returned as a `warnings` JSON field. Operators may intentionally save incomplete profiles during editing.
- **isSafeName() guard**: Rejects empty names, names containing ".." or "/" — prevents path traversal on both GET and PUT profile routes.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] handlers_secrets_test.go present but handlers_secrets.go already pre-created**
- **Found during:** Task 1 (running editor tests)
- **Issue:** `handlers_secrets_test.go` referenced `SSMAPI` interface, `ssmClient` Handler field, and 5 handler methods. Initial attempt to create stub caused redeclaration errors — `handlers_secrets.go` already existed in the working tree.
- **Fix:** Recognized that `handlers_secrets.go` was pre-created by earlier work; no stub needed. The `ssmClient SSMAPI` field in Handler was already added by that file. Removed the conflicting stub.
- **Verification:** `go build ./cmd/configui/` succeeded, all 30 tests pass.
- **Committed in:** `1cc9a62` (Task 1 feat commit)

**2. [Rule 1 - Bug] TestHandleValidate_ValidYAML used incomplete YAML fixture**
- **Found during:** Task 1 GREEN phase
- **Issue:** Test YAML had only `lifecycle` + `runtime` spec fields; schema requires `execution`, `sourceAccess`, `network`, `identity`, `sidecars`, `observability`, `policy`, `agent` — causing 1 validation error on valid test case.
- **Fix:** Expanded test YAML to include all required spec fields (matching open-dev.yaml structure).
- **Verification:** TestHandleValidate_ValidYAML passes with empty error array.
- **Committed in:** `1cc9a62` (Task 1 feat commit)

---

**Total deviations:** 2 auto-fixed (1 Rule 3 blocking, 1 Rule 1 bug)
**Impact on plan:** Both fixes necessary for compilation and test correctness. No scope creep.

## Issues Encountered

- `handlers_secrets.go` was already present from a prior session, containing full secrets handler implementation and SSMAPI interface — required recognizing and avoiding duplication rather than creating stubs.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- All editor routes live: GET /editor, POST /api/validate, GET/PUT /api/profiles/{name}, GET /api/profiles
- Profile list, get, save, and validation fully operational
- Monaco editor with JSON Schema autocomplete and debounced validation inline markers
- Plan 03 (secrets) can proceed: Handler struct, SSMAPI interface, and route patterns all established

## Self-Check: PASSED
