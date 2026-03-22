---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
status: planning
stopped_at: Completed 05-configui-01-PLAN.md
last_updated: "2026-03-22T17:42:43.197Z"
last_activity: 2026-03-21 — Roadmap revised; ECS added as v1 substrate; PROV-09, PROV-10 added; total v1 requirements now 45
progress:
  total_phases: 6
  completed_phases: 4
  total_plans: 23
  completed_plans: 20
  percent: 0
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-03-21)

**Core value:** A sandbox is a declarative policy object that compiles into a controlled, auditable execution environment
**Current focus:** Phase 1 — Schema, Compiler & AWS Foundation

## Current Position

Phase: 1 of 5 (Schema, Compiler & AWS Foundation)
Plan: 0 of TBD in current phase
Status: Ready to plan
Last activity: 2026-03-21 — Roadmap revised; ECS added as v1 substrate; PROV-09, PROV-10 added; total v1 requirements now 45

Progress: [░░░░░░░░░░] 0%

## Performance Metrics

**Velocity:**
- Total plans completed: 0
- Average duration: —
- Total execution time: 0 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| - | - | - | - |

**Recent Trend:**
- Last 5 plans: —
- Trend: —

*Updated after each plan completion*
| Phase 01-schema-compiler-aws-foundation P01 | 5 | 2 tasks | 14 files |
| Phase 01-schema-compiler-aws-foundation P02 | 25 | 2 tasks | 22 files |
| Phase 01-schema-compiler-aws-foundation P04 | 45 | 1 tasks | 21 files |
| Phase 02-core-provisioning-security-baseline P02 | 4 | 2 tasks | 9 files |
| Phase 02-core-provisioning-security-baseline P01 | 353s | 2 tasks | 12 files |
| Phase 02-core-provisioning-security-baseline P03 | 8 | 2 tasks | 6 files |
| Phase 03-sidecar-enforcement-lifecycle-management P03 | 7min | 2 tasks | 5 files |
| Phase 03-sidecar-enforcement-lifecycle-management P02 | 12min | 2 tasks | 6 files |
| Phase 03-sidecar-enforcement-lifecycle-management P01 | 8min | 2 tasks | 6 files |
| Phase 03-sidecar-enforcement-lifecycle-management P00 | 15 | 2 tasks | 14 files |
| Phase 03-sidecar-enforcement-lifecycle-management P04 | 568s | 3 tasks | 14 files |
| Phase 03-sidecar-enforcement-lifecycle-management P05 | 10min | 2 tasks | 9 files |
| Phase 04-lifecycle-hardening-artifacts-email P01 | 237s | 2 tasks | 8 files |
| Phase 04-lifecycle-hardening-artifacts-email P02 | 3min | 2 tasks | 5 files |
| Phase 04-lifecycle-hardening-artifacts-email P03 | 5min | 3 tasks | 10 files |
| Phase 04-lifecycle-hardening-artifacts-email P04 | 371s | 2 tasks | 8 files |
| Phase 04-lifecycle-hardening-artifacts-email P05 | 282s | 2 tasks | 11 files |
| Phase 02-core-provisioning-security-baseline P04 | 1 | 2 tasks | 0 files |
| Phase 05-configui P01 | 426s | 2 tasks | 9 files |

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- [Roadmap]: SG-first security model must be established in Phase 2 — VPC Security Groups are the real enforcement boundary; proxy sidecars are a policy layer on top
- [Roadmap]: Profile inheritance semantics (child overrides parent, no additive merge on allowlists) must be defined and tested in Phase 1 before any IAM compilation code is written
- [Roadmap]: INFR (AWS account setup) is assigned to Phase 1 because provisioning in Phase 2 depends on the account structure, Route53, KMS, and S3 being present
- [Roadmap]: MAIL (email/SES) is assigned to Phase 4 alongside artifact hardening — it depends on a working sandbox runtime but is independent of the sidecar enforcement layer
- [Roadmap revision 2026-03-21]: ECS/Fargate is a v1 substrate alongside EC2 — `runtime.substrate: ec2 | ecs` is the selection mechanism; the compiler must produce different Terragrunt artifacts per substrate; Phase 2 includes both ec2-instance and ecs-cluster/ecs-task/ecs-service modules from defcon.run.34
- [Roadmap revision 2026-03-21]: Sidecar model differs by substrate — EC2 sidecars are OS-level processes injected into the instance; ECS sidecars are additional containers in the Fargate task definition; NETW-02, NETW-03, OBSV-01, OBSV-02 must work on both
- [Roadmap revision 2026-03-21]: Kubernetes (k8s/EKS) positioned as v2 PLAT-01; Docker/local substrate remains out of scope for v1
- [Phase 01-schema-compiler-aws-foundation]: go:embed requires schema inside package directory tree — schema lives at schemas/ root for tooling and pkg/profile/schemas/ for embedding
- [Phase 01-schema-compiler-aws-foundation]: ValidateSchema uses YAML->JSON->jsonschema pipeline; jsonschema/v6 AddResource requires parsed JSON value not raw bytes
- [Phase 01-02]: Network module security groups have no egress — Phase 2 profile compiler adds per-profile egress rules based on allowlists
- [Phase 01-02]: ECS service module has no load balancer — sandboxes use service discovery; FARGATE_SPOT preferred capacity strategy
- [Phase 01-02]: ec2spot IMDSv2 enforced (http_tokens=required); SSH removed; SSM-only access
- [Phase 01-04]: CLI architecture: cmd/ entry point -> internal/app/cmd/ Cobra commands -> pkg/ libraries (tiogo pattern)
- [Phase 01-04]: km validate adds file's directory to search paths for extends resolution; schema validation on child bytes, semantic on merged struct
- [Phase 01-04]: Plan 03 artifacts (inherit.go, builtins.go) implemented as Rule 3 auto-fix — blocking dependency for Plan 04
- [Phase 02-core-provisioning-security-baseline]: BuildXxxCommand methods expose exec.Cmd for test inspection without executing terragrunt — preserves testability while keeping Apply/Destroy simple
- [Phase 02-core-provisioning-security-baseline]: ErrSandboxNotFound defined as package-level sentinel — callers use errors.Is() for typed handling in destroy path
- [Phase 02-01]: Baseline SG egress: TCP 443 + UDP 53 to 0.0.0.0/0 in Phase 2; Phase 3 tightens when proxy sidecars enforce per-host filtering
- [Phase 02-01]: sg_egress_rules and iam_session_policy serialized into service.hcl module_inputs — Terragrunt passes them as Terraform variables automatically (NETW-01/NETW-04 reach AWS)
- [Phase 02-01]: Compiler pattern: pure function Compile(profile, sandboxID, onDemand) -> CompiledArtifacts; text/template for HCL generation, never fmt.Sprintf
- [Phase 02-core-provisioning-security-baseline]: findRepoRoot() walks up from source path anchor then falls back to cwd — works in both tests and production without environment variables
- [Phase 02-core-provisioning-security-baseline]: AWS credential validation is the gate between profile parsing and compilation — STS GetCallerIdentity called before any compile or filesystem work
- [Phase 02-core-provisioning-security-baseline]: destroy reconstructs minimal sandbox dir from template when missing locally — only sandbox_id in service.hcl for Terragrunt state key resolution
- [Phase 03-03]: ExitStatus stored as *int in MLflowRun so exit_status=0 (success) is preserved through JSON omitempty serialization
- [Phase 03-03]: S3RunAPI narrow interface (PutObject + GetObject) for MLflow run logging — real *s3.Client satisfies it directly
- [Phase 03-03]: OTel sidecar config uses env-var substitution for AWS_REGION/OTEL_S3_BUCKET/SANDBOX_ID — zero Go config parsing needed
- [Phase 03-02]: Package layout: auditlog.go (package auditlog) + cmd/main.go (package main) in subdirectory — Go disallows two packages in one directory; cmd/ pattern separates library from binary
- [Phase 03-02]: CloudWatchBackend interface in auditlog package decouples sidecar from AWS SDK — tests run without credentials; CWLogsAPI interface in pkg/aws follows same narrow-interface pattern as TagAPI
- [Phase 03-01]: DNS/HTTP proxy library code in dnsproxy/httpproxy subdirs to resolve Go package conflict between library and main
- [Phase 03-01]: InjectTraceContext() exported as testable function since goproxy CONNECT handler chain breaks on first non-nil result
- [Phase 03]: Sidecar library packages use subdirectories (dnsproxy/, httpproxy/, auditlog/) with package main at parent for binary entry points
- [Phase 03]: Wave-0 stubs: dns-proxy, audit-log, http-proxy packages pre-built by linter; scheduler, lifecycle, list-cmd, status-cmd remain as failing stubs for Plans 03-04/03-05
- [Phase Phase 03-04]: SandboxMetadata defined in pkg/aws/metadata.go — sandbox.go stub expected Plan 03-04 to create it
- [Phase Phase 03-04]: DeleteTTLSchedule called BEFORE terragrunt destroy — schedule cancelled even if destroy partially fails
- [Phase Phase 03-04]: TTL schedule creation is non-fatal in km create — sandbox provisioned even if EventBridge call fails
- [Phase 03-05]: SandboxLister/SandboxFetcher DI interfaces exported (uppercase) so cmd_test (external package) can inject fakes without AWS credentials
- [Phase 03-05]: SandboxRecord placed in pkg/aws (not cmd) so it can be shared with future plans that read sandbox state
- [Phase 04-01]: Regex patterns compiled once at NewRedactingDestination construction — safe for concurrent use, zero allocation per Write call
- [Phase 04-01]: UploadArtifacts returns ArtifactSkippedEvent slice for size-limit violations; PutObject failures are logged but not returned
- [Phase 04-01]: S3PutAPI narrow interface (PutObject only) for artifact uploads — mirrors S3RunAPI pattern from mlflow.go
- [Phase 04-lifecycle-hardening-artifacts-email]: SES receipt rule 'position' attribute removed — not supported in provider v6 at rule level (only on actions)
- [Phase 04-lifecycle-hardening-artifacts-email]: CleanupSandboxEmail swallows sesv2types.NotFoundException for idempotent km destroy retries
- [Phase 04-03]: IMDS token TTL changed from 60s to 21600s — spot poll loop runs for hours, 60s token would expire
- [Phase 04-03]: Two-step bind mount required for EC2 read-only enforcement (mount --bind then remount,bind,ro)
- [Phase 04-03]: ECS Fargate writable volumes use scope=task named volumes, not linuxParameters.tmpfs (Fargate does not support tmpfs)
- [Phase 04-03]: UploadArtifacts called for ALL teardown policies including retain — data preservation always desired
- [Phase 04-lifecycle-hardening-artifacts-email]: Profile YAML stored in S3 at artifacts/{sandbox-id}/.km-profile.yaml to enable destroy-path artifact upload without passing profile through command args
- [Phase 04-lifecycle-hardening-artifacts-email]: SES IAM uses ses:FromAddress StringEquals condition — each sandbox can only send from its own address, preventing cross-sandbox email abuse
- [Phase 04-lifecycle-hardening-artifacts-email]: S3 replication excludes mail/ prefix — inbox objects are ephemeral; only artifacts/ is replicated for durability
- [Phase 04-lifecycle-hardening-artifacts-email]: TTL Lambda scope: artifact upload + notification + schedule cleanup only; actual terragrunt destroy delegated (Lambda has no km binary)
- [Phase 04-lifecycle-hardening-artifacts-email]: OnNotify/OnIdleNotify: optional callbacks (nil-safe, best-effort) — backward compatible; past-tense event names match ses.go convention
- [Phase 02-core-provisioning-security-baseline]: All 6 E2E tests passed on real AWS — EC2 spot, EC2 on-demand, ECS Fargate Spot, EC2 destroy, ECS destroy, secrets injection verified with zero orphaned resources
- [Phase 02-core-provisioning-security-baseline]: IMDSv2 enforced on EC2 (http_tokens=required) and SSM-only access confirmed on real AWS infrastructure
- [Phase 05-configui]: package main for all cmd/configui files — Go prohibits two packages per directory; handler logic co-located with main since no separate library consumer
- [Phase 05-configui]: buildTestTemplates() in handlers.go with inline template strings — test isolation without filesystem; truncateID no-op registered for test compatibility
- [Phase 05-configui]: handleSandboxLogs graceful degradation when cwClient nil or call fails — logs are informational, not critical path

### Pending Todos

None yet.

### Blockers/Concerns

- [Phase 2]: ECS substrate introduces a second Terraform module path (ecs-cluster, ecs-task, ecs-service) — compiler branch logic for substrate selection needs careful design to avoid divergence
- [Phase 3]: On ECS, DNS and HTTP proxy sidecars run as containers in the task definition; iptables DNAT rules used for EC2 interception do not apply — ECS needs a different traffic interception approach (likely environment-variable proxy configuration or VPC endpoint routing)
- [Phase 3]: iptables DNAT interaction with IMDSv2 hop limit not fully resolved on EC2 — research recommends `/gsd:research-phase` before Phase 3 planning
- [Phase 3]: HTTPS proxy mode (SNI-only vs. full MITM) is a security trade-off that needs an explicit decision before Phase 3 implementation
- [Phase 4]: Filesystem policy enforcement mechanism (seccomp, Linux mount namespaces, OverlayFS) not decided — research recommends `/gsd:research-phase` before Phase 4 planning

## Session Continuity

Last session: 2026-03-22T17:42:43.195Z
Stopped at: Completed 05-configui-01-PLAN.md
Resume file: None
