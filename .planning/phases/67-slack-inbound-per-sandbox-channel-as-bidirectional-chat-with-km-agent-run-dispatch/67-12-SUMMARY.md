---
phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
plan: 12
subsystem: slack
tags: [slack, events-api, bridge-lambda, isBotLoop, allow-list, gap-closure, bedrock-spend-control]

# Dependency graph
requires:
  - phase: 67-slack-inbound
    provides: EventsHandler.isBotLoop deny-list filter (events_handler.go) — the function being switched from deny-list to allow-list semantics
  - phase: 67-11
    provides: Sibling Gap A closure (poller posts .result + Stop hook gate); landed in same wave but no merge conflict (different files)
provides:
  - isBotLoop allow-list: only "" and "thread_broadcast" subtypes pass (UAT Gap B closed)
  - Forensic debug log line `events: subtype filter dropped` for CloudWatch-based regression detection on system subtypes
  - Regression-proof default: any future Slack-added subtype is filtered automatically until explicitly opted in
  - 14 new system-subtype test cases + 1 thread_broadcast positive test (15 net new sub-tests)
  - 67-UAT.md "Gap B Closure Re-test" section with operator post-merge verification steps
affects: [67 UAT re-execution Step 14, future Slack subtype additions, bedrock spend on Slack Connect invite acceptance]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Allow-list over deny-list for external API enum fields where the upstream vendor adds new values over time (Slack subtypes are the canonical example — ekm_access_denied was added years post-launch)"
    - "Defence-in-depth filter ordering: bot_id → subtype → empty user → bot user_id (each gate is cheaper than the next; expensive bot user_id Fetch only runs when no earlier gate matches)"
    - "Forensic debug log on filter drop with structured fields (subtype/channel/ts) — enables CloudWatch insights queries to catalogue all subtypes seen in production"

key-files:
  created: []
  modified:
    - pkg/slack/bridge/events_handler.go (isBotLoop function, lines 214-256: deny-list switch → allow-list switch + debug log)
    - pkg/slack/bridge/events_handler_test.go (14 new system-subtype cases in TestEventsHandler_BotSelfMessageFiltered + new TestEventsHandler_ThreadBroadcastPasses)
    - .planning/phases/67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch/67-UAT.md (Gap B Closure Re-test section appended)

key-decisions:
  - "isBotLoop uses allow-list (empty + thread_broadcast) instead of deny-list — closes UAT Gap B (channel_join slip-through) AND prevents every future subtype regression"
  - "me_message (stylistic /me posts) is filtered by the allow-list; documented trade-off: cost of false negative (a deliberate /me prompt is dropped) is far lower than false positive (every casual /me burns ~$0.05 of Bedrock spend); revisitable if operator reports a real /me-as-prompt being silently dropped — visible via the new debug log"
  - "Existing bot_id and bot-user-ID checks preserved as second-line defence, not replaced — defence-in-depth"
  - "thread_broadcast (Also send to channel) is the only subtype besides empty that counts as a real human turn; carries human content, must reach SQS"

patterns-established:
  - "Allow-list filter for vendor-managed enum fields (Slack subtypes, AWS event types, Stripe event types) — vendor adds new values, deny-list silently regresses every time"
  - "Forensic-grade debug log on every filter drop with structured fields enables 'what subtypes does production actually see' CloudWatch queries"

requirements-completed: [REQ-SLACK-IN-EVENTS]

# Metrics
duration: 2min
completed: 2026-05-03
---

# Phase 67 Plan 12: isBotLoop Allow-List Switch (Gap B Closure) Summary

**Switched isBotLoop from deny-list (`bot_message`/`message_changed`/`message_deleted`) to allow-list (`""` + `thread_broadcast`) semantics — closes UAT Gap B where Slack Connect invite acceptances burned ~$0.05 of Bedrock spend per accept by routing `channel_join` system messages to a no-op Claude turn whose reply post failed with `cannot_reply_to_message`.**

## Performance

- **Duration:** 2 min
- **Started:** 2026-05-03T13:20:00Z
- **Completed:** 2026-05-03T13:22:40Z
- **Tasks:** 2
- **Files modified:** 3 (1 source, 1 test, 1 doc)

## Accomplishments

- `isBotLoop` in `pkg/slack/bridge/events_handler.go` now uses an allow-list: only `subtype == ""` (regular human posts) and `subtype == "thread_broadcast"` (user replied with "Also send to channel") fall through. Every other subtype is dropped before SQS write.
- Existing `bot_id` and bot-user-ID checks preserved as second-line defence (not replaced).
- New debug log line `events: subtype filter dropped` with structured fields `subtype`, `channel`, `ts` — enables CloudWatch Insights queries to catalogue every subtype the bridge sees in production.
- 14 new system-subtype test cases added to `TestEventsHandler_BotSelfMessageFiltered` (channel_join, channel_leave, channel_topic, channel_purpose, channel_name, channel_archive, channel_unarchive, pinned_item, unpinned_item, file_share, me_message, reminder_add, ekm_access_denied, plus a `subtype_unknown_future` regression-proof guarantee).
- New positive-case test `TestEventsHandler_ThreadBroadcastPasses` asserts `thread_broadcast` reaches SQS with 1 send and 1 threads upsert.
- 67-UAT.md updated with "Gap B Closure Re-test" section: pre-conditions (terragrunt apply for bridge Lambda), 3 re-test steps (channel_join drop, thread_broadcast positive, uninvited user gate), and a forensic CloudWatch query for catching future regressions.

