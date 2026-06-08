---
phase: 101
slug: github-bridge-orphan-repo-helpful-reply-front-door-posts-guidance-when-no-install-owns-the-repo-claim-aware-scatter-gather-slack-phase-96-analog
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-08
---

# Phase 101 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go stdlib `testing` (table-driven), go 1.25.5 |
| **Config file** | none — standard `go test` |
| **Quick run command** | `go test ./pkg/github/bridge/... -count=1` |
| **Full suite command** | `go test ./pkg/github/bridge/... ./internal/app/config/... ./internal/app/cmd/... -count=1` |
| **Estimated runtime** | ~15 seconds (no AWS; mocks/httptest only) |

> `make test` deliberately EXCLUDES `internal/app/cmd` and `cmd/km-*` (Makefile:75). The config + init tests live in `internal/app/{config,cmd}` and MUST be run with explicit package paths. The relayer + handler tests live in `pkg/github/bridge`, which `make test` DOES cover.

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/github/bridge/... -count=1` (relayer + handler — fast, no AWS)
- **After every plan wave:** Run the full suite command above
- **Before `/gsd:verify-work`:** Full suite green + `make build-lambdas` succeeds + `terraform validate` clean on `lambda-github-bridge/v1.1.0`
- **Max feedback latency:** ~15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 101-01-xx | 01 | 1 | GH-ORPHAN-ROLLOUT | unit | `go test ./internal/app/config/... -run GithubDefaultRouter -count=1` | ❌ W0 | ⬜ pending |
| 101-01-xx | 01 | 1 | GH-ORPHAN-ROLLOUT | unit | `go test ./internal/app/cmd/... -run GithubDefaultRouter -count=1` | ❌ W0 | ⬜ pending |
| 101-02-xx | 02 | 1 | GH-ORPHAN-CLAIM | unit | `go test ./pkg/github/bridge/... -run ClaimTally -count=1` | ❌ W0 | ⬜ pending |
| 101-02-xx | 02 | 1 | GH-ORPHAN-ROLLOUT | unit | `go test ./pkg/github/bridge/... -run 'RelayerLegacy|Rollout' -count=1` | ❌ W0 | ⬜ pending |
| 101-03-xx | 03 | 2 | GH-ORPHAN-CLAIM | unit | `go test ./pkg/github/bridge/... -run PeerClaim -count=1` | ❌ W0 | ⬜ pending |
| 101-03-xx | 03 | 2 | GH-ORPHAN-REPLY | unit | `go test ./pkg/github/bridge/... -run OrphanComment -count=1` | ❌ W0 | ⬜ pending |
| 101-03-xx | 03 | 2 | GH-ORPHAN-COOLDOWN | unit | `go test ./pkg/github/bridge/... -run OrphanCooldown -count=1` | ❌ W0 | ⬜ pending |
| 101-03-xx | 03 | 2 | GH-ORPHAN-ROLLOUT | unit | `go test ./pkg/github/bridge/... -run DefaultRouterOff -count=1` | ❌ W0 | ⬜ pending |
| 101-04-xx | 04 | 3 | GH-ORPHAN-E2E | manual UAT | n/a — documented two-install runbook (101-UAT.md) | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

> Wave/plan split is the researcher's suggested shape — the planner may refine. The invariant: every automated row maps to a `pkg/github/bridge` or `internal/app/{config,cmd}` test; GH-ORPHAN-E2E is the sole manual deliverable.

---

## Wave 0 Requirements

- [ ] `pkg/github/bridge/relayer_test.go` — extend for the `Broadcast` signature change (`error` → `([]PeerClaimResult, error)`): claim-result tally, legacy-`"ok"`-no-body → `Claimed:true`, non-2xx → `Claimed:true`, timeout → `Claimed:true`, `{claimed:false}` parse — covers GH-ORPHAN-CLAIM, GH-ORPHAN-ROLLOUT
- [ ] `pkg/github/bridge/webhook_handler_phase101_test.go` — peer-side claim emit (relayed-miss → `{claimed:false}`, relayed-owned → `{claimed:true}`, non-relayed owned → plain ok), front-door tally short-circuit (any claim ⇒ no post), orphan happy-path (zero claims + mention + cooldown-clear ⇒ exactly ONE PostComment), non-mention skip, `DefaultRouter=false` silent (no tally, no post), cooldown suppress/expire, `Commenter==nil` skip, `Installation.ID==0` skip — covers GH-ORPHAN-CLAIM, GH-ORPHAN-REPLY, GH-ORPHAN-COOLDOWN, GH-ORPHAN-ROLLOUT
- [ ] Test doubles: `fakePeerRelayer.Broadcast` returns `([]PeerClaimResult, error)`; reuse/extend the `CommentPoster` test double from `webhook_handler_phase99_test.go` to capture `PostComment` calls; a fake cooldown store (reuse the `DeliveryNonceStore` fake) with configurable `seen` state
- [ ] `internal/app/config/config_test.go` — `TestLoadGithubDefaultRouter_Set` (round-trip + nil/false-when-absent + a test PROVING no new merge-list entry is required) — covers GH-ORPHAN-ROLLOUT
- [ ] `internal/app/cmd/init_test.go` — `KM_GITHUB_DEFAULT_ROUTER` env export + env-wins drift WARN — covers GH-ORPHAN-ROLLOUT
- [ ] `101-UAT.md` — two-install / one-App / unowned-repo runbook — covers GH-ORPHAN-E2E

*No doctor test: Slack Phase 96 shipped no `default_router` doctor check (verified empty grep in RESEARCH); none required here. Framework already installed; no new test deps.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Two installs (`kph` + `sec`), one GitHub App, front door `github.default_router: true`: an @-mention on a repo NO install owns ⇒ exactly ONE guidance comment from the front door; an @-mention on an owned repo ⇒ no guidance comment (owner dispatches normally); a second mention on the same unowned PR within the cooldown window ⇒ no second comment | GH-ORPHAN-E2E | Requires two live installs sharing one GitHub App webhook + real GitHub delivery + real PR/issue; cannot be exercised by unit mocks | Documented `101-UAT.md` runbook: set `github.default_router: true` + `github.peer_bridges` on front door, `km init --dry-run=false` both installs, comment on an unowned repo → assert one guidance comment; comment on an owned repo → assert none; re-comment within window → assert no repeat |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
