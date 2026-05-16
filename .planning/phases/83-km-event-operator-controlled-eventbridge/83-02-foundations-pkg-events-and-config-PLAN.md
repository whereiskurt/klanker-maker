---
phase: 83-km-event-operator-controlled-eventbridge
plan: 02
type: execute
wave: 1
depends_on: [01]
files_modified:
  - pkg/events/doc.go
  - pkg/events/manifest.go
  - pkg/events/parser.go
  - pkg/events/validate.go
  - pkg/events/schemas/event_manifest.schema.json
  - pkg/events/testdata/valid-schedule-km.yaml
  - pkg/events/testdata/valid-schedule-arn.yaml
  - pkg/events/testdata/valid-event-pattern.yaml
  - pkg/events/testdata/invalid-both-schedule-and-pattern.yaml
  - pkg/events/testdata/invalid-target-mismatch.yaml
  - internal/app/config/config.go
  - infra/modules/km-operator-policy/v1.0.0/main.tf
autonomous: true
requirements:
  - operator-feature-83
must_haves:
  truths:
    - "pkg/events.EventManifest YAML parses + validates the canonical examples in pkg/events/testdata/"
    - "EventManifest.Validate() rejects manifests with both schedule and event_pattern set, and with target.type/companion-field mismatches"
    - "pkg/events.AtExprToCronForRules('at(2026-05-20T14:30:00)') returns 'cron(30 14 20 5 ? 2026)' (verified by Wave 0 TestAtExprToCronForRules)"
    - "JSON Schema file at pkg/events/schemas/event_manifest.schema.json is embedded via //go:embed and accessible via SchemaJSON()"
    - "config.EventConfig struct exists with mapstructure+yaml tags; Config struct gains Events []EventConfig field"
    - "Loading a km-config.yaml with events: list round-trips through config.Load (Wave 0 TestEventsConfigRoundTrip passes)"
    - "infra/modules/km-operator-policy/v1.0.0/main.tf EventBridgePutEvents policy includes BOTH event-bus/default AND event-bus/${var.resource_prefix}-operator-events in its Resource list"
    - "terraform validate passes on infra/modules/km-operator-policy/v1.0.0/ after the policy update"
  artifacts:
    - path: "pkg/events/manifest.go"
      provides: "EventManifest + Target structs with yaml tags, ParseManifest(), SchemaJSON() embed accessor"
      contains: "type EventManifest struct"
    - path: "pkg/events/parser.go"
      provides: "AtExprToCronForRules — converts at(ISO8601) to cron(M H D Mo ? Y) for EventBridge Rules"
      contains: "func AtExprToCronForRules"
    - path: "pkg/events/validate.go"
      provides: "EventManifest.Validate() — schedule/event_pattern XOR, target.type discriminator, kebab-case slug check"
      contains: "func (m EventManifest) Validate"
    - path: "pkg/events/schemas/event_manifest.schema.json"
      provides: "JSON Schema for IDE/editor completion on events/*.yaml files"
      contains: "\"$schema\""
    - path: "internal/app/config/config.go"
      provides: "EventConfig struct + Config.Events []EventConfig field + Load() unmarshal"
      contains: "type EventConfig struct"
    - path: "infra/modules/km-operator-policy/v1.0.0/main.tf"
      provides: "EventBridgePutEvents policy scope additively widened to include the {prefix}-operator-events custom bus"
      contains: "operator-events"
  key_links:
    - from: "pkg/events/manifest.go"
      to: "pkg/events/schemas/event_manifest.schema.json"
      via: "//go:embed directive"
      pattern: "//go:embed schemas/event_manifest"
    - from: "internal/app/config/config.go (Load)"
      to: "EventConfig struct"
      via: "v.UnmarshalKey(\"events\", &cfg.Events) and v.SetDefault(\"events\", []interface{}{})"
      pattern: "UnmarshalKey.*events|SetDefault.*events"
---

<objective>
Build the foundational Go packages and IAM policy update that Plans 83-03 (Terraform modules), 83-04 (km-runner Lambda),
and 83-05 (km event CLI) depend on. No CLI surface yet, no Terraform modules yet — just the data structures, validation,
and the IAM policy widening that lets km-runner emit events to the custom bus.

