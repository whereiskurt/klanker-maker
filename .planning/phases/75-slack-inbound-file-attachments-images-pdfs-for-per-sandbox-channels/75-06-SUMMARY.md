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

**PASSED (after three in-flight hotfixes)** — operator verified the
full pipeline end-to-end on 2026-05-15: top-thread image decode worked,
in-thread PDF decode worked (`km shell` confirmed `attachments=1` SQS
enqueue and on-box mirror at `/workspace/.km-slack/attachments/<thread_ts>/`).
The 12-step runbook surfaced three previously-unknown gaps that shipped
as 75.1 / 75.2 / 75.3 hotfix commits before the UAT closed clean.

### UAT Runbook Results

| Step | Description | Status | Notes |
|------|-------------|--------|-------|
| 0 | Prerequisites | PASS | klanker-application SSO, Pro Slack workspace, per-sandbox channels live |
| 1 | Add files:read scope + reinstall | PASS | |
| 2 | km slack rotate-token | PASS | smoke test passed |
| 3 | make build && km init **--dry-run=false** | PASS (after gap-1) | Initial run used default `--dry-run=true` and silently no-op'd. Gap surfaced as: Lambda env vars still empty after step 3. Resolved by re-running with `--dry-run=false`. Drove the docs/CLAUDE.md update committed in a094669. |
| 4 | AWS CLI deploy verification | PASS | MemorySize=1024, Timeout=60 (post-75.2), Env contains `KM_ARTIFACTS_BUCKET` |
| 5 | km doctor scope check | PASS | `Slack App events scopes` lists `files:read` |
| 6 | Create fresh sandbox | PASS (after gap-1) | First sandbox created before `--dry-run=false` ran; create-handler Lambda's km toolchain was stale, generated pre-Phase-75 userdata. Recreate after the real `km init` produced correct userdata. |
| 7 | Image drag test (top-thread) | PASS (after 75.1, 75.2, 75.3) | First attempt: `Get "": unsupported protocol scheme ""` → 75.1 (files.info enrichment). Second: `Client.Timeout exceeded while awaiting headers` → 75.2 (sync handler + 60s timeout). Third: poller `KM_ARTIFACTS_BUCKET: unbound variable` → 75.3 (systemd env line). Fourth attempt: image decoded. |
| 8 | PDF drag test (in-thread) | PASS | First in-thread test after 75.3 worked first try — file_share handler is thread-position-agnostic, and the `--resume <session-id>` poller path still gets the master-prompt wrapper prepended on every turn. |
| 9 | 26-file cap test | DEFERRED | Caps verified by unit tests (`TestFileDownloader_Over25Files_Truncated`); skipped in live UAT to keep iteration tight. Re-run when convenient. |
| 10 | >100 MB oversize test | DEFERRED | Caps verified by unit test (`TestFileDownloader_Over100MB_Dropped`); deferred for the same reason as step 9. |
| 11 | File-only (empty text) test | DEFERRED | Covered by `TestUserdata_SlackInbound_AllowsEmptyTextWhenAttachments` and the Pitfall 4 guard in userdata.go. Live UAT primarily tested the "with-caption" path. |
| 12 | Cleanup | PENDING | `learn-59d8f4a5` left running for follow-on testing; operator can `km destroy` when done. |

### Hotfix commits (in UAT order)

| # | Commit | Symptom + fix |
|---|---|---|
| 75.1 | `9ee8c0c` | Modern Slack stub file objects — `url_private_download` empty in `file_share` event. Added `FilesInfoFetcher` + `SlackFilesInfoAdapter`; downloader enriches via `files.info` when URL empty. |
| 75.2 | `3351bdf` | Goroutine-after-handler unsound on Lambda freeze — every files-fork attempt timed out at the 10s `httpClient.Timeout` because Lambda froze the runtime once the 200 returned. Made `file_share` handling synchronous inside `Handle`. Bumped Lambda timeout 15s → 60s. Existing `event_id` nonce dedup absorbs Slack's 3s retry. |
| 75.3 | `9c9fb6c` | `km-slack-inbound-poller.service` missing `Environment=KM_ARTIFACTS_BUCKET={{ ... }}` — sibling units (mail-poller, enforcer) had it, this unit was simply missed. Added the line + regression test in `userdata_slack_inbound_test.go`. |

### Docs hardening (commit `a094669`)

After UAT closed, the three hotfix lessons (plus the `--dry-run=false`
gap surfaced in step 3) were baked into operator-facing docs so the
next operator doesn't repeat them:

- `docs/slack-notifications.md` setup step 4 now spells out `--dry-run=false`
- New step 5: post-deploy `aws lambda get-function-configuration` verification
  recipe with expected `MemorySize=1024`, `Timeout=60`, `Vars` containing
  `KM_ARTIFACTS_BUCKET`
- Troubleshooting table gained four new rows covering the hotfix
  symptoms (stub file objects, freeze timeout, unbound variable, the
  remote-create stale-toolchain gotcha)
- New "verify the full pipeline" snippet with the `events: enqueued (files-sync)`
  log marker check + on-box `ls` check
- `CLAUDE.md` "Phase 75 hotfix lessons" subsection summarizing 75.1/75.2/75.3
  with their symptoms and fixes; inline `--dry-run=false` gate and
  verification recipe

## Issues Encountered

Three gaps surfaced + closed in-flight:

1. **`km init` dry-run default** — UAT step 3 ran `make build && km init`
   per the original runbook, but `km init`'s default `--dry-run=true`
   meant nothing actually deployed. The Lambda was running the prior
   `km slack rotate-token` state (env vars wiped to just `TOKEN_ROTATION_TS`,
   memory still 256 MB). Resolved by re-running with `--dry-run=false`.
   Docs updated.

2. **Stale create-handler toolchain (75.1 precursor)** — The first
   sandbox was created via `--remote` before `km init --dry-run=false`
   landed the new km binary in the create-handler Lambda's S3
   toolchain. The Lambda generated userdata using its stale `km` ⇒
   missing Plan 03 attachment-mirror bash. Resolved by `km destroy
   && km create` after the successful `km init`.

3. **Three hotfixes** (75.1, 75.2, 75.3) — see commits above.

## Next Phase Readiness

- Phase 75 fully complete (Plans 01-05 + hotfixes 75.1/75.2/75.3 + docs hardening)
- All operator-facing docs reflect the production behavior + known pitfalls
- Recommended next: cleanup of caps-related UAT steps (9, 10, 11) when convenient
- Phase 76 unblocked

---
*Phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels*
*Completed: 2026-05-15 (UAT passed after 75.1/75.2/75.3 hotfixes)*
