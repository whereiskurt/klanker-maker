---
phase: 67
slug: slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-02
---

# Phase 67 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (Go 1.22+) — existing pkg/profile, pkg/compiler, pkg/slack/bridge test infrastructure |
| **Config file** | `go.mod` (project root); per-package `*_test.go` |
| **Quick run command** | `go test ./pkg/profile/... ./pkg/slack/bridge/... ./pkg/compiler/... -short` |
| **Full suite command** | `make test` (defined in Makefile — runs all unit + integration tests with race detection) |
| **Estimated runtime** | ~45 seconds quick, ~3 minutes full |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/{affected-package}/... -short`
- **After every plan wave:** Run `make test`
- **Before `/gsd:verify-work`:** Full suite must be green; manual E2E with real Slack workspace required
- **Max feedback latency:** 45 seconds (quick), 180 seconds (full)

---

## Per-Task Verification Map

This map will be filled in by the planner once PLAN.md files exist with task IDs. Anticipated coverage:

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 67-01-XX | 01 (schema) | 1 | REQ-SLACK-IN-* | unit | `go test ./pkg/profile/... -run TestNotifySlackInbound` | ⬜ W0 | ⬜ pending |
| 67-02-XX | 02 (DDB+GSI) | 1 | REQ-SLACK-IN-* | terraform | `terragrunt plan` (smoke) | ⬜ W0 | ⬜ pending |
| 67-03-XX | 03 (events handler) | 1 | REQ-SLACK-IN-* | unit | `go test ./pkg/slack/bridge/... -run TestEventsHandler` | ⬜ W0 | ⬜ pending |
| 67-04-XX | 04 (compiler poller) | 2 | REQ-SLACK-IN-* | unit | `go test ./pkg/compiler/... -run TestSlackInboundPoller` | ⬜ W0 | ⬜ pending |
| 67-05-XX | 05 (SQS/DDB adapters) | 2 | REQ-SLACK-IN-* | unit | `go test ./pkg/slack/bridge/... -run TestSQSSender TestSlackThreadStore` | ⬜ W0 | ⬜ pending |
| 67-06-XX | 06 (km create wiring) | 2 | REQ-SLACK-IN-* | unit | `go test ./internal/app/cmd/... -run TestCreateSlackInbound` | ⬜ W0 | ⬜ pending |
| 67-07-XX | 07 (km destroy drain) | 3 | REQ-SLACK-IN-* | unit | `go test ./internal/app/cmd/... -run TestDestroyDrain` | ⬜ W0 | ⬜ pending |
| 67-08-XX | 08 (status/list/doctor) | 3 | REQ-SLACK-IN-* | unit | `go test ./internal/app/cmd/... -run TestDoctorSlackInbound` | ⬜ W0 | ⬜ pending |
| 67-09-XX | 09 (km slack init scope) | 3 | REQ-SLACK-IN-* | unit | `go test ./internal/app/cmd/... -run TestSlackInitEvents` | ⬜ W0 | ⬜ pending |
| 67-10-XX | 10 (E2E + docs) | 4 | REQ-SLACK-IN-* | manual | E2E checklist (real Slack workspace) | ⬜ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

**Note:** Planner derives REQ-SLACK-IN-* requirement IDs from CONTEXT.md decisions and adds them to REQUIREMENTS.md (CONTEXT.md flagged this — roadmap shows `Requirements: TBD`).

---

## Wave 0 Requirements

Wave 0 task — runs once at the start of execution to seed the test surface so subsequent waves have something to fail against:

- [ ] `pkg/profile/validate_test.go` — extend with stubs for `notifySlackInboundEnabled` validation rules (3 new rules)
- [ ] `pkg/slack/bridge/events_handler_test.go` — new file, stubs for url_verification, signing-secret check, bot-loop filter, channel→sandbox lookup
- [ ] `pkg/compiler/userdata_phase67_test.go` — new file, stubs for poller-script + systemd-unit emission gated by `notifySlackInboundEnabled`
- [ ] `internal/app/cmd/create_slack_inbound_test.go` — new file, stubs for SQS create + rollback + env-var injection
- [ ] `internal/app/cmd/destroy_drain_test.go` — new file, stubs for drain-poll-stop sequence
- [ ] Add `github.com/aws/aws-sdk-go-v2/service/sqs` to `go.mod` via `go get` (one-time dep add — must happen in Wave 0 so all other plans can compile)

---

## Manual-Only Verifications

These behaviors cannot be fully automated — they require a real Slack workspace, real SQS, and real EC2/SSM:

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Slack signing-secret verification end-to-end | REQ-SLACK-IN-EVENTS | Real Slack signs events with the operator's secret; cannot mock the signing key holder | Configure Slack App with bridge `/events` URL, post test message, confirm bridge accepts and rejects per signing rules |
| Slack `url_verification` challenge round-trip | REQ-SLACK-IN-EVENTS | Slack sends challenge once during URL save; not exercised again | Save Events URL in Slack App config; confirm Slack reports "Verified" |
| Per-sandbox SQS queue lifecycle | REQ-SLACK-IN-LIFECYCLE | Real SQS API call timing + IAM at create/destroy | `km create` profile with `notifySlackInboundEnabled: true`; `aws sqs list-queues` → confirm queue; `km destroy` → confirm queue deleted |
| Multi-turn `--resume` continuity | REQ-SLACK-IN-DELIVERY | Claude session-id persistence in `~/.claude/projects/<cwd>/` is sandbox-side state requiring real EC2 | Reply twice in same Slack thread, confirm Claude references first turn's content in second reply |
| Top-level Slack post starts new thread | REQ-SLACK-IN-DELIVERY | Slack thread_ts assignment is server-side | Post top-level msg → poller invokes without `--resume` → reply lands as thread reply on user's message |
| Paused-sandbox queue-and-wait | REQ-SLACK-IN-PAUSE | Requires real EC2 hibernate + SQS retention behavior | `km pause` sandbox; post Slack msg; `km resume`; confirm message processed within 60s |
| `km destroy` drain best-effort 30s timeout | REQ-SLACK-IN-LIFECYCLE | Requires real in-flight `km agent run` + tmux session | Start long agent turn via Slack; immediately `km destroy`; verify drain wait + clean shutdown |
| Bot-loop prevention | REQ-SLACK-IN-EVENTS | Slack bot user_id is workspace-scoped; mock can't fully cover edge cases (subtype variations) | Confirm Claude's outbound replies do NOT trigger inbound flow (no doubled responses) |
| Slack Connect invite gate | REQ-SLACK-IN-EVENTS | Workspace-level membership check | Post from a user NOT invited to the channel → bridge ignores |
| Real `km doctor` slack_inbound_* checks | REQ-SLACK-IN-DOCTOR | Requires real SQS + DDB + Slack API for auth.test scopes | Run `km doctor --all-regions`; confirm three new checks (queue exists, stale queues, Events scopes) pass green for healthy state and red for synthetic failures |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies (filled by planner)
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (sqs SDK dep, 5 new test files)
- [ ] No watch-mode flags
- [ ] Feedback latency < 45s (quick) / 180s (full)
- [ ] `nyquist_compliant: true` set in frontmatter once planner fills the per-task map
- [ ] Manual E2E checklist executed against real klankermaker.ai workspace before phase verification

**Approval:** pending — set to `approved YYYY-MM-DD` once planner completes per-task map and Wave 0 task is concrete
