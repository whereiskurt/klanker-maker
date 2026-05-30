---
phase: 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot
plan: "03"
subsystem: slack-bridge
tags:
  - slack
  - bridge
  - mention-only
  - polite-bot
  - terraform
  - lambda
dependency_graph:
  requires:
    - 91-00  # profile field + schema
    - 91-01  # compiler KM_SLACK_MENTION_ONLY emission
    - 91-02  # compiler resolveMentionOnly + SSM bot-user-id fetch
  provides:
    - EventsHandler.MentionOnly bool field (step 4b guard in Handle)
    - CachedBotUserIDFetcher.PrimeCache method
    - WireMentionOnly exported helper (cmd/km-slack-bridge)
    - KM_SLACK_MENTION_ONLY + KM_SLACK_BOT_USER_ID in Lambda Terraform env block
    - terraform fmt clean + module variables declared
  affects:
    - 91-04  # SSM bot-user-id caching (relies on KM_SLACK_BOT_USER_ID being wired)
    - 91-05  # km init --sidecars rollout (applies Terraform changes)
    - 91-06  # km doctor check (reads mention_only state)
tech_stack:
  added:
    - CachedBotUserIDFetcher.PrimeCache (Go method on bridge adapter)
    - WireMentionOnly exported helper (main package)
  patterns:
    - Step-numbered guard in Handle() (between step 4 and step 5) — Phase 91 locked insertion point
    - Fail-open on BotUserID.Fetch error — matches isBotLoop policy
    - get_env() in terragrunt.hcl — same pattern as KM_ARTIFACTS_BUCKET
key_files:
  created:
    - (none — all existing files modified)
  modified:
    - pkg/slack/bridge/events_handler.go
    - pkg/slack/bridge/events_handler_test.go
    - pkg/slack/bridge/aws_adapters.go
    - pkg/slack/bridge/aws_adapters_test.go
    - cmd/km-slack-bridge/main.go
    - cmd/km-slack-bridge/main_mention_test.go
    - infra/modules/lambda-slack-bridge/v1.0.0/variables.tf
    - infra/modules/lambda-slack-bridge/v1.0.0/main.tf
    - infra/live/use1/lambda-slack-bridge/terragrunt.hcl
decisions:
  - Exported WireMentionOnly helper (not inlined in wireEventsHandler) so tests can call
    it directly without needing the full Lambda cold-start AWS client init
  - PrimeCache uses f.ttl (defaulting to 1h if zero) — consistent with Fetch's TTL handling
  - fakeSlackAuthTestAPI91/fakeBotTokenFetcher91 type names have 91 suffix to avoid collision
    with any existing fakes in the same package (external bridge_test package)
  - No regex for mention scan — locked by CONTEXT.md and PLAN.md (substring match only)
metrics:
  duration: ~20min
  completed: 2026-05-30
  tasks_completed: 3
  tasks_deferred: 1 (Task 4 - checkpoint:human-verify for terragrunt plan)
  files_modified: 9
---

# Phase 91 Plan 03: Bridge Polite-Bot Guard + Terraform Wiring Summary

**One-liner:** Mention-scan guard (step 4b) wired into EventsHandler with PrimeCache cold-start injection and Terraform env vars for install-level polite-bot toggle.

## What Shipped

### Task 1: MentionOnly field + step 4b mention-scan guard (commit e1a739b)

Added `EventsHandler.MentionOnly bool` to the struct and inserted the step 4b guard in `Handle()` between the `isBotLoop` return (step 4) and the nonce dedup check (step 5). Placement before step 5 is critical — non-mention messages must NOT consume a nonce slot.

Guard logic:
- When `MentionOnly=false` (default): no-op, every message dispatched (pre-Phase-91 back-compat)
- When `MentionOnly=true` and `msg.Text` contains `<@{bot_user_id}>`: dispatched as normal
- When `MentionOnly=true` and no `<@{bot_user_id}>` match: return 200 immediately (skipped, no nonce, no SQS, no reaction)
- When `BotUserID.Fetch` returns error with `MentionOnly=true`: fail-open (allow message through)

`TestEventsHandler_MentionOnly` flipped from `t.Skip` to a live 7-case table test. All 7 subtests pass. Existing `TestEventsHandler_*` tests remain green.

