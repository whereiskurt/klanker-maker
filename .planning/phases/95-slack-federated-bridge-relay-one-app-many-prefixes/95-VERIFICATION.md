---
phase: 95-slack-federated-bridge-relay-one-app-many-prefixes
verified: 2026-06-05T07:00:00Z
status: human_needed
score: 13/14 must-haves verified (1 requires live Slack/AWS infra)
re_verification: false
human_verification:
  - test: "Two-install end-to-end relay (SLACK-FED-E2E / SLACK-FED-UAT)"
    expected: "A message posted in install B's per-sandbox channel (#sb-{id}) is received by install A's bridge (front door), relayed to install B's /events URL, and install B's bridge enqueues it to SQS and posts the 👀 ack. CloudWatch logs for both Lambdas confirm the broadcast path and FetchByChannel hit. No Slack retries (3s ack honored). Negative check: relayed miss logs slack_relay_no_owner, no loop."
    why_human: "Requires a real Slack App (xoxb + signing secret), two live km installs in one AWS account/region, and two deployed bridge Lambdas with KM_SLACK_PEER_BRIDGES set via km init --dry-run=false. Cannot be simulated with unit tests."
    setup: |
      1. km slack init on both installs A and B with the SAME xoxb + signing secret.
      2. Set Slack App Events Request URL to install A's {bridge-url}/events.
      3. Set slack.peer_bridges in A's km-config.yaml to [B's /events URL]; set B's to [A's /events URL].
      4. make build-lambdas (clean) + km init --dry-run=false on each install.
      5. Confirm: aws lambda get-function-configuration --function-name {prefix}-slack-bridge shows KM_SLACK_PEER_BRIDGES.
      6. km doctor on A shows "slack peer bridges" = OK.
      7. Create a sandbox under B; post a message in its #sb-{id}; verify relay path in CloudWatch.
---

# Phase 95: Slack Federated Bridge Relay Verification Report

**Phase Goal:** Let one Slack App's single Events Request URL serve many km installs (resource_prefix) and operators in a single AWS account/region. The operator points the App at any one install's bridge ("front door"); a bridge that receives a message for a channel it doesn't own relays the verbatim event to sibling bridges via a static per-install slack.peer_bridges list (single-hop broadcast-on-miss with X-KM-Relayed loop guard), and the owning install processes it normally. Opt-in, mirrors slack.mention_only plumbing end-to-end, no shared infra, no SandboxProfile schema change, no sandbox recreate.

