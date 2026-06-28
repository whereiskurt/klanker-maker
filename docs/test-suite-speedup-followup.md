# Test-suite speedup — follow-up work

**Status:** the easy, high-confidence wins are merged to `main` (PRs #40, #42).
This doc is the remaining, larger effort. Written 2026-06-28.

## Where we are

`go test ./...` wall-clock is **gated by `internal/app/cmd`** (~405s; everything
else runs in parallel alongside it in <20s). Two coverage-preserving fixes
already landed:

- **IMDS-disable** (`AWS_EC2_METADATA_DISABLED` + dummy creds in `TestMain` /
  per-package `func init()`): killed ~30s metadata timeouts in tests that build
  real AWS clients. `internal/app/cmd/main_test.go` + 13 `*/imds_disable_test.go`.
  Biggest single win: `TestEmailSend_MissingFrom` 33s → 1.3s.
- **Injectable `sleep` seam** (`internal/app/cmd/clock.go`, `var sleep =
  time.Sleep`, no-op'd in `TestMain`): zeroed the 13 fixed `time.Sleep` waits
  (port-forward bind, SSM retry, boot grace).

Net: 487s → ~405s for the package. The rest is **genuine work + structural**,
not waits — so it needs real seam-work, not another env var.

## The residual, categorized (target in this order)

### 1. Deliberate timeout-behavior tests — make the duration injectable (~20s, easy)
These wait a REAL context deadline to assert a timeout fires:
- `TestRunBootstrapSharedSES_HonorsBootstrapTimeout` (5.2s)
- `TestDefaultApplyTerragrunt_HonorsContextTimeout` (5.2s)
- `TestBootstrapAll_PlanRespectsGate` (8.6s)

Fix: the timeout durations they exercise are hardcoded `context.WithTimeout(...,
N*time.Second)` in `init.go` / `bootstrap*.go`. Promote each to a package var
(e.g. `var bootstrapTimeout = 30 * time.Second`) and have these tests set it to
a few milliseconds. The assertion (that the deadline fires) still holds; the wait
drops to ~0.

### 2. Port-forward / SSM select-loops — extend the clock seam (~40s, medium)
`shell.go:813 time.After(2s)`, `:823 time.After(20s)`, `:829 NewTicker(20s)` and
the agent/desktop equivalents are select-loop liveness waits the `sleep` seam did
NOT cover. Tests hitting them:
- `TestShellCmd_EC2_Root` (6.8s), `TestShellCmd_NoBedrock_CredentialsPresent` (4.6s)
- `TestDesktopRekey` (6s), `TestDesktopRestart` (5s), `TestDesktopStatus` (3s)
- `TestAgentInteractive` (5.3s), `TestAgentNonInteractive_IdleReset` (7.8s),
  `TestAgentListQueue`, `TestAgentResults`, `TestAgentRun_*` (3s each)

Fix: add seams alongside `sleep` for the wait durations — e.g.
`var portForwardGrace = 20 * time.Second` and `var livenessTick = 20 *
time.Second` — set small in `TestMain`. Keep `time.After`/`NewTicker` (they're
selects, not sleeps); just make their durations vars.

### 3. Real terragrunt subprocesses — use the existing runner seams (~20s, medium)
`TestInitAllModulesOrder` (4.1s) and the bootstrap/init plan tests shell out to a
real `terragrunt`. Seams already exist — `InitRunner` (`init.go:52`),
`SlackTerragruntRunner` (`slack.go:70`), `ClusterRunner` (`cluster.go:44`). The
slow tests don't all use the stub. Route them through a fake runner that returns
canned plan/output instead of exec'ing terragrunt.

### 4. Destroy / Configure-wizard / Validate (~60s, investigate each)
`TestDestroyCmd_*` (3-8s), `TestConfigureWizard*` (3.5s), `TestValidateValidProfile`
(5.2s), `TestResolveSandboxID_CustomPrefix` (4.2s), `TestInfoShowsNewAccountFields`
(6.5s). Mixed causes — some may still make a real AWS call that now fails-fast
(check), some compile every built-in profile (real work), some prompt/IO. Profile
each with `go test -run <name> -v` and decide per-test (stub the AWS/IO seam, or
share a compiled-profile fixture across tests).

### 5. The structural lever (the real ceiling)
Even with 1-4 done, ~255s is a long tail of ~1,200 tests at ~0.2s each (cobra
command construction + profile compile) running **serially in one package**.
`go test` parallelises across packages but not within one, and almost no cmd test
uses `t.Parallel()`. Two ways to cut this:
- **Split `internal/app/cmd`** into sub-packages (`cmd/agent`, `cmd/bootstrap`,
  `cmd/slack`, `cmd/destroy`, …) so `go test ./...` runs them as parallel test
  binaries. Wall-clock drops toward the slowest sub-package. Big mechanical
  refactor; highest ceiling.
- **`t.Parallel()` audit** — add it to independent tests. Blocked today by shared
  global state (cobra root, `os` env via `t.Setenv` which is incompatible with
  `t.Parallel`, working-dir). Needs an audit + isolation pass first.

## How to verify (each step)
1. `go test ./internal/app/cmd/ -count=1 -v 2>&1 | grep -E '^--- ' | <sort by time>`
   — capture the per-test baseline (the run takes ~7 min; do it once).
2. After each category, re-run the affected tests + the whole package; confirm
   **green (0 FAIL)** and measure the drop. The suite must stay green — coverage
   is non-negotiable.
3. Watch for tests that DEPEND on a real wait/timeout/subprocess for correctness
   (rare here, but the timeout-behavior tests in #1 are exactly that — keep the
   assertion, only shrink the duration).

## Suggested approach
Do it subagent-driven, one category per task (1→2→3→4), each its own commit +
spec-vs-quality review, then decide on #5 (split vs t.Parallel) as a separate,
bigger phase. Categories 1-3 are ~80s of clear, low-risk wins; 4 is
investigate-then-fix; 5 is the structural project.
