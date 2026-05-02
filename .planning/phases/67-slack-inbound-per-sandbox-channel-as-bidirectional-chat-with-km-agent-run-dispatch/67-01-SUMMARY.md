---
phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
plan: "01"
subsystem: profile-schema
tags: [slack, inbound, validation, schema, phase-67]
dependency_graph:
  requires: [67-00]
  provides: [notifySlackInboundEnabled-field, slack-inbound-validation-rules]
  affects: [pkg/profile/types.go, pkg/profile/validate.go, pkg/profile/schemas/sandbox_profile.schema.json]
tech_stack:
  added: []
  patterns: [bool-field-not-pointer, semantic-validation-block-extension, profile_test-external-package]
key_files:
  created: []
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/validate.go
    - pkg/profile/validate_slack_inbound_test.go
decisions:
  - "Use plain bool (not *bool) for NotifySlackInboundEnabled — matches NotifySlackPerSandbox pattern; false is the correct default with no need to distinguish unset-vs-false"
  - "Tests placed in profile_test external package to match validate_test.go convention — no need to extract validateCLISlackInbound helper"
  - "Three rules are hard errors (IsWarning=false) — misconfiguration should be caught at km validate, not silently skipped"
metrics:
  duration: "102s"
  completed: "2026-05-02"
  tasks_completed: 2
  files_modified: 4
  commits: 2
---

# Phase 67 Plan 01: notifySlackInboundEnabled Schema + Validation Summary

One-liner: boolean profile field `notifySlackInboundEnabled` with three semantic validation rules gating it on Slack outbound prerequisites.

## What Was Built

### New Field: `pkg/profile/types.go`

Added `NotifySlackInboundEnabled bool` to the `CLISpec` struct (after `SlackArchiveOnDestroy`) as a plain `bool` with `yaml:"notifySlackInboundEnabled,omitempty"`. Uses plain `bool` (not `*bool`) — same pattern as `NotifySlackPerSandbox`; `false` is the correct default and there is no need to distinguish unset from explicit `false` at the Go level.

### JSON Schema: `pkg/profile/schemas/sandbox_profile.schema.json`

Added `notifySlackInboundEnabled` property in the `spec.cli` properties block after `slackArchiveOnDestroy`:

```json
"notifySlackInboundEnabled": {
  "type": "boolean",
  "default": false,
  "description": "Enable bidirectional Slack chat (per-sandbox channel inbound). Requires notifySlackEnabled=true and notifySlackPerSandbox=true; incompatible with notifySlackChannelOverride."
}
```

Not added to any `required` array — field is optional with default `false`.

### Validation Rules: `pkg/profile/validate.go`

Three new rules added to the existing Phase 63 Slack validation block in `ValidateSemantic`, reusing in-scope helper variables (`slackOn`, `perSandbox`, `override`):

- **Rule SI1** (`notifySlackInboundEnabled: true requires notifySlackEnabled: true`) — ensures outbound transport is configured
- **Rule SI2** (`notifySlackInboundEnabled: true requires notifySlackPerSandbox: true`) — ensures 1:1 channel→sandbox routing
- **Rule SI3** (`notifySlackInboundEnabled: true is incompatible with notifySlackChannelOverride`) — prevents ambiguous routing in v1

All three are hard errors (`IsWarning: false`).

### Tests: `pkg/profile/validate_slack_inbound_test.go`

Replaced four `t.Skip` Wave 0 stubs with implemented tests in the `profile_test` external package (matching the convention in `validate_test.go`):

| Test | Scenario | Expected |
|---|---|---|
| `TestValidate_SlackInbound_RequiresSlackEnabled` | inbound=true + slackEnabled=false | error "requires notifySlackEnabled" |
| `TestValidate_SlackInbound_RequiresPerSandbox` | inbound=true + perSandbox=false | error "requires notifySlackPerSandbox" |
| `TestValidate_SlackInbound_RejectsChannelOverride` | inbound=true + channelOverride set | error "incompatible with notifySlackChannelOverride" |
| `TestValidate_SlackInbound_DefaultFalseHasNoImpact` | inbound=false (default) | no inbound-related errors |

## Commits

| Hash | Message |
|---|---|
| c6c0728 | feat(67-01): add notifySlackInboundEnabled field, JSON schema entry, and three validation rules |
| e179040 | test(67-01): replace Wave 0 stubs with real notifySlackInboundEnabled validation tests |

## Deviations from Plan

None — plan executed exactly as written. Used external `profile_test` package for tests (pattern B from the plan's implementation note) instead of extracting `validateCLISlackInbound` as a standalone function, since the existing test convention in `validate_test.go` already uses `profile.ValidateSemantic(p)` from the external package.

## Verification Results

- `go build ./pkg/profile/...` — clean
- `go vet ./pkg/profile/...` — clean
- `go test ./pkg/profile/... -run TestValidate_SlackInbound -v` — 4/4 PASS
- `go test ./pkg/profile/... -count=1` — all tests pass (no regressions)
- `go build ./...` — clean
- `make build` — Built km v0.2.456 (e179040)

## Self-Check: PASSED
