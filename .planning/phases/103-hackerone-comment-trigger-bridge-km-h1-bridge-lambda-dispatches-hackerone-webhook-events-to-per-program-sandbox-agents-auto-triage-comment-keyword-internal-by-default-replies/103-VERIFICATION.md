---
phase: 103-hackerone-comment-trigger-bridge
verified: 2026-06-10T00:00:00Z
status: human_needed
score: 16/17 requirements verified in code; 1 (H1-E2E) is an operator-run live UAT
re_verification:
  previous_status: none
  note: initial verification
human_verification:
  - test: "Live HackerOne webhook reply-visibility UAT (Plan 10 Task 3 / 103-UAT.md Part 2)"
    expected: "A lifecycle/comment webhook from the HackerOne Sandbox program produces an INTERNAL triage comment; an allowlisted @handle /reply_to_researcher produces a researcher-visible reply; a non-allowlisted /reply_to_researcher is downgraded to internal."
    why_human: "Requires a live HackerOne Sandbox program + real webhook deliveries + visual confirmation of comment internal/external visibility in the HackerOne UI. The RUN_H1_E2E=1-gated harness (test/e2e/h1/e2e_test.go) and the 103-UAT.md runbook exist; only the live execution against the operator-provisioned Sandbox program remains. This is a P0 safety-critical visual check, not a code gap."
---

# Phase 103: HackerOne comment-trigger bridge — Verification Report

**Phase Goal:** A HackerOne program webhook drives a sandbox agent turn the same way a GitHub PR comment does (Phase 97-102): a single km-h1-bridge Lambda Function URL HMAC-verifies X-H1-Signature, dedupes by X-H1-Delivery, resolves the report's program handle to one-or-more sandbox targets from `h1.programs:`, and dispatches an agent turn (warm FIFO / cold create / resume) with report-id-keyed thread continuity — via two trigger models (opt-in lifecycle auto-triage + configurable @-handle comment-keyword), config-driven event→prompt mappings, multi-target fanout, and a reply path that is INTERNAL by default with an allowlist-gated /reply_to_researcher. The agent posts back through a new cmd/km-h1 helper using HackerOne customer-API Basic Auth.

