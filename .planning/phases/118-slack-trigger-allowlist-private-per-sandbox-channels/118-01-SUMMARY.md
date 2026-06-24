---
phase: 118-slack-trigger-allowlist-private-per-sandbox-channels
plan: "01"
subsystem: slack-bridge
tags: [tdd, wave-0, red-tests, scaffold, allowlist, private-channels]
dependency_graph:
  requires: []
  provides:
    - "Allow field stubs on EventsHandler, SandboxRoutingInfo, SandboxMetadata"
    - "Private/Allow field stubs on profile types (NotificationSlackSpec, NotificationSlackInboundSpec)"
    - "6 CreateChannel mocks updated to (ctx, name, private bool) signature"
    - "Wave-0 RED tests for AC2/AC3/AC4/AC5/AC6/AC7/AC8"
  affects:
    - "pkg/slack/bridge — EventsHandler, SandboxRoutingInfo struct contracts"
    - "pkg/aws — SandboxMetadata struct contract"
    - "pkg/profile — NotificationSlackSpec, NotificationSlackInboundSpec struct contracts"
    - "internal/app/cmd — 6 test mocks (test-only build intentionally broken until Plan 02)"
tech_stack:
  added: []
  patterns:
    - "TDD Wave-0: RED tests define acceptance-criteria contract before implementation"
    - "SandboxMetadata comma-joined S attribute pattern (mirrors slack_mention_only)"
    - "Per-sandbox REPLACES install-level (inverted from GitHub deny-by-default)"
key_files:
  created:
    - "pkg/slack/bridge/events_handler_allowlist_test.go"
    - "pkg/aws/sandbox_dynamo_allow_test.go"
  modified:
    - "pkg/slack/bridge/events_handler.go (Allow []string added to EventsHandler)"
    - "pkg/slack/bridge/events_interfaces.go (Allow []string added to SandboxRoutingInfo)"
    - "pkg/aws/metadata.go (SlackAllow []string added to SandboxMetadata)"
    - "pkg/profile/types.go (Private bool on NotificationSlackSpec; Allow []string on NotificationSlackInboundSpec)"
    - "pkg/profile/validate_test.go (AC6 warn-rule tests appended)"
    - "internal/app/cmd/create_slack_test.go (fakeSlackAPI mock; createPrivate field captured)"
    - "internal/app/cmd/create_slack_transcript_test.go (fakeSlackAPIWithMembers mock)"
    - "internal/app/cmd/create_slack_invite_test.go (fakeSlackAPIForInvite mock)"
    - "internal/app/cmd/slack_invite_test.go (fakeSlackAPIForInvite mock)"
    - "internal/app/cmd/slack_test.go (fakeSlackInitAPI + fakeRotateAPI mocks)"
decisions:
  - "Stub fields added to all 5 structs in this plan so Wave-2 plans build against a frozen contract"
  - "internal/app/cmd test suite intentionally breaks (interface mismatch) until Plan 02 updates the real CreateChannel signature — this is the designed TDD red state"
  - "AC6b test (allow without perSandbox) also triggers an existing hard error about inbound.enabled requiring perSandbox — test only asserts on allow-related warnings, not the existing error"
  - "createPrivate bool field captured in fakeSlackAPI (create_slack_test.go) per plan spec so Plan 02 Task 1 step 7 can assert it"
metrics:
  duration: "306s"
  completed: "2026-06-24"
  tasks_completed: 3
  files_modified: 11
---

# Phase 118 Plan 01: Wave-0 TDD Scaffold — Allowlist + Private Channel Stubs Summary

Wave-0 RED test scaffold locking the Phase 118 acceptance-criteria contract in code: 5 allowlist/round-trip struct stubs + 11 named tests (RED and GREEN) across 3 packages before any Feature A/B implementation lands.

## What Was Done

### Task 1 — EventsHandler allowlist RED tests + Allow field stubs

Added `Allow []string` to `EventsHandler` (install-level) and `SandboxRoutingInfo` (per-sandbox override that REPLACES install-level). Created `pkg/slack/bridge/events_handler_allowlist_test.go` with 5 tests:

