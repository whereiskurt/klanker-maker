---
phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway
plan: 03
subsystem: infra
tags: [config, viper, json-schema, sandbox-profile, network, nat-gateway]

requires:
  - phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway (plan 01)
    provides: network module v1.1.0 (per-AZ NAT gateway/EIP/route-table Terraform)
  - phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway (plan 02)
    provides: ec2spot v1.3.0 sandbox_subnets/associate_public_ip + per-substrate version pin
provides:
  - "network.nat_gateway install-level config toggle (config.Config.Network.NATGateway, tri-state *bool)"
  - "GetNATGatewayEnabled() nil-safe accessor on *config.Config"
  - "spec.network.privateSubnet per-profile SandboxProfile field + JSON schema property"
  - "profiles/private-subnet.yaml demo profile for the live UAT"
affects: [125-04, 125-05, 125-06, 125-07, network-module, ec2spot-module, config-loader, profile-schema]

tech-stack:
  added: []
  patterns:
    - "New top-level km-config.yaml block without an existing atomic UnmarshalKey call: declare a tri-state *bool field, mirror the slack.default_router merge-list + load-logic + regression-test shape exactly"
    - "Profile spec field addition to a closed (additionalProperties:false) schema object requires BOTH the Go struct field AND a schema property edit; a Go-only change passes go test but fails km validate"

key-files:
  created:
    - profiles/private-subnet.yaml
  modified:
    - internal/app/config/config.go
    - internal/app/config/config_test.go
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/validate_test.go
    - scripts/validate-all-profiles.sh

key-decisions:
  - "network.nat_gateway follows the slack.default_router exemplar exactly (not github.default_router) because network: is a brand-new top-level block with no existing UnmarshalKey to piggyback on — needs its own merge-list entry, its own IsSet/GetBool load block, and its own 4-test regression shape."
  - "profiles/private-subnet.yaml does NOT extend base/network/safenetwork (allowedHosts: [\"*\"]) — a wildcard allowlist would make live UAT step 4 (assert evil.example.com is blocked) vacuous. Instead the leaf supplies its own real allowlist (.amazonaws.com suffix + sts/ssm/bedrock-runtime hosts), modeled on pkg/profile/builtins/hardened.yaml."
  - "No km validate warning added for spec.network.privateSubnet without network.nat_gateway — CONTEXT.md is explicit the NAT-state cross-check belongs at km create time, not validate time, because the profile is never wrong on its own."

requirements-completed: [REQ-125-TOGGLE]

coverage:
  - id: D1
    description: "network.nat_gateway install-level toggle round-trips through the config v2-to-v merge path into cfg.Network.NATGateway (tri-state true/false/absent), with a regression test guarding the merge-list entry"
    requirement: "REQ-125-TOGGLE"
    verification:
      - kind: unit
        ref: "internal/app/config/config_test.go#TestLoadNetworkNATGateway_True"
        status: pass
      - kind: unit
        ref: "internal/app/config/config_test.go#TestLoadNetworkNATGateway_False"
        status: pass
      - kind: unit
        ref: "internal/app/config/config_test.go#TestLoadNetworkNATGateway_Absent"
        status: pass
      - kind: unit
        ref: "internal/app/config/config_test.go#TestLoadNetworkNATGateway_MergeListRegression"
        status: pass
    human_judgment: false
  - id: D2
    description: "spec.network.privateSubnet is a schema-validated boolean on NetworkSpec; the enclosing spec.network object remains additionalProperties:false (an unrecognised sibling key still fails validation)"
    requirement: "REQ-125-TOGGLE"
    verification:
      - kind: unit
        ref: "pkg/profile/validate_test.go#TestValidateSchema_PrivateSubnet"
        status: pass
    human_judgment: false
  - id: D3
    description: "Concrete profiles/private-subnet.yaml demo profile with a real (non-wildcard) egress allowlist passes km validate and is registered in the repo's profile-validation gate"
    verification:
      - kind: integration
        ref: "bash scripts/validate-all-profiles.sh (ok profiles/private-subnet.yaml; all 21 profiles valid; exit 0)"
        status: pass
    human_judgment: false

duration: 35min
completed: 2026-08-19
status: complete
---

# Phase 125 Plan 03: Dormant-by-default toggles — network.nat_gateway + spec.network.privateSubnet Summary

**Adds the two decoupled dormant-by-default knobs the whole Phase 125 hangs off: an install-level `network.nat_gateway` config toggle (with its own merge-list entry and regression test) and a per-profile `spec.network.privateSubnet` schema field, plus a demo profile for the live UAT.**

## Performance

- **Duration:** ~35 min
- **Tasks:** 3/3 completed
- **Files modified:** 6 (1 created, 5 modified)

