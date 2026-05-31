# Phase 92: Profile spec restructure — Context

**Gathered:** 2026-05-30
**Status:** Ready for planning
**Source:** PRD Express Path (`.planning/todos/pending/2026-05-30-phase-92-profile-spec-restructure-CONTEXT.md`)

**User constraints:**
- Zero running sandboxes (no live-sandbox impact)
- No backwards compatibility required (no aliases, no deprecation window)
- All profile YAMLs may be rewritten in place

<domain>
## Phase Boundary

**What this phase delivers:**

A coherent SandboxProfile spec that is honest about what each section is and contains no dead fields. Four user-facing changes land together:

1. **`spec.notification:` block** owns every "send email / post to Slack / invite somebody / archive a channel" decision. The 14 `notify*` fields currently scattered through `spec.cli:` move under structured sub-blocks (`notification.events`, `notification.email`, `notification.slack.*`).
2. **`spec.identity:` → `spec.iam:`** — the section is AWS IAM (role + region-lock + SSM secret allowlist), not "identity". Rename to match reality.
3. **Dead fields removed.** `identity.sessionPolicy` (parsed, schema-required, never read). The entire `spec.agent:` top-level block (`maxConcurrentTasks`, `taskTimeout`, `allowedTools` — all three confirmed-dead) is dropped and then re-introduced with new semantics.
4. **Structured `spec.agent:` block** for Claude/Codex tool gating, with the compiler synthesizing `/home/sandbox/.claude/settings.json` and `~/.codex/config.toml` from typed fields. Eliminates the current antipattern of inlining raw JSON in `spec.execution.configFiles`.

Plus three correctness fixes that fall out of the work:
- **Inheritance bug fix.** `pkg/profile/inherit.go` does not merge pointer-typed sections (`Spec.CLI`, `Spec.Email`, `Spec.Budget`, ...) — a child profile that sets one notify field today silently loses its parent's other notify settings. A typed `mergeNotificationSpec` (and `mergeAgentSpec`) replaces the reflection-based zero-replace pattern for these sections.
- **Schema drift fix.** `identity.allowedSecretPaths` (Phase 89 SOPS work) is read by the compiler but missing from the JSON schema. The new `iam:` block declares it.
- **`vscodeEnabled` relocated** from `spec.cli:` → `spec.runtime.vscode.enabled`. It gates a userdata sshd block — provisioning, not CLI-side defaulting.

**Schema before → after (combined):**

```yaml
# BEFORE
spec:
  identity:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
    sessionPolicy: minimal                  # DEAD
    allowedSecretPaths: [/foo/bar]          # missing from schema
  agent:                                     # entire block dead
    maxConcurrentTasks: 1
    taskTimeout: "30m"
    allowedTools: [bash, read_file]
  cli:
    notifyOnPermission: false
    notifyOnIdle: false
    notifyCooldownSeconds: 0
    notificationEmailAddress: ""
    notifyEmailEnabled: true
    notifySlackEnabled: true
    notifySlackPerSandbox: true
    notifySlackChannelOverride: ""
    notifySlackInboundEnabled: true
    notifySlackInboundMentionOnly: true
    notifySlackTranscriptEnabled: false
    notifySlackInviteEmails: [whereiskurt@gmail.com]
    slackArchiveOnDestroy: false
    useSlackConnect: true
    vscodeEnabled: true                     # provisioning, not CLI
    noBedrock: true
    agent: claude
    claudeArgs: ["--dangerously-skip-permissions"]
    codexArgs: []
  execution:
    configFiles:
      "/home/sandbox/.claude/settings.json": |
        {"trustedDirectories":["/home/sandbox","/workspace"],"autoApprove":["Bash","Read","Write","Edit","Glob","Grep","WebFetch","WebSearch","NotebookEdit"]}

# AFTER
spec:
  iam:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
    allowedSecretPaths: [/foo/bar]
  runtime:
    vscode:
      enabled: true
  cli:
    noBedrock: true                         # only field that survives
  notification:
    events:
      onPermission: false
      onIdle: false
      cooldownSeconds: 0
    email:
      enabled: true
      address: ""
    slack:
      enabled: true
      perSandbox: true
      channelOverride: ""
      archiveOnDestroy: false
      inbound:
        enabled: true
        mentionOnly: true                   # tri-state *bool preserved
      transcript:
        enabled: false
      invites:
        emails: [whereiskurt@gmail.com]
        useConnect: true
  agent:
    default: claude
    claude:
      trustedDirectories: [/home/sandbox, /workspace]
      tools:
        autoApprove: [Bash, Read, Write, Edit, Glob, Grep, WebFetch, WebSearch, NotebookEdit]
        deny: []
      permissions: {}                       # passthrough map[string]any
      args: ["--dangerously-skip-permissions"]
    codex:
      tools:
        autoApprove: []
        deny: []
      args: []
  execution:
    # inlined settings.json string GONE — synthesized from agent.claude.*
    configFiles: {}
```

