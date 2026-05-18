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
