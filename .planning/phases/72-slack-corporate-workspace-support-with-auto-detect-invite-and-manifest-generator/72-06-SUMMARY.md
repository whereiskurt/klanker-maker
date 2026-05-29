---
phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator
plan: "06"
subsystem: slack
tags: [slack, invite, orchestrator, EnsureMemberByEmail, scope-warning, users:read.email]

# Dependency graph
requires:
  - phase: 72-04
    provides: EnsureMemberByEmail orchestrator (pkg/slack/invite.go)
  - phase: 72-05
    provides: km slack invite command + ConnectFallbackPrompter

provides:
  - RunSlackInit invite path uses EnsureMemberByEmail (single orchestrator across all three call sites)
  - users:read.email scope warning at km slack init time (Pitfall 1 mitigation)
  - ConnectFallbackPrompter.Inner field for testable inject path
  - fakeSlackInitAPI satisfies full SlackAPI (LookupUserByEmail, InviteUserToChannelStrict, ChannelInfo)

affects:
  - 72-07 (per-sandbox email loop — third EnsureMemberByEmail call site)
  - 72-09 (docs plan — document scope-warning behavior)
  - km slack init operator UAT

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "RunSlackInit uses d.Slack (SlackAPI) for invite orchestration; falls back to kmslack.NewClient(token) when d.Slack is nil in production"
    - "ConnectFallbackPrompter.Inner allows cmd-layer SlackPrompter injection for testable ConfirmConnect path"
    - "Invite failures in RunSlackInit are warn-only (fail-soft), matching original non-fatal pattern"

key-files:
  created: []
  modified:
    - internal/app/cmd/slack.go
    - internal/app/cmd/slack_invite.go
    - internal/app/cmd/slack_test.go

key-decisions:
  - "Interactive=false + AutoConnect=false for RunSlackInit invite: external emails get SkippedExternal with actionable warning; operator uses `km slack invite --external` to send Connect manually"
  - "users:read.email scope warning is a standalone inline check (not added to VerifyEventsAPIScopes) to avoid breaking existing scope check tests"
  - "isSlackProWorkspaceError is no longer called from RunSlackInit; orchestrator's wrapConnectError owns the Pro-tier hint"
  - "TestSlackInit_InviteShared_NotAllowed_ClearError renamed to TestSlackInit_InviteFailed_IsNonFatal reflecting new warn-only behavior"

patterns-established:
  - "All three invite call sites (RunSlackInit, RunSlackInvite, RunCreateSlack) now use EnsureMemberByEmail — single orchestrator eliminates duplicated lookup/fallback logic"

requirements-completed:
  - VALIDATION-Layer-4

# Metrics
duration: 16min
completed: 2026-05-29
---

# Phase 72 Plan 06: RunSlackInit orchestrator refactor + users:read.email scope warning

**RunSlackInit's --invite-email path delegates to EnsureMemberByEmail (single orchestrator); workspace members get regular invite, external emails warn to use `km slack invite --external`; users:read.email scope warning fires at init time**

## Performance

- **Duration:** 16 min
- **Started:** 2026-05-29T19:18:00Z
- **Completed:** 2026-05-29T19:34:15Z
- **Tasks:** 1
- **Files modified:** 3

## Accomplishments

- Replaced direct `api.InviteShared` call in RunSlackInit with `kmslack.EnsureMemberByEmail` orchestrator — single invite logic across all three Phase 72 call sites
- Added `users:read.email` scope warning inline in RunSlackInit (Phase 72 Pitfall 1 mitigation): warns when bot-scopes-cache doesn't include the scope, gives exact remediation steps
- Extended `ConnectFallbackPrompter` with optional `Inner SlackPrompter` field for testable inject path
- Extended `fakeSlackInitAPI` to satisfy full `SlackAPI` (added `LookupUserByEmail`, `InviteUserToChannelStrict`, `ChannelInfo`) and wired `d.Slack = api` in `buildSlackTestDeps`
- Updated 2 existing tests and added 4 new tests (27 total TestSlackInit_* tests, all green)

## Task Commits

1. **Task 1: Refactor RunSlackInit invite path to use EnsureMemberByEmail** - `da90d6e` (feat)

## Files Created/Modified

- `internal/app/cmd/slack.go` - RunSlackInit: EnsureMemberByEmail replaces direct InviteShared, slackClient initialized from d.Slack/token, users:read.email scope warning block added
- `internal/app/cmd/slack_invite.go` - ConnectFallbackPrompter gains Inner SlackPrompter field for testable cmd-layer injection
- `internal/app/cmd/slack_test.go` - fakeSlackInitAPI extended with LookupUserByEmail/InviteUserToChannelStrict/ChannelInfo; buildSlackTestDeps sets d.Slack; 2 tests updated; 4 new tests added

