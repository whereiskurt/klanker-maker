---
phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
plan: 10
subsystem: testing
tags: [slack, e2e, uat, documentation, inbound, bidirectional-chat, claude-md, slack-notifications]

# Dependency graph
requires:
  - phase: 67-04
    provides: bridge /events handler with HMAC + bot-loop filter
  - phase: 67-05
    provides: sandbox-side bash poller (km-slack-inbound-poller.sh)
  - phase: 67-06
    provides: km create SQS provisioning + ready announcement
  - phase: 67-07
    provides: km destroy drain + channel archive
  - phase: 67-08
    provides: km status / list / doctor extensions + paused queue-and-wait
  - phase: 67-09
    provides: km slack init --signing-secret + Events URL + scope check
  - phase: 67-11
    provides: poller posts .result; Stop hook gate on KM_SLACK_THREAD_TS; AWS_REGION export; parallel /etc/km/notify.env (Gap A closure)
  - phase: 67-12
    provides: isBotLoop allow-list semantics (Gap B closure)
provides:
  - test/e2e/slack/inbound_e2e_test.go (RUN_SLACK_E2E=1 gated end-to-end test)
  - test/e2e/slack/profiles/inbound-e2e.yaml (test profile)
  - docs/slack-notifications.md "Inbound chat (Phase 67)" operator section
  - CLAUDE.md "Slack inbound (Phase 67)" subsection
  - 67-10-UAT.md (manual UAT checklist with PASS/FAIL gates per VALIDATION.md row)
  - GREEN ship verdict for Phase 67 (11/13 PASS + 1 partial + 2 NOT-EXERCISED with compensating coverage)
affects:
  - Phase 67 close-out (this is the final wave-4 plan)
  - Phase 68 (transcript streaming) — operator docs reference Phase 67 inbound stack
  - Future operators onboarding to Slack inbound from a clean Phase 63 install

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "RUN_SLACK_E2E=1 env gate for opt-in live-workspace E2E tests (mirrors Phase 63 precedent in slack_e2e_test.go)"
    - "UAT.md document with per-step PASS/FAIL/PARTIAL/NOT-EXERCISED gates mapped to VALIDATION.md manual-only rows"
    - "Compensating-coverage doctrine: NOT-EXERCISED rows must cite either unit-test coverage, AWS service guarantee, or alternative defence path"

key-files:
  created:
    - test/e2e/slack/inbound_e2e_test.go
    - test/e2e/slack/profiles/inbound-e2e.yaml
    - .planning/phases/67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch/67-10-UAT.md
  modified:
    - docs/slack-notifications.md
    - CLAUDE.md

key-decisions:
  - "RUN_SLACK_E2E=1 is the only gate — no -tags=e2e build tag, just env var skip in test body. Keeps default `go test ./...` green; CI workflow with secret env var runs the live test"
  - "UAT.md uses 4 verdict states: PASS, FAIL, PARTIAL, NOT-EXERCISED (with compensating coverage). NOT-EXERCISED is allowed but must cite alternative defence"
  - "Pause/resume (Steps 9-11) PASS upgrade comes from operator's separate validation post-checkpoint, not the original UAT script — captured as a verdict update via beadaa3"
  - "Step 14 (channel_join filter) accepts unit-test coverage (14 negative + 1 positive subtype cases) in lieu of live CloudWatch verification — Gap B isBotLoop allow-list is the authoritative defence"
  - "Steps 15 (invite gate) and 16 (signing-secret mismatch) deferred to a future UAT cycle — neither is a Phase 67 ship blocker; both have documented compensating coverage"
  - "Phase 67 ship verdict: GREEN. 11/13 actively-exercised steps PASS, 1 partial (Step 12: 30s-timeout edge case deferred), 2 NOT-EXERCISED with compensating coverage"

patterns-established:
  - "Gap-closure followups commit independently of original plan commits — chain from a single PLAN.md but produce multiple atomic commits per gap (8bd25ba, 9383bb4, e0e76e5, bc058af)"
  - "UAT verdict updates are docs commits (not gap closures) — Steps 9-11 PASS upgrade lands as docs(67-10): without an executable gap-closure plan"

