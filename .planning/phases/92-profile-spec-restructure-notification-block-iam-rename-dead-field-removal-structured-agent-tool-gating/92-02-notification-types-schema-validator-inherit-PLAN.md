---
phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating
plan: 2
type: execute
wave: 2
depends_on: [0]
files_modified:
  - pkg/profile/types.go
  - pkg/profile/schemas/sandbox_profile.schema.json
  - pkg/profile/inherit.go
  - pkg/profile/validate.go
  - pkg/profile/slack_validate.go
  - pkg/profile/transcript_validate.go
  - pkg/profile/invite_validate.go
  - pkg/profile/inherit_notification_test.go
  - pkg/profile/inherit_test.go
autonomous: true
requirements: []
verifies: [VC-1, VC-7]

must_haves:
  truths:
    - "`spec.notification:` block exists in types + schema, with all sub-blocks (events, email, slack, slack.inbound, slack.transcript, slack.invites)."
    - "11 `Notify*` fields + `SlackArchiveOnDestroy` + `UseSlackConnect` + `VSCodeEnabled` removed from CLISpec (14 fields total this wave; 4 more — Agent/ClaudeArgs/CodexArgs/NotificationEmailAddress — removed by Wave 4 + already counted)."
    - "`runtime.vscode.enabled` exists in RuntimeSpec + schema; `IsVSCodeEnabled` signature updated to accept the new shape."
    - "Typed `mergeNotificationSpec(parent, child *NotificationSpec) *NotificationSpec` handles field-level nil-aware merge for pointer-typed sections — the pointer-merge inheritance bug fix."
    - "All Slack/transcript/invite semantic validators read from `notification.slack.*` paths (not `cli.notifySlack*`)."
    - "Wave 0's `TestInheritNotificationSpec_ChildOnlyTranscriptInheritsParentPerSandbox` (VC-7) is GREEN."
    - "Wave 0's mixed-mode test (VC-6) is NOT yet GREEN — Wave 4 wires that validator after agent block lands."
    - "Profile YAMLs are NOT rewritten in this wave — Wave 3 carries the YAML rewrite. After Wave 2 land, `km validate` on existing (post-Wave-1) YAMLs will FAIL because they still have `cli.notify*` keys — that's expected; Wave 3 fixes."
  artifacts:
    - path: "pkg/profile/types.go"
      provides: "NotificationSpec + 6 sub-types; RuntimeVSCodeSpec; CLISpec stripped of 14 fields."
      contains: "type NotificationSpec struct"
    - path: "pkg/profile/schemas/sandbox_profile.schema.json"
      provides: "notification definition + runtime.vscode.enabled; 14 cli properties removed."
      contains: "\"notification\":"
    - path: "pkg/profile/inherit.go"
      provides: "Typed mergeNotificationSpec for pointer-merge bug fix."
      contains: "mergeNotificationSpec"
    - path: "pkg/profile/slack_validate.go"
      provides: "Slack channel-ID + per-sandbox semantic validators rewired to notification.slack.*."
      pattern: "Notification\\.Slack"
  key_links:
    - from: "pkg/profile/inherit.go"
      to: "pkg/profile/types.go"
      via: "mergeNotificationSpec consumes NotificationSpec struct"
      pattern: "NotificationSpec"
    - from: "pkg/profile/schemas/sandbox_profile.schema.json"
      to: "pkg/profile/types.go"
      via: "JSON schema notification block mirrors NotificationSpec struct shape"
      pattern: "\"notification\""
    - from: "pkg/profile/inherit_notification_test.go"
      to: "pkg/profile/inherit.go (mergeNotificationSpec)"
      via: "Wave 0 RED test removes build tag and runs against mergeNotificationSpec"
      pattern: "mergeNotificationSpec"
---

<objective>
Land the new `spec.notification:` block in types + schema + inheritance + semantic validators. This wave is "make the new shape exist"; Wave 3 does "make the compiler + CLI cmd + fixtures + docs consume it."

Critical separation: Wave 2 does NOT rewrite any YAML and does NOT touch `pkg/compiler/userdata.go` or any `internal/app/cmd/` file. That means after Wave 2 merges, `km validate` against the existing (post-Wave-1) profile YAMLs WILL FAIL — they still have `cli.notify*` keys, and the schema no longer accepts those keys. Wave 3 immediately closes this gap. Orchestrator MUST chain Wave 3 right after Wave 2 — DO NOT release Wave 2 to users between waves.

