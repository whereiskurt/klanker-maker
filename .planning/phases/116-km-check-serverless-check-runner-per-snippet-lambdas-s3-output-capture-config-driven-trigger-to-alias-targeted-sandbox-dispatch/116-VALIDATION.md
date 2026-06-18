---
phase: 116
slug: km-check-serverless-check-runner
status: approved
nyquist_compliant: true
wave_0_complete: false  # test stubs are embedded inline as the RED task of each TDD plan (116-02/04/05/06), not a separate Wave 0 plan
created: 2026-06-17
---

# Phase 116 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Net-new feature — **no pre-existing requirement IDs** in REQUIREMENTS.md apply
> (confirmed by full read). Behaviors are validated against the phase Success
> Criteria + the design spec, not REQ-IDs.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` (standard library) |
| **Config file** | none (module-level test flags) |
| **Quick run command** | `go test ./pkg/dispatch/... ./internal/app/config/ ./internal/app/cmd/ -count=1 -timeout 120s` |
| **Full suite command** | `go test ./... -count=1 -timeout 600s` |
| **Estimated runtime** | ~120 s quick / ~10 min full |

**ALWAYS check exit code, not the pipe** (`feedback_check_go_test_exit_not_pipe`):
`go test ./... > /tmp/t.out 2>&1; echo "EXIT=$?"` then read the ok/FAIL summary.
Use `-timeout 600s`. The whole-repo suite is currently green
(`project_full_suite_known_red_packages` RESOLVED) — a FAIL means a real regression.

---

## Sampling Rate

- **After every task commit:** `go test ./pkg/dispatch/... ./internal/app/config/ ./internal/app/cmd/ -count=1 -timeout 120s`
- **After every plan wave:** `go test ./... -count=1 -timeout 600s`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** ~120 s (quick) / ~600 s (full)

---

## Per-Task Verification Map

> Warm-path note: the warm/resume terminal action is **SSM agent-run dispatch**
> (reuse `ttl-handler.handleAgentRun`), NOT SQS enqueue. Tests assert the dispatch
> DECISION (resolve → exists?agent-run : absent?cold-create|skip), mocking the
> agent-run + PutSandboxCreate sinks.

| Behavior | Test Type | Automated Command | File Exists | Status |
|----------|-----------|-------------------|-------------|--------|
| `pkg/dispatch.ResumeOrCreate` — absent alias + onAbsent=cold-create → PutSandboxCreate | unit (mock) | `go test ./pkg/dispatch/ -run TestResumeOrCreate_AbsentColdCreate` | ❌ W0 | ⬜ pending |
| `pkg/dispatch.ResumeOrCreate` — absent alias + onAbsent=skip → no-op | unit | `go test ./pkg/dispatch/ -run TestResumeOrCreate_AbsentSkip` | ❌ W0 | ⬜ pending |
| `pkg/dispatch.ResumeOrCreate` — existing (running) alias → agent-run dispatch | unit | `go test ./pkg/dispatch/ -run TestResumeOrCreate_RunningAgentRun` | ❌ W0 | ⬜ pending |
| `pkg/dispatch.ResumeOrCreate` — existing (stopped/paused) alias → agent-run dispatch (auto-resume) | unit | `go test ./pkg/dispatch/ -run TestResumeOrCreate_StoppedAgentRun` | ❌ W0 | ⬜ pending |
| Cooldown: second fire within window suppressed (`check-trigger:{name}` nonce) | unit | `go test ./pkg/dispatch/ -run TestResumeOrCreate_Cooldown` | ❌ W0 | ⬜ pending |
| Config merge: `checks` key in merge-list populates `ChecksConfig.Triggers` | unit | `go test ./internal/app/config/ -run TestChecksConfigMerge` | ❌ W0 | ⬜ pending |
| `KM_CHECK_TRIGGER` bake: inline `when_py` → JSON env value | unit | `go test ./internal/app/cmd/ -run TestCheckTriggerBakeInline` | ❌ W0 | ⬜ pending |
| `KM_CHECK_TRIGGER` bake: `@file` predicate + `@file` prompt resolved at deploy | unit | `go test ./internal/app/cmd/ -run TestCheckTriggerBakeAtFile` | ❌ W0 | ⬜ pending |
| `sourceHash` covers resolved predicate+prompt (drift detect) | unit | `go test ./internal/app/cmd/ -run TestCheckSourceHash` | ❌ W0 | ⬜ pending |
| `regionalModules()` includes dynamodb-checks + check-runner-role; count bumped | unit | `go test ./internal/app/cmd/ -run TestRunInitPlan_ModuleOrder` | ✅ (must bump 22→24) | ⬜ pending |
| Python bootstrap: `when_py` truthy → emits `CheckDispatch` (boto3 stubbed) | Python unit | `python3 -m pytest profiles/checks/_bootstrap/test_bootstrap.py -k dispatch` | ❌ W0 | ⬜ pending |
| Python bootstrap: non-JSON stdout → output captured, NO trigger | Python unit | `python3 -m pytest profiles/checks/_bootstrap/test_bootstrap.py -k notjson` | ❌ W0 | ⬜ pending |
| Python bootstrap: env precedence static→secret→per-run (later wins) | Python unit | `python3 -m pytest profiles/checks/_bootstrap/test_bootstrap.py -k env` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/dispatch/dispatch.go` — the factored `ResumeOrCreate` (+ small interfaces for resolver/agent-run/cold sinks)
- [ ] `pkg/dispatch/dispatch_test.go` — 5 unit tests (3-way decision + cooldown) with mock sinks
- [ ] `internal/app/config/config_check_test.go` — `checks` merge test
- [ ] `internal/app/cmd/check_test.go` — trigger-bake (inline + @file) + sourceHash tests
- [ ] `profiles/checks/_bootstrap/test_bootstrap.py` + `conftest.py` — Python bootstrap unit tests (stub boto3 S3/EventBridge); requires `pytest` available locally (Wave 0 installs if absent)
- [ ] Update `TestRunInitPlan_ModuleOrder` hardcoded count 22 → 24

