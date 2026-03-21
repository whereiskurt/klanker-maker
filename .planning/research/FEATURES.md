# Feature Research

**Domain:** Policy-driven sandbox / execution environment platform (AI agent workloads)
**Researched:** 2026-03-21
**Confidence:** HIGH (cross-verified across E2B, Modal, Fly Machines, gVisor, Firecracker, Kubernetes agent-sandbox, NVIDIA OpenShell, Northflank, Daytona)

---

## Feature Landscape

### Table Stakes (Users Expect These)

Features operators and agent framework authors assume exist. Missing these = platform is not credible.

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Isolated execution environment | Agents run untrusted/LLM-generated code; no isolation = host risk | HIGH | EC2 + VPC provides VM-level isolation; stronger than containers by default. Firecracker/gVisor needed only if multi-tenant density is required. |
| Declarative environment definition | Operators want to version and review sandbox config, not imperative scripts | MEDIUM | Klanker Maker's SandboxProfile YAML covers this. apiVersion/kind/metadata/spec mirrors Kubernetes and NVIDIA OpenShell patterns. |
| Lifecycle management (create/destroy/TTL) | Sandboxes that persist indefinitely accumulate cost and risk | MEDIUM | TTL-based auto-destroy, idle timeout, manual destroy. All leading platforms (E2B 24h cap, Modal 5m–24h) enforce session limits. |
| Network egress control | Unrestricted outbound = data exfiltration, C2 channels, supply chain attacks | HIGH | DNS + HTTP proxy sidecars cover this. Zero-trust default (block all, allowlist) is the industry-recommended posture. |
| Secrets injection (scoped) | Agents need credentials; hardcoding or broad IAM is a security anti-pattern | MEDIUM | SSM Parameter Store refs with explicit allowlist. Must prevent secrets from appearing in logs. |
| Audit log (commands + network) | Compliance, forensics, and debugging all require what the agent did | MEDIUM | Append-only sidecar log. OWASP ASI 2026 and NIST CSF both flag audit trail gaps as critical. |
| CLI for sandbox management | Operators need scriptable control (CI/CD, local development) | LOW | `km create/destroy/list/validate` — standard for any IaC-adjacent tool. |
| Profile validation | Bad configs should fail loudly before provisioning | LOW | `km validate` catches schema errors before AWS spend begins. |
| Status / observability | Operators must know if sandbox is running, idle, or crashed | LOW | `km list` / `km status` + CloudWatch/S3 log destination. |
| Resource constraints (CPU/mem/disk) | Uncontrolled resource usage = cost explosion and noisy-neighbor effects | MEDIUM | EC2 instance type selection + filesystem quotas. All platforms enforce this. |
| IAM scoping / least privilege | Broad IAM = blast radius on compromise | MEDIUM | Role assumption with scoped session, region lock. AWS-specific but essential for AWS-substrate sandboxes. |
| Clean teardown | Leaving dangling VPCs, security groups, IAM roles = cost and drift | MEDIUM | Terragrunt destroy covers this. Teardown policy (destroy/stop/retain) adds flexibility. |

### Differentiators (Competitive Advantage)

