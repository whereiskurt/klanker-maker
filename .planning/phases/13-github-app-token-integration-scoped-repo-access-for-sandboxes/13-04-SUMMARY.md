---
phase: 13-github-app-token-integration-scoped-repo-access-for-sandboxes
plan: 04
subsystem: auth
tags: [github-app, ssm, cobra, cli, installation-token, jwt, km-configure, km-create, km-destroy]

requires:
  - phase: 13-github-app-token-integration-scoped-repo-access-for-sandboxes
    plan: 01
    provides: GenerateGitHubAppJWT, ExchangeForInstallationToken, WriteTokenToSSM in pkg/github
  - phase: 13-github-app-token-integration-scoped-repo-access-for-sandboxes
    plan: 02
    provides: github-token Terraform module and SSM KMS key infrastructure
  - phase: 13-github-app-token-integration-scoped-repo-access-for-sandboxes
    plan: 03
    provides: CompiledArtifacts.GitHubTokenHCL and generateGitHubTokenHCL in pkg/compiler

provides:
  - "km configure github subcommand storing GitHub App credentials in SSM"
  - "GitHub App installation token generated and written to SSM at km create time"
  - "github-token/ Terragrunt directory deployed by km create (non-fatal)"
  - "github-token resources cleaned up by km destroy before main destroy (non-fatal)"

affects:
  - phase 14 (sandbox identity)
  - phase 15 (km doctor — validates github credentials present in SSM)

tech-stack:
  added: []
  patterns:
    - "SSMWriteAPI narrow interface for DI in configure_github.go (PutParameter only)"
    - "NewConfigureGitHubCmdWithDeps exported DI constructor for external test package"
    - "generateAndStoreGitHubToken helper function keeps runCreate readable with non-fatal error return"
    - "source-level verification pattern for wiring tests (os.ReadFile + strings.Contains)"
    - "goto avoided in favour of helper function for non-fatal multi-step flows"

key-files:
  created:
    - internal/app/cmd/configure_github.go
    - internal/app/cmd/configure_github_test.go
  modified:
    - internal/app/cmd/root.go
    - internal/app/cmd/create.go
    - internal/app/cmd/destroy.go
    - internal/app/cmd/create_test.go
    - internal/app/cmd/destroy_test.go

key-decisions:
  - "SSMWriteAPI narrow interface defined in configure_github.go (PutParameter only) — follows S3PutAPI/CWLogsAPI pattern"
  - "km configure github registered as subcommand of km configure (configureCmd.AddCommand) not at root level"
  - "PEM validation is decode-only (pem.Decode) — key parsing tested by pkg/github, not CLI"
  - "generateAndStoreGitHubToken helper function used instead of goto — Go goto jumps over variable declarations"
  - "GitHub token generation (Step 13a) guarded by profile.Spec.SourceAccess.GitHub != nil — skipped for non-GitHub profiles"
  - "KMS key ARN defaults to alias/km-platform when KM_PLATFORM_KMS_KEY_ARN not set — real key resolved by SSM"

requirements-completed:
  - GH-01
  - GH-03
  - GH-05
  - GH-09

duration: 7min
completed: 2026-03-23
---

# Phase 13 Plan 04: GitHub App Token CLI Integration Summary

**km configure github, km create GitHub token generation, and km destroy token cleanup wired end-to-end with SSM storage and non-fatal error handling throughout**

## Performance

- **Duration:** 7 min
- **Started:** 2026-03-23T03:19:38Z
- **Completed:** 2026-03-23T03:26:18Z
- **Tasks:** 2
- **Files modified:** 7

## Accomplishments

