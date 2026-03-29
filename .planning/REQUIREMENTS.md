# Requirements: Klanker Maker

**Defined:** 2026-03-21
**Core Value:** A sandbox is a declarative policy object that compiles into a controlled, auditable execution environment

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.

### Schema & Validation

- [x] **SCHM-01**: Operator can define a SandboxProfile in YAML with apiVersion, kind, metadata, spec sections
- [x] **SCHM-02**: Schema supports lifecycle, runtime, execution, sourceAccess, network, identity, sidecars, observability, policy, and agent sections
- [x] **SCHM-03**: Operator can run `km validate <profile.yaml>` and get clear error messages for invalid profiles
- [x] **SCHM-04**: Profile can extend a base profile via `extends` field, inheriting and overriding specific sections (code exists, needs verification — Phase 7) (verified Phase 7 — inherit_test.go passes)
- [x] **SCHM-05**: Four built-in profiles ship with Klanker Maker: open-dev, restricted-dev, hardened, sealed (code exists, needs verification — Phase 7) (verified Phase 7 — builtins_test.go passes)

### Provisioning & Lifecycle

- [x] **PROV-01**: Operator can run `km create <profile>` to compile profile into Terragrunt inputs and provision EC2 + VPC + IAM
- [x] **PROV-02**: Operator can run `km destroy <sandbox-id>` to cleanly tear down all sandbox resources
- [x] **PROV-03**: Operator can run `km list` to see all running sandboxes with status
- [x] **PROV-04**: Operator can run `km status <sandbox-id>` to see detailed sandbox state
- [x] **PROV-05**: Sandbox auto-destroys after TTL expires
- [x] **PROV-06**: Sandbox auto-destroys after idle timeout with no activity
- [x] **PROV-07**: Sandbox teardown policy is configurable (destroy/stop/retain)
- [x] **PROV-08**: Every sandbox resource is tagged with `km:sandbox-id` for tracking and cost attribution
- [x] **PROV-09**: Operator can specify substrate (`ec2` or `ecs`) in the profile's `runtime.substrate` field and `km create` provisions the corresponding infrastructure
- [x] **PROV-10**: ECS substrate provisions an AWS Fargate task with sidecar containers for enforcement (DNS proxy, HTTP proxy, audit log) defined in the task definition
- [x] **PROV-11**: EC2 sandboxes use spot instances by default; on-demand fallback is configurable per profile
- [x] **PROV-12**: ECS sandboxes use Fargate Spot capacity provider by default; on-demand fallback is configurable per profile
- [x] **PROV-13**: Sandbox handles spot interruption gracefully — uploads artifacts to S3 before termination when possible

### Network & Security

- [x] **NETW-01**: Security Groups enforce egress restrictions as the primary enforcement layer
- [x] **NETW-02**: DNS proxy sidecar filters outbound DNS by allowlisted suffixes (works on both EC2 and ECS substrates)
- [x] **NETW-03**: HTTP proxy sidecar filters outbound HTTP/S by allowlisted hosts and methods (works on both EC2 and ECS substrates)
- [x] **NETW-04**: IAM role is session-scoped with configurable duration and region lock
- [x] **NETW-05**: IMDSv2 is enforced (http-tokens=required) on all sandbox EC2 instances
- [x] **NETW-06**: Secrets are injected via SSM Parameter Store with allowlist of permitted secret refs
- [x] **NETW-07**: SOPS encrypts secrets at rest with KMS keys provisioned as part of Klanker Maker infrastructure
- [x] **NETW-08**: GitHub source access controls allowlist repos, refs, and permissions (clone/fetch/push)

### Observability & Artifacts

- [x] **OBSV-01**: Audit log sidecar captures command execution logs (works on both EC2 and ECS substrates)
- [x] **OBSV-02**: Audit log sidecar captures network traffic logs (works on both EC2 and ECS substrates)
- [x] **OBSV-03**: Log destination is configurable (CloudWatch/S3/stdout)
- [x] **OBSV-04**: Filesystem policy enforces writable and read-only paths
- [x] **OBSV-05**: Artifacts upload to S3 on sandbox exit with configurable size limits
- [x] **OBSV-06**: S3 artifact storage supports multi-region replication
- [x] **OBSV-07**: Secret patterns are redacted from audit logs before storage
- [x] **OBSV-08**: Tracing sidecar collects OpenTelemetry traces and spans from sandbox workloads and exports to a configurable OTel collector endpoint
- [x] **OBSV-09**: Each sandbox session is logged as an MLflow run with sandbox metadata (profile, sandbox-id, duration, exit status) as run parameters
- [x] **OBSV-10**: OTel trace context is propagated through proxy sidecars so outbound HTTP requests carry trace headers

