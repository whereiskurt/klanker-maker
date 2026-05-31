# Phase 92: Profile Spec Restructure — Research

**Researched:** 2026-05-31
**Domain:** Go profile schema + compiler + CLI cmd restructure
**Confidence:** HIGH (all claims grounded in direct code inspection + authoritative external docs)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- No backwards compatibility. YAML decoder rejects legacy keys (`identity:`, `cli.notify*`, `cli.vscodeEnabled`, `cli.agent`, dead `agent:` shape). `additionalProperties: false` schema-side. Profile fixtures rewritten atomically in the owning wave.
- `iam:` not `aws:`. Section is specifically IAM; AWS-scoped naming over-broad.
- `sessionPolicy` deleted without replacement.
- `allowedSecretPaths` lives under `iam:` (not under `secrets:`).
- `vscodeEnabled` moves to `runtime.vscode.enabled`. Tri-state `*bool` preserved; `IsVSCodeEnabled()` helper updated.
- `notification:` is optional. Sub-blocks optional. Booleans default false. Absent block produces same userdata as today's absent CLI notify fields.
- Notify env var names unchanged. Sandbox-side `KM_NOTIFY_ON_PERMISSION`, `KM_NOTIFY_SLACK_ENABLED`, `KM_SLACK_MENTION_ONLY`, etc. keep current names.
- `agent:` is optional. Absent → default `claude`, no tool gating beyond Claude Code defaults, no Codex config.
- `permissions:` is `map[string]any` passthrough. Type the high-value keys (`tools.autoApprove`, `tools.deny`, `trustedDirectories`); passthrough the rest.
- Validation forbids mixed mode. `agent.claude.tools.autoApprove` populated AND `execution.configFiles[".claude/settings.json"]` present → hard validation error.
- Notify-hook merge layer preserved. Synthesizer → notify-hook merge → write. Order tested.
- `cli.noBedrock` survives as the lone `cli:` field.
- Codex synthesizer is research-gated. If Codex 0.133 has no native tool allow/deny, Wave 5 emits inert config + args echo + documented asymmetry.
- Inherit fix is typed, not reflection-extended.
- Zero running sandboxes. No backwards compatibility required. All profile YAMLs may be rewritten in place.

### Claude's Discretion
- Exact `scripts/validate-all-profiles.sh` vs `make validate-profiles` target name.
- Whether `additionalSnapshots` placeholder snapshot IDs need synthetic values vs schema relaxation (Wave 1 decides).
- Whether `notification.slack.invites.emails` flattens to `notification.slack.inviteEmails` (Wave 2 decides).
- Whether `notification.events.{onIdle,onPermission}` become a list `notify: [idle, permission]` (Wave 2 decides).
- Whether `roleSessionDuration` becomes `int` seconds (deferred — orthogonal).
- Whether `allowedRegions: minItems: 1` relaxes to allow empty list (Wave 1 decides).
- Specific test fixture choice for `agent_claude_golden_test.go` beyond learn.v2/dc34/locked/codex.
- Whether `pkg/profile/configfiles/*` gets retired vs trimmed.

### Deferred Ideas (OUT OF SCOPE)
- `roleSessionDuration` type change (int seconds vs string pattern).
- Re-deriving the 4 user-facing changes.
- Re-deciding the wave dependency graph.
- Backwards-compat strategy (locked: no backcompat).
</user_constraints>

---

## Summary

Phase 92 restructures the SandboxProfile spec in six implementation waves. The research confirms all major PRD claims with one correction on the CLISpec field count, one correction on the `security.go` site count (PRD says 3; actually the IAM rename touches 3 in `security.go` plus 2 in `service_hcl.go`), and one correction on the "notify sites" count (~21 `.CLI.` reads in `userdata.go`, not ~30 raw KM_NOTIFY occurrences which number 110+ including template variables). The wave design is well-grounded in current code.

The most important Wave 0 finding: **Codex CLI 0.133 has no native tool allow/deny in config.toml AND its hooks do not fire**. The existing codebase already documents this in `docs/codex-parity.md:82-89`. Wave 5's Codex synthesizer scope is confirmed: emit existing inert config + args echo + documented asymmetry note.

For Claude Code, the canonical deny location is `permissions.deny` (NOT a top-level `disallowedTools` field). The legacy `autoApprove` key in existing profiles is valid but the new canonical form is `permissions.allow`. Migration-equivalence must be verified in the golden tests.

**Primary recommendation:** Proceed as designed. Wave 0 research spike is resolved. Wave 5 Codex synthesizer scope is "inert config + args echo + asymmetry doc" — there is no real allow/deny to synthesize.

---

## 1. Wave 0 Research Spike Results

### 1a. Codex CLI 0.133 `~/.codex/config.toml` — Tool Allow/Deny

**CONCLUSION: Codex 0.133 has NO native tool allow/deny in config.toml. Hooks also do not fire. (HIGH confidence)**

The full research is in `.planning/research/codex-config-toml.md`. Summary:

**What exists in config.toml:**
- `approval_policy` — gates when Codex pauses for approval (`"untrusted"`, `"on-request"`, `"never"`, `{ granular = {...} }`)
- `sandbox_mode` — filesystem/network scope at OS level (`"workspace-write"`, `"read-only"`, `"danger-full-access"`)
- `[permissions.<name>]` — named profiles for filesystem/network path restrictions (NOT per-tool)
- `[features]` — feature toggles including `hooks = true`
- `[mcp_servers.<id>]` — `enabled_tools`/`disabled_tools` apply to MCP tools only, not built-in tools

