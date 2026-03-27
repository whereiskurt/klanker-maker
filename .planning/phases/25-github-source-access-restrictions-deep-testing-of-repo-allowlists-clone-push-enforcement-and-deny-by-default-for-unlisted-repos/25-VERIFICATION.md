---
phase: 25-github-source-access-restrictions-deep-testing-of-repo-allowlists-clone-push-enforcement-and-deny-by-default-for-unlisted-repos
verified: 2026-03-26T00:00:00Z
status: passed
score: 11/11 must-haves verified
re_verification: false
---

# Phase 25: GitHub Source Access Restrictions Verification Report

**Phase Goal:** Comprehensive test coverage for GitHub source access enforcement (deny-by-default, permission edge cases, wildcard patterns) plus implement ref enforcement via git pre-push hooks and document ECS credential delivery gap
**Verified:** 2026-03-26
**Status:** passed
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | Empty allowedRepos produces no token infrastructure (same as nil github config) | VERIFIED | `TestCompileEC2EmptyAllowedRepos_DenyByDefault`, `TestCompileECSEmptyAllowedRepos_DenyByDefault` pass; compiler.go and service_hcl.go guard with `!= nil && len(AllowedRepos) > 0` at lines 122, 170 (compiler.go) and lines 519, 650 (service_hcl.go) |
| 2 | Unknown permission strings are silently ignored by CompilePermissions | VERIFIED | `TestCompilePermissions_UnknownPermission` passes; tests "write", "admin", "delete", "read" all return empty map |
| 3 | Empty permissions slice produces empty GitHub permission map | VERIFIED | `TestCompilePermissions_EmptySlice` passes |
| 4 | Wildcard repo patterns in allowedRepos are rejected or handled safely by ExchangeForInstallationToken | VERIFIED | `TestExchangeForInstallationToken_WildcardRepoName` passes; 422 error is propagated to caller |
| 5 | EC2 and ECS service.hcl both omit github_token_inputs when allowedRepos is empty | VERIFIED | Both deny-by-default tests check ServiceHCL for absence of "github_token_inputs"; service_hcl.go HasGitHub guarded in both EC2 and ECS paths |
| 6 | User-data omits GIT_ASKPASS when allowedRepos is empty | VERIFIED | `TestUserDataEmptyAllowedRepos_NoGITASKPASS` passes; checks for absence of both "km-git-askpass" and "GIT_ASKPASS"; userdata.go HasGitHub at line 617 guards `{{- if .HasGitHub }}` block |
| 7 | When allowedRefs is non-empty, compiler injects pre-push hook and KM_ALLOWED_REFS env var into user-data | VERIFIED | `TestUserDataAllowedRefsEnvVar` and `TestUserDataPrePushHookPresent` pass; userdata.go section 4b template block present at line 154; `KM_ALLOWED_REFS="main:feature/*"` asserted exactly |
| 8 | When allowedRefs is empty or nil, no hook or env var is emitted | VERIFIED | `TestUserDataEmptyAllowedRefs_NoEnvVar`, `TestUserDataPrePushHookAbsent`, `TestUserDataNilAllowedRefs_NoHook` all pass |
| 9 | Pre-push hook supports wildcard patterns (feature/* matches feature/my-branch) | VERIFIED | Hook template in userdata.go line 161-182 uses `[[ "$branch" == $pattern ]]` bash glob matching; KM_ALLOWED_REFS uses colon separator |
| 10 | ECS limitation for GitHub credential delivery is documented in security-model.md | VERIFIED | docs/security-model.md lines 317-321 document ECS credential gap with specific failure mode ("Authentication failed") and known v1 limitation statement |
| 11 | AllowedRefs enforcement approach and limitations are documented | VERIFIED | docs/security-model.md lines 294-311 contain "AllowedRefs Enforcement" subsection with EC2-only limitation, --no-verify bypass risk, and defense-in-depth framing |

**Score:** 11/11 truths verified

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/github/token_test.go` | Permission edge case tests containing `TestCompilePermissions_EmptySlice` | VERIFIED | Functions at lines 234 and 244; `TestExchangeForInstallationToken_WildcardRepoName` at line 258 |
| `pkg/compiler/compiler_test.go` | Deny-by-default tests containing `TestCompileEC2EmptyAllowedRepos_DenyByDefault` | VERIFIED | All 8 new test functions present at lines 806-970 |
| `pkg/compiler/testdata/ec2-empty-repos.yaml` | Test profile with `allowedRepos: []` | VERIFIED | Contains `allowedRepos: []` at line 27; non-nil github block confirmed |
| `pkg/compiler/testdata/ecs-empty-repos.yaml` | Test profile with `allowedRepos: []` for ECS | VERIFIED | Contains `allowedRepos: []` at line 27; `substrate: ecs` confirmed |
| `pkg/compiler/userdata.go` | Ref enforcement hook injection containing `KM_ALLOWED_REFS` | VERIFIED | `HasAllowedRefs`/`AllowedRefs` fields at lines 549-550; section 4b template at lines 154-187; `joinAllowedRefs()` helper at lines 582-589 |
| `pkg/compiler/compiler_test.go` | Ref enforcement tests containing `TestUserDataAllowedRefsEnvVar` | VERIFIED | 5 ref enforcement test functions present at lines 867-970 |
| `pkg/compiler/testdata/ec2-with-allowed-refs.yaml` | Test profile with `allowedRefs: ["main", "feature/*"]` | VERIFIED | `allowedRefs: ["main", "feature/*"]` at lines 30-31; populated `allowedRepos` present |
| `docs/security-model.md` | Updated security documentation containing "pre-push hook" | VERIFIED | "AllowedRefs Enforcement" subsection at lines 294-321; "pre-push" string appears at lines 296, 298, 302 |
| `pkg/compiler/compiler.go` | Deny-by-default guard: `len(AllowedRepos) > 0` | VERIFIED | Lines 122 and 170 both guard with `!= nil && len(AllowedRepos) > 0` |
| `pkg/compiler/service_hcl.go` | HasGitHub guards in both EC2 and ECS paths | VERIFIED | Lines 519-520 (EC2) and 650-651 (ECS) both apply the dual guard |
| `internal/app/cmd/create.go` | Token generation gate with `len(AllowedRepos) > 0` | VERIFIED | Line 539 applies `!= nil && len(AllowedRepos) > 0` before `generateAndStoreGitHubToken` |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `pkg/compiler/compiler_test.go` | `pkg/compiler/userdata.go` | `Compile()` call with ec2-empty-repos profile | WIRED | `loadTestProfile("ec2-empty-repos.yaml")` at line 807; `compiler.Compile()` invoked; `artifacts.UserData` checked |
| `pkg/github/token_test.go` | `pkg/github/token.go` | `CompilePermissions` edge cases | WIRED | Direct calls to `github.CompilePermissions([]string{})` and `github.CompilePermissions([]string{"write"})` |
| `pkg/compiler/userdata.go` | `pkg/profile/types.go` | `AllowedRefs` field read during user-data generation | WIRED | `p.Spec.SourceAccess.GitHub.AllowedRefs` accessed at lines 585-586 and 618; field comes from `pkg/profile/types.go:155` |
| `pkg/compiler/compiler_test.go` | `pkg/compiler/userdata.go` | `Compile()` with allowedRefs profile | WIRED | `loadTestProfile("ec2-with-allowed-refs.yaml")` at line 870; `artifacts.UserData` checked for `KM_ALLOWED_REFS` content |

---

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|---------|
| GH25-01 | 25-01-PLAN.md | Deny-by-default: empty allowedRepos must not emit token infrastructure | SATISFIED | compiler.go, service_hcl.go, userdata.go all guard with `len(AllowedRepos) > 0`; 3 new tests prove the contract |
| GH25-02 | 25-01-PLAN.md | Permission edge cases: empty slice and unknown strings handled safely | SATISFIED | `TestCompilePermissions_EmptySlice` and `TestCompilePermissions_UnknownPermission` pass |
| GH25-03 | 25-02-PLAN.md | AllowedRefs enforced via pre-push hook in EC2 user-data | SATISFIED | Section 4b injected in userdata.go; 5 tests verify presence/absence logic; `core.hooksPath` configured |
| GH25-04 | 25-01-PLAN.md | Wildcard repo patterns handled safely (documented behavior, error propagated) | SATISFIED | `TestExchangeForInstallationToken_WildcardRepoName` verifies 422 error propagated |
| GH25-05 | 25-02-PLAN.md | ECS credential gap and enforcement approach documented accurately in security-model.md | SATISFIED | `docs/security-model.md` AllowedRefs Enforcement subsection, ECS credential gap section (lines 317-321) present |

**Note on REQUIREMENTS.md traceability:** GH25-01 through GH25-05 are phase-local requirement IDs defined only in ROADMAP.md (Phase 25 entry). They do not appear in the main `.planning/REQUIREMENTS.md` traceability table. The traceability table ends at Phase 7 for many entries and has no row for Phase 25. This is a documentation gap in REQUIREMENTS.md but does not indicate missing functionality — all five requirements are addressed by the implementation.

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| None | — | — | — | — |

No TODO/FIXME/PLACEHOLDER comments, empty implementations, or stub return values found in the modified files. All test functions contain substantive assertions. All production code changes implement real logic.

---

### Human Verification Required

#### 1. ECS GitHub Credential End-to-End

**Test:** Deploy an ECS sandbox with `sourceAccess.github` configured with populated `allowedRepos`. Exec into the running task and run `git clone https://github.com/org/repo`.
**Expected:** Failure with "Authentication failed" (the ECS credential gap is a documented known v1 limitation — the pre-push infrastructure deploys but no GIT_ASKPASS equivalent runs in the container).
**Why human:** Requires a live ECS Fargate task with actual GitHub App credentials and an installed app on the test organization.

#### 2. Wildcard allowedRefs Runtime Behavior

**Test:** Create an EC2 sandbox with `allowedRefs: ["main", "feature/*"]`. Attempt `git push origin feature/my-branch` (should succeed) and `git push origin hotfix/bug123` (should be blocked by pre-push hook).
**Expected:** First push succeeds; second push fails with `[km] Push to 'hotfix/bug123' denied -- not in allowedRefs`.
**Why human:** Requires a live EC2 instance with git configured, a real remote repository, and verification that `core.hooksPath` and `KM_ALLOWED_REFS` are correctly set in the running environment.

#### 3. km create Token Skip for Empty Repos

**Test:** Run `km create` with a profile that has `allowedRepos: []`. Confirm no SSM parameter is created and no GitHub token Lambda is provisioned.
**Expected:** `km create` completes without calling GitHub App API; no `/sandbox/{id}/github-token` SSM path created.
**Why human:** Requires AWS credentials and a configured GitHub App installation to verify the full create path.

---

### Gaps Summary

No gaps found. All automated checks pass.

Both plans executed as specified. Plan 01 hardened four production code locations (compiler.go, service_hcl.go, userdata.go, create.go) with the deny-by-default `!= nil && len(AllowedRepos) > 0` guard and added 6 new tests. Plan 02 implemented `HasAllowedRefs`/`AllowedRefs` fields and a pre-push hook template in userdata.go, added 5 ref enforcement tests, and updated security-model.md with accurate documentation. All 11 new test functions pass. All 3 commits (71ebeae, 0516b52, 816bff7) verified in git history.

The only non-blocking finding is that GH25-01 through GH25-05 are not in the REQUIREMENTS.md traceability table. These are internal phase IDs only defined in ROADMAP.md, not in the global requirements registry. Future phases that define phase-local IDs should add them to the traceability table for completeness.

---

_Verified: 2026-03-26_
_Verifier: Claude (gsd-verifier)_
