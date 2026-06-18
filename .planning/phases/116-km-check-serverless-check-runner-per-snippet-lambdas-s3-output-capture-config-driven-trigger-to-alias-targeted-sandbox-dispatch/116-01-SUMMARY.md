---
phase: 116-km-check-serverless-check-runner
plan: "01"
subsystem: infra
tags: [terraform, terragrunt, dynamodb, iam, lambda, km-check]

# Dependency graph
requires: []
provides:
  - "{prefix}-checks DynamoDB table (hash key name, PAY_PER_REQUEST, SSE) via infra/modules/dynamodb-checks/v1.0.0"
  - "{prefix}-check-runner IAM role with baseline Lambda execution policies via infra/modules/check-runner-role/v1.0.0"
  - "live terragrunt units for both modules under infra/live/use1/"
  - "both modules registered in regionalModules() (count 22->24); ses remains last"
affects:
  - "116-km-check-serverless-check-runner (all subsequent plans)"
  - "km init (now provisions checks table + check-runner role)"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Minimal DDB table: hash-key-only + PAY_PER_REQUEST + SSE, no TTL, no composite PK"
    - "Shared Lambda execution role module: IAM role + inline policies, no EC2/SQS scope, KM_ARTIFACTS_BUCKET from get_env()"
    - "regionalModules() entry: envReqs: nil for pure-DDB modules; envReqs: [KM_ARTIFACTS_BUCKET] for IAM modules referencing the bucket"

key-files:
  created:
    - infra/modules/dynamodb-checks/v1.0.0/main.tf
    - infra/modules/dynamodb-checks/v1.0.0/variables.tf
    - infra/modules/dynamodb-checks/v1.0.0/outputs.tf
    - infra/modules/check-runner-role/v1.0.0/main.tf
    - infra/modules/check-runner-role/v1.0.0/variables.tf
    - infra/modules/check-runner-role/v1.0.0/outputs.tf
    - infra/live/use1/dynamodb-checks/terragrunt.hcl
    - infra/live/use1/check-runner-role/terragrunt.hcl
  modified:
    - internal/app/cmd/init.go
    - internal/app/cmd/init_plan_test.go

key-decisions:
  - "No dependency blocks on live units — role references only string inputs (bucket name, prefix, table name), not other module outputs; avoids mock_outputs boilerplate"
  - "check-runner-role envReqs: [KM_ARTIFACTS_BUCKET] so km init skips the role module on unconfigured installs without the bucket, consistent with other bucket-referencing modules"
  - "EventBridge target hardcoded to default event bus (arn:aws:events:region:account:event-bus/default) — CheckDispatch uses source=km.sandbox which routes on the default bus per existing pattern in pkg/aws/eventbridge.go"

patterns-established:
  - "All new Phase 116 modules placed immediately before ses in regionalModules() (ses stays last)"
  - "Pure-DDB scaffolding modules: no data sources, no dependency blocks, site.label for table name"

requirements-completed: []

# Metrics
duration: 4min
completed: "2026-06-18"
---

# Phase 116 Plan 01: km check scaffolding modules Summary

**Two control-plane terragrunt modules (dynamodb-checks + check-runner-role) registered in regionalModules(), bringing the fleet count from 22 to 24 with ses remaining last**

## Performance

- **Duration:** 4 min
- **Started:** 2026-06-18T00:28:38Z
- **Completed:** 2026-06-18T00:32:29Z
- **Tasks:** 3
- **Files modified:** 8 (created) + 2 (modified)

## Accomplishments

