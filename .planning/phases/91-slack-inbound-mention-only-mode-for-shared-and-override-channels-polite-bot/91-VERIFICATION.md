---
phase: 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot
verified: 2026-05-30T23:14:16Z
status: human_needed
score: 12/13 must-haves verified
human_verification:
  - test: "Run terragrunt plan against infra/live/use1/lambda-slack-bridge to confirm KM_SLACK_MENTION_ONLY and KM_SLACK_BOT_USER_ID appear in the Lambda environment block (POL-05)"
    expected: "aws_lambda_function.slack_bridge shows an in-place update with both new env keys; destroy-class gate stays green; exit code 0"
    why_human: "No terraform unit-test framework in repo; requires real AWS credentials. Per 91-VALIDATION.md 'Manual-Only Verifications' table for POL-05."
  - test: "Live Slack UAT: deploy polite-bot bridge and verify scenarios 7-10 from 91-06 Plan Task 4"
    expected: "Non-mention message in shared channel → no 👀 reaction, no dispatch. @-mention message in shared channel → 👀 reaction + dispatch. Per-sandbox channel (#sb-{id}) → every-message dispatch preserved. Profile with notifySlackInboundMentionOnly:true in per-sandbox channel → mention-only behaviour."
    why_human: "Requires real Slack workspace + AWS credentials + deployed bridge Lambda. Operator explicitly chose not to generate Slack Connect invite requests during testing (per objective notes)."
notes:
  - "CLAUDE.md km slack rotate-token CLI bullet does not mention bot_user_id caching (Phase 91) — this was called out in plan 91-06 task 2 action step 2 but is absent from the must_haves.truths section and from the final SUMMARY. Not a blocker; the km slack init bullet does have the note."
  - "Phase 72 validator rejects notifySlackInboundEnabled:true + notifySlackChannelOverride together — Mode 3 default-polite semantics are only observable once that constraint is loosened in a future phase. Mode 2 opt-in polite is the immediately useful path. Tracked as a planning observation, not a gap."
---

# Phase 91: Slack Inbound @-mention-only Mode Verification Report

**Phase Goal:** Stop the km-slack bridge from forwarding every message in subscribed channels — only react when the message text contains `<@{bot_user_id}>`. Smart per-channel-mode defaults (Mode 1/3 → mention-only, Mode 2 → every-message). New `cli.notifySlackInboundMentionOnly *bool` profile field. Bridge handler detects mention via substring scan; bot_user_id cached in SSM. Compiler emits `KM_SLACK_MENTION_ONLY`. `km doctor` sanity-checks bot_user_id cache.

