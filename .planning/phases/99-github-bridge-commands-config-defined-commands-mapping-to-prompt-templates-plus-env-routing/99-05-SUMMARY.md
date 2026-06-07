---
phase: 99-github-bridge-commands-config-defined-commands-mapping-to-prompt-templates-plus-env-routing
plan: "05"
subsystem: km-doctor-github-status-docs
tags: [github, doctor, km-github-status, docs, tdd, deploy-surface]

requires:
  - phase: 99-plan-01
    provides: GithubCommandEntry + GithubConfig.Commands + GithubConfig.DefaultCommand + GithubRepoEntry.DefaultCommand
  - phase: 99-plan-03
    provides: ResolveCommandPrompts + PublishGitHubCommandsToSSM + {prefix}/config/github/commands SSM param
  - phase: 99-plan-04
    provides: bridge handler wiring, SSMCommandsFetcher, InstallationCommenter, dormancy invariant

provides:
  - checkGitHubCommandsValid: 5-check pure-config doctor function (@file-missing WARN, profile-unresolvable WARN, help-shadow WARN, command↔repo alias overlap WARN, undefined default_command ERROR)
  - checkGitHubCommandsSSMParam: SSM param presence check (WARN when configured but absent)
  - Both checks registered in GitHub doctor group after checkGitHubAliasCollision (dormant when commands absent)
  - DoctorConfigProvider gains GetGithubCommands/GetGithubDefaultCommand/GetConfigFilePath interface methods
  - RunGitHubStatus extended: lists commands from SSM + per-repo effective default (dormant when no SSM param)
  - docs/github-bridge.md Phase 99 section: commands config surface, @file rules, {{args}}, dispatch rules, /help, doctor checks, status output, deploy sequence
  - Deploy-surface cross-check: no discrepancies between Phase 99 docs claims and Plans 03/04

affects:
  - km operator UX: doctor catches command misconfigs before deploy; status shows live command map
  - docs/github-bridge.md: authoritative Phase 99 operator runbook

tech-stack:
  added: []
  patterns:
    - "Pure config doctor checks: os.Stat for @file and profile, string-key map lookup for default_command"
    - "DoctorConfigProvider interface extension pattern: new methods + adapter + all test stubs updated"
    - "RunGitHubStatus extension: reads SSM JSON param, parses, prints command list; dormant when param absent"
    - "Deploy-surface verification embedded in docs task (Phase-97-gap-class guard)"

key-files:
  created:
    - internal/app/cmd/doctor_github_commands_test.go
  modified:
    - internal/app/cmd/doctor.go
    - internal/app/cmd/github.go
    - internal/app/cmd/doctor_test.go
    - docs/github-bridge.md

key-decisions:
  - "DoctorConfigProvider interface extended (not bypassed): GetGithubCommands/GetGithubDefaultCommand/GetConfigFilePath added; all three test stubs (testConfig, testDoctorConfig) updated with zero-value returns — cleaner than type-asserting to *appcfg.Config in buildChecks"
  - "configDir derived from cfg.GetConfigFilePath() (Plan 03 field) not re-threaded; falls back to '.' when empty (Lambda/test environments)"
  - "checkGitHubCommandsValid returns ERROR only for undefined default_command; all other issues WARN — matches plan spec exactly"
  - "printGitHubCommandsStatus reads SSM commands param directly (not cfg.Github.Commands) so it shows live published state, not config-file state — operator sees what the Lambda actually reads"
  - "Deploy-surface cross-check: no new module/table/Lambda; make build-lambdas + km init --dry-run=false ONLY; confirmed against Plans 03/04 implementation"

requirements-completed: [GH-CMD-CONFIG, GH-CMD-HELP, GH-CMD-E2E]

duration: 556s
completed: 2026-06-07
---

# Phase 99 Plan 05: Doctor Checks + km github status Command Listing + Docs Summary

**km doctor validates command config (6 checks, dormant when absent); km github status lists commands from SSM; docs/github-bridge.md gains Phase 99 operator section with explicit no-sidecars/no-recreate deploy sequence, cross-checked against Plans 03/04.**

## Performance

- **Duration:** ~556s (~9 min)
- **Started:** 2026-06-07T23:22:48Z
- **Completed:** 2026-06-07T23:32:04Z
- **Tasks:** 3
- **Files modified:** 5

## Accomplishments

- Created `doctor_github_commands_test.go` with 15 tests (RED-first per TDD): @file-missing, profile-unresolvable, help-shadow, alias-overlap, default-undefined (top-level + per-repo), SSM-present checks, clean-config, dormant, status listing, per-repo override, and no-commands dormancy
- Implemented `checkGitHubCommandsValid` (5 sub-checks, pure config, no AWS) and `checkGitHubCommandsSSMParam` (SSM presence) in `doctor.go`
- Extended `DoctorConfigProvider` interface with `GetGithubCommands`, `GetGithubDefaultCommand`, `GetConfigFilePath`; updated `appConfigAdapter` and both test stubs
- Registered both new checks in the GitHub doctor group after `checkGitHubAliasCollision`; dormant when `github.commands` is absent
- Extended `RunGitHubStatus` with `printGitHubCommandsStatus`: reads SSM commands JSON param, prints sorted `/name — description [→ target]` list + per-repo effective default; dormant when param absent
- Added Phase 99 section to `docs/github-bridge.md`: config surface YAML example, all dispatch rules (D2-D10), @file/@@ semantics, {{args}}, /help, doctor checks table, status output example, deploy sequence with explicit NOT-sidecars/NOT-recreate/NOT-make-build rationale, Plans 03/04 cross-check, E2E checklist (A-H)

