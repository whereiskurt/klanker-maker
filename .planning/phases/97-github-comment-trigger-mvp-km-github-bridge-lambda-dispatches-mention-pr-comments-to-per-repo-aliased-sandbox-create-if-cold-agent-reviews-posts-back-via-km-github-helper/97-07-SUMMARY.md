---
phase: 97-github-comment-trigger-mvp
plan: 07
subsystem: infra
tags: [terragrunt, lambda, github-bridge, km-init, deploy-wiring]

# Dependency graph
requires:
  - phase: 97-github-comment-trigger-mvp
    provides: "lambda-github-bridge TF module (infra/modules/lambda-github-bridge/v1.0.0) and compiled binary (build/km-github-bridge.zip)"
provides:
  - "Live terragrunt unit infra/live/use1/lambda-github-bridge/terragrunt.hcl wiring bridge into deploy path"
  - "km init deploy enumeration of lambda-github-bridge with correct dependency ordering and 5-min timeout"
  - "Module-list test assertions updated and new TestRegionalModulesIncludesGitHubBridge passes"
affects: [97-github-comment-trigger-mvp, km-init, km-uninit]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Live terragrunt units cloned from sibling (lambda-slack-bridge) then trimmed to module-specific deps/inputs"
    - "GitHub bridge reuses km-slack-bridge-nonces table with distinct key namespace (github-delivery: prefix)"
    - "New bridge modules inserted after lambda-slack-bridge and before ses in regionalModules() ordered list"

key-files:
  created:
    - "infra/live/use1/lambda-github-bridge/terragrunt.hcl"
  modified:
    - "internal/app/cmd/init.go"
    - "internal/app/cmd/init_test.go"
    - "internal/app/cmd/init_plan_test.go"
    - "internal/app/cmd/uninit_test.go"

key-decisions:
  - "GitHub bridge reuses dynamodb-slack-nonces (shared nonce table) — no new DynamoDB table; uses github-delivery: key namespace"
  - "lambda-github-bridge placed after lambda-slack-bridge in regionalModules() to keep all bridge Lambdas together before ses"
  - "TestRunInitPlan_ModuleOrder count bumped 16 -> 17 (auto-fix Rule 1: hardcoded count was a blocking test failure)"

patterns-established:
  - "Any new bridge Lambda module: insert after lambda-slack-bridge, before ses; add to 5-min timeout case; update all three test count/order assertions"

requirements-completed: [GH-BRIDGE-ROUTE, GH-BRIDGE-DEPLOY]

# Metrics
duration: 15min
completed: 2026-06-06
---

# Phase 97 Plan 07: GH-BRIDGE-DEPLOY Gap Closure Summary

**Live terragrunt unit + km init registration wires lambda-github-bridge into the deploy path so `km init --dry-run=false` can create the Function URL that GitHub webhooks POST to.**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-06-06T00:00:00Z
- **Completed:** 2026-06-06T00:15:00Z
- **Tasks:** 3
- **Files modified:** 5

## Accomplishments
- Created `infra/live/use1/lambda-github-bridge/terragrunt.hcl` cloned from lambda-slack-bridge, sourcing the v1.0.0 module with all three required (no-default) inputs mapped and only the two needed dependencies (dynamodb-sandboxes + dynamodb-slack-nonces), both with "show" in mock_outputs_allowed_terraform_commands
- Registered `lambda-github-bridge` in `regionalModules()` after lambda-slack-bridge and before ses, with `envReqs: ["KM_ARTIFACTS_BUCKET"]` and 5-minute apply timeout
- Updated all three module-list test files (init_test, init_plan_test, uninit_test) and added `TestRegionalModulesIncludesGitHubBridge` with full dependency-order assertions — all module-list tests pass

## Task Commits

Each task was committed atomically:

1. **Task 1: Create live terragrunt unit** - `b4b62129` (feat)
2. **Task 2: Register lambda-github-bridge in init.go** - `3c35c82f` (feat)
3. **Task 3: Update module-list test assertions + add github-bridge test** - `dc4f5385` (test)

## Files Created/Modified
- `infra/live/use1/lambda-github-bridge/terragrunt.hcl` - Live terragrunt unit wiring github-bridge TF module into deploy path
- `internal/app/cmd/init.go` - lambda-github-bridge entry in regionalModules() + defaultModuleTimeout 5-min case
- `internal/app/cmd/init_test.go` - TestRegionalModulesIncludesGitHubBridge added
- `internal/app/cmd/init_plan_test.go` - allModuleNames: lambda-github-bridge added (16 -> 17); TestRunInitPlan_ModuleOrder count bumped
- `internal/app/cmd/uninit_test.go` - wantOrder: lambda-github-bridge inserted before lambda-slack-bridge (reverse order)

## Decisions Made
- GitHub bridge reuses `dynamodb-slack-nonces` — confirmed in variables.tf default `nonces_table_name = "km-slack-bridge-nonces"` and main.tf nonce key `github-delivery:`+guid. No new table needed.
- Placed lambda-github-bridge immediately after lambda-slack-bridge so all bridge Lambdas are contiguous before ses.
- All SSM paths use `/${local.site_vars.locals.site.label}/config/github/*` matching what `km github init` / `km configure github` write.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] TestRunInitPlan_ModuleOrder hardcoded count 16 was a failing test**
- **Found during:** Task 3 (Update module-list tests)
- **Issue:** `init_plan_test.go` line 435 checked `len(mods) != 16` — with 17 modules it emitted `FAIL: len(mods) = 17, want 16`
- **Fix:** Bumped count to 17 and updated the Expected order comment to include lambda-github-bridge
- **Files modified:** `internal/app/cmd/init_plan_test.go`
- **Verification:** All module-list tests pass after fix
- **Committed in:** `dc4f5385` (Task 3 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - hardcoded count blocking test)
**Impact on plan:** Necessary correctness fix. No scope creep.

## Issues Encountered
None beyond the auto-fixed hardcoded count.

## User Setup Required
None for this plan. The actual apply + GH-E2E webhook test remain the operator's human checkpoint (tracked in 97-VERIFICATION.md `human_verification`).

Operator steps when ready:
1. `make build-lambdas` (clean) to ensure `build/km-github-bridge.zip` is current
2. `km init --plan` to preview — should enumerate `lambda-github-bridge` in the module list
3. `km init --dry-run=false` to apply — creates the Function URL
4. Retrieve URL: `aws lambda get-function-url-config --function-name km-github-bridge`

## Next Phase Readiness
- GH-BRIDGE-DEPLOY gap is closed at the code/wiring level
- GH-BRIDGE-ROUTE moves from PARTIAL to deployable (handler logic was already complete)
- All remaining Phase 97 work is operator-facing: real `km init` apply, GitHub App webhook URL configuration, E2E smoke test

---
*Phase: 97-github-comment-trigger-mvp*
*Completed: 2026-06-06*