**Verified:** 2026-05-30T23:14:16Z
**Status:** human_needed — 12/13 must-haves verified; 2 items deferred to operator (terragrunt plan + live Slack UAT, both pre-declared as manual-only in 91-VALIDATION.md)
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `CLISpec.NotifySlackInboundMentionOnly *bool` field exists with tri-state semantics | VERIFIED | `pkg/profile/types.go` line 551; yaml+json tags; full godoc comment |
| 2 | JSON Schema accepts boolean, rejects non-boolean, treats field as optional | VERIFIED | `pkg/profile/schemas/sandbox_profile.schema.json` line 608; `TestSchema_NotifySlackInboundMentionOnly` GREEN |
| 3 | `km validate` accepts the field with no semantic rules | VERIFIED | `TestCLISpec_NotifySlackInboundMentionOnly` all 4 subtests GREEN; `validate.go` untouched |
| 4 | `resolveMentionOnly` returns correct bool for all 9 (mode × override) combinations | VERIFIED | `pkg/compiler/userdata.go` line 3768; `TestResolveMentionOnly` 10 cases GREEN including nil-CLI edge |
| 5 | Compiler emits `KM_SLACK_MENTION_ONLY` into `notifyEnv` when Slack enabled; absent when disabled | VERIFIED | `pkg/compiler/userdata.go` lines 3994-4004; `TestMentionOnlyCompiler` 5 cases GREEN |
| 6 | Bridge `EventsHandler` has `MentionOnly bool` field + step-4b mention-scan guard (before dedup) | VERIFIED | `pkg/slack/bridge/events_handler.go` line 59 (field), line 174 (guard); `TestEventsHandler_MentionOnly` 7 cases GREEN |
| 7 | Fail-open on BotUserID fetch error; non-mention → no nonce consumed, no SQS write, no reaction | VERIFIED | `events_handler_test.go` cases 3 + 7; nonce store call count assertions pass |
| 8 | `wireEventsHandler` reads `KM_SLACK_MENTION_ONLY` + `KM_SLACK_BOT_USER_ID`; `PrimeCache` exists | VERIFIED | `cmd/km-slack-bridge/main.go` `WireMentionOnly` at line 319; `CachedBotUserIDFetcher.PrimeCache` at line 1075 of `aws_adapters.go`; `TestWireEventsHandler_BotUserIDPrime` 5 cases GREEN |
| 9 | Lambda Terraform module declares `slack_mention_only` + `slack_bot_user_id` variables and wires them into env block | VERIFIED | `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` lines 106/116; `main.tf` lines 326-327; `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` lines 97/103; `terraform fmt -check` clean |
| 10 | Terragrunt plan confirms both env vars in Lambda environment block | HUMAN NEEDED | Manual-only per 91-VALIDATION.md (POL-05); no AWS creds in this session |
| 11 | `km slack init` and `km slack rotate-token` call `AuthTestWithUserID` + write `{prefix}slack/bot-user-id` to SSM (non-fatal on Put failure) | VERIFIED | `internal/app/cmd/slack.go` lines 218/241 (init), 677/694 (rotate-token); `TestRunSlackInit_BotUserIDCached` + `TestRotateToken_BotUserIDCached` + both `_PutErrorIsNonFatal` variants GREEN |
| 12 | `checkSlackBotUserIDCached` exists with 4 return paths (SKIPPED/OK/WARN-empty/WARN-error); registered in `doctor.go` conditionally | VERIFIED | `internal/app/cmd/doctor_slack_transcript.go` line 393; `doctor.go` line 2985/2993; `TestCheckSlackBotUserIDCached` 4 cases GREEN; `TestAnyProfileMentionOnly` GREEN |
| 13 | Docs updated: `docs/slack-notifications.md` Phase 91 section + `CLAUDE.md` + `OPERATOR-GUIDE.md` | VERIFIED | See artifact checks below; live Slack UAT pending operator |

