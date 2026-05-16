---
phase: 83-km-event-operator-controlled-eventbridge
plan: 01
type: execute
wave: 0
depends_on: []
files_modified:
  - pkg/events/manifest_test.go
  - pkg/events/parser_test.go
  - pkg/events/testdata/.gitkeep
  - internal/app/cmd/event_test.go
  - internal/app/cmd/event_e2e_test.go
  - cmd/km-runner/main_test.go
  - internal/app/config/config_events_test.go
autonomous: true
requirements:
  - operator-feature-83
must_haves:
  truths:
    - "Test files exist on disk with skeleton Test* functions and t.Skip bodies — they compile clean, do not run real logic"
    - "All test function names referenced in 83-VALIDATION.md's Per-Task Verification Map exist in the corresponding test files"
    - "mockEventRunner type is declared in event_test.go (Plan/Apply/Destroy/Reconfigure/Output methods) so Plan 83-05's runner seam has a test double ready"
    - "Fixture directory pkg/events/testdata/ exists with at least .gitkeep so go test ./pkg/events/... -v does not error on missing dir lookup"
    - "Wave 1+ tasks can cite the exact go test commands from 83-VALIDATION.md without hitting 'no tests to run' or build failure"
  artifacts:
    - path: "pkg/events/manifest_test.go"
      provides: "Skeletons for TestManifestValidation (schedule/event_pattern XOR, target-type discriminator), TestManifestYAMLRoundTrip"
      contains: "func TestManifestValidation"
    - path: "pkg/events/parser_test.go"
      provides: "Skeleton for TestAtExprToCronForRules (one-off at()→cron conversion edge cases)"
      contains: "func TestAtExprToCronForRules"
    - path: "internal/app/cmd/event_test.go"
      provides: "Skeletons for TestEventAdd_Idempotent / DryRun / Apply / PersistFailure, TestEventRm_DryRun / NotFound, TestGenerateEventHCL, TestPersistEvents, TestAtRecurringRejected, TestAtCronFlagRemoved, TestOperatorEventRulesHealthy_Empty / Stale + mockEventRunner"
      contains: "type mockEventRunner"
    - path: "internal/app/cmd/event_e2e_test.go"
      provides: "Skeleton for TestEventE2E (mock terragrunt.Runner full flow)"
      contains: "func TestEventE2E"
    - path: "cmd/km-runner/main_test.go"
      provides: "Skeletons for TestKMRunner_ValidCommand, TestKMRunner_MissingCommand"
      contains: "func TestKMRunner_ValidCommand"
    - path: "internal/app/config/config_events_test.go"
      provides: "Skeleton for TestEventsConfigRoundTrip (km-config.yaml events: round-trip)"
      contains: "func TestEventsConfigRoundTrip"
  key_links:
    - from: "internal/app/cmd/event_test.go"
      to: "internal/app/cmd/cluster_test.go"
      via: "mirrors mockClusterRunner pattern; same package cmd_test, same test-function naming conventions"
      pattern: "mockEventRunner|package cmd_test"
---

<objective>
Establish the Wave 0 Nyquist test infrastructure required by 83-VALIDATION.md before any production code lands.
Plans 83-02 (foundations), 83-04 (Lambda), 83-05 (CLI), and 83-06 (cleanup) cite go test commands in their
`<automated>` blocks; those commands need real test files to target.

Purpose: Eliminate "MISSING — Wave 0 must create test_file first" stubs from Wave 1/2/3 verification commands.
Make `go test` exit non-zero ONLY when implementation regresses, not because tests don't compile.

Output: Six new `*_test.go` files (plus a testdata directory marker) containing function skeletons with
`t.Skip("pending implementation — Plan 83-XX")` bodies, and a reusable `mockEventRunner` (with Plan/Apply/
Destroy/Reconfigure/Output methods) so Plan 83-05's `EventRunner` interface has a test double ready.

These scaffolds become the contract that Plans 83-02, 83-04, 83-05, and 83-06 must satisfy.
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
@internal/app/cmd/cluster_test.go
@internal/app/config/config.go

