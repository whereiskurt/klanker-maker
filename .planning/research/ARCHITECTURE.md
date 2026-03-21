# Architecture Research

**Domain:** Policy-driven sandbox/execution environment platform
**Researched:** 2026-03-21
**Confidence:** HIGH (grounded in reference implementation at defcon.run.34 + well-established patterns)

## Standard Architecture

### System Overview

```
┌────────────────────────────────────────────────────────────────────┐
│                        Operator Interface Layer                     │
├───────────────────────────────────┬────────────────────────────────┤
│  Go CLI (km)                  │  TypeScript ConfigUI            │
│  create / destroy / list          │  Profile editor + live status   │
│  validate / status                │  AWS resource discovery         │
└───────────────────────────────────┴────────────────────────────────┘
                          │                   │
                          ▼                   ▼
┌────────────────────────────────────────────────────────────────────┐
│                        Compilation Layer                            │
├────────────────────────────────────────────────────────────────────┤
│  SandboxProfile (YAML)                                              │
│    apiVersion: klankermaker.ai/v1alpha1                                  │
│    kind: SandboxProfile                                             │
│    spec: { lifecycle, runtime, network, identity, sidecars, ... }  │
│                    │                                                │
│                    ▼                                                │
│  Profile Compiler (Go)                                              │
│    - Schema validation (JSON Schema / Go structs)                   │
│    - Inheritance resolution (extends field)                         │
│    - Output: terragrunt.hcl inputs + user-data script              │
└────────────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌────────────────────────────────────────────────────────────────────┐
│                        Provisioning Layer                           │
├────────────────────────────────────────────────────────────────────┤
│  Terragrunt (orchestrator)                                          │
│    └── Terraform modules                                            │
│          ├── network   (VPC, subnets, security groups)              │
│          ├── ec2spot   (instance, key pair, spot config)            │
│          ├── iam       (role, instance profile, session policy)     │
│          └── secrets   (SSM Parameter Store refs)                   │
└────────────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌────────────────────────────────────────────────────────────────────┐
│                        Sandbox Instance (EC2)                       │
├────────────────────────────────────────────────────────────────────┤
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │  Workload Process (agent / tool / arbitrary exec)            │  │
│  └──────────────────────────────────────────────────────────────┘  │
│  ┌──────────────┐  ┌──────────────┐  ┌────────────────────────┐   │
│  │  DNS Proxy   │  │  HTTP Proxy  │  │  Audit Log Sidecar     │   │
│  │  (allowlist  │  │  (allowlist  │  │  (command log +        │   │
│  │  DNS filter) │  │  HTTP filter)│  │   network log)         │   │
│  └──────────────┘  └──────────────┘  └────────────────────────┘   │
│  ┌────────────────────────────────────────────────────────────┐    │
│  │  Tracing Sidecar (OTel collector + MLflow run logger)      │    │
│  │  Collects traces/spans, propagates context through proxies │    │
│  └────────────────────────────────────────────────────────────┘    │
│  iptables DNAT rules intercept all outbound DNS (53) + HTTP (80,   │
│  443) traffic before it leaves the instance and route through       │
│  local proxy processes                                              │
└────────────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌────────────────────────────────────────────────────────────────────┐
│                        Observability Layer                          │
├────────────────────────────────────────────────────────────────────┤
│  ┌────────────────┐  ┌───────────────┐  ┌──────────────────────┐  │
│  │  CloudWatch    │  │  S3 Bucket    │  │  stdout (local dev)  │  │
│  │  (log groups)  │  │  (audit dump) │  │                      │  │
│  └────────────────┘  └───────────────┘  └──────────────────────┘  │
│  ┌────────────────┐  ┌───────────────┐                            │
│  │  OTel          │  │  MLflow       │                            │
│  │  Collector     │  │  Tracking     │                            │
│  └────────────────┘  └───────────────┘                            │
└────────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility | Implementation |
|-----------|----------------|----------------|
| SandboxProfile YAML | Declarative policy object: what is allowed, how long, what can be accessed | YAML with Kubernetes-style `apiVersion/kind/metadata/spec` |
| Profile Compiler | Translate profile spec → Terragrunt `inputs` block + `user-data` script | Go library, invoked by CLI |
| `km` CLI | User-facing lifecycle: validate, create, destroy, list, status | Go + Cobra/Viper (`cmd/` → `internal/app/cmd/` → `pkg/`) |
| Terragrunt | Orchestrate Terraform module execution with compiled inputs | Existing HCL pattern from defcon.run.34 |
| Terraform modules | Provision actual AWS resources: VPC, EC2, IAM, security groups | Adapted from defcon.run.34 `modules/network`, `modules/ec2spot`, `modules/secrets` |
| user-data script | Bootstrap sandbox on first boot: install sidecars, configure iptables, start processes | Bash, embedded in compiled output |
| DNS Proxy sidecar | Intercept all DNS queries, allow only allowlisted suffixes/hosts | Small Go or dnscrypt-proxy process; iptables DNAT port 53 |
| HTTP Proxy sidecar | Intercept all HTTP/HTTPS traffic, enforce domain allowlist | Squid or custom Go proxy; iptables DNAT port 80/443 |
| Audit Log sidecar | Record commands executed and network connections made | Go daemon tailing shell history + proxy logs; ships to log destination |
| Tracing sidecar | Collect OTel traces/spans from workload, propagate trace context through proxies, log MLflow runs per sandbox session | Go process running OTel collector agent + MLflow client; exports to configured endpoints |
| ConfigUI | Web dashboard: profile editor, sandbox status, AWS resource view | TypeScript frontend + Go HTTP server (adapted from defcon.run.34 `apps/`) |
| Terraform state | Source of truth for running sandboxes and their current state | S3 backend (existing pattern) |

## Recommended Project Structure

```
km/
├── cmd/
│   └── km/
│       └── main.go              # Binary entry point
├── internal/
│   └── app/
│       ├── cmd/                 # Cobra command implementations
│       │   ├── create.go        # km create <profile>
│       │   ├── destroy.go       # km destroy <sandbox-id>
│       │   ├── list.go          # km list
│       │   ├── status.go        # km status <sandbox-id>
│       │   └── validate.go      # km validate <profile.yaml>
│       └── config/
│           └── config.go        # Central Config struct (Viper-backed)
├── pkg/
│   ├── profile/                 # SandboxProfile schema + validation
│   │   ├── schema.go            # Go struct definitions (apiVersion/kind/spec)
│   │   ├── validate.go          # Schema validation logic
│   │   └── inherit.go           # extends field resolution
│   ├── compiler/                # Profile → Terragrunt inputs + user-data
│   │   ├── compiler.go          # Main compilation entry point
│   │   ├── inputs.go            # Generate terragrunt inputs HCL
│   │   └── userdata.go          # Generate EC2 user-data script
│   ├── terragrunt/              # Terragrunt execution wrapper
│   │   ├── runner.go            # Apply / destroy / output calls
│   │   └── state.go             # Read sandbox state from TF outputs
│   └── aws/                     # AWS SDK helpers
│       ├── ec2.go               # Instance status, describe
│       └── iam.go               # Role assumption, session scoping
├── infra/
│   ├── modules/                 # Terraform modules (from defcon.run.34)
│   │   ├── network/             # VPC, subnets, security groups
│   │   ├── ec2spot/             # EC2 instance provisioning
│   │   ├── iam/                 # Role, instance profile, policies
│   │   └── secrets/             # SSM Parameter Store integration
│   └── live/
│       └── sandbox/
│           ├── site.hcl         # Global site config (region, backend)
│           └── terragrunt.hcl   # Sandbox-specific inputs (compiled output lands here)
├── profiles/                    # Built-in SandboxProfile YAML templates
│   ├── open-dev.yaml
│   ├── restricted-dev.yaml
│   ├── hardened.yaml
│   └── sealed.yaml
├── sidecars/                    # Sidecar source / configuration templates
│   ├── dns-proxy/               # DNS allowlist proxy
│   ├── http-proxy/              # HTTP/HTTPS allowlist proxy (Squid config templates)
│   ├── audit-log/               # Audit log daemon
│   └── tracing/                 # OTel trace collector + MLflow run logger
└── ui/                          # TypeScript ConfigUI (adapted from defcon.run.34)
    ├── src/
    │   ├── components/
    │   │   ├── ProfileEditor/   # YAML profile editor
    │   │   └── SandboxStatus/   # Live sandbox monitoring
    │   └── api/                 # Calls to Go HTTP server
    └── server/                  # Go HTTP server (serves UI + proxies AWS calls)
