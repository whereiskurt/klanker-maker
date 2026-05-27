---
phase: 89-sops-secret-injection-for-sandboxes
plan: 02
subsystem: infra
tags: [terraform, kms, sops, s3, ec2spot, iam, lifecycle]

# Dependency graph
requires:
  - phase: 87-snapshot-backed-ebs
    provides: ec2spot v1.1.0 module that v1.2.0 is built on top of
  - phase: 84-ses-foundation-refactor
    provides: ses-shared-rule-set live wiring pattern reused for sandbox-secrets-key
  - phase: 75-slack-s3-lifecycle
    provides: s3-artifacts-lifecycle v1.0.0 that v1.1.0 extends
provides:
  - "infra/modules/sandbox-secrets-key/v1.0.0: KMS key + alias for SOPS bundle decryption"
  - "infra/live/use1/sandbox-secrets-key/terragrunt.hcl: live wiring for km bootstrap --shared-secrets-key"
  - "infra/modules/s3-artifacts-lifecycle/v1.1.0: additive sandbox-secrets-7day lifecycle rule"
  - "infra/modules/ec2spot/v1.2.0: IAM policies for KMS Decrypt + S3 GetObject of own SOPS bundle"
affects:
  - 89-04-bootstrap-cli
  - 89-05-compiler
  - 89-06-doctor

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Module bump pattern: new version dir (v1.1.0, v1.2.0) copied from previous, then additively modified — no in-place edit to existing versions"
    - "register_secrets_key bool toggle: mirrors ses-shared-rule-set register_shared_rule_set for bootstrap auto-detect"
    - "kms:ResourceAliases condition: scopes sandbox role's KMS Decrypt to the specific per-install alias without needing the key ARN at compile time"
    - "S3 secrets bundle path: sandboxes/{sandbox_id}/secrets.enc.yaml — consistent across lifecycle rule prefix, IAM policy ARN, and userdata fetch"

key-files:
  created:
    - infra/modules/sandbox-secrets-key/v1.0.0/main.tf
    - infra/modules/sandbox-secrets-key/v1.0.0/variables.tf
    - infra/modules/sandbox-secrets-key/v1.0.0/outputs.tf
    - infra/live/use1/sandbox-secrets-key/terragrunt.hcl
    - infra/modules/s3-artifacts-lifecycle/v1.1.0/main.tf
    - infra/modules/s3-artifacts-lifecycle/v1.1.0/variables.tf
    - infra/modules/ec2spot/v1.2.0/main.tf
    - infra/modules/ec2spot/v1.2.0/variables.tf
    - infra/modules/ec2spot/v1.2.0/outputs.tf
  modified: []

key-decisions:
  - "No required_providers blocks in any new module HCL — root.hcl is the single provider source (memory project_terragrunt_providers_in_root)"
  - "No dependency blocks in sandbox-secrets-key/terragrunt.hcl — same as ses-shared-rule-set pattern; mock_outputs_allowed_terraform_commands not needed"
  - "ec2spot_sandbox_secrets_s3 gated on artifacts_bucket != '' so pre-Phase-89 callers compile unchanged without passing the new variable"
  - "ec2spot_sandbox_secrets_kms uses kms:ResourceAliases condition (not key ARN) so the IAM policy works without knowing the key ARN at sandbox compile time"
  - "s3-artifacts-lifecycle live wiring NOT updated in this plan — the live module source bump is operator-driven via km init and owned by 89-05"

patterns-established:
  - "Module version bump: copy entire prior version directory, then make additive edits only — never modify existing version dirs"
  - "KMS alias-scoped IAM: use kms:ResourceAliases condition to scope Decrypt without hard-coding key ARN in per-sandbox policies"

requirements-completed:
  - SOPS-03-KMS-MODULE
  - SOPS-04-MODULE-WIRING
  - SOPS-09-IAM-SANDBOX
  - SOPS-17-S3-LIFECYCLE

# Metrics
duration: 4min
completed: 2026-05-27
---

# Phase 89 Plan 02: Terraform Module Surface for SOPS Secret Injection Summary

**KMS key module (sandbox-secrets-key/v1.0.0), s3-artifacts-lifecycle v1.1.0 with sandbox-secrets-7day rule, and ec2spot v1.2.0 with alias-scoped KMS Decrypt + S3 GetObject IAM policies — all terraform validate clean**

## Performance

- **Duration:** ~4 min
- **Started:** 2026-05-27T20:39:40Z
- **Completed:** 2026-05-27T20:43:17Z
- **Tasks:** 3
- **Files modified:** 9 created

## Accomplishments

