---
phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating
plan: 5
type: execute
wave: 5
depends_on: [0, 4]
files_modified:
  - pkg/compiler/agent_claude.go
  - pkg/compiler/agent_codex.go
  - pkg/compiler/userdata.go
  - pkg/compiler/agent_claude_golden_test.go
  - pkg/compiler/agent_codex_golden_test.go
  - pkg/compiler/testdata/claude_settings_learn_v2.golden.json
  - pkg/compiler/testdata/claude_settings_dc34.golden.json
  - pkg/compiler/testdata/claude_settings_locked.golden.json
  - pkg/compiler/testdata/claude_settings_codex.golden.json
  - pkg/compiler/testdata/codex_config_codex.golden.toml
  - profiles/ao.yaml
  - profiles/codex.yaml
  - profiles/dc34.yaml
  - profiles/dc34.ami.yaml
  - profiles/example-additional-snapshots.yaml
  - profiles/goose.yaml
  - profiles/learn.v2.yaml
  - profiles/learn.v2.chatty.yaml
  - profiles/learn.v2.codex.yaml
  - profiles/learn.v2.polite.yaml
  - profiles/locked.yaml
  - profiles/locked.ami.yaml
  - pkg/profile/builtins/ao.yaml
  - pkg/profile/builtins/codex.yaml
  - pkg/profile/builtins/goose.yaml
  - pkg/profile/builtins/hardened.yaml
  - pkg/profile/builtins/learn.yaml
  - pkg/profile/builtins/open-dev.yaml
  - pkg/profile/builtins/restricted-dev.yaml
  - pkg/profile/builtins/sealed.yaml
  - docs/agent-tool-gating.md
  - docs/codex-parity.md
  - CLAUDE.md
autonomous: true
requirements: []
verifies: [VC-1, VC-3, VC-5, VC-11]

must_haves:
  truths:
    - "New `pkg/compiler/agent_claude.go` with `synthesizeClaudeSettings(agent *profile.AgentSpec) (map[string]any, error)` produces canonical `permissions.allow` / `permissions.deny` shape (NOT legacy `autoApprove`; NOT `disallowedTools`)."
    - "New `pkg/compiler/agent_codex.go` with `synthesizeCodexConfig(agent *profile.AgentSpec) (string, error)` emits existing inert hook blocks + args echo. Per RESEARCH.md Wave 0 spike: Codex 0.133 has no native tool gating; synthesizer is honest about this."
    - "`pkg/compiler/userdata.go` userdata pipeline: (1) `synthesizeClaudeSettings` → (2) `mergeNotifyHookIntoSettings` → (3) write to `/home/sandbox/.claude/settings.json`. Order is preserved and tested in both directions."
    - "Codex pipeline parallel: (1) `synthesizeCodexConfig` → (2) merge notify hook entries (currently inert) → (3) write to `/home/sandbox/.codex/config.toml`."
    - "All 20 profile YAMLs: inlined `configFiles[\"/home/sandbox/.claude/settings.json\"]` strings REMOVED; equivalent semantics encoded as `agent.claude.tools.autoApprove` + `trustedDirectories` + `permissions` passthrough. `cli.agent` / `cli.claudeArgs` / `cli.codexArgs` MOVED to `agent.default` / `agent.claude.args` / `agent.codex.args`."
    - "`scripts/validate-all-profiles.sh` still exits 0 against all 20 profiles."
    - "Wave 0's synthesizer golden tests (`TestSynthesizeClaudeSettingsGolden` 4 fixtures + `TestSynthesizeCodexConfigGolden`) are GREEN — VC-5. Build tag `phase92_wave5` removed."
    - "Wave 0's byte-identity test (`TestUserdataLearnV2Phase92ByteIdentity`, VC-3) is STILL GREEN — pipeline emits the same userdata as pre-Phase-92 main even though the Claude settings.json content is now synthesized rather than inlined."
    - "`docs/agent-tool-gating.md` (new) documents agent block, claude/codex symmetry, synthesis, deny semantics, no-merge-with-configFiles rule. Includes Codex asymmetry note (per RESEARCH.md Wave 0 spike)."
    - "`docs/codex-parity.md` updated: remove `inert ~/.codex/config.toml` note; document synthesizer with asymmetry callout."
    - "`pkg/profile/configfiles/` NOT touched — per RESEARCH.md §2f the directory does not exist; CONTEXT.md Claude's Discretion item is moot."
  artifacts:
    - path: "pkg/compiler/agent_claude.go"
      provides: "synthesizeClaudeSettings — typed AgentClaudeSpec → permissions.allow/deny + trustedDirectories + passthrough."
      min_lines: 60
      contains: "permissions"
    - path: "pkg/compiler/agent_codex.go"
      provides: "synthesizeCodexConfig — inert hooks + args echo + logged note when tools populated."
      min_lines: 50
      contains: "hooks"
    - path: "docs/agent-tool-gating.md"
      provides: "Operator-facing guide for the new agent block."
      min_lines: 80
      contains: "agent.claude.tools"
  key_links:
    - from: "pkg/compiler/userdata.go"
      to: "pkg/compiler/agent_claude.go"
      via: "userdata pipeline calls synthesizeClaudeSettings then mergeNotifyHookIntoSettings"
      pattern: "synthesizeClaudeSettings"
    - from: "pkg/compiler/agent_claude.go"
      to: "Claude Code 2.1.132 settings.json schema"
      via: "emits permissions.allow / permissions.deny (canonical per Wave 0 research)"
      pattern: "permissions"
    - from: "pkg/compiler/agent_codex.go"
      to: ".planning/research/codex-config-toml.md"
      via: "honors Wave 0 research: no real tool gating; inert hooks + args echo"
      pattern: "hooks"
