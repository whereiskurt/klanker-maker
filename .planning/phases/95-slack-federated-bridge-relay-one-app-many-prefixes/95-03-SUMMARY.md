---
phase: 95-slack-federated-bridge-relay-one-app-many-prefixes
plan: 03
subsystem: slack
tags: [slack, federation, doctor, km-doctor, peer-bridges, relay, go-test]

# Dependency graph
requires:
  - phase: 95-01
    provides: cfg.Slack.PeerBridges []string config field + KM_SLACK_PEER_BRIDGES init export
provides:
  - checkSlackPeerBridges doctor check (SKIPPED/WARN/OK) in doctor_slack.go
  - GetSlackPeerBridges() on DoctorConfigProvider interface + appConfigAdapter
  - Phase 95 federated relay operator section in docs/slack-notifications.md
  - Phase 95 note in CLAUDE.md
  - Manual two-install E2E UAT procedure (SLACK-FED-E2E checkpoint)
affects: [95-02, km-doctor-additions, slack-operator-docs]

# Tech tracking
tech-stack:
  added: [net/url (new import in doctor_slack.go)]
  patterns:
    - "checkSlackPeerBridges mirrors checkSlackBotUserIDCached pattern: pure-function, returns CheckResult, no side effects"
    - "DoctorConfigProvider interface extended with GetSlackPeerBridges() for each new Slack config field"
    - "Wiring in doctor.go: anonymous closure captures peerBridges + slackSSMStore, fetches ownBridgeURL lazily at check time"

key-files:
  created:
    - internal/app/cmd/doctor_slack_peer_bridges_test.go
  modified:
    - internal/app/cmd/doctor_slack.go
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go
    - docs/slack-notifications.md
    - CLAUDE.md

key-decisions:
  - "checkSlackPeerBridges accepts raw []string + ownBridgeURL string — pure function, no SSM calls inside"
  - "ownBridgeURL fetched lazily in the doctor.go closure via slackSSMStore.Get, not re-read from config"
  - "GetSlackPeerBridges() added to DoctorConfigProvider interface (not captured via cfg.Slack.PeerBridges directly) for testability"
  - "Self-loop check skipped gracefully when ownBridgeURL is empty string"
  - "SLACK-FED-E2E is a manual checkpoint: needs real Slack App + two live km installs; not automatable"

patterns-established:
  - "New Slack config field → new DoctorConfigProvider getter → new doctor_slack*.go check → new _test.go — follow this pattern"

requirements-completed: [SLACK-FED-DOCTOR, SLACK-FED-E2E]

# Metrics
duration: 7min
completed: 2026-06-05
---

# Phase 95 Plan 03: Doctor checks + docs for federated Slack bridge relay

**`km doctor` WARN checks for malformed peer URL / self-loop in `slack.peer_bridges`, plus full Phase 95 operator docs in `slack-notifications.md` and `CLAUDE.md`; SLACK-FED-E2E recorded as manual UAT checkpoint.**

## Performance

- **Duration:** ~7 min
- **Started:** 2026-06-05T06:15:05Z
- **Completed:** 2026-06-05T06:22:19Z
- **Tasks:** 2 of 3 automated (Task 3 is `checkpoint:human-verify`)
- **Files modified:** 5

## Accomplishments

- `checkSlackPeerBridges` function in `doctor_slack.go`: returns SKIPPED (empty list), WARN (malformed URL), WARN (self-loop), OK (all valid) — pure function, no side effects
- Wired into `doctor.go` Slack checks block after `checkSlackBotUserIDCached`; ownBridgeURL fetched lazily via `slackSSMStore`; demoted CheckError → CheckWarn (Slack issues never fail doctor)
- 6 unit tests (table-driven) in `doctor_slack_peer_bridges_test.go` — all green
- `GetSlackPeerBridges() []string` added to `DoctorConfigProvider` interface + `appConfigAdapter` + both test stubs
- Phase 95 operator section in `docs/slack-notifications.md`: problem/solution, architecture diagram, YAML config example, setup flow, correctness invariants, `km doctor` check table, troubleshooting table
- Phase 95 note in `CLAUDE.md`: "Where to look" row + `slack.peer_bridges` / `KM_SLACK_PEER_BRIDGES` / deploy constraint bullet
- Manual E2E UAT procedure (Task 3) recorded in this SUMMARY for human verifier

## Task Commits

Each task was committed atomically:

1. **Task 1: checkSlackPeerBridges + unit tests, wired into doctor.go** - `50c85136` (feat + TDD)
2. **Task 2: Document federated relay in slack-notifications.md + CLAUDE.md note** - `32f5175a` (docs)
3. **Task 3: Manual two-install E2E UAT** - `checkpoint:human-verify` — AWAITING OPERATOR

## Files Created/Modified

- `internal/app/cmd/doctor_slack_peer_bridges_test.go` — 6 table-driven unit tests for checkSlackPeerBridges
- `internal/app/cmd/doctor_slack.go` — `checkSlackPeerBridges` function + `net/url` import
- `internal/app/cmd/doctor.go` — `GetSlackPeerBridges()` in interface + adapter + wiring in Slack checks block
- `internal/app/cmd/doctor_test.go` — `GetSlackPeerBridges() []string { return nil }` stub on two test config types (auto-fix Rule 3)
- `docs/slack-notifications.md` — Phase 95 federated relay operator section appended
- `CLAUDE.md` — "Where to look" row + Phase 95 block added

