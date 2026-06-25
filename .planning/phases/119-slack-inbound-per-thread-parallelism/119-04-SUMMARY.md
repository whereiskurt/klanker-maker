---
phase: 119-slack-inbound-per-thread-parallelism
plan: "04"
subsystem: pkg/compiler
tags: [slack, inbound, poller, concurrency, ack-after-completion, heartbeat, idempotency, golden]
dependency_graph:
  requires: [119-01, 119-03]
  provides: [bounded-concurrent-poller, ack-after-completion, visibility-heartbeat, idempotency-guard, run-id-uniqueness]
  affects: [pkg/compiler/userdata.go, pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh]
tech_stack:
  patterns: [bash-counting-semaphore, ack-after-completion, sqs-heartbeat, ddb-idempotency-guard]
key_files:
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh
    - pkg/compiler/userdata_slack_inbound_test.go
decisions:
  - "Idempotency guard uses DDB last_processed_event_ts keyed on InboundQueueBody.EventTS (= msg.TS from events_types.go:44) — Option C from research; no extra Slack API call"
  - "delete-message moved to AFTER km-slack post (ack-after-completion reversal P119-D); old 'Ack first' comment removed"
  - "dormancy tests updated to check KM_SLACK_MAX_CONCURRENCY= (env assignment) not substring match, since poller bash always references ${KM_SLACK_MAX_CONCURRENCY:-1}"
  - "TestUserdata_PollerPostsAfterDeleteMessage renamed to TestUserdata_PollerDeleteAfterPost to reflect the reversed ordering"
  - "Frozen golden replaced by Python-based surgical patch (start=echo-Starting line, end=before-SLACKINBOUND marker); no CAPTURE flag used"
metrics:
  duration: 661s
  completed: "2026-06-25"
  tasks_completed: 2
  files_modified: 3
---

# Phase 119 Plan 04: Bounded-Concurrent Poller Rewrite Summary

Rewrote the sandbox-side Slack inbound poller in `pkg/compiler/userdata.go` from a serial blocking executor into a bounded-concurrent dispatcher with ack-after-completion, per-message visibility heartbeat, RUN_ID uniqueness, and a DDB-backed crash-redelivery idempotency guard. Hand-patched the FROZEN `userdata_learn_v2_pre92_baseline.golden.sh` to match.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Rewrite poller into bounded-concurrent ack-after-completion dispatcher | 31275344 | pkg/compiler/userdata.go, pkg/compiler/userdata_slack_inbound_test.go |
| 2 | Hand-patch the frozen learn.v2 byte-identity golden | 4267ee9d | pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh |

## What Was Implemented

### P119-B: Counting semaphore (bounded concurrency)

Before the `while true` loop:
```bash
MAX="${KM_SLACK_MAX_CONCURRENCY:-1}"
[ "$MAX" -lt 1 ] && MAX=1
BATCH=$MAX; [ "$BATCH" -gt 10 ] && BATCH=10
inflight=0
```

The receive call now uses `--max-number-of-messages "$BATCH"`. Messages are iterated with `for _MSG_IDX in $(seq 0 $((COUNT-1)))`. Before backgrounding each:
```bash
while [ "$inflight" -ge "$MAX" ]; do
  wait -n 2>/dev/null || true
  inflight=$((inflight-1))
done
```
Each message runs in `( ... ) &` with `inflight=$((inflight+1))` after backgrounding.

### P119-D: Ack-after-completion

The `delete-message` was moved from its old position before `km-slack post` ("Ack first") to AFTER the Slack post completes — the last substantive step of the subshell's success branch. This reversal is required for per-thread FIFO ordering: the message must stay in-flight until the turn is completely done so SQS does not release the next message for that thread while the current turn is still running.

### P119-E: Visibility heartbeat

Replaces the old one-shot 300s extension with a per-message background ticker inside the subshell:
```bash
( while true; do sleep 120
    aws sqs change-message-visibility --queue-url "$QUEUE_URL" \
      --receipt-handle "$RECEIPT" --visibility-timeout 360 --region "$REGION" 2>/dev/null || true
  done ) &
HB_PID=$!
```
Killed unconditionally after the turn: `kill "$HB_PID" 2>/dev/null || true; wait "$HB_PID" 2>/dev/null || true`. The `|| true` swallows `ReceiptHandleIsInvalid` after delete.

### P119-F: Idempotency guard (crash-redelivery dup suppression)

Event identity source: `InboundQueueBody.EventTS` (`json:"event_ts"` in `events_types.go:44`) = `msg.TS` (the Slack message timestamp, set at `events_handler.go:466`). This field is guaranteed non-empty for well-formed SQS bodies on both the files and no-files paths.

