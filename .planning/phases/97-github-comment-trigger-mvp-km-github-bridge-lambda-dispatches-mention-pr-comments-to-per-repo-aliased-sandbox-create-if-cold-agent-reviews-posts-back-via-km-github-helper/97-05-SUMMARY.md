---
phase: 97-github-comment-trigger-mvp
plan: 05
subsystem: infra
tags: [github, sqs, userdata, systemd, bash, km-github, poller, comment, review]

# Dependency graph
requires:
  - phase: 97-02
    provides: per-sandbox GitHub installation token at SSM /{prefix}/sandbox/{id}/github-token
  - phase: 97-03
    provides: GitHubInboundQueueName helper + KM_GITHUB_INBOUND_QUEUE_URL SSM path
provides:
  - source-aware github-inbound FIFO poller in userdata.go (GitHubInboundEnabled template param)
  - GitHub context preamble builder (repo/PR/branch/head_sha + worktree guidance) dispatching to agent
  - km-github helper binary (comment + review verbs) with per-sandbox installation token
  - km-github-inbound-poller.service systemd unit (EnvironmentFile=-/etc/km/notify.env)
affects:
  - sandbox provisioning (userdata rendered on km create)
  - sandbox agent workflow (GitHub comment-trigger dispatch)

# Tech tracking
tech-stack:
  added: [cmd/km-github binary]
  patterns:
    - github-inbound poller mirrors slack-inbound-poller pattern (bash heredoc + systemd + TDD)
    - testable inner entry point (runCommentWith/runReviewWith) bypasses SSM for httptest coverage
    - GitHubAPIBaseURL package-level var for httptest injection (mirrors pkg/github/token.go)
    - byte-identity preserved via whitespace-aware Go template if/end blocks

key-files:
  created:
    - pkg/compiler/userdata_github_inbound_test.go
    - cmd/km-github/main.go
    - cmd/km-github/main_test.go
  modified:
    - pkg/compiler/userdata.go

key-decisions:
  - "Separate binary cmd/km-github (not embedded in km) mirrors cmd/km-slack dispatch pattern; deployed via S3 artifact download in userdata conditionally gated on GitHubInboundEnabled"
  - "GitHub context preamble includes worktree-per-PR guidance for concurrent PR review in long-lived sandboxes"
  - "APPROVE does not require body; COMMENT and REQUEST_CHANGES do — validated at flag parse time before any HTTP call"
  - "commit_id is omitted from review JSON payload when empty (reviewPayload struct uses omitempty)"
  - "Byte-identity preservation: {{- if .GitHubInboundEnabled }}{{- end }} blocks must not introduce extra newlines in existing profiles that don't enable github-inbound"

patterns-established:
  - "GitHub inbound poller TDD: RED test file written first, then GREEN implementation, then byte-identity regression verified"
  - "Template whitespace: {{- if false }}{{- end }} blocks eat surrounding whitespace correctly when content matches existing patterns"

requirements-completed: [GH-POLLER, GH-HELPER]

# Metrics
duration: 15min
completed: 2026-06-06
---

# Phase 97 Plan 05: Sandbox-Side GitHub Inbound Poller + km-github Helper Summary

**Source-aware FIFO poller in userdata.go builds GitHub context preamble and dispatches to agent; km-github binary posts comments/reviews via per-sandbox installation token**

## Performance

- **Duration:** 15 min
- **Started:** 2026-06-06T19:40:23Z
- **Completed:** 2026-06-06T19:54:47Z
- **Tasks:** 2
- **Files modified:** 4 (1 created test file, 3 new files, 1 modified)

## Accomplishments

- Source-aware github-inbound poller: drains KM_GITHUB_INBOUND_QUEUE_URL FIFO, parses GitHub envelope (repo/PR/branch/head_sha/sender/body), builds context preamble with worktree-per-PR guidance, dispatches via claude -p or codex exec dispatch fork
- GitHub poller systemd unit (km-github-inbound-poller.service) wired into systemctl enable/restart, gated on GitHubInboundEnabled; dormant when disabled (zero byte delta to pre-97 baselines)
- km-github binary: comment and review subcommands with correct API shape, token from SSM, header enforcement (Authorization/Accept/X-GitHub-Api-Version), event validation
- 24 tests total (12 per task): preamble, dispatch, queue drain, SSM fallback, systemd unit, systemctl enable, env var, dormant invariant, comment shape, review events, missing flags

## Task Commits

