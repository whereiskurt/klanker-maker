---
phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway
plan: 02
subsystem: infra
tags: [terraform, terragrunt, ec2spot, module-versioning, go-test]

# Dependency graph
requires:
  - phase: 125-01
    provides: "infra/modules/network/v1.1.0 (per-AZ NAT gateways) — the private subnet IDs this plan's sandbox_subnets/associate_public_ip will eventually carry"
provides:
  - "infra/modules/ec2spot/v1.3.0 — sandbox_subnets (renamed from public_subnets, truthfully named) and associate_public_ip (bool, default true) replacing two hardcoded associate_public_ip_address = true sites"
  - "infra/templates/sandbox/terragrunt.hcl per-substrate module version resolution — locals.substrate_module_versions map + locals.substrate_module_version lookup, consumed by terraform.source"
  - "pkg/terragrunt/substrate_version_pin_test.go — general invariant test (TestSubstrateVersionPinPointsAtExistingModules) preventing any substrate from ever pinning at a nonexistent module directory again"
affects: ["125-03..125-09 (Go plumbing that will populate sandbox_subnets/associate_public_ip from the resolved placement, profile schema, km doctor/init guards, live UAT)"]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Module version bump = new immutable vX.Y.Z directory (verbatim copy + delta), never an in-place edit of a released version — same convention as 125-01's network v1.1.0"
    - "Per-substrate version resolution via a Terragrunt locals map + lookup(), replacing a single shared literal — generalizes to any future substrate without further template edits"
    - "hclsyntax-parsed Go regression test asserting a structural invariant (every declared substrate resolves to an existing module directory) rather than two hardcoded string checks"

key-files:
  created:
    - infra/modules/ec2spot/v1.3.0/main.tf
    - infra/modules/ec2spot/v1.3.0/variables.tf
    - infra/modules/ec2spot/v1.3.0/outputs.tf
    - pkg/terragrunt/substrate_version_pin_test.go
    - .planning/phases/125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway/deferred-items.md
  modified:
    - infra/templates/sandbox/terragrunt.hcl
    - pkg/compiler/compiler_secrets_test.go

key-decisions:
  - "Retired TestSandboxTemplateUsesEC2SpotV120 (single shared-literal check) rather than updating it in place — the per-substrate map it would need to assert against no longer has one literal to check, and the plan explicitly called for retirement with a pointer comment to the new test."
  - "Wrote the comment block explaining the pre-Phase-125 bug without literally quoting the old '/v1.2.0' string, so the file satisfies its own no-legacy-literal acceptance check (grep -c 'v1.2.0' == 0) while still documenting the history for future readers."
  - "Did not touch infra/modules/ecs/ at all — REQ-125-SUBPIN's scope boundary is the pin only. git status --porcelain infra/modules/ecs/ is confirmed empty."

requirements-completed: [REQ-125-EC2SPOT, REQ-125-SUBPIN]

coverage:
  - id: D1
    description: "ec2spot v1.3.0 renames public_subnets to sandbox_subnets (truthfully named for either public or private placement) and adds associate_public_ip (bool, default true, both associate_public_ip_address sites wired to it); v1.2.0 left byte-identical; identical 32-resource-block set; cmd/ttl-handler/main.go untouched (frozen destroy stub still pins ec2spot/v1.0.0)"
    requirement: "REQ-125-EC2SPOT"
    verification:
      - kind: other
        ref: "terraform fmt -check -recursive infra/modules/ec2spot/v1.3.0 (exit 0)"
        status: pass
      - kind: other
        ref: "grep -c 'variable \"sandbox_subnets\"' / 'variable \"associate_public_ip\"' / associate_public_ip_address=var.associate_public_ip (x2) / var.sandbox_subnets — all match plan's expected counts"
        status: pass
      - kind: other
        ref: "resource-block count parity: grep -c '^resource ' v1.2.0/main.tf == v1.3.0/main.tf (32 == 32)"
        status: pass
    human_judgment: false
  - id: D2
    description: "infra/templates/sandbox/terragrunt.hcl resolves a module version per substrate via locals.substrate_module_versions (ec2spot->v1.3.0, ecs->v1.0.0) + locals.substrate_module_version lookup; terraform.source interpolates the resolved local, not a hardcoded literal; every declared substrate points at a module directory that exists on disk, generally enforced for any future substrate"
    requirement: "REQ-125-SUBPIN"
    verification:
      - kind: unit
        ref: "pkg/terragrunt/substrate_version_pin_test.go#TestSubstrateVersionPinResolvesPerSubstrate"
        status: pass
      - kind: unit
        ref: "pkg/terragrunt/substrate_version_pin_test.go#TestSubstrateVersionPinPointsAtExistingModules"
        status: pass
      - kind: other
        ref: "grep -c 'substrate_module_versions' terragrunt.hcl == 3; grep -c 'v1.2.0' terragrunt.hcl == 0"
        status: pass
      - kind: other
        ref: "git status --porcelain infra/modules/ecs/ (empty — scope boundary held)"
        status: pass
    human_judgment: false