**Score:** 12/13 truths verified (1 requires human)

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/profile/types.go` | `CLISpec.NotifySlackInboundMentionOnly *bool` with godoc | VERIFIED | Line 551; both yaml+json tags; godoc covers nil/&true/&false semantics |
| `pkg/profile/schemas/sandbox_profile.schema.json` | `notifySlackInboundMentionOnly` property, type boolean, optional | VERIFIED | Line 608; no `required` entry; no `default` |
| `pkg/profile/profile_cli_mention_test.go` | Live tests for POL-01/02/03 | VERIFIED | 4 subtests in `TestCLISpec_NotifySlackInboundMentionOnly` + 4 in `TestSchema_NotifySlackInboundMentionOnly` + validate semantic; no `t.Skip` |
| `pkg/compiler/userdata.go` | `resolveMentionOnly` helper + `KM_SLACK_MENTION_ONLY` emission | VERIFIED | Lines 3758-3779 (helper), 3994-4004 (emission gated on `NotifySlackEnabled==&true`) |
| `pkg/compiler/userdata_mention_test.go` | Live `TestResolveMentionOnly` (9 cases) + `TestMentionOnlyCompiler` | VERIFIED | 10 cases (9 + nil-CLI edge) + 5 compiler cases; all GREEN; no `t.Skip` |
| `pkg/slack/bridge/events_handler.go` | `EventsHandler.MentionOnly bool` field + step-4b guard | VERIFIED | Field at line 59; guard at lines 174-186; positioned between isBotLoop and dedup |
| `pkg/slack/bridge/aws_adapters.go` | `CachedBotUserIDFetcher.PrimeCache(uid string)` | VERIFIED | Lines 1070-1090; empty-string no-op; 24h TTL; mutex-safe |
| `cmd/km-slack-bridge/main.go` | `WireMentionOnly` reads `KM_SLACK_MENTION_ONLY` + `KM_SLACK_BOT_USER_ID` | VERIFIED | Line 319; exported helper; called from `wireEventsHandler` at line 262 |
| `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` | `slack_mention_only` + `slack_bot_user_id` Terraform variables | VERIFIED | Lines 106/116; default "false"/"" for back-compat |
| `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` | `KM_SLACK_MENTION_ONLY` + `KM_SLACK_BOT_USER_ID` in Lambda env block | VERIFIED | Lines 326-327 |
| `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` | `slack_mention_only` + `slack_bot_user_id` via `get_env` | VERIFIED | Lines 97/103 |
| `pkg/slack/client.go` | `Client.AuthTestWithUserID(ctx) (string, error)` | VERIFIED | Line 191; parses `user_id` from auth.test response; `TestAuthTestWithUserID_OK/NotOK/TransportError` all GREEN |
| `internal/app/cmd/slack.go` | `SlackInitAPI.AuthTestWithUserID` interface extension; SSM writes in RunSlackInit/RunSlackRotateToken | VERIFIED | Line 51 (interface); lines 218+241 (init); lines 677+694 (rotate-token); warn-not-fatal on Put error |
| `internal/app/cmd/doctor_slack_transcript.go` | `checkSlackBotUserIDCached` with closure injection | VERIFIED | Line 393; mirrors `checkSlackUsersReadEmailScope` shape exactly |
| `internal/app/cmd/doctor.go` | Registration of `checkSlackBotUserIDCached`; `anyProfileMentionOnly` helper | VERIFIED | Line 2985 (conditional construction); line 2993 (registration); line 3774 (`anyProfileMentionOnly`) |
| `docs/slack-notifications.md` | Phase 91 section (≥60 lines) after Phase 72 section | VERIFIED | 154 lines starting at line 1607; 8 required subsections present; 20 key-term hits |
| `CLAUDE.md` | Where-to-look row + km slack init CLI note + Phase 91 architecture paragraph | VERIFIED | Line 20 (where-to-look); line 52 (init note); lines 292-321 (architecture §) |
| `OPERATOR-GUIDE.md` | `### Mention-only mode (polite-bot)` subsection | VERIFIED | Line 1181; cross-ref to `docs/slack-notifications.md § Phase 91` at line 1199 |
| `profiles/learn.v2.polite.yaml` | New profile with `notifySlackInboundMentionOnly: true` | VERIFIED | Line 188; passes `km validate` per operator testing |
| `profiles/learn.v2.chatty.yaml` | New profile with `notifySlackInboundMentionOnly: false` | VERIFIED | Line 185; passes `km validate` per operator testing |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `pkg/profile/profile_cli_mention_test.go` | `pkg/profile/types.go` | `NotifySlackInboundMentionOnly` round-trip | WIRED | 4 subtests parse YAML/JSON and assert nil/&true/&false |
| `pkg/compiler/userdata_mention_test.go` | `pkg/compiler/userdata.go` | calls `resolveMentionOnly()` | WIRED | Test calls the function directly; 10-case table-driven |
| `pkg/compiler/userdata.go` | `pkg/profile/types.go` | `resolveMentionOnly` reads `NotifySlackInboundMentionOnly`, `NotifySlackPerSandbox`, `NotifySlackChannelOverride` | WIRED | Lines 3768-3779 read all three fields |
| `pkg/slack/bridge/events_handler.go Handle()` | `pkg/slack/bridge/aws_adapters.go CachedBotUserIDFetcher.Fetch()` | `h.BotUserID.Fetch(ctx)` in step-4b guard | WIRED | Line 179 in events_handler.go |
| `cmd/km-slack-bridge/main.go wireEventsHandler` | `os.Getenv("KM_SLACK_MENTION_ONLY")` + `os.Getenv("KM_SLACK_BOT_USER_ID")` | `WireMentionOnly` reads env + calls `fetcher.PrimeCache` | WIRED | Lines 320-323 |
| `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` | `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` | `var.slack_mention_only/slack_bot_user_id` from `get_env` | WIRED | HCL verified; terraform fmt clean |
| `internal/app/cmd/doctor.go` | `internal/app/cmd/doctor_slack_transcript.go checkSlackBotUserIDCached` | conditional `getUID` closure construction + call | WIRED | Lines 2985-2993 |
| `CLAUDE.md Where-to-look` | `docs/slack-notifications.md § Phase 91` | cross-ref in table | WIRED | Line 20 in CLAUDE.md |
| `OPERATOR-GUIDE.md mention-only subsection` | `docs/slack-notifications.md § Phase 91` | markdown link | WIRED | Line 1199 in OPERATOR-GUIDE.md |

