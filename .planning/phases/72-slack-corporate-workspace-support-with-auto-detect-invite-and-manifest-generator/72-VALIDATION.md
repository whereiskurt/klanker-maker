---
phase: 72
slug: slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-05-06
---

# Phase 72 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` (stdlib testing + httptest mocks) |
| **Config file** | None — uses module-level `go.mod`; per-test fixtures co-located |
| **Quick run command** | `go test ./pkg/slack/... ./internal/app/cmd/... ./pkg/profile/... -count=1 -run TestPhase72` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~6s targeted; ~90s full suite |

---

## Sampling Rate

- **After every task commit:** Run the targeted quick command for the package(s) touched (e.g., `go test ./pkg/slack/... -count=1`).
- **After every plan wave:** Run the cross-package quick command above.
- **Before `/gsd:verify-work`:** Full suite must be green; `make build` must succeed (memory: feedback_rebuild_km — every CLI edit must be rebuilt with ldflags).
- **Max feedback latency:** ~10 seconds (single-package targeted runs).

---

## Per-Task Verification Map

*Populated by the planner as PLAN.md tasks are written. Each task lands one row; the test type matches the layer the task touches.*

| Task ID | Plan | Wave | Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|----------|-----------|-------------------|-------------|--------|
| 72-00-T1 | 00 | 0 | Slack client + orchestrator stub tests skip cleanly | unit | `go test -run 'TestClient_LookupUserByEmail\|TestClient_InviteUserToChannel\|TestEnsureMemberByEmail' ./pkg/slack/... -count=1` (expects SKIP) | ✅ | ⬜ |
| 72-00-T2 | 00 | 0 | Cmd-level + manifest stub tests skip cleanly | unit | `go test -run 'TestSlackInvite_\|TestSlackManifest_\|TestCreateSlack_\|TestDoctor_SlackUsersReadEmailScope_' ./internal/app/cmd/... -count=1` (expects SKIP) | ✅ | ⬜ |
| 72-00-T3 | 00 | 0 | Profile validation stub tests skip cleanly | unit | `go test -run 'TestParse_NotifySlackInviteEmails\|TestValidate_InviteEmails_\|TestSchema_InviteEmails' ./pkg/profile/... -count=1` (expects SKIP) | ✅ | ⬜ |
| 72-01-T1 | 01 | 1 | LookupUserByEmail returns hit/miss/scope correctly + lowercases email | unit | `go test -run 'TestClient_LookupUserByEmail' ./pkg/slack/... -count=1` | ✅ | ⬜ |
| 72-01-T2 | 01 | 1 | InviteUserToChannel idempotent on already_in_channel; surfaces other codes | unit | `go test -run 'TestClient_InviteUserToChannel' ./pkg/slack/... -count=1` | ✅ | ⬜ |
| 72-02-T1 | 02 | 1 | Profile field + JSON schema mirror added atomically | unit | `go test ./pkg/profile/... -count=1 && python3 -c "import json; json.load(open('pkg/profile/schemas/sandbox_profile.schema.json'))"` | ✅ | ⬜ |
| 72-02-T2 | 02 | 1 | Validation rules SE1+SE2; 4 tests turn green | unit | `go test -run 'TestParse_NotifySlackInviteEmails\|TestValidate_InviteEmails_\|TestSchema_InviteEmails' ./pkg/profile/... -count=1` | ✅ | ⬜ |
| 72-03-T1 | 03 | 1 | RenderSlackManifest + golden test + scope inclusion | unit | `go test -run 'TestSlackManifest_' ./internal/app/cmd/... -count=1` | ✅ | ⬜ |
| 72-03-T2 | 03 | 1 | newSlackManifestCmd registered under km slack | unit + smoke | `go test -run 'Slack' ./internal/app/cmd/... -count=1 && ./bin/km slack --help \| grep manifest` | ✅ | ⬜ |
| 72-04-T1 | 04 | 2 | ErrAlreadyInChannel sentinel + InviteUserToChannelStrict | unit | `go test -run 'TestClient_InviteUserToChannelStrict' ./pkg/slack/... -count=1` | ✅ | ⬜ |
| 72-04-T2 | 04 | 2 | EnsureMemberByEmail result paths (incl. non-interactive AutoConnect + DryRun classify) | unit | `go test -run 'TestEnsureMemberByEmail_' ./pkg/slack/... -count=1` | ✅ | ⬜ |
| 72-05-T1 | 05 | 3 | km slack invite cobra wiring + channel resolution + --dry-run (read-only probe) | unit | `go test -run 'TestSlackInvite_' ./internal/app/cmd/... -count=1` | ✅ | ⬜ |
| 72-06-T1 | 06 | 3 | RunSlackInit refactored to orchestrator + scope warning | unit | `go test -run 'TestSlackInit_' ./internal/app/cmd/... -count=1` | ✅ | ⬜ |
| 72-07-T1 | 07 | 3 | km create operator invite (orchestrator, AutoConnect=true) + additional-folks loop (useSlackConnect-gated, fail-soft) | unit | `go test -run 'TestCreateSlack_(OperatorInvite_NativeMember\|OperatorInvite_ExternalConnect\|OperatorInvite_MissingEmail\|InvitesEmails\|AutoConnectsExternalWhenEnabled\|SkipsExternalWhenConnectDisabled\|WarnsOnInviteFailure\|EmptyInviteList)' ./internal/app/cmd/... -count=1` | ✅ | ⬜ |
| 72-08-T1 | 08 | 4 | slack_users_read_email_scope doctor check | unit | `go test -run 'TestDoctor_SlackUsersReadEmailScope_' ./internal/app/cmd/... -count=1` | ✅ | ⬜ |
| 72-09-T1 | 09 | 4 | docs/slack-notifications.md + CLAUDE.md updated | docs | `grep -q 'km slack manifest' docs/slack-notifications.md CLAUDE.md && grep -q 'notifySlackInviteEmails' docs/slack-notifications.md CLAUDE.md` | ✅ | ⬜ |
| 72-09-T2 | 09 | 4 | 72-VALIDATION map populated + 72-UAT.md exists | meta | `test -s .planning/phases/72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator/72-UAT.md && grep -q 'nyquist_compliant: true' .planning/phases/72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator/72-VALIDATION.md` | ✅ | ⬜ |
| 72-09-T3 | 09 | 4 | Operator UAT — live new-account scenarios pass (Part B) | manual UAT | (operator follows 72-UAT.md Part B) | ✅ | ⬜ |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

