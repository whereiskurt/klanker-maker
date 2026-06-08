# Phase 100 — Deferred / out-of-scope items

## Out-of-scope test failure (pre-existing, environment-dependent)

- **`TestUnlockCmd_RequiresStateBucket`** (`internal/app/cmd/unlock_test.go:62`)
  fails in this sandbox with `sandbox sb-aabbccdd is not locked` instead of the
  expected "state bucket" error. The test reaches **live AWS** (a real DynamoDB
  lock lookup succeeds) rather than short-circuiting on an empty `StateBucket`.
  - **Not caused by Plan 100-04** — it exercises `km unlock`, entirely unrelated to
    the doctor/github/config changes in this plan. It fails identically on the
    pre-change commit `f81fd713`.
  - Logged per the executor SCOPE BOUNDARY (only auto-fix issues directly caused by
    the current task's changes). Left for a follow-up that makes the unlock
    state-bucket guard run before any AWS resolution, or marks the test as requiring
    AWS-free isolation.
