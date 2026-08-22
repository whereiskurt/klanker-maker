---
phase: 126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin
plan: 02
subsystem: infra
tags: [terragrunt, hcl, compiler, cross-account, assume-role]

requires:
  - phase: 126-01
    provides: launch_accounts config block + Config.GetLaunchAccount(s) getters, RuntimeSpec.LaunchAccount schema field, ValidateLaunchAccountLink
provides:
  - "infra/templates/sandbox/terragrunt.hcl: deep-merge include \"root\" + conditional generate \"provider\" override that reproduces root's full generated output plus an interpolated assume_role stanza"
  - "compiler.NetworkConfig / ec2HCLParams: LaunchAccount, LauncherRoleARN, LauncherExternalID fields, wired into service.hcl only when LaunchAccount is set"
  - "pkg/terragrunt: always-on structural regression test + terragrunt-binary-gated render test proving both the dormant byte-identity and the active assume_role render"
affects: [126-03, 126-04, 126-05, 126-06, 126-07, 126-08, 126-09, 126-10]

tech-stack:
  added: []
  patterns:
    - "Conditional (ternary) HCL local + plain ${...} interpolation, instead of an inline %{ if }/%{ endif } control sequence, for content that must be optionally injected into a <<- heredoc without disturbing the heredoc's automatic dedent-margin calculation"
    - "Deep-merge include (merge_strategy = \"deep\") required whenever a child terragrunt unit redeclares a same-labeled generate block also declared by an included parent"

key-files:
  created:
    - pkg/compiler/service_hcl_launch_account_test.go
    - pkg/terragrunt/provider_override_test.go
    - pkg/terragrunt/provider_override_render_test.go
  modified:
    - infra/templates/sandbox/terragrunt.hcl
    - pkg/compiler/service_hcl.go

key-decisions:
  - "Replaced the plan's originally-specified %{ if local.launch_account != \"\" ~} ... %{ endif ~} heredoc control sequence with a conditional (ternary) local (local.assume_role_block) interpolated via ${...} — the %{if} shape, verified by direct render against terragrunt v0.99.1, does not achieve the dormant byte-identity the plan's must_haves.truths requires (it either leaves stray trailing whitespace on the blank line above default_tags, or disables heredoc dedent entirely, depending on trim-marker placement)."
  - "Region expression in the generated provider.tf reads local.site_vars.locals.region.full (root's own expression), not local.svc_config.locals.region_full, so the dormant render is provably identical to root's own output."

requirements-completed: [REQ-126-LAUNCH]

coverage:
  - id: D1
    description: "Sandbox template gains merge_strategy = \"deep\" on include \"root\" and a conditional generate \"provider\" override that reproduces root's full generated contents plus an assume_role branch"
    requirement: "REQ-126-LAUNCH"
    verification:
      - kind: unit
        ref: "pkg/terragrunt/provider_override_test.go#TestSandboxTemplateProviderOverride_MergeStrategyDeep"
        status: pass
      - kind: unit
        ref: "pkg/terragrunt/provider_override_test.go#TestSandboxTemplateProviderOverride_ExactlyOneGenerateProvider"
        status: pass
      - kind: unit
        ref: "pkg/terragrunt/provider_override_test.go#TestSandboxTemplateProviderOverride_VersionPinsPreserved"
        status: pass
      - kind: unit
        ref: "pkg/terragrunt/provider_override_test.go#TestSandboxTemplateProviderOverride_LaunchAccountGuard"
        status: pass
      - kind: integration
        ref: "pkg/terragrunt/provider_override_render_test.go#TestSandboxProviderRender (ran against real terragrunt v0.99.1, did not skip)"
        status: pass
    human_judgment: false
  - id: D2
    description: "Compiler emits launch_account / launcher_role_arn / launcher_external_id into service.hcl only when spec.runtime.launchAccount is set; dormant compile is byte-identical to Phase 125"
    requirement: "REQ-126-LAUNCH"
    verification:
      - kind: unit
        ref: "pkg/compiler/service_hcl_launch_account_test.go#TestEC2ServiceHCL_LaunchAccountAbsent"
        status: pass
      - kind: unit
        ref: "pkg/compiler/service_hcl_launch_account_test.go#TestEC2ServiceHCL_LaunchAccountSet"
        status: pass
      - kind: unit
        ref: "pkg/compiler/service_hcl_launch_account_test.go#TestEC2ServiceHCL_LaunchAccountSurvivesSpecialChars"
        status: pass
      - kind: unit
        ref: "pkg/compiler/service_hcl_launch_account_test.go#TestEC2ServiceHCL_LaunchAccountDormantByteIdentical"
        status: pass
    human_judgment: false

duration: 25min
completed: 2026-08-22
status: complete
---

# Phase 126 Plan 02: Cross-account provider override mechanism Summary

