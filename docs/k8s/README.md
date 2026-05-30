# Running `km` inside a Kubernetes pod (IRSA)

This directory holds the operator-facing artifacts for letting a pod in
an EKS cluster — either in a different AWS account or in the same one as
your klanker install — run the `km` CLI against the klanker account. The
pod authenticates as an IAM role in the klanker account via IRSA — no
static AWS keys, no `aws_access_key_id` env vars, auto-rotating 3600s
session tokens issued by the cluster's projected ServiceAccount.

## Accounts and the OIDC provider

Two scenarios are supported:

| Scenario | Cluster account | klanker account | OIDC provider |
|---|---|---|---|
| **Cross-account** (typical) | Corporate `example.com`, `123456789012` | `123456789012` | `km cluster add` registers a *local mirror* of the cluster's issuer URL in the klanker account |
| **Same-account** | `123456789012` (same as klanker) | `123456789012` | `km cluster add` *references* the existing provider already registered by `eksctl`/Terraform/Console — does NOT register a duplicate |

The non-obvious bit: **AWS STS validates `AssumeRoleWithWebIdentity` tokens
against an OIDC provider registered in the same account as the IAM role.**
It cannot reach across accounts. So the cluster's issuer URL must always
be discoverable in the klanker account — either as a mirror that
`km cluster add` registered (cross-account), or as a pre-existing provider
that the EKS toolchain registered (same-account). Phase 80.1 auto-detects
which case applies before generating the per-cluster terragrunt config.
The cluster's own provider ARN is named on the CLI only to derive the
issuer URL — the *account portion* of that ARN is informational.

```
EKS cluster (account A)              klanker account (account B)
─────────────────────────            ────────────────────────────
oidc.eks.us-east-1.amazonaws         OIDC provider for same URL
   .com/id/ABC123                       (created by km cluster add — cross-account,
        │                                or already-registered by EKS — same-account)
        │                                       │
        │ projected SA token                    ▼
        │ signed by issuer ─────────►   IAM role km-cluster-dev-use1-0
        │                               Trust policy:
        │                                  Principal.Federated = <provider in B>
        │                                  sub = system:serviceaccount:security:km
        ▼
pod with sa annotation                STS validates token, returns creds
```

When the cluster and klanker accounts coincide, account A == account B and
the provider is the one EKS already registered. The trust evaluation is
identical either way.

## Auto-detect (Phase 80.1)

Before generating the per-cluster terragrunt.hcl, `km cluster add` calls
`aws iam list-open-id-connect-providers` against the target account:

- **No matching provider** → the module sets `register_oidc_provider = true`
  and creates a fresh `aws_iam_openid_connect_provider` mirror in the
  klanker account (the cross-account default).
- **Matching provider exists** → the module sets
  `register_oidc_provider = false` and references the existing provider via
  a Terraform data source (the same-account case, or a second `km cluster add`
  against an EKS cluster whose mirror was registered by an earlier stack).

The trust Principal points at the correct provider ARN either way —
same-account and cross-account reach the same outcome without operator
flags. This also lifts the previous "one cluster-irsa stack per EKS issuer
URL" restriction.

You'll see one of two log lines:

```
OIDC provider auto-detected: creating
OIDC provider auto-detected: reusing existing arn:aws:iam::...:oidc-provider/...
```

### Manual override

```bash
# Force create a new OIDC provider (fails if one already exists for the same URL)
km cluster add --name my-cluster --oidc-provider-arn <arn> --register-oidc-provider=true

# Force reuse an existing provider (skips auto-detect; fails if none exists)
km cluster add --name my-cluster --oidc-provider-arn <arn> --register-oidc-provider=false
```

### `km cluster rm` behavior

When the stack was created via the reuse path (`register_oidc_provider=false`),
`km cluster rm` destroys only the IAM role. The pre-existing OIDC provider is
owned by the EKS provisioning toolchain (eksctl / Terraform EKS module /
Console) and is left in place.

## One-time operator setup (klanker side)

From the host where `km` is installed:

```bash
# 1. Find the EKS cluster's OIDC issuer URL
aws eks describe-cluster --name <cluster> --region <region> \
  --profile <eks-account-profile> \
  --query 'cluster.identity.oidc.issuer' --output text
# => https://oidc.eks.us-east-1.amazonaws.com/id/ABC123

# 2. Compose the OIDC provider ARN. The account ID portion is informational
#    only — km cluster add uses it to derive the URL, not as the trust Principal.
OIDC_ARN="arn:aws:iam::<eks-account>:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/ABC123"

# 3. Provision the IRSA role in the klanker account
km cluster add \
  --name dev-use1-0 \
  --oidc-provider-arn "$OIDC_ARN" \
  --namespace security \
  --service-account km \
  --dry-run=true            # sanity-check plan first
km cluster add ... --dry-run=false

# 4. Verify
km cluster list
```

