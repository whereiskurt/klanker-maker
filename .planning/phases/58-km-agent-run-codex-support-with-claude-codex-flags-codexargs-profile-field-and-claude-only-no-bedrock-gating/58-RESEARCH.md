# Phase 58: km agent run — Codex Support - Research

**Researched:** 2026-04-19
**Domain:** Go CLI refactor, bash script generation, profile schema extension
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **Flag design:** Mirror the interactive path — `--claude` (default) and `--codex` as mutually exclusive bools on `km agent run`. No `--agent <name>` enum. Default is `--claude` for backward compatibility so no existing invocation breaks.
- **Mutual exclusion enforcement:** `--claude` and `--codex` enforced in `RunE` with an explicit error (pattern: existing `--interactive + --wait` check at agent.go:178).
- **Codex invocation:** `codex exec --json --dangerously-bypass-approvals-and-sandbox "$PROMPT"` — the codex equivalent of claude's `--dangerously-skip-permissions`. JSON output flag is `--json`. Prompt is base64-encoded and decoded inside the shell script (preserves existing escaping safety from phase 50). Only the binary + flags line changes.
- **`--no-bedrock` scoping:** Claude-only. `km agent run --codex --no-bedrock` is a hard error returned from `RunE` before any SSM interaction. Error text: `"--no-bedrock is only valid with --claude"`. Neither the env-unset block nor the OAuth-extraction block is ever injected into a codex-run script — they are anchored inside the claude branch of the refactored `BuildAgentShellCommands`.
- **Output contract:** Pass-through. Whatever `codex exec --json` writes to stdout lands in `output.json` unchanged. No normalization, no envelope wrapping.
- **Profile schema:** Add `spec.cli.codexArgs []string`, parallel to existing `spec.cli.claudeArgs` (added in commit 4dbbe63). Not a unified `agentArgs` map.
- **Testing:** TDD per project convention. Must extend `TestAgentNonInteractive_CommandConstruction` with a codex variant. Must add `--codex --no-bedrock` error test. Must add `TestCLISpec_CodexArgsParsesFromYAML`.

### Claude's Discretion

- **Signature of `BuildAgentShellCommands`:** Current variadic `noBedrock ...bool` is acknowledged as ugly. Planner can clean this up to a struct-arg or positional required args as part of the refactor.
- **Whether to expose a new helper `BuildAgentCommandLine(agent, args)`** vs inlining the agent-switch into `BuildAgentShellCommands` — planner's call based on testability.
- **Loader consolidation:** `loadProfileCLINoBedrock` and `loadProfileCLIClaudeArgs` currently both call `loadProfileCLI` — adding `loadProfileCLICodexArgs` is trivial; planner decides whether to further consolidate into a single helper returning the whole `CLISpec`.

### Deferred Ideas (OUT OF SCOPE)

- `codexArgs` entry in `learn.yaml` — easy follow-up once this lands.
- Output normalization envelope — explicitly rejected for this phase.
- Goose / other agent support — no demand signal yet.
- `km agent run --no-interactive-login`-style auth flags for codex.
- Unified `--agent <name>` enum flag — considered and rejected.
</user_constraints>

---

## Summary

Phase 58 is a targeted Go refactor with three parallel workstreams: (1) add `--claude`/`--codex` flag pair to `newAgentRunCmd`, (2) refactor `BuildAgentShellCommands` to branch on agent type instead of hardcoding claude, and (3) extend the profile schema with `spec.cli.codexArgs`. All design decisions are fully locked. No new infrastructure, no new AWS calls, no new dependencies.

The existing `BuildAgentShellCommands` at agent.go:1020 is the single function that generates the entire bash script. The refactor localizes agent-specific lines (the binary + flags line, and the `--no-bedrock` unset stanza) inside an agent-type branch, while leaving the surrounding scaffold (run directory creation, base64 decoding, tmux session, status file, S3 upload, `tmux wait-for`) completely unchanged.

The codex invocation is confirmed: `codex exec --json --dangerously-bypass-approvals-and-sandbox "$PROMPT"`. The `--json` flag produces JSONL events, `--dangerously-bypass-approvals-and-sandbox` (alias `--yolo`) bypasses all approval prompts. Codex reads the prompt as a positional argument to `exec`. Credential storage is via `~/.codex/auth.json`; the interactive `--codex` path already handles auth setup, so this phase inherits that without additional work.

