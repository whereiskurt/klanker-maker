# Running `km` inside a Kubernetes pod (cross-account IRSA)

This directory holds the operator-facing artifacts for letting a pod in some
other team's EKS cluster (e.g. Greenhouse) run the `km` CLI against your
klanker AWS account. The pod authenticates as an IAM role in the klanker
account via IRSA — no static AWS keys, no `aws_access_key_id` env vars,
auto-rotating 3600s session tokens issued by the cluster's projected
ServiceAccount.

## Accounts and the OIDC mirror

Two accounts are involved end-to-end:

| Account | Role | Example |
|---|---|---|
| **EKS cluster** account | Hosts the cluster, issues SA tokens, owns the cluster's OIDC issuer URL | Greenhouse, `874364631781` |
| **klanker** account | Hosts the IAM role and a *mirrored* OIDC provider; holds the artifacts/state buckets `km` reads | `850919910932` |

The non-obvious bit: **AWS STS validates `AssumeRoleWithWebIdentity` tokens
against an OIDC provider registered in the same account as the IAM role.**
It cannot reach across to the cluster account's provider. So
`km cluster add` registers a *local* copy of the EKS cluster's OIDC issuer
URL in the klanker account, and the role's trust policy references that
local copy. The cluster's own provider is named only to derive the issuer
URL.

```
EKS cluster (account A)              klanker account (account B)
─────────────────────────            ────────────────────────────
oidc.eks.us-east-1.amazonaws         OIDC provider (mirror, same URL)
   .com/id/ABC123                            │
        │                                    ▼
        │ projected SA token            IAM role kph-cluster-dev-use1-0
        │ signed by issuer ─────────►   Trust policy:
        │                                  Principal.Federated = <local mirror>
        │                                  sub = system:serviceaccount:security:km
        ▼
pod with sa annotation                STS validates token, returns creds
```

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

What this provisions in the klanker account (one Terragrunt stack per
cluster issuer URL):

- `aws_iam_openid_connect_provider` mirroring the cluster's issuer URL +
  TLS thumbprint
- `aws_iam_role` named `<resource_prefix>-cluster-<name>` (e.g.
  `kph-cluster-dev-use1-0`) with a trust policy that accepts
  `sts:AssumeRoleWithWebIdentity` from the *local mirror* — gated on
  `<host>:aud=sts.amazonaws.com` and
  `<host>:sub=system:serviceaccount:<namespace>:<service-account>`
- The 14 shared operator policies via `module.km_operator_policy` (same
  module that backs the create-handler Lambda), scoped to the
  `KM_ARTIFACTS_BUCKET` and the klanker DynamoDB tables

`--namespace` and `--service-account` flags accept wildcards (`*`). One
stack per cluster issuer URL — re-use the wildcard pattern for multi-SA
scenarios rather than running `km cluster add` per ServiceAccount.

## In-cluster setup (k8s side)

Two manifests in this directory; apply them in order on the EKS cluster.

### 1. `serviceaccount.km-irsa.yaml`

Annotates a ServiceAccount with the role ARN from the klanker account.
The `eks.amazonaws.com/role-arn` annotation is what the EKS pod-identity
webhook reads to inject the `web_identity_token_file` + `role_arn` env
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

Apply it:

```bash
kubectl apply -f serviceaccount.km-irsa.yaml
```

The `namespace` and `name` here MUST match the values you passed to
`km cluster add` (or be wildcards if you used `*`).

### 2. `pod.km-list.yaml` — smoke-test pod

A two-container pod that:

1. **Init container (`fetch-km`)** — uses the projected SA token to assume
   the IRSA role and `aws s3 cp` the `km` binary from
   `s3://${KM_ARTIFACTS_BUCKET}/sidecars/km` into an `emptyDir` shared
   with the main container.
2. **Main container (`km`)** — writes a tiny `~/.aws/config` profile
   (`klanker-terraform`) that points at the same role and uses the
   projected SA token, then runs `/km/km list`.

