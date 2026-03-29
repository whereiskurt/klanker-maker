# Deferred Items — Phase 29

## Pre-existing test failures from Plan 29-02

Test files using old sandbox ID format (e.g. `sb-001`, `sb-123`, `sb-ec2`) fail validation with the new `sandboxIDLike` pattern `^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$` introduced in Plan 29-02.

**Affected tests:**
- `TestBudgetAdd_*` (budget_test.go) — uses `sb-001`
- `TestShellCmd_EC2/ECS` (shell_test.go) — uses `sb-ec2`
- `TestStatusCmd_*` (status_test.go) — uses `sb-123`
- `TestStatus_*` (status_test.go) — uses `sb-123`

**Root cause:** Plan 29-02 tightened the sandbox ID pattern to require exactly 8 hex chars. Test fixtures were not updated.

**Resolution:** Update test fixture sandbox IDs to use valid 8-hex format (e.g. `sb-a1b2c3d4`).
