---
phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions
plan: "06"
subsystem: bridge-quota-enforcement
tags: [quota, freeze, slack-bridge, h1-bridge, brd-01, brd-02, brd-03, h1-01, iam, tdd]
dependency_graph:
  requires: ["121-02", "121-04"]
  provides: ["BRG-01", "BRG-02", "BRG-03", "H1-01"]
  affects: ["pkg/slack/bridge", "pkg/h1/bridge", "infra/modules/lambda-slack-bridge", "infra/modules/lambda-h1-bridge"]
tech_stack:
  added: ["pkg/quota (Record calls at bridge chokepoints)"]
  patterns: ["TDD red-green per task", "fail-open quota enforcement", "control-plane notices (uncounted)"]
key_files:
  created:
    - pkg/slack/bridge/handler_quota_test.go
    - pkg/slack/bridge/quota_fake_test.go
    - pkg/h1/bridge/quota_test.go
    - pkg/h1/bridge/quota_fake_test.go
  modified:
    - pkg/slack/bridge/events_interfaces.go
    - pkg/slack/bridge/aws_adapters.go
    - pkg/slack/bridge/events_handler.go
    - pkg/slack/bridge/handler.go
    - pkg/slack/bridge/interfaces.go
    - pkg/h1/bridge/interfaces.go
    - pkg/h1/bridge/webhook_handler.go
    - infra/modules/lambda-slack-bridge/v1.0.0/main.tf
    - infra/modules/lambda-slack-bridge/v1.0.0/variables.tf
    - infra/modules/lambda-h1-bridge/v1.0.0/main.tf
    - infra/modules/lambda-h1-bridge/v1.0.0/variables.tf
decisions:
  - "Frozen gate placed before dedup (step 5c in EventsHandler) so a frozen sandbox never consumes a nonce"
  - "Quota check in Handler.checkQuota is fail-open: limits fetch error or nil Quota → proceed as before (dormant)"
  - "H1 quota+frozen checks added to enqueueAndUpsert (where sandboxID is resolved) rather than dispatchTarget top"
  - "Trip notices are posted by the bridge (control-plane, not counted by quota.Record)"
  - "IAM policies for quota table are gated on non-empty quota_table_arn variable (backward-compatible, dormant until table deployed)"
metrics:
  duration: "719s"
  completed_date: "2026-06-27T13:19:29Z"
  tasks_completed: 4
  files_modified: 11
  files_created: 4
---

# Phase 121 Plan 06: Bridge Quota Enforcement + Frozen Dispatch Gate Summary

Bridge Lambdas (Slack + H1) are now the agent-untrusted chokepoint for outbound chat: they call `quota.Record` for `slack_post` / `h1_comment`, post enforce-aware in-thread control-plane notices on trips, and refuse to dispatch new turns when the sandbox is quarantine-frozen.

## What Was Built

### Task 1: FetchByChannel reads action_limits/action_frozen + frozen-dispatch gate (BRG-02/03)

- Added `ActionLimits string`, `ActionFrozen bool`, `FrozenReason string` to `SandboxRoutingInfo` in `events_interfaces.go`
- `DDBSandboxByChannel.FetchByChannel` now reads `action_limits` (S), `action_frozen` (BOOL), `frozen_reason` (S) from the km-sandboxes item (mirroring `slack_allow` read pattern)
- Added step 5c frozen gate in `EventsHandler.Handle` — before dedup/enqueue; posts in-thread control-plane notice (`🛑 This sandbox is frozen (reason). ...`) via `h.Slack.PostMessage`, then returns 200 (Slack never retries 200)
- Tests: `TestFetchByChannel_ActionAttrs` (BRG-03) + `TestFrozenDispatch` (BRG-02) — both green

### Task 2: Slack quota.Record on ActionPost/ActionUpload + in-thread trip notice (BRG-01)

- Added `QuotaAPI` and `ActionLimitsFetcher` interfaces to `interfaces.go`
- Added `Quota QuotaAPI`, `QuotaTable string`, `Limits ActionLimitsFetcher` fields to `Handler`
- `Handler.checkQuota` helper: fetches limits JSON, unmarshals, calls `quota.Record`, posts notice, returns `(decision, block)` — fail-open on any error
- `Handler.postQuotaNotice` helper: enforce-aware wording (⚠️ for WARN, 🛑 for BLOCK/FREEZE)
- `quota.Record` called at `ActionPost`/`ActionTest` top and `ActionUpload` top — before any Slack API call
- BLOCK/FREEZE trip → return 429; WARN → user message still posted; no limits → dormant
- Exported `FakeQuotaClient` test helper for predictable count injection
- Tests: `TestQuotaRecord_{BlockTrip,WarnTrip,NoLimits}` — all green

### Task 3: H1 bridge parity — quota.Record + frozen path (H1-01)

