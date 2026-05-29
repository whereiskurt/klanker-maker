---
phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator
plan: "00"
subsystem: testing
tags: [slack, tdd, wave0, stubs, manifest, json-template]

# Dependency graph
requires: []
provides:
  - "9 Wave 0 stub test files with 39 named SKIP tests for Phase 72 behaviors"
  - "slack_manifest_template.json — embedded JSON template with {{.AppName}} and {{.EventsURL}} placeholders"
  - "testdata/slack_manifest_golden.json — rendered golden fixture for AppName=KlankerMaker-test, EventsURL=https://example.lambda-url.us-east-1.on.aws/events"
affects:
  - "72-01 (Wave 1): flip LookupUserByEmail + InviteUserToChannel stubs to assertions"
  - "72-02 (Wave 1): flip notifySlackInviteEmails/useSlackConnect profile stubs"
  - "72-03 (Wave 1): implement RenderSlackManifest and flip manifest stubs"
  - "72-04 (Wave 2): implement EnsureMemberByEmail and flip orchestrator stubs"
  - "72-05 (Wave 3): implement km slack invite cobra command and flip invite stubs"
  - "72-07 (Wave 3): extend km create operator-invite refactor and flip create stubs"
  - "72-08 (Wave 4): add km doctor scope check and flip doctor stubs"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Wave 0 TDD stub seeding: pure t.Skip stubs with no symbol references to non-existent production code"
    - "text/template Go template delims {{}} are safe in JSON since single { } does not trigger template parsing"
    - "golden fixture pattern: rendered template output committed as testdata/ file for byte-exact comparison in Wave 1"

key-files:
  created:
    - "pkg/slack/client_lookup_test.go"
    - "pkg/slack/client_invite_test.go"
    - "pkg/slack/invite_test.go"
    - "internal/app/cmd/slack_invite_test.go"
    - "internal/app/cmd/slack_manifest_test.go"
    - "internal/app/cmd/slack_manifest_template.json"
    - "internal/app/cmd/testdata/slack_manifest_golden.json"
    - "internal/app/cmd/create_slack_invite_test.go"
    - "internal/app/cmd/doctor_slack_users_email_test.go"
    - "pkg/profile/validate_slack_invite_emails_test.go"
  modified: []

key-decisions:
  - "Wave 0 stubs contain ONLY t.Skip with no references to non-existent symbols — avoids go vet/compile failures while still naming all test targets"
  - "Manifest template uses exact 13-scope list: chat:write, channels:manage, channels:join, channels:read, channels:history, groups:write, groups:history, conversations.connect:write, reactions:read, reactions:write, files:write, files:read, users:read.email — adds files:read (Phase 75 inbound) and users:read.email (Phase 72) vs reference manifest"
  - "Golden fixture rendered via Go text/template for AppName=KlankerMaker-test, EventsURL=https://example.lambda-url.us-east-1.on.aws/events — Wave 1 asserts byte-exact match"
  - "create_slack_invite_test.go uses package cmd (internal) to match existing create_slack_test.go pattern"
  - "doctor_slack_users_email_test.go uses package cmd (internal) to match existing doctor_slack_transcript_test.go pattern"
  - "slack_manifest_test.go and slack_invite_test.go use package cmd_test (external) since they reference exported cmd symbols"

patterns-established:
  - "TDD Wave 0: seed named failing tests before any production code to lock the behavioral contract"
  - "Template validation: strip {{.X}} placeholders before JSON.parse to verify template is structurally valid JSON"

requirements-completed:
  - VALIDATION-Layer-1
  - VALIDATION-Layer-2
  - VALIDATION-Layer-3
  - VALIDATION-Layer-4
  - VALIDATION-Layer-5
  - VALIDATION-Layer-6
  - VALIDATION-Layer-7
  - VALIDATION-Layer-8

# Metrics
duration: 11min
completed: 2026-05-29
---

# Phase 72 Plan 00: Wave 0 Stub Seeding Summary

**39 named SKIP tests across 9 stub files + manifest JSON template + golden fixture establish the Phase 72 validation contract before any production code lands**

## Performance

- **Duration:** 11 min
- **Started:** 2026-05-29T18:29:59Z
- **Completed:** 2026-05-29T18:40:51Z
- **Tasks:** 3
- **Files modified:** 10 created, 0 modified

## Accomplishments

