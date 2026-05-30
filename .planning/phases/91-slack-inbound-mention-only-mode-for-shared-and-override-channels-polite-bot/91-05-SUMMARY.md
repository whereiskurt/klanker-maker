---
phase: 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot
plan: 05
subsystem: slack
tags: [slack, doctor, ssm, mention-only, polite-bot]

# Dependency graph
requires:
  - phase: 91-01
    provides: "NotifySlackInboundMentionOnly *bool profile field"
  - phase: 91-04
    provides: "km slack init caches bot-user-id in SSM at {prefix}slack/bot-user-id"
provides:
  - "checkSlackBotUserIDCached doctor check function (4 return paths)"
  - "anyProfileMentionOnly helper gating the check registration"
  - "TestCheckSlackBotUserIDCached table-driven test (4 subtests)"
  - "TestAnyProfileMentionOnly table-driven test (7 subtests)"
  - "Conditional registration in doctor Slack health block"
affects:
  - "91-06 (docs plan should mirror Remediation copy from this plan)"
  - "km doctor output when mention-only profiles exist"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Closure-injection pattern for testable doctor checks (mirrors checkSlackUsersReadEmailScope)"
    - "anyProfileMentionOnly gates expensive/noisy checks behind profile scan"
    - "Inline logic duplication preferred over exporting compiler internals"

key-files:
  created:
    - internal/app/cmd/doctor_slack_bot_user_id_test.go
  modified:
    - internal/app/cmd/doctor_slack_transcript.go
    - internal/app/cmd/doctor.go

key-decisions:
  - "Duplicated resolveMentionOnly logic into anyProfileMentionOnly in doctor.go rather than exporting from pkg/compiler — keeps compiler package sealed and avoids cross-package coupling"
  - "anyProfileMentionOnly scans all ProfileSearchPaths (not just profiles/) to match the AMI/stale-profile scan pattern already used in doctor.go"
  - "getUID closure captures slackSSMStore + ssmPrefix by value at registration time — safe because both are config-derived constants within the buildChecks call"

patterns-established:
  - "Profile-gated doctor checks: construct closure only if anyProfileMentionOnly() → nil → SKIPPED, avoids spurious WARNs on installs without the feature"

requirements-completed:
  - POL-10

# Metrics
duration: 12min
completed: 2026-05-30
---

# Phase 91 Plan 05: km doctor checkSlackBotUserIDCached Summary

**`km doctor` check for Phase 91 bot-user-id SSM cache with profile-gated registration and closure-injection pattern matching Phase 72 neighbors**

## Performance

- **Duration:** ~12 min
- **Started:** 2026-05-30T22:40:00Z
- **Completed:** 2026-05-30T22:52:02Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments
- `checkSlackBotUserIDCached` implemented with all four documented return paths (SKIPPED/OK/WARN-empty/WARN-error)
- `anyProfileMentionOnly(searchDirs []string) bool` helper scans profile YAML files using inline copy of `resolveMentionOnly` logic
- Conditional registration in `doctor.go` Slack health block: `getUID` closure is nil unless a local profile activates mention-only, keeping `SKIPPED` behavior on installs without the feature
- 11 new subtests across two test functions; all GREEN; `make build` clean

## Task Commits

1. **Task 1: Implement checkSlackBotUserIDCached + live test** - `5d13bc4` (feat, TDD)
2. **Task 2: Register in doctor.go Slack health block** - `1f594e7` (feat)

## Files Created/Modified
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/doctor_slack_transcript.go` — `checkSlackBotUserIDCached` function appended after `checkSlackUsersReadEmailScope`
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/doctor_slack_bot_user_id_test.go` — `TestCheckSlackBotUserIDCached` (4 subtests) + `TestAnyProfileMentionOnly` (7 subtests)
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/doctor.go` — `anyProfileMentionOnly` helper + conditional `checkSlackBotUserIDCached` registration in Slack health block

## Decisions Made
- Duplicated the 6-line `resolveMentionOnly` logic into `anyProfileMentionOnly` in `doctor.go` rather than exporting from `pkg/compiler` — plan explicitly recommends this to keep the compiler package self-contained.
- `anyProfileMentionOnly` accepts `[]string` (search dirs slice) to match the existing `cfg.GetProfileSearchPaths()` pattern already used by the AMI stale check in `buildChecks`.

## Deviations from Plan

None — plan executed exactly as written. The `ssmPrefix` parameter was passed into `checkSlackBotUserIDCached` as specified by the plan's action block. The `anyProfileMentionOnly` helper scans the same `searchDirs` pattern the plan recommended.

## Issues Encountered
None. The full `./internal/app/cmd/...` test suite hit the 120s timeout due to pre-existing integration tests making real HTTP/AWS calls — not related to changes in this plan. All doctor-related and Slack tests run cleanly in under 1s.

## Next Phase Readiness
- Plan 91-06 (docs): `docs/slack-notifications.md` Phase 91 section should mirror the Remediation string from `checkSlackBotUserIDCached`: `"Run km slack init --force (or km slack rotate-token --bot-token <token>) to re-capture and cache the bot user_id."`
- `km doctor` now surfaces WARN when any local profile enables mention-only mode and the SSM cache is missing

---
*Phase: 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot*
*Completed: 2026-05-30*