Per RESEARCH.md §2a CLISpec accounting:
- Wave 2 removes 14 fields: NotifyOnPermission, NotifyOnIdle, NotifyCooldownSeconds, NotificationEmailAddress, NotifyEmailEnabled, NotifySlackEnabled, NotifySlackPerSandbox, NotifySlackChannelOverride, SlackArchiveOnDestroy, NotifySlackInboundEnabled, NotifySlackTranscriptEnabled, NotifySlackInviteEmails, UseSlackConnect, VSCodeEnabled + NotifySlackInboundMentionOnly = 15 fields.
- That leaves CLISpec with: NoBedrock, Agent, ClaudeArgs, CodexArgs (4 fields). Wave 4 removes the last 3 (agent-related). Only NoBedrock survives — locked decision.

Purpose: Add the new shape; fix the pointer-merge inheritance bug; keep behavior the same until Wave 3 wires it up.
Output: 4 types files + 1 schema file + 1 inheritance file + 3 validator files (slack/transcript/invite) + Wave 0 RED stub turning GREEN.
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@.planning/ROADMAP.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-CONTEXT.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-RESEARCH.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-VALIDATION.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-00-test-scaffolding-research-spikes-PLAN.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-01-structural-cleanup-iam-rename-dead-field-removal-PLAN.md
@pkg/profile/types.go
@pkg/profile/inherit.go
@pkg/profile/schemas/sandbox_profile.schema.json
@pkg/profile/inherit_notification_test.go

<interfaces>
<!-- Target types for Wave 2 (per CONTEXT.md §Phase Boundary "AFTER" block): -->

```go
type NotificationSpec struct {
    Events *NotificationEventsSpec `json:"events,omitempty" yaml:"events,omitempty"`
    Email  *NotificationEmailSpec  `json:"email,omitempty"  yaml:"email,omitempty"`
    Slack  *NotificationSlackSpec  `json:"slack,omitempty"  yaml:"slack,omitempty"`
}

type NotificationEventsSpec struct {
    OnPermission     *bool `json:"onPermission,omitempty"     yaml:"onPermission,omitempty"`
    OnIdle           *bool `json:"onIdle,omitempty"           yaml:"onIdle,omitempty"`
    CooldownSeconds  *int  `json:"cooldownSeconds,omitempty"  yaml:"cooldownSeconds,omitempty"`
}

type NotificationEmailSpec struct {
    Enabled *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty"`
    Address string `json:"address,omitempty" yaml:"address,omitempty"`
}

type NotificationSlackSpec struct {
    Enabled          *bool                            `json:"enabled,omitempty"          yaml:"enabled,omitempty"`
    PerSandbox       *bool                            `json:"perSandbox,omitempty"       yaml:"perSandbox,omitempty"`
    ChannelOverride  string                           `json:"channelOverride,omitempty"  yaml:"channelOverride,omitempty"`
    ArchiveOnDestroy *bool                            `json:"archiveOnDestroy,omitempty" yaml:"archiveOnDestroy,omitempty"`
    Inbound          *NotificationSlackInboundSpec    `json:"inbound,omitempty"          yaml:"inbound,omitempty"`
    Transcript       *NotificationSlackTranscriptSpec `json:"transcript,omitempty"       yaml:"transcript,omitempty"`
    Invites          *NotificationSlackInvitesSpec    `json:"invites,omitempty"          yaml:"invites,omitempty"`
}

type NotificationSlackInboundSpec struct {
    Enabled     *bool `json:"enabled,omitempty"     yaml:"enabled,omitempty"`
    MentionOnly *bool `json:"mentionOnly,omitempty" yaml:"mentionOnly,omitempty"`
}

type NotificationSlackTranscriptSpec struct {
    Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}

type NotificationSlackInvitesSpec struct {
    Emails     []string `json:"emails,omitempty"     yaml:"emails,omitempty"`
    UseConnect *bool    `json:"useConnect,omitempty" yaml:"useConnect,omitempty"`
}

type RuntimeVSCodeSpec struct {
    Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}

