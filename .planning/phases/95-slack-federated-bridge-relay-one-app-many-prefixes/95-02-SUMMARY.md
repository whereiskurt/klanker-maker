---
phase: 95-slack-federated-bridge-relay-one-app-many-prefixes
plan: "02"
subsystem: slack-bridge
tags: [federation, relay, slack, lambda, bridge, loop-guard]
dependency_graph:
  requires: [95-01]
  provides: [SLACK-FED-RELAY, SLACK-FED-LOOP, SLACK-FED-VERIFY]
  affects: [pkg/slack/bridge, cmd/km-slack-bridge]
tech_stack:
  added: []
  patterns:
    - sync.WaitGroup for synchronous parallel HTTP fan-out
    - context.WithTimeout(2.5s) bounded broadcast
    - nil-safe optional-dependency field pattern (Relayer PeerRelayer)
    - four-row decision table at FetchByChannel miss site
key_files:
  created:
    - pkg/slack/bridge/relayer.go
    - pkg/slack/bridge/relayer_test.go
  modified:
    - pkg/slack/bridge/events_interfaces.go
    - pkg/slack/bridge/events_handler.go
    - pkg/slack/bridge/events_handler_test.go
    - cmd/km-slack-bridge/main.go
decisions:
  - "Broadcast is synchronous (sync.WaitGroup.Wait() before return) to prevent Lambda freeze from losing in-flight relay goroutines"
  - "X-KM-Relayed: 1 is the entire loop guard — a relayed miss is TERMINAL and never re-relayed"
  - "Relay site is after verifySlackSignature so the loop guard is authenticated before the drop"
  - "nil Relayer == federation off == today (byte-identical nil-invariant enforced by TestEventsHandler_NilRelayer_MissReturns200)"
  - "Failing peer is non-fatal: logged Warn + aggregated error returned to caller, which logs Warn and returns 200 regardless"
metrics:
  duration: "316s"
  completed_date: "2026-06-05"
  tasks_completed: 3
  files_modified: 6
---

# Phase 95 Plan 02: Federated Relay Engine + Handler Wiring Summary

One-liner: HTTPPeerRelayer broadcasts verbatim Slack events to peer bridges in parallel synchronous fan-out, wired via four-row decision table in EventsHandler with X-KM-Relayed:1 loop guard and nil-safe off-by-default federation.

## What Was Built

### Task 1: PeerRelayer interface + HTTPPeerRelayer (relayer.go)

Declared `PeerRelayer` interface in `events_interfaces.go` alongside the other bridge interfaces. Created `pkg/slack/bridge/relayer.go` with `HTTPPeerRelayer`:

- `Broadcast(ctx, rawBody, slackHeaders)` — parallel per-peer goroutines, each using `http.NewRequestWithContext` with a `context.WithTimeout(ctx, 2500ms)` child
- Forwards verbatim body unchanged (HMAC covers body+timestamp)
- Sets: `Content-Type: application/json`, `X-Slack-Signature`, `X-Slack-Request-Timestamp`, `X-KM-Relayed: 1`
- `sync.WaitGroup.Wait()` before returning (SYNCHRONOUS — Lambda freeze guard per Pitfall 4)
- Failing peers logged Warn; aggregated error returned; caller always returns 200

Tests (all green):
- `TestPeerRelayer_PreservesHeaders` — body + header forwarding verified against httptest.Server
- `TestPeerRelayer_Parallel` — both peers in a two-server broadcast receive the POST
- `TestPeerRelayer_BoundedTimeout` — 5s slow peer completes within 3.5s leeway
- `TestPeerRelayer_FailingPeerNonFatal` — 500 peer returns error; healthy peer still served

### Task 2: Relayer field + four-row decision table (events_handler.go)

Added `Relayer PeerRelayer` field to `EventsHandler` struct with nil-safe comment.

Replaced the single-line `return 200` at the `FetchByChannel` miss site (line ~194) with the four-row decision table:

| X-KM-Relayed? | Owns channel? | Action |
|---|---|---|
| absent | yes | process locally (fall through — unchanged) |
| absent | no | `h.Relayer.Broadcast(...)` if non-nil, else log Warn; return 200 |
| present | yes | process locally (fall through — unchanged) |
| present | no | TERMINAL drop (`slack_relay_no_owner`); Relayer never invoked; return 200 |

The relay check runs AFTER `verifySlackSignature` (line 154) so the loop guard is authenticated.

Tests (all green):
- `TestEventsHandler_FederatedRelay` — four-row table driven, each row verified for Relayer call count + SQS sends
- Loop-impossibility assertion: `tc.relayed && !tc.owns && len(calls) != 0` → test failure
- `TestEventsHandler_NilRelayer_MissReturns200` — nil Relayer + miss → 200, no SQS, no broadcast

### Task 3: Bridge cold-start wiring + SLACK-FED-VERIFY test

`wireEventsHandler()` in `cmd/km-slack-bridge/main.go` now parses `KM_SLACK_PEER_BRIDGES` after `WireMentionOnly`:
- Splits on `,`, TrimSpace, filters empties
- Only wires `eventsHandler.Relayer` when `len(peers) > 0` (empty/unset env → nil → federation off)
- Reuses `initHTTPClient` (existing 10s global; overridden per-broadcast by 2.5s context)

`TestVerifySlackSignature_Relayed` proves SLACK-FED-VERIFY:
- Computes a valid Slack HMAC signature over (timestamp, body)
- Forwards the same body+ts+sig verbatim → `verifySlackSignature` passes with the shared secret
- Sanity: tampered body fails; stale timestamp (>5 min) fails

## Commits

| Hash | Task | Description |
|------|------|-------------|
| eff6763d | 1 | PeerRelayer interface + HTTPPeerRelayer broadcast engine |
| afc87314 | 2 | Relayer field + four-row decision table at FetchByChannel miss |
| 6e1b8b71 | 3 | Wire KM_SLACK_PEER_BRIDGES into bridge cold-start + SLACK-FED-VERIFY test |

## Deviations from Plan

None — plan executed exactly as written.

## Verification

```
go build ./... && go test ./pkg/slack/bridge/... -v
```

All 130+ tests in `pkg/slack/bridge` pass. New tests: 9 (4 relayer, 4 federated relay decision table, 1 nil-invariant, 1 signature relay verify).

Key invariants confirmed by explicit tests:
- `TestEventsHandler_NilRelayer_MissReturns200` — nil Relayer is byte-identical to today
- Loop guard: relayed+miss never invokes relayer (verified in `TestEventsHandler_FederatedRelay/present+miss`)
- `TestVerifySlackSignature_Relayed` — forwarded request passes peer signature check (SLACK-FED-VERIFY)
- `TestPeerRelayer_BoundedTimeout` — Broadcast returns within 3.5s regardless of peer latency

## Self-Check: PASSED
