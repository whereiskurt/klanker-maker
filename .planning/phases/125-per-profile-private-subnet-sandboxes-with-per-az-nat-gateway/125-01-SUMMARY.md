---
phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway
plan: 01
subsystem: infra
tags: [terraform, terragrunt, nat-gateway, vpc, networking, go-test]

# Dependency graph
requires:
  - phase: 124-platform-wide-az-failover-capacity-feasibility
    provides: "AZ-failover sweep (RankAZs, classify-and-retry) that Phase 125's per-AZ NAT removes as a cross-AZ egress cost / single-AZ SPOF"
provides:
  - "infra/modules/network/v1.1.0 — new immutable Terraform module version with one NAT gateway + one EIP per AZ, each NAT in the public subnet of the AZ it serves, and one route table per private subnet"
  - "infra/live/use1/network/terragrunt.hcl sourcing v1.1.0 and deriving nat_gateway.enabled from KM_NAT_GATEWAY_ENABLED via tobool(get_env(...)), defaulting to \"false\" (dormant)"
  - "pkg/terragrunt/network_module_v110_test.go — Go structural regression test locking the per-AZ counts, moved blocks, plural outputs, and additive-bump invariants"
affects: ["125-02 (ec2spot v1.3.0 rename)", "125-03..125-09 (Go plumbing, profile schema, km doctor/init guards, live UAT)"]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Module version bump = new immutable vX.Y.Z directory (verbatim copy + delta), never an in-place edit of a released version"
    - "moved blocks for every singleton-to-indexed resource transition so the first plan against an existing install shows moves, not destroy+create"
    - "Structural Terraform regression tests as plain-text/regexp assertions over .tf files — no terraform shell-out, so they run without a terraform binary and never risk the module-source lock-drift footgun"

key-files:
  created:
    - infra/modules/network/v1.1.0/main.tf
    - infra/modules/network/v1.1.0/variables.tf
    - infra/modules/network/v1.1.0/outputs.tf
    - pkg/terragrunt/network_module_v110_test.go
  modified:
    - infra/live/use1/network/terragrunt.hcl

key-decisions:
  - "Added all 3 moved blocks exactly as the plan specified (aws_route_table.private, aws_eip.nat, aws_nat_gateway.nat), even though only aws_route_table.private is a genuine singleton-to-indexed transition — aws_eip.nat and aws_nat_gateway.nat were already count-based (0 or 1) in v1.0.0, so their moved blocks are semantically inert (nothing to relocate) but harmless, matching the plan's explicit exactly-3 acceptance check."
  - "Extended the outputs test beyond the plan's literal wording to also assert the OLD singular outputs (private_route_table_id, nat_gateway_id, nat_eip_public_ip) are absent from v1.1.0's outputs.tf, locking in Task 1's 'Replace' instruction and guarding against accidental re-introduction."

requirements-completed: [REQ-125-NETMOD, REQ-125-TOGGLE]

coverage:
  - id: D1
    description: "infra/modules/network/v1.1.0 provisions one NAT gateway + one EIP per AZ (count = length(var.vpc.private_subnets_cidr)), each NAT in the public subnet of the AZ it serves, with one route table per private subnet so 4 distinct 0.0.0.0/0 NAT targets can coexist; v1.0.0 left byte-identical; 3 moved blocks cover the singleton-to-indexed transitions; plural outputs (private_route_table_ids, nat_gateway_ids, nat_eip_public_ips) replace the old singular ones while vpc_id/public_subnets are preserved"
    requirement: "REQ-125-NETMOD"
    verification:
      - kind: unit
        ref: "pkg/terragrunt/network_module_v110_test.go#TestNetworkModuleV110PerAZNAT"
        status: pass
      - kind: unit
        ref: "pkg/terragrunt/network_module_v110_test.go#TestNetworkModuleV110MovedBlocks"
        status: pass
      - kind: unit
        ref: "pkg/terragrunt/network_module_v110_test.go#TestNetworkModuleV110Outputs"
        status: pass
      - kind: other
        ref: "terraform fmt -check -recursive infra/modules/network/v1.1.0 (exit 0)"
        status: pass
    human_judgment: false
  - id: D2
    description: "Live network unit (infra/live/use1/network/terragrunt.hcl) sources v1.1.0 and derives nat_gateway.enabled from tobool(get_env(\"KM_NAT_GATEWAY_ENABLED\", \"false\")) instead of a hardcoded literal, keeping the toggle dormant (byte-identical to Phase 124) when the env var is unset; the bump is additive — v1.0.0 still exists on disk"
    requirement: "REQ-125-TOGGLE"
    verification:
      - kind: unit
        ref: "pkg/terragrunt/network_module_v110_test.go#TestNetworkModuleV110IsAdditive"
        status: pass
      - kind: other
        ref: "grep -c 'tobool(get_env(\"KM_NAT_GATEWAY_ENABLED\", \"false\"))' infra/live/use1/network/terragrunt.hcl (== 1)"
        status: pass
    human_judgment: false

