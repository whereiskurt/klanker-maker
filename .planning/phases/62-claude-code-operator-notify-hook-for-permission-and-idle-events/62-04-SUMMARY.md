---
phase: 62-claude-code-operator-notify-hook-for-permission-and-idle-events
plan: "04"
subsystem: cli
tags: [cobra, notify, km-shell, km-agent, ssm, sendcommand, hooks, phase62]

# Dependency graph
requires:
  - phase: 62-01
    provides: CLISpec.NotifyOnPermission, CLISpec.NotifyOnIdle fields in pkg/profile/types.go
provides:
  - "--notify-on-permission / --no-notify-on-permission flags on km shell and km agent run"
  - "--notify-on-idle / --no-notify-on-idle flags on km shell and km agent run"
  - "AgentRunOptions.NotifyOnPermission *bool and NotifyOnIdle *bool for per-invocation override"
  - "BuildAgentShellCommands emits export KM_NOTIFY_ON_* lines when pointers are non-nil"
  - "resolveNotifyFlags() and buildNotifySendCommands() pure-function helpers in shell.go"
  - "SSM SendCommand writes /etc/profile.d/zz-km-notify.sh before km shell session; deferred cleanup removes it"
affects:
  - 62-03
  - 62-05

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Positive + negative flag pair pattern (--notify-on-X / --no-notify-on-X) mirrors --no-bedrock idiom"
    - "*bool for optional CLI override: nil = unset (profile.d default), non-nil = explicit"
    - "SSM SendCommand write + deferred cleanup bracket pattern (mirrors zz-km-no-bedrock.sh)"
    - "Pure helper functions for flag resolution and shell command building (no AWS dependencies, fully unit-testable)"

key-files:
  created:
    - internal/app/cmd/shell_notify_test.go
  modified:
    - internal/app/cmd/agent.go
    - internal/app/cmd/agent_test.go
    - internal/app/cmd/shell.go

key-decisions:
  - "Use *bool (not bool) for AgentRunOptions.NotifyOnPermission and NotifyOnIdle: nil = unset (no env line emitted), non-nil = emit KM_NOTIFY_ON_*=0|1. Avoids explicit-false ambiguity without a separate Explicit field."
  - "km shell: return nil from resolveNotifyFlags when no CLI flag changed (avoids pointless SSM SendCommand — profile.d km-notify-env.sh from Plan 02 supplies defaults)"
  - "notifyEnvLines inserted AFTER noBedrockLines and BEFORE KM_ARTIFACTS_BUCKET in the BuildAgentShellCommands template, ensuring exports precede agent invocation"
  - "runShell signature changed from variadic flags ...bool to explicit asRoot/noBedrock bool + notifyPerm/notifyIdle *bool; single caller only"

patterns-established:
  - "resolveNotifyFlags(cmd): reads cobra Changed() for positive and negative flags, returns *bool or nil — reusable pattern for future bool-override CLI flags"
  - "buildNotifySendCommands(perm, idle *bool): pure function, no AWS/SSM deps, returns (write, cleanup) slices for SSM RunShellScript — fully unit-testable without mocks"

requirements-completed: [HOOK-04]

# Metrics
duration: 12min
completed: 2026-04-28
---

# Phase 62 Plan 04: CLI Flag Wiring for Notify Gates Summary

**km shell and km agent run gain symmetric --notify-on-permission/idle flag pairs that inject KM_NOTIFY_ON_* env vars via SSM SendCommand (shell) or export lines in the tmux script (agent run)**

## Performance

- **Duration:** 12 min
- **Started:** 2026-04-28T18:05:27Z
- **Completed:** 2026-04-28T22:11:04Z
- **Tasks:** 3
- **Files modified:** 4 (+ 1 created)

## Accomplishments

