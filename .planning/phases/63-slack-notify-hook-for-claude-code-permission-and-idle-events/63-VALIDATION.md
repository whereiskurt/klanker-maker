---
phase: 63
slug: slack-notify-hook-for-claude-code-permission-and-idle-events
status: approved
nyquist_compliant: true
wave_0_complete: true
created: 2026-04-29
last_updated: 2026-04-29
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
| **E2E command (opt-in)** | `RUN_SLACK_E2E=1 KM_SLACK_E2E_BOT_TOKEN=... KM_SLACK_E2E_INVITE_EMAIL=... KM_SLACK_E2E_REGION=... go test ./test/e2e/slack/...` |
| **Estimated runtime** | ~30s quick / ~90s full / ~5–15min E2E (live AWS + Slack) |

---

## Sampling Rate

- **After every task commit:** Run quick command for the package(s) touched
- **After every plan wave:** Run full suite command
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** ~30 seconds (quick suite per-package)

---

## Per-Task Verification Map

> Task IDs derived from PLAN.md frontmatter and task XML names. Each row pre-binds the requirement and validation modality.

| Task ID  | Plan | Wave | Requirement | Test Type            | Automated Command                                                                            | File Exists | Status      |
|----------|------|------|-------------|----------------------|-----------------------------------------------------------------------------------------------|-------------|-------------|
| 63-01-T1 | 01   | 1    | SLCK-01     | unit                 | `go test ./pkg/profile/... -count=1`                                                          | partial (Wave 0 stubs) | pending |
| 63-01-T2 | 01   | 1    | SLCK-01     | grep-validate        | `grep -c "\| SLCK-" .planning/REQUIREMENTS.md` returns 10                                     | yes         | pending     |
| 63-02-T1 | 02   | 1    | SLCK-03     | unit                 | `go test ./pkg/slack/ -count=1 -run 'TestBuildEnvelope\|TestCanonicalJSON\|TestSignVerify\|TestVerify'` | Wave 0 stubs | pending |
| 63-02-T2 | 02   | 1    | SLCK-03     | unit                 | `go test ./pkg/slack/ -count=1 -run 'TestClient\|TestPostToBridge'`                           | Wave 0 stubs | pending     |
| 63-03-T1 | 03   | 1    | SLCK-04     | unit                 | `go test ./pkg/slack/bridge/... -count=1`                                                     | Wave 0 stubs | pending     |
| 63-04-T1 | 04   | 2    | SLCK-02     | unit                 | `go test ./pkg/compiler/... -count=1`                                                         | yes (extends 62-02) | pending |
| 63-05-T1 | 05   | 2    | SLCK-03     | unit + build         | `go test ./cmd/km-slack/... -count=1 && go build ./cmd/km-slack/`                             | new         | pending     |
| 63-05-T2 | 05   | 2    | SLCK-03     | build + grep         | `make build && grep -q "sidecars/km-slack" pkg/compiler/userdata.go && grep -q "km-slack" Makefile` | yes (existing files) | pending |
| 63-06-T1 | 06   | 2    | SLCK-04     | unit + build         | `go test ./pkg/slack/bridge/... ./pkg/aws/... -count=1 && go build ./cmd/km-slack-bridge/`    | new         | pending     |
| 63-06-T2 | 06   | 2    | SLCK-04     | unit + tf-fmt        | `go test ./internal/app/cmd/... -count=1 -run 'TestRegional\|TestBuildLambdaZips' && (cd infra/modules/lambda-slack-bridge/v1.0.0 && terraform fmt -check)` | new         | pending     |
| 63-07-T1 | 07   | 3    | SLCK-05     | unit + cli-help      | `go test ./internal/app/cmd/... -count=1 -run TestSlack && make build && ./km slack --help \| grep -q init` | new       | pending     |
| 63-08-T1 | 08   | 3    | SLCK-06     | unit                 | `go test ./internal/app/cmd/... ./pkg/slack/... -count=1 -run 'TestResolveSlack\|TestSanitizeChannelName\|TestClient_ChannelInfo'` | new | pending |
| 63-08-T2 | 08   | 3    | SLCK-06     | unit + build         | `go test ./internal/app/cmd/... ./pkg/aws/... ./pkg/compiler/... ./pkg/slack/... -count=1 && make build` | yes        | pending     |
| 63-09-T1 | 09   | 3    | SLCK-07     | unit                 | `go test ./internal/app/cmd/... -count=1 -run TestDestroySlack`                               | new         | pending     |
| 63-09-T2 | 09   | 3    | SLCK-08     | unit                 | `go test ./internal/app/cmd/... -count=1 -run 'TestCheckSlack\|TestDoctor'`                   | yes (extends doctor) | pending |
| 63-10-T1 | 10   | 4    | SLCK-09     | gate-test + validate | `make build && ./km validate profiles/slack-test-shared.yaml && ./km validate profiles/slack-test-per-sandbox.yaml && (go test ./test/e2e/slack/... -count=1 2>&1 \| grep -q "skipping live Slack E2E tests")` | new         | pending     |
| 63-10-T2 | 10   | 4    | SLCK-10     | grep-validate        | `grep -q "km slack init" CLAUDE.md && grep -q "/km/slack/bot-token" CLAUDE.md && grep -q "KM_NOTIFY_SLACK_ENABLED" CLAUDE.md && [ -f docs/slack-notifications.md ] && grep -q "Pro Slack workspace" docs/slack-notifications.md` | new         | pending     |
| 63-10-T3 | 10   | 4    | SLCK-09     | checkpoint:human-verify | live UAT — see Manual-Only table below; signed off in 63-10-UAT.md                         | n/a         | pending     |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

