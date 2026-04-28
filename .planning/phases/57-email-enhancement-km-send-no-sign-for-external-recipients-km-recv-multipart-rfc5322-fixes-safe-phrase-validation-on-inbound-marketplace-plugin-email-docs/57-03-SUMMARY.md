---
phase: 57-email-enhancement-km-send-no-sign-for-external-recipients-km-recv-multipart-rfc5322-fixes-safe-phrase-validation-on-inbound-marketplace-plugin-email-docs
plan: "03"
subsystem: compiler/userdata
tags: [km-mail-poller, safe-phrase, bash, email, heredoc, inbound, filtering, ssm, grep-fixed-string]

requires:
  - phase: 57-00
    provides: 7 RED test stubs for km-mail-poller in userdata_phase57_test.go
  - phase: 57-02
    provides: km-recv RFC 5322 + multipart/alternative + [EXTERNAL] hint (no code dependency, wave sequencing)

provides:
  - km-mail-poller safe-phrase gate: external email without KM-AUTH phrase dropped to MAIL_DIR/skipped/
  - Unconditional sender_id/sender_email extraction (hoisted out of KM_ALLOWED_SENDERS block)
  - SSM-cached safe phrase (KM_SAFE_PHRASE_CACHED) fetched at poller startup, fail-open
  - Fixed-string phrase match via grep -qF (no regex injection from SSM-stored phrase values)

affects:
  - phase: 57-04 (skills docs should document safe-phrase inbound enforcement)

tech-stack:
  added: []
  patterns:
    - "Pre-loop SSM cache: KM_SAFE_PHRASE_CACHED fetched once before while-true, fail-open with || true"
    - "Unconditional header extraction: sender_from/sender_email/sender_id hoisted above both gate blocks"
    - "Series gate design: allowlist check then safe-phrase check — both apply independently"
    - "grep -qF for fixed-string match: prevents regex metacharacter injection from SSM-stored values"
    - "Fail-open semantics: empty KM_SAFE_PHRASE_CACHED skips the phrase gate entirely"

key-files:
  created: []
  modified:
    - pkg/compiler/userdata.go

key-decisions:
  - "Enforcement layer is km-mail-poller (bash systemd service), NOT a SES receipt rule Lambda — sandbox_inbound SES rule is pure S3 action with no Lambda hook (infra/modules/ses/v1.0.0/main.tf line 126)"
  - "grep -qF (fixed string) required over grep -qP/-qE — SSM phrase values may contain regex metacharacters (RESEARCH.md Pitfall 4)"
  - "sender_id/sender_email extraction hoisted unconditionally — when KM_ALLOWED_SENDERS is unset the allowlist block is skipped entirely, so sender_id was never set, breaking the sandbox-vs-external distinction needed by the phrase gate (RESEARCH.md Pitfall 5)"
  - "Fail-open when SSM unreachable — consistent with existing allowlist behavior (email passes when KM_ALLOWED_SENDERS is unset); log WARN to stderr"
  - "Sandbox-to-sandbox email (X-KM-Sender-ID non-empty) bypasses phrase gate — already cryptographically signed; phrase is for unauthenticated external senders only"

requirements-completed:
  - PHASE57-MAILPOLLER-SAFEPHRASE
  - PHASE57-MAILPOLLER-SENDERID-SCOPING
  - PHASE57-MAILPOLLER-FIXEDSTRING

duration: 5min
completed: 2026-04-28T20:46:00Z
---

# Phase 57 Plan 03: km-mail-poller Safe Phrase Gate Summary

**Two surgical edits to the MAILPOLL heredoc in userdata.go add SSM-cached safe-phrase validation for inbound external email: hoisted sender_id extraction, fail-open SSM fetch at startup, and grep -qF fixed-string phrase gate — turning all 7 RED Phase-57 MailPoller tests GREEN**

## Performance

- **Duration:** ~5 min
- **Started:** 2026-04-28T20:40:27Z
- **Completed:** 2026-04-28T20:46:00Z
- **Tasks:** 2
- **Files modified:** 1

## Accomplishments

- All 7 Phase-57 km-mail-poller tests turned GREEN: FetchesSafePhrase, SsmFailOpen, ExtractsSenderIdUnconditionally, DropsExternalNoPhrase, DeliversExternalWithPhrase, SkipsCheckForSandbox, PhraseMatchUsesFixedString
- All pre-existing `pkg/compiler/...` tests remain GREEN (full regression suite passes)
- `make build` succeeds: km v0.2.412

## Task Commits

1. **Task 1: Add safe-phrase SSM fetch to km-mail-poller startup** - `bb50f42` (feat)
2. **Task 2: Hoist sender_id extraction + add safe-phrase gate with grep -F** - `b3d7292` (feat)

## Files Created/Modified

- `pkg/compiler/userdata.go` — Two edits inside the MAILPOLL heredoc (lines ~606-701):
  - **Edit 1A:** Inserted SSM pre-fetch block before `while true; do` loop, declaring `KM_SAFE_PHRASE_CACHED` with fail-open `|| true` semantics and startup log
  - **Edit 2A:** Hoisted `sender_from`/`sender_email`/`sender_id` extraction unconditionally after `mv "$tmp_file" "$local_file"` (removed duplicate extraction from inside allowlist block)
  - **Edit 2B:** Inserted safe-phrase gate block after the allowlist `fi` and before `echo "[km-mail-poller] New mail: $key"` using `grep -qF` for fixed-string match

