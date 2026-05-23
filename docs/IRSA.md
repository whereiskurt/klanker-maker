# IRSA — Running `km` From a Kubernetes Pod

A technical reference for the IAM Roles for Service Accounts integration that
lets pods on any EKS cluster authenticate as a role in the klanker AWS account
and run the `km` CLI against the klanker control plane. Written for engineers
who already know how Kubernetes RBAC, IAM, and OIDC work — this is the
implementation-level walkthrough, not the marketing summary.

The shorter operator-onboarding guide lives in
[`docs/k8s/README.md`](k8s/README.md). The original design rationale is in
[`docs/superpowers/specs/2026-05-11-km-cluster-cross-account-irsa-design.md`](superpowers/specs/2026-05-11-km-cluster-cross-account-irsa-design.md).
This document is the union of both plus everything learned implementing
the cross-account IRSA integration and the in-pod refactor that shipped with `v0.2.598`.

---

## 1. What IRSA actually is, at the protocol level

The k8s cluster issues an OIDC-flavored JWT to every pod that asks for one.
AWS STS, given that JWT, validates its signature against a public OIDC
provider that the cluster's issuer URL points at, then issues short-lived AWS
session credentials for an IAM role whose trust policy names that same OIDC
provider as a `Federated` principal. The pod never holds a long-lived AWS
secret; the only durable credential is the cluster's signing key.

### 1.1 The token

EKS (and every conformant k8s ≥ 1.21) supports projected service account
tokens via the `BoundServiceAccountTokenVolume` feature. When the EKS Pod
Identity Webhook (more on this below) sees a pod whose ServiceAccount carries
the `eks.amazonaws.com/role-arn` annotation, it mutates the pod spec to inject:

- A **projected volume** at
  `/var/run/secrets/eks.amazonaws.com/serviceaccount/token`. The
  `projected.sources[].serviceAccountToken` definition pins:
  - `audience: sts.amazonaws.com`
  - `expirationSeconds: 86400` (default; the kubelet refreshes at 80% of TTL)
  - `path: token`
- The env vars `AWS_ROLE_ARN` (from the SA annotation) and
  `AWS_WEB_IDENTITY_TOKEN_FILE` (pointing at the file above).

The token is a standard RS256-signed JWT:

```
header   { "alg":"RS256", "kid":"<rotating-key-id>", "typ":"JWT" }
payload  { "iss":"https://oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE",
           "aud":["sts.amazonaws.com"],
           "sub":"system:serviceaccount:<namespace>:<service-account>",
           "exp":<unix-ts>,
           "iat":<unix-ts>,
           "kubernetes.io":{"namespace":"...","pod":{"name":"...","uid":"..."},
                            "serviceaccount":{"name":"...","uid":"..."}} }
signature  <RS256 over header.payload using cluster's private key>
```

The `sub` claim is the load-bearing identifier — it's what the trust policy's
`<host>:sub` condition matches against. The `aud` claim is what the trust
policy's `<host>:aud` condition matches against. The cluster's signing keys
are published as a JWKS document at `<issuer-url>/keys` (and the discovery
metadata at `<issuer-url>/.well-known/openid-configuration`); STS fetches that
JWKS over the public internet to verify the signature.

### 1.2 The exchange

The AWS SDK reads `AWS_WEB_IDENTITY_TOKEN_FILE` + `AWS_ROLE_ARN` from the pod
env, reads the token from disk on every refresh, and calls
`sts:AssumeRoleWithWebIdentity` against `sts.amazonaws.com` (or the regional
endpoint, in regional mode). STS:

1. Parses the JWT and extracts the `iss` claim.
2. Looks up an OIDC provider in **its own account** (the account that owns
   the role being assumed) whose URL matches the `iss` claim. **It does not
   reach across accounts** — see §3.1.
3. Fetches `<issuer-url>/keys` to find the public key whose `kid` matches the
   JWT header's `kid`.
4. Verifies the signature, `exp`, `iat`, `aud`, `iss`.
5. Checks the trust policy of the role for matching `Principal.Federated`,
   `Action: sts:AssumeRoleWithWebIdentity`, and any `Condition` clauses on
   `<host>:aud` and `<host>:sub`.
6. Returns temporary credentials (access key, secret key, session token) with
   the role's `max_session_duration` cap (we set 3600s).

The SDK caches the credentials and refreshes ~5 minutes before expiry by
re-reading the projected token from disk (which the kubelet has refreshed
independently) and calling `AssumeRoleWithWebIdentity` again.

### 1.3 The OIDC provider in IAM

An `aws_iam_openid_connect_provider` resource is an account-local handle that
tells AWS "trust JWTs signed by the issuer at this URL." Its key fields:

