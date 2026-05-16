---
phase: 83-km-event-operator-controlled-eventbridge
plan: 06
type: execute
wave: 3
depends_on: [02, 03, 04, 05]
files_modified:
  - internal/app/cmd/at.go
  - internal/app/cmd/at_test.go
  - internal/app/cmd/init.go
  - internal/app/cmd/doctor.go
  - internal/app/cmd/event_test.go
autonomous: true
requirements:
  - operator-feature-83
must_haves:
  truths:
    - "km at --cron 'cron(...)' destroy <sb> returns 'unknown flag: --cron' (Cobra error); the cronFlag declaration + the cronFlag != '' branch in at.go are removed"
    - "km at 'every monday at 1pm' destroy <sb> errors with exit non-zero and stderr contains 'recurring expressions not supported by km at; use `km event' (per CONTEXT.md error wording)"
    - "km at '5pm tomorrow' destroy <sb> (regression — one-off, IsRecurring=false) continues to work and creates EventBridge Scheduler schedule as before"
    - "km init (full bootstrap) applies the operator-event-bus stack at infra/live/{regionLabel}/operator-event-bus/ as part of its regionalModules slice"
    - "km init --lambdas deploys km-runner.zip via the lambdaBinaries slice (decision per phase_specific_notes: yes, consistent with slack-bridge)"
    - "km doctor includes operator_event_rules_healthy check that — for every entry in cfg.Events — calls EventBridge DescribeRule and reports WARN if any rule is missing or not ENABLED; empty Events returns OK"
    - "Wave 0 tests TestAtRecurringRejected, TestAtCronFlagRemoved, TestAtOneOff, TestOperatorEventRulesHealthy_Empty, TestOperatorEventRulesHealthy_Stale all move from SKIP to PASS"
  artifacts:
    - path: "internal/app/cmd/at.go"
      provides: "km at command surface with --cron stripped and recurring NL rejected"
      contains: "recurring expressions not supported"
    - path: "internal/app/cmd/init.go"
      provides: "regionalModules includes operator-event-bus; lambdaBinaries includes km-runner"
      contains: "operator-event-bus"
    - path: "internal/app/cmd/doctor.go"
      provides: "checkOperatorEventRulesHealthy + registration in the check chain"
      contains: "operator_event_rules_healthy"
  key_links:
    - from: "internal/app/cmd/at.go (RunE)"
      to: "pkg/at/parser.go ScheduleSpec.IsRecurring"
      via: "after atpkg.Parse, if spec.IsRecurring → return error pointing at km event"
      pattern: "IsRecurring.*km event|km event.*IsRecurring"
    - from: "internal/app/cmd/init.go regionalModules"
      to: "infra/modules/operator-event-bus/v1.0.0/"
      via: "regionalModule entry referencing infra/modules//operator-event-bus/v1.0.0"
      pattern: "operator-event-bus"
    - from: "internal/app/cmd/init.go lambdaBinaries"
      to: "build/km-runner.zip"
      via: "lambdaBinary entry pointing at km-runner with the zip path"
      pattern: "km-runner"
    - from: "internal/app/cmd/doctor.go checkOperatorEventRulesHealthy"
      to: "cfg.Events + EventBridge DescribeRule API"
      via: "for-loop calling ebClient.DescribeRule for each event's rule name on the custom bus"
      pattern: "DescribeRule|cfg.Events"
---

<objective>
Three independent cleanup + glue tasks that complete the Phase 83 surface:

1. km at hard cut — strip --cron flag and reject recurring NL inputs (CONTEXT.md locked decision)
2. km init folding — add operator-event-bus to regionalModules so km init auto-applies it; add km-runner to lambdaBinaries
3. km doctor check — operator_event_rules_healthy WARNs on missing/disabled rules

Each is small and they have no cross-dependencies, so they can be implemented in any order.

