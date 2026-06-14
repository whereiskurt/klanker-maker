---
phase: 111-rich-slack-rendering-markdown-and-table-blocks-opt-in
plan: 02
subsystem: slack
tags: [slack, block-kit, table-block, rich-renderer, gfm, tdd]

# Dependency graph
requires:
  - phase: 111-01
    provides: renderRich skeleton + segmentInput + Plan-02 table seam stub
  - phase: 74-slack-mrkdwn-renderer
    provides: splitTableRow, isSeparatorRow, fencePipeTables from mrkdwn.go
provides:
  - "pkg/slack/table.go: buildTableBlock(lines []string) (blockTable, bool)"
  - "blockTable/columnSetting/tableCell/richTextElement/rtStyle structs"
  - "alignFromSep: GFM delimiter cell => left/center/right"
  - "classifyCell: reNumeric => raw_number; else raw_text (v1)"
  - "makeBoldCell: rich_text with bold style for header rows"
  - "guards: >20 cols or >100 data rows => ok=false"
  - "rich.go table segment branch: buildTableBlock or fencePipeTables fallback"
  - "RICH-04..09 test coverage + TestRichTable_GuardFallback"
  - "testdata/rich-table-basic.md + rich-table-basic.expected-blocks.json golden"
  - "testdata/rich-table-guards.md: 21-col guard fixture"
affects:
  - "111-03 (cmd wiring): RenderRich signature unchanged, ready for cmd/km-slack"
  - "111-04 (corpus+docs): TestRichCorpus now has table golden case"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "TDD RED/GREEN cycle: tests written first against undefined buildTableBlock, then implementation"
    - "splitTableRow/isSeparatorRow reused from mrkdwn.go — no duplication"
    - "fencePipeTables reused as the exact guard-hit fallback — detection parity guaranteed"
    - "v1 cell simplification: only header row uses rich_text/bold; body cells raw_number or raw_text"
    - "reNumeric regexp: ^\\s*[-+]?[\\d,]*\\.?\\d+\\s*$ matches integers, decimals, comma-separated"
    - "Ragged row padding: body rows padded to numCols with empty raw_text cells"
    - "alignFromSep: HasPrefix(':') && HasSuffix(':') => center; HasSuffix(':') only => right; else left"

key-files:
  created:
    - pkg/slack/table.go
    - pkg/slack/testdata/rich-table-basic.md
    - pkg/slack/testdata/rich-table-basic.expected-blocks.json
    - pkg/slack/testdata/rich-table-guards.md
  modified:
    - pkg/slack/rich.go
    - pkg/slack/blocks_rich_test.go

key-decisions:
  - "v1 body cell simplification: raw_number for pure-numeric, raw_text for everything else (no rich_text body encoder)"
  - "Guard fallback reuses fencePipeTables exactly — the same function renderRich would have called anyway"
  - "tableCell.Text omitempty: rich_text cells omit text field; raw_* cells omit elements field"
  - "IsWrapped: false for all v1 columns (per CONTEXT.md)"
  - "reNumeric matches signed integers, decimals, comma-thousand-separators (1,000 is a number)"

requirements-completed: [RICH-04, RICH-05, RICH-06, RICH-07, RICH-08, RICH-09]

# Metrics
duration: 4min
completed: 2026-06-14
---

# Phase 111 Plan 02: GFM Table → Slack Table Block Transformer Summary

**buildTableBlock transformer wired into renderRich: bold rich_text header, raw_number/raw_text body, left/center/right alignment, ragged-row padding, >20-col/>100-row guard fallback to fencePipeTables monospace**

## Performance

- **Duration:** ~4 min
- **Started:** 2026-06-14T15:33:10Z
- **Completed:** 2026-06-14T15:37:38Z
- **Tasks:** 2 (Task 1: buildTableBlock + tests; Task 2: wire + golden)
- **Files modified/created:** 6

## Accomplishments

