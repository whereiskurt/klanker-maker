# Klanker Maker (km)

**An agent runtime on your own AWS account - declarative, eBPF-enforced, Slack+Github-native, with hard budgets that actually stop runaway loops.**
 
**Built for security/engineer teams.** You're team's coverage includes 100x of repos, and you need to move fast+safely - triaging, patching/PRs, doing code reviews, and reasoning about vulnerabilities - without the investigation itself becoming the next breach. Klanker Maker gives you an isolated AWS EC2 instance, YAML policy-governed sandbox where untrusted code, dependencies, and AI agents run inside a contained blast radius.

Klanker Maker compiles a YAML profile into a real AWS sandbox: a scoped IAM role, a kernel-level network policy, a MITM proxy that meters every Bedrock/Anthropic/OpenAI token, a Slack channel that talks back to the agent, and a dollar ceiling that suspends compute when the money runs out. 

The point is to take agentic work off your laptop and put it on AWS at the size the work actually needs - a `t3.medium` for a quick fix, an `r7i.48xlarge` against EFS-backed datasets for a multi-day data pipeline, a GPU box for a training loop, or a crew of Claudes coordinating across all of the above. Drive any of it from a simple `km` invocation, prebuilt `at` AWS EventBridge style schedule, an AWS SES inbound email, or a Slack thread - same control plane, same guardrails.

Isolation is the product. Every sandbox is **default-deny on the network**: an explicit allowlist controls which hosts it can reach, which secrets it can read, and how much it can spend. These are intentional design choices to make **data exfiltration** and **supply-chain compromise** hard by construction - a malicious dependency, a poisoned build step, or a compromised agent has nowhere to phone home and nothing ambient to steal. Patch fast, review at scale, and rationalize about vulns without trusting the thing you're investigating.

<p align="center">
  <a href="https://link.excalidraw.com/p/readonly/IRnJEYKzu0XezsHBg1mx">
    <img src="docs/diagrams/excalidraw.presentation.gif" alt="Klanker Maker architecture slideshow preview" width="720" />
  </a>
  <br />
  <sub>📊 <a href="https://link.excalidraw.com/p/readonly/IRnJEYKzu0XezsHBg1mx">Open the narrated walk-through</a></sub>
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

> 📖 **Full documentation lives in the [Klanker Maker Wiki](https://github.com/whereiskurt/klanker-maker/wiki)** — architecture, security model, network enforcement, Slack/GitHub/email integrations, the SandboxProfile reference, and the full CLI reference.

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

```bash
# Build the CLI
make build

# Create a sandbox from a profile, run an agent, tear it down
./km create profiles/g1.yaml
./km agent run g1 --prompt "investigate the OOM in api-server" --wait
./km destroy g1 --yes
```

Full setup (AWS bootstrap, `km init`, Slack/GitHub wiring) → **[Getting Started](https://github.com/whereiskurt/klanker-maker/wiki/Getting-Started)** in the Wiki.

## Core Capabilities

A sandbox is a **compiled policy object** - the YAML declares the constraints, the infrastructure is the artifact:

- **Hard budget ceiling** - set a dollar cap for compute and AI API spend per sandbox. At 80% you get a warning email + Slack ping. At 100% the proxy returns 403 and a Lambda revokes Bedrock IAM permissions. Suspended, not destroyed - `km budget add` tops up and resumes.
- **Three network enforcement modes** - `proxy` (iptables DNAT → MITM sidecar), `ebpf` (kernel-level cgroup BPF, no DNAT), or `both` (eBPF gatekeeper + transparent proxy for L7 inspection). See [Network Enforcement](https://github.com/whereiskurt/klanker-maker/wiki/Security-and-Network#network-enforcement).
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

---

<p align="center">
  <img src="docs/klankerdome-dark.gif" alt="Klanker Maker - robots working inside a sandboxed dome" width="480" />
  <br />
  <sub>Art by Mike Wigmore (<a href="https://github.com/mikewigmore">@mikewigmore</a>)</sub>
</p>
