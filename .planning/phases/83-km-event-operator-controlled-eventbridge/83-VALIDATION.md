---
phase: 83
slug: km-event-operator-controlled-eventbridge
status: planned
nyquist_compliant: true
wave_0_complete: false
created: 2026-05-16
plans_created: 2026-05-16
---

# Phase 83 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (existing project standard) |
| **Config file** | none (Go modules) |
| **Quick run command** | `go test ./pkg/events/... ./internal/app/cmd/... -count=1 -run 'Test83'` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~45-90 seconds full suite, ~5s quick |

---

## Sampling Rate

- **After every task commit:** Run quick command for the package touched
- **After every plan wave:** Run full suite
- **Before `/gsd:verify-work`:** Full suite green + manual operator smoke test
- **Max feedback latency:** ~10 seconds per package

---

## Per-Task Verification Map

Filled by gsd-planner. Anchored on the test surfaces and mapped to plan Task IDs.

| Surface | Task ID | Wave | Test Type | Automated Command |
|---|---|---|---|---|
| `pkg/events/manifest.go` schema validation | 83-02-T1 | 1 | unit | `go test ./pkg/events/... -run TestManifestValidation` |
| `pkg/events/manifest.go` YAML round-trip | 83-02-T1 | 1 | unit | `go test ./pkg/events/... -run TestManifestYAMLRoundTrip` |
| `pkg/events/parser.go` NL → cron conversion (incl. `at()` → cron equivalent) | 83-02-T1 | 1 | unit | `go test ./pkg/events/... -run TestAtExprToCronForRules` |
| `infra/modules/operator-event-bus/v1.0.0/` `terraform validate` | 83-03-T1 | 1 | static | `cd infra/modules/operator-event-bus/v1.0.0 && terraform init -backend=false && terraform validate` |
| `infra/modules/operator-event-rule/v1.0.0/` `terraform validate` | 83-03-T2 | 1 | static | `cd infra/modules/operator-event-rule/v1.0.0 && terraform init -backend=false && terraform validate` |
| `cmd/km-runner/main.go` builds + handler unit tests | 83-04-T1 | 2 | unit | `go test ./cmd/km-runner/... -count=1` |
| `cmd/km-runner` zip artifact builds | 83-04-T2 | 2 | build | `make build-km-runner` |
| `internal/app/cmd/event.go` add/list/rm/apply table tests | 83-05-T2 | 2 | unit | `go test ./internal/app/cmd/... -run TestEvent` |
| `internal/app/cmd/event_e2e_test.go` end-to-end with mock terragrunt | 83-05-T2 | 2 | integration | `go test ./internal/app/cmd/... -run TestEventE2E` |
| `internal/app/cmd/event.go` registered in root.go (help-text smoke) | 83-05-T3 | 2 | smoke | `./km event --help` exits 0 |
| `internal/app/cmd/at.go` cron-flag-removed regression | 83-06-T1 | 3 | unit | `go test ./internal/app/cmd/... -run TestAtCronFlagRemoved` |
| `internal/app/cmd/at.go` recurring NL rejected | 83-06-T1 | 3 | unit | `go test ./internal/app/cmd/... -run TestAtRecurringRejected` |
| `internal/app/cmd/at.go` existing one-off behavior unchanged | 83-06-T1 | 3 | unit | `go test ./internal/app/cmd/... -run TestAtOneOff` |
| `pkg/at/parser.go` (shared) — unchanged behavior | 83-06-T1 | 3 | unit | `go test ./pkg/at/... -count=1` |
| `infra/modules/km-operator-policy/v1.0.0/` updated `EventBridgePutEvents` scope | 83-02-T3 | 1 | static | `cd infra/modules/km-operator-policy/v1.0.0 && terraform init -backend=false && terraform validate` |
| `internal/app/cmd/doctor.go` `operator_event_rules_healthy` check | 83-06-T3 | 3 | unit | `go test ./internal/app/cmd/... -run TestOperatorEventRulesHealthy` |
| `internal/app/config/config.go` `events:` YAML round-trip | 83-02-T2 | 1 | unit | `go test ./internal/app/config/... -run TestEventsConfigRoundTrip` |
| `internal/app/cmd/init.go` regionalModules + lambdaBinaries gained Phase 83 entries | 83-06-T2 | 3 | smoke | `grep -c "operator-event-bus\|km-runner" internal/app/cmd/init.go` |
| CLAUDE.md + docs/operator-events.md exist | 83-07-T1, T2 | 4 | static | `test -f docs/operator-events.md && grep -c "Operator-controlled EventBridge" CLAUDE.md` |
| End-to-end UAT against real AWS | 83-07-T3 | 4 | manual | See Manual-Only Verifications section |
| STATE.md + ROADMAP.md closeout | 83-07-T4 | 4 | static | `grep -c "Phase 83 — km event" .planning/STATE.md` |