```

### Structure Rationale

- **`pkg/profile/`:** Schema and validation are decoupled from compilation. The CLI calls validate independently (`km validate`) and the compiler calls it internally before generating outputs.
- **`pkg/compiler/`:** Isolated compilation logic makes it testable without invoking Terraform. Input generation and user-data generation are separate files because they have different output targets.
- **`infra/modules/`:** Terraform modules are stable versioned artifacts (following defcon.run.34 pattern). The CLI never edits them — only the `live/sandbox/terragrunt.hcl` inputs file changes between sandbox instances.
- **`profiles/`:** Built-in profiles ship with the binary as embedded files. User-defined profiles live wherever the operator keeps them.
- **`sidecars/`:** Sidecar binaries or configuration templates are compiled into the user-data bootstrap script. Keeping them in a dedicated directory makes version control and testing straightforward.

## Architectural Patterns

### Pattern 1: Compile → Provision Separation

**What:** The profile compiler produces static artifacts (HCL inputs, user-data script) that are then handed to Terragrunt. The CLI does not call AWS APIs directly for provisioning — that is entirely Terraform's job.

**When to use:** Always. This is the core Klanker Maker architecture. It keeps the compiler testable (no real AWS needed), keeps provisioning idempotent (Terraform handles drift), and keeps audit trails clean (compiled inputs are diffs against state).

**Trade-offs:** Slightly longer feedback loop for `km create` (compile → terragrunt apply). The upside is full Terraform safety guarantees: plan before apply, state tracking, destroy idempotency.

**Example:**
```go
// pkg/compiler/compiler.go
func Compile(profile *profile.SandboxProfile) (*CompiledArtifacts, error) {
    inputs, err := generateInputs(profile)      // → terragrunt inputs block
    userdata, err := generateUserData(profile)  // → EC2 user-data script
    return &CompiledArtifacts{Inputs: inputs, UserData: userdata}, nil
}
```

### Pattern 2: Sidecar-per-Instance with iptables Interception

**What:** Each sandbox EC2 instance runs three local sidecar processes: a DNS proxy, an HTTP proxy, and an audit log daemon. iptables DNAT rules redirect all outbound traffic through the local proxies before it leaves the instance. The workload process does not know about the proxies — enforcement is transparent.

**When to use:** Always for Klanker Maker sandboxes. The transparency is intentional: workloads written for unrestricted environments run unchanged in sandboxes, and the enforcement is non-bypassable from userspace (iptables rules run in kernel).

**Trade-offs:** Requires iptables configuration in user-data. HTTPS inspection requires the HTTP proxy to act as a CONNECT terminator (or enforce SNI allowlists without full MITM). Full HTTPS content inspection is complex — allowlist by domain at the TLS SNI level is the practical approach.

**Example (user-data snippet):**
```bash
# Redirect DNS to local proxy on port 5300
iptables -t nat -A OUTPUT -p udp --dport 53 -j REDIRECT --to-ports 5300
iptables -t nat -A OUTPUT -p tcp --dport 53 -j REDIRECT --to-ports 5300

