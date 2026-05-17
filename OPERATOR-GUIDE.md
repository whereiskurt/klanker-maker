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
| `KM_RESOURCE_PREFIX` | Prefix for all account-globally-unique AWS resource names. Default: `km`. Set this when running a second km install in the same AWS account. One-time choice at `km init` — changing on a live install is unsupported. | `km` |
| `KM_EMAIL_SUBDOMAIN` | Subdomain for SES email addresses (`{sandboxID}@{subdomain}.{domain}`). Default: `sandboxes`. Changing requires fresh DNS verification + DKIM/MX records in Route53 (up to 72h propagation). | `sandboxes` |
| `KM_ACCOUNTS_ORGANIZATION` | AWS Organizations management account ID. Used by `km bootstrap` SCP deployment. Optional — blank skips SCP. | `111111111111` |
| `KM_ACCOUNTS_DNS_PARENT` | AWS account ID owning the parent Route53 hosted zone for `cfg.Domain`. Used by `km init` DNS delegation. Optional if no domain is configured. | `111111111111` |
| `KM_ACCOUNTS_TERRAFORM` | AWS account ID for the Terraform account | `222222222222` |
| `KM_ACCOUNTS_APPLICATION` | AWS account ID for the application (sandbox) account | `333333333333` |
| `KM_ARTIFACTS_BUCKET` | S3 bucket for Lambda zips, sidecar binaries, artifacts | `my-km-artifacts` |
| `KM_ROUTE53_ZONE_ID` | Route53 hosted zone ID for `sandboxes.<domain>` | `Z1234ABCDEFGH` |
| `KM_OPERATOR_EMAIL` | Operator inbox address (operator-{resource_prefix}@{email_subdomain}.{domain}; derived by `km configure` from Phase 84 onward) | `operator-km@sandboxes.example.com` |
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

