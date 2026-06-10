---
phase: 104
slug: slack-channel-o-1-resolution-on-alias-reuse
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-10
---

# Phase 104 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` (stdlib) + `net/http/httptest` |
| **Config file** | none — `go test ./...` |
| **Quick run command** | `go test ./pkg/slack/ ./internal/app/cmd/ ./pkg/aws/ ./internal/app/config/ -run 'TestFindChannel\|TestIsChannel\|TestResolvePerSandbox\|TestSlackResolveBudget\|TestSlackAdopt\|TestSlackChannelStore\|TestGetSlackChannels' -v` |
| **Full suite command** | `go test ./... 2>&1 \| tail -30` |
| **Estimated runtime** | ~25 seconds (unit subset <5s) |

---

## Sampling Rate

- **After every task commit:** Run the relevant test package subset (quick run command, scoped to the package touched)
- **After every plan wave:** Run `go test ./... 2>&1 | tail -30`
- **Before `/gsd:verify-work`:** Full suite green **AND** `scripts/validate-all-profiles.sh` green (confirm no schema regression — none expected)
- **Max feedback latency:** ~25 seconds

---

## Per-Task Verification Map

| Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|------|------|-------------|-----------|-------------------|-------------|--------|
| 01 | 1 | SLACK-CHAN-BOUND | unit | `go test ./pkg/slack/ -run TestFindChannelByName -v` | ❌ W0 (3 new cases in `client_test.go`) | ⬜ pending |
| 01 | 1 | SLACK-CHAN-BOUND | unit | `go test ./internal/app/cmd/ -run TestSlackResolveBudget -v` | ❌ W0 | ⬜ pending |
| 01 | 1 | SLACK-CHAN-INFO-CLASS | unit | `go test ./pkg/slack/ -run TestIsChannelNotFound -v` | ❌ W0 (4 cases) | ⬜ pending |
| 01 | 1 | SLACK-CHAN-LOOKUP | unit | `go test ./internal/app/cmd/ -run TestResolvePerSandbox_StoredID_Live_NoScan -v` | ❌ W0 | ⬜ pending |
| 01 | 1 | SLACK-CHAN-LOOKUP | unit | `go test ./internal/app/cmd/ -run TestResolvePerSandbox_StoredID_TransientInfo_NoScan -v` | ❌ W0 (today's bug) | ⬜ pending |
| 01 | 1 | SLACK-CHAN-LOOKUP | unit | `go test ./internal/app/cmd/ -run TestResolvePerSandbox_StoredID_NotFound_Recreates -v` | ❌ W0 | ⬜ pending |
| 01 | 1 | SLACK-CHAN-STORE | unit | `go test ./internal/app/cmd/ -run TestResolvePerSandbox_StoredID_SSMOnly_BackfillsDDB -v` (SSM-sourced hit migrates into DDB store) | ❌ W0 | ⬜ pending |
| 01 | 1 | SLACK-CHAN-LOOKUP | unit | `go test ./internal/app/cmd/ -run TestResolvePerSandbox_NameTaken_NoMapping_FailFast -v` | ❌ W0 | ⬜ pending |
| 01 | 1 | SLACK-CHAN-LOOKUP | unit | `go test ./internal/app/cmd/ -run TestResolvePerSandbox_FreshCreate_WritesStore -v` | ❌ W0 | ⬜ pending |
| 02 | 2 | SLACK-CHAN-STORE/DEPLOY | smoke | `cd infra/modules/dynamodb-slack-channels/v1.0.0 && terraform init -backend=false && terraform validate` | ❌ W0 (new module) | ⬜ pending |
| 02 | 2 | SLACK-CHAN-DEPLOY | smoke | `make build && ./km init --plan 2>&1 \| grep -i slack-channels` | after init.go entry | ⬜ pending |
| 03 | 3 | SLACK-CHAN-STORE | unit | `go test ./pkg/aws/ -run TestSlackChannelStore -v` | ❌ W0 (new file) | ⬜ pending |
| 03 | 3 | SLACK-CHAN-STORE | unit | `go test ./internal/app/config/ -run TestGetSlackChannelsTableName -v` | ❌ W0 | ⬜ pending |
| 03 | 3 | SLACK-CHAN-DEPLOY | smoke | `make build && AWS_PROFILE=klanker-application ./km init --plan 2>&1 \| grep -iE 'slack-channels\|create-handler'` | after IAM wiring | ⬜ pending |
| 04 | 4 | SLACK-CHAN-ADOPT | unit | `go test ./internal/app/cmd/ -run TestSlackAdopt -v` | ❌ W0 (new file) | ⬜ pending |
| 04 | 4 | SLACK-CHAN-ADOPT | smoke | `make build && ./km slack adopt --help` | after Task 11 | ⬜ pending |
| 04 | 4 | SLACK-CHAN-DEPLOY | smoke | `make build && AWS_PROFILE=klanker-application ./km doctor 2>&1 \| grep -i slack-channels` | after doctor check | ⬜ pending |
| 05 | 5 | SLACK-CHAN-DEPLOY | smoke | `scripts/validate-all-profiles.sh` | existing — confirm no regression | ⬜ pending |
| 05 | 5 | SLACK-CHAN-E2E | live UAT | operator: `km create profiles/github-review.yaml --alias github-bot --wait` → completes ~2 min, `slack_resolve path=cache_hit\|created`, never an unbounded scan | manual | ⬜ pending |
| 05 | 5 | SLACK-CHAN-E2E | live UAT | operator: cold orphan → `km slack adopt github-bot C0B91RA9CPR` → recreate resolves O(1) | manual | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/slack/client_test.go` — 3 new `TestFindChannelByName_*` (PageCapExceeded, ZeroCapDisablesScan, CtxCancelledMidScan); update the existing 2-arg call sites in `TestFindChannelByName_RetriesOnRateLimit` / `_RateLimitExhausted`
- [ ] `pkg/slack/client_test.go` — `TestIsChannelNotFound` (4 cases: definitive, transient ratelimited, nil, network)
- [ ] `internal/app/cmd/create_slack_test.go` — extend `fakeSlackAPI` (`findShouldPanic`, `channelInfoErr`, `createCalls`, 3-arg `FindChannelByName`); add `fakeChannelStore`; add 6 `TestResolvePerSandbox_*` (incl. `StoredID_SSMOnly_BackfillsDDB` — SSM-sourced hit promotes into DDB store); `TestSlackResolveBudget` env-parse test
- [ ] `pkg/aws/slack_channels.go` + `pkg/aws/slack_channels_test.go` — new `SlackChannelStore` + interface + 2 tests (UpsertThenGet, GetMiss); add a `fakeDDB` if absent
- [ ] `internal/app/config/config_test.go` — `TestGetSlackChannelsTableName` (3 cases: nil receiver, explicit override, prefix-derived)
- [ ] `internal/app/cmd/slack_adopt.go` + `internal/app/cmd/slack_adopt_test.go` — new command + 3 tests (RejectsBadChannelID, RequiresBotMembership, WritesThrough)

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Reused-alias create completes bounded on the REAL large workspace; `slack_resolve` path is `cache_hit`/`created`, never unbounded scan | SLACK-CHAN-E2E | Needs the operator's corporate Slack (thousands of channels) + AWS create-handler; cannot reproduce locally | `km create profiles/github-review.yaml --alias github-bot --wait`; inspect create-handler logs for the `slack_resolve` line |
| Cold orphan channel adopted then resolves O(1) | SLACK-CHAN-E2E, SLACK-CHAN-ADOPT | Requires a real orphaned channel ID in the live workspace | `km slack adopt github-bot <Cxxxx>` → re-`km create` → confirm `path=cache_hit` |
| `km init --plan` ADDs the table + IAM with no destroy-class trip | SLACK-CHAN-DEPLOY | Needs live AWS + terragrunt state | `AWS_PROFILE=klanker-application ./km init --plan` |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies (E2E rows are inherently manual — operator UAT)
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify (Plans 01/03/04 are unit-dense; 02/05 are deploy/smoke)
- [ ] Wave 0 covers all MISSING references (listed above)
- [ ] No watch-mode flags
- [ ] Feedback latency < 25s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
