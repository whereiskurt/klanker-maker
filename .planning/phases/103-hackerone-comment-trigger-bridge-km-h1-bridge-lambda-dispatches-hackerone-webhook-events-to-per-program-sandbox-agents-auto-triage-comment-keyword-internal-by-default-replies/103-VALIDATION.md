---
phase: 103
slug: hackerone-comment-trigger-bridge
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-09
---

# Phase 103 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Derived from 103-RESEARCH.md § Validation Architecture. The phase is ~90% a port of
> `pkg/github/bridge` (Phases 97-102); most tests port from the existing `pkg/github/bridge/*_test.go`.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go stdlib `testing` (table-driven), per `pkg/github/bridge/*_test.go` |
| **Config file** | none (Go modules) |
| **Quick run command** | `go test ./pkg/h1/... ./cmd/km-h1/... ./internal/app/cmd/... -run H1 -count=1` |
| **Full suite command** | `make test` (or `go test ./... -count=1`) |
| **Estimated runtime** | ~30–90 seconds (full suite is repo-wide) |

---

## Sampling Rate

- **After every task commit:** `go test ./pkg/h1/... ./cmd/km-h1/... ./internal/app/cmd/... -run H1 -count=1`
- **After every plan wave:** `make test` (full suite green)
- **Before `/gsd:verify-work`:** Full suite green + deploy-surface guard tests pass + Wave-0 live payload capture done
- **Max feedback latency:** ~90 seconds

---

## Per-Task Verification Map

