---
phase: 62-claude-code-operator-notify-hook-for-permission-and-idle-events
plan: 02
subsystem: compiler
tags: [bash, heredoc, json-merge, userdata, claude-code-hooks, profile-compiler]

requires:
  - phase: 62-01
    provides: CLISpec with NotifyOnPermission/NotifyOnIdle/NotifyCooldownSeconds/NotificationEmailAddress fields

provides:
  - pkg/compiler/userdata.go unconditional km-notify-hook script heredoc (HOOK-01)
  - pkg/compiler/userdata.go conditional /etc/profile.d/km-notify-env.sh emission (HOOK-03)
  - pkg/compiler/userdata.go mergeNotifyHookIntoSettings() compile-time JSON merge (HOOK-02)
  - pkg/compiler/userdata_notify_test.go with 10 tests covering HOOK-01/02/03
  - pkg/compiler/testdata/notify-hook-stub-km-send.sh stub for Plan 03

affects:
  - 62-03 (hook script behavior tests consume the script and stub)
  - 62-04 (CLI flag plumbing reads the same env var names written here)

tech-stack:
  added: [encoding/json, fmt, strconv — added to userdata.go imports]
  patterns:
    - compile-time JSON merge (encoding/json) for settings.json hook injection
    - inline heredoc (KM_NOTIFY_HOOK_EOF) following pre-push hook pattern
    - conditional profile.d env file emission gated on Spec.CLI != nil

key-files:
  created:
    - pkg/compiler/userdata_notify_test.go
    - pkg/compiler/testdata/notify-hook-stub-km-send.sh
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go

key-decisions:
  - "Emit KM_NOTIFY_ON_PERMISSION and KM_NOTIFY_ON_IDLE whenever Spec.CLI != nil (not per-field); Go bool zero value + omitempty cannot distinguish explicit-false from unset"
  - "Use /etc/profile.d/km-notify-env.sh NOT /etc/environment; profile.d is guaranteed sourced in SSM sessions on Amazon Linux 2"
  - "mergeNotifyHookIntoSettings() runs at compile time in Go (not shell jq) for testability and to avoid missing-jq AMI issues"
  - "Hook script uses KM_NOTIFY_LAST_FILE env var as cooldown-file override (hardcoded /tmp/km-notify.last as default) to enable test isolation in Plan 03"
  - "Fixed TestKmSendAbsentWhenNoEmail to check for KMSEND heredoc marker (deploy) not any path mention (Rule 1 auto-fix)"

patterns-established:
  - "Compile-time JSON merge: read ConfigFiles[path], json.Unmarshal, mutate, json.MarshalIndent, write back — no shell jq"
  - "Unconditional hook script block after pre-push hook (section 4c), conditional env file inside {{ if .NotifyEnv }}"
  - "boolToZeroOne(b bool) string helper for env-var boolean encoding"

requirements-completed: [HOOK-01, HOOK-02, HOOK-03]

duration: 18min
completed: 2026-04-28
---

# Phase 62 Plan 02: Compiler Hook Script + Settings Merge Summary

**Compile-time drop of /opt/km/bin/km-notify-hook bash script and Claude Code settings.json hook merge, with /etc/profile.d/km-notify-env.sh emitted from spec.cli profile fields**

## Performance

- **Duration:** ~18 min
- **Started:** 2026-04-28T22:00:00Z
- **Completed:** 2026-04-28T22:18:00Z
- **Tasks:** 3 (Tasks 1+2 TDD, Task 3 testdata)
- **Files modified:** 4 (userdata.go, userdata_test.go, userdata_notify_test.go [new], testdata stub [new])

## Accomplishments

