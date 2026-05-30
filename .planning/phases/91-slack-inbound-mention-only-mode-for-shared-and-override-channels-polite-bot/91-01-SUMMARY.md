---
phase: 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot
plan: "01"
subsystem: profile-schema
tags: [slack, bridge, mention-only, schema, tdd, tri-state]

# Dependency graph
requires:
  - phase: 91-00
    provides: Wave 0 t.Skip stub test contract in profile_cli_mention_test.go
  - phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator
    provides: CLISpec *bool tri-state pattern (UseSlackConnect, NotifySlackInviteEmails)
provides:
  - CLISpec.NotifySlackInboundMentionOnly *bool field with yaml+json tags and godoc
  - sandbox_profile.schema.json notifySlackInboundMentionOnly optional boolean property
  - Live GREEN tests for POL-01, POL-02, POL-03 replacing t.Skip stubs
affects: [91-02, 91-03, 91-04, 91-05]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - Tri-state *bool field: nil=mode-derived-default, &true=force-on, &false=force-off
    - TDD RED→GREEN: write failing test first, add field/schema, verify GREEN
    - JSON Schema optional boolean without default (Go side handles nil semantics)

key-files:
  created: []
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/profile_cli_mention_test.go

key-decisions:
  - "Used *bool tri-state (nil/&true/&false) matching UseSlackConnect and SlackArchiveOnDestroy precedent — no default in JSON Schema, Go side resolves nil"
  - "No semantic reject rules added to validate.go — POL-03 is purely additive; any bool value is valid"
  - "Field placed after VSCodeEnabled in CLISpec to maintain chronological phase ordering"

# Metrics
duration: 140s
completed: 2026-05-30
---

# Phase 91 Plan 01: Profile Schema — NotifySlackInboundMentionOnly Summary

**`CLISpec.NotifySlackInboundMentionOnly *bool` tri-state field added to Go struct and JSON Schema, with 11 live TDD tests replacing the Wave 0 t.Skip stubs for POL-01/02/03**

## Performance

- **Duration:** ~140s (2.3 min)
- **Started:** 2026-05-30T22:17:59Z
- **Completed:** 2026-05-30T22:20:19Z
- **Tasks:** 3
- **Files modified:** 3

## Accomplishments

- Added `NotifySlackInboundMentionOnly *bool` to `CLISpec` with complete godoc documenting nil/&true/&false tri-state semantics, bridge behaviour (mention-scan skip logic), and deploy notes
- Added `notifySlackInboundMentionOnly` optional boolean property to `pkg/profile/schemas/sandbox_profile.schema.json` under the cli object — no `default`, not `required`, matches sibling style
- Flipped all Wave 0 t.Skip stubs in `profile_cli_mention_test.go` to live assertions: 11 subtests across 3 test functions, all GREEN
- `make build` succeeds at v0.3.758; full `pkg/profile` test suite green

## Task Commits

1. **Task 1: Add CLISpec.NotifySlackInboundMentionOnly *bool field** - `fd89f6b` (feat)
2. **Task 2: Add JSON Schema property** - `dc89052` (feat)
3. **Task 3: ValidateSemantic acceptance test (POL-03)** - `a847fbf` (test)

## Files Modified

- `pkg/profile/types.go` — new `NotifySlackInboundMentionOnly *bool` field after `VSCodeEnabled`
- `pkg/profile/schemas/sandbox_profile.schema.json` — new `notifySlackInboundMentionOnly` property in cli object
- `pkg/profile/profile_cli_mention_test.go` — 11 live subtests replacing t.Skip stubs

## Tests Added

| Function | Subtests | POL |
|---|---|---|
| TestCLISpec_NotifySlackInboundMentionOnly | omitted-yaml, explicit-true, explicit-false, json-roundtrip | POL-01 |
| TestSchema_NotifySlackInboundMentionOnly | true-accepted, false-accepted, string-rejected, omitted-accepted | POL-02 |
| TestValidateSemantic_NotifySlackInboundMentionOnly | force-true, force-false, nil-default | POL-03 |

## Decisions Made

- Tri-state `*bool` (nil/&true/&false) chosen to match `UseSlackConnect` and `SlackArchiveOnDestroy` — the most recent precedent in CLISpec. No default in JSON Schema; the compiler resolver (Plan 91-02) will read the nil to apply mode-derived default.
- No changes to `validate.go` — POL-03 is verified by test (no semantic rules fire for any tri-state value), not code.
- `json` tag added alongside `yaml` tag for symmetry with Phase 72/73 additions (`UseSlackConnect`, `VSCodeEnabled`).

## Deviations from Plan

None — plan executed exactly as written. TDD RED→GREEN→commit flow followed for Tasks 1 and 2. Task 3 implemented as a separate `TestValidateSemantic_NotifySlackInboundMentionOnly` function (the plan offered "your call" on placement; separate function was cleaner given the fixture helper needed).

## Rollout Notes for Downstream Plans

This plan adds a profile schema field. **Before this new field can be used in production:**

1. `make build` — already done, produces `km v0.3.758`
2. `km init --sidecars` — required after deploy so the management Lambda's `km` binary recognises the new `NotifySlackInboundMentionOnly` field. Without this, the remote create handler will parse profiles with an older binary that silently ignores the field.
3. Existing sandboxes need `km destroy && km create` to pick up the new field — profiles compiled before this change will not have the field in their rendered userdata/env.

## Next Phase Readiness

- Plan 91-02: `resolveMentionOnly()` in `pkg/compiler/` can now read `Spec.CLI.NotifySlackInboundMentionOnly` — the field is present and tri-state-safe
- Plan 91-03: `EventsHandler` can read the resolved value from compiled env (`KM_SLACK_MENTION_ONLY`)
- Plans 91-04/05: `km slack init` / `km doctor` changes are unblocked

## Self-Check: PASSED

All files present and all commits verified:
- FOUND: pkg/profile/types.go
- FOUND: pkg/profile/schemas/sandbox_profile.schema.json
- FOUND: pkg/profile/profile_cli_mention_test.go
- FOUND: 91-01-SUMMARY.md
- Commits fd89f6b, dc89052, a847fbf all in git log

---
*Phase: 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot*
*Completed: 2026-05-30*
