---
phase: 97-github-comment-trigger-mvp
verified: 2026-06-06T00:00:00Z
updated: 2026-06-06T22:11:00Z
status: human_needed
score: 11/11 must-haves verified (GH-E2E is human-only UAT)
re_verification:
  previous_status: gaps_found
  previous_score: 9/11 must-haves verified (1 code gap found post-verification, 1 human-only)
  gaps_closed:
    - "GH-BRIDGE-DEPLOY: infra/live/use1/lambda-github-bridge/terragrunt.hcl created; lambda-github-bridge added to init.go regionalModules() ordered list + 5-min timeout case; go build clean; TestRegionalModulesIncludesGitHubBridge + TestUninitDestroyOrder pass"
  gaps_remaining: []
  regressions: []
human_verification:
  - test: "Deploy bridge Lambda + configure GitHub App, then @-mention bot on a real PR from allowlisted login"
    expected: "👀 reaction within ~10s ack window; Claude runs in per-repo sandbox (warm path: enqueue; cold path: SandboxCreate carries envelope); PR review comment posted by bot via km-github review"
    why_human: "Requires real GitHub webhook delivery + real AWS deploy; cannot be tested programmatically (GH-E2E gate)"
  - test: "Redeliver the same webhook via GitHub UI"
    expected: "No double dispatch — GUID dedupe via nonces table silently drops replay"
    why_human: "Requires real GitHub App event delivery"
  - test: "@-mention from non-allowlisted login"
    expected: "No 👀 reaction, no dispatch, no log trace of sandbox touched (silent 200)"
    why_human: "Requires real GitHub webhook; silent path has no observable side-effect to grep for"
---

# Phase 97: GitHub Comment-Trigger MVP Verification Report

**Phase Goal:** Let an operator @-mention the klanker-maker GitHub App bot in a PR/issue comment and have the platform dispatch the request to an aliased per-repo sandbox (creating it cold if absent), where Claude reviews the PR and posts back via a new sandbox-side km-github helper — the GitHub-shaped twin of the Slack inbound path.

**Verified:** 2026-06-06
**Updated:** 2026-06-06 (re-verification after plan 97-07 gap closure)
**Status:** human_needed — all code-level requirements verified; GH-E2E manual UAT pending
**Re-verification:** Yes — after gap closure (GH-BRIDGE-DEPLOY closed by plan 97-07)

---

## Re-verification Summary

Plan 97-07 closed the single blocker gap, GH-BRIDGE-DEPLOY, by:

1. Creating `infra/live/use1/lambda-github-bridge/terragrunt.hcl` — sources `infra/modules/lambda-github-bridge/v1.0.0`, two dependency blocks (`dynamodb-sandboxes`, `dynamodb-slack-nonces`) each with `"show"` in `mock_outputs_allowed_terraform_commands`, no `required_providers`, maps all three no-default inputs (`lambda_zip_path`, `sandboxes_table_arn`, `nonces_table_arn`), and passes `github_repos_json` via `get_env("KM_GITHUB_REPOS", "")`.
2. Registering `lambda-github-bridge` in `internal/app/cmd/init.go` `regionalModules()` ordered list at line 302 (after `lambda-slack-bridge` at 293, before `ses` at 310), and in the 5-min `defaultModuleTimeout` `case` at line 184.

Verification checks run:
- `infra/live/use1/lambda-github-bridge/terragrunt.hcl` — EXISTS, all structural requirements met (confirmed above)
- `go build ./...` — CLEAN (no output)
- `go test ./internal/app/cmd/ -run 'RegionalModules|UninitDestroyOrder|RunInitPlan_ModuleOrder' -count=1` — ALL PASS: `TestRunInitPlan_ModuleOrder`, `TestRegionalModulesIncludesSSMDoc`, `TestRegionalModulesIncludesEFS`, `TestRegionalModulesIncludesSlackBridge`, `TestRegionalModulesIncludesGitHubBridge`, `TestUninitDestroyOrder`

