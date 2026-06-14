---
phase: 111-rich-slack-rendering-markdown-and-table-blocks-opt-in
verified: 2026-06-14T16:21:24Z
status: human_needed
score: 20/20 must-haves verified
human_verification:
  - test: "Send a prose+table+links message with KM_SLACK_RENDER=blocks-rich on a real sandbox and view in Slack desktop client"
    expected: "Headings render with visual hierarchy (header block), prose paragraphs render with native GFM (bold/italic visible), [label](url) links are clickable anchors (not <url|label> mrkdwn), GFM tables render as native Slack tables (not monospace grids), tool lines render as context elements"
    why_human: "Slack client rendering of markdown and table blocks is not assertable from Go; the table block GA surface needs eyeball confirmation (desktop + mobile)"
  - test: "Check mobile client rendering of the same blocks-rich message"
    expected: "Header block, markdown blocks, and table blocks render acceptably on mobile; text fallback is clean for email notifications"
    why_human: "Mobile rendering of GA block types is not programmatically assertable"
  - test: "Post a message exceeding 12K cumulative markdown chars with KM_SLACK_RENDER=blocks-rich"
    expected: "Message is delivered via Tier-2 blocks fallback (no dropped message); Slack does not reject the payload"
    why_human: "Slack's actual rejection response on oversized payloads is not unit-assertable"
  - test: "Verify table block rich_text bold header-cell schema is accepted by the Slack API"
    expected: "Header row renders bold in the table block; Slack does not reject the payload with a schema error"
    why_human: "Cell rich_text schema was flagged as LOW-confidence in RESEARCH; real API acceptance must be confirmed before Phase 112 default flip"
---

# Phase 111: Rich Slack Rendering — Verification Report

**Phase Goal:** Claude's Slack output renders like real markdown — proper headings, native clickable link anchors, and actual tables — instead of mrkdwn reflow + monospace grids. Adopt Slack's `markdown` block (GA Feb 2025) and dedicated `table` block (GA Aug 2025), shipped OPT-IN so the default path and its golden tests stay frozen until a later flip phase (Phase 112).

**Verified:** 2026-06-14T16:21:24Z
**Status:** human_needed
**Re-verification:** No — initial verification

---

## Goal Achievement

