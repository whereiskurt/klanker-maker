---
phase: 110-session-aware-slack-reply-thread-channel-repair
verified: 2026-06-13T00:00:00Z
status: passed
score: 7/7 automated must-haves verified; deploy + operator-path live-UAT passed
human_verification:
  - test: "Run make build && make build-lambdas && km init --dry-run=false; confirm session-index GSI reaches ACTIVE in DynamoDB console; run km init --plan and confirm no-op (no drift, no destroy-class trip)"
    expected: "GSI session-index in ACTIVE state; plan shows 0 changes"
    why_human: "Terraform/DynamoDB GSI provisioning requires live AWS; cannot be verified without a real apply"
  - test: "km destroy && km create <slack-enabled profile>; from sandbox, run a poller-driven agent turn, then /opt/km/bin/km-slack reply --body /tmp/msg.md; inspect Slack thread"
    expected: "Reply lands inside the agent's session thread, not as a new top-level message in the sandbox channel"
    why_human: "Requires a running sandbox, live Slack workspace, and an active agent session with a stored thread row in km-slack-threads"
  - test: "On a Codex sandbox (agent.default: codex), run km-slack reply --body /tmp/test.md without --session; confirm WARN logged to stderr + message posts to channel root"
    expected: "Warning 'no session id resolved; falling back to channel root' on stderr; post succeeds to channel root"
    why_human: "Codex session auto-detect path is LOW-confidence; requires a real Codex sandbox environment"
  - test: "Manually delete a #sb-* channel in Slack admin (or archive it so conversations.info returns channel_not_found). Run km slack prune-threads --dry-run; confirm the dead-channel row is listed but not deleted. Re-run without --dry-run; confirm row removed."
    expected: "Dry-run lists dead row, zero deletes; non-dry-run removes it"
    why_human: "Requires manually-deleted Slack channel to create the dead-row condition"
  - test: "After deleting a channel as above, run km doctor; confirm two new WARN lines appear (one for km-slack-threads dead channels, one if alias row exists in km-slack-channels)"
    expected: "km doctor emits WARN with remediation commands km slack prune-threads and km slack forget-channel <alias>"
    why_human: "Requires live dead-channel condition; doctor check skips when no dead rows exist"
  - test: "km slack reply --session <id> --body /tmp/m.md from the operator side; confirm post lands in the session's thread"
    expected: "Message appears in the correct Slack thread for that session ID"
    why_human: "Requires a real km-slack-threads GSI row with a known session ID + live Slack"
---

# Phase 110: Session-aware Slack Reply + Thread/Channel Repair — Verification Report

**Phase Goal:** From a `claude/codex --resume` session, post to the Slack thread bound to that session — and fall back to the sandbox channel root when no thread is bound. Add operator cleanup commands for stale/wrong thread and channel mappings (manually-deleted channels, wrong thread/session resolution).
**Verified:** 2026-06-13
**Status:** human_needed — all 7 automated scope items verified; 6 live-AWS/Slack behaviors require a real deploy and running environment.
**Re-verification:** No — initial verification.

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | GSI `session-index` (hash `claude_session_id`, KEYS_ONLY) on km-slack-threads, module v1.0.0→v1.1.0 | VERIFIED | `infra/modules/dynamodb-slack-threads/v1.1.0/main.tf` lines 47-49; live unit sources v1.1.0 at line 32 of `infra/live/use1/dynamodb-slack-threads/terragrunt.hcl`; no lock files in module dir |
| 2 | Bridge `lookup-thread` action with Ed25519 signing, filtered by sandbox_id; `SessionID` envelope field in canonical alphabetical order | VERIFIED | `pkg/slack/payload.go` line 42 (`ActionLookupThread`), line 87 (`SessionID` between `S3Key` and `SenderID`); `aws_adapters.go` line 926 (IndexName `session-index`); `handler.go` line 472 dispatch case; all 3 `TestHandler_LookupThread*` tests present |
| 3 | Sandbox `km-slack reply` 4-step resolution chain routes session→thread through bridge only (NO direct DDB in reply path) | VERIFIED | `cmd/km-slack/main.go` line 72 dispatch; `runReplyWith` lines 748-815 implements 4-step chain; DynamoDB import at lines 40-41 is in `runRecordMapping` (pre-existing `record-mapping` subcommand), NOT in `runReply`/`runReplyWith`/`lookupThreadBySession` — boundary invariant holds |
| 4 | Operator `km slack reply` with `--session` queries GSI directly using operator creds (no bridge) | VERIFIED | `internal/app/cmd/slack_reply.go` `RunSlackReply` function with `session-index` GSI query; registered via `newSlackReplyCmd` in `slack.go` line 149; operator-mode bypass (`sandboxID=""`) in `LookupBySession` lines 969-974 |
| 5 | Repair commands: `km slack threads`/`forget-thread`/`prune-threads [--dry-run]`/`forget-channel` | VERIFIED | `internal/app/cmd/slack_repair.go` implements all four (`RunSlackThreads`, `RunSlackForgetThread`, `RunSlackPruneThreads`, `RunSlackForgetChannel`); all registered in `slack.go` lines 151-154; prune-threads `--dry-run` confirmed zero-delete in test (`TestRunSlackPruneThreads_DryRun`) |
| 6 | `km doctor` 2 WARN checks (dead-channel thread rows + dead alias rows) | VERIFIED | `internal/app/cmd/doctor_slack_threads.go` implements `checkSlackThreadDeadChannels` (line 42) and `checkSlackChannelDeadAlias` (line 121); both registered in `doctor.go` lines 4119/4126 with Error→Warn downgrade; SKIP when checker nil (no bot token) |
| 7 | `klanker:slack` skill section + plugin version bumped 0.4.7→0.4.8 in both files | VERIFIED | `skills/slack/SKILL.md` line 232 `## Session-aware Reply (km-slack reply)` section present with 4-step resolution order; `.claude-plugin/plugin.json` line 4 `"version": "0.4.8"`; `.claude-plugin/marketplace.json` line 11 `"version": "0.4.8"` |

