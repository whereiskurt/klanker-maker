---
phase: 86-km-create-prompt-queue
plan: "03"
subsystem: compiler/userdata
tags: [bash, systemd, queue-runner, tdd, green-state, pq-08]
dependency_graph:
  requires:
    - 86-01 (RED-state stubs, bash harness skeleton)
  provides:
    - km-queue-runner bash script seeded unconditionally on every EC2 sandbox
    - km-queue.service systemd unit with Restart=on-failure
    - ReconcileMetaStatus Go helper (operator-side mirror of reconcile logic)
    - GREEN bash harness (7 PASS)
    - GREEN TestQueueRunnerStateMachine Go test
  affects:
    - 86-02 (kickQueueRunner no longer emits "unit not found" WARN after this lands)
    - 86-04 (seed wire validation against real sandbox)
    - 86-05 (--wait polling + km agent list --queue)
    - 86-06 (full UAT)
tech_stack:
  added: []
  patterns:
    - Inline heredoc in Go raw string template (avoids backtick collision — use double-quotes in bash comments)
    - Env-var-overridable defaults in bash script (QUEUE_DIR, RUNS_DIR, LOG_FILE, SANDBOX_HOME, PROBE_INTERVAL)
    - PATH-shim testing pattern for portable bash unit tests
    - km_timeout() wrapper for macOS (no system `timeout` binary)
    - Exported Go wrapper (ReconcileMetaStatus) to expose internal helper to cmd_test
key_files:
  created: []
  modified:
    - pkg/compiler/userdata.go (+222 lines: km-queue-runner heredoc + km-queue.service heredoc + enables)
    - pkg/profile/configfiles/km-queue-runner_test.sh (7 SKIP stubs -> 7 PASS tests, +~350 lines)
    - internal/app/cmd/create_prompt.go (+12 lines: ReconcileMetaStatus)
    - internal/app/cmd/create_prompt_test.go (TestQueueRunnerStateMachine: SKIP -> PASS)
decisions:
  - "Restart=on-failure (not Restart=always): runner exits 0 on empty queue; always would busy-loop"
  - "journalctl -u km-queue -f for runner loop observability (not tmux attach -t km-queue): cleaner alignment with other km systemd services; per-entry sessions still named km-agent-<runID>"
  - "Unconditional seeding: both the runner and unit land on every EC2 sandbox — no profile flag needed; R1 regression is unit-installed-but-idle, not unit-absent"
  - "PROBE_INTERVAL/PROBE_LOG_INTERVAL made env-var-overridable for test isolation (W3-locked path per plan)"
  - "ReconcileMetaStatus exported (PascalCase) so cmd_test package can call it without import tricks"
  - "km_timeout() portable timeout wrapper in test harness (macOS has no system timeout binary)"
  - "EOFSCRIPT inner heredoc is unquoted (variables expand at outer-script runtime) — correct: $b64prompt etc. are local vars in run_entry"
metrics:
  duration: "724 seconds (~12 min)"
  completed: "2026-05-20T03:11:00Z"
  tasks: 2
  files_created: 0
  files_modified: 4
---

# Phase 86 Plan 03: On-Box Runner + Systemd Unit (Wave 1) Summary

Wave 1 lands the on-box artifacts that consume the queue files Plan 86-02 pushes: a ~170-line bash runner at `/opt/km/bin/km-queue-runner` and a systemd unit `km-queue.service` that supervises it. Both seeded via inline heredoc blocks in `pkg/compiler/userdata.go`. PQ-08 satisfied.

## One-liner

Inline userdata.go heredocs seed km-queue-runner (reconcile+auth probe+linear-chain failure) and km-queue.service (Restart=on-failure) unconditionally on every EC2 sandbox; 7 bash tests + 1 Go test GREEN.

## What Was Done

### Task 1: userdata.go inline heredoc blocks

`pkg/compiler/userdata.go` gained 222 lines immediately after the `km-presence.service` block (~line 1889), before the `{{- if .VSCodeEnabled }}` block. Two new unconditional blocks:

**Block A — `/opt/km/bin/km-queue-runner`** (~170 lines bash):
- `set -u` with env-var-overridable defaults: `QUEUE_DIR`, `RUNS_DIR`, `LOG_FILE`, `SANDBOX_HOME`, `PROBE_INTERVAL`, `PROBE_LOG_INTERVAL`
- Reconcile loop on startup: any `running` entry reset to `pending` (atomically via `jq + mv`)
- Auth probes: `probe_bedrock` (aws invoke-model) / `probe_direct_api` (jq -e on .credentials.json)
- `wait_for_auth` loops indefinitely at `$PROBE_INTERVAL` seconds; logs every `$PROBE_LOG_INTERVAL` seconds
- `pick_next` sorts meta.json files and returns lowest pending
- `run_entry` writes a per-entry inner script, `sudo -u sandbox tmux new-session`, waits for completion, reads exit code
- `skip_remaining` marks all remaining pending as skipped on failure
- Main loop: pick → auth probe → run → on failure: skip remaining + exit 1