## Profile Inventory

Every SandboxProfile YAML in the repo MUST validate against the new schema by end-of-phase. Each file is rewritten in the wave that owns the surface it touches (Wave 1 for IAM rename + dead-block removal, Wave 3 for notification block, Wave 5 for agent block + inlined-JSON removal). After Wave 5 closes, all 20 files are on the new schema.

**Operator-facing fixtures (`profiles/`) — 12 files:**

- `ao.yaml`
- `codex.yaml`
- `dc34.yaml`, `dc34.ami.yaml`
- `example-additional-snapshots.yaml`
- `goose.yaml`
- `learn.v2.yaml`, `learn.v2.chatty.yaml`, `learn.v2.codex.yaml`, `learn.v2.polite.yaml`
- `locked.yaml`, `locked.ami.yaml`

**Built-ins (`pkg/profile/builtins/`) — 8 files:**

- `ao.yaml`, `codex.yaml`, `goose.yaml`, `hardened.yaml`, `learn.yaml`, `open-dev.yaml`, `restricted-dev.yaml`, `sealed.yaml`

**Total: 20 profile YAMLs that MUST `km validate` cleanly at end of phase.**

**Pre-phase cleanup already done (2026-05-30, prior to phase planning):**

- `profiles/uat/87/uat-{1..8}.yaml` and the `profiles/uat/` tree — deleted (Phase 87 UAT scratch fixtures, no longer needed).
- `profiles/hermes.open.yaml` — deleted.
- `profiles/secrets/codex.enc.yaml` — deleted (was a SOPS bundle, out of scope anyway).

The phase inherits this clean baseline; planner does not need to re-do the deletions.

**Explicitly out of scope (NOT SandboxProfile documents — skip validation):**

- `profiles/secrets/EXAMPLE.yaml` — SOPS-encrypted secret bundle example (Phase 89).
- `pkg/profile/testdata/secrets-fixture.enc.yaml` — Phase 89 test fixture.

**Automation:** Wave 1 adds `scripts/validate-all-profiles.sh` (or a `make validate-profiles` target — planner picks) that iterates the inventory list, calls `km validate <file>` for each, and exits non-zero on any failure. The script is wired into:

1. Each implementation wave's local verification loop (catch regressions wave-by-wave instead of at Wave 6).
2. The Wave 6 UAT checklist (operator-facing confirmation).
3. CI if a CI hook exists for this repo (the planner should check `.github/workflows/` or equivalent and add the gate there if appropriate).

The inventory list lives in the script itself so the script is the single source of truth — if a new profile is added in a later phase, it must be added to the script.

**Per-wave fixture treatment:**