- **`url`** — must match the JWT's `iss` claim *exactly*. EKS uses
  `https://oidc.eks.<region>.amazonaws.com/id/<cluster-OIDC-id>`.
- **`client_id_list`** — the set of `aud` values the JWT may carry. We pin
  this to `["sts.amazonaws.com"]`.
- **`thumbprint_list`** — historically a SHA1 of the certificate chain
  fronting the issuer's JWKS endpoint, intended as TLS pinning. For
  AWS-managed EKS issuers this is effectively ignored (AWS trusts its own
  CA-fronted endpoint regardless), but the IAM API still requires the field
  to be populated and well-formed. We provide it via `data.tls_certificate`.
  For non-EKS OIDC providers (e.g. self-hosted Kubernetes with a custom
  issuer), the thumbprint *does* gate trust — keep this in mind if you ever
  point this module at a non-EKS cluster.

---

## 2. The big topological choice

The cluster issuing the JWT and the AWS account that owns the IAM role can be
either:

- **Same account** — the cluster lives in the klanker AWS account. The
  cluster's existing OIDC provider (registered by `eksctl` / the EKS
  Terraform module / the console at cluster creation time) is the one STS
  looks up.
- **Cross account** — the cluster lives in some other AWS account
  (developer's corporate account, CI provider, etc.). The cluster's
  provisioning toolchain registered an OIDC provider in *its* account, but
  STS in the klanker account can't see across accounts. We have to register
  a **mirror** OIDC provider in the klanker account whose `url` and
  `client_id_list` match the cluster's, then point the role's trust policy
  at our local mirror.

The two cases reach the same end state — a role with a trust policy that
names an OIDC provider in the klanker account — but the cross-account case
requires creating a duplicate provider object.

### 2.1 Token flow diagram

```
EKS cluster (Account A — may be klanker account or another account)
─────────────────────────────────────────────────────────────────
  ServiceAccount: km
    annotations: eks.amazonaws.com/role-arn = arn:aws:iam::B:role/km-cluster-X
  Pod (in pod-spec.serviceAccountName: km)
    │
    │ kubelet writes projected SA token at
    │ /var/run/secrets/eks.amazonaws.com/serviceaccount/token
    │   aud = sts.amazonaws.com
    │   sub = system:serviceaccount:<ns>:<sa>
    │   iss = https://oidc.eks.us-east-1.amazonaws.com/id/<OIDC-ID>
    │
    │ km process reads token, AWS SDK calls AssumeRoleWithWebIdentity
    ▼
sts.amazonaws.com (Account B — klanker account)
─────────────────────────────────────────────────────────────────
  1. Parse JWT, extract iss
  2. Find IAM OIDC provider in Account B with matching url
     ── same-account case: provider was created by EKS/eksctl in Account B
     ── cross-account case: provider was mirrored by `km cluster add` (Account B)
  3. Fetch <iss>/keys (public internet), verify RS256 signature
  4. Check trust policy of arn:aws:iam::B:role/km-cluster-X
       Principal.Federated == arn:aws:iam::B:oidc-provider/...   ✓
       Condition: <host>:aud == sts.amazonaws.com                ✓
       Condition: <host>:sub == system:serviceaccount:<ns>:<sa>  ✓
  5. Issue temp credentials (3600s, klanker account)
    │
    ▼
Pod now has AWS credentials for IRSA role; calls klanker-account APIs
```

### 2.2 Why the provider has to live in Account B

This trips up everyone who first encounters cross-account IRSA. The reason is
simply that `sts:AssumeRoleWithWebIdentity` does its trust policy evaluation
in the *role's* account. The role's `Principal.Federated` ARN can only refer
to an `aws_iam_openid_connect_provider` in the same account as the role; it
cannot point at one in Account A. So the mirror is mandatory.

The cluster's own provider ARN (in Account A) is named on the
`km cluster add --oidc-provider-arn` CLI only to derive the issuer URL — the
account-ID portion of that ARN is informational. STS never reads it.

---

## 3. The `cluster-irsa` Terraform module

Source: [`infra/modules/cluster-irsa/v1.0.0/`](../infra/modules/cluster-irsa/v1.0.0/).

A per-cluster Terragrunt stack instantiates this module; `km cluster add`
generates the stack's `terragrunt.hcl` from a template in
`internal/app/cmd/cluster.go`. One stack = one IRSA role. One
ServiceAccount-namespace pair per stack (or wildcards across namespaces /
service accounts).

### 3.1 What it creates

| Resource | Always? | Notes |
|---|---|---|
| `aws_iam_openid_connect_provider.this[0]` | Only when `register_oidc_provider = true` | The mirror. URL + client_id_list match the cluster's. Tagged `km:cluster`, `km:manager=km-cluster`. |
| `data.aws_iam_openid_connect_provider.existing[0]` | Only when `register_oidc_provider = false` | Read-only lookup of the provider EKS/eksctl already registered (same-account case, or a second stack against the same cluster). |
| `aws_iam_role.cluster_irsa` | Always | Named `<resource_prefix>-cluster-<cluster_name>`. `max_session_duration = 3600s`. Trust policy below. |
| `module.km_operator_policy` | Always | Attaches the shared inline-policy set (§6). |

### 3.2 The trust policy

```hcl
data "aws_iam_policy_document" "trust" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [local.oidc_provider_arn_local]    # mirror or existing
    }
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_provider_host}:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = local.sub_condition                   # StringEquals or StringLike
      variable = "${local.oidc_provider_host}:sub"
      values   = ["system:serviceaccount:${var.namespace}:${var.service_account_name}"]
    }
  }
}
```

The `<host>` in the condition keys is the OIDC issuer's hostname (the URL
minus the `https://` prefix) — e.g.
`oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE`. AWS strips the URL down to
this form when populating the condition context at `AssumeRoleWithWebIdentity`
time.

