---
phase: 12-ecs-budget-topup-s3-replication
plan: "02"
subsystem: infra
tags: [terragrunt, s3, replication, aws-provider, cross-region]

requires:
  - phase: 12-01
    provides: s3-replication Terraform module at infra/modules/s3-replication/v1.0.0

provides:
  - Terragrunt live deployment config at infra/live/use1/s3-replication/terragrunt.hcl
  - Dual-provider (default + replica alias) override pattern for cross-region S3 replication
  - Wires KM_ARTIFACTS_BUCKET and KM_REPLICA_REGION env vars into module inputs

affects: [phase-09-live-infrastructure-operator-docs, any phase running terragrunt apply for s3-replication]

tech-stack:
  added: []
  patterns:
    - "generate \"provider\" block name collision override: use same block name as root terragrunt.hcl with if_exists=overwrite_terragrunt to replace single-provider root config with dual-provider config"
    - "Cross-region provider injection via get_env for destination region without dependency blocks"

key-files:
  created:
    - infra/live/use1/s3-replication/terragrunt.hcl
  modified: []

key-decisions:
  - "generate \"provider\" block MUST be named 'provider' (not 'providers') to overwrite root-generated provider.tf via overwrite_terragrunt; misname causes duplicate provider blocks"
  - "Replica region read from KM_REPLICA_REGION env var (default us-west-2); no dependency block on source bucket since it already exists"
  - "State key follows tf-km/{region_label}/s3-replication/terraform.tfstate pattern matching existing live configs"

patterns-established:
  - "Dual-provider override pattern: use same generate block name as root with if_exists=overwrite_terragrunt"

requirements-completed: [OBSV-06]

duration: 1min
completed: 2026-03-22
---

# Phase 12 Plan 02: s3-replication Terragrunt Live Config Summary

**Terragrunt live config for cross-region S3 artifact replication with dual AWS provider override (default us-east-1 + replica alias via KM_REPLICA_REGION)**

## Performance

- **Duration:** ~1 min
- **Started:** 2026-03-22T00:51:11Z
- **Completed:** 2026-03-22T00:52:12Z
- **Tasks:** 1
- **Files modified:** 1

## Accomplishments

- Created `infra/live/use1/s3-replication/terragrunt.hcl` closing OBSV-06
- Override root-generated provider block with dual-provider config (default + alias "replica") using same block name and `if_exists = "overwrite_terragrunt"` to prevent duplicate provider blocks
- Wired all four module inputs via `get_env` — no hard-coded values, no dependency blocks

## Task Commits

Each task was committed atomically:

1. **Task 1: Create s3-replication Terragrunt live config with dual-provider support** - `4255094` (feat)

**Plan metadata:** (docs commit below)

## Files Created/Modified

- `infra/live/use1/s3-replication/terragrunt.hcl` - Terragrunt live config sourcing `infra/modules/s3-replication/v1.0.0` with dual provider generation, remote state, and env-var-driven inputs

## Decisions Made

- The `generate "provider"` block MUST be named `"provider"` (same as in root `terragrunt.hcl`) so that `if_exists = "overwrite_terragrunt"` replaces the root-generated file rather than creating a second `provider.tf`, which would cause duplicate provider blocks and a Terraform error.
- Replica region is read from `KM_REPLICA_REGION` env var with a default of `us-west-2`; no `dependency` block is used since the source bucket is pre-existing infrastructure.
- State key follows the established pattern: `tf-km/{region_label}/s3-replication/terraform.tfstate`.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required. Operators must set `KM_ARTIFACTS_BUCKET` and optionally `KM_REPLICA_REGION` (defaults to `us-west-2`) before running `terragrunt apply`.

## Next Phase Readiness

- `infra/live/use1/s3-replication/terragrunt.hcl` is ready for `terragrunt apply`
- Operator must ensure `KM_ARTIFACTS_BUCKET` is set to the existing artifacts bucket name
- The s3-replication module will create the replica bucket and configure replication rules for the `artifacts/` prefix only

---
*Phase: 12-ecs-budget-topup-s3-replication*
*Completed: 2026-03-22*