## Task Commits

Each task was committed atomically (TDD-first for Task 1):

1. **Task 1 RED: failing tests for isBotLoop allow-list** — `b73d938` (test) — 13/14 new sub-tests fail against deny-list code, confirming RED phase
2. **Task 1 GREEN: switch isBotLoop to allow-list** — `9383bb4` (fix) — all 20 sub-tests of BotSelfMessageFiltered + ThreadBroadcastPasses pass
3. **Task 2: append Gap B Closure Re-test to 67-UAT.md** — `2b81c07` (docs)

No REFACTOR commit needed — the GREEN code is already clean (matches the plan's prescribed action block verbatim plus the documenting comment).

## Files Created/Modified

- `pkg/slack/bridge/events_handler.go` — `isBotLoop` function rewritten: bot_id check (1) → subtype allow-list switch (2, with debug-log default branch) → empty user check (3) → bot user_id second-line defence (4). Added 14-line doc comment explaining why allow-list beats deny-list for Slack subtypes.
- `pkg/slack/bridge/events_handler_test.go` — 14 new system-subtype cases inside the existing `TestEventsHandler_BotSelfMessageFiltered` table + new `TestEventsHandler_ThreadBroadcastPasses` top-level test (right above `TestEventsHandler_ReplayedEventID`).
- `.planning/phases/67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch/67-UAT.md` — appended `## Gap B Closure Re-test` section after the existing `## Gap A Closure Re-test` section. Original Tests/Gaps/Summary/Notes sections unmodified.

## Decisions Made

- **Allow-list over deny-list:** The fundamental decision. Deny-list silently regresses every time Slack adds a new subtype (ekm_access_denied was added years post-launch; the next one will arrive without warning). Allow-list is regression-proof by default — any new subtype is filtered until an operator explicitly opts in.
- **`me_message` filtered (documented trade-off):** `/me waves`-style posts carry human text. They could go either way, but operators almost never type `/me` to deliberately invoke the agent — the cost asymmetry (every casual `/me` burns ~$0.05 of Bedrock spend) makes filtering the correct default. The new `events: subtype filter dropped subtype=me_message` debug log makes the dropped-prompt failure mode forensically visible. If a user reports a real `/me`-as-prompt regression, moving it to the allow-list is a one-line change.
- **`thread_broadcast` is the second allow-list member:** When a user replies in a thread with "Also send to channel" ticked, Slack delivers `subtype: thread_broadcast` carrying the human text. It must reach SQS or "Also send" silently fails. (Note: the deprecated pre-2016 alias `reply_broadcast` is NOT added — modern field is `thread_broadcast`.)
- **Defence-in-depth preserved:** The new `bot_id` (1) and bot-user-ID (4) checks remain — Slack edge cases like a bot posting via webhook with `subtype=""` still get caught.
- **Forensic debug log on every drop:** Structured fields (`subtype`, `channel`, `ts`) enable CloudWatch Insights `stats count() by subtype` queries to discover what subtypes production actually sees, catching future regressions before users do.

## Deviations from Plan

None — plan executed exactly as written. The plan's `<action>` block prescribed the exact source diff and test additions, both of which match what's in the commits character-for-character.

## Issues Encountered

None. TDD flow worked exactly as specified: 13/14 new sub-tests failed in RED phase against the deny-list code (the lone passing case, `subtype_ekm_access_denied`, was caught by the existing `m.User == ""` branch because the test fixture had no user field). After applying the allow-list, all 20 sub-tests passed in GREEN.

## User Setup Required

None — no external service configuration required. Follow-up operator step (NOT part of plan automation, NOT a checkpoint):
- After merge, redeploy the bridge Lambda: `cd infra/live/management/lambda-slack-bridge && terragrunt apply` (or wait for next `km init` cycle).
- Then re-run UAT Step 14 per the new "Gap B Closure Re-test" section in 67-UAT.md to confirm `channel_join` is dropped in production.

## Next Phase Readiness

- Phase 67 Gap B is closed at the source-code level. Once the bridge Lambda is redeployed, UAT Step 14 should pass and Step 15 (Slack Connect invite gate for uninvited users) becomes meaningfully testable.
- Combined with Plan 67-11 (Gap A closure: poller posts `.result` + Stop hook gate), Phase 67 should now be UAT-ready for full re-execution.
- No blockers for downstream phases.

## Self-Check: PASSED

- File checks: events_handler.go, events_handler_test.go, 67-UAT.md, 67-12-SUMMARY.md all present
- Commit checks: b73d938 (test), 9383bb4 (fix), 2b81c07 (docs) all in git log
- Allow-list pattern `case "", "thread_broadcast"` present in events_handler.go
- Forensic debug log `events: subtype filter dropped` present in events_handler.go
- All 20 sub-tests of TestEventsHandler_BotSelfMessageFiltered + TestEventsHandler_ThreadBroadcastPasses green
- `go vet ./pkg/slack/bridge/...` clean

---
*Phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch*
*Completed: 2026-05-03*
