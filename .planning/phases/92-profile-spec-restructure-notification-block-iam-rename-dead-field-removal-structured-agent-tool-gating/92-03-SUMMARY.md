---
phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating
plan: 03
subsystem: compiler-cli-fixtures-docs
tags: [notification-block, compiler-migration, cmd-migration, profile-yaml-rewrite, byte-identity, react-always-rehome, doc-sweep]

# Dependency graph
requires:
  - phase: 92-02
    provides: NotificationSpec + RuntimeVSCodeSpec types/schema/validator/inherit; 14 CLISpec fields stripped; IsVSCodeEnabled(*RuntimeVSCodeSpec)
provides:
  - pkg/compiler/userdata.go reads notification.* / runtime.vscode (21 sites + resolveMentionOnly/resolveReactAlways re-signed to *SandboxProfile)
  - notifySlackInboundReactAlways re-homed off CLISpec → notification.slack.inbound.reactAlways (types + schema + inherit merger)
  - 8 internal/app/cmd/ consumers migrated to notification.* / runtime.vscode reads; vscode.go error text → spec.runtime.vscode.enabled
  - 7 profile YAMLs (of 20) rewritten to notification: block; other 13 carried no notify keys
  - go build ./... GREEN — Wave 2 build-break closed
  - VC-3 byte-identity GREEN; VC-11 validate-all-profiles GREEN
  - docs/slack-notifications.md + CLAUDE.md + OPERATOR-GUIDE.md swept to notification.* paths
affects: [92-04-agent-types, 92-05-agent-synthesizers, 92-06-operator-uat]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Nil-safe notification accessors (notifyEvents/notifyEmail/notifySlack/notifySlackInbound + slackEnabled/slackPerSandbox/slackInboundEnabled/slackTranscriptEnabled) in pkg/compiler/userdata.go"
    - "Parallel notificationSlack/notificationSlackInbound/runtimeVSCode nil-safe accessors in internal/app/cmd/create_slack.go reused across cmd consumers"

key-files:
  created: []
  modified:
    - pkg/compiler/userdata.go
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/inherit.go
    - pkg/profile/profile_cli_mention_test.go
    - pkg/compiler/userdata_82_02_test.go
    - pkg/compiler/userdata_ami_test.go
    - pkg/compiler/userdata_mention_test.go
    - pkg/compiler/userdata_notify_test.go
    - pkg/compiler/userdata_codex_test.go
    - pkg/compiler/userdata_transcript_test.go
    - pkg/compiler/userdata_slack_inbound_test.go
    - pkg/compiler/userdata_phase79_test.go
    - pkg/compiler/userdata_test.go
    - internal/app/cmd/create.go
    - internal/app/cmd/create_slack.go
    - internal/app/cmd/create_slack_inbound.go
    - internal/app/cmd/agent.go
    - internal/app/cmd/doctor.go
    - internal/app/cmd/vscode.go
    - internal/app/cmd/create_slack_test.go
    - internal/app/cmd/create_slack_invite_test.go
    - internal/app/cmd/create_slack_inbound_test.go
    - internal/app/cmd/create_test.go
    - internal/app/cmd/doctor_slack_bot_user_id_test.go
    - internal/app/cmd/vscode_test.go
    - profiles/dc34.yaml
    - profiles/learn.v2.yaml
    - profiles/learn.v2.chatty.yaml
    - profiles/learn.v2.codex.yaml
    - profiles/learn.v2.polite.yaml
    - profiles/locked.yaml
    - profiles/locked.ami.yaml
    - docs/slack-notifications.md
    - CLAUDE.md
    - OPERATOR-GUIDE.md

key-decisions:
  - "Resolved the Wave-2 deferred item: re-homed notifySlackInboundReactAlways off CLISpec into notification.slack.inbound.reactAlways (types + schema property + inherit merger + resolveReactAlways), per the environment-notes instruction. CLISpec is now reduced to exactly NoBedrock/Agent/ClaudeArgs/CodexArgs."
  - "Kept the NotifyEnv emission outer gate at `if p.Spec.CLI != nil` (not Notification != nil): KM_AGENT (Wave 4 territory) still reads p.Spec.CLI.Agent, and learn.v2 retains a cli: block, so byte-identity is preserved. Field reads inside the block all moved to Notification.*."
  - "Re-signed resolveMentionOnly/resolveReactAlways from *CLISpec to *SandboxProfile and rewrote userdata_mention_test.go to drive profiles through the notification block."

patterns-established:
  - "Wave-3 closes the Wave-2 build-break: go build ./... + go test ./pkg/compiler ./pkg/profile + scripts/validate-all-profiles.sh are all GREEN after this wave; the only remaining CLISpec reads in the codebase are Spec.CLI.Agent (Wave 4 territory)."

requirements-completed: []

# Metrics
duration: 55min
completed: 2026-05-31
---

# Phase 92 Plan 03: Notification Compiler + CLI + Fixtures + Docs Summary

