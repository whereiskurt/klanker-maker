---
phase: 103-hackerone-comment-trigger-bridge
plan: 01
subsystem: infra
tags: [hackerone, webhooks, bridge, payload-parsing, byte-identity, dormancy, golden-test]

# Dependency graph
requires:
  - phase: 102-github-bridge-agent-verbs
    provides: "pkg/github/bridge payload/resolve/webhook architecture that pkg/h1/bridge ports from"
provides:
  - "Two synthetic HackerOne webhook bodies (report_created, report_comment_created) modeled on the docs payload shape with live-confirmed inner report-object paths"
  - "103-CAPTURE/field-paths.md pinning every JSON path Plan 03 needs (OQ1 resolved, OQ2 deferred LOW), with a parse-tolerance directive"
  - "Pre-H1 userdata dormancy golden + TestUserdataH1ByteIdentity guarding the Plan 08 dormancy invariant"
affects: [103-03-payload-parse, 103-07-userdata-poller, 103-08-byte-identity-guard, 103-10-e2e]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Dual-mode capture/verify golden harness (CAPTURE_PRE_H1_BASELINE=1 to regenerate, default run verifies) mirrored from phase92 byte-identity test"
    - "Synthetic-fallback webhook fixtures explicitly self-marked (_synthetic: true) to drive downstream parse-tolerance"

key-files:
  created:
    - .planning/phases/103-.../103-CAPTURE/report_created.json
    - .planning/phases/103-.../103-CAPTURE/report_comment_created.json
    - .planning/phases/103-.../103-CAPTURE/field-paths.md
    - pkg/compiler/userdata_h1_byte_identity_test.go
    - pkg/compiler/testdata/h1_byte_identity_golden.txt
  modified: []

key-decisions:
  - "Took the pre-authorized SYNTHETIC fallback for the live webhook capture (Task 1 checkpoint) — operator deferred the real HackerOne Sandbox capture to Plan 10 (Wave 6) E2E"
  - "Used the in-tree testdata profile ec2-basic.yaml (not a shipped profiles/ file) as the H1-free dormancy baseline so the golden stays stable against profile-inventory churn"
  - "OQ1 (program-handle path) resolved via live-confirmed inner paths; OQ2 (state endpoint) left LOW-confidence/deferred since it is an outbound API call not pinnable from a webhook capture"

patterns-established:
  - "Wave-0 dormancy golden captured BEFORE any feature code exists makes the post-change byte-identity assertion meaningful"
  - "Synthetic fixtures carry an explicit marker + a written parse-tolerance directive so the downstream parser fails safe on real-delivery wrapper surprises"

requirements-completed: [H1-RESOLVE-PROGRAM, H1-REPLY-INTERNAL-DEFAULT, H1-HELPER-KM-H1, H1-DEPLOY-WIRING]

# Metrics
duration: 2min
completed: 2026-06-10
---

# Phase 103 Plan 01: HackerOne webhook field-path capture + userdata dormancy golden Summary

**Pinned the HackerOne webhook field paths (program-handle resolve key, internal-flag safety field, actor/comment paths) from synthetic-fallback bodies with live-confirmed inner paths, and captured the pre-H1 userdata dormancy golden that guards the Plan 08 invariant.**

## Performance

- **Duration:** ~2 min
- **Started:** 2026-06-10T03:55:19Z
- **Completed:** 2026-06-10T03:57:17Z
- **Tasks:** 3
- **Files created:** 5

## Accomplishments
- Two synthetic HackerOne webhook bodies (`report_created`, `report_comment_created`) modeled on the docs payload shape (`data` → `data.activity` + `data.report`), with inner report-object paths set to the live-confirmed values from 103-CONTEXT.md. Each body is explicitly self-marked `_synthetic: true`.
- `103-CAPTURE/field-paths.md` pins every JSON path Plan 03 needs — program handle (OQ1 resolved), report id, title/state, actor username, internal flag, comment body — each tagged LIVE-CONFIRMED / DOCS-SHAPED / LOW, plus a parse-tolerance directive for the wrapper variance and a deferred LOW-confidence note on the OQ2 state endpoint.
- Pre-H1 userdata dormancy golden (92162 bytes) captured from the H1-free `ec2-basic.yaml` baseline, with `TestUserdataH1ByteIdentity` (dual-mode capture/verify) passing green — this guards the Plan 08 dormancy invariant before any H1 poller heredoc exists.

