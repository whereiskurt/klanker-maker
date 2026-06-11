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

### Phase 24 — Documentation Refresh (Phases 18-32) — superseded; folded into ongoing docs work, no standalone phase directory

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
**Requirements**: LEARN-CMD-01, LEARN-CMD-02, LEARN-CMD-03, LEARN-CMD-04, LEARN-CMD-05, LEARN-CMD-06, LEARN-CMD-07
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

### Phase 33.1: Raw AMI ID support — extend schema, compiler, and Terraform to accept ami-xxxxxxxx IDs alongside slugs (prereq for Phase 56 snapshot lifecycle) (INSERTED)

**Goal:** Raw `ami-xxxxxxxx` IDs flow through profile YAML → JSON schema → Go compiler → HCL template → Terraform module → `aws_instance.ami` argument without regressing the existing slug behavior, unblocking Phase 56 snapshot lifecycle workflows.
**Requirements**: P33.1-01, P33.1-02, P33.1-03, P33.1-04, P33.1-05, P33.1-06, P33.1-07
**Depends on:** Phase 33
**Plans:** 2/2 plans complete

Plans:
- [ ] 33.1-01-PLAN.md — Open ami JSON schema to oneOf(slug enum, raw-ID pattern); add isRawAMIID() helper and Wave 0 failing tests
- [ ] 33.1-02-PLAN.md — Wire AMIID through ec2HCLParams + template, add var.ami_id and effective_ami_id to Terraform module, rewire instance resources

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
**Requirements**: LEARN-CMD-01, LEARN-CMD-02, LEARN-CMD-03, LEARN-CMD-04, LEARN-CMD-05, LEARN-CMD-06, LEARN-CMD-07
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
**Requirements**: LEARN-CMD-01, LEARN-CMD-02, LEARN-CMD-03, LEARN-CMD-04, LEARN-CMD-05, LEARN-CMD-06, LEARN-CMD-07
**Depends on:** Phase 31 (allowlist profile generator / --learn), Phase 47 (privileged execution + learn profile)
**Plans:** 2/2 plans complete

Plans:
- [ ] 55-01-PLAN.md — Extend allowlistgen Recorder and Generator for command capture
- [ ] 55-02-PLAN.md — Wire command capture into EC2 and Docker learn mode integration

### Phase 56: Learn mode AMI snapshot and lifecycle management

**Goal:** Add `--ami` flag to `km shell --learn` that snapshots the EC2 instance as a custom AMI on exit. The AMI ID is written into the generated profile YAML at `spec.runtime.ami`, so future sandboxes boot from the pre-configured image with all packages/tools pre-installed. AMIs are tagged with sandbox metadata (sandbox-id, profile, alias, date). Add `km ami list` to show custom AMIs with age/size/usage and `km ami delete` for cleanup. Extend `km doctor` with a stale/unused AMI check that flags AMIs older than a configurable threshold or not referenced by any profile. The `initCommands` captured by Phase 55 serve as documentation of what's baked into the AMI and as a fallback for AMI-less regions.
**Requirements:** [P56-01, P56-02, P56-03, P56-04, P56-05, P56-06, P56-07, P56-08, P56-09, P56-10, P56-11, P56-12]
**Depends on:** Phase 55 (learn mode command capture), Phase 33 (AMI resolution), Phase 33.1 (raw AMI ID schema)
**Plans:** 6/6 plans complete

Plans:
- [ ] 56-01-PLAN.md — AWS SDK helpers (pkg/aws/ec2_ami.go) + mocks/tests for BakeAMI/DeleteAMI/CopyAMI/ListBakedAMIs
- [ ] 56-02-PLAN.md — Config schema: Doctor.StaleAMIDays + km-config.yaml key (default 30)
- [ ] 56-03-PLAN.md — SCP/IAM: add ec2:DeregisterImage / ec2:DeleteSnapshot / ec2:CreateTags to bootstrap.go DenyInfraAndStorage exemption
- [ ] 56-04-PLAN.md — km ami Cobra subcommand tree (list/delete/bake/copy) + profile refcount scanner + BakeFromSandbox helper
- [ ] 56-05-PLAN.md — km shell --learn --ami integration + Recorder.RecordAMI + generator emits spec.runtime.ami
- [ ] 56-06-PLAN.md — km doctor checkStaleAMIs + DoctorDeps.EC2AMIClients + --all-regions fan-out

### Phase 56.2: Resume hardening — km-bootstrap.service for cgroup + /run/km recreation on every boot, harden cgroup.procs redirect writes (lessons from Phase 56.1 km resume e2e) (INSERTED)

**Goal:** Eliminate two errors observed on shell entry after `km resume` of a stopped (or baked-then-resumed) sandbox: (1) `bash: /sys/fs/cgroup/km.slice/km-{sandbox-id}.scope/cgroup.procs: No such file or directory` from `/etc/profile.d/km-cgroup.sh` and the `km-sandbox-shell` wrapper — `/sys/fs/cgroup` is a kernel-managed pseudo-fs that's empty after EC2 stop+start, the scope created during cloud-init at `pkg/compiler/userdata.go:1369` is gone, cloud-init does not re-run on resume, and the existing `2>/dev/null || true` does not catch the error because bash opens the redirect target before applying stderr suppression — net effect: shell is not placed in the eBPF cgroup, enforcement may be bypassed; (2) `bash: /run/km/learn-commands.log: No such file or directory` from the learn-mode PROMPT_COMMAND append at `userdata.go:498` — `/run` is tmpfs and gets recreated empty at every boot, so the file `touch`ed once at `userdata.go:426` disappears on resume. Approach: add `km-bootstrap.service` (Type=oneshot, RemainAfterExit=yes, Before=amazon-ssm-agent.service, Wants=km.slice) emitted by the compiler and packaged into the userdata systemd-unit drop, running a small bash script that (a) sources `/etc/profile.d/km-identity.sh` so SANDBOX_ID is correct on baked-AMI relaunch, (b) `mkdir -p /sys/fs/cgroup/km.slice/km-${SANDBOX_ID}.scope` and chowns `cgroup.procs`, (c) `mkdir -p /run/km` and `touch` the well-known sentinel files (`learn-commands.log`, `audit-pipe` placeholder) with the right ownership/permissions; harden the existing `echo $$ > cgroup.procs` writes in `km-cgroup.sh` and `km-sandbox-shell` to use `{ echo $$ > path; } 2>/dev/null || true` so redirect-open errors are actually suppressed.
**Requirements**: P56.2-01, P56.2-02, P56.2-03, P56.2-04, P56.2-05, P56.2-06, P56.2-07
**Depends on:** Phase 56.1
**Plans:** 1/1 plans complete

Plans:
- [ ] 56.2-01-PLAN.md — Emit km-bootstrap.service + /usr/local/bin/km-bootstrap script + systemctl enable; harden cgroup.procs writes with compound-command form (TDD)

### Phase 56.1: Bake-loop hardening — fix additionalVolume/AMI BDM collision, non-blocking audit hook, sidecar FIFO-retry + post-env-rewrite restart (lessons from Phase 56 e2e) (INSERTED)

**Goal:** Fix four runtime bugs in the bake-loop discovered in Phase 56 e2e: (1) hardcoded /dev/sdf in ec2spot Terraform module collides with baked-AMI BDMs that already declare it — compiler now detects and picks /dev/sdg…/dev/sdp; (2) bare > redirect in _km_audit PROMPT_COMMAND deadlocks login shells when the FIFO has no reader — replace with timeout 0.1 tee; (3) km-audit-log sidecar opens FIFO once and falls through to empty stdin on race — add 10-attempt retry with backoff that re-creates /run/km/ + the FIFO; (4) sidecars on a baked AMI inherit the bake-source's SANDBOX_ID — add systemctl restart of km-* units after env-rewrite + sidecar download.
**Requirements**: P56.1-01, P56.1-02, P56.1-03, P56.1-04, P56.1-05, P56.1-06
**Depends on:** Phase 56
**Plans:** 2/2 plans complete

Plans:
- [ ] 56.1-01-PLAN.md — BDM collision fix: AMIBDMDeviceNames helper + pickAdditionalVolumeDevice + thread device name through Compile + parameterize ec2spot Terraform module
- [ ] 56.1-02-PLAN.md — Non-blocking audit hook + sidecar FIFO retry + post-env-rewrite systemctl restart of km-* sidecars

### Phase 57: Email enhancement — km-send --no-sign for external recipients, km-recv multipart/RFC5322 fixes, safe phrase validation on inbound, marketplace plugin email docs

**Goal:** Fix km-send and km-recv bash scripts to handle external (non-sandbox) email. km-send gets `--no-sign` flag that skips Ed25519 SSM key fetch and X-KM-* headers, enabling plain email to Gmail/external addresses. km-recv gets RFC 5322 folded header parsing, multipart/alternative body extraction (Gmail HTML emails), and `--from-external` display hint. Inbound external emails must contain the configured safe phrase (from km configure) to be accepted — the SES receipt rule Lambda validates this before delivery to the sandbox mailbox. Update the marketplace plugin/skill docs to document /opt/km/bin/km-send and km-recv paths, external email workflow, and safe phrase requirements.
**Requirements**: TBD
**Depends on:** Phase 45 (km-send/km-recv scripts), Phase 46 (AI email-to-command / safe phrase)
**Plans:** 5/5 plans complete

Plans:
- [ ] 57-00-PLAN.md — Wave 0 test scaffolding (RED test stubs + MIME fixtures for Plans 01-03)
- [ ] 57-01-PLAN.md — km-send --no-sign flag (skip SSM/openssl, omit X-KM-* headers, keep KM-AUTH)
- [ ] 57-02-PLAN.md — km-recv RFC 5322 unfold + multipart/alternative + nested + [EXTERNAL] hint
- [ ] 57-03-PLAN.md — km-mail-poller safe-phrase validation for external inbound (fail-open SSM)
- [ ] 57-04-PLAN.md — Marketplace plugin docs: skills/email + skills/sandbox SKILL.md updates

### Phase 58: km agent run codex support with --claude/--codex flags, codexArgs profile field, and claude-only --no-bedrock gating

**Goal:** `km agent run <sb> --prompt "..."` gains `--claude` (default) and `--codex` mutually-exclusive flags. Refactor `BuildAgentShellCommands` to branch on agent type and emit `codex exec --json --dangerously-bypass-approvals-and-sandbox "$PROMPT"` for codex. Anchor the `--no-bedrock` env-unset + OAuth extraction stanza inside the claude-only branch. Add `spec.cli.codexArgs []string` to the profile schema (parallel to `claudeArgs`). Hard-error on `--codex --no-bedrock` before any SSM call. Pass-through output — codex JSONL lands in `output.json` unchanged.
**Requirements:** CODEX-01, CODEX-02, CODEX-03, CODEX-04, CODEX-05
**Depends on:** Phase 57
**Plans:** 3/3 plans complete

Plans:
- [ ] 58-01-PLAN.md — Add spec.cli.codexArgs to CLISpec + JSON Schema (TDD)
- [ ] 58-02-PLAN.md — Refactor BuildAgentShellCommands to branch on agent type (claude|codex)
- [ ] 58-03-PLAN.md — Wire --claude/--codex flags, mutex + no-bedrock gating, profile codexArgs loader

### Phase 59: Email sender allowlist for operator inbox and sandbox inbound

**Goal:** Add email sender allowlist enforcement at operator inbox (Lambda) and sandbox inbound (km-recv/km-mail-poller) levels
**Requirements**: [AL-01, AL-02, AL-03, AL-04, AL-05, AL-06, AL-07, AL-08]
**Depends on:** Phase 58
**Plans:** 2/2 plans complete

Plans:
- [x] 59-01-PLAN.md — Extend MatchesAllowList with email patterns + config struct
- [x] 59-02-PLAN.md — Lambda operator allowlist + bash sandbox sender filtering








### Phase 60: Budget compute accounting excludes paused/hibernated intervals — track pausedAt/resumedAt transitions, accumulate paused seconds in budget row, subtract from elapsed time in calculateComputeCost so hibernated EC2 stops accruing compute spend

**Goal:** Paused/hibernated EC2 sandboxes stop accruing compute budget — `calculateComputeCost` subtracts accumulated `pausedSeconds` (closed intervals) plus any open interval (`now - pausedAt`) from elapsed time before multiplying by spot rate, while preserving the existing SET-based idempotent spend recompute. Every pause/resume transition (km pause/resume, km at scheduled pause/resume, ttl-handler idle-hibernate, budget-enforcer exhaustion, km budget add auto-resume, agent-run auto-start) writes `pausedAt`/`pausedSeconds` on the `BUDGET#compute` DynamoDB row.
**Requirements**: BUDG-PAUSE-01, BUDG-PAUSE-02, BUDG-PAUSE-03 (phase-scoped; fixes bug in BUDG-03 accounting)
**Depends on:** Phase 59
**Plans:** 3/3 plans complete

Plans:
- [ ] 60-01-PLAN.md — pkg/aws/budget.go foundation: BudgetSummary.PausedSeconds/PausedAt, RecordPauseStart, RecordResumeClose, GetBudget extension, unit tests
- [ ] 60-02-PLAN.md — Wire pause/resume hooks at external call sites (km pause, km resume, km budget add auto-resume, ttl-handler handleStop/handleResume/handleAgentRun)
- [ ] 60-03-PLAN.md — budget-enforcer cost calculation: calculateComputeCost accepts pausedSeconds, HandleBudgetCheck threads effective pausedSeconds (closed + open interval), enforceBudgetCompute records pausedAt after StopInstances

**Follow-up — 60.1 (2026-05-31):** Producer-side gap closure. `RecordPauseStart` was previously gated on `hibernate==true` in `ttl-handler/handleStop` (so TTL-fired stops on non-hibernating profiles — the default — kept ticking wall-clock spend) and was missing entirely from `km stop`. Both paths now write `pausedAt` unconditionally on successful StopInstances, matching the cost-accounting invariant (AWS charges $0 compute for stopped EC2 either way). Resume side already correct (unconditional, no-op when no open interval). See [60-60.1-FOLLOWUP.md](phases/60-budget-compute-accounting-excludes-paused-hibernated-intervals-track-pausedat-resumedat-transitions-accumulate-paused-seconds-in-budget-row-subtract-from-elapsed-time-in-calculatecomputecost-so-hibernated-ec2-stops-accruing-compute-spend/60-60.1-FOLLOWUP.md). Requires `km init --dry-run=false` to deploy the ttl-handler Lambda zip.

### Phase 61: km shell Ctrl+C fix: switch interactive SSM sessions from AWS-StartInteractiveCommand to a parameterized Standard_Stream document with runAsDefaultUser=sandbox

**Goal:** Eliminate the Ctrl+C teardown bug in `km shell` (non-root) and all three `km agent` interactive subcommands by replacing `AWS-StartInteractiveCommand` (sessionType `InteractiveCommands`, terminates on Ctrl+C) with a custom `KM-Sandbox-Session` regional document (sessionType `Standard_Stream`, `runAsDefaultUser: sandbox`) that forwards Ctrl+C as a PTY byte (SSH-like). Drop the redundant `sudo -u sandbox -i` wrapper from each callsite. Build `--parameters` JSON via `encoding/json.Marshal`. Surface a fail-fast actionable error when the doc is missing in the target region. Leave the root-shell path unchanged.
**Requirements**: Functional correctness fix; touches CONF-05 implicitly (no explicit requirement IDs in REQUIREMENTS.md)
**Depends on:** Phase 60
**Plans:** 3/3 plans complete

Plans:
- [ ] 61-01-PLAN.md — Create regional `infra/modules/ssm-session-doc/v1.0.0/` Terraform module + Terragrunt live wiring + plug into `regionalModules()` + update init_test.go counts and add TestRegionalModulesIncludesSSMDoc
- [ ] 61-02-PLAN.md — Switch all four CLI callsites (shell.go non-root, agent.go --claude / attach / run --interactive) to KM-Sandbox-Session via `encoding/json.Marshal`, drop sudo wrappers, add fail-fast on missing doc, update tests + add TestShellCmd_EC2_Root, TestShellCmd_MissingSSMDoc, TestAgentParametersEscaping
- [ ] 61-03-PLAN.md — Manual UAT (7 scenarios) on a live sandbox: Ctrl+C forwarding for all four affected callsites, root-path regression guard, missing-doc fail-fast verification, signed-off UAT outcome table

### Phase 62: Claude Code operator-notify hook for permission and idle events

**Goal:** Claude Code agents running on km sandboxes emit signed emails to the operator (or a profile-specified override address) when they need permission to use a tool (`Notification` hook event) or finish a turn and are waiting for further input (`Stop` hook event). Behavior is controlled by four new `spec.cli` profile fields (`notifyOnPermission`, `notifyOnIdle`, `notifyCooldownSeconds`, `notificationEmailAddress`); `km shell` and `km agent run` gain `--notify-on-permission`/`--notify-on-idle` (and `--no-*`) flags that override per invocation. The hook script is wired into `~/.claude/settings.json` at compile time; profile fields write env-var defaults to `/etc/profile.d/km-notify-env.sh` (codebase convention; spec said `/etc/environment` but research overrode); CLI flags inject overrides into the SSM-launched Claude process. v1 is one-way notification only; v2 closed-loop (operator emails back, agent resumes) is out of scope here but the design (subject prefix `[<sandbox-id>] <event>`, single recipient field) is forward-compatible.
**Spec:** `docs/superpowers/specs/2026-04-26-operator-notify-hook-design.md`
**Requirements:** [HOOK-01, HOOK-02, HOOK-03, HOOK-04, HOOK-05]
**Depends on:** Phase 45 (km-send/km-recv), Phase 50/51 (km agent run + tmux). All upstream deps already complete.
**Plans:** 5/5 plans complete

Plans:
- [x] 62-01-PLAN.md — Profile schema additions (`spec.cli.notify*`) + REQUIREMENTS.md HOOK-01..HOOK-05 registration
- [x] 62-02-PLAN.md — Compiler: inline km-notify-hook script via heredoc in `userdata.go`, write `/etc/profile.d/km-notify-env.sh` from profile fields, Go-side merge of settings.json hook entries via `encoding/json`
- [x] 62-03-PLAN.md — Hook script behavior tests: extract heredoc body, exec via bash with synthetic env + stdin payloads + stub km-send, cover gate / cooldown / Notification / Stop / send-failure invariants
- [x] 62-04-PLAN.md — CLI flag wiring: `km shell` (pre-session SendCommand writes `/etc/profile.d/zz-km-notify.sh`) + `km agent run` (extend `AgentRunOptions` with `*bool` notify fields, prepend `notifyEnvLines` to `BuildAgentShellCommands` script)
- [x] 62-05-PLAN.md — Manual UAT: provision live sandbox, exercise both event paths, confirm signed emails arrive at operator + override addresses, verify cooldown + CLI override behavior end-to-end


### Phase 63: Slack-notify hook for Claude Code permission and idle events

**Goal:** Claude Code agents on km sandboxes deliver hook events (Notification for tool-permission prompts, Stop for idle/turn-complete) to a klankermaker.ai-owned Slack workspace in parallel with Phase 62 email delivery. Bot token never leaves AWS — sandboxes call a new `km-slack-bridge` Lambda Function URL with Ed25519-signed payloads (same trust model as Phase 45 km-send). Operators are invited to channels via Slack Connect from their separate workspace. Three channel modes (shared default `#km-notifications`, per-sandbox `#sb-{id}` opt-in, operator-pinned override). New `notifyEmailEnabled` *bool field gives Phase 62 backward compat plus the option to switch fully to Slack-only delivery. ValidationError gains `IsWarning` so non-blocking validation rules (no-op combinations) don't fail `km validate`.
**Spec:** `docs/superpowers/specs/2026-04-29-slack-notify-hook-design.md`
**Requirements**: SLCK-01, SLCK-02, SLCK-03, SLCK-04, SLCK-05, SLCK-06, SLCK-07, SLCK-08, SLCK-09, SLCK-10
**Depends on:** Phase 62 (operator-notify hook), Phase 45 (km-send signed email + operator identity), Phase 14 (Ed25519 sandbox identity), Phase 39 (DynamoDB sandbox metadata), Phase 23 (credential rotation), Phase 27 (OTEL integration). All upstream deps complete.
**Plans:** 10/10 plans complete

