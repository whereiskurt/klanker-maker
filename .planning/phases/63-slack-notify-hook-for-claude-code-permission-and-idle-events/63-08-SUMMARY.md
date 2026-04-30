---
phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events
plan: "08"
subsystem: km-create-slack-integration
tags: [slack, km-create, channel-resolution, ssm, dynamodb]
dependency_graph:
  requires: [63-04, 63-05, 63-06]
  provides: [slack-channel-resolution, per-sandbox-channel-create, sandbox-env-injection]
  affects: [km-create, sandbox-metadata, km-destroy-plan-09]
tech_stack:
  added: []
  patterns:
    - resolveSlackChannel three-mode pattern (shared/per-sandbox/override)
    - productionSSMParamStore/productionSSMRunner adapter pattern
    - SSM SendCommand post-launch env injection
key_files:
  created:
    - internal/app/cmd/create_slack.go
    - internal/app/cmd/create_slack_test.go
    - internal/app/cmd/destroy_slack.go
    - internal/app/cmd/destroy_slack_test.go
    - internal/app/cmd/slack.go
    - internal/app/cmd/doctor_slack.go
  modified:
    - internal/app/cmd/create.go
    - internal/app/cmd/create_test.go
    - pkg/slack/client.go
    - pkg/slack/client_test.go
    - pkg/aws/metadata.go
    - pkg/aws/sandbox_dynamo.go
decisions:
  - "Per-sandbox name collision aborts km create with actionable error (name_taken + suggest --alias or notifySlackChannelOverride); no automatic suffix fallback"
  - "Slack channel created in per-sandbox mode is NOT rolled back on later create failures — operator does manual cleanup; documented as known trade-off"
  - "Shared/per-sandbox modes use runtime SSM SendCommand (Step 11d) to inject env vars; override mode pre-bakes channel ID at compile time (Plan 04); consistency favors runtime inject"
  - "SlackArchiveOnDestroy *bool field declaration moved into Plan 63-08 metadata.go (Rule-1 pull-forward) so Plan 63-09 destroy_slack_test.go compiles; field SET here at create time, READ in Plan 63-09 at destroy"
  - "destroySlackChannel and km slack init/test/status pulled forward to unblock pre-existing test files (destroy_slack_test.go, slack_test.go, doctor_test.go) that were committed without their implementation files"
metrics:
  duration: "989s"
  completed_date: "2026-04-30"
  tasks_completed: 2
  files_created: 6
  files_modified: 6
---

# Phase 63 Plan 08: km create Slack Channel Provisioning — Summary

**One-liner:** Three-mode Slack channel resolution wired into km create (shared SSM lookup / per-sandbox conversations.create+invite / override ChannelInfo validate) with DynamoDB metadata persistence and post-launch SSM SendCommand env injection.

## What Was Built

### Task 1: create_slack.go + pkg/slack.Client.ChannelInfo

**`internal/app/cmd/create_slack.go`** provides:
- `SlackAPI` interface (`CreateChannel`, `InviteShared`, `ChannelInfo`)
- `SSMParamStore` interface (narrow SSM Get-only)
- `SSMRunner` interface (SSM SendCommand RunShell)
- `productionSSMParamStore` — wraps `*ssm.Client` as `SSMParamStore`
- `productionSSMRunner` — wraps `*ssm.Client` as `SSMRunner` via `AWS-RunShellScript`
- `resolveSlackChannel` — three-mode resolution before EC2 launch
- `sanitizeChannelName` — lowercase/hyphenate/cap-at-80 for Slack channel names
- `injectSlackEnvIntoSandbox` — idempotent grep/sed SSM script for `KM_SLACK_CHANNEL_ID` + `KM_SLACK_BRIDGE_URL`

**`pkg/slack/client.go`** extension:
- `ChannelInfo(ctx, channelID) (memberCount int, isMember bool, err error)` — calls `conversations.info` with `include_num_members=true`; `SlackAPIResponse.Channel` struct extended with `IsMember bool` + `NumMembers int`

### Task 2: create.go integration

