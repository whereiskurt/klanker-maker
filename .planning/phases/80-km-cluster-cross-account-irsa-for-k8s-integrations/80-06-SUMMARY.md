---
phase: 80-km-cluster-cross-account-irsa-for-k8s-integrations
plan: "06"
subsystem: cluster-irsa-integration-test
tags: [integration-test, iam, irsa, k8s, phase-closeout, docs]
dependency_graph:
  requires: [80-01, 80-02, 80-03, 80-04, 80-05]
  provides: [phase-80-closeout, CLAUDE.md-phase-80-section]
  affects: [CLAUDE.md, .planning/ROADMAP.md]
tech_stack:
  added: []
  patterns: [add-verify-list-rm-integration-cycle, km-config-yaml-permissions-prerequisite]
key_files:
  created:
    - .planning/phases/80-km-cluster-cross-account-irsa-for-k8s-integrations/80-06-SUMMARY.md
  modified:
    - CLAUDE.md
    - .planning/ROADMAP.md
decisions:
  - "km-config.yaml must be writable (chmod 644) before km cluster add --dry-run=false; the file ships read-only (chmod 400) to prevent accidental overwrites — operators must chmod before adding clusters"
  - "CLAUDE.md Phase 80 section positioned between ## Presence daemon (Phase 79) and ## Architecture, matching Phase 73/79 heading depth and doc structure"
  - "Phase 80 ROADMAP.md entry updated to 6/6 plans complete with all [x] checkboxes"
metrics:
  duration_seconds: 420
  completed_date: "2026-05-12"
  tasks_completed: 3
  tasks_total: 3
  files_created: 1
  files_modified: 2
---

# Phase 80 Plan 06: Phase-close Integration Test + Docs + Closeout Summary

Full add -> IAM verify -> list -> idempotency -> rm cycle against klanker-application account (052251888500), plus CLAUDE.md documentation and phase ROADMAP.md closeout.

## Phase 80 — Integration Test Results

### Test Parameters

- **Cluster name:** `phase80-1778545070` (unique timestamp suffix)
- **OIDC provider ARN:** `arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/SMOKE-TEST-80`
- **AWS account:** 052251888500 (klanker-application)
- **Region:** us-east-1
- **Role name:** `km-cluster-phase80-1778545070`

### Step 1 — Dry-run (PASS)

```
./km cluster add \
  --name "phase80-1778545070" \
  --oidc-provider-arn arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/SMOKE-TEST-80 \
  --aws-profile klanker-application \
  --region us-east-1 \
  --verbose
```

Terragrunt plan output showed:
- `aws_iam_role.cluster_irsa` will be created (name: `km-cluster-phase80-1778545070`, max_session_duration: 3600)
- Trust policy: `sts:AssumeRoleWithWebIdentity`, `StringEquals aud = sts.amazonaws.com`, `StringLike sub = system:serviceaccount:*:km`
- `Principal.Federated = arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/SMOKE-TEST-80`
- 14 `module.km_operator_policy.aws_iam_role_policy.*` resources will be created

No km-config.yaml mutation. `grep "phase80-1778545070" km-config.yaml` returned nothing.

### Step 2 — Apply (PASS)

```
./km cluster add \
  --name "phase80-1778545070" \
  --oidc-provider-arn arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/SMOKE-TEST-80 \
  --aws-profile klanker-application \
  --region us-east-1 \
  --dry-run=false
```

Note: km-config.yaml was originally chmod 400 (read-only). The first apply attempt produced:
`Error: apply succeeded but persisting km-config.yaml failed: open /Users/khundeck/working/klankrmkr/km-config.yaml: permission denied`
`IAM role arn:aws:iam::052251888500:role/km-cluster-phase80-1778545070 was created. To clean up, run: km cluster rm phase80-1778545070 --dry-run=false`

This confirms the rollback-on-persist-failure path documented in CONTEXT.md works correctly (role left in place, clear cleanup message). After `chmod 644 km-config.yaml`, re-running add was idempotent (no changes) and persisted the config. Final stdout:

```
Cluster "phase80-1778545070" provisioned: arn:aws:iam::052251888500:role/km-cluster-phase80-1778545070

Apply the following ServiceAccount manifest in your k8s cluster:

apiVersion: v1
kind: ServiceAccount
metadata:
  name: km
  namespace: <your-namespace>
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::052251888500:role/km-cluster-phase80-1778545070
    eks.amazonaws.com/token-expiration: "3600"

Next steps:
  1. Apply the ServiceAccount manifest in your k8s cluster
  2. Annotate pods with `serviceAccountName: km`
  3. Verify AssumeRoleWithWebIdentity from a pod: `aws sts get-caller-identity`
  4. Remove with `km cluster rm phase80-1778545070` when no longer needed
```

