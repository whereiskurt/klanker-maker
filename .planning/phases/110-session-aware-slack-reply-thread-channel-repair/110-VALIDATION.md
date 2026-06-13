---
phase: 110
slug: session-aware-slack-reply-thread-channel-repair
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-12
---

# Phase 110 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` + `testify/assert` |
| **Config file** | none — project uses `go test ./...` |
| **Quick run command** | `go test ./pkg/slack/... ./cmd/km-slack/... -count=1 -timeout 120s` |
| **Full suite command** | `go test ./... -count=1 -timeout 600s` |
| **Estimated runtime** | ~quick: 30s · full: 300–600s |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/slack/... ./cmd/km-slack/... -count=1 -timeout 120s`
- **After every plan wave:** Run `go test ./... -count=1 -timeout 600s`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** ~120 seconds (quick)

> Note: `internal/app/cmd/` full-suite is GREEN baseline as of Phase 107 — a FAIL there now signals a real regression (see memory: cmd suite pre-existing failures RESOLVED).

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Scope | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------|-----------|-------------------|-------------|--------|
| 110-01-* | 01 | 1 | 1 GSI v1.1.0 | manual/TF | `terraform plan` in module dir + `km init --plan` no-op | ❌ W0 (TF, no go test) | ⬜ pending |
| 110-02-* | 02 | 2 | 2 bridge lookup-thread dispatch | unit | `go test ./pkg/slack/bridge/ -run TestHandler_LookupThread` | ❌ W0 | ⬜ pending |
| 110-02-* | 02 | 2 | 2 missing SessionID → 400 | unit | `go test ./pkg/slack/bridge/ -run TestHandler_LookupThread_MissingSessionID` | ❌ W0 | ⬜ pending |
| 110-02-* | 02 | 2 | 2 cross-sandbox → found:false | unit | `go test ./pkg/slack/bridge/ -run TestHandler_LookupThread_WrongSandbox` | ❌ W0 | ⬜ pending |
| 110-02-* | 02 | 2 | 2 canonical-JSON determinism w/ SessionID | unit | `go test ./pkg/slack/ -run TestCanonicalJSON` | ✅ (extend) | ⬜ pending |
| 110-03-* | 03 | 3 | 3 reply resolution chain order | unit | `go test ./cmd/km-slack/ -run TestRunReply` | ❌ W0 | ⬜ pending |
| 110-03-* | 03 | 3 | 3 auto-detect newest Claude session | unit | `go test ./cmd/km-slack/ -run TestAutoDetectClaudeSession` | ❌ W0 | ⬜ pending |
| 110-03-* | 03 | 3 | 3 fallback to channel root | unit | `go test ./cmd/km-slack/ -run TestRunReply_FallbackToChannelRoot` | ❌ W0 | ⬜ pending |
| 110-04-* | 04 | 3 | 4 operator reply session→GSI→post | unit | `go test ./internal/app/cmd/ -run TestRunSlackReply` | ❌ W0 | ⬜ pending |
| 110-05-* | 05 | 3 | 5 forget-thread deletes row | unit | `go test ./internal/app/cmd/ -run TestRunSlackForgetThread` | ❌ W0 | ⬜ pending |
| 110-05-* | 05 | 3 | 5 prune-threads --dry-run lists only | unit | `go test ./internal/app/cmd/ -run TestRunSlackPruneThreads_DryRun` | ❌ W0 | ⬜ pending |
| 110-05-* | 05 | 3 | 5 forget-channel deletes alias row | unit | `go test ./internal/app/cmd/ -run TestRunSlackForgetChannel` | ❌ W0 | ⬜ pending |
| 110-06-* | 06 | 3 | 6 doctor dead-channel WARN | unit | `go test ./internal/app/cmd/ -run TestCheckSlackThreadDeadChannels` | ❌ W0 | ⬜ pending |
| 110-06-* | 06 | 3 | 6 doctor dead-alias WARN | unit | `go test ./internal/app/cmd/ -run TestCheckSlackChannelDeadAlias` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

New test files needed (scaffolded before/with implementation tasks):

- [ ] `pkg/slack/bridge/lookup_thread_handler_test.go` — bridge `lookup-thread` action: dispatch, GSI query, sandbox_id filter, missing-SessionID 400, cross-sandbox isolation
- [ ] `pkg/slack/payload_test.go` (extend existing) — canonical-JSON determinism with the new `SessionID` field inserted at the correct alphabetical position
- [ ] `cmd/km-slack/main_reply_test.go` — `reply` resolution chain ordering + Claude session auto-detect + channel-root fallback
- [ ] `internal/app/cmd/slack_repair_test.go` — `threads` / `forget-thread` / `prune-threads --dry-run` / `forget-channel`
- [ ] `internal/app/cmd/doctor_slack_threads_test.go` — both new doctor WARN checks

Framework install: already present (Go + `testify` in `go.mod`). No new framework.

---

## Manual-Only Verifications

| Behavior | Scope | Why Manual | Test Instructions |
|----------|-------|------------|-------------------|
| GSI `session-index` materializes + bridge Query works against live DDB | 1, 2 | Requires live AWS DynamoDB + GSI provisioning via terragrunt | `make build && make build-lambdas && km init --dry-run=false`; confirm GSI ACTIVE in console; `km slack threads <sandbox>` returns rows |
| Sandbox `km-slack reply` posts to bound thread end-to-end | 3 | Requires a running sandbox + live Slack workspace + active agent session | `km destroy && km create <profile>`; from sandbox run a poller-driven turn, then `km-slack reply --body /tmp/msg.md`; confirm it lands in the session's thread |
| Codex session auto-detect path | 3 | Codex session file location LOW-confidence (Phase 70 hangover); WARN-and-skip fallback | On a `agent.default: codex` sandbox, verify `--session` explicit works; document if auto-detect path resolves |
| `prune-threads` drops genuinely dead rows vs live Slack | 5 | Requires manually-deleted Slack channel to create the dead-row condition | Delete a `#sb-*` channel in Slack admin; `km slack prune-threads --dry-run` lists it; without `--dry-run` removes it |
| `km doctor` WARN fires on dead channel/alias | 6 | Same — needs a real dead channel | After deleting a channel, `km doctor` shows the two WARN lines |
| Plugin/skill version bump visible to clients | 7 | Client-side plugin cache | Bump `plugin.json` + `marketplace.json`; confirm `klanker:slack` skill shows new reply section |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies (TF/live-AWS tasks documented as manual)
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (5 new/extended test files)
- [ ] No watch-mode flags
- [ ] Feedback latency < 120s (quick)
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
