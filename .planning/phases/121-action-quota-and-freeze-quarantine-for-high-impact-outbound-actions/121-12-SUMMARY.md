---
phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions
plan: 12
subsystem: api
tags: [quota, freeze, dynamodb, slack-bridge, h1-bridge, tdd, gap-closure]

# Dependency graph
requires:
  - phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions
    provides: "Quota enforcement in Slack/H1 bridges (BreachBlock/BreachWarn); FreezeSandboxDynamo already exists in pkg/aws"

provides:
  - "Freezer interface in pkg/slack/bridge (FreezeSandbox method; nil ⇒ dormant)"
  - "Freezer interface in pkg/h1/bridge (FreezeSandbox method; nil ⇒ dormant)"
  - "DynamoFreezer adapter in each bridge's aws_adapters.go wrapping kmaws.FreezeSandboxDynamo"
  - "Auto-latch: BreachFreeze trip in Slack checkQuota calls h.Freezer.FreezeSandbox with by=auto:<action>:<window>"
  - "Auto-latch: BreachFreeze trip in H1 dispatch path calls h.Freezer.FreezeSandbox with by=auto:h1_comment:<window>"
  - "BreachBlock unchanged (block without quarantine); BreachWarn unchanged (warn without block)"
  - "Fail-soft: freeze error is Warn-logged but action still blocked (no silently-passing turns)"

affects:
  - "BRG-01/H1-01 auto-on-breach freeze path (CONTEXT.md §7 decision 8)"
  - "events_handler.go frozen gate — now reachable from auto-breach path (action_frozen=true finally written)"
  - "km status/km list FROZEN display — now truthful for auto-freeze events"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Freezer interface pattern: nil-safe dormant field on bridge Handler structs; concrete adapter in aws_adapters.go"
    - "updateOnlyMetaClient adapter: narrows *dynamodb.Client to kmaws.SandboxMetadataAPI for UpdateItem-only callers (panic on unused methods)"
    - "fail-soft freeze: auto-freeze error logged as Warn; action remains blocked regardless of latch write success"
    - "by convention: auto-on-breach freeze writes by=auto:<action>:<window> for auditability in km status"

key-files:
  created: []
  modified:
    - pkg/slack/bridge/handler.go
    - pkg/slack/bridge/aws_adapters.go
    - pkg/slack/bridge/handler_quota_test.go
    - pkg/h1/bridge/webhook_handler.go
    - pkg/h1/bridge/aws_adapters.go
    - pkg/h1/bridge/quota_test.go

key-decisions:
  - "Fail-soft freeze: if FreezeSandbox returns an error, log Warn but still block the action — the action must stay denied even if the latch write transiently fails"
  - "Nil-dormant Freezer: nil ⇒ BreachFreeze blocks but does not latch; backward-compatible with any handler constructed without Freezer"
  - "Separate BreachBlock and BreachFreeze cases in switch: BreachBlock never quarantines (only denies for window); BreachFreeze auto-latches"
  - "updateOnlyMetaClient/h1UpdateOnlyMetaClient adapters: panic on unused SandboxMetadataAPI methods to make any accidental call loud"
  - "Production main.go wiring of Handler.Freezer is out of scope (pre-existing latent deploy-surface item; Quota/Limits themselves also not wired in main.go)"

patterns-established:
  - "Freezer interface: define per-bridge-package (not shared) to keep bridge packages decoupled"
  - "DynamoFreezer: wraps kmaws.FreezeSandboxDynamo via updateOnlyMetaClient adapter rather than duplicating UpdateItem logic"

requirements-completed: [BRG-01, BRG-02, BRG-03, H1-01]

# Metrics
duration: 5min
completed: 2026-06-27
---

# Phase 121 Plan 12: Auto-Freeze-on-BreachFreeze Bridge Latch Summary

**Freezer interface + DynamoFreezer adapter wired into Slack and H1 bridge checkQuota paths so onBreach:freeze trips actually write action_frozen=true to km-sandboxes via atomic UpdateItem (by="auto:<action>:<window>"), closing GAP-2 from the phase verifier**

## Performance

- **Duration:** 5 min
- **Started:** 2026-06-27T14:52:19Z
- **Completed:** 2026-06-27T14:57:29Z
- **Tasks:** 2 (TDD)
- **Files modified:** 6

