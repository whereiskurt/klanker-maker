---
phase: 98-github-bridge-expansion
plan: "01"
subsystem: sandbox-github-client
tags: [km-github, check-runs, pr-create, push-hardening, worktree, preamble]
dependency_graph:
  requires: [98-00]
  provides: [check-verb, pr-create-verb, worktree-preamble]
  affects: [cmd/km-github/main.go, pkg/compiler/userdata.go]
tech_stack:
  added: []
  patterns: [run<Verb>+run<Verb>With dispatch pattern, checkRunPayload, prCreatePayload]
key_files:
  created: []
  modified:
    - cmd/km-github/main.go
    - cmd/km-github/check_test.go
    - cmd/km-github/prcreate_test.go
    - pkg/compiler/userdata.go
    - pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh
decisions:
  - "checkRunOutput.Title uses the check name as title (simple, agent-friendly default)"
  - "runPRCreateWith receives stdout io.Writer for testability — html_url printed to stdout"
  - "Golden re-captured rather than patching the byte-identity test: preamble change is intentional/permanent"
metrics:
  duration: 238s
  completed: "2026-06-07"
  tasks_completed: 2
  files_modified: 5
---

# Phase 98 Plan 01: km-github check + pr create verbs + worktree preamble Summary

**One-liner:** km-github check (CI check run) + pr create (open PR, print html_url) verbs added using the existing run<Verb>With dispatch pattern; github-inbound poller preamble updated to instruct worktree-per-PR isolation and advertise full verb set.

## Tasks Completed

| Task | Name | Commit | Key Files |
|------|------|--------|-----------|
| 1 | Implement km-github check + pr create verbs (GREEN 98-00 tests) | 7baaaac7 | cmd/km-github/main.go, check_test.go, prcreate_test.go |
| 2 | Harden push path — worktree-per-PR + verb-list preamble (GH-X-PUSH) | c5a160c4 | pkg/compiler/userdata.go, userdata_learn_v2_pre92_baseline.golden.sh |

## What Was Built

### Task 1: km-github check + pr create verbs

Added two new verb pairs to `cmd/km-github/main.go`:

**check verb** (`runCheck` + `runCheckWith`):
- Dispatches to `POST /repos/{owner}/{repo}/check-runs`
- Validates conclusion against `{success, failure, neutral}` — rejects early, no HTTP call
- Requires non-empty `--head-sha` (GitHub API returns 422 without it)
- Payload: `{name, head_sha, status:"completed", conclusion, output:{title, summary}}`
- Exit 0 on 201, non-zero on bad conclusion / missing head_sha / non-2xx

**pr create verb** (`runPRCreate` + `runPRCreateWith`):
- Dispatches to `POST /repos/{owner}/{repo}/pulls`
- Requires `--repo`, `--title`, `--head`, `--base`; body is optional
- Prints `html_url` to stdout on 201 (agent reads it to know the new PR URL)
- Signature: `runPRCreateWith(repo, title, head, base, body, token string, stderr, stdout io.Writer) int`

Removed `//go:build phase98_wave0` from `check_test.go` and `prcreate_test.go`.

All 8 new tests pass (TestCheck, TestCheck_AllValidConclusions, TestCheck_BadConclusion, TestCheck_MissingHeadSHA, TestCheck_MissingRequired, TestPRCreate, TestPRCreate_EmptyBody, TestPRCreate_MissingRequired). Full `cmd/km-github` suite green.

### Task 2: Worktree-per-PR + full verb-list preamble

Updated the `PREAMBLE` string in the github-inbound poller (`pkg/compiler/userdata.go` lines ~2176-2197) to:

1. **Worktree isolation section** — explicitly tells agent to ALWAYS use a dedicated git worktree:
   ```
   git fetch origin pull/{number}/head
   git worktree add /workspace/pr-{number} FETCH_HEAD
   ```
   Explicitly warns: never switch branches in `/workspace` (would break other concurrent PRs).

2. **Full verb list** — expanded from just `comment` + `review` to all five:
   - `km-github comment` — plain PR/issue comment
   - `km-github review` — formal PR review (APPROVE/COMMENT/REQUEST_CHANGES)
   - `km-github check --name … --conclusion success|failure|neutral --summary … --head-sha {head_sha}`
   - `km-github pr create --title … --base … --head … [--body …]` (prints html_url)
   - `git push origin HEAD:<branch-name>` (credential helper pre-configured)

Updated `userdata_learn_v2_pre92_baseline.golden.sh` via `CAPTURE_PRE92_BASELINE=1 go test` to reflect the intentional preamble expansion. The `learn.v2.yaml` profile has `github.inbound.enabled: true`, so the golden includes the poller preamble.

## Verification

- `go test ./cmd/km-github/... -count=1` — PASS (all tests including 8 new check/pr-create tests)
- `go test ./pkg/compiler/... -count=1` — PASS (byte-identity tests pass after golden re-capture)
- `go build ./...` — PASS (clean build, no compilation errors)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Byte-identity golden update required after preamble expansion**
- **Found during:** Task 2
- **Issue:** `TestUserdataKmPrefixByteIdentity` and `TestUserdataLearnV2Phase92ByteIdentity` compare userdata output against a golden file. The `learn.v2.yaml` profile has `github.inbound.enabled: true`, so the preamble is part of the golden. The Phase 92 byte-identity tests don't carve out the GitHub preamble, so the "outside settings.json" comparison failed.
- **Fix:** Re-ran `CAPTURE_PRE92_BASELINE=1 go test ./pkg/compiler/ -run TestCapturePre92Userdata` to re-capture the golden. The preamble change is intentional and permanent — re-capture is the correct approach (the plan explicitly says "If there is an existing compiler test that snapshots/asserts the github preamble, update its expectation").
- **Files modified:** `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh`
- **Commit:** c5a160c4

## Self-Check: PASSED

- cmd/km-github/main.go — FOUND
- pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh — FOUND
- Commit 7baaaac7 (Task 1) — FOUND
- Commit c5a160c4 (Task 2) — FOUND
