---
phase: 62-claude-code-operator-notify-hook-for-permission-and-idle-events
plan: 01
subsystem: profile
tags: [profile, schema, json-schema, cli-spec, notify-hook, requirements]

# Dependency graph
requires: []
provides:
  - CLISpec.NotifyOnPermission bool field with yaml tag notifyOnPermission
  - CLISpec.NotifyOnIdle bool field with yaml tag notifyOnIdle
  - CLISpec.NotifyCooldownSeconds int field with yaml tag notifyCooldownSeconds
  - CLISpec.NotificationEmailAddress string field with yaml tag notificationEmailAddress
  - JSON schema properties for all four notify fields under spec.cli with type enforcement
  - HOOK-01..HOOK-05 requirement definitions in REQUIREMENTS.md with traceability rows
affects:
  - 62-02 (compiler: reads notify fields to write km-notify-env.sh and merge settings.json)
  - 62-03 (km-notify-hook bash script writer)
  - 62-04 (km shell / km agent run CLI flags --notify-on-permission / --notify-on-idle)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "CLISpec extension: add optional fields with omitempty yaml tags; JSON schema gets matching properties with strict types; TDD RED→GREEN→commit cycle"
    - "Schema validation: santhosh-tekuri/jsonschema/v6 enforces integer/boolean types but not format assertions (format: email dropped to avoid false-positives)"

key-files:
  created:
    - .planning/phases/62-claude-code-operator-notify-hook-for-permission-and-idle-events/62-01-SUMMARY.md
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/types_test.go
    - pkg/profile/validate_test.go
    - .planning/REQUIREMENTS.md

key-decisions:
  - "Dropped format: email from JSON schema for notificationEmailAddress — santhosh-tekuri/jsonschema/v6 treats format as annotation-only unless AssertFormat is explicitly enabled; actual address validation is handled by SES at send time"
  - "All four notify fields use omitempty yaml tags so existing profiles remain fully valid with zero-value defaults"

patterns-established:
  - "CLISpec optional field pattern: bool/int/string with omitempty yaml tags; JSON schema property with minimum: 0 for int; no format assertions on string fields unless validator is configured for them"

requirements-completed: [HOOK-01, HOOK-02, HOOK-03, HOOK-04, HOOK-05]

# Metrics
duration: 3min
completed: 2026-04-28
---

# Phase 62 Plan 01: Schema and Requirements Summary

**Four notify fields added to CLISpec (NotifyOnPermission, NotifyOnIdle, NotifyCooldownSeconds, NotificationEmailAddress) with JSON schema type enforcement and HOOK-01..HOOK-05 registered in REQUIREMENTS.md**

## Performance

- **Duration:** 3 min (182 seconds)
- **Started:** 2026-04-28T21:52:04Z
- **Completed:** 2026-04-28T21:55:06Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments

- CLISpec struct extended with four notify fields using correct yaml tags and Go types (bool, bool, int, string), all with omitempty to preserve backwards compat
- JSON schema updated with matching property entries under spec.cli, preserving additionalProperties: false; notifyCooldownSeconds has minimum: 0 constraint
- Six TDD tests added and passing: three parser round-trip tests (all set, defaults zero, explicit false) and three schema validation tests (all valid, cooldown wrong type, permission wrong type)
- HOOK-01..HOOK-05 registered in REQUIREMENTS.md with definitions, five traceability rows pointing at Phase 62, and coverage block updated from 66→71 requirements / 56→61 mapped phases

## Task Commits

Each task was committed atomically:

1. **TDD RED — failing tests** - `670689a` (test)
2. **Task 1: Extend CLISpec + JSON schema** - `74c6152` (feat)
3. **Task 2: Register HOOK-01..HOOK-05 in REQUIREMENTS.md** - `411efeb` (feat)

_Note: TDD task had two commits (test RED then feat GREEN)_

## Files Created/Modified

- `pkg/profile/types.go` — Four new fields appended to CLISpec struct after CodexArgs
- `pkg/profile/schemas/sandbox_profile.schema.json` — Four new property entries under spec.cli properties block (after codexArgs, before closing brace of properties)
- `pkg/profile/types_test.go` — Three new TestParse_CLISpec_Notify* test functions
- `pkg/profile/validate_test.go` — Three new TestValidate_NotifyFields_* test functions plus minimalCLIBase fixture constant
- `.planning/REQUIREMENTS.md` — New "Operator Notification Hooks" section, five traceability rows, updated coverage counts, new dated trailer

## Decisions Made

- Dropped `"format": "email"` from the JSON schema notificationEmailAddress property. The `santhosh-tekuri/jsonschema/v6` library treats format as an annotation (not validation assertion) unless `AssertFormat` is explicitly configured. No existing schema uses format assertions. Actual email address validation happens at SES send time, making schema enforcement redundant. Dropping it avoids a potential false-positive where a valid string passes the library but a future format-enabled build would reject it differently.
- Used `omitempty` on all four yaml tags so a profile that omits any or all notify fields parses identically to how it did before this plan (zero-value fields, no schema error).

## Deviations from Plan

None — plan executed exactly as written (one pre-planned deviation: dropped format: email per plan's own conditional instruction).

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- Plan 02 (compiler): can now read `Spec.CLI.NotifyOnPermission`, `NotifyOnIdle`, `NotifyCooldownSeconds`, `NotificationEmailAddress` to write `/etc/profile.d/km-notify-env.sh`
- Plan 03 (km-notify-hook script): can reference HOOK-01..HOOK-05 requirements as acceptance criteria
- Plan 04 (CLI flags): can reference the struct fields as the profile defaults the flags override

---
*Phase: 62-claude-code-operator-notify-hook-for-permission-and-idle-events*
*Completed: 2026-04-28*