All 20 automated requirements (RICH-01..RICH-20) verified. Phase goal is code-complete. Four items require real-Slack UAT before the Phase 112 default flip — these were expected per `111-VALIDATION.md` § Manual-Only Verifications and are NOT gaps in the code deliverable.

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | RenderRich renders prose as a Slack markdown block carrying verbatim GFM | VERIFIED | `TestRichBlocks_ProseMarkdown` passes; golden `rich-prose-basic.expected-blocks.json` shows `{"type":"markdown","text":"..."}` with `[clickable link](https://example.com)` verbatim (NOT converted to `<url|label>`) |
| 2 | A leading H1 is promoted to a real header block, not buried inside a markdown block | VERIFIED | `TestRichBlocks_H1Header` passes; `rich-prose-basic.expected-blocks.json` shows a `{"type":"header","text":{"type":"plain_text","text":"Build Complete"}}` block preceding the markdown block |
| 3 | Tool lines (🔧) render as context blocks, identical to Tier-2 | VERIFIED | `TestRichBlocks_ToolLine` passes; `rich-mixed.expected-blocks.json` shows `{"type":"context","elements":[{"type":"mrkdwn","text":"🔧 Edit:..."}]}` |
| 4 | A GFM pipe table becomes a Slack table block with column_settings alignment | VERIFIED | `TestRichTable_Alignment` passes; `rich-table-basic.expected-blocks.json` shows `{"type":"table","column_settings":[{"align":"left",...},{"align":"center",...},{"align":"right",...}],...}` |
| 5 | The header row renders as bold rich_text cells | VERIFIED | `TestRichTable_HeaderBold` passes; golden shows `{"type":"rich_text","elements":[{"type":"text","text":"...","style":{"bold":true}}]}` for each header cell |
| 6 | Pure-numeric body cells render as raw_number; other body cells as raw_text | VERIFIED | `TestRichTable_RawNumber` passes; golden shows `{"type":"raw_number","text":"100"}` and `{"type":"raw_text","text":"pass"}` |
| 7 | Ragged rows are padded to the column count | VERIFIED | `TestRichTable_RaggedPad` passes |
| 8 | A table wider than 20 columns falls back to fencePipeTables monospace reflow | VERIFIED | `TestRichTable_ColsGuard` + `TestRichTable_GuardFallback` pass; `rich-table-guards.md` (21-col table) emits a `blockMarkdown` with monospace fence, no `"type":"table"` in output |
| 9 | A table longer than 100 data rows falls back to monospace reflow | VERIFIED | `TestRichTable_RowsGuard` passes; buildTableBlock returns ok=false |
| 10 | 12K cumulative markdown cap returns ok=false (Tier-2 fallback) | VERIFIED | `TestRichBlocks_12KCap` passes; `TestRunWith_FallbackChain` confirms runWith chain routes to RenderBlocks on cap hit |
| 11 | 50-block cap returns ok=false (Tier-2 fallback) | VERIFIED | `TestRichBlocks_50BlockCap` passes |
| 12 | recover() on panic returns ok=false (fail-soft) | VERIFIED | `TestRichBlocks_PanicRecover` passes; adversarial inputs confirmed safe |
| 13 | H1 inside code fence NOT promoted to header | VERIFIED | `TestRichBlocks_H1InCodeFence` passes; inCodeFence tracking gates H1 detection |
| 14 | KM_SLACK_AI_FOOTER=true appends a trailing AI-disclaimer context block | VERIFIED | `TestRunWith_AIFooter` passes (3 sub-tests: true/false/unset); `KM_SLACK_AI_FOOTER` is read sandbox-side via `os.Getenv` in runWith — NOT compiler-emitted |
| 15 | blocks-rich mode selected in runPost AND runReply | VERIFIED | `TestRunPost_BlocksRich` + `TestRunReply_BlocksRich` pass; `blocks-rich` appears in 4 locations in main.go: runPost flag help, runPost switch (line 126), runReply flag help (line 691), runReply switch (line 713) |
| 16 | Fallback chain renderRich→RenderBlocks→Mrkdwnify | VERIFIED | `TestRunWith_FallbackChain` passes; main.go lines 372–384 implement the three-tier chain |
| 17 | Corpus golden: prose+table+mixed → expected blocks JSON | VERIFIED | `TestRichCorpus` passes for all 3 fixtures (rich-prose-basic, rich-table-basic, rich-mixed); auto-discovery via filepath.Glob |
| 18 | blocks (Tier-2) default path unmodified — existing goldens green | VERIFIED | `TestBlocksCorpus` passes (7/7); `TestRichDefaultPathFrozen` adds explicit RICH-18 regression guard in blocks_rich_test.go |
| 19 | Output is valid Block Kit JSON (structural property test) | VERIFIED | `TestRichBlocks_StructuralValidity` passes; every block has non-empty "type" in {header, markdown, context, table} |
| 20 | Compiler golden byte-identity unaffected (no new NotifyEnv keys) | VERIFIED | `TestUserdataAdditionalVolumeOnly_GoldenByteIdentical` + `TestUserdataH1ByteIdentity` both pass; `KM_SLACK_AI_FOOTER` confirmed absent from `pkg/compiler/userdata.go` |

**Score:** 20/20 truths verified

---

### Required Artifacts

