---
phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator
plan: "04"
subsystem: slack
tags: [slack, invite, orchestrator, tdd]
dependency_graph:
  requires: [72-01]
  provides: [EnsureMemberByEmail, InviteAPI, Prompter, EnsureMemberOpts, EnsureMemberResult, ErrAlreadyInChannel, InviteUserToChannelStrict]
  affects: [72-05, 72-06, 72-07]
tech_stack:
  added: []
  patterns: [sentinel-error, narrow-interface, typed-result-enum, tdd]
key_files:
  created:
    - pkg/slack/invite.go
  modified:
    - pkg/slack/client.go
    - pkg/slack/client_invite_test.go
    - pkg/slack/invite_test.go
decisions:
  - "Option 1 (ErrAlreadyInChannel sentinel + InviteUserToChannelStrict) chosen over Option 2 (typed result struct): minimum-surface change, mirrors Go sentinel-error idiom, lets InviteAPI keep a single InviteUserToChannel method, and existing public callers are unaffected"
  - "Interactive=true takes precedence over AutoConnect on a lookup miss — AutoConnect only governs the non-interactive path"
  - "DryRun returns InvitedDirect on lookup hit (AlreadyMember not detectable without write attempt)"
metrics:
  duration: "~5 minutes"
  completed: "2026-05-29T19:05:08Z"
  tasks_completed: 2
  files_changed: 4
---

# Phase 72 Plan 04: EnsureMemberByEmail Orchestrator Summary

**One-liner:** EnsureMemberByEmail orchestrator unifying lookup/invite/Connect into a typed-result function with narrow mockable interfaces and 10 Layer 2 tests.

## What Was Built

`pkg/slack/invite.go` — the unified invite primitive that three Wave 3 plans (`km slack init`, `km slack invite`, `km create`) reuse. The orchestrator is pure: no HTTP, no SSM, no cobra.

`pkg/slack/client.go` additions:
- `ErrAlreadyInChannel` sentinel var (use `errors.Is`)
- `InviteUserToChannelStrict` — strict variant that returns the sentinel on `already_in_channel`

## Orchestrator Behavior Matrix

| Condition | Result |
|-----------|--------|
| `ForceExternal=true` → InviteShared OK | `InvitedConnect` |
| `ForceExternal=true` → InviteShared `not_allowed_token_type` | `Failed` + Pro-tier hint |
| Lookup hit → InviteUserToChannelStrict nil | `InvitedDirect` |
| Lookup hit → InviteUserToChannelStrict `ErrAlreadyInChannel` | `AlreadyMember` |
| Lookup hit → other invite error | `Failed` |
| Lookup miss + `Interactive=false` + `AutoConnect=false` | `SkippedExternal` |
| Lookup miss + `Interactive=false` + `AutoConnect=true` | `InvitedConnect` (no prompt) |
| Lookup miss + `Interactive=true` + prompter declines | `SkippedExternal` |
| Lookup miss + `Interactive=true` + prompter approves | `InvitedConnect` |
| Lookup miss + `Interactive=true` + `Prompter=nil` | `Failed` |
| `DryRun=true` + lookup hit | `InvitedDirect` (no write) |
| `DryRun=true` + lookup miss | `SkippedExternal` (no write/prompt) |
| `DryRun=true` + `ForceExternal=true` | `InvitedConnect` (no write/lookup) |

Precedence: `ForceExternal` > lookup; on miss `Interactive=true` > `AutoConnect`.

## Seam Choice: Option 1 (Sentinel Error)

Option 1 was selected: `ErrAlreadyInChannel` sentinel + `InviteUserToChannelStrict`.

Rationale:
- Minimum-surface change — one sentinel var + one method added to `client.go`
- Mirrors standard Go sentinel idiom (`io.EOF`, `sql.ErrNoRows`)
- `InviteAPI` interface has a single `InviteUserToChannel`-style method returning the sentinel; callers use `errors.Is`
- Existing public `InviteUserToChannel` (idempotent, swallows the sentinel) is unchanged — Plan 72-01 tests remain green
- Option 2 (typed result struct as method return) would have required changing `InviteAPI`'s method signature and updating all existing callers

## Public API Surface (Wave 3 callers reference)

From `pkg/slack/invite.go`:
- `EnsureMemberByEmail(ctx, api InviteAPI, channelID, email string, opts EnsureMemberOpts) (EnsureMemberResult, error)`
- `type Prompter interface { ConfirmConnect(email string) (bool, error) }`
- `type InviteAPI interface { LookupUserByEmail; InviteUserToChannelStrict; InviteShared }`
- `type EnsureMemberOpts struct { ForceExternal, Interactive, AutoConnect, DryRun bool; Prompter Prompter }`
- `type EnsureMemberResult int`
- Constants: `InvitedDirect`, `InvitedConnect`, `AlreadyMember`, `SkippedExternal`, `Failed`

From `pkg/slack/client.go`:
- `var ErrAlreadyInChannel = errors.New("slack: user already in channel")`
- `func (c *Client) InviteUserToChannelStrict(ctx, channelID, userID string) error`

Compile-time assertion at bottom of `invite.go`:
```go
var _ InviteAPI = (*Client)(nil)
```

## Notes for Wave 3 Plans

### Plan 72-05 (km slack invite)
The cmd-side stdin `Prompter` implementation goes in `slack_invite.go`. `km slack invite` ad-hoc keeps `Interactive=true` (prompt) / `--external` (`ForceExternal=true`). It does NOT set `AutoConnect`. A `--dry-run` flag sets `DryRun=true` and prints the classification ("would invite natively" / "would send Connect invite" / "not a member") without sending anything.

### Plan 72-06 (km slack init refactor)
Refactor `RunSlackInit` to call `EnsureMemberByEmail` with `Interactive=true`. The existing direct `InviteShared` call is replaced by the orchestrator. The operator is prompted before Connect is used. The cmd-side `Prompter` adapter wraps the existing `SlackPrompter.Confirm` method.

### Plan 72-07 (km create Slack wiring)
`km create` calls with `Interactive=false`. The primary operator invite uses `AutoConnect=true` unconditionally. The additional-folks loop uses `AutoConnect` derived from `spec.cli.useSlackConnect` (nil → true). `SkippedExternal` and `Failed` both warn; both are non-fatal. No prompt is shown during `km create`.

## Deviations from Plan

None — plan executed exactly as written.

The plan noted `slackOK(t, map[string]any{...})` in the test template (with a `t` arg), but the existing helper `slackOK(map[string]any)` in `client_test.go` takes no `t`. Tests were written with the correct existing signature.

## Self-Check: PASSED

All files confirmed present:
- `pkg/slack/invite.go` — FOUND
- `pkg/slack/invite_test.go` — FOUND
- `pkg/slack/client_invite_test.go` — FOUND

Commits confirmed:
- `8424879` feat(72-04): add ErrAlreadyInChannel sentinel + InviteUserToChannelStrict
- `41ce4f7` feat(72-04): implement EnsureMemberByEmail orchestrator + Layer 2 tests