`local.sub_condition` flips between `StringEquals` (exact match) and
`StringLike` (wildcard) automatically — if either `namespace` or
`service_account_name` contains a `*`, the condition uses `StringLike` and
treats the literal `*` as a wildcard glob. Operators who want any
ServiceAccount across any namespace pass `--namespace=*` and accept
`StringLike` semantics.

### 3.3 The auto-detect dual-mode

Before generating the `terragrunt.hcl`, `km cluster add` calls
`iam:ListOpenIDConnectProviders` against the target account and walks the
results comparing each provider's URL to the issuer URL extracted from
`--oidc-provider-arn`. The decision tree:

| State | `register_oidc_provider` | Outcome |
|---|---|---|
| No matching provider in target account | `true` (default) | Module creates `aws_iam_openid_connect_provider.this[0]` |
| Matching provider exists in target account | `false` | Module reads via `data.aws_iam_openid_connect_provider.existing[0]` and uses that ARN |

Operators see one of:

```
OIDC provider auto-detected: creating
OIDC provider auto-detected: reusing existing arn:aws:iam::...:oidc-provider/...
```

Running `km cluster add` a second time against an EKS cluster whose
mirror was registered by an earlier stack no longer fails with
`EntityAlreadyExists` — auto-detect picks the reuse branch, supporting
multiple cluster-irsa stacks per EKS issuer URL.

Manual override:

```bash
km cluster add ... --register-oidc-provider=true    # force create (fails if exists)
km cluster add ... --register-oidc-provider=false   # force reuse (fails if absent)
```

The `moved {}` block at the top of the module migrates older state
from the unindexed `aws_iam_openid_connect_provider.this` to
`aws_iam_openid_connect_provider.this[0]` on first apply — state-only
operation, no IAM mutation.

### 3.4 `km cluster rm`

When the stack was created via the reuse branch (`register_oidc_provider =
false`), `km cluster rm` destroys only the IAM role. The pre-existing OIDC
provider is owned by the cluster-provisioning toolchain (`eksctl`, the EKS
Terraform module, console) and is intentionally left in place. Removing it
would break every other workload on that cluster that does IRSA.

When the stack was created via the create branch, `km cluster rm` destroys
both the role and the mirror. This is safe because nothing else in the
klanker account uses the mirror (it's a per-cluster object tagged
`km:cluster`).

---

## 4. CLI lifecycle

### 4.1 `km cluster add`

```bash
km cluster add \
  --name <cluster-name> \                # used in the role name, e.g. dev-use1-0
  --oidc-provider-arn <arn> \            # cluster's own provider (from `aws eks describe-cluster`)
  --namespace <ns> \                     # ServiceAccount namespace, wildcards allowed
  --service-account <name> \             # ServiceAccount name, wildcards allowed
  [--aws-profile klanker-application] \  # operator's AWS profile for the klanker account
  [--region us-east-1] \                 # IAM is global but SDK needs a region
  [--dry-run=true|false] \               # default true; pass --dry-run=false to apply
  [--register-oidc-provider=auto|true|false]  # default auto (see §3.3)
```

The command:

1. Validates inputs and loads `km-config.yaml`.
2. Calls `iam:ListOpenIDConnectProviders` to decide
   `register_oidc_provider`.
3. Generates `infra/live/{region-label}/cluster-{name}/terragrunt.hcl` from
   the template in `cluster.go`. This file is `.gitignore`d (per-install
   artifact, like sandbox dirs).
