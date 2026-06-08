---
phase: 100
slug: github-bridge-federated-relay-one-github-app-serving-many-resource-prefix-installs-via-github-peer-bridges
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-08
---

# Phase 100 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go stdlib `testing` (table-driven), go 1.25.5 |
| **Config file** | none — standard `go test` |
| **Quick run command** | `go test ./pkg/github/bridge/... -count=1` |
| **Full suite command** | `go test ./pkg/github/bridge/... ./internal/app/config/... ./internal/app/cmd/... -count=1` |
| **Estimated runtime** | ~15 seconds (no AWS; mocks only) |

> `make test` deliberately EXCLUDES `internal/app/cmd` and `cmd/km-*` (Makefile:75). The config + init + doctor tests live in `internal/app/{config,cmd}` and MUST be run with explicit package paths. The relayer + handler tests live in `pkg/github/bridge`, which `make test` DOES cover.

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/github/bridge/... -count=1` (relayer + handler — fast, no AWS)
- **After every plan wave:** Run the full suite command above
- **Before `/gsd:verify-work`:** Full suite green + `make build-lambdas` succeeds + `terraform validate` clean on the edited `lambda-github-bridge` module
- **Max feedback latency:** ~15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 100-01-xx | 01 | 0/1 | GH-FED-CONFIG | unit | `go test ./internal/app/config/... -run GithubPeerBridges -count=1` | ❌ W0 | ⬜ pending |
| 100-01-xx | 01 | 0/1 | GH-FED-CONFIG | unit | `go test ./internal/app/cmd/... -run GithubPeerBridges -count=1` | ❌ W0 | ⬜ pending |
| 100-02-xx | 02 | 0/1 | GH-FED-RELAY | unit | `go test ./pkg/github/bridge/... -run Relayer -count=1` | ❌ W0 | ⬜ pending |
| 100-02-xx | 02 | 0/1 | GH-FED-VERIFY | unit | `go test ./pkg/github/bridge/... -run RelayedVerify -count=1` | ❌ W0 | ⬜ pending |
| 100-03-xx | 03 | 0/1 | GH-FED-REORDER | unit | `go test ./pkg/github/bridge/... -run 'DecisionTable|Reorder' -count=1` | ❌ W0 | ⬜ pending |
| 100-03-xx | 03 | 0/1 | GH-FED-LOOPGUARD | unit | `go test ./pkg/github/bridge/... -run LoopGuard -count=1` | ❌ W0 | ⬜ pending |
| 100-03-xx | 03 | 0/1 | GH-FED-SCALE | unit (call-count mock) | `go test ./pkg/github/bridge/... -run NoWastedRead -count=1` | ❌ W0 | ⬜ pending |
| 100-04-xx | 04 | 2 | GH-FED-DOCTOR | unit | `go test ./internal/app/cmd/... -run GithubPeerBridges -count=1` | ❌ W0 | ⬜ pending |
| 100-04-xx | 04 | 2 | GH-FED-E2E | manual UAT | n/a — documented two-install runbook | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/github/bridge/relayer_test.go` — `HTTPPeerRelayer` unit tests (headers incl. `X-KM-Relayed: 1`, parallel POST to all peers, bounded ctx, tolerates a failing peer) — covers GH-FED-RELAY, GH-FED-VERIFY
- [ ] `pkg/github/bridge/webhook_handler_phase100_test.go` — `{relayed?, matched?}` 4-row decision table, reorder correctness (no-mention known-thread follow-up: owned→dispatch, peer-owned→relay), loop guard (relayed+miss never re-relays), no-wasted-read scale assertion — covers GH-FED-REORDER, GH-FED-LOOPGUARD, GH-FED-SCALE
- [ ] `internal/app/config/config_test.go` — `TestLoadGithubPeerBridges_Set` (+ nil-when-absent + a test PROVING no new merge-list entry is required, mirroring `TestLoadSlackPeerBridges_Set` at :880) — covers GH-FED-CONFIG
- [ ] `internal/app/cmd/init_test.go` — `KM_GITHUB_PEER_BRIDGES` env export + env-wins drift WARN — covers GH-FED-CONFIG
- [ ] `internal/app/cmd/doctor_github_test.go` — `checkGitHubPeerBridges` cases (malformed URL → WARN, self-loop → WARN, empty → OK/SKIP) — covers GH-FED-DOCTOR
- [ ] UAT runbook doc — two-install / one-App live verification — covers GH-FED-E2E
- [ ] Confirm/extend the existing handler test mocks (`SandboxAliasResolver` / thread store in `handle_test.go`) support call-count assertions for the no-wasted-read test; extend only if not already countable

*Framework already installed; no new test deps.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Two installs (`kph` + `sec`), one GitHub App: a PR comment on a `sec`-owned repo delivered to the `kph` "front-door" bridge relays and is processed by `sec`; exactly ONE 👀 reaction (owner only); front door posts none | GH-FED-E2E | Requires two live installs sharing one GitHub App webhook + real GitHub delivery; cannot be exercised by unit mocks | Documented runbook: configure `github.peer_bridges` on front door, point App webhook at front door, `km init --dry-run=false` both installs, comment on a `sec` repo, assert single 👀 + correct dispatch + no double-processing |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
