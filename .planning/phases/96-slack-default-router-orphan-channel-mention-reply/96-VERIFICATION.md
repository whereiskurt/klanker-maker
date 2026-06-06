---
phase: 96-slack-default-router-orphan-channel-mention-reply
verified: 2026-06-05T15:45:00Z
status: human_needed
score: 6/7 must-haves verified (SLACK-RTR-E2E requires live AWS+Slack — human checkpoint)
re_verification: false
human_verification:
  - test: "Cross-install E2E: @-mention in orphan channel yields exactly one threaded reply listing both installs' running channels as <#CID>"
    expected: "One threaded reply naming #sb-{alias}-{profile} convention plus bullet list of running sandbox channels; reply posted in thread of the @-mention message"
    why_human: "Requires two live km installs sharing one Slack App, live DynamoDB/SQS, and real Slack message delivery — cannot be automated"
  - test: "Cooldown: @-mention again in the same orphan channel within ~5 minutes yields NO second reply"
    expected: "Silence — the per-channel nonces-table TTL suppresses the second reply"
    why_human: "Requires real Slack + live nonces DynamoDB table (router-cooldown:{channel}, 3600s TTL)"
  - test: "Owned channel: @-mention bot in an active #sb-* sandbox channel yields NO router reply"
    expected: "Normal SQS dispatch occurs; no orphan-reply posted"
    why_human: "Requires a live sandbox with a bound Slack channel and real Slack delivery"
  - test: "Non-mention: posting a regular (non-@-mention) message in the orphan channel yields NO reply"
    expected: "Bridge processes the message normally; no threaded reply posted"
    why_human: "Requires real Slack event delivery to the live bridge Lambda"
---

# Phase 96: Slack Default Router — Orphan Channel @-Mention Reply Verification Report

