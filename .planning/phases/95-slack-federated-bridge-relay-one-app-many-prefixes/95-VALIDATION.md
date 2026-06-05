---
phase: 95
slug: slack-federated-bridge-relay-one-app-many-prefixes
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-05
---

# Phase 95 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (standard library, table-driven) |
| **Config file** | none — existing Go toolchain |
| **Quick run command** | `go test ./pkg/slack/bridge/... ./internal/app/config/...` |
| **Full suite command** | `go build ./... && go test ./...` |
| **Estimated runtime** | ~30–90 seconds |

---

## Sampling Rate

- **After every task commit:** Run the package-scoped `go test` for the touched package(s).
- **After every plan wave:** Run `go build ./... && go test ./...`.
- **Before `/gsd:verify-work`:** Full suite must be green; `make build` succeeds.
- **Max feedback latency:** ~90 seconds.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 95-01-xx | 01 | 1 | SLACK-FED-CFG | unit | `go test ./internal/app/config/... -run Slack` | ❌ W0 (extend config_test.go) | ⬜ pending |
| 95-01-xx | 01 | 1 | SLACK-FED-PLUMB (config half + init export) | unit | `go test ./internal/app/config/... ./internal/app/cmd/... -run 'Slack|PeerBridge'` | ❌ W0 | ⬜ pending |
| 95-02-xx | 02 | 2 | SLACK-FED-RELAY | unit | `go test ./pkg/slack/bridge/... -run Relay` | ❌ W0 (new relayer_test.go) | ⬜ pending |
| 95-02-xx | 02 | 2 | SLACK-FED-LOOP | unit | `go test ./pkg/slack/bridge/... -run 'Relay|DecisionTable'` | ✅ extend events_handler_test.go | ⬜ pending |
| 95-02-xx | 02 | 2 | SLACK-FED-VERIFY | unit | `go test ./pkg/slack/bridge/... -run Verify` | ✅ verifySlackSignature already tested | ⬜ pending |
| 95-03-xx | 03 | 3 | SLACK-FED-DOCTOR | unit | `go test ./internal/app/cmd/... -run Doctor.*Peer` | ❌ W0 (new doctor test) | ⬜ pending |
| 95-03-xx | 03 | 3 | SLACK-FED-PLUMB (TF/terragrunt half) | manual/build | `terraform fmt -check` + `make build-lambdas` | n/a (HCL) | ⬜ pending |
| 95-E2E | — | — | SLACK-FED-E2E | manual UAT | two installs, one App (see Manual-Only) | n/a | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/app/config/config_test.go` — extend with `slack.peer_bridges` round-trip + merge-list + drift cases (template: `TestLoadSlackMentionOnly_True`).
- [ ] `pkg/slack/bridge/relayer_test.go` — new; `fakePeerRelayer` + `HTTPPeerRelayer` against `httptest.Server`(s); header-preservation, parallel, bounded-timeout, failing-peer cases.
- [ ] `pkg/slack/bridge/events_handler_test.go` — extend with the four-row decision table (`{relayed?, hit?} → {process, broadcast, drop}`) + loop-guard (relayed+miss never calls relayer) using a `fakePeerRelayer`.
- [ ] `internal/app/cmd/` doctor test — new cases for malformed/self-loop/empty peer_bridges.

*Existing infrastructure (go test, events_handler_test.go mock patterns, verifySlackSignature tests) covers the rest.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Cross-install relay end-to-end | SLACK-FED-E2E | Needs one real Slack App + two live km installs (real AWS account, real SQS/DDB, Slack delivery) | Install one App; `km slack init` on installs A and B with the SAME xoxb+signing secret; set Slack Request URL → A's bridge `/events`; set A's `slack.peer_bridges` → [B's `/events`]; `make build-lambdas && km init --dry-run=false` on A (and B); create a sandbox under B; post in B's `#sb-{id}` channel; confirm B's SQS enqueue + 👀 ack and that A relayed (CloudWatch logs show broadcast, B shows processed). |
| TF env-var reaches Lambda | SLACK-FED-PLUMB (infra half) | Terraform apply against real AWS | `km init --dry-run=false`; `aws lambda get-function-configuration --function-name {prefix}-slack-bridge` shows `KM_SLACK_PEER_BRIDGES`. |

*Automated tests cover all pure-Go behavior (CFG, PLUMB config-half, RELAY, LOOP, VERIFY, DOCTOR). Only the live infra wiring + cross-install delivery are manual.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 90s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
