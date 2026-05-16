---
name: cluster
description: Provision cross-account IAM roles in the klanker AWS account that trust k8s clusters in other AWS accounts via IRSA (projected ServiceAccount tokens, auto-rotating 3600s sessions)
---

# Cross-Account k8s Cluster Onboarding (IRSA)

This skill provisions an IAM role in the klanker AWS account that a pod in *another* AWS account's k8s cluster can assume via IRSA (IAM Roles for Service Accounts). Pods authenticate with projected ServiceAccount tokens — no static IAM user keys, auto-rotating 3600s session tokens.

**Audience:** the operator running `km` on their workstation. After the role is provisioned, the k8s operator on the other end applies a ServiceAccount manifest in their cluster.

## Cross-references

- `klanker:init` — `km configure` + AWS profile setup must happen first
- `klanker:user` — operator CLI tour

## How it works

`km cluster add` generates a per-cluster `infra/live/{region-label}/cluster-{name}/terragrunt.hcl`, runs `terragrunt apply` against `infra/modules/cluster-irsa/v1.0.0/`, captures the role ARN output, and persists the cluster metadata to `km-config.yaml`. The trust policy permits `sts:AssumeRoleWithWebIdentity`, scoped to a single namespace + ServiceAccount (wildcards allowed).

The IRSA role is attached to the same shared `km-operator-policy/v1.0.0/` Terraform module that the create-handler Lambda role uses — they cannot drift.

### OIDC provider is account-local

AWS STS validates web-identity tokens against an OIDC provider in the *same* account as the IAM role being assumed — it cannot reach across accounts to the cluster's own provider. The `cluster-irsa` module mirrors the remote cluster's issuer URL into a new `aws_iam_openid_connect_provider` registered in the klanker account, then references that local provider as the trust Principal.

The `--oidc-provider-arn` flag names the *remote* cluster's provider only to derive its issuer URL — the account portion of that ARN is informational.

## CLI

```bash
km cluster add --name <name> --oidc-provider-arn <arn> [flags]   # provision
km cluster list                                                   # show configured roles
km cluster rm <name> [flags]                                      # destroy
```

### Flags

| Flag | Default | Required |
|---|---|---|
| `--name` | (none) | yes |
| `--oidc-provider-arn` | (none) | yes |
| `--namespace` | `*` | no — literal namespace tightens trust scope |
| `--service-account` | `km` | no |
| `--aws-profile` | `klanker-application` | no |
| `--region` | `us-east-1` | no |
| `--verbose` | `false` | no |
| `--dry-run` | `true` | no — `false` to actually apply |
| `--register-oidc-provider` | `auto` | no — `auto` / `true` / `false` |

`--dry-run=true` runs `terragrunt plan` only; `--dry-run=false` runs `terragrunt apply --auto-approve`.

### OIDC provider auto-detect

Before generating the per-cluster terragrunt.hcl, `km cluster add` calls `aws iam list-open-id-connect-providers` against the target account.

| Branch | Trigger |
|---|---|
| Reuse existing | Cluster's issuer URL is already registered (same-account EKS, a second stack against the same cluster, or `eksctl`/Terraform-EKS auto-registered the provider). Module sets `register_oidc_provider = false` and references the existing provider via a data source. |
| Create new | No match. Module creates a fresh `aws_iam_openid_connect_provider`. |

The log line `OIDC provider auto-detected: [creating | reusing existing arn:...]` reports which branch was taken. Override with `--register-oidc-provider=true|false`.

`km cluster rm` only destroys providers that this stack registered — pre-existing providers (the `register=false` path) are left intact.

## Walkthrough

```bash
# 1. Plan (default --dry-run=true)
km cluster add --name dev-use1-0 \
  --oidc-provider-arn arn:aws:iam::874364631781:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE \
  --namespace ai-pods \
  --service-account km

# 2. Review the plan; if it looks right, apply
km cluster add --name dev-use1-0 \
  --oidc-provider-arn arn:aws:iam::874364631781:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE \
  --namespace ai-pods \
  --service-account km \
  --dry-run=false

# 3. Confirm it landed
km cluster list
```

On successful apply, the command prints a ready-to-paste ServiceAccount manifest. Hand it to the k8s operator:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: km
  namespace: ai-pods
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::052251888500:role/km-cluster-dev-use1-0
    eks.amazonaws.com/token-expiration: "3600"
```

`kubectl apply -f sa.yaml` — pods annotated `serviceAccountName: km` will pick up the role automatically.

## km-config.yaml schema

`km cluster add` appends to `clusters:` in `km-config.yaml`:

```yaml
clusters:
  - name: dev-use1-0
    oidc_provider_arn: arn:aws:iam::874364631781:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE
    namespace: ai-pods
    service_account: km
    role_arn: arn:aws:iam::052251888500:role/km-cluster-dev-use1-0
```

Absent `clusters:` key is treated as empty slice — existing installs need no migration.

## Operator notes

- **Idempotency:** `km cluster add --name foo ...` returns the existing role ARN if `foo` already exists in `km-config.yaml` — safe to re-run.
- **Rollback on persist failure:** if `terragrunt apply` succeeds but writing `km-config.yaml` fails, the IAM role is left in place. Run `km cluster rm <name>` (using the role name from terraform state) to clean up.
- **Wildcard trust:** `--namespace=*` makes the role assumable by the named ServiceAccount in **any** namespace. Specify a literal namespace for tighter scoping.
- **No `--sidecars` propagation required:** cluster IRSA ships no sandbox-side or Lambda-side code. Operators only need a fresh `km` binary (`make build`).
- **Shared `km-operator-policy` module:** the create-handler Lambda role and every IRSA role both consume `infra/modules/km-operator-policy/v1.0.0/`. The first apply against an existing install performs an address-only state move (no IAM mutations) thanks to `moved {}` blocks. Subsequent applies see no changes.

## Teardown

```bash
km cluster rm <name>              # default --dry-run=true
km cluster rm <name> --dry-run=false
```

Destroys the IRSA role + (if this stack registered it) the OIDC provider. Removes the `clusters:` entry from `km-config.yaml`. Does NOT touch the remote cluster's ServiceAccount — that's the k8s operator's job.

See the design spec under `docs/superpowers/specs/` for full architectural detail.
