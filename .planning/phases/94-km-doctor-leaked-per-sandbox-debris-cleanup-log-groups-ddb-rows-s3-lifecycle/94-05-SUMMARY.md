---
phase: 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle
plan: "05"
subsystem: infra
tags: [terraform, cloudwatch, log-groups, resource_prefix, multi-install, compiler, ecs, ec2, byte-identity]

# Dependency graph
requires:
  - phase: 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle
    provides: "Plan 94-02 doctor check that reclaims both-names log groups at teardown (the DELETE side)"

provides:
  - "budget-enforcer Lambda log group created with ${var.resource_prefix}-budget-enforcer-{id}"
  - "github-token-refresher Lambda log group created with ${var.resource_prefix}-github-token-refresher-{id}"
  - "create-handler IAM ARNs for /${var.resource_prefix}/sandboxes/* (lockstep with audit path)"
  - "EC2 userdata CW_LOG_GROUP uses /{{ .ResourcePrefix }}/sandboxes/{id}/ (dynamic)"
  - "ECS service.hcl CW_LOG_GROUP + awslogs-group use /{{ .ResourcePrefix }}/ (dynamic)"
  - "Golden byte-identity proof: km→km no-op for default install (EC2 + ECS)"
  - "SCP coupling investigation documented (ELSE branch: v1.0.0 unused, v2.0.0 already correct)"

affects:
  - "km destroy / ttl-handler / pkg/aws/cloudwatch.go — teardown now matches creation names for non-km installs"
  - "Phase 94-02 doctor check — both sides of the match now use the same prefix"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "ecsHCLParams.ResourcePrefix derived once at struct construction from KM_RESOURCE_PREFIX (default km)"
    - "Golden-file byte-identity test pattern (CAPTURE_X_BASELINE=1 gate) extended to ECS service.hcl"
    - "SCP investigation: ELSE branch documented with inline comment + follow-up note (no blind scope widening)"

key-files:
  created:
    - pkg/compiler/userdata_94_05_prefix_test.go
    - pkg/compiler/service_hcl_94_05_prefix_test.go
    - pkg/compiler/testdata/service_hcl_km_prefix_94_05.golden.hcl
  modified:
    - infra/modules/budget-enforcer/v1.0.0/main.tf
    - infra/modules/github-token/v1.0.0/main.tf
    - infra/modules/create-handler/v1.0.0/main.tf
    - pkg/compiler/userdata.go
    - pkg/compiler/service_hcl.go
    - pkg/compiler/service_hcl_test.go
    - pkg/compiler/userdata_test.go
    - infra/modules/scp/v1.0.0/main.tf

key-decisions:
  - "create-handler IAM ARNs moved in lockstep with audit path (/km/sandboxes/* → /{prefix}/sandboxes/*) — without this the Lambda loses log-write permission after the path move"
  - "ecsHCLParams.ResourcePrefix derived once (not per-branch) at params construction; GitHub token block re-uses the same value (eliminates duplicate os.Getenv call)"
  - "SCP v1.0.0 ELSE branch: no resource_prefix variable; module unused (live uses v2.0.0 which is already wildcard-correct); added inline comment documenting the gap; no logic change"
  - "km→km byte-identity: TestUserdataKmPrefixByteIdentity reuses pre-92 golden (same km-default inputs); TestServiceHCLKmPrefixByteIdentity uses new 94-05 golden captured post-migration"

patterns-established:
  - "Phase 94-05 pattern: investigate SCP coupling → apply ELSE branch rule (document + comment) when module has no resource_prefix var and is unused in production"
  - "Golden capture workflow: CAPTURE_9405_BASELINE=1 gate for ECS service.hcl golden, mirrors pre-92 userdata pattern"

requirements-completed: [DBG-SRCFIX]

# Metrics
duration: 6min
completed: 2026-06-05
---

# Phase 94 Plan 05: ResourcePrefix migration for per-sandbox CloudWatch log groups Summary

**Three TF modules + compiler migrated so sandbox log groups are CREATED with the same dynamic {resource_prefix} prefix that teardown DELETES with — closing the source-side leak on non-default installs (e.g. kph) while remaining byte-identical on the default km install**

## Performance

- **Duration:** 6 min
- **Started:** 2026-06-05T01:39:24Z
- **Completed:** 2026-06-05T01:45:37Z
- **Tasks:** 3
- **Files modified:** 11 (8 source + 3 new)

## Accomplishments

- Migrated `budget-enforcer/v1.0.0/main.tf` and `github-token/v1.0.0/main.tf` log-group names from hardcoded `km-` to `${var.resource_prefix}-`; both modules already declared `var.resource_prefix` so no variable changes needed.
- Migrated `create-handler/v1.0.0/main.tf` IAM log-group ARNs from `/km/sandboxes/*` to `/${var.resource_prefix}/sandboxes/*` — the critical lockstep change that keeps the Lambda's log-write permission after the sandbox audit path moves.
- Migrated `pkg/compiler/userdata.go` EC2 systemd unit `CW_LOG_GROUP` from `/km/sandboxes/` to `/{{ .ResourcePrefix }}/sandboxes/`; field already in scope.
- Migrated `pkg/compiler/service_hcl.go` ECS `CW_LOG_GROUP` and `awslogs-group` from `/km/` to `/{{ .ResourcePrefix }}/`; added `ResourcePrefix string` to `ecsHCLParams` struct, derived once at construction, removed duplicate `os.Getenv` in GitHub token block; removed both `TODO(plan-04)` comments.
- All five new tests pass: `TestUserdataCWLogGroupResourcePrefix` (EC2 kph+km), `TestECSCWLogGroupResourcePrefix` (ECS kph+km), `TestUserdataKmPrefixByteIdentity` (km golden), `TestServiceHCLKmPrefixByteIdentity` (ECS km golden), `TestServiceHCLKmPrefixContainsDynamicPaths` (static guard).
- Investigated SCP coupling; determined ELSE branch (see Decisions Made) and documented with inline comments.

