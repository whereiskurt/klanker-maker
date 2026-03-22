---
phase: 06-budget-enforcement-platform-configuration
plan: 01
subsystem: config
tags: [viper, cobra, km-config, configure-wizard, platform-config, aws-accounts, sso]

requires:
  - phase: 05-configui
    provides: "Working km CLI with Cobra command tree and config.Load() pattern"

provides:
  - "Extended Config struct with Domain, ManagementAccountID, TerraformAccountID, ApplicationAccountID, SSOStartURL, SSORegion, PrimaryRegion, BudgetTableName"
  - "Load() merges km-config.yaml from repo root on top of ~/.km/config.yaml"
  - "km configure wizard (interactive + --non-interactive) writes km-config.yaml"
  - "km bootstrap stub validates km-config.yaml and prints dry-run infrastructure plan"
  - "km-config.yaml.example with full field documentation"

affects:
  - 06-budget-enforcement-platform-configuration
  - plans that use Domain or AccountIDs for multi-account routing

tech-stack:
  added: []
  patterns:
    - "Two-viper merge pattern: primary ~/.km/config.yaml + secondary km-config.yaml with env var precedence guard via isSetByEnv()"
    - "io.Reader/io.Writer injection on wizard command for test isolation without real terminal"
    - "findKMConfigPath: check cwd before repo root anchor for portable test execution"

key-files:
  created:
    - internal/app/config/config_test.go
    - internal/app/cmd/configure.go
    - internal/app/cmd/configure_test.go
    - internal/app/cmd/bootstrap.go
    - km-config.yaml.example
  modified:
    - internal/app/config/config.go
    - internal/app/cmd/root.go

key-decisions:
  - "Two-viper merge pattern: v1 loads ~/.km/config.yaml, v2 loads km-config.yaml, platform keys merged with isSetByEnv() guard so KM_* env vars always win"
  - "BudgetTableName defaults to 'km-budgets' so budget enforcement plans have a usable default without mandatory configuration"
  - "km configure wizard accepts io.Reader/io.Writer for testability — same pattern as aws configure simplicity goal from research"
  - "bootstrap uses findKMConfigPath() (cwd first, then repo root) so tests that run the binary from a temp dir with km-config.yaml work without hacking GOPATH"

patterns-established:
  - "Config extension pattern: add fields to Config struct, add SetDefault in Load(), read with v.GetString(key), map km-config.yaml keys via v2 merge"
  - "Wizard test pattern: buildKM() + exec.Command with --non-interactive flags + temp dir for output + YAML parse verification"

requirements-completed: [CONF-01, CONF-03, CONF-04]

duration: 8min
completed: 2026-03-22
---

# Phase 06 Plan 01: Platform Config Wizard Summary

**Extended Config struct with 8 platform fields loaded from km-config.yaml, km configure wizard with topology detection and DNS delegation guidance, and km bootstrap dry-run stub**

## Performance

- **Duration:** 8 min
- **Started:** 2026-03-22T20:12:11Z
- **Completed:** 2026-03-22T20:19:36Z
- **Tasks:** 2
- **Files modified:** 7

## Accomplishments

- Config struct extended with Domain, ManagementAccountID, TerraformAccountID, ApplicationAccountID, SSOStartURL, SSORegion, PrimaryRegion, BudgetTableName — all loaded from km-config.yaml with env var override
- km configure wizard: interactive + --non-interactive modes; detects 2-account vs 3-account topology; prints DNS delegation guidance for 3-account setups
- km bootstrap stub: validates km-config.yaml exists, prints dry-run plan describing S3 bucket, DynamoDB lock table, KMS key, and budget DynamoDB table

## Task Commits

1. **Task 1: Extend Config struct and km-config.yaml loading** - `67216b7` (feat)
2. **Task 2: km configure wizard + km bootstrap stub** - `45bcca4` (feat)

## Files Created/Modified

- `internal/app/config/config.go` - Extended Config struct + two-viper merge in Load()
- `internal/app/config/config_test.go` - 4 TDD tests for platform field loading, backward compat, defaults, env override
- `internal/app/cmd/configure.go` - Interactive wizard + --non-interactive mode; topology detection; DNS delegation message
- `internal/app/cmd/configure_test.go` - 3 TDD tests for non-interactive write, 2-account/3-account topology detection
- `internal/app/cmd/bootstrap.go` - Stub with findKMConfigPath(), dry-run output, config.Load() validation
- `internal/app/cmd/root.go` - NewConfigureCmd + NewBootstrapCmd registered
- `km-config.yaml.example` - Fully documented example config file

## Decisions Made

- Two-viper merge pattern: v1 loads `~/.km/config.yaml`, v2 loads `./km-config.yaml`, platform keys merged with `isSetByEnv()` guard so `KM_*` env vars always win. This preserves the existing config layering while adding repo-root platform config without changing the primary config file path.
- `BudgetTableName` defaults to `"km-budgets"` — budget enforcement plans have a usable default without mandatory configuration.
- `km configure` accepts `io.Reader`/`io.Writer` for testability; tests use `--non-interactive` flags to avoid terminal dependency.
- `bootstrap` uses `findKMConfigPath()` (cwd first, then repo root anchor) so test binaries run from temp dirs pick up the test's km-config.yaml.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed env var precedence over km-config.yaml**
- **Found during:** Task 1 (Config loading implementation)
- **Issue:** The initial merge implementation used `v.Set(key, v2.Get(key))` after `AutomaticEnv()`, which overwrote env var values with km-config.yaml file values
- **Fix:** Added `isSetByEnv()` helper that checks `KM_*` env var directly; km-config.yaml values only merged when env var is not set
- **Files modified:** internal/app/config/config.go
- **Verification:** TestLoadEnvOverride passes — KM_DOMAIN overrides domain in km-config.yaml
- **Committed in:** 67216b7 (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 bug fix)
**Impact on plan:** Required fix for correct precedence behavior. No scope creep.

## Issues Encountered

- Linter reverted config_test.go's `TestLoadBackwardCompat` Domain check to `cfg.Domain != ""` — this is consistent with the linter adding `v.SetDefault("domain", "klankermaker.ai")` to config.go. Test still passes since Domain now has a default.
- Pre-existing `TestDestroyCmd_InvalidSandboxID` failures logged to `deferred-items.md` — unrelated to this plan.

## Next Phase Readiness

- Platform config foundation complete — Plans 02-07 can read `cfg.Domain`, `cfg.ManagementAccountID`, etc.
- km-config.yaml.example ready for operators forking the platform
- km bootstrap is a stub; full provisioning implementation is a future plan

---
*Phase: 06-budget-enforcement-platform-configuration*
*Completed: 2026-03-22*

## Self-Check: PASSED

- FOUND: internal/app/config/config.go
- FOUND: internal/app/config/config_test.go
- FOUND: internal/app/cmd/configure.go
- FOUND: internal/app/cmd/configure_test.go
- FOUND: internal/app/cmd/bootstrap.go
- FOUND: km-config.yaml.example
- FOUND: 06-01-SUMMARY.md
- FOUND commit 67216b7: feat(06-01): extend Config struct with platform fields and km-config.yaml loading
- FOUND commit 45bcca4: feat(06-01): add km configure wizard and km bootstrap stub