Features that set Klanker Maker apart from generic cloud IDEs, managed sandbox APIs, or DIY Terraform.

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| SandboxProfile as policy object | Profile is the source of truth; infrastructure is just its compiled form. Forces declarative-first thinking and enables auditability of what was allowed. | MEDIUM | Core value prop. Kubernetes agent-sandbox and NVIDIA OpenShell validate this pattern as the direction the industry is heading. |
| Profile inheritance (`extends`) | Operators define a base hardened profile once; specific workloads override only what they need. Reduces config drift and mistakes. | MEDIUM | No major managed sandbox platform (E2B, Modal, Fly) supports template inheritance. Kubernetes agent-sandbox has SandboxTemplate but no inheritance. |
| Four built-in profiles (open-dev → sealed) | Gives teams a ladder of constraint. "Start with restricted-dev, harden to sealed when ready." No other platform offers pre-audited named profiles. | LOW | Opinionated defaults reduce operator decision fatigue. Differentiates from DIY Terraform where every team invents their own pattern. |
| Explicit allowlist everywhere | Allowlists are more legible and auditable than deny-lists. Every allowed repo, host, DNS suffix, secret, and command is named in the profile. | MEDIUM | Industry best practice but rarely implemented end-to-end. Modal blocks inbound by default; E2B has no per-sandbox allowlists. |
| GitHub source access controls | Agent workloads need to clone repos; uncontrolled git access = supply chain risk. Profile-level repo/ref/permission allowlist makes this auditable. | MEDIUM | Not offered as a first-class feature by any competing platform. |
| Artifact upload on exit | Agents produce outputs (reports, test results, build artifacts). Controlled upload with size limits prevents both data loss and exfiltration. | LOW | Unique to Klanker Maker's design. Other platforms leave artifact handling to the agent. |
| Filesystem policy (writable/read-only paths) | Fine-grained control over what the agent can write. Prevents ~/.zshrc attacks and protects sensitive directories. | MEDIUM | Identified as non-obvious necessary control by security research (Martin Alderson, 2025). No managed platform enforces this at the profile level. |
| ConfigUI for profile editing + live status | Web-based management reduces onboarding friction. Git-based YAML-only workflows exclude non-CLI operators. | MEDIUM | GitHub Codespaces and Modal have dashboards; none combine profile editor + live sandbox status with declarative compilation. |
| AWS resource discovery in ConfigUI | Operators see what AWS resources the sandbox created, not just Klanker Maker's abstraction. Reduces "what did this actually do?" confusion. | MEDIUM | Unique to Klanker Maker's AWS-first design. |
| Workload-agnostic schema | SandboxProfile spec does not hardcode "AI agent." Any workload (CI runner, research compute, CTF challenge) can use it. | LOW | Widens the addressable use case without extra implementation cost. |
| Secret redaction in audit logs | Log files are high-value attack targets. Pattern-based redaction prevents accidental credential capture in traces. | MEDIUM | Identified as critical by security research but absent from competing platforms' feature lists. |

### Anti-Features (Commonly Requested, Often Problematic)

Features that seem valuable but introduce complexity, scope creep, or security regressions.

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|-----------------|-------------|
| Container substrate (Docker/ECS) | "Faster startup, cheaper than EC2" | Container escape is easier than VM escape; shared kernel means a compromised container can affect other workloads. Modal uses gVisor to compensate, adding its own complexity. EC2 is simpler and proven for v1. | EC2 only for v1. Add ECS/Fargate in v2 if startup time becomes a blocker. |
| Multi-cloud support | "Avoid AWS lock-in" | Schema is already cloud-neutral. Implementation across AWS + GCP + Azure triples provisioning surface area with no v1 user need. | Keep schema namespace `klankermaker.ai/v1alpha1` cloud-agnostic. Implement AWS only. |
| Full OPA / policy engine integration | "More expressive policy" | OPA requires significant operator expertise to author and debug policies. YAML allowlists in SandboxProfile are already more legible for 90% of use cases. | Schema-level policy in SandboxProfile. Add OPA in v2 if enterprise compliance requires it. |
| Real-time collaboration / multi-user | "Team can watch the agent work" | Multi-tenancy requires auth, RBAC, session ownership — major scope increase. Not the primary use case (operator runs agent, not teams). | Single-operator model for v1. ConfigUI shows status without collaborative editing. |
| Persistent sandbox state (warm pool) | "Faster startup by keeping sandboxes warm" | Warm pools accumulate cost, require state management, and create pets-not-cattle risk. Kubernetes agent-sandbox's SandboxWarmPool solves this at a platform level, not at the tool level. | TTL + fast Terragrunt apply. Add warm pool optimization in v2 if startup time is measured and found slow. |
| Cost budgeting / spend limits | "Prevent runaway sandbox costs" | Requires real-time AWS Cost Explorer integration, per-sandbox tagging, alerting — significant infrastructure. Deferred is correct call. | TTL auto-destroy is the primary cost guard. AWS Budgets alerts handle the rest at account level. |
| Deny-list network policy ("block these hosts") | "More flexible than allowlist" | Deny-lists are incomplete by definition; attackers enumerate around them. Allow only known-good is stronger and more auditable. | Allowlist-only DNS and HTTP proxy sidecars. |
| Interactive terminal / IDE in sandbox | "Developers want to shell in and debug" | SSH access into sandboxes creates implicit "pet server" behavior — operators stop treating sandboxes as ephemeral. Conflicts with destroy-on-TTL model. | `km status` + audit logs for debugging. SSH is technically possible via EC2 but should not be a promoted feature. |
| Sandbox API server (persistent object store) | "Manage sandboxes via REST API not CLI" | Requires running a control plane service, database, auth, versioning — full platform shift. CLI + Terraform state is sufficient for v1. | `km` CLI + Terragrunt state. Add API server in v2 if programmatic access is measured as a blocker. |