### Email & Communication

- [x] **MAIL-01**: SES is configured globally with Route53 domain verification
- [x] **MAIL-02**: Each sandbox agent gets its own email address (agent-id@domain)
- [x] **MAIL-03**: Agents inside sandboxes can send email via SES
- [x] **MAIL-04**: Operator receives email notifications for sandbox lifecycle events (expiry, errors, limits)
- [x] **MAIL-05**: Cross-account agent orchestration is possible via email

### Infrastructure Foundation

- [x] **INFR-01**: AWS multi-account setup: management account, terraform account, application account (defcon.run.34 pattern)
- [x] **INFR-02**: AWS SSO configured for operator access across accounts
- [x] **INFR-03**: Route53 hosted zone configured in management account, delegated to application account
- [x] **INFR-04**: KMS keys provisioned for SOPS encryption
- [x] **INFR-05**: S3 buckets for artifacts with lifecycle policies and cross-region replication
- [x] **INFR-06**: Terragrunt per-sandbox directory isolation (no workspace sharing)
- [x] **INFR-07**: Domain registered in management account and connected to application account
- [x] **INFR-08**: All infrastructure modules and application code from defcon.run.34 (Terraform modules: network, ec2spot, ecs-cluster, ecs-task, ecs-service, secrets; Terragrunt patterns: site.hcl, service.hcl; Go application: apps/local/configui/) are copied into the Klanker Maker repo, renamed, and adapted — no runtime or build-time dependency on defcon.run.34 exists

### ConfigUI

- [x] **CFUI-01**: Web-based profile editor for creating/editing SandboxProfile YAML
- [x] **CFUI-02**: Live sandbox status dashboard showing running sandboxes
- [x] **CFUI-03**: AWS resource discovery showing what each sandbox provisioned
- [x] **CFUI-04**: SOPS secrets management UI for encrypt/decrypt operations

### Platform Configuration

- [x] **CONF-01**: All platform-specific values (domain name, AWS account IDs, SSO start URL, region preferences) are defined in a single configuration file (e.g. `km-config.yaml` or `.klankermaker.yaml`) — operators checking out the repo set their own values before first use, AWS SSO-style configure flow
- [x] **CONF-02**: Domain name is configurable — SES email addresses (`{sandbox-id}@sandboxes.{domain}`), JSON Schema `$id` URL, `apiVersion` in profiles, and ConfigUI branding all derive from the configured domain, not hardcoded `klankermaker.ai`
- [x] **CONF-03**: AWS account numbers (management, terraform, application) and SSO start URL are configurable — referenced by Terragrunt hierarchy, IAM policies, and `km` CLI commands without hardcoding
- [x] **CONF-04**: `km init` or `km configure` command walks the operator through initial setup: domain, accounts, region, SSO — writes the config file and validates AWS access
- [x] **CONF-05**: `km shell <sandbox-id>` opens an interactive shell into a running sandbox — abstracts the substrate (EC2: SSM Session Manager, ECS: ECS Exec, future k8s: kubectl exec). Operator never needs to know the underlying AWS CLI incantation

### Budget Enforcement

- [x] **BUDG-01**: Per-sandbox budget with separate compute and AI spend pools defined in profile YAML (`spec.budget.compute.maxSpendUSD`, `spec.budget.ai.maxSpendUSD`)
- [x] **BUDG-02**: DynamoDB global table (single-table design, extending defcon.run.34 auth pattern) stores budget limits and running spend per sandbox, replicated to all regions where agents run for low-latency local reads
- [x] **BUDG-03**: Compute spend tracked as instance type spot rate × elapsed minutes (per-minute billing); rate sourced from AWS Price List API at sandbox creation
- [x] **BUDG-04**: AI/token spend tracked per Bedrock Anthropic model (Haiku, Sonnet, Opus); http-proxy sidecar intercepts `InvokeModel` responses, extracts `usage.input_tokens`/`usage.output_tokens`, multiplies by model rate, increments DynamoDB budget record
- [x] **BUDG-05**: Model pricing sourced from AWS Price List API (cached, refreshed daily) — supports all Anthropic models available on Bedrock
- [x] **BUDG-06**: At 80% budget threshold (configurable via `spec.budget.warningThreshold`), operator receives warning email via SES using existing `SendLifecycleNotification` pattern
- [x] **BUDG-07**: Dual-layer enforcement — at 100% AI budget, http-proxy returns 403 for Bedrock calls (immediate, real-time); the same EventBridge-triggered Lambda that checks compute spend also reads DynamoDB AI spend records and revokes the instance profile's Bedrock IAM permissions as a backstop (catches SDK/CLI calls that bypass the proxy); at 100% compute budget, Lambda suspends the sandbox: EC2 instances are stopped (`StopInstances` — preserves EBS, no compute charges, resumable on top-up); ECS Fargate tasks trigger artifact upload then stop (tasks are ephemeral — top-up re-provisions from stored profile in S3)
- [x] **BUDG-08**: Operator can top up a sandbox budget via `km budget add <sandbox-id> --compute <amount> --ai <amount>` which updates DynamoDB limits and resumes enforcement: for AI, restores Bedrock IAM and proxy unblocks; for compute, EC2 instances are started (`StartInstances` — resumes from stopped state), ECS Fargate tasks are re-provisioned from the stored profile in S3
- [x] **BUDG-09**: `km status <sandbox-id>` shows current spend vs budget for both compute and AI pools, including per-model AI breakdown
- [x] **BUDG-10**: AI/token spend tracked for Anthropic API (Claude Code) calls via `api.anthropic.com`; http-proxy sidecar intercepts `POST /v1/messages` responses (both non-streaming and SSE streaming), extracts `usage.input_tokens`/`usage.output_tokens`, prices against Anthropic's published model rates, and increments DynamoDB budget record using the same `IncrementAISpend` path as Bedrock metering