**Verified:** 2026-06-05T07:00:00Z
**Status:** human_needed (13/14 automated checks passed; SLACK-FED-E2E/SLACK-FED-UAT require live infrastructure)
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | slack.peer_bridges in km-config.yaml round-trips through config load into cfg.Slack.PeerBridges | VERIFIED | SlackConfig.PeerBridges []string at config.go:54; v2→v merge-list entry at config.go:417; GetStringSlice population at config.go:514-515; TestLoadSlackPeerBridges_Set (len==2) and TestLoadSlackPeerBridges_Absent (nil) both green |
| 2 | Absent slack.peer_bridges yields a nil slice (federation off sentinel) | VERIFIED | config.go:514 gated on v.IsSet; TestLoadSlackPeerBridges_Absent passes |
| 3 | km init exports KM_SLACK_PEER_BRIDGES (comma-joined) only when PeerBridges is non-empty, with an env-wins drift WARN | VERIFIED | init.go:906-911; gate is len(PeerBridges)>0; drift WARN writes to stderr; env-wins; TestExportTerragruntEnvVars_PeerBridges_DriftWarn passes |
| 4 | The TF module exposes slack_peer_bridges var and writes KM_SLACK_PEER_BRIDGES into the bridge Lambda env | VERIFIED | variables.tf:133 declares variable "slack_peer_bridges"; main.tf:331 KM_SLACK_PEER_BRIDGES = var.slack_peer_bridges; terragrunt.hcl:116 slack_peer_bridges = get_env("KM_SLACK_PEER_BRIDGES", "") |
| 5 | A non-relayed message for a channel this install does NOT own is broadcast to every configured peer with original Slack signature headers + X-KM-Relayed:1; front door returns 200 | VERIFIED | events_handler.go:222-229 (Relayer.Broadcast called on nil-relayed miss); TestEventsHandler_FederatedRelay/absent+miss passes with wantBroadcast=1 |
| 6 | A relayed (X-KM-Relayed:1) message this install does not own is dropped (slack_relay_no_owner) and the relayer is NEVER invoked — loops impossible | VERIFIED | events_handler.go:215-220 (relayed+miss returns 200 before Relayer check); TestEventsHandler_FederatedRelay/present+miss passes with wantBroadcast=0 and explicit loop-impossibility assertion at test line 1518 |
| 7 | A message this install owns is processed exactly as today, whether direct or relayed | VERIFIED | Decision table falls through to existing processing path for owned channel regardless of X-KM-Relayed; TestEventsHandler_FederatedRelay/absent+owns and present+owns both pass with wantSQS=1, wantBroadcast=0 |
| 8 | When EventsHandler.Relayer is nil, a local miss returns 200 and never broadcasts — byte-identical to today | VERIFIED | events_handler.go:222 nil guard; TestEventsHandler_NilRelayer_MissReturns200 explicitly asserts resp.StatusCode==200 and no SQS sends |
| 9 | A relayed request passes verifySlackSignature with the shared signing secret (forwarded body+timestamp unchanged) | VERIFIED | TestVerifySlackSignature_Relayed: valid HMAC computed over (ts, body), forwarded verbatim, verifySlackSignature passes; tampered body and stale timestamp both fail |
| 10 | Broadcast is SYNCHRONOUS (sync.WaitGroup + bounded context.WithTimeout) before returning 200 | VERIFIED | relayer.go:85 wg.Wait() before return; relayer.go:61 context.WithTimeout(ctx, 2500ms); TestPeerRelayer_BoundedTimeout confirms bounded behavior |
| 11 | km doctor WARNs on a malformed peer_bridges URL and a self-loop URL | VERIFIED | doctor_slack.go:586-625 checkSlackPeerBridges; TestCheckSlackPeerBridges/malformed_URL_→_WARN and self-loop_→_WARN both pass |
| 12 | km doctor SKIPs the check when peer_bridges is unset (federation not configured) | VERIFIED | doctor_slack.go:588-593 len==0 returns CheckSkipped; TestCheckSlackPeerBridges/nil_peerBridges_→_SKIPPED passes |
| 13 | Phase 95 operator documentation is present in docs/slack-notifications.md and CLAUDE.md | VERIFIED | docs/slack-notifications.md line 1970 "Phase 95: Federated bridge relay"; CLAUDE.md line 38 "Where to look" row + lines 430-450 Phase 95 block |
| 14 | Two-install end-to-end relay (SLACK-FED-E2E / SLACK-FED-UAT) | HUMAN NEEDED | Manual UAT: requires real Slack App + two live km installs. See human_verification section. |

**Score:** 13/14 truths verified (14th is live-infra UAT)

---

## Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/config/config.go` | SlackConfig.PeerBridges field + merge-list entry + GetStringSlice population | VERIFIED | All three present and confirmed at lines 54, 417, 514-515 |
| `internal/app/config/config_test.go` | TestLoadSlackPeerBridges_Set and TestLoadSlackPeerBridges_Absent | VERIFIED | Lines 880 and 915; both green |
| `internal/app/cmd/init.go` | KM_SLACK_PEER_BRIDGES export with comma-join + drift WARN | VERIFIED | Lines 899-911 |
| `internal/app/cmd/slack_peer_bridges_init_test.go` | Drift-warn + round-trip tests | VERIFIED | 4 tests: _Set, _Absent, _DriftWarn, _NoOverwriteWhenEnvMatches — all green |
| `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` | slack_peer_bridges = get_env("KM_SLACK_PEER_BRIDGES", "") | VERIFIED | Line 116 |
| `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` | variable "slack_peer_bridges" string | VERIFIED | Line 133 |
| `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` | KM_SLACK_PEER_BRIDGES = var.slack_peer_bridges in Lambda env | VERIFIED | Line 331 |
| `pkg/slack/bridge/relayer.go` | HTTPPeerRelayer.Broadcast — parallel bounded synchronous POST | VERIFIED | 141 lines; wg.Wait() at line 85; context.WithTimeout at line 61 |
| `pkg/slack/bridge/events_interfaces.go` | PeerRelayer interface declaration | VERIFIED | Lines 8-17; Broadcast(ctx, rawBody, slackHeaders) error |
| `pkg/slack/bridge/relayer_test.go` | TestPeerRelayer_* — header-preservation, parallel, bounded-timeout, failing-peer | VERIFIED | 4 tests at lines 40, 79, 106, 141; all green |
| `pkg/slack/bridge/events_handler.go` | Relayer field + four-row decision table at FetchByChannel miss | VERIFIED | Relayer field at line 79; decision table at lines 202-233 reading req.Headers["x-km-relayed"] |
| `pkg/slack/bridge/events_handler_test.go` | TestEventsHandler_FederatedRelay + TestEventsHandler_NilRelayer_MissReturns200 | VERIFIED | Lines 1424, 1535; four-row table + nil-invariant + loop-impossibility assertion; all green |
| `cmd/km-slack-bridge/main.go` | eventsHandler.Relayer wired from KM_SLACK_PEER_BRIDGES | VERIFIED | Lines 264-282; parses KM_SLACK_PEER_BRIDGES, TrimSpace, filters empties, gates on len(peers)>0 |
| `internal/app/cmd/doctor_slack.go` | checkSlackPeerBridges function | VERIFIED | Lines 575-628; SKIPPED/WARN-malformed/WARN-selfloop/OK |
| `internal/app/cmd/doctor_slack_peer_bridges_test.go` | TestCheckSlackPeerBridges — 6 table-driven cases | VERIFIED | All 6 cases green including nil, empty, malformed, self-loop, valid, empty-ownBridgeURL |
| `internal/app/cmd/doctor.go` | checkSlackPeerBridges wired into Slack checks block | VERIFIED | Lines 3186-3200; peerBridges from cfg.GetSlackPeerBridges(); ownBridgeURL fetched lazily from SSM; CheckError demoted to CheckWarn |
| `docs/slack-notifications.md` | Phase 95 federated relay operator section | VERIFIED | Lines 1970+ with architecture, YAML example, operator flow, doctor table, troubleshooting |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| config.go merge-list | cfg.Slack.PeerBridges population | "slack.peer_bridges" string at config.go:417 | WIRED | Present in merge-list; GetStringSlice at line 514 |
| init.go ExportTerragruntEnvVars | infra/live/use1/lambda-slack-bridge/terragrunt.hcl get_env | KM_SLACK_PEER_BRIDGES env var | WIRED | init.go:911 os.Setenv; terragrunt.hcl:116 get_env("KM_SLACK_PEER_BRIDGES","") |
| events_handler.go FetchByChannel miss | h.Relayer.Broadcast | absent X-KM-Relayed + Relayer != nil branch | WIRED | events_handler.go:222 if h.Relayer != nil { h.Relayer.Broadcast(...) } |
| cmd/km-slack-bridge/main.go | eventsHandler.Relayer | parse KM_SLACK_PEER_BRIDGES into HTTPPeerRelayer | WIRED | main.go:276 eventsHandler.Relayer = &bridge.HTTPPeerRelayer{...} |
| doctor.go Slack checks block | checkSlackPeerBridges | anonymous-closure append | WIRED | doctor.go:3190 checks = append(checks, func...) calling checkSlackPeerBridges |

---

## Requirements Coverage

| Requirement | Source Plan | Description (abbreviated) | Status | Evidence |
|-------------|------------|---------------------------|--------|----------|
| SLACK-FED-CFG | 95-01 | SlackConfig.PeerBridges field + merge-list + population | SATISFIED | config.go:54,417,514; TestLoadSlackPeerBridges_Set/Absent green |
| SLACK-FED-PLUMB | 95-01 | init.go KM_SLACK_PEER_BRIDGES export + TF chain | SATISFIED | init.go:906-911; variables.tf:133; main.tf:331; terragrunt.hcl:116 |
| SLACK-FED-RELAY | 95-02 | HTTPPeerRelayer.Broadcast — parallel bounded synchronous | SATISFIED | relayer.go:53-96; 4 TestPeerRelayer_* tests green |
| SLACK-FED-LOOP | 95-02 | Four-row decision table; relayed+miss terminal | SATISFIED | events_handler.go:202-233; TestEventsHandler_FederatedRelay/present+miss wantBroadcast=0 with loop-guard assertion |
| SLACK-FED-VERIFY | 95-02 | Relayed request passes verifySlackSignature | SATISFIED | TestVerifySlackSignature_Relayed green |
| SLACK-FED-DOCTOR | 95-03 | km doctor WARN on malformed/self-loop; SKIP when unset | SATISFIED | doctor_slack.go:586-628; 6 TestCheckSlackPeerBridges cases green; wired in doctor.go:3190 |
| SLACK-FED-E2E | 95-03 | Two-install live relay (manual) | HUMAN NEEDED | Procedure recorded in 95-03-SUMMARY.md; awaiting operator UAT |
| SLACK-FED-UAT | (orphaned) | Duplicate of SLACK-FED-E2E — same manual UAT | ORPHANED | In REQUIREMENTS.md but NOT in ROADMAP phase requirements list and NOT claimed in any plan. SLACK-FED-E2E (Plan 03) covers the same scope. This is a documentation duplicate, not a gap — the implementation is the same live UAT. |

**Orphaned requirement note:** SLACK-FED-UAT appears in REQUIREMENTS.md § Phase 95 but is absent from the ROADMAP phase requirements list (which lists only the 7 SLACK-FED-CFG through SLACK-FED-E2E IDs) and is not claimed in any plan's `requirements:` frontmatter. It describes the same manual two-install live relay that SLACK-FED-E2E covers. This is a documentation duplicate — no additional implementation gap.

