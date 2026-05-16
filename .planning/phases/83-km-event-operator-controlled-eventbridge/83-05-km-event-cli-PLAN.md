---
phase: 83-km-event-operator-controlled-eventbridge
plan: 05
type: execute
wave: 2
depends_on: [01, 02, 03]
files_modified:
  - internal/app/cmd/event.go
  - internal/app/cmd/event_test.go
  - internal/app/cmd/event_e2e_test.go
  - internal/app/cmd/root.go
autonomous: true
requirements:
  - operator-feature-83
must_haves:
  truths:
    - "`km event --help` prints add/list/rm/apply subcommands"
    - "`km event add --help` documents all flags: --name, --target-km, --target (repeatable), --input, --dlq, --description, --no-archive, --event-pattern, --hcl, --aws-profile, --region, --verbose, --dry-run (default true), and the positional NL time expression argument"
    - "`km event add 'every monday at 1pm' doctor --name nightly-doctor` with --dry-run=true runs runner.Plan only (no state mutation), prints 'plan complete — re-run with --dry-run=false'"
    - "`km event add 'every monday at 1pm' doctor --name nightly-doctor --dry-run=false` runs runner.Apply, captures rule_arn/role_arn outputs, appends to cfg.Events, persists km-config.yaml"
    - "Idempotency: re-running `km event add --name foo ...` when foo exists in cfg.Events prints existing rule ARN and exits 0 without re-applying"
    - "Rollback contract: when PersistEventsConfigFunc fails after a successful Apply, RunEventAdd returns an error containing 'km event rm <name>' and does NOT call runner.Destroy"
    - "One-off NL expressions like `km event add '5pm tomorrow' doctor` convert via AtExprToCronForRules to pinned-year cron(...) before terraform inputs are written"
    - "`km event apply events/<name>.yaml` reads a manifest from disk, validates via pkg/events.Validate(), generates the per-rule terragrunt.hcl, applies it"
    - "`km event list` reads cfg.Events and prints a tabwriter table with NAME / TYPE / EXPRESSION / TARGET / RULE ARN columns"
    - "`km event rm <name>` runs runner.Destroy on the rule's stack dir, prunes cfg.Events, persists km-config.yaml; idempotent (errors with 'not found' if the name is missing)"
    - "ALL Wave 0 cmd_test.go event tests (TestEventAdd_Idempotent, TestEventAdd_DryRun, TestEventAdd_Apply, TestEventAdd_PersistFailure, TestEventRm_DryRun, TestEventRm_NotFound, TestGenerateEventHCL, TestPersistEvents, TestEventE2E) move from SKIP to PASS"
    - "ExportConfigEnvVars(cfg) is called BEFORE any runner.Apply / runner.Plan invocation (matches cluster.go pattern; prevents 403 HeadBucket on non-default resource_prefix installs per memory project_terragrunt_env_export)"
    - "region.hcl is bootstrapped if missing (verbatim copy of cluster.go lines 405-415)"
  artifacts:
    - path: "internal/app/cmd/event.go"
      provides: "km event add/list/rm/apply Cobra command tree + RunEventAdd / RunEventRm / RunEventApply / runEventList exported functions + EventRunner interface + NewEventRunnerFunc / PersistEventsConfigFunc seams + GenerateEventHCL template + atExprToCronForRules wiring"
      contains: "type EventRunner interface"
    - path: "internal/app/cmd/root.go"
      provides: "Registration of NewEventCmd in the root command tree"
      contains: "NewEventCmd"
  key_links:
    - from: "internal/app/cmd/event.go (RunEventAdd)"
      to: "pkg/at/parser.Parse"
      via: "NL time expression argument → ScheduleSpec; one-off goes through AtExprToCronForRules; recurring passes through as cron(...) / rate(...)"
      pattern: "atpkg.Parse\\|AtExprToCronForRules"
    - from: "internal/app/cmd/event.go (generated terragrunt.hcl)"
      to: "infra/modules/operator-event-rule/v1.0.0/"
      via: "source = \"${local.repo_root}/infra/modules//operator-event-rule/v1.0.0\" (double-slash // pattern from Phase 80 memory)"
      pattern: "infra/modules//operator-event-rule"
    - from: "internal/app/cmd/event.go (RunEventAdd)"
      to: "infra/modules/operator-event-bus/v1.0.0/ outputs"
      via: "reads bus_name + km_runner_lambda_arn via terragrunt output --terragrunt-working-dir infra/live/{region}/operator-event-bus/"
      pattern: "operator-event-bus|terragrunt.*output"
