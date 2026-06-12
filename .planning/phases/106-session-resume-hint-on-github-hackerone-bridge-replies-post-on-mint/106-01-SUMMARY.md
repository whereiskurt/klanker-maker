---
phase: 106-session-resume-hint-on-github-hackerone-bridge-replies-post-on-mint
plan: "01"
subsystem: compiler-tests
tags: [tdd, red-tests, github-inbound, h1-inbound, resume-hint, phase-106]
dependency_graph:
  requires: []
  provides: [RESUME-HINT-TESTS]
  affects: [pkg/compiler/userdata_github_inbound_test.go, pkg/compiler/userdata_h1_byte_identity_test.go]
tech_stack:
  added: []
  patterns: [TDD-RED, substring-assertion, heredoc-extraction]
key_files:
  created: []
  modified:
    - pkg/compiler/userdata_github_inbound_test.go
    - pkg/compiler/userdata_h1_byte_identity_test.go
decisions:
  - "Scoped all assertions to extractGitHubInboundPoller output (not whole userdata) to prove hint lives inside the GitHub poller block"
  - "Locked post-on-mint condition as literal string assertion: '$NEW_GITHUB_SESSION' != '${GITHUB_SESSION:-}' — prevents silent drift"
  - "Extended existing wantSubstrings slice (not a new function) for H1 test, preserving the 7 pre-existing entries"
metrics:
  duration: 92s
  completed: "2026-06-12T02:48:16Z"
  tasks_completed: 2
  files_modified: 2
---

# Phase 106 Plan 01: Wave 0 RED test scaffold — GitHub + H1 resume-hint assertions

RED test scaffold that pins Phase 106's post-on-mint resume-hint behavior for both the
GitHub inbound poller and HackerOne inbound poller before any production edit to userdata.go.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Add RED GitHub resume-hint test | d2862d9c | pkg/compiler/userdata_github_inbound_test.go |
| 2 | Extend H1 enabled-poller test with hint markers | 22635fb4 | pkg/compiler/userdata_h1_byte_identity_test.go |

## What Was Built

**Task 1:** Added `TestUserdata_GitHubInboundPoller_ResumeHint` to `userdata_github_inbound_test.go`.
The function uses the existing `minimalGitHubInboundProfile` / `compileGitHubInboundUserData` /
`extractGitHubInboundPoller` helpers and asserts 8 substrings scoped to the GITHUBINBOUND
heredoc body:
- `<details>` — collapsible fold opener
- `🔧 Resume` — locked summary emoji+wording
- `claude --resume` — Claude resume branch
- `codex exec resume` — Codex resume branch
- `/workspace` — run-from directory (not /home/sandbox)
- `SANDBOX_ID` — sandbox id referenced in hint body
- `|| true` — best-effort non-blocking guard
- `"$NEW_GITHUB_SESSION" != "${GITHUB_SESSION:-}"` — exact post-on-mint condition guard

**Task 2:** Extended `wantSubstrings` in `TestUserdataH1EnabledRendersPoller` with 6 Phase 106
hint-marker entries (tagged `// Phase 106 resume-hint markers`):
- `<details>`, `🔧 Resume`, `claude --resume`, `codex exec resume`, `/workspace`
- `"$NEW_H1_SESSION" != "${H1_SESSION:-}"` — H1 post-on-mint condition

## Verification Results

| Check | Result |
|-------|--------|
| `go vet ./pkg/compiler/` | CLEAN |
| `TestUserdata_GitHubInboundPoller_ResumeHint` at HEAD | RED (missing-substring on `<details>`) |
| `TestUserdataH1EnabledRendersPoller` at HEAD | RED (4 missing Phase 106 markers) |
| `TestUserdataH1ByteIdentity` (dormancy golden) | GREEN — unaffected |
| `TestUserdataKmPrefixByteIdentity` (dormancy golden) | GREEN — unaffected |

The H1 test failure is on the Phase 106 markers only — all 7 pre-existing wantSubstrings
entries still compile correctly and will pass when the production code is present.

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- `pkg/compiler/userdata_github_inbound_test.go` — present, contains `TestUserdata_GitHubInboundPoller_ResumeHint`
- `pkg/compiler/userdata_h1_byte_identity_test.go` — present, contains `codex exec resume`
- Commit d2862d9c — found in git log
- Commit 22635fb4 — found in git log
- Both tests RED at HEAD (confirmed by test run output above)
- No production code changed
