---
phase: 80
slug: km-cluster-cross-account-irsa-for-k8s-integrations
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-11
---

# Phase 80 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing stdlib (`testing` package) |
| **Config file** | none — `go test ./...` convention |
| **Quick run command** | `go test ./internal/app/cmd/ -run TestCluster -v` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~120 seconds (full); ~3 seconds (cluster subset) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/ -run TestCluster -v`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green AND manual integration test against `klanker-application` profile must have completed successfully (role created → listed → destroyed)
- **Max feedback latency:** ~3 seconds for cluster-specific unit tests

---

## Per-Task Verification Map

> Populated by planner from PLAN.md task IDs once plans are written. Initial scaffold below; planner refines.

| Task ID | Plan | Wave | Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|----------|-----------|-------------------|-------------|--------|
| 80-01-XX | 01 | 1 | Extract km-operator-policy module from create-handler with zero net diff | manual (terragrunt plan) | `cd infra/live/use1/create-handler && terragrunt plan -detailed-exitcode` | N/A — Terraform | ⬜ pending |
| 80-01-XX | 01 | 1 | All 14 policies present in shared module | unit | `terraform validate` in module dir | ❌ W0 | ⬜ pending |
| 80-02-XX | 02 | 2 | cluster-irsa trust policy compiles + wildcard sub_condition switches StringLike/StringEquals | manual (terraform validate) + HCL review | `cd infra/modules/cluster-irsa/v1.0.0 && terraform validate` | N/A | ⬜ pending |
| 80-03-XX | 03 | 2 | `generateClusterHCL` produces valid terragrunt.hcl with substituted placeholders | unit | `go test ./internal/app/cmd/ -run TestGenerateClusterHCL -v` | ❌ W0 | ⬜ pending |
| 80-03-XX | 03 | 2 | `km cluster add` validates creds, runs apply via mockRunner, captures output, persists | unit | `go test ./internal/app/cmd/ -run TestClusterAdd -v` | ❌ W0 | ⬜ pending |
| 80-03-XX | 03 | 2 | `km cluster list` reads clusters from config, prints tabwriter table | unit | `go test ./internal/app/cmd/ -run TestClusterList -v` | ❌ W0 | ⬜ pending |
| 80-03-XX | 03 | 2 | `km cluster rm` calls Destroy + removes config entry + removes dir | unit (mockRunner) | `go test ./internal/app/cmd/ -run TestClusterRm -v` | ❌ W0 | ⬜ pending |
| 80-03-XX | 03 | 2 | `persistClustersConfig` writes + reloads clusters slice idempotently | unit | `go test ./internal/app/cmd/ -run TestPersistClusters -v` | ❌ W0 | ⬜ pending |
| 80-04-XX | 04 | 2 | config.Clusters field loads from km-config.yaml on startup | unit | `go test ./internal/app/config/ -run TestClustersField -v` | ❌ W0 | ⬜ pending |
| 80-05-XX | 05 | 3 | Full integration: add → IAM verify → list → rm against klanker-application | manual e2e | `km cluster add --name dev-use1-0-test --oidc-provider-arn arn:... --dry-run=false --aws-profile klanker-application` then verify + rm | N/A | ⬜ pending |
| 80-06-XX | 06 | 3 | CLAUDE.md adds `## Cross-account k8s integrations` section matching Phase 73/79 format | manual review | grep + manual diff against Phase 79 doc | N/A | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/app/cmd/cluster_test.go` — stubs for TestGenerateClusterHCL, TestClusterAdd, TestClusterList, TestClusterRm, TestPersistClusters; reuses `mockRunner` from `init_test.go`
- [ ] `internal/app/config/config_clusters_test.go` — stubs for TestClustersField (load+marshal round trip via temp km-config.yaml)
- [ ] No framework install needed — Go stdlib `testing` already in use

---

## Manual-Only Verifications

| Behavior | Why Manual | Test Instructions |
|----------|------------|-------------------|
| `terragrunt plan` on create-handler shows zero net diff after extract | Requires real AWS profile + state bucket access | `cd infra/live/use1/create-handler && terragrunt plan -detailed-exitcode`; exit 0 OR all moved-block reorgs with byte-identical JSON; no resource replacements |
| IAM role exists with correct trust policy after `km cluster add --dry-run=false` | Real AWS API call; can't be cleanly mocked end-to-end | `aws iam get-role --role-name km-cluster-{name} --profile klanker-application`; inspect `AssumeRolePolicyDocument` for OIDC provider ARN, `sub` condition value |
| IAM role gone after `km cluster rm --dry-run=false` | Real AWS API call | `aws iam get-role --role-name km-cluster-{name} --profile klanker-application` should return `NoSuchEntity` |
| `km cluster list` shows expected entry between add and rm | Depends on local `km-config.yaml` mutation | Inspect `km-config.yaml` after add, observe `clusters:` list entry; rerun list, observe table row; after rm, observe entry gone |
| CLAUDE.md doc structure matches Phase 73/79 pattern | Subjective fit-to-template | Manual review against `## VS Code Remote-SSH (Phase 73)` and `## Presence daemon (Phase 79)` sections |
| Handoff ServiceAccount YAML in success output is paste-ready for kubectl | YAML validity check; not in scope for automated assertion | `km cluster add ... 2>&1 \| grep -A 10 "apiVersion: v1" \| kubectl apply --dry-run=client -f -` (operator-side; OPTIONAL) |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies (planner to refine task IDs)
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (cluster_test.go, config_clusters_test.go)
- [ ] No watch-mode flags
- [ ] Feedback latency < 3s for unit tests, < 30s for terraform validate
- [ ] `nyquist_compliant: true` set in frontmatter (after planner refines task IDs)

**Approval:** pending