**Primary recommendation:** Refactor `BuildAgentShellCommands` to accept an explicit `AgentRunOptions` struct (replaces the ugly variadic bool); branch inside the function for claude vs codex; the surrounding tmux scaffold is unchanged.

---

## Standard Stack

### Core (unchanged — all already in repo)

| Component | Current State | Phase 58 Touch |
|-----------|--------------|----------------|
| `internal/app/cmd/agent.go` | 1144 lines, `BuildAgentShellCommands` at L1020 | Refactor L1020-1072, extend `newAgentRunCmd` L152-216 |
| `pkg/profile/types.go` | `CLISpec` at L354-367, has `ClaudeArgs []string` | Add `CodexArgs []string` |
| `pkg/profile/schemas/sandbox_profile.schema.json` | `cli` section at L474-488 | Add `codexArgs` array entry |
| `internal/app/cmd/agent_test.go` | Tests at L137-193 and L820-879 | Extend with codex variants |
| `pkg/profile/types_test.go` | `TestCLISpec_ClaudeArgsParsesFromYAML` at L818 | Add `TestCLISpec_CodexArgsParsesFromYAML` |

### No New Dependencies

This phase introduces zero new Go imports. The `base64`, `fmt`, `strings`, `time` packages used by `BuildAgentShellCommands` are already in the import block.

---

## Architecture Patterns

### Pattern 1: BuildAgentShellCommands Refactor

**What:** Replace the variadic `noBedrock ...bool` signature with a struct, branch inside on `AgentType`.

**Current signature:**
```go
func BuildAgentShellCommands(prompt string, artifactsBucket string, noBedrock ...bool) ([]string, string)
```

**Recommended new signature:**
```go
type AgentRunOptions struct {
    AgentType       string   // "claude" or "codex"
    NoBedrock       bool     // claude-only; ignored (safe) for codex
    ClaudeArgs      []string // appended to claude invocation
    CodexArgs       []string // appended to codex invocation
    ArtifactsBucket string
}

func BuildAgentShellCommands(prompt string, opts AgentRunOptions) ([]string, string)
```

**Agent-specific section inside the script template:**
```go
var agentLine string
var noBedrockLines string

switch opts.AgentType {
case "codex":
    args := []string{"codex", "exec", "--json", "--dangerously-bypass-approvals-and-sandbox"}
    args = append(args, opts.CodexArgs...)
    args = append(args, `"$PROMPT"`)
    agentLine = strings.Join(args, " ") + ` \
  > "$RUN_DIR/output.json" 2>"$RUN_DIR/stderr.log"`
default: // "claude"
    args := []string{`claude -p "$PROMPT"`, "--output-format json", "--dangerously-skip-permissions", "--bare"}
    args = append(args, opts.ClaudeArgs...)
    agentLine = strings.Join(args, " ") + ` \
  > "$RUN_DIR/output.json" 2>"$RUN_DIR/stderr.log"`
    if opts.NoBedrock {
        noBedrockLines = `unset CLAUDE_CODE_USE_BEDROCK
unset ANTHROPIC_BASE_URL
...
`
    }
}
```

**The tmux scaffold, run directory, status file, S3 upload, and `tmux wait-for` lines are identical for both agents — only `agentLine` and `noBedrockLines` differ.**

### Pattern 2: Flag Wiring in newAgentRunCmd

Mirror exactly what `newAgentRunCmd` does for `--interactive`/`--wait` mutual exclusion. The check belongs at the top of `RunE`, before any `ResolveSandboxID` call:

```go
// In RunE:
var useClaude bool
var useCodex bool

// Mutual exclusion: --claude and --codex
if useClaude && useCodex {
    return fmt.Errorf("--claude and --codex are mutually exclusive")
}

// Default to claude
agentType := "claude"
if useCodex {
    agentType = "codex"
}

// Gate --no-bedrock to claude-only (BEFORE any SSM call)
if useCodex && noBedrock {
    return fmt.Errorf("--no-bedrock is only valid with --claude")
}
```

The `--no-bedrock` check must happen **before** `ResolveSandboxID` — the context says "before any SSM call". `ResolveSandboxID` does a DynamoDB/S3 lookup, which counts. Place both mutual-exclusion checks at the very top of `RunE`.

