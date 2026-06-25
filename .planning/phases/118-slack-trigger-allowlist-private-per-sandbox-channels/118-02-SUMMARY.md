---
phase: 118-slack-trigger-allowlist-private-per-sandbox-channels
plan: "02"
subsystem: slack-bridge
tags: [feature-a, private-channel, validate, json-schema, create-channel]
dependency_graph:
  requires:
    - "118-01 CreateChannel mock signature + Private/Allow profile field stubs"
  provides:
    - "private bool threaded through CreateChannel (real signature)"
    - "is_private:true honored at per-sandbox channel creation (AC1)"
    - "km validate warns for private/allow + perSandbox:false (AC6)"
    - "JSON schema entries for notification.slack.private + inbound.allow"
  affects:
    - "pkg/slack — Client.CreateChannel signature"
    - "internal/app/cmd — SlackAPI + SlackInitAPI interfaces, call sites"
    - "pkg/profile — validate.go warn rules, JSON schema"
tech_stack:
  added: []
  patterns:
    - "private bool param defaults false at init/rotate call sites (only perSandbox create passes resolved value)"
    - "validate warns (not errors) for no-op field combinations"
key_files:
  created: []
  modified:
    - "pkg/slack/client.go (CreateChannel: is_private from param, was hardcoded false at :606)"
    - "internal/app/cmd/create_slack.go (SlackAPI interface + perSandbox call site)"
    - "internal/app/cmd/slack.go (SlackInitAPI interface + init/rotate call sites pass false)"
    - "pkg/slack/client_test.go (2 CreateChannel call sites updated)"
    - "internal/app/cmd/create_slack_test.go (TestResolveSlack_PerSandbox_PrivateChannel asserts createPrivate)"
    - "pkg/profile/validate.go (Rule S-private + Rule S-allow warnings)"
    - "pkg/profile/schemas/sandbox_profile.schema.json (private + allow, both blocks additionalProperties:false)"
decisions:
  - "Both SlackAPI (create_slack.go:87) AND SlackInitAPI (slack.go:54) re-declare CreateChannel — both updated; RESEARCH only cited the first"
  - "Schema entries are REQUIRED, not optional: both slack + inbound blocks are additionalProperties:false, so a profile using the fields would fail schema validation without them"
  - "validate WARNS (never errors) — private/allow with perSandbox:false is a no-op, not a misconfiguration that should block create"
metrics:
  completed: "2026-06-24"
  tasks_completed: 2
  files_modified: 7
---

# Phase 118 Plan 02: Feature A — Private Per-Sandbox Channel Summary

Threaded a `private bool` through the entire `CreateChannel` chain so `notification.slack.private:true` + `perSandbox:true` creates the channel as `is_private:true` (was hardcoded `false` at `pkg/slack/client.go:606`), and added the `km validate` warn rules + JSON-schema entries for both Phase 118 fields.

## What Was Done

### Task 1 — private bool through CreateChannel (AC1)
- `pkg/slack.Client.CreateChannel(ctx, name, private bool)` now passes `is_private: private` instead of the hardcoded `false`.
- Both `SlackAPI` (`create_slack.go:87`) and `SlackInitAPI` (`slack.go:54`) interface declarations updated. Init/rotate call sites pass `false`; the perSandbox create path passes the resolved `notification.slack.private`.
- Turned the Wave-1 RED `internal/app/cmd` compile state GREEN.

### Task 2 — validate warns + JSON schema (AC6)
- `validate.go` Rule S-private + Rule S-allow: WARN when `private`/`inbound.allow` set with `perSandbox:false`.
- JSON schema gained `private` (slack block) and `allow` (inbound block). Both blocks are `additionalProperties:false`, so this is required for real profiles to validate.

## Verification Summary
```
go build ./... → clean
go test ./internal/app/cmd/... -run TestResolveSlack_PerSandbox -count=1 → PASS (createPrivate asserted, AC1)
go test ./pkg/profile/... -run 'TestValidateSemantic_Slack_(Private|Allow)' → PASS (AC6 all 4)
```
Live: `km validate` on a `perSandbox:false` variant emitted both warnings.

## Commits
| Hash | Message |
|------|---------|
| 112c9e10 | feat(118-02): thread private bool through CreateChannel — AC1 GREEN |
| 0f9b614d | feat(118-02): km validate warns + JSON schema for private/allow (AC6) |

## Deviations from Plan
Recovered after a parallel-wave executor stall; completed directly. The config-struct work for Plan 04 was swept into commit 112c9e10 by parallel `git add` interleaving (functionally correct, noted in 118-04).
