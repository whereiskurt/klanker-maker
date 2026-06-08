---
phase: 100-github-bridge-federated-relay-one-github-app-serving-many-resource-prefix-installs-via-github-peer-bridges
verified: 2026-06-08T00:00:00Z
status: passed
score: 10/10 success criteria verified (8/8 requirements, 7/7 critical goal-backward checks)
re_verification:
  previous_status: none
  previous_score: n/a
---

# Phase 100: GitHub Bridge Federated Relay Verification Report

**Phase Goal:** Let ONE GitHub App serve many `resource_prefix` installs in a single AWS account (direct analog of Slack's Phase 95 relay). Opt-in `github.peer_bridges: []string` → `KM_GITHUB_PEER_BRIDGES`; front door broadcasts unowned webhooks (verbatim body + 3 GitHub headers + `X-KM-Relayed:1`) to siblings; unconditional `Resolve()` reorder doubles as a 700-repo scale fix; single-hop loop guard; dormancy = byte-identical to Phase 97/98.

**Verified:** 2026-06-08
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| #   | Truth | Status | Evidence |
| --- | ----- | ------ | -------- |
| 1   | Federation off ⇒ byte-identical to Phase 97/98 (no relay, resolve-miss → 200) | ✓ VERIFIED | `cfg.Github.PeerBridges` nil ⇒ no env export ⇒ TF default `""` ⇒ `relayer` nil ⇒ `webhookHandler.Relayer` true-nil (main.go:268). Full `pkg/github/bridge` suite green incl Phase 98 thread-bypass. |
| 2   | `github.peer_bridges` round-trips + `km init` exports `KM_GITHUB_PEER_BRIDGES` w/ drift WARN | ✓ VERIFIED | config.go:182 `PeerBridges` field; decoded by existing `UnmarshalKey("github",…)` (config.go:690), NO new merge entry. init.go:1129-1134 gated export + env-wins WARN. `TestLoadGithubPeerBridges_Set` + 4 cmd export tests pass. |
| 3   | Front-door miss broadcasts verbatim body + headers + `X-KM-Relayed:1`, returns 200; peer re-verifies + dispatches | ✓ VERIFIED | relayer.go:97-103 forwards 3 GitHub headers + `X-KM-Relayed:1`; webhook_handler.go:235-249 `!matched`+!relayed → Broadcast → 200. `TestHTTPPeerRelayer_RelayedVerify` proves HMAC re-verifies over verbatim body. |
| 4   | `Resolve()` runs ahead of mention/thread filter; no-mention known-thread peer-owned follow-up relays | ✓ VERIFIED | Resolve() at webhook_handler.go:222 (after PR filter), before LookupSandbox (266) and mention filter. `TestReorder_PeerOwnedThreadFollowup_Relays` passes (broadcastCalls=1, lookupCalls=0). |
| 5   | Loop guard: `X-KM-Relayed:1` terminal — process if owned, drop `github_relay_no_owner` else, never re-relay | ✓ VERIFIED | webhook_handler.go:228-234 relayed+miss → WARN `github_relay_no_owner` → 200, no Broadcast. `TestDecisionTable_RelayLoopGuard` (4 rows) + `TestLoopGuard_RelayedMiss_NeverRebroadcasts` pass. |
| 6   | Exactly one 👀 + one dispatch; front door on miss neither reacts nor enqueues; per-install dedupe | ✓ VERIFIED | `!matched` early-exit returns 200 before any reactor/dispatch/dedupe; reaction + dispatch only on matched path. Decision-table `absent+unmatched` row asserts zero local dispatch. |
| 7   | Synchronous bounded broadcast returns 200 within ack window even when peer slow/unreachable | ✓ VERIFIED | relayer.go:16 `relayBroadcastTimeout=5s`, `wg.Wait()` (118). `TestHTTPPeerRelayer_Broadcast_BoundedContext` (5.00s) + `_FailingPeerNonFatal` pass (non-fatal, returns nil). |
| 8   | `km doctor` reports peer-bridge health (malformed URL / self-loop / empty) | ✓ VERIFIED | doctor.go:1121 `checkGitHubPeerBridges`; wired at 4017-4032 gated on `githubConfigured`. `TestGithubPeerBridges` (7 subtests) pass. |
| 9   | Phase 97/98 criteria continue to hold (no regression) | ✓ VERIFIED | Full `pkg/github/bridge` suite green (7.5s); Phase 98 thread-bypass tests pass; matched-path order unchanged. |
| 10  | Scale fix (federation OFF): unowned-repo PR comment → 200 with ZERO `LookupSandbox` DDB read; owned still reads | ✓ VERIFIED | `TestNoWastedRead_UnownedRepo_ZeroLookup` (lookupCalls==0) + `TestNoWastedRead_OwnedRepo_PerformsLookup` (>=1) pass via call-count mock. |

**Score:** 10/10 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
| -------- | -------- | ------ | ------- |
| `internal/app/config/config.go` | `GithubConfig.PeerBridges` field, no new merge entry | ✓ VERIFIED | Field at :182; only mention of `github.peer_bridges` is a comment (:177). UnmarshalKey at :690. |
| `internal/app/cmd/init.go` | `KM_GITHUB_PEER_BRIDGES` export + drift WARN | ✓ VERIFIED | :1129-1134, gated on `len>0`, env-wins WARN. |
| `infra/modules/lambda-github-bridge/v1.1.0/variables.tf` | `github_peer_bridges` var default `""` | ✓ VERIFIED | :62, in-place v1.1.0 edit. |
| `infra/modules/lambda-github-bridge/v1.1.0/main.tf` | `KM_GITHUB_PEER_BRIDGES` env entry | ✓ VERIFIED | :287 `= var.github_peer_bridges`. |
| `infra/live/use1/lambda-github-bridge/terragrunt.hcl` | `get_env(...)` input + source `v1.1.0` unchanged | ✓ VERIFIED | :95 input; :32 source still `/v1.1.0` (no version bump). |
| `pkg/github/bridge/interfaces.go` | `PeerRelayer` interface, plain-error Broadcast | ✓ VERIFIED | :15 `Broadcast(ctx,rawBody,ghHeaders) error`; NO PeerClaimResult. |
| `pkg/github/bridge/relayer.go` | `HTTPPeerRelayer` + 5s bound + verbatim headers | ✓ VERIFIED | :65 Broadcast, :16 timeout, :97-103 headers + loop guard, :118 wg.Wait. |
| `pkg/github/bridge/webhook_handler.go` | `Relayer` field + reordered Handle() + !matched branch | ✓ VERIFIED | :141 field; :222 Resolve reorder; :228-249 decision branch. |
| `cmd/km-github-bridge/main.go` | env parse + typed-nil-safe Relayer injection | ✓ VERIFIED | :222-240 parse; :268-269 conditional assign (struct literal omits Relayer). |
| `internal/app/cmd/doctor.go` | `checkGitHubPeerBridges` + wiring | ✓ VERIFIED | :1121 impl; :4017-4032 wiring gated on githubConfigured. |
| `docs/github-bridge.md` | federated-relay runbook | ✓ VERIFIED | 16 `peer_bridges` refs. |
| `100-UAT.md` | two-install/one-App E2E manual runbook | ✓ VERIFIED | Exists, GH-FED-E2E deliverable. |
| Test files (relayer_test, phase100_test, config_test, doctor_github_test, init_test) | RED-first coverage | ✓ VERIFIED | All present + green. |

### Key Link Verification

| From | To | Via | Status |
| ---- | -- | --- | ------ |
| `km-config.yaml github.peer_bridges` | `cfg.Github.PeerBridges` | existing `UnmarshalKey("github",…)`, no new merge entry | ✓ WIRED |
| `cfg.Github.PeerBridges` | Lambda env `KM_GITHUB_PEER_BRIDGES` | init.go Join → terragrunt get_env → TF var → main.tf env | ✓ WIRED |
| `HTTPPeerRelayer.Broadcast` | peer Function URLs | parallel POST w/ `X-KM-Relayed:1` + 3 GitHub headers, bounded ctx, wg.Wait | ✓ WIRED |
| forwarded sig + verbatim body | `VerifyGitHubSignature` on peer | verbatim forward ⇒ same HMAC (RelayedVerify test) | ✓ WIRED |
| `Handle() !matched` branch | `h.Relayer.Broadcast` | only when Relayer != nil AND not already relayed | ✓ WIRED |
| `cmd/main.go` env parse | `WebhookHandler.Relayer` | conditional assign avoids typed-nil-into-interface panic | ✓ WIRED |
| `km doctor` GitHub group | `checkGitHubPeerBridges` | gated on githubConfigured; ownBridgeURL from SSM | ✓ WIRED |

### Requirements Coverage

| Requirement | Source Plan | Status | Evidence |
| ----------- | ----------- | ------ | -------- |
| GH-FED-CONFIG | 100-01 | ✓ SATISFIED | PeerBridges field + UnmarshalKey round-trip, NO merge-list entry, init.go export + drift WARN, TF/terragrunt wiring. |
| GH-FED-RELAY | 100-02 | ✓ SATISFIED | HTTPPeerRelayer fire-and-forget, verbatim body + headers + loop guard. |
| GH-FED-VERIFY | 100-02 | ✓ SATISFIED | RelayedVerify HMAC re-verify test green. |
| GH-FED-REORDER | 100-03 | ✓ SATISFIED | Resolve() at :222 before lookup/mention; byte-identical matched path; full suite green. |
| GH-FED-LOOPGUARD | 100-03 | ✓ SATISFIED | 4-row decision table + RelayedMiss-never-rebroadcasts tests green. |
| GH-FED-SCALE | 100-03 | ✓ SATISFIED | NoWastedRead zero-lookup (unowned) / >=1 (owned) call-count mock tests green. |
| GH-FED-DOCTOR | 100-04 | ✓ SATISFIED | checkGitHubPeerBridges + 7 subtests; wired gated on githubConfigured. |
| GH-FED-E2E | 100-04 | ✓ SATISFIED | 100-UAT.md two-install/one-App manual runbook (documented deliverable). |

Note: REQUIREMENTS.md has no Phase 100 section by project convention; these synthetic IDs are declared in PLAN frontmatter and cross-referenced in ROADMAP. No ORPHANED requirements.

### Critical Goal-Backward Checks

| # | Check | Result |
| - | ----- | ------ |
| 1 | CONFIG: round-trip w/o new merge entry; KM_GITHUB_PEER_BRIDGES exported + TF/Lambda wired, in-place v1.1.0 | ✓ PASS — grep confirms 0 `github.peer_bridges` merge entries; source still `/v1.1.0`. |
| 2 | RELAY/VERIFY: Broadcast plain-error fire-and-forget, NO PeerClaimResult leaked | ✓ PASS — `grep PeerClaimResult relayer.go interfaces.go` → NONE. |
| 3 | REORDER+SCALE: Resolve() before thread-lookup + mention; byte-identical; zero-DDB-read proof | ✓ PASS — :222 < :266; full suite + NoWastedRead call-count tests green. |
| 4 | LOOPGUARD: relayed+miss terminal drop (github_relay_no_owner), 4-row table tested | ✓ PASS — decision table + never-rebroadcast tests green. |
| 5 | DOCTOR: checkGitHubPeerBridges (malformed/self-loop WARN, empty SKIP), wired, tested | ✓ PASS — impl + 7 subtests green. |
| 6 | E2E: 100-UAT.md exists | ✓ PASS — present. |
| 7 | Dormancy: typed-nil Relayer guard in main.go does NOT panic | ✓ PASS — conditional assign at :268, struct literal omits Relayer. |

### Test Results

- `go test ./pkg/github/bridge/... ./internal/app/config/...` → **ok** (no failures)
- `go test ./internal/app/cmd/ -run 'GithubPeerBridges'` → **PASS** (TestGithubPeerBridges 7 subtests + 4 export tests)
- Targeted reorder/loopguard/scale/relayer verbose run → all PASS

### Anti-Patterns Found

| File | Pattern | Severity | Impact |
| ---- | ------- | -------- | ------ |
| (none in phase source files) | — | — | Clean scan across relayer.go, webhook_handler.go, main.go, doctor.go, config.go, init.go. |

### Known Pre-Existing Failure (NOT counted against phase)

`TestUnlockCmd_RequiresStateBucket` in `internal/app/cmd` FAILS because it reaches **live AWS** (DynamoDB lock lookup returns "is not locked" instead of short-circuiting on empty StateBucket). Git history confirms `unlock_test.go` was last touched by the module-rename commit `cab47e61`, never by any Phase 100 commit — pre-existing and unrelated to federation. Logged in `deferred-items.md`.

### Human Verification Required

GH-FED-E2E (`100-UAT.md`) is a manual two-install/one-App live runbook by design — it requires two live `resource_prefix` installs sharing one real GitHub App webhook and real GitHub delivery, which cannot be automated. It ships as a documented deliverable (acceptable per the phase contract). All automatable behavior (relay, reorder, loop guard, scale fix, doctor, dormancy) is covered by unit tests, so this does NOT block goal achievement — it is a recommended operator confidence check, not an open gap.

### Gaps Summary

No gaps. All 10 ROADMAP success criteria verified, all 8 requirements satisfied, all 7 critical goal-backward checks pass, all key links wired, no anti-patterns. The single failing test in the package is pre-existing, AWS-dependent, and untouched by this phase. The phase goal — one GitHub App serving many installs via opt-in `github.peer_bridges`, with an unconditional scale-fix reorder and dormancy byte-identity — is achieved.

---

_Verified: 2026-06-08_
_Verifier: Claude (gsd-verifier)_
