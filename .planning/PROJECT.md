# Klanker Maker

## What This Is

Klanker Maker is an open-source, policy-driven sandbox platform that lets you define execution environments as declarative YAML profiles and compile them into real AWS infrastructure. It provides a Go CLI (`km create/destroy/list/validate`) and a web-based ConfigUI for managing sandbox profiles and monitoring running sandboxes. The primary use case is spinning up isolated, observable, policy-constrained EC2 environments for agent workloads (like Goose), but the sandbox model is workload-agnostic.

## Core Value

A sandbox is a declarative policy object that compiles into a controlled, auditable execution environment — policy defines what's allowed, infrastructure is just the compiled artifact.

## Requirements

### Validated

<!-- Shipped and confirmed valuable. -->

(None yet — ship to validate)

### Active

<!-- Current scope. Building toward these. -->

- [ ] SandboxProfile YAML schema with validation (apiVersion, kind, metadata, spec)
- [ ] Schema supports: lifecycle, runtime, execution, sourceAccess, network, identity, sidecars, observability sections
- [ ] Profile inheritance via `extends` field
- [ ] `km validate` — validate a SandboxProfile YAML against the schema
- [ ] `km create <profile>` — compile profile to Terragrunt inputs, apply EC2 + IAM + networking
- [ ] `km destroy <sandbox-id>` — tear down a running sandbox
- [ ] `km list` / `km status` — show running sandboxes and their state
- [ ] EC2 sandbox provisioning via Terraform/Terragrunt (VPC, security groups, IAM roles, EC2 instance)
- [ ] Network policy enforcement — DNS proxy sidecar for allowlist DNS filtering
- [ ] Network policy enforcement — HTTP proxy sidecar for allowlist HTTP filtering
- [ ] Audit log sidecar — command logging and network logging
- [ ] Identity management — AWS IAM role assumption with scoped session duration and regions
- [ ] GitHub source access — allowlist mode with repo/ref/permission controls
- [ ] Secrets injection — allowlist mode with SSM Parameter Store refs
- [ ] Lifecycle management — TTL-based auto-destroy, idle timeout, teardown policy (destroy/stop/retain)
- [ ] Observability — command log, network log, configurable log destination (CloudWatch/S3/stdout)
- [ ] Filesystem policy — writable/read-only path enforcement
- [ ] Artifact upload on exit with size limits
- [ ] Four built-in profiles: open-dev, restricted-dev, hardened, sealed
- [ ] ConfigUI web dashboard — profile editor + live sandbox status
- [ ] ConfigUI — AWS resource discovery and status

### Out of Scope

<!-- Explicit boundaries. Includes reasoning to prevent re-adding. -->

- ECS/Fargate substrate — v1 is EC2 only, keeps scope manageable
- Multi-cloud support — AWS only for now, schema is cloud-neutral but implementation is AWS
- Full policy engine / OPA integration — schema-level policy is sufficient for v1
- Mobile app — web UI only
- Multi-tenancy / user management — single operator for v1
- Cost budgeting / spend limits — deferred to v2
- Sandbox-as-API-object — v1 uses CLI + Terraform state, not a persistent API server
- Docker/local substrate — EC2 only for v1

## Context

Klanker Maker is built on proven infrastructure patterns from defcon.run.34 (`~/working/defcon.run.34`), specifically:

- **Terraform modules**: network (VPC, ALB, security groups), ec2spot, secrets (SSM/Secrets Manager) — adapted and renamed for sandbox provisioning
- **ConfigUI**: Go HTTP server with embedded web UI for infrastructure management, AWS integration, SOPS secrets, terminal multiplexing — becomes Klanker Maker's management dashboard
- **Terragrunt patterns**: site.hcl configuration, service definitions, multi-region deployment patterns

The Go CLI follows the architecture established in tiogo (`github.com/whereiskurt/tiogo`):
- `cmd/` entry point → `internal/app/cmd/` Cobra commands → `pkg/` reusable libraries
- Central Config struct with Viper integration, passed via dependency injection
- Logrus structured logging, embedded templates, AES credential encryption

The SandboxProfile schema uses Kubernetes-style `apiVersion/kind/metadata/spec` structure at `klankermaker.ai/v1alpha1`.

## Constraints

- **Substrate**: EC2 only for v1 — keeps compilation simple and proven
- **Provisioning**: Terragrunt apply — SandboxProfile compiles to terragrunt inputs
- **Language**: Go CLI + Go API, TypeScript ConfigUI frontend
- **Security model**: Explicit allowlists everywhere — allowed repos, hosts, DNS suffixes, secrets, commands
- **Timeline**: Target 1-2 weeks for v1 functional sandbox lifecycle
- **Infrastructure source**: Adapt modules from defcon.run.34, don't rebuild from scratch

## Key Decisions

<!-- Decisions that constrain future work. Add throughout project lifecycle. -->

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| EC2 only for v1 | Keeps compilation path simple, proven infra from defcon.run.34 | — Pending |
| Terragrunt for provisioning | Profile → terragrunt inputs → apply; consistent with existing patterns | — Pending |
| Allowlist-first security | Explicit allows beat broad access + denies; more legible and secure | — Pending |
| SandboxProfile vs Sandbox separation | Profile = reusable template, Sandbox = instantiated; prevents pet servers | — Pending |
| tiogo-style Go architecture | Cobra/Viper, internal/pkg layout, Config DI — proven across multiple projects | — Pending |
| Three sidecars in v1 (DNS proxy, HTTP proxy, audit log) | Full enforcement from day one, not retrofit later | — Pending |
| ConfigUI from defcon.run.34 | Proven Go HTTP server + web UI, adapt rather than rebuild | — Pending |

---
*Last updated: 2026-03-21 after initialization*