Output:
- at.go: --cron stripped, recurring rejected with helpful error pointing at km event
- init.go: operator-event-bus in regionalModules, km-runner in lambdaBinaries
- doctor.go: checkOperatorEventRulesHealthy added and registered
- All 5 Wave 0 cleanup tests promoted from SKIP to PASS
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-CONTEXT.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-RESEARCH.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-VALIDATION.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-02-SUMMARY.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-03-SUMMARY.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-05-SUMMARY.md
@internal/app/cmd/at.go
@internal/app/cmd/init.go
@internal/app/cmd/doctor.go
@pkg/at/parser.go

<interfaces>
at.go current state (per 83-RESEARCH.md):
- Line 73: var cronFlag string
- Lines 119-191: cronFlag != "" branch handling raw cron
- Line 317: cmd.Flags().StringVar(&cronFlag, "cron", "", ...)
- Long help text advertises --cron

at.go target state:
- Remove the cronFlag declaration
- Remove the if cronFlag != "" branch; keep only NL-parse path
- Remove the cmd.Flags().StringVar registration
- After atpkg.Parse, add: if spec.IsRecurring return error pointing at km event
- Update help text — remove --cron example, mention km event for recurring

init.go regionalModules (line 83): add operator-event-bus entry after create-handler.
init.go lambdaBinaries (line 983): add km-runner entry.

doctor.go: add checkOperatorEventRulesHealthy function + EventRulesAPI interface (mock seam) + registration in the check chain. WARN (not ERROR) for missing/disabled rules.