# Redirect HTTP/HTTPS to local Squid on port 3128
iptables -t nat -A OUTPUT -p tcp --dport 80  -j REDIRECT --to-ports 3128
iptables -t nat -A OUTPUT -p tcp --dport 443 -j REDIRECT --to-ports 3128
```

### Pattern 3: Profile Inheritance (extends)

**What:** A SandboxProfile can declare `extends: restricted-dev` to inherit all fields from a base profile and override only the differences. Resolution happens in the compiler before validation.

**When to use:** When teams want project-specific profiles that vary slightly from a built-in baseline. Prevents copy-paste drift across profiles.

**Trade-offs:** Inheritance depth must be bounded (max 1-2 levels) to keep mental models simple. Circular extends must be detected at compile time.

**Example:**
```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: agent-goose
extends: restricted-dev          # inherits all restricted-dev fields
spec:
  network:
    allowedDomains:
      - "*.github.com"           # adds to parent's domain allowlist
  lifecycle:
    ttl: 2h                      # overrides parent's ttl
```

### Pattern 4: Sandbox vs. SandboxProfile Separation

**What:** A `SandboxProfile` is a reusable template. A `Sandbox` is an instantiated running environment with its own ID, timestamps, and state. `km create` takes a profile and produces a sandbox ID. Destroying a sandbox does not affect the profile.

**When to use:** Always. This prevents "pet servers" — you always know the authoritative definition of what should be running (the profile), not just what is running (the instance).

**Trade-offs:** State must be tracked separately from the profile. For v1, Terraform state serves this purpose (sandbox ID = Terraform workspace or unique resource tag).

## Data Flow

### km create Flow

```
Operator runs: km create agent-goose.yaml
    │
    ▼