What this provisions in the klanker account, per Terragrunt stack:

- `aws_iam_openid_connect_provider` mirroring the cluster's issuer URL +
  TLS thumbprint — **only when auto-detect determined the provider didn't
  yet exist** (`register_oidc_provider = true`). When it already exists,
  the stack references it via a data source and does NOT register a copy.
- `aws_iam_role` named `<resource_prefix>-cluster-<name>` (e.g.
  `km-cluster-dev-use1-0`) with a trust policy that accepts
  `sts:AssumeRoleWithWebIdentity` from the provider above (whether
  freshly-mirrored or pre-existing) — gated on
  `<host>:aud=sts.amazonaws.com` and
  `<host>:sub=system:serviceaccount:<namespace>:<service-account>`
- The shared operator policies attached via `module.km_operator_policy`
  (the same module that backs the create-handler Lambda), scoped to the
  `KM_ARTIFACTS_BUCKET` and the klanker DynamoDB tables

`--namespace` and `--service-account` flags accept wildcards (`*`). The
wildcard pattern remains the recommended way to give multiple
ServiceAccounts in a single cluster access to the same role, but multiple
distinct stacks against one cluster also work (auto-detect handles the
shared provider).

## In-cluster setup (k8s side)

Two multi-document manifests in this directory:

| File | Workload | When to use |
|---|---|---|
| `km-list.test.yaml` | `km list` | Quickest smoke test — proves the pod can assume the role and read sandbox metadata |
| `pod.km-create.test.yaml` | `km create` against a learn profile | Heavier path — exercises the Terraform-state-bucket fetch and every site that previously hard-coded an AWS profile name. If this works, the rest of `km` works in-pod. |

Both contain a ServiceAccount and a Pod separated by `---`.

### ServiceAccount

Annotated with the role ARN from the klanker account. The
`eks.amazonaws.com/role-arn` annotation is what the EKS pod-identity webhook
reads to inject the `AWS_WEB_IDENTITY_TOKEN_FILE` + `AWS_ROLE_ARN` env vars
into the pod.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: km
  namespace: security
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::<klanker-account>:role/<resource_prefix>-cluster-<name>
    eks.amazonaws.com/token-expiration: "3600"
```

The `namespace` and `name` here MUST match the values you passed to
`km cluster add` (or be wildcards if you used `*`).

### Pod — `km-list.test.yaml`

Two containers:

1. **Init container (`fetch-km`)** — uses the projected SA token to assume
   the IRSA role and `aws s3 cp` the `km` binary from
   `s3://${KM_ARTIFACTS_BUCKET}/sidecars/km` into an `emptyDir` shared with
   the main container.
2. **Main container (`km`)** — runs `/km/km list` directly. No
   `~/.aws/config` file, no `AWS_PROFILE` env var. `km` detects the
   managed-identity environment via `KUBERNETES_SERVICE_HOST` and falls
   through to the AWS SDK's default credential chain, which picks up the
   projected SA token automatically. The first credential-loading call
   prints a one-shot info log: `managed-identity environment detected;
   ignoring AWS profile and using default credential chain`.

```bash
kubectl apply -f km-list.test.yaml
kubectl -n security logs km-list-test -c km
```

A successful run prints the `km list` table (empty if no sandboxes).

### Pod — `pod.km-create.test.yaml`

Same shape, but the main container runs
`/km/km create /profiles/learn.v2.yaml --alias learn` and mounts two extra
ConfigMaps:

- `km-config` (mounted at `/config`) — operator's `km-config.yaml`
- `km-learn-profile` (mounted at `/profiles`) — the learn profile YAML

For non-default-prefix installs (anything other than `resource_prefix: km`),
set `KM_RESOURCE_PREFIX` explicitly on the pod env. `ExportConfigEnvVars`
fires inside `runCreateRemote` and would set it from `KM_CONFIG_PATH`, but
some early code paths read the env var directly; the explicit value is
belt-and-suspenders.

```bash
kubectl apply -f pod.km-create.test.yaml
kubectl -n security logs km-create-test -c km -f
```

#### Required ConfigMaps

Create these once in the cluster before applying `pod.km-create.test.yaml`:

