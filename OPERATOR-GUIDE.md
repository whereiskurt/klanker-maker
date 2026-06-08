# Klanker Maker Operator Guide

This guide covers the full first-time setup procedure for deploying Klanker Maker, from
AWS account prerequisites through deploying shared infrastructure and creating the first
sandbox. Follow the sections in order.

> **Profile spec change — Phase 92 (2026-05-31):** `spec.identity:` → `spec.iam:`.
> `sessionPolicy:` removed. Dead top-level `spec.agent:` block removed. The schema
> gained `iam.allowedSecretPaths` (Phase 89 drift fix). Profiles must now declare
> `apiVersion: klankermaker.ai/v1alpha2` (`v1alpha1` is rejected).
> **`spec.cli.notify*` moved under `spec.notification:`** (`notification.events.*`,
> `notification.email.*`, `notification.slack.*` incl. `slack.inbound`,
> `slack.transcript`, `slack.invites`). **`spec.cli.vscodeEnabled` →
> `spec.runtime.vscode.enabled`.** Sandbox-side `KM_NOTIFY_*` / `KM_SLACK_*` env var
> names are UNCHANGED — only the YAML surface moved. The
> `notification.slack.invites.{emails,useConnect}` toggles replace the former
> `cli.notifySlackInviteEmails` / `cli.useSlackConnect` operator runbook fields.
> Validate the full built-in/profile inventory with
> `bash scripts/validate-all-profiles.sh`.

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

The `accounts.organization` field (SCP target) and `accounts.dns_parent` field (Route53 parent
zone owner) are separate. If your `km-config.yaml` still contains a legacy `accounts.management`
field, rename it to `accounts.dns_parent` and add `accounts.organization` (leave blank to skip
SCP). Run `km doctor` for remediation guidance — it will flag legacy `accounts.management` with
an error message and instructions.

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
| terragrunt | >= 0.99 | https://terragrunt.gruntwork.io/docs/getting-started/install/ |
| terraform | >= 1.7 | https://developer.hashicorp.com/terraform/downloads |
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
| `KM_OPERATOR_EMAIL` | Operator inbox address (`operator-{resource_prefix}@{email_subdomain}.{domain}`; derived by `km configure`) | `operator-km@sandboxes.example.com` |
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

After writing `km-config.yaml`, the wizard prints the canonical bootstrap sequence and embeds
it as `#` header comments at the top of the generated yaml:

```
km bootstrap --all --plan
km bootstrap --all --dry-run=false
km init --plan
km init --dry-run=false
```

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

**Configure-time safeguards:**

- **HeadBucket-checked `state_bucket`**: when you type a globally-taken name, `km configure`
  HeadBuckets it, prints `<name> is taken`, suggests `<name>-<account_id>`, and offers
  `[Y / edit / abort]`. `Y` accepts the suggestion, `edit` re-prompts freeform, `abort` exits
  cleanly.
- **Auto-derived `artifacts_bucket`**: default is `${prefix}-artifacts-${account_id}` (e.g.
  `km-artifacts-123456789012`). Angle-bracket placeholder tokens and the literal example sentinel
  `km-artifacts-12345` are rejected at load; the error includes the recovery command
  `re-run 'km configure'`.
- **Shell-env conflict WARN**: if `KM_*` env vars in your shell conflict with values the wizard
  is about to write, each conflicting var prints a `WARN: KM_<KEY>=<env-value>` line to stderr.

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
derive them from `km-config.yaml`, or use `km env` to print the full block:

```bash
eval $(km env)
```

`km env` prints `export KEY=value` lines for the `KM_*` env vars that Terragrunt's `site.hcl`
reads via `get_env()`. The `--aws-profile` flag opt-in adds `export AWS_PROFILE=<current value>`
(excluded by default to keep the export block portable across operator shells).

Use cases: recovering from a partial bootstrap, manual `terragrunt import`, debugging
cfg-vs-env precedence, running terragrunt from a module directory without re-invoking `km`
for each step.

Or export values manually:

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

### km create --prompt — provision + queue prompts

The `--prompt` flag queues one or more Claude prompts for sequential execution immediately
after the sandbox is reachable. The on-box `km-queue.service` systemd unit drains the queue;
each entry runs as a standard `km agent run` session visible to `km agent list`.

**Basic usage:**

```bash
# Queue a single prompt — returns as soon as sandbox is up and queue is written
km create profiles/open-dev.yaml --prompt "clone https://github.com/acme/api.git && run tests"

# Queue multiple prompts (run sequentially: second starts only after first succeeds)
km create profiles/open-dev.yaml \
  --prompt "clone the repo" \
  --prompt "run lint and tests" \
  --prompt "open a PR for any lint fixes"

# Block until all prompts complete; exit code = 0 (all done) or 1 (first failure)
km create profiles/open-dev.yaml --prompt "run the full suite" --wait

# Use direct API (claude auth login credentials) instead of Bedrock
km create profiles/open-dev.yaml --prompt "fix failing tests" --no-bedrock
```

**Prompt syntax:**

| Syntax | Meaning |
|--------|---------|
| `--prompt "text"` | Literal prompt text |
| `--prompt @path/to/file.txt` | Read file verbatim (UTF-8). Missing file fails before any AWS call. |
| `--prompt @@literal` | Literal `@literal` (escape one leading `@`) |

**--wait exit-code semantics:**

- `0` — all prompts completed successfully
- `1` — first failing prompt exited non-zero; remaining are marked `skipped`

**Observability:**

