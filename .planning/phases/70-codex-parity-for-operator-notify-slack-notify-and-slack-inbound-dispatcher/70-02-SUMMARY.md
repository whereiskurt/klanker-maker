---
phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
plan: 02
subsystem: compiler
tags: [codex, userdata, km-notify-hook, notify-env, toml, hooks]

# Dependency graph
requires:
  - phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
    provides: "Plan 70-01 added CLISpec.Agent field to pkg/profile/types.go"
  - phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
    provides: "Plan 70-03 added km-notify-hook PermissionRequest + Stop last_assistant_message branches"
provides:
  - "Unconditional ~/.codex/config.toml install heredoc in every sandbox userdata"
  - "KM_AGENT env var emitted in /etc/profile.d/km-notify-env.sh and /etc/km/notify.env"
  - "5 golden tests: TestUserdata_CodexConfig_Emitted/NoPostToolUse, TestUserdata_KMAgentEnv_DefaultClaude/Codex/BothFiles"
affects:
  - "70-05 (Slack inbound poller dispatch fork reads KM_AGENT)"
  - "70-06 (Slack prefix routing reads agent_type from DDB, seeded by KM_AGENT)"
  - "70-09 (UAT verifies config.toml is on disk with correct TOML content)"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Unconditional template heredoc: config.toml written for ALL sandboxes, inert for Claude-default ones"
    - "KM_AGENT default resolution: absent/'' → 'claude'; 'codex' → 'codex' (binary, no third case)"
    - "Dual-file env emission: single notifyEnv map feeds both profile.d/ and /etc/km/notify.env via template range"

key-files:
  created:
    - pkg/compiler/userdata_codex_test.go
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/testdata/userdata_additional_volume_only.golden.sh

key-decisions:
  - "config.toml written unconditionally (not gated on spec.cli.agent) per CONTEXT.md locked decision"
  - "KM_AGENT emitted only when Spec.CLI != nil — same gating as KM_NOTIFY_ON_PERMISSION (consistency)"
  - "No [[hooks.PostToolUse]] in config.toml — Tier 2 explicit deferral (Phase 68 parity adds it later)"
  - "Golden file regenerated after template expansion (Rule 1 auto-fix — 70-03 had already added partial content)"

requirements-completed: [SC-1, SC-4, SC-5, SC-6]

# Metrics
duration: 6min
completed: 2026-05-23
---

# Phase 70 Plan 02: Compiler Codex Config Writer Summary

**Unconditional ~/.codex/config.toml install + KM_AGENT dual-file env emission wired into the EC2 userdata template**

## Performance

- **Duration:** 6 min
- **Started:** 2026-05-23T04:01:45Z
- **Completed:** 2026-05-23T04:07:45Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments

- Codex hook configuration (`[features] codex_hooks = true`, `[[hooks.PermissionRequest]]` matcher `".*"`, `[[hooks.Stop]]`, both pointing at `/opt/km/bin/km-notify-hook`) is now installed at sandbox boot via `KM_CODEX_CONFIG_EOF` heredoc for every EC2 sandbox — Claude-default sandboxes simply never invoke Codex so the file has no runtime effect
- `KM_AGENT` env var (value `claude` or `codex`, defaulting to `claude` when `Spec.CLI.Agent == ""`) is now emitted into both `/etc/profile.d/km-notify-env.sh` (interactive shells) and `/etc/km/notify.env` (systemd `EnvironmentFile=`) via the existing dual-range template loop
- 5 named tests in `pkg/compiler/userdata_codex_test.go` guard the new compiler artifacts: 2 tests for config.toml content + Tier 2 deferral guard (no PostToolUse), 3 tests for KM_AGENT env emission (default, codex, dual-file)

## Task Commits

1. **Task 1: Wave 0 stub seed** - `d5854e8` (test) — 5 `t.Skip` stubs
2. **Task 2: Implementation + replace stubs** - `e93e872` (feat) — userdata.go new heredoc + KM_AGENT, real tests, golden file update