### Operator Experience

- [x] **OPER-01**: All terragrunt-calling CLI commands (`km create`, `km destroy`, `km init`, `km uninit`) suppress raw terragrunt/terraform output by default — show step-level summaries instead; `--verbose` flag restores full output streaming; errors and warnings always shown regardless of mode

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Advanced Profiles

- **PROF-01**: Profile composition (policy bundles) beyond simple extends
- **PROF-02**: Profile versioning with migration support

### Cost & Operations

- **COST-02**: Warm pool / pre-provisioned sandboxes for faster startup
- **COST-03**: `km gc` for orphan detection and cleanup

### Platform Expansion

- **PLAT-01**: Kubernetes substrate option (k8s/EKS) — natural v2 extension after EC2 and ECS are working
- **PLAT-02**: Docker/local substrate for development
- **PLAT-03**: Sandbox REST API server (persistent control plane)
- **PLAT-04**: Multi-cloud support (GCP, Azure)

### Sandbox Identity Customization

- **PREFIX-01**: Profile schema supports optional `metadata.prefix` field with validation (`^[a-z][a-z0-9]{0,11}$`)
- **PREFIX-02**: `GenerateSandboxID()` accepts a prefix parameter — generates `{prefix}-{8 hex}` IDs
- **PREFIX-03**: All sandbox ID validation/matching patterns accept any valid prefix, not just `sb-`
- **PREFIX-04**: Compiler, CLI, and Lambda code use sandbox ID as-is — no component hardcodes the `sb-` prefix
- **PREFIX-05**: Backwards compatible — profiles without `metadata.prefix` default to `sb`
- **ALIAS-01**: `km create --alias <name>` stores alias in S3 metadata.json; all commands resolve alias to sandbox ID via S3 scan
- **ALIAS-02**: Profile-level `metadata.alias` template auto-generates `{alias}-1`, `{alias}-2` etc. by scanning active sandboxes
- **ALIAS-03**: `--alias` flag overrides profile-level template; alias freed on destroy for reuse
- **ALIAS-04**: `km list` displays alias column; `ResolveSandboxRef` resolves aliases (future: DynamoDB index for O(1) lookup)

### Advanced Policy