// New field on Spec:
//   Notification *NotificationSpec
// New field on RuntimeSpec:
//   VSCode *RuntimeVSCodeSpec
//
// Removed from CLISpec (14 fields — locked decisions resolve "discretion" items as: keep sub-blocks, keep booleans):
//   NotifyOnPermission, NotifyOnIdle, NotifyCooldownSeconds, NotificationEmailAddress,
//   NotifyEmailEnabled, NotifySlackEnabled, NotifySlackPerSandbox, NotifySlackChannelOverride,
//   SlackArchiveOnDestroy, NotifySlackInboundEnabled, NotifySlackInboundMentionOnly,
//   NotifySlackTranscriptEnabled, NotifySlackInviteEmails, UseSlackConnect, VSCodeEnabled.
// Surviving CLISpec fields after Wave 2: NoBedrock, Agent, ClaudeArgs, CodexArgs (Wave 4 removes the last 3).
```

```go
// pkg/profile/inherit.go — new typed merger
func mergeNotificationSpec(parent, child *NotificationSpec) *NotificationSpec {
    if parent == nil { return child }
    if child  == nil { return parent }
    out := &NotificationSpec{}
    out.Events = mergeNotificationEventsSpec(parent.Events, child.Events)
    out.Email  = mergeNotificationEmailSpec(parent.Email,  child.Email)
    out.Slack  = mergeNotificationSlackSpec(parent.Slack,  child.Slack)
    return out
}
// Each sub-merger: field-level nil-aware (child wins iff non-nil).
```
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Add NotificationSpec + RuntimeVSCodeSpec types; strip 14+1 CLISpec fields; update schema</name>
  <files>
    pkg/profile/types.go,
    pkg/profile/schemas/sandbox_profile.schema.json
  </files>
  <behavior>
    - After this task: `go build ./...` does NOT yet succeed — `pkg/compiler/userdata.go` still reads removed CLISpec fields. Build break is acceptable scope-limited because Wave 3 lands immediately. (Alternative: gate with build tag — see Action.)
    - The new types are exported and have proper YAML/JSON tags.
    - The schema has a `notification` object with all sub-blocks, all optional (no required fields anywhere in notification.*).
    - Schema has `runtime.vscode.enabled` (`*bool` → boolean type).
    - Schema removes the 14 `cli.*` properties listed in `<interfaces>` (note: leave `cli.agent`, `cli.claudeArgs`, `cli.codexArgs` for Wave 4 to remove).
    - `IsVSCodeEnabled` helper updated to accept `*RuntimeSpec` (or `*RuntimeVSCodeSpec`); old `*CLISpec` signature deleted.
  </behavior>
  <action>
**`pkg/profile/types.go`:**

  1. Add all 7 new types from the `<interfaces>` block above (NotificationSpec, NotificationEventsSpec, NotificationEmailSpec, NotificationSlackSpec, NotificationSlackInboundSpec, NotificationSlackTranscriptSpec, NotificationSlackInvitesSpec, RuntimeVSCodeSpec).
  2. Add `Notification *NotificationSpec` field to `type Spec struct`.
  3. Add `VSCode *RuntimeVSCodeSpec` field to `type RuntimeSpec struct`.
  4. From `type CLISpec struct`: delete the following 14 fields:
     - `NotifyOnPermission`, `NotifyOnIdle`, `NotifyCooldownSeconds`, `NotificationEmailAddress`
     - `NotifyEmailEnabled`, `NotifySlackEnabled`, `NotifySlackPerSandbox`, `NotifySlackChannelOverride`
     - `SlackArchiveOnDestroy`, `NotifySlackInboundEnabled`, `NotifySlackInboundMentionOnly`
     - `NotifySlackTranscriptEnabled`, `NotifySlackInviteEmails`, `UseSlackConnect`
  5. Also delete `VSCodeEnabled *bool` from CLISpec (moves to RuntimeSpec.VSCode.Enabled).
  6. Update `IsVSCodeEnabled` helper (currently at `pkg/profile/types.go:560` per RESEARCH.md §4d):
     ```go
     // OLD: func IsVSCodeEnabled(cli *CLISpec) bool
     // NEW:
     func IsVSCodeEnabled(vscode *RuntimeVSCodeSpec) bool {
         if vscode == nil || vscode.Enabled == nil { return false }
         return *vscode.Enabled
     }
     ```
     Callers (`create.go:620`, `create.go:2060`, `vscode.go:422`) WILL BREAK — that's Wave 3's job to update. Document the break in commit message so Wave 3 catches it.

  After this task: CLISpec contains only `NoBedrock`, `Agent`, `ClaudeArgs`, `CodexArgs` (Wave 4 removes the last 3).

**`pkg/profile/schemas/sandbox_profile.schema.json`:**

  1. Inside `properties.spec.properties.cli.properties`: delete the 14 properties listed above (do NOT delete `agent`, `claudeArgs`, `codexArgs` — Wave 4 owns those).
  2. Inside `properties.spec.properties.cli.required` (if it lists any of the deleted fields): remove them.
  3. Add `properties.spec.properties.notification`:
     ```json
     "notification": {
       "type": "object",
       "additionalProperties": false,
       "properties": {
         "events": {
           "type": "object",
           "additionalProperties": false,
           "properties": {
             "onPermission":    { "type": "boolean" },
             "onIdle":          { "type": "boolean" },
             "cooldownSeconds": { "type": "integer", "minimum": 0 }
           }
         },
         "email": {
           "type": "object",
           "additionalProperties": false,
           "properties": {
             "enabled": { "type": "boolean" },
             "address": { "type": "string", "format": "email" }
           }
         },
         "slack": {
           "type": "object",
           "additionalProperties": false,
           "properties": {
             "enabled":          { "type": "boolean" },
             "perSandbox":       { "type": "boolean" },
             "channelOverride":  { "type": "string", "pattern": "^C[A-Z0-9]+$" },
             "archiveOnDestroy": { "type": "boolean" },
             "inbound": {
               "type": "object", "additionalProperties": false,
               "properties": {
                 "enabled":     { "type": "boolean" },
                 "mentionOnly": { "type": "boolean" }
               }
             },
             "transcript": {
               "type": "object", "additionalProperties": false,
               "properties": { "enabled": { "type": "boolean" } }
             },
             "invites": {
               "type": "object", "additionalProperties": false,
               "properties": {
                 "emails":     { "type": "array", "items": { "type": "string", "format": "email" } },
                 "useConnect": { "type": "boolean" }
               }
             }
           }
         }
       }
     }
     ```
     NOT in `spec.required` — `notification:` is optional per locked decision.
  4. Inside `properties.spec.properties.runtime.properties`: add
     ```json
     "vscode": {
       "type": "object",
       "additionalProperties": false,
       "properties": { "enabled": { "type": "boolean" } }
     }
     ```
     Not required.
  5. Run schema lint (if any tool exists): `node -e "JSON.parse(require('fs').readFileSync('pkg/profile/schemas/sandbox_profile.schema.json'))"` to confirm valid JSON.

**Build-break note:** After this task, `go build ./...` fails because Wave 2 strips 14 fields from CLISpec that compiler + CLI cmd still read. This is intentional — Wave 3 fixes those in the same wave-handoff. Two options:
  - Option A (chosen, simpler): land Task 1 + Task 2 + Task 3 of Wave 2 in atomic commits but DO NOT mark Wave 2 "complete" until Wave 3 also lands the compiler changes. Orchestrator chains them.
  - Option B (more complex, NOT chosen): gate types via build tag. Adds gating boilerplate; not needed for solo execution.

Commit message: `feat(92-02): add NotificationSpec + RuntimeVSCodeSpec types + schema; strip 14 fields from CLISpec (build will be RED until Wave 3 lands)`.
  </action>
  <verify>
    <automated>node -e "JSON.parse(require('fs').readFileSync('pkg/profile/schemas/sandbox_profile.schema.json'))" &amp;&amp; go vet ./pkg/profile/...</automated>
    Expected: schema parses as valid JSON. `go vet ./pkg/profile/...` succeeds (pkg/profile/ itself compiles in isolation since it doesn't depend on pkg/compiler).
    Note: `go build ./...` (full repo) is EXPECTED to fail until Wave 3 lands.
    VC-1 (partial — full build green after Wave 3).
  </verify>
  <done>
    types.go has new shapes; CLISpec has only NoBedrock/Agent/ClaudeArgs/CodexArgs; schema is valid JSON with notification + runtime.vscode added and the 14 cli properties removed; `go vet ./pkg/profile/...` passes.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Add typed mergeNotificationSpec to inherit.go — pointer-merge bug fix</name>
  <files>
    pkg/profile/inherit.go,
    pkg/profile/inherit_notification_test.go
  </files>
  <behavior>
    - After this task: `mergeNotificationSpec(parent, child *NotificationSpec) *NotificationSpec` exists in inherit.go.
    - The merger is field-level nil-aware: a child setting only one notification field inherits ALL parent settings for unset fields.
    - The merger handles nested sub-blocks (Events, Email, Slack — including Slack.Inbound, Slack.Transcript, Slack.Invites) with their own field-level merge.
    - The Wave 0 RED test `TestInheritNotificationSpec_ChildOnlyTranscriptInheritsParentPerSandbox` becomes GREEN. Build tag `phase92_wave2` is removed.
    - `ResolveInheritance` (the outer entry point in inherit.go) is updated to call `mergeNotificationSpec` for the `Notification` field, bypassing the broken pointer-replace path.
  </behavior>
  <action>
**`pkg/profile/inherit.go`:**

  1. Add typed merger functions (one per type that needs field-level merge). Place them after the existing `mergeSpecSection` helper:

     ```go
     // mergeNotificationSpec performs field-level nil-aware merge of parent and child
     // NotificationSpec values. Child non-nil fields override parent; nil fields inherit.
     // This replaces the broken pointer-replace path in mergeSpecSection for Spec.Notification.
     //
     // Bug being fixed: pre-Phase-92, child Profile setting any single notification field
     // caused the entire parent.Spec.CLI pointer to be replaced wholesale by child.Spec.CLI,
     // losing all other parent notify settings. The Notification block is the first pointer-
     // typed Spec section to get a typed merger; mergeAgentSpec (Wave 4) is the second.
     func mergeNotificationSpec(parent, child *NotificationSpec) *NotificationSpec {
         if parent == nil { return child }
         if child  == nil { return parent }
         out := &NotificationSpec{
             Events: mergeNotificationEventsSpec(parent.Events, child.Events),
             Email:  mergeNotificationEmailSpec(parent.Email,  child.Email),
             Slack:  mergeNotificationSlackSpec(parent.Slack,  child.Slack),
         }
         return out
     }

     func mergeNotificationEventsSpec(parent, child *NotificationEventsSpec) *NotificationEventsSpec {
         if parent == nil { return child }
         if child  == nil { return parent }
         out := &NotificationEventsSpec{
             OnPermission:    pickBoolPtr(parent.OnPermission,    child.OnPermission),
             OnIdle:          pickBoolPtr(parent.OnIdle,          child.OnIdle),
             CooldownSeconds: pickIntPtr (parent.CooldownSeconds, child.CooldownSeconds),
         }
         return out
     }

     func mergeNotificationEmailSpec(parent, child *NotificationEmailSpec) *NotificationEmailSpec {
         if parent == nil { return child }
         if child  == nil { return parent }
         out := &NotificationEmailSpec{
             Enabled: pickBoolPtr(parent.Enabled, child.Enabled),
             Address: pickString  (parent.Address, child.Address),
         }
         return out
     }

     func mergeNotificationSlackSpec(parent, child *NotificationSlackSpec) *NotificationSlackSpec {
         if parent == nil { return child }
         if child  == nil { return parent }
         out := &NotificationSlackSpec{
             Enabled:          pickBoolPtr(parent.Enabled,          child.Enabled),
             PerSandbox:       pickBoolPtr(parent.PerSandbox,       child.PerSandbox),
             ChannelOverride:  pickString (parent.ChannelOverride,  child.ChannelOverride),
             ArchiveOnDestroy: pickBoolPtr(parent.ArchiveOnDestroy, child.ArchiveOnDestroy),
             Inbound:          mergeNotificationSlackInboundSpec   (parent.Inbound,    child.Inbound),
             Transcript:       mergeNotificationSlackTranscriptSpec(parent.Transcript, child.Transcript),
             Invites:          mergeNotificationSlackInvitesSpec   (parent.Invites,    child.Invites),
         }
         return out
     }

     func mergeNotificationSlackInboundSpec(parent, child *NotificationSlackInboundSpec) *NotificationSlackInboundSpec {
         if parent == nil { return child }
         if child  == nil { return parent }
         return &NotificationSlackInboundSpec{
             Enabled:     pickBoolPtr(parent.Enabled,     child.Enabled),
             MentionOnly: pickBoolPtr(parent.MentionOnly, child.MentionOnly),
         }
     }

     func mergeNotificationSlackTranscriptSpec(parent, child *NotificationSlackTranscriptSpec) *NotificationSlackTranscriptSpec {
         if parent == nil { return child }
         if child  == nil { return parent }
         return &NotificationSlackTranscriptSpec{
             Enabled: pickBoolPtr(parent.Enabled, child.Enabled),
         }
     }

     func mergeNotificationSlackInvitesSpec(parent, child *NotificationSlackInvitesSpec) *NotificationSlackInvitesSpec {
         if parent == nil { return child }
         if child  == nil { return parent }
         // Emails: child non-empty replaces parent (do not concat — match "child wins" semantics
         // of every other field; if operator wants to extend, they re-list parent's emails).
         out := &NotificationSlackInvitesSpec{
             Emails:     parent.Emails,
             UseConnect: pickBoolPtr(parent.UseConnect, child.UseConnect),
         }
         if len(child.Emails) > 0 {
             out.Emails = child.Emails
         }
         return out
     }

     // Helpers (place once at file end; check if already present from prior phases):
     func pickBoolPtr(parent, child *bool)  *bool   { if child != nil { return child }; return parent }
     func pickIntPtr (parent, child *int)   *int    { if child != nil { return child }; return parent }
     func pickString (parent, child string) string  { if child != "" { return child }; return parent }
     ```

  2. Update `ResolveInheritance` (the function that currently does `result.Spec = child.Spec` followed by `mergeSpecSection` calls):

     - Add an explicit pointer-merge call for Notification BEFORE the line that bulk-copies `result.Spec`:
       ```go
       result.Spec.Notification = mergeNotificationSpec(parent.Spec.Notification, child.Spec.Notification)
       ```
     - Make sure this fires regardless of the broken pointer-replace pattern that previously zeroed out `result.Spec.Notification`. If the existing pattern is `result.Spec = child.Spec` (RESEARCH.md §2b line 80), the merge call must come AFTER that line so it can read both parent + child and write to result.

     - The runtime/vscode pointer is handled by ordinary value-typed `mergeSpecSection` of RuntimeSpec (which already handles its other fields); confirm RuntimeSpec is value-typed by inspection. If it's pointer-typed too, also add a typed Runtime merger — but per RESEARCH.md, only CLI/Email/Budget/OTP/Secrets/Artifacts are pointer-typed pre-Phase-92. Runtime is value-typed.

**`pkg/profile/inherit_notification_test.go`:**

  1. Remove the `//go:build phase92_wave2` line + `// +build phase92_wave2` line at the top of the file (the Wave 0 RED-stub gating).
  2. Re-run `go test ./pkg/profile/ -run TestInheritNotificationSpec_ChildOnlyTranscriptInheritsParentPerSandbox` — must be GREEN.
  3. If the test has additional sub-cases (e.g., "child invites email replaces parent's"), confirm those pass too. Add if missing.

  Optional additional test cases (recommend adding):
  - `TestInheritNotificationSpec_ParentNil_ChildOverrides` (parent has no notification → use child as-is).
  - `TestInheritNotificationSpec_ChildNil_ParentInherits` (child has no notification → use parent as-is).
  - `TestInheritNotificationSpec_BothNil` (both nil → nil result).
  - `TestInheritNotificationSpec_InvitesMerge_ChildEmailsReplaceParent` (`len(child.Emails) > 0` → child wins).

  Each test follows the existing `pkg/profile/inherit_test.go` structure (per RESEARCH.md §3d).

