---
phase: 126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin
plan: 01
subsystem: config
tags: [viper, jsonschema, sandboxprofile, km-config-yaml, validation]

requires: []
provides:
  - "config.LaunchAccountConfig struct + config.Config.LaunchAccounts map, load-bearing on the v2→v merge-list"
  - "config.GetLaunchAccount(name) / GetLaunchAccounts() getters"
  - "profile.RuntimeSpec.LaunchAccount string field + spec.runtime.launchAccount schema property"
  - "pkg/profile.ValidateSemantic: launchAccount + privateSubnet mutual-exclusion gate"
  - "internal/app/cmd.ValidateLaunchAccountLink: config-aware unknown-link error + off-allowlist instance-type warning, wired into both km validate call sites"
affects: [126-02, 126-03, 126-04, 126-05, 126-06, 126-07, 126-08, 126-09, 126-10]

tech-stack:
  added: []
  patterns:
    - "New top-level km-config.yaml block registration: SetDefault (map[string]interface{}{}) + own merge-list entry + UnmarshalKey triad, composite of the clusters (list) and github.commands (map) precedents"
    - "Config-aware profile validation lives in internal/app/cmd, not pkg/profile, to avoid pkg/profile importing internal/app/config (layering)"

key-files:
  created:
    - internal/app/config/config_launch_accounts_test.go
    - pkg/profile/validate_launch_account_test.go
    - internal/app/cmd/launch_account_validate.go
    - internal/app/cmd/launch_account_validate_test.go
  modified:
    - internal/app/config/config.go
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/validate.go
    - internal/app/cmd/validate.go

key-decisions:
  - "launch_accounts is a NEW top-level key (unlike github.commands, which piggybacks on the parent github: merge-list entry) — it needs its own SetDefault/merge-list/UnmarshalKey triad"
  - "The unknown-link-name check lives in internal/app/cmd.ValidateLaunchAccountLink (needs *config.Config), not pkg/profile.ValidateSemantic (profile-only, no config dependency) — avoids a pkg/profile → internal/app/config import"
  - "Off-allowlist instance type is a WARNING, not an error: the launcher role's own IAM policy is the real enforcement point, and an operator authoring locally may not have synced the latest allowlist"

requirements-completed: [REQ-126-CFG]

coverage:
  - id: D1
    description: "km-config.yaml launch_accounts: block survives config.Load() and is reachable via GetLaunchAccount/GetLaunchAccounts, with a merge-list regression guard"
    requirement: "REQ-126-CFG"
    verification:
      - kind: unit
        ref: "internal/app/config/config_launch_accounts_test.go#TestLaunchAccountsConfigLoaded"
        status: pass
      - kind: unit
        ref: "internal/app/config/config_launch_accounts_test.go#TestGetLaunchAccount"
        status: pass
    human_judgment: false
  - id: D2
    description: "spec.runtime.launchAccount is a schema-declared, optional string that parses into RuntimeSpec.LaunchAccount without affecting profiles that omit it"
    requirement: "REQ-126-CFG"
    verification:
      - kind: unit
        ref: "pkg/profile/validate_launch_account_test.go#TestValidateSchema_LaunchAccount"
        status: pass
      - kind: other
        ref: "bash scripts/validate-all-profiles.sh"
        status: pass
    human_judgment: false
  - id: D3
    description: "km validate errors on launchAccount + privateSubnet combination and on an unknown link name; warns on an off-allowlist instance type; is a no-op when launchAccount is absent"
    requirement: "REQ-126-CFG"
    verification:
      - kind: unit
        ref: "pkg/profile/validate_launch_account_test.go#TestValidateSemantic_LaunchAccount_PrivateSubnetMutex"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/launch_account_validate_test.go#TestValidateLaunchAccountLink_UnknownLink"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/launch_account_validate_test.go#TestValidateLaunchAccountLink_OffAllowlistInstanceType"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/launch_account_validate_test.go#TestValidateLaunchAccountLink_Dormant"
        status: pass
      - kind: e2e
        ref: "internal/app/cmd/validate_test.go#TestValidateValidProfile (km binary, full validate path unaffected)"
        status: pass
    human_judgment: false

duration: 25min
completed: 2026-08-22
status: complete
---

# Phase 126 Plan 01: launch_accounts config, spec.runtime.launchAccount schema field, and two validation gates Summary

**km-config.yaml `launch_accounts:` links now round-trip through config.Load() into `config.LaunchAccountConfig`, `spec.runtime.launchAccount` is a first-class schema field, and `km validate` rejects an unknown link name or a launchAccount+privateSubnet combination while warning on an off-allowlist instance type — all zero-AWS, dormant by default.**

## Performance

- **Duration:** ~25 min
- **Tasks:** 3 completed
- **Files modified:** 5 modified, 4 created

