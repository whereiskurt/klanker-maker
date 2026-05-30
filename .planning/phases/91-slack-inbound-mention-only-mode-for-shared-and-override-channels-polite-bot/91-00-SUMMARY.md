---
phase: 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot
plan: "00"
subsystem: testing
tags: [slack, bridge, mention-only, tdd, stub, wave0]

# Dependency graph
requires:
  - phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator
    provides: CLISpec *bool tri-state pattern (UseSlackConnect, NotifySlackInviteEmails)
  - phase: 67-slack-inbound-dispatch
    provides: EventsHandler, SQS dispatch, fakeBotUserID test doubles
provides:
  - Wave 0 stub test contract for all 10 POL-XX requirements with automated validation commands
  - t.Skip stubs in 6 files (5 new, 1 extended) giving Wave 1+ a clear RED→GREEN signal
affects: [91-01, 91-02, 91-03, 91-04, 91-05]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - Wave 0 stub seeding with t.Skip to lock validation contract before implementation
    - Table-driven stub structure with named cases pre-defined for Wave N to fill in assertions

key-files:
  created:
    - pkg/profile/profile_cli_mention_test.go
    - pkg/compiler/userdata_mention_test.go
    - cmd/km-slack-bridge/main_mention_test.go
    - internal/app/cmd/slack_mention_init_test.go
    - internal/app/cmd/doctor_slack_bot_user_id_test.go
  modified:
    - pkg/slack/bridge/events_handler_test.go

key-decisions:
  - "Extended events_handler_test.go in place (single file) rather than creating a sibling — keeps all bridge fakes co-located"
  - "7-case table structure for TestEventsHandler_MentionOnly pre-defines all named cases so Plan 91-03 only fills assertions, not test structure"
  - "9-case comment block in TestResolveMentionOnly documents the full Mode x override matrix (Mode 1/2/3 x nil/&true/&false) per RESEARCH.md Pattern 4 + Q5"

patterns-established:
  - "Wave 0 stub pattern: t.Skip with TODO Plan 91-XX message gives clear RED→GREEN signal and human-readable context"

requirements-completed: [POL-01, POL-02, POL-04, POL-06, POL-07, POL-08, POL-09, POL-10, POL-11, POL-12]

# Metrics
duration: 6min
completed: 2026-05-30
---

# Phase 91 Plan 00: Wave 0 Stub Seeding Summary

**Six test stub files (5 new, 1 extended) lock the Phase 91 validation contract across all 10 POL-XX requirements before any production code lands**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-05-30T22:13:04Z
- **Completed:** 2026-05-30T22:19:00Z
- **Tasks:** 3
- **Files modified:** 6

## Accomplishments

- Seeded Wave 0 stubs for all POL-XX requirements that have an `<automated>` command in 91-VALIDATION.md (POL-01, POL-02, POL-04, POL-06, POL-07, POL-08, POL-09, POL-10, POL-11, POL-12)
- Extended `pkg/slack/bridge/events_handler_test.go` with a 7-case table-driven `TestEventsHandler_MentionOnly` covering all mention-scan scenarios Wave 91-03 will implement
- All 6 files compile cleanly under `go vet`; all 10 stub functions are discoverable and skip via `t.Skip`

## Task Commits

1. **Task 1: Create pkg/profile + pkg/compiler stub test files** - `7025ba0` (test)
2. **Task 2: Extend bridge events handler tests + add bridge main wiring stub** - `e776626` (test)
3. **Task 3: Create internal/app/cmd stub test files** - `a4ad9cb` (test)

## Files Created/Modified

- `pkg/profile/profile_cli_mention_test.go` - TestCLISpec_NotifySlackInboundMentionOnly + TestSchema_NotifySlackInboundMentionOnly (POL-01, POL-02)
- `pkg/compiler/userdata_mention_test.go` - TestResolveMentionOnly (9-case table, POL-04/POL-11) + TestMentionOnlyCompiler
- `pkg/slack/bridge/events_handler_test.go` - Extended with TestEventsHandler_MentionOnly (7-case table, POL-06/POL-12)
- `cmd/km-slack-bridge/main_mention_test.go` - TestWireEventsHandler_BotUserIDPrime (POL-09)
- `internal/app/cmd/slack_mention_init_test.go` - TestRunSlackInit_BotUserIDCached + TestRotateToken_BotUserIDCached (POL-07, POL-08)
- `internal/app/cmd/doctor_slack_bot_user_id_test.go` - TestCheckSlackBotUserIDCached (POL-10)

## Decisions Made

- Extended `events_handler_test.go` in place (single file) rather than creating a sibling — keeps all bridge fakes (fakeBotUserID, fakeSQS, fakeThreads, fakeNonces, fakeSandboxes) co-located per plan requirement
- Pre-defined all 7 named table cases in TestEventsHandler_MentionOnly so Plan 91-03 only fills assertions inside the loop
- Pre-documented the 9-case Mode x override matrix (comment block) in TestResolveMentionOnly per RESEARCH.md Pattern 4 + Q5

## Deviations from Plan

None — plan executed exactly as written. All files created in the specified packages, all stubs use `t.Skip` with the exact TODO messages specified in the plan.

## Issues Encountered

`go vet ./...` reports a pre-existing IPv6 format string warning in `sidecars/http-proxy/httpproxy/transparent.go:204` that is unrelated to Phase 91 changes. All Phase 91 target packages (`pkg/profile`, `pkg/compiler`, `pkg/slack/bridge`, `cmd/km-slack-bridge`, `internal/app/cmd`) vet cleanly.

## Next Phase Readiness

- Wave 0 contract is locked; Plans 91-01 through 91-05 each have a clear RED→GREEN signal to drive implementation
- Plan 91-01: add `CLISpec.NotifySlackInboundMentionOnly *bool` to types.go + schema → flip profile stubs green
- Plan 91-02: add `resolveMentionOnly()` to compiler + emit `KM_SLACK_MENTION_ONLY` → flip compiler stubs green
- Plan 91-03: add `MentionOnly bool` to EventsHandler + step 4b mention-scan in Handle() → flip bridge stubs green
- Plan 91-04: wire `AuthTestWithUserID` + SSM bot-user-id caching in RunSlackInit + RotateToken → flip init stubs green
- Plan 91-05: add `checkSlackBotUserIDCached` doctor check → flip doctor stub green

---
*Phase: 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot*
*Completed: 2026-05-30*
