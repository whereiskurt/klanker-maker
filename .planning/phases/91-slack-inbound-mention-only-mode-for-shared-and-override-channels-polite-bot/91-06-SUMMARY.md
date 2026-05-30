---
phase: 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot
plan: 06
subsystem: docs
tags: [slack, polite-bot, mention-only, bridge, lambda, km-doctor]

# Dependency graph
requires:
  - phase: 91-01
    provides: profile schema field notifySlackInboundMentionOnly
  - phase: 91-02
    provides: compiler KM_SLACK_MENTION_ONLY emission
  - phase: 91-03
    provides: bridge handler mention scan
  - phase: 91-04
    provides: km slack init bot_user_id SSM caching
  - phase: 91-05
    provides: km doctor slack_bot_user_id_cached check
provides:
  - Full operator guide for Phase 91 polite-bot mode in docs/slack-notifications.md
  - CLAUDE.md architecture + CLI notes for Phase 91
  - OPERATOR-GUIDE.md mention-only subsection with cross-refs
  - UAT checkpoint envelope for live workspace verification (task 4 pending)

affects: [klanker:slack, docs/slack-notifications.md, CLAUDE.md, OPERATOR-GUIDE.md]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Phase section pattern in docs/slack-notifications.md (mirroring Phase 72 structure)"
    - "Cross-ref pattern: CLAUDE.md short-form + OPERATOR-GUIDE.md bridge + full doc runbook"

key-files:
  created: []
  modified:
    - docs/slack-notifications.md
    - CLAUDE.md
    - OPERATOR-GUIDE.md

key-decisions:
  - "Phase 91 docs follow Phase 72 structural template exactly (overview + table + field ref + examples + env vars + doctor + rollout + troubleshooting)"
  - "CLAUDE.md Phase 91 section kept under 35 lines (terse > verbose for agent context)"
  - "OPERATOR-GUIDE.md adds a top-level ## Slack notifications section (was absent) containing the mention-only subsection"
  - "km destroy --remote --yes used consistently (not --force) per project convention"
  - "Generic placeholders only: example.com, #km-notifications, ${KM_RESOURCE_PREFIX} — no real workspace names"
  - "Task 4 (live Slack UAT) deferred to checkpoint:human-verify — requires real workspace + AWS credentials"

patterns-established:
  - "Rollout sequence for Lambda env var schema additions: make build → km slack init --force → export envs → km init --sidecars → km doctor → km destroy + km create"

requirements-completed: [POL-13]

# Metrics
duration: 3min
completed: 2026-05-30
---

# Phase 91 Plan 06: Documentation + UAT — Polite-bot Mention-only Mode Summary

**Operator-facing docs for Phase 91 polite-bot mode: per-mode defaults table, profile field reference, rollout sequence, troubleshooting matrix added to three doc files; live Slack UAT deferred to operator checkpoint.**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-05-30T22:55:09Z
- **Completed:** 2026-05-30T22:57:24Z
- **Tasks:** 3 of 4 completed (task 4 = checkpoint:human-verify, pending operator)
- **Files modified:** 3

## Accomplishments

- Full Phase 91 operator guide added to `docs/slack-notifications.md` (158 lines): per-mode defaults, profile field tri-state, four override examples, bridge env vars table, `km doctor` check, rollout sequence with correct `--remote --yes` flags, eight-entry troubleshooting matrix, out-of-scope callout
- `CLAUDE.md` updated in three places: Where-to-look table row for polite-bot, `km slack init` CLI bullet noting bot_user_id caching, Phase 91 architecture paragraph (30 lines, within the 50-line budget)
- `OPERATOR-GUIDE.md` gains a new `## Slack notifications` top-level section (previously absent) containing the `### Mention-only mode (polite-bot)` subsection with profile snippet, doctor check note, `km init --sidecars` callout, and cross-ref

## Task Commits

1. **Task 1: Write Phase 91 section in docs/slack-notifications.md** - `83b53fe` (docs)
2. **Task 2: Update CLAUDE.md with Phase 91 architecture + CLI notes** - `75b87e7` (docs)
3. **Task 3: Add mention-only subsection to OPERATOR-GUIDE.md** - `bd3b6fa` (docs)

**Plan metadata:** (pending final commit)

## Files Created/Modified

- `docs/slack-notifications.md` — Added `## Phase 91` section (158 new lines) after Phase 72 section
- `CLAUDE.md` — Added Where-to-look row, updated km slack init bullet, added Phase 91 architecture section (33 new lines)
- `OPERATOR-GUIDE.md` — Added `## Slack notifications` section with mention-only subsection (33 new lines)

## Decisions Made

- Used `--remote --yes` for `km destroy` throughout (not `--force`) per project memory `feedback_destroy_flags`.
- Added `## Slack notifications` as a new top-level section in OPERATOR-GUIDE.md rather than inserting into an existing Slack section (none existed). Placed before SOPS section following the same "brief summary + full runbook cross-ref" pattern.
- CLAUDE.md Phase 91 section kept to 30 lines (well under 50-line limit) — table + bullets + rollout snippet + cross-ref.
- All generic placeholders: `#km-notifications`, `${KM_RESOURCE_PREFIX}`, `example.com` — no real workspace names, channel names, or user IDs.

## Deviations from Plan

None — plan executed exactly as written for tasks 1-3. Task 4 is a `checkpoint:human-verify` as specified by the plan and the objective.

## Issues Encountered

None.

## User Setup Required

Task 4 (live Slack UAT) is a checkpoint:human-verify. See checkpoint envelope in agent output for the exact step-by-step verification protocol (scenarios 7-10).

## Next Phase Readiness

- All three doc files are updated and committed
- Phase 91 docs are complete and accurate per the implementation decisions in 91-CONTEXT.md
- Live UAT (task 4) is blocked on: real Slack workspace + AWS credentials + deployed bridge Lambda
- When the operator completes UAT and types "approved", the phase is fully closed

---
*Phase: 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot*
*Completed: 2026-05-30 (tasks 1-3); task 4 UAT pending operator*
