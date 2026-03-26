---
phase: 25-github-source-access-restrictions-deep-testing-of-repo-allowlists-clone-push-enforcement-and-deny-by-default-for-unlisted-repos
plan: 01
subsystem: testing
tags: [github, compiler, deny-by-default, tdd, allowedRepos, permissions, userdata, service-hcl]

# Dependency graph
requires:
  - phase: 24-github-token-infrastructure
    provides: GitHub token infrastructure (compiler.go, service_hcl.go, userdata.go HasGitHub logic)

provides:
  - Deny-by-default enforcement: empty allowedRepos treated identically to nil github config
  - 6 new tests covering permission edge cases and empty-repo scenarios
  - 2 test data profiles (ec2-empty-repos.yaml, ecs-empty-repos.yaml)
  - Production code hardened in 4 files: compiler.go, service_hcl.go, userdata.go, create.go

affects: [phase-26, any phase using sourceAccess.github compilation]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Deny-by-default: guard GitHub infra emission with both != nil AND len(AllowedRepos) > 0"
    - "TDD: write failing tests first, confirm RED, implement fix, confirm GREEN"
    - "Template section comments inside conditional blocks to avoid false positive string matches in tests"

key-files:
  created:
    - pkg/compiler/testdata/ec2-empty-repos.yaml
    - pkg/compiler/testdata/ecs-empty-repos.yaml
  modified:
    - pkg/compiler/compiler_test.go
    - pkg/github/token_test.go
    - pkg/compiler/compiler.go
    - pkg/compiler/service_hcl.go
    - pkg/compiler/userdata.go
    - internal/app/cmd/create.go

key-decisions:
  - "Empty allowedRepos is denied by default: requires both GitHub != nil AND len(AllowedRepos) > 0 to emit token infra"
  - "GIT_ASKPASS section header comment moved inside {{- if .HasGitHub }} block to prevent false positive in string-matching tests"
  - "service_hcl.go HasGitHub guard fixed in both EC2 and ECS paths (was only checking != nil)"

patterns-established:
  - "Deny-by-default gate pattern: `obj != nil && len(obj.Field) > 0` used in compiler.go, service_hcl.go, userdata.go, create.go"

requirements-completed: [GH25-01, GH25-02, GH25-04]

# Metrics
duration: 4min
completed: 2026-03-26
---

# Phase 25 Plan 01: Deny-by-Default Enforcement and Permission Edge Cases Summary

**6 new tests and 4-file production hardening enforce deny-by-default when allowedRepos is empty: no github_token_inputs, no GitHubTokenHCL, no GIT_ASKPASS emitted**

## Performance

- **Duration:** 4 min
- **Started:** 2026-03-26T23:46:24Z
- **Completed:** 2026-03-26T23:50:20Z
- **Tasks:** 2
- **Files modified:** 8

## Accomplishments
- Discovered and fixed the deny-by-default gap: code checked `GitHub != nil` but not `len(AllowedRepos) == 0`, meaning an empty allowedRepos list would still emit token infrastructure
- Added 6 new test functions covering all specified edge cases (empty slice, unknown permissions, wildcard repo names, EC2/ECS deny-by-default, GIT_ASKPASS suppression)
- Hardened production code in 4 locations: compiler.go (EC2 + ECS paths), service_hcl.go (EC2 + ECS paths), userdata.go (HasGitHub param), create.go (token generation gate)
- Full test suite for `pkg/compiler` and `pkg/github` passes; 2 pre-existing failures in `internal/app/cmd` confirmed to be unrelated to these changes

## Task Commits

Each task was committed atomically:

1. **Task 1: Add deny-by-default tests and fix production code** - `71ebeae` (feat)
2. **Task 2: Run full test suite** - no commit needed (read-only verification)