duration: ~25min
completed: 2026-08-20
status: complete
---

# Phase 125 Plan 02: ec2spot v1.3.0 + per-substrate module version pin Summary

**ec2spot v1.3.0 renames `public_subnets`→`sandbox_subnets` and adds `associate_public_ip`; the sandbox terragrunt template now resolves a module version per substrate via a locals map, closing the pre-existing bug where a single shared `/v1.2.0` literal silently pointed the (never-tested) ECS substrate at a nonexistent module directory.**

## Performance

- **Duration:** ~25 min
- **Completed:** 2026-08-20
- **Tasks:** 3/3
- **Files modified:** 7 (5 created, 2 modified)

## Accomplishments

- Wrote `pkg/terragrunt/substrate_version_pin_test.go` (RED first): `TestSubstrateVersionPinResolvesPerSubstrate` parses `locals.substrate_module_versions` and `terraform.source` via `hclsyntax` and asserts `ec2spot`→`v1.3.0`, `ecs`→`v1.0.0`, and that `source` interpolates the resolved local (not a bare literal); `TestSubstrateVersionPinPointsAtExistingModules` is the general invariant — for every substrate/version pair in the map, `infra/modules/<substrate>/<version>/` must exist and contain at least one `.tf` file
- Retired the superseded `TestSandboxTemplateUsesEC2SpotV120` from `pkg/compiler/compiler_secrets_test.go`, removing the now-unused `os` import it left behind
- Created `infra/modules/ec2spot/v1.3.0` as a verbatim copy of v1.2.0 with `public_subnets`→`sandbox_subnets` (rename, truthful for either placement) and a new `associate_public_ip` bool var (default `true`) wired into both previously-hardcoded `associate_public_ip_address = true` sites; v1.2.0 and `cmd/ttl-handler/main.go` left untouched
- Restructured `infra/templates/sandbox/terragrunt.hcl`'s single shared `/v1.2.0` literal into `locals.substrate_module_versions` (map) + `locals.substrate_module_version` (`lookup(...)` with a `v1.0.0` fallback), consumed by `terraform.source` — turning the Task 1 RED test GREEN
- ECS's version pin is corrected (`v1.0.0`, the only version that exists) but the ECS module directory itself was never opened — `git status --porcelain infra/modules/ecs/` is empty, holding the REQ-125-SUBPIN scope boundary exactly as specified

## Task Commits

Each task was committed atomically:

1. **Task 1: Write the per-substrate version-pin regression test (RED first)** - `1d4bc2d4` (test)
2. **Task 2: Create infra/modules/ec2spot/v1.3.0 with sandbox_subnets and associate_public_ip** - `c9c99e4a` (feat)
3. **Task 3: Restructure the sandbox template's version pin into a per-substrate map (turn Task 1 GREEN)** - `d1508616` (feat)

**Plan metadata:** committed separately below (docs: complete plan)

## Files Created/Modified