---

<objective>
The synthesis wave. Wave 4 introduced the `agent:` block as typed YAML; Wave 5 makes the compiler USE it. Two new files (`agent_claude.go`, `agent_codex.go`) replace the inlined-JSON antipattern in profile fixtures with typed-field synthesis.

Per RESEARCH.md §1a / §1b — locked synthesizer scope:

**synthesizeClaudeSettings**:
- Emit `permissions.allow` (NOT legacy `autoApprove` — canonical Claude Code 2.1.132 form per Wave 0 research).
- Emit `permissions.deny` (NOT `disallowedTools` — that's a CLI flag, not a settings.json key).
- Top-level `trustedDirectories` preserved as a top-level key.
- `agent.claude.permissions` map passthrough: deep-merge into the output `permissions` object (operator-typed `permissions: {ask: [...]}` etc. flows through without schema update per release).
- Pipeline order: synthesize → mergeNotifyHookIntoSettings → write. Tested both directions.

**synthesizeCodexConfig**:
- Emit existing inert `[[hooks.PermissionRequest]]` + `[[hooks.Stop]]` blocks (forward-compat — Codex 0.133 ignores; future release may honor).
- Echo `args` via `approval_policy` / `sandbox_mode` if those flags appear in `agent.codex.args`.
- When `agent.codex.tools.autoApprove` / `deny` are non-empty: emit a `# NOTE:` comment block in the toml saying "Codex 0.133 does not support tool gating in config.toml. Fields preserved for future release support."
- Log the same notice at compile time so operators see it.

After this wave, the entire phase is done modulo Wave 6 UAT.

Output: 2 new compiler files + 1 modified compiler file + 20 fixture rewrites + 5 golden files + 3 docs.
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
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-04-agent-types-schema-inherit-mixed-mode-validator-PLAN.md
@.planning/research/codex-config-toml.md
@pkg/compiler/userdata.go
@pkg/compiler/agent_claude_golden_test.go
@pkg/compiler/agent_codex_golden_test.go
@docs/codex-parity.md
@profiles/learn.v2.yaml
@profiles/codex.yaml

<interfaces>
Synthesizer contracts (new in this wave):

  // pkg/compiler/agent_claude.go
  //
  // Builds a Claude Code settings.json map from typed AgentClaudeSpec.
  // Emits canonical permissions.allow / permissions.deny (NOT legacy autoApprove).
  // passthrough merges agent.claude.permissions[k] into output.
  //
  // Returns map[string]any so caller (mergeNotifyHookIntoSettings) can deep-merge
  // hooks before serializing to JSON.
  func synthesizeClaudeSettings(agent *profile.AgentSpec) (map[string]any, error)

Expected output shape (per Wave 0 research):

  {
    "permissions": {
      "allow": ["Bash", "Read", "Write", "Edit", "Glob", "Grep", "WebFetch", "WebSearch", "NotebookEdit"],
      "deny":  []
    },
    "trustedDirectories": ["/home/sandbox", "/workspace"],
    ...passthrough keys from agent.claude.permissions...
  }

  // pkg/compiler/agent_codex.go
  //
  // Builds a Codex config.toml string. Codex 0.133 has no native tool gating —
  // emits existing inert hook blocks + args echo + explicit asymmetry note.
  //
  // Returns string (toml) so it can be written via configFiles userdata path
  // without further marshalling.
  func synthesizeCodexConfig(agent *profile.AgentSpec) (string, error)

Expected output shape:

  # Generated by km (Phase 92 synthesizer)
  # NOTE: Codex 0.133 does not honor permission/tool config.toml entries.
  # The blocks below are forward-compat scaffolding. Use sandbox-level network
  # policy / km eBPF rules for actual gating today.

  [features]
  hooks = true  # currently inert in Codex 0.133 per docs/codex-parity.md

  [[hooks.PermissionRequest]]
  command = "/opt/km/bin/km-notify-hook"

  [[hooks.Stop]]
  command = "/opt/km/bin/km-notify-hook"

  # Codex CLI args (echoed for reference; passed via wrapper, not config.toml):
  # args: --approval-mode=on-request
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Implement pkg/compiler/agent_claude.go synthesizer + remove Wave 0 build tag + commit goldens</name>
  <files>
    pkg/compiler/agent_claude.go,
    pkg/compiler/agent_claude_golden_test.go,
    pkg/compiler/testdata/claude_settings_learn_v2.golden.json,
    pkg/compiler/testdata/claude_settings_dc34.golden.json,
    pkg/compiler/testdata/claude_settings_locked.golden.json,
    pkg/compiler/testdata/claude_settings_codex.golden.json
  </files>
  <behavior>
    - After this task: pkg/compiler/agent_claude.go exists with synthesizeClaudeSettings(agent *profile.AgentSpec) (map[string]any, error).
    - The synthesizer is nil-safe: nil agent or nil agent.Claude returns empty map.
    - Emits canonical Claude Code 2.1.132 shape: permissions.allow (NOT autoApprove), permissions.deny (NOT disallowedTools), top-level trustedDirectories.
    - agent.claude.permissions map merges into output. Top-level keys from passthrough may override synthesized keys IF operator deliberately sets them (e.g., permissions: {defaultMode: "default"}).
    - 4 golden test files committed under pkg/compiler/testdata/.
    - Wave 0's TestSynthesizeClaudeSettingsGolden is GREEN; build tag removed.
  </behavior>
  <action>
**Create pkg/compiler/agent_claude.go**:

  package compiler

  import (
      "github.com/klankermaker/km/pkg/profile"
  )

  // synthesizeClaudeSettings builds a Claude Code 2.1.132 settings.json map from
  // the typed AgentClaudeSpec.
  //
  // Output shape (per Wave 0 research, RESEARCH.md §1b):
  //   {
  //     "permissions": {
  //       "allow": [...],   // from agent.claude.tools.autoApprove
  //       "deny":  [...]    // from agent.claude.tools.deny
  //     },
  //     "trustedDirectories": [...],
  //     ...passthrough keys from agent.claude.permissions...
  //   }
  //
  // Why not legacy "autoApprove" top-level key?
  //   The canonical Claude Code current form is "permissions.allow". Legacy
  //   "autoApprove" still works but is deprecated. Phase 92 chooses the canonical
  //   form to avoid tech debt; golden tests verify equivalence to the inlined-JSON
  //   that fixtures previously used.
  //
  // Why not "disallowedTools"?
  //   "disallowedTools" is a Claude Code CLI flag (--disallowedTools), NOT a
  //   settings.json key. The settings.json deny location is "permissions.deny".
  func synthesizeClaudeSettings(agent *profile.AgentSpec) (map[string]any, error) {
      out := map[string]any{}
      if agent == nil || agent.Claude == nil {
          return out, nil
      }
      c := agent.Claude

      perms := map[string]any{}
      if len(c.Tools.AutoApprove) > 0 {
          perms["allow"] = c.Tools.AutoApprove
      }
      if len(c.Tools.Deny) > 0 {
          perms["deny"] = c.Tools.Deny
      }
      if p := c.Permissions; len(p) > 0 {
          if pp, ok := p["permissions"].(map[string]any); ok {
              for k, v := range pp {
                  if _, exists := perms[k]; !exists {
                      perms[k] = v
                  }
              }
          }
      }
      if len(perms) > 0 {
          out["permissions"] = perms
      }

      if len(c.TrustedDirectories) > 0 {
          out["trustedDirectories"] = c.TrustedDirectories
      }

      for k, v := range c.Permissions {
          if k == "permissions" { continue }
          if _, exists := out[k]; exists      { continue }
          out[k] = v
      }

      return out, nil
  }

**Remove build tag from pkg/compiler/agent_claude_golden_test.go**:
- Delete the //go:build phase92_wave5 + // +build phase92_wave5 lines.

**Capture goldens**:

For each of the 4 fixtures (learn.v2, dc34, locked, codex), the golden file claude_settings_FIXTURENAME.golden.json should be the SYNTHESIZED output of running the synthesizer on the Wave 4 (or post-Wave-3) state of the fixture's agent.claude.* block. Since Task 3 (this plan) rewrites the fixtures to populate agent.claude.*, this is a chicken-and-egg situation — resolve by:

- Step 1: Hand-write the expected golden JSON files based on the EFFECTIVE semantics of each fixture's pre-Phase-92 configFiles[".claude/settings.json"] content (e.g., learn.v2 had `{"autoApprove": ["Bash","Read","Write","Edit","Glob","Grep","WebFetch","WebSearch","NotebookEdit"], "trustedDirectories": ["/home/sandbox","/workspace"]}` — its golden becomes `{"permissions": {"allow": ["Bash",...]}, "trustedDirectories": ["/home/sandbox","/workspace"]}`).
- Step 2: After Task 3 rewrites the fixtures to encode the same semantics under agent.claude.tools.autoApprove, the synthesizer should produce byte-identical JSON to the golden.
- Step 3: If golden differs (e.g., JSON key ordering, indentation), use Go's json.MarshalIndent with a deterministic key order — sort keys, 2-space indent. Update the test to do the same when comparing.

For each fixture, write the golden file. Examples:

testdata/claude_settings_learn_v2.golden.json:

  {
    "permissions": {
      "allow": [
        "Bash",
        "Read",
        "Write",
        "Edit",
        "Glob",
        "Grep",
        "WebFetch",
        "WebSearch",
        "NotebookEdit"
      ]
    },
    "trustedDirectories": [
      "/home/sandbox",
      "/workspace"
    ]
  }

testdata/claude_settings_codex.golden.json:

  {}

(codex.yaml profile has agent.default: codex — Claude not configured — synthesizer returns empty map.)

testdata/claude_settings_dc34.golden.json + testdata/claude_settings_locked.golden.json: derive from each fixture's current inlined JSON content. Inspect the file, transcribe the semantics, encode in canonical form.

Commit message: `feat(92-05): add agent_claude.go synthesizer (permissions.allow / permissions.deny canonical) + 4 goldens (VC-5)`.
  </action>
  <verify>
    <automated>go test ./pkg/compiler/ -run TestSynthesizeClaudeSettingsGolden -count=1 -v</automated>
    Expected: 4 sub-tests GREEN (one per fixture). VC-5.
  </verify>
  <done>
    Synthesizer in place; 4 goldens committed; Wave 0 RED stub GREEN; build tag removed.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Implement pkg/compiler/agent_codex.go synthesizer + golden</name>
  <files>
    pkg/compiler/agent_codex.go,
    pkg/compiler/agent_codex_golden_test.go,
    pkg/compiler/testdata/codex_config_codex.golden.toml
  </files>
  <behavior>
    - After this task: pkg/compiler/agent_codex.go exists with synthesizeCodexConfig(agent *profile.AgentSpec) (string, error).
    - Nil-safe: nil agent or nil agent.Codex returns an empty-config-with-asymmetry-note string.
    - Emits inert hook blocks (forward-compat) + args echo + asymmetry note when tools are populated.
    - 1 golden test file committed.
    - Wave 0's TestSynthesizeCodexConfigGolden is GREEN; build tag removed.
  </behavior>
  <action>
**Create pkg/compiler/agent_codex.go**:

  package compiler

  import (
      "fmt"
      "strings"

      "github.com/klankermaker/km/pkg/profile"
  )

  // synthesizeCodexConfig builds a Codex 0.133 config.toml from the typed
  // AgentCodexSpec.
  //
  // HONEST SCOPE (per RESEARCH.md §1a + .planning/research/codex-config-toml.md):
  //   Codex 0.133 has NO native tool allow/deny in config.toml. The `[features]
  //   hooks = true` flag is honored at the schema level but hooks DO NOT FIRE
  //   from Codex 0.133 (Phase 70 spike confirmed at docs/codex-parity.md:82-89).
  //
  // This synthesizer therefore:
  //   1. Emits existing inert hook blocks (so a future Codex release that activates
  //      them will pick them up without a Phase 93+ migration).
  //   2. Echoes agent.codex.args via comments — operator passes them through km
  //      wrapper, not via config.toml.
  //   3. Emits a `# NOTE:` block when agent.codex.tools.{autoApprove,deny} are
  //      non-empty, documenting the asymmetry vs Claude Code.
  func synthesizeCodexConfig(agent *profile.AgentSpec) (string, error) {
      var b strings.Builder

      b.WriteString("# Generated by km (Phase 92 synthesizer).\n")
      b.WriteString("# NOTE: Codex 0.133 does not honor permission/tool config.toml entries\n")
      b.WriteString("# beyond filesystem/network sandbox scoping. Tool gating from\n")
      b.WriteString("# agent.codex.tools.{autoApprove,deny} is captured below as forward-compat\n")
      b.WriteString("# documentation; today, use sandbox-level network policy or km eBPF rules.\n")
      b.WriteString("# See docs/agent-tool-gating.md and docs/codex-parity.md.\n\n")

      b.WriteString("[features]\n")
      b.WriteString("hooks = true  # inert in Codex 0.133, forward-compat for future release\n\n")

      b.WriteString("[[hooks.PermissionRequest]]\n")
      b.WriteString("command = \"/opt/km/bin/km-notify-hook\"\n\n")

      b.WriteString("[[hooks.Stop]]\n")
      b.WriteString("command = \"/opt/km/bin/km-notify-hook\"\n")

      if agent == nil || agent.Codex == nil {
          return b.String(), nil
      }
      c := agent.Codex

      if len(c.Args) > 0 {
          b.WriteString("\n# agent.codex.args (echoed for reference; passed via km wrapper):\n")
          for _, a := range c.Args {
              b.WriteString(fmt.Sprintf("# args: %s\n", a))
          }
      }

      if len(c.Tools.AutoApprove) > 0 || len(c.Tools.Deny) > 0 {
          b.WriteString("\n# agent.codex.tools.{autoApprove,deny} declared but NOT enforced:\n")
          b.WriteString("# Codex 0.133 lacks a settings.json/config.toml tool-gating schema.\n")
          if len(c.Tools.AutoApprove) > 0 {
              b.WriteString(fmt.Sprintf("# autoApprove: %s\n", strings.Join(c.Tools.AutoApprove, ", ")))
          }
          if len(c.Tools.Deny) > 0 {
              b.WriteString(fmt.Sprintf("# deny:        %s\n", strings.Join(c.Tools.Deny, ", ")))
          }
      }

      return b.String(), nil
  }

**Golden file testdata/codex_config_codex.golden.toml**:
Capture the exact output of running synthesizeCodexConfig(p.Spec.Agent) against profiles/codex.yaml AFTER Task 3 rewrites it (which will set agent.default: codex, agent.codex.args: [...], optionally agent.codex.tools.*). For now, hand-construct the expected output following the synthesizer logic and the post-Task-3 fixture state. Iterate if needed: run the synthesizer, copy output, paste as golden, verify byte-identity.

**Remove build tag from pkg/compiler/agent_codex_golden_test.go**:
- Delete the //go:build phase92_wave5 + // +build phase92_wave5 lines.

Commit message: `feat(92-05): add agent_codex.go synthesizer (inert hooks + args echo + asymmetry note) + golden (VC-5)`.
  </action>
  <verify>
    <automated>go test ./pkg/compiler/ -run TestSynthesizeCodexConfigGolden -count=1 -v</automated>
    Expected: GREEN. VC-5.
  </verify>
  <done>
    Synthesizer in place; 1 golden committed; Wave 0 RED stub GREEN; build tag removed; the asymmetry is documented in the toml output itself, not just docs.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: Wire synthesizers into userdata.go pipeline + rewrite all 20 profile YAMLs</name>
  <files>
    pkg/compiler/userdata.go,
    profiles/ao.yaml,
    profiles/codex.yaml,
    profiles/dc34.yaml,
    profiles/dc34.ami.yaml,
    profiles/example-additional-snapshots.yaml,
    profiles/goose.yaml,
    profiles/learn.v2.yaml,
    profiles/learn.v2.chatty.yaml,
    profiles/learn.v2.codex.yaml,
    profiles/learn.v2.polite.yaml,
    profiles/locked.yaml,
    profiles/locked.ami.yaml,
    pkg/profile/builtins/ao.yaml,
    pkg/profile/builtins/codex.yaml,
    pkg/profile/builtins/goose.yaml,
    pkg/profile/builtins/hardened.yaml,
    pkg/profile/builtins/learn.yaml,
    pkg/profile/builtins/open-dev.yaml,
    pkg/profile/builtins/restricted-dev.yaml,
    pkg/profile/builtins/sealed.yaml
  </files>
  <behavior>
    - After this task: pkg/compiler/userdata.go calls synthesizeClaudeSettings then mergeNotifyHookIntoSettings then writes to /home/sandbox/.claude/settings.json via the configFiles userdata path.
    - Codex path parallel: synthesizeCodexConfig then write to /home/sandbox/.codex/config.toml (notify hook entries already embedded in synthesizer output; no separate merge step).
    - All 20 profile YAMLs:
      - REMOVE spec.execution.configFiles["/home/sandbox/.claude/settings.json"] inline JSON string.
      - POPULATE spec.agent.claude.tools.autoApprove / spec.agent.claude.trustedDirectories with the SAME effective semantics (transcribe from the inlined JSON to typed fields).
      - MOVE spec.cli.agent to spec.agent.default.
      - MOVE spec.cli.claudeArgs to spec.agent.claude.args.
      - MOVE spec.cli.codexArgs to spec.agent.codex.args.
      - Leave any other spec.execution.configFiles[...] entries alone (only the Claude settings.json key is removed).
    - After this task: cli: block in every profile contains AT MOST noBedrock (often just cli: noBedrock: true or absent).
    - scripts/validate-all-profiles.sh exits 0 against all 20 profiles.
    - Wave 0's TestUserdataLearnV2Phase92ByteIdentity (VC-3) is GREEN — the pipeline emits byte-identical userdata even though the Claude settings.json content is now synthesized rather than inlined. THE WHOLE PHASE'S BYTE-IDENTITY CONTRACT HINGES ON THIS.
  </behavior>
  <action>
**Modify pkg/compiler/userdata.go** — the existing Claude settings.json write path (look for mergeNotifyHookIntoSettings calls and the configFiles emission):

Pipeline rewrite:
  // OLD (pre-Phase-92): operator's inlined configFiles JSON was the source; mergeNotifyHookIntoSettings parsed it and added hooks.
  // NEW (Wave 5 onward):
  // 1. settings, err := synthesizeClaudeSettings(p.Spec.Agent)
  //    (Replaces the inline JSON read entirely.)
  // 2. settings, err = mergeNotifyHookIntoSettings(settings, p)
  // 3. encode settings to JSON; emit as a configFiles entry for the userdata path
  //    at "/home/sandbox/.claude/settings.json".

Implementation:

  settings, err := synthesizeClaudeSettings(p.Spec.Agent)
  if err != nil { return nil, fmt.Errorf("synthesizeClaudeSettings: %w", err) }
  settings, err = mergeNotifyHookIntoSettings(settings, p)
  if err != nil { return nil, fmt.Errorf("mergeNotifyHookIntoSettings: %w", err) }
  buf, err := json.MarshalIndent(settings, "", "  ")
  if err != nil { return nil, fmt.Errorf("marshal settings.json: %w", err) }
  params.ConfigFiles["/home/sandbox/.claude/settings.json"] = string(buf)

Codex parallel (replace the existing hardcoded ~/.codex/config.toml heredoc):

  codexTOML, err := synthesizeCodexConfig(p.Spec.Agent)
  if err != nil { return nil, fmt.Errorf("synthesizeCodexConfig: %w", err) }
  params.ConfigFiles["/home/sandbox/.codex/config.toml"] = codexTOML

The mergeNotifyHookIntoSettings helper itself stays mostly unchanged — it accepts a map[string]any (from the synthesizer) instead of parsing inline JSON. If the existing signature was mergeNotifyHookIntoSettings(jsonStr string, p *profile.Profile) (string, error), refactor to:

  func mergeNotifyHookIntoSettings(settings map[string]any, p *profile.Profile) (map[string]any, error)

And update its call sites.

**Profile YAML rewrites (per fixture)**:

For each profile, the transformation is:

1. Read the current spec.execution.configFiles["/home/sandbox/.claude/settings.json"] JSON string.
2. Parse it: it contains some subset of `{"autoApprove": [...], "trustedDirectories": [...], ...}`.
3. Transcribe to typed YAML:

       spec:
         agent:
           default: claude  # or codex, from old cli.agent
           claude:
             trustedDirectories: [/home/sandbox, /workspace]
             tools:
               autoApprove: [Bash, Read, Write, Edit, Glob, Grep, WebFetch, WebSearch, NotebookEdit]
               deny: []
             args: ["--dangerously-skip-permissions"]   # from old cli.claudeArgs
           codex:
             args: []                                   # from old cli.codexArgs (only if non-empty)

4. DELETE the configFiles["/home/sandbox/.claude/settings.json"] entry. Leave OTHER configFiles entries alone.
5. DELETE spec.cli.agent, spec.cli.claudeArgs, spec.cli.codexArgs. Spec.cli should now have at most noBedrock.

Special-case YAMLs:
- profiles/codex.yaml and profiles/learn.v2.codex.yaml: set agent.default: codex. May still have Claude args (operator-side default for --claude switch).
- profiles/learn.v2.yaml: byte-identity baseline. After rewrite + synthesizer wiring, TestUserdataLearnV2Phase92ByteIdentity (VC-3) MUST be GREEN. This is the whole-phase contract. If golden differs, diff the synthesized vs inlined-pre-92 settings.json content — likely a JSON key order or indent difference. Fix synthesizer to match (or update test to compare JSON semantically). Per Wave 0 research recommendation (Option B): emit canonical permissions.allow even though pre-Phase-92 inlined the legacy autoApprove.

  RECONCILIATION: Pre-Phase-92 the userdata embedded `{"autoApprove": [...], "trustedDirectories": [...]}` literally. Post-Wave-5 the userdata embeds `{"permissions": {"allow": [...]}, "trustedDirectories": [...]}` (canonical form). These are SEMANTICALLY equivalent but BYTE-DIFFERENT. The byte-identity test will FAIL on the inlined-Claude-settings portion of the userdata.

  **Decision (Wave 5 must lock this)**: The Wave 0 VC-3 byte-identity test scope must be REDEFINED to exclude the Claude settings.json content blob (since synthesizer canonicalizes). Update the test to:
  - Strip the line(s) containing the inlined Claude settings.json content before comparing.
  - Or split the test into two: (a) byte-identity of NON-Claude-settings portions; (b) semantic equivalence of the Claude settings.json portion (parse old + new JSON, assert effective permissions are equivalent).

  Document this in the SUMMARY clearly. The "byte-identity" contract was always meant to prove "pipeline is semantically transparent" — canonical → canonical migration is BETTER than literal byte-identity for the settings.json portion. The IAM byte-identity (VC-4) and the NON-settings.json portion of userdata remain strict byte-identity.

**Verification step within this task**:
- make build
- bash scripts/validate-all-profiles.sh — all 20 must pass.
- go test ./pkg/compiler/... — VC-3 (or its split successor), VC-5 all GREEN.

Commit message: `feat(92-05): wire synthesizer into userdata pipeline; rewrite 20 profiles to use agent.claude.tools.* + agent.default; remove inlined Claude settings.json`.
  </action>
  <verify>
    <automated>make build &amp;&amp; bash scripts/validate-all-profiles.sh &amp;&amp; go test ./pkg/compiler/... -count=1</automated>
    Expected: km builds; all 20 profiles validate; synthesizer goldens GREEN; userdata semantic-equivalent or byte-identical depending on test split. VC-1, VC-3, VC-5, VC-11.
  </verify>
  <done>
    Synthesizer wired in 2 sites (Claude + Codex); all 20 fixtures rewritten; inlined Claude settings.json strings GONE everywhere; CLISpec usage in fixtures reduced to cli.noBedrock only.
  </done>
</task>

<task type="auto">
  <name>Task 4: Docs — agent-tool-gating.md (new) + codex-parity.md + CLAUDE.md</name>
  <files>
    docs/agent-tool-gating.md,
    docs/codex-parity.md,
    CLAUDE.md
  </files>
  <action>
**Create docs/agent-tool-gating.md** — operator-facing guide for the new agent block.

Required sections:

1. **What this is** — single-page overview of spec.agent: block.

2. **Quick example**:

       spec:
         agent:
           default: claude
           claude:
             trustedDirectories: [/home/sandbox, /workspace]
             tools:
               autoApprove: [Bash, Read, Write, Edit, Glob, Grep, WebFetch, WebSearch, NotebookEdit]
               deny: [WebFetch]   # deny wins over allow per Claude Code 2.1.132
             args: ["--dangerously-skip-permissions"]
           codex:
             args: []

3. **Synthesis pipeline**:
   - synthesizeClaudeSettings(spec.agent.claude) → settings.json.
   - mergeNotifyHookIntoSettings(settings, profile) → adds km-notify hooks.
   - Write to /home/sandbox/.claude/settings.json via userdata configFiles path.
   - Mirror pipeline for synthesizeCodexConfig(spec.agent.codex) → ~/.codex/config.toml.

4. **Claude Code asymmetry vs Codex**:
   - Claude Code 2.1.132 honors permissions.allow / permissions.deny in settings.json. Per-tool gating is REAL.
   - Codex 0.133 has NO native per-tool allow/deny in config.toml (per Wave 0 research, RESEARCH.md §1a). Synthesizer emits inert hook blocks + args echo + a comment in the generated toml documenting the asymmetry. For actual Codex tool gating today, use sandbox-level network policy (eBPF allowlist) and sandbox_mode filesystem scoping.
   - When agent.codex.tools.{autoApprove,deny} are populated, fields are PRESERVED in the YAML for forward-compat (future Codex release may honor them).

5. **No-merge-with-configFiles rule** (locked decision):
   - Profiles that populate BOTH agent.claude.tools.autoApprove AND inline execution.configFiles["/home/sandbox/.claude/settings.json"] are HARD validation errors.
   - No merge fallback. Pick one mode.
   - Rationale: synthesizer can't tell whether to trust the inline JSON or override with typed; either silently confusing or breaks the operator's mental model.

6. **Migrating from pre-Phase-92 inlined JSON**:
   - Pre-Phase-92 example:

         spec:
           execution:
             configFiles:
               "/home/sandbox/.claude/settings.json": |
                 {"autoApprove":["Bash","Read"], "trustedDirectories":["/home/sandbox"]}

   - Post-Phase-92 equivalent:

         spec:
           agent:
             claude:
               trustedDirectories: [/home/sandbox]
               tools:
                 autoApprove: [Bash, Read]

   - Note: synthesizer emits permissions.allow (canonical), not legacy autoApprove. Both are honored by Claude Code 2.1.132; canonical form is preferred going forward.

7. **Permissions passthrough** (agent.claude.permissions: map[string]any):
   - For Claude Code settings.json keys not worth typing (per-release additions). Example: defaultMode, ask, additionalDirectories.
   - Document well-known keys: defaultMode, ask, additionalDirectories. Anything else flows through and Claude Code either honors or ignores.
   - Schema is additionalProperties: true for this map (the ONE schema exception).

8. **Future work**:
   - Codex tool gating once OpenAI ships native support.
   - Per-tool quota / rate limiting (out of scope Phase 92).

**Update docs/codex-parity.md**:
- Remove "inert ~/.codex/config.toml" note from Phase 70 era.
- Add Phase 92 section: synthesizer now generates the config.toml; per-fixture content visible via `cat /home/sandbox/.codex/config.toml` post-create.
- Keep the Phase 70 spike documentation (RESEARCH.md §1a referenced it as authoritative).
- Point to docs/agent-tool-gating.md for the asymmetry write-up.

**Update CLAUDE.md**:
- Add row to "Where to look":

      | Structured Claude/Codex tool gating via `spec.agent:`, synthesizers, asymmetry note | `docs/agent-tool-gating.md` (Phase 92) |

- Add Phase 92 callout near the top (consolidates Wave 1+3+5 changes):
  > Phase 92 (2026-05-31): SandboxProfile spec restructure complete.
  > - `spec.identity:` → `spec.iam:` (with `allowedSecretPaths` declared)
  > - `spec.cli.notify*` → `spec.notification:` (sub-blocks)
  > - `spec.cli.vscodeEnabled` → `spec.runtime.vscode.enabled`
  > - `spec.cli.{agent,claudeArgs,codexArgs}` → `spec.agent:` block
  > - Inlined `configFiles["/home/sandbox/.claude/settings.json"]` REMOVED; synthesized from `spec.agent.claude.tools.*`
  > - Sandbox-side env var names (KM_NOTIFY_*, KM_SLACK_*, KM_AGENT) UNCHANGED.
  > - Post-merge: `make build && km init --sidecars` to refresh management Lambdas.
- Audit other CLAUDE.md sections for references to inlined Claude settings.json examples; replace each with the typed agent.claude.* form.

Commit message: `docs(92-05): new docs/agent-tool-gating.md; update codex-parity.md + CLAUDE.md (Phase 92 consolidated callout)`.
  </action>
  <verify>
    <automated>test -f docs/agent-tool-gating.md &amp;&amp; grep -q 'agent.claude.tools' docs/agent-tool-gating.md &amp;&amp; grep -q 'Phase 92' CLAUDE.md</automated>
    Expected: file exists with the right content + CLAUDE.md callout present. VC-1.
  </verify>
  <done>
    Docs in place; agent-tool-gating.md authoritative; CLAUDE.md has consolidated Phase 92 callout.
  </done>
</task>

</tasks>

<verification>
- `go build ./...` GREEN.
- `go test ./...` GREEN.
- All 4 synthesizer goldens GREEN (`TestSynthesizeClaudeSettingsGolden` 4 sub-tests + `TestSynthesizeCodexConfigGolden`) — VC-5.
- VC-3 userdata byte-identity (or semantic-equivalent per Wave 5 reconciliation) GREEN.
- VC-11 `scripts/validate-all-profiles.sh` exits 0.
- `docs/agent-tool-gating.md` exists and explains the asymmetry.
</verification>

<success_criteria>
- 2 new synthesizer files, ~150 lines of new compiler code (well within CONTEXT.md sizing).
- 5 golden files committed.
- 20 profile YAMLs migrated to typed agent block; inlined Claude settings.json removed everywhere.
- `cli:` block in every fixture reduced to optional `noBedrock` only.
- userdata pipeline rewired: synthesize → merge hooks → write.
- 3 docs updated; new doc `agent-tool-gating.md` is the authoritative source.
- Per RESEARCH.md §2f, `pkg/profile/configfiles/` is NOT touched (directory doesn't exist).
- Codex synthesizer is HONEST about lack of tool gating per Wave 0 research; the asymmetry is documented in the toml output, in `agent-tool-gating.md`, and in `codex-parity.md`.
</success_criteria>

<output>
After completion, create `.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-05-SUMMARY.md` capturing:
- The synthesizer file paths + line counts.
- Per-fixture before/after of the `agent:` block (1 representative example).
- Confirmation that the byte-identity VC-3 strategy was reconciled (canonical form vs literal byte-identity).
- All 5 golden file paths.
- Confirmation that `pkg/profile/configfiles/` was NOT touched (per RESEARCH.md §2f — doesn't exist).
- Wave 6 UAT handoff: all automated VCs except VC-8/VC-9/VC-10 are GREEN.
</output>
