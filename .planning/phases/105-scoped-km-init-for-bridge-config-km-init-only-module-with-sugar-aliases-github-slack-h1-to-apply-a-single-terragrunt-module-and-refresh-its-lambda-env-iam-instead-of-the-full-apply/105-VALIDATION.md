---
phase: 105
slug: scoped-km-init-for-bridge-config
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-11
---

# Phase 105 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` package (stdlib) |
| **Config file** | none — standard `go test ./...` |
| **Quick run command** | `go test ./internal/app/cmd/ -run TestScoped -v -timeout 30s` |
| **Full suite command** | `go test ./internal/app/cmd/ -timeout 120s` |
| **Estimated runtime** | ~30–90 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/ -run TestScoped -v -timeout 30s`
- **After every plan wave:** Run `go test ./internal/app/cmd/ -timeout 120s`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** ~90 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 105-01-01 | 01 | 0 | INIT-SCOPED-TESTS | unit (stubs) | `go test ./internal/app/cmd/ -run TestScoped -v` | ❌ W0 | ⬜ pending |
| 105-02-01 | 02 | 1 | INIT-SCOPED-FLAG | unit | `go test ./internal/app/cmd/ -run TestScopedModuleResolution -v` | ❌ W0 | ⬜ pending |
| 105-02-02 | 02 | 1 | INIT-SCOPED-FLAG | unit | `go test ./internal/app/cmd/ -run TestScopedModuleRejection -v` | ❌ W0 | ⬜ pending |
| 105-02-03 | 02 | 1 | INIT-SCOPED-ALIASES | unit | `go test ./internal/app/cmd/ -run TestScopedAliases -v` | ❌ W0 | ⬜ pending |
| 105-02-04 | 02 | 1 | INIT-SCOPED-GUARD | unit | `go test ./internal/app/cmd/ -run TestScopedMutualExclusion -v` | ❌ W0 | ⬜ pending |
| 105-03-01 | 03 | 2 | INIT-SCOPED-IMPL | unit | `go test ./internal/app/cmd/ -run TestScopedDryRun -v` | ❌ W0 | ⬜ pending |
| 105-03-02 | 03 | 2 | INIT-SCOPED-IMPL | unit | `go test ./internal/app/cmd/ -run TestScopedApply -v` | ❌ W0 | ⬜ pending |
| 105-03-03 | 03 | 2 | INIT-SCOPED-IMPL | unit | `go test ./internal/app/cmd/ -run TestScopedEnvVarsExported -v` | ❌ W0 | ⬜ pending |
| 105-04-01 | 04 | 3 | INIT-SCOPED-GUARD | unit | `go test ./internal/app/cmd/ -run TestScopedTier2Gate -v` | ❌ W0 | ⬜ pending |
| 105-04-02 | 04 | 3 | INIT-SCOPED-GUARD | unit | `go test ./internal/app/cmd/ -run TestScopedTier2GateBlocked -v` | ❌ W0 | ⬜ pending |
| 105-04-03 | 04 | 3 | INIT-SCOPED-IMPL | unit | `go test ./internal/app/cmd/ -run TestScopedSesPreflight -v` | ❌ W0 | ⬜ pending |
| 105-05-01 | 05 | 4 | INIT-SCOPED-DOCS | manual review | grep docs/skill for `--github`/`--slack`/`--h1`/`--email` | Existing | ⬜ pending |
| 105-05-02 | 05 | 4 | INIT-SCOPED-IMPL (no-drift) | manual UAT | `km init --github --dry-run=false` → `km init --plan` shows no new trips | N/A | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*
*Task IDs above are indicative; the planner sets final plan/wave/task numbering. The point is each requirement has an automated test or an explicit manual-UAT row.*

---

## Wave 0 Requirements

- [ ] `internal/app/cmd/init_scoped_test.go` — stub tests for INIT-SCOPED-FLAG / INIT-SCOPED-ALIASES / INIT-SCOPED-GUARD / INIT-SCOPED-IMPL, using the existing `mockRunner` / `mockPlanRunner` patterns from the current init test files (research confirmed these exist and are injectable).

*No new framework install needed — standard Go test infrastructure is already in place.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| No-drift invariant | INIT-SCOPED-IMPL | Requires a live install + real terragrunt/S3 state; cannot be asserted from a unit mock | On a live install: edit a `github.*` key (e.g. flip `default_router`), run `km init --github --dry-run=false`, confirm the bridge Lambda env block updated; then run `km init --plan` and confirm it plans the same env block as a NO-OP (no destroy-class trips, no env change) — proving zero drift. |
| Tier-2 `ses` no-op on stable install | INIT-SCOPED-GUARD | Consolidated S3 bucket policy diff can only be observed against real state | On a stable live install run `km init --only ses` (dry-run/plan); confirm the destroy-class gate reports no protected destroy/replace and no spurious `aws_s3_bucket_policy` change. |
| `--slack` env-block + SSM refresh | INIT-SCOPED-ALIASES | Bridge reads bot-user-id from SSM at cold start; needs live Lambda | After `km init --slack`, confirm `EnsureSlackBotUserIDFromSSM` ran and the bridge picks up `slack.mention_only`/`react_always` changes. |
| `--github`/`--h1` command republish to SSM | INIT-SCOPED-IMPL | `PublishGitHubCommandsToSSM` / `PublishH1CommandsToSSM` target live SSM | After scoped apply, confirm the bridge's command set reflects the edited `github.commands` / `h1.programs` (read from SSM, not just env). |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (`init_scoped_test.go`)
- [ ] No watch-mode flags
- [ ] Feedback latency < 90s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