**Sandbox terragrunt template deep-merges root and conditionally emits an assume_role provider block; compiler writes the three launch-account locals only when set; both layers proven byte-identical in the dormant case against the real pinned terragrunt binary.**

## Performance

- **Duration:** ~25 min (git log span from prior plan's close to this plan's last commit; excludes the manual render-fixture iteration used to diagnose and fix the heredoc dedent bug, which was not itself committed)
- **Started:** 2026-08-22T23:02Z (approx, first commit after 126-01 close)
- **Completed:** 2026-08-22T23:20Z
- **Tasks:** 3/3 completed (plus one Rule-1 fix commit discovered mid-verification)
- **Files modified:** 5 (2 modified, 3 created)

## Accomplishments

- `infra/templates/sandbox/terragrunt.hcl` now deep-merges `include "root"` and declares its own `generate "provider"` block that reproduces root's entire generated output (terraform{} required_version + both required_providers pins, provider "aws" region + default_tags) plus a conditionally-interpolated `assume_role` stanza — proven, against the real pinned terragrunt v0.99.1, to render byte-identically to root's own output when no launch account is set.
- `pkg/compiler` gained `NetworkConfig.LaunchAccount` / `LauncherRoleARN` / `LauncherExternalID` and the matching `ec2HCLParams` fields, wired into `service.hcl`'s locals only inside a `{{- if .LaunchAccount }}` template guard — a profile with no launch account compiles to byte-identical service.hcl.
- Two-layer regression guard in `pkg/terragrunt`: an always-on structural test (`hclsyntax` parsing, no external binary) and a terragrunt-binary-gated end-to-end render test that actually shells out to `terragrunt render --json` and diffs the resolved `generate.provider.contents`.

## Task Commits

Each task was committed atomically:

1. **Task 1: sandbox template — deep-merge include + conditional generate "provider"** - `50a3936f` (feat)
2. **Rule-1 fix: replace heredoc %{if} guard with interpolated local** - `edd7b6a4` (fix) — discovered while building Task 3's render test; see Deviations below
3. **Task 2: compiler emits launch_account locals into service.hcl, only when set** - `391a2cc4` (feat)
4. **Task 3: two-layer regression guard for the provider override** - `04e7d2dd` (test)

_Note: the fix commit (`edd7b6a4`) landed between Tasks 1/2 and Task 3's commit in git history because the bug was discovered while manually validating Task 3's render fixtures against the real terragrunt binary, after Task 2 had already been committed. It amends Task 1's file, not Task 2's._

**Plan metadata:** commit pending (this SUMMARY + STATE.md + ROADMAP.md)

## Files Created/Modified

- `infra/templates/sandbox/terragrunt.hcl` - deep-merge include, `launch_account` + `assume_role_block` locals, full provider-generation override
- `pkg/compiler/service_hcl.go` - `NetworkConfig`/`ec2HCLParams` launch-account fields, template guard emitting the three service.hcl locals
- `pkg/compiler/service_hcl_launch_account_test.go` - dormancy, value-carry, special-character, and dormant-byte-identity tests
- `pkg/terragrunt/provider_override_test.go` - always-on structural regression test (merge_strategy, single generate block, version-pin preservation, launch_account guard shape)
- `pkg/terragrunt/provider_override_render_test.go` - terragrunt-binary-gated end-to-end render test (root-only / no-account / with-account fixtures, real `terragrunt render --json`)

## Decisions Made

