---
phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
plan: 11
subsystem: infra
tags: [slack, sqs, ssm, bash, userdata, claude-p, ed25519, gap-closure]

# Dependency graph
requires:
  - phase: 67
    provides: 67-04 (km-slack-inbound-poller heredoc + systemd unit), 67-09 (cloud-init SSM-fetch for /etc/profile.d/km-slack-runtime.sh)
  - phase: 63
    provides: km-notify-hook Stop branch with KM_SLACK_THREAD_TS / THREAD_FLAG (Phase 67 already consumes this from 67-04 thread-flag work)
provides:
  - Inbound poller posts canonical .result text from output.json to Slack via /opt/km/bin/km-slack post --thread "$THREAD_TS" — direct ROOT-side invocation, bypassing the unreliable Stop hook transcript-JSONL scrape that fails in `claude -p` mode
  - Startup-time SSM Parameter Store resolution of KM_SLACK_CHANNEL_ID (/sandbox/{id}/slack-channel-id) and KM_SLACK_BRIDGE_URL (/km/slack/bridge-url) cached for the systemd service lifetime — eliminates the systemd-EnvironmentFile gap (only loads /etc/profile.d/km-notify-env.sh, NOT km-slack-runtime.sh)
  - Ack-first post ordering (post happens AFTER aws sqs delete-message succeeds) — eliminates host-crash-window where the user sees a reply but the message gets redelivered → duplicate Claude run + duplicate Slack post
  - Stop hook Slack branch (# 6b.) gated on KM_SLACK_INBOUND_REPLY_HANDLED!=1 — prevents double-post on inbound flows. Email branch (# 6a.) unchanged — non-inbound `km agent run --notify-on-idle` email pings still fire.
affects: [67-12, 67-UAT re-test, future inbound observability work]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "ROOT-side SSM Parameter Store fetch at systemd-service startup (not per-turn) — mirrors the existing queue-url SSM fallback pattern at userdata.go:929-942"
    - "Ack-first ordering for at-most-once-visible delivery: SQS delete-message succeeds BEFORE the user-visible side effect (Slack post)"
    - "Silence-on-failure trade-off: agent failure produces no fallback Slack message; operator diagnoses via journalctl + km agent list. Documented in deferred-items.md and 67-UAT.md re-test plan."
    - "Sentinel-env-var coordination between two scripts in the same userdata: poller exports KM_SLACK_INBOUND_REPLY_HANDLED=1; Stop hook (running as a child of the same claude process) reads it to gate its Slack branch."

key-files:
  created:
    - .planning/phases/67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch/67-11-SUMMARY.md
    - .planning/phases/67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch/deferred-items.md
  modified:
    - pkg/compiler/userdata.go (3 surgical edits — SSM-resolve block, post-block in success branch after delete-message, Stop hook gate)
    - pkg/compiler/userdata_slack_inbound_test.go (4 new tests + 1 helper extractSlackInboundPoller)
    - .planning/phases/67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch/67-UAT.md (Gap A Closure Re-test section appended)

key-decisions:
  - "Resolve KM_SLACK_CHANNEL_ID and KM_SLACK_BRIDGE_URL from SSM at poller startup (3×5s retry, ~15s ceiling) rather than relying on /etc/profile.d/km-slack-runtime.sh — systemd EnvironmentFile only loads km-notify-env.sh, so cloud-init's km-slack-runtime.sh is invisible to the ROOT-side post block."
  - "Place post call AFTER aws sqs delete-message (ack-first) — host-crash between post and ack would cause SQS redelivery and duplicate replies. Trade-off accepted: on rare crash between delete-message and post, user sees no reply but message is gone (operator diagnoses via journalctl)."
  - "Silence-on-failure: agent failure (non-zero exit OR empty .result) produces NO fallback Slack message. Documented trade-off — a fake '(no recent assistant text)' message in production is indistinguishable from a real empty reply, while genuine silence is a clear signal to inspect logs."
  - "Sentinel coordination via KM_SLACK_INBOUND_REPLY_HANDLED=1 — narrowly scoped to the Slack branch (# 6b.). Email branch (# 6a.) unchanged so users running `km agent run --notify-on-idle` outside the inbound flow still get email pings."

patterns-established:
  - "Pattern: 'Two-script coordination via sentinel env var' — when two cooperating shell scripts in the same sandbox-userdata need to deduplicate side effects (here: poller posts to Slack vs Stop hook posting to Slack), export a sentinel from the upstream script and gate the downstream branch on it. Avoids forking the downstream script's logic into 'inbound-aware' and 'non-inbound-aware' copies."
  - "Pattern: 'Service-startup SSM resolve, cache-for-lifetime' — values that don't change between requests (channel-id, bridge-url) should be SSM-fetched ONCE at service startup, not per-turn. Mirrors the queue-url fallback pattern; reduces SSM API calls + latency per turn."
  - "Pattern: 'Ack-first delivery' — for at-least-once SQS delivery, perform the visible side effect AFTER the ack to avoid the redelivery-after-side-effect failure mode. Trade-off: rare host crash between ack and side effect drops the side effect entirely; this is the safer default vs the duplicate-side-effect alternative."
  - "Pattern: 'Structural test split on stable comment markers' — TestUserdata_StopHookSkipsSlackWhenInboundHandled splits rendered userdata on # 6a. / # 6b. markers and asserts substring presence within each slice. More robust than fragile strings.Count heuristics; fails clearly if a marker drifts."

requirements-completed:
  - REQ-SLACK-IN-POLLER
  - REQ-SLACK-IN-DELIVERY

# Metrics
duration: 6m
completed: 2026-05-03
---

# Phase 67 Plan 11: Slack inbound Gap A closure (poller posts .result, Stop hook gated) Summary

**Inbound poller posts Claude's canonical .result text from output.json to Slack via km-slack post --thread AFTER acking SQS, with KM_SLACK_INBOUND_REPLY_HANDLED gating the Stop hook's Slack branch to prevent double-posts; SSM-resolves channel/bridge URL at startup to bypass the systemd EnvironmentFile gap.**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-05-03T13:09:13Z
- **Completed:** 2026-05-03T13:15:00Z (approx)
- **Tasks:** 2
- **Files modified:** 3 (userdata.go, userdata_slack_inbound_test.go, 67-UAT.md) + 2 created (SUMMARY.md, deferred-items.md)

## Accomplishments

- **Gap A root cause closed:** poller now posts `.result` text from `/workspace/.km-agent/runs/<id>/output.json` directly to Slack via `/opt/km/bin/km-slack post --thread "$THREAD_TS"` — replacing the Stop hook's unreliable transcript-JSONL scrape that produced "(no recent assistant text)" in `claude -p` mode.
- **SSM resolution at poller startup** for `KM_SLACK_CHANNEL_ID` (`/sandbox/${SANDBOX_ID}/slack-channel-id`) and `KM_SLACK_BRIDGE_URL` (`/km/slack/bridge-url`) with 3×5s retry — bypasses the systemd EnvironmentFile gap (only loads km-notify-env.sh, not km-slack-runtime.sh).
- **Ack-first ordering:** post block sits INSIDE `if [ -n "$NEW_SESSION" ]` AFTER `aws sqs delete-message` so a host crash between them can't cause the message to redeliver after the user already saw a reply.
- **Stop hook gate** (# 6b.) on `${KM_SLACK_INBOUND_REPLY_HANDLED:-0} != "1"` prevents double-post when the inbound poller drove the run. Email branch (# 6a.) unchanged.
- **Four new compiler tests** (TDD RED→GREEN) guard the wiring: post-exists, SSM-before-while-loop, post-after-delete ordering, and structural 6a/6b email-vs-Slack isolation. SlackInbound test count grows from 5 → 9.
- **UAT.md re-test plan** documents the operator's post-merge sequence: destroy + recreate + UAT Steps 6/7/8 re-runs + optional failure-mode regression check.

## Task Commits

Each task was committed atomically:

1. **Task 1: TDD — 4 new compiler tests (RED) → 3 surgical edits to userdata.go (GREEN)** — `8bd25ba` (fix)
2. **Task 2: Append "Gap A Closure Re-test" section to 67-UAT.md** — `b7d7de0` (docs)

**Plan metadata commit:** (final, after this SUMMARY)

_Note: Task 1 was a TDD task but the RED tests + GREEN edits were squashed into a single fix commit since the test file additions and userdata.go edits are tightly coupled — the test scaffolding helper (`extractSlackInboundPoller`) only makes sense alongside the production wiring it asserts._

## Files Created/Modified

- `pkg/compiler/userdata.go` — three surgical edits:
  - Lines ~948-993: SSM-resolve block for channel/bridge URL BEFORE `while true` loop (3×5s retry per param, cached for service lifetime)
  - Lines ~1093-1136: post block inside `if [ -n "$NEW_SESSION" ]`, AFTER `aws sqs delete-message`. Reads `.result` from `output.json`, exports `KM_SLACK_INBOUND_REPLY_HANDLED=1`, calls `/opt/km/bin/km-slack post --channel ... --subject "" --thread "$THREAD_TS" --body <file>`. Three branches: success path (post + log "Posted reply"), missing-config path (WARN: KM_SLACK_CHANNEL_ID/KM_SLACK_BRIDGE_URL unset), empty-result path (WARN: empty .result).
  - Lines ~471-477: Stop hook Slack branch (# 6b.) conditional extended with `&& "${KM_SLACK_INBOUND_REPLY_HANDLED:-0}" != "1"`. Email branch (# 6a.) at lines ~463-469 untouched.
- `pkg/compiler/userdata_slack_inbound_test.go` — added `extractSlackInboundPoller` helper + four new tests:
  - `TestUserdata_PollerPostsResultToSlack` — asserts `.result` extraction, `km-slack post`, `--thread "$THREAD_TS"`, `KM_SLACK_INBOUND_REPLY_HANDLED=1` all present in SLACKINBOUND heredoc
  - `TestUserdata_PollerResolvesChannelAndBridgeFromSSM` — asserts `/sandbox/${SANDBOX_ID}/slack-channel-id` and `/km/slack/bridge-url` SSM paths present BEFORE `while true` (one-time startup, not per-turn)
  - `TestUserdata_PollerPostsAfterDeleteMessage` — asserts `km-slack post` appears AFTER `aws sqs delete-message` within the success branch (ack-first ordering)
  - `TestUserdata_StopHookSkipsSlackWhenInboundHandled` — splits rendered userdata on `# 6a.` / `# 6b.` markers; asserts the gate is in 6b but NOT in 6a (email-branch isolation)
- `.planning/phases/67-slack-inbound-...-/67-UAT.md` — appended "## Gap A Closure Re-test" section (38 lines) describing the operator re-test sequence after `make build` + `km init --sidecars`.
- `.planning/phases/67-slack-inbound-...-/deferred-items.md` — created to track pre-existing test failure (TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime) that was failing on the parent commit before any 67-11 edits applied.

## Decisions Made

See key-decisions in frontmatter. Brief recap:

- **Startup SSM resolve, not per-turn:** values don't change between turns; one fetch + cache is sufficient. Mirrors the existing queue-url fallback pattern.
- **Ack-first (post AFTER delete-message):** prevents the worse failure mode (duplicate reply on visibility-timeout expiry) at the cost of the rarer failure (no reply on host crash between delete-message and post — operator visibility via journalctl).
- **Silence-on-failure:** no fake "(no recent assistant text)" fallback in Slack. Documented in plan context, code comments, and 67-UAT.md re-test plan.
- **Sentinel KM_SLACK_INBOUND_REPLY_HANDLED:** narrowly scoped to Slack branch only. Email branch unchanged so non-inbound `km agent run --notify-on-idle` flows are unaffected.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Removed Go-template-killing backticks from new comment blocks**
- **Found during:** Task 1 (Edit C application + first GREEN test run)
- **Issue:** The plan's recommended comment text included literal backticks around `claude -p`, `sudo -u sandbox bash -c`, `km slack init`, `journalctl -u km-slack-inbound-poller`, and `km agent list`. Backticks close Go raw-string literals, so the userdata.go template (a single Go raw-string `const userDataTemplate = \`...\``) failed to compile with `syntax error: unexpected name claude after top level declaration`.
- **Fix:** Replaced backticks with single-quotes in all new comment text inside the template raw string (lines ~953, ~959, ~1103, ~1110, ~1111, ~473). Behavior preserved (these are bash comments — quote style is cosmetic).
- **Files modified:** pkg/compiler/userdata.go
- **Verification:** `go build ./pkg/compiler/...` passes; all 9 SlackInbound tests pass.
- **Committed in:** 8bd25ba (Task 1 commit)

**2. [Rule 2 - Helper API mismatch] Used existing test-file helper signatures, not the plan's hypothetical mustGenerateUserData/profileWithSlackInbound**
- **Found during:** Task 1 (writing the four new tests)
- **Issue:** Plan recommended `mustGenerateUserData(t, profileWithSlackInbound())` but the actual helpers in `userdata_slack_inbound_test.go` are `compileInboundUserData(t, p)` and `minimalSlackInboundProfile(t, true)`. The plan explicitly noted "If the existing test file uses different helper names, mirror those exactly" — so this isn't a deviation against intent, but it IS a deviation from the literal recommended code.
- **Fix:** Wrote tests using the existing helpers. Added a new local helper `extractSlackInboundPoller(t, out)` to centralize the SLACKINBOUND heredoc bounding logic (used by 3 of the 4 new tests).
- **Files modified:** pkg/compiler/userdata_slack_inbound_test.go
- **Verification:** All 4 new tests pass; existing 5 tests still pass.
- **Committed in:** 8bd25ba (Task 1 commit)

### Out-of-scope discoveries (logged, NOT fixed)

**3. Pre-existing test failure: TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime**
- **Found during:** Task 1 verification (full `go test ./pkg/compiler/...` run)
- **Issue:** Test asserts `KM_SLACK_BRIDGE_URL=` substring is NOT in rendered userdata. The cloud-init Slack runtime block at userdata.go:175-215 (Phase 67 cloud-init SSM-fetch from prior plans) writes `export KM_SLACK_BRIDGE_URL="$BRIDGE_URL"` into the heredoc, which contains `KM_SLACK_BRIDGE_URL=`. Test was failing on the parent commit (5f6b571) before any 67-11 edits applied — verified via `git stash` + re-run.
- **Why not fixed:** Out of scope for 67-11 (the plan's verify command explicitly scopes to `-run "TestUserdata_PollerPosts...|TestUserdata_SlackInbound"`, not the full suite). Logged in `.planning/phases/.../deferred-items.md` with three resolution options for follow-up.
- **Impact on 67-11:** None. All 4 new 67-11 tests pass; all 5 prior SlackInbound tests pass; `make build` succeeds.

---

**Total deviations:** 2 auto-fixed (1 bug — template backticks, 1 helper-mismatch alignment to existing code) + 1 out-of-scope discovery logged
**Impact on plan:** All auto-fixes essential for correctness (template compilation, test-file helper conformance). No scope creep. The pre-existing test failure is pre-existing and explicitly out of scope.

## Issues Encountered

- **Go template raw-string + backticks** (resolved as Deviation 1 above). Brief friction during the GREEN phase. Lesson: when authoring plans that prescribe new comment text inside `pkg/compiler/userdata.go`, avoid backticks since the entire 2800-line userdata template is a single Go raw-string literal.

## User Setup Required

None — no external service configuration required. The change is internal to the userdata template that runs on EC2 instance creation.

**However, operator post-merge action is REQUIRED for the fix to land on new sandboxes:**
1. `make build` (already done as part of Task 1 verify — produces km v0.2.476)
2. `km init --sidecars` — refreshes management Lambdas' bundled km binary so remote `km create` calls render the new userdata template with the Phase 67-11 fixes
3. Existing sandboxes (e.g. `lrn2-16a1cff8`) do NOT get the fix retroactively — userdata only runs at create time. Operator must `km destroy --remote --yes` + recreate to exercise the fix.

See `.planning/phases/67-slack-inbound-...-/67-UAT.md` "## Gap A Closure Re-test" section for the full re-test sequence.

## Next Phase Readiness

- **Plan 67-12 unblocked:** the remaining gap-closure plan (isBotLoop allow-list) is independent of 67-11 and can be executed next.
- **UAT Steps 7-17 unblocked once operator runs the re-test** — Gap A was the blocker preventing meaningful execution of those steps; Gap B (bot-loop allow-list, addressed by 67-12) is independently exercisable.
- **Phase 67 completion** depends on: (a) 67-12 ships, (b) operator runs the documented re-test sequence and confirms PASS gates, (c) 67-UAT.md frontmatter status flips to closed.

## Self-Check: PASSED

- **Files exist:**
  - FOUND: `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go` (modified)
  - FOUND: `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata_slack_inbound_test.go` (modified)
  - FOUND: `/Users/khundeck/working/klankrmkr/.planning/phases/67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch/67-UAT.md` (modified)
  - FOUND: `/Users/khundeck/working/klankrmkr/.planning/phases/67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch/deferred-items.md` (created)
- **Commits exist:**
  - FOUND: 8bd25ba (Task 1)
  - FOUND: b7d7de0 (Task 2)
- **Tests:** 9/9 SlackInbound + 4/4 new Gap A tests pass; `make build` produces km v0.2.476.

---
*Phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch*
*Completed: 2026-05-03*