**Nyquist compliance summary:**
- 17 of 18 tasks have an automated `<verify><automated>...</automated></verify>` command.
- Task 63-10-T3 is `checkpoint:human-verify` (live UAT for Slack Connect invite acceptance + bot token rotation — both genuinely require humans-in-loop and are infeasible to script).
- The automated coverage in 63-10-T1 + 63-10-T2 covers the same surface for everything that CAN be scripted; 63-10-T3 covers only the truly manual residue.
- No 3 consecutive tasks lack automated verify (T3 is the only manual task; preceded and followed by automated coverage).
- `nyquist_compliant: true` set above.

---

## Wave 0 Requirements

Wave 0 stub-creation lands as part of Wave 1A (schema), 1B (payload/client), and 1C (bridge handler skeleton). Specifically:

- [x] `pkg/profile/validate_test.go` — validation rule stubs for the five new fields (mutual exclusion, no-op warnings, channel-ID regex, no-channels warning); IsWarning assertions. **Lands in 63-01-T1.**
- [x] `pkg/slack/payload_test.go` — canonical-JSON construction, signature determinism, 40 KB body cap. **Lands in 63-02-T1.**
- [x] `pkg/slack/client_test.go` — HTTP retry behavior matrix (200/429/5xx/network) using `httptest.NewServer`; PostToBridge tests. **Lands in 63-02-T2.**
- [x] `pkg/slack/bridge/handler_test.go` — verification-flow stubs covering all 7 verification steps + every negative branch. **Lands in 63-03-T1.**
- [x] `pkg/compiler/userdata_notify_test.go` extensions — env-var emission and hook-script extension stubs additive to Phase 62 test file. **Lands in 63-04-T1.**

`wave_0_complete: true` set above because each stub file is explicitly created as part of the corresponding Wave 1 / Wave 2 task body (TDD-style: tests written first, implementation second). The frontmatter flag flips `true` because the plans have committed to creating these files; in-flight execution toggles individual rows from pending → green as the work lands.

**Stub conventions:** mirror Phase 62's `pkg/compiler/userdata_notify_test.go` pattern — table-driven tests with golden-file snapshots for the generated user-data heredoc.

