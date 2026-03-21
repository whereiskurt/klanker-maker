# Requirements: Fabric

**Defined:** 2026-03-21
**Core Value:** A sandbox is a declarative policy object that compiles into a controlled, auditable execution environment

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.

### Schema & Validation

- [ ] **SCHM-01**: Operator can define a SandboxProfile in YAML with apiVersion, kind, metadata, spec sections
- [ ] **SCHM-02**: Schema supports lifecycle, runtime, execution, sourceAccess, network, identity, sidecars, observability, policy, and agent sections
- [ ] **SCHM-03**: Operator can run `fabric validate <profile.yaml>` and get clear error messages for invalid profiles
- [ ] **SCHM-04**: Profile can extend a base profile via `extends` field, inheriting and overriding specific sections
- [ ] **SCHM-05**: Four built-in profiles ship with Fabric: open-dev, restricted-dev, hardened, sealed

### Provisioning & Lifecycle

- [ ] **PROV-01**: Operator can run `fabric create <profile>` to compile profile into Terragrunt inputs and provision EC2 + VPC + IAM
- [ ] **PROV-02**: Operator can run `fabric destroy <sandbox-id>` to cleanly tear down all sandbox resources
- [ ] **PROV-03**: Operator can run `fabric list` to see all running sandboxes with status
- [ ] **PROV-04**: Operator can run `fabric status <sandbox-id>` to see detailed sandbox state
- [ ] **PROV-05**: Sandbox auto-destroys after TTL expires
- [ ] **PROV-06**: Sandbox auto-destroys after idle timeout with no activity
- [ ] **PROV-07**: Sandbox teardown policy is configurable (destroy/stop/retain)
- [ ] **PROV-08**: Every sandbox resource is tagged with `fabric:sandbox-id` for tracking and cost attribution

### Network & Security

- [ ] **NETW-01**: Security Groups enforce egress restrictions as the primary enforcement layer
- [ ] **NETW-02**: DNS proxy sidecar filters outbound DNS by allowlisted suffixes
- [ ] **NETW-03**: HTTP proxy sidecar filters outbound HTTP/S by allowlisted hosts and methods
- [ ] **NETW-04**: IAM role is session-scoped with configurable duration and region lock
- [ ] **NETW-05**: IMDSv2 is enforced (http-tokens=required) on all sandbox EC2 instances
- [ ] **NETW-06**: Secrets are injected via SSM Parameter Store with allowlist of permitted secret refs
- [ ] **NETW-07**: SOPS encrypts secrets at rest with KMS keys provisioned as part of Fabric infrastructure
- [ ] **NETW-08**: GitHub source access controls allowlist repos, refs, and permissions (clone/fetch/push)

### Observability & Artifacts

- [ ] **OBSV-01**: Audit log sidecar captures command execution logs
- [ ] **OBSV-02**: Audit log sidecar captures network traffic logs
- [ ] **OBSV-03**: Log destination is configurable (CloudWatch/S3/stdout)
- [ ] **OBSV-04**: Filesystem policy enforces writable and read-only paths
- [ ] **OBSV-05**: Artifacts upload to S3 on sandbox exit with configurable size limits
- [ ] **OBSV-06**: S3 artifact storage supports multi-region replication
- [ ] **OBSV-07**: Secret patterns are redacted from audit logs before storage

### Email & Communication

- [ ] **MAIL-01**: SES is configured globally with Route53 domain verification
- [ ] **MAIL-02**: Each sandbox agent gets its own email address (agent-id@domain)
- [ ] **MAIL-03**: Agents inside sandboxes can send email via SES
- [ ] **MAIL-04**: Operator receives email notifications for sandbox lifecycle events (expiry, errors, limits)
- [ ] **MAIL-05**: Cross-account agent orchestration is possible via email

### Infrastructure Foundation

- [ ] **INFR-01**: AWS multi-account setup: management account, terraform account, application account (defcon.run.34 pattern)
- [ ] **INFR-02**: AWS SSO configured for operator access across accounts
- [ ] **INFR-03**: Route53 hosted zone configured in management account, delegated to application account
- [ ] **INFR-04**: KMS keys provisioned for SOPS encryption
- [ ] **INFR-05**: S3 buckets for artifacts with lifecycle policies and cross-region replication
- [ ] **INFR-06**: Terragrunt per-sandbox directory isolation (no workspace sharing)
- [ ] **INFR-07**: Domain registered in management account and connected to application account

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
- **COST-03**: `fabric gc` for orphan detection and cleanup

### Platform Expansion

- **PLAT-01**: ECS/Fargate substrate option
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
| Container-based isolation (Docker/ECS) | EC2 VM-level isolation is stronger and simpler for v1; containers add escape risk |
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
| SCHM-01 | — | Pending |
| SCHM-02 | — | Pending |
| SCHM-03 | — | Pending |
| SCHM-04 | — | Pending |
| SCHM-05 | — | Pending |
| PROV-01 | — | Pending |
| PROV-02 | — | Pending |
| PROV-03 | — | Pending |
| PROV-04 | — | Pending |
| PROV-05 | — | Pending |
| PROV-06 | — | Pending |
| PROV-07 | — | Pending |
| PROV-08 | — | Pending |
| NETW-01 | — | Pending |
| NETW-02 | — | Pending |
| NETW-03 | — | Pending |
| NETW-04 | — | Pending |
| NETW-05 | — | Pending |
| NETW-06 | — | Pending |
| NETW-07 | — | Pending |
| NETW-08 | — | Pending |
| OBSV-01 | — | Pending |
| OBSV-02 | — | Pending |
| OBSV-03 | — | Pending |
| OBSV-04 | — | Pending |
| OBSV-05 | — | Pending |
| OBSV-06 | — | Pending |
| OBSV-07 | — | Pending |
| MAIL-01 | — | Pending |
| MAIL-02 | — | Pending |
| MAIL-03 | — | Pending |
| MAIL-04 | — | Pending |
| MAIL-05 | — | Pending |
| INFR-01 | — | Pending |
| INFR-02 | — | Pending |
| INFR-03 | — | Pending |
| INFR-04 | — | Pending |
| INFR-05 | — | Pending |
| INFR-06 | — | Pending |
| INFR-07 | — | Pending |
| CFUI-01 | — | Pending |
| CFUI-02 | — | Pending |
| CFUI-03 | — | Pending |
| CFUI-04 | — | Pending |

**Coverage:**
- v1 requirements: 43 total
- Mapped to phases: 0
- Unmapped: 43 ⚠️

---
*Requirements defined: 2026-03-21*
*Last updated: 2026-03-21 after initial definition*