[CLI: cmd/create.go]
    Load profile YAML from path
    │
    ▼
[pkg/profile: Validate]
    Resolve extends inheritance
    Validate against schema
    Return typed SandboxProfile struct
    │
    ▼
[pkg/compiler: Compile]
    Generate Terraform inputs block (HCL)
    Generate EC2 user-data bootstrap script
    Write artifacts to working directory
    │
    ▼
[pkg/terragrunt: Runner.Apply]
    cd infra/live/sandbox/
    terragrunt apply --var-file=<compiled inputs>
    Stream Terraform output to operator
    │
    ▼
[AWS: Provisioning]
    VPC + subnets created (or reused)
    EC2 instance launched with user-data
    IAM role attached with scoped policy
    SSM secrets injected
    │
    ▼
[EC2 user-data execution]
    Install sidecar binaries
    Configure iptables rules (DNS + HTTP redirect)
    Start DNS proxy process (allowlist from compiled config)
    Start HTTP proxy process (allowlist from compiled config)
    Start audit log daemon (ship to configured destination)
    Signal "ready"
    │
    ▼
[CLI: Output sandbox ID to operator]
    sandbox-id: sandbox-a1b2c3d4
    status: running
    expires: 2026-03-21T14:00:00Z
```

### km destroy Flow

```
Operator runs: km destroy sandbox-a1b2c3d4
    │
    ▼
[CLI: cmd/destroy.go]
    Resolve sandbox-id → Terraform workspace / tags
    │
    ▼
[pkg/terragrunt: Runner.Destroy]
    terragrunt destroy --target=<sandbox resources>
    │
    ▼
[AWS: Teardown]
    EC2 terminated, EIP released, IAM role detached
    Artifact upload triggered (if configured)
    Audit logs flushed to destination before teardown
```

### Audit Log Data Flow

```
[Workload process] → shell commands → [Audit Log daemon] → [Log destination]
[DNS Proxy]        → DNS query log   → [Audit Log daemon] → [Log destination]
[HTTP Proxy]       → request log     → [Audit Log daemon] → [Log destination]

Log destination (per profile spec):
    CloudWatch Logs group: /km/sandboxes/<sandbox-id>/
    S3:                    s3://<bucket>/km/<sandbox-id>/audit.log
    stdout:                (local dev only)
```

### ConfigUI Data Flow

```
Browser → TypeScript UI → Go HTTP server (ui/server/)
                              │
                              ├── Reads profile YAML files from disk
                              ├── Calls pkg/compiler to validate profiles
                              ├── Calls AWS SDK for sandbox status (EC2 describe)
                              └── Reads Terraform state outputs for sandbox list
```

## Build Order (Dependencies)

Components have hard dependencies that dictate build order:

```
1. SandboxProfile schema + validation (pkg/profile/)
   No dependencies. Everything else depends on it.

2. Profile compiler (pkg/compiler/)
   Depends on: pkg/profile/

