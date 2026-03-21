# Klanker Maker (km)

**A sandbox is a declarative policy object that compiles into a controlled, auditable execution environment.**

Klanker Maker is an open-source, policy-driven sandbox platform. Define execution environments as declarative YAML profiles and compile them into real AWS infrastructure. It provides a Go CLI (`km create/destroy/list/validate`) and a web-based ConfigUI for managing sandbox profiles and monitoring running sandboxes.

The primary use case is spinning up isolated, observable, policy-constrained EC2/ECS environments for agent workloads, but the sandbox model is workload-agnostic.

## How It Works

```
┌─────────────────┐     ┌──────────┐     ┌──────────────┐     ┌─────────────────┐
│ SandboxProfile   │────▶│ Compiler │────▶│  Terragrunt  │────▶│  AWS Infra      │
│ (YAML)           │     │          │     │  Inputs      │     │  (EC2 or ECS)   │
└─────────────────┘     └──────────┘     └──────────────┘     └─────────────────┘
```

1. **Define** a SandboxProfile in YAML — lifecycle, network policy, identity, sidecars, observability
2. **Validate** with `km validate <profile.yaml>`
3. **Create** with `km create <profile>` — compiles to Terragrunt inputs, provisions infrastructure
4. **Monitor** with `km list` / `km status` or the ConfigUI web dashboard
5. **Destroy** with `km destroy <sandbox-id>` — clean teardown of all resources

## SandboxProfile

Profiles use a Kubernetes-style schema at `km.run/v1alpha1`:

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: restricted-dev
  description: Restricted development sandbox with network filtering
extends: open-dev

spec:
  lifecycle:
    ttl: 4h
    idleTimeout: 30m
    teardownPolicy: destroy

  runtime:
    substrate: ec2          # ec2 or ecs
    instanceType: t3.medium
    spot: true              # spot by default

  network:
    egress:
      allowedDNSSuffixes:
        - "*.github.com"
        - "*.amazonaws.com"
      allowedHosts:
        - host: "api.openai.com"
          methods: [GET, POST]

  identity:
    iamRole: arn:aws:iam::role/sandbox-restricted
    sessionDuration: 1h
    regionLock: [us-east-1]

  sourceAccess:
    github:
      allowedRepos:
        - org: "whereiskurt"
          repo: "klanker-maker"
          refs: ["main", "develop"]
          permissions: [clone, fetch]
        - org: "mycompany"
          repo: "*"                  # wildcard — all repos in org
          refs: ["*"]
          permissions: [clone, fetch, push]
      allowedOrgs:
        - "mycompany"               # org-level access grant

  secrets:
    allowedRefs:
      - "arn:aws:ssm:us-east-1::parameter/sandbox/api-key"
      - "arn:aws:ssm:us-east-1::parameter/sandbox/db-password"

  sidecars:
    dnsProxy: { enabled: true }
    httpProxy: { enabled: true }
    auditLog: { enabled: true }
    tracing: { enabled: true }

  observability:
    logDestination: cloudwatch
    commandLog: true
    networkLog: true
    tracing:
      otelCollectorEndpoint: "http://otel-collector:4317"
      mlflow:
        trackingUri: "http://mlflow:5000"
        experimentName: "sandbox-runs"
```

### GitHub Source Access

The `sourceAccess.github` section controls which repositories an agent can interact with inside the sandbox. Everything is allowlist-only — repos not listed are inaccessible.

```yaml
sourceAccess:
  github:
    allowedRepos:
      # Specific repo + branch lock
      - org: "mycompany"
        repo: "api-service"
        refs: ["main", "release/*"]
        permissions: [clone, fetch]

      # All repos in an org, full access
      - org: "mycompany"
        repo: "*"
        refs: ["*"]
        permissions: [clone, fetch, push]

      # Public repo, read-only, pinned to a tag
      - org: "open-source"
        repo: "useful-lib"
        refs: ["v2.1.0"]
        permissions: [clone]

    allowedOrgs:
      - "mycompany"
```

| Field | Description |
|-------|-------------|
| `org` | GitHub organization or user |
| `repo` | Repository name, or `*` for all repos in the org |
| `refs` | Allowed branches, tags, or `*` for any ref. Supports glob patterns like `release/*` |
| `permissions` | `clone`, `fetch`, `push` — granular per-repo |
| `allowedOrgs` | Org-level shorthand — grants access to all repos in the org |

## Built-in Profiles

| Profile | Description |
|---------|-------------|
| `open-dev` | Unrestricted development — full network access, long TTL |
| `restricted-dev` | Network-filtered development — allowlisted DNS/HTTP, audit logging |
| `hardened` | Strict isolation — minimal egress, short TTL, full audit trail |
| `sealed` | Maximum lockdown — no egress, no secrets, read-only filesystem |

Profiles support **inheritance** via the `extends` field — start from a base and override specific sections.

## Security Model

Klanker Maker uses **explicit allowlists everywhere** — if it's not allowed, it's denied.

- **Network**: Security Groups as primary enforcement, DNS proxy + HTTP proxy sidecars for granular filtering
- **Identity**: Scoped IAM role sessions with region lock and configurable duration
- **Secrets**: SSM Parameter Store injection with allowlisted refs, SOPS encryption at rest via KMS
- **Source Access**: Allowlisted GitHub repos, refs, and permissions (clone/fetch/push)
- **Filesystem**: Writable/read-only path enforcement
- **Metadata**: IMDSv2 enforced on all EC2 instances
- **Audit**: Command and network logging with secret redaction
- **Tracing**: OTel traces/spans per sandbox session, MLflow experiment tracking for agent run history

## Architecture

```
km CLI
├── cmd/                    # Entry point
├── internal/app/cmd/       # Cobra commands (create, destroy, list, validate, status)
├── pkg/                    # Reusable libraries (schema, compiler, provisioner)
├── profiles/               # Built-in SandboxProfile YAML files
└── infrastructure/
    ├── modules/            # Terraform modules (network, ec2spot, ecs-*, secrets)
    └── terragrunt/         # site.hcl, service definitions, per-sandbox isolation

ConfigUI (apps/local/configui/)
├── Go HTTP server with embedded web UI
├── Profile editor with inline validation
├── Live sandbox status dashboard
└── AWS resource discovery + SOPS secrets management
```

## Substrates

| Substrate | Implementation | Spot Support |
|-----------|---------------|--------------|
| EC2 | Spot instances by default, on-demand fallback | Spot interruption handling with artifact upload |
| ECS (Fargate) | Fargate Spot capacity provider, sidecar containers in task definition | Graceful task state change handling |

## Roadmap

| Phase | Description | Status |
|-------|-------------|--------|
| 1 | Schema, Compiler & AWS Foundation | Not started |
| 2 | Core Provisioning & Security Baseline | Not started |
| 3 | Sidecar Enforcement & Lifecycle Management | Not started |
| 4 | Lifecycle Hardening, Artifacts & Email | Not started |
| 5 | ConfigUI Web Dashboard | Not started |

See [.planning/ROADMAP.md](.planning/ROADMAP.md) for detailed phase breakdowns and success criteria.

## License

TBD
