---
phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator
plan: 05
subsystem: slack
tags: [slack, cobra, invite, connect, dry-run, exit-codes, tdd]

requires:
  - phase: 72-04
    provides: "EnsureMemberByEmail orchestrator in pkg/slack/invite.go; Prompter/InviteAPI interfaces"

provides:
  - "km slack invite <email> cobra command with --external, --channel, --dry-run flags"
  - "RunSlackInvite + SlackInviteOpts exported for testability"
  - "ConnectFallbackPrompter (stdin-backed slack.Prompter implementation)"
  - "SlackAPI interface extended with LookupUserByEmail + InviteUserToChannelStrict"
  - "Slack SlackAPI field added to SlackCmdDeps"
  - "Channel resolution: ID passthrough, name lookup via FindChannelByName, SSM default"

affects:
  - 72-06 (km slack init refactor can reuse ConnectFallbackPrompter pattern)
  - 72-07 (km create operator-invite loop shares same SlackAPI interface extension)

tech-stack:
  added: []
  patterns:
    - "Exit-code semantics via ExitCodeError{Code:2} for SkippedExternal (reuses existing cmd pattern)"
    - "SlackAPI interface option-a extension: add methods to interface, update all fakes"
    - "Dry-run probe: DryRun=true in EnsureMemberOpts; orchestrator guarantees zero writes"
    - "Defensive JoinChannel before invite (Pitfall 2 mitigation), skip under --dry-run"
    - "channelIDPattern regexp (^C[A-Z0-9]+$) for Slack channel ID detection"

key-files:
  created:
    - internal/app/cmd/slack_invite.go
  modified:
    - internal/app/cmd/slack_invite_test.go
    - internal/app/cmd/slack.go
    - internal/app/cmd/create_slack.go

key-decisions:
  - "Used ExitCodeError (existing create_prompt.go type) for SkippedExternal exit code 2 — no new type needed; root.go already handles it"
  - "Extended SlackAPI interface (option a) with LookupUserByEmail + InviteUserToChannelStrict; fakes in create_slack_test.go were already updated (linter auto-added stubs)"
  - "ConnectFallbackPrompter uses direct bufio.Scanner on stdin, not wrapping SlackPrompter.Confirm — SlackPrompter only has PromptString/PromptSecret (no Confirm method); avoids widening the existing interface"
  - "isStdinInteractive() gates Interactive=true so non-TTY invocations (CI, piped) never prompt"

patterns-established:
  - "invite command wires: channel resolve → JoinChannel (defensive, skip on dry-run) → EnsureMemberByEmail → result switch"

requirements-completed:
  - VALIDATION-Layer-3

duration: 18min
completed: 2026-05-29
---

# Phase 72 Plan 05: km slack invite Command Summary

**`km slack invite` command with native/Connect auto-detection, --dry-run probe, and stdin Prompter — wires the Phase 72-04 orchestrator for ad-hoc operator use**

## Performance

- **Duration:** ~18 min
- **Started:** 2026-05-29T19:00:00Z
- **Completed:** 2026-05-29T19:18:00Z
- **Tasks:** 1
- **Files modified:** 4

## Accomplishments

- `km slack invite <email>` registered under `km slack`; handles native member invite and Slack Connect fallback
- `--dry-run` probe is read-only (zero JoinChannel/InviteStrict/InviteShared calls); prints classification to stderr; exits 0
- `--external` flag skips lookup and forces Connect invite with no prompt
- `--channel` flag accepts either a name (resolved via FindChannelByName) or a Slack channel ID (^C[A-Z0-9]+$ — used directly)
- Default channel reads SSM `{prefix}slack/shared-channel-id` when `--channel` is omitted
- Defensive `JoinChannel` before invite (Pitfall 2 mitigation from CONTEXT.md); skipped under `--dry-run`
- Exit codes: 0 (Invited*/AlreadyMember/dry-run), 1 (Failed), 2 (SkippedExternal)
- 8 Layer 3 tests all PASS

## Task Commits

1. **Task 1: Implement newSlackInviteCmd + RunSlackInvite + ConnectFallbackPrompter** - `82a1af4` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified

- `internal/app/cmd/slack_invite.go` — RunSlackInvite, SlackInviteOpts, ConnectFallbackPrompter, newSlackInviteCmd, resolveInviteChannel, isStdinInteractive
- `internal/app/cmd/slack_invite_test.go` — 8 Layer 3 tests (HappyPath, ExternalFlag, ChannelByName, ChannelByID, DefaultChannelFromSSM, SkippedExternalExitCode, DryRun, FailedExitCode)
- `internal/app/cmd/slack.go` — Added `Slack SlackAPI` field to SlackCmdDeps; registered newSlackInviteCmd
- `internal/app/cmd/create_slack.go` — Extended SlackAPI with LookupUserByEmail + InviteUserToChannelStrict

## Decisions Made

**SlackAPI interface extension (option a):** Extended the cmd-level `SlackAPI` with `LookupUserByEmail(ctx, email) (userID, bool, error)` and `InviteUserToChannelStrict(ctx, channelID, userID) error`. The fakes in `create_slack_test.go` were automatically updated (linter added no-op stubs). All existing tests continue to pass. Option b (cast to `*slack.Client`) was rejected for test ergonomics.

**Exit-code plumbing:** Used `*ExitCodeError{Code:2}` (existing type in `create_prompt.go`) for `SkippedExternal`. `root.go`'s `Execute()` already handles `*ExitCodeError` via `errors.As` → `os.Exit(exitErr.Code)`. No new types needed; full proper exit code plumbing as specified.

**ConnectFallbackPrompter implementation:** Uses `bufio.Scanner` on `os.Stdin` directly rather than wrapping `SlackPrompter` (which has `PromptString`/`PromptSecret` but no `Confirm` method). Avoids widening the existing `SlackPrompter` interface. The prompt text verbatim:
```
{email} is not a member of this Slack workspace.
Send a Slack Connect invite (requires Pro Slack workspace)? [y/N]:
```

**--dry-run output wording verbatim:**
- Lookup hit: `[dry-run] {email} is a workspace member — would invite via conversations.invite to {channelID}`
- ForceExternal: `[dry-run] would send a Slack Connect invite to {email} for {channelID} (--external)`
- Lookup miss: `[dry-run] {email} is NOT a workspace member — would require a Slack Connect invite (re-run with --external, without --dry-run, to send)`

**Dry-run zero-write confirmation:** Verified in `TestSlackInvite_DryRun` — asserts `len(joinCalls)==0`, `len(inviteStrictCalls)==0`, `len(inviteSharedCalls)==0` for both lookup-hit and lookup-miss cases.

**Note for Plan 72-06:** `RunSlackInit` can be refactored to call `EnsureMemberByEmail` using the same `ConnectFallbackPrompter` introduced here. The `SlackCmdDeps.Slack` field (now wired) gives the init command access to the full `SlackAPI` (which satisfies `slack.InviteAPI`) without changes to `buildSlackCmdDeps`.

## Deviations from Plan

None — plan executed exactly as written.

Pre-existing unrelated test failure noted: `TestUnlockCmd_RequiresStateBucket` fails with expired AWS SSO credentials (makes a real DynamoDB call). This is an infrastructure/auth issue predating this plan, not caused by any changes here. Logged to `deferred-items.md`.

## Issues Encountered

- `FindChannelByName` in the plan template showed `(string, bool, error)` return but actual `*slack.Client` returns `(string, error)` — empty string signals "not found". Fixed `resolveInviteChannel` to use the real signature.
- `SlackPrompter` interface does not have a `Confirm` method — `ConnectFallbackPrompter` implemented using direct `bufio.Scanner` instead of wrapping the inner prompter.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- `km slack invite` command is live; operators can run it immediately after `km slack init`
- `SlackAPI` interface extended; 72-07's `create_slack.go` operator-invite refactor has the interface it needs
- `ConnectFallbackPrompter` pattern available for 72-06's `km slack init` refactor
- Pre-existing `TestUnlockCmd_RequiresStateBucket` SSO failure is unrelated to this plan

---
*Phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator*
*Completed: 2026-05-29*