- **PLCY-01**: OPA / policy engine integration for enterprise compliance
- **PLCY-02**: Multi-tenancy with RBAC and session ownership

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Docker/local substrate | Development convenience only; adds complexity without isolation guarantees; v2 candidate |
| Kubernetes substrate (k8s/EKS) | EC2 and ECS cover v1 use cases; k8s is a near-future v2 extension (PLAT-01) |
| Multi-cloud implementation | Schema is cloud-neutral but v1 implements AWS only |
| Full OPA policy engine | YAML allowlists cover 90% of use cases; OPA adds operator complexity |
| Real-time collaboration / multi-user editing | Single-operator model for v1; multi-tenancy is a major scope increase |
| Interactive terminal / SSH into sandboxes | Creates "pet server" behavior; conflicts with ephemeral destroy-on-TTL model |
| Deny-list network policy | Allowlists are more auditable and secure; deny-lists are incomplete by definition |
| Mobile app | Web ConfigUI is sufficient |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| SCHM-01 | Phase 1 | Complete |
| SCHM-02 | Phase 1 | Complete |
| SCHM-03 | Phase 1 | Complete |
| SCHM-04 | Phase 7 | Complete |
| SCHM-05 | Phase 7 | Complete |
| INFR-01 | Phase 1 | Complete |
| INFR-02 | Phase 1 | Complete |
| INFR-03 | Phase 1 | Complete |
| INFR-04 | Phase 1 | Complete |
| INFR-05 | Phase 1 | Complete |
| INFR-06 | Phase 1 | Complete |
| INFR-07 | Phase 1 | Complete |
| INFR-08 | Phase 1 | Complete |
| PROV-01 | Phase 2 | Complete |
| PROV-02 | Phase 2 | Complete |
| PROV-08 | Phase 2 | Complete |
| PROV-09 | Phase 2 | Complete |
| PROV-10 | Phase 2 | Complete |
| PROV-11 | Phase 2 | Complete |
| PROV-12 | Phase 2 | Complete |
| NETW-01 | Phase 2 | Complete |
| NETW-04 | Phase 2 | Complete |
| NETW-05 | Phase 2 | Complete |
| NETW-06 | Phase 2 | Complete |
| NETW-07 | Phase 2 | Complete |
| NETW-08 | Phase 2 | Complete |
| PROV-03 | Phase 11 | Complete |
| PROV-04 | Phase 11 | Complete |
| PROV-05 | Phase 11 | Complete |
| PROV-06 | Phase 11 | Complete |
| PROV-07 | Phase 3 | Complete |
| NETW-02 | Phase 3 | Complete |
| NETW-03 | Phase 3 | Complete |
| OBSV-01 | Phase 3 | Complete |
| OBSV-02 | Phase 3 | Complete |
| OBSV-03 | Phase 3 | Complete |
| OBSV-04 | Phase 4 | Complete |
| OBSV-05 | Phase 4 | Complete |
| OBSV-06 | Phase 12 | Complete |
| OBSV-07 | Phase 4 | Complete |
| OBSV-08 | Phase 3 | Complete |
| OBSV-09 | Phase 3 | Complete |
| OBSV-10 | Phase 3 | Complete |
| PROV-13 | Phase 4 | Complete |
| MAIL-01 | Phase 4 | Complete |
| MAIL-02 | Phase 4 | Complete |
| MAIL-03 | Phase 4 | Complete |
| MAIL-04 | Phase 4 | Complete |
| MAIL-05 | Phase 4 | Complete |
| CFUI-01 | Phase 5 | Complete |
| CFUI-02 | Phase 5 | Complete |
| CFUI-03 | Phase 5 | Complete |
| CFUI-04 | Phase 5 | Complete |
| CONF-01 | Phase 6 | Complete |
| CONF-02 | Phase 6 | Complete |
| CONF-03 | Phase 6 | Complete |
| CONF-04 | Phase 6 | Complete |
| CONF-05 | Phase 6 | Complete |
| BUDG-01 | Phase 6 | Complete |
| BUDG-02 | Phase 6 | Complete |
| BUDG-03 | Phase 6 | Complete |
| BUDG-04 | Phase 6 | Complete |
| BUDG-05 | Phase 6 | Complete |
| BUDG-06 | Phase 6 | Complete |
| BUDG-07 | Phase 19 | Complete |
| BUDG-08 | Phase 19 | Complete |
| BUDG-10 | Phase 20 | Complete |
| OPER-01 | Phase 20 | Complete |
| BUDG-09 | Phase 6 | Complete |
| PROV-06 | Phase 7 | Complete |
| OBSV-07 | Phase 7 | Complete |
| OBSV-09 | Phase 7 | Complete |
| CONF-03 | Phase 7 | Complete |

**Coverage:**
- v1 requirements: 66 total
- Mapped to phases: 56
- Unmapped: 0

---
*Requirements defined: 2026-03-21*
*Last updated: 2026-03-21 — PROV-09, PROV-10 added; ECS moved from Out of Scope to v1; k8s added to v2; Docker/local remains out of scope*
*Last updated: 2026-03-21 — INFR-08 added: no cross-repo dependency on defcon.run.34; all modules and app code must be copied and adapted into Klanker Maker repo*
*Last updated: 2026-03-21 — PROV-11, PROV-12, PROV-13 added: spot instances by default for EC2 and ECS, graceful interruption handling with artifact upload*
*Last updated: 2026-03-21 — OBSV-08, OBSV-09, OBSV-10 added: OTel tracing sidecar, MLflow experiment tracking per sandbox session, trace context propagation through proxy sidecars*
*Last updated: 2026-03-22 — COST-01 promoted from v2, expanded into BUDG-01 through BUDG-09: per-sandbox budget enforcement with DynamoDB global table, http-proxy Bedrock metering, threshold warnings, hard enforcement, operator top-up*