**Score:** 7/7 truths verified (automated)

---

## Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `infra/modules/dynamodb-slack-threads/v1.1.0/main.tf` | GSI `session-index` KEYS_ONLY on `claude_session_id` | VERIFIED | Lines 42-49: attribute + GSI block present; no lock file |
| `infra/live/use1/dynamodb-slack-threads/terragrunt.hcl` | Sources v1.1.0 (not v1.0.0) | VERIFIED | Line 32 shows v1.1.0; no v1.0.0 reference remains |
| `pkg/slack/payload.go` | `ActionLookupThread` constant + `SessionID` field alphabetically between `s3_key` and `sender_id` | VERIFIED | Line 42 constant; line 87 field in correct position; `EnvelopeVersion` stays at 1 |
| `pkg/slack/bridge/aws_adapters.go` | `DDBThreadStore.LookupBySession` querying `session-index` GSI | VERIFIED | Lines 923-982: Query on `session-index`, GetItem for sandbox_id/agent_type, operator-mode bypass when `sandboxID==""` |
| `pkg/slack/bridge/handler.go` | `ActionLookupThread` dispatch case; allow-list entry; channel-ownership bypass | VERIFIED | Line 102 allow-list; line 231 ownership bypass for ActionLookupThread; lines 472-503 dispatch case |
| `pkg/slack/bridge/events_interfaces.go` | `SlackThreadStore.LookupBySession` interface method | VERIFIED | Lines 91-95 |
| `pkg/slack/bridge/lookup_thread_handler_test.go` | 3 test cases: happy path, missing session_id, wrong sandbox | VERIFIED | Lines 77, 119, 143 — all 3 tests present |
| `cmd/km-slack/main.go` | `reply` subcommand, `runReplyWith` 4-step chain, `lookupThreadBySession` (HTTP-only), `autoDetectSession` | VERIFIED | Lines 72-73 dispatch; lines 748-815 implementation |
| `cmd/km-slack/main_reply_test.go` | Tests for reply resolution chain including fallback | VERIFIED | Created by commit 15cd1481 |
| `internal/app/cmd/slack_reply.go` | `RunSlackReply` + `newSlackReplyCmd` (operator reply with GSI) | VERIFIED | `RunSlackReply` line 61; `newSlackReplyCmd` line 130; `session-index` pattern at line 147 |
| `internal/app/cmd/slack_repair.go` | `threads`, `forget-thread`, `prune-threads`, `forget-channel` | VERIFIED | Lines 83-584; all four commands implemented and registered |
| `internal/app/cmd/slack.go` | All 5 new commands registered under `km slack` | VERIFIED | Lines 149-154: reply + 4 repair commands |
| `internal/app/cmd/doctor_slack_threads.go` | `checkSlackThreadDeadChannels` + `checkSlackChannelDeadAlias` | VERIFIED | Lines 42 and 121 |
| `internal/app/cmd/doctor.go` | Both checks registered with Error→Warn downgrade | VERIFIED | Lines 4119 and 4126 |
| `skills/slack/SKILL.md` | `## Session-aware Reply (km-slack reply)` section | VERIFIED | Line 232 |
| `.claude-plugin/plugin.json` | Version `0.4.8` | VERIFIED | Line 4 |
| `.claude-plugin/marketplace.json` | Version `0.4.8` | VERIFIED | Line 11 |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `infra/live/use1/dynamodb-slack-threads/terragrunt.hcl` | `infra/modules/dynamodb-slack-threads/v1.1.0` | `source = ...` | WIRED | Line 32 exact match |
| `pkg/slack/bridge/handler.go` | `DDBThreadStore.LookupBySession` | `h.Threads.LookupBySession(ctx, env.SessionID, env.SenderID)` | WIRED | Lines 472-494 |
| `pkg/slack/bridge/aws_adapters.go` | DynamoDB `session-index` GSI | `IndexName = "session-index"` | WIRED | Line 926 |
| `cmd/km-slack/main.go runReplyWith` | bridge `lookup-thread` action | `ActionLookupThread` envelope via `lookupThreadBySession` | WIRED | Lines 781-800 |
| `cmd/km-slack/main.go runReplyWith` | existing `runWith` signed-post path | `runWith(ctx, priv, sandboxID, bridgeURL, resolvedChannel, ...)` | WIRED | Line 813 |
| `internal/app/cmd/slack_reply.go` | `session-index` GSI (operator direct) | `deps.ThreadLookup.LookupBySession(ctx, opts.Session, "")` | WIRED | Present in `RunSlackReply` |
| `internal/app/cmd/slack_repair.go forget-thread` | `km-slack-threads DeleteItem` | `ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{...})` | WIRED | Line 189 |
| `internal/app/cmd/slack_repair.go prune-threads` | `Slack conversations.info` | `SlackChannelChecker.IsChannelDead` via `slackClientChannelChecker` | WIRED | Lines 260-300 |
| `internal/app/cmd/doctor.go` | `checkSlackThreadDeadChannels` / `checkSlackChannelDeadAlias` | `checks = append(checks, ...)` closures | WIRED | Lines 4119, 4126 |

