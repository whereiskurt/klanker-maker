---
phase: 61-km-shell-ctrl-c-fix-switch-interactive-ssm-sessions-from-aws-startinteractivecommand-to-a-parameterized-standard-stream-document-with-runasdefaultuser-sandbox
plan: "01"
subsystem: infra
tags: [terraform, terragrunt, ssm, aws-ssm-document, session-manager, init]

# Dependency graph
requires: []
provides:
  - aws_ssm_document resource KM-Sandbox-Session (Standard_Stream, runAsDefaultUser=sandbox, parameterized shellProfile)
  - infra/modules/ssm-session-doc/v1.0.0 Terraform module
  - infra/live/use1/ssm-session-doc Terragrunt live wiring
  - ssm-session-doc entry in regionalModules() for km init provisioning
affects:
  - 61-02 (CLI callsite switch from AWS-StartInteractiveCommand to KM-Sandbox-Session)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "SSM Session document via aws_ssm_document resource (document_type=Session, document_format=JSON)"
    - "lifecycle { create_before_destroy = true } for schema-v1.0 SSM Session docs (no in-place UpdateDocument)"
    - "Conditional shellProfile.linux: [ -z '{{ command }}' ] && exec bash -l || bash -lc '{{ command }}'"

key-files:
  created:
    - infra/modules/ssm-session-doc/v1.0.0/main.tf
    - infra/modules/ssm-session-doc/v1.0.0/variables.tf
    - infra/modules/ssm-session-doc/v1.0.0/outputs.tf
    - infra/live/use1/ssm-session-doc/terragrunt.hcl
  modified:
    - internal/app/cmd/init.go
    - internal/app/cmd/init_test.go

key-decisions:
  - "CONTEXT.md IAM locked decision overridden: grep confirms ssm:StartSession only appears in SCP DenySSMPivot deny entry; no per-resource ALLOW policy exists in infra/modules/ to extend. Operator SSO uses AdministratorAccess from IAM Identity Center outside this repo."
  - "ssm-session-doc placed after dynamodb-schedules and before s3-replication in regionalModules() — matches no-dependency grouping pattern"
  - "shellProfile.linux uses conditional one-liner to handle both empty command (exec bash -l) and non-empty command (bash -lc) cases"
  - "RegionalModules exported function already existed in init.go — no new export needed"

patterns-established:
  - "SSM Session doc module pattern: aws_ssm_document with jsonencode content, document_type=Session, document_format=JSON, lifecycle create_before_destroy"
  - "Terragrunt live wiring for new regional modules mirrors infra/live/use1/dynamodb-schedules/terragrunt.hcl pattern"

requirements-completed: []

# Metrics
duration: 3min
completed: 2026-04-25
---

# Phase 61 Plan 01: SSM Session Document Module Summary

**KM-Sandbox-Session Terraform module with Standard_Stream sessionType and conditional bash-l shellProfile, wired into regionalModules() so km init provisions the doc required for Plan 02's Ctrl+C fix**

## Performance

- **Duration:** 3 min
- **Started:** 2026-04-25T23:02:28Z
- **Completed:** 2026-04-25T23:05:04Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments
- Created `infra/modules/ssm-session-doc/v1.0.0/` Terraform module with `aws_ssm_document` resource using `Standard_Stream` sessionType, `runAsDefaultUser=sandbox`, and conditional `shellProfile.linux` template
- Created `infra/live/use1/ssm-session-doc/terragrunt.hcl` Terragrunt live wiring using same pattern as `dynamodb-schedules`
- Added `ssm-session-doc` entry to `regionalModules()` between `dynamodb-schedules` and `s3-replication` (no envReqs)
- Added `TestRegionalModulesIncludesSSMDoc` and updated `TestRunInitWithRunnerAllModules` (6→7) and `TestRunInitSkipsSESWithoutZoneID` (5→6)

## Task Commits

Each task was committed atomically:

1. **Task 1: Create ssm-session-doc Terraform module + Terragrunt live wiring** - `fd49e11` (feat)
2. **Task 2: Add ssm-session-doc to regionalModules() and update init_test.go** - `c0743b1` (feat)

**Plan metadata:** (final commit below)

## Files Created/Modified
- `infra/modules/ssm-session-doc/v1.0.0/main.tf` - aws_ssm_document KM-Sandbox-Session with Standard_Stream sessionType, runAsDefaultUser=sandbox, conditional shellProfile
- `infra/modules/ssm-session-doc/v1.0.0/variables.tf` - document_name (default KM-Sandbox-Session) and tags variables
- `infra/modules/ssm-session-doc/v1.0.0/outputs.tf` - document_name and document_arn outputs
- `infra/live/use1/ssm-session-doc/terragrunt.hcl` - Terragrunt live wiring for use1 region
- `internal/app/cmd/init.go` - ssm-session-doc entry inserted in regionalModules() after dynamodb-schedules
- `internal/app/cmd/init_test.go` - TestRegionalModulesIncludesSSMDoc added; TestRunInitWithRunnerAllModules count 6→7; TestRunInitSkipsSESWithoutZoneID count 5→6

## Decisions Made
- Overrode CONTEXT.md locked IAM decision: `grep -r 'ssm:StartSession' infra/modules/` confirms only SCP DenySSMPivot deny entry exists — no per-resource ALLOW policy in this repo to extend. Operator SSO AdministratorAccess is provisioned outside this repo (IAM Identity Center). The SCP already permits operator's SSO role via trustedSSM list.
- `RegionalModules` exported function + `RegionalModule` struct already existed in init.go (lines 63-79) — no new export was needed.

## Deviations from Plan

None — plan executed exactly as written, including the pre-execution IAM evidence audit (`grep -r 'ssm:StartSession' infra/modules/` returned only the SCP deny entry, confirming the `<decisions_revised>` override).

## Issues Encountered
None.

## User Setup Required
None — no external service configuration required. The `KM-Sandbox-Session` document will be provisioned when operators run `km init <region>`.

## Next Phase Readiness
- Plan 02 can now switch the four CLI callsites (`shell.go:214`, `agent.go:300`, `agent.go:373`, `agent.go:532`) from `AWS-StartInteractiveCommand` to `KM-Sandbox-Session`
- The `ssm-session-doc` module is validated (`terraform validate` succeeds), wired into `regionalModules()`, and test coverage is complete
- Operators who run `km init us-east-1` after this change will have the SSM document provisioned in AWS

---
*Phase: 61-km-shell-ctrl-c-fix*
*Completed: 2026-04-25*
