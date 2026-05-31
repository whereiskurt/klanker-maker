---
phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating
plan: 01
subsystem: profile-spec
tags: [iam-rename, dead-field-removal, schema-drift-fix, apiversion-bump, structural-cleanup, byte-identity]

# Dependency graph
requires:
  - phase: 92-00
    provides: pre-Phase-92 byte-identity baselines (userdata + IAM HCL) that prove the rename emits identical compiled output
provides:
  - spec.identity → spec.iam rename across types/schema/validators/compiler/inherit/generator + 30 test-consumed YAMLs + 11 operator profiles
  - dead spec.agent: block (MaxConcurrentTasks/TaskTimeout/AllowedTools) removed from types/schema/inherit/generator/all YAMLs
  - iam.allowedSecretPaths declared in JSON schema (closes Phase 89 drift)
  - apiVersion bumped v1alpha1 → v1alpha2 (STRICT; v1alpha1 rejected)
  - scripts/validate-all-profiles.sh — 20-file inventory gate
  - pkg/profile/validate_v1alpha2_test.go — version + rename rejection unit assertions
affects: [92-02-notification-types, 92-04-agent-types, 92-05-synthesizers]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Indentation-aware YAML transformer that deletes a top-level 2-space agent: block while preserving the 4-space cli.agent: key, and preserves Go-string / markdown backticks appended to deleted lines"
    - "Stash-baseline diff to classify a failing test as pre-existing vs introduced before treating it as out-of-scope"

key-files:
  created:
    - scripts/validate-all-profiles.sh
    - pkg/profile/validate_v1alpha2_test.go
  modified:
    - pkg/profile/types.go
    - pkg/profile/schema.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/inherit.go
    - pkg/compiler/security.go
    - pkg/compiler/service_hcl.go
    - pkg/allowlistgen/generator.go
    - pkg/compiler/security_phase92_byte_identity_test.go
    - "30 test-consumed YAMLs (builtins, compiler testdata, repo-root testdata, learn.v2) + 11 operator profiles"
    - "8 docs (profile-reference, user-manual, multi-agent-email, budget-guide, security-model, sandbox-secrets, CLAUDE.md, OPERATOR-GUIDE.md)"

key-decisions:
  - "Folded the operator-requested apiVersion bump (v1alpha1→v1alpha2, STRICT) into the same edits as the IAM rename so validation never sees a version/schema mismatch mid-task"
  - "Committed test-consumed YAML (builtins/testdata/learn.v2) in the Task-1 commit (not Task-2) because go-test byte-identity goldens Parse them — splitting would leave a red commit"
  - "Kept allowedRegions minItems:1 (all builtins declare ≥1 region; no relaxation needed)"
  - "Updated the Wave-0 byte-identity test's one p.Spec.Identity reference to p.Spec.IAM — that IS the rename Wave 1 performs; the golden output stayed byte-identical"
  - "Added a real legacy-rejection unit test (validate_v1alpha2_test.go) per the added_scope step-3 instruction, since no validate_legacy_keys-style test pre-existed"

patterns-established:
  - "Per-wave YAML migration via a reusable indentation-aware transformer rather than fragile line regex"

requirements-completed: []

# Metrics
duration: 30min
completed: 2026-05-31
---

# Phase 92 Plan 01: Structural Cleanup — IAM Rename + Dead-Field Removal Summary

**Renamed `spec.identity:` → `spec.iam:`, deleted the dead `spec.agent:`/`sessionPolicy` fields, closed the Phase 89 `allowedSecretPaths` schema drift, and bumped the profile apiVersion to a STRICT `v1alpha2` — all purely lexical/structural, proven by the Wave-0 byte-identity goldens staying GREEN.**

## Performance
- **Duration:** ~30 min
- **Started:** 2026-05-31T21:03:51Z
- **Completed:** 2026-05-31T21:34:17Z
- **Tasks:** 3
- **Files changed:** 78 across 3 commits

## Accomplishments

### IAM rename — 5 compiler sites (RESEARCH.md §2c confirmed)
- `pkg/compiler/security.go` (3 sites): `p.Spec.Identity.RoleSessionDuration` (line 50), `.AllowedRegions` (line 56), `.AllowedSecretPaths` (line 74) → `p.Spec.IAM.*`
- `pkg/compiler/service_hcl.go` (2 sites): `len(p.Spec.Identity.AllowedSecretPaths)` (line 1032) + `strings.Join(p.Spec.Identity.AllowedSecretPaths, ",")` (line 1033) → `p.Spec.IAM.*`
- Type layer: `IdentitySpec` → `IAMSpec` (SessionPolicy field deleted); `Spec.Identity IdentitySpec` → `Spec.IAM IAMSpec` (yaml `iam`); `inherit.go` merge → `&result.Spec.IAM` + dead Spec.Agent merge deleted.

