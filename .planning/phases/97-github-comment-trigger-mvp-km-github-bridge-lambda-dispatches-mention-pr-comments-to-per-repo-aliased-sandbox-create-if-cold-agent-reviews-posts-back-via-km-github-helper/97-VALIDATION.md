---
phase: 97
slug: github-comment-trigger-mvp
status: planned
nyquist_compliant: true
wave_0_complete: false
created: 2026-06-06
---

# Phase 97 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` (table-driven, mocked AWS/GitHub interfaces — same style as `pkg/slack/bridge`) |
| **Config file** | none — standard Go toolchain |
| **Quick run command** | `go test ./pkg/github/... ./internal/app/cmd/... -run GitHub -count=1` |
| **Full suite command** | `make build && go test ./... -count=1` |
| **Estimated runtime** | ~60–120 seconds (full suite) |

---

## Sampling Rate

- **After every task commit:** Run the quick command scoped to the touched package.
- **After every plan wave:** Run `make build && go test ./... -count=1`.
- **Before `/gsd:verify-work`:** Full suite green + `scripts/validate-all-profiles.sh` green (new `github-review` profile).
- **Max feedback latency:** ~120 seconds.

---

## Per-Task Verification Map

> Each GH-* requirement maps to a `go test` package except GH-E2E (manual, real AWS + GitHub).
> Test-creating tasks are TDD (`tdd="true"`) — the test file is authored in the same task as the
> implementation (RED→GREEN), so no separate Wave 0 plan is required.

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | Status |
|---------|------|------|-------------|-----------|-------------------|--------|
| 97-01-T1 | 01 | 1 | GH-CLI (config round-trip + merge-list) | unit (footgun) | `go test ./internal/app/config/ -run GitHub` | ⬜ pending |
| 97-01-T2 | 01 | 1 | GH-CLI (JSON env export + drift WARN) | unit | `go test ./internal/app/cmd/ -run 'GitHubEnvExport\|GitHubRepos'` | ⬜ pending |
| 97-01-T3 | 01 | 1 | GH-APP-SCOPE (manifest/init/status + SSM keys) | unit (mocked SSM) | `go test ./internal/app/cmd/ -run 'GitHubManifest\|GitHubInit\|GitHubStatus'` | ⬜ pending |
| 97-02-T1 | 02 | 1 | GH-BRIDGE-ROUTE (write-scoped token perms) | unit | `go test ./pkg/github/ -run 'CompilePermissions\|Permissions'` | ⬜ pending |
| 97-02-T2 | 02 | 1 | GH-BRIDGE-ROUTE (cold-create envelope carry) | unit | `go test ./pkg/aws/ -run 'SandboxCreate' && go test ./cmd/create-handler/` | ⬜ pending |
| 97-02-T3 | 02 | 1 | GH-BRIDGE-ROUTE (create-handler enqueue) | unit + build | `go test ./cmd/create-handler/ && go build ./cmd/create-handler/` | ⬜ pending |
| 97-03-T1 | 03 | 1 | GH-INBOUND-Q (profile field + DDB round-trip + SQS) | unit | `go test ./pkg/profile/ ./pkg/aws/ -run 'GitHub\|Notification\|Metadata\|InboundQueue'` | ⬜ pending |
| 97-03-T2 | 03 | 1 | GH-INBOUND-Q (provision/rollback/destroy) | unit (mocked deps) | `go test ./internal/app/cmd/ -run GitHubInbound` | ⬜ pending |
| 97-03-T3 | 03 | 1 | GH-PROFILE (github-review validates) | validate | `./build/km validate profiles/github-review.yaml && bash scripts/validate-all-profiles.sh` | ⬜ pending |
| 97-04-T1 | 04 | 2 | GH-BRIDGE-VERIFY + GH-BRIDGE-ROUTE (resolve) | unit (table) | `go test ./pkg/github/bridge/ -run 'Resolve\|Signature\|Payload'` | ⬜ pending |
| 97-04-T2 | 04 | 2 | GH-BRIDGE-AUTH + GH-BRIDGE-ROUTE + dormant | unit (mocked AWS) | `go test ./pkg/github/bridge/ -count=1` | ⬜ pending |
| 97-04-T3 | 04 | 2 | GH-APP-SCOPE (Lambda + TF module) | build + vet | `go build ./cmd/km-github-bridge/ && go vet ./pkg/github/bridge/ ./cmd/km-github-bridge/` | ⬜ pending |
| 97-05-T1 | 05 | 2 | GH-POLLER (source-aware poller render + dormant) | unit (userdata render) | `go test ./pkg/compiler/ -run GitHubInbound` | ⬜ pending |
| 97-05-T2 | 05 | 2 | GH-HELPER (comment/review via httptest) | unit | `go test ./cmd/km-github/` | ⬜ pending |
| 97-06-T1 | 06 | 3 | GH-DOCTOR (checks + unconfigured-skip) | unit (mocked SSM) | `go test ./internal/app/cmd/ -run 'GitHubDoctor\|DoctorGithub'` | ⬜ pending |
| 97-06-T2 | 06 | 3 | (gate) full suite + profile gate | build + suite | `make build && go test ./... -count=1 && bash scripts/validate-all-profiles.sh` | ⬜ pending |
| 97-06-T3 | 06 | 3 | GH-E2E (real PR warm + cold + negative) | manual (real AWS + GitHub) | manual UAT runbook (checkpoint) | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

No separate Wave-0 test-scaffold plan: every code-producing task is `tdd="true"`, so the failing
test is authored in the same task as its implementation (RED→GREEN→REFACTOR within the task). The
mocking patterns to clone already exist in-tree:

- `pkg/slack/bridge/*_test.go` — handler/adapter mock templates (clone for `pkg/github/bridge`)
- `internal/app/cmd/create_slack_inbound_test.go` — deps-struct DI mock (clone for github-inbound)
- `internal/app/cmd/doctor_slack_inbound_test.go` — doctor check test template
- `pkg/compiler/userdata_slack_inbound_test.go` — userdata render assertion template
- `internal/app/config/config_test.go:880` `TestLoadSlackPeerBridges_Set` — merge-list footgun template
- Test doubles: `github.MockSSMClient` (token.go:366), `GitHubAPIBaseURL` httptest var (token.go:27)

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end `@klanker-maker review` on a real PR ⇒ 👀 ⇒ Claude review posted (warm) | GH-E2E | Requires real GitHub App install, real webhook delivery, real AWS sandbox create | See 97-06 checkpoint (Task 3): pre-warm sandbox, comment from allowlisted login, observe 👀 then posted review |
| Cold-create path (first @-mention on a repo with no sandbox) | GH-E2E | Requires real EventBridge → create-handler provisioning + first-boot envelope drain | See 97-06 checkpoint: @-mention on an alias with no running sandbox; observe SandboxCreate → provision → carried-envelope drain → review |
| Negative: unauthorized login silent; redelivery deduped | GH-BRIDGE-AUTH (live) | Best confirmed against real GitHub delivery + Redeliver button | See 97-06 checkpoint steps 12–13 |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or are the manual checkpoint (GH-E2E)
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covered via task-level TDD (no MISSING references — all test files authored in-task)
- [x] No watch-mode flags
- [x] Feedback latency < 120s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** planner-approved 2026-06-06
