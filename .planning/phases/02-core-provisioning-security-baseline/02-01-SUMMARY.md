---
phase: 02-core-provisioning-security-baseline
plan: 01
subsystem: compiler
tags: [go, terraform, terragrunt, ec2, ecs, fargate, security-groups, iam, ssm, uuid]

requires:
  - phase: 01-schema-compiler-aws-foundation
    provides: "SandboxProfile types (pkg/profile), ec2spot Terraform module, Cobra CLI pattern"

provides:
  - "pkg/compiler.Compile() translating SandboxProfile to CompiledArtifacts (EC2 + ECS)"
  - "pkg/compiler.GenerateSandboxID() producing sb-XXXXXXXX identifiers"
  - "EC2 service.hcl generation with sg_egress_rules + iam_session_policy in module_inputs"
  - "ECS service.hcl generation with 5-container task definition and FARGATE_SPOT support"
  - "EC2 user-data.sh with SSM agent, IMDSv2 check, secret injection, GitHub token"
  - "ec2spot Terraform module extended with sg_egress_rules variable and aws_security_group_rule resources"
  - "ec2spot Terraform module extended with iam_session_policy variable and max_session_duration on IAM role"
  - "profile.IdentitySpec.AllowedSecretPaths field for SSM secret allowlisting"

affects:
  - 02-core-provisioning-security-baseline
  - 03-sidecar-enforcement-layer
  - create-command
  - terragrunt-runner

tech-stack:
  added:
    - "github.com/google/uuid v1.6.0 (sandbox ID generation)"
  patterns:
    - "text/template for HCL generation — never fmt.Sprintf for HCL values"
    - "Pure function compiler: no AWS side effects, testable without credentials"
    - "TDD: failing tests first, then implementation"
    - "sg_egress_rules and iam_session_policy flow through service.hcl module_inputs to Terraform"

key-files:
  created:
    - "pkg/compiler/compiler.go - Compile() entry point and CompiledArtifacts/SGRule/IAMSessionPolicy types"
    - "pkg/compiler/compiler_test.go - 17 unit tests covering all compilation paths"
    - "pkg/compiler/sandbox_id.go - GenerateSandboxID() using uuid"
    - "pkg/compiler/security.go - compileSGRules(), compileIAMPolicy(), compileSecrets()"
    - "pkg/compiler/service_hcl.go - EC2 and ECS service.hcl generation via text/template"
    - "pkg/compiler/userdata.go - EC2 user-data.sh generation via text/template"
    - "pkg/compiler/testdata/ec2-basic.yaml - minimal EC2 test profile"
    - "pkg/compiler/testdata/ec2-with-secrets.yaml - EC2 profile with secrets + GitHub"
    - "pkg/compiler/testdata/ecs-basic.yaml - minimal ECS test profile"
  modified:
    - "infra/modules/ec2spot/v1.0.0/variables.tf - added sg_egress_rules and iam_session_policy variables"
    - "infra/modules/ec2spot/v1.0.0/main.tf - added aws_security_group_rule + aws_iam_role_policy resources"
    - "pkg/profile/types.go - added AllowedSecretPaths to IdentitySpec"

key-decisions:
  - "Baseline SG egress: TCP 443 + UDP 53 to 0.0.0.0/0 in Phase 2; Phase 3 tightens when proxy sidecars handle per-host filtering"
  - "sg_egress_rules and iam_session_policy serialized into service.hcl module_inputs (not separate files) so Terragrunt passes them as Terraform variables automatically"
  - "ECS main container gets 640 CPU / 1280 MiB; 4 sidecars share remaining 384 CPU / 768 MiB of 1024/2048 task total"
  - "onDemand=true flag overrides profile spot=true for both EC2 (spot_price_multiplier) and ECS (FARGATE vs FARGATE_SPOT)"
  - "AllowedSecretPaths added to IdentitySpec as Rule 2 auto-fix — field was required for compiler but missing from schema"

patterns-established:
  - "Compiler pattern: Compile(profile, sandboxID, onDemand) -> CompiledArtifacts (pure function, no AWS calls)"
  - "HCL generation: text/template with custom FuncMap (sgRuleHCL, joinStrings) — never string interpolation"
  - "Security flow: compileSGRules/compileIAMPolicy/compileSecrets all called regardless of substrate, serialized into module_inputs"
  - "Spot override: useSpot = profile.spot && !onDemand — single boolean controls spot vs on-demand in both EC2 and ECS"

requirements-completed: [PROV-01, PROV-08, PROV-09, PROV-10, PROV-11, PROV-12, NETW-01, NETW-04, NETW-06, NETW-08]

duration: 4min
completed: 2026-03-22
---

# Phase 02 Plan 01: Profile Compiler Summary

**Pure-function profile compiler (EC2 + ECS) using text/template HCL generation; sg_egress_rules and iam_session_policy flow through service.hcl module_inputs to ec2spot Terraform module**

## Performance

- **Duration:** 4 min
- **Started:** 2026-03-22T00:20:46Z
- **Completed:** 2026-03-22T00:24:51Z
- **Tasks:** 2 (both tasks implemented in single TDD cycle)
- **Files modified:** 12

