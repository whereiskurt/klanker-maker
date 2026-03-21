# Project Research Summary

**Project:** Fabric — Policy-Driven Sandbox Platform
**Domain:** Policy-driven sandbox / execution environment platform (AI agent workloads)
**Researched:** 2026-03-21
**Confidence:** HIGH

## Executive Summary

Fabric is a policy-driven sandbox platform for running AI agent workloads (and arbitrary execution environments) on EC2, where the source of truth is a declarative `SandboxProfile` YAML object that compiles to Terraform/Terragrunt provisioning artifacts. The industry pattern — validated by Kubernetes agent-sandbox, NVIDIA OpenShell, E2B, Modal, and Fly Machines — is converging on declarative-profile-first sandbox definitions with explicit allow-lists for network egress, secrets access, and identity scope. Fabric's differentiation is that it takes this pattern further than any managed platform: profile inheritance, four named built-in profiles, fine-grained filesystem policy, GitHub source allowlists, and full audit trails are absent from every surveyed competitor. The recommended build order follows architectural dependency: schema first, compiler next, Terraform modules in parallel, then CLI, then sidecars, then ConfigUI.

The primary security risk is an enforcement model inversion: teams typically treat the DNS/HTTP proxy sidecars as the security boundary, when they are actually a policy and audit layer. The real enforcement boundary is the VPC Security Group — the proxy can be killed from userspace by the workload. Research identifies this as the single most consequential architectural mistake. The correct posture is SG-first (egress blocked at the VPC layer; proxy is gatekeeper on top), IMDSv2 enforced, and iptables rules configured at user-data bootstrap. This must be established in Phase 1, not retrofitted.

The secondary risk cluster is operational: Terraform destroy orphaning resources (especially with the inherited `aws_spot_instance_request` module), IAM policy over-broadening through inheritance flattening, and Terragrunt provider cache corruption under concurrent sandbox creation. All three are well-understood with specific mitigations. The schema-first build order prevents most of them — the profile compiler's inheritance semantics must be defined and tested before any provisioning code is written.

## Key Findings

### Recommended Stack

The Go CLI layer uses Cobra (v1.10.2) + Viper (v1.21.0) + zerolog (v1.34.0), with `goccy/go-yaml` for YAML parsing (the old `gopkg.in/yaml.v3` is archived) and `santhosh-tekuri/jsonschema/v6` for JSON Schema Draft 2020-12 validation. AWS integration uses `aws-sdk-go-v2` (v1.41.4). Infrastructure provisioning uses OpenTofu 1.9.1 + Terragrunt 0.77.x — Terraform OSS (BSL) is EOL July 2025 and should not be used in new projects. Sidecar binaries use `miekg/dns` v1.1.72 for DNS proxy and `elazarl/goproxy` v1.8.2 for HTTP/HTTPS proxy. The ConfigUI is React 19 + Vite 8 (Rolldown bundler) + shadcn/ui (Tailwind v4) + TanStack Query v5, served as a static bundle embedded in the Go HTTP server via `//go:embed dist/*`.

**Core technologies:**
- Go 1.23+ / Cobra + Viper: CLI command tree and config hierarchy — de facto standard, used by kubectl and docker CLI
- OpenTofu 1.9.1 + Terragrunt 0.77.x: IaC engine — OpenTofu is the MPL 2.0 fork of Terraform, drop-in replacement; Terraform OSS BSL is EOL
- aws-sdk-go-v2: AWS API calls (EC2, IAM, SSM, STS, CloudWatch) — v1 is EOL; v2 is modular and context-aware
- miekg/dns v1.1.72 + elazarl/goproxy v1.8.2: Sidecar proxy binaries — canonical Go DNS and HTTP proxy libraries; production-proven
- goccy/go-yaml: YAML parsing for SandboxProfile — actively maintained replacement for archived gopkg.in/yaml.v3
- santhosh-tekuri/jsonschema/v6: Schema validation — Draft 2020-12 compliance; correct choice for external/user-facing schemas
- React 19 + Vite 8 + shadcn/ui: ConfigUI — shadcn copies components into project (no upstream breakage); Vite 8 Rolldown is 10-30x faster build

