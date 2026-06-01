---
phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating
plan: 02
subsystem: profile-spec
tags: [notification-block, schema, typed-inheritance-merge, pointer-merge-bug-fix, dead-field-removal, validator-rewire]

# Dependency graph
requires:
  - phase: 92-00
    provides: phase92_wave2 RED stub (inherit_notification_test.go) + NotificationSpec target API contract
  - phase: 92-01
    provides: apiVersion v1alpha2 + IAM rename + dead-agent removal (clean CLISpec base to strip)
provides:
  - spec.notification block (events/email/slack + slack.inbound/transcript/invites) in types + schema at v1alpha2
  - RuntimeVSCodeSpec at spec.runtime.vscode.enabled; IsVSCodeEnabled re-signed to *RuntimeVSCodeSpec
  - 14 cli.notify* fields + cli.vscodeEnabled stripped from CLISpec (NoBedrock/Agent/ClaudeArgs/CodexArgs/NotifySlackInboundReactAlways survive)
  - typed mergeNotificationSpec + 6 sub-mergers + pickBoolPtr/pickIntPtr/pickString (pointer-merge inheritance bug fix, VC-7)
  - all Slack/transcript/invite/email/event semantic validators read notification.* paths
affects: [92-03-notification-compiler-cli-fixtures-docs, 92-04-agent-types]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Typed field-level nil-aware pointer-merge (mergeNotificationSpec) replacing reflection-based whole-section pointer-replace for the first pointer-typed Spec section"
    - "pickBoolPtr/pickIntPtr/pickString child-wins-iff-set merge primitives reusable by Wave 4's mergeAgentSpec"

key-files:
  created: []
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/inherit.go
    - pkg/profile/validate.go
    - pkg/profile/inherit_notification_test.go
    - pkg/profile/validate_test.go
    - pkg/profile/validate_slack_inbound_test.go
    - pkg/profile/validate_slack_transcript_test.go
    - pkg/profile/validate_slack_invite_emails_test.go
    - pkg/profile/profile_cli_mention_test.go
    - pkg/profile/types_test.go

key-decisions:
  - "Kept cli.notifySlackInboundReactAlways in CLISpec — the plan's removal list + target NotificationSlackInboundSpec omit it, and removing it would break the compiler's resolveReactAlways reader without a target home. Flagged for Wave 3/follow-up (deferred-items)."
  - "Reconciled the Wave-0 RED test's signature mismatch: it called mergeNotificationSpec(*SandboxProfile) but the plan's typed merger takes *NotificationSpec. Rewrote the RED test to the *NotificationSpec signature AND added a profile-level merge() integration test proving the merger is wired into the inheritance entry point (preserves the original test intent)."
  - "Migrated 5 sibling test files (not just the RED stub) — Rule 3 blocking: stripping 14 CLISpec fields breaks their compile; they had to move to the notification block for pkg/profile to test-green."

patterns-established:
  - "Wave-2 verification gate is package-scoped (go test ./pkg/profile + go vet) not repo-wide; the compiler/cmd build is intentionally RED between Wave 2 and Wave 3"

requirements-completed: []

# Metrics
duration: 12min
completed: 2026-05-31
---

# Phase 92 Plan 02: Notification Types + Schema + Validator + Inherit Summary

**Landed the structured `spec.notification:` block (events/email/slack + inbound/transcript/invites) and `spec.runtime.vscode.enabled` in types + JSON schema, stripped 14 `cli.notify*` fields + `cli.vscodeEnabled` from CLISpec, and fixed the pointer-merge inheritance bug with a typed `mergeNotificationSpec` deep-merger (VC-7 GREEN) — `pkg/profile` is internally self-consistent while the compiler/CLI boundary is intentionally RED for Wave 3.**

## Performance
- **Duration:** ~12 min
- **Started:** 2026-05-31T21:39:12Z
- **Completed:** 2026-05-31T21:51:01Z
- **Tasks:** 3
- **Files changed:** 11 across 3 commits

## Accomplishments

