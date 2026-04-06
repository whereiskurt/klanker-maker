# Klanker Maker User Manual

## Table of Contents

- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Configuration](#configuration)
- [Command Reference](#command-reference)
  - [km init](#km-init)
  - [km validate](#km-validate)
  - [km create](#km-create)
  - [km destroy](#km-destroy)
  - [km pause](#km-pause)
  - [km lock](#km-lock)
  - [km unlock](#km-unlock)
  - [km list](#km-list)
  - [km status](#km-status)
  - [km logs](#km-logs)
  - [km doctor](#km-doctor)
  - [km configure github](#km-configure-github)
  - [km budget add](#km-budget-add)
  - [km shell](#km-shell)
  - [km agent](#km-agent)
  - [km stop](#km-stop)
  - [km resume](#km-resume)
  - [km extend](#km-extend)
  - [km otel](#km-otel)
  - [km info](#km-info)
  - [km rsync](#km-rsync)
  - [km at / km schedule](#km-at--km-schedule)
  - [km configure](#km-configure)
  - [km uninit](#km-uninit)
  - [km bootstrap](#km-bootstrap)
  - [km roll creds](#km-roll-creds)
  - [km completion](#km-completion)
- [Walkthrough: Claude Code in a Sandbox](#walkthrough-claude-code-in-a-sandbox)
- [Walkthrough: Goose with Budget Cap](#walkthrough-goose-with-budget-cap)
- [Walkthrough: Security Agent in a Sealed Sandbox](#walkthrough-security-agent-in-a-sealed-sandbox)
- [Profile Authoring Guide](#profile-authoring-guide)
  - [Profile spec.email](#profile-specemail)
  - [Profile sourceAccess.github](#profile-sourceaccessgithub)
- [Lifecycle and Teardown](#lifecycle-and-teardown)
- [Troubleshooting](#troubleshooting)

---

## Prerequisites

Before using Klanker Maker you need:

1. **Go 1.25+** — for building `km` from source
2. **Terraform 1.5+** and **Terragrunt 0.55+** — sandbox provisioning
3. **AWS CLI v2** — configured with named profiles
4. **AWS Account** — with the following set up:
   - An S3 bucket for Terraform state (e.g., `tf-km-state-use1`)
   - A DynamoDB table for state locking (e.g., `tf-km-locks-use1`)
   - An S3 bucket for artifacts and SES inbox (e.g., `km-sandbox-artifacts-ea554771`)
   - A KMS key alias `alias/km-sops` for secret encryption
   - (Optional) A TTL handler Lambda deployed from `cmd/ttl-handler/`
   - (Optional) SES domain identity verified for `klankermaker.ai`

### AWS CLI Profiles

Klanker Maker expects two named AWS CLI profiles:

| Profile | Used By | Purpose |
|---------|---------|---------|
| `klanker-application` | `km init` | Provisions shared VPC/network infrastructure |
| `klanker-terraform` | `km create`, `km destroy`, `km list`, `km status`, `km logs` | Sandbox provisioning and management |

Configure them in `~/.aws/config`:

```ini
[profile klanker-application]
sso_start_url = https://yourorg.awsapps.com/start
sso_account_id = 111111111111
sso_role_name = ApplicationAdmin
region = us-east-1

[profile klanker-terraform]
sso_start_url = https://yourorg.awsapps.com/start
sso_account_id = 222222222222
sso_role_name = TerraformOperator
region = us-east-1
```

---

## Installation

```bash
# From source
git clone https://github.com/whereiskurt/klankrmkr.git
cd klankrmkr
go build -o km ./cmd/km/
sudo mv km /usr/local/bin/

# Or install directly
go install github.com/whereiskurt/klankrmkr/cmd/km@latest

# Verify
km --help
```

---

## Configuration

Klanker Maker reads configuration from multiple sources in order of precedence:

1. **CLI flags** (highest)
2. **Environment variables** (`KM_` prefix)
3. **Config file** (`~/.km/config.yaml` or `./config.yaml`)
4. **Defaults** (lowest)

### Config File

Create `~/.km/config.yaml`:

```yaml
# Where to look for profile YAML files (in addition to built-in profiles)
profile_search_paths:
  - ./profiles
  - ~/.km/profiles

# Logging verbosity: trace, debug, info, warn, error
log_level: info

# S3 bucket for Terraform state and sandbox metadata
state_bucket: tf-km-state-use1

# Lambda ARN for TTL auto-destroy (deployed from cmd/ttl-handler/)
ttl_lambda_arn: arn:aws:lambda:us-east-1:222222222222:function:km-ttl-handler

# IAM role ARN for EventBridge Scheduler to assume when invoking TTL Lambda
scheduler_role_arn: arn:aws:iam::222222222222:role/km-scheduler-role
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `KM_LOG_LEVEL` | Logging verbosity | `info` |
| `KM_STATE_BUCKET` | S3 bucket for Terraform state | (from config file) |
| `KM_ARTIFACTS_BUCKET` | S3 bucket for artifacts, profiles, SES inbox | `km-sandbox-artifacts-ea554771` |
| `KM_TTL_LAMBDA_ARN` | Lambda function ARN for TTL teardown | (from config file) |
| `KM_SCHEDULER_ROLE_ARN` | IAM role ARN for EventBridge Scheduler | (from config file) |
| `KM_OPERATOR_EMAIL` | Email address for lifecycle notifications | (none — notifications disabled) |
| `KM_PROFILE_SEARCH_PATHS` | Colon-separated profile directories | `./profiles:~/.km/profiles` |

---

## Command Reference

### km init

Initialize shared regional infrastructure (VPC, subnets, security groups). **Run once per region** before creating sandboxes. Use `--sidecars` or `--lambdas` for fast partial deploys.

```
km init [--region <region>] [--aws-profile <profile>] [--sidecars] [--lambdas] [--verbose]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--region` | `us-east-1` | AWS region to initialize |
| `--aws-profile` | `klanker-application` | AWS CLI profile for provisioning |
| `--sidecars` | `false` | Only rebuild and upload sidecars + km binary + toolchain (skip Terraform) |
| `--lambdas` | `false` | Only rebuild and deploy Lambda functions (skip Terraform) |
| `--verbose` | `false` | Show full terragrunt/terraform output |

**Full init** (~9 min): DNS zone, Lambda build, sidecar build+upload, ECR push, toolchain upload, proxy CA, Terraform apply.

**Partial init** (~1-2 min): `--sidecars` and `--lambdas` can be combined or used alone to skip the Terraform apply when you only need to update binaries.

**Example:**

```bash
# Full init for us-east-1
km init --region us-east-1

# Fast: just rebuild and deploy km + sidecars to S3
km init --sidecars

# Fast: just rebuild and deploy Lambdas
km init --lambdas

# Both: rebuild everything except Terraform
km init --sidecars --lambdas
```

**Output:**

```
Initializing region us-east-1 (use1)...
  VPC:     vpc-0a1b2c3d4e5f6a7b8
  Subnets: subnet-aaa, subnet-bbb, subnet-ccc
  AZs:     us-east-1a, us-east-1b, us-east-1c

Ready for: km create --region us-east-1 <profile.yaml>
```

**Region label mapping:**

| Region | Label |
|--------|-------|
| us-east-1 | use1 |
| us-west-2 | usw2 |
| ca-central-1 | cac1 |
| eu-west-1 | euw1 |
| ap-southeast-1 | apse1 |

---

### km validate

Validate one or more SandboxProfile YAML files against the schema.

```
km validate <profile.yaml> [profile2.yaml ...]
```

**What it does:**

1. Parses each YAML file
2. If the profile uses `extends`, resolves the full inheritance chain
3. Validates schema structure (required fields, types, allowed values)
4. Validates semantic constraints (TTL vs idle timeout, substrate-specific rules)
5. Reports errors per-file; continues checking all files even if some fail

**Exit codes:** 0 if all valid, 1 if any invalid.

**Example:**

```bash
# Validate a single profile
km validate profiles/goose.yaml
# goose.yaml: valid

# Validate multiple profiles
km validate profiles/*.yaml
# goose.yaml: valid
# goose-ebpf.yaml: valid
# hardened.yaml: valid
# sealed.yaml: valid

# Validate a custom profile with errors
km validate my-broken-profile.yaml
# ERROR: my-broken-profile.yaml: spec.runtime.substrate: must be "ec2" or "ecs"
# ERROR: my-broken-profile.yaml: spec.lifecycle.ttl: invalid duration format
```

---

### km create

Provision a sandbox from a profile.

```
km create <profile.yaml> [--on-demand] [--no-bedrock] [--docker] [--alias <alias>] [--aws-profile <profile>] [--verbose] [--remote] [--sandbox-id-override <id>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--on-demand` | `false` | Override `spot: true` in profile; use on-demand instances |
| `--no-bedrock` | `false` | Disable Bedrock access -- removes IAM permissions and all Bedrock/Claude env vars |
| `--docker` | `false` | Shortcut for `--substrate=docker` (use Docker Compose local substrate) |
| `--alias` | `""` | Human-friendly alias for the sandbox (e.g. `orc`, `wrkr`). Overrides profile `metadata.alias` template |
| `--aws-profile` | `klanker-terraform` | AWS CLI profile for provisioning |
| `--verbose` | `false` | Show full terragrunt/terraform output |
| `--remote` | `false` | Dispatch to Lambda via EventBridge |
| `--sandbox-id-override` | `""` | Override the generated sandbox ID |

**What it does:**

1. Reads, parses, and validates the profile (including inheritance)
2. Generates a unique sandbox ID: `sb-{8-hex-chars}`
3. Validates AWS credentials
4. Loads shared network config from `km init` outputs
5. Compiles profile → Terragrunt artifacts (service.hcl, user-data.sh)
6. Creates sandbox directory under `infra/live/{region}/sandboxes/{sandbox-id}/`
7. Runs `terragrunt apply -auto-approve`
8. Stores metadata and profile in S3
9. Creates EventBridge TTL schedule (if configured)
10. Provisions SES email identity for the sandbox
11. Sends lifecycle notification (if `KM_OPERATOR_EMAIL` set)

**Example:**

```bash
# Create a sandbox from a built-in profile
km create profiles/goose.yaml

# Create with on-demand instances (no spot)
km create profiles/sealed.yaml --on-demand

# Disable Bedrock — use direct Anthropic API keys instead
km create profiles/goose.yaml --no-bedrock

# Use Docker Compose local substrate
km create profiles/goose.yaml --docker

# Override the sandbox alias
km create profiles/goose.yaml --alias mybot

# Create with a different AWS profile
km create my-profile.yaml --aws-profile my-custom-profile
```

**Output:**

```
Validating profile... ok
Compiling profile... ok
  sandbox:   sb-7f3a9e12
  substrate: ec2 (spot)
  region:    us-east-1

Provisioning sandbox...
  [terragrunt apply output streams here]

Post-provisioning:
  metadata:  s3://tf-km-state-use1/tf-km/sandboxes/sb-7f3a9e12/metadata.json
  profile:   s3://km-sandbox-artifacts-ea554771/artifacts/sb-7f3a9e12/.km-profile.yaml
  ttl:       scheduled for 2026-03-23T02:00:00Z (8h from now)
  email:     sb-7f3a9e12@sandboxes.klankermaker.ai

Sandbox sb-7f3a9e12 is ready.
Connect: aws ssm start-session --target <instance-id>
```

**Errors:**

| Error | Cause | Fix |
|-------|-------|-----|
| `network not initialized for region us-east-1` | `km init` not run | Run `km init --region us-east-1` first |
| `spec.runtime.substrate: must be "ec2" or "ecs"` | Invalid profile | Fix the YAML and re-validate |
| Credential errors | AWS profile not configured or expired | Run `aws sso login --profile klanker-terraform` |

---

### km destroy

Tear down a sandbox and clean up all resources. `km kill` is an alias for `km destroy`.

```
km destroy <sandbox-id> [--aws-profile <profile>] [--yes] [--verbose] [--remote]
km kill <sandbox-id> [--yes]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--aws-profile` | `klanker-terraform` | AWS CLI profile |
| `--yes` | `false` | Skip confirmation prompt |
| `--verbose` | `false` | Show full terragrunt/terraform output |
| `--remote` | `true` | Dispatch to Lambda via EventBridge (default); forced off for Docker substrate |

**What it does:**

1. Validates sandbox ID format (`{prefix}-[a-f0-9]{8}`)
2. Checks lock guard -- blocked if sandbox is locked (`km lock` was applied)
3. By default, dispatches destroy to Lambda via EventBridge (`--remote=true`)
4. For Docker substrate: automatically forces local destroy (Lambda cannot reach local containers)
5. Discovers sandbox resources via AWS tag scan (`km:sandbox-id`)
6. For EC2 spot: explicitly terminates the instance first (critical -- `terraform destroy` alone leaves spot instances running)
7. Cancels EventBridge TTL schedule
8. Uploads artifacts (if configured in profile)
9. Runs `terragrunt destroy -auto-approve`
10. Sends lifecycle notification
11. Cleans up local sandbox directory and SES email identity

**Example:**

```bash
km destroy sb-7f3a9e12              # Lambda-dispatched (default)
km destroy sb-7f3a9e12 --yes        # skip confirmation
km kill sb-7f3a9e12 --yes           # alias, same behavior
km destroy sb-7f3a9e12 --remote=false  # local terragrunt destroy
```

**Output:**

```
Discovering sandbox sb-7f3a9e12... found (3 resources)
Terminating spot instance i-0abc123def456...
Cancelling TTL schedule...
Uploading artifacts...
  /workspace/output/results.json (2.3 KB)
Destroying infrastructure...
  [terragrunt destroy output streams here]
Cleaning up...

Sandbox sb-7f3a9e12 destroyed.
```

---

### km pause

Pause (hibernate) a sandbox's EC2 instance, preserving RAM state and infrastructure.

```
km pause <sandbox-id | #number> [--remote]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--remote` | `false` | Dispatch to Lambda via EventBridge (no local AWS creds needed) |

**What it does:**

1. Validates sandbox ID
2. Checks lock guard -- blocked if sandbox is locked (`km lock` was applied)
3. Reads metadata from DynamoDB (falls back to S3 if table not provisioned) to detect substrate
4. **Docker substrate**: runs `docker compose pause` on the local sandbox containers (verifies the sandbox is running on this host first)
5. **EC2 substrate**: calls `StopInstances` with `Hibernate: true` -- falls back to a normal stop if hibernation is not configured
6. **Spot instances**: cannot be paused -- returns a clear error suggesting `--on-demand`
7. Updates metadata status to `"paused"` in DynamoDB

**Notes:**
- Hibernation preserves the RAM state to EBS; the instance resumes exactly where it left off on restart
- If the instance was not enabled for hibernation at launch, the command falls back to a normal stop with an info message
- Docker sandboxes must be running on the local host (checks for `docker-compose.yml`)

**Example:**

```bash
km pause 1
km pause sb-7f3a9e12
km pause sb-7f3a9e12 --remote
```

---

### km lock

Lock a sandbox to prevent accidental destruction, stopping, or pausing.

```
km lock <sandbox-id | #number> [--remote]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--remote` | `false` | Dispatch to Lambda via EventBridge (no local AWS creds needed) |

**What it does:**

1. Validates sandbox ID
2. Performs an atomic conditional update in DynamoDB (`km-sandboxes` table) -- no read-modify-write race condition
3. Falls back to S3 read-modify-write if the DynamoDB table does not exist
4. If already locked, returns a clear "already locked" message

**Blocked operations while locked:**
- `km destroy` — returns "is locked" error before any resource teardown
- `km stop` — returns "is locked" error before EC2 API calls
- `km pause` — returns "is locked" error before EC2 API calls

**Example:**

```bash
km lock 1
km lock sb-7f3a9e12
```

**Output:**

```
Sandbox sb-7f3a9e12 locked.
```

To remove the lock, use `km unlock`.

---

### km unlock

Unlock a sandbox to re-enable destroy, stop, and pause operations.

```
km unlock <sandbox-id | #number> [--yes] [--remote]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--yes` | `false` | Skip confirmation prompt |
| `--remote` | `false` | Dispatch to Lambda via EventBridge (no local AWS creds needed) |

**What it does:**

1. Validates sandbox ID
2. Performs an atomic conditional update in DynamoDB (`km-sandboxes` table) -- no read-modify-write race condition
3. Falls back to S3 read-modify-write if the DynamoDB table does not exist
4. Without `--yes`: prompts `Unlock sandbox <id>? This will allow destroy/stop/pause. [y/N]`
5. If not locked, returns a clear "not locked" message

**Example:**

```bash
km unlock 1
km unlock sb-7f3a9e12 --yes   # skip confirmation
```

**Output:**

```
Unlock sandbox sb-7f3a9e12? This will allow destroy/stop/pause. [y/N] y
Sandbox sb-7f3a9e12 unlocked.
```

---

### km list

List all running sandboxes. Alias: `km ls`.

```
km list [--json] [--tags] [--wide]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | `false` | Output as JSON array |
| `--tags` | `false` | Use AWS tag scan instead of DynamoDB (slower, more reliable) |
| `--wide` | `false` | Show all columns (profile, substrate, region) |

**Data source:** Primary lookup uses a single DynamoDB Scan on the `km-sandboxes` table (fast, O(1) per page). Falls back to S3 listing if the DynamoDB table does not exist.

**Display:**
- Default (narrow) mode shows: `#`, `ALIAS`, `SANDBOX ID`, `STATUS`, `TTL`
- Wide mode (`--wide`) adds: `PROFILE`, `SUBSTRATE`, `REGION`
- `ALIAS` column appears first (after `#`), then `SANDBOX ID`
- Locked sandboxes are shown in bold white text with a lock icon at the end of the row
- Paused/stopped sandboxes show in magenta
- Spot-reaped instances show as "reaped" in yellow

**Example:**

```bash
km list
```

**Output (narrow, default):**

```
#   ALIAS       SANDBOX ID    STATUS     TTL
1   my-agent    sb-7f3a9e12   running    5h46m
2   -           sb-a4c8e210   running    2h12m
3   dev-box     sb-d9f01b3c   paused     18h33m
4   locked-one  sb-e5b82f01   running    3h10m  🔒
```

**Output (wide):**

```bash
km list --wide
```

```
#   ALIAS       SANDBOX ID    PROFILE          SUBSTRATE  REGION       STATUS     TTL
1   my-agent    sb-7f3a9e12   goose            ec2        us-east-1    running    5h46m
2   -           sb-a4c8e210   hardened         ecs        us-east-1    running    2h12m
3   dev-box     sb-d9f01b3c   goose-ebpf       ec2        us-west-2    paused     18h33m
4   locked-one  sb-e5b82f01   codex            ec2        us-east-1    running    3h10m  🔒
```

```bash
km list --json
```

```json
[
  {
    "sandbox_id": "sb-7f3a9e12",
    "profile": "goose",
    "substrate": "ec2",
    "region": "us-east-1",
    "status": "running",
    "created_at": "2026-03-22T18:14:00Z",
    "ttl_expiry": "2026-03-23T02:14:00Z",
    "ttl_remaining": "5h46m"
  }
]
```

---

### km status

Show detailed state for a specific sandbox.

```
km status <sandbox-id>
```

**Example:**

```bash
km status sb-7f3a9e12
```

**Output:**

```
Sandbox ID:  sb-7f3a9e12
Profile:     goose
Substrate:   ec2
Region:      us-east-1
Status:      running
Created At:  2026-03-22T18:14:00Z
TTL Expiry:  2026-03-23T02:14:00Z
Resources (3):
  - arn:aws:ec2:us-east-1:222222222222:instance/i-0abc123def456
  - arn:aws:ec2:us-east-1:222222222222:security-group/sg-0abc123def456
  - arn:aws:iam::222222222222:role/km-sb-7f3a9e12
```

---

### km logs

Tail audit logs for a running sandbox.

```
km logs <sandbox-id> [--follow] [--stream <name>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--follow` | `false` | Stream logs continuously until Ctrl+C |
| `--stream` | `audit` | Log stream name within the sandbox's CloudWatch log group |

**Example:**

```bash
# View recent audit logs
km logs sb-7f3a9e12

# Stream logs in real time
km logs sb-7f3a9e12 --follow

# View network logs
km logs sb-7f3a9e12 --stream network
```

**Output:**

```
2026-03-22T18:15:02Z [cmd]  git clone https://github.com/mycompany/api-service.git
2026-03-22T18:15:08Z [net]  ALLOW dns github.com → 140.82.112.4
2026-03-22T18:15:09Z [net]  ALLOW https github.com:443
2026-03-22T18:15:34Z [cmd]  npm install
2026-03-22T18:15:35Z [net]  ALLOW dns registry.npmjs.org → 104.16.23.35
2026-03-22T18:16:12Z [cmd]  npm test
2026-03-22T18:17:45Z [net]  BLOCK dns evil.example.com → NXDOMAIN
```

---

### km doctor

Check platform health and bootstrap verification.

```
km doctor [--json] [--quiet]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | `false` | Output results as a JSON array |
| `--quiet` | `false` | Suppress OK and SKIPPED results; show only WARN and ERROR |

**What it checks:**

| Check | Description |
|-------|-------------|
| Config | Required config fields: domain, account IDs, SSO start URL, primary region |
| Credentials | AWS credentials via STS `GetCallerIdentity`; auto-skips all AWS checks if expired |
| State Bucket | S3 state bucket exists and is accessible |
| Budget Table | DynamoDB `km-budgets` table exists |
| Identity Table | DynamoDB `km-identities` table exists (WARN if absent, not ERROR) |
| KMS Key | KMS alias `km-platform` exists |
| SCP | `km-sandbox-containment` SCP attached to the application account |
| GitHub App Config | SSM parameters for GitHub App exist (WARN if missing — GitHub integration is optional) |
| Credential Rotation | Checks rotation age against 90-day threshold from SSM |
| VPC | km-managed VPC and subnets exist in the primary region |
| Lambda TTL Handler | Verifies `km-ttl-handler` Lambda function exists |
| SES Domain Identity | Checks SES domain is verified |
| Sandbox Summary | Counts total sandboxes and reports running/stopped/paused breakdown |
| Sidecar Artifacts | Checks S3 artifacts bucket for sidecar binaries |
| Safe Phrase | Verifies KM-AUTH safe phrase configured in SSM |
| Stale KMS Keys | Detects unused KMS keys from destroyed sandboxes |
| Stale IAM Roles | Detects unused IAM roles from destroyed sandboxes |

All checks run in parallel. Results are sorted alphabetically and include a remediation hint for failures.

**Exit codes:** 0 if all checks pass (warnings don't fail), 1 if any check returns ERROR.

**Example output:**

```bash
km doctor
```

```
✓ Active Sandboxes                    total=2 (running=2)
✓ Budget Table (km-budgets)           table "km-budgets" exists
✓ Config                              domain=klankermaker.ai region=us-east-1
✓ Credentials (klanker-terraform)     authenticated as arn:aws:iam::333333333333:role/...
⚠ GitHub App Config                   parameter not found — GitHub integration not configured
  → Run 'km configure github' to set up GitHub App integration
✓ KMS Key (km-platform)               key "alias/km-platform" exists
✓ SCP (Sandbox Containment)           policy "km-sandbox-containment" attached to account 333333333333
✓ State Bucket                        bucket "tf-km-state-use1" is accessible
✓ VPC (us-east-1)                     found 1 km-managed VPC(s) in us-east-1

16 checks passed, 1 warnings, 0 errors
```

**JSON output:**

```bash
km doctor --json | jq '.[] | select(.status != "OK")'
```

**CI usage (exit code only):**

```bash
km doctor --quiet && echo "healthy" || echo "issues found"
```

---

### km configure github

Configure GitHub App credentials for sandbox source-access tokens. Credentials are stored in SSM Parameter Store and read at `km create` time to provision the per-sandbox token refresh Lambda.

```
km configure github [--setup] [--non-interactive] [--app-client-id <id>] [--private-key-file <path>] [--installation-id <id>] [--force]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--setup` | `false` | One-click GitHub App creation via manifest flow (opens browser) |
| `--non-interactive` | `false` | Skip interactive prompts; use flag values directly |
| `--app-client-id` | `""` | GitHub App client ID (e.g. `Iv1.abc123`) |
| `--private-key-file` | `""` | Path to the GitHub App private key PEM file |
| `--installation-id` | `""` | GitHub App installation ID for the target org/user |
| `--force` | `false` | Overwrite existing SSM parameters |

**Automated setup (recommended):**

```bash
km configure github --setup
```

Opens your browser to the GitHub App creation page, waits for the OAuth callback, exchanges the manifest code for credentials, and writes them to SSM automatically. If the browser does not open, copy the printed URL manually.

After App creation, if no installation is found, install the App on your org and then run:

```bash
km configure github --installation-id <ID>
```

**Manual flow:**

```bash
# Interactive (prompts for each value)
km configure github

# Non-interactive (for CI or scripts)
km configure github \
  --non-interactive \
  --app-client-id "Iv1.abc123" \
  --private-key-file /path/to/private-key.pem \
  --installation-id 12345678
```

**SSM parameters written:**

| Parameter | Type | Contents |
|-----------|------|----------|
| `/km/config/github/app-client-id` | String | GitHub App client ID |
| `/km/config/github/private-key` | SecureString | PEM-encoded RSA private key |
| `/km/config/github/installation-id` | String | Installation ID |

These parameters are required before `km create` can provision sandboxes with `sourceAccess.github` profiles.

---

### km budget add

Add budget to a sandbox and auto-resume it if suspended.

```
km budget add <sandbox-id> [--compute <amount>] [--ai <amount>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--compute` | `0` | Amount in USD to add to the compute budget limit |
| `--ai` | `0` | Amount in USD to add to the AI budget limit |

**Example:**

```bash
# Top up $5 compute and $3 AI
km budget add sb-7f3a9e12 --compute 5.00 --ai 3.00
```

**Output:**

```
Budget updated: compute $2.00/$7.00, AI $4.80/$7.80
Sandbox sb-7f3a9e12 resumed.
```

**What it does:**

1. Reads current budget limits from DynamoDB (`km-budgets` table)
2. Adds the top-up amounts to current limits (additive — not a replacement)
3. Writes the new limits back to DynamoDB
4. Auto-resumes the sandbox:
   - **EC2**: starts stopped instances via `StartInstances`
   - **ECS**: re-provisions the Fargate task using the stored profile YAML from S3
5. Restores the `AmazonBedrockFullAccess` IAM policy if the budget enforcer detached it

**Budget states visible in km status:**

```
Sandbox ID:  sb-7f3a9e12
...
Budget:
  Compute:  $2.00 / $7.00  (29%)
  AI:       $4.80 / $7.80  (62%)
    claude-3-5-sonnet:  $3.20
    claude-3-haiku:     $1.60
```

When compute spend reaches 80% of the limit, a warning email is sent (if `KM_OPERATOR_EMAIL` is set). At 100%, the EC2 instance is stopped or the ECS task is stopped. The sandbox is not destroyed — use `km budget add` to resume.

When AI spend (tracked by the http-proxy MITM) reaches 100%, the Bedrock IAM policy is detached from the sandbox role, causing API calls to return 403. `km budget add` re-attaches it.

---

### km shell

Open an interactive shell into a running sandbox via SSM.

```
km shell <sandbox-id | #number> [--root] [--ports <ports>] [--learn] [--learn-output <path>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--root` | `false` | Connect as root instead of restricted sandbox user |
| `--ports` | `""` | Port forwards (e.g. `8080`, `8080:80`, or comma-separated `8080:80,3000`) |
| `--learn` | `false` | After shell exit, generate a SandboxProfile YAML from observed DNS/TLS traffic |
| `--learn-output` | `observed-profile.yaml` | Path to write the generated profile |

**What it does:**

1. Resolves sandbox ID (supports `#number` shorthand from `km list`)
2. Discovers the EC2 instance or ECS task
3. Opens an SSM session to the sandbox
4. If `--ports` specified, establishes port forwarding (Docker-style `local:remote` syntax)
5. If `--learn` specified, after the shell exits:
   - Sends SIGUSR1 to the eBPF enforcer to flush observed traffic to S3
   - Downloads the observation data from S3
   - Generates an annotated SandboxProfile YAML with DNS suffix summary
   - Writes to `--learn-output` path

**Example:**

```bash
km shell 1                        # restricted user shell
km shell 1 --root                 # operator access (root)
km shell 1 --ports 8080           # forward localhost:8080 → remote:8080
km shell 1 --ports 8080:80,3000   # multiple forwards

# Learn mode: generate a profile from observed traffic
km shell --learn sb-abc123        # do stuff, exit → observed-profile.yaml
```

**Learn mode requirements:** The sandbox must have `spec.observability.learnMode: true` in its profile (e.g. `profiles/learn.yaml`). This enables the `--observe` flag on the eBPF enforcer so it records DNS queries and TLS connections. Without `learnMode`, there is no observation data to collect.

---

### km agent

Launch an AI coding agent inside a running sandbox.

```
km agent <sandbox-id | #number> [--claude] [--codex] [-- extra-args...]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--claude` | `false` | Launch Claude Code |
| `--codex` | `false` | Launch OpenAI Codex |

**What it does:**

1. Resolves sandbox ID
2. Opens an SSM session to the sandbox
3. Launches the selected agent binary with optional extra arguments passed after `--`

**Example:**

```bash
km agent 1 --claude                          # interactive Claude Code session
km agent 1 --claude -- -p "fix failing tests"  # headless with a prompt
km agent 2 --codex                            # launch OpenAI Codex
```

---

### km stop

Stop a sandbox's EC2 instance, preserving infrastructure for later restart.

```
km stop <sandbox-id | #number> [--remote]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--remote` | `false` | Dispatch to Lambda via EventBridge |

**What it does:**

1. Validates sandbox ID
2. Checks lock guard — blocked if sandbox is locked
3. Calls `StopInstances` on the EC2 instance
4. Instance can be restarted via `km resume` or `km budget add` (which auto-resumes stopped instances)

**Example:**

```bash
km stop 1
km stop sb-7f3a9e12 --remote
```

---

### km resume

Resume a paused or stopped sandbox.

```
km resume <sandbox-id | #number> [--remote]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--remote` | `false` | Dispatch resume to Lambda via EventBridge |

**What it does:**

1. Validates sandbox ID
2. Starts the stopped/paused EC2 instance via `StartInstances`
3. Updates DynamoDB metadata status back to `"running"`

**Example:**

```bash
km resume 1
km resume sb-7f3a9e12 --remote
```

---

### km extend

Extend a sandbox's TTL before it expires.

```
km extend <sandbox-id | #number> <duration> [--remote]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--remote` | `false` | Dispatch to Lambda via EventBridge |

Duration format: `1h`, `30m`, `2h30m`, etc.

**What it does:**

1. Reads sandbox metadata from S3
2. Adds the specified duration to the current TTL expiry
3. Enforces `maxLifetime` cap — if extending would exceed the profile's `maxLifetime`, the extension is capped
4. Updates the EventBridge TTL schedule

**Example:**

```bash
km extend 1 2h             # add 2 hours
km extend sb-7f3a9e12 30m   # add 30 minutes
```

---

### km otel

Show OpenTelemetry telemetry and AI spend summary for a sandbox.

```
km otel <sandbox-id | #number> [--prompts] [--events] [--timeline] [--tools]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--prompts` | `false` | Show Claude Code user prompts with timestamps |
| `--events` | `false` | Show full OpenTelemetry event stream |
| `--timeline` | `false` | Show conversation turns with per-turn cost |
| `--tools` | `false` | Show tool call history with parameters and duration |

**What it does:**

1. Reads OTEL data from S3 (exported by the tracing sidecar)
2. With no flags: shows budget summary + S3 data location + metrics overview
3. With flags: shows the requested view of telemetry data

**Example:**

```bash
km otel 1                  # summary
km otel 1 --prompts        # user prompts
km otel 1 --timeline       # conversation turns with cost
km otel 1 --tools          # tool calls with params + duration
km otel 1 --events         # full event stream
```

---

### km email send

Send a signed email between sandboxes or to/from the operator inbox.

```
km email send --subject <subject> --body <body> [--from <ref>] [--to <ref>] [--attach <files>] [--cc <addrs>] [--use-bcc] [--reply-to <addr>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--subject` | (required) | Email subject line |
| `--body` | (required) | Body text, file path, or `-` for stdin |
| `--from` | `operator` | Sender: sandbox ID, alias, list number, or raw email address |
| `--to` | `operator@sandboxes.{domain}` | Recipient: sandbox ID, alias, list number, or raw email address |
| `--attach` | `""` | Comma-separated file paths to attach |
| `--cc` | `""` | Comma-separated CC recipients (visible in MIME headers) |
| `--use-bcc` | `false` | BCC the operator email address |
| `--reply-to` | `""` | Set Reply-To header |

**What it does:**

1. Resolves `--from` and `--to` references (alias, list number, sandbox ID, or raw email address)
2. Reads body from inline text, a file path, or stdin (`-`)
3. Fetches the sender's Ed25519 private key from SSM and signs the body
4. Encrypts the body if the sender's profile has `encryption: required` or `optional` and the recipient has a published public key
5. Sends via SES with raw MIME (preserving `X-KM-*` custom headers)
6. When sending to the operator inbox, automatically appends the KM-AUTH safe phrase to the body before signing

**Example:**

```bash
# Operator sends to operator inbox (both default)
km email send --subject "test" --body "hello"

# Sandbox alice sends to operator
km email send --from alice --subject "help" --body report.txt

# Send between sandboxes
km email send --from alice --to bob --subject "task done" --body "results attached" --attach output.tar.gz

# Send from stdin
echo "pipeline complete" | km email send --from 1 --to 2 --subject "done" --body -

# CC and BCC
km email send --from alice --to bob --subject "update" --body msg.txt --cc carol --use-bcc
```

---

### km email read

Read messages from a sandbox mailbox with signature verification and auto-decryption.

```
km email read <sandbox-ref> [--json] [--raw] [--mark-read] [--message-id <id>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | `false` | Output as newline-delimited JSON (one object per message) |
| `--raw` | `false` | Dump raw MIME bytes to stdout |
| `--mark-read` | `false` | Move processed messages from `new/` to `processed/` |
| `--message-id` | `""` | Fetch a specific message (used with `--raw`; defaults to latest) |

**What it does:**

1. Resolves the sandbox reference (alias, list number, or sandbox ID)
2. Lists messages in the sandbox mailbox (S3-backed `new/` directory)
3. For each message: verifies the Ed25519 signature by fetching the sender's public key from DynamoDB
4. Auto-decrypts encrypted messages when the sandbox's encryption keys are available in SSM
5. Displays a summary table with columns: `#`, `FROM`, `SUBJECT`, `SIG` (verification status), `ENC` (encrypted), `BODY PREVIEW`

Messages remain in `new/` unless `--mark-read` is passed.

**Example:**

```bash
# Read alice's mailbox
km email read alice

# JSON output for scripting
km email read 1 --json

# Read and mark messages as processed
km email read bob --mark-read

# Dump raw MIME of latest message
km email read alice --raw
```

---

### km info

Show platform configuration and operational details.

```
km info
```

**What it does:**

Displays the current platform configuration organized into sections:

| Section | Fields |
|---------|--------|
| **Platform** | Domain, Region |
| **AWS Accounts** | Management, Terraform, Application account IDs |
| **AWS SSO** | Start URL, Region |
| **Storage** | State bucket, Artifacts bucket, Route53 zone |
| **DynamoDB Tables** | Sandboxes (`km-sandboxes`), Budgets (`km-budgets`), Identities (`km-identities`), Schedules (`km-schedules`) |
| **Email** | Operator email, Sandbox domain, Signing method (Ed25519), In-sandbox tools (`km-send`/`km-recv`) |
| **Email-to-Create** | Send-to address, Safe phrase (if configured) |
| **AWS Usage** | SES daily quota and remaining capacity, Account MTD spend, Bedrock MTD, EC2 MTD |
| **Config file** | Location of `km-config.yaml` |

The **AWS Usage** section fetches live data from SES (via `GetAccount`) and Cost Explorer (via `GetCostAndUsage`). Fields fall back to "(unavailable)" if the API is inaccessible — this is non-fatal.

**Example:**

```bash
km info
```

```
Platform
  Domain:   klankermaker.ai
  Region:   us-east-1

AWS Usage
  SES (24h):    487 / 50000 remaining (0.97% used)
  Account MTD:  $142.37
  AI MTD:       $89.12
  EC2 MTD:      $31.45
```

---

### km rsync

Save and restore sandbox user home directory snapshots.

```
km rsync save <sandbox-id> <name> [--profile-dir <dir>]
km rsync load <sandbox-id> <name>
```

| Flag | Default | Description |
|------|---------|-------------|
| `--profile-dir` | `""` | Directory containing sandbox profile (for rsyncPaths resolution) |

**What it does:**

- **save**: Creates a tarball of the sandbox user's home directory (or specific paths defined by `rsyncPaths` / `rsyncFileList` in the profile) and uploads it to S3
- **load**: Lists or restores a previously saved snapshot

Paths can be scoped via `spec.execution.rsyncPaths` in the profile, or via an external YAML file referenced by `spec.execution.rsyncFileList`.

**Example:**

```bash
km rsync save 1 checkpoint-1     # save home snapshot
km rsync load 1 checkpoint-1     # restore snapshot
```

---

### km at / km schedule

Schedule a deferred or recurring sandbox operation using AWS EventBridge Scheduler. `km schedule` is an alias for `km at`.

```
km at '<time-expr>' <command> [args...]
km at list
km at cancel <schedule-name> [--group <name>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--cron` | `""` | Raw EventBridge `cron()` expression (bypasses natural language parsing) |
| `--name` | `""` | Override auto-generated schedule name |
| `--group` | `km-at` | EventBridge Scheduler group name |

**Supported commands:** `create`, `destroy`, `kill`, `stop`, `pause`, `resume`, `extend`, `budget-add`

Each command is dispatched to the appropriate Lambda (create-handler for `create`, TTL handler for lifecycle commands) via EventBridge Scheduler.

**What it does:**

1. Parses the time expression (natural language like `'10pm tomorrow'` or `--cron 'cron(0 22 * * ? *)'`)
2. Creates an EventBridge Scheduler schedule targeting the appropriate Lambda
3. Stores schedule metadata in DynamoDB for `km at list`

**Alias resolution:** Lifecycle commands (`destroy`, `kill`, `stop`, `pause`, `resume`, `extend`, `budget-add`) resolve sandbox references the same way as interactive commands — by alias, list number (`1`), or raw sandbox ID.

**Create flags:** `km at create` supports `--alias` and `--on-demand` flags. The `--docker` flag is rejected (Docker sandboxes require local execution). When a profile YAML is available locally, it is auto-uploaded to S3 under the `scheduled/` prefix.

**Budget-add flags:** `km at budget-add` accepts `--compute <USD>` and `--ai <USD>` to specify the amounts to add.

**Subcommands:**

- **`km at list`** -- Lists all scheduled operations (name, command, target, schedule, status, created date)
- **`km at cancel <name>`** -- Cancels a scheduled operation (deletes from EventBridge Scheduler and DynamoDB)

**Example:**

```bash
# One-shot: create a sandbox tomorrow at 10pm
km at '10pm tomorrow' create profiles/goose.yaml

# Create with alias and on-demand
km at 'in 1 hour' create profiles/goose.yaml --alias g1 --on-demand

# Recurring: kill a sandbox every Thursday at 3pm (alias resolution)
km at 'every thursday at 3pm' kill alice

# Schedule a budget top-up
km at 'every monday at 9am' budget-add alice --compute 5.00 --ai 2.00

# Raw cron expression
km at --cron 'cron(0 15 ? * 5 *)' kill sb-abc123

# List all scheduled operations
km at list

# Cancel a schedule
km at cancel my-schedule-name

# km schedule is an alias
km schedule '10pm tomorrow' create profiles/goose.yaml
```

---

### km configure

Interactive wizard to set up `km-config.yaml`.

```
km configure [--non-interactive] [--domain <domain>] [--management-account <id>] [--terraform-account <id>] [--application-account <id>] [--sso-start-url <url>] [--sso-region <region>] [--region <region>] [--state-bucket <name>] [--artifacts-bucket <name>] [--operator-email <email>] [--safe-phrase <phrase>] [--max-sandboxes <count>]
```

**What it does:**

Walks through platform configuration interactively (or accepts flags for non-interactive/CI use). Writes `km-config.yaml` to the repo root.

**Example:**

```bash
km configure                     # interactive wizard
km configure --non-interactive \
  --domain mysandboxes.example.com \
  --management-account 111111111111 \
  --terraform-account 222222222222 \
  --application-account 333333333333
```

---

### km uninit

Tear down all shared regional infrastructure (reverse of `km init`).

```
km uninit [--region <region>] [--aws-profile <profile>] [--force] [--yes] [--verbose]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--region` | `us-east-1` | AWS region to tear down |
| `--aws-profile` | `klanker-application` | AWS CLI profile |
| `--force` | `false` | Destroy even if active sandboxes exist |
| `--yes` | `false` | Skip confirmation prompt |
| `--verbose` | `false` | Show full terragrunt/terraform output |

---

### km bootstrap

Deploy SCP containment policy, KMS key, and artifacts bucket.

```
km bootstrap [--dry-run] [--show-prereqs]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `true` | Print what would be created without making changes |
| `--show-prereqs` | `false` | Print the IAM role and trust policy needed in the management account |

---

### km roll creds

Rotate platform and sandbox credentials.

```
km roll creds [--sandbox <id>] [--platform]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--sandbox` | `""` | Rotate a single sandbox's credentials |
| `--platform` | `false` | Rotate proxy CA, KMS, optional GitHub App key |

**What it does:**

With no flags, rotates all platform credentials and all running sandbox identities. With `--sandbox`, targets a single sandbox. With `--platform`, rotates platform-level secrets (proxy CA certificate, KMS key, optionally GitHub App private key).

---

### km completion

Generate shell completion scripts.

```
km completion [bash|zsh]
```

**Example:**

```bash
# Bash
source <(km completion bash)

# Zsh
km completion zsh > "${fpath[1]}/_km"
```

---

## Walkthrough: Claude Code in a Sandbox

This walkthrough creates a sandboxed environment where Claude Code can clone a repo, edit files, run tests, and push changes — with network controls, budget limits, and a full audit trail.

### Step 1: Create the Profile

Save this as `claude-code-sandbox.yaml`:

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: claude-code-sandbox
  description: Sandboxed Claude Code agent with repo access and API egress
extends: hardened

spec:
  lifecycle:
    ttl: 8h
    idleTimeout: 2h
    teardownPolicy: destroy

  runtime:
    substrate: ec2
    instanceType: t3.medium
    spot: true
    region: us-east-1

  execution:
    shell: /bin/bash
    workingDir: /workspace
    env:
      ANTHROPIC_API_KEY_REF: "arn:aws:ssm:us-east-1::parameter/sandbox/anthropic-api-key"

  network:
    egress:
      allowedDNSSuffixes:
        - "*.github.com"
        - "*.githubusercontent.com"
        - "*.amazonaws.com"
        - "*.anthropic.com"
        - "*.npmjs.org"
        - "*.pypi.org"
        - "*.golang.org"
      allowedHosts:
        - host: "api.anthropic.com"
          methods: [GET, POST]
        - host: "api.github.com"
          methods: [GET, POST, PUT, PATCH]
        - host: "registry.npmjs.org"
          methods: [GET]
        - host: "pypi.org"
          methods: [GET]
      allowedMethods: [GET, POST, PUT, PATCH]

  sourceAccess:
    mode: allowlist
    github:
      allowedRepos:
        - "github.com/mycompany/*"
      allowedRefs:
        - "main"
        - "develop"
        - "feature/*"
      permissions: [read, write]

  identity:
    roleSessionDuration: 1h
    allowedRegions: [us-east-1]

  secrets:
    allowedRefs:
      - "arn:aws:ssm:us-east-1::parameter/sandbox/anthropic-api-key"
      - "arn:aws:ssm:us-east-1::parameter/sandbox/github-token"

  sidecars:
    dnsProxy: { enabled: true }
    httpProxy: { enabled: true }
    auditLog: { enabled: true }
    tracing: { enabled: true }

  policy:
    allowShellEscape: true
    allowedCommands:
      - git
      - node
      - npm
      - npx
      - python3
      - pip
      - go
      - bash
      - sh
      - curl
      - claude

  artifacts:
    paths:
      - "/workspace/**/*.patch"
      - "/workspace/**/test-results/**"
      - "/workspace/.claude/**"
    maxSizeMB: 50
    replicationRegion: us-west-2

  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch

  agent:
    maxConcurrentTasks: 4
    taskTimeout: 30m
```

### Step 2: Store the Anthropic API Key

Store your API key in SSM Parameter Store so the sandbox can access it without the key appearing in the profile or logs:

```bash
aws ssm put-parameter \
  --name "/sandbox/anthropic-api-key" \
  --type SecureString \
  --value "sk-ant-api03-..." \
  --profile klanker-application
```

### Step 3: Validate and Create

```bash
# Validate the profile
km validate claude-code-sandbox.yaml
# claude-code-sandbox.yaml: valid

# Create the sandbox
km create claude-code-sandbox.yaml
```

Wait for provisioning to complete (~45–90 seconds for spot, longer for on-demand).

### Step 4: Connect via SSM

Get the instance ID from `km status`, then connect:

```bash
# Find the instance
km status sb-7f3a9e12
# Resources (3):
#   - arn:aws:ec2:us-east-1:...:instance/i-0abc123def456

# Connect — no SSH key needed
aws ssm start-session --target i-0abc123def456 --profile klanker-terraform
```

### Step 5: Run Claude Code Inside the Sandbox

Once connected to the sandbox instance:

```bash
# The API key is already injected from SSM
echo $ANTHROPIC_API_KEY | head -c 10
# sk-ant-api

# Clone your repo (allowed by sourceAccess)
cd /workspace
git clone https://github.com/mycompany/api-service.git
cd api-service

# Install Claude Code
npm install -g @anthropic-ai/claude-code

# Run Claude Code — it operates within the sandbox constraints:
#   - Network: can only reach allowlisted hosts
#   - Source: can only push to allowlisted repos/branches
#   - Commands: restricted to allowedCommands list
#   - Audit: every command and network request is logged
claude

# Inside Claude Code, work normally:
#   > Fix the failing test in auth_test.go
#   > Add input validation to the /users endpoint
#   > Run the test suite and commit if green
```

### Step 6: Monitor from Outside

In another terminal, watch the audit trail:

```bash
# Stream audit logs in real time
km logs sb-7f3a9e12 --follow

# Check sandbox status
km status sb-7f3a9e12
```

### Step 7: Collect Artifacts and Destroy

When done, destroy the sandbox. Artifacts are uploaded to S3 automatically:

```bash
km destroy sb-7f3a9e12
# Uploading artifacts...
#   /workspace/api-service/test-results/report.xml (12 KB)
#   /workspace/.claude/session.json (3 KB)
# Destroying infrastructure...
# Sandbox sb-7f3a9e12 destroyed.
```

Artifacts are available in S3:

```bash
aws s3 ls s3://km-sandbox-artifacts-ea554771/artifacts/sb-7f3a9e12/
```

---

## Walkthrough: Goose with Budget Cap

This walkthrough runs Block's [Goose](https://github.com/block/goose) agent with a strict budget ceiling — the sandbox suspends before you overspend.

### Step 1: Create the Profile

Save as `goose-budgeted.yaml`:

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: goose-budgeted
  description: Goose agent with $2 compute + $5 AI budget cap
extends: hardened

spec:
  lifecycle:
    ttl: 12h
    idleTimeout: 1h
    teardownPolicy: destroy

  runtime:
    substrate: ec2
    instanceType: t3.medium
    spot: true
    region: us-east-1

  execution:
    shell: /bin/bash
    workingDir: /workspace
    env:
      GOOSE_PROVIDER: anthropic

  network:
    egress:
      allowedDNSSuffixes:
        - "*.github.com"
        - "*.githubusercontent.com"
        - "*.amazonaws.com"
        - "*.anthropic.com"
        - "*.npmjs.org"
        - "*.pypi.org"
        - "*.golang.org"
        - "*.docker.io"
        - "*.crates.io"
      allowedHosts:
        - host: "api.anthropic.com"
          methods: [GET, POST]
        - host: "api.github.com"
          methods: [GET, POST, PUT, PATCH, DELETE]
        - host: "registry.npmjs.org"
          methods: [GET]
      allowedMethods: [GET, POST, PUT, PATCH, DELETE]

  sourceAccess:
    mode: allowlist
    github:
      allowedRepos:
        - "github.com/mycompany/*"
      allowedRefs: ["*"]
      permissions: [read, write]

  secrets:
    allowedRefs:
      - "arn:aws:ssm:us-east-1::parameter/sandbox/anthropic-api-key"

  sidecars:
    dnsProxy: { enabled: true }
    httpProxy: { enabled: true }
    auditLog: { enabled: true }
    tracing: { enabled: true }

  policy:
    allowShellEscape: true
    allowedCommands:
      - git
      - node
      - npm
      - npx
      - python3
      - pip
      - go
      - cargo
      - bash
      - sh
      - curl
      - goose

  artifacts:
    paths:
      - "/workspace/**/*.patch"
      - "/workspace/**/test-results/**"
      - "/workspace/.goose/**"
    maxSizeMB: 100

  agent:
    maxConcurrentTasks: 4
    taskTimeout: 30m
```

### Step 2: Create and Connect

```bash
km create goose-budgeted.yaml
# sandbox: sb-e4a19c07

aws ssm start-session --target i-0abc123def456 --profile klanker-terraform
```

### Step 3: Run Goose

Inside the sandbox:

```bash
cd /workspace
git clone https://github.com/mycompany/api-service.git
cd api-service

# Install Goose
pipx install goose-ai

# Run Goose — it will use the Anthropic API key from SSM
goose session start
```

Goose runs autonomously — installing dependencies, editing code, running tests. The HTTP proxy meters every Anthropic API call. When the AI budget reaches 80%, you get a warning email. At 100%, the proxy returns 403 for Bedrock/Anthropic calls and the sandbox suspends.

### Step 4: Top Up if Needed

If the sandbox suspends at budget, top it up:

```bash
km budget add sb-e4a19c07 --ai 3.00
# ai budget: $5.00 → $8.00
# proxy: unblocked
# status: running
```

---

## Walkthrough: Security Agent in a Sealed Sandbox

Running red-team or security research agents like [redamon](https://github.com/samugit83/redamon) or [raptor](https://github.com/gadievron/raptor) requires maximum containment.

### Step 1: Use the Sealed Profile (or Create a Custom One)

The built-in `sealed` profile has no egress, no GitHub access, and a 1-hour TTL. For a security agent that needs *some* network access (e.g., to scan an internal target), create a custom profile:

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: security-agent
  description: Contained red-team agent with narrow target scope
extends: hardened

spec:
  lifecycle:
    ttl: 2h
    idleTimeout: 30m
    teardownPolicy: destroy

  runtime:
    substrate: ec2
    instanceType: t3.small
    spot: true
    region: us-east-1

  network:
    egress:
      allowedDNSSuffixes:
        - "*.internal.mycompany.com"
      allowedHosts:
        - host: "target-app.internal.mycompany.com"
          methods: [GET, POST, PUT, DELETE, OPTIONS, HEAD]
      allowedMethods: [GET, POST, PUT, DELETE, OPTIONS, HEAD]

  sidecars:
    dnsProxy: { enabled: true }
    httpProxy: { enabled: true }
    auditLog: { enabled: true }
    tracing: { enabled: true }

  policy:
    allowShellEscape: false
    allowedCommands:
      - python3
      - pip
      - nmap
      - curl
      - nikto

  artifacts:
    paths:
      - "/workspace/reports/**"
      - "/workspace/findings/**"
    maxSizeMB: 200

  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch
```

### Step 2: Run It

```bash
km validate security-agent.yaml
km create security-agent.yaml

# Monitor every command and network request in real time
km logs sb-f1d2e3c4 --follow
```

Every command the agent runs, every DNS query, every HTTP request is logged with timestamps. When the 2h TTL expires, the sandbox self-destructs and reports are uploaded to S3.

---

## Profile Authoring Guide

### Inheritance

Use `extends` to start from a base profile and override specific sections:

```yaml
extends: hardened

spec:
  lifecycle:
    ttl: 4h          # Override parent's 24h TTL
  network:
    egress:
      allowedDNSSuffixes:
        - "*.github.com"    # Replaces (not extends) parent's DNS allowlist
```

**Important:** Child values **replace** parent values — they don't merge. If the parent allows 8 DNS suffixes and you specify 1, the sandbox only gets 1.

### Built-in Profile Hierarchy

```
sealed (most restrictive)
  └── hardened
        └── goose (Goose + Claude Code + Codex, Bedrock)
              ├── goose-ebpf (pure eBPF enforcement)
              └── goose-ebpf-gatekeeper (eBPF + proxy L7 inspection)
codex (OpenAI Codex agent)
agent-orchestrator (multi-agent orchestration)
```

Start from the most restrictive profile that works and open up what you need:

```yaml
# Start hardened, add just what your agent needs
extends: hardened

spec:
  network:
    egress:
      allowedDNSSuffixes:
        - "*.anthropic.com"
        - "*.github.com"
```

### Profile Sections Reference

| Section | Required | Controls |
|---------|----------|----------|
| `lifecycle` | Yes | TTL, idle timeout, teardown policy |
| `runtime` | Yes | Substrate (ec2/ecs), instance type, spot, region |
| `execution` | No | Shell, working directory, env vars |
| `network` | Yes | DNS suffixes, hosts, HTTP methods, `httpsOnly` toggle, `enforcement` mode (proxy/ebpf/both) |
| `sourceAccess` | No | GitHub repos, refs, permissions |
| `identity` | No | IAM session duration, allowed regions |
| `secrets` | No | SSM parameter path allowlist |
| `sidecars` | Yes | DNS proxy, HTTP proxy, audit log, tracing |
| `policy` | No | Shell escape, allowed commands, filesystem policy |
| `observability` | No | Command and network log destinations |
| `agent` | No | Concurrent tasks, timeout, allowed tools |
| `artifacts` | No | Paths to collect on exit, max size, replication region |

### Profile network.httpsOnly

`spec.network.httpsOnly` blocks plain HTTP traffic, allowing only HTTPS. On EC2, this is enforced at the security group layer. On Docker, the HTTP proxy enforces it.

```yaml
spec:
  network:
    httpsOnly: true
    egress:
      allowedDNSSuffixes:
        - ".github.com"
```

When `httpsOnly: true`, any attempt to connect over plain HTTP (port 80) is blocked. This is useful for hardened profiles where all external communication should be encrypted.

### Profile network.enforcement

`spec.network.enforcement` selects the network enforcement mechanism. Three modes are available:

```yaml
spec:
  network:
    enforcement: "ebpf"   # or "proxy" (default) or "both"
```

| Mode | iptables DNAT | DNS Proxy Sidecar | eBPF Enforcer | Best for |
|------|---------------|-------------------|---------------|----------|
| `proxy` (default) | Yes | Yes | No | Backward compatible, all substrates |
| `ebpf` | No | No | Yes (block mode) | Maximum kernel-level security, EC2 only |
| `both` (gatekeeper) | DNS only | No | Yes (block mode) + transparent proxy | Kernel enforcement + L7 inspection, EC2 only |

**eBPF mode** attaches four BPF programs to a sandbox-scoped cgroup. E2E verified on AL2023 kernel 6.18 across 14+ iterations:

- **cgroup/connect4** — intercepts TCP `connect()`. Dual-PID exemption for enforcer and proxy sidecar. Checks destination IP against an LPM trie allowlist. Denied connections get EPERM. Allowed connections to proxy-marked IPs are transparently redirected to the L7 MITM proxy (127.0.0.1:3128) via syscall-level DNAT rewrite.
- **cgroup/sendmsg4** — intercepts UDP `sendmsg()`. Redirects all DNS queries (port 53) to a local resolver that enforces the profile's `allowedDNSSuffixes` and populates the BPF allowlist with resolved IPs.
- **sockops** — tracks TCP connection state. Maps source ports to socket cookies so the transparent proxy can recover the original destination after BPF rewrites.
- **cgroup_skb/egress** — packet-level backstop. Drops any packets to non-allowlisted IPs that slip past the connect4 hook.

The BPF programs and maps are pinned to `/sys/fs/bpf/km/{sandboxID}/` and survive enforcer process restarts. Cleanup happens automatically during `km destroy`.

**Key advantage over proxy mode:** eBPF enforcement runs in the kernel and cannot be bypassed by root users. In proxy mode, a root user can bypass iptables DNAT rules (e.g., via `yum install`). In eBPF mode, the cgroup scope applies to all processes regardless of privilege level.

**Both (gatekeeper) mode:** The eBPF `connect4` hook runs in block mode as the primary enforcer. For hosts that need L7 inspection (GitHub repo filtering, Bedrock token metering), connect4 performs a syscall-level DNAT rewrite to the local transparent proxy. The proxy detects raw TLS connections (first byte `0x16`), recovers the original destination via a BPF map lookup chain (`src_port_to_sock` → `sock_to_original_ip` → `sock_to_original_port`), then applies L7 policy. Non-L7 traffic flows direct — never touches the proxy. E2E verified: allowed repos clone, blocked repos get 403, evil.com gets EPERM at the kernel level.

**Note:** eBPF enforcement is currently EC2-only. Docker and ECS substrates fall back to proxy mode. The cgroup-based approach is designed to extend naturally to EKS pods in future substrates.

For Docker-specific details (local container architecture, credential isolation, troubleshooting), see [Docker Substrate](docker.md).

### eBPF SSL Uprobe Observability

When eBPF enforcement is enabled, uprobes provide **passive TLS plaintext capture** for audit and observability — without MITM certificates. E2E verified on AL2023 with 8 probes attaching to OpenSSL 3.2.2 (`SSL_write`, `SSL_read`, `SSL_write_ex`, `SSL_read_ex`, plus `__sys_connect` and `accept4` kprobes for connection correlation).

| TLS Library | Used By | What's Captured | Status |
|-------------|---------|-----------------|--------|
| OpenSSL (libssl.so.3) | curl, wget, Python, Ruby | Full plaintext (HTTP/1.1 requests/responses) | **E2E verified** |
| Go crypto/tls | Goose, Go agents | Plaintext (requires unstripped binary) | Schema-ready |
| BoringSSL (Bun) | Claude Code | Plaintext (requires per-binary offset discovery) | Schema-ready |

**Use cases:**
- Compliance audit trail independent of proxy logs
- Visibility into TLS traffic that might bypass the proxy
- Git-smart-HTTP capture (clone/push uses HTTP/1.1 — parsed correctly)
- Captures plaintext without requiring sandbox processes to trust a MITM CA certificate

**Limitations:** Uprobes are passive — they observe but cannot block individual requests. Active filtering (GitHub repo blocking, budget 403s) remains in the transparent proxy. HTTP/2 body parsing (GitHub API and Bedrock use HTTP/2 — uprobe captures HPACK binary, not parseable plaintext) and token metering are proxy-only. Uprobes are global (system-wide, not cgroup-scoped) — userspace consumer filters by PID.

---

### Profile spec.email

`spec.email` controls sandbox email behavior -- signed outbound email, verified inbound email, and encrypted messages for inter-sandbox communication.

```yaml
spec:
  email:
    # Ed25519 signing of all outbound emails (required | optional | off)
    signing: required

    # Verify Ed25519 signatures on inbound emails (required | optional | off)
    verifyInbound: required

    # NaCl encryption for outbound emails (required | optional | off)
    encryption: optional

    # Human-friendly alias (e.g. "agent-1" → agent-1@sandboxes.klankermaker.ai)
    alias: "agent-1"

    # Only accept email from these sender addresses
    allowedSenders:
      - "self"
      - "sb-a1b2c3d4@sandboxes.klankermaker.ai"
```

**Fields:**

| Field | Type | Values | Description |
|-------|------|--------|-------------|
| `signing` | string | `required` \| `optional` \| `off` | Controls Ed25519 signing of outbound email. Adds `X-KM-Signature` and `X-KM-Sender-ID` headers |
| `verifyInbound` | string | `required` \| `optional` \| `off` | Controls signature verification on inbound email. `required` rejects unsigned/invalid messages |
| `encryption` | string | `required` \| `optional` \| `off` | Controls NaCl `box.SealAnonymous` encryption for outbound email |
| `alias` | string | | Human-friendly alias. Must be lowercase dot-notation (e.g. `my-agent`). Resolved to `{alias}@sandboxes.klankermaker.ai`. Stored in the `km-identities` GSI for lookup |
| `allowedSenders` | list | `["self"]`, `["*"]`, `["sb-abc"]`, `["build.*"]` | Allow-list of which sandboxes may send to this sandbox. Empty means unrestricted |

**How signing works:** The sandbox Ed25519 private key is stored encrypted in KMS. When sending email, the signing sidecar calls KMS to decrypt the key, signs the message body, and adds `X-KM-Signature: {base64}` and `X-KM-Sender-ID: {sandbox-id}` to the headers. The recipient retrieves the sender's public key from `km-identities` by `sandbox_id` (or alias) to verify.

**How encryption works:** NaCl `box.SealAnonymous` is used — the sender does not need a private key for encryption, only the recipient's public key from `km-identities`. The sender's identity is carried in `X-KM-Sender-ID`, not the ciphertext. SES `Content.Raw` is used for sending (not `Content.Simple`) to preserve custom headers through the SES delivery pipeline.

See [Multi-Agent Email](multi-agent-email.md) for the full protocol, SES receipt rules, and multi-sandbox orchestration patterns.

---

### Profile sourceAccess.github

`sourceAccess.github` gives sandboxes access to private GitHub repositories using short-lived GitHub App installation tokens. No long-lived credentials are stored in the sandbox.

```yaml
spec:
  sourceAccess:
    mode: allowlist
    github:
      allowedRepos:
        - "github.com/mycompany/api-service"
        - "github.com/mycompany/*"
      allowedRefs:
        - "main"
        - "feature/*"
      permissions:
        - read
        - write
```

**Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `mode` | `allowlist` | Must be `allowlist` — sandboxes only get access to explicitly listed repos |
| `github.allowedRepos` | list | Repository patterns the sandbox can access. Supports `*` wildcard within an org |
| `github.allowedRefs` | list | Branch/tag patterns the sandbox can push to |
| `github.permissions` | list | `read` (clone), `write` (push), or both |

**How it works:**

When `km create` runs with a profile that has `sourceAccess.github`, it provisions:

1. A per-sandbox Lambda (`km-github-token-refresher-{sandbox-id}`) that generates a GitHub App installation token scoped to `allowedRepos` and `permissions`
2. An EventBridge Scheduler schedule that fires the Lambda every 45 minutes
3. An SSM SecureString parameter at `/sandbox/{sandbox-id}/github-token` (KMS-encrypted) that holds the current token

Inside the sandbox, `GIT_ASKPASS` is set to a helper script that reads the token from SSM at git time. The token is never exposed in environment variables or process listings.

**Prerequisites:** `km configure github` must be run first to store the GitHub App credentials in SSM. See the [km configure github](#km-configure-github) section.

**Token scoping:** The installation token is generated via `POST /app/installations/{id}/access_tokens` with a `repositories` body scoped to the sandbox's `allowedRepos`. GitHub enforces repo scope at the API level — the token cannot access repos outside the allowlist even if the installation covers more repos.

**Cleanup:** The token refresh Lambda and EventBridge schedule are destroyed when the sandbox is destroyed.

---

## Lifecycle and Teardown

### TTL Auto-Destroy

When a sandbox is created with `lifecycle.ttl`, an EventBridge Scheduler schedule fires at expiry time. The TTL handler Lambda:

1. Downloads the sandbox profile from S3
2. Uploads artifacts (if configured)
3. Sends a "ttl-expired" lifecycle notification email
4. Deletes the EventBridge schedule (self-cleanup)
5. Triggers sandbox teardown

### Idle Timeout

If `lifecycle.idleTimeout` is set, the system polls CloudWatch for the last audit log event. If no activity for the configured duration, teardown begins.

### Teardown Policies

| Policy | Behavior |
|--------|----------|
| `destroy` | Full deprovisioning — all AWS resources removed (default) |
| `stop` | Halt compute (EC2: StopInstances; ECS: StopTask) — state preserved |
| `retain` | Log intent only — operator must destroy manually |

### Spot Interruption

When AWS reclaims a spot instance (2-minute warning):
- **EC2**: Instance metadata signals interruption; bootstrap handler uploads artifacts to S3
- **ECS Fargate**: EventBridge rule detects `stopCode=SpotInterruption`; Lambda triggers artifact upload via ECS Exec

Artifacts are preserved even on interruption.

### Lifecycle Email Notifications

If `KM_OPERATOR_EMAIL` is set, you receive emails for:
- Sandbox created
- Sandbox destroyed (normal, TTL, idle, budget)
- Spot interruption (with artifact upload status)
- Budget warnings (80% threshold)
- Errors during teardown

---

## Troubleshooting

### "network not initialized for region"

```
Error: network not initialized for region us-east-1 — run 'km init --region <region>' first
```

Run `km init --region us-east-1` before creating sandboxes in that region.

### AWS Credential Errors

```bash
# Login to SSO
aws sso login --profile klanker-terraform

# Verify credentials work
aws sts get-caller-identity --profile klanker-terraform
```

### Sandbox Not Found on Destroy

```
Error: sandbox sb-7f3a9e12 not found: no AWS resources tagged with km:sandbox-id=sb-7f3a9e12
```

The sandbox may have already been destroyed (TTL or idle timeout). Check CloudWatch Logs for the lifecycle event:

```bash
aws logs filter-log-events \
  --log-group-name /km/sandboxes/sb-7f3a9e12/ \
  --profile klanker-terraform
```

### Spot Instance Not Terminating

If `km destroy` fails partway through, the spot instance may still be running. Terminate it manually:

```bash
# Find the instance
aws ec2 describe-instances \
  --filters "Name=tag:km:sandbox-id,Values=sb-7f3a9e12" \
  --query 'Reservations[].Instances[].InstanceId' \
  --profile klanker-terraform

# Terminate it
aws ec2 terminate-instances --instance-ids i-0abc123def456 --profile klanker-terraform
```

### DNS or HTTP Blocked Unexpectedly

Check the sidecar logs:

```bash
# DNS proxy logs
km logs sb-7f3a9e12 --stream dns-proxy

# HTTP proxy logs
km logs sb-7f3a9e12 --stream http-proxy
```

Look for `BLOCK` entries to see what domain or host was denied, then add it to the profile's allowlist.

### Agent Can't Reach a Host

The host must be allowlisted at **both** layers:

1. `network.egress.allowedDNSSuffixes` — so the DNS proxy resolves the name
2. `network.egress.allowedHosts` — so the HTTP proxy allows the connection

Missing either one blocks the request.