| Artifact | Lines | Status | Evidence |
|----------|-------|--------|----------|
| `pkg/slack/rich.go` | 289 | VERIFIED | Exports `RenderRich(input string, aiFooter bool) (blocksJSON, fallbackText string, ok bool)`; contains `renderRich`, `segmentInput`, `emitProseBlocks`, `chunkMarkdown`, `blockMarkdown` struct; 289 lines (>120 min) |
| `pkg/slack/table.go` | 222 | VERIFIED | Exports `buildTableBlock(lines []string) (blockTable, bool)`; contains `alignFromSep`, `classifyCell`, `makeBoldCell`, `blockTable`/`columnSetting`/`tableCell`/`richTextElement`/`rtStyle` structs; 222 lines (>90 min) |
| `pkg/slack/blocks_rich_test.go` | 682 | VERIFIED | Contains all RICH-01..RICH-13, RICH-17, RICH-18, RICH-19 test functions including `TestRichBlocks_ProseMarkdown`, `TestRichCorpus`, `TestRichDefaultPathFrozen` |
| `cmd/km-slack/main_rich_test.go` | 404 | VERIFIED | Contains `TestRunPost_BlocksRich`, `TestRunReply_BlocksRich`, `TestRunWith_BlocksRich`, `TestRunWith_FallbackChain`, `TestRunWith_AIFooter` (RICH-14/15/16) |
| `pkg/slack/testdata/rich-prose-basic.md` | exists | VERIFIED | H1 + two GFM paragraphs with bold, italic, native link |
| `pkg/slack/testdata/rich-prose-basic.expected-blocks.json` | exists | VERIFIED | Two-key format `{"blocks":[...],"text":"..."}` with header block + markdown block; `[label](url)` verbatim (not mrkdwn-converted) |
| `pkg/slack/testdata/rich-table-basic.md` | exists | VERIFIED | 3-col table with left/center/right alignment and numeric column |
| `pkg/slack/testdata/rich-table-basic.expected-blocks.json` | exists | VERIFIED | table block with bold rich_text header and raw_number/raw_text body cells |
| `pkg/slack/testdata/rich-table-guards.md` | exists | VERIFIED | 21-column guard fixture; used by TestRichTable_GuardFallback |
| `pkg/slack/testdata/rich-mixed.md` | exists | VERIFIED | H1 + prose with link + 3-col table + 🔧 tool line |
| `pkg/slack/testdata/rich-mixed.expected-blocks.json` | exists | VERIFIED | header + markdown + table + context blocks in order |
| `docs/slack-notifications.md § Phase 111` | present | VERIFIED | `grep "Phase 111"` returns line 2516; contains render-mode table, fallback chain, KM_SLACK_AI_FOOTER, surface caveats, deploy instructions |
| `skills/slack/SKILL.md` (blocks-rich row) | present | VERIFIED | Line 104 contains `blocks-rich` row in render-mode table with KM_SLACK_AI_FOOTER mention |
| `.claude-plugin/plugin.json` | 0.4.9 | VERIFIED | `"version": "0.4.9"` at line 4 |
| `.claude-plugin/marketplace.json` | 0.4.9 | VERIFIED | `"version": "0.4.9"` at line 11 |

### Key Link Verification

| From | To | Via | Status | Evidence |
|------|----|-----|--------|----------|
| `pkg/slack/rich.go renderRich` | `pkg/slack/mrkdwn.go isPipeLine` | segmentInput reuses isPipeLine for table detection | WIRED | `grep isPipeLine rich.go` → 3 calls in segmentInput |
| `pkg/slack/rich.go renderRich` | `pkg/slack/blocks.go stripForFallback/buildFallback/splitSection` | fallback text + chunk-splitting | WIRED | `grep "stripForFallback\|buildFallback\|splitSection" rich.go` → all three used |
| `pkg/slack/table.go buildTableBlock` | `pkg/slack/mrkdwn.go splitTableRow/isSeparatorRow` | row parsing + separator detection | WIRED | `grep "splitTableRow\|isSeparatorRow" table.go` → used at lines 94 and 97 |
| `pkg/slack/rich.go table segment branch` | `pkg/slack/mrkdwn.go fencePipeTables` | guard-hit fallback to monospace reflow | WIRED | `grep fencePipeTables rich.go` → used at line 95 for guard-hit path |
| `cmd/km-slack/main.go runWith blocks-rich case` | `pkg/slack.RenderRich` | `slack.RenderRich(string(body), aiFooter)` | WIRED | `grep "slack\.RenderRich" main.go` → line 372 |
| `cmd/km-slack/main.go runWith blocks-rich fallback` | `pkg/slack.RenderBlocks + slack.Mrkdwnify` | `ok=false → RenderBlocks → Mrkdwnify` chain | WIRED | `grep "slack\.RenderBlocks\|slack\.Mrkdwnify" main.go` → lines 378, 384 |
| `pkg/slack/blocks_rich_test.go TestRichCorpus` | `pkg/slack/testdata/rich-mixed.{md,expected-blocks.json}` | corpus walk asserts rendered == expected | WIRED | TestRichCorpus/rich-mixed passes in test run |
| `.claude-plugin/plugin.json version` | `.claude-plugin/marketplace.json version` | lockstep 0.4.9 bump | WIRED | Both files contain `"version": "0.4.9"` |

