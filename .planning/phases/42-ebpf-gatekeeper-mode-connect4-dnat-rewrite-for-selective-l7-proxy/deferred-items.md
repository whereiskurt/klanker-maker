## Deferred Items — Phase 42

### Pre-existing Test Failure: TestUnlockCmd_RequiresStateBucket

**Discovered during:** Plan 42-03, Task 1 (make build + test suite)
**File:** internal/app/cmd/unlock_test.go:73
**Error:** `error should mention 'state bucket', got: sandbox sb-aabbccdd is not locked`
**Root cause:** Test expects error message to mention "state bucket" but the actual error message is "sandbox sb-aabbccdd is not locked". This was introduced in Phase 30 (commit 22366b1) and is unrelated to Phase 42 changes.
**Scope:** Pre-existing failure from Phase 30 (feat(30-02): add km lock and km unlock commands with tests). The pkg/ebpf/... and pkg/compiler/... tests all pass.
**Action needed:** Fix unlock error message to include "state bucket" context, or update test expectation. Out of scope for Phase 42.