---

### Requirements Coverage

All 13 Phase 91 requirement IDs are synthetic phase-local (POL-XX) per the Phase 84.2/84.3/89 convention.

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| POL-01 | 91-01 | `CLISpec.NotifySlackInboundMentionOnly *bool` field (yaml+json tags, tri-state) | SATISFIED | `types.go` line 551; `TestCLISpec_NotifySlackInboundMentionOnly` GREEN |
| POL-02 | 91-01 | JSON Schema accepts boolean, rejects non-boolean, optional | SATISFIED | `schema.json` line 608; `TestSchema_NotifySlackInboundMentionOnly` GREEN |
| POL-03 | 91-01 | `km validate` accepts the new field with no semantic rules | SATISFIED | `validate.go` untouched; validate semantic test GREEN |
| POL-04 | 91-02 | `KM_SLACK_MENTION_ONLY` emitted into `notifyEnv` for each (mode × override) when Slack enabled | SATISFIED | `userdata.go` lines 3994-4004; `TestMentionOnlyCompiler` GREEN |
| POL-05 | 91-03 | Terragrunt plan confirms both new env vars in Lambda environment block | HUMAN NEEDED | Manual verification required (no AWS creds in this session) |
| POL-06 | 91-03 | 7-case `TestEventsHandler_MentionOnly` GREEN; mention-scan guard between steps 4 and 5 | SATISFIED | All 7 subcases GREEN; positioned before `CheckAndStore` |
| POL-07 | 91-04 | `km slack init` calls `AuthTestWithUserID` + writes `{prefix}slack/bot-user-id`; non-fatal on Put error | SATISFIED | `slack.go` line 218/241; `TestRunSlackInit_BotUserIDCached` + `_PutErrorIsNonFatal` GREEN |
| POL-08 | 91-04 | `km slack rotate-token` re-caches bot_user_id after token rotation; non-fatal on Put error | SATISFIED | `slack.go` line 677/694; `TestRotateToken_BotUserIDCached` + `_PutErrorIsNonFatal` GREEN |
| POL-09 | 91-03 | `CachedBotUserIDFetcher.PrimeCache` exists; `wireEventsHandler` seeds it from `KM_SLACK_BOT_USER_ID` | SATISFIED | `aws_adapters.go` line 1075; `main.go` `WireMentionOnly`; `TestWireEventsHandler_BotUserIDPrime` 5 cases GREEN |
| POL-10 | 91-05 | `checkSlackBotUserIDCached` 4 return paths; registered conditionally in doctor.go | SATISFIED | `doctor_slack_transcript.go` line 393; `doctor.go` line 2993; `TestCheckSlackBotUserIDCached` GREEN |
| POL-11 | 91-02 | 9-case (mode × override) truth table in `TestResolveMentionOnly` + nil-CLI edge | SATISFIED | 10-case test in `userdata_mention_test.go`; all GREEN |
| POL-12 | 91-03 | Mention-scan negative/edge cases (different UID, start/end of text, fetch error fail-open) | SATISFIED | Cases 4/5/6/7 of `TestEventsHandler_MentionOnly`; all GREEN |
| POL-13 | 91-06 | All three doc files updated; Phase 91 section operator-readable; operator readthrough sign-off | SATISFIED (docs) / HUMAN NEEDED (UAT) | Docs presence + key-term coverage verified; live Slack UAT deferred per objective |

---

### Anti-Patterns Found

No blockers or stubs detected.

| File | Pattern | Severity | Notes |
|------|---------|----------|-------|
| All Phase 91 test files | No `t.Skip` remaining | OK | All 6 stub test files from Plan 00 fully flipped to live assertions |
| `internal/app/cmd/slack.go` | `return nil` on multiple paths | OK | Legitimate function returns; not placeholders |
| `cmd/km-slack-bridge/main.go` | `WireMentionOnly` exported helper | OK | Exported deliberately for testability in `main_mention_test.go` |

