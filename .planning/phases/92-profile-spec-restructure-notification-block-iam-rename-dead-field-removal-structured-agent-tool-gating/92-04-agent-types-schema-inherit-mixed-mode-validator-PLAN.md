---
phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating
plan: 4
type: execute
wave: 4
depends_on: [0, 1]
files_modified:
  - pkg/profile/types.go
  - pkg/profile/schemas/sandbox_profile.schema.json
  - pkg/profile/inherit.go
  - pkg/profile/validate.go
  - pkg/profile/validate_mixed_settings_test.go
  - internal/app/cmd/create.go
  - internal/app/cmd/agent.go
  - internal/app/cmd/shell.go
  - internal/app/cmd/doctor.go
  - internal/app/cmd/doctor_codex.go
  - pkg/compiler/userdata.go
autonomous: true
requirements: []
verifies: [VC-1, VC-6]

must_haves:
  truths:
    - "New `spec.agent:` block exists in types + schema: `Default string`, `Claude *AgentClaudeSpec`, `Codex *AgentCodexSpec`. NOT in `spec.required` (optional)."
    - "`AgentClaudeSpec` typed fields: `TrustedDirectories []string`, `Tools AgentToolsSpec`, `Permissions map[string]any` (the one passthrough exception), `Args []string`."
    - "`AgentCodexSpec` typed fields: `Tools AgentToolsSpec`, `Args []string`."
    - "`AgentToolsSpec`: `AutoApprove []string`, `Deny []string`."
    - "JSON schema `agent.claude.permissions` is `additionalProperties: true` (only passthrough exception per locked decision)."
    - "`CLISpec.Agent`, `CLISpec.ClaudeArgs`, `CLISpec.CodexArgs` deleted. After this wave, ONLY `CLISpec.NoBedrock` remains."
    - "Typed `mergeAgentSpec(parent, child *AgentSpec) *AgentSpec` field-level nil-aware merge handles `Default`, `Claude`, `Codex` correctly."
    - "Mixed-mode validator: profile with `agent.claude.tools.autoApprove` (non-empty) AND `execution.configFiles[\"/home/sandbox/.claude/settings.json\"]` present → hard ValidationError."
    - "Per-invoke flag handling in `create.go`/`agent.go`/`shell.go`: `--claude`/`--codex` reads `Spec.Agent.Default`; `claudeArgs`/`codexArgs` read from `Spec.Agent.Claude.Args`/`Spec.Agent.Codex.Args`."
    - "Slack inbound dispatcher per-message prefix routing (Phase 70) reads `Spec.Agent.Default`."
    - "Wave 0's `TestValidate_MixedAgentClaudeAndInlinedConfigFiles_Errors` (VC-6) is GREEN. Build tag `phase92_wave4` removed."
    - "Wave 4 does NOT yet ship the synthesizer (Wave 5 owns it). Profile fixtures still inline `configFiles[\".claude/settings.json\"]` after this wave; Wave 5 removes those entries and populates `agent.claude.tools.*`."
  artifacts:
    - path: "pkg/profile/types.go"
      provides: "AgentSpec (new shape), AgentClaudeSpec, AgentCodexSpec, AgentToolsSpec; CLISpec contains only NoBedrock."
      contains: "type AgentSpec struct"
    - path: "pkg/profile/schemas/sandbox_profile.schema.json"
      provides: "`agent` block (optional); `cli.agent`/`cli.claudeArgs`/`cli.codexArgs` removed."
      contains: "\"agent\":"
    - path: "pkg/profile/inherit.go"
      provides: "Typed mergeAgentSpec (and sub-mergers) — second typed merger for pointer-typed sections."
      contains: "mergeAgentSpec"
    - path: "pkg/profile/validate.go"
      provides: "Mixed-mode validator: autoApprove + inlined configFiles → ValidationError."
      contains: "autoApprove"
  key_links:
    - from: "pkg/profile/inherit.go"
      to: "pkg/profile/types.go (AgentSpec)"
      via: "mergeAgentSpec consumes AgentSpec struct"
      pattern: "AgentSpec"
    - from: "pkg/profile/schemas/sandbox_profile.schema.json"
      to: "pkg/profile/types.go (AgentSpec)"
      via: "Schema agent block mirrors AgentSpec struct shape"
      pattern: "\"agent\""
    - from: "internal/app/cmd/create.go"
      to: "pkg/profile/types.go (Spec.Agent.Default + Spec.Agent.Claude.Args)"
      via: "--claude/--codex flag reads default; args read from typed fields"
      pattern: "Spec\\.Agent"
    - from: "pkg/profile/validate_mixed_settings_test.go"
      to: "pkg/profile/validate.go (mixed-mode validator)"
      via: "Wave 0 RED test removes build tag and runs against the new validator"
      pattern: "validate_mixed"