Plans:
- [ ] 63-01-PLAN.md — Profile schema (5 new spec.cli fields, *bool semantics) + ValidationError.IsWarning + 5 semantic validation rules + REQUIREMENTS.md SLCK-01..SLCK-10 registration
- [ ] 63-02-PLAN.md — pkg/slack package: SlackEnvelope + canonical JSON + Ed25519 sign/verify + 40 KB body cap + thin Slack Web API client + PostToBridge retry helper
- [ ] 63-03-PLAN.md — pkg/slack/bridge handler skeleton: 7-step verification flow (parse, replay, signature, action-auth, channel-mismatch, token, dispatch) against narrow injectable interfaces (DynamoDB-backed public-key fetch per RESEARCH correction #1)
- [ ] 63-04-PLAN.md — Compiler: extend Phase 62 km-notify-hook heredoc for sent_any multi-channel dispatch + emit KM_NOTIFY_EMAIL_ENABLED / KM_NOTIFY_SLACK_ENABLED / KM_SLACK_CHANNEL_ID / KM_SLACK_BRIDGE_URL into /etc/profile.d/km-notify-env.sh honoring *bool pointer semantics
- [ ] 63-05-PLAN.md — cmd/km-slack Go binary (post subcommand, SSM key load, retry) + Makefile build-sidecars target + init.go --sidecars upload + userdata.go aws s3 cp wiring (first sandbox-side Go binary in codebase)
- [ ] 63-06-PLAN.md — cmd/km-slack-bridge Lambda entry + AWS-backed adapters (5 interfaces) + infra/modules/lambda-slack-bridge (Function URL auth=NONE, replace_triggered_by) + infra/modules/dynamodb-slack-nonces + Terragrunt live wiring + regionalModules + buildLambdaZips + SandboxMetadata.SlackChannelID/SlackPerSandbox
- [ ] 63-07-PLAN.md — internal/app/cmd/slack.go: km slack init/test/status operator commands; idempotent bootstrap with --bot-token / --invite-email / --shared-channel / --force flags; Terragrunt apply integration
- [ ] 63-08-PLAN.md — km create channel provisioning: shared/per-sandbox/override resolution + sanitizeChannelName + ChannelInfo client extension; populate DynamoDB SlackChannelID + SlackPerSandbox + SlackArchiveOnDestroy; runtime SSM SendCommand env injection
- [ ] 63-09-PLAN.md — km destroy archive flow (final post + conversations.archive matrix; never blocks destroy on Slack failures) + km doctor checks (checkSlackTokenValidity via bridge auth.test, checkStaleSlackChannels via DynamoDB scan)
- [ ] 63-10-PLAN.md — Live UAT: test/e2e/slack/ harness gated by RUN_SLACK_E2E=1 + reusable test profiles + docs/slack-notifications.md operator guide + CLAUDE.md updates + 63-10-UAT.md sign-off

### Phase 63.1: Slack notify hook gap closure (INSERTED)

**Goal:** Close two operational gaps and one rotation-hardening item from Phase 63 UAT (2026-04-30). (1) Lambda subprocess silently swallowed step 11d runtime-injection outcomes — `KM_SLACK_CHANNEL_ID` and `KM_SLACK_BRIDGE_URL` never reached `/etc/profile.d/km-notify-env.sh` on remote-created sandboxes. Make the outcome visible on stderr (success path AND every failure branch) and fix the root cause so injection actually persists. (2) `km destroy` calls `destroySlackChannel` but the archive bridge call evidently doesn't reach Slack — visible logging shipped in `377b588` is the diagnostic harness; this phase diagnoses root cause and fixes it. (3) Full bot-token rotation cycle (revoke → cache TTL elapse → reissue → smoke test) end-to-end, deferred from UAT Scen 7. Operator workarounds remain documented in CLAUDE.md until shipped.
**Requirements**: SLCK-11, SLCK-12, SLCK-13
**Depends on:** Phase 63
**Plans:** 3/3 plans complete

Plans:
- [ ] 63.1-01-PLAN.md — SLCK-11: km create step 11d runtime injection — visibility commit + bounded SSM retry loop (6 × 5s) for agent readiness; 5 stderr branches; Wave 0 stderr-capture tests
- [ ] 63.1-02-PLAN.md — SLCK-12: km destroy Slack archive auto-trigger — visibility commit for cases A/B/E/F/G/H + success-path split + targeted root-cause fix driven by live destroy diagnosis
- [ ] 63.1-03-PLAN.md — SLCK-13: bot-token rotation full E2E — km slack rotate-token subcommand + forceSlackBridgeColdStart helper + operator runbook in docs/slack-notifications.md + live revoke-and-rotate UAT

### Phase 64: km create reliability and doctor cleanup hardening

**Goal:** [To be planned]
**Requirements**: TBD
**Depends on:** Phase 63
**Plans:** 0 plans

Plans:
- [ ] TBD (run /gsd:plan-phase 64 to break down)

### Phase 65: Four-account config model: separate accounts.organization (SCP) from accounts.dns_parent and rename

**Goal:** Rename km-config.yaml accounts.management into accounts.organization (SCP target, optional) and accounts.dns_parent (Route53 parent zone owner) so single-account installs run km bootstrap and km init cleanly with accounts.organization blank.
**Requirements**: none — operator-driven phase
**Depends on:** Phase 64
**Plans:** 4/4 plans complete

Plans:
- [ ] 65-01-PLAN.md — Config struct rename + Wave 0 test stubs (config.go, config_test.go, doctor_test.go stubs, cmd test skeletons)
- [ ] 65-02-PLAN.md — Migrate Go callers (bootstrap.go, init.go, create.go, uninit.go, info.go, configure.go + their tests)
- [ ] 65-03-PLAN.md — Doctor checks (DoctorConfigProvider rename, checkOrganizationAccountBlank WARN, checkLegacyManagementField FAIL)
- [ ] 65-04-PLAN.md — HCL/docs/live km-config migration + grep-audit verification

### Phase 66: Multi-instance support: configurable resource_prefix and email_subdomain

**Goal:** Allow multiple km installs to coexist in a single AWS account by introducing two configurable knobs in km-config.yaml — `resource_prefix` (default `"km"`) and `email_subdomain` (default `"sandboxes"`) — and threading them through every account-globally-unique resource name and the ~25 hardcoded `"sandboxes."` call sites. Defaults preserve today's behavior so existing installs upgrade without rename or data migration.

**Scope — names that must accept the prefix:**
- DynamoDB tables: `{prefix}-budgets`, `{prefix}-sandboxes`, `{prefix}-identities`, `{prefix}-schedules`, `{prefix}-slack-bridge-nonces`
- Management Lambdas: `{prefix}-ttl-handler`, `{prefix}-create-handler`, `{prefix}-email-create-handler`, `{prefix}-slack-bridge`, `{prefix}-ecs-spot-handler`
- Lambda IAM roles: `{prefix}-ttl-scheduler`, `{prefix}-email-handler-*`, `{prefix}-create-handler-*`, `{prefix}-spot-handler-*` (~12 sub-policies each)
- EventBridge: schedule group `{prefix}-at`, rule `{prefix}-ecs-spot-interruption`
- CloudWatch log group prefix: `/{prefix}/sandboxes/*`
- SSM parameter prefix: `/{prefix}/slack/*`, `/{prefix}/config/github/*`, `/{prefix}/config/remote-create/*`
- TF state: bucket `tf-{prefix}-state-{region}`, lock table `tf-{prefix}-locks-{region}`
- SES domain identity + Route53 zone: `{email_subdomain}.{domain}`
- All ~25 inline `"sandboxes." + cfg.Domain` call sites collapse to a single `Config.GetEmailDomain()` helper

**Out of scope (do NOT rename):**
- The `km` binary, CLI command names, `KM_*` env vars (sandbox-internal scope), `~/.km/` operator directory, `/opt/km/bin/` on sandboxes — these are runtime/UX, not AWS resources
- Per-sandbox resource names that already include `{sandboxID}` or `{region}` (e.g. `km-budget-enforcer-{sandboxID}`, ECS task families) — already collision-free
- Migrating existing installs from `km` to a new prefix — operators choose at `km init` and live with it

**Constraints:**
- **Terraform resource renames must NOT trigger destroy/create on stateful resources** (DynamoDB tables especially — data loss). Keep TF logical names (`aws_dynamodb_table.budget`) constant; parameterize only the `name` attribute via existing `var.table_name` pattern in modules
- **Org SCP escape hatch**: `km-sandbox-containment` is org-scoped, not account-scoped. If two installs share the org, only one can deploy SCP. Reuse the existing `accounts.organization == ""` skip path; document the caveat
- **Backwards compat is via defaults**: existing installs see no diff because `resource_prefix=km` and `email_subdomain=sandboxes` reproduce current names exactly
- **Slack bot token is per-prefix** (`/{prefix}/slack/bot-token`) — two installs use separate bot apps, not a shared workspace credential

**Requirements**: REQ-PLATFORM-MULTI-INSTANCE (new), REQ-CONFIG-EXTENSIBILITY (extends existing config model from Phase 65)
**Depends on:** Phase 65 (four-account config rename — this phase extends the same config struct)
**Plans:** 5/5 plans complete

Plans:
- [ ] 66-01-PLAN.md — Config struct (ResourcePrefix + EmailSubdomain) + helper methods (GetResourcePrefix, GetEmailDomain, GetSsmPrefix) + DoctorConfigProvider extension + Wave 0 unit tests
- [ ] 66-02-PLAN.md — Migrate Go email-domain call sites (~30 sites) to cfg.GetEmailDomain(); Lambda handlers read KM_EMAIL_DOMAIN env var; pkg/aws/ec2_ami.go AMI filter prefix-aware
- [ ] 66-03-PLAN.md — Migrate Go resource-name + SSM-path call sites (100+ sites) to cfg.GetResourcePrefix/GetSsmPrefix; Lambda handlers env-var-driven; ttl-handler self-naming (Pitfall 4); configui kmPrefix runtime variable (Pitfall 5)
- [ ] 66-04-PLAN.md — Extend site.hcl with KM_RESOURCE_PREFIX + KM_EMAIL_SUBDOMAIN; add var.resource_prefix to five Lambda modules; migrate five DynamoDB live configs + four Lambda live configs; TF plan smoke check (zero destroy/replace)
- [ ] 66-05-PLAN.md — km init env exports + km configure wizard + km doctor checkPrefixCollision/checkEmailDomainMatchesSESIdentity; OPERATOR-GUIDE + CLAUDE.md + km-config.yaml docs; final grep-audit gates

### Phase 67: Slack inbound: per-sandbox channel as bidirectional chat with km agent run dispatch

**Goal:** Close the Slack bidirectional loop deferred from Phase 63: messages in per-sandbox channels become Claude turns inside the sandbox via SQS FIFO dispatch, with sessions persisted by (channel_id, thread_ts) → claude_session_id mapping for in-thread continuity. Outbound replies (Phase 63's Stop / Notification hook) thread under the inbound message. Per-sandbox channel mode only (shared / override remain outbound-only).
**Requirements**: REQ-SLACK-IN-SCHEMA, REQ-SLACK-IN-DDB, REQ-SLACK-IN-EVENTS, REQ-SLACK-IN-DELIVERY, REQ-SLACK-IN-POLLER, REQ-SLACK-IN-LIFECYCLE, REQ-SLACK-IN-OBSERVABILITY, REQ-SLACK-IN-INIT (all new — added to REQUIREMENTS.md by Plan 67-00)
**Depends on:** Phase 66 (Phase 67 ships a forward-compatible GetResourcePrefix shim so it can be implemented before Phase 66 lands)
**Plans:** 13/13 plans complete

Plans:
- [ ] 67-00-PLAN.md — Wave 0: add github.com/aws/aws-sdk-go-v2/service/sqs dep, append REQ-SLACK-IN-* to REQUIREMENTS.md, seed six test stub files
- [ ] 67-01-PLAN.md — Profile schema + JSON schema field notifySlackInboundEnabled + three validation rules
- [ ] 67-02-PLAN.md — DynamoDB module dynamodb-slack-threads v1.0.0 + dynamodb-sandboxes v1.1.0 (additive slack_channel_id-index GSI) + Config helpers (SlackThreadsTableName, GetResourcePrefix shim, GetSlackThreadsTableName)
- [ ] 67-03-PLAN.md — Bridge /events handler (signing-secret HMAC, url_verification, bot-loop filter, nonce dedup) + five new interfaces + comprehensive table-driven tests
- [ ] 67-04-PLAN.md — Compiler poller (km-slack-inbound-poller bash + systemd unit, inline heredoc) + KM_SLACK_INBOUND_QUEUE_URL env + km-notify-hook --thread pass-through
- [ ] 67-05-PLAN.md — Five AWS adapters (SQSSender, DDBThreadStore, SandboxByChannel, SSMSigningSecretFetcher, CachedBotUserIDFetcher) + Lambda main.go path dispatch + bridge IAM extensions
- [ ] 67-06-PLAN.md — pkg/aws/sqs.go helpers + km create SQS provisioning + DDB persist + env injection + rollback + sandbox EC2 IAM (sqs:Receive/Delete/ChangeVisibility on own queue)
- [ ] 67-07-PLAN.md — km create ready announcement + km destroy drain (stop poller, wait ≤30s, queue delete, threads cleanup) before Phase 63 archive
- [ ] 67-08-PLAN.md — km status / km list --wide / km doctor (three new checks: queue exists, stale queues, Events scopes)
- [ ] 67-09-PLAN.md — km slack init --signing-secret + scope verification + Events URL print
- [ ] 67-10-PLAN.md — RUN_SLACK_E2E gated end-to-end test + docs/slack-notifications.md inbound section + CLAUDE.md update + manual UAT checkpoint
- [ ] 67-11-PLAN.md — Gap A closure: move Slack reply post from Stop hook into the inbound poller (read .result from output.json) + KM_SLACK_INBOUND_REPLY_HANDLED gate to suppress double-post + 2 new compiler tests + UAT re-test section
- [ ] 67-12-PLAN.md — Gap B closure: switch isBotLoop from deny-list to allow-list semantics (only "" + "thread_broadcast" pass) + 14 new system-subtype test cases + ThreadBroadcastPasses positive test + debug log line for forensics + UAT re-test section

### Phase 67.1: Slack inbound ACK reaction (INSERTED)

**Goal:** Close the Phase 67 UAT UX gap where Slack users get no visual feedback that their inbound message was received until the agent boots and posts its first reply (10-60s for paused sandboxes). The bridge Lambda adds a 👀 emoji reaction to the originating Slack message within ~1 second of successful SQS enqueue via Slack `reactions.add` Web API. Bridge-only change — no sandbox redeploy. Configurable emoji via `KM_SLACK_ACK_EMOJI` Lambda env var (default `eyes`). New required Slack scope: `reactions:write`.
**Requirements**: UAT-1..UAT-5 (per 67.1-CONTEXT.md success criteria — no REQ-* IDs assigned; this is a UAT-gap-closure phase)
**Depends on:** Phase 67
**Plans:** 3/3 plans complete

Plans:
- [x] 67.1-01-PLAN.md — Bridge code: Reactor interface + SlackReactorAdapter + EventsHandler fire-and-forget call after SQS write + cold-start KM_SLACK_ACK_EMOJI wiring + 4 unit tests + newHandler test signature ripple
- [x] 67.1-02-PLAN.md — Scope checks: append `reactions:write` to required-slice in `km slack init` (VerifyEventsAPIScopes) and `km doctor` (checkSlackAppEventsScopes) + extended tests
- [x] 67.1-03-PLAN.md — Terraform env passthrough (lambda-slack-bridge module) + docs/slack-notifications.md ACK section + CLAUDE.md update + manual UAT checkpoint (operator scope-add + reinstall + 👀 smoke test) — UAT APPROVED

### Phase 67.2: Slack ACK reaction bounded retry — absorb transient Slack API failures (429, 5xx, internal_error) with backoff + jitter inside SlackReactorAdapter.Add (INSERTED)

**Spec:** `docs/superpowers/specs/2026-05-14-slack-ack-reaction-bounded-retry-design.md`
**Goal:** Make the Phase 67.1 ACK reaction robust to transient Slack API failures. Today's single-attempt fire-and-forget at `pkg/slack/bridge/events_handler.go:228-241` logs and discards HTTP 429 (despite already producing a typed `ErrSlackRateLimited{RetryAfter}`), HTTP 5xx, network errors, and Slack JSON errors `internal_error`/`service_unavailable`/`fatal_error`/`request_timeout`. This proposal adds bounded retry (max 3 attempts, 200ms→600ms backoff with ±25% jitter, honoring `Retry-After` within the context budget) inside `SlackReactorAdapter.Add`, classifies error codes into success / terminal-no-retry (auth-class at Error log, bad-input at Warn) / transient-retry, defaults unknown error strings to transient (safer for new Slack codes). Bumps handler goroutine context 5s→10s to fit retry budget. `Reactor` interface signature unchanged; back-compat with existing fakes/tests. Triggered by intermittent missing 👀 during 2026-05-14 Slack-wide outages.
**Requirements**: REQ-ACK-RETRY-CLASSIFY, REQ-ACK-RETRY-BUDGET, REQ-ACK-RETRY-RETRYAFTER, REQ-ACK-RETRY-CTXCANCEL, REQ-ACK-RETRY-JITTER-DETERMINISM, REQ-ACK-RETRY-LOGS, REQ-ACK-RETRY-HANDLER-TIMEOUT, REQ-ACK-RETRY-DEPLOY
**Depends on:** Phase 67.1
**Plans:** 3/3 plans complete

Plans:
- [x] 67.2-01-PLAN.md — Wave 1: Add reactionErrorClass + classifyReactionError pure helper + recordingTransport/log-capture test fixtures + TestClassifyReactionError table-driven test (REQ-ACK-RETRY-CLASSIFY)
- [x] 67.2-02-PLAN.md — Wave 2: Wire classifier into SlackReactorAdapter.Add as bounded retry loop (3 attempts, 200ms→600ms ±25% jitter, Retry-After honoring, ctx-cancellable) + Sleep/Rand injection fields + doOneAttempt/sleepWithCtx/withJitter helpers + 10 new TestReactor_* tests + handler context 5s→10s bump (REQ-ACK-RETRY-BUDGET, REQ-ACK-RETRY-RETRYAFTER, REQ-ACK-RETRY-CTXCANCEL, REQ-ACK-RETRY-JITTER-DETERMINISM, REQ-ACK-RETRY-LOGS, REQ-ACK-RETRY-HANDLER-TIMEOUT)
- [x] 67.2-03-PLAN.md — Wave 3: docs/slack-notifications.md retry paragraph + CLAUDE.md ACK section update + operator UAT checkpoint (make build && km init --lambdas + live #sb-{id} smoke test) (REQ-ACK-RETRY-DEPLOY)

### Phase 68: Slack transcript streaming — per-turn chat + gzipped JSONL upload (Phase A)

**Spec:** `docs/superpowers/specs/2026-05-03-slack-transcript-streaming-design.md`
**Goal:** Make a Slack-connected sandbox a faithful real-time view of its Claude session — every assistant turn streams to a per-sandbox Slack thread as it happens, and the full session transcript (gzipped JSONL) lands as a downloadable file in the same thread when the run ends. Provisions a stream-message → transcript-position mapping table that a future Phase B (reaction-triggered session fork) can consume.
**Requirements**: Spec-driven (no REQ-* IDs) — see 68-CONTEXT.md locked decisions
**Depends on:** Phase 67
**Plans:** 13/13 plans complete

Note: Phase A only. Reaction-triggered session fork deferred to a future phase.

Plans:
- [ ] 68-00-PLAN.md — Wave 0: seed 13 stub test files + 3 testdata fixtures so Plans 01-12 have green-baseline test surfaces
- [ ] 68-01-PLAN.md — Profile schema: notifySlackTranscriptEnabled field + 3 validation rules + JSON Schema entry
- [x] 68-02-PLAN.md — Envelope schema: ActionUpload const + 4 additive fields (S3Key/Filename/ContentType/SizeBytes) + canonical JSON forward+backward compat
- [ ] 68-03-PLAN.md — DDB Terraform module dynamodb-slack-stream-messages + Config.GetSlackStreamMessagesTableName helper (resolves table-naming open question)
- [ ] 68-04-PLAN.md — pkg/slack.Client.UploadFile method (Slack 3-step file upload flow, streaming, explicit Content-Length)
- [ ] 68-05-PLAN.md — cmd/km-slack restructure: multi-subcommand dispatcher (post + upload + record-mapping)
- [ ] 68-06-PLAN.md — IAM additions: ec2spot artifacts_bucket variable + transcript S3 PutObject + DDB PutItem policies; lambda-slack-bridge S3 GetObject + HeadObject on transcripts/*
- [ ] 68-07-PLAN.md — CLI flags: --transcript-stream / --no-transcript-stream on km agent run + km shell
- [ ] 68-08-PLAN.md — Bridge ActionUpload handler: validation + S3 stream → Slack 3-step upload + cold-start files:write scope check
- [ ] 68-09-PLAN.md — Hook script: PostToolUse branch (auto-thread-parent + offset tracking + tool one-liners + record-mapping) + Stop branch transcript upload + settings.json registration
- [ ] 68-10-PLAN.md — km create env injection (KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED + KM_SLACK_STREAM_TABLE) + operator audience warning with Slack channel member count
- [x] 68-11-PLAN.md — km doctor checks: slack_transcript_table_exists + slack_files_write_scope + slack_transcript_stale_objects
- [ ] 68-12-PLAN.md — Documentation (docs/slack-notifications.md + CLAUDE.md) + UAT (9 manual scenarios)

### Phase 68.1: Fix km agent run PostToolUse hook skip blocking transcript streaming (INSERTED)

**Goal:** [Urgent work - to be planned]
**Requirements**: TBD
**Depends on:** Phase 68
**Plans:** 0 plans

Plans:
- [ ] TBD (run /gsd:plan-phase 68.1 to break down)

### Phase 69: AWS API SCP-style allow/deny via SigV4 inspection

**Spec:** `.planning/phases/69-aws-api-scp-style-allow-deny-via-sigv4-inspection/SPEC.md`
**Goal:** Operators can declare a service-level AWS allowlist on a SandboxProfile and the http-proxy will allow, log-only, or block AWS API calls (any service that uses SigV4) made by the sandbox user, while platform sidecars (km-mail-poller, km-slack-inbound-poller, OTEL exporter, metadata sync) remain unaffected. Schema designed to be forward-compatible with operation-level entries (`s3:GetObject`) in a follow-up phase.
**Requirements**: Spec-driven (no REQ-* IDs) — see SPEC.md locked decisions from brainstorming session 2026-05-04
**Depends on:** Phase 6 (Bedrock metering + http-proxy MITM), Phase 40 (eBPF cgroup egress + transparent proxy maps), Phase 62/63 (audit-log sidecar event consumption)
**Success Criteria** (what must be TRUE):
  1. Profile with `inspection: enforce` + `awsAllowlist: ["*"]` lets `aws sts get-caller-identity` succeed; audit log shows `aws_api_allowed service=sts mode=enforce`
  2. Profile with `inspection: enforce` + `awsAllowlist: []` returns HTTP 403 from the proxy on AWS CLI calls; audit log shows `aws_api_blocked reason=empty_allowlist`; concurrently `km email read <sandbox>` (driven by `km-mail-poller`) continues to work and emits `aws_api_platform` events
  3. Profile with `inspection: observe` + `awsAllowlist: []` lets calls through but every call shows `aws_api_blocked` (mode=observe) — supports inventory-before-enforce
  4. `km shell --learn` against a permissive profile generates `inspection: observe` + allowlist matching the exact set of services the operator touched
  5. Profile with `inspection: enforce` + non-zero Bedrock budget but missing `bedrock-runtime` from allowlist fails `km validate` with a message naming the missing entry
  6. `km doctor` runs `aws_inspection_uid_map` + `aws_allowlist_known_services` checks green on a sandbox using each of the three modes
  7. `aws_api_allowed`, `aws_api_blocked`, `aws_api_platform` events flow through the audit-log sidecar to its configured destination with `sandbox_id`, `service`, `region`, `host`, `method`, `path`, `mode`, and (for platform events) `uid` + `caller` fields populated
**Plans:** 11 plans

Plans:
- [ ] 69-00-PLAN.md — Wave 0: seed 4 stub test files + 2 shared testdata fixtures (SigV4 examples, proxy log events) so Plans 01-08 have green-baseline test surfaces
- [ ] 69-01-PLAN.md — Profile schema: spec.sourceAccess.aws (Inspection enum + Allowlist) struct + JSON Schema + 4 ValidateSemantic rules (Bedrock cross-check, wildcard-mixing, allowlist-required-when-not-off, enum)
- [ ] 69-02-PLAN.md — eBPF: new pinned sock_to_uid map (BPF_MAP_TYPE_HASH) populated by cgroup/connect4 via bpf_get_current_uid_gid; bpf2go regen + loader.go pin/unpin
- [ ] 69-03-PLAN.md — Proxy AWS inspector: new sidecars/http-proxy/httpproxy/aws.go with SigV4 parser, allowlist matcher, four audit emitters (allowed/blocked/platform/unsigned), AWSBlockedResponse; proxy.go registers AWS handlers ahead of budget block
- [ ] 69-04-PLAN.md — Vetted SigV4 service slug list: pkg/aws/sigv4_services.go with KnownSigV4Services + IsKnownSigV4Service helper (colon-suffix forward-compat)
- [ ] 69-05-PLAN.md — Compiler env-var injection: KM_AWS_INSPECTION + KM_AWS_ALLOWLIST + KM_AWS_PLATFORM_UID_MAX into km-http-proxy systemd unit; UserDataParams + joinAWSAllowlist helper
- [ ] 69-06-PLAN.md — Proxy wiring + transparent.go GetCallerUID: WithAWSPlatformUIDLookup option, sock_to_uid map load in transparent.go's sync.Once, main.go reads env vars
- [ ] 69-07-PLAN.md — Learn-mode parser: AWSServices field on learnObservedState, dispatch aws_api_allowed/blocked into RecordAWSService (skip platform/unsigned), GenerateAnnotatedYAML emits inspection: observe + alphabetized allowlist
- [ ] 69-08-PLAN.md — km doctor: two new checks (aws_inspection_uid_map probes km-mail-poller uid via SSM RunCommand; aws_allowlist_known_services WARNs on slugs not in pkg/aws/sigv4_services.go)
- [ ] 69-09-PLAN.md — Documentation: docs/aws-allowlist.md operator guide + CLAUDE.md AWS allowlist section
- [ ] 69-10-PLAN.md — Manual UAT: real EC2 sandbox running through 4-flow demo storyboard (wide-open + locked-down + observe + learn) + km doctor verification + 69-VERIFY.md captures

### Phase 70: Codex parity for operator-notify, Slack notify, and Slack inbound dispatcher (Tier 2) + Slack prefix routing & cross-agent thread switching

**Spec:** `.planning/phases/70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher/SPEC.md`
**Goal:** A SandboxProfile can declare `spec.cli.agent: codex` and the resulting sandbox gets the same operator-notify (Phase 62) and Slack notify (Phase 63) experience that Claude sandboxes get today, plus working Slack inbound bidirectional chat (Phase 67) — including multi-turn session resume — driven by Codex CLI instead of Claude Code. The hook plumbing is unified: a single `km-notify-hook` script handles both agents' payloads. The DDB schema gains `agent_type` + `last_assistant_msg` attributes on `km-slack-threads` so the same row can carry either a Claude or a Codex session. Beyond agent selection, the Slack inbound dispatcher learns per-message prefix routing: a Slack message starting with `claude:` or `codex:` routes the turn to the named agent. A prefix that names the *other* agent inside an existing thread triggers a clean handoff — bot posts a "Switching to {agent} → continuing in this thread." message with a Slack permalink, spawns a new top-level thread in the same channel, and seeds the new agent with the prior agent's last assistant message. Phase 68 transcript streaming parity is explicitly deferred to Tier 3.
**Requirements**: Spec-driven (no REQ-* IDs) — see SPEC.md locked decisions from brainstorming sessions 2026-05-05 (Tier 2 parity) + 2026-05-22 (prefix routing + cross-agent switch)
**Depends on:** Phase 58 (Codex CLI agent-run support + `spec.cli.codexArgs`), Phase 62 (operator-notify hook script), Phase 63 (Slack notify hook + km-slack sidecar + bridge Lambda), Phase 67 (Slack inbound poller + DDB threads table)
**Success Criteria** (what must be TRUE — full verification text in SPEC.md § Success criteria):
  1. Profile with `spec.cli.agent: codex` + existing notify/Slack toggles → after `km create`, sandbox has both `~/.claude/settings.json` AND fresh `~/.codex/config.toml` with `[features] codex_hooks = true` + `PermissionRequest` + `Stop` hooks pointing at `/opt/km/bin/km-notify-hook` (no `PostToolUse` entry; Tier 2 defers)
  2. `km agent run --codex --prompt "What model are you?"` → `Stop` event payload contains `last_assistant_message`; notify hook reads it directly, sends operator email + Slack post; no transcript-tailing path runs
  3. Codex `PermissionRequest` event → notify hook sends operator + Slack ping with tool name, exits 0 with no JSON body, `--dangerously-bypass-approvals-and-sandbox` continues to auto-approve
  4. Operator types in `#sb-x` against inbound-enabled `agent: codex` sandbox → poller dispatches `codex exec --json --dangerously-bypass-approvals-and-sandbox`, captures session ID, writes `km-slack-threads` row with `agent_type=codex`
  5. Follow-up Slack message → poller dispatches `codex exec resume <session-id> "<prompt>"`, conversation continues in Codex's session context
  6. Stop hook gating on `KM_SLACK_THREAD_TS` works for Codex: poller-driven run → Stop hook's Slack branch silent; operator `km agent run --codex` → Stop hook posts as usual
  7. `km doctor` runs `codex_hook_config_present` + `agent_type_consistency` green on `agent: codex` sandbox; no false positives on claude-default sandbox
  8. **Top-level prefix routing:** On `agent: claude` profile, operator posts `codex: list workspace files` as new top-level → poller strips prefix, dispatches codex, writes new DDB row with `agent_type=codex` keyed on the new `thread_ts`; profile compiled `KM_AGENT` env var unchanged on disk
  9. **Same-agent prefix is no-op:** `claude: do another thing` inside an existing claude-rooted thread → strip + resume same session in same thread; no new thread, no new DDB row, no handoff post
  10. **Cross-agent mid-thread switch:** Inside running claude thread, operator posts `codex: check this` → bot posts "Switching to codex → continuing in this thread." with permalink in old thread; new top-level message appears with "Continuing from <permalink>" + truncated last assistant excerpt; codex's reply (seeded with prior assistant message) lands in new thread as first reply; DDB has two rows; old claude session is NOT killed and remains resumable
**Plans:** 9/10 plans executed

Plans:
- [ ] 70-00-PLAN.md — Spike: confirm `codex exec --json` fires hooks (Stop + last_assistant_message) on a learn-derived sandbox with Codex auth'd. ~30 minutes, single sandbox, discard.
- [ ] 70-01-PLAN.md — Schema + validation: `pkg/profile/types.go` adds `Agent string` to `CLISpec`, embedded JSON Schema enum (`claude` | `codex`), validator unit tests; absence ≡ `claude`
- [ ] 70-02-PLAN.md — Compiler config-file writer: `pkg/compiler/userdata.go` writes `~/.codex/config.toml` for every sandbox + emits `KM_AGENT` to both `/etc/profile.d/km-notify-env.sh` and `/etc/km/notify.env`
- [ ] 70-03-PLAN.md — `km-notify-hook` agent-aware branches: `PermissionRequest` (Codex) branch in the heredoc-inlined bash + `last_assistant_message` fallback in `Stop` clause; ~30 LOC; smoke tests piping canonical Codex + Claude payloads
- [ ] 70-04-PLAN.md — `km-slack` sidecar API additions: `--new-message` flag on `post` (omits `thread_ts`, returns `ts`), `permalink` subcommand (wraps `chat.getPermalink`), `update` subcommand (wraps `chat.update`); thin REST wrappers, no business logic
- [ ] 70-05-PLAN.md — Phase 67 poller dispatch fork + DDB attribute writeback: Codex first-turn + resume bash branches, hook-file session-ID extraction, writer always sets `agent_type` + `last_assistant_msg`, reader defaults to `claude` on missing attribute
- [ ] 70-06-PLAN.md — Slack prefix routing & cross-agent switch: poller-side prefix parser, per-thread state lookup, switch sequence (post new top-level first → capture permalink → post handoff in old thread → compose seeded prompt → dispatch new agent → write new DDB row); uses km-slack flags from Plan 70-04
- [ ] 70-07-PLAN.md — `km doctor` checks: `codex_hook_config_present` (SSM-probes `~/.codex/config.toml`) + `agent_type_consistency` (cross-checks DDB rows against S3 profile); honor `--all-regions`
- [ ] 70-08-PLAN.md — Documentation: `docs/codex-parity.md` operator guide with prefix + switching examples; CLAUDE.md additions for `agent: codex` + `claude:`/`codex:` prefix; `docs/slack-notifications.md` "Prefix routing & agent switching" section
- [ ] 70-09-PLAN.md — End-to-end manual UAT: nine demo flows on real EC2 sandboxes (one `agent: claude` + one `agent: codex` from learn-derived AMI); captures live in `70-VERIFY.md`

### Phase 71: Agent playbook orchestration — multi-step prompts with session continuity against existing sandboxes via cron and manual triggers

**Spec:** `.planning/phases/71-agent-playbook-orchestration-multi-step-prompts-with-session-continuity-against-existing-sandboxes-via-cron-and-manual-triggers-driven-by-sandbox-side-runner-sidecar/SPEC.md`
**Goal:** An operator can declare a multi-step Claude prompt sequence as a YAML file (`kind: Playbook`), register it with `km playbook apply`, and either fire it manually (`km playbook run`) or schedule it via the existing `km at` integration (`km at '0 8 * * 1-5 *' playbook run morning-ops --sandbox sb-ops`). Each scheduled fire walks the steps in order against an existing sandbox, resuming the same Claude session across steps and across runs (so day N continues the conversation from day N−1) via a `(playbook, sandbox) → claude_session_id` DDB map mirroring Phase 67's `slack-threads`. If the sandbox is paused/stopped at fire time, it auto-resumes; if it's destroyed, the run fails clearly. Step failures abort the run and notify the operator via the existing Phase 62/63 hooks. v1 ships cron + manual triggers only; the data model leaves clean room for event triggers (email arrival, Slack reaction) and ephemeral-sandbox lifecycle workflows (create → run → destroy) to be added without re-shaping any v1 surface.
**Requirements**: Spec-driven (no REQ-* IDs) — see SPEC.md locked decisions from brainstorming session 2026-05-05
**Depends on:** Phase 50 (`km agent run` non-interactive Claude execution), Phase 51 (tmux-backed agent sessions), Phase 62 (operator-notify hook), Phase 63 (Slack notify hook + km-slack sidecar), Phase 67 (Slack inbound poller pattern + per-sandbox SQS FIFO + DDB session-id map), the existing `km at` EventBridge Scheduler integration
**Success Criteria** (what must be TRUE — full verification text in SPEC.md § Success criteria):
  1. `km playbook validate` accepts a well-formed playbook (`metadata.name`, `spec.sandbox.mode: existing`, `spec.session.scope: [playbook, sandbox]`, ≥ 1 named step with non-empty prompt) and rejects each malformed variant with a field-pointing error
  2. `km playbook apply` writes `s3://{artifacts}/playbooks/{name}/{sha256}.yaml` (content-addressed, idempotent) + a `{prefix}-playbooks` DDB row
  3. `km playbook run morning-ops --sandbox sb-ops` against a running sandbox enqueues exactly one SQS FIFO message (MessageGroupId `playbook:morning-ops`); the runner walks all steps and the final `playbook-runs` row reaches `status=completed` with all step outputs at `s3://{artifacts}/playbook-runs/{sandbox_id}/{run_id}/step-{n}-{name}.json`
  4. `{prefix}-playbook-sessions` row keyed `(morning-ops, sb-ops)` persists `claude_session_id` across runs; a second run of the same playbook resumes the prior session (model demonstrably has memory of the prior run's last turn)
  5. `km at '*/5 * * * ? *' playbook run morning-ops --sandbox sb-ops` creates an EventBridge schedule routed through the TTL Lambda with the new `playbook-run` event type; at the next boundary it fires and reaches the same end-state as criterion 3
  6. Sandbox readiness: stopped/paused → Lambda calls StartInstances + enqueues immediately (Lambda never waits on boot); terminated/missing → run row written with `status=failed` and operator-notify fires; total wall-clock from cron fire to first step prompt is ≤ 120 s on a hibernated sandbox
  7. Step-failure abort: a step exiting non-zero leaves run `status=failed`, `current_step` pinned at failure, no subsequent steps executed, operator-notify fires with `kind: playbook-run-completed status=failed`
  8. Concurrent-fire serialization: two same-playbook fires within 5 s produce two distinct `run_id` values processed strictly sequentially by SQS FIFO MessageGroupId; different playbooks against the same sandbox process in parallel
  9. Crash-mid-step idempotency: SIGKILL the runner mid-step → systemd restarts → SQS visibility timeout re-delivers → runner replays the current step; no duplicate run row, run eventually reaches `status=completed`
  10. `km doctor` runs `playbook_runner_service_active`, `playbook_queue_exists`, `playbook_dlq_depth` checks green on healthy installs and red on broken
  11. `km destroy sb-ops` atomically deletes per-sandbox FIFO + DLQ + SSM param; `playbook-runs` and `playbook-sessions` rows retained (history); `km playbook delete` clears playbook-scoped state
  12. Operator-notify hook gains `kind: playbook-run-completed` payload (`playbook`, `sandbox_id`, `run_id`, `status`, `steps_completed`, `steps_total`, `duration_seconds`, `error_msg`); existing Phase 62/63 formatter routes to email and Slack with no new transport code
**Plans:** 11 plans

Plans:
- [ ] 71-00-PLAN.md — Wave 0 stubs: pkg/playbook + pkg/playbook/runner skeletons, 20 test stub files (one per Wave 1+ test target), 6 YAML testdata fixtures (one per SC-1 invalid-rule variant)
- [ ] 71-01-PLAN.md — pkg/profile.PlaybookEnabled (bool, no cross-field rules) + JSON schema entry; pkg/playbook Parse + Validate with field-path errors covering all SPEC § Validation rules (closes SC-1 at the package layer)
- [ ] 71-02-PLAN.md — Three DDB Terraform modules (dynamodb-{playbooks,playbook-sessions,playbook-runs}/v1.0.0/) + matching Terragrunt live entries + Go-side GetPlaybook*TableName helpers in pkg/aws/config.go (multi-instance prefix-aware)
- [ ] 71-03-PLAN.md — pkg/aws/sqs.go gains PlaybookQueueName, PlaybookDLQName, CreatePlaybookQueues (two-step DLQ flow with RedrivePolicy maxReceiveCount=5), DeletePlaybookQueues (idempotent)
- [ ] 71-04-PLAN.md — TTL Lambda playbook-run event handler (cmd/ttl-handler) covering {running, stopped, paused, terminated, missing} sandbox states, mints pr-{uuid} run_ids, sends SQS FIFO with MessageGroupId=playbook:{name}; ttl-handler Terraform module IAM policies + env vars extended for SQS + 3 playbook DDB tables
- [ ] 71-05-PLAN.md — pkg/compiler/userdata.go conditional bash runner heredoc + systemd unit (EnvironmentFile=-/etc/km/notify.env per CLAUDE.md gotcha) + km-notify-hook playbook-run-completed case + env-file injection; pkg/playbook/runner Go shim with TestSessionResume + TestStepFailure (bash heredoc is production runner per Phase 67 precedent — Go is unit-testable shadow)
- [ ] 71-06-PLAN.md — pkg/playbook/apply.go (content-addressed S3 + idempotent DDB) + internal/app/cmd/playbook.go cobra group with all 10 sub-commands (validate/apply/list/show/run/list-runs/show-run/logs/cancel-run/delete) wired into root.go with DI per at_test.go convention
- [ ] 71-07-PLAN.md — internal/app/cmd/at.go three-point edit: schedulableCommands map entry, two-word command merge (playbook + run -> playbook-run), buildTargetInput branch reading --sandbox flag (with ResolveSandboxID guard for the playbook-name positional arg)
- [ ] 71-08-PLAN.md — Lifecycle wiring: create_playbook.go (provision queue + SSM param + DDB row) + destroy_playbook.go (teardown preserving playbook-runs + playbook-sessions history per SC-11) + create.go/destroy.go integration + km init wiring of 3 DDB modules + ttl-handler terragrunt deps
- [ ] 71-09-PLAN.md — internal/app/cmd/doctor_playbook.go three checks: playbook_queue_exists, playbook_dlq_depth, playbook_queue_healthy (renamed from SPEC playbook_runner_service_active per planner finding #9 — uses SQS attrs as runner liveness proxy since systemd state is not operator-side observable under SCP)
- [ ] 71-10-PLAN.md — Closeout: docs/playbooks.md operator guide + CLAUDE.md update + playbooks/morning-ops.yaml reference + 2 integration tests (concurrent serialization SC-8 + crash recovery SC-9 with build tag) + 71-UAT.md (10 manual scenarios) + 71-VALIDATION.md Per-Task Verification Map populated; ends in operator UAT checkpoint

### Phase 72: slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator

**Goal:** Support installing klankermaker into a corporate Slack workspace with auto-detected native vs Slack Connect invites, a `km slack manifest` generator that ships the new `users:read.email` scope as code, and profile-driven per-sandbox auto-invite (`spec.cli.notifySlackInviteEmails`) of ADDITIONAL collaborators beyond the always-invited primary operator. The primary operator invite (and the additional-folks loop) route through the auto-detect orchestrator; `spec.cli.useSlackConnect` (default true) gates the Connect fallback for the additional-folks list only.
**Requirements**: VALIDATION-Layer-1..8 (CONTEXT.md decisions D1–D12; mapped to the 8 layers in 72-VALIDATION.md)
**Depends on:** Phase 71
**Plans:** 10/10 plans complete

Plans:
- [x] 72-00-PLAN.md — Wave 0 stub seeding: 9 test stub files + manifest template + golden fixture (failing tests for Layers 1-8)
- [x] 72-01-PLAN.md — pkg/slack client primitives: LookupUserByEmail (lowercase, miss=boolean) + InviteUserToChannel (idempotent on already_in_channel) + SlackAPIResponse.User extension (Layer 1)
- [x] 72-02-PLAN.md — Profile fields notifySlackInviteEmails + useSlackConnect (*bool, default true): types.go + JSON schema (atomic) + validate.go rules SE1/SE2 (Layer 6)
- [x] 72-03-PLAN.md — km slack manifest cobra command: RenderSlackManifest + embedded text/template + golden-file test + scope warning includes users:read.email (Layer 5)
- [x] 72-04-PLAN.md — pkg/slack/invite.go orchestrator: EnsureMemberByEmail (8 result paths incl. non-interactive AutoConnect) + EnsureMemberOpts.AutoConnect + Prompter + InviteAPI + ErrAlreadyInChannel sentinel + InviteUserToChannelStrict (Layer 2)
- [x] 72-05-PLAN.md — km slack invite cobra command: RunSlackInvite + ConnectFallbackPrompter (stdin) + channel resolution (name/ID/SSM-default) + exit codes 0/1/2 (Layer 3)
- [x] 72-06-PLAN.md — Refactor RunSlackInit to call orchestrator instead of InviteShared directly + add users:read.email scope warning at init time (Layer 4 + Pitfall 1 mitigation)
- [x] 72-07-PLAN.md — km create: refactor primary operator invite through orchestrator (AutoConnect=true, always invited) + additional-folks loop gated by useSlackConnect, fail-soft on SkippedExternal/Failed (Layer 7)
- [x] 72-08-PLAN.md — km doctor slack_users_read_email_scope check: mirrors slack_files_write_scope pattern; surfaces scope drift before runtime errors (Layer 8)
- [x] 72-09-PLAN.md — Closeout: docs/slack-notifications.md Phase 72 section + CLAUDE.md updates + 72-VALIDATION.md Per-Task Verification Map populated + 72-UAT.md (8 operator scenarios) + operator UAT sign-off (KPH 2026-05-30)

### Phase 73: km vscode remote session via SSM

**Goal:** Add `km vscode start | status` so an operator can connect their **local desktop VS Code** (via the Remote-SSH extension) to a sandbox over SSM port-forward. km auto-generates a per-sandbox ed25519 keypair on the operator's laptop at `km create` time (private key under `~/.km/keys/sb-<id>`, public key shipped via userdata to `/home/sandbox/.ssh/authorized_keys`). `km vscode start <sb>` opens a foreground SSM port-forward (sandbox port 22 → operator local port 2222), upserts a managed entry in `~/.ssh/config`, and tells the operator how to launch Remote-SSH. `km destroy` cleans up the ssh-config block and the key files. Gated by a new default-true `spec.cli.vscodeEnabled` profile flag. Nothing related to VS Code is installed on the sandbox — Remote-SSH auto-deploys `vscode-server` on first connect. Full design at `docs/superpowers/specs/2026-05-06-km-vscode-design.md`.
**Requirements**: GOAL-1..9 (goal-backward; no REQ-IDs in REQUIREMENTS.md — developer-experience phase, see 73-CONTEXT.md must-haves)
**Depends on:** Phase 72
**Plans:** 10/10 plans complete

Plans:
- [ ] 73-00-PLAN.md — Wave 0 stub seeding: 5 test stub files + 4 production stubs (sshkey/keygen.go, sshconfig.go, vscode.go, profile types_test, userdata_test additions)
- [ ] 73-01-PLAN.md — pkg/sshkey/keygen.go: Go-native ed25519 keypair generation using crypto/ed25519 + golang.org/x/crypto/ssh.MarshalPrivateKey (Wave 1, parallel)
- [ ] 73-02-PLAN.md — Profile field spec.cli.vscodeEnabled (*bool, default true) + IsVSCodeEnabled helper + JSON schema (Wave 1, parallel)
- [ ] 73-03-PLAN.md — internal/app/cmd/sshconfig.go: ~/.ssh/config managed-block parser/writer (UpsertHost + RemoveHost, atomic temp+rename) (Wave 1, parallel)
- [ ] 73-04-PLAN.md — pkg/compiler/userdata.go: conditional sshd-enable + authorized_keys + restorecon block + NetworkConfig.VSCodeSSHPubKey + loud-fail validation (Wave 1, parallel)
- [ ] 73-05-PLAN.md — internal/app/cmd/create.go: generate per-sandbox keypair before compiler.Compile, populate NetworkConfig.VSCodeSSHPubKey (Wave 2)
- [ ] 73-06-PLAN.md — internal/app/cmd/vscode.go: km vscode start + status cobra commands; reuses buildPortForwardCmd + sendSSMAndWait verbatim; root.go registration (Wave 3)
- [ ] 73-07-PLAN.md — internal/app/cmd/destroy.go: cleanup hook removes ~/.ssh/config block + ~/.km/keys/sb-X* files (idempotent on missing) (Wave 2, parallel with 73-05)
- [ ] 73-08-PLAN.md — Docs: docs/vscode.md operator guide + CLAUDE.md additions (Wave 4)
- [ ] 73-09-PLAN.md — Closeout: 73-VALIDATION.md Per-Task Verification Map populated + 73-UAT.md (6 manual scenarios) + blocking operator UAT checkpoint (Wave 5)

### Phase 74: Slack mrkdwn rendering: tokenizer-based markdown→Slack mrkdwn transformer + optional Block Kit tier for streaming hook output

**Goal:** Eliminate today's production failures (literal `***heading***` asterisks, dropped `# headings`, broken pipe-tables) by adding a tokenizer-based renderer that converts Claude's CommonMark-ish output into valid Slack mrkdwn (Tier 1) and structured Block Kit (Tier 2). Two-PR phasing: PR1 ships Tier 1 + `--render=mrkdwn` flag with the streaming hook unchanged; PR2 ships Tier 2 Block Kit + flips the Phase 68 streaming hook in `pkg/compiler/userdata.go _km_stream_drain` to `--render=blocks`. Existing Phase 62/63/67 callers stay on default `plain` (no behavior change). Robustness moat: tokenizer preserves code blocks byte-for-byte, idempotent + fail-soft properties, fuzz target, corpus fixtures.
**Requirements**: REND-01..REND-16, BLK-01..BLK-10, BRDG-01..BRDG-03, HOOK-01 (local to phase, defined in 74-VALIDATION.md)
**Depends on:** Phase 73
**Plans:** 2/2 plans complete

Plans:
- [ ] 74-01-PLAN.md — PR1: tokenizer + Tier 1 mrkdwn transforms in pkg/slack/mrkdwn.go + corpus fixtures + fuzz target + `--render=plain|mrkdwn` flag on `km-slack post` with `KM_SLACK_RENDER` env safety valve; streaming hook unchanged. (Wave 1, autonomous, 3 tasks)
- [ ] 74-02-PLAN.md — PR2: Tier 2 Block Kit builder in pkg/slack/blocks.go + additive bridge changes (SlackEnvelope.Blocks field, BlockPoster optional interface, SlackPosterAdapter.PostMessageBlocks, handler dispatch wrap) + `--render=blocks` execution path + `pkg/compiler/userdata.go _km_stream_drain` hook flip + manual end-to-end Slack verification. (Wave 2, has checkpoint, 5 tasks, depends on 74-01)

### Phase 75: Slack inbound file attachments (images, PDFs) for per-sandbox channels

**Goal:** Extend the Phase 67 Slack inbound flow so users can paste files (images, PDFs, etc.) into a per-sandbox channel/thread and reference them conversationally ("what's in this picture?"). Bridge Lambda detects `files[]` on inbound message events, downloads each file from `files.slack.com` using the bot token, and stages to S3 under `slack-inbound/<sandbox-id>/<thread_ts>/<filename>`. Sandbox-side `km-slack-inbound-poller` mirrors the staged files to `/workspace/.km-slack/attachments/<thread_ts>/<filename>` (chown sandbox), then prepends a "master prompt" wrapper to the `claude -p` turn enumerating each attachment by absolute path + MIME type so Claude reads them via its Read tool (multimodal for images, native for PDFs). Wrapper is added only when `files[]` is non-empty. Files persist for the session (matches the 30-day Slack-thread DDB TTL via S3 lifecycle on the staging prefix).

**Caps:** 25 files per inbound message, 100MB per file. Bridge enforces; over-cap files are dropped with a warning posted as a thread reply. Filenames sanitized for path safety (strip `/`, `..`, non-printable chars) but original Slack-supplied name preserved in the master-prompt wrapper.

**New Slack scope:** `files:read` (one-time re-auth via `km slack rotate-token` after re-installing the app).

**Requirements**: Phase 67 inbound flow (SQS FIFO + km-slack-inbound-poller) and Phase 67.1 ACK reaction must be in place. No new SSM, DDB, or profile-schema changes anticipated; gated under existing `notifySlackInboundEnabled`. Bridge IAM gains `s3:PutObject` on the staging prefix; sandbox role already reads `KM_ARTIFACTS_BUCKET`.

**Depends on:** Phase 67, Phase 67.1
**Plans:** 6/6 plans complete

Plans:
- [ ] 75-01-PLAN.md — Wave 1: SQS payload types (SlackFile/Attachment) + isBotLoop allow-list (file_share) + ROADMAP fix (autonomous, 3 tasks)
- [ ] 75-02-PLAN.md — Wave 2: S3FileDownloader + bridge Handle fork on len(Files)>0 + Pitfall 1/2 mitigations (autonomous, 2 tasks)
- [ ] 75-03-PLAN.md — Wave 2: Sandbox poller bash extension (S3 mirror + master-prompt wrapper + Pitfall 4 fix) (autonomous, 2 tasks)
- [ ] 75-04-PLAN.md — Wave 2: Bridge IAM s3:PutObject + memory_size 1024 + S3 lifecycle module + files:read scope checks (autonomous, 3 tasks)
- [ ] 75-05-PLAN.md — Wave 3: Bridge cold-start wiring (S3FileDownloader instantiated in main.go) (autonomous, 1 task)
- [ ] 75-06-PLAN.md — Wave 4: Docs (slack-notifications.md + CLAUDE.md) + manual UAT runbook (checkpoint, 2 tasks)

### Phase 76: km vscode rekey: rotate ed25519 keypair for an existing sandbox

**Goal:** Add `km vscode rekey <sandbox-id>` to rotate the per-sandbox VS Code Remote-SSH keypair on an already-running sandbox without `km destroy && km create`. Solves three operator pain points surfaced by Phase 73 in production:

1. **Baked-AMI relaunch carries stale authorized_keys** — `km shell --learn --ami` snapshots the EC2 instance mid-session, capturing `/home/sandbox/.ssh/authorized_keys` from the bake-source sandbox. On relaunch from that AMI, cloud-init may mark itself "done" and skip the userdata block that writes the new pubkey, leaving the old key in place. (This is exactly the failure mode Phase 56.2 fixed for cgroup setup; the architectural fix — relocating the authorized_keys write into `km-bootstrap.service` — is deferred to a follow-up phase.)
2. **Cross-laptop portability** — Phase 73 keys live on the creation machine only. An operator who wants to `km vscode start` from a different laptop currently has to manually copy `~/.km/keys/<sandbox-id>*`. Rekey gives them a path to issue themselves a fresh key without that copy.
3. **Post-incident rotation** — if a private key is suspected compromised, the operator can rotate without rebuilding the sandbox.

**Implementation:**

- New subcommand under existing `km vscode` group; mirrors keypair-generation logic from `km create` (ed25519, written to `~/.km/keys/<sandbox-id>` mode 0600 + `<sandbox-id>.pub` mode 0644).
- Atomic local replace: write the new keypair to `<path>.new`, then `os.Rename` over the old path so a partial failure doesn't brick local access.
- Push the new pubkey to the sandbox via SSM SendCommand: `cat > /home/sandbox/.ssh/authorized_keys` (overwrite, not append — matches the existing userdata write pattern); `chmod 600`; `chown sandbox:sandbox`; `restorecon -R -v /home/sandbox/.ssh` (AL2023 SELinux mandatory). No sshd restart — sshd reads authorized_keys per-connection.
- **Order matters:** push pubkey to sandbox FIRST, only replace local keypair on success. The reverse order can brick access if the SSM step fails.
- Pre-flight: refuse if the sandbox isn't in `running` state (no SSM channel). Helpful error pointing at `km resume <id>`.
- Active VS Code Remote-SSH sessions stay connected (sshd doesn't re-read authorized_keys for already-authenticated sessions); reconnect picks up the new key transparently because the local IdentityFile path under `~/.ssh/config` is unchanged.
- No userdata template change. No DDB schema change. No new SSM parameters. No infra/modules change.

**Requirements**: Phase 73 must be in place (per-sandbox keypair convention + ssh-config Host block managed in `~/.ssh/config`). Existing pre-Phase-73 sandboxes (no key on disk) should be detected and emit a clear error pointing at `km destroy && km create`.

**Depends on:** Phase 75
**Plans:** 4/4 plans complete

Plans:
- [ ] 76-00-PLAN.md — Wave 0 stub seeding: append 16 TestVSCodeRekey_* failing-stub tests to vscode_test.go
- [ ] 76-01-PLAN.md — Wave 1: register `km vscode rekey` cobra command + four pre-flight gates (EC2 running, lock with --force, SSM probe via parseVSCodeStatus); turn 8 stubs green
- [ ] 76-02-PLAN.md — Wave 2: complete runVSCodeRekey (key classification, prompt, GenerateAndWrite to scratch, SSM install + readback verify, atomic .pub-first rename, output markers); turn remaining 8 stubs green
- [ ] 76-03-PLAN.md — Wave 2 (parallel): documentation in CLAUDE.md and docs/vscode.md (Rotating a sandbox key section, three pain-point scenarios, runbook table)

### Phase 77: failed sandbox discoverability — persist failure_reason in DDB and km logs Lambda fallback

**Goal:** [To be planned]
**Requirements**: TBD
**Depends on:** Phase 76
**Plans:** 5/5 plans complete

Plans:
- [x] TBD (run /gsd:plan-phase 77 to break down) (completed 2026-05-15)

### Phase 78: km agent auth — SSM-mediated OAuth login for claude and codex CLIs inside sandboxes (paste-code for claude, port-forward 1455 for codex)

**Goal:** Operator can run `km agent auth <sandbox> [--claude | --codex]` to mediate the underlying CLI's OAuth login over SSM. Default `--claude` runs `claude auth login` interactively in the sandbox via SSM session (operator pastes the OAuth code into the SSM-attached terminal); `--codex` opens an SSM port-forward `localhost:1455 ↔ sandbox:1455` so codex's hardcoded callback URL flows back through the tunnel. Success signal is the credentials file (`~/.claude/.credentials.json` or `~/.codex/auth.json`) appearing on the sandbox. `km shell --no-bedrock` and `km agent run --no-bedrock` print a clear hint pointing at `km agent auth` when credentials are missing (no silent auto-bootstrap).
**Requirements**: AUTH-01, AUTH-02, AUTH-03, AUTH-04, AUTH-05, AUTH-06, AUTH-07, AUTH-08, AUTH-09, AUTH-10, AUTH-11, AUTH-12, AUTH-13, INT-01, INT-02 (phase-local IDs defined in 78-VALIDATION.md — no formal REQUIREMENTS.md mapping; this is operator-tooling/UX)
**Depends on:** Phase 77
**Plans:** 2/2 plans complete

Plans:
- [ ] 78-01-PLAN.md — Wave 1 (`--claude` paste-code path + missing-credentials hint in `--no-bedrock` consumers; AUTH-01..07, AUTH-11..13)
- [ ] 78-02-PLAN.md — Wave 2 (`--codex` port-forward 1455 path; AUTH-08..10, INT-01)

### Phase 79: km-presence daemon — replace bash _km_heartbeat with single systemd-managed liveness service checking login shells (utmp), attached tmux clients, recent inbound email/slack, and headless agent process

**Goal:** Replace the per-shell bash `_km_heartbeat` function (which orphans subshells and pegs the IDLE column at full timeout — observed on sandbox L1 2026-05-10) with a single root-owned systemd daemon (`km-presence.service`) that ticks every 60s, OR's five concrete liveness signals (utmp, tmux clients, recent email, recent Slack, headless agent process), and emits `source:"presence"` heartbeat events to the existing `/run/km/audit-pipe`. Migration follows the Phase 63/67/68/73 pattern: `make build && make sidecars && km init --sidecars`; existing sandboxes keep bash heartbeats until `km destroy && km create`.
**Requirements**: PHASE-79-PRESENCE-DAEMON (tactical bug fix — no formal REQ-IDs; goal-backward must_haves drive verification)
**Depends on:** Phase 78
**Plans:** 6/6 plans complete

Plans:
- [x] 79-00-PLAN.md — Wave 0 stub seeding (cmd/km-presence skeleton + commandRunner seam + 13 failing test stubs + doctor_presence.go stub + 3 doctor test stubs)
- [x] 79-01-PLAN.md — Wave 1 daemon implementation (5 signal checks + tick + emit + 60s ticker main loop; turns 13 stubs GREEN)
- [x] 79-02-PLAN.md — Wave 1 userdata.go edits (REMOVE _km_heartbeat lines 1056-1080, ADD km-presence S3 fetch + systemd unit heredoc + slack-inbound stamp touch + enable in both eBPF/proxy branches; 5 regression tests)
- [x] 79-03-PLAN.md — Wave 1 Makefile sidecar pipeline (km-presence build line + S3 upload line in `sidecars` target; build-only line in `build-sidecars`)
- [x] 79-04-PLAN.md — Wave 2 doctor check `presence_daemon_healthy` (CloudWatch FilterLogEvents for source:"presence" within 5min staleness threshold; WARN-not-ERROR; turns 3 stubs GREEN; registered in buildChecks)
- [x] 79-05-PLAN.md — Wave 3 closeout (CLAUDE.md docs section + populate VALIDATION.md Per-Task Verification Map + BLOCKING operator UAT for bug-fix regression on live sandbox)

### Phase 79.1: audit-pipe FIFO recreation on resumed sandboxes — systemd-tmpfiles drop-in + audit-log self-heal when path exists as non-FIFO regular file (INSERTED)

**Goal:** Fix the Phase 79 stop+resume integration gap so that on EC2 stop+resume cycles (where `/run` tmpfs is wiped and cloud-init does NOT re-run), `/run/km/audit-pipe` is recreated as a correctly-owned FIFO before km-presence's first heartbeat can stamp the path as a regular file (Layer 1: `/usr/lib/tmpfiles.d/km.conf` with `p+`), and if the path somehow ends up wrong-typed, the audit-log sidecar self-heals on startup (Layer 2: `openAuditPipeWithRetry` unlinks + mkfifos before opening). Validated end-to-end by a live `km pause`+`km resume` UAT proving `journalctl -u km-audit-log` shows `reading from audit pipe` (not `permission denied`) and `km doctor` reports `✓ Presence daemon healthy`.
**Requirements**: L1-TMPFILES, L1-ORDER, L1-MODE, L2-SELFHEAL, L2-EXISTING-FIFO, UAT-RESUME (synthetic IDs; tactical bug fix, no formal v1 REQ-IDs)
**Depends on:** Phase 79
**Plans:** 4/4 plans complete

Plans:
- [ ] 79.1-01-PLAN.md — Wave 0 RED test stubs: 3 failing userdata tests for Layer 1 (tmpfiles.d drop-in present/ordered/correct-mode) + 1 failing audit-log sub-test for Layer 2 (path-exists-as-regular-file self-heal)
- [ ] 79.1-02-PLAN.md — Wave 1 Layer 1 implementation: insert `/usr/lib/tmpfiles.d/km.conf` heredoc in `pkg/compiler/userdata.go` between the existing mkfifo block and the km-audit-log.service unit heredoc (parallel with 79.1-03)
- [ ] 79.1-03-PLAN.md — Wave 1 Layer 2 implementation: replace the stat+mkfifo guard in `sidecars/audit-log/cmd/main.go::openAuditPipeWithRetry` with a three-branch self-heal (absent → mkfifo; FIFO → no-op; wrong-type → unlink+mkfifo) (parallel with 79.1-02)
- [x] 79.1-04-PLAN.md — Wave 2 closeout: CLAUDE.md "Phase 79.1 follow-up" subsection under Presence daemon section + BLOCKING live AWS UAT (km create + km pause + km resume + journalctl + km doctor + ls -la) + write phase SUMMARY.md (completed 2026-05-16)

### Phase 80: km cluster — cross-account IRSA for k8s integrations

**Goal:** Ship `km cluster add/list/rm` that provisions an IAM role in the klanker AWS account with a cross-account trust policy referencing a k8s cluster's OIDC provider in a *different* AWS account. K8s pods authenticate via projected service-account tokens (no static keys). Refactor `create-handler` and the new `cluster-irsa` module to share a single `km-operator-policy` Terraform module so Lambda and IRSA roles can never drift. Phase closes when full `km cluster add --dry-run=false` against the `klanker-application` profile creates the role, persists to `km-config.yaml`, and `km cluster rm` cleanly tears it down.
**Requirements**: operator-feature-80 (synthetic ID — operator-facing feature, not in v1 REQUIREMENTS.md list)
**Depends on:** Phase 79
**Plans:** 6/6 plans complete

Plans:
- [x] 80-01-PLAN.md — Wave 0 test scaffolds (cluster_test.go + config_clusters_test.go skeletons; un-skipped by Plans 80-04/80-05)
- [x] 80-02-PLAN.md — Extract km-operator-policy/v1.0.0/ shared module from create-handler; 14 moved blocks; zero-net-diff terragrunt plan gate (BLOCKING checkpoint)
- [x] 80-03-PLAN.md — New cluster-irsa/v1.0.0/ Terraform module with cross-account trust policy + km_operator_policy module consumption
- [x] 80-04-PLAN.md — ClusterConfig struct + Config.Clusters field + viper merge wiring in internal/app/config/config.go
- [x] 80-05-PLAN.md — km cluster CLI (add/list/rm) in internal/app/cmd/cluster.go; generateClusterHCL, persistClustersConfig, region.hcl bootstrap, idempotency, handoff output
- [x] 80-06-PLAN.md — Phase-close integration test against klanker-application (BLOCKING checkpoint) + CLAUDE.md Cross-account k8s integrations section + closeout SUMMARY

### Phase 80.1: Auto-detect existing OIDC provider in cluster-irsa module, supporting same-account IRSA without manual flags (INSERTED)

**Goal:** Make `km cluster add` Just Work whether the EKS cluster lives in the same AWS account as the klanker install or in a different account. Today the `cluster-irsa` module unconditionally creates a new `aws_iam_openid_connect_provider` mirroring the cluster's issuer URL, which fails with `EntityAlreadyExists` whenever an OIDC provider for that URL is already registered in the target account (the same-account case, the second-cluster-irsa-stack-against-same-EKS-issuer case, and the "EKS auto-registered the provider for us" case). Add a `register_oidc_provider` variable to the module (resource is `count = var.register ? 1 : 0`, a `data "aws_iam_openid_connect_provider"` lookup covers the false branch), and have `km cluster add` auto-detect by calling `aws iam list-open-id-connect-providers` against the target account before generating the terragrunt.hcl. Operator can override with `--register-oidc-provider=true|false`. Phase closes when (a) same-account `km cluster add` against a cluster whose provider is already registered succeeds without `EntityAlreadyExists` and surfaces the existing provider ARN as the trust Principal, (b) cross-account `km cluster add` against a brand-new issuer still registers a fresh provider (existing behavior preserved), and (c) running `km cluster add` twice against the same EKS issuer (multi-stack-per-cluster) succeeds for both invocations.
**Requirements**: operator-feature-80 (extends Phase 80 — same synthetic ID)
**Depends on:** Phase 80
**Plans:** 5/5 plans complete

Plans:
- [ ] 80.1-01-PLAN.md — Wave 1 test scaffold: mockOidcLister + two pending test stubs in cluster_test.go
- [ ] 80.1-02-PLAN.md — Wave 1 km-operator-policy IAM permissions for OIDC provider lifecycle
- [ ] 80.1-03-PLAN.md — Wave 2 cluster-irsa module: moved block + count-gated resource/data source + updated trust policy + outputs
- [ ] 80.1-04-PLAN.md — Wave 2 cluster.go: OidcProviderLister interface + auto-detect + --register-oidc-provider flag + template placeholder + activate tests
- [ ] 80.1-05-PLAN.md — Wave 3 docs update (CLAUDE.md + docs/k8s/README.md) + operator-supervised phase-close integration test

### Phase 81: GitHub Actions self-hosted runner — sandbox registers as runner for declared repos

**Goal:** A klanker sandbox can register as a long-lived self-hosted GitHub Actions runner for one or more repos declared in its profile (`spec.sourceAccess.github.runner.enabled: true`). Per-repo registration is best-effort (failures don't block km create, recoverable via `km runner reattach`). Clean teardown on `km destroy` via belt-and-suspenders Lambda DELETE (no ghost runners). New centralised token Lambda mints registration / removal tokens from the existing GitHub App credentials. Workflow jobs execute under the sandbox's full policy boundary (eBPF, proxy MITM, allowedDomains, budget, audit). EC2-only for v1; Docker substrate out of scope (no systemd). Migration follows the Phase 63/67/68/73/79/80 pattern (make build → make sidecars → km init --sidecars → terragrunt apply for the new Lambda → km init), plus one-time GitHub App re-install with `administration: write`.
**Requirements**: RUNNER-PROFILE-SCHEMA, RUNNER-GITHUB-API, RUNNER-TOKEN-LAMBDA, RUNNER-LAMBDA-IAM, RUNNER-SIDECAR, RUNNER-SYSTEMD-UNITS, RUNNER-USERDATA-WIRING, RUNNER-NETWORK-ALLOWLIST, RUNNER-INSTANCE-ROLE, RUNNER-CLI, RUNNER-CREATE-WIRING, RUNNER-DESTROY-TEARDOWN, RUNNER-DOCTOR, RUNNER-PRESENCE-SIGNAL, RUNNER-DOCS, RUNNER-MIGRATION
**Depends on:** Phase 80
**Plans:** 6 plans

Plans:
- [ ] 81-01-PLAN.md — Profile schema (RunnerSpec + JSON schema) + semantic validation rules + pkg/github/runner.go API wrappers with httptest unit tests (Wave 0 contract surface)
- [ ] 81-02-PLAN.md — cmd/km-actions-runner-token Lambda (register/remove/delete/list dispatch) + infra/modules/actions-runner-token/v1.0.0 Terraform module (Function URL with AuthType: AWS_IAM) + live terragrunt unit + Makefile build-lambdas wiring (Wave 1)
- [ ] 81-03-PLAN.md — sidecars/km-actions-runner Cobra binary (register/remove subcommands, sigv4 Lambda invoke, config.sh wrapper) + Makefile sidecar target (Wave 1)
- [ ] 81-04-PLAN.md — Compiler userdata wiring (heredoc systemd template units + /etc/km/runner-repos.json + binary download), security.go DNS allowlist auto-injection, compiler.go IAM session policy lambda:InvokeFunctionUrl statement (Wave 2)
- [ ] 81-05-PLAN.md — internal/app/cmd/runner.go (km runner status|reattach|detach) + km destroy belt-and-suspenders Lambda DELETE + km create runtime injection of KM_ACTIONS_RUNNER_TOKEN_URL with visible-stderr per SLCK-11 lesson (Wave 3)
- [ ] 81-06-PLAN.md — Four km doctor checks (actions_runner_app_perms ERROR + register_failures/ghosts/drift WARN) + km-presence sixth signal (Runner.Listener pgrep extension) + docs/github.md Self-hosted Actions runners section + CLAUDE.md Phase 81 section + operator checkpoint (migration + GitHub App re-install + end-to-end smoke test) (Wave 4)

### Phase 82: Multi-instance resource_prefix isolation

**Goal:** Close the gap between CLAUDE.md's 'multiple km installs per AWS account via resource_prefix' promise and reality — fix 3 hard Terraform blockers (SES rule-set, email-handler S3 IAM, ECS SSM ARN), 1 configure-flow footgun, 4 silent km-* fallbacks, add the km:resource-prefix install-discriminator tag at bake-time + via terraform + via a one-time km doctor --backfill-tags retro-sweep, and tag-filter doctor's cross-install destruction surfaces.
**Requirements**: None — operator-driven phase (CONTEXT.md `<decisions>` block enumerates the locked deliverables in lieu of requirement IDs)
**Depends on:** Phase 81
**Plans:** 10/10 plans complete

Plans:
- [ ] 82-01-PLAN.md — Configure preserve-on-re-run + --reset-prefix flag (Wave 1, Go-only)
- [ ] 82-02-PLAN.md — Hard-fail 4 silent km-* fallbacks (Wave 1, Go-only)
- [ ] 82-03-PLAN.md — KMBakeTags emits km:resource-prefix tag (Wave 1, Go-only)
- [ ] 82-04-PLAN.md — Doctor tag-filter: ListBakedAMIs + checkOrphanedEC2 (Wave 2, Go)
- [ ] 82-05-PLAN.md — km doctor --backfill-tags command + cross-install guard (Wave 2, Go)
- [ ] 82-06-PLAN.md — SES module resource_prefix variable + rule-set rename (Wave 3, Terraform)
- [ ] 82-07-PLAN.md — Email-handler state_prefix variable (Wave 3, Terraform)
- [ ] 82-08-PLAN.md — Three ECS modules use var.km_label for SSM ARN (Wave 3, Terraform)
- [ ] 82-09-PLAN.md — Tag every sandbox-creating module with km:resource-prefix (Wave 3, Terraform)
- [ ] 82-10-PLAN.md — Apply Wave 3 + km doctor --backfill-tags + docs updates (Wave 4, OPERATOR CHECKPOINT — NOT autonomous)

### Phase 82.1: Multi-instance polish — bare-path configure preserve + service_hcl literal + SES active-rule-set handoff (INSERTED)

**Goal:** Close the three remaining multi-instance isolation gaps left after Phase 82: (1) the bare `km configure` invocation (no `--output-dir`) silently skips the preserve-on-rerun branch at `configure.go:145`, so operators rotating an unrelated field can still retarget their config at the wrong install; (2) `pkg/compiler/service_hcl.go:784` still emits the literal `"km-slack-stream-messages"` table-name fallback (Phase 82-02 deferred); (3) `aws_ses_active_receipt_rule_set` is a per-account/region singleton, so running `km init` under a second prefix deactivates the first install's inbound email path — Phase 82 fixed the name (B1) but not the activation handoff. Phase closes when (a) bare `km configure` preserves an existing non-default prefix without `--output-dir`, (b) `service_hcl.go:784` derives the stream-messages table name from the prefix the same way `userdata.go` does, and (c) running `km init --dry-run=true` under a second prefix does NOT plan to switch the SES active rule set (operator opt-in, documented handoff procedure, or split rule-set per prefix — design choice resolved by the planner via CONTEXT.md).
**Requirements**: CONFIG-PRESERVE-BARE, SVCHCL-PREFIX-FALLBACK, SES-ACTIVE-RULESET (synthetic IDs; gap closure from Phase 82-VERIFICATION.md + operator UAT discovery)
**Depends on:** Phase 82
**Plans:** 3/3 plans complete

Plans:
- [ ] 82.1-01-PLAN.md — Bare-path configure preserve: extend outputDir guard to findRepoRoot() fallback (Wave 1, TDD, Go-only)
- [ ] 82.1-02-PLAN.md — service_hcl.go stream-table prefix-aware derivation: replace literal at line 784 (Wave 1, TDD, Go-only)
- [ ] 82.1-03-PLAN.md — SES activate_rule_set opt-in variable: count-gate aws_ses_active_receipt_rule_set + operator checkpoint (Wave 2, Terraform + docs) — **SUPERSEDED by Phase 84**

### Phase 83: Add km event command for operator-controlled EventBridge

**Goal:** Operators can declare durable, cron-driven (and event-pattern-driven) platform actions in git-tracked YAML manifests deployed via terragrunt, with a singleton km-runner Lambda routing scheduled `km <command>` invocations. Preserves ad-hoc `km at` for one-shot operations and removes recurring support from `km at` (hard cut).
**Requirements**: operator-feature-83 (synthetic ID — no v1 REQUIREMENTS.md entries; tracked under "Operator-controlled EventBridge")
**Depends on:** Phase 81
**Plans:** 7 plans

Plans:
- [ ] 83-01-wave0-test-scaffolds-PLAN.md — Wave 0 Nyquist test infrastructure (4 test files + mockEventRunner + pkg/events/testdata fixtures)
- [ ] 83-02-foundations-pkg-events-and-config-PLAN.md — pkg/events package (manifest schema + at()→cron conversion + JSON Schema) + config.EventConfig + km-operator-policy widened to custom bus
- [ ] 83-03-terraform-modules-bus-and-rule-PLAN.md — Two new Terraform modules: operator-event-bus/v1.0.0/ (singleton bus + 7-day archive + km-runner Lambda + IAM) and operator-event-rule/v1.0.0/ (per-rule resources)
- [ ] 83-04-km-runner-lambda-binary-and-makefile-PLAN.md — cmd/km-runner Lambda binary (handler + main) + Makefile build-km-runner target (zips bootstrap + km together)
- [ ] 83-05-km-event-cli-PLAN.md — internal/app/cmd/event.go Cobra tree (add/list/rm/apply) modeled on Phase 80 km cluster add + EventRunner seam + HCL template + PersistEventsConfig + root.go registration
- [ ] 83-06-cleanup-km-init-doctor-PLAN.md — km at --cron strip + recurring NL reject + operator-event-bus in km init regionalModules + km-runner in lambdaBinaries + km doctor operator_event_rules_healthy WARN check
- [ ] 83-07-docs-and-uat-PLAN.md — docs/operator-events.md operator runbook + CLAUDE.md Phase 83 section + 12-step operator UAT against real AWS + STATE.md/ROADMAP.md closeout

### Phase 84: SES per-install rule namespacing via operator address prefix (supersedes 82.1's activate_rule_set)

**Goal:** Replace Phase 82.1's `activate_rule_set` handoff mechanism with per-install address namespacing so a second `km init` against the same AWS account/region never touches the first install's inbound email path. The operator inbound address becomes `operator-${resource_prefix}@sandboxes.${domain}` (e.g. `operator-kph@`, `operator-rg@`), and the SES receipt rule set is promoted to account-shared state owned by a new foundation/bootstrap layer (`infra/modules/ses-shared-rule-set/v1.0.0/`) rather than a per-install regional resource. Each install's regional `ses/` module shifts to *only* adding prefix-named rules (`${prefix}-operator-inbound`, `${prefix}-sandbox-catchall`) to the always-active shared rule set; `km uninit` removes only this install's rules and leaves the rule set + sibling rules intact. Phase 82.1's `activate_rule_set` variable, `aws_ses_active_receipt_rule_set` resource, `KM_SES_ACTIVATE_RULESET` env-var opt-out path, and CLAUDE.md runbook are deleted entirely (hard removal — no live operator inbox uses the old design). Sandbox addresses stay flat (`{sandbox-id}@`); only the operator address carries the prefix. `km configure` derives `KM_OPERATOR_EMAIL` from the prefix; the email-handler Lambda resolves `operator-${prefix}@` for inbound dispatch; `km doctor` gains a check for orphaned rules whose prefix is not present in the local `km-config.yaml` (WARN-level, helps cleanup after a failed uninit on a partner install).

Phase closes when (a) a second install can run `km init --dry-run=false` against an account where another install already has SES configured, without any opt-out env var, and observe no AWS API call against the first install's resources; (b) operator-bound email lands at `operator-${prefix}@` for both installs and the email-handler Lambda routes correctly; (c) `km uninit` on either install removes only its own `${prefix}-*` rules and leaves the rule set + the sibling install's rules untouched; (d) the foundation `ses-shared-rule-set` module bootstraps the rule set exactly once per account/region and is idempotent on re-apply; (e) Phase 82.1's `activate_rule_set` + `KM_SES_ACTIVATE_RULESET` paths are gone from the codebase (grep returns zero hits in non-archive directories); (f) `km doctor` reports `✓ SES rules healthy` when all rules map to known prefixes and `⚠ orphan SES rules: <list>` when they don't.

**Requirements:** SES-PREFIX-ADDRESS, SES-SHARED-RULESET, SES-PER-INSTALL-RULES, SES-82.1-REMOVAL, SES-CONFIGURE-WIRING, SES-HANDLER-LOOKUP, SES-DOCTOR-ORPHANS (synthetic IDs — supersedes SES-ACTIVE-RULESET from Phase 82.1)
**Depends on:** Phase 82.1
**Plans:** 9/10 plans executed

Plans:
- [ ] 84-01-wave0-test-scaffolds-PLAN.md — Wave 0 Nyquist failing-test infrastructure (11 stubs + Makefile grep gate)
- [ ] 84-02-foundation-ses-shared-rule-set-module-PLAN.md — New foundation Terraform module + live wiring (rule set + active + domain identity + DKIM + MX + verification)
- [ ] 84-03-regional-ses-v2-module-PLAN.md — New regional ses/v2.0.0 module owning prefix-named rules only + live dir cutover to v2.0.0
- [ ] 84-04-km-configure-operator-email-derivation-PLAN.md — deriveOperatorEmail helper + runConfigure integration + --reset-prefix clears stored email
- [ ] 84-05-userdata-and-ses-pkg-operator-literal-PLAN.md — Replace operator@ literals in pkg/compiler/userdata.go + pkg/aws/ses.go; export KM_OPERATOR_EMAIL in userdata env files
- [ ] 84-06-email-handler-recipient-verification-PLAN.md — email-create-handler verifies operator-${prefix}@; silent-drops foreign; updates outbound From
- [ ] 84-07-km-bootstrap-shared-ses-and-doctor-check-PLAN.md — km bootstrap --shared-ses (auto-detect via SESIdentityLister) + km doctor checkSESRules + go.mod aws-sdk-go-v2/service/ses
- [ ] 84-08-phase-82.1-hard-removal-and-grep-gate-PLAN.md — Delete activate_rule_set from infra + Phase 82.1 sections from CLAUDE.md + OPERATOR-GUIDE.md; wire grep gate into make test
- [ ] 84-09-docs-claude-md-operator-guide-and-roadmap-PLAN.md — CLAUDE.md Phase 84 section + OPERATOR-GUIDE.md upgrade runbook + ROADMAP/REQUIREMENTS closeout
- [ ] 84-10-operator-uat-checkpoint-PLAN.md — Operator UAT against real AWS + STATE/ROADMAP closeout (NOT autonomous)


### Phase 84.1: SES upgrade safety — gap closure for in-place v1.0.0 → v2.0.0 cutover (INSERTED)

**Goal:** Close the 8 gaps diagnosed by Phase 84's UAT so an operator can upgrade an existing Phase 82.x install to Phase 84 without manual recovery: (1) `km bootstrap` exports the full terragrunt env var set, (2) `km bootstrap` is idempotent across re-runs, (3) foundation safely takes ownership of pre-existing v1.0.0 resources (domain identity, DKIM, MX, verification TXT, active rule set pointer) without data loss during regional cutover, (4) wedged `terragrunt apply` surfaces as a timeout error instead of hanging silently, (5) state-digest mismatch recovery is documented and/or detected by `km doctor`. Outcome: re-running the UAT closures (a)/(c)/(d) that were skipped in Phase 84 all pass.

**Requirements**: GAP-1, GAP-2, GAP-3, GAP-4, GAP-5, GAP-6, GAP-7, GAP-8, DRIFT-A, DRIFT-B, DRIFT-C (synthetic IDs — gap-derived from Phase 84 UAT diagnosis 84-10-UAT.md)
**Depends on:** Phase 84
**Plans:** 5/5 plans complete (closed 2026-05-17 — UAT passed-with-caveats; empirical re-run deferred to Phase 84.2)

Plans (4-wave structure after plan-checker rev 2 — C-NEW-1 serialized init.go/bootstrap.go file conflicts):
- [x] 84.1-01-PLAN.md — Unified ExportTerragruntEnvVars helper closing GAP-1 + GAP-7 (Wave 1, TDD, Go-only, no deps)
- [x] 84.1-03-PLAN.md — km doctor state-lock-digest mismatch detection + Remediation closing GAP-8 (Wave 1, TDD, Go-only, no deps — parallel-safe with 84.1-01)
- [x] 84.1-02-PLAN.md — Terragrunt runner per-module timeout + quiet-mode heartbeat closing GAP-4 + GAP-5 + bounded km bootstrap defaultApplyTerragrunt (H6) (Wave 2, TDD, Go-only — depends on 84.1-01 per C1 file-conflict fix)
- [x] 84.1-04-PLAN.md — Foundation register_*=manage semantics + import/removed blocks for safe in-place v1.0.0→v2.0.0 cutover closing GAP-2 + GAP-3 + GAP-6 (Wave 3, Terraform + Go — depends on 84.1-01 + 84.1-02 per C-NEW-1 file-conflict fix on init.go/bootstrap.go)
- [x] 84.1-05-PLAN.md — Operator UAT (3 skipped Phase 84 closures + DRIFT-A/B/C) + OPERATOR-GUIDE.md state-digest recovery + CLAUDE.md Phase 84.1 section + Phase 84 SUMMARY drift notes (Wave 4, OPERATOR CHECKPOINT — NOT autonomous, depends on all) — closed `passed-with-caveats` 2026-05-17 (minimal UAT scope: Step 1+2 only, GAP-6 structurally verified; Steps 3-9 + DRIFT-B/C deferred to Phase 84.2)

### Phase 84.2: `km init --plan` flag with destroy-class gate (INSERTED)

**Goal:** Make `km init --plan` and `km bootstrap --shared-ses --plan` run a real `terragrunt plan` per module with a curated destroy-class safety gate, so the next Phase-84-style incident is caught before apply. Today `km init --dry-run=true` (`internal/app/cmd/init.go:276`) is a static documentation print — it never invokes `terragrunt plan`, never touches AWS, never shows resource diffs. Phase 84.1 closes the SES-specific upgrade gaps but does not add a generalizable plan-before-apply mechanism; any future destructive change in another module lands with the same blind spot. This phase adds a new `--plan` flag (independent of `--dry-run`, never applies) that loops `regionalModules()` in order, captures each plan via `terragrunt plan -out=<file>` + `terraform show -json <file>`, parses the JSON into a structured report, and runs a gate over a compiled-in `ProtectedTypes` allowlist (initial set: `aws_ses_domain_identity`, `aws_ses_domain_dkim`, `aws_ses_active_receipt_rule_set`, `aws_ses_receipt_rule_set`, `aws_route53_record`, `aws_s3_bucket`, `aws_s3_bucket_policy`, `aws_dynamodb_table`, `aws_kms_key` — each entry annotated with the incident that motivated it). Any destroy or replace of a protected type exits non-zero with a formatted trip block; operator override is `--i-accept-destroys` (per-invocation, never persisted, does NOT auto-apply — only clears the `--plan` exit code). Bootstrap gets the same flag for symmetric coverage of the foundation path that hit Phase 84 Gaps 2/3/6. Output model: sequential execution, per-module one-line summary by default, `--verbose` streams the full plan text, trip block always prints in full regardless of verbose. The gate package (`pkg/terragrunt/planreport/`) is pure JSON-in/decision-out, no terragrunt or AWS imports, trivially unit-testable via captured plan fixtures.

Phase closes when (a) `km init --plan` runs cleanly against the operator's already-Phase-84 account with all-zero counts; (b) a synthetic destroy injected into a regional module trips the gate with the correct address + module name + action and exits 1; (c) the same scenario with `--i-accept-destroys` exits 0 but still prints the trip list; (d) `km bootstrap --shared-ses --plan` exhibits the same three behaviors against the foundation module; (e) the `planreport` package has table-driven unit tests covering clean/add-only/destroy-trip/replace-trip/parse-fail and the override toggle; (f) `--plan` combined with `--sidecars` or `--lambdas` is rejected as mutually exclusive; (g) skipped modules (missing env-vars) are reported as `[skip: KM_FOO not set]` and do not count toward the gate; (h) plan failures (auth, syntax) stop the loop with module-named stderr and exit non-zero.

**Design spec:** `docs/superpowers/specs/2026-05-16-km-init-plan-flag-and-destroy-class-gate-design.md`

**Requirements:** PLAN-FLAG, BOOTSTRAP-PLAN-PARITY, DESTROY-CLASS-GATE, PROTECTED-TYPES-LIST, ACCEPT-DESTROYS-OVERRIDE, PLAN-OUTPUT-FORMAT, PLAN-ERROR-HANDLING (synthetic IDs — derived from the design spec's Decisions + Architecture sections)
**Depends on:** Phase 84.1 (specifically 84.1-01 ExportTerragruntEnvVars helper; 84.1-02 runner timeouts are desirable but not strictly blocking)
**Plans:** 9/9 plans complete

- [ ] 84.2-01-PLAN.md — Wave 0 test scaffolding: planreport package tests + 5 JSON fixtures, runner test extension, init_plan_test.go + bootstrap_plan_test.go (per Nyquist — Wave 0)
- [ ] 84.2-02-PLAN.md — pkg/terragrunt/planreport/ package: protected.go (9-entry list) + report.go (Parse) + gate.go (Evaluate) — pure logic, no terragrunt/AWS deps (Wave 1)
- [ ] 84.2-03-PLAN.md — pkg/terragrunt/runner.go extension: PlanWithOutput + ShowPlanJSON + Build* helpers, inherits Phase 84.1-02 runBounded (Wave 1, parallel with 02)
- [ ] 84.2-04-PLAN.md — internal/app/cmd/init.go wiring: --plan + --i-accept-destroys flags, runInitPlan + RunInitPlanWithRunner + planModule/summarizeReport/printTripBlock/printAggregateSummary helpers (Wave 2)
- [ ] 84.2-05-PLAN.md — internal/app/cmd/bootstrap.go wiring: same flags on --shared-ses, runBootstrapSharedSESPlan, reuses Plan 04 helpers (Wave 2, parallel with 04)
- [x] 84.2-06-PLAN.md — Docs + skill updates: skills/init/SKILL.md (corrects always-wrong --dry-run=true claim), OPERATOR-GUIDE.md (Phase 84.2 runbook), CLAUDE.md, km doctor footer Tip (Wave 3) (completed 2026-05-17)
- [ ] 84.2-07-PLAN.md — Operator UAT: 7 manual scenarios on real AWS (clean + synthetic destroy + override for both init and bootstrap + mutual-exclusion + skip + plan-failure) — OPERATOR CHECKPOINT, NOT autonomous (Wave 4)
- [ ] 84.2-08-PLAN.md — Gap closure: add aws_ses_receipt_rule to ProtectedTypes, new gate test + fixture, upgrade vacuous cmd-level behavioral tests to real assertions (Wave 1, autonomous)
- [ ] 84.2-09-PLAN.md — Gap closure UAT: re-run Scenarios 2, 3, 4b, 4c + validation doc prose corrections (GAP-2/GAP-3) — OPERATOR CHECKPOINT, depends on 84.2-08 (Wave 2)

### Phase 84.3: Second-install bootstrap UX — wrapper-level fixes (INSERTED)

**Goal:** Make a fresh `git clone` + `km configure` + the canonical bootstrap sequence (`km bootstrap --dry-run=false` → `km bootstrap --shared-ses --dry-run=false` → `km init --dry-run=false`) succeed end-to-end on a shared-account install (e.g. a second prefix in an account where another `km` install already exists) without requiring the operator to hand-export env vars, hand-derive bucket names, hand-edit `km-config.yaml`, or read undocumented config-loading order rules — assuming Phase 84.4 has fixed the underlying module-source hard-coded `km-` prefixes that make multi-install module-level resources collide. This phase covers the eight wrapper-level (km binary + km-config.yaml + km commands) friction points surfaced during the 2026-05-17 `klanker-maker-kph` → `klanker-maker-whereiskurt` UAT after Phase 84.2 closed: (1) `tf-${prefix}-state-${region}` collides with a globally-registered S3 name, terragrunt errors with cryptic 403 HeadBucket only at first apply — `km configure` should HeadBucket-check the proposed state_bucket and re-prompt on 403 with an account-ID-suffix recommendation; (2) `km bootstrap` defaults to `--dry-run=true` and the dry-run text claims "would run: terragrunt plan" when the apply path actually runs `terragrunt apply` — fix the dry-run banner text to match the real action; (3) `ExportTerragruntEnvVars` (init.go:776+) silently honors stale `KM_*` shell env vars over the freshly-loaded `km-config.yaml`, hiding config drift across shell sessions — emit a single-line WARN per drifted env var while still deferring to the env (no behavior change, just visibility); (4) `km init --plan` against a never-applied install fails on the second module (efs) because `infra/live/<region>/efs/terragrunt.hcl` reads `${get_terragrunt_dir()}/../network/outputs.json` at plan-time, but that file is only written by network's apply — chicken-and-egg: plan can't see upstream outputs that don't exist yet — `--plan` should emit `[skip: depends on <upstream>/outputs.json — apply <upstream> first]` and exit 0 with a clean partial-coverage summary; (5) `km configure` writes the literal placeholder string `<prefix>-artifacts-12345678` into `km-config.yaml`'s `artifacts_bucket` field (copied verbatim from `km-config.example.yaml`), so on first apply `km init` tries to upload sidecars to a non-existent bucket and emits a warn-not-fail `NoSuchBucket` error — should derive `${prefix}-artifacts-${account_id}` automatically (matching the `tf-${prefix}-state-${region}` derivation pattern that DOES work); (6) the artifacts bucket is created by `ensureArtifactsBucket` at `bootstrap.go:1411`, but only from the plain `runBootstrap` path (line 1345 — the SCP/KMS/artifacts workflow) — NOT from `runBootstrapSharedSES`. Operators easily skip the plain `km bootstrap --dry-run=false` step thinking `--shared-ses` covers everything — `km configure` should finale with the exact three-command sequence and `km init` should hard-fail (not warn) when the artifacts bucket doesn't exist with a message pointing at the missing bootstrap step. Consider also adding `km bootstrap --all` that runs both subflows in order; (7) operators recovering from a partial bootstrap must drop to direct `terragrunt import` outside the km wrapper, and km provides no helper to export the 12+ `KM_*` env vars (KM_RESOURCE_PREFIX, KM_REGION, KM_REGION_LABEL, KM_DOMAIN, KM_EMAIL_SUBDOMAIN, KM_ROUTE53_ZONE_ID, KM_ACCOUNTS_ORGANIZATION, KM_ACCOUNTS_DNS_PARENT, KM_ACCOUNTS_APPLICATION, KM_ARTIFACTS_BUCKET, KM_OPERATOR_EMAIL, AWS_PROFILE) that `site.hcl` `get_env()` reads — the user missed `KM_ACCOUNTS_ORGANIZATION` first try, producing a cryptic `arn:aws:iam:::role/km-org-admin` AssumeRole error (resolved role_arn had an empty account-ID segment; the actual error message blamed permissions, not the missing env var). Add `km env` (or `km env --export`) that prints the canonical export block to stdout, so `eval $(km env)` is a one-liner that sets up the operator shell for any direct terragrunt work; (8) `km bootstrap`'s status header displays `Organization account: (not set)` / `DNS parent account: (not set)` / `Application account: ` (empty) even when `km-config.yaml`'s `accounts:` block has all three populated, and the same config-load path produced the correct values on a previous invocation in the same shell. The mismatch correlates with `KM_ACCOUNTS_*` env vars being set between invocations — viper's env-var-vs-yaml precedence interaction is silently shadowing the yaml-loaded account IDs and resolving them to empty. Bootstrap then silently "Skips SCP deployment — no organization account configured" even though the SCP target IS configured in yaml. Make the config-display banner the source of truth (warn if any account ID resolves to empty post-load), and audit viper bindings to ensure `accounts.organization` etc. yaml keys take precedence over partial/mistyped env-var overrides — or just remove env-var fallback for nested account IDs entirely (yaml is authoritative; env vars are for terragrunt subprocess consumption only).

Phase closes when: (a) `km configure` HeadBucket-checks `state_bucket` and re-prompts on 403; (b) `km bootstrap` dry-run text says "would run: terragrunt apply" (not "plan"); (c) `ExportTerragruntEnvVars` warns per drifted env var; (d) `km init --plan` skips fresh-install downstream modules with a clear rationale and exits 0; (e) `km configure` derives `${prefix}-artifacts-${account_id}` and refuses to accept the example placeholder; (f) `km configure` finale prints the three-command sequence and/or `km bootstrap --all` exists; (g) `km env --export` works and produces shell that survives `eval`; (h) `km bootstrap` status banner is authoritative — empty required account IDs WARN with config-load source; (i) all eight closures verified via the Phase 84.4 third-install UAT (84.3 + 84.4 ship together for that UAT to be meaningful, but 84.3 can be implemented and unit-tested independently).

**Design spec:** `.planning/phases/84.3-second-install-bootstrap-ux/84.3-CONTEXT.md` (to be created during planning — captures the eight UX decisions from the live UAT)

**Requirements:** BOOTSTRAP-PREFLIGHT-BUCKET-CHECK, BOOTSTRAP-DRYRUN-TEXT-FIX, ENV-CONFIG-DRIFT-WARN, PLAN-FRESH-INSTALL-OUTPUTS-HANDLING, ARTIFACTS-BUCKET-DERIVATION, BOOTSTRAP-WORKFLOW-DISCOVERABILITY, KM-ENV-EXPORT-HELPER, CONFIG-DISPLAY-VS-YAML-AUTHORITY (synthetic IDs — derived from the eight wrapper-level problems surfaced in the post-84.2 second-install UAT)

**Depends on:** Phase 84.2 (specifically the `ensureRegionHCL` helper added in commit `c345229` — Phase 84.3 builds on the env-derivation patterns there)

**Plans:** 10/10 plans complete

Plans:
- [ ] 84.3-01-PLAN.md — Wave 0 RED test scaffolding for all 8 closures (configure_84_3_test.go, env_test.go, init_84_3_test.go, init_plan_test.go ext, bootstrap_84_3_test.go, config_84_3_test.go) — autonomous, Wave 0
- [ ] 84.3-02-PLAN.md — Wave 1: configure.go + config.go — closures (a) HeadBucket retry, (e) artifacts derivation + placeholder reject, (f) finale + yaml header comments, (h) configure-side WARN + accounts.* yaml-authoritative — autonomous, Wave 1
- [ ] 84.3-03-PLAN.md — Wave 2: bootstrap.go — closures (b) dry-run text fix, (f) --all flag + runBootstrapAll chain, (h) banner WARN — autonomous, Wave 2
- [ ] 84.3-04-PLAN.md — Wave 3: init.go + NEW env.go + root.go — closures (c) drift WARN, (d) outputs.json skip probe, (f) init hard-fail, (g) km env subcommand — autonomous, Wave 3
- [ ] 84.3-05-PLAN.md — Wave 4: OPERATOR CHECKPOINT joint UAT (8 per-closure scenarios + joint scenario i with Phase 84.4) + OPERATOR-GUIDE.md + CLAUDE.md updates — NOT autonomous, Wave 4
- [ ] 84.3-06-PLAN.md — Wave 5 gap-closure RED tests: integration tests for 4 UAT gaps (config_load_drift_test.go, bootstrap_drift_warn_test.go, load_validate_test.go) — autonomous, Wave 5
- [ ] 84.3-07-PLAN.md — Wave 6 gap-closure: Config.YAMLDefaults snapshot + ExportTerragruntEnvVars uses snapshot for drift comparison (gap 1) — autonomous, Wave 6
- [ ] 84.3-08-PLAN.md — Wave 6 gap-closure: wire ExportTerragruntEnvVars into runBootstrap (gap 2) — autonomous, Wave 6
- [ ] 84.3-09-PLAN.md — Wave 6 gap-closure: isPlaceholderBucket in config.Load() + validateArtifactsBucket in runInitPlan (gaps 3+4) — autonomous, Wave 6
- [ ] 84.3-10-PLAN.md — Wave 7 gap-closure: re-run UAT closures c/e/f.6/h autonomously, update UAT.md + REQUIREMENTS.md — autonomous, Wave 7

### Phase 84.4: Multi-install module hardening — infra/modules/ source fixes (INSERTED)

**Goal:** Make the `infra/modules/` source files safe for multiple parallel installs in the same AWS account/Organization. Today (2026-05-17) the Phase 84 prefix-namespacing model only covers SES — the rest of the infra modules still hard-code the literal "km-" string for resource names, IAM role names, SCP policy content, EFS creation tokens, security group names, and other AWS-globally-unique identifiers. A second `km` install with `resource_prefix != "km"` collides on these resources at apply time, importing them creates terraform state drift, and even after import the SCP's policy *content* (trusted_arns_*) still denies the second install's lambdas at runtime — so the second install's sandboxes can't actually be created. This phase audits every `infra/modules/**/*.tf` for hard-coded `km-` literals, classifies each as either (a) "should be prefix-namespaced" (per-install resource that just happens to share a name) or (b) "should be account-shared and auto-imported on conflict" (org-level governance primitive like SCP, foundation SES rule set), implements the locked decision per module, and verifies via teardown-and-restart UAT against the `whereiskurt` probe install. Known collisions surfaced in the 2026-05-17 UAT: (i) `infra/modules/scp/v1.0.0/main.tf:215` hard-codes `name = "km-sandbox-containment"` AND lines 14-34 hard-code `km-ecs-spot-handler`, `km-budget-enforcer-*`, `km-ec2spot-ssm-*`, `km-github-token-refresher-*`, `km-ttl-handler` in `trusted_arns_instance/iam/ssm` — second install gets `DuplicatePolicyException` on apply, and even after import gets DENIED at sandbox-create time because its `${prefix}-*` role names don't match the trusted_arns patterns; (ii) `infra/modules/efs/v1.0.0/main.tf:6` hard-codes `creation_token = "km-shared-${region}"` and line 20 hard-codes SG `name = "km-efs-${region}"` — second install gets `FileSystemAlreadyExists` 409 on apply (creation_token is unique per account/region); (iii) `infra/modules/s3-replication/v1.0.0/main.tf:43,60` hard-codes IAM role `name = "km-s3-replication-${source_bucket_name}"` and policy `name = "km-s3-replication-policy"` — second install gets `EntityAlreadyExists` on the policy name (IAM policy names are unique per account); plus an audit step to catch every other hard-coded `km-` literal that the UAT didn't surface (search target: `grep -rn '"km-' infra/modules/`). Architectural decisions to lock in CONTEXT.md: (1) SCP — account-shared with prefix-registry, OR fully prefix-namespaced (per-install SCP)? Recommendation: prefix-namespaced (each install gets `${prefix}-sandbox-containment` attached to its own application account) — operationally simpler than maintaining an org-wide prefix registry, matches the Phase 84 isolation model. (2) EFS — per-install (each install gets its own filesystem; more cost but cleaner isolation) or account-shared (one filesystem all installs mount; cheaper but requires mount target sharing). Recommendation: per-install. (3) IAM roles/policies — universally prefix-namespaced. (4) Foundation DKIM/MX/TXT auto-import (carried over from Phase 84.3 #4 because it's a bootstrap.go change that needs the module-hardening decisions locked first): when `runBootstrapSharedSES` detects domain identity in AWS but no `aws_route53_record.dkim` entries in foundation tfstate, shell out to `aws ses get-identity-dkim-attributes`, run 3× `terragrunt import aws_route53_record.dkim[N]` plus conditional MX + verification TXT imports (replicating the manual runbook in OPERATOR-GUIDE.md § Phase 84.1 — already zsh-fixed in commit `69704cf`).

Phase closes when: (a) `grep -rn '"km-' infra/modules/` returns zero results in resource-name positions (`name`, `creation_token`, `alias`, `policy_name`, IAM `Resource` arns referencing roles by name) — only comments / variable-defaults / sentinel-strings remain; (b) each module's `${var.resource_prefix}` (or equivalent) is wired through from the live wiring's `inputs = { ... }` block; (c) `km bootstrap --shared-ses --dry-run=false` on a second install in a shared-domain account auto-imports DKIM CNAMEs, MX, and verification TXT — no manual operator import needed; (d) the SCP architectural decision (per-install vs account-shared) is shipped with both code and tests; (e) the EFS architectural decision is shipped with both code and tests; (f) `km uninit --dry-run=false` + `km unbootstrap --dry-run=false` on the `whereiskurt` probe install cleanly tears down every resource that install created (no orphaned EFS/SG/SCP/IAM roles, no terraform state drift), as verified by `aws` CLI inventory of the application account before/after; (g) a fresh-prefix UAT (operator picks new prefix like `rg`, clones repo into `klanker-maker-rg/`, runs `km configure` + the canonical three-command bootstrap sequence) ends with all 17 regional modules + the foundation module green AND a sandbox successfully created via `km create profiles/learn.yaml` — proving the multi-install path works end-to-end including sandbox runtime (not just the apply path). The UAT explicitly tests the load-bearing question: "does a second install actually function (not just bootstrap), and can it be cleanly removed without affecting the first install?"

**Design spec:** `.planning/phases/84.4-multi-install-module-hardening/84.4-CONTEXT.md` (to be created during planning — captures architectural decisions: SCP per-install vs shared, EFS per-install vs shared, prefix-namespacing universality, foundation DKIM/MX/TXT auto-import design)

**Requirements:** SCP-PREFIX-NAMESPACING (or SCP-ACCOUNT-SHARED-WITH-REGISTRY), EFS-PREFIX-NAMESPACING, S3-REPLICATION-PREFIX-NAMESPACING, KM-LITERAL-AUDIT, BOOTSTRAP-AUTO-IMPORT-EXISTING-DNS, TEARDOWN-AND-RESTART-VERIFICATION, FRESH-PREFIX-UAT (synthetic IDs — derived from the module-hardening problems surfaced in the post-84.2 second-install UAT)

**Depends on:** Phase 84.3 (the wrapper-level UX fixes — 84.4's fresh-prefix UAT exercises the full 84.3-fixed bootstrap path). Phase 84.3 can technically be planned and partially executed before 84.4 lands, but the third-install UAT in 84.4 is what verifies both phases together.

**Operator-visible probe state during planning:** The `klanker-maker-whereiskurt` install (resource_prefix: whereiskurt) is currently in a partial state on AWS account 052251888500 — has its own state bucket, KMS key alias, S3 artifacts bucket, foundation SES (imported DKIM CNAMEs from prior install), regional network apply complete, EFS apply FAILED (creation_token collision with km-/kph- install's EFS), SCP imported into whereiskurt state but its policy content actively DENIES whereiskurt's lambdas. This install is a probe — Phase 84.4 plans must include a clean-teardown task (`km uninit` + `km unbootstrap` + manual SCP detach + manual orphan-resource cleanup) verified by AWS-CLI before the fresh-prefix UAT runs. Do NOT attempt to "finish" the whereiskurt install — its purpose is exercising the failure modes that 84.4 codifies.

**Plans:** 9/9 plans complete

Plans:
- [ ] 84.4-00-PLAN.md — Wave 0: prerequisites (hcl/v2 dep, Makefile test target, Runner.Import method, testdata fixtures, inventory-diff script)
- [ ] 84.4-01-PLAN.md — Wave 1: HCL static-analysis audit test (pkg/terragrunt/modulehygiene_test.go) wired into make test
- [ ] 84.4-02-PLAN.md — Wave 1: scp/v2.0.0/ prefix-templated module + 5KB precondition guard + BuildSCPPolicy(resourcePrefix) update + size unit tests
- [ ] 84.4-03-PLAN.md — Wave 1: efs/v2.0.0/ prefix-templated creation_token + SG name + Name tags
- [ ] 84.4-04-PLAN.md — Wave 1: s3-replication/v2.0.0/ prefix-templated IAM role + policy names + stale-literal grep audit
- [x] 84.4-05-PLAN.md — Wave 2: live wiring flip (scp/efs/s3-replication v1.0.0→v2.0.0) + operator zero-diff verification on km install — NOT autonomous
- [ ] 84.4-06-PLAN.md — Wave 2: runBootstrapSharedSES auto-import for DKIM[0..2]/MX/_amazonses TXT via Runner.Import + mocked unit tests
- [ ] 84.4-07-PLAN.md — Wave 3: whereiskurt probe teardown UAT (BEFORE snapshot → km uninit → manual SCP/EFS cleanup → km unbootstrap → AFTER diff) — NOT autonomous
- [ ] 84.4-08-PLAN.md — Wave 4: fresh-prefix rg UAT (full lifecycle: configure → bootstrap → init → sandbox create/destroy → uninit → unbootstrap → km install isolation diff) + OPERATOR-GUIDE.md multi-install runbook — NOT autonomous

### Phase 84.4.1: Multi-install identity/permission gap closure (INSERTED)

**Goal:** Close the design gaps surfaced by the Phase 84.4-08 fresh-prefix `rg` UAT (2026-05-18) that downgraded Phase 84.4 to PARTIAL PASS. Phase 84.4 proved the resource-naming layer works (v2.0.0 modules apply cleanly, 113 adds / 0 destroys), but three load-bearing cross-install issues block the multi-install thesis from being production-safe: (1) **ssm-session-doc not migrated to v2.0.0 (TIER-1)** — `infra/modules/ssm-session-doc/v1.0.0/main.tf` hardcodes the SSM document name `KM-Sandbox-Session`. Plans 02-05 missed it because the audit walks v2.0.0+ only. Operator workaround was `terragrunt import` into rg's state — which then **destroyed the shared document from km's state** when rg was torn down (`km uninit` on rg deleted `KM-Sandbox-Session`, leaving km's state pointing at a missing resource and `km shell` broken on km). Imports are an unsafe workaround for hardcoded-name resources; the module must be properly prefix-namespaced. (2) **SCP AND-composition (CO-PRIMARY)** — Plan 02 added per-install `${prefix}-sandbox-containment` SCP *naming* but did not address that SCPs attached to a shared application account compose by AND (intersection of allows). The canonical km install's SCP `p-cvd490xt` trusts only `km-*` create-handler ARNs, so when rg's create-handler tried `ec2:CreateSecurityGroup` / `iam:CreateRole` for sandbox creation, the km SCP denied it with `explicit deny in a service control policy`. Workaround was hand-editing the deployed SCP body to add `arn:aws:iam::*:role/rg-create-handler` to the allowlist — destructive of the per-install isolation design. (3) **Plan 06 auto-import does not fire (PRIMARY)** — `bootstrap.go:595` gates `autoImportFoundationSESRecords` on `!registerID`, but `detectSharedSESState` (bootstrap.go:550) returns `registerID = true` even when the SES domain identity already exists in AWS. On the rg fresh install, bootstrap tried to create the 3 DKIM CNAMEs that km already owns; Route53 rejected them with `InvalidChangeBatch: ... already exists`. Operator had to run `terragrunt import` manually for each DKIM/MX/TXT record — which is exactly what Plan 06 was supposed to eliminate. Plan 06's unit tests passed because mocks made the auto-import path appear correct; the live shared-domain scenario was never exercised end-to-end. Additionally, six fresh-clone DX gaps surfaced that block the operator path from `git clone` to first sandbox: (a) `km init --plan` fails cryptically when Lambda zips aren't built — `make build-lambdas` is drifted from `init.go:buildLambdaZips()` (4 of 6 zips); (b) `km bootstrap --shared-ses` requires `region.hcl` which is gitignored and only written by `km init`, so fresh clones hit a `read_terragrunt_config` failure; (c) `km configure` state_bucket prompt has no default and no HeadBucket retry — operators enter custom values that drift from site.hcl's computed name; (d) `km unbootstrap` leaves orphan DynamoDB lock table; (e) Phase 84.3 closure-e wrongly assumes `km-artifacts-12345` is always a placeholder (`isPlaceholderBucket` already fixed in d551bba, included here for closure); (f) `downloadTerraform` caches stale binary across tfVersion bumps. The phase closes the multi-install thesis by making cross-install identity/permission/sharing work end-to-end on a fresh prefix install — proven via a re-run fresh-prefix UAT against a second probe install (suggested: `tg` or reuse `rg` post-teardown) that exercises the full lifecycle (configure → bootstrap → init → sandbox create → uninit → unbootstrap) without manual import/SCP-edit workarounds.

Phase closes when: (a) `infra/modules/ssm-session-doc/v2.0.0/` exists with `${var.resource_prefix}-Sandbox-Session` naming, live wiring flipped from v1.0.0 → v2.0.0, and Plan 01's audit extended to scan v1.0.0 modules with WARN-level uppercase-KM-/km- detection (case-insensitive); (b) the SCP cross-install design is shipped — pick one of {shared SCP with union allowlist + prefix registry, per-install SCPs attached to separate OUs, pattern-based `arn:aws:iam::*:role/*-create-handler` allow} based on operator preference, with both code and tests, and the canonical km install's SCP transitions cleanly via `terragrunt apply` (no manual policy-body edits); (c) `detectSharedSESState` actually probes AWS via `GetEmailIdentity` / `ListIdentities` so `registerID = false` on a shared-domain second install — OR the DKIM-record-import gate is separated from the `registerID` gate so any matching Route53 record gets auto-imported regardless of registration ownership, AND an integration test (LocalStack or moto) exercises the second-install-shared-domain path end-to-end; (d) `make build-lambdas` builds the same 6 zips as `init.go:buildLambdaZips()` OR `km init --plan` checks for required zips and prints a clear `./km init --lambdas` hint; (e) `km bootstrap --shared-ses` and `km bootstrap --all` write `region.hcl` as a prereq via a shared helper extracted from `init.go`; (f) `km configure` state_bucket prompt shows computed default `[tf-${prefix}-state-${region}]`, HeadBucket-checks it, offers `[Y/edit/abort]` retry on 403, and writes the accepted value back to km-config.yaml in sync with site.hcl's computation; (g) `km unbootstrap` deletes the DynamoDB lock table created by terragrunt's backend; (h) `downloadTerraform` invalidates cached binaries when `tfVersion` changes (`d5d554b` bumped to 1.9.8 — should not require manual `rm`); (i) the fresh-prefix UAT (a second probe install, full lifecycle, no manual workarounds) ends green AND `km shell` on the canonical km install still works after the probe is torn down (no shared-resource destruction); (j) OPERATOR-GUIDE.md § Phase 84.4 multi-install lifecycle is updated to remove the "gaps + workarounds" section because the gaps are closed.

**Design spec:** `.planning/phases/84.4.1-multi-install-identity-permission-gap-closure/84.4.1-CONTEXT.md` (to be created during planning — captures SCP composition design choice, ssm-session-doc per-install vs shared decision, detectSharedSESState probe-vs-gate-separation design)

**Requirements:** SSM-SESSION-DOC-PREFIX-NAMESPACING, SCP-CROSS-INSTALL-COMPOSITION, SES-AUTO-IMPORT-SHARED-DOMAIN, LAMBDA-ZIP-MAKEFILE-PARITY, BOOTSTRAP-REGION-HCL-PREREQ, CONFIGURE-STATE-BUCKET-UX, UNBOOTSTRAP-DDB-LOCK-CLEANUP, TERRAFORM-VERSION-CACHE-INVALIDATION, FRESH-PREFIX-UAT-2 (synthetic IDs — derived from the 84.4-08 UAT gap inventory in `.planning/phases/84.4-multi-install-module-hardening/deferred-items.md`)

**Depends on:** Phase 84.4 (specifically the v2.0.0 module pattern, BuildSCPPolicyFromPrefix helper, and Plan 06 auto-import scaffolding — all are extended, not replaced)

**Operator-visible probe state during planning:** As of 2026-05-18, the canonical `km` install is healthy after a manual `terragrunt apply` recovery of `ssm-session-doc` (re-created the `KM-Sandbox-Session` document destroyed during rg teardown). The `rg` probe install was cleanly torn down via `km uninit` + `km unbootstrap` (recovery sequence documented in 84.4-08 SUMMARY). Phase 84.4.1's UAT will require a fresh second probe install (suggested prefix: `tg`).

**Plans:** 7/7 plans complete

Plans:
- [ ] 84.4.1-00-PLAN.md — Wave 0: Test scaffolding (GetSandboxSessionDocumentName, UnbootstrapDynamoDBAPI interface, 8 scaffolded tests)
- [ ] 84.4.1-01-PLAN.md — Wave 1: SCP *-* pattern fix (in-place v2.0.0 update + management/scp/terragrunt.hcl + BuildSCPPolicyFromPrefix Go mirror + test rewrite)
- [ ] 84.4.1-02-PLAN.md — Wave 1: ssm-session-doc/v2.0.0 per-install rename + 5 Go callsite updates + audit case-insensitive matching
- [ ] 84.4.1-03-PLAN.md — Wave 1: SES auto-import gate fix at bootstrap.go:595 + fail-fast recovery message + 2 tests
- [ ] 84.4.1-04-PLAN.md — Wave 2: Fresh-clone DX hardening (Makefile 6 zips, region.hcl prereq, configure UX, unbootstrap DDB cleanup, downloadTerraform cache, isPlaceholderBucket regression)
- [ ] 84.4.1-05-PLAN.md — Wave 3: OPERATOR CHECKPOINT — apply scp/v2.0.0 + ssm-session-doc/v2.0.0 to canonical km install (BEFORE/AFTER inventory snapshots) — NOT autonomous
- [ ] 84.4.1-06-PLAN.md — Wave 4: OPERATOR CHECKPOINT — fresh-prefix tg UAT-2 + OPERATOR-GUIDE.md clean runbook (replaces gaps+workarounds prose) — NOT autonomous

### Phase 84.4.1.1: Multi-install follow-on: km-init-plan lambda zips, configure artifacts-bucket derive, orphan SCP detach, validate bucket, uninit TTL bug (INSERTED)

**Goal:** Close 5 post-UAT-2 multi-install correctness and DX gaps: Gap #1 buildLambdaZips in km init --plan, Gap #2 artifacts_bucket derive+validate, Gap #3a doctor orphan-SCP warn, Gap #3b km uninit --include-scp, Gap #4 canonical bucket regex, Gap #5 uninit TTL investigation.
**Requirements**: INIT-PLAN-BUILDS-LAMBDAS, CONFIGURE-DERIVES-ARTIFACTS-BUCKET, VALIDATE-BUCKET-ON-LOAD, VALIDATE-CANONICAL-BUCKET-SHAPE, DOCTOR-WARNS-ON-ORPHAN-SCPS, UNINIT-DETACHES-SCP, UNINIT-TTL-INVESTIGATION
**Depends on:** Phase 84.4.1
**Plans:** 7/7 plans complete

Plans:
- [ ] 84.4.1.1-00-PLAN.md — Wave 0: interface contracts + t.Skip scaffolding (UninitOpts, UninitOrgsAPI, ValidateArtifactsBucket skeleton, 7 test stubs)
- [ ] 84.4.1.1-01-PLAN.md — Wave 1: Gap #1 — buildLambdaZips in RunInitPlanWithRunner + TestRunInitPlan_BuildsLambdaZips GREEN
- [ ] 84.4.1.1-02-PLAN.md — Wave 1: Gap #2 — configure derive-default + config.Load() ValidateArtifactsBucket wiring
- [ ] 84.4.1.1-03-PLAN.md — Wave 2: Gap #4 — validateArtifactsBucket canonical regex + table test GREEN (after Plan 02 — same file)
- [ ] 84.4.1.1-04-PLAN.md — Wave 2: Gap #3a — doctor checkOrphanSCPs + TestDoctor_WarnsOnOrphanSCPs GREEN
- [ ] 84.4.1.1-05-PLAN.md — Wave 2: Gap #3b — km uninit --include-scp detach+delete + TestRunUninit_DetachesSCPWhenFlagSet GREEN
- [ ] 84.4.1.1-06-PLAN.md — Wave 3: Gap #5 — uninit TTL investigation: structured logging + TestRunUninitWithDeps_ActiveSandboxCheck GREEN

### Phase 85: doctor: orphan state-lock digest sweeper + report cleanup

**Goal:** Close the Phase 84.1 digest-leak loop in `km doctor`: add a `--delete-state-digests` cleanup category (also folded into `--with-deletes`) that removes orphan rows from the Terragrunt state-lock DDB table where the sibling S3 state object is definitively gone (NoSuchKey + age > 24h). Replace the unreadable single-line digest-mismatch warn with a `summary + 10-item preview + --json full list` format matching Stale Lambdas. Parallelize the per-item S3 HEAD scan + BatchWriteItem deletes; target `km doctor` < 30s wall clock on accounts with hundreds of orphans (vs. ~1:40 today). Out of scope: plugging the upstream leak in `km destroy` / `km uninit` (separate follow-up phase).

**Requirements**: See `.planning/phases/85-doctor-orphan-state-lock-digest-sweeper-report-cleanup/BRIEF.md` (in-scope items, safety guards, acceptance criteria, TDD test list).
**Depends on:** Phase 84.1 (`checkStateLockDigest` + `StateLockS3Client`/`StateLockDDBClient` wiring)
**Plans:** 4/4 plans complete

Plans:
- [ ] 85-01-PLAN.md — Wave 0 scaffolding: new file doctor_state_digest_sweeper.go with S3StateHeadAPI + LockDigestDeleterAPI interfaces, checkStateLockDigestSweeper stub; 7 red-state TDD test stubs in doctor_state_digest_test.go (Wave 0, autonomous)
- [ ] 85-02-PLAN.md — Wave 1 TDD implementation: parallel HeadObject scan (10-worker semaphore), age guard via sandbox-lister cross-reference (parseSandboxIDFromLockID helper for shared-module fallback), s3types.NotFound classification, BatchWriteItem 25-item batches with UnprocessedItems retry; turn all 5 TDD tests + output-format + UnprocessedItems tests GREEN (Wave 1, autonomous, depends on 85-01)
- [ ] 85-03-PLAN.md — Wave 2 integration: --delete-state-digests flag + --with-deletes fold-in + DoctorDeps fields + initRealDepsWithExisting wiring + buildChecks registration replacement; doctor.go-only changes (Wave 2, autonomous, depends on 85-02)
- [ ] 85-04-PLAN.md — Wave 3 operator UAT: timed km doctor against ~275-orphan account; before/after lock-table snapshots; live sandbox lock-row safety verification; sign-off (Wave 3, NOT autonomous — operator checkpoint, depends on 85-03)

### Phase 86: km-create-prompt-queue — operator-side --prompt flag with on-box queue runner

**Goal:** Add repeatable `--prompt <text-or-@file>` to `km create` that queues prompts on-box at `/workspace/.km-agent/queue/` and drains them sequentially once Claude auth is available. Composes existing `km agent run` primitives — no schema/Lambda/Terragrunt changes. Linear chain semantics: indefinite auth wait, fail-stops-chain, remaining marked `skipped`. Add `km agent list --queue` view. Spec: `docs/superpowers/specs/2026-05-19-km-create-prompt-queue-design.md`.

**Requirements**: PQ-01, PQ-02, PQ-03, PQ-04, PQ-05, PQ-06, PQ-07, PQ-08, PQ-09, PQ-10, PQ-11, PQ-12, PQ-13, R1 (see `.planning/phases/86-km-create-prompt-queue/BRIEF.md` § Acceptance criteria for full descriptions)
**Depends on:** Phase 85 (clean doctor baseline for UAT sandbox lifecycle)
**Plans:** 6/6 plans complete

Plans:
- [ ] 86-01-PLAN.md — Wave 0: RED-state test stubs for PQ-01..PQ-08 in `create_prompt_test.go`, augmented `agent_test.go`, bash test harness skeleton (Wave 0, autonomous)
- [ ] 86-02-PLAN.md — Wave 1: `--prompt` repeatable flag + `--wait` flag on `km create`, `@file`/`@@` parsing, `--docker` mutex, SSM batch queue-file push (PQ-01..PQ-04 + R1; Wave 1, autonomous, depends on 86-01)
- [ ] 86-03-PLAN.md — Wave 1: on-box bash runner + systemd unit via inline userdata.go heredocs (Restart=on-failure, reconcile, auth probes, fail-stops-chain); bash harness flipped GREEN (PQ-08; Wave 1, autonomous, depends on 86-01)
- [ ] 86-04-PLAN.md — Wave 2: `--wait` polling loop with context cancellation + exit-code propagation via os.Exit (PQ-05, PQ-06; Wave 2, autonomous, depends on 86-02 + 86-03)
- [ ] 86-05-PLAN.md — Wave 2: `km agent list --queue` view + CLAUDE.md/OPERATOR-GUIDE.md docs (PQ-07; Wave 2, autonomous, depends on 86-01)
- [ ] 86-06-PLAN.md — Wave 3: operator UAT — pre-flight + UAT.md drafted (Task 1 auto) + 6 real-AWS scenarios executed by operator (Task 2 checkpoint) (PQ-09..PQ-13 + R1; Wave 3, NOT autonomous — operator checkpoint, depends on 86-02, 86-03, 86-04, 86-05)

### Phase 87: additionalSnapshots — snapshot-backed EBS volumes in SandboxProfile

**Goal:** Add `spec.runtime.additionalSnapshots: [...]` to SandboxProfile — a list of `(snapshotId, mountPoint, device?, encrypted?, size?)` tuples. Each entry materialises a fresh `aws_ebs_volume` from an existing EBS snapshot, attaches it on `/dev/sd[f-p]` (auto-allocated or pinned), and mounts via userdata-detected filesystem type. Layered validation: schema rules at `km validate`, `DescribeSnapshots` pre-flight at `km create`. Coexists with existing `additionalVolume` (separate field, both can be set). EC2-only. Requires new module version `ec2spot/v1.1.0` (additive); existing `additionalVolume` semantics unchanged. Volume lifecycle = sandbox lifecycle (destroyed with `km destroy`; source snapshot untouched). Spec: `docs/superpowers/specs/2026-05-21-additional-snapshots-design.md`.

**Requirements**: SNAP-01..SNAP-08 (see `.planning/phases/87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile/BRIEF.md`)
**Depends on:** Phase 86
**Plans:** 7/7 plans complete

Plans:
- [ ] 87-01-PLAN.md — Wave 0: schema scaffolding (Go type + JSON schema) + 6 RED-state test stubs (SNAP-01)
- [ ] 87-02-PLAN.md — Wave 1: Layer 1 validation in validate.go + EC2-only compiler gate parity (SNAP-02; depends on 87-01)
- [ ] 87-03-PLAN.md — Wave 1: Layer 2 AWS DescribeSnapshots pre-flight + create.go wiring + BDM gate broadening + boolPtrHCL template func (SNAP-03; depends on 87-01)
- [ ] 87-04-PLAN.md — Wave 2: compiler pickAdditionalVolumeDevice extension + additional_snapshots HCL render (SNAP-04; depends on 87-01, 87-02, 87-03)
- [ ] 87-05-PLAN.md — Wave 3: userdata.go range-loop refactor + blkid FS detection + golden test for legacy byte-identity (SNAP-05, SNAP-07; depends on 87-01, 87-04)
- [ ] 87-06-PLAN.md — Wave 3: new Terraform module ec2spot/v1.1.0/ (additive copy) + sandbox template version bump (SNAP-06, SNAP-07; depends on 87-04; parallel with 87-05)
- [ ] 87-07-PLAN.md — Wave 4: operator UAT (8 scenarios + SNAP-07 cross-check) + CLAUDE.md/OPERATOR-GUIDE.md docs + example profile (SNAP-07, SNAP-08; NOT autonomous — operator checkpoint; depends on 87-01..06)

### Phase 88: Codex/OpenAI budget metering — http-proxy interceptor for api.openai.com + price table + IncrementAISpend wiring (mirrors Anthropic pipeline)

**Goal:** http-proxy MITM sidecar meters OpenAI direct API (`api.openai.com`) traffic into the same `BUDGET#ai#{modelID}` DynamoDB rows as Bedrock + Anthropic, so Codex sandboxes accrue measurable AI spend and the existing IAM-revoke + proxy-403 enforcement paths fire on OpenAI rows without any enforcer code changes.
**Requirements**: OAI-BUDGET-01, OAI-BUDGET-02, OAI-BUDGET-03, OAI-BUDGET-04, OAI-BUDGET-05, OAI-BUDGET-06, OAI-BUDGET-07, OAI-BUDGET-09
**Depends on:** Phase 87
**Plans:** 7/7 plans complete

Plans:
- [ ] 88-01-PLAN.md — Wave 0: openai_test.go RED scaffold (11 tests — 7 extractor + 1 rate-table + 2 cost + 1 blocked-response) [OAI-BUDGET-01..04]
- [ ] 88-02-PLAN.md — Wave 0: http_proxy_test.go integration RED scaffold (3 tests — AIByModel + MITM end-to-end + transparent) [OAI-BUDGET-05, 06]
- [ ] 88-03-PLAN.md — Wave 0: userdata_test.go L7 host gate RED scaffold (TestL7ProxyHostsWithCodex + Codex+Bedrock regression) [OAI-BUDGET-07]
- [ ] 88-04-PLAN.md — Wave 1: openai.go production code + BedrockModelRate.CachedInputPricePer1KTokens extension; turns 88-01 GREEN [OAI-BUDGET-01..04] (depends on 88-01)
- [ ] 88-05-PLAN.md — Wave 1: proxy.go third intercept block + transparent.go meterOpenAIResponse; turns 88-02 GREEN [OAI-BUDGET-05, 06] (depends on 88-02, 88-04)
- [ ] 88-06-PLAN.md — Wave 1: userdata.go buildL7ProxyHosts Codex gate; turns 88-03 GREEN [OAI-BUDGET-07] (depends on 88-03)
- [ ] 88-07-PLAN.md — Wave 2: make sidecars + km init --sidecars + live UAT (4 scenarios) + CLAUDE.md docs [OAI-BUDGET-09] (NOT autonomous — operator checkpoint; depends on 88-04, 88-05, 88-06)

### Phase 89: SOPS secret injection for sandboxes

**Goal:** Declarative SOPS-encrypted secrets bundle attached to a profile (`spec.secrets.sopsFile: ./secrets/*.enc.yaml`); sandbox decrypts at boot using a shared per-install KMS key (provisioned by `km bootstrap --shared-secrets-key`) and exposes secret values as env vars via `/etc/profile.d/zz-sandbox-secrets.sh`. Acceptance: a Codex sandbox declares `spec.secrets.sopsFile: ./secrets/codex.enc.yaml`, boots, and Phase 88's OpenAI meter writes `BUDGET#ai#gpt-*` rows in DynamoDB without operator post-create wiring.
**Requirements**: SOPS-01-SCHEMA, SOPS-02-VALIDATION, SOPS-03-KMS-MODULE, SOPS-04-MODULE-WIRING, SOPS-05-BOOTSTRAP-FLAG, SOPS-06-BOOTSTRAP-PLAN, SOPS-07-BOOTSTRAP-ALL-CHAIN, SOPS-08-IAM-OPERATOR, SOPS-09-IAM-SANDBOX, SOPS-10-SCHEMA-EXPORT, SOPS-11-COMPILER-UPLOAD, SOPS-12-USERDATA-FETCH, SOPS-13-USERDATA-DECRYPT, SOPS-14-USERDATA-ENV-EXPOSURE, SOPS-15-BOOT-FAIL-ABORT, SOPS-16-DESTROY-CLEANUP, SOPS-17-S3-LIFECYCLE, SOPS-18-DOCTOR-CHECK, SOPS-19-CONFIGURE-GITIGNORE, SOPS-20-SIDECARS-SOPS-DEPLOY, SOPS-21-UNINIT-CLEANUP, SOPS-22-DOCS, SOPS-23-UAT-ACCEPTANCE
**Depends on:** Phase 88
**Plans:** 7/7 plans complete

Plans:
- [ ] 89-01-PLAN.md — Wave 0: Profile schema + JSON Schema + offline semantic validator + age-encrypted fixture [SOPS-01, SOPS-02, SOPS-10]
- [ ] 89-02-PLAN.md — Wave 0: sandbox-secrets-key KMS module v1.0.0 + terragrunt live wiring + s3-artifacts-lifecycle v1.1.0 + ec2spot v1.2.0 additive IAM [SOPS-03, SOPS-04, SOPS-09, SOPS-17]
- [ ] 89-03-PLAN.md — Wave 0: km init --sidecars sops binary upload + km configure gitignore append [SOPS-19, SOPS-20]
- [ ] 89-04-PLAN.md — Wave 1: bootstrap CLI — --shared-secrets-key flag + runBootstrapSharedSecretsKey + plan flow + --all chain + mutex + km uninit cleanup [SOPS-05, SOPS-06, SOPS-07, SOPS-21] (depends on 89-02)
- [ ] 89-05-PLAN.md — Wave 1: compiler — userdata sops fetch/decrypt/env-exposure/fail-abort block + create.go bundle upload + destroy.go bundle cleanup [SOPS-11, SOPS-12, SOPS-13, SOPS-14, SOPS-15, SOPS-16] (depends on 89-01)
- [ ] 89-06-PLAN.md — Wave 2: checkSharedSecretsKey doctor check + operator IAM no-op verify + docs/sandbox-secrets.md + CLAUDE.md entry [SOPS-08, SOPS-18, SOPS-22] (depends on 89-02, 89-04)
- [ ] 89-07-PLAN.md — Wave 3: live UAT — Codex sandbox with sops-injected OPENAI_API_KEY accrues BUDGET#ai#gpt-* in DDB; mirrors Phase 88 plan 07 [SOPS-23] (NOT autonomous — operator checkpoint; depends on 89-03, 89-04, 89-05, 89-06)

### Phase 90: km init self-healing provider locks via reconfigure-upgrade per module

**Goal:** `km init` (apply loop + `--plan` path) runs `terragrunt init -reconfigure -upgrade` per regional module so stale `.terraform.lock.hcl` files from an upgraded old install (observed on km 0.2.x) are moved forward to root.hcl's exact pins (aws 6.46.0, tls 4.3.0) automatically — no manual `init -upgrade` sweep. Destroy paths (uninit, cluster) and bootstrap foundation apply stay on plain `Reconfigure`.
**Requirements**: TBD
**Depends on:** Phase 89
**Design:** docs/superpowers/specs/2026-05-27-km-init-self-healing-locks-design.md
**Plans:** 0 plans

Plans:
- [ ] TBD (run /gsd:plan-phase 90 to break down)

### Phase 91: Slack inbound @-mention-only mode for shared and override channels (polite-bot)

**Goal:** Stop the km-slack bridge from forwarding every message in subscribed channels — only react when the message text contains `<@{bot_user_id}>`. Smart per-channel-mode defaults: per-sandbox `#sb-{id}` channels keep current every-message behaviour (the bot is the primary participant); shared (Mode 1) and operator-controlled override (Mode 3) channels default to @-mention-only. New profile field `cli.notifySlackInboundMentionOnly *bool` lets operators force on/off, otherwise the mode-derived default applies. Bridge handler detects mention via `<@{bot_user_id}>` substring scan; bot_user_id cached in SSM at `{prefix}slack/bot-user-id` (verify caching in `km slack init`). Compiler emits `KM_SLACK_MENTION_ONLY` env var into bridge config from resolved profile. `km doctor` sanity-checks bot_user_id cache when at least one profile has mention-only enabled. Origin: raised by operator during Phase 72 UAT (2026-05-30) — corporate-workspace install where shared `#km-notifications` would be too noisy if the bot 👀-reacted to every team message. Initial design note at `.planning/todos/pending/2026-05-30-slack-inbound-mention-only-mode.md`. **Out of scope:** per-channel runtime overrides (slash command), display-name mentions without `<@U...>` form, reactions-as-actions integration.
**Requirements**: POL-01, POL-02, POL-03, POL-04, POL-05, POL-06, POL-07, POL-08, POL-09, POL-10, POL-11, POL-12, POL-13 (synthetic phase-local IDs, recorded in REQUIREMENTS.md following the Phase 84.2/84.3/89 precedent)
**Depends on:** Phase 72 (uses `bot_user_id` from `auth.test` response shape established in 72-01; reuses `notifySlackEnabled`/`notifySlackPerSandbox`/`notifySlackChannelOverride` mode dispatch from `create_slack.go`)
**Plans:** 7/7 plans complete

Plans:
- [ ] 91-00-PLAN.md — Wave 0 test stub seeding (6 stub test files covering all POL-XX with automated commands)
- [ ] 91-01-PLAN.md — Schema + types + validate: CLISpec.NotifySlackInboundMentionOnly *bool, JSON Schema property (POL-01/02/03)
- [ ] 91-02-PLAN.md — Compiler resolveMentionOnly helper + KM_SLACK_MENTION_ONLY emission into notifyEnv (POL-04/11)
- [ ] 91-03-PLAN.md — Bridge handler MentionOnly field + step 4b mention-scan guard + main.go wiring + Lambda Terraform vars (POL-05/06/09/12)
- [ ] 91-04-PLAN.md — km slack init + km slack rotate-token cache bot_user_id to {prefix}slack/bot-user-id SSM (POL-07/08)
- [ ] 91-05-PLAN.md — km doctor checkSlackBotUserIDCached WARN check (POL-10)
- [ ] 91-06-PLAN.md — Documentation (slack-notifications.md, CLAUDE.md, OPERATOR-GUIDE.md) + UAT checkpoint (POL-13)

**Phase 91.1 (in-place follow-up, 2026-05-30):** Drive `KM_SLACK_MENTION_ONLY` from `km-config.yaml` key `slack.mention_only` and auto-read `KM_SLACK_BOT_USER_ID` from SSM `{prefix}slack/bot-user-id` during `km init`. Eliminates the two pre-init `export` steps from the Phase 91 rollout. Surgical change across `internal/app/config/config.go` (new `SlackConfig{MentionOnly *bool}`), `internal/app/cmd/init.go` (`ExportTerragruntEnvVars` emits the var; new `EnsureSlackBotUserIDFromSSM` helper called from `runInit`), and docs (`docs/slack-notifications.md`, `CLAUDE.md`, `OPERATOR-GUIDE.md`). 6 new unit tests GREEN; verified end-to-end on application account (km v0.3.769) — `km init` shows the `[info]` line on first-install (SSM empty), `km doctor` fires `⚠ Slack bot-user-id cache` correctly. See `.planning/phases/91-.../91-91.1-FOLLOWUP.md`. Commits: `5550573` (code+docs), `cd4038a` (learn.v2 fixtures).

### Phase 92: Profile spec restructure — notification block + iam rename + dead-field removal + structured agent tool gating

**Goal:** A coherent SandboxProfile spec that is honest about what each section is and contains no dead fields. Four user-facing changes land together: (1) new `spec.notification:` block owns every email/Slack/invite/archive decision (14 `cli.notify*` fields move under structured sub-blocks); (2) `spec.identity:` → `spec.iam:` (rename to match reality — section is AWS IAM, not identity); (3) dead fields removed (`identity.sessionPolicy` + entire dead `spec.agent:` block); (4) structured `spec.agent:` block with Claude/Codex tool gating — compiler synthesizes `/home/sandbox/.claude/settings.json` and `~/.codex/config.toml` from typed fields, eliminating the inlined-JSON antipattern. Plus three correctness fixes: pointer-merge inheritance bug (typed `mergeNotificationSpec` + `mergeAgentSpec`), schema drift fix (`iam.allowedSecretPaths` declared in JSON schema), and `vscodeEnabled` relocated from `spec.cli:` to `spec.runtime.vscode.enabled` (provisioning-time, not CLI default). Zero running sandboxes constraint allows atomic YAML rewrites with no backwards compatibility.
**Requirements**: Phase-local synthetic IDs (no formal REQ tracking — legacy/restructure phase). Validation criteria VC-1 through VC-11 in 92-VALIDATION.md.
**Depends on:** Phase 91
**Plans:** 6/7 plans complete

Plans:
- [ ] 92-00-test-scaffolding-research-spikes-PLAN.md — Wave 0: capture pre-Phase-92 byte-identity baselines (userdata + IAM HCL) before any Wave 1 touch + 6 RED test stubs for synthesizers/inheritance/mixed-mode (VC-3, VC-4, VC-5, VC-6, VC-7)
- [ ] 92-01-structural-cleanup-iam-rename-dead-field-removal-PLAN.md — Wave 1: IdentitySpec→IAMSpec rename (5 sites: 3 in security.go + 2 in service_hcl.go) + drop sessionPolicy + delete dead AgentSpec + fix allowedSecretPaths schema drift + update pkg/allowlistgen/generator.go + 30 YAML rewrites + scripts/validate-all-profiles.sh + doc sweep (VC-1, VC-2, VC-4, VC-11)
- [ ] 92-02-notification-types-schema-validator-inherit-PLAN.md — Wave 2: NotificationSpec + 6 sub-types + RuntimeVSCodeSpec + schema additions + typed mergeNotificationSpec (pointer-merge bug fix) + Slack/transcript/invite validator rewires; 14 fields stripped from CLISpec (VC-1, VC-7)
- [ ] 92-03-notification-compiler-cli-fixtures-docs-PLAN.md — Wave 3: 21 userdata.go .CLI.* notify reads relocated to Spec.Notification + Spec.Runtime.VSCode + 8 internal/app/cmd/ files migrated + vscode.go:422 error text update + 20 profile YAML rewrites + 3 doc sweeps (VC-1, VC-3, VC-11)
- [ ] 92-04-agent-types-schema-inherit-mixed-mode-validator-PLAN.md — Wave 4: new AgentSpec/Claude/Codex/ToolsSpec types + schema + typed mergeAgentSpec + mixed-mode validator (autoApprove + inlined configFiles → error) + CLI cmd + userdata.go agent migrations (CLISpec now NoBedrock-only) (VC-1, VC-6)
- [ ] 92-05-agent-synthesizers-fixture-rewrite-docs-PLAN.md — Wave 5: new agent_claude.go + agent_codex.go synthesizers (canonical permissions.allow/deny per Wave 0 research; Codex inert-config + asymmetry doc) + 20 fixture rewrites (inlined Claude settings.json removed; agent.claude.tools.* populated) + docs/agent-tool-gating.md (new) + codex-parity.md + CLAUDE.md (VC-1, VC-3, VC-5, VC-11)
- [ ] 92-06-operator-uat-PLAN.md — Wave 6: 10-scenario operator UAT (Scenario 0 full-inventory validation is hard exit gate; scenarios cover stale-key rejection, real km create + SSM inspection, denied-tool refusal, Slack notify-hook idle, mentionOnly inbound, codex: prefix routing, km doctor); autonomous: false (VC-2, VC-8, VC-9, VC-10, VC-11)

### Phase 93: km desktop — KasmVNC-backed browser/XFCE remote session over SSM port-forward
**Goal:** Give an operator a graphical session — default a single maximized browser (kiosk), optionally a full XFCE desktop — rendered in their **local** browser over an SSM port-forward, so web-browser-based interactions (Chrome/Firefox/Brave) run remotely inside the sandbox EC2. New `spec.runtime.desktop` block (`enabled` default **false** / opt-in; `mode: kiosk|full` default kiosk; `browsers` ⊆ {firefox,chromium,chrome,brave}; optional `geometry`). Engine is **KasmVNC** (web-native VNC server with built-in HTML5 client + seamless bidirectional clipboard) — one component replacing TigerVNC+websockify+noVNC. `km desktop start/status <id>` mirrors `km vscode` (loopback-only bind, SSM port-forward is the sole access path, per-sandbox KasmVNC credential at `~/.km/desktop/<id>` seeded fresh at boot and never baked). Idempotent, AMI-bakeable userdata. Posture-agnostic networking (inherits the profile's `spec.network`). **Ubuntu 24.04/22.04 only** in v1. Deliverables include `profiles/desktop.yaml` (kiosk-Firefox example, added to `scripts/validate-all-profiles.sh`) and a `klanker:desktop` skill. Acceptance: a profile with `spec.runtime.desktop.enabled: true, mode: kiosk, browsers: [firefox]` boots, `km desktop start` prints a `localhost` URL + credential, and the operator drives Firefox in their browser with working clipboard. Design spec: `docs/superpowers/specs/2026-06-02-km-desktop-remote-browser-design.md`.
**Out of scope (v1):** GNOME/KDE, audio, multi-monitor, session recording, Amazon Linux 2023 support, web-based file transfer as a headline feature.
**Requirements**: DSK-01..DSK-15 (synthetic phase-local IDs, recorded in REQUIREMENTS.md following the Phase 84.2/84.3/89 precedent)
**Depends on:** Phase 92 (`spec.runtime` schema shape + `IsVSCodeEnabled`/RuntimeVSCodeSpec sibling pattern; `km vscode` CLI/SSM helpers reused)
**Plans:** 7/8 plans executed

Plans:
- [ ] 93-00-PLAN.md — Wave 0: RED Desktop test stubs (Nyquist scaffold) (DSK-15)
- [ ] 93-01-PLAN.md — Profile schema: RuntimeDesktopSpec + IsDesktopEnabled + JSON Schema (DSK-01, DSK-02, DSK-04)
- [ ] 93-02-PLAN.md — km validate desktop rules + Ubuntu-only AMI guard (DSK-03)
- [ ] 93-03-PLAN.md — Compiler threading + idempotent KasmVNC userdata (kiosk/full, loopback, no-SSL) (DSK-05, DSK-06, DSK-07, DSK-11)
- [ ] 93-04-PLAN.md — Per-sandbox KasmVNC credential at km create (~/.km/desktop/<id>) (DSK-08)
- [ ] 93-05-PLAN.md — km desktop start/status CLI (mirrors km vscode) (DSK-09, DSK-10)
- [ ] 93-06-PLAN.md — profiles/desktop.yaml + inventory gate + klanker:desktop skill + docs (DSK-12, DSK-13, DSK-14)
- [ ] 93-07-PLAN.md — Phase gate + operator live UAT checkpoint (DSK-15)

### Phase 94: km doctor leaked per-sandbox debris cleanup (log groups, DDB rows, S3 lifecycle)

**Goal:** Teach `km doctor` to detect, reclaim, and prevent the orphaned per-sandbox debris that teardown leaves behind — found in a live crawl as ~271 retention-less CloudWatch log groups (`/aws/lambda/{prefix}-budget-enforcer-{id}`, `/aws/lambda/{prefix}-github-token-refresher-{id}`, the per-sandbox sandbox log-group family), leaked DynamoDB rows (`{prefix}-budgets` per-sandbox rows, `-identities`, `-slack-threads`, `status=failed` `-sandboxes` rows), and an artifacts S3 bucket with no lifecycle expiry on transient prefixes (`logs/`, `remote-create/`, `agent-runs/`, `slack-inbound/`). Three new checks follow the established `checkStale*`/`checkOrphaned*` contract (list → group by sandbox-id → diff against `SandboxLister` active set → WARN with hint → reclaim only under `--dry-run=false --delete-X`): `checkStaleLogGroups` (`doctor_log_groups.go`, `--delete-logs` + `--set-log-retention` guardrail), `checkOrphanedDDBRows` (`doctor_ddb_rows.go`, `--delete-ddb-rows`, preserving AI-model `BUDGET#ai#` rows and guarding in-flight creates), and `checkS3LifecyclePolicy` (extends `doctor_artifacts.go`, `--set-s3-lifecycle` guardrail). Two config knobs `doctor_log_retention_days` / `doctor_s3_expire_days` (default 30) via the five-touchpoint pattern. **Plus a root-cause source fix (added 2026-06-04):** the per-sandbox log groups are CREATED with a hardcoded `km-`/`/km/` prefix while teardown DELETES with the dynamic `{resource_prefix}` — so on non-default-prefix installs (e.g. `kph`) they never match and every group leaks (`project_teardown_prefix_asymmetry`). Wave 5 finishes the migration in budget-enforcer/github-token/create-handler TF modules + `userdata.go`/`service_hcl.go` (no-op on the default `km` install, asserted byte-identical); `checkStaleLogGroups` matches BOTH legacy `km-` and new `{prefix}-` names. Deploy: doctor checks are binary-only, but the source fix needs `make build-lambdas` + `km init --dry-run=false`. Out of scope: orphan EBS snapshots (manual operator backups), retroactive renaming of existing sandboxes' log groups. Design spec: `docs/superpowers/specs/2026-06-04-km-doctor-debris-cleanup-design.md`.
**Requirements**: DBG-INFRA, DBG-CFG, DBG-FLAGS, DBG-LOGS, DBG-LOGS-PREFIX, DBG-LOGS-RET, DBG-DDB, DBG-DDB-AI, DBG-DDB-GUARD, DBG-DDB-SLACK, DBG-S3, DBG-S3-SET, DBG-PAGE, DBG-MULTI, DBG-SRCFIX (phase-local synthetic IDs — see REQUIREMENTS.md § Phase 94)
**Depends on:** Phase 93
**Plans:** 5/5 plans complete

Plans:
- [ ] 94-01-PLAN.md — Shared infra: 3 mocked-API interfaces + DoctorDeps fields + initRealDeps wiring, 2 five-touchpoint config knobs, 4 flags + --with-deletes fan-out, runDoctor threading (Wave 1)
- [ ] 94-02-PLAN.md — checkStaleLogGroups: 4 log-group families, matches BOTH legacy km- and new {prefix}- names (deduped) + --set-log-retention guardrail + tests (Wave 2)
- [ ] 94-03-PLAN.md — checkOrphanedDDBRows: 4 tables, BUDGET#ai# preservation, status=failed/nocap guard, slack non-key sandbox_id + tests (Wave 3)
- [ ] 94-04-PLAN.md — checkS3LifecyclePolicy: transient-prefix expiry detection + merge-preserving --set-s3-lifecycle + tests (Wave 4)
- [ ] 94-05-PLAN.md — Root-cause prefix fix: finish resource_prefix migration in budget-enforcer/github-token/create-handler TF modules + userdata.go/service_hcl.go; byte-identical on default km install (Wave 5)

### Phase 95: Slack federated bridge relay — one Slack App serving many resource_prefix installs and operators in one AWS account

**Goal:** Let **one** Slack App's single Events Request URL serve **many** km installs (`resource_prefix`) and operators in a single AWS account. The operator points the App at any one install's bridge ("the front door"); when that bridge receives a message for a channel it does not own, it **relays** the verbatim event to sibling bridges, and the install that owns the channel processes it normally. Mechanism: opt-in static per-install `slack.peer_bridges` list in `km-config.yaml`, plumbed to the bridge Lambda as `KM_SLACK_PEER_BRIDGES` exactly mirroring the `slack.mention_only` end-to-end pattern (config struct + merge-list + `init.go` env export + drift WARN → terragrunt `get_env` → TF var → Lambda env → bridge reads). Every bridge runs **single-hop broadcast-on-miss**: verify the Slack HMAC (the shared App signing secret pasted into each install's normal `km slack init` — no shared SSM, each install keeps its own per-prefix paths), `FetchByChannel` against its own `{prefix}-sandboxes`; on a local **hit** process as today; on a local **miss** POST the verbatim body + `X-Slack-Signature` + `X-Slack-Request-Timestamp` + `X-KM-Relayed: 1` to all `peer_bridges` `/events` URLs (parallel, bounded ~2.5s, synchronous before returning 200 so Slack's 3s ack window holds), then 200. A relayed request is **terminal** — processed if owned, dropped (`slack_relay_no_owner`) otherwise, **never re-relayed** — so loops are structurally impossible; `X-KM-Relayed` is the entire loop guard. **Correctness invariant:** channel name/alias uniqueness across all installs/operators (per-sandbox `#sb-{id}` and single-owner channels route unambiguously; multi-install shared channels like `#km-notifications` stay notify-only). New `pkg/slack/bridge/relayer.go` (`PeerRelayer`/`HTTPPeerRelayer`) injected into `EventsHandler` (nil ⇒ federation off ⇒ byte-identical to today). `km doctor` gains peer-bridge validity / self-loop / empty-list-on-front-door checks. **No shared infra, no SandboxProfile schema change, no sandbox recreate.** Deploy is a Lambda env-block change ⇒ `make build-lambdas` (clean) + `km init --dry-run=false` (NOT `--sidecars`), same constraint as `slack.mention_only`. Design spec: `docs/superpowers/specs/2026-06-05-slack-federated-bridge-relay-design.md`.

**Success Criteria** (what must be TRUE):
  1. With `slack.peer_bridges` unset, the bridge behaves byte-identically to today (federation off; a local miss returns 200 and never broadcasts).
  2. `slack.peer_bridges` in `km-config.yaml` round-trips through config load (merge-list + tri-state population) and `km init` exports `KM_SLACK_PEER_BRIDGES` (comma-joined) with the env-wins drift WARN; the value reaches the bridge Lambda env via the TF module.
  3. A bridge that owns the message's channel (`FetchByChannel` hit) processes it exactly as today, whether the event arrived directly from Slack or as a relayed (`X-KM-Relayed: 1`) request.
  4. A non-relayed message for a channel this install does NOT own is broadcast to every configured peer `/events` URL with the original Slack signature headers preserved + `X-KM-Relayed: 1` added; the front door still returns 200 inside Slack's 3-second window; exactly the owning peer enqueues + reacts and all others drop.
  5. A relayed (`X-KM-Relayed: 1`) message that this install does not own is dropped (logged `slack_relay_no_owner`) and is NEVER re-relayed — verified by test that the relayer is not invoked on a relayed miss (loops impossible).
  6. A relayed request passes the peer's `verifySlackSignature` using the shared signing secret (forwarded body + timestamp unchanged, within the ±5-minute window).
  7. `km doctor` WARNs on a malformed `peer_bridges` URL, a URL pointing at this install's own bridge (self-loop), and an empty `peer_bridges` on the install hosting the Slack Request URL.
  8. End-to-end across two installs in one account/region: a message in install B's per-sandbox channel delivered by Slack to install A's bridge is relayed to and processed by install B (SQS enqueue on B's queue), with the 👀 ack posted.

**Requirements**: SLACK-FED-CFG, SLACK-FED-PLUMB, SLACK-FED-RELAY, SLACK-FED-LOOP, SLACK-FED-VERIFY, SLACK-FED-DOCTOR, SLACK-FED-E2E (phase-local synthetic IDs — see REQUIREMENTS.md § Phase 95)
**Depends on:** Phase 94
**Plans:** 3/3 plans complete

Plans:
- [ ] 95-01-PLAN.md — Config key `slack.peer_bridges` + merge-list + init.go env export + TF/terragrunt plumbing to the bridge Lambda (SLACK-FED-CFG, SLACK-FED-PLUMB)
- [ ] 95-02-PLAN.md — `PeerRelayer`/`HTTPPeerRelayer` + four-row broadcast-on-miss decision table + bridge wiring; nil-Relayer == today (SLACK-FED-RELAY, SLACK-FED-LOOP, SLACK-FED-VERIFY)
- [ ] 95-03-PLAN.md — `km doctor` peer-bridge checks + docs/CLAUDE.md + manual two-install E2E UAT (SLACK-FED-DOCTOR, SLACK-FED-E2E)

### Phase 96: Slack default router — helpful reply for orphan-channel @-mentions

**Goal:** Builds on Phase 95. When the shared bot is @-mentioned in a channel that **no install owns** (no sandbox bound), a single designated **default-router** install posts a helpful threaded reply instead of the message being silently dropped (`slack_relay_no_owner`): it explains no sandbox is bound, shows the naming convention (`#sb-{alias}-{profile}`), and lists currently-running sandbox channels across **all** installs as `<#CID>` mentions so the human can join one. Core mechanism: upgrade Phase 95's fire-and-forget broadcast into a **claim-aware scatter-gather** — a relayed-request handler returns `200 {claimed:bool, channels:[…]}` (a non-owner peer also returns its running sandbox channels via a local `km-sandboxes` `state=running` query); the front door tallies, and **zero claims ⇒ true orphan**. Only the front door can do this (one App ⇒ only the front door receives raw Slack events), so `slack.default_router` is effectively a front-door capability toggle, plumbed exactly like `slack.mention_only` → `KM_SLACK_DEFAULT_ROUTER`. Trigger requires ALL of: the message @-mentions the bot (reuse Phase 91 mention detection), the front door misses `FetchByChannel` locally, AND zero peer claims. Reply is threaded (`thread_ts`=msg ts) with a per-channel cooldown (reuse the pause-hint cooldown, default 3600s). **Member-channels-only (v1)** — Slack delivers message events only for channels the bot is in, so in-channel `chat:write` suffices: **no new Slack scopes, no `app_mention`, no manifest change.** Rollout safety: a peer still on Phase-95 code returns the legacy plain `"ok"`; the front door treats any unparseable/legacy/error response as `claimed:true` (conservative — never post a false "no sandbox here"), so a mixed-version fleet is safe during deploy. `default_router` defaults **false** ⇒ Phase 96 dormant until opted in (byte-identical to Phase 95 when off). **Out of scope (deferred):** agentic self-serve create (`@bot spin me up a profiles/patch.yaml bot` → bridge triggers `km create` via EventBridge — the north star, separate phase), non-member channels (`app_mention`+`chat:write.public`), DM fallback (`im:write`). No SandboxProfile schema change, no sandbox recreate; deploy = `make build-lambdas` + `km init --dry-run=false` (NOT `--sidecars`), all installs before relying on cross-install lists. Design spec: `docs/superpowers/specs/2026-06-05-slack-default-router-design.md`.

**Success Criteria** (what must be TRUE):
  1. With `slack.default_router` unset/false, the bridge behaves byte-identically to Phase 95 (orphan @-mentions dropped/logged, no reply).
  2. A relayed-request handler returns `200 {claimed:true}` when it owns the channel and `200 {claimed:false, channels:[…]}` (with its running sandbox channels) when it does not.
  3. The front door's scatter-gather tally treats any `claimed:true` (or a legacy `"ok"` / HTTP-error response) as "owned" and posts NO router reply; only an all-`claimed:false` result is a true orphan.
  4. On a true orphan where the message @-mentions the bot in a member channel AND `slack.default_router:true`, the front door posts exactly one threaded reply listing running sandbox channels aggregated across the front door + all peers, rendered as `<#CID>` mentions (guidance-only variant when the list is empty).
  5. A second qualifying @-mention in the same channel within the cooldown window is suppressed (no duplicate reply); after the window it replies again.
  6. A non-mention message in an orphan channel never triggers a reply; the bot's own reply does not re-trigger the router (existing self-message/bot-loop filter).
  7. `slack.default_router` round-trips through config load (merge-list regression) and `km init` exports `KM_SLACK_DEFAULT_ROUTER` with the env-wins drift WARN; the value reaches the bridge Lambda env via the TF module.

**Requirements**: SLACK-RTR-CFG, SLACK-RTR-GATHER, SLACK-RTR-ORPHAN, SLACK-RTR-REPLY, SLACK-RTR-COOLDOWN, SLACK-RTR-SAFE, SLACK-RTR-E2E (phase-local synthetic IDs — see REQUIREMENTS.md § Phase 96)
**Depends on:** Phase 95
**Plans:** 3/3 plans complete

Plans:
- [ ] 96-01-PLAN.md — Config + plumbing: slack.default_router struct/merge-list/population, KM_SLACK_DEFAULT_ROUTER export + drift WARN, terragrunt/TF var/Lambda env, dynamodb:Scan IAM grant
- [ ] 96-02-PLAN.md — Scatter-gather contract: Broadcast → []PeerClaimResult (legacy/error→claimed:true), DDBRunningChannelLister Scan adapter, peer-side relayed-request {claimed,channels} response
- [ ] 96-03-PLAN.md — Front-door orphan reply: tally + gated threaded reply (default_router+mention+cooldown), nonces-table cooldown, main.go wiring, docs/CLAUDE.md, manual E2E checkpoint

### Phase 97: GitHub comment-trigger MVP — @-mention dispatch to a per-repo sandbox (PR review)

**Goal:** Let an operator invoke the existing `klanker-maker` GitHub App by **@-mentioning the bot in a PR/issue comment** and have the platform dispatch the free-form request to an **aliased per-repo sandbox** — creating it cold if absent — where Claude reviews the PR and posts back a review via a new sandbox-side `km-github` helper. The GitHub-shaped twin of the Slack inbound path (Phase 67/91/95/96): a new `km-github-bridge` Lambda verifies `X-Hub-Signature-256` over the **raw body** (constant-time), drops non-`created` / `comment.user.type==Bot` events (loop guard), dedupes on `X-GitHub-Delivery` via the existing nonces table, detects an @-mention of the cached bot-login, **authorizes** `sender.login` against a deny-by-default per-repo allowlist, resolves `owner/repo → {alias, profile}` from a new `github.repos:` block in `km-config.yaml` (exact-before-glob, alias defaults to `gh-{owner}-{repo}`), looks up the sandbox via the `alias-index` GSI, and either **enqueues** to the per-sandbox `github-inbound` FIFO (warm) or **publishes a `SandboxCreate` EventBridge event carrying the pending prompt** (cold, via the Phase 86 prompt-queue) — then mints an installation token (reusing `pkg/github/token.go`), posts a 👀 reaction, and returns 200 inside the ~10s webhook window. A new `spec.notification.github.inbound.enabled` profile field provisions the per-sandbox `github-inbound` queue + makes the sandbox poller source-aware; the poller builds a GitHub context preamble (repo/PR/branch/head + worktree-per-PR guidance) and dispatches to the agent. The `km-github` helper (`comment`, `review` verbs) uses the per-sandbox installation token (SSM `{prefix}sandbox/{id}/github-token`, scoped via `sourceAccess.github.allowedRepos`). Ships a lean built-in `github-review` profile and `km github init/manifest/status` operator commands + `km doctor` checks. **Extends the existing App** (adds `issues`/`pull_requests`/`contents`/`checks` write scopes + the `issue_comment` webhook subscription in one reconfigure — the check/PR-create *verbs* land in Phase 98). Absent `github:` config ⇒ feature fully dormant. Deploy: `make build-lambdas` + `km init --dry-run=false` (new Lambda + EventBridge + env block need a full apply) + `km init --sidecars` (schema field for create-handler); existing sandboxes need `km destroy && km create`. Design spec: `docs/superpowers/specs/2026-06-06-github-app-bridge-pr-review-design.md`.

**Success Criteria** (what must be TRUE):
  1. With no `github:` block in `km-config.yaml`, the platform behaves byte-identically to today (no bridge dispatch path active; no new Slack-style env on existing Lambdas).
  2. The `km-github-bridge` rejects any request whose `X-Hub-Signature-256` does not match an HMAC-SHA256 of the raw body under the webhook secret (constant-time compare), and accepts a correctly-signed one.
  3. The bridge drops events with `action != "created"`, with `comment.user.type == "Bot"`, or whose `X-GitHub-Delivery` GUID was already seen (nonces TTL) — no double-dispatch on redelivery, no self-trigger loop.
  4. An `issue_comment` on a PR that @-mentions the bot from an **allowlisted** `sender.login` resolves `owner/repo → {alias, profile}` and dispatches; a comment from a **non-allowlisted** login is silently ignored (no reaction, no comment, no dispatch).
  5. Warm path: when the resolved alias maps to a running sandbox (`alias-index`), the bridge enqueues the `{source:github,…}` envelope to that sandbox's `github-inbound` FIFO and the source-aware poller dispatches it to the agent.
  6. Cold path: when no sandbox exists for the alias, the bridge publishes a `SandboxCreate` event carrying the pending prompt; create-handler provisions the sandbox (queue + poller + write-scoped token) and the carried prompt is drained on first boot.
  7. `spec.notification.github.inbound.enabled: true` round-trips through the schema and `km create` provisions the `github-inbound` queue (DDB attr `github_inbound_queue_url` + SSM + env var); `false`/absent leaves zero SQS/DDB/SSM artifacts.
  8. `github.repos:` round-trips through config load (merge-list regression) and `km init` exports it to the bridge Lambda env with the env-wins drift WARN.
  9. End-to-end: `@klanker-maker review this PR` on a real PR ⇒ 👀 reaction within the ack window ⇒ Claude runs in the per-repo sandbox ⇒ a PR review comment is posted by the bot via `km-github review`.
 10. `km github init/manifest/status` manage the App config (`/km/config/github/{webhook-secret,bot-login,bridge-url}`); `km doctor` reports GitHub bridge health (App configured, secret present, bot-login cached, bridge URL, repo-allowlist resolvability).

**Requirements**: GH-APP-SCOPE, GH-BRIDGE-VERIFY, GH-BRIDGE-AUTH, GH-BRIDGE-ROUTE, GH-INBOUND-Q, GH-POLLER, GH-HELPER, GH-PROFILE, GH-CLI, GH-DOCTOR, GH-E2E (phase-local synthetic IDs — see REQUIREMENTS.md § Phase 97)
**Depends on:** Phase 96
**Plans:** 7/7 plans complete

Plans:
- [ ] 97-01-PLAN.md — github: config block, KM_GITHUB_REPOS JSON env export, km github init/manifest/status (GH-CLI, GH-APP-SCOPE)
- [ ] 97-02-PLAN.md — write-scoped per-sandbox token + cold-create envelope carry through SandboxCreate/create-handler (GH-BRIDGE-ROUTE)
- [ ] 97-03-PLAN.md — notification.github.inbound profile field + github-inbound FIFO provisioning + DDB round-trip + github-review profile (GH-INBOUND-Q, GH-PROFILE)
- [ ] 97-04-PLAN.md — km-github-bridge Lambda (HMAC verify, auth, resolve, warm/cold dispatch, ACK) + TF module (GH-BRIDGE-VERIFY, GH-BRIDGE-AUTH, GH-BRIDGE-ROUTE, GH-APP-SCOPE)
- [ ] 97-05-PLAN.md — source-aware github poller + km-github comment/review helper (GH-POLLER, GH-HELPER)
- [ ] 97-06-PLAN.md — km doctor GitHub checks + runbook + manual E2E (GH-DOCTOR, GH-E2E)
- [ ] 97-07-PLAN.md — gap-closure: wire km-github-bridge into deploy path (live terragrunt unit + init.go module list) (GH-BRIDGE-DEPLOY, GH-BRIDGE-ROUTE)

### Phase 98: GitHub bridge expansion — richer write-backs, thread continuity, shared-alias

**Goal:** Build on Phase 97. Extend the `km-github` helper with the remaining write verbs — `km-github check` (check-run pass/fail + summary, CI-style gating), `km-github pr create` (open a PR / push a new branch, e.g. turning an issue @-mention into a PR), and harden the push-commit path (the App write scopes already landed in Phase 97). Add **thread/session continuity**: a `(repo, number) → {sandbox_id, agent_session_id}` mapping (generalize `km-slack-threads` or a sibling `km-github-threads` table) so follow-up @-mentions in the same PR/issue continue the **same agent session**, plus a thread-bypass so replies in a known thread skip the re-mention requirement (mirrors Phase 91.3). Add **shared-alias across repos**: let several `github.repos:` entries point at one sandbox alias for a single larger shared box, with worktree-per-PR isolation and `km doctor` overlap/collision warnings. Add **stopped-sandbox auto-resume**: when the warm-path alias lookup (`ResolveByAlias`) finds a sandbox in `stopped`/`paused` state, the bridge resumes it before/while enqueueing (the poller drains the FIFO backlog once it boots) — enabling the "pre-configure a fixed-alias sandbox with creds once, stop it, and let a GitHub @-mention wake it on demand" workflow. Today (Phase 97) the warm path is status-agnostic: it finds the stopped row and enqueues, but nothing resumes the box, so the message sits undrained and the cold path never fires (the stopped row captures the alias). Needs added bridge IAM (`ec2:StartInstances` / resume path) and must respect the ~10s ack window (enqueue + fire resume async; poller drains post-boot). **Fix the cold-create path** (broken/never-exercised in Phase 97 — see 97-VERIFICATION.md § Cold path): the bridge's `SandboxCreate` event omits `sandbox_id` + `artifact_bucket` and builds a malformed `artifact_prefix`, so `create-handler` rejects it. Cold-create must (a) generate a valid `sandbox_id` and set `artifact_bucket`/`artifact_prefix` correctly; (b) get the resolved `github.repos` profile to `create-handler` — `km init` pre-stages each cold profile to S3 at `{bucket}/{prefix}/.km-profile.yaml`; (c) solve cold-box auth via **SOPS-injected Claude credentials** (`spec.secrets.sopsFile`, Phase 89 — NOT Bedrock; operator decision) so a fresh box self-authenticates and can post; (d) the dispatch decision is unified with GH-X-RESUME — *resume* a paused/stopped aliased box if one exists, *cold-create* only when truly absent. Optionally widen the trigger surface (deferred from 97): `pull_request_review_comment` inline-diff comments. Design spec: `docs/superpowers/specs/2026-06-06-github-app-bridge-pr-review-design.md`.

**Success Criteria** (what must be TRUE):
  1. `km-github check` posts a check run (name + conclusion + summary) visible on the PR; `km-github pr create` opens a PR from a new branch and returns its URL.
  2. A follow-up @-mention in a PR/issue whose `(repo, number)` is already mapped continues the same agent session (no fresh session), and a reply in a known thread dispatches without requiring a re-@-mention.
  3. Multiple `github.repos:` entries pointing at one shared alias all dispatch to the same sandbox; concurrent PRs are isolated via worktree-per-PR; `km doctor` warns on match overlap / alias collisions.
  4. An @-mention targeting an alias whose sandbox is `stopped`/`paused` auto-resumes that sandbox and the enqueued request is processed after it boots (no manual `km resume`); a running sandbox is unaffected.
  5. An @-mention for an allowlisted repo with **no** sandbox cold-creates one (valid `sandbox_id` + S3-staged profile), the carried envelope drains on first boot, the box self-authenticates via SOPS-injected Claude creds, and a review posts — fully automated, no manual auth.
  6. All Phase 97 success criteria continue to hold (no regression to the comment-trigger MVP warm path).

**Requirements**: GH-X-CHECK, GH-X-PRCREATE, GH-X-PUSH, GH-X-CONTINUITY, GH-X-THREADBYPASS, GH-X-SHARED, GH-X-RESUME, GH-COLD-CREATE, GH-X-E2E (phase-local synthetic IDs — see REQUIREMENTS.md § Phase 98)
**Depends on:** Phase 97
**Plans:** 7/7 plans complete

Plans:
- [ ] 98-00-PLAN.md — Wave 0: RED test scaffolding + km-github-threads TF module/live unit + module-list guard
- [ ] 98-01-PLAN.md — km-github check + pr create verbs; push hardening (worktree-per-PR preamble)
- [ ] 98-02-PLAN.md — thread/session continuity + thread-bypass (km-github-threads store, bridge IAM v1.1.0, poller session write)
- [ ] 98-03-PLAN.md — shared-alias resolution characterization + km doctor collision/overlap WARN
- [ ] 98-04-PLAN.md — auto-resume + cold-create fix (sandbox_id/artifact_prefix, km init pre-stage, EC2 IAM, SOPS cold-box auth)
- [ ] 98-05-PLAN.md — deploy-surface verification + github-bridge docs + manual E2E checkpoint

### Phase 99: GitHub bridge commands — config-defined /commands mapping to prompt templates plus env routing

**Goal:** Build on Phases 97/98. Give operators a **command** abstraction for the GitHub comment-trigger bridge: a `github.commands:` block in `km-config.yaml` where each named command bundles a **prompt template** (inline, or `@file` loaded at `km init` time), an optional **routing override** (`alias`/`profile`), and an optional **per-command user allowlist**. A user invokes one with `@klanker-maker … /name …` placed **anywhere** in a PR/issue comment. The bridge gains a second resolution pass after the existing repo `Resolve()`: it strips fenced/inline code, scans for whitespace-bounded `/name` tokens, and — for **exactly one** distinct known command — expands its template (`{{args}}` = the comment minus the mention + command tokens, whitespace-normalized) and routes to `command.alias || repo.alias` / `command.profile || repo.profile || default_profile`, feeding the resulting `{alias, profile, prompt}` into Phase 98's existing warm / resume / cold-create dispatch **unchanged**. Authorization is **deny-by-default outer** (`repo.allow` gates engagement, silent drop) **plus inner narrowing** (`command.allow`, when set, intersects — effective = `repo.allow ∩ command.allow`; a known user who fails it gets a polite "not authorized" reply). Routing model is **command-overrides-repo**; a command-less comment runs an optional configurable **`default_command`** (per-repo `repos[].default_command` overrides install-wide `github.default_command`; `{{args}}` = comment minus mention), falling back to today's free-form passthrough when unset. **More than one distinct known command in a comment is an error** (one-at-a-time reply, no dispatch); **unknown `/token`s are lenient** — treated as plain text so the comment dispatches free-form (no help spam). A built-in `/help` lists commands (name + `description`). The command set (with `@file` templates already inlined) is published to **SSM** `{prefix}/config/github/commands` — not a Lambda env var — to dodge the 4 KB env ceiling; the bridge reads it at cold start alongside the existing `{prefix}/config/github/{webhook-secret,bot-login,bridge-url}` params. `km doctor` validates `@file` existence, command-profile resolvability, `help`-shadow, command↔repo alias overlap (extends 98-03), and SSM-param presence; `km github status` lists commands. **No SandboxProfile schema change → no `km init --sidecars`, no sandbox recreate.** Absent `github.commands` ⇒ byte-identical to Phase 98 (no SSM param, no command pass). Deploy: `make build-lambdas` + `km init --dry-run=false` (bridge code + SSM/config). Design spec: `docs/superpowers/specs/2026-06-07-github-bridge-commands-design.md`.

**Success Criteria** (what must be TRUE):
  1. With no `github.commands` block, the bridge behaves byte-identically to Phase 98 (no SSM command param written, no command pass active, free-form dispatch only).
  2. `github.commands:` round-trips through config load (merge-list regression) including `@file` prompt resolution at `km init` (missing `@file` ⇒ hard `km init` error); the assembled set is published to SSM `{prefix}/config/github/commands` and read by the bridge at cold start.
  3. A comment with exactly one known `/command` expands its template (`{{args}}` substituted) and dispatches with `command.alias || repo.alias` / `command.profile || repo.profile || default_profile`; a comment with no command runs the effective `default_command` (`repo.default_command || github.default_command`) when configured, else dispatches free-form to the repo box.
  3a. A configurable default command applies on a command-less comment: per-repo `default_command` overrides the install-wide `github.default_command`; with neither set, the command-less comment is free-form passthrough; `km init`/`km doctor` error when any `default_command` names an undefined command.
  4. Command parsing finds the token **anywhere** in the comment, ignores tokens inside fenced/inline code and embedded-slash tokens (e.g. `/usr/bin/patch`), dedupes repeats of the same command, and **errors** (one-at-a-time reply, no dispatch) when two distinct known commands appear.
  5. An unknown/typo'd `/token` is treated as plain text and the comment dispatches free-form (no unknown-command reply); `/help` posts the command listing.
  6. Authorization: a sender not in `repo.allow` is dropped silently regardless of command; a sender in `repo.allow` but not in a command's `command.allow` gets a "not authorized for /cmd" reply and no dispatch; a sender in both dispatches.
  7. The routing override feeds Phase 98's warm / stopped-resume / cold-create pipeline unchanged (a command targeting a stopped aliased box resumes it; a command targeting an absent alias cold-creates it).
  8. `km doctor` reports command health (`@file` exists, profile resolvable, `help` not shadowed, command↔repo alias overlap WARN, SSM param present); `km github status` lists configured commands.
  9. All Phase 97/98 success criteria continue to hold (no regression to the comment-trigger MVP, thread continuity, shared-alias, or auto-resume/cold-create paths).

**Requirements**: GH-CMD-CONFIG, GH-CMD-FILEREF, GH-CMD-SSM, GH-CMD-PARSE, GH-CMD-ROUTE, GH-CMD-AUTH, GH-CMD-HELP, GH-CMD-E2E (phase-local synthetic IDs — see REQUIREMENTS.md § Phase 98)
**Depends on:** Phase 98
**Plans:** 5/5 plans complete

Plans:
- [ ] 99-01-PLAN.md — Config plumbing: GithubCommandEntry + Commands/DefaultCommand + per-repo default_command + getters (GH-CMD-CONFIG)
- [ ] 99-02-PLAN.md — Bridge command parser + resolver pure functions: code-strip, scan, {{args}}, expand, routing, auth-intersection (GH-CMD-PARSE/ROUTE/AUTH)
- [ ] 99-03-PLAN.md — km init @file resolution + SSM commands publication + drift WARN (GH-CMD-FILEREF/SSM)
- [ ] 99-04-PLAN.md — Bridge handler wiring + SSM read + reply paths (/help, multi-command, not-authorized) + dormancy (GH-CMD-AUTH/HELP/E2E)
- [ ] 99-05-PLAN.md — km doctor checks + km github status + docs + deploy-surface (GH-CMD-CONFIG/HELP/E2E)

### Phase 99.1: Harden github/slack inbound pollers against FIFO poison-message wedge via shared per-install DLQ + RedrivePolicy (INSERTED)

**Goal:** Fix a production-risk robustness gap in the per-sandbox **github-inbound** and **slack-inbound** SQS **FIFO** queues, discovered live during Phase 99 UAT (2026-06-08). Today those queues have **no redrive policy**, and the userdata poller shell loops (`pkg/compiler/userdata.go` — github ~2336, slack ~2071) silently requeue a failed `claude -p` agent run (`WARN: agent run failed … message returns to queue`). Because the queues are FIFO, a poison message **head-of-line-blocks its entire message group forever** and wedges the poller — a `systemctl restart` does NOT clear it (the message stays `NotVisible`); only `aws sqs purge-queue` recovers. A pre-auth `/review` reproduced this live (poller spun ~50s CPU, stuck ~20 min, blocked the next valid `/review`). There is already a NARROW Gap-E retry (98-06) for the stale-`--resume` "No conversation found" case, but the GENERAL failure path (auth, network, any non-zero exit) still poison-loops. **Fix (resolved design):** (1) create **ONE shared per-install DLQ FIFO** for github-inbound and one for slack-inbound (created once at `km init`/`bootstrap`, not per sandbox); (2) set `RedrivePolicy` (`deadLetterTargetArn` + `maxReceiveCount` ~3) on the per-sandbox inbound FIFO queues in `pkg/aws/sqs.go CreateGitHubInboundQueue` **and** the slack equivalent, covering **BOTH creation paths** — warm `km create` (`provision*InboundQueue` → the helper) and the **cold-create handler Lambda** (`cmd/create-handler/main.go`); SQS then auto-evicts a poison message to the DLQ after N receives, un-wedging the group with no poller-loop change required for the un-wedge itself; (3) teardown leaves the shared DLQ intact and cleans only per-sandbox references (`destroy_*_inbound.go`); (4) `km doctor` gains visibility on DLQ depth (poison messages waiting) so operators notice stuck runs; (5) **deploy/migration:** `make build-lambdas` + `km init --dry-run=false` (shared DLQ provisioning + create-handler Lambda rebuild); existing sandboxes' queues do NOT gain redrive retroactively → documented as requiring `km destroy && km create` (no silent backfill assumption). No SandboxProfile schema change ⇒ no `km init --sidecars` for the schema, but the create-handler Lambda + shared-infra changes require a full `km init` apply. Absent any inbound config ⇒ byte-identical to today (dormant). **Out of scope:** the Phase 99 `{{args}}` command layer (green); changing visibility-timeout/retention semantics; a DLQ *consumer* (depth visibility only). Builds on Phase 97 (inbound pollers) / 98 / 99. See `project_inbound_poller_fifo_poison_wedge` and `feedback_verify_deploy_surface_not_just_code`.

**Success Criteria** (what must be TRUE):
  1. A per-sandbox github-inbound (and slack-inbound) FIFO queue created by EITHER path (warm `km create` or cold-create handler Lambda) has a `RedrivePolicy` pointing at the shared per-install DLQ with `maxReceiveCount` set.
  2. The shared DLQ FIFO queues exist after `km init` (idempotent; created once per install), and a poison message (repeatedly failing agent run) lands in the DLQ after `maxReceiveCount` receives instead of head-of-line-blocking its FIFO group — a subsequent valid message in the same group is processed.
  3. `km doctor` reports DLQ depth (poison messages waiting) for the inbound DLQs; clean when empty, WARN when non-empty.
  4. Teardown (`km destroy`) removes per-sandbox queue references but leaves the shared DLQ; `km uninit` handles the shared DLQ lifecycle without orphaning sibling installs.
  5. Absent inbound config the behavior is byte-identical to today (no DLQ traffic, dormant); the deploy path is documented (full `km init` apply + create-handler rebuild; existing sandboxes need recreate).

**Requirements**: GH-DLQ-REDRIVE, GH-DLQ-SHARED, GH-DLQ-BOTHPATHS, GH-DLQ-SLACK, GH-DLQ-DOCTOR, GH-DLQ-TEARDOWN, GH-DLQ-DEPLOY (phase-local synthetic IDs)
**Depends on:** Phase 99
**Plans:** 4/4 plans complete

Plans:
- [ ] 99.1-01-PLAN.md — SQS layer: DLQ name/ARN helpers, idempotent CreateSharedInboundDLQ, RedrivePolicy injection on both Create*InboundQueue (dormant when no DLQ)
- [ ] 99.1-02-PLAN.md — Warm km create provisioning: thread derived DLQ ARN through provision*InboundQueue; teardown leaves the shared DLQ; no create-handler change
- [ ] 99.1-03-PLAN.md — Shared DLQ infra: sqs-inbound-dlq TF module + live use1 unit + regionalModules() registration + IAM coverage verify
- [ ] 99.1-04-PLAN.md — km doctor inbound DLQ-depth check + docs (deploy sequence, recreate migration) + deploy-surface verification

### Phase 100: GitHub bridge federated relay — one GitHub App serving many resource_prefix installs via github.peer_bridges

**Goal:** Let **one GitHub App serve many `resource_prefix` installs** in a single AWS account (e.g. `kph` + `sec`), the direct analog of Slack's Phase 95 federated relay. A GitHub App has exactly one webhook URL, but each install runs its own `{prefix}-github-bridge` Lambda (own Function URL, own SSM App config under `/{prefix}/config/github/`, own `github.repos:`, dispatches only into its own `{prefix}-sandboxes`). The operator points the App's single webhook at any one install's bridge ("the front door"); on an event for a repo it does not own (the `Resolve()` `!matched` miss at `webhook_handler.go:216`), the front door **broadcasts** the verbatim webhook to sibling bridges, and the install whose `github.repos:` matches processes it normally. New opt-in `github.peer_bridges: []string` in `km-config.yaml`, plumbed to the bridge Lambda as `KM_GITHUB_PEER_BRIDGES` (struct + v2→v merge-list per `project_config_key_merge_list` + `init.go` env export with env-wins drift WARN → terragrunt `get_env` → TF var → Lambda env → bridge parses), mirroring `slack.peer_bridges` end-to-end. **One structural change vs Slack's drop-in:** `Resolve()` ownership must move ahead of the mention/thread filter so a peer-owned known-thread follow-up *with no @-mention* (Phase 98 thread-bypass) still relays; the mention/thread/auth/dedupe/dispatch steps run only on the locally-owned (matched) path, and each peer re-runs the full `Handle()` including its own thread-bypass. **This reorder is UNCONDITIONAL (applies even with `peer_bridges` empty) and doubles as a 700-repo SCALE FIX:** today the thread-continuity `LookupSandbox` DDB GetItem runs for every created PR comment *before* the mention/ownership gates, so an App on many repos does a DDB read per PR comment org-wide even for repos never wired into `github.repos`; after the reorder a comment on an unconfigured/unowned repo short-circuits at the in-memory config match with no DDB read (byte-identical dispatch outcomes — a thread row only ever exists for an owned repo — so it just removes wasted reads; single-install deployments benefit equally). Relay forwards verbatim body + `X-Hub-Signature-256` + `X-GitHub-Event` + `X-GitHub-Delivery` + `X-KM-Relayed: 1`; the operator pastes the SAME App webhook secret into each install's `km github init` so every peer re-verifies HMAC (GitHub signatures carry no timestamp → no skew window). Loop guard: `X-KM-Relayed: 1` makes a relayed request terminal (processed if owned, dropped `github_relay_no_owner` otherwise, NEVER re-relayed) — single-hop, loops structurally impossible. Each install dedupes the forwarded `X-GitHub-Delivery` in its own `{prefix}-nonces`; the OWNER posts the single 👀 (front door on a miss just relays, no reaction). Broadcast is synchronous + parallel under a bounded context (~5s; GitHub's ~10s ack window is roomier than Slack's 3s). Correctness invariant: each repo owned by exactly one install (unique `github.repos:` match across installs) — `km doctor` can't read peers' configs, so documented not enforced (mirrors Slack channel-uniqueness). `km doctor` gains peer-URL-validity / self-loop / empty-on-front-door checks. NO SandboxProfile schema change ⇒ no `km init --sidecars`, no sandbox recreate; absent `github.peer_bridges` ⇒ byte-identical to Phase 97/98. Deploy: `make build-lambdas` (clean) + `km init --dry-run=false` (env-block ⇒ full apply, NOT `--sidecars`). **Deferred to a future Phase 101:** orphan-repo helpful PR comment when no install owns the repo (the Phase-96 default-router analog; needs claim-aware scatter-gather). **Out of scope entirely:** routing by command on the same repo to different prefixes (no cross-prefix dispatch). Design spec: `docs/superpowers/specs/2026-06-07-github-bridge-peer-relay-design.md`.

**Success Criteria** (what must be TRUE):
  1. With no `github.peer_bridges` configured, the bridge behaves byte-identically to Phase 97/98 (no relay, resolve-miss returns 200 as today).
  2. `github.peer_bridges:` round-trips through config load (merge-list regression) and `km init` exports `KM_GITHUB_PEER_BRIDGES` to the bridge Lambda env with the env-wins drift WARN.
  3. A front-door bridge receiving a webhook for a repo NOT in its `github.repos:` broadcasts the verbatim body + GitHub headers + `X-KM-Relayed: 1` to all `peer_bridges` and returns 200; a peer whose `github.repos:` matches re-verifies the signature with the shared secret and dispatches normally.
  4. The `Resolve()` ownership check runs ahead of the mention/thread filter: a no-@-mention known-thread follow-up for a peer-owned repo is relayed (not dropped at the front door's mention filter); each peer re-runs its full `Handle()` including its own thread-bypass.
  5. Loop guard: a request carrying `X-KM-Relayed: 1` is terminal — processed if locally owned, dropped (`github_relay_no_owner`) otherwise, and NEVER re-broadcast.
  6. Exactly one 👀 and one dispatch per event: the owning install reacts + dispatches; the front door on a miss neither reacts nor enqueues; redelivery is deduped per-install via the forwarded `X-GitHub-Delivery`.
  7. The synchronous bounded broadcast returns 200 within the GitHub ack window even when a peer is slow/unreachable (failure logged, non-fatal).
  8. `km doctor` reports peer-bridge health (malformed peer URL, self-loop, empty list when this install hosts the App webhook).
  9. All Phase 97/98 success criteria continue to hold (no regression to the comment-trigger MVP, thread continuity, shared-alias, auto-resume/cold-create).
  10. Scale fix (federation OFF): with `peer_bridges` empty and Phase 98 thread-continuity enabled, a created PR comment on a repo NOT in `github.repos` returns 200 with NO `LookupSandbox` DDB read (resolve-miss short-circuits ahead of the thread-bypass), while a comment on an owned repo still performs the lookup; dispatch outcomes are byte-identical to Phase 98.

**Requirements**: GH-FED-CONFIG, GH-FED-RELAY, GH-FED-REORDER, GH-FED-LOOPGUARD, GH-FED-VERIFY, GH-FED-DOCTOR, GH-FED-SCALE, GH-FED-E2E (phase-local synthetic IDs — see REQUIREMENTS.md § Phase 98)
**Depends on:** Phase 98 (independent of Phase 99)
**Plans:** 4/4 plans complete

Plans:
- [ ] 100-01-PLAN.md — config plumbing: github.peer_bridges → KM_GITHUB_PEER_BRIDGES (struct + init.go export + TF/terragrunt), no new merge entry
- [ ] 100-02-PLAN.md — HTTP peer relayer (PeerRelayer interface + fire-and-forget HTTPPeerRelayer, X-KM-Relayed loop guard)
- [ ] 100-03-PLAN.md — Resolve() reorder + !matched relay/drop branch + cmd wiring (byte-identity + 700-repo scale fix)
- [ ] 100-04-PLAN.md — km doctor peer checks + docs (github-bridge.md / OPERATOR-GUIDE / CLAUDE.md) + two-install E2E UAT runbook

### Phase 101: GitHub bridge orphan-repo helpful reply — front-door posts guidance when no install owns the repo (claim-aware scatter-gather, Slack Phase 96 analog)

**Goal:** Builds on Phase 100. After federation, an @-mention on a repo that NO install owns is silently dropped (`github_relay_no_owner`) and the human gets no response — the GitHub analog of the gap Slack Phase 96 closed. Goal: the front-door install posts ONE helpful PR/issue comment explaining no sandbox is bound to the repo and how to wire it up (point at `github.repos:` / the bot install). Mechanism (mirrors Slack 96): upgrade Phase 100's fire-and-forget broadcast to a **claim-aware scatter-gather** — each relayed-to peer returns `200 {claimed:bool}` (claimed=true when its `github.repos:` matches), the front door tallies, and **zero claims ⇒ true orphan ⇒ post the guidance comment**; any claim ⇒ owner handled it ⇒ no comment. Per-(repo,number) cooldown via the nonces table (reuse the Phase 96 cooldown-key pattern) to avoid repeat spam on a busy PR. Rollout-safe mixed fleet: a peer still on Phase-100 code returns a plain 200 with no body → treat as `claimed:true` (never post a false "nobody owns this"). Front-door-only toggle (analog of `slack.default_router`): e.g. `github.default_router: true`. Dormant by default ⇒ byte-identical to Phase 100 when off. No schema change, no sandbox recreate; deploy `make build-lambdas` + `km init --dry-run=false`. **Deferred from Phase 100** (which shipped routing-relay-only). Design: mirrors `docs/superpowers/specs/2026-06-05-slack-default-router-design.md` + the Phase 100 spec's deferred note; full spec to be written at `/gsd:plan-phase 101` time.

**Requirements**: GH-ORPHAN-CLAIM, GH-ORPHAN-REPLY, GH-ORPHAN-COOLDOWN, GH-ORPHAN-ROLLOUT, GH-ORPHAN-E2E (phase-local synthetic IDs — see REQUIREMENTS.md § Phase 98)
**Depends on:** Phase 100
**Plans:** 4/4 plans complete

Plans:
- [ ] 101-01-PLAN.md — github.default_router config plumbing (struct → KM_GITHUB_DEFAULT_ROUTER env → TF var/Lambda env); no merge-list entry
- [ ] 101-02-PLAN.md — relayer claim-aware upgrade (Broadcast → []PeerClaimResult; rollout-safe legacy/timeout/non-2xx ⇒ Claimed:true)
- [ ] 101-03-PLAN.md — handler wiring (peer-side claim emit, front-door tally, orphan-comment method, cooldown, main.go gate)
- [ ] 101-04-PLAN.md — docs (github-bridge.md/OPERATOR-GUIDE/CLAUDE Phase 101) + two-install E2E UAT runbook (101-UAT.md)

### Phase 102: GitHub bridge agent verbs — /claude and /codex select the per-thread agent in a PR comment (Slack Phase 70 analog)

**Goal:** Reserved **`/claude`** and **`/codex`** verbs in a PR/issue comment select the agent for that **thread** — the GitHub analog of Slack's Phase 70 `claude:`/`codex:` prefix routing. Today GitHub dispatch always runs the profile-default agent (`userdata.go:2248` hardwires `EFFECTIVE_AGENT="$AGENT"`); the poller already HAS the Claude/Codex dispatch fork and already captures both session types (Claude `.session_id`, Codex `thread.started` `thread_id`, `userdata.go:2253-2317`) but never varies the agent. Decisions: slash verbs (consistent with Phase 99 `/command` tokens, parsed anywhere; NOT Slack's `codex:` colon prefix), reserved built-ins (like `/help`); a **separate axis** from Phase 99 template commands that COMPOSES (`@bot /codex /patch fix X` = Codex agent + `/patch` template, `{{args}}`="fix X"); **persistent per-thread** (writes a new `agent_type` column on `km-github-threads`; follow-ups with no verb continue with it); precedence **verb > thread `agent_type` > profile default**; **≤1 agent verb** per comment (two = error reply); **single `agent_session_id` column, reset on cross-agent switch** (switching agent starts a fresh session + overwrites; switching back = fresh — no Slack-style 8-step new-top-level handoff, because the PR IS the thread); `/codex` requires a **Codex-capable profile** (the lean `github-review` is Claude-only) → documented precondition + a runtime helpful-error comment instead of a stranded turn. Plumbing: `GitHubEnvelope` gains `agent`; bridge parses+strips the verb; `km-github-threads` gains `agent_type` (schema-on-write, no TF/migration); poller computes `EFFECTIVE_AGENT` + writes `agent_type` back. `claude`/`codex`/`help` become reserved (github.commands shadow → `km doctor` WARN). No verb ⇒ byte-identical to today. Deploy: poller lives in `userdata.go` ⇒ `make build-lambdas` + `km init --dry-run=false` for remote create + existing sandboxes need `km destroy && km create`; bridge verb-parse via the same redeploy. Design spec: `docs/superpowers/specs/2026-06-07-github-bridge-agent-verbs-design.md`.

**Success Criteria** (what must be TRUE):
  1. A PR comment containing `/codex` dispatches the turn to Codex and persists `agent_type=codex` on the `(repo, number)` row; a subsequent no-verb comment in the same thread continues with Codex; `/claude` switches it back.
  2. With no agent verb and no stored `agent_type`, dispatch uses the profile default — byte-identical to today.
  3. The agent verb is parsed anywhere in the comment, stripped from `{{args}}`, composes with a Phase 99 template command (`/codex /patch …`), and two distinct agent verbs in one comment produce an error reply with no dispatch.
  4. A cross-agent switch (stored `agent_type` differs from the verb) starts a FRESH session for the new agent (no `--resume` of the other agent's session) and overwrites `agent_session_id` + `agent_type`.
  5. `/codex` on a Claude-only profile posts a helpful comment (no Codex here) rather than a silent failure / stranded turn.
  6. `claude`, `codex`, `help` are reserved: a `github.commands` entry shadowing them is ignored with a `km doctor` WARN.
  7. All Phase 97/98/99 success criteria continue to hold (no regression to dispatch, thread continuity, or command parsing).

**Requirements**: GH-AGENT-VERB, GH-AGENT-PERSIST, GH-AGENT-SWITCH, GH-AGENT-POLLER, GH-AGENT-PROFILE, GH-AGENT-E2E (phase-local synthetic IDs — see REQUIREMENTS.md § Phase 98)
**Depends on:** Phase 99 (parser) + Phase 98 (km-github-threads); independent of Phases 100/101
**Plans:** 5/5 plans complete

Plans:
- [ ] 102-01-PLAN.md — Bridge verb parser (/claude, /codex) + GitHubEnvelope.Agent + two-verb conflict reply [GH-AGENT-VERB]
- [ ] 102-02-PLAN.md — km-github-threads agent_type read/write (LookupSandbox + UpdateSession) [GH-AGENT-PERSIST]
- [ ] 102-03-PLAN.md — GitHub poller EFFECTIVE_AGENT precedence + cross-agent switch + codex-missing guard + agent_type write-back [GH-AGENT-POLLER, GH-AGENT-SWITCH, GH-AGENT-PROFILE]
- [ ] 102-04-PLAN.md — km doctor reserved-shadow (claude/codex/help) + /help agent listing + docs (github-bridge.md + CLAUDE.md) [GH-AGENT-VERB, GH-AGENT-PROFILE]
- [ ] 102-05-PLAN.md — Deploy-surface audit + live GH-AGENT-E2E UAT (checkpoint) [GH-AGENT-E2E]

### Phase 103: HackerOne comment-trigger bridge — km-h1-bridge Lambda dispatches HackerOne webhook events to per-program sandbox agents (auto-triage + comment-keyword), internal-by-default replies

**Goal:** A HackerOne program webhook can drive a sandbox agent turn the same way a GitHub PR comment does (Phase 97-102): a single `km-h1-bridge` Lambda Function URL HMAC-verifies the `X-H1-Signature`, dedupes by `X-H1-Delivery`, resolves the report's program handle to one-or-more sandbox targets from `h1.programs:` in km-config.yaml, and dispatches an agent turn (warm FIFO / cold create / resume) with report-id-keyed thread continuity — via TWO trigger models (opt-in lifecycle-event auto-triage + configurable @-handle comment-keyword), config-driven event→prompt mappings, multi-target fanout, and a reply path that is INTERNAL by default with an allowlist-gated `/reply_to_researcher` for researcher-visible replies. The agent posts back through a new `cmd/km-h1` helper using HackerOne customer-API Basic Auth.
**Requirements**: H1-BRIDGE-HMAC, H1-BRIDGE-DEDUP, H1-RESOLVE-PROGRAM, H1-TRIGGER-AUTOTRIAGE, H1-TRIGGER-MENTION, H1-COMMAND-PARSE, H1-AGENT-VERB, H1-EVENT-PROMPT-MAP, H1-FANOUT-MULTITARGET, H1-DISPATCH-3WAY, H1-THREAD-CONTINUITY, H1-REPLY-INTERNAL-DEFAULT, H1-REPLY-RESEARCHER-GATED, H1-HELPER-KM-H1, H1-CLI-INIT-STATUS, H1-DEPLOY-WIRING, H1-E2E (defined at plan time)
**Depends on:** Phase 102
**Plans:** 10/10 plans complete

Plans:
- [x] 103-01-PLAN.md — Wave 0: live HackerOne webhook payload capture + pin field paths + userdata dormancy golden
- [x] 103-02-PLAN.md — h1: config structs + merge-list wiring + pkg/h1/bridge resolve/interfaces
- [x] 103-03-PLAN.md — H1 payload + envelope + HMAC verify + command/agent-verb parser (+ /reply_to_researcher)
- [x] 103-04-PLAN.md — webhook_handler: two-trigger gate, dedup, multi-target fanout, 3-way dispatch, thread continuity, safety-critical reply gate
- [x] 103-05-PLAN.md — cmd/km-h1 sandbox helper (comment/state/read, Basic Auth, internal-by-default)
- [x] 103-06-PLAN.md — km h1 init/status CLI (mint secret + Basic-Auth -> SSM; no manifest)
- [x] 103-07-PLAN.md — cmd/km-h1-bridge Lambda entry + lambda-h1-bridge TF module v1.0.0 + live unit
- [x] 103-08-PLAN.md — deploy wiring: regionalModules/lambdaBuilds/sidecarBuilds + KM_H1_* env + SSM publish + dynamodb-h1-threads + create_h1_inbound (DLQ) + Makefile
- [x] 103-09-PLAN.md — km-h1-inbound-poller userdata + notification.h1.inbound schema + profiles/h1-triage.yaml (dormant by default)
- [x] 103-10-PLAN.md — deploy-surface guard tests + gated E2E harness + 103-UAT.md + live reply-visibility UAT

### Phase 104: Slack channel O(1) resolution on alias reuse — bounded lookup-first create-time resolution plus durable km-slack-channels alias store and km slack adopt

**Goal:** `km create` on a **reused `--alias`** (whose per-sandbox Slack channel already exists, e.g. `profiles/github-review.yaml` with `archiveOnDestroy:false`) must resolve the existing channel in **bounded, O(1)** time — never the unbounded `conversations.list` workspace enumeration that, in a corporate Slack with thousands of channels, wedges the **900s create-handler Lambda** and strands the sandbox in `starting`. Root cause (confirmed against the code): `resolveExistingChannelID` (`create_slack.go:152`) gates the SSM by-name cache hit on `conversations.info(cachedID) == nil-err`, so **any** transient info error (a momentary `ratelimited`/5xx/context blip) falls through to `FindChannelByName` — a bare `for{}` with `limit:1000`, no page cap, no sub-deadline (freshly-created channels sort LAST, so the scan walks every page). The fix is three layers from the design spec: **P0** wrap resolution in a ~45s wall-clock sub-context + hard page cap on the scan (**default OFF** ⇒ fail-fast); **P1** look up the stored ID *before* `conversations.create` and classify `conversations.info` errors so only a definitive `channel_not_found` invalidates the mapping (transient ⇒ bounded-retry 2× then optimistically trust the ID, **never enumerate**); **P2** add a durable, authoritative `km-slack-channels` DynamoDB table (PK `alias`, no TTL — survives destroy, unlike the deleted-on-destroy `km-sandboxes` row, and unlike a synthetic `km-sandboxes` item it doesn't pollute the `Scan`-based `km list`) read first and written through on create/resolve, with the existing SSM by-name cache kept as a back-compat fallback, plus a **`km slack adopt <alias> <channelID>`** operator escape hatch for genuinely orphaned channels. The bridge is NOT a consumer ⇒ no `lambda-slack-bridge` changes; `notification.slack.channelOverride` remains the documented zero-lookup manual escape. Design spec: `docs/superpowers/specs/2026-06-10-slack-channel-reuse-o1-resolution-spec.md`; implementation plan: `docs/superpowers/plans/2026-06-10-slack-channel-o1-resolution.md`.

**Success Criteria** (what must be TRUE):
  1. Reuse with a stored ID + live channel resolves O(1): no `conversations.create`, no `conversations.list`; `slack_resolve path=cache_hit` logged.
  2. Reuse with a stored ID + **transient** `conversations.info` error does NOT enumerate — bounded-retry then optimistically uses the stored ID (`path=cache_optimistic`). (This is the exact defeater that caused the incident.)
  3. Reuse with a stored ID that returns definitive `channel_not_found` invalidates the mapping and recreates the channel cleanly, rewriting the store to the new ID.
  4. `name_taken` with no stored mapping and scan disabled (default `KM_SLACK_MAX_SCAN_PAGES=0`) **fails fast** (`path=failfast`, <1 min) with `km slack adopt` / `channelOverride` guidance — never an unbounded scan; opt-in `KM_SLACK_MAX_SCAN_PAGES>0` runs a page-capped scan that fails fast on cap-exceed.
  5. Total per-sandbox Slack resolution can never exceed `KM_SLACK_RESOLVE_BUDGET` (default 45s) ≪ the 900s create-handler ceiling; on budget exceed the create aborts fast (no infra provisioned yet) with a clear next step.
  6. A fresh-alias create writes the `(alias → channel_id)` mapping to the `km-slack-channels` DDB table AND the SSM by-name cache; a later recreate on the same alias hits the O(1) DDB path.
  7. `km slack adopt <alias> <channelID>` validates `^C[A-Z0-9]+$` + bot membership, then write-throughs to DDB + SSM so the next reuse is O(1); bad ID / non-member is rejected with actionable guidance.
  8. Full deploy surface present and verified: TF module + live unit + `init.go` regional-module entry + create-handler IAM (var→policy→wiring→live input) + config getter + Go store helper + runtime table-name derivation (`{prefix}-slack-channels`); `km doctor` reports the table's presence. Multi-install prefixes don't collide.
  9. No SandboxProfile schema change; existing SSM by-name entries keep working; no regression to Slack Mode-1 (shared) / Mode-3 (override) resolution. With `slack.enabled:false`, create is byte-identical.

**Requirements**: SLACK-CHAN-BOUND, SLACK-CHAN-LOOKUP, SLACK-CHAN-INFO-CLASS, SLACK-CHAN-STORE, SLACK-CHAN-ADOPT, SLACK-CHAN-DEPLOY, SLACK-CHAN-E2E (defined at plan time; see design spec §3 constraints + §7 failure-mode matrix)
**Depends on:** Existing Slack per-sandbox create path (Phases 72 invites + 91 inbound + 95/96 routing) and the SSM by-name cache (`05a4415e`, v0.4.901). **Independent of Phase 103** (HackerOne) — sequenced after it only by phase number.
**Plans:** 5/5 plans complete

Plans:
- [ ] 104-01-PLAN.md — P0+P1 core: bounded `FindChannelByName` (page cap + ctx-per-page + `ErrScanCapExceeded`), `IsChannelNotFound` classifier, and the lookup-first/budgeted `resolveSlackChannel` state machine with `slack_resolve` observability [SLACK-CHAN-BOUND, SLACK-CHAN-LOOKUP, SLACK-CHAN-INFO-CLASS]
- [ ] 104-02-PLAN.md — `dynamodb-slack-channels` TF module (PK `alias`, no TTL) + live terragrunt unit + `init.go` regional-module registration [SLACK-CHAN-STORE, SLACK-CHAN-DEPLOY]
- [ ] 104-03-PLAN.md — create-handler IAM (km-operator-policy var+policy, create-handler var+wiring, live input) + `GetSlackChannelsTableName` config getter + `pkg/aws.SlackChannelStore` helper + wire store into `km create` resolution [SLACK-CHAN-STORE, SLACK-CHAN-DEPLOY]
- [ ] 104-04-PLAN.md — `km slack adopt <alias> <channelID>` (validate + membership + DDB/SSM write-through) + `km doctor` table-existence check [SLACK-CHAN-ADOPT]
- [ ] 104-05-PLAN.md — docs (`slack-notifications.md` + `CLAUDE.md` phase note + deploy sequence) + deploy-surface audit + live large-workspace UAT (the spec's Phase 0 confirmation, shipped inline via the `slack_resolve` log) [SLACK-CHAN-E2E, SLACK-CHAN-DEPLOY]

### Phase 105: Scoped km init for bridge config — km init --only <module> with sugar aliases --github, --slack, --h1 to apply a single terragrunt module and refresh its Lambda env/IAM instead of the full apply

**Goal:** Let an operator push a bridge config-key edit (`github.*` / `slack.*` / `h1.*` in `km-config.yaml`) into the owning Lambda's env block by applying ONLY that module, instead of the full ~27-module `km init`. Add `km init --only <module>` plus sugar aliases `--github` → `lambda-github-bridge`, `--slack` → `lambda-slack-bridge`, `--h1` → `lambda-h1-bridge`. **Option A (scoped terragrunt apply):** filter the existing `regionalModules()` loop to the selected module and still run `ExportTerragruntEnvVars(cfg)` first, so the module recomputes its env block from yaml-derived `KM_*` vars and stays fully inside terraform state (zero drift; picks up IAM changes too). Chosen over a direct `UpdateFunctionConfiguration` env poke (Option B) because A has no drift footgun and reuses the existing apply loop rather than a parallel deploy path.

**Scope / key decisions:**
- New flag `--only <module>` validated against a **curated allowlist** — NOT all ~27 `regionalModules()`. Rationale (operator, 2026-06-11): the goal is fast iteration on slack/github/h1/email config edits; full-fleet `--only` access is "too much rope." An unknown/out-of-allowlist `--only` value errors with the allowed set listed.
- **Two tiers in the allowlist:**
  - **Tier 1 — cheap (env+IAM, no destroy-class resources):** `lambda-github-bridge`, `lambda-slack-bridge`, `lambda-h1-bridge`, `email-handler`. Exposed via sugar `--github` / `--slack` / `--h1` / `--email`. Fast, no confirmation needed (same safety profile — all are Lambdas with an `environment { variables }` block + scoped IAM).
  - **Tier 2 — gated (destroy-class):** `ses`. Reachable ONLY via explicit `--only ses` — **no cheap alias** — and MUST route through the destroy-class safety gate (`aws_ses_domain_identity`, `aws_ses_domain_dkim`, `aws_route53_record` DKIM/MX/verification, `aws_ses_receipt_rule`, `aws_s3_bucket_policy`; `ses` is also the last module + owns the consolidated bucket policy). A scoped `ses` apply runs the same curated destroy-class trip-block as `km init --plan` and refuses to apply a protected destroy/replace without `--i-accept-destroys`. This is the "did the dns and all that" path — deliberately heavier and explicit, not one-keystroke.
- `--email` → `email-handler` only; the SES/DNS layer is NOT touched by the `--email` alias (it's `--only ses`).
- Implement the allowlist as named slices (tier-1 cheap vs tier-2 gated) so adding a future target is a one-line change + a tier choice.
- Mutually exclusive with `--sidecars`/`--lambdas`/`--plan` (guard in the `init.go:583-601` dispatch block). NOTE: tier-2 `--only ses` REUSES the destroy-class gate machinery (`RunInitPlanFunc`/curated trip-block) but as a pre-apply gate, not a standalone plan — confirm wiring at plan time.
- `runInitScoped()` reuses `RunInitWithRunner` (`init.go:1794-1999`) filtered to one module dir; upstream `outputs.json` already exist on a live install so `dependency` blocks resolve.
- Boundary to DOCUMENT: scoped apply refreshes env + IAM for that module but NOT a stale code zip (still `make build-lambdas` + `--lambdas`) and NOT new resources/wiring (new table/queue ⇒ full `km init`).
- CLI-only, operator-side: no SandboxProfile schema change, no new TF resource. Deploy = `make build` the km binary.

**Requirements**: INIT-SCOPED-FLAG, INIT-SCOPED-ALIASES, INIT-SCOPED-GUARD, INIT-SCOPED-IMPL, INIT-SCOPED-TESTS, INIT-SCOPED-DOCS
**Depends on:** None structural (operates on the existing bridge modules from Phases 97/103/95). Relates to the deferred Slack-auto-start idea discussed same session.
**Plans:** 1/5 plans executed

Plans:
- [ ] 105-01-PLAN.md — Wave 0 TDD scaffold: init_scoped_test.go with 10 named stub tests [INIT-SCOPED-TESTS]
- [ ] 105-02-PLAN.md — Flags + two-tier curated allowlist + resolveScopedModule + mutual-exclusion guard [INIT-SCOPED-FLAG, INIT-SCOPED-ALIASES, INIT-SCOPED-GUARD]
- [ ] 105-03-PLAN.md — Tier-1 runInitScoped/RunInitScopedWithRunner (ExportTerragruntEnvVars + SSM republish + single-module apply; dry-run honored) [INIT-SCOPED-IMPL]
- [ ] 105-04-PLAN.md — Tier-2 --only ses gate: planModule + planreport.Evaluate + SES preflight/Reconfigure (pre-apply destroy-class) [INIT-SCOPED-GUARD, INIT-SCOPED-IMPL]
- [ ] 105-05-PLAN.md — Docs (7 surfaces) + live UAT (no-drift invariant + ses no-op) [INIT-SCOPED-DOCS, INIT-SCOPED-IMPL]
