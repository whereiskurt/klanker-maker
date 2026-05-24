---
title: Phase 70 follow-up — cross-agent switch leaks prior agent's session UUID into new agent's --resume
area: compiler-poller
created: 2026-05-24
origin: Phase 70 SC-10 UAT 2026-05-24
---

### Problem
During SC-10 cross-agent switch live UAT, journal showed:

```
KM_SLACK_THREAD_TS='1779655948.659999'  ← new thread spawned correctly
claude -p ... --resume 019e5ba3-1864-7672-b760-8d90f5e78672  ← but resumes the PRIOR codex session!
```

The session UUID `019e5ba3-...` is the **Codex** session from the old thread (DDB row keyed on old thread_ts). Plan 70-06 Task 3's state-rewrite step rewrites `THREAD_TS`, `EFFECTIVE_AGENT`, and `PROMPT_FILE` for the dispatch fork, but it does NOT null out `CLAUDE_SESSION`. So the new agent's dispatch path picks up the prior agent's session UUID and passes it as `--resume`.

Result: claude (or whichever new agent) tries to resume a session that doesn't exist in its session store. In this UAT the dispatch failed for an orthogonal reason (no claude auth), so the bug was harmless. On a working sandbox, the new agent would either error out or behave unexpectedly.

### Fix
In `pkg/compiler/userdata.go`, the DO_SWITCH=1 block's state-rewrite section needs:

```bash
CLAUDE_SESSION=""   # new agent does a FIRST turn, not a resume
```

This must happen alongside the existing THREAD_TS/EFFECTIVE_AGENT/PROMPT_FILE rewrites, BEFORE falling through to the dispatch fork.

### Test
Add a unit test alongside `TestPoller_CrossAgentSwitch_OrderingFetchesOldPermalinkFirst` that asserts the switch block contains a `CLAUDE_SESSION=""` (or equivalent) reset.

### Files
- `pkg/compiler/userdata.go` (DO_SWITCH=1 state-rewrite block in the slack-inbound-poller heredoc)
- `pkg/compiler/userdata_prefix_test.go` (new assertion)

### Verification
After fix: SC-10 retry should show the new dispatch line with NO `--resume` flag (claude) or no `resume` subcommand (codex), confirming a fresh first turn on the new agent.

### Resolution (2026-05-24)
Original diagnosis was slightly off — `CLAUDE_SESSION=""` was already present in the DO_SWITCH=1 block (added by commit `19b3786`, before the todo was filed). The actual missed reset was **`RESUME_ARG`**, which is computed once at the top of the poller dispatch loop (`pkg/compiler/userdata.go:1550`) from the pre-switch `CLAUDE_SESSION` and never recomputed. The codex dispatch fork re-reads `CLAUDE_SESSION` at fork time (so the post-switch empty value correctly takes the first-turn branch), but the claude dispatch fork consumes the stale `RESUME_ARG` verbatim — producing the `--resume <prior-codex-UUID>` line seen in UAT.

Fix added one line to the DO_SWITCH=1 block alongside the existing `CLAUDE_SESSION=""` reset:

```bash
CLAUDE_SESSION=""
RESUME_ARG=""        # NEW — without this, claude -p inherits the prior agent's --resume arg
```

New unit test `TestPoller_CrossAgentSwitch_ResetsResumeArg` in `pkg/compiler/userdata_prefix_test.go` pins this — extracts the cross-agent block via `extractCrossAgentBlock` and asserts `RESUME_ARG=""` is present.

**Deploy:** `km init --sidecars` to push the updated userdata template into the create-handler Lambda's toolchain. New sandboxes pick up the fix on next create; existing sandboxes need `km destroy && km create` (per the standard "userdata change → recreate" rule).
