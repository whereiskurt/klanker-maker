---
phase: 13-github-app-token-integration-scoped-repo-access-for-sandboxes
plan: "03"
subsystem: compiler
tags: [github, git-askpass, terragrunt, hcl, compiler, ssm, credentials]

requires:
  - phase: 13-01
    provides: pkg/github token generation, CompilePermissions, SSM storage at /sandbox/{id}/github-token
  - phase: 13-02
    provides: github-token Terraform module and SCP carve-out for Lambda

provides:
  - GIT_ASKPASS credential helper injected into EC2 userdata (reads SSM at git-operation time, never exports GITHUB_TOKEN)
  - github_token_inputs block in both EC2 and ECS service.hcl for Terragrunt consumption
  - GitHubTokenHCL field in CompiledArtifacts pointing to github-token/v1.0.0 module
  - compileSecrets no longer injects /km/github/app-token into SecretPaths

affects: [infra-live, km-create, km-destroy, phase-14-identity, phase-15-doctor]

tech-stack:
  added: []
  patterns:
    - "GIT_ASKPASS credential helper pattern: script reads SSM at git time, not at boot time"
    - "Per-sandbox SSM path /sandbox/{id}/github-token for scoped token isolation"
    - "permissionsToHCL() converts pkg/github.CompilePermissions output to HCL map literal"
    - "github_token_inputs block mirrors budget_enforcer_inputs conditional block pattern"
    - "generateGitHubTokenHCL mirrors generateBudgetEnforcerHCL: sandbox_id is only template var"

key-files:
  created:
    - pkg/compiler/github_token_hcl.go
    - pkg/compiler/testdata/ecs-with-github.yaml
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/security.go
    - pkg/compiler/compiler.go
    - pkg/compiler/service_hcl.go
    - pkg/compiler/compiler_test.go

key-decisions:
  - "GIT_ASKPASS reads /sandbox/${SANDBOX_ID}/github-token at git time — token never in environment variables"
  - "ECS credential helper injection deferred — EC2 userdata path only in Phase 13"
  - "github_token_inputs emitted for both EC2 and ECS substrates so Lambda/EventBridge infra deploys for ECS too"
  - "permissionsToHCL placed in service_hcl.go (not github_token_hcl.go) — both EC2 and ECS generators use it"
  - "SANDBOX_ID exported in section 4 before km-git-askpass script to make it available in the heredoc"

patterns-established:
  - "Conditional HCL block pattern: {{- if .HasGitHub }} guards github_token_inputs in both EC2/ECS templates"
  - "New CompiledArtifacts fields follow same nil/empty convention as BudgetEnforcerHCL"

requirements-completed: [GH-02, GH-04, GH-05, GH-11, GH-12]

duration: 18min
completed: 2026-03-22
---

# Phase 13 Plan 03: Compiler GitHub Token Integration Summary

**GIT_ASKPASS credential helper injected into EC2 userdata replacing GITHUB_TOKEN env var, with github_token_inputs block emitted in both EC2 and ECS service.hcl and GitHubTokenHCL field added to CompiledArtifacts**

## Performance

- **Duration:** ~18 min
- **Started:** 2026-03-22T00:00:00Z
- **Completed:** 2026-03-22T00:18:00Z
- **Tasks:** 2
- **Files modified:** 7 (5 modified + 2 created)

## Accomplishments
- Replaced the old `GITHUB_TOKEN` env var export pattern with a GIT_ASKPASS credential helper script (`km-git-askpass`) that reads the per-sandbox SSM path `/sandbox/${SANDBOX_ID}/github-token` at git-operation time — token never exported into process environment
- Removed `/km/github/app-token` from `compileSecrets()` — the GitHub token uses its own per-sandbox SSM path managed by the token refresher Lambda, not the general secret injection mechanism
- Added `github_token_inputs` block to both EC2 and ECS service.hcl templates (guarded by `HasGitHub`), consumed by `github-token/terragrunt.hcl` via `read_terragrunt_config` — ensures Lambda/EventBridge infrastructure deploys for both substrates
- Added `GitHubTokenHCL` field to `CompiledArtifacts` and created `github_token_hcl.go` with `generateGitHubTokenHCL()` following the same Terragrunt pattern as `generateBudgetEnforcerHCL()`

