---
phase: 72
slug: slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator
status: draft
nyquist_compliant: false
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
| 72-WW-TT | WW | W | (filled by planner) | unit / integration / e2e | `go test ...` | ✅ / ❌ W0 | ⬜ pending |

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
- Returns `SkippedExternal` when lookup misses and `Interactive=false`.
- Returns `SkippedExternal` when lookup misses, prompter declines, and `Interactive=true`.
- Returns `SkippedExternal` when `ForceExternal=true` and `Interactive=false` (no, this should still send Connect — confirm during planning).
- Wraps Connect errors so callers can surface the Pro-tier hint.

**Layer 3 — `km slack invite` cobra command (`internal/app/cmd/slack_invite.go`):**
- `--channel <name>` resolves via `FindChannelByName`.
- `--channel <C012ABCDE3F>` (ID format) is used directly without lookup.
- Default channel falls back to SSM `{prefix}/slack/shared-channel-id`.
- `--external` short-circuits to `inviteShared` without `users.lookupByEmail`.
- Exit codes: `0` for `Invited*` / `AlreadyMember`; `1` for `Failed`; `2` for `SkippedExternal` (so scripts can distinguish).

**Layer 4 — `km slack init` refactor (`internal/app/cmd/slack.go`):**
- Existing PoC test path (operator email is external) still routes via the orchestrator and produces the same SSM writes + Slack Connect invite.
- New corporate path (operator email is in workspace) uses regular invite.

**Layer 5 — `km slack manifest` cobra command (`internal/app/cmd/slack_manifest.go`):**
- Renders embedded JSON template with deployment-specific `request_url` from SSM bridge URL.
- Substitutes `display_information.name` and `bot_user.display_name` from `--app-name` flag or `KlankerMaker-{resource_prefix}` default.
- Output is valid JSON parseable by `encoding/json`.
- Output includes the new `users:read.email` scope.
- Output matches a golden fixture for a known input set (deterministic).
- Errors cleanly when bridge URL SSM key is missing (init not run).

**Layer 6 — Profile field (`pkg/profile/...`):**
- `notifySlackInviteEmails: []` parses without error from YAML.
- Each entry is validated as RFC 5322 email (or "looks like" — match existing email pattern in codebase).
- Validation rejects non-empty list when `notifySlackEnabled=false` (Phase 67/68 cross-field pattern).
- Schema additions in `sandbox_profile.schema.json` validate the field shape.

**Layer 7 — `km create` per-sandbox auto-invite (`internal/app/cmd/create_slack.go`):**
- After `#sb-{id}` channel creation, the orchestrator runs once per `notifySlackInviteEmails` entry.
- Internal users land via `InvitedDirect`; create succeeds.
- External users (lookup miss, non-interactive) emit a stderr warning containing the
  follow-up `km slack invite --external ...` command but DO NOT fail the create.
- A Slack 5xx during invite emits a warning and does not fail the create (fail-soft contract).

**Layer 8 — `km doctor` scope check (additive):**
- Add `slack_users_read_email_scope` check that surfaces missing scope before users hit the
  cryptic `missing_scope` runtime error. Mirrors existing `slack_files_write_scope` pattern.

---

## Wave 0 Requirements

The planner MUST seed the following stubs in Wave 0 so subsequent waves can land tasks against
them. All are TODO-marked failing tests at first; tasks in later waves turn them green.

- [ ] `pkg/slack/client_lookup_test.go` — stub for `LookupUserByEmail` happy/miss/scope-error.
- [ ] `pkg/slack/client_invite_test.go` — stub for `InviteUserToChannel` happy/idempotent/typed-error.
- [ ] `pkg/slack/invite_test.go` — stub for the five `EnsureMemberByEmail` result paths + prompter mock.
- [ ] `internal/app/cmd/slack_invite_test.go` — stub for the new cobra command (mock orchestrator).
- [ ] `internal/app/cmd/slack_manifest_test.go` — stub for golden-file render comparison.
- [ ] `internal/app/cmd/slack_manifest_template_golden.json` — fixture rendered output for a known input.
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
| `km slack invite alice@greenhouse.io` against a real corporate workspace where `alice@greenhouse.io` is a member. | Requires real workspace + real bot token + real user. | After re-installing app with `users:read.email`, run `km slack invite alice@greenhouse.io --channel km-notifications`; confirm Alice is added to the channel. |
| Slack Connect fallback prompt when `bob@external.com` is not in workspace. | Requires real workspace; Connect is non-mockable end-to-end. | Same as above with non-member email; confirm prompt appears, default `N`, accepting `y` triggers Connect invite. Free-tier workspace MUST surface Pro-tier error. |
| `km create` with `notifySlackInviteEmails: [internal@..., external@...]` provisions a sandbox, regular-invites the internal address, warns on the external one without failing. | E2E real-AWS create. | Provision a sandbox with the profile field set; verify `#sb-{id}` channel membership in Slack and stderr warning content from `km create`. |
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
