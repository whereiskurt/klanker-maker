---
phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator
plan: "07"
subsystem: slack
tags: [slack, invite, EnsureMemberByEmail, km-create, corporate-workspace, per-sandbox]

requires:
  - phase: 72-04
    provides: "EnsureMemberByEmail orchestrator in pkg/slack/invite.go"
  - phase: 72-02
    provides: "NotifySlackInviteEmails + UseSlackConnect fields in CLISpec"

provides:
  - "Operator invite in resolveSlackChannel Mode 2 routed through EnsureMemberByEmail (AutoConnect=true unconditional)"
  - "Additional-folks loop over cli.NotifySlackInviteEmails with AutoConnect gated by UseSlackConnect"
  - "slackInviteResultWarn helper for fail-soft warn-vs-log dispatch"
  - "SlackAPI interface extended with LookupUserByEmail + InviteUserToChannelStrict"
  - "8 Layer 7 tests covering all invite outcome paths"

affects:
  - "72-09 (docs): notifySlackInviteEmails / useSlackConnect documentation"
  - "any plan that uses resolveSlackChannel or SlackAPI interface"

tech-stack:
  added: []
  patterns:
    - "slackInviteResultWarn: single dispatch helper that maps EnsureMemberResult to fail-soft stderr output"
    - "SlackAPI extends InviteAPI: cmd-level interface now fully satisfies pkg/slack.InviteAPI"
    - "channelName (human-readable sb-{id}) passed to slackInviteResultWarn for operator-friendly hint messages"

key-files:
  created:
    - internal/app/cmd/create_slack_invite_test.go
  modified:
    - internal/app/cmd/create_slack.go
    - internal/app/cmd/create_slack_test.go
    - internal/app/cmd/create_slack_transcript_test.go

key-decisions:
  - "AutoConnect=true for operator invite is UNCONDITIONAL — not gated by useSlackConnect. The operator is always invited; auto-detection picks conversations.invite vs Slack Connect."
  - "Pass channelName (sb-{sandboxID}) rather than opaque Slack channel ID to slackInviteResultWarn so SkippedExternal hints render as usable km slack invite --channel commands."
  - "fakeSlackAPI + fakeSlackAPIWithMembers in existing test files extended with stub InviteAPI methods (no-op behavior) to preserve backward compatibility without duplicating fakes."
  - "Pre-existing TestStep11d_Success_WritesChannelIDParam failure is out of scope (caused by prefix-scoping commit f54b8db, unrelated to Phase 72); logged to deferred-items.md."

patterns-established:
  - "slackInviteResultWarn pattern: centralize result→stderr mapping so both operator invite and additional-folks loop share identical warning semantics."
  - "Extending existing fakes with no-op Phase 72 method stubs rather than replacing them keeps older tests compiling without modification."

requirements-completed:
  - VALIDATION-Layer-7

duration: 15min
completed: 2026-05-29
---

# Phase 72 Plan 07: Operator Invite Refactor + Additional-Folks Loop Summary

**Operator invite in resolveSlackChannel Mode 2 refactored from raw InviteShared to EnsureMemberByEmail(AutoConnect=true); additional-folks loop over notifySlackInviteEmails added with UseSlackConnect gating; 8 Layer 7 tests all PASS.**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-05-29T19:05:00Z
- **Completed:** 2026-05-29T19:16:44Z
- **Tasks:** 1
- **Files modified:** 4

## Accomplishments

- Operator invite no longer hard-codes Slack Connect — native workspace members are now invited via `conversations.invite`, fixing the corporate case where the operator never landed in `#sb-{id}` under the old Connect-only call.
- Additional-folks loop wired: profiles with `notifySlackInviteEmails` now auto-invite each address after the channel is created and the bot has joined, before terragrunt apply.
- All five result paths (InvitedDirect, InvitedConnect, AlreadyMember, SkippedExternal, Failed) handled fail-soft through `slackInviteResultWarn`.
- `SlackAPI` interface in `create_slack.go` extended to include `LookupUserByEmail` + `InviteUserToChannelStrict` — `*slack.Client` already implements both since Phase 72-01/72-04.

## km create timeline (where invites sit)

Inside `resolveSlackChannel` Mode 2, after the bot self-join (`api.JoinChannel`) and before `return chID, true, nil`, in this order:

1. SSM lookup for `{prefix}/slack/invite-email`
2. If empty/missing: warn-and-skip (existing path unchanged)
3. Operator invite via `EnsureMemberByEmail(AutoConnect=true)` — unconditional
4. Additional-folks loop: `for _, email := range cli.NotifySlackInviteEmails`
5. `return chID, true, nil` (proceed to terragrunt apply)