## Decisions Made

- **grep -qF over grep -qP:** The RESEARCH.md draft (Pattern 6) used `grep -qP "KM-AUTH:\s*..."` — this was explicitly replaced with `grep -qF "KM-AUTH: ${KM_SAFE_PHRASE_CACHED}"` per Pitfall 4 recommendation. SSM phrase values may contain `.`, `+`, `?`, `*`, or other regex metacharacters, creating injection risk or false positives with `-P`/`-E`.
- **Fail-open semantics:** When SSM is unreachable at poller startup, `KM_SAFE_PHRASE_CACHED` stays empty and the phrase gate is skipped. This is consistent with the existing allowlist behavior (email flows when `KM_ALLOWED_SENDERS` is unset). A WARN is logged to stderr.
- **Unconditional sender extraction:** `sender_id` was previously only set inside the `if [ -n "${KM_ALLOWED_SENDERS:-}" ]` block. When no allowlist is configured that block is skipped, leaving `sender_id` unset — the phrase gate cannot distinguish sandbox senders from external senders. Fix: extract unconditionally right after To-header match.
- **Gate sequencing:** Allowlist check runs first, safe-phrase check runs second. The safe-phrase check applies even when `KM_ALLOWED_SENDERS` is `*` (all senders allowed) — the two gates are independent and in series.

## Deviations from Plan

### Architecture Deviation (Documented, Not a Code Change)

**Enforcement layer: km-mail-poller (bash systemd service) NOT a SES receipt rule Lambda**

The Phase 57 roadmap goal text stated: "the SES receipt rule Lambda validates this before delivery." This description does NOT match the codebase.

**Evidence:** `infra/modules/ses/v1.0.0/main.tf` line 126 shows the `sandbox_inbound` SES receipt rule is a plain S3 action with no Lambda trigger. Contrast with the `create_inbound` rule (lines 74-89) which conditionally uses a Lambda when `email_create_handler_arn != ""`. The `sandbox_inbound` rule has no equivalent Lambda hook.

**Correct enforcement layer:** km-mail-poller, which already runs as a per-sandbox systemd service and already enforces sender-allowlist filtering (lines 658-686). Adding a safe-phrase gate alongside the allowlist is a localized, additive bash change that requires no new Terraform, no new IAM roles, no new SES rule ordering.

**References:** RESEARCH.md Section "SES Architecture" table + `infra/modules/ses/v1.0.0/main.tf` line 126.

### Code Changes: All as Specified

No code deviations from the plan's Edit 1A, 2A, and 2B specifications. The only deviation is the architecture clarification above (documented from the plan's objective section itself).

## Test Results

```
=== RUN   TestUserData_MailPoller_ExtractsSenderIdUnconditionally
--- PASS: TestUserData_MailPoller_ExtractsSenderIdUnconditionally (0.00s)
=== RUN   TestUserData_MailPoller_FetchesSafePhrase
--- PASS: TestUserData_MailPoller_FetchesSafePhrase (0.00s)
=== RUN   TestUserData_MailPoller_DropsExternalNoPhrase
--- PASS: TestUserData_MailPoller_DropsExternalNoPhrase (0.00s)
=== RUN   TestUserData_MailPoller_DeliversExternalWithPhrase
--- PASS: TestUserData_MailPoller_DeliversExternalWithPhrase (0.00s)
=== RUN   TestUserData_MailPoller_SkipsCheckForSandbox
--- PASS: TestUserData_MailPoller_SkipsCheckForSandbox (0.00s)
=== RUN   TestUserData_MailPoller_SsmFailOpen
--- PASS: TestUserData_MailPoller_SsmFailOpen (0.00s)
=== RUN   TestUserData_MailPoller_PhraseMatchUsesFixedString
--- PASS: TestUserData_MailPoller_PhraseMatchUsesFixedString (0.00s)
PASS
ok      github.com/whereiskurt/klankrmkr/pkg/compiler   0.385s
```

Full regression:
```
ok      github.com/whereiskurt/klankrmkr/pkg/compiler   0.296s
```

## make build Output

```
go build -ldflags '... -X .../version.Number=v0.2.412 ...' -o km ./cmd/km/
Built: km v0.2.412 (bb50f42)
```

## Fixed-String Match Confirmation

The phrase match uses `grep -qF "KM-AUTH: ${KM_SAFE_PHRASE_CACHED}"` — a fixed-string (not regex) match. This is verified by `TestUserData_MailPoller_PhraseMatchUsesFixedString`. Any SSM phrase value containing regex metacharacters (`.`, `+`, `?`, `*`, `[`, `(`, etc.) is treated as a literal string, eliminating injection risk and ensuring exact match semantics.

## Issues Encountered

None — the plan's action specifications were complete and accurate. All landmark line numbers from RESEARCH.md matched the actual code locations.

## Next Phase Readiness

- Plan 57-03 is complete: all 7 Phase-57 MailPoller tests GREEN; all pre-existing tests GREEN
- Phase 57 implementation is now 3/4 done (Plans 00-03 complete; Plan 04 skills docs updates remaining)
- Plan 57-04 (skills/email/SKILL.md + skills/sandbox/SKILL.md updates) can proceed immediately

---
*Phase: 57-email-enhancement*
*Completed: 2026-04-28*