**Verified:** 2026-06-10
**Status:** human_needed (all code-verifiable items pass; only the live reply-visibility UAT is outstanding)
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| #  | Truth | Status | Evidence |
| -- | ----- | ------ | -------- |
| 1  | km-h1-bridge Function URL HMAC-verifies X-H1-Signature over the base64-DECODED body | ✓ VERIFIED | `payload.go:259-276` VerifyH1Signature documents+enforces HMAC over decoded bytes; `cmd/km-h1-bridge/main.go:286,325` IsBase64Encoded + DecodeString before verify; Function URL auth NONE (`main.tf:349`) with app-layer HMAC |
| 2  | Dedup by X-H1-Delivery via shared nonces table with `h1-delivery:` prefix | ✓ VERIFIED | `webhook_handler.go:288` reads `x-h1-delivery`; `aws_adapters.go:86` `H1DeliveryNoncePrefix = "h1-delivery:"`; DynamoH1NonceStore reuses shared nonces table |
| 3  | Program handle resolves to targets[]/allow/events/commands; miss → 200-drop | ✓ VERIFIED | `resolve.go` Resolve(); config tests `TestLoadH1_*` pass; `webhook_handler.go:171` Resolve call; miss path drops |
| 4  | Opt-in auto-triage: event in `events:` dispatches; unlisted drops; empty `events:` dormant | ✓ VERIFIED | `webhook_handler.go:147` event-gate step; handler header documents auto-triage = event presence; webhook_handler tests pass |
| 5  | @-handle comment-keyword trigger + known-thread @handle bypass | ✓ VERIFIED | `webhook_handler.go` ContainsHandle + ThreadStore-driven bypass (Step thread-bypass, line 91/130); tests pass |
| 6  | /commands parse: two distinct → MultiError; agent verbs /claude /codex; /reply_to_researcher reserved | ✓ VERIFIED | `commands.go` ParseCommands/RunCommandPass; `commands_test.go` passes; reserved tokens present |
| 7  | Config-driven event→prompt mapping with ExpandTemplate ({{args}},{{report_id}},{{title}},{{state}},{{program}}) | ✓ VERIFIED | `commands.go` ExpandTemplate; payload field accessors; tests pass |
| 8  | Multi-target fanout: same prompt → N targets, DISTINCT dedupIDs, N (report_id,target) thread rows | ✓ VERIFIED | `webhook_handler.go:315` `for i, target := range targets`; header documents per-target dedupID + thread row |
| 9  | 3-way dispatch warm-FIFO / cold-EventBridge / resume-StartInstances per target | ✓ VERIFIED | Handler Publisher (EventBridge), SQS (FIFO), Resumer (StartInstances) injected; header line 80-91 |
| 10 | Reply INTERNAL by default; /reply_to_researcher external ONLY when command present AND actor in allow; primary(index 0) only | ✓ VERIFIED | computeReplyToResearcher; `webhook_handler_replygate_test.go` full truth table (DowngradePure, NeverNExternal, SingleTargetExternal) all PASS |
| 11 | cmd/km-h1 helper: Basic Auth, internal default true, 429 backoff | ✓ VERIFIED | `cmd/km-h1/main.go:14-16` internal defaults true; defaultBackoff 1s/2s/4s; `TestCommentInternalDefault`, `TestRetry429` PASS |
| 12 | km h1 init/status; no manifest subcommand | ✓ VERIFIED | `TestH1Init`, `TestH1Init_NoManifest`, `TestH1Status`, `TestH1Status_Dormant` PASS |
| 13 | Deploy wiring: regionalModules (h1-bridge after h1-threads), lambdaBuilds, sidecarBuilds, env+SSM | ✓ VERIFIED | `TestRegionalModulesIncludesH1Bridge`, `TestRegionalModulesH1BridgeOrdering`, `TestLambdaBuildsIncludesH1Bridge`, `TestH1BridgeBuildListMembership` PASS; km-h1 in sidecarBuilds (init.go:2606) |
| 14 | Dormancy: H1-disabled profile renders byte-identical userdata; enabled renders poller | ✓ VERIFIED | `TestUserdataH1ByteIdentity` + `TestUserdataH1EnabledRendersPoller` PASS; golden in pkg/compiler/testdata |
| 15 | profiles/h1-triage.yaml validates + api.hackerone.com egress | ✓ VERIFIED | `km validate profiles/h1-triage.yaml: valid` |
| 16 | Guard tests fail if H1 wiring is dropped + merge-list regression guard | ✓ VERIFIED | init_test.go + config_h1_test.go (`TestLoadH1_MergeListRegression`) PASS |
| 17 | Live HackerOne reply-visibility UAT | ? HUMAN | RUN_H1_E2E=1 harness + 103-UAT.md exist; live execution against operator-provisioned Sandbox program outstanding |

**Score:** 16/17 verified in code; 1 (H1-E2E) deferred to operator-run live UAT (human_needed, not a gap).

### Required Artifacts

