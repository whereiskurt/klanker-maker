---
phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
plan: "06"
subsystem: slack-inbound-poller
tags: [prefix-routing, cross-agent-switch, bash-heredoc, tdd]
dependency_graph:
  requires: [70-04, 70-05, 70-10]
  provides: [SC-8, SC-9, SC-10]
  affects: [pkg/compiler/userdata.go, pkg/compiler/userdata_prefix_test.go]
tech_stack:
  added: []
  patterns: [bash-prefix-parser, cross-agent-switch-sequence, empirical-bash-subprocess-tests]
key_files:
  created:
    - pkg/compiler/userdata_prefix_test.go
  modified:
    - pkg/compiler/userdata.go
decisions:
  - "Prefix parser bash regex uses per-character case classes for ERE bash [[ =~ ]] compatibility"
  - "Cross-agent switch fetches OLD permalink FIRST (THREAD_TS already known from SQS event) so new top-level body embeds it at post-time — no placeholder string ever posted to Slack, no chat.update in critical path"
  - "Test bash scripts use tr for lowercase instead of ${var,,} (bash 4+) for macOS bash 3.2 portability"
  - "extractCrossAgentBlock helper added to test file to scope assertions to the DO_SWITCH=1 block only"
metrics:
  duration: "327s"
  completed: "2026-05-23"
  tasks_completed: 3
  tasks_total: 3
  files_created: 1
  files_modified: 1
---

# Phase 70 Plan 06: Prefix Routing + Cross-Agent Thread Switch Summary

**One-liner:** Per-message prefix parser (`^([Cc][Ll][Aa][Uu][Dd][Ee]|[Cc][Oo][Dd][Ee][Xx]):[[:space:]]?`) + 8-step locked cross-agent switch sequence added to km-slack-inbound-poller bash heredoc, with 6 empirical/structural tests.

## What Was Built

### 1. Prefix parser bash regex + routing decision (SC-8, SC-9)

Inserted into the poller heredoc after `EFFECTIVE_AGENT="$CURRENT_AGENT"` and before `RUN_ID`:

```bash
REQUESTED_AGENT=""
STRIPPED_TEXT="$TEXT"
if [[ "$TEXT" =~ ^([Cc][Ll][Aa][Uu][Dd][Ee]|[Cc][Oo][Dd][Ee][Xx]):[[:space:]]? ]]; then
  PREFIX="${BASH_REMATCH[1],,}"   # bash 4+ lowercase; EC2 Linux always has bash 4+
  REQUESTED_AGENT="$PREFIX"
  STRIPPED_TEXT="${TEXT#*:}"
  STRIPPED_TEXT="${STRIPPED_TEXT# }"
fi

DO_SWITCH=0
if [ -n "$REQUESTED_AGENT" ]; then
  if [ "$REQUESTED_AGENT" != "$CURRENT_AGENT" ] && [ -n "$CLAUDE_SESSION" ]; then
    DO_SWITCH=1   # cross-agent mid-thread switch
  else
    EFFECTIVE_AGENT="$REQUESTED_AGENT"   # fresh-thread prefix OR same-agent no-op
    TEXT="$STRIPPED_TEXT"
  fi
fi
```

Key invariants:
- `^` anchor prevents mid-sentence `claude:` from matching (Pitfall 4 guard)
- `DO_SWITCH=1` only fires when an existing DDB row (`CLAUDE_SESSION` non-empty) pins the thread to a DIFFERENT agent
- Fresh-thread prefix (no session yet) and same-agent prefix both fall into the else branch — strip and continue, no switch sequence

### 2. Cross-agent switch sequence — 8 ordered steps (SC-10)

Inserted after the Phase 75 attachment mirror block and before `export KM_SLACK_THREAD_TS`. The ordering is LOCKED in CONTEXT.md:

**Why OLD permalink is fetched FIRST:** `THREAD_TS` is already known from the inbound SQS event before the new top-level exists. Fetching OLD permalink first means the new top-level body can include `Continuing from $OLD_PERMALINK` at the moment `km-slack post --new-message` is called. No placeholder string is ever posted to Slack. No `chat.update` in the critical path. This eliminates both the Slack 10-minute edit window risk and the v1 `<permalink-placeholder>` pattern.

