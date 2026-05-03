---
phase: 68
slug: slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-03
---

# Phase 68 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (Go 1.x — existing in repo) |
| **Config file** | none — uses standard `go test` discovery |
| **Quick run command** | `go test ./pkg/profile/... ./pkg/slack/... ./pkg/compiler/... ./internal/app/cmd/...` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~30s quick, ~90s full |

---

## Sampling Rate

- **After every task commit:** Run quick command (scoped to changed package)
- **After every plan wave:** Run full suite command
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 90 seconds

---

## Per-Task Verification Map

*Tasks not yet enumerated — populated by gsd-planner. Each task should map to one of these test surfaces:*

| Test Surface | Files | Type | Command |
|---|---|---|---|
| Profile schema validation | `pkg/profile/validation_test.go` | unit | `go test ./pkg/profile/... -run TranscriptEnabled` |
| Envelope canonical JSON | `pkg/slack/payload_test.go` | unit (table-driven) | `go test ./pkg/slack/... -run Envelope` |
| Slack client UploadFile | `pkg/slack/client_test.go` | unit (httptest server) | `go test ./pkg/slack/... -run UploadFile` |
| Bridge ActionUpload handler | `pkg/slack/bridge/*_test.go` | unit (table-driven) | `go test ./pkg/slack/bridge/...` |
| Notify hook script PostToolUse | `pkg/compiler/notify_hook_script_test.go` | unit (script harness with stub binaries) | `go test ./pkg/compiler/... -run NotifyHook` |
| km-slack subcommand dispatcher | `cmd/km-slack/main_test.go` | unit | `go test ./cmd/km-slack/...` |
| km-slack-bridge handler | `cmd/km-slack-bridge/main_test.go` | unit | `go test ./cmd/km-slack-bridge/...` |
| CLI flag plumbing (agent run / shell) | `internal/app/cmd/agent_test.go`, `shell_test.go` | unit | `go test ./internal/app/cmd/... -run TranscriptStream` |
| doctor checks | `internal/app/cmd/doctor_slack_transcript_test.go` (new) | unit (table-driven, mock interfaces) | `go test ./internal/app/cmd/... -run DoctorSlackTranscript` |
| km create operator warning | `internal/app/cmd/create_slack_test.go` | unit (capture stderr) | `go test ./internal/app/cmd/... -run CreateTranscriptWarning` |
| Compiler env injection | `pkg/compiler/userdata_test.go` | unit | `go test ./pkg/compiler/... -run NotifyEnv` |

*Status column populated as tasks land: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Test stubs to seed before any implementation work begins (so each Wave 1+ task can write tests first per TDD discipline):

- [ ] `pkg/profile/validation_test.go` — add `TestValidateNotifySlackTranscriptEnabled` covering: requires `notifySlackEnabled`, requires `notifySlackPerSandbox`, incompatible with `notifySlackChannelOverride`, all-true accepted
- [ ] `pkg/slack/payload_test.go` — add `TestActionUploadEnvelopeCanonicalJSON` and `TestEnvelopeBackwardsCompatPostFieldsZeroed` (post envelope with new struct must serialize identically to pre-Phase-68 form)
- [ ] `pkg/slack/client_test.go` — add `TestUploadFile_3StepFlow_Success`, `TestUploadFile_Step1Failure`, `TestUploadFile_Step2NetworkFailure`, `TestUploadFile_Step3Failure` (httptest.Server stub for Slack)
- [ ] `pkg/slack/bridge/handler_test.go` (or wherever existing bridge tests live) — add `TestActionUpload_PrefixValidation`, `TestActionUpload_SizeCap`, `TestActionUpload_ContentTypeAllowlist`, `TestActionUpload_ScopeMissing`
- [ ] `pkg/compiler/notify_hook_script_test.go` — extend with `TestPostToolUse_GateOff`, `TestPostToolUse_GateOn_AutoThreadParent`, `TestPostToolUse_GateOn_ExistingThread`, `TestPostToolUse_OffsetTracking_MultiFire`, `TestStop_UploadsAndCleansUp`. New fixture files: `notify-hook-fixture-posttooluse.json`, `notify-hook-fixture-multitool-transcript.jsonl`
- [ ] `cmd/km-slack/main_test.go` — restructure for multi-subcommand dispatch; add `TestDispatch_Upload`, `TestDispatch_RecordMapping`, `TestDispatch_UnknownSubcommand`
- [ ] `cmd/km-slack-bridge/main_test.go` — add `TestActionUploadRouting` (envelope action=upload reaches new handler)
- [ ] `internal/app/cmd/agent_test.go` — add `TestAgentRun_TranscriptStreamFlag`, `TestAgentRun_NoTranscriptStreamFlag`
- [ ] `internal/app/cmd/shell_test.go` — add `TestShell_TranscriptStreamFlag`, `TestShell_NoTranscriptStreamFlag`
- [ ] `internal/app/cmd/doctor_slack_transcript_test.go` (NEW file) — `TestSlackTranscriptTableExists`, `TestSlackFilesWriteScope`, `TestSlackTranscriptStaleObjects` (mock S3 + DDB clients)
- [ ] `internal/app/cmd/create_slack_test.go` — add `TestCreate_TranscriptWarning_PrintsWhenEnabled`, `TestCreate_TranscriptWarning_AbsentWhenDisabled`
- [ ] `pkg/compiler/userdata_test.go` — add `TestUserData_NotifySlackTranscriptEnabledEnvVar`, `TestUserData_PostToolUseHookRegistered`