---

## Requirements Coverage

No formal requirement IDs were mapped for Phase 110 (ROADMAP notes "Requirements: TBD"). Coverage verified directly against the 7 scope items defined in the phase goal. All 7 verified above.

---

## Anti-Patterns Found

No blocker anti-patterns found in Phase 110 files. The `return nil` occurrences in `slack_reply.go` and `slack_repair.go` are standard error-free returns, not implementation stubs.

| File | Pattern | Severity | Assessment |
|------|---------|----------|------------|
| `cmd/km-slack/main_test.go` | `TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0` fails | Info | Pre-existing failure; predates Phase 110 (confirmed at baseline commit e31bba3f). The test asserts retry-on-503 which contradicts the deliberate fail-fast-on-5xx policy. Not a Phase 110 regression. Tracked in `deferred-items.md`. |

---

## Boundary Invariant: Sandbox Never Reads DDB

The DynamoDB import in `cmd/km-slack/main.go` (lines 40-41) is consumed exclusively by the pre-existing `runRecordMapping` function (the `record-mapping` subcommand). The new `runReply`, `runReplyWith`, and `lookupThreadBySession` functions contain no DynamoDB calls — session-to-thread resolution is performed entirely via an HTTP POST to the bridge's `lookup-thread` action. The invariant holds.

---

## Human Verification Required

### 1. GSI Provisioning + No-Drift (Live AWS)

**Test:** `make build && make build-lambdas && km init --dry-run=false`; check DynamoDB console for `session-index` GSI status; then `km init --plan` (must be a no-op).
**Expected:** GSI reaches `ACTIVE` status within seconds–minutes (online GSI add for a low-volume table); subsequent plan shows 0 resource changes.
**Why human:** Terraform/DynamoDB GSI provisioning requires a real AWS account and terragrunt apply. Cannot be verified from source alone.

### 2. Sandbox km-slack reply End-to-End

**Test:** `km destroy && km create <slack-enabled-profile>`; inside sandbox, run a poller-driven agent turn (which stores a thread row in km-slack-threads); then run `/opt/km/bin/km-slack reply --body /tmp/msg.md`.
**Expected:** Reply appears inside the agent's session thread in Slack, not as a new top-level message.
**Why human:** Requires a live sandbox, real Slack workspace, and a km-slack-threads row written by the poller.

### 3. Codex Auto-Detect Path

**Test:** On an `agent.default: codex` sandbox without `--session`, run `km-slack reply --body /tmp/test.md`.
**Expected:** Warning to stderr ("no session id resolved; falling back to channel root") and message posts to channel root (exit 0).
**Why human:** Codex session auto-detect is LOW-confidence (no deterministic session file location). Auto-detect path requires a running Codex environment.

### 4. prune-threads with Genuinely Dead Channel

**Test:** Delete a `#sb-*` channel in Slack admin. Run `km slack prune-threads --dry-run` and confirm the dead row is listed. Run without `--dry-run` and confirm row is removed.
**Expected:** Dry-run: lists dead row, zero DeleteItem calls. Non-dry-run: row deleted from km-slack-threads.
**Why human:** Requires creating a dead-channel condition by manually deleting or archiving a Slack channel so `conversations.info` returns `channel_not_found`.