| Step | Action |
|------|--------|
| 1 | `km-slack permalink --channel C --ts $THREAD_TS` → `OLD_PERMALINK` (fallback: `(unavailable)`) |
| 2 | Build new top-level body: `Continuing from $OLD_PERMALINK` + 500-char excerpt |
| 3 | `km-slack post --new-message` → capture `NEW_TOP_TS` from `ts=...` stdout |
| 4 | Abort guard: if `NEW_TOP_TS` empty → error reply in OLD thread + `continue` |
| 5 | `km-slack permalink --channel C --ts $NEW_TOP_TS` → `NEW_PERMALINK` (fallback: `(unavailable)`) |
| 6 | `km-slack post --thread $THREAD_TS` → handoff: `Switching to $NEW_AGENT → continuing in this thread.\n$NEW_PERMALINK` |
| 7 | Compose seeded prompt: `$STRIPPED_TEXT` + `--- Context from prior thread (agent: $OLD_AGENT) ---\n` + `$LAST_ASSISTANT_MSG | head -c 2000` |
| 8 | Rewrite: `CLAUDE_SESSION=""`, `THREAD_TS="$NEW_TOP_TS"`, `EFFECTIVE_AGENT="$NEW_AGENT"`, `PROMPT_FILE="$SEED_PROMPT_FILE"` — fall through to Plan 70-05 dispatch |

### 3. OLD-row-untouched invariant

The cross-agent block never calls `aws dynamodb update-item` or `aws dynamodb delete-item` on the original `THREAD_TS`. By rewriting `THREAD_TS="$NEW_TOP_TS"` and `EFFECTIVE_AGENT="$NEW_AGENT"`, Plan 70-05's existing `put-item` path automatically writes a NEW DDB row keyed on `(channel_id, NEW_TOP_TS)` with `agent_type=NEW_AGENT`. The old session remains resumable — operator replies in the old thread continue to resume the old agent.

### 4. Empirical (bash sub-process) testing approach

The test suite avoids purely textual regex assertions. `TestPoller_PrefixParser_TableDriven` runs 11 cases through a real bash subprocess that executes the same regex + expansion logic the production poller does. This catches runtime failures (wrong match, wrong capture group, off-by-one space stripping) that textual grep would miss.

Note: test bash scripts use `tr '[:upper:]' '[:lower:]'` for case folding instead of `${var,,}` (bash 4+ only) for macOS bash 3.2 portability. The production poller runs on Linux EC2 with bash 4+, so `${var,,}` is correct there.

### 5. CONTEXT.md locked decisions — not re-debated

All surface-area decisions were pre-locked in CONTEXT.md before this plan ran:
- Prefix grammar: `^([Cc]laude|[Cc]odex|CLAUDE|CODEX):[[:space:]]?`
- Switch ordering: OLD permalink first, then new top-level body embedding it, then new permalink, then handoff
- Failure modes: `(unavailable)` fallback for permalink failures; abort-on-new-top-level-failure; DDB row write failure acceptable two-turn-loss
- Truncation: 500 chars for Slack excerpt; 2000 chars for prompt seed
- OLD row: never mutated

## Tests

| Test | Type | SC | Status |
|------|------|----|--------|
| TestPoller_PrefixParser_TableDriven (11 subtests) | empirical bash | SC-8/SC-9 | PASS |
| TestPoller_PrefixParser_AnchoredAtStart | textual + empirical | SC-8/SC-9 | PASS |
| TestPoller_TopLevelPrefix_FreshThread | structural | SC-8 | PASS |
| TestPoller_SameAgentPrefix_NoOp | structural | SC-9 | PASS |
| TestPoller_CrossAgentSwitch_OrderingFetchesOldPermalinkFirst | structural | SC-10 | PASS |
| TestPoller_CrossAgentSwitch_OldRowUntouched | structural | SC-10 | PASS |

All Plan 70-05 regression tests continue to PASS.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Backtick in Go raw string constant terminated the template**
- **Found during:** Task 3
- **Issue:** The comment `# ever posting a \`<permalink-placeholder>\` string to Slack` inside the Go raw string (`userDataTemplate = \`...\``) was terminated by the backtick inside the comment, causing two `undefined: permalink, placeholder` compile errors.
- **Fix:** Removed backticks from the comment: `<permalink-placeholder>` (unquoted).
- **Files modified:** `pkg/compiler/userdata.go`
- **Commit:** 19b3786

**2. [Rule 2 - Auto-fix] Test bash sub-scripts use tr for bash 3.2 portability**
- **Found during:** Task 2 (test run)
- **Issue:** `${var,,}` (bash 4+ lowercase) produced `CODEX` instead of `codex` on macOS bash 3.2, failing 2 of 11 table-driven subtests.
- **Fix:** Test bash scripts use `$(echo "${BASH_REMATCH[1]}" | tr '[:upper:]' '[:lower:]')` for portability. Production poller code is unchanged (runs on EC2 Linux with bash 4+).
- **Files modified:** `pkg/compiler/userdata_prefix_test.go`
- **Commit:** 727f281

## Self-Check: PASSED

- FOUND: pkg/compiler/userdata_prefix_test.go
- FOUND: commit 1d50259 (wave 0 stub seed)
- FOUND: commit 727f281 (prefix parser + 4 tests)
- FOUND: commit 19b3786 (cross-agent switch + 2 tests)