**What does NOT exist:**
- No `tools.allow` or `tools.deny` for built-in tools (bash, file read/write)
- No equivalent to Claude Code's `permissions.allow`/`permissions.deny` for arbitrary tool names

**Critical in-codebase confirmation:**
`docs/codex-parity.md:82-89` and `pkg/compiler/userdata.go:1821` both document the Phase 70 spike result:
> "Codex 0.121/0.133 spike confirmed hooks do NOT fire from `~/.codex/config.toml`"

**Wave 5 decision (from locked Codex synthesizer decision):**
`synthesizeCodexConfig()` emits:
1. Existing notify hook entries (`[[hooks.PermissionRequest]]`, `[[hooks.Stop]]`) — forward-compat, currently inert
2. `args:` echo via `approval_policy`/`sandbox_mode` if `agent.codex.args` contains these flags
3. Logged note when `agent.codex.tools.autoApprove`/`deny` are populated: "Codex 0.133 does not support tool gating in config.toml. Fields preserved for future release support."
4. `docs/agent-tool-gating.md` documents the asymmetry explicitly

Source: `.planning/research/codex-config-toml.md`, `docs/codex-parity.md`, official OpenAI Codex docs at https://developers.openai.com/codex/config-reference

### 1b. Claude Code 2.1.132 `settings.json` Deny Canonical Location

**CONCLUSION: `permissions.deny` is canonical. No top-level `disallowedTools` key in settings.json. (HIGH confidence)**

The full research is in `.planning/research/codex-config-toml.md`. Summary:

**Canonical structure:**
```json
{
  "permissions": {
    "allow": ["Bash", "Read", "Write", "Edit", "Glob", "Grep", "WebFetch", "WebSearch"],
    "deny":  ["SomeDeniedTool"]
  },
  "trustedDirectories": ["/home/sandbox", "/workspace"]
}
```

**Key facts:**
- `permissions.deny` is the canonical deny field. `disallowedTools` is a CLI flag only, not a settings.json key.
- `permissions.allow` is the canonical autoApprove field. The legacy `autoApprove` top-level key is still honored but deprecated.
- Deny rule evaluation: `deny → ask → allow`. Deny always wins regardless of scope.
- A bare tool name in deny (e.g., `"WebFetch"`) removes the tool from Claude's context entirely.
- `trustedDirectories` is a valid settings.json key (distinct from `permissions.additionalDirectories`).

**Migration note:**
Existing profiles use `{"autoApprove": [...], "trustedDirectories": [...]}` inline JSON. The synthesizer MUST convert this to `{"permissions": {"allow": [...]}, "trustedDirectories": [...]}`. These are semantically equivalent; Claude Code honors both. Golden tests must verify the migration produces bit-identical effective behavior.

**Wave 5 `synthesizeClaudeSettings` output shape:**
```json
{
  "permissions": {
    "allow": ["Bash", "Read", ...],
    "deny": []
  },
  "trustedDirectories": ["/home/sandbox", "/workspace"]
}
```
Then `mergeNotifyHookIntoSettings` appends hooks. The `permissions:` passthrough map is deep-merged.

Source: https://code.claude.com/docs/en/permissions, https://code.claude.com/docs/en/settings, https://json.schemastore.org/claude-code-settings.json

---

## 2. Codebase Verification

### 2a. `pkg/profile/types.go` — IdentitySpec and AgentSpec

**CONFIRMED. Exact locations:**
- `IdentitySpec` defined at line 277. Fields: `RoleSessionDuration`, `AllowedRegions`, `SessionPolicy` (dead), `AllowedSecretPaths`
- `Spec.Identity IdentitySpec` at line 35 (value-typed, not pointer)
- `AgentSpec` defined at line 372. Fields: `MaxConcurrentTasks` (dead), `TaskTimeout` (dead), `AllowedTools` (dead)
- `Spec.Agent AgentSpec` at line 38 (value-typed, not pointer)

**CLISpec field count:** 19 fields total (PRD says "14 scattered notify* fields" — see breakdown):
- `NoBedrock` — survives
- `ClaudeArgs` — moves to `agent.claude.args`
- `CodexArgs` — moves to `agent.codex.args`
- `Agent` — moves to `agent.default`
- `NotifyOnPermission` → `notification.events.onPermission`
- `NotifyOnIdle` → `notification.events.onIdle`
- `NotifyCooldownSeconds` → `notification.events.cooldownSeconds`
- `NotificationEmailAddress` → `notification.email.address`
- `NotifyEmailEnabled` → `notification.email.enabled`
- `NotifySlackEnabled` → `notification.slack.enabled`
- `NotifySlackPerSandbox` → `notification.slack.perSandbox`
- `NotifySlackChannelOverride` → `notification.slack.channelOverride`
- `SlackArchiveOnDestroy` → `notification.slack.archiveOnDestroy`
- `NotifySlackInboundEnabled` → `notification.slack.inbound.enabled`
- `NotifySlackTranscriptEnabled` → `notification.slack.transcript.enabled`
- `NotifySlackInviteEmails` → `notification.slack.invites.emails`
- `UseSlackConnect` → `notification.slack.invites.useConnect`
- `VSCodeEnabled` → `runtime.vscode.enabled`
- `NotifySlackInboundMentionOnly` → `notification.slack.inbound.mentionOnly`

