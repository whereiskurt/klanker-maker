# Phase 67.2 — Deferred Items

Items discovered during plan 67.2-02 execution that are out-of-scope
for this phase.

## Pre-existing pkg/compiler test failures

**Discovered during:** Plan 67.2-02 execution (Task 3 verification — final
project-wide `go test ./...`)

**Status:** Pre-existing — verified by `git stash`-ing all 67.2-02
changes and re-running `go test ./pkg/compiler/`. Same six failures
reproduce on a clean tree.

**Failing tests:**
- `TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock`
- `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`
- `TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime`
- `TestUserDataKMTracingServicectlStart`
- `TestAuditHookNonBlocking`
- `TestGitHubUserDataGITASKPASS`

**Reason for deferral:** Plan 67.2-02 only modified files in
`pkg/slack/bridge/` (aws_adapters.go, aws_adapters_test.go,
events_handler.go). The `pkg/compiler` failures are entirely unrelated
to ACK reaction retry, and per executor scope-boundary policy
("Only auto-fix issues DIRECTLY caused by the current task's
changes"), they are out-of-scope for this plan.

**Fix path:** A separate phase or hot-fix plan should investigate the
six `pkg/compiler` test failures. They appear to be userdata-template
assertion drift, possibly from recent Phase 79 / 80 / 81 changes.
Recommended starting point: `git log --oneline pkg/compiler/userdata*`
to find the most recent userdata-template edits and bisect from there.

**Impact on Phase 67.2:** None. The
`pkg/slack/bridge/` test suite — which is the only surface plan 67.2
touches — is fully green:
- 10 new `TestReactor_*` tests (added in plan 02 task 2)
- 4 existing `TestSlackReactorAdapter_*` tests (1 updated for new retry
  loop)
- 4 existing `TestEventsHandler_Reactor_*` tests (unchanged, verified
  pass after handler-timeout bump)
- 1 `TestClassifyReactionError` table-driven test (added in plan 01)

## Phase 80 follow-up: `km init` should auto-remediate Terraform lock-file drift

**Discovered during:** Plan 67.2-03 Task 3 operator UAT (2026-05-15 deploy
cycle).

**Status:** Out-of-scope for Phase 67.2 — cross-phase issue affecting any
operator running `km init` on a workstation initialized before Phase 80.

**Issue:** Phase 80 (commit `c578db1`) added `hashicorp/tls` to `root.hcl`'s
`required_providers` to support the new `cluster-irsa` / `km-operator-policy`
modules. Pre-Phase-80 modules still carry `.terraform.lock.hcl` files that
predate this addition. Running `km init --lambdas` (and full `km init`) on a
workstation initialized before Phase 80 fails with a Terraform provider-lock
mismatch on the `tls` provider.

**Operator workaround (until fix lands):** refresh the lock files manually
before running `km init`:

```bash
cd infra/live
terragrunt run --all --queue-exclude-dir 'sandboxes/**' init -- -upgrade
# Then re-run the original km init command
```

After this remediation, the Phase 67.2 deploy proceeded cleanly and the
bridge Lambda received the new zip with the retry loop.

**Fix path:** `pkg/terragrunt/runner.go` (or wherever `km init` shells to
terragrunt) should either:
1. Pass `-upgrade` to the `init` call automatically when it detects a
   `Failed to query available provider packages` error — retry once, then
   continue with the original command. OR
2. Pre-flight check: parse `root.hcl`'s `required_providers` vs each
   module's `.terraform.lock.hcl` and proactively run `terragrunt init
   --upgrade` for modules whose lock files don't contain all the
   root-declared providers. OR
3. At minimum, document the lock-drift symptom and the remediation command
   inside `km init`'s error output when it sees the provider-lock mismatch
   error from terragrunt.

**Impact if NOT fixed:** Every operator who runs `km init` on a workstation
initialized before Phase 80 hits this on first run. Phase 67.2's deploy was
the first observed instance; future phases that touch infra will keep
tripping it until the auto-remediation lands.

**Recommended phase:** Phase 80.x hotfix, or rolled into whichever next
phase touches `pkg/terragrunt/`.
