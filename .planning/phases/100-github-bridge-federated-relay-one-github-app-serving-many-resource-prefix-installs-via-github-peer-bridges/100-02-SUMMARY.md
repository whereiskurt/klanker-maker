---
phase: 100-github-bridge-federated-relay-one-github-app-serving-many-resource-prefix-installs-via-github-peer-bridges
plan: 02
subsystem: api
tags: [github-bridge, federated-relay, hmac, lambda, webhook, fire-and-forget]

# Dependency graph
requires:
  - phase: 95-slack-federated-relay
    provides: HTTPPeerRelayer structure (synchronous wg.Wait, bounded context, parallel POSTs, X-KM-Relayed loop guard)
  - phase: 97-github-bridge
    provides: VerifyGitHubSignature HMAC verify + WebhookHandler scaffold the relayer integrates with
provides:
  - PeerRelayer interface (fire-and-forget, plain-error Broadcast — Phase-95-era shape, no claim machinery)
  - HTTPPeerRelayer impl that POSTs the verbatim webhook to all peer bridges in parallel under a bounded context
  - relayBroadcastTimeout (5s) bounded-context constant
  - relayer_test.go unit coverage (headers, parallel, bounded ctx, failing-peer non-fatal, empty no-op, RelayedVerify HMAC re-verify)
affects: [100-03-webhook-handler-wiring, 100-cmd-main-relayer-build, phase-101-orphan-repo-reply]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Fire-and-forget peer relay: synchronous wg.Wait() before return (Lambda-freeze safe), bounded context, parallel POSTs, verbatim body + forwarded auth/routing headers + X-KM-Relayed:1 loop guard, failing peer non-fatal"
    - "Deliberate de-scoping of Phase-96 claim machinery: Broadcast returns plain error (orphan-reply scatter-gather deferred to Phase 101)"

key-files:
  created:
    - pkg/github/bridge/relayer.go
    - pkg/github/bridge/relayer_test.go
  modified:
    - pkg/github/bridge/interfaces.go

key-decisions:
  - "PeerRelayer.Broadcast returns plain error (NOT []PeerClaimResult) — copied the Phase-95-era Slack relayer SHAPE, dropped the Phase-96 claim machinery (peerRelayResponse/PeerClaimResult/response-body parsing) since the orphan-repo reply is deferred to Phase 101"
  - "relayBroadcastTimeout = 5s named const (GitHub ~10s ack window leaves ample headroom)"
  - "ghHeaders read with lowercase keys (x-hub-signature-256 / x-github-event / x-github-delivery) because Lambda Function URL headers are lowercased upstream"

patterns-established:
  - "Pattern: compile-time interface assertion `var _ PeerRelayer = (*HTTPPeerRelayer)(nil)`"
  - "Pattern: bounded-context httptest test releases its slow handler on teardown (defer close(done) before defer Close) so httptest.Server.Close() returns promptly"

requirements-completed: [GH-FED-RELAY, GH-FED-VERIFY]

# Metrics
duration: 4min
completed: 2026-06-08
---

# Phase 100 Plan 02: GitHub PeerRelayer Summary

**Fire-and-forget HTTPPeerRelayer that broadcasts a verbatim GitHub webhook (body + X-Hub-Signature-256/X-GitHub-Event/X-GitHub-Delivery + X-KM-Relayed:1) to all peer bridges in parallel under a 5s bounded context, synchronously (wg.Wait), tolerating failing peers — Phase-95-era shape with NO claim machinery.**

## Performance

- **Duration:** ~4 min
- **Started:** 2026-06-08T12:17:37Z
- **Completed:** 2026-06-08T12:21:34Z
- **Tasks:** 2 (TDD: RED + GREEN)
- **Files modified:** 3 (2 created, 1 modified)

