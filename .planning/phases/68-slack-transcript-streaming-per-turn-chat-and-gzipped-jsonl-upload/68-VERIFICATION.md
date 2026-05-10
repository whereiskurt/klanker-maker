---
phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
verified: 2026-05-10T14:55:00Z
status: gaps_found
score: 8/12 must-haves verified
gaps:
  - truth: "km agent run transcript streaming fires per-turn PostToolUse hooks"
    status: failed
    reason: "claude -p (print/non-interactive mode) does not fire PostToolUse hooks per Claude Code platform behavior. The hook script, env var gate, and settings.json registration are all correct — but the Claude Code runtime skips PostToolUse in -p mode regardless."
    artifacts:
      - path: "internal/app/cmd/agent.go:1179"
        issue: "BuildAgentShellCommands emits `claude -p \"$PROMPT\"` — PostToolUse hooks confirmed non-firing in UAT; stderr hook log showed 0 bytes across multiple km agent run invocations"
      - path: "pkg/compiler/userdata.go:3085"
        issue: "appendKMHook(PostToolUse, ...) registers the hook unconditionally — correct, but ineffective when claude -p skips PostToolUse"
    missing:
      - "Phase 68.1 fix: pivot km agent run to post-process transcript after run completion (read transcript path from run dir, gzip, upload — no hook needed); or wait for Claude Code -p mode to support PostToolUse hooks"
  - truth: "Final gzipped JSONL transcript is uploaded as a Slack file at Stop for per-sandbox Slack Connect channels"
    status: failed
    reason: "files.completeUploadExternal returns internal_error for externally-shared (is_ext_shared: true) Slack Connect channels — Slack platform restriction on cross-workspace file uploads. Per-turn chat lines and auto-thread work; only the .jsonl.gz attachment fails. No channel-type detection or fallback path exists in the code."
    artifacts:
      - path: "pkg/slack/client.go:233-257"
        issue: "UploadFile step 3 (files.completeUploadExternal) returns internal_error for Slack Connect channels with no detection or fallback"
      - path: "pkg/slack/bridge/handler.go:369-381"
        issue: "ActionUpload handler calls h.FileUploader.UploadFile without channel-type detection; no dual-path for Connect vs internal channels"
    missing:
      - "Phase 68.1 fix: conversations.info channel-type probe at km create time; record is_ext_shared in sandbox metadata; bridge ActionUpload selects upload path (native vs S3 presigned URL message) based on channel type. Recommended: option 3 from deferred-items.md."
  - truth: "Operator sees a single Slack thread per operator turn (not one per subagent session)"
    status: failed
    reason: "_km_stream_drain caches the auto-thread-parent ts per Claude session_id (/tmp/km-slack-thread.${sid}). When Task-tool or /clone creates subagents, each has a distinct session_id and creates its own top-level thread parent. UAT confirmed 3 distinct session_ids posting parents for a single operator activity."
    artifacts:
      - path: "pkg/compiler/userdata.go:551"
        issue: "local thread_cache=\"/tmp/km-slack-thread.${sid}\" — cache key is session_id, not operator turn. KM_OPERATOR_TURN_ID does not exist; no detect-and-reuse logic for recent sibling threads"
    missing:
      - "Phase 68.1 fix: inject KM_OPERATOR_TURN_ID (reusing top-level run session_id) into Claude env before km agent run / km shell; change thread_cache key to use KM_OPERATOR_TURN_ID when set. Alternative: detect-and-reuse any /tmp/km-slack-thread.* file newer than N minutes."
  - truth: "Operator sees transcript audience-containment warning on km create for all create paths (including default --remote EC2)"
    status: failed
    reason: "printTranscriptWarning is called at create.go:509 inside runCreate (local path) but runCreateRemote (lines 1785-2067, the default EC2 path) has zero Slack or transcript references — no warning fires. Most operators creating EC2 sandboxes never see the warning."
    artifacts:
      - path: "internal/app/cmd/create.go:509"
        issue: "printTranscriptWarning wired only in runCreate (local create); confirmed by grepping lines 1785-2067 of runCreateRemote — zero Slack/transcript/printTranscript references"
    missing:
      - "Phase 68.1 fix option A: resolve Slack channel locally in runCreateRemote before artifact upload + EventBridge dispatch, call printTranscriptWarning (mirrors runCreate lines 467-510). Option B: have create-handler Lambda include the warning in its SES completion email."
