# `km cluster` — Cross-Account IRSA for K8s Integrations

**Status:** Design — ready for implementation.
**Date:** 2026-05-11.
**Phase:** 80.

---

## Problem

klanker-maker's infrastructure lives in a dedicated AWS account (the "klanker
account"). Today the only way to invoke `km` commands is from a developer's
local machine or from the create-handler Lambda — both authenticate via an AWS
CLI profile or Lambda execution role respectively.

A persistent k8s service running on an existing Kubernetes cluster (the
Greenhouse `dev-use1-0` EKS cluster) needs to call `km` commands against the
klanker account. Using a static IAM user key is insecure and operationally
painful. The standard solution is **IRSA (IAM Roles for Service Accounts)**:
the k8s pod authenticates via its projected service account token, exchanges it
for short-lived AWS credentials via STS, and never touches a long-lived secret.

This doc specifies a `km cluster add` command that provisions the cross-account
IRSA trust in the klanker account and produces a role ARN for the service team
to wire into their k8s service account.

---

## Background: How IRSA Works in the Greenhouse Ecosystem

Understanding the Greenhouse pattern is essential for seeing where klanker-maker
diverges.

### Same-account IRSA (the Greenhouse standard)

Every Greenhouse service that needs AWS access follows this flow:

1. An EKS cluster has an **OIDC provider** registered in the same AWS account.
   For `dev-use1-0`, this is an IAM OIDC identity provider in account
   `874364631781` whose URL comes from
   `data.aws_eks_cluster.this.identity[0].oidc[0].issuer` (see
   `infrastructure/terraform/modules/ack/controllers/iam/main.tf:43-47`).

2. A Terraform module
   (`infrastructure/terraform/modules/aws_irsa_role_lotus/main.tf`) creates an
   IAM role in that **same** account. The trust policy sets the role's
   `Principal.Federated` to
   `arn:aws:iam::874364631781:oidc-provider/<oidc-endpoint>` — the account ID
   and the OIDC provider are both Greenhouse's.

3. The role is constrained by the `boundary_lotus_application` permissions
   boundary created by the ACK IAM controller
   (`infrastructure/terraform/modules/ack/controllers/iam/main.tf:141-164`).
   That boundary caps the max permissions to a curated set (S3, DynamoDB, SQS,
   SSM, SES, Events, Kinesis, KMS, Bedrock AgentCore, etc.). ACK enforces this
   boundary on every Lotus app role it creates.

4. The k8s `ServiceAccount` is annotated with
   `eks.amazonaws.com/role-arn: <role-arn>`. EKS injects a projected web
   identity token into the pod at
   `/var/run/secrets/eks.amazonaws.com/serviceaccount/token`. The AWS SDK picks
   it up automatically via the `AWS_WEB_IDENTITY_TOKEN_FILE` and `AWS_ROLE_ARN`
   env vars that EKS also injects.

A real example: `infrastructure/services/product/interseller/dev.use1/dev/main.tf:80-94`
creates an IRSA role for the interseller service with SQS, SSM, and S3
policies. The Terraform runs in Greenhouse's AWS account, so everything stays
in one account.

### Why klanker-maker is different

klanker-maker's AWS resources (DynamoDB, Lambda, EC2, S3, SES, etc.) live in a
**separate AWS account** — the klanker account (`850919910932` per
`km-config.yaml`). The Greenhouse dev cluster lives in account `874364631781`.

This creates a **cross-account IRSA trust**:

- The OIDC identity provider is still registered in Greenhouse's account
  (`874364631781`).
- The IAM role must be created in the **klanker account** (`850919910932`) so
  it can grant access to klanker-account resources.
- The role's trust policy `Principal.Federated` must reference the OIDC
  provider by its ARN in the Greenhouse account:
  `arn:aws:iam::874364631781:oidc-provider/<oidc-endpoint>`.

This is valid IAM — cross-account principals in OIDC trust policies work the
same as cross-account role assumptions. The difference is that the Greenhouse
`aws_irsa_role` module always uses `data.aws_caller_identity.this.account_id`
for the principal ARN (`main.tf:46-50`), which makes it same-account only. The
klanker-maker module must accept the Greenhouse account's OIDC provider ARN as
an explicit input instead.

Additionally, klanker-maker needs far broader permissions than the
`boundary_lotus_application` policy permits. It must create EC2 instances, ECS
clusters, Lambda functions, IAM roles for sandboxes, EventBridge schedules,
Route53 records, etc. The Greenhouse boundary intentionally excludes these.
There is no ACK controller managing klanker-maker's role — it owns and manages
its own IAM roles directly via Terraform.

