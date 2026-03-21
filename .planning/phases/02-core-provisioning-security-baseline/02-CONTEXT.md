# Phase 2: Core Provisioning & Security Baseline - Context

**Gathered:** 2026-03-21
**Status:** Ready for planning

<domain>
## Phase Boundary

Implement `km create` and `km destroy` commands that compile a SandboxProfile into Terragrunt inputs and provision/teardown sandboxes on EC2 or ECS substrates. Establish the SG-first security model, IMDSv2 enforcement, resource tagging, secrets injection via SSM, GitHub access via App tokens, and spot instance support. No sidecars, TTL auto-destroy, or lifecycle management in this phase — that's Phase 3.

</domain>

<decisions>
## Implementation Decisions

### Compiler Design
- Output format: JSON tfvars (`terraform.tfvars.json`) — unambiguous escaping, easy to generate from Go structs via json.Marshal
- Single tfvars file per sandbox — Terragrunt handles module orchestration via dependencies
- Substrate branching via flag in tfvars: `substrate="ec2"` or `"ecs"` — Terragrunt conditionally includes the right modules. Compiler stays simple.
- Basic user-data bootstrap script generated in Phase 2: SSM agent, IMDSv2 config, basic networking. Phase 3 adds sidecar injection to this script.

### Create/Destroy UX
- Sandbox IDs: `sb-` prefix + 8 hex chars (e.g. `sb-a1b2c3d4`) — short enough to type, used in AWS tags, state paths, CLI output
- Progress: stream Terraform output in real time — operator sees every resource being created, no mystery
- No local state — rely entirely on Terraform state in S3 + AWS resource tags (`km:sandbox-id`) for discovery
- `km destroy sb-a1b2c3d4` looks up `km:sandbox-id` tag in AWS to find the Terragrunt state path in S3, then runs destroy. Works from any terminal.

### Spot Instance Strategy
- Spot unavailable: fail with message, don't auto-fallback to on-demand. No surprise costs.
- ECS: `spot: true` → FARGATE_SPOT capacity provider only. `spot: false` → FARGATE only. Clean mapping, same semantics as EC2.
- CLI flag: `km create --on-demand profile.yaml` overrides `spot: true` in the profile for this one sandbox without editing the profile.

### Secrets + GitHub Access
- Secrets injected as environment variables at boot — user-data script fetches from SSM, exports as env vars. Simple, standard.
- GitHub access via GitHub App installation token stored in SSM SecureString, injected as `GITHUB_TOKEN` env var. Works with HTTPS git operations, rotates automatically.
- SOPS decryption at provision time (not compile time) — compiler writes SSM parameter ARNs into tfvars, user-data script decrypts at boot using instance IAM role. Secrets never in Terraform state.

### Claude's Discretion
- Terragrunt dependency graph structure (how modules reference each other)
- Exact user-data script content for Phase 2 baseline
- AWS SDK usage patterns for tag-based sandbox lookup in `km destroy`
- EC2 spot request configuration details (request type, interruption behavior)
- Security group rule specifics (which ports, which CIDRs)

</decisions>

<specifics>
## Specific Ideas

- AWS accounts already provisioned: application account `052251888500`, KMS key `alias/km-sops`, artifact bucket `km-sandbox-artifacts-ea554771`, domain `klankermaker.ai`
- Terraform state auto-provisioned by Terragrunt on first run (S3 + DynamoDB)
- AWS CLI profiles: `klanker-application`, `klanker-management`, `klanker-terraform`
- SG-first security model: VPC Security Groups block all egress by default; proxy sidecars (Phase 3) are the policy layer on top

</specifics>

<code_context>
## Existing Code Insights

### Reusable Assets
- `pkg/profile/types.go`: SandboxProfile struct with all 10 spec sections — compiler reads these types
- `pkg/profile/validate.go`: Validate() + ValidateSemantic() — `km create` should validate before compiling
- `pkg/profile/inherit.go`: Resolve() for extends chains — `km create` resolves inheritance before compilation
- `internal/app/cmd/validate.go`: Cobra command pattern to follow for create/destroy commands
- `internal/app/config/config.go`: Config struct with Viper — reuse for create/destroy commands
- `infra/modules/`: 6 Terraform modules ready to use (network, ec2spot, ecs-cluster, ecs-task, ecs-service, secrets)
- `infra/live/site.hcl`: Global config with domain, KMS alias, state backend config
- `infra/live/sandboxes/_template/`: Per-sandbox Terragrunt directory template

### Established Patterns
- Cobra command constructor: `NewXxxCmd(cfg *config.Config)` pattern from validate.go
- Terragrunt per-sandbox directory isolation under `infra/live/sandboxes/<sandbox-id>/`
- `km:sandbox-id` tag on all AWS resources (from module copy in Phase 1)

### Integration Points
- `cmd/km/main.go` → `internal/app/cmd/root.go` → new create.go, destroy.go commands
- `pkg/compiler/` (new) — called by create command, reads profile types, outputs JSON tfvars
- `pkg/terragrunt/` (new) — wraps Terragrunt binary execution, streams output
- `pkg/aws/` (new) — AWS SDK helpers for tag-based sandbox lookup in destroy

</code_context>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 02-core-provisioning-security-baseline*
*Context gathered: 2026-03-21*