### Expected Features

**Must have (table stakes):**
- Isolated EC2 execution environment — VM-level isolation; stronger than containers by default
- SandboxProfile YAML schema (apiVersion/kind/metadata/spec) — declarative config as the source of truth; everything derives from it
- `fabric validate` — schema validation before any AWS spend begins
- `fabric create / destroy / list / status` — standard lifecycle CLI for any IaC-adjacent tool
- DNS proxy sidecar (miekg/dns) — network egress allowlist enforcement at DNS layer
- HTTP/HTTPS proxy sidecar (elazarl/goproxy) — network egress allowlist enforcement at HTTP layer
- Audit log sidecar (zerolog → CloudWatch/S3) — append-only command + network log; compliance and forensics requirement
- IAM role scoping — session-scoped, region-locked credentials via STS AssumeRole
- Secrets injection via SSM Parameter Store — allowlist mode; secrets never in compiled artifacts
- TTL-based auto-destroy — sandboxes are not pets; all leading platforms enforce session limits
- Four built-in profiles (open-dev / restricted-dev / hardened / sealed) — operator ladder of constraint; reduces decision fatigue
- ConfigUI — profile editor + live sandbox status (adapted from defcon.run.34)

**Should have (differentiators):**
- Profile inheritance (`extends`) — base hardened profile once; override only what differs; not offered by any competitor
- GitHub source access allowlist — repo/ref/permission allowlist; Goose-on-Fabric primary use case
- Filesystem policy enforcement (writable/read-only paths) — prevents ~/.zshrc attacks; not enforced at profile level by any managed platform
- Artifact upload on exit — controlled upload with size limits; prevents data loss and exfiltration
- Secret redaction in audit logs — pattern-based redaction to prevent accidental credential capture
- AWS resource discovery in ConfigUI — operators see what the sandbox actually created

**Defer (v2+):**
- ECS/Fargate substrate — only if EC2 startup latency is measured as a blocker
- Sandbox REST API server — only if programmatic access is measured as needed
- Warm pool / pre-provisioned sandboxes — only if Terragrunt apply latency is unacceptable after measurement
- Multi-cloud (GCP, Azure) — schema is already cloud-neutral; implementation can follow
- OPA policy engine — defer unless enterprise compliance mandates it
- Cost budgeting / spend limits — TTL auto-destroy is the primary cost guard; AWS Budgets handles the rest

### Architecture Approach

The architecture has four distinct layers: Operator Interface (CLI + ConfigUI), Compilation (SandboxProfile YAML → Terragrunt inputs + EC2 user-data), Provisioning (Terragrunt → OpenTofu → AWS), and Sandbox Instance (workload process + three sidecar processes). The critical insight from architecture research is that the Profile Compiler is the central intelligence — it is a Go library that translates the declarative schema into static HCL artifacts and a bootstrap shell script. The CLI never calls AWS APIs for provisioning directly; Terraform handles all provisioning and idempotency. Sidecars enforce policy transparently via iptables DNAT rules, making the workload unaware of the enforcement layer. The build order is strictly determined by dependency: schema → compiler → Terraform modules (parallel) → CLI commands → sidecars → ConfigUI.

**Major components:**
1. SandboxProfile YAML schema (`pkg/profile/`) — root dependency; everything else reads from it
2. Profile Compiler (`pkg/compiler/`) — translates profile to Terragrunt inputs (HCL/JSON) + EC2 user-data bootstrap script
3. Terraform modules (`infra/modules/network`, `ec2spot`, `iam`, `secrets`) — adapted from defcon.run.34; never modified by the CLI
4. Sidecar processes (`sidecars/dns-proxy`, `http-proxy`, `audit-log`) — injected via user-data; iptables DNAT intercepts all traffic
5. Go CLI (`cmd/fabric/`, `internal/app/cmd/`) — user-facing lifecycle; wraps compiler + Terragrunt runner
6. ConfigUI (`ui/`) — React 19 + Go HTTP server BFF; served as embedded static bundle

### Critical Pitfalls