GH-BRIDGE-ROUTE is promoted from PARTIAL to SATISFIED. Phase status is promoted from `gaps_found` to `human_needed`. The sole remaining item is the GH-E2E manual UAT.

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `github.repos` list-of-objects round-trips through `config.Load` (merge-list footgun prevented) | VERIFIED | `GithubConfig` struct at `config.go:107`, merge-list `"github"` at `:484`, `UnmarshalKey("github",...)` at `:607`; config tests pass |
| 2 | `km init` exports `KM_GITHUB_REPOS` as JSON with env-wins drift WARN; absent config exports nothing | VERIFIED | `init.go:931-954`: gates on `len(cfg.Github.Repos) > 0`, marshals JSON, drift WARN at `:951-952` |
| 3 | `km github manifest/init/status` operator commands are substantive (scopes, SSM writes, status print) | VERIFIED | `github.go` 325 lines: `manifest` renders write scopes + issue_comment webhook; `init` writes 3 SSM keys + random secret; `status` reads + redacts; `configure_github.go` at `:157-282` |
| 4 | Bridge verifies X-Hub-Signature-256 HMAC-SHA256 raw-body constant-time; bad/absent → 401 | VERIFIED | `webhook_handler.go:258-270`: `hmac.Equal` constant-time; sha256= prefix check; 401 return at `:119` |
| 5 | Bridge: loop guard / dedupe / mention / deny-by-default allowlist; warm enqueue + cold SandboxCreate-with-envelope; sync 👀; 200 on error | VERIFIED | 11-step `Handle()` ordering confirmed at `webhook_handler.go:44-57,108-254`; `PutSandboxCreate` called at `:223`; `SQS.Send` at `:234`; reaction at `:241-252`; 200 on SQS error |
| 6 | Resolve: exact-before-glob, first-match-wins, alias defaults `gh-{owner}-{repo}` | VERIFIED | `resolve.go:37-82`: pass-1 exact, pass-2 glob; alias default confirmed; `resolve_test.go` covers all branches |
| 7 | `spec.notification.github.inbound.enabled` provisions FIFO queue + DDB attr + SSM + env; disabled = zero artifacts | VERIFIED | `create_github_inbound.go`: gates via `notificationGitHubInbound`; creates queue, writes `github_inbound_queue_url` DDB attr, SSM param, env; `destroy_github_inbound.go` cleans up; wired into `create.go:1141,1174` |
| 8 | `GithubInboundQueueURL` round-trips all 4 metadata spots (struct/copy/unmarshal/marshal) | VERIFIED | `metadata.go:51-57`; `sandbox_dynamo.go:140,298,421` — all four spots confirmed |
| 9 | Sandbox-side poller drains github-inbound queue, builds preamble, dispatches to agent; dormant when disabled | VERIFIED | `userdata.go:2080-2234`: poller heredoc; preamble building; systemd unit at `:2538-2556`; gate `{{ if .GitHubInboundEnabled }}` at `:1114,2079,2536,3683`; compiler tests pass |
| 10 | `km-github comment/review` posts back via per-sandbox installation token with correct API shape | VERIFIED | `cmd/km-github/main.go` 298 lines: `comment` → `POST issues/{n}/comments`; `review` → `POST pulls/{n}/reviews` with event validation; token read from SSM; version header set; tests pass |
| 11 | `km doctor` reports GitHub bridge health; skips silently when unconfigured | VERIFIED | `doctor.go:877-1093`: `checkGitHubConfig`, `checkGitHubWebhookSecret`, `checkGitHubBotLoginCached`, `checkGitHubBridgeURL`, `checkGitHubReposResolvable`; silent skip gate at `:3003-3041` |
| E2E | Real PR @-mention → 👀 → Claude runs (warm + cold) → review posted; unauthorized silent; dedupe works | HUMAN NEEDED | GH-E2E: requires real GitHub App + real AWS deploy; operator UAT pending |