<interfaces>
<!-- Mirror Phase 80's mockClusterRunner pattern (cluster.go line 40-50, cluster_test.go).
     Plan 83-05's cmd/event.go will declare:
       type EventRunner interface {
         Plan(ctx context.Context, dir string) error
         Apply(ctx context.Context, dir string) error
         Destroy(ctx context.Context, dir string) error
         Reconfigure(ctx context.Context, dir string) error
         Output(ctx context.Context, dir string) (map[string]interface{}, error)
       }
       var NewEventRunnerFunc = func(profile, repoRoot string) EventRunner { ... }
       var PersistEventsConfigFunc = func(events []config.EventConfig) error { ... }

     Plan 83-02's pkg/events/manifest.go will declare:
       type EventManifest struct {
         Name         string   `yaml:"name"`
         Schedule     string   `yaml:"schedule,omitempty"`     // cron(...) or rate(...) — exactly one of {Schedule, EventPattern}
         EventPattern string   `yaml:"event_pattern,omitempty"`
         Target       Target   `yaml:"target"`
         Input        string   `yaml:"input,omitempty"`
         DLQ          string   `yaml:"dlq,omitempty"`
         Description  string   `yaml:"description,omitempty"`
         Archive      *bool    `yaml:"archive,omitempty"`      // default true; *bool to distinguish unset from false
       }
       type Target struct {
         Type    string   `yaml:"type"`              // "km" or "arn"
         Command string   `yaml:"command,omitempty"` // for type:km
         ARNs    []string `yaml:"arns,omitempty"`    // for type:arn
       }
       func (m EventManifest) Validate() error
       func ParseManifest(data []byte) (EventManifest, error)
       func SchemaJSON() []byte

     Plan 83-02's pkg/events/parser.go will declare:
       func AtExprToCronForRules(atExpr string) (string, error)
         // "at(2026-05-20T14:30:00)" → "cron(30 14 20 5 ? 2026)"

     Plan 83-02's internal/app/config/config.go will gain:
       type EventConfig struct {
         Name       string   `mapstructure:"name"        yaml:"name"`
         Type       string   `mapstructure:"type"        yaml:"type"`        // "schedule" or "pattern"
         Expression string   `mapstructure:"expression"  yaml:"expression"`
         TargetType string   `mapstructure:"target_type" yaml:"target_type"` // "km" or "arn"
         Targets    []string `mapstructure:"targets"     yaml:"targets"`
         RuleARN    string   `mapstructure:"rule_arn"    yaml:"rule_arn"`
         RoleARN    string   `mapstructure:"role_arn"    yaml:"role_arn"`
       }
       Config struct gains: Events []EventConfig `mapstructure:"events" yaml:"events"`
