---
phase: 103-hackerone-comment-trigger-bridge
plan: 04
subsystem: h1-bridge
tags: [hackerone, webhook-handler, multi-target-fanout, reply-gate, dynamodb, tdd, port]

# Dependency graph
requires:
  - phase: 103-hackerone-comment-trigger-bridge
    plan: 02
    provides: "Resolve(handle)->[]Target, forked interfaces (H1ThreadStore + internal-by-default H1Commenter), ContainsHandle/ExtractBody"
  - phase: 103-hackerone-comment-trigger-bridge
    plan: 03
    provides: "ParsePayload, VerifyH1Signature, H1Envelope (ReplyToResearcher zero=internal), RunCommandPass/ParseResult.ReplyToResearcher, ExpandTemplateFields, CommandSet/CommandEntry"
provides:
  - "pkg/h1/bridge/webhook_handler.go — WebhookHandler.Handle() ~11-step flow: verify, event-gate, parse, loop-guard, resolve, thread-bypass, trigger-gate, authz, dedup, command-pass, multi-target-fanout-dispatch, internal-ACK"
  - "ComputeReplyToResearcher(commandPresent, actor, allow) — exported safety-critical reply gate (command AND allowlist; deny-by-default)"
  - "pkg/h1/bridge/aws_adapters.go — DynamoH1ThreadStore (report_id+target, UpdateItem), DynamoH1NonceStore (h1-delivery: prefix), DynamoAliasResolver(+status), H1SQSAdapter, EventBridgeAdapter, EC2Resumer, DynamoSandboxStatusWriter, SSMSecretFetcher, SSMCommandsFetcher, H1APICommenter (internal ACK)"
  - "H1DeliveryNoncePrefix + H1DeliveryNonceTTLSeconds consts"
affects: [103-07-userdata-poller, 103-08-lambda-main]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Multi-target fanout: single-target 3-way dispatch wrapped in `for i, target := range targets` with per-target distinct dedupID (h1-{report_id}-{alias}) + per-(report_id,target) thread upsert"
    - "Two-trigger gate: auto-triage (event ∈ events: map, no allow gate — OQ3) vs comment-keyword (ContainsHandle + deny-by-default allow gate); thread-bypass on known reports"
    - "Safety reply gate at the handler: ComputeReplyToResearcher = command-present AND actor∈allow; only targets[0] may carry the external flag; never N external replies"
    - "Internal-error→200 (never 5xx) so the platform does not redeliver with a fresh GUID that bypasses dedup"

key-files:
  created:
    - pkg/h1/bridge/aws_adapters.go
    - pkg/h1/bridge/aws_adapters_test.go
    - pkg/h1/bridge/webhook_handler.go
    - pkg/h1/bridge/webhook_handler_test.go
    - pkg/h1/bridge/webhook_handler_replygate_test.go
  modified: []

key-decisions:
  - "Reply gate enforced at the handler (the authoritative layer): ComputeReplyToResearcher requires BOTH /reply_to_researcher AND allowlist membership; the per-target primary-only rule (`researcherReply && i == 0`) lives in the fanout loop so an external reply is posted by EXACTLY ONE target"
  - "Comment-keyword non-allowlisted actor is silent-dropped at the allow gate BEFORE dispatch (even stricter than a downgrade); the downgrade leg of ComputeReplyToResearcher is the defense-in-depth backstop, proven directly by a truth-table unit test"
  - "Loop guard compares actor username against APIUsername (Basic-Auth identity) — HackerOne has no Bot user type, so the GitHub bot-type check is dropped"
  - "Stateless adapters are thin H1-named wrappers (not edits to pkg/github/bridge) to keep the 6 shipped GitHub-bridge phases uncoupled; only the h1_inbound_queue_url attribute name + log prefixes differ"
  - "H1APICommenter (customer-API Basic Auth) serves the bridge's synchronous INTERNAL ack only — researcher-visible replies come from the sandbox helper (cmd/km-h1), never the bridge"

requirements-completed: [H1-BRIDGE-DEDUP, H1-TRIGGER-AUTOTRIAGE, H1-TRIGGER-MENTION, H1-FANOUT-MULTITARGET, H1-DISPATCH-3WAY, H1-THREAD-CONTINUITY, H1-REPLY-RESEARCHER-GATED, H1-REPLY-INTERNAL-DEFAULT]

# Metrics
duration: 17min
completed: 2026-06-10
---

# Phase 103 Plan 04: H1 webhook handler — two triggers, multi-target fanout, safety reply gate Summary

