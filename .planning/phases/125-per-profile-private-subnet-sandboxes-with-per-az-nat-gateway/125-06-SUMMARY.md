---
phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway
plan: 06
subsystem: infra
tags: [km-init, terragrunt-env-export, nat-gateway, dynamodb, refuse-guard]

requires:
  - phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway (plan 01)
    provides: network module v1.1.0 plural outputs (private_subnets, nat_gateway_ids, nat_eip_public_ips, private_route_table_ids)
  - phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway (plan 03)
    provides: network.nat_gateway install-level config toggle + GetNATGatewayEnabled() accessor
  - phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway (plan 04)
    provides: SandboxMetadata.NetworkPlacement DynamoDB chokepoint
provides:
  - "NetworkOutputs.PrivateSubnets / NATGatewayIDs (populated on both the local outputs.json path and the S3 tfstate fallback used by --remote / the create-handler Lambda)"
  - "KM_NAT_GATEWAY_ENABLED env export from cfg.Network.NATGateway, with the repo-standard env-wins drift WARN"
  - "Pre-apply NAT cost notice + post-apply private-subnet/NAT-id/NAT-EIP display in km init"
  - "internal/app/cmd/init_nat_guard.go — pure natDisableGuard refuse-to-disable-NAT gate, wired as a pre-apply hook scoped to the network module"
affects: ["125-07 (km doctor NAT idle/missing checks)", "125-08 (compiler.NetworkConfig population from these outputs)", "km create", "km doctor"]

tech-stack:
  added: []
  patterns:
    - "Package-level nil-by-default hook var (InitNATDisableGuardHook), bound in runInit and cleared via defer, mirrors the existing InitSESPreflight / PublishOperatorIdentityHook test seam so RunInitWithRunner's module loop can invoke cfg/awsCfg-bound logic without changing RunInitWithRunner's signature"
    - "Dependency-injected lister func type (natGuardSandboxLister) keeps the guard's core (natDisableGuardPreApply) unit-testable with a nil or stub lister, while the pure predicate (natDisableGuard) takes no AWS types at all"

key-files:
  created:
    - internal/app/cmd/init_nat_guard.go
    - internal/app/cmd/init_nat_guard_test.go
  modified:
    - internal/app/cmd/init.go
    - internal/app/cmd/init_test.go

key-decisions:
  - "Split the plan's single 'if mod.name == network' pre-apply block into two adjacent blocks (cost notice, then guard invocation) purely so Task 2 and Task 3 could land as clean, independently-revertable commits touching non-overlapping concerns in the same function — no behavioral difference from a single combined block."
  - "'Disabling' is treated as a genuine transition (NAT currently exists, read from the network module's existing outputs.json nat_gateway_ids, AND the desired value is false) rather than merely 'desired value is false'. This means natDisableGuardPreApply skips the DynamoDB list call entirely (and never blocks) when NAT was never on or is already off — nothing to protect in that case. This is an extension beyond the pure natDisableGuard predicate the plan specified in <action>, added at the wiring layer so the AWS-optional guard doesn't need to walk sandbox rows on every km init when NAT was never enabled."
  - "The 6 behaviour tests from the plan are split as: 5 table-driven subtests under TestNATDisableGuard covering the pure predicate (tests 1-5), plus TestNATDisableGuard_NilListerDegradesGracefully covering test 6 (the wiring layer). Two additional defensive tests (lister-error-degrades, not-currently-enabled-skips-lister) were added beyond the plan's literal 6 to lock in the wiring-layer decision above; all pass."

requirements-completed: [REQ-125-TOGGLE, REQ-125-REMOTE, REQ-125-GUARD]