---

### Human Verification Required

#### 1. Terragrunt Plan Verification (POL-05)

**Test:** From repo root — `eval $(km env)` then `terragrunt plan --terragrunt-working-dir infra/live/use1/lambda-slack-bridge`

**Expected:** `aws_lambda_function.slack_bridge` shows an in-place update to `environment.0.variables` with two new keys: `KM_SLACK_MENTION_ONLY = "false"` (or `"true"` if `KM_SLACK_MENTION_ONLY=true` was exported) and `KM_SLACK_BOT_USER_ID = ""` (or the operator's UID if `KM_SLACK_BOT_USER_ID` was exported). No protected resource destroyed/replaced. Exit code 0.

**Optional validation:** Re-run with `KM_SLACK_MENTION_ONLY=true KM_SLACK_BOT_USER_ID=UBOT123` exported; confirm plan output reflects those values.

**Why human:** No terraform unit-test framework in repo; requires real AWS credentials.

#### 2. Live Slack UAT — Polite-bot Scenarios (POL-13 + regression)

**Test:** After `make build && km slack init --force && export KM_SLACK_MENTION_ONLY=true && export KM_SLACK_BOT_USER_ID=$(aws ssm get-parameter ...) && km init --sidecars && km doctor`:

- Scenario 7: Type `hello team` (no mention) in shared channel → bot does NOT 👀-react; no dispatch
- Scenario 8: Type `<@{bot_user_id}> hello` in shared channel → bot DOES 👀-react; dispatch confirmed
- Scenario 9 (regression): Type `hello` in per-sandbox `#sb-{id}` channel → bot DOES 👀-react; every-message preserved
- Scenario 10 (override test): Profile with `notifySlackInboundMentionOnly: true, notifySlackPerSandbox: true`; per-sandbox channel behaves like polite-bot

**Expected:** All four scenarios behave as documented. Operator types "approved."

**Why human:** Requires real Slack workspace + inbound messages + deployed bridge Lambda. Operator explicitly chose not to generate Slack Connect invite requests during testing session.

---

### Planning Observations (not gaps)

**Mode 3 polite-bot only exercisable after Phase 72 constraint is loosened**

The Phase 72 validator rejects `notifySlackInboundEnabled: true` combined with `notifySlackChannelOverride` (`validate.go` line 374: "notifySlackInboundEnabled: true is incompatible with notifySlackChannelOverride"). This means Phase 91's Mode 3 default-polite semantics (`notifySlackChannelOverride != ""` → mention-only default) cannot be fully exercised end-to-end until that constraint is loosened in a future phase. Mode 2 opt-in polite (`notifySlackPerSandbox: true` + `notifySlackInboundMentionOnly: true`) is the immediately useful path. The `resolveMentionOnly` logic is correct for Mode 3 and will activate correctly once the constraint is relaxed — this is a future-phase planning item, not a Phase 91 defect.

**CLAUDE.md `km slack rotate-token` bullet missing Phase 91 note**

The 91-06 plan task 2 action step 2 said the `km slack rotate-token` CLI bullet should also append a note about bot_user_id caching. The `km slack init` bullet was updated (line 52) but the rotate-token bullet was not. This was not in the must_haves.truths for POL-13 (only `km slack init` was explicit there) and was not flagged in the 91-06 SUMMARY as a deviation. The implementation is correct (`RunSlackRotateToken` does cache bot_user_id at line 694); only the CLAUDE.md CLI documentation is missing the note. A trivial one-line fix for the next maintenance pass.

---

## Gaps Summary

No blocking gaps. The phase goal is fully achieved in code. The two human-needed items are:

1. **POL-05 terragrunt plan** — Terraform module wiring is present and fmt-clean; plan confirmation requires AWS credentials that were not available in this session.
2. **POL-13 live UAT** — All implementation and docs are in place; the operator explicitly deferred live Slack workspace testing during the Phase 91 session.

Both items were pre-declared as `Manual-Only` in `91-VALIDATION.md` before any implementation began.

---

*Verified: 2026-05-30T23:14:16Z*
*Verifier: Claude (gsd-verifier)*
