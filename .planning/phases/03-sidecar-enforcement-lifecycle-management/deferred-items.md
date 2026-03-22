# Deferred Items — Phase 03

## Pre-existing Build Errors (Out of Scope)

### sidecars/audit-log/cmd/main.go — undefined kmaws symbols

**Found during:** Plan 03-03 Task 2 verification
**Status:** Pre-existing before plan 03-03 changes
**Errors:**
- `kmaws.CWLogsAPI` undefined
- `kmaws.EnsureLogGroup` undefined
- `kmaws.LogEvent` undefined
- `kmaws.PutLogEvents` undefined

**Cause:** `sidecars/audit-log/cmd/main.go` references types and functions in `pkg/aws` that have not yet been implemented. These stubs are expected to be implemented in a later plan (03-01 or 03-02 audit-log plan).

**Action needed:** Implement `CWLogsAPI`, `EnsureLogGroup`, `LogEvent`, `PutLogEvents` in `pkg/aws/` as part of the audit-log sidecar plan.