### Dead AgentSpec removal
- `pkg/profile/types.go`: entire `AgentSpec` struct + `Spec.Agent` field deleted (Wave 4 re-introduces a new shape as a pointer).
- `pkg/allowlistgen/generator.go`: dead `AgentSpec{MaxConcurrentTasks: 1, TaskTimeout: "30m"}` construction removed (RESEARCH.md §2j honored); generator's `Identity:` → `IAM:` (SessionPolicy dropped); emitted apiVersion → v1alpha2.
- Schema: `agent` definition block deleted; `agent` removed from `spec.required`.

### Schema-drift fix
- `iam.allowedSecretPaths` (Phase 89, read by `compileSecrets`/`service_hcl` but previously absent from the schema) is now declared under the `iam` block as an array of strings. Drift closed.

### apiVersion bump v1alpha1 → v1alpha2 (operator added_scope; STRICT)
- `schemas/sandbox_profile.schema.json`: `$id` → `.../sandbox-profile/v1alpha2`; apiVersion `pattern` `^.+/v1alpha2$`; description bumped.
- `schema.go`: compile `id` string → `.../sandbox-profile/v1alpha2` (kept byte-identical to schema.json `$id`).
- All ~30 test-consumed YAMLs + 11 operator profiles + configui editor template + every doc example now declare `apiVersion: klankermaker.ai/v1alpha2`.
- Verified rejections: legacy `v1alpha1` → `apiVersion ... does not match pattern '^.+/v1alpha2$'`; legacy `identity:` → `additional properties 'identity' not allowed` + `missing property 'iam'`; dead `agent:` → `additional properties 'agent' not allowed`.

### Validation gate + tests
- `scripts/validate-all-profiles.sh` (executable, 67 lines): iterates the 20-file Profile Inventory, runs `km validate` per file, exits non-zero on any failure. **All 20 pass.** Local-only — no CI workflow exists (RESEARCH.md §3d), so the "wire into CI" sub-task was correctly skipped.
- `pkg/profile/validate_v1alpha2_test.go`: 4 assertions — v1alpha2 accepted; v1alpha1 / `identity:` / dead `agent:` all rejected.

## Full path list — 30 YAMLs migrated (identity→iam, sessionPolicy dropped, dead agent removed, v1alpha2)

**Operator profiles (12):** profiles/{ao, codex, dc34, dc34.ami, example-additional-snapshots, goose, learn.v2, learn.v2.chatty, learn.v2.codex, learn.v2.polite, locked, locked.ami}.yaml
**Builtins (8):** pkg/profile/builtins/{ao, codex, goose, hardened, learn, open-dev, restricted-dev, sealed}.yaml
**Compiler testdata (10):** pkg/compiler/testdata/{ec2-basic, ec2-empty-repos, ec2-with-allowed-refs, ec2-with-secrets, ec2-with-budget, ecs-basic, ecs-empty-repos, ecs-with-github, docker-basic, docker-with-budget}.yaml
**Plus (Rule 3 — blocking) repo-root fixtures (11):** testdata/profiles/{child-of-open-dev, circular-a, circular-b, depth-4, invalid-bad-substrate, invalid-missing-spec, invalid-unknown-field, test-ecs, test-secrets, valid-docker-substrate, valid-minimal}.yaml + inline-Go-YAML in 8 `*_test.go` files + cmd/configui/templates/editor.html.

## Byte-identity confirmation
`TestIAMHCLPhase92ByteIdentity` and `TestUserdataLearnV2Phase92ByteIdentity` (Wave 0 VC-4 / VC-3) **stayed GREEN** through this wave — apiVersion is metadata not rendered into IAM HCL or userdata, and the identity→iam rename / sessionPolicy removal did not alter serialized output. The only test-side change required was updating one `p.Spec.Identity` reference in the Wave-0 IAM golden test to `p.Spec.IAM` (the exact rename under test).

