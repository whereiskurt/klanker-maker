---
phase: 05-configui
plan: 03
subsystem: ui
tags: [ssm, go, htmx, html-template, pii-blur, secrets-management]

requires:
  - phase: 05-configui-01
    provides: Handler struct, base templates, HTMX scaffold, render helper, isHTMXRequest helper
  - phase: 05-configui-02
    provides: handlers_editor.go stubs, ssmClient SSMAPI field in Handler struct (from stub)
provides:
  - SSMAPI narrow interface for SSM Parameter Store dependency injection
  - handleSecretsPage: full-page secrets management UI at GET /secrets
  - handleSecretsList: GET /api/secrets returns parameter metadata JSON (no values)
  - handleSecretDecrypt: GET /api/secrets/{name...} returns decrypted value (pii-blur HTML for HTMX)
  - handleSecretPut: PUT /api/secrets/{name...} creates/updates SecureString parameter
  - handleSecretDelete: DELETE /api/secrets/{name...} idempotent delete
  - validateKMPrefix: rejects any parameter name not starting with /km/
  - secrets.html: Secrets Management page with pii-blur toggle, create form, refresh
  - secret_row.html: table row partial with Reveal/Edit/Delete HTMX actions
affects:
  - 05-configui-04
  - any plan requiring SSM parameter management via UI

tech-stack:
  added:
    - github.com/aws/aws-sdk-go-v2/service/ssm (already indirect, now direct import)
  patterns:
    - SSMAPI narrow interface — same pattern as CWLogsFilterAPI, SandboxLister; *ssm.Client satisfies directly
    - Paginated SSM list using manual NextToken loop (not NewGetParametersByPathPaginator — simpler for testing)
    - HTMX content negotiation: JSON for API, HTML partial for HX-Request; pii-blur span on decrypt
    - TDD: failing tests first, then implementation (11 RED tests -> GREEN)

key-files:
  created:
    - cmd/configui/handlers_secrets.go
    - cmd/configui/handlers_secrets_test.go
    - cmd/configui/templates/secrets.html
    - cmd/configui/templates/partials/secret_row.html
  modified:
    - cmd/configui/handlers.go (ssmClient SSMAPI field added to Handler struct)
    - cmd/configui/main.go (ssm.NewFromConfig, ssmClient injection, 5 secrets routes wired)

key-decisions:
  - "SSMAPI narrow interface in handlers_secrets.go; stub file from Plan 02 deleted (redeclare conflict)"
  - "handleSecretDecrypt returns pii-blur HTML span for HTMX, JSON for plain API callers"
  - "listKMSecrets uses manual NextToken loop — easier to unit-test than NewGetParametersByPathPaginator"
  - "secrets.html is fully self-contained (not using Go template block override) to avoid multi-page content block conflict"
  - "Overwrite=true on PutParameter — idempotent create/update; ParameterNotFound swallowed on delete"

patterns-established:
  - "PII safety: list endpoints never return values; only /api/secrets/{name...} decrypt returns value"
  - "pii-blur CSS class on revealed values with onclick toggle — matches style.css .pii-blur definition"
  - "Prefix guard: validateKMPrefix returns 400 for any non-/km/ path before touching AWS"

requirements-completed: [CFUI-04]

duration: 5min
completed: 2026-03-22
---

# Phase 05 Plan 03: Secrets Management UI Summary

**SSM Parameter Store CRUD UI at /secrets with HTMX pii-blur reveal, SecureString create/delete, and SSMAPI narrow-interface for testability**

## Performance

- **Duration:** 5 min
- **Started:** 2026-03-22T17:44:20Z
- **Completed:** 2026-03-22T17:49:36Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments

- Secrets page lists all SSM parameters under /km/ with metadata-only table (no value exposure)
- Reveal button fetches decrypted value via HTMX and wraps in pii-blur span (click to toggle)
- Create, edit (inline), and delete operations wired via HTMX; delete is idempotent (ParameterNotFound swallowed)
- 11 unit tests cover all behaviors including pagination, prefix validation, and ParameterNotFound idempotency
- All 30 configui tests pass; binary compiles cleanly

## Task Commits

Each task was committed atomically:

1. **Task 1: SSM secrets handlers + tests** - `52cd91d` (feat)
2. **Task 2: Secrets templates + route wiring** - `8fd388a` (feat, included with Plan 02 linter commit)

**Plan metadata:** (see final docs commit below)

_Note: Task 1 used TDD — failing tests committed first, then implementation._

## Files Created/Modified

- `cmd/configui/handlers_secrets.go` - SSMAPI interface, all 5 secrets handlers, validateKMPrefix, listKMSecrets
- `cmd/configui/handlers_secrets_test.go` - 11 unit tests with mockSSM, covers all behaviors
- `cmd/configui/templates/secrets.html` - Secrets Management page with create form, pii-blur style, refresh
- `cmd/configui/templates/partials/secret_row.html` - Table row partial: Reveal/Edit/Delete with HTMX
- `cmd/configui/handlers.go` - Added ssmClient SSMAPI field to Handler struct + secrets templates in buildTestTemplates
- `cmd/configui/main.go` - SSM client creation, ssmClient injection into Handler, 5 secrets routes wired

## Decisions Made

- **Stub file deleted:** `handlers_secrets_stub.go` from Plan 02 declared SSMAPI and stub methods; replaced entirely by `handlers_secrets.go` to avoid redeclaration compile errors.
- **Manual pagination loop:** Used manual `NextToken` loop instead of `ssm.NewGetParametersByPathPaginator` — the paginator wraps the same API calls but requires more mock complexity; simpler for testing.
- **HTMX content negotiation on decrypt:** Returns `<span class="pii-blur">` for HTMX requests, JSON for plain API — allows CLI/API access alongside UI reveal workflow.
- **Self-contained secrets.html:** Go's `{{block}}` override mechanism conflicts when multiple pages define "content" in the same parse set — secrets.html is standalone to avoid runtime template conflicts.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Deleted handlers_secrets_stub.go to resolve SSMAPI redeclaration**
- **Found during:** Task 1 GREEN phase (compiling after adding handlers_secrets.go)
- **Issue:** Plan 02 had created `handlers_secrets_stub.go` with SSMAPI interface and stub handler methods; our real implementation in `handlers_secrets.go` caused "redeclared in this block" compile error
- **Fix:** Removed the stub file — it was explicitly intended as temporary placeholder for Plan 03
- **Files modified:** cmd/configui/handlers_secrets_stub.go (deleted)
- **Verification:** `go build ./cmd/configui/` passes; all 11 secrets tests pass
- **Committed in:** 52cd91d (Task 1 feat commit)

---

**Total deviations:** 1 auto-fixed (Rule 3 — blocking issue)
**Impact on plan:** Required stub cleanup; no scope changes.

## Issues Encountered

None beyond the stub cleanup above.

## Next Phase Readiness

- Secrets CRUD fully operational; ready for Plan 04 integration
- pii-blur CSS already defined in style.css from Plan 01 — no CSS changes needed
- SSM client wired in main.go — Plan 04 can extend Handler with additional AWS clients using same pattern

---
*Phase: 05-configui*
*Completed: 2026-03-22*

## Self-Check: PASSED

- cmd/configui/handlers_secrets.go: FOUND
- cmd/configui/handlers_secrets_test.go: FOUND
- cmd/configui/templates/secrets.html: FOUND
- cmd/configui/templates/partials/secret_row.html: FOUND
- Commit 52cd91d: FOUND
- Commit 8fd388a: FOUND