```bash
kubectl -n security create configmap km-config \
  --from-file=km-config.yaml=./km-config.yaml

kubectl -n security create configmap km-learn-profile \
  --from-file=learn.v2.yaml=./profiles/learn.yaml
```

## Token flow (what happens at request time)

1. EKS injects `/var/run/secrets/eks.amazonaws.com/serviceaccount/token`
   into the pod (signed JWT, audience `sts.amazonaws.com`, issuer = cluster's
   OIDC URL).
2. AWS SDK reads `AWS_WEB_IDENTITY_TOKEN_FILE` + `AWS_ROLE_ARN` (injected by
   the EKS webhook from the SA annotation). `km` strips its hard-coded
   `klanker-terraform` profile name in this environment so the SDK actually
   reaches step 3 instead of failing with `failed to get shared config profile`.
3. SDK calls `sts:AssumeRoleWithWebIdentity` in the klanker account, sending
   the projected token.
4. STS in the klanker account looks up the OIDC provider whose URL matches
   the token's issuer claim — finds either the mirror that `km cluster add`
   created (cross-account) or the provider EKS/Terraform registered
   (same-account).
5. STS validates the token signature against the provider's published JWKS
   keys (fetched live from the cluster issuer's `/keys` endpoint), checks
   the `aud` and `sub` claims against the role's trust policy conditions.
6. If all checks pass, STS returns short-lived credentials for the IRSA
   role. The SDK caches them and rotates 5 minutes before expiry.

The cluster account is never asked to assume anything — it only signs the
projected token. The klanker account does all validation and authorization
against the OIDC provider registered in its own account.

## Teardown

```bash
# From the operator host:
km cluster rm dev-use1-0          # destroys the IAM role + stack
                                  # also destroys the OIDC provider IFF this stack registered it
                                  # (pre-existing providers from EKS/eksctl are left in place)

# Inside the cluster:
kubectl delete -f km-list.test.yaml
kubectl delete -f pod.km-create.test.yaml
kubectl -n security delete configmap km-config km-learn-profile  # if you created them for the create test
```

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `AccessDenied` from `sts.amazonaws.com` mentioning `Not authorized to perform sts:AssumeRoleWithWebIdentity` | (a) OIDC provider for the cluster's issuer URL not registered in the klanker account, (b) `client_id_list` on an existing provider doesn't include `sts.amazonaws.com`, or (c) the role's trust policy `sub` condition doesn't match `system:serviceaccount:<ns>:<sa>`. Verify with `aws iam list-open-id-connect-providers` (and `aws iam get-open-id-connect-provider --open-id-connect-provider-arn ...` to check `client_id_list`) and `aws iam get-role --role-name <name>`. |
| `InvalidIdentityToken` | The cluster's OIDC issuer URL changed (cluster recreated). If the provider was registered by `km cluster add` (cross-account), run `km cluster rm` + `km cluster add` to refresh. If it was registered by EKS/eksctl, refresh the cluster-side provider through that toolchain. |
| `Could not connect to the endpoint URL` from the `fetch-km` init container | Pod isn't reaching S3 — check VPC endpoints / NAT and that `KM_ARTIFACTS_BUCKET` matches the klanker `km-config.yaml` `artifacts_bucket` value. |
| `failed to get shared config profile, klanker-terraform` (any km command) | Pod is running a pre-managed-identity-detection build of `km` (older than the `LoadAWSConfig` env-detect change). Rebuild with `make build && km init --sidecars` and re-create the pod so the init container fetches the fresh binary. |
| `EntityAlreadyExists: Provider with url X already exists` during `km cluster add` | Auto-detect was bypassed (e.g. `--register-oidc-provider=true` while a provider for that URL already exists in the target account). Either remove the override to let auto-detect pick `false`, or force reuse with `--register-oidc-provider=false`. See the "Auto-detect (Phase 80.1)" section above. |
| Pod gets credentials but `km` calls fail with `AccessDenied` on a specific API | The shared operator policies attached via `module.km_operator_policy` don't cover that action. The cluster-irsa role gets the same policy set as the create-handler Lambda — extend `infra/modules/km-operator-policy/v1.0.0/` and re-apply for both stacks. |

## See also

- `CLAUDE.md` § `## Cross-account k8s integrations (Phase 80)` — operator
  reference for the `km cluster` command tree
- `infra/modules/cluster-irsa/v1.0.0/` — the Terraform module that
  provisions the role and (conditionally, via `register_oidc_provider`) the
  OIDC provider mirror
- `infra/modules/km-operator-policy/v1.0.0/` — the shared IAM policy set
  consumed by both the IRSA role and the create-handler Lambda