```bash
# See queue entry status (index, status, timestamp, prompt preview)
km agent list <sandbox-id> --queue

# See individual run output (each queued prompt becomes a run)
km agent list <sandbox-id>

# Tail runner log on the box for auth probe / lifecycle events
km shell <sandbox-id>
journalctl -u km-queue -f              # runner lifecycle (auth probe, entry start/finish)
cat /workspace/.km-agent/km-queue.log  # probe-status log (every 5 min when waiting for auth)
```

**Failure model:**

- First non-zero exit: that entry is marked `failed`; all subsequent `pending` entries become `skipped`
- Auth wait is indefinite (no timeout). The runner probes every 5 seconds and logs every 5 minutes.
- On reboot or `km pause` + `km resume`, the runner reconciles: any entry marked `running` is reset
  to `pending` and retried from the start.

**Recovery procedure (abandon a stuck queue):**

```bash
km shell <sandbox-id>

# Option 1: clear the whole queue (all entries abandoned)
sudo rm /workspace/.km-agent/queue/*

# Option 2: manually retry a failed or skipped entry
sudo -u sandbox bash
jq '.status = "pending"' /workspace/.km-agent/queue/003.meta.json > /tmp/m.json
mv /tmp/m.json /workspace/.km-agent/queue/003.meta.json
systemctl start km-queue   # kick the runner
```

**Constraints:**

- EC2 substrate only. `--prompt + --docker` fails immediately with a clear error — Docker sandboxes
  do not have systemd.
- No per-prompt timeout, retry policy, or conditional execution in v1.
- Bedrock auth probe incurs a tiny API call (~$0.000003) every 5 seconds when waiting for auth.
  If a queue is stuck waiting, expect ~$0.05/day in probe cost per sandbox.
- `--no-bedrock` mode: the runner checks for `~/.claude/.credentials.json` instead of probing
  Bedrock. **Important:** `claude auth login --claudeai` on a minimal headless AMI completes the
  OAuth exchange but cannot persist the token without libsecret + gnome-keyring + a session bus.
  See **Claude auth modes for sandboxes** below for the three working options.

**Reference:**

- Full spec: `docs/superpowers/specs/2026-05-19-km-create-prompt-queue-design.md`

### Claude auth modes for sandboxes

A sandbox running a Claude-driven workload (`km agent run`, `km shell`, `km create --prompt`)
needs Claude to authenticate to either Bedrock or the Anthropic API. There are three working
modes; pick based on whether you want sandbox Claude to count against your Claude.ai subscription
or a Bedrock IAM bill, and how much per-sandbox setup you're willing to do.

#### Mode 1 — Bedrock (recommended default)

Sandbox Claude calls Bedrock via the EC2 instance's IAM role. No token persistence needed; AWS
handles auth via the IMDS-provided STS credentials that auto-rotate.

```yaml
spec:
  execution:
    useBedrock: true   # default
```

| Pro | Con |
|---|---|
| Just works — no setup beyond `km create` | Counts against AWS account's Bedrock spend, not your Claude.ai subscription |
| Survives pause/resume cleanly (creds rotate from IMDS) | Requires `bedrock:InvokeModel` permission in the sandbox IAM role profile |
| No interactive auth step needed | Some accounts need to opt model IDs into on-demand throughput first |
| `km create --prompt --wait` works end-to-end out of the box | — |

This is what most profiles use (e.g. `dc34.yaml`, `codex.yaml`).

#### Mode 2 — Direct API via `CLAUDE_CODE_OAUTH_TOKEN` env-var (recommended for Claude.ai subscribers)

Obtain a Claude.ai OAuth token from a *desktop* machine where `claude auth login` works (macOS
keychain stores it natively), then pass it into the sandbox via the profile's env block.
Token survives pause/resume because it's in the profile, not on-box state.

```yaml
spec:
  execution:
    useBedrock: false
    env:
      CLAUDE_CODE_OAUTH_TOKEN: "${KM_CLAUDE_OAUTH_TOKEN}"   # populated from operator env
```

Or pin the literal token directly in the YAML (don't commit secrets to git — keep this profile
gitignored or use a `${env}` reference).

**Where to get the token:** on macOS, run `claude auth login` in Terminal, then read it back from
the keychain:

```bash
security find-generic-password -s "Claude Code-credentials" -w
```