### Pattern 3: Profile Schema Extension

The `codexArgs` entry in `sandbox_profile.schema.json` mirrors `claudeArgs` exactly:

```json
"codexArgs": {
  "type": "array",
  "items": { "type": "string" },
  "description": "Extra args appended to the `codex exec` command when running via km agent run --codex. User-supplied args via --codex-args still take precedence."
}
```

And in `pkg/profile/types.go`:
```go
type CLISpec struct {
    NoBedrock  bool     `yaml:"noBedrock,omitempty"`
    ClaudeArgs []string `yaml:"claudeArgs,omitempty"`
    CodexArgs  []string `yaml:"codexArgs,omitempty"`   // NEW
}
```

### Pattern 4: loadProfileCLI Helper Extension

The simplest approach: add a twin `loadProfileCLICodexArgs` mirroring `loadProfileCLIClaudeArgs`. This keeps the pattern recognizable and avoids breaking the existing call sites:

```go
func loadProfileCLICodexArgs(ctx context.Context, cfg *config.Config, sandboxID string) []string {
    p := loadProfileCLI(ctx, cfg, sandboxID)
    if p == nil {
        return nil
    }
    return p.CodexArgs
}
```

**Alternative (Claude's Discretion):** A generalized helper `loadProfileCLIAgentArgs(ctx, cfg, sandboxID, agentType string) []string` is cleaner if the planner expects more agent types soon, but given the explicit deferral of goose/other agents, the twin pattern is fine.

### Pattern 5: runAgentNonInteractive Extension

The `runAgentNonInteractive` function signature gains `agentType string`:

```go
func runAgentNonInteractive(ctx context.Context, cfg *config.Config, ..., agentType string, ...) error
```

The `BuildAgentShellCommands` call at agent.go:459 becomes:

```go
cmds, runID := BuildAgentShellCommands(prompt, AgentRunOptions{
    AgentType:       agentType,
    NoBedrock:       noBedrock,
    ClaudeArgs:      claudeArgs,  // from loadProfileCLIClaudeArgs (if agentType=="claude")
    CodexArgs:       codexArgs,   // from loadProfileCLICodexArgs (if agentType=="codex")
    ArtifactsBucket: cfg.ArtifactsBucket,
})
```

### Anti-Patterns to Avoid

- **Don't check `--no-bedrock` after `ResolveSandboxID`:** The mutual exclusion error must fire before any AWS calls. The existing `--interactive + --wait` check at agent.go:184 is the correct model — it's the first thing in `RunE`.
- **Don't inject `noBedrockLines` outside the claude branch:** The env-unset and OAuth blocks are Bedrock/Anthropic-specific. Codex uses `~/.codex/auth.json` and `OPENAI_API_KEY` — none of those env vars should be unset.
- **Don't normalize codex output:** The pass-through contract is locked. `km agent results` streams raw `output.json` regardless. Adding any JSON envelope would break the locked decision.
- **Don't use variadic bool in the refactored signature:** The current `noBedrock ...bool` is the ugliness being fixed. Use the struct.

---

## Codex CLI Verification (v0.121.0)

Verified against official OpenAI Codex documentation (HIGH confidence):

| Question | Answer | Confidence |
|----------|--------|------------|
| Non-interactive subcommand | `codex exec "<prompt>"` — prompt is a positional arg | HIGH |
| JSON output flag | `--json` — produces JSONL event stream to stdout | HIGH |
| Approval bypass flag | `--dangerously-bypass-approvals-and-sandbox` (alias: `--yolo`) | HIGH |
| Prompt passing | Positional arg after `exec`, or stdin with `-` | HIGH |
| Auth credential file | `~/.codex/auth.json` (or OS credential store) | HIGH |
| Auth env var | `OPENAI_API_KEY` is read but `CODEX_API_KEY` is the CI-recommended var | MEDIUM |
| `--bare` equivalent needed? | No — codex has no Bedrock dual-mode; auth is simpler | HIGH |

**Key finding:** The `--dangerously-bypass-approvals-and-sandbox` flag is confirmed in official docs. The `--json` flag produces JSONL (newline-delimited JSON events), not a single JSON object — this is the pass-through contract. The output in `output.json` will be JSONL format.

**Auth context:** The learn.yaml `initCommands` creates `/home/sandbox/.codex/` directory (line 66: `mkdir -p /home/sandbox/.codex`). The interactive `km agent <sb> --codex` path already handles auth setup for the sandbox user. This phase inherits that credential path without additional work.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Base64 prompt encoding | Custom escaping | Existing `base64.StdEncoding.EncodeToString` + `base64 -d` in bash | Already battle-tested against injection in TestAgentNonInteractive_PromptEscaping |
| Tmux session management | New session naming | Existing `km-agent-<runID>` pattern | Unchanged for codex; `km agent attach` and `km agent list` discover sessions by this name |
| S3 artifact upload | New S3 client | Existing `aws s3 cp` in the bash script | No Go-side changes needed; the script's upload block is agent-agnostic |
| Profile loading | New profile fetch | Existing `loadProfileCLI` + S3 fetch | Already handles errors gracefully (fail-open on any error) |

---

## Common Pitfalls

### Pitfall 1: --no-bedrock Check After AWS Calls
**What goes wrong:** If `--codex --no-bedrock` validation happens after `ResolveSandboxID`, the user sees an error but an AWS DynamoDB lookup already occurred (slow, and misleading in offline tests).
**Why it happens:** It's tempting to put the check near the `BuildAgentShellCommands` call.
**How to avoid:** Follow the `--interactive + --wait` model at agent.go:184 — first two lines of `RunE`, before `ctx` setup.

### Pitfall 2: Codex Args Position in Invocation
**What goes wrong:** `CodexArgs` appended before `--dangerously-bypass-approvals-and-sandbox` may interact with positional arg parsing.
**How to avoid:** Order: `codex exec --json --dangerously-bypass-approvals-and-sandbox [codexArgs...] "$PROMPT"`. The prompt must be the last positional arg.

### Pitfall 3: TestAgentNonInteractive_NoBedrock Breaks After Signature Refactor
**What goes wrong:** Existing test calls `BuildAgentShellCommands("test prompt", "", true)` (variadic). If the signature changes to a struct, this test breaks.
**How to avoid:** Update both the old tests AND add new ones in the same commit. Don't leave the old variadic signature as a shim — the CONTEXT explicitly enables cleaning it up.

### Pitfall 4: Codex Output Is JSONL, Not JSON
**What goes wrong:** Attempting to `jq .result output.json` on codex output fails because it's newline-delimited JSON events.
**How to avoid:** This is the locked pass-through contract. Document in the `km agent results` help text that format differs by agent. No code change needed for this phase, but do not silently wrap.

### Pitfall 5: `--claude` and `--codex` Both False (Default Behavior)
**What goes wrong:** If `RunE` requires one of `--claude`/`--codex` to be set, existing `km agent run --prompt "..."` invocations break.
**How to avoid:** When neither flag is set, default to `agentType = "claude"`. The CONTEXT explicitly states: "Default is `--claude` for backward compatibility so no existing invocation breaks."

### Pitfall 6: Interactive Path Unchanged
**What goes wrong:** The refactor touches `runAgentNonInteractive` but the interactive `RunE` in `NewAgentCmdWithDeps` (agent.go:89-128) also loads `profileClaudeArgs` — it must NOT load `codexArgs` for the interactive path (interactive codex is already handled and is out of scope).
**How to avoid:** Scope all changes to `newAgentRunCmd` and `runAgentNonInteractive`. The parent `cmd.RunE` at L89 is out of scope.

---

## Code Examples

### Existing Test Pattern (TestBuildAgentCommand, agent_test.go:820-879)

The six-sub-test table structure is the exact pattern to mirror for codex:

```go
// Source: internal/app/cmd/agent_test.go:820-879
tests := []struct {
    name        string
    base        string
    profileArgs []string
    userArgs    []string
    want        string
}{
    {
        name: "codex ignores claudeArgs from profile",
        base: "codex",
        profileArgs: []string{"--dangerously-skip-permissions"},
        userArgs:    []string{"--flag"},
        want:        "codex --flag",
    },
    // ... etc
}
```

New test cases needed (same table, new entries):

```go
{
    name:        "codex with profile codexArgs",
    // ... BuildAgentShellCommands variant, not BuildAgentCommand
},
{
    name:    "codex invocation contains --json and --dangerously-bypass-approvals-and-sandbox",
    agentType: "codex",
    // assert shell output contains "codex exec --json --dangerously-bypass-approvals-and-sandbox"
},
{
    name:    "codex invocation does NOT contain unset CLAUDE_CODE_USE_BEDROCK",
    agentType: "codex",
    // even with NoBedrock: true in options, assert absence
},
```

### Existing loadProfileCLI Pattern (agent.go:1074-1120)

```go
// Source: internal/app/cmd/agent.go:1083-1089
func loadProfileCLIClaudeArgs(ctx context.Context, cfg *config.Config, sandboxID string) []string {
    p := loadProfileCLI(ctx, cfg, sandboxID)
    if p == nil {
        return nil
    }
    return p.ClaudeArgs
}
```

New twin:
```go
func loadProfileCLICodexArgs(ctx context.Context, cfg *config.Config, sandboxID string) []string {
    p := loadProfileCLI(ctx, cfg, sandboxID)
    if p == nil {
        return nil
    }
    return p.CodexArgs
}
```

### TestCLISpec_ClaudeArgsParsesFromYAML Pattern (types_test.go:818)

The codex test is identical except `claudeArgs` → `codexArgs` in YAML and `p.Spec.CLI.ClaudeArgs` → `p.Spec.CLI.CodexArgs` in assertions.

### Existing Mutual-Exclusion Pattern (agent.go:184)

```go
// Source: internal/app/cmd/agent.go:184 — mirror this for --codex/--no-bedrock
if interactive && wait {
    return fmt.Errorf("--interactive and --wait are mutually exclusive")
}
```

New checks (first in RunE):
```go
if useClaude && useCodex {
    return fmt.Errorf("--claude and --codex are mutually exclusive")
}
if useCodex && noBedrock {
    return fmt.Errorf("--no-bedrock is only valid with --claude")
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Hardcoded `claude -p` in BuildAgentShellCommands | Agent-type branching | Phase 58 | Enables codex runs |
| Variadic `noBedrock ...bool` | `AgentRunOptions` struct | Phase 58 | Cleaner signature, testable |
| `claudeArgs`-only in CLISpec | `claudeArgs` + `codexArgs` parallel fields | Phase 58 | Per-agent profile defaults |

**Existing from commit 4dbbe63 (yesterday):**
- `BuildAgentCommand(base, profileClaudeArgs, userArgs)` — interactive-path helper; already handles `base == "codex"` by not applying `profileClaudeArgs` (see agent.go:1128). This pattern should guide the non-interactive refactor.
- `loadProfileCLIClaudeArgs` — the exact function to twin.
- `TestCLISpec_ClaudeArgsParsesFromYAML` + `TestCLISpec_ClaudeArgsOptional` — the exact tests to mirror.
- `TestBuildAgentCommand` with 6 sub-tests including "codex ignores claudeArgs from profile" — confirms codex awareness already exists in `BuildAgentCommand`.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` package (standard library) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./internal/app/cmd/ -run TestAgent -v` |
| Full suite command | `go test ./internal/app/cmd/... ./pkg/profile/...` |

### Phase Requirements → Test Map

| Behavior | Test Type | Automated Command | File Exists? |
|----------|-----------|-------------------|-------------|
| `BuildAgentShellCommands` emits `codex exec --json --dangerously-bypass-approvals-and-sandbox` for codex | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_CommandConstruction -v` | Wave 0 extension |
| `BuildAgentShellCommands` claude path unchanged (regression) | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_CommandConstruction -v` | ✅ exists |
| `--no-bedrock` unset stanza absent from codex script | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_NoBedrock -v` | Wave 0 extension |
| `--codex --no-bedrock` returns error before SSM call | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_CodexNoBedrock -v` | Wave 0 new |
| `codexArgs` from profile plumbed into codex invocation | unit | `go test ./internal/app/cmd/ -run TestBuildAgentShellCommands_Codex -v` | Wave 0 new |
| `spec.cli.codexArgs` parses from YAML | unit | `go test ./pkg/profile/ -run TestCLISpec_CodexArgs -v` | Wave 0 new |
| No args default to claude (backward compat) | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive -v` | ✅ verify |
| `--claude` explicit same as default | unit | covered by above | ✅ |

### Sampling Rate

- **Per task commit:** `go test ./internal/app/cmd/ -run TestAgent -v && go test ./pkg/profile/ -run TestCLISpec -v`
- **Per wave merge:** `go test ./internal/app/cmd/... ./pkg/profile/...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `TestAgentNonInteractive_CommandConstruction` — extend with codex sub-case
- [ ] `TestAgentNonInteractive_NoBedrock` — extend with "codex: no unset stanza even with NoBedrock true"
- [ ] `TestAgentNonInteractive_CodexNoBedrock` — new test: `--codex --no-bedrock` returns error string before SSM
- [ ] `TestBuildAgentShellCommands_Codex` — new test: full options struct, verify codex invocation shape
- [ ] `TestCLISpec_CodexArgsParsesFromYAML` — new test: mirror of claudeArgs test
- [ ] `TestCLISpec_CodexArgsOptional` — new test: codexArgs absent → nil

---

## Open Questions

1. **JSONL vs single JSON in output.json**
   - What we know: `codex exec --json` produces JSONL (newline-delimited JSON events), not a single JSON object.
   - What's unclear: Whether `km agent results` consumers will need updated documentation or whether the CONTEXT's pass-through decision implicitly accepts this.
   - Recommendation: Add a note to the `km agent results` help text in agent.go stating that output format depends on the agent used. No functional change required.

2. **`--claude`/`--codex` flag on parent `agent` cmd vs `run` subcommand**
   - What we know: The parent `NewAgentCmdWithDeps` already has `--claude`/`--codex` flags (agent.go:132-133). The `run` subcommand in `newAgentRunCmd` does NOT currently have them — that's the gap.
   - What's unclear: Whether Cobra will complain about duplicate flag names on parent vs subcommand.
   - Recommendation: Because Cobra flags are scoped per-command and the `run` subcommand is separate, adding `--claude`/`--codex` to `newAgentRunCmd` is safe. The parent flags are for interactive mode; the subcommand flags are for non-interactive.

3. **`BuildAgentShellCommands` signature change breaks test at agent_test.go:140**
   - What we know: `TestAgentNonInteractive_CommandConstruction` calls `cmd.BuildAgentShellCommands(prompt, "")` at line 140. If signature changes to struct, this breaks.
   - Recommendation: Update all call sites in the same wave as the signature change. There are exactly three call sites: agent.go:459 (production), agent_test.go:140, agent_test.go:173/180.

---

## Sources

### Primary (HIGH confidence)
- Official OpenAI Codex CLI documentation — https://developers.openai.com/codex/noninteractive
- Official OpenAI Codex CLI reference — https://developers.openai.com/codex/cli/reference
- `internal/app/cmd/agent.go` (direct read) — BuildAgentShellCommands L1020-1072, newAgentRunCmd L152-216, loadProfileCLI family L1074-1120, BuildAgentCommand L1122-1143
- `internal/app/cmd/agent_test.go` (direct read) — TestAgentNonInteractive_* L137-193, TestBuildAgentCommand L820-879
- `pkg/profile/types.go` (direct read) — CLISpec L354-367
- `pkg/profile/schemas/sandbox_profile.schema.json` (direct read) — cli section L474-488
- `pkg/profile/types_test.go` (direct read) — TestCLISpec_ClaudeArgsParsesFromYAML
- `profiles/learn.yaml` (direct read) — codex install at L63-66

### Secondary (MEDIUM confidence)
- OpenAI Codex auth docs — https://developers.openai.com/codex/auth — `~/.codex/auth.json` credential path confirmed

### Tertiary (LOW confidence)
- Community reports on `OPENAI_API_KEY` vs `CODEX_API_KEY` behavior — relevant only if auth issues arise (deferred by design)

## Metadata

**Confidence breakdown:**
- Codex CLI flags: HIGH — verified against official OpenAI docs
- Code locations: HIGH — direct file reads
- Refactor pattern: HIGH — exact signatures and call sites read from source
- Test patterns: HIGH — existing tests read directly, new tests derived from parallel patterns

**Research date:** 2026-04-19
**Valid until:** 2026-05-19 (stable CLI, conservative estimate)
