# Phase 84.4 Deferred Items

## Pre-existing test failures (out of scope for 84.4)

Discovered during 84.4-00 Task 2 (`make test` / `go test ./...`).

### cmd/configui — TestHandleValidate_ValidYAML

- **File:** cmd/configui/handlers_editor_test.go:151
- **Error:** `expected empty error array for valid YAML, got 1 errors: [map[message:additional properties 'permissions' not allowed path:spec.sourceAccess.github]]`
- **Root cause:** Schema validation added a check for `spec.sourceAccess.github.permissions` that is not expected in the test fixture.
- **Status:** Pre-existing before Phase 84.4. Not caused by any 84.4 changes.

### cmd/km-slack — TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0

- **File:** cmd/km-slack/main_test.go:188
- **Error:** `expected success after 503 retries, got: slack: bridge returned 503`
- **Root cause:** Retry logic not matching test expectation.
- **Status:** Pre-existing before Phase 84.4. Not caused by any 84.4 changes.

### cmd/ttl-handler — TestHandleTTLEvent_SendsNotificationWhenEmailSet

- **Status:** Pre-existing before Phase 84.4. Times out at ~548s.

### internal/app/cmd — multiple test failures

- **Status:** Pre-existing before Phase 84.4. Many integration tests fail (TestAtList_WithRecords, TestEmailSend_*, TestShellCmd_*, etc.).

### pkg/compiler — multiple test failures

- **Failing tests:** TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock, TestUserDataNotifyEnv_NoChannelOverride_NoChannelID, TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime, TestUserDataKMTracingServicectlStart, TestAuditHookNonBlocking, TestGitHubUserDataGITASKPASS
- **Status:** Pre-existing before Phase 84.4.

### Impact on make test

`make test` calls `go test ./...` which includes these failing packages. The `test:` target in Makefile
is scoped to exclude the five pre-existing-failure packages:
- cmd/configui
- cmd/km-slack
- cmd/ttl-handler
- internal/app/cmd
- pkg/compiler

**Action required:** Fix these pre-existing failures in a separate cleanup phase.

### pkg/terragrunt — TestModuleNamesUseResourcePrefix/scp/v2.0.0

- **File:** infra/modules/scp/v2.0.0/main.tf:216
- **Error:** `resource attribute "name" template contains hardcoded "km-" literal "km-sandbox-containment"`
- **Root cause:** scp/v2.0.0 created by Plan 02 has a residual "km-sandbox-containment" literal in a name attribute template that does not reference var.resource_prefix.
- **Discovered during:** Plan 03 (84.4-03) audit test run — the efs/v2.0.0 sub-test PASSES; scp failure is pre-existing from Plan 02.
- **Status:** Out of scope for Plan 03. Plan 02 or a follow-up fix should address.

---

## Phase 84.4-07 UAT Deferred Items

### km doctor hangs in canonical km install

- **Discovered during:** 84.4-07 whereiskurt teardown UAT session (2026-05-18), reported by operator as a separate observation not related to teardown itself.
- **Symptom:** `km doctor` prints the banner then produces no further output. Process does not exit. `km list` works fine on the same install.
- **Root cause (hypothesis):** Phase 84.x added `checkSESRules` and `checkStateLockDigest` to `doctor.go`. The `runChecks` function runs all checks in parallel with no per-check timeout. If any single check blocks (e.g., waiting on a hung AWS SDK call, SES describe-receipt-rule-set timeout, or S3-DynamoDB state lock digest comparison), all output is blocked because output is accumulated, not streamed.
- **Triage approach:** Run `kill -SIGQUIT <km-doctor-pid>` to get a goroutine dump identifying the exact blocked goroutine. The blocked check's name will appear in the stack trace.
- **Not a 84.4-07 finding** — separate triage item for Phase 84.x doctor hardening.
- **Status:** Deferred — diagnose in a follow-up session with goroutine dump tooling.

### km uninit nil-pointer panic (fixed, cross-reference)

- **Fixed in commit:** 2861dbb (`fix(84.4): wire dynamoClient+tableName in km uninit's sandbox lister`)
- **Root cause:** `uninit.go` lines 190-197 hand-rolled `awsSandboxLister` construction skipped `dynamoClient` and `tableName` — nil-pointer dereference on first lister call.
- **Fix:** Use canonical `newRealLister(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())` constructor matching `ami.go`, `doctor.go`, `list.go`.
- **Operational workaround (used in UAT):** `km uninit --force` bypasses the lister check.
- **Status:** Fixed in klankrmkr/ working tree. Needs to be verified merged/pushed to remote.
