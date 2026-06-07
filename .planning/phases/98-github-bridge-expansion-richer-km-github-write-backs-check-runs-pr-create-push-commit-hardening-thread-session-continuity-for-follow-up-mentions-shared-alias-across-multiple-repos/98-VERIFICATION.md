---
phase: 98-github-bridge-expansion
verified: 2026-06-07T21:08:34Z
status: passed
score: 9/9 must-haves verified
---

# Phase 98: GitHub Bridge Expansion Verification Report

**Phase Goal:** Build on Phase 97 — extend km-github with check-run + pr-create write verbs + push hardening; thread/session continuity (`(repo,number)→{sandbox_id,agent_session_id}`) so follow-up @-mentions continue the same agent session + thread-bypass; shared-alias across repos with km doctor overlap/collision warnings; stopped-sandbox auto-resume (bridge ec2:StartInstances, ~10s ack window, poller drains post-boot); and fix the broken cold-create path (valid sandbox_id + artifact bucket/prefix, km init S3 profile pre-staging, SOPS-injected Claude creds, dispatch unified with auto-resume).

**Verified:** 2026-06-07T21:08:34Z
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | `km-github check` posts a check run (name + conclusion + summary) and exits 0; bad conclusion exits non-zero | VERIFIED | `cmd/km-github/main.go:79,288-390` — `runCheck`/`runCheckWith` implemented; `TestCheck` + subtests green (`go test ./cmd/km-github/...`) |
| 2 | `km-github pr create` opens a PR from a branch and prints the PR html_url to stdout | VERIFIED | `cmd/km-github/main.go:87,392-470` — `runPRCreate`/`runPRCreateWith` implemented; `TestPRCreate` green |
| 3 | The GitHub poller preamble tells the agent to use a dedicated git worktree per PR and lists check + pr create + push verbs | VERIFIED | `pkg/compiler/userdata.go:2191,2204-2229` — worktree-per-PR + full verb list in preamble |
| 4 | A follow-up @-mention in a PR/issue whose (repo, number) is already tracked continues the SAME agent session (thread-bypass + continuity) | VERIFIED | `pkg/github/bridge/webhook_handler.go:186-208` thread-bypass; `aws_adapters.go:710-795` DynamoGitHubThreadStore; `userdata.go:2274-2295` resume fallback; `TestHandle_ThreadBypass` + `TestGitHubThreadStore` green |
| 5 | Multiple github.repos entries pointing at one explicit shared alias all resolve to that same alias; km doctor WARNs on alias collisions / match overlap | VERIFIED | `pkg/github/bridge/resolve.go` already supports shared alias (characterization test `TestResolve_SharedAlias` green); `internal/app/cmd/doctor.go:1077-1333` `detectGitHubAliasIssues` + `TestDoctorGitHubAliasCollision` green |
| 6 | An @-mention for a stopped/paused aliased sandbox auto-resumes it (ec2:StartInstances); the enqueued prompt drains after boot; status written back to running | VERIFIED | `pkg/github/bridge/aws_adapters.go:465-604` EC2Resumer (includes stopping-state tolerance Gap C); `webhook_handler.go:304-330` unified dispatch; IAM `infra/modules/lambda-github-bridge/v1.1.0/main.tf:221-229` scoped to km:resource-prefix (not km:managed); `SetStatusRunning` wired; `TestHandle_AutoResume` + `TestEC2Resumer` green; live UAT confirmed 2026-06-07 (98-UAT.md) |
| 7 | Cold-create emits a valid gh- sandbox_id, non-doubled artifact_prefix (github-profiles/{slug}), and non-empty artifact_bucket | VERIFIED | `pkg/github/bridge/aws_adapters.go:360-438` — `generateGitHubSandboxID()`, `profileSlug()`, fixed `PutSandboxCreate`; `TestEventBridgeAdapter_SandboxID` + `TestEventBridgeAdapter_ArtifactPrefix` green |
| 8 | km init pre-stages each github.repos profile (+ SOPS bundle) to S3; dynamodb-github-threads is in regionalModules | VERIFIED | `internal/app/cmd/init.go:295-296` — dynamodb-github-threads in regionalModules; `init.go:328+` preStageGitHubProfiles; `TestRegionalModulesIncludesGitHubThreads` + `TestPreStageGitHubProfiles` green |
| 9 | github-review.yaml self-authenticates a cold box via SOPS Claude creds (spec.secrets.sopsFile set, useBedrock:false) | VERIFIED | `profiles/github-review.yaml:118` — `sopsFile: github-review-secrets.enc.yaml`; `TestGitHubReviewProfileSecrets` green |