| File | Wave 1 (iam) | Wave 3 (notification) | Wave 5 (agent) |
|---|---|---|---|
| All 12 `profiles/*.yaml` | `identity:` → `iam:`, drop `sessionPolicy`, drop dead `agent:` block | Move `notify*` → `notification.*`, move `vscodeEnabled` → `runtime.vscode.enabled` | Move `cli.{agent,claudeArgs,codexArgs}` → `agent.*`, remove inlined `configFiles[".claude/settings.json"]`, populate `agent.claude.tools.*` |
| All 8 `pkg/profile/builtins/*.yaml` | same | same | same |

**Special-case files to flag for the planner:**

- `profiles/example-additional-snapshots.yaml` — primarily a documentation example for Phase 87. Snapshot IDs may be placeholders; must still pass syntactic + schema validation. Wave 1 confirms `km validate` accepts placeholder snapshot IDs OR replaces them with `snap-aaaaaaaaaaaaaaaaa` style synthetic values that satisfy any pattern check.
- `pkg/profile/builtins/sealed.yaml`, `hardened.yaml`, `restricted-dev.yaml`, `open-dev.yaml` — these are the high-trust built-ins shipped with the binary. Get the most scrutiny in Wave 6 UAT: not only `km validate` but also a `km create --dry-run` (if such a flag exists) or a real `km create` smoke test against each.
- `profiles/learn.v2.yaml` is the byte-identity baseline for Wave 0/3/5 golden tests — it must remain the same EFFECTIVE userdata after rewrite, not just schema-valid.

## Wave Breakdown

```
Wave 0  → Test scaffolding + research spikes
Wave 1  → Structural cleanup (iam rename, dead-field removal, schema drift fix)
Wave 2  → Notification: types + schema + validator + inherit fix
Wave 3  → Notification: compiler emission + CLI cmd rewrites + fixtures + docs
Wave 4  → Agent block: types + schema + inherit + mixed-mode validator
Wave 5  → Agent: synthesizers (Claude settings.json + Codex config.toml) + fixture rewrite + docs
Wave 6  → Operator UAT checkpoint (not autonomous)
```

**Wave dependency graph:**
- Wave 0 has no deps (research + RED test stubs).
- Wave 1 is orthogonal — can run parallel with Wave 0.
- Wave 2 depends on Wave 0 (RED tests).
- Wave 3 depends on Wave 2.
- Wave 4 depends on Wave 1 (agent slot vacated) and Wave 0 (RED tests).
- Wave 5 depends on Wave 4 and Wave 0 (Codex research).
- Wave 6 depends on Waves 1–5 all merged.

### Wave 0 — Test scaffolding + research spikes

**Research spikes:**
- Codex CLI 0.133 config.toml schema: does it honor tool allow/deny? Where are the keys documented? Output: `.planning/research/codex-config-toml.md`.
- Claude Code 2.1.132 settings.json deny canonical: top-level `disallowedTools` vs `permissions.deny`? Output appended to the same research file.

**Test stub seeding:**
- `pkg/compiler/userdata_phase92_byte_identity_test.go` (RED) — golden test that compiled userdata for `profiles/learn.v2.yaml` matches a baseline captured from pre-phase main.
- `pkg/compiler/security_phase92_byte_identity_test.go` (RED) — golden test for `aws_iam_role.max_session_duration` + `aws_iam_role_policy.ec2spot_region_lock`.
- `pkg/compiler/agent_claude_golden_test.go` (RED) — golden tests for `synthesizeClaudeSettings(...)` per representative fixture.
- `pkg/compiler/agent_codex_golden_test.go` (RED) — golden test for `synthesizeCodexConfig(...)`.
- `pkg/profile/inherit_notification_test.go` (RED) — child setting only `notification.slack.transcript.enabled` inherits parent's `notification.slack.perSandbox`.
- `pkg/profile/validate_mixed_settings_test.go` (RED) — profile with both `agent.claude.tools.autoApprove` and `execution.configFiles[".claude/settings.json"]` fails validation.

### Wave 1 — Structural cleanup