1. **Sidecar proxies are not the security boundary** — The proxy runs in the same OS trust domain as the workload; any process with shell access can kill it or bypass iptables. Fix: VPC Security Groups (and VPC NACLs) must block direct internet egress; proxy is the policy layer on top of the hard SG boundary. Establish this in Phase 1 before any real workloads run.

2. **Terraform destroy leaves orphaned resources** — The inherited `aws_spot_instance_request` module does not terminate the actual EC2 instance on destroy. Fix: tag every resource at creation with `fabric:sandbox-id=<id>`; implement a `fabric gc` command that finds all tagged resources without corresponding Terraform state; test destroy with an actively-running workload.

3. **IAM inheritance produces over-broad policies** — If the profile compiler flattens `extends` inheritance with additive merge on allowedActions, a child profile can never be more restrictive than its parent. Fix: define inheritance semantics explicitly before writing the compiler — for allowlist fields, child values OVERRIDE parent values, not extend them. Write a test asserting a minimal child of `open-dev` produces a minimal IAM policy.

4. **IMDSv2 not enforced → credential theft via SSRF** — IMDSv1 accessible at `169.254.169.254` with no session token lets any HTTP library inside the workload harvest IAM credentials. Fix: set `http-tokens = required` and `http-put-response-hop-limit = 1` (or 2 if sidecars need IMDS) in every EC2 Terraform resource — never an afterthought.

5. **Concurrent Terragrunt creates corrupt provider lock files** — Two simultaneous `fabric create` commands sharing a `TF_PLUGIN_CACHE_DIR` produce "inconsistent lock file" errors. Fix: use Terragrunt's Provider Cache Server; give each sandbox its own working directory; pin provider versions explicitly.

## Implications for Roadmap

Based on the architectural dependency chain (schema → compiler → provisioning → sidecars → UI) and the pitfall-to-phase mapping from PITFALLS.md, the following phase structure is strongly recommended:

### Phase 1: YAML Schema + Compiler Foundation
**Rationale:** SandboxProfile schema is the root dependency for all other components. The compiler must have defined, tested inheritance semantics before any provisioning code is written. This phase is also when the most consequential pitfalls (IAM over-broadening, profile inheritance cycles) are easiest to prevent.
**Delivers:** Validated SandboxProfile YAML schema (apiVersion/kind/metadata/spec sections); `fabric validate` command; Profile Compiler producing HCL inputs and user-data; built-in profile YAML files (4); cycle detection and max-depth enforcement in the profile loader.
**Addresses features:** SandboxProfile YAML schema, `fabric validate`, four built-in profiles, profile inheritance spec (semantics defined now, implemented in P2).
**Avoids:** IAM inheritance over-broadening (define semantics here), profile cycle infinite loop, HCL string-interpolation anti-pattern (use JSON tfvars).

### Phase 2: Core Provisioning + Security Baseline
**Rationale:** With a working compiler producing artifacts, the provisioning layer can be built and tested. This phase must establish the SG-first enforcement model — it cannot be retrofitted. IMDSv2, resource tagging, and the teardown correctness baseline must be set here or they will never be set.
**Delivers:** Terraform modules (network, ec2spot, iam, secrets) adapted from defcon.run.34; `fabric create` and `fabric destroy` commands; VPC Security Group egress restriction (SG-first, not proxy-first); IMDSv2 enforced; sandbox-id tagging on all resources; concurrent create test passing.
**Uses:** OpenTofu 1.9.1, Terragrunt 0.77.x, aws-sdk-go-v2, `pkg/terragrunt` runner.
**Avoids:** SG allowing direct internet egress, IMDSv1, orphaned resources from spot teardown, Terragrunt lock corruption.