## Files Created/Modified
- `pkg/compiler/testdata/ec2-empty-repos.yaml` - Test profile with non-nil github block and allowedRepos: []
- `pkg/compiler/testdata/ecs-empty-repos.yaml` - Same for ECS substrate
- `pkg/compiler/compiler_test.go` - Added TestCompileEC2EmptyAllowedRepos_DenyByDefault, TestCompileECSEmptyAllowedRepos_DenyByDefault, TestUserDataEmptyAllowedRepos_NoGITASKPASS
- `pkg/github/token_test.go` - Added TestCompilePermissions_EmptySlice, TestCompilePermissions_UnknownPermission, TestExchangeForInstallationToken_WildcardRepoName
- `pkg/compiler/compiler.go` - Hardened EC2 and ECS github-token HCL gate with len(AllowedRepos) > 0
- `pkg/compiler/service_hcl.go` - Hardened HasGitHub assignment in EC2 and ECS generateServiceHCL functions
- `pkg/compiler/userdata.go` - Hardened HasGitHub param and moved GIT_ASKPASS comment inside conditional block
- `internal/app/cmd/create.go` - Hardened Step 13a gate with len(AllowedRepos) > 0

## Decisions Made
- Empty allowedRepos is denied by default using a two-part guard (`!= nil && len() > 0`) across all four code sites
- The GIT_ASKPASS section header comment was moved inside the `{{- if .HasGitHub }}` conditional block to prevent false positive substring matches in `TestUserDataEmptyAllowedRepos_NoGITASKPASS`
- `service_hcl.go` required fixes in both EC2 and ECS paths (plan only called out compiler.go and userdata.go explicitly, but service_hcl.go was the root cause of github_token_inputs appearing in service.hcl)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] service_hcl.go HasGitHub guards also needed fixing**
- **Found during:** Task 1 (GREEN phase — tests still failing after compiler.go fix)
- **Issue:** Plan said to fix compiler.go and userdata.go; service_hcl.go independently controls the `github_token_inputs` block in service.hcl via its own `HasGitHub` parameter. Both EC2 and ECS paths in service_hcl.go checked only `GitHub != nil`.
- **Fix:** Applied the same `len(AllowedRepos) > 0` guard to the `HasGitHub` assignment in both `generateEC2ServiceHCL` and `generateECSServiceHCL` functions.
- **Files modified:** pkg/compiler/service_hcl.go
- **Verification:** TestCompileEC2EmptyAllowedRepos_DenyByDefault and TestCompileECSEmptyAllowedRepos_DenyByDefault both pass.
- **Committed in:** 71ebeae (part of Task 1 commit)

**2. [Rule 1 - Bug] GIT_ASKPASS comment in userdata.go was outside conditional block**
- **Found during:** Task 1 (GREEN phase — TestUserDataEmptyAllowedRepos_NoGITASKPASS still failing after HasGitHub fix)
- **Issue:** Section header comment "# GIT_ASKPASS (if GitHub access is configured)" contained the string "GIT_ASKPASS" and was rendered unconditionally, before `{{- if .HasGitHub }}`. The test used `strings.Contains(UserData, "GIT_ASKPASS")` so the comment triggered a false positive.
- **Fix:** Moved the section header comment to inside the `{{- if .HasGitHub }}` block.
- **Files modified:** pkg/compiler/userdata.go
- **Verification:** TestUserDataEmptyAllowedRepos_NoGITASKPASS passes.
- **Committed in:** 71ebeae (part of Task 1 commit)

---

**Total deviations:** 2 auto-fixed (both Rule 1 bugs)
**Impact on plan:** Both fixes were necessary for the deny-by-default contract to hold completely. No scope creep.

## Issues Encountered
- Pre-existing test failures in `internal/app/cmd` (`TestRunInitWithRunnerAllModules` module ordering, `TestStatusCmd_Found` TTL timestamp format) confirmed to be unrelated to our changes by verifying they fail on the base commit without our changes.

## Next Phase Readiness
- Deny-by-default contract is now fully hardened and tested across all four code paths
- Pre-existing failures in `internal/app/cmd` are out of scope and deferred

## Self-Check: PASSED

All files verified present. All commits verified in git log.

---
*Phase: 25-github-source-access-restrictions-deep-testing-of-repo-allowlists-clone-push-enforcement-and-deny-by-default-for-unlisted-repos*
*Completed: 2026-03-26*