Total: 18 fields move out of CLISpec; 1 (`NoBedrock`) stays. PRD says "14" but the actual count of notify-related fields is 15 (rows 5-19 above) plus 3 agent-related (rows 2-4) plus `vscodeEnabled`. The "14 scattered notify* fields" in the PRD appears to count only the fields with `Notify` prefix (excluding `vscodeEnabled`, `useSlackConnect`, `slackArchiveOnDestroy`, `claudeArgs`, `codexArgs`, `agent`). The actual restructure scope is larger.

### 2b. `pkg/profile/inherit.go` Line References

**CONFIRMED with exact line numbers:**
- Line 101: `mergeSpecSection(&result.Spec.Identity, &parent.Spec.Identity, &child.Spec.Identity)` — becomes `&result.Spec.IAM` in Wave 1
- Line 104: `mergeSpecSection(&result.Spec.Agent, &parent.Spec.Agent, &child.Spec.Agent)` — deleted in Wave 1 (dead block)

**Inheritance bug confirmation:** Pointer-typed sections (`CLI *CLISpec`, `Email *EmailSpec`, `Budget *BudgetSpec`, `OTP *OTPSpec`, `Secrets *SecretsSpec`, `Artifacts *ArtifactsSpec`) are NOT called through `mergeSpecSection`. They fall through to `result.Spec = child.Spec` (line 80), which means if a child profile sets any one CLI field, the parent's entire `CLI` spec is replaced by the child's pointer. This is the zero-replace bug the PRD describes. Wave 2 typed `mergeNotificationSpec` and Wave 4 typed `mergeAgentSpec` fix this pattern.

### 2c. `pkg/compiler/security.go` — Identity Sites

**CONFIRMED: Exactly 3 `p.Spec.Identity` sites in `security.go`:**
- Line 50: `p.Spec.Identity.RoleSessionDuration`
- Line 56: `p.Spec.Identity.AllowedRegions`
- Line 74: `p.Spec.Identity.AllowedSecretPaths`

**Additional: `service_hcl.go` has 2 more sites (not mentioned in PRD claim but present):**
- Line 1032: `len(p.Spec.Identity.AllowedSecretPaths) > 0`
- Line 1033: `strings.Join(p.Spec.Identity.AllowedSecretPaths, ",")`

Total IAM rename sites: 3 in `security.go` + 2 in `service_hcl.go` = 5 sites. PRD's "3 sites" refers only to `security.go`. Both files need renaming in Wave 1.

### 2d. `pkg/compiler/userdata.go` — Notify Emission Site Count

**VERIFIED: userdata.go is exactly 4211 lines** — PRD claim confirmed.

**CLI spec reads in userdata.go:** 21 `.CLI.` field reads (not ~30). The PRD's "~30 notify emission sites" refers to total distinct ENV var emissions counting template-level variables; the actual Go-level `.CLI.` field read count is 21. Both are reasonable characterizations of different things. For Wave 3 planning, note:

Direct `.CLI.` field reads in `userdata.go` (comprehensive list):
- Lines 3651, 3664: comments referencing `CLI` fields
- Line 3866: `p.Spec.CLI.Agent` (codex check)
- Lines 3972-3973: `NotifyOnPermission`, `NotifyOnIdle`
- Lines 3979-3980: `NotifyCooldownSeconds`
- Lines 3982-3983: `NotificationEmailAddress`
- Lines 3988-3989: `NotifyEmailEnabled`
- Lines 3991-3992: `NotifySlackEnabled`
- Line 4000: `NotifySlackEnabled` (second check for channel block)
- Lines 4010-4011: `NotifySlackChannelOverride`
- Line 4021: `NotifySlackInboundEnabled`
- Line 4038: `CLI.Agent` (codex KM_AGENT var)
- Line 4047: `NotifySlackInboundEnabled` (repeat in inbound poller block)
- Line 4079: `NotifySlackTranscriptEnabled`
- Line 4090: `NotifySlackEnabled` (third check for transcript)
- Lines 4001-4004: `resolveMentionOnly(p.Spec.CLI)` (calls into `NotifySlackInboundMentionOnly`)

Plus `VSCodeEnabled` at lines 3653-3655 (template parameter field set at line 4198), and all template lines that expand `{{- if .VSCodeEnabled }}`, `{{- if .NotifyEnv }}`, etc.

### 2e. JSON Schema State

**CONFIRMED:**
- `identity` block at schema line 399; `required: ["roleSessionDuration", "allowedRegions", "sessionPolicy"]` at line 401
- `sessionPolicy` is in `required` (must be removed in Wave 1)
- `allowedSecretPaths` is **NOT in the schema** (confirmed: grep returns nothing) — this IS the schema drift the PRD describes
- `agent` block at schema line 488; `required: ["maxConcurrentTasks", "taskTimeout"]` at line 490
- `additionalProperties: false` exists on `spec:` object (line 61), confirming legacy key rejection is schema-enforced
- `identity` in `spec.required` array at line 56; `agent` in `spec.required` array at line 59

