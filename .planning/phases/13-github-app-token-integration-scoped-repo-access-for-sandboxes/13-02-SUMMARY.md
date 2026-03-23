---
phase: 13-github-app-token-integration-scoped-repo-access-for-sandboxes
plan: "02"
subsystem: infra
tags: [terraform, lambda, eventbridge-scheduler, kms, ssm, scp, github-app]

# Dependency graph
requires:
  - phase: 10-scp-sandbox-containment-org-level-ec2-breakout-prevention
    provides: SCP module with trusted_arns_ssm carve-out pattern
  - phase: 06-budget-enforcement-platform-configuration
    provides: budget-enforcer module as structural template for Lambda + EventBridge Scheduler pattern
provides:
  - github-token Terraform module (Lambda + EventBridge Scheduler 45-min + SSM IAM + KMS key with policy)
  - SCP trusted_arns_ssm carve-out for km-github-token-refresher-*
  - Makefile build-lambdas target for github-token-refresher.zip (arm64)
affects:
  - phase 13-03 (compiler wiring to emit github-token module in sandbox artifacts)
  - phase 13-04 (GitHub App token Go library that the Lambda will call)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - per-sandbox Lambda naming (km-github-token-refresher-{sandbox-id}) — same pattern as budget-enforcer
    - KMS key policy with three principals (root admin, Lambda encrypt, sandbox role decrypt)
    - EventBridge Scheduler payload embedding all metadata at creation time (no runtime SSM reads for config)
    - trusted_arns_ssm as the minimal carve-out for SSM-only Lambda roles in the SCP

key-files:
  created:
    - infra/modules/github-token/v1.0.0/main.tf
    - infra/modules/github-token/v1.0.0/variables.tf
    - infra/modules/github-token/v1.0.0/outputs.tf
  modified:
    - infra/modules/scp/v1.0.0/main.tf
    - Makefile

key-decisions:
  - "github-token-refresher added to trusted_arns_ssm only (not base/instance/iam) — it only needs SSM GetParameter/PutParameter, not EC2/IAM/instance mutation"
  - "KMS key policy embeds sandbox_iam_role_arn at module instantiation time — sandbox role gets decrypt without needing an SSM policy change"
  - "ssm_parameter_name defaults to /sandbox/{sandbox_id}/github-token via local — caller can override for custom paths"
  - "EventBridge Scheduler payload carries kms_key_arn, allowed_repos, permissions — Lambda has all data per invocation without extra SSM reads"

patterns-established:
  - "KMS key policy: three-principal model (root admin + Lambda encrypt/decrypt + sandbox role decrypt only)"
  - "Per-sandbox Lambda/Scheduler/KMS naming: km-{component}-{sandbox_id}"

requirements-completed:
  - GH-06
  - GH-07
  - GH-10
  - GH-13

# Metrics
duration: 6min
completed: "2026-03-23"
---

# Phase 13 Plan 02: GitHub Token Terraform Module Summary

**KMS-encrypted GitHub installation token pipeline: Lambda + 45-min EventBridge Scheduler + SSM SecureString + per-sandbox KMS key with SCP SSM carve-out**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-03-23T02:53:00Z
- **Completed:** 2026-03-23T02:59:53Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments

- Created github-token Terraform module with 10 resources (KMS key, KMS alias, Lambda IAM role, Lambda IAM policy, Lambda function, CloudWatch log group, scheduler IAM role, scheduler IAM policy, EventBridge Scheduler, Lambda permission)
- KMS key policy grants Lambda role encrypt+decrypt and sandbox execution role decrypt-only — sandbox can read the token without needing its own SSM policy
- SCP updated with minimal km-github-token-refresher-* carve-out in trusted_arns_ssm only — refresher needs SSM access, not EC2/IAM/instance operations
- Makefile build-lambdas target extended to produce github-token-refresher.zip alongside existing ttl-handler.zip and budget-enforcer.zip

## Task Commits

Each task was committed atomically:

1. **Task 1: Create github-token Terraform module** - `8dc35ba` (feat)
2. **Task 2: SCP carve-out + Makefile build target** - `827463c` (feat)

**Plan metadata:** committed with SUMMARY.md (docs)

## Files Created/Modified

- `infra/modules/github-token/v1.0.0/main.tf` - Lambda + EventBridge Scheduler + KMS + IAM resources for per-sandbox token refresher
- `infra/modules/github-token/v1.0.0/variables.tf` - Module inputs: sandbox_id, lambda_zip_path, installation_id, ssm_parameter_name, allowed_repos, permissions, sandbox_iam_role_arn
- `infra/modules/github-token/v1.0.0/outputs.tf` - lambda_function_arn, kms_key_arn, kms_key_id
- `infra/modules/scp/v1.0.0/main.tf` - Added km-github-token-refresher-* to trusted_arns_ssm
- `Makefile` - Added github-token-refresher build lines to build-lambdas target

## Decisions Made

- github-token-refresher carved out of SCP via trusted_arns_ssm only — rationale: the refresher's sole AWS operations are SSM GetParameter (config) and PutParameter (token write); it has no EC2, IAM, or instance mutation needs
- KMS key policy uses three-principal model — Lambda role gets Encrypt+Decrypt+GenerateDataKey (needs to write SecureString), sandbox role gets Decrypt-only (needs to read token), root admin gets kms:* for break-glass
- EventBridge Scheduler payload is fully self-contained — all data (kms_key_arn, installation_id, allowed_repos, permissions) embedded at creation time so the Lambda function requires no additional SSM reads beyond the GitHub App private key at /km/config/github/*

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- github-token module is deployable; compiler wiring (Plan 03) can reference it
- The Lambda binary at `cmd/github-token-refresher/` is expected by the Makefile — that binary is produced by Plan 01 (Go GitHub App client library)
- Module terraform fmt passes; ready for `terraform validate` once provider configured

---
*Phase: 13-github-app-token-integration-scoped-repo-access-for-sandboxes*
*Completed: 2026-03-23*

## Self-Check: PASSED

- FOUND: infra/modules/github-token/v1.0.0/main.tf
- FOUND: infra/modules/github-token/v1.0.0/variables.tf
- FOUND: infra/modules/github-token/v1.0.0/outputs.tf
- FOUND: commit 8dc35ba (feat: create github-token Terraform module)
- FOUND: commit 827463c (feat: SCP carve-out and Makefile build target)
