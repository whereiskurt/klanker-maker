# Deferred Items

## TestStep11d_Success_WritesChannelIDParam failure

**Found during:** Phase 72 Plan 07 execution
**Pre-existing:** Yes — caused by commit f54b8db (prefix scoping for non-default installs).
The test expects `/sandbox/sb-test/slack-channel-id` but `SandboxParameterPath` now returns `/km/sandbox/sb-test/slack-channel-id` when called with a `km` prefix (the test uses ssmPrefix="/km/").
**Scope:** Not caused by Plan 72-07 changes. The function `runStep11dInject` and `writeSlackChannelIDToSSM` are unchanged in this plan. Fix requires updating the test expectation to include the prefix or adjusting how the prefix is stripped/trimmed.
**Files:** `internal/app/cmd/create_slack_test.go:TestStep11d_Success_WritesChannelIDParam`