3. Terraform modules (infra/modules/)
   Depends on: design decisions from compiler output (what variables are needed)
   Parallel track with compiler.

4. Terragrunt runner (pkg/terragrunt/)
   Depends on: infra/modules/ being callable

5. Go CLI commands (internal/app/cmd/)
   Depends on: pkg/profile/ + pkg/compiler/ + pkg/terragrunt/

6. Sidecar processes (sidecars/)
   Depends on: profile schema (what allowlists look like in compiled form)
   Can develop in parallel with CLI.

7. ConfigUI (ui/)
   Depends on: Go HTTP server having endpoints; CLI having sandbox lifecycle working.
   Build last — it's a monitoring layer over the working system.
```

In practice: Phase 1 = schema + compiler + Terraform modules (the compilation pipeline). Phase 2 = CLI + sidecars (live sandboxes). Phase 3 = ConfigUI (visibility layer).

## Anti-Patterns

### Anti-Pattern 1: Generating Terraform HCL by String Interpolation

**What people do:** Build Terragrunt inputs files using `fmt.Sprintf` or Go template strings that concatenate HCL syntax.

**Why it's wrong:** HCL has quoting rules, escaping rules, and block syntax that differ from JSON/YAML. String concatenation produces files that silently fail to parse, or worse, parse with different semantics than intended.

**Do this instead:** Use the `hashicorp/hcl/v2` library to write HCL programmatically, or generate JSON-format `.tfvars.json` files (Terraform accepts these natively and JSON has unambiguous escaping rules). JSON tfvars are safer and simpler than generated HCL.

### Anti-Pattern 2: Embedding Enforcement in the Workload

**What people do:** Ask the agent workload to self-enforce network restrictions (only call allowed domains, log its own commands).

**Why it's wrong:** Self-enforcement is bypassable. Any code with shell access can disable its own enforcement. The value of a sandbox is that enforcement is external to the workload.

**Do this instead:** All enforcement (DNS, HTTP, filesystem, command audit) must live in sidecar processes and kernel rules (iptables, Linux capabilities, seccomp). The workload is assumed hostile.

### Anti-Pattern 3: One Terraform Workspace per Sandbox

**What people do:** Create a new Terraform workspace for every sandbox instance to isolate state.

**Why it's wrong:** Workspaces share a backend configuration and are not a security boundary. Managing per-workspace state becomes complex. Workspace names are global, creating naming collision risk.

**Do this instead:** Use Terragrunt's per-directory pattern — each sandbox gets a unique directory under `infra/live/sandbox/<sandbox-id>/` with its own `terragrunt.hcl`. State files are isolated by directory prefix in S3. This is the pattern defcon.run.34 uses and Terragrunt is designed for.

### Anti-Pattern 4: TTL Enforcement in the CLI Process

**What people do:** Have `km create` spawn a background process that sleeps until TTL expires and then calls `km destroy`.

**Why it's wrong:** The CLI process can be killed, the operator's laptop can close, or the background process can be missed. TTL enforcement cannot be reliable if it lives in the client.

**Do this instead:** Implement TTL via AWS EventBridge Scheduler or a Lambda function triggered at sandbox creation time that calls the destroy path. For v1, a simpler alternative is a cron-like daemon running on the EC2 instance itself that self-terminates at TTL and signals CloudWatch for state reconciliation. The CLI is stateless.

## Integration Points

### External Services

| Service | Integration Pattern | Notes |
|---------|---------------------|-------|
| AWS EC2 | Terraform module; CLI reads instance state via SDK | Instance state is ground truth for sandbox liveness |
| AWS IAM | Terraform creates role + instance profile; profile spec defines permissions | Session policy scoping limits blast radius |
| AWS SSM Parameter Store | Terraform outputs secret ARNs; user-data fetches secrets at boot | Secrets never appear in compiled artifacts |
| AWS CloudWatch Logs | Audit log sidecar ships directly via CloudWatch agent or SDK | Log group named by sandbox ID for isolation |
| AWS S3 | Artifact upload at sandbox exit; optional audit log destination | Size limit enforced by sidecar before upload |
| GitHub | SSH deploy key or HTTPS token injected from SSM; allowlist enforced by HTTP proxy | Repo/ref allowlist in profile spec |

### Internal Boundaries

| Boundary | Communication | Notes |
|----------|---------------|-------|
| CLI ↔ Compiler | Direct Go function call (same binary) | Compiler is a library, not a service |
| CLI ↔ Terragrunt | `os/exec` subprocess; parse stdout/stderr | Terragrunt is external binary; wrap with timeout |
| Compiler → Terraform artifacts | Write files to filesystem (HCL/JSON + shell script) | Artifacts are stateless — regenerate on demand |
| ConfigUI frontend ↔ Go HTTP server | HTTP/JSON REST API | Go server serves the TypeScript bundle and exposes API endpoints |
| Go HTTP server ↔ AWS | AWS SDK (Go) with instance profile credentials | Server does not hold long-lived AWS credentials |
| Sidecar ↔ Workload | Kernel (iptables); no explicit API | Sidecars are transparent to workload by design |
| Audit daemon ↔ Log destination | CloudWatch Logs PutLogEvents or S3 PutObject | Buffered; flush on SIGTERM before instance terminates |

## Scaling Considerations

| Scale | Architecture Adjustments |
|-------|--------------------------|
| 1-10 sandboxes | Single operator, CLI-driven, local Terraform state acceptable for development |
| 10-100 sandboxes | S3 backend for Terraform state (already recommended), sandbox listing needs a state index (DynamoDB table or S3 manifest) |
| 100+ sandboxes | TTL enforcement must move to server-side scheduler (EventBridge); ConfigUI needs pagination; Terraform apply concurrency becomes a bottleneck (consider async job queue) |

### Scaling Priorities

1. **First bottleneck:** Sandbox list/status — `km list` must scan Terraform state files or AWS tags across N sandboxes. At 50+ this becomes slow. An index (DynamoDB or S3 manifest updated at create/destroy time) solves this. V1 can skip this; it becomes necessary by v2.
2. **Second bottleneck:** Audit log volume — a busy agent workload generates significant log volume. CloudWatch Log Insights handles this at scale; S3 + Athena for bulk analysis. No architecture change needed, just configuration.

## Sources

- OpenSandbox architecture overview (Alibaba, 2026): [open-sandbox.ai/overview/architecture](https://open-sandbox.ai/overview/architecture)
- Northflank sandbox platform analysis: [northflank.com/blog/remote-code-execution-sandbox](https://northflank.com/blog/remote-code-execution-sandbox)
- Squid transparent proxy with iptables REDIRECT: [wiki.squid-cache.org/ConfigExamples/Intercept/LinuxRedirect](https://wiki.squid-cache.org/ConfigExamples/Intercept/LinuxRedirect)
- Squid at-source DNAT pattern: [wiki.squid-cache.org/ConfigExamples/Intercept/AtSource](https://wiki.squid-cache.org/ConfigExamples/Intercept/AtSource)
- AWS DNS filtering with Squid NAT instance: [aws.amazon.com/blogs/security/how-to-add-dns-filtering-to-your-nat-instance-with-squid](https://aws.amazon.com/blogs/security/how-to-add-dns-filtering-to-your-nat-instance-with-squid/)
- Sidecar pattern: [learn.microsoft.com/azure/architecture/patterns/sidecar](https://learn.microsoft.com/en-us/azure/architecture/patterns/sidecar)
- IaC architecture patterns with Terragrunt: [spacelift.io/blog/iac-architecture-patterns-terragrunt](https://spacelift.io/blog/iac-architecture-patterns-terragrunt)
- Go CLI Cobra/Viper architecture: [glukhov.org/post/2025/11/go-cli-applications-with-cobra-and-viper](https://www.glukhov.org/post/2025/11/go-cli-applications-with-cobra-and-viper/)
- Reference implementation: defcon.run.34 (`infra/terraform/modules/`, `infra/terraform/live/`) — HIGH confidence, inspected directly

---
*Architecture research for: policy-driven sandbox/execution environment platform (Klanker Maker)*
*Researched: 2026-03-21*
