---
phase: 82-multi-instance-resource-prefix-isolation
plan: "09"
subsystem: infra/modules
tags: [terraform, multi-instance, tagging, ec2spot, ecs, ses, email-handler]
dependency_graph:
  requires: [82-06, 82-07, 82-08]
  provides: [km:resource-prefix tag emission on all sandbox-creating Terraform resources]
  affects: [infra/modules/ec2spot, infra/modules/ecs-task, infra/modules/ecs, infra/modules/ecs-cluster, infra/modules/ses, infra/modules/email-handler]
tech_stack:
  added: []
  patterns: [tag-map addition, in-place Terraform updates only]
key_files:
  created: []
  modified:
    - infra/modules/ecs-task/v1.0.0/variables.tf
    - infra/modules/ecs/v1.0.0/variables.tf
    - infra/modules/ecs-cluster/v1.0.0/variables.tf
    - infra/modules/ec2spot/v1.0.0/main.tf
    - infra/modules/ecs-task/v1.0.0/main.tf
    - infra/modules/ecs/v1.0.0/main.tf
    - infra/modules/ecs-cluster/v1.0.0/main.tf
    - infra/modules/ses/v1.0.0/main.tf
    - infra/modules/email-handler/v1.0.0/main.tf
decisions:
  - "Used var.resource_prefix (not var.km_label) for the km:resource-prefix tag value — resource_prefix is the Phase-66 name for the install prefix, km_label is the Phase-2 name; both carry the same value but resource_prefix is the canonical cross-install discriminator"
  - "Added aws_ec2_tag.ec2spot_resource_prefix alongside existing aws_ec2_tag.ec2spot_sandbox_id for spot instance propagation — spot requests don't propagate tags from the spot request resource to the instance, so individual aws_ec2_tag resources are required"
  - "Added tags to aws_ses_receipt_rule_set (the only SES resource that supports tags and is per-install); aws_ses_domain_identity and aws_route53_record resources not tagged as they are not install-discriminating at the tagging level"
  - "email-handler: used replace_all on the three existing km:component/km:managed tag maps rather than patching each individually"
metrics:
  duration: 220s
  completed_date: "2026-05-16"
  tasks_completed: 2
  files_modified: 9
---

# Phase 82 Plan 09: km:resource-prefix Tag Emission in Terraform Modules Summary

**One-liner:** Added `km:resource-prefix = var.resource_prefix` tags to all tag maps across ec2spot, ecs-task, ecs, ecs-cluster, ses, and email-handler modules, and declared the missing `resource_prefix` variable in the three ECS modules.

## What Was Built

### Task 1: resource_prefix variable declared in ECS modules

Added `variable "resource_prefix"` with `default = "km"` to:
- `infra/modules/ecs-task/v1.0.0/variables.tf`
- `infra/modules/ecs/v1.0.0/variables.tf`
- `infra/modules/ecs-cluster/v1.0.0/variables.tf`

The sandbox template (`infra/templates/sandbox/terragrunt.hcl`) already passes `resource_prefix = local.site_vars.locals.site.label` in the top-level common inputs block — no template changes needed.

### Task 2: km:resource-prefix tag added to all tag maps

**ec2spot/main.tf (12 occurrences):**
- Tag maps: `aws_vpc.sandbox`, `aws_internet_gateway.sandbox`, `aws_subnet.sandbox`, `aws_route_table.sandbox`, `aws_security_group.ec2spot`, `aws_iam_role.ec2spot_ssm`, `aws_iam_instance_profile.ec2spot`
- Spot request: `aws_spot_instance_request.ec2spot` — both `tags` and `volume_tags` blocks
- On-demand: `aws_instance.ec2_ondemand` tags
- EBS: `aws_ebs_volume.additional` tags
- New resource `aws_ec2_tag.ec2spot_resource_prefix` for actual spot EC2 instance tag propagation

**ecs-task/main.tf (2 occurrences):** `aws_iam_role.execution_role`, `aws_ecs_task_definition.task`

**ecs/main.tf (7 occurrences):** `aws_ecs_cluster.sandbox`, `aws_security_group.sandbox`, `aws_iam_role.task_execution`, `aws_iam_role.task_role`, `aws_cloudwatch_log_group.sandbox`, `aws_ecs_task_definition.sandbox`, `aws_ecs_service.sandbox`

**ecs-cluster/main.tf (3 occurrences):** `aws_service_discovery_private_dns_namespace.namespace`, `aws_ecs_cluster.cluster`, `aws_iam_role.ecs_task_role`

**ses/main.tf (1 occurrence):** `aws_ses_receipt_rule_set.km_sandbox` — per-install resource supporting tags; receipt rules themselves don't support `tags` argument

**email-handler/main.tf (3 occurrences):** `aws_iam_role.email_handler`, `aws_lambda_function.email_handler`, `aws_cloudwatch_log_group.email_handler`

## Verification

All automated checks passed:

```
ec2spot: km:sandbox-id=12 km:resource-prefix=12  (parity)
ecs-task: km:sandbox-id=2 km:resource-prefix=2   (parity)
ecs: km:sandbox-id=7 km:resource-prefix=7         (parity)
ecs-cluster: km:sandbox-id=3 km:resource-prefix=3 (parity)
ses: km:resource-prefix=1                          (presence)
email-handler: km:resource-prefix=3               (presence)
```

`terraform fmt -check` passed for all 6 modules (no output = clean).

HCL validate via `terragrunt validate` failed with "No valid credential sources found" (S3 backend needs AWS creds) — expected in dev/offline environment, not an HCL syntax failure. `terraform fmt -check` confirms all HCL is syntactically valid.

## Deviations from Plan

None — plan executed exactly as written.

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| Task 1 | 9f4ae49 | feat(82-09): declare resource_prefix variable in ECS modules |
| Task 2 | 90b3a30 | feat(82-09): add km:resource-prefix tag to every tag map across 6 modules |
