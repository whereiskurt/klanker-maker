---
phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
plan: 05
subsystem: infra
tags: [bash, userdata, slack, codex, dynamodb, jq, km-slack-inbound-poller]

requires:
  - phase: 70-02
    provides: KM_AGENT env var emitted to /etc/km/notify.env from spec.cli.agent
  - phase: 70-03
    provides: km-notify-hook PermissionRequest branch + Stop last_assistant_message fast-path

provides:
  - "Poller dispatch fork: codex vs claude based on EFFECTIVE_AGENT"
  - "Boot-time AGENT resolution from KM_AGENT env var (default claude)"
  - "DDB GetItem extended: agent_type + last_assistant_msg alongside claude_session_id"
  - "Codex first-turn dispatch: codex exec --json --dangerously-bypass-approvals-and-sandbox"
  - "Codex resume dispatch: codex exec resume <id> (subcommand form)"
  - "KM_CODEX_RUN_ID inline-exported in sudo -u sandbox command string"
  - "Hook-file session-ID extraction with 5x1s retry loop"
  - "km-notify-hook Stop branch: writes session_id to /tmp/km-codex-session.$KM_CODEX_RUN_ID"
  - "DDB PutItem: agent_type + jq-Rs-escaped last_assistant_msg on every successful turn"
  - "5 real tests: CodexDispatch_FirstTurn, CodexDispatch_Resume, AgentTypeWriteback, JQEscaping_RoundTrip, ClaudePath_Unchanged"

affects:
  - "70-06 — depends on EFFECTIVE_AGENT variable + LAST_ASSISTANT_MSG for prefix routing + cross-agent switch"
  - "km-slack-inbound-poller systemd service"
  - "km-slack-threads DynamoDB table (schemaless attribute additions)"

tech-stack:
  added: []
  patterns:
    - "KM_CODEX_RUN_ID inline-env pattern: VAR=val in sudo -u sandbox bash -lc string (not separate export) per Pitfall 3"
    - "jq -Rs . for DDB JSON encoding: no extra quotes around $LAST_MSG_JSON in item JSON"
    - "Hook-file session-ID handoff: Stop hook writes /tmp/km-codex-session.$RUN_ID; poller reads with retry"

key-files:
  created: []
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_slack_inbound_test.go
    - pkg/compiler/testdata/userdata_additional_volume_only.golden.sh

key-decisions:
  - "Codex resume uses 'codex exec resume SESSION PROMPT --flags' subcommand form, NOT --resume flag"
  - "KM_CODEX_RUN_ID exported inline in sudo -u sandbox command string (Pitfall 3 mitigation)"
  - "jq -Rs . for last_assistant_msg DDB encoding — no extra quotes around $LAST_MSG_JSON"
  - "Hook-side session-ID write placed in the existing km-notify-hook Stop branch (not a separate file)"
  - "agent_type + last_assistant_msg added as DDB attributes on every successful turn (schemaless, no Terraform change)"
  - "Pre-existing test failures in compiler suite are out of scope (confirmed pre-existed before this plan)"

requirements-completed: [SC-1, SC-4, SC-5, SC-6]

duration: 10min
completed: 2026-05-23
---

# Phase 70 Plan 05: Poller Dispatch Fork + DDB Writeback Summary

**Slack inbound poller extended with codex/claude dispatch fork, hook-file session-ID extraction, and jq-escaped DDB writeback for agent_type and last_assistant_msg**

## Performance

- **Duration:** ~10 min
- **Started:** 2026-05-23T04:10:37Z
- **Completed:** 2026-05-23T04:20:13Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments

- Poller heredoc gains 6 edits: boot-time AGENT, extended GetItem, dispatch fork, session-ID extraction, per-agent RESULT_TEXT, extended PutItem
- km-notify-hook Stop branch writes `/tmp/km-codex-session.$KM_CODEX_RUN_ID` when the poller sets `KM_CODEX_RUN_ID` — enables the hook-file session-ID extraction pattern
- All 5 new tests GREEN; 17 existing Phase 67/74/75 poller tests unaffected

## Task Commits

1. **Task 1: Wave 0 stub seed** — `6f229d3` (test: 5 stub functions with t.Skip)
2. **Task 2: Poller dispatch fork + real tests** — `dc91022` (feat: 6 poller edits + hook writer + 5 real tests + golden regen)

## Files Created/Modified

- `pkg/compiler/userdata.go` — 6 edits to km-slack-inbound-poller heredoc (see Edits A-F below); km-notify-hook Stop branch gets KM_CODEX_RUN_ID session-ID writer
- `pkg/compiler/userdata_slack_inbound_test.go` — 5 stub stubs replaced with real tests; helper funcs pollerWithAgentCodex/pollerWithAgentClaude added; exec import added; PollerPostsResultToSlack assertion updated
- `pkg/compiler/testdata/userdata_additional_volume_only.golden.sh` — regenerated to capture EFFECTIVE_AGENT + RESULT_TEXT lines added to poller

## Poller Heredoc Edits Detail

**Edit A — boot-time AGENT** (after THREADS_TABLE):
`AGENT="${KM_AGENT:-claude}"` — profile default; Plan 70-06 prefix parser may override per-turn into EFFECTIVE_AGENT.

**Edit B — extended DDB GetItem** (after CLAUDE_SESSION):
Reads `agent_type.S` into `CURRENT_AGENT` and `last_assistant_msg.S` into `LAST_ASSISTANT_MSG`; defaults `CURRENT_AGENT` to `$AGENT` when absent (backward compat); sets `EFFECTIVE_AGENT="$CURRENT_AGENT"`.