### Task 1 — types + schema (`aeebeba6`)
- **7 new types in `pkg/profile/types.go`** (after CLISpec, before `IsVSCodeEnabled`): `NotificationSpec`, `NotificationEventsSpec`, `NotificationEmailSpec`, `NotificationSlackSpec`, `NotificationSlackInboundSpec`, `NotificationSlackTranscriptSpec`, `NotificationSlackInvitesSpec`, plus `RuntimeVSCodeSpec`.
- `Spec.Notification *NotificationSpec` added; `RuntimeSpec.VSCode *RuntimeVSCodeSpec` added.
- **14 CLISpec fields removed:** `NotifyOnPermission`, `NotifyOnIdle`, `NotifyCooldownSeconds`, `NotificationEmailAddress`, `NotifyEmailEnabled`, `NotifySlackEnabled`, `NotifySlackPerSandbox`, `NotifySlackChannelOverride`, `SlackArchiveOnDestroy`, `NotifySlackInboundEnabled`, `NotifySlackInboundMentionOnly`, `NotifySlackTranscriptEnabled`, `NotifySlackInviteEmails`, `UseSlackConnect` — plus `VSCodeEnabled` (15th, re-homed to runtime). Surviving CLISpec fields: `NoBedrock`, `Agent`, `ClaudeArgs`, `CodexArgs`, and `NotifySlackInboundReactAlways` (deviation — see below).
- `IsVSCodeEnabled` re-signed `func(*CLISpec) bool` → `func(*RuntimeVSCodeSpec) bool`.
- **Schema** (`schemas/sandbox_profile.schema.json`): added `properties.spec.properties.notification` (object, `additionalProperties:false`, all sub-blocks optional, `slack.channelOverride` keeps the `^C[A-Z0-9]+$` pattern, email fields use `format:email`); added `properties.spec.properties.runtime.properties.vscode.enabled`; removed 14 `cli.*` properties. Valid JSON confirmed via `node -e JSON.parse`.

### Task 2 — typed inheritance merge (`e476534e`)
- **`mergeNotificationSpec(parent, child *NotificationSpec) *NotificationSpec`** + 6 sub-mergers (events/email/slack/inbound/transcript/invites) + `pickBoolPtr`/`pickIntPtr`/`pickString` helpers in `inherit.go`.
- `merge()` now overwrites `result.Spec.Notification = mergeNotificationSpec(parent, child)` after the bulk `result.Spec = child.Spec` copy, replacing the broken whole-section pointer-replace.
- **The bug fixed (VC-7):** pre-Phase-92 a child profile setting any single notify field replaced the entire parent `Spec.CLI` pointer, dropping all other parent notify settings. Now a child that flips only `slack.transcript.enabled` still inherits the parent's `slack.perSandbox`.
- `inherit_notification_test.go`: removed the `phase92_wave2` build tag; rewrote the RED stub to the `*NotificationSpec` signature; added 6 cases incl. a profile-level `merge()` wiring test and invites child-wins / parent-keep cases. All GREEN.

### Task 3 — validator rewire (`eb4d553c`)
- `validate.go`: the Phase 63/67/68/72 Slack block (gate changed from `if p.Spec.CLI != nil` to `if p.Spec.Notification != nil && .Slack != nil`) now reads `notification.slack.*`, `notification.events.*`, `notification.email.*` with nil-safe chained guards. All `ValidationError.Path` + message text updated to the new dotted paths. Rules S1–S5, SI1–SI3, ST1–ST3, SE1–SE2 preserved.
- Migrated 5 sibling test files off the removed CLISpec fields onto the notification block: `validate_test.go`, `validate_slack_inbound_test.go`, `validate_slack_transcript_test.go`, `validate_slack_invite_emails_test.go`, `profile_cli_mention_test.go`, `types_test.go` (added `intPtr`, `minimalNotificationProfileYAML`, `minimalNotificationWith`).

## Verification (Wave-2 gate)
- `go vet ./pkg/profile/...` — **clean.**
- `go test ./pkg/profile/... -count=1` — **GREEN** (incl. `TestInheritNotificationSpec_ChildOnlyTranscriptInheritsParentPerSandbox` = VC-7).
- `node -e "JSON.parse(...)"` — schema is **valid JSON.**
- `grep 'CLI\.Notify\|CLI\.Slack\|CLI\.VSCodeEnabled\|CLI\.NotificationEmailAddress\|CLI\.UseSlackConnect'` over non-test `pkg/profile/*.go` — **ZERO matches.**
- Repo-wide `go build ./...` — **RED at `pkg/compiler/userdata.go`** (cli.NotifySlack* / NotifyOn* / etc. undefined) and downstream `internal/app/cmd` — **expected and by design.**

