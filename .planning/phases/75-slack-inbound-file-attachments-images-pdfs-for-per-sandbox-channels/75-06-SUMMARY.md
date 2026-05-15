---
phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels
plan: 06
subsystem: docs
tags: [slack, file-attachments, docs, operator-runbook]

# Dependency graph
requires:
  - phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels
    provides: Plans 01-05 implementation (types, S3 downloader, bridge fork, poller bash, IAM/lifecycle, cold-start wiring)
provides:
  - Phase 75 operator runbook in docs/slack-notifications.md
  - Phase 75 short entry in CLAUDE.md Slack-inbound section
  - Structured UAT runbook for human gate (12 steps)
affects: [phase-76-and-beyond, operators-deploying-phase-75]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Doc-first gate: UAT checkpoint surfaces runbook before human verification"

key-files:
  created:
    - .planning/phases/75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels/75-06-SUMMARY.md
  modified:
    - docs/slack-notifications.md
    - CLAUDE.md

key-decisions:
  - "Docs use spec-link pattern (not content duplication): docs/slack-notifications.md links to the spec; CLAUDE.md links to both doc and spec"
  - "km init --lambdas pitfall explicitly called out in both docs per project memory project_km_init_lambdas_doesnt_deploy"

patterns-established:
  - "Phase N doc sections appended at end of relevant doc — no edits to prior Phase 67/68 subsections"

requirements-completed:
  - REQ-FILES-DEPLOY

# Metrics
duration: ~5min
completed: 2026-05-15
---

# Phase 75 Plan 06: Docs + Operator UAT Summary

**Phase 75 operator runbook and CLAUDE.md entry written; UAT gate pending human verification of live AWS + Slack deployment**

## Performance

- **Duration:** ~5 min
- **Started:** 2026-05-15T15:25:41Z
- **Completed:** 2026-05-15T15:30:00Z (docs task); UAT pending
- **Tasks:** 1 of 2 autonomous tasks complete; 1 human-verify checkpoint pending
- **Files modified:** 2

## Accomplishments

- `docs/slack-notifications.md`: new `## Slack inbound file attachments (Phase 75)` section with profile field, caps, one-time operator setup (5 steps), S3 layout, sandbox-side layout, troubleshooting table, and spec reference
- `CLAUDE.md`: new `### Slack inbound file attachments (Phase 75)` paragraph in the Slack-inbound subsection — highlights files:read scope, km init --lambdas pitfall, memory bump, and links to both the operator doc and the spec
- Both docs warn about the `km init --lambdas` pitfall (per project memory `project_km_init_lambdas_doesnt_deploy`)
- UAT runbook (12 steps) surfaced for operator execution

## Task Commits

1. **Task 06-01: Document Phase 75 in docs/slack-notifications.md and CLAUDE.md** - `8ba0bf6` (docs)

## Files Created/Modified

- `docs/slack-notifications.md` — new `## Slack inbound file attachments (Phase 75)` section at end of file
- `CLAUDE.md` — new `### Slack inbound file attachments (Phase 75)` subsection after Phase 68 content

## Decisions Made

- Doc structure follows spec-link pattern: operator guide carries the runbook; CLAUDE.md is a short pointer. No content duplication.
- `km init --lambdas` pitfall explicitly highlighted in both docs (per `project_km_init_lambdas_doesnt_deploy` memory item).

## Deviations from Plan

None — plan executed exactly as written.

## UAT Status

**PENDING** — Task 06-02 is a `checkpoint:human-verify` gate. The operator must:

1. Add `files:read` scope to Slack App + reinstall
2. `km slack rotate-token --bot-token <new>`
3. `make build && km init` (NOT `km init --lambdas`)
4. Verify deployed IAM policy + S3 lifecycle + Lambda memory_size via AWS CLI
5. `km doctor` — confirm `files:read` in scope list
6. Create fresh sandbox (Phase 75 userdata required)
7. Drag image + describe
8. Drag PDF + identify title
9. Drag 26 files → cap warning + first-25 dispatch
10. Drag >100 MB file → size warning + skip
11. Drag file with empty text (Pitfall 4 regression)
12. `km destroy` — cleanup

PASS if all 12 steps complete with runbook criteria met.

### UAT Runbook Results (to be filled in by operator)

| Step | Description | Status | Notes |
|------|-------------|--------|-------|
| 0 | Prerequisites | PENDING | |
| 1 | Add files:read scope + reinstall | PENDING | |
| 2 | km slack rotate-token | PENDING | |
| 3 | make build && km init | PENDING | |
| 4 | AWS CLI state verification | PENDING | |
| 5 | km doctor scope check | PENDING | |
| 6 | Create fresh sandbox | PENDING | |
| 7 | Image drag test | PENDING | |
| 8 | PDF drag test | PENDING | |
| 9 | 26-file cap test | PENDING | |
| 10 | >100 MB oversize test | PENDING | |
| 11 | File-only (empty text) test | PENDING | |
| 12 | Cleanup | PENDING | |

## Issues Encountered

None.

## Next Phase Readiness

- Phase 75 docs complete
- Awaiting operator UAT approval to close Phase 75 as fully complete
- After UAT passes: update STATE.md Phase 75 status + REQUIREMENTS.md REQ-FILES-* traceability

---
*Phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels*
*Completed: 2026-05-15 (docs); UAT pending*