### Phase 3: Sidecar Enforcement + Lifecycle Management
**Rationale:** Sidecars require a provisioned EC2 instance to deploy into. Once provisioning works, the three sidecar processes (DNS proxy, HTTP proxy, audit log) can be built, embedded into user-data, and tested with real iptables rules. TTL and spot interruption handling belong here because they depend on the sidecar runtime.
**Delivers:** DNS proxy sidecar (miekg/dns); HTTP/HTTPS proxy sidecar (elazarl/goproxy); audit log sidecar (zerolog → CloudWatch/S3); iptables DNAT rules in user-data; TTL auto-destroy; spot interruption handling (`/latest/meta-data/spot/termination-time` polling); `fabric list` and `fabric status`; SSM secrets injection.
**Addresses features:** DNS/HTTP proxy sidecars, audit log, IAM role scoping, TTL auto-destroy, secrets injection, `fabric list/status`.
**Avoids:** Proxy-as-boundary mistake (SG already established in Phase 2), DNS proxy bypass via resolv.conf (immutable after bootstrap), audit sidecar killable by workload (systemd with Restart=always).

### Phase 4: Lifecycle Hardening + P2 Features
**Rationale:** Once the core sandbox lifecycle is working and dog-fooded, add the differentiating features that require schema stability: inheritance implementation, GitHub source controls, filesystem policy, artifact upload, and secret redaction. Also add the `fabric gc` orphan detector and the ConfigUI.
**Delivers:** Profile inheritance (`extends`) implemented and tested; `fabric gc` orphan detection; GitHub source access allowlist; filesystem policy enforcement; artifact upload on exit; secret redaction in audit logs; `fabric list` human context from metadata; destroy stored state (not profile file dependency).
**Addresses features:** Profile inheritance, GitHub controls, filesystem policy, artifact upload, secret redaction, P2 items from FEATURES.md.

### Phase 5: ConfigUI
**Rationale:** ConfigUI is explicitly a monitoring layer over the working system. It wraps `fabric` operations and requires CLI + AWS status endpoints to exist. Building it last eliminates rework caused by changing the underlying CLI interface.
**Delivers:** React 19 + Vite 8 + shadcn/ui frontend; Go HTTP server BFF (embedded static bundle via `//go:embed dist/*`); profile editor with inline `fabric validate`; live sandbox status via TanStack Query polling; AWS resource discovery view.
**Addresses features:** ConfigUI profile editor, live status, AWS resource discovery in ConfigUI.
**Uses:** React 19, Vite 8, shadcn/ui (Tailwind v4), TanStack Query v5, TanStack Table v8, React Router v7.

### Phase Ordering Rationale

- Schema must precede the compiler (compiler reads the schema), which must precede provisioning (provisioning uses compiled artifacts), which must precede sidecars (sidecars are injected into provisioned instances), which must precede the UI (UI wraps all of the above). This is a strict dependency chain.
- Profile inheritance semantics must be defined in Phase 1 (when the schema spec is written) even though the implementation lands in Phase 4. Getting semantics wrong after provisioning code is written is expensive to fix because it affects every IAM policy compilation.
- The SG-first security model must be established in Phase 2 alongside the Terraform modules — this is the only phase where it costs nothing to get it right and everything to get it wrong.
- ConfigUI in Phase 5 is intentional: the defcon.run.34 reference implementation already has a working ConfigUI to adapt. The risk is low; the benefit of having a working backend to test against is high.

### Research Flags

Phases likely needing deeper research during planning:
- **Phase 3 (Sidecars):** iptables DNAT interaction with IMDSv2 hop limit, HTTPS SNI-only allowlist vs. full MITM decision, CloudWatch agent systemd ordering — niche operational details not fully resolved in this research pass. Recommend `/gsd:research-phase` before Phase 3 planning.
- **Phase 4 (Lifecycle Hardening):** Filesystem policy enforcement mechanism (seccomp, Linux capabilities, or mount namespaces) — multiple approaches with different trade-offs; needs a decision before implementation. Recommend `/gsd:research-phase` before Phase 4 planning if filesystem policy is a Phase 4 commitment.