- Resource prefix for AWS resource names (default: `km`; one-time choice — see [Multi-instance support](#multi-instance-support))
- Email subdomain for SES addresses (default: `sandboxes`; one-time choice — changing requires fresh SES verification)
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

---

## 8. Multi-instance support

km supports multiple installs in a single AWS account via the `resource_prefix` configuration
knob. Each install uses a distinct prefix for all account-globally-unique resource names
(DynamoDB tables, Lambda functions, IAM roles, EventBridge schedules, SSM parameters).

### Configuration knobs

| km-config.yaml key | Default | Purpose |
|--------------------|---------|---------|
| `resource_prefix` | `km` | Prefix for all AWS resource names. One-time choice at `km init`. |
| `email_subdomain` | `sandboxes` | Subdomain for SES email addresses (`{id}@{subdomain}.{domain}`). One-time choice — changing requires fresh SES verification. |

Sample `km-config.yaml` block:

```yaml
resource_prefix: "km"        # default; change only for a second install in the same account
email_subdomain: "sandboxes" # default; one-time choice — changing requires fresh SES verification
```

### How to configure a second install

1. Create a separate checkout of this repository (or a separate working directory).
2. Edit `km-config.yaml` and set a distinct prefix before running `km init`:

   ```bash
   # km-config.yaml for the second install
   resource_prefix: "km2"
   email_subdomain: "sandboxes2"
   domain: mycompany.ai
   # ... other fields ...
   ```

3. Run the wizard: `km configure` — it will prompt for `resource_prefix` and `email_subdomain` first.
4. Run `km init` — all AWS resources will be created under the new prefix.

### Constraints

**Prefix is a one-time choice.** Changing `resource_prefix` on a live install is unsupported.
DynamoDB tables, EventBridge schedules, IAM roles, Lambda functions, and SSM parameters all carry
the original prefix in their names and ARNs. Migration to a new prefix would require manual
`terraform state mv` and recreation of stateful resources — this is not a supported workflow.

**SES domain caveat.** Changing `email_subdomain` after `km init` requires fresh DNS verification
and DKIM/MX records in Route53 (up to 72 hours propagation). `km doctor` warns if the configured
email domain does not match a verified SES identity.

**SCP is org-scoped.** The `km-sandbox-containment` SCP is deployed at the AWS Organizations
level (not per-account). If two installs share the same AWS Organization, only one can deploy
the SCP. The second install should leave `accounts.organization` blank in `km-config.yaml` to
skip SCP deployment — the existing SCP from the first install continues to enforce sandbox
containment for both.

### Doctor checks

`km doctor` adds two Phase 66 checks for multi-instance operators:

- **Prefix Collision** — warns if `{prefix}-ttl-handler` Lambda already exists. If you have
  already run `km init`, this is expected and informational. If you have NOT run `km init`,
  this indicates another install is using the same prefix; change `resource_prefix` before
  proceeding.
- **Email Domain SES Match** — warns if `cfg.GetEmailDomain()` is not a verified SES identity.
  Run `km init` to verify the domain, or check DNS records if you recently changed `email_subdomain`.

### Phase 82 isolation guarantees

Phase 82 (2026-05-16) completed the resource-prefix isolation work, making the two-install scenario safe:

**What changed:**
- **SES module** — receipt rule set name is now `${resource_prefix}-sandbox-email` (was hard-coded `km-sandbox-email`). A second install with `resource_prefix: km2` gets `km2-sandbox-email` without touching the first install's rule set.
- **email-handler module** — IAM policy ARNs and S3 paths now interpolate `state_prefix`. A second install's Lambda role is scoped to its own prefix.
- **ECS modules** — SSM parameter ARN interpolates `km_label`. ECS task containers reference only their own install's parameters.
- **Resource tag** — every per-install platform resource carries `km:resource-prefix=${prefix}`. `km doctor` untagged-resource checks and cross-install safety filters all key off this tag to prevent false-positive deletions.

**km configure preserve behavior:** Re-running `km configure` preserves the existing `resource_prefix` — it is never overwritten. Use `km configure --reset-prefix` to reset to the `km` default.

**Upgrade procedure for existing installs (one-time):**
```bash
make build                   # rebuild km CLI
km init --sidecars           # refresh management Lambda + sidecar binaries
km init --dry-run=false      # apply Terraform module changes (tag additions only — no recreations)
AWS_DEFAULT_REGION=us-east-1 AWS_PROFILE=<your-profile> \
  km doctor --backfill-tags --dry-run=true    # preview which resources will be tagged
AWS_DEFAULT_REGION=us-east-1 AWS_PROFILE=<your-profile> \
  km doctor --backfill-tags --dry-run=false   # apply tags (idempotent — safe to re-run)
```

**Troubleshooting:** If `km doctor` reports WARN-level untagged-instance results after Phase 82 upgrade, run `km doctor --backfill-tags --dry-run=true` to preview, then `--dry-run=false` to commit. The cross-install safety guard skips resources whose `km:sandbox-id` tag does not match any row in the current install's DynamoDB sandbox table — those resources belong to a different install and are intentionally left alone.

### Phase 84 upgrade — shared SES rule set + per-install rules

Phase 84 (2026-05-16) replaces Phase 82.1's `activate_rule_set` handoff with per-install SES rule namespacing. Each install now adds prefix-named rules to a single account-shared rule set (`sandbox-email-shared`) that `km bootstrap --shared-ses` provisions. The operator inbound address becomes `operator-{resource_prefix}@{email_subdomain}.{domain}`.

**This is a hard upgrade — Phase 82.1's `activate_rule_set` variable and `KM_SES_ACTIVATE_RULESET` path have been removed. No in-place rollback flag exists. Rollback requires checking out a pre-Phase-84 commit.**

#### Prerequisites

- `km` binary built from Phase 84 sources (`make build`)
- AWS credentials with foundation scope (bootstrap account) and regional scope (application account)
- For a second install: the primary operator has already run `km bootstrap --shared-ses` (the shared rule set must exist before any install runs `km init`)

#### Single install or primary install in a multi-install account

```bash
make build
km init --sidecars
km bootstrap --shared-ses --dry-run=true
km bootstrap --shared-ses --dry-run=false
km init --dry-run=true
km init --dry-run=false
km configure
km doctor
```

`km bootstrap --shared-ses` auto-detects whether `sandbox-email-shared` already exists via `SESIdentityLister` — subsequent runs are no-ops.

`km configure` derives and stores `KM_OPERATOR_EMAIL` as `operator-{resource_prefix}@{email_subdomain}.{domain}` (e.g. `operator-km@sandboxes.example.com`). Use `km configure --reset-prefix` to clear the stored email if you are changing `resource_prefix`.

> **Phase 84.1 note:** the second `km bootstrap --shared-ses --dry-run=false` is a true no-op (idempotent). If `km init` reports a timeout error on a module, see § State-digest mismatch recovery below.

#### Second install in an existing account

A second install with a different `resource_prefix` (e.g. `km2`) follows the same sequence but skips re-running `km bootstrap --shared-ses` — the shared rule set is already active. If you do run it, the auto-detect path performs a no-op apply.

```bash
make build
km init --sidecars
km bootstrap --shared-ses --dry-run=true    # will plan no changes if rule set exists
km bootstrap --shared-ses --dry-run=false   # no-op apply, safe to run
km init --dry-run=true
km init --dry-run=false
km configure
km doctor
```

After `km init`, the second install's rules (`km2-operator-inbound`, `km2-sandbox-catchall`) are added to the shared rule set alongside the first install's rules (`km-operator-inbound`, `km-sandbox-catchall`). Both installs remain fully isolated.

`km doctor` will report `⚠ orphan SES rules: km-operator-inbound, km-sandbox-catchall` when run from the second install's context — this is EXPECTED. The first install's rules are healthy but unknown to the second install's `km-config.yaml`. Run `km doctor` from each install's shell context to get a clean report for that install.

#### Rollback

Phase 84 hard-removes Phase 82.1. No in-place rollback flag is available. To revert:

```bash
git checkout <pre-phase-84-commit>
make build
# Re-run km init to restore Phase 82.1 Terraform resources
```

#### Validation

```bash
# Confirm the shared rule set is active
aws ses describe-active-receipt-rule-set --query 'Name'
# Expected: "sandbox-email-shared"

# Confirm this install's rules are present
aws ses describe-receipt-rule-set \
  --rule-set-name sandbox-email-shared \
  --query "Rules[?starts_with(Name, \`${KM_RESOURCE_PREFIX}\`)].Name"
# Expected: ["km-operator-inbound", "km-sandbox-catchall"] (for prefix=km)

# Confirm km doctor is clean for this install
km doctor | grep "SES rules"
# Expected: ✓ SES rules healthy
```

#### State-digest mismatch recovery (Phase 84.1)

If a `km init` or `km bootstrap` run is interrupted mid-flight (Ctrl-C, terminal closure, terminal session loss), terragrunt may leave the S3 state file and the DynamoDB lock-table digest out of sync. Subsequent terragrunt operations will report:

```
Error: Error acquiring the state lock
Error message: state data in S3 does not have the expected content.
```

`km doctor` (Phase 84.1 onward) detects this via the `Terraform state lock digest` check and prints the exact recovery command in the WARN Remediation field. Manual recovery (mirrors what `km doctor` prints):

1. Identify the mismatched LockID from `km doctor` output (format: `<bucket>/<state-key>-md5`).
2. Download the S3 state object and compute MD5:
   ```bash
   aws s3 cp s3://<bucket>/<state-key> /tmp/state.tfstate
   MD5=$(md5sum /tmp/state.tfstate | awk '{print $1}')
   echo "Correct digest: $MD5"
   ```
3. Overwrite the stale Digest in the lock table:
   ```bash
   aws dynamodb update-item \
     --table-name tf-km-locks-use1 \
     --key '{"LockID":{"S":"<bucket>/<state-key>-md5"}}' \
     --update-expression 'SET Digest = :d' \
     --expression-attribute-values "{\":d\":{\"S\":\"$MD5\"}}"
   ```
4. Re-run `terragrunt apply` (or `km init`); the lock acquisition now succeeds.

Phase 84.1's `km init` per-module timeout (default 5–10 min) prevents the indefinite-hang scenario that caused state-digest drift in Phase 84 UAT. If `km init` reports a timeout error referencing a specific module, the wedged terragrunt PID is printed in the heartbeat lines above the error; `kill -9 <pid>` to clean up the orphan before re-running.

#### Phase 84.1 upgrade safety (in-place v1.0.0 → v2.0.0 cutover)

Phase 84.1 closes the upgrade-path gaps surfaced in Phase 84's UAT:

- `km bootstrap --shared-ses` is now idempotent. Re-running on an already-bootstrapped account is a true no-op (no destroy planned, no error).
- Foundation module's auto-detect now respects foundation state ownership before consulting AWS reality. Pre-existing AWS resources owned by the old regional `ses/v1.0.0` module are brought under foundation management via Terraform `import {}` blocks during the first post-upgrade apply.
- Regional `ses/v2.0.0` module ships with `removed { lifecycle { destroy = false } }` blocks for the shared resources, so the v1.0.0 → v2.0.0 source flip does NOT destroy domain identity, DKIM, MX, or verification records.

**In-place upgrade procedure from Phase 82.x (UPDATED, supersedes the Phase 84 procedure for in-place upgrades):**

```bash
make build
km init --sidecars
km bootstrap --shared-ses --dry-run=true
# Verify plan: existing AWS resources should be planned as "imported" (not created).
km bootstrap --shared-ses --dry-run=false
km init --dry-run=true
# Verify plan: shared resources (domain identity, DKIM, MX, verification TXT,
# rule set, active pointer) should show ZERO destroys. Only ADD of the
# two prefix-named rules + S3 bucket policy update.
km init --dry-run=false
km configure
km doctor
```

If the `km init --dry-run=true` step shows ANY destroy for a shared resource (`aws_ses_domain_identity`, `aws_ses_domain_dkim`, `aws_ses_receipt_rule_set`, `aws_ses_active_receipt_rule_set`, `aws_route53_record.dkim/mx/ses_verification`) — STOP and file a bug. Phase 84.1's removed blocks should suppress all of these.

**DKIM import note:** The foundation module's DKIM import blocks may require a two-pass apply because terraform must read the domain identity before resolving DKIM token names. If the first apply fails on a DKIM import, run it again — the identity will be in state by then. Alternatively, import DKIM records manually:

```bash
cd infra/live/use1/ses-shared-rule-set

# Export the env vars terragrunt needs for state-bucket resolution (omit any that are
# already exported in your shell):
export KM_RESOURCE_PREFIX=<your-prefix>     # e.g. km, whereiskurt
export KM_REGION_LABEL=use1
export KM_REGION=us-east-1
export KM_DOMAIN=example.com
export KM_EMAIL_SUBDOMAIN=sandboxes
export KM_ROUTE53_ZONE_ID=Z...
export AWS_PROFILE=klanker-terraform

# Discover the existing DKIM tokens. The AWS CLI emits tab-separated text, and zsh
# (default on macOS) does NOT word-split unquoted variable expansions the way bash
# does — pipe through `tr` to get a clean newline-split array.
TOKENS_ARR=($(aws ses get-identity-dkim-attributes --identities sandboxes.example.com \
  --query 'DkimAttributes."sandboxes.example.com".DkimTokens' --output text | tr '\t' '\n'))
echo "Got ${#TOKENS_ARR[@]} tokens: ${TOKENS_ARR[@]}"

# Import each DKIM CNAME. Use the quoted array expansion to survive both bash and zsh:
i=0
for t in "${TOKENS_ARR[@]}"; do
  terragrunt import "aws_route53_record.dkim[$i]" \
    "${KM_ROUTE53_ZONE_ID}_${t}._domainkey.sandboxes.example.com_CNAME"
  i=$((i+1))
done

# Same pattern if MX + verification TXT also pre-exist (skip if not):
terragrunt import "aws_route53_record.mx[0]" \
  "${KM_ROUTE53_ZONE_ID}_sandboxes.example.com_MX" || echo "(mx already managed or absent)"
terragrunt import "aws_route53_record.ses_verification[0]" \
  "${KM_ROUTE53_ZONE_ID}__amazonses.sandboxes.example.com_TXT" || echo "(verification already managed or absent)"
```

After the imports complete, `cd` back to the repo root and re-run `km bootstrap --shared-ses --dry-run=false`.

See also: `CLAUDE.md` § Phase 84 for the architecture summary and operator address format.

### Phase 84.2 plan-before-apply

Phase 84.2 (2026-05-16) adds `km init --plan` and `km bootstrap --shared-ses --plan` —
real `terragrunt plan` per module with a curated destroy-class safety gate. The gate
trips on any destroy or replace of a resource type from a compiled-in protected list
(initially: `aws_ses_domain_identity`, `aws_ses_domain_dkim`, `aws_ses_active_receipt_rule_set`,
`aws_ses_receipt_rule_set`, `aws_route53_record`, `aws_s3_bucket`, `aws_s3_bucket_policy`,
`aws_dynamodb_table`, `aws_kms_key` — each entry annotated with the incident that motivated it).

**When to use:** Before `km init --dry-run=false` or `km bootstrap --shared-ses` on
an upgrade, run the plan variant first to see what would actually change.

**Example output — clean apply:**

```
km init --plan: us-east-1 (use1)

  Planning network... 0 to add, 0 to change, 0 to destroy ✓
  Planning ses... 2 to add, 1 to change, 0 to destroy ✓
  …
Total across 16 modules: 2 to add, 1 to change, 0 to destroy
Run 'km init --dry-run=false' to apply.
```

**Example output — protected destroy tripped:**

```
✗ km init --plan would destroy 3 protected resources:

  ses-shared-rule-set:
    - aws_ses_domain_identity.sandboxes      [DESTROY]
    - aws_ses_domain_dkim.sandboxes          [DESTROY]
    - aws_route53_record.dkim[0]             [DESTROY]

These resource types are on the protected list because past incidents
caused unrecoverable data loss (see pkg/terragrunt/planreport/protected.go).

To proceed anyway, re-run with --i-accept-destroys (you must understand
why each resource is destroying — terragrunt apply will not ask again).
```

Exit code 1.

**Override:** `--i-accept-destroys` is per-invocation only. It NEVER persists. It does
NOT auto-apply. It only clears the `--plan` exit code from 1 to 0 so a CI pipeline
can proceed past the gate. You still must separately run `--dry-run=false` to actually apply.

**Bootstrap parity:** Same flags on `km bootstrap --shared-ses --plan` for the
foundation module (closes Phase 84 Gaps 2, 3, 6 in the bootstrap path too).

**Adding a protected type:** Edit `pkg/terragrunt/planreport/protected.go` and add
the new resource type with a rationale comment citing the incident. PR review = the
safety mechanism (intentionally NOT operator-configurable per CONTEXT.md Decision 6).

### Phase 84.3 wrapper-level UX

Phase 84.3 (2026-05-17) tightens the operator path from `git clone` to first apply.
Eight wrapper-level closures address ergonomic gaps surfaced during the Phase 84
second-install UAT — none change the runtime; all change what the operator sees
and types.

**Configure-time changes (`km configure`):**

- **HeadBucket-checked `state_bucket`** (closure a): when you type a globally-taken
  name (`tf-state`, `s3`), `km configure` HeadBuckets it, prints
  `<name> is taken`, suggests `<name>-<account_id>`, and offers `[Y / edit / abort]`.
  `Y` accepts the suggestion, `edit` re-prompts freeform, `abort` exits cleanly.
- **Auto-derived `artifacts_bucket`** (closure e): default is
  `${prefix}-artifacts-${account_id}` (e.g. `km-artifacts-052251888500`). Two
  placeholder forms are rejected by `validateArtifactsBucket` at load:
  `<…>` angle-bracket tokens (e.g. `<prefix>-artifacts-12345678`) and the literal
  example sentinel `km-artifacts-12345`. Error includes the recovery command
  `re-run 'km configure'`.
- **`Next steps:` finale** (closure f.7): after writing `km-config.yaml`, the wizard
  prints the canonical bootstrap sequence (`km bootstrap --all --plan` →
  `--dry-run=false` → `km init --plan` → `--dry-run=false`). The same lines are
  embedded as `#` header comments at the top of the generated yaml.
- **Shell-env conflict WARN** (closure h-shell): if `KM_*` env vars in your shell
  conflict with values the wizard is about to write, each conflicting var prints a
  `WARN: KM_<KEY>=<env-value>` line to stderr before validation, even when the
  wizard would otherwise fail on missing required flags.

**Bootstrap-time changes (`km bootstrap`):**

- **`km bootstrap --all`** (closure f.1–f.5): single command chains foundation
  (SCP + KMS + artifacts) then shared SES rule set. Mutually exclusive with
  `--shared-ses` (error: `--all and --shared-ses are mutually exclusive; --all runs
  both subflows in order`). `--all --plan` honors the Phase 84.2 destroy-class gate.
  This is the recommended primary bootstrap command for new installs.
- **Dry-run text says "apply"** (closure b): `km bootstrap --shared-ses --dry-run=true`
  prints `Dry run — would run: terragrunt apply <path>`. Previously said "plan"
  which was misleading because the operator would later `--dry-run=false` to
  actually apply. Dry-run gracefully degrades when AWS auto-detect is unreachable
  (stale SSO token, missing creds): a single-line apply intent prints, exit 0.
- **Status banner WARNs on empty required account IDs** (closure h-banner): if
  `accounts.organization`, `accounts.dns_parent`, or `accounts.application` is
  empty in `km-config.yaml`, the bootstrap banner emits
  `WARN: accounts.<key> is empty in km-config.yaml — required for this command`
  to stderr; the banner shows `(not set)` in place of the value.

**Init-time changes (`km init`):**

- **Per-var drift WARN** (closure c): `ExportTerragruntEnvVars` emits one stderr
  line per env-vs-yaml mismatch:
  `WARN: KM_REGION=us-west-2 (env) overrides km-config.yaml region=us-east-1`.
  Env still wins (no override of `os.Setenv`); the WARN exists so operators see
  the precedence they're getting. **Phase 84.3 partial-pass note:** the drift
  WARN currently fires reliably only for yaml-authoritative keys
  (`accounts.organization`, `accounts.dns_parent`, `accounts.application`) called
  via the `--shared-ses` path. Drift for env-bound keys (`KM_REGION`, `KM_DOMAIN`,
  `KM_ARTIFACTS_BUCKET`, etc.) is masked by viper's env-binding into `cfg` at
  `config.Load()` time — gap-closure planned for 84.3.1.
- **`km init --plan` skips fresh-install dependents** (closure d): when an
  upstream module's `outputs.json` is absent (e.g. `network` has not been
  applied), `km init --plan` prints `[skip] efs — depends on network/outputs.json
  (apply network first)` for each dependent module and exits 0. After
  `terragrunt apply` in `network/`, re-running `km init --plan` plans the
  dependents normally.
- **Hard-fail on missing artifacts bucket** (closure f.6): `km init --dry-run=false`
  HeadBuckets `cfg.artifacts_bucket` and, on 404, exits with an error naming
  both recovery commands: `km bootstrap --all` (recommended) and
  `km bootstrap --dry-run=false`.

**New command — `km env`** (closure g):

`km env` prints `export KEY=value` lines for the 11 `KM_*` env vars that
Terragrunt's `site.hcl` reads via `get_env()`. Use with `eval $(km env)` to
prepare an operator shell for direct terragrunt invocation:

```bash
eval $(km env)
cd infra/live/use1/network/
terragrunt apply
```

`--aws-profile` opt-in adds `export AWS_PROFILE=<current value>` (excluded by
default to keep the export block portable across operator shells). `KM_ACCOUNTS_TERRAFORM`
is intentionally excluded from the block — see "accounts.* yaml-authoritative" below.

Use cases: recovering from a partial bootstrap, manual `terragrunt import`,
debugging cfg-vs-env precedence, running terragrunt from a module directory
without re-invoking `km` for each step.

**Behavior change — `accounts.*` yaml-authoritative** (closure h):

`accounts.organization`, `accounts.dns_parent`, `accounts.application` are
yaml-authoritative: yaml wins unconditionally, env values do NOT override the
in-memory `cfg`. `KM_ACCOUNTS_*` is still exported to terragrunt subprocesses
(operator overrides reach terraform), but `cfg` reads always reflect yaml.

`accounts.terraform` preserves env-precedence (env wins) — operators retain
shell-local override for the cross-account terraform role; this asymmetry is
intentional per CONTEXT.md.

**Cross-references:**
- Phase 84.4 owns module-level hard-coded `km-` prefix fixes — required for
  full multi-install runtime parity. See § Phase 84.4 once it ships.
- The eight closures map 1:1 to acceptance criteria in
  `.planning/phases/84.3-second-install-bootstrap-ux-wrapper-level-fixes-inserted/84.3-CONTEXT.md`.
