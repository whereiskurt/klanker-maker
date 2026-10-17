# Cold-create boot race — design

**Date:** 2026-10-17. **Status:** approved in chat (operator: "defaults confirmed"). **Scope:** the
four source-aware inbound pollers (Slack, GitHub, HackerOne, webhook); the mail poller is
untouched. Bounded fix, one phase.

## 1. Problem

A cold-created sandbox answers its first prompt about 30 minutes late. Reproduced four times
on 2026-10-16/17 (`sl-aff46386`, `sl-a41d1215`, `sl-648f21e9`, `sl-33ae9a96`, all extending
`base/userinit`). Every time the poller journal reads:

```
[km-inbound-poller] WARNING: agent 'claude' not on PATH — dispatched turns will NOT run; …
[km-slack-inbound-poller] WARN: agent run failed (exit 127), message returns to queue
```

and the reply lands ~1800 s later, which is the per-sandbox FIFO's `VisibilityTimeout`
(Phase 119's new-queue default).

## 2. Root cause, stated precisely

`pkg/compiler/userdata.go` starts the pollers in **section 5** (`systemctl enable … /
systemctl restart …` around line 5380, gated per poller on `{{ if .SlackInboundEnabled }}`
etc.) and only reaches **section 7.5 "Profile init"** (`/tmp/km-init.sh`, line ~5861) several
sections later. `initCommands` is where every shipped profile installs `claude`/`codex`
(`base/userinit`); the platform installs no agent. So on first boot the poller is up, drains
the cold-create envelope (or the first mention), dispatches, and the turn exits 127 before the
agent exists. The poller's own startup comment ("initCommands have already run by now",
line ~1986) is false for a first boot; it is true only after a stop/resume, where userdata does
not re-run.

Two independent faults compound:
1. **Ordering.** Nothing makes the first receive wait for profile init.
2. **No recovery.** On exit 127 the poller leaves the message in flight ("returns to queue"
   when the visibility timeout expires). The Phase 119 heartbeat is killed after the turn, so
   the base 1800 s applies, and nothing shortens it.

## 3. Decisions

| Decision | Choice | Why |
|---|---|---|
| Gate the first receive on an init marker | Yes: 7.5 writes `/var/lib/km/init-done` when `/tmp/km-init.sh` returns, **whether it succeeded or failed**, and also when there is no init script to run. Each inbound poller waits for that file before its first `receive-message`, bounded at **1200 s**, logging once when it starts waiting and once when it proceeds (with the reason: marker seen / timeout). | A marker written only on success would hang the poller forever on a failed init, and a failed init already leaves `/var/lib/km/init-failed` for `km status`/`km doctor`. The bound keeps a wedged init from silencing the box; 1200 s covers the longest shipped `initCommands` with margin. |
| Marker location | `/var/lib/km/` | Persists across stop/resume (userdata never re-runs; a resumed box must not wait) and across hibernate. `/run` would be cleared on a cold boot after `km stop`, re-triggering the wait on a box whose init ran long ago. |
| Re-check the agent after the wait | Yes: the existing `command -v "$AGENT"` warning moves to after the gate, so it reports a profile that truly lacks the agent, not a race. | Today's warning fires on every cold boot and trains operators to ignore it. |
| Recovery on exit 127 | After a turn whose exit code is 127, the poller sets the message's visibility to **60 s** (`change-message-visibility --visibility-timeout 60`) instead of leaving the 1800 s to expire. Other non-zero exits keep today's behaviour. | 127 means "agent binary missing", which is either the race (the gate now prevents it) or a profile with no agent install, where quick retries cost little and the Phase 99.1 shared DLQ's `maxReceiveCount` still bounds them. Setting 0 would hot-loop. |
| Scope | Slack, GitHub, H1, webhook pollers: all four get the gate and the 127 rule in one commit. | Same dispatch shape, same race; fixing one leaves the GitHub/H1 first turn 30 minutes late. |
| `km doctor` | Not in this phase. | The 127 line is now rare; a doctor check needs a journal read per sandbox (SSM) and is not worth it yet. |

