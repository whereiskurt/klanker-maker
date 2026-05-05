---
phase: 71
slug: agent-playbook-orchestration-multi-step-prompts-with-session-continuity-against-existing-sandboxes-via-cron-and-manual-triggers-driven-by-sandbox-side-runner-sidecar
status: planned
nyquist_compliant: true
wave_0_complete: false
created: 2026-05-05
updated: 2026-05-05
---

# Phase 71 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Source mapping: 71-RESEARCH.md § Validation Architecture.
> Per-Task Verification Map populated by `gsd-planner` 2026-05-05.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test ./...` (standard Go testing — same as all existing km tests) |
| **Config file** | none |
| **Quick run command** | `go test ./pkg/playbook/... ./internal/app/cmd/... -run TestPlaybook -count=1` |
| **Full suite command** | `go test ./...` |
| **Integration tests** | `go test -tags integration ./test/integration/... -run TestPlaybook` (requires RUN_E2E=1 + AWS creds + KM_TEST_SANDBOX_ID) |
| **Estimated runtime** | ~45 seconds quick / ~3 minutes full / ~10 minutes integration |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/playbook/... ./internal/app/cmd/... -run TestPlaybook -count=1`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 60 seconds for the per-commit run; ≤ 3 minutes for the full suite

---

## Per-Task Verification Map

> Filled by `gsd-planner` 2026-05-05. Plan + Wave columns reflect the actual phase plan structure.