**Score:** 11/11 code-level truths verified (GH-E2E is human-only by design)

---

### Required Artifacts

| Artifact | Provides | Status | Detail |
|----------|----------|--------|--------|
| `internal/app/config/config.go` | `GithubConfig` struct + merge-list + `UnmarshalKey` | VERIFIED | `GithubRepoEntry` at `:83`; merge-list `"github"` at `:484`; `UnmarshalKey` at `:607` |
| `internal/app/config/config_github_test.go` | Round-trip + dormant + merge-list tests | VERIFIED | Tests pass: `go test ./internal/app/config/ -run GitHub` |
| `internal/app/cmd/github.go` | `km github manifest/init/status` | VERIFIED | 325 lines, substantive; all three commands implemented |
| `internal/app/cmd/configure_github.go` | Extended SSM keys: webhook-secret, bot-login, bridge-url | VERIFIED | Lines `:157-282`: all three keys written |
| `internal/app/cmd/init.go` | `KM_GITHUB_REPOS` JSON env export + `lambda-github-bridge` in ordered module list | VERIFIED | Lines `:931-954` (env export); `:184` (5-min timeout case); `:302-304` (ordered list, after slack-bridge, before ses) |
| `infra/live/use1/lambda-github-bridge/terragrunt.hcl` | Live terragrunt unit for bridge deploy | VERIFIED | Sources `infra/modules/lambda-github-bridge/v1.0.0`; dependency blocks for `dynamodb-sandboxes` + `dynamodb-slack-nonces` (each with "show" in mock_outputs_allowed_terraform_commands); no `required_providers`; all 3 no-default inputs mapped; `github_repos_json` via `get_env("KM_GITHUB_REPOS", "")` |
| `pkg/github/token.go` | `CompilePermissions` extended with comment/review write scopes | VERIFIED | `:191-230`: comment→issues:write, review→pull_requests:write, inbound write set |
| `pkg/aws/eventbridge.go` | `SandboxCreateDetail.GithubEnvelope` | VERIFIED | `:37` field present |
| `pkg/aws/sqs.go` | `GitHubInboundQueueName` + `CreateGitHubInboundQueue` + `DeleteGitHubInboundQueue` | VERIFIED | `:109-153`: all three helpers present |
| `cmd/create-handler/main.go` | `CreateEvent.GithubEnvelope` + post-provision enqueue | VERIFIED | `:65`: field; `:408-445`: `drainGithubEnvelope` called when non-empty |
| `pkg/profile/types.go` | `NotificationGitHubSpec` / `NotificationGitHubInboundSpec` tri-state | VERIFIED | `:90-102`: `Github *NotificationGitHubSpec` on `NotificationSpec` |
| `pkg/profile/schemas/sandbox_profile.schema.json` | `notification.github.inbound.enabled` schema | VERIFIED | Line `:385,670,682`: github block under notification |
| `pkg/profile/inherit.go` | `mergeNotificationGitHubSpec` field-level merge | VERIFIED | `:148,155,163,167`: merge functions wired |
| `pkg/aws/metadata.go` | `GithubInboundQueueURL` in struct | VERIFIED | `:51-57` |
| `pkg/aws/sandbox_dynamo.go` | `GithubInboundQueueURL` in copy/unmarshal/marshal | VERIFIED | `:140,298,421` — all four spots |
| `internal/app/cmd/create_github_inbound.go` | Provision/rollback/teardown | VERIFIED | 116+ lines; substantive; wired into `create.go:1141,1174` |
| `internal/app/cmd/destroy_github_inbound.go` | Teardown/drain | VERIFIED | File exists and wired |
| `pkg/github/bridge/webhook_handler.go` | 11-step `Handle()` ordering | VERIFIED | 304 lines; all branches confirmed |
| `pkg/github/bridge/resolve.go` | Pure `Resolve()` exact-before-glob | VERIFIED | `:37-82`; exact pass-1, glob pass-2 |
| `pkg/github/bridge/interfaces.go` | `SQSSender`, `EventNonceStore`, `Reactor`, etc. | VERIFIED | File present |
| `pkg/github/bridge/aws_adapters.go` | SSM secret fetcher, `DynamoNonceStore`, `SQSAdapter`, `GitHubReactor` | VERIFIED | File present |
| `pkg/github/bridge/payload.go` | `issue_comment` payload structs | VERIFIED | File present |
| `cmd/km-github-bridge/main.go` | Lambda entrypoint; `KM_GITHUB_REPOS` JSON parse; Function URL dispatch | VERIFIED | `:102-121`: JSON unmarshal at init |
| `infra/modules/lambda-github-bridge/v1.0.0/main.tf` | Lambda + Function URL + IAM | VERIFIED | EventBridge `:162`, SSM `:84-85`, DDB `:123`, SQS `:151`, timeout 60s `:195`, `KM_GITHUB_REPOS` env `:201`, no `required_providers` |
| `infra/modules/lambda-github-bridge/v1.0.0/outputs.tf` | `function_url` output | VERIFIED | `:11-13` |
| `pkg/compiler/userdata.go` | Github-inbound poller heredoc + systemd unit + `GitHubInboundEnabled` gate | VERIFIED | `:2080-2234,2538-2556,4355-4914` |
| `cmd/km-github/main.go` | `comment` + `review` verbs | VERIFIED | 298 lines; both verbs substantive |
| `profiles/github-review.yaml` | Lean built-in github-review profile | VERIFIED | `notification.github.inbound.enabled: true` at `:103`; validates |
| `scripts/validate-all-profiles.sh` | github-review.yaml in inventory | VERIFIED | Line `:24` |
| `internal/app/cmd/doctor.go` | 5 GitHub bridge checks + silent skip | VERIFIED | `:877-1093,3003-3093` |
| `docs/github-bridge.md` | Operator runbook + deploy sequence | VERIFIED | 420 lines; deploy sequence at `:284-315` |