At turn start (after DDB get-item that reads the thread row):
```bash
LAST_EVENT=$(echo "$DDB_ITEM" | jq -r '.Item.last_processed_event_ts.S // empty' 2>/dev/null || true)
if [ -n "$EVENT_TS" ] && [ -n "$LAST_EVENT" ] && [ "$EVENT_TS" = "$LAST_EVENT" ]; then
  echo "[km-slack-inbound-poller] duplicate event $EVENT_TS already processed, skipping (idempotency guard)"
  aws sqs delete-message ... && exit 0
fi
```

At turn success (put-item), adds `"last_processed_event_ts":{"S":"$EVENT_TS"}` alongside the existing session/agent/turn attrs.

Fail-OPEN: if DDB read errors or EVENT_TS is empty, processing proceeds normally.

### Pitfall 1 fix: RUN_ID uniqueness

```bash
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$-$RANDOM"
```
Computed INSIDE the subshell so concurrent jobs dispatched in the same second get distinct run dirs under `/workspace/.km-agent/runs/`.

### Test updates

- `TestUserdata_PollerPostsAfterDeleteMessage` → `TestUserdata_PollerDeleteAfterPost`: flipped the assertion to verify delete comes AFTER km-slack post (Phase 119 ack-after-completion reversal).
- `TestUserdata_SlackInbound_MaxConcurrency_AbsentWhenCap1` / `AbsentWhenNil`: changed to check `KM_SLACK_MAX_CONCURRENCY=` (env assignment) instead of the plain substring, because the poller bash always embeds `${KM_SLACK_MAX_CONCURRENCY:-1}` as a runtime default reference regardless of cap value.

## Deviations from Plan

### Auto-fixed: Dormancy test assertion mismatch

**Found during:** Task 1 test run
**Issue:** Tests `TestUserdata_SlackInbound_MaxConcurrency_AbsentWhenCap1` and `AbsentWhenNil` checked for `KM_SLACK_MAX_CONCURRENCY` substring anywhere in userdata. The new poller bash always contains `${KM_SLACK_MAX_CONCURRENCY:-1}` as a runtime default, which would always match.
**Fix:** Updated tests to check for `KM_SLACK_MAX_CONCURRENCY=` (the env assignment form) which only appears in the notify.env section when cap > 1 is explicitly set.
**Files modified:** pkg/compiler/userdata_slack_inbound_test.go

### Auto-fixed: Frozen golden comment contained `KM_SLACK_MAX_CONCURRENCY=`

**Found during:** Task 1 test debug
**Issue:** Comment `# KM_SLACK_MAX_CONCURRENCY=1 (default)...` in the poller bash matched the `=` test.
**Fix:** Rewrote the comment to not contain the `=` form.
**Files modified:** pkg/compiler/userdata.go

### Auto-fixed: Malformed-body check variables used unique names

**Found during:** Task 1 first test run
**Issue:** Used `_CHANNEL_CHK` / `_TEXT_CHK` etc. in the outer-loop malformed check; test `TestUserdata_SlackInbound_AllowsEmptyTextWhenAttachments` checks for exact bash fragment `[ -z "$TEXT" ] && [ "$ATTACH_COUNT" -eq 0 ]`.
**Fix:** Changed outer-loop pre-check to use the standard variable names `CHANNEL`/`THREAD_TS`/`TEXT`/`ATTACH_COUNT`.
**Files modified:** pkg/compiler/userdata.go

## Golden Discipline

- `TestUserdataLearnV2Phase92ByteIdentity` GREEN via hand-patch
- `TestUserdataKmPrefixByteIdentity` GREEN (unaffected, no poller refs)
- `userdata_h1_byte_identity` and `additional_volume_only` goldens UNTOUCHED (0 poller refs confirmed)
- No `CAPTURE_PRE92_BASELINE=1` flag used
- Patch method: Python script replacing the echo-Starting line through done-while-true in the golden, using the freshly rendered output as source of truth

## Key Decisions Made

1. **Idempotency keyed on `event_ts` (Option C)**: DDB `last_processed_event_ts` on the thread row, checked at turn start, written at turn success. No Slack API scan, no RUN_ID-based scheme. Source field: `InboundQueueBody.EventTS` = `msg.TS` from the bridge.
2. **Ack-after reversal is unconditional**: The message always stays in-flight until the Slack post completes. For cap=1 (serial), this is safe and correct (the old ack-first was an optimization that traded correctness for a narrower dup window — the idempotency guard now covers that window).
3. **Heartbeat as primary mechanism**: Belt-and-suspenders with the 1800s queue base (raised in Plan 02). The heartbeat covers existing 30s sandboxes until they are recreated.

## Deploy Note

Profile field + poller bash → `make build-lambdas` + `km init --dry-run=false` (NOT `--sidecars`; the create-handler zip carries the new userdata). Existing sandboxes need `km destroy && km create` to pick up the new poller.

## Self-Check

- pkg/compiler/userdata.go modified: FOUND
- pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh modified: FOUND
- Commit 31275344 (Task 1): FOUND
- Commit 4267ee9d (Task 2): FOUND

## Self-Check: PASSED
