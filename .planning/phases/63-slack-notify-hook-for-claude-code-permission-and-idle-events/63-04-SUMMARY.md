---
phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events
plan: "04"
subsystem: compiler
tags: [slack, notifications, userdata, heredoc, bash, multi-channel, sent_any]

# Dependency graph
requires:
  - phase: 63-01
    provides: "*bool CLISpec fields NotifyEmailEnabled, NotifySlackEnabled, NotifySlackChannelOverride in pkg/profile/types.go"
  - phase: 62-02
    provides: "km-notify-hook heredoc + NotifyEnv emission framework in pkg/compiler/userdata.go"
provides:
  - "sent_any multi-channel dispatch in km-notify-hook heredoc (email + Slack branches, shared cooldown)"
  - "KM_NOTIFY_EMAIL_ENABLED, KM_NOTIFY_SLACK_ENABLED, KM_SLACK_CHANNEL_ID emitted in /etc/profile.d/km-notify-env.sh with *bool pointer semantics"
  - "KM_SLACK_BRIDGE_URL documented as intentionally absent (Plan 08 runtime injection)"
  - "10 new tests covering Slack-on/off, nil pointer, channel override baking, Phase 62 regression"
affects:
  - "63-05 (km-slack binary must exist at /opt/km/bin/km-slack for hook to call it)"
  - "63-08 (km create injects KM_SLACK_CHANNEL_ID and KM_SLACK_BRIDGE_URL at runtime)"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "sent_any=0 multi-channel dispatch with per-channel success tracking before shared cooldown update"
    - "*bool pointer semantics for *bool fields: nil=no env var emitted (hook default), non-nil=emit KEY=0|1"
    - "Compile-time channel pinning via NotifySlackChannelOverride; runtime injection for remaining keys"

key-files:
  created: []
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_notify_test.go

key-decisions:
  - "KM_SLACK_BRIDGE_URL not emitted at compile time — requires runtime SSM lookup of /km/slack/bridge-url; Plan 08 injects it post-launch into /etc/profile.d/km-notify-env.sh"
  - "Email branch defaults to enabled via KM_NOTIFY_EMAIL_ENABLED:-1 so Phase 62 profiles (nil NotifyEmailEnabled) behave identically"
  - "Slack branch defense-in-depth: gated on BOTH enabled=1 AND non-empty KM_SLACK_CHANNEL_ID to prevent km-slack call with empty --channel"

patterns-established:
  - "sent_any pattern: initialize sent_any=0, set to 1 in each branch on success, gate cooldown on [[ $sent_any -eq 1 ]]"
  - "*bool compiler emission: nil pointer = no env var; deref = boolToZeroOne (reuses Phase 62 helper)"

requirements-completed: [SLCK-02]

# Metrics
duration: 2min
completed: 2026-04-30
---

# Phase 63 Plan 04: km-notify-hook Multi-Channel Dispatch Summary

**km-notify-hook heredoc extended with sent_any parallel email+Slack dispatch; compiler NotifyEnv emission wired to *bool CLISpec fields with Phase 62 backward compat preserved**

## Performance

- **Duration:** 2 min
- **Started:** 2026-04-30T01:24:13Z
- **Completed:** 2026-04-30T01:26:10Z
- **Tasks:** 1
- **Files modified:** 2

## Accomplishments
- Replaced single-path km-send dispatch in the km-notify-hook heredoc with `sent_any=0` multi-channel pattern supporting email and Slack branches
- Extended NotifyEnv population to emit `KM_NOTIFY_EMAIL_ENABLED`, `KM_NOTIFY_SLACK_ENABLED`, `KM_SLACK_CHANNEL_ID` honoring `*bool` nil=unset semantics from Plan 01
- Documented `KM_SLACK_BRIDGE_URL` as intentionally absent at compile time (SSM runtime injection deferred to Plan 08)
- Added 10 new tests; all 26 tests in notify/km-send suites pass; full compiler package green

## Task Commits

1. **Task 1: Extend km-notify-hook heredoc + NotifyEnv for sent_any multi-channel dispatch** - `a197738` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified
- `pkg/compiler/userdata.go` — Heredoc dispatch block replaced (~lines 418-428 old → ~lines 418-447 new); NotifyEnv population extended with 3 new conditional keys (~lines 2425-2460)
- `pkg/compiler/userdata_notify_test.go` — 10 new tests appended for Phase 63 (plus helper `profileWithSlack`)

## New Env Keys

