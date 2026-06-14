---
phase: 111-rich-slack-rendering-markdown-and-table-blocks-opt-in
plan: 01
subsystem: slack
tags: [slack, block-kit, markdown-block, rich-renderer, gfm]

# Dependency graph
requires:
  - phase: 74-slack-block-kit-renderer
    provides: RenderBlocks/renderBlocks, splitSection, stripForFallback, buildFallback, blockHeader/blockContext/blockSection/blockDivider structs
  - phase: 74-slack-mrkdwn-renderer
    provides: isPipeLine, fencePipeTables, splitTableRow, isSeparatorRow from mrkdwn.go
provides:
  - "pkg/slack.RenderRich: public fail-soft Tier-3 Slack renderer (blocks-rich mode)"
  - "renderRich inner function: prose segmentation + markdown/header/context block emission"
  - "segmentInput: splits input at pipe-table boundaries using isPipeLine (same threshold as fencePipeTables)"
  - "emitProseBlocks: H1→header, 🔧→context, verbatim-GFM accumulation with inCodeFence tracking"
  - "chunkMarkdown: 12K cumulative-budget chunker using splitSection"
  - "blockMarkdown struct: {type:'markdown', text:<GFM>} Slack GA Feb 2025 block"
  - "RICH-01..03,10..13,19 test coverage; TestRichCorpus prose golden"
  - "testdata/rich-prose-basic.md + rich-prose-basic.expected-blocks.json golden fixture"
affects:
  - "111-02 (table transformer): buildTableBlock replaces the Plan-02 seam stub"
  - "111-03 (cmd wiring): RenderRich wired into cmd/km-slack runWith / runReply"
  - "111-04 (corpus+docs): TestRichCorpus extended to table+mixed fixtures"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Tier-3 renderer adds blockMarkdown alongside existing Tier-2 structs — additive, no Tier-2 change"
    - "segmentInput uses identical isPipeLine threshold as fencePipeTables for detection parity"
    - "emitProseBlocks tracks inCodeFence state: H1/tool-line detection gated on !inCodeFence"
    - "chunkMarkdown: CUMULATIVE 12K cap across all markdown blocks, not per-block"
    - "RenderRich public wrapper carries defer recover() identical to RenderBlocks"
    - "Verbatim GFM in markdown blocks: do NOT run Mrkdwnify (would double-convert [l](u) links)"

key-files:
  created:
    - pkg/slack/rich.go
    - pkg/slack/blocks_rich_test.go
    - pkg/slack/testdata/rich-prose-basic.md
    - pkg/slack/testdata/rich-prose-basic.expected-blocks.json
  modified: []

key-decisions:
  - "Verbatim GFM in markdown blocks: skip Mrkdwnify entirely — markdown block accepts native GFM and Mrkdwnify would break [l](u) links"
  - "12K cumulative cap → ok=false (fail-soft to Tier-2), not silent truncation — consistent with maxBlocks check in renderBlocks"
  - "Table segments stubbed as fencePipeTables-wrapped markdown blocks with a clear Plan-02 seam comment"
  - "Two-key golden fixture format {blocks:[...], text:'...'} matches existing blocks-plain-text-fallback.expected-blocks.json convention"
  - "richSegment/richSegKind types scoped to rich.go; do not widen mrkdwn.go or blocks.go"

patterns-established:
  - "Rich corpus tests (TestRichCorpus) mirror TestBlocksCorpus pattern: glob rich-*.md, skip fixtures without expected JSON"
  - "RICH-12 PanicRecover: verify adversarial inputs via defer/recover in test, plus direct empty-input ok=false assertion"

requirements-completed: [RICH-01, RICH-02, RICH-03, RICH-10, RICH-11, RICH-12, RICH-13, RICH-19]

# Metrics
duration: 4min
completed: 2026-06-14
---

# Phase 111 Plan 01: Rich Slack Rendering — Tier-3 Prose Skeleton Summary

**RenderRich Tier-3 prose skeleton: verbatim-GFM markdown blocks, H1→header promotion, 🔧→context, 12K cumulative cap, 50-block cap, fail-soft recover — Plan-02 table seam stubbed**

## Performance

- **Duration:** 4 min
- **Started:** 2026-06-14T15:26:28Z
- **Completed:** 2026-06-14T15:30:40Z
- **Tasks:** 2 (Task 0 + Task 1)
- **Files modified:** 4

