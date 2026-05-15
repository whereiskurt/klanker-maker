---
phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels
plan: 03
subsystem: slack
tags: [slack, sqs, bash, userdata, ec2, s3, tdd, compiler]

requires:
  - phase: 67-slack-inbound
    provides: km-slack-inbound-poller heredoc in userdata.go, SQS FIFO dispatch loop
  - phase: 75-02
    provides: SQS message shape with Attachments []Attachment field (bridge-side fork)

provides:
  - "Sandbox-side attachment mirror: SQS .attachments[]? -> /workspace/.km-slack/attachments/<thread_ts>/<file_id>-<name>"
  - "Master-prompt wrapper prepended to PROMPT_FILE when ATTACH_COUNT > 0 (exact CONTEXT.md phrasing + em-dash)"
  - "Pitfall 4 fix: malformed-message guard admits file-only uploads (empty text + non-empty attachments)"
  - "3 regression tests pinning all three behaviors"

affects:
  - 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels
  - phase-75-uat

tech-stack:
  added: []
  patterns:
    - "jq -c '.field[]?' safe-degradation (? operator returns empty on missing key)"
    - "while IFS= read -r LINE done < <(jq -c) pattern for spaces in JSON values"
    - "ATTACH_COUNT computed before malformed-message guard so guard can reference it"
    - "Wrapper-prepend: write wrapper to mktemp, cat original, mv into place"

key-files:
  created: []
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_slack_inbound_test.go

key-decisions:
  - "Mirror block uses basename of s3_key (not re-sanitizing) — bridge wrote the safe name; poller trusts it"
  - "ATTACH_LIST built as newline-separated strings via shell variable; echo -e used inside wrapper heredoc"
  - "Wrapper gated on ATTACH_COUNT -gt 0 only — text-only path is bit-for-bit unchanged per Phase 67 compat"
  - "Pre-existing test failures (TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock and 5 others) are out-of-scope; scope boundary enforced"

patterns-established:
  - "Phase 75 bash additions: always gate on ATTACH_COUNT -gt 0 before mkdir/mirror/wrapper"
  - "logger -t km-slack-inbound for per-attachment observability (journalctl -t km-slack-inbound -f)"

requirements-completed:
  - REQ-FILES-POLLER
  - REQ-FILES-WRAPPER-FORMAT

duration: 2min
completed: 2026-05-15
---

# Phase 75 Plan 03: Inbound Poller Attachment Mirror + Master-Prompt Wrapper Summary

**Sandbox-side inbound poller extended to mirror Slack-attached S3 objects to /workspace/.km-slack/attachments/<thread_ts>/ and prepend a master-prompt wrapper to claude -p input when files present, with Pitfall 4 fix admitting file-only uploads**

## Performance

- **Duration:** ~2 min
- **Started:** 2026-05-15T15:01:31Z
- **Completed:** 2026-05-15T15:03:48Z
- **Tasks:** 2 (03-01 + 03-02 implemented together; guard change naturally coupled to mirror block)
- **Files modified:** 2

## Accomplishments

- Extended inbound poller heredoc in `pkg/compiler/userdata.go` with ~54 lines:
  - `ATTACH_COUNT=$(echo "$BODY" | jq -r '.attachments // [] | length')` computed before the malformed-message guard
  - Guard updated to `{ [ -z "$TEXT" ] && [ "$ATTACH_COUNT" -eq 0 ]; }` compound (Pitfall 4)
  - Mirror block: `mkdir -p $ATTACH_DIR`, iterate `jq -c '.attachments[]?'`, `aws s3 cp "s3://$KM_ARTIFACTS_BUCKET/$S3_KEY"`, `chown sandbox:sandbox`
  - Wrapper block: builds `ATTACH_LIST`, prepends to `PROMPT_FILE` when `ATTACH_COUNT -gt 0`; exact phrasing from CONTEXT.md including em-dash `[no text — file-only]`
- Added 3 new regression tests in `userdata_slack_inbound_test.go` (all pass)
- All 8 `TestUserdata_SlackInbound*` tests pass; no previously-passing tests regressed

## Task Commits

1. **RED — 3 failing tests** - `130cdc5` (test)
2. **GREEN — implementation** - `d50bd62` (feat)

## Files Created/Modified

- `pkg/compiler/userdata.go` — inbound poller heredoc: ATTACH_COUNT extraction, Pitfall 4 guard update, mirror block, wrapper-prepend block (+54 lines)
- `pkg/compiler/userdata_slack_inbound_test.go` — 3 new tests: `TestUserdata_SlackInbound_AttachmentMirrorBlock`, `TestUserdata_SlackInbound_MasterPromptWrapper`, `TestUserdata_SlackInbound_AllowsEmptyTextWhenAttachments` (+77 lines)

## Decisions Made

- Implemented Tasks 03-01 and 03-02 together in the GREEN commit because the guard change (03-02) is a prerequisite for the mirror block (03-01) — they share `ATTACH_COUNT` and splitting them would leave the code in a broken intermediate state.
- Used `jq -c '.attachments[]?'` with the `?` safe-degradation operator so messages from older bridges (no `attachments` key) degrade silently to `ATTACH_COUNT=0`.
- `basename "$S3_KEY"` extracts `<file_id>-<sanitized_name>` from the full key — poller trusts the bridge wrote a safe name and does not re-sanitize.
- Pre-existing compiler test failures (`TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock`, `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`, `TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime`, `TestUserDataKMTracingServicectlStart`, `TestAuditHookNonBlocking`, `TestGitHubUserDataGITASKPASS`) verified as pre-existing before my changes; logged as out-of-scope per deviation scope boundary.

## Deviations from Plan

None — plan executed exactly as written. The two tasks were implemented in one GREEN commit (rather than two) because the ATTACH_COUNT guard change is a logical prerequisite for the mirror block; separating them would create an invalid intermediate bash state.

## Issues Encountered

Pre-existing test failures in `pkg/compiler/` package exist independent of this plan's changes. Verified via `git stash` baseline check — identical 6 failures both before and after my changes. Logged to deferred items scope boundary.

## Next Phase Readiness

- Plan 03 delivers the sandbox-side mirror and wrapper
- Plan 02 (bridge-side Attachments fork) has already shipped
- Plans 04 (IAM), 05 (wiring), and 06 (deploy + UAT) complete the loop
- The compile-time tests prove bash control flow is correct; live behavior (S3 download in sandbox) arrives with Plan 06 UAT

---
*Phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels*
*Completed: 2026-05-15*