Replace the placeholder env values to match your install:

```yaml
env:
- name: KM_ARTIFACTS_BUCKET
  value: "km-artifacts-kph-abc123ef45"   # KM_ARTIFACTS_BUCKET from your km-config.yaml
- name: KM_ACCOUNT_ID
  value: "850919910932"                   # klanker account id (NOT the EKS account)
```

Apply and watch:

```bash
kubectl apply -f pod.km-list.yaml
kubectl -n security logs km-list-test -c km
```

A successful run prints the `km list` table (empty if no sandboxes), proving
the pod assumed the klanker-account role and called klanker-account
APIs through the IRSA chain.

## Token flow (what happens at request time)

1. EKS injects `/var/run/secrets/eks.amazonaws.com/serviceaccount/token`
   into the pod (signed JWT, audience `sts.amazonaws.com`, issuer = cluster's
   OIDC URL).
2. AWS SDK reads `AWS_WEB_IDENTITY_TOKEN_FILE` + `AWS_ROLE_ARN` (injected by
   the EKS webhook from the SA annotation), or the `~/.aws/config` profile
   used in `pod.km-list.yaml`.
3. SDK calls `sts:AssumeRoleWithWebIdentity` in the klanker account, sending
   the projected token.
4. STS in the klanker account looks up the OIDC provider whose URL matches
   the token's issuer claim — finds the *local mirror* registered by
   `km cluster add`.
5. STS validates the token signature against the mirror's published JWKS
   keys (fetched live from the cluster issuer's `/keys` endpoint), checks
   the `aud` and `sub` claims against the role's trust policy conditions.
6. If all checks pass, STS returns short-lived credentials for the IRSA
   role. The SDK caches them and rotates 5 minutes before expiry.

The cluster account is never asked to assume anything — it only signs the
projected token. The klanker account does all validation and authorization
against its local mirror.

## Teardown

```bash
# From the operator host:
km cluster rm dev-use1-0          # destroys the IAM role, mirror provider, and stack

# Inside the cluster:
kubectl -n security delete pod km-list-test
kubectl -n security delete -f serviceaccount.km-irsa.yaml
```

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `AccessDenied` from `sts.amazonaws.com` mentioning `Not authorized to perform sts:AssumeRoleWithWebIdentity` | OIDC provider mirror missing in klanker account, or the role's trust policy `sub` condition doesn't match `system:serviceaccount:<ns>:<sa>`. Verify with `aws iam list-open-id-connect-providers --profile <klanker>` and `aws iam get-role --role-name <name>`. |
| `InvalidIdentityToken` | The cluster's OIDC issuer URL changed (cluster recreated). Run `km cluster rm` + `km cluster add` to refresh the mirror. |
| `Could not connect to the endpoint URL` from the `fetch-km` init container | Pod isn't reaching S3 — check VPC endpoints / NAT and that `KM_ARTIFACTS_BUCKET` matches the klanker `km-config.yaml` `artifacts_bucket` value. |
| `EntityAlreadyExists: Provider with url X already exists` during `km cluster add` | Second `km cluster add` against the same EKS issuer URL. Use one stack per cluster with wildcard `--namespace=*` instead. |
| Pod gets credentials but `km` calls fail with `AccessDenied` on a specific API | The 14 inline policies attached via `module.km_operator_policy` don't cover that action. Phase 80 attached the same policy set as the create-handler Lambda — extend `infra/modules/km-operator-policy/v1.0.0/` and re-apply for both stacks. |

## See also

- `CLAUDE.md` § `## Cross-account k8s integrations (Phase 80)` — operator
  reference for the `km cluster` command tree
- `infra/modules/cluster-irsa/v1.0.0/` — the Terraform module that
  provisions the role + mirror provider
- `infra/modules/km-operator-policy/v1.0.0/` — the 14 shared IAM policies
  consumed by both the IRSA role and the create-handler Lambda
