---
phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events
plan: 01
subsystem: profile-schema
tags: [slack, validation, schema, go, yaml, bool-pointer, km-validate]

# Dependency graph
requires:
  - phase: 62-claude-code-operator-notify-hook-for-permission-and-idle-events
    provides: CLISpec with NotifyOnPermission/NotifyOnIdle/NotifyCooldownSeconds/NotificationEmailAddress; km-notify-hook shell script; /etc/profile.d/km-notify-env.sh compiler output

provides:
  - Five new spec.cli fields on CLISpec with correct types (*bool for notifyEmailEnabled/notifySlackEnabled/slackArchiveOnDestroy, bool for notifySlackPerSandbox, string for notifySlackChannelOverride)
  - IsWarning bool field on ValidationError enabling non-blocking validation messages
  - Five Slack semantic validation rules in ValidateSemantic (mutual-exclusion error, two no-op warnings, channel-ID regex error, neither-channel warning)
  - JSON schema entries for all five new cli properties with ^C[A-Z0-9]+$ channel-ID pattern
  - km validate honors warnings (WARN: prefix, exit 0) vs errors (ERROR: prefix, exit 1)
  - SLCK-01..SLCK-10 in REQUIREMENTS.md (added during Phase 63 planning)

affects: [63-02-compiler, 63-04-compiler-slack-env, 63-08-km-create-slack-channel, 63-09-km-destroy-archive]

# Tech tracking
tech-stack:
  added: [regexp (added import to validate.go)]
  patterns:
    - "*bool for feature flags where nil=unset/default differs from explicit-false (Phase 62 Pitfall 4 pattern)"
    - "IsWarning on ValidationError separates non-blocking linting from hard validation failures"
    - "ValidateSemantic returns []ValidationError with mixed IsWarning=true/false — caller separates"

key-files:
  created: []
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - schemas/sandbox_profile.schema.json
    - pkg/profile/validate.go
    - pkg/profile/types_test.go
    - pkg/profile/validate_test.go
    - internal/app/cmd/validate.go

key-decisions:
  - "*bool for notifyEmailEnabled, notifySlackEnabled, slackArchiveOnDestroy: nil (omitted YAML) is distinguishable from explicit false, preserving Phase 62 backward compat where unset email = on by default"
  - "IsWarning on ValidationError (not a separate warning type): consistent with existing Error() interface, km validate is the only caller that needs to separate them so the bool field is the minimal change"
  - "Five Slack rules in ValidateSemantic (not JSON schema): mutual-exclusion and no-op combinations require cross-field awareness that JSON schema cannot express; channel-ID regex is belt-and-suspenders in both layers"
  - "boolPtr(b bool) *bool helper in validate_test.go for clear test readability when constructing *bool fields"
  - "Root schemas/ sync: both sandbox_profile.schema.json files (root for tooling, pkg/profile/schemas/ for go:embed) updated with cli section additions"

patterns-established:
  - "IsWarning pattern: semantic rules emit IsWarning=true for no-op combos, IsWarning=false for hard errors; km validate separates them"
  - "*bool pointer pattern for CLI booleans that need three states (unset, explicit-false, explicit-true)"

requirements-completed: [SLCK-01]

# Metrics
duration: 5min
completed: 2026-04-29
---

# Phase 63 Plan 01: Slack Profile Schema + IsWarning Validation Summary

**Five spec.cli Slack fields with *bool semantics, IsWarning on ValidationError for non-blocking validation, and five semantic rules for Slack field combinations**

## Performance

- **Duration:** ~5 min
- **Started:** 2026-04-29T13:16:45Z
- **Completed:** 2026-04-29T13:21:44Z
- **Tasks:** 2
- **Files modified:** 7

## Accomplishments

- Extended CLISpec with five Phase 63 fields (`NotifyEmailEnabled *bool`, `NotifySlackEnabled *bool`, `NotifySlackPerSandbox bool`, `NotifySlackChannelOverride string`, `SlackArchiveOnDestroy *bool`) — `*bool` used for the three flag fields that need nil/false/true distinctions per RESEARCH.md Pitfall 4
- Added `IsWarning bool` to `ValidationError` and implemented five Slack-specific validation rules in `ValidateSemantic()`: mutual-exclusion error (perSandbox + channelOverride), no-op warning (perSandbox + slackDisabled), no-op warning (archiveOnDestroy without perSandbox), channel-ID regex error (belt-and-suspenders with schema), neither-channel warning (both emailEnabled=false + slackEnabled=false)
- Updated JSON schemas in both `pkg/profile/schemas/` (embedded, runtime) and root `schemas/` (tooling), including `^C[A-Z0-9]+$` pattern constraint for `notifySlackChannelOverride`
- Updated `km validate` to separate warnings (WARN: prefix, no exit-1 flip) from errors (ERROR: prefix, exit 1); also applied to the `extends` resolution code path
- 10 new tests: 3 round-trip tests in `types_test.go` (all-set, omitted-nil-pointers, explicit-false), 7 semantic tests in `validate_test.go` (all five rules + happy-path + Phase 62 backward-compat regression)
- REQUIREMENTS.md SLCK-01..SLCK-10 with traceability rows and coverage update were already present from Phase 63 planning (commit 013647e)