### Task 2: PrimeCache + WireMentionOnly (commit 28ba47f)

`CachedBotUserIDFetcher.PrimeCache(uid string)` seeds the in-memory tokenCache at Lambda cold-start from the Terraform-injected `KM_SLACK_BOT_USER_ID` env var. No-op on empty uid (never cache empty strings). Uses the same `f.mu` + `tokenCache` pattern as `Fetch`.

`WireMentionOnly(h *bridge.EventsHandler, fetcher *bridge.CachedBotUserIDFetcher)` is an exported helper that reads both env vars and applies them. Exported to allow direct unit testing without the full Lambda cold-start AWS client initialization.

`wireEventsHandler()` calls `WireMentionOnly(eventsHandler, botUserIDFetcher)` immediately after the `EventsHandler` struct literal.

Tests: `TestCachedBotUserIDFetcher_PrimeCache_Hit` and `_EmptyNoOp` in `aws_adapters_test.go`; `TestWireEventsHandler_BotUserIDPrime` (5 subtests) in `main_mention_test.go`. All pass.

### Task 3: Terraform module + terragrunt.hcl wiring (commit ba662b1)

`infra/modules/lambda-slack-bridge/v1.0.0/variables.tf`: Added `slack_mention_only` (string, default "false") and `slack_bot_user_id` (string, default "").

`infra/modules/lambda-slack-bridge/v1.0.0/main.tf` environment block: Added `KM_SLACK_MENTION_ONLY = var.slack_mention_only` and `KM_SLACK_BOT_USER_ID = var.slack_bot_user_id`.

`infra/live/use1/lambda-slack-bridge/terragrunt.hcl` inputs block: Added `slack_mention_only = get_env("KM_SLACK_MENTION_ONLY", "false")` and `slack_bot_user_id = get_env("KM_SLACK_BOT_USER_ID", "")`.

`terraform fmt -check` is clean (exit 0). `terraform validate` requires `terraform init` (missing AWS provider in offline environment) — not a blocker; this is expected behavior.

## Task 4: Checkpoint (Not Run — Offline Mode)

Task 4 is a `checkpoint:human-verify` requiring a live `terragrunt plan` against a real AWS account. This plan was executed in OFFLINE mode (no AWS credentials). The operator must run this manually when AWS is available. See the checkpoint envelope below.

## Deviations from Plan

### Auto-fix: Type-naming collision avoidance in test fakes

When adding `PrimeCache` tests to `aws_adapters_test.go` (package `bridge_test`), the type names `fakeSlackAuthTestAPI` and `fakeBotTokenFetcher` were suffixed with `91` (`fakeSlackAuthTestAPI91`, `fakeBotTokenFetcher91`) to avoid any potential collision with fakes in the same external test package. This is a minor naming deviation for correctness, not a design change.

None - plan executed exactly as written for Tasks 1-3; Task 4 is a known deferred checkpoint.

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| 1 | e1a739b | feat(91-03): MentionOnly field + step 4b mention-scan guard |
| 2 | 28ba47f | feat(91-03): PrimeCache + WireMentionOnly helper |
| 3 | ba662b1 | feat(91-03): Terraform module variables + env block wiring |

## Verification Results

```
go vet ./pkg/slack/bridge/... ./cmd/km-slack-bridge/...  → CLEAN
go test ./pkg/slack/bridge/...                           → ok (all pass)
go test ./cmd/km-slack-bridge/...                        → ok (all pass)
make build                                               → Built: km v0.3.762 (ba662b1)
terraform fmt -check (lambda-slack-bridge/v1.0.0/)       → EXIT 0 (clean)
```

## Note for Downstream Plans

Plan 04 implements writing the SSM parameter `{prefix}slack/bot-user-id` during `km slack init` / `km slack rotate-token`. Operators wire SSM → env via:

```bash
export KM_SLACK_BOT_USER_ID=$(aws ssm get-parameter \
  --name /km/slack/bot-user-id --query Parameter.Value --output text)
```

before `km init --sidecars`, OR Plan 04 can extend `km env` to emit this automatically.

## Self-Check: PASSED
