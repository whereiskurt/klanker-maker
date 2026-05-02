# Klanker Maker Operator Guide

This guide covers the full first-time setup procedure for deploying Klanker Maker, from
AWS account prerequisites through deploying shared infrastructure and creating the first
sandbox. Follow the sections in order.

---

## 1. Prerequisites

### AWS Accounts

Klanker Maker supports multiple AWS Organizations topologies:

**4-account topology** (recommended for production):

| Account | km-config.yaml field | Role | Purpose |
|---------|----------------------|------|---------|
| Organization | `accounts.organization` | AWS Organizations management account (SCP policy root) | SCP policies that constrain sandbox accounts are applied here. Set blank to skip SCP deployment. |
| DNS Parent | `accounts.dns_parent` | Route53 parent-zone owner | AWS account that owns the parent hosted zone for `cfg.Domain`. Required for DNS delegation during `km init`. |
| Terraform | `accounts.terraform` | Infrastructure execution account | Terraform and Terragrunt run with credentials from this account; state bucket lives here |
| Application | `accounts.application` | Sandbox workload account | EC2 instances and ECS tasks run here; sandboxes are provisioned into this account |

**Single-account topology** (simpler for development):

All four `accounts.*` fields point at the same account. Set `accounts.organization` to blank
(skips SCP) or the same account ID (enables SCP from the same account). DNS parent and
application fields both point at the single account.

The configure wizard detects single-account topology and prints a confirmation.

> **Migration note (Phase 65):** The previous `accounts.management` field has been split into
> `accounts.organization` (SCP target) and `accounts.dns_parent` (Route53 parent zone owner).
> If your `km-config.yaml` still contains `accounts.management`, rename it to `accounts.dns_parent`
> and add `accounts.organization` (leave blank to skip SCP). Run `km doctor` for remediation
> guidance — it will flag legacy `accounts.management` with an error message and instructions.

### AWS SSO (IAM Identity Center)

Configure IAM Identity Center in the organization account (AWS Organizations management account):

1. Enable IAM Identity Center in the AWS Console under Security, Identity, & Compliance.
2. Create at least one permission set with AdministratorAccess (or a scoped policy covering
   EC2, ECS, DynamoDB, SES, Lambda, S3, IAM, Route53, and EventBridge).
3. Assign the permission set to your operator user for each of the three accounts.
4. Note the SSO start URL (format: `https://<subdomain>.awsapps.com/start`).

### Domain and Route53

- Register a domain name (e.g., `example.com`).
- Create a Route53 hosted zone for the sandboxes subdomain: `sandboxes.example.com`.
  This zone can live in the DNS parent account or the application account — the operator
  guide assumes the application account.
- Note the hosted zone ID (format: `Z1234ABCDEFGH`). You will set this as
  `KM_ROUTE53_ZONE_ID`.

If the root domain zone (`example.com`) is in a different account than the
`sandboxes.example.com` zone, you need to add an NS delegation record in the parent zone
pointing to the subdomain zone's name servers. See the DNS delegation pattern in the
project memory index if your setup is split across management and application accounts.

### Required CLI Tools

| Tool | Version | Install |
|------|---------|---------|
| aws-cli | v2 | https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html |
| terragrunt | >= 0.67 | https://terragrunt.gruntwork.io/docs/getting-started/install/ |
| terraform | >= 1.5 | https://developer.hashicorp.com/terraform/downloads |
| Go | >= 1.25 | https://go.dev/dl/ |
| Docker | latest stable | https://docs.docker.com/engine/install/ — required for ECR image push |
| sops | latest stable | https://github.com/getsops/sops/releases — required for secret encryption |
| zip | system package | Typically pre-installed on macOS and Linux |

### Terraform State Backend

Terragrunt automatically creates the S3 state bucket and DynamoDB lock table on the first
`terragrunt apply` if they do not already exist. The bucket name, lock table name, and
region are configured in `infra/live/site.hcl` and derived from the `km-config.yaml`
values set during `km configure`.

No manual creation is required. If you prefer to pre-create the backend resources (e.g.,
for stricter control over bucket policies), you can create them manually before the first
apply:

```bash
# Optional — only if you want to pre-create the backend
aws s3 mb s3://YOUR-TF-STATE-BUCKET --region us-east-1
aws s3api put-bucket-versioning \
  --bucket YOUR-TF-STATE-BUCKET \
  --versioning-configuration Status=Enabled
aws dynamodb create-table \
  --table-name YOUR-TF-LOCK-TABLE \
  --attribute-definitions AttributeName=LockID,AttributeType=S \
  --key-schema AttributeName=LockID,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST \
  --region us-east-1
```

### Environment Variables