| Artifact | Status | Details |
| -------- | ------ | ------- |
| `103-CAPTURE/{report_created,report_comment_created}.json`, `field-paths.md` | ✓ VERIFIED | Present; synthetic/docs-fallback by design (per phase context), real Sandbox capture folded into live UAT |
| `pkg/compiler/testdata/h1_byte_identity_golden.txt` | ✓ VERIFIED | Dormancy golden; TestUserdataH1ByteIdentity passes against it |
| `pkg/h1/bridge/{resolve,payload,commands,webhook_handler,aws_adapters,interfaces}.go` | ✓ VERIFIED | All exist + substantive; `go test ./pkg/h1/... ok` |
| `cmd/km-h1/main.go` | ✓ VERIFIED | comment/state/read, Basic Auth, internal-default, 429 backoff |
| `cmd/km-h1-bridge/main.go` | ✓ VERIFIED | base64 decode + lowercase headers + bridge.Handle wiring; builds |
| `internal/app/cmd/h1.go` + h1_test.go | ✓ VERIFIED | RunH1Init/RunH1Status; SSM /{prefix}/config/h1/* writes |
| `internal/app/config/config.go` H1Config + merge-list | ✓ VERIFIED | h1 unmarshals; merge-list regression guard passes |
| `infra/modules/lambda-h1-bridge/v1.0.0/*` | ✓ VERIFIED | Function URL auth NONE + IAM grants (SSM/DDB nonces+sandboxes+h1-threads/SQS/EventBridge/EC2) |
| `infra/modules/dynamodb-h1-threads/v1.0.0/*` | ✓ VERIFIED | PK=report_id SK=target + TTL |
| `infra/live/use1/{lambda-h1-bridge,dynamodb-h1-threads}/terragrunt.hcl` | ✓ VERIFIED | Source v1.0.0 |
| `internal/app/cmd/create_h1_inbound.go` | ✓ VERIFIED | provisionH1InboundQueue with DLQ RedrivePolicy(maxReceiveCount=3) |
| `pkg/compiler/userdata.go` H1 poller | ✓ VERIFIED | km-h1-inbound-poller heredoc, conditionally emitted; fetches km-h1 from s3 sidecars |
| `pkg/profile/types.go` notification.h1.inbound | ✓ VERIFIED | Schema field present; profile validates |
| `profiles/h1-triage.yaml` | ✓ VERIFIED | Validates |
| `test/e2e/h1/e2e_test.go` + `103-UAT.md` | ✓ VERIFIED | RUN_H1_E2E gate skips clean by default; UAT runbook complete |

### Key Link Verification

| From | To | Status | Details |
| ---- | -- | ------ | ------- |
| VerifyH1Signature | decoded body bytes | ✓ WIRED | HMAC over base64-decoded bytes (Pitfall 1 honored) |
| config.Load merge-list | cfg.H1 | ✓ WIRED | UnmarshalKey("h1") gated; regression guard passes |
| Handle() dispatch | targets[] fanout loop | ✓ WIRED | `for i, target := range targets` per-target dispatch + thread upsert |
| /reply_to_researcher gate | actor in allow + primary-only | ✓ WIRED | computeReplyToResearcher truth table tests PASS |
| DynamoH1ThreadStore | {prefix}-h1-threads | ✓ WIRED | UpdateItem (not PutItem) |
| regionalModules lambda-h1-bridge | live unit dir | ✓ WIRED | ordered after dynamodb-h1-threads; ordering test passes |
| create_h1_inbound | shared DLQ | ✓ WIRED | RedrivePolicy maxReceiveCount=3 (poison-wedge protection) |
| ExportTerragruntEnvVars | KM_H1_* | ✓ WIRED | env-block change documented (km init --dry-run=false not --sidecars) |
| userdata km-h1-inbound-poller | KM_H1_INBOUND_QUEUE_URL | ✓ WIRED | poller present; km-h1 fetched + symlinked |
| km h1 init webhook-secret | bridge SSMSecretFetcher | ✓ WIRED | same /{prefix}/config/h1/* path |

### Requirements Coverage

| Requirement | Status | Evidence |
| ----------- | ------ | -------- |
| H1-BRIDGE-HMAC | ✓ SATISFIED | VerifyH1Signature over decoded bytes; bridge main base64 decode |
| H1-BRIDGE-DEDUP | ✓ SATISFIED | h1-delivery: nonce prefix; shared nonces table |
| H1-RESOLVE-PROGRAM | ✓ SATISFIED | Resolve() + config getters; tests pass |
| H1-TRIGGER-AUTOTRIAGE | ✓ SATISFIED | event-gate step; empty events dormant |
| H1-TRIGGER-MENTION | ✓ SATISFIED | ContainsHandle + thread-bypass |
| H1-COMMAND-PARSE | ✓ SATISFIED | ParseCommands two-command MultiError |
| H1-AGENT-VERB | ✓ SATISFIED | /claude /codex select; conflict error |
| H1-EVENT-PROMPT-MAP | ✓ SATISFIED | ExpandTemplate field refs |
| H1-FANOUT-MULTITARGET | ✓ SATISFIED | per-target loop, distinct dedupIDs, N thread rows |
| H1-DISPATCH-3WAY | ✓ SATISFIED | warm/cold/resume adapters |
| H1-THREAD-CONTINUITY | ✓ SATISFIED | (report_id,target) UpdateItem; poller resume |
| H1-REPLY-INTERNAL-DEFAULT | ✓ SATISFIED | km-h1 internal default true; bridge ACK internal-only |
| H1-REPLY-RESEARCHER-GATED | ✓ SATISFIED | computeReplyToResearcher truth table (command AND allow AND primary) |
| H1-HELPER-KM-H1 | ✓ SATISFIED | cmd/km-h1 comment/state/read Basic Auth |
| H1-CLI-INIT-STATUS | ✓ SATISFIED | km h1 init/status; no manifest |
| H1-DEPLOY-WIRING | ✓ SATISFIED | regionalModules/lambdaBuilds/sidecarBuilds/env/SSM + guard tests |
| H1-E2E | ? NEEDS HUMAN | live reply-visibility UAT against HackerOne Sandbox program |

### Anti-Patterns Found

| File | Pattern | Severity | Impact |
| ---- | ------- | -------- | ------ |
| internal/app/cmd/doctor_artifacts.go:351, doctor_log_groups.go:62/68/74/80 | hardcoded `km-` (hygiene TestGoSourceNamesUseResourcePrefix RED) | ℹ️ Info | PRE-EXISTING (Phase 94); verified RED before this phase; zero H1 source files flagged — NOT attributable to Phase 103 |
| TestUnlockCmd_RequiresStateBucket | environmental (expired SSO) | ℹ️ Info | Pre-existing/environmental; out of scope |

No blocker or warning anti-patterns introduced by Phase 103. Both deferred-items.md notes are addressed (km-h1 now gitignored at line 51) or correctly scoped out (pre-existing hygiene).

### Human Verification Required

**1. Live HackerOne reply-visibility UAT (P0 safety-critical)**
- **Test:** Follow 103-UAT.md Part 2 against the operator-provisioned HackerOne Sandbox program: fire a lifecycle/comment webhook; verify the triage comment lands INTERNAL; verify an allowlisted @handle /reply_to_researcher produces a researcher-visible reply; verify a non-allowlisted /reply_to_researcher is DOWNGRADED to internal.
- **Expected:** Internal-by-default holds; external replies only for allowlisted actors with explicit command on the primary target. Any reply that should have been internal but lands external = P0 bug.
- **Why human:** Requires live HackerOne Sandbox program + real deliveries + visual UI confirmation of comment visibility. Code paths (HMAC, gate truth table, base64 decode) are fully unit-verified; only live visual confirmation remains.

### Gaps Summary

No code gaps. All 16 code-verifiable requirements are satisfied with passing tests across pkg/h1/bridge, cmd/km-h1, cmd/km-h1-bridge, internal/app/cmd (H1 + deploy-surface guards), internal/app/config (merge-list + getters), and pkg/compiler (dormancy golden + poller render). The lambda-h1-bridge module (Function URL auth NONE + full IAM), dynamodb-h1-threads table, live terragrunt units (v1.0.0), DLQ-protected inbound queue, and h1-triage profile all verify. km-h1 deploys via sidecarBuilds() (same proven path as km-github), not the Makefile `sidecars` upload target — the must_have phrasing "Makefile build-lambdas builds km-h1" is imprecise but the binary IS built/uploaded by `km init --sidecars` and the membership guard test confirms it.

The single outstanding item, H1-E2E, is an operator-run live UAT against a HackerOne Sandbox program (operator is provisioning it). The RUN_H1_E2E=1-gated harness and 103-UAT.md runbook are in place. Per phase context this is human_needed, not a code gap. The two known pre-existing failures (Phase-94 hygiene km- sites, environmental expired-SSO unlock test) are confirmed unrelated to Phase 103.

---

_Verified: 2026-06-10_
_Verifier: Claude (gsd-verifier)_
