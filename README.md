# Klanker Maker (`km`) - AWS EC2 `klankers`
# Define a `sandbox.yaml` and run Claude+friends safely in your AWS Organization/Account.
Hi! 👋 I'm [KPH](https://www.linkedin.com/in/kurthundeck/) and this project has been useful for me building [defcon.run.34](https://github.com/whereiskurt/defcon.run.34), [klanker-voice](https://github.com/whereiskurt/klanker-voice/) and just learning a tonne about "The Future of Software". 

FYI - I've made a bunch of decisions/conventions and put a tonne of thought / experience into building `klanker-maker` **the way it is.** This is approach is called 'opinionated software' and is what makes `klanker-maker` easy to use.🤞 Doesn't mean my opinions won't change, just that I've baked a bunch in, and they are often Security First.

For example - you may not like the fact ZERO inbound network traffic is allowed by the AWS Security Group, blocking all inbound. Everything is over AWS SSM - even 'public network `klankers`'. 🤷 Maybe you don't use `herdr` / vscode / Github / Slack / Hackerone / Wiz or even want remote dev envs. 🤷 Maybe you just want to keep everything localhost?! Maybe you hate AWS EC2 even more than I do?!?! 🤡

Take it, and run. Free software. 🙇

> [!WARNING]
> `klanker-maker` currently a protoype `pre-v1.0.0` which means it has a lot of "vibes" and "abandonded paths." But! I've been writing software for +30-years and and working w/ Claude+friends has been amazing. 🤗

## Easily run a bunch of AWS EC2s instances you can interact with over Slack / Github / Hackerone / email use herdr / vscode / browser+ubuntu / eBPF / Wiz Sensor

<img alt="klanker with slack vscode herdr kasm ebpf" src="https://github.com/user-attachments/assets/3b9600db-3355-48c3-a172-85907c0c522c" />

#### Example 1 - create a klanker and connect to xfce ubuntu desktop:
```shell
  ./km create profiles/desk.yaml desk1
  ./km dekstop start desk1
```

<img width="450"  alt="km create+desktop start" src="https://github.com/user-attachments/assets/a4d3c0da-4bbe-4021-abbc-9c08c71da172" />

#### Example 2 - use a remote vscode session on the klanker:
```
  ./km create profiles/desk.yaml desk1
  ./km vscode start desk1
```

<img width="450" alt="km create + vscode" src="https://github.com/user-attachments/assets/f072982e-6442-4bee-8653-24e63ac05ee5" />


#### Example 3 - herdr and/or full shell access
```
  ./km herdr desk1 # Open right into herdr.
  ./km shell desk2 # OR! bash command line
```
<img width="450" alt="Screenshot 2026-09-22 at 10 29 02" src="https://github.com/user-attachments/assets/110d7051-2580-4990-bf3b-a9bc5cc3a19b" />

# Why?
This project **is not about** solving 'Agents at Scale'🙅 - **it is** about #2 above - isolated YAML defined virutal machines (AWS EC2) for my development and hacking purposes 🔐. To "safely-enable " my new "AI centric workflows/tasks" running Claude/Codex/vLLM sessions, in the cloud, on my AWS infrastructure (detached from my localhost.) 

## 🔥Hot take??🧑‍🚒 AWS EC2 is really only appropriate for 1) making k8s/ECS nodes or 2) disposable cloud developer environment. 🧨
Manage/interact with AWS EC2 klankers over Slack/Github/email/herdr securely inside your own AWS account using AWS security primitives like SCP,IAM,KMS,SG,VPC,Lambda+SQS, etc. 

# Details
- A CLI for easily running AWS EC2 instances (`km create`, `km ls`, `km desktop start`, `km vscode start` ...)
- Out-of-the-box support for the Slack / Github / Hackerone / herdr / vscode / KASM browser liunx / Wiz Sensor; extensible EC2 `UserData` interface
- Secure AWS Architecture balancing privilege with containment/isolation (SSO+Organizations+Accounts, SCP, KMS, SSM, IAM, SG, VPC+NATGW+IGW, Lambda+SQS+SES+S3, Cloudtrail, Budgets)
- Run SKILLS/plugins/prompts with Claude/Codex/Bedrock over Slack/Github/webhooks/email (AWS SES)