requirements-completed: [REQ-SLACK-IN-EVENTS, REQ-SLACK-IN-DELIVERY, REQ-SLACK-IN-POLLER, REQ-SLACK-IN-LIFECYCLE]

# Metrics
duration: 2 days (Task 1 landed 2026-05-02; UAT round-3 + final docs 2026-05-03)
completed: 2026-05-03
---

# Phase 67 Plan 10: E2E Test + Operator Docs + UAT Summary

**RUN_SLACK_E2E=1 gated end-to-end test, operator documentation in `docs/slack-notifications.md` and `CLAUDE.md`, and a 17-step manual UAT against the live klankermaker.ai workspace closing Phase 67 with a GREEN ship verdict (11 PASS + 1 partial + 2 NOT-EXERCISED with compensating coverage)**

## Performance

- **Duration:** Spanning 2 days — Task 1 (E2E + docs) landed 2026-05-02; checkpoint round-3 with UAT fill + Steps 9-11 PASS upgrade landed 2026-05-03
- **Started:** 2026-05-02 (Task 1 commit ffca576)
- **Completed:** 2026-05-03T14:58:07Z (this SUMMARY)
- **Tasks:** 2 (Task 1 auto, Task 2 human-verify checkpoint)
- **Files modified:** 5 (3 created, 2 modified)
- **Total commits in this plan:** 8 (1 original + 4 gap-closure follow-ups + 3 docs/verdict commits)

## Accomplishments

- Phase 67 ships GREEN — Slack inbound bidirectional chat is operator-ready, documented, and UAT-validated against a real workspace
- Live UAT round-3 on `l11` confirmed Gap A closure (`.result` text in Slack, no fallback string) and Gap B closure (no bot-loops on system events) — the two failure modes from UAT round-1 are gone
- Pause/resume round-trip validated end-to-end: pause → message queued in SQS → resume → poller drains → Claude replies in-thread (Steps 9-11)
- Operator documentation in `docs/slack-notifications.md` and `CLAUDE.md` covers prerequisites, one-time setup, profile fields, behavior, troubleshooting, security model, and limitations
- Opt-in E2E test (`test/e2e/slack/inbound_e2e_test.go`) provides regression coverage for the full Slack→sandbox→Claude→Slack round-trip when run with `RUN_SLACK_E2E=1`
- 67-10-UAT.md serves as the canonical operator-facing checklist mapping each step to VALIDATION.md manual-only rows, with verdict gates that drive ship/no-ship decisions

## Task Commits

This plan produced atomic commits across the original execution and several gap-closure / verdict-update follow-ups. All commits referenced below are part of Phase 67 close-out:

**Original 67-10 work (Task 1):**

1. **Task 1: E2E test + operator docs** — `ffca576` (feat) — test/e2e/slack/inbound_e2e_test.go, test/e2e/slack/profiles/inbound-e2e.yaml, docs/slack-notifications.md, CLAUDE.md
2. **UAT scaffold creation** — `d4fc2a1` (docs) — 67-10-UAT.md initial structure mapping VALIDATION.md rows
3. **Checkpoint state update** — `6962664` (chore) — STATE.md position update awaiting UAT

**Gap-closure follow-ups (sourced from 67-11 + 67-12 plans, but materially required to take 67-10 to PASS):**

4. **Gap A: poller posts .result** — `8bd25ba` (fix) — sidecars/km-slack/scripts/km-slack-inbound-poller.sh — replaces fallback string with real Claude output in Slack
5. **Gap B: isBotLoop allow-list** — `9383bb4` (fix) — pkg/slack/bridge/handler.go — switch from deny-list to allow-list to drop channel_join + 13 other system subtypes
6. **Gap A follow-up: Stop hook gate + AWS_REGION export** — `e0e76e5` (fix) — sandbox-side hook scripts — close double-post and us-east-1 fallback paths
7. **Gap A follow-up: parallel systemd-format env file** — `bc058af` (fix) — parallel /etc/km/notify.env (no `export` prefix) for `EnvironmentFile=` consumption