**Block B — `/etc/systemd/system/km-queue.service`**:
- `Restart=on-failure` (not `Restart=always` — avoids busy-loop when runner exits 0 on empty queue)
- `User=root` with `sudo -u sandbox` for claude invocations (RESEARCH.md pitfall #3)
- `Environment=SANDBOX_ID={{ .SandboxID }}` and `Environment=KM_ARTIFACTS_BUCKET={{ .KMArtifactsBucket }}`
- `systemctl enable km-queue` fires unconditionally

**Go template constraint:** The userdata.go raw string literal (`` const userDataTemplate = ` `` ... `` ` ``) cannot contain backtick characters. Bash comments that used backtick-quoted words were changed to double-quoted equivalents.

### Task 2: bash harness + Go test GREEN

`pkg/profile/configfiles/km-queue-runner_test.sh` replaced 7 `echo "SKIP"` stubs with real test bodies:

| Test | What it exercises |
|------|-------------------|
| `test_reconcile_running_to_pending` | meta.json starting as "running" ends as "done" (reconcile → pending → picked → claude → done) |
| `test_lowest_pending_picked_first` | 001 + 002 both pending → both end as "done" in correct order |
| `test_failure_marks_remaining_skipped` | 001 fails (claude exits 1) → 001=failed, 002=skipped, 003=skipped, runner exits 1 |
| `test_auth_probe_bedrock_success_proceeds` | aws shim exits 0 → entry runs and finishes as done |
| `test_auth_probe_bedrock_failure_loops` | aws shim fails 2 times then succeeds → call_count >= 3, entry done |
| `test_auth_probe_direct_api_creds_present` | .credentials.json exists + valid JSON → probe passes → entry done |
| `test_auth_probe_direct_api_creds_missing` | no .credentials.json → runner loops on probe → entry stays pending |

Test infrastructure:
- `extract_runner()` uses `awk` + `sed` to extract runner from userdata.go heredoc into `/tmp/km-queue-runner-test-$$.sh`
- PATH shims for `aws`, `claude`, `tmux`, `sudo` in a `mktemp -d` shimdir
- `km_timeout()` portable wrapper (macOS has no system `timeout`; uses background job + sleep + kill)
- `sudo` shim strips `-u <user>` and runs remaining args (test runs as current user)
- `tmux new-session` shim runs the script directly (no actual tmux needed)

`internal/app/cmd/create_prompt.go` gained `ReconcileMetaStatus(status string) string` — the Go-side mirror of the bash reconcile logic. Exported (PascalCase) so `cmd_test` package can call it.

`internal/app/cmd/create_prompt_test.go` `TestQueueRunnerStateMachine` flipped from SKIP to 5-subtest PASS (running→pending, pending→pending, done→done, failed→failed, skipped→skipped).

## Design Decisions Explained

### Why `Restart=on-failure` (not `Restart=always`)

`Restart=always` restarts the service regardless of exit code. When the runner finishes draining the queue, it exits 0 ("no pending entries"). With `Restart=always`, systemd would immediately restart it, the runner would find no pending entries and exit 0 again, creating a rapid restart loop consuming CPU (RESEARCH.md pitfall #6). `Restart=on-failure` only restarts on non-zero exit, which happens on crash. On reboot/resume, systemd starts the unit fresh via `WantedBy=multi-user.target` — no restart-trigger needed.

### Why `journalctl -u km-queue -f` not `tmux attach -t km-queue`

The CONTEXT.md / BRIEF.md originally described wrapping `ExecStart` in a `tmux new-session -d -s km-queue ...` so operators could `tmux attach -t km-queue` to watch the runner loop. The cleaner pattern (chosen here) is: systemd manages the runner process directly; per-entry claude invocations get individual tmux sessions named `km-agent-<runID>` (matching the existing `km agent run` convention). Operator visibility:
- Runner loop: `journalctl -u km-queue -f` (standard systemd pattern, same as km-mail-poller / km-presence)
- Individual runs: `km agent attach <sandbox>` (same as always — `km-agent-*` tmux sessions)
- Log file: `/workspace/.km-agent/km-queue.log` (probe status, entry lifecycle)

This is strictly simpler and aligns with every other km systemd service.

### Why unconditional seeding

CONTEXT.md revision 2026-05-19 locked: every EC2 sandbox gets the runner + unit, unconditionally. R1 regression check becomes "unit installed+enabled+idle when no --prompt used" (not "unit absent"). Operators who never use --prompt see no behavioral change — the runner exits 0 immediately when it finds no pending entries.

### Linear-chain failure: runner exits 1

When the first prompt fails (claude exits non-zero), the runner marks that entry `failed`, marks all remaining `pending` entries `skipped`, and exits 1. Systemd sees non-zero exit code — if there were future prompts added while it was running, `Restart=on-failure` would restart the runner (which would then reconcile and process them). In practice, all prompts are pushed atomically by `pushQueueFiles` before `kickQueueRunner` starts the service, so the chain is static.

## Verification Results

```
make build                            → Built km v0.2.699 (219b047) — exit 0
go vet ./pkg/compiler/...             → exit 0
go test ./pkg/compiler/... -count=1   → 6 pre-existing FAIL (unchanged); 0 new regressions
go test ./internal/app/cmd/ -run TestQueueRunnerStateMachine -v
                                      → PASS (5/5 subtests)
bash pkg/profile/configfiles/km-queue-runner_test.sh
                                      → Results: 0 SKIP, 7 PASS, 0 FAIL — exit 0
grep -c 'km-queue' pkg/compiler/userdata.go → 19 (> 6 required)
Restart=on-failure in km-queue.service → confirmed
systemctl enable km-queue line        → confirmed
```

Pre-existing compiler test failures (6): TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock, TestUserDataNotifyEnv_*, TestUserDataKMTracingServicectlStart, TestAuditHookNonBlocking, TestGitHubUserDataGITASKPASS — confirmed present before Phase 86 changes via `git stash` check.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Backtick characters in bash comments inside Go raw string literal**
- **Found during:** Task 1 — `make build` failed with "syntax error: unexpected name running after top level declaration"
- **Issue:** The `const userDataTemplate = \`` raw string in userdata.go cannot contain backtick characters. The bash comments originally used `` `running` `` and `` `pending` `` formatting.
- **Fix:** Changed bash comments from backtick-quoted to double-quoted: `` `running` `` → `"running"`, `` `pending` `` → `"pending"`, etc.
- **Files modified:** `pkg/compiler/userdata.go`
- **Commit:** `633af89`

**2. [Rule 1 - Bug] PROBE_INTERVAL hardcoded (not env-var-overridable) broke test isolation**
- **Found during:** Task 2 — first test run: auth probe slept 5 seconds per iteration; tests failed due to timing
- **Issue:** `PROBE_INTERVAL=5` was hardcoded in the runner body; the test's `PROBE_INTERVAL=0` env var had no effect.
- **Fix:** Changed to `PROBE_INTERVAL="${PROBE_INTERVAL:-5}"` and `PROBE_LOG_INTERVAL="${PROBE_LOG_INTERVAL:-300}"` (W3-locked pattern: other vars already used `${VAR:-default}` defaults).
- **Files modified:** `pkg/compiler/userdata.go` (runner heredoc)
- **Commit:** `aa0410e`

**3. [Rule 3 - Blocking] macOS has no system `timeout` binary**
- **Found during:** Task 2 — `timeout 10 bash ...` in test harness returned exit 127 (command not found)
- **Issue:** GNU `timeout` is not installed on macOS (homebrew coreutils would provide it as `gtimeout`, but that wasn't available either).
- **Fix:** Added `km_timeout()` bash function using background job + sleep + kill pattern. All `timeout N bash` calls replaced with `km_timeout N bash`.
- **Files modified:** `pkg/profile/configfiles/km-queue-runner_test.sh`
- **Commit:** `aa0410e`

## Commits

| Hash | Message |
|------|---------|
| 633af89 | feat(86-03): add km-queue-runner bash script + km-queue.service to userdata.go |
| aa0410e | feat(86-03): flip bash harness + Go test GREEN; add ReconcileMetaStatus |

## Setup for Next Plans

- **Plan 86-04** (seed wire validation): can now provision a real EC2 sandbox and verify `km-queue.service` is installed, enabled, active-but-idle; verify reconcile on `km pause + km resume` cycle.
- **Plan 86-05** (`--wait` polling + `km agent list --queue`): `waitForQueueDrain` already stubbed in `create_prompt_test.go` (PQ-05/PQ-06 SKIP markers); the runner's meta.json status files are the polling target.
- **Plan 86-06** (full UAT): `kickQueueRunner` in Plan 86-02 now finds the unit without "unit not found" WARN.

## Self-Check: PASSED

Files verified:
- `pkg/compiler/userdata.go` — exists, contains km-queue-runner, Restart=on-failure, systemctl enable km-queue
- `pkg/profile/configfiles/km-queue-runner_test.sh` — exists, 7 PASS tests
- `internal/app/cmd/create_prompt.go` — exists, contains ReconcileMetaStatus
- `internal/app/cmd/create_prompt_test.go` — exists, TestQueueRunnerStateMachine PASS

Commits:
- `633af89` — verified in git log
- `aa0410e` — verified in git log