---

## Design

Three pieces: a new Terraform module, a Terragrunt HCL template generated per
cluster, and a new `km cluster` CLI subcommand.

### 1. Terraform module: `infra/modules/cluster-irsa/`

A new module, versioned at `v1.0.0`, that creates one IAM role per cluster in
the klanker account with:

- A trust policy referencing the caller-supplied OIDC provider ARN (which
  lives in the Greenhouse account).
- The full set of inline IAM policies needed to run any `km` command — same
  surface as the `create-handler` Lambda role
  (`infra/modules/create-handler/v1.0.0/main.tf`), since a `km` process
  running in-cluster needs identical permissions to one running in Lambda.

**File layout:**

```
infra/modules/cluster-irsa/
└── v1.0.0/
    ├── main.tf
    ├── variables.tf
    └── outputs.tf
```

#### `variables.tf`

```hcl
variable "cluster_name" {
  type        = string
  description = "Short identifier for the cluster, used in the IAM role name (e.g. dev-use1-0)."
}

variable "oidc_provider_arn" {
  type        = string
  description = "Full ARN of the OIDC provider in the cluster's AWS account (e.g. arn:aws:iam::874364631781:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/XXXX)."
}

variable "namespace" {
  type        = string
  default     = "*"
  description = "Kubernetes namespace the service account lives in. Defaults to '*' (any namespace), which switches the trust condition to StringLike."
}

variable "service_account_name" {
  type        = string
  description = "Kubernetes service account name that will assume this role."
}

variable "resource_prefix" {
  type        = string
  description = "Resource prefix for scoping IAM resource ARNs (e.g. km)."
}

variable "state_bucket" {
  type        = string
  description = "S3 bucket used for Terraform state (needed by km subprocess)."
}
```

#### `outputs.tf`

```hcl
output "role_arn" {
  value       = aws_iam_role.cluster_irsa.arn
  description = "ARN of the IAM role. Annotate the k8s ServiceAccount with this value."
}

output "role_name" {
  value = aws_iam_role.cluster_irsa.name
}
```

#### `main.tf` — trust policy

The OIDC provider ARN is passed in directly. Extract the bare hostname from it
(strip the `arn:aws:iam::<account>:oidc-provider/` prefix) to use as the
condition variable key, since AWS condition keys use the bare hostname:

```hcl
data "aws_caller_identity" "current" {}

locals {
  # e.g. "oidc.eks.us-east-1.amazonaws.com/id/XXXX"
  oidc_provider_host = replace(var.oidc_provider_arn, "/^arn:aws:iam::[0-9]+:oidc-provider\\//", "")

  # Use StringLike when namespace is a wildcard (mirrors the Greenhouse
  # aws_irsa_role module's wildcard detection — variables.tf:78-83). This
  # matches how aws_irsa_role_lotus sets service_account_namespace = "*" for
  # all Lotus apps, trading tighter scoping for deployment flexibility.
  has_wildcard   = can(regex("\\*", var.namespace)) || can(regex("\\*", var.service_account_name))
  sub_condition  = local.has_wildcard ? "StringLike" : "StringEquals"
}

data "aws_iam_policy_document" "trust" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [var.oidc_provider_arn]
    }

    condition {
      test     = "StringEquals"
      variable = "${local.oidc_provider_host}:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = local.sub_condition
      variable = "${local.oidc_provider_host}:sub"
      values   = ["system:serviceaccount:${var.namespace}:${var.service_account_name}"]
    }
  }
}

resource "aws_iam_role" "cluster_irsa" {
  name               = "${var.resource_prefix}-cluster-${var.cluster_name}"
  assume_role_policy = data.aws_iam_policy_document.trust.json
}
```

#### `main.tf` — inline policies

Attach the same inline policy set as `create-handler` (see
`infra/modules/create-handler/v1.0.0/main.tf:28-528` for the authoritative
source). The policies will be sourced from a shared module
`infra/modules/km-operator-policy/v1.0.0/` (extracted in this phase).

The full list of policies in scope:

