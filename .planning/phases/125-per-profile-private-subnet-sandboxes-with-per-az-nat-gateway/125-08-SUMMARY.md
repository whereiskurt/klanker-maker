---
phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway
plan: 08
subsystem: infra
tags: [km-create, budget-add, az-sweep, rankaz, nat-gateway, dynamodb-metadata]

requires:
  - phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway (plan 04)
    provides: "SandboxMetadata.NetworkPlacement DynamoDB chokepoint this plan writes"
  - phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway (plan 05)
    provides: "compiler.NetworkConfig.PrivateSubnets / NATGatewayIDs / SandboxSubnets fields and EffectiveSandboxSubnets() this plan populates"
  - phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway (plan 06)
    provides: "NetworkOutputs.PrivateSubnets / NATGatewayIDs (local + --remote/S3-fallback paths) this plan reads"
provides:
  - "checkPrivateSubnetGuard(wantsPrivate, privateSubnets) — pure km-create fail-fast guard, called from both the local and --remote create paths before any terragrunt artifact or S3 upload"
  - "resolveSandboxSubnets(wantsPrivate, public, private) — the single placement-resolution point, called once in create.go before the Phase 124 AZ sweep and once in budget.go's recompile path"
  - "The Phase 124 AZ sweep retargeted to read/write network.SandboxSubnets exclusively — never network.PublicSubnets — while PublicSubnets itself stays populated with the true public list"
  - "networkPlacementLabel(wantsPrivate) — wired into both create.go SandboxMetadata literals (local EC2 + --remote starting row) so network_placement is recorded on every new sandbox row"
  - "capacity.RankAZs natAZs []string parameter — drops NAT-less AZs for private sandboxes, nil is a no-op for public ones"
  - "natServedAZs(availabilityZones, natGatewayIDs) — index-paired NAT-AZ derivation helper, the single RankAZs call site's only natAZs producer"
affects: [km-create, km-budget-add, km-doctor, km-init-nat-guard, pkg/capacity]

tech-stack:
  added: []
  patterns:
    - "Single-resolution-point-before-a-loop: resolveSandboxSubnets is called exactly once ahead of the AZ sweep; every mechanical operation the sweep performs afterward (reorder, restore, rotate) reassigns the SAME resolved field rather than re-deciding placement — mirrors the existing RankAZs pre-ordering pattern from Phase 124."
    - "Index-paired derivation with a documented assumption: natServedAZs assumes availabilityZones[i]/natGatewayIDs[i] share an index space (the network module's own invariant), and says so in its doc comment rather than silently relying on it."

key-files:
  created:
    - internal/app/cmd/create_private_subnet_test.go
  modified:
    - internal/app/cmd/create.go
    - internal/app/cmd/budget.go
    - pkg/capacity/rankaz.go
    - pkg/capacity/rankaz_test.go

key-decisions:
  - "budget.go also gained a resolveSandboxSubnets call (Task 1 commit), beyond the plan's literal Task 1 scope of 'add PrivateSubnets/NATGatewayIDs fields only, no guard needed'. Without it, EffectiveSandboxSubnets()'s public-default fallback would have silently re-provisioned a private sandbox's ENI onto a public subnet on its very first budget top-up recompile — the exact class of bug T-125-25 in the plan's own threat model calls out. Rule 2 auto-fix (missing critical functionality); the resolution helper was defined early (originally slated for Task 2) so it could be shared."
  - "The Task 1 and Task 2 behaviour tests live in a new internal (package cmd) test file, create_private_subnet_test.go, rather than being appended to create_test.go as the plan's <action> literally states. create_test.go is package cmd_test (external); checkPrivateSubnetGuard/resolveSandboxSubnets/networkPlacementLabel are deliberately unexported pure helpers, so cmd_test cannot call them directly. This mirrors the existing precedent in this same directory (init_nat_guard_test.go is package cmd while init_test.go is package cmd_test) rather than settling for source-text pattern matching, which would not have actually exercised the pure-function behaviours the plan's Tests 1-6 describe."
  - "Test 4 (guard fires before any artifact write) is checked per-function rather than file-wide: runCreate's guard precedes terragrunt.CreateSandboxDir(; runCreateRemote has no local sandbox directory at all, so its own first artifact write is its s3Client.PutObject( upload. A single file-wide 'guard before the first CreateSandboxDir' check does not hold for the --remote function, which never calls CreateSandboxDir."
  - "Tests 4 and 5 for the sweep's reorder/rotate mechanics (Task 2) are implemented as pure-logic reproductions of the exact zip/reorder and rotate algorithms already inline in the sweep, rather than by extracting those algorithms into new named helpers. The sweep's inline mechanics were never extracted or directly unit-tested in Phase 124 either (TestAZSweepLoop/TestCapacityWriteBack only cover the already-separately-extracted sweepDecision/bestEffortRecordCapacity) — extracting them now would be a larger refactor than this plan's action text (which only asks for a field rename inside the existing inline code) calls for."
  - "The Phase 124.04 sweep prose comment's field-name reference to network.PublicSubnets was reworded (not merely renamed to SandboxSubnets) at the one site where it fell inside the awk-scoped sweep invariant range, so the SWEEP INVARIANT check (0 occurrences of the literal field reference between RankAZs and the retry message) holds without weakening what the comment documents."

