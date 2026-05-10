# Phase 78 — Deferred Items

## Pre-existing Test Failures (Out of Scope)

These failures existed before Phase 78 work began. They are not caused by Phase 78 changes.

### TestStep11d_Success_WritesChannelIDParam (create_slack_test.go)
- SSM path mismatch: got `/km/sandbox/sb-test/slack-channel-id`, want `/sandbox/sb-test/slack-channel-id`
- Related to Phase 63/67 Slack parameter naming — not touched by Phase 78

### TestAtList_WithRecords (at_test.go)
- Unrelated to Phase 78 agent auth changes

### pkg/compiler failures (5 tests)
- TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock
- TestUserDataNotifyEnv_NoChannelOverride_NoChannelID
- TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime
- TestUserDataKMTracingServicectlStart
- TestGitHubUserDataGITASKPASS
- All pre-existing, unrelated to Phase 78 CLI work

## Follow-on Work (Plan 02 — Wave 2)

### runAgentAuthCodex stub
- Wave-1 stub returns: `--codex auth flow ships in Plan 02 (Wave 2)`
- Plan 02 implements: SSM port-forward to localhost:1455, `codex login` interactive session, `~/.codex/auth.json` verification
