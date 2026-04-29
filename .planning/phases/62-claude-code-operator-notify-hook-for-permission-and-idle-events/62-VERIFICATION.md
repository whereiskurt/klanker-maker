---
phase: 62-claude-code-operator-notify-hook-for-permission-and-idle-events
verified: 2026-04-26T00:00:00Z
status: passed
score: 5/5 must-haves verified
---

# Phase 62: Claude Code Operator-Notify Hook Verification Report

**Phase Goal:** Claude Code agents running on km sandboxes emit signed emails to the operator (or a profile-specified override address) when they need permission to use a tool (`Notification` hook event) or finish a turn and are waiting for further input (`Stop` hook event). Behavior is controlled by four new `spec.cli` profile fields; `km shell` and `km agent run` gain flag overrides. The hook script is wired into `~/.claude/settings.json` at compile time.
**Verified:** 2026-04-26
**Status:** PASSED
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Every sandbox user-data unconditionally includes `/opt/km/bin/km-notify-hook` script | VERIFIED | `pkg/compiler/userdata.go` lines 354-428: heredoc writes `/opt/km/bin/km-notify-hook` outside any template conditional — executes for all profiles regardless of CLI block presence |
| 2 | Hook script gates on `KM_NOTIFY_ON_PERMISSION`/`KM_NOTIFY_ON_IDLE` env vars, never blocks Claude on failure, and uses `--body <file>` not stdin | VERIFIED | `userdata.go:363-427`: `set -euo pipefail`, case gate at lines 369-372, `|| echo ""` fallback at line 401, `--body "$body_file"` at line 421, `exit 0` at line 427 |
| 3 | Compiler merges km-notify-hook entries into `~/.claude/settings.json` for every sandbox, preserving user-supplied hooks | VERIFIED | `mergeNotifyHookIntoSettings()` at lines 2265-2318: appends to `hooks.Notification` and `hooks.Stop` arrays unconditionally; preserves existing entries; fails fast on invalid JSON |
| 4 | Profile-derived env vars are written to `/etc/profile.d/km-notify-env.sh` (not `/etc/environment`) when `spec.cli` block is present | VERIFIED | `userdata.go:432-449`: template conditional `{{- if .NotifyEnv }}` emits the profile.d file; `generateUserData` at lines 2432-2443 populates `params.NotifyEnv` when `p.Spec.CLI != nil` |
| 5 | `km shell` and `km agent run` honor 4 CLI flags (`--notify-on-permission`, `--no-notify-on-permission`, `--notify-on-idle`, `--no-notify-on-idle`) with correct override semantics | VERIFIED | `shell.go:193-196`: 4 flag declarations; `resolveNotifyFlags()` at lines 69-85; `buildNotifySendCommands()` at lines 93-119; `agent.go:276-279`: 4 flag declarations; `AgentRunOptions` with `NotifyOnPermission *bool` and `NotifyOnIdle *bool` at lines 1152-1153; `notifyEnvLines` stanza at lines 1208-1228 |

