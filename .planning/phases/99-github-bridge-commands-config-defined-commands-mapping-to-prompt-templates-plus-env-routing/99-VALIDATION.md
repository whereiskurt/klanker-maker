---
phase: 99
slug: github-bridge-commands-config-defined-commands-mapping-to-prompt-templates-plus-env-routing
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-07
---

# Phase 99 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (`testing` stdlib, table-driven) |
| **Config file** | none — `go test ./...` |
| **Quick run command** | `go test ./pkg/github/bridge/... ./internal/app/cmd/... -run 'GitHubCmd\|Command\|Handle_' -count=1` |
| **Full suite command** | `go test ./pkg/github/bridge/... ./internal/app/config/... ./internal/app/cmd/... -count=1` |
| **Estimated runtime** | ~30 seconds (pure-function + fake-backed; no AWS/live calls) |

---

## Sampling Rate

- **After every task commit:** Run the quick command (scoped to bridge + cmd command tests)
- **After every plan wave:** Run the full suite command
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** ~30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 99-01-* | 01 | 1 | GH-CMD-CONFIG | unit | `go test ./internal/app/config/... -run TestGithubConfigCommands -count=1` | ❌ W0 | ⬜ pending |
| 99-02-* | 02 | 1 | GH-CMD-PARSE | unit | `go test ./pkg/github/bridge/... -run 'TestCommandParse\|TestExtractArgs\|TestExpandTemplate\|TestEffectiveDefault' -count=1` | ❌ W0 | ⬜ pending |
| 99-02-* | 02 | 1 | GH-CMD-ROUTE | unit | `go test ./pkg/github/bridge/... -run TestCommandRouting -count=1` | ❌ W0 | ⬜ pending |
| 99-02-* | 02 | 1 | GH-CMD-AUTH | unit | `go test ./pkg/github/bridge/... -run TestCommandAuth -count=1` | ❌ W0 | ⬜ pending |
| 99-03-* | 03 | 2 | GH-CMD-FILEREF | unit | `go test ./internal/app/cmd/... -run TestResolveCommandPrompts -count=1` | ❌ W0 | ⬜ pending |
| 99-03-* | 03 | 2 | GH-CMD-SSM | unit (fake SSM) | `go test ./internal/app/cmd/... -run TestInitGitHubCommands -count=1` | ❌ W0 | ⬜ pending |
| 99-04-* | 04 | 2 | GH-CMD-AUTH/HELP/E2E | unit (handler+fakes) | `go test ./pkg/github/bridge/... -run 'TestHandle_(MultiCommand\|CommandNotAuthorized\|Help\|UnknownToken\|DefaultCommand\|CommandsDormant)' -count=1` | ❌ W0 | ⬜ pending |
| 99-05-* | 05 | 3 | GH-CMD-CONFIG | unit (config checks) | `go test ./internal/app/cmd/... -run TestDoctorGitHubCommands -count=1` | ❌ W0 | ⬜ pending |
| 99-05-* | 05 | 3 | GH-CMD-HELP | unit | `go test ./internal/app/cmd/... -run TestGitHubStatusCommands -count=1` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

*Final task IDs are assigned by the planner; the map binds each requirement to its automated command and the Wave-0 test file that must exist first.*

---

## Wave 0 Requirements

All test files are new — none exist yet. Each plan's first task creates its test file (TDD: red before green):

- [ ] `internal/app/config/config_github_commands_test.go` — GH-CMD-CONFIG struct + `UnmarshalKey` round-trip (Plan 01)
- [ ] `pkg/github/bridge/commands_test.go` — GH-CMD-PARSE / ROUTE / AUTH pure-function tests (Plan 02)
- [ ] `internal/app/cmd/init_github_commands_test.go` — GH-CMD-FILEREF `@file` resolution + GH-CMD-SSM write/drift (Plan 03)
- [ ] `pkg/github/bridge/webhook_handler_phase99_test.go` — GH-CMD-AUTH/HELP/E2E handler reply paths + dormancy (Plan 04)
- [ ] `internal/app/cmd/doctor_github_commands_test.go` — all doctor-check WARNs + `km github status` listing (Plan 05)

**Reusable helpers (no new fixtures needed):**
- `pkg/github/bridge/handle_test.go`: `mockSecretFetcher`, `mockBotLoginFetcher`, `mockNonceStore`, `mockResolver`, `mockPublisher`, `mockSQS`, `mockReactor`
- `pkg/github/bridge/webhook_handler_phase98_test.go`: `mockGitHubThreadStore`, `buildPayloadJSON()`, `buildRequest()`, `defaultOpts()`
- `pkg/github/bridge/resolve_test.go`: table-driven `[]struct{name,...want...}` style to mirror
- New fake needed: `mockCommenter` (POST-to-comment) — mirror `mockReactor` for the 3 reply paths

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end live command dispatch (real PR comment `@bot /patch …` → SSM-published command → bridge expands template → warm/resume/cold-create) | GH-CMD-E2E | Requires a live GitHub App install, a configured repo, and real SSM/Lambda — cannot run in `go test` | Configure `github.commands` in `km-config.yaml`, `make build-lambdas && km init --dry-run=false`, comment `@klanker-maker /<cmd> <args>` on a PR in an allowlisted repo, confirm 👀 ACK + expanded-template dispatch to the override box; confirm a command-less comment runs the effective default; confirm multi-command + not-authorized + `/help` replies post |
| `km doctor` GitHub command health output (live SSM param present) | GH-CMD-SSM | The SSM-param-present check reads live SSM | Run `km doctor` after `km init`; confirm command checks pass and SSM-param-present is green |

*All pure logic (parsing, routing, auth, template expansion, `@file` resolution, config round-trip, doctor config-checks) has automated coverage; only live AWS/GitHub integration is manual.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (5 new test files)
- [ ] No watch-mode flags (all commands use `-count=1`)
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