**67-10 close-out (Task 2 + final docs):**

8. **Operator guide refresh for post-UAT fixes** — `3188442` (docs) — docs/slack-notifications.md — Stop hook gate, AWS_REGION, parallel /etc/km/notify.env explained
9. **UAT round-3 PASS gates** — `0327fd1` (docs) — 67-10-UAT.md filled with PASS/PARTIAL/NOT-EXERCISED verdicts from operator's l11 round-3 validation
10. **UAT Steps 9-11 PASS upgrade** — `beadaa3` (docs) — 67-10-UAT.md — pause/resume round-trip validated post-checkpoint

**Plan metadata commit:** (this commit, lands SUMMARY + STATE + ROADMAP updates)

_Note: Commits 4-7 originated in plans 67-11 and 67-12 but are listed here because they materially close gaps that were necessary for 67-10's UAT to verdict GREEN. The cross-plan attribution reflects how Phase 67's final wave (10, 11, 12) operated as a single integrated UAT-driven ship effort._

## Files Created/Modified

- `test/e2e/slack/inbound_e2e_test.go` — RUN_SLACK_E2E=1 gated end-to-end test exercising the full bidirectional flow (km create → ready announcement → first turn → session continuity turn 2 → top-level new thread → km destroy → queue + channel archive verification)
- `test/e2e/slack/profiles/inbound-e2e.yaml` — Test profile with `notifySlackInboundEnabled: true` and t3.micro substrate
- `docs/slack-notifications.md` — New "Inbound chat (Phase 67)" operator section appended after Phase 63 content; covers prerequisites (channels:history + groups:history scopes, signing secret), one-time setup (`km slack init --force --signing-secret`), profile fields, behavior (ready announcement, top-level vs in-thread, session continuity, pause/destroy semantics), troubleshooting matrix, security model, rotation, limitations
- `CLAUDE.md` — New "Slack inbound (Phase 67)" subsection inserted after Phase 63 Slack block; profile field, env vars (KM_SLACK_INBOUND_QUEUE_URL, KM_SLACK_THREAD_TS, KM_SLACK_THREADS_TABLE), SSM parameter (`/km/slack/signing-secret`), DDB tables, SQS resources, one-time setup, doctor checks
- `67-10-UAT.md` — Operator-facing UAT checklist with 17 numbered steps mapped to VALIDATION.md manual-only rows; per-step PASS/FAIL/PARTIAL/NOT-EXERCISED gates; final ship verdict + status checkbox

## Decisions Made

- **Phase 67 ships GREEN despite 2 NOT-EXERCISED rows** — Steps 15 (invite gate) and 16 (signing-secret mismatch) lack live exercise but have documented compensating coverage (channel→sandbox lookup; unit tests). Operator approved this verdict.
- **Step 14 (channel_join filter) accepts unit-test coverage** — Gap B's `isBotLoop` allow-list ships with 14 negative subtype tests + 1 positive `thread_broadcast` test. Live CloudWatch verification was offered but not run; unit-test coverage accepted as sufficient.
- **Pause/resume validation came from operator's post-checkpoint extended testing** — Original UAT script Steps 9-11 were marked NOT EXERCISED on first round; operator validated pause/resume separately and confirmed PASS during the resume conversation, prompting the verdict upgrade in commit beadaa3.
- **Gap-closure work attributed across plans** — Commits 8bd25ba (67-11), 9383bb4 (67-12), e0e76e5 + bc058af (67-11 follow-ups) were essential to the 67-10 GREEN verdict. Listed in this SUMMARY for traceability even though they have separate plan commits.
- **Slack signing-secret rotation negative test deferred** — Running it would create a ~15min Events delivery outage during the 15min Lambda cache TTL. Defended by `TestEventsHandler_SignatureMismatch_Returns401`. Recommend exercising before next major Phase 67 release.