**Mock surfaces:**
- Slack Web API → `httptest.NewServer` returning fixture responses for `chat.postMessage`, `conversations.create`, `conversations.inviteShared`, `conversations.archive`, `auth.test`, `conversations.info`.
- DynamoDB nonce table → narrow interface (`NonceStore`) with in-memory fake; production = `DynamoNonceStore` adapter.
- DynamoDB km-identities → narrow `PublicKeyFetcher` interface; in-memory fake holding ephemeral ed25519 keys generated per test.
- DynamoDB km-sandboxes → narrow `ChannelOwnershipFetcher` interface; in-memory fake.
- SSM bot-token fetch → narrow `BotTokenFetcher` interface; in-memory fake with token + err fields.
- Sandbox / operator Ed25519 keys → ephemeral keys via `ed25519.GenerateKey`.
- `km` CLI in E2E harness → `exec.Command("./km", args...)` with combined output capture.

---

## Manual-Only Verifications

These cannot be safely automated in CI without a real Slack workspace. Run via opt-in CI flag (`RUN_SLACK_E2E=1`) with credentials in a non-default test environment, or as live UAT (63-10-UAT.md).

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Slack Connect invite delivered to operator's separate workspace; acceptance creates a usable shared channel | SLCK-09 (partial — automated portion confirms SSM populated) | Slack Connect cannot be exercised against a stub — requires real workspace + receiving inbox + human click | `km slack init` against test workspace with `--invite-email <test-inbox>`. Manually accept invite. Verify channel ID stored in SSM matches the channel visible in receiver workspace. |
| End-to-end notification delivery (sandbox → bridge → Slack) for both `Notification` and `Stop` events | SLCK-09 | Automated as `TestE2ESlack_SharedMode_NotificationDelivery` and `TestE2ESlack_SharedMode_PermissionEvent` (opt-in) | `km create profiles/slack-test-shared.yaml` → `km agent run --prompt "What's 2+2?" --wait`. Confirm message arrives in `#km-notifications` with bold subject header. Repeat with permission-triggering prompt. |
| Per-sandbox channel creation, Connect invite, and archive on destroy | SLCK-09 | Automated as `TestE2ESlack_PerSandboxMode_LifecycleAndArchive` (opt-in) | `km create profiles/slack-test-per-sandbox.yaml --alias=demo`. Confirm `#sb-demo` appears in receiver workspace via Slack Connect. Run `km destroy sb-... --remote --yes`. Confirm archive in Slack UI. Repeat with `slackArchiveOnDestroy: false` and confirm channel persists. |
| Backward compat: existing Phase 62 profiles continue to send email when `notifyEmailEnabled` is unset | SLCK-09 | Automated as `TestE2ESlack_Phase62Compat_EmailWhenSlackOff` (opt-in); requires real sandbox + email round-trip | Provision a Phase 62 profile (no Slack fields). Confirm idle event delivers email via `km email read --json` AND that no Slack channel receives a corresponding message. |
| Bot token rotation: new token in SSM picked up by warm Lambda within cache TTL | SLCK-09 (UAT-only) | Requires real Lambda warm container + SSM + ≥15-minute wait — infeasible to script without long-running CI jobs | Deploy bridge, post message; swap `/km/slack/bot-token` to invalid value; post within 15 min — should fail. Wait >15 min, post — should fail with token error. Restore valid token, wait, post — should succeed. Document in 63-10-UAT.md. |
| Slack rate-limit upstream propagation (Slack 429 → bridge 503 → `km-slack` retry) | SLCK-09 | Automated as `TestE2ESlack_RateLimit_BurstBackoff` gated by additional `KM_SLACK_E2E_RATELIMIT=1` (avoids workspace spam in default E2E runs) | Burst N posts via `km-slack` until Slack returns 429. Confirm `km-slack` retries with backoff and eventually succeeds. |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or are explicitly classified `checkpoint:human-verify` (only 63-10-T3)
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references (5 stub files listed above; each is explicitly created by its parent Wave 1/2 task body in TDD order)
- [x] No watch-mode flags (Go test is single-shot by default — compliant)
- [x] Feedback latency < 30s for quick suite, < 90s for full
- [x] `nyquist_compliant: true` set in frontmatter
- [x] `wave_0_complete: true` set in frontmatter (commitment-level; row statuses flip green during execution)

**Approval:** approved 2026-04-29 — task IDs finalized from 63-01..63-10 PLAN.md files, requirement IDs from REQUIREMENTS.md SLCK-01..SLCK-10, every task either has automated verification or is justified as a checkpoint for genuine human-only acceptance.
