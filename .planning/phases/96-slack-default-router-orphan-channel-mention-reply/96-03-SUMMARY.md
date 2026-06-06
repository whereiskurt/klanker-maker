---
phase: 96-slack-default-router-orphan-channel-mention-reply
plan: "03"
subsystem: slack-bridge
tags: [slack, federation, orphan-reply, cooldown, phase96]
dependency_graph:
  requires: [96-02-PLAN]
  provides: [SLACK-RTR-ORPHAN, SLACK-RTR-REPLY, SLACK-RTR-COOLDOWN, SLACK-RTR-SAFE]
  affects: [pkg/slack/bridge/events_handler.go, pkg/slack/bridge/events_interfaces.go, cmd/km-slack-bridge/main.go, docs/slack-notifications.md, CLAUDE.md]
tech_stack:
  added: []
  patterns: [RouterCooldownStore interface, routerCooldownAdapter (nonces table TTL), maybePostOrphanReply (synchronous PostMessage)]
key_files:
  created: []
  modified:
    - pkg/slack/bridge/events_interfaces.go
    - pkg/slack/bridge/events_handler.go
    - pkg/slack/bridge/events_handler_test.go
    - cmd/km-slack-bridge/main.go
    - docs/slack-notifications.md
    - CLAUDE.md
decisions:
  - RouterCooldownStore wraps DynamoNonceStore.Reserve with a 'router-cooldown:' key prefix — reuses the existing nonces table and IAM grants, no new infrastructure
  - orphan reply posted SYNCHRONOUSLY (no goroutine) — Lambda-freeze safety (PITFALL 3)
  - DefaultRouter is a plain bool (false zero value = dormant), not *bool, so nil-Relayer path is structurally byte-identical to Phase 95
  - maybePostOrphanReply extracted as a helper method for testability and readability
metrics:
  duration: 314s
  completed: "2026-06-06T03:37:36Z"
  tasks: 3
  files: 6
---

# Phase 96 Plan 03: Front-door orphan reply, cooldown, wiring, and docs Summary

**One-liner:** Synchronous threaded orphan-channel reply behind a claim-tally + mention + cooldown gate, wired from `KM_SLACK_DEFAULT_ROUTER` env, dormant by default.

## What Was Built

### Task 1 — RouterCooldownStore interface + front-door orphan detection, gates, and threaded reply

Added `RouterCooldownStore` interface to `events_interfaces.go`:

```go
type RouterCooldownStore interface {
    Reserve(ctx context.Context, channelID string, cooldownSeconds int) error
}
```

Added two new fields to `EventsHandler`:
- `DefaultRouter bool` — feature flag (false zero value = dormant)
- `RouterCooldown RouterCooldownStore` — per-channel cooldown via nonces table

Added `maybePostOrphanReply` helper that enforces four gates in order:
1. `h.DefaultRouter` true (cheapest check)
2. `msg.Channel != ""` (defensive guard)
3. Bot @-mention (`<@{bot_user_id}>` in text, using `h.BotUserID.Fetch`)
4. Cooldown clear (`h.RouterCooldown.Reserve(ctx, channel, 3600)` returns nil)

Reply aggregates local `h.RunningChannels.ListRunning` + all peers' `result.Channels`
with deduplication by channel ID. Non-empty list uses the `<#CID> — alias (profile)`
format; empty list uses the guidance-only variant.

All nine behavior tests pass (GREEN): OrphanReply, ClaimShortCircuit, NonMention,
Off (Phase 95 byte-identical), EmptyList (guidance-only), Cooldown_Suppress,
Cooldown_Allow, BotLoopNoRetrigger (regression), EmptyChannelGuard.

### Task 2 — Cold-start wiring in main.go

Added `routerCooldownAdapter` type (modelled on `nonceStoreAdapter`) that prefixes
`"router-cooldown:"` on the channel ID before calling `DynamoNonceStore.Reserve`.

In `wireEventsHandler`, after the Phase 95 relay block:

```go
if os.Getenv("KM_SLACK_DEFAULT_ROUTER") == "true" {
    eventsHandler.DefaultRouter = true
    eventsHandler.RunningChannels = &bridge.DDBRunningChannelLister{Client: initDDB, TableName: sandboxesTable}
    eventsHandler.RouterCooldown = &routerCooldownAdapter{inner: initNonces}
    slog.Info("km-slack-bridge: default-router enabled")
}
```

When env absent/false all three fields remain zero/nil — router is structurally dormant.

### Task 3 — Docs

- `docs/slack-notifications.md`: added § Phase 96 covering what/why/how, config,
  claim-aware scatter-gather mechanism, cooldown, no-new-scopes note, deploy
  constraint (`make build-lambdas` + `km init --dry-run=false` NOT `--sidecars`),
  deferred items, doctor check table, troubleshooting table.
- `CLAUDE.md`: added Phase 96 phase-history note + Where-to-look row.

### Task 4 — Manual E2E (CHECKPOINT — NOT YET EXECUTED)

The cross-install E2E is a `checkpoint:human-verify` gate. The operator must:
1. Set `slack.default_router: true` in `km-config.yaml` on the front-door install.
2. Deploy ALL installs: `make build-lambdas` + `km init --dry-run=false`.
3. Verify `KM_SLACK_DEFAULT_ROUTER=true` in the front-door Lambda env tab.
4. @-mention the bot in a true orphan channel; expect ONE threaded reply.
5. @-mention again within 5 min; expect NO second reply (cooldown).
6. @-mention in an owned `#sb-*` channel; expect NO router reply.
7. Post a non-mention message in the orphan channel; expect NO reply.

Record outcomes in `96-UAT.md`.

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| 1 | `1f839e3c` | RouterCooldownStore interface + orphan detection, gates, threaded reply (9 tests) |
| 2 | `81a3c60c` | main.go cold-start wiring: KM_SLACK_DEFAULT_ROUTER + routerCooldownAdapter |
| 3 | `c6bdd1e8` | docs: Phase 96 slack-notifications.md section + CLAUDE.md note |

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

All key files verified present. All 3 commits verified in git log. All key content verified in files.
