# Requirements: Klanker Maker

**Defined:** 2026-03-21
**Core Value:** A sandbox is a declarative policy object that compiles into a controlled, auditable execution environment

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.

### Schema & Validation

- [x] **SCHM-01**: Operator can define a SandboxProfile in YAML with apiVersion, kind, metadata, spec sections
- [x] **SCHM-02**: Schema supports lifecycle, runtime, execution, sourceAccess, network, identity, sidecars, observability, policy, and agent sections
- [x] **SCHM-03**: Operator can run `km validate <profile.yaml>` and get clear error messages for invalid profiles
- [ ] **SCHM-04**: Profile can extend a base profile via `extends` field, inheriting and overriding specific sections
- [ ] **SCHM-05**: Four built-in profiles ship with Klanker Maker: open-dev, restricted-dev, hardened, sealed

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
- [ ] **PROV-13**: Sandbox handles spot interruption gracefully — uploads artifacts to S3 before termination when possible

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
- [ ] **OBSV-04**: Filesystem policy enforces writable and read-only paths
- [x] **OBSV-05**: Artifacts upload to S3 on sandbox exit with configurable size limits
- [ ] **OBSV-06**: S3 artifact storage supports multi-region replication
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

- [ ] **CFUI-01**: Web-based profile editor for creating/editing SandboxProfile YAML
- [ ] **CFUI-02**: Live sandbox status dashboard showing running sandboxes
- [ ] **CFUI-03**: AWS resource discovery showing what each sandbox provisioned
- [ ] **CFUI-04**: SOPS secrets management UI for encrypt/decrypt operations

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Advanced Profiles

- **PROF-01**: Profile composition (policy bundles) beyond simple extends
- **PROF-02**: Profile versioning with migration support

### Cost & Operations

- **COST-01**: Cost budgeting per sandbox with spend limits
- **COST-02**: Warm pool / pre-provisioned sandboxes for faster startup
- **COST-03**: `km gc` for orphan detection and cleanup

### Platform Expansion

- **PLAT-01**: Kubernetes substrate option (k8s/EKS) — natural v2 extension after EC2 and ECS are working
- **PLAT-02**: Docker/local substrate for development
- **PLAT-03**: Sandbox REST API server (persistent control plane)
- **PLAT-04**: Multi-cloud support (GCP, Azure)

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
| SCHM-04 | Phase 1 | Pending |
| SCHM-05 | Phase 1 | Pending |
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
| PROV-03 | Phase 3 | Complete |
| PROV-04 | Phase 3 | Complete |
| PROV-05 | Phase 3 | Complete |
| PROV-06 | Phase 3 | Complete |
| PROV-07 | Phase 3 | Complete |
| NETW-02 | Phase 3 | Complete |
| NETW-03 | Phase 3 | Complete |
| OBSV-01 | Phase 3 | Complete |
| OBSV-02 | Phase 3 | Complete |
| OBSV-03 | Phase 3 | Complete |
| OBSV-04 | Phase 4 | Pending |
| OBSV-05 | Phase 4 | Complete |
| OBSV-06 | Phase 4 | Pending |
| OBSV-07 | Phase 4 | Complete |
| OBSV-08 | Phase 3 | Complete |
| OBSV-09 | Phase 3 | Complete |
| OBSV-10 | Phase 3 | Complete |
| PROV-13 | Phase 4 | Pending |
| MAIL-01 | Phase 4 | Complete |
| MAIL-02 | Phase 4 | Complete |
| MAIL-03 | Phase 4 | Complete |
| MAIL-04 | Phase 4 | Complete |
| MAIL-05 | Phase 4 | Complete |
| CFUI-01 | Phase 5 | Pending |
| CFUI-02 | Phase 5 | Pending |
| CFUI-03 | Phase 5 | Pending |
| CFUI-04 | Phase 5 | Pending |

**Coverage:**
- v1 requirements: 52 total
- Mapped to phases: 52
- Unmapped: 0

---
*Requirements defined: 2026-03-21*
*Last updated: 2026-03-21 — PROV-09, PROV-10 added; ECS moved from Out of Scope to v1; k8s added to v2; Docker/local remains out of scope*
*Last updated: 2026-03-21 — INFR-08 added: no cross-repo dependency on defcon.run.34; all modules and app code must be copied and adapted into Klanker Maker repo*
*Last updated: 2026-03-21 — PROV-11, PROV-12, PROV-13 added: spot instances by default for EC2 and ECS, graceful interruption handling with artifact upload*
*Last updated: 2026-03-21 — OBSV-08, OBSV-09, OBSV-10 added: OTel tracing sidecar, MLflow experiment tracking per sandbox session, trace context propagation through proxy sidecars*
