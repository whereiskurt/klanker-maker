---
phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
plan: 00
subsystem: research
tags: [spike, codex-cli, hook-system, risk-elimination]

# Dependency graph
requires:
  - phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
    provides: Phase 70 context + risk-register identifying the Codex hook payload shape unknowns
provides:
  - Spike Findings (Plan 70-00) section appended to 70-RESEARCH.md
  - Confirmation of `codex exec resume <SESSION_ID> <PROMPT>` subcommand form (eliminates the biggest pre-code unknown)
  - Documented Codex auth limitation on the `learn` AMI fixture (ChatGPT-OAuth tier rejecting all models tried)
  - Decision-of-record to proceed with documented field names; UAT (Plan 70-09) verifies / corrects
affects:
  - 70-03 (km-notify-hook field names locked from documentation, not spike-verified)
  - 70-05 (poller dispatch fork uses confirmed `codex exec resume` syntax)
  - 70-09 (UAT carries deferred verification surface for unverified payload fields)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "SSM-deployed spike script pattern (operator workstation → aws ssm send-command → sandbox /tmp/) — alternative to interactive km shell for non-interactive verification"

key-files:
  created:
    - /tmp/spike-codex.sh (operator workstation, discarded after spike)
    - /tmp/spike-codex.sh (sandbox /tmp/, discarded with sandbox)
  modified:
    - .planning/phases/70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher/70-RESEARCH.md (appended Spike Findings section)
---

# Plan 70-00 — Codex hook spike (PARTIAL)

## Outcome

**CLI surface fully confirmed; hook firing deferred to UAT.**

The spike was designed to eliminate Phase 70's only remaining pre-code unknowns by running a toy `~/.codex/config.toml` on the operator's `learn` sandbox (Codex CLI 0.121.0 pre-authenticated via baked AMI) and verifying:
1. `codex exec --json` fires hooks identically to interactive TUI
2. Stop payload field names (`last_assistant_message`, `session_id`)
3. PermissionRequest hook fires under `--dangerously-bypass-approvals-and-sandbox`
4. `KM_CODEX_RUN_ID` env visibility inside hook subprocess
5. `codex exec resume` subcommand form (vs `--resume` flag)

### What ran

The spike script (`/tmp/spike-codex.sh`) was deployed to the sandbox via SSM `send-command` (base64-encoded payload), then executed as the `sandbox` user. It:
- Captured `codex --help`, `codex exec --help`, `codex exec resume --help`
- Wrote a toy `~/.codex/config.toml` with PermissionRequest + Stop hooks pointing at `/tmp/codex-spike-*.json` writers
- Ran two `codex exec --json --dangerously-bypass-approvals-and-sandbox` invocations (neutral + permission-triggering)
- Inspected hook output files + log
- Tested `codex exec resume <SID> <PROMPT>` subcommand form
- Restored the operator's original config

### What was confirmed (HIGH confidence)

| # | Question | Answer |
|---|----------|--------|
| 1 | `codex exec resume <SESSION_ID> <PROMPT>` subcommand exists | YES — `codex exec --help` shows `resume` subcommand; `codex exec resume --help` confirms exact form |
| 2 | `--dangerously-bypass-approvals-and-sandbox` flag valid on `codex exec` | YES |
| 3 | `codex exec --json` JSONL mode valid | YES |
| 4 | Codex version on `learn` AMI | `codex-cli 0.121.0` |
| 5 | Auth mode on the test sandbox | ChatGPT OAuth (token-based, not API-key) |

### What blocked verification

Every `codex exec` invocation failed with:

```
"The '<model>' model is not supported when using Codex with a ChatGPT account."
```

This happened for the Codex CLI's own default model (`gpt-5.2-codex`), and explicit `-m gpt-5`, `-m gpt-4o`, `-m o3-mini` were also rejected. The Codex turn fails before any hook fires (no `turn.completed` event ever reached). This is a Codex billing/subscription gating issue on the operator's ChatGPT account — not a Phase 70 design problem.

### Decision: proceed with documented field names

Phase 70 implementation continues with Plans 70-01..70-08 using the field names assumed by SPEC.md and CONTEXT.md:

- Stop payload: `last_assistant_message`, `session_id`
- PermissionRequest payload: `tool_name`
- Hooks fire under bypass flag (per OpenAI engineering issue thread; documented as expected behavior)
- `KM_CODEX_RUN_ID` passed via inline `VAR=val sudo -u sandbox bash -lc "..."` (Pitfall #3 mitigation already locked in CONTEXT.md)

Plan 70-09 (UAT) becomes the verification surface. If a field name turns out wrong, the fix is single-line per item in `pkg/compiler/userdata.go` — no re-architecture.

### Operator action items (pre-UAT)

Before Plan 70-09 UAT:
1. **Either** re-login Codex with an API key on the UAT sandbox: `printenv OPENAI_API_KEY | codex login --with-api-key` (unlocks all models)
2. **Or** upgrade the ChatGPT subscription tier to one that includes Codex CLI API access

Pre-flight check: `codex exec --json --dangerously-bypass-approvals-and-sandbox "ping"` must produce a `turn.completed` event before UAT step 2 runs.

## Commits

- `c54a2e3`: docs(70-00): append Spike Findings — CLI surface confirmed, hook firing deferred to UAT (Codex auth gating)

## Hard stops triggered

None. The CLI surface confirmation (item #1 — `codex exec resume` subcommand) was the single highest-risk item in the spec and is now LOCKED. The deferred items (payload field names) are low-risk per-line fixes if UAT finds discrepancies.