---

# Phase 68: Slack Transcript Streaming Verification Report

**Phase Goal:** Per-turn assistant text + tool one-liners stream to per-sandbox Slack channel thread; final gzipped JSONL transcript uploaded as Slack file at Stop hook. Opt-in via `notifySlackTranscriptEnabled` profile field. Layered on Phase 67's per-sandbox channel. New DDB table `{prefix}-slack-stream-messages`. S3 layout `transcripts/{sandbox_id}/{session_id}.jsonl.gz`. Bot scope `files:write` required.
**Verified:** 2026-05-10T14:55:00Z
**Status:** gaps_found
**Re-verification:** No — initial verification (retroactive; no prior VERIFICATION.md existed)

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | `notifySlackTranscriptEnabled` profile field exists, is validated, and gated on Phase 67 prerequisites | VERIFIED | `pkg/profile/types.go:446` field; `validate.go:358-380` three rules; schema `sandbox_profile.schema.json:539`; 5 tests PASS |
| 2 | Per-turn PostToolUse streaming fires in interactive `km shell` sessions | VERIFIED | `pkg/compiler/userdata.go:3085` registers hook unconditionally; `userdata.go:661-668` PostToolUse branch; `userdata.go:484` gates on `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=1`; 10 hook tests PASS; UAT Scenario 2 confirmed auto-thread-parent + per-turn threading in interactive mode |
| 3 | **km agent run transcript streaming fires per-turn PostToolUse hooks** | FAILED | `claude -p` skips PostToolUse hooks (Claude Code platform behavior). UAT Scenario 1 confirmed 0 bytes in hook log. --bare removal (commit 7911c1c) necessary but not sufficient. |
| 4 | Stop hook gzips transcript, uploads to S3, calls bridge ActionUpload | VERIFIED | `userdata.go:787-841` Stop branch; gzip + `s3 cp transcripts/${sandbox_id}/${st_sid}.jsonl.gz`; `km-slack upload` call; 2 Stop tests PASS (`TestNotifyHook_Stop_TranscriptUpload`, `TestNotifyHook_Stop_CleansUpTmpFiles`) |
| 5 | **Final .jsonl.gz Slack file attachment works for all per-sandbox channels (including Slack Connect)** | FAILED | `files.completeUploadExternal` returns `internal_error` for `is_ext_shared: true` channels. No detection or fallback path in `pkg/slack/client.go` or `pkg/slack/bridge/handler.go`. UAT Scenario 1: per-turn chat works; file attachment blocked. |
| 6 | DDB `{prefix}-slack-stream-messages` table provisioned by `km init` and record-mapping wired | VERIFIED | `internal/app/cmd/init.go:163-164` registers module; `infra/live/use1/dynamodb-slack-stream-messages/terragrunt.hcl` exists; `userdata.go:645-651` calls `km-slack record-mapping` when table name set; UAT Scenario 7 confirmed table ACTIVE in doctor |
| 7 | S3 `transcripts/{sandbox_id}/{session_id}.jsonl.gz` layout enforced | VERIFIED | `userdata.go:822` S3 key pattern; `bridge/handler.go:320-322` prefix validation (`transcripts/{senderID}/`); doctor `checkSlackTranscriptStaleObjects` sweeps `transcripts/` |
| 8 | `km-slack upload` and `km-slack record-mapping` subcommands dispatch correctly | VERIFIED | `cmd/km-slack/main.go:59-62` dispatch; `runUpload` (line 132) and `runRecordMapping` (line 209) implemented; 5 dispatch tests PASS |
| 9 | Bridge `ActionUpload` handler validates and calls `UploadFile` | VERIFIED | `pkg/slack/bridge/handler.go:301` ActionUpload case; `handler.go:369-381` S3Get + UploadFile; 9 handler tests PASS; bridge main wires S3Getter + FileUploader (main.go:102-114) |
| 10 | **Operator sees audience-containment warning on all km create paths** | FAILED | `printTranscriptWarning` called at `create.go:509` in `runCreate` (local path only). `runCreateRemote` (lines 1785-2067) has zero Slack/transcript references — warning never fires for default EC2 `--remote` creates. UAT Scenario 6: PARTIAL. |
| 11 | **Operator sees one Slack thread per operator turn, not one per subagent session** | FAILED | `_km_stream_drain` caches thread ts per `session_id` (`userdata.go:551`). No `KM_OPERATOR_TURN_ID` exists. UAT confirmed 3 distinct session_ids posting top-level parents for a single operator activity. |
| 12 | `km doctor` adds three transcript checks; `files:write` scope enforced | VERIFIED | `internal/app/cmd/doctor_slack_transcript.go` implements `checkSlackTranscriptTableExists`, `checkSlackFilesWriteScope`, `checkSlackTranscriptStaleObjects`; wired into doctor at lines 2640, 2648, 2659; UAT Scenario 7 PASS (all 3 checks green) |