**Phase Goal:** When the shared bot is @-mentioned in a channel no install owns, the designated front-door install posts one helpful threaded reply (naming convention + running sandbox channels from all installs as <#CID>) using a claim-aware scatter-gather; zero claims => orphan. Opt-in (default false => byte-identical to Phase 95).

**Verified:** 2026-06-05T15:45:00Z
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `slack.default_router: true` in km-config.yaml produces `KM_SLACK_DEFAULT_ROUTER=true` via `km init` | VERIFIED | `config.go:66` SlackConfig.DefaultRouter *bool; merge-list entry at line 432; population block at line 538; `init.go:922` exports env var |
| 2 | Absent `slack.default_router` leaves cfg field nil and exports nothing (Phase 95 byte-identical) | VERIFIED | `init.go:922` — export block only runs when `cfg.Slack.DefaultRouter != nil`; nil => no setenv call |
| 3 | Broadcast returns `[]PeerClaimResult`; legacy "ok"/HTTP-error/timeout all map to Claimed:true | VERIFIED | `relayer.go:65` Broadcast signature; line 88 transport error => `Claimed:true`; `postToPeer` line 151 non-200 or unmarshal-fail => `Claimed:true`; tests TestPeerRelayer_LegacyResponseClaimedTrue, TestPeerRelayer_HTTPErrorClaimedTrue, TestPeerRelayer_BoundedTimeout all pass |
| 4 | Relayed-miss returns `{claimed:false, channels:[...]}` JSON; relayed-owned returns `{claimed:true}` | VERIFIED | `events_handler.go:246` relayed-miss branch with JSON marshal; `events_handler.go:522` relayed-owned branch; both tests TestEventsHandler_RelayedMiss_ReturnsChannels and TestEventsHandler_RelayedOwned_ReturnsClaimedTrue pass |
| 5 | Orphan reply fires only when: default_router=true + bot mention + zero claims + cooldown clear; all other cases => 200 silently | VERIFIED | `maybePostOrphanReply` at line 599 with 4 ordered gates; 9 behavior tests all pass (OrphanReply, ClaimShortCircuit, NonMention, Off, EmptyList, Cooldown_Suppress, Cooldown_Allow, BotLoopNoRetrigger, EmptyChannelGuard) |
| 6 | DDBRunningChannelLister uses ExpressionAttributeNames `{#s:state}` + pagination | VERIFIED | `aws_adapters.go:1376` ExpressionAttributeNames with `"#s":"state"`; loop on `LastEvaluatedKey`; TestDDBRunningChannelLister_ReservedWordExpr + TestDDBRunningChannelLister_Pagination both pass |
| 7 | Cross-install E2E: orphan @-mention triggers one reply, cooldown suppresses repeat, owned channel unaffected, non-mention silent | HUMAN NEEDED | SLACK-RTR-E2E requires live AWS + two installs + real Slack delivery — see human verification section; 96-UAT.md not yet written |

**Score:** 6/7 truths verified (1 human checkpoint remaining)

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/config/config.go` | SlackConfig.DefaultRouter *bool + merge-list + tri-state population | VERIFIED | Line 66 struct field; line 432 merge-list `"slack.default_router"`; lines 538-540 population block |
| `internal/app/cmd/init.go` | KM_SLACK_DEFAULT_ROUTER export with drift WARN | VERIFIED | Lines 915-927 export block, env-wins WARN on line 925 |
| `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` | slack_default_router string variable (default "false") | VERIFIED | Line 142 variable declaration, type string, default "false" |
| `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` | KM_SLACK_DEFAULT_ROUTER Lambda env + dynamodb:Scan IAM on sandboxes table | VERIFIED | Line 341 Lambda env entry; lines 208-211 `DDBSandboxesScanRunningChannels` statement with `dynamodb:Scan` on `var.sandboxes_table_arn` |
| `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` | slack_default_router = get_env passthrough | VERIFIED | Line 121 `slack_default_router = get_env("KM_SLACK_DEFAULT_ROUTER", "false")` |
| `pkg/slack/bridge/events_interfaces.go` | PeerClaimResult, SandboxChannelInfo, updated PeerRelayer, RunningChannelLister, DDBScanAPI, RouterCooldownStore | VERIFIED | All six types/interfaces present at lines 13-69 |
| `pkg/slack/bridge/relayer.go` | Broadcast returns []PeerClaimResult; legacy/error/timeout => Claimed:true | VERIFIED | Line 65 signature; lines 83-91 goroutine with conservative claim on error |
| `pkg/slack/bridge/aws_adapters.go` | DDBRunningChannelLister with reserved-word alias + pagination | VERIFIED | Lines 1359-1413 struct + ListRunning implementation |
| `pkg/slack/bridge/events_handler.go` | DefaultRouter bool + RouterCooldown + maybePostOrphanReply + relayed response changes | VERIFIED | Fields at lines 82-110; maybePostOrphanReply at line 599; relayed paths at lines 246-273 and 522-532 |
| `cmd/km-slack-bridge/main.go` | KM_SLACK_DEFAULT_ROUTER env read + wiring block + routerCooldownAdapter | VERIFIED | Lines 294-302 wiring block; lines 613-619 routerCooldownAdapter |
| `docs/slack-notifications.md` | Phase 96 section | VERIFIED | Lines 2101-2229+ with full operator guide |
| `CLAUDE.md` | Phase 96 note + Where-to-look row | VERIFIED | Lines 21-29 phase note; line 49 Where-to-look entry |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `config.go` v2->v merge-list | slack.default_router population block | `"slack.default_router"` entry at line 432 | VERIFIED | Both merge-list entry and population block confirmed present |
| `init.go` export block | `os.Setenv("KM_SLACK_DEFAULT_ROUTER")` | `cfg.Slack.DefaultRouter != nil` guard | VERIFIED | Line 927 setenv; nil path skips entirely |
| `terragrunt.hcl` | Lambda env `KM_SLACK_DEFAULT_ROUTER` | `get_env("KM_SLACK_DEFAULT_ROUTER", "false")` | VERIFIED | Line 121 passthrough confirmed |
| `events_handler.go` front-door miss | `h.Relayer.Broadcast` -> tally -> `maybePostOrphanReply` | `!anyClaimed` gate at line 294 | VERIFIED | Full claim tally loop at lines 287-293; orphan call at line 295 |
| `maybePostOrphanReply` cooldown gate | `RouterCooldownStore.Reserve("router-cooldown:{channel}", 3600)` | `h.RouterCooldown.Reserve` at line 619 | VERIFIED | Prefixed key confirmed; ErrNonceReplayed check at line 621 |
| `main.go` cold-start wiring | `eventsHandler.DefaultRouter/RunningChannels/RouterCooldown` | `KM_SLACK_DEFAULT_ROUTER == "true"` | VERIFIED | Lines 294-302; `routerCooldownAdapter` at lines 613-619 |
| `HTTPPeerRelayer` postToPeer | Claimed:true on legacy/error/unparseable | JSON unmarshal failure path + status code check | VERIFIED | Lines 146-157 in relayer.go |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| SLACK-RTR-CFG | 96-01 | SlackConfig.DefaultRouter *bool, merge-list, tri-state, env export | SATISFIED | config.go + init.go + TF files all confirmed |
| SLACK-RTR-GATHER | 96-02 | Claim-aware scatter-gather, JSON response protocol, running-channels lister | SATISFIED | relayer.go Broadcast returns []PeerClaimResult; aws_adapters.go DDBRunningChannelLister; events_handler.go relayed paths |
| SLACK-RTR-ORPHAN | 96-03 | True-orphan detection: zero claims => orphan; any claim => owner handled | SATISFIED | events_handler.go lines 286-295: tally loop + anyClaimed gate |
| SLACK-RTR-REPLY | 96-03 | Threaded reply with <#CID> channel list; guidance-only when empty | SATISFIED | maybePostOrphanReply at line 599; content building at lines 657-685; synchronous PostMessage at line 697 |
| SLACK-RTR-COOLDOWN | 96-03 | Per-channel 3600s cooldown via nonces table | SATISFIED | RouterCooldownStore gate at line 618; routerCooldownAdapter prefixes "router-cooldown:" at line 618; all cooldown tests pass |
| SLACK-RTR-SAFE | 96-01/02/03 | default_router=false => Phase 95 byte-identical; non-mention silent; bot-loop structural anti-loop | SATISFIED | isBotLoop at line 216 (before miss site); TestEventsHandler_DefaultRouter_BotLoopNoRetrigger passes; TestEventsHandler_DefaultRouter_Off confirms no reply |
| SLACK-RTR-E2E | 96-03 | Two installs + one Slack App: orphan reply cross-install, cooldown, owned-channel no-reply | HUMAN NEEDED | Documented as checkpoint:human-verify task in Plan 03; 96-UAT.md not yet written; requires live environment |

### Anti-Patterns Found

No stubs, placeholders, or empty implementations found in Phase 96 modified files.

- No `TODO`/`FIXME`/`PLACEHOLDER` comments in modified source files
- No stub returns (`return null`, `return {}`, `return []`)
- No placeholder Plan 02 marker left in events_handler.go (the `// Phase 96 Plan 03: tally claims + orphan reply goes here` placeholder was replaced by the actual implementation)
- isBotLoop filter confirmed at line 216, structurally before the miss site (line 221+) — the anti-loop guarantee is structural

### Human Verification Required

#### 1. Orphan-channel @-mention triggers one threaded reply listing both installs' running channels

**Test:** Deploy all installs (`make build-lambdas` then `km init --dry-run=false` on EACH install). Set `slack.default_router: true` on the front-door install only. Have at least one running sandbox with a bound `#sb-*` channel on each install. Invite the shared bot to a channel with no bound sandbox (an orphan). @-mention the bot.

**Expected:** Exactly one threaded reply on the message. Reply text includes the `#sb-{alias}-{profile}` naming convention and bullet-lists running sandbox channels from BOTH installs as `<#CID>` Slack channel mentions.

**Why human:** Requires two live km installs sharing one Slack App, real DynamoDB Scan across both installs, and real Slack delivery.

#### 2. Cooldown: second @-mention within 5 minutes yields no second reply

**Test:** After step 1 above, @-mention the bot again in the same orphan channel within ~5 minutes.

**Expected:** No second reply. The nonces-table TTL (`router-cooldown:{channel}`, 3600s) suppresses it.

**Why human:** Requires live DynamoDB nonces table and real Slack event delivery.

#### 3. Owned channel: @-mention in active #sb-* channel yields no router reply

**Test:** @-mention the bot in a live `#sb-*` channel that has a running sandbox.

**Expected:** Normal SQS dispatch occurs (the sandbox agent receives the message). No orphan router reply posted.

**Why human:** Requires live sandbox, bound Slack channel, and SQS dispatch.

#### 4. Non-mention message in orphan channel yields no reply

**Test:** Post a regular message (no @-mention) in the orphan channel from step 1.

**Expected:** No reply posted. The mention gate silently suppresses.

**Why human:** Requires real Slack delivery.

**Deploy instructions for human verification:**
```bash
# On EACH install (front-door and all peers):
make build-lambdas     # clean build to avoid stale zip skip
km init --dry-run=false   # full apply — NOT --sidecars (env block + IAM require terragrunt)

# On front-door install only, in km-config.yaml:
slack:
  default_router: true

# Confirm in AWS console: Lambda env tab should show KM_SLACK_DEFAULT_ROUTER=true
# Confirm IAM role policy includes dynamodb:Scan on {prefix}-sandboxes
```

Record outcomes in `.planning/phases/96-slack-default-router-orphan-channel-mention-reply/96-UAT.md`.

## Build and Test Status

- `go build ./...` — GREEN (exit 0, confirmed)
- `go test ./pkg/slack/bridge/... -count=1` — GREEN (all 25 Phase-96-specific tests pass)
- `go test ./internal/app/config/... -count=1` — GREEN (DefaultRouter round-trip, absent-nil, merge-list regression)
- `go test ./internal/app/cmd/... -run 'DefaultRouter|Export' -count=1` — GREEN
- `terraform validate` (lambda-slack-bridge module) — GREEN per Plan 01 SUMMARY

The failing test `TestRunAgentAuthClaude_TeesAndCleans` in `internal/app/cmd` is an OAuth browser integration test predating Phase 96 — it requires an interactive browser session and is unrelated to this phase.

## Critical Invariant Verification

| Invariant | Status | Evidence |
|-----------|--------|----------|
| `default_router` default false / nil => byte-identical to Phase 95 (no reply, no extra calls) | VERIFIED | `h.DefaultRouter` is plain bool (false zero value); `DefaultRouter` check is Gate 1 in `maybePostOrphanReply` (cheapest, first check); when env absent/false all three router fields remain zero in main.go |
| Gate order: `DefaultRouter` check is first/cheapest | VERIFIED | `maybePostOrphanReply` line 600: `if !h.DefaultRouter { return }` — before any DDB or Slack API calls |
| Scatter-gather: any `Claimed:true` => NO router reply; only all-`Claimed:false` => orphan | VERIFIED | `events_handler.go` lines 287-295: `anyClaimed` loop breaks on first `true`; `maybePostOrphanReply` only called when `!anyClaimed` |
| Rollout safety: legacy "ok" / HTTP-error / timeout / unparseable => Claimed:true | VERIFIED | `relayer.go` postToPeer: non-2xx OR unmarshal failure => `Claimed:true`; transport error goroutine => `PeerClaimResult{Claimed: true}` |
| isBotLoop fires BEFORE miss site (structural anti-loop) | VERIFIED | `events_handler.go` line 216 (step 4) vs miss site starting at line 221 (step 5) |
| Cooldown uses nonces table with `router-cooldown:{channel}` key prefix | VERIFIED | `routerCooldownAdapter.Reserve` at line 618: `r.inner.Reserve(ctx, "router-cooldown:"+channelID, cooldownSeconds)` |
| Running-channels lister uses `#s` reserved-word alias | VERIFIED | `aws_adapters.go` line 1376: `ExpressionAttributeNames: map[string]string{"#s": "state"}` |
| `dynamodb:Scan` added to bridge Lambda IAM | VERIFIED | `main.tf` lines 208-211: `DDBSandboxesScanRunningChannels` statement with `["dynamodb:Scan"]` on `var.sandboxes_table_arn` |
| `PeerRelayer.Broadcast` signature change updated ALL implementers | VERIFIED | HTTPPeerRelayer (relayer.go:65) + fakePeerRelayer (events_handler_test.go:1403) both return `([]PeerClaimResult, error)`; `go build ./...` green |
| Orphan reply is synchronous (not goroutine) — Lambda freeze safety | VERIFIED | `maybePostOrphanReply` calls `h.Slack.PostMessage` directly at line 697 (no goroutine); code comment at line 593 documents PITFALL 3 |

---

_Verified: 2026-06-05T15:45:00Z_
_Verifier: Claude (gsd-verifier)_