---

## Critical Invariants Verified

**Invariant 1: nil Relayer == byte-identical to today**
Confirmed. events_handler.go:222 guards Broadcast behind `if h.Relayer != nil`. TestEventsHandler_NilRelayer_MissReturns200 explicitly asserts 200 + no SQS sends when Relayer is nil.

**Invariant 2: Broadcast is SYNCHRONOUS**
Confirmed. relayer.go:85 calls wg.Wait() before the function returns. No fire-and-forget pattern exists. TestPeerRelayer_BoundedTimeout exercises the bounded-context path and confirms the call returns (within 3.5s leeway) rather than blocking indefinitely.

**Invariant 3: Loop guard — relayed+miss never re-relays**
Confirmed. events_handler.go:215-220 checks req.Headers["x-km-relayed"] FIRST within the miss branch. If set, it returns 200 immediately before reaching the Relayer.Broadcast call at line 222. TestEventsHandler_FederatedRelay/present+miss has wantBroadcast=0 and an explicit assertion at line 1518 (`if tc.relayed && !tc.owns && len(calls) != 0 { t.Errorf("LOOP GUARD VIOLATED...") }`).

**Invariant 4: Merge-list footgun closed**
Confirmed. "slack.peer_bridges" appears at config.go:417 inside the v2→v merge key list with a comment calling out the footgun. TestLoadSlackPeerBridges_Set asserts len==2 (not just non-nil), making a missing merge-list entry visibly fail.

**Invariant 5: SLACK-FED-E2E scoped as human-verify, not falsely automated**
Confirmed. Plan 03 Task 3 is typed `checkpoint:human-verify` with `gate="blocking"`. The Summary records "AWAITING OPERATOR VERIFICATION" with full procedure. The implementation does not claim automated passing for this requirement.

---

## Anti-Patterns Found

None. Key files (relayer.go, events_handler.go, config.go, init.go, doctor_slack.go) contain no TODO/FIXME/PLACEHOLDER comments, no stub return patterns, no fire-and-forget goroutines. `go build ./...` is clean.

---

## Human Verification Required

### 1. Two-Install End-to-End Relay (SLACK-FED-E2E / SLACK-FED-UAT)

**Test:** Follow the procedure in `.planning/phases/95-slack-federated-bridge-relay-one-app-many-prefixes/95-03-SUMMARY.md` § "Manual E2E UAT Procedure":
1. Run `km slack init` on installs A (front door) and B with the same xoxb + signing secret.
2. Set Slack App Events Request URL to A's `{bridge-url}/events`.
3. Set A's `km-config.yaml` `slack.peer_bridges` to `[B's /events URL]`; B's to `[A's /events URL]`.
4. On each install: `make build-lambdas` + `km init --dry-run=false` (NOT `--sidecars`).
5. Confirm `KM_SLACK_PEER_BRIDGES` in Lambda config; `km doctor` on A reports OK.
6. Create a sandbox under B; post a message in its `#sb-{id}`; observe CloudWatch.

**Expected:**
- A's bridge CloudWatch shows relay broadcast (to B).
- B's bridge CloudWatch shows relayed request processed (FetchByChannel hit, SQS enqueue).
- 👀 ack posted in Slack; no Slack retries visible (3s window honored).
- Negative check: relayed miss for unknown channel logs `slack_relay_no_owner`, no loop.
- Single-install without `slack.peer_bridges` set: behavior byte-identical to pre-Phase-95.

**Why human:** Requires a real Slack App with xoxb + signing secret, two live km installs deployed in one AWS account/region, and real Lambda invocations visible in CloudWatch. Unit tests cover all code paths at the unit level; the live integration path cannot be exercised without real Slack event delivery.

---

## Automated Test Results (All Green)

```
go test ./pkg/slack/bridge/... -run "TestPeerRelayer|TestEventsHandler_FederatedRelay|TestEventsHandler_NilRelayer|TestVerifySlackSignature_Relayed"
PASS  (6.172s, 9 tests)

go test ./internal/app/config/... -run "TestLoadSlackPeerBridges"
PASS  (0.278s, 2 tests)

go test ./internal/app/cmd/... -run "TestExportTerragruntEnvVars_PeerBridges"
PASS  (0.716s, 4 tests)

go test ./internal/app/cmd/... -run "TestCheckSlackPeerBridges"
PASS  (0.814s, 6 tests)

go build ./...
PASS  (clean, no errors)
```

Total new tests for Phase 95: 21 (9 bridge, 2 config, 4 init, 6 doctor).

---

_Verified: 2026-06-05T07:00:00Z_
_Verifier: Claude (gsd-verifier)_