- Every sandbox user-data now unconditionally drops `/opt/km/bin/km-notify-hook` as a bash heredoc, following the pre-push hook pattern established in Phase 25. The script's runtime behavior is gated by env vars, not profile fields.
- `mergeNotifyHookIntoSettings()` performs a compile-time Go JSON merge of any user-supplied `~/.claude/settings.json`, appending km hook entries to `hooks.Notification` and `hooks.Stop` arrays while preserving user hooks. Fails fast with a descriptive error on invalid JSON.
- `/etc/profile.d/km-notify-env.sh` is written conditionally when `Spec.CLI != nil`, emitting `KM_NOTIFY_ON_PERMISSION`, `KM_NOTIFY_ON_IDLE`, and optionally `KM_NOTIFY_COOLDOWN_SECONDS` / `KM_NOTIFY_EMAIL`. Uses profile.d (not /etc/environment) for guaranteed sourcing in SSM sessions.
- `pkg/compiler/testdata/notify-hook-stub-km-send.sh` stub created for Plan 03 to test hook script behavior without sending real email.

## Task Commits

Each task was committed atomically:

1. **Tasks 1+2: Hook script + env emission + settings.json merge** - `0fa6919` (feat)
2. **Task 3: Stub km-send testdata** - `b98e6f4` (feat)

## Files Created/Modified

- `pkg/compiler/userdata.go` — Added `encoding/json`, `fmt`, `strconv` imports; `NotifyEnv` field on `userDataParams`; km-notify-hook heredoc (section 4c, unconditional); `{{- if .NotifyEnv }}` env file block; `boolToZeroOne()` helper; `mergeNotifyHookIntoSettings()` function; NotifyEnv population and settings.json merge wiring in `generateUserData()`
- `pkg/compiler/userdata_test.go` — Updated `TestKmSendAbsentWhenNoEmail` to check for `KMSEND` deploy marker not any km-send path mention (Rule 1 auto-fix)
- `pkg/compiler/userdata_notify_test.go` — New file with 10 tests (HOOK-01/02/03 positive and negative)
- `pkg/compiler/testdata/notify-hook-stub-km-send.sh` — Stub km-send for Plan 03 hook behavior tests

## Decisions Made

**1. Emit KM_NOTIFY_ON_PERMISSION/ON_IDLE whenever Spec.CLI != nil**

CONTEXT.md specifies "each variable is written iff its corresponding profile field is set (true OR false)." Go's `bool` zero value + `omitempty` YAML tag makes it impossible to distinguish "not configured in YAML" from "explicitly set to false in YAML" — they both parse to `false`. Pointer-based `*bool` would be more faithful to the spec but requires significant schema churn.

**Decision (v1 pragmatic):** Emit both `KM_NOTIFY_ON_PERMISSION` and `KM_NOTIFY_ON_IDLE` whenever `Spec.CLI != nil`, regardless of their values. Only omit them when the entire `cli` block is absent from the profile. `NotifyCooldownSeconds` and `NotificationEmailAddress` remain conditional (zero/empty = omit).

This means: `cli: {}` in a profile will write `KM_NOTIFY_ON_PERMISSION="0"` and `KM_NOTIFY_ON_IDLE="0"` (gated off, safe default). A profile with no `cli` block at all writes no env file. Tests `TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock` and `TestUserDataNotifyEnvVars_ExplicitFalseStillEmitsZero` document this behavior.

**2. /etc/profile.d/ instead of /etc/environment**

CONTEXT.md and the spec say `/etc/environment`. RESEARCH.md Pitfall 1 explains: SSM sessions on Amazon Linux 2 source `/etc/profile.d/` via `bash -l`, but `/etc/environment` is read by PAM (`pam_env`) which may not be invoked by the SSM agent. The entire codebase uses `/etc/profile.d/` for env var delivery. Writing to `/etc/environment` risks the vars being silently absent in SSM-launched Claude processes.

**Decision:** Use `/etc/profile.d/km-notify-env.sh`, following codebase convention. The hook script uses `${KM_NOTIFY_ON_PERMISSION:-0}` defaults so even if the env file is missing, behavior is safe (no-op). This is a spec deviation; documented here and noted in code comments.

**3. KM_NOTIFY_LAST_FILE override for test isolation (Plan 03 dependency)**