## Task Commits

Each task was committed atomically:

1. **Task 1: Replace userdata GITHUB_TOKEN with GIT_ASKPASS + remove security.go stub** - `31607a1` (feat)
2. **Task 2: Emit github_token_inputs in service.hcl + GitHubTokenHCL in CompiledArtifacts** - `6abdb20` (feat)

**Plan metadata:** (docs commit follows)

_Note: Both tasks used TDD (RED → GREEN approach). All existing compiler tests continue to pass._

## Files Created/Modified
- `pkg/compiler/userdata.go` - Section 4: replaced GITHUB_TOKEN export with GIT_ASKPASS credential helper script
- `pkg/compiler/security.go` - Removed /km/github/app-token from compileSecrets(); updated comment
- `pkg/compiler/compiler.go` - Added GitHubTokenHCL field to CompiledArtifacts; call generateGitHubTokenHCL in compileEC2/compileECS
- `pkg/compiler/service_hcl.go` - Added HasGitHub/GitHubSSMPath/GitHubAllowedRepos/GitHubPermissions to both param structs; added github_token_inputs block to EC2 and ECS templates; permissionsToHCL helper; import pkg/github
- `pkg/compiler/github_token_hcl.go` - New file: generateGitHubTokenHCL() producing github-token/terragrunt.hcl
- `pkg/compiler/testdata/ecs-with-github.yaml` - New ECS test profile with sourceAccess.github
- `pkg/compiler/compiler_test.go` - Replaced TestCompileGitHubToken; added 9 new tests covering GIT_ASKPASS, SecretPaths cleanup, github_token_inputs for EC2/ECS, GitHubTokenHCL field

## Decisions Made
- **GIT_ASKPASS vs env var:** Token reads SSM at git time, not at boot — avoids token appearing in `ps aux`, `/proc/environ`, or child process environments
- **ECS deferred:** Only EC2 userdata gets the GIT_ASKPASS injection. ECS needs the script baked into sandbox image or delivered via sidecar volume mount — deferred to a future phase per plan spec
- **github_token_inputs for ECS service.hcl:** Even though ECS doesn't get the in-sandbox credential helper yet, the `github_token_inputs` block is emitted so the Lambda/EventBridge infrastructure still deploys for ECS sandboxes. The token refresh Lambda will write to SSM regardless of substrate
- **permissionsToHCL in service_hcl.go:** Both EC2 and ECS generators need this helper, so it lives alongside the template functions rather than in github_token_hcl.go
- **SANDBOX_ID export in section 4:** The heredoc `ASKPASS` script references `${SANDBOX_ID}` at runtime, so SANDBOX_ID must be set before the script is written and the variable expanded at git-operation time (not at script-write time)

## Deviations from Plan

None - plan executed exactly as written. The ECS deferral was explicitly called out in the plan spec.

## Issues Encountered
None. Both tasks compiled cleanly on first attempt.

## User Setup Required
None - no external service configuration required. The GitHub token SSM path `/sandbox/{id}/github-token` is populated by the token refresher Lambda deployed via `github_token_inputs`.

## Next Phase Readiness
- Compiler now produces all necessary artifacts for GitHub App token integration
- `km create` will need to apply `github-token/terragrunt.hcl` when `GitHubTokenHCL` is non-empty (Phase 13-04 wiring concern)
- ECS in-sandbox credential helper delivery remains deferred — tracked in deferred-items.md

---
*Phase: 13-github-app-token-integration-scoped-repo-access-for-sandboxes*
*Completed: 2026-03-22*
