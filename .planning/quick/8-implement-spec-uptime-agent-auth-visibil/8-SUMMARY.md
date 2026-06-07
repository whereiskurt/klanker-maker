---
phase: quick-8
plan: 8
subsystem: cli-ux
tags: [km-list, km-status, uptime, agent-auth, ssm, di, tdd]
dependency_graph:
  requires: [agent_auth.go SSMSendAPI, sendSSMAndWait, extractResourceID, fprintBanner]
  provides: [formatUptime, AgentAuthChecker, km-list-banner, km-list-UP-column, km-list-auth, km-status-uptime, km-status-auth]
  affects: [internal/app/cmd/list.go, internal/app/cmd/status.go]
tech_stack:
  added: []
  patterns: [DI-interface-widening, TDD-red-green, concurrent-semaphore-pool, soft-fail-SSM]
key_files:
  created:
    - internal/app/cmd/agent_auth_check.go
    - internal/app/cmd/uptime_test.go
  modified:
    - internal/app/cmd/list.go
    - internal/app/cmd/list_test.go
    - internal/app/cmd/status.go
    - internal/app/cmd/status_test.go
decisions:
  - "Export FormatUptime as public wrapper so cmd_test package can unit-test it directly"
  - "Use single SSM command for both claude+codex auth checks to minimise round-trips"
  - "Banner emitted before empty-list message so operators always see km version + timestamp"
  - "awsCfg zero-value Region check used to distinguish injected-lister (test) vs real path for EC2 enrichment"
  - "Pre-existing TestListCmd_EmptyStateBucketError + TestStatusCmd_EmptyStateBucketError test failures deferred (not caused by this task)"
metrics:
  duration: ~30 minutes
  completed: 2026-06-07
  tasks_completed: 3
  tasks_total: 3
  files_created: 2
  files_modified: 4
---

# Quick Task 8: km status / km list — uptime + agent-auth visibility

**One-liner:** Compact uptime (formatUptime), per-sandbox auth state (claude/codex SSM), and version banner surfaced in km list and km status via DI-injected AgentAuthChecker, zero AWS calls without --auth.

## What Was Built

### Task 1: formatUptime + AgentAuthChecker (agent_auth_check.go)

New file `internal/app/cmd/agent_auth_check.go`:

- `formatUptime(time.Time) string` — compact duration bands: `8m` / `3h12m` / `2d4h`. Negative/zero guards to `"0m"`. Exported as `FormatUptime` for cmd_test package access.
- `AgentAuthChecker` interface — `CheckAuth(ctx, *SandboxRecord) (bool, bool, error)` — the DI seam so tests stub auth with no AWS.
- `ssmAgentAuthChecker` — real implementation; resolves EC2 instance ID from `rec.Resources`, delegates to `checkAgentAuth`.
- `checkAgentAuth` — single SSM round-trip running `sudo -u sandbox bash -lc 'claude auth status 2>/dev/null'` + `test -f /home/sandbox/.codex/auth.json` in one command. Parses `"loggedIn": true` (spacing-tolerant) for claude; `KM_CODEX_OK` sentinel for codex.

Unit-tested in `uptime_test.go` with 12 cases covering all three display bands plus zero and negative-duration guards.

### Task 2: km status — Uptime + Auth (status.go)

- New `NewStatusCmdWithChecker` constructor (widest overload); `NewStatusCmdWithAllFetchers` delegates to it with `nil` checker — all existing call sites unchanged.
- `printSandboxStatus` signature extended with `checker AgentAuthChecker`.
- For `rec.Status == "running"`: emits `Uptime:      <formatUptime(rec.CreatedAt)>` after the `Created At:` line, then calls `checker.CheckAuth` and prints the `Auth:` block.
- Soft-fail: SSM error → `Auth:        <unavailable: ...>`, exits 0.
- Non-running sandboxes: no Uptime line, no Auth block, zero SSM calls.

