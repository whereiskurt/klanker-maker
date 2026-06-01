---
phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating
plan: 05
subsystem: compiler-agent-synthesizers
tags: [agent-synthesizer, tool-gating, claude-settings-json, codex-config-toml, permissions-allow, byte-identity, fixture-rewrite, golden-tests]

# Dependency graph
requires:
  - phase: 92-00
    provides: phase92_wave5 RED golden stubs (agent_claude/codex_golden_test.go) + learn.v2 userdata byte-identity baseline (VC-3) + Claude/Codex Wave-0 research spike
  - phase: 92-04
    provides: typed spec.agent block (AgentSpec/Claude/Codex/ToolsSpec) + schema + inheritance + mixed-mode validator (PASSIVE until this wave populates tools.*)
provides:
  - "pkg/compiler/agent_claude.go — synthesizeClaudeSettings(agent) -> canonical permissions.allow/deny + trustedDirectories + permissions passthrough merge"
  - "pkg/compiler/agent_codex.go — synthesizeCodexConfig(agent) -> base hook block (byte-identical to Phase 70 heredoc) + args echo + tools asymmetry NOTE"
  - "userdata pipeline rewired: synthesizeClaudeSettings -> inject configFiles -> mergeNotifyHookIntoSettings -> write; codex config.toml synthesized via params.CodexConfigTOML in the existing early heredoc slot"
  - "5 golden files (4 claude_settings_*.golden.json + codex_config_codex.golden.toml); phase92_wave5 build tag removed -> VC-5 GREEN in default suite"
  - "VC-3 reconciled: strict byte-identity outside the settings.json blob + semantic-equivalence assertion (same tool set / trustedDirectories / hooks) for the blob (canonical permissions.allow intentionally replaces legacy autoApprove)"
  - "11 profile YAMLs rewritten: inlined Claude settings.json REMOVED everywhere; agent.claude.tools.autoApprove + trustedDirectories populated (zero behavior change)"
  - "docs/agent-tool-gating.md (new, 158 lines) + docs/codex-parity.md + CLAUDE.md (Phase 92 consolidated callout, Releases preserved)"
affects: [92-06-operator-uat]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Typed-field -> config-file synthesizer (agent_claude.go / agent_codex.go): profile YAML carries typed tool gating; compiler emits the agent's native config format at km create time. Replaces the inlined-JSON-in-YAML antipattern."
    - "Behavior-preserving config.toml synthesis: synthesizeCodexConfig reproduces the pre-Phase-92 heredoc byte-for-byte as a base block so the userdata byte-identity contract (VC-3) holds and real sandboxes get unchanged bytes."
    - "Split byte-identity reconciliation: when an intentional canonical-form migration changes one well-defined content blob, split the golden assertion into (a) strict byte-identity for the surrounding bytes and (b) semantic-equivalence for the blob — rather than weakening the whole test."

key-files:
  created:
    - pkg/compiler/agent_claude.go
    - pkg/compiler/agent_codex.go
    - pkg/compiler/testdata/claude_settings_learn_v2.golden.json
    - pkg/compiler/testdata/claude_settings_dc34.golden.json
    - pkg/compiler/testdata/claude_settings_locked.golden.json
    - pkg/compiler/testdata/claude_settings_codex.golden.json
    - pkg/compiler/testdata/codex_config_codex.golden.toml
    - docs/agent-tool-gating.md
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/agent_claude_golden_test.go
    - pkg/compiler/agent_codex_golden_test.go
    - pkg/compiler/userdata_phase92_byte_identity_test.go
    - profiles/learn.v2.yaml
    - profiles/dc34.yaml
    - profiles/locked.yaml
    - profiles/learn.v2.chatty.yaml
    - profiles/learn.v2.codex.yaml
    - profiles/learn.v2.polite.yaml
    - profiles/dc34.ami.yaml
    - profiles/locked.ami.yaml
    - profiles/goose.yaml
    - pkg/profile/builtins/learn.yaml
    - pkg/profile/builtins/goose.yaml
    - docs/codex-parity.md
    - CLAUDE.md

key-decisions:
  - "Claude settings.json emits canonical permissions.allow / permissions.deny (Wave 0 research Option B), NOT legacy top-level autoApprove and NOT disallowedTools (a CLI flag). Permissions passthrough merges into the permissions object; typed allow/deny win on collision."
  - "synthesizeCodexConfig reproduces the existing Phase 70 codex config.toml heredoc byte-for-byte as its base block (matcher/timeout/statusMessage syntax), NOT the plan's simplified pseudocode — required by the #1 behavior-preservation property: changing those bytes would alter every sandbox's real codex config AND break VC-3."
  - "Codex config.toml stays in its EARLY userdata heredoc slot (rendered via params.CodexConfigTOML), NOT moved to the post-initCommands configFiles path — so codex.yaml's initCommands can still overwrite ~/.codex/config.toml with its model/provider config exactly as before."
  - "VC-3 byte-identity test redefined (Wave 5 locked decision): strict byte-identity for all userdata OUTSIDE the Claude settings.json blob; semantic-equivalence (same effective autoApprove set + deny set + trustedDirectories + km-notify hooks) for the blob itself, since canonical permissions.allow intentionally replaces legacy autoApprove. The byte-identity baseline golden was NOT regenerated — the reconciled test parses the legacy baseline blob and proves equivalence to the canonical output."