- New `sandbox-secrets-key/v1.0.0` module: one `aws_kms_key` + one `aws_kms_alias` named `alias/${var.resource_prefix}-sandbox-secrets` with `prevent_destroy=true`, `enable_key_rotation=true`, `deletion_window_in_days=30`, and a dual-statement key policy (admin + AllowSandboxDecrypt scoped by `kms:ResourceAliases`)
- Live wiring at `infra/live/use1/sandbox-secrets-key/terragrunt.hcl` mirrors the ses-shared-rule-set pattern: reads site.hcl + region.hcl, passes `KM_REGISTER_SECRETS_KEY` env var (default true), tags `km:owner=foundation km:phase=89`
- `s3-artifacts-lifecycle/v1.1.0`: preserves existing `slack-inbound-30day` rule, adds `sandbox-secrets-7day` rule on `sandboxes/` prefix with 7-day expiration
- `ec2spot/v1.2.0`: adds `ec2spot_sandbox_secrets_kms` (kms:Decrypt+DescribeKey via `kms:ResourceAliases` condition) and `ec2spot_sandbox_secrets_s3` (s3:GetObject on `sandboxes/{sandbox_id}/secrets.enc.yaml`), both gated on `total_ec2spot_count > 0`

## Task Commits

Each task was committed atomically:

1. **Task 1: Create sandbox-secrets-key/v1.0.0 module + live terragrunt.hcl** - `4c97e23` (feat)
2. **Task 2: Bump s3-artifacts-lifecycle to v1.1.0 (additive 7-day rule)** - `15095c4` (feat)
3. **Task 3: Bump ec2spot to v1.2.0 (additive IAM policies)** - `5d8fa00` (feat)

## Files Created/Modified

- `infra/modules/sandbox-secrets-key/v1.0.0/main.tf` — KMS key + alias + key policy document
- `infra/modules/sandbox-secrets-key/v1.0.0/variables.tf` — resource_prefix, aws_region, register_secrets_key, tags
- `infra/modules/sandbox-secrets-key/v1.0.0/outputs.tf` — alias_name, key_arn, key_id
- `infra/live/use1/sandbox-secrets-key/terragrunt.hcl` — live wiring for km bootstrap --shared-secrets-key
- `infra/modules/s3-artifacts-lifecycle/v1.1.0/main.tf` — existing slack-inbound-30day + new sandbox-secrets-7day rule
- `infra/modules/s3-artifacts-lifecycle/v1.1.0/variables.tf` — unchanged copy of v1.0.0
- `infra/modules/ec2spot/v1.2.0/main.tf` — all v1.1.0 content + 2 new aws_iam_role_policy resources
- `infra/modules/ec2spot/v1.2.0/variables.tf` — unchanged copy from v1.1.0 (artifacts_bucket already present)
- `infra/modules/ec2spot/v1.2.0/outputs.tf` — unchanged copy from v1.1.0

## Decisions Made

- No `required_providers` blocks in any module HCL — root.hcl is the single source (memory project_terragrunt_providers_in_root)
- No `dependency` blocks in sandbox-secrets-key/terragrunt.hcl — mirrors ses-shared-rule-set, so `mock_outputs_allowed_terraform_commands` not needed (memory project_terragrunt_show_needs_mocks does not apply)
- `ec2spot_sandbox_secrets_s3` gated on `artifacts_bucket != ""` for backward-compat with pre-Phase-89 callers
- `kms:ResourceAliases` condition used instead of key ARN — sandbox IAM policies don't need the key ARN at compile time
- Live wiring for s3-artifacts-lifecycle is NOT changed here — source bump is operator-driven, owned by 89-05 compiler plan

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- `grep -c required_providers` flagged `main.tf` as count=1 due to a comment line referencing the term; confirmed no actual `required_providers { }` block exists

## User Setup Required

None - no external service configuration required. These are Terraform modules only. Apply is handled by:
- `km bootstrap --shared-secrets-key` (owned by plan 89-04) for the KMS key module
- `km init` (full, not `--sidecars`) for the s3-artifacts-lifecycle v1.1.0 bump
- Plan 89-05 (compiler) updates the sandbox ec2spot module reference from v1.1.0 to v1.2.0

## Next Phase Readiness

- 89-04 (bootstrap CLI) can now wire `km bootstrap --shared-secrets-key` to apply `infra/live/use1/sandbox-secrets-key/`
- 89-05 (compiler) can update ec2spot module emission from v1.1.0 to v1.2.0
- 89-06 (doctor) can reference `alias/${var.resource_prefix}-sandbox-secrets` for key presence check
- All three module dirs pass `terraform validate` from a clean backend-less init

---
*Phase: 89-sops-secret-injection-for-sandboxes*
*Completed: 2026-05-27*
