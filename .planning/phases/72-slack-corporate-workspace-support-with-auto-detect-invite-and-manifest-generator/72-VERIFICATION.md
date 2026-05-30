---
phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator
verified: 2026-05-30T00:00:00Z
status: passed
score: 8/8 must-haves verified
re_verification: false
human_verification:
  - test: "B6 — km doctor scope-drift check with real Slack token rotation"
    expected: "km doctor shows WARN on missing users:read.email; OK after restore"
    why_human: "Requires destructive scope removal from live Slack Admin and bot token rotation; unit tests (TestDoctor_SlackUsersReadEmailScope_Pass/Warn) already cover the logic path. UAT explicitly deferred B6 with operator sign-off KPH 2026-05-30."
---

# Phase 72: Slack Corporate Workspace Support — Verification Report

**Phase Goal:** Add Slack corporate-workspace support so `km` works against Slack workspaces where the operator and additional folks may be native workspace members (not just external Slack Connect guests). Three operator-facing surfaces: `km slack manifest`, `km slack invite <email>`, and profile-driven auto-invite via `cli.notifySlackInviteEmails` / `cli.useSlackConnect`. Plus refactor `km slack init --invite-email` to share the same orchestrator.
**Verified:** 2026-05-30
**Status:** passed
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `LookupUserByEmail` + `InviteUserToChannel` + `InviteUserToChannelStrict` + `ErrAlreadyInChannel` exist on `*slack.Client` with form-encoded routing | VERIFIED | `pkg/slack/client.go` lines 459–556; `callForm` at line 116; form-encoded routing confirmed by `TestClient_LookupUserByEmail_Found` asserting `Content-Type: application/x-www-form-urlencoded` |
| 2 | `EnsureMemberByEmail` orchestrator exists with all 5 result values and `EnsureMemberOpts` (ForceExternal/Interactive/AutoConnect/DryRun) + Prompter/InviteAPI interfaces | VERIFIED | `pkg/slack/invite.go` lines 24–127; all 5 enum values (InvitedDirect, InvitedConnect, AlreadyMember, SkippedExternal, Failed) declared |
| 3 | `km slack invite` cmd exists with channel resolution, SSM default, --dry-run, exit codes | VERIFIED | `internal/app/cmd/slack_invite.go`; channelIDPattern regex, FindChannelByName, SSM fallback, DryRun path, ExitCodeError for code 2 (SkippedExternal); registered at `slack.go:135` |
| 4 | `km slack init --invite-email` delegates to `EnsureMemberByEmail` orchestrator | VERIFIED | `internal/app/cmd/slack.go` lines 215–316; direct `InviteShared` replaced with `EnsureMemberByEmail` call at line 308; lazy-init of `slackClient` at lines 222–226 |
| 5 | `km slack manifest` renders 13-scope manifest with `users:read.email` + `files:read`; golden fixture exists | VERIFIED | `internal/app/cmd/slack_manifest.go` + `slack_manifest_template.json`; golden at `testdata/slack_manifest_golden.json`; 13 scopes confirmed by `python3 -c` parse; registered at `slack.go:134` |
| 6 | Profile schema: `NotifySlackInviteEmails []string` + `UseSlackConnect *bool` in types.go + JSON schema mirror + SE1/SE2 validation rules | VERIFIED | `pkg/profile/types.go` lines 495–521; schema at `schemas/sandbox_profile.schema.json` lines 593–599; `validate.go` SE1 at line 414, SE2 at line 421 |
| 7 | `km create` operator invite uses EnsureMemberByEmail with AutoConnect=true unconditionally; additional-folks loop respects UseSlackConnect gating; fail-soft | VERIFIED | `internal/app/cmd/create_slack.go` lines 205–264; operator path AutoConnect=true at line 215; loop AutoConnect derived from `cli.UseSlackConnect == nil \|\| *cli.UseSlackConnect` at line 223; warn-not-abort at line 264 |
| 8 | `km doctor` `checkSlackUsersReadEmailScope` check exists and is registered in buildChecks | VERIFIED | `internal/app/cmd/doctor_slack_transcript.go` lines 324–374; registered in `doctor.go` at line 2974 |

