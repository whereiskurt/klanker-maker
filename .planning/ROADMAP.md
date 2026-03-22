# Roadmap: Klanker Maker

## Overview

Klanker Maker is built along a strict architectural dependency chain: the SandboxProfile YAML schema is the root dependency from which everything derives. The compiler reads the schema and produces Terragrunt artifacts; Terraform modules consume those artifacts to provision infrastructure on the chosen substrate (EC2 or ECS); sidecar processes are injected into provisioned instances/tasks; and the ConfigUI wraps the working system as a management layer. Phases follow this chain — no phase can begin until its dependency is complete.

Both EC2 and ECS (Fargate) are first-class v1 substrates. The profile's `runtime.substrate` field selects the substrate; the compiler produces different Terragrunt artifacts for each. Kubernetes is a near-future extension tracked as v2.

**Cross-repo dependency policy:** All code and modules from defcon.run.34 that Klanker Maker needs (Terraform modules, ConfigUI Go code, site.hcl/service.hcl patterns, etc.) must be COPIED into this repo and renamed/adapted. No runtime or build-time dependency on defcon.run.34 is permitted. This constraint is enforced starting in Phase 1 and applies to all subsequent phases.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [ ] **Phase 1: Schema, Compiler & AWS Foundation** - SandboxProfile YAML schema (including `runtime.substrate` field), profile compiler, `km validate`, AWS account/infrastructure prerequisites, and copy of all foundation Terraform/Terragrunt modules from defcon.run.34 into the Klanker Maker repo
- [ ] **Phase 2: Core Provisioning & Security Baseline** - `km create/destroy` for EC2 and ECS substrates using Terraform modules copied and adapted within this repo, substrate-aware Terragrunt artifact generation, SG-first security model, IMDSv2, secrets, GitHub source access, spot instances by default for both substrates
- [ ] **Phase 3: Sidecar Enforcement & Lifecycle Management** - DNS proxy, HTTP proxy, audit log, and tracing sidecars on both substrates; OTel trace collection and MLflow experiment tracking per sandbox session; TTL auto-destroy, `km list/status`, observability
- [ ] **Phase 4: Lifecycle Hardening, Artifacts & Email** - Profile inheritance, filesystem policy, artifact upload, spot interruption handling, secret redaction, email/SES agent communication
- [ ] **Phase 5: ConfigUI** - Web dashboard for profile editing, live sandbox status, and AWS resource discovery — ConfigUI Go code copied and adapted from defcon.run.34 with no external dependency
- [ ] **Phase 6: Budget Enforcement** - Per-sandbox compute and AI spend tracking via DynamoDB global table, http-proxy Bedrock interception for real-time token metering, threshold warnings via SES, hard enforcement via IAM revocation and proxy 403s, operator top-up via CLI

## Phase Details

### Phase 1: Schema, Compiler & AWS Foundation
**Goal**: Operators can define, validate, and compile SandboxProfile YAML into provisioning artifacts, with the underlying AWS account structure ready to receive infrastructure — and all reusable modules from defcon.run.34 are copied into this repo so no cross-repo dependency exists
**Depends on**: Nothing (first phase)
**Requirements**: SCHM-01, SCHM-02, SCHM-03, SCHM-04, SCHM-05, INFR-01, INFR-02, INFR-03, INFR-04, INFR-05, INFR-06, INFR-07, INFR-08
**Success Criteria** (what must be TRUE):
  1. Operator can write a SandboxProfile YAML with all supported sections (lifecycle, runtime, execution, sourceAccess, network, identity, sidecars, observability, policy, agent) including `runtime.substrate: ec2 | ecs` and `km validate` reports it as valid
  2. Operator can run `km validate <profile.yaml>` against an invalid profile and receive clear, actionable error messages identifying the specific field and violation
  3. Operator can write a profile that extends a base profile via `extends`, and inheritance overrides are applied correctly (child values override, not extend, parent allowlists)
  4. All four built-in profiles (open-dev, restricted-dev, hardened, sealed) are present and pass `km validate` out of the box
  5. AWS multi-account structure (management, terraform, application) is provisioned with SSO access, Route53 delegation, KMS keys, S3 artifact buckets, and Terragrunt per-sandbox directory isolation in place — all Terraform modules (network, ec2spot, ecs-cluster, ecs-task, ecs-service, secrets, and Terragrunt site.hcl/service.hcl patterns) are present inside the Klanker Maker repo under their own module paths, renamed and adapted, with no reference to defcon.run.34