## Task Commits

1. **Task 1: Extend CLISpec + JSON schema + IsWarning + five validation rules + km validate + tests** - `6296db6` (feat)
2. **Task 2: Register SLCK-01..SLCK-10 in REQUIREMENTS.md** - Already done in planning commit `013647e`; Task 2 verification confirmed 10 SLCK rows present

**Plan metadata:** (committed with final docs commit)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/profile/types.go` — Five new CLISpec fields with correct types and Phase 63 comment block
- `/Users/khundeck/working/klankrmkr/pkg/profile/schemas/sandbox_profile.schema.json` — Five new cli properties including `^C[A-Z0-9]+$` pattern; `additionalProperties: false` preserved
- `/Users/khundeck/working/klankrmkr/schemas/sandbox_profile.schema.json` — Root schema synced with full cli section (was missing cli entirely)
- `/Users/khundeck/working/klankrmkr/pkg/profile/validate.go` — `regexp` import added; `IsWarning bool` on `ValidationError`; five Slack rules in `ValidateSemantic`
- `/Users/khundeck/working/klankrmkr/pkg/profile/types_test.go` — Three new tests: `TestParse_CLISpec_SlackFields_AllSet`, `_OmittedNilPointers`, `_ExplicitFalse`
- `/Users/khundeck/working/klankrmkr/pkg/profile/validate_test.go` — Seven new tests: `TestValidateSemantic_Slack_PerSandboxAndOverride_Error`, `_PerSandboxWithoutSlackEnabled_Warning`, `_ArchiveWithoutPerSandbox_Warning`, `_BadChannelOverride_Error`, `_BothChannelsDisabled_Warning`, `_HappyPath_NoErrors`, `_BackwardCompat_Phase62Profile`
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/validate.go` — Warning/error separation in `validateFile()` for both direct and extends-chain paths

## Decisions Made

- **`*bool` for `notifyEmailEnabled`, `notifySlackEnabled`, `slackArchiveOnDestroy`**: nil (field omitted from YAML) is distinguishable from explicit `false`. This enables Phase 62 backward compat: `notifyEmailEnabled` nil → compiler emits no `KM_NOTIFY_EMAIL_ENABLED` env var → hook default of `1` keeps email on (no regression for existing sandboxes). Explicit `false` → compiler explicitly disables email.
- **`IsWarning bool` on `ValidationError` (not a separate `ValidationWarning` type)**: Minimal change that preserves `Error()` interface, doesn't break any existing callers (they still iterate `[]ValidationError`), and gives `km validate` the single bit of information it needs. A separate warning type would require changes to every call site.
- **Five rules in `ValidateSemantic` not JSON schema**: Cross-field constraints (perSandbox vs. channelOverride, archiveOnDestroy without perSandbox) cannot be expressed in JSON schema. Channel-ID regex is intentionally in both layers (belt-and-suspenders with clearer semantic error message).
- **Root schemas/ sync**: The Phase 37-01 decision mandates keeping both schema files in sync. The root `schemas/sandbox_profile.schema.json` was missing the `cli` section entirely; this plan added the full cli block.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Synced root schemas/ with cli section addition**
- **Found during:** Task 1 (JSON schema update)
- **Issue:** Phase 37-01 decision requires both schema files in sync. Root `schemas/sandbox_profile.schema.json` was missing `cli` section entirely (472 lines vs embedded 552+ lines). Adding only to `pkg/profile/schemas/` would leave the root schema giving IDE false errors for all cli fields.
- **Fix:** Added full cli section to root schema including all Phase 62 + Phase 63 fields with correct types and patterns.
- **Files modified:** `schemas/sandbox_profile.schema.json`
- **Verification:** Root schema now has `cli` properties; build passes.
- **Committed in:** `6296db6` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 2 — missing critical sync)
**Impact on plan:** Required for correctness. Phase 37-01 established this invariant. No scope creep.

## Issues Encountered

None — all code followed the plan's action spec exactly. The REQUIREMENTS.md changes (Task 2) were already present from the planning phase.

## Next Phase Readiness

- `CLISpec` has all five Slack fields ready for consumption by 63-02 (compiler env vars), 63-04 (hook heredoc), 63-08 (km create channel provisioning), 63-09 (km destroy archive)
- `*bool` pointer semantics locked in: nil=unset, &false=disabled, &true=enabled — consistent across all three bool-pointer fields
- `IsWarning` mechanism locked in: downstream semantic rules (added in 63-04 and later) can use `IsWarning: true` for additional no-op warnings without any further infrastructure changes
- Key dependency for 63-04 (compiler): reads `cli.NotifyEmailEnabled` (*bool) and `cli.NotifySlackEnabled` (*bool); nil check is the gate for emitting env vars
- Key dependency for 63-08 (km create): reads `NotifySlackPerSandbox`, `NotifySlackChannelOverride` for channel provisioning mode selection

---
*Phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events*
*Completed: 2026-04-29*