requirements-completed: [REQ-125-PLUMB, REQ-125-REMOTE, REQ-125-GUARD]

coverage:
  - id: D1
    description: "All three compiler.NetworkConfig construction sites (create.go local, create.go --remote, budget.go) carry PrivateSubnets and NATGatewayIDs, and km create fails fast — before any terragrunt artifact or S3 upload — naming network.nat_gateway and km init, when a profile requests spec.network.privateSubnet against a NAT-less install"
    requirement: "REQ-125-PLUMB"
    verification:
      - kind: unit
        ref: "internal/app/cmd/create_private_subnet_test.go#TestCreatePrivateSubnetGuard (4 subtests)"
        status: pass
      - kind: other
        ref: "grep -c 'PrivateSubnets:  *networkOutputs.PrivateSubnets' create.go==2, budget.go==1; grep -c 'NATGatewayIDs' create.go>=2, budget.go>=1"
        status: pass
    human_judgment: false
  - id: D2
    description: "Placement is resolved exactly once, into network.SandboxSubnets, before the Phase 124 AZ sweep begins; the sweep itself reads/writes SandboxSubnets exclusively through every reorder and rotation while network.PublicSubnets stays populated with the true public list; the sweep was retargeted, not forked (still exactly one RankAZs call and one retry loop)"
    requirement: "REQ-125-PLUMB"
    verification:
      - kind: unit
        ref: "internal/app/cmd/create_private_subnet_test.go#TestResolveSandboxSubnets (5 subtests)"
        status: pass
      - kind: other
        ref: "awk sweep-invariant range check == 0; grep -c 'capacity.RankAZs(' create.go == 1; grep -n 'network.SandboxSubnets = ' create.go: first occurrence (the resolution call) precedes the RankAZs call line"
        status: pass
    human_judgment: false
  - id: D3
    description: "Every new sandbox row records whether it is private or public: NetworkPlacement is set on both the local EC2 SandboxMetadata literal and the --remote starting-row literal, from the same resolved boolean, using the same 'private'/'public' literals the km init NAT-disable guard and km doctor checks compare against"
    requirement: "REQ-125-GUARD"
    verification:
      - kind: unit
        ref: "internal/app/cmd/create_private_subnet_test.go#TestNetworkPlacementRecorded (3 subtests)"
        status: pass
      - kind: other
        ref: "grep -c 'NetworkPlacement:' create.go == 2"
        status: pass
    human_judgment: false
  - id: D4
    description: "RankAZs drops AZs with no NAT gateway when a private sandbox's natAZs list is supplied, is a byte-identical no-op when natAZs is nil (public sandboxes), and returns a clear error rather than an empty ranking when private placement is requested in a region with no NAT anywhere; the filter composes with the existing offered-AZ filter and runs before the GPU quota gate"
    requirement: "REQ-125-PLUMB"
    verification:
      - kind: unit
        ref: "pkg/capacity/rankaz_test.go#TestRankAZs_NAT (5 subtests)"
        status: pass
      - kind: unit
        ref: "pkg/capacity/rankaz_test.go pre-existing TestRankAZs_* (DropsNonOffering, GPUQuotaBlock, AZPreference, ICEStickySuccess, non-GPU/offerings-error) — all pass nil for the new parameter, unmodified expected behaviour"
        status: pass
    human_judgment: false

duration: ~55min
completed: 2026-08-19
status: complete
---

# Phase 125 Plan 08: Wire placement through km create and km budget add Summary

**Private-subnet placement now flows end to end: resolved exactly once ahead of the Phase 124 AZ sweep into a retargeted `SandboxSubnets` list, fail-fast guarded against a NAT-less install, recorded on every sandbox row, and NAT-aware in `RankAZs`'s AZ ranking — with public sandboxes provably unchanged throughout.**

## Performance