---

## Feature Dependencies

```
[SandboxProfile YAML schema]
    └──required by──> [km validate]
    └──required by──> [km create]
                          └──required by──> [EC2 provisioning via Terragrunt]
                                                └──required by──> [Network policy sidecars]
                                                └──required by──> [Audit log sidecar]
                                                └──required by──> [IAM role scoping]
                                                └──required by──> [Secrets injection]
                                                └──required by──> [Filesystem policy]
                                                └──required by──> [Lifecycle TTL]

[Profile inheritance (extends)]
    └──enhances──> [SandboxProfile YAML schema]
    └──requires──> [Schema validation (km validate)]

[Built-in profiles (open-dev/restricted-dev/hardened/sealed)]
    └──requires──> [Complete SandboxProfile schema]
    └──enhances──> [km create] (named profile as shorthand)

[ConfigUI profile editor]
    └──requires──> [SandboxProfile YAML schema]
    └──enhances──> [km validate] (UI triggers validation)

[ConfigUI live status]
    └──requires──> [km list / km status]
    └──requires──> [AWS resource discovery]

[GitHub source access]
    └──requires──> [IAM role scoping]
    └──requires──> [Network policy (allowlist GitHub domains)]

[Artifact upload on exit]
    └──requires──> [Lifecycle management (teardown hook)]
    └──requires──> [IAM role with S3/destination write]

[Secret redaction in audit logs]
    └──requires──> [Audit log sidecar]
    └──enhances──> [Secrets injection]

[DNS proxy sidecar] ──conflicts──> [Unrestricted network egress]
[HTTP proxy sidecar] ──conflicts──> [Unrestricted network egress]
```

### Dependency Notes

- **SandboxProfile schema is the root dependency:** Everything else — validation, provisioning, sidecars, UI — reads from the schema. Get the schema right before building anything else.
- **Network policy sidecars require EC2 provisioning:** Sidecars are EC2 processes that must be started with the instance. They cannot be retrofitted after provisioning.
- **Profile inheritance requires schema stability:** Adding `extends` before the schema is stable risks breaking the inheritance model with every schema change. Defer until schema is validated.
- **ConfigUI requires a working CLI:** The UI wraps `km` operations. Build CLI first, UI second.
- **GitHub source access requires network allowlist:** The DNS/HTTP proxy must allowlist `github.com` and `api.github.com` for source access to work. They are not independent features.
- **Artifact upload on exit conflicts with "sandbox as read-only":** Sealed profile (maximum restriction) should treat artifact upload as an explicit opt-in, not default.

---

## MVP Definition

### Launch With (v1)

Minimum viable: a sandbox spins up, runs an agent workload under policy, and tears itself down with an audit trail.