- `pkg/profile/types.go` — rename `IdentitySpec` → `IAMSpec`, `Spec.Identity` field → `Spec.IAM`; drop `IdentitySpec.SessionPolicy`; drop `AgentSpec` type + `Spec.Agent` field.
- `pkg/profile/schemas/sandbox_profile.schema.json` — rename `identity` block → `iam`; drop `sessionPolicy` property + required entry; **add** `allowedSecretPaths` to `iam` (drift fix); remove `agent` from `spec.required` + drop `agent` definition.
- `pkg/profile/inherit.go` — line 101: `&result.Spec.Identity` → `&result.Spec.IAM`; line 104: delete `Spec.Agent` merge call.
- `pkg/profile/validate.go`, `aws_validate.go` — field-path renames for IAM; remove `SessionPolicy` references.
- `pkg/compiler/security.go` + `service_hcl.go` — `p.Spec.Identity` → `p.Spec.IAM` (3 sites).
- All `profiles/*.yaml` + `pkg/profile/builtins/*` + `pkg/profile/testdata/*` — `identity:` → `iam:`, drop `sessionPolicy:`, drop dead `agent:` block.
- `docs/sandbox-secrets.md`, `CLAUDE.md`, `OPERATOR-GUIDE.md` — Phase 89 reference paths updated.
- **Goal:** Wave 1's byte-identity test for `security.go` output stays green (IAM emission unchanged).

### Wave 2 — Notification block (types + schema + validator + inherit)

- `pkg/profile/types.go` — new `NotificationSpec` + sub-types (`NotificationEventsSpec`, `NotificationEmailSpec`, `NotificationSlackSpec`, `NotificationSlackInboundSpec`, `NotificationSlackTranscriptSpec`, `NotificationSlackInvitesSpec`); new `RuntimeVSCodeSpec`. Remove all `notify*` fields + `VSCodeEnabled` from `CLISpec`.
- `pkg/profile/schemas/sandbox_profile.schema.json` — add `notification` definition + sub-schemas; remove migrated `cli.*` properties; add `runtime.vscode.enabled`.
- `pkg/profile/inherit.go` — add typed `mergeNotificationSpec(parent, child *NotificationSpec) *NotificationSpec` with field-level nil-aware merge.
- `pkg/profile/validate.go` + the slack/transcript/invite validator suites — semantic validators rewired to `notification.slack.*` paths.

### Wave 3 — Notification: compiler + CLI cmd + fixtures + docs

- `pkg/compiler/userdata.go` — ~30 emission sites for `KM_NOTIFY_*` env vars now read from `Spec.Notification.*`. `KM_SLACK_MENTION_ONLY`, `KM_NOTIFY_SLACK_CHANNEL_ID`, the lot.
- `internal/app/cmd/create_slack.go` — `resolveSlackChannel` reads `notification.slack.channelOverride` / `perSandbox` / `invites.*`.
- `internal/app/cmd/create_slack_inbound.go` — inbound provisioning reads `notification.slack.inbound.*`.
- `internal/app/cmd/create.go` — Slack provisioning gating + transcript wiring.
- `internal/app/cmd/agent.go` + `shell.go` — per-invoke `--notify-on-permission` / `--notify-on-idle` / `--notify-slack-transcript-enabled` override flags.
- `internal/app/cmd/destroy_slack.go` — `archiveOnDestroy` read path.
- `internal/app/cmd/doctor.go` — `checkSlackBotUserIDCached` reads `notification.slack.inbound.mentionOnly`.
- All `profiles/*.yaml` — move `notify*` under `notification:`, move `vscodeEnabled` under `runtime.vscode.enabled`.
- All `pkg/profile/builtins/*` updated.
- `docs/slack-notifications.md`, `CLAUDE.md`, `OPERATOR-GUIDE.md` — `notifySlackX` → `notification.slack.X` sweep.
- **Goal:** Wave 0's userdata byte-identity test goes GREEN.