**Score:** 8/12 truths verified

---

### Required Artifacts

| Artifact | Purpose | Status | Details |
|----------|---------|--------|---------|
| `pkg/profile/types.go` | `NotifySlackTranscriptEnabled` field on CLISpec | VERIFIED | Line 446; `omitempty` YAML/JSON |
| `pkg/profile/validate.go` | Three validation rules | VERIFIED | Lines 358-380; all 5 tests PASS |
| `pkg/profile/schemas/sandbox_profile.schema.json` | Schema entry | VERIFIED | Line 539 |
| `pkg/slack/client.go` | `UploadFile` 3-step flow | VERIFIED (partial) | Implemented; step 3 fails for Slack Connect channels — no detection |
| `pkg/slack/bridge/handler.go` | `ActionUpload` handler | VERIFIED (partial) | Wired; no channel-type detection for Connect fallback |
| `pkg/compiler/userdata.go` | km-notify-hook PostToolUse + Stop branches; env injection; settings.json registration | VERIFIED | Lines 479-843, 3082-3085, 3302-3304 |
| `cmd/km-slack/main.go` | `upload` and `record-mapping` subcommands | VERIFIED | Lines 59-62, 132-262 |
| `cmd/km-slack-bridge/main.go` | ActionUpload wiring in bridge Lambda | VERIFIED | Lines 102-114 |
| `internal/app/cmd/create.go` | `printTranscriptWarning` helper | VERIFIED (partial) | Helper exists and tested; NOT wired into `runCreateRemote` |
| `internal/app/cmd/doctor_slack_transcript.go` | Three doctor checks | VERIFIED | All three functions implemented and wired |
| `internal/app/config/config.go` | `GetSlackStreamMessagesTableName()` | VERIFIED | Line 481; 4 tests PASS |
| `infra/live/use1/dynamodb-slack-stream-messages/terragrunt.hcl` | DDB table Terragrunt config | VERIFIED | Exists; wired in `init.go:163-164` |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `pkg/compiler/userdata.go` | `km-notify-hook PostToolUse` | `appendKMHook("PostToolUse", ...)` at line 3085 | WIRED | Hook registered in settings.json unconditionally; gates at runtime on `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=1` |
| `km-notify-hook PostToolUse` → `km-slack post` | Slack thread parent + per-turn messages | `_km_stream_drain` at line 538 | WIRED for km shell | NOT WIRED for km agent run (claude -p skips PostToolUse) |
| `km-notify-hook Stop` → `gzip` → `aws s3 cp` → `km-slack upload` | S3 upload + bridge ActionUpload | `userdata.go:787-841` | WIRED | Stop hook fires in both interactive and non-interactive modes |
| `km-slack upload` → bridge `ActionUpload` → `pkg/slack/Client.UploadFile` | Final Slack file attachment | `handler.go:381` | WIRED but broken for Slack Connect | `files.completeUploadExternal` fails for `is_ext_shared: true` channels |
| `km-slack record-mapping` → DDB `km-slack-stream-messages` | `(channel_id, slack_ts) → offset` mapping | `userdata.go:645-651` | WIRED | Conditional on `KM_SLACK_STREAM_TABLE` being set |
| `runCreate` → `printTranscriptWarning` | Operator audience-containment warning | `create.go:508-510` | PARTIAL | Only wired in `runCreate`; `runCreateRemote` (default EC2 path, lines 1785-2067) has no Slack block |
| `thread_cache /tmp/km-slack-thread.${sid}` | Per-session thread parent ts | `userdata.go:551` | WIRED but incorrect scope | `sid` = Claude `session_id`; should be operator turn ID for subagent fan-out |