## Wave 3 handoff note
**Full `go build ./...` is RED until Wave 3 lands the compiler + CLI cmd updates.** Wave 3 must:
- Re-point `pkg/compiler/userdata.go` (notify env emission, `resolveReactAlways`, VSCode userdata gate) and `pkg/compiler/service_hcl.go` from `p.Spec.CLI.Notify*`/`VSCodeEnabled` to `p.Spec.Notification.*` / `p.Spec.Runtime.VSCode`.
- Re-point `internal/app/cmd/*` (create.go:620/2060, vscode.go:422, create_slack*.go, destroy_slack.go, doctor.go) and update `IsVSCodeEnabled` call sites to pass `p.Spec.Runtime.VSCode`.
- Rewrite the profile YAMLs (`cli.notify*` → `notification.*`) — after Wave 2, `km validate` on post-Wave-1 YAMLs FAILS (schema no longer accepts `cli.notify*`). This is the gap Wave 3 closes; do NOT release between waves.
- Restore the pkg/compiler byte-identity goldens (cannot run while compiler is build-broken).
- **Resolve the `notifySlackInboundReactAlways` re-home** (see deviation 1) — either add `reactAlways` to `NotificationSlackInboundSpec` or confirm it stays on CLISpec.

## Deviations from Plan

### Auto-fixed / scoped decisions

**1. [Rule 3 — Blocking / scope] Kept `cli.notifySlackInboundReactAlways` in CLISpec (15th notify field not in the plan's removal list)**
- **Found during:** Task 1 — CLISpec audit.
- **Issue:** The plan enumerates exactly 14 fields to remove and asserts only `NoBedrock/Agent/ClaudeArgs/CodexArgs` survive, but a 15th notify field — `NotifySlackInboundReactAlways` (Phase 91.4/91.5) — exists, is read by `pkg/compiler/userdata.go:resolveReactAlways`, and has NO slot in the plan's target `NotificationSlackInboundSpec` (which defines only `enabled` + `mentionOnly`).
- **Fix:** Left the field on CLISpec (and its schema property) untouched so the compiler reader and its round-trip test keep working. Did not invent an unplanned `reactAlways` schema field.
- **Files:** none beyond not-deleting; flagged for Wave 3.
- **Commit:** aeebeba6.

**2. [Rule 3 — Blocking] Rewrote the Wave-0 RED test to the planned typed signature**
- **Found during:** Task 2.
- **Issue:** The Wave-0 stub called `mergeNotificationSpec(parent, child)` with `*SandboxProfile` args and read `merged.Spec.Notification`, but the plan's `<interfaces>` defines `mergeNotificationSpec(parent, child *NotificationSpec) *NotificationSpec`. Incompatible signatures.
- **Fix:** Adopted the plan's `*NotificationSpec` signature (the canonical one — it's what `merge()` consumes), rewrote the RED test to it, and added a separate profile-level `merge()` integration test to preserve the original test's "inheritance is actually wired" intent.
- **Commit:** e476534e.

**3. [Rule 3 — Blocking] Migrated 5 sibling test files the plan only partially listed**
- **Found during:** Task 3 (`go test` compile).
- **Issue:** The plan's file list named `validate.go` + the test stub, but stripping 14 CLISpec fields breaks compile in `validate_test.go`, `validate_slack_inbound_test.go`, `validate_slack_transcript_test.go`, `validate_slack_invite_emails_test.go`, `profile_cli_mention_test.go`, and `types_test.go` (which construct `&CLISpec{NotifySlack...}` / inject `cli.notify*` YAML).
- **Fix:** Migrated each to the notification block (field construction + YAML paths + error-message substrings); added `intPtr` + `minimalNotificationProfileYAML` + `minimalNotificationWith` helpers. The plan's validator files (`slack_validate.go`/`transcript_validate.go`/`invite_validate.go`) do NOT exist — all validators live in `validate.go`, so the rewire was a single-file change.
- **Commit:** eb4d553c.

**Total deviations:** 3 (all Rule 3 — directly caused by this wave's field removal / Wave-0 contract). No Rule 4 (architectural) triggers.
**Impact on plan:** All plan must-haves met. The notifySlackInboundReactAlways carry-over is the one open item handed to Wave 3.

## Self-Check: PASSED