**Plans:** 3/4 plans executed

Plans:
- [ ] 01-01-PLAN.md — Go project scaffold, SandboxProfile types, JSON Schema, schema+semantic validation (TDD)
- [ ] 01-02-PLAN.md — Copy and adapt Terraform modules + Terragrunt hierarchy from defcon.run.34
- [ ] 01-03-PLAN.md — Profile inheritance resolver + four built-in profiles (TDD)
- [ ] 01-04-PLAN.md — km validate CLI command + AWS foundation infrastructure verification

### Phase 2: Core Provisioning & Security Baseline
**Goal**: Operators can create and destroy sandboxes via `km create/destroy` on either EC2 or ECS substrate using spot capacity by default, with VPC Security Group egress as the primary enforcement boundary, IMDSv2 enforced on EC2, and every resource tagged for tracking — all using Terraform modules that live inside the Klanker Maker repo
**Depends on**: Phase 1
**Requirements**: PROV-01, PROV-02, PROV-08, PROV-09, PROV-10, PROV-11, PROV-12, NETW-01, NETW-04, NETW-05, NETW-06, NETW-07, NETW-08
**Success Criteria** (what must be TRUE):
  1. Operator runs `km create <profile>` with `runtime.substrate: ec2` and an EC2 spot instance + VPC + IAM role is provisioned using the network and ec2spot Terraform modules inside the Klanker Maker repo; outbound traffic is blocked at the Security Group layer except through designated proxy paths
  2. Operator runs `km create <profile>` with `runtime.substrate: ecs` and a Fargate Spot task + VPC + IAM task role is provisioned using the ecs-cluster, ecs-task, and ecs-service Terraform modules inside the Klanker Maker repo using the FARGATE_SPOT capacity provider; outbound traffic is blocked at the Security Group layer — no module is sourced from defcon.run.34
  3. A profile with `runtime.spot: false` (or equivalent on-demand override) provisions an on-demand EC2 instance or standard Fargate task instead of spot — the fallback path is exercised and confirmed working
  4. Operator runs `km destroy <sandbox-id>` for either substrate and all sandbox resources are cleanly removed with no orphaned resources remaining; for EC2 spot, the spot instance request itself is cancelled in addition to instance termination (not relying solely on `terraform destroy` which does not terminate spot instances)
  5. Every AWS resource created by `km create` carries a `km:sandbox-id` tag matching the sandbox ID, visible in the AWS console
  6. EC2 instances are created with IMDSv2 enforced (`http-tokens=required`) — direct calls to the metadata endpoint without a session token fail from within the sandbox
  7. Secrets referenced in the profile's allowlist are injected into the sandbox via SSM Parameter Store; secrets not on the allowlist are inaccessible; SOPS encrypted secrets decrypt correctly via KMS
**Plans:** 2/4 plans executed

Plans:
- [ ] 02-01-PLAN.md — Profile compiler package (EC2 + ECS service.hcl, user-data, SG, IAM, secrets)
- [ ] 02-02-PLAN.md — Terragrunt runner + AWS SDK helpers (tag discovery, spot termination)
- [ ] 02-03-PLAN.md — km create + km destroy CLI commands
- [ ] 02-04-PLAN.md — End-to-end AWS verification checkpoint