## Accomplishments
- `network.nat_gateway` (km-config.yaml) round-trips into `cfg.Network.NATGateway` (tri-state `*bool`) through the full viper v2-to-v merge path, with `TestLoadNetworkNATGateway_MergeListRegression` guarding the exact silent-drop footgun this repo has hit repeatedly (`project_config_key_merge_list`). Absent block → nil → `GetNATGatewayEnabled()` false → Phase 124 byte-identical.
- `spec.network.privateSubnet` is now a first-class, schema-validated boolean on `NetworkSpec` (plain non-pointer `bool`, like sibling `HTTPSOnly`). The `spec.network` JSON schema object stayed `additionalProperties: false` — verified by a test that an unrecognised sibling key still fails validation.
- `profiles/private-subnet.yaml` is a lean, spot `t3.medium` concrete leaf whose distinguishing feature is `privateSubnet: true`, carrying a real (non-wildcard) egress allowlist so the live UAT's "evil.example.com still blocked from a private subnet" assertion is non-vacuous. Registered in `scripts/validate-all-profiles.sh`; the profile-inventory count self-updates to 21.

## Task Commits

Each task was committed atomically (TDD split into test/RED then feat/GREEN commits):

1. **Task 1: network.nat_gateway install-level toggle**
   - `1abf5212` test(125-03): add failing test for network.nat_gateway config toggle
   - `e606be67` feat(125-03): implement network.nat_gateway install-level toggle
2. **Task 2: spec.network.privateSubnet field + schema**
   - `4bed6b4c` test(125-03): add failing test for spec.network.privateSubnet schema field
   - `34e35c07` feat(125-03): add spec.network.privateSubnet field and schema property
3. **Task 3: private-placement demo profile + validation gate registration**
   - `cf983c52` feat(125-03): add private-subnet demo profile and register in validation gate

_TDD tasks (1 and 2) each ran RED (build-fails-to-compile confirmed) before GREEN, per project convention._

## Files Created/Modified
- `internal/app/config/config.go` — new `NetworkConfig` struct (`NATGateway *bool`), `Config.Network` field, v2-to-v merge-list entry `"network.nat_gateway"`, load logic mirroring `slack.default_router`, nil-safe `GetNATGatewayEnabled()` accessor
- `internal/app/config/config_test.go` — `TestLoadNetworkNATGateway_{True,False,Absent,MergeListRegression}`
- `pkg/profile/types.go` — `NetworkSpec.PrivateSubnet bool` (`yaml:"privateSubnet,omitempty"`)
- `pkg/profile/schemas/sandbox_profile.schema.json` — `privateSubnet` boolean property under `spec.network`
- `pkg/profile/validate_test.go` — `TestValidateSchema_PrivateSubnet` (4 subtests: accept, unmarshal, reject-unknown-sibling, zero-value)
- `profiles/private-subnet.yaml` (new) — private-placement demo profile
- `scripts/validate-all-profiles.sh` — registered the new profile; header count 20→21

## Decisions Made
- Followed the `slack.default_router` chain exactly for `network.nat_gateway` rather than the atomic-`UnmarshalKey` github/h1/checks precedent, because `network:` has no existing block-level unmarshal to extend.
- Did not extend `base/network/safenetwork` in the demo profile (its `allowedHosts: ["*"]` would make the live-UAT egress-filtering assertion vacuous); supplied a real allowlist directly in the leaf instead, modeled on `pkg/profile/builtins/hardened.yaml`.
- No `km validate` warning added for `privateSubnet` without install-level NAT — per CONTEXT.md, that cross-field/cross-artifact consistency check is deliberately deferred to `km create` time (Plans 06/07), not `km validate`.

## Deviations from Plan
None — plan executed exactly as written. `make build` (run as part of Task 3's verify step) advanced the dev-build `VERSION` file (0.5.56 → 0.5.57); this is the standard, expected side effect of `make build` per repo convention and was committed alongside Task 3.

## Issues Encountered
None.

## User Setup Required
None — no external service configuration required. Both toggles are dormant by default; no deploy action needed for this plan alone (the deploy surface — `make build` before `km init`, `make build-lambdas`, `km init --dry-run=false` — is documented in 125-CONTEXT.md for the plans that consume these toggles at runtime).

## Next Phase Readiness
`cfg.Network.NATGateway` / `GetNATGatewayEnabled()` and `profile.NetworkSpec.PrivateSubnet` are now available for the Go plumbing plans (subnet resolution, AZ-sweep integration, `km create` fail-fast guard, `km doctor` checks, `SandboxMetadata.network_placement`). No blockers.

---
*Phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway*
*Completed: 2026-08-19*