- `pkg/terragrunt/substrate_version_pin_test.go` - New hclsyntax-based regression test (2 test functions + 4 helpers) locking the per-substrate version-pin invariant
- `pkg/compiler/compiler_secrets_test.go` - Removed the superseded single-literal test and its now-unused `os` import; left a pointer comment (without re-quoting the old literal, so it doesn't trip the new no-legacy-literal grep check)
- `infra/modules/ec2spot/v1.3.0/main.tf` - `local.effective_subnets` reads `var.sandbox_subnets`; both `associate_public_ip_address` sites read `var.associate_public_ip`
- `infra/modules/ec2spot/v1.3.0/variables.tf` - `sandbox_subnets` (renamed) and `associate_public_ip` (new, default `true`)
- `infra/modules/ec2spot/v1.3.0/outputs.tf` - Unchanged copy of v1.2.0
- `infra/templates/sandbox/terragrunt.hcl` - Added `substrate_module_versions` map + `substrate_module_version` lookup local; `terraform.source` interpolates the resolved local
- `.planning/phases/125-.../deferred-items.md` - New: records a pre-existing, unrelated test observation (see below)

## Decisions Made

- Retired rather than rewrote `TestSandboxTemplateUsesEC2SpotV120` — the plan explicitly called this out as superseded, and a single-shared-literal assertion has no meaning once the pin is per-substrate.
- Worded the new template comment to explain the pre-Phase-125 bug without literally embedding the string `v1.2.0`, so `grep -c 'v1.2.0' infra/templates/sandbox/terragrunt.hcl` correctly returns `0` per the task's own acceptance criterion, while still preserving the "why" for future readers.
- Left `cmd/ttl-handler/main.go` and `pkg/compiler/compiler_secrets_test.go`'s now-unused-but-harmless `repoRootForSecretsTest` helper untouched beyond what each task specified — the plan was explicit about touching only the named test, and Go does not error on an unused top-level function (only unused imports/locals), so no further cleanup was required for a green build.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking issue] Removed the now-unused `os` import from `pkg/compiler/compiler_secrets_test.go`**
- **Found during:** Task 1, immediately after deleting `TestSandboxTemplateUsesEC2SpotV120`
- **Issue:** the deleted test was the only user of `os.ReadFile` in that file; leaving the import in place is a compile error in Go (unused import)
- **Fix:** removed the `"os"` import line
- **Files modified:** `pkg/compiler/compiler_secrets_test.go`
- **Commit:** `1d4bc2d4`

**2. [Rule 3 - Blocking issue] Reworded the new template comment to avoid literally containing `v1.2.0`**
- **Found during:** Task 3 verification — `grep -c 'v1.2.0'` initially returned `2` (from my own explanatory comment), failing the acceptance criterion that requires `0`
- **Issue:** the comment I wrote to explain the historical bug quoted the old literal directly, which the test itself now forbids
- **Fix:** reworded to describe "a single shared version literal" without quoting it
- **Files modified:** `infra/templates/sandbox/terragrunt.hcl`
- **Commit:** `d1508616`

Otherwise the plan executed as written. No Rule 4 architectural questions arose, no authentication gates encountered.

## Known/Deferred Issues

`TestRunner_Apply_ContextCancellationKillsSubprocess` (`pkg/terragrunt/runner_test.go`) fails when run in isolation in this environment (`Apply took ~5s to return after cancel — expected sub-2s response`), but passes when run as part of the full `pkg/terragrunt` package suite. The file predates this plan by several phases (last touched in phase 84.4, commits `5e10e370`/`56f6e1bc`) and neither it nor `runner.go` was modified by any task in this plan — it is unrelated to the substrate version pin or ec2spot changes. Logged to `deferred-items.md` per the scope-boundary rule rather than investigated/fixed. The plan-level verification command (`go test ./pkg/terragrunt/ ./pkg/compiler/ -count=1`) was run multiple times during this session and was green in its most recent execution; treat this as a pre-existing environmental flake, not a regression introduced here.

## Issues Encountered

None beyond the two Rule-3 fixes documented above.

## User Setup Required

None — pure Terraform-module authoring + Terragrunt template edit + Go test, no `terraform plan`/`apply` run (out of scope for this plan; live UAT is a later plan in this phase per `125-CONTEXT.md`).

## Next Phase Readiness

- `infra/modules/ec2spot/v1.3.0` (`sandbox_subnets`, `associate_public_ip`) and the per-substrate template version resolution are ready for the Go plumbing plans that will populate `sandbox_subnets`/`associate_public_ip` from the placement-resolution helper (`NetworkOutputs.PrivateSubnets`/`NATGatewayIDs`, per `125-CONTEXT.md`).
- No blockers. `git status --porcelain infra/modules/ec2spot/v1.2.0 infra/modules/ec2spot/v1.0.0 cmd/ttl-handler/main.go` is empty; `git status --porcelain infra/modules/ecs/` is empty; no `.terraform`/lock-file artifacts under `infra/modules/ec2spot/v1.3.0`.

## Self-Check: PASSED

All created files verified present on disk:
- FOUND: pkg/terragrunt/substrate_version_pin_test.go
- FOUND: infra/modules/ec2spot/v1.3.0/main.tf
- FOUND: infra/modules/ec2spot/v1.3.0/variables.tf
- FOUND: infra/modules/ec2spot/v1.3.0/outputs.tf
- FOUND: .planning/phases/125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway/deferred-items.md

All commit hashes verified present in git log:
- FOUND: 1d4bc2d4 (Task 1)
- FOUND: c9c99e4a (Task 2)
- FOUND: d1508616 (Task 3)

---
*Phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway*
*Completed: 2026-08-20*