### Phase 3: Sidecar Enforcement & Lifecycle Management
**Goal**: Running sandboxes on either substrate enforce network policy via DNS and HTTP proxy sidecars, produce auditable logs and OpenTelemetry traces, log MLflow experiment runs per session, and auto-terminate based on TTL and idle policy — operators can observe all running sandboxes
**Depends on**: Phase 2
**Requirements**: PROV-03, PROV-04, PROV-05, PROV-06, PROV-07, NETW-02, NETW-03, OBSV-01, OBSV-02, OBSV-03, OBSV-08, OBSV-09, OBSV-10
**Success Criteria** (what must be TRUE):
  1. A sandbox running under a restricted profile (EC2 or ECS) cannot resolve DNS names outside its allowlisted suffixes — attempts to reach non-allowlisted domains fail at the DNS layer
  2. A sandbox (EC2 or ECS) cannot make HTTP/HTTPS requests to hosts outside the profile's allowlisted hosts — blocked requests return a proxy error, not a silent failure; on ECS the proxy runs as a sidecar container in the same task definition
  3. Command execution and network traffic inside a running sandbox are captured in the audit log and delivered to the configured destination (CloudWatch, S3, or stdout); on ECS the audit sidecar is a container in the task definition
  4. A sandbox with a TTL set automatically destroys itself after expiry; a sandbox with idle timeout set destroys itself after the configured period of no activity; teardown policy (destroy/stop/retain) is honored
  5. Operator runs `km list` and sees all running sandboxes with status and substrate type; `km status <sandbox-id>` shows detailed state for a specific sandbox
  6. A tracing sidecar collects OTel traces/spans from the sandbox workload and exports them to the configured collector endpoint; trace context is propagated through the HTTP proxy sidecar on outbound requests
  7. Each sandbox session is recorded as an MLflow run with sandbox metadata (profile name, sandbox-id, duration, exit status) as run parameters; operators can query MLflow to see agent execution history across sandbox sessions
**Plans:** 5/6 plans executed

Plans:
- [ ] 03-00-PLAN.md — Wave 0: test stub scaffolding for all Phase 3 packages (Nyquist compliance)
- [ ] 03-01-PLAN.md — DNS proxy + HTTP proxy sidecar binaries (NETW-02, NETW-03, OBSV-10)
- [ ] 03-02-PLAN.md — Audit log sidecar + CloudWatch Logs helpers (OBSV-01, OBSV-02, OBSV-03)
- [ ] 03-03-PLAN.md — OTel tracing sidecar config + MLflow S3 run logging (OBSV-08, OBSV-09)
- [ ] 03-04-PLAN.md — Compiler integration: EC2 user-data + ECS service.hcl + EventBridge TTL + lifecycle package + create/destroy wiring (PROV-05, PROV-06, PROV-07)
- [ ] 03-05-PLAN.md — km list, km status, km logs CLI commands (PROV-03, PROV-04)

### Phase 4: Lifecycle Hardening, Artifacts & Email
**Goal**: Sandboxes enforce filesystem access policy and upload artifacts on exit (including on spot interruption); secret patterns are scrubbed from audit logs; agent sandboxes can send and receive email; the platform is ready for real agent workloads
**Depends on**: Phase 3
**Requirements**: OBSV-04, OBSV-05, OBSV-06, OBSV-07, PROV-13, MAIL-01, MAIL-02, MAIL-03, MAIL-04, MAIL-05
**Success Criteria** (what must be TRUE):
  1. A sandbox with filesystem policy configured cannot write to read-only paths — attempts fail with a permission error at the OS level, not at the application level
  2. On sandbox exit, files in the configured artifact paths are uploaded to S3; uploads exceeding the configured size limit are rejected; S3 bucket replicates to the configured secondary region
  3. When a spot interruption notice is received (2-minute warning via EC2 instance metadata or ECS task state change event), the sandbox initiates an artifact upload to S3 before the instance or task is reclaimed — artifacts present in the bucket after interruption confirm the handler fired
  4. Secret values (SSM parameter values, tokens) present in the sandbox environment are redacted from audit logs before storage — the raw secret value does not appear in CloudWatch or S3 logs
  5. Each sandbox agent has a unique email address provisioned via SES; the agent can send email from within the sandbox; the operator receives lifecycle event notifications (expiry, errors, limits) via email
  6. Cross-account agent orchestration via email is demonstrable: an agent in one sandbox can trigger an action in another sandbox by sending a correctly structured email
**Plans:** 5 plans