- [ ] SandboxProfile YAML schema (lifecycle, runtime, network, identity, observability sections) — schema is the foundation for everything
- [ ] `km validate` — fail fast before AWS spend
- [ ] `km create` — compile profile to Terragrunt inputs, provision EC2 + VPC + IAM
- [ ] `km destroy` — clean teardown, no dangling resources
- [ ] `km list` / `km status` — operator visibility
- [ ] DNS proxy sidecar — network policy enforcement (allowlist DNS)
- [ ] HTTP proxy sidecar — network policy enforcement (allowlist HTTP/S)
- [ ] Audit log sidecar — command + network log to CloudWatch/S3
- [ ] IAM role scoping — session-scoped, region-locked credentials
- [ ] Secrets injection via SSM — allowlist mode
- [ ] TTL-based auto-destroy — sandboxes are not pets
- [ ] Four built-in profiles — gives operators a starting point without reading the full spec
- [ ] ConfigUI — profile editor + live sandbox status (adapted from defcon.run.34)

### Add After Validation (v1.x)

Add once v1 is used and friction points are measured.

- [ ] Profile inheritance (`extends`) — add when operators report copy-paste between profiles
- [ ] GitHub source access allowlist — add when Goose-on-Klanker Maker is the primary use case
- [ ] Filesystem policy enforcement (writable/read-only paths) — add when audit logs reveal unexpected file writes
- [ ] Artifact upload on exit — add when operators report losing agent outputs
- [ ] Secret redaction in audit logs — add when secrets appear in log samples during dog-fooding
- [ ] AWS resource discovery in ConfigUI — add to reduce "what did this create?" support burden

### Future Consideration (v2+)

Defer until product-market fit and user feedback justify the scope.

- [ ] ECS/Fargate substrate — only if EC2 startup latency is measured as a blocker
- [ ] Cost budgeting / spend limits — only if operators report cost surprises after TTL is in place
- [ ] Sandbox API server (REST control plane) — only if programmatic access is needed by automation
- [ ] Warm pool / pre-provisioned sandboxes — only if Terragrunt apply latency is measured and unacceptable
- [ ] Multi-cloud (GCP, Azure) — schema is already cloud-neutral; defer implementation
- [ ] OPA / policy engine — defer unless enterprise compliance mandates it

---

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| SandboxProfile YAML schema | HIGH | MEDIUM | P1 |
| `km validate` | HIGH | LOW | P1 |
| `km create` (EC2 provisioning) | HIGH | HIGH | P1 |
| `km destroy` | HIGH | LOW | P1 |
| DNS + HTTP proxy sidecars | HIGH | MEDIUM | P1 |
| Audit log sidecar | HIGH | MEDIUM | P1 |
| IAM role scoping | HIGH | MEDIUM | P1 |
| TTL auto-destroy | HIGH | LOW | P1 |
| Four built-in profiles | HIGH | LOW | P1 |
| `km list` / `km status` | MEDIUM | LOW | P1 |
| Secrets injection (SSM) | HIGH | MEDIUM | P1 |
| ConfigUI (profile editor + status) | MEDIUM | MEDIUM | P1 |
| Profile inheritance (`extends`) | MEDIUM | MEDIUM | P2 |
| GitHub source access controls | MEDIUM | MEDIUM | P2 |
| Filesystem policy enforcement | HIGH | MEDIUM | P2 |
| Artifact upload on exit | MEDIUM | LOW | P2 |
| Secret redaction in audit logs | HIGH | MEDIUM | P2 |
| AWS resource discovery in ConfigUI | MEDIUM | MEDIUM | P2 |
| Warm pool / pre-provisioning | LOW | HIGH | P3 |
| Cost budgeting / spend limits | MEDIUM | HIGH | P3 |
| Sandbox REST API server | MEDIUM | HIGH | P3 |
| ECS/Fargate substrate | LOW | HIGH | P3 |
| OPA policy engine | LOW | HIGH | P3 |

**Priority key:**
- P1: Must have for launch
- P2: Should have, add when possible
- P3: Nice to have, future consideration

---

## Competitor Feature Analysis