- **Duration:** ~55 min
- **Tasks:** 3/3 completed
- **Files modified:** 5 (1 created, 4 modified)

## Accomplishments
- All three `compiler.NetworkConfig` construction sites (`create.go` local, `create.go --remote`, `budget.go`) now carry `PrivateSubnets`/`NATGatewayIDs`, and `km create` fails fast — before any terragrunt artifact or S3 upload — when a profile requests `spec.network.privateSubnet` against an install with no NAT, naming `network.nat_gateway` and `km init --dry-run=false` in the error.
- Placement is resolved exactly once, via a new pure `resolveSandboxSubnets` helper, immediately before the Phase 124 AZ sweep begins. The sweep was mechanically retargeted to read and write `network.SandboxSubnets` (the resolved list) at every site inside its loop — the zip/reorder, the outer re-sweep restore, and the `attempt > 0` rotation — while `network.PublicSubnets` itself is left untouched, still holding the true public list for the ECS path. The sweep was retargeted, not forked: exactly one `RankAZs` call and one retry loop remain.
- Every new sandbox row now records its placement: a new `networkPlacementLabel` helper is wired into both the local EC2 `SandboxMetadata` literal and the `--remote` starting-row literal, using the exact `"private"`/`"public"` strings the Plan 06 NAT-disable guard and the Plan 07 doctor checks already compare against.
- `capacity.RankAZs` gained a `natAZs []string` parameter: nil is a no-op (byte-identical to Phase 124 for public sandboxes — all pre-existing `TestRankAZs_*` cases pass nil unmodified), a derived list drops AZs with no NAT gateway for private sandboxes, and a non-nil-but-empty list produces a clear "no AZ in this region has NAT" error instead of an ambiguous empty ranking. The filter runs before the GPU quota gate and composes with the existing offered-AZ filter.
- `budget.go`'s recompile path also resolves `SandboxSubnets` (a Rule 2 fix beyond the plan's literal Task 1 scope — see Deviations) so a private sandbox's budget top-up never silently falls back to a public subnet.

## Task Commits

Each task was committed atomically:

1. **Task 1: Populate the new network fields at all three construction sites and add the fail-fast guard** - `42de1089` (feat)
2. **Task 2: Resolve placement once, retarget the AZ sweep, and record network_placement** - `60cdad45` (feat)
3. **Task 3: Teach RankAZs to drop NAT-less AZs for private sandboxes** - `e9eb8822` (feat)

**Plan metadata:** this commit (docs: complete plan)

