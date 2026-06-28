# Test-suite speedup — follow-up work

**Status:** categories 1–4 **DONE** (PR #43, 2026-06-28). `internal/app/cmd` went
**460.9s → ~33s (~14×)**; the whole-repo `go test ./...` wall-clock is now gated
by `internal/app/cmd` at ~33s (was ~405–460s). Category 5 (the structural split)
was evaluated and **deferred** — see the bottom. The earlier IMDS + sleep wins
landed on `main` first (PRs #40, #42).

> **What the original plan got wrong (corrected here):** measurement after the
> fact showed the dominant cost was NOT the shell/agent waits this doc first
> named. It was (a) `RunInitPlanWithRunner` **re-downloading the terraform binary
> over the network** on every init-plan test (~130s — the thing that wedged
> slow-network runs), and (b) `ResolveSandboxID` **loading a named SSO profile +
> hitting real DynamoDB** on every cobra command. Both are now fixed. The
> per-category sections below record what was actually done.

## Where we ended up

`go test ./internal/app/cmd/` per-test sum ≈ wall-clock (the package runs serial
in one binary). Baseline 460.9s → ~33s. Remaining ~33s is the long tail of ~1,200
tests at ~0.025s each (cobra construction + profile compile) — the cat-5 ceiling.

The only `go test ./...` failures are 5–8 **pre-existing environmental** tests
(configure/cluster/bootstrap making real KMS/STS/S3/DDB calls) that fail on an
expired/transient AWS SSO token (`InvalidGrantException`). They flake by token
validity and are NOT introduced by this work; making them hermetic is the natural
follow-up if CI-green-without-SSO is wanted.

## What was done (categories 1–4)

### 1. Deliberate timeout-behavior tests — DONE (`5f788a93`)
`TestDefaultApplyTerragrunt_HonorsContextTimeout`,
`TestRunBootstrapSharedSES_HonorsBootstrapTimeout`,
`TestBootstrapAll_PlanRespectsGate`. The context timeout was already short (200ms);
the real cost was the **5s `WaitDelay` kill-grace** in `pkg/terragrunt/runner.go`
SIGINT-ing a faked `sleep 30` that didn't die promptly. Fix: fake script uses
`exec sleep 30` (SIGINT reaches `sleep` directly → dies instantly), plus a fast
`exit 0` fake terragrunt for the plan test. Test-only. ~14s removed.

### 2. Port-forward / agent select-loops — DONE (`fee437fa`)
The `sleep` seam didn't cover `time.After` / `time.NewTicker` select-loop waits in
`shell.go` (reconnect 2s / grace 20s / tick 20s) and `agent.go` (poll 2s/2s/1s).
Promoted all six to package vars (production defaults unchanged) and shrank them to
1ms in `TestMain`. `TestAgentNonInteractive_IdleReset` keeps a local 50ms override
so its poll loop still outlasts the heartbeat ticker. Desktop 5–6s → 0.01s.

### 3. Build/subprocess cost — DONE (`06100dcb`) — the biggest win (~150s)
`RunInitPlanWithRunner` calls `BuildLambdaZipsFunc`, whose default **downloads the
terraform binary + go-builds 9 lambdas on every call**, paid by all 10
`TestRunInitPlan_*` (~130s) even though the runner is already mocked. Fix: global
no-op of the `BuildLambdaZipsFunc` DI seam in `TestMain` (the two build-contract
tests override it locally, so coverage is preserved). Separately, `buildKM` now
builds the km binary **once** via `sync.Once` instead of `go build` + `t.Cleanup`
removal on all 40 call sites. `TestRunInitPlan_*`: ~130s → 1.26s.

### 4. Real AWS calls in cobra-command tests — DONE (`6aa1398d`)
`ResolveSandboxID` (and ~64 sites) call `LoadAWSConfig(ctx, "klanker-terraform")` —
a **named-profile load that hits real SSO/AWS** on every command, bypassing the
dummy env creds in `TestMain`. Fix: promote `LoadAWSConfig`/`LoadAWSConfigInRegion`
to `pkg/aws` package vars (default = real impl; production unchanged) and override
them in cmd `TestMain` to a config whose credentials provider errors instantly — so
the incidental DynamoDB call fails in microseconds with no network. The slow
families (Status/Agent/Resolve/Destroy/Lock) already tolerated a failing call
(they passed under expired SSO), so this is coverage-safe; reviewed non-vacuous.
**244s → ~34s.**

**Gotcha introduced:** any AWS call via `LoadAWSConfig[InRegion]` now fast-fails in
cmd tests. A test that genuinely needs a working AWS config must restore the real
loader locally (save/restore the var).

## 5. The structural lever — DEFERRED
Split `internal/app/cmd` (1,259 test funcs / 145 files / 1 package, only 2
`t.Parallel()`, 26 files use `t.Setenv` which is incompatible with `t.Parallel`)
into parallel sub-package test binaries so `go test ./...` runs them concurrently.
**Deferred:** with the next-slowest packages already at ~12–15s
(`cmd/km-h1-bridge`, `pkg/github/bridge`, `pkg/compiler`), a split only buys
~33s → ~15s of whole-suite wall-clock — a large mechanical refactor (plus a
`t.Setenv` isolation pass) for diminishing returns now that 1–4 banked the ~14×.

## How to verify
1. `go test ./internal/app/cmd/ -count=1 2>&1 | tail -5 ; echo EXIT=$?` — expect
   `ok ... ~33s` (or 5–8 environmental SSO failures if the local token is expired).
2. Never let a pipe mask a FAIL: always capture the command's own exit code.
3. Any new failure OUTSIDE the environmental configure/cluster/bootstrap set is a
   real regression.
