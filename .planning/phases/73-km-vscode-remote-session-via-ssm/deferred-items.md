# Deferred Items — Phase 73 Plan 04

## Pre-existing test failures (out of scope — present before Plan 73-04)

These failures existed before Plan 73-04 was executed (verified by git stash check).
They are NOT regressions introduced by this plan.

| Test | File | Issue |
|------|------|-------|
| TestUserDataNotifyEnv_NoChannelOverride_NoChannelID | userdata_notify_test.go | KM_SLACK_CHANNEL_ID appears when it shouldn't |
| TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime | userdata_notify_test.go | KM_SLACK_BRIDGE_URL emission check failing |
| TestUserDataKMTracingServicectlStart | userdata_test.go | systemctl start line assertion failing |
| TestGitHubUserDataGITASKPASS | (unknown test file) | GITASKPASS env var assertion failing |

These should be investigated and fixed in a separate maintenance plan.