---

### Key Link Verification

| From | To | Via | Status | Detail |
|------|----|-----|--------|--------|
| `config.go Load` | `cfg.Github` | v2 merge-list `"github"` + `UnmarshalKey` | WIRED | `:484` + `:607` |
| `init.go ExportTerragruntEnvVars` | `KM_GITHUB_REPOS` env | `json.Marshal` gate `len > 0` | WIRED | `:931-954` |
| `init.go regionalModules()` | `lambda-github-bridge` terragrunt unit | ordered list entry at `:302-304`; 5-min case `:184` | WIRED | After `lambda-slack-bridge` (:293), before `ses` (:310) |
| `infra/live/use1/lambda-github-bridge/terragrunt.hcl` | `infra/modules/lambda-github-bridge/v1.0.0` | `terraform { source = ... }` | WIRED | `:32` |
| `cmd/km-github-bridge/main.go` | `KM_GITHUB_REPOS` env JSON | `json.Unmarshal` at cold start | WIRED | `:102-121` |
| `webhook_handler cold path` | `PutSandboxCreateEvent` | `SandboxCreateDetail.GithubEnvelope` | WIRED | `webhook_handler.go:223`; `eventbridge.go:37` |
| `webhook_handler warm path` | github-inbound FIFO | `SQS.Send` to alias-resolved queue URL | WIRED | `webhook_handler.go:234` |
| `create-handler` | github-inbound FIFO | `drainGithubEnvelope` → `SendMessage` to `GitHubInboundQueueName` | WIRED | `create-handler/main.go:408-445` |
| `spec.notification.github.inbound.enabled` | `provisionGitHubInboundQueue` | gate at `create.go:1141` | WIRED | `create.go:1141,1174` |
| `pkg/aws/metadata.go SandboxMetadata` | `sandbox_dynamo.go` marshal/unmarshal/copy | `GithubInboundQueueURL` in all 4 spots | WIRED | `:140,298,421` |
| `userdata.go github poller` | agent tmux dispatch | preamble + body to agent-run; `GitHubInboundEnabled` gate | WIRED | `:2080-2234; 3683-3691` |
| `cmd/km-github` | GitHub REST API | per-sandbox token from SSM + `GitHubAPIBaseURL` | WIRED | `main.go:127,194`; tested against httptest |
| `km doctor github checks` | SSM `/{prefix}/config/github/*` + `cfg.Github.Repos` | presence + resolvability checks | WIRED | `doctor.go:877-1093,3003-3093` |

