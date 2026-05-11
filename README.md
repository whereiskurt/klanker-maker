# Klanker Maker (km)

**An agent runtime on your own AWS account - declarative, eBPF-enforced, Slack-native, with hard budgets that actually stop runaway loops.**

Klanker Maker compiles a YAML profile into a real AWS sandbox: a scoped IAM role, a kernel-level network policy, a MITM proxy that meters every Bedrock/Anthropic/OpenAI token, a Slack channel that talks back to the agent, and a dollar ceiling that suspends compute when the money runs out. The point is to take agentic work off your laptop and put it on AWS at the size the work actually needs - a `t3.medium` for a quick fix, an `r7i.48xlarge` against EFS-backed datasets for a multi-day data pipeline, a GPU box for a training loop, or a crew of Claudes coordinating across all of the above. Drive any of it from a CLI, an `at` schedule, an inbound email, or a Slack thread - same control plane, same guardrails.

<p align="center">
  <img src="docs/klankerdome-dark.gif" alt="Klanker Maker - robots working inside a sandboxed dome" width="480" />
  <br />
  <sub>Art by Mike Wigmore (<a href="https://github.com/mikewigmore">@mikewigmore</a>)</sub>
</p>

A profile is the contract - declare what's allowed, get the infrastructure as the artifact:

```yaml
spec:
  network:
    enforcement: both          # eBPF connect4 + transparent MITM proxy
    egress:
      allowedDNSSuffixes: [.amazonaws.com, .anthropic.com, .github.com]
  budget:
    compute: { maxSpendUSD: 0.50 }
    ai:      { maxSpendUSD: 1.00 }
  sourceAccess:
    mode: allowlist
    github:
      allowedRepos: [my-org/api, my-org/infra]
      allowedRefs:  [main, "feature/*"]
  cli:
    notifySlackEnabled: true
    notifySlackPerSandbox: true
    notifySlackInboundEnabled: true       # bidirectional chat
    notifySlackTranscriptEnabled: true    # per-turn streaming + JSONL upload
```

```bash
$ ./km create profiles/g1.yaml
$ ./km list --wide
$ ./km agent run g1 --prompt "investigate the OOM in api-server" --wait
$ ./km destroy g1 --yes
```

---

## Table of Contents