## Accomplishments
- Slack bridge BreachFreeze now auto-latches action_frozen=true via Freezer interface (BreachBlock unchanged — deny-for-window without quarantine)
- H1 bridge BreachFreeze now auto-latches action_frozen=true via Freezer interface (same semantics)
- DynamoFreezer concrete adapter added to both bridges' aws_adapters.go, wrapping kmaws.FreezeSandboxDynamo with a narrow updateOnlyMetaClient bridge
- 6 new TDD tests across both bridges asserting freeze-only auto-latch (FreezeTrip calls FreezeSandbox; BlockTrip/WarnTrip do NOT)
- The "Sandbox is now frozen" notice in the Slack bridge is now truthful — the DDB latch is actually written before the notice is posted

## Task Commits

Each task was committed atomically:

1. **Task 1: Slack bridge Freezer interface + adapter + auto-latch** - `5810c326` (feat)
2. **Task 2: H1 bridge Freezer interface + adapter + auto-latch** - `f198a1bb` (feat)

## Files Created/Modified
- `pkg/slack/bridge/handler.go` - Freezer interface definition; Freezer field on Handler; checkQuota BreachFreeze/BreachBlock split with auto-latch
- `pkg/slack/bridge/aws_adapters.go` - DynamoFreezer + updateOnlyMetaClient adapter; compile-time var _ Freezer = (*DynamoFreezer)(nil) check
- `pkg/slack/bridge/handler_quota_test.go` - fakeFreezer; TestQuotaRecord_FreezeTrip_AutoLatches; TestQuotaRecord_BlockTrip_NoFreeze; TestQuotaRecord_WarnTrip_NoFreeze
- `pkg/h1/bridge/webhook_handler.go` - Freezer interface definition; Freezer field on WebhookHandler; dispatch BreachFreeze/BreachBlock split with auto-latch
- `pkg/h1/bridge/aws_adapters.go` - kmaws import added; DynamoFreezer + h1UpdateOnlyMetaClient adapter; compile-time check
- `pkg/h1/bridge/quota_test.go` - fakeH1Freezer; TestQuotaRecord_H1_FreezeTrip_AutoLatches; TestQuotaRecord_H1_BlockTrip_NoFreeze

## Decisions Made
- **Nil-dormant Freezer field:** A nil Freezer means BreachFreeze still blocks the action (returns 429 / skips SQS) but does not write the latch. This is backward-compatible and avoids a panic on pre-production-wiring builds.
- **Fail-soft on freeze error:** Latch write failure is Warn-logged but the action remains blocked. The quota enforcement must not be bypassed because a DDB write transiently fails.
- **Separate BreachBlock and BreachFreeze cases:** BreachBlock is an explicit "deny for the window" policy with no quarantine intent; combining it with BreachFreeze in one case was the root of the gap.
- **updateOnlyMetaClient/h1UpdateOnlyMetaClient adapters:** FreezeSandboxDynamo is typed against kmaws.SandboxMetadataAPI (wide interface) but only calls UpdateItem. Adapter satisfies the full interface; unused methods panic to surface accidental misuse.
- **Production main.go wiring out of scope:** Handler.Freezer is not wired in production main.go (same as Quota/Limits which are also not wired there per the plan note). The interface + adapter + call-site + tests are the gap-closure deliverable per plan spec.

## Deviations from Plan
None - plan executed exactly as written.

## Issues Encountered
- H1 bridge aws_adapters.go did not import `kmaws` (the Slack bridge had it; H1 bridge did not). Added the import as part of Task 2 — this was the expected pattern per the plan's "check the file header" note.
- DeleteItem method in h1UpdateOnlyMetaClient initially had a copy-paste error using wrong variable names; caught by compile check and fixed before any test run.

## User Setup Required
None — this is a pure bridge Lambda code change. Deploy = `make build-lambdas` + `km init --slack` / `km init --h1` to cold-start the bridge Lambdas with the new code. No TF/IAM change. No sandbox recreate needed (freeze write targets existing km-sandboxes row).

## Next Phase Readiness
- GAP-2 (BreachFreeze auto-latch) is now closed. The onBreach:freeze path finally writes action_frozen=true, making the events_handler.go frozen-dispatch gate reachable from auto-breach (not just km freeze CLI).
- GAP-1 (breached_at never written by pkg/quota.atomicIncrement) is addressed by Plan 121-11 (separate plan per the verifier). These two gaps are independent.
- The Freezer field is ready for main.go wiring whenever Quota/Limits are also wired (both remain latent deploy-surface items per the plan's out-of-scope note).

---
*Phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions*
*Completed: 2026-06-27*