## Accomplishments
- `PeerRelayer` interface with a single fire-and-forget method `Broadcast(ctx, rawBody []byte, ghHeaders map[string]string) error` — plain error, no scatter-gather return.
- `HTTPPeerRelayer` impl: empty-no-op dormancy, bounded child context (`relayBroadcastTimeout = 5s`), parallel per-peer goroutines, verbatim body forward, three GitHub headers forwarded verbatim + `X-KM-Relayed: 1` loop guard, `Content-Type: application/json`, `wg.Wait()` before return (Lambda-freeze safe), failing/slow peer logged and non-fatal.
- Full unit coverage with an `httptest` server (no AWS): header forwarding, all-peers parallel fan-out, failing-peer non-fatal, bounded-context (slow peer does not hang Broadcast), empty no-op, and `RelayedVerify` (forwarded sig re-verifies via `VerifyGitHubSignature` over the verbatim body).
- Compile-time assertion `var _ PeerRelayer = (*HTTPPeerRelayer)(nil)`.

## Task Commits

Each task was committed atomically (TDD):

1. **Task 1: RED — relayer unit tests** - `e4d5c276` (test)
2. **Task 2: GREEN — PeerRelayer interface + HTTPPeerRelayer impl** - `56be629b` (feat)

**Plan metadata:** (final docs commit)

## Files Created/Modified
- `pkg/github/bridge/relayer.go` - HTTPPeerRelayer + relayBroadcastTimeout + Broadcast (fire-and-forget) + compile-time assertion (created)
- `pkg/github/bridge/relayer_test.go` - RED-first unit tests for the relayer + RelayedVerify HMAC re-verify (created)
- `pkg/github/bridge/interfaces.go` - added PeerRelayer interface (plain-error Broadcast) (modified)

## Decisions Made
- **Plain-error Broadcast, no claim machinery.** Per the RESEARCH anti-pattern, copied the Phase-95-era Slack relayer STRUCTURE (synchronous wg.Wait, bounded context, parallel POSTs, X-KM-Relayed loop guard) but dropped the Phase-96 claim-result type / peer-response struct / response-body parsing. The orphan-repo reply (claim-aware scatter-gather) is deferred to Phase 101.
- **Comment wording avoids the literal `PeerClaimResult` token** so the plan's `! grep -q "PeerClaimResult"` gate passes cleanly while the intent (deliberate omission) stays documented.
- **5s bounded timeout** as a named const (`relayBroadcastTimeout`), comfortably under GitHub's ~10s ack window.

## Deviations from Plan

None - plan executed exactly as written. (One minor in-task test-robustness fix made during Task 2: the bounded-context test's slow handler is now released on teardown via `defer close(done)` so `httptest.Server.Close()` returns promptly instead of blocking on its 30s timer; this kept the relayer test runtime at ~5s. Committed as part of the GREEN commit `56be629b`.)

## Issues Encountered
- The bounded-context test initially took ~30s because `httptest.Server.Close()` blocked on the slow handler's 30s sleep (the client request was correctly cancelled by the 5s bound, but the server handler kept sleeping). Resolved by signalling the handler to release on test teardown. Broadcast itself was always correctly bounded (returns in ~5s).

## User Setup Required
None - no external service configuration required. This plan delivers the pkg-level relay engine + interface in isolation (no AWS, no Lambda env, no config). Wiring into webhook_handler.go and cmd/main.go is plan 100-03.

## Next Phase Readiness
- `PeerRelayer` interface + `HTTPPeerRelayer` ready for plan 100-03 to add a `Relayer PeerRelayer` field on `WebhookHandler` and call `Broadcast` in the `!matched` branch, and for cmd/km-github-bridge/main.go to build it from the parsed `KM_GITHUB_PEER_BRIDGES` peer list.
- No blockers.

## Self-Check: PASSED

- Files verified on disk: relayer.go, relayer_test.go, interfaces.go — all FOUND
- Commits verified in git log: e4d5c276 (test), 56be629b (feat) — all FOUND

---
*Phase: 100-github-bridge-federated-relay*
*Completed: 2026-06-08*
