---
phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator
plan: 09
subsystem: docs
tags: [slack, invite, manifest, docs, uat, validation]

requires:
  - phase: 72-03
    provides: km slack manifest command + RenderSlackManifest
  - phase: 72-05
    provides: km slack invite cobra command + --dry-run
  - phase: 72-06
    provides: km slack init orchestrator refactor
  - phase: 72-07
    provides: km create per-sandbox invite loop (operator + additional-folks)
  - phase: 72-08
    provides: slack_users_read_email_scope doctor check

provides:
  - docs/slack-notifications.md Phase 72 section (manifest, invite, profile fields, doctor, troubleshooting)
  - CLAUDE.md updated CLI list + Phase 72 subsection
  - 72-VALIDATION.md Per-Task Verification Map (19 rows, nyquist_compliant: true)
  - 72-UAT.md Part A (dev-machine automated) + Part B (live scenarios B0-B6 cheapest→most-expensive)

affects: [operator-docs, klanker:slack skill, klanker:init skill]

tech-stack:
  added: []
  patterns:
    - "UAT split into Part A (automated, no AWS) + Part B (live, ordered cheapest→most-expensive)"
    - "km slack invite --dry-run as primary live exercise of orchestrator before sandbox provisioning"
    - "Install-ordering checklist: km init → km slack manifest → install app → km slack init → km doctor"

key-files:
  created:
    - .planning/phases/72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator/72-UAT.md
  modified:
    - docs/slack-notifications.md
    - CLAUDE.md
    - .planning/phases/72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator/72-VALIDATION.md

key-decisions:
  - "docs/slack-notifications.md Phase 72 section added at end of file after Phase 70 prefix routing section"
  - "72-UAT.md Part B ordered B0(install ordering)→B1(manifest render)→B2(doctor)→B3(dry-run)→B4(invite)→B5(km create)→B6(scope drift)"
  - "km slack invite --dry-run framed as primary live test; km create (B5) as one-shot AWS wiring confirmation"
  - "nyquist_compliant: true set in 72-VALIDATION.md after full Per-Task Verification Map populated"
  - "B6 (doctor scope-drift destructive cycle) deferred — covered by unit tests at Plan 72-08; operator chose to skip"
  - "Three production bugs caught during live UAT (ec13e5b, 6cf1deb, 2653bc3) fixed with regression tests before sign-off"
  - "Reinstall-ejects-bot documented as known consequence in docs/slack-notifications.md — not a bug, expected Slack behavior"

patterns-established:
  - "Phase docs section: append at end of docs/slack-notifications.md after prior phase sections"
  - "UAT runbook: Part A proves logic (no external deps), Part B confirms wiring (live, cheap→expensive)"

requirements-completed:
  - VALIDATION-Layer-1
  - VALIDATION-Layer-2
  - VALIDATION-Layer-3
  - VALIDATION-Layer-4
  - VALIDATION-Layer-5
  - VALIDATION-Layer-6
  - VALIDATION-Layer-7
  - VALIDATION-Layer-8

duration: 25min
completed: 2026-05-30
---

# Phase 72 Plan 09: Docs + Validation + UAT Closeout Summary

**Phase 72 operator UAT passed against a real corporate Slack workspace; three production bugs (auth.test decode, nil-pointer deref in invite, form-vs-JSON encoding) caught and fixed with regression tests during live Part B run; Phase 72 is COMPLETE.**

## Performance

- **Duration:** ~25 min total (Tasks 1+2: 8 min 2026-05-29; Task 3 finalization: ~17 min 2026-05-30)
- **Started:** 2026-05-29T19:37:05Z
- **Completed:** 2026-05-30
- **Tasks:** 3 of 3 complete
- **Files modified:** 5

## Accomplishments

- Added comprehensive Phase 72 section to `docs/slack-notifications.md` covering `km slack manifest`, `km slack invite` (incl. `--dry-run`), profile fields `notifySlackInviteEmails` + `useSlackConnect`, `slack_users_read_email_scope` doctor check, cross-account install ordering, PoC update path, troubleshooting table, and known reinstall-ejects-bot consequence
- Added `km slack invite` + `km slack manifest` to `CLAUDE.md` CLI list and a new "Phase 72: Corporate workspace support" subsection with phase summary and token-rotation one-liner
- Populated `72-VALIDATION.md` Per-Task Verification Map with 19 rows (one per task across plans 72-00 through 72-09) and set `nyquist_compliant: true` in frontmatter
- Created `72-UAT.md` with Part A (dev-machine `go test`, no Slack/AWS) and Part B (8 live scenarios B0-B6, ordered cheapest→most-expensive, with install-ordering chicken-and-egg callout and Sign-off table)
- Operator ran full live UAT against a real corporate Slack workspace; 7/8 rows passed (B6 deferred with unit-test justification); sign-off recorded 2026-05-30 by KPH
- Three production bugs caught and fixed during live Part B with regression tests: ec13e5b (auth.test decode), 6cf1deb (nil-pointer deref in RunSlackInvite), 2653bc3 (form-encoded vs JSON for users.lookupByEmail)

## Task Commits

