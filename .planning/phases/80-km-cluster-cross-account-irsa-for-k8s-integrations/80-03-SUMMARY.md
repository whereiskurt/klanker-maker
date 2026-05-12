---
phase: 80-km-cluster-cross-account-irsa-for-k8s-integrations
plan: "03"
subsystem: infra/modules/cluster-irsa
tags: [terraform, iam, irsa, k8s, cross-account, oidc]
dependency_graph:
  requires: [80-02]
  provides: [infra/modules/cluster-irsa/v1.0.0]
  affects: [80-05, 80-06]
tech_stack:
  added: []
  patterns:
    - "Cross-account IRSA trust policy with dynamic StringLike/StringEquals sub_condition"
    - "Module composition: cluster-irsa consumes km-operator-policy via relative source path"
key_files:
  created:
    - infra/modules/cluster-irsa/v1.0.0/main.tf
    - infra/modules/cluster-irsa/v1.0.0/variables.tf
    - infra/modules/cluster-irsa/v1.0.0/outputs.tf
    - infra/modules/cluster-irsa/v1.0.0/test/main.tf
    - infra/modules/cluster-irsa/v1.0.0/test/README.md
    - infra/modules/cluster-irsa/v1.0.0/test_harness.tf.skip
  modified: []
decisions:
  - "OIDC provider host stripped via regex replace (no data.aws_caller_identity needed for trust policy)"
  - "sub_condition uses StringLike when namespace or service_account_name contains wildcard, StringEquals otherwise"
  - "IAM role name follows {resource_prefix}-cluster-{cluster_name} convention"
  - "Module source path uses ../../km-operator-policy/v1.0.0 (relative); Plan 80-05 terragrunt.hcl must use // double-slash notation"
  - "test_harness.tf.skip marker committed alongside module (not a production Terraform file)"
metrics:
  duration: "3m18s"
  completed_date: "2026-05-11"
  tasks_completed: 2
  files_created: 6
  files_modified: 0
---

# Phase 80 Plan 03: cluster-irsa/v1.0.0 Terraform Module Summary

**One-liner:** Cross-account IRSA IAM role module with dynamic StringLike/StringEquals trust policy and km-operator-policy composition via relative module source.

## What Was Built

The `infra/modules/cluster-irsa/v1.0.0/` Terraform module provisions an IAM role whose
trust policy references an OIDC provider in a **remote AWS account** (the k8s cluster's
account). It then attaches the 14 km-operator policies by composing the shared
`km-operator-policy/v1.0.0` module from Plan 80-02.

## Variable Interface (for Plan 80-05's terragrunt.hcl `inputs` block)

The module accepts exactly 11 variables:

| Variable | Type | Purpose |
|---|---|---|
| `cluster_name` | string | Used in IAM role name: `{resource_prefix}-cluster-{cluster_name}` |
| `oidc_provider_arn` | string | ARN of remote OIDC provider (e.g. `arn:aws:iam::123456789012:oidc-provider/...`) |
| `namespace` | string | K8s namespace; `*` triggers StringLike trust condition |
| `service_account_name` | string | K8s SA name; `*` triggers StringLike trust condition |
| `resource_prefix` | string | Passed through to km-operator-policy (default: `km`) |
| `state_bucket` | string | Terraform state S3 bucket name |
| `artifact_bucket_arn` | string | S3 artifact bucket ARN |
| `dynamodb_table_name` | string | DynamoDB lock table name |
| `dynamodb_budget_table_arn` | string | DynamoDB budget table ARN |
| `sandbox_table_name` | string | DynamoDB sandbox metadata table name |
| `identities_table_name` | string | DynamoDB identities table name |

## Output Keys (for Plan 80-05's `runner.Output()` parsing)

| Output | Value |
|---|---|
| `role_arn` | `aws_iam_role.cluster_irsa.arn` |
| `role_name` | `aws_iam_role.cluster_irsa.name` |

## Module Source Path Convention

The km-operator-policy sub-module is referenced as:
```
source = "../../km-operator-policy/v1.0.0"
```

Plan 80-05's generated `terragrunt.hcl` MUST use the `//` double-slash notation
when referencing this module from an `infra/live/` path, so that Terragrunt copies
the full `infra/modules/` tree into its cache (enabling the relative source above
to resolve). See `infra/live/use1/create-handler/terragrunt.hcl` for the working pattern.

## Trust Policy Design

```hcl
locals {
  oidc_provider_host = replace(var.oidc_provider_arn, "/^arn:aws:iam::[0-9]+:oidc-provider\\//", "")
  has_wildcard       = can(regex("\\*", var.namespace)) || can(regex("\\*", var.service_account_name))
  sub_condition      = local.has_wildcard ? "StringLike" : "StringEquals"
}
```

- `aud` condition always uses `StringEquals` with `"sts.amazonaws.com"`
- `sub` condition switches between `StringLike` (wildcard) and `StringEquals` (literal)
- No `data.aws_caller_identity.current` is used in the trust policy (the OIDC provider ARN
  from the remote account is passed directly as `var.oidc_provider_arn`)

## Commits

| Task | Commit | Description |
|---|---|---|
| Task 1 | ef45d74 | feat(80-03): create cluster-irsa/v1.0.0 Terraform module |
| Task 2 | 2272c64 | test(80-03): add cluster-irsa smoke-test fixture + regex validation |

## Verification Results

- `terraform validate` exits 0 in `infra/modules/cluster-irsa/v1.0.0/`
- `terraform validate` exits 0 in `infra/modules/cluster-irsa/v1.0.0/test/` (both wildcard + literal)
- `terraform console` confirms OIDC ARN regex strips to `"oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE"`
- 11 variable blocks in variables.tf
- 2 output blocks in outputs.tf
- `module "km_operator_policy"` source path is `../../km-operator-policy/v1.0.0`

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

All 5 files found on disk. Both commits (ef45d74, 2272c64) confirmed in git log.
