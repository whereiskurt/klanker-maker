---
phase: 124-platform-wide-az-failover-and-capacity-feasibility-for-ec2-launches
plan: 03
subsystem: infra
tags: [dynamodb, terraform, terragrunt, ec2spot, capacity, az-failover]

# Dependency graph
requires:
  - phase: 121-action-quotas
    provides: dynamodb module pattern (PAY_PER_REQUEST, SSE, TTL) used as template
provides:
  - "{prefix}-capacity DynamoDB table module (instanceType/az composite key, TTL)"
  - "infra/live/use1/dynamodb-capacity live unit deriving {label}-capacity table name"
  - "regionalModules() entry for dynamodb-capacity (module count 26→27)"
  - "Bounded spot fulfillment waiter: aws_spot_instance_request timeouts{create=3m}"
  - "spot_create_timeout variable (default 3m) in ec2spot v1.2.0"
  - "TestEC2ServiceHCL_SpotTimeout + TestEC2ServiceHCL_OnDemandNoTimeout tests"
affects:
  - 124-01-capacity-store
  - 124-04-create-go-az-sweep
  - 124-06-deploy-and-validation

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "DynamoDB module pattern: hash+range key table with TTL+SSE, no required_providers (root.hcl)"
    - "Bounded Terraform waiter via timeouts{} block on aws_spot_instance_request"
    - "Module variable default hardcoded (spot_create_timeout=3m) — not wired through service.hcl"

key-files:
  created:
    - infra/modules/dynamodb-capacity/v1.0.0/main.tf
    - infra/modules/dynamodb-capacity/v1.0.0/variables.tf
    - infra/modules/dynamodb-capacity/v1.0.0/outputs.tf
    - infra/live/use1/dynamodb-capacity/terragrunt.hcl
    - pkg/compiler/ec2spot_timeout_test.go
  modified:
    - internal/app/cmd/init.go
    - internal/app/cmd/init_plan_test.go
    - infra/modules/ec2spot/v1.2.0/main.tf
    - infra/modules/ec2spot/v1.2.0/variables.tf

key-decisions:
  - "spot_create_timeout default hardcoded to 3m, NOT wired through service.hcl — preserves byte-identity of existing profiles, no service.hcl churn"
  - "On-demand aws_instance gets NO timeouts block — on-demand ICE errors fast already; bounded waiter is spot-only (124-RESEARCH Pitfall 7)"
  - "dynamodb-capacity placed adjacent to dynamodb-sandboxes in regionalModules() ordering — no upstream deps, plain table"
  - "delete timeout for spot request set to 10m to give terraform time to cancel the request cleanly"

patterns-established:
  - "New DynamoDB module: copy dynamodb-action-quota pattern (PAY_PER_REQUEST, TTL, SSE, no required_providers)"
  - "New live unit: copy dynamodb-action-quota terragrunt.hcl pattern (site_vars label-derived name)"
  - "Module count test: bump len(mods) assertion AND add new module to allModuleNames mock-dir list"

requirements-completed: [REQ-124-STORE, REQ-124-WAITER]

# Metrics
duration: 12min
completed: 2026-06-28
---

# Phase 124 Plan 03: Deploy Surface — DynamoDB Capacity Table + Bounded Spot Waiter

**DynamoDB {prefix}-capacity table (instanceType/az composite key, TTL) provisioned as TF module + live unit + regionalModules entry (26→27 modules), and aws_spot_instance_request bounded to 3-min create timeout so capacity-dry AZ sweeps fail fast within the Lambda 900s budget**

## Performance

- **Duration:** ~12 min
- **Started:** 2026-06-28T22:05:00Z
- **Completed:** 2026-06-28T22:17:00Z
- **Tasks:** 2
- **Files modified:** 9 (5 created, 4 modified)

