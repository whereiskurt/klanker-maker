---
phase: 01-schema-compiler-aws-foundation
plan: "02"
subsystem: infrastructure
tags: [terraform, terragrunt, ecs, fargate, ec2-spot, networking, secrets, iam]
dependency_graph:
  requires: []
  provides:
    - infra/modules/network/v1.0.0
    - infra/modules/ec2spot/v1.0.0
    - infra/modules/ecs-cluster/v1.0.0
    - infra/modules/ecs-task/v1.0.0
    - infra/modules/ecs-service/v1.0.0
    - infra/modules/secrets/v1.0.0
    - infra/live/site.hcl
    - infra/live/terragrunt.hcl
    - infra/live/sandboxes/_template/
  affects:
    - Phase 2 provisioning (depends on these modules existing locally)
tech_stack:
  added:
    - Terraform (HCL modules, v1.0.0 versioned subdirectories)
    - Terragrunt (site.hcl hierarchy, remote_state, provider generation)
    - AWS ECS Fargate / FARGATE_SPOT capacity providers
    - AWS EC2 Spot instances with IMDSv2
    - AWS KMS + SSM Parameter Store
    - AWS Service Discovery (private DNS)
  patterns:
    - Per-sandbox directory isolation via sandbox_id in state key (INFR-06)
    - Parameterized container definitions supporting main + 4 sidecar slots
    - SOPS secrets pattern (run_cmd sops decrypt with plaintext fallback)
    - km_label / region_label / sandbox_id naming convention across all modules
    - CLAUDE.md as Terragrunt repo root anchor (replaces AGENTS.md)
key_files:
  created:
    - infra/modules/network/v1.0.0/main.tf
    - infra/modules/network/v1.0.0/variables.tf
    - infra/modules/network/v1.0.0/outputs.tf
    - infra/modules/ec2spot/v1.0.0/main.tf
    - infra/modules/ec2spot/v1.0.0/variables.tf
    - infra/modules/ec2spot/v1.0.0/outputs.tf
    - infra/modules/ecs-cluster/v1.0.0/main.tf
    - infra/modules/ecs-cluster/v1.0.0/variables.tf
    - infra/modules/ecs-cluster/v1.0.0/outputs.tf
    - infra/modules/ecs-task/v1.0.0/main.tf
    - infra/modules/ecs-task/v1.0.0/variables.tf
    - infra/modules/ecs-task/v1.0.0/outputs.tf
    - infra/modules/ecs-service/v1.0.0/main.tf
    - infra/modules/ecs-service/v1.0.0/variables.tf
    - infra/modules/ecs-service/v1.0.0/outputs.tf
    - infra/modules/secrets/v1.0.0/main.tf
    - infra/modules/secrets/v1.0.0/variables.tf
    - infra/modules/secrets/v1.0.0/outputs.tf
    - infra/live/site.hcl
    - infra/live/terragrunt.hcl
    - infra/live/sandboxes/_template/terragrunt.hcl
    - infra/live/sandboxes/_template/service.hcl
  modified: []
decisions:
  - "Network module uses two skeleton security groups (sandbox_mgmt with no egress, sandbox_internal with self-only ingress) — Phase 2 profile compiler adds per-profile egress rules"
  - "ECS service module removes all load balancer resources — sandboxes use service discovery for intra-task communication; no public LB needed"
  - "ec2spot module drops tls_private_key, aws_key_pair, local_file, aws_route53_record — SSM-only access via IAM instance profile with AmazonSSMManagedInstanceCore"
  - "secrets module drops Secrets Manager resources — SSM Parameter Store only for Phase 1; Phase 2 evaluation deferred per plan"
  - "CLAUDE.md used as Terragrunt repo root anchor instead of AGENTS.md"
  - "ecs-service capacity_provider_strategy defaults to FARGATE_SPOT (weight=1) + FARGATE (base=1) for cost savings with fallback"
  - "service.hcl includes 5-container placeholder slots (main + dns-proxy + http-proxy + audit-log + tracing) per sidecar model"
metrics:
  duration_minutes: 25
  tasks_completed: 2
  tasks_total: 2
  files_created: 22
  files_modified: 0
  completed_date: "2026-03-21"
requirements_fulfilled:
  - INFR-06
  - INFR-08
