---
phase: 115
slug: generic-github-webhook-event-prompt-router
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-15
---

# Phase 115 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` package (standard) |
| **Config file** | none |
| **Quick run command** | `go test ./pkg/github/bridge/... -run TestEventRouter -timeout 30s` |
| **Full suite command** | `go test ./pkg/github/bridge/... ./internal/app/cmd/... ./internal/app/config/... -timeout 600s -count=1` |
| **Estimated runtime** | ~60 seconds (full); <5s (quick) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/github/bridge/... -run TestEventRouter -timeout 30s`
- **After every plan wave:** Run `go test ./pkg/github/bridge/... ./internal/app/cmd/... ./internal/app/config/... -timeout 600s -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green (capture the command's own exit code, not a piped `tail` — known footgun)
- **Max feedback latency:** ~60 seconds

---

## Per-Task Verification Map

| Requirement | Test Type | Automated Command | File Exists | Status |
|-------------|-----------|-------------------|-------------|--------|
| GH-EVENT-CONFIG | unit | `go test ./internal/app/config/... -run TestLoad.*Github -timeout 30s` | ❌ W0 — `config_test.go` additions | ⬜ pending |
| GH-EVENT-ROUTER | unit | `go test ./pkg/github/bridge/... -run TestMatchEventRule -timeout 30s` | ❌ W0 — `event_router_test.go` | ⬜ pending |
| GH-EVENT-GATING | unit | `go test ./pkg/github/bridge/... -run TestHandle.*EventRoute -timeout 30s` | ❌ W0 — `webhook_handler_phase115_test.go` | ⬜ pending |
| GH-EVENT-TEMPLATE | unit | `go test ./pkg/github/bridge/... -run TestExpandEventTemplate -timeout 30s` | ❌ W0 — `event_router_test.go` | ⬜ pending |
| GH-EVENT-DISPATCH | unit | `go test ./pkg/github/bridge/... -run 'TestHandleEventRoute' -timeout 30s` | ❌ W0 — `webhook_handler_phase115_test.go` | ⬜ pending |
| GH-EVENT-COOLDOWN | unit | `go test ./pkg/github/bridge/... -run TestHandle.*Cooldown -timeout 30s` | ❌ W0 — `webhook_handler_phase115_test.go` | ⬜ pending |
| GH-EVENT-MANIFEST | unit | `go test ./internal/app/cmd/... -run TestRunGitHubManifest -timeout 30s` | ❌ W0 — `github_test.go` additions | ⬜ pending |
| GH-EVENT-DOCTOR | unit | `go test ./internal/app/cmd/... -run TestCheckGitHubEventsValid -timeout 30s` | ❌ W0 — `doctor_test.go` additions | ⬜ pending |
| GH-EVENT-POLLER | manual | live UAT (userdata bash is invisible to Go unit tests) | N/A | ⬜ pending |
| GH-EVENT-DOCS | manual | `docs/github-bridge.md` § Phase 115 present | N/A | ⬜ pending |
| GH-EVENT-E2E | manual | live UAT: create throwaway org repo → cold-create → prompt runs; `exclude:`d repo does not fire | N/A | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/github/bridge/event_router_test.go` — stubs for GH-EVENT-ROUTER + GH-EVENT-TEMPLATE (table-driven, mirror `resolve_test.go`/`commands_test.go`)
- [ ] `pkg/github/bridge/webhook_handler_phase115_test.go` — stubs for GH-EVENT-GATING, GH-EVENT-DISPATCH, GH-EVENT-COOLDOWN (mock nonces/SQS/EventBridge/reactor)
- [ ] `internal/app/config/config_test.go` additions — GH-EVENT-CONFIG (yaml load round-trip)
- [ ] `internal/app/cmd/doctor_test.go` additions — GH-EVENT-DOCTOR
- [ ] `internal/app/cmd/github_test.go` additions — GH-EVENT-MANIFEST

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Poller tolerates `Number==0` + builds event-context preamble for `Kind != issue_comment` | GH-EVENT-POLLER | userdata bash is invisible to Go goldens; only a live box parses it (project memory: skill/poller bash needs live UAT) | After deploy, create a throwaway repo; SSM into the cold-created sandbox; confirm the agent received a sane event preamble (no `PR: #0` / `pull/0/head`) |
| End-to-end event → sandbox → prompt | GH-EVENT-E2E | requires real GitHub webhook delivery + real AWS | Configure a `github.events:` rule (`on: repository`, `match: <org>/*`); `make build-lambdas` + `km init --github`; regenerate manifest + re-install App; create a throwaway repo; confirm a sandbox cold-creates and the prompt runs; create an `exclude:`d-pattern repo and confirm no fire |
| Docs section present | GH-EVENT-DOCS | doc prose | `grep "Phase 115" docs/github-bridge.md` |

---

## Validation Sign-Off

- [ ] All code requirements have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 60s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
