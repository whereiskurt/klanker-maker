---
phase: 118-slack-trigger-allowlist-private-per-sandbox-channels
plan: "03"
subsystem: slack-bridge
tags: [feature-b, per-sandbox, ddb, round-trip, fetchbychannel, slack_allow]
dependency_graph:
  requires:
    - "118-01 SandboxMetadata.SlackAllow + SandboxRoutingInfo.Allow field stubs"
  provides:
    - "slack_allow comma-joined S attribute marshalled/unmarshalled on km-sandboxes row (AC7)"
    - "per-sandbox allow written at km create (create_slack_inbound.go)"
    - "bridge FetchByChannel reads slack_allow into SandboxRoutingInfo.Allow"
  affects:
    - "pkg/aws — SandboxMetadata marshal/unmarshal"
    - "pkg/slack/bridge — aws_adapters FetchByChannel"
    - "internal/app/cmd — create_slack_inbound write path"
tech_stack:
  added: []
  patterns:
    - "comma-joined S attribute (UpdateSandboxAttr is string-only; NOT DDB StringSet)"
    - "write only when non-empty; absent attr = use install-level default"
    - "mirrors slack_react_always (Phase 91.5) end-to-end"
key_files:
  created: []
  modified:
    - "pkg/aws/sandbox_dynamo.go (marshal/unmarshal slack_allow as comma-joined S)"
    - "pkg/slack/bridge/aws_adapters.go (FetchByChannel reads slack_allow → info.Allow)"
    - "pkg/slack/bridge/aws_adapters_test.go"
    - "internal/app/cmd/create_slack_inbound.go (persist slack_allow when inbound.allow non-empty)"
    - "internal/app/cmd/create_slack_inbound_test.go (TestCreate_SlackInboundAllowOverride)"
decisions:
  - "Stored as comma-joined S, not DDB StringSet — UpdateSandboxAttr is string-only (RESEARCH finding)"
  - "Attribute name slack_allow verified for parity across all 4 sites (marshal, unmarshal, FetchByChannel, write) — not just the test mock"
  - "Empty/nil inbound.allow → attribute NOT written (signals fall-back to install-level)"
metrics:
  completed: "2026-06-24"
  tasks_completed: 3
  files_modified: 5
---

# Phase 118 Plan 03: Feature B — Per-Sandbox Allowlist Plumbing Summary

Carried `notification.slack.inbound.allow` from the profile to the `km-sandboxes` DDB row at `km create`, round-tripped it through `SandboxMetadata` (survives pause/resume/extend), and read it in the bridge's `FetchByChannel` into `SandboxRoutingInfo.Allow` — following the Phase 91.5 `slack_react_always` chain exactly.

## What Was Done

### Task 1 — SandboxMetadata round-trip (AC7)
`slack_allow` marshalled as a comma-joined `S` attribute (`UpdateSandboxAttr` is string-only — not DDB SS) and split on unmarshal. Turned the Wave-1 RED non-empty round-trip case GREEN.

### Task 2 — FetchByChannel read
`pkg/slack/bridge/aws_adapters.go` reads the `slack_allow` GSI attribute into `SandboxRoutingInfo.Allow`; absent/empty → nil (use install-level default).

### Task 3 — write at km create
`create_slack_inbound.go` persists `slack_allow` (comma-joined) alongside `slack_react_always`, only when `inbound.allow` is non-empty. Added `TestCreate_SlackInboundAllowOverride` (nil/empty → no write; single/multi → comma-joined).

## Verification Summary
```
go test ./pkg/aws/... -run TestSandboxMetadata_SlackAllow_RoundTrip → PASS (AC7)
go test ./internal/app/cmd/... -run TestCreate_SlackInboundAllowOverride → PASS
4-site slack_allow attr-name parity verified by grep
```
Live: `km-sandboxes` row for the UAT sandbox showed `slack_allow=U0B0162H1GX`; AC3 live test proved the bridge reads it.

## Commits
| Hash | Message |
|------|---------|
| 1fc99339 | feat(118-03): slack_allow marshal + unmarshal (comma-joined S round-trip) |
| e592ccb7 | feat(118-03): FetchByChannel reads slack_allow → SandboxRoutingInfo.Allow |
| 2cc3c0df | feat(118-03): write per-sandbox slack_allow at km create (AC7 path) |

## Deviations from Plan
Recovered after a parallel-wave executor stall; Task 3 test (`TestCreate_SlackInboundAllowOverride`) was added during recovery (agent stalled right before writing it).