- Created 9 Wave 0 stub test files with 39 named SKIP tests spanning all Phase 72 behaviors (Layers 1-8)
- Created the embedded manifest JSON template with Go text/template placeholders for AppName and EventsURL, 13-scope bot list (adds files:read + users:read.email vs reference manifest)
- Created the golden fixture for deterministic byte-exact manifest render assertion in Wave 1

## Task Commits

1. **Task 1: Seed Slack client + orchestrator test stubs** - `563ccd3` (test)
2. **Task 2: Seed cmd-level test stubs + manifest template + golden fixture** - `1ab0504` (test)
3. **Task 3: Seed profile validation test stub** - `c4bdce5` (test)

## Files Created/Modified

### pkg/slack/ (3 files)
- `pkg/slack/client_lookup_test.go` — 3 stubs for LookupUserByEmail (Found, NotFound, MissingScope); Wave 1 (72-01) flips them
- `pkg/slack/client_invite_test.go` — 4 stubs for InviteUserToChannel (OK, AlreadyMember, CantInviteSelf, NotInChannel); Wave 1 (72-01) flips them
- `pkg/slack/invite_test.go` — 9 stubs for EnsureMemberByEmail result paths (Direct, AlreadyMember, InvitedConnect, SkippedNonInteractive, AutoConnectNonInteractive, SkippedInteractiveNo, ForceExternal, FreeTierConnect, DryRun); Wave 2 (72-04) flips them

### internal/app/cmd/ (6 files)
- `internal/app/cmd/slack_invite_test.go` — 4 stubs for km slack invite cobra wiring (HappyPath, ExternalFlag, ChannelByName, DefaultChannelFromSSM); Wave 3 (72-05) flips them
- `internal/app/cmd/slack_manifest_test.go` — 4 stubs for km slack manifest golden render (Golden, AppNameOverride, BridgeURLFromSSM, ScopesIncludeUsersReadEmail); Wave 1 (72-03) flips them
- `internal/app/cmd/slack_manifest_template.json` — embedded JSON template with {{.AppName}} / {{.EventsURL}} placeholders; valid JSON after placeholder substitution; 13-scope bot list
- `internal/app/cmd/testdata/slack_manifest_golden.json` — golden fixture for inputs AppName=KlankerMaker-test, EventsURL=https://example.lambda-url.us-east-1.on.aws/events; valid JSON
- `internal/app/cmd/create_slack_invite_test.go` — 8 stubs for km create operator-invite refactor + additional-folks loop (all Layer 7 behaviors); Wave 3 (72-07) flips them
- `internal/app/cmd/doctor_slack_users_email_test.go` — 2 stubs for km doctor slack_users_read_email_scope check (Pass, Warn); Wave 4 (72-08) flips them

### pkg/profile/ (1 file)
- `pkg/profile/validate_slack_invite_emails_test.go` — 5 stubs for notifySlackInviteEmails/useSlackConnect validation (Parse_NotifySlackInviteEmails, Parse_UseSlackConnect, Validate_InviteEmails_RequiresSlackEnabled, Validate_InviteEmails_InvalidEmail, Schema_InviteEmails); Wave 1 (72-02) flips them

## Manifest Template Schema Decisions

- **Template file:** `internal/app/cmd/slack_manifest_template.json`
- **Placeholders:** `{{.AppName}}` (display_information.name + features.bot_user.display_name) and `{{.EventsURL}}` (settings.event_subscriptions.request_url)
- **Literal fields:** description, background_color, always_online=false, all bot_events, all 5 boolean settings flags
- **Scope list (13 total):** chat:write, channels:manage, channels:join, channels:read, channels:history, groups:write, groups:history, conversations.connect:write, reactions:read, reactions:write, files:write, files:read, users:read.email
  - `files:read` added vs reference manifest (required by Phase 75 inbound; doctor check verifies it)
  - `users:read.email` added (Phase 72 LookupUserByEmail)

## Golden Fixture Inputs

| Field | Value |
|-------|-------|
| AppName | `KlankerMaker-test` |
| EventsURL | `https://example.lambda-url.us-east-1.on.aws/events` |

Plan 72-03 (Wave 1) asserts `cmd.RenderSlackManifest()` output against this file byte-for-byte.

## Tests to Flip Per Plan (Wave 1+ targets)