4. Runs `terragrunt apply` (or `plan` if `--dry-run=true`) against
   `infra/modules/cluster-irsa/v1.0.0/`.
5. Captures the `role_arn` output and persists the cluster metadata to
   `km-config.yaml` under the `clusters:` key.
6. Prints a ready-to-paste ServiceAccount manifest.

Idempotency: re-running `km cluster add --name foo ...` returns the existing
role ARN if `foo` is already in `km-config.yaml`. Re-running with the same
`--name` against a different cluster overrides nothing; you'd see
`EntityAlreadyExists` on the role name and would need to `km cluster rm foo`
first.

### 4.2 `km cluster list` / `km cluster rm`

`list` reads from `km-config.yaml` only (no AWS calls). `rm <name>` runs
`terragrunt destroy` against the per-cluster stack, deletes the
`terragrunt.hcl`, and removes the entry from `km-config.yaml`. If the
DynamoDB persist fails after a successful `terragrunt destroy`, the IAM role
is gone but the config row remains — re-running `km cluster rm` is a no-op
(role already gone) but the row gets cleaned up.

---

## 5. Runtime: making `km` work in the pod

Up to and including `v0.2.596`, every `km` call from a pod failed with
`failed to get shared config profile, klanker-terraform` because the CLI
hard-codes that profile name in ~30 call sites and the SDK refuses to fall
through to the default credential chain when a named profile is set but
absent from `~/.aws/config`. The fix landed in `v0.2.598`.

### 5.1 `LoadAWSConfig` managed-identity auto-detect

[`pkg/aws/client.go`](../pkg/aws/client.go) now downgrades the supplied
profile name to `""` when running in a managed-identity environment:

```go
func isManagedIdentityEnv() bool {
    return os.Getenv("KUBERNETES_SERVICE_HOST") != "" ||  // any pod
        os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" ||    // any Lambda
        os.Getenv("AWS_EXECUTION_ENV") != ""              // ECS / App Runner / CodeBuild
}
```

When that returns true, the SDK call uses *only* `WithRegion(...)` — no
`WithSharedConfigProfile(...)`. The default credential chain then fires in
its documented order:

1. Process credentials (`credential_process`)
2. Static env vars (`AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY`)
3. **Web identity from env vars (`AWS_ROLE_ARN` + `AWS_WEB_IDENTITY_TOKEN_FILE`)**
   ← this is what fires in a pod
4. SSO
5. Shared credentials file
6. IMDS

A one-shot info log surfaces the downgrade so it's discoverable when
debugging:

```
managed-identity environment detected; ignoring AWS profile and using default credential chain
```

The CLI sites that hard-code `"klanker-terraform"` therefore work unchanged
in-pod — no per-call-site refactor needed. Two helper variants exist:

| Function | Use when |
|---|---|
| `LoadAWSConfig(ctx, profile)` | us-east-1 (default — covers ~95% of call sites) |
| `LoadAWSConfigInRegion(ctx, profile, region)` | Non-default region (e.g. cross-region doctor checks, hosted-zone management in `us-east-1` even from another primary region) |

Phase v0.2.598 also swept 10 sites that called
`config.LoadDefaultConfig(... WithSharedConfigProfile(...))` directly
(bypassing the helper) to route through `LoadAWSConfig` /
`LoadAWSConfigInRegion`. After that sweep, no call site can opt out of the
managed-identity downgrade by accident.

### 5.2 The EKS Pod Identity Webhook

This is the piece that makes IRSA "just work" in EKS without per-pod
mutation. The webhook (`pod-identity-webhook`) is a mutating admission
controller deployed by AWS in `kube-system` on every EKS cluster. It reads
the `eks.amazonaws.com/role-arn` annotation on the pod's ServiceAccount and
mutates the pod spec at admission time to:

1. Add a projected volume with the SA token (audience `sts.amazonaws.com`).
2. Mount the volume at `/var/run/secrets/eks.amazonaws.com/serviceaccount/`.
3. Inject env vars `AWS_ROLE_ARN` and `AWS_WEB_IDENTITY_TOKEN_FILE` into
   every container in the pod (unless the container opts out via
   `eks.amazonaws.com/skip-containers` annotation).

