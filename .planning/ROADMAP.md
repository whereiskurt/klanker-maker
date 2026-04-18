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
- [x] **Phase 2: Core Provisioning & Security Baseline** - `km create/destroy` for EC2 and ECS substrates using Terraform modules copied and adapted within this repo, substrate-aware Terragrunt artifact generation, SG-first security model, IMDSv2, secrets, GitHub source access, spot instances by default for both substrates (completed 2026-03-22)
- [ ] **Phase 3: Sidecar Enforcement & Lifecycle Management** - DNS proxy, HTTP proxy, audit log, and tracing sidecars on both substrates; OTel trace collection and MLflow experiment tracking per sandbox session; TTL auto-destroy, `km list/status`, observability
- [ ] **Phase 4: Lifecycle Hardening, Artifacts & Email** - Profile inheritance, filesystem policy, artifact upload, spot interruption handling, secret redaction, email/SES agent communication
- [x] **Phase 5: ConfigUI** - Web dashboard for profile editing, live sandbox status, and AWS resource discovery — fresh Go application at `cmd/configui/` inspired by defcon.run.34 patterns, with no external dependency (completed 2026-03-22)
- [x] **Phase 6: Budget Enforcement & Platform Configuration** - Per-sandbox compute and AI spend tracking via DynamoDB global table, http-proxy Bedrock interception for real-time token metering, threshold warnings via SES, hard enforcement via IAM revocation and proxy 403s, operator top-up via CLI; plus full platform configurability (domain, accounts, SSO, region) so forks work out of the box (completed 2026-03-22)

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
**Plans:** 4/4 plans complete

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
**Goal**: Operators can manage profiles and monitor live sandboxes through a web dashboard without using the CLI — the ConfigUI is a fresh Go application at `cmd/configui/` inspired by defcon.run.34 patterns, built entirely from source within the Klanker Maker repo with no external dependency on that repo
**Depends on**: Phase 4
**Requirements**: CFUI-01, CFUI-02, CFUI-03, CFUI-04
**Success Criteria** (what must be TRUE):
  1. Operator opens the ConfigUI in a browser, sees a profile editor with inline validation — editing a profile field and saving it runs `km validate` and shows errors without leaving the page; the ConfigUI binary is built entirely from source within the Klanker Maker repo
  2. The live sandbox status dashboard updates in real time (polling) showing all running sandboxes, their status, substrate type, and time remaining on TTL without a page refresh
  3. Operator can click into a sandbox in the dashboard and see the AWS resources it provisioned (EC2 instance ID or ECS task ARN, VPC, security groups, IAM role) discovered from AWS
  4. Operator can manage SOPS secrets from the ConfigUI — encrypting a new secret and decrypting an existing one without using the CLI
**Plans:** 4/4 plans complete

Plans:
- [ ] 05-01-PLAN.md — Server scaffold + dashboard + sandbox detail (CFUI-02, CFUI-03)
- [ ] 05-02-PLAN.md — Profile editor with Monaco + validation (CFUI-01)
- [ ] 05-03-PLAN.md — SOPS secrets management UI (CFUI-04)
- [ ] 05-04-PLAN.md — Dashboard actions + visual verification checkpoint (CFUI-01, CFUI-02, CFUI-03, CFUI-04)

### Phase 6: Budget Enforcement & Platform Configuration
**Goal**: Operators can set per-sandbox dollar budgets for compute and AI (Bedrock Anthropic models), with real-time spend tracking, threshold warnings, and hard enforcement; the platform is fully configurable for any domain and AWS account structure so anyone can fork and deploy their own instance
**Depends on**: Phase 5
**Requirements**: CONF-01, CONF-02, CONF-03, CONF-04, CONF-05, BUDG-01, BUDG-02, BUDG-03, BUDG-04, BUDG-05, BUDG-06, BUDG-07, BUDG-08, BUDG-09
**Success Criteria** (what must be TRUE):
  1. Operator runs `km configure` (or `km init`) and is walked through setting domain, AWS account IDs, SSO start URL, and region — a config file is written and all subsequent commands use these values
  2. A fork of the repo with a different domain (e.g. `mysandboxes.example.com`) works end-to-end after running `km configure` — SES emails, JSON Schema `$id`, profile `apiVersion`, and ConfigUI branding all reflect the configured domain with no hardcoded `klankermaker.ai` references
  3. Operator creates a sandbox with `spec.budget.compute.maxSpendUSD: 2.00` and `spec.budget.ai.maxSpendUSD: 5.00`; DynamoDB global table stores the budget limits alongside the sandbox record using the defcon.run.34 single-table pattern
  4. A running sandbox's compute spend is tracked as spot rate × elapsed minutes; `km status` shows current compute spend vs budget; when compute budget is exhausted, the Lambda suspends the sandbox (EC2: `StopInstances` preserving EBS; ECS Fargate: artifact upload then task stop) and the operator receives an email notification
  5. An agent inside a sandbox makes Bedrock InvokeModel calls (Haiku, Sonnet, or Opus); the http-proxy sidecar intercepts each response, extracts token usage, prices it against the model's rate from AWS Price List API, and increments the DynamoDB spend record; `km status` shows per-model AI spend breakdown
  6. When AI budget reaches 100%, the http-proxy returns HTTP 403 for subsequent Bedrock calls (real-time enforcement); additionally, the compute-budget Lambda also reads DynamoDB AI spend records and revokes the instance profile's Bedrock IAM permissions as a backstop for SDK/CLI calls that bypass the proxy — the operator receives an email notification from whichever layer fires first
  7. At 80% of either budget pool, the operator receives a warning email; the threshold is configurable via `spec.budget.warningThreshold`
  8. Operator runs `km budget add <sandbox-id> --ai 3.00` and the sandbox's AI budget increases by $3; if proxy was blocking, it unblocks; if IAM was revoked, it's restored; if EC2 was stopped, it's started; if ECS task was terminated, it's re-provisioned from the stored S3 profile
  9. DynamoDB budget table is a global table replicated to all regions where agents run — budget reads from within the sandbox hit the local regional replica with sub-millisecond latency
  10. Operator runs `km shell <sandbox-id>` and gets an interactive shell into the sandbox — the command auto-detects the substrate (EC2 via SSM Session Manager, ECS via ECS Exec) and dispatches the correct underlying AWS CLI call without the operator needing to know which substrate is running
**Plans:** 9 plans (7 complete + 2 gap closure)

Plans:
- [ ] 06-01-PLAN.md — Config struct + km configure wizard + km bootstrap stub (CONF-01, CONF-03, CONF-04)
- [ ] 06-02-PLAN.md — BudgetSpec types + DynamoDB module + BudgetAPI + PricingAPI (BUDG-01, BUDG-02, BUDG-05)
- [ ] 06-03-PLAN.md — Hardcoded domain replacement across codebase (CONF-02)
- [ ] 06-04-PLAN.md — Bedrock MITM proxy interception + SSE token metering (BUDG-04)
- [ ] 06-05-PLAN.md — Budget enforcer Lambda + compute tracking + dual-layer enforcement (BUDG-03, BUDG-07)
- [ ] 06-06-PLAN.md — km budget add + km status budget display + budget init in create (BUDG-06, BUDG-08, BUDG-09)
- [ ] 06-07-PLAN.md — ConfigUI budget dashboard + end-to-end checkpoint (BUDG-09)
- [ ] 06-08-PLAN.md — Gap closure: wire spot rate into compiler + create flow (BUDG-03)
- [ ] 06-09-PLAN.md — Gap closure: km shell substrate-abstracted interactive shell (CONF-05)

### Phase 7: Unwired Code Paths
**Goal**: Close code-level integration gaps where implementations exist but are never called — idle detection, secret redaction, MLflow session tracking, and account ID propagation all become active in production code paths
**Depends on**: Phase 6
**Requirements**: PROV-06, OBSV-07, OBSV-09, CONF-03, SCHM-04, SCHM-05
**Gap Closure:** Closes gaps from v1.0 milestone audit
**Success Criteria** (what must be TRUE):
  1. IdleDetector is invoked on a schedule for running sandboxes — idle sandboxes are detected and acted on per teardown policy
  2. Audit log sidecar binary wraps log output with RedactingDestination — secret patterns present in sandbox environment are scrubbed before reaching CloudWatch/S3
  3. Every `km create` records an MLflow run with sandbox metadata; every `km destroy` finalizes the run with duration and exit status
  4. Account IDs from km-config.yaml are consumed by site.hcl via get_env() — cross-account IAM and provider configs reference configured values
  5. Profile extends and built-in profiles are verified working and tracked as complete
**Plans:** 2/2 plans complete

Plans:
- [ ] 07-01-PLAN.md — Wire RedactingDestination + IdleDetector into audit-log sidecar binary (PROV-06, OBSV-07)
- [ ] 07-02-PLAN.md — Wire MLflow into create/destroy + site.hcl account IDs + SCHM-04/SCHM-05 verification (OBSV-09, CONF-03, SCHM-04, SCHM-05)

### Phase 8: Sidecar Build & Deployment Pipeline
**Goal**: Sidecar binaries and container images are buildable and deployable via a single command — EC2 sandboxes can download sidecars from S3 at boot, ECS sandboxes pull sidecar images from ECR
**Depends on**: Phase 7
**Requirements**: NETW-02, NETW-03, OBSV-01, OBSV-02, PROV-10
**Gap Closure:** Closes gaps from v1.0 milestone audit
**Success Criteria** (what must be TRUE):
  1. `make sidecars` cross-compiles all 4 sidecar binaries for linux/amd64 and uploads to S3
  2. `make ecr-push` builds Docker images for each sidecar and pushes to ECR
  3. Compiler emits resolvable ECR image URIs in ECS service.hcl (not literal ${var.*} strings)
  4. EC2 sandbox user-data successfully downloads sidecar binaries from S3 at boot
**Plans:** 2/2 plans complete

Plans:
- [ ] 08-01-PLAN.md — Makefile + Dockerfiles for sidecar build and deployment pipeline
- [ ] 08-02-PLAN.md — Fix ECS compiler to emit resolvable ECR image URIs

### Phase 9: Live Infrastructure & Operator Docs
**Goal**: All Terraform modules that exist but have no live deployment are deployable via Terragrunt, and operators have a setup guide documenting the full bootstrap procedure
**Depends on**: Phase 8
**Requirements**: PROV-05, BUDG-02, BUDG-06, BUDG-07, MAIL-01, INFR-01, INFR-02
**Gap Closure:** Closes gaps from v1.0 milestone audit
**Success Criteria** (what must be TRUE):
  1. `infra/live/use1/ttl-handler/terragrunt.hcl` exists and deploys the TTL Lambda + EventBridge schedule
  2. `infra/live/use1/dynamodb-budget/terragrunt.hcl` exists and deploys the budget global table
  3. `infra/live/use1/ses/terragrunt.hcl` exists and deploys SES domain verification with Route53 records
  4. Budget enforcer Lambda is deployable per-sandbox via the existing Terraform module
  5. OPERATOR-GUIDE.md documents the full setup procedure: AWS accounts, SSO, km configure, km bootstrap, km init, live infra deployment
**Plans:** 4 plans (3 complete + 1 gap closure)

Plans:
- [ ] 09-01-PLAN.md — Makefile Lambda build targets + shared Terragrunt live configs (PROV-05, BUDG-02, MAIL-01)
- [ ] 09-02-PLAN.md — Per-sandbox budget-enforcer compiler extension + km create wiring (BUDG-06, BUDG-07)
- [ ] 09-03-PLAN.md — OPERATOR-GUIDE.md full bootstrap documentation (INFR-01, INFR-02)
- [ ] 09-04-PLAN.md — Gap closure: fix budget-enforcer lambda_zip_path dist/build mismatch (BUDG-06, BUDG-07)

### Phase 10: SCP Sandbox Containment — org-level EC2 breakout prevention

**Goal:** AWS Organizations Service Control Policy (SCP) that prevents sandbox IAM roles from EC2/network/IAM breakout — even if the sandbox role's IAM policy is misconfigured. The SCP is the org-level backstop that makes sandbox containment a property of the account, not just the role.

**Requirements:** SCP-01, SCP-02, SCP-03, SCP-04, SCP-05, SCP-06, SCP-07, SCP-08, SCP-09, SCP-10, SCP-11, SCP-12
- SCP-01: SCP denies Security Group mutation (create/modify/delete) for non-provisioner roles
- SCP-02: SCP denies network escape (create VPC/subnet/route/NAT/IGW/peering/transit gateway) for non-provisioner roles
- SCP-03: SCP denies instance mutation (RunInstances, ModifyInstanceAttribute, ModifyInstanceMetadataOptions) for non-provisioner/lifecycle roles
- SCP-04: SCP denies IAM escalation (CreateRole, AttachRolePolicy, PassRole, AssumeRole) for non-provisioner/lifecycle roles
- SCP-05: SCP denies storage exfiltration (CreateSnapshot, CopySnapshot, CreateImage, ExportImage) for non-provisioner roles
- SCP-06: SCP denies SSM cross-instance pivoting (SendCommand, StartSession) for non-operator roles
- SCP-07: SCP denies Organizations/account discovery for all roles
- SCP-08: SCP enforces region lock matching `km configure` allowed regions
- SCP-09: Budget-enforcer Lambda scoped to only modify sandbox roles (km-ec2spot-ssm-*, km-ecs-task-*), not arbitrary IAM
- SCP-10: Terraform module `infra/modules/scp/` with variables for account IDs, allowed regions, role ARN patterns
- SCP-11: `km bootstrap` wires SCP creation into Management account provisioning flow
- SCP-12: Carve-outs for km-provisioner-*, km-lifecycle-*, km-ttl-handler, km-ecs-spot-handler, km-budget-enforcer-* verified against existing role naming conventions