## Accomplishments
- Created `infra/modules/dynamodb-capacity/v1.0.0/` TF module with (instanceType, az) composite key, PAY_PER_REQUEST billing, TTL, and SSE — following the dynamodb-action-quota pattern exactly
- Created `infra/live/use1/dynamodb-capacity/terragrunt.hcl` live unit deriving table name `{label}-capacity`
- Added `dynamodb-capacity` to `regionalModules()` in init.go (adjacent to dynamodb-sandboxes), bumped module-count test 26→27, updated allModuleNames mock-dir list
- Added `timeouts{create=var.spot_create_timeout, delete=10m}` to `aws_spot_instance_request` in ec2spot v1.2.0 with `spot_create_timeout` variable defaulting to `"3m"`
- Created two tests: `TestEC2ServiceHCL_SpotTimeout` (asserts timeouts block + 3m default) and `TestEC2ServiceHCL_OnDemandNoTimeout` (asserts on-demand resource is untouched)

## Task Commits

1. **Task 1: DynamoDB capacity module + live unit + regionalModules + module count** - `e9c13f45` (feat)
2. **Task 2: Bounded spot waiter + tests** - `70ea74f3` (feat)

## Files Created/Modified
- `infra/modules/dynamodb-capacity/v1.0.0/main.tf` - aws_dynamodb_table.capacity with instanceType/az composite key, TTL, SSE
- `infra/modules/dynamodb-capacity/v1.0.0/variables.tf` - table_name, tags variables
- `infra/modules/dynamodb-capacity/v1.0.0/outputs.tf` - table_name, table_arn outputs
- `infra/live/use1/dynamodb-capacity/terragrunt.hcl` - live unit: {label}-capacity table name
- `internal/app/cmd/init.go` - dynamodb-capacity added to regionalModules() after dynamodb-sandboxes
- `internal/app/cmd/init_plan_test.go` - module count 26→27, dynamodb-capacity in allModuleNames, comment updated
- `infra/modules/ec2spot/v1.2.0/main.tf` - timeouts{create, delete} block on aws_spot_instance_request
- `infra/modules/ec2spot/v1.2.0/variables.tf` - spot_create_timeout variable (default "3m")
- `pkg/compiler/ec2spot_timeout_test.go` - TestEC2ServiceHCL_SpotTimeout + TestEC2ServiceHCL_OnDemandNoTimeout

## Decisions Made
- Hardcoded `spot_create_timeout` default to `"3m"` without wiring through service.hcl — avoids byte-identity churn on existing profiles (124-RESEARCH Pattern 2 + Open Question 1)
- `delete = "10m"` on the spot timeouts block for clean cancellation
- No timeouts block on `aws_instance "ec2_ondemand"` — on-demand returns ICE quickly without needing a bounded waiter

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

**Parallel wave execution (informational):** Plan 124-01 deposited `pkg/compiler/service_hcl_azpref_test.go` and `pkg/profile/azpreference_test.go` as untracked files while this plan executed. These reference `profile.RuntimeSpec.AZPreference` (not yet committed by 124-01), causing `go test ./pkg/compiler/...` to fail at compile time. This is not caused by this plan's changes — `go build ./...` and both this plan's specific tests are clean. Will resolve once 124-01 commits the struct field.

## User Setup Required

None — no external service configuration required. Operator must run `make build` before `km init --dry-run=false` when dynamodb-capacity is a new regionalModules entry (project memory: project_make_build_precedes_km_init). This deploy step is handled in 124-06.

## Next Phase Readiness
- DynamoDB capacity table module and live unit are ready for `km init --dry-run=false` (executed in 124-06)
- ec2spot v1.2.0 bounded spot waiter is ready — a capacity-dry AZ will timeout in ~3 min instead of hanging forever
- `pkg/capacity` (from 124-01) can now reference `GetCapacityTableName` knowing the table will exist after deploy

---
*Phase: 124-platform-wide-az-failover-and-capacity-feasibility-for-ec2-launches*
*Completed: 2026-06-28*
