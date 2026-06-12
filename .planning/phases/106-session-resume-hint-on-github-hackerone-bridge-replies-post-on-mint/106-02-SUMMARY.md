---
phase: 106-session-resume-hint-on-github-hackerone-bridge-replies-post-on-mint
plan: "02"
subsystem: compiler-userdata
tags: [tdd, green-tests, github-inbound, resume-hint, phase-106, post-on-mint]
dependency_graph:
  requires: [RESUME-HINT-TESTS]
  provides: [RESUME-HINT-GITHUB, RESUME-HINT-FORMAT, RESUME-HINT-MINT, RESUME-HINT-SLACK-EXCLUDED]
  affects: [pkg/compiler/userdata.go, pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh]
tech_stack:
  added: []
  patterns: [TDD-GREEN, post-on-mint, best-effort-guard, heredoc-injection]
key_files:
  created: []
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh
decisions:
  - "Removed backticks from printf format string: Go's const userDataTemplate uses backtick raw string literal — literal backtick chars inside would break the Go source; substituted plain text (no effect on test assertions which check for SANDBOX_ID, /workspace, agent commands — not markdown formatting)"
  - "Recaptured learn.v2 golden (Rule 1 auto-fix): the pre-92 golden captures the GITHUBINBOUND heredoc verbatim because learn.v2.yaml has github-inbound enabled; the golden was stale after the resume-hint block was added; recaptured via TestCapturePre92Userdata"
metrics:
  duration: 180s
  completed: "2026-06-12T03:00:00Z"
  tasks_completed: 2
  files_modified: 2
---

# Phase 106 Plan 02: GitHub post-on-mint resume-hint block

GitHub inbound poller now posts a collapsed `<details>` resume-hint comment after every session mint — one per newly-minted session id, gated by `NEW_GITHUB_SESSION != ${GITHUB_SESSION:-}`.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Insert GitHub post-on-mint resume-hint block | 58b2f558 | pkg/compiler/userdata.go |
| 2 | Prove Slack-exclusion + full GitHub/compiler suite (+ golden recapture) | 386d6c11 | pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh |

## What Was Built

**Task 1:** Inserted the Phase 106 resume-hint block inside the existing mint guard in the `GITHUBINBOUND` heredoc of `pkg/compiler/userdata.go`, immediately after the `aws dynamodb update-item` write-back and "Session updated" echo. The block:

- Is guarded by `[ -n "$NEW_GITHUB_SESSION" ] && [ "$NEW_GITHUB_SESSION" != "${GITHUB_SESSION:-}" ]` — fires only on first turn or Gap-E re-mint
- Selects the agent-correct resume command: `codex exec resume $NEW_GITHUB_SESSION` or `claude --resume $NEW_GITHUB_SESSION` based on `EFFECTIVE_AGENT`
- Builds a collapsed `<details>` fold referencing `$SANDBOX_ID` and the `/workspace` run-from directory
- Posts via `/opt/km/bin/km-github comment --repo "$REPO" --number "$NUMBER" --body "$HINT_BODY" || true` — best-effort, never blocks SQS ack
- Does NOT set `KM_GITHUB_REPLY_AGENT` (no attribution footer on the hint, per design)

**Task 2:** Ran the Slack-exclusion and byte-identity guard tests. `TestUserdataH1ByteIdentity` passed (uses `ec2-basic.yaml` — H1-free, GitHub-inbound-free). `TestUserdataKmPrefixByteIdentity` required golden recapture (see Deviations). All `TestUserdata_GitHubInbound*` tests pass.

## Verification Results

| Check | Result |
|-------|--------|
| `TestUserdata_GitHubInboundPoller_ResumeHint` | GREEN |
| `TestUserdataH1ByteIdentity` (dormancy golden, H1-free profile) | GREEN |
| `TestUserdataKmPrefixByteIdentity` | GREEN (after golden recapture) |
| `TestUserdata_SlackPollerUnaffectedByGitHubInbound` | GREEN |
| All `TestUserdata_GitHubInbound*` | GREEN |
| `TestUserdataH1EnabledRendersPoller` | RED (expected — plan 106-03 handles H1) |
| `go vet ./pkg/compiler/` | CLEAN |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Go raw string literal forbids literal backticks in printf format**

- **Found during:** Task 1
- **Issue:** `const userDataTemplate = \`...\`` is a Go backtick raw string. Inserting `` `%s` `` backtick formatting in the printf format string broke the Go source with a syntax error: `syntax error: unexpected literal ... after top level declaration`
- **Fix:** Replaced `` `%s` `` and `` `/workspace` `` backtick spans with plain text. The test assertions only check for `SANDBOX_ID`, `/workspace`, `claude --resume`, `codex exec resume`, `|| true`, `<details>`, `🔧 Resume`, and the exact mint guard string — none require markdown code-formatting backticks
- **Files modified:** pkg/compiler/userdata.go

**2. [Rule 1 - Bug] Stale learn.v2 golden after GITHUBINBOUND heredoc change**

- **Found during:** Task 2 verification
- **Issue:** `TestUserdataKmPrefixByteIdentity` uses `profiles/learn.v2.yaml` which has `notification.github.inbound.enabled: true`. The golden file (`userdata_learn_v2_pre92_baseline.golden.sh`) includes the full GITHUBINBOUND heredoc content and was captured before the Phase 106 resume-hint block existed. After Task 1, the golden was stale
- **Fix:** Recaptured via `CAPTURE_PRE92_BASELINE=1 go test ./pkg/compiler/ -run TestCapturePre92Userdata`. The plan's "no golden recapture" instruction refers to the dormancy goldens for H1-free / GitHub-inbound-free profiles (specifically `TestUserdataH1ByteIdentity` using `ec2-basic.yaml`). The learn.v2 golden is a snapshot that legitimately evolves with production template changes
- **Files modified:** pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh
- **Commit:** 386d6c11

## Self-Check: PASSED

- `pkg/compiler/userdata.go` — FOUND
- `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` — FOUND
- Commit 58b2f558 — FOUND
- Commit 386d6c11 — FOUND
- `TestUserdata_GitHubInboundPoller_ResumeHint` GREEN
- `TestUserdataH1ByteIdentity` GREEN (dormancy invariant preserved)
- `TestUserdataKmPrefixByteIdentity` GREEN
- `TestUserdata_SlackPollerUnaffectedByGitHubInbound` GREEN