**The schema is authoritative for rejection.** When `additionalProperties: false` is on a block and a profile has a key not in `properties`, the JSON schema validator rejects it. The existing Go-yaml decoder + schema validation pipeline already enforces this. No extra code needed for the "decoder rejects legacy keys" behavior — just update the schema.

### 2f. `pkg/profile/configfiles/` Directory

**CONFIRMED ABSENT:** There is no `pkg/profile/configfiles/` directory. The `schemas/` folder contains one file (`sandbox_profile.schema.json`) and one test script. The PRD reference to "possibly retired" was incorrect — the directory does not exist. Wave 5 has nothing to retire here.

**Correction to PRD:** `pkg/profile/configfiles/` mentioned in the Components Touched table does not exist. Planner should remove this row from the Wave 5 plan.

### 2g. `notifySlackInviteEmails` CLI Command References

**CONFIRMED. Non-test files that reference `NotifySlackInviteEmails`:**
- `internal/app/cmd/create_slack.go:224` — loop over `cli.NotifySlackInviteEmails`

**Other CLI files reading notify/slack fields (from grep across `internal/app/cmd/`):**
- `agent.go` — `Spec.CLI.Agent`, `ClaudeArgs`, `CodexArgs`
- `create_slack_inbound.go` — Slack inbound provisioning reads CLI fields
- `create_slack.go` — `resolveSlackChannel` reads CLI notify/slack fields
- `create.go` — `IsVSCodeEnabled`, Slack provisioning gating
- `destroy_slack.go` — `archiveOnDestroy` read path
- `doctor.go` — `checkSlackBotUserIDCached` reads `notification.slack.inbound.mentionOnly`
- `doctor_codex.go` — `prof.Spec.CLI.Agent`

The PRD additionally lists `shell.go` — shell.go does NOT directly reference `Spec.CLI` notify fields (only `IsVSCodeEnabled` via `create.go:620`, `create.go:2060`).

### 2h. `mergeSpecSection` Reflection Behavior

**CONFIRMED:** `mergeSpecSection` is value-type only. It uses `reflect.ValueOf(child).Elem().IsZero()` to check if the child section is the zero value. This works correctly for value-typed struct fields. It does NOT handle pointer types (where `IsZero()` would check if the pointer is nil, not whether the pointed-to struct is zero-value). Since `Spec.CLI`, `Spec.Budget`, etc. are pointer-typed, they are NOT in the `mergeSpecSection` call chain at all — they inherit directly from `child.Spec` on line 80.

### 2i. Profile Inventory Count

**CONFIRMED:**
- `profiles/` directory: 12 files (ao.yaml, codex.yaml, dc34.yaml, dc34.ami.yaml, example-additional-snapshots.yaml, goose.yaml, learn.v2.yaml, learn.v2.chatty.yaml, learn.v2.codex.yaml, learn.v2.polite.yaml, locked.yaml, locked.ami.yaml) — matches PRD
- `pkg/profile/builtins/`: 8 files (ao.yaml, codex.yaml, goose.yaml, hardened.yaml, learn.yaml, open-dev.yaml, restricted-dev.yaml, sealed.yaml) — matches PRD
- Total: 20 — matches PRD

### 2j. `allowlistgen/generator.go` — Dead Agent Field Reference

**FOUND ADDITIONAL SITE (PRD does not mention):**
`pkg/allowlistgen/generator.go:96-99` constructs a profile with the dead `AgentSpec{MaxConcurrentTasks: 1, TaskTimeout: "30m"}`. This file must be updated in Wave 1 when `AgentSpec` is redefined.

---

## 3. Test Scaffolding Strategy

### 3a. Existing Golden Test Pattern

The existing golden test pattern in `pkg/compiler/userdata_test.go` is:
1. Build a profile programmatically using `baseProfile()` helper
2. Call `generateUserData(p, ...)` 
3. Compare `string(got) != string(want)` using `diffStrings()` helper
4. Golden file stored in `pkg/compiler/testdata/userdata_additional_volume_only.golden.sh`
5. **Update mechanism: delete the golden file and re-run tests to regenerate**

No `make test-update-golden` target exists. The golden file is regenerated by deleting it and running `go test ./pkg/compiler/...`. Wave 0 must capture the baseline by running `generateUserData` on `profiles/learn.v2.yaml` against pre-phase main and writing it to a `.golden.sh` file.

### 3b. Test Files Requiring Deletion/Update Due to Dead-Field Removal

**Files in `pkg/profile/` that reference dead `Spec.Agent` fields:**
- `pkg/profile/types_test.go:167` — `p.Spec.Agent.MaxConcurrentTasks` — must be deleted/updated in Wave 1

**Files in `pkg/compiler/testdata/` that contain dead `agent:` block:**
All 9 compiler testdata YAML files contain `maxConcurrentTasks` + `taskTimeout` in the `agent:` block. These must be updated in Wave 1:
- `ec2-basic.yaml`, `ec2-empty-repos.yaml`, `ec2-with-allowed-refs.yaml`, `ec2-with-secrets.yaml`, `ec2-with-budget.yaml`
- `ecs-basic.yaml`, `ecs-empty-repos.yaml`, `ecs-with-github.yaml`
- `docker-basic.yaml`, `docker-with-budget.yaml`