coverage:
  - id: D1
    description: "km init exports KM_NAT_GATEWAY_ENABLED from km-config.yaml's network.nat_gateway, guarded on non-nil, with an env-wins drift WARN matching every other *bool toggle export in this file (KM_SLACK_DEFAULT_ROUTER / KM_GITHUB_DEFAULT_ROUTER)"
    requirement: "REQ-125-TOGGLE"
    verification:
      - kind: unit
        ref: "go test ./internal/app/cmd/ -run 'TestExport|TestRunInit' -count=1 -timeout 600s"
        status: pass
      - kind: other
        ref: "grep -c 'KM_NAT_GATEWAY_ENABLED' internal/app/cmd/init.go == 6 (>= 2 required); block is guarded on cfg.Network.NATGateway != nil"
        status: pass
    human_judgment: false
  - id: D2
    description: "NetworkOutputs.PrivateSubnets / NATGatewayIDs are populated on both the local outputs.json extraction path (LoadNetworkOutputs) and the S3 raw-tfstate fallback (fetchAndCacheOutputs, unchanged — it re-serializes all outputs unfiltered) used by km create --remote and the create-handler Lambda; a pre-125 outputs.json with neither key still loads without error"
    requirement: "REQ-125-REMOTE"
    verification:
      - kind: unit
        ref: "internal/app/cmd/init_test.go#TestLoadNetworkOutputs_PrivateSubnetsAndNATGatewayIDs"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/init_test.go#TestLoadNetworkOutputs_Pre125Compatibility"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/init_test.go#TestLoadNetworkOutputs_NATGatewayIDsEmptyWhenDisabled"
        status: pass
    human_judgment: false
  - id: D3
    description: "km init refuses to disable NAT while any sandbox row is network_placement=private and status=running, naming the offending sandbox ids and the recovery (destroy them first); enabling NAT never blocks; no --force/--i-accept-destroys escape hatch exists"
    requirement: "REQ-125-GUARD"
    verification:
      - kind: unit
        ref: "internal/app/cmd/init_nat_guard_test.go#TestNATDisableGuard (5 subtests) + TestNATDisableGuard_NilListerDegradesGracefully + 2 wiring tests"
        status: pass
      - kind: other
        ref: "grep -c -- '-i-accept-destroys\\|--force' internal/app/cmd/init_nat_guard.go == 0"
        status: pass
    human_judgment: false

duration: ~50min
completed: 2026-08-19
status: complete
---

# Phase 125 Plan 06: km init — NAT toggle export, outputs plumbing, and the refuse-to-disable guard Summary

**`km init` is now the producer of `KM_NAT_GATEWAY_ENABLED`, the reader of network module v1.1.0's private-subnet/NAT-gateway outputs on both the local and remote paths, the printer of the NAT cost notice before spending it, and the enforcer of a pure, table-tested pre-apply gate that refuses to pull NAT out from under a running private sandbox.**

## Performance

- **Duration:** ~50 min
- **Tasks:** 3/3 completed
- **Files modified:** 4 (2 created, 2 modified)

