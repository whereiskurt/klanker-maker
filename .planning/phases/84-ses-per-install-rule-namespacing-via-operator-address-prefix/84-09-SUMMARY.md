---
phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
plan: 09
subsystem: docs
tags: [ses, email, multi-instance, operator-guide, documentation]

# Dependency graph
requires:
  - phase: 84-08-phase-82.1-hard-removal-and-grep-gate
    provides: Phase 82.1 sections removed from CLAUDE.md and OPERATOR-GUIDE.md; grep gate in Makefile
  - phase: 84-07-km-bootstrap-shared-ses-and-doctor-check
    provides: km bootstrap --shared-ses and km doctor checkSESRules behavior to document
  - phase: 84-04-km-configure-operator-email-derivation
    provides: operator-{prefix}@{subdomain}.{domain} derivation and --reset-prefix flag
provides:
  - "CLAUDE.md Phase 84 section documenting operator address format, shared rule set, bootstrap, doctor check, and upgrade procedure"
  - "OPERATOR-GUIDE.md Phase 84 upgrade runbook with single-install and two-install sequences, rollback note, and validation commands"
  - "OPERATOR-GUIDE.md KM_OPERATOR_EMAIL description updated to prefix-aware format"
  - "ROADMAP.md 82.1-03-PLAN.md marked SUPERSEDED by Phase 84"
  - "REQUIREMENTS.md 7 Phase 84 synthetic requirement IDs in traceability table"
affects: [84-10-operator-uat-checkpoint, future phase planning]

# Tech tracking
tech-stack:
  added: []
  patterns: [docs-alongside-code, phase-upgrade-runbook-in-operator-guide, cross-reference-see-also]

key-files:
  created: []
  modified:
    - CLAUDE.md
    - OPERATOR-GUIDE.md
    - .planning/ROADMAP.md
    - .planning/REQUIREMENTS.md

key-decisions:
  - "Insert CLAUDE.md Phase 84 section between Architecture and Network Enforcement (Alternative anchor — no Phase History parent section exists)"
  - "KM_OPERATOR_EMAIL table row lives in OPERATOR-GUIDE.md not CLAUDE.md; updated there rather than adding duplicate in CLAUDE.md"
  - "OPERATOR-GUIDE.md Phase 84 runbook appended after Phase 82 isolation guarantees section (end of file, maintains chronological order)"

patterns-established:
  - "Phase upgrade runbooks belong in OPERATOR-GUIDE.md with see-also pointer from CLAUDE.md"
  - "Superseded plan items are annotated with SUPERSEDED by Phase N, never deleted"

requirements-completed:
  - SES-SHARED-RULESET
  - SES-PER-INSTALL-RULES
  - SES-82.1-REMOVAL
  - SES-CONFIGURE-WIRING
  - SES-DOCTOR-ORPHANS

# Metrics
duration: 2min
completed: 2026-05-16
---

# Phase 84 Plan 09: Docs — CLAUDE.md, OPERATOR-GUIDE.md, and ROADMAP Summary

**Operator-facing Phase 84 documentation: per-install SES rule namespacing, operator address format, shared rule set bootstrap, and two-install upgrade runbook across CLAUDE.md, OPERATOR-GUIDE.md, ROADMAP.md, and REQUIREMENTS.md**

## Performance

- **Duration:** ~2 min
- **Started:** 2026-05-16T20:43:11Z
- **Completed:** 2026-05-16T20:45:05Z
- **Tasks:** 3
- **Files modified:** 4

## Accomplishments

- Added `## Phase 84: SES per-install rule namespacing via operator address prefix` section to CLAUDE.md between Architecture and Network Enforcement, documenting operator address format, `sandbox-email-shared` rule set, `km bootstrap --shared-ses` auto-detect bootstrap, `km doctor` orphan check, and one-time upgrade procedure
- Added Phase 84 upgrade runbook to OPERATOR-GUIDE.md with single-install and second-install sequences, rollback note, AWS CLI validation commands, and updated `KM_OPERATOR_EMAIL` table row to prefix-aware description
- Marked ROADMAP.md `82.1-03-PLAN.md` entry as SUPERSEDED by Phase 84 and appended 7 Phase 84 synthetic requirement IDs to REQUIREMENTS.md traceability table

## Task Commits

Each task was committed atomically:

1. **Task 1: Add Phase 84 section to CLAUDE.md** - `366732d` (docs)
2. **Task 2: Add Phase 84 upgrade runbook to OPERATOR-GUIDE.md** - `490cee5` (docs)
3. **Task 3: Mark 82.1-03 superseded + REQUIREMENTS.md traceability rows** - `aae004d` (docs)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/CLAUDE.md` - Added 33-line Phase 84 section (operator address format, shared rule set, bootstrap, doctor check, upgrade procedure code block, see-also pointer)
- `/Users/khundeck/working/klankrmkr/OPERATOR-GUIDE.md` - Updated KM_OPERATOR_EMAIL table row; appended 79-line Phase 84 upgrade runbook section
- `/Users/khundeck/working/klankrmkr/.planning/ROADMAP.md` - 82.1-03 SUPERSEDED annotation; Phase 84 plan counter updated to 9/10
- `/Users/khundeck/working/klankrmkr/.planning/REQUIREMENTS.md` - 7 Phase 84 synthetic requirement IDs appended to traceability table

## Decisions Made

- **CLAUDE.md insertion anchor:** Used Alternative anchor (## level, between Architecture and Network Enforcement) rather than creating a new ## Phase History section — no existing parent phase section in the file to group under.
- **KM_OPERATOR_EMAIL location:** The variable's description row lives in OPERATOR-GUIDE.md (not CLAUDE.md); updated there rather than creating a duplicate entry in CLAUDE.md. CLAUDE.md's Phase 84 section describes the operator address format inline.
- **OPERATOR-GUIDE.md placement:** Phase 84 runbook appended after Phase 82 isolation guarantees section (chronological order, end of file) — mirrors the Phase 82 upgrade runbook pattern established in 82-10.

## Deviations from Plan

None — plan executed exactly as written. The KM_OPERATOR_EMAIL no-op for CLAUDE.md was anticipated by Task 1 action item 4: "If ABSENT, this sub-step is a no-op — document in SUMMARY".

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- All Phase 84 operator documentation is complete
- Phase 84 plan 09 done; only 84-10 (Operator UAT checkpoint — NOT autonomous) remains
- Phase 84 Plans counter updated to 9/10 in ROADMAP.md
- REQUIREMENTS.md traceability table includes all 7 Phase 84 synthetic IDs

---
*Phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix*
*Completed: 2026-05-16*