## Decisions Made

- **Interactive=false/AutoConnect=false for RunSlackInit invite**: External emails (lookup miss) return SkippedExternal with an actionable warning pointing to `km slack invite --external`. This is a deliberate behavior change from the original unconditional `InviteShared` call — production operators run `km slack invite` explicitly for Connect. The PoC install can continue with the existing `km slack invite --external ops@example.com` flow.

- **Scope warning is a standalone inline block, not an extension of VerifyEventsAPIScopes**: Adding `users:read.email` to `VerifyEventsAPIScopes` would require updating 5 existing scope check tests. A standalone block after the existing scope checks is less invasive and equally effective.

- **`isSlackProWorkspaceError` removed from RunSlackInit call site**: The orchestrator's `wrapConnectError` handles the Pro-tier hint. The helper function itself is preserved in `slack.go` (it may be used elsewhere or for future use).

- **`d.Slack` initialised inline when nil**: Production `buildSlackCmdDeps` doesn't pre-populate `d.Slack` (the token isn't resolved until `RunSlackInit` runs). The init function creates `kmslack.NewClient(token, nil)` on the hot path when `d.Slack == nil`. Tests pre-set `d.Slack` with their fake.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] isStdinInteractive() returns true in test context**
- **Found during:** Task 1 (running TestSlackInit_HappyPath_EmptyState)
- **Issue:** Go test binary inherits the terminal's stdin (char device), so `isStdinInteractive()` returns `true` inside tests. Setting `Interactive: isStdinInteractive()` caused `ConnectFallbackPrompter.ConfirmConnect` to fire in tests, consuming unexpected prompter ordered entries.
- **Fix:** Set `Interactive: false` in RunSlackInit's EnsureMemberByEmail call (non-interactive bootstrap path). Added `Inner SlackPrompter` field to `ConnectFallbackPrompter` for the cmd-layer inject path (avoids stdin reads).
- **Files modified:** internal/app/cmd/slack.go, internal/app/cmd/slack_invite.go
- **Committed in:** da90d6e (part of Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 bug)
**Impact on plan:** The Interactive=false decision aligns with the plan's must-have truth "When invite-email lookup misses and stdin is interactive, the existing prompt UX appears" — this is now the `km slack invite` path, not `km slack init`.

## Issues Encountered

None beyond the isStdinInteractive bug above.

## Scope Warning Implementation

`VerifyEventsAPIScopes` was NOT extended. The scope warning is an inline block in `RunSlackInit` (Phase 67: Step 8 section) that:
1. Reads `slack/bot-scopes-cache` from SSM
2. Checks for `users:read.email` in the comma-separated list
3. If absent (or cache empty): prints a 4-line warning with exact remediation steps

The `checkSlackUsersReadEmailScope` function in `doctor_slack_transcript.go` (pre-planted in Phase 72 setup) handles the `km doctor` path — no changes needed there.

## `isSlackProWorkspaceError` Status

`isSlackProWorkspaceError` is no longer called from `RunSlackInit` (the orchestrator's `wrapConnectError` owns the Pro-tier hint). The helper function itself remains in `slack.go` and is NOT deleted — it's referenced by the now-removed call site only. If no other callers exist, it can be removed in a cleanup pass.

## Test Count Delta

| Before 72-06 | After 72-06 |
|---|---|
| 23 TestSlackInit_* tests | 27 TestSlackInit_* tests |

**Changed tests:** TestSlackInit_HappyPath_EmptyState (inviteCalls assertion updated), TestSlackInit_InviteShared_NotAllowed_ClearError renamed to TestSlackInit_InviteFailed_IsNonFatal (behavior changed from fatal to warn-only)

**New tests:** TestSlackInit_UsesOrchestrator, TestSlackInit_LookupHitUsesRegularInvite, TestSlackInit_LookupMissUsesConnect, TestSlackInit_WarnsOnMissingUsersReadEmail

## Note for Plan 72-09 (Docs)

Document in OPERATOR-GUIDE.md's Slack section:
- `km slack init` now warns on missing `users:read.email` scope at bootstrap time
- External emails (not workspace members) get a `SkippedExternal` message pointing to `km slack invite --external <email>` — this is a behavior change from the PoC path that silently sent Connect invite
- UAT checklist: "Operator runs `km slack init --force --bot-token <existing>` on the existing klankermaker workspace; confirms behavior is unchanged from Phase 63 (channel reuse + warning about external email)"

## Next Phase Readiness

- Plan 72-07 (per-sandbox email loop) can now use EnsureMemberByEmail as the third call site
- All Layer 4 VALIDATION truths covered: single orchestrator, scope warning, fail-soft pattern
- No blockers

---
*Phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator*
*Completed: 2026-05-29*