## Warning text (operators will see these)

**SkippedExternal** (useSlackConnect: false, external email):
```
[warn] bob@external.com is not a member of the Slack workspace; not sending Connect invite (useSlackConnect: false).
  To send one: km slack invite --external bob@external.com --channel sb-{sandboxID}
```

**Failed** (any invite error):
```
[warn] Slack invite failed for guest@example.com on channel sb-{sandboxID}: ... (non-fatal — sandbox provisioning continues)
```

## Task Commits

1. **Task 1: Refactor operator invite + add additional-folks loop** - `f928af7` (feat)

## Files Created/Modified

- `internal/app/cmd/create_slack.go` — SlackAPI interface extended; operator invite refactored; additional-folks loop added; slackInviteResultWarn helper added
- `internal/app/cmd/create_slack_invite_test.go` — 8 Layer 7 tests replacing t.Skip stubs
- `internal/app/cmd/create_slack_test.go` — fakeSlackAPI extended with stub InviteAPI methods
- `internal/app/cmd/create_slack_transcript_test.go` — fakeSlackAPIWithMembers extended with stub InviteAPI methods

## Decisions Made

**AutoConnect unconditional for operator:** The operator invite uses `AutoConnect=true` hardcoded, not gated by `useSlackConnect`. This matches the plan's "operator is ALWAYS invited" mandate and is the fix for corporate workspaces where Slack Connect would fail/be unnecessary for native members.

**channelName in SkippedExternal hint:** `slackInviteResultWarn` receives `channelName` (`sb-test123`) rather than the opaque Slack channel ID (`CNEW`) so the `km slack invite --external ... --channel` follow-up command is immediately usable by operators.

**No adapter pattern needed:** Plan 72-05 extended the cmd-level `SlackAPI` interface directly (not via adapter), so `create_slack.go` simply adds `LookupUserByEmail` and `InviteUserToChannelStrict` to the interface. `*slack.Client` already satisfies it.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Extended fakeSlackAPI + fakeSlackAPIWithMembers with stub InviteAPI methods**
- **Found during:** Task 1 (compilation after SlackAPI interface extension)
- **Issue:** Existing fakes in `create_slack_test.go` and `create_slack_transcript_test.go` did not implement the newly added `LookupUserByEmail` + `InviteUserToChannelStrict` methods, causing compilation failure.
- **Fix:** Added no-op stub implementations (lookup always misses, InviteStrict always succeeds) to both fakes. Pre-Phase-72 tests continue to pass unmodified — the stubs don't affect their behavior.
- **Files modified:** `internal/app/cmd/create_slack_test.go`, `internal/app/cmd/create_slack_transcript_test.go`
- **Verification:** Full `TestCreateSlack_*` + `TestResolveSlack_*` + transcript tests all pass.
- **Committed in:** `f928af7` (part of Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 2 — missing critical interface stubs)
**Impact on plan:** Essential for compilation. No scope creep.

## Issues Encountered

Pre-existing `TestStep11d_Success_WritesChannelIDParam` test failure (expects `/sandbox/sb-test/slack-channel-id`, gets `/km/sandbox/sb-test/slack-channel-id`) — caused by prefix-scoping commit `f54b8db`, predates Phase 72, out of scope. Logged to `deferred-items.md`.

Full `cmd` test suite times out at 120s due to tests that make real HTTP calls — pre-existing, not caused by this plan.

## Note for Plan 72-09 (docs)

Document the following for operators:

- `notifySlackInviteEmails`: list of additional email addresses auto-invited to the per-sandbox channel at `km create` time. Works independently of the primary operator invite.
- `useSlackConnect` (default `true`): gates the Connect fallback for the **additional-folks loop** only (not the operator invite). When `true`, external addresses not in the workspace are auto-sent a Slack Connect invite. When `false`, they are skipped with a stderr hint to use `km slack invite --external`.
- Primary operator (from `{prefix}/slack/invite-email`): always invited unconditionally, auto-detected — native member gets `conversations.invite`, external gets Slack Connect.
- Recommended workflow: list additional internal/external collaborators in `notifySlackInviteEmails`; with `useSlackConnect` default-true they're auto-invited at `km create` (Connect for externals); set `useSlackConnect: false` to keep create-time invites workspace-internal and add externals manually via `km slack invite --external`.

## Next Phase Readiness

- Plan 72-09 (docs) can proceed: `notifySlackInviteEmails` and `useSlackConnect` behavior is fully implemented and tested.
- All five result paths are fail-soft — scheduled `km at` invocations stay non-aborting on invite errors.

---
*Phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator*
*Completed: 2026-05-29*
