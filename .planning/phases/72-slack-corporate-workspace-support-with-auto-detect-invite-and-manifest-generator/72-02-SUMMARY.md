---
phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator
plan: "02"
subsystem: profile
tags: [slack, profile-validation, types, json-schema, tdd]

# Dependency graph
requires:
  - phase: 72-00
    provides: Phase 72 wave-0 stubs (test stubs for Layer 6 in validate_slack_invite_emails_test.go)

provides:
  - CLISpec.NotifySlackInviteEmails []string field in types.go with godoc and yaml/json tags
  - CLISpec.UseSlackConnect *bool field in types.go with godoc and yaml/json tags
  - Rule SE1 (cross-field): non-empty notifySlackInviteEmails requires notifySlackEnabled:true
  - Rule SE2 (per-element): each list entry must pass emailLooksValid (permissive RFC-5322-ish regex)
  - emailLooksValid helper in validate.go (new, no prior helper existed)
  - JSON schema mirror entries for notifySlackInviteEmails (array, format:email) and useSlackConnect (boolean, default:true)
  - 6 Layer 6 tests (all PASS): Parse round-trip, SE1, SE2, UseSlackConnect nil/false, schema presence, empty-list no-op

affects:
  - 72-07 (km create auto-invite loop: reads cli.NotifySlackInviteEmails, passes AutoConnect=(cli.UseSlackConnect == nil || *cli.UseSlackConnect))
  - 72-09 (docs: document both fields in docs/slack-notifications.md profile field reference table)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "emailLooksValid: permissive `^[^@\s]+@[^@\s]+\\.[^@\s]+$` regex for profile-side email checks; Slack API is the authoritative gate"
    - "SE1/SE2 pattern mirrors SI1/ST1 from Phase 67/68 — cross-field gate then per-element loop"
    - "UseSlackConnect *bool: nil=true semantics (resolved at call time in 72-07, not in validator)"

key-files:
  created:
    - pkg/profile/validate_slack_invite_emails_test.go (rewritten from stubs to 6 real tests)
  modified:
    - pkg/profile/types.go (two new CLISpec fields added after NotifySlackTranscriptEnabled)
    - pkg/profile/validate.go (Rule SE1, Rule SE2, emailLooksValid helper)
    - pkg/profile/schemas/sandbox_profile.schema.json (notifySlackInviteEmails + useSlackConnect entries)

key-decisions:
  - "emailLooksValid is a new helper (no prior email validator existed in pkg/profile); regex is intentionally permissive — Slack API provides the authoritative check"
  - "types.go and schema.json updated atomically in Task 1 (single commit) — closes Pitfall 7 drift risk"
  - "UseSlackConnect produces no validator rule — it is a pure behavior toggle; nil defaults to true at read time in km create (Plan 72-07)"
  - "Empty notifySlackInviteEmails list is explicitly a no-op — does not require notifySlackEnabled (SE1 guards non-empty only)"

patterns-established:
  - "SE1: len(cli.NotifySlackInviteEmails) > 0 && !slackOn → ValidationError{Path: 'spec.cli.notifySlackInviteEmails', ...}"
  - "SE2: for i, e := range cli.NotifySlackInviteEmails { if !emailLooksValid(e) → ValidationError{Path: fmt.Sprintf('spec.cli.notifySlackInviteEmails[%d]', i), ...} }"

requirements-completed: [VALIDATION-Layer-6]

# Metrics
duration: 3min
completed: 2026-05-29
---

# Phase 72 Plan 02: Slack Invite-Email Profile Fields Summary

**CLISpec.NotifySlackInviteEmails and CLISpec.UseSlackConnect fields added to types.go and schema.json atomically, with SE1/SE2 validator rules and 6 passing Layer 6 tests**

## Performance

- **Duration:** 3 min
- **Started:** 2026-05-29T18:50:13Z
- **Completed:** 2026-05-29T18:53:18Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments
- Added `NotifySlackInviteEmails []string` and `UseSlackConnect *bool` to CLISpec in types.go with full godoc
- Mirrored both fields in sandbox_profile.schema.json atomically (notifySlackInviteEmails with format:email, useSlackConnect with default:true)
- Introduced `emailLooksValid` helper (no prior email validator existed in pkg/profile)
- Implemented Rule SE1 (cross-field: non-empty list requires slackEnabled) and Rule SE2 (per-element email format check)
- Replaced 5 stub t.Skip tests with 6 real passing Layer 6 assertions; full pkg/profile suite green; km v0.3.738 rebuilt

## Task Commits

Each task was committed atomically:

1. **Task 1: Add CLISpec field + JSON schema entry (atomic)** - `c5a006b` (feat)
2. **Task 2: Add validation rules + flip stub tests green** - `c6f4887` (feat)

**Plan metadata:** (final commit below)

_Note: Both tasks were TDD — Task 1 touched data shape only; Task 2 added rules + tests_

## Files Created/Modified
- `pkg/profile/types.go` - CLISpec.NotifySlackInviteEmails []string and CLISpec.UseSlackConnect *bool added after NotifySlackTranscriptEnabled
- `pkg/profile/schemas/sandbox_profile.schema.json` - Mirror entries for both fields (notifySlackInviteEmails array/email format, useSlackConnect boolean/default:true)
- `pkg/profile/validate.go` - Rule SE1, Rule SE2, emailLooksValid helper (regexp-based)
- `pkg/profile/validate_slack_invite_emails_test.go` - Rewritten from 5 t.Skip stubs to 6 real assertions

## Decisions Made
- **emailLooksValid helper:** New helper introduced (permissive `^[^@\s]+@[^@\s]+\.[^@\s]+$` regex) since no prior email validator existed in pkg/profile. Intentionally permissive — Slack API is the authoritative gate. Placed at bottom of validate.go with explanatory godoc.
- **Atomic types+schema commit:** Task 1 updates both types.go and schema.json in a single commit to prevent drift (Pitfall 7 from 72-CONTEXT.md). This is the canonical pattern for future field additions.
- **UseSlackConnect: no validator rule:** The field is inert in the validator — nil means true-at-read-time (resolved in Plan 72-07's km create loop, not here). SE1/SE2 are the only new rules.
- **Empty list = no-op:** SE1 guards `len > 0 && !slackOn`, so empty `[]` never fires SE1. Confirmed by TestValidate_InviteEmails_EmptyList_NoRequiresSlack.

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered
None.

## Notes for Downstream Plans

**For Plan 72-07 (km create auto-invite loop):**
- Slice is at `cli.NotifySlackInviteEmails`
- Pass to EnsureMemberByEmail orchestrator with `Interactive=false`
- Derive AutoConnect as: `autoConnect := cli.UseSlackConnect == nil || *cli.UseSlackConnect`
- The loop is ADDITIVE — runs after, and does not replace, the existing primary-operator invite

**For Plan 72-09 (docs):**
- Document both fields in `docs/slack-notifications.md` profile field reference table
- Clarify `notifySlackInviteEmails` adds folks *beyond* the always-invited primary operator
- Clarify `useSlackConnect` (default true) controls Connect fallback for the create-time loop only

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Layer 6 validation complete; types.go and schema.json atomically in sync
- Plan 72-07 (km create auto-invite loop) and Plan 72-09 (docs) can now proceed
- No blockers

---
*Phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator*
*Completed: 2026-05-29*
