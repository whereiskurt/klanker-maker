---
phase: 80-km-cluster-cross-account-irsa-for-k8s-integrations
plan: "02"
status: checkpoint-paused
checkpoint: human-verify
paused_at: Task 3 — Zero-net-diff terragrunt plan verification
completed_tasks: [1, 2]
pending_tasks: [3]
---

# Phase 80 Plan 02 Progress — Paused at Checkpoint

## Completed Tasks

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Create km-operator-policy/v1.0.0 module with 14 extracted policies | be10872 | infra/modules/km-operator-policy/v1.0.0/main.tf, variables.tf, outputs.tf |
| 2 | Refactor create-handler to consume shared module + 14 moved blocks | 479dabe | infra/modules/create-handler/v1.0.0/main.tf |

## What Was Built

**Task 1 — km-operator-policy/v1.0.0 module:**
- `infra/modules/km-operator-policy/v1.0.0/main.tf` — 14 `aws_iam_role_policy` resources extracted verbatim from create-handler (s3_artifacts, dynamodb, dynamodb_sandboxes, terraform_state, ec2_provisioning, iam_sandbox, ecs_provisioning, scheduler, ssm, ssm_send_command, ses_send, lambda_budget, kms, sqs_slack_inbound)
- `infra/modules/km-operator-policy/v1.0.0/variables.tf` — 8 inputs: role_id, resource_prefix, artifact_bucket_arn, state_bucket, dynamodb_table_name, dynamodb_budget_table_arn, sandbox_table_name, identities_table_name
- `infra/modules/km-operator-policy/v1.0.0/outputs.tf` — empty (policies attach directly to var.role_id)
- cloudwatch_logs intentionally NOT extracted (Lambda-specific per RESEARCH.md Open Question 1)
- `terraform validate` exits 0

**Task 2 — create-handler refactor:**
- Removed 14 inline `aws_iam_role_policy` resources from `infra/modules/create-handler/v1.0.0/main.tf`
- Added `module "km_operator_policy"` block with all 8 inputs wired
- Added 14 `moved {}` blocks at bottom of main.tf mapping old addresses → module addresses
- cloudwatch_logs still inline (no moved block)
- `terraform validate` exits 0

## Pending: Task 3 (HARD GATE)

**Checkpoint Type:** human-verify (blocking gate)

Operator must run `terragrunt plan -detailed-exitcode` against `infra/live/use1/create-handler/` and confirm the output shows zero net change.

### Verification Steps

1. Load AWS credentials for klanker-application profile:
   ```bash
   export AWS_PROFILE=klanker-application
   aws sts get-caller-identity
   ```

2. Run the plan:
   ```bash
   cd /Users/khundeck/working/klankrmkr/infra/live/use1/create-handler
   terragrunt plan -detailed-exitcode 2>&1 | tee /tmp/80-02-plan.txt
   echo "exit:$?"
   ```

3. Inspect `/tmp/80-02-plan.txt` for:
   - **Pattern A (preferred):** exit 0, "No changes."
   - **Pattern B (acceptable):** exit 2 with only `# module.km_operator_policy.aws_iam_role_policy.X has moved from aws_iam_role_policy.X` lines — NO destroy+create, NO `~ aws_iam_role.create_handler`

4. Additional sanity check:
   ```bash
   grep -c "moved from aws_iam_role_policy" /tmp/80-02-plan.txt
   # Should return 14 for Pattern B
   ```

5. Reply `approved` if Pattern A or B. Paste diff snippet if any policy is destroyed/created/modified.
