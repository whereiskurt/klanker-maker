---
phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating
plan: 00
subsystem: testing
tags: [golden-test, byte-identity, build-tags, red-stubs, nyquist, compiler, profile]

# Dependency graph
requires:
  - phase: 91-slack-inbound-mention-only-mode
    provides: green pkg/compiler + pkg/profile baseline suites to capture pre-change goldens against
provides:
  - Pre-Phase-92 userdata byte-identity baseline for profiles/learn.v2.yaml (VC-3)
  - Pre-Phase-92 IAM HCL byte-identity baseline (max_session_duration + allowed_regions + km_secret_paths) (VC-4)
  - Four build-tagged RED test stubs that Waves 2/4/5 turn GREEN (VC-5, VC-6, VC-7)
  - Capture-guarded regeneration helpers for both goldens (CAPTURE_PRE92_BASELINE / CAPTURE_PRE92_IAM_BASELINE)
affects: [92-wave1-iam-rename, 92-wave2-notification-block, 92-wave4-agent-spec, 92-wave5-synthesizers]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Byte-identity golden capture via env-guarded TestCapture* helper that shares its generator with the assertion test (capture and verify drive identical compiler inputs)"
    - "Per-wave Go build tags (phase92_wave2/4/5) to land RED stubs that reference post-phase API without breaking the default build"

key-files:
  created:
    - pkg/compiler/userdata_phase92_byte_identity_test.go
    - pkg/compiler/security_phase92_byte_identity_test.go
    - pkg/compiler/agent_claude_golden_test.go
    - pkg/compiler/agent_codex_golden_test.go
    - pkg/profile/inherit_notification_test.go
    - pkg/profile/validate_mixed_settings_test.go
    - pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh
    - pkg/compiler/testdata/security_iam_pre92_baseline.golden.hcl
  modified: []

key-decisions:
  - "Adapted plan stub import path github.com/klankermaker/km -> real module github.com/whereiskurt/klanker-maker, and internal/util/ptr -> local phase92BoolPtr helper"
  - "Used profile.Parse([]byte) (not the plan's hypothetical profile.Load) and generateUserData(p, id, nil, bucket, false, nil) — the actual repo API"
  - "IAM baseline rendered as a focused fragment driving the REAL compileIAMPolicy + compileSecrets, not the full EC2 service HCL — captures exactly the three identity reads with zero subnet/AMI noise"
  - "Injected synthetic allowedSecretPaths into the restricted-dev.yaml fixture so the IAM baseline covers the SSM-allowlist read (the builtin sets only roleSessionDuration + allowedRegions)"
  - "VC-6 stub targets the package-level ValidateSemantic(p) []ValidationError API (not the plan's p.ValidateSemantic() method) to match the real validator Wave 4 extends"

patterns-established:
  - "Capture-once / verify-forever golden workflow: env-guarded capture writes the .golden file; the committed assertion re-runs the SAME generator and diffs"
  - "RED stubs are build-tagged per consuming wave so go test stays green on pre-change main while each wave has a precise compile-failing target to satisfy"

requirements-completed: []

# Metrics
duration: 4min
completed: 2026-05-31
---

# Phase 92 Plan 00: Test Scaffolding + Research Spikes Summary

**Captured two pre-Phase-92 byte-identity goldens (learn.v2 userdata, IAM HCL) before any restructure code lands, and stood up four build-tagged RED stubs that Waves 2/4/5 turn GREEN — establishing Nyquist verification anchors VC-3 through VC-7.**

## Performance

- **Duration:** ~4 min
- **Started:** 2026-05-31T20:52:58Z
- **Completed:** 2026-05-31T20:56:41Z
- **Tasks:** 3
- **Files modified:** 8 (all created)

## Accomplishments
- Captured `userdata_learn_v2_pre92_baseline.golden.sh` (136,427 bytes / 3,184 lines) and a passing `TestUserdataLearnV2Phase92ByteIdentity` (VC-3) — both committed BEFORE any Wave 1 file was touched.
- Captured `security_iam_pre92_baseline.golden.hcl` (166 bytes covering `max_session_duration=3600`, `allowed_regions=["us-east-1"]`, `km_secret_paths=...`) and a passing `TestIAMHCLPhase92ByteIdentity` (VC-4), driving the real `compileIAMPolicy` + `compileSecrets`.
- Landed four RED stubs behind build tags `phase92_wave5` (synthesizeClaudeSettings/synthesizeCodexConfig goldens, VC-5), `phase92_wave2` (notification inheritance, VC-7), and `phase92_wave4` (mixed-mode validation, VC-6).
- Verified the default `go test ./pkg/compiler/... ./pkg/profile/...` stays GREEN, and that each wave tag fails compile by design with errors naming exactly the post-phase API each wave must create.

## Task Commits

Each task was committed atomically:

1. **Task 1: Capture pre-Phase-92 userdata baseline + byte-identity stub** - `c56ffdaf` (test)
2. **Task 2: Capture pre-Phase-92 IAM HCL baseline + byte-identity stub** - `0d739ac7` (test)
3. **Task 3: RED stubs for synthesizers + inherit + mixed-mode** - `2b4df4a6` (test)