The webhook can be disabled per-namespace by labelling the namespace
`eks.amazonaws.com/skip-pod-identity-webhook-amazon=true`. If you see pods
that have the SA annotation but *don't* get the env vars injected, the
webhook is the first place to look (`kubectl describe pod` will show the
admission decision; `kubectl -n kube-system logs deploy/pod-identity-webhook`
shows the webhook's own logs).

A subtle gotcha: the webhook reads the SA annotation at *admission* time,
not at runtime. If you annotate a ServiceAccount *after* a pod is already
running with that SA, the running pod does not pick up the env vars — you
have to delete and recreate it. `km cluster add` workflows always create the
SA before the pod, so this only bites when iterating manually.

### 5.3 The in-pod fixtures

[`docs/k8s/km-list.test.yaml`](k8s/km-list.test.yaml) — minimal smoke test,
runs `/km/km list`. Two containers: an init container that `aws s3 cp`s the
`km` binary from `s3://${KM_ARTIFACTS_BUCKET}/sidecars/km`, and the main
container that runs `km list`. No `~/.aws/config` file. No `AWS_PROFILE` env
var. The SA annotation alone makes it work.

[`docs/k8s/pod.km-create.test.yaml`](k8s/pod.km-create.test.yaml) — heavier
smoke test, runs `/km/km create /profiles/learn.v2.yaml --alias learn`. Adds
two ConfigMap mounts (`/config` for `km-config.yaml`, `/profiles` for the
profile YAML). This exercises the Terraform-state-bucket fetch path
(`fetchAndCacheOutputs` reads `KM_RESOURCE_PREFIX` to build the bucket name
and reads the state via S3 GetObject), which is the workload that revealed
every direct-`LoadDefaultConfig` bypass site.

For non-default `resource_prefix` installs, set `KM_RESOURCE_PREFIX`
explicitly on the pod env. `ExportConfigEnvVars` fires inside
`runCreateRemote` and would set it from `KM_CONFIG_PATH`, but some early
code paths read the env var directly, so the explicit value is
belt-and-suspenders.

---

## 6. IAM permissions: the shared `km-operator-policy` module

Source: [`infra/modules/km-operator-policy/v1.0.0/`](../infra/modules/km-operator-policy/v1.0.0/).

This module attaches a set of inline `aws_iam_role_policy` resources to a
target role via `var.role_id`. It's consumed by two callers, who get the
same policy set:

- `infra/modules/create-handler/v1.0.0/` — the create-handler Lambda's
  execution role.
- `infra/modules/cluster-irsa/v1.0.0/` — every per-cluster IRSA role.

This deliberate sharing is what guarantees that anything `km` can do from
the Lambda, it can also do from a pod. The 16 inline policies (as of
`v0.2.598`) are summarised below; consult the module source for the
authoritative list and scoping.

| Policy | Actions | Resource scope |
|---|---|---|
| `s3_artifacts` | `s3:Get/Put/List/Delete*` | `var.artifact_bucket_arn` and `/*` |
| `dynamodb` | State-lock + budget table CRUD + `DescribeTable` | Per-table ARNs |
| `dynamodb_sandboxes` | Sandbox metadata CRUD + Scan/Query | Sandbox table + alias GSI + identities table |
| `dynamodb_schedules` | `km at` schedule records | `{prefix}-schedules` |
| `terraform_state` | State-bucket S3 + bucket-level GetPolicy/Versioning/Encryption/etc | State bucket + `/*` |
| `ec2_provisioning` | RunInstances, Volumes, SGs, Spot, Tags | `*` (EC2 actions don't all support resource-level) |
| `iam_sandbox` | Role + InstanceProfile + PassRole, scoped to `{prefix}-*` | Per-role-name ARN prefix |
| `ecs_provisioning` | Cluster + Task + Service lifecycle | `*` (ECS resource-level varies) |
| `scheduler` | `scheduler:Create/Get/Delete/Update*` + ScheduleGroup mgmt | Default group `/{prefix}-*` + `{prefix}-at/*` + group `{prefix}-at` |
| `ssm` | Parameter Store under `/{prefix}/*` | Per-prefix ARN pattern |
| `ssm_send_command` | `ssm:SendCommand` on `AWS-RunShellScript` + tagged EC2 instances | Document ARN + `*` instance with `KMSandboxID` tag condition |
| `ses_send` | `ses:SendEmail`, `ses:SendRawEmail` | `*` (SES resource ARN format awkward) |
| `lambda_budget` | `lambda:*` on `{prefix}-*` functions only | Per-function ARN prefix |
| `kms` | `kms:*` | `*` (intentional — sandbox identities use customer-managed keys we don't pre-enumerate) |
| `tagging` | `tag:GetResources` | `*` (no resource-level support) |
| `pricing` | `pricing:GetProducts` | `*` (no resource-level support; endpoint is us-east-1 only) |
| `eventbridge` | `events:PutEvents` on default bus | `event-bus/default` only |
| `sqs_slack_inbound` | Per-sandbox SQS FIFO lifecycle + `ListQueues` | `{prefix}-slack-inbound-*.fifo` (ListQueues is account-wide regardless) |
| `oidc_provider` | OIDC provider Create/Get/Delete/Tag/Untag | `*` (List has no resource-level support) |

The IRSA role gets all of these. **This means a pod with the IRSA role can
do everything `km` can do, including destroying sandboxes, billing the
operator's AWS account, and rotating SSM SecureString parameters under
`/{prefix}/*`.** Treat the IRSA role as a high-privilege workload identity
(see §7.3).

---

## 7. Security considerations

This is the section to read carefully if you're integrating IRSA from a
shared / multi-tenant cluster.

### 7.1 The trust boundary is the `sub` condition

The single most important line in the role's trust policy:

```
StringEquals: oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE:sub == system:serviceaccount:security:km
```

If this is exact (`StringEquals` with no `*`), **only** the ServiceAccount
named `km` in the namespace `security` on this specific cluster can assume
this role. The `sub` claim is set by the kubelet from
`system:serviceaccount:<NS>:<NAME>`; the application has no way to forge it
short of compromising the cluster's signing keys.

If you pass `--namespace=*` or `--service-account=*` to `km cluster add`,
the module switches to `StringLike` and treats `*` as a glob. **A wildcard
namespace means every namespace on the cluster gets access** — anyone who
can deploy a pod with the right SA name into any namespace assumes the
role. This is a deliberately documented escape hatch (it's needed for some
shared-cluster patterns), but it's the single biggest privilege-escalation
risk in this system. Audit your wildcards.

Specifically dangerous: `--namespace=* --service-account=km` on a cluster
where any tenant can create ServiceAccounts. Mitigations:

- Use a literal namespace whenever possible (`--namespace=security`).
- If you must wildcard, ensure namespace creation is controlled by cluster
  RBAC (only platform admins can create namespaces).
- Consider a less-obvious SA name than `km` (`--service-account=km-prod` or
  similar) — an attacker who can create a SA called `km` in any tenant
  namespace would assume the role.

### 7.2 The audience condition is the second line of defence

```
StringEquals: oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE:aud == sts.amazonaws.com
```

This restricts the role assumption to JWTs minted with audience
`sts.amazonaws.com`. A pod that asks the kubelet for a token bound to a
different audience (e.g. `vault.example.com`) cannot use that token here.
This is what prevents a vault-bound token from also being valid for STS.

The module pins `client_id_list = ["sts.amazonaws.com"]` on the OIDC
provider itself, which is a *second* check at the provider level — STS
rejects tokens whose `aud` isn't in the provider's `client_id_list` before
the trust policy is even evaluated. Both checks have to agree.

### 7.3 The role is high-privilege; treat it accordingly

Per §6, the IRSA role gets the full `km-operator-policy` set. The most
sensitive grants:

- **`s3:Get/Put/Delete*` on the artifacts bucket** — anyone with the role
  can replace the `sidecars/km` binary that the in-pod init container
  fetches. A malicious actor who could write to the bucket could backdoor
  every subsequent pod. The artifacts bucket should be private, have
  versioning enabled (default in our Terraform), and have
  `BucketKeyEnabled=true` with KMS encryption (already configured).
- **`kms:*` on `*`** — the role can decrypt every customer-managed KMS key
  in the account. We accept this because (a) sandbox per-identity keys are
  the only customer-managed KMS keys in the klanker account, and (b)
  scoping `kms:*` to a wildcard set of resources we'd have to enumerate at
  apply time is operationally painful. **If you ever add KMS keys to the
  klanker account that hold sensitive non-sandbox data, narrow this
  policy first.**
- **`iam:PassRole` on `{prefix}-*`** — the role can pass any IAM role whose
  name starts with the install's resource prefix to `RunInstances` or
  `ECS:RegisterTaskDefinition`. The prefix-bounded scope means the role
  can't pass arbitrary roles, only ones the same install created — but if
  the install also creates roles outside this trust pattern, this needs
  re-evaluation.
- **`ec2:RunInstances` on `*`** — the role can launch EC2 instances. We
  don't constrain by AMI, instance type, or VPC at IAM. Cost controls live
  in the budget enforcer (a per-sandbox Lambda) rather than IAM.
- **`ssm:GetParameter` + `PutParameter` under `/{prefix}/*`** — the role
  can read and rotate Slack tokens, GitHub tokens, and per-sandbox
  identity-signing keys. Anyone with this role can impersonate sandboxes
  for cross-account email signing.

The cluster-irsa role is therefore not an "audit / read-only" identity. If
you need a read-only identity for a different consumer (a monitoring tool,
for instance), provision a separate role with a narrower policy set —
*do not* layer a permissions boundary on top of the cluster-irsa role,
because the shared `km-operator-policy` would shadow any narrower boundary
you attach.

### 7.4 Per-cluster role isolation

`km cluster add` creates one role per cluster. Two clusters, two roles.
The role name (`{prefix}-cluster-{name}`) encodes the cluster identity, and
the role's `sub` condition is bound to that cluster's OIDC issuer. A pod on
cluster A cannot assume cluster B's role because:

- A's token has `iss=A-issuer-url`; B's role's trust policy `Principal.Federated`
  is an OIDC provider whose `url=B-issuer-url`. STS sees the mismatch on
  step 2 of §1.2 and rejects.

This isolation is the main reason to prefer multiple narrow `km cluster
add` invocations over one wildcard role shared across clusters.

### 7.5 Token theft surface

The projected SA token file at
`/var/run/secrets/eks.amazonaws.com/serviceaccount/token` is world-readable
inside the pod (mode `0644` by default on the projected volume — the kubelet
can't set ownership precisely because the projection runs as root). Any
process inside the pod that can `read(2)` the file can use it to assume the
IRSA role from anywhere on the internet for the remainder of the token's
TTL (up to 24h default, our SA annotation pins 3600s).

What this means in practice:

- **A compromised container in the pod is a compromised IRSA role.** The
  SDK in your `km` binary, the SDK in any sidecar, and any process the
  attacker can run as the pod's user all have equal access.
- **The token survives pod exec.** A `kubectl exec` session into the pod
  can `cat` the file and exfiltrate it. The window is 3600s but the
  attacker doesn't need to maintain pod presence to use it.
- **Logs that include the env var get the role ARN, not the token.** The
  token is on disk only, never in env. Don't write the token to logs.

Mitigations:

- Run `km` containers with the smallest practical image surface
  (`alpine` for the test fixtures — for production deployments consider
  distroless).
- Don't bind a `kubectl exec`-enabled RBAC role to operators casually for
  pods that hold an IRSA token.
- For interactive debugging, exec into an unannotated sidecar pod rather
  than the IRSA pod itself.
- Network egress controls (NetworkPolicies, VPC egress allow-lists) can
  partially defang exfiltration but don't help against an attacker who
  exfils via legitimate STS calls.

### 7.6 The shared-module risk

Anything added to `km-operator-policy` lands on *both* the Lambda role and
every IRSA role. This was a deliberate design choice (consistency over
duplication), but it means:

- **Reviewers must consider both surfaces.** A new policy added "for the
  Lambda" automatically grants pods that perm. The IRSA role is
  cross-network (callable from anywhere with the token), so a Lambda-only
  grant doesn't mean Lambda-only blast radius.
- **Permissions boundaries don't help.** A boundary on the IRSA role would
  cap its perms but the boundary lives on the role, not the module. Doable
  but operationally painful; not currently done.
- **The `events:PutEvents` + `pricing:GetProducts` + `tag:GetResources`
  perms** added in `v0.2.598` were all surfaced by IRSA testing of paths
  that the Lambda already exercised silently. Same shape will recur:
  whenever a new path is exercised from a pod for the first time, expect to
  discover one or two more bypass / missing perms.

If you ever want to truly split the surfaces, the move is to extract a
read-only or scoped variant of the module and have `cluster-irsa` consume
that instead. The surface is small enough that operational consistency wins;
revisit if the shared module grows.

### 7.7 The managed-identity auto-detect is signal-based

`isManagedIdentityEnv()` checks three env vars
(`KUBERNETES_SERVICE_HOST`, `AWS_LAMBDA_FUNCTION_NAME`, `AWS_EXECUTION_ENV`).
None of these are set on an operator workstation under normal conditions,
but they are not authenticated — any process inside the pod that sets one
of them in its own env triggers the downgrade.

This is fine in practice because:

- The downgrade only changes *which* credentials the SDK looks for. If
  the SDK is in an environment where the IRSA env vars aren't actually
  set, it'll fall through the chain and ultimately fail with no
  credentials. The downgrade doesn't make credentials appear from nowhere.
- A process that wanted to bypass `klanker-terraform` profile lookup on
  the operator's laptop could already do so by passing `--aws-profile ""`
  on the CLI. This adds another way (set `AWS_LAMBDA_FUNCTION_NAME=foo` in
  the shell) but doesn't grant access that wasn't already grantable.

The risk to watch for is the reverse: an operator running IRSA-emulating
tooling locally (`aws-vault`, `leapp`, custom dev containers that set
`KUBERNETES_SERVICE_HOST` for local k8s testing) would silently get the
downgrade. The one-shot info log surfaces this when it happens; if you see
it on a laptop you don't expect, that's the signal.

### 7.8 TLS thumbprint pinning

For EKS-managed OIDC issuers, the SHA1 thumbprint stored on the
`aws_iam_openid_connect_provider` is essentially decorative — AWS validates
EKS issuer JWKS endpoints via its own internal trust path. We still
populate `thumbprint_list` from `data.tls_certificate` because the IAM API
rejects empty values.

For non-EKS issuers (self-hosted Kubernetes with a custom OIDC issuer
URL), this thumbprint *is* the trust anchor. If you ever point the
`cluster-irsa` module at a non-EKS cluster, ensure the issuer is fronted by
a stable certificate or wire in a thumbprint-rotation runbook. We don't
have one today.

---

## 8. Failure modes & troubleshooting

| Symptom | Likely cause | First check |
|---|---|---|
| `failed to get shared config profile, klanker-terraform` (any `km` command in-pod) | `km` binary predates the managed-identity auto-detect (v0.2.598). | `kubectl exec ... -- /km/km --version` — confirm ≥ `v0.2.598`. If older, run `make build && km init --sidecars` from the operator host and recreate the pod. |
| `AccessDenied` on `sts:AssumeRoleWithWebIdentity` mentioning the role | One of: (a) OIDC provider for the cluster's issuer URL isn't registered in the klanker account; (b) `client_id_list` doesn't contain `sts.amazonaws.com`; (c) role's `sub` condition doesn't match `system:serviceaccount:<ns>:<sa>`. | `aws iam list-open-id-connect-providers --profile klanker-application`, then `aws iam get-open-id-connect-provider --open-id-connect-provider-arn ...` to check `client_id_list`. Then `aws iam get-role --role-name {prefix}-cluster-{name} --query 'Role.AssumeRolePolicyDocument'` to read the trust policy. |
| `InvalidIdentityToken` | Cluster's OIDC issuer URL changed (cluster was recreated, possibly with a new OIDC config). | `aws eks describe-cluster --name <cluster> --query 'cluster.identity.oidc.issuer'` and compare to the provider's `url`. If they differ, `km cluster rm <name> && km cluster add ...` to register a fresh mirror; for same-account installs, `eksctl utils associate-iam-oidc-provider` on the cluster side. |
| Pod gets credentials but a specific API call returns `AccessDenied` | The shared `km-operator-policy` doesn't cover that action. | Identify the API call from the error (e.g. `tag:GetResources`), grep the module for the action, add a policy resource if missing. Re-apply on both `create-handler` and the `cluster-irsa` stacks. |
| `EntityAlreadyExists` on `km cluster add` | `--register-oidc-provider=true` was forced while a provider for that URL already exists in the target account. | Remove the override to let auto-detect pick `false`, or force reuse with `--register-oidc-provider=false`. |
| `Could not connect to the endpoint URL` from the `fetch-km` init container | Pod can't reach S3. | Check VPC endpoints / NAT routing; confirm `KM_ARTIFACTS_BUCKET` matches `km-config.yaml` `artifacts_bucket`. |
| No `AWS_ROLE_ARN` / `AWS_WEB_IDENTITY_TOKEN_FILE` env vars in the container | EKS Pod Identity Webhook didn't mutate the pod (namespace opt-out, webhook crashed, SA annotated after pod created). | `kubectl describe pod ...` for admission events. `kubectl -n kube-system logs deploy/pod-identity-webhook`. Recreate the pod after annotating the SA. |
| `km list` empty but no error | The role assumed correctly but the account has no sandboxes, OR the IRSA role landed in the wrong AWS account (mismatched cluster-side and klanker-side configs). | `kubectl exec ... -- /km/km --account-id` (if added in a future build) or check the role's account in the trust policy and compare to `cfg.Accounts.Application` in `km-config.yaml`. |

---

## 9. References

- AWS docs — IAM Roles for Service Accounts:
  https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html
- AWS docs — OIDC provider:
  https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_providers_create_oidc.html
- Kubernetes — projected service account tokens:
  https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/#projected-service-account-tokens
- AWS Pod Identity Webhook source:
  https://github.com/aws/amazon-eks-pod-identity-webhook
- Internal: design spec (cross-account IRSA):
  [`docs/superpowers/specs/2026-05-11-km-cluster-cross-account-irsa-design.md`](superpowers/specs/2026-05-11-km-cluster-cross-account-irsa-design.md)
- Internal: operator setup guide:
  [`docs/k8s/README.md`](k8s/README.md)
- Internal: cluster-irsa Terraform module:
  [`infra/modules/cluster-irsa/v1.0.0/`](../infra/modules/cluster-irsa/v1.0.0/)
- Internal: shared operator policy module:
  [`infra/modules/km-operator-policy/v1.0.0/`](../infra/modules/km-operator-policy/v1.0.0/)
- Internal: `LoadAWSConfig` with managed-identity detection:
  [`pkg/aws/client.go`](../pkg/aws/client.go)
