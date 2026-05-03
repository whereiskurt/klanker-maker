---
phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
plan: "04"
subsystem: compiler
tags: [sqs, slack, systemd, bash, poller, userdata, ec2-bootstrap, km-agent-run]

requires:
  - phase: 67-01
    provides: NotifySlackInboundEnabled field on CLISpec in pkg/profile/types.go
  - phase: 67-02
    provides: GetSlackThreadsTableName() on Config, SlackThreadsTableName field in config.go

provides:
  - "km-slack-inbound-poller bash script inline heredoc in userdata.go (conditional on SlackInboundEnabled)"
  - "/etc/systemd/system/km-slack-inbound-poller.service with EnvironmentFile=/etc/profile.d/km-notify-env.sh"
  - "KM_SLACK_THREADS_TABLE and KM_SLACK_INBOUND_QUEUE_URL exported in /etc/profile.d/km-notify-env.sh when inbound enabled"
  - "systemctl enable/start lines extended with km-slack-inbound-poller conditional"
  - "km-notify-hook --thread pass-through: reads KM_SLACK_THREAD_TS, passes to km-slack post (Phase 63 flag finally consumed)"
  - "5 passing compiler tests in userdata_slack_inbound_test.go"

affects:
  - 67-06-km-create-sqs-queue-and-env-injection
  - future-phases-using-KM_SLACK_INBOUND_QUEUE_URL
  - pkg/compiler/userdata.go consumers

tech-stack:
  added: []
  patterns:
    - "Inline bash heredoc for systemd service scripts in userdata.go (mirrors km-mail-poller)"
    - "EnvironmentFile=/etc/profile.d/km-notify-env.sh in systemd units for runtime-injected env vars"
    - "SQS ChangeMessageVisibility 300s before km agent run to prevent re-delivery (Pitfall 1 pattern)"
    - "KM_SLACK_THREAD_TS env var as thread context carrier from poller to notify-hook"

key-files:
  created:
    - pkg/compiler/userdata_slack_inbound_test.go
  modified:
    - pkg/compiler/userdata.go

key-decisions:
  - "Compile-time slot for KM_SLACK_INBOUND_QUEUE_URL with empty value — km create (Plan 67-06) overwrites at runtime via SSM SendCommand"
  - "SlackThreadsTableName read from KM_SLACK_THREADS_TABLE env var in generateUserData (no Config parameter threading needed — mirrors budgetTable pattern)"
  - "EnvironmentFile=/etc/profile.d/km-notify-env.sh in systemd unit (option b from plan) — cleanest, runtime-safe"
  - "Added TestUserdata_SlackInboundThreadFlag as fifth test beyond the plan's four stubs — covers --thread pass-through directly"
  - "ttl_expiry as Number (Unix epoch + 30*24*3600) written by poller for DDB TTL per RESEARCH.md Open Question 3"

patterns-established:
  - "Pattern: Bash poller script as inline heredoc in userdata.go gated on profile bool field"
  - "Pattern: systemd EnvironmentFile pointing to /etc/profile.d/km-notify-env.sh for runtime-injected values"

requirements-completed:
  - REQ-SLACK-IN-POLLER

duration: 4min
completed: "2026-05-03"
---

# Phase 67 Plan 04: Userdata Compiler Slack Inbound Extension Summary

**Bash poller script + systemd unit inline in userdata.go for SQS-driven Claude dispatch, with --thread pass-through wiring finally consuming the Phase 63 unused flag**

## Performance

- **Duration:** ~4 min
- **Started:** 2026-05-03T00:01:46Z
- **Completed:** 2026-05-03T00:05:46Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- Extended `pkg/compiler/userdata.go` with six additive edits: poller heredoc, systemd unit, env var exports, systemctl enable extensions, notify-hook --thread wiring, and profile-to-template field wiring
- The km-slack-inbound-poller bash script implements the full SQS poll → ChangeMessageVisibility 300s → DDB session lookup → sudo -u sandbox claude → DDB write-back with ttl_expiry → DeleteMessage flow
- Replaced all four Wave 0 test stubs with real assertions; added a fifth test for --thread pass-through coverage
- `make build`, `go build ./...`, and `go test ./pkg/compiler/... -count=1` all clean

## Task Commits

1. **Task 1: Extend userdata.go (6 edits)** - `fa43f14` (feat)
2. **Task 2: Implement compiler tests** - `668b08c` (test)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go` - Six additive edits (183 lines inserted): poller heredoc at ~line 862, systemd unit at ~line 1245, systemctl enable extensions at ~line 1809/1813, notify-hook --thread block at ~line 434, SlackInboundEnabled/SlackThreadsTableName fields at ~line 2226, NotifyEnv extension and params wiring at ~line 2627
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata_slack_inbound_test.go` - 5 tests replacing 4 Wave 0 stubs (139 lines)

## Decisions Made

- Compile-time slot for `KM_SLACK_INBOUND_QUEUE_URL=""` reserved in `/etc/profile.d/km-notify-env.sh`; km create (Plan 67-06) fills the value at runtime — this avoids a nil template value while keeping the slot wired
- `SlackThreadsTableName` populated via `os.Getenv("KM_SLACK_THREADS_TABLE")` with `"km-slack-threads"` fallback — matches `budgetTable` pattern in same function, no new parameters needed
- `EnvironmentFile=/etc/profile.d/km-notify-env.sh` chosen for systemd unit over inline `Environment=` lines — env file is written at bootstrap time and runtime-patched by km create, making it the single source of truth
- Added `ttl_expiry` as Number type in DDB write (Unix epoch + 30 days) per RESEARCH.md Open Question 3 guidance — required for DDB TTL to work correctly

## Deviations from Plan

### Auto-fixed Issues

None — plan executed as specified. Added `TestUserdata_SlackInboundThreadFlag` as a fifth test beyond the four stubs (covers the notify-hook --thread wiring that was done in Task 1 but had no dedicated test in the plan's four stubs). This is additive test coverage, not a deviation.

## Issues Encountered

None. All six edits applied cleanly. The compile-time `KM_SLACK_INBOUND_QUEUE_URL=""` slot pattern resolved the "empty at compile time, runtime-injected" requirement without needing a second template parameter.

## Next Phase Readiness

- Plan 67-04 provides the sandbox-side dispatch surface. The SQS poller will start and immediately exit (queue URL empty) until Plan 67-06 wires the runtime injection.
- Plan 67-06 (km create SQS queue + env injection) can now inject `KM_SLACK_INBOUND_QUEUE_URL` into the env file slot that was reserved here.
- The notify-hook `--thread` wiring is live for all Slack-enabled sandboxes — Plan 67-05 (poller exports `KM_SLACK_THREAD_TS` before each `km agent run`) will activate it end-to-end.

---
*Phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch*
*Completed: 2026-05-03*