**Score:** 8/8 truths verified

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/slack/client.go` | LookupUserByEmail, InviteUserToChannel, InviteUserToChannelStrict, ErrAlreadyInChannel, callForm | VERIFIED | All present; callForm at line 116 routes `LookupUserByEmail` as form-encoded |
| `pkg/slack/client_lookup_test.go` | Unit tests for lookup happy/miss/scope-error + form-encoded assertion | VERIFIED | Tests pass; Content-Type + body assertions at lines 27–33 |
| `pkg/slack/client_invite_test.go` | Unit tests for invite happy/idempotent/errors | VERIFIED | Tests pass |
| `pkg/slack/invite.go` | EnsureMemberByEmail orchestrator with all opts/result paths | VERIFIED | Full decision tree documented and tested |
| `pkg/slack/invite_test.go` | All 10 EnsureMemberByEmail behavior paths tested | VERIFIED | Tests pass |
| `internal/app/cmd/slack_invite.go` | km slack invite cobra cmd with RunSlackInvite | VERIFIED | Exists; lazy-init at line 101; all flag paths wired |
| `internal/app/cmd/slack_invite_test.go` | Cmd-level tests incl. nil-Slack regression (6cf1deb) | VERIFIED | TestSlackInvite_NoPreSetSlackAPI_NoBotTokenInSSM at line 99 |
| `internal/app/cmd/slack_manifest.go` | km slack manifest cobra cmd with RenderSlackManifest | VERIFIED | go:embed at line 24; RenderSlackManifest at line 44 |
| `internal/app/cmd/slack_manifest_template.json` | Embedded 13-scope template with {{.AppName}}/{{.EventsURL}} | VERIFIED | files:read and users:read.email present; template placeholders wired |
| `internal/app/cmd/testdata/slack_manifest_golden.json` | Golden fixture for deterministic render test | VERIFIED | Valid JSON; 13 scopes confirmed |
| `internal/app/cmd/slack_manifest_test.go` | Golden-file render comparison | VERIFIED | Tests pass |
| `pkg/profile/types.go` | NotifySlackInviteEmails + UseSlackConnect fields | VERIFIED | Lines 507, 521 |
| `pkg/profile/schemas/sandbox_profile.schema.json` | Mirror fields in JSON schema | VERIFIED | Lines 593–599 |
| `pkg/profile/validate.go` | SE1 + SE2 validation rules | VERIFIED | Lines 414–430 |
| `pkg/profile/validate_slack_invite_emails_test.go` | Profile field validation tests | VERIFIED | Tests pass |
| `internal/app/cmd/create_slack.go` | Invite loop with EnsureMemberByEmail + UseSlackConnect gating | VERIFIED | Lines 205–264 |
| `internal/app/cmd/create_slack_invite_test.go` | Fail-soft behavior tests | VERIFIED | Tests pass |
| `internal/app/cmd/doctor_slack_transcript.go` | checkSlackUsersReadEmailScope function | VERIFIED | Lines 324–374 |
| `internal/app/cmd/doctor_slack_users_email_test.go` | Pass/Warn/Skip/Error test cases | VERIFIED | 4 tests pass |
| `internal/app/cmd/doctor.go` | Doctor check registration in buildChecks | VERIFIED | Line 2974 |
| `pkg/slack/client_test.go` | TestClient_AuthTest_RealShape regression (ec13e5b) | VERIFIED | Line 69 |
| `docs/slack-notifications.md` | Phase 72 docs updated with km slack manifest + notifySlackInviteEmails | VERIFIED | Both grep hits confirmed |
| `CLAUDE.md` | CLI reference updated with km slack manifest + notifySlackInviteEmails | VERIFIED | Both grep hits confirmed |
| `72-UAT.md` | UAT sign-off file with 7/8 rows passed | VERIFIED | status: passed; KPH 2026-05-30 |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `slack_invite.go:RunSlackInvite` | `invite.go:EnsureMemberByEmail` | direct call at line 129 | WIRED | Lazy-init guards d.Slack == nil before call at line 101 |
| `slack.go:RunSlackInit` | `invite.go:EnsureMemberByEmail` | call at line 308 with full-capability client | WIRED | slackClient constructed from validated token at line 222 when d.Slack == nil |
| `create_slack.go` operator path | `invite.go:EnsureMemberByEmail` | line 213 with AutoConnect=true | WIRED | Unconditional; not gated by UseSlackConnect |
| `create_slack.go` additional-folks loop | `invite.go:EnsureMemberByEmail` | loop at line 224 with AutoConnect derived from UseSlackConnect | WIRED | `autoConnect := cli.UseSlackConnect == nil \|\| *cli.UseSlackConnect` at line 223 |
| `slack_manifest.go` | `slack_manifest_template.json` | `//go:embed slack_manifest_template.json` at line 24 | WIRED | Template embedded at compile time |
| `slack.go:newSlackCmd` | `slack_manifest.go:newSlackManifestCmd` | `slackCmd.AddCommand` at line 134 | WIRED | |
| `slack.go:newSlackCmd` | `slack_invite.go:newSlackInviteCmd` | `slackCmd.AddCommand` at line 135 | WIRED | |
| `doctor.go:buildChecks` | `doctor_slack_transcript.go:checkSlackUsersReadEmailScope` | call at line 2974 | WIRED | |
| `client.go:LookupUserByEmail` | `callForm` | call at line 477 | WIRED | Form-encoded body + Content-Type header; regression test asserts wire format |

---

### Requirements Coverage

