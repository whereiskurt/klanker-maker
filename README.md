# Klanker Maker (km)

<p align="center">
  <img src="docs/klankerdome-480-12-sierra.gif" alt="Klanker Maker — robots working inside a sandboxed dome" width="480" />
  <br />
  <sub>Art by Mike Wigmore (<a href="https://github.com/mikewigmore">@mikewigmore</a>)</sub>
</p>

**Define a sandbox policy. Set a budget. Let your klankers run.**

Klanker Maker is an open-source platform that turns declarative YAML profiles into budget-capped, policy-locked AWS sandboxes. Every sandbox gets its own Security Group boundary, IAM role, network allowlists, and a dollar ceiling — when the budget runs out, the sandbox stops. No surprises on your AWS bill.

The idea is simple: you shouldn't have to choose between giving AI agents real infrastructure access and keeping your AWS account safe. Define what an agent is allowed to do, how much it can spend, and walk away.

```
$ km create restricted-dev.yaml
  sandbox: sb-a1b2c3d4
  substrate: ec2 (spot)
  budget: $2.00 compute / $5.00 AI
  ttl: 8h
  egress: 6 hosts allowlisted
  ready in 47s

$ km status sb-a1b2c3d4
  compute: $0.12 / $2.00  (6%)
  ai:      $1.40 / $5.00  (28%)
  uptime:  2h 14m / 8h TTL
  spot:    running (us-east-1b)
```

## Why This Exists

The agent ecosystem is exploding. My GitHub stars tell the story — in the last few months alone I've starred:

