---
phase: 96
slug: slack-default-router-orphan-channel-mention-reply
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-05
---

# Phase 96 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (standard library, table-driven + httptest) |
| **Config file** | none — existing Go toolchain |
| **Quick run command** | `go test ./pkg/slack/bridge/... ./internal/app/config/...` |
| **Full suite command** | `go build ./... && go test ./...` |
| **Estimated runtime** | ~30–90 seconds |

---

## Sampling Rate

- **After every task commit:** package-scoped `go test` for the touched package(s).
- **After every plan wave:** `go build ./... && go test ./...`.
- **Before `/gsd:verify-work`:** full suite green; `make build` succeeds.
- **Max feedback latency:** ~90 seconds.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 96-01-xx | 01 | 1 | SLACK-RTR-CFG | unit | `go test ./internal/app/config/... -run DefaultRouter` | ❌ W0 (extend config_test.go) | ⬜ pending |
| 96-01-xx | 01 | 1 | SLACK-RTR-CFG (init export) | unit | `go test ./internal/app/cmd/... -run DefaultRouter` | ❌ W0 | ⬜ pending |
| 96-02-xx | 02 | 2 | SLACK-RTR-GATHER | unit | `go test ./pkg/slack/bridge/... -run Gather` | ❌ W0 (extend relayer_test.go) | ⬜ pending |
| 96-02-xx | 02 | 2 | SLACK-RTR-SAFE (legacy→claimed) | unit | `go test ./pkg/slack/bridge/... -run 'Gather|Legacy'` | ❌ W0 | ⬜ pending |
| 96-03-xx | 03 | 2/3 | SLACK-RTR-ORPHAN | unit | `go test ./pkg/slack/bridge/... -run Orphan` | ❌ W0 (extend events_handler_test.go) | ⬜ pending |
| 96-03-xx | 03 | 2/3 | SLACK-RTR-REPLY | unit | `go test ./pkg/slack/bridge/... -run RouterReply` | ❌ W0 | ⬜ pending |
| 96-03-xx | 03 | 2/3 | SLACK-RTR-COOLDOWN | unit | `go test ./pkg/slack/bridge/... -run Cooldown` | ❌ W0 | ⬜ pending |
| 96-03-xx | 03 | 2/3 | SLACK-RTR-SAFE (default off / anti-loop) | unit | `go test ./pkg/slack/bridge/... -run 'NilRelayer|RouterDisabled|BotLoop'` | ✅ partial (NilRelayer exists) | ⬜ pending |
| 96-E2E | — | — | SLACK-RTR-E2E | manual UAT | two installs + one Slack App (see Manual-Only) | n/a | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/app/config/config_test.go` — `slack.default_router` round-trip + merge-list regression + drift (template: `TestLoadSlackPeerBridges_Set`).
- [ ] `pkg/slack/bridge/relayer_test.go` — extend for scatter-gather: parse `{claimed,channels}`, legacy `"ok"`→claimed:true, HTTP-error→claimed:true, bounded timeout (httptest servers).
- [ ] `pkg/slack/bridge/events_handler_test.go` — orphan detection (zero claims), claim short-circuit, mention gate, cooldown suppress/allow, reply formatting (`<#CID>` aggregation), relayed-request returns `{claimed,channels}`, default_router=false silent, bot-loop no re-trigger.
- [ ] A `fakeRunningChannelLister` + `fakeCooldownStore` (or reuse nonce-store fake) for the handler tests.

*Existing infra (go test, events_handler_test.go / relayer_test.go fakes, TestEventsHandler_NilRelayer_MissReturns200) covers the default-off baseline.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Cross-install orphan reply E2E | SLACK-RTR-E2E | Needs one real Slack App + two live installs (real DDB/SQS, Slack delivery) | Invite the bot to a channel with no bound sandbox; @-mention it; confirm exactly ONE threaded reply listing running sandbox channels from BOTH installs as `<#CID>`; @-mention again within the cooldown → no second reply; bind/own the channel (create a sandbox there or have an install own it) → @-mention → owner handles it, no router reply. Deploy: `make build-lambdas && km init --dry-run=false` (NOT `--sidecars`) on all installs. |
| DDB Scan IAM reaches Lambda | SLACK-RTR-GATHER (infra) | Terraform apply against real AWS | After `km init --dry-run=false`, confirm the bridge role policy includes `dynamodb:Scan` on `{prefix}-sandboxes` (the new running-channels lister). |

*Automated tests cover all pure-Go behavior (CFG, GATHER, ORPHAN, REPLY, COOLDOWN, SAFE). Only live infra wiring + cross-install delivery are manual.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 90s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