- `km configure github` subcommand prompts for GitHub App client ID, private key PEM, and installation ID, validates PEM, and writes three SSM parameters with correct types (String/SecureString)
- `km create` Step 13a generates a GitHub App installation token by reading credentials from SSM, minting a JWT, exchanging for an installation token, and writing to `/sandbox/{id}/github-token`
- `km create` Step 13b deploys the `github-token/` Terragrunt directory (Lambda + EventBridge schedule) from the compiled HCL artifact
- `km destroy` Step 7c runs `terragrunt destroy` on `github-token/` and deletes the SSM parameter before main sandbox destroy — idempotent (ParameterNotFound swallowed)
- All create/destroy steps are non-fatal, matching Phase 06-06 budget-enforcer pattern

## Task Commits

Each task was committed atomically:

1. **Task 1 RED: configure github tests** - `51586cc` (test)
2. **Task 1 GREEN: configure github implementation** - `905d92c` (feat)
3. **Task 2: km create + km destroy GitHub token wiring** - `d966b9f` (feat)

**Plan metadata:** (this commit)

_Note: Task 1 used TDD (RED test commit, then GREEN implementation commit)_

## Files Created/Modified

- `internal/app/cmd/configure_github.go` - New: `km configure github` subcommand with SSMWriteAPI DI, interactive + non-interactive modes, PEM validation
- `internal/app/cmd/configure_github_test.go` - New: 8 tests covering command shape, SSM writes, PEM rejection, --force flag, subcommand registration
- `internal/app/cmd/root.go` - Modified: registers `NewConfigureGitHubCmd` as subcommand of `NewConfigureCmd`
- `internal/app/cmd/create.go` - Modified: Step 13a (token generation) and Step 13b (github-token/ Terragrunt deploy) after budget-enforcer step
- `internal/app/cmd/destroy.go` - Modified: Step 7c (github-token destroy + SSM delete) before main sandbox destroy
- `internal/app/cmd/create_test.go` - Modified: source-level verification test for Step 13a/13b wiring
- `internal/app/cmd/destroy_test.go` - Modified: source-level verification test for Step 7c wiring

## Decisions Made

- **SSMWriteAPI narrow interface** defined in `configure_github.go` (PutParameter only) — follows S3PutAPI/CWLogsAPI pattern from Phase 3. Exported for test package DI.
- **km configure github as subcommand** of `km configure` not at root level — `configureCmd.AddCommand(NewConfigureGitHubCmd(cfg))` in root.go.
- **PEM validation is decode-only** (`pem.Decode`) — key parsing and signature validation are tested in `pkg/github`, not the CLI layer.
- **Helper function over goto** — `generateAndStoreGitHubToken` helper avoids Go's restriction on goto jumping over variable declarations.
- **KMS key defaults to `alias/km-platform`** when `KM_PLATFORM_KMS_KEY_ARN` is not set — SSM resolves alias to the real key.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Replaced goto with helper function**
- **Found during:** Task 2 (create.go implementation)
- **Issue:** Go compiler rejects `goto skipGitHubToken` that jumps over variable declarations (`token`, `tokenErr`, `jwtToken` etc.) — causes build failure
- **Fix:** Extracted all SSM reads + JWT generation + token exchange into `generateAndStoreGitHubToken()` helper function; caller treats non-nil return as non-fatal
- **Files modified:** `internal/app/cmd/create.go`
- **Verification:** `go build ./internal/app/cmd/...` passes; all tests pass
- **Committed in:** d966b9f (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - bug)
**Impact on plan:** Refactored to helper function pattern which is actually cleaner than goto. No scope creep.

## Issues Encountered

None beyond the goto build failure resolved via Rule 1 auto-fix.

## Next Phase Readiness

- Complete GitHub App token lifecycle is operational: configure once, then every `km create` with `sourceAccess.github` generates a scoped installation token
- Phase 13 is now complete (all 4 plans done)
- Phase 14 (Sandbox Identity & Signed Email) can proceed — no GitHub token dependencies
- Phase 15 (km doctor) can add GitHub credential validation via `km configure github --check`

---
*Phase: 13-github-app-token-integration-scoped-repo-access-for-sandboxes*
*Completed: 2026-03-23*
