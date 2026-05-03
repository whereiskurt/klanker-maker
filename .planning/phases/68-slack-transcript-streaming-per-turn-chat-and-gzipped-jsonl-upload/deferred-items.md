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