### Requirements Coverage

| Requirement | Plans | Description | Status | Test Evidence |
|-------------|-------|-------------|--------|---------------|
| RICH-01 | 111-01 | Prose → markdown block JSON | SATISFIED | TestRichBlocks_ProseMarkdown PASS |
| RICH-02 | 111-01 | Leading H1 → header block | SATISFIED | TestRichBlocks_H1Header PASS |
| RICH-03 | 111-01 | Tool lines → context block | SATISFIED | TestRichBlocks_ToolLine PASS |
| RICH-04 | 111-02 | GFM table → table block with alignment | SATISFIED | TestRichTable_Alignment PASS |
| RICH-05 | 111-02 | Header row → rich_text bold cells | SATISFIED | TestRichTable_HeaderBold PASS |
| RICH-06 | 111-02 | Pure-numeric body → raw_number | SATISFIED | TestRichTable_RawNumber PASS |
| RICH-07 | 111-02 | Ragged rows padded to column count | SATISFIED | TestRichTable_RaggedPad PASS |
| RICH-08 | 111-02 | Table >20 cols → monospace fallback | SATISFIED | TestRichTable_ColsGuard + GuardFallback PASS |
| RICH-09 | 111-02 | Table >100 rows → monospace fallback | SATISFIED | TestRichTable_RowsGuard PASS |
| RICH-10 | 111-01 | 12K cumulative cap → ok=false | SATISFIED | TestRichBlocks_12KCap PASS |
| RICH-11 | 111-01 | 50-block cap → ok=false | SATISFIED | TestRichBlocks_50BlockCap PASS |
| RICH-12 | 111-01 | recover() on panic → ok=false | SATISFIED | TestRichBlocks_PanicRecover PASS |
| RICH-13 | 111-01 | H1 in code fence NOT promoted | SATISFIED | TestRichBlocks_H1InCodeFence PASS |
| RICH-14 | 111-03 | KM_SLACK_AI_FOOTER=true → context block | SATISFIED | TestRunWith_AIFooter (3 sub-tests) PASS |
| RICH-15 | 111-03 | blocks-rich in runPost AND runReply | SATISFIED | TestRunPost_BlocksRich + TestRunReply_BlocksRich PASS |
| RICH-16 | 111-03 | Fallback chain renderRich→RenderBlocks→Mrkdwnify | SATISFIED | TestRunWith_FallbackChain PASS |
| RICH-17 | 111-04 | Corpus golden: mixed fixture | SATISFIED | TestRichCorpus/rich-mixed PASS |
| RICH-18 | 111-04 | Default blocks corpus unchanged | SATISFIED | TestBlocksCorpus (7/7) + TestRichDefaultPathFrozen PASS |
| RICH-19 | 111-01 | Output is valid Block Kit JSON | SATISFIED | TestRichBlocks_StructuralValidity PASS |
| RICH-20 | 111-04 | Compiler golden byte-identity unaffected | SATISFIED | TestUserdataAdditionalVolumeOnly_GoldenByteIdentical + TestUserdataH1ByteIdentity PASS; KM_SLACK_AI_FOOTER absent from pkg/compiler/userdata.go |

### Anti-Patterns Found