- All four flag pairs (`--notify-on-permission`, `--no-notify-on-permission`, `--notify-on-idle`, `--no-notify-on-idle`) wired symmetrically on both `km shell` and `km agent run`
- `AgentRunOptions.NotifyOnPermission *bool` / `NotifyOnIdle *bool` added; nil = no env line emitted (profile.d default applies), non-nil = emit `KM_NOTIFY_ON_*="0"|"1"` in the tmux script
- SSM `SendCommand` pattern mirrors existing `zz-km-no-bedrock.sh`: write `/etc/profile.d/zz-km-notify.sh` before session, deferred cleanup after
- 12 unit tests covering all notify paths (6 for agent, 6 for shell helpers) — all pure functions, no SSM mocking needed

## Task Commits

Each task was committed atomically:

1. **Task 1: Wire notify flags on km agent run + AgentRunOptions extension** - `d422402` (feat)
2. **Task 2: Wire notify flags on km shell + resolveNotifyFlags/buildNotifySendCommands helpers** - `c92ed1d` (feat)
3. **Task 3: Build binary + smoke test --help outputs** - `4143d3a` (chore)

## Files Created/Modified

- `internal/app/cmd/agent.go` - AgentRunOptions extended with *bool fields; notifyEnvLines stanza in BuildAgentShellCommands; 4 flag declarations + resolution logic in newAgentRunCmd; runAgentNonInteractive signature extended
- `internal/app/cmd/agent_test.go` - 6 BuildAgentShellCommands_Notify* tests added (agentTestBoolPtr helper)
- `internal/app/cmd/shell.go` - resolveNotifyFlags() and buildNotifySendCommands() pure helpers; 4 flag declarations; runShell signature changed to explicit params; execSSMSession extended with notify SendCommand block
- `internal/app/cmd/shell_notify_test.go` (new) - 6 pure-function unit tests in package cmd (internal): BothNil, PermissionOnly, BothExplicit, NoneChanged, PositiveOnly, NegativeOverridesProfile

## Decisions Made

- `*bool` for `AgentRunOptions.NotifyOnPermission` and `NotifyOnIdle`: nil = unset (no env line emitted), non-nil = emit `KM_NOTIFY_ON_*="0"|"1"`. This cleanly represents three states (unset, explicit-true, explicit-false) without a companion `Explicit bool` field.
- `resolveNotifyFlags` returns `nil` when no CLI flag was changed — deliberately avoids an SSM `SendCommand` roundtrip when the profile.d file from Plan 02 already supplies the defaults.
- `notifyEnvLines` is inserted after `noBedrockLines` and before `KM_ARTIFACTS_BUCKET` in the tmux script template, guaranteeing exports precede the agent invocation.
- `runShell` variadic `flags ...bool` refactored to explicit `asRoot, noBedrock bool` + `notifyPerm, notifyIdle *bool` — only one caller, no migration cost.

## Deviations from Plan

None - plan executed exactly as written.

The `loadProfileCLI` helper already existed in `agent.go` (from an earlier refactor before this plan ran), so the plan's instruction to "add it if it doesn't exist" was a no-op — no deviation required.

## Issues Encountered

Three pre-existing test failures (`TestShellCmd_StoppedSandbox`, `TestShellCmd_UnknownSubstrate`, `TestShellCmd_MissingInstanceID`) were present before this plan began (confirmed via git stash check). These are out-of-scope per deviation scope boundary rules — only issues directly caused by this plan's changes are fixed.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- HOOK-04 complete: `km shell` and `km agent run` both expose all four notify gate flags
- The hook script (Plan 02) and its tests (Plan 03) can now gate on `KM_NOTIFY_ON_PERMISSION` / `KM_NOTIFY_ON_IDLE` set by these CLI flags
- Binary builds cleanly via `make build`; `km shell --help` and `km agent run --help` both list all 4 flag pairs

---
*Phase: 62-claude-code-operator-notify-hook-for-permission-and-idle-events*
*Completed: 2026-04-28*