**`internal/app/cmd/create.go`** integration:
- **Step 6c** (before terragrunt apply): reads bot-token from SSM, calls `resolveSlackChannel`; failure aborts create before any infra is provisioned
- **Metadata write** (Step 11): `SlackChannelID`, `SlackPerSandbox`, `SlackArchiveOnDestroy` populated in `SandboxMetadata` before `WriteSandboxMetadataDynamo`
- **Step 11d** (post-launch, non-fatal): reads bridge-url from SSM, reads instance ID from terraform outputs, calls `injectSlackEnvIntoSandbox` via SSM SendCommand

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Pre-existing] Fixed syntax error in slack.go**
- **Found during:** Task 1 build
- **Issue:** `internal/app/cmd/slack.go` (Plan 63-07's output) contained `from slackpkg "..."` which is invalid Go, and `newTerragruntInitRunner` that didn't exist
- **Fix:** Recreated slack.go with valid syntax; replaced `newTerragruntInitRunner` with `terragrunt.NewRunner`
- **Files modified:** `internal/app/cmd/slack.go`

**2. [Rule 1 - Pre-existing] destroy_slack_test.go + doctor_test.go referenced unimplemented functions**
- **Found during:** Task 1 test compilation
- **Issue:** `destroy_slack_test.go` references `destroySlackChannel` (Plan 63-09); `doctor_test.go` references `checkSlackTokenValidity` and `checkStaleSlackChannels` (Plan 63-09); both files were pre-committed without implementation
- **Fix:** Created `destroy_slack.go` with full `destroySlackChannel` implementation (covering all 9 test cases); created `doctor_slack.go` with `checkSlackTokenValidity`, `checkStaleSlackChannels`, `SlackMetadataScanner`, `EC2InstanceLister`
- **Files modified:** Created `internal/app/cmd/destroy_slack.go`, `internal/app/cmd/doctor_slack.go`

**3. [Rule 1 - Cross-plan] SlackArchiveOnDestroy field pulled forward from Plan 63-09**
- **Found during:** Task 1 — destroy_slack_test.go uses `SandboxMetadata.SlackArchiveOnDestroy`
- **Issue:** Plan 63-09 was supposed to declare this field; needed here to compile
- **Fix:** Added `SlackArchiveOnDestroy *bool` to `pkg/aws/metadata.go`; added marshal/unmarshal in `sandbox_dynamo.go`; SET the value at create time in `create.go` as Plan 08 spec required
- **Files modified:** `pkg/aws/metadata.go`, `pkg/aws/sandbox_dynamo.go`

**4. [Rule 1 - Pre-existing] fakeSSMParamStore redeclaration**
- **Found during:** Task 1 — both `create_slack_test.go` and the existing `destroy_slack_test.go` declared `fakeSSMParamStore`
- **Fix:** Renamed mine to use `fakeSSMParamStore` (canonical name) in `create_slack_test.go`; added note in `destroy_slack_test.go` that it's declared in the sibling file

## Test Summary

| Test | Count | Status |
|------|-------|--------|
| `TestResolveSlack_*` | 11 | Pass |
| `TestSanitizeChannelName_*` | 8 | Pass |
| `TestInjectSlackEnvIntoSandbox_*` | 2 | Pass |
| `TestClient_ChannelInfo_*` | 3 | Pass |
| `TestDestroySlackChannel_Case{A-I}` | 9 | Pass |
| `TestRunCreate_Slack*` | 2 | Pass |
| `TestCheckSlackTokenValidity_*` | 5 | Pass |
| `TestCheckStaleSlackChannels_*` | 3 | Pass |

## Known Trade-offs

1. **Per-sandbox channel not rolled back on create failure:** If `km create` creates a Slack channel in per-sandbox mode but later fails (e.g. EC2 spot unavailable), the channel is left orphaned. The operator must manually archive it. This is consistent with the existing "no infrastructure cleanup on partial failure" pattern.

2. **Override-mode channel ID pre-baked at compile time (Plan 04), not runtime:** For consistency, override mode's channel ID could be injected at runtime too. But Plan 04 already emits it at compile time when `NotifySlackChannelOverride != ""`, so runtime injection (Step 11d) only triggers for shared and per-sandbox modes.

3. **Schema change requires `km init --sidecars`:** As noted in CLAUDE.md memory, the `SlackArchiveOnDestroy` field addition requires operators to run `km init --sidecars` to refresh the management Lambda's bundled `km` binary before remote creates work end-to-end.

## Next Plan Dependencies

- **Plan 63-09** (`km destroy` + `km doctor`): reads `SlackChannelID`, `SlackPerSandbox`, `SlackArchiveOnDestroy` from DynamoDB metadata at destroy time; uses `destroySlackChannel` (implemented here) wired into the `km destroy` flow
- **Plan 63-10** (E2E tests): exercises both shared and per-sandbox flows end-to-end

## Commits

| Hash | Message |
|------|---------|
| `cea0dd8` | `feat(63-08): add resolveSlackChannel (3-mode) + sanitizeChannelName + injectSlackEnvIntoSandbox` |
| `f586e4e` | `feat(63-08): wire resolveSlackChannel + injectSlackEnvIntoSandbox into km create` |