Plans:
- [ ] 04-01-PLAN.md — ArtifactsSpec schema + RedactingDestination + S3 artifact uploader (OBSV-05, OBSV-07)
- [ ] 04-02-PLAN.md — SES Terraform module + Go SES helpers (MAIL-01, MAIL-02, MAIL-03, MAIL-04, MAIL-05)
- [ ] 04-03-PLAN.md — Filesystem enforcement + spot interruption in compiler templates + teardown artifact callback (OBSV-04, PROV-13)
- [ ] 04-04-PLAN.md — Create/destroy wiring: SES email + artifacts + lifecycle notifications + S3 replication (OBSV-05, OBSV-06, MAIL-02, MAIL-03, MAIL-04, MAIL-05)
- [ ] 04-05-PLAN.md — Gap closure: TTL handler Lambda + idle/error lifecycle notification wiring (MAIL-04, OBSV-05)

### Phase 5: ConfigUI
**Goal**: Operators can manage profiles and monitor live sandboxes through a web dashboard without using the CLI — the ConfigUI Go application is copied and adapted from defcon.run.34 into `apps/local/configui/` inside the Klanker Maker repo, with all defcon.run.34-specific references renamed and no external dependency on that repo
**Depends on**: Phase 4
**Requirements**: CFUI-01, CFUI-02, CFUI-03, CFUI-04
**Success Criteria** (what must be TRUE):
  1. Operator opens the ConfigUI in a browser, sees a profile editor with inline validation — editing a profile field and saving it runs `km validate` and shows errors without leaving the page; the ConfigUI binary is built entirely from source within the Klanker Maker repo
  2. The live sandbox status dashboard updates in real time (polling) showing all running sandboxes, their status, substrate type, and time remaining on TTL without a page refresh
  3. Operator can click into a sandbox in the dashboard and see the AWS resources it provisioned (EC2 instance ID or ECS task ARN, VPC, security groups, IAM role) discovered from AWS
  4. Operator can manage SOPS secrets from the ConfigUI — encrypting a new secret and decrypting an existing one without using the CLI
**Plans**: TBD

### Phase 6: Budget Enforcement
**Goal**: Operators can set per-sandbox dollar budgets for compute and AI (Bedrock Anthropic models), with real-time spend tracking, threshold warnings, and hard enforcement — sandboxes that exceed their budget are blocked from further API calls or compute, and operators can top up budgets without destroying the sandbox
**Depends on**: Phase 5
**Requirements**: BUDG-01, BUDG-02, BUDG-03, BUDG-04, BUDG-05, BUDG-06, BUDG-07, BUDG-08, BUDG-09
**Success Criteria** (what must be TRUE):
  1. Operator creates a sandbox with `spec.budget.compute.maxSpendUSD: 2.00` and `spec.budget.ai.maxSpendUSD: 5.00`; DynamoDB global table stores the budget limits alongside the sandbox record using the defcon.run.34 single-table pattern
  2. A running sandbox's compute spend is tracked as spot rate × elapsed minutes; `km status` shows current compute spend vs budget; when compute budget is exhausted, a Lambda revokes the sandbox IAM role permissions and the operator receives an email notification
  3. An agent inside a sandbox makes Bedrock InvokeModel calls (Haiku, Sonnet, or Opus); the http-proxy sidecar intercepts each response, extracts token usage, prices it against the model's rate from AWS Price List API, and increments the DynamoDB spend record; `km status` shows per-model AI spend breakdown
  4. When AI budget reaches 100%, the http-proxy returns HTTP 403 for subsequent Bedrock calls — the agent receives an immediate error, not a timeout; the operator receives an email notification
  5. At 80% of either budget pool, the operator receives a warning email; the threshold is configurable via `spec.budget.warningThreshold`
  6. Operator runs `km budget add <sandbox-id> --ai 3.00` and the sandbox's AI budget increases by $3; if IAM was revoked or proxy was blocking, enforcement is lifted and the sandbox resumes normal operation
  7. DynamoDB budget table is a global table replicated to all regions where agents run — budget reads from within the sandbox hit the local regional replica with sub-millisecond latency
**Plans**: TBD

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5 → 6

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Schema, Compiler & AWS Foundation | 3/4 | In Progress|  |
| 2. Core Provisioning & Security Baseline | 2/4 | In Progress|  |
| 3. Sidecar Enforcement & Lifecycle Management | 5/6 | In Progress|  |
| 4. Lifecycle Hardening, Artifacts & Email | 4/5 | In Progress|  |
| 5. ConfigUI | 0/TBD | Not started | - |
| 6. Budget Enforcement | 0/TBD | Not started | - |
