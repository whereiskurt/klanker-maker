---
status: diagnosed
phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
source:
  - 67-10-UAT.md (manual UAT script — Steps 1-17)
  - Live test on sandbox lrn2-16a1cff8 (alias l9), channel C0B1FLZL0NQ, 2026-05-03
started: 2026-05-03T08:00:00-04:00
updated: 2026-05-03T08:35:00-04:00
---

## Current Test

[testing halted at Step 6 — two blocker gaps must close before remaining steps are meaningful]

## Tests

### 1. Terraform infrastructure applied (Step 1 of 67-10-UAT.md)
expected: dynamodb-sandboxes (slack_channel_id-index GSI), dynamodb-slack-threads, lambda-slack-bridge all show "no changes" against current state
result: pass
evidence: All three modules `terragrunt plan` returned `No changes`. GSI present on km-sandboxes (alongside alias-index). Lambda IAM has dynamodb_slack_threads, sqs_send_inbound, ssm_signing_secret, dynamodb_sandboxes_pause_hint policies.

### 2. Signing secret configured (Step 2)
expected: km slack init writes /km/slack/signing-secret to SSM, returns Events API URL, force cold-starts bridge Lambda
result: pass
evidence: Used rotate-signing-secret path (avoids bot-token re-prompt). SSM param v2→v3. Bridge URL: https://6ov5pfv6ml3fjo66liqljsyazi0hsaad.lambda-url.us-east-1.on.aws/

### 3. Slack App config — Events URL + scopes (Step 3, USER)
expected: Slack reports Verified for url_verification challenge; message.channels event subscribed; channels:history + groups:history scopes present
result: pass
evidence: Operator confirmed Events URL verified, scopes present (km doctor `slack_app_events_subscription` check OK).

### 4. km doctor pre-flight (Step 4)
expected: 3 new checks fire — slack_inbound_queue_exists, slack_inbound_stale_queues, slack_app_events_subscription
result: pass
evidence: All 3 checks present and functional. (Note: check correctly detected 2 stale orphan queues from earlier dev cycles — surfaced as warnings, not test failure.)

### 5. km create with notifySlackInboundEnabled (Step 5)
expected: per-sandbox FIFO queue created, KM_SLACK_INBOUND_QUEUE_URL written to SSM, slack_channel_id stamped on DDB row, ready announcement posted to #sb-XXX
result: pass
evidence: Sandbox lrn2-16a1cff8 created. Queue km-slack-inbound-lrn2-16a1cff8.fifo present. SSM /sandbox/lrn2-16a1cff8/slack-inbound-queue-url written. DDB row has slack_channel_id=C0B1FLZL0NQ. Ready announcement posted at ts=1777810593.332719.

### 6. First turn — reply to ready announcement (Step 6)
expected: User posts in-thread "What model are you?"; within 60s Claude reply visible in-thread mentioning "Claude"; SQS depth returns to 0
result: issue
reported: "It shows me in the thread asking 'what model are you' and a reply '(no recent assistant text)'."
severity: blocker
evidence:
- Slack API conversations.replies confirms thread 1777810593.332719 has only 3 messages: ready announcement, user "What model are you using?", bot reply text="(no recent assistant text)\n"
- Agent run 20260503T122834Z output.json shows result="I'm Claude Opus 4.7 (1M context), model ID `claude-opus-4-7`." with cost $0.05 — agent SUCCEEDED
- Bridge log 12:28:42 shows "bridge: ok action=post channel=C0B1FLZL0NQ ts=1777811322.731899 status=200" — post succeeded but with the wrong body

### 7. Second turn — session continuity (Step 7)
expected: Reply "Repeat my last question verbatim" → Claude references prior turn (proves --resume works)
result: pending
reason: gated on Test 6 fix; not exercised

### 8. Top-level post starts new thread (Step 8)
expected: New top-level message → Claude replies threaded under that message, NOT under ready announcement
result: pending
reason: gated on Test 6 fix; not exercised

### 9-11. Pause / queue / resume drain (Steps 9-11)
expected: km pause → message queues during pause → km resume drains within 60s
result: pending
reason: gated on Test 6 fix; not exercised

### 12. km destroy drain (Step 12)
expected: km destroy emits drain log lines, deletes queue, archives channel
result: pending
reason: gated on Test 6 fix; not exercised

### 13. km doctor post-cleanup (Step 13)
expected: All 3 inbound checks remain OK after destroy
result: pending
reason: gated on Test 6 fix; not exercised

### 14. Bot-loop prevention (Step 14)
expected: Phase 63 hook posts to channel; bot's own post NOT re-enqueued; queue depth stays 0
result: issue
reported: "channel_join system event from operator's Slack Connect acceptance was enqueued and ran a wasted Claude turn"
severity: blocker
evidence:
- Slack API conversations.history confirms message 1777810693.749309 has subtype="channel_join", user="U07KW5SBGQH" (operator), text="<@U07KW5SBGQH> has joined the channel"
- Bridge log 12:18:15 shows "events: enqueued sandbox=lrn2-16a1cff8 channel=C0B1FLZL0NQ thread_ts=1777810693.749309 event_id=Ev0B1C3T8KM0"
- Agent run 20260503T122819Z processed it, produced "Welcome message noted — let me know what you'd like to work on." at $0.055 cost
- Reply post failed with cannot_reply_to_message (system messages are un-threadable)
- Filter pkg/slack/bridge/events_handler.go:216-222 only catches subtype ∈ {bot_message, message_changed, message_deleted}; channel_join/leave/topic/purpose/name/archive/unarchive/pinned_item/etc. all bypass