---

### Requirements Coverage

| Requirement | Source Plan(s) | Description | Status | Evidence |
|-------------|----------------|-------------|--------|----------|
| GH-APP-SCOPE | 97-01, 97-04 | Write scopes + issue_comment webhook; `km github manifest`; SSM keys | SATISFIED | `github.go:82-132`; `configure_github.go:157-282`; `token.go:224-230`; `main.tf:201` |
| GH-BRIDGE-VERIFY | 97-04 | HMAC-SHA256 raw-body constant-time verify; bad sig → 401 | SATISFIED | `webhook_handler.go:258-270,119` |
| GH-BRIDGE-AUTH | 97-04 | Loop guard / dedupe / mention / deny-by-default allowlist | SATISFIED | `webhook_handler.go:44-175`; all branches tested |
| GH-BRIDGE-ROUTE | 97-02, 97-04 | Resolve owner/repo → alias/profile; warm enqueue / cold SandboxCreate; 👀 + 200; bridge reachable by `km init` | SATISFIED | Handler logic: `resolve.go:46-82`; `webhook_handler.go:197-254`; Deploy path: `infra/live/use1/lambda-github-bridge/terragrunt.hcl` (:32,58-60,74); `init.go:302-304,184` |
| GH-INBOUND-Q | 97-03 | `spec.notification.github.inbound.enabled`; FIFO + DDB + SSM + env; destroy cleanup | SATISFIED | `create_github_inbound.go`; `destroy_github_inbound.go`; `metadata.go:57`; `sandbox_dynamo.go:140,298,421` |
| GH-POLLER | 97-05 | Source-aware poller; GitHub preamble; agent dispatch; dormant when disabled | SATISFIED | `userdata.go:2080-2234`; `GitHubInboundEnabled` gate; tests pass |
| GH-HELPER | 97-05 | `km-github comment/review`; per-sandbox write-scoped token; correct API shape | SATISFIED | `cmd/km-github/main.go:94-215`; httptest tests pass |
| GH-PROFILE | 97-03 | `github-review` lean profile; validates; in validate-all-profiles.sh | SATISFIED | `profiles/github-review.yaml`; `scripts/validate-all-profiles.sh:24` |
| GH-CLI | 97-01 | `km github init/manifest/status`; `github.repos` round-trips; `KM_GITHUB_REPOS` env | SATISFIED | `github.go`; `init.go:931-954`; config tests pass |
| GH-DOCTOR | 97-06 | Doctor checks: App/secret/bot-login/bridge-url/resolvability + overlap; silent skip | SATISFIED | `doctor.go:877-1093,3003-3093`; doctor tests pass |
| GH-E2E | 97-06 | Real @-mention → 👀 → Claude runs → review posted (warm + cold + negative) | HUMAN NEEDED | Manual UAT; real GitHub App + real AWS deploy required; not yet executed |

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/app/cmd/github.go` | 93 | `"placeholder"` in comment: `// placeholder; operator fills in after Lambda deploy` | INFO | Expected behavior — the manifest renders a placeholder URL when `--bridge-url` not yet supplied. Not a stub. |

No blocker anti-patterns found. No `TODO`/`FIXME` in Phase 97 files. No empty implementations.

---

### Pre-existing Test Failures (Not Phase 97 Regressions)

Two test suites fail but are confirmed pre-existing from earlier phases:

