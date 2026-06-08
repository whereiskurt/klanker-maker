---
phase: 101-github-bridge-orphan-repo-helpful-reply-front-door-posts-guidance-when-no-install-owns-the-repo-claim-aware-scatter-gather-slack-phase-96-analog
verified: 2026-06-08T00:00:00Z
status: passed
score: 15/15 must-haves verified
re_verification: false
e2e_verified:
  - test: "GH-ORPHAN-E2E — live single-install self-as-peer harness (2026-06-08)"
    result: "PASS (Tests A, B, C); Test D skipped (rollout-safety unit-covered)"
    evidence: "See 101-UAT.md § Notes / Findings. Live km-github-bridge Lambda, acct 052251888500 us-east-1. Test A: orphan @-mention → exactly one guidance comment from klanker-maker[bot], 0 reactions, no dispatch. Test B: cooldown key present (ttl ~3600s), no second comment. Test C: owned repo → warm enqueue to sandbox learn-d510e339, one 👀, no guidance comment (router on). Design confirmed: orphan reply gated behind Relayer!=nil, mirroring Slack Phase 96 (nil Relayer ⇒ no reply)."
---

# Phase 101: GitHub Bridge Orphan-Repo Helpful Reply — Verification Report

**Phase Goal:** GitHub bridge orphan-repo helpful reply — when an allowlisted login @-mentions the bot on a PR/issue in a repo NO install owns, the front-door install (with github.default_router:true) posts ONE helpful guidance comment, using claim-aware scatter-gather across github.peer_bridges (Slack Phase 96 analog). Dormant by default; rollout-safe with Phase-100 peers; per-(repo,number) cooldown.
**Verified:** 2026-06-08
**Status:** passed — all automated checks pass; GH-ORPHAN-E2E verified live on 2026-06-08 (single-install self-as-peer harness; Tests A/B/C PASS, D skipped). See `101-UAT.md`.
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `github.default_router: true` round-trips into `cfg.Github.DefaultRouter (*bool)` via existing `UnmarshalKey`; absent ⇒ nil (tri-state) | VERIFIED | `config.go:195` field declared; `TestLoadGithubDefaultRouter_Set` passes; no merge-list entry (comment at line 193 notes intent) |
| 2 | `km init` exports `KM_GITHUB_DEFAULT_ROUTER` only when non-nil, with env-wins drift WARN | VERIFIED | `init.go:1138-1152`; `TestInitExportsGithubDefaultRouter_{True,Nil,DriftWarn}` all pass |
| 3 | Terragrunt passes `github_default_router = get_env("KM_GITHUB_DEFAULT_ROUTER", "false")` → `lambda-github-bridge/v1.1.0` module → Lambda env block `KM_GITHUB_DEFAULT_ROUTER` | VERIFIED | `terragrunt.hcl:100`, `variables.tf:68-71`, `main.tf:288` |
| 4 | `PeerClaimResult{PeerURL, Claimed}` type exists; `Broadcast` returns `([]PeerClaimResult, error)`; no Channels field | VERIFIED | `interfaces.go:12` type; `relayer.go:84` signature; no Channels anywhere in github bridge relayer/interfaces |
| 5 | Transport error / non-2xx / unparseable body ⇒ `Claimed:true`; explicit `{"claimed":false}` ⇒ `Claimed:false` | VERIFIED | `relayer.go:155-174` postToPeer; `TestHTTPPeerRelayer_RolloutLegacyOk_ClaimedTrue`, `RolloutNon2xx_ClaimedTrue`, `RolloutTimeout_ClaimedTrue`, `ClaimedFalse_Parsed` all pass |
| 6 | Broadcast is synchronous (`wg.Wait`), bounded by `relayBroadcastTimeout`, forwards `X-KM-Relayed:1`; empty PeerURLs ⇒ `(nil, nil)` | VERIFIED | `relayer.go:114` wg.Wait; `relayer.go:145` X-KM-Relayed header; `TestHTTPPeerRelayer_Empty_NilNil` passes |
| 7 | Relayed + unowned ⇒ `200 {"claimed":false}`; relayed + owned ⇒ `200 {"claimed":true}`; non-relayed owned ⇒ plain `"ok"` (byte-identical) | VERIFIED | `webhook_handler.go:255` jsonClaim(false); `webhook_handler.go:547` jsonClaim(true); `TestPeerClaim_{RelayedMiss,RelayedOwned,NonRelayedOwned}` pass |
| 8 | Front-door tally: `h.DefaultRouter=true` ⇒ tallies claims; zero claims ⇒ calls `maybePostGitHubOrphanComment`; `DefaultRouter=false` ⇒ no tally, no post (dormant) | VERIFIED | `webhook_handler.go:267-277`; `TestOrphanComment_HappyPath`, `TestDefaultRouterOff_Silent` pass |
| 9 | `maybePostGitHubOrphanComment` gates: DefaultRouter + ContainsMention re-check + Commenter!=nil && Installation.ID!=0 + cooldown CheckAndStore(`gh-router-cooldown:{owner}/{repo}#{number}`, 3600) | VERIFIED | `orphan_reply.go:33-90`; `TestOrphanComment_{NonMention,CommenterNil,InstallationIDZero}` and `TestOrphanCooldown_{FirstTime_Posts,SecondSuppressed}` pass |
| 10 | Guidance comment body names `github.repos:` and `km init` | VERIFIED | `orphan_reply.go:80` literal strings `\`github.repos:\`` and `km init` |
| 11 | `cmd/km-github-bridge/main.go` sets `DefaultRouter=true` and `OrphanCooldown=nonceStore` only when `KM_GITHUB_DEFAULT_ROUTER=="true"` | VERIFIED | `main.go:275-278`; no RunningChannels analog |
| 12 | `docs/github-bridge.md` Phase 101 section covers toggle, mechanism, rollout-safe tally, cooldown, deploy (`km init --dry-run=false` NOT `--sidecars`) | VERIFIED | Lines 675-790; `grep "dry-run=false"` confirms correct deploy instruction |
| 13 | `OPERATOR-GUIDE.md` Phase 101 orphan-repo router entry adjacent to Phase 100 | VERIFIED | Lines 798-828 |
| 14 | `CLAUDE.md` Phase 101 block + `101-UAT.md` two-install / one-App / unowned-repo runbook with Tests A-D and `GH-ORPHAN-E2E` reference | VERIFIED | CLAUDE.md:21-28; 101-UAT.md has Tests A-D, pass/fail table, `default_router` and cooldown assertions |