The hook script's cooldown state is stored in `/tmp/km-notify.last` by default. Plan 03 tests run concurrently and need isolated cooldown state. The script reads `last_file="${KM_NOTIFY_LAST_FILE:-/tmp/km-notify.last}"` so tests can set `KM_NOTIFY_LAST_FILE` to a per-test temp path.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed TestKmSendAbsentWhenNoEmail: too strict after hook script added km-send reference**

- **Found during:** Task 1 (running full compiler suite after GREEN implementation)
- **Issue:** Pre-existing test `TestKmSendAbsentWhenNoEmail` checked `strings.Contains(out, "/opt/km/bin/km-send")`. The new hook script body always references `/opt/km/bin/km-send` (to call it), so the test began failing even though km-send is NOT installed when SandboxEmail is empty. The test's intent was to verify km-send is not *deployed*, not that it's not *mentioned*.
- **Fix:** Changed the assertion to check for `cat > /opt/km/bin/km-send << 'KMSEND'` (the install heredoc marker) rather than any path mention. Updated comment to explain the distinction.
- **Files modified:** `pkg/compiler/userdata_test.go`
- **Verification:** `go test ./pkg/compiler/... -run TestKmSendAbsentWhenNoEmail` passes
- **Committed in:** `0fa6919` (combined with Tasks 1+2 feat commit)

**2. [Rule 1 - Bug] Removed backtick from template comment (Go raw string literal constraint)**

- **Found during:** Task 1 (first `go build` after adding template block)
- **Issue:** `userDataTemplate` is a Go raw string literal (backtick-delimited). A comment in the unconditional hook block contained `km shell --notify-*` with backtick backtick formatting, causing syntax error `unexpected name km after top level declaration`.
- **Fix:** Removed backtick formatting from the comment text.
- **Files modified:** `pkg/compiler/userdata.go`
- **Verification:** `go build ./...` passes
- **Committed in:** `0fa6919`

**3. [Rule 1 - Bug] Removed /etc/profile.d/km-notify-env.sh mention from unconditional comment**

- **Found during:** Task 1 (running tests in GREEN phase)
- **Issue:** `TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock` checks that the string `/etc/profile.d/km-notify-env.sh` does NOT appear in user-data when `Spec.CLI == nil`. The unconditional section 4c comment mentioned this path, causing the negative test to fail (the conditional block hadn't rendered, but the comment had).
- **Fix:** Replaced the path mention in the unconditional comment with generic wording ("written to the notify env file below").
- **Files modified:** `pkg/compiler/userdata.go`
- **Verification:** Test passes; `grep "km-notify-env.sh"` in rendered user-data for nil-CLI profile returns empty
- **Committed in:** `0fa6919`

---

**Total deviations:** 3 auto-fixed (3 Rule 1 bugs)
**Impact on plan:** All auto-fixes were necessary for correctness. No scope creep. Bug 1 and 3 were discovered during test verification (expected phase of TDD); Bug 2 was a Go raw-string literal constraint triggered by the template edit.

## Issues Encountered

- The `encoding/json`, `fmt`, and `strconv` imports were absent from `userdata.go`; added as part of implementation (standard deviation Rule 3 — missing import).
- `mergeNotifyHookIntoSettings` reuses the `err` variable already declared from `template.New(...).Parse(...)`. Go's `:=` with a new LHS variable (`mergedCF`) correctly reassigns the existing `err` without shadowing.

## Next Phase Readiness

- Plan 03 (hook script behavior tests) can now consume:
  - `/opt/km/bin/km-notify-hook` script (extracted from user-data via `generateUserData()`)
  - `pkg/compiler/testdata/notify-hook-stub-km-send.sh` stub km-send
  - `KM_NOTIFY_LAST_FILE` env var for cooldown test isolation
- Plan 04 (CLI flag plumbing) can read `KM_NOTIFY_ON_PERMISSION` / `KM_NOTIFY_ON_IDLE` env var names from the env file and inject them via SSM/agent run

---
*Phase: 62-claude-code-operator-notify-hook-for-permission-and-idle-events*
*Completed: 2026-04-28*