**Score:** 5/5 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/profile/types.go` | 4 new CLISpec fields with correct types | VERIFIED | Lines 379-395: `NotifyOnPermission bool`, `NotifyOnIdle bool`, `NotifyCooldownSeconds int`, `NotificationEmailAddress string` — all with `omitempty` yaml tags |
| `pkg/profile/schemas/sandbox_profile.schema.json` | 4 schema entries under `cli` with correct types and `additionalProperties: false` preserved | VERIFIED | Lines 477-514: `cli` object has `additionalProperties: false`; `notifyOnPermission` (boolean), `notifyOnIdle` (boolean), `notifyCooldownSeconds` (integer, minimum: 0), `notificationEmailAddress` (string) |
| `pkg/compiler/userdata.go` | Hook script heredoc + env file emission + settings.json merge | VERIFIED | Lines 354-454 (hook + env file); `mergeNotifyHookIntoSettings()` at 2265-2318; invoked unconditionally at lines 2450-2454 |
| `pkg/compiler/userdata_notify_test.go` | HOOK-01/02/03 compiler tests | VERIFIED | 10 test functions covering hook presence, env var emission for all field combinations, settings.json merge with/without user content, invalid JSON fail-fast |
| `pkg/compiler/notify_hook_script_test.go` | HOOK-05 hook script runtime tests including regression test | VERIFIED | 8 test functions: `TestNotifyHook_GatedOff`, `TestNotifyHook_Notification`, `TestNotifyHook_Notification_RecipientOverride`, `TestNotifyHook_Stop`, `TestNotifyHook_Stop_MalformedTranscript_StillExitsZero` (regression), `TestNotifyHook_Cooldown`, `TestNotifyHook_SendFailure_StillExitsZero`, `TestNotifyHook_BodyViaFile_NotStdin` |
| `internal/app/cmd/shell.go` | 4 flag declarations + `resolveNotifyFlags()` + `buildNotifySendCommands()` + SSM SendCommand wiring | VERIFIED | Flags at lines 193-196; helpers at lines 69-119; SSM wiring at lines 318-340 |
| `internal/app/cmd/shell_notify_test.go` | Shell helper pure-function tests | VERIFIED | 6 tests: `TestBuildNotifySendCommands_BothNil`, `TestBuildNotifySendCommands_PermissionOnly`, `TestBuildNotifySendCommands_BothExplicit`, `TestResolveNotifyFlags_NoneChanged`, `TestResolveNotifyFlags_PositiveOnly`, `TestResolveNotifyFlags_NegativeOverridesProfile` |
| `internal/app/cmd/agent.go` | 4 flag declarations + `AgentRunOptions` with `*bool` fields + `notifyEnvLines` stanza | VERIFIED | Flags at lines 276-279; `NotifyOnPermission *bool` and `NotifyOnIdle *bool` at lines 1152-1153; stanza at lines 1208-1228 |
| `internal/app/cmd/agent_test.go` | 6 agent notify gate tests | VERIFIED | Lines 1205-1311: `TestBuildAgentShellCommands_NotifyOnPermission`, `_NotifyOnIdle`, `_NotifyBoth`, `_NotifyNeitherEmitsNoEnv`, `_NotifyExplicitFalse`, `_NotifyOrderingBeforeAgentLaunch` |
| `profiles/notify-test.yaml` | UAT profile with `notifyOnPermission: true`, `notifyOnIdle: true` | VERIFIED | File exists at `profiles/notify-test.yaml` (2889 bytes); lines 109-111 confirm both flags set to true |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `userdata.go` hook heredoc | Runtime `/opt/km/bin/km-notify-hook` | Written unconditionally by user-data bootstrap | WIRED | Lines 350-429: `mkdir -p /opt/km/bin`, `cat > /opt/km/bin/km-notify-hook << 'KM_NOTIFY_HOOK_EOF'`, `chmod +x` — no conditional wrapper |
| `userdata.go` `mergeNotifyHookIntoSettings()` | `~/.claude/settings.json` on sandbox | Called unconditionally at `generateUserData` lines 2450-2454 | WIRED | Returns updated `configFiles` map; result assigned to `params.ConfigFiles` which the template writes to disk |
| `p.Spec.CLI.NotifyOnPermission` → `params.NotifyEnv` | `/etc/profile.d/km-notify-env.sh` | `generateUserData` lines 2432-2443; template `{{- if .NotifyEnv }}` block | WIRED | `boolToZeroOne()` helper converts bool to "0"/"1"; conditional on `p.Spec.CLI != nil` |
| `km shell --notify-on-permission` | `/etc/profile.d/zz-km-notify.sh` via SSM SendCommand | `resolveNotifyFlags()` → `buildNotifySendCommands()` → `execSSMSession` SSM call | WIRED | shell.go lines 318-340: SendCommand writes zz-km-notify.sh, deferred cleanup removes it |
| `km agent run --notify-on-idle` | `export KM_NOTIFY_ON_IDLE="1"` in tmux script | `AgentRunOptions.NotifyOnIdle` → `notifyEnvLines` → `BuildAgentShellCommands` script template | WIRED | agent.go line 1252: `notifyEnvLines` is the 2nd `%s` substitution, positioned before `agentLine` |
| Hook script gate env vars | km-send invocation (or silent exit) | Runtime `KM_NOTIFY_ON_PERMISSION`/`KM_NOTIFY_ON_IDLE` case gate | WIRED | userdata.go lines 368-372; verified in `TestNotifyHook_GatedOff` and `TestNotifyHook_Notification` |
| `KM_NOTIFY_LAST_FILE` / `KM_NOTIFY_COOLDOWN_SECONDS` | Cooldown suppress path | `/tmp/km-notify.last` timestamp check | WIRED | userdata.go lines 375-381; regression test `TestNotifyHook_Cooldown` |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| HOOK-01 | 62-02 | Compiler unconditionally writes `/opt/km/bin/km-notify-hook` | SATISFIED | `userdata.go` heredoc at lines 354-428; `TestUserDataNotifyHookAlwaysPresent` verifies with `Spec.CLI = nil` |
| HOOK-02 | 62-02 | Compiler merges Notification/Stop hook entries into `~/.claude/settings.json` | SATISFIED | `mergeNotifyHookIntoSettings()` at lines 2265-2318; 4 tests in `userdata_notify_test.go` covering no-user-settings, user-hook preservation, non-hooks key preservation, invalid JSON fail-fast |
| HOOK-03 | 62-02 | Compiler writes `/etc/profile.d/km-notify-env.sh` from profile `spec.cli.notify*` fields | SATISFIED | Template block at lines 432-449; Go logic at lines 2425-2443; 5 tests: `NoneSet_NoEnvBlock`, `PermissionOnly`, `IdleAndCooldown`, `RecipientOverride`, `ExplicitFalseStillEmitsZero` |
| HOOK-04 | 62-04 | `km shell` and `km agent run` honor 4 notify CLI flags | SATISFIED | `shell.go` helpers + SSM SendCommand wiring; `agent.go` `AgentRunOptions *bool` fields + `notifyEnvLines`; 6 shell helper tests + 6 agent script tests; all green |
| HOOK-05 | 62-03 | Hook script honors gate vars, cooldown, correct subjects/bodies, `--body <file>`, never blocks Claude | SATISFIED | 8 runtime tests including malformed-transcript regression test `TestNotifyHook_Stop_MalformedTranscript_StillExitsZero`; UAT T3-T7 confirmed live SES delivery with 5 distinct MessageIds |

### Anti-Patterns Found

None. The following were checked and confirmed clean:

- No `TODO`, `FIXME`, `PLACEHOLDER` in Phase 62 source files
- No `return null`/empty stub returns in the hook script or merge function
- `set -euo pipefail` is present in the hook script; the critical Stop-path has `|| echo ""` fallback so pipefail cannot propagate from jq errors (Rule 1 fix verified in commits `095a51e` + `9c0690c`)
- No `console.log`-only implementations; all hooks actually call km-send or gate silently

### Human Verification Required

The following were verified live by operator during Phase 62-05 UAT and do not require re-verification:

1. **SES delivery confirmed** — T3-T7 each produced a distinct SES MessageId. Operator inbox received correct subject lines (`[nt-5cd75540] needs permission`, `[nt-0f0d2906] idle`) and correct body content. Ed25519 signing confirmed on send side.

2. **Cooldown coalescing** — T6 confirmed that a second fire within 60 seconds produced no km-send invocation; `/tmp/km-notify.last` timestamp was written only on fire 1.

3. **CLI flag A/B test** — T7 confirmed `KM_NOTIFY_ON_PERMISSION=0` silences the hook and `KM_NOTIFY_ON_PERMISSION=1` restores it, using identical hook binary and identical payload.

Items that are inherently human-only (cannot be verified programmatically):

| Test | What to do | Expected | Why human |
|------|-----------|----------|-----------|
| Real Claude Notification event | Provision sandbox without `--dangerously-skip-permissions`, wait for permission prompt | Email arrives at operator inbox with subject `[<id>] needs permission` | `km agent run` uses `--dangerously-skip-permissions` by default; manual hook fire exercised this in UAT instead |

### Gaps Summary

No gaps found. All five requirements (HOOK-01 through HOOK-05) are fully satisfied:

- The hook script is unconditionally emitted and correctly gated at runtime.
- The Rule 1 bug (jq exit-5 propagation in Stop path) was fixed and regression-tested before phase close.
- The settings.json merge is unconditional and preserves user-supplied hooks.
- The `/etc/profile.d/km-notify-env.sh` emission is correctly conditional on `spec.cli` presence.
- CLI flags on both `km shell` and `km agent run` are wired with correct `*bool` semantics, pure-function helpers, and full unit test coverage.
- The test suite (8 hook-script tests + 10 compiler tests + 6 shell helper tests + 6 agent script tests = 30 notify-specific tests) passes in full.

**One notable deviation from CONTEXT.md (accepted, documented):**
The CONTEXT.md originally specified `/etc/environment` for the env file. Implementation uses `/etc/profile.d/km-notify-env.sh` per codebase convention. This is documented in ROADMAP.md Phase 62 goal text, STATE.md decisions, and all SUMMARY files. The behavior is correct for the target environment (Amazon Linux 2 SSM sessions).

**One accepted v1 limitation (documented):**
When `spec.cli` block is present but both booleans are `false`, the compiler emits `KM_NOTIFY_ON_PERMISSION="0"` and `KM_NOTIFY_ON_IDLE="0"` rather than emitting no variables. This is due to Go's bool zero value + `omitempty` YAML tag making explicit-false indistinguishable from unset. The runtime effect is identical to the unset case (hook exits 0 immediately). Documented in STATE.md and `TestUserDataNotifyEnvVars_ExplicitFalseStillEmitsZero`.

---

_Verified: 2026-04-26_
_Verifier: Claude (gsd-verifier)_