**Score:** 14/14 truths verified

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/config/config.go` | `GithubConfig.DefaultRouter *bool` field | VERIFIED | Line 195; mapstructure + yaml tags correct |
| `internal/app/config/config_test.go` | `TestLoadGithubDefaultRouter_Set` round-trip + nil-when-absent | VERIFIED | Lines 1184-1195+; tests pass |
| `internal/app/cmd/init.go` | `KM_GITHUB_DEFAULT_ROUTER` export + drift WARN | VERIFIED | Lines 1138-1152 |
| `internal/app/cmd/init_test.go` | `TestInitExportsGithubDefaultRouter_{True,Nil,DriftWarn}` | VERIFIED | Lines 1074-1130+; tests pass |
| `infra/modules/lambda-github-bridge/v1.1.0/variables.tf` | `github_default_router` TF variable | VERIFIED | Lines 68-71 |
| `infra/modules/lambda-github-bridge/v1.1.0/main.tf` | `KM_GITHUB_DEFAULT_ROUTER` in Lambda env block | VERIFIED | Line 288 |
| `infra/live/use1/lambda-github-bridge/terragrunt.hcl` | `get_env("KM_GITHUB_DEFAULT_ROUTER", "false")` | VERIFIED | Line 100 |
| `pkg/github/bridge/interfaces.go` | `PeerClaimResult` type + updated `Broadcast` signature | VERIFIED | Lines 5-31 |
| `pkg/github/bridge/relayer.go` | `peerRelayResponse` + claim-aware `Broadcast` + `postToPeer` | VERIFIED | Lines 21-174 |
| `pkg/github/bridge/relayer_test.go` | Claim-tally + rollout-safety tests | VERIFIED | Lines 277-440; all pass |
| `pkg/github/bridge/webhook_handler.go` | `DefaultRouter bool` + `OrphanCooldown DeliveryNonceStore` fields; `jsonClaim` helper; front-door tally | VERIFIED | Lines 24-31, 155-162, 255, 267-277, 547 |
| `pkg/github/bridge/orphan_reply.go` | `maybePostGitHubOrphanComment` with four gates + bounded PostComment | VERIFIED | Lines 19-95; cooldown key at line 61 |
| `pkg/github/bridge/webhook_handler_phase101_test.go` | All Phase 101 handler tests | VERIFIED | Full suite passes |
| `cmd/km-github-bridge/main.go` | `KM_GITHUB_DEFAULT_ROUTER` gate wiring | VERIFIED | Lines 275-278; builds clean |
| `docs/github-bridge.md` | Phase 101 section | VERIFIED | Lines 675-790; TOC entry at line 29 |
| `OPERATOR-GUIDE.md` | Phase 101 entry | VERIFIED | Lines 798-828 |
| `CLAUDE.md` | Phase 101 project-history block | VERIFIED | Lines 21-28 |
| `.planning/phases/101-*/101-UAT.md` | GH-ORPHAN-E2E two-install UAT runbook | VERIFIED | Tests A-D with explicit pass/fail assertions |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `km-config.yaml github.default_router` | `cfg.Github.DefaultRouter (*bool)` | `v.UnmarshalKey("github", &cfg.Github)` — no new merge entry | WIRED | `config.go:195`; round-trip proven by test |
| `cfg.Github.DefaultRouter` | `KM_GITHUB_DEFAULT_ROUTER` env var | `init.go` strconv.FormatBool, non-nil only, drift WARN | WIRED | `init.go:1138-1152` |
| `KM_GITHUB_DEFAULT_ROUTER` env | Lambda `environment.variables` | terragrunt `get_env` → TF var `github_default_router` → `main.tf` env block | WIRED | All three files confirmed |
| Each peer response body | `PeerClaimResult.Claimed` | `json.Unmarshal into peerRelayResponse`; transport-err/non-2xx/unparseable ⇒ `Claimed:true` | WIRED | `relayer.go:155-174` |
| `HTTPPeerRelayer.Broadcast` | `[]PeerClaimResult` | Collect `resultCh` after `wg.Wait` (synchronous, Lambda-freeze safe) | WIRED | `relayer.go:84-125` |
| Front-door `Broadcast []PeerClaimResult` | `maybePostGitHubOrphanComment` | `if h.DefaultRouter { anyClaimed tally; if !anyClaimed { post } }` | WIRED | `webhook_handler.go:267-277` |
| `maybePostGitHubOrphanComment` | `Commenter.PostComment` (GitHub PR/issue comment) | Under 5s ctx; all four gates | WIRED | `orphan_reply.go:87-93` |
| `maybePostGitHubOrphanComment` | `OrphanCooldown.CheckAndStore` (nonces table) | key `gh-router-cooldown:{owner}/{repo}#{number}`, ttl 3600 | WIRED | `orphan_reply.go:61-68` |
| `os.Getenv("KM_GITHUB_DEFAULT_ROUTER")=="true"` | `webhookHandler.DefaultRouter=true` + `OrphanCooldown=nonceStore` | `main.go` guard | WIRED | `main.go:275-278` |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| GH-ORPHAN-ROLLOUT | Plans 01, 02, 03 | Config surface + tri-state dormancy + rollout-safe tally (legacy/non-2xx/timeout ⇒ Claimed:true) | SATISFIED | `config.go:195`, `init.go:1138-1152`, TF plumbing, `relayer.go` postToPeer rollout safety, `TestDefaultRouterOff_Silent` |
| GH-ORPHAN-CLAIM | Plans 02, 03 | Claim-aware scatter-gather: peers return `{claimed:bool}`; front-door tallies | SATISFIED | `interfaces.go` PeerClaimResult, `relayer.go` Broadcast, `webhook_handler.go:255,547`, all tally tests pass |
| GH-ORPHAN-REPLY | Plan 03 | Front-door posts ONE guidance comment naming `github.repos:` + `km init` on zero claims + mention + all gates | SATISFIED | `orphan_reply.go` all four gates; comment text at line 77-82; `TestOrphanComment_HappyPath` pass |
| GH-ORPHAN-COOLDOWN | Plan 03 | Per-(repo,number) cooldown 3600s via nonces table; key `gh-router-cooldown:{owner}/{repo}#{number}` | SATISFIED | `orphan_reply.go:61`; `TestOrphanCooldown_{FirstTime,SecondSuppressed}` pass |
| GH-ORPHAN-E2E | Plan 04 | Two-install / one-App / unowned-repo UAT with explicit pass/fail assertions; docs across github-bridge.md, OPERATOR-GUIDE.md, CLAUDE.md | NEEDS HUMAN | UAT runbook (`101-UAT.md`) complete and runnable; actual live execution requires two deployed installs + real GitHub webhook delivery |