---

## Manual-Only Verifications (live AWS — `AWS_PROFILE=klanker-application`)

| Behavior | Why Manual | Test Instructions |
|----------|------------|-------------------|
| QOTD check deploy + run + S3 output | needs real Lambda + internet egress | `make build && km init --dry-run=false` (scaffolding) → `km check deploy profiles/checks/qotd/qotd.py --schedule "rate(1 hour)"` → `km check run qotd` → assert `s3://{artifacts}/check-runs/qotd/<ts>/output.json` exists + contains a quote |
| Wiz check trigger → CheckDispatch → sandbox | needs real EventBridge + ttl-handler + a target alias box | configure `checks.triggers` for `wiz-intel` with `when_py` threshold + `alias: <box>`; `km check sync` → `km check run wiz-intel` (force threshold) → observe `CheckDispatch` in EventBridge + ttl-handler CloudWatch logs + the alias box resumed and prompted (`km otel`/`km logs`) |
| Wiz check below threshold → NO dispatch | negative path | run with sub-threshold sim data → assert no `CheckDispatch`, output still in S3 |
| `--image` container packaging | needs Docker + ECR | deploy a check with a `Dockerfile` via `--image`; assert lazy `{prefix}-checks` ECR repo created + Lambda `PackageType=Image` runs |
| `github.events` `check:` pre-filter | needs a real webhook delivery | wire `check:` on a `github.events` rule; deliver event; assert sandbox dispatched only when the check triggers |
| `km doctor` check group | needs deployed checks + an induced orphan | `km doctor` reports table present, flags an orphan `{prefix}-check-*` Lambda + a drifted `KM_CHECK_TRIGGER` |

---

## Validation Sign-Off

- [x] All Go tasks have `<automated>` verify or Wave 0 dependencies
- [x] Python bootstrap has unit coverage (pytest, 116-04) + live UAT (116-08)
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references (inline RED tasks per TDD plan)
- [x] No watch-mode flags
- [x] Feedback latency < 120 s (quick)
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** approved (planner mapped every task; waves corrected)
