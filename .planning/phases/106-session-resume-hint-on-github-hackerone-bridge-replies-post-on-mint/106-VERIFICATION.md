---
phase: 106-session-resume-hint-on-github-hackerone-bridge-replies-post-on-mint
verified: 2026-06-12T00:00:00Z
status: passed
score: 7/7 must-haves verified
re_verification: false
---

# Phase 106: Session-Resume Hint on GitHub + HackerOne Bridge Replies (Post-on-Mint) Verification Report

**Phase Goal:** After a bridge agent turn, surface the operator-facing `--resume` handle directly in the GitHub PR/issue reply and the HackerOne report so an operator can re-attach to the exact Claude/Codex session without querying DynamoDB. Each relevant poller (GitHub + HackerOne ONLY — Slack deliberately excluded) posts ONE extra collapsed `<details>` comment carrying the run-from directory (`/workspace`) + the agent-correct resume command (claude --resume / codex exec resume), keyed on the freshly-extracted session id, fired ONLY when the session id is newly minted (post-on-mint). GitHub hint is a public collapsed comment; the HackerOne hint is INTERNAL-only (no --reply-to-researcher, never visible to the external researcher). Best-effort (|| true), never blocks the SQS ack. No SandboxProfile schema change, no new TF resource, no DDB schema change.

**Verified:** 2026-06-12
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | GitHub poller posts `<details>` hint block with both agent branches, /workspace, SANDBOX_ID, `\|\| true`, and post-on-mint guard | VERIFIED | `pkg/compiler/userdata.go` lines 2399-2411; `go test ./pkg/compiler/ -run TestUserdata_GitHubInboundPoller_ResumeHint` PASS |
| 2 | H1 poller posts `<details>` hint block INTERNAL-only (no --reply-to-researcher) with both agent branches, /workspace, SANDBOX_ID, `\|\| true`, and post-on-mint guard | VERIFIED | `pkg/compiler/userdata.go` lines 2722-2734; no `--reply-to-researcher` in hint call; `go test ./pkg/compiler/ -run TestUserdataH1EnabledRendersPoller` PASS |
| 3 | Slack poller remains byte-identical — no Phase 106 content in lines 1535-2085 | VERIFIED | `grep "Phase 106\|RESUME_CMD\|HINT_BODY"` in Slack bounds = 0 hits; byte-identity tests PASS |
| 4 | Full `go test ./pkg/compiler/ -count=1 -timeout 300s` is GREEN | VERIFIED | EXIT=0, `ok github.com/whereiskurt/klanker-maker/pkg/compiler 5.509s` |
| 5 | docs/github-bridge.md has Phase 106 section with /workspace, make build-lambdas, post-on-mint, deploy surface | VERIFIED | `## Phase 106` at line 1747; `/workspace`, `make build-lambdas`, collapsed fold, post-on-mint semantics, `km init --dry-run=false`, Slack excluded all present |
| 6 | docs/h1-bridge.md has Phase 106 section with INTERNAL-only note, /workspace, deploy surface | VERIFIED | `## Phase 106` at line 435; INTERNAL-only safety property section at line 444 names no --reply-to-researcher explicitly |
| 7 | CLAUDE.md carries Phase 106 (complete) note above Phase 105; skills/init/SKILL.md corrected to route userdata edits to make build-lambdas + km init | VERIFIED | CLAUDE.md line 21; skills/init/SKILL.md line 239 (`make build-lambdas` for create-handler-embedded userdata); old `--sidecars ... userdata template changed` line absent |

**Score:** 7/7 truths verified

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/compiler/userdata.go` | GitHub GITHUBINBOUND heredoc resume-hint block | VERIFIED | Lines 2399-2411: Phase 106 comment, post-on-mint if-guard, EFFECTIVE_AGENT branch, HINT_BODY printf with `<details>`, `km-github comment ... \|\| true` |
| `pkg/compiler/userdata.go` | H1 H1INBOUND heredoc resume-hint block (internal-only) | VERIFIED | Lines 2722-2734: Phase 106 comment, post-on-mint if-guard, EFFECTIVE_AGENT branch, HINT_BODY printf, `km-h1 comment --report ... --body ... \|\| true` (NO --reply-to-researcher) |
| `pkg/compiler/userdata_github_inbound_test.go` | `TestUserdata_GitHubInboundPoller_ResumeHint` function | VERIFIED | Lines 321-355: doc comment naming Phase 106 + post-on-mint contract; all 8 required substrings asserted; scoped to `extractGitHubInboundPoller` output |
| `pkg/compiler/userdata_h1_byte_identity_test.go` | Extended `wantSubstrings` with Phase 106 hint markers | VERIFIED | Lines 161-167: 6 Phase 106 markers appended with `// Phase 106 resume-hint markers` comment |
| `docs/github-bridge.md` | `## Phase 106` section | VERIFIED | Line 1747; 79 lines covering what/frequency/robustness/deploy/Slack exclusion |
| `docs/h1-bridge.md` | `## Phase 106` section with INTERNAL-only note | VERIFIED | Line 435; INTERNAL-only safety property section is its own subsection |
| `CLAUDE.md` | Phase 106 (complete) phase note | VERIFIED | Line 21; reverse-chronological above Phase 105 at line 30 |
| `skills/init/SKILL.md` | Corrected rollout-sequence template | VERIFIED | Line 239: `make build-lambdas # if create-handler-embedded userdata`; old inaccurate `--sidecars ... userdata template changed` absent |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| GITHUBINBOUND heredoc (~line 2402) | `/opt/km/bin/km-github comment --repo "$REPO" --number "$NUMBER"` | post-on-mint if-guard + printf HINT_BODY | WIRED | `"$NEW_GITHUB_SESSION" != "${GITHUB_SESSION:-}"` guard present; `km-github comment` call at line 2410 with `\|\| true` |
| H1INBOUND heredoc (~line 2725) | `/opt/km/bin/km-h1 comment --report "$REPORT_ID"` | post-on-mint if-guard + printf HINT_BODY, no --reply-to-researcher | WIRED | `"$NEW_H1_SESSION" != "${H1_SESSION:-}"` guard present; `km-h1 comment` call at line 2733 with `\|\| true`; no `--reply-to-researcher` |
| GitHub hint if-guard | `NEW_GITHUB_SESSION` vs `GITHUB_SESSION` comparison | post-on-mint condition | WIRED | Exact literal `"$NEW_GITHUB_SESSION" != "${GITHUB_SESSION:-}"` present; test asserts exact string |
| H1 hint if-guard | `NEW_H1_SESSION` vs `H1_SESSION` comparison | post-on-mint condition | WIRED | Exact literal `"$NEW_H1_SESSION" != "${H1_SESSION:-}"` present; test asserts exact string |
| Slack poller (lines 1535-2085) | (deliberate non-link) | exclusion invariant | WIRED | Zero occurrences of `Phase 106`, `RESUME_CMD`, `HINT_BODY` in Slack bounds; byte-identity tests pass |