Note: GH-ORPHAN-* IDs are phase-local synthetic IDs declared in the ROADMAP and PLAN frontmatter. REQUIREMENTS.md has no Phase 100/101 section; these IDs follow the same phase-local pattern as Phase 98's GH-X-* IDs. No orphaned requirements found.

### Anti-Patterns Found

None. The following false positives were confirmed as comments-only:
- `"github.default_router"` in `config.go` appears only in a comment at line 193 documenting the absence of a merge entry. No actual merge-list entry exists.
- `Channels`, `SandboxChannelInfo` in `relayer.go` and `interfaces.go` appear only in comments explicitly noting these are NOT included.

### Human Verification Required

#### 1. GH-ORPHAN-E2E: Two-install unowned-repo @-mention ⇒ one guidance comment

**Test:** Configure two km installs (e.g. `kph` as front door with `github.default_router: true` and `github.peer_bridges: [<sec-bridge-url>]`; `sec` as peer). Deploy both with `make build-lambdas` (clean) + `km init --dry-run=false`. Install the GitHub App on a test repo owned by neither install. Post a PR/issue comment @-mentioning the bot on that unowned repo.
**Expected:** Exactly ONE guidance comment from the front-door install naming `github.repos:` and `km init`. No 👀 reaction on the unowned repo. No double-post.
**Why human:** Requires two live km Lambda deployments, a real GitHub App webhook, real GitHub API call to post the comment, and live DynamoDB nonces table. Unit tests mock all these boundaries.

#### 2. GH-ORPHAN-E2E: Cooldown — second @-mention within window suppressed

**Test:** Within 3600s of Test A above, post a second PR/issue comment @-mentioning the bot on the same PR/issue.
**Expected:** No second guidance comment. The nonce key `gh-router-cooldown:{owner}/{repo}#{number}` is still active.
**Why human:** TTL enforcement in DynamoDB and live Lambda invocation required.

#### 3. GH-ORPHAN-E2E: Owned-repo @-mention ⇒ no guidance comment

**Test:** @-mention the bot on a PR/issue in a repo listed under `sec`'s `github.repos:`.
**Expected:** Normal dispatch (👀 + sandbox turn). No guidance comment from the front door (`anyClaimed=true` from `sec`'s `{"claimed":true}` response).
**Why human:** Requires live relay + live peer claim response + live sandbox dispatch.

---

### Gaps Summary

None. All 14 must-have truths are verified and all automated tests pass. The only open item is GH-ORPHAN-E2E, which by design requires live infrastructure and is documented in `101-UAT.md` as a manual runbook.

---

_Verified: 2026-06-08_
_Verifier: Claude (gsd-verifier)_