duration: ~20min
completed: 2026-08-20
status: complete
---

# Phase 125 Plan 01: Network module v1.1.0 with per-AZ NAT gateways Summary

**New immutable `infra/modules/network/v1.1.0` provisions one NAT gateway + EIP per AZ (not one shared), each NAT in its own AZ's public subnet with a dedicated per-AZ private route table; the live unit now sources it and reads the toggle from `KM_NAT_GATEWAY_ENABLED` instead of a hardcoded `false`.**

## Performance

- **Duration:** ~20 min (not precisely timed — start timestamp was not captured at session start; based on tool-call sequence)
- **Completed:** 2026-08-20T00:18:48Z
- **Tasks:** 3/3
- **Files modified:** 5 (4 created, 1 modified)

## Accomplishments
- Created `infra/modules/network/v1.1.0` as a verbatim copy of v1.0.0 with NAT, EIP, and private route tables all counted off `length(var.vpc.private_subnets_cidr)` instead of a fixed 1, each NAT placed in the public subnet of the AZ it serves (`count.index`, not fixed index 0)
- Added 3 `moved` blocks (`aws_route_table.private`, `aws_eip.nat`, `aws_nat_gateway.nat` → their `[0]` indexed forms) so the first `terraform plan` against an existing install shows moves, not destroy+create
- Replaced the singular `private_route_table_id` / `nat_gateway_id` / `nat_eip_public_ip` outputs with plural list outputs (`private_route_table_ids`, `nat_gateway_ids`, `nat_eip_public_ips`); left `vpc_id`, `public_subnets`, and every other output untouched (`infra/live/use1/efs/terragrunt.hcl` keeps working unmodified)
- Pointed `infra/live/use1/network/terragrunt.hcl` at v1.1.0 and replaced the hardcoded `nat_gateway = { enabled = false }` with `tobool(get_env("KM_NAT_GATEWAY_ENABLED", "false"))`, with an inline comment explaining why the `tobool()` wrap is load-bearing here (unlike every other `get_env` toggle in this repo, which flows into a Lambda string-map env block)
- Added `pkg/terragrunt/network_module_v110_test.go` (4 test functions, package `terragrunt_test`, reusing the existing `findRepoRoot` helper) that locks down the per-AZ counts, the 3 moved blocks, the plural-plus-preserved outputs, and the additive nature of the version bump — pure text/regexp assertions, no `terraform` shell-out
- `v1.0.0` left completely untouched (`git status --porcelain infra/modules/network/v1.0.0` prints nothing)

## Task Commits

Each task was committed atomically:

1. **Task 1: Create infra/modules/network/v1.1.0 with per-AZ NAT, EIP and private route tables** - `dc861a76` (feat)
2. **Task 2: Point the network live unit at v1.1.0 and read the NAT toggle from KM_NAT_GATEWAY_ENABLED** - `29302f1a` (feat)
3. **Task 3: Add a Go structural regression test over the v1.1.0 network module** - `e3cd6442` (test)

**Plan metadata:** committed separately below (docs: complete plan)

## Files Created/Modified
- `infra/modules/network/v1.1.0/main.tf` - Per-AZ NAT gateway, EIP, and private route table resources plus 3 `moved` blocks
- `infra/modules/network/v1.1.0/variables.tf` - Unchanged copy of v1.0.0 (`nat_gateway.enabled` already shaped correctly)
- `infra/modules/network/v1.1.0/outputs.tf` - Plural `private_route_table_ids` / `nat_gateway_ids` / `nat_eip_public_ips` outputs replacing the old singular ones
- `infra/live/use1/network/terragrunt.hcl` - Sources v1.1.0; `nat_gateway.enabled` now reads `KM_NAT_GATEWAY_ENABLED` via `tobool(get_env(...))`
- `pkg/terragrunt/network_module_v110_test.go` - 4 new test functions asserting the module's per-AZ structure