**The heart of the HackerOne bridge: a ported-and-reshaped `Handle()` flow that converges every Phase 103 requirement — the two trigger models (auto-triage event-gate + literal-handle comment-keyword), the genuinely-new multi-target fanout loop (one trigger → N distinct dispatches + N thread rows), thread continuity keyed by (report_id, target), and the safety-critical reply gate (internal-by-default, allowlist-gated, primary-only external) — backed by the forked H1 AWS adapters (DynamoH1ThreadStore + stateless wrappers).**

## Performance

- **Duration:** ~17 min
- **Tasks:** 3 (all TDD: RED → GREEN)
- **Files created:** 5 (2 impl, 3 test)
- **Test result:** `go test ./pkg/h1/bridge -count=1` green (25 new tests pass; full package green); `go vet` clean

## Accomplishments

### Task 1 — aws_adapters.go (thread store + stateless wrappers)
- **`DynamoH1ThreadStore`** keyed `PK=report_id, SK=target` — all session writes UpdateItem-shaped (never full-row PutItem, the SandboxMetadata lossy round-trip footgun). `TestThreadStore_MultiTarget` proves N targets on one report write N distinct rows that don't collide (the fanout-continuity safety property).
- **`DynamoH1NonceStore`** reuses the shared nonces-table shape with the `h1-delivery:` key prefix (isolated from Slack/GitHub keys); `H1DeliveryNoncePrefix` + `H1DeliveryNonceTTLSeconds` (24h) consts. A `ConditionalCheckFailed` → `replayed=true`.
- **Thin H1-named wrappers** for the stateless/product-agnostic adapters: `DynamoAliasResolver`(+WithStatus, reads `h1_inbound_queue_url`), `H1SQSAdapter`, `EventBridgeAdapter` (`h1-profiles/{slug}` + `h1_envelope` detail), `EC2Resumer` (stopping-tolerant bounded poll, ported), `DynamoSandboxStatusWriter`, `SSMSecretFetcher`, `SSMCommandsFetcher`.
- **`H1APICommenter`** (customer-API Basic Auth) posts the synchronous INTERNAL ack only; `internal` is passed through verbatim (the handler decides visibility — this adapter never defaults to public).