Commit message: `feat(92-02): add typed mergeNotificationSpec — fix pointer-merge inheritance bug (VC-7)`.
  </action>
  <verify>
    <automated>go test ./pkg/profile/ -run 'TestInheritNotification' -count=1 -v</automated>
    Expected: all TestInheritNotification* tests GREEN. Specifically TestInheritNotificationSpec_ChildOnlyTranscriptInheritsParentPerSandbox GREEN.
    VC-7.
  </verify>
  <done>
    inherit.go has 7 new merger functions (1 entry + 6 sub-mergers); ResolveInheritance calls mergeNotificationSpec; the Wave 0 RED test is GREEN; build tag removed from inherit_notification_test.go.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: Rewire Slack / transcript / invite semantic validators to read notification.slack.*</name>
  <files>
    pkg/profile/validate.go,
    pkg/profile/slack_validate.go,
    pkg/profile/transcript_validate.go,
    pkg/profile/invite_validate.go
  </files>
  <behavior>
    - After this task: every semantic validator that currently reads `p.Spec.CLI.NotifySlack*` now reads `p.Spec.Notification.Slack.*`.
    - The Phase 63/67 mutual-exclusion rule (`notifySlackEnabled` + `notifySlackPerSandbox` + `notifySlackChannelOverride` interactions) is preserved at the new field paths.
    - The Phase 63 channel-ID regex error (`^C[A-Z0-9]+$`) fires against `notification.slack.channelOverride`.
    - The Phase 63 IsWarning flag still gates non-blocking warnings (no-op + neither-channel scenarios).
    - `pkg/profile/validate.go` orchestrator still calls into these sub-validators.
  </behavior>
  <action>