| Policy resource name | Covers |
|---|---|
| `s3_artifacts` | Artifact bucket get/put/list/delete |
| `dynamodb` | State lock table + budget table CRUD |
| `dynamodb_sandboxes` | Sandboxes, alias-index, identities tables |
| `terraform_state` | State bucket + bucket-level introspection ops |
| `ec2_provisioning` | RunInstances, spot, volumes, SGs, describe ops |
| `iam_sandbox` | CreateRole, PutRolePolicy, PassRole scoped to `{prefix}-*` |
| `ecs_provisioning` | CreateCluster, RegisterTaskDefinition, CreateService, etc. |
| `scheduler` | EventBridge Scheduler CRUD + PassRole for ttl/budget schedulers |
| `ssm` | GetParameter, PutParameter, DeleteParameter on `/{prefix}/*` |
| `ssm_send_command` | SendCommand to AWS-RunShellScript on KMSandboxID-tagged EC2 |
| `ses_send` | SendEmail, SendRawEmail |
| `lambda_budget` | Lambda `*` on `{prefix}-*` functions |
| `kms` | KMS `*` on all resources |
| `sqs_slack_inbound` | SQS lifecycle on `{prefix}-slack-inbound-*.fifo` |

**Decision:** Extract `km-operator-policy` as a shared module in this phase
(rather than duplicating from create-handler). Both `create-handler` and
`cluster-irsa` consume the shared module. This eliminates the documentation
debt and reduces drift risk between Lambda role and IRSA role.

### 2. Terragrunt HCL template

`km cluster add` generates a `terragrunt.hcl` at
`infra/live/{region}/cluster-{cluster-name}/terragrunt.hcl`. The pattern
mirrors every other module in `infra/live/use1/` (see
`infra/live/use1/create-handler/terragrunt.hcl` as the reference).

Generated content:

```hcl
locals {
  repo_root     = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars     = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  region_label  = local.region_config.locals.region_label
  account_id    = get_aws_account_id()
}

include "root" {
  path = find_in_parent_folders("root.hcl")
}

remote_state {
  backend = "s3"

  generate = {
    path      = "backend.tf"
    if_exists = "overwrite_terragrunt"
  }

  config = {
    bucket         = local.site_vars.locals.backend.bucket
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/cluster-{CLUSTER_NAME}/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/cluster-irsa/v1.0.0"
}

inputs = {
  cluster_name         = "{CLUSTER_NAME}"
  oidc_provider_arn    = "{OIDC_PROVIDER_ARN}"
  namespace            = "{NAMESPACE}"
  service_account_name = "{SERVICE_ACCOUNT_NAME}"
  resource_prefix      = local.site_vars.locals.site.label
  state_bucket         = local.site_vars.locals.backend.bucket
}
```

Placeholders `{CLUSTER_NAME}`, `{OIDC_PROVIDER_ARN}`, `{NAMESPACE}`,
`{SERVICE_ACCOUNT_NAME}` are substituted from the CLI flags at generation time.
`{NAMESPACE}` defaults to `"*"` when `--namespace` is not supplied.

### 3. `km cluster` CLI subcommand

New file: `internal/app/cmd/cluster.go`.

#### Command tree

```
km cluster
├── add     Provision a cross-account IRSA role for a k8s cluster
├── list    List clusters registered in km-config.yaml
└── rm      Destroy the IRSA role for a cluster and remove it from config
```

#### `km cluster add` flags

| Flag | Default | Description |
|---|---|---|
| `--name` | required | Short cluster identifier, e.g. `dev-use1-0`. Used in the IAM role name and directory name. |
| `--oidc-provider-arn` | required | Full ARN of the OIDC provider in the cluster's AWS account, e.g. `arn:aws:iam::874364631781:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/XXXX`. |
| `--namespace` | `*` | Kubernetes namespace the service account lives in. Defaults to wildcard — any namespace on the cluster can assume the role, scoped only by service account name. Pass an exact namespace to tighten the trust policy to `StringEquals`. |
| `--service-account` | `km` | Kubernetes service account name that will assume the role. |
| `--aws-profile` | `klanker-application` | AWS CLI profile to authenticate with. |
| `--region` | `us-east-1` | AWS region for the Terragrunt stack. |
| `--verbose` | `false` | Stream full terragrunt/terraform output. |
| `--dry-run` | `true` | Preview without applying. Pass `--dry-run=false` to execute. |

#### `km cluster add` execution flow

1. **Validate AWS credentials** — call `LoadAWSConfig` + `sts.GetCallerIdentity`
   (same pattern as `cmd/init.go:310-316`). Fail fast if the profile resolves
   to the wrong account or credentials are expired.

2. **Check for duplicate** — read km-config.yaml; if a cluster with the same
   `--name` already exists, print the existing role ARN and exit 0 without
   re-applying. This makes the command idempotent for CI.

3. **Generate Terragrunt HCL** — create the directory
   `infra/live/{region-label}/cluster-{name}/` if it doesn't exist. Write
   `terragrunt.hcl` from the template above. The region label is derived from
   `--region` using `compiler.RegionLabel()` (same function used by `km init`).

