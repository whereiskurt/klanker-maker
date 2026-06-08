# Deferred Items — Phase 99.1

Out-of-scope discoveries logged during execution. NOT fixed here (SCOPE BOUNDARY).

## Pre-existing, environment-induced test failure (Plan 04 execution)

- **Test:** `TestUnlockCmd_RequiresStateBucket` (`internal/app/cmd/unlock_test.go:62`)
- **Symptom:** Expects an error mentioning "state bucket" when `StateBucket==""`, but
  gets `sandbox sb-aabbccdd is not locked` instead.
- **Root cause:** The execution environment has live AWS credentials, so the unlock
  command path reaches a real DynamoDB GetItem (returning "not locked") before the
  empty-`StateBucket` guard would fire. The test assumes a no-credentials environment.
- **Scope:** Unrelated to the Phase 99.1 DLQ work (no SQS / doctor / DLQ code path).
  Confirmed pre-existing via `git stash` — fails identically on the clean Plan-03 tree.
- **Action:** Deferred. Belongs to the unlock command's test/guard ordering, not this phase.