*Existing test infrastructure (httptest, table-driven patterns, in-process AWS SDK mocks via interfaces) covers all required surfaces. No framework install needed.*

---

## Manual-Only Verifications

These behaviors require a real EC2 sandbox + a real Slack workspace and cannot be exercised by unit tests:

| Behavior | Why Manual | Test Instructions |
|----------|------------|-------------------|
| End-to-end stream visible in Slack thread | Requires real Slack workspace, real bot token, real channel, real Phase 67 inbound or operator-CLI dispatch | `make build && km init --sidecars && km create profiles/test-transcript.yaml`; `km agent run sb-X --prompt "audit and fix the failing tests"`; observe `#sb-X` channel — per-turn messages stream into a thread, final transcript file appears at Stop |
| Auto-thread-parent for operator runs | Hook ts-resolution path requires real Slack response to capture parent ts | Same setup; `km agent run sb-X --prompt "..."` (NOT triggered from Slack); verify a `🤖 [sb-X] turn started — ...` parent message is posted to channel root and subsequent fires thread under it |
| File upload via 3-step flow opens correctly in Slack | Slack files API behavior + browser rendering | Click the uploaded `claude-transcript-{sid}.jsonl.gz` in the Slack thread; verify download succeeds, `gzip -d` produces valid JSONL, lines parse |
| 100 MB transcript upload memory + timeout headroom | Lambda runtime constraints | Synthesize a large JSONL; trigger upload via test envelope; confirm Lambda CloudWatch logs show < 200 MB peak memory and < 60s end-to-end |
| `files:write` scope missing path | Requires Slack App admin to remove scope | Remove `files:write` from the App's scopes (in Slack admin); trigger an upload; verify bridge returns 400 `scope_missing`; verify hook stderr in journald shows `WARN: operator must re-auth Slack App with files:write`; verify hook still exits 0 (Claude not blocked) |
| Operator warning at `km create` | stderr output during real `km create` flow | `km create profiles/test-transcript.yaml`; verify a single `⚠ Slack transcript streaming enabled — ...` line appears on stderr including correct channel ID + member count |
| Phase 63 regression (idle-ping unchanged for non-opt-in sandbox) | E2E path through hook + bridge + Slack | Create a sandbox with `notifySlackTranscriptEnabled: false` (default); run any agent prompt; verify Phase 63 single idle-ping arrives, NO per-turn streaming, NO upload |
| Phase 67 regression (inbound still works) | E2E path through poller + km agent run | Create sandbox with `notifySlackInboundEnabled: true` AND `notifySlackTranscriptEnabled: true`; post a message in `#sb-X`; verify poller dispatches, agent run starts, replies stream into the same thread (Phase 67 thread), final transcript uploads under same thread |
| `km destroy` cleanup of S3 transcript objects | Bucket lifecycle behavior + km destroy semantics | After multiple agent runs producing transcripts: `km destroy sb-X --remote --yes`; verify either (a) lifecycle policy will eventually delete the prefix, or (b) `km destroy` explicitly cleans `s3://${KM_ARTIFACTS_BUCKET}/transcripts/sb-X/`. Decision point for planner: which approach. |
| `km doctor` stale-objects check accuracy | Requires real S3 + DDB state | Create + destroy a sandbox; manually leave a transcript object behind (or wait for `km destroy` not to clean); run `km doctor`; verify `slack_transcript_stale_objects` advisory fires with correct prefix |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (12 stub items above)
- [ ] No watch-mode flags
- [ ] Feedback latency < 90s (full suite estimate)
- [ ] `nyquist_compliant: true` set in frontmatter (set by gsd-plan-checker after plans pass)

**Approval:** pending