### Task 2 — Handle() flow (two triggers, fanout, 3-way dispatch)
- Ported the GitHub 11-step pipeline and applied the H1 deltas: signature verify (bad→401, secret-fetch error→200), event-gate (`report_comment_created` OR any event in a resolved program's `events:` map), tolerant parse, loop-guard (actor==APIUsername, no Bot type), `Resolve()`→`[]Target` (miss→200 drop), thread-bypass (any known (report,target) row), trigger-gate, authz, dedup.
- **Trigger asymmetry:** auto-triage builds the prompt from `events[event].prompt` via `ExpandTemplateFields` and does NOT gate on `allow` (OQ3 — the operator's `events:` choice is the authorization); comment-keyword runs `ContainsHandle` + the deny-by-default allow gate + `RunCommandPass`.
- **Multi-target fanout (`for i, target := range targets`):** each target gets a distinct `dedupID = {guid}-h1-{report_id}-{alias}` (so N targets are not deduped to one), a `groupID = h1-{report_id}-{alias}`, a 3-way dispatch (warm→SQS / cold→EventBridge / resume→StartSandbox+SQS), and its own `(report_id, target)` thread upsert.
- Internal errors (SQS/DDB/nonce) → 200 (never 5xx); exactly one synchronous **internal** ack comment on a successful dispatch.

### Task 3 — the safety-critical reply gate
- **`ComputeReplyToResearcher(commandPresent, actor, allow)`** (exported): researcher-visible reply ⇔ `/reply_to_researcher` present **AND** actor ∈ allow. Command-present-but-not-allowlisted **downgrades to internal**; empty allowlist → false (deny-by-default). A 5-row truth-table unit test (`TestReplyGate_DowngradePure`) proves the BOTH-required contract in isolation.
- **Primary-only external** (`researcherReply && i == 0`): under fanout exactly ONE target (targets[0]) may carry `ReplyToResearcher=true`; every other target is forced internal. `TestReplyGate_NeverNExternal` (3 targets) asserts exactly one external envelope. `TestReplyGate_DefaultInternal` proves internal-by-default; `TestReplyGate_NotAllowed` proves zero external replies for a non-allowlisted actor by any path.

## Task Commits

1. **Task 1 RED** (thread-store + nonce tests) — `e103dba3` (test)
2. **Task 1 GREEN** (aws_adapters.go) — `d183fa68` (feat)
3. **Task 2 RED** (Handle() flow tests) — `ee37e954` (test)
4. **Task 2 GREEN** (webhook_handler.go) — `a34bae1e` (feat)
5. **Task 3** (reply-gate tests + export gate fn) — `37700fec` (test)

## Files Created
- `pkg/h1/bridge/aws_adapters.go` — DynamoH1ThreadStore + DynamoH1NonceStore + 8 stateless wrappers + H1APICommenter (~640 lines)
- `pkg/h1/bridge/aws_adapters_test.go` — thread store (Upsert/MultiTarget/Lookup/UpdateSession/Invalidate) + nonce prefix (~300 lines)
- `pkg/h1/bridge/webhook_handler.go` — WebhookRequest/Response/Handler, Handle() flow, fanout dispatch, reply gate (~430 lines)
- `pkg/h1/bridge/webhook_handler_test.go` — Dedup/AutoTriage/Mention/LoopGuard/Authz/Fanout/Dispatch/InternalError200/ACK/BadSignature (~500 lines)
- `pkg/h1/bridge/webhook_handler_replygate_test.go` — DefaultInternal/AllowedPrimary/NotAllowed/DowngradePure/SingleTargetExternal/NeverNExternal (~210 lines)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Exported the safety-critical reply gate for direct truth-table testing**
- **Found during:** Task 3
- **Issue:** The plan placed `computeReplyToResearcher` as an unexported helper. The CONTEXT lock (command-present-alone is NOT sufficient → downgrade) is a safety property that deserves an in-isolation truth-table test, not just indirect coverage via Handle(). The comment-keyword allow-gate silent-drops a non-allowlisted actor BEFORE dispatch, so the downgrade leg of the gate is otherwise hard to exercise end-to-end.
- **Fix:** Exported `ComputeReplyToResearcher` and added `TestReplyGate_DowngradePure` — a 5-case truth table (command×allow) proving BOTH-required deny-by-default directly. The end-to-end `TestReplyGate_NotAllowed` was reframed to assert the safety invariant ("zero external replies for a non-allowlisted actor, by any path") rather than a specific drop-vs-downgrade mechanism.
- **Files modified:** `pkg/h1/bridge/webhook_handler.go`, `pkg/h1/bridge/webhook_handler_replygate_test.go`
- **Verification:** `go test ./pkg/h1/bridge -run TestReplyGate -count=1` green.
- **Commit:** `37700fec`

**Total deviations:** 1 auto-fixed (1 missing-critical — test surface for the safety gate).
**Impact:** No scope change; strengthens the safety-critical test coverage the plan called out as highest-risk.

## Coordination Notes
- Ran in parallel with Plan 103-06 (`internal/app/cmd` km h1 CLI — different package, no shared files). Plan 06's commits (`c27df983`, `2784bfa5`) landed between my task commits; no conflict. Stayed strictly within `pkg/h1/bridge/` (my plan's `files_modified`).
- The untracked `km-h1` binary in the repo root (a sibling-plan build artifact) was left untouched.
- No production HackerOne program is referenced; synthetic handle `km-sandbox` is used throughout. The live UAT target (the operator's HackerOne Sandbox account) is exercised in Wave 6, not here.

## Issues Encountered
- None blocking. The `TestReplyGate_NotAllowed` reframing (above) was the only adjustment — it surfaced that the comment-keyword allow gate is strictly safer than the downgrade (drops before dispatch), which is the desired behavior.

## Next Phase Readiness
- Plan 07 (userdata poller): consumes `H1Envelope` (incl. `ReplyToResearcher`, `Agent`, `Body`) from the per-sandbox h1-inbound FIFO; the poller posts the agent's reply via `cmd/km-h1` honoring the envelope's reply flag.
- Plan 08 (Lambda main): wires the concrete adapters (`SSMSecretFetcher`, `DynamoH1NonceStore`, `DynamoAliasResolver`, `H1SQSAdapter`, `EventBridgeAdapter`, `EC2Resumer`, `DynamoSandboxStatusWriter`, `DynamoH1ThreadStore`, `H1APICommenter`) into `WebhookHandler`, lowercases headers, and base64-decodes the body before `VerifyH1Signature`.
- Wave 6 (E2E/UAT): re-pin the real HackerOne Sandbox webhook envelope; the parse is already tolerant and the handler fails safe on resolve-miss.

## Self-Check: PASSED

All 5 created files exist on disk; all 5 task commits (`e103dba3`, `d183fa68`, `ee37e954`, `a34bae1e`, `37700fec`) present in git history. `go test ./pkg/h1/bridge -count=1` green (25 plan-04 tests pass); both plan verification commands and `go vet` pass.

---
*Phase: 103-hackerone-comment-trigger-bridge*
*Completed: 2026-06-10*