**Score:** 9/9 truths verified

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `cmd/km-github/main.go` | check + pr create verbs in dispatch() | VERIFIED | `runCheck`/`runCheckWith`/`runPRCreate`/`runPRCreateWith` present; dispatch cases at lines 79,87 |
| `pkg/compiler/userdata.go` | worktree-per-PR + verb-list preamble | VERIFIED | Lines 2191-2229 — git worktree + km-github check/pr create/push preamble |
| `pkg/github/bridge/aws_adapters.go` | DynamoGitHubThreadStore + ResolveByAliasWithStatus + EC2Resumer + fixed EventBridgeAdapter | VERIFIED | All four present; LookupSandbox/Upsert/UpdateSession/InvalidateStaleSession; StartSandbox with stopping-state tolerance; gh- sandbox_id + github-profiles/{slug} prefix |
| `pkg/github/bridge/webhook_handler.go` | thread-bypass + Upsert + unified dispatch (resume/cold-create) + SetStatusRunning call | VERIFIED | threadKnown at line 191; Upsert in warm+resume+cold branches; unified 3-way dispatch at line 270+; StatusWriter.SetStatusRunning at line 315 |
| `pkg/github/bridge/interfaces.go` | GitHubThreadStore + SandboxResumer + SandboxStatusWriter interfaces | VERIFIED | All three interfaces present; SetStatusRunning on SandboxStatusWriter |
| `infra/modules/dynamodb-github-threads/v1.0.0/main.tf` | km-github-threads table: hash=repo(S), range=number(N) | VERIFIED | hash_key=repo(S), range_key=number(N), ttl_expiry attribute, PAY_PER_REQUEST |
| `infra/live/use1/dynamodb-github-threads/terragrunt.hcl` | live unit sourcing the threads table | VERIFIED | Sources `dynamodb-github-threads/v1.0.0`; terraform state key correct |
| `infra/modules/lambda-github-bridge/v1.1.0/main.tf` | DDB-threads IAM (GetItem/PutItem/UpdateItem) + ec2:StartInstances (km:resource-prefix condition) + DDB-sandboxes UpdateItem | VERIFIED | All three policy blocks present; StartInstances condition uses km:resource-prefix NOT km:managed |
| `infra/live/use1/lambda-github-bridge/terragrunt.hcl` | sources v1.1.0 | VERIFIED | `source = .../lambda-github-bridge/v1.1.0` confirmed |
| `internal/app/cmd/init.go` | preStageGitHubProfiles + dynamodb-github-threads in regionalModules | VERIFIED | `dynamodb-github-threads` at line 295; `preStageGitHubProfiles` at line 328 |
| `profiles/github-review.yaml` | spec.secrets.sopsFile for cold-box Claude auth | VERIFIED | `sopsFile: github-review-secrets.enc.yaml` at line 118 |
| `docs/github-bridge.md` | Phase 98 operator runbook section | VERIFIED | Phase 98 section at line 488; covers check/pr-create, continuity, shared-alias, auto-resume, cold-create, deploy sequence |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `cmd/km-github/main.go runCheckWith` | `POST /repos/{owner}/{repo}/check-runs` | addGitHubHeaders + loadToken | WIRED | Pattern `check-runs` found at line 355; HTTP flow clones runReviewWith |
| `cmd/km-github/main.go runPRCreateWith` | `POST /repos/{owner}/{repo}/pulls` | addGitHubHeaders + loadToken | WIRED | Pattern `/pulls` found at line 447; html_url printed to stdout |
| `pkg/github/bridge/webhook_handler.go Handle()` | km-github-threads (LookupSandbox) | `h.Threads` short-circuit before mention check | WIRED | `Threads.LookupSandbox` at line 194; threadKnown gates mention filter |
| `pkg/github/bridge/webhook_handler.go Handle()` | ec2:StartInstances (EC2Resumer) | status==stopped/paused branch before enqueue | WIRED | `h.Resumer.StartSandbox` at line 309; unified dispatch at 304 |
| `pkg/github/bridge/webhook_handler.go Handle()` | SetStatusRunning after StartSandbox success | StatusWriter | WIRED | Line 315: `h.StatusWriter.SetStatusRunning(ctx, sandboxID)` non-fatal |
| `cmd/km-github-bridge/main.go` | EC2Resumer + DynamoGitHubThreadStore + DynamoSandboxStatusWriter | AWS clients wired to WebhookHandler | WIRED | Lines 153,186 — all three constructed and assigned |
| `internal/app/cmd/init.go preStageGitHubProfiles` | s3 github-profiles/{slug}/.km-profile.yaml | PutObject per unique profile slug | WIRED | `preStageGitHubProfiles` at line 328; called from init flow when github.repos non-empty |
| `pkg/compiler/userdata.go github poller` | km-github-threads (UpdateSession) | post-turn session write | WIRED | Pattern `agent_session_id` in poller block; `km-github-threads` UpdateItem after turn |
| `pkg/compiler/userdata.go github poller` | fresh-session fallback on stale --resume | Gap E fix | WIRED | Lines 2274-2295: detects "No conversation found", retries without --resume, clears stale DDB row |
| `pkg/github/bridge/webhook_handler.go` | InvalidateStaleSession on sandbox_id change | Gap E row invalidation | WIRED | Lines 284-292: stale sandbox_id detected, `InvalidateStaleSession` called before Upsert |