### Coverage Targets (cross-task)

The planner MUST ensure at least one task covers each of the behaviors below. If a behavior has no
automated cover, it goes in **Manual-Only Verifications** with a UAT script.

**Layer 1 — Slack API client primitives (`pkg/slack/client.go`):**
- `LookupUserByEmail` returns `(userID, true, nil)` on hit.
- `LookupUserByEmail` returns `("", false, nil)` on `users_not_found`.
- `LookupUserByEmail` surfaces `missing_scope` (no `users:read.email`) as a typed error.
- `InviteUserToChannel` succeeds on Slack `ok=true`.
- `InviteUserToChannel` treats `already_in_channel` as success (idempotent).
- `InviteUserToChannel` surfaces `not_in_channel`, `cant_invite_self`, `user_is_restricted` as typed errors.

**Layer 2 — Orchestrator (`pkg/slack/invite.go`):**
- `EnsureMemberByEmail` returns `InvitedDirect` for an in-workspace user.
- Returns `AlreadyMember` when invite returns `already_in_channel`.
- Returns `InvitedConnect` when lookup misses, prompter approves, and Connect succeeds.
- Returns `SkippedExternal` when lookup misses, `Interactive=false`, and `AutoConnect=false`.
- Returns `InvitedConnect` when lookup misses, `Interactive=false`, and `AutoConnect=true` (non-interactive auto-fallback; no prompt consulted).
- Returns `SkippedExternal` when lookup misses, prompter declines, and `Interactive=true`.
- Returns `InvitedConnect` when `ForceExternal=true` (sends Connect directly, regardless of Interactive/AutoConnect).
- `Interactive=true` takes precedence over `AutoConnect` on a lookup miss.
- `DryRun=true` classifies without any write: hit→`InvitedDirect`, miss→`SkippedExternal`, `ForceExternal`→`InvitedConnect`; asserts zero invite/Connect calls and no prompter call.
- Wraps Connect errors so callers can surface the Pro-tier hint.