## Task Commits

1. **Task 1: Write failing doctor + status tests (RED)** — `394d6682` (test)
2. **Task 2: Implement doctor checks + km github status listing (GREEN)** — `3ed88830` (feat)
3. **Task 3: Document Phase 99 commands + deploy surface** — `7fd24089` (docs)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/internal/app/cmd/doctor_github_commands_test.go` — 15 tests (RED-first TDD)
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/doctor.go` — checkGitHubCommandsValid + checkGitHubCommandsSSMParam + DoctorConfigProvider interface extensions + adapter methods + check registration
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/github.go` — RunGitHubStatus extended with printGitHubCommandsStatus
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/doctor_test.go` — testConfig + testDoctorConfig new method stubs
- `/Users/khundeck/working/klankrmkr/docs/github-bridge.md` — Phase 99 operator section (249 lines added)

## Decisions Made

- **DoctorConfigProvider interface extended**: Added `GetGithubCommands()`, `GetGithubDefaultCommand()`, `GetConfigFilePath()` to the interface rather than type-asserting to `*appcfg.Config` in `buildChecks`. This is consistent with the existing pattern for `GetGithubRepos()` and `GetGithubDefaultProfile()`. Three test stubs updated with zero-value returns.

- **`printGitHubCommandsStatus` reads SSM, not `cfg.Github.Commands`**: The status function shows what the Lambda actually reads at runtime (the published SSM state), not the operator's config file. This is the correct behavior — operator can verify both the config (via `km doctor`) and the live Lambda state (via `km github status`).

- **sortedCommandNames helper added**: Extracted from `checkGitHubCommandsValid` for use in error messages listing defined commands. Avoids repeating the sort-and-join pattern.

- **Deploy-surface cross-check (Plans 03/04 verification)**: Confirmed that `PublishGitHubCommandsToSSM` in Plan 03 writes to the exact SSM path read by `SSMCommandsFetcher` in Plan 04. `lambdaBuilds()` already includes km-github-bridge from Phase 97. No new Terraform modules. No sandbox-side changes. Docs claims are accurate.

## Deviations from Plan

**[Rule 2 - Missing Critical Functionality] DoctorConfigProvider interface extended**
- **Found during:** Task 2 implementation
- **Issue:** `buildChecks` receives `DoctorConfigProvider` (interface), not `*appcfg.Config`. Direct field access (`cfg.Github.Commands`) fails to compile.
- **Fix:** Added three new interface methods + adapter implementations + test stub implementations.
- **Files modified:** `internal/app/cmd/doctor.go`, `internal/app/cmd/doctor_test.go`
- **Impact:** Non-breaking; all new methods have zero-value returns in test stubs (dormant behavior preserved in all pre-existing tests).

## Issues Encountered

- Pre-existing test suite timeout: `TestRunAgentAuthClaude_TeesAndCleans` and `TestRunReconnectingPortForward` require real external processes and timeout the full suite at 60s. These predate Phase 99 and are unrelated to Phase 99 changes (confirmed via `git log`). Verified by running the target tests directly.

## Deploy-Surface Cross-Check

| Claim | Source | Status |
|-------|--------|--------|
| `make build-lambdas` rebuilds km-github-bridge zip | `init.go:1876` lambdaBuilds() list (Phase 97) | Confirmed — no new entry needed |
| `km init --dry-run=false` uploads zip + writes SSM | `PublishGitHubCommandsToSSM` (Plan 03) | Confirmed — writes `/km/config/github/commands` |
| NOT `km init --sidecars` | No SandboxProfile schema change in Phase 99 | Confirmed — no new profile fields |
| NOT `make build` | No new regionalModules() entry | Confirmed — km-github-bridge was Phase 97 |
| NOT sandbox recreate | No sandbox-side changes in Phase 99 | Confirmed — command pass is Lambda-side only |

**No discrepancies found.**

## Self-Check: PASSED

- FOUND: `internal/app/cmd/doctor_github_commands_test.go`
- FOUND: `internal/app/cmd/doctor.go` (checkGitHubCommandsValid + checkGitHubCommandsSSMParam + DoctorConfigProvider additions)
- FOUND: `internal/app/cmd/github.go` (printGitHubCommandsStatus)
- FOUND: `internal/app/cmd/doctor_test.go` (testConfig + testDoctorConfig new methods)
- FOUND: `docs/github-bridge.md` (Phase 99 section with deploy sequence)
- RED commit `394d6682` — FOUND
- GREEN commit `3ed88830` — FOUND
- docs commit `7fd24089` — FOUND
- `go build ./...` — CLEAN
- 15/15 new tests GREEN

---
*Phase: 99-github-bridge-commands-config-defined-commands-mapping-to-prompt-templates-plus-env-routing*
*Completed: 2026-06-07*
