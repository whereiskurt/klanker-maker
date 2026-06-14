---
phase: 113-sandbox-self-awareness-on-box-profile-plus-capability-network-privilege-self-census-in-klanker-sandbox
plan: 01
subsystem: compiler
tags: [userdata, go-yaml, golden-tests, byte-identity, sandbox-profile, ec2-bootstrap]

# Dependency graph
requires:
  - phase: 103-h1-bridge
    provides: H1 byte-identity golden and capture pattern (TestCapturePreH1Userdata)
  - phase: 92-sandboxprofile-spec-restructure
    provides: pre-Phase-92 byte-identity golden and capture pattern (TestCapturePre92Userdata)
provides:
  - ProfileYAML field on userDataParams struct (Phase 113 on-box profile write)
  - Section 2.10 template block writing /opt/km/.km-profile.yaml at boot
  - CAPTURE_ADDVOL_GOLDEN env guard for TestUserdataAdditionalVolumeOnly_GoldenByteIdentical
  - Two new unit tests: TestUserdataProfileWriteBlockRendered + TestUserdataProfileYAMLRoundTrip
  - Regenerated goldens: h1_byte_identity_golden.txt, userdata_learn_v2_pre92_baseline.golden.sh, userdata_additional_volume_only.golden.sh
affects: [113-02, 113-03, any phase touching pkg/compiler/userdata.go or byte-identity goldens]

# Tech tracking
tech-stack:
  added: [github.com/goccy/go-yaml import in pkg/compiler/userdata.go (was already in go.mod, not yet imported here)]
  patterns:
    - yaml.Marshal(p) marshaled immediately before tmpl.Execute to capture all mutations
    - heredoc with 'KM_PROFILE_EOF' sentinel (single-quoted to prevent shell expansion)
    - "{{- if .ProfileYAML }} guard makes the block non-fatal (marshal error = skip block)"
    - Env-gated capture guards (CAPTURE_FOO=1) for regenerating byte-identity goldens

key-files:
  created:
    - pkg/compiler/userdata_phase113_test.go
    - .planning/phases/113-sandbox-self-awareness.../113-01-SUMMARY.md
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go
    - pkg/compiler/testdata/h1_byte_identity_golden.txt
    - pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh
    - pkg/compiler/testdata/userdata_additional_volume_only.golden.sh

key-decisions:
  - "Profile written via yaml.Marshal(p) immediately before tmpl.Execute — captures all mutations (noBedrock, ttl/idle overrides) applied before compiler.Compile()"
  - "Section 2.10 placed between 2.9 (OTEL) and section 3 (secret injection) in template"
  - "Single-quoted heredoc sentinel 'KM_PROFILE_EOF' prevents shell variable expansion in YAML body"
  - "Literal chown sandbox:sandbox (not a template field) matches existing userdata pattern"
  - "CAPTURE_PRE92_BASELINE regen + SubagentStop strip: the pre-92 baseline golden must NOT contain SubagentStop (test strips it from generated output before comparison); ran capture then stripped the bash script block"

patterns-established:
  - "Pattern: byte-identity goldens updated as a separate Task 3 commit after code changes"
  - "Pattern: CAPTURE_ADDVOL_GOLDEN=1 env guard added to golden tests that lack a capture helper"

requirements-completed: []

# Metrics
duration: 15min
completed: 2026-06-14
---

# Phase 113 Plan 01: Userdata Profile On-Box Write Summary

**`/opt/km/.km-profile.yaml` now written at EC2 sandbox boot via section 2.10 heredoc, sourced from the same yaml.Marshal(p) that avoids re-marshal drift with the S3 copy.**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-06-14T16:56:00Z
- **Completed:** 2026-06-14T17:11:41Z
- **Tasks:** 3 of 3
- **Files modified:** 6

## Accomplishments
- Added `ProfileYAML string` field to `userDataParams` struct with Phase 113 doc comment
- Added `yaml "github.com/goccy/go-yaml"` import to `pkg/compiler/userdata.go` (library was in go.mod at v1.19.2 but not yet imported here)
- Inserted `yaml.Marshal(p)` immediately before `tmpl.Execute` in `generateUserData()` so all create-time mutations (noBedrock, TTL/idle overrides) are captured
- Added section "2.10" template block between OTEL (2.9) and secret injection (3): `mkdir -p /opt/km`, heredoc `<< 'KM_PROFILE_EOF'`, `chmod 0644`, `chown sandbox:sandbox`
- Two new unit tests pass: `TestUserdataProfileWriteBlockRendered` and `TestUserdataProfileYAMLRoundTrip`
- Added `CAPTURE_ADDVOL_GOLDEN=1` env-gated capture guard to `TestUserdataAdditionalVolumeOnly_GoldenByteIdentical` (mirrors the other two capture helpers)
- Regenerated all three byte-identity goldens with the new 2.10 block present; full `go test ./pkg/compiler/` is GREEN

## Task Commits

1. **Task 1: Add ProfileYAML field + section 2.10 template block** - `7ea1b70d` (feat)
2. **Task 2: New unit tests — write-block + YAML round-trip** - `cde30e46` (test)
3. **Task 3: Regenerate 3 byte-identity goldens + CAPTURE_ADDVOL_GOLDEN guard** - `e8a99481` (chore)