**`pkg/allowlistgen/generator.go:96-99`** — constructs `AgentSpec{MaxConcurrentTasks: 1, TaskTimeout: "30m"}`. Must be updated in Wave 1 when `AgentSpec` is redefined.

**No existing compiler `*_test.go` files reference `MaxConcurrentTasks` or `TaskTimeout` directly** — the dead fields are only in testdata YAMLs and `types_test.go`.

### 3c. Baseline Capture Mechanism for Wave 0 RED Tests

For `userdata_phase92_byte_identity_test.go` (Wave 0):
```go
// Golden file: pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh
// Capture: run generateUserData on profiles/learn.v2.yaml before Wave 1 changes
// Update: delete golden file + re-run tests
```

For `security_phase92_byte_identity_test.go` (Wave 0):
```go
// Golden file: pkg/compiler/testdata/security_iam_pre92_baseline.golden.hcl
// Capture: serialize the IAMSessionPolicy output from a representative profile
// Update: delete golden file + re-run tests
```

The test must be RED initially (golden file absent), go GREEN after the golden file is committed, then stay GREEN through Waves 1-5 as the schema migrates but the compiled output stays identical.

### 3d. Existing Test Infrastructure Notes

- No snapshot library (jest-style) exists — manual golden files only
- No `make test-update-golden` target — update by deleting + re-running
- `diffStrings()` utility at `pkg/compiler/userdata_test.go:2047` is reusable across the new test files
- `baseProfile()` helper at `pkg/compiler/service_hcl_test.go:12` and `pkg/compiler/userdata_test.go` — reusable foundation for new tests
- No CI workflows directory (`/.github/workflows/` absent) — no CI gate to add `validate-all-profiles.sh` to
- `pkg/profile/inherit_test.go` exists — the Wave 2 RED test for notification inheritance should mirror its test structure

---

## 4. Operational / Integration Notes

### 4a. Schema Enforcement Mechanism

**`additionalProperties: false` is schema-enforced.** The schema at `pkg/profile/schemas/sandbox_profile.schema.json` has `additionalProperties: false` at:
- The root document level (line 8)
- The `spec:` object level (line 61)
- Every sub-object block

When a profile YAML has `identity:` (old key) and the schema now has only `iam:`, the JSON schema validator will reject the profile with a clear error. No extra Go code is needed for the "decoder rejects legacy keys" behavior — this is entirely schema-driven. The YAML decoder uses `goccy/go-yaml` with strict mode.

**Schema enforcement order:** `Parse()` → `ValidateSchema()` (JSON schema validation) → `ValidateSemantic()`. The schema gate catches structural errors before semantic validators run.

### 4b. Lambda Redeploy Triggers

**Wave 1 ships a breaking schema change** that remote `km create` Lambda calls will fail on pre-Phase-92 `km` binaries. The post-merge runbook requires:
```bash
make build && km init --sidecars
```
This is stated in the PRD post-merge runbook. No existing sandboxes exist (locked decision: zero running sandboxes), so the Lambda binary refresh is the only required step.

**Why `km init --sidecars` (not full `km init`):** `--sidecars` rebuilds binaries and forces a Lambda cold-start, which is sufficient for schema change propagation. It does NOT update Terraform-managed Lambda environment variables, but Phase 92 adds no new Lambda env vars (notification changes are YAML-side only; KM_SLACK_* env var names are unchanged per locked decision).

**Exception for Wave 3:** If `notification.slack.invites.*` changes how `km create` calls the Slack API, the create Lambda bundle also needs refreshing. `km init --sidecars` covers this since the create handler binary is rebuilt.

### 4c. Mid-Path Slack Flow Consumers

The Slack provisioning flow traces through these points (all must be updated in Wave 3):

1. `internal/app/cmd/create_slack.go` — `resolveSlackChannel()` reads `cli.NotifySlackPerSandbox`, `cli.NotifySlackChannelOverride`, `cli.NotifySlackEnabled`, `cli.NotifySlackInviteEmails`, `cli.UseSlackConnect`
2. `internal/app/cmd/create_slack_inbound.go` — reads `cli.NotifySlackInboundEnabled`, `cli.NotifySlackEnabled`, `cli.NotifySlackPerSandbox`
3. `internal/app/cmd/create.go` — reads `IsVSCodeEnabled()`, Slack provisioning gating (`cli.NotifySlackEnabled`, `cli.NotifySlackPerSandbox`)
4. `internal/app/cmd/agent.go` — reads `cli.Agent`, `cli.ClaudeArgs`, `cli.CodexArgs`
5. `internal/app/cmd/destroy_slack.go` — reads `cli.SlackArchiveOnDestroy`
6. `internal/app/cmd/doctor.go` — reads `cli.NotifySlackInboundMentionOnly` via `notification.slack.inbound.mentionOnly` post-Wave-3

**Non-obvious mid-path consumers:**
- `pkg/compiler/userdata.go:3866` reads `Spec.CLI.Agent` to emit `KM_AGENT` env var. In Wave 4, this moves to `Spec.Agent.Default`.
- `internal/app/cmd/doctor_codex.go:260` reads `prof.Spec.CLI.Agent` to determine if a sandbox uses Codex. In Wave 4, this moves to `prof.Spec.Agent.Default`.
- `internal/app/cmd/doctor.go:3346` reads `prof.Spec.CLI.Agent`. Same update required in Wave 4.