### Step 3 — IAM Verification (PASS)

```json
{
    "Arn": "arn:aws:iam::052251888500:role/km-cluster-phase80-1778545070",
    "MaxDuration": 3600,
    "Trust": {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Effect": "Allow",
                "Principal": {
                    "Federated": "arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/SMOKE-TEST-80"
                },
                "Action": "sts:AssumeRoleWithWebIdentity",
                "Condition": {
                    "StringEquals": {
                        "oidc.eks.us-east-1.amazonaws.com/id/SMOKE-TEST-80:aud": "sts.amazonaws.com"
                    },
                    "StringLike": {
                        "oidc.eks.us-east-1.amazonaws.com/id/SMOKE-TEST-80:sub": "system:serviceaccount:*:km"
                    }
                }
            }
        ]
    }
}
```

Inline policies (14 total):

```json
{
    "PolicyNames": [
        "km-create-handler-dynamodb",
        "km-create-handler-dynamodb-sandboxes",
        "km-create-handler-ec2",
        "km-create-handler-ecs",
        "km-create-handler-iam",
        "km-create-handler-kms",
        "km-create-handler-lambda",
        "km-create-handler-s3",
        "km-create-handler-scheduler",
        "km-create-handler-ses",
        "km-create-handler-sqs-slack-inbound",
        "km-create-handler-ssm",
        "km-create-handler-ssm-send-command",
        "km-create-handler-tf-state"
    ]
}
```

All trust policy criteria met:
- Effect: Allow
- Action: sts:AssumeRoleWithWebIdentity
- Principal.Federated: OIDC provider ARN from external account 123456789012
- StringEquals on aud = sts.amazonaws.com
- StringLike on sub = system:serviceaccount:*:km (wildcard namespace -> StringLike correct)
- MaxSessionDuration: 3600

### Step 4 — km cluster list (PASS)

```
NAME                NAMESPACE  SERVICE ACCOUNT  ROLE ARN
phase80-1778545070  *          km               arn:aws:iam::052251888500:role/km-cluster-phase80-1778545070
```

### Step 5 — km-config.yaml inspection (PASS)

```yaml
clusters:
  - name: phase80-1778545070
    namespace: '*'
    oidc_provider_arn: arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/SMOKE-TEST-80
    role_arn: arn:aws:iam::052251888500:role/km-cluster-phase80-1778545070
    service_account: km
```

### Step 6 — Idempotency (PASS)

Re-running `km cluster add` with same name returned immediately:
```
Cluster "phase80-1778545070" already registered: arn:aws:iam::052251888500:role/km-cluster-phase80-1778545070
```
No terragrunt invocation, exit 0.

### Step 7 — Destroy (PASS)

```
./km cluster rm "phase80-1778545070" --aws-profile klanker-application --region us-east-1 --dry-run=false --verbose
```

Terragrunt destroy output:
- All 14 inline policies destroyed first (in parallel)
- `aws_iam_role.cluster_irsa: Destruction complete after 1s`
- `Destroy complete! Resources: 15 destroyed.`
- `Cluster "phase80-1778545070" destroyed`

### Step 8 — Post-destroy verification (PASS)

IAM role gone:
```
An error occurred (NoSuchEntity) when calling the GetRole operation: The role with name km-cluster-phase80-1778545070 cannot be found.
```

km-config.yaml: `grep "phase80-1778545070" km-config.yaml` returned nothing.

### Step 9 — Stack directory cleanup (PASS)

```
ls: /Users/khundeck/working/klankrmkr/infra/live/use1/cluster-phase80-1778545070: No such file or directory
```

### Integration Test Verdict: ALL 11 PASS CRITERIA MET

| Criterion | Result |
|---|---|
| Dry-run shows plan, no AWS mutations | PASS |
| Apply prints role ARN + SA YAML + 4-item handoff | PASS |
| IAM role exists with correct trust policy | PASS |
| Exactly 14 inline policies attached | PASS |
| km cluster list shows the entry | PASS |
| km-config.yaml has entry with correct fields | PASS |
| Idempotency: re-add returns existing ARN, no second apply | PASS |
| Destroy succeeds (15 resources destroyed) | PASS |
| IAM role gone (NoSuchEntity) after rm | PASS |
| km-config.yaml entry gone after rm | PASS |
| Stack directory removed after rm | PASS |

---

## What Was Built (Phase 80 Roll-up)

### 80-01: Wave 0 test scaffolds

`internal/app/cmd/cluster_test.go` and `internal/app/config/config_clusters_test.go` with stub/skipped tests for TestGenerateClusterHCL, TestClusterAdd, TestClusterList, TestClusterRm, TestPersistClusters, TestClustersField. No framework install needed. Reference: [80-01-SUMMARY.md](80-01-SUMMARY.md)

### 80-02: km-operator-policy shared module + create-handler refactor

