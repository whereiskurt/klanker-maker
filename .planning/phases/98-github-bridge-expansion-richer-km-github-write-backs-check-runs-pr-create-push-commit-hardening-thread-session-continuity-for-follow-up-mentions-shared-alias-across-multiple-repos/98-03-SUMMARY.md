---
phase: 98-github-bridge-expansion
plan: 03
subsystem: infra
tags: [github, doctor, alias, bridge, resolve, shared-alias, km-config]

# Dependency graph
requires:
  - phase: 98-00
    provides: Wave-0 scaffolding — resolve_phase98_test.go stub behind phase98_wave0 build tag, doctor.go GitHub checks wired from Phase 97
provides:
  - TestResolve_SharedAlias characterization test (GREEN, tag removed) proving shared-alias dispatch
  - DetectGitHubAliasIssues pure helper detecting alias collision + match-overlap misconfiguration
  - checkGitHubAliasCollision doctor check wired into km doctor run
  - TestDoctorGitHubAliasCollision (4 cases) confirming all behavior branches
affects:
  - 98-04 (thread continuity — wires dynamodb-github-threads module; doctor tests already GREEN for that guard)
  - operator alias configuration patterns going forward

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Pure config-scan helper (DetectGitHubAliasIssues) extracted for testability without live AWS — matches checkGitHubReposResolvable pattern"
    - "Glob matching via segment-level recursion to mirror resolve.go path.Match semantics without importing path package into doctor.go"
    - "Doctor checks wired via closure capturing githubRepos / githubConfigured — dormant-by-default when github.repos absent"

key-files:
  created:
    - pkg/github/bridge/resolve_phase98_test.go (tag removed — now always-on characterization test)
  modified:
    - internal/app/cmd/doctor.go (DetectGitHubAliasIssues + matchGlob/matchSegment/matchRunePattern helpers + checkGitHubAliasCollision + wired check)
    - internal/app/cmd/init_test.go (TestDoctorGitHubAliasCollision added)

key-decisions:
  - "Used appcfg.GithubRepoEntry (not bridge.RepoEntry) in DetectGitHubAliasIssues — consistent with existing checkGitHubReposResolvable pattern; avoids importing pkg/github/bridge into doctor.go"
  - "Glob matching implemented inline (matchRunePattern) rather than importing path package — keeps doctor.go imports clean; handles the common owner/* pattern used in practice"
  - "Intentional shared alias (multiple entries with same explicit alias) produces NO warning — the supported GH-X-SHARED feature; only implicit-vs-explicit collision warns"

patterns-established:
  - "Pure helper + doctor check separation: DetectGitHubAliasIssues is the testable pure function; checkGitHubAliasCollision wraps it in CheckResult formatting"
  - "Dormant-by-default doctor check: returns CheckSkipped when github.repos absent, matching Phase 97 GitHub check group gate"

requirements-completed: [GH-X-SHARED]

# Metrics
duration: 8min
completed: 2026-06-07
---

# Phase 98 Plan 03: Shared-Alias Characterization + km doctor Alias-Collision Check Summary

**Shared-alias dispatch confirmed via GREEN characterization test; km doctor now WARNs on alias collisions and overlapping match patterns while staying silent on intentional shared-sandbox aliases and absent config**

## Performance

- **Duration:** ~8 min
- **Started:** 2026-06-07T14:45:00Z
- **Completed:** 2026-06-07T14:51:00Z
- **Tasks:** 2 (Task 1: characterization test; Task 2: TDD alias collision/overlap)
- **Files modified:** 3

## Accomplishments

- Removed `//go:build phase98_wave0` from `resolve_phase98_test.go` — `TestResolve_SharedAlias` now runs unconditionally and is GREEN (resolve.go already supported shared aliases)
- Added `DetectGitHubAliasIssues([]appcfg.GithubRepoEntry) []string` pure helper to doctor.go with two detection classes: unintentional alias collision (auto-default equals explicit alias on another entry) and overlapping match (exact + glob covering same repo)
- Wired `checkGitHubAliasCollision` into the km doctor run alongside the existing `checkGitHubReposResolvable` check; silent when `github.repos` absent (dormant-by-default)
- Added `TestDoctorGitHubAliasCollision` (4 sub-tests) to `init_test.go` covering all four behavioral branches per plan spec

## Task Commits

1. **Task 1: Shared-alias characterization test** — `d7f996a7` (test — remove wave0 build tag)
2. **Task 2 RED: TestDoctorGitHubAliasCollision** — `ed867b57` (test — failing stub)
3. **Task 2 GREEN: DetectGitHubAliasIssues + doctor wiring** — `4d2b3afe` (feat — all 4 cases pass)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/github/bridge/resolve_phase98_test.go` — build tag removed; test is now always-on GREEN characterization
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/doctor.go` — DetectGitHubAliasIssues helper + matchGlob/matchSegment/matchRunePattern + checkGitHubAliasCollision function + wired into doctor run
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/init_test.go` — TestDoctorGitHubAliasCollision (4 sub-tests) added

## Decisions Made

- Used `appcfg.GithubRepoEntry` (not `bridge.RepoEntry`) for the helper type — consistent with how `checkGitHubReposResolvable` already works; avoids pulling `pkg/github/bridge` into `doctor.go` imports
- Implemented glob matching inline rather than importing the `path` package — handles the common `owner/*` case used in practice and keeps the import list clean
- Intentional shared alias (two or more entries with the same explicit `alias:` value) produces NO warning — this is the GH-X-SHARED supported feature; only implicit-vs-explicit alias collision warns

## Deviations from Plan

None — plan executed exactly as written. `resolve.go` needed no changes (characterization test was GREEN immediately as expected).

## Issues Encountered

None.

## Next Phase Readiness

- 98-03 is complete; `TestRegionalModulesIncludesGitHubThreads` remains RED (per plan 98-00 guard) — ready for 98-04 to add `dynamodb-github-threads` to `regionalModules()`
- `DetectGitHubAliasIssues` is available for future plans that need alias-configuration validation

---
*Phase: 98-github-bridge-expansion*
*Completed: 2026-06-07*