## Decisions Made
- Implemented all 3 `moved` blocks exactly as specified in the plan (`aws_route_table.private`, `aws_eip.nat`, `aws_nat_gateway.nat`), even though only `aws_route_table.private` is a genuine singleton-to-indexed state-address transition — `aws_eip.nat` and `aws_nat_gateway.nat` already carried `count = enabled ? 1 : 0` in v1.0.0, so their state address was already `[0]`-indexed and their `moved` blocks are semantically inert (Terraform finds nothing at the bare unindexed address to relocate). They're harmless and satisfy the plan's explicit `grep -c 'moved {' == 3` acceptance criterion, so implemented as specified rather than second-guessed.
- Extended `TestNetworkModuleV110Outputs` to also assert the *absence* of the old singular output names in v1.1.0's `outputs.tf`, beyond the plan's literal Test 4 wording — this directly locks in Task 1's "Replace" instruction (not just "add the new ones") and would catch a future accidental re-add of a singular output that returns `null` under multi-AZ NAT.
- Task 3 carries `tdd="true"` but has no separate `<implementation>` block distinct from `<behavior>` — the structure it asserts was already built by Tasks 1–2 of this same plan. Treated this as a structural regression/lock-down test rather than forcing an artificial RED phase against already-correct code (see TDD Gate Compliance below).

## Deviations from Plan

None - plan executed exactly as written. No Rule 1-4 auto-fixes were needed, no authentication gates encountered, no blockers.

## TDD Gate Compliance

Task 3 (`tdd="true"`) produced a single `test(125-01): ...` commit (`e3cd6442`) with no separate RED-then-GREEN commit pair. This is intentional, not a missed gate: the plan's own Task 3 `<action>` describes writing only the test file (no accompanying `<implementation>` section), because the module structure it asserts was already implemented in Tasks 1–2 of this same plan. Running the new tests immediately after writing them showed 4/4 passing (`go test ./pkg/terragrunt/ -run 'TestNetworkModuleV110' -count=1 -v`, exit 0) — expected, since the underlying implementation predates the test by two commits. Forcing a contrived failing version of the test first would have tested nothing real. `go test ./pkg/terragrunt/ -count=1` (whole package, all pre-existing tests included) is green.

## Issues Encountered
- `terraform fmt` on first write found one alignment issue (`gateway_id` on `aws_route.public_igw` had stray extra spaces from a copy/paste edit) — caught by `terraform fmt -check -recursive`, fixed by running `terraform fmt` (no `-check`) once, then re-verified `-check` exits 0. No lock file or `.terraform/` directory was created by this (bare `terraform fmt` writes no lock file, unlike `terraform init`/`validate`).

## User Setup Required

None - no external service configuration required. This plan is pure Terraform-module authoring plus a Go test; no `terraform plan`/`apply` was run (out of scope for this plan — the live UAT sequence from `125-CONTEXT.md` runs in a later plan in this phase).

## Next Phase Readiness
- `infra/modules/network/v1.1.0` and its live-unit wiring are ready for `125-02` (ec2spot v1.3.0 rename) and the Go plumbing plans (`NetworkOutputs.PrivateSubnets`/`NATGatewayIDs`, the placement-resolution helper feeding the Phase 124 AZ sweep) to consume its new plural outputs.
- The `KM_NAT_GATEWAY_ENABLED` env var is wired on the consumer side only; its producer (`network.nat_gateway` in `km-config.yaml` → the config v2-to-v merge-list entry → env export) is Plan 06 per this plan's own `<artifacts_produced>` note — not yet done, so `km-config.yaml`'s `network.nat_gateway` key does nothing until Plan 06 lands.
- No blockers. `git status --porcelain infra/modules/network/v1.0.0` remains clean; no `.terraform`/lock-file artifacts anywhere under `infra/modules/network/`.

---
*Phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway*
*Completed: 2026-08-20*
