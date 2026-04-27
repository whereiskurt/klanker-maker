# Deferred Items — Phase 56

## Pre-existing Test Failures (out of scope for Phase 56-05)

### TestShellCmd_MissingInstanceID
- **Location:** internal/app/cmd/shell_test.go:337
- **Failure:** "expected error for missing instance ID, got nil"
- **Root cause:** `runShell` returns an error when no instance ARN is found, but
  the `RunE` closure in `NewShellCmdWithFetcher` uses `_ = runShell(...)` to
  intentionally discard the error (by design: avoids spurious cobra error output
  when the SSM session ends normally). The test expects the discarded error to
  propagate, which it never will under the current design.
- **Confirmed pre-existing:** fails on commit `2abfb81` (before Plan 56-05 changes)
- **Fix path:** Either change the test to test `runShell` directly (not via cobra),
  or thread the error from `runShell` back up through `RunE` when it's a real
  failure (not a normal session exit).

### TestUnlockCmd_RequiresStateBucket
- **Location:** internal/app/cmd/unlock_test.go
- **Failure:** Makes real DynamoDB call when AWS SSO credentials are expired
- **Root cause:** Test relies on AWS credentials check failing before DynamoDB
  call, but expired SSO tokens cause DynamoDB to be reached and return a
  different error message
- **Confirmed pre-existing:** unrelated to Phase 56-05