## Accomplishments
- `config.LaunchAccountConfig` (14 fields, including the SSM-path-only `ExternalIDSSM` — no plaintext secret field exists on the type) + `config.Config.LaunchAccounts map[string]LaunchAccountConfig`, registered via the SetDefault/merge-list/UnmarshalKey triad (composite of the `clusters` list-shape and `github.commands` map-shape precedents), with `GetLaunchAccount(name)` / `GetLaunchAccounts()` getters.
- `profile.RuntimeSpec.LaunchAccount string` (yaml/json key `launchAccount`) + the matching optional `spec.runtime.launchAccount` JSON-schema property (not added to `required`).
- Two validation gates: a profile-only mutual-exclusion check in `pkg/profile.ValidateSemantic` (launchAccount + privateSubnet), and a config-aware `internal/app/cmd.ValidateLaunchAccountLink` (unknown link → error naming `km account register`; off-allowlist instance type → warning; empty launchAccount → nil, no config lookup at all) wired into both `km validate` call sites (the `extends` path and the raw path).

## Task Commits

Each task was committed atomically:

1. **Task 1: launch_accounts config block, merge-list entry, and getters** - `1f382fd9` (feat)
2. **Task 2: RuntimeSpec.LaunchAccount field + JSON schema property** - `f6ad671f` (feat)
3. **Task 3: two validation gates — profile-only mutual exclusion, and config-aware unknown-link rejection** - `cec50afa` (feat)

_No TDD RED/GREEN split — tests were written and run together with the implementation per task, and every acceptance criterion (including the merge-list-deletion regression check) was hand-verified before commit._

## Files Created/Modified
- `internal/app/config/config.go` - `LaunchAccountConfig` struct, `Config.LaunchAccounts` field, `SetDefault`/merge-list/`UnmarshalKey` triad, `GetLaunchAccount(s)` getters
- `internal/app/config/config_launch_accounts_test.go` - merge-list regression guard + dormancy + getter tests
- `pkg/profile/types.go` - `RuntimeSpec.LaunchAccount` field
- `pkg/profile/schemas/sandbox_profile.schema.json` - `runtime.launchAccount` property
- `pkg/profile/validate.go` - `ValidateSemantic` launchAccount+privateSubnet mutex gate
- `pkg/profile/validate_launch_account_test.go` - schema/unmarshal/dormancy tests + mutex-gate tests
- `internal/app/cmd/launch_account_validate.go` - `ValidateLaunchAccountLink` (config-aware unknown-link + allowlist checks)
- `internal/app/cmd/launch_account_validate_test.go` - unit tests for all four `ValidateLaunchAccountLink` behaviors
- `internal/app/cmd/validate.go` - wired `ValidateLaunchAccountLink` into both the extends-path and raw-path result sites

## Decisions Made
- `launch_accounts` is a brand-new top-level key, so it gets its own merge-list entry (unlike `github.commands`, which piggybacks on the parent `github` entry) — documented in an adjacent comment naming the `silently`/`merge-list` footgun, matching the discipline of the `network.nat_gateway` entry it sits next to.
- The unknown-link-name check could not live inside `ValidateSemantic` (single-parameter signature, no `*config.Config`) without either changing that signature across three call sites or making `pkg/profile` import `internal/app/config` (a layering violation) — so it's a standalone exported function in `internal/app/cmd` instead, called explicitly from `validate.go`.
- Off-allowlist instance type is a warning, not a hard error, because the actual enforcement point is the launcher role's IAM policy in the target account — `km validate` runs locally and may be checking against a stale copy of the allowlist.

## Deviations from Plan

None - plan executed exactly as written. All `must_haves.truths`, `artifacts`, and `key_links` from the plan frontmatter are satisfied; every acceptance criterion in all three tasks was checked directly (including the merge-list-deletion regression proof, done via a temporary `sed` removal + test run + restore, not committed).

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required. This plan is pure Go/schema/config code with zero AWS calls.

## Next Phase Readiness
Every later Phase 126 plan can resolve a link record through `config.LaunchAccountConfig` / `cfg.GetLaunchAccount(name)`, and every profile can declare `spec.runtime.launchAccount` and have it both parse and validate correctly. No blockers for Plan 02 (`km account add/rm` + the enrollment Terraform unit) or Plan 06 (the create-path AZ sweep that consumes `SubnetIDs`/`AvailabilityZones`).

---
*Phase: 126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin*
*Completed: 2026-08-22*

## Self-Check: PASSED

All 5 created files found on disk (`internal/app/config/config_launch_accounts_test.go`,
`pkg/profile/validate_launch_account_test.go`, `internal/app/cmd/launch_account_validate.go`,
`internal/app/cmd/launch_account_validate_test.go`, this SUMMARY.md). All 4 commits found in
`git log` (`1f382fd9`, `f6ad671f`, `cec50afa`, `4d37b662`).
