---
phase: 100-github-bridge-federated-relay-one-github-app-serving-many-resource-prefix-installs-via-github-peer-bridges
plan: 03
subsystem: api
tags: [github-bridge, federated-relay, webhook-handler, loop-guard, scale-fix, byte-identity]

# Dependency graph
requires:
  - phase: 100-02
    provides: PeerRelayer interface + HTTPPeerRelayer (fire-and-forget Broadcast)
  - phase: 97-github-bridge
    provides: WebhookHandler.Handle() dispatch pipeline + Resolve() ownership match
  - phase: 98-github-thread-continuity
    provides: Phase 98 thread-bypass (known-thread no-mention dispatch) — byte-identity target
provides:
  - WebhookHandler.Relayer field (nil ⇒ federation off ⇒ byte-identical to Phase 97/98)
  - Reordered Handle() — Resolve() runs unconditionally ahead of thread-lookup (4b) + @-mention filter (5)
  - !matched decision branch (4-row {relayed?, matched?} loop-guard table)
  - 700-repo scale fix — zero Threads.LookupSandbox DDB read on unowned/unconfigured repos
  - cmd/km-github-bridge KM_GITHUB_PEER_BRIDGES → HTTPPeerRelayer wiring
affects: [100-04-doctor-and-deploy, phase-101-orphan-repo-reply]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Resolve-ownership-first: pure-config ownership match moved ahead of all I/O (thread-lookup DDB GetItem + mention scan) so the wasted read only happens on the owned path"
    - "Single-hop relay loop guard: X-KM-Relayed:1 makes a relayed request TERMINAL (process if owned, drop otherwise, never re-relay)"
    - "Typed-nil-into-interface guard: set WebhookHandler.Relayer only when peers configured, so the h.Relayer != nil dispatch guard stays correct"

key-files:
  created:
    - pkg/github/bridge/webhook_handler_phase100_test.go
  modified:
    - pkg/github/bridge/webhook_handler.go
    - pkg/github/bridge/webhook_handler_phase98_test.go
    - cmd/km-github-bridge/main.go

key-decisions:
  - "Resolve() reorder is unconditional (applies even with federation off): it is BOTH the relay hook AND the 700-repo scale fix. Byte-identity holds because a km-github-threads continuity row only ever exists for an owned repo (rows written at dispatch, which requires matched=true), so skipping the thread-lookup on the unowned path loses nothing."
  - "Set WebhookHandler.Relayer ONLY when peers were parsed — assigning a typed-nil *HTTPPeerRelayer into the PeerRelayer interface field would make h.Relayer != nil TRUE (non-nil interface holding nil pointer) and panic in Broadcast."
  - "Added a lookupCalls counter to mockGitHubThreadStore (no return-semantics change) so the GH-FED-SCALE no-wasted-read test asserts ZERO DDB read via a call-count mock — not an inference."
  - "HTTPPeerRelayer.HTTPClient given a 10s-timeout http.Client in cmd main (no existing shared client to reuse); relayBroadcastTimeout (5s) still bounds the fan-out independently."

patterns-established:
  - "Decision-table test driven by {relayed?, matched?} with a Broadcast call-count mock"
  - "Reorder regression coverage: peer-owned thread follow-up RELAYS (front door); owned thread follow-up still DISPATCHES (Phase 98 thread-bypass preserved)"

requirements-completed: [GH-FED-REORDER, GH-FED-LOOPGUARD, GH-FED-SCALE]

# Metrics
duration: 188s
completed: 2026-06-08
tasks: 3
files: 4
---

# Phase 100 Plan 03: Handle() Reorder + Federated Relay Branch Summary

Made `Resolve()` ownership-match unconditional-first in `WebhookHandler.Handle`, ahead of the Phase-98 thread-lookup and the @-mention filter; added the `Relayer PeerRelayer` field and the `!matched` 4-row loop-guard decision branch; wired the relayer in `cmd/km-github-bridge/main.go` from `KM_GITHUB_PEER_BRIDGES`. This is the behavioral heart of the phase and doubles as the 700-repo scale fix (no wasted `LookupSandbox` DDB read on unowned repos), with byte-identical dispatch — including the Phase 98 thread-bypass — preserved on the matched path.

## What Was Built