## Wave handoff note
Wave 2/3 own the `spec.cli.notify*` notification block; **Wave 4 will re-introduce a new `agent:` block** with structured tool-gating semantics. Wave 1's deletion intentionally leaves the slot empty ("drop the old, leave the slot empty"). The build-tagged RED stubs `phase92_wave2/4/5` remain RED by design (confirmed: NotificationSpec / AgentSpec / synthesizeClaudeSettings undefined under their tags); default `go test` stays GREEN.

## Task Commits
1. **Task 1 — IAM rename + dead AgentSpec + schema drift + apiVersion (code+schema+test-consumed YAML):** `f03b8cee` (feat)
2. **Task 2 — operator profile YAMLs + validate-all-profiles.sh + v1alpha2 gate tests:** `7924fc43` (feat)
3. **Task 3 — doc sweep (identity→iam, sessionPolicy/agent removal, v1alpha2):** `c4b7ae33` (docs)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 — Blocking] Migrated test fixtures the plan did not list**
- **Found during:** Task 1 verification (`go test`).
- **Issue:** ~8 `*_test.go` files (types_test, types_efs_test, validate_test, parse_test, secrets_test, email_test, schema_storage_test, handlers_editor_test) embed inline `identity:`/`sessionPolicy:`/`agent:` YAML; 11 repo-root `testdata/profiles/*.yaml` and `cmd/configui/templates/editor.html` embed full profiles. After the schema rename these would fail to validate/compile, blocking `go test`.
- **Fix:** Applied the same indentation-aware transform + v1alpha2 bump; updated `types_test.go` (`Spec.Identity`→`Spec.IAM`, deleted the dead `Spec.Agent.MaxConcurrentTasks` assertion), `service_hcl_test.go` (`IdentitySpec{}`→`IAMSpec{}`), and the Wave-0 IAM golden test's one `Spec.Identity` ref.
- **Commit:** f03b8cee (test fixtures + Go test refs), 7924fc43 (editor template).

**2. [Rule 3 — Blocking] Doc coverage beyond the plan's 3 named files**
- **Found during:** Task 3.
- **Issue:** The plan's 3 named docs (sandbox-secrets, CLAUDE.md, OPERATOR-GUIDE.md) actually had no live `identity`/`sessionPolicy`/`agent` field usages, but `docs/profile-reference.md` (canonical schema reference), `user-manual.md`, `multi-agent-email.md`, `budget-guide.md`, `security-model.md` documented the renamed/removed fields — leaving them stale would mis-document a now-rejected schema.
- **Fix:** Migrated those docs too (identity→iam section rewrite, sessionPolicy section deleted, dead-agent section marked removed, apiVersion→v1alpha2). Broader agent-section rewrite for the NEW Wave-4 shape logged to deferred-items.md.
- **Commit:** c4b7ae33.

### Plan grep caveat
Task 3's strict negated grep (`! grep ... spec.identity|sessionPolicy|maxConcurrentTasks` over the 3 named files) now matches the deliberate Phase-92 explanatory notes I added (which *describe* the rename). The verification *intent* — no live field usage — is satisfied; the matches are documentation of the change, not stale usages.

**Total deviations:** 2 auto-fixes (both Rule 3 — directly caused by this task's schema rename). No Rule 4 (architectural) triggers.
**Impact on plan:** None to scope. All plan must-haves met; additional test/doc files were unavoidable consequences of the rename.

## Issues Encountered / Out of scope
- 4 packages have **pre-existing** test failures (configui, km-slack, ttl-handler, internal/app/cmd) — confirmed via git-stash baseline that they fail on clean HEAD and relate to env/state-bucket/docker/AWS mocks, NOT identity/agent/apiVersion. Logged to `deferred-items.md`. The packages 92-01 owns (pkg/profile, pkg/compiler, pkg/allowlistgen) all pass.
- No AWS/terragrunt invoked — verification was local (`go build`, `go test`, `make build`, `km validate`, `scripts/validate-all-profiles.sh`).

## Self-Check: PASSED

All created files exist on disk (scripts/validate-all-profiles.sh, pkg/profile/validate_v1alpha2_test.go, 92-01-SUMMARY.md, deferred-items.md). All 3 task commits (f03b8cee, 7924fc43, c4b7ae33) present in git history. `go build ./...` clean; `go test ./pkg/profile/... ./pkg/compiler/... ./pkg/allowlistgen/...` GREEN; byte-identity goldens GREEN; `scripts/validate-all-profiles.sh` exits 0 (all 20); RED stubs (wave2/4/5) still fail-compile by design.