## 4. Changes

- `pkg/compiler/userdata.go` §7.5: after the `if /tmp/km-init.sh; then … fi` block (both
  arms) and in the "No init script found" arm, `touch /var/lib/km/init-done`.
- Each inbound poller script (the four `SLACKINBOUND`/`GITHUBINBOUND`/`H1INBOUND`/
  `WEBHOOKINBOUND` heredocs), before the main `while true` loop:
  ```bash
  # Cold-create boot race: section 5 starts this poller before section 7.5 runs the
  # profile's initCommands (which install the agent). Wait for the init marker, bounded.
  if [ ! -e /var/lib/km/init-done ]; then
    echo "[km-inbound-poller] waiting for profile init (/var/lib/km/init-done)"
    _w=0; while [ ! -e /var/lib/km/init-done ] && [ "$_w" -lt 1200 ]; do sleep 5; _w=$((_w+5)); done
    if [ -e /var/lib/km/init-done ]; then echo "[km-inbound-poller] profile init done after ${_w}s"; else echo "[km-inbound-poller] WARN: profile init marker not seen after ${_w}s; proceeding"; fi
  fi
  command -v "$AGENT" >/dev/null 2>&1 || echo "[km-inbound-poller] WARNING: agent '$AGENT' not on PATH — …"   # moved here
  ```
- Each poller's failure arm: `if [ "$RUN_EXIT" -eq 127 ]; then aws sqs change-message-visibility --queue-url "$QUEUE_URL" --receipt-handle "$RECEIPT" --visibility-timeout 60 --region "$REGION" 2>/dev/null || true; fi` with a log line naming the shortened retry.
- Frozen pre-92 golden hand-patched with exactly these hunks (never `CAPTURE_PRE92_BASELINE=1`).
- Docs: `docs/operational-gotchas.md` new entry; `docs/slack-default-route.md` §11 item closed;
  CLAUDE.md two-line bullet. Memory `project_cold_create_first_turn_boot_race` updated to "fixed".

## 5. Tests

- `pkg/compiler`: a rendered-bash test runs the gate snippet under `bash` with a temp marker
  dir (the literal `/var/lib/km/init-done` rewritten into the temp dir as the final-post test
  does): marker present ⇒ no wait; marker appearing after 7 s ⇒ proceeds with "done after";
  never appearing with the bound shrunk to 10 s ⇒ "not seen … proceeding".
- A name-agnostic guard (like `TestEveryLocalBinPrependReassertsShimDir`): every heredoc that
  defines `dispatch_as_sandbox` carries the init gate before its first `receive-message` and the
  127 visibility rule after `agent run failed`.
- §7.5 test: `TestUserdataInitBlock_*` extended so both the success and failure arms leave the
  marker.

## 6. Deploy and UAT

`make build-lambdas` + `km init --dry-run=false` (create-handler userdata). Existing boxes keep
the race until recreate. UAT: `km destroy` the default sandbox, @-mention from a channel, and the
first reply must arrive within boot time plus seconds, never 30 minutes; the poller journal shows
"waiting for profile init" then "profile init done after Ns" and no exit 127.

## 7. Live UAT (2026-10-17, install prefix `km`)

| Step | Result |
|---|---|
| `km destroy` the default sandbox, @-mention from a channel at 13:19:37Z | **PASS**: cold-create published 13:19:38Z; box running 13:20:59Z; poller journal `waiting for profile init (/var/lib/km/init-done)` at 13:22:15Z, `profile init done after 90s` at 13:23:45Z (marker mtime 13:23:40); `Posted reply` at 13:23:55Z. First reply **4 min 18 s** after the mention, no `exit 127`, no `not on PATH` warning. Before the fix the same step took ~32 minutes four times running. |
