---
phase: 119
slug: slack-inbound-per-thread-parallelism
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-24
---

# Phase 119 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go standard `testing` (table-driven), in-repo |
| **Config file** | none (`go test`) |
| **Quick run command** | `go test ./pkg/slack/bridge/ ./pkg/profile/ ./pkg/compiler/ ./pkg/aws/ -count=1` |
| **Full suite command** | `go test ./... -count=1 -timeout 600s` |
| **Estimated runtime** | quick ~30s · full ~5–8 min |

**Note:** capture the command's OWN exit code, not a piped `tail` (memory `feedback_check_go_test_exit_not_pipe`). `internal/app/cmd` tests may show `InvalidGrantException` on expired AWS SSO (environmental, not a regression — memory `project_cmd_suite_pre_existing_failures`).

---

## Sampling Rate

- **After every task commit:** `go test ./pkg/slack/bridge/ ./pkg/profile/ ./pkg/compiler/ ./pkg/aws/ -count=1`
- **After every plan wave:** `go test ./... -count=1 -timeout 600s`
- **Before `/gsd:verify-work`:** full suite green **AND** the live synthetic-HMAC E2E (assertions 1–6). The live E2E is **NON-OPTIONAL** — the concurrency logic is bash, invisible to Go goldens.
- **Max feedback latency:** ~30s (quick), ~8 min (full)

---

## Per-Task Verification Map

| Behavior | Requirement | Test Type | Automated Command | File Exists | Status |
|----------|-------------|-----------|-------------------|-------------|--------|
| Bridge `MessageGroupId == threadTS` (files + no-files paths); `== msg.TS` when no thread | P119-A | unit | `go test ./pkg/slack/bridge/ -run TestEventsHandler` | ❌ W0 (extend `events_handler_test.go`; `fakeSQS.Send` `:165` captures `group`) | ⬜ pending |
| Schema accepts `maxConcurrentThreads`; default 1; `minimum:1` rejects 0/neg; `additionalProperties:false` | P119-C/G | unit | `go test ./pkg/profile/ -run TestValidate` | ❌ W0 (extend `validate_slack_inbound_test.go`) | ⬜ pending |
| `km validate` WARN cap>1 w/o perSandbox+inbound.enabled | P119-G | unit | `go test ./pkg/profile/ -run TestValidate` | ❌ W0 | ⬜ pending |
| `KM_SLACK_MAX_CONCURRENCY=N` in `notify.env`/`profile.d` when cap>1; ABSENT when cap=1 | P119-C | unit (substring) | `go test ./pkg/compiler/ -run TestUserData` | ❌ W0 (new substring test) | ⬜ pending |
| `inboundQueueAttrs` carries chosen base `VisibilityTimeout` | P119-E | unit | `go test ./pkg/aws/ -run TestInboundQueueAttrs` | ❌ W0 | ⬜ pending |
| Default-1 profile renders byte-identical userdata to Phase 118 (dormancy) | dormancy | golden | `go test ./pkg/compiler/ -run TestUserdataLearnV2Phase92ByteIdentity` | ✅ (`userdata_phase92_byte_identity_test.go:108`) — stays green via hand-patch | ⬜ pending |
| parallelism / ordering / cap / heartbeat-no-dup / dedup (bash runtime) | P119-B/D/E/F | live E2E | synthetic HMAC `/events` POST (below) | ❌ W0 (helper script) | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/slack/bridge/events_handler_test.go` — add `MessageGroupId == threadTS` cases (files + no-files) + `== msg.TS` top-level case (P119-A)
- [ ] `pkg/profile/validate_slack_inbound_test.go` — add cap>1 WARN case + `minimum:1` schema-reject case (P119-C/G)
- [ ] `pkg/compiler/userdata_test.go` — add `KM_SLACK_MAX_CONCURRENCY` substring test (present when cap>1, absent when cap=1) (P119-C)
- [ ] `pkg/aws/sqs_test.go` (or existing) — assert new base `VisibilityTimeout` in `inboundQueueAttrs` (P119-E)
- [ ] Hand-patch `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` for the poller-bash change — **NEVER re-capture** (memory `project_frozen_byte_identity_golden_capture_trap`)
- [ ] E2E helper `/tmp/km119_send_event.sh` — Phase-114-style multi-thread variant
- [ ] No framework install needed (Go testing in place)

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Parallelism — 3 threads run concurrently | P119-B | Concurrency is bash runtime, invisible to Go goldens | Fire A/B/C simultaneously via `/tmp/km119_send_event.sh` (distinct `thread_ts`, ~30–60s prompts) → `km agent list <sb>` shows 3 overlapping runs; wall-clock ≈ max(turn), not sum |
| Per-thread ordering | P119-D | Requires real SQS FIFO + live poller | 2 back-to-back msgs in thread A → 2nd run starts only after 1st completes; replies ordered (`conversations.replies`) |
| Cap enforcement | P119-E | Live concurrency count | 5 threads, cap=3 → never >3 concurrent (overlapping RUN_DIRs / `km agent list`) |
| Heartbeat / no-dup | P119-E/F | Needs a turn longer than base visibility timeout | one long turn → exactly ONE reply (no redelivery); poller journal shows `ChangeMessageVisibility` ticks |
| Dormant regression | dormancy | Live serial behavior | cap=1 sandbox → serial, identical to Phase 118 |
| Dedup | P119-F | Live nonce + poller guard | replay same `event_id` → single-processed |

**E2E caveat (harness artifact):** a fabricated `ts` is not a real Slack message, so the 👀 reaction + threaded reply may fail with `message_not_found`/`thread_not_found` — harmless. Verify via side effects (run dirs, DDB row, `conversations.history`/`conversations.replies`), not the reaction.

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s (quick) / live E2E before phase gate
- [ ] `nyquist_compliant: true` set in frontmatter (after Wave 0 lands)

**Approval:** pending
