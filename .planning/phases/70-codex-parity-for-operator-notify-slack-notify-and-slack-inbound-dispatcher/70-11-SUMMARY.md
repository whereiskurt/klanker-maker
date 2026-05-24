---
phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
plan: 11
subsystem: agent-run
tags: [path-b, gap-closure, jsonl-parse, synthetic-hook, operator-side]

requires:
  - phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
    provides: 70-09 UAT findings (SC-2 FAIL — operator-side km agent run --codex doesn't fire hook)
provides:
  - Post-`codex exec` JSONL parse + synthetic Stop hook invocation in BuildAgentShellCommands codex branch
  - Operator-side notify (email + Slack) for Codex agent runs WITHOUT depending on Codex's hook system (which doesn't fire)
  - SC-2 unblocked: km agent run --codex --prompt "..." → operator notify fires
  - SC-6 unblocked: KM_SLACK_THREAD_TS not set in this path → hook's Slack branch fires (gating asymmetry preserved)
affects:
  - Plan 70-09 (UAT) — SC-2/SC-6 now expected to PASS in re-run (Plan 70-12)
  - Plan 70-03 (km-notify-hook Codex Stop branch) — its `last_assistant_message` fast-path is now reachable on operator-side runs

tech-stack:
  added: []
  patterns:
    - "jq -rs slurp + `map(select) | last | .item.text` pattern for picking the FINAL agent_message in a Codex JSONL stream with multiple reasoning items"
    - "jq -n synthesizer to construct hook stdin payload from extracted JSONL fields — bridges Path B (no real hooks) with Plan 70-03's hook-payload contract"
    - "Non-fatal hook invocation (|| true) — a hook bug doesn't break the agent run itself"

key-files:
  created:
    - .planning/phases/70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher/70-11-PLAN.md
  modified:
    - internal/app/cmd/agent.go (BuildAgentShellCommands codex branch + ~13 LOC bash block)
    - internal/app/cmd/agent_test.go (TestBuildAgentShellCommands_Codex extended with Path B synthetic Stop test case)
---

# Plan 70-11 — Path B agent-run JSONL parse + synthetic Stop hook

## Outcome

**Operator-side `km agent run --codex` now notifies under Path B.** The 70-09 UAT found that this path was the last hook-dependent codepath that Plan 70-10's Path B didn't address. Plan 70-11 closes it.

## What changed

`internal/app/cmd/agent.go`, in the codex case of `BuildAgentShellCommands` (line ~1283), the script appended a post-exec block:

```bash
if [ -s "$RUN_DIR/output.json" ]; then
  KM_LAST_MSG=$(jq -rs 'map(select(.type=="item.completed" and .item.type=="agent_message")) | last | .item.text // ""' "$RUN_DIR/output.json" 2>/dev/null || echo "")
  KM_SID=$(jq -r 'select(.type=="thread.started") | .thread_id // empty' "$RUN_DIR/output.json" 2>/dev/null | head -1 || echo "")
  if [ -n "$KM_LAST_MSG" ]; then
    jq -n --arg msg "$KM_LAST_MSG" --arg sid "$KM_SID" \
      '{hook_event_name:"Stop", last_assistant_message:$msg, session_id:$sid}' \
      | /opt/km/bin/km-notify-hook Stop 2>>"$RUN_DIR/stderr.log" || true
  fi
fi
```

The block:
1. Guards on `output.json` having content (size > 0 → codex exec produced JSONL).
2. Extracts `last_assistant_message` from the LAST `item.completed` event with `item.type=agent_message`. The `-rs` (slurp) flag reads all JSONL lines into an array, then `map(select(...))` filters to agent_message items, `last` picks the final one (Codex may emit multiple reasoning messages per turn — only the final user-facing reply matters).
3. Extracts `thread_id` from the first `thread.started` event — same source Plan 70-10's poller uses.
4. Synthesizes the Stop hook payload via `jq -n`. This produces the exact stdin shape Plan 70-03's hook script reads on the Codex Stop branch.
5. Pipes the payload to `/opt/km/bin/km-notify-hook Stop`. The hook handles cooldown, env-gate checks (`KM_NOTIFY_ON_IDLE`, `KM_NOTIFY_EMAIL_ENABLED`, `KM_NOTIFY_SLACK_ENABLED`), and the email + Slack post.
6. Non-fatal (`|| true`) — a hook failure logs to stderr but doesn't fail the agent run itself.

## What Path B's two halves now look like

| Codex flow | How the Stop event reaches the notify hook |
|---|---|
| Slack inbound poller | Plan 70-05 + 70-10: poller parses JSONL, writes DDB row, posts directly to Slack thread (hook NOT invoked — `KM_SLACK_THREAD_TS` is set, which would silence the hook's Slack branch anyway) |
| Operator `km agent run --codex` | **Plan 70-11**: agent shell script parses JSONL, synthesizes Stop payload, pipes to km-notify-hook. `KM_SLACK_THREAD_TS` is NOT set on this path → hook's Slack branch posts to `#sb-{sandbox}`. SC-6's gating asymmetry preserved. |

Both halves now produce the SC-1..SC-10 behavior the original spec required, without depending on Codex actually firing hooks.

## Verification

`go test ./internal/app/cmd/... -run TestBuildAgentShellCommands_Codex -count=1 -v` PASSES all 4 subtests including the new "Path B synthetic Stop" case (asserts 6 substring markers in the codex branch's rendered shell script).

## What Plan 70-12 will verify end-to-end

Operator-driven UAT re-run on `learncodex` (signing-key fix pending, separately tracked):
- `km agent run --codex --prompt "What model are you?" --wait learncodex` → Slack post lands in `#sb-learncodex` within ~10 seconds
- Hook log shows km-notify-hook execution
- DDB rows from poller path remain agent_type-correct (Plan 70-10's prior validation)