**Identify the existing validator files.** Per CONTEXT.md §Components Touched, "pkg/profile/validate.go + the slack/transcript/invite validator suites". The exact filenames may be `validate.go`, `validate_slack.go`, etc., or they may all live in `validate.go`. Inspect first:
  ```bash
  grep -l 'NotifySlackEnabled\|NotifySlackPerSandbox\|NotifySlackChannelOverride' pkg/profile/*.go
  ```

**For each file with notify references, replace `cli.NotifySlackX` → `notification.slack.X` reads:**

  Concrete renames (per RESEARCH.md §2a CLISpec mapping):
  | Old read                              | New read                                                        |
  |---|---|
  | `p.Spec.CLI.NotifySlackEnabled`        | `p.Spec.Notification.Slack.Enabled`                              |
  | `p.Spec.CLI.NotifySlackPerSandbox`     | `p.Spec.Notification.Slack.PerSandbox`                           |
  | `p.Spec.CLI.NotifySlackChannelOverride`| `p.Spec.Notification.Slack.ChannelOverride`                      |
  | `p.Spec.CLI.SlackArchiveOnDestroy`     | `p.Spec.Notification.Slack.ArchiveOnDestroy`                     |
  | `p.Spec.CLI.NotifySlackInboundEnabled` | `p.Spec.Notification.Slack.Inbound.Enabled`                      |
  | `p.Spec.CLI.NotifySlackInboundMentionOnly` | `p.Spec.Notification.Slack.Inbound.MentionOnly`              |
  | `p.Spec.CLI.NotifySlackTranscriptEnabled`  | `p.Spec.Notification.Slack.Transcript.Enabled`                |
  | `p.Spec.CLI.NotifySlackInviteEmails`   | `p.Spec.Notification.Slack.Invites.Emails`                       |
  | `p.Spec.CLI.UseSlackConnect`            | `p.Spec.Notification.Slack.Invites.UseConnect`                  |
  | `p.Spec.CLI.NotifyEmailEnabled`         | `p.Spec.Notification.Email.Enabled`                             |
  | `p.Spec.CLI.NotificationEmailAddress`   | `p.Spec.Notification.Email.Address`                             |
  | `p.Spec.CLI.NotifyOnPermission`         | `p.Spec.Notification.Events.OnPermission`                       |
  | `p.Spec.CLI.NotifyOnIdle`               | `p.Spec.Notification.Events.OnIdle`                             |
  | `p.Spec.CLI.NotifyCooldownSeconds`      | `p.Spec.Notification.Events.CooldownSeconds`                    |

  Each new read MUST be nil-safe: `if p.Spec.Notification == nil || p.Spec.Notification.Slack == nil { return nil }` style guard at the top of each validator. The notification block is optional per locked decision.