## Task Commits

1. **Task 1: Migrate the three TF modules to ${var.resource_prefix}** - `1d67d151` (feat)
2. **Task 2: Migrate the compiler (userdata.go + service_hcl.go) + golden byte-identity test** - `12def7df` (feat)
3. **Task 3: Investigate the SCP role-pattern coupling** - `0b929ff4` (docs)

## Files Created/Modified

- `infra/modules/budget-enforcer/v1.0.0/main.tf` — log-group name uses `${var.resource_prefix}`
- `infra/modules/github-token/v1.0.0/main.tf` — log-group name + KMS description use `${var.resource_prefix}`
- `infra/modules/create-handler/v1.0.0/main.tf` — IAM ARNs `/km/sandboxes/*` → `/${var.resource_prefix}/sandboxes/*`
- `pkg/compiler/userdata.go` — `CW_LOG_GROUP=/km/sandboxes/` → `/{{ .ResourcePrefix }}/sandboxes/`
- `pkg/compiler/service_hcl.go` — `ResourcePrefix` added to `ecsHCLParams`; templates use `{{ .ResourcePrefix }}`; TODO(plan-04) removed
- `pkg/compiler/service_hcl_test.go` — `TestECSCWLogGroupResourcePrefix` added
- `pkg/compiler/userdata_test.go` — `TestUserdataCWLogGroupResourcePrefix` added
- `pkg/compiler/userdata_94_05_prefix_test.go` — `TestUserdataKmPrefixByteIdentity`
- `pkg/compiler/service_hcl_94_05_prefix_test.go` — `TestServiceHCLKmPrefixByteIdentity` + `TestServiceHCLKmPrefixContainsDynamicPaths`
- `pkg/compiler/testdata/service_hcl_km_prefix_94_05.golden.hcl` — ECS km-prefix baseline golden (5784 bytes)
- `infra/modules/scp/v1.0.0/main.tf` — inline comments documenting km-hardcode gap (logic unchanged)

## Decisions Made

**1. create-handler IAM lockstep change is mandatory:** Moving the sandbox audit path from `/km/sandboxes/*` to `/{prefix}/sandboxes/*` without updating the create-handler IAM policy would cause the Lambda to lose `CreateLogGroup`/`PutLogEvents` permission. Changed in lockstep.

**2. ResourcePrefix derived once at ecsHCLParams construction:** The prior code derived `ecsResourcePrefix` only inside the GitHub token if-block. Moving derivation to before the struct literal allows it to be used in both `params.ResourcePrefix` and `params.GitHubSSMPath`, eliminating the duplicate `os.Getenv("KM_RESOURCE_PREFIX")` call.

**3. SCP role-pattern coupling — ELSE branch (do not fix v1.0.0):**
- `scp/v1.0.0/main.tf` has hardcoded `km-budget-enforcer-*` and `km-github-token-refresher-*` patterns with no `resource_prefix` variable.
- The live terragrunt unit (`infra/live/management/scp/terragrunt.hcl:60`) references `scp//v2.0.0` which already uses wildcard `*-budget-enforcer-*` / `*-github-token-refresher-*` patterns (prefix-agnostic).
- v1.0.0 is unused in production. Fixing it would require adding a variable with no caller to pass it.
- Action taken: added inline comments to v1.0.0 noting the pre-existing km-hardcode gap and pointing to v2.0.0 as the fix. No logic change. No silent scope widening.

## Deviations from Plan

None — plan executed exactly as specified. The SCP ELSE branch was the locked decision rule in the plan.

## Issues Encountered

None. The `terraform fmt -check` on `budget-enforcer/v1.0.0/main.tf` initially failed (exit 3), indicating formatting needed normalization — applied `terraform fmt` and all three modules then passed `fmt -check` cleanly.

## Rollout Note

**TF + Lambda changes require deployment:**
1. `make build-lambdas` (clean — clears stale zips; see `project_km_init_skips_existing_lambda_zips` memory)
2. `km init --dry-run=false` (full apply to update Lambda code + IAM policy)

**Existing sandboxes are NOT retroactively renamed:** They keep their legacy `km-` log groups, which Plan 94-02's doctor check reclaims at teardown via the both-names match (it matches both `km-{name}` and `{prefix}-{name}` patterns). Only new sandboxes created after deployment will use the correct `{resource_prefix}-` names.

This is a **no-op on the default `km` install** — `km → km` produces byte-identical output (proven by the golden tests).

## User Setup Required

None — no external service configuration required. Deploy with `make build-lambdas && km init --dry-run=false`.

## Next Phase Readiness

- The CREATE side of the prefix asymmetry is fixed. Combined with Plan 94-02's DELETE side (both-names match), new non-default-install sandboxes will self-clean at teardown.
- Plan 94-02's doctor check covers the legacy km- orphans on the kph install that pre-date this fix.

---
*Phase: 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle*
*Completed: 2026-06-05*
