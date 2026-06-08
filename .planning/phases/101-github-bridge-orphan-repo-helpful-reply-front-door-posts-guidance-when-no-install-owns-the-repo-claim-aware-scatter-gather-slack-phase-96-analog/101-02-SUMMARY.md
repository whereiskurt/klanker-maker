---
phase: "101"
plan: "02"
subsystem: pkg/github/bridge
tags: [tdd, relayer, claim-aware, scatter-gather, phase-101, federation]
dependency_graph:
  requires: []
  provides: [PeerClaimResult, claim-aware-Broadcast]
  affects: [pkg/github/bridge/webhook_handler.go, cmd/km-github-bridge/main.go]
tech_stack:
  added: []
  patterns: [scatter-gather, rollout-safety-conservative-claim, TDD-RED-GREEN]
key_files:
  created: []
  modified:
    - pkg/github/bridge/interfaces.go
    - pkg/github/bridge/relayer.go
    - pkg/github/bridge/relayer_test.go
    - pkg/github/bridge/webhook_handler.go
    - pkg/github/bridge/webhook_handler_phase100_test.go
decisions:
  - "peerRelayResponse has no Channels field — GitHub orphan reply has no repo list to return, unlike Slack Phase-96"
  - "rollout safety: legacy 'ok' body / non-2xx / timeout all tally Claimed:true; only explicit {claimed:false} is unclaimed"
  - "webhook_handler.go call site updated to discard tally for now — Plan 03 (Wave 2) adds the front-door orphan reply logic"
metrics:
  duration: "286s"
  completed_date: "2026-06-08"
  tasks_completed: 2
  files_modified: 5
---

# Phase 101 Plan 02: Claim-Aware GitHub Bridge Relayer Summary

Upgraded the GitHub bridge relayer from Phase-100 fire-and-forget to the Phase-96 Slack claim-aware scatter-gather shape — minus the channel list. `Broadcast` now returns `([]PeerClaimResult, error)` so the front door can detect true orphan repos (zero claims from all peers) and post a helpful reply.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 (RED) | claim-tally + rollout-safety relayer tests | ac7c0ebc | relayer_test.go |
| 2 (GREEN) | PeerClaimResult + claim-aware Broadcast/postToPeer | 5e240fdb | interfaces.go, relayer.go, webhook_handler.go, webhook_handler_phase100_test.go |

## What Was Built

### `pkg/github/bridge/interfaces.go`

- Added `PeerClaimResult{ PeerURL string; Claimed bool }` — no `Channels` field (GitHub orphan reply has no repo list, unlike Slack)
- Changed `PeerRelayer.Broadcast` return type from `error` to `([]PeerClaimResult, error)` — exact same evolution as Slack Phase 95→96
- Rewrote interface doc; removed the superseded Phase-100 "do not add a claim-result slice return here" note

### `pkg/github/bridge/relayer.go`

- Added `type peerRelayResponse struct { Claimed bool \`json:"claimed"\` }` — no `Channels`
- Rewrote `Broadcast` to use buffered `resultCh chan PeerClaimResult`, per-peer goroutines calling `postToPeer`, `wg.Wait()`, drain into `[]PeerClaimResult`
- Added `postToPeer(ctx, peerURL, rawBody, ghHeaders)` with rollout-safety invariant:
  - Transport error → return `(PeerClaimResult{}, err)` → caller sets `Claimed:true`
  - Non-2xx status → `PeerClaimResult{..., Claimed: true}`
  - `json.Unmarshal` fails (legacy `"ok"` body) → `Claimed:true`
  - Explicit `{"claimed":false}` → `Claimed:false` (only way to count as unclaimed)
- Preserved: `relayBroadcastTimeout` const, GitHub header forwarding, `X-KM-Relayed:1` loop guard, `wg.Wait()` synchronous pattern

### `pkg/github/bridge/relayer_test.go`

New tests (Phase 101):
- `TestHTTPPeerRelayer_ClaimTally_MixedPeers` — 3 peers, 2×claimed:true + 1×claimed:false
- `TestHTTPPeerRelayer_RolloutLegacyOk_ClaimedTrue` — legacy `"ok"` body → Claimed:true
- `TestHTTPPeerRelayer_RolloutNon2xx_ClaimedTrue` — HTTP 500 → Claimed:true
- `TestHTTPPeerRelayer_RolloutTimeout_ClaimedTrue` — timeout → Claimed:true (bounded return)
- `TestHTTPPeerRelayer_ClaimedFalse_Parsed` — explicit `{"claimed":false}` → Claimed:false (orphan detection)
- `TestHTTPPeerRelayer_Empty_NilNil` — empty PeerURLs → (nil, nil)

Phase-100 tests preserved and updated to 2-value call sites.

## Verification Results

```
--- PASS: TestHTTPPeerRelayer_Broadcast_ForwardsHeaders
--- PASS: TestHTTPPeerRelayer_Broadcast_AllPeers
--- PASS: TestHTTPPeerRelayer_Broadcast_FailingPeerNonFatal
--- PASS: TestHTTPPeerRelayer_Broadcast_BoundedContext
--- PASS: TestHTTPPeerRelayer_Broadcast_EmptyPeers_NoOp
--- PASS: TestHTTPPeerRelayer_RelayedVerify
--- PASS: TestHTTPPeerRelayer_ClaimTally_MixedPeers
--- PASS: TestHTTPPeerRelayer_RolloutLegacyOk_ClaimedTrue
--- PASS: TestHTTPPeerRelayer_RolloutNon2xx_ClaimedTrue
--- PASS: TestHTTPPeerRelayer_RolloutTimeout_ClaimedTrue
--- PASS: TestHTTPPeerRelayer_ClaimedFalse_Parsed
--- PASS: TestHTTPPeerRelayer_Empty_NilNil
ok  github.com/whereiskurt/klanker-maker/pkg/github/bridge  10.421s
```

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Updated webhook_handler.go call site and mockPeerRelayer**
- **Found during:** Task 2 (GREEN) — `go test` compile step
- **Issue:** `webhook_handler.go:239` called `h.Relayer.Broadcast(...)` expecting single `error` return; `mockPeerRelayer.Broadcast` in `webhook_handler_phase100_test.go` also had the old signature
- **Fix:** Updated `webhook_handler.go` to `if _, bErr := h.Relayer.Broadcast(...)` with a comment that Plan 03 (Wave 2) will consume the tally; updated `mockPeerRelayer.Broadcast` to return `([]bridge.PeerClaimResult, error)`
- **Files modified:** `pkg/github/bridge/webhook_handler.go`, `pkg/github/bridge/webhook_handler_phase100_test.go`
- **Commit:** 5e240fdb (same GREEN commit)

## Success Criteria Met

- GH-ORPHAN-CLAIM (relayer half): `Broadcast` returns `[]PeerClaimResult` per peer
- GH-ORPHAN-ROLLOUT (tally invariant): transport error / non-2xx / legacy-`"ok"` / timeout all tally `Claimed:true`; only explicit `{"claimed":false}` counts as unclaimed
- Synchronous (`wg.Wait`), bounded (`relayBroadcastTimeout`), header/loop-guard unchanged
- No channel machinery (`SandboxChannelInfo`, `RunningChannelLister`, `Channels` field)
- 12/12 tests pass

## Self-Check: PASSED