patterns-established:
  - "Synthesizer nil-safety: nil agent / nil agent.Claude -> empty settings map (codex-default profiles get a hooks-only settings.json, byte-identical to pre-92); nil agent.Codex -> base hook block only (the common case)."

requirements-completed: []

# Metrics
duration: 24min
completed: 2026-05-31
---

# Phase 92 Plan 05: Agent Synthesizers + Fixture Rewrite + Docs Summary

**Landed `agent_claude.go` (`synthesizeClaudeSettings` -> canonical `permissions.allow`/`permissions.deny` + `trustedDirectories`) and `agent_codex.go` (`synthesizeCodexConfig` -> byte-identical Phase-70 hook block + args echo + honest tool-gating asymmetry note), rewired the userdata pipeline to synthesize both config files from the typed `spec.agent` block, removed every inlined `configFiles[".claude/settings.json"]` across 11 profile fixtures with ZERO change to the effective auto-approved tool set, reconciled the VC-3 byte-identity contract into strict-outside-blob + semantic-equivalence-inside-blob, and shipped `docs/agent-tool-gating.md` as the authoritative guide.**

## Performance
- **Duration:** ~24 min
- **Started:** 2026-05-31T22:51:57Z
- **Completed:** 2026-05-31T23:16:52Z
- **Tasks:** 4
- **Files modified:** 24 (8 created)

## Accomplishments
- **2 synthesizers** (`agent_claude.go` 80 lines, `agent_codex.go` 99 lines) + userdata pipeline rewiring. Claude path: synthesize -> inject into configFiles -> `mergeNotifyHookIntoSettings` -> write. Codex path: synthesize -> render in the existing early heredoc slot via `params.CodexConfigTOML`.
- **11 fixtures migrated** off inlined Claude settings.json to typed `agent.claude.tools.*` + `trustedDirectories`, with a per-profile semantic diff confirming a ZERO-change tool set (see table below).
- **5 golden files** committed; `phase92_wave5` build tag removed -> VC-5 GREEN in the default suite.
- **VC-3 reconciled** and GREEN: the byte-identity test now proves the spec restructure is semantically transparent (strict byte-identity for all userdata except the settings.json blob; provable tool-set/trustedDirectories/hooks equivalence for the blob).
- **3 docs**: new `docs/agent-tool-gating.md` (agent block, synthesis pipeline, Claude/Codex asymmetry, no-merge rule, migration, passthrough, future work); `codex-parity.md` updated (config.toml now synthesized); CLAUDE.md Phase 92 consolidated callout (Releases preserved).

## Per-fixture semantic permission diff (behavior preservation — the #1 safety property)

For every rewritten profile, the OLD inlined settings.json tool set vs the NEW synthesized tool set:

| Profile | Old inlined autoApprove | New synthesized permissions.allow | trustedDirectories | Δ |
|---|---|---|---|---|
| learn.v2 | Bash,Read,Write,Edit,Glob,Grep,WebFetch,WebSearch,NotebookEdit | (same 9) | /home/sandbox,/workspace | **none** |
| dc34 | (same 9) | (same 9) | (same) | **none** |
| dc34.ami | (same 9) | (same 9) | (same) | **none** |
| locked | (same 9) | (same 9) | (same) | **none** |
| locked.ami | (same 9) | (same 9) | (same) | **none** |
| learn.v2.chatty | (same 9) | (same 9) | (same) | **none** |
| learn.v2.codex | (same 9) | (same 9) | (same) | **none** |
| learn.v2.polite | (same 9) | (same 9) | (same) | **none** |
| builtins/learn | (same 9) | (same 9) | (same) | **none** |
| goose | (none — trustedDirectories only) | (none) | /home/sandbox,/workspace | **none** |
| builtins/goose | (none — trustedDirectories only) | (none) | /home/sandbox,/workspace | **none** |

`codex.yaml` and all other builtins (ao, codex, hardened, open-dev, restricted-dev, sealed) + `profiles/ao.yaml` + `example-additional-snapshots.yaml` never inlined a Claude settings.json and carried no remaining `cli.agent` fields, so they needed no edit (left untouched to avoid needless churn — see Deviations).