---

<objective>
Implement the `km event` Cobra command tree — the CLI surface that operators use to declare durable EventBridge
rules in git-tracked YAML manifests deployed via terragrunt. This is the user-facing payload of Phase 83.

The full flow:
1. `km event add 'every monday at 1pm' doctor --name nightly-doctor` —
2. NL parse via pkg/at.Parse → ScheduleSpec
3. If IsRecurring=false, convert at(...) to pinned cron via pkg/events.AtExprToCronForRules (EventBridge Rules don't support at())
4. Read bus name + km-runner ARN from `terragrunt output` against infra/live/{region}/operator-event-bus/
5. Generate per-rule terragrunt.hcl at infra/live/{region}/event-rule-{name}/ from the embedded template
6. ExportConfigEnvVars(cfg), bootstrap region.hcl if missing
7. If --dry-run=true: runner.Plan → return; else runner.Apply → runner.Output → captures rule_arn + role_arn
8. Append to cfg.Events; PersistEventsConfigFunc; on persist failure → error pointing at `km event rm` (no auto-destroy)

Output:
- internal/app/cmd/event.go — full Cobra command tree + RunEventAdd/RunEventRm/RunEventApply + EventRunner seam
- Wave 0 event tests promoted from SKIP to PASS (10 event_test.go tests + TestEventE2E)
- root.go wires NewEventCmd into the top-level command tree
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
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-01-SUMMARY.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-02-SUMMARY.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-03-SUMMARY.md
@internal/app/cmd/cluster.go
@internal/app/cmd/cluster_test.go
@internal/app/cmd/root.go
@pkg/at/parser.go
@pkg/events/manifest.go

<interfaces>
event.go MUST mirror cluster.go exactly. The patterns are:

```go
// internal/app/cmd/event.go

type eventAddOpts struct {
    name         string
    targetKM     string    // --target-km '<cmd>' shorthand
    targets      []string  // --target arn:... (repeatable, for type:arn)
    inputJSON    string    // --input '{...}'
    dlq          string    // --dlq arn:aws:sqs:...
    description  string
    noArchive    bool
    eventPattern string    // --event-pattern <file.json>
    hclOverride  string    // --hcl <file>
    awsProfile   string
    region       string
    verbose      bool
    dryRun       bool      // default TRUE — matches cluster.go convention
}

type EventRunner interface {
    Plan(ctx context.Context, dir string) error
    Apply(ctx context.Context, dir string) error
    Destroy(ctx context.Context, dir string) error
    Reconfigure(ctx context.Context, dir string) error
    Output(ctx context.Context, dir string) (map[string]interface{}, error)
}

var NewEventRunnerFunc = func(profile, repoRoot string) EventRunner {
    return terragrunt.NewRunner(profile, repoRoot)
}

var PersistEventsConfigFunc = func(events []config.EventConfig) error {
    configPath := filepath.Join(findRepoRoot(), "km-config.yaml")
    return PersistEventsConfig(configPath, events)
}

// HCL template (mirror cluster.go's clusterTerragruntHCLTemplate exactly,
// just swap module name and inputs):
const eventRuleTerragruntHCLTemplate = `locals {
  repo_root     = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars     = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  region_label  = local.region_config.locals.region_label
  account_id    = get_aws_account_id()
}
include "root" {
  path = find_in_parent_folders("root.hcl")
}
remote_state {
  ...
  key = ".../${local.region_label}/event-rule-{RULE_NAME}/terraform.tfstate"
  ...
}
terraform {
  source = "${local.repo_root}/infra/modules//operator-event-rule/v1.0.0"  # double-slash!
}
inputs = {
  resource_prefix = local.site_vars.locals.site.label
  rule_name       = "{RULE_NAME}"
  event_bus_name  = "{EVENT_BUS_NAME}"
  schedule        = "{SCHEDULE}"
  event_pattern   = "{EVENT_PATTERN}"
  target_arn      = "{TARGET_ARN}"
  input_json      = "{INPUT_JSON}"
  description     = "{DESCRIPTION}"
}
`

func GenerateEventHCL(ruleName, busName, schedule, eventPattern, targetARN, inputJSON, description string) string

// RunEventAdd: mirrors RunClusterAdd lines 344-499 with the following differences:
//   - NL parse step (cluster.go has no equivalent): atpkg.Parse(timeExpr) → ScheduleSpec.
//     If IsRecurring → use spec.Expression directly. If !IsRecurring → AtExprToCronForRules.
//   - Read bus name + km-runner ARN from terragrunt output BEFORE writing the rule's HCL.
//     If --target-km is set, target_arn = km_runner_lambda_arn from bus outputs.
//     If --target is set (one or more), target_arn = first; multi-target uses --hcl escape hatch.
//   - The rest is mechanically identical: idempotency check, ExportConfigEnvVars, region.hcl bootstrap,
//     HCL write, dry-run path, Apply, Output, Events append, PersistEventsConfigFunc + rollback hint.
func RunEventAdd(cfg *config.Config, timeExpr, kmCmd, name, targetKM string, targets []string, inputJSON, dlq, description string, noArchive bool, eventPattern, hclOverride, awsProfile, region string, verbose, dryRun bool, repoRoot string) error

func RunEventRm(cfg *config.Config, name, awsProfile, region string, verbose, dryRun bool, repoRoot string) error

// RunEventApply: reads events/<name>.yaml, validates via pkg/events.Validate(), translates to RunEventAdd-equivalent
// flow (or directly generates HCL from the manifest fields; the YAML can express more than the flag set).
func RunEventApply(cfg *config.Config, manifestPath, awsProfile, region string, verbose, dryRun bool, repoRoot string) error

func PersistEventsConfig(configPath string, events []config.EventConfig) error
```

Plan 83-02 SUMMARY provides the EventConfig struct shape and the EventManifest schema. Plan 83-03 SUMMARY provides the module outputs (bus_name, km_runner_lambda_arn) to consume.

cluster.go is the verbatim template. The diff is only in:
- The HCL template's module source path (operator-event-rule, not cluster-irsa)
- The NL parsing + at()→cron conversion step (cluster.go has no time component)
- Reading the bus stack outputs to discover km_runner_lambda_arn (cluster.go has no upstream-stack dependency)
- 11 flags instead of 8 (event has more knobs)
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Implement internal/app/cmd/event.go (add/list/rm/apply Cobra tree + EventRunner seam + HCL template + PersistEventsConfig)</name>
  <files>internal/app/cmd/event.go</files>
  <behavior>
    - File declares `package cmd` (matches cluster.go convention).
    - Header comment block per cluster.go style: `// Package cmd provides the Cobra command tree for the km CLI.\n// This file implements `km event {add,list,rm,apply}` — operator-controlled EventBridge rules. (Phase 83)`
    - All public symbols exported with PascalCase + godoc-style comments (mirror cluster.go).
    - eventAddOpts struct per <interfaces>.
    - EventRunner interface per <interfaces>.
    - NewEventRunnerFunc factory var (testing seam).
    - PersistEventsConfigFunc seam var.
    - eventRuleTerragruntHCLTemplate constant (verbatim from cluster.go's clusterTerragruntHCLTemplate with substitutions).
    - GenerateEventHCL exported function (substitutes 7 placeholders; if SCHEDULE empty, leave as empty string and Terraform's null-handling skips it; same for EVENT_PATTERN).
    - PersistEventsConfig exported function — read/unmarshal/set/marshal/write pattern verbatim from PersistClustersConfig.
    - NewEventCmd(cfg *config.Config) *cobra.Command returning the parent "event" command with four subcommands.
    - newEventAddCmd / newEventListCmd / newEventRmCmd / newEventApplyCmd — Cobra constructors per cluster.go pattern.
    - RunEventAdd / RunEventRm / RunEventApply / runEventList — exported flow functions.
    - RunEventAdd implementation steps (mirror RunClusterAdd lines 344-499):
      1. Idempotency check: for c in cfg.Events { if c.Name == name → print existing rule ARN, return nil }
      2. AWS credential pre-flight via awspkg.LoadAWSConfig + ValidateCredentials
      3. ExportConfigEnvVars(cfg) — MANDATORY before any terragrunt call
      4. NL parse: spec, err := atpkg.Parse(timeExpr, time.Now()); if err return wrapped error. Compute expression string:
         - If spec.IsRecurring → expression = spec.Expression (already cron(...) or rate(...))
         - If !spec.IsRecurring → expression, err := events.AtExprToCronForRules(spec.Expression); if err return wrapped
      5. Read bus stack outputs: build a terragrunt runner pointed at infra/live/{regionLabel}/operator-event-bus/; call runner.Output(ctx, busDir). If the bus stack hasn't been applied yet → return clear error: "operator-event-bus stack not found at %s — run `km init` first". Extract bus_name and km_runner_lambda_arn (the latter only needed if --target-km was used).
      6. Determine target_arn:
         - If targetKM != "" → target_arn = km_runner_lambda_arn (from bus outputs); also set INPUT_JSON to json-encoded `{"command": "%s"}` with kmCmd
         - If len(targets) > 0 → target_arn = targets[0]; if len(targets) > 1 → return error "multi-target requires --hcl escape hatch" (per CONTEXT.md, v1 takes first)
         - If both empty → return error "must specify --target-km or --target"
      7. Compute stackDir = filepath.Join(repoRoot, "infra", "live", regionLabel, "event-rule-"+name)
      8. region.hcl bootstrap (verbatim from cluster.go lines 405-415)
      9. Write per-rule terragrunt.hcl via GenerateEventHCL + os.WriteFile
      10. Build runner via NewEventRunnerFunc(awsProfile, repoRoot)
      11. If dryRun → runner.Plan(ctx, stackDir); print "(dry-run) terragrunt plan complete — re-run with --dry-run=false to apply"; return nil
      12. runner.Apply(ctx, stackDir); on error wrap
      13. outputs, err := runner.Output(ctx, stackDir); extract rule_arn + role_arn
      14. Append config.EventConfig{Name: name, Type: "schedule"/"pattern", Expression: expression, TargetType: "km"/"arn", Targets: [target_arn], RuleARN: rule_arn, RoleARN: role_arn} to cfg.Events
      15. Call PersistEventsConfigFunc(cfg.Events). If err: return wrapped error with message containing "km event rm "+name. Do NOT call runner.Destroy.
      16. Print confirmation: "Event %q provisioned: %s\n", name, rule_arn
    - RunEventRm implementation (mirror RunClusterRm verbatim, swap "cluster" → "event"):
      - Find by name in cfg.Events; error if not found
      - Pre-flight + ExportConfigEnvVars
      - Compute stackDir (same naming as Add)
      - If dryRun → Plan; return
      - Reconfigure + Destroy
      - Remove from cfg.Events; PersistEventsConfigFunc; os.RemoveAll(stackDir) [warn if fails]
    - RunEventApply implementation:
      - Read manifest file via os.ReadFile
      - events.ParseManifest(data) → events.EventManifest
      - manifest.Validate() — if error, return wrapped
      - Translate manifest into the same call shape as RunEventAdd (effectively call into a shared internal `runEventAddCore` helper that doesn't take the CLI flag values). Or just inline the translation: extract name, expression, target_arn, target_type from manifest fields and proceed through steps 5-16 of RunEventAdd.
      - If manifest has --hcl override (manifest schema doesn't include this; only CLI flag), error.
    - runEventList — tabwriter table with NAME / TYPE / EXPRESSION / TARGET TYPE / TARGETS / RULE ARN columns (mirror runClusterList).

    - Cobra flag wiring for newEventAddCmd:
      - cmd.Flags().StringVar(&opts.name, "name", "", "event rule name (required, kebab-case slug)")
      - cmd.Flags().StringVar(&opts.targetKM, "target-km", "", "shorthand: target km-runner Lambda with this command (e.g. 'doctor --all-regions')")
      - cmd.Flags().StringArrayVar(&opts.targets, "target", nil, "target ARN (repeatable for fanout — v1 uses first; multi-target requires --hcl)")
      - cmd.Flags().StringVar(&opts.inputJSON, "input", "", "static target input override (JSON)")
      - cmd.Flags().StringVar(&opts.dlq, "dlq", "", "dead-letter queue SQS ARN")
      - cmd.Flags().StringVar(&opts.description, "description", "", "rule description")
      - cmd.Flags().BoolVar(&opts.noArchive, "no-archive", false, "skip bus archive (default: archive enabled)")
      - cmd.Flags().StringVar(&opts.eventPattern, "event-pattern", "", "path to JSON file with event pattern (alternative to NL schedule)")
      - cmd.Flags().StringVar(&opts.hclOverride, "hcl", "", "path to operator-supplied HCL file (escape hatch for advanced rules)")
      - cmd.Flags().StringVar(&opts.awsProfile, "aws-profile", "klanker-application", "AWS profile for terragrunt apply")
      - cmd.Flags().StringVar(&opts.region, "region", "us-east-1", "AWS region")
      - cmd.Flags().BoolVar(&opts.verbose, "verbose", false, "stream terragrunt output")
      - cmd.Flags().BoolVar(&opts.dryRun, "dry-run", true, "plan only; set --dry-run=false to apply")
      - cmd.MarkFlagRequired("name")
      - Positional args: cobra.MinimumNArgs(1) — the time expression (and optionally the km command if --target-km uses positional shorthand; we keep it explicit via --target-km for clarity)
    - --dry-run defaults to TRUE per CONTEXT.md, matching `km cluster add`.

    AVOID:
    - Forking pkg/at parser logic — call atpkg.Parse and use its ScheduleSpec output directly
    - Inventing a new EventConfig struct — Plan 83-02's config.EventConfig is canonical
    - Skipping ExportConfigEnvVars (memory: project_terragrunt_env_export — non-default prefixes hit 403)
    - Calling runner.Destroy on persist failure — the rollback contract is "leave infra, hint at km event rm"
    - Using event_bus_name = ARN — pass the bus NAME (Pitfall 1 from RESEARCH.md)
    - Wiring `--cron` flag on `km event` — recurring NL via `'every monday'` covers the use case; raw cron is an --hcl power-user move

    REFERENCES:
    - internal/app/cmd/cluster.go — the verbatim mechanistic template (lines 1-608)
    - internal/app/cmd/cluster_test.go — for the test patterns Task 2 must satisfy
    - pkg/at/parser.go ScheduleSpec — the type returned by atpkg.Parse
    - pkg/events/manifest.go (from Plan 83-02) — the type RunEventApply consumes
    - Plan 83-03 SUMMARY — bus + rule module variable shape
  </behavior>
  <action>
    1. Open and study internal/app/cmd/cluster.go fully (especially lines 86-145 for the HCL template, 344-499 for RunClusterAdd, 532-608 for RunClusterRm, 506-516 for runClusterList).
    2. Open pkg/at/parser.go to confirm ScheduleSpec struct + Parse signature.
    3. Open the Plan 83-02 + 83-03 SUMMARY files to confirm config.EventConfig + module output names.
    4. Create internal/app/cmd/event.go with the full content per <behavior>. Start by copying cluster.go to event.go and doing a literal find/replace of cluster→event, Cluster→Event, CLUSTER→EVENT, then layer in:
       - NL parser step (steps 4-5 of RunEventAdd flow)
       - Bus-stack output read (step 5)
       - Target ARN resolution (step 6)
       - The eventRuleTerragruntHCLTemplate with 7 placeholders (vs cluster's 5)
       - Three subcommands instead of three (event adds `apply`)
    5. Special handling for runEventList: include the target/expression columns since events are more varied than clusters.
    6. Run `go build ./internal/app/cmd/...`. Must succeed.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && go build ./internal/app/cmd/... && go vet ./internal/app/cmd/...</automated>
  </verify>
  <done>
    internal/app/cmd/event.go exists, contains EventRunner interface, NewEventRunnerFunc + PersistEventsConfigFunc seams, eventRuleTerragruntHCLTemplate, GenerateEventHCL, PersistEventsConfig, NewEventCmd + 4 subcommand constructors, RunEventAdd + RunEventRm + RunEventApply + runEventList. `go build ./internal/app/cmd/...` succeeds. `go vet ./internal/app/cmd/...` clean.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Promote Wave 0 event tests from SKIP to PASS — fill test bodies against the production code from Task 1</name>
  <files>internal/app/cmd/event_test.go, internal/app/cmd/event_e2e_test.go</files>
  <behavior>
    - All event-related tests in event_test.go and event_e2e_test.go that target Phase 83 CLI functionality move from SKIP to PASS:
      - TestEventAdd_Idempotent — seed cfg.Events with {Name: "foo"}; call RunEventAdd with name="foo"; assert mock.Applied stays empty; assert return value nil; assert printed output contains "already registered"
      - TestEventAdd_DryRun — mock returns no error from Plan; call RunEventAdd with dryRun=true; assert mock.PlanCalled == true AND len(mock.Applied) == 0 AND PersistEventsConfigFunc NOT invoked
      - TestEventAdd_Apply — mock returns no error from Apply, Output returns {rule_arn: "...", role_arn: "..."}; mock the bus output read (this requires the bus output read to be a seam too OR the test exercises a code path where bus is already known — see test fixtures); call RunEventAdd with dryRun=false; assert mock.Applied contains the event-rule stack dir; assert cfg.Events has 1 entry with name+rule_arn; assert PersistEventsConfigFunc was called via test-injected fake.
      - TestEventAdd_PersistFailure — inject PersistEventsConfigFunc that returns fmt.Errorf("disk full"); call RunEventAdd with dryRun=false; assert err != nil AND strings.Contains(err.Error(), "km event rm") AND len(mock.Destroyed) == 0
      - TestEventRm_DryRun — seed cfg.Events; call RunEventRm dryRun=true; assert mock.PlanCalled AND len(mock.Destroyed) == 0
      - TestEventRm_NotFound — empty cfg.Events; call RunEventRm with unknown name; assert err contains "not found"
      - TestGenerateEventHCL — call GenerateEventHCL with all 7 placeholders; assert no literal "{...}" remains; assert each input value appears verbatim in the output
      - TestPersistEvents — temp km-config.yaml with `domain: example.com`; PersistEventsConfig with 2 entries; re-read; assert domain still present AND events: list has 2 entries
      - TestEventE2E (event_e2e_test.go) — full mock terragrunt flow: t.TempDir() as repoRoot; mock EventRunner records calls; call RunEventAdd(...) → call RunEventRm(...) → assert correct order: Plan-or-Apply → Output → Destroy → final cfg.Events is empty
    - Note: For tests that need to mock the bus output read, prefer one of:
      (a) Refactor RunEventAdd to take an optional `busOutputs map[string]interface{}` parameter that, if set, skips the runner.Output call to the bus stack. Tests inject; production passes nil.
      (b) Add a `BusOutputsReaderFunc` seam similar to NewEventRunnerFunc that production points at a function calling runner.Output on the bus dir; tests inject a fake.
      Choose (b) — symmetric with NewEventRunnerFunc, no production code change after the wave.

    AVOID:
    - Removing t.Skip without actually filling the test body — Wave 0 contract said tests must PASS post-Task 1
    - Mocking system calls (os.WriteFile, os.MkdirAll) — use t.TempDir() instead
    - Asserting on stdout text exactly — match substring containment to absorb minor wording drift
    - Skipping any of the Wave 0 enumerated test functions — full coverage is the must_have

    REFERENCES:
    - internal/app/cmd/cluster_test.go — mirror its test body patterns verbatim (variable names, mock-call assertions, t.Cleanup usage for seam restoration)
    - 83-01 Wave 0 scaffolds — the comment blocks above each t.Skip enumerate the exact assertions
  </behavior>
  <action>
    1. Read internal/app/cmd/event_test.go (Wave 0 skeletons) to see the enumerated assertions in each test's comment block.
    2. Read internal/app/cmd/cluster_test.go for verbatim test body patterns (especially TestClusterAddPersistFailure for the rollback contract test).
    3. If Task 1 didn't add a BusOutputsReaderFunc seam, ADD IT NOW to event.go (small refactor): declare `var BusOutputsReaderFunc = func(ctx context.Context, runner EventRunner, busDir string) (map[string]interface{}, error) { return runner.Output(ctx, busDir) }` at the seam-var section near NewEventRunnerFunc. Update RunEventAdd to call BusOutputsReaderFunc instead of runner.Output directly for the bus stack.
    4. For each test in event_test.go: remove the t.Skip line and write the test body per the enumerated comment block. Each test uses t.Cleanup to restore overridden seams. Match Phase 80 TestClusterAddPersistFailure for the rollback test:
       ```go
       func TestEventAdd_PersistFailure(t *testing.T) {
           origPersist := cmd.PersistEventsConfigFunc
           origRunner := cmd.NewEventRunnerFunc
           origBus := cmd.BusOutputsReaderFunc
           t.Cleanup(func() {
               cmd.PersistEventsConfigFunc = origPersist
               cmd.NewEventRunnerFunc = origRunner
               cmd.BusOutputsReaderFunc = origBus
           })
           mock := &mockEventRunner{OutputResult: map[string]interface{}{"rule_arn": "arn:...", "role_arn": "arn:..."}}
           cmd.NewEventRunnerFunc = func(_, _ string) cmd.EventRunner { return mock }
           cmd.PersistEventsConfigFunc = func(_ []config.EventConfig) error { return fmt.Errorf("disk full") }
           cmd.BusOutputsReaderFunc = func(_ context.Context, _ cmd.EventRunner, _ string) (map[string]interface{}, error) {
               return map[string]interface{}{"bus_name": "test-prefix-operator-events", "km_runner_lambda_arn": "arn:aws:lambda:..."}, nil
           }
           cfg := &config.Config{}
           err := cmd.RunEventAdd(cfg, "5pm tomorrow", "doctor --all-regions", "test-rule", "doctor --all-regions", nil, "", "", "", false, "", "", "klanker-application", "us-east-1", false, false, t.TempDir())
           if err == nil { t.Fatal("expected error from PersistEventsConfigFunc failure") }
           if !strings.Contains(err.Error(), "km event rm") { t.Errorf("error %q should contain 'km event rm'", err.Error()) }
           if len(mock.Destroyed) != 0 { t.Errorf("expected no Destroy call after persist failure, got %d", len(mock.Destroyed)) }
       }
       ```
    5. Write TestEventE2E in event_e2e_test.go: assemble a temp repoRoot with infra/live/use1/region.hcl pre-seeded, inject mock for everything, run RunEventAdd + RunEventRm, assert the call sequence.
    6. Run `go test ./internal/app/cmd/ -run TestEvent -v -count=1`. All 9 Event-prefixed tests must PASS.

    AVOID:
    - Cleaning the t.Cleanup blocks — they're essential to prevent test pollution (mocking seams persists across tests in same package).
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && go test ./internal/app/cmd/ -run "TestEventAdd|TestEventRm|TestEventE2E|TestGenerateEventHCL|TestPersistEvents" -v -count=1 2>&1 | grep -E "(--- PASS|--- FAIL|--- SKIP)" | tee /tmp/83-05-task2-tests.out && ! grep -q "FAIL\|SKIP" /tmp/83-05-task2-tests.out</automated>
  </verify>
  <done>
    All 9 event-related tests PASS (TestEventAdd_Idempotent, TestEventAdd_DryRun, TestEventAdd_Apply, TestEventAdd_PersistFailure, TestEventRm_DryRun, TestEventRm_NotFound, TestGenerateEventHCL, TestPersistEvents, TestEventE2E). No SKIP statuses remaining. No FAIL statuses.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: Register NewEventCmd in root.go and run end-to-end help-text smoke test</name>
  <files>internal/app/cmd/root.go</files>
  <behavior>
    - Locate the registration of NewClusterCmd in root.go (likely a line like `rootCmd.AddCommand(NewClusterCmd(cfg))`).
    - Add `rootCmd.AddCommand(NewEventCmd(cfg))` immediately after the cluster registration, preserving alphabetical order if root.go follows that convention (event comes before cluster lexically; check existing order and place accordingly).
    - Smoke test: build the km binary via `make build`, then run `./km event --help`, `./km event add --help`, `./km event list --help`, `./km event rm --help`, `./km event apply --help`. Each must exit 0 and print the expected help text.

    AVOID:
    - Adding any logic to root.go beyond the one AddCommand line
    - Breaking existing command registration order if root.go has strict alphabetical ordering enforced

    REFERENCES:
    - internal/app/cmd/root.go — find the AddCommand chain
  </behavior>
  <action>
    1. Read internal/app/cmd/root.go to locate the AddCommand chain.
    2. Add `rootCmd.AddCommand(NewEventCmd(cfg))` adjacent to the cluster registration (matching existing ordering convention).
    3. Run `make build`. Must succeed (this also confirms the version embed continues to work).
    4. Run `./km event --help`. Must exit 0 and print "Usage:" + the four subcommand names.
    5. Run `./km event add --help`. Must exit 0 and print all 13 flags from Task 1.
    6. Run `./km event list --help`. Must exit 0.
    7. Run `./km event rm --help`. Must exit 0.
    8. Run `./km event apply --help`. Must exit 0.

    AVOID: Running any `km event add` against AWS in this verify — that's UAT (Plan 83-07), not unit work.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && make build > /dev/null 2>&1 && ./km event --help > /dev/null && ./km event add --help > /dev/null && ./km event list --help > /dev/null && ./km event rm --help > /dev/null && ./km event apply --help > /dev/null && ./km event add --help 2>&1 | grep -E "(--name|--target-km|--dry-run|--event-pattern|--hcl)" | wc -l</automated>
  </verify>
  <done>
    `make build` succeeds. All 5 help-text invocations exit 0. `./km event add --help` mentions at least --name, --target-km, --dry-run, --event-pattern, and --hcl (count ≥ 5). The km event command tree is reachable from the root.
  </done>
</task>

</tasks>

<verification>
- `go build ./...` exits 0
- `go vet ./...` exits 0
- `go test ./internal/app/cmd/ -run TestEvent -v -count=1` shows 9 PASS, 0 SKIP, 0 FAIL
- `make build` succeeds
- `./km event --help` and all 4 subcommand --help calls exit 0
- internal/app/cmd/event.go exports EventRunner, NewEventRunnerFunc, PersistEventsConfigFunc, BusOutputsReaderFunc, GenerateEventHCL, RunEventAdd, RunEventRm, RunEventApply, NewEventCmd
</verification>

<success_criteria>
- `km event` Cobra command tree is fully wired into the root command
- All 9 Wave 0 event tests have been promoted to PASS
- The idempotency, dry-run, apply, persist-failure-rollback, rm-dry-run, rm-not-found, hcl-template, persist contracts are all verified by unit tests
- Plan 83-06 has a working `km event` to add the doctor check around and a working CLI surface to strip --cron from `km at`
</success_criteria>

<output>
After completion, create `.planning/phases/83-km-event-operator-controlled-eventbridge/83-05-SUMMARY.md` documenting:
- Whether the BusOutputsReaderFunc seam was added (and why it's needed for testability)
- The exact 7 placeholders the eventRuleTerragruntHCLTemplate uses (Plan 83-06's doctor check needs to know the rule name format)
- The decision on multi-target handling (v1 takes targets[0], errors on len > 1; documented for Plan 83-07 docs)
- The decision on --no-archive flag handling — operator-event-bus archive is always provisioned; --no-archive may be a no-op in v1 OR may be wired into a per-rule archive opt-out (Plan 83-03's module doesn't currently support per-rule archive opt-out; document the decision)
- Final list of all 13 CLI flags on `km event add` for Plan 83-07 docs
</output>
</content>
