# Deferred Items — Quick Task 8

## Pre-existing Test Failures (Out of Scope)

These two tests were already failing on the `km-uptime-auth` branch **before** any Task-8 changes
were made (verified by running the tests after `git stash` against the pre-Task-8 commit `2392e7f4`):

1. **`TestListCmd_EmptyStateBucketError`** (`internal/app/cmd/list_test.go:191`)
   - Expected: `error != nil` when `StateBucket == ""` and no lister injected
   - Got: `nil` (the real lister path succeeds in the test environment because
     the local test runner actually loads an AWS config that returns a "no records" response
     rather than failing on the bucket name).
   - Cause: Test assumption about AWS config availability doesn't hold in this dev environment.
   - Action needed: Fix test to not depend on AWS config availability (mock the load).

2. **`TestStatusCmd_EmptyStateBucketError`** (`internal/app/cmd/status_test.go:301`)
   - Expected: error containing "state bucket not configured" or "get sandbox metadata"
   - Got: `sandbox not found: sb-test`
   - Cause: Same as above — real AWS path succeeds the config load and falls through to
     the DynamoDB FetchSandbox which returns "not found" rather than a bucket error.
   - Action needed: Fix test to not depend on AWS config availability.

Neither failure is related to Quick Task 8 changes. Both should be addressed separately.