## Accomplishments

- `pkg/compiler` package with `Compile()` handling both EC2 and ECS substrates as pure functions (no AWS calls, fully testable)
- Critical security link established: `sg_egress_rules` and `iam_session_policy` serialized into `service.hcl module_inputs` so NETW-01 (SG egress enforcement) and NETW-04 (IAM session policy) actually reach AWS Terraform resources
- EC2 user-data.sh with SSM agent install, IMDSv2 token verification, SSM secret injection loop, and GitHub token injection — generated from `text/template` with strict quoting
- ECS service.hcl with 5-container task definition (main + dns-proxy + http-proxy + audit-log + tracing) and FARGATE_SPOT/FARGATE capacity provider selection
- ec2spot Terraform module extended with `aws_security_group_rule` resources driven by `sg_egress_rules` variable and `max_session_duration`/region-lock on IAM role
- 17 tests pass (TDD RED → GREEN), full test suite green with no regressions

## Task Commits

Both tasks executed in a single TDD cycle:

1. **Task 1 + Task 2: EC2 + ECS compiler with ec2spot module extensions** - `1201a64` (feat)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/compiler/compiler.go` - Compile() entry, CompiledArtifacts, SGRule, IAMSessionPolicy types
- `/Users/khundeck/working/klankrmkr/pkg/compiler/sandbox_id.go` - GenerateSandboxID() via uuid
- `/Users/khundeck/working/klankrmkr/pkg/compiler/security.go` - compileSGRules, compileIAMPolicy, compileSecrets
- `/Users/khundeck/working/klankrmkr/pkg/compiler/service_hcl.go` - EC2/ECS service.hcl via text/template
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go` - EC2 user-data.sh via text/template
- `/Users/khundeck/working/klankrmkr/pkg/compiler/compiler_test.go` - 17 unit tests (TDD)
- `/Users/khundeck/working/klankrmkr/pkg/compiler/testdata/ec2-basic.yaml` - minimal EC2 test profile
- `/Users/khundeck/working/klankrmkr/pkg/compiler/testdata/ec2-with-secrets.yaml` - EC2 profile with secrets + GitHub
- `/Users/khundeck/working/klankrmkr/pkg/compiler/testdata/ecs-basic.yaml` - minimal ECS test profile
- `/Users/khundeck/working/klankrmkr/infra/modules/ec2spot/v1.0.0/variables.tf` - sg_egress_rules + iam_session_policy variables
- `/Users/khundeck/working/klankrmkr/infra/modules/ec2spot/v1.0.0/main.tf` - aws_security_group_rule + aws_iam_role_policy resources
- `/Users/khundeck/working/klankrmkr/pkg/profile/types.go` - AllowedSecretPaths in IdentitySpec

## Decisions Made

- Baseline SG egress rules are TCP 443 (HTTPS) and UDP 53 (DNS) to 0.0.0.0/0 — Phase 3 tightens when proxy sidecars enforce per-host filtering
- SG rules and IAM policy serialized directly into `service.hcl module_inputs` rather than separate files — Terragrunt already passes `module_inputs` as Terraform variables, so no extra wiring needed
- ECS main container receives 640/1280 (CPU/MiB); four sidecars share the remaining 384/768 of the 1024/2048 task total
- `onDemand=true` computes `useSpot = profile.spot && !onDemand` for both substrates — single boolean controls spot vs on-demand

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Added AllowedSecretPaths field to IdentitySpec**
- **Found during:** Task 1 (implementing compileSecrets())
- **Issue:** The plan specifies `compileSecrets(p)` reads `identity.allowedSecretPaths`, but the field didn't exist in `pkg/profile/types.go` IdentitySpec struct
- **Fix:** Added `AllowedSecretPaths []string yaml:"allowedSecretPaths,omitempty"` to IdentitySpec
- **Files modified:** pkg/profile/types.go
- **Verification:** compileSecrets() correctly reads the field; TestCompileSecretsInjection passes
- **Committed in:** 1201a64 (Task commit)

---

**Total deviations:** 1 auto-fixed (Rule 2 — missing critical field required for compiler correctness)
**Impact on plan:** Essential fix — without AllowedSecretPaths, the secrets injection feature could not be implemented. No scope creep.

## Issues Encountered

None — TDD cycle went cleanly RED → GREEN with all 17 tests passing on first GREEN run.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- `pkg/compiler` is the foundation for Plan 02-02 (terragrunt runner) and Plan 02-03 (create/destroy commands)
- The `Compile()` function produces `ServiceHCL` and `UserData` strings ready to be written to the sandbox directory by the terragrunt runner
- ec2spot module now accepts `sg_egress_rules` and `iam_session_policy` — when first sandbox is provisioned, security baseline is enforced at the SG level without any further changes
- Concern: the generated service.hcl uses empty strings for `vpc_id`, `public_subnets`, `availability_zones` — these must be filled in by the terragrunt runner from network module outputs at provision time

---
*Phase: 02-core-provisioning-security-baseline*
*Completed: 2026-03-22*
