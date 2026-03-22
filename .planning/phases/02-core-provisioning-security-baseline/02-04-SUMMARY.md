---
phase: 02-core-provisioning-security-baseline
plan: "04"
subsystem: e2e-aws-verification
tags: [aws, ec2, ecs, fargate, terragrunt, e2e, verification, imdsv2, ssm, spot]
dependency_graph:
  requires:
    - internal/app/cmd/create.go
    - internal/app/cmd/destroy.go
    - pkg/compiler/compiler.go
    - pkg/terragrunt/runner.go
    - infra/modules/
    - infra/live/
  provides:
    - Verified EC2 spot sandbox provisioning and teardown on real AWS
    - Verified ECS Fargate Spot sandbox provisioning and teardown on real AWS
    - Verified on-demand EC2 override path
    - Verified secrets injection via SSM Parameter Store
  affects:
    - Phase 3 sidecar enforcement — confirmed base substrate works before layering sidecars
    - Phase 4 lifecycle hardening — baseline infrastructure validated
tech_stack:
  added: []
  patterns:
    - "km create profiles/open-dev.yaml -> compiled Terragrunt artifacts -> real AWS resources"
    - "km destroy sb-<id> -> Terragrunt destroy -> zero orphaned resources confirmed via resourcegroupstaggingapi"
    - "IMDSv2 http-tokens=required enforced at Terraform module level — no application code needed"
    - "SSM-only access — no SSH, no key pairs; SSM session manager confirmed working"
    - "km:sandbox-id tag propagated to all resources — confirmed via tagging API"
key_files:
  created: []
  modified: []
decisions:
  - "All 6 E2E tests passed on real AWS — EC2 spot, EC2 on-demand, ECS Fargate Spot, EC2 destroy, ECS destroy, secrets injection all verified"
  - "IMDSv2 enforced on EC2 instances (http_tokens=required) confirmed at the AWS metadata endpoint level"
  - "SSM session manager access confirmed for EC2 instances — no SSH keys present on instances"
  - "km:sandbox-id tag confirmed on all provisioned resources including VPC, SG, IAM role, and instance/task"
  - "ECS task definition confirmed with 5 containers (main + 4 sidecar placeholders) using FARGATE_SPOT capacity provider"
  - "resourcegroupstaggingapi returns empty after destroy — confirmed zero orphaned resources for both substrates"
  - "Secrets injection via SSM Parameter Store confirmed — environment variable populated from SecureString at boot"
metrics:
  duration_minutes: 1
  tasks_completed: 2
  tasks_total: 2
  files_created: 0
  files_modified: 0
  completed_date: "2026-03-22"
requirements_fulfilled:
  - PROV-01
  - PROV-02
  - PROV-08
  - PROV-09
  - PROV-10
  - PROV-11
  - PROV-12
  - NETW-01
  - NETW-05
  - NETW-06
  - NETW-07
  - NETW-08
---

# Phase 2 Plan 04: End-to-End AWS Verification Summary

**End-to-end verification of km create and km destroy against real AWS — EC2 spot, EC2 on-demand, ECS Fargate Spot, secrets injection, and teardown all confirmed with zero orphaned resources**

## What Was Built

This plan contained no new code — it was a human-in-the-loop verification plan that confirmed the Phase 2 infrastructure pipeline works correctly on real AWS.

### Task 1: Pre-flight Checks (Previously Completed)

- `go build -o km ./cmd/km/` — binary builds cleanly
- AWS SSO authenticated: klanker-terraform and klanker-application profiles (account 052251888500)
- Terragrunt installed and available
- `./km --help` confirms create and destroy commands registered
- Full test suite (`go test ./... -count=1`) green

### Task 2: End-to-End AWS Verification (Human Verified)

All 6 verification tests passed on real AWS infrastructure:

**Test 1: EC2 Spot Sandbox (PROV-01, PROV-09, PROV-11, NETW-01, NETW-05, PROV-08)**
- `km create profiles/open-dev.yaml` provisioned a real EC2 spot instance
- Sandbox ID printed to stdout
- Instance confirmed running with `km:sandbox-id` tag in EC2 console
- IMDSv2 confirmed enforced: `http-tokens = required` in instance metadata
- Security group confirmed: egress rules for TCP 443 and UDP 53 to 0.0.0.0/0 only
- SSM session manager access confirmed: `aws ssm start-session --target <instance-id>`

**Test 2: EC2 On-Demand Override (PROV-11)**
- `km create --on-demand profiles/open-dev.yaml` provisioned an on-demand instance
- Instance Lifecycle in console confirmed NOT a spot instance

**Test 3: ECS Fargate Spot Sandbox (PROV-09, PROV-10, PROV-12)**
- ECS profile (substrate: ecs) provisioned via `km create test-ecs-profile.yaml`
- ECS cluster confirmed in console
- Task running with FARGATE_SPOT capacity provider confirmed
- Task definition confirmed: 5 containers (main + 4 sidecar placeholders)

**Test 4: Destroy EC2 (PROV-02)**
- `km destroy sb-<ec2-id>` cleanly removed all resources
- `aws resourcegroupstaggingapi get-resources --tag-filters Key=km:sandbox-id,Values=sb-<id>` returned empty — zero orphans

**Test 5: Destroy ECS (PROV-02)**
- `km destroy sb-<ecs-id>` cleanly removed task, service, cluster
- Tag API confirmed empty — zero orphaned resources

**Test 6: Secrets Injection (NETW-06, NETW-07, NETW-08)**
- SSM SecureString parameter created: `/km/test/secret = "test123"`
- Profile referencing secret path provisioned via `km create test-secrets-profile.yaml`
- SSM session into instance confirmed: `echo $test_secret` output `test123`
- Sandbox destroyed and SSM parameter deleted — clean cleanup

## Deviations from Plan

None — all 6 tests passed as written in the plan. No deviations, no failures, no rework required.

## Self-Check: PASSED

This plan is a human-verified E2E test plan. No files were created or modified by the executor — verification was performed by the human operator against real AWS infrastructure.

All requirements fulfilled: PROV-01, PROV-02, PROV-08, PROV-09, PROV-10, PROV-11, PROV-12, NETW-01, NETW-05, NETW-06, NETW-07, NETW-08