---

### Requirements Coverage

All requirement IDs are phase-internal (not present in `.planning/REQUIREMENTS.md`). Coverage is assessed against plan frontmatter declarations:

| Requirement | Source Plans | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| RESUME-HINT-FORMAT | 106-02, 106-03 | `<details>` fold format, both agent branches, /workspace, SANDBOX_ID | SATISFIED | printf format at lines 2408, 2731; `<details>`, `🔧 Resume`, `/workspace`, `$SANDBOX_ID` all present |
| RESUME-HINT-MINT | 106-02, 106-03 | Post-on-mint guard (fires only on new/changed session id) | SATISFIED | `if [ -n "$NEW_GITHUB_SESSION" ] && [ "$NEW_GITHUB_SESSION" != "${GITHUB_SESSION:-}" ]` at line 2402; analogous H1 guard at line 2725 |
| RESUME-HINT-GITHUB | 106-02 | GitHub hint via km-github comment, `\|\| true` | SATISFIED | `km-github comment --repo "$REPO" --number "$NUMBER" --body "$HINT_BODY" \|\| true` at line 2410 |
| RESUME-HINT-H1 | 106-03 | H1 hint INTERNAL-only, no --reply-to-researcher, `\|\| true` | SATISFIED | `km-h1 comment --report "$REPORT_ID" --body "$HINT_BODY" \|\| true` at line 2733; no `--reply-to-researcher` |
| RESUME-HINT-SLACK-EXCLUDED | 106-02, 106-03 | Slack poller byte-identical | SATISFIED | 0 Phase-106 tokens in lines 1535-2085; byte-identity tests pass |
| RESUME-HINT-TESTS | 106-01 | RED-then-GREEN test scaffold | SATISFIED | `TestUserdata_GitHubInboundPoller_ResumeHint` (8 assertions); `TestUserdataH1EnabledRendersPoller` extended with 6 Phase 106 markers; both GREEN |
| RESUME-HINT-DOCS | 106-04 | Phase 106 sections in github-bridge.md, h1-bridge.md, CLAUDE.md; corrected SKILL.md rollout template | SATISFIED | All four files updated; `## Phase 106` in both bridge docs; CLAUDE.md note at line 21; SKILL.md corrected |

---

### Anti-Patterns Found

None. No TODOs, FIXMEs, placeholder returns, or stub implementations found in modified files.

---

### Human Verification Required

**1. Live resume command re-attachment (optional UAT)**

**Test:** Trigger a GitHub bridge turn on a real PR; observe exactly ONE collapsed hint comment on first turn and none on a same-session follow-up. Copy the displayed `claude --resume <id>` command, `cd /workspace` on the sandbox, run it, confirm session re-attaches.

**Expected:** One hint fold on first turn; zero on stable follow-up turns; session re-attaches successfully.

**Why human:** Requires a live sandbox, live GitHub App webhook delivery, and SSM access. Not unit-testable.

**2. H1 internal-only comment verification (optional UAT)**

**Test:** Trigger an H1 bridge turn; confirm the resume-hint comment appears ONLY on the internal/team comment track in HackerOne, not visible to the external researcher.

**Expected:** Hint is on the internal comment track only; researcher view shows no hint.

**Why human:** Requires a live HackerOne sandbox and access to both the internal and external comment views.

---

### Gaps Summary

No gaps. All 7 must-haves are verified. All 7 requirement IDs from plan frontmatter are satisfied. The full `go test ./pkg/compiler/` suite is GREEN (EXIT=0). The Slack-exclusion invariant holds. The H1 internal-only safety property is present in code and documented. Both bridge docs carry Phase 106 sections with the locked `/workspace` run-from directory, post-on-mint semantics, and the correct deploy surface (`make build-lambdas` + `km init --dry-run=false` + recreate; NOT `--sidecars`).

---

_Verified: 2026-06-12_
_Verifier: Claude (gsd-verifier)_