**DDB / Lambda env consumers:** `KM_SLACK_MENTION_ONLY` is read by the bridge Lambda from the sandbox's userdata env. The Lambda does NOT parse SandboxProfile YAML — it reads from DDB `km-sandboxes` table which gets the value from userdata. So as long as the env var NAME is preserved (locked: it is), the bridge Lambda requires no changes.

### 4d. Schema `additionalProperties` and VSCode

`vscodeEnabled` currently lives under `cli:` in the schema (`sandbox_profile.schema.json:604`). After Wave 2, it moves to `runtime.vscode.enabled`. The `RuntimeSpec` struct needs a new `VSCode *RuntimeVSCodeSpec` field added. The existing `IsVSCodeEnabled(cli *CLISpec)` helper in `pkg/profile/types.go:560` must be updated to accept `*RuntimeSpec` or `*RuntimeVSCodeSpec` in Wave 2.

**Warning:** `create.go:620` and `create.go:2060` both call `profile.IsVSCodeEnabled(resolvedProfile.Spec.CLI)`. These must be updated in Wave 2 to `profile.IsVSCodeEnabled(resolvedProfile.Spec.Runtime.VSCode)`. The `vscode.go:422` error message references `"spec.cli.vscodeEnabled"` — this string must be updated to `"spec.runtime.vscode.enabled"`.

---

## 5. Risk Validation

### 5a. `userdata.go` Line Count

**CONFIRMED:** `wc -l` gives exactly 4211 lines. PRD claim verified.

### 5b. Notify Site Count