**Layer 3 — `km slack invite` cobra command (`internal/app/cmd/slack_invite.go`):**
- `--channel <name>` resolves via `FindChannelByName`.
- `--channel <C012ABCDE3F>` (ID format) is used directly without lookup.
- Default channel falls back to SSM `{prefix}/slack/shared-channel-id`.
- `--external` short-circuits to `inviteShared` without `users.lookupByEmail`.
- `--dry-run` does the read-only lookup, prints the classification (would-invite-native / would-Connect / not-a-member), performs NO JoinChannel/invite/Connect calls, and exits 0.
- Exit codes: `0` for `Invited*` / `AlreadyMember` / `--dry-run`; `1` for `Failed`; `2` for `SkippedExternal` (so scripts can distinguish).

**Layer 4 — `km slack init` refactor (`internal/app/cmd/slack.go`):**
- Existing PoC test path (operator email is external) still routes via the orchestrator and produces the same SSM writes + Slack Connect invite.
- New corporate path (operator email is in workspace) uses regular invite.

**Layer 5 — `km slack manifest` cobra command (`internal/app/cmd/slack_manifest.go`):**
- Renders embedded JSON template with deployment-specific `request_url` from SSM bridge URL.
- Substitutes `display_information.name` and `bot_user.display_name` from `--app-name` flag or `KlankerMaker-{resource_prefix}` default.
- Output is valid JSON parseable by `encoding/json`.
- Output includes the new `users:read.email` scope AND `files:read` (Phase 75 inbound requirement; the rendered scope set must be a superset of `km doctor`'s inbound `required` list).
- Output matches a golden fixture for a known input set (deterministic).
- Errors cleanly when bridge URL SSM key is missing (init not run).

**Layer 6 — Profile fields (`pkg/profile/...`):**
- `notifySlackInviteEmails: []` parses without error from YAML.
- Each entry is validated as RFC 5322 email (or "looks like" — match existing email pattern in codebase).
- Validation rejects non-empty list when `notifySlackEnabled=false` (Phase 67/68 cross-field pattern).
- `useSlackConnect: true|false` parses; omitting it leaves the `*bool` nil (resolved to true at create time).
- `useSlackConnect` adds NO reject rule — inert when the invite list is empty.
- Schema additions in `sandbox_profile.schema.json` validate both field shapes (`useSlackConnect`: boolean, default true).

**Layer 7 — `km create` per-sandbox invites (`internal/app/cmd/create_slack.go`):**
- The primary operator invite (SSM `{prefix}/slack/invite-email`) is routed through the
  orchestrator with `AutoConnect=true` (unconditional): native operator → `InvitedDirect`,
  external operator → `InvitedConnect`. The operator is ALWAYS invited.
- Missing/empty invite-email keeps the existing warn-and-skip path (no operator invite call).
- After the operator invite and `#sb-{id}` channel creation, the orchestrator runs once per
  `notifySlackInviteEmails` entry (additive — does not replace the operator invite).
- Additional internal users land via `InvitedDirect`; create succeeds.
- With `useSlackConnect` true/unset, additional external users (lookup miss) are auto-Connected
  (`InvitedConnect`) with NO warning.
- With `useSlackConnect: false`, additional external users emit a stderr warning containing the
  follow-up `km slack invite --external ... --channel sb-{id}` command but DO NOT fail the create.
- A Slack 5xx / Connect failure during any invite emits a warning and does not fail the create
  (fail-soft contract).
- `AutoConnect` for the additional-folks loop is derived as `cli.UseSlackConnect == nil || *cli.UseSlackConnect`.

**Layer 8 — `km doctor` scope check (additive):**
- Add `slack_users_read_email_scope` check that surfaces missing scope before users hit the
  cryptic `missing_scope` runtime error. Mirrors existing `slack_files_write_scope` pattern.

---

## Wave 0 Requirements

The planner MUST seed the following stubs in Wave 0 so subsequent waves can land tasks against
them. All are TODO-marked failing tests at first; tasks in later waves turn them green.

- [ ] `pkg/slack/client_lookup_test.go` — stub for `LookupUserByEmail` happy/miss/scope-error.
- [ ] `pkg/slack/client_invite_test.go` — stub for `InviteUserToChannel` happy/idempotent/typed-error.
- [ ] `pkg/slack/invite_test.go` — stub for the `EnsureMemberByEmail` result paths (incl. non-interactive AutoConnect → InvitedConnect) + prompter mock.
- [ ] `internal/app/cmd/slack_invite_test.go` — stub for the new cobra command (mock orchestrator).
- [ ] `internal/app/cmd/slack_manifest_test.go` — stub for golden-file render comparison.
- [ ] `internal/app/cmd/testdata/slack_manifest_golden.json` — fixture rendered output for a known input.
- [ ] `pkg/profile/validate_slack_invite_emails_test.go` — stub for new field validation.
- [ ] `internal/app/cmd/create_slack_invite_test.go` — stub for fail-soft behavior in `km create`.
- [ ] `internal/app/cmd/doctor_slack_users_email_test.go` — stub for new doctor check.

*Existing testing infrastructure (`go test`, `httptest`, `*SlackAPIError`) covers all behaviors —
no new framework install needed.*

---

## Manual-Only Verifications

These behaviors REQUIRE a live Slack workspace and bot token; gate behind `KM_SLACK_E2E_TOKEN`
or document as UAT-only.

| Behavior | Why Manual | Test Instructions |
|----------|------------|-------------------|
| Manifest copy-paste flow into Slack admin "From manifest" UI accepts the rendered JSON without parse errors. | Slack admin UI is web-only; no API for manifest validation. | Run `km slack manifest > /tmp/m.json`, paste into a sandbox Slack workspace's app config; confirm Slack accepts and shows all expected scopes. |
| `km slack invite alice@example.com` against a real corporate workspace where `alice@example.com` is a member. | Requires real workspace + real bot token + real user. | After re-installing app with `users:read.email`, run `km slack invite alice@example.com --channel km-notifications`; confirm Alice is added to the channel. |
| Slack Connect fallback prompt when `bob@external.com` is not in workspace. | Requires real workspace; Connect is non-mockable end-to-end. | Same as above with non-member email; confirm prompt appears, default `N`, accepting `y` triggers Connect invite. Free-tier workspace MUST surface Pro-tier error. |
| `km create` always invites the primary operator (SSM invite-email) to `#sb-{id}` — native operator via regular invite, external operator via Connect — independent of `notifySlackInviteEmails`. | E2E real-AWS create. | Provision a sandbox; confirm the operator address is a member of `#sb-{id}` in both a corporate (native operator) and PoC (external operator) workspace. |
| `km create` with `notifySlackInviteEmails: [internal@..., external@...]` and `useSlackConnect: true` (or unset) regular-invites the internal address AND auto-Connects the external one, no warning. | E2E real-AWS create. | Provision with the field set; verify both addresses are members/invited in `#sb-{id}`; confirm no `[warn]` on stderr. |
| `km create` with the same list but `useSlackConnect: false` regular-invites the internal address and SKIPS the external one with a stderr warning, without failing. | E2E real-AWS create. | Provision with `useSlackConnect: false`; verify the external address is NOT invited and stderr shows the `km slack invite --external ... --channel sb-{id}` hint. |
| `km doctor` `slack_users_read_email_scope` check surfaces missing scope before runtime error. | Verifies operator-facing diagnostic. | Install bot WITHOUT `users:read.email`; run `km doctor`; confirm FAIL with actionable remediation message. Re-install with scope; re-run; confirm PASS. |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (the 9 stub files above)
- [ ] No watch-mode flags
- [ ] Feedback latency < 10s for targeted runs
- [ ] `nyquist_compliant: true` set in frontmatter once planner finishes Per-Task Verification Map

**Approval:** pending