The `km` CLI commands (`km bootstrap`, `km create`, etc.) read configuration from
`km-config.yaml` and automatically pass values to Terragrunt. You do **not** need to
export `KM_ACCOUNTS_*` or `KM_DOMAIN` env vars for `km` commands.

Environment variables are only needed when running `terragrunt apply` directly (e.g.,
deploying shared infrastructure in Section 4). Set these in your shell or a per-project
`.env` file:

| Variable | Description | Example |
|----------|-------------|---------|
| `KM_DOMAIN` | Base domain for sandboxes | `example.com` |
| `KM_ACCOUNTS_ORGANIZATION` | AWS Organizations management account ID. Used by `km bootstrap` SCP deployment. Optional — blank skips SCP. | `111111111111` |
| `KM_ACCOUNTS_DNS_PARENT` | AWS account ID owning the parent Route53 hosted zone for `cfg.Domain`. Used by `km init` DNS delegation. Optional if no domain is configured. | `111111111111` |
| `KM_ACCOUNTS_TERRAFORM` | AWS account ID for the Terraform account | `222222222222` |
| `KM_ACCOUNTS_APPLICATION` | AWS account ID for the application (sandbox) account | `333333333333` |
| `KM_ARTIFACTS_BUCKET` | S3 bucket for Lambda zips, sidecar binaries, artifacts | `my-km-artifacts` |
| `KM_ROUTE53_ZONE_ID` | Route53 hosted zone ID for `sandboxes.<domain>` | `Z1234ABCDEFGH` |
| `KM_OPERATOR_EMAIL` | Operator email for sandbox expiry notifications | `ops@example.com` |
| `KM_REGION` | AWS region (default: us-east-1) | `us-east-1` |

Environment variables override values from `km-config.yaml` when both are present.

---

## 2. Initial Configuration

### Run km configure

`km configure` is an interactive wizard that generates the platform configuration file.
Run it once per environment:

```bash
km configure
```

The command prompts for:

- Base domain (e.g., `klankermaker.ai`)
- Organization AWS account ID (AWS Organizations management account; blank to skip SCP)
- DNS parent AWS account ID (account owning the parent Route53 zone for your domain)
- Terraform AWS account ID
- Application AWS account ID
- SSO start URL (e.g., `https://myorg.awsapps.com/start`)
- SSO region (e.g., `us-east-1`)
- Primary region (e.g., `us-east-1`)

The command writes `./km-config.yaml` at the repo root. If all account IDs match, it detects
a single-account topology and prints a confirmation. For multi-account topologies, it prints
DNS delegation instructions.

For non-interactive use (CI/CD or scripted setup), pass values as flags:

```bash
km configure --non-interactive \
  --domain klankermaker.ai \
  --organization-account 111111111111 \
  --dns-parent-account 111111111111 \
  --terraform-account 222222222222 \
  --application-account 333333333333 \
  --sso-start-url https://myorg.awsapps.com/start \
  --sso-region us-east-1 \
  --region us-east-1
```

All flags are required in non-interactive mode.

### Verify AWS Access

Confirm credentials work in each account context before proceeding:

```bash
# Terraform account (runs Terragrunt)
aws sts get-caller-identity

# Application account (sandbox workloads)
aws sts get-caller-identity --profile <application-account-profile>
```

If you use AWS SSO, run `aws sso login` first:

```bash
aws sso login --sso-session <session-name>
```

### Set Environment Variables (for direct Terragrunt use)

If you plan to run `terragrunt apply` directly (Section 4), export these values. You can
derive them from `km-config.yaml`:

```bash
export KM_DOMAIN=example.com
export KM_ACCOUNTS_ORGANIZATION=""              # blank for single-account; set to AWS Org management account ID otherwise
export KM_ACCOUNTS_DNS_PARENT=111111111111      # AWS account ID owning your domain's parent Route53 zone
export KM_ACCOUNTS_TERRAFORM=222222222222
export KM_ACCOUNTS_APPLICATION=333333333333
export KM_ARTIFACTS_BUCKET=my-km-artifacts
export KM_ROUTE53_ZONE_ID=Z1234ABCDEFGH
export KM_OPERATOR_EMAIL=ops@example.com
export KM_REGION=us-east-1
```

### Bootstrap SCP Policy

Before deploying shared infrastructure, bootstrap the SCP sandbox-containment policy.
This step requires credentials that can assume the `km-org-admin` role in the organization
account (the AWS Organizations management account set in `accounts.organization`). If
`accounts.organization` is blank, SCP deployment is skipped — `km bootstrap` exits with
a notice and sandbox containment relies on IAM policies only.

Preview what will be created:

```bash
km bootstrap
```

Deploy:

```bash
km bootstrap --dry-run=false
```

`km bootstrap` reads account IDs and region from `km-config.yaml` and passes them to
Terragrunt automatically — no environment variable exports are needed.