---

### Requirements Coverage

Phase 68 uses phase-local must-haves from plan frontmatter (no REQ-* IDs in REQUIREMENTS.md).

| Plan | Core Requirement | Status | Evidence |
|------|-----------------|--------|---------|
| 68-01 | `notifySlackTranscriptEnabled` field + validation | SATISFIED | Types, validate, schema all present; 5 tests PASS |
| 68-02 | `ActionUpload` envelope canonical JSON + `BuildEnvelopeUpload` | SATISFIED | `pkg/slack/payload.go`; 4 tests PASS |
| 68-03 | `GetSlackStreamMessagesTableName()` config helper + DDB terragrunt | SATISFIED | config.go:481; terragrunt.hcl; init.go:163; 4 tests PASS |
| 68-04 | `Client.UploadFile` 3-step flow | SATISFIED (partial) | 7 tests PASS; step 3 fails for Slack Connect channels — known platform limitation |
| 68-05 | `km-slack` multi-subcommand dispatch (post/upload/record-mapping) | SATISFIED | main.go dispatch; 5 tests PASS |
| 68-06 | Env injection (`KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED`, `KM_SLACK_STREAM_TABLE`) + IAM policies | SATISFIED | userdata.go:3302-3304; service_hcl.go:132 |
| 68-07 | `--transcript-stream` / `--no-transcript-stream` flags on `km agent run` + `km shell` | SATISFIED | agent.go:1216-1222; 6 tests PASS |
| 68-08 | Bridge `ActionUpload` handler validates + calls S3Getter + FileUploader | SATISFIED | handler.go:301-381; 9 tests PASS; bridge main wires adapters |
| 68-09 | PostToolUse + Stop hook implementation (`_km_stream_drain`, offset tracking, tool one-liner, gzip upload) | SATISFIED for km shell | PostToolUse silently no-ops for km agent run (claude -p platform behavior) |
| 68-10 | Env emission + `printTranscriptWarning` at `km create` | PARTIALLY SATISFIED | Warning helper correct + tested; not wired into `runCreateRemote` |
| 68-11 | Three `km doctor` checks | SATISFIED | All three implemented and wired; UAT PASS |
| 68-12 | Documentation (CLAUDE.md, docs/slack-notifications.md, UAT) | SATISFIED | UAT Scenario sign-off by operator; docs updated |

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/app/cmd/agent.go` | 1179 | `claude -p "$PROMPT"` invocation | BLOCKER | PostToolUse hooks never fire for km agent run; transcript streaming silently no-ops |
| `internal/app/cmd/create.go` | 1785-2067 | `runCreateRemote` has zero Slack references | WARNING | Operator audience-containment warning absent on default EC2 create path |
| `pkg/compiler/userdata.go` | 551 | `thread_cache="/tmp/km-slack-thread.${sid}"` (session_id as key) | WARNING | Subagent fan-out creates N top-level Slack thread parents per operator turn |
| `pkg/slack/client.go` | 233-257 | No `internal_error` detection or fallback for Slack Connect channels | BLOCKER | Final .jsonl.gz file upload silently fails for all Slack Connect per-sandbox channels |

---

### Human Verification Required

The following UAT scenarios from 68-12-UAT.md were deferred or skipped and remain outstanding:

#### 1. End-to-end km shell transcript stream (Scenario 1, interactive path)

**Test:** `km create` a profile with `notifySlackTranscriptEnabled: true`, run `km shell <sandbox>`, execute multi-tool Claude session
**Expected:** Per-turn `🔧 ToolName: input` messages appear in `#sb-X` Slack thread; final `.jsonl.gz` appears as Slack file (internal channel) or is pullable from S3 (Slack Connect channel)
**Why human:** Requires live sandbox + Slack workspace + AWS; per-turn hook firing verified in unit tests but not re-confirmed against production since UAT 2026-05-04

#### 2. Phase 63 regression (Scenario 4 — already PASS in UAT)