**Closed the Wave-2 build-break: migrated the 21 `.CLI.` notify/slack/vscode reads in `pkg/compiler/userdata.go` and all 8 `internal/app/cmd/` consumers to `spec.notification.*` / `spec.runtime.vscode`, re-homed the 15th notify field (`notifySlackInboundReactAlways` → `notification.slack.inbound.reactAlways`), rewrote the 7 profile YAMLs that carried notify keys, and swept three docs — `go build ./...` is GREEN, VC-3 byte-identity holds (learn.v2 userdata is byte-identical), and all 20 profiles validate.**

## Performance
- **Duration:** ~55 min
- **Tasks:** 4 (+ 1 follow-up comment fix)
- **Files changed:** 36 across 6 commits

## Accomplishments

### Task 1 — pkg/compiler/userdata.go migration (`a2091ec7`)
- **21 `.CLI.` notify/slack/vscode reads relocated.** Added nil-safe helpers `notifyEvents`/`notifyEmail`/`notifySlack`/`notifySlackInbound` + boolean predicates `slackEnabled`/`slackPerSandbox`/`slackInboundEnabled`/`slackTranscriptEnabled`.
  - Main NotifyEnv block (lines ~3990–4174): `NotifyOnPermission`→`Notification.Events.OnPermission`, `NotifyOnIdle`→`Events.OnIdle`, `NotifyCooldownSeconds`→`Events.CooldownSeconds` (`*int`), `NotificationEmailAddress`→`Email.Address`, `NotifyEmailEnabled`→`Email.Enabled`, `NotifySlackEnabled`→`Slack.Enabled`, `NotifySlackChannelOverride`→`Slack.ChannelOverride`, `NotifySlackInboundEnabled`→`Slack.Inbound.Enabled`, `NotifySlackTranscriptEnabled`→`Slack.Transcript.Enabled`. `KM_AGENT` (lines 3933, 4120) left reading `p.Spec.CLI.Agent` — Wave 4.
  - VSCode caller (line 4280): `IsVSCodeEnabled(p.Spec.CLI)` → `IsVSCodeEnabled(p.Spec.Runtime.VSCode)`.
- **`resolveMentionOnly`/`resolveReactAlways` re-signed** `*profile.CLISpec` → `*profile.SandboxProfile`; updated all call sites.
- **`notifySlackInboundReactAlways` re-home:** added `ReactAlways *bool` to `NotificationSlackInboundSpec` (types.go), added the `reactAlways` schema property + removed the `notifySlackInboundReactAlways` cli property, added `ReactAlways` to `mergeNotificationSlackInboundSpec`, removed the 15th field from CLISpec. **CLISpec is now exactly `NoBedrock/Agent/ClaudeArgs/CodexArgs`.**
- **Sandbox-side env var names UNCHANGED** (`KM_NOTIFY_*`, `KM_SLACK_*`) — locked decision.
- Migrated 9 pkg/compiler test files + 1 pkg/profile round-trip test off the removed CLISpec fields onto the notification block.

### Task 2 — internal/app/cmd/ migration (`e7c0c88f`)
- **create_slack.go:** `resolveSlackChannel` reads `notification.slack.*` via new nil-safe `notificationSlack`/`notificationSlackInbound`/`runtimeVSCode` accessors; invites loop reads `slack.invites.{emails,useConnect}`.
- **create_slack_inbound.go:** `provisionSlackInboundQueue` + `postReadyAnnouncement` read `notification.slack.inbound.*`; per-sandbox react_always override reads `inbound.ReactAlways`.
- **create.go:** Slack-provision gate (590), transcript warning (610), `archiveOnDestroy` metadata (896), inbound queue gate (1030), and both `IsVSCodeEnabled` callers (620, 2060) moved to notification.* / runtime.vscode.
- **agent.go:** added `loadProfileNotifyEvents` returning `*NotificationEventsSpec`; `--notify-on-permission`/`--notify-on-idle` profile defaults now read `notification.events.*`. `Spec.CLI.Agent/ClaudeArgs/CodexArgs` reads left intact (Wave 4).
- **doctor.go:** `anyProfileMentionOnly` reads `notification.slack.*`.
- **vscode.go:** line 422 error text now references `spec.runtime.vscode.enabled`.
- Migrated 6 cmd test files (incl. source-scan pattern updates + YAML-fixture rewrites for the doctor mention-only test and the vscode hint assertions).

### Task 3 — profile YAML rewrite (`ad2507f9`)
- Rewrote the **7 of 20** profiles that carried notify keys: `dc34.yaml`, `learn.v2.yaml`, `learn.v2.chatty.yaml`, `learn.v2.codex.yaml`, `learn.v2.polite.yaml`, `locked.yaml`, `locked.ami.yaml`. The other 13 (ao/codex/goose/dc34.ami/example-additional-snapshots/all 8 builtins) carried no notify keys and were left untouched.
- `learn.v2.polite.yaml` carries `inbound.mentionOnly: true` + `inbound.reactAlways: false`; `learn.v2.chatty.yaml` carries `inbound.mentionOnly: false`; invites moved to `slack.invites.{emails,useConnect}`.
- **VC-3 byte-identity:** `TestUserdataLearnV2Phase92ByteIdentity` GREEN — the learn.v2 notification-block rewrite produces byte-identical userdata.
- **VC-11:** `scripts/validate-all-profiles.sh` exits 0 across all 20.