-->
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Create pkg/events/ test scaffolds (manifest_test.go, parser_test.go) and testdata/ directory</name>
  <files>pkg/events/manifest_test.go, pkg/events/parser_test.go, pkg/events/testdata/.gitkeep</files>
  <behavior>
    - mkdir -p pkg/events/testdata/ and touch pkg/events/testdata/.gitkeep
    - pkg/events/manifest_test.go declares `package events_test` (or `package events` if internal tests are needed for unexported helpers — prefer `_test`):
      - TestManifestValidation: t.Skip("pending — Plan 83-02 wires pkg/events/manifest.go"). Comment block enumerates cases the test will exercise: (a) valid schedule+km, (b) valid schedule+arn, (c) valid event_pattern+arn, (d) invalid: both schedule and event_pattern set, (e) invalid: neither set, (f) invalid: target.type=km but arns set, (g) invalid: target.type=arn but command set, (h) invalid name (uppercase or whitespace; must be kebab-case slug).
      - TestManifestYAMLRoundTrip: t.Skip("pending — Plan 83-02"). Comment block enumerates: parse a YAML manifest into EventManifest, marshal back, parse again, assert deep equality on all fields including the Archive *bool.
    - pkg/events/parser_test.go declares `package events_test`:
      - TestAtExprToCronForRules: t.Skip("pending — Plan 83-02 wires AtExprToCronForRules"). Comment block enumerates cases:
        - "at(2026-05-20T14:30:00)" → "cron(30 14 20 5 ? 2026)"
        - "at(2026-12-31T23:59:00)" → "cron(59 23 31 12 ? 2026)"
        - "at(2027-01-01T00:00:00)" → "cron(0 0 1 1 ? 2027)"
        - "at(2028-02-29T12:00:00)" (leap day) → "cron(0 12 29 2 ? 2028)"
        - Invalid input "rate(5 minutes)" → error containing "expected at(...)"
        - Invalid input "at(not-a-date)" → error containing "cannot parse"
    - Each test body STARTS with `t.Skip("pending implementation — Plan 83-02")`
    - File has a `// Wave 0 scaffold — see Plan 83-02 for implementations` package-level comment.

    AVOID: Implementing real test bodies before Plan 83-02 lands the production code (t.Skip prevents Wave 0 from going red against vapour).
    AVOID: Putting fixtures inline in test bodies — Plan 83-02 will reference pkg/events/testdata/ fixtures via filepath.Join. The .gitkeep ensures the dir exists in git.

    REFERENCES:
    - internal/app/cmd/cluster_test.go — mirror the cmd_test package and t.Skip pattern verbatim
    - pkg/profile/schema_test.go (if exists) — mirror the //go:embed schema pattern Plan 83-02 will use
  </behavior>
  <action>
    1. Run `mkdir -p pkg/events/testdata` and `touch pkg/events/testdata/.gitkeep`.
    2. Create pkg/events/manifest_test.go with package events_test, the two test skeletons (TestManifestValidation, TestManifestYAMLRoundTrip), each body `t.Skip("pending implementation — Plan 83-02")` followed by enumerated comment block.
    3. Create pkg/events/parser_test.go with package events_test, the TestAtExprToCronForRules skeleton, body `t.Skip("pending implementation — Plan 83-02")` followed by the 6-case comment block.
    4. Run `go build ./pkg/events/... 2>&1 || true` — expect "no Go files in pkg/events" (acceptable: test-only package at Wave 0 close until Plan 83-02 adds manifest.go).
    5. Run `go test ./pkg/events/... -v` — expect tests to run with all SKIP statuses, exit code 0. If Go errors on "no non-test Go files", add a stub `pkg/events/doc.go` containing only `// Package events defines the EventManifest schema for km event ...\npackage events\n` to satisfy the compiler.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && go test ./pkg/events/... -v 2>&1 | grep -E "(PASS|SKIP|FAIL|TestManifestValidation|TestManifestYAMLRoundTrip|TestAtExprToCronForRules)" && test -d pkg/events/testdata</automated>
  </verify>
  <done>
    `go test ./pkg/events/... -v` exits 0 and shows TestManifestValidation, TestManifestYAMLRoundTrip, TestAtExprToCronForRules all with SKIP status. `test -d pkg/events/testdata` exits 0. `go vet ./pkg/events/...` is clean.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Create internal/app/cmd/event_test.go + event_e2e_test.go with full test surface + mockEventRunner</name>
  <files>internal/app/cmd/event_test.go, internal/app/cmd/event_e2e_test.go</files>
  <behavior>
    - Both files declare `package cmd_test` (matches cluster_test.go convention).
    - event_test.go declares a `mockEventRunner` struct that mirrors mockClusterRunner exactly — fields PlanCalled bool, Applied []string, Destroyed []string, Reconfigured []string, OutputCalled bool, OutputResult map[string]interface{}, ApplyErr error. Methods Plan / Apply / Destroy / Reconfigure / Output are stubs that record the call. This struct will be the test double Plan 83-05's EventRunner interface satisfies.
    - event_test.go contains skeletons for ALL these test functions (each body `t.Skip("pending implementation — Plan 83-XX")`):
      - TestEventAdd_Idempotent (Plan 83-05): name already in cfg.Events → no-op, prints existing rule ARN, returns nil; mock.Applied stays empty
      - TestEventAdd_DryRun (Plan 83-05): dryRun=true → mock.Plan called, mock.Apply NOT called, no state mutation
      - TestEventAdd_Apply (Plan 83-05): dryRun=false → mock.Apply + mock.Output called, rule_arn appears in cfg.Events[0].RuleARN, PersistEventsConfigFunc invoked
      - TestEventAdd_PersistFailure (Plan 83-05): apply succeeds, PersistEventsConfigFunc returns error → RunEventAdd returns non-nil error whose message contains "km event rm", mock.Destroyed stays empty (no auto-destroy)
      - TestEventRm_DryRun (Plan 83-05): dryRun=true → mock.Plan called, mock.Destroy NOT called
      - TestEventRm_NotFound (Plan 83-05): name not in cfg.Events → returns error containing "not found"
      - TestGenerateEventHCL (Plan 83-05): substitutes {RULE_NAME}, {SCHEDULE}, {TARGET_TYPE}, {COMMAND}, {EVENT_BUS_NAME} placeholders correctly; no literal "{...}" markers remain
      - TestPersistEvents (Plan 83-05): writes temp km-config.yaml with existing `domain: example.com` key, calls PersistEventsConfig with two events, re-reads file, asserts domain: still present AND events: list has two entries (preserves other keys)
      - TestAtRecurringRejected (Plan 83-06): `km at 'every monday at 1pm' destroy <sb>` → exit non-zero, stderr contains "km event" and "recurring expressions not supported"
      - TestAtCronFlagRemoved (Plan 83-06): `km at --cron 'cron(...)' destroy <sb>` → cobra returns "unknown flag: --cron"
      - TestAtOneOff (Plan 83-06): `km at '5pm tomorrow' destroy <sb>` regression test — still creates EventBridge Scheduler schedule successfully
      - TestOperatorEventRulesHealthy_Empty (Plan 83-06): cfg.Events empty → CheckResult.Status == OK
      - TestOperatorEventRulesHealthy_Stale (Plan 83-06): mock DescribeRule returns ResourceNotFoundException for one of two rules → CheckResult.Status == WARN, message lists the stale rule name
    - event_e2e_test.go declares TestEventE2E: t.Skip("pending — Plan 83-05 wires full flow"). Comment block enumerates: temp dir as repoRoot, mock EventRunner whose Apply records the stackDir, mock PersistEventsConfigFunc, call RunEventAdd → verify both seams called in correct order → call RunEventRm → verify destroy + persist called → assert km-config.yaml content has events: list mutate correctly.
    - Each test body STARTS with `t.Skip("pending implementation — Plan 83-XX")` — XX = 02/04/05/06 as appropriate.
    - File has a `// Wave 0 scaffold — see Plans 83-05 (CLI) and 83-06 (km at cleanup + doctor) for implementations` package-level comment.

    AVOID: Asserting interface satisfaction at compile time (Plan 83-05 will declare EventRunner; Wave 0 mockEventRunner is just a struct with methods until then).
    AVOID: Importing pkg/events at Wave 0 — Plan 83-02 creates the types. Tests that need types use stub locals like `type fakeManifest struct{}`.
    AVOID: Mutating cluster_test.go's mockClusterRunner — declare mockEventRunner as a separate local type to keep cluster tests untouched.

    REFERENCES:
    - internal/app/cmd/cluster_test.go — verbatim mock pattern (lines declaring mockClusterRunner, its Plan/Apply/Destroy/Reconfigure/Output methods)
    - internal/app/cmd/at_test.go (if exists) — for the cobra invocation pattern in TestAtCronFlagRemoved
  </behavior>
  <action>
    1. Read internal/app/cmd/cluster_test.go fully to confirm mockClusterRunner signature.
    2. Read internal/app/cmd/at.go to understand the cronFlag declaration site (line 73) — TestAtCronFlagRemoved will assert this flag's absence after Plan 83-06.
    3. Create internal/app/cmd/event_test.go with package cmd_test, the mockEventRunner struct (fields + 5 method stubs), and 13 test function skeletons. Each test body is `t.Skip("pending implementation — Plan 83-XX")` with the plan number from the <behavior> list.
    4. For TestEventAdd_PersistFailure, include in the comment block:
       - "Inject NewEventRunnerFunc returning mock whose Apply returns nil and Output returns rule_arn + role_arn"
       - "Inject PersistEventsConfigFunc returning fmt.Errorf(\"disk full\") via t.Cleanup"
       - "Call RunEventAdd with dryRun:false"
       - "Assert err != nil AND strings.Contains(err.Error(), \"km event rm\") AND len(mock.Destroyed) == 0"
    5. Create internal/app/cmd/event_e2e_test.go with package cmd_test and TestEventE2E skeleton.
    6. Run `go build ./internal/app/cmd/...` — must compile clean.
    7. Run `go test ./internal/app/cmd/ -run TestEvent -v` and confirm all 13 event tests + TestEventE2E appear with --- SKIP status.
    8. Also confirm TestAtRecurringRejected, TestAtCronFlagRemoved, TestAtOneOff appear (these test the km at surface but live in event_test.go for cohesion with the Phase 83 plan).
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && go test ./internal/app/cmd/ -run "TestEvent|TestAtRecurringRejected|TestAtCronFlagRemoved|TestAtOneOff|TestOperatorEventRulesHealthy|TestGenerateEventHCL|TestPersistEvents" -v 2>&1 | grep -E "(SKIP|FAIL|TestEventAdd|TestEventRm|TestEventE2E|TestAtRecurringRejected|TestAtCronFlagRemoved|TestAtOneOff|TestOperatorEventRulesHealthy|TestGenerateEventHCL|TestPersistEvents)" && grep -c "mockEventRunner" internal/app/cmd/event_test.go</automated>
  </verify>
  <done>
    `go test ./internal/app/cmd/ -run TestEvent -v` exits 0 and shows all 14 Event-prefixed test names with SKIP status. `grep -c "t.Skip" internal/app/cmd/event_test.go` returns at least 13. `grep -c "mockEventRunner" internal/app/cmd/event_test.go` returns at least 6 (struct decl + 5 method receivers). `go vet ./internal/app/cmd/...` is clean.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: Create cmd/km-runner/main_test.go and internal/app/config/config_events_test.go</name>
  <files>cmd/km-runner/main_test.go, internal/app/config/config_events_test.go</files>
  <behavior>
    - cmd/km-runner/main_test.go declares `package main` (matches cmd/create-handler/main_test.go pattern — runs as part of the binary's package):
      - TestKMRunner_ValidCommand: t.Skip("pending — Plan 83-04 wires km-runner Handle"). Comment block enumerates: build events.CloudWatchEvent with Detail = `{"command":"doctor --all-regions"}`, call Handle, assert nil error, stdout contains the exec'd command name.
      - TestKMRunner_MissingCommand: t.Skip("pending — Plan 83-04"). Comment block: Detail = `{}` (missing command field) → Handle returns error containing "command".
    - internal/app/config/config_events_test.go declares `package config_test` (matches cluster_test.go convention):
      - TestEventsConfigRoundTrip: t.Skip("pending — Plan 83-02 adds Events field + viper merge key"). Comment block enumerates: write temp km-config.yaml with `events:` list containing one EventConfig entry, load via config.Load(), assert cfg.Events[0].Name == "nightly-doctor" and cfg.Events[0].RuleARN starts with "arn:aws:events:".
    - Both files start each test body with `t.Skip("pending — Plan 83-XX")`.

    AVOID: Importing events package from km-runner tests (km-runner consumes its own input struct; doesn't depend on pkg/events).
    AVOID: Bypassing config.Load() public surface in the config_test (the test must exercise the same code path operators hit).

    REFERENCES:
    - cmd/create-handler/main_test.go (if exists) — mirror the events.CloudWatchEvent stub construction pattern
    - internal/app/config/config_clusters_test.go (from Phase 80) — mirror the temp km-config.yaml fixture pattern exactly
  </behavior>
  <action>
    1. Check whether cmd/create-handler/ has a *_test.go file: `ls cmd/create-handler/*_test.go`. If it exists, mirror its package + import shape.
    2. Check internal/app/config/config_clusters_test.go (Phase 80 wave 0 artifact): `cat internal/app/config/config_clusters_test.go`. Use this as the verbatim template for config_events_test.go.
    3. Create cmd/km-runner/ directory if missing: `mkdir -p cmd/km-runner`.
    4. Create cmd/km-runner/main_test.go with package main, imports for testing + github.com/aws/aws-lambda-go/events, and the two test skeletons.
    5. Create internal/app/config/config_events_test.go with package config_test (or whichever package config_clusters_test.go uses), and TestEventsConfigRoundTrip skeleton.
    6. Run `go test ./cmd/km-runner/... -v` — expect "no Go files in cmd/km-runner" (acceptable: test-only at Wave 0 close until Plan 83-04 adds main.go). If Go errors on this, add a stub `cmd/km-runner/doc.go` containing `// Package main is the km-runner Lambda handler.\npackage main\n` to satisfy the compiler.
    7. Run `go test ./internal/app/config/ -run TestEventsConfigRoundTrip -v` — expect SKIP status.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && go test ./cmd/km-runner/... -v 2>&1 | grep -E "(SKIP|FAIL|TestKMRunner_ValidCommand|TestKMRunner_MissingCommand)" && go test ./internal/app/config/ -run TestEventsConfigRoundTrip -v 2>&1 | grep -E "(SKIP|FAIL|TestEventsConfigRoundTrip)"</automated>
  </verify>
  <done>
    `go test ./cmd/km-runner/... -v` exits 0 and shows TestKMRunner_ValidCommand + TestKMRunner_MissingCommand with SKIP status. `go test ./internal/app/config/ -run TestEventsConfigRoundTrip -v` exits 0 and shows TestEventsConfigRoundTrip with SKIP status. `go vet ./cmd/km-runner/... ./internal/app/config/...` is clean.
  </done>
</task>

</tasks>

<verification>
- `go build ./...` exits 0 from repo root
- `go test ./pkg/events/... -v` exits 0, shows 3 SKIP entries (TestManifestValidation, TestManifestYAMLRoundTrip, TestAtExprToCronForRules)
- `go test ./internal/app/cmd/ -run TestEvent -v` exits 0, shows 13 SKIP entries (Event* + AtRecurringRejected + AtCronFlagRemoved + AtOneOff + OperatorEventRulesHealthy_*)
- `go test ./cmd/km-runner/... -v` exits 0, shows 2 SKIP entries
- `go test ./internal/app/config/ -run TestEventsConfigRoundTrip -v` exits 0, shows 1 SKIP entry
- `mockEventRunner` type exists in event_test.go with Plan/Apply/Destroy/Reconfigure/Output methods
- `pkg/events/testdata/` directory exists on disk
- Wave 1/2/3 plans can cite all the go test commands without "no tests to run" / build failure
</verification>

<success_criteria>
- All six test files exist with described skeletons.
- All ~20 test functions present (3 events + 14 cmd + 2 km-runner + 1 config) with t.Skip bodies.
- mockEventRunner scaffold in event_test.go exposes Plan/Apply/Destroy/Reconfigure/Output methods.
- Compile clean; no build regression in `go build ./...`.
- Update `.planning/phases/83-km-event-operator-controlled-eventbridge/83-VALIDATION.md` — set `wave_0_complete: true` in frontmatter (single edit; no logic change).
</success_criteria>

<output>
After completion, create `.planning/phases/83-km-event-operator-controlled-eventbridge/83-01-SUMMARY.md` documenting:
- The exact mockEventRunner struct fields and method signatures so Plan 83-05 can wire it via NewEventRunnerFunc without re-reading this file
- Test function signatures for all 20 scaffolded tests so Plans 83-02/83-04/83-05/83-06 know the contracts
- Whether pkg/events/doc.go and cmd/km-runner/doc.go stubs were needed (and which packages now compile clean)
</output>
</content>