---

### Requirements Coverage

| Requirement | Source Plans | Description | Status | Evidence |
|-------------|-------------|-------------|--------|---------|
| GH-X-CHECK | 98-00, 98-01 | `km-github check` posts check run; bad conclusion → non-zero | SATISFIED | `runCheckWith` in main.go; `TestCheck` + subtests green |
| GH-X-PRCREATE | 98-00, 98-01 | `km-github pr create` opens PR, prints html_url | SATISFIED | `runPRCreateWith` in main.go; `TestPRCreate` green |
| GH-X-PUSH | 98-01 | Push-commit path hardened; worktree-per-PR preamble | SATISFIED | `pkg/compiler/userdata.go` preamble with worktree-add + push instruction; credential helper unchanged |
| GH-X-CONTINUITY | 98-00, 98-02, 98-06 | (repo,number)→{sandbox_id,agent_session_id} mapping; follow-up @-mentions continue session | SATISFIED | DynamoGitHubThreadStore; poller writes session_id post-turn + reads --resume on follow-up; Gap E stale-session fallback |
| GH-X-THREADBYPASS | 98-00, 98-02 | Replies in known PR/issue thread dispatch without re-@-mention | SATISFIED | threadKnown path in Handle(); `TestHandle_ThreadBypass` green |
| GH-X-SHARED | 98-00, 98-03 | Multiple repos → one shared alias; km doctor warns on collisions/overlap | SATISFIED | `TestResolve_SharedAlias` green (existing resolve.go supports it); `detectGitHubAliasIssues` + doctor check; `TestDoctorGitHubAliasCollision` green |
| GH-X-RESUME | 98-00, 98-04, 98-06 | Stopped/paused alias @-mention auto-resumes; status written back to running; stopping-state tolerance | SATISFIED | EC2Resumer with stopping-state tolerance (Gap C); SetStatusRunning (Gap B); IAM condition km:resource-prefix (Gap A); `TestHandle_AutoResume` green; live UAT confirmed (98-UAT.md: klanker-maker[bot] review posted at 19:11:34Z) |
| GH-COLD-CREATE | 98-00, 98-04, 98-06 | Valid sandbox_id + artifact_prefix; km init pre-stages profile + SOPS; SOPS cold-box auth; token mint robustness | SATISFIED | generateGitHubSandboxID(); profileSlug() + github-profiles/{slug} prefix; preStageGitHubProfiles; github-review.yaml sopsFile; Gap D token-mint robustness (granted-perms intersection + non-empty refresher input) |
| GH-X-E2E | 98-05, 98-06 | Full chain on real AWS+GitHub: continuity, write-backs, shared-alias, auto-resume | SATISFIED | Live UAT on 2026-06-07 (98-UAT.md): bridge → auto-resume (no 403) → DDB status→running → poller drain → worktree-per-PR → agent review → `km-github` POST to PR #11 confirmed unattended after Task 6 gap-closure. Task 6 checkpoint approved (commit 7b35483f). |