- Deviated from the plan's literal `%{ if }/%{ endif }` heredoc control-sequence design (see Deviations below) in favor of a conditional local + plain interpolation, because the literal design does not achieve the dormant byte-identity the plan's own `must_haves.truths` requires — verified empirically, not assumed.
- Task 3's structural test (`TestSandboxTemplateProviderOverride_LaunchAccountGuard`) was written against the shipped shape (guard lives in a locals-block ternary, not inline in the heredoc) rather than the plan's originally-described inline-heredoc shape, since the shipped shape is what actually ships and is what needs regression protection.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `%{ if }/%{ endif }` heredoc guard does not achieve dormant byte-identity**
- **Found during:** Task 3, while manually building and rendering the fixtures that would become the render test, before writing the test code
- **Issue:** The plan's Task 1 action text and 126-RESEARCH.md/126-PATTERNS.md's worked examples specify an inline `%{ if local.launch_account != "" ~} ... %{ endif ~}` control sequence directly inside the `generate "provider"` block's `<<-` heredoc. Building that exact shape and rendering it for real against the pinned terragrunt v0.99.1 showed the dormant (no-launch-account) output was NOT byte-identical to root's own generated output — it left a stray 2-space-wide blank line (trailing whitespace) between the region line and `default_tags`. Adding a left-trim marker (`%{~ endif ~}`) to try to clean that up instead disabled the heredoc's automatic dedent entirely, reproducing the ORIGINAL (undedented) 4-space indentation for the whole block. Neither variant satisfied `must_haves.truths`: *"With no launch_account local in service.hcl, the generated provider.tf is byte-identical to what root.hcl alone would have generated."*
- **Fix:** Replaced the inline `%{if}/%{endif}` heredoc control sequence with a conditional (ternary) HCL local, `local.assume_role_block`, computed in the template's `locals` block as `local.launch_account != "" ? "\n  assume_role {\n    role_arn    = \"...\"\n    external_id = \"...\"\n  }" : ""`, and referenced inside the heredoc via a plain `${local.assume_role_block}` interpolation immediately after the `region` line. Plain interpolation does not participate in the heredoc's static dedent-margin calculation (that calculation runs on the literal template text, not on interpolated values), so it sidesteps the bug entirely while still producing a cleanly-indented `assume_role` block in the active case.
- **Files modified:** `infra/templates/sandbox/terragrunt.hcl`
- **Verification:** Built temporary fixture directories under the gitignored `infra/live/use1/sandboxes/` path and ran `terragrunt render --json --non-interactive` against the real pinned terragrunt v0.99.1 binary by hand before writing any test code, confirming: (a) dormant render's `generate.provider.contents` is byte-identical to a root-only unit's own output; (b) active render's contents contain a correctly-indented `assume_role` block with the synthetic `role_arn`/`external_id` values and both provider version pins intact. Task 3's `TestSandboxProviderRender` automates exactly this comparison and passed on the first run after the fix. All scratch fixture directories were deleted before finishing (they live under a path already covered by `.gitignore`'s `infra/live/*/sandboxes/*/` rule, so `git status` was never dirtied by them).
- **Committed in:** `edd7b6a4` (separate fix commit, not folded into a task commit, since the bug spanned Task 1's already-committed work)

**2. [Rule 1 - consequence of Fix 1] `grep -c 'assume_role'` on the template is 2, not 1**
- **Found during:** Re-checking Task 1's literal acceptance-criteria greps after applying Fix 1
- **Issue:** Task 1's acceptance criteria state `grep -c 'assume_role' infra/templates/sandbox/terragrunt.hcl` should be `1`. With the ternary-local design, "assume_role" necessarily appears on two lines: the local's definition (`assume_role_block = ... "assume_role {" ...`) and its reference site (`${local.assume_role_block}`) inside the heredoc — an inherent, unavoidable consequence of moving the guard out of the heredoc.
- **Fix:** None applied — this is accepted as a byproduct of Fix 1. The comment prose was trimmed to avoid using the literal token "assume_role" a third time (so the count is 2, not 3), but 2 is the practical floor for this design. The higher-priority `must_haves.truths` requirement (dormant byte-identity, which the original 1-occurrence design could not actually satisfy) takes precedence over the literal grep-count acceptance bullet.
- **Files modified:** none beyond Fix 1
- **Verification:** `grep -c 'merge_strategy'` = 1, `grep -c 'generate "provider"'` = 1, `grep -c 'launcher_external_id'` = 1 all still hold exactly as specified; only the `assume_role` count deviates, and only because the original literal target was unreachable without reintroducing the byte-identity bug.
- **Committed in:** `edd7b6a4`

---

**Total deviations:** 2 (both Rule 1, both stemming from the same root cause)
**Impact on plan:** Necessary for correctness — the plan's own primary truth requirement (dormant byte-identity) could not be satisfied by the literally-specified design. No scope creep; no other plan surface touched.

## Issues Encountered

None beyond the deviation documented above.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- The provider-override mechanism and the compiler's service.hcl wiring are both landed and regression-tested. Downstream plans in this phase (account link management, capacity-check assume-role, teardown/TTL cross-account handling, doctor checks) can now rely on `spec.runtime.launchAccount` flowing through to a working generated `provider.tf` with zero additional terragrunt-template work.
- No blockers. The `pkg/terragrunt` test suite ran the real terragrunt v0.99.1 binary successfully in this environment; a CI environment without terragrunt on PATH will skip `TestSandboxProviderRender` with a message naming the pinned version, while the always-on structural test still runs.

---
*Phase: 126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin*
*Completed: 2026-08-22*

## Self-Check: PASSED

- All 6 claimed files found on disk: `infra/templates/sandbox/terragrunt.hcl`, `pkg/compiler/service_hcl.go`, `pkg/compiler/service_hcl_launch_account_test.go`, `pkg/terragrunt/provider_override_test.go`, `pkg/terragrunt/provider_override_render_test.go`, this SUMMARY.md.
- All 4 claimed commit hashes found in `git log --oneline --all`: `50a3936f`, `edd7b6a4`, `391a2cc4`, `04e7d2dd`.
- `go test ./pkg/terragrunt/... -count=1` exits 0.
- `go test ./pkg/compiler/... -timeout 600s -count=1` exits 0.
- `go build ./...` exits 0.