**Depends on:** Phase 6 (budget-enforcer role naming must be stable)
**Plans:** 2/2 plans complete

Plans:
- [ ] 10-01-PLAN.md — SCP Terraform module + Terragrunt management account deployment unit (SCP-01 through SCP-08, SCP-10)
- [ ] 10-02-PLAN.md — Wire SCP deployment into km bootstrap command (SCP-09, SCP-11, SCP-12)

### Phase 11: Sandbox Auto-Destroy & Metadata Wiring
**Goal**: TTL expiry and idle timeout actually destroy sandbox resources instead of just exiting sidecars; km list and km status read metadata from the correct bucket
**Depends on**: Phase 10
**Requirements**: PROV-03, PROV-04, PROV-05, PROV-06
**Gap Closure:** Closes gaps from v1.0 milestone audit
**Success Criteria** (what must be TRUE):
  1. TTL handler Lambda calls terragrunt destroy (or equivalent) after uploading artifacts — sandbox EC2/ECS resources are fully reclaimed on TTL expiry
  2. IdleDetector.OnIdle triggers sandbox teardown via ExecuteTeardown() — idle EC2 instances are stopped/destroyed per teardown policy, not left running
  3. km list returns accurate sandbox data by reading from the same bucket/source that km create writes to — no hardcoded bucket constant diverges from runtime config
  4. km status shows correct metadata for a sandbox by reading from the same source as km list
**Plans:** 2/2 plans complete

Plans:
- [ ] 11-01-PLAN.md — Fix km list/status to use cfg.StateBucket instead of hardcoded constant (PROV-03, PROV-04)
- [ ] 11-02-PLAN.md — Wire TTL Lambda teardown + idle EventBridge publish + IAM permissions (PROV-05, PROV-06)
### Phase 12: ECS Budget Top-Up & S3 Replication Deployment
**Goal**: ECS sandboxes suspended by budget enforcement can be resumed via km budget add; S3 artifact replication has a deployable Terragrunt config
**Depends on**: Phase 11
**Requirements**: BUDG-08, OBSV-06
**Gap Closure:** Closes gaps from v1.0 milestone audit
**Success Criteria** (what must be TRUE):
  1. km budget add for an ECS sandbox re-provisions the Fargate task from the stored S3 profile — the task starts with the same container definitions and the budget enforcer resumes monitoring
  2. infra/live/use1/s3-replication/terragrunt.hcl exists and deploys the S3 replication module to a secondary region
**Plans:** 2/2 plans complete

Plans:
- [ ] 12-01-PLAN.md — ECS re-provisioning branch in km budget add (BUDG-08)
- [ ] 12-02-PLAN.md — S3 replication Terragrunt live deployment config (OBSV-06)

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9 → 10 → 11 → 12 → 13 → 14 → 15 → 16 → 17 → 18 → 19 → 20

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Schema, Compiler & AWS Foundation | 4/4 | Complete   | 2026-03-21 |
| 2. Core Provisioning & Security Baseline | 4/4 | Complete   | 2026-03-22 |
| 3. Sidecar Enforcement & Lifecycle Management | 6/6 | Complete   | 2026-03-22 |
| 4. Lifecycle Hardening, Artifacts & Email | 5/5 | Complete   | 2026-03-22 |
| 5. ConfigUI | 4/4 | Complete   | 2026-03-22 |
| 6. Budget Enforcement & Platform Configuration | 9/9 | Complete   | 2026-03-22 |
| 7. Unwired Code Paths | 2/2 | Complete   | 2026-03-22 |
| 8. Sidecar Build & Deployment Pipeline | 2/2 | Complete   | 2026-03-22 |
| 9. Live Infrastructure & Operator Docs | 4/4 | Complete   | 2026-03-23 |
| 10. SCP Sandbox Containment | 2/2 | Complete    | 2026-03-23 |
| 11. Sandbox Auto-Destroy & Metadata Wiring | 2/2 | Complete    | 2026-03-23 |
| 12. ECS Budget Top-Up & S3 Replication | 2/2 | Complete    | 2026-03-23 |
| 13. GitHub App Token Integration | 4/4 | Complete    | 2026-03-23 |
| 14. Sandbox Identity & Signed Email | 4/4 | Complete    | 2026-03-23 |
| 15. km doctor & Bootstrap Verification | 2/2 | Complete    | 2026-03-23 |
| 16. Documentation Refresh | 3/3 | Complete    | 2026-03-23 |
| 17. Sandbox Email Mailbox & Access Control | 3/3 | Complete    | 2026-03-23 |
| 18. Loose Ends | 4/4 | Complete    | 2026-03-24 |
| 19. Budget Enforcement Wiring | 2/2 | Complete    | 2026-03-25 |
| 20. Anthropic API Metering | 2/2 | Complete    | 2026-03-25 |

### Phase 13: GitHub App Token Integration — scoped repo access for sandboxes

**Goal:** Sandboxes authenticate to GitHub using short-lived, repo-scoped installation tokens from a GitHub App — not SSH keys, PATs, or long-lived credentials. Tokens are generated at sandbox creation, stored in SSM Parameter Store, and auto-refreshed by a Lambda before expiry. The profile's `sourceAccess.github` controls which repos and permissions each token covers.

**Requirements:**
- `km configure github` stores GitHub App ID, private key (in SSM/KMS), and installation ID
- At `km create` time, profile `sourceAccess.github.allowedRepos` maps to GitHub App installation token scopes: clone/fetch → `contents:read`, push → `contents:write`
- Installation token generated via GitHub App API (`POST /app/installations/{id}/access_tokens`) with repository and permission scoping
- Token stored in SSM Parameter Store at `/sandbox/{sandbox-id}/github-token`, encrypted with per-sandbox KMS key
- Sandbox boots with `GIT_ASKPASS` credential helper that reads token from SSM — no token in environment variables or user-data
- Token refresh Lambda (`km-github-token-refresher-{sandbox-id}`) generates new token before 1-hour expiry, writes to SSM
- EventBridge schedule triggers refresh Lambda every 45 minutes (15-minute safety margin before expiry)
- Token refresh is non-fatal — sandbox continues with existing token if refresh fails, logs warning
- `km destroy` cleans up: SSM parameter, EventBridge schedule, Lambda
- Terraform module `infra/modules/github-token/` encapsulates Lambda + EventBridge + SSM + IAM
- Compiler emits `github-token` module inputs in service.hcl when profile has `sourceAccess.github`
- Ref enforcement: credential helper or proxy rejects `git push` to refs not in `sourceAccess.github.allowedRepos[].refs` (defense in depth — GitHub App scoping is primary control)
- Token audit: Lambda logs token generation events to CloudWatch with repo scope and sandbox ID

**Depends on:** Phase 6 (SSM/KMS patterns), Phase 10 (SCP must allow github-token-refresher Lambda through)
**Plans:** 4/4 plans complete

Plans:
- [ ] 13-01-PLAN.md — pkg/github/ core library: JWT generation, token exchange, permission mapping (TDD) (GH-03, GH-08, GH-13)
- [ ] 13-02-PLAN.md — github-token Terraform module + SCP carve-out + Makefile build target (GH-06, GH-07, GH-10, GH-13)
- [ ] 13-03-PLAN.md — Compiler: GIT_ASKPASS credential helper + github_token_inputs in service.hcl (GH-02, GH-04, GH-05, GH-11, GH-12)
- [ ] 13-04-PLAN.md — CLI: km configure github + create/destroy token wiring (GH-01, GH-03, GH-05, GH-09)

### Phase 14: Sandbox Identity & Signed Email — Ed25519 key pairs for inter-sandbox trust

**Goal:** Every sandbox gets an Ed25519 key pair at creation. Private key stored in SSM (KMS-encrypted), public key published to a DynamoDB `km-identities` table. Outbound emails are digitally signed, inbound emails can require signature verification, and encryption is optionally layered on via X25519 key exchange. Profile controls (`email.signing`, `email.verifyInbound`, `email.encryption`) govern behavior per sandbox.

**Requirements:**
- `km create` generates Ed25519 key pair via Go `crypto/ed25519` stdlib — no external dependencies for signing
- Private key stored in SSM Parameter Store at `/sandbox/{sandbox-id}/signing-key`, encrypted with per-sandbox KMS key
- Public key published to DynamoDB `km-identities` table: `{ sandbox_id, public_key (base64), created_at, email_address }`
- `km-identities` table provisioned alongside `km-budgets` in bootstrap or init (same DynamoDB module pattern; `replica_regions` variable available for global table replication when needed, defaulting to single-region for v1)
- Outbound email signing: sandbox reads private key from SSM, signs email body with Ed25519, attaches `X-KM-Signature` and `X-KM-Sender-ID` headers
- Inbound email verification: receiving sandbox fetches sender's public key from `km-identities` DynamoDB, calls `ed25519.Verify()`
- Profile schema additions under `spec.email`: `signing` (required|optional|off), `verifyInbound` (required|optional|off), `encryption` (required|optional|off)
- Inbound verification library: `VerifyEmailSignature()` validates Ed25519 signatures on received email bodies. When `verifyInbound: required`, the library returns an error for unsigned or invalid-signature emails. NOTE: Phase 14 provides the verification library only; wiring into an SES receipt handler (Lambda/SNS trigger) to enforce rejection at delivery time requires a future phase — no inbound receipt pipeline exists yet beyond the S3 storage action from Phase 4
- Optional encryption via `golang.org/x/crypto/nacl/box` — Ed25519 keys converted to X25519 for key exchange, NaCl box for authenticated encryption
- When `encryption: optional`, encrypt if recipient's public key exists in DynamoDB, send plaintext if not
- When `encryption: required`, reject send if recipient has no published public key
- `km status` displays public key, signing policy, and encryption policy alongside email address and budget
- `km destroy` cleans up: SSM signing key parameter + DynamoDB `km-identities` row (same cleanup patterns as budget)
- Hardened and sealed built-in profiles default to `signing: required, verifyInbound: required`
- Open-dev and restricted-dev profiles default to `signing: optional, verifyInbound: optional`

**Depends on:** Phase 4 (SES email infrastructure), Phase 6 (SSM/KMS/DynamoDB patterns)
**Plans:** 4/4 plans complete

Plans:
- [ ] 14-01-PLAN.md — EmailSpec schema + DynamoDB identities module + config + built-in profile defaults (IDENT-SCHEMA, IDENT-DYNAMO, IDENT-CONFIG)
- [ ] 14-02-PLAN.md — Core identity library: Ed25519 keygen, SSM storage, DynamoDB publish, email signing/verification, NaCl encryption (TDD) (IDENT-KEYGEN, IDENT-SSM, IDENT-PUBLISH, IDENT-SIGN, IDENT-VERIFY, IDENT-ENCRYPT, IDENT-CLEANUP, IDENT-SEND-SIGNED)
- [ ] 14-03-PLAN.md — Wire identity into km create (non-fatal) + km destroy (cleanup) + km status (display) (IDENT-CREATE-WIRE, IDENT-DESTROY-WIRE, IDENT-STATUS-WIRE)
- [ ] 14-04-PLAN.md — Gap closure: add Signing/Verify Inbound/Encryption policy fields to km status Identity section (IDENT-STATUS-WIRE)

### Phase 15: km doctor — platform health check and bootstrap verification

**Goal:** `km doctor` command that validates the entire platform setup — config, AWS credentials, bootstrap resources, per-region infrastructure, and active sandboxes — and outputs a structured health report with actionable remediation for any issues found. Also includes `km configure github --setup` manifest flow for one-click GitHub App creation.

