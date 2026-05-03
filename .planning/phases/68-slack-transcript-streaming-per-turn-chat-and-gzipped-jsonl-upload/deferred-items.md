# Deferred Items — Phase 68

Tracks pre-existing test failures discovered while running `go test ./...` during Plan 68-00 (Wave 0 stub seeding). These are NOT caused by Plan 68-00 changes (verified by stashing the new stub files and rerunning — same failures reproduce on the unmodified baseline).

Per the GSD execution scope-boundary rule, out-of-scope discoveries are logged here rather than auto-fixed.

## Pre-existing failures on baseline (gsd/phase-67-slack-inbound, commit 36f263b)

Confirmed via `git stash --include-untracked && go test … && git stash pop`.

### `pkg/compiler/`
- `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`
- `TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime`

### `cmd/km-slack/`
- `TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0`

### `internal/app/cmd/`
- `TestAtList_WithRecords`
- `TestConfigureInteractivePromptsUseNewNames`
- `TestCreateDockerWritesComposeFile`
- `TestApplyLifecycleOverrides_RunCreateRemoteSignature`
- `TestListCmd_EmptyStateBucketError`
- `TestLockCmd_RequiresStateBucket`
- `TestShellDockerContainerName`
- `TestShellDockerNoRootFlag`
- `TestLearnOutputPath`
- `TestShellCmd_StoppedSandbox`
- `TestShellCmd_UnknownSubstrate`
- `TestShellCmd_MissingInstanceID`
- `TestUnlockCmd_RequiresStateBucket`

## Disposition

These should be triaged separately (likely environment-dependent — many are bucket/state checks that probably need AWS env or test fixtures). They do NOT block Plan 68-00 verification because:

1. Plan 68-00 verify command targets only the new stub names — all 63 SKIPs reported.
2. The targeted package builds (`go build ./...`) succeeded without errors.
3. None of the failing tests overlap with the 13 stub files seeded in Plan 68-00.

If these are intended to pass on this branch, they should be addressed in a dedicated cleanup plan or marked `t.Skip` with a tracking note in their own packages.

## Plan 68-02 follow-up: 68-01 validation tests pre-promoted but not implemented

While executing Plan 68-02, the working tree contained an in-flight modification to `pkg/profile/validate_slack_transcript_test.go` that promoted four Plan 68-01 stubs to real assertions BEFORE the corresponding validation logic was added to `pkg/profile/validate.go`. That file was inadvertently swept into commit `78955b8` (Plan 68-02's Task 2 commit) because it was already-modified in the worktree.

Failing tests (all live in Plan 68-01's scope, not 68-02):

- `TestValidate_SlackTranscript_RequiresSlackEnabled`
- `TestValidate_SlackTranscript_RequiresPerSandbox`
- `TestValidate_SlackTranscript_IncompatibleWithChannelOverride`

These tests expect `notifySlackTranscriptEnabled` validation rules (must require `notifySlackEnabled: true`, must require `notifySlackPerSandbox: true`, must conflict with `notifySlackChannelOverride`). The validation logic landing in Plan 68-01's full implementation will turn them green. Until then, they are out-of-scope failures bundled into Plan 68-02's commit due to a worktree-staging accident — Plan 68-01's executor should pick them up when it adds the validation rules to `pkg/profile/validate.go`.

Plan 68-02's actual scope (`pkg/slack/...`) is 100% green:
- 4 new TestCanonicalJSON_ActionUpload / TestBuildEnvelopeUpload tests PASS
- All Phase 63 baseline tests still PASS (golden constant updated for new fields)
- `go build ./...` clean

## Plan 68-06 confirmation: pre-existing TestUserDataNotifyEnv_* failures unchanged

While executing Plan 68-06, the two `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`
and `TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime` failures listed above
were re-confirmed as pre-existing — they fail identically on the pre-change tree
(verified via `git stash && go test … && git stash pop`). Plan 68-06's changes
(adding `SlackStreamMessagesTableName` to `userDataParams` + `ec2HCLParams`, wiring
`artifacts_bucket` + `slack_stream_messages_table_name` into the ec2spot terragrunt
template, and adding new IAM policies in `infra/modules/ec2spot/v1.0.0`) do not
touch the KM_SLACK_CHANNEL_ID / KM_SLACK_BRIDGE_URL emission path. `go build ./...`
clean after Plan 68-06 changes.
