---
phase: 97
slug: github-comment-trigger-mvp
status: draft
nyquist_compliant: false
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

> Filled by the planner per PLAN.md task. Each GH-* requirement maps to a `go test` package
> except GH-E2E (manual, real AWS + GitHub).

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| TBD | — | — | GH-BRIDGE-VERIFY | unit (table-driven HMAC) | `go test ./pkg/github/bridge/ -run Signature` | ❌ W0 | ⬜ pending |
| TBD | — | — | GH-BRIDGE-AUTH | unit | `go test ./pkg/github/bridge/ -run Authorize` | ❌ W0 | ⬜ pending |
| TBD | — | — | GH-BRIDGE-ROUTE | unit | `go test ./pkg/github/bridge/ -run Resolve` | ❌ W0 | ⬜ pending |
| TBD | — | — | GH-INBOUND-Q | unit | `go test ./internal/app/cmd/ -run GitHubInbound` | ❌ W0 | ⬜ pending |
| TBD | — | — | GH-POLLER | unit | `go test ./pkg/compiler/ -run GitHubInbound` | ❌ W0 | ⬜ pending |
| TBD | — | — | GH-HELPER | unit | `go test ./... -run KmGithub` | ❌ W0 | ⬜ pending |
| TBD | — | — | GH-PROFILE | validate | `scripts/validate-all-profiles.sh` | ✅ | ⬜ pending |
| TBD | — | — | GH-CLI | unit | `go test ./internal/app/cmd/ -run GithubCmd` | ❌ W0 | ⬜ pending |
| TBD | — | — | GH-DOCTOR | unit | `go test ./internal/app/cmd/ -run DoctorGithub` | ❌ W0 | ⬜ pending |
| TBD | — | — | GH-APP-SCOPE | unit (manifest render) | `go test ./internal/app/cmd/ -run Manifest` | ❌ W0 | ⬜ pending |
| TBD | — | — | GH-BRIDGE-ROUTE (dormant) | unit (byte-identity when unconfigured) | `go test ./pkg/github/bridge/ -run Dormant` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/github/bridge/*_test.go` — signature, loop-guard, dedupe, mention, authorize, resolve, warm/cold dispatch (mocked AWS + GitHub)
- [ ] `internal/app/cmd/create_github_inbound_test.go` — queue provision/rollback/destroy (clone of slack-inbound test)
- [ ] `pkg/compiler/userdata_github_inbound_test.go` — source-aware poller emission
- [ ] `internal/app/cmd/configure_github_test.go` extension / `github_cmd_test.go` — init/manifest/status + `github.repos` config load + merge-list regression
- [ ] `github-review` profile added to `scripts/validate-all-profiles.sh` inventory

*Existing test infrastructure (`pkg/slack/bridge`, `create_slack_inbound_test.go`) provides the mocking patterns to clone.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end `@klanker-maker review` on a real PR ⇒ 👀 ⇒ Claude review posted | GH-E2E | Requires real GitHub App install, real webhook delivery, real AWS sandbox create | Configure `github.repos`, deploy (`make build-lambdas` + `km init --dry-run=false` + `--sidecars`), set App webhook URL/secret, comment on a PR from an allowlisted login; observe 👀 then a posted review |
| Cold-create path (first @-mention on a repo with no sandbox) | GH-E2E | Requires real EventBridge → create-handler provisioning | @-mention on a repo whose alias has no running sandbox; observe SandboxCreate, provision, first-boot prompt drain, review posted |
| Dormant when unconfigured (no `github:` block) | GH-BRIDGE-ROUTE | Best confirmed against a real deploy with no config | Deploy without `github:`; confirm no bridge dispatch path / no env regression on existing Lambdas |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 120s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