- `pkg/slack/table.go` (148 lines): `buildTableBlock` transformer with `blockTable`/`columnSetting`/`tableCell`/`richTextElement`/`rtStyle` structs; `alignFromSep`, `classifyCell`, `makeBoldCell` helpers; `reNumeric` regexp. Guards fire at >20 cols or >100 data rows. Reuses `splitTableRow`/`isSeparatorRow` from `mrkdwn.go`.
- `pkg/slack/rich.go`: Plan-01 table stub replaced with `buildTableBlock` call. `ok=true` → append `blockTable`; `ok=false` (guard hit or malformed) → `fencePipeTables` monospace reflow emitted as `blockMarkdown`.
- `pkg/slack/blocks_rich_test.go`: 6 new unit tests (RICH-04..09) + `TestRichTable_GuardFallback`. `TestRichBlocks_StructuralValidity` updated to include "table" in valid block types.
- `testdata/rich-table-basic.md`: 3-col GFM table with left/center/right alignment, numeric + text body cells.
- `testdata/rich-table-basic.expected-blocks.json`: golden fixture — header block + table block (bold `rich_text` header, `raw_number`/`raw_text` body).
- `testdata/rich-table-guards.md`: 21-column table used by `TestRichTable_GuardFallback` to assert monospace fallback.

## Task Commits

1. **Task 1: buildTableBlock transformer + guards** - `aa803235` (feat)
2. **Task 2: wire table emission into renderRich + table corpus fixture** - `64f4d19f` (feat)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/slack/table.go` — buildTableBlock + supporting structs and helpers
- `/Users/khundeck/working/klankrmkr/pkg/slack/rich.go` — Plan-01 stub replaced with real table emission
- `/Users/khundeck/working/klankrmkr/pkg/slack/blocks_rich_test.go` — 6 table unit tests + guard fallback test
- `/Users/khundeck/working/klankrmkr/pkg/slack/testdata/rich-table-basic.md` — 3-col table corpus fixture
- `/Users/khundeck/working/klankrmkr/pkg/slack/testdata/rich-table-basic.expected-blocks.json` — expected golden output
- `/Users/khundeck/working/klankrmkr/pkg/slack/testdata/rich-table-guards.md` — 21-col guard fixture

## Decisions Made

- v1 body cell simplification: only header row uses `rich_text`/bold; body cells are `raw_number` (pure-numeric) or `raw_text` (everything else). No rich_text body encoder in v1 — code spans/inline markup degrade to `raw_text`. Explicitly allowed by CONTEXT.md.
- Guard fallback reuses `fencePipeTables` exactly: the same function the old Tier-2 path used. Detection parity is guaranteed because `segmentInput` also uses `isPipeLine` from `mrkdwn.go`.
- `tableCell.Text` has `omitempty`: `rich_text` header cells omit `text` field; `raw_*` cells omit `elements` field. This keeps JSON clean.
- `IsWrapped: false` for all v1 columns per CONTEXT.md and the RESEARCH pattern.
- `reNumeric` matches `^\s*[-+]?[\d,]*\.?\d+\s*$` — handles `42`, `3.14`, `1,000`, `+99`, `-0.5`.

## Deviations from Plan

None - plan executed exactly as written. The TDD RED/GREEN cycle was clean: tests compiled but failed (undefined: `buildTableBlock`), then all 6 passed immediately after writing `table.go`. The golden fixture was generated by running the implementation and verified manually before writing the JSON.

## Issues Encountered

None. The Plan-01 seam comment (`// Plan 02: replace with buildTableBlock`) made the wiring trivial — a clean 10-line replacement in `rich.go`. All existing Plan-01 tests remain green.

## Self-Check: PASSED

- `pkg/slack/table.go`: FOUND
- `pkg/slack/rich.go`: contains "buildTableBlock": FOUND
- `pkg/slack/testdata/rich-table-basic.md`: FOUND
- `pkg/slack/testdata/rich-table-basic.expected-blocks.json`: FOUND
- `pkg/slack/testdata/rich-table-guards.md`: FOUND
- Commit `aa803235`: FOUND
- Commit `64f4d19f`: FOUND
- `go test ./pkg/slack/... -run TestRich`: PASS (16/16)
- `go test ./pkg/slack/... -run TestBlocksCorpus`: PASS (7/7, Tier-2 unchanged)
- `go vet ./pkg/slack/...`: PASS

## Next Phase Readiness

- Plan 03 (`111-03`): `RenderRich(input, aiFooter)` signature is unchanged and ready for `cmd/km-slack` wiring. `"blocks-rich"` mode just needs adding to `runPost`/`runReply` switches.
- Plan 04 (`111-04`): `TestRichCorpus` now covers the table case; ready for `rich-mixed.md` (H1 + prose + table + tool line).

---
*Phase: 111-rich-slack-rendering-markdown-and-table-blocks-opt-in*
*Completed: 2026-06-14*