**Update error message paths** in the validators. Each ValidationError should reference the NEW field path:
  - OLD: `field: "spec.cli.notifySlackPerSandbox", message: "..."`
  - NEW: `field: "spec.notification.slack.perSandbox", message: "..."`

**Update error message text** where it embeds the field name:
  - OLD: `"notifySlackPerSandbox cannot be true without notifySlackEnabled"`
  - NEW: `"notification.slack.perSandbox cannot be true without notification.slack.enabled"`

**`pkg/profile/validate.go` orchestrator:**
  - Confirm it still calls each sub-validator. If the orchestrator pre-checks `p.Spec.CLI != nil` to skip Slack validation, update to `p.Spec.Notification != nil && p.Spec.Notification.Slack != nil`.
  - Verify no remaining `Spec.CLI.Notify` or `Spec.CLI.Slack` reads in any `pkg/profile/*.go` file:
    ```bash
    grep -n 'CLI\.Notify\|CLI\.Slack\|CLI\.VSCodeEnabled\|CLI\.NotificationEmailAddress\|CLI\.UseSlackConnect' pkg/profile/*.go
    ```
    Expected: zero matches. (Wave 4 owns `CLI.Agent`/`CLI.ClaudeArgs`/`CLI.CodexArgs`, which still exist.)