*Status: pending · green · red · flaky*

---

## Wave 0 Requirements

- [ ] `pkg/events/manifest_test.go` — stubs for schema validation, YAML round-trip, target-type discriminator (Plan 83-01 Task 1)
- [ ] `pkg/events/parser_test.go` — stubs for at()→cron-for-rules conversion edge cases (UTC, DST boundary, leap day, EventBridge `?` day-of-week vs day-of-month constraint) (Plan 83-01 Task 1)
- [ ] `internal/app/cmd/event_test.go` — stubs for add/list/rm/apply CLI surface (Plan 83-01 Task 2)
- [ ] `internal/app/cmd/event_e2e_test.go` — stubs with mock `terragrunt.Runner` (mirror `internal/app/cmd/cluster_test.go` pattern from Phase 80) (Plan 83-01 Task 2)
- [ ] `cmd/km-runner/main_test.go` — stubs for event input parsing, `km` exec, stdout/stderr capture, error handling (Plan 83-01 Task 3)
- [ ] `internal/app/config/config_events_test.go` — stubs for EventConfig YAML round-trip (Plan 83-01 Task 3)
- [ ] Fixture corpus at `pkg/events/testdata/` — valid manifests (schedule+km, schedule+arn, pattern+arn, at-style one-off, with-DLQ), invalid manifests (missing required, conflicting target type, recurring under km at), and the EventBridge-rules-only constraint cases (at-style → cron conversion fixtures) (Plan 83-02 Task 1)

*Existing infrastructure: `pkg/at/parser_test.go` covers NL parsing — no Wave 0 work needed there. `internal/app/cmd/cluster_test.go` is the table-driven test pattern mirrored for `event_test.go`.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end `km event add 'every monday at 1pm' doctor` against real AWS | (locked decision: matches Phase 80 km cluster add behavior) | Requires AWS credentials + region + terragrunt state; not safe in CI | Run with `--dry-run=false` against `klanker-application` profile in operator's dev region; verify rule exists in EventBridge console, ENABLED, target = km-runner; trigger manually via `aws events put-events` or wait for cron fire; check CloudWatch logs for `km-runner` Lambda showing `doctor --all-regions` output |
| `km event rm <name>` cleanly destroys rule + prunes config | (locked decision) | Same — requires real terragrunt state | After above, run `km event rm <name>`; verify rule gone from EventBridge console; verify entry gone from `km-config.yaml`; run twice to confirm idempotency |
| Archive + replay window works | (locked decision: archive on by default) | Archive verification requires waiting for ingest + console inspection | After 1+ rules exist, check EventBridge console → Archives; confirm `{prefix}-operator-events-archive` exists, retention 7 days, source bus is the custom bus |
| `km doctor` `operator_event_rules_healthy` detects drift | (locked decision) | Requires manually drifting state (delete rule via AWS console while leaving config entry) | After at least one rule exists, delete it manually via `aws events delete-rule`; run `km doctor`; verify WARN line citing the drifted rule name |
| `km at '5pm tomorrow' destroy <sb>` still works (regression) | (locked decision: km at one-off unchanged) | Real AWS schedule needs ~24h to fire, OR use `at 1 minute from now` for tight test | Smoke: `km at 'in 2 minutes' destroy <test-sandbox>` then `km at list`; wait + verify destroy fired |
| `km at --cron '...'` errors with pointer at `km event` | (locked decision: hard cut) | Confirms CLI surface change | Run `km at --cron 'cron(0 9 * * ? *)' destroy <sb>`; verify exit code non-zero + stderr contains "km event" |
| `km at 'every monday'` (recurring NL) errors with pointer at `km event` | (locked decision: hard cut) | CLI surface change | Run `km at 'every monday at 1pm' destroy <sb>`; verify exit code non-zero + stderr "use km event" |

UAT walkthrough lives in Plan 83-07 Task 3 (the human-verify checkpoint). 12 steps covering install → add → list → rule fire → drift detect → rm → regression.

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 10s per package
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** approved by planner 2026-05-16 — 7 plans (83-01..83-07), 4 waves, 19 unit/static tests + 1 e2e + 12 UAT steps mapped to Task IDs.