### 15. Slack Connect invite gate (Step 15, USER)
expected: Uninvited user's message dropped by bridge
result: pending
reason: gated on Test 14 fix (filter must distinguish system events from impostor users)

### 16-17. Signing secret rotation (Steps 16-17)
expected: Old secret → 401; new secret → 200 after Lambda cache TTL
result: pending
reason: independent of Tests 6/14 but deferred to keep gap closure tight

## Summary

total: 17
passed: 5
issues: 2
pending: 10
skipped: 0

## Gaps

- truth: "Stop hook posts the agent's actual reply text to Slack when the agent run produces assistant text"
  status: failed
  reason: "User reported: '(no recent assistant text)' appears in-thread instead of Claude's actual reply ('I'm Claude Opus 4.7...')"
  severity: blocker
  test: 6
  root_cause: "Stop hook (pkg/compiler/userdata.go:441-448) parses payload.transcript_path JSONL for assistant text. In `claude -p` (--print) mode used by km-slack-inbound-poller (userdata.go:1015), the JSONL transcript is either (a) not flushed by Stop hook firing time, (b) not at the path passed in the payload, or (c) shaped differently than the jq query expects. The poller already has the canonical reply in /workspace/.km-agent/runs/<id>/output.json (.result field) but the Stop hook never consults it."
  artifacts:
    - path: "pkg/compiler/userdata.go:441-448"
      issue: "Stop hook reads transcript JSONL via tail | jq — fails silently to '(no recent assistant text)' fallback in --print mode"
    - path: "pkg/compiler/userdata.go:947-1051"
      issue: "Poller produces output.json but does not post the .result field to Slack itself; relies on hook for reply"
  missing:
    - "Either: (preferred) move Slack reply post out of Stop hook into the poller's post-claude-exit code path — read .result from output.json and call km-slack post directly. The Stop hook keeps doing email/idle notifications; the inbound reply path becomes deterministic."
    - "Or: change Stop hook to ALSO try $RUN_DIR/output.json (.result field) when transcript JSONL parse returns empty — but this requires the hook to know which RUN_DIR is current. (More fragile than the poller-driven path.)"
    - "Add E2E test: assert thread-reply body matches output.json .result for a successful run."
  debug_session: ""

- truth: "Bot-loop filter drops all non-user-text Slack message events (system events, bot posts, deleted/edited messages) before SQS write"
  status: failed
  reason: "User reported phantom enqueue + wasted agent turn after Slack Connect invite acceptance — channel_join system event passed the filter"
  severity: blocker
  test: 14
  root_cause: "isBotLoop (pkg/slack/bridge/events_handler.go:216-233) catches ONLY subtypes ∈ {bot_message, message_changed, message_deleted}. Slack emits many other non-user message subtypes that should also be excluded: channel_join, channel_leave, channel_topic, channel_purpose, channel_name, channel_archive, channel_unarchive, channel_posting_permissions, pinned_item, unpinned_item, channel_convert_to_private, channel_convert_to_public, ekm_access_denied, file_share, file_comment, etc. Allow-list (only `subtype == ''` = real human turn) is safer than deny-list — Slack adds new subtypes over time, every one is a future regression."
  artifacts:
    - path: "pkg/slack/bridge/events_handler.go:216-233"
      issue: "isBotLoop deny-list doesn't include channel_join (and other system-message subtypes)"
    - path: "pkg/slack/bridge/events_handler.go:121-130"
      issue: "Pre-filter only checks msg.Type == 'message'; doesn't gate on subtype empty"
  missing:
    - "Switch isBotLoop to ALLOW-list semantics: 'real user turn' = subtype is empty string OR subtype == 'thread_broadcast'. Reject everything else."
    - "Keep the existing bot_id and bot-user-ID checks as a second-line defence."
    - "Update events_handler unit tests to cover channel_join, channel_leave, channel_topic, channel_purpose, channel_name, channel_archive, channel_unarchive, pinned_item, unpinned_item, file_share, thread_broadcast (positive case)."
    - "Add log line at debug level: 'events: subtype filter dropped' with subtype value, for forensics."
  debug_session: ""

## Notes

- **Both gaps were diagnosed live during UAT** using bridge CloudWatch logs + Slack API ground truth + source code reading. Skipped the workflow's parallel debug-agent dispatch since the diagnoses are already concrete and evidenced.
- **Recommended fix order:** Gap A (Stop hook → poller-driven reply) first — without it, no replies make it to Slack at all, so Gap B is invisible from the operator's seat. Gap B independent of A but easy to land alongside.
- **Sandbox lrn2-16a1cff8 is left running** for re-test after fixes land. TTL is 8h. Channel C0B1FLZL0NQ retains the broken-state evidence.
- **Stale resources (3) deferred** until after gap closure: 2 orphan SQS queues (lrn2-429624f2, lrn2-e218a532), 1 stale Slack channel (C0B0VSKMDBR). Tracked separately.