- [What Klanker Maker Is](#what-klanker-maker-is)
- [How It Compares](#how-it-compares)
- [Quick Start](#quick-start)
- [Why This Exists](#why-this-exists)
- [Core Capabilities](#core-capabilities)
- [Cloud-Native Control Plane](#cloud-native-control-plane)
- [Slack-Native Operations](#slack-native-operations)
- [GitHub App Integration](#github-app-integration)
- [Multi-Agent Orchestration via Signed Email](#multi-agent-orchestration-via-signed-email)
- [AWS Account Architecture](#aws-account-architecture)
- [Security Model](#security-model)
- [Network Enforcement (Proxy / eBPF / Both)](#network-enforcement)
- [Budget Enforcement](#budget-enforcement)
- [SandboxProfile](#sandboxprofile)
- [Built-in Profiles](#built-in-profiles)
- [Substrates](#substrates)
- [Non-Interactive Agent Execution](#non-interactive-agent-execution)
- [Scheduling and Recurring Operations](#scheduling-and-recurring-operations)
- [AMI Lifecycle](#ami-lifecycle)
- [CLI Reference](#cli-reference)
- [Architecture](#architecture)
- [Documentation](#documentation)
- [Roadmap](#roadmap)
- [License & Project Status](#license--project-status)

---

## What Klanker Maker Is

Klanker Maker (`km`) is a single Go CLI that turns a Kubernetes-style YAML profile into a self-contained AWS sandbox for running AI agents. Every sandbox gets its own identity, its own network policy, its own dollar budget, and its own Slack thread. The platform itself is cloud-native AWS - EventBridge Scheduler, Lambda dispatchers, DynamoDB global tables, SES, SSM, KMS, SCP - running in your account, under your IAM, on your bill.

There are four useful frames for it:

**1. The runtime.** A sandbox is a compiled policy object. The profile declares what's allowed (egress hosts, repos, regions, spend) and the compiler produces real AWS infrastructure: a Security Group, an IAM role, EBS volumes, EFS mounts, a per-sandbox cgroup with eBPF programs attached, a transparent MITM proxy for L7-required traffic, sidecar systemd services for DNS/HTTP/audit/OTEL. No shared multi-tenant runtime to trust. No container escape surface. The isolation is at the AWS primitive layer.

**2. The fleet manager.** `km` doesn't just create sandboxes - it manages a fleet. A DynamoDB table is the source of truth (`km list`, `km status`, alias lookups). EventBridge Scheduler drives `km at` ("destroy at 5pm Friday", "every Thursday run nightly tests"). Lambda dispatchers handle `km create --remote`, `km destroy --remote`, email-to-create, GitHub App token refresh, TTL expiry, spot interruption, budget enforcement. Sandboxes can be paused (hibernated to disk), stopped, locked, cloned, baked into AMIs, or scheduled to resume.

**3. The integrations layer.** Klanker Maker is built to be the surface a human (or another agent) drives an agent fleet through. A Slack App provides bidirectional chat: `#sb-{id}` channels per sandbox, transcript streaming, `:eyes:` ack reactions, signing-secret-verified Events API webhooks dispatched to per-sandbox SQS FIFO queues. A GitHub App provides per-sandbox short-lived installation tokens scoped to allowlisted repos. SES + Ed25519 lets sandboxes message each other (and the operator) with cryptographically verified sender identity. OTEL captures every prompt, tool call, and API request to S3 for replay.

**4. The work envelope.** Sandboxes scale with the workload. The profile picks the substrate (EC2 spot/on-demand, ECS Fargate, Docker), the instance type (`t3.medium` for a quick fix, `r7i.48xlarge` for an in-memory backtest, a GPU box for fine-tuning), and the storage shape (EBS, additional EBS volume, EFS shared across a crew of agents). The same eBPF + MITM + budget layer wraps all of them. The point isn't "sandbox a coding agent on my laptop" - it's *put Claude (or a dozen Claudes) in front of cloud-scale compute and data, with the policy, identity, and dollar rails wired in by construction*. Heavy data and ML workloads belong on AWS; the agent driving them shouldn't be the part that has to live on your MacBook.

---

## How It Compares

Klanker Maker sits in the gap between three categories of tool. The table below is the elevator pitch.

| | **Klanker Maker** | **AWS Bedrock AgentCore** | **Coder** | **E2B / agent-sandbox** |
|---|---|---|---|---|
| **Who runs the runtime?** | You - your AWS account, your VPC, your bill | AWS-managed | You - typically Kubernetes | E2B-managed (SaaS) / your K8s cluster |
| **Who is it for?** | AI agents (Claude, Goose, Codex, security tools) | AI agents | Human developers | AI agents |
| **Definition format** | Declarative YAML profile, schema-validated | SDK / API construction | Terraform-templated workspaces | SDK / Dockerfile |
| **Network policy** | Cgroup eBPF + DNS/HTTP MITM proxy + SCP backstop | VPC + IAM | NetworkPolicy / SG | Container network |
| **Budget enforcement** | Per-sandbox $ ceiling, dual-layer (proxy 403 + IAM revocation) | Per-account billing alarms (delayed) | None | None |
| **Identity model** | Per-sandbox Ed25519 + scoped IAM session + GitHub App token | Bedrock-managed identity provider | Per-workspace OIDC | Container env |
| **Slack integration** | Native: bidirectional chat, transcript stream, ack reactions | None | None | None |
| **GitHub integration** | Native: GitHub App, per-repo allowlist, short-lived tokens | None | Limited | None |
| **Multi-agent comms** | Signed email (Ed25519 + optional NaCl box) over SES | None | N/A | Inter-sandbox HTTP |
| **Org-level guardrails** | Service Control Policy (6-statement deny set) | AWS-internal | None | None |
| **Substrate** | EC2 spot/on-demand, ECS Fargate spot/on-demand, Docker (local), EKS planned | AWS-managed Firecracker | Kubernetes / cloud VM | Firecracker |

**Closest mental models:**

- *AgentCore, but you own the substrate.* AgentCore is a managed runtime; Klanker Maker is the same problem solved on infrastructure you control, with the policy authored in YAML instead of constructed via SDK, and with the eBPF/MITM/SCP layers AgentCore doesn't expose.
- *Coder, but for agents instead of humans.* Coder gives developers ephemeral cloud workspaces from a template. Klanker Maker gives agents ephemeral cloud sandboxes from a profile, with the security additions agents need (kernel-level egress filtering, MITM token metering, dollar ceilings, signed inter-agent email).
- *E2B, but self-hosted with kernel-level controls.* E2B is excellent for stateless code execution. Klanker Maker is for agents that need real cloud credentials, real persistent storage, real GitHub repos, and a real budget - running where your data already lives.

---

## Quick Start

Get a budgeted Claude running on AWS in five commands. Assumes AWS SSO is already set up; see the [Operator Guide](OPERATOR-GUIDE.md) for full prerequisites.

```bash
# Install
go install github.com/whereiskurt/klanker-maker/cmd/km@latest

# 1. One-time platform configuration (domain, account IDs, region)
km configure

# 2. One-time bootstrap: SCP + KMS + artifacts bucket (in management account)
km bootstrap --dry-run=false

# 3. One-time per region: build Lambdas/sidecars, provision shared VPC
km init --region us-east-1

# 4. Health check (20+ checks across all accounts)
km doctor

# 5. Ship a Claude
km create profiles/goose.yaml --alias dev1
km agent run dev1 --prompt "summarize CHANGELOG.md" --wait
km destroy dev1 --yes
```

Need to manage many at once? `km list` shows the fleet, `km at` schedules deferred work, and the Slack `#sb-{id}` channels give every sandbox a thread you can talk to.

```bash
# Spin up 5 worker sandboxes from one profile
for i in 1 2 3 4 5; do km create profiles/goose.yaml --alias worker-$i & done; wait

# Schedule nightly destroys
km at 'every weekday at 11pm' kill worker-1
km at 'every weekday at 11pm' kill worker-2

# Send all of them a prompt at 6am
km at '6am tomorrow' agent run worker-1 --prompt "pull main, run tests, post results" --auto-start
```

---

## Why This Exists

The agent ecosystem is exploding. My GitHub stars tell the story - in the last few months alone I've starred:

**Coding agents** that need real compute, real network, and real credentials:
- [Goose](https://github.com/block/goose) - Block's autonomous agent that installs deps, edits files, runs tests, orchestrates workflows
- [Aider](https://github.com/Aider-AI/aider) - AI pair programming in your terminal with automatic git commits
- [open-swe](https://github.com/langchain-ai/open-swe) - LangChain's asynchronous coding agent
- [DeepCode](https://github.com/HKUDS/DeepCode) - agentic coding for Paper2Code, Text2Web, Text2Backend
- [deepagents](https://github.com/langchain-ai/deepagents) - LangGraph harness with planning, filesystem, and sub-agent spawning

**Multi-agent orchestrators** that spawn fleets of workers:
- [agent-orchestrator](https://github.com/ComposioHQ/agent-orchestrator) - parallel coding agents with autonomous CI fixes and code reviews
- [nanoclaw](https://github.com/qwibitai/nanoclaw) - lightweight agent on Anthropic's Agent SDK, runs in containers
- [pi-mono](https://github.com/badlogic/pi-mono) - coding agent CLI, unified LLM API, Slack bot, vLLM pods
- [gobii-platform](https://github.com/gobii-ai/gobii-platform) - always-on AI workforce
- [autoresearch](https://github.com/karpathy/autoresearch) - Karpathy's agents running research on single-GPU training automatically

**Security and red-team agents** that *definitely* need containment:
- [redamon](https://github.com/samugit83/redamon) - AI-powered red team framework, recon to exploitation, zero human intervention
- [raptor](https://github.com/gadievron/raptor) - turns Claude Code into an offensive/defensive security agent
- [hexstrike-ai](https://github.com/0x4m4/hexstrike-ai) - 150+ cybersecurity tools orchestrated by AI agents
- [strix](https://github.com/usestrix/strix) - open-source AI hackers that find and fix vulnerabilities
- [shannon](https://github.com/KeygraphHQ/shannon) - autonomous white-box AI pentester for web apps and APIs

**Sandbox platforms** solving adjacent problems:
- [agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) - Kubernetes SIG for isolated agent runtimes
- [E2B](https://github.com/e2b-dev/E2B) - secure cloud environments for enterprise agents
- [OpenSandbox](https://github.com/alibaba/OpenSandbox) - Alibaba's sandbox platform with Docker/K8s runtimes
- [monty](https://github.com/pydantic/monty) - Pydantic's minimal, secure Python interpreter in Rust for AI

Every one of these projects needs *somewhere safe to run*. The common pattern is either "trust the agent" (bad), "containerize it locally" (no real cloud), or "use a hosted SaaS sandbox" (your data, their infra). What's missing is **cloud-native physical isolation on infrastructure you already own** - a real VPC, real IAM boundaries, real network controls, real budgets, in your AWS account, with the integrations (Slack, GitHub, OTEL, SES) wired up.

That's what Klanker Maker is.

---

## Core Capabilities

A sandbox is a **compiled policy object** - the YAML declares the constraints, the infrastructure is the artifact:

- **Hard budget ceiling** - set a dollar cap for compute and AI API spend per sandbox. At 80% you get a warning email + Slack ping. At 100% the proxy returns 403 and a Lambda revokes Bedrock IAM permissions. Suspended, not destroyed - `km budget add` tops up and resumes.
- **Three network enforcement modes** - `proxy` (iptables DNAT → MITM sidecar), `ebpf` (kernel-level cgroup BPF, no DNAT), or `both` (eBPF gatekeeper + transparent proxy for L7 inspection). See [Network Enforcement](#network-enforcement).
- **Scoped IAM identity** - each sandbox gets its own role, region-locked, time-limited, with only the permissions the profile declares. Cannot escalate (SCP backstop blocks `CreateRole`/`AttachRolePolicy`/`PassRole`).
- **Bidirectional Slack** - per-sandbox `#sb-{id}` channel, operator invited via Slack Connect, signing-secret-verified inbound dispatched via SQS FIFO to a sandbox-side poller that turns Slack messages into Claude turns. Per-turn transcript streaming. `:eyes:` ack reactions.
- **GitHub App** - per-account installation tokens, refreshed by Lambda, scoped to the profile's `allowedRepos` + `allowedRefs`, never written to env. Multi-account org support.
- **Signed email** - every sandbox gets `{id}@sandboxes.{domain}` and an Ed25519 keypair. Inter-sandbox messages are signed, verified against the `km-identities` table, and optionally encrypted (NaCl box). Operator can `km email send` and `km email read` from the workstation.
- **OTEL telemetry** - Claude Code prompts, tool calls, API requests, token usage, cost per turn → OTel Collector sidecar → S3. `km otel --timeline` replays a session.
- **Spot-first economics** - `t3.medium` spot is ~$0.01/hr in `us-east-1`. Spot interruption handlers fire artifact uploads on the 2-minute warning. Run 10 sandboxes for a workday for under $1.
- **Lifecycle automation** - TTL auto-destroy via EventBridge Scheduler, idle timeout, hibernation (`km pause` preserves RAM), stop/resume, lock/unlock, clone, AMI bake.
- **AMI snapshot lifecycle** - bake a tuned sandbox into a private AMI on shell exit, reference by slug or ID in profiles, copy across regions, garbage-collect via `km doctor`.
- **VS Code Remote-SSH** - `km vscode start <sandbox>` opens an SSM port-forward and writes a managed `~/.ssh/config` block; VS Code "Remote-SSH: Connect to Host..." lands directly in `/workspace`. Per-sandbox ed25519 keypairs generated locally, pubkey shipped via userdata. No public IP, no SSH bastion. See [VS Code Remote-SSH](docs/vscode.md).

---

## Cloud-Native Control Plane

Klanker Maker is itself an AWS application. The `km` CLI is the front door, but most of the platform runs as Lambdas, EventBridge schedules, DynamoDB tables, and SQS queues - so a sandbox can be created, modified, or destroyed from anywhere there's AWS API access.

![Klanker Maker AWS services overview](docs/diagrams/AWS-services-and-apps.svg)

| Service | Role |
|---|---|
| **EventBridge Scheduler** | Drives `km at` deferred and recurring operations (one-shot creates, nightly destroys, recurring agent runs). Per-sandbox TTL schedules trigger the TTL handler Lambda. |
| **Lambda - `km-create-handler`** | Remote sandbox creation (`km create --remote`). The CLI publishes the profile to EventBridge; the Lambda runs the compile + Terragrunt apply with a service role. |
| **Lambda - `km-ttl-handler`** | Fires on TTL expiry: artifact upload to S3, lifecycle email + Slack notification, Terragrunt destroy, EventBridge schedule cancel. |
| **Lambda - `km-budget-enforcer`** | Triggered when the proxy reports a budget breach: revokes Bedrock IAM permissions on the sandbox role; on `km budget add`, restores them. |
| **Lambda - `km-email-create-handler`** | SES inbound rule routes operator emails to this handler. Haiku interprets free-form English (`km at create`, `please destroy worker-3`), validates safe-phrase auth, dispatches the action. |
| **Lambda - `km-github-token-refresher`** | Refreshes GitHub App installation tokens before expiry, writes to per-sandbox SSM Parameter Store paths. |
| **Lambda - `km-slack-bridge`** | Function URL that receives signed payloads from sandboxes (outbound notifications) and signing-secret-verified Slack `/events` webhooks (inbound). Posts to Slack Web API; enqueues inbound to per-sandbox SQS FIFO queues. |
| **DynamoDB - `km-sandboxes`** | Source of truth for the fleet. `km list`, `km status`, alias lookups, GSIs for Slack channel ID → sandbox lookup. |
| **DynamoDB - `km-budget` (Global Table)** | Per-sandbox spend counters, replicated to every region where agents run. Sub-millisecond reads from inside the sandbox. |
| **DynamoDB - `km-identities`** | Public Ed25519 keys for every sandbox; used by recipients to verify inbound email signatures. |
| **DynamoDB - `km-slack-threads`** | `(channel_id, thread_ts) → claude_session_id` mapping for resumable Slack-driven Claude sessions. TTL-expired after 30 days. |
| **DynamoDB - `km-slack-stream-messages`** | Per-turn message anchors for Phase 68 transcript streaming. Future: reaction-as-action triggers. |
| **DynamoDB - `km-schedules`** | Active `km at` schedules, surfaced by `km at list`. |
| **SQS FIFO (per sandbox)** | `km-slack-inbound-{id}.fifo` - bridge enqueues Slack messages here; sandbox-side poller dequeues and dispatches to Claude. ContentBasedDeduplication off; FIFO ordering preserved. |
| **SES** | Inbound: operator inbox, sandbox mailboxes ({id}@sandboxes.{domain}). Outbound: lifecycle notifications, inter-sandbox email, signed payloads. Domain DKIM + SPF auto-configured by `km init`. |
| **S3** | Artifacts bucket (per region, replicated cross-region), OTEL telemetry, transcripts (gzipped JSONL), agent run output, sidecar binaries. |
| **SSM Parameter Store + KMS** | Per-sandbox signing keys, GitHub tokens, Slack secrets, sandbox config. KMS-encrypted, allowlisted refs only. |
| **SSM Session Manager** | The *only* way to reach a sandbox. `km shell`, `km agent`, command dispatch. No SSH, no bastion, no inbound ports. |
| **Service Control Policy** | Org-level deny on SG mutation, IAM escalation, instance creation, SSM pivot, org discovery, out-of-region resource creation. Enforced before IAM. |

The control plane scales to whatever the underlying AWS services scale to. There is no shared in-process state between operators, no central coordinator to run, no daemon to monitor. Two operators on opposite sides of the world can drive the same fleet via SSO; the DynamoDB tables are the rendezvous.

---

## Slack-Native Operations

Klanker Maker's Slack integration is the primary surface for talking to agents at scale. Phases 62–68 build a complete bidirectional control plane.

### What you get per sandbox

When `notifySlackPerSandbox: true`:

1. **A dedicated channel** - `#sb-{sandbox-id}` is created on `km create`, archived on `km destroy` (configurable). Operator is invited via Slack Connect from their own workspace.
2. **Lifecycle notifications** - sandbox creation, TTL warnings, budget thresholds, errors, Stop hooks all post to the channel.
3. **Bidirectional chat** (`notifySlackInboundEnabled: true`) - Send a Slack message; the bridge verifies the Slack signing secret, looks up the sandbox via the `slack_channel_id-index` GSI, enqueues to the per-sandbox SQS FIFO queue. A systemd poller on the sandbox dequeues, exports `KM_SLACK_THREAD_TS`, and dispatches `claude -p` with session continuity (resumed via `km-slack-threads` DDB lookup).
4. **Per-turn transcript streaming** (`notifySlackTranscriptEnabled: true`) - Each assistant turn streams to the Slack thread as it happens. Tool calls render as one-liners. On `Stop`, the full session uploads as a gzipped JSONL file. Independent of the `claude -p` cost - runs as a hook.
5. **`:eyes:` ack reactions** - The bridge adds 👀 to the originating message the moment it's enqueued, before the agent has even seen it. Lets the human know it's been received.

### Trust model

The bot token never leaves AWS. Sandboxes don't have it. Outbound Slack notifications go through the bridge Lambda with Ed25519-signed payloads (same key the sandbox uses for email). Inbound Slack events are verified against the signing secret using HMAC-SHA256 before the bridge will dispatch them. The Slack App's bot scopes are minimal: `chat:write`, `channels:manage`, `conversations.connect:write`, `groups:write`, `channels:history`, `groups:history`, `reactions:write`, `files:write`.

### Setup, in one block

```bash
# One-time: rebuild km, build sidecars, deploy bridge + DDB tables
make build && km init --sidecars && km init

# Bootstrap Slack: validates token, creates shared channel, sends Slack Connect invite,
# deploys bridge Lambda, writes signing secret to SSM
km slack init --bot-token xoxb-… --invite-email ops@example.com --signing-secret …

# (paste the printed Events URL into Slack App → Event Subscriptions)
km slack test     # smoke-test through the bridge
km slack status   # show wiring
```

### Profile flags

| Field | Default | Effect |
|---|---|---|
| `notifyEmailEnabled` | `true` | SES email path; pair with Slack or use standalone |
| `notifySlackEnabled` | `false` | Master switch for Slack delivery |
| `notifySlackPerSandbox` | `false` | Provision `#sb-{id}` channel; archive at destroy |
| `notifySlackChannelOverride` | empty | Pin to existing channel ID (mutually exclusive with per-sandbox) |
| `notifySlackInboundEnabled` | `false` | SQS FIFO + sandbox poller for chat → Claude turns |
| `notifySlackTranscriptEnabled` | `false` | Stream per-turn output + upload final JSONL |

See [`docs/slack-notifications.md`](docs/slack-notifications.md) for the full operator guide, including troubleshooting and the security model.

---

## GitHub App Integration

Klanker Maker uses a GitHub App (not personal access tokens) to grant sandboxes scoped, short-lived git access.

```bash
km github                                      # manifest-flow App creation in browser
km configure github --discover --force         # discover installations across accounts
```

Per profile:

```yaml
spec:
  sourceAccess:
    mode: allowlist
    github:
      allowedRepos:
        - my-org/api
        - my-org/infra
      allowedRefs: [main, "feature/*", "fix/*"]
```

What happens at create:

1. The compiler reads `allowedRepos`, looks up the matching App installation per repo owner, and provisions a per-sandbox SSM parameter holding the installation token.
2. The `km-github-token-refresher` Lambda runs on a schedule, refreshing the token before its 1-hour expiry.
3. Inside the sandbox, the HTTP proxy is configured to inject the token as `Authorization: token …` for `*.github.com` and `*.githubusercontent.com` requests - but only for paths matching `allowedRepos`. Other repos return 403 even if the agent has the URL.
4. Refs are enforced at the proxy layer too: `git push` to a non-allowlisted ref is rejected.

Multi-account is supported (Phase 54): install the App on multiple GitHub accounts/orgs, and `km configure github --discover` writes an installation key per account. The compiler matches `org/repo` to the right installation.

See [`docs/github.md`](docs/github.md) for the full setup.

---

## Multi-Agent Orchestration via Signed Email

Sandboxes communicate through **digitally signed email** (SES + Ed25519). Each sandbox gets a unique address derived from its ID (e.g., `sb-a1b2c3d4@sandboxes.klankermaker.ai`) and an Ed25519 key pair at creation time.

- **Signing** - outbound emails are signed with the sender's Ed25519 private key (stored in SSM, KMS-encrypted). The signature and sender ID are attached as `X-KM-Signature` and `X-KM-Sender-ID` headers.
- **Verification** - the receiver fetches the sender's public key from the `km-identities` DynamoDB table and verifies the signature. When `verifyInbound: required`, unsigned or invalid emails are rejected.
- **Encryption** - optional X25519 key exchange (NaCl box). When `encryption: required`, the sender encrypts the body with the recipient's public key. When `encryption: optional`, it encrypts if the recipient has a published key, plaintext otherwise.

Inside a sandbox, `km-send` and `km-recv` wrap the protocol:

```bash
# From inside any sandbox
km-send --to sb-x9y8z7@sandboxes.example.com --subject "results" --body output.json --attach data.tar.gz
km-recv --watch                    # poll inbox every 5s
km-recv --json | jq '.[0].body'    # for agent parsing
```

From the operator workstation, `km email send` and `km email read` do the same with full signature verification and auto-decryption.

This enables multi-agent pipelines where each worker is physically isolated but logically connected - with cryptographic proof of sender identity and optional confidentiality. Combined with `km at` scheduling, you can build pipelines like *"every Monday at 9am, sb-A runs the smoke tests, on success emails sb-B which runs the deploy"*.

See [`docs/multi-agent-email.md`](docs/multi-agent-email.md) for SES setup, IAM policy, signing protocol, and orchestration patterns.

---

## AWS Account Architecture

Klanker Maker follows AWS Organizations best practices, supporting either a **three-account** or **two-account** topology. In both models, sandboxes run in a dedicated application account - completely separated from the account that owns the domain and applies SCP policies.

<p align="center">
  <img src="docs/frame1-security-network.svg" alt="Security & Network Architecture - 3 accounts, shared VPC, per-sandbox Security Groups" />
</p>

| Account | Role | What Lives Here | Why Separate |
|---------|------|----------------|--------------|
| **Management** | DNS, identity, org root | Route53 hosted zone, domain registration, AWS SSO, Organizations root, SCP attachments | Domain and identity are org-wide - they don't belong in a sandbox blast radius |
| **Terraform** | State and provisioning | S3 state buckets, DynamoDB lock tables, cross-account provisioning role | Terraform state contains every resource ARN and secret path - isolating it limits exposure if the application account is compromised |
| **Application** | Sandbox execution | Regional VPCs, EC2/ECS instances, IAM sandbox roles, SES, Lambda handlers, DynamoDB budget table, S3 artifacts, CloudWatch Logs | This is where agents run - if an agent escapes its sandbox, it can only reach resources in this account, not state or DNS |

In a **two-account topology**, the Terraform and Application accounts are the same - set both account IDs to the same value during `km configure`. Simpler for development; the management account stays separate for SCP and DNS.

Authentication is via AWS SSO with named profiles:

```bash
aws sso login --profile klanker-management     # DNS, domain, SCP
aws sso login --profile klanker-terraform      # State, provisioning
aws sso login --profile klanker-application    # VPC, sandbox runtime
```

The `km` CLI selects the right AWS profile per command automatically.

### Multi-instance support

`km configure` prompts for `resource_prefix` (default `km`) and `email_subdomain` - propagated to Terragrunt via `KM_RESOURCE_PREFIX` and `KM_EMAIL_SUBDOMAIN`. This lets you run multiple isolated km installs in the same AWS account (e.g., `prod-` and `staging-` prefixes). See `OPERATOR-GUIDE.md` § Multi-instance support.

### Forkable

No hardcoded account IDs. No hardcoded domains. A fork with a different domain works end-to-end after `km configure`.

---

## Security Model

Klanker Maker uses **explicit allowlists everywhere** - if it's not in the policy, it's denied. There is no "default allow."

### No SSH. No Bastion. No Keys.

Sandboxes are accessed exclusively through **AWS SSM Session Manager**:

- **Zero open inbound ports** - Security Groups have no SSH ingress rules. Port 22 doesn't exist.
- **No SSH keys to manage** - no generation, rotation, distribution, or leaked keys on GitHub.
- **IAM-gated access** - who can connect is controlled by IAM policy, not by who has a `.pem` file.
- **Full session audit** - every session and every command is logged to CloudTrail and CloudWatch. There is no "off the record."
- **No bastion hosts** - no jump boxes, no VPN. SSM connects through the agent, even in private subnets with no internet access.

### SCP Sandbox Containment

Even if a sandbox IAM role is misconfigured - or an agent finds a way to escalate within the application account - the **Service Control Policy (SCP)** is an org-level backstop that cannot be bypassed from within the account. SCPs are enforced by AWS Organizations at the API layer, before IAM policy evaluation.

The `km-sandbox-containment` SCP is deployed to the management account and attached to the application account. Six deny statements:

| Statement | What It Blocks | Why It Matters |
|-----------|---------------|----------------|
| **DenyInfraAndStorage** | SG mutation, VPC/subnet/route/IGW/NAT creation, VPC peering, Transit Gateway, snapshot/image creation and export | A compromised sandbox cannot open new network paths, create escape routes, peer with other VPCs, or exfiltrate data via EBS snapshots or AMI copies |
| **DenyInstanceMutation** | `RunInstances`, `ModifyInstanceAttribute`, `ModifyInstanceMetadataOptions` | Prevents launching rogue EC2 instances or disabling IMDSv2 (which would enable SSRF credential theft via the metadata service) |
| **DenyIAMEscalation** | `CreateRole`, `AttachRolePolicy`, `DetachRolePolicy`, `PassRole`, `AssumeRole` | Blocks the classic IAM privilege escalation chain: create a new admin role → attach `AdministratorAccess` → assume it |
| **DenySSMPivot** | `SendCommand`, `StartSession` | Prevents a compromised sandbox from using SSM to pivot laterally into other sandbox instances |
| **DenyOrgDiscovery** | `organizations:List*`, `organizations:Describe*` | Prevents enumeration of the org structure, other accounts, and OUs - information useful for targeting lateral movement |
| **DenyOutsideRegion** | All regional actions outside allowed regions | Region-locks the entire account to prevent resource creation in regions where there's no monitoring or VPC infrastructure |

Each statement uses `ArnNotLike` conditions to carve out trusted operator roles (SSO, provisioner, lifecycle handlers). The carve-outs are minimal - for example, the budget enforcer Lambda only gets an IAM carve-out (it needs `AttachRolePolicy`/`DetachRolePolicy` to revoke Bedrock access), not a network or instance carve-out.

The SCP is deployed via `km bootstrap --dry-run=false`. Run `km bootstrap --show-prereqs` to see the exact IAM role and trust policy that must be created in the management account first.

### Defense in Depth

| Layer | Control | Enforcement |
|-------|---------|-------------|
| **Organization** | SCP sandbox containment | Org-level deny on SG/network/IAM/instance/SSM/region - cannot be bypassed from within the account |
| **Account** | Three-account isolation | Sandbox blast radius limited to Application account; state and DNS unreachable |
| **Network** | VPC Security Groups | Primary boundary - blocks all egress except proxy paths |
| **DNS** | DNS proxy sidecar / eBPF resolver | Allowlisted suffixes only; non-matching → NXDOMAIN |
| **HTTP** | HTTP proxy sidecar / eBPF connect4 | Allowlisted hosts only; non-matching → 403 / EPERM |
| **eBPF** | Cgroup BPF programs (connect4, sendmsg4, sockops, egress) | Kernel-level enforcement; LPM trie allowlist; ring buffer audit; no root bypass |
| **Identity** | Scoped IAM sessions | Region-locked, time-limited, minimal permissions |
| **Email** | Ed25519 signed email | Per-sandbox key pairs; profile-controlled signing, verification, and encryption policies |
| **Slack** | Bridge Lambda + signing secret | Outbound Ed25519-signed; inbound HMAC-verified; bot token never leaves AWS |
| **Secrets** | SSM Parameter Store + KMS | Allowlisted refs only; per-sandbox encryption key with auto-rotation |
| **Metadata** | IMDSv2 enforced | Token-required; blocks SSRF credential theft via instance metadata |
| **Source** | GitHub App scoped tokens | Per-repo, per-ref, per-permission; short-lived installation tokens refreshed via Lambda |
| **Filesystem** | Path-level enforcement | Writable vs read-only directories at OS level |
| **Audit** | Command + network logging | Secret-redacted; delivered to CloudWatch/S3 |
| **TLS Observability** | eBPF SSL uprobes (OpenSSL, Go, BoringSSL) | Passive plaintext capture without MITM certs; independent audit trail |
| **Telemetry** | OTEL observability | Claude Code prompts, tool calls, API requests, cost metrics → OTel Collector → S3 |
| **Budget** | Compute + AI spend tracking | DynamoDB real-time metering; proxy 403 + IAM revocation at ceiling |

---

## Network Enforcement

Three modes via `spec.network.enforcement`:

- **`proxy`** (default) - iptables DNAT redirects traffic to userspace proxy sidecars for MITM inspection. Traditional approach, works everywhere.
- **`ebpf`** - Cilium-style cgroup BPF programs enforce DNS/HTTP/TLS-SNI allowlists directly in the kernel. No iptables, no DNAT bypass possible (closes the root-user escape).
- **`both`** - eBPF `connect4` as the primary block-mode enforcer, with selective DNAT rewrite to a transparent proxy for L7-required hosts (GitHub repo filtering, Bedrock token metering). Non-L7 traffic flows direct - never touches the proxy.

```yaml
spec:
  network:
    enforcement: "both"
    egress:
      allowedDNSSuffixes: [".amazonaws.com", ".github.com", …]
      allowedHosts: ["api.anthropic.com", …]
```

### eBPF deep dive

When `enforcement` is `ebpf` or `both`, the sandbox uses Cilium-style cgroup BPF programs instead of (or alongside) iptables DNAT. Same approach Cilium uses in Kubernetes - attach BPF programs to a cgroup to intercept all network syscalls from processes in that group. E2E verified across 14+ iterations on AL2023 kernel 6.18.

```text
Sandbox Cgroup (/sys/fs/cgroup/km.slice/km-{id}.scope)
│
├── cgroup/connect4   - TCP connect() hook
│   ├── Dual-PID exemption (enforcer + proxy sidecar)
│   ├── LPM trie lookup: is dest IP in allowed_cidrs?
│   ├── If denied → return EPERM (connection refused)
│   ├── If allowed + proxy-marked → stash original dest, rewrite to 127.0.0.1:3128
│   └── Emit structured audit event to ring buffer
│
├── cgroup/sendmsg4   - UDP sendmsg() hook
│   ├── Intercept DNS (port 53)
│   └── Redirect to local resolver (127.0.0.1:53)
│
├── sockops           - TCP state transitions
│   └── Map source_port → socket_cookie (transparent proxy recovers real dest)
│
└── cgroup_skb/egress - Packet-level backstop
    ├── Parse IPv4 header, check allowed_cidrs
    └── Drop packets to non-allowlisted IPs (L3 defense-in-depth)
```

**How the allowlist stays fresh:** A userspace DNS resolver (127.0.0.1:53) checks every DNS query against the profile's `allowedDNSSuffixes`. Allowed queries are forwarded to VPC DNS; resolved IPs are injected into the BPF `allowed_cidrs` LPM trie map with TTL-based expiry. For L7-required hosts (GitHub, Bedrock), IPs are also inserted into `http_proxy_ips` for selective proxy redirect. The allowlist is *dynamic* - it grows as the agent resolves new hosts and shrinks as DNS TTLs expire.

**Why cgroups?** The BPF programs are scoped to the sandbox cgroup, not the whole instance. The enforcer process, SSM agent, and sidecars run outside the cgroup and are unaffected. Same isolation model that makes this approach portable to EKS pods, Docker cgroups, and other container runtimes in future substrates.

**Transparent proxy (both mode):** When `connect4` rewrites a connection's destination to the local proxy, the sandbox app sends raw TLS (not HTTP CONNECT). A `TransparentListener` in the HTTP proxy peeks the first byte (`0x16` = TLS ClientHello), then recovers the original destination via a three-step BPF map lookup chain: `src_port_to_sock[peer_port]` → `sock_to_original_ip[cookie]` → `sock_to_original_port[cookie]`. Enables L7 inspection (GitHub repo filtering, Bedrock token metering) without `HTTP_PROXY` env var cooperation from the client.

Editable diagram: [`docs/diagrams/ebpf-architecture.excalidraw`](docs/diagrams/ebpf-architecture.excalidraw)

### eBPF SSL uprobe observability

Alongside kernel-level enforcement, eBPF uprobes provide **passive TLS plaintext capture** for audit and observability - without MITM certificates. E2E verified on AL2023 with 8 probes attaching to OpenSSL 3.2.2:

| TLS Library | Used By | Uprobe Target | Status |
|-------------|---------|---------------|--------|
| OpenSSL (libssl.so.3) | curl, wget, Python, Ruby | `SSL_write` / `SSL_read` / `SSL_write_ex` / `SSL_read_ex` | **E2E verified** (8 probes) |
| Go crypto/tls | Goose (if Go) | `writeRecordLocked` / `Read` | Schema-ready (per-RET offsets, no uretprobe) |
| BoringSSL (Bun) | Claude Code | `SSL_write` | Schema-ready (byte-pattern offset discovery) |
| rustls | Future Rust agents | `rustls_connection_write_tls` | Schema-ready |

**What uprobes add that MITM can't:** Visibility into traffic that bypasses the proxy (if any), audit trail independent of proxy logs, plaintext capture without certificate trust issues. The observer logs structured JSON events with HTTP method, URL, host, and response status for every TLS connection. Git-smart-HTTP (clone/push) uses HTTP/1.1 and is captured correctly.

**What uprobes can't replace:** Active request blocking (uprobes are passive - they observe but cannot deny), HTTP/2 body parsing (GitHub API and Bedrock use HTTP/2 - uprobe captures HPACK-compressed binary, not parseable HTTP/1.1), and the transparent proxy's active enforcement (repo filtering, budget 403s).

### Learn mode

Generate a minimal SandboxProfile from observed traffic:

```bash
km create profiles/learn.yaml          # wide-open sandbox with learnMode + privileged
km shell --learn <sandbox-id>          # observe traffic + commands, generate profile on exit
cat learned.*.yaml                     # annotated profile with DNS suffixes, initCommands
km validate learned.*.yaml             # validate before use
```

Add `--ami` to `km shell --learn` to bake the running instance into a custom AMI on exit; the AMI ID is written into the generated profile's `spec.runtime.ami`.

---

## Budget Enforcement

Budget enforcement tracks two spend pools per sandbox, stored in a **DynamoDB global table** replicated to every region where agents run. Reads from within the sandbox hit the local regional replica with sub-millisecond latency.

<p align="center">
  <img src="docs/frame2-budget-enforcement.svg" alt="Budget Enforcement Flow - proxy metering, DynamoDB tracking, dual-layer enforcement" />
</p>

### Compute budget

Tracked as spot rate × elapsed minutes, sourced from the AWS Price List API at sandbox creation. Paused/hibernated intervals are excluded (Phase 60). When the compute budget is exhausted, the sandbox is *suspended* - not destroyed:

- **EC2**: `StopInstances` preserves the EBS volume. No compute charges accrue while stopped.
- **ECS Fargate**: Artifacts are uploaded, then the task is stopped. Re-provision from the stored S3 profile on top-up.

### AI budget (Bedrock, Anthropic, OpenAI)

The HTTP proxy sidecar intercepts every AI API response - **Bedrock** (`invoke-with-response-stream`), **Anthropic direct** (`api.anthropic.com`, for Claude Code Max/API key users), and **OpenAI-compatible** endpoints. A tee-reader streams data through to the client without blocking, captures the full response, then extracts token counts asynchronously:

- **Bedrock streaming**: base64-decodes `{"bytes":"<b64>"}` event-stream wrappers to find `message_start`/`message_delta` payloads
- **Anthropic SSE**: parses `data:` lines for the same event types
- **Non-streaming**: reads `usage` from the JSON response body

Tokens are priced against static model rates and atomically incremented in the DynamoDB spend counter.

**Dual-layer enforcement** at 100%:

1. **Proxy layer** (immediate) - HTTP proxy returns 403 for subsequent AI calls
2. **IAM layer** (backstop) - a Lambda revokes the sandbox IAM role's Bedrock permissions, catching calls that bypass the proxy

`km status` shows per-model AI spend grouped by provider; warnings fire at 80% (configurable via `spec.budget.warningThreshold`). `km budget add` unblocks the proxy, restores IAM, and restarts suspended compute in one command.

### OTEL telemetry

Claude Code running inside sandboxes exports OpenTelemetry (prompts, tool calls, API requests, token usage, cost metrics) through an OTel Collector sidecar to S3. Five views via `km otel`:

```bash
km otel <sandbox>              # summary: budget + S3 + metrics
km otel <sandbox> --prompts    # user prompts with timestamps
km otel <sandbox> --events     # full event stream (API calls, tool calls)
km otel <sandbox> --tools      # tool call history with parameters and duration
km otel <sandbox> --timeline   # conversation turns with per-turn cost
```

---

## SandboxProfile

Profiles use a Kubernetes-style schema at `klankermaker.ai/v1alpha1`. Here's the `goose` profile - provisions a Goose agent sandbox with Bedrock, OTEL, hibernation, EFS, GitHub repo allowlisting, Slack notifications, and the eBPF gatekeeper:

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: goose
  prefix: gebpfgk

spec:
  lifecycle:
    ttl: "4h"
    idleTimeout: "1h"
    teardownPolicy: stop

  runtime:
    substrate: ec2
    spot: false
    instanceType: t3.medium
    region: us-east-1
    rootVolumeSize: 15
    hibernation: true              # preserve RAM state on pause (on-demand only)
    mountEFS: true
    efsMountPoint: /shared
    additionalVolume:
      size: 20
      mountPoint: /data

  execution:
    shell: /bin/bash
    workingDir: /workspace
    useBedrock: true               # SigV4 auth via AWS Bedrock
    privileged: false
    env:
      GOOSE_PROVIDER: aws_bedrock
      GOOSE_MODEL: us.anthropic.claude-opus-4-6-v1
    configFiles:
      "/home/sandbox/.claude/settings.json": |
        {"trustedDirectories":["/home/sandbox","/workspace"]}
    initCommands:
      - "yum install -y git nodejs npm python3 jq tmux"
      - "npm install -g @anthropic-ai/claude-code"

  budget:
    compute: { maxSpendUSD: 0.50 }
    ai:      { maxSpendUSD: 1.00 }
    warningThreshold: 0.80

  network:
    enforcement: both
    egress:
      allowedDNSSuffixes:
        - .amazonaws.com
        - .anthropic.com
        - .github.com
        - .githubusercontent.com
        - .npmjs.org
        - .pypi.org

  sourceAccess:
    mode: allowlist
    github:
      allowedRepos: [my-org/api, my-org/infra]
      allowedRefs:  [main, "feature/*", "fix/*"]

  identity:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
    sessionPolicy: minimal

  cli:
    notifyEmailEnabled: true
    notifySlackEnabled: true
    notifySlackPerSandbox: true
    notifySlackInboundEnabled: true
    notifySlackTranscriptEnabled: true

  observability:
    claudeTelemetry:
      enabled: true
      logPrompts: true
      logToolDetails: true
    tlsCapture:
      enabled: true
      libraries: [openssl]

  email:
    signing: required
    verifyInbound: required
    encryption: required
```

Profiles support **inheritance** via `extends` - start from a base and override what you need. See [`docs/profile-reference.md`](docs/profile-reference.md) for the full schema.

---

## Built-in Profiles

| Profile | TTL | Network | Budget | Use Case |
|---------|-----|---------|--------|----------|
| `hardened` | 4h | eBPF+proxy (both), AWS services only | None | Production-adjacent testing |
| `sealed` | 1h | Proxy, .anthropic.com + .npmjs.org only | $5 / $10 | Minimal egress, short-lived |
| `goose` | 4h | eBPF+proxy (both), Anthropic + GitHub + npm + PyPI + OpenAI | $0.50 / $1 | Goose agent (Block) with Bedrock + MCP |
| `codex` | 4h | Proxy, OpenAI + GitHub | $2 / $5 | OpenAI Codex agent |
| `ao` | 8h | eBPF+proxy (both), Anthropic + GitHub + npm + OpenAI | $4 / $10 | Multi-agent orchestration (Claude + Codex + AO) |
| `learn` | 2h | eBPF+proxy (both), wide-open TLD suffixes | $2 / $0 | Traffic observation for profile generation |

---

## Substrates

| Substrate | How It Works | Cost |
|-----------|-------------|------|
| **EC2 Spot** (default) | Shared regional VPC, per-sandbox SG, spot instance, SSM access, sidecar systemd services | ~$0.01/hr for t3.medium |
| **EC2 On-Demand** | Same as above, guaranteed capacity (required for hibernation) | ~$0.04/hr for t3.medium |
| **ECS Fargate Spot** | Fargate task with sidecar containers, service discovery | ~$0.01/hr for 1 vCPU / 2GB |
| **ECS Fargate** | Same as above, guaranteed capacity | ~$0.04/hr for 1 vCPU / 2GB |
| **Docker** (local) | Docker Compose on local machine, sidecar containers, IAM roles via STS | Free (local compute) |

Spot interruption handlers automatically upload artifacts to S3 before instances are reclaimed. EKS substrate is on the roadmap (Phase 38).

---

## Non-Interactive Agent Execution

Run Claude (or any agent) non-interactively inside a sandbox. Prompts dispatched via SSM SendCommand, agents run in persistent tmux sessions that survive disconnects, output stored on disk + S3 for fast retrieval.

```bash
# Fire-and-forget - agent runs in tmux, returns immediately
km agent run sb-abc123 --prompt "fix the failing tests"

# Wait for completion - blocks until done, prints JSON result
km agent run sb-abc123 --prompt "What model are you?" --wait

# Interactive - attach to tmux, watch Claude work live (Ctrl-B d to detach)
km agent run sb-abc123 --prompt "refactor auth module" --interactive

# Attach to a running agent's tmux session
km agent attach sb-abc123

# Fetch results (S3 fast path, ~3s)
km agent results sb-abc123 | jq '.result'
km agent results sb-abc123 | jq '.total_cost_usd'

# List all runs with status
km agent list sb-abc123

# Use direct Anthropic API instead of Bedrock
km agent run sb-abc123 --prompt "..." --no-bedrock --wait
```

**Profile defaults:** Set `spec.cli.noBedrock: true` to default to direct API. Use `spec.execution.configFiles` to pre-seed Claude settings (trusted directories, etc.).

**Codex parity (Phase 70):** the same `--prompt` / `--wait` / `--interactive` flags work for Codex via `--codex`, with notify hooks (Slack, email) and inbound dispatch wired identically.

---

## Scheduling and Recurring Operations

`km at` (alias `km schedule`) is the cron-like layer for sandbox operations. Backed by EventBridge Scheduler, persisted in DynamoDB.

```bash
# One-shot: create a sandbox at 10pm tomorrow
km at '10pm tomorrow' create profiles/goose.yaml --alias nightly

# Recurring: kill nightly sandbox every weekday at 11pm
km at 'every weekday at 11pm' kill nightly

# Schedule an agent run that auto-resumes a paused sandbox first
km at '6am tomorrow' agent run nightly --prompt "pull main, run tests" --auto-start

# Top up a budget on a schedule
km at 'every monday at 9am' budget-add nightly --ai 5.00

# Manage schedules
km at list
km at cancel my-nightly-tests
```

Supported subcommands: `create`, `destroy`/`kill`, `stop`, `pause`, `resume`, `extend`, `budget-add`, `agent run`. Same dispatch model as the CLI - the schedule fires a Lambda that invokes the same handler `km` would.

---

## AMI Lifecycle

Sandboxes can be baked into private AMIs for fast cold starts of tuned environments.

```bash
km shell --learn --ami <sandbox>     # bake on shell exit; AMI ID written into learned profile
km ami list                          # operator-baked AMIs with profile references and size
km ami bake <sandbox>                # snapshot a running sandbox into an AMI
km ami copy <ami-id> --region <dst>  # copy AMI to another region in the same account
km ami delete <ami-id>               # deregister and delete EBS snapshots atomically
```

AMIs are private to the application AWS account (no `LaunchPermission` set), live in a single region until copied, and are surfaced as a `WARN` in `km doctor` when older than `doctor_stale_ami_days` (default 30) and unreferenced by any profile or running sandbox. `spec.runtime.ami` accepts both slugs (`amazon-linux-2023`, `ubuntu-24.04`, `ubuntu-22.04`) and raw AMI IDs (`ami-xxxxxxxx`); the compiler auto-rotates `additionalVolume` off `/dev/sdf` if a baked AMI already claims it.

---

## CLI Reference

The `km` CLI is grouped by workflow stage. Every command picks its AWS profile automatically based on the operation.

### Setup (once per platform)

| Command | What it does |
|---------|--------------|
| `km configure` | Set domain, account IDs, SSO URL, region, `resource_prefix`, `email_subdomain` |
| `km configure github` | Configure GitHub App token integration (`--discover` to find installations) |
| `km bootstrap` | Deploy SCP containment policy + KMS key + artifacts bucket |
| `km init` | Build Lambdas/sidecars, provision shared VPC/network (`--sidecars`, `--lambdas`) |
| `km doctor` | Validate platform health (20+ checks; `--all-regions`) |
| `km info` | Show platform config, accounts, SES quota, AWS spend, DynamoDB tables |

### Sandbox lifecycle

| Command | What it does |
|---------|--------------|
| `km validate <profile>` | Check a profile YAML against the schema |
| `km create <profile>` | Provision a sandbox (`--no-bedrock`, `--docker`, `--alias`, `--on-demand`, `--ttl`, `--idle`) |
| `km clone <sandbox>` | Duplicate a running sandbox (`--alias`, `--count`, `--no-copy`) |
| `km list` (alias: `ls`) | List sandboxes with live status (`--wide`, `--json`, `--tags`) |
| `km status <sandbox>` | Budget, identity, idle countdown, resources |
| `km shell <sandbox>` | SSM session (`--root`, `--ports`, `--learn`, `--ami`) |
| `km agent <sandbox> --claude` | Interactive Claude session via SSM |
| `km agent run <sandbox>` | Non-interactive Claude/Codex (`--prompt`, `--wait`, `--interactive`, `--codex`) |
| `km agent attach <sandbox>` | Attach to a running agent's tmux session |
| `km agent results <sandbox>` | Fetch latest run output |
| `km agent list <sandbox>` | List all agent runs with status |

### Lifecycle management

| Command | What it does |
|---------|--------------|
| `km extend <sandbox> <dur>` | Add time before TTL expires |
| `km pause <sandbox>` | Hibernate (preserves RAM state on on-demand) |
| `km stop <sandbox>` | Stop instance, preserve infrastructure |
| `km resume <sandbox>` | Resume a paused or stopped sandbox |
| `km lock <sandbox>` | Prevent accidental destroy/stop/pause |
| `km unlock <sandbox>` | Re-enable lifecycle commands |
| `km destroy <sandbox>` (alias: `kill`) | Teardown sandbox (`--remote` by default; `--yes`) |
| `km budget add <sandbox>` | Top up compute or AI budget |
| `km rsync save/load <sandbox>` | Save/restore sandbox home directory snapshots |
| `km roll` | Rotate platform and sandbox credentials (`--platform`, `--sandbox`, `--dry-run`) |

### Scheduling

| Command | What it does |
|---------|--------------|
| `km at '<time>' <cmd>` | Schedule deferred/recurring operation (`create`, `destroy`, `pause`, `resume`, `extend`, `budget-add`, `agent run`) |
| `km at list` | List scheduled operations |
| `km at cancel <name>` | Cancel a scheduled operation |

### Observability

| Command | What it does |
|---------|--------------|
| `km logs <sandbox>` | Tail CloudWatch audit logs |
| `km otel <sandbox>` | AI spend summary + OTEL S3 data (`--prompts`, `--events`, `--tools`, `--timeline`) |

### Email

| Command | What it does |
|---------|--------------|
| `km email send` | Send signed email between sandboxes or to/from operator (`--cc`, `--use-bcc`, `--reply-to`) |
| `km email read <sandbox>` | Read sandbox mailbox with signature verification (`--json`, `--mark-read`) |

### Slack

| Command | What it does |
|---------|--------------|
| `km slack init` | Bootstrap: validate token, write SSM params, create channel, send Connect invite, deploy bridge |
| `km slack test` | End-to-end smoke test through the bridge |
| `km slack status` | Print SSM-backed Slack config |
| `km slack rotate-token` | Rotate Slack bot token (validates, persists, force-cold-starts bridge, smoke tests) |
| `km slack rotate-signing-secret` | Rotate Slack App signing secret |

### AMI

| Command | What it does |
|---------|--------------|
| `km ami list` | List operator-baked AMIs (`--wide`) |
| `km ami bake <sandbox>` | Snapshot running sandbox into a custom AMI |
| `km ami copy <ami-id> --region <dst>` | Copy AMI to another region |
| `km ami delete <ami-id>` | Deregister AMI + delete EBS snapshots atomically |

### Teardown

| Command | What it does |
|---------|--------------|
| `km uninit` | Destroy all shared regional infrastructure (reverse of `km init`) |
| `km unbootstrap` | Destroy foundation infrastructure (reverse of `km bootstrap`) |

---

## Architecture

```text
km CLI / ConfigUI
├── cmd/km/                     CLI entry point
├── cmd/configui/               Web dashboard (Go + embedded HTML)
├── cmd/ttl-handler/            Lambda: TTL expiry + artifact upload
├── cmd/budget-enforcer/        Lambda: budget ceiling enforcement
├── cmd/create-handler/         Lambda: remote sandbox creation via EventBridge
├── cmd/email-create-handler/   Lambda: email-driven sandbox creation
├── cmd/github-token-refresher/ Lambda: GitHub App installation token refresh
├── cmd/km-slack-bridge/        Lambda: Slack outbound + inbound bridge (Function URL)
├── internal/app/cmd/           Cobra commands (configure, bootstrap, init, validate,
│                                create, clone, destroy, pause/resume/lock, stop, extend,
│                                roll, at, list, status, logs, budget, shell, agent,
│                                doctor, otel, info, rsync, email, slack, ami)
├── pkg/
│   ├── profile/                SandboxProfile schema, validation, inheritance
│   ├── compiler/               Profile → Terragrunt artifacts (EC2 + ECS paths)
│   ├── ebpf/                   eBPF enforcer (cgroup BPF programs, DNS resolver,
│   │                            audit consumer, SSL uprobes)
│   ├── aws/                    SDK helpers (S3, SES, CloudWatch, DynamoDB,
│   │                            EventBridge Scheduler, identity/signing)
│   ├── terragrunt/             Runner + per-sandbox state isolation
│   ├── lifecycle/              TTL scheduling, idle detection, teardown
│   ├── github/                 GitHub App token management (multi-account)
│   ├── allowlistgen/           Allowlist generation from observed traffic
│   ├── at/                     Deferred/recurring operation scheduling
│   └── localnumber/            Persistent local sandbox numbering
├── sidecars/
│   ├── dns-proxy/              DNS allowlist filter (UDP/TCP:53)
│   ├── http-proxy/             HTTP allowlist + AI token metering (Bedrock, Anthropic, OpenAI)
│   ├── audit-log/              Command + network log router with secret redaction
│   └── tracing/                OTel Collector sidecar (logs, metrics → S3)
├── km-slack/                   Sandbox-side Slack post binary (Ed25519-signed)
├── km-slack-bridge/            Bridge Lambda source
├── profiles/                   Built-in YAML profiles
└── infra/
    ├── modules/                Terraform modules (network, ec2spot, ecs-cluster,
    │                            ecs-task, ecs-service, efs, ses, scp,
    │                            dynamodb-budget, dynamodb-identities,
    │                            dynamodb-sandboxes, dynamodb-schedules,
    │                            budget-enforcer, create-handler, email-handler,
    │                            github-token, s3-replication, ttl-handler,
    │                            ecs-spot-handler, slack-bridge)
    └── live/                   Terragrunt hierarchy (site.hcl, per-sandbox isolation)
```

Editable architecture diagram: [`docs/sandbox-architecture.excalidraw`](docs/sandbox-architecture.excalidraw) - open in [excalidraw.com](https://excalidraw.com) or the VS Code Excalidraw extension.

---

## Documentation

| Document | Description |
|----------|-------------|
| [Operator Guide](OPERATOR-GUIDE.md) | AWS account setup, KMS, S3, SES, Lambda deployment - everything before `km init` |
| [User Manual](docs/user-manual.md) | Full command reference, walkthroughs (Claude, Goose, security agents), profile authoring |
| [Profile Reference](docs/profile-reference.md) | Complete YAML schema with every field, type, default, and validation rule |
| [Security Model](docs/security-model.md) | Deep dive on each security layer, from VPC to IMDSv2 to secret redaction |
| [eBPF Reference](docs/ebpf.md) | BPF program internals, map layout, transparent proxy, SSL uprobes |
| [Budget Guide](docs/budget-guide.md) | DynamoDB schema, proxy metering, enforcement flow, threshold configuration |
| [Slack Notifications](docs/slack-notifications.md) | Bridge setup, channel modes, inbound dispatch, transcript streaming, troubleshooting |
| [GitHub App](docs/github.md) | App creation, multi-account installations, token refresh, repo allowlists |
| [Multi-Agent Email](docs/multi-agent-email.md) | SES setup, sandbox addressing, signing protocol, cross-sandbox orchestration |
| [Docker Substrate](docs/docker.md) | Running sandboxes locally via Docker Compose (`km create --docker`) |
| [Sidecar Reference](docs/sidecar-reference.md) | Each sidecar's config, env vars, log formats, EC2 vs ECS deployment |
| [ConfigUI Guide](docs/configui-guide.md) | Web dashboard setup, profile editor, secrets management |
| [VS Code Remote-SSH](docs/vscode.md) | `km vscode start/status`, ed25519 keypair lifecycle, SSM tunnel, troubleshooting |

---

## Roadmap

Klanker Maker tracks work in [`.planning/`](.planning) using a phase-numbered roadmap. Phases 1–68 and 73 are complete; 69–72 are queued.

| Phase | Description | Status |
|-------|-------------|--------|
| 1–10 | Schema, compiler, provisioning, sidecars, ConfigUI, budgets, SCP | **Complete** |
| 11–20 | Lifecycle, ECS, GitHub App tokens, signed email, doctor, IAM revocation, Anthropic metering | **Complete** |
| 21–30 | Remote dispatch, email-driven ops, GitHub repo allowlists, OTEL, hibernation, pause/lock | **Complete** |
| 31–40 | Transparent HTTPS, agent profiles, MITM CA trust, base container, Docker substrate, eBPF cgroup enforcement | **Complete** |
| 41–50 | eBPF SSL uprobes, gatekeeper mode, EFS, `km at` scheduler, signed mail tooling, AI email-to-command, learn mode, prebaked AMI, non-interactive `km agent` | **Complete** |
| 51–60 | Tmux sessions, `km clone`, persistent numbering, multi-account GitHub, learn-mode AMI, bake hardening, resume hardening, email enhancements, Codex support, sender allowlist, paused-budget accounting | **Complete** |
| 61 | `km shell` Ctrl-C fix (interactive SSM) | **Complete** |
| 62 | Operator-notify hook (Claude Code permission + idle events) | **Complete** |
| 63 | Slack-notify hook (parallel Slack delivery) | **Complete** |
| 64 | `km create` reliability + doctor cleanup hardening | **Complete** |
| 65 | Four-account config model (separate org-SCP from DNS-parent) | **Complete** |
| 66 | Multi-instance support (configurable `resource_prefix` + email subdomain) | **Complete** |
| 67 | Slack inbound - bidirectional chat with `km agent run` dispatch | **Complete** |
| 67.1 | Slack inbound `:eyes:` ack reaction | **Complete** |
| 68 | Slack transcript streaming (per-turn chat + gzipped JSONL upload) | **Complete** |
| 69 | AWS API SCP-style allow/deny via SigV4 inspection | Planned |
| 70 | Codex parity for operator-notify, Slack-notify, and Slack-inbound | Planned |
| 71 | Agent playbook orchestration (multi-step prompts with session continuity) | Planned |
| 72 | Slack corporate workspace support (auto-detect, invite, manifest generator) | Planned |
| 73 | `km vscode` - remote VS Code Remote-SSH via SSM port-forward | **Complete** |

See [.planning/ROADMAP.md](.planning/ROADMAP.md) for detailed phase breakdowns and success criteria.

---

## License & Project Status

Klanker Maker is the personal project of **Kurt Hundeck**, released under the [MIT License](LICENSE).

It is **not** affiliated with, endorsed by, or sponsored by any current or past employer of the author, and it is **not** a commercial product or supported service. The software is provided **AS IS, without warranty of any kind** - see [LICENSE](LICENSE) and [NOTICE.md](NOTICE.md) for the full disclaimer.

| Document | Purpose |
|---|---|
| [LICENSE](LICENSE) | MIT License - the legal terms under which this code is shared |
| [NOTICE.md](NOTICE.md) | Personal-project authorship, no employer affiliation, trademark notice, use-at-your-own-risk |
| [SECURITY.md](SECURITY.md) | How to report security vulnerabilities |
| [CONTRIBUTING.md](CONTRIBUTING.md) | DCO sign-off requirement and contributor warranty (incl. third-party / employer IP terms) |
| [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) | Behavioral expectations for issues, PRs, and discussions |

Klanker Maker provisions real AWS infrastructure that costs real money to operate and grants AI agents scoped credentials in your AWS account. If you use it, you accept full responsibility for everything it does on your bill, your network, and your data.