None. No TODO/FIXME/placeholder comments in any new files. All new functions have real implementations. The `// Plan 02: replace with buildTableBlock` seam comment from Plan 01 has been replaced in Plan 02 — confirmed by `grep "Plan 02" pkg/slack/rich.go` returning no output.

### Out-of-Scope Absence Confirmed

No Terraform, DynamoDB, SandboxProfile schema, or bridge Lambda changes were made in any phase 111 commit. All 16 changed files are scoped to: `pkg/slack/`, `cmd/km-slack/`, `docs/`, `skills/slack/`, `.claude-plugin/`. Phase 112 (default flip) is NOT done here — the default remains `${KM_SLACK_RENDER:-blocks}` in `pkg/compiler/userdata.go`.

---

### Full Suite Status

```
go test ./... -count=1 -timeout 600s
EXIT: 0   (44 packages, 0 failures)
```

All 44 packages green including the 3 packages that were previously red (resolved by Phase 110 per project memory `project_full_suite_known_red_packages`).

---

### Human Verification Required

These items are the Phase 111 soak/UAT items that gate the Phase 112 default flip. They are NOT code gaps — the implementation is complete. They are expected per `111-VALIDATION.md` § Manual-Only Verifications.

**1. Real Slack rendering (desktop + mobile)**

**Test:** Set `KM_SLACK_RENDER=blocks-rich` in `/etc/km/notify.env` on a test sandbox, run a Claude turn that produces prose with headers, a GFM table, and a `[label](url)` link. Observe the Slack message in a desktop client.

**Expected:** Headings appear with Slack's visual header block styling; prose paragraphs render GFM (bold/italic visible); `[label](url)` links are clickable anchors (NOT rendered as `<url|label>` mrkdwn); GFM tables appear as native Slack tables (not monospace grids); tool lines appear as grey context elements. Repeat on mobile.

**Why human:** Slack client rendering of `markdown` and `table` block types is not assertable from Go unit tests.

**2. 12K cumulative cap real-API behavior**

**Test:** Post a Claude turn exceeding 12K chars of prose with `KM_SLACK_RENDER=blocks-rich`.

**Expected:** Message is delivered (no dropped message). If cap is hit, the Tier-2 blocks fallback fires. Slack does not reject the payload.

**Why human:** Slack's actual rejection response on oversized payloads is not unit-assertable.

**3. table block rich_text bold header-cell schema acceptance**

**Test:** Post a message containing a GFM table with `KM_SLACK_RENDER=blocks-rich` to a real Slack workspace.

**Expected:** Header row renders bold. Slack does not return a schema validation error. If the `rich_text` bold-cell schema is rejected, downgrade header cells to `raw_text` before Phase 112.

**Why human:** The `rich_text` bold header-cell schema was flagged as LOW-confidence in RESEARCH (Open Question 1). Real API acceptance must be confirmed. The API's Block Kit validator is not exposed programmatically.

**4. Email notification text fallback**

**Test:** Receive a Slack email notification for a `blocks-rich` message with a GFM table.

**Expected:** The `text` field (plain fallback) is clean and readable — the table appears as pipe-separated plain text, prose is stripped of markup.

**Why human:** Email rendering is not assertable from Go.

---

## Summary

Phase 111 is code-complete. All 20 automated requirements RICH-01..RICH-20 are verified by passing unit tests, golden corpus tests, and regression guards. The full repo suite (44 packages) is green. The `blocks-rich` opt-in tier is correctly scoped: it is reachable ONLY via `KM_SLACK_RENDER=blocks-rich` or `--render blocks-rich`; the production default remains `${KM_SLACK_RENDER:-blocks}` (Tier-2) in `pkg/compiler/userdata.go`. No Terraform, DDB, or SandboxProfile changes were introduced.

The four human-verification items are the pre-existing Phase 111 soak requirements (per `111-VALIDATION.md` § Manual-Only Verifications) that gate the Phase 112 default flip. They represent real-Slack UAT, not code defects.

---

_Verified: 2026-06-14T16:21:24Z_
_Verifier: Claude (gsd-verifier)_