| Requirement | Plans | Description | Status | Evidence |
|-------------|-------|-------------|--------|----------|
| VALIDATION-Layer-1 | 72-01 | Slack client primitives (LookupUserByEmail, InviteUserToChannel, InviteUserToChannelStrict, ErrAlreadyInChannel, form-encoded callForm) | SATISFIED | pkg/slack/client.go; all methods present; form-encoded routing verified by TestClient_LookupUserByEmail_Found |
| VALIDATION-Layer-2 | 72-04 | EnsureMemberByEmail orchestrator with full opts/result paths | SATISFIED | pkg/slack/invite.go; 5 result values; 10 behavior paths tested in invite_test.go |
| VALIDATION-Layer-3 | 72-05 | km slack invite cobra command | SATISFIED | slack_invite.go; all flag paths + channel resolution + lazy-init + exit codes |
| VALIDATION-Layer-4 | 72-06 | km slack init refactor to orchestrator | SATISFIED | slack.go lines 215–316; EnsureMemberByEmail replaces direct InviteShared |
| VALIDATION-Layer-5 | 72-03 | km slack manifest command + 13-scope template + golden fixture | SATISFIED | slack_manifest.go + slack_manifest_template.json + testdata/slack_manifest_golden.json |
| VALIDATION-Layer-6 | 72-02 | Profile schema: NotifySlackInviteEmails + UseSlackConnect + SE1/SE2 rules | SATISFIED | types.go + schemas/sandbox_profile.schema.json + validate.go |
| VALIDATION-Layer-7 | 72-07 | km create invite loop (operator unconditional + additional-folks UseSlackConnect-gated, fail-soft) | SATISFIED | create_slack.go lines 200–264; create_slack_invite_test.go |
| VALIDATION-Layer-8 | 72-08 | km doctor slack_users_read_email_scope check | SATISFIED | doctor_slack_transcript.go:324; doctor.go:2974; 4 unit tests pass |

All 8 VALIDATION-Layer requirements satisfied. No orphaned requirements.

---

### Regression Tests Verified

| Commit | Test | File | Status |
|--------|------|------|--------|
| ec13e5b | TestClient_AuthTest_RealShape — auth.test JSON "user":string shape | `pkg/slack/client_test.go:69` | VERIFIED |
| 6cf1deb | TestSlackInvite_NoPreSetSlackAPI_NoBotTokenInSSM — nil d.Slack production deps | `internal/app/cmd/slack_invite_test.go:99` | VERIFIED |
| 2653bc3 | TestClient_LookupUserByEmail_Found — form-encoded Content-Type + body assertions | `pkg/slack/client_lookup_test.go:20` | VERIFIED |

---

### Anti-Patterns Found

None. No TODO/FIXME/placeholder stubs found in Phase 72 files. No empty implementations. No console.log-only handlers. All key wiring points use substantive implementations.

---

### Human Verification Required

#### 1. B6 — km doctor scope-drift live check

**Test:** Remove `users:read.email` from the installed Slack app (Slack Admin), reinstall the app, rotate the bot token with `km slack rotate-token`, then run `km doctor`. Confirm WARN on `slack_users_read_email_scope`. Restore the scope, reinstall, rotate again, confirm OK.
**Expected:** `km doctor` shows WARN when scope missing; OK when scope present.
**Why human:** Requires a destructive live Slack Admin operation (removing a bot scope from an installed app) and a real token rotation. UAT operator (KPH) deferred this scenario with justification that `TestDoctor_SlackUsersReadEmailScope_Pass` and `TestDoctor_SlackUsersReadEmailScope_Warn` unit tests already exercise the check logic with mock scope responses, covering the decision path without the destructive cycle.

---

### Test Suite Results

Full Phase 72 surface test run executed 2026-05-30:

```
go test -run 'TestClient_LookupUserByEmail|TestClient_InviteUserToChannel|TestEnsureMemberByEmail_|...' ./... -count=1
ok  github.com/whereiskurt/klanker-maker/internal/app/cmd   0.794s
ok  github.com/whereiskurt/klanker-maker/pkg/profile        0.266s
ok  github.com/whereiskurt/klanker-maker/pkg/slack          0.343s
```

All three packages green. Pre-existing `pkg/compiler` test failures are unrelated to Phase 72 (confirmed: same failures present at pre-Phase-72 commit f0cd289; zero references to Phase 72 fields in pkg/compiler).

---

### Live UAT Outcome (72-UAT.md)

7/8 UAT rows passed with operator sign-off KPH 2026-05-30:
- B0 (install ordering), B1 (manifest renders), B2 (init + doctor), B3 (dry-run classify), B4 (native invite), B5 (full km create — all four orchestrator quadrants exercised in one create) all PASS.
- B6 (doctor scope-drift) DEFERRED with unit-test justification — not a gap, operator chose to skip the destructive scope-removal cycle.

---

## Gaps Summary

No gaps. All 8 layers implemented, all artifacts substantive and wired, all key links connected, all regression tests in place, test suite green.

---

_Verified: 2026-05-30_
_Verifier: Claude (gsd-verifier)_
