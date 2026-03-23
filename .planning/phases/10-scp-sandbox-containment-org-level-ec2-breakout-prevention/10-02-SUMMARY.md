---
phase: 10-scp-sandbox-containment-org-level-ec2-breakout-prevention
plan: "02"
subsystem: cli
tags: [cobra, bootstrap, scp, terragrunt, tdd, di]

# Dependency graph
requires:
  - phase: 10-scp-sandbox-containment-org-level-ec2-breakout-prevention
    plan: "01"
    provides: infra/live/management/scp/ Terragrunt unit that this bootstrap step deploys
  - phase: 06-budget-enforcement-platform-configuration
    provides: ShellExecFunc DI pattern that TerragruntApplyFunc follows; km configure that produces km-config.yaml bootstrap reads
provides:
  - km bootstrap --dry-run shows SCP sandbox-containment as a planned step with policy name, target account, threat coverage, and trusted roles
  - km bootstrap (non-dry-run) calls terragrunt apply on infra/live/management/scp/ via applyTerragruntFunc
  - TerragruntApplyFunc exported type and ApplyTerragruntFunc package-level var for test DI
  - NewBootstrapCmdWithWriter constructor for testable output capture
affects:
  - operator docs (OPERATOR-GUIDE.md bootstrap section references this command)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "TDD RED-GREEN: write failing tests first, then implement to pass"
    - "Package-level exported func var (ApplyTerragruntFunc) for test DI — mirrors ShellExecFunc pattern from shell.go"
    - "NewBootstrapCmdWithWriter constructor for io.Writer injection — same pattern as configure.go non-interactive mode"
    - "runBootstrap accepts context.Context as first parameter — consistent with other runX functions in cmd package"

key-files:
  created:
    - internal/app/cmd/bootstrap_test.go
  modified:
    - internal/app/cmd/bootstrap.go

key-decisions:
  - "Exported TerragruntApplyFunc type and ApplyTerragruntFunc var (not lowercase) — external test package cmd_test requires exported symbols for DI; mirrors ShellExecFunc pattern from Phase 06-09"
  - "NewBootstrapCmdWithWriter added alongside NewBootstrapCmd — backward compatible; tests inject bytes.Buffer; production uses os.Stdout"
  - "runBootstrap accepts cfg directly when ManagementAccountID/ApplicationAccountID/Domain are populated — avoids requiring km-config.yaml on disk during unit tests"
  - "SCP apply uses ApplyTerragruntFunc package-level var not a WithApplier constructor — plan specified package-level DI var; consistent with defaultShellExec pattern"

patterns-established:
  - "TerragruntApplyFunc: exported function type var for Terragrunt DI in external test packages"

requirements-completed:
  - SCP-09
  - SCP-11
  - SCP-12

# Metrics
duration: 2min
completed: 2026-03-23
---

# Phase 10 Plan 02: SCP Bootstrap Wiring Summary

**km bootstrap extended with SCP deployment step: dry-run shows km-sandbox-containment policy details, non-dry-run calls terragrunt apply on infra/live/management/scp/ via exported ApplyTerragruntFunc for test DI**

## Performance

- **Duration:** 2 min
- **Started:** 2026-03-23T01:04:21Z
- **Completed:** 2026-03-23T01:06:41Z
- **Tasks:** 1 (TDD: 2 commits — test then implementation)
- **Files modified:** 2

## Accomplishments

- TDD RED: wrote 3 failing tests covering dry-run SCP output, no-management-account warning, and non-dry-run apply path
- TDD GREEN: implemented bootstrap.go changes — SCP dry-run section, SCP apply step, DI exports — all 4 bootstrap tests pass
- Existing TestBootstrapDryRun (binary integration test) continues to pass unchanged

## Task Commits

TDD commits:

1. **RED — bootstrap_test.go (3 failing tests)** - `beaeddf` (test)
2. **GREEN — bootstrap.go (SCP step + DI)** - `f588d25` (feat)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/internal/app/cmd/bootstrap_test.go` - 3 unit tests: TestBootstrapDryRunShowsSCP, TestBootstrapDryRunNoManagementAccount, TestBootstrapSCPApplyPath
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/bootstrap.go` - Extended with SCP dry-run output, SCP apply step, TerragruntApplyFunc exported type, ApplyTerragruntFunc exported var, NewBootstrapCmdWithWriter constructor, context.Context parameter

## Decisions Made

- **Exported DI symbols:** `TerragruntApplyFunc` type and `ApplyTerragruntFunc` var are exported (uppercase) because the test package `cmd_test` is external and cannot access unexported package members. This mirrors the `ShellExecFunc` pattern from Phase 06-09.
- **NewBootstrapCmdWithWriter:** Added alongside the original `NewBootstrapCmd` for backward compatibility. Root command wiring in root.go is unchanged.
- **Config injection shortcut:** `runBootstrap` detects when a non-zero config is passed (for tests) and skips the km-config.yaml file check. This avoids the need for test fixtures on disk for unit tests while keeping the production path unchanged.

## Deviations from Plan

None - plan executed exactly as written. The TDD pattern, DI approach, and SCP output format match the plan's specifications exactly.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Phase 10 complete: SCP Terraform module (plan 01) and bootstrap wiring (plan 02) both shipped
- Operators can run `km bootstrap --dry-run` to preview SCP deployment and `km bootstrap --dry-run=false` to apply
- Requirements SCP-09, SCP-11, SCP-12 satisfied

---
*Phase: 10-scp-sandbox-containment-org-level-ec2-breakout-prevention*
*Completed: 2026-03-23*