| Feature | E2B | Modal | Fly Machines | k8s agent-sandbox | Klanker Maker (planned) |
|---------|-----|-------|--------------|-------------------|-----------------|
| Isolation substrate | Firecracker microVM | gVisor | microVM (Firecracker) | Configurable (GKE Sandbox) | EC2 (VM-level) |
| Startup time | ~150ms | <1s | <1s | Seconds (pod spin-up) | ~30-120s (Terragrunt apply) |
| Declarative profile | Template (pre-built) | Runtime-defined | Image-based | SandboxTemplate YAML | SandboxProfile YAML |
| Profile inheritance | No | No | No | No (SandboxTemplate is flat) | Yes (`extends`) |
| Named built-in profiles | No | No | No | No | Yes (4 profiles) |
| Network allowlist (DNS) | No | Egress policies (limited) | No | NetworkPolicy (k8s) | Yes (DNS proxy sidecar) |
| Network allowlist (HTTP) | No | Egress policies | No | No | Yes (HTTP proxy sidecar) |
| Audit log (command + network) | No | Dashboard metrics | No | No | Yes (sidecar) |
| IAM / credential scoping | No (managed service) | No (managed service) | No | ServiceAccount | Yes (AWS IAM, session-scoped) |
| Secrets injection | No | Environment vars | Secrets (fly secrets) | Kubernetes Secrets | Yes (SSM allowlist) |
| TTL / auto-destroy | Yes (24h max) | Yes (5m–24h) | Manual | No | Yes (configurable TTL + idle timeout) |
| GitHub source controls | No | No | No | No | Yes (repo/ref/permission allowlist) |
| Filesystem policy | No | No | No | No | Yes (writable/read-only paths) |
| Artifact upload on exit | No | No | Volume persistence | No | Yes (with size limits) |
| Web dashboard | No | Yes (metrics) | flyctl only | No | Yes (ConfigUI) |
| AWS resource visibility | N/A | N/A | N/A | N/A | Yes (ConfigUI) |
| Open source | Yes | No | No | Yes | Yes |
| Self-hostable | Yes (E2B Cloud is managed) | No | No | Yes (GKE) | Yes (your AWS account) |

---

## Sources

- [E2B documentation and GitHub](https://github.com/e2b-dev/E2B) — HIGH confidence
- [Modal Sandboxes product page](https://modal.com/use-cases/sandboxes) — HIGH confidence
- [Northflank: Top AI sandbox platforms 2026](https://northflank.com/blog/top-ai-sandbox-platforms-for-code-execution) — MEDIUM confidence (vendor blog, cross-verified)
- [Northflank: How to sandbox AI agents](https://northflank.com/blog/how-to-sandbox-ai-agents) — MEDIUM confidence
- [Northflank: E2B vs Modal](https://northflank.com/blog/e2b-vs-modal) — MEDIUM confidence
- [Kubernetes agent-sandbox project](https://github.com/kubernetes-sigs/agent-sandbox) — HIGH confidence (official Kubernetes SIG)
- [Google Open Source Blog: Agent Sandbox announcement](https://opensource.googleblog.com/2025/11/unleashing-autonomous-ai-agents-why-kubernetes-needs-a-new-standard-for-agent-execution.html) — HIGH confidence
- [Firecracker microVM GitHub](https://github.com/firecracker-microvm/firecracker) — HIGH confidence (official)
- [gVisor security model](https://gvisor.dev/docs/architecture_guide/security/) — HIGH confidence (official)
- [Fly Machines overview](https://fly.io/docs/machines/overview/) — HIGH confidence (official docs)
- [Martin Alderson: Why sandboxing coding agents is harder than you think](https://martinalderson.com/posts/why-sandboxing-coding-agents-is-harder-than-you-think/) — MEDIUM confidence (practitioner analysis)
- [Modal blog: Top code agent sandbox products](https://modal.com/blog/top-code-agent-sandbox-products) — MEDIUM confidence (vendor blog)
- [NVIDIA OpenShell announcement (GTC 2026)](https://ubos.tech/news/nvidia-unveils-openshell-a-secure-runtime-for-autonomous-ai-agents/) — MEDIUM confidence (secondary coverage)
- [Daytona vs E2B comparison (Northflank)](https://northflank.com/blog/daytona-vs-e2b-ai-code-execution-sandboxes) — MEDIUM confidence

---

*Feature research for: Policy-driven sandbox / execution environment platform (Klanker Maker)*
*Researched: 2026-03-21*
