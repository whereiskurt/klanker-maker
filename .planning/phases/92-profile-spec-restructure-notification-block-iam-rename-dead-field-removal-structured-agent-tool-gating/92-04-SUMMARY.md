---
phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating
plan: 04
subsystem: profile-schema-inheritance-validation
tags: [agent-block, tool-gating, json-schema, typed-inheritance, mixed-mode-validator, cli-migration, byte-identity]

# Dependency graph
requires:
  - phase: 92-00
    provides: pre-Phase-92 byte-identity baselines + phase92_wave4 RED stub (validate_mixed_settings_test.go)
  - phase: 92-01
    provides: removal of the dead top-level Spec.Agent block (re-introduced here with new shape)
  - phase: 92-03
    provides: CLISpec reduced to NoBedrock/Agent/ClaudeArgs/CodexArgs; notification block migration; VC-3/VC-11 baselines
provides:
  - "spec.agent block: AgentSpec{Default, Claude, Codex} + AgentClaudeSpec + AgentCodexSpec + AgentToolsSpec (types + schema at v1alpha2)"
  - "CLISpec reduced to NoBedrock only — last 3 agent fields (Agent/ClaudeArgs/CodexArgs) deleted"
  - "typed mergeAgentSpec + 4 sub-mergers (second pointer-typed Spec section to get a field-level merger after Notification)"
  - "mixed-mode validator (VC-6): agent.claude.tools.autoApprove/deny + inlined .claude/settings.json -> hard ValidationError"
  - "phase92_wave4 build tag removed; VC-6 now a green test in the default suite"
  - "all Spec.CLI.Agent/ClaudeArgs/CodexArgs reads migrated to Spec.Agent.* across cmd + compiler (zero remaining)"
  - "10 profile YAMLs (9 profiles/ + 1 builtin) migrated cli.agent/claudeArgs/codexArgs -> spec.agent.*"
affects: [92-05-agent-synthesizers, 92-06-operator-uat]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Typed pointer-section inheritance merger (mergeAgentSpec) mirroring Wave 2's mergeNotificationSpec — fixes the same pointer-replace bug for a second Spec section"
    - "map[string]any passthrough merge (mergePermissionsPassthrough): shallow top-level key-union, child-wins — the one untyped exception per CONTEXT.md locked decision"
    - "nil-safe agent accessors agentDefault(compiler) / profileAgentDefault + loadProfileAgent (cmd) parallel to the Wave 3 notification accessors"

key-files:
  created:
    - pkg/profile/inherit_agent_test.go
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/inherit.go
    - pkg/profile/validate.go
    - pkg/profile/validate_mixed_settings_test.go
    - pkg/profile/types_test.go
    - pkg/profile/builtins/learn.yaml
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_codex_test.go
    - pkg/compiler/userdata_test.go
    - pkg/compiler/userdata_slack_inbound_test.go
    - internal/app/cmd/agent.go
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_codex.go
    - internal/app/cmd/doctor_codex_test.go
    - profiles/codex.yaml
    - profiles/dc34.yaml
    - profiles/dc34.ami.yaml
    - profiles/learn.v2.yaml
    - profiles/learn.v2.chatty.yaml
    - profiles/learn.v2.codex.yaml
    - profiles/learn.v2.polite.yaml
    - profiles/locked.yaml
    - profiles/locked.ami.yaml

key-decisions:
  - "agent.claude.permissions is map[string]any with schema additionalProperties:true — the ONLY passthrough exception per the CONTEXT.md locked decision; everything else typed aggressively."
  - "KM_AGENT emission stays gated on Spec.CLI != nil but now sources its value from agentDefault(p) (spec.agent.default). Env var name + value semantics UNCHANGED — VC-3 byte-identity holds because learn.v2 keeps both a cli: (noBedrock) and an agent: block."
  - "mergeAgentToolsSpec is value-typed (child non-empty slices replace parent's); mergePermissionsPassthrough is a shallow key-union with child-wins on collision."
  - "loadProfileCLIClaudeArgs/loadProfileCLICodexArgs keep their names for caller stability but now read spec.agent.{claude,codex}.args via the new loadProfileAgent helper."

patterns-established:
  - "Per-message Slack prefix routing + km agent --claude/--codex flag derivation are unchanged: agentType is computed from CLI flags, never from the profile default read — so no per-invoke routing site needed migration beyond the args accessors."

requirements-completed: []

# Metrics
duration: 20min
completed: 2026-05-31
---

# Phase 92 Plan 04: Agent Types + Schema + Inherit + Mixed-Mode Validator Summary

