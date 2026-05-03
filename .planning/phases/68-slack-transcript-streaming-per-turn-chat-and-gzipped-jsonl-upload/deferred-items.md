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

## Plan 68-09 confirmation: pre-existing TestUserDataNotifyEnv_* failures unchanged

While executing Plan 68-09 (km-notify-hook PostToolUse + Stop transcript
upload), the same two `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`
and `TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime` failures listed
above were re-confirmed as pre-existing (verified via `git stash && go test
… && git stash pop`). Plan 68-09's changes (km-notify-hook heredoc extension
+ PostToolUse settings.json registration + 11 new tests) do not touch the
KM_SLACK_CHANNEL_ID / KM_SLACK_BRIDGE_URL emission path, and `go build ./...`
remains clean after Plan 68-09 commits.

Plan 68-09's actual scope is 100% green:
- 10 new `TestNotifyHook_PostToolUse_*` and `TestNotifyHook_Stop_*` tests PASS
- 1 new `TestUserData_PostToolUseHookRegistered` test PASS
- 8 pre-existing Phase 62/63 `TestNotifyHook_*` tests still PASS (no regression)
- 2 pre-existing Phase 67 `TestUserdata_StopHook*` tests still PASS (the
  `# 6a.` / `# 6b.` markers in the heredoc are preserved)
- Full pkg/compiler suite: all tests PASS except the two pre-existing
  baseline failures listed above.

## Plan 68-07 confirmation: pre-existing TestShellCmd_* failures unchanged

While executing Plan 68-07 (`--transcript-stream` / `--no-transcript-stream` flag
plumbing for `km agent run` and `km shell`), the three `TestShellCmd_StoppedSandbox`,
`TestShellCmd_UnknownSubstrate`, and `TestShellCmd_MissingInstanceID` failures listed
above were re-confirmed as pre-existing — they fail identically on the pre-change
tree (verified via `git stash --keep-index … && go test … && git stash pop`). Plan
68-07's changes (adding `TranscriptStream *bool` to `AgentRunOptions`, two new flags
on each command, a `resolveTranscriptFlag` helper, and extending
`buildNotifySendCommands` with a third `transcript` arg) do NOT touch the
`runShell` error-propagation path that those tests assert on. The pre-existing
`_ = runShell(…)` swallow at shell.go:209 is not a Plan 68-07 regression.

Plan 68-07's actual scope is 100% green:
- All 3 new `TestAgentRun_TranscriptStream*` tests PASS
- All 3 new `TestShell_TranscriptStream*` tests PASS
- All existing `TestBuildAgentShellCommands_Notify*`, `TestBuildNotifySendCommands_*`,
  and `TestResolveNotifyFlags_*` tests still PASS (the existing
  `buildNotifySendCommands` callers in `shell_notify_test.go` were updated to pass
  `nil` as the new third arg — preserves Phase 62 semantics)
- `go build ./...` clean

## Plan 68-11 transient cross-plan compile conflict (resolved during execution)

While executing Plan 68-11 (km doctor checks for transcript streaming), the
test binary for `internal/app/cmd/...` briefly failed to link due to in-flight
artifacts from Plan 68-10 (running in parallel on the same branch per the
executor concurrency note):

- `internal/app/cmd/testhelpers_test.go:13:6: captureStderr redeclared in this block`
  (other declaration in `create_slack_transcript_test.go:36:6`)
- `internal/app/cmd/create_slack_transcript_test.go:61:3: undefined: printTranscriptWarning`
- `internal/app/cmd/create_slack_transcript_test.go:95:3: undefined: printTranscriptWarning`

Plan 68-11 did NOT modify any of these files (scope boundary). The conflict
resolved itself during Plan 68-11 execution as Plan 68-10's executor advanced
its tasks (printTranscriptWarning was added to its production source and the
duplicate captureStderr declaration was reconciled).

Final verification on Plan 68-11 commits:
- `go build ./...` clean
- `go vet ./internal/app/cmd/...` clean
- `go test ./internal/app/cmd/... -count=1 -run "TestDoctor_SlackTranscript|TestDoctor_SlackFilesWrite" -v` reports 12 PASS (5 original Wave-0 stub names + 7 added coverage cases)
- All Phase 67 `TestDoctor_SlackInbound*` tests still PASS (no regression — the new checks share the existing `getScopes` callback that the inbound suite drives).
