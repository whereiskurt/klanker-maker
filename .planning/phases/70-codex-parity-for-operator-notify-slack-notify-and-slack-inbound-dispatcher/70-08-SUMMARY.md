---
phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
plan: "08"
subsystem: documentation
tags: [docs, codex-parity, slack-prefix-routing, cross-agent-switch, claude-md]
dependency_graph:
  requires: [70-01, 70-02, 70-03, 70-04, 70-05, 70-06, 70-07]
  provides: [SC-1, SC-2, SC-4, SC-5, SC-6, SC-7, SC-8, SC-9, SC-10]
  affects: [docs/codex-parity.md, docs/slack-notifications.md, CLAUDE.md]
tech_stack:
  added: []
  patterns: [additive-docs-only, operator-guide]
key_files:
  created:
    - docs/codex-parity.md
  modified:
    - docs/slack-notifications.md
    - CLAUDE.md
decisions:
  - "docs/codex-parity.md documents Path B (JSONL stream) as the actual mechanism; config.toml noted as forward-compat artifact with no runtime effect under Codex 0.121-0.133"
  - "SC-3 drop rationale documented: hooks don't fire under --dangerously-bypass-approvals-and-sandbox; expected behavior, not a bug"
  - "All edits to CLAUDE.md and docs/slack-notifications.md are strictly additive; no existing content removed"
metrics:
  duration: "191s"
  completed: "2026-05-23"
  tasks_completed: 2
  tasks_total: 2
  files_created: 1
  files_modified: 2
---

# Phase 70 Plan 08: Codex Parity Documentation Summary

**One-liner:** New `docs/codex-parity.md` (361 lines) + Phase 70 sections appended to `docs/slack-notifications.md` and `CLAUDE.md`, covering JSONL stream mechanism, Slack prefix grammar, 8-step cross-agent switch sequence, and km doctor checks.

## What Was Built

### 1. `docs/codex-parity.md` (NEW, 361 lines)

Primary operator guide for Phase 70. Sections:

- **What changed** — comparison table: before/after Phase 70 across 7 concerns
- **Profile schema** — `spec.cli.agent: claude | codex` field reference with validation note
- **Runtime behavior** — KM_AGENT env files, `~/.codex/config.toml` content, Path B rationale (JSONL vs hooks), SC-3 drop explanation, km-notify-hook branches
- **Slack inbound dispatch** — 5-step flow from Bridge Lambda through dispatch fork to DDB write-back, with both Claude and Codex dispatch commands
- **Slack prefix routing** — grammar regex, case-insensitivity, anchor rules, 4-case semantics matrix
- **Cross-agent thread switch** — 8-step ordered sequence with abort guard, OLD-row-untouched invariant, failure mode table, and storyboard reference
- **km-slack sidecar additions** — `--new-message`, `permalink`, `update` surface descriptions
- **km doctor** — `codex_version_supports_jsonl` + `agent_type_consistency` checks
- **Troubleshooting** — 8-row symptom/cause/fix matrix
- **Hangover: `claude_session_id` column** — agent-agnostic reuse rationale
- **Phase 70 deferrals** — 7 explicitly out-of-scope items
- **See also** cross-reference block

### 2. `docs/slack-notifications.md` (extended, +93 lines)

New section `## Prefix routing & agent switching (Phase 70)` appended after the Phase 75 attachment section. Contents:

- Feature overview (2 bullets)
- Prefix grammar block
- Behavior matrix (4-row table)
- Cross-agent switch artifacts (OLD thread post, NEW top-level post, prompt seed — verbatim examples)
- km-slack sidecar additions summary
- km doctor checks summary
- Cross-reference to `docs/codex-parity.md`

**Lines before:** 1020. **Lines after:** 1113.

### 3. `CLAUDE.md` (extended, +37 lines)

Four additive edits:

**Edit A** — New "Where to look" row:
`| Codex parity, spec.cli.agent, Slack prefix routing & agent switching | docs/codex-parity.md (Phase 70) |`

**Edit B** — New `### Agent: claude | codex (Phase 70)` subsection under Agent Execution:
- `spec.cli.agent` field with YAML snippet
- KM_AGENT env file note
- `~/.codex/config.toml` forward-compat note
- Slack prefix routing one-liner with grammar
- Cross-agent switch reference link
- `km init --sidecars` required reminder

**Edit C** — New `### DDB column hangover (Phase 70)` subsection:
- `claude_session_id` agent-agnostic reuse rationale
- Future-agent (Goose etc.) extension path

No existing content removed. All edits purely additive.

## Tests

Documentation-only plan. No code tests. Verification was automated bash checks:

```bash
test -f docs/codex-parity.md
grep -q "spec.cli.agent" docs/codex-parity.md
grep -q "Cross-agent thread switch" docs/codex-parity.md
grep -q "claude_session_id" docs/codex-parity.md
grep -q "km doctor" docs/codex-parity.md
wc -l docs/codex-parity.md  # 361 >= 150 ✓
grep -q "Prefix routing & agent switching" docs/slack-notifications.md
grep -q "spec.cli.agent" CLAUDE.md
grep -q "claude_session_id" CLAUDE.md
grep -q "km init --sidecars" CLAUDE.md
```

All checks PASSED.

## Deviations from Plan

None. Plan executed exactly as written.

The plan specified `grep -q "Prefix routing"` and the written section header is
`## Prefix routing & agent switching (Phase 70)` — the grep matches. No
adjustment needed.

## Self-Check: PASSED

- FOUND: docs/codex-parity.md (361 lines)
- FOUND: commit 2dbc1d9 (Task 1 — new docs/codex-parity.md)
- FOUND: commit 2a031e9 (Task 2 — docs/slack-notifications.md + CLAUDE.md)
- FOUND: grep spec.cli.agent in CLAUDE.md
- FOUND: grep claude_session_id in CLAUDE.md
- FOUND: grep km init --sidecars in CLAUDE.md
- FOUND: grep Prefix routing in docs/slack-notifications.md
