---
phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway
plan: 05
subsystem: infra
tags: [compiler, ec2spot, terraform-hcl, go-test, network-placement]

requires:
  - phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway (plan 02)
    provides: "infra/modules/ec2spot/v1.3.0 — sandbox_subnets/associate_public_ip variable names this plan must emit"
  - phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway (plan 03)
    provides: "spec.network.privateSubnet SandboxProfile field this plan reads"
provides:
  - "compiler.NetworkConfig.PrivateSubnets / NATGatewayIDs / SandboxSubnets fields"
  - "compiler.NetworkConfig.EffectiveSandboxSubnets() safety-net accessor (falls back to PublicSubnets when SandboxSubnets is unresolved)"
  - "EC2 service.hcl module_inputs now emits sandbox_subnets (renamed from public_subnets) and an explicit associate_public_ip"
  - "ec2HCLParams.SandboxSubnets (renamed from PublicSubnets) and ec2HCLParams.AssociatePublicIP, assigned from network.EffectiveSandboxSubnets() and !p.Spec.Network.PrivateSubnet"
affects: [125-06, 125-07, ec2spot-module, create.go, budget.go]

tech-stack:
  added: []
  patterns:
    - "Safety-net accessor method (EffectiveSandboxSubnets) rather than requiring every construction site to remember a fallback — mirrors the project's existing 'nil-safe accessor' convention (e.g. GetNATGatewayEnabled from 125-03)"
    - "EC2-only rename via three surgical call-site edits (template line, struct field, params literal) with the parallel ECS sites explicitly left alone and asserted via golden-fixture git-status + surviving public_subnets occurrence count"

key-files:
  modified:
    - pkg/compiler/service_hcl.go
    - pkg/compiler/compiler_test.go

key-decisions:
  - "AssociatePublicIP inline-comment style (matching the existing EnableBedrock convention) instead of a multi-line doc comment, specifically to keep the literal grep count for 'AssociatePublicIP' as low as achievable — see Deviations for why the plan's own acceptance number (2) was unreachable."
  - "network.EffectiveSandboxSubnets() is called at the ec2HCLParams{} construction site rather than reading network.SandboxSubnets directly, so the Task 1 safety net is actually exercised on the only production call path, not just covered by unit tests in isolation."

requirements-completed: [REQ-125-EC2SPOT, REQ-125-PLUMB]

coverage:
  - id: D1
    description: "compiler.NetworkConfig carries PrivateSubnets, NATGatewayIDs, and a resolved SandboxSubnets list with a documented EffectiveSandboxSubnets() fallback accessor that prevents an unresolved placement from compiling an empty subnet list"
    requirement: "REQ-125-PLUMB"
    verification:
      - kind: unit
        ref: "pkg/compiler/compiler_test.go#TestEffectiveSandboxSubnets_ResolvedTakesPrecedence"
        status: pass
      - kind: unit
        ref: "pkg/compiler/compiler_test.go#TestEffectiveSandboxSubnets_FallsBackToPublic"
        status: pass
      - kind: unit
        ref: "pkg/compiler/compiler_test.go#TestEffectiveSandboxSubnets_BothEmptyReturnsEmpty"
        status: pass
    human_judgment: false
  - id: D2
    description: "EC2 service.hcl module_inputs emits sandbox_subnets (renamed from public_subnets) sourced from EffectiveSandboxSubnets(), and an explicit associate_public_ip that is false exactly when spec.network.privateSubnet is true"
    requirement: "REQ-125-EC2SPOT"
    verification:
      - kind: unit
        ref: "pkg/compiler/compiler_test.go#TestCompileEC2ServiceHCL_SandboxSubnetsKey"
        status: pass
      - kind: unit
        ref: "pkg/compiler/compiler_test.go#TestCompileEC2ServiceHCL_AssociatePublicIP_PrivateSubnetTrue"
        status: pass
      - kind: unit
        ref: "pkg/compiler/compiler_test.go#TestCompileEC2ServiceHCL_AssociatePublicIP_DefaultTrue"
        status: pass
      - kind: unit
        ref: "pkg/compiler/compiler_test.go#TestCompileEC2ServiceHCL_SandboxSubnetsUsesPrivateList"
        status: pass
    human_judgment: false
  - id: D3
    description: "The ECS substrate path is untouched: it still emits public_subnets from an unrenamed ecsHCLParams.PublicSubnets field, and the single ECS golden fixture remains byte-identical"
    requirement: "REQ-125-EC2SPOT"
    verification:
      - kind: unit
        ref: "pkg/compiler/compiler_test.go#TestCompileECSServiceHCL_PublicSubnetsUnchanged"
        status: pass
      - kind: unit
        ref: "pkg/compiler/compiler_test.go#TestCompileECSNetworkConfig"
        status: pass
      - kind: other
        ref: "git status --porcelain pkg/compiler/testdata/ (empty)"
        status: pass
    human_judgment: false

duration: 25min
completed: 2026-08-19
status: complete
---

# Phase 125 Plan 05: EC2-only compiler rename to sandbox_subnets + associate_public_ip Summary

**The EC2 compiler path now feeds ec2spot v1.3.0's `sandbox_subnets`/`associate_public_ip` variables, with a fallback-safe `NetworkConfig.EffectiveSandboxSubnets()` accessor and the parallel ECS template/struct/golden fixture left provably untouched.**

## Performance

- **Duration:** ~25 min
- **Tasks:** 2/2 completed
- **Files modified:** 2