Wave 0 tests to promote (live in event_test.go for cohesion):
- TestAtRecurringRejected — recurring NL errors with "km event" pointer
- TestAtCronFlagRemoved — --cron returns Cobra "unknown flag"
- TestAtOneOff — one-off NL still creates a Scheduler schedule (regression)
- TestOperatorEventRulesHealthy_Empty — empty cfg.Events → StatusOK
- TestOperatorEventRulesHealthy_Stale — mock DescribeRule returns 404 → StatusWarn, message lists stale rule name
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Strip --cron from km at; reject recurring NL with helpful error; promote 3 Wave 0 at-tests to PASS</name>
  <files>internal/app/cmd/at.go, internal/app/cmd/at_test.go, internal/app/cmd/event_test.go</files>
  <behavior>
    - Remove `var cronFlag string` declaration (line 73).
    - Remove the `if cronFlag != "" ... else ...` branching in the RunE function (lines 119-191). Keep only the normal-mode path (NL parse → ScheduleSpec).
    - Remove `cmd.Flags().StringVar(&cronFlag, "cron", "", ...)` registration (line 317).
    - Remove the `km at --cron 'cron(0 15 ? * 5 *)' destroy sb-abc123` example from the help Long text.
    - Update the help Long text: add a line "For recurring or durable schedules, use `km event` — `km at` is for one-off operations only."
    - After atpkg.Parse, add:
      ```go
      if spec.IsRecurring {
          return fmt.Errorf(
              "recurring expressions not supported by km at; use `km event '%s' %s --name <slug>` for durable schedules",
              timeExprArg, cmdArg,
          )
      }
      ```
    - Wave 0 tests in event_test.go to promote (these live in event_test.go for cohesion with Phase 83):
      - TestAtRecurringRejected: build at command via NewAtCmdWithDeps with injected fakes; SetArgs("every monday at 1pm", "destroy", "sb-abc123"); execute; assert err.Error() (or captured stderr) contains both "recurring expressions not supported" AND "km event"
      - TestAtCronFlagRemoved: SetArgs("--cron", "cron(...)", "destroy", "sb-abc123"); execute; assert err.Error() contains "unknown flag"
      - TestAtOneOff (regression): SetArgs("5pm tomorrow", "destroy", "sb-abc123") with injected mock SchedulerAPI; assert err == nil AND mock recorded a schedule creation call
    - Existing tests in at_test.go that referenced --cron must be updated or removed.

    AVOID:
    - Leaving var cronFlag string declared (dead code)
    - Wrapping recurring-rejection in a feature flag — hard cut per CONTEXT.md
    - Translating the timeExprArg differently — `km event '<when>'` form matches operator's mental model

    REFERENCES:
    - internal/app/cmd/at.go lines 73, 80-93, 119-191, 317
    - 83-RESEARCH.md "Pitfall 2"
    - CONTEXT.md Claude's Discretion: exact error message wording
  </behavior>
  <action>
    1. Read internal/app/cmd/at.go lines 60-220.
    2. Read internal/app/cmd/at_test.go (if exists) for cronFlag-related tests.
    3. Read internal/app/cmd/event_test.go to see the Wave 0 stub bodies for TestAtRecurringRejected, TestAtCronFlagRemoved, TestAtOneOff.
    4. Remove cronFlag declaration, branching, and flag registration from at.go.
    5. Add the recurring-rejection error after atpkg.Parse.
    6. Update help text Long string: remove --cron example, add note pointing at km event.
    7. Remove any obsoleted tests in at_test.go that referenced --cron.
    8. Fill the three Wave 0 tests in event_test.go (remove t.Skip, write bodies per <behavior>).
    9. Run `go build ./...`.
    10. Run `go test ./internal/app/cmd/ -run "TestAtRecurringRejected|TestAtCronFlagRemoved|TestAtOneOff" -v -count=1`. All three must PASS.
    11. Run `go test ./internal/app/cmd/... -count=1` for full at coverage — no regressions.
    12. Smoke: `make build && ./km at --help | grep -v cron` — must succeed. `./km at --cron 'cron(...)' destroy foo 2>&1 | grep "unknown flag"` exits 0.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && go build ./... && go test ./internal/app/cmd/ -run "TestAtRecurringRejected|TestAtCronFlagRemoved|TestAtOneOff" -v -count=1 2>&1 | grep -E "(--- PASS|--- FAIL|--- SKIP)" | tee /tmp/83-06-task1.out && ! grep -q "FAIL\|SKIP" /tmp/83-06-task1.out && ! grep -q "cronFlag" internal/app/cmd/at.go</automated>
  </verify>
  <done>
    `grep "cronFlag" internal/app/cmd/at.go` returns 0 matches. All three Wave 0 at-tests PASS. `./km at --help` no longer references --cron. The recurring-rejection error is reachable.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Add operator-event-bus to regionalModules and km-runner to lambdaBinaries in init.go</name>
  <files>internal/app/cmd/init.go</files>
  <behavior>
    - regionalModules (line 83): add a new entry for operator-event-bus.
      - name: "operator-event-bus"
      - source: "infra/modules//operator-event-bus/v1.0.0" (double-slash per Phase 80 memory)
      - envReqs: match what create-handler / ttl-handler already require (KM_ARTIFACTS_BUCKET, KM_OPERATOR_EMAIL, etc.)
      - Inputs (passed via the per-module terragrunt.hcl): must include lambda_zip_path pointing at build/km-runner.zip
      - Place AFTER create-handler in the slice — it consumes km-operator-policy similarly.
    - If regionalModules entries also generate a per-module live terragrunt.hcl (per init.go convention), ensure the live HCL exists at infra/live/{regionLabel}/operator-event-bus/terragrunt.hcl by following init.go's bootstrap chain.
    - lambdaBinaries (line 983): add a new entry for km-runner.
      - name: "km-runner"
      - cmdPath: "./cmd/km-runner"
      - zipName: "km-runner.zip"
      - Triggers `make build-km-runner` (from Plan 83-04) when `km init --lambdas` runs.
    - Smoke verification (NO real apply): `./km init --help` continues to work; `go test ./internal/app/cmd/ -run TestInit` continues to pass.

    AVOID:
    - Apply ordering bugs — operator-event-bus consumes km-operator-policy (shared submodule). Terraform resolves order at apply time. But km-runner.zip MUST exist before operator-event-bus applies — the Makefile chain handles this since `make build-lambdas` runs before `km init` apply.
    - Adding inputs that don't exist in the module's variables.tf (Plan 83-03 has 11 variables)
    - Breaking existing slice order — operators expect a stable apply sequence
    - Skipping inputs — all 11 module variables MUST be passed in via the auto-generated terragrunt.hcl

    REFERENCES:
    - internal/app/cmd/init.go lines 73-183 (regionalModules), 284 (apply loop), 664 (re-apply path), 983 (lambdaBinaries)
    - 83-03 SUMMARY — exact list of variables operator-event-bus needs as inputs
  </behavior>
  <action>
    1. Read internal/app/cmd/init.go around lines 73-200 to study regionalModules structure (look at create-handler + ttl-handler entries verbatim).
    2. Read init.go around line 983 to study lambdaBinaries structure.
    3. Add the new entries per <behavior>.
    4. Verify by reading what auto-generates the per-module terragrunt.hcl at infra/live/{regionLabel}/{module-name}/terragrunt.hcl — if there's a template substitution step, ensure operator-event-bus inputs include all 11 variables (resource_prefix, region_label, state_bucket, artifact_bucket_arn, artifact_bucket_name, dynamodb_table_name, dynamodb_budget_table_arn, sandbox_table_name, identities_table_name, operator_email, lambda_zip_path).
    5. Run `go build ./...`. Must succeed.
    6. Run `make build` + `./km init --help`. No regression.
    7. Run `go test ./internal/app/cmd/ -run TestInit -count=1`. No regression.

    AVOID: Running `./km init` end-to-end — that's UAT (Plan 83-07).
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && go build ./... && test $(grep -c "operator-event-bus" internal/app/cmd/init.go) -ge 1 && test $(grep -c "km-runner" internal/app/cmd/init.go) -ge 1 && go test ./internal/app/cmd/ -run TestInit -count=1 2>&1 | tail -3</automated>
  </verify>
  <done>
    init.go contains at least one reference to "operator-event-bus" and "km-runner". `go build ./...` succeeds. Existing TestInit tests continue to pass.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: Add checkOperatorEventRulesHealthy to doctor.go and promote 2 Wave 0 doctor tests to PASS</name>
  <files>internal/app/cmd/doctor.go, internal/app/cmd/event_test.go</files>
  <behavior>
    - Add EventRulesAPI interface to doctor.go (or wherever other check API interfaces live):
      ```go
      type EventRulesAPI interface {
          DescribeRule(ctx context.Context, params *eventbridgepkg.DescribeRuleInput, optFns ...func(*eventbridgepkg.Options)) (*eventbridgepkg.DescribeRuleOutput, error)
      }
      ```
    - Add helper:
      ```go
      // deriveRuleNameFromARN parses "arn:aws:events:region:account:rule/<bus>/<rule>" → "<rule>"
      func deriveRuleNameFromARN(arn string) string {
          parts := strings.Split(arn, "/")
          if len(parts) < 2 { return "" }
          return parts[len(parts)-1]
      }
      ```
    - Add checkOperatorEventRulesHealthy per the <interfaces> example.
    - Bus name derivation: cfg.ResourcePrefix + "-operator-events". If cfg.ResourcePrefix is empty, default to "km" (matches Phase 80 GetResourcePrefix fallback).
    - Register the check in doctor.go's main check chain (find where checkPresenceDaemonHealthy or checkSandboxSummary is registered and add checkOperatorEventRulesHealthy alongside, bound to cfg.Events + the EventBridge client + bus name).
    - Wave 0 tests to promote in event_test.go:
      - TestOperatorEventRulesHealthy_Empty: cfg.Events nil/empty → checkOperatorEventRulesHealthy returns CheckResult{Status: StatusOK, Name: "operator_event_rules_healthy", Message: "no event rules configured"}
      - TestOperatorEventRulesHealthy_Stale: mock EventRulesAPI returns ResourceNotFoundException for the first event, and DescribeRuleOutput{State: "DISABLED"} for the second; cfg.Events has 2 entries; assert CheckResult.Status == StatusWarn AND message contains both event names

    AVOID:
    - Returning StatusError (hard fail) instead of StatusWarn — opt-in feature can't be ERROR (matches Slack inbound check rationale)
    - Querying without the bus name — DescribeRule on a custom bus requires EventBusName explicitly (Pitfall 1 from RESEARCH.md)
    - Hardcoding the rule name format — derive from RuleARN to match what operator-event-rule module actually produces

    REFERENCES:
    - internal/app/cmd/doctor.go — find checkPresenceDaemonHealthy / checkSandboxSummary registration for the pattern
    - 83-RESEARCH.md "Doctor check pattern — mirror presence daemon check" snippet
    - eventbridgepkg = github.com/aws/aws-sdk-go-v2/service/cloudwatchevents (confirm import path in doctor.go's existing imports)
  </behavior>
  <action>
    1. Read internal/app/cmd/doctor.go to understand the check pattern (find checkPresenceDaemonHealthy or checkSandboxSummary registration site).
    2. Confirm the EventBridge SDK import path the project uses: `grep "cloudwatchevents\|eventbridge" internal/app/cmd/doctor.go internal/app/cmd/at.go pkg/aws/scheduler.go`. Most likely github.com/aws/aws-sdk-go-v2/service/cloudwatchevents OR github.com/aws/aws-sdk-go-v2/service/eventbridge.
    3. Add the EventRulesAPI interface (or extend an existing one if doctor.go has a single mega-interface for all checks).
    4. Add deriveRuleNameFromARN helper.
    5. Add checkOperatorEventRulesHealthy function per <behavior>.
    6. Register the check in the doctor check chain — find the line that adds checks to a slice or calls them sequentially, and insert checkOperatorEventRulesHealthy.
    7. Fill the two Wave 0 tests in event_test.go (remove t.Skip, write bodies). Use a local mockEventRulesAPI struct with a DescribeRule method that returns the configured response.
    8. Run `go build ./...`. Must succeed.
    9. Run `go test ./internal/app/cmd/ -run TestOperatorEventRulesHealthy -v -count=1`. Both PASS.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && go build ./... && go test ./internal/app/cmd/ -run "TestOperatorEventRulesHealthy" -v -count=1 2>&1 | grep -E "(--- PASS|--- FAIL|--- SKIP)" | tee /tmp/83-06-task3.out && ! grep -q "FAIL\|SKIP" /tmp/83-06-task3.out && grep -c "checkOperatorEventRulesHealthy" internal/app/cmd/doctor.go</automated>
  </verify>
  <done>
    doctor.go contains checkOperatorEventRulesHealthy (grep returns at least 1). Both Wave 0 doctor tests (TestOperatorEventRulesHealthy_Empty, TestOperatorEventRulesHealthy_Stale) PASS. No SKIPs remain among the 5 Wave 0 tests this plan owned.
  </done>
</task>

</tasks>

<verification>
- `go build ./...` exits 0
- `go vet ./...` exits 0
- `go test ./internal/app/cmd/... -count=1` exits 0; no SKIPs remain in Phase-83-owned tests
- `grep cronFlag internal/app/cmd/at.go` returns 0 matches
- `grep operator-event-bus internal/app/cmd/init.go` returns at least 1
- `grep km-runner internal/app/cmd/init.go` returns at least 1
- `grep checkOperatorEventRulesHealthy internal/app/cmd/doctor.go` returns at least 1
- `./km at --help` no longer mentions --cron
- `./km at --cron foo bar` returns "unknown flag: --cron"
</verification>

<success_criteria>
- km at hard cut applied: --cron stripped, recurring NL rejected with helpful error
- km init folds in operator-event-bus (regional bootstrap) and km-runner (lambda binary)
- km doctor has operator_event_rules_healthy check that WARNs on missing/disabled rules
- All 5 Wave 0 cleanup tests (TestAtRecurringRejected, TestAtCronFlagRemoved, TestAtOneOff, TestOperatorEventRulesHealthy_Empty, TestOperatorEventRulesHealthy_Stale) PASS
- Phase 83 functional surface is complete; Plan 83-07 covers docs + UAT
</success_criteria>

<output>
After completion, create `.planning/phases/83-km-event-operator-controlled-eventbridge/83-06-SUMMARY.md` documenting:
- The exact recurring-rejection error string for Plan 83-07 docs to reference
- The exact EventBridge SDK import path used (cloudwatchevents vs eventbridge — they're different)
- The deriveRuleNameFromARN helper's exact behavior so future plans can reuse it
- Whether init.go regionalModules auto-generates per-module terragrunt.hcl (if yes, document the inputs map; if no, document the manual write step)
- Where in doctor.go's check chain checkOperatorEventRulesHealthy was registered (line number for follow-up plan references)
</output>