**Plan metadata:** pending (docs: complete plan — final commit)

## Files Created/Modified
- `pkg/compiler/userdata_phase92_byte_identity_test.go` - Capture helper + VC-3 byte-identity assertion; shared `generateLearnV2Userdata()` drives both.
- `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` - Pre-Phase-92 userdata baseline (136,427 bytes).
- `pkg/compiler/security_phase92_byte_identity_test.go` - Capture helper + VC-4 IAM HCL byte-identity assertion; `emitCombinedIAMHCLForTest` drives real `compileIAMPolicy`/`compileSecrets`.
- `pkg/compiler/testdata/security_iam_pre92_baseline.golden.hcl` - Pre-Phase-92 IAM HCL baseline.
- `pkg/compiler/agent_claude_golden_test.go` - VC-5 RED stub (`phase92_wave5`) for `synthesizeClaudeSettings()` per learn.v2/dc34/locked/codex fixtures.
- `pkg/compiler/agent_codex_golden_test.go` - VC-5 RED stub (`phase92_wave5`) for `synthesizeCodexConfig()` (inert config + args echo).
- `pkg/profile/inherit_notification_test.go` - VC-7 RED stub (`phase92_wave2`): child-only transcript flag inherits parent perSandbox.
- `pkg/profile/validate_mixed_settings_test.go` - VC-6 RED stub (`phase92_wave4`): autoApprove + inlined configFiles -> ValidationError.

## Decisions Made
See `key-decisions` frontmatter. Core deviations from the plan's literal stub code were all forced by the plan referencing a different module/API surface than this repo:
- Import path `github.com/klankermaker/km` -> `github.com/whereiskurt/klanker-maker`.
- `profile.Load(path)` -> `profile.Parse(readFile)`; `internal/util/ptr` -> local `phase92BoolPtr`.
- `p.ValidateSemantic()` method -> `ValidateSemantic(p) []ValidationError` package func.
- IAM baseline rendered as a focused fragment (not full EC2 service HCL) to eliminate subnet/AMI noise while still driving the exact identity reads Wave 1 renames.

## Deviations from Plan

The four RED-stub files differ from the plan's verbatim code only in import path, helper names, and the load/validate API calls listed above — these are not behavioral deviations but corrections to make the stubs reference this repo's real (pre-change) and post-change API. No source under test was modified; goldens reflect current `main` output.

**Total deviations:** 0 auto-fixes (Rules 1-4 not triggered). All differences are plan-stub-to-repo-API adaptations within the task's stated intent.
**Impact on plan:** None. All six stubs + two goldens exist exactly as specified; VC-3/VC-4 pass on current main; VC-5/VC-6/VC-7 are correctly RED behind their wave tags.

## Issues Encountered
- The plan's example stubs assumed APIs (`profile.Load`, `ResolveInheritance`, `internal/util/ptr`, `p.ValidateSemantic()`) that do not exist in this repo. Resolved by inspecting the real surface (`profile.Parse`, `generateUserData`, `compileIAMPolicy`/`compileSecrets`, `ValidateSemantic(p)`) and adapting. Build-tag-gated stubs were free to reference the genuinely-absent post-phase API (that absence IS the RED signal).
- No AWS/terragrunt was invoked; all verification was local (`go test`, `go vet`). No deferred-to-UAT items.

## Downstream Wave Handoff

| Wave | Build tag to REMOVE | RED stub(s) to turn GREEN | VC |
|------|---------------------|---------------------------|----|
| Wave 1 (IAM rename) | — (no tag) | Must keep `TestIAMHCLPhase92ByteIdentity` + `TestUserdataLearnV2Phase92ByteIdentity` GREEN | VC-3, VC-4 |
| Wave 2 (notification block) | `phase92_wave2` | `inherit_notification_test.go` | VC-7 |
| Wave 4 (structured AgentSpec + validation) | `phase92_wave4` | `validate_mixed_settings_test.go` | VC-6 |
| Wave 5 (synthesizers) | `phase92_wave5` | `agent_claude_golden_test.go`, `agent_codex_golden_test.go` | VC-5 |

**Wave 1 is now UNBLOCKED:** both byte-identity baselines were captured and committed (`c56ffdaf`, `0d739ac7`) before any Wave 1 source change. The IAM rename must keep `TestIAMHCLPhase92ByteIdentity` GREEN.

## Next Phase Readiness
- Verification anchors VC-3..VC-7 are in place; every Wave 1-5 plan can wire `verifies: VC-N` to a concrete stub here.
- `.planning/research/codex-config-toml.md` confirmed present and untouched (contains the `permissions.deny` contract marker).

## Self-Check: PASSED

All 8 created files exist on disk; all 3 task commits (`c56ffdaf`, `0d739ac7`, `2b4df4a6`) present in git history. `go test ./pkg/compiler/... ./pkg/profile/...` GREEN; each wave build tag fails compile by design (RED confirmed).

---
*Phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating*
*Completed: 2026-05-31*