---

<objective>
Land the new `spec.agent:` block as types + schema + typed inheritance merger + mixed-mode validation. Wave 4 does NOT yet ship the synthesizer (Wave 5 owns `pkg/compiler/agent_claude.go` + `pkg/compiler/agent_codex.go`). After Wave 4 lands, profile fixtures still have the inlined `configFiles[".claude/settings.json"]` JSON — Wave 5 removes those entries and populates `agent.claude.tools.*`.

This wave finishes the CLISpec cleanup: removes the 3 remaining agent-related fields (`Agent`, `ClaudeArgs`, `CodexArgs`). After this wave, CLISpec contains ONLY `NoBedrock`.

Per CONTEXT.md locked decision on permissions: `agent.claude.permissions` is `map[string]any` passthrough — the one exception to "type aggressively." Use only for Claude-Code settings.json keys that aren't worth typing individually (e.g., per-release additions). Document the well-known keys in Wave 5's `docs/agent-tool-gating.md`.

Per RESEARCH.md §5d/§5e: synthesizer must emit `permissions.allow` + `permissions.deny` (NOT legacy `autoApprove`, NOT `disallowedTools`). This wave does NOT synthesize — but the schema + types must define the fields with the correct names so Wave 5 can read them.

Per RESEARCH.md §4c — CLI sites that read `Spec.CLI.Agent`/`ClaudeArgs`/`CodexArgs` (must migrate in this wave):
- `internal/app/cmd/agent.go`
- `internal/app/cmd/create.go` (if it reads agent default for CLI routing)
- `internal/app/cmd/shell.go` (if it routes `--claude`/`--codex` flags)
- `internal/app/cmd/doctor.go:3346` — `prof.Spec.CLI.Agent`
- `internal/app/cmd/doctor_codex.go:260` — `prof.Spec.CLI.Agent`

Plus userdata.go lines 3866 + 4038 (the two `.CLI.Agent` reads Wave 3 left alone).

Output: 2 types files + 1 schema file + 1 inherit file + 1 validator file + 5 CLI cmd files + 1 compiler file.
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-CONTEXT.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-RESEARCH.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-VALIDATION.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-01-structural-cleanup-iam-rename-dead-field-removal-PLAN.md
@.planning/research/codex-config-toml.md
@pkg/profile/types.go
@pkg/profile/inherit.go
@pkg/profile/schemas/sandbox_profile.schema.json
@pkg/profile/validate_mixed_settings_test.go
@internal/app/cmd/agent.go
@internal/app/cmd/doctor.go
@internal/app/cmd/doctor_codex.go