### Overview of AWS Services Used
<img alt="image" src="https://github.com/user-attachments/assets/68a8f944-1dd9-4fcf-9fd0-f370cbbe9e70" />

I've been interested in [this topic a long time as an extension of 'home virtual labs'](https://github.com/whereiskurt/kvmlab/blob/v1.0/Virtual%20Lab%20Design%20(KVM).png) but for Claude + friends. 

**Useful for security/engineer practitioners.** if team's coverage includes 100x of repos, and you need to move fast+safely - triaging, patching/PRs, reading code, and reasoning about vulnerabilities - without becoming the next breach.

Isolation and monitoring.

### Overview of Slack/Github Integration


<img alt="image" src="https://github.com/user-attachments/assets/38dc63fd-e441-464c-9512-935d3dff6b12" />

A profile declares what's allowed (ie. egress network) and then is built/instantiated with `km create`. Here's a comprehensive profile example that shows a lot of what's possible:

```yaml
# extends resolves left→right; base/os/redhat must come before base/platform
extends: [base/os/redhat, base/userinit, base/platform, base/security/wiz]

runtime:
  region: us-east-1
  instanceType: m7i.2xlarge
  hibernation: true
  rootVolumeSize: 120
  additionalVolume: { size: 60, mountPoint: /data }
  additionalSnapshots: [{ snapshotId: snap-abcdef01234567890, mountPoint: /repos }]
  mountEFS: true
  efsMountPoint: /shared

execution:
  initCommandsAppend:
    - yum install -y yum-utils
    - yum-config-manager --add-repo https://cli.github.com/packages/rpm/gh-cli.repo
    - yum install -y gh

secrets: { sopsFile: ./secrets/kmprivs.enc.yaml }

network:
  enforcement: both
  egress:
    allowedDNSSuffixes: [
      .amazonaws.com, time.aws.com,                                              # AWS / Bedrock / SSM
      .anthropic.com, .claude.ai, .claude.com, .sentry.io, .cloudfront.net,     # Claude Code
      .statsig.com, .featuregates.org,                                           # Claude Code feature flags
      .openai.com, .chatgpt.com,                                                 # Codex
      .github.com, .githubusercontent.com,                                       # git / gh / plugins
      .npmjs.org, .npmjs.com, .nodejs.org, .npmmirror.com,                       # node
      .pypi.org, .pythonhosted.org,                                              # python
      .pulsemcp.com, .google.com, .google-analytics.com, .googletagmanager.com,  # MCP registry / GA
      .visualstudio.com ]                                                        # VS Code
    allowedHosts: [vscode.download.prss.microsoft.com, nodejs.org]

notification:
  slack: { enabled: true, perSandbox: true, private: true, 
            channelName: "sec-{alias}",
            invites: { emails: [user1@example.com, user2@example.com] },
            inbound: { enabled: true, maxConcurrentThreads: 4 },
            archiveOnDestroy: true }
  github: { inbound: { enabled: true } }
  h1: { inbound: { enabled: true } }
```

Klanker Maker compiles a YAML profile into a real AWS sandbox: a scoped IAM role, a kernel-level network policy, a MITM proxy that meters every Bedrock/Anthropic/OpenAI token, a Slack channel that talks back to the agent, and a dollar ceiling that suspends compute when the money runs out. All of this an isolated AWS Account with AWS SCP policies applied, to further preventing breakouts.

```bash
$ ./km create profiles/g1.yaml
$ ./km list --wide
$ ./km agent run g1 --prompt "investigate the OOM in api-server" --wait
$ ./km destroy g1 --yes
```

The point is to take agentic work off your laptop and put it on AWS at the size the work actually needs - a `t3.medium` for a quick fix, an `r7i.48xlarge` against EFS-backed datasets for a multi-day data pipeline, a GPU box for a training loop, or a crew of Claudes coordinating across all of the above. Drive any of it from a simple `km` invocation, prebuilt `at` AWS EventBridge style schedule, an AWS SES inbound email, or a Slack thread - same control plane, same guardrails.

