---
phase: 57-email-enhancement-km-send-no-sign-for-external-recipients-km-recv-multipart-rfc5322-fixes-safe-phrase-validation-on-inbound-marketplace-plugin-email-docs
plan: "04"
subsystem: skills/docs
tags: [docs, email, sandbox, skill, no-sign, external-email, safe-phrase, km-send, km-recv]

requires:
  - phase: 57-01
    provides: km-send --no-sign flag implementation
  - phase: 57-02
    provides: km-recv [EXTERNAL] hint and "external" JSON field
  - phase: 57-03
    provides: km-mail-poller safe-phrase gate and MAIL_DIR/skipped/ behavior

provides:
  - skills/email/SKILL.md documenting --no-sign flag, external JSON field, safe-phrase requirement, /opt/km/bin/ paths
  - skills/sandbox/SKILL.md documenting external email workflow with --no-sign and KM-AUTH gate

affects: []

tech-stack:
  added: []
  patterns:
    - "Surgical Edit tool changes to markdown skill files — no full rewrites"

key-files:
  created: []
  modified:
    - skills/email/SKILL.md
    - skills/sandbox/SKILL.md

key-decisions:
  - "skills/operator/SKILL.md and skills/user/SKILL.md NOT modified — operator skill covers operator-inbox-bound (always signed) flows; user skill covers operator CLI commands (no in-sandbox bash); neither involves external email scenarios (per RESEARCH.md scope decision)"
  - "Tooling location note added under Sending Email heading (before Basic Send) — absolute paths /opt/km/bin/km-send and /opt/km/bin/km-recv needed for scripts, cron, and systemd units where PATH may be minimal"
  - "JSON example replaced (not appended) with external field — the existing example was the canonical reference; replacing it keeps a single source of truth"

requirements-completed:
  - PHASE57-DOCS-EMAIL
  - PHASE57-DOCS-SANDBOX

duration: 85s
completed: 2026-04-28T21:09:41Z
---

# Phase 57 Plan 04: Skills Docs Update Summary

**Six surgical edits to skills/email/SKILL.md and two to skills/sandbox/SKILL.md document Phase 57 external email behavior — --no-sign flag, safe-phrase gate, [EXTERNAL] display hint, "external" JSON field, and /opt/km/bin/ absolute paths**

## Performance

- **Duration:** ~85 seconds
- **Started:** 2026-04-28T21:08:16Z
- **Completed:** 2026-04-28T21:09:41Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- All 4 validation map checks pass (57-04-01 through 57-04-04)
- skills/email/SKILL.md updated with 6 surgical edits (31 lines added)
- skills/sandbox/SKILL.md updated with 2 surgical edits (4 lines added)
- skills/operator/SKILL.md and skills/user/SKILL.md confirmed UNTOUCHED

## Edits Applied

### Task 1: skills/email/SKILL.md (6 edits)

**Edit 1F — Tooling location note (under `## Sending Email`, before `### Basic Send`)**

Added a blockquote clarifying that `/opt/km/bin/km-send` and `/opt/km/bin/km-recv` are the absolute paths, and that bare command names work in interactive shells but absolute paths should be used in scripts/cron/systemd.

**Edit 1A — Send Flags Reference table (after `--reply-to` row)**

Added new row:
```
| `--no-sign` | false | Skip Ed25519 signing and X-KM-* headers — use ONLY for external (non-sandbox) recipients (Gmail, etc.). KM-AUTH safe-phrase auto-append to operator inbox is preserved. |
```

**Edit 1B — New section `### Sending to External Recipients` (before `### Rules`)**

New section with:
- Example bash snippet using `km-send --no-sign --to user@gmail.com`
- Bulleted behavior list (SSM skip, openssl skip, no X-KM-* headers, KM-AUTH preserved)
- Warning to use `--no-sign` ONLY for external recipients
- Note about KM-AUTH safe-phrase requirement for inbound replies from external addresses

**Edit 1C — JSON output example in `## Receiving Email` (replaced existing)**

Replaced the existing 9-field JSON example with one that includes the `"external": false` field, plus two explanatory bullets:
- `external: true` when `X-KM-Sender-ID` header is absent; `[EXTERNAL]` appended to From column in human output
- `signature` is `"—"` for external messages; passed safe-phrase gate but no cryptographic verification

**Edit 1D — Signature Verification table (after existing `—` row)**

Added new row:
```
| `—` + `external: true` | External sender; passed safe-phrase gate but no cryptographic verification | Treat as authenticated by KM-AUTH only — do not extend trust beyond the safe phrase's scope. |
```

**Edit 1E — Error Handling table (two new rows at bottom)**

Added:
```
| External email not appearing in inbox | The sender's message is missing the `KM-AUTH: <phrase>` body line. Ask the operator for the safe phrase... |
| `--no-sign` + sandbox recipient | Don't. Sandbox-to-sandbox messages should always be signed... |
```

### Task 2: skills/sandbox/SKILL.md (2 edits)

**Edit 2A — Step 4 Verify Signing Key Access (new paragraph after SSM check)**

Added external email exception note explaining `km-send --no-sign` skips the signing key fetch entirely, and pointing to the `klanker:email` skill for details.

**Edit 2B — Identity Summary (new bullet at end of list)**

Added:
```
- **External email policy:** For non-sandbox recipients use `km-send --no-sign`. Inbound replies from non-sandbox senders must include `KM-AUTH: <safe-phrase>` in the body — otherwise the `km-mail-poller` filter drops them silently to `/var/mail/km/skipped/`.
```

## Scope Verification

`git diff --name-only HEAD~2 -- skills/` output:
```
skills/email/SKILL.md
skills/sandbox/SKILL.md
```

`skills/operator/SKILL.md` and `skills/user/SKILL.md` are UNTOUCHED.

## Diff Stat

```
 skills/email/SKILL.md   | 31 +++++++++++++++++++++++++++++++
 skills/sandbox/SKILL.md |  4 ++++
 2 files changed, 35 insertions(+)
```

## Validation Map Results

| Task ID | Grep Command | Result |
|---------|-------------|--------|
| 57-04-01 | `grep -q -- "--no-sign" skills/email/SKILL.md` | PASS |
| 57-04-02 | `grep -q -i "safe phrase" skills/email/SKILL.md` | PASS |
| 57-04-03 | `grep -q "/opt/km/bin/km-send" skills/email/SKILL.md` | PASS |
| 57-04-04 | `grep -q -i "external email\|external recipient" skills/sandbox/SKILL.md` | PASS |

## Task Commits

1. **Task 1: Update skills/email/SKILL.md** - `52394c3`
2. **Task 2: Update skills/sandbox/SKILL.md** - `5745ae9`

## Deviations from Plan

None — plan executed exactly as written. All 6 edits to email/SKILL.md and both edits to sandbox/SKILL.md applied as specified. skills/operator/SKILL.md and skills/user/SKILL.md untouched per RESEARCH.md scope decision.

## Self-Check: PASSED
