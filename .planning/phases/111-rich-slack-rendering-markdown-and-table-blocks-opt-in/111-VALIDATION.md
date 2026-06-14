---
phase: 111
slug: rich-slack-rendering-markdown-and-table-blocks-opt-in
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-14
---

# Phase 111 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Source: `111-RESEARCH.md` § Validation Architecture (RICH-01..RICH-20).

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` (stdlib) — no external test dependencies |
| **Config file** | none |
| **Quick run command** | `go test ./pkg/slack/... -run TestRich -count=1 -timeout 60s` |
| **Full suite command** | `go test ./pkg/slack/... ./cmd/km-slack/... -count=1 -timeout 120s` |
| **Estimated runtime** | ~10–20 seconds (pkg+cmd); full repo gate `go test ./... -count=1 -timeout 600s` |

---

## Sampling Rate

- **After every task commit:** `go test ./pkg/slack/... -run TestRich -count=1 -timeout 60s`
- **After every plan wave:** `go test ./pkg/slack/... ./cmd/km-slack/... -count=1 -timeout 120s`
- **Before `/gsd:verify-work`:** `go test ./... -count=1 -timeout 600s` full suite green (read go test's OWN exit, not a pipe — per `feedback_check_go_test_exit_not_pipe`)
- **Max feedback latency:** ~20 seconds (quick); ~10 min (full repo gate)

---

## Per-Task Verification Map

Requirement-level map from RESEARCH (task IDs assigned by the planner; each RICH-NN must land in a plan task's `must_haves`/verification).

| Req | Behavior | Test Type | Automated Command | File Exists | Status |
|-----|----------|-----------|-------------------|-------------|--------|
| RICH-01 | `RenderRich` prose → `markdown` block JSON | unit | `go test ./pkg/slack/... -run TestRichBlocks_ProseMarkdown -count=1` | ❌ W0 | ⬜ pending |
| RICH-02 | Leading H1 → `header` block (not inside markdown block) | unit | `go test ./pkg/slack/... -run TestRichBlocks_H1Header -count=1` | ❌ W0 | ⬜ pending |
| RICH-03 | Tool lines → `context` block (unchanged from Tier-2) | unit | `go test ./pkg/slack/... -run TestRichBlocks_ToolLine -count=1` | ❌ W0 | ⬜ pending |
| RICH-04 | GFM table → `table` block, correct `column_settings` alignment | unit | `go test ./pkg/slack/... -run TestRichTable_Alignment -count=1` | ❌ W0 | ⬜ pending |
| RICH-05 | Table header row → `rich_text` bold cells | unit | `go test ./pkg/slack/... -run TestRichTable_HeaderBold -count=1` | ❌ W0 | ⬜ pending |
| RICH-06 | Pure-numeric body cells → `raw_number` | unit | `go test ./pkg/slack/... -run TestRichTable_RawNumber -count=1` | ❌ W0 | ⬜ pending |
| RICH-07 | Ragged rows padded to column count | unit | `go test ./pkg/slack/... -run TestRichTable_RaggedPad -count=1` | ❌ W0 | ⬜ pending |
| RICH-08 | Table >20 cols → monospace fallback (`fencePipeTables`) | unit | `go test ./pkg/slack/... -run TestRichTable_ColsGuard -count=1` | ❌ W0 | ⬜ pending |
| RICH-09 | Table >100 rows → monospace fallback | unit | `go test ./pkg/slack/... -run TestRichTable_RowsGuard -count=1` | ❌ W0 | ⬜ pending |
| RICH-10 | 12K cumulative markdown cap → `ok=false` + Tier-2 fallback | unit | `go test ./pkg/slack/... -run TestRichBlocks_12KCap -count=1` | ❌ W0 | ⬜ pending |
| RICH-11 | 50-block cap → `ok=false` + Tier-2 fallback | unit | `go test ./pkg/slack/... -run TestRichBlocks_50BlockCap -count=1` | ❌ W0 | ⬜ pending |
| RICH-12 | `recover()` on panic → `ok=false` (fail-soft) | unit | `go test ./pkg/slack/... -run TestRichBlocks_PanicRecover -count=1` | ❌ W0 | ⬜ pending |
| RICH-13 | H1 inside code fence NOT promoted to header | unit | `go test ./pkg/slack/... -run TestRichBlocks_H1InCodeFence -count=1` | ❌ W0 | ⬜ pending |
| RICH-14 | `KM_SLACK_AI_FOOTER=true` → trailing context block | unit | `go test ./cmd/km-slack/... -run TestRunWith_AIFooter -count=1` | ❌ W0 | ⬜ pending |
| RICH-15 | `blocks-rich` mode selected in `runPost` AND `runReply` | unit | `go test ./cmd/km-slack/... -run 'TestRunPost_BlocksRich\|TestRunReply_BlocksRich' -count=1` | ❌ W0 | ⬜ pending |
| RICH-16 | Fallback chain `renderRich→RenderBlocks→Mrkdwnify` | unit | `go test ./cmd/km-slack/... -run TestRunWith_FallbackChain -count=1` | ❌ W0 | ⬜ pending |
| RICH-17 | Corpus golden: prose+table → expected blocks JSON | golden | `go test ./pkg/slack/... -run TestRichCorpus -count=1` | ❌ W0 | ⬜ pending |
| RICH-18 | `blocks` (Tier-2) default path unmodified — existing goldens green | regression | `go test ./pkg/slack/... -run TestBlocksCorpus -count=1` | ✅ existing | ⬜ pending |
| RICH-19 | Output is valid Block Kit JSON (structural property test) | property | `go test ./pkg/slack/... -run TestRichBlocks_StructuralValidity -count=1` | ❌ W0 | ⬜ pending |
| RICH-20 | Compiler golden byte-identity unaffected (no new NotifyEnv keys) | regression | `go test ./pkg/compiler/... -run TestUserdataAdditionalVolumeOnly_Golden -count=1` | ✅ existing | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/slack/testdata/rich-prose-basic.md` + `rich-prose-basic.expected-blocks.json` — prose → markdown block
- [ ] `pkg/slack/testdata/rich-table-basic.md` + `rich-table-basic.expected-blocks.json` — 3-col table with mixed alignment
- [ ] `pkg/slack/testdata/rich-mixed.md` + `rich-mixed.expected-blocks.json` — H1 + prose + table + 🔧 tool line
- [ ] `pkg/slack/testdata/rich-table-guards.md` — >20-col table (asserts monospace fallback, no expected-blocks.json)
- [ ] `pkg/slack/blocks_rich_test.go` — RICH-01..RICH-13, RICH-17, RICH-19 unit/golden/property tests
- [ ] `cmd/km-slack/main_rich_test.go` — RICH-14..RICH-16 runWith/runPost/runReply integration tests
- [ ] No new test infrastructure — existing `go test` conventions apply

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| `markdown`/`table` blocks render acceptably on real Slack (desktop + mobile) | RICH-04, RICH-17 | Slack client rendering not assertable from Go; table-block GA surface needs eyeball | `KM_SLACK_RENDER=blocks-rich` on a test sandbox/profile, post a prose+table+links message, confirm headings/anchors/table render; check mobile + the email-notification `text` fallback |
| 12K cumulative cap real-API behavior | RICH-10 | Slack's actual rejection response not unit-assertable | Post a >12K markdown payload, confirm graceful Tier-2 fallback (no dropped message) |
| `rich_text` bold header-cell schema accepted by `table` block API | RICH-05 | Cell `rich_text` schema is the LOW-confidence research item | UAT: confirm header row renders bold and Slack does not reject the payload; if rejected, downgrade header cells to `raw_text` |

*These are the Phase 111 soak/UAT items that gate Phase 112 (default flip).*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 20s (quick)
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
</content>
