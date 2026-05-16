---
phase: 82-multi-instance-resource-prefix-isolation
plan: "08"
subsystem: infra
tags: [terraform, ecs, iam, ssm, multi-instance]

requires:
  - phase: 82-01
    provides: km_label variable pattern and research confirming pre-existing variable declarations

provides:
  - SSM IAM ARN in ecs-task module scoped to var.km_label namespace
  - SSM IAM ARN in ecs module scoped to var.km_label namespace
  - SSM IAM ARN in ecs-cluster module scoped to var.km_label namespace

affects:
  - 82-10 (Wave 3 dry-run gate — plan gate validates these three modules show no diff for km_label="km")
  - Any future ECS substrate sandbox provisions

tech-stack:
  added: []
  patterns:
    - "ECS task/service/cluster IAM SSM policy uses parameter/${var.km_label}/* instead of literal parameter/km/*"

key-files:
  created: []
  modified:
    - infra/modules/ecs-task/v1.0.0/main.tf
    - infra/modules/ecs/v1.0.0/main.tf
    - infra/modules/ecs-cluster/v1.0.0/main.tf

key-decisions:
  - "No new variable added — km_label already declared in all three variables.tf files (confirmed pre-flight)"
  - "One-line substitution per module; rest of ARN (region, account-id) left intact"

patterns-established:
  - "ECS substrate IAM SSM namespace: parameter/${var.km_label}/* pattern matches the EC2/Lambda/SES pattern from parallel Wave 3 plans"

requirements-completed: []

duration: 3min
completed: 2026-05-16
---

# Phase 82 Plan 08: ECS Modules SSM ARN Interpolation Summary

**Three ECS module IAM policies now scope SSM access to `parameter/${var.km_label}/*` instead of the hardcoded `parameter/km/*` literal, eliminating the blocker that prevented second-install ECS tasks from reading their own SSM params.**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-05-16T17:13:41Z
- **Completed:** 2026-05-16T17:16:45Z
- **Tasks:** 1
- **Files modified:** 3

## Accomplishments

- Replaced `parameter/km/*` literal with `${var.km_label}/*` interpolation in `ecs-task/v1.0.0/main.tf` (line 156)
- Replaced `parameter/km/*` literal with `${var.km_label}/*` interpolation in `ecs/v1.0.0/main.tf` (line 126)
- Replaced `parameter/km/*` literal with `${var.km_label}/*` interpolation in `ecs-cluster/v1.0.0/main.tf` (line 132)
- Pre-flight confirmed all three modules already declare `variable "km_label"` — no new variable required

## Task Commits

Each task was committed atomically:

1. **Task 1: Replace parameter/km/* literal with parameter/${var.km_label}/* in 3 ECS modules** - `11428bb` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified

- `infra/modules/ecs-task/v1.0.0/main.tf` - SSM resource ARN in `aws_iam_role_policy.ssm_access` uses `${var.km_label}` interpolation
- `infra/modules/ecs/v1.0.0/main.tf` - SSM resource ARN in `aws_iam_role_policy.task_role` uses `${var.km_label}` interpolation
- `infra/modules/ecs-cluster/v1.0.0/main.tf` - SSM resource ARN in the cluster task policy uses `${var.km_label}` interpolation

## Decisions Made

No new variable added — RESEARCH.md finding #3 was correct; all three modules already declared `km_label` with default `"km"`. Backward compatibility preserved: existing `km` install evaluates to identical ARN `parameter/km/*`.

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required. Full validation deferred to Plan 82-10 (`terragrunt plan` gate with `km_label="km"` confirming zero diff).

## Next Phase Readiness

- All three ECS modules are ready for Plan 82-10's dry-run gate
- Wave 3 parallel plans 82-06 (ses), 82-07 (email-handler), and 82-08 (ecs-task/ecs/ecs-cluster) all target non-overlapping modules — no merge conflicts expected
- No `terragrunt apply` needed at this stage; Plan 82-10 is the apply gate

---
*Phase: 82-multi-instance-resource-prefix-isolation*
*Completed: 2026-05-16*