- Created `infra/modules/dynamodb-checks/v1.0.0/` — `{prefix}-checks` DDB table (hash key `name`, PAY_PER_REQUEST, SSE, no TTL, no required_providers)
- Created `infra/modules/check-runner-role/v1.0.0/` — shared `{prefix}-check-runner` Lambda execution role with 6 inline policies (CW Logs, S3 reads checks/*, S3 writes check-runs/*, EventBridge PutEvents on default bus, SSM GetParameter on {prefix}/checks/*, DynamoDB GetItem+Query on checks table)
- Created live terragrunt units for both modules (`infra/live/use1/dynamodb-checks/` and `infra/live/use1/check-runner-role/`) with no dependency blocks
- Added both entries to `regionalModules()` in `internal/app/cmd/init.go` (before ses); bumped `TestRunInitPlan_ModuleOrder` count 22→24; `make build` green (v0.4.989)

## Task Commits

1. **Task 1: Create the dynamodb-checks + check-runner-role TF modules** - `ed5efae2` (feat — bundled with prior session's 116-03 commit; modules authored and verified in this session)
2. **Task 2: Create the two live terragrunt units** - `fb0427a4` (feat)
3. **Task 3: Register both modules in regionalModules() + bump module-order test** - `449cc05e` (feat)

## Files Created/Modified

- `infra/modules/dynamodb-checks/v1.0.0/main.tf` — aws_dynamodb_table "checks", hash key name, PAY_PER_REQUEST, SSE
- `infra/modules/dynamodb-checks/v1.0.0/variables.tf` — table_name (default km-checks), tags
- `infra/modules/dynamodb-checks/v1.0.0/outputs.tf` — table_name, table_arn
- `infra/modules/check-runner-role/v1.0.0/main.tf` — aws_iam_role + 6 aws_iam_role_policy inline policies
- `infra/modules/check-runner-role/v1.0.0/variables.tf` — role_name, artifacts_bucket, resource_prefix, table_name, tags
- `infra/modules/check-runner-role/v1.0.0/outputs.tf` — role_arn, role_name
- `infra/live/use1/dynamodb-checks/terragrunt.hcl` — live unit; table_name from site.label
- `infra/live/use1/check-runner-role/terragrunt.hcl` — live unit; artifacts_bucket via get_env("KM_ARTIFACTS_BUCKET")
- `internal/app/cmd/init.go` — two new regionalModule entries before ses
- `internal/app/cmd/init_plan_test.go` — TestRunInitPlan_ModuleOrder count 22→24 + comment updated

## Decisions Made

- No dependency blocks on either live unit — the role inputs are pure strings (bucket name, prefix, table name), not module outputs. Eliminates mock_outputs boilerplate and avoids the `project_terragrunt_show_needs_mocks` footgun.
- `check-runner-role` gets `envReqs: []string{"KM_ARTIFACTS_BUCKET"}` so `km init` skips it gracefully on installs where the bucket hasn't been configured (consistent with lambda-*-bridge pattern).
- EventBridge target is the AWS default event bus — the existing `pkg/aws/eventbridge.go` pattern confirms `km.sandbox` source events are emitted to default bus, so the IAM policy resource uses `arn:aws:events:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:event-bus/default`.

## Deviations from Plan

None — plan executed exactly as written. TF modules were already authored by a prior session commit (ed5efae2); this session verified they are correct (fmt clean, no required_providers) and completed Tasks 2 and 3 which were unambiguously missing.

## Issues Encountered

The initial `git add + commit` for Task 1 returned exit 1 because the module files were already committed in `ed5efae2` (a prior session had bundled the TF modules with the 116-03 config struct work). Verified the committed files match the plan spec (fmt clean, no required_providers, correct schema) and proceeded to complete the unfinished Tasks 2 and 3.

## Next Phase Readiness

- `km init --dry-run=false` will now provision the `{prefix}-checks` table and `{prefix}-check-runner` role (after `make build` — binary carries the new entries per `project_make_build_precedes_km_init`)
- Plan 116-02 (`pkg/dispatch.ResumeOrCreate`) has already been started on this branch (`082f898c` RED + `35abe332` GREEN)
- Plan 116-03 (`ChecksConfig` config plumbing) is partially complete (`ed5efae2` + `07e286e2`)

---
*Phase: 116-km-check-serverless-check-runner*
*Completed: 2026-06-18*

## Self-Check: PASSED

All 8 created files exist on disk. Task commits fb0427a4 and 449cc05e verified in git log. TF module commit ed5efae2 (prior session) also verified. TestRunInitPlan_ModuleOrder exits 0 with count 24.