### Task 4 — doc sweep (`3f09f9c5`, + comment fix `388430ed`)
- **docs/slack-notifications.md:** top-of-file Phase 92 NOTE; swept channel-modes table, profile-fields table, validation-rules table, all example-profile YAML, env-var source columns, the bot-scope table, the invites section, and the entire Phase 91 polite-bot section to `notification.slack.*`.
- **CLAUDE.md:** extended the Phase 92 structural-cleanup section with the notification-block + vscode mapping; updated Phase 72/91 callouts. **Releases section preserved untouched.**
- **OPERATOR-GUIDE.md:** extended the top-of-file Phase 92 note; per-profile mention-only override example rewritten.
- Env var names (`KM_SLACK_MENTION_ONLY`, `KM_NOTIFY_SLACK_ENABLED`, …) preserved as-is.

## Verification
- `go build ./...` — **GREEN** (Wave 2 build-break closed). VC-1.
- `go test ./pkg/compiler/ -run TestUserdataLearnV2Phase92ByteIdentity` — **GREEN**. VC-3.
- `go test ./pkg/compiler/ ./pkg/profile/...` — **GREEN.**
- `go test ./internal/app/cmd/` (notification/slack/vscode subset) — **GREEN** after fixture migration.
- `bash scripts/validate-all-profiles.sh` — **all 20 valid, exit 0.** VC-11.
- cmd CLI-field audit: zero `Spec.CLI.Notify*/Slack*/VSCodeEnabled/UseSlackConnect` reads remain; only `Spec.CLI.Agent` (Wave 4) survives.
- Doc grep audit: zero stale `notifySlack*`/`notifyOn*`/`useSlackConnect` references in active prose (only the intentional `spec.cli.notify*` wildcard in the Phase 92 NOTEs).

## Wave 4 handoff
- The **only** remaining `Spec.CLI` reads in the codebase are `Spec.CLI.Agent` (userdata.go:3933/4120, doctor.go:3401, doctor_codex.go:260) plus `Spec.CLI.ClaudeArgs`/`Spec.CLI.CodexArgs` in agent.go's `loadProfileCLIClaudeArgs`/`loadProfileCLICodexArgs`/`BuildAgentCommand`. Wave 4 owns the structured `agent:` block and removes these last CLISpec fields.
- Wave 0's `phase92_wave4` / `phase92_wave5` build-tagged RED stubs (`pkg/profile/validate_mixed_settings_test.go`, `pkg/compiler/agent_{claude,codex}_golden_test.go`) are untouched and remain RED by design.

## Deviations from Plan

### Auto-fixed / scoped decisions

**1. [Plan-directed] Re-homed `notifySlackInboundReactAlways` off CLISpec (Wave-2 deferred item)**
- **Found during:** Task 1 — resolving the Wave-2 deviation #1 carry-over.
- **Action:** Per the environment notes, added `reactAlways` to `NotificationSlackInboundSpec` (types + schema + inherit merger), updated `resolveReactAlways` to read it, and removed `NotifySlackInboundReactAlways` from CLISpec. This was explicitly instructed and closes the open Wave-2 item.
- **Files:** types.go, schema, inherit.go, userdata.go, create_slack_inbound.go, learn.v2.polite.yaml, profile_cli_mention_test.go, create_slack_inbound_test.go.
- **Commits:** a2091ec7, e7c0c88f, ad2507f9.

**2. [Rule 3 — Blocking] Migrated more test files than the plan enumerated**
- **Found during:** Tasks 1–2 (`go vet`/`go test` compile).
- **Issue:** Stripping the CLISpec fields breaks compile in 9 pkg/compiler test files, 1 pkg/profile round-trip test, and 6 cmd test files (struct literals + YAML fixtures + source-scan pattern strings). The plan listed only a subset.
- **Fix:** Migrated each to the notification block; rewrote the doctor mention-only YAML fixtures and the vscode hint assertions (`vscodeEnabled` → `vscode.enabled`) and the create.go source-scan patterns.
- **Commits:** a2091ec7, e7c0c88f.

**3. [Sequencing note] Task 1 verify (byte-identity) could only pass after Task 3**
- The byte-identity test parses the live `profiles/learn.v2.yaml`, which still had `cli.notify*` keys (schema-rejected after Wave 2) until Task 3 rewrote it. Task 1 committed with the compiler migration verified by build + the cmd/profile suites; byte-identity was confirmed GREEN immediately after the learn.v2 rewrite in Task 3 and again in final verification.

**Total deviations:** 3 (1 plan-directed re-home, 1 Rule 3 test-scope expansion, 1 sequencing note). No Rule 4 (architectural) triggers.

## Self-Check: PASSED