1. **`pkg/hygiene: TestGoSourceNamesUseResourcePrefix`** — `doctor_log_groups.go` hardcoded `km-github-token-refresher-` and three other literals introduced in Phase 94 commit `af50bb69` (2026-06-04). The hygiene gate (`65c94c52`, 2026-05-31) predates this. Phase 97 did not touch `doctor_log_groups.go`.

2. **`internal/app/cmd` suite** — `TestUnlockCmd_RequiresStateBucket`, `TestShellDockerContainerName`, and others. None of these test files were modified in any Phase 97 commit (confirmed via `git log --since="2026-06-05"`). Phase 97-specific packages (`pkg/github`, `pkg/github/bridge`, `pkg/compiler`, `pkg/aws`, `internal/app/config`, `cmd/km-github`, `cmd/create-handler`) all pass.

---

### Human Verification Required

#### 1. Warm path E2E

**Test:** Deploy the bridge Lambda (`make build-lambdas` + `km init --dry-run=false`), configure `github.repos:` for an allowlisted repo, pre-warm the sandbox (`km create profiles/github-review.yaml --alias gh-{owner}-{repo}`), then @-mention the bot login on a real PR.
**Expected:** 👀 reaction appears within ~10 seconds, Claude runs in the sandbox, and a PR review comment is posted by the bot via `km-github review`.
**Why human:** Requires live GitHub App webhook delivery to a real Lambda URL + real AWS execution. Cannot be mocked.

#### 2. Cold path E2E

**Test:** Ensure no sandbox exists for the alias, then @-mention the bot on a PR in the configured repo.
**Expected:** `SandboxCreate` EventBridge event fires, `create-handler` provisions a new sandbox, the carried `GithubEnvelope` is drained into the github-inbound FIFO on first boot, and a review is eventually posted.
**Why human:** Requires real `SandboxCreate` provisioning cycle against real AWS infrastructure.

#### 3. Negative: non-allowlisted login

**Test:** Comment from a GitHub login NOT in the `allow:` list.
**Expected:** No 👀 reaction, no dispatch, no sandbox interaction (silent 200 at step 7).
**Why human:** Silent path has no observable side-effect; requires real webhook to confirm the Lambda returns 200 without reacting.

#### 4. Negative: GUID dedupe

**Test:** Use GitHub UI "Redeliver" to replay the same `X-GitHub-Delivery` GUID.
**Expected:** No second dispatch — nonces table deduplication returns 200 silently on replay.
**Why human:** Requires real GitHub App redelivery.

---

### Gaps Summary

**GH-BRIDGE-DEPLOY (closed by plan 97-07):** The `km-github-bridge` Lambda deploy gap that was the sole blocker in the initial verification is now resolved. The live terragrunt unit `infra/live/use1/lambda-github-bridge/terragrunt.hcl` exists, sources the correct module, passes the correct inputs and dependency ARNs, and includes `"show"` in both dependency blocks' `mock_outputs_allowed_terraform_commands`. The ordered module list in `init.go` includes `lambda-github-bridge` at line 302 (after `lambda-slack-bridge`, before `ses`) and the 5-min timeout case at line 184. `go build ./...` is clean and all targeted tests pass, including `TestRegionalModulesIncludesGitHubBridge` and `TestUninitDestroyOrder`.

All 11 code-level requirements (GH-APP-SCOPE through GH-DOCTOR) are fully implemented, wired, deployed, and tested. GH-BRIDGE-ROUTE is now SATISFIED (was PARTIAL). The only remaining item is GH-E2E, which is a manual operator UAT requiring a real GitHub webhook and real AWS deploy.

**Next step:** Operator runs `make build-lambdas` (clean) + `km init --dry-run=false` to deploy the bridge, then executes the GH-E2E UAT items above.

---

_Verified: 2026-06-06_
_Re-verified: 2026-06-06 (after plan 97-07 gap closure)_
_Verifier: Claude (gsd-verifier)_