1. **Task 1: Update docs/slack-notifications.md and CLAUDE.md** - `1039eda` (docs)
2. **Task 2: Populate 72-VALIDATION.md + write 72-UAT.md** - `15d9a4d` (docs)
3. **Task 3: Operator UAT finalization** - committed in `docs(72-09): finalize UAT sign-off and Phase 72 closeout`

## Files Created/Modified

- `docs/slack-notifications.md` — Added Phase 72 section (~175 lines) at end of file; added reinstall UX note + troubleshooting row
- `CLAUDE.md` — Added `km slack invite` + `km slack manifest` to CLI list; added Phase 72 subsection (~42 lines)
- `.planning/phases/72-.../72-VALIDATION.md` — Replaced placeholder row with 19-row Per-Task Verification Map; set `nyquist_compliant: true`
- `.planning/phases/72-.../72-UAT.md` — Created with Part A + Part B (8 scenarios, Sign-off table); sign-off completed; Bugs Caught + Reinstall UX Note sections appended

## Decisions Made

- Appended Phase 72 section at the end of `docs/slack-notifications.md` after the Phase 70 prefix routing section, matching the per-phase append convention
- UAT Part B ordered: B0 (install ordering, required first) → B1 (manifest render, read-only) → B2 (doctor, verifies B0) → B3 (--dry-run, cheapest live test) → B4 (real invite, primary live test) → B5 (km create, one-shot AWS) → B6 (scope drift)
- `km slack invite --dry-run` (B3) framed as the primary live exercise of the EnsureMemberByEmail orchestrator; km create (B5) is the one-shot AWS-plumbing confirmation rather than the main invite test
- B6 (scope-drift destructive cycle) deferred — covered by unit tests at Plan 72-08; operator chose to skip the destructive scope-removal step; phase considered complete
- Three production bugs caught during live UAT fixed with regression tests before sign-off

## UAT Sign-off

| Row | Result | Notes |
|-----|--------|-------|
| A. Dev-machine `go test` | ✅ | Phase 72 surface clean; pre-existing pkg/compiler failures unrelated (same at f0cd289) |
| B0. Install ordering | ✅ | Manifest installed cleanly; km init complete; doctor confirms scopes |
| B1. Manifest renders correctly | ✅ | Pasted into Slack admin → all 13 scopes present |
| B2. Install + init + doctor | ✅ | slack_users_read_email_scope OK, slack_files_write_scope OK; bridge 502 is pre-existing/unrelated |
| B3. `km slack invite --dry-run` | ✅ | whereiskurt@gmail.com native; kurt.hundeck@greenhouse.io external |
| B4. `km slack invite` real | ✅ | Native invite to sb-learn printed `✓ Invited`; Connect via greenhouse path in B5 |
| B5. Full `km create` | ✅ | sb-phase72-fresh created; both orchestrator quadrants (native + Connect) exercised |
| B6. Doctor scope drift | ⏭ | Deferred — covered by TestDoctor_SlackUsersReadEmailScope_Pass + _Warn unit tests (Plan 72-08) |

**Operator: KPH — Date: 2026-05-30**

## Bugs Caught During UAT (Production Fixes)

Three production bugs caught during live Part B and fixed with regression tests before sign-off:

**1. `ec13e5b` — fix(72-01): auth.test decode regression**
- SlackAPIResponse.User was `struct{ID string}` but `auth.test` returns `"user": "<string>"`. Fixed with SlackUserField tolerant unmarshaller + TestClient_AuthTest_RealShape regression test.

**2. `6cf1deb` — fix(72-05): nil-pointer deref in RunSlackInvite**
- buildSlackCmdDeps returns deps with Slack=nil; RunSlackInvite called without nil-check. Fixed with lazy-init at slack.go:222–225 mirroring RunSlackInit pattern + regression test.

**3. `2653bc3` — fix(72-01): form-encoded vs JSON for users.lookupByEmail**
- Slack legacy Web API rejects JSON bodies with invalid_arguments. Fixed with callForm helper; test updated to assert Content-Type and form-encoded body.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Three production bugs caught during live UAT**
- **Found during:** Task 3 — Part B live verification
- **Issue:** auth.test decode shape mismatch, nil-pointer deref in invite deps, form-vs-JSON encoding
- **Fix:** Each fixed inline with a regression test; committed before UAT sign-off
- **Files modified:** pkg/slack/client.go, internal/app/cmd/slack.go (and test files)
- **Verification:** Regression tests added; Part B re-ran successfully after fixes
- **Committed in:** ec13e5b, 6cf1deb, 2653bc3 (separate fix commits)

---

**Total deviations:** 3 auto-fixed (Rule 1 — bugs caught by live UAT)
**Impact on plan:** All three fixes required for correct operation against a real Slack workspace. No scope creep. Each regression test adds durable coverage.

## Issues Encountered

None beyond the three bugs listed above (all resolved before sign-off).

## Next Phase Readiness

- Phase 72 is fully complete — all 10 plans landed, operator UAT passed 2026-05-30 by KPH
- Phase 73 (km vscode remote session via SSM) depends on Phase 72 and can proceed
- Deferred items from 72-CONTEXT.md (manifest diff/upgrade tool) are tracked in the deferred list; not blocking Phase 73
- `km init --sidecars` required to deploy bridge Lambda changes from Phase 72 to production

---
*Phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator*
*Completed: 2026-05-30*