**Edit C — dispatch fork** (wraps existing claude block in `else`, prepends codex branch):
- Codex: `KM_CODEX_RUN_ID` exported inline in the sudo command string (Pitfall 3)
- Codex resume: `codex exec resume '$CLAUDE_SESSION' "$(cat '$PROMPT_FILE')" --json --dangerously-bypass-approvals-and-sandbox`
- Codex first-turn: `codex exec --json --dangerously-bypass-approvals-and-sandbox "$(cat '$PROMPT_FILE')"`
- Claude path: verbatim from Phase 67 (no changes)

**Edit D — session-ID extraction** (replaces single-path NEW_SESSION assignment):
Codex path: waits up to 5x1s for `/tmp/km-codex-session.$RUN_ID` (hook writes it), reads, removes.
Claude path: `jq -r '.session_id // empty'` from output.json (unchanged).

**Edit E — per-agent RESULT_TEXT extraction** (new block before DDB put-item):
Codex: `.last_assistant_message // .result // ""` (spike field-name assumption; 1-line fix if UAT finds mismatch).
Claude: `.result // .response // ""`.

**Edit F — extended DDB PutItem** (replaces existing put-item):
Adds `agent_type` and `last_assistant_msg` attributes. Uses `jq -Rs .` for `LAST_MSG_JSON` (Pitfall 2). No extra quotes around `$LAST_MSG_JSON` in the item JSON.

## Decisions Made

- Codex session-ID extraction via hook-file approach confirmed as the right pattern (per 70-RESEARCH.md). Spike couldn't verify hook firing (ChatGPT API gating on test fixture), but the implementation matches the locked design and Plan 70-09 UAT will confirm or correct field names.
- `KM_CODEX_RUN_ID` passed inline (not as a prior `export`) per Pitfall 3: `sudo -u sandbox bash -lc` drops root-side shell variables; inline VAR=val is the only reliable mechanism.
- Codex output field `last_assistant_message` used per SPEC.md/CONTEXT.md research notes (MEDIUM confidence); plan notes this is a 1-line fix if UAT reveals a different field name.
- The claude path's `.result // empty` string changed to `.result // .response // ""` as part of the per-agent RESULT_TEXT extraction. The `TestUserdata_PollerPostsResultToSlack` assertion was updated to test for `.result` (still present) rather than the exact Phase 67 literal.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Backtick in Go raw string literal caused syntax error**
- **Found during:** Task 2 (implementation of poller edits)
- **Issue:** Comment text `` `sudo -u sandbox bash -lc` `` contained a backtick, which terminated the `const userDataTemplate = \`` raw string literal in Go, causing a syntax error at the word following the backtick.
- **Fix:** Removed backticks from comments (two occurrences), writing them as plain text.
- **Files modified:** pkg/compiler/userdata.go
- **Verification:** `go build ./...` clean
- **Committed in:** dc91022 (Task 2 commit)

**2. [Rule 1 - Bug] Golden snapshot test failure after template additions**
- **Found during:** Task 2 (running full compiler test suite)
- **Issue:** `TestUserdataAdditionalVolumeOnly_GoldenByteIdentical` failed because the golden file no longer matched after the new lines added to the poller heredoc.
- **Fix:** Regenerated the golden file using a temporary test helper that writes the current output to testdata/.
- **Files modified:** pkg/compiler/testdata/userdata_additional_volume_only.golden.sh
- **Verification:** Test passes after regeneration
- **Committed in:** dc91022 (Task 2 commit)

**3. [Rule 1 - Bug] Test assertion matched backslash-escaped DDB JSON form**
- **Found during:** Task 2 (TestPoller_AgentTypeWriteback failed on first run)
- **Issue:** The test looked for `"agent_type":{"S":"$EFFECTIVE_AGENT"}` (plain quotes) but the rendered poller uses bash quoting: `\"agent_type\":{\"S\":\"$EFFECTIVE_AGENT\"}` (backslash-escaped).
- **Fix:** Updated test assertions to use the backslash-escaped form that matches the actual rendered heredoc.
- **Files modified:** pkg/compiler/userdata_slack_inbound_test.go
- **Verification:** All 5 tests GREEN
- **Committed in:** dc91022 (Task 2 commit)

---

**Total deviations:** 3 auto-fixed (Rule 1 — bug)
**Impact on plan:** All fixes are correctness-only. No scope creep. 6 pre-existing failures in compiler suite are out of scope (confirmed via git stash verification — present before this plan).

## Issues Encountered

- Spike (Plan 70-00) could not verify hook payload field names (`last_assistant_message`, `session_id`) due to ChatGPT API gating on the test fixture. Plan 70-09 UAT will confirm. Risk is bounded: 1-line fix per mismatched field name, in `pkg/compiler/userdata.go` only.

## Next Phase Readiness

- Plan 70-06 (Wave 4) can now build on `EFFECTIVE_AGENT` + `LAST_ASSISTANT_MSG` for prefix routing and cross-agent thread switching
- The `EFFECTIVE_AGENT` variable is in place at the poller dispatch site; Plan 70-06 only needs to add the prefix parser + switch sequence before that site
- The DDB row now carries `agent_type` and `last_assistant_msg` on every successful turn

---
*Phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher*
*Completed: 2026-05-23*
