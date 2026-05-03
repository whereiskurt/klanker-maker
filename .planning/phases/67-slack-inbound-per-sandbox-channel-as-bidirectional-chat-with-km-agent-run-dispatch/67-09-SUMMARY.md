---
phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
plan: 09
subsystem: slack
tags: [slack, ssm, signing-secret, events-api, scope-check, km-slack-init]

# Dependency graph
requires:
  - phase: 63-slack-notify-hook
    provides: km slack init flow, SlackCmdDeps, SlackSSMStore, SlackInitAPI, rotate-token command
  - phase: 63.1-slack-notify-hook-gap-closure
    provides: RunSlackRotateToken pattern, BridgeColdStart helper
provides:
  - km slack init --signing-secret flag persisting /km/slack/signing-secret as KMS-encrypted SecureString
  - VerifyEventsAPIScopes helper for checking channels:history + groups:history
  - PersistSigningSecret exported helper
  - Events URL (/events) printed at end of init for Slack App Events Subscriptions
  - km slack rotate-signing-secret command mirroring rotate-token pattern
affects:
  - phase 67-10 (docs: operator runbook references new prompts + events URL)
  - Bridge Lambda (reads /km/slack/signing-secret at cold-start to validate event signatures)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "interactive vs non-interactive mode tracking via opts.BotToken == '' sentinel"
    - "PersistSigningSecret / VerifyEventsAPIScopes exported as thin testable helpers"
    - "km slack rotate-signing-secret mirrors rotate-token: 3 steps (persist, cold-start, confirm)"

key-files:
  created: []
  modified:
    - internal/app/cmd/slack.go
    - internal/app/cmd/slack_test.go

key-decisions:
  - "Interactive mode gated on opts.BotToken == '' — if bot token is pre-supplied (CI), signing secret is not prompted"
  - "PersistSigningSecret always uses alias/km-platform KMS key (not the bot-token KMS key) for simplicity and Phase 66 prefix compatibility"
  - "Scope verification warns only — does not fail init — because operator may not have added scopes yet"
  - "Events URL trim trailing slash before appending /events to handle bridge URLs with trailing /"
  - "rotate-signing-secret omits smoke test (no token to test) — just persist + cold-start + confirm"

patterns-established:
  - "interactive sentinel: track interactivity on opts.BotToken for downstream prompt decisions"
  - "Phase 67 inbound config is additive to Phase 63 install; existing operators run init again with --signing-secret"

requirements-completed: [REQ-SLACK-IN-INIT]

# Metrics
duration: 18min
completed: 2026-05-03
---

# Phase 67 Plan 09: km slack init Signing Secret + Scope Check + Events URL Summary

**km slack init gains --signing-secret flag persisting /km/slack/signing-secret as SecureString, scope verification warning on missing channels:history/groups:history, Events URL print, and a new km slack rotate-signing-secret rotation command**

## Performance

- **Duration:** ~18 min
- **Started:** 2026-05-03T00:12:00Z
- **Completed:** 2026-05-03T00:30:43Z
- **Tasks:** 1 (TDD)
- **Files modified:** 2

## Accomplishments
- `km slack init` now prompts for and persists the Slack App signing secret to `/km/slack/signing-secret` as a KMS-encrypted SSM SecureString
- Scope verification warns when `channels:history` or `groups:history` are missing from the bot's scopes, with exact fix instructions
- Events API URL (`<bridge-url>/events`) printed at end of init so operators know exactly what to paste into Slack App Events Subscriptions
- New `km slack rotate-signing-secret` command mirrors `rotate-token`: resolves secret (flag or prompt), persists to SSM, forces bridge cold-start
- 12 new tests covering all paths; all 27 Slack tests pass; `go build ./...` clean

## Task Commits

1. **Task 1: Extend km slack init with signing-secret + scope check + events URL** - `424271c` (feat)

## Files Created/Modified
- `internal/app/cmd/slack.go` - Added `SigningSecret` to `SlackInitOpts`, `SlackRotateSigningSecretOpts` struct, `interactive` mode tracking, signing secret prompt/persist step, scope warning step, events URL print step, `PersistSigningSecret` helper, `VerifyEventsAPIScopes` helper, `newSlackRotateSigningSecretCmd`, `RunSlackRotateSigningSecret`
- `internal/app/cmd/slack_test.go` - 12 new tests: `TestSlackInit_PersistsSigningSecret`, `TestSlackInit_ScopeCheck_AllPresent/MissingOne/MissingBoth`, `TestSlackInit_Events_URL_Format`, `TestSlackInit_WithSigningSecret_FlagProvided/Interactive/Force_WithSigningSecret_Overwrites`, `TestSlackRotateSigningSecret_HappyPath/Prompts`, `TestSlackCmd_Registered_RotateSigningSecret`