| Plan | Wave | Test Functions to Flip |
|------|------|------------------------|
| 72-01 | 1 | TestClient_LookupUserByEmail_Found, TestClient_LookupUserByEmail_NotFound, TestClient_LookupUserByEmail_MissingScope, TestClient_InviteUserToChannel_OK, TestClient_InviteUserToChannel_AlreadyMember, TestClient_InviteUserToChannel_CantInviteSelf, TestClient_InviteUserToChannel_NotInChannel |
| 72-02 | 1 | TestParse_NotifySlackInviteEmails, TestParse_UseSlackConnect, TestValidate_InviteEmails_RequiresSlackEnabled, TestValidate_InviteEmails_InvalidEmail, TestSchema_InviteEmails |
| 72-03 | 1 | TestSlackManifest_Golden, TestSlackManifest_AppNameOverride, TestSlackManifest_BridgeURLFromSSM, TestSlackManifest_ScopesIncludeUsersReadEmail |
| 72-04 | 2 | TestEnsureMemberByEmail_Direct, TestEnsureMemberByEmail_AlreadyMember, TestEnsureMemberByEmail_InvitedConnect, TestEnsureMemberByEmail_SkippedNonInteractive, TestEnsureMemberByEmail_AutoConnectNonInteractive, TestEnsureMemberByEmail_SkippedInteractiveNo, TestEnsureMemberByEmail_ForceExternal, TestEnsureMemberByEmail_FreeTierConnect, TestEnsureMemberByEmail_DryRun |
| 72-05 | 3 | TestSlackInvite_HappyPath, TestSlackInvite_ExternalFlag, TestSlackInvite_ChannelByName, TestSlackInvite_DefaultChannelFromSSM |
| 72-07 | 3 | TestCreateSlack_OperatorInvite_NativeMember, TestCreateSlack_OperatorInvite_ExternalConnect, TestCreateSlack_OperatorInvite_MissingEmail, TestCreateSlack_InvitesEmails, TestCreateSlack_AutoConnectsExternalWhenEnabled, TestCreateSlack_SkipsExternalWhenConnectDisabled, TestCreateSlack_WarnsOnInviteFailure, TestCreateSlack_EmptyInviteList |
| 72-08 | 4 | TestDoctor_SlackUsersReadEmailScope_Pass, TestDoctor_SlackUsersReadEmailScope_Warn |

## Decisions Made

- Wave 0 stubs contain only `t.Skip` with no references to non-existent symbols. This avoids go vet/compile failures while still naming all test targets for Wave 1+ to hit.
- Manifest template scopes list adds both `files:read` (Phase 75 inbound requirement already in km doctor) and `users:read.email` (Phase 72 addition) versus the reference manifest at `/Users/khundeck/Downloads/km-personal.json`.
- Golden fixture was rendered by a small Go program to guarantee it matches text/template output exactly.
- `create_slack_invite_test.go` and `doctor_slack_users_email_test.go` use `package cmd` (internal) to match adjacent test files. `slack_manifest_test.go` and `slack_invite_test.go` use `package cmd_test` to match the external test pattern in slack_test.go.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Removed symbol references from client_lookup_test.go and client_invite_test.go**
- **Found during:** Task 1 verification
- **Issue:** Initial drafts included `c.LookupUserByEmail()` and `c.InviteUserToChannel()` calls after `t.Skip`. Go compiler resolves all symbols regardless of t.Skip, failing go vet.
- **Fix:** Removed all non-existent method calls; kept only `t.Skip` + documentation comments describing what Wave 1 assertions will do.
- **Files modified:** pkg/slack/client_lookup_test.go, pkg/slack/client_invite_test.go
- **Verification:** go vet ./pkg/slack/... passes; 16 tests show SKIP
- **Committed in:** 563ccd3 (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - Bug)
**Impact on plan:** Necessary for Wave 0 contract: stub tests must compile. Using comment-only references is the correct pattern for Wave 0 TDD stubs targeting non-existent symbols.

## Issues Encountered

None beyond the symbol-reference compile issue (addressed above).

## Next Phase Readiness

- Wave 1 plans (72-01, 72-02, 72-03) can now proceed in parallel — each has a clear set of named failing tests to turn green.
- Wave 2 plan (72-04) has 9 named orchestrator test targets.
- Wave 3 plans (72-05, 72-07) have 12 named command-level test targets.
- Wave 4 plan (72-08) has 2 named doctor check test targets.
- The manifest template is ready for `//go:embed` in Plan 72-03; the golden fixture is committed and ready for byte-exact assertion.

---
*Phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator*
*Completed: 2026-05-29*
