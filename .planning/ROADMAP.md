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
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9 → 10 → 11 → 12 → 13 → 14

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
**Plans:** 2 plans

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
**Plans:** 0 plans

Plans:
- [ ] TBD (run /gsd:plan-phase 16 to break down)