## Decisions Made
- Interactive mode tracked by `opts.BotToken == ""`: if bot token was pre-supplied (CI/non-interactive), skip the signing secret prompt to preserve backward compatibility of `TestSlackInit_NonInteractive_FlagsBypass`
- `PersistSigningSecret` uses `alias/km-platform` KMS key (same as platform key used elsewhere) rather than detecting the bot-token parameter's KMS key at runtime — avoids a DescribeParameters API call and works with Phase 66 resource prefix
- Scope verification is always a warning, never a failure: operator may not have added scopes yet and can add them before enabling inbound
- `rotate-signing-secret` intentionally has no smoke test — unlike `rotate-token`, there's no bridge smoke test that exercises the signing secret path (that's an inbound test requiring a live Slack event)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Removed unused `strings` import from create_slack_inbound_test.go**
- **Found during:** Task 1 (test compilation)
- **Issue:** `strings` imported but Go compiler flagged it as unused during test compilation; this was a pre-existing issue from Plan 67-06
- **Fix:** Re-investigated: `strings.Contains` IS used at line 285 of that file. The issue was that the import was temporarily removed and then restored. The file was correctly restored.
- **Files modified:** internal/app/cmd/create_slack_inbound_test.go (no net change)
- **Verification:** `go test -c ./internal/app/cmd/` succeeds
- **Committed in:** 424271c (included in task commit)

**2. [Rule 1 - Bug] Fixed double slash in events URL output**
- **Found during:** Task 1 verification (test output review)
- **Issue:** bridge URL in SSM ends with `/` (e.g. `https://bridge.lambda.example.com/`), causing `bridgeURL + "/events"` to produce `https://bridge.lambda.example.com//events`
- **Fix:** Use `strings.TrimRight(bridgeURL, "/") + "/events"` before printing
- **Files modified:** internal/app/cmd/slack.go
- **Verification:** Events URL format test passes; output shows single `/events` suffix
- **Committed in:** 424271c (included in task commit)

---

**Total deviations:** 2 auto-fixed (1 blocking - pre-existing import issue, 1 bug - double slash)
**Impact on plan:** Both fixes necessary for correctness. No scope creep.

## Issues Encountered

**Interactive mode gating:** The plan did not explicitly define the "non-interactive" sentinel for signing secret prompting. Adding signing secret as a mandatory prompt broke `TestSlackInit_NonInteractive_FlagsBypass`. Resolved by tracking interactivity via `opts.BotToken == ""` — if the session started with a pre-supplied bot token, it's treated as non-interactive throughout (consistent with CI usage).

## Operator Runbook: Upgrading Phase 63 Install to Phase 67 Inbound

Existing Phase 63 installs can add inbound capability without disturbing outbound config:

```bash
# Option A: one command with flag
km slack init --signing-secret <value-from-slack-app-basic-info>

# Option B: interactive (prompts for signing secret)
km slack init

# After running init, the output shows:
# Slack Events URL: https://<bridge-fn-url>/events
# Paste this into: Slack App config → Event Subscriptions → Request URL
# Enable bot events: message.channels, message.groups

# Rotate signing secret later:
km slack rotate-signing-secret --signing-secret <new-value>
```

Scope warnings appear when `channels:history` or `groups:history` are missing:
```
km slack init: WARNING — Slack App missing Events API scopes: channels:history, groups:history
  Add at: Slack App config → OAuth & Permissions → Bot Token Scopes
  Then re-install the app and run: km slack rotate-token --bot-token <new>
```

## Next Phase Readiness
- Signing secret infrastructure complete; bridge Lambda's `/events` handler (Plan 67-05) already reads `/km/slack/signing-secret` from SSM at cold-start
- Plan 67-10 (docs) can reference `--signing-secret`, `rotate-signing-secret`, and the events URL output format
- No blockers

---
*Phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch*
*Completed: 2026-05-03*