**Requirements:**
- `km doctor` Cobra command in `internal/app/cmd/doctor.go` with colored terminal output (✓/✗/⚠ symbols)
- Config check: verify `km-config.yaml` exists and contains required fields (domain, account IDs, SSO URL, primary region)
- AWS credential check: `sts:GetCallerIdentity` against each configured profile (management, terraform, application) — report authenticated identity or auth failure
- Bootstrap check: verify S3 state bucket (`s3:HeadBucket`), DynamoDB lock table (`dynamodb:DescribeTable`), KMS key (`kms:DescribeKey` by alias)
- SCP check: verify SCP policy attached to Application account OU (`organizations:ListPoliciesForTarget`) — only if management credentials available
- GitHub App check: verify SSM parameters `/km/config/github/app-id` and `/km/config/github/installation-id` exist — report "not configured" if missing (informational, not error)
- Per-region check for each initialized region: VPC exists with km tags (`ec2:DescribeVpcs`), subnets present, DynamoDB budget table (`km-budgets`), DynamoDB identity table (`km-identities`)
- Sandbox summary: count active/suspended/expired sandboxes via existing `km list` logic
- Exit code: 0 if all checks pass, 1 if any errors found — enables CI/scripted usage
- `--json` flag for machine-readable output (array of check results with name, status, message, remediation)
- `--quiet` flag that only shows failures and warnings (skip passing checks)
- Each check is independent and non-fatal — a failed AWS call for one check doesn't prevent other checks from running
- Checks run in parallel where possible (credential checks, region checks) for speed
- `km configure github --setup` manifest flow: generates GitHub App manifest JSON (permissions: `contents: write`, no webhook), opens browser to `https://github.com/settings/apps/new?manifest=...`, operator clicks "Create GitHub App", exchanges temporary code for App credentials via `POST /app-manifests/{code}/conversions`, stores App ID + private key + installation ID in SSM automatically
- Manifest flow pre-fills: app name (`klanker-maker-sandbox`), permissions (`contents: read/write`), no webhook URL, no events — minimal App with exactly what sandboxes need
- After manifest exchange, automatically runs `km configure github` logic to store credentials in SSM — no manual copy-paste of App ID or PEM files

**Depends on:** Phase 6 (config/bootstrap patterns), Phase 10 (SCP), Phase 13 (GitHub App config), Phase 14 (identity table)
**Plans:** 2/2 plans complete

Plans:
- [ ] 15-01-PLAN.md — km doctor command with parallel platform health checks, colored output, JSON/quiet modes
- [ ] 15-02-PLAN.md — km configure github --setup manifest flow for one-click GitHub App creation

### Phase 16: Documentation refresh — operator guide, user manual, and docs for Phases 6-15 features

**Goal:** Bring all documentation up to date with features built in Phases 6–15. The operator guide, user manual, and specialized docs were written during early phases and are missing budget enforcement, SCP containment, sidecar build pipeline, GitHub App integration, sandbox identity/signed email, and km doctor.

**Requirements:**
- **docs/operator-guide.md** updates:
  - `km bootstrap` command reference (replaces manual S3/DynamoDB/KMS setup steps)
  - Budget enforcement: DynamoDB `km-budgets` table, budget-enforcer Lambda, EventBridge schedule, `km budget add` top-up flow
  - SCP sandbox containment: SCP deployment via `km bootstrap`, what the SCP blocks, role carve-outs, management account prerequisites
  - Sidecar build pipeline: `make sidecars` (cross-compile), `make ecr-push` (Docker + ECR), S3 binary upload for EC2
  - GitHub App setup: `km configure github --setup` manifest flow, manual alternative, SSM parameter layout
  - GitHub token refresh Lambda: per-sandbox Lambda + EventBridge schedule, IAM, cleanup
  - DynamoDB `km-identities` table for sandbox identity (provisioned alongside `km-budgets`)
  - `km doctor` command: what it checks, `--json`/`--quiet` flags, CI usage
- **docs/user-manual.md** updates:
  - `km doctor` usage and output interpretation
  - `km configure github` (both manual and `--setup` flow)
  - `km budget add` / `km status` budget breakdown
  - Profile `spec.email` section (signing, verifyInbound, encryption policies)
  - Profile `sourceAccess.github` with GitHub App token explanation
- **docs/budget-guide.md** updates:
  - Budget-enforcer Lambda architecture (per-sandbox Lambda, DynamoDB Streams trigger vs EventBridge)
  - Compute budget: spot rate lookup, suspend vs destroy, EC2 StopInstances vs ECS task stop
  - AI budget: Bedrock proxy metering, dual-layer enforcement, per-model breakdown
  - `km budget add` top-up flow: proxy unblock + IAM restore + compute restart
- **docs/security-model.md** updates:
  - SCP layer: what each deny statement blocks, carve-out roles, region lock
  - GitHub App tokens: short-lived, repo-scoped, no SSH keys or PATs
  - Sandbox identity: Ed25519 signing, email verification, optional encryption
- **docs/multi-agent-email.md** updates:
  - Signed email: X-KM-Signature / X-KM-Sender-ID headers, verification flow
  - Optional encryption: X25519 key exchange, NaCl box, DynamoDB public key discovery
  - Profile `spec.email` policy controls per sandbox
- **docs/sidecar-reference.md** updates:
  - Build pipeline: Makefile targets, Dockerfiles, ECR image URIs
  - S3 binary delivery for EC2 sidecars
- README.md roadmap table: update phase statuses to reflect completion through Phase 15
- All docs reviewed for stale references to old paths (e.g., `infra/live/sandboxes/_template/` → `infra/templates/sandbox/`)

**Depends on:** Phase 15 (all features must be implemented before documenting)
**Plans:** 3/3 plans complete

Plans:
- [ ] 16-01-PLAN.md — Operator guide, user manual, and README updates
- [ ] 16-02-PLAN.md — Budget guide and security model updates
- [ ] 16-03-PLAN.md — Multi-agent email guide and sidecar reference updates

### Phase 17: Sandbox Email Mailbox & Access Control — aliases, allow-lists, self-mail, S3 reader

**Goal:** Sandbox aliases (human-friendly dot-notation names like `research.team-a`), profile-driven email allow-lists controlling which sandboxes can send to this sandbox (even if they have valid public keys), implicit self-mail capability for long-term agent memory, and a Go library for reading/parsing raw MIME emails stored in S3 by the SES receipt rule.

**Requirements:**
- Sandbox alias field in profile schema (`spec.email.alias`) — optional dot-notation name (e.g., `research.team-a`, `build.frontend`) registered in `km-identities` DynamoDB alongside sandbox ID
- Alias lookup: `FetchPublicKeyByAlias()` resolves alias → sandbox identity record, enabling addressing by name instead of ID
- `km-identities` DynamoDB table gains a GSI on alias for efficient alias→identity lookups
- Email allow-list in profile schema (`spec.email.allowedSenders[]`) — array of patterns: `"self"` (always implicit), specific sandbox IDs (`sb-a1b2c3d4`), alias patterns with wildcards (`build.*`), or `"*"` for open access
- Allow-list enforcement: receiving sandbox checks sender against allow-list before accepting email, even if sender has a valid signature — separate from signature verification
- Self-mail always permitted regardless of allow-list configuration — sandbox can email its own address for persistent storage / long-term memory
- `pkg/aws/mailbox.go` library: `ListMailboxMessages()` lists S3 objects under the sandbox's mail prefix, `ReadMessage()` fetches and parses a raw MIME message, `ParseSignedMessage()` extracts `X-KM-Sender-ID`, `X-KM-Signature`, `X-KM-Encrypted` headers and body
- Mailbox reader handles both signed/encrypted and plaintext messages gracefully
- `km status` displays alias (if set) and allow-list summary alongside existing identity fields
- Built-in profile defaults: hardened/sealed get restrictive allow-lists (`self` only), open-dev/restricted-dev get `"*"` (any sandbox)

**Depends on:** Phase 4 (SES email/S3 storage), Phase 14 (identity/signing infrastructure)
**Plans:** 3/3 plans complete

Plans:
- [ ] 17-01-PLAN.md — EmailSpec type + JSON schema extension (alias, allowedSenders) + DynamoDB identities v1.1.0 GSI + built-in profile defaults
- [ ] 17-02-PLAN.md — Identity extension (FetchPublicKeyByAlias, MatchesAllowList, PublishIdentity) + mailbox reader library (mailbox.go)
- [ ] 17-03-PLAN.md — CLI wiring (create.go PublishIdentity call + km status alias/allowedSenders display)

### Phase 18: Loose Ends — km init deploys all regional infra, km uninit teardown, bootstrap KMS, github-token graceful skip

**Goal:** Close all operational gaps discovered during live testing — `km init` deploys all regional infrastructure (not just the VPC), `km uninit` tears it all down, bootstrap creates the KMS platform key, github-token module skips gracefully when unconfigured, and `km configure` populates `state_bucket` automatically.

**Requirements:**
- `km init` deploys all regional infrastructure in one command: network (VPC/subnets/SGs), DynamoDB budget table, DynamoDB identities table, SES domain/DKIM/receipt rules, S3 replication, TTL handler Lambda — not just the network
- `km init` is idempotent — re-running applies any missing resources without destroying existing ones
- `km uninit --region <region>` tears down all regional infrastructure in reverse dependency order: sandboxes first (error if active, `--force` to override), then TTL handler, SES, S3 replication, DynamoDB tables, network last
- `km uninit` refuses to run if active sandboxes exist in the region unless `--force` is passed
- `km bootstrap` creates KMS key with alias `alias/km-platform` if it doesn't exist (already implemented — verify it works end-to-end with `km create`)
- `km bootstrap --show-prereqs` output matches the actual working policy (SSO path fix, three-statement least-privilege, tag permissions, SCP enable step — already implemented, verify accuracy)
- GitHub token module (`infra/modules/github-token`) defaults `sandbox_iam_role_arn` variable or the compiler skips generating github-token HCL entirely when GitHub App is not configured (no SSM params for app-id/installation-id)
- `km configure` auto-detects or prompts for `state_bucket` and writes it to `km-config.yaml` — `km list`/`km status` fail without it
- `km create` non-fatal warnings for github-token and identity are clearly labeled as "skipped (not configured)" rather than error stack traces
- Remove stale top-level `infra/live/network/` directory remnants (untracked cache files) if any remain
- `root.hcl` rename is complete and all Terragrunt configs reference `root.hcl` (already done — verify no regressions)
- `km doctor` checks that all regional infra is deployed (DynamoDB tables, SES, TTL handler) not just network/VPC

**Depends on:** Phase 15 (km doctor, bootstrap), Phase 14 (identity table)
**Plans:** 4/4 plans complete

Plans:
- [ ] 18-01-PLAN.md — Expand km init to all 6 regional modules + km configure state_bucket
- [ ] 18-02-PLAN.md — New km uninit command with active-sandbox guard
- [ ] 18-03-PLAN.md — GitHub token graceful skip on ParameterNotFound
- [ ] 18-04-PLAN.md — km doctor regional checks + bootstrap/root.hcl verification

### Phase 19: Budget Enforcement Wiring — EC2 hard stop, IAM revocation, resume tag fix

**Goal:** Fix two cross-phase wiring gaps that cause budget hard enforcement to silently fail at runtime: (1) budget-enforcer Lambda receives empty `instance_id`/`role_arn` because its Terragrunt config has no dependency on the ec2spot module, and (2) `km budget add` EC2 resume uses the wrong tag key (`tag:sandbox-id` instead of `tag:km:sandbox-id`).

**Requirements:** BUDG-07, BUDG-08
**Gap Closure:** Closes gaps from v1.0 milestone audit (2026-03-24)
**Success Criteria** (what must be TRUE):
  1. Budget-enforcer Lambda EventBridge payload contains the actual EC2 instance ID and IAM role ARN from the provisioned sandbox — not empty strings
  2. At 100% compute budget, the Lambda successfully calls `StopInstances` on the sandbox EC2 instance
  3. At 100% AI budget, the Lambda successfully revokes Bedrock permissions from the sandbox IAM role
  4. `km budget add` for a stopped EC2 sandbox finds the instance via `tag:km:sandbox-id` filter and starts it
  5. Unit tests verify the compiler emits a Terragrunt dependency block from budget-enforcer to the parent sandbox
  6. Unit tests verify the tag key used in `resumeEC2Sandbox` matches `km:sandbox-id`

**Depends on:** Phase 6 (budget-enforcer), Phase 2 (ec2spot module)
**Plans:** 2/2 plans complete

Plans:
- [ ] 19-01-PLAN.md — Wire budget-enforcer dependency block to ec2spot module (BUDG-07)
- [ ] 19-02-PLAN.md — Fix EC2 resume tag filter key mismatch (BUDG-08)

### Phase 20: Anthropic API Metering + Terragrunt Output Suppression

**Goal:** Two improvements: (1) Extend the http-proxy sidecar's AI spend metering to intercept Anthropic API calls (`api.anthropic.com/v1/messages`) in addition to Bedrock — sandboxes running Claude Code get the same per-token budget tracking, threshold warnings, and hard enforcement (proxy 403) as Bedrock workloads. (2) Suppress terragrunt/terraform output by default across all CLI commands — show only step summaries unless `--verbose` is passed.