Phases with standard patterns (skip research-phase):
- **Phase 1 (Schema + Compiler):** JSON Schema 2020-12 validation with `santhosh-tekuri/jsonschema/v6` is well-documented. Go struct-based schema design is standard. No novel territory.
- **Phase 2 (Core Provisioning):** Terragrunt + OpenTofu patterns are well-established; defcon.run.34 modules provide the reference implementation. SG and IMDSv2 configuration is documented in official AWS sources.
- **Phase 5 (ConfigUI):** React 19 + Vite 8 + shadcn/ui stack is well-documented. defcon.run.34 has an existing ConfigUI to adapt. TanStack Query patterns for polling are standard.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | Core Go libraries verified via pkg.go.dev with live version checks. OpenTofu BSL-vs-MPL sourced from vendor blogs (MEDIUM) but widely corroborated. Vite 8 Rolldown from official announcement. |
| Features | HIGH | Cross-verified across E2B, Modal, Fly Machines, Kubernetes agent-sandbox (official SIG), NVIDIA OpenShell, Northflank. Competitor feature matrix is high-confidence. |
| Architecture | HIGH | Grounded in defcon.run.34 reference implementation (directly inspected) + Squid iptables docs + official Terraform/Terragrunt patterns. Sidecar architecture from authoritative Istio bypass research. |
| Pitfalls | HIGH (critical), MEDIUM (integration) | Critical pitfalls sourced from authoritative security research and official AWS docs. Integration gotchas from direct defcon.run.34 module inspection and Terragrunt GitHub issues. |

**Overall confidence:** HIGH

### Gaps to Address

- **Filesystem policy enforcement mechanism:** The SandboxProfile spec should declare writable/read-only paths, but the enforcement mechanism (seccomp BPF, Linux mount namespaces, or OverlayFS) was not resolved in this research pass. The choice has security and complexity implications that need a decision before Phase 4.
- **TTL server-side enforcement for v1:** Research recommends EventBridge Scheduler for robust TTL enforcement (CLI process is not reliable). Whether v1 ships with EventBridge or with the simpler "sidecar self-terminates" pattern needs a decision during Phase 3 planning.
- **HTTPS proxy mode (SNI vs. MITM):** Full HTTPS inspection requires a per-sandbox CA cert installed in the instance trust store. SNI-only allowlisting is simpler but doesn't inspect request content. This is a security trade-off decision that should be made explicit in Phase 3 planning.
- **`fabric list` state index:** At small scale, scanning Terraform state files per-sandbox is acceptable. The threshold for needing a DynamoDB index is estimated at ~50 concurrent sandboxes. v1 can skip this, but it should be a known limitation.

## Sources

### Primary (HIGH confidence)
- pkg.go.dev/github.com/spf13/cobra — v1.10.2, verified live
- pkg.go.dev/github.com/rs/zerolog — v1.34.0, verified live
- pkg.go.dev/github.com/miekg/dns — v1.1.72 (Jan 2026), verified
- pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6 — v6.0.2, verified
- github.com/aws/aws-sdk-go-v2 releases — v1.41.4 (Mar 2026), verified live
- vite.dev/blog/announcing-vite8 — Vite 8 Rolldown bundler announcement
- ui.shadcn.com/docs/tailwind-v4 — shadcn/ui Tailwind v4 + React 19 compatibility
- kubernetes-sigs/agent-sandbox — official Kubernetes SIG project
- blog.howardjohn.info/posts/bypass-egress — sidecar proxy bypass techniques (7 methods)
- aws.amazon.com/blogs/security (IMDSv2 enforcement) — official AWS guidance
- defcon.run.34 reference implementation — directly inspected (ec2spot module, infra modules, apps/)

### Secondary (MEDIUM confidence)
- northflank.com/blog (multiple posts) — competitor feature analysis, sandbox architecture patterns
- spacelift.io/blog/opentofu-vs-terraform — OpenTofu vs. Terraform BSL analysis
- docs.terragrunt.com/reference/supported-versions — OpenTofu 1.9.1 + Terragrunt 0.77.x compatibility
- github.com/gruntwork-io/terragrunt/issues/2646 — concurrent init lock file bug
- rhinosecuritylabs.com — IAM privilege escalation methods (iam:PassRole)
- ubos.tech/news/nvidia-unveils-openshell — NVIDIA OpenShell GTC 2026 coverage

### Tertiary (LOW confidence)
- controlmonkey.io/resource/terraform-license-change-impact-2025 — Terraform OSS BSL EOL July 2025 (blog source; corroborated by spacelift.io)

---
*Research completed: 2026-03-21*
*Ready for roadmap: yes*