Isolation is the product. Every sandbox is **default-deny on the network**: an explicit allowlist controls which hosts it can reach, which secrets it can read, and how much it can spend. These are intentional design choices to make **data exfiltration** and **supply-chain compromise** hard by construction - a malicious dependency, a poisoned build step, or a compromised agent has nowhere to phone home and nothing ambient to steal. Patch fast, review at scale, and rationalize about vulns without trusting the thing you're investigating.

<p align="center">
  <a href="https://link.excalidraw.com/p/readonly/IRnJEYKzu0XezsHBg1mx">
    <img src="docs/diagrams/excalidraw.presentation.gif" alt="Klanker Maker architecture slideshow preview" width="720" />
  </a>
  <br />
  <sub>📊 <a href="https://link.excalidraw.com/p/readonly/IRnJEYKzu0XezsHBg1mx">Open the narrated walk-through</a></sub>
</p>

> 📖 **Full documentation lives in the [Klanker Maker Wiki](https://github.com/whereiskurt/klanker-maker/wiki)** — architecture, security model, network enforcement, Slack/GitHub/email integrations, the SandboxProfile reference, and the full CLI reference.

---

## What Klanker Maker Is

Klanker Maker (`km`) is a single Go CLI that turns a Kubernetes-style YAML profile into a self-contained AWS sandbox for running AI agents. Every sandbox gets its own identity, its own network policy, its own dollar budget, and its own Slack thread. The platform itself is cloud-native AWS - EventBridge Scheduler, Lambda dispatchers, DynamoDB global tables, SES, SSM, KMS, SCP - running in your account, under your IAM, on your bill.

There are four useful frames for it:

**1. The runtime.** A sandbox is a compiled policy object. The profile declares what's allowed (egress hosts, repos, regions, spend) and the compiler produces real AWS infrastructure: a Security Group, an IAM role, EBS volumes, EFS mounts, a per-sandbox cgroup with eBPF programs attached, a transparent MITM proxy for L7-required traffic, sidecar systemd services for DNS/HTTP/audit/OTEL. No shared multi-tenant runtime to trust. No container escape surface. The isolation is at the AWS primitive layer.

**2. The fleet manager.** `km` doesn't just create sandboxes - it manages a fleet. A DynamoDB table is the source of truth (`km list`, `km status`, alias lookups). EventBridge Scheduler drives `km at` ("destroy at 5pm Friday", "every Thursday run nightly tests"). Lambda dispatchers handle `km create --remote`, `km destroy --remote`, email-to-create, GitHub App token refresh, TTL expiry, spot interruption, budget enforcement. Sandboxes can be paused (hibernated to disk), stopped, locked, cloned, baked into AMIs, or scheduled to resume.

**3. The integrations layer.** Klanker Maker is built to be the surface a human (or another agent) drives an agent fleet through. A Slack App provides bidirectional chat: `#sb-{id}` channels per sandbox, transcript streaming, `:eyes:` ack reactions, signing-secret-verified Events API webhooks dispatched to per-sandbox SQS FIFO queues. A GitHub App provides per-sandbox short-lived installation tokens scoped to allowlisted repos. SES + Ed25519 lets sandboxes message each other (and the operator) with cryptographically verified sender identity. OTEL captures every prompt, tool call, and API request to S3 for replay.

**4. The work envelope.** Sandboxes scale with the workload. The profile picks the substrate (EC2 spot/on-demand, ECS Fargate, Docker), the instance type (`t3.medium` for a quick fix, `r7i.48xlarge` for an in-memory backtest, a GPU box for fine-tuning), and the storage shape (EBS, additional EBS volume, EFS shared across a crew of agents). The same eBPF + MITM + budget layer wraps all of them. The point isn't "sandbox a coding agent on my laptop" - it's *put Claude (or a dozen Claudes) in front of cloud-scale compute and data, with the policy, identity, and dollar rails wired in by construction*. Heavy data and ML workloads belong on AWS; the agent driving them shouldn't be the part that has to live on your MacBook.

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