**Requirements:** BUDG-10, OPER-01
**Success Criteria** (what must be TRUE):
  1. http-proxy sidecar detects outbound requests to `api.anthropic.com/v1/messages` and intercepts the response
  2. For non-streaming responses, proxy extracts `usage.input_tokens` and `usage.output_tokens` from the JSON body and increments DynamoDB AI spend via `IncrementAISpend`
  3. For SSE streaming responses, proxy reads cumulative `output_tokens` from the `message_delta` event and increments DynamoDB AI spend
  4. Model rates for Anthropic API models (claude-sonnet-4-20250514, claude-opus-4-20250514, claude-haiku-4-5-20251001, etc.) are sourced from a rate table (static or configurable) and applied to token counts
  5. `km status` AI breakdown shows Anthropic API spend alongside Bedrock spend (same per-model format)
  6. At 100% AI budget, proxy returns 403 for Anthropic API calls (same enforcement as Bedrock)
  7. Unit tests verify Anthropic response parsing for both streaming and non-streaming formats
  8. `km create`, `km destroy`, `km init`, and `km uninit` suppress terragrunt/terraform output by default — show step-level summaries (e.g., "Applying network... done", "Destroying ttl-handler... done") instead of raw HCL output
  9. `--verbose` flag on all terragrunt-calling commands restores full terragrunt output streaming to stdout/stderr
  10. Errors and warnings from terragrunt are always shown regardless of verbose mode

**Depends on:** Phase 19 (budget enforcement wiring must work first)
**Plans:** 2/2 plans complete

Plans:
- [ ] 20-01-PLAN.md — Anthropic API token extraction and budget enforcement (BUDG-10)
- [ ] 20-02-PLAN.md — Terragrunt output suppression with --verbose flag (OPER-01)

### Phase 21: Bug fixes and mini-features — budget precision, polish, small enhancements

**Goal:** Polish, harden, and validate the platform with bug fixes, small features, and E2E verification

**Scope:**
1. Budget display precision — 4 decimal places to show sub-penny spend changes
2. CloudWatch log export on teardown — archive sandbox logs to S3 as a standard artifact during idle/TTL-triggered destroy
3. E2E sidecar verification — confirm all 4 sidecars work: DNS proxy, HTTP proxy, audit log, OTel tracing
4. GitHub repo cloning/locking validation — verify GitHub App token integration and repo access
5. Inter-sandbox email send/receive test — two klankers email each other
6. Email allow-list enforcement test — only `whereiskurt@gmail.com` can email a klanker
7. Safe phrase email override — embed a secret phrase in email to authorize/override klanker actions
8. Klanker action approval via email — klanker emails `whereiskurt+klankerqq@gmail.com` to request allow/deny on actions
9. One-time password sync — bootstrap credential/secret sync into sandboxes

**Requirements**: Polish/hardening phase (no specific requirement IDs)
**Depends on:** Phase 20
**Plans:** 4/4 plans complete

Plans:
- [ ] 21-01-PLAN.md — Budget display precision (%.4f) + CloudWatch log export on teardown
- [ ] 21-02-PLAN.md — Safe phrase email override + OTP secret sync
- [ ] 21-03-PLAN.md — Action approval via email (send request + poll for reply)
- [ ] 21-04-PLAN.md — E2E verification checklist + operator review checkpoint

### Phase 22: Remote Sandbox Creation — `km create --remote` via Lambda + email-to-create

**Goal:** Enable sandbox creation without local terraform/terragrunt, via Lambda dispatch and email triggers

**Scope:**
1. **Create Lambda** — new Lambda with `km` binary + terragrunt + terraform + modules bundled (container image for size)
2. **`km create --remote`** — compile profile locally, upload artifacts to S3, publish EventBridge event, Lambda runs `terragrunt apply`
3. **Email-to-create** — SES receipt rule for `create@sandboxes.{domain}`, parses YAML attachment, verifies safe phrase, triggers create Lambda
4. **Safe phrase auth** — `KM-AUTH: <phrase>` in email body must match operator-configured safe phrase in SSM to authorize creation
5. **EventBridge integration** — new rule routes `SandboxCreate` events to the create Lambda
6. **Status notification** — Lambda emails operator with sandbox ID and connection details on success, error details on failure

**Requirements**: REMOTE-01 through REMOTE-06
**Depends on:** Phase 21, Phase 17 (email infrastructure), Phase 14 (identity/signing)
**Success Criteria** (what must be TRUE):
  1. Operator runs `km create --remote <profile>` and the profile is compiled locally, artifacts are uploaded to S3, a SandboxCreate EventBridge event is published, and the create-handler Lambda picks it up and runs `km create` inside a container image — the sandbox is provisioned without terraform/terragrunt installed locally
  2. Operator emails a YAML profile to `create@sandboxes.{domain}` with `KM-AUTH: <phrase>` in the body; the email-create Lambda parses the MIME attachment, validates the safe phrase against SSM, and triggers sandbox creation via EventBridge — wrong or missing phrases result in a rejection email
  3. The create-handler Lambda runs as a container image (ECR, arm64) bundling km binary + terraform + terragrunt + infra/modules; EventBridge rule routes SandboxCreate events to it with 0 retry attempts
  4. On success or failure, the operator receives an email notification with sandbox ID and connection details (success) or error details (failure)
**Plans:** 3/3 plans complete

Plans:
- [ ] 22-01-PLAN.md — EventBridge SDK package + km create --remote flag + create-handler Lambda (REMOTE-01, REMOTE-02, REMOTE-05, REMOTE-06)
- [ ] 22-02-PLAN.md — Email-create-handler Lambda + KMAuthPattern export + MIME parsing (REMOTE-03, REMOTE-04)
- [ ] 22-03-PLAN.md — Terraform module + Dockerfile + SES receipt rule + Makefile + live config (REMOTE-01, REMOTE-03, REMOTE-05)

### Phase 23: Credential Rotation — `km roll creds` for platform and sandbox secrets

**Goal:** One-command credential rotation for all platform and per-sandbox secrets

**Scope:**
1. **`km roll creds`** — rotates all platform credentials:
   - GitHub App private key (regenerate + update SSM)
   - KMS key rotation trigger (alias swap)
   - Proxy CA cert+key (regenerate, upload to S3, restart proxies on running sandboxes)
   - Ed25519 signing keys for all running sandboxes (new key pair, update DynamoDB, update SSM)
2. **`km roll creds --sandbox <id>`** — rotate secrets for a single sandbox:
   - Ed25519 signing key pair
   - GitHub token (force refresh)
   - SSM parameters re-encrypted with current KMS key
3. **`km roll creds --platform`** — rotate only platform-level secrets (GitHub App, proxy CA, KMS)
4. **Audit trail** — each rotation logged to CloudWatch with before/after key fingerprints
5. **Zero-downtime rotation** — running sandboxes pick up new secrets on next poll cycle (no restart needed for tokens; proxy CA requires sidecar restart via SSM SendCommand)
6. **`km doctor --check-rotation`** — warn if any credential hasn't been rotated in >90 days

**Requirements**: CRED-01 through CRED-06
**Depends on:** Phase 13 (GitHub App), Phase 14 (identity keys), Phase 6 (SSM/KMS)
**Plans:** 3/3 plans complete

Plans:
- [ ] 23-01-PLAN.md — Core rotation library: Ed25519, proxy CA, SSM re-encryption, CloudWatch audit (CRED-04)
- [ ] 23-02-PLAN.md — km roll creds Cobra command with --sandbox, --platform, --force-restart flags (CRED-01, CRED-02, CRED-03, CRED-05)
- [ ] 23-03-PLAN.md — km doctor credential rotation age check (CRED-06)

### Phase 24: Documentation Refresh (Phases 18-32)

**Goal:** Update operator guide, user manual, README, and inline docs to cover all features through Phase 32

**Scope:**
1. README roadmap table — update statuses
2. Operator guide — remote create, email triggers, credential rotation procedures, OTEL observability, MITM proxy filtering
3. User manual — `km stop`, `km extend --remote`, `km destroy --remote`, `km roll creds`, configurable sandbox ID prefix, `km pause`, `km lock`, `km unlock`, `km list --wide`
4. Security model — credential rotation lifecycle, email auth flow, GitHub repo-level MITM enforcement, sandbox lock safety mechanism
5. Profile reference — new fields from Phases 22-32 (telemetry, MITM filtering, metadata.prefix, rsyncPaths, rsyncFileDetails)
6. Observability guide — Claude Code OTEL integration, telemetry pipeline, S3 storage, learning-mode proxy traffic recording
7. Lifecycle guide — `km pause` (EC2 hibernate), `km lock`/`km unlock` (safety lock), lock guards on destroy/stop/pause
8. Rsync guide — profile-scoped rsync paths, external file lists, shell wildcard support

**Depends on:** Phase 32

### Phase 25: GitHub Source Access Restrictions — deep testing of repo allowlists, clone/push enforcement, and deny-by-default for unlisted repos

**Goal:** Comprehensive test coverage for GitHub source access enforcement (deny-by-default, permission edge cases, wildcard patterns) plus implement ref enforcement via git pre-push hooks and document ECS credential delivery gap
**Requirements**: [GH25-01, GH25-02, GH25-03, GH25-04, GH25-05]
**Depends on:** Phase 24
**Plans:** 2/2 plans complete

Plans:
- [ ] 25-01-PLAN.md — Deny-by-default tests for empty allowedRepos, permission edge cases, wildcard validation
- [ ] 25-02-PLAN.md — Ref enforcement via pre-push hooks, security documentation update
### Phase 26: Live Operations Hardening — bootstrap, init, create, destroy, TTL auto-destroy, idle detection, sidecar fixes, proxy enforcement, CLI polish

**Goal:** Harden the platform after extensive live testing (~60 commits). Fix remaining test failures, backfill critical-path test coverage, polish CLI UX (aliases, completion, help text, color), test --remote flag, audit multi-region code, and implement max lifetime cap.
**Requirements**: [HARD-01, HARD-02, HARD-03, HARD-04, HARD-05, HARD-06]
**Depends on:** Phase 25
**Plans:** 5/4 plans complete

Plans:
- [x] 26-01-SUMMARY.md — Ad-hoc live operations work (bootstrap, init, create, destroy, TTL, idle, sidecars, CLI polish)
- [ ] 26-02-PLAN.md — Fix test failures, multi-region audit, max lifetime cap
- [ ] 26-03-PLAN.md — CLI UX: aliases, completion, help text, color styling
- [ ] 26-04-PLAN.md — Remote flag testing, failed status in km list, test backfill
- [ ] 26-05-PLAN.md — Gap closure: populate MaxLifetime in metadata at create time

### Phase 27: Claude Code OTEL Integration — sandbox observability via built-in telemetry

**Goal:** Claude Code running inside sandboxes exports full OpenTelemetry telemetry (prompts, tool calls, API requests, token usage, cost metrics) through the existing OTel Collector sidecar to S3 — giving operators complete visibility into agent behavior, spend, and performance per sandbox session

**Requirements**: OTEL-01, OTEL-02, OTEL-03, OTEL-04, OTEL-05, OTEL-06, OTEL-07
- OTEL-01: Claude Code OTEL env vars (CLAUDE_CODE_ENABLE_TELEMETRY, OTEL_METRICS_EXPORTER, OTEL_LOGS_EXPORTER, OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_LOG_USER_PROMPTS, OTEL_LOG_TOOL_DETAILS) injected into sandbox via user-data (EC2) and container environment (ECS)
- OTEL-02: OTel Collector sidecar config extended with `logs` and `metrics` pipelines (currently only has `traces`) — all three signal types exported to S3 at `s3://<bucket>/{signal}/{sandbox-id}/`
- OTEL-03: Claude Code prompt events (`claude_code.user_prompt`), tool result events (`claude_code.tool_result`), API request events (`claude_code.api_request`), and API error events (`claude_code.api_error`) flow through the collector to S3 in OTLP JSON format
- OTEL-04: Claude Code metrics (`claude_code.token.usage`, `claude_code.cost.usage`, `claude_code.session.count`, `claude_code.lines_of_code.count`, `claude_code.active_time.total`) flow through the collector to S3
- OTEL-05: Profile schema supports operator control over telemetry: `spec.observability.claudeTelemetry` with fields for enabling/disabling prompt logging and tool detail logging per profile
- OTEL-06: OTEL_RESOURCE_ATTRIBUTES includes sandbox_id, profile_name, and substrate for per-sandbox filtering in downstream analysis
- OTEL-07: Collector HTTP endpoint (4318) added to sandbox network allowlist so Claude Code OTLP HTTP exports reach the local collector without being blocked by the HTTP proxy

**Depends on:** Phase 26
**Plans:** 3/3 plans complete

Plans:
- [ ] 27-01-PLAN.md — Profile schema + collector config (claudeTelemetry types, logs/metrics pipelines)
- [ ] 27-02-PLAN.md — Compiler env var injection (EC2 user-data + ECS container env)
- [ ] 27-03-PLAN.md — EC2 km-tracing systemd unit (otelcol-contrib binary download + service start)

### Phase 28: GitHub repo-level MITM filtering in HTTP proxy

**Goal:** Enforce sourceAccess.github.allowedRepos at the network layer via MITM path inspection in the HTTP proxy sidecar — close the gap where github.com in allowedHosts permits access to any public repo
**Requirements**: NETW-08
**Depends on:** Phase 27
**Plans:** 2/2 plans complete