The SCP policy constrains the application account to prevent sandbox workloads from
escaping their containment boundary (security group mutation, network escape, IAM
escalation, etc.). The bootstrap step must complete before creating sandboxes.

**Prerequisite**: The `km-org-admin` IAM role must exist in the organization account
(`accounts.organization`) with an Organizations permissions policy and a trust relationship
allowing your operator credentials to assume it.

---

## 3. Build Artifacts

Run these targets from the repo root before deploying infrastructure. All targets require
`KM_ARTIFACTS_BUCKET` to be set.

### Lambda Binaries

Cross-compile the Go Lambda binaries for `linux/arm64` (required for the Graviton Lambda
runtime used by all `km` Lambda modules):

```bash
make build-lambdas
```

This produces:
- `build/ttl-handler.zip` — deployment package for the TTL handler Lambda
- `build/budget-enforcer.zip` — deployment package for the per-sandbox budget enforcer Lambda

Both zips contain a single file named `bootstrap`, which is required by the
`provided.al2023` Lambda runtime. Do not rename or restructure the zip contents.

### Sidecar Binaries

Cross-compile and upload the sidecar binaries to S3:

```bash
make sidecars
```

This cross-compiles the dns-proxy, http-proxy, and audit-log sidecars for `linux/amd64`
(Fargate and EC2 x86) and uploads them to
`s3://${KM_ARTIFACTS_BUCKET}/sidecars/`.

### ECR Images

Build and push Docker images for the ECS sidecar containers:

```bash
make ecr-push
```

This builds Docker images for the tracing sidecar and other ECS-specific containers and
pushes them to ECR in the application account. Docker must be running and the ECR
repositories must exist (created by Terragrunt on first `km create` for an ECS sandbox).

---

## 4. Deploy Shared Infrastructure

Shared infrastructure is deployed once per environment. It must be in place before the
first `km create` call. Deploy in the order listed below.

**Note:** These steps run `terragrunt apply` directly, so the `KM_*` environment
variables listed in Section 1 must be exported in your shell.

### a. Network

```bash
cd infra/live/use1/network && terragrunt apply
```

Creates the VPC, subnets, and baseline security groups shared by all sandboxes.

### b. DynamoDB Budget Table

```bash
cd infra/live/use1/dynamodb-budget && terragrunt apply
```

Creates the `km-budgets` DynamoDB global table that stores budget limits and spend for all
sandboxes. DynamoDB Streams are enabled for Lambda-triggered budget enforcement.

### c. SES Email Domain

```bash
cd infra/live/use1/ses && terragrunt apply
```

Requires: `KM_ROUTE53_ZONE_ID` must be set.

Configures SES for the `sandboxes.<domain>` subdomain, including DKIM CNAME records, a TXT
domain verification record, and an MX record for inbound mail. Terraform creates the
Route53 records automatically.

After apply, SES domain verification takes up to 72 hours for full DKIM propagation. Check
verification status in the SES console or with:

```bash
aws sesv2 get-email-identity --email-identity sandboxes.${KM_DOMAIN}
```

The `DkimAttributes.Status` field should read `SUCCESS` before sandbox email is usable.

SES inbound email (receipt rules) is only supported in: `us-east-1`, `us-west-2`,
`eu-west-1`. The default region (`us-east-1`) is correct. If you override `KM_REGION` to a
different region, sandbox email receipt will not work.

### d. TTL Handler Lambda

```bash
cd infra/live/use1/ttl-handler && terragrunt apply
```

Requires: `build/ttl-handler.zip` must exist (run `make build-lambdas` first).

Deploys the `km-ttl-handler` Lambda that fires when a sandbox TTL expires, uploads sandbox
artifacts to S3, sends an expiry notification email, and cancels the EventBridge schedule.

### Deployment Order Note

Steps b, c, and d can run in any order relative to each other, but all three must complete
before the first `km create`. The DynamoDB table must exist for budget tracking, the SES
domain must be configured for sandbox email, and the TTL Lambda must exist for TTL-based
auto-destroy to work.

The network (step a) must be deployed first because the other modules depend on VPC and
subnet IDs as data sources.

---

## 5. First Sandbox

### Validate a Profile

Validate a built-in profile before creating a sandbox:

```bash
km validate profiles/open-dev.yaml
```

Output should indicate the profile is valid. If there are validation errors, they describe
which fields are incorrect and why.

### Create the Sandbox

```bash
km create profiles/open-dev.yaml
```

`km create` performs these steps:
1. Validates the profile
2. Generates Terragrunt artifacts in `infra/live/sandboxes/<sandbox-id>/`
3. Runs `terragrunt apply` to provision the sandbox (EC2 spot instance or ECS Fargate task)
4. Injects secrets into the sandbox via SSM Parameter Store
5. Sets the EventBridge TTL schedule (non-fatal if EventBridge is unreachable)
6. Initializes the budget in DynamoDB (non-fatal)
7. Deploys the per-sandbox budget-enforcer Lambda if the profile defines a budget (non-fatal)

