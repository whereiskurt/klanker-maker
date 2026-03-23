# Deferred Items — Phase 06

## Pre-existing Test Failures (out of scope)

### TestDestroyCmd_InvalidSandboxID (internal/app/cmd/destroy_test.go)

**Discovered:** Plan 06-01, Task 2
**Status:** Pre-existing failure, not caused by 06-01 changes
**Description:** `km destroy` does not validate sandbox ID format before attempting AWS operations.
  The test expects an error for IDs like "sandbox-123", "sb-ABCD1234", etc. but the command proceeds.
**Impact:** Low — destroy with invalid IDs will fail at AWS lookup rather than at input validation.
**Remediation:** Add sandbox ID regex validation in destroy.go RunE before AWS calls.
