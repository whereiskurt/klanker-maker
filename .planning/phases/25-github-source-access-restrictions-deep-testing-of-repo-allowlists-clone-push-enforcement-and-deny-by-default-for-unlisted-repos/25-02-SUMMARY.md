---
phase: 25-github-source-access-restrictions-deep-testing-of-repo-allowlists-clone-push-enforcement-and-deny-by-default-for-unlisted-repos
plan: 02
subsystem: testing
tags: [github, compiler, allowedRefs, pre-push-hook, userdata, tdd, security-docs, ec2]

# Dependency graph
requires:
  - phase: 25-github-source-access-restrictions-deep-testing-of-repo-allowlists-clone-push-enforcement-and-deny-by-default-for-unlisted-repos
    plan: 01
    provides: Deny-by-default enforcement, HasGitHub logic, GIT_ASKPASS credential helper in userdata.go

provides:
  - Pre-push hook injection in EC2 user-data when allowedRefs is non-empty
  - KM_ALLOWED_REFS env var (colon-separated) exported in EC2 bootstrap
  - git config --system core.hooksPath /opt/km/hooks installed during bootstrap
  - 5 new ref enforcement tests proving correctness
  - Test profile ec2-with-allowed-refs.yaml with allowedRefs: [main, feature/*]
  - Accurate security-model.md documentation for AllowedRefs enforcement and ECS gap

affects: [phase-26, any phase using sourceAccess.github allowedRefs compilation]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Pre-push hook enforcement: KM_ALLOWED_REFS colon-separated, bash glob matching via [[ \"$branch\" == $pattern ]]"
    - "HasAllowedRefs gate: GitHub != nil && len(AllowedRefs) > 0 (mirrors deny-by-default pattern from Plan 01)"
    - "TDD: write failing tests first (RED), implement (GREEN), all 5 new tests pass"

key-files:
  created:
    - pkg/compiler/testdata/ec2-with-allowed-refs.yaml
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/compiler_test.go
    - docs/security-model.md

key-decisions:
  - "AllowedRefs enforcement is EC2-only: ECS has no user-data bootstrap and therefore no pre-push hook"
  - "KM_ALLOWED_REFS uses colon separator to avoid conflicts with space/comma in branch name patterns"
  - "pre-push hook lives at /opt/km/hooks/ and uses git config --system core.hooksPath (system-wide, applies to all users)"
  - "AllowedRefs is defense-in-depth only: primary control remains token scoping by allowedRepos"

patterns-established:
  - "AllowedRefs hook gate: HasAllowedRefs bool derived from GitHub != nil && len(AllowedRefs) > 0 in userDataParams"
  - "joinAllowedRefs() helper: converts []string to colon-separated string, returns empty string for nil/empty input"

requirements-completed: [GH25-03, GH25-05]

# Metrics
duration: 4min
completed: 2026-03-26
---

# Phase 25 Plan 02: AllowedRefs Enforcement via Pre-Push Hook Summary

**EC2 user-data now injects a git pre-push hook using bash glob matching (KM_ALLOWED_REFS colon-separated) plus accurate security documentation for AllowedRefs limits and ECS credential gap**

## Performance

- **Duration:** 4 min
- **Started:** 2026-03-26T23:57:25Z
- **Completed:** 2026-03-26T23:57:25Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments
- Implemented `HasAllowedRefs` and `AllowedRefs` fields in `userDataParams` with a `joinAllowedRefs()` helper that produces a colon-separated pattern string
- Injected section 4b of EC2 user-data: exports `KM_ALLOWED_REFS`, writes `/opt/km/hooks/pre-push` bash hook, runs `git config --system core.hooksPath /opt/km/hooks`
- Added 5 new tests via TDD (RED/GREEN): env var presence, env var absence, hook presence, hook absence, nil allowedRefs — all pass
- Updated `docs/security-model.md` Section 9 to accurately document the enforcement mechanism, EC2-only limitation, `--no-verify` bypass risk, and ECS GitHub credential gap

## Task Commits

Each task was committed atomically:

1. **Task 1: Implement ref enforcement in user-data and add tests** - `0516b52` (feat)
2. **Task 2: Update security documentation and run full suite** - `816bff7` (docs)

**Plan metadata:** `a84b688` (docs: complete plan)

## Files Created/Modified
- `pkg/compiler/testdata/ec2-with-allowed-refs.yaml` - Test profile with allowedRefs: [main, feature/*]
- `pkg/compiler/userdata.go` - Added HasAllowedRefs/AllowedRefs fields, joinAllowedRefs() helper, section 4b template block
- `pkg/compiler/compiler_test.go` - Added 5 new test functions: TestUserDataAllowedRefsEnvVar, TestUserDataEmptyAllowedRefs_NoEnvVar, TestUserDataPrePushHookPresent, TestUserDataPrePushHookAbsent, TestUserDataNilAllowedRefs_NoHook
- `docs/security-model.md` - Rewrote Section 9 enforcement paragraph, added AllowedRefs Enforcement subsection with implementation details, limitations, and ECS credential gap documentation

## Decisions Made
- AllowedRefs enforcement is EC2-only: ECS has no user-data bootstrap, so the pre-push hook cannot be installed. Documented as known v1 limitation in security-model.md.
- Colon separator for KM_ALLOWED_REFS: avoids conflicts with spaces or commas that might appear in exotic branch names; colon is not valid in git ref names so it is safe to use as a delimiter.
- git config --system core.hooksPath (system-wide): applies to all users on the instance, not just the sandbox user. This prevents the agent from bypassing the hook by running git operations as a different user.
- AllowedRefs is defense-in-depth: the primary access control is token scoping by allowedRepos. AllowedRefs prevents push to unlisted branches as a secondary layer.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
- Pre-existing test failures in `internal/app/cmd` (`TestRunInitWithRunnerAllModules` module ordering, `TestStatusCmd_Found` TTL timestamp format) confirmed unrelated to our changes (same failures as in Plan 01 baseline).

## Next Phase Readiness
- Ref enforcement for EC2 is complete: user-data injects pre-push hook and KM_ALLOWED_REFS when allowedRefs is non-empty
- ECS AllowedRefs enforcement remains a documented gap for a future phase
- All `pkg/compiler` and `pkg/github` tests pass; pre-existing `internal/app/cmd` failures are out of scope

## Self-Check: PASSED

Files verified:
- pkg/compiler/testdata/ec2-with-allowed-refs.yaml: present
- pkg/compiler/userdata.go: HasAllowedRefs/AllowedRefs fields present, joinAllowedRefs() present, section 4b template present
- pkg/compiler/compiler_test.go: 5 new test functions present
- docs/security-model.md: AllowedRefs Enforcement subsection present

Commits verified:
- 0516b52: feat(25-02): implement ref enforcement via pre-push hook in user-data
- 816bff7: docs(25-02): update security-model.md with ref enforcement and ECS credential gap

---
*Phase: 25-github-source-access-restrictions-deep-testing-of-repo-allowlists-clone-push-enforcement-and-deny-by-default-for-unlisted-repos*
*Completed: 2026-03-26*