## Decisions Made

- `checkSlackPeerBridges` is a pure function accepting `([]string, string)` — no context, no SSM calls. ownBridgeURL is resolved in the `doctor.go` wiring closure, keeping the check function independently testable.
- `GetSlackPeerBridges()` added to `DoctorConfigProvider` interface (rather than accessing `cfg.Slack.PeerBridges` directly) to keep doctor.go consistent with the existing adapter pattern and to allow test stubs to return any value.
- Self-loop check is skipped gracefully when `ownBridgeURL == ""` (bridge not yet deployed) — the check returns OK rather than failing, since no comparison is possible.
- Multiple findings (e.g. two malformed URLs) are aggregated into one WARN message rather than returning multiple results — consistent with the checkSlackBotUserIDCached precedent.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Added GetSlackPeerBridges() to test config stubs**
- **Found during:** Task 1 (after adding method to DoctorConfigProvider interface)
- **Issue:** `doctor_test.go` has two test structs (`testConfig`, `testDoctorConfig`) that implement `DoctorConfigProvider`; both needed the new method or test builds failed.
- **Fix:** Added `func (c *testConfig) GetSlackPeerBridges() []string { return nil }` and equivalent for `testDoctorConfig`.
- **Files modified:** `internal/app/cmd/doctor_test.go`
- **Verification:** `go build ./...` succeeds; `go test ./internal/app/cmd/... -run TestCheckSlackPeerBridges -v` green.
- **Committed in:** `50c85136` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 3 — blocking interface mismatch)
**Impact on plan:** Required for correctness; zero scope creep.

## Issues Encountered

- `TestRunAgentAuthClaude_TeesAndCleans` times out at 60s in the full `./internal/app/cmd/...` suite — pre-existing issue unrelated to this plan. All doctor/Slack tests pass when run with `-run TestCheckSlack|TestDoctor|TestAnyProfile`.

## Manual E2E UAT Procedure (SLACK-FED-E2E)

**Status: AWAITING OPERATOR VERIFICATION**

This task cannot be automated — it requires a real Slack App + two live km installs in one AWS account/region.

### PRE-DEPLOY (operator):

1. Install ONE Slack App; obtain `xoxb-...` bot token and signing secret.
2. Run `km slack init` on installs A and B, pasting the SAME `xoxb-...` + signing secret into each (each stores credentials in its own per-prefix SSM paths — no shared SSM).
3. Set the Slack App Events Request URL to install A's `{bridge-url}/events` (A = front door).
4. In A's `km-config.yaml` set `slack.peer_bridges` to `[B's /events URL]`; for symmetry set B's to `[A's /events URL]`.
5. On each affected install:
   ```bash
   make build-lambdas    # clean rebuild (avoids stale zip pitfall)
   km init --dry-run=false
   ```
   **NOTE: Use `km init --dry-run=false`, NOT `km init --sidecars`.** The `--sidecars` flag does NOT update the Lambda env-block where `KM_SLACK_PEER_BRIDGES` lives.
6. Confirm the env reached the Lambda:
   ```bash
   aws lambda get-function-configuration \
     --function-name {prefix}-slack-bridge \
     --query 'Environment.Variables.KM_SLACK_PEER_BRIDGES' --output text
   ```
7. Run `km doctor` on A — `slack peer bridges` should report OK (non-empty list, no self-loop, no malformed URLs).

### E2E (the relay):

8. Create a sandbox under install B (per-sandbox channel `#sb-{id}` owned by B).
9. Post a message in B's `#sb-{id}` channel (via Slack UI).
10. Confirm: A's bridge CloudWatch logs show a broadcast (relay to B); B's bridge logs show the relayed request processed (`FetchByChannel` hit); B's SQS queue enqueues the message; the 👀 ack is posted in the channel. Slack's 3-second ack window must be honored (no Slack retries visible).

### NEGATIVE checks:

11. Confirm: A relays a message for a channel no install owns → dropped, logged `slack_relay_no_owner`, no infinite loop.
12. Confirm: with `slack.peer_bridges` unset on a single-install setup, behaviour is byte-identical to pre-Phase-95 (local miss returns 200, no broadcast, no errors).

### Acceptance:

Operator reports the two-install relay works (B enqueues + reacts) and the deploy used `km init --dry-run=false`. Record outcome (pass/fail + any notes) and resume with signal "approved" or describe the failure.

## Next Phase Readiness

- Plan 95-02 (relay engine + decision table) runs in parallel and is unblocked — it touches different files (`pkg/slack/bridge/*`, `cmd/km-slack-bridge/main.go`, infra TF).
- After both 95-02 and 95-03 are merged and the E2E checkpoint passes, Phase 95 is complete.

---
*Phase: 95-slack-federated-bridge-relay-one-app-many-prefixes*
*Completed: 2026-06-05 (Tasks 1-2 done; Task 3 awaiting operator UAT)*
