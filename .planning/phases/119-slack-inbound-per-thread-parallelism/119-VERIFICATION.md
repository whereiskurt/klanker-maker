---
phase: 119-slack-inbound-per-thread-parallelism
verified: 2026-06-25T17:00:00Z
status: passed
score: 9/9 must-haves verified
re_verification: false
---

# Phase 119: Slack Inbound Per-Thread Parallelism Verification Report

**Phase Goal:** Different Slack threads to the same sandbox run in PARALLEL while messages within a thread stay serial+ordered, bounded by an operator concurrency cap. Dormant by default (cap=1 == today's serial behaviour).
**Verified:** 2026-06-25
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | Bridge MessageGroupId == threadTS at both Send sites (files + no-files paths) | VERIFIED | `events_handler.go:470` and `:490` both pass `threadTS` as group arg; confirmed by `TestEventsHandler_GroupID_IsThreadTS_NoFiles` / `_IsMsgTS_TopLevel` tests GREEN |
| 2 | Top-level mention groups by msg.TS (threadTS fallback) | VERIFIED | `events_handler.go:403-405` sets `threadTS = msg.TS` when `msg.ThreadTS == ""` before both Send calls |
| 3 | km validate WARNs when maxConcurrentThreads>1 without perSandbox+inbound.enabled | VERIFIED | `validate.go:364-377` S-maxconcurrency rule; `TestValidate_SlackInbound_MaxConcurrency_WarnsWithoutPerSandbox` GREEN |
| 4 | KM_SLACK_MAX_CONCURRENCY emitted only when cap>1 (dormancy: cap=1/absent emits nothing) | VERIFIED | `userdata.go:5602-5603` emit-only-when->1 guard; dormancy test asserts no assignment line; confirmed live on dormant-51f48ae0 (SSM send-command grep → 0) |
| 5 | Bounded concurrent dispatch with wait -n counting semaphore | VERIFIED | `userdata.go:1671-1713`: `MAX="${KM_SLACK_MAX_CONCURRENCY:-1}"`, `BATCH=$MAX`, `inflight` counter, `wait -n || true` drain; golden hand-patched at `:2025`; UAT A1 (3 RUN_DIRs in 4s window) + A3 (cap honored at 3) PASS |
| 6 | Ack-after-completion: delete-message is the LAST step of the subshell after km-slack post | VERIFIED | `userdata.go:2188` delete-message is post the km-slack post at `:2100`; UAT A2 proves FIFO holds next message in-flight until 1st acked (10s gap) |
| 7 | ChangeMessageVisibility heartbeat ticker killed after turn | VERIFIED | `userdata.go:1722-1737` starts HB_PID background loop every 120s; `:2199-2202` kills it post-turn; also killed on early-exit paths `:1792-1793`, `:1940-1941` |
| 8 | Idempotency guard: last_processed_event_ts read at turn start, written at DDB put | VERIFIED | `userdata.go:1774-1793` reads guard + skip-delete path; `:2123-2137` writes `last_processed_event_ts` in put-item; UAT A6 confirms bridge-level dedup (and code shows on-box layer) |
| 9 | Inbound FIFO queue base VisibilityTimeout raised to 1800s | VERIFIED | `pkg/aws/sqs.go:76` `const slackInboundVisibilityTimeout = "1800"`; `:135` used in `inboundQueueAttrs`; `TestInboundQueueAttrs_VisibilityTimeout` GREEN; UAT A4 confirms live queue attribute |

**Score:** 9/9 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/profile/types.go` | MaxConcurrentThreads *int on NotificationSlackInboundSpec | VERIFIED | Line 266: `MaxConcurrentThreads *int \`json:"maxConcurrentThreads,omitempty"\`` |
| `pkg/profile/schemas/sandbox_profile.schema.json` | maxConcurrentThreads property (integer, minimum:1) under inbound | VERIFIED | Line 773: `"maxConcurrentThreads": {"type": "integer", "minimum": 1, ...}` |
| `pkg/slack/bridge/events_handler.go` | threadTS-grouped Send at both files + no-files paths | VERIFIED | Lines 470 + 490: `h.SQS.Send(..., threadTS, dedupID)` |
| `pkg/slack/bridge/aws_adapters.go` | Stale sandboxID doc comment corrected | VERIFIED | Line 827: comment now reads "Slack thread timestamp (threadTS)" |
| `pkg/aws/sqs.go` | slackInboundVisibilityTimeout const + inboundQueueAttrs uses it | VERIFIED | Lines 76 + 135; const "1800" replacing literal "30" |
| `pkg/profile/validate.go` | S-maxconcurrency WARN rule at path spec.notification.slack.inbound.maxConcurrentThreads | VERIFIED | Lines 364-377 |
| `pkg/compiler/userdata.go` | KM_SLACK_MAX_CONCURRENCY emit-only-when->1 block; full bounded-concurrent poller | VERIFIED | Lines 5597-5604 (emit); 1671-2210 (poller rewrite with semaphore/heartbeat/ack-after/dedup) |
| `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` | Hand-patched to match new poller bash | VERIFIED | Lines 1984, 2025 (`wait -n`), 2043 (`change-message-visibility`), 2087 (`last_processed_event_ts`) present; CAPTURE flag NOT used |
| `profiles/learn.v2.parallel.yaml` | Demo profile with maxConcurrentThreads: 3, validates clean | VERIFIED | Line 190: `maxConcurrentThreads: 3`; profile validates (km validate passes per UAT Task 1) |
| `docs/slack-notifications.md` | Phase 119 operator section | VERIFIED | Line 2900: `## Phase 119 — Slack inbound per-thread parallelism`; 11 Phase-119 mentions |
| `CLAUDE.md` | Phase 119 summary block + Where-to-look row | VERIFIED | Lines 29 + 203: Phase 119 complete block with deploy surface and Where-to-look row |
| `skills/slack/SKILL.md` | Per-thread parallelism note at inbound section | VERIFIED | Lines 178-186: Phase 119 sub-section with maxConcurrentThreads field docs |
| `.claude-plugin/plugin.json` + `marketplace.json` | Version bumped 0.4.11 → 0.4.12 | VERIFIED | Both files contain `"version": "0.4.12"` |
| `internal/app/cmd/create_slack_inbound_test.go` | Stale "30" assertion patched to "1800" | VERIFIED | Line 201: asserts `"1800"` with Phase-119 comment |
| `internal/app/cmd/create_github_inbound_test.go` | Stale "30" assertion patched to "1800" | VERIFIED | Line 100: asserts `"1800"` with Phase-119 comment |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `events_handler.go Handle()` | `h.SQS.Send` | groupID arg == threadTS | WIRED | Lines 470 + 490 confirmed; tests GREEN |
| `inboundQueueAttrs` | Slack inbound queue | slackInboundVisibilityTimeout const ("1800") | WIRED | `sqs.go:76,135` |
| `userdata.go NotifyEnv block` | `/etc/km/notify.env` + `profile.d` | `notifyEnv["KM_SLACK_MAX_CONCURRENCY"]` when cap>1 | WIRED | Lines 5602-5603 inside `if slackInboundEnabled(p)` |
| `validate.go S-maxconcurrency rule` | cap>1 without perSandbox/inbound.enabled | IsWarning ValidationError at correct path | WIRED | Lines 364-377; test `TestValidate_SlackInbound_MaxConcurrency_WarnsWithoutPerSandbox` GREEN |
| `poller receive loop` | backgrounded subshell | `wait -n` counting semaphore + inflight counter | WIRED | Lines 1711-1713, 2206; UAT A1/A3 live proof |
| `subshell turn` | `sqs delete-message` | ack AFTER km-slack post | WIRED | Line 2188 is last step of subshell; UAT A2 proves FIFO holds in-flight message |
| `subshell turn` | `ChangeMessageVisibility` | heartbeat HB_PID ticker, killed post-turn | WIRED | Lines 1722-1737 (start), 2199-2202 (kill) |
| `redelivered event` | `km-slack-threads` DDB row | `last_processed_event_ts` guard read+write | WIRED | Lines 1774-1793 (read+skip), 2123-2137 (write) |

### Requirements Coverage

| Requirement | Source Plans | Description | Status | Evidence |
|-------------|-------------|-------------|--------|---------|
| P119-A | 119-01, 119-02, 119-03 | Bridge groups by threadTS at both Send sites | SATISFIED | `events_handler.go:470,490`; bridge tests GREEN |
| P119-B | 119-01, 119-04, 119-05 | Bounded concurrent dispatch (wait -n semaphore, KM_SLACK_MAX_CONCURRENCY) | SATISFIED | `userdata.go:1671-2210`; UAT A1/A3 PASS |
| P119-C | 119-01, 119-03, 119-05 | KM_SLACK_MAX_CONCURRENCY env emission (only when cap>1) | SATISFIED | `userdata.go:5597-5604`; compiler tests GREEN; UAT A5 PASS |
| P119-D | 119-04, 119-05 | Ack-after-completion (delete last, after post) | SATISFIED | `userdata.go:2188`; UAT A2 PASS (10s ordering gap) |
| P119-E | 119-01, 119-02, 119-03, 119-04, 119-05 | Visibility heartbeat + 1800s base VisibilityTimeout | SATISFIED | `sqs.go:76,135`; `userdata.go:1722-1737,2199-2202`; UAT A4 PASS |
| P119-F | 119-04, 119-05 | last_processed_event_ts idempotency guard (fail-open) | SATISFIED | `userdata.go:1774-1793,2123-2137`; UAT A6 PASS |
| P119-G | 119-01, 119-03 | km validate WARNS cap>1 without perSandbox+inbound.enabled | SATISFIED | `validate.go:364-377`; profile tests GREEN |

All 7 requirement IDs (P119-A through P119-G) fully accounted for and SATISFIED.

### Anti-Patterns Found

No blockers or warnings found. Scanned `events_handler.go`, `sqs.go`, `validate.go`, `userdata.go`. The `return nil` occurrences are legitimate error-path returns (not stubs). No TODO/FIXME/placeholder comments in modified production code.

One cosmetic non-blocking item noted in UAT: `[: : integer expression expected` at poller line 122 when SQS long-poll returns empty batch (COUNT is empty string vs "0"). This is pre-existing behavior, not introduced by Phase 119, and documented in 119-UAT.md § Notes.

### Human Verification Required

The live synthetic-HMAC E2E was completed as part of Plan 05 (Task 2, `autonomous: false`). All 6 assertions recorded in `119-UAT.md` with evidence:

- A1 (parallelism): PASS — 3 distinct RUN_DIRs minted in 4s window from one poller PID
- A2 (per-thread ordering): PASS — 10s gap between same-thread events, FIFO held in-flight
- A3 (cap enforcement): PASS — never >3 concurrent runs under cap=3
- A4 (heartbeat/no-dup): PASS — 1800s queue attr confirmed live; heartbeat + ack-after code verified in deployed userdata
- A5 (dormant regression): PASS — SSM send-command confirms `grep -c KM_SLACK_MAX_CONCURRENCY /etc/km/notify.env` → 0 on cap=1 box, `KM_SLACK_MAX_CONCURRENCY=3` on cap=3 box
- A6 (dedup): PASS — bridge nonce-dedup drops replay in <10ms with no enqueue

### Additional Mid-UAT Fixes Verified

Two stale-assertion bugs found and fixed mid-UAT (documented in 119-UAT.md § Bug found + fixed):

1. `internal/app/cmd/create_slack_inbound_test.go:201` — stale `"30"` assertion updated to `"1800"`
2. `internal/app/cmd/create_github_inbound_test.go:100` — stale `"30"` assertion updated to `"1800"`

Both files now pass `go test ./internal/app/cmd/` (confirmed: exit 0, 468s).

### Test Suite

`go test ./... -count=1 -timeout 600s` — EXIT 0 (full suite green, no FAIL lines in output). All packages ok including the 4 Phase-119 core packages (`pkg/slack/bridge`, `pkg/profile`, `pkg/compiler`, `pkg/aws`) and the patched `internal/app/cmd`.

---

_Verified: 2026-06-25_
_Verifier: Claude (gsd-verifier)_