<interfaces>
Target types added in this wave:

  type AgentSpec struct {
      Default string           `json:"default,omitempty" yaml:"default,omitempty"`
      Claude  *AgentClaudeSpec `json:"claude,omitempty"  yaml:"claude,omitempty"`
      Codex   *AgentCodexSpec  `json:"codex,omitempty"   yaml:"codex,omitempty"`
  }

  type AgentClaudeSpec struct {
      TrustedDirectories []string       `json:"trustedDirectories,omitempty" yaml:"trustedDirectories,omitempty"`
      Tools              AgentToolsSpec `json:"tools,omitempty" yaml:"tools,omitempty"`
      Permissions        map[string]any `json:"permissions,omitempty" yaml:"permissions,omitempty"`
      Args               []string       `json:"args,omitempty" yaml:"args,omitempty"`
  }

  type AgentCodexSpec struct {
      Tools AgentToolsSpec `json:"tools,omitempty" yaml:"tools,omitempty"`
      Args  []string       `json:"args,omitempty" yaml:"args,omitempty"`
  }

  type AgentToolsSpec struct {
      AutoApprove []string `json:"autoApprove,omitempty" yaml:"autoApprove,omitempty"`
      Deny        []string `json:"deny,omitempty" yaml:"deny,omitempty"`
  }

  // Field on Spec (new):
  //   Agent *AgentSpec   // optional, NOT in spec.required
  // Deleted from CLISpec (Wave 4):
  //   Agent, ClaudeArgs, CodexArgs
  // Surviving CLISpec fields after Wave 4: NoBedrock only.

JSON schema target (additions to existing schema):

  "agent": {
    "type": "object",
    "additionalProperties": false,
    "properties": {
      "default": { "type": "string", "enum": ["claude", "codex"] },
      "claude": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "trustedDirectories": { "type": "array", "items": { "type": "string" } },
          "tools": {
            "type": "object",
            "additionalProperties": false,
            "properties": {
              "autoApprove": { "type": "array", "items": { "type": "string" } },
              "deny":        { "type": "array", "items": { "type": "string" } }
            }
          },
          "permissions": { "type": "object", "additionalProperties": true },
          "args":        { "type": "array",  "items": { "type": "string" } }
        }
      },
      "codex": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "tools": {
            "type": "object",
            "additionalProperties": false,
            "properties": {
              "autoApprove": { "type": "array", "items": { "type": "string" } },
              "deny":        { "type": "array", "items": { "type": "string" } }
            }
          },
          "args": { "type": "array", "items": { "type": "string" } }
        }
      }
    }
  }
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Add new AgentSpec types + schema; delete 3 CLISpec fields</name>
  <files>
    pkg/profile/types.go,
    pkg/profile/schemas/sandbox_profile.schema.json
  </files>
  <behavior>
    - After this task: 4 new types in types.go (AgentSpec, AgentClaudeSpec, AgentCodexSpec, AgentToolsSpec).
    - Spec.Agent *AgentSpec field exists (Wave 1 deleted the dead Spec.Agent AgentSpec; this re-introduces with new shape).
    - CLISpec reduced to just NoBedrock bool.
    - JSON schema has new agent block (optional) and removes cli.agent, cli.claudeArgs, cli.codexArgs.
    - Schema's agent.claude.permissions has additionalProperties: true (only passthrough exception).
    - go vet ./pkg/profile/... succeeds (pkg/profile compiles in isolation).
    - go build ./... is EXPECTED to fail at this point because pkg/compiler/userdata.go + internal/app/cmd/{agent,doctor,doctor_codex,...} still read Spec.CLI.Agent. Tasks 3-4 fix.
  </behavior>
  <action>