| Key | Condition | Default in hook |
|-----|-----------|-----------------|
| `KM_NOTIFY_EMAIL_ENABLED` | `CLISpec.NotifyEmailEnabled != nil` | `:-1` (email on) |
| `KM_NOTIFY_SLACK_ENABLED` | `CLISpec.NotifySlackEnabled != nil` | `:-0` (Slack off) |
| `KM_SLACK_CHANNEL_ID` | `CLISpec.NotifySlackChannelOverride != ""` | absent (Plan 08 injects) |
| `KM_SLACK_BRIDGE_URL` | never at compile time | absent (Plan 08 injects) |

## sent_any Dispatch Shape

```bash
sent_any=0
if [[ "${KM_NOTIFY_EMAIL_ENABLED:-1}" == "1" ]]; then
  # km-send branch — Phase 62 macOS empty-array pattern preserved
  if /opt/km/bin/km-send ... ; then sent_any=1; fi
fi
if [[ "${KM_NOTIFY_SLACK_ENABLED:-0}" == "1" && -n "${KM_SLACK_CHANNEL_ID:-}" ]]; then
  if /opt/km/bin/km-slack post ... ; then sent_any=1; fi
fi
[[ $sent_any -eq 1 ]] && date +%s > "$last_file"
rm -f "$body_file"
exit 0
```

Preserves both Phase 62 invariants:
- Hook never blocks Claude (`exit 0` unconditional final line)
- Cooldown updates only on success (`$sent_any -eq 1` guard)

## New Tests (10 total)

| Test | What it asserts |
|------|-----------------|
| `TestUserDataNotifyHook_HasSentAnyDispatch` | Heredoc contains `sent_any=0`, `KM_NOTIFY_EMAIL_ENABLED:-1`, `KM_NOTIFY_SLACK_ENABLED:-0`, `[[ $sent_any -eq 1 ]]`, `/opt/km/bin/km-slack post` |
| `TestUserDataNotifyEnv_SlackEnabledTrue` | `notifySlackEnabled: true` → `KM_NOTIFY_SLACK_ENABLED="1"` in env file |
| `TestUserDataNotifyEnv_SlackEnabledFalse` | `notifySlackEnabled: false` → `KM_NOTIFY_SLACK_ENABLED="0"` |
| `TestUserDataNotifyEnv_SlackEnabledNilPointer` | nil both fields → neither key emitted |
| `TestUserDataNotifyEnv_EmailEnabledFalse` | `notifyEmailEnabled: false` → `KM_NOTIFY_EMAIL_ENABLED="0"` |
| `TestUserDataNotifyEnv_ChannelOverrideBaked` | `notifySlackChannelOverride: "C0123ABC"` → `KM_SLACK_CHANNEL_ID="C0123ABC"` |
| `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID` | no override → `KM_SLACK_CHANNEL_ID=` absent |
| `TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime` | `KM_SLACK_BRIDGE_URL=` absent in all cases |
| `TestUserDataNotifyHook_Phase62Profile_NoRegression` | Phase 62-only profile → no `KM_SLACK_*="..."` in env file; Phase 62 keys still emitted |
| `TestUserDataNotifyHook_HookScriptStillExitsZero` | Last line of heredoc body is `exit 0` |

## Decisions Made

- **KM_SLACK_BRIDGE_URL deferred to runtime (Plan 08):** The bridge URL is stored in SSM at `/km/slack/bridge-url` and varies by AWS account/region. Emitting it at compile time would require the compiler to call SSM during user-data generation, coupling a pure Go function to AWS. Plan 08 appends it to `/etc/profile.d/km-notify-env.sh` post-launch via the same runtime env-injection mechanism.
- **Email default-on via `:-1`:** Phase 62 profiles have nil `NotifyEmailEnabled`. The `:-1` default in the hook means email is on for all Phase 62 profiles without any env var being emitted — byte-for-byte env file compat.
- **Slack channel double-gate:** Both `SLACK_ENABLED=1` AND non-empty `CHANNEL_ID` are required before calling `km-slack`. This prevents the binary from receiving an empty `--channel` argument if the runtime injection (Plan 08) hasn't run yet.

## Deviations from Plan

None — plan executed exactly as written. The existing Phase 62 test `TestUserDataNotifyHookAlwaysPresent` checked for `/opt/km/bin/km-send` which is still present in the new email branch; no assertion updates were needed.

## Issues Encountered

None.

## Next Phase Readiness

- Plan 05 (km-slack binary): the heredoc now references `/opt/km/bin/km-slack post --channel ... --subject ... --body ...`; Plan 05 must produce a binary at that path with those flags.
- Plan 08 (km create): must append `KM_SLACK_CHANNEL_ID` and `KM_SLACK_BRIDGE_URL` to `/etc/profile.d/km-notify-env.sh` post-launch for sandboxes without a compile-time channel override.
- No blockers for either downstream plan.

---
*Phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events*
*Completed: 2026-04-30*