## Files Created/Modified

- `pkg/compiler/userdata_codex_test.go` — 5 tests: CodexConfig_Emitted, CodexConfig_NoPostToolUse, KMAgentEnv_DefaultClaude, KMAgentEnv_Codex, KMAgentEnv_BothFiles (all PASS)
- `pkg/compiler/userdata.go` — new `KM_CODEX_CONFIG_EOF` heredoc block (lines ~884-906) with exact TOML from CONTEXT.md; `notifyEnv["KM_AGENT"] = agent` emission (line ~3692) inside existing `if p.Spec.CLI != nil` block
- `pkg/compiler/testdata/userdata_additional_volume_only.golden.sh` — regenerated to include new 31-line codex config.toml install block

## Decisions Made

- `config.toml` written unconditionally per CONTEXT.md locked decision. Rationale: Claude-default sandboxes never start Codex; the file is inert.
- `KM_AGENT` emitted inside `if p.Spec.CLI != nil` block — consistent with every other notify env var. If CLI block is absent (nil), no env file is written, so no KM_AGENT either; downstream poller defaults to `claude` via `${KM_AGENT:-claude}`.
- No `[[hooks.PostToolUse]]` in config.toml — guarded by `TestUserdata_CodexConfig_NoPostToolUse` to prevent accidental Tier 3 leakage.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Golden file stale after userdata template expansion**
- **Found during:** Task 2 (full compiler test run after implementation)
- **Issue:** `TestUserdataAdditionalVolumeOnly_GoldenByteIdentical` failed because Plan 70-03 (already committed before 70-02 ran) had partially added the codex config.toml block; my Task 2 implementation completed and expanded the block, making the golden file's byte count stale
- **Fix:** Regenerated `testdata/userdata_additional_volume_only.golden.sh` using a temporary `TestGenerateGolden` helper with `KM_UPDATE_GOLDEN=1` env guard, then deleted the helper
- **Files modified:** `pkg/compiler/testdata/userdata_additional_volume_only.golden.sh`
- **Verification:** `TestUserdataAdditionalVolumeOnly_GoldenByteIdentical` PASS after regeneration
- **Committed in:** `e93e872` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 — bug)
**Impact on plan:** Golden file regeneration is a normal maintenance action when the template grows. No scope creep.

## Issues Encountered

Plan 70-03 was executed before Plan 70-02 (wave scheduling). 70-03 had pre-emptively added a partial codex config.toml block and KM_AGENT to `userdata.go`. Task 2 completed and normalized the template to match CONTEXT.md exactly (full PermissionRequest + Stop TOML, correct statusMessage, install -d, chown/chmod). The golden file needed regeneration as a result.

## Pre-existing Test Failures (Out of Scope)

Six compiler tests were failing before Plan 70-02 ran (pre-existing, unrelated to this plan):
- `TestAuditHookNonBlocking`
- `TestGitHubUserDataGITASKPASS`
- `TestUserDataKMTracingServicectlStart`
- `TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime`
- `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`
- `TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock`

These are logged to `deferred-items.md` scope. Plan 70-02 did not introduce any new failures.

## Next Phase Readiness

- Plan 70-03 (km-notify-hook PermissionRequest + Stop fast-path): COMPLETE (executed before this plan)
- Plan 70-05 (Slack inbound poller dispatch fork): can now read `KM_AGENT` from `/etc/km/notify.env` (resolved at systemd start) and from `/etc/profile.d/km-notify-env.sh` (interactive shells)
- Plan 70-06 (Slack prefix routing): `KM_AGENT` in sandbox env provides the profile-level default; prefix parser overrides per-turn
- Plan 70-09 (UAT): `km doctor codex_hook_config_present` check can now verify the two hook entries in `~/.codex/config.toml` on live sandboxes

---
*Phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher*
*Completed: 2026-05-23*