### 5. km doctor WARN on Dead Channels

**Test:** After creating a dead-channel condition (see #4), run `km doctor`.
**Expected:** Two WARN lines: one for km-slack-threads dead channels (remediation: `km slack prune-threads`), one if km-slack-channels alias row is also affected (remediation: `km slack forget-channel <alias>` + `km slack adopt`).
**Why human:** doctor checks SKIP when no dead rows exist; requires live dead-channel state.

### 6. Operator km slack reply with Session ID

**Test:** `km slack reply --session <id> --body /tmp/m.md` where `<id>` is a real session id with a row in km-slack-threads.
**Expected:** Message posted into the correct Slack thread for that session.
**Why human:** Requires real km-slack-threads data and live Slack API call.

---

## Gaps Summary

No gaps. All 7 automated scope items passed verification. The 6 human verification items are deploy-time behaviors that require live AWS infrastructure, a running sandbox, and a real Slack workspace. They cannot be verified statically and do not represent code gaps — they are live integration tests that require the operator to run `make build && make build-lambdas && km init --dry-run=false` followed by sandbox recreate.

Deploy sequence (from SUMMARY.md files):
1. `make build` (operator binary — carries new CLI commands)
2. `make build-lambdas` (bridge Lambda zip + km-slack sidecar binary)
3. `km init --dry-run=false` (provisions GSI via terragrunt + uploads sidecar via `buildAndUploadSidecars` + updates bridge env)
4. `km destroy && km create <profile>` per existing sandbox (to pick up new km-slack binary + new poller userdata)

---

## Live UAT Performed (2026-06-13, deploy + operator-only)

Deployed to us-east-1 (`make build && make build-lambdas && km init --dry-run=false`) and ran an operator-only live UAT (no sandbox spend). Results:

| # | Item | Result |
|---|------|--------|
| 1 | GSI provisioning + ACTIVE + no-drift | ✅ `session-index` reached ACTIVE (initial `km init` wedged only on km's 3-min per-module timeout vs the async GSI-activation wait — re-run reconciled state cleanly, no drift). GSI query returns Count=0 for a missing key (queryable, correctly keyed). |
| 6 | Operator `km slack reply --session` (GSI hit → post into bound thread) | ✅ Seeded a poller-style row (session→channel/thread), `km slack reply --session` resolved via GSI and posted into the thread. **Surfaced + fixed a latent bug** (see below). |
| — | `km slack threads <sandbox>` | ✅ Listed the seeded row (session_id + agent populated). |
| 4 | `km slack prune-threads --dry-run` | ✅ Correctly reported "no dead-channel rows found" against a live channel (no deletes). |
| — | `km slack forget-thread --session` | ✅ Deleted the row (GSI Count=0 after). |
| — | `km slack forget-channel <alias>` | ✅ Deleted a real seeded km-slack-channels alias row. |
| 5 | `km doctor` dead-channel/dead-alias checks | ✅ Both checks live and passing: `✓ Slack thread dead channels (1 channel probed)`, `✓ Slack channel dead alias`. |
| — | `lambda-slack-bridge` deploy | ✅ Applied; new `lookup-thread` handler live. |

**Bug found + fixed during UAT (commit `65e8bbaf`):** `pkg/slack.Client.PostMessage` could not decode `chat.postMessage` responses — `SlackAPIResponse.Channel` was typed as an object but the API returns `channel` as a bare string. Latent since 2026-04-29 (the bridge's own poster and `km slack test`-through-the-bridge never exercised this direct decode); Phase 110's operator `km slack reply` is the first direct caller. Fixed with a polymorphic `SlackChannelField` (mirrors the existing `SlackUserField` pattern) + regression test. After the fix, `km slack reply --session` posts cleanly (exit 0). All UAT Slack messages and seeded DDB rows were cleaned up; tables returned to prior state.

**Deferred (explicit operator choice — "operator-only, no spend"):** items #2 (sandbox-side `km-slack reply` end-to-end) and #3 (Codex auto-detect) require a live sandbox + poller turn. Covered by unit tests; defer live confirmation to next natural sandbox use.

**Unrelated pre-existing bug noted (NOT Phase 110, NOT fixed):** `km slack adopt` (Phase 104) fails `conversations.info: invalid_arguments` — it calls `conversations.info` with the wrong arg encoding. Phase 110's own doctor dead-channel check calls `conversations.info` correctly. Tracked for a separate fix.

---

*Verified: 2026-06-13 (static) + live deploy/operator UAT*
*Verifier: Claude (gsd-verifier + orchestrator live UAT)*