**pkg/profile/types.go**:
1. Add 4 new types from Interfaces section. Place near other Spec sub-types.
2. Add Agent *AgentSpec field to type Spec struct.
3. From type CLISpec struct: delete Agent string, ClaudeArgs []string, CodexArgs []string.
4. CLISpec now has ONLY: NoBedrock *bool (or bool, whichever was current). Per CONTEXT.md locked decision: cli.noBedrock survives. Per RESEARCH.md Pitfall 6: single-field struct is fine; keep as struct (don't collapse to scalar spec.noBedrock — keeps naming consistency).

**pkg/profile/schemas/sandbox_profile.schema.json**:
1. Inside properties.spec.properties: add the agent definition from Interfaces section.
2. agent does NOT go in spec.required (optional per locked decision).
3. Inside properties.spec.properties.cli.properties: delete agent, claudeArgs, codexArgs properties.
4. If cli.required lists any of those, remove.
5. Verify schema parses: node -e "JSON.parse(require('fs').readFileSync('pkg/profile/schemas/sandbox_profile.schema.json'))".

Commit message: `feat(92-04): add AgentSpec + sub-types + schema; delete cli.agent/claudeArgs/codexArgs (CLISpec now NoBedrock only)`.
  </action>
  <verify>
    <automated>node -e "JSON.parse(require('fs').readFileSync('pkg/profile/schemas/sandbox_profile.schema.json'))" &amp;&amp; go vet ./pkg/profile/...</automated>
    Expected: schema is valid JSON; pkg/profile compiles. VC-1 partial.
  </verify>
  <done>
    4 new types + schema additions committed; CLISpec is single-field; pkg/profile vet passes.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Add typed mergeAgentSpec to inherit.go</name>
  <files>pkg/profile/inherit.go</files>
  <behavior>
    - After this task: mergeAgentSpec(parent, child *AgentSpec) *AgentSpec and sub-mergers exist in inherit.go.
    - Field-level nil-aware merge: child non-nil fields override parent; nil fields inherit parent's.
    - ResolveInheritance calls mergeAgentSpec for the Spec.Agent field.
    - Tests for inheritance of the new agent block pass (write at least one — parallel to Wave 2's notification inheritance test).
  </behavior>
  <action>
Add to pkg/profile/inherit.go after the mergeNotificationSpec block:

  func mergeAgentSpec(parent, child *AgentSpec) *AgentSpec {
      if parent == nil { return child }
      if child  == nil { return parent }
      return &AgentSpec{
          Default: pickString(parent.Default, child.Default),
          Claude:  mergeAgentClaudeSpec(parent.Claude, child.Claude),
          Codex:   mergeAgentCodexSpec (parent.Codex,  child.Codex),
      }
  }

  func mergeAgentClaudeSpec(parent, child *AgentClaudeSpec) *AgentClaudeSpec {
      if parent == nil { return child }
      if child  == nil { return parent }
      out := &AgentClaudeSpec{
          TrustedDirectories: parent.TrustedDirectories,
          Tools:              mergeAgentToolsSpec(parent.Tools, child.Tools),
          Permissions:        mergePermissionsPassthrough(parent.Permissions, child.Permissions),
          Args:               parent.Args,
      }
      if len(child.TrustedDirectories) > 0 { out.TrustedDirectories = child.TrustedDirectories }
      if len(child.Args)               > 0 { out.Args               = child.Args }
      return out
  }

  func mergeAgentCodexSpec(parent, child *AgentCodexSpec) *AgentCodexSpec {
      if parent == nil { return child }
      if child  == nil { return parent }
      out := &AgentCodexSpec{
          Tools: mergeAgentToolsSpec(parent.Tools, child.Tools),
          Args:  parent.Args,
      }
      if len(child.Args) > 0 { out.Args = child.Args }
      return out
  }

  func mergeAgentToolsSpec(parent, child AgentToolsSpec) AgentToolsSpec {
      // value-typed; child non-empty slices replace parent's
      out := parent
      if len(child.AutoApprove) > 0 { out.AutoApprove = child.AutoApprove }
      if len(child.Deny)        > 0 { out.Deny        = child.Deny }
      return out
  }

  func mergePermissionsPassthrough(parent, child map[string]any) map[string]any {
      if parent == nil { return child }
      if child  == nil { return parent }
      // top-level key merge; child wins on collision
      out := make(map[string]any, len(parent)+len(child))
      for k, v := range parent { out[k] = v }
      for k, v := range child  { out[k] = v }
      return out
  }

Update ResolveInheritance to call mergeAgentSpec after the bulk result.Spec = child.Spec line:

  result.Spec.Agent = mergeAgentSpec(parent.Spec.Agent, child.Spec.Agent)

Add inherit_test.go cases (or extend existing inherit_test.go):
- TestInheritAgent_DefaultOnlyChildInheritsParentClaude — child sets only Default: "claude"; parent has full Claude.Tools.AutoApprove; merged keeps both.
- TestInheritAgent_ChildEmptyArgsInheritsParent — child has no Args; parent has ["--dangerously-skip-permissions"]; merged keeps parent's.
- TestInheritAgent_PermissionsPassthroughKeyMerge — parent has permissions: {a: 1}; child has permissions: {b: 2}; merged has both.

Commit message: `feat(92-04): add typed mergeAgentSpec + sub-mergers — second pointer-merge inheritance fix`.
  </action>
  <verify>
    <automated>go test ./pkg/profile/ -run TestInheritAgent -count=1 -v</automated>
    Expected: agent inheritance tests GREEN. VC-1.
  </verify>
  <done>
    4 new merger functions + ResolveInheritance call; inherit_test.go has new agent inheritance cases passing.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: Add mixed-mode validator + turn Wave 0 VC-6 RED stub GREEN</name>
  <files>
    pkg/profile/validate.go,
    pkg/profile/validate_mixed_settings_test.go
  </files>
  <behavior>
    - After this task: a new semantic validator rejects profiles that populate agent.claude.tools.autoApprove (non-empty) AND inline execution.configFiles[".claude/settings.json"] or "/home/sandbox/.claude/settings.json". Hard ValidationError; no merge fallback (per locked decision).
    - Wave 0's TestValidate_MixedAgentClaudeAndInlinedConfigFiles_Errors (VC-6) is GREEN.
    - Build tag phase92_wave4 removed from validate_mixed_settings_test.go.
    - Error path is descriptive: includes both the agent path and the configFiles path; suggests the operator pick one mode.
  </behavior>
  <action>
In pkg/profile/validate.go (or wherever semantic validators register), add:

  func validateAgentClaudeNoMixedMode(p *Profile) error {
      if p.Spec.Agent == nil || p.Spec.Agent.Claude == nil {
          return nil
      }
      autoApprove := p.Spec.Agent.Claude.Tools.AutoApprove
      deny        := p.Spec.Agent.Claude.Tools.Deny
      if len(autoApprove) == 0 && len(deny) == 0 {
          return nil
      }
      if p.Spec.Execution == nil || p.Spec.Execution.ConfigFiles == nil {
          return nil
      }
      candidates := []string{
          "/home/sandbox/.claude/settings.json",
          "~/.claude/settings.json",
          ".claude/settings.json",
      }
      for _, path := range candidates {
          if _, ok := p.Spec.Execution.ConfigFiles[path]; ok {
              return &ValidationError{
                  Field:   "spec.execution.configFiles[\"" + path + "\"]",
                  Message: "cannot inline Claude settings.json when spec.agent.claude.tools is populated; pick one mode (typed agent.claude.tools.* OR inlined configFiles, not both)",
                  IsWarning: false,
              }
          }
      }
      return nil
  }

Wire it into the orchestrator's semantic-validator list so it runs as part of ValidateSemantic.

Update pkg/profile/validate_mixed_settings_test.go:
1. Remove //go:build phase92_wave4 line + // +build phase92_wave4 line.
2. Run: go test ./pkg/profile/ -run TestValidate_MixedAgentClaudeAndInlinedConfigFiles_Errors -count=1 -v. Expected: GREEN.

If the Wave 0 stub asserted error text that doesn't match this validator's text, prefer updating the validator's text to match the test's expectations. Otherwise update the test to match the validator. Either way, the test must pass.

Commit message: `feat(92-04): add mixed-mode validator (agent.claude.tools + inlined configFiles) — VC-6 GREEN`.
  </action>
  <verify>
    <automated>go test ./pkg/profile/ -run TestValidate_MixedAgentClaudeAndInlinedConfigFiles_Errors -count=1 -v</automated>
    Expected: GREEN. VC-6.
  </verify>
  <done>
    Mixed-mode validator wired in; VC-6 RED stub now GREEN; build tag removed.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 4: Migrate CLI cmd + userdata.go remaining Spec.CLI.Agent/ClaudeArgs/CodexArgs reads</name>
  <files>
    internal/app/cmd/create.go,
    internal/app/cmd/agent.go,
    internal/app/cmd/shell.go,
    internal/app/cmd/doctor.go,
    internal/app/cmd/doctor_codex.go,
    pkg/compiler/userdata.go
  </files>
  <behavior>
    - All remaining Spec.CLI.Agent / Spec.CLI.ClaudeArgs / Spec.CLI.CodexArgs reads in the codebase are migrated.
    - internal/app/cmd/agent.go: --claude/--codex flag handling reads Spec.Agent.Default; --prompt-related args read from Spec.Agent.Claude.Args / Spec.Agent.Codex.Args.
    - internal/app/cmd/create.go + shell.go: same per-invoke routing.
    - pkg/compiler/userdata.go lines 3866 + 4038 (the two .CLI.Agent reads Wave 3 left): now read from Spec.Agent.Default. KM_AGENT env var name UNCHANGED.
    - Slack inbound dispatcher: per-message prefix routing reads Spec.Agent.Default.
    - doctor_codex.go:260 + doctor.go:3346: each prof.Spec.CLI.Agent becomes prof.Spec.Agent.Default (nil-safe).
    - After this task: go build ./... GREEN.
    - After this task: the Wave 0 byte-identity tests are GREEN (compiler still emits same KM_AGENT env var from same value, just sourced differently).
  </behavior>
  <action>
**Audit first**:
  grep -rn 'Spec\.CLI\.Agent\|Spec\.CLI\.ClaudeArgs\|Spec\.CLI\.CodexArgs\|CLI\.Agent\b\|CLI\.ClaudeArgs\b\|CLI\.CodexArgs\b' --include='*.go' .

Should find ~6-10 sites across the listed files.

**Migration pattern** (nil-safe helper):

  // Helper at top of compiler/userdata.go (or in a shared util):
  func agentDefault(p *profile.Profile) string {
      if p.Spec.Agent == nil { return "" }
      return p.Spec.Agent.Default
  }
  func agentClaudeArgs(p *profile.Profile) []string {
      if p.Spec.Agent == nil || p.Spec.Agent.Claude == nil { return nil }
      return p.Spec.Agent.Claude.Args
  }
  func agentCodexArgs(p *profile.Profile) []string {
      if p.Spec.Agent == nil || p.Spec.Agent.Codex == nil { return nil }
      return p.Spec.Agent.Codex.Args
  }

Then each call site:

  // OLD: if p.Spec.CLI != nil && p.Spec.CLI.Agent == "codex" { ... }
  // NEW: if agentDefault(p) == "codex" { ... }

  // OLD: claudeArgs := p.Spec.CLI.ClaudeArgs
  // NEW: claudeArgs := agentClaudeArgs(p)

**Specific sites**:
- pkg/compiler/userdata.go:3866 — codex check. Replace p.Spec.CLI.Agent with agentDefault(p).
- pkg/compiler/userdata.go:4038 — KM_AGENT emission. Same replacement. Env var value remains the agent name string.
- internal/app/cmd/agent.go — --claude / --codex flag handling. The flags should set Spec.Agent.Default in memory (or compute a per-invoke "effective agent" by precedence: flag > profile default > "claude"). --prompt related arg reads use agentClaudeArgs(p) / agentCodexArgs(p).
- internal/app/cmd/create.go — if it reads agent default to decide post-create steps, update.
- internal/app/cmd/shell.go — if km shell checks the agent default for shell prompts/behavior, update.
- internal/app/cmd/doctor.go:3346 — prof.Spec.CLI.Agent becomes agentDefault(prof).
- internal/app/cmd/doctor_codex.go:260 — same. The doctor_codex.go check identifies Codex sandboxes for codex-specific health checks; logic preserved, only the read path changes.

**Slack inbound dispatcher prefix routing** (per CONTEXT.md §Wave 4):
- Search for claude: / codex: prefix-routing logic (likely in pkg/compiler/userdata.go heredoc for the Slack inbound poller or in a Lambda handler).
- If the routing reads Spec.CLI.Agent, update to Spec.Agent.Default.
- Per Phase 70 §docs/codex-parity.md: per-message prefix overrides the profile default for that turn; 8-step clean handoff applies when crossing agent boundary in an existing thread. Logic preserved.

**End-of-task audit**:
  grep -rn 'Spec\.CLI\.Agent\|Spec\.CLI\.ClaudeArgs\|Spec\.CLI\.CodexArgs' --include='*.go' .
Expected: ZERO matches.

Commit message: `feat(92-04): migrate .CLI.Agent/ClaudeArgs/CodexArgs to Spec.Agent.* across cmd + compiler (last CLISpec fields gone)`.
  </action>
  <verify>
    <automated>go build ./... &amp;&amp; go test ./... -count=1</automated>
    Expected: full repo build GREEN; all tests pass. Wave 0 byte-identity tests STILL GREEN — VC-3 + VC-4 carry through. VC-1.
  </verify>
  <done>
    Zero Spec.CLI.Agent / ClaudeArgs / CodexArgs reads remain; go build ./... succeeds; doctor/codex checks still find Codex sandboxes via new path.
  </done>
</task>

</tasks>

<verification>
- node -e "JSON.parse(require('fs').readFileSync('pkg/profile/schemas/sandbox_profile.schema.json'))" confirms schema is valid JSON.
- `go build ./...` GREEN — VC-1.
- `go test ./...` GREEN.
- `go test ./pkg/profile/ -run TestValidate_MixedAgentClaudeAndInlinedConfigFiles_Errors` GREEN — VC-6.
- `go test ./pkg/profile/ -run TestInheritAgent` GREEN — agent inheritance fix.
- Wave 0 byte-identity tests still GREEN — Wave 4 doesn't change emitted userdata or IAM HCL.
- Grep audit: zero `Spec.CLI.Agent`/`.ClaudeArgs`/`.CodexArgs` reads across the codebase.
</verification>

<success_criteria>
- 4 new types (AgentSpec, AgentClaudeSpec, AgentCodexSpec, AgentToolsSpec) added.
- CLISpec reduced to NoBedrock only.
- Schema has new `agent` block (optional); `cli.agent`/`claudeArgs`/`codexArgs` removed.
- `mergeAgentSpec` (+ 4 sub-mergers + permissions passthrough) added.
- Mixed-mode validator wired in; VC-6 RED stub GREEN.
- 5 internal/app/cmd files migrated; 2 userdata.go sites migrated.
- After this wave, fixtures STILL inline `configFiles[".claude/settings.json"]` — Wave 5 removes those and populates `agent.claude.tools.*`. Mixed-mode validator does NOT fire on current fixtures because `agent.claude.tools.autoApprove` is still empty.
</success_criteria>

<output>
After completion, create `.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-04-SUMMARY.md` capturing:
- The 4 new types in types.go with file/line.
- The new schema `agent` block.
- The 4 new merger functions in inherit.go and the bug they fix.
- The mixed-mode validator and its trigger conditions.
- All grep-confirmed `Spec.CLI.Agent` migration sites with file/line.
- Confirmation: CLISpec now has only NoBedrock.
- Wave 5 handoff note: synthesizer + fixture rewrite + docs come next; mixed-mode validator currently passive on all fixtures (no profile has populated `agent.claude.tools.autoApprove` yet).
</output>