**Coding agents** that need real compute, real network, and real credentials:
- [Goose](https://github.com/block/goose) — Block's autonomous agent that installs deps, edits files, runs tests, orchestrates workflows
- [Aider](https://github.com/Aider-AI/aider) — AI pair programming in your terminal with automatic git commits
- [OpenDev](https://github.com/opendev-to/opendev) — open-source coding agent in the terminal
- [open-swe](https://github.com/langchain-ai/open-swe) — LangChain's asynchronous coding agent
- [DeepCode](https://github.com/HKUDS/DeepCode) — agentic coding for Paper2Code, Text2Web, Text2Backend
- [deepagents](https://github.com/langchain-ai/deepagents) — LangGraph harness with planning, filesystem, and sub-agent spawning

**Multi-agent orchestrators** that spawn fleets of workers:
- [agent-orchestrator](https://github.com/ComposioHQ/agent-orchestrator) — parallel coding agents with autonomous CI fixes and code reviews
- [nanoclaw](https://github.com/qwibitai/nanoclaw) — lightweight agent on Anthropic's Agent SDK, runs in containers
- [openclaw](https://github.com/openclaw/openclaw) — personal AI assistant across platforms
- [pi-mono](https://github.com/badlogic/pi-mono) — coding agent CLI, unified LLM API, Slack bot, vLLM pods
- [gobii-platform](https://github.com/gobii-ai/gobii-platform) — always-on AI workforce
- [autoresearch](https://github.com/karpathy/autoresearch) — Karpathy's agents running research on single-GPU training automatically

**Security and red-team agents** that *definitely* need containment:
- [redamon](https://github.com/samugit83/redamon) — AI-powered red team framework, recon to exploitation, zero human intervention
- [raptor](https://github.com/gadievron/raptor) — turns Claude Code into an offensive/defensive security agent
- [hexstrike-ai](https://github.com/0x4m4/hexstrike-ai) — 150+ cybersecurity tools orchestrated by AI agents
- [strix](https://github.com/usestrix/strix) — open-source AI hackers that find and fix vulnerabilities
- [shannon](https://github.com/KeygraphHQ/shannon) — autonomous white-box AI pentester for web apps and APIs
- [EVA](https://github.com/ARCANGEL0/EVA) — AI-assisted penetration testing agent

**Sandbox platforms** solving the same problem from different angles:
- [agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) — Kubernetes SIG for isolated agent runtimes
- [E2B](https://github.com/e2b-dev/E2B) — secure cloud environments for enterprise agents
- [OpenSandbox](https://github.com/alibaba/OpenSandbox) — Alibaba's sandbox platform with Docker/K8s runtimes
- [void-box](https://github.com/the-void-ia/void-box) — composable agent runtime with enforced isolation
- [monty](https://github.com/pydantic/monty) — Pydantic's minimal, secure Python interpreter in Rust for AI

Every one of these projects needs *somewhere safe to run*. The common pattern is either "trust the agent" (bad) or "containerize it locally" (insufficient — no real cloud resources, no real credentials, no real network). What's missing is **cloud-native physical isolation** — a real VPC, real IAM boundaries, real network controls, with a budget ceiling that prevents a $10 experiment from becoming a $10,000 AWS bill.

That's what Klanker Maker builds. The sandbox is a **compiled policy object** — you declare the constraints and the infrastructure is the compiled artifact:

- **Budget ceiling** — set a dollar cap for compute and AI API spend per sandbox. At 80% you get a warning email. At 100% the sandbox is suspended, not destroyed — top it up and resume.
- **Network allowlists** — DNS and HTTP proxies enforce which hosts the agent can reach. Everything else is blocked at the proxy layer *and* at the Security Group layer. Two walls, not one.
- **Scoped identity** — each sandbox gets its own IAM role session, region-locked, time-limited, with only the permissions the profile declares.
- **Automatic lifecycle** — TTL auto-destroy, idle timeout, artifact upload on exit (including on spot interruption), and email notifications for every lifecycle event.
- **Spot-first economics** — EC2 Spot and Fargate Spot by default. A `t3.medium` spot instance in `us-east-1` costs ~$0.01/hr. Run 10 agent sandboxes for a full workday for under $1 in compute.

The difference between Klanker Maker and the other sandbox platforms: this is **pure AWS infrastructure** — no orchestration layer to trust, no shared runtime, no container escape surface. Each region has a shared VPC (provisioned once by `km init`), and every sandbox gets its own Security Groups, IAM role, and sidecar enforcement. The isolation is at the network policy and identity layer, backed by real AWS primitives.

## AWS Account Architecture

Klanker Maker uses a **three-account model** following AWS Organizations best practices. Sandboxes run in a dedicated application account — completely separated from the account that provisions them and the account that owns the domain.

<p align="center">
  <img src="docs/frame1-security-network.svg" alt="Security & Network Architecture — 3 accounts, shared VPC, per-sandbox Security Groups" />
</p>

### Why Three Accounts?

| Account | Role | What Lives Here | Why Separate |
|---------|------|----------------|--------------|
| **Management** | DNS, identity, org root | Route53 hosted zone, domain registration, AWS SSO, Organizations root | Domain and identity are org-wide — they don't belong in a sandbox blast radius |
| **Terraform** | State and provisioning | S3 state buckets, DynamoDB lock tables, cross-account provisioning role | Terraform state contains every resource ARN and secret path — isolating it limits exposure if the application account is compromised |
| **Application** | Sandbox execution | Regional VPCs, EC2/ECS instances, IAM sandbox roles, SES, Lambda handlers, DynamoDB budget table, S3 artifacts, CloudWatch Logs | This is where agents run — if an agent escapes its sandbox, it can only reach resources in this account, not state or DNS |

The operator authenticates via **AWS SSO** with named profiles:

```bash
# Authenticate to all three accounts
aws sso login --profile klanker-management     # DNS, domain
aws sso login --profile klanker-terraform      # State, provisioning
aws sso login --profile klanker-application    # VPC/network init
```

The `km` CLI selects the right profile per command:

| Command | AWS Profile | Account |
|---------|-------------|---------|
| `km init` | `klanker-application` | Application — provisions shared VPC/network |
| `km create` | `klanker-terraform` | Terraform — assumes role into Application to provision |
| `km destroy` | `klanker-terraform` | Terraform — assumes role into Application to teardown |
| `km list` | `klanker-terraform` | Terraform — reads state from S3 |
| `km status` | `klanker-terraform` | Terraform — reads state + discovers resources |
| `km logs` | `klanker-terraform` | Terraform — reads CloudWatch Logs |

### Platform Configuration

Klanker Maker is forkable. All platform-specific values — domain, account IDs, SSO URL, region preferences — are configurable via `km configure`:

```bash
km configure
  Domain:                 mysandboxes.example.com
  Management account ID:  111111111111
  Terraform account ID:   222222222222
  Application account ID: 333333333333
  SSO start URL:          https://myorg.awsapps.com/start
  Primary region:         us-east-1
```

No hardcoded account IDs. No hardcoded domains. A fork with a different domain works end-to-end after `km configure`.

## Security Model

Klanker Maker uses **explicit allowlists everywhere** — if it's not in the policy, it's denied. There is no "default allow."

### No SSH. No Bastion. No Keys.

Sandboxes are accessed exclusively through **AWS SSM Session Manager**:

- **Zero open inbound ports** — Security Groups have no SSH ingress rules. Port 22 doesn't exist.
- **No SSH keys to manage** — no generation, rotation, distribution, or leaked keys on GitHub.
- **IAM-gated access** — who can connect is controlled by IAM policy, not by who has a `.pem` file.
- **Full session audit** — every session and every command is logged to CloudTrail and CloudWatch. There is no "off the record."
- **No bastion hosts** — no jump boxes, no VPN. SSM connects through the agent, even in private subnets with no internet access.

### Defense in Depth

| Layer | Control | Enforcement |
|-------|---------|-------------|
| **Account** | Three-account isolation | Sandbox blast radius limited to Application account; state and DNS unreachable |
| **Network** | VPC Security Groups | Primary boundary — blocks all egress except proxy paths |
| **DNS** | DNS proxy sidecar | Allowlisted suffixes only; non-matching → NXDOMAIN |
| **HTTP** | HTTP proxy sidecar | Allowlisted hosts only; non-matching → 403 Forbidden |
| **Identity** | Scoped IAM sessions | Region-locked, time-limited, minimal permissions |
| **Secrets** | SSM Parameter Store + KMS | Allowlisted refs only; per-sandbox encryption key with auto-rotation |
| **Metadata** | IMDSv2 enforced | Token-required; blocks SSRF credential theft via instance metadata |
| **Source** | GitHub repo allowlist | Per-repo, per-ref, per-permission (clone/fetch/push) |
| **Filesystem** | Path-level enforcement | Writable vs read-only directories at OS level |
| **Audit** | Command + network logging | Secret-redacted; delivered to CloudWatch/S3 |
| **Budget** | Compute + AI spend tracking | DynamoDB real-time metering; proxy 403 + IAM revocation at ceiling |

### Architecture Diagrams

Editable source: [`docs/sandbox-architecture.excalidraw`](docs/sandbox-architecture.excalidraw) — open in [excalidraw.com](https://excalidraw.com) or the VS Code Excalidraw extension.

## How It Works

<p align="center">
  <img src="docs/frame3-lifecycle-pipeline.svg" alt="Sandbox Lifecycle & Pipeline — configure through destroy, automatic exit triggers" />
</p>

1. **Configure** with `km configure` — set your domain, account IDs, SSO URL, region (once)
2. **Bootstrap** with `km bootstrap` — creates S3 state buckets, DynamoDB lock tables, and KMS keys for your configured regions (once)
3. **Initialize** with `km init --region us-east-1` — provisions shared VPC and network (once per region)
4. **Define** a SandboxProfile in YAML — budget, lifecycle, network policy, identity, sidecars
5. **Validate** with `km validate <profile.yaml>`
6. **Create** with `km create <profile>` — compiles to Terragrunt inputs, provisions infrastructure
7. **Monitor** with `km list` / `km status` or the ConfigUI web dashboard
8. **Destroy** with `km destroy <sandbox-id>` — clean teardown of all resources

## SandboxProfile

Profiles use a Kubernetes-style schema at `klankermaker.ai/v1alpha1`. Here's what a realistic agent sandbox looks like:

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: restricted-dev
  description: Budget-capped development sandbox with network filtering
extends: open-dev

spec:
  lifecycle:
    ttl: 8h
    idleTimeout: 2h
    teardownPolicy: destroy

  runtime:
    substrate: ec2
    instanceType: t3.medium
    spot: true

  budget:
    compute:
      maxSpendUSD: 2.00
    ai:
      maxSpendUSD: 5.00
    warningThreshold: 0.80

  network:
    egress:
      allowedDNSSuffixes:
        - "*.github.com"
        - "*.amazonaws.com"
      allowedHosts:
        - host: "api.openai.com"
          methods: [GET, POST]
        - host: "api.anthropic.com"
          methods: [GET, POST]

  identity:
    sessionDuration: 1h
    regionLock: [us-east-1]

  sourceAccess:
    github:
      allowedRepos:
        - org: "mycompany"
          repo: "*"
          refs: ["main", "develop"]
          permissions: [clone, fetch, push]

  secrets:
    allowedRefs:
      - "arn:aws:ssm:us-east-1::parameter/sandbox/api-key"

  sidecars:
    dnsProxy: { enabled: true }
    httpProxy: { enabled: true }
    auditLog: { enabled: true }
    tracing: { enabled: true }

  artifacts:
    paths: ["/workspace/output/**"]
    maxSizeMB: 100
    replicationRegion: us-west-2

  observability:
    logDestination: cloudwatch
    commandLog: true
    networkLog: true
```

### Built-in Profiles

| Profile | TTL | Network | Budget | Use Case |
|---------|-----|---------|--------|----------|
| `open-dev` | 24h | Broad allowlist (npm, PyPI, Docker Hub, GitHub) | Operator-set | General development, prototyping |
| `restricted-dev` | 8h | Narrow allowlist, GET/POST only | Operator-set | Agent coding tasks with audit trail |
| `hardened` | 4h | AWS services only | Operator-set | Production-adjacent testing |
| `sealed` | 1h | No egress | Operator-set | Offline analysis, air-gapped execution |

Profiles support **inheritance** via `extends` — start from a base and override what you need.

## Running Agents in Sandboxes

Klanker Maker is workload-agnostic — any agent that runs on Linux works inside a sandbox. Here's how the controls map to real agent workloads:

| Agent | What It Does | Which Controls Matter |
|-------|-------------|----------------------|
| [Goose](https://github.com/block/goose) | Installs deps, edits files, runs tests, orchestrates workflows | **Budget cap** — prevents runaway Bedrock/API costs when Goose loops |
| [Aider](https://github.com/Aider-AI/aider) | AI pair programming with auto git commits | **Source access** — controls which repos it can push to |
| [agent-orchestrator](https://github.com/ComposioHQ/agent-orchestrator) | Spawns parallel coding agents, handles CI fixes autonomously | **Budget + TTL** — caps fleet cost; each spawned worker inherits the sandbox ceiling |
| [deepagents](https://github.com/langchain-ai/deepagents) | Planning + filesystem + sub-agent spawning via LangGraph | **Network allowlist** — limits where sub-agents can reach |
| [open-swe](https://github.com/langchain-ai/open-swe) | Async coding agent that clones, patches, and PRs | **Source access** — allowlist repos + refs; block push to protected branches |
| [redamon](https://github.com/samugit83/redamon) | Automated red team: recon → exploitation → post-exploitation | **Sealed profile** — air-gapped, no egress, full audit trail |
| [raptor](https://github.com/gadievron/raptor) | Claude Code as an offensive security agent | **Hardened profile** — minimal egress, short TTL, every command logged |
| [autoresearch](https://github.com/karpathy/autoresearch) | Agents running research on GPU training | **Compute budget** — prevents a runaway training loop from burning hours of GPU |
| [nanoclaw](https://github.com/qwibitai/nanoclaw) | Anthropic Agent SDK agent connected to messaging apps | **HTTP proxy** — controls which external APIs the agent can call |
| [gobii-platform](https://github.com/gobii-ai/gobii-platform) | Always-on AI workforce | **Idle timeout** — shuts down workers that stop producing; artifact upload preserves state |

### Multi-Agent Orchestration via Email

Sandboxes can communicate through email (SES). Each sandbox gets a unique address derived from its ID (e.g., `sb-a1b2c3d4@sandboxes.klankermaker.ai`). An agent in one sandbox can trigger work in another by sending a structured email — enabling multi-agent pipelines without shared network access. This is how you build agent fleets where each worker is physically isolated but logically connected.

## Substrates

| Substrate | How It Works | Cost |
|-----------|-------------|------|
| **EC2 Spot** (default) | Shared regional VPC, per-sandbox SG, spot instance, SSM access, sidecar systemd services | ~$0.01/hr for t3.medium |
| **EC2 On-Demand** | Same as above, guaranteed capacity | ~$0.04/hr for t3.medium |
| **ECS Fargate Spot** | Fargate task with sidecar containers, service discovery | ~$0.01/hr for 1 vCPU / 2GB |
| **ECS Fargate** | Same as above, guaranteed capacity | ~$0.04/hr for 1 vCPU / 2GB |

Spot interruption handlers automatically upload artifacts to S3 before instances are reclaimed.

## Budget Enforcement

Budget enforcement tracks two spend pools per sandbox, stored in a **DynamoDB global table** replicated to every region where agents run. Reads from within the sandbox hit the local regional replica with sub-millisecond latency.

<p align="center">
  <img src="docs/frame2-budget-enforcement.svg" alt="Budget Enforcement Flow — proxy metering, DynamoDB tracking, dual-layer enforcement" />
</p>

### Compute Budget

Tracked as spot rate x elapsed minutes, sourced from the AWS Price List API at sandbox creation. When the compute budget is exhausted, the sandbox is *suspended* — not destroyed:

- **EC2**: `StopInstances` preserves the EBS volume. No compute charges accrue while stopped.
- **ECS Fargate**: Artifacts are uploaded, then the task is stopped. Re-provision from the stored S3 profile on top-up.

### AI Budget (Bedrock)

The HTTP proxy sidecar intercepts every Bedrock `InvokeModel` response, extracts `input_tokens` and `output_tokens`, prices them against cached model rates (Haiku, Sonnet, Opus), and atomically increments the DynamoDB spend counter.

**Dual-layer enforcement** at 100%:

1. **Proxy layer** (immediate) — HTTP proxy returns 403 for subsequent Bedrock calls
2. **IAM layer** (backstop) — a Lambda revokes the sandbox IAM role's Bedrock permissions, catching SDK/CLI calls that bypass the proxy

`km status` shows per-model AI spend breakdown:

```
$ km status sb-a1b2c3d4
  ...
  budget:
    compute:  $0.12 / $2.00  (6%)
    ai:       $1.40 / $5.00  (28%)
      haiku:    $0.20  (142K in / 58K out)
      sonnet:   $1.20  (89K in / 34K out)
```

### Warnings and Top-Up

At 80% (configurable via `spec.budget.warningThreshold`) of either pool, the operator receives an email via SES.

```
$ km budget add sb-a1b2c3d4 --ai 3.00
  ai budget: $5.00 → $8.00
  proxy: unblocked
  iam: restored
  status: running
```

Top-up unblocks the proxy, restores IAM permissions, and restarts suspended compute — all in one command.

## Architecture

```
km CLI / ConfigUI
├── cmd/km/                  CLI entry point
├── cmd/configui/            Web dashboard (Go + embedded HTML)
├── cmd/ttl-handler/         Lambda: TTL expiry + artifact upload
├── internal/app/cmd/        Cobra commands (create, destroy, list, validate, status, logs)
├── internal/app/config/     Configuration (config.yaml, env vars, CLI flags)
├── pkg/
│   ├── profile/             SandboxProfile schema, validation, inheritance
│   ├── compiler/            Profile → Terragrunt artifacts (EC2 + ECS paths)
│   ├── aws/                 SDK helpers (S3, SES, CloudWatch, EC2 metadata, DynamoDB)
│   ├── terragrunt/          Runner + per-sandbox state isolation
│   └── lifecycle/           TTL scheduling, idle detection, teardown
├── sidecars/
│   ├── dns-proxy/           DNS allowlist filter (UDP/TCP:53)
│   ├── http-proxy/          HTTP allowlist filter (TCP:3128) + Bedrock token metering
│   ├── audit-log/           Command + network log router with secret redaction
│   └── tracing/             OTel trace collection + MLflow experiment logging
├── profiles/                Built-in YAML profiles (open-dev → sealed)
└── infra/
    ├── modules/             Terraform modules
    │   ├── network/         VPC, subnets, security groups
    │   ├── ec2spot/         Spot + on-demand instances, IMDSv2, IAM
    │   ├── ecs-cluster/     ECS cluster, Fargate Spot capacity provider
    │   ├── ecs-task/        Task definitions with sidecar containers
    │   ├── ecs-service/     Service deployment + service discovery
    │   ├── ecs-spot-handler/  Lambda: Fargate Spot interruption → artifact upload
    │   ├── secrets/         SSM Parameter Store + KMS encryption
    │   ├── ses/             SES domain, DKIM, inbound email → S3
    │   ├── s3-replication/  Cross-region artifact replication
    │   └── ttl-handler/     Lambda: TTL expiry → artifacts + email + self-cleanup
    └── live/                Terragrunt hierarchy (site.hcl, per-sandbox isolation)
```

## Quick Start

```bash
# Install
go install github.com/whereiskurt/klankrmkr/cmd/km@latest

# Configure your platform (once)
km configure

# Bootstrap state backends and KMS (once)
km bootstrap

# Initialize the region (once per region)
km init --region us-east-1

# Validate a profile
km validate profiles/restricted-dev.yaml

# Create a sandbox
km create profiles/restricted-dev.yaml

# Check status
km list
km status sb-a1b2c3d4

# Connect (via SSM — no SSH, no keys)
aws ssm start-session --target i-0abc123def456

# Destroy
km destroy sb-a1b2c3d4
```

## Documentation

| Document | Description |
|----------|-------------|
| [User Manual](docs/user-manual.md) | Full command reference, walkthroughs (Claude Code, Goose, security agents), profile authoring |
| [Operator Guide](docs/operator-guide.md) | AWS account setup, KMS, S3, SES, Lambda deployment — everything before `km init` |
| [Profile Reference](docs/profile-reference.md) | Complete YAML schema with every field, type, default, and validation rule |
| [Security Model](docs/security-model.md) | Deep dive on each security layer, from VPC to IMDSv2 to secret redaction |
| [Budget Guide](docs/budget-guide.md) | DynamoDB schema, proxy metering, enforcement flow, threshold configuration |
| [Sidecar Reference](docs/sidecar-reference.md) | Each sidecar's config, env vars, log formats, EC2 vs ECS deployment |
| [Multi-Agent Email](docs/multi-agent-email.md) | SES setup, sandbox addressing, cross-sandbox orchestration patterns |
| [ConfigUI Guide](docs/configui-guide.md) | Web dashboard setup, profile editor, secrets management |

## Roadmap

| Phase | Description | Status |
|-------|-------------|--------|
| 1 | Schema, Compiler & AWS Foundation | In Progress |
| 2 | Core Provisioning & Security Baseline | **Complete** |
| 3 | Sidecar Enforcement & Lifecycle Management | In Progress |
| 4 | Lifecycle Hardening, Artifacts & Email | In Progress |
| 5 | ConfigUI Web Dashboard | In Progress |
| 6 | Budget Enforcement & Platform Configuration | Planned |

See [.planning/ROADMAP.md](.planning/ROADMAP.md) for detailed phase breakdowns and success criteria.

## License

TBD
