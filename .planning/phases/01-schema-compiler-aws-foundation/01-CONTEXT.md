# Phase 1: Schema, Compiler & AWS Foundation - Context

**Gathered:** 2026-03-21
**Status:** Ready for planning

<domain>
## Phase Boundary

Define the SandboxProfile YAML schema with validation, build the profile compiler that translates profiles into Terragrunt inputs, implement `km validate`, create four built-in profiles (open-dev, restricted-dev, hardened, sealed), and copy/adapt all foundation Terraform modules and Terragrunt patterns from defcon.run.34 into this repo. No runtime provisioning in this phase — that's Phase 2.

</domain>

<decisions>
## Implementation Decisions

### Schema Validation
- Strict validation: reject unknown fields (catches typos like 'lifecylce' immediately)
- All top-level spec sections are required (lifecycle, runtime, execution, sourceAccess, network, identity, sidecars, observability, policy, agent) — no hidden defaults
- Error messages use JSON path + message format: `spec.network.egress.allowedDNSSuffixes[0]: must be a valid DNS suffix pattern` — precise, grep-friendly, CI-ready
- Semantic checks included in v1 (not just schema structure): catch logical errors like spot: true on unsupported instance types, TTL shorter than idle timeout, etc.

### Profile Inheritance
- Max inheritance depth: 3 levels (built-in → team base → workload-specific)
- `extends` references profiles by name only (not file path) — resolver looks up built-in profiles and a configurable search path
- Omitted sections in child profiles inherit parent's values — child only needs to specify overrides
- Child always wins on conflicts, no warning — simple override semantics, no noise

### Built-in Profiles
- All four profiles route traffic through proxy sidecars (even open-dev) — enforcement is always on, profiles differ only in allowlist strictness
- All four sidecars enabled on all profiles (DNS proxy, HTTP proxy, audit log, tracing) — no graduated sidecar enablement
- Default TTLs: open-dev 24h, restricted-dev 8h, hardened 4h, sealed 1h
- Filesystem policy is independent of profile tier — sealed does NOT imply read-only filesystem
- Network policy graduation: open-dev has permissive allowlist, restricted-dev has curated allowlist, hardened has minimal egress (AWS APIs + specific hosts), sealed has zero egress

### Module Copy Strategy
- Terraform modules go in `infra/modules/` (network, ec2spot, ecs-cluster, ecs-task, ecs-service, secrets)
- Rename variables/outputs from defcon naming to km naming; strip features not needed for sandboxes (e.g. ALB from network module); keep core logic intact
- ConfigUI Go code deferred to Phase 5 — not copied in Phase 1
- Full Terragrunt hierarchy copied: site.hcl, service.hcl, and the full live/ directory structure from defcon.run.34

### Claude's Discretion
- Exact JSON Schema Draft 2020-12 structure and composition
- Go struct design for SandboxProfile types
- Compiler output format (JSON tfvars vs HCL)
- Specific semantic validation rules beyond the examples discussed
- Profile search path resolution implementation details

</decisions>

<specifics>
## Specific Ideas

- apiVersion is `klankermaker.ai/v1alpha1` with Kubernetes-style apiVersion/kind/metadata/spec structure
- Go CLI follows tiogo architecture: `cmd/` entry point → `internal/app/cmd/` Cobra commands → `pkg/` reusable libraries
- Use `goccy/go-yaml` for YAML parsing and `santhosh-tekuri/jsonschema/v6` for schema validation (from stack research)
- Profile names for extends are resolved by name, not path — keeps profiles portable across environments

</specifics>

<code_context>
## Existing Code Insights

### Reusable Assets
- defcon.run.34 `infra/terraform/modules/`: network, ec2spot, ecs-cluster, ecs-task, ecs-service, secrets, email, s3-uploads, github-oidc — copy and adapt relevant modules
- defcon.run.34 Terragrunt live/ hierarchy: site.hcl patterns, per-environment directory structure
- tiogo Go CLI architecture patterns (Cobra/Viper, Config DI, internal/pkg layout)

### Established Patterns
- Terragrunt site.hcl for global config (region, backend, provider) — proven pattern from defcon.run.34
- Terraform modules are versioned under `v1.0.0/` subdirectories in defcon.run.34

### Integration Points
- Greenfield repo — no existing code to integrate with
- defcon.run.34 at `~/working/defcon.run.34` is the source for module copies

</code_context>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 01-schema-compiler-aws-foundation*
*Context gathered: 2026-03-21*
