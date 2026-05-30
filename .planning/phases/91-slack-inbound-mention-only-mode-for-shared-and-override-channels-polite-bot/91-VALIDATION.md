---
phase: 91
slug: slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-05-30
---

# Phase 91 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib `testing` package) |
| **Config file** | none — `go test ./...` convention |
| **Quick run command** | `go test ./pkg/slack/bridge/... ./pkg/compiler/... ./pkg/profile/... ./internal/app/cmd/... -run TestMention -v` |
| **Full suite command** | `make test` (or `go test ./...`) |
| **Estimated runtime** | ~25 seconds (quick) / ~90 seconds (full) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/slack/bridge/... ./pkg/compiler/... ./pkg/profile/... ./internal/app/cmd/... -run TestMention -v`
- **After every plan wave:** Run `go test ./pkg/... ./internal/... ./cmd/...`
- **Before `/gsd:verify-work`:** Full suite must be green (`make test`)
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 91-01-01 | 01 | 1 | POL-01 | unit | `go test ./pkg/profile/... -run TestCLISpec_NotifySlackInboundMentionOnly` | ❌ W0 | ⬜ pending |
| 91-01-02 | 01 | 1 | POL-02 | unit | `go test ./pkg/profile/... -run TestSchema_NotifySlackInboundMentionOnly` | ❌ W0 | ⬜ pending |
| 91-01-03 | 01 | 1 | POL-03 | unit | `go test ./pkg/profile/... -run TestValidateSemantic` | ✅ | ⬜ pending |
| 91-02-01 | 02 | 2 | POL-04 | unit table-driven (9 cases) | `go test ./pkg/compiler/... -run TestMentionOnlyCompiler` | ❌ W0 | ⬜ pending |
| 91-02-02 | 02 | 2 | POL-11 | unit table-driven (9 cases) | `go test ./pkg/compiler/... -run TestResolveMentionOnly` | ❌ W0 | ⬜ pending |
| 91-03-01 | 03 | 2 | POL-05 | manual (terragrunt plan) | `terragrunt plan --terragrunt-working-dir infra/live/use1/lambda-slack-bridge` | N/A | ⬜ pending |
| 91-04-01 | 04 | 3 | POL-06 | unit table-driven (7 cases) | `go test ./pkg/slack/bridge/... -run TestEventsHandler_MentionOnly` | ❌ W0 | ⬜ pending |
| 91-04-02 | 04 | 3 | POL-12 | unit | `go test ./pkg/slack/bridge/... -run TestEventsHandler_MentionOnly` | ❌ W0 | ⬜ pending |
| 91-04-03 | 04 | 3 | POL-09 | unit | `go test ./cmd/km-slack-bridge/... -run TestWireEventsHandler_BotUserIDPrime` | ❌ W0 | ⬜ pending |
| 91-05-01 | 05 | 3 | POL-07 | unit (fake SSM) | `go test ./internal/app/cmd/... -run TestRunSlackInit_BotUserIDCached` | ❌ W0 | ⬜ pending |
| 91-05-02 | 05 | 3 | POL-08 | unit (fake SSM) | `go test ./internal/app/cmd/... -run TestRotateToken_BotUserIDCached` | ❌ W0 | ⬜ pending |
| 91-06-01 | 06 | 4 | POL-10 | unit (fake SSM) | `go test ./internal/app/cmd/... -run TestCheckSlackBotUserIDCached` | ❌ W0 | ⬜ pending |
| 91-07-01 | 07 | 5 | POL-13 | manual review | — | N/A | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*
*Plan/wave numbers are recommendations; planner finalises in 91-XX-PLAN.md frontmatter.*

---

## Wave 0 Requirements

- [ ] `pkg/profile/profile_cli_mention_test.go` (or extend existing `*_test.go`) — stubs for POL-01, POL-02
- [ ] `pkg/compiler/userdata_mention_test.go` — new file; stubs for POL-04, POL-11 (`TestResolveMentionOnly`, `TestMentionOnlyCompiler`)
- [ ] `pkg/slack/bridge/events_handler_test.go` — extend existing file with `TestEventsHandler_MentionOnly` stub for POL-06, POL-12
- [ ] `cmd/km-slack-bridge/main_test.go` (or extend) — `TestWireEventsHandler_BotUserIDPrime` stub for POL-09
- [ ] `internal/app/cmd/slack_test.go` — extend for `TestRunSlackInit_BotUserIDCached` (POL-07) and `TestRotateToken_BotUserIDCached` (POL-08)
- [ ] `internal/app/cmd/doctor_slack_bot_user_id_test.go` (sibling of `doctor_slack_users_email_test.go`) — stub for POL-10
- [ ] No new framework install needed — Go stdlib `testing` already in use

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Terragrunt env-block wiring for `KM_SLACK_MENTION_ONLY` and `KM_SLACK_BOT_USER_ID` | POL-05 | Requires real AWS plan; no terraform unit-test framework in repo | `terragrunt plan --terragrunt-working-dir infra/live/use1/lambda-slack-bridge` — confirm both env vars appear in the bridge Lambda's environment block |
| `km init --sidecars` flows the schema change through | POL-01..05 | Requires real Lambda redeploy | Run after schema change; confirm `km validate` passes on a profile using the new field on a freshly-redeployed install |
| Polite-bot in a real shared channel | POL-06, POL-12 | Requires real Slack workspace + posting messages | Create `profiles/hermes.open.yaml`-style sandbox with `notifySlackChannelOverride: "#km-test"`, post non-mention then mention message; confirm bot only reacts/dispatches on mention |
| `km doctor` WARN fires correctly | POL-10 | Requires a real install state with mention-only profile + missing SSM param | Delete `{prefix}slack/bot-user-id` from SSM, set a profile with mention-only, run `km doctor`, confirm WARN |
| `docs/slack-notifications.md` Phase 91 § reads well | POL-13 | Prose quality | Operator-style readthrough |
| `CLAUDE.md` Phase 91 § locked-decisions block accurate | POL-13 | Prose quality | Author readthrough |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (events_handler_test extension, userdata_mention_test, profile_cli_mention_test, slack_test extension, doctor_slack_bot_user_id_test)
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter (after planner confirms task IDs map cleanly)

**Approval:** pending