## Task Commits

1. **Task 1: Capture two HackerOne webhook bodies (synthetic fallback)** - `d1f1efa1` (chore)
2. **Task 2: Pin field paths from the captured bytes** - `b542dc4e` (docs)
3. **Task 3: Capture userdata dormancy golden (pre-H1 baseline)** - `69f794b0` (test)

**Plan metadata:** appended below.

## Files Created/Modified
- `103-CAPTURE/report_created.json` - synthetic report_created webhook body (live-confirmed inner paths)
- `103-CAPTURE/report_comment_created.json` - synthetic report_comment_created body (internal:true, @km handle + /reply_to_researcher in message)
- `103-CAPTURE/field-paths.md` - pinned JSON paths + confidence levels + parse-tolerance directive + OQ2 deferral
- `pkg/compiler/userdata_h1_byte_identity_test.go` - dual-mode capture/verify dormancy test
- `pkg/compiler/testdata/h1_byte_identity_golden.txt` - 92162-byte pre-H1 userdata golden

## Decisions Made
- **Synthetic-fallback capture (pre-authorized):** Task 1 is a blocking human-action checkpoint for a live HackerOne webhook capture. Per the operator's pre-authorization, the documented synthetic/docs fallback was taken instead of pausing — the HackerOne Sandbox program is being provisioned in parallel and the real capture (plus envelope-wrapper + header-casing confirmation) is deferred to Plan 10 (Wave 6) E2E. No production program is referenced anywhere; the only live target for this phase is the operator's HackerOne Sandbox account.
- **OQ1 resolved, OQ2 deferred:** program-handle path confirmed against the live read-only customer API (`data.report.relationships.program.data.attributes.handle`); the state-change endpoint cannot be pinned from a webhook capture (it is an outbound call) and is marked LOW-confidence — `km-h1 state` may ship as a fast-follow.
- **ec2-basic.yaml as the dormancy baseline:** chosen over a shipped `profiles/` file so the golden remains stable against profile-inventory churn; it declares no `notification.h1` block of any kind.

## Deviations from Plan

None - plan executed exactly as written. Task 1's checkpoint was resolved via the plan's own explicit fallback path (synthetic/docs capture), pre-authorized by the operator; this is documented flow, not a deviation.

## Issues Encountered
None.

## User Setup Required
**Deferred to Plan 10 (Wave 6).** The real HackerOne **Sandbox** program webhook capture (a real `Test request` + a real report comment) and the envelope-wrapper / header-casing confirmation are deferred to the Wave-6 E2E. The Sandbox program is the only live target — no production program is used. Until then, Plan 03's payload parser must honor the parse-tolerance directive in `103-CAPTURE/field-paths.md`.

## Next Phase Readiness
- Plan 03 (payload parse) is unblocked: field-paths.md pins the resolve key + safety-critical internal flag and prescribes wrapper-tolerant parsing.
- Plan 08 (byte-identity guard) is unblocked: the dormancy golden + `TestUserdataH1ByteIdentity` exist and are green pre-H1; Plan 07's poller change must keep them green.
- Open item carried to Plan 10: re-pin every DOCS-SHAPED row + OQ2 state endpoint against the real HackerOne Sandbox delivery and tighten the parser if the live wrapper differs.

## Self-Check: PASSED

All 5 created artifacts exist on disk; all 3 task commits (`d1f1efa1`, `b542dc4e`, `69f794b0`) present in git history. `TestUserdataH1ByteIdentity` green.

---
*Phase: 103-hackerone-comment-trigger-bridge*
*Completed: 2026-06-10*