**ACTUAL COUNT: 21 `.CLI.` field reads in `userdata.go`** (vs PRD's "~30 notify emission sites"). The discrepancy is because:
- PRD counts "emission sites" = places where `KM_NOTIFY_*` env vars are written to the `notifyEnv` map, which is a different cardinality than Go field reads
- Total `KM_NOTIFY`/`KM_SLACK` occurrences (template-level) in userdata.go: 110+ (many are within heredoc template strings, not field reads)
- Direct `p.Spec.CLI.*` field reads: 21

For Wave 3 implementation planning, the actual change scope is 21 field renames plus the `resolveMentionOnly` function refactor. Not ~30.

### 5c. Dead Agent Field — Risk of Dangling Tests

**CONFIRMED RISK.** One test references dead agent fields:
- `pkg/profile/types_test.go:167`: `p.Spec.Agent.MaxConcurrentTasks` — will fail to compile after Wave 1 redefines `AgentSpec`

One additional non-test file uses dead fields:
- `pkg/allowlistgen/generator.go:97-98` — uses `MaxConcurrentTasks`, `TaskTimeout`

Both must be updated in Wave 1 or the codebase will not compile.

### 5d. Synthesizer Correctness Risk

**CRITICAL NOTE on `autoApprove` vs `permissions.allow`:**
Existing profiles inline `{"autoApprove": [...]}` in `configFiles[".claude/settings.json"]`. Claude Code still honors the legacy `autoApprove` key. The Wave 5 synthesizer must decide:
- Option A: Emit legacy `autoApprove` for backwards compatibility (simpler, still works)
- Option B: Emit canonical `permissions.allow` (correct going forward)

Research recommendation: **emit `permissions.allow`** (Option B). Claude Code honors both, golden tests will verify byte equivalence of the effective behavior, and using the canonical API avoids tech debt. Document this choice in `docs/agent-tool-gating.md`.

### 5e. Schema Drift — `allowedSecretPaths` Missing from Schema

**CONFIRMED DRIFT:** `allowedSecretPaths` is read by `pkg/compiler/security.go:74` and `pkg/compiler/service_hcl.go:1032` but is NOT present in `sandbox_profile.schema.json`. This means profiles with `identity.allowedSecretPaths:` currently pass `km validate` without schema coverage of that field.

In Wave 1, renaming `identity:` → `iam:` and adding `allowedSecretPaths` to the `iam` schema block fixes the drift. This is also why `allowedRegions: minItems: 1` exists (Wave 1 decides whether to relax it) — `security.go:56` already handles empty `AllowedRegions` by producing an empty region list, so the compiler supports it even though the schema rejects it.

### 5f. `pkg/profile/configfiles/` Directory — Does Not Exist

**DISCREPANCY:** PRD Components Touched table includes `pkg/profile/configfiles/*` as "Wave 5, possibly retired." This directory does not exist in the codebase. The planner should remove this row from Wave 5 tasks.

---

## 6. Validation Architecture

> `workflow.nyquist_validation` not explicitly set to false in `.planning/config.json` — Validation Architecture is required.

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing package (stdlib) — no external test framework |
| Config file | none (go test ./...) |
| Quick run command | `go test ./pkg/profile/... ./pkg/compiler/...` |
| Full suite command | `go test ./...` |

### Phase Verification Criteria → Test Map

| VC# | Criterion | Test Type | Target File | Gating Wave | Failure Mode | Success Mode |
|-----|-----------|-----------|-------------|-------------|--------------|--------------|
| 1 | `go test ./...` passes across all waves | build + unit | — | All waves | compile error or test failure | zero failures |
| 2 | `km validate` passes on all 20 fixtures; rejects stale-key fixtures | unit | `pkg/profile/validate_test.go` + new `validate_legacy_keys_test.go` | Wave 1 (reject), Waves 1/3/5 (pass) | validator accepts legacy key OR rejects valid new key | clear error on `identity:` / `agent.maxConcurrentTasks:` / `cli.notifySlackEnabled:` |
| 3 | Userdata byte-identity: `profiles/learn.v2.yaml` pre- vs post-Wave-5 | golden | `pkg/compiler/userdata_phase92_byte_identity_test.go` | Wave 0 (RED capture), Wave 3+ (GREEN) | compiled userdata differs from pre-phase baseline | string comparison passes |
| 4 | IAM Terraform byte-identity post-Wave-1 | golden | `pkg/compiler/security_phase92_byte_identity_test.go` | Wave 0 (RED capture), Wave 1 (GREEN) | `aws_iam_role.max_session_duration` or region_lock HCL differs | string comparison passes |
| 5 | Synthesizer golden tests: learn.v2, dc34, locked, codex fixtures | golden | `pkg/compiler/agent_claude_golden_test.go`, `pkg/compiler/agent_codex_golden_test.go` | Wave 0 (RED), Wave 5 (GREEN) | synthesized settings.json or config.toml differs from fixture | byte-identical to golden file |
| 6 | Mixed-mode rejection: `agent.claude.tools.autoApprove` + `configFiles[".claude/settings.json"]` → error | unit | `pkg/profile/validate_mixed_settings_test.go` | Wave 0 (RED), Wave 4 (GREEN) | validator accepts conflicting fields | ValidationError with clear path |
| 7 | Inheritance fix: child-only-transcript flag inherits parent perSandbox | unit | `pkg/profile/inherit_notification_test.go` | Wave 0 (RED), Wave 2 (GREEN) | child's `notification.slack.perSandbox` is zero/false | merged result has parent's `notification.slack.perSandbox: true` |
| 8 | `km doctor` passes | integration/UAT | — | Wave 6 | doctor check fails | all checks green |
| 9 | `make build && km init --sidecars` succeeds | integration | — | Wave 6 | build error or Lambda deploy fails | sidecars deployed, km binary updated |
| 10 | Wave 6 UAT scenarios (9 scenarios) | UAT | `92-06-UAT.md` | Wave 6 | any scenario fails | all 9 scenarios verified |
| 11 | All 20 profile YAMLs pass `km validate` via `scripts/validate-all-profiles.sh` | script | `scripts/validate-all-profiles.sh` | Wave 1 (script added), enforced every wave | any profile fails km validate | script exits 0, zero failures |

### Sampling Rate

- **Per wave merge:** `go test ./pkg/profile/... ./pkg/compiler/...`
- **Phase gate:** `go test ./...` full suite green before Wave 6 UAT

### Wave 0 Test Stubs (RED — must be created in Wave 0)

| File | Purpose | Coverage |
|------|---------|---------|
| `pkg/compiler/userdata_phase92_byte_identity_test.go` | Byte-identity golden for `profiles/learn.v2.yaml` userdata | VC-3 |
| `pkg/compiler/security_phase92_byte_identity_test.go` | Byte-identity golden for IAM HCL output | VC-4 |
| `pkg/compiler/agent_claude_golden_test.go` | Golden tests for `synthesizeClaudeSettings()` per 4+ fixtures | VC-5 |
| `pkg/compiler/agent_codex_golden_test.go` | Golden test for `synthesizeCodexConfig()` | VC-5 |
| `pkg/profile/inherit_notification_test.go` | Inheritance fix: child transcript flag inherits parent perSandbox | VC-7 |
| `pkg/profile/validate_mixed_settings_test.go` | Mixed-mode rejection: both autoApprove and inlined configFiles → error | VC-6 |

**Wave 0 gaps (must exist before Wave 1 merges):**
- Golden files for VC-3 and VC-4 — captured by running against pre-change main
- All 6 RED test stubs above
- `scripts/validate-all-profiles.sh` — added in Wave 1 per PRD, enforced every wave after

---

## Standard Stack

No new library dependencies. All work uses existing Go stdlib, `goccy/go-yaml`, and the existing JSON schema validation path.

**Tools/patterns being used:**
- `reflect.ValueOf(x).Elem().IsZero()` — existing `mergeSpecSection` pattern; new typed mergers replace this for pointer sections
- `json.Marshal` / `json.Unmarshal` — existing pattern in `mergeNotifyHookIntoSettings`; synthesizer pipeline uses same
- `go-yaml` struct tags — existing YAML decode/encode pattern; no change

---

## Architecture Patterns

### Settings.json Pipeline (Wave 5)

```
agent.claude spec (typed fields)
    ↓
synthesizeClaudeSettings()         → {permissions:{allow:[...],deny:[...]}, trustedDirectories:[...], ...passthrough}
    ↓
mergeNotifyHookIntoSettings()      → adds hooks.{Notification,Stop,PostToolUse}
    ↓
write to /home/sandbox/.claude/settings.json via configFiles userdata path
```

### Codex config.toml Pipeline (Wave 5)

```
agent.codex spec (typed fields)
    ↓
synthesizeCodexConfig()            → existing notify hook toml + args echo + inert tool fields
    ↓
write to /home/sandbox/.codex/config.toml (replaces current hardcoded heredoc)
```

### Typed Merge Pattern (Waves 2, 4)

```go
// Replace mergeSpecSection for pointer-typed notification/agent:
func mergeNotificationSpec(parent, child *NotificationSpec) *NotificationSpec {
    // field-level nil-aware merge
    // if child.Slack == nil, use parent.Slack
    // etc.
}
```

---

## Common Pitfalls

### Pitfall 1: Schema `required` array vs field removal
When removing `sessionPolicy` from `IdentitySpec`, the schema `required` array at line 401 must also be updated or `km validate` will error on profiles that don't declare it. Wave 1 must update both `properties` AND `required` simultaneously.

### Pitfall 2: `agent` in `spec.required` array
The schema has `agent` in `spec.required` (line 59). Removing the dead `agent` block requires removing it from `required` in Wave 1. After Wave 4, the new `agent:` block is optional (not in `required`).

### Pitfall 3: Legacy `autoApprove` vs `permissions.allow` migration
Existing profiles use `{"autoApprove": [...]}`. The synthesizer must emit `{"permissions": {"allow": [...]}}`. Golden tests must verify that the inlined JSON equivalent and the synthesized output produce the same effective Claude Code behavior. The PRD's "migration-equivalence" test is this check.

### Pitfall 4: `IsVSCodeEnabled()` signature update
The function `pkg/profile/types.go:560` currently takes `*CLISpec`. After Wave 2 moves `vscodeEnabled` to `RuntimeSpec.VSCode`, the function signature must change. All callers (`create.go:620`, `create.go:2060`, `vscode.go:422`) must be updated. The `vscode.go:422` error message text must also be updated to reference the new path.

### Pitfall 5: `allowlistgen/generator.go` dead field reference
`pkg/allowlistgen/generator.go:96-99` constructs `AgentSpec{MaxConcurrentTasks: 1, TaskTimeout: "30m"}`. After Wave 1 redefines `AgentSpec`, this file will not compile. Must be updated in Wave 1 (not discovered in compiler package — PRD risk callout was only about `_test.go` files, but this is non-test production code).

### Pitfall 6: Pointer-typed `Spec.CLI` in Wave 3 — all pointer semantics
After Wave 2 removes all notify fields from `CLISpec`, the only remaining field is `NoBedrock`. If the `CLISpec` struct becomes a single-field struct, consider whether it should be collapsed into a scalar or stay as a struct. The planner should decide in Wave 2.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| JSON schema validation | Custom YAML key checker | Existing `ValidateSchema()` in `pkg/profile/schema.go` | Already uses `xeipuuv/gojsonschema` |
| YAML encoding/decoding | Custom parser | `goccy/go-yaml` (existing) | Used throughout |
| JSON merge for settings.json | Custom recursive merge | Extend existing `mergeNotifyHookIntoSettings` pattern | Already handles the edge cases |

---

## Open Questions

1. **`allowedRegions: minItems: 1` — relax or keep?**
   - What we know: `security.go:56` already produces empty region list when `AllowedRegions` is empty; compiler handles it correctly
   - What's unclear: Whether any profile legitimately needs an empty region list
   - Recommendation: Wave 1 decides; relaxing to `minItems: 0` is safe given compiler support

2. **`notification.slack.invites.emails` vs `notification.slack.inviteEmails` flattening**
   - What we know: PRD current design keeps sub-block for `useConnect` co-location
   - What's unclear: Whether the added nesting is user-friendly
   - Recommendation: Keep sub-block as designed (Wave 2 decides)

3. **`CLISpec` with single remaining field**
   - What we know: After Wave 2, `CLISpec` has only `NoBedrock bool`
   - What's unclear: Should this collapse to `spec.noBedrock: bool` directly, avoiding the `cli:` wrapper?
   - Recommendation: Keep `cli.noBedrock` per locked decision; single-field struct is valid Go and maintains naming consistency

---

## Sources

### Primary (HIGH confidence)
- Direct code inspection: `pkg/profile/types.go`, `pkg/profile/inherit.go`, `pkg/compiler/security.go`, `pkg/compiler/service_hcl.go`, `pkg/compiler/userdata.go`, `pkg/profile/schemas/sandbox_profile.schema.json`
- Existing documentation: `docs/codex-parity.md:82-89` — Codex 0.133 hooks spike result
- Official Claude Code permissions docs: https://code.claude.com/docs/en/permissions
- Official Claude Code settings docs: https://code.claude.com/docs/en/settings

### Secondary (MEDIUM confidence)
- OpenAI Codex config reference: https://developers.openai.com/codex/config-reference (WebFetch)
- OpenAI Codex permissions: https://developers.openai.com/codex/permissions (WebFetch)
- Claude Code settings JSON schema: https://json.schemastore.org/claude-code-settings.json (WebSearch)

### Research Artifacts
- `.planning/research/codex-config-toml.md` — Full Wave 0 research spike output for Codex and Claude Code

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all existing Go stdlib + existing libraries, no new deps
- Architecture: HIGH — grounded in direct code inspection, all PRD claims verified or corrected with specific line numbers
- Pitfalls: HIGH — discovered from reading actual code, not speculation
- Wave 0 research spikes: HIGH — Codex finding confirmed by existing in-codebase documentation; Claude Code finding from official docs

**Research date:** 2026-05-31
**Valid until:** 2026-06-30 (stable domain, Codex and Claude Code APIs change frequently but the absence of config.toml tool gating in Codex 0.133 is documented in-codebase)