Purpose: Establish the manifest schema and config schema FIRST so downstream plans implement against a stable contract.
The at()→cron conversion is also here — it is independent of Lambda + CLI and the Wave 0 test already targets it.

Output:
- pkg/events/ package with EventManifest, Target, Validate, AtExprToCronForRules, embedded JSON Schema, and 5 fixture manifests
- internal/app/config/config.go extended with EventConfig type and Config.Events field
- infra/modules/km-operator-policy/v1.0.0/main.tf EventBridgePutEvents policy additively widened to include the {prefix}-operator-events custom bus
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
@internal/app/config/config.go
@infra/modules/km-operator-policy/v1.0.0/main.tf
@pkg/profile/schema.go
@pkg/at/parser.go

<interfaces>
<!-- Wave 0 test expectations (from 83-01) drive the API surface:

  // pkg/events/manifest.go
  package events
  
  //go:embed schemas/event_manifest.schema.json
  var eventManifestSchemaJSON []byte
  
  type EventManifest struct {
    Name         string `yaml:"name"`
    Schedule     string `yaml:"schedule,omitempty"`     // "cron(...)" or "rate(...)"; XOR with EventPattern
    EventPattern string `yaml:"event_pattern,omitempty"`
    Target       Target `yaml:"target"`
    Input        string `yaml:"input,omitempty"`        // optional static JSON
    DLQ          string `yaml:"dlq,omitempty"`
    Description  string `yaml:"description,omitempty"`
    Archive      *bool  `yaml:"archive,omitempty"`      // default true; *bool distinguishes unset
  }
  type Target struct {
    Type    string   `yaml:"type"`              // "km" or "arn"
    Command string   `yaml:"command,omitempty"` // for type:km
    ARNs    []string `yaml:"arns,omitempty"`    // for type:arn (fanout)
  }
  func ParseManifest(data []byte) (EventManifest, error)
  func SchemaJSON() []byte
  
  // pkg/events/validate.go
  func (m EventManifest) Validate() error
  // Rules:
  //   - exactly one of Schedule / EventPattern (XOR)
  //   - Target.Type ∈ {"km","arn"}
  //   - Target.Type=="km" → Command non-empty, ARNs nil
  //   - Target.Type=="arn" → ARNs non-empty, Command empty
  //   - Name matches ^[a-z][a-z0-9-]{0,62}$ (kebab-case slug)
  
  // pkg/events/parser.go
  func AtExprToCronForRules(atExpr string) (string, error)
  // "at(2026-05-20T14:30:00)" → "cron(30 14 20 5 ? 2026)"
  // (EventBridge Rules don't support at(); must convert to pinned cron)
  
  // internal/app/config/config.go
  type EventConfig struct {
    Name       string   `mapstructure:"name"        yaml:"name"`
    Type       string   `mapstructure:"type"        yaml:"type"`        // "schedule" or "pattern"
    Expression string   `mapstructure:"expression"  yaml:"expression"`
    TargetType string   `mapstructure:"target_type" yaml:"target_type"` // "km" or "arn"
    Targets    []string `mapstructure:"targets"     yaml:"targets"`
    RuleARN    string   `mapstructure:"rule_arn"    yaml:"rule_arn"`
    RoleARN    string   `mapstructure:"role_arn"    yaml:"role_arn"`
  }
  // Add to Config: Events []EventConfig `mapstructure:"events" yaml:"events"`
  // Add to Load(): v.SetDefault("events", []interface{}{}) and UnmarshalKey
-->

Existing reference (from cluster.go and config.go):

```go
// internal/app/config/config.go line 16-26 — ClusterConfig (mirror exactly):
type ClusterConfig struct {
    Name             string `mapstructure:"name"               yaml:"name"`
    OIDCProviderARN  string `mapstructure:"oidc_provider_arn"  yaml:"oidc_provider_arn"`
    Namespace        string `mapstructure:"namespace"          yaml:"namespace"`
    ServiceAccount   string `mapstructure:"service_account"    yaml:"service_account"`
    RoleARN          string `mapstructure:"role_arn"           yaml:"role_arn"`
}
// line 188-192 — Clusters field:
Clusters []ClusterConfig `mapstructure:"clusters" yaml:"clusters"`
```