**Existing tests under `pkg/profile/`:** Some test files likely construct `&CLISpec{NotifySlackEnabled: ptr.Bool(true), ...}`. These will break compile. Update each to `&Spec{Notification: &NotificationSpec{Slack: &NotificationSlackSpec{Enabled: ptr.Bool(true)}}}`. There may be 5-10 such tests. Audit:
  ```bash
  grep -rn 'NotifySlackEnabled\|NotifySlackPerSandbox' pkg/profile/*_test.go
  ```

Commit message: `feat(92-02): rewire slack/transcript/invite validators to read notification.slack.* (paths + error messages)`.
  </action>
  <verify>
    <automated>go test ./pkg/profile/... -count=1</automated>
    Expected: all pkg/profile/ tests GREEN — both inheritance (Task 2) and existing slack/transcript/invite validators (Task 3).
    VC-1, VC-7.
  </verify>
  <done>
    All Slack/transcript/invite validators read from `p.Spec.Notification.Slack.*`; error paths updated; no `CLI.Notify*` or `CLI.Slack*` reads remain in pkg/profile/; pkg/profile/ tests pass.
  </done>
</task>

</tasks>

<verification>
- `go vet ./pkg/profile/...` succeeds (VC-1 partial — full `go build ./...` is RED until Wave 3, by design).
- `go test ./pkg/profile/... -count=1` GREEN — includes the Wave 0 inheritance RED test now GREEN (VC-7).
- `node -e "JSON.parse(...)"` confirms schema is valid JSON.
- `grep -rn 'CLI\.Notify\|CLI\.Slack\|CLI\.VSCodeEnabled' pkg/profile/` returns ZERO matches.
- Wave 3 inherits a known-broken `go build ./...` at the compiler/CLI-cmd boundary and is responsible for fixing it.
</verification>

<success_criteria>
- 7 new types added to `pkg/profile/types.go` (NotificationSpec + 6 sub-types + RuntimeVSCodeSpec).
- 14 fields stripped from CLISpec (+ `VSCodeEnabled` moved to RuntimeSpec.VSCode).
- Schema: `notification` block added + `runtime.vscode` added + 14 cli properties removed.
- `mergeNotificationSpec` + 6 sub-mergers + helpers in `pkg/profile/inherit.go`.
- Wave 0's VC-7 RED test stub has its build tag removed and is GREEN.
- All Slack/transcript/invite validators read from `notification.slack.*` paths.
- pkg/profile tests pass; pkg/compiler tests are EXPECTED to fail until Wave 3.
</success_criteria>

<output>
After completion, create `.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-02-SUMMARY.md` capturing:
- Path of the 7 new types in types.go.
- Path of new schema notification + runtime.vscode blocks.
- The 7 new merger functions in inherit.go and the bug they fix.
- Confirmation that VC-7 RED test is GREEN.
- The 14 CLISpec field removals enumerated.
- Wave 3 handoff note: "full `go build ./...` is RED until Wave 3 lands compiler + CLI cmd updates."
</output>