### Task 1 — RED tests (commit `79ce2a9d`)
- `pkg/github/bridge/webhook_handler_phase100_test.go`: `mockPeerRelayer` (records `broadcastCalls`); the 4-row `TestDecisionTable_RelayLoopGuard`; reorder coverage (`TestReorder_PeerOwnedThreadFollowup_Relays`, `TestReorder_OwnedThreadFollowup_Dispatches`); `TestLoopGuard_RelayedMiss_NeverRebroadcasts`; `TestNoWastedRead_UnownedRepo_ZeroLookup` + `TestNoWastedRead_OwnedRepo_PerformsLookup`.
- Extended `mockGitHubThreadStore` (in `webhook_handler_phase98_test.go`) with a `lookupCalls int` counter incremented in `LookupSandbox` — no return-semantics change, all Phase 98 tests stay green.

### Task 2 — GREEN reorder + relay branch (commit `030a5958`)
- Added `Relayer PeerRelayer` to the `WebhookHandler` struct (nil ⇒ federation off).
- Moved the `Resolve()` call + `(alias, profile, allow, matched)` capture to immediately after the PR-only filter, before thread-lookup (4b) and mention (5).
- Replaced the old `!matched → 200` with the decision branch: `relayed := req.Headers["x-km-relayed"] != ""`; relayed+miss → terminal drop (`github_relay_no_owner` WARN); unrelayed+miss → `Relayer.Broadcast` if set else silent-drop Info; both return 200.
- Matched path (thread-lookup 4b → mention 5 → allowlist → dedupe → command-pass → dispatch → 👀) unchanged in order/behavior; only `Resolve()` position and the early-exit changed.

### Task 3 — cmd wiring (commit `a98135d3`)
- `cmd/km-github-bridge/main.go`: parse `KM_GITHUB_PEER_BRIDGES` (split comma / `TrimSpace` / drop empties), build `&bridge.HTTPPeerRelayer{PeerURLs, HTTPClient: &http.Client{Timeout: 10s}}` when non-empty, dormant log line when empty.
- Set `webhookHandler.Relayer = relayer` ONLY when `relayer != nil` to avoid the typed-nil-into-interface panic. Added `net/http` import.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Typed-nil *HTTPPeerRelayer would defeat the nil-interface dispatch guard**
- **Found during:** Task 3
- **Issue:** The plan's literal `Relayer: relayer` on the struct literal would assign a nil `*bridge.HTTPPeerRelayer` into the `PeerRelayer` interface field when no peers are configured. A non-nil interface holding a nil pointer makes `h.Relayer != nil` TRUE in `Handle()`, so `Broadcast` would be called on a nil pointer and panic at `r.PeerURLs`.
- **Fix:** Declared `var relayer *bridge.HTTPPeerRelayer`, built it only when peers parsed, and set `webhookHandler.Relayer = relayer` inside `if relayer != nil { ... }` after the struct literal — keeping the federation-off interface field a true nil.
- **Files modified:** cmd/km-github-bridge/main.go
- **Commit:** a98135d3

No other deviations — the Task 1/2 behavior matched the plan exactly.

## Verification

- `go test ./pkg/github/bridge/... -count=1` → GREEN (full suite, no Phase 97/98/99 regression; Phase 98 thread-bypass tests pass).
- `go test ./pkg/github/bridge/... -run 'DecisionTable|Reorder|LoopGuard|NoWastedRead' -count=1 -v` → all 6 tests + 4 decision-table subtests PASS.
- `NoWastedRead` proves `threads.lookupCalls == 0` on the unowned-repo path and `>= 1` on the owned path via a call-count mock.
- `go build ./cmd/km-github-bridge/...` → succeeds; `KM_GITHUB_PEER_BRIDGES` + `HTTPPeerRelayer` present.
- `make build` → succeeds (km v0.4.882).

## Deploy Note

`KM_GITHUB_PEER_BRIDGES` is an env-block change → deploy with `make build-lambdas` (clean) + `km init --dry-run=false`, NOT `km init --sidecars` (the bridge Lambda's `environment.variables` block only updates on a full terragrunt apply). The TF variable + env wiring + init.go export land in plan 100-04.

## Self-Check: PASSED

All created/modified files exist on disk; all 3 task commits (79ce2a9d, 030a5958, a98135d3) present in git history.