---

# Phase 1 Plan 02: Terraform Module Copy & Adapt Summary

Six Terraform modules and a complete Terragrunt hierarchy copied from defcon.run.34 and adapted for the Klanker Maker sandbox isolation model, with all cross-repo references eliminated.

## What Was Built

### Six Terraform Modules (infra/modules/)

All modules use `km_label`, `region_label`, and `sandbox_id` variables (replacing the defcon `site.*` naming). Every resource carries a `km:sandbox-id` tag.

**network/v1.0.0** — VPC, public/private subnets, route tables, IGW, optional NAT gateway. ALB and NLB completely removed. Two sandbox-appropriate security groups created: `sandbox_mgmt` (no egress — Phase 2 compiler fills per-profile rules) and `sandbox_internal` (self-only ingress for sidecar communication).

**ec2spot/v1.0.0** — EC2 Spot instance with IMDSv2 enforcement (`http_tokens = "required"`). SSH key pair (`tls_private_key`, `aws_key_pair`, `local_file`) removed. DNS record creation removed. SSM-only IAM role (`AmazonSSMManagedInstanceCore`). `km:sandbox-id` tag propagated to actual EC2 instance via `aws_ec2_tag`.

**secrets/v1.0.0** — KMS key + SSM Parameter Store only. Secrets Manager resources removed (deferred to Phase 2 evaluation). KMS key policy grants access to SSM service and ECS tasks. SSM prefix template supports `{{KM_LABEL}}`, `{{REGION_LABEL}}`, `{{REGION}}` substitutions.

**ecs-cluster/v1.0.0** — ECS cluster with `FARGATE` + `FARGATE_SPOT` capacity providers (via `aws_ecs_cluster_capacity_providers`). Service discovery private DNS namespace. Least-privilege IAM task role (SSM, KMS, CloudWatch Logs, Service Discovery — not the broad wildcard from defcon source).

**ecs-task/v1.0.0** — ECS task definition with fully parameterized `containers` list supporting main container + 4 sidecar slots. Template variable substitution (`{{SANDBOX_ID}}`, `{{KM_LABEL}}`, `{{REGION_LABEL}}`, `{{REGION}}`) in environment values, secret paths, and health check commands. ECR image URL auto-construction.

**ecs-service/v1.0.0** — ECS Fargate service, load balancer configuration completely removed. FARGATE_SPOT preferred capacity strategy with FARGATE fallback. Service discovery optional. `km-{sandbox_id}-{name}-{region_label}` naming.

### Terragrunt Hierarchy (infra/live/)

**site.hcl** — Root config: `label = "km"`, `tf_state_prefix = "tf-km"`, SOPS secrets pattern, region config, S3 backend variables. Anchored by `find_in_parent_folders("CLAUDE.md")`.

**terragrunt.hcl** — S3 + DynamoDB remote state backend, AWS provider generation with `default_tags`, transient network error retry config.

**sandboxes/_template/terragrunt.hcl** — Per-sandbox directory isolation: reads `service.hcl` for `sandbox_id`, includes it in state key (`sandboxes/{sandbox_id}/terraform.tfstate`) for full isolation (INFR-06). Module source is substituted by profile compiler based on substrate.

**sandboxes/_template/service.hcl** — Sandbox service config pattern with `sandbox_id`, `substrate_module`, ECS task/service definitions, and 5-container placeholder slots (main + dns-proxy + http-proxy + audit-log + tracing). This file is what the Phase 2 profile compiler generates per sandbox.

## Deviations from Plan

### Auto-fixed Issues

None — plan executed exactly as written with one minor interpretation:

**Interpretation: ecs-cluster IAM policy scope**
The source defcon module used a broad wildcard policy (`s3:*`, `dynamodb:*`, etc.). For sandbox isolation this was replaced with a least-privilege baseline (SSM read, KMS decrypt, CloudWatch Logs, Service Discovery). Phase 2 compiler will extend per `identity.awsPermissions` in the profile.

## Self-Check: PASSED

All 22 created files verified on disk. Both task commits verified in git log:
- `de868fa`: feat(01-02): copy and adapt network, ec2spot, and secrets modules
- `e04457f`: feat(01-02): add ECS modules and Terragrunt hierarchy with sandbox template