Plans:
- [ ] 28-01-PLAN.md — GitHub MITM proxy core (TDD: ExtractRepoFromPath, IsRepoAllowed, handler registration)
- [ ] 28-02-PLAN.md — Compiler wiring (EC2 userdata + ECS service.hcl + main.go env var)

### Phase 29: Configurable Sandbox ID Prefix — profile-driven prefix replaces hardcoded 'sb'

**Goal:** Sandbox ID prefix is configurable per profile via `metadata.prefix` and sandboxes can be addressed by human-friendly aliases — operators define meaningful prefixes (e.g., `claude`, `build`, `research`) that replace the hardcoded `sb-` prefix, and can assign aliases (e.g., `orc`, `wrkr`) via `--alias` flag or profile-level `metadata.alias` template with auto-incrementing suffix. Profiles without `metadata.prefix` default to `sb` for backwards compatibility.
**Requirements**: PREFIX-01, PREFIX-02, PREFIX-03, PREFIX-04, PREFIX-05, ALIAS-01, ALIAS-02, ALIAS-03, ALIAS-04
**Depends on:** Phase 28
**Success Criteria** (what must be TRUE):
  1. Profile schema supports optional `metadata.prefix` field — `km validate` accepts profiles with and without it; prefix must match `^[a-z][a-z0-9]{0,11}$` (lowercase, starts with letter, max 12 chars)
  2. `GenerateSandboxID()` accepts a prefix parameter — a profile with `metadata.prefix: claude` generates IDs like `claude-a1b2c3d4` instead of `sb-a1b2c3d4`
  3. All sandbox ID validation patterns (`destroy.go`, `sandbox_ref.go`, `email-create-handler`) accept any valid prefix, not just `sb-`
  4. Compiler output (S3 paths, SSM parameters, IAM role names, CloudWatch log groups, email addresses) uses the sandbox ID as-is with the profile-specified prefix — no component assumes the `sb-` prefix
  5. Built-in profiles (`profiles/*.yaml`) are updated: `claude-dev.yaml` gets `prefix: claude`, others get appropriate prefixes or omit the field to default to `sb`
  6. Existing sandboxes created with `sb-` prefix continue to work — `km list`, `km status`, `km destroy` operate on the full sandbox ID regardless of prefix
  7. `km create <profile> --alias orc` stores alias in S3 metadata.json — `km destroy orc`, `km status orc`, and all other commands resolve the alias to the real sandbox ID
  8. Profile-level `metadata.alias` acts as a template — creating sandboxes from a profile with `metadata.alias: orc` auto-generates `orc-1`, `orc-2`, `orc-3` etc. by scanning active sandboxes
  9. `--alias` flag on `km create` overrides the profile-level template
  10. Alias is freed when sandbox is destroyed — a new sandbox can reuse a previously destroyed alias
  11. `km list` output shows alias column alongside sandbox ID
**Plans:** 3/3 plans complete

Plans:
- [ ] 29-01-PLAN.md — Schema prefix field + parameterized GenerateSandboxID
- [ ] 29-02-PLAN.md — Generalize validation patterns + fix email handler + update profiles
- [ ] 29-03-PLAN.md — Sandbox aliases: --alias flag, profile template, ResolveSandboxRef, km list display

### Phase 27: AI Spend Metering — Extract Token Counts from MITM'd Bedrock Streaming Responses

**Goal:** Complete the AI budget tracking pipeline by extracting token counts and costs from MITM-intercepted Bedrock InvokeModelWithResponseStream responses.
**Requirements**: TBD
**Depends on:** Phase 26
**Plans:** 2 plans

Plans:
- [ ] 43-01-PLAN.md — EFS Terraform module, Terragrunt config, profile fields, km init registration
- [ ] 43-02-PLAN.md — LoadEFSOutputs, NetworkConfig wiring, userdata EFS mount block, destroy no-op verification

Context:
- MITM proxy successfully intercepts Bedrock HTTPS CONNECT tunnels (verified)
- Bedrock returns 200 responses through the proxy (verified) 
- The response handler fires but token extraction from streaming (SSE/chunked) responses needs implementation
- Bedrock streaming responses deliver token counts in the final SSE chunk or response headers (x-amzn-bedrock-input-token-count, x-amzn-bedrock-output-token-count)
- The proxy's DynamoDB write path is wired (EC2 instance role has km-budgets access)
- Compute budget tracking works end-to-end (verified)

Key files:
- sidecars/http-proxy/httpproxy/proxy.go — Bedrock OnResponse handler (line ~191)
- sidecars/http-proxy/httpproxy/bedrock.go — Bedrock token extraction logic
- sidecars/http-proxy/httpproxy/anthropic.go — Anthropic direct API token extraction (reference)
- pkg/aws/budget.go — DynamoDB budget read/write

Plans:
- [x] TBD (run /gsd:plan-phase 27 to break down) (completed 2026-03-28)

### Phase 30: sandbox lifecycle commands - km pause km lock km unlock

**Goal:** Working km pause, km lock, and km unlock commands with lock enforcement guards
**Requirements**: [PAUSE-01, PAUSE-02, PAUSE-03, LOCK-01, LOCK-02, LOCK-03, UNLOCK-01, UNLOCK-02]
**Depends on:** Phase 29
**Plans:** 2/2 plans complete

Plans:
- [ ] 30-01-PLAN.md — km pause command + SandboxMetadata lock fields
- [ ] 30-02-PLAN.md — km lock/unlock commands + lock guards + root.go wiring

### Phase 31: Allowlist profile generator — observe sandbox traffic via eBPF TLS uprobes + proxy logs, auto-generate minimal SandboxProfile YAML

**Goal:** Run a sandbox in "learning mode" that records all observed DNS, HTTP, and TLS traffic from Phase 41 uprobe events and Phase 40 eBPF audit logs, then generates a minimal `SandboxProfile` YAML with only the DNS suffixes, hosts, and GitHub repos actually used. Leverages the existing TLS uprobe consumer (pkg/ebpf/tls/), eBPF audit ring buffer (pkg/ebpf/audit/), and MITM proxy logs as data sources. Output is a ready-to-use profile YAML that can be reviewed and applied.
**Requirements**: [AGEN-01, AGEN-02, AGEN-03, AGEN-04]
**Depends on:** Phase 41 (TLS uprobe observability), Phase 40 (eBPF cgroup enforcement)
**Plans:** 2/2 plans complete

Plans:
- [ ] 31-01-PLAN.md — Core pkg/allowlistgen: Recorder, DNS suffix normalization, SandboxProfile generator (TDD)
- [ ] 31-02-PLAN.md — km shell --learn flag, DNS resolver DomainObserver hook, ebpf-attach --observe with S3 learn session upload


### Phase 32: Profile-scoped rsync paths with external file lists and shell wildcards

**Goal:** Move rsync path configuration from global km-config.yaml into per-profile YAML with external file list references and shell wildcard support
**Requirements**: [RSYNC-01, RSYNC-02, RSYNC-03, RSYNC-04, RSYNC-05, RSYNC-06]
**Depends on:** Phase 31
**Plans:** 3/3 plans complete

Plans:
- [ ] 32-01-PLAN.md — Add rsyncPaths and rsyncFileList fields to profile schema and types
- [ ] 32-02-PLAN.md — Wire profile-scoped paths into km rsync save with wildcard support
- [ ] 32-03-PLAN.md — Gap closure: TestRsyncSaveCmd test + live sandbox verification

### Phase 33: EC2 storage customization and AMI selection - profile-driven root volume sizing, optional additional EBS volumes with auto-mount, hibernation support for on-demand instances, and loose AMI specification resolved per-region

**Goal:** Profiles can specify root volume sizing, optional additional EBS volumes with auto-mount, hibernation for on-demand instances, and loose AMI slugs resolved per-region -- extending the EC2 provisioning pipeline from schema through compiler to Terraform
**Requirements**: P33-01, P33-02, P33-03, P33-04, P33-05, P33-06, P33-07, P33-08
**Depends on:** Phase 32
**Plans:** 3/3 plans complete

Plans:
- [ ] 33-01-PLAN.md — Profile types, JSON schema, and semantic validation tests for storage/AMI fields (TDD)
- [ ] 33-02-PLAN.md — Compiler HCL + Terraform module for AMI resolution, root volume sizing, hibernation
- [ ] 33-03-PLAN.md — Additional EBS volume: Terraform resources, compiler HCL, userdata auto-mount

### Phase 34: Agent Profiles: agent-orchestrator, goose, and codex sandbox profiles

**Goal:** Three new SandboxProfile YAML files (agent-orchestrator, goose, codex) are added to profiles/ and pass km validate, giving operators ready-to-use sandbox environments for the broader AI coding agent ecosystem
**Requirements**: PROF-34-01, PROF-34-02, PROF-34-03, PROF-34-04
**Depends on:** Phase 33
**Plans:** 1/1 plans complete

Plans:
- [ ] 34-01-PLAN.md — Create agent-orchestrator, goose, and codex profile YAML files and validate

### Phase 35: MITM CA trust for Python, Node, and non-system SSL libraries

**Goal:** Tools that bundle their own CA stores (Python certifi, Node.js, Rust webpki-roots) trust the km proxy CA so MITM interception works for budget metering and GitHub repo filtering — without `SSLCertVerificationError` or equivalent
**Depends on:** Phase 34
**Plans:** 1/1 plans complete

Plans:
- [ ] 35-01-PLAN.md — Add SSL_CERT_FILE, REQUESTS_CA_BUNDLE, CURL_CA_BUNDLE, NODE_EXTRA_CA_CERTS env vars to user-data template

### Phase 36: km-sandbox base container image

**Goal:** A `km-sandbox` base container image that provides the same sandbox environment as EC2 user-data — proxy CA trust, secret injection, GitHub credentials, initCommands, rsync restore, OTEL telemetry, and mail polling — all driven by environment variables via a container entrypoint script. This is the foundation for both Docker local and EKS substrates.
**Depends on:** Phase 35
**Requirements:** PROV-09, PROV-10
**Plans:** 4/4 plans complete

Plans:
- [ ] 36-01-PLAN.md — Dockerfile + entrypoint.sh (containers/sandbox/)
- [ ] 36-02-PLAN.md — ECS compiler: replace MAIN_IMAGE_PLACEHOLDER, add KM_* env vars
- [ ] 36-03-PLAN.md — Build pipeline: Makefile targets + km init sandbox image push
- [ ] 36-04-PLAN.md — Smoke test: build image, run locally, verify entrypoint mechanics

### Phase 37: Docker Compose local substrate (connected mode)

**Goal:** `km create --substrate docker` provisions a local sandbox using Docker Compose with the same 5-container topology (main + 4 sidecars), connected to the existing AWS platform — SSM for secrets, SES for email, DynamoDB for budget tracking, S3 for artifacts/OTEL. Same enforcement as EC2, faster iteration (~5s up vs ~60s), runs on the operator's laptop
**Depends on:** Phase 36
**Plans:** 3/3 plans complete
**Requirements:** none (v2 feature, no formal requirement IDs)

Plans:
- [ ] 37-01-PLAN.md -- Schema validation + compiler: add docker substrate, compileDocker(), docker-compose.yml template
- [ ] 37-02-PLAN.md -- CLI create + destroy: runCreateDocker() with IAM SDK + docker compose up, runDestroyDocker()
- [ ] 37-03-PLAN.md -- CLI shell + stop + pause + roll: docker exec, docker compose stop/pause, roll skip

### Phase 38: EKS / Kubernetes substrate

**Goal:** `km create --substrate eks` provisions a sandbox as a Kubernetes Pod with sidecar containers, NetworkPolicy for egress enforcement, IRSA for IAM, and the same budget/proxy/audit topology — running on an existing EKS cluster
**Depends on:** Phase 36
**Plans:** 0/0

### Phase 39: Migrate sandbox metadata from S3 JSON to DynamoDB - km list, km status, km lock, km pause, and all metadata reads/writes switch to DynamoDB table while artifacts remain in S3

**Goal:** All sandbox metadata reads/writes (km list, km status, km lock/unlock, km pause/resume, km create/destroy, and Lambda handlers) switch from S3 JSON blobs to a DynamoDB km-sandboxes table with alias-index GSI, atomic lock/unlock via ConditionExpression, DynamoDB TTL for auto-cleanup, and backward-compat S3 fallback when table does not exist — artifacts remain in S3
**Requirements**: META-DYNAMO-01, META-DYNAMO-02, META-DYNAMO-03, META-DYNAMO-04, META-DYNAMO-05, META-DYNAMO-06, META-DYNAMO-IAM, META-DYNAMO-INFRA, META-DYNAMO-CONFIG, META-DYNAMO-SWITCHOVER-CLI, META-DYNAMO-SWITCHOVER-LAMBDA, META-DYNAMO-BACKWARD-COMPAT
**Depends on:** Phase 38
**Plans:** 3/3 plans complete