Existing EventBridgePutEvents policy (infra/modules/km-operator-policy/v1.0.0/main.tf line 543-546):

```hcl
Sid      = "EventBridgePutEvents"
...
Resource = "arn:aws:events:*:${data.aws_caller_identity.current.account_id}:event-bus/default"
```

Widen to:

```hcl
Resource = [
  "arn:aws:events:*:${data.aws_caller_identity.current.account_id}:event-bus/default",
  "arn:aws:events:*:${data.aws_caller_identity.current.account_id}:event-bus/${var.resource_prefix}-operator-events",
]
```
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Create pkg/events package — manifest.go + parser.go + validate.go + JSON Schema + fixtures</name>
  <files>pkg/events/doc.go, pkg/events/manifest.go, pkg/events/parser.go, pkg/events/validate.go, pkg/events/schemas/event_manifest.schema.json, pkg/events/testdata/valid-schedule-km.yaml, pkg/events/testdata/valid-schedule-arn.yaml, pkg/events/testdata/valid-event-pattern.yaml, pkg/events/testdata/invalid-both-schedule-and-pattern.yaml, pkg/events/testdata/invalid-target-mismatch.yaml</files>
  <behavior>
    - Tests Wave 0 already declared (TestManifestValidation, TestManifestYAMLRoundTrip, TestAtExprToCronForRules) must move from SKIP to PASS after this task.
    - Test 1 (TestManifestValidation valid cases):
      - testdata/valid-schedule-km.yaml: name="nightly-doctor", schedule="cron(0 9 * * ? *)", target.type="km", target.command="doctor --all-regions" → Validate() returns nil
      - testdata/valid-schedule-arn.yaml: name="kick-step-function", schedule="rate(1 hour)", target.type="arn", target.arns=["arn:aws:lambda:us-east-1:123:function:foo"] → Validate() returns nil
      - testdata/valid-event-pattern.yaml: name="on-s3-upload", event_pattern='{"source":["aws.s3"]}', target.type="arn", target.arns=["arn:aws:sns:..."] → Validate() returns nil
    - Test 2 (TestManifestValidation invalid cases):
      - testdata/invalid-both-schedule-and-pattern.yaml: both schedule AND event_pattern set → Validate() returns error containing "exactly one of"
      - testdata/invalid-target-mismatch.yaml: target.type="km" but target.arns=["arn:..."] set → Validate() returns error containing "target.type"
      - Inline (no file): name="BadName_underscores" → Validate() returns error containing "kebab-case"
      - Inline: neither schedule nor event_pattern → Validate() returns error containing "exactly one of"
    - Test 3 (TestManifestYAMLRoundTrip): parse valid-schedule-km.yaml → marshal back → parse again → reflect.DeepEqual the two structs (including *bool Archive which yaml omits when nil).
    - Test 4 (TestAtExprToCronForRules) cases from Wave 0:
      - "at(2026-05-20T14:30:00)" → "cron(30 14 20 5 ? 2026)"
      - "at(2026-12-31T23:59:00)" → "cron(59 23 31 12 ? 2026)"
      - "at(2027-01-01T00:00:00)" → "cron(0 0 1 1 ? 2027)"
      - "at(2028-02-29T12:00:00)" → "cron(0 12 29 2 ? 2028)"
      - "rate(5 minutes)" → error containing "expected at("
      - "at(not-a-date)" → error containing "cannot parse"

    Implementation notes:
    - pkg/events/doc.go: existing stub (created in Wave 0). Either remove if it was empty, OR turn into the package's real doc comment.
    - pkg/events/manifest.go:
      - Imports: `_ "embed"`, `fmt`, `gopkg.in/yaml.v3`
      - `//go:embed schemas/event_manifest.schema.json` declaration above `var eventManifestSchemaJSON []byte`
      - EventManifest + Target structs exactly per <interfaces>
      - ParseManifest(data []byte) (EventManifest, error): yaml.Unmarshal into EventManifest, return wrapped error on failure
      - SchemaJSON() []byte: returns eventManifestSchemaJSON
    - pkg/events/parser.go:
      - AtExprToCronForRules(atExpr string) (string, error) per <interfaces> contract
      - Use `strings.HasPrefix(atExpr, "at(")` + `strings.HasSuffix(atExpr, ")")` for surface check, then `time.Parse("2006-01-02T15:04:05", inner)` for the datetime, then fmt.Sprintf the cron form. Year goes last per EventBridge cron syntax.
    - pkg/events/validate.go:
      - regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`) for the slug check (compiled at package init via package-level var)
      - Validate() returns the FIRST error encountered (matches Phase 80 ValidationError style); no aggregation
    - pkg/events/schemas/event_manifest.schema.json: hand-authored draft-07 JSON Schema mirroring the EventManifest struct (required fields: name, target; oneOf: [schedule, event_pattern]; target.type enum: ["km", "arn"]). Doesn't need to be exhaustive — IDE completion is the goal, runtime validation is in Validate().

    AVOID:
    - Implementing a full jsonschema runtime validator — Validate() is the runtime check; SchemaJSON() is for IDE/editor only
    - Aggregating errors — return first failure (matches profile.Validate pattern)
    - Implementing TestAtExprToCronForRules with UTC/timezone logic — the input is "naive datetime, operator's intent"; just parse + format, no TZ math

    REFERENCES:
    - pkg/profile/schema.go — //go:embed pattern; pkg/profile/validate.go — error-style pattern
    - pkg/at/parser.go — ScheduleSpec type for the contract km event will consume in Plan 83-05
    - Wave 0 pkg/events/manifest_test.go and parser_test.go — these tests' enumerated comment blocks are the assertion list to implement against
  </behavior>
  <action>
    1. Open pkg/events/manifest_test.go and parser_test.go (created in Plan 83-01); read the enumerated assertion lists for each test.
    2. If pkg/events/doc.go was a Wave 0 stub, replace with: `// Package events defines EventManifest — the typed schema for events/<name>.yaml manifests\n// consumed by km event add/apply, and the at()→cron conversion needed because\n// EventBridge Rules do not support at() one-off expressions.\npackage events\n`
    3. Write pkg/events/manifest.go with EventManifest + Target structs, ParseManifest, SchemaJSON.
    4. Write pkg/events/schemas/event_manifest.schema.json (draft-07 schema mirroring the struct).
    5. Write pkg/events/parser.go with AtExprToCronForRules.
    6. Write pkg/events/validate.go with the Validate method + slug regex + XOR check + target discriminator.
    7. Write the 5 fixture files under pkg/events/testdata/ with the YAML content described in <behavior>.
    8. Remove all `t.Skip` lines from pkg/events/manifest_test.go and parser_test.go (Plan 83-01 scaffolds — implement them now). Replace each Skip with the actual assertion sequence enumerated in that test's comment block. Use os.ReadFile + filepath.Join("testdata", "X.yaml") for fixture loads.
    9. Run `go test ./pkg/events/... -v -count=1`. All 3 test functions must PASS (not SKIP).
    10. Run `go vet ./pkg/events/...`. Must exit 0.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && go test ./pkg/events/... -v -count=1 2>&1 | grep -E "(PASS|FAIL|TestManifestValidation|TestManifestYAMLRoundTrip|TestAtExprToCronForRules)" && go vet ./pkg/events/...</automated>
  </verify>
  <done>
    `go test ./pkg/events/... -v -count=1` exits 0 with all of TestManifestValidation, TestManifestYAMLRoundTrip, TestAtExprToCronForRules at PASS (not SKIP, not FAIL). `go vet ./pkg/events/...` is clean. pkg/events/schemas/event_manifest.schema.json exists and is valid JSON (verified by `jq . pkg/events/schemas/event_manifest.schema.json` exiting 0).
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Add config.EventConfig + Config.Events field with viper Load wiring</name>
  <files>internal/app/config/config.go</files>
  <behavior>
    - Wave 0 TestEventsConfigRoundTrip (internal/app/config/config_events_test.go) moves from SKIP to PASS.
    - EventConfig struct added directly below or beside ClusterConfig in config.go, with all 7 fields per <interfaces>.
    - Config struct gains: `Events []EventConfig \`mapstructure:"events" yaml:"events"\`` — place beside `Clusters` field.
    - In Load() function: add `v.SetDefault("events", []interface{}{})` near the other SetDefaults (mirror the Clusters setup at line 364 + 238 of config.go).
    - In Load() function: add `v.UnmarshalKey("events", &cfg.Events)` immediately after the Clusters unmarshal (so events: round-trips through the same code path).
    - TestEventsConfigRoundTrip (Wave 0 scaffold) implementation:
      - t.TempDir() for config dir
      - Write km-config.yaml containing the minimal required fields (domain, accounts, region, etc. — mirror config_clusters_test.go's fixture verbatim, just adding the events: block)
      - Add `events:` list with one EventConfig entry: name="nightly-doctor", type="schedule", expression="cron(0 9 * * ? *)", target_type="km", targets=["arn:aws:lambda:us-east-1:123:function:km-runner"], rule_arn="arn:aws:events:us-east-1:123:rule/nightly-doctor", role_arn="arn:aws:iam::123:role/km-event-nightly-doctor-target"
      - Call config.Load(configPath) — assert cfg.Events has 1 entry, cfg.Events[0].Name == "nightly-doctor", cfg.Events[0].RuleARN starts with "arn:aws:events:"

    AVOID:
    - Adding any validation logic in config.go — config.Load() is a dumb loader; validation lives in pkg/events.Validate() and is invoked by km event CLI in Plan 83-05
    - Breaking the existing Clusters wiring — Events code mirrors Clusters EXACTLY; if Clusters works, Events works
    - Mutating any field's mapstructure tag or yaml tag — they're load-bearing for PersistEventsConfig in Plan 83-05

    REFERENCES:
    - internal/app/config/config.go lines 16-26 (ClusterConfig), 188-192 (Clusters field), 238-239 + 361 + 364 (Load wiring) — verbatim pattern
    - internal/app/config/config_clusters_test.go (from Phase 80) — mirror the fixture YAML structure exactly, just add events: block
  </behavior>
  <action>
    1. Read internal/app/config/config.go fully to understand the ClusterConfig + Load() wiring sites.
    2. Add EventConfig struct (use //comment to mark new code: `// EventConfig represents a single registered EventBridge rule managed by km event. (Phase 83)`).
    3. Add `Events []EventConfig \`mapstructure:"events" yaml:"events"\`` field to Config struct.
    4. In Load(): add `v.SetDefault("events", []interface{}{})` next to the Clusters SetDefault.
    5. In Load(): add `v.UnmarshalKey("events", &cfg.Events)` right after the Clusters UnmarshalKey.
    6. Open internal/app/config/config_events_test.go (Wave 0 scaffold). Replace t.Skip with the implementation per <behavior>.
    7. Run `go test ./internal/app/config/ -run TestEventsConfigRoundTrip -v -count=1`. Must PASS.
    8. Run `go test ./internal/app/config/... -count=1` to confirm no regression to existing tests.
    9. Run `go vet ./internal/app/config/...`. Must exit 0.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && go test ./internal/app/config/ -run TestEventsConfigRoundTrip -v -count=1 2>&1 | grep -E "(PASS|FAIL|TestEventsConfigRoundTrip)" && go test ./internal/app/config/... -count=1 && go vet ./internal/app/config/...</automated>
  </verify>
  <done>
    TestEventsConfigRoundTrip PASSes. All other tests in internal/app/config/ continue to pass. `go vet ./internal/app/config/...` clean. The EventConfig struct and Config.Events field are present in config.go.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: Widen km-operator-policy EventBridgePutEvents to include the {prefix}-operator-events custom bus</name>
  <files>infra/modules/km-operator-policy/v1.0.0/main.tf</files>
  <behavior>
    - Find the existing EventBridgePutEvents policy statement (Sid="EventBridgePutEvents", around line 543-546 of main.tf).
    - Change its `Resource` field from a single string to a list of two strings: the original `event-bus/default` ARN AND a new `event-bus/${var.resource_prefix}-operator-events` ARN. This is purely additive — nothing existing breaks.
    - No `moved {}` block needed — we are adding a resource to an existing policy's Resource list, NOT moving/renaming a Terraform resource address.
    - `var.resource_prefix` already exists in the module's variables.tf (used elsewhere); no new variable needed.
    - `terraform init -backend=false` + `terraform validate` in infra/modules/km-operator-policy/v1.0.0/ must exit 0 with no errors.

    AVOID:
    - Creating a NEW separate aws_iam_role_policy resource for the custom bus — adding to the existing one's Resource list is correct (and avoids a Terraform state diff for create-handler, which already uses km-operator-policy).
    - Modifying the Sid or any other field on the existing policy — only the Resource field changes
    - Adding any new variable to variables.tf — var.resource_prefix is already wired

    REFERENCES:
    - infra/modules/km-operator-policy/v1.0.0/main.tf line ~543 — the exact policy statement to mutate
    - infra/modules/km-operator-policy/v1.0.0/variables.tf — confirms var.resource_prefix exists
    - 83-RESEARCH.md "Pitfall 5: km-operator-policy's EventBridgePutEvents scoped to default bus only"
  </behavior>
  <action>
    1. Read infra/modules/km-operator-policy/v1.0.0/main.tf focusing on lines 530-555 to find the EventBridgePutEvents block.
    2. Confirm via `grep "resource_prefix" infra/modules/km-operator-policy/v1.0.0/variables.tf` that var.resource_prefix exists.
    3. Replace the single-string Resource value with a list of two strings (keep the original first to preserve diff readability, add the operator-events ARN second):
       ```hcl
       Resource = [
         "arn:aws:events:*:${data.aws_caller_identity.current.account_id}:event-bus/default",
         "arn:aws:events:*:${data.aws_caller_identity.current.account_id}:event-bus/${var.resource_prefix}-operator-events",
       ]
       ```
    4. Run `cd infra/modules/km-operator-policy/v1.0.0/ && terraform init -backend=false` (download providers; idempotent — already done in repo).
    5. Run `cd infra/modules/km-operator-policy/v1.0.0/ && terraform validate`. Exit 0.
    6. (Optional sanity, no commit impact) Run `terraform fmt -check -diff infra/modules/km-operator-policy/v1.0.0/main.tf` — if it fails, run `terraform fmt` to fix whitespace.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02/infra/modules/km-operator-policy/v1.0.0 && terraform init -backend=false &gt; /dev/null 2>&1 && terraform validate 2>&1 && grep -c "operator-events" main.tf</automated>
  </verify>
  <done>
    `terraform validate` in infra/modules/km-operator-policy/v1.0.0/ exits 0 ("Success! The configuration is valid."). `grep -c "operator-events" infra/modules/km-operator-policy/v1.0.0/main.tf` returns at least 1. The EventBridgePutEvents Resource is now a list containing both the default bus and the {prefix}-operator-events custom bus.
  </done>
</task>

</tasks>

<verification>
- `go build ./...` exits 0
- `go test ./pkg/events/... -count=1` exits 0; TestManifestValidation, TestManifestYAMLRoundTrip, TestAtExprToCronForRules all PASS
- `go test ./internal/app/config/... -count=1` exits 0; TestEventsConfigRoundTrip PASS; no regression in existing tests
- `terraform validate` in infra/modules/km-operator-policy/v1.0.0/ exits 0
- The 5 testdata fixtures exist in pkg/events/testdata/
- The JSON Schema at pkg/events/schemas/event_manifest.schema.json is embedded via //go:embed
</verification>

<success_criteria>
- pkg/events/ package compiles, validates manifests correctly, converts at()→cron correctly
- config.EventConfig + Config.Events field load km-config.yaml events: round-trip cleanly
- km-operator-policy/v1.0.0/ EventBridgePutEvents widened to the custom bus, validates clean
- All Wave 0 tests at pkg/events/ and internal/app/config/ that target Phase 83 functionality now PASS
</success_criteria>

<output>
After completion, create `.planning/phases/83-km-event-operator-controlled-eventbridge/83-02-SUMMARY.md` documenting:
- The exact final EventManifest + Target + EventConfig struct definitions (for Plan 83-05 to import without re-reading)
- Whether any Wave 0 stubs (pkg/events/doc.go, cmd/km-runner/doc.go) were retained/modified
- The exact two-line Resource list of the EventBridgePutEvents policy after the update (for Plan 83-03 / 83-04 to reference)
- The JSON Schema file path so Plan 83-05's `km event apply` knows where to embed the SchemaJSON() byte slice
</output>
</content>