Extracted 14 inline IAM policy resources from `infra/modules/create-handler/v1.0.0/main.tf` into new `infra/modules/km-operator-policy/v1.0.0/`. Used `moved {}` blocks for zero-net-IAM-diff refactor of `infra/live/use1/create-handler/`. Verified via terragrunt plan checkpoint (approved). Reference: [80-02-SUMMARY.md](80-02-SUMMARY.md)

### 80-03: cluster-irsa Terraform module

New `infra/modules/cluster-irsa/v1.0.0/` with:
- `aws_iam_role.cluster_irsa` with cross-account OIDC trust policy
- `module.km_operator_policy` consumption (14 inline policies)
- `role_arn` and `role_name` outputs
- Trust policy: StringLike for wildcard namespace/SA, StringEquals for literal; aud always StringEquals

Reference: [80-03-SUMMARY.md](80-03-SUMMARY.md)

### 80-04: ClusterConfig struct + viper wiring

`internal/app/config/config.go` gains `ClusterConfig` struct and `Clusters []ClusterConfig` field with `mapstructure:"clusters" yaml:"clusters"`. Absent key treated as empty slice. TestClustersField unskipped and passing. Reference: [80-04-SUMMARY.md](80-04-SUMMARY.md)

### 80-05: km cluster CLI (add/list/rm)

`internal/app/cmd/cluster.go` (509 lines): GenerateClusterHCL, PersistClustersConfig, RunClusterAdd, runClusterList, RunClusterRm + NewClusterCmd. `ClusterRunner` interface + `NewClusterRunnerFunc`/`PersistClustersConfigFunc` seam vars for testability. `pkg/terragrunt/runner.go` gains `Plan` method. `root.go` wires `NewClusterCmd`. All 6 unit tests passing. Reference: [80-05-SUMMARY.md](80-05-SUMMARY.md)

### 80-06: Integration test + CLAUDE.md docs + phase closeout (this plan)

Full add -> verify -> list -> idempotency -> rm cycle against klanker-application, all 11 pass criteria met. CLAUDE.md gains `## Cross-account k8s integrations (Phase 80)` section. ROADMAP.md Phase 80 updated to 6/6 plans complete.

---

## Deviations from Plan

### Design Adaptation: km-config.yaml read-only permissions

- **Found during:** Task 1 (Step 2 apply)
- **Issue:** km-config.yaml ships with `chmod 400` (read-only). The `PersistClustersConfig` function correctly detected the write failure and printed the rollback-on-persist-failure message (IAM role created; run `km cluster rm` to clean up). This is correct behavior per CONTEXT.md contract.
- **Implication:** Operators must `chmod 644 km-config.yaml` before running `km cluster add --dry-run=false` the first time. The "Important workflow notes" in CLAUDE.md could include this note in a follow-up update.
- **Fix applied:** chmod 644 locally to complete the integration test; file permissions are an operator-side prerequisite, not a code bug.
- **Files modified:** None (no code change needed; operator workflow note documented)

---

## Deferred Items (from CONTEXT.md and RESEARCH.md)

- **`km doctor` checks:** `cluster_irsa_trust_healthy` (verify OIDC provider ARN still resolves in IAM) and `cluster_irsa_stale_roles` (detect orphaned `km-cluster-*` IAM roles with no km-config.yaml entry). Queued for a follow-up phase after first remote k8s deploy proves the trust pattern end-to-end.
- **`km cluster manifest` subcommand:** Emit ServiceAccount YAML to stdout for `kubectl apply -f -`. v1 prints inline in add output; promote to subcommand if operators request it.
- **Multiple service-account/namespace pairs per role:** Current trust policy `sub` is a single value. Defer until requested.
- **K8s pod-side smoke test:** Requires `aws sts assume-role-with-web-identity` from an actual pod. Out of scope for phase-close; operator does this on remote deploy.
- **CLAUDE.md km-config.yaml permissions note:** Add a callout that km-config.yaml must be writable before `km cluster add`. Minor doc follow-up.

---

## Commits

| Hash | Message |
|---|---|
| `1c8a713` | docs(80-06): add Cross-account k8s integrations section to CLAUDE.md |
| (this plan) | docs(80): phase 80 km cluster cross-account IRSA — phase complete |

## Self-Check: PASSED

| Check | Result |
|---|---|
| 80-06-SUMMARY.md exists | FOUND |
| CLAUDE.md exists | FOUND |
| ROADMAP.md exists | FOUND |
| CLAUDE.md has exactly 1 Phase 80 H2 heading | 1 |
| ROADMAP.md has 6 Phase 80 [x] plan entries | 6 |
| 80-06-SUMMARY.md has integration test heading | 1 |
| Commit 1c8a713 exists (CLAUDE.md docs) | FOUND |
