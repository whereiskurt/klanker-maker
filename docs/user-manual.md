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
  - [km list](#km-list)
  - [km status](#km-status)
  - [km logs](#km-logs)
- [Walkthrough: Claude Code in a Sandbox](#walkthrough-claude-code-in-a-sandbox)
- [Walkthrough: Goose with Budget Cap](#walkthrough-goose-with-budget-cap)
- [Walkthrough: Security Agent in a Sealed Sandbox](#walkthrough-security-agent-in-a-sealed-sandbox)
- [Profile Authoring Guide](#profile-authoring-guide)
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

Initialize shared regional infrastructure (VPC, subnets, security groups). **Run once per region** before creating sandboxes.

```
km init [--region <region>] [--aws-profile <profile>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--region` | `us-east-1` | AWS region to initialize |
| `--aws-profile` | `klanker-application` | AWS CLI profile for provisioning |

**What it does:**

1. Creates the region directory structure under `infra/live/`
2. Writes `region.hcl` with region label mapping (e.g., `us-east-1` → `use1`)
3. Copies the network Terragrunt template
4. Runs `terragrunt apply` to provision VPC, subnets, and security groups
5. Captures network outputs (VPC ID, subnet IDs, AZs) to `outputs.json`

**Example:**

```bash
# Initialize us-east-1
km init --region us-east-1

# Initialize a second region
km init --region us-west-2
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
km validate profiles/restricted-dev.yaml
# restricted-dev.yaml: valid

# Validate multiple profiles
km validate profiles/*.yaml
# open-dev.yaml: valid
# restricted-dev.yaml: valid
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
km create <profile.yaml> [--on-demand] [--aws-profile <profile>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--on-demand` | `false` | Override `spot: true` in profile; use on-demand instances |
| `--aws-profile` | `klanker-terraform` | AWS CLI profile for provisioning |

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
km create profiles/restricted-dev.yaml

# Create with on-demand instances (no spot)
km create profiles/open-dev.yaml --on-demand

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

Tear down a sandbox and clean up all resources.

```
km destroy <sandbox-id> [--aws-profile <profile>] [--force]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--aws-profile` | `klanker-terraform` | AWS CLI profile |
| `--force` | `false` | Skip confirmation prompt (reserved for future use) |

**What it does:**

1. Validates sandbox ID format (`sb-[a-f0-9]{8}`)
2. Discovers sandbox resources via AWS tag scan (`km:sandbox-id`)
3. For EC2 spot: explicitly terminates the instance first (critical — `terraform destroy` alone leaves spot instances running)
4. Cancels EventBridge TTL schedule
5. Uploads artifacts (if configured in profile)
6. Runs `terragrunt destroy -auto-approve`
7. Sends lifecycle notification
8. Cleans up local sandbox directory and SES email identity

**Example:**

```bash
km destroy sb-7f3a9e12
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

### km list

List all running sandboxes.

```
km list [--json] [--tags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | `false` | Output as JSON array |
| `--tags` | `false` | Use AWS tag scan instead of S3 state scan (slower, more reliable) |

**Example:**

```bash
km list
```

**Output:**

```
SANDBOX ID    PROFILE          SUBSTRATE  REGION      STATUS   TTL
sb-7f3a9e12   restricted-dev   ec2        us-east-1   running  5h46m
sb-a4c8e210   hardened         ecs        us-east-1   running  2h12m
sb-d9f01b3c   open-dev         ec2        us-west-2   running  18h33m
```

```bash
km list --json
```

```json
[
  {
    "sandbox_id": "sb-7f3a9e12",
    "profile": "restricted-dev",
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
Profile:     restricted-dev
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
extends: restricted-dev

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
extends: open-dev

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
extends: open-dev

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
        └── restricted-dev
              └── open-dev (most permissive)
```

Start from the most restrictive profile that works and open up what you need:

```yaml
# Start hardened, add just what Claude Code needs
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
| `network` | Yes | DNS suffixes, hosts, HTTP methods |
| `sourceAccess` | No | GitHub repos, refs, permissions |
| `identity` | No | IAM session duration, allowed regions |
| `secrets` | No | SSM parameter path allowlist |
| `sidecars` | Yes | DNS proxy, HTTP proxy, audit log, tracing |
| `policy` | No | Shell escape, allowed commands, filesystem policy |
| `observability` | No | Command and network log destinations |
| `agent` | No | Concurrent tasks, timeout, allowed tools |
| `artifacts` | No | Paths to collect on exit, max size, replication region |

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