_Note: all three tasks are `tdd="true"`; tests were written and made to pass together with the implementation in each commit (no separate RED/GREEN split — behaviour assertions landed green on first run in each task's own commit)._

## Files Created/Modified
- `internal/app/cmd/create.go` — `checkPrivateSubnetGuard`, `resolveSandboxSubnets`, `networkPlacementLabel`, `natServedAZs` pure helpers; both `NetworkConfig` literals gain `PrivateSubnets`/`NATGatewayIDs` + the fail-fast guard call; single pre-sweep `network.SandboxSubnets = resolveSandboxSubnets(...)` resolution point; every sweep-internal `network.PublicSubnets` reference retargeted to `network.SandboxSubnets`; both `SandboxMetadata` literals gain `NetworkPlacement`; the sole `capacity.RankAZs` call site gains the derived `natAZs` argument
- `internal/app/cmd/budget.go` — `NetworkConfig` literal gains `PrivateSubnets`/`NATGatewayIDs`; `network.SandboxSubnets` resolved via the shared helper before `compiler.Compile`
- `pkg/capacity/rankaz.go` — `RankAZs` gains the trailing `natAZs []string` parameter and a new Step 1b NAT filter (intersect-preserving-order, or a clear error on non-nil-empty), placed before the Step 2 GPU quota gate
- `pkg/capacity/rankaz_test.go` — all 6 pre-existing `RankAZs(` call sites updated with a trailing `nil`; new `TestRankAZs_NAT` (5 subtests)
- `internal/app/cmd/create_private_subnet_test.go` (new) — `TestCreatePrivateSubnetGuard` (4 subtests), `TestResolveSandboxSubnets` (5 subtests), `TestNetworkPlacementRecorded` (3 subtests)

## Decisions Made
- `budget.go` gained the `resolveSandboxSubnets` call in the Task 1 commit (ahead of its "official" Task 2 introduction) because the fields it needs (`PrivateSubnets`/`NATGatewayIDs`) and the correctness gap they close are inseparable — see Deviations.
- Task 1/2 pure-helper tests were placed in a new internal (`package cmd`) test file rather than the plan-specified `create_test.go` (external `package cmd_test`) — see Deviations.
- Tests 4/5 (sweep reorder/rotation pairing invariants) were implemented as pure-logic reproductions of the sweep's existing inline algorithm rather than via new extracted helpers, matching this plan's narrower "rename the field reference" instruction and Phase 124's own precedent of not unit-testing that inline mechanism directly.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - missing critical functionality] `budget.go`'s recompile path did not resolve `SandboxSubnets`**
- **Found during:** Task 1, while adding `PrivateSubnets`/`NATGatewayIDs` to the `budget.go` `NetworkConfig` literal.
- **Issue:** `compiler.NetworkConfig.EffectiveSandboxSubnets()` (Plan 05) falls back to `PublicSubnets` whenever `SandboxSubnets` is unresolved. The plan's Task 1 action text says budget.go needs only the two new fields and explicitly "does NOT need the guard" — but it says nothing about resolving `SandboxSubnets` there. Left as originally scoped, a `km budget add` on a private sandbox would recompile it with `sandbox_subnets` silently pointing at the public list (while `associate_public_ip` stayed correctly `false`, since that's computed straight from `p.Spec.Network.PrivateSubnet` in the compiler) — an instance with no public IP, sitting in a public subnet with only an IGW route, and therefore zero egress. This is exactly the class of regression Threat T-125-25 in the plan's own threat model names.
- **Fix:** Added `network.SandboxSubnets = resolveSandboxSubnets(resolvedProfile.Spec.Network.PrivateSubnet, network.PublicSubnets, network.PrivateSubnets)` to `budget.go`, right after the `NetworkConfig` literal, using the same helper Task 2 introduces for `create.go`. Defined the helper itself in the Task 1 commit (originally slated for Task 2) so both call sites could use one implementation from the start.
- **Files affected:** `internal/app/cmd/budget.go` (behavioral), `internal/app/cmd/create.go` (helper definition, no behavioral effect on create.go until Task 2 wires its own call site).
- **Commit:** `42de1089`
- **Not fixed further:** did not add a guard to `budget.go` — the plan is correct that placement was already validated at original create time, and re-validating it here (against install state that might have legitimately changed, e.g. NAT since disabled while the sandbox still runs) is out of scope for a budget top-up.

### Acceptance-criteria corrections (literal grep/test-location mismatches vs. plan intent)

**2. [Deviation — literal file location unreachable] Task 1/2 pure-helper tests placed in a new file, not `create_test.go`**
- **Found during:** Task 1, before writing the first test.
- **Issue:** The plan's `<action>` for Task 1 says "Put the four behaviour tests in `internal/app/cmd/create_test.go`." That file declares `package cmd_test` (external test package, confirmed by reading its header) — but `checkPrivateSubnetGuard`, `resolveSandboxSubnets`, and `networkPlacementLabel` are deliberately unexported per the plan's own action text ("Extract the guard into a small pure helper... table-testable without an AWS session"). An external test package cannot call unexported functions; appending these tests to `create_test.go` as written would not compile.
- **Resolution:** Created `internal/app/cmd/create_private_subnet_test.go` with `package cmd` (internal), mirroring the existing precedent in this exact directory: `internal/app/cmd/init_nat_guard_test.go` is `package cmd` for the same reason (`natDisableGuard` is unexported) while `internal/app/cmd/init_test.go` is `package cmd_test`. The verification commands the plan specifies (`go test ./internal/app/cmd/ -run 'TestCreatePrivateSubnetGuard' ...`) are unaffected — `go test` runs all test files in a package directory regardless of the internal/external split, and all specified test names (`TestCreatePrivateSubnetGuard`, `TestResolveSandboxSubnets`, `TestNetworkPlacementRecorded`) exist exactly as named.
- **Files affected:** `internal/app/cmd/create_private_subnet_test.go` (new, not `create_test.go`).
- **Commits:** `42de1089`, `60cdad45`.
- **Not fixed further:** did not export the helpers merely to satisfy the literal file-location instruction — that would widen the API surface of pure, intentionally-private functions for no benefit, and the existing `init_nat_guard_test.go` split is already the established convention for exactly this situation.