### Wave 4 — Agent block (types + schema + inherit + mixed-mode validator)

- `pkg/profile/types.go` — new `AgentSpec` (`Default string`, `Claude *AgentClaudeSpec`, `Codex *AgentCodexSpec`); new `AgentClaudeSpec` (`TrustedDirectories []string`, `Tools AgentToolsSpec`, `Permissions map[string]any`, `Args []string`); new `AgentCodexSpec` (`Tools AgentToolsSpec`, `Args []string`); new `AgentToolsSpec` (`AutoApprove []string`, `Deny []string`). Delete `CLISpec.Agent`, `CLISpec.ClaudeArgs`, `CLISpec.CodexArgs`.
- `pkg/profile/schemas/sandbox_profile.schema.json` — add `agent` definition (NOT required); `agent.claude.permissions` uses `additionalProperties: true` (the one passthrough exception). Remove `cli.agent`, `cli.claudeArgs`, `cli.codexArgs`.
- `pkg/profile/inherit.go` — add typed `mergeAgentSpec`.
- `pkg/profile/validate.go` — new validator: profile with both `agent.claude.tools.autoApprove` (non-empty) AND `execution.configFiles[".claude/settings.json"]` errors. No merge fallback.
- `internal/app/cmd/create.go`, `agent.go`, `shell.go` — `--claude` / `--codex` flag handling reads `Spec.Agent.Default`; args read from `Spec.Agent.Claude.Args` / `Spec.Agent.Codex.Args`.
- Slack inbound dispatcher per-message prefix routing reads `Spec.Agent.Default`.
- **Wave 4 does NOT yet ship the synthesizer.** Profile fixtures still inline `configFiles[".claude/settings.json"]` until Wave 5.

### Wave 5 — Synthesizers + agent fixture rewrite + docs

- **New file `pkg/compiler/agent_claude.go`** — `synthesizeClaudeSettings(agent *profile.AgentSpec) (map[string]any, error)` builds settings.json from typed fields.
- **New file `pkg/compiler/agent_codex.go`** — `synthesizeCodexConfig(agent *profile.AgentSpec) (string, error)` builds config.toml from typed fields. If Wave 0 research showed Codex has no native allow/deny, this emits the existing inert config + args echo + a logged note (per the locked decision below).
- `pkg/compiler/userdata.go` — `mergeNotifyHookIntoSettings` becomes step 2 of a pipeline: (1) `synthesizeClaudeSettings(profile.Spec.Agent.Claude)`, (2) merge `km-notify-hook` entries, (3) write to `/home/sandbox/.claude/settings.json` via configFiles userdata path. Similar pipeline for Codex.
- All `profiles/*.yaml` — remove inlined `configFiles[".claude/settings.json"]` strings; populate `agent.claude.tools.autoApprove` / `trustedDirectories` / `permissions` with the same effective values; move `cli.agent` → `agent.default`; move `cli.claudeArgs` → `agent.claude.args`; move `cli.codexArgs` → `agent.codex.args`.
- `pkg/profile/builtins/*` updated.
- **New file `docs/agent-tool-gating.md`** — describes the `agent:` block, claude/codex symmetry, synthesis, deny semantics, no-merge-with-configFiles rule.
- `docs/codex-parity.md`, `CLAUDE.md` — remove "inert ~/.codex/config.toml" note; document synthesizer.
- **Goal:** Wave 0's synthesizer golden tests + migration-equivalence test go GREEN.

### Wave 6 — Operator UAT checkpoint (not autonomous)

Operator runs end-to-end smoke tests against representative fixtures:

0. **Full-inventory validation:** `scripts/validate-all-profiles.sh` (or `make validate-profiles`) iterates the 20-file Profile Inventory and runs `km validate` against each. Zero failures, zero warnings. This is the phase's hard exit gate — any failure blocks UAT close.
1. `km validate profiles/learn.v2.yaml` and all others — pass (covered by step 0; called out separately for narrative completeness).
2. `km validate` rejects a fixture with stale `identity:` / `agent: {maxConcurrentTasks: ...}` / `cli: {notifySlackEnabled: ...}` keys with clear errors.
3. `make build && km init --sidecars` succeeds.
4. `km create profiles/learn.v2.yaml --no-bedrock` provisions; SSM-shell in; `cat /home/sandbox/.claude/settings.json` matches expectations; `cat /home/sandbox/.codex/config.toml` validates.
5. Claude Code task attempts a denied tool (e.g. profile with `agent.claude.tools.deny: [WebFetch]`); Claude refuses.
6. Slack notify-hook fires for an idle event in a `notification.slack.perSandbox: true` profile.
7. Slack inbound `mentionOnly: true` in shared channel works as in Phase 91 (mention required); `mentionOnly: false` in per-sandbox channel accepts every message.
8. Per-message `codex:` prefix in a `agent.default: claude` profile triggers Codex (Phase 70 routing intact).
9. `km doctor` passes.

</domain>

<decisions>
## Implementation Decisions (locked)

### Backwards compatibility
- **No backwards compatibility.** YAML decoder rejects legacy keys (`identity:`, `cli.notify*`, `cli.vscodeEnabled`, `cli.agent`, dead `agent:` shape). `additionalProperties: false` schema-side. Profile fixtures rewritten atomically in the owning wave.

### IAM block (rename + drift fix)
- **`iam:` not `aws:`.** Section is specifically IAM; AWS-scoped naming over-broad.
- **`sessionPolicy` deleted without replacement.** Re-introduce later if tiered policies become real.
- **`allowedSecretPaths` lives under `iam:`** (not under `secrets:`). `iam.allowedSecretPaths` is the IAM SSM allowlist; `secrets.sopsFile` is the SOPS bundle pointer — different mechanisms.

### VS Code relocation
- **`vscodeEnabled` moves to `runtime.vscode.enabled`.** Tri-state `*bool` preserved; `IsVSCodeEnabled()` helper updated.

### Notification block
- **`notification:` is optional.** Sub-blocks optional. Booleans default false. Absent block produces same userdata as today's absent CLI notify fields.
- **Notify env var names unchanged.** Sandbox-side `KM_NOTIFY_ON_PERMISSION`, `KM_NOTIFY_SLACK_ENABLED`, `KM_SLACK_MENTION_ONLY`, etc. keep current names. Relocation is YAML-side only.

### Agent block
- **`agent:` is optional.** Absent → default `claude`, no tool gating beyond Claude Code defaults, no Codex config.
- **`permissions:` is `map[string]any` passthrough.** Type the high-value Claude settings.json keys (`tools.autoApprove`, `tools.deny`, `trustedDirectories`); passthrough the rest to avoid schema bumps every Claude Code release.
- **Validation forbids mixed mode.** `agent.claude.tools.autoApprove` populated AND `execution.configFiles[".claude/settings.json"]` present → hard validation error. No merge fallback.
- **Notify-hook merge layer preserved.** Synthesizer → notify-hook merge → write. Order tested in both directions.

### CLI block
- **`cli.noBedrock` survives** as the lone `cli:` field. It is genuinely operator-CLI default behavior, not sandbox-provisioning.

### Codex synthesizer
- **Codex synthesizer is research-gated.** If Codex 0.133 has no native tool allow/deny in config.toml, Wave 5 emits the existing inert config + an `args:` echo for completeness and `docs/agent-tool-gating.md` documents the asymmetry plus a future-Codex-release TODO. Honest > performative symmetry.

### Inheritance
- **Inherit fix is typed, not reflection-extended.** Two new typed mergers: `mergeNotificationSpec` (Wave 2) and `mergeAgentSpec` (Wave 4). Reflection-based `mergeSpecSection` keeps working for value-type sections.