## Accomplishments

- `pkg/slack/rich.go` (283 lines): `RenderRich` public wrapper with `defer recover()`, `renderRich` inner, `segmentInput` (reuses `isPipeLine` from mrkdwn.go), `emitProseBlocks` (inCodeFence state, H1→header, 🔧→context, verbatim-GFM accumulation), `chunkMarkdown` (12K cumulative budget), `blockMarkdown` struct.
- `blocks_rich_test.go`: 8 unit tests covering RICH-01/02/03/10/11/12/13/19 plus `TestRichCorpus` prose case — all pass.
- Golden fixtures: `rich-prose-basic.md` (H1 + two GFM paragraphs with bold, italic, link) and matching `rich-prose-basic.expected-blocks.json` (header block + markdown block with verbatim `[label](url)` link).
- Default-path `TestBlocksCorpus` and compiler golden tests remain green (RICH-18/RICH-20 regression guards verified).

## Task Commits

1. **Task 0: Wave-0 prose golden fixture + RenderRich signature stub** - `1f164106` (feat)
2. **Task 1: renderRich prose pass — segmentInput + markdown/header/context blocks + caps** - `f774c169` (feat)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/slack/rich.go` — Tier-3 RenderRich public wrapper + renderRich inner + segmentInput + emitProseBlocks + chunkMarkdown + blockMarkdown/richSegment types
- `/Users/khundeck/working/klankrmkr/pkg/slack/blocks_rich_test.go` — RICH-01..03, RICH-10..13, RICH-19 unit tests + TestRichCorpus prose golden case
- `/Users/khundeck/working/klankrmkr/pkg/slack/testdata/rich-prose-basic.md` — prose golden input fixture
- `/Users/khundeck/working/klankrmkr/pkg/slack/testdata/rich-prose-basic.expected-blocks.json` — two-key golden: header block + markdown block + fallback text

## Decisions Made

- Verbatim GFM in markdown blocks: `Mrkdwnify` intentionally skipped — the `markdown` block accepts native GFM and Mrkdwnify would double-convert `[l](u)` links to `<u|l>` Slack syntax.
- 12K cumulative cap returns `ok=false` (fail-soft to Tier-2), not silent truncation. This matches the `maxBlocks` cap precedent in `renderBlocks`.
- Table segments in Plan-01 are stubbed with `fencePipeTables` + `blockMarkdown` and a clear `// Plan 02: replace with buildTableBlock` seam comment.
- Golden fixture uses two-key format `{"blocks":[...],"text":"..."}` matching `blocks-plain-text-fallback.expected-blocks.json` convention.

## Deviations from Plan

None - plan executed exactly as written. The implementation matched the RESEARCH patterns 1-3 precisely. No auto-fixes required.

## Issues Encountered

None. The RED/GREEN TDD cycle passed on first run: all 9 test functions (8 unit + 1 corpus) were green immediately after writing the full implementation.

## Self-Check: PASSED

- `pkg/slack/rich.go`: FOUND
- `pkg/slack/blocks_rich_test.go`: FOUND
- `pkg/slack/testdata/rich-prose-basic.md`: FOUND
- `pkg/slack/testdata/rich-prose-basic.expected-blocks.json`: FOUND
- Commit `1f164106`: FOUND
- Commit `f774c169`: FOUND
- `go test ./pkg/slack/... -run TestRich`: PASS (9/9)
- `go test ./pkg/slack/... -run TestBlocksCorpus`: PASS (7/7, Tier-2 unchanged)
- `go build ./pkg/slack/...`: PASS

## Next Phase Readiness

- Plan 02 (`111-02`): `buildTableBlock` replaces the `// Plan 02: replace with buildTableBlock` stub in `renderRich`. The `segmentInput` / `richSegment` seam is stable and ready to consume.
- Plan 03 (`111-03`): `RenderRich(input, aiFooter)` signature is finalized and ready for `cmd/km-slack` wiring.
- Plan 04 (`111-04`): `TestRichCorpus` is ready for extension to `rich-table-basic.md` / `rich-mixed.md` fixtures.

---
*Phase: 111-rich-slack-rendering-markdown-and-table-blocks-opt-in*
*Completed: 2026-06-14*