4. **Run `terragrunt apply`** — use the existing `terragrunt.Runner` from
   `cmd/init.go` (the same `RunInitWithRunner` path). Pass `--auto-approve`.
   Capture stdout/stderr. If `--verbose`, stream output live; otherwise buffer
   and print only on error (same pattern as other modules in `runInit`).

5. **Capture role ARN** — run `terragrunt output -raw role_arn` in the stack
   directory. This is the same pattern used to capture Lambda ARNs after
   `create-handler` applies.

6. **Persist to km-config.yaml** — append a new entry under a `clusters:` list:

   ```yaml
   clusters:
     - name: dev-use1-0
       oidc_provider_arn: arn:aws:iam::874364631781:oidc-provider/oidc.eks...
       namespace: <namespace>
       service_account: km
       role_arn: arn:aws:iam::850919910932:role/km-cluster-dev-use1-0
   ```

   Use the same `persistKMConfigFields` pattern from `cmd/init.go:1719-1755`.

7. **Print handoff instructions** — output a ready-to-use block for the service
   team:

   ```
   ✓ IRSA role provisioned: arn:aws:iam::850919910932:role/km-cluster-dev-use1-0

   Annotate your Kubernetes ServiceAccount:

       apiVersion: v1
       kind: ServiceAccount
       metadata:
         name: km
         namespace: <namespace>
         annotations:
           eks.amazonaws.com/role-arn: arn:aws:iam::850919910932:role/km-cluster-dev-use1-0
           eks.amazonaws.com/token-expiration: "3600"
   ```

#### `km cluster list`

Reads `clusters:` from km-config.yaml and prints a table:

```
NAME          NAMESPACE   SERVICE ACCOUNT   ROLE ARN
dev-use1-0    <ns>        km                arn:aws:iam::850919910932:role/km-cluster-dev-use1-0
```

#### `km cluster rm`

Flags: `--name` (required), `--aws-profile`, `--region`, `--verbose`.

1. Look up the cluster entry in km-config.yaml by name. Error if not found.
2. Run `terragrunt destroy --auto-approve` in
   `infra/live/{region-label}/cluster-{name}/`.
3. Remove the cluster entry from km-config.yaml.
4. Remove the `infra/live/{region-label}/cluster-{name}/` directory.

### 4. Config schema change

Add a `clusters` field to the config struct in
`internal/app/config/config.go`. This is a slice of structs:

```go
type ClusterConfig struct {
    Name              string `mapstructure:"name"               yaml:"name"`
    OIDCProviderARN   string `mapstructure:"oidc_provider_arn"  yaml:"oidc_provider_arn"`
    Namespace         string `mapstructure:"namespace"          yaml:"namespace"`
    ServiceAccount    string `mapstructure:"service_account"    yaml:"service_account"`
    RoleARN           string `mapstructure:"role_arn"           yaml:"role_arn"`
}

type Config struct {
    // ... existing fields ...
    Clusters []ClusterConfig `mapstructure:"clusters" yaml:"clusters"`
}
```

No default value needed — an absent `clusters` key is treated as an empty
slice.

### 5. Register in root command

In `internal/app/cmd/root.go` (or wherever `NewInitCmd` is registered),
add:

```go
rootCmd.AddCommand(NewClusterCmd(cfg))
```

`NewClusterCmd` returns the `km cluster` parent command with `add`, `list`,
and `rm` as subcommands, following the same `cobra.Command` structure as
`NewInitCmd`.

---

## How the cross-account trust works (step by step)

This is the runtime flow once the role is provisioned:

1. The k8s pod starts on `dev-use1-0`. EKS sees the `ServiceAccount` annotation
   `eks.amazonaws.com/role-arn: arn:aws:iam::850919910932:role/km-cluster-dev-use1-0`
   and injects two env vars into the pod:
   - `AWS_ROLE_ARN=arn:aws:iam::850919910932:role/km-cluster-dev-use1-0`
   - `AWS_WEB_IDENTITY_TOKEN_FILE=/var/run/secrets/eks.amazonaws.com/serviceaccount/token`

2. The AWS SDK in the pod (Go SDK v2, `config.LoadDefaultConfig`) detects these
   env vars and automatically uses the Web Identity Token credential provider.

3. The SDK calls `sts.AssumeRoleWithWebIdentity` in the **klanker account**
   (`850919910932`), presenting the projected OIDC token from the file.

4. STS validates the token against the OIDC provider referenced in the role's
   trust policy (`arn:aws:iam::874364631781:oidc-provider/...`). AWS allows
   cross-account validation — the OIDC provider lives in `874364631781` but the
   role (and therefore the STS call) is in `850919910932`.