### Claude's Discretion

Areas not covered by PRD — planner picks:
- Exact `scripts/validate-all-profiles.sh` vs `make validate-profiles` target name.
- Whether `additionalSnapshots` placeholder snapshot IDs need synthetic values vs schema relaxation (Wave 1 decides).
- Whether `notification.slack.invites.emails` flattens to `notification.slack.inviteEmails` (Wave 2 decides — current design keeps sub-block).
- Whether `notification.events.{onIdle,onPermission}` become a list `notify: [idle, permission]` (Wave 2 decides — current design keeps booleans).
- Whether `roleSessionDuration` becomes `int` seconds (deferred — orthogonal).
- Whether `allowedRegions: minItems: 1` relaxes to allow empty list (Wave 1 decides).
- Specific test fixture choice for `agent_claude_golden_test.go` beyond learn.v2/dc34/locked/codex.
- Whether `pkg/profile/configfiles/*` gets retired vs trimmed.

</decisions>

<specifics>
## Components Touched (summary)

| File / Group | Wave | Approx scope |
|---|---|---|
| `pkg/profile/types.go` | 1, 2, 4 | Renames + new types + deletions |
| `pkg/profile/schemas/sandbox_profile.schema.json` | 1, 2, 4 | Full restructure of multiple blocks |
| `pkg/profile/inherit.go` | 1, 2, 4 | Field renames + 2 new typed mergers |
| `pkg/profile/validate.go` + slack/transcript validators | 1, 2, 4 | Path renames + mixed-mode validator |
| `pkg/profile/aws_validate.go` | 1 | IAM field-path rename |
| `pkg/compiler/security.go` | 1 | IAM field-path rename (3 sites) |
| `pkg/compiler/service_hcl.go` | 1 | IAM serialization rename |
| `pkg/compiler/userdata.go` (4211 lines) | 3, 5 | ~30 notification sites + synthesizer integration |
| `pkg/compiler/agent_claude.go` (new) | 5 | ~80 lines |
| `pkg/compiler/agent_codex.go` (new) | 5 | ~70 lines |
| `internal/app/cmd/create_slack*.go` | 3 | resolveSlackChannel + inbound provisioning |
| `internal/app/cmd/create.go`, `agent.go`, `shell.go` | 3, 4 | Per-invoke override flags + agent-default reads |
| `internal/app/cmd/destroy_slack.go` | 3 | archiveOnDestroy read |
| `internal/app/cmd/doctor.go` | 3 | mentionOnly cached-bot-id check |
| `profiles/*.yaml` (~12 files) | 1, 3, 5 | Rewritten per wave |
| `pkg/profile/builtins/*` | 1, 3, 5 | Rewritten per wave |
| `pkg/profile/testdata/*` | 1, 2, 4 | Rewritten per wave |
| `pkg/profile/configfiles/*` | 5 | Possibly retired |
| `scripts/validate-all-profiles.sh` (new) | 1 | ~30 lines; iterates 20-file Profile Inventory |
| `CLAUDE.md`, `OPERATOR-GUIDE.md` | 1, 3, 5 | Sweep per wave |
| `docs/slack-notifications.md` | 3 | `notifySlackX` → `notification.slack.X` sweep |
| `docs/sandbox-secrets.md` | 1 | `identity.allowedSecretPaths` → `iam.allowedSecretPaths` |
| `docs/codex-parity.md` | 5 | Remove "inert config.toml" note |
| `docs/agent-tool-gating.md` (new) | 5 | Full doc, ~200 lines |
| `.planning/research/codex-config-toml.md` (new) | 0 | Research spike output |

## Verification Criteria (phase-level)