Plans:
- [ ] 39-01-PLAN.md — DynamoDB CRUD functions + tests (pkg/aws layer)
- [ ] 39-02-PLAN.md — Terraform module, IAM permissions, km init ordering, config field
- [ ] 39-03-PLAN.md — Switch all 22 CLI + Lambda call sites from S3 to DynamoDB

### Phase 40: eBPF cgroup network enforcement layer — kernel-level DNS/IP allowlisting as toggleable alternative to iptables DNAT, fixes root-in-sandbox bypass

**Goal:** Replace iptables DNAT + userspace proxy with eBPF cgroup programs for L3/L4 network enforcement. Sandboxed processes cannot bypass enforcement even with root — eBPF programs attached to cgroups require `CAP_BPF` in the host namespace to detach, which the sandbox never has. DNS queries are intercepted and resolved by a userspace daemon that populates BPF IP allowlist maps; only traffic requiring L7 inspection (GitHub per-repo filtering) is redirected to the existing MITM proxy. Profile schema gains `spec.network.enforcement` toggle (`ebpf | proxy | both`) so operators can choose per-profile. Programs are pinned to bpffs so enforcement survives CLI process exit.

**Architecture (modeled after [lawrencegripper/ebpf-cgroup-firewall](https://github.com/lawrencegripper/ebpf-cgroup-firewall)):**
```
Layer 1: cgroup/connect4 (BPF_PROG_TYPE_CGROUP_SOCK_ADDR, kernel 4.17+)
  → Intercepts ALL connect() from sandbox cgroup
  → Blocks disallowed IPs before SYN is sent (return 0 = EPERM)
  → Can rewrite dest IP/port (DNAT replacement, no iptables)
  → Redirects GitHub/Bedrock traffic to local MITM proxy for L7 inspection

Layer 2: cgroup/sendmsg4 (BPF_PROG_TYPE_CGROUP_SOCK_ADDR, kernel 4.18+)
  → Intercepts all UDP sendmsg (DNS queries on port 53)
  → Redirects DNS to userspace km-dns-resolver daemon

Layer 3: cgroup_skb/egress (BPF_PROG_TYPE_CGROUP_SKB)
  → Packet-level defense-in-depth IP allowlist
  → Catches raw socket traffic that bypasses connect() syscall
  → Catches hardcoded IPs that bypass DNS

Layer 4 (best-effort): TC egress classifier — TLS SNI check
  → Parses ClientHello SNI from cleartext TLS handshake
  → Validates hostname matches expected IP in BPF map
  → Caveat: Chrome ClientHellos >1500 bytes get TCP-segmented, SNI may be in 2nd segment
  → Defense-in-depth only, not primary enforcement
```

**Domain allowlist flow (Cilium FQDN model, [CiliumCon 2025 talk](https://tldrecap.tech/posts/2025/ciliumcon-na/cilium-ebpf-dns-parsing-fqdn-policies/)):**
1. cgroup/sendmsg4 redirects DNS queries to km-dns-resolver daemon
2. Daemon resolves domain, checks against profile allowlist (wildcard matching in Go: `strings.HasSuffix`)
3. If allowed, pushes resolved IPs into BPF `LPM_TRIE` map ([Cloudflare LPM deep dive](https://blog.cloudflare.com/a-deep-dive-into-bpf-lpm-trie-performance-and-optimization/))
4. cgroup/connect4 does O(prefix_len) IP lookup — nanoseconds, no userspace involved
5. Denied domains get NXDOMAIN; denied IPs get EPERM on connect()

**Implementation stack:**
- `cilium/ebpf` pure-Go library (no CGO) — [ebpf-go.dev](https://ebpf-go.dev/guides/getting-started/)
- `bpf2go` compiles BPF C → Go-embeddable bytecode at build time; only clang needed at build, nothing on target EC2
- CO-RE (Compile Once Run Everywhere) via vmlinux.h — no kernel headers needed on target
- BPF ring buffer (kernel 5.8+) for verdict/log events to userspace
- Pin programs + maps to `/sys/fs/bpf/km/{sandbox-id}/` for persistence across CLI exit
- AL2023 kernel 6.1 has everything needed: BTF, CO-RE, ring buffer, cgroup BPF, LPM trie
- No TCX on 6.1 (needs 6.6+) — use TC cls_bpf via netlink for Layer 4, or cgroup hooks for Layers 1-3

**Key reference projects:**
- [lawrencegripper/ebpf-cgroup-firewall](https://github.com/lawrencegripper/ebpf-cgroup-firewall) — Go+cilium/ebpf, DNS-based domain allowlist with cgroup/connect4 + cgroup_skb/egress, exactly the pattern we need
- [iximiuz — Transparent Egress Proxy with eBPF and Envoy](https://labs.iximiuz.com/tutorials/ebpf-envoy-egress-dc77ccd7) — cgroup/connect4 DNAT redirect to Envoy, Go attachment code
- [Cloudflare ebpf_connect4](https://github.com/cloudflare/cloudflare-blog/tree/master/2022-02-connectx/ebpf_connect4) — production cgroup/connect4 C code
- [cilium/ebpf examples](https://github.com/cilium/ebpf/tree/main/examples) — cgroup_skb, tcx, ringbuffer, uretprobe patterns in Go
- [nikofil — eBPF Firewall with cgroups](https://nfil.dev/coding/security/ebpf-firewall-with-cgroups/) — hash map IP blocklist with cgroup_skb ingress/egress
- [eBPF Docs — BPF_PROG_TYPE_CGROUP_SOCK_ADDR](https://docs.ebpf.io/linux/program-type/BPF_PROG_TYPE_CGROUP_SOCK_ADDR/) — connect4/sendmsg4 context fields and semantics

**Requirements:** EBPF-NET-01 through EBPF-NET-12
- EBPF-NET-01: `pkg/ebpf/` package scaffold with bpf2go pipeline — `go generate` compiles BPF C programs, bpf2go generates Go loader code, `make build` embeds compiled bytecode in km binary
- EBPF-NET-02: BPF cgroup/connect4 program intercepts all `connect()` syscalls from sandbox cgroup; looks up destination IP in `BPF_MAP_TYPE_LPM_TRIE` allowlist; returns 0 (EPERM) for disallowed IPs, returns 1 (allow) for allowed IPs
- EBPF-NET-03: BPF cgroup/connect4 program rewrites destination IP/port for connections needing L7 inspection (GitHub, Bedrock endpoints) — redirects to `127.0.0.1:{proxy_port}`, stores original dest in `BPF_MAP_TYPE_HASH` keyed by socket cookie (DNAT replacement without iptables)
- EBPF-NET-04: BPF cgroup/sendmsg4 program intercepts UDP port 53 DNS queries; redirects to km-dns-resolver daemon listening on localhost
- EBPF-NET-05: Userspace km-dns-resolver daemon receives redirected DNS queries, resolves domains, checks against profile allowlist (supports wildcards `*.github.com` via suffix matching), returns NXDOMAIN for denied domains, and pushes allowed resolved IPs into BPF LPM_TRIE map
- EBPF-NET-06: BPF cgroup_skb/egress program provides packet-level defense-in-depth — blocks packets to IPs not in the LPM_TRIE allowlist, catches raw socket traffic and hardcoded IPs that bypass the connect() hook
- EBPF-NET-07: BPF ring buffer (`BPF_MAP_TYPE_RINGBUF`) emits structured events to userspace for every deny action — event includes `{timestamp, pid, src_ip, dst_ip, dst_port, action, layer}` for audit logging
- EBPF-NET-08: All BPF programs and maps are pinned to `/sys/fs/bpf/km/{sandbox-id}/` — enforcement persists after `km create` exits; `km destroy` unpins and detaches; reattach on km process restart via `link.LoadPinnedLink()` and `ebpf.LoadPinnedMap()`
- EBPF-NET-09: Profile schema gains `spec.network.enforcement` field — `proxy` (current iptables DNAT behavior), `ebpf` (pure eBPF, no iptables), `both` (eBPF primary + proxy for L7); `km validate` accepts all three values; default is `proxy` for backwards compatibility
- EBPF-NET-10: TC egress classifier (best-effort, defense-in-depth) parses TLS ClientHello SNI from first TCP segment of port-443 connections; validates hostname against BPF hash map; drops packets with disallowed SNI; passes traffic where SNI is not in first segment (no TCP reassembly)
- EBPF-NET-11: Compiler (`pkg/compiler/`) emits eBPF enforcement setup in EC2 user-data when profile has `enforcement: ebpf | both` — starts km-dns-resolver daemon, attaches BPF programs to sandbox cgroup, populates initial allowlist from profile; for Docker substrate, attaches to container cgroup
- EBPF-NET-12: EC2 sandbox with root cannot bypass eBPF enforcement — verified by test: process with `CAP_NET_ADMIN` inside sandbox attempts `iptables -F` (succeeds but irrelevant) and direct connection to blocked IP (fails with EPERM from cgroup/connect4); process cannot call `bpf()` syscall to detach programs (no `CAP_BPF` in host namespace; seccomp blocks `bpf()` in sandbox)

**Depends on:** Phase 39
**Plans:** 7/7 plans complete

Plans:
- [ ] 40-01-PLAN.md — BPF C programs (connect4, sendmsg4, sockops, egress_skb), maps, bpf2go pipeline
- [ ] 40-02-PLAN.md — Go enforcer with cgroup management, BPF attachment, pin/unpin lifecycle
- [ ] 40-03-PLAN.md — Userspace DNS resolver daemon with domain allowlist and BPF map population
- [ ] 40-04-PLAN.md — TC egress classifier for TLS ClientHello SNI inspection (best-effort)
- [ ] 40-05-PLAN.md — Profile schema enforcement field and compiler user-data integration
- [ ] 40-06-PLAN.md — Ring buffer audit consumer, km ebpf-attach command, root bypass verification

### Phase 41: eBPF SSL uprobe observability layer — plaintext TLS capture via library-specific hooks, replaces MITM proxy for budget metering and traffic inspection

**Goal:** Attach eBPF uprobes to TLS library functions (`SSL_write`/`SSL_read` etc.) to capture plaintext HTTP request/response bodies without MITM proxy TLS termination or injected CA certificates. This enables budget metering (extract Bedrock/Anthropic token counts from response bodies), GitHub repo path inspection, and full traffic observability with <0.5% overhead. Each TLS library (OpenSSL, GnuTLS, NSS, Go crypto/tls, rustls) has different hook points and challenges. Library instrumentation is toggled per-profile via `spec.observability.tlsCapture`. The MITM proxy remains available as a fallback but is no longer required for metering or inspection when uprobes are active.

**Per-library hook points (from [BCC sslsniff](https://github.com/iovisor/bcc/blob/master/tools/sslsniff.py), [ecapture](https://github.com/gojue/ecapture), [Coroot](https://coroot.com/blog/instrumenting-rust-tls-with-ebpf)):**

| Library | Shared/Static | Write (uprobe entry) | Read (uprobe entry + uretprobe) | Used By |
|---------|--------------|---------------------|--------------------------------|---------|
| OpenSSL | Shared (`libssl.so`) | `SSL_write`, `SSL_write_ex` | `SSL_read`, `SSL_read_ex` | Python, Node.js, curl, most Linux tools |
| GnuTLS | Shared (`libgnutls.so`) | `gnutls_record_send` | `gnutls_record_recv` | curl (Debian default), wget |
| NSS/NSPR | Shared (`libnspr4.so`) | `PR_Write`, `PR_Send` | `PR_Read`, `PR_Recv` | Firefox, some curl builds |
| BoringSSL | Static (per-binary) | `SSL_write`, `SSL_write_ex` | `SSL_read`, `SSL_read_ex` | Chrome, gRPC, Envoy |
| Go crypto/tls | Static (per-binary) | `crypto/tls.(*Conn).Write` | `crypto/tls.(*Conn).Read` | Claude Code, Go CLI tools |
| rustls | Static (per-binary) | `Writer::write` (mangled) | `Reader::read` (mangled) | Rust CLI tools |

**How plaintext capture works ([eCapture architecture](https://fedepaol.github.io/blog/2023/10/13/ebpf-journey-by-examples-hijacking-ssl-with-ebpf-with-ecapture/)):**
1. Uprobe on `SSL_write` entry: `buf` (arg2) contains plaintext before encryption; `len` (arg3) gives size; read buf via `bpf_probe_read_user()`
2. Uprobe on `SSL_read` entry: stash buf pointer in `BPF_MAP_TYPE_PERCPU_ARRAY` keyed by `pid_tgid`
3. Uretprobe on `SSL_read` return: retrieve stashed pointer, read `PT_REGS_RC(ctx)` bytes of decrypted data
4. Extract fd from SSL struct: `ssl->wbio->num` (version-dependent offset); or use Pixie's offset-free approach — kprobe on `send`/`recv` syscalls on the call stack ([Pixie TLS tracing](https://blog.px.dev/ebpf-tls-tracing-past-present-future/))
5. Correlate fd → remote IP via `(pid, fd) → connection_info` BPF map populated by kprobe on `connect()`/`accept()`
6. Send `{timestamp, pid, fd, remote_ip, remote_port, direction, plaintext}` via ring buffer to userspace

**Go crypto/tls challenges ([Speedscale blog](https://speedscale.com/blog/ebpf-go-design-notes-1/), [Pixie blog](https://blog.px.dev/ebpf-function-tracing/)):**
- uretprobe is broken for Go — dynamic goroutine stack resizing invalidates return address
- Solution: disassemble function bytecode, find ALL `RET` instruction offsets via `golang.org/x/arch`, attach separate uprobe at each `RET` offset
- Go ABI changed in 1.17 — stack-based (ABI0) → register-based (ABIInternal); need version-specific parameter extraction macros
- Symbols present in unstripped Go binaries via `.gopclntab` section

**Rust/rustls challenges ([Coroot blog](https://coroot.com/blog/instrumenting-rust-tls-with-ebpf)):**
- rustls doesn't own the socket — works on buffers, app moves bytes over network
- Hook `Writer::write` (plaintext before encryption) and `Reader::read` (decrypted data)
- Find symbols via ELF metadata pattern matching for mangled Rust v0 names
- Read path is inverted: `recvfrom` happens first, then `conn.read_tls`, then `conn.process_new_packets`, then `reader.read` — store fd on `recvfrom`, retrieve when `reader.read` fires
- `Result<usize>` return uses separate registers (`rax` for status, `rdx` for byte count)

**Key reference projects:**
- [ecapture](https://github.com/gojue/ecapture) — **Primary reference.** Go CLI + cilium/ebpf, captures TLS plaintext via uprobes for OpenSSL/BoringSSL/GnuTLS/NSS/Go. 8 modules. Three output modes (text, keylog, pcapng). Auto-detects OpenSSL version from `.rodata` section. Kernel ≥4.18 (x86_64), ≥5.5 (aarch64).
- [Pixie openssl-tracer demo](https://github.com/pixie-io/pixie-demos/tree/main/openssl-tracer) — Standalone demo: 4 probes (entry+return for SSL_read and SSL_write), perf buffer events
- [Pixie TLS tracing: Past, Present, Future](https://blog.px.dev/ebpf-tls-tracing-past-present-future/) — Evolution from hardcoded offsets to offset-free call-stack method; 99.937% success rate
- [BCC sslsniff.py](https://github.com/iovisor/bcc/blob/master/tools/sslsniff.py) — Canonical multi-library SSL sniff with per-library toggle flags (`-o`/`-g`/`-n`), `--extra-lib` for custom paths
- [Coroot — Instrumenting Rust TLS with eBPF](https://coroot.com/blog/instrumenting-rust-tls-with-ebpf) — rustls Writer::write/Reader::read hooking, Rust symbol detection, inverted read path
- [Speedscale — eBPF Go Design Notes](https://speedscale.com/blog/ebpf-go-design-notes-1/) — Go crypto/tls multi-RET uprobe, ABI version handling
- [Kung Fu Dev — HTTPS Sniffer with Rust Aya](https://www.kungfudev.com/blog/2023/12/07/https-sniffer-with-rust-aya) — eBPF SSL capture implemented in Rust/Aya framework
- [eunomia tutorial — eBPF SSL/TLS capture](https://eunomia.dev/tutorials/30-sslsniff/) — Step-by-step sslsniff clone with `__ATTACH_UPROBE` macro pattern
- [cilium/ebpf uretprobe example](https://github.com/cilium/ebpf/blob/main/examples/uretprobe/main.go) — `link.OpenExecutable()` → `.Uprobe()` / `.Uretprobe()` Go API

**Requirements:** EBPF-TLS-01 through EBPF-TLS-14
- EBPF-TLS-01: `pkg/ebpf/tls/` package with per-library probe modules — each module discovers library path, resolves symbol offsets, attaches uprobes/uretprobes via `link.OpenExecutable()`, and reads plaintext via ring buffer
- EBPF-TLS-02: OpenSSL module hooks `SSL_write`/`SSL_write_ex` entry + `SSL_read`/`SSL_read_ex` entry+return on `libssl.so.3` (AL2023 default); auto-detects OpenSSL version from `.rodata` for struct offset selection (ecapture pattern); handles OpenSSL 1.1.x and 3.x
- EBPF-TLS-03: GnuTLS module hooks `gnutls_record_send` entry + `gnutls_record_recv` entry+return on `libgnutls.so`
- EBPF-TLS-04: NSS module hooks `PR_Write`/`PR_Send` entry + `PR_Read`/`PR_Recv` entry+return on `libnspr4.so`
- EBPF-TLS-05: Go crypto/tls module hooks `crypto/tls.(*Conn).Write` and `crypto/tls.(*Conn).Read` in target Go binaries — disassembles function to find all `RET` offsets (via `golang.org/x/arch` disassembler), attaches uprobe at each `RET` instead of uretprobe; detects Go ABI version (stack vs register) from binary metadata
- EBPF-TLS-06: rustls module hooks `Writer::write` entry + `Reader::read` entry+return in Rust binaries — discovers symbols via ELF section scan for `rustc` marker + `rustls` pattern matching on mangled v0 names; handles inverted read path (store fd from `recvfrom` kprobe, retrieve on `Reader::read`)
- EBPF-TLS-07: Connection correlation — kprobe on `connect()` and `accept()` populates `BPF_MAP_TYPE_HASH` mapping `(pid, fd) → {remote_ip, remote_port, local_port}`; SSL hook extracts fd from library struct (OpenSSL: `ssl->wbio->num`) or from the connection map; ring buffer events include remote endpoint for each captured payload
- EBPF-TLS-08: Ring buffer events carry structured data: `{timestamp_ns, pid, tid, fd, remote_ip, remote_port, direction (read/write), library_type, payload_len, payload[up to 16384 bytes]}` — 16KB aligned with TLS max fragment length
- EBPF-TLS-09: Userspace consumer in `pkg/ebpf/tls/` reads ring buffer, reassembles HTTP request/response pairs, and routes to registered handlers — budget metering handler extracts Bedrock/Anthropic token counts using existing `ExtractBedrockTokens()`/`ExtractAnthropicTokens()` functions from `sidecars/http-proxy/`
- EBPF-TLS-10: Budget metering via uprobes replaces MITM proxy metering when `tlsCapture` is enabled — captured response bodies from Bedrock (`bedrock-runtime.*.amazonaws.com`) and Anthropic (`api.anthropic.com`) calls are parsed for token usage and routed through existing `IncrementAISpend()` DynamoDB path
- EBPF-TLS-11: Profile schema gains `spec.observability.tlsCapture` field with sub-fields: `enabled` (bool), `libraries` (array of `openssl | gnutls | nss | go | rustls | all`, default `all`), `capturePayloads` (bool, default false — when false, only captures metadata for metering; when true, captures full plaintext for inspection/logging)
- EBPF-TLS-12: Library discovery at sandbox startup — scans `/proc/<pid>/maps` for loaded shared libraries matching known patterns (`libssl`, `libgnutls`, `libnspr4`); scans `/usr/bin/`, `/usr/local/bin/` for Go and Rust binaries via ELF header inspection; attaches probes to each discovered library/binary; logs which libraries were instrumented
- EBPF-TLS-13: Per-library toggle — BPF map `(cgroup_id, library_type) → enabled` checked at start of each uprobe handler; userspace can enable/disable specific library capture without detaching probes; `km status` shows which TLS libraries are being captured per sandbox
- EBPF-TLS-14: GitHub repo path extraction from captured HTTPS plaintext — when a sandbox connects to `github.com`/`api.github.com`, captured HTTP request paths are parsed to extract `owner/repo`; compared against profile `sourceAccess.github.allowedRepos`; violations logged to audit trail (enforcement still handled by GitHub App token scoping, uprobes provide observability and alerting)

**Performance:** <0.5% overhead with metadata-only capture (metering), 5-15% with full payload capture (inspection). Source: [OneUptime eBPF SSL inspection guide](https://oneuptime.com/blog/post/2026-01-07-ebpf-ssl-tls-inspection/view)

**Depends on:** Phase 40 (shares `pkg/ebpf/` scaffold, bpf2go pipeline, ring buffer patterns)
**Plans:** 5/5 plans complete

Plans:
- [ ] 41-01-PLAN.md — BPF C programs, shared headers, Go types, bpf2go pipeline for TLS uprobe + connection correlation
- [ ] 41-02-PLAN.md — OpenSSL uprobe attach module, library discovery, ring buffer consumer
- [ ] 41-03-PLAN.md — HTTP plaintext parser, GitHub repo path extractor, audit handler
- [ ] 41-04-PLAN.md — Profile schema tlsCapture field + deferred library schema entries
- [ ] 41-05-PLAN.md — km ebpf-attach --tls integration + compiler user-data wiring

### Phase 42: eBPF gatekeeper mode — connect4 DNAT rewrite for selective L7 proxy

**Goal:** eBPF runs in block mode as kernel-level gatekeeper in `both` enforcement mode — connect4 performs DNAT rewrite for L7-required hosts (GitHub, Bedrock) routing them to the proxy, while allowed non-L7 hosts connect directly; iptables DNAT and km-dns-proxy are removed for `both` mode
**Requirements**: EBPF-NET-03, EBPF-NET-09
**Depends on:** Phase 41
**Plans:** 3/3 plans complete

Plans:
- [ ] 42-01-PLAN.md — BPF dual-PID exemption + L7ProxyHosts derivation + enforcer wiring
- [ ] 42-02-PLAN.md — Userdata template gatekeeper flip for both enforcement mode + unit tests
- [ ] 42-03-PLAN.md — Build, deploy, and E2E verification of gatekeeper mode

### Phase 43: Regional EFS shared filesystem — cross-sandbox persistent storage via km init provisioning and profile-driven mount

**Goal:** `km init` provisions a Regional EFS filesystem with mount targets in each AZ, and sandboxes with `mountEFS: true` in their profile automatically mount the shared filesystem at a configurable path — enabling cross-sandbox artifact sharing without S3
**Requirements**: EFS-01, EFS-02, EFS-03, EFS-04, EFS-05, EFS-06
**Depends on:** Phase 33
**Plans:** 2/2 plans complete

Plans:
- [ ] 43-01-PLAN.md — EFS Terraform module, Terragrunt config, profile fields, km init registration
- [ ] 43-02-PLAN.md — LoadEFSOutputs, NetworkConfig wiring, userdata EFS mount block, destroy no-op verification

Key design decisions:
- `km init` creates the EFS filesystem (Regional, General Purpose, Elastic throughput, encrypted) and one mount target per AZ in the VPC
- EFS filesystem ID stored in km-config.yaml (or SSM) so `km create` can reference it
- Profile fields: `spec.runtime.mountEFS` (bool) and `spec.runtime.efsMountPoint` (string, default "/shared")
- Userdata installs `amazon-efs-utils`, mounts EFS with TLS + `_netdev,nofail` options
- Security group created during `km init` allowing NFS (port 2049) from sandbox instance SGs
- `km destroy` does NOT remove EFS — it persists across sandbox lifecycles
- Cross-AZ transfer cost ($0.01/GB/direction) accepted as trade-off for simplicity
- After E2E validation, wire `mountEFS: true` + `efsMountPoint: /shared` into goose, goose-ebpf, and goose-ebpf-gatekeeper profiles

Key design decisions:
- `km init` creates the EFS filesystem (Regional, General Purpose, Elastic throughput, encrypted) and one mount target per AZ in the VPC
- EFS filesystem ID stored in km-config.yaml (or SSM) so `km create` can reference it
- Profile fields: `spec.runtime.mountEFS` (bool) and `spec.runtime.efsMountPoint` (string, default "/shared")
- Userdata installs `amazon-efs-utils`, mounts EFS with TLS + `_netdev,nofail` options
- Security group created during `km init` allowing NFS (port 2049) from sandbox instance SGs
- `km destroy` does NOT remove EFS — it persists across sandbox lifecycles
- Cross-AZ transfer cost ($0.01/GB/direction) accepted as trade-off for simplicity

### Phase 44: km at/schedule — EventBridge Scheduler command for deferred and recurring sandbox operations

**Goal:** Operators can schedule any remote-capable sandbox command (create, destroy, stop, pause, resume, extend) for deferred or recurring execution via EventBridge Scheduler, using natural language time expressions or raw cron. Includes schedule listing and cancellation.
**Requirements**: [SCHED-PARSE, SCHED-STATE, SCHED-INFRA, SCHED-CMD, SCHED-LIST, SCHED-CANCEL, SCHED-GUARDRAIL]
**Depends on:** Phase 43
**Plans:** 4/4 plans complete

Plans:
- [ ] 44-01-PLAN.md — TDD: Natural language time parser (pkg/at/)
- [ ] 44-02-PLAN.md — SchedulerAPI extension + DynamoDB schedule CRUD + config
- [ ] 44-03-PLAN.md — km at CLI command with list/cancel subcommands and schedule alias
- [ ] 44-04-PLAN.md — E2E integration test for km at scheduling lifecycle

### Phase 45: km-send/km-recv sandbox scripts & km email send/read CLI

**Goal:** Close the gap between Phase 14's crypto library and in-sandbox usability. Deploy `km-send` and `km-recv` bash scripts into sandboxes (pure bash + AWS CLI + openssl, no km binary) that produce/consume signed MIME emails with attachments. Add operator-side `km email send` and `km email read` Go CLI commands for orchestrating inter-sandbox communication with authoritative Ed25519 verification and auto-decryption.
**Requirements**:
- In-sandbox `km-send` script: reads Ed25519 privkey from SSM, signs body with openssl, builds raw multipart/mixed MIME with X-KM-Signature/X-KM-Sender-ID headers, supports --body file/stdin and --attach file1,file2,..., sends via aws sesv2 send-email Content.Raw
- In-sandbox `km-recv` script: reads /var/mail/km/new/*, parses MIME headers, best-effort signature verification via DynamoDB lookup + openssl, --json for agent consumption, --watch for polling, moves processed to /var/mail/km/processed/
- `km email send --from <sandbox-id> --to <sandbox-id> --subject <subject> --body <file> [--attach f1,f2,...]`: operator-side Go command using pkg/aws/identity.go SendSignedEmail (extended for multipart MIME + attachments)
- `km email read <sandbox-id> [--json] [--raw]`: operator-side Go command, authoritative Ed25519 verification, auto-decrypt when X-KM-Encrypted present, extract attachments from multipart MIME
- Extend buildRawMIME and ParseSignedMessage for multipart/mixed with attachments
- Deploy km-send and km-recv via userdata.go alongside km-mail-poller
- Shared MIME contract: X-KM-Sender-ID, X-KM-Signature (body-only), X-KM-Encrypted headers; multipart/mixed for attachments
- No encryption in bash scripts (Go CLI only); no km binary in sandbox
**Depends on:** Phase 14 (identity/signing library), Phase 17 (mailbox/allow-lists)
**Plans:** 4/4 plans complete

#### Wave 1 — Foundation
- [ ] 45-01-PLAN.md — Multipart MIME support in pkg/aws (extend buildRawMIME + ParseSignedMessage for attachments)

#### Wave 2 — In-sandbox scripts (parallel)
- [ ] 45-02-PLAN.md — km-send bash script (Ed25519 signing via openssl, multipart MIME, SES send)
- [ ] 45-03-PLAN.md — km-recv bash script (mailbox reader, best-effort verification, --json/--watch)

#### Wave 3 — Operator CLI
- [ ] 45-04-PLAN.md — km email send/read Go CLI commands (authoritative verification, auto-decrypt, attachments)

Plans:
- [x] TBD (run /gsd:plan-phase 45 to break down) (completed 2026-04-03)

### Phase 46: AI email-to-command — Haiku interprets free-form operator emails into km commands

**Goal:** Replace the rigid keyword-matching email-create-handler with a conversational AI-powered flow. Operator sends free-form email to operator@sandboxes.{domain} describing what they want. Lambda calls Haiku to interpret the intent, resolve it to a km command with profile selection and overrides, and replies with a structured confirmation template. Operator replies "yes" to execute, or describes changes for another round. Safe phrase auth is preserved.
**Requirements**:
- Extend email-create-handler Lambda to call Bedrock Haiku for intent extraction from free-form email body
- Haiku receives context: available profiles (names + descriptions), available commands (create, destroy, status, extend, pause, resume), current sandboxes
- Haiku extracts: command, profile, overrides (TTL, repos, instance type, etc.), confidence level
- Lambda replies with structured confirmation template showing resolved command + parameters
- Operator replies "yes" → Lambda triggers command via EventBridge (existing remote-create pattern)
- Operator replies with changes → another Haiku round → updated confirmation
- Preserve KM-AUTH safe phrase validation (existing)
- Low-confidence extractions trigger clarifying questions instead of confirmation
- Conversation state tracked per thread (Message-ID / In-Reply-To chain in S3)
- Existing keyword-based handlers (create with YAML attachment, status) continue to work as fast-path
**Depends on:** Phase 22 (remote create via EventBridge), Phase 45 (email infrastructure)
**Plans:** 2/2 plans complete
Plans:
- [ ] 46-01-PLAN.md — Haiku AI invocation layer + conversation state management
- [ ] 46-02-PLAN.md — Wire AI dispatch into handler + Terraform Bedrock IAM/timeout

### Phase 47: Privileged execution mode & learn profile — spec.execution.privileged + profiles/learn.yaml

**Goal:** Operators can create a wide-open sandbox for traffic observation using `km create profiles/learn.yaml`, run their workload with full sudo/root access, then `km shell --learn` to generate a minimal SandboxProfile from observed network traffic. The `privileged` field is general-purpose and available to any profile.
**Requirements**:
- Add `Privileged bool` field to `ExecutionSpec` in `pkg/profile/types.go` (`yaml:"privileged,omitempty"`)
- Add `Privileged bool` to `userDataParams` in `pkg/compiler/userdata.go`
- Wire `Privileged` from profile into userdata params in `generateUserData()`
- Update userdata template: when Privileged is true, add sandbox user to wheel group and write passwordless sudoers entry
- Create `profiles/learn.yaml` — permissive profile with wide-open DNS suffixes covering common TLDs, `enforcement: both` for eBPF observe + L7 capture, `privileged: true`, higher budget limits, short TTL safety net
- Add tests for privileged userdata generation (wheel group + sudoers entry present/absent)
**Depends on:** Phase 31 (allowlist profile generator / --learn), Phase 42 (eBPF gatekeeper mode)
**Plans:** 1/1 plans complete
Plans:
- [ ] 47-01-PLAN.md — Privileged execution field + learn profile + tests

### Phase 48: Profile override flags for km create — targeted budget flags and generic --set

**Goal:** Add --ttl and --idle flags to km create for command-line lifecycle overrides. --ttl 0 disables auto-destroy and enables an indefinite hibernate-on-idle loop.
**Requirements**:
- `--ttl <duration>` flag on `km create` to override `spec.lifecycle.ttl` from the command line (e.g. `km create profile.yaml --ttl 3h`)
- `--idle <duration>` flag on `km create` to override idle timeout before auto-hibernate/stop/destroy kicks in (e.g. `km create profile.yaml --idle 30m`)
- TBD (additional budget/override flags)
**Depends on:** Phase 46
**Plans:** 2/2 plans complete

Plans:
- [ ] 48-01-PLAN.md — CLI flags, profile mutation, TTL=0 schedule guard, S3 upload fix
- [ ] 48-02-PLAN.md — Compiler IdleAction param + sidecar hibernate loop

### Phase 49: Prebaked AMI support — custom images with preinstalled toolchains for fast sandbox boot

**Goal:** [To be planned]
**Requirements**: TBD
**Depends on:** Phase 48
**Plans:** 2 plans
Plans:
- [ ] 52-01-PLAN.md — Add cloned_from field to DynamoDB metadata and km list --wide
- [ ] 52-02-PLAN.md — Implement km clone command with workspace staging

Plans:
- [ ] TBD (run /gsd:plan-phase 49 to break down)

### Phase 50: km agent non-interactive execution — fire prompts into sandboxes via SSM, capture JSON output on disk, fetch results with km agent results

**Goal:** Extend `km agent` with `--prompt` flag for fire-and-forget non-interactive Claude execution inside sandboxes. SSM send-command runs `claude --json --dangerously-skip-permissions -p "..."`, output lands at `/workspace/.km-agent/runs/<timestamp>/output.json`. Add `km agent results` to fetch run output, `km agent list` to list runs. Background idle-reset keeps sandbox alive while agent is running.
**Requirements:** [AGENT-01, AGENT-02, AGENT-03, AGENT-04, AGENT-05, AGENT-06, AGENT-07, AGENT-08]
**Depends on:** None (extends existing km agent from Phase 34)
**Plans:** 2/2 plans complete

Plans:
- [ ] 50-01-PLAN.md -- Cobra restructure, --prompt flag, non-interactive fire-and-forget via SSM SendCommand, idle-reset heartbeat
- [ ] 50-02-PLAN.md -- km agent results and km agent list subcommands

### Phase 51: km agent tmux sessions — run agents inside persistent tmux sessions for live attach, survive disconnects, and interactive mode

**Goal:** Wrap all non-interactive agent execution in persistent tmux sessions on the sandbox. SSM SendCommand creates a named tmux session (`km-agent-<runID>`) that runs Claude, so operators can `km agent attach <sandbox>` to watch live, survive SSM disconnects, and scroll back through output. Add `--interactive` flag that opens an SSM session attached directly to the tmux pane. Add `km agent attach` subcommand for connecting to running/completed agent sessions.
**Requirements**: [TMUX-01, TMUX-02, TMUX-03, TMUX-04, TMUX-05]
**Depends on:** Phase 50
**Plans:** 2/2 plans complete

Plans:
- [ ] 51-01-PLAN.md -- tmux-wrap BuildAgentShellCommands, deterministic RUN_ID, wait-for channel
- [ ] 51-02-PLAN.md -- km agent attach subcommand, --interactive flag on km agent run

### Phase 52: km clone — duplicate a running sandbox with workspace copy

**Goal:** Add `km clone <source> [new-alias]` command that creates a new sandbox from an existing one's profile, copies /workspace and rsyncFileList paths via SSM+rsync through S3 staging, and provisions a fully independent identity (new sandbox ID, email, keys, budget, TTL). Supports `--no-copy` for fresh-from-same-profile, `--count N` for multi-clone with auto-suffixed aliases, and `--alias` flag as alternative to positional alias. Source must be running; live copy (no freeze). Clone metadata includes `cloned_from` field for lineage tracking.
**Requirements**: [CLONE-01, CLONE-02, CLONE-03, CLONE-04, CLONE-05]
**Depends on:** Phase 2 (core provisioning)
**Plans:** 2/2 plans complete
Plans:
- [ ] 52-01-PLAN.md — Add cloned_from field to DynamoDB metadata and km list --wide
- [ ] 52-02-PLAN.md — Implement km clone command with workspace staging

### Phase 53: Persistent local sandbox numbering — monotonic counter assigned at create time, stored locally, replaces ephemeral positional numbers in km list

**Goal:** Replace ephemeral positional numbering in km list with persistent local numbers assigned at create time, stored in ~/.config/km/local-numbers.json
**Requirements**: LOCAL-01, LOCAL-02, LOCAL-03, LOCAL-04, LOCAL-05, LOCAL-06, LOCAL-07, LOCAL-08
**Depends on:** Phase 52
**Plans:** 2/2 plans complete

Plans:
- [ ] 53-01-PLAN.md — Create pkg/localnumber package with TDD (State, Load, Save, Assign, Remove, Resolve, Reconcile)
- [ ] 53-02-PLAN.md — Wire local numbers into create, list, sandbox_ref, and destroy commands

### Phase 54: Multi-account GitHub App installations — support multiple GitHub users installing the same App

**Goal:** Allow a single GitHub App to be installed on multiple GitHub accounts/orgs simultaneously. Currently only one installation ID is stored globally at `/km/config/github/installation-id`; a second user installing the App overwrites the first. Change storage to per-account installation IDs, auto-resolve the correct installation at sandbox create time based on repo owner, and update discover/setup flows to store all installations.
**Requirements**: GHMI-01, GHMI-02, GHMI-03, GHMI-04, GHMI-05
**Depends on:** Phase 13 (GitHub App token integration)
**Plans:** 3/3 plans complete

Plans:
- [ ] 54-01-PLAN.md — Migrate SSM storage from single installation-id to per-account `/km/config/github/installations/{account}` keys; update configure_github.go setup/discover/manual flows to store all installations; backward-compat read of legacy key
- [ ] 54-02-PLAN.md — Update create.go `generateAndStoreGitHubToken` to resolve installation ID from repo owner; extract owner from `sourceAccess.github.repos` entries; fall back to legacy single ID if per-account not found
- [ ] 54-03-PLAN.md — Update doctor.go health check, token-refresher Lambda event to carry per-sandbox installation ID, and add tests for multi-installation flows








### Phase 55: Learn mode command capture — record shell commands typed by root and sandbox user during km shell --learn sessions, include in observed state and generated profile output

**Goal:** Extend learn mode to capture shell commands executed by both root and sandbox users during `km shell --learn` sessions. Commands are included in the observed state JSON alongside DNS/hosts/repos, and appear in the generated profile output as `spec.execution.initCommands` suggestions. Captures via bash PROMPT_COMMAND history logging or eBPF exec tracing, covering both interactive and scripted commands.
**Requirements**: TBD
**Depends on:** Phase 31 (allowlist profile generator / --learn), Phase 47 (privileged execution + learn profile)
**Plans:** 0 plans

Plans:
- [ ] TBD (run /gsd:plan-phase 55 to break down)
