---
phase: 96-slack-default-router-orphan-channel-mention-reply
plan: "02"
subsystem: slack
tags: [slack, dynamodb, scatter-gather, federation, relay, claim-aware, orphan-detection]

# Dependency graph
requires:
  - phase: 96-01
    provides: cfg.Slack.DefaultRouter + KM_SLACK_DEFAULT_ROUTER + dynamodb:Scan IAM

provides:
  - PeerClaimResult and SandboxChannelInfo types for scatter-gather results
  - Updated PeerRelayer interface: Broadcast returns ([]PeerClaimResult, error)
  - HTTPPeerRelayer parses peer JSON responses; legacy/error/timeout => Claimed:true
  - DDBRunningChannelLister: Scan km-sandboxes for running sandboxes with bound channels
  - Peer-side relayed-miss returns {claimed:false, channels:[...]} JSON
  - Peer-side relayed-owned returns {claimed:true} JSON
  - RunningChannelLister and DDBScanAPI interfaces

affects:
  - 96-03 (front-door orphan reply: consumes PeerClaimResult tally + RunningChannelLister)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Rollout-safety conservative claim: any legacy/error/unparseable/timeout response => Claimed:true"
    - "DDB reserved-word alias: ExpressionAttributeNames {#s: state} for FilterExpression"
    - "Narrow interface DDBScanAPI wraps *dynamodb.Client.Scan for testability"
    - "peerRelayResponse private struct shared between relayer.go postToPeer and events_handler.go peer-side response"

key-files:
  created:
    - pkg/slack/bridge/running_channels_test.go
  modified:
    - pkg/slack/bridge/events_interfaces.go
    - pkg/slack/bridge/relayer.go
    - pkg/slack/bridge/relayer_test.go
    - pkg/slack/bridge/aws_adapters.go
    - pkg/slack/bridge/events_handler.go
    - pkg/slack/bridge/events_handler_test.go

key-decisions:
  - "Rollout safety LOCKED: any legacy 'ok', HTTP error, non-JSON, or transport timeout maps to Claimed:true — never produce false orphan in mixed-version fleet"
  - "Transport errors in postToPeer return (PeerClaimResult{}, error); goroutine converts to Claimed:true result — error never surfaces to caller"
  - "Relayed-owned path returns JSON {claimed:true} gated at the final return statement via req.Headers check — clean separation from front-door path"
  - "peerRelayResponse struct is package-private (not exported) — shared between relayer.go and events_handler.go within same package"

patterns-established:
  - "Scatter-gather result accumulation: chan PeerClaimResult (buffered len(peers)) + WaitGroup + collect-after-Wait pattern"
  - "Conservative-claim rule: when uncertain, claim=true prevents false orphan replies"

requirements-completed: [SLACK-RTR-GATHER, SLACK-RTR-SAFE]

# Metrics
duration: 8min
completed: 2026-06-05
---

# Phase 96 Plan 02: Scatter-Gather Contract + Running Channel Lister Summary

**Claim-aware scatter-gather contract: Broadcast returns []PeerClaimResult with JSON parse + rollout-safety conservative-claim rule; DDBRunningChannelLister Scans km-sandboxes; peers now return {claimed:bool,channels} JSON**

## Performance

- **Duration:** 8 min
- **Started:** 2026-06-05T00:20:48Z
- **Completed:** 2026-06-05T00:28:29Z
- **Tasks:** 3
- **Files modified:** 6 modified, 1 created

## Accomplishments

- Changed `PeerRelayer.Broadcast` return type from `error` to `([]PeerClaimResult, error)` and updated both implementers (`HTTPPeerRelayer` and `fakePeerRelayer`)
- Implemented rollout-safety rule: legacy `"ok"` body, HTTP errors, and transport/timeout errors all map to `Claimed:true` so mixed-version fleets never produce false orphan replies
- Added `DDBRunningChannelLister` with the DDB reserved-word alias pattern (`ExpressionAttributeNames {"#s":"state"}`) and full pagination support
- Peer-side relayed-miss handler now returns `{claimed:false, channels:[...]}` JSON; relayed-owned handler returns `{claimed:true}` JSON — both with `content-type: application/json`
- All 7 new test functions pass; full `go test ./pkg/slack/bridge/... -count=1 -race` green

## Task Commits

Each task was committed atomically:

1. **Task 1: Interface change + relayer scatter-gather + update ALL implementers** - `fd6680c0` (feat)
2. **Task 2: DDBRunningChannelLister — Scan adapter for running sandbox channels** - `1a2b1268` (feat)
3. **Task 3: Peer-side relayed-request response — {claimed:false,channels} on miss, {claimed:true} on own** - `86aa515c` (feat)

_All tasks used TDD: tests written (RED) before implementation (GREEN)._

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/slack/bridge/events_interfaces.go` - Added SandboxChannelInfo, PeerClaimResult, updated PeerRelayer interface, RunningChannelLister, DDBScanAPI
- `/Users/khundeck/working/klankrmkr/pkg/slack/bridge/relayer.go` - Broadcast returns []PeerClaimResult; postToPeer parses JSON with legacy/error->Claimed:true safety; added peerRelayResponse struct
- `/Users/khundeck/working/klankrmkr/pkg/slack/bridge/relayer_test.go` - New scatter-gather tests: GatherClaims, LegacyResponseClaimedTrue, HTTPErrorClaimedTrue, MixedResults; updated existing tests to new signature
- `/Users/khundeck/working/klankrmkr/pkg/slack/bridge/aws_adapters.go` - Added DDBRunningChannelLister with reserved-word alias + pagination
- `/Users/khundeck/working/klankrmkr/pkg/slack/bridge/running_channels_test.go` - New file: Filter, Pagination, ReservedWordExpr, EmptyAlias tests
- `/Users/khundeck/working/klankrmkr/pkg/slack/bridge/events_handler.go` - Added RunningChannels field; relayed-miss returns JSON channels; relayed-owned returns {claimed:true}; Broadcast call site captures results
- `/Users/khundeck/working/klankrmkr/pkg/slack/bridge/events_handler_test.go` - Updated fakePeerRelayer; added fakeRunningChannelLister; added RelayedMiss/RelayedOwned/FrontDoorUnaffected tests

## Decisions Made

- **peerRelayResponse as package-private struct:** Shared between `relayer.go` (postToPeer parse) and `events_handler.go` (peer-side response marshal). Both live in the same `bridge` package so no export needed.
- **Transport errors → Claimed:true at goroutine level:** The goroutine in Broadcast catches transport errors and pushes `Claimed:true` directly to the result channel rather than returning them to the caller as errors. This ensures the conservative-claim rule applies even for connection refused / context timeout.
- **Relayed-owned path gated at final return:** Rather than threading `isRelayed bool` through the entire Handle function, the check `req.Headers["x-km-relayed"] != ""` at the final return cleanly separates the relayed-owned response from the non-relayed path without modifying any of the steps 5–10 logic.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## Next Phase Readiness

- Plan 03 can immediately consume `[]PeerClaimResult` from the updated `Broadcast` call site (`_ = claimResults` marker in events_handler.go)
- `RunningChannelLister` interface + `DDBRunningChannelLister` implementation ready for wiring in main.go
- `peerRelayResponse` struct available for Plan 03's front-door orphan-reply tally
- All scatter-gather contract tests passing; rollout-safety rule verified

---
*Phase: 96-slack-default-router-orphan-channel-mention-reply*
*Completed: 2026-06-05*