| Req ID | Behavior | Test Type | Automated Command | File Exists | Status |
|--------|----------|-----------|-------------------|-------------|--------|
| H1-BRIDGE-HMAC | sha256 verify of raw + base64 body; 401 on mismatch | unit | `go test ./pkg/h1/bridge -run TestVerifyH1Signature` | ❌ W0 | ⬜ pending |
| H1-BRIDGE-DEDUP | replay GUID → 200 no-dispatch | unit (mock nonce) | `go test ./pkg/h1/bridge -run TestHandle_Dedup` | ❌ W0 | ⬜ pending |
| H1-RESOLVE-PROGRAM | handle → targets/allow/events/commands; miss → drop | unit (pure table) | `go test ./pkg/h1/bridge -run TestResolve` | ❌ W0 | ⬜ pending |
| H1-TRIGGER-AUTOTRIAGE | listed event dispatches; unlisted → drop; dormant when empty | unit | `go test ./pkg/h1/bridge -run TestHandle_AutoTriage` | ❌ W0 | ⬜ pending |
| H1-TRIGGER-MENTION | `@handle` present → dispatch; absent+unknown-thread → drop; thread-bypass | unit | `go test ./pkg/h1/bridge -run TestHandle_Mention` | ❌ W0 | ⬜ pending |
| H1-COMMAND-PARSE | ≤1 distinct command; MultiError → reply-no-dispatch | unit | `go test ./pkg/h1/bridge -run TestParseCommands` | partial (port GH) | ⬜ pending |
| H1-AGENT-VERB | `/claude`/`/codex` select; conflict → reply; carried to envelope | unit | `go test ./pkg/h1/bridge -run TestAgentVerb` | partial (port GH) | ⬜ pending |
| H1-EVENT-PROMPT-MAP | event→prompt + command→prompt expansion incl `{{args}}`/field refs | unit | `go test ./pkg/h1/bridge -run TestExpandTemplate` | partial | ⬜ pending |
| H1-FANOUT-MULTITARGET | N targets → N enqueues + N thread rows; distinct dedupIDs | unit (mock SQS/threads) | `go test ./pkg/h1/bridge -run TestHandle_Fanout` | ❌ W0 | ⬜ pending |
| H1-DISPATCH-3WAY | warm/cold/resume per target | unit (mock resolver+status) | `go test ./pkg/h1/bridge -run TestHandle_Dispatch` | ❌ W0 | ⬜ pending |
| H1-THREAD-CONTINUITY | (report_id,target) upsert; poller resumes session | unit + manual (poller) | `go test ./pkg/h1/bridge -run TestThreadStore` | ❌ W0 | ⬜ pending |
| H1-REPLY-INTERNAL-DEFAULT | helper default body `internal:true`; envelope zero=internal | unit | `go test ./cmd/km-h1 -run TestCommentInternalDefault` | ❌ W0 | ⬜ pending |
| H1-REPLY-RESEARCHER-GATED | public only when actor∈allow AND `/reply_to_researcher`; else downgraded | unit | `go test ./pkg/h1/bridge -run TestReplyGate` | ❌ W0 | ⬜ pending |
| H1-HELPER-KM-H1 | comment/state/read build correct Basic-Auth requests; 429 retry | unit (httptest) | `go test ./cmd/km-h1 -run TestHelper` | ❌ W0 | ⬜ pending |
| H1-CLI-INIT-STATUS | init writes SSM `/config/h1/*`; status redacts secret | unit (mock SSM) | `go test ./internal/app/cmd -run TestH1Init` | ❌ W0 | ⬜ pending |
| H1-DEPLOY-WIRING | guard: h1-bridge ∈ lambdaBuilds + regionalModules; km-h1 ∈ sidecarBuilds; `h1` ∈ merge-list | unit | `go test ./internal/app/cmd -run TestLambdaBuilds` + config-load test | ❌ W0 (mirror `TestLambdaBuildsIncludesGitHubBridge`) | ⬜ pending |
| H1-DEPLOY-WIRING | userdata byte-identity: unrelated profile renders IDENTICAL pre/post (dormancy) | golden | `go test ./pkg/compiler -run ByteIdentity` | mirror `userdata_phase92_byte_identity_test.go` | ⬜ pending |
| H1-E2E | live program: test webhook → internal triage comment; `@handle /command` → reply | E2E (live, gated) | `RUN_H1_E2E=1 go test ./test/e2e/h1/...` | ❌ W0 (manual UAT OK) | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/h1/bridge/webhook_handler_test.go` — HMAC, dedup, two-trigger, fanout, dispatch, reply-gate (port from `pkg/github/bridge/*_test.go`)
- [ ] `pkg/h1/bridge/resolve_test.go` — program-handle resolution table
- [ ] `pkg/h1/bridge/commands_test.go` — command/agent-verb + `/reply_to_researcher` reserved token (port)
- [ ] `pkg/h1/bridge/payload_test.go` — parse a **captured real webhook body** (Wave 0 live capture prerequisite)
- [ ] `cmd/km-h1/main_test.go` — Basic-Auth request shape, internal-default, 429 retry (httptest)
- [ ] `internal/app/cmd/h1_test.go` — init/status SSM
- [ ] `internal/app/cmd/init_test.go` additions — guard tests (h1-bridge ∈ lambdaBuilds, km-h1 ∈ sidecarBuilds, `h1` ∈ merge-list, h1-bridge ∈ regionalModules)
- [ ] `pkg/compiler/userdata_h1_byte_identity_test.go` — dormancy golden
- [ ] **Live capture (Wave 0, blocks Open Questions 1 & 2):** one real `report_created` + one `report_comment_created` body via the HackerOne Webhooks "Test request" → pin payload struct tags (program-handle path, internal-flag field, state endpoint)

*Existing `pkg/github/bridge/*_test.go` provides the table-driven harness pattern to port.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Webhook accepted in Recent-Deliveries inspector w/ valid signature | H1-BRIDGE-HMAC | Needs live HackerOne program + UI | Configure program webhook → bridge Function URL + secret from `km h1 init`; click **Test request**; confirm 200 in the Recent-Deliveries tab |
| Internal vs researcher-visible reply visibility | H1-REPLY-INTERNAL-DEFAULT / -RESEARCHER-GATED | Visibility only observable in live H1 UI | Trigger a triage → confirm comment is INTERNAL; trigger `@handle /reply_to_researcher` as an allowlisted user → confirm researcher-visible; as a non-allowlisted user → confirm downgraded/blocked |
| Exact payload field paths (program handle, internal flag, state endpoint) | H1-RESOLVE-PROGRAM, H1-REPLY-*, H1-HELPER-KM-H1 | HackerOne docs MEDIUM-confidence on these paths | Wave-0 live capture pins struct tags before payload_test.go is written |
| End-to-end auto-triage + comment-keyword + multi-target fanout | H1-E2E | Needs running sandbox(es) + live program | Full UAT runbook in PLAN (gated `RUN_H1_E2E=1`); manual UAT acceptable per Phase-97 precedent |
| HackerOne API rate-limit / 429 backoff under load | H1-HELPER-KM-H1 | Live API behavior | Observe helper under burst; confirm 429 retry/backoff path |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (incl. live payload capture)
- [ ] No watch-mode flags
- [ ] Feedback latency < 90s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