**Test:** Profile with `notifySlackTranscriptEnabled: false` — confirm no transcript artifacts created
**Expected:** Only Phase 63 idle-ping messages; S3 `transcripts/` empty; DDB no rows
**Why human:** UAT marked PASS on 2026-05-04; no re-run needed unless code changed since

#### 3. km agent run transcript streaming (Scenario 1, non-interactive)

**Test:** `km agent run <sandbox> --prompt "audit tests" --transcript-stream`
**Expected:** Per-turn streaming no-ops (known gap); Stop hook should still fire and upload final transcript to S3
**Why human:** UAT UAT Scenario 1 result was "PASS-with-Slack-Connect-limitation"; needs re-test specifically for Stop-only path confirmation after --bare removal (commit 7911c1c)

#### 4. files:write scope missing path (Scenario 5 — skipped in UAT)

**Test:** Remove `files:write` from bot, force bridge cold start, run transcript session, observe hook stderr, restore scope
**Expected:** Hook exits 0; streaming continues; upload returns 400; hook logs WARN; after restore, next run succeeds
**Why human:** Cold-start scope probe at bridge init validates ergonomically but negative path was not exercised

#### 5. Synthetic 100MB transcript memory test (Scenario 8 — skipped in UAT)

**Test:** Create synthetic 100MB JSONL, trigger bridge upload
**Expected:** Bridge Lambda peak memory < 200 MB; end-to-end < 60 seconds
**Why human:** Requires real Lambda invocation; streaming body path verified in unit tests only

---

## Gaps Summary

Phase 68 delivered a structurally complete transcript streaming system: profile field, validation, envelope format, S3 upload, bridge ActionUpload handler, DDB record-mapping, km doctor checks, and per-turn streaming for interactive `km shell` sessions. The UAT was conducted by operator KPH on 2026-05-04 and resulted in a SHIP verdict with four documented Phase 68.1 follow-ups.

**Four production-blocking gaps confirmed in code:**

**Gap 1 — km agent run transcript streaming silently no-ops (`agent.go:1179`).**
`claude -p` mode (non-interactive print) does not fire PostToolUse hooks per Claude Code platform behavior. The hook registration, gate conditions, and env injection are all correct; the platform simply does not call PostToolUse in print mode. Removing `--bare` (commit 7911c1c) was a necessary prerequisite but not sufficient. Phase 68's transcript streaming is interactive-only in production.

**Gap 2 — Final .jsonl.gz upload fails for Slack Connect channels (`client.go:257`, `handler.go:381`).**
`files.completeUploadExternal` returns `internal_error` for `is_ext_shared: true` channels. This affects every operator using the default per-sandbox channel provisioning (Phase 63/67 provisioned all channels via Slack Connect). The 3-step upload flow, S3 retrieval, and bridge routing all work correctly — failure is at the Slack API boundary. No detection or fallback path exists in the code. Per-turn chat lines continue to work. The workaround is manual S3 pull (`aws s3 ls`).

**Gap 3 — Subagent fan-out creates N Slack thread parents (`userdata.go:551`).**
`_km_stream_drain` keys its auto-thread-parent cache on Claude's `session_id`. Task-tool spawns and `/clone` commands each get a fresh `session_id`, so each subagent creates its own top-level "turn started" message. UAT confirmed 3 distinct top-level parents for one operator activity. `KM_OPERATOR_TURN_ID` does not exist in the codebase; no detect-and-reuse logic is present.

**Gap 4 — Audience-containment warning absent on default EC2 create path (`create.go:1785-2067`).**
`printTranscriptWarning` is called at `create.go:509` inside `runCreate` (the local create path). `runCreateRemote` (the `--remote` default for all EC2 substrates) has no Slack block — it dispatches artifacts to EventBridge without resolving the channel locally, so the warning helper is never called. The 3 unit tests for the helper all PASS; the dispatch-path wiring gap was missed because unit tests exercise the helper directly, not through the command dispatch.

**Recommended Phase 68.1 bundle:** Fix Gap 2 first (channel-type detection + dual-path upload resolves the most operator-visible failure), then Gap 1 (post-process transcript from km agent run wrapper), then Gap 4 (wire warning into runCreateRemote), then Gap 3 (KM_OPERATOR_TURN_ID injection).

---

_Verified: 2026-05-10T14:55:00Z_
_Verifier: Claude (gsd-verifier)_