5. STS checks the `sub` condition:
   `system:serviceaccount:<namespace>:<service-account>` must match the token's
   subject claim exactly.

6. If all conditions pass, STS returns short-lived credentials scoped to the
   klanker account. The pod's subsequent AWS API calls (DynamoDB, EC2, Lambda,
   etc.) all hit the klanker account directly.

No static keys. No cross-account assume-role chain. The credentials expire
after 3600 seconds and are automatically refreshed by the SDK.

---

## How to find the OIDC provider ARN

The `--oidc-provider-arn` flag takes the full ARN. To find it for
`dev-use1-0`:

```bash
# From any shell with Greenhouse dev account access:
aws eks describe-cluster --name dev-use1-0 --query 'cluster.identity.oidc.issuer' --output text
# Returns: https://oidc.eks.us-east-1.amazonaws.com/id/XXXX

# The OIDC provider ARN in the Greenhouse account is:
# arn:aws:iam::874364631781:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/XXXX
# (strip the "https://" prefix, prepend the account ARN format)
```

Alternatively, the ACK IAM controller Terraform at
`infrastructure/services/infra/ack/dev.use1/global/main.tf:25-38` uses
`data.aws_eks_cluster.this` and `data.aws_iam_openid_connect_provider.this` to
look this up programmatically — the same data sources can be used to verify the
ARN.

---

## File change summary

| Action | Path |
|---|---|
| **Create** | `infra/modules/km-operator-policy/v1.0.0/main.tf` (extracted) |
| **Create** | `infra/modules/km-operator-policy/v1.0.0/variables.tf` |
| **Create** | `infra/modules/km-operator-policy/v1.0.0/outputs.tf` |
| **Modify** | `infra/modules/create-handler/v1.0.0/main.tf` — consume shared policy module |
| **Create** | `infra/modules/cluster-irsa/v1.0.0/main.tf` |
| **Create** | `infra/modules/cluster-irsa/v1.0.0/variables.tf` |
| **Create** | `infra/modules/cluster-irsa/v1.0.0/outputs.tf` |
| **Create** | `internal/app/cmd/cluster.go` |
| **Modify** | `internal/app/config/config.go` — add `ClusterConfig` struct + `Clusters` field |
| **Modify** | Root command registration — add `NewClusterCmd` |
| Generated at runtime | `infra/live/{region-label}/cluster-{name}/terragrunt.hcl` |
| Modified at runtime | `km-config.yaml` — `clusters:` list entries added/removed |

No changes to `km init`, existing sandbox provisioning, or the Greenhouse
infrastructure repo.

---

## What the service team needs

Once `km cluster add` has run, the implementer of the k8s service needs exactly
two things:

1. **The role ARN** printed by `km cluster add` (also readable via `km cluster list`).

2. **A `ServiceAccount` manifest** annotated with that ARN and the token
   expiration:

   ```yaml
   apiVersion: v1
   kind: ServiceAccount
   metadata:
     name: km                        # must match --service-account
     namespace: <namespace>          # must match --namespace
     annotations:
       eks.amazonaws.com/role-arn: <role-arn>
       eks.amazonaws.com/token-expiration: "3600"
   ```

3. The `km` binary (or a Docker image containing it) needs to be present in
   the container. No AWS credentials or profiles are required — the SDK picks
   up `AWS_ROLE_ARN` and `AWS_WEB_IDENTITY_TOKEN_FILE` automatically.

4. The service must set `KM_AWS_PROFILE` to `""` (empty string) or remove it
   from the config, so that `LoadAWSConfig` in `pkg/aws/client.go:23-40` does
   not attempt to load a named profile and instead falls through to the
   environment-variable credential provider.

---

## Locked decisions for Phase 80

Confirmed with operator 2026-05-11:

1. **Single phase 80** — module extract, new cluster-irsa module, CLI, config,
   and local integration test all ship together. Doctor checks deferred to a
   follow-up phase.

2. **Test target before phase close:** Full `km cluster add --dry-run=false`
   against the `klanker-application` profile. Verify role exists in IAM
   (`aws iam get-role`), captured in km-config.yaml under `clusters:`. Then
   `km cluster rm` to destroy + un-persist. No k8s pod required at phase close.

3. **Extract `km-operator-policy` shared module** — do NOT duplicate
   create-handler policies. Refactor create-handler to consume the shared
   module in the same phase. The refactor must produce zero net change to the
   IAM policy JSON (verifiable via `terragrunt plan` showing no resource
   replacements).