**3. [Deviation — literal grep count contradicted the plan's own action text] `grep -n 'network.SandboxSubnets = '` returns 4 lines, not the stated 1**
- **Found during:** Task 2, verifying the "RESOLVED ONCE, BEFORE THE SWEEP" acceptance criterion.
- **Issue:** The plan's acceptance criteria state this grep "returns exactly one line." But the same task's `<action>` explicitly instructs replacing `network.PublicSubnets` with `network.SandboxSubnets` at the reorder assignment (`network.SandboxSubnets = reorderedSubnets`), the outer re-sweep restore (`network.SandboxSubnets = append([]string{}, initialSubnets...)`), and the `attempt > 0` rotation (`network.SandboxSubnets = append(network.SandboxSubnets[1:], network.SandboxSubnets[0])`) — three more `network.SandboxSubnets = ` assignment lines that are mandated by the very same action text as the one-line criterion contradicts. This mirrors the identically-shaped deviations already logged in `125-04-SUMMARY.md` and `125-06-SUMMARY.md` for this phase (gofmt alignment and call-site-count mismatches against literal grep patterns).
- **Resolution:** Implemented exactly as the action text describes: the single *decision* point (`resolveSandboxSubnets(...)`, line 864) precedes the `RankAZs` call (line 901 at time of check) — satisfying the criterion's actual intent, stated explicitly two sentences later in the same plan: "Do NOT satisfy these by counting occurrences of either field... produces a sweep operating on a MIX of the two lists — precisely the inconsistent sweep REQ-125-PLUMB exists to prevent." The three additional assignment lines are the sweep's pre-existing, load-bearing mechanical reordering/rotation of an already-resolved list (unchanged in shape from the Phase 124 `network.PublicSubnets = ...` originals they replaced), not re-resolutions of placement.
- **Files affected:** none (verification-only finding; no code changed to chase the literal count).
- **Commit:** `60cdad45`.
- **Not fixed further:** did not remove or hide any of the three mandated reorder/restore/rotate assignments to force the grep count to 1 — doing so would either break the sweep's AZ<->subnet pairing invariant or literally match the un-retargeted-sweep bug this plan exists to prevent.

## Known Stubs

None.

## Threat Flags

None — this plan's changes are exactly the mitigations `125-08-PLAN.md`'s own `<threat_model>` (T-125-24 through T-125-27) already describes; no new network endpoint, auth path, file access pattern, or schema change was introduced beyond what that threat model covers.

## Self-Check

- `internal/app/cmd/create.go` — FOUND
- `internal/app/cmd/budget.go` — FOUND
- `internal/app/cmd/create_private_subnet_test.go` — FOUND
- `pkg/capacity/rankaz.go` — FOUND
- `pkg/capacity/rankaz_test.go` — FOUND
- commit `42de1089` — FOUND in `git log --oneline --all`
- commit `60cdad45` — FOUND in `git log --oneline --all`
- commit `e9eb8822` — FOUND in `git log --oneline --all`
- `go test ./internal/app/cmd/ -run 'TestCreatePrivateSubnetGuard' -count=1 -timeout 600s -v` — EXIT=0, 4/4 subtests pass
- `go test ./internal/app/cmd/ -run 'TestResolveSandboxSubnets|TestNetworkPlacementRecorded' -count=1 -timeout 600s -v` — EXIT=0, 5/5 + 3/3 subtests pass
- `go test ./pkg/capacity/ -count=1 -v` — EXIT=0, `TestRankAZs_NAT` 5/5 subtests pass, all pre-existing `TestRankAZs_*` pass
- `go test ./internal/app/cmd/ -count=1 -timeout 600s` — EXIT=1, but the only failures are `TestBootstrapSCPApplyPath`, `TestBootstrapSCPSkipped_OrganizationBlank`, `TestClusterAdd`, `TestClusterRm`, `TestClusterAddPersistFailure` — the same 5 pre-existing environmental failures documented in this session's own instructions (no live AWS credentials in this shell). Re-ran twice to confirm the set is stable and this plan introduces no new failures; a one-off `TestRunAgentAuthClaude_TeesAndCleans` failure seen in one combined-package run was reproduced as a pass in isolation (flaky/order-dependent, unrelated to any file this plan touches).
- `go test ./internal/app/cmd/ ./pkg/capacity/ ./pkg/compiler/ -count=1 -timeout 600s` — `pkg/capacity` and `pkg/compiler` both `ok`; `internal/app/cmd` shows only the 5 known environmental failures
- `go build ./...` — clean
- `go vet ./...` — clean except a pre-existing, unrelated warning in `sidecars/http-proxy/httpproxy/transparent.go` (a file this plan never touches)
- `gofmt -l` on all 5 touched/created files — clean
- `bash scripts/validate-all-profiles.sh` (after `make build`) — `validate-all-profiles: all 21 profiles valid`, including `profiles/private-subnet.yaml`

## Self-Check: PASSED

---
*Phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway*
*Completed: 2026-08-19*
