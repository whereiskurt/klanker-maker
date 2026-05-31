---
phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating
plan: 3
type: execute
wave: 3
depends_on: [2]
files_modified:
  - pkg/compiler/userdata.go
  - internal/app/cmd/create.go
  - internal/app/cmd/create_slack.go
  - internal/app/cmd/create_slack_inbound.go
  - internal/app/cmd/agent.go
  - internal/app/cmd/shell.go
  - internal/app/cmd/destroy_slack.go
  - internal/app/cmd/doctor.go
  - internal/app/cmd/vscode.go
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
  - docs/slack-notifications.md
  - CLAUDE.md
  - OPERATOR-GUIDE.md
autonomous: true
requirements: []
verifies: [VC-1, VC-3, VC-11]

must_haves:
  truths:
    - "All 21 `.CLI.` notify/slack/vscode reads in `pkg/compiler/userdata.go` now read from `Spec.Notification.*` and `Spec.Runtime.VSCode`."
    - "Sandbox-side env var names (`KM_NOTIFY_ON_PERMISSION`, `KM_NOTIFY_SLACK_ENABLED`, `KM_SLACK_MENTION_ONLY`, `KM_NOTIFY_EMAIL`, `KM_SLACK_CHANNEL_ID`, etc.) are UNCHANGED — locked decision."
    - "All `internal/app/cmd/` files (create_slack.go, create_slack_inbound.go, create.go, agent.go, shell.go, destroy_slack.go, doctor.go, vscode.go) read from `notification.*` paths."
    - "`IsVSCodeEnabled` callers (`create.go:620`, `create.go:2060`, `vscode.go:422`) pass `Spec.Runtime.VSCode`. The `vscode.go:422` error message text references `spec.runtime.vscode.enabled`."
    - "All 20 profile YAMLs migrate `notify*` keys under `notification.*` and `vscodeEnabled` under `runtime.vscode.enabled`."
    - "`scripts/validate-all-profiles.sh` still exits 0 against all 20 profiles."
    - "Wave 0's `TestUserdataLearnV2Phase92ByteIdentity` (VC-3) is GREEN — userdata output for learn.v2 is byte-identical."
    - "`go build ./...` is GREEN after this wave — the Wave 2 build-break is closed."
    - "Doc sweep: `notifySlack*` references in docs/CLAUDE.md/OPERATOR-GUIDE.md updated to `notification.slack.*`."
  artifacts:
    - path: "pkg/compiler/userdata.go"
      provides: "All 21 .CLI.Notify*/Slack*/VSCodeEnabled reads moved to Spec.Notification.* / Spec.Runtime.VSCode reads."
      contains: "Spec.Notification"
    - path: "internal/app/cmd/create_slack.go"
      provides: "resolveSlackChannel reads Spec.Notification.Slack.*."
      contains: "Notification.Slack"
    - path: "internal/app/cmd/create_slack_inbound.go"
      provides: "Inbound provisioning reads Spec.Notification.Slack.Inbound.*."
      contains: "Notification.Slack.Inbound"
    - path: "docs/slack-notifications.md"
      provides: "notifySlack* sweep complete — references updated to notification.slack.*."
      pattern: "notification\\.slack"
  key_links:
    - from: "pkg/compiler/userdata.go"
      to: "pkg/profile/types.go (Spec.Notification, Spec.Runtime.VSCode)"
      via: "21 .CLI.* reads relocated to Notification and Runtime.VSCode"
      pattern: "Spec\\.Notification"
    - from: "internal/app/cmd/create_slack.go"
      to: "pkg/profile/types.go (Spec.Notification.Slack)"
      via: "resolveSlackChannel reads notification.slack.{channelOverride,perSandbox,enabled}"
      pattern: "Notification\\.Slack"
    - from: "scripts/validate-all-profiles.sh"
      to: "20 rewritten profile YAMLs"
      via: "km validate succeeds on every file in inventory"
      pattern: "km validate"
---

<objective>
Wire the new `notification:` block + `runtime.vscode.enabled` through the compiler, every `internal/app/cmd/` consumer, every profile YAML, and the doc surface. After this wave, `go build ./...` is GREEN and the Wave 2 build-break is closed.

