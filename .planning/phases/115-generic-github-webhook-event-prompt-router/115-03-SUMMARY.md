---
phase: 115-generic-github-webhook-event-prompt-router
plan: "03"
subsystem: github-bridge
tags: [github-webhooks, event-router, lambda, dedup, cooldown, dispatch]
dependency_graph:
  requires:
    - "115-01 (Wave 0 RED scaffold: webhook_handler_phase115_test.go with TestHandleEventRoute_Dispatch/Cooldown)"
    - "115-02 (EventRouter core: EventRule, EventPayload, MatchEventRule, ExpandEventTemplate, WebhookHandler.EventRules field)"
  provides:
    - "pkg/github/bridge/webhook_handler.go: GitHubEventCooldownPrefix constant, two-branch event switch, handleEventRoute method, genericEventPayload struct"
    - "cmd/km-github-bridge/main.go: KM_GITHUB_EVENTS cold-start parse + webhookHandler.EventRules wiring"
  affects:
    - "Phase 115 Plans 04-05 (init.go KM_GITHUB_EVENTS export, doctor check, manifest extension, poller Number==0 tolerance)"
tech_stack:
  added: []
  patterns:
    - "Two-branch event switch: issue_comment → existing 11-step path (byte-identical); other events + EventRules non-empty → handleEventRoute; EventRules empty → dormant drop"
    - "handleEventRoute ordering: parse generic payload → delivery-GUID dedup → MatchEventRule → cooldown gate → ExpandEventTemplate → GitHubEnvelope (Number=0, Kind=eventType) → dispatch"
    - "Delivery-GUID dedup BEFORE rule match (Pitfall 1 guard): GitHub retries with new GUIDs are caught by cooldown, identical GUIDs by dedup"
    - "gh-event-cooldown:{event}:{repo}:{action} nonce key prefix (distinct from github-delivery: and gh-router-cooldown:)"
    - "Post-construction EventRules field assignment in main.go (mirrors Relayer/DefaultRouter pattern)"
key_files:
  created: []
  modified:
    - path: "pkg/github/bridge/webhook_handler.go"
      purpose: "Added GitHubEventCooldownPrefix constant, two-branch event switch at line ~204, genericEventPayload struct, handleEventRoute method (169 lines total addition)"
    - path: "cmd/km-github-bridge/main.go"
      purpose: "Updated env-var doc comment; added KM_GITHUB_EVENTS parse block (mirrors KM_GITHUB_REPOS); post-construction webhookHandler.EventRules = eventRules"
key-decisions:
  - "handleEventRoute uses h.Resolver.ResolveByAlias (base interface) for alias warm path — SandboxAliasResolverWithStatus (status-aware) is reserved for the issue_comment 3-way dispatch which has resume semantics that autonomous events don't need"
  - "Alias empty + no sandbox found → PutSandboxCreate with empty alias (new sandbox per event); alias set + resolve error → cold-create fallback with alias (not silent drop) to avoid losing the event when alias is configured but sandbox not yet running"
  - "No 👀 reaction posted in handleEventRoute — autonomous events have no originating comment to react to (CONTEXT.md hard requirement)"
  - "genericEventPayload uses InstallField (int64 ID) matching existing IssueCommentPayload.Installation shape"
  - "Fail-open on both dedup and cooldown nonce errors (mirror issue_comment path): log error + proceed rather than drop the event"
requirements-completed: [GH-EVENT-GATING, GH-EVENT-DISPATCH, GH-EVENT-COOLDOWN]
duration: "~8min"
completed: "2026-06-15"
---

# Phase 115 Plan 03: Event Branch + handleEventRoute + Lambda Wiring Summary

**Live event routing wired into km-github-bridge: two-branch switch routes non-issue_comment webhooks to handleEventRoute (dedup → match → cooldown → envelope → dispatch) when EventRules non-empty; issue_comment path byte-identical; KM_GITHUB_EVENTS parsed at Lambda cold-start**

## Performance

- **Duration:** ~8 min
- **Started:** 2026-06-15T23:43:00Z
- **Completed:** 2026-06-15T23:51:24Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- Replaced the 4-line `issue_comment`-only guard in `Handle()` with a two-branch switch that preserves byte-identity for the issue_comment path and routes non-issue_comment events to `handleEventRoute` when `EventRules` is non-empty
- Implemented `handleEventRoute` with the critical ordering: parse generic payload → delivery-GUID dedup (before match, guards against GitHub retry storms) → MatchEventRule first-match → opt-in cooldown gate (gh-event-cooldown: nonce prefix) → ExpandEventTemplate → GitHubEnvelope (Number=0, Kind=eventType, Agent=rule.Agent) → cold PutSandboxCreate (no alias) or warm SQS.Send (alias set)
- Wired KM_GITHUB_EVENTS JSON parse at Lambda cold-start in main.go, mirroring the KM_GITHUB_REPOS pattern; absent/invalid → dormant with a log line
- `TestHandleEventRoute_Dispatch` (6 subtests) and `TestHandleEventRoute_Cooldown` (3 subtests) now GREEN; full `pkg/github/bridge/...` suite GREEN (all prior phase tests unbroken)

## Task Commits

1. **Task 1: Event branch + handleEventRoute** - `934e003a` (feat)
2. **Task 2: Lambda cold-start wiring for KM_GITHUB_EVENTS** - `182c4406` (feat)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/github/bridge/webhook_handler.go` — Added `GitHubEventCooldownPrefix` constant, two-branch event switch, `genericEventPayload` struct, `handleEventRoute` method
- `/Users/khundeck/working/klankrmkr/cmd/km-github-bridge/main.go` — Updated doc comment, added KM_GITHUB_EVENTS parse block, post-construction `webhookHandler.EventRules = eventRules`

## Decisions Made

- `handleEventRoute` uses `h.Resolver.ResolveByAlias` (base interface) for the alias warm path, not the status-aware `ResolveByAliasWithStatus`. Autonomous events don't need the stopped/paused → resume semantics that issue_comment PR threads do; simple alias resolution + cold-create fallback on miss is correct and simpler.
- Fail-open on both delivery-GUID dedup error AND cooldown nonce error (log + proceed), mirroring the issue_comment path. Dropping a legitimate event because of a transient DDB blip is worse than a rare duplicate dispatch.
- No 👀 reaction in `handleEventRoute` — autonomous events have no originating comment.

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None. Tests compiled and ran on first attempt; both task verifications passed immediately.

## Next Phase Readiness

- Plan 04 (init.go KM_GITHUB_EVENTS export + doctor check) can now wire the operator-side config surface
- Plan 05 (poller Number==0 tolerance + preamble branch on Kind) is needed before live E2E UAT (the poller currently emits "PR: #0" for event envelopes)
- `km github manifest` extension (Plans 04/05) to include configured event types beyond issue_comment

---
*Phase: 115-generic-github-webhook-event-prompt-router*
*Completed: 2026-06-15*