---

### Anti-Patterns Found

No blocker anti-patterns found in Phase 98 artifacts.

The following pre-existing test failures are out of scope for Phase 98:

| File | Test | Reason Out-of-Scope |
|------|------|---------------------|
| `cmd/km-slack` | `TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0` | Pre-existing (last commit Phase 70, 2026-05-24); explicitly called out in 98-UAT.md |
| `internal/app/cmd` | `TestRunAgentAuthClaude_TeesAndCleans` | OAuth flow test requiring interactive browser; pre-existing (no Phase 98 commits to agent_auth_test.go) |
| `internal/app/cmd` | `TestAtList_WithRecords` | Pre-existing scheduler test flakiness; no Phase 98 commits to at_test.go |
| `cmd/ttl-handler` | `TestHandleTTLEvent_UploadsArtifactsWhenConfigured` | EC2 IMDS timeout (requires live AWS); pre-existing |
| `pkg/hygiene` | `TestGoSourceNamesUseResourcePrefix` | Hardcoded `km-` prefix in doctor_artifacts.go + doctor_log_groups.go — both files last touched in Phase 94; no Phase 98 commits |

---

### Deploy Surface Verification

`TestDeploySurfaceGitHubBridgePhase98` (5 sub-tests) all pass:

| Sub-test | Assertion | Result |
|----------|-----------|--------|
| `dynamodb_github_threads_before_bridge` | dynamodb-github-threads in regionalModules(), ordered before lambda-github-bridge | PASS |
| `build_lists_complete` | km-github-bridge in LambdaBuildNames(), km-github in SidecarBuildNames() | PASS |
| `lambda_github_bridge_envreqs_artifacts_bucket` | lambda-github-bridge EnvReqs includes KM_ARTIFACTS_BUCKET | PASS |
| `iam_runtime_cross_check` | v1.1.0/main.tf contains km-github-threads, ec2:StartInstances, ec2:DescribeInstances, dynamodb:GetItem, dynamodb:UpdateItem | PASS |
| `live_unit_sources_v1_1_0` | infra/live/use1/lambda-github-bridge/terragrunt.hcl sources v1.1.0 | PASS |

Additional regression guard from 98-06: `ec2_resume_condition_uses_resource_prefix` asserts `aws:ResourceTag/km:resource-prefix` present AND `aws:ResourceTag/km:managed` absent — preventing the Gap A regression.

---

### Human Verification (GH-X-E2E)

The GH-X-E2E criterion is satisfied by live UAT evidence in `98-UAT.md`, not a pending human check.

**98-UAT.md records (2026-06-07):**
- Bridge @-mention → ACK → auto-resume (no 403, Gap A fix confirmed) → DDB status→running (Gap B confirmed) → poller drain (SSM queue URL fallback confirmed) → worktree `/workspace/pr-11` → agent review → `klanker-maker[bot]` posted review to PR #11 at 19:11:34Z
- Task 6 checkpoint approved (commit `7b35483f`): "all 5 gaps live-validated unattended"
- Gaps C/D/E fixes (Tasks 3-5 of 98-06) are committed (`22a0ab45`, `c9c37739`, `af8c97cb`) and `go build ./...` is green

The GH-X-E2E requirement is SATISFIED. No further human verification gate is blocking.

---

### Gaps Summary

No gaps. All 9 observable truths verified, all 9 requirement IDs satisfied, all key links wired, deploy-surface assertions pass, and live UAT confirms end-to-end behavior.

The five live-UAT defects (Gaps A-E) found on 2026-06-07 were all fixed in 98-06 and committed:
- Gap A (IAM condition km:managed→km:resource-prefix): `50e6c9b7`, regression test `e57ff4ba`
- Gap B (status write-back after resume): `1eda6f0e`, wired in main.go `94722eb9`
- Gap C (stopping-state timing race in EC2Resumer): `22a0ab45`
- Gap D (token mint robustness — granted-perms only + non-empty refresher input): `c9c37739`
- Gap E (stale/cross-box session fallback + row invalidation): `af8c97cb`

---

_Verified: 2026-06-07T21:08:34Z_
_Verifier: Claude (gsd-verifier)_