1. **Task 1: source-aware github poller in userdata + render test** - `4df0e863` (feat)
2. **Task 2: km-github helper — comment + review verbs** - `8658e2fc` (feat)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go` - Added GitHubInboundEnabled template param, github-inbound poller heredoc, systemd unit, S3 binary download, systemctl wiring, githubInboundEnabled helper, notifyEnv slot
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata_github_inbound_test.go` - 12 tests covering render (enabled/disabled), preamble fields, dispatch, queue drain, SSM fallback, systemd unit content, systemctl enable line, env var, Slack isolation
- `/Users/khundeck/working/klankrmkr/cmd/km-github/main.go` - dispatch table, runComment/runCommentWith, runReview/runReviewWith, addGitHubHeaders, loadToken (SSM)
- `/Users/khundeck/working/klankrmkr/cmd/km-github/main_test.go` - 12 tests covering dispatch, comment request shape/headers/body, review event validation, commit_id optional/present, missing args

## Decisions Made

- Separate binary `cmd/km-github` (not embedded in km) mirrors `cmd/km-slack` dispatch pattern; deployed via conditional S3 artifact download in userdata only when GitHubInboundEnabled
- GitHub context preamble includes worktree-per-PR guidance to support concurrent PR review in long-lived sandboxes without branch collision
- APPROVE does not require body; COMMENT and REQUEST_CHANGES require non-empty body (GitHub API constraint)
- `commit_id` is optional via `omitempty` — omitted from JSON when empty to avoid null field in payload
- Byte-identity preserved for existing profiles (learn.v2, km-prefix baselines): careful whitespace trimming in Go template `{{- if }}{{- end }}` blocks prevents extra blank lines when GitHubInboundEnabled=false

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed extra blank line in systemd unit section breaking byte-identity**
- **Found during:** Task 1 (github-inbound poller in userdata)
- **Issue:** Initial `{{- if .GitHubInboundEnabled }}...{{- end }}\n\ncat > /etc/systemd/system/km-presence.service` introduced an extra blank line before the presence service unit when `GitHubInboundEnabled=false` + `SlackInboundEnabled=true`, breaking `TestUserdataLearnV2Phase92ByteIdentity`
- **Fix:** Removed the blank line between `{{- end }}` (GitHub unit) and `cat > ...` (presence unit), matching the Slack inbound pattern exactly
- **Files modified:** pkg/compiler/userdata.go
- **Verification:** `go test ./pkg/compiler/ -run TestUserdataLearnV2Phase92ByteIdentity|TestUserdataKmPrefixByteIdentity` passes
- **Committed in:** 4df0e863 (Task 1 commit)

**2. [Rule 1 - Bug] Fixed test assertion for envelope fields (jq .field syntax not "field")**
- **Found during:** Task 1 (TDD RED→GREEN)
- **Issue:** Test `TestUserdata_GitHubInboundPoller_Preamble` initially checked for `"repo"` (JSON key with quotes) but the poller uses jq `.repo` (no quotes around field name)
- **Fix:** Updated test to check for `.repo`, `.number`, `.branch`, `.head_sha`, `.body`, `.sender`
- **Files modified:** pkg/compiler/userdata_github_inbound_test.go
- **Verification:** Test passes
- **Committed in:** 4df0e863 (Task 1 commit)

**3. [Rule 1 - Bug] Fixed Slack isolation test to use heredoc marker not substring**
- **Found during:** Task 1 (TDD RED→GREEN)
- **Issue:** `TestUserdata_SlackPollerUnaffectedByGitHubInbound` checked `strings.Contains(out, "km-slack-inbound-poller")` but the github-inbound template comment mentioned "Slack inbound pollers" in the comment text
- **Fix:** Updated test to check for `<< 'SLACKINBOUND'` and `<< 'SLACKINBOUNDUNIT'` heredoc markers (definitive absence of the poller heredoc); also updated the template comment to not reference the slack poller name
- **Files modified:** pkg/compiler/userdata_github_inbound_test.go, pkg/compiler/userdata.go
- **Verification:** Test passes
- **Committed in:** 4df0e863 (Task 1 commit)

---

**Total deviations:** 3 auto-fixed (all Rule 1 — bugs found during TDD cycle)
**Impact on plan:** All three fixes were discovered during TDD iteration and resolved inline. No scope creep; all within Task 1 scope.

## Issues Encountered

None beyond the deviation items above.

## Next Phase Readiness

- Plan 05 closes the sandbox-side loop: bridge (plan 04) enqueues → poller (plan 05) dequeues + dispatches
- km-github binary is ready for agents to call: `km-github comment --repo owner/repo --number N --body "..."` and `km-github review --event APPROVE|COMMENT|REQUEST_CHANGES`
- Wave 2 is complete (plans 04 + 05); Wave 3 (plan 06: integration test + make build wiring) is next

---
*Phase: 97-github-comment-trigger-mvp*
*Completed: 2026-06-06*

## Self-Check: PASSED

All verified:
- `pkg/compiler/userdata_github_inbound_test.go` — FOUND
- `cmd/km-github/main.go` — FOUND
- `cmd/km-github/main_test.go` — FOUND
- `97-05-SUMMARY.md` — FOUND
- Commit `4df0e863` (Task 1) — FOUND
- Commit `8658e2fc` (Task 2) — FOUND