| SC# | Plan(s) | Wave(s) | Test Type | Automated Command | Notes |
|-----|---------|---------|-----------|-------------------|-------|
| SC-1 — `km playbook validate` | 71-01, 71-06 (CLI surface) | 1, 2 | unit | `go test ./pkg/playbook/... -run TestValidate` and `go test ./internal/app/cmd/... -run TestPlaybookValidate` | Table-driven; one row per invalid-rule variant; CLI integration adds the `km playbook validate <file>` invocation in plan 71-06 |
| SC-2 — `km playbook apply` content-addressed S3 + DDB idempotency | 71-06 | 2 | unit (fake S3 + DDB) | `go test ./pkg/playbook/... -run TestApply` and `go test ./internal/app/cmd/... -run TestPlaybookApply` | DI per `at_test.go` convention; HEAD-then-PUT idempotency tested |
| SC-3 — manual `km playbook run` end-to-end on running sandbox | 71-04 (Lambda), 71-05 (runner), 71-06 (CLI), 71-08 (queue provisioning), 71-10 (UAT Scen 3) | 2, 2, 2, 3, 5 | unit at each layer + manual UAT | `go test ./cmd/ttl-handler/... -run TestHandlePlaybookRun` + `go test ./pkg/playbook/runner/...` + `go test ./internal/app/cmd/... -run TestPlaybookRun` + 71-UAT.md Scenario 3 | Real-sandbox path is manual UAT (E2E) — no localstack-equivalent for Claude Code |
| SC-4 — cross-run session continuity via DDB session-map | 71-05 (runner), 71-10 (UAT Scen 4) | 2, 5 | unit (mock DDB) + manual UAT | `go test ./pkg/playbook/runner/... -run TestSessionResume` + 71-UAT.md Scenario 4 | Unit verifies the GetItem/UpdateItem flow + --resume flag passing; UAT verifies model memory of prior turn |
| SC-5 — `km at` schedule routes through TTL Lambda new event | 71-04, 71-07 (km at extension), 71-10 (UAT Scen 5) | 2, 3, 5 | unit (mock SchedulerAPI) + manual UAT | `go test ./internal/app/cmd/... -run TestAtPlaybookRun` + 71-UAT.md Scenario 5 | Unit verifies Input JSON shape + two-word merge + ResolveSandboxID guard; UAT verifies real EventBridge fire |
| SC-6 — sandbox readiness (running / stopped / paused / terminated / missing) | 71-04, 71-10 (UAT Scen 6) | 2, 5 | unit (mock EC2 + DDB + SQS) + manual UAT | `go test ./cmd/ttl-handler/... -run TestHandlePlaybookRun` + 71-UAT.md Scenario 6 | Table-driven over 5 EC2 states; UAT measures real wall-clock ≤ 120s on hibernated sandbox |
| SC-7 — step-failure abort + notify | 71-05 | 2 | unit (claude shim) | `go test ./pkg/playbook/runner/... -run TestStepFailure` | Shim ClaudeExec returns exit≠0 on step N → assert run status=failed, current_step pinned, no step-(N+1) call, notify fired |
| SC-8 — concurrent-fire SQS FIFO serialization | 71-10 (integration test) | 5 | integration (real SQS, build tag) | `go test -tags integration ./test/integration/... -run TestConcurrentPlaybook` (requires RUN_E2E=1 + AWS creds + KM_TEST_SANDBOX_ID) | Verify DDB started_at/ended_at ordering: same-playbook serial, different-playbook parallel |
| SC-9 — crash-mid-step idempotency on SIGKILL | 71-05 (Go shim partial), 71-10 (integration + UAT Scen 7) | 2, 5 | unit (Go shim) + integration + manual UAT | `go test ./pkg/playbook/runner/... -run TestPlaybookRunnerCrashRecovery` (skipped at unit level — see plan 71-05) + `go test -tags integration ./test/integration/... -run TestPlaybookRunnerCrashRecovery` + 71-UAT.md Scen 7 | Most novel surface — SIGKILL the systemd unit, verify replay |
| SC-10 — `km doctor` three new checks | 71-09, 71-10 (UAT Scen 8) | 4, 5 | unit (mock SQS + DDB) + manual UAT | `go test ./internal/app/cmd/... -run TestDoctorPlaybook` + 71-UAT.md Scenario 8 | Three checks: playbook_queue_exists, playbook_dlq_depth, playbook_queue_healthy (renamed from SPEC's playbook_runner_service_active per planner finding #9) |
| SC-11 — `km destroy` atomic teardown of FIFO + DLQ + SSM, DDB rows preserved | 71-08, 71-10 (UAT Scen 9) | 3, 5 | unit + manual UAT | `go test ./internal/app/cmd/... -run TestDestroyPlaybook` (includes TestDestroyPlaybook_PreservesHistory) + 71-UAT.md Scenario 9 | History-preservation is the load-bearing assertion |
| SC-12 — operator-notify hook `playbook-run-completed` payload | 71-05 (case branch in heredoc), 71-10 (UAT Scen 10) | 2, 5 | unit (compiler heredoc) + manual UAT | `go test ./pkg/compiler/... -run TestUserDataPlaybookHook` + 71-UAT.md Scenario 10 | Verify case branch in km-notify-hook + email/Slack route via existing transport |

*Status legend: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Plan-to-Wave Map (Execution Order)

> Wave-dependency invariant: if `B.depends_on` includes `A`, then `B.wave > A.wave`. Same-wave plans MUST be independent (the GSD executor runs them in parallel).
> Updated 2026-05-05 per checker iteration 1: 71-04 was previously placed in Wave 1 alongside its dependencies 71-02 and 71-03 (circular within Wave 1); 71-07 was Wave 2 but depends on 71-04 (also Wave 2); 71-09 was Wave 3 but depends on 71-08 (also Wave 3). All three corrected; 71-10 cascaded from Wave 4 to Wave 5 because it depends on 71-09.

| Wave | Plans | Notes |
|------|-------|-------|
| 0 | 71-00 | Stubs (test files, package skeletons, YAML fixtures) — must run before all other plans |
| 1 | 71-01, 71-02, 71-03 | Independent foundations: profile schema + pkg/playbook validate, DDB modules, SQS helpers — all depend only on 71-00 |
| 2 | 71-04, 71-05, 71-06 | TTL Lambda handler (depends on 71-02 + 71-03), userdata runner emission (depends on 71-01 + 71-02), CLI surface (depends on 71-01) |
| 3 | 71-07, 71-08 | km at extension (depends on 71-04), lifecycle wiring create/destroy/init (depends on 71-02, 71-03, 71-04, 71-05) |
| 4 | 71-09 | Doctor checks (depends on 71-03 + 71-08) |
| 5 | 71-10 | Closeout: docs, integration tests, UAT — `autonomous: false` (depends on 71-05, 71-06, 71-07, 71-08, 71-09; last task is operator checkpoint) |

---

## Wave 0 Requirements

These files don't exist yet — Wave 0 (Plan 71-00) MUST create stub test files so subsequent waves have green baselines (mirrors Phase 67's Plan 67-00 and Phase 68's Plan 68-00).

- [ ] `pkg/playbook/` — entire new package (parse, validate, types) + `playbook_test.go` stubs for `TestParse`, `TestValidate`, `TestApply`
- [ ] `pkg/playbook/testdata/valid.yaml` + 5 `pkg/playbook/testdata/invalid-*.yaml` fixtures (one per invalid rule variant)
- [ ] `pkg/playbook/runner/` — runner skeleton with mockable AWS clients + `runner_test.go` stubs for `TestSessionResume`, `TestStepFailure`, `TestPlaybookRunnerCrashRecovery`
- [ ] `internal/app/cmd/playbook.go` + `playbook_test.go` (CLI: 10 sub-commands) + matching `TestPlaybookXxx` stubs
- [ ] `internal/app/cmd/at_playbook_test.go`: `TestAtPlaybookRun` + `TestAtPlaybookRunInputShape` stubs
- [ ] `internal/app/cmd/create_playbook_test.go` + `destroy_playbook_test.go` + `doctor_playbook_test.go` stubs
- [ ] `cmd/ttl-handler/playbook_handler_test.go`: `TestHandlePlaybookRun` cases for {running, stopped, paused, terminated, missing}
- [ ] `pkg/compiler/userdata_playbook_test.go`: `TestUserDataPlaybookRunner` + `TestUserDataPlaybookHook`
- [ ] `pkg/profile/validate_playbook_test.go`: `TestValidatePlaybookEnabled`

(Plan 71-02's three Terraform modules + Terragrunt entries are NOT Wave 0 stubs — they're net-new infra files created in Wave 1. `terraform validate` is the smoke check there.)

---

## Manual-Only Verifications

Some criteria can only be proven against a real sandbox; mock-based unit tests cannot replace them. These map to the manual UAT plan (71-UAT.md) created by Plan 71-10.

| Behavior | SC# | Why Manual | Test Instructions |
|----------|-----|------------|-------------------|
| End-to-end `km playbook run` walks all steps to completion on a real sandbox with `playbookEnabled: true` | SC-3 | Requires real EC2 + real Claude session + real SQS round-trip | 71-UAT.md Scenario 3 |
| Cross-run session continuity — model demonstrably remembers prior turn | SC-4 | Memory verification requires LLM observation, not assertion | 71-UAT.md Scenario 4 |
| EventBridge schedule fire path (cron actually triggers Lambda) | SC-5 | EventBridge fire timing not unit-testable; localstack scheduler patchy | 71-UAT.md Scenario 5 |
| ≤ 120s wall-clock from cron fire to first step on a hibernated sandbox | SC-6 | Real EC2 hibernation resume timing | 71-UAT.md Scenario 6 |
| Crash-mid-step idempotency under real systemd | SC-9 | systemd restart + SQS visibility re-delivery only realistic on real EC2 | 71-UAT.md Scenario 7 (companion to integration test) |

The integration tests in `test/integration/` (build tag `integration`, env gate `RUN_E2E=1`) cover SC-8 (concurrent serialization) and SC-9 (crash recovery) with real SQS and SSM-driven SIGKILL automation. They are opt-in to keep the default test suite fast.

---

## SPEC Reconciliations Tracked Here

The planner reconciled 4 SPEC ambiguities/gaps into specific implementation choices. Each is documented in the relevant plan and surfaced in `docs/playbooks.md` (Plan 71-10):

1. **Bash heredoc, not Go binary** (Plan 71-05) — SPEC's `sidecars/playbook-runner/` notation is misleading; Phase 67's km-slack-inbound-poller precedent is bash heredoc embedded in `pkg/compiler/userdata.go`. Lower risk, no Makefile sidecar target, no `km init --sidecars` binary deploy.
2. **UUIDv4 prefixed `pr-`, not ULID** (Plan 71-04) — `oklog/ulid` is not in go.mod; `github.com/google/uuid` is.
3. **AWS-owned SSE on DDB tables, not platform CMK** (Plan 71-02) — Matches dynamodb-slack-threads precedent; avoids additional `kms:GenerateDataKey` IAM surface on Lambda role. SPEC's "platform CMK" intent is documented for future re-evaluation.
4. **`playbook_queue_healthy` doctor check, not `playbook_runner_service_active`** (Plan 71-09) — Operator-side systemd state observability is impossible without SCP-blocked `ssm:SendCommand`. Implementation uses SQS queue attributes (`ApproximateNumberOfMessagesNotVisible` + `LastModifiedTimestamp`) as runner-liveness proxy.

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references (Plan 71-00 ships before Wave 1+)
- [x] No watch-mode flags
- [x] Feedback latency < 60s (per-commit) / 180s (full suite)
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** pending operator sign-off after 71-UAT.md execution (Plan 71-10's checkpoint task)