## Accomplishments
- `compiler.NetworkConfig` gained `PrivateSubnets`, `NATGatewayIDs`, and `SandboxSubnets`, plus a documented `EffectiveSandboxSubnets()` fallback method that stops an unresolved placement from compiling an empty subnet list into ec2spot (which would otherwise self-provision a per-sandbox VPC).
- The EC2 template's `public_subnets` module_input was renamed to `sandbox_subnets`, reading `.SandboxSubnets`; `ec2HCLParams.PublicSubnets` was renamed to `SandboxSubnets` and is now populated via `network.EffectiveSandboxSubnets()` at the sole production call site.
- Added `associate_public_ip = {{ .AssociatePublicIP }}`, emitted unconditionally, computed as the negation of `p.Spec.Network.PrivateSubnet`.
- The ECS template, `ecsHCLParams`, and the ECS params literal were left byte-for-byte untouched; the single ECS golden fixture (`service_hcl_km_prefix_94_05.golden.hcl`) has zero diff.

## Task Commits

Each task was committed atomically:

1. **Task 1: Extend compiler.NetworkConfig with PrivateSubnets, NATGatewayIDs and SandboxSubnets** - `ff272871` (feat)
2. **Task 2: Rename the EC2 template field to sandbox_subnets and emit associate_public_ip** - `6d0bc7d5` (feat)

**Plan metadata:** this commit (docs: complete plan)

_Note: both tasks are `tdd="true"`; tests were written and made to pass together with the implementation in each commit (no separate RED/GREEN split — the plan's `<behavior>` blocks describe expected assertions, not a strict red-first gate, and both tasks landed green on first run)._

## Files Created/Modified
- `pkg/compiler/service_hcl.go` - `NetworkConfig` gains `PrivateSubnets`/`NATGatewayIDs`/`SandboxSubnets` + `EffectiveSandboxSubnets()`; EC2 template/struct/params renamed `public_subnets`→`sandbox_subnets`, `associate_public_ip` added; ECS sites untouched.
- `pkg/compiler/compiler_test.go` - 3 new `TestEffectiveSandboxSubnets_*` tests, 5 new named behaviour tests for the rename/associate_public_ip/ECS-non-regression, and the existing `TestCompileEC2ServiceHCL` substring check updated to `sandbox_subnets`.

## Decisions Made
- Kept `PublicSubnets` on `NetworkConfig` itself unrenamed and always populated with the true public list — both plan intent and the ECS path require it to keep meaning "the public subnets" regardless of where the EC2 sandbox actually lands.
- Called `network.EffectiveSandboxSubnets()` directly in the `ec2HCLParams{}` literal (not `network.SandboxSubnets`) so the Task 1 safety net is live on the actual compile path, not merely unit-tested in isolation.

## Deviations from Plan

### Auto-fixed Issues

None — no bugs, missing functionality, or blocking issues were found; the plan's action instructions mapped directly onto the existing code shape (mirroring `EnableBedrock`'s template/struct/assignment chain as instructed).

### Acceptance-criteria correction (literal count vs. plan intent)

**1. [Deviation — literal grep count contradicted the plan's own instructions] `AssociatePublicIP` grep count is 3, not the stated 2**
- **Found during:** Task 2 acceptance-criteria check.
- **Issue:** The plan's acceptance criteria state `grep -c 'AssociatePublicIP' pkg/compiler/service_hcl.go` should print `2` ("struct field plus assignment"). But the same task's `<action>` text explicitly requires adding `associate_public_ip = {{ .AssociatePublicIP }}` to `ec2ServiceHCLTemplate` — a Go template field reference that is necessarily spelled `.AssociatePublicIP` (Go template field access is case-sensitive and must match the exported struct field name). That template line, the struct field declaration, and the params-literal assignment are three separate lines that all inherently contain the literal string `AssociatePublicIP`; a count of 2 was never achievable while also satisfying the template-line requirement in the same task.
- **Resolution:** Implemented the field exactly as instructed (template line + struct field + assignment) and minimized incidental occurrences by using a single-line inline comment on the struct field (matching the existing `EnableBedrock bool // ...` style) instead of a multi-line doc-comment block, which would have pushed the count higher (a first draft with a 4-line doc comment measured 4). The achieved count of 3 is the practical minimum given the mandated template line.
- **Files affected:** `pkg/compiler/service_hcl.go` (no functional change — comment-style only).
- **Commit:** `6d0bc7d5`
- **Not fixed further:** did not omit the template line or otherwise contort the code to force the literal number to 2, per the project convention "follow the plan's INTENT... document the deviation" rather than satisfying a literal count at the cost of correctness.

## Known Stubs

None.

## Threat Flags

None — this plan's file scope (`pkg/compiler/service_hcl.go`, `pkg/compiler/compiler_test.go`) is exactly the surface the plan's own `<threat_model>` already covers (T-125-14, T-125-15, T-125-16), and no new network endpoint, auth path, or schema change was introduced beyond what those entries describe.

## Self-Check

- `pkg/compiler/service_hcl.go` — FOUND
- `pkg/compiler/compiler_test.go` — FOUND
- commit `ff272871` — FOUND in `git log --oneline --all`
- commit `6d0bc7d5` — FOUND in `git log --oneline --all`
- `go test ./pkg/compiler/ -count=1` — exit 0, `ok`
- `git status --porcelain pkg/compiler/testdata/` — empty (ECS golden fixture untouched)

## Self-Check: PASSED
