---
phase: 63
slug: slack-notify-hook-for-claude-code-permission-and-idle-events
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-29
---

# Phase 63 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (Go 1.x — existing project standard) |
| **Config file** | none (Go's built-in test discovery) |
| **Quick run command** | `go test ./pkg/profile/... ./pkg/slack/... ./pkg/compiler/...` |
| **Full suite command** | `go test ./... && go vet ./...` |
| **Estimated runtime** | ~30s quick / ~90s full |

---

## Sampling Rate

- **After every task commit:** Run quick command for the package(s) touched
- **After every plan wave:** Run full suite command
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** ~30 seconds (quick suite per-package)

---

## Per-Task Verification Map

> Task IDs are placeholders — populated by gsd-planner from PLAN.md frontmatter. Each row pre-binds the requirement and validation modality this phase needs.

| Task ID | Plan | Wave | Requirement (TBD by planner) | Test Type | Automated Command | File Exists | Status |
|---------|------|------|------------------------------|-----------|-------------------|-------------|--------|
| 63-01-XX | 01 (Wave 1A — schema) | 1 | REQ-SLACK-SCHEMA | unit | `go test ./pkg/profile/...` | ⬜ W0 | ⬜ pending |
| 63-02-XX | 02 (Wave 1B — payload/client) | 1 | REQ-SLACK-PAYLOAD | unit | `go test ./pkg/slack/...` | ⬜ W0 | ⬜ pending |
| 63-03-XX | 03 (Wave 1C — bridge handler skeleton) | 1 | REQ-SLACK-BRIDGE-VERIFY | unit | `go test ./pkg/slack/bridge/...` | ⬜ W0 | ⬜ pending |
| 63-04-XX | 04 (Wave 2A — compiler/hook) | 2 | REQ-SLACK-COMPILER | unit | `go test ./pkg/compiler/...` | ⬜ W0 | ⬜ pending |
| 63-05-XX | 05 (Wave 2B — km-slack binary) | 2 | REQ-SLACK-CLI | unit + integration | `go test ./cmd/km-slack/...` | ⬜ W0 | ⬜ pending |
| 63-06-XX | 06 (Wave 2C — bridge Lambda + Terraform) | 2 | REQ-SLACK-LAMBDA | unit + tf-validate | `go test ./pkg/slack/bridge/...` + `terragrunt hclvalidate` | ⬜ W0 | ⬜ pending |
| 63-07-XX | 07 (Wave 3A — km slack init/test/status) | 3 | REQ-SLACK-OPS-CLI | unit | `go test ./internal/app/cmd/...` | ⬜ W0 | ⬜ pending |
| 63-08-XX | 08 (Wave 3B — km create channel provisioning) | 3 | REQ-SLACK-CREATE | unit + e2e-mock | `go test ./internal/app/cmd/...` | ⬜ W0 | ⬜ pending |
| 63-09-XX | 09 (Wave 3C — km destroy archive) | 3 | REQ-SLACK-DESTROY | unit | `go test ./internal/app/cmd/...` | ⬜ W0 | ⬜ pending |
| 63-10-XX | 10 (Wave 3D — km doctor) | 3 | REQ-SLACK-DOCTOR | unit | `go test ./internal/app/cmd/...` | ⬜ W0 | ⬜ pending |
| 63-11-XX | 11 (Wave 4A — E2E harness) | 4 | REQ-SLACK-E2E | manual / opt-in CI | see Manual-Only table | n/a | ⬜ pending |
| 63-12-XX | 12 (Wave 4B/4C — docs) | 4 | REQ-SLACK-DOCS | doc-lint | `markdownlint docs/slack-notifications.md` (if available, else manual review) | ⬜ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Wave 0 lands as part of Wave 1A (schema) and Wave 1B (payload/client) — the foundation packages that don't yet have tests. Specifically:

- [ ] `pkg/profile/validate_test.go` — validation rule stubs for the five new fields (mutual exclusion, no-op warnings, channel-ID regex, no-channels warning). Uses existing `ValidationError` shape; if Research finding #5 (`IsWarning` field) is adopted, schema test must verify warnings don't fail validation.
- [ ] `pkg/slack/payload_test.go` — canonical-JSON construction stubs, signature determinism stubs, 40 KB body cap stub.
- [ ] `pkg/slack/client_test.go` — HTTP retry behavior stubs (200/429/5xx/network error matrix) using `httptest.NewServer`.
- [ ] `pkg/slack/bridge/handler_test.go` — verification-flow stubs covering each of the 7 verification steps (parse, replay, signature, action-auth, token fetch, execute, response).
- [ ] `pkg/compiler/compiler_test.go` extensions — env-var emission and hook-script extension stubs (additive to Phase 62 test file; do not regress existing cases).

**Stub conventions:** mirror Phase 62's `pkg/compiler/compiler_test.go` pattern — table-driven tests with golden-file snapshots for the generated user-data heredoc.

**Mock surfaces:**
- Slack Web API → `httptest.NewServer` returning fixture responses for `chat.postMessage`, `conversations.create`, `conversations.inviteShared`, `conversations.archive`, `auth.test`.
- DynamoDB nonce table → use `aws-sdk-go-v2` middleware override (see existing Phase 39 metadata table tests for precedent) or in-memory fake.
- SSM bot-token fetch → fake `ssm.Client` interface (mirror `pkg/aws/identity.go` test pattern).
- Sandbox Ed25519 keys → ephemeral keys generated per-test via `ed25519.GenerateKey`.

---

## Manual-Only Verifications

These cannot be safely automated in CI without a real Slack workspace. Run via opt-in CI flag (e.g., `RUN_SLACK_E2E=1`) with credentials in a non-default test environment.

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Slack Connect invite is delivered to operator's email and acceptance creates a usable shared channel | REQ-SLACK-CONNECT | Slack Connect cannot be exercised against a stub — requires real workspace + receiving inbox | Run `km slack init` against test workspace with `--invite-email <test-inbox>`. Manually accept invite. Verify channel ID stored in SSM matches the channel visible in receiver workspace. |
| End-to-end notification delivery (sandbox → bridge → Slack) for both `Notification` and `Stop` events | REQ-SLACK-E2E-NOTIFY | Requires real SSM, real Lambda deploy, real Slack API | `km create profiles/p.yaml` (with `notifySlackEnabled: true`) → `km agent run --prompt "What's 2+2?"`. Confirm message arrives in `#km-notifications` with bold subject header. Repeat with permission-triggering prompt. |
| Per-sandbox channel creation, Connect invite, and archive on destroy | REQ-SLACK-PER-SANDBOX | Live channel lifecycle | `km create profiles/p.yaml --alias=demo` (with `notifySlackPerSandbox: true`). Confirm `#sb-demo` appears in receiver workspace via Connect. Run `km destroy sb-... --remote --yes`. Confirm archive in Slack UI. Repeat with `slackArchiveOnDestroy: false` and confirm channel persists. |
| Backward compat: existing Phase 62 profiles continue to send email when `notifyEmailEnabled` is unset | REQ-SLACK-PHASE62-COMPAT | Requires real sandbox + email round-trip | Re-run any Phase 62 E2E test from `62-VALIDATION.md` against a sandbox built with the Phase 63 compiler. Verify email still arrives. |
| Bot token rotation: new token in SSM picked up by warm Lambda within cache TTL | REQ-SLACK-TOKEN-ROTATION | Requires real Lambda warm container + SSM | Deploy bridge, post message, swap `/km/slack/bot-token` to invalid value, post within 15min — should fail. Wait >15min, post — should fail with token error. Restore valid token, wait, post — should succeed. |
| Slack rate-limit upstream propagation (Slack 429 → bridge 503 → `km-slack` retry) | REQ-SLACK-RATE-LIMIT | Triggering a real 429 requires sustained traffic; can be approximated by a stub but real-API behavior should be smoke-tested once | Burst N posts via `km-slack` until Slack returns 429. Confirm `km-slack` retries with backoff and eventually succeeds (Tier 4 limit is 100/min for `chat.postMessage`). |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (5 stub files listed above)
- [ ] No watch-mode flags (Go test is single-shot by default — compliant)
- [ ] Feedback latency < 30s for quick suite, < 90s for full
- [ ] `nyquist_compliant: true` set in frontmatter (after planner finalizes task IDs)

**Approval:** pending — set to `approved YYYY-MM-DD` once gsd-planner finalizes task IDs and confirms each row maps to a real task in PLAN.md files.