The command prints the sandbox ID on completion.

### List and Check Status

```bash
km list
km status <sandbox-id>
```

`km status` shows the sandbox state (running, stopped, destroyed), current budget spend,
and TTL expiry time.

### Clean Up

```bash
km destroy <sandbox-id>
```

Destroys the budget-enforcer Lambda first (if present), then tears down the sandbox
infrastructure. Sandbox artifacts are uploaded to S3 before destroy regardless of the
teardown policy.

---

## 6. Verification Checklist

Walk through these checks after deploying shared infrastructure and creating the first
sandbox:

1. **DynamoDB budget table exists**

   ```bash
   aws dynamodb describe-table --table-name km-budgets
   ```

   Expected: `TableStatus` is `ACTIVE`.

2. **SES domain verification status**

   ```bash
   aws sesv2 get-email-identity --email-identity sandboxes.${KM_DOMAIN}
   ```

   Expected: `VerifiedForSendingStatus` is `true` and `DkimAttributes.Status` is `SUCCESS`.
   Note: DKIM can take up to 72 hours to propagate.

3. **TTL Lambda exists**

   ```bash
   aws lambda get-function --function-name km-ttl-handler
   ```

   Expected: Function state is `Active`.

4. **Sidecar binaries in S3**

   ```bash
   aws s3 ls s3://${KM_ARTIFACTS_BUCKET}/sidecars/
   ```

   Expected: Listing shows dns-proxy, http-proxy, and audit-log binaries.

5. **ECR images present**

   ```bash
   aws ecr list-images --repository-name km-dns-proxy
   ```

   Expected: At least one image with the `latest` tag.

---

## 7. Troubleshooting

### "Cannot assume IAM Role" during km bootstrap

The `km-org-admin` role does not exist in the organization account (`accounts.organization`),
or the trust policy does not allow your current credentials to assume it. Verify:

1. The role exists in the organization account:

   ```bash
   aws iam get-role --role-name km-org-admin --profile <organization-account-profile>
   ```

2. The trust policy includes your operator principal (SSO role or IAM user).

3. You are authenticated to the correct account. Check with:

   ```bash
   aws sts get-caller-identity
   ```

   The output should show the account/role that is trusted by the `km-org-admin` role.

### "exec format error" in Lambda logs

The Lambda binary was compiled for the wrong architecture. Rebuild with:

```bash
make build-lambdas
cd infra/live/use1/ttl-handler && terragrunt apply
```

The Lambda modules require `arm64`. The sidecar build targets use `amd64` — do not
mix them.

### SES "not verified" or DKIM still pending

DKIM propagation takes up to 72 hours. After `terragrunt apply` on the ses config, the
Route53 CNAME records for DKIM are created automatically. Check that the records exist:

```bash
aws route53 list-resource-record-sets --hosted-zone-id ${KM_ROUTE53_ZONE_ID} \
  | grep -i dkim
```

If the records are missing, re-run `cd infra/live/use1/ses && terragrunt apply`.

### Terragrunt "state bucket not found" or "NoSuchBucket"

Terragrunt normally auto-creates the S3 state bucket on first run. If auto-creation fails
(e.g., due to IAM permissions), create the bucket manually in the Terraform account (see
Prerequisites — Terraform State Backend) and retry.

### "KM_ROUTE53_ZONE_ID: invalid hosted zone ID" during ses apply

The environment variable is not set or contains an invalid value. Look up the zone ID:

```bash
aws route53 list-hosted-zones --query \
  "HostedZones[?Name=='sandboxes.${KM_DOMAIN}.'].Id" \
  --output text
```

Strip the `/hostedzone/` prefix from the result and export as `KM_ROUTE53_ZONE_ID`.

### "zip file not found" during ttl-handler apply

The Lambda zip does not exist at `build/ttl-handler.zip`. Run the build step first:

```bash
make build-lambdas
```

Terraform validates the zip path at plan time, so the file must exist locally before
`terragrunt apply`.

### km create fails with "DynamoDB table not found"

The `km-budgets` table has not been deployed yet. Run:

```bash
cd infra/live/use1/dynamodb-budget && terragrunt apply
```

Then retry `km create`.

### Budget enforcer Lambda not appearing after km create

Budget enforcement is only deployed when the profile defines a `budget` block. Check the
profile for a `spec.budget` section. If the budget enforcer apply failed, it is non-fatal
— inspect the `km create` output for budget enforcer warnings and re-deploy manually:

```bash
cd infra/live/sandboxes/<sandbox-id>/budget-enforcer && terragrunt apply
```