## Files Created/Modified
- `pkg/compiler/userdata.go` - Added yaml import, ProfileYAML field, yaml.Marshal call, section 2.10 template block
- `pkg/compiler/userdata_phase113_test.go` - New: TestUserdataProfileWriteBlockRendered + TestUserdataProfileYAMLRoundTrip
- `pkg/compiler/userdata_test.go` - Added CAPTURE_ADDVOL_GOLDEN=1 env-gated capture guard
- `pkg/compiler/testdata/h1_byte_identity_golden.txt` - Regenerated with section 2.10 block
- `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` - Regenerated with section 2.10 block (SubagentStop stripped per test invariant)
- `pkg/compiler/testdata/userdata_additional_volume_only.golden.sh` - Regenerated with section 2.10 block

## Golden Regen Commands (for future phases)

```bash
# 1. H1 dormancy golden (strict byte-identity, no stripping needed)
CAPTURE_PRE_H1_BASELINE=1 go test ./pkg/compiler/ -run TestCapturePreH1Userdata -count=1

# 2. Pre-92 baseline golden (SubagentStop MUST be stripped from bash block after capture)
CAPTURE_PRE92_BASELINE=1 go test ./pkg/compiler/ -run TestCapturePre92Userdata -count=1
# Then strip SubagentStop from the bash script block (NOT the settings.json JSON blob):
# The stripSubagentStopScript function in the test strips:
#   - "PostToolUse|SubagentStop)" -> "PostToolUse)"
#   - The "# 4b. SubagentStop:" handler block up to "# 5. Build subject + body"
# Apply equivalent strip to the golden file after capture.

# 3. Additional-volume golden (env-gated capture guard added in Phase 113)
CAPTURE_ADDVOL_GOLDEN=1 go test ./pkg/compiler/ -run TestUserdataAdditionalVolumeOnly_GoldenByteIdentical -count=1

# Spot-check: each golden must contain the 2.10 path (expect >= 4 per file)
grep -c '/opt/km/.km-profile.yaml' pkg/compiler/testdata/*.golden.sh pkg/compiler/testdata/h1_byte_identity_golden.txt
```

## Decisions Made
- `yaml.Marshal(p)` called immediately before `tmpl.Execute` (not from raw file bytes, not via remoteProfileYAML from create.go) — matches CONTEXT.md locked decision: "no re-marshal drift", captures mutations
- Heredoc uses single-quoted sentinel `<< 'KM_PROFILE_EOF'` to prevent shell expansion of any `$VAR` or `${VAR}` references in the YAML body (e.g. configFile content with shell vars would otherwise be expanded at boot)
- `{{- if .ProfileYAML }}` gate makes the block non-fatal: if yaml.Marshal ever returns an error, the profile-write block is simply omitted and the rest of userdata is valid
- No signature change to `Compile()`/`compileEC2()`/`generateUserData()` (plan constraint honored)
- No profile redaction (write verbatim — locked decision in CONTEXT.md)
- No IAM/schema/Terraform/DDB change

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] SubagentStop strip needed after CAPTURE_PRE92_BASELINE regen**
- **Found during:** Task 3 (golden regeneration)
- **Issue:** Running `CAPTURE_PRE92_BASELINE=1 go test ...` captures the raw `generateLearnV2Userdata()` output which includes SubagentStop content. But `TestUserdataLearnV2Phase92ByteIdentity` and `TestUserdataKmPrefixByteIdentity` call `stripSubagentStopScript(generateLearnV2Userdata(t))` before comparing to the golden. So the golden must also be SubagentStop-stripped.
- **Fix:** After capturing with `CAPTURE_PRE92_BASELINE=1`, applied the `stripSubagentStopScript` logic (two replacements: gate-case line + handler block) to the golden file before committing. The remaining 2 SubagentStop references in the golden are inside the settings.json JSON blob, handled separately by `assertClaudeSettingsSemanticEquivalence`.
- **Files modified:** `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh`
- **Verification:** `TestUserdataLearnV2Phase92ByteIdentity` and `TestUserdataKmPrefixByteIdentity` both GREEN after fix.
- **Committed in:** e8a99481 (Task 3 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 — bug in capture process)
**Impact on plan:** Necessary fix to maintain existing test invariant. No scope creep. The strip is consistent with what `stripSubagentStopScript` removes at test time.

## Issues Encountered

**TestUserdataKmPrefixByteIdentity pre-existing failure:** This test was already failing on the committed main branch before Phase 113 (confirmed via git stash + test run). The pre-113 golden had SubagentStop stripped (baseline was captured pre-SubagentStop), but subsequent `CAPTURE_PRE92_BASELINE` captures included SubagentStop. Phase 113 resolved this by ensuring the regenerated golden is properly stripped. The test is now GREEN for the first time in Phase 113.

## User Setup Required

None — no external service configuration required. Deploy notes from plan: `make build-lambdas` + `km init --dry-run=false` for live deploy. Existing sandboxes need `km destroy && km create`. This plan's tasks are unit-test-only.

## Next Phase Readiness
- 113-02 (klanker:sandbox SKILL.md self-census rewrite) can proceed: the `/opt/km/.km-profile.yaml` file now exists at boot on all EC2 sandboxes provisioned after this deploy
- 113-03 (docs + live UAT) can proceed after 113-02

---
*Phase: 113-sandbox-self-awareness-on-box-profile-plus-capability-network-privilege-self-census-in-klanker-sandbox*
*Completed: 2026-06-14*
