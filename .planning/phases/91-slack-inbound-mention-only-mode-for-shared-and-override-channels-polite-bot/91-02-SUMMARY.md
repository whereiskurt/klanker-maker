---
phase: 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot
plan: "02"
subsystem: compiler
tags:
  - slack
  - mention-only
  - polite-bot
  - compiler
  - notifyEnv
dependency_graph:
  requires:
    - 91-01 (CLISpec.NotifySlackInboundMentionOnly *bool field in pkg/profile/types.go)
  provides:
    - resolveMentionOnly(*profile.CLISpec) bool helper in pkg/compiler/userdata.go
    - KM_SLACK_MENTION_ONLY env var emitted into sandbox notifyEnv when Slack enabled
  affects:
    - 91-03 (bridge Lambda reads KM_SLACK_MENTION_ONLY from terraform module env)
    - All sandboxes with notifySlackEnabled: true (new env var written to /etc/profile.d/km-notify-env.sh)
tech_stack:
  added: []
  patterns:
    - boolToZeroOne existing pattern; KM_SLACK_MENTION_ONLY emits "true"/"false" string (not 0/1) to match bridge expectation
    - resolveMentionOnly follows channel-mode dispatch order from create_slack.go
key_files:
  created: []
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_mention_test.go
decisions:
  - resolveMentionOnly uses "true"/"false" string values (not 0/1) for KM_SLACK_MENTION_ONLY — bridge reads this as a string bool, not a binary flag
  - KM_SLACK_MENTION_ONLY gated on NotifySlackEnabled == &true (not just non-nil); consistent with KM_NOTIFY_SLACK_ENABLED emission at line 3968
  - Helper placed adjacent to boolToZeroOne for discoverability by future maintainers
  - TestMentionOnlyCompiler uses generateUserData + string search (same pattern as existing notify tests) rather than inspecting params struct directly
metrics:
  duration: 155s
  completed: 2026-05-30
  tasks_completed: 2
  files_modified: 2
---

# Phase 91 Plan 02: Mention-Only Compiler Resolver + KM_SLACK_MENTION_ONLY Emission Summary

**One-liner:** Compile-time `resolveMentionOnly(*CLISpec) bool` helper with Mode1/2/3 × nil/true/false truth table, emitting `KM_SLACK_MENTION_ONLY="true"|"false"` into sandbox notifyEnv when Slack is enabled.

## What Was Built

Added two pieces to `pkg/compiler/userdata.go`:

1. `resolveMentionOnly(*profile.CLISpec) bool` — a pure helper that resolves the effective mention-only boolean using the same channel-mode dispatch order as `create_slack.go`: explicit override wins, then Mode 2 (per-sandbox) defaults to false (chatty), Modes 1 and 3 default to true (polite).

2. `KM_SLACK_MENTION_ONLY` emission in the existing `notifyEnv` block (adjacent to `KM_NOTIFY_SLACK_ENABLED`). Gated on `NotifySlackEnabled == &true` — same gate as the bridge Lambda so the env var is absent from sandboxes that don't use Slack at all (backward compat: pre-91 profiles unchanged).

## Tests

`pkg/compiler/userdata_mention_test.go` (previously stubs with `t.Skip`):

- `TestResolveMentionOnly`: 10-case table (9 Mode × override combinations + nil-cli defensive edge), all passing.
- `TestMentionOnlyCompiler`: 5-case sampled e2e table asserting correct `"true"`/`"false"` value in generated userdata and key-absent when Slack disabled.

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check

### Files

- [x] `pkg/compiler/userdata.go` contains `resolveMentionOnly` and `KM_SLACK_MENTION_ONLY` emission
- [x] `pkg/compiler/userdata_mention_test.go` contains live test table (no t.Skip)

### Commits

- cab4497: feat(91-02): add resolveMentionOnly helper + 9-case live test
- a97b1cf: feat(91-02): emit KM_SLACK_MENTION_ONLY into notifyEnv when Slack enabled

### Verification

- `go vet ./pkg/compiler/...`: PASS
- `TestResolveMentionOnly` (10 cases): PASS
- `TestMentionOnlyCompiler` (5 cases): PASS
- All pre-existing passing compiler tests: PASS (zero regression)
- Pre-existing failures (`TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock`, `TestAuditHookNonBlocking`, `TestGitHubUserDataGITASKPASS`, etc.) were failing before this plan and are out of scope
- `make build`: Built km v0.3.759 (a97b1cf) — SUCCESS

## Self-Check: PASSED
