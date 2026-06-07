---
phase: 98
slug: github-bridge-expansion
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-07
---

# Phase 98 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (`go test ./...`), `testify` assertions, `httptest` server stubs |
| **Config file** | none — standard Go test runner |
| **Quick run command** | `go test ./cmd/km-github/... ./pkg/github/... ./pkg/github/bridge/... -count=1 -timeout 60s` |
| **Full suite command** | `go test ./... -count=1 -timeout 120s` |
| **Estimated runtime** | ~60–120 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./cmd/km-github/... ./pkg/github/... ./pkg/github/bridge/... -count=1 -timeout 60s`
- **After every plan wave:** Run `go test ./... -count=1 -timeout 120s` + `go build ./...`
- **Before `/gsd:verify-work`:** Full suite green + deploy-surface checklist (new DDB module in `regionalModules()`, bridge IAM ↔ runtime API call cross-check, Lambda env-block vars, lambda-zip build-list entry)
- **Max feedback latency:** ~120 seconds

---

## Per-Task Verification Map

| Req ID | Behavior | Test Type | Automated Command | File Exists | Status |
|--------|----------|-----------|-------------------|-------------|--------|
| GH-X-CHECK | `km-github check` posts check run; bad conclusion → non-zero exit | unit | `go test ./cmd/km-github/... -run TestCheck -count=1` | ❌ W0 | ⬜ pending |
| GH-X-PRCREATE | `km-github pr create` opens PR, prints html_url, returns 0 | unit | `go test ./cmd/km-github/... -run TestPRCreate -count=1` | ❌ W0 | ⬜ pending |
| GH-X-PUSH | `git push` in worktree uses km-git-credential-helper (no manual token) | integration | `go test ./pkg/compiler/... -run TestUserdataGitHubCredentialHelper -count=1` | ⚠️ may exist | ⬜ pending |
| GH-X-CONTINUITY | Bridge Upserts thread row on first dispatch; Get returns sandbox_id+sessionID on follow-up | unit | `go test ./pkg/github/bridge/... -run TestGitHubThreadStore -count=1` | ❌ W0 | ⬜ pending |
| GH-X-THREADBYPASS | Handle() skips mention check when (repo, number) in threads table | unit | `go test ./pkg/github/bridge/... -run TestHandle_ThreadBypass -count=1` | ❌ W0 | ⬜ pending |
| GH-X-SHARED | Multiple repo entries → same alias → same queue | unit | `go test ./pkg/github/bridge/... -run TestResolve_SharedAlias -count=1` | ⚠️ extend | ⬜ pending |
| GH-X-SHARED (doctor) | `km doctor` WARN on alias collision across repos | unit | `go test ./internal/app/cmd/... -run TestDoctorGitHubAliasCollision -count=1` | ❌ W0 | ⬜ pending |
| GH-X-RESUME | Handle() calls Resumer.StartSandbox when alias found with status=stopped/paused | unit | `go test ./pkg/github/bridge/... -run TestHandle_AutoResume -count=1` | ❌ W0 | ⬜ pending |
| GH-X-RESUME (IAM) | Bridge Lambda IAM has ec2:StartInstances; module-list guard | unit | `go test ./internal/app/cmd/... -run TestRegionalModulesIncludesGitHubThreads -count=1` | ❌ W0 | ⬜ pending |
| GH-COLD-CREATE (id) | EventBridgeAdapter generates valid sandbox_id (`^gh-[0-9a-f]{8}$`) | unit | `go test ./pkg/github/bridge/... -run TestEventBridgeAdapter_SandboxID -count=1` | ❌ W0 | ⬜ pending |
| GH-COLD-CREATE (prefix) | EventBridgeAdapter sets artifact_prefix = `github-profiles/{slug}` (no doubled path) + sets artifact_bucket | unit | `go test ./pkg/github/bridge/... -run TestEventBridgeAdapter_ArtifactPrefix -count=1` | ❌ W0 | ⬜ pending |
| GH-COLD-CREATE (staging) | `km init` pre-stages each github.repos profile to S3 `{bucket}/{prefix}/.km-profile.yaml` | unit | `go test ./internal/app/cmd/... -run TestPreStageGitHubProfiles -count=1` | ❌ W0 | ⬜ pending |
| GH-COLD-CREATE (auth) | Cold profile carries `spec.secrets.sopsFile` for Claude-cred injection (not Bedrock) | unit | `go test ./pkg/profile/... -run TestGitHubReviewProfileSecrets -count=1` | ❌ W0 | ⬜ pending |
| (infra) DDB threads module | `dynamodb-github-threads` present in `regionalModules()` + buildLambdaZips list intact | unit | `go test ./internal/app/cmd/... -run TestRegionalModulesIncludesGitHubThreads -count=1` | ❌ W0 | ⬜ pending |
| GH-X-E2E | Full chain on real AWS+GitHub: follow-up continues session; check run + opened PR visible; shared-alias dispatch across 2 repos to 1 box; stopped-alias @-mention auto-resumes & processes | manual UAT | — (see Manual-Only) | n/a | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `cmd/km-github/check_test.go` — unit tests for `runCheck` / `runCheckWith` (GH-X-CHECK)
- [ ] `cmd/km-github/prcreate_test.go` — unit tests for `runPRCreate` / `runPRCreateWith` (GH-X-PRCREATE)
- [ ] `pkg/github/bridge/thread_store_test.go` — `DynamoGitHubThreadStore` unit tests (GH-X-CONTINUITY)
- [ ] `pkg/github/bridge/webhook_handler_test.go` — add `TestHandle_ThreadBypass` + `TestHandle_AutoResume` (GH-X-THREADBYPASS, GH-X-RESUME)
- [ ] `pkg/github/bridge/aws_adapters_test.go` — add `TestEventBridgeAdapter_SandboxID` + `TestEventBridgeAdapter_ArtifactPrefix` (GH-COLD-CREATE)
- [ ] `pkg/github/bridge/resolve_test.go` — add `TestResolve_SharedAlias` (GH-X-SHARED)
- [ ] `internal/app/cmd/init_github_prestage_test.go` — `TestPreStageGitHubProfiles` (GH-COLD-CREATE)
- [ ] `internal/app/cmd/init_test.go` — add `TestRegionalModulesIncludesGitHubThreads` + `TestDoctorGitHubAliasCollision`
- [ ] `infra/modules/dynamodb-github-threads/v1.0.0/` — TF module (GH-X-CONTINUITY infra)
- [ ] `infra/live/use1/dynamodb-github-threads/terragrunt.hcl` — live unit

*Existing test infrastructure does NOT cover Phase 98 requirements — all files above are new (Wave 0 must create them). `resolve_test.go` and the compiler userdata test pre-exist and are extended in place.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Follow-up @-mention continues the same agent session | GH-X-E2E / GH-X-CONTINUITY | Needs a live agent session + real GitHub PR thread; session-resume is observable only end-to-end | @-mention on a PR; after the agent replies, post a follow-up without re-mentioning; confirm the reply references prior turn context (same session id in `km-github-threads`) |
| Check run + opened PR visible on GitHub | GH-X-E2E / GH-X-CHECK / GH-X-PRCREATE | Requires real GitHub App installation token + repo | Trigger a request that runs `km-github check` and `km-github pr create`; confirm the check run + PR appear in the GitHub UI |
| Shared-alias dispatch across two repos to one sandbox | GH-X-E2E / GH-X-SHARED | Requires two real repos pointing at one alias + a live shared box with worktree isolation | Configure 2 `github.repos:` entries → 1 alias; @-mention in each repo; confirm both dispatch to the same sandbox in separate worktrees |
| Stopped-alias @-mention auto-resumes and processes | GH-X-E2E / GH-X-RESUME | Requires a real stopped EC2 sandbox + bridge `ec2:StartInstances` IAM + post-boot FIFO drain | Stop an aliased sandbox; @-mention its repo; confirm the bridge resumes it (CloudWatch) and the poller drains the queued prompt after boot |
| Cold-create from truly-absent alias (SOPS auth, no Bedrock) | GH-X-E2E / GH-COLD-CREATE | Full EventBridge → create-handler → S3 profile fetch → SOPS cred injection → self-auth chain only exists live | @-mention a repo whose alias has no sandbox; confirm create-handler provisions it from the S3-staged profile, the box self-authenticates via SOPS Claude creds, and posts back |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 120s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
