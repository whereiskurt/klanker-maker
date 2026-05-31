# Phase 92 — Deferred Items

Out-of-scope discoveries logged during execution. NOT fixed in their discovering wave.

## Pre-existing test failures (NOT caused by Wave 1 / 92-01)

Confirmed pre-existing via `git stash` baseline check — these fail on the clean
`phase-92-profile-spec-restructure` HEAD (commit 10dd37cc) BEFORE any 92-01 change,
and none reference identity/iam/agent/apiVersion. They are environment- or
mock-dependent (state bucket, AWS creds, docker exec args, nil-pointer in notification
handler). The packages 92-01 owns — `pkg/profile`, `pkg/compiler`, `pkg/allowlistgen` —
all pass.

| Package | Failing test(s) | Likely cause (pre-existing) |
|---------|-----------------|------------------------------|
| `cmd/configui` | `TestHandleValidate_ValidYAML` | test fixture `validYAML` has `permissions:` under `spec.sourceAccess.github` which the schema rejects (`additional properties 'permissions' not allowed`) — unrelated to Phase 92 |
| `cmd/km-slack` | `TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0` | bridge retry/exit-code mock |
| `cmd/ttl-handler` | `TestHandleTTLEvent_SendsNotificationWhenEmailSet` | nil-pointer deref in notification handler (main_test.go:209) |
| `internal/app/cmd` | `TestRunAgentAuthClaude_TeesAndCleans`, `TestStep11d_Success_WritesChannelIDParam`, `TestAtList_WithRecords`, `TestCreateDockerWritesComposeFile`, `TestApplyLifecycleOverrides_RunCreateRemoteSignature`, `TestRunDestroy_GitHubTokenCleanup`, `TestEmailSend_*`, `TestEmailRead_EncryptedMessageAutoDecrypts`, `TestLoadEFSOutputs_NotExist`, `TestListCmd_EmptyStateBucketError`, `TestLockCmd_RequiresStateBucket`, `TestShellDocker*`, `TestLearnOutputPath`, `TestShellCmd_*`, `TestStatusCmd_EmptyStateBucketError` | state-bucket env not set, docker exec arg expectations, AWS/SSM mocks, learn-output default — all environment/mock-dependent |

## Doc rewrites that belong to later waves

- `docs/profile-reference.md` documents the full `spec.agent.{maxConcurrentTasks,taskTimeout,allowedTools}`
  block (the DEAD one removed in Wave 1). Wave 4 re-introduces an `agent:` block with NEW
  structured tool-gating semantics. Wave 1 (92-01) marked the dead-agent sections as removed and
  migrated `identity:`→`iam:` / dropped `sessionPolicy` in the reference, but the full
  agent-section rewrite (new shape, examples) is Wave 4/5 doc work.
- `docs/codex-parity.md`, `docs/user-manual.md`, `docs/multi-agent-email.md`,
  `docs/budget-guide.md`, `docs/security-model.md`: contain profile YAML examples. 92-01 bumped
  their `apiVersion` to v1alpha2 and migrated any `identity:`/`sessionPolicy:`/dead-`agent:` keys
  for correctness; notification/agent narrative updates are owned by Waves 2–5.