**The only on-the-wire change** is the JSON SHAPE of the synthesized settings.json (`{"autoApprove":[...]}` -> `{"permissions":{"allow":[...]}}`), which is semantically equivalent per Claude Code 2.1.132 (Wave 0 research §1b) and proven equivalent by the reconciled VC-3 test. No tool gained or lost auto-approval anywhere.

## Task Commits

1. **Task 1: agent_claude.go synthesizer + 4 goldens + build-tag removal** — `fdfc974f` (feat). Also atomically rewrote learn.v2/dc34/locked (remove inlined settings.json + populate tools) so the golden test goes green without tripping the mixed-mode validator.
2. **Task 2: agent_codex.go synthesizer + golden + build-tag removal** — `572e6162` (feat)
3. **Task 3: wire synthesizers into userdata; rewrite remaining 8 fixtures; reconcile VC-3** — `95518ca8` (feat)
4. **Task 4: docs (agent-tool-gating.md new + codex-parity.md + CLAUDE.md)** — `f9dc10bf` (docs)

**Plan metadata:** _(see final docs commit)_

## Files Created/Modified
- `pkg/compiler/agent_claude.go` — synthesizeClaudeSettings (canonical permissions.allow/deny + trustedDirectories + passthrough)
- `pkg/compiler/agent_codex.go` — synthesizeCodexConfig (codexConfigBaseBlock byte-identical to Phase 70 heredoc + args echo + asymmetry NOTE)
- `pkg/compiler/userdata.go` — synthesize+inject Claude settings before merge; CodexConfigTOML param drives the early heredoc
- `pkg/compiler/userdata_phase92_byte_identity_test.go` — reconciled VC-3 (extractClaudeSettingsBlob + assertClaudeSettingsSemanticEquivalence helpers)
- `pkg/compiler/agent_{claude,codex}_golden_test.go` — phase92_wave5 build tag removed
- 5 golden files under `pkg/compiler/testdata/`
- 11 profile YAMLs — inlined Claude settings.json removed; agent.claude.tools.* / trustedDirectories populated
- `docs/agent-tool-gating.md` (new), `docs/codex-parity.md`, `CLAUDE.md`

## Decisions Made
See `key-decisions` frontmatter. Headlines: canonical `permissions.allow`/`deny` (Option B); codex base block reproduced byte-for-byte for behavior preservation; codex config.toml kept in its early heredoc slot to preserve codex.yaml's initCommands override; VC-3 split into strict-outside-blob + semantic-inside-blob.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Codex synthesizer reproduces the existing heredoc hook syntax, NOT the plan's simplified pseudocode**
- **Found during:** Task 2 (agent_codex.go).
- **Issue:** The plan's `<interfaces>` pseudocode showed a simplified codex hook block (`command = "/opt/km/bin/km-notify-hook"`, no `matcher`/`timeout`/`statusMessage`). The REAL Phase 70 userdata heredoc (and the learn.v2 byte-identity baseline) embed the richer syntax. Emitting the simplified form would (a) change the actual config.toml shipped to every sandbox and (b) break VC-3.
- **Fix:** Authored `codexConfigBaseBlock` as a byte-identical reproduction of the existing heredoc; the golden was captured from the synthesizer to guarantee byte-identity.
- **Files modified:** pkg/compiler/agent_codex.go, pkg/compiler/testdata/codex_config_codex.golden.toml
- **Verification:** TestSynthesizeCodexConfigGolden GREEN; learn.v2 byte-identity confirms the codex block is unchanged.
- **Committed in:** `572e6162`.

**2. [Rule 3 - Blocking] Codex config.toml kept in the early userdata heredoc slot (params.CodexConfigTOML), not moved to the post-initCommands configFiles path**
- **Found during:** Task 3 (userdata wiring).
- **Issue:** The plan suggested writing codex config.toml via `params.ConfigFiles["/home/sandbox/.codex/config.toml"]`. But configFiles are written AFTER initCommands, and `codex.yaml`'s initCommands write their OWN model/provider config.toml. Routing through configFiles would overwrite the model/provider config — a behavior change for codex.yaml.
- **Fix:** Added a `CodexConfigTOML` param and rendered the synthesizer output in the existing EARLY heredoc position, preserving the original ordering and the initCommands-override semantics.
- **Files modified:** pkg/compiler/userdata.go (UserDataParams + template).
- **Verification:** codex.yaml userdata still has the base hook block early; pkg/compiler suite GREEN.
- **Committed in:** `95518ca8`.