| Pro | Con |
|---|---|
| Uses your Claude.ai subscription (no Bedrock bill) | Tokens may expire — must refresh periodically |
| Token in profile = portable across pause/resume cycles | Token in profile = handle as a secret (don't commit to git) |
| No on-box keyring infrastructure required | macOS-specific extraction; Linux desktops vary |
| Compatible with `km create --prompt --no-bedrock --wait` | — |

#### Mode 3 — `km agent auth --claude` interactive flow (NOT working on default sandbox AMIs)

`km agent auth <sandbox> --claude` opens an SSM session running `claude auth login --claudeai`,
opens the OAuth URL in your local browser, and verifies via `claude auth status`. **This mode
currently fails on the default Amazon Linux 2023 AMI** because `claude` v2.1.x on Linux uses
libsecret for credential persistence, and the AMI doesn't ship libsecret + gnome-keyring + an
unlocked session bus.

You'll see this:

```text
$ km agent auth my-sandbox
...
Login successful.
Error: claude OAuth succeeded but credentials were not persisted: ...
  Most likely cause: claude v2.1.x on Linux persists OAuth tokens via libsecret
  (system keyring). When neither libsecret nor gnome-keyring is installed (common
  on minimal headless AMIs), the token exchange succeeds but the token is held
  in-memory only, then lost when the auth process exits.
```

The error message points at Modes 1 and 2 as workarounds. Switch to one of those.

**Making Mode 3 actually work** would require adding to your profile's `initCommands`:

```yaml
spec:
  execution:
    initCommands:
      - "yum install -y libsecret gnome-keyring dbus-x11"
      # Then, before claude runs, the sandbox user must have:
      #   eval $(dbus-launch --sh-syntax)
      #   gnome-keyring-daemon --unlock --components=secrets <<< ""
      #   export DBUS_SESSION_BUS_ADDRESS GNOME_KEYRING_CONTROL
```

In practice this is brittle — the empty-password unlock is rejected on some distros, the daemon
dies on pause/resume, each SSM session spawns its own bus, and claude version bumps shift the
dbus probe behavior. **Mode 1 or Mode 2 is almost always the right answer.** Mode 3 is
documented here for completeness, not recommendation.

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

### Inbound Slack/GitHub turns stop flowing for one sandbox (FIFO poison-message wedge)

If a sandbox stops responding to **all** inbound Slack or GitHub turns — but other
sandboxes are fine and the poller is running — a *poison message* may be
head-of-line-blocking its FIFO inbound queue. A poison envelope (one whose agent turn
fails every time) blocks every later message in the same FIFO message group; a poller
restart does **not** clear it.

**Phase 99.1** cures this with a shared per-install **dead-letter queue** + a
`RedrivePolicy` (`maxReceiveCount=3`) on each per-sandbox inbound FIFO queue: after 3 failed
receives the poison envelope is auto-evicted to the DLQ and the group unblocks.

Diagnose and remediate:

```bash
# 1. km doctor surfaces stranded poison messages:
km doctor          # look for: "Inbound DLQ depth" → WARN: N poison message(s)

# 2. Inspect the dead-lettered envelope before deciding:
aws sqs receive-message --queue-url \
  https://sqs.<region>.amazonaws.com/<account>/<prefix>-slack-inbound-dlq.fifo
#   (or <prefix>-github-inbound-dlq.fifo for the GitHub bridge)

# 3. After triage, purge (or redrive) the DLQ:
aws sqs purge-queue --queue-url \
  https://sqs.<region>.amazonaws.com/<account>/<prefix>-slack-inbound-dlq.fifo
```

**Deploy (NOT `--sidecars`):** the shared DLQs are a new Terraform module, so they need a
full apply — `make build && make build-lambdas && km init --dry-run=false`. **Existing
sandboxes do not gain the RedrivePolicy retroactively** (no silent backfill) — recreate
with `km destroy <id> --remote --yes && km create <profile>`. See `docs/slack-notifications.md`
§ Phase 99.1 and `docs/github-bridge.md` § Phase 99.1.

### GitHub bridge federated relay — one App, many installs (Phase 100)

To run a **single GitHub App** across multiple `resource_prefix` installs (one bot
identity, one place to manage), use the federated relay. GitHub delivers every
`issue_comment` webhook to one **front-door** install; its bridge relays repos it
does not own (verbatim body + `X-Hub-Signature-256` + `X-GitHub-Event` +
`X-GitHub-Delivery`, adding `X-KM-Relayed: 1`) to the peer bridges in
`github.peer_bridges`, and the install whose `github.repos:` matches the repo
processes the comment and posts the **single** 👀. This mirrors the Slack Phase 95
relay (`slack.peer_bridges`).

```yaml
# km-config.yaml — list EVERY OTHER install's GitHub bridge Function URL
# (km github status → bridge-url). Absent/empty ⇒ federation off (dormant).
github:
  peer_bridges:
    - https://sec000.lambda-url.us-east-1.on.aws/
```

**Deploy (NOT `--sidecars`):** `github.peer_bridges` → `KM_GITHUB_PEER_BRIDGES` is an
env-block change, so it needs a full apply — `make build-lambdas && km init
--dry-run=false` on each affected install. `km init --sidecars` rebuilds the zip but
does **not** update the Lambda env block, so the relay would stay silently off. No
SandboxProfile schema change ⇒ no sandbox recreate; absent `github.peer_bridges` is
byte-identical to Phase 97/98. `km doctor` adds a **`GitHub peer bridges`** check
(malformed URL / self-loop → WARN; empty → SKIP). Each repo must be owned by exactly
one install (documented, not enforced). Full runbook + two-install/one-App UAT:
`docs/github-bridge.md` § Phase 100.

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

`km doctor` includes these multi-instance checks:

- **Prefix Collision** — warns if `{prefix}-ttl-handler` Lambda already exists. If you have
  already run `km init`, this is expected and informational. If you have NOT run `km init`,
  this indicates another install is using the same prefix; change `resource_prefix` before
  proceeding.
- **Email Domain SES Match** — warns if `cfg.GetEmailDomain()` is not a verified SES identity.
  Run `km init` to verify the domain, or check DNS records if you recently changed `email_subdomain`.

### Resource-prefix isolation guarantees

km fully isolates all per-install resources under the configured prefix, making the two-install
scenario safe:

- **SES module** — receipt rule set name is `${resource_prefix}-sandbox-email`. A second install
  with `resource_prefix: km2` gets `km2-sandbox-email` without touching the first install's rule set.
- **email-handler module** — IAM policy ARNs and S3 paths interpolate `state_prefix`. A second
  install's Lambda role is scoped to its own prefix.
- **ECS modules** — SSM parameter ARN interpolates `km_label`. ECS task containers reference only
  their own install's parameters.
- **Resource tag** — every per-install platform resource carries `km:resource-prefix=${prefix}`.
  `km doctor` untagged-resource checks and cross-install safety filters all key off this tag to
  prevent false-positive deletions.

**km configure preserve behavior:** Re-running `km configure` preserves the existing
`resource_prefix` — it is never overwritten. Use `km configure --reset-prefix` to reset to the
`km` default.

To backfill tags on existing resources:

```bash
km doctor --backfill-tags --dry-run=true    # preview which resources will be tagged
km doctor --backfill-tags --dry-run=false   # apply tags (idempotent — safe to re-run)
```

If `km doctor` reports WARN-level untagged-instance results, run the backfill sequence above.
The cross-install safety guard skips resources whose `km:sandbox-id` tag does not match any row
in the current install's DynamoDB sandbox table — those resources belong to a different install
and are intentionally left alone.

### SES per-install rule namespacing

km uses per-install SES rule namespacing so a second `km init` in the same AWS account never
touches the first install's inbound email path.

**Operator address format:** `operator-{resource_prefix}@{email_subdomain}.{domain}`

Example: `operator-km@sandboxes.example.com` for the default install; `operator-km2@sandboxes.example.com`
for a second install with `resource_prefix: km2`.

**Shared rule set:** `sandbox-email-shared` — account-shared, provisioned once per account/region
by `km bootstrap --shared-ses`; idempotent on re-apply.

**Per-install rules:** Each install adds exactly two rules to the shared rule set:
- `{prefix}-operator-inbound` — routes `operator-{prefix}@` to the operator Lambda
- `{prefix}-sandbox-catchall` — routes all other `{sandbox-id}@` addresses to sandbox mailboxes

`km uninit` removes only this install's two rules and leaves the shared rule set and sibling
installs' rules intact.

**Bootstrap:** `km bootstrap --shared-ses` provisions the shared rule set (idempotent auto-detect —
subsequent runs are no-ops). Must run once before `km init` on a fresh account. Use
`km bootstrap --all` to chain foundation (SCP/KMS/artifacts) and shared SES rule set in one command.

**Doctor check:** `km doctor` reports `✓ SES rules healthy` when all rules in the shared rule set
map to a known `resource_prefix`, or `⚠ orphan SES rules: <list>` when rules exist for prefixes
not in the local `km-config.yaml`. The orphan check is WARN-level — expected when a sibling
install is present.

**Silencing known siblings:** the three cross-install checks (`Orphan SCPs`, `SES rules`,
`Shared secrets KMS key`) WARN on resources belonging to *other* installs in the same account.
When you knowingly run siblings, declare them so those checks report OK (with a note) instead:

```yaml
# km-config.yaml — install-level, applies every run
doctor_ignore_prefixes:
  - km2
  - rg
```

```bash
# or ad-hoc, augmenting the config list
km doctor --ignore-prefix=km2,rg
```

A still-unknown prefix (one you did not declare) continues to WARN, so genuine leftovers from a
botched `km uninit` are not masked. The flag/key only affects these cross-install checks — the
per-sandbox stale-resource scans already filter to your own `{resource_prefix}-*`.

**Validation:**

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

When `km doctor` is run from a second install's context, it will report
`⚠ orphan SES rules: km-operator-inbound, km-sandbox-catchall` for the first install's rules —
this is EXPECTED. Run `km doctor` from each install's shell context to get a clean report for
that install.

### Plan-before-apply

`km init --plan` and `km bootstrap --shared-ses --plan` run real `terragrunt plan` per module
with a curated destroy-class safety gate. The gate trips on any destroy or replace of a resource
type from a compiled-in protected list (initially: `aws_ses_domain_identity`,
`aws_ses_domain_dkim`, `aws_ses_active_receipt_rule_set`, `aws_ses_receipt_rule_set`,
`aws_route53_record`, `aws_s3_bucket`, `aws_s3_bucket_policy`, `aws_dynamodb_table`,
`aws_kms_key` — each entry annotated with the incident that motivated it).

**When to use:** Before `km init --dry-run=false` or `km bootstrap --shared-ses` on
any apply, run the plan variant first to see what would actually change.

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
foundation module.

**Adding a protected type:** Edit `pkg/terragrunt/planreport/protected.go` and add
the new resource type with a rationale comment citing the incident. PR review = the
safety mechanism (intentionally NOT operator-configurable per CONTEXT.md Decision 6).

**Fresh-install note:** `km init --plan` skips modules whose upstream `outputs.json` is absent
(e.g. `network` has not been applied yet), printing `[skip] efs — depends on network/outputs.json
(apply network first)` and exiting 0. After `terragrunt apply` in `network/`, re-running
`km init --plan` plans the dependents normally.

### Bootstrap UX

`km bootstrap --all` chains foundation (SCP + KMS + artifacts) then shared SES rule set in a
single command. It is mutually exclusive with `--shared-ses`. `--all --plan` honors the
destroy-class gate. This is the recommended primary bootstrap command for new installs.

`km bootstrap --shared-ses` provisions only the shared SES rule set (idempotent). Use this
when you need to re-run the SES step independently.

The status banner warns on empty required account IDs. If `accounts.organization`,
`accounts.dns_parent`, or `accounts.application` is empty in `km-config.yaml`, the bootstrap
banner emits `WARN: accounts.<key> is empty in km-config.yaml — required for this command` and
shows `(not set)` in place of the value.

### accounts.* precedence

`accounts.organization`, `accounts.dns_parent`, `accounts.application` are yaml-authoritative:
yaml wins unconditionally, env values do NOT override the in-memory `cfg`. `KM_ACCOUNTS_*` is
still exported to terragrunt subprocesses (operator overrides reach terraform), but `cfg` reads
always reflect yaml.

`accounts.terraform` preserves env-precedence (env wins) — operators retain shell-local override
for the cross-account terraform role; this asymmetry is intentional per CONTEXT.md.

### State-digest mismatch recovery

If a `km init` or `km bootstrap` run is interrupted mid-flight (Ctrl-C, terminal closure,
terminal session loss), terragrunt may leave the S3 state file and the DynamoDB lock-table
digest out of sync. Subsequent terragrunt operations will report:

```
Error: Error acquiring the state lock
Error message: state data in S3 does not have the expected content.
```

`km doctor` detects this via the `Terraform state lock digest` check and prints the exact
recovery command in the WARN Remediation field. Manual recovery (mirrors what `km doctor`
prints):

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

`km init` applies per-module context timeouts (default 5–10 min) and emits a quiet-mode
heartbeat every 15s — wedged applies no longer hang silently. If `km init` reports a timeout
error referencing a specific module, the wedged terragrunt PID is printed in the heartbeat
lines above the error; `kill -9 <pid>` to clean up the orphan before re-running.

### Multi-install quickstart

The multi-install design is production-ready. A second install in the same AWS account/Organization
(e.g. `resource_prefix: tg`) bootstraps cleanly on a shared-domain account without operator-side
workarounds.

From a sibling working directory (e.g. `klanker-maker-tg/`):

```bash
# 1. Clone the repo to a sibling working directory (NOT a subdirectory of klankrmkr/)
git clone <repo-url> klanker-maker-tg
cd klanker-maker-tg
make build

# 2. Configure (state_bucket default + HeadBucket retry are automatic)
./bin/km configure
# Set resource_prefix: tg; choose email_subdomain (shared or separate)

# 3. Plain bootstrap (SCP, KMS, artifacts, state) — region.hcl auto-prereq
./bin/km bootstrap --dry-run=false

# 4. Shared SES (auto-imports DKIM/MX/TXT records from sibling install — no manual import needed)
./bin/km bootstrap --shared-ses --dry-run=false

# 5. Plan + apply
make build-lambdas              # prereq for `km init --plan`: builds the 6 Lambda zips
                                # that the create-handler module's filebase64sha256 needs.
                                # `km init --dry-run=false` (full apply) builds zips itself;
                                # `km init --plan` (read-only) does not.
./bin/km init --plan
./bin/km init --dry-run=false

# 6. Sandbox lifecycle
./bin/km create profiles/learn.yaml
# ... use sandbox ...
./bin/km destroy <sandbox-id> --remote --yes

# 7. Clean teardown
./bin/km uninit --dry-run=false
./bin/km unbootstrap --dry-run=false
```

#### How it works

**Per-install resource naming.** All v2.0.0 modules (`scp`, `efs`, `s3-replication`,
`ssm-session-doc`) template resource names from `${var.resource_prefix}`. The
SSM Session Manager document is named `${prefix}-Sandbox-Session` (e.g.
`km-Sandbox-Session`, `tg-Sandbox-Session`). Each install owns its own copy;
teardown of one install does not affect another install's SSM documents.

**Pattern-based SCP trust.** Each install's `${prefix}-sandbox-containment`
SCP attaches to its own application account. The SCP body's `trusted_arns_*`
allowlists use suffix patterns (`*-create-handler`, `*-ec2spot-ssm-*`,
`*-budget-enforcer-*`, etc.) so any install's well-known role names are
trusted by any other install's SCP via suffix matching. A second install's
`tg-create-handler` is trusted by the canonical install's SCP without requiring
a policy-body edit.

**Security trade-off.** The wildcard pattern `arn:aws:iam::*:role/*-create-handler`
means a hypothetical role named `evil-create-handler` deployed in ANY account
would be trusted by the SCP. This is mitigated by layered defenses: (1)
operator-only `iam:CreateRole` permission in the application account, (2)
cross-account assume-role grants required to actually use deployed roles,
(3) the SCP is one defense-in-depth layer, not the only one. Operators who
want stricter isolation can move each install's application account into a
per-install OU; this is a manual operation not currently automated by km.

**DKIM auto-import on shared domains.** `km bootstrap --shared-ses` detects
when Route53 DKIM/MX/TXT records already exist (from a sibling install) and
automatically runs `terragrunt import` for each before the apply phase. No
manual import workaround is required for second-install shared-domain bootstrap.

**Fresh-clone DX.** `km configure` displays a computed default
`[tf-${prefix}-state-${regionLabel}]` for the state bucket prompt and
HeadBucket-checks the accepted name. `km bootstrap` auto-writes `region.hcl`
via a shared helper. `km unbootstrap` deletes the terragrunt-created DynamoDB
lock table. `make build-lambdas` produces all 6 Lambda zips that
`init.go:buildLambdaZips` enumerates.

> **Note — `km init --plan` prerequisite on a fresh clone.** Unlike
> `km init --dry-run=false` (full apply), which calls `buildLambdaZips`
> automatically, `km init --plan` (read-only) skips the build step.
> If `build/create-handler.zip` is missing, `terragrunt plan` for the
> `create-handler` module fails at `filebase64sha256(var.lambda_zip_path)`
> with `no such file or directory`. Run `make build-lambdas` (or
> `./bin/km init --lambdas`) once before the first `km init --plan` on a
> fresh clone; subsequent plans reuse the cached zips.

#### AWS limits to watch

**5 SCPs per OU/target.** AWS allows up to 5 SCPs per OU or account. Each install
attaches its own `${prefix}-sandbox-containment` SCP; the account-level SCP count
can reach 5 installs sharing a single application account before hitting this limit.

**5,120-byte SCP policy size.** `infra/modules/scp/v2.0.0/main.tf` includes a
`terraform_data.scp_size_guard` precondition that trips at plan-time if the rendered
SCP policy JSON exceeds 5,000 bytes (120-byte buffer below the AWS hard limit). If
a future `trusted_arns_*` addition would exceed the limit, the plan fails with a
clear error before AWS rejects the apply.

**1-2s SSM document outage during ssm-session-doc apply.** When applying
`ssm-session-doc/v2.0.0` for the first time on an install that previously had
v1.0.0, the AWS provider destroys the old document and creates the new
`${prefix}-Sandbox-Session` in a ~1-2 second window. Active SSM sessions
started before the destroy survive. New sessions started during the window fail
with `InvalidDocument` and retry cleanly. Terminate active `km shell` sessions
before applying this transition.

#### Companion teardown

`km uninit` destroys regional resources. `km unbootstrap` destroys the install's
foundation (state bucket, KMS alias, artifacts bucket, DynamoDB lock table).
The install's SCP is NOT auto-detached/deleted by either command — manually
detach via:

```bash
AWS_PROFILE=<management-profile> aws organizations detach-policy \
  --policy-id <id> --target-id <application-account-id>
AWS_PROFILE=<management-profile> aws organizations delete-policy \
  --policy-id <id>
```

---

## Slack notifications

klankermaker's Slack bridge routes sandbox output to a shared channel (`#km-notifications` or
similar) and to per-sandbox `#sb-{id}` channels. Full setup runbook in
[docs/slack-notifications.md](docs/slack-notifications.md).