## Accomplishments
- `NetworkOutputs` gained `PrivateSubnets []string` and `NATGatewayIDs []string`, extracted non-fatally in `LoadNetworkOutputs` (mirroring the existing `sandbox_mgmt_sg_id` pattern) so a pre-125 `outputs.json` — including one bundled into a stale create-handler Lambda — still loads with the two new fields simply empty. `fetchAndCacheOutputs` (the S3 raw-tfstate fallback for `km create --remote`) needed no code change: it already re-serializes all outputs unfiltered.
- `km init` exports `KM_NAT_GATEWAY_ENABLED` from `cfg.Network.NATGateway` in `ExportTerragruntEnvVars`, using the exact `strconv.FormatBool` / env-wins / drift-WARN shape as the `KM_SLACK_DEFAULT_ROUTER` and `KM_GITHUB_DEFAULT_ROUTER` exemplars, guarded on `cfg.Network.NATGateway != nil` so an absent `network:` block leaves the env untouched and the terragrunt `get_env(..., "false")` default applies.
- A pre-apply NAT cost notice (roughly $132/month baseline for 4 AZs plus $0.045/GB, with a concrete 300GB GPU-weights example) now prints immediately before the network module's `Apply` whenever `KM_NAT_GATEWAY_ENABLED=true` — before the operator has spent anything, not after. The post-apply network summary gained three more lines (private subnets, NAT gateway ids, NAT EIP public IPs in AZ order) printed only when the module actually emitted them.
- New `internal/app/cmd/init_nat_guard.go`: a pure `natDisableGuard(desiredEnabled bool, sandboxes []awspkg.SandboxMetadata) error` — no AWS client in its signature — refuses a NAT-disabling apply when one or more sandbox rows are both `network_placement=private` and `status=running`, naming every offending sandbox id and the recovery (`km destroy <id> --remote --yes`, then re-run `km init`). No `--force`/`--i-accept-destroys` override exists by design. A dependency-injected wiring layer (`natDisableGuardPreApply` + `natGuardSandboxLister`) determines whether this is a genuine disable (NAT currently on, read from the network module's own `outputs.json`, AND the desired value is false) before touching DynamoDB at all, and degrades to a WARN-and-proceed on any lister failure or absence — an operator with no DynamoDB access is never locked out of `km init`.
- The guard is wired into `RunInitWithRunner`'s module loop via a new nil-by-default package hook, `InitNATDisableGuardHook`, bound in `runInit` (closed over `cfg`/`awsCfg`/`repoRoot`/`region`) exactly like the existing `InitSESPreflight` and `PublishOperatorIdentityHook` test seams — no signature change to the exported `RunInitWithRunner`.

## Task Commits

1. **Task 1: Extend NetworkOutputs with PrivateSubnets and NATGatewayIDs** — `47361a16` (feat)
2. **Task 2: Export KM_NAT_GATEWAY_ENABLED with drift WARN, print NAT cost and NAT outputs** — `54cf6b91` (feat)
3. **Task 3: Refuse a NAT-disabling km init while private sandboxes are running** — `76c68bb1` (feat)

**Plan metadata:** committed separately below (docs: complete plan).

## Files Created/Modified
- `internal/app/cmd/init.go` — `NetworkOutputs` fields + non-fatal extraction; `KM_NAT_GATEWAY_ENABLED` export block; pre-apply cost notice + post-apply NAT display; `InitNATDisableGuardHook` var, its `runInit` binding, and its pre-apply invocation scoped to the `network` module
- `internal/app/cmd/init_test.go` — `mockRunner.Output` fixture gained `private_subnets`/`nat_gateway_ids`; 3 new `TestLoadNetworkOutputs_*` tests; new `reflect` import
- `internal/app/cmd/init_nat_guard.go` (new) — `natDisableGuard` (pure), `natDisableGuardPreApply` (dependency-injected wiring core), `checkNATDisableGuardBeforeApply` (cfg/awsCfg-bound production entry point), `natCurrentlyEnabledFromOutputs`
- `internal/app/cmd/init_nat_guard_test.go` (new) — `TestNATDisableGuard` (5 subtests), `TestNATDisableGuard_NilListerDegradesGracefully`, plus 2 additional wiring-layer tests

## Decisions Made
- Split the plan's single combined "cost notice + guard" pre-apply block into two adjacent `if mod.name == "network"` blocks so Task 2 and Task 3 could each land as an independently-revertable commit — behaviorally identical to one combined block (both still run in the cost-notice-then-guard order before `Apply`).
- Defined "disabling" at the wiring layer as a genuine state transition — NAT currently exists (from `outputs.json`'s `nat_gateway_ids`) AND the desired value is false — rather than merely "desired value is false" as literally stated in the plan's <action> prose. This means the DynamoDB list call (and thus any possibility of blocking) is skipped entirely when NAT was never enabled or is already off, since there is nothing to protect in that state. The plan itself directed exactly this read-from-outputs.json approach ("Deriving whether NAT was previously ON can be read from the existing `nat_gateway_ids`... Use that rather than trying to read prior config state") — this decision operationalizes that instruction at the wiring boundary rather than inside the pure predicate, keeping `natDisableGuard` itself simple and matching its literal plan signature `(desiredEnabled bool, sandboxes []SandboxMetadata) error`.
- `mockRunner.Output`'s NAT fixture values (`private_subnets`, `nat_gateway_ids`) were added even though no existing test in `init_test.go` exercised that mock path for these keys — matches the plan's explicit Task 1 instruction and keeps the fixture representative for any future test exercising `RunInitWithRunner`'s post-apply display block.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 — literal acceptance-criteria grep vs. gofmt struct alignment] `NATGatewayIDs  []string` has two spaces, not one**
- **Found during:** Task 1, verifying `grep -c 'NATGatewayIDs \[\]string' internal/app/cmd/init.go` against the literal acceptance criterion.
- **Issue:** gofmt right-aligns the new `NetworkOutputs` struct's field types; `NATGatewayIDs` (13 chars) is one character shorter than `PrivateSubnets` (14 chars), so gofmt inserts a second space before `[]string` to keep the column aligned. `grep -c 'NATGatewayIDs \[\]string'` (single space) therefore prints `0`, not `1`.
- **Fix:** None needed — the field exists exactly as specified (`grep -n NATGatewayIDs` confirms it), `go build` and `go vet` are clean, and this is purely a gofmt cosmetic outcome the plan's literal grep pattern didn't anticipate. Not "fixed" by de-aligning the struct (that would itself fail `gofmt -l`).
- **Files affected:** `internal/app/cmd/init.go`
- **Commit:** `47361a16`
- **Consequence:** the field is present and correctly typed; only the exact-literal-grep acceptance check undercounts by gofmt's alignment behavior, matching the precedent already logged in 125-04's SUMMARY (`project` memory: "plan's literal grep numbers are not always achievable").

## Issues Encountered
None beyond the gofmt-alignment grep mismatch documented above.

## User Setup Required
None for this plan alone. Per `125-CONTEXT.md`'s documented deploy surface for the full phase: `make build` (before `km init` — this plan's binary changes must be built in), then `make build-lambdas`, then `km init --dry-run=false`. This plan alone changes only the operator-side `km` binary (no Lambda/schema/Terraform change), so `make build` is sufficient to pick up these changes; the fuller deploy sequence applies once later plans in this phase (Plan 07/08) also land.

## Next Phase Readiness
- `NetworkOutputs.PrivateSubnets`/`NATGatewayIDs` are available for Plan 08's `compiler.NetworkConfig` population and for `km create`'s placement-resolution helper.
- `cfg.Network.NATGateway` now reaches terragrunt via `KM_NAT_GATEWAY_ENABLED`; a real `km init --dry-run=false` with `network.nat_gateway: true` will provision the per-AZ NAT/EIP/route-table infrastructure from Plan 01's v1.1.0 module.
- `natDisableGuard`/`InitNATDisableGuardHook` are ready to protect any private sandbox that Plan 08+ create-time wiring places — the guard already correctly treats a pre-125 row (`NetworkPlacement == ""`) as public, so no additional back-compat work is needed here.
- No blockers.

## Self-Check: PASSED

Files verified present on disk:
- FOUND: internal/app/cmd/init_nat_guard.go
- FOUND: internal/app/cmd/init_nat_guard_test.go
- FOUND: internal/app/cmd/init.go (modified)
- FOUND: internal/app/cmd/init_test.go (modified)

Commit hashes verified present in git log:
- FOUND: 47361a16 (Task 1)
- FOUND: 54cf6b91 (Task 2)
- FOUND: 76c68bb1 (Task 3)

Test verification (own exit status read directly, never through a pipe):
- `go test ./internal/app/cmd/ -run 'TestLoadNetworkOutputs' -count=1 -timeout 600s` — EXIT=0
- `go test ./internal/app/cmd/ -run 'TestExport|TestRunInit' -count=1 -timeout 600s` — EXIT=0
- `go test ./internal/app/cmd/ -run 'TestNATDisableGuard' -count=1 -timeout 600s -v` — EXIT=0, 9/9 PASS
- `go test ./internal/app/cmd/... -count=1 -timeout 600s` — EXIT=1, but the only failures are `TestBootstrapSCPApplyPath`, `TestBootstrapSCPSkipped_OrganizationBlank`, `TestClusterAdd` (both subtests), `TestClusterRm`, `TestClusterAddPersistFailure` — all failing with `get credentials: test: no real AWS credentials (fast-fail seam)`, a pre-existing environmental failure mode unrelated to this plan's `bootstrap.go`/`cluster.go`-untouched changes (matches the documented `project_frozen_byte_identity_golden_capture_trap` memory: "cmd Bootstrap/Cluster/Configure tests fail InvalidGrantException on expired AWS SSO (environmental)")
- `go build ./...` — clean
- `go vet ./internal/app/cmd/...` — clean
- `gofmt -l` on all 4 touched/created files — clean

---
*Phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway*
*Completed: 2026-08-19*