Per RESEARCH.md §2d: Wave 3 changes 21 `.CLI.` field reads in `pkg/compiler/userdata.go` (not the PRD's "~30 notify sites" — the PRD counted total env var emissions including template-level variables; the actual Go-level changes are 21 field reads + the `resolveMentionOnly()` helper signature change).

Per RESEARCH.md §4d: `vscode.go:422` error message text references `"spec.cli.vscodeEnabled"` — must be updated to `"spec.runtime.vscode.enabled"`.

Per RESEARCH.md §4c: `internal/app/cmd/shell.go` does NOT directly reference `Spec.CLI` notify fields — it only calls `IsVSCodeEnabled` indirectly through `create.go`. The PRD's claim that `shell.go` needs notify-flag plumbing is partially incorrect; the actual shell.go change is small (per-invoke override flag plumbing only).

Purpose: Close the Wave 2 build-break; verify byte-identity (VC-3) holds; complete the notification-block migration through the entire vertical stack.
Output: 1 compiler file + 8 CLI cmd files + 20 profile YAMLs + 3 docs.
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-CONTEXT.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-RESEARCH.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-02-notification-types-schema-validator-inherit-PLAN.md
@pkg/compiler/userdata.go
@internal/app/cmd/create_slack.go
@internal/app/cmd/create_slack_inbound.go
@internal/app/cmd/create.go
@internal/app/cmd/destroy_slack.go
@internal/app/cmd/doctor.go
@internal/app/cmd/vscode.go
@profiles/learn.v2.yaml
@pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh

<interfaces>
Wave 3 consumes the types Wave 2 added. Key paths read in this wave:

  p.Spec.Notification                                      // *NotificationSpec (nil-safe)
  p.Spec.Notification.Events.OnPermission                  // *bool
  p.Spec.Notification.Events.OnIdle                        // *bool
  p.Spec.Notification.Events.CooldownSeconds               // *int
  p.Spec.Notification.Email.Enabled                        // *bool
  p.Spec.Notification.Email.Address                        // string
  p.Spec.Notification.Slack.Enabled                        // *bool
  p.Spec.Notification.Slack.PerSandbox                     // *bool
  p.Spec.Notification.Slack.ChannelOverride                // string
  p.Spec.Notification.Slack.ArchiveOnDestroy               // *bool
  p.Spec.Notification.Slack.Inbound.Enabled                // *bool
  p.Spec.Notification.Slack.Inbound.MentionOnly            // *bool
  p.Spec.Notification.Slack.Transcript.Enabled             // *bool
  p.Spec.Notification.Slack.Invites.Emails                 // []string
  p.Spec.Notification.Slack.Invites.UseConnect             // *bool
  p.Spec.Runtime.VSCode.Enabled                            // *bool

New helper signature from Wave 2:
  func IsVSCodeEnabled(vscode *RuntimeVSCodeSpec) bool

Sandbox-side env var names UNCHANGED (locked decision):
  KM_NOTIFY_ON_PERMISSION, KM_NOTIFY_ON_IDLE, KM_NOTIFY_COOLDOWN_SECONDS, KM_NOTIFY_EMAIL,
  KM_NOTIFY_EMAIL_ENABLED, KM_NOTIFY_SLACK_ENABLED, KM_SLACK_CHANNEL_ID, KM_SLACK_BRIDGE_URL,
  KM_SLACK_MENTION_ONLY, KM_NOTIFY_SLACK_INBOUND_ENABLED, KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED,
  KM_SLACK_INBOUND_QUEUE_URL, etc.
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Migrate pkg/compiler/userdata.go — 21 CLI notify/slack/vscode reads to Spec.Notification + Spec.Runtime.VSCode</name>
  <files>pkg/compiler/userdata.go</files>
  <behavior>
    - After this task: pkg/compiler/userdata.go has ZERO .CLI.Notify, .CLI.Slack, .CLI.VSCodeEnabled, .CLI.NotificationEmailAddress, .CLI.UseSlackConnect reads.
    - All 21 notify-related field reads now go through Spec.Notification.* (nil-safe) and Spec.Runtime.VSCode (nil-safe).
    - The resolveMentionOnly(p.Spec.CLI) helper is updated to take a profile or *NotificationSpec instead of *CLISpec.
    - Emitted env var names are UNCHANGED.
    - Wave 0's TestUserdataLearnV2Phase92ByteIdentity (VC-3) is GREEN.
    - pkg/compiler/ tests all pass.
  </behavior>
  <action>
Strategy: line-by-line migration using RESEARCH.md §2d as the surgical guide.

The 21 sites (per RESEARCH.md §2d):
  - Line 3866 — p.Spec.CLI.Agent codex check (NOT touched here — Wave 4 owns it)
  - Lines 3972-3973 — NotifyOnPermission, NotifyOnIdle
  - Lines 3979-3980 — NotifyCooldownSeconds
  - Lines 3982-3983 — NotificationEmailAddress
  - Lines 3988-3989 — NotifyEmailEnabled
  - Lines 3991-3992 — NotifySlackEnabled
  - Line 4000 — NotifySlackEnabled (channel block check)
  - Lines 4001-4004 — resolveMentionOnly(p.Spec.CLI) helper — must refactor
  - Lines 4010-4011 — NotifySlackChannelOverride
  - Line 4021 — NotifySlackInboundEnabled
  - Line 4038 — CLI.Agent (codex KM_AGENT) — NOT touched here — Wave 4 owns it
  - Line 4047 — NotifySlackInboundEnabled (inbound poller block)
  - Line 4079 — NotifySlackTranscriptEnabled
  - Line 4090 — NotifySlackEnabled (transcript check)
  - Lines 3653-3655 — template parameter VSCodeEnabled
  - Line 4198 — params.VSCodeEnabled = profile.IsVSCodeEnabled(...) caller

Migration pattern. Extract nil-safe helpers at top of file:

  func notifyEvents(p *profile.Profile) *profile.NotificationEventsSpec {
      if p.Spec.Notification == nil { return nil }
      return p.Spec.Notification.Events
  }
  func notifyEmail(p *profile.Profile) *profile.NotificationEmailSpec {
      if p.Spec.Notification == nil { return nil }
      return p.Spec.Notification.Email
  }
  func notifySlack(p *profile.Profile) *profile.NotificationSlackSpec {
      if p.Spec.Notification == nil { return nil }
      return p.Spec.Notification.Slack
  }

Then each call site:

  // OLD: if p.Spec.CLI != nil && p.Spec.CLI.NotifyOnPermission != nil && *p.Spec.CLI.NotifyOnPermission { ... }
  // NEW:
  if e := notifyEvents(p); e != nil && e.OnPermission != nil && *e.OnPermission { ... }

Specific field-by-field mapping table (RESEARCH.md §2a):

| OLD `p.Spec.CLI.X`              | NEW path                                              |
|---|---|
| NotifyOnPermission               | Notification.Events.OnPermission                       |
| NotifyOnIdle                     | Notification.Events.OnIdle                             |
| NotifyCooldownSeconds            | Notification.Events.CooldownSeconds                    |
| NotificationEmailAddress         | Notification.Email.Address                             |
| NotifyEmailEnabled               | Notification.Email.Enabled                             |
| NotifySlackEnabled               | Notification.Slack.Enabled                             |
| NotifySlackPerSandbox            | Notification.Slack.PerSandbox                          |
| NotifySlackChannelOverride       | Notification.Slack.ChannelOverride                     |
| NotifySlackInboundEnabled        | Notification.Slack.Inbound.Enabled                     |
| NotifySlackInboundMentionOnly    | Notification.Slack.Inbound.MentionOnly                 |
| NotifySlackTranscriptEnabled     | Notification.Slack.Transcript.Enabled                  |
| VSCodeEnabled (in CLI)           | Runtime.VSCode.Enabled                                 |

Refactor resolveMentionOnly:
- Current signature: resolveMentionOnly(p.Spec.CLI) reads CLI.NotifySlackInboundMentionOnly + Phase 91 mode-default logic.
- New signature: resolveMentionOnly(p *profile.Profile) bool reads Notification.Slack.Inbound.MentionOnly, same Phase 91 mode-default logic.
- Update every call site.

VSCode emission at line 4198:
  // OLD: params.VSCodeEnabled = profile.IsVSCodeEnabled(p.Spec.CLI)
  // NEW:
  var vscode *profile.RuntimeVSCodeSpec
  if p.Spec.Runtime != nil { vscode = p.Spec.Runtime.VSCode }
  params.VSCodeEnabled = profile.IsVSCodeEnabled(vscode)

DO NOT TOUCH in this task (Wave 4 owns):
- Line 3866: p.Spec.CLI.Agent (codex KM_AGENT detection)
- Line 4038: p.Spec.CLI.Agent (second occurrence)

Commit message: `feat(92-03): migrate userdata.go 21 CLI notify/slack/vscode reads to Notification + Runtime.VSCode (VC-3)`.
  </action>
  <verify>
    <automated>go build ./pkg/compiler/... &amp;&amp; go test ./pkg/compiler/ -run TestUserdataLearnV2Phase92ByteIdentity -count=1 -v</automated>
    Expected: build GREEN; byte-identity test GREEN. VC-1, VC-3.
  </verify>
  <done>
    Zero .CLI.Notify, .CLI.Slack, .CLI.VSCodeEnabled, .CLI.NotificationEmailAddress, .CLI.UseSlackConnect reads in userdata.go (audit via grep); byte-identity test GREEN; pkg/compiler builds.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Migrate internal/app/cmd/ — 8 files reading notification paths + vscode.go error message update</name>
  <files>
    internal/app/cmd/create.go,
    internal/app/cmd/create_slack.go,
    internal/app/cmd/create_slack_inbound.go,
    internal/app/cmd/agent.go,
    internal/app/cmd/shell.go,
    internal/app/cmd/destroy_slack.go,
    internal/app/cmd/doctor.go,
    internal/app/cmd/vscode.go
  </files>
  <behavior>
    - Every Spec.CLI.Notify, Spec.CLI.Slack, Spec.CLI.VSCodeEnabled, Spec.CLI.UseSlackConnect read in internal/app/cmd/ is migrated to the notification.* path.
    - IsVSCodeEnabled callers (create.go:620, create.go:2060, vscode.go) pass Spec.Runtime.VSCode.
    - vscode.go:422 error message text references "spec.runtime.vscode.enabled" not "spec.cli.vscodeEnabled".
    - internal/app/cmd/agent.go and shell.go STILL read Spec.CLI.Agent, Spec.CLI.ClaudeArgs, Spec.CLI.CodexArgs — Wave 4 owns those. Do not pre-migrate them in this wave.
    - --notify-on-permission, --no-notify-on-permission, --notify-on-idle, --no-notify-on-idle, --notify-slack-transcript-enabled CLI flag handling still works. Flag binding code reads/writes through the new notification paths but emits the same KM_NOTIFY_* env var names.
    - go build ./... is GREEN after this task.
  </behavior>
  <action>
Per RESEARCH.md §4c, file-by-file:

**create_slack.go** — resolveSlackChannel() reads cli.NotifySlackPerSandbox, cli.NotifySlackChannelOverride, cli.NotifySlackEnabled, cli.NotifySlackInviteEmails, cli.UseSlackConnect. Update signature: take *profile.NotificationSpec or *profile.Profile instead of *profile.CLISpec. Rewrite each read using notification.slack.* paths (nil-safe). Line 224 loop over cli.NotifySlackInviteEmails becomes notification.Slack.Invites.Emails.

**create_slack_inbound.go** — reads cli.NotifySlackInboundEnabled, cli.NotifySlackEnabled, cli.NotifySlackPerSandbox. Update signature similarly.

**create.go** — IsVSCodeEnabled callers at lines 620, 2060 (RESEARCH.md §4d):
  // OLD: profile.IsVSCodeEnabled(resolvedProfile.Spec.CLI)
  // NEW:
  var v *profile.RuntimeVSCodeSpec
  if resolvedProfile.Spec.Runtime != nil { v = resolvedProfile.Spec.Runtime.VSCode }
  profile.IsVSCodeEnabled(v)

Or extract a one-line runtimeVSCode(p) helper. Slack provisioning gating reads (cli.NotifySlackEnabled, cli.NotifySlackPerSandbox): update to notification paths.

**agent.go** — reads cli.Agent, cli.ClaudeArgs, cli.CodexArgs — DO NOT MIGRATE. Wave 4 owns. If this file also reads notify fields (e.g., --notify-on-permission flag bound to a struct), update those reads to notification.events.* paths.

**shell.go** — per RESEARCH.md §2g, does NOT directly reference Spec.CLI notify fields. It only calls IsVSCodeEnabled indirectly through create.go:620, 2060. If shell.go has its own IsVSCodeEnabled call (audit via grep), update similarly. Per CONTEXT.md §Wave 3: --notify-on-permission, --no-notify-on-permission, --notify-on-idle, --no-notify-on-idle, --notify-slack-transcript-enabled override flags MUST still work in km shell and km agent run. If bound here, update the binding to set the notification path's typed equivalent in memory; emit the same KM_NOTIFY_* env vars at SSM-launch time.

**destroy_slack.go** — reads cli.SlackArchiveOnDestroy becomes notification.slack.archiveOnDestroy.

**doctor.go** — checkSlackBotUserIDCached reads cli.NotifySlackInboundMentionOnly per CONTEXT.md, update to notification.slack.inbound.mentionOnly. Other doctor checks iterating sandboxes and reading notify gates: update each read.

**vscode.go** — Line 422 (RESEARCH.md §4d): error message text references spec.cli.vscodeEnabled — change to spec.runtime.vscode.enabled. Any read of Spec.CLI.VSCodeEnabled: update to Spec.Runtime.VSCode.Enabled via the helper.

End-of-task audit:
- grep -rn 'Spec\.CLI\.Notify\|Spec\.CLI\.Slack\|Spec\.CLI\.VSCodeEnabled\|Spec\.CLI\.UseSlackConnect\|Spec\.CLI\.NotificationEmailAddress' internal/app/cmd/ — expected ZERO matches.
- grep -rn 'Spec\.CLI\.Agent\|Spec\.CLI\.ClaudeArgs\|Spec\.CLI\.CodexArgs' internal/app/cmd/ — expected SOME matches (Wave 4 owns).

Commit message: `feat(92-03): migrate internal/app/cmd/ to notification + runtime.vscode reads (8 files)`.
  </action>
  <verify>
    <automated>go build ./... &amp;&amp; go test ./internal/... -count=1</automated>
    Expected: full repo build is GREEN (Wave 2 build-break closed); internal/ tests pass. VC-1.
  </verify>
  <done>
    All 8 files migrated; go build ./... succeeds; vscode.go:422 error text references new path; only Spec.CLI.Agent, ClaudeArgs, CodexArgs remain as CLI reads in internal/app/cmd/ (Wave 4 territory).
  </done>
</task>

<task type="auto">
  <name>Task 3: Rewrite all 20 profile YAMLs — notify keys move under notification; vscodeEnabled moves under runtime.vscode.enabled</name>
  <files>
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
  <action>
Single atomic rewrite of all 20 profile YAMLs. Same Wave 1 rationale: shared mechanical transformation, partial state breaks km validate.

Transformation per file:

1. Under spec.cli: block, MOVE the following 14 fields out to a new spec.notification: block:

| OLD spec.cli.*                       | NEW path                                              |
|---|---|
| notifyOnPermission                   | spec.notification.events.onPermission                  |
| notifyOnIdle                         | spec.notification.events.onIdle                        |
| notifyCooldownSeconds                | spec.notification.events.cooldownSeconds               |
| notificationEmailAddress             | spec.notification.email.address                        |
| notifyEmailEnabled                   | spec.notification.email.enabled                        |
| notifySlackEnabled                   | spec.notification.slack.enabled                        |
| notifySlackPerSandbox                | spec.notification.slack.perSandbox                     |
| notifySlackChannelOverride           | spec.notification.slack.channelOverride                |
| slackArchiveOnDestroy                | spec.notification.slack.archiveOnDestroy               |
| notifySlackInboundEnabled            | spec.notification.slack.inbound.enabled                |
| notifySlackInboundMentionOnly        | spec.notification.slack.inbound.mentionOnly            |
| notifySlackTranscriptEnabled         | spec.notification.slack.transcript.enabled             |
| notifySlackInviteEmails              | spec.notification.slack.invites.emails                 |
| useSlackConnect                      | spec.notification.slack.invites.useConnect             |

2. MOVE spec.cli.vscodeEnabled to spec.runtime.vscode.enabled.

3. Leave in spec.cli: ONLY: noBedrock, agent, claudeArgs, codexArgs (Wave 4 removes the last 3).

4. Omit empty sub-blocks. If a profile didn't set any notify* fields, do NOT add spec.notification: {} — omit the block entirely (optional per locked decision).

5. Preserve value semantics. A profile with notifySlackEnabled: false becomes notification.slack.enabled: false (NOT omitted — false is explicit). A profile lacking the field entirely keeps lacking it.

Special-cases per CONTEXT.md §Per-wave fixture treatment:
- profiles/learn.v2.yaml — byte-identity baseline. After rewrite, TestUserdataLearnV2Phase92ByteIdentity MUST stay GREEN. If it fails: investigate immediately. Common cause: omitting an explicit false that the userdata template treats differently from "absent" — re-emit false explicitly.
- profiles/learn.v2.chatty.yaml / profiles/learn.v2.polite.yaml — encode Phase 91 polite-bot mode via notifySlackInboundMentionOnly. After rewrite, they sit at notification.slack.inbound.mentionOnly: {true|false} and the chatty/polite contract is preserved.
- pkg/profile/builtins/{open-dev,restricted-dev,hardened,sealed}.yaml — extra scrutiny in Wave 6 UAT. Confirm they pass km validate.
- profiles/example-additional-snapshots.yaml — snapshot placeholders untouched (Wave 1 already handled).

Verification step within this task:
- Build km: make build.
- Run: bash scripts/validate-all-profiles.sh — all 20 must pass.
- Run: go test ./pkg/compiler/ -run TestUserdataLearnV2Phase92ByteIdentity -count=1 — must be GREEN.

Commit message: `feat(92-03): rewrite 20 profile YAMLs — notify keys to notification.* and vscodeEnabled to runtime.vscode.enabled`.
  </action>
  <verify>
    <automated>make build &amp;&amp; bash scripts/validate-all-profiles.sh &amp;&amp; go test ./pkg/compiler/ -run TestUserdataLearnV2Phase92ByteIdentity -count=1</automated>
    Expected: km builds; all 20 profiles validate; byte-identity test GREEN. VC-3, VC-11.
  </verify>
  <done>
    All 20 YAMLs rewritten with new field paths; validation script exits 0; byte-identity test GREEN.
  </done>
</task>

<task type="auto">
  <name>Task 4: Doc sweep — docs/slack-notifications.md + CLAUDE.md + OPERATOR-GUIDE.md</name>
  <files>
    docs/slack-notifications.md,
    CLAUDE.md,
    OPERATOR-GUIDE.md
  </files>
  <action>
**docs/slack-notifications.md**:
- Grep for notifySlack*, notifyEmail*, notifyOn*, notificationEmailAddress, slackArchiveOnDestroy, useSlackConnect, vscodeEnabled references in profile YAML examples.
- Replace each with the new notification.* / runtime.vscode.enabled path.
- Update the Phase 91 polite-bot section: KM_SLACK_MENTION_ONLY is now driven by notification.slack.inbound.mentionOnly (NOT cli.notifySlackInboundMentionOnly).
- Add a top-of-file NOTE: "Phase 92 (2026-05-31): All cli.notify* fields moved under spec.notification:. Sandbox-side env var names are UNCHANGED — only the YAML surface changed. See CONTEXT.md §Phase Boundary for the full mapping."

**CLAUDE.md**:
- Grep for notifySlackEnabled, notifySlackPerSandbox, notifySlackInboundMentionOnly, notifyEmailEnabled, vscodeEnabled in profile-shape examples.
- Replace with new paths.
- The Phase 91 callout already references KM_SLACK_MENTION_ONLY (env var, unchanged) and notifySlackInboundMentionOnly (YAML key, must change). Update the YAML key reference to notification.slack.inbound.mentionOnly. Keep env var references as-is.
- Add Phase 92 entry near top: "Phase 92 (2026-05-31): cli.notify* fields moved under spec.notification:. cli.vscodeEnabled moved to spec.runtime.vscode.enabled. Env var names unchanged."

**OPERATOR-GUIDE.md**:
- Grep for the same fields in profile YAML examples; update each.
- The notifySlackInviteEmails operator runbook is now notification.slack.invites.emails; the useSlackConnect toggle is notification.slack.invites.useConnect.
- Add a Phase 92 entry in the recent-changes section.

Smoke check after edits:

  grep -rn 'notifySlackEnabled\|notifySlackPerSandbox\|notifySlackInboundMentionOnly\|notifyEmailEnabled\|notificationEmailAddress\|slackArchiveOnDestroy\|useSlackConnect\|notifyOnPermission\|notifyOnIdle\|notifySlackTranscriptEnabled\|notifySlackInboundEnabled\|notifySlackChannelOverride\|notifyCooldownSeconds' docs/slack-notifications.md CLAUDE.md OPERATOR-GUIDE.md

Expected: zero matches (or only inside historical phase-change notes that quote the old name).

Note: env var names (KM_SLACK_MENTION_ONLY, KM_NOTIFY_SLACK_ENABLED, etc.) are PRESERVED — do NOT rename them in docs.

Commit message: `docs(92-03): sweep notifySlack keys to notification.slack.* and vscodeEnabled to runtime.vscode.enabled`.
  </action>
  <verify>
    <automated>! grep -rn 'notifySlackEnabled\b\|notifySlackPerSandbox\b\|notifySlackInboundMentionOnly\b\|notifyEmailEnabled\b\|notificationEmailAddress\b\|slackArchiveOnDestroy\b\|useSlackConnect\b\|notifyOnPermission\b\|notifyOnIdle\b' docs/slack-notifications.md CLAUDE.md OPERATOR-GUIDE.md</automated>
    Expected: exit code 0 from the negated grep (no matches in active prose). If any historical phase-change note still references the old key, that is acceptable. VC-1.
  </verify>
  <done>
    All three docs reflect Wave 3 renames; grep audit clean against active prose.
  </done>
</task>

</tasks>

<verification>
- `go build ./...` is GREEN (closes Wave 2 build-break) — VC-1.
- `go test ./pkg/compiler/ -run TestUserdataLearnV2Phase92ByteIdentity` is GREEN — VC-3.
- `bash scripts/validate-all-profiles.sh` exits 0 on all 20 profiles — VC-11.
- Grep audit on `internal/app/cmd/` shows only `Spec.CLI.Agent/ClaudeArgs/CodexArgs` remaining (Wave 4 territory).
- Grep audit on docs shows zero stale `notifySlack*` references.
</verification>

<success_criteria>
- Wave 2 build-break is fully closed: `go build ./...` succeeds.
- userdata.go has 21 `.CLI.` notify reads relocated to `Spec.Notification.*` / `Spec.Runtime.VSCode`.
- 8 internal/app/cmd/ files migrated.
- 20 profile YAMLs migrated atomically.
- 3 docs migrated.
- VC-3 byte-identity GREEN: end-to-end the pipeline emits the same userdata for `profiles/learn.v2.yaml`.
- VC-11 validation script GREEN.
</success_criteria>

<output>
After completion, create `.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-03-SUMMARY.md` capturing:
- Exact line/file references for the 21 userdata.go migrations.
- Per-file change notes for the 8 internal/app/cmd/ files.
- Confirmation that VC-3 byte-identity test stayed GREEN.
- Confirmation that VC-11 validation script exits 0 against all 20 profiles.
- Wave 4 handoff: only `Spec.CLI.Agent/ClaudeArgs/CodexArgs` remain in the codebase as CLI reads — Wave 4 removes them.
</output>