**Landed the structured `spec.agent:` block (AgentSpec/Claude/Codex/ToolsSpec) as types + JSON schema + typed field-level `mergeAgentSpec` inheritance + a mixed-mode validator that hard-errors on `agent.claude.tools.autoApprove` coexisting with an inlined `.claude/settings.json`; deleted the last 3 CLISpec fields (Agent/ClaudeArgs/CodexArgs) so CLISpec is now NoBedrock-only, migrated every `Spec.CLI.Agent` read across cmd + compiler to `Spec.Agent.*`, and kept VC-3 byte-identity GREEN.**

## Performance
- **Duration:** ~20 min
- **Started:** 2026-05-31T22:24:02Z
- **Completed:** 2026-05-31T22:44:24Z
- **Tasks:** 4
- **Files modified:** 24 (1 created)

## Accomplishments
- **4 new types** in `pkg/profile/types.go`: `AgentSpec` (line 548), `AgentClaudeSpec` (566), `AgentCodexSpec` (586), `AgentToolsSpec` (598); `Spec.Agent *AgentSpec` field (line 63, optional, NOT in spec.required). CLISpec reduced to `NoBedrock` only.
- **Schema** `agent` block at `properties.spec.properties.agent` (line 520); `cli.agent`/`claudeArgs`/`codexArgs` removed; `agent.claude.permissions` is `additionalProperties:true`.
- **Typed `mergeAgentSpec`** (inherit.go:246) + `mergeAgentClaudeSpec` (260) + `mergeAgentCodexSpec` (282) + `mergeAgentToolsSpec` (300) + `mergePermissionsPassthrough` (314); wired into `merge()` at line 119 — the second pointer-typed Spec section (after Notification) to get a field-level merger, fixing the same pointer-replace bug.
- **Mixed-mode validator** `validateAgentClaudeNoMixedMode` (validate.go:483), wired into `ValidateSemantic` at line 454; the Wave 0 `phase92_wave4` build tag is removed so VC-6 is a real green test.
- **All `Spec.CLI.Agent`/`ClaudeArgs`/`CodexArgs` reads migrated**: userdata.go:3944 + 4133 (`agentDefault(p)`), agent.go loadProfileCLIClaudeArgs/CodexArgs via `loadProfileAgent` (1447) + `profileAgentDefault` (1478), doctor.go:3402 + doctor_codex.go:261. Final grep audit: **zero** real reads remain.

## Task Commits

Each task committed atomically (TDD where applicable):

1. **Task 1: AgentSpec types + schema; delete 3 CLISpec fields** — `86f09c3f` (feat)
2. **Task 2: typed mergeAgentSpec + sub-mergers** — `48641eaa` (feat)
3. **Task 3: mixed-mode validator + remove build tag + fixture migration** — `bf47243d` (feat)
4. **Task 4: migrate cmd + compiler CLI.Agent/Args reads** — `f0b9fc37` (feat)

**Plan metadata:** _(see final docs commit)_

## Files Created/Modified
- `pkg/profile/types.go` — 4 new agent types; Spec.Agent field; CLISpec → NoBedrock only
- `pkg/profile/schemas/sandbox_profile.schema.json` — optional agent block; cli.agent/claudeArgs/codexArgs removed; permissions additionalProperties:true
- `pkg/profile/inherit.go` — mergeAgentSpec + 4 sub-mergers; wired into merge()
- `pkg/profile/validate.go` — validateAgentClaudeNoMixedMode + ValidateSemantic wiring
- `pkg/profile/validate_mixed_settings_test.go` — removed phase92_wave4 build tag (VC-6 in default suite)
- `pkg/profile/inherit_agent_test.go` — NEW: 5 inheritance cases (default-only, empty-args, permissions key-merge, collision, nil-edges)
- `pkg/profile/types_test.go` — migrated cli args/enum tests to spec.agent.*; new minimalAgentDefaultProfileYAML helper
- `pkg/compiler/userdata.go` — agentDefault helper; buildL7ProxyHosts + KM_AGENT read spec.agent.default
- `internal/app/cmd/agent.go` — loadProfileAgent + profileAgentDefault; args accessors read spec.agent.{claude,codex}.args
- `internal/app/cmd/doctor.go`, `doctor_codex.go` — codex detection via profileAgentDefault
- 10 profile YAMLs (9 profiles/ + builtins/learn.yaml) — cli.agent/claudeArgs/codexArgs → spec.agent.*
- 4 compiler/cmd test files — migrated off CLISpec.Agent struct literals

