---
phase: 118
slug: slack-trigger-allowlist-private-per-sandbox-channels
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-24
---

# Phase 118 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (`go test`) |
| **Config file** | none — standard Go test runner |
| **Quick run command** | `go test ./pkg/slack/bridge/... ./pkg/profile/... -count=1 -timeout 120s` |
| **Full suite command** | `go test ./... -count=1 -timeout 600s` |
| **Estimated runtime** | quick ~30s · full ~5–8 min |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/slack/bridge/... ./pkg/profile/... -count=1 -timeout 120s`
- **After every plan wave:** Run `go test ./... -count=1 -timeout 600s` (capture the command's own exit code — `| tail` masks a real FAIL, see memory feedback_check_go_test_exit_not_pipe)
- **Before `/gsd:verify-work`:** Full suite green + live Slack UAT for AC1/AC2
- **Max feedback latency:** ~30 seconds (quick run)

---

## Per-Task Verification Map

| AC | Plan | Wave | Behavior | Test Type | Automated Command | File Exists | Status |
|----|------|------|----------|-----------|-------------------|-------------|--------|
| AC1 | A (private channel) | varies | `private:true` creates private Slack channel at km create | unit (mock SlackAPI) | `go test ./internal/app/cmd/... -run TestResolveSlackChannel -count=1` | ❌ W0 (extend mock for new signature) | ⬜ pending |
| AC2 | B (enforce) | varies | install-level allow: listed triggers, unlisted silent-drop | unit (EventsHandler) | `go test ./pkg/slack/bridge/... -run TestEventsHandler_Allowlist -count=1` | ❌ W0 | ⬜ pending |
| AC3 | B (enforce) | varies | per-sandbox allow overrides install-level | unit (EventsHandler) | `go test ./pkg/slack/bridge/... -run TestEventsHandler_PerSandboxAllowOverrides -count=1` | ❌ W0 | ⬜ pending |
| AC4 | B (enforce) | varies | empty/unset allow at both levels = everyone allowed | unit (EventsHandler) | `go test ./pkg/slack/bridge/... -run TestEventsHandler_AllowlistEmpty_EveryoneAllowed -count=1` | ❌ W0 | ⬜ pending |
| AC5 | B (enforce) | varies | allowlist enforced on thread replies (Phase 91.3 bypass non-exempt) | unit (EventsHandler) | `go test ./pkg/slack/bridge/... -run TestEventsHandler_Allowlist_ThreadBypassDoesNotExempt -count=1` | ❌ W0 | ⬜ pending |
| AC6 | A+B (validate) | varies | km validate warns on private/allow set with perSandbox:false | unit (validate.go) | `go test ./pkg/profile/... -run TestValidate_Slack -count=1` | ⚠️ extend existing | ⬜ pending |
| AC7 | B (per-sandbox plumbing) | varies | per-sandbox allow survives pause/resume/extend | unit (SandboxMetadata round-trip) | `go test ./pkg/aws/... -run TestSandboxMetadata_SlackAllow_RoundTrip -count=1` | ❌ W0 | ⬜ pending |
| AC8 | B (enforce) | varies | existing installs (no allow/private) = byte-identical | unit (EventsHandler nil-invariant) | `go test ./pkg/slack/bridge/... -run TestEventsHandler_NoAllowlistSet_ByteIdentical -count=1` | ⚠️ verify existing pass | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*
*Wave numbers finalized by the planner; AC→test mapping is fixed.*

---

## Wave 0 Requirements

- [ ] `pkg/slack/bridge/events_handler_allowlist_test.go` — stubs for AC2, AC3, AC4, AC5, AC8 (EventsHandler allowlist enforcement + resolution order)
- [ ] `pkg/aws/sandbox_dynamo_allow_test.go` (or extend `sandbox_dynamo_test.go`) — stub for AC7 (`slack_allow` comma-joined-S round-trip through `SandboxMetadata`)
- [ ] Extend `internal/app/cmd/create_slack_test.go` mock for the new `CreateChannel(ctx, name, private)` signature — covers AC1
- [ ] Extend `pkg/profile/validate_test.go` for the new `private`/`allow` + `perSandbox:false` warn rules — covers AC6

*No framework install needed — Go test runner already in use.*

---

## Manual-Only Verifications

| Behavior | AC | Why Manual | Test Instructions |
|----------|-----|-----------|-------------------|
| Private channel actually created private in Slack | AC1 | Requires real Slack API — mock proves the param is passed, not the workspace result | `km create` a profile with `perSandbox:true` + `private:true`; confirm `conversations.info` returns `is_private:true` and operator + invites.emails are members |
| Install-level allow silent-drop end-to-end | AC2 | Requires real bridge + signed event | Self-drive via the HMAC-signed synthetic `event_callback` POST to `{bridge-url}/events` (helper from memory project_slack_bridge_inbound_e2e_and_status_attr) with `event.User` set to a non-listed Uxxxx; assert no 👀 reaction, no SQS dispatch, no run |

*All other ACs (AC3–AC8) have automated Go unit coverage.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (4 stubs above)
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s (quick run)
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