**3. [Rule 3 - Blocking] VC-3 byte-identity test redefined into strict-outside-blob + semantic-inside-blob**
- **Found during:** Task 3.
- **Issue:** Canonical `permissions.allow` intentionally replaces legacy `autoApprove` (Wave 0 Option B), making the settings.json BLOB byte-different from the pre-92 baseline while everything else stays identical. The plan (Task 3 lines 528-536) explicitly mandates this reconciliation.
- **Fix:** Rewrote `TestUserdataLearnV2Phase92ByteIdentity` to extract the settings.json heredoc blob, assert strict byte-identity on the remainder, and assert semantic equivalence (same effective autoApprove set, deny set, trustedDirectories, km-notify hooks) on the blob. The baseline golden was NOT regenerated — the test parses the legacy blob and proves equivalence, which is a STRONGER guarantee than a regenerated golden.
- **Files modified:** pkg/compiler/userdata_phase92_byte_identity_test.go.
- **Verification:** TestUserdataLearnV2Phase92ByteIdentity GREEN.
- **Committed in:** `95518ca8`.

**4. [Rule 3 - Blocking] Task 1 absorbed the learn.v2/dc34/locked fixture rewrite (golden/validator coupling)**
- **Found during:** Task 1.
- **Issue:** The Claude golden test reads the actual profile files, and the mixed-mode validator hard-errors if `agent.claude.tools.autoApprove` coexists with an inlined settings.json. So the 3 golden-referenced fixtures had to have their settings.json removed AND tools populated atomically for both the golden test and validate-all-profiles to stay green — they couldn't wait for Task 3.
- **Fix:** Rewrote learn.v2/dc34/locked in the Task 1 commit; the remaining 8 fixtures landed in Task 3 with the userdata wiring.
- **Files modified:** profiles/{learn.v2,dc34,locked}.yaml (in Task 1).
- **Verification:** golden test + km validate GREEN per fixture.
- **Committed in:** `fdfc974f`.

**5. [Note, not a code change] RESEARCH §2f claimed pkg/profile/configfiles/ does not exist; it DOES (pre-existing, unrelated content)**
- **Found during:** SUMMARY self-check.
- **Issue:** The directory `pkg/profile/configfiles/` exists (created 2026-05-21, contains an unrelated `km-queue-runner_test.sh`). RESEARCH §2f said it doesn't exist and to not touch it.
- **Fix:** None needed — the directory was NOT touched (zero git diff). The plan's intent (leave it alone) is honored; only the existence claim was inaccurate.
- **Files modified:** none.
- **Committed in:** n/a.

---

**Total deviations:** 4 auto-fixed (all Rule 3 - Blocking; behavior-preservation + golden/validator coupling) + 1 documentation note. No Rule 4 (architectural) triggers.
**Impact on plan:** Deviations 1-3 are the direct consequence of upholding the #1 behavior-preservation property and the VC-3 reconciliation the plan itself mandated; the synthesizer's emitted shape and the pipeline wiring are exactly as specified semantically. Deviation 4 is a task-boundary shuffle (same total work, atomic commits). No scope creep.

## Issues Encountered
- The ao/codex/hardened/open-dev/restricted-dev/sealed builtins + profiles/ao.yaml + example-additional-snapshots.yaml are listed in the plan's `files_modified` but required NO edits (no inlined Claude settings.json, no remaining cli.agent fields after Wave 4). Left untouched to avoid needless churn; all 20 profiles still validate.
- The pre-existing `internal/app/cmd/TestUnlockCmd_RequiresStateBucket` flake noted in the 92-04 SUMMARY is unrelated to this wave; the full `go test ./...` run completed with exit 0 in this environment.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness (Wave 6 UAT handoff)
- All automated VCs are GREEN: VC-1 (`go build ./...`), VC-3 (reconciled byte-identity), VC-5 (5 synthesizer goldens), VC-11 (`validate-all-profiles.sh` exit 0, all 20). VC-8/VC-9/VC-10 are UAT-only (require live `km create` + SSM) and are the subject of Wave 6.
- The mixed-mode validator is now an ACTIVE gate on the rewritten fixtures (they populate `agent.claude.tools.*` and no longer inline settings.json).
- Wave 6 UAT should `km create profiles/learn.v2.yaml` and `cat /home/sandbox/.claude/settings.json` to confirm the synthesized canonical form on a live sandbox, plus `cat /home/sandbox/.codex/config.toml` for a codex profile.
- Reminder for operators after merge: `make build && km init --sidecars` so management Lambdas pick up the synthesizer + schema.

---
*Phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating*
*Completed: 2026-05-31*

## Self-Check: PASSED
- Created files verified on disk: agent_claude.go, agent_codex.go, 5 golden files, docs/agent-tool-gating.md, 92-05-SUMMARY.md
- Task commits verified: fdfc974f, 572e6162, 95518ca8, f9dc10bf