## Decisions Made
- `agent.claude.permissions` is the sole untyped passthrough (`map[string]any` / `additionalProperties:true`) per the CONTEXT.md locked decision.
- KM_AGENT keeps its `Spec.CLI != nil` emission gate but sources its value from `agentDefault(p)`; byte-identity holds because learn.v2 carries both a `cli:` and an `agent:` block.
- The args accessor functions retained their `loadProfileCLI*` names for caller stability while their read path moved to the agent block.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Migrated profile YAML `cli.agent/claudeArgs/codexArgs` keys to `spec.agent.*` (10 files)**
- **Found during:** Task 3 (`go test ./pkg/profile/` — TestBuiltinProfilesValidate/learn failed: "additional properties 'claudeArgs' not allowed").
- **Issue:** Removing the schema's `cli.agent/claudeArgs/codexArgs` properties (Task 1) made every fixture carrying those keys schema-invalid, blocking the profile suite and validate-all-profiles.sh.
- **Fix:** Mechanically moved each key — `cli.agent`→`spec.agent.default`, `cli.claudeArgs`→`spec.agent.claude.args`, `cli.codexArgs`→`spec.agent.codex.args` — across 9 `profiles/` YAMLs + `pkg/profile/builtins/learn.yaml`. Did NOT populate `agent.claude.tools.*` and did NOT strip the inlined `configFiles[".claude/settings.json"]` (Wave 5 owns that), so the mixed-mode validator stays passive on all current fixtures.
- **Files modified:** profiles/{codex,dc34,dc34.ami,learn.v2,learn.v2.chatty,learn.v2.codex,learn.v2.polite,locked,locked.ami}.yaml, pkg/profile/builtins/learn.yaml
- **Verification:** `bash scripts/validate-all-profiles.sh` — all 20 valid (VC-11); `go test ./pkg/profile/` GREEN.
- **Committed in:** `bf47243d` (Task 3).

**2. [Rule 3 - Blocking] Migrated more test files than the plan enumerated**
- **Found during:** Tasks 1, 3, 4 (`go vet` / `go test` compile failures on removed CLISpec fields).
- **Issue:** Deleting CLISpec.Agent/ClaudeArgs/CodexArgs broke compile in `pkg/profile/types_test.go`, 3 `pkg/compiler/*_test.go` files, and `internal/app/cmd/doctor_codex_test.go` (struct literals + YAML fixtures).
- **Fix:** Migrated each to `spec.agent.*`; added `minimalAgentDefaultProfileYAML` test helper; kept a present-but-empty `cli:` block alongside the agent block where KM_AGENT emission still gates on `Spec.CLI != nil`.
- **Files modified:** types_test.go, userdata_codex_test.go, userdata_test.go, userdata_slack_inbound_test.go, doctor_codex_test.go
- **Verification:** `go test ./pkg/profile/ ./pkg/compiler/` GREEN; agent/codex/doctor cmd tests GREEN.
- **Committed in:** `86f09c3f`, `bf47243d`, `f0b9fc37`.

---

**Total deviations:** 2 auto-fixed (both Rule 3 - Blocking; fixture + test migrations required to keep the suites and validate-all-profiles.sh green). No Rule 4 (architectural) triggers.
**Impact on plan:** Both deviations are the mechanical fallout of removing the schema/struct fields — explicitly anticipated by the environment notes ("do whatever the plan specifies to keep validate-all-profiles.sh and the suites green"). No scope creep; the synthesizer + tools.* population + inlined-configFiles removal remain Wave 5.

## Issues Encountered
- `internal/app/cmd/TestUnlockCmd_RequiresStateBucket` FAILS in the local environment (expects "state bucket" error, got "sandbox sb-aabbccdd is not locked"). Pre-existing, environment/credential-ordering dependent (km unlock precondition check order), unrelated to the agent block and untouched by this plan. Logged to `deferred-items.md` per the SCOPE BOUNDARY rule. All agent/codex/doctor cmd tests pass.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness (Wave 5 handoff)
- The structured `spec.agent:` block is fully defined (types + schema + inheritance + validation). Wave 5 owns the **synthesizer** (`pkg/compiler/agent_claude.go` + `agent_codex.go`) that reads `agent.claude.tools.*` / `agent.codex.tools.*` and emits Claude-Code `settings.json` `permissions.allow` / `permissions.deny` (NOT legacy `autoApprove` / `disallowedTools`).
- **Mixed-mode validator is currently PASSIVE on every fixture** — no profile populates `agent.claude.tools.autoApprove`/`deny` yet, and all fixtures still inline `configFiles[".claude/settings.json"]`. Wave 5 removes those inlined entries and populates `agent.claude.tools.*`; once it does, the validator becomes an active gate.
- The `phase92_wave5` build-tagged stubs (`pkg/compiler/agent_{claude,codex}_golden_test.go`) remain RED by design — Wave 5 removes that tag.
- VC-1 (`go build ./...`), VC-3 (Phase92ByteIdentity), VC-6 (mixed-mode), VC-11 (validate-all-profiles) all GREEN.

---
*Phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating*
*Completed: 2026-05-31*

## Self-Check: PASSED
- Created files verified on disk: 92-04-SUMMARY.md, pkg/profile/inherit_agent_test.go
- Task commits verified: 86f09c3f, 48641eaa, bf47243d, f0b9fc37