4 new status tests (Uptime, Auth, error-soft-fail, non-running-no-uptime-no-auth).

### Task 3: km list — banner + UP column + --auth (list.go)

- New `NewListCmdWithCheckers` constructor (widest overload); `NewListCmdWithLister` delegates to it — all existing call sites unchanged.
- Banner: `fprintBanner(cmd.OutOrStdout(), "km list", "<N> sandbox(es)")` emitted at top of all non-JSON output, including the empty-list path. Suppressed in `--json` mode.
- UP column added to both narrow and wide layouts; running rows → `formatUptime(r.CreatedAt)`, others → `-`.
- `--auth` flag: concurrent goroutine pool (semaphore capacity 8, WaitGroup) fans out `checker.CheckAuth` only over `Status == "running"` rows. Results collected in a `map[string]string` keyed by SandboxID. Renders `cl✓ cx✗` (or `?` on error) in the AUTH column.
- `--wide` alone does NOT enable `--auth` — they are independent flags.
- Without `--auth`: zero checker calls, no AUTH column.

7 new list tests (banner-present, banner-suppressed-JSON, banner-on-empty, UP-column, auth-fan-out-concurrent, no-auth-zero-calls, wide-alone-no-auth).

## Deviations from Plan

### Pre-existing failures (Rule 3 scope boundary — NOT introduced by this task)

Two tests were already failing on the branch before any Task 8 changes:

- `TestListCmd_EmptyStateBucketError` — expected `err != nil` for empty StateBucket + nil lister; test environment resolves real AWS config and returns empty list instead of an error.
- `TestStatusCmd_EmptyStateBucketError` — same pattern; status path returns "sandbox not found" rather than a bucket config error.

Verified by running tests after `git stash` against pre-task commit `2392e7f4`. Documented in `deferred-items.md`. Not fixed (pre-existing, out of scope per deviation rules).

### Auto-fixed during execution

**[Rule 3 - Blocking] EC2 enrichment guard for injected-lister path**
- Found during: Task 3
- Issue: `awsCfg` zero-value when lister injected (test path) would cause a second `LoadAWSConfig` call that always fails in tests, potentially skipping EC2 enrichment in an undetectable way.
- Fix: Added `awsCfg.Region == ""` check to conditionally attempt enrichment load only when the lister was injected.
- Files: `internal/app/cmd/list.go`

## Commits

| Task | Commit | Message |
|------|--------|---------|
| 1 | `2392e7f4` | feat(quick-8): add formatUptime helper + AgentAuthChecker interface + checkAgentAuth |
| 2 | `1885fc62` | feat(quick-8): km status — Uptime: line + Auth: section for running sandboxes |
| 3 | `1aa7bbaa` | feat(quick-8): km list — banner + UP column + --auth flag with concurrent fan-out + AUTH column |

## Verification

- `make build` passed — `km v0.4.857 (1aa7bbaa)`.
- `go test ./internal/app/cmd/ -run 'TestFormatUptime|TestStatus|TestList'` — all 35 new + regression tests pass; only 2 pre-existing failures remain (`EmptyStateBucketError` for both commands).
- Binary smoke-check: `./km list` produces banner + UP column; `./km list --json | head` is valid JSON with no banner.

## Self-Check: PASSED

Files verified:
- `internal/app/cmd/agent_auth_check.go` — exists (created)
- `internal/app/cmd/uptime_test.go` — exists (created)
- `internal/app/cmd/list.go` — modified (NewListCmdWithCheckers, banner, UP, AUTH)
- `internal/app/cmd/status.go` — modified (NewStatusCmdWithChecker, Uptime, Auth)
- `internal/app/cmd/list_test.go` — modified (7 new tests)
- `internal/app/cmd/status_test.go` — modified (4 new tests)

Commits verified:
- `2392e7f4` — present in git log
- `1885fc62` — present in git log
- `1aa7bbaa` — present in git log