| Test | AC | Initial State |
|------|----|---------------|
| TestEventsHandler_Allowlist | AC2 | RED (AC2 sub-case "other user dropped" fails; "operator dispatched" passes) |
| TestEventsHandler_PerSandboxAllowOverrides | AC3 | RED (AC3 sub-case "U_OP dropped by per-sandbox" fails) |
| TestEventsHandler_AllowlistEmpty_EveryoneAllowed | AC4 | GREEN (everyone dispatched with no allowlist) |
| TestEventsHandler_Allowlist_ThreadBypassDoesNotExempt | AC5 | RED (U_OTHER dispatches through thread-bypass; enforcement not yet present) |
| TestEventsHandler_NoAllowlistSet_ByteIdentical | AC8 | GREEN (zero-value Allow = byte-identical to pre-118) |

### Task 2 — SandboxMetadata SlackAllow round-trip RED test + field stub

Added `SlackAllow []string` to `SandboxMetadata` (comma-joined S attribute `slack_allow` in DDB, NOT SS). Created `pkg/aws/sandbox_dynamo_allow_test.go` with 4 sub-cases:

| Sub-case | AC | Initial State |
|----------|----|---------------|
| non-empty round-trips as comma-joined S | AC7 | RED (marshalSandboxItem not yet updated) |
| nil → attribute omitted | AC7 | GREEN (omitempty) |
| empty-slice → attribute omitted | AC7 | GREEN (omitempty) |
| single-element no trailing comma | AC7 | RED (marshalSandboxItem not yet updated) |

### Task 3 — 6 CreateChannel mock updates + profile stubs + AC6 validate tests

All 6 `CreateChannel` mocks updated to the forthcoming `(ctx, name string, private bool)` signature. `fakeSlackAPI` additionally captures `createPrivate bool` for Plan 02 assertion. Added `Private bool` to `NotificationSlackSpec` and `Allow []string` to `NotificationSlackInboundSpec`. Appended 4 AC6 tests to `validate_test.go`:

| Test | AC | Initial State |
|------|----|---------------|
| TestValidateSemantic_Slack_Private_WithoutPerSandbox_Warning | AC6a | RED (warn rule not in validate.go yet) |
| TestValidateSemantic_Slack_Allow_WithoutPerSandbox_Warning | AC6b | RED (warn rule not in validate.go yet) |
| TestValidateSemantic_Slack_Private_WithPerSandbox_NoWarning | AC6c | GREEN |
| TestValidateSemantic_Slack_Allow_WithPerSandbox_NoWarning | AC6d | GREEN |

## Verification Summary

```
go vet ./pkg/slack/bridge/... ./pkg/aws/... ./pkg/profile/...  → CLEAN
pkg/slack/bridge tests (AC2/3/4/5/8): AC4+AC8 GREEN; AC2/3/5 RED [expected]
pkg/aws tests (AC7): nil+empty GREEN; non-empty RED [expected]
pkg/profile tests (AC6): AC6c+AC6d GREEN; AC6a+AC6b RED [expected]
```

## Known Intentional State

**`internal/app/cmd` test compilation is broken until Plan 02.** The 6 mocks now implement `CreateChannel(ctx, name string, private bool)` but the real `SlackAPI` interface in `create_slack.go:87` and `slack.go:54` still declare `CreateChannel(ctx, name string)`. `go vet ./internal/app/cmd/...` reports interface-mismatch errors for all 6 mock types. This is the designed Wave-0 state — the real interface change lands in Plan 02.

## Commits

| Hash | Message |
|------|---------|
| b78af279 | test(118-01): Wave-0 RED tests + Allow field stubs for EventsHandler allowlist (AC2/AC3/AC4/AC5/AC8) |
| b15a8d43 | test(118-01): Wave-0 RED test + SlackAllow field stub for SandboxMetadata round-trip (AC7) |
| d4653fa3 | test(118-01): Update 6 CreateChannel mocks to (ctx,name,private) + profile stubs + AC6 validate tests |

## Deviations from Plan

None — plan executed exactly as written.