1. `go test ./...` passes across all waves.
2. `km validate` passes on every fixture; rejects stale-key fixtures with clear errors.
3. **Userdata byte-identity** for `profiles/learn.v2.yaml` between pre-phase main and post-Wave-5 (the contract: full pipeline is semantically transparent).
4. **IAM Terraform byte-identity** post-Wave-1.
5. **Synthesizer golden tests** GREEN for every representative fixture (learn.v2, dc34, locked, codex).
6. **Mixed-mode rejection** test GREEN (Wave 4).
7. **Inheritance fix** test GREEN: child-only-transcript-flag inherits parent perSandbox (Wave 2).
8. `km doctor` passes.
9. `make build && km init --sidecars` succeeds.
10. Wave 6 UAT scenarios all pass.
11. **All 20 profile YAMLs in the Profile Inventory pass `km validate`** — enforced by `scripts/validate-all-profiles.sh` (added in Wave 1, run as part of every wave's local verification loop, gates Wave 6 close).

## Risks

| Risk | Mitigation |
|---|---|
| `userdata.go` (4211 lines, ~30 notify sites + synthesizer integration) is the largest blast radius. | Byte-identity golden test from Wave 0 catches any miss. |
| IAM rename touches security-critical code paths. | Wave 1 byte-identity test for `security.go` IAM HCL output. |
| Synthesizer correctness is security-critical — wrong `autoApprove` = sandbox-escape-equivalent. | Per-fixture synthesizer golden tests + migration-equivalence test against pre-phase inlined JSON. |
| Codex tool-gating asymmetry if Codex 0.133 lacks native support. | Wave 0 research dictates Wave 5 scope; honest doc + future-version TODO. |
| `permissions:` `map[string]any` passthrough weakens "no inlined JSON" win. | Type aggressively; passthrough only as last resort. Document well-known keys. |
| Doc drift across CLAUDE.md / OPERATOR-GUIDE.md / docs/slack-notifications.md / docs/codex-parity.md. | Sweep checklist per wave; grep-based pre-merge verification. |
| `pkg/profile/inherit.go` line-104 deletion may leave dangling tests asserting `Spec.Agent.MaxConcurrentTasks`. | Pre-sweep `*_test.go` for `Spec.Agent.` before Wave 1 merges. |

## Sizing

- **~730 line edits + ~150 lines new synthesizer code across ~60 files.**
- **6 implementation waves + 1 UAT checkpoint** = 7 plan files.
- Largest single new code: `pkg/compiler/agent_claude.go` synthesizer (~80 lines).
- Largest single file touched: `pkg/compiler/userdata.go`.
- Highest-uncertainty work: Wave 5 (synthesizers + Codex schema research).

## Post-merge runbook

- `make build && km init --sidecars` (refreshes management Lambdas' bundled km binary so remote create parses the new schema).
- No sandbox cycle (none exist).
- Smoke test against a representative fixture per Wave 6 UAT criteria.

</specifics>

<deferred>
## Deferred Ideas

Open Questions (some resolve in Wave 0 research, others deferred to later phases):

1. Does Codex CLI 0.133 honor tool allow/deny in `~/.codex/config.toml`? Where documented? — **Wave 0 research spike**.
2. Claude Code 2.1.132 deny-list canonical location: top-level `disallowedTools` vs `permissions.deny`? — **Wave 0 research spike**.
3. Should `notification.slack.invites.emails` flatten to `notification.slack.inviteEmails`? — **Wave 2 decides**; current design keeps sub-block for `useConnect` co-location.
4. Should `notification.events.onIdle` / `onPermission` become a list (`notify: [idle, permission]`)? — **Wave 2 decides**; current design keeps booleans for diff minimalism.
5. Should `roleSessionDuration` become `int` seconds vs the string pattern `"^[0-9]+(s|m|h)$"`? — **Deferred** (orthogonal to this phase).
6. `allowedRegions: minItems: 1` — relax to allow empty list (which the compiler already supports per `security.go:56`)? — **Wave 1 decides**.

</deferred>

---

*Phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating*
*Context gathered: 2026-05-30 via PRD Express Path*