- Added `H1QuotaAPI`, `H1ActionLimitsFetcher`, `H1FrozenChecker` interfaces to `h1/bridge/interfaces.go`
- Added `Quota`, `QuotaTable`, `Limits`, `FrozenCheck` fields to `WebhookHandler`
- Frozen check + quota.Record added in `enqueueAndUpsert` (where sandboxID is resolved)
- Frozen: `H1FrozenChecker.IsFrozen` → `postInternalReply` + early return (no SQS)
- Quota: `quota.Record(h1_comment)` → BLOCK/FREEZE → early return; WARN → `postH1QuotaNotice` then continue; `postH1QuotaNotice` always posts `internal=true` (never researcher-visible)
- Exported `FakeH1QuotaClient` test helper
- Tests: `TestQuotaRecord_H1_{BlockTrip,WarnTrip,NoLimits,FrozenDispatch}` — all green

### Task 4: IAM — quota-table write on slack + h1 bridge roles

- Added `quota_table_arn` variable (default `""`) to both module's `variables.tf`
- Added `dynamodb_action_quota` IAM policy resource (gated `count = var.quota_table_arn != "" ? 1 : 0`) granting `dynamodb:UpdateItem` + `dynamodb:GetItem` on the quota table ARN — to both bridge roles
- Added `KM_QUOTA_TABLE` env var to both Lambda environments (empty when quota not yet provisioned)
- `terraform fmt` clean; in-place additive edit at current module version (precedent: Phase 109/100)

## Verification

```
go test ./pkg/slack/bridge/... ./pkg/h1/bridge/... -count=1
ok  github.com/whereiskurt/klanker-maker/pkg/slack/bridge  5.328s
ok  github.com/whereiskurt/klanker-maker/pkg/h1/bridge     0.436s
```

Specific plan tests:
- `TestFetchByChannel_ActionAttrs` (BRG-03) ✓
- `TestFrozenDispatch` (BRG-02) ✓
- `TestQuotaRecord_BlockTrip` (BRG-01) ✓
- `TestQuotaRecord_WarnTrip` (BRG-01) ✓
- `TestQuotaRecord_NoLimits` (BRG-01 dormant) ✓
- `TestQuotaRecord_H1_BlockTrip` (H1-01) ✓
- `TestQuotaRecord_H1_WarnTrip` (H1-01) ✓
- `TestQuotaRecord_H1_FrozenDispatch` (H1-01) ✓

## Must-Haves Verification

| Truth | Status |
|---|---|
| FetchByChannel reads action_limits + action_frozen onto SandboxRoutingInfo | ✓ |
| Slack bridge calls quota.Record for ActionPost/ActionUpload; BLOCK → 429; trip posts in-thread notice | ✓ |
| Events handler refuses to dispatch when ActionFrozen=true; posts frozen notice | ✓ |
| H1 bridge calls quota.Record for h1_comment with same frozen/notice path | ✓ |
| Both trip + frozen notices are control-plane (uncounted, from token holder), best-effort | ✓ |
| slack/h1 bridge roles get UpdateItem/GetItem on {prefix}-action-quota | ✓ |

## Deviations from Plan

### Auto-fixed Issues

None — plan executed exactly as written.

### Design Notes (non-deviation)

1. **Frozen gate position:** Placed at step 5c (before dedup, after channel resolution + allowlist) so frozen sandboxes never consume a nonce. The plan said "before enqueue" which is satisfied.

2. **H1 quota+frozen in `enqueueAndUpsert`:** The plan suggested adding in `dispatchTarget`. Implemented in `enqueueAndUpsert` instead — this is where `sandboxID` is resolved and available (needed by `H1FrozenChecker.IsFrozen`). Functionally equivalent; avoids redundant resolver calls.

3. **`ActionLimitsFetcher` not `SandboxByChannelFetcher` for Handler:** The outbound Handler uses sandbox ID (from `env.SenderID`) not channel ID for the limits lookup, so a separate interface was cleaner than reusing `SandboxByChannelFetcher`.

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| 1 | eb0ed0d6 | FetchByChannel reads action_limits/action_frozen + frozen-dispatch gate (BRG-02/03) |
| 2 | 5b566563 | Slack quota.Record on ActionPost/ActionUpload + in-thread trip notice (BRG-01) |
| 3 | a4db7853 | H1 bridge parity — quota.Record + frozen gate for h1_comment (H1-01) |
| 4 | 9b930a44 | IAM — dynamodb:UpdateItem/GetItem on {prefix}-action-quota for both bridge roles |

## Deploy Notes

- `make build-lambdas` + `km init --dry-run=false` (NOT `--sidecars` — env block + IAM require full terragrunt apply; `--sidecars` does not update the env block)
- The quota table (`{prefix}-action-quota`) must be deployed first (plan 02) before the quota IAM policies take effect (empty `quota_table_arn` = policies omitted, bridge dormant)
- Existing sandboxes keep dormant behavior until `km destroy && km create` (new attrs `action_limits`/`action_frozen` written at create time)

## Self-Check: PASSED

All 12 key files found. All 4 task commits verified in git log.