### Mention-only mode (polite-bot)

Phase 91 introduced polite-bot mode: in shared (Mode 1) and operator-controlled override
(Mode 3) Slack channels the bot only reacts to messages that explicitly @-mention it
(`<@KlankerMaker>`). Per-sandbox `#sb-{id}` channels continue to process every message.

**Install-level default** (Phase 91.1) lives in `km-config.yaml`:

```yaml
slack:
    mention_only: true     # polite-bot default for the whole install
    # mention_only: false  # chatty default — every message routed
    # (omit the slack: block → terragrunt fallback "false" applies)
```

`km init` reads this and exports `KM_SLACK_MENTION_ONLY` into terragrunt. The bridge Lambda's
environment block is updated on `terragrunt apply` (full `km init`).

**Per-profile override** still wins per sandbox:

```yaml
spec:
  notification:
    slack:
      enabled: true
      inbound:
        # Tri-state *bool: nil = mode default, true = force polite, false = force chatty
        mentionOnly: true
```

New `km doctor` check: `slack_bot_user_id_cached` — WARNs when at least one profile resolves
to mention-only AND the bot user ID is not cached at `{prefix}slack/bot-user-id` in SSM.
Remediation: `km slack init --force`.

See [docs/slack-notifications.md § Phase 91](docs/slack-notifications.md#phase-91-polite-bot--mention-only-mode-for-sharedoverride-channels)
for the full operator guide, rollout sequence, and troubleshooting matrix.

> **Rollout (Phase 91.1):** edit `km-config.yaml` (`slack.mention_only: true`) → `km init
> --dry-run=false` (full terragrunt apply — `--sidecars` alone is NOT enough; it only swaps
> binaries and forces a cold-start, but the Lambda env block only updates on full apply).
> `KM_SLACK_BOT_USER_ID` is auto-read from SSM by `km init`. Existing sandboxes need
> `km destroy <id> --remote --yes && km create <profile>` to pick up the new schema field.

---

## SOPS secret injection

Declarative secret injection into sandboxes via `spec.secrets.sopsFile`. Full
runbook in [docs/sandbox-secrets.md](docs/sandbox-secrets.md). One-time setup:

```bash
km bootstrap --shared-secrets-key
km init --sidecars       # if not already done since Phase 89 shipped
km configure              # writes /secrets/* + !/secrets/*.enc.yaml to .gitignore
```

Then encrypt a bundle with `sops --kms alias/${resource_prefix}-sandbox-secrets`
and reference it from a profile via `spec.secrets.sopsFile`.

`km doctor` will report `✓ Shared secrets KMS key healthy` once the key is
provisioned, and will WARN on orphan sibling aliases (expected when a sibling
install is present on the same account).

## 9. additionalSnapshots — snapshot-backed EBS volumes

`spec.runtime.additionalSnapshots` materialises fresh EBS volumes from existing snapshots at
sandbox creation time.

### When to use

Use `additionalSnapshots` when your sandbox workload needs read-write access to a dataset
that is too large to provision at boot (models, training data, build caches). Snapshot the
data once, then reference it in any number of sandbox profiles. Each sandbox gets its own
independent materialised copy — writes don't affect the source snapshot or sibling sandboxes.

### Schema

```yaml
spec:
  runtime:
    substrate: ec2          # EC2 only — docker/ECS rejects at validation
    additionalSnapshots:
      - snapshotId: snap-0123abcdef0123456   # required; regex ^snap-[0-9a-f]{8,17}$
        mountPoint: /opt/models              # required; absolute path, not in reserved list
        device: /dev/sdh                     # optional; pin to /dev/sd[f-p]; omit for auto
        encrypted: true                      # optional; omit = inherit from snapshot
        size: 200                            # optional GiB; omit/0 = inherit from snapshot
```

Snapshot IDs use the standard AWS format: `snap-` followed by 8 to 17 lowercase hex digits.

### Validation layers

**Layer 1 — `km validate` (no AWS calls):**

| Rule | Detail |
|------|--------|
| EC2-only substrate | `docker` / future ECS substrates are rejected with a clear error |
| `snapshotId` format | Must match `^snap-[0-9a-f]{8,17}$` |
| `mountPoint` safety | Must be absolute; must not equal a reserved path (`/`, `/shared`, `/workspace`, `/proc`, `/sys`, `/dev`, `/etc`, `/usr`, `/var`, `/root`, `/home`, `/boot`, `/tmp`, `/run`, or bare `/opt`) |
| Mount collision | Must not collide with `additionalVolume.mountPoint` or another snapshot entry |
| `device` (if explicit) | Must match `^/dev/sd[f-p]$`; must be unique across all entries |
| `size` (if set) | Must be >= 1 |

**Layer 2 — `km create` pre-flight (single AWS call before compile):**

| Condition | Behaviour |
|-----------|-----------|
| Snapshot missing / wrong region / not shared | Aborts; error names the snap ID + 3-line hint (region / sharing / deletion) |
| Snapshot in `pending` or `error` state | Aborts; error names snap ID + state |
| `size` < `snapshot.VolumeSize` | Aborts; error states BOTH the requested size and the snapshot size |
| Caller lacks `ec2:DescribeSnapshots` | WARN logged; pre-flight skipped; terragrunt apply becomes the fallback failure path |

Pre-flight runs BEFORE the compiler emits any HCL — a rejection leaves zero terragrunt
working directories on disk.

### Device allocation

The compiler allocates `/dev/sd[f-p]` (11 slots) across:

1. AMI BlockDeviceMappings (reserved first — read from DescribeImages at compile time)
2. `additionalVolume` (if set)
3. `additionalSnapshots` entries in declaration order (explicit pins are claimed first,
   then auto-pick fills remaining slots)

Pool exhaustion (>11 total entries minus AMI BDM volumes) is a compile-time error naming
the offending entry index.

### Coexistence with `additionalVolume`

Both fields can be set on the same profile. They use separate render fields. Device
allocation pools them together so there is never a collision.

```yaml
spec:
  runtime:
    additionalVolume:
      size: 10
      mountPoint: /data        # auto-device: /dev/sdf

    additionalSnapshots:
      - snapshotId: snap-xxxx
        mountPoint: /opt/models  # auto-device: /dev/sdg (sdf taken by additionalVolume)
```

### Filesystem detection at boot

The userdata mount loop uses `blkid` to detect the filesystem type on each attached volume.
If the volume already has a filesystem (e.g., `ext4` on a snapshot from a formatted volume),
it is mounted directly — no `mkfs`. If no filesystem is detected (blank volume from
`additionalVolume`), `mkfs.ext4` is run first.

The fstab line uses a `${FSTYPE}` variable (resolved by userdata from `blkid` output).
For blank `additionalVolume` volumes this resolves to `ext4`.

### Lifecycle

- Materialised volumes are created and destroyed with the sandbox.
- `km destroy` deletes all materialised `aws_ebs_volume.snapshot[*]` instances.
- The **source snapshot is not touched** — it survives `km destroy` and can be reused
  across multiple sandboxes or profiles.

### Example profile

See `profiles/example-additional-snapshots.yaml`. Replace the placeholder `snap-*` IDs with
real snapshot IDs before running `km create`:

```bash
# Create a snapshot from an existing volume
VOL=$(aws ec2 create-volume --region us-east-1 --availability-zone us-east-1a \
        --size 5 --volume-type gp3 --query VolumeId --output text)
aws ec2 wait volume-available --region us-east-1 --volume-ids "$VOL"
SNAP=$(aws ec2 create-snapshot --region us-east-1 --volume-id "$VOL" \
         --description "my-dataset" --query SnapshotId --output text)
aws ec2 wait snapshot-completed --region us-east-1 --snapshot-ids "$SNAP"
echo "Use this in your profile: snapshotId: $SNAP"
```

Then edit your profile to reference `$SNAP` and run `km validate` before `km create`.

### UAT runbook

`.planning/phases/87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile/87-07-UAT.md`
contains 8 operator-driven UAT scenarios covering: single snapshot, multi-snapshot + additionalVolume,
explicit device pin, AMI BDM collision, missing snapshot, wrong-region snapshot, size-override-larger,
and size-override-smaller (pre-flight rejection).

Pre-authored UAT profiles live in `profiles/uat/87/uat-{1..8}.yaml`.

### Module version

`additionalSnapshots` ships as `infra/modules/ec2spot/v1.1.0/` (additive minor version per the
module-immutability convention). Existing sandboxes pinned to `v1.0.0` are unchanged.
New sandboxes use `v1.1.0` via the bumped `infra/templates/sandbox/terragrunt.hcl` source path.

### Failure mode reference

| Condition | Caught where | Behaviour |
|-----------|--------------|-----------|
| `snapshotId` malformed | `km validate` | Profile rejected; error names entry index |
| `mountPoint` collision or reserved | `km validate` | Profile rejected; error names colliding entries |
| `device` duplicate (explicit) | `km validate` | Validation error |
| `device` auto-pick exhaustion | Compiler | Compile error names offending entry |
| Snapshot missing / wrong region / not shared | `km create` pre-flight | Create aborts; names snap ID + region + 3-line hint |
| Snapshot in pending or error state | `km create` pre-flight | Create aborts; names snap ID + state |
| `size` < `snapshot.VolumeSize` | `km create` pre-flight | Create aborts; states both sizes |
| Caller lacks `ec2:DescribeSnapshots` | `km create` pre-flight | WARN logged; terragrunt apply is fallback |
| EBS attach > 60s at boot | userdata | WARNING to `/var/log/km-bootstrap.log`; sandbox boots without mount |
| Snapshot KMS decryption fails | EC2 boot | Volume attach fails; userdata WARNs; sandbox boots without mount. Fix: grant `kms:CreateGrant` to sandbox EC2 service role |

## 10. km desktop — KasmVNC remote browser session

`km desktop` gives operators a graphical browser session inside a sandbox EC2 rendered in their **local browser** over an SSM port-forward. The engine is **KasmVNC** — a web-native VNC server with a built-in HTML5 client and seamless bidirectional clipboard. Two modes are available: `kiosk` (default — single maximized browser) and `full` (XFCE4 desktop).

Full runbook: `docs/desktop.md`. Skill: `klanker:desktop`.

### When to use

- You need a remote browser running inside the sandbox EC2 (manual web testing, OAuth flows, visual QA, browser automation observation).
- You need a full graphical desktop environment in the sandbox.
- Your use case requires bidirectional clipboard between the operator's machine and the remote browser.

### Schema

```yaml
spec:
  runtime:
    ami: ubuntu-24.04         # Ubuntu 24.04 or 22.04 required
    desktop:
      enabled: true           # default false; opt-in (heavy install)
      mode: kiosk             # kiosk | full
      browsers: [firefox]     # subset of: firefox, chromium, chrome, brave
      geometry: 1920x1080     # optional
```

`desktop.enabled` defaults **false** (deliberately opt-in — the install is heavy). Set it explicitly. `km validate` hard-errors when `desktop.enabled: true` and the AMI is not Ubuntu.

**Browser enum:** `{firefox, chromium, chrome, brave}`. `chrome` = Google Chrome; `chromium` = open-source build.

### Validation layers

| Check | Caught by | Behavior |
|---|---|---|
| `mode` not in `{kiosk, full}` | `km validate` | Error |
| `browsers` not a subset of `{firefox, chromium, chrome, brave}` | `km validate` | Error |
| `browsers` empty when `mode: kiosk` | `km validate` | Error |
| `geometry` not matching `NNNxNNN` | `km validate` | Error |
| `desktop.enabled: true` + non-Ubuntu AMI | `km validate` | Error |

### Operator workflow

```bash
# One-time: redeploy the create-handler Lambda so REMOTE create understands the
# desktop schema + Ubuntu OS-aware bootstrap (the Lambda compiles userdata itself).
make build-lambdas && km init --dry-run=false   # (km create --local skips this)

# Create — per-sandbox KasmVNC credential is generated locally
km create profiles/desktop.yaml --alias my-desktop

# Open the SSM tunnel (blocking; auto-reconnects on drop — Ctrl-C to close)
km desktop start my-desktop
# Prints: https://localhost:8444/   user: sandbox   password: <random>

# Open https://localhost:8444/ in your local browser and log in.

# Check KasmVNC unit state
km desktop status my-desktop

# (Optional) rotate the KasmVNC password on a running sandbox — no restart,
# no session interruption; re-open with km desktop start afterward
km desktop rekey my-desktop [--force] [--yes]

# Teardown
km destroy my-desktop --remote --yes
```

`--local-port <N>` overrides the default 8444.

### Credential lifecycle

At `km create` time, a per-sandbox KasmVNC credential (username + random password) is generated and stored locally at `~/.km/desktop/<sandbox-id>`. It is seeded into the sandbox's `~/.kasmpasswd` via userdata and is **never baked into an AMI**, so one desktop AMI serves all sandboxes with different credentials. `km destroy` removes the local credential file.

### AMI-bake for routine use

The first-boot userdata installs KasmVNC + WM + browsers — this is slow (several minutes). For repeated use, bake an AMI:

```bash
# Any network posture works — the install runs pre-enforcement over HTTPS
km create profiles/desktop.yaml --alias bake-session

# Wait for full boot, then bake
km ami bake bake-session
# Output includes: ami-xxxxxxxxxxxxxxxxx

km destroy bake-session --remote --yes

# In your production profile: spec.runtime.ami: ami-xxxxxxxxxxxxxxxxx
# Subsequent boots skip package installation entirely.
```

### First-boot network requirements

The `spec.network` allowlist does **not** gate the desktop install. The stack is installed in userdata *before* network enforcement (proxy/iptables/eBPF) comes up, and the sandbox security group permits HTTPS (443) egress — so a fresh boot reaches distro mirrors, the KasmVNC release URL, and browser vendor repos regardless of `allowedDNSSuffixes`/`allowedHosts`. (On Ubuntu `apt` is auto-pinned to HTTPS + IPv4 because the SG allows only 443/53 — no port 80.) The allowlist governs the **browser's runtime egress** once the session is up; tune it per profile. The only first-boot cost is time — use the AMI-bake workflow above to skip it.

### Security model

- KasmVNC + session bind `127.0.0.1` on the sandbox — no LAN/VPC exposure.
- Only ingress is the operator's SSM `AWS-StartPortForwardingSession` (authenticated, encrypted).
- SSL disabled at KasmVNC layer — acceptable because of loopback bind + SSM tunnel.
- Per-sandbox credential is defense-in-depth against local port-riding.

### Rollout

The desktop schema + Ubuntu OS-aware bootstrap are compiled by the **create-handler Lambda** (it runs `km create` as a subprocess), which only updates on a full apply — so redeploy with `make build-lambdas` (clean) + `km init --dry-run=false`, not `--sidecars`. Existing sandboxes do not pick up `desktop` retroactively — `km destroy && km create`.

### Failure mode reference

| Condition | Caught where | Behavior |
|---|---|---|
| Non-Ubuntu AMI + `desktop.enabled: true` | `km validate` | Hard error |
| `browsers` empty in kiosk mode | `km validate` | Hard error |
| Local port in use | `km desktop start` | Fails fast with `--local-port` hint |
| KasmVNC unit not active | `km desktop start` pre-flight | Descriptive error; suggests `km desktop status` |
| Desktop not enabled in profile | `km desktop start` pre-flight | "desktop not enabled — set `spec.runtime.desktop.enabled: true` and recreate" |
| Credential file missing | `km desktop start` | Error with recovery hint (use `km shell` to read `~/.kasmpasswd`) |
| Slow first boot (no AMI) | boot time | Expected; nudge toward `km ami bake` |