## Deviations from Plan

The 67-10 PLAN.md scope was: write E2E test, update operator docs, run manual UAT. All three were delivered. However, the UAT surfaced two gaps (Gap A: Stop hook reply path / `.result` posting; Gap B: `isBotLoop` channel_join slip-through) that required new gap-closure plans (67-11, 67-12) and four follow-up commits to resolve.

These were tracked as separate plans (per the GSD pattern of one-plan-per-gap-closure), so this is **not a deviation** in the Rule 1-3 sense — it is the documented gap-closure flow operating as designed.

**No auto-fix deviations** were applied during this checkpoint resume. The SUMMARY documents the cross-plan commit lineage for traceability but does not introduce new code changes.

---

**Total deviations:** 0 auto-fixed.
**Impact on plan:** Plan executed as written. The UAT round-1 → round-2 → round-3 iteration with intervening gap-closure plans is the documented Phase 67 wave-4 ship pattern.

## Issues Encountered

- **UAT round 1 (sandbox `l8`)** halted at Step 6 with two diagnosed blockers: Stop hook reply path (Gap A) and channel_join filter (Gap B). Captured in `f6c0f1c` and `UAT-2-HANDOFF.md`. Resolved via plans 67-11 + 67-12 + four follow-up commits.
- **UAT round 2 (sandbox `l8`)** validated the destroy/archive path and km doctor post-cleanup checks but was incomplete on multi-turn continuity due to the same Gap A. Captured in `UAT-2-HANDOFF.md` G-list.
- **UAT round 3 (sandbox `l11`, 2026-05-03)** validated all Gap A and Gap B closures end-to-end, plus extended validations: resume older thread mid-stream, parallel threads stay isolated, no fallback string, no double-posts, no systemd warnings.
- **Pause/resume validation** initially deferred at checkpoint approval; operator validated separately and approved the upgrade to PASS for Steps 9-11.

## User Setup Required

None — Phase 67 is operator-facing but `km slack init --force --signing-secret <value>` is the only manual step, fully documented in both `docs/slack-notifications.md` and `CLAUDE.md`.

The Slack App config side-channel (paste Events URL into Event Subscriptions, add `channels:history` + `groups:history` scopes, reinstall app) is documented in the same operator guide.

## Self-Check

Verified files exist on disk and commits exist in git history.

- `test/e2e/slack/inbound_e2e_test.go` — FOUND (committed in ffca576)
- `test/e2e/slack/profiles/inbound-e2e.yaml` — FOUND (committed in ffca576)
- `docs/slack-notifications.md` — FOUND (modified in ffca576 + 3188442)
- `CLAUDE.md` — FOUND (modified in ffca576)
- `67-10-UAT.md` — FOUND (created d4fc2a1, filled 0327fd1, upgraded beadaa3)
- All commit hashes verified present: ffca576, d4fc2a1, 6962664, 8bd25ba, 9383bb4, e0e76e5, bc058af, 3188442, 0327fd1, beadaa3

## Self-Check: PASSED

## Next Phase Readiness

Phase 67 is complete and ready for `/gsd:verify-work`. The full inbound stack is operator-ready:

- E2E test ready to run on demand (`RUN_SLACK_E2E=1 go test ./test/e2e/slack/... -count=1 -run TestSlackInbound_E2E`)
- Operator can self-serve from a clean Phase 63 install to inbound-enabled sandbox using only the docs (validated by running the UAT against `l11`)
- All 4 Phase 67 requirements complete: REQ-SLACK-IN-EVENTS, REQ-SLACK-IN-DELIVERY, REQ-SLACK-IN-POLLER, REQ-SLACK-IN-LIFECYCLE
- Phase 68 (transcript streaming) is unblocked — depends on Phase 67's km-slack-threads DDB schema and `(channel, thread_ts) → session_id` map, both shipped in 67-02

---
*Phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch*
*Completed: 2026-05-03*
