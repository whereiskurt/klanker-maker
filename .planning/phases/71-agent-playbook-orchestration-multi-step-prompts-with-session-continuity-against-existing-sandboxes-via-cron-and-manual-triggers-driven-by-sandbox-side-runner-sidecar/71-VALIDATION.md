---
phase: 71
slug: agent-playbook-orchestration-multi-step-prompts-with-session-continuity-against-existing-sandboxes-via-cron-and-manual-triggers-driven-by-sandbox-side-runner-sidecar
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-05
---

# Phase 71 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Source mapping: 71-RESEARCH.md § Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test ./...` (standard Go testing — same as all existing km tests) |
| **Config file** | none |
| **Quick run command** | `go test ./pkg/playbook/... ./internal/app/cmd/... -run TestPlaybook -count=1` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~45 seconds quick / ~3 minutes full |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/playbook/... ./internal/app/cmd/... -run TestPlaybook -count=1`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 60 seconds for the per-commit run; ≤ 3 minutes for the full suite

---

## Per-Task Verification Map

> Filled by `gsd-planner` once PLAN.md files exist. The Success-Criterion → Test mapping below is the contract the planner must honor when deriving task-level `<automated>` blocks.

| SC# | Plan (TBD) | Wave (TBD) | Test Type | Automated Command | Notes |
|-----|-----------|-----------|-----------|-------------------|-------|
| SC-1 — `km playbook validate` | TBD | TBD | unit | `go test ./pkg/playbook/... -run TestValidate` | Table-driven; one row per invalid-rule variant from SPEC § Validation rules |
| SC-2 — `km playbook apply` content-addressed S3 + DDB idempotency | TBD | TBD | unit (fake S3 + DDB) | `go test ./internal/app/cmd/... -run TestPlaybookApply` | DI per `at_test.go` convention |
| SC-3 — manual `km playbook run` end-to-end on running sandbox | TBD | TBD | unit (mocks) + manual UAT | `go test ./internal/app/cmd/... -run TestPlaybookRun` + UAT script | Real-sandbox path is manual UAT (E2E) |
| SC-4 — cross-run session continuity via DDB session-map | TBD | TBD | unit (mock DDB) + manual UAT | `go test ./pkg/playbook/runner/... -run TestSessionResume` + UAT | Unit verifies the GetItem/UpdateItem flow; UAT verifies model memory |
| SC-5 — `km at` schedule routes through TTL Lambda new event | TBD | TBD | unit (mock SchedulerAPI) | `go test ./internal/app/cmd/... -run TestAtPlaybookRun` | Verify Input JSON shape; existing `at_test.go` template |
| SC-6 — sandbox readiness (running / stopped / missing) | TBD | TBD | unit (mock EC2 + DDB + SQS) | `go test ./cmd/ttl-handler/... -run TestHandlePlaybookRun` | Table-driven over EC2 states; per researcher SC#6 path |
| SC-7 — step-failure abort + notify | TBD | TBD | unit (claude shim) | `go test ./pkg/playbook/runner/... -run TestStepFailure` | Shim binary exits non-zero on step N |
| SC-8 — concurrent-fire SQS FIFO serialization | TBD | TBD | integration (localstack or RUN_E2E gate) | `go test ./... -run TestConcurrentPlaybook -tags integration` | Verify DDB started_at ordering across runs |
| SC-9 — crash-mid-step idempotency on SIGKILL | TBD | TBD | integration | `go test ./... -run TestPlaybookRunnerCrashRecovery -tags integration` | Most novel surface — must have explicit coverage |
| SC-10 — `km doctor` three new checks | TBD | TBD | unit (mock SQS + DDB + SSM) | `go test ./internal/app/cmd/... -run TestDoctorPlaybook` | Inject missing queue URL → assert CheckError |
| SC-11 — `km destroy` atomic teardown of FIFO + DLQ + SSM, DDB rows preserved | TBD | TBD | unit | `go test ./internal/app/cmd/... -run TestDestroyPlaybook` | Verify delete-call set; assert DDB Get returns rows |
| SC-12 — operator-notify hook `playbook-run-completed` payload | TBD | TBD | unit (compiler heredoc) | `go test ./pkg/compiler/... -run TestUserDataPlaybookHook` + bash CI | Verify case branch in km-notify-hook + email/Slack route |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

These files don't exist yet — Wave 0 of the planner output must create stub test files so subsequent waves have green baselines (mirrors Phase 67's Plan 67-00 and Phase 68's Plan 68-00).

- [ ] `pkg/playbook/` — entire new package (parse, validate, types) + `playbook_test.go` stubs for `TestParse`, `TestValidate`, `TestApply`
- [ ] `pkg/playbook/testdata/valid.yaml` + `pkg/playbook/testdata/invalid-*.yaml` (one fixture per invalid rule)
- [ ] `pkg/playbook/runner/` — runner-loop logic with mockable AWS clients + `runner_test.go` stubs for `TestSessionResume`, `TestStepFailure`, `TestPlaybookRunnerCrashRecovery`
- [ ] `internal/app/cmd/playbook.go` + `playbook_test.go` (CLI: validate/apply/run/list/show/list-runs/show-run/logs/cancel-run/delete) + `TestPlaybookApply`, `TestPlaybookRun`
- [ ] `internal/app/cmd/at_test.go` additions: `TestAtPlaybookRun` (Input JSON shape verification)
- [ ] `internal/app/cmd/create_playbook.go` + `destroy_playbook.go` + `doctor_playbook.go` + `_test.go` stubs (`TestDestroyPlaybook`, `TestDoctorPlaybook`)
- [ ] `cmd/ttl-handler/main_test.go` additions: `TestHandlePlaybookRun` cases for {running, stopped, paused, terminated, missing}
- [ ] `pkg/compiler/userdata_test.go` additions: `TestUserDataPlaybookHook` (asserts the new `case "playbook-run-completed")` branch in km-notify-hook heredoc renders)
- [ ] `infra/modules/dynamodb-playbooks/v1.0.0/` + `dynamodb-playbook-sessions/v1.0.0/` + `dynamodb-playbook-runs/v1.0.0/` (Terraform modules; `terraform validate` is the smoke check)
- [ ] `infra/live/{region}/dynamodb-playbooks/terragrunt.hcl` + corresponding entries for `playbook-sessions` + `playbook-runs`

---

## Manual-Only Verifications

Some criteria can only be proven against a real sandbox; mock-based unit tests cannot replace them. These map to the manual UAT plan that the final plan in the wave will own (mirrors `67-10-PLAN.md` and `69-10-PLAN.md`).

| Behavior | SC# | Why Manual | Test Instructions |
|----------|-----|------------|-------------------|
| End-to-end `km playbook run` walks all steps to completion on a real sandbox with `playbookEnabled: true` | SC-3 | Requires real EC2 + real Claude session + real SQS round-trip | `km create profiles/playbook-test.yaml`; `km playbook apply playbooks/morning-ops.yaml`; `km playbook run morning-ops --sandbox sb-X`; `km playbook show-run <id>` shows status=completed and 3/3 steps |
| Cross-run session continuity — model demonstrably remembers prior turn | SC-4 | Memory verification requires LLM observation, not assertion | After SC-3 manual: re-run `km playbook run morning-ops --sandbox sb-X` with a step prompt like "what did you summarize on the prior run?"; assert non-empty answer referencing prior content |
| EventBridge schedule fire path (cron actually triggers Lambda) | SC-5 | EventBridge fire timing not unit-testable; localstack scheduler patchy | `km at '*/5 * * * ? *' playbook run morning-ops --sandbox sb-X`; wait one boundary; verify `km playbook show-run` of new row |
| ≤ 120s wall-clock from cron fire to first step on a hibernated sandbox | SC-6 | Real EC2 hibernation resume timing | Hibernate sb-X; `km playbook run` immediately; record timestamp of first step exec in DDB; assert ≤ 120 s |
| Crash-mid-step idempotency under real systemd | SC-9 | systemd restart + SQS visibility re-delivery only realistic on real EC2 | `km shell sb-X`; in another window run `km playbook run morning-ops --sandbox sb-X`; SIGKILL `km-playbook-runner` mid-step; verify systemd restarts it, run completes, exactly one playbook-runs row exists |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 60s (per-commit) / 180s (full suite)
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
