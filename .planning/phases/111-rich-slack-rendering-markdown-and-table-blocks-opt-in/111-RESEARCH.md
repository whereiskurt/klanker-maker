# Phase 111: Rich Slack Rendering — markdown and table blocks (opt-in) - Research

**Researched:** 2026-06-14
**Domain:** pkg/slack renderer extension + cmd/km-slack sidecar + skills/slack skill update
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- New opt-in Tier-3 `renderRich` Slack renderer (`KM_SLACK_RENDER=blocks-rich`); default stays `blocks`.
- Prose segments → `markdown` blocks (verbatim GFM, 12K cumulative cap, chunking).
- Leading H1 → `header` block (promoted, not inside markdown block).
- Tool lines (🔧) → `context` blocks (unchanged from Tier-2).
- GFM pipe tables → dedicated `table` blocks via a new transformer. NOT pipe-tables inside a markdown block.
- Column alignment from delimiter row (`:--` left, `:-:` center, `--:` right).
- Header row → `rich_text` bold cells (no native header-row flag in table block v1).
- Body cells: `raw_number` (pure numeric), `rich_text` (inline formatting), `raw_text` (plain).
- Guards: ≤20 columns, ≤100 rows, pad ragged rows. Over-limit → `fencePipeTables` monospace fallback.
- One `table` block per GFM table (no multi-table-block pagination for over-limit tables).
- `KM_SLACK_AI_FOOTER` flag (default off/`"false"`), settable in `/etc/km/notify.env`.
- Fallback chain: `renderRich` → `ok=false` → Tier-2 `RenderBlocks` → Tier-1 `Mrkdwnify`. Existing tiers NOT deleted.
- `recover()` wrapper on `renderRich` (same fail-soft contract as existing tiers).
- Plain `text` fallback (push/email/search) unchanged: `stripForFallback`/`buildFallback` logic reused.
- Default-path (`blocks`) golden fixtures do NOT change in Phase 111.
- No `markdown_text` top-level param (mutually exclusive with `blocks` + `text`).
- No bare-URL auto-anchoring, no Sources footer. Native `[label](url)` anchors sufficient.
- Streaming (`chat.startStream`) deferred.
- Deploy: `km init --sidecars`. No TF/DDB/Lambda/SandboxProfile schema change.

### Claude's Discretion
- How `renderRich` is structured inside `pkg/slack/blocks.go` (new function vs new file).
- Whether the table transformer lives in `blocks.go` or a new `table.go` within `pkg/slack`.
- Exact approach for segmenting input at table boundaries before the line-walk loop.
- `rich_text` cell encoding: Slack `rich_text` block element format for bold cells vs flat bold-within-raw_text approach.
- Test file organization for `renderRich` + table transformer tests.
- Whether `KM_SLACK_AI_FOOTER` is read in `runWith` (cmd/km-slack) or in `renderRich` itself.

### Deferred Ideas (OUT OF SCOPE)
- Phase 112: flip default render to `blocks-rich`.
- Slack native streaming (`chat.startStream`/`appendStream`) + feedback buttons.
- Bare-URL auto-anchoring with derived anchor text; "Sources" footer.
- `card`/`carousel`/hero-image output.
- `raw_number` analytics tables for budget/usage reporting.
</user_constraints>

---

## Summary

Phase 111 is a pure `pkg/slack` + `cmd/km-slack` sidecar change. The work is self-contained: add a new `renderRich` function alongside the existing `renderBlocks`/`RenderBlocks` in `blocks.go`, wire it behind the `"blocks-rich"` render mode in `cmd/km-slack/main.go runWith()`, and add `KM_SLACK_AI_FOOTER` opt-in. The bridge (`PostMessageBlocks`) already forwards the pre-serialized blocks array verbatim via `json.RawMessage(blocksJSON)` — it does NOT deserialize into typed block structs, so `markdown` and `table` block types pass through without any bridge change. The default render mode stays `"blocks"`, protecting all existing golden fixtures byte-for-byte.

The core implementation challenge is the GFM-table → `table`-block transformer. The detection half is already implemented in `fencePipeTables` / `reflowTable` / `isPipeLine` / `isSeparatorRow` / `splitTableRow` in `mrkdwn.go`; Phase 111 reuses those detection and parsing primitives and replaces the monospace-reflow output with structured `table` block JSON. The `markdown` block prose pass is additive: instead of accumulated lines going through `Mrkdwnify` → mrkdwn section chunks, they go verbatim into `{"type":"markdown","text":<GFM>}` blocks, chunked at a 12,000-char cumulative cap.

**Primary recommendation:** Add `renderRich` in `pkg/slack/blocks.go` (or a new sibling `pkg/slack/rich.go` for cleaner separation) with a new `tableTransformer` function. Segment input at table boundaries using the existing `isPipeLine`/`isSeparatorRow` detection from `mrkdwn.go`. Wire `"blocks-rich"` into `runWith()` in `cmd/km-slack/main.go` alongside the existing `"blocks"` case. Add `KM_SLACK_AI_FOOTER` reading in `runWith()` and pass as a parameter to `renderRich`.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `encoding/json` | stdlib | Marshal block structs to JSON array; `json.RawMessage` for table cell payloads | Already used throughout `blocks.go` |
| `regexp` | stdlib | Table detection, cell type classification (pure numeric) | Already used in `blocks.go` / `mrkdwn.go` |
| `strings` | stdlib | Line splitting, segment accumulation, cumulative cap tracking | Already used throughout |
| `unicode/utf8` | stdlib | Rune-safe splitting for the 12K markdown-block chunk boundary | Already used in `splitSection` |

### No New External Dependencies
Phase 111 has zero new Go module dependencies. All needed primitives are in the Go standard library and the existing `pkg/slack` package.

---

## Architecture Patterns

### Recommended Project Structure
```
pkg/slack/
├── blocks.go          — Tier-2 RenderBlocks (unchanged); new renderRich + RenderRich public wrapper
├── rich.go            — (OPTION B) table transformer + markdown-block structs if blocks.go gets too large
├── mrkdwn.go          — isPipeLine, isSeparatorRow, splitTableRow, reflowTable reused as-is
├── payload.go         — SlackEnvelope.Blocks (unchanged); MaxBodyBytes/MaxRenderedBytes caps unchanged
├── testdata/
│   ├── blocks-*.md    — existing Tier-2 corpus (unchanged)
│   └── rich-*.md + rich-*.expected-blocks.json  — new Tier-3 golden corpus
cmd/km-slack/
└── main.go            — add "blocks-rich" case in runWith(); add KM_SLACK_AI_FOOTER reading
skills/slack/
└── SKILL.md           — add blocks-rich render mode row to the render mode table (§ Step 5)
docs/
└── slack-notifications.md  — § Phase 111 (render modes, KM_SLACK_AI_FOOTER, surface caveats)
```

### Pattern 1: Two-pass renderRich (segment-then-emit)

**What:** Split input into alternating prose segments and table segments at pipe-table boundaries, then emit each segment as the appropriate block type.

**When to use:** Cleaner than interleaving table detection inside the line-walk loop; lets the table transformer operate on a pre-extracted table run without tracking state across the main loop.

```go
// Source: pkg/slack/blocks.go (new function)

// RenderRich is the public fail-soft wrapper for the Tier-3 renderer.
func RenderRich(input string, aiFooter bool) (blocksJSON, fallbackText string, ok bool) {
    defer func() {
        if r := recover(); r != nil {
            blocksJSON, fallbackText, ok = "", "", false
        }
    }()
    return renderRich(input, aiFooter)
}

// renderRich is the inner Tier-3 implementation.
// Segments input into prose runs and pipe-table runs, then emits:
//   - prose       → markdown blocks (12K cumulative cap, chunked)
//   - pipe tables → table blocks (with fencePipeTables monospace fallback on overflow)
//   - leading H1  → header block (promoted for visual hierarchy)
//   - tool lines  → context blocks
//   - ai footer   → context block (when aiFooter is true)
func renderRich(input string, aiFooter bool) (blocksJSON, fallbackText string, ok bool) {
    segments := segmentInput(input) // returns []richSegment{kind: prose|table, lines: []string}
    var blocks []any
    var fallbackLines []string
    cumulativeMDChars := 0

    for _, seg := range segments {
        switch seg.kind {
        case segRichProse:
            newBlocks, newFallback := emitProseBlocks(seg.lines, &cumulativeMDChars)
            blocks = append(blocks, newBlocks...)
            fallbackLines = append(fallbackLines, newFallback...)
        case segRichTable:
            tb, ok := buildTableBlock(seg.lines)
            if ok {
                blocks = append(blocks, tb)
            } else {
                // Guard hit (>100 rows / >20 cols) — fall back to monospace reflow
                fallback := fencePipeTables(strings.Join(seg.lines, "\n"))
                // Emit as a markdown block (the fenced monospace is valid GFM)
                blocks = append(blocks, blockMarkdown{Type: "markdown", Text: fallback})
            }
            for _, l := range seg.lines {
                fallbackLines = append(fallbackLines, stripForFallback(l))
            }
        }
    }
    if aiFooter {
        blocks = append(blocks, blockContext{
            Type:     "context",
            Elements: []mrkdwnField{{Type: "mrkdwn", Text: "_Generated by AI — verify before sharing._"}},
        })
    }
    if len(blocks) > maxBlocks {
        return "", "", false
    }
    // ... marshal + buildFallback (same pattern as renderBlocks)
}
```

**Key insight:** The 12K cumulative cap is tracked across all markdown blocks in the payload (not per-block), matching the Slack API constraint.

### Pattern 2: Table segment detection (reuse mrkdwn.go primitives)

**What:** Walk lines collecting runs of ≥2 consecutive pipe-lines (same heuristic as `fencePipeTables`). This ensures detection is IDENTICAL to the existing Tier-2 fallback path.

```go
// Source: pkg/slack/blocks.go (new helper)

type richSegKind int

const (
    segRichProse richSegKind = iota
    segRichTable
)

type richSegment struct {
    kind  richSegKind
    lines []string
}

// segmentInput splits input into prose and table segments.
// Reuses isPipeLine from mrkdwn.go — detection is identical to fencePipeTables.
func segmentInput(input string) []richSegment {
    lines := strings.Split(input, "\n")
    var segs []richSegment
    i := 0
    for i < len(lines) {
        if isPipeLine(lines[i]) {
            // Collect the full table run (≥1 pipe line; the transformer enforces
            // the structural validity check for header+separator+body).
            start := i
            for i < len(lines) && isPipeLine(lines[i]) {
                i++
            }
            run := lines[start:i]
            if len(run) >= 2 {
                segs = append(segs, richSegment{kind: segRichTable, lines: run})
            } else {
                // Solo pipe line — treat as prose
                segs = append(segs, richSegment{kind: segRichProse, lines: run})
            }
        } else {
            // Collect prose lines until next table
            start := i
            for i < len(lines) && !isPipeLine(lines[i]) {
                i++
            }
            segs = append(segs, richSegment{kind: segRichProse, lines: lines[start:i]})
        }
    }
    return segs
}
```

### Pattern 3: Prose emission with H1/tool-line extraction + 12K chunking

**What:** Inside prose segments, scan for H1 lines (→ header block), tool lines (→ context block), and accumulate remaining lines into a `strings.Builder`. Flush when the builder would exceed the per-flush budget (12K cumulative).

**Seam:** H1 and tool-line handling is structurally identical to `renderBlocks`; the difference is that remaining prose goes into `blockMarkdown` (not `blockSection`) and the budget is 12,000 cumulative (not 3,000/section).

```go
// blockMarkdown is the new Tier-3 prose block.
type blockMarkdown struct {
    Type string `json:"type"` // "markdown"
    Text string `json:"text"`
}
```

**Cumulative cap enforcement:**
```go
const maxMarkdownCumulative = 12000

// chunkMarkdown splits prose text honoring the 12K cumulative budget.
// cumUsed is a pointer updated across calls so the cap spans all markdown blocks.
func chunkMarkdown(text string, cumUsed *int) []blockMarkdown {
    var out []blockMarkdown
    remaining := text
    for len(remaining) > 0 {
        budget := maxMarkdownCumulative - *cumUsed
        if budget <= 0 {
            // Cumulative cap exhausted — drop remaining (or return ok=false)
            break
        }
        chunk := remaining
        if len(chunk) > budget {
            // Split at paragraph boundary, sentence, or rune boundary (same splitSection logic)
            chunk = safeSplit(remaining, budget)
        }
        out = append(out, blockMarkdown{Type: "markdown", Text: chunk})
        *cumUsed += len(chunk)
        remaining = remaining[len(chunk):]
    }
    return out
}
```

### Pattern 4: Table block transformer

**What:** Convert a slice of pipe-table lines into a `blockTable` JSON struct using `splitTableRow` and `isSeparatorRow` from `mrkdwn.go`.

```go
// blockTable is the Tier-3 table block struct.
type blockTable struct {
    Type           string           `json:"type"` // "table"
    ColumnSettings []columnSetting  `json:"column_settings"`
    Rows           [][]tableCell    `json:"rows"`
}

type columnSetting struct {
    Align     string `json:"align"`      // "left"|"center"|"right"
    IsWrapped bool   `json:"is_wrapped"` // false for v1
}

type tableCell struct {
    Type string `json:"type"` // "raw_text"|"raw_number"|"rich_text"
    Text string `json:"text,omitempty"` // for raw_text/raw_number
    // rich_text uses Elements []richTextElement (bold for header row)
    Elements []richTextElement `json:"elements,omitempty"`
}

type richTextElement struct {
    Type   string   `json:"type"`             // "text"
    Text   string   `json:"text"`
    Style  *rtStyle `json:"style,omitempty"`
}

type rtStyle struct {
    Bold bool `json:"bold,omitempty"`
}

// buildTableBlock converts a pipe-table line run into a table block.
// Returns (block, true) on success, or (zero, false) when guards fire.
func buildTableBlock(lines []string) (blockTable, bool) {
    // Parse all rows
    type prow struct {
        cells []string
        isSep bool
    }
    var rows []prow
    for _, l := range lines {
        cells := splitTableRow(l) // reuse from mrkdwn.go
        rows = append(rows, prow{cells: cells, isSep: isSeparatorRow(cells)})
    }

    // Find separator row index
    sepIdx := -1
    for i, r := range rows {
        if r.isSep {
            sepIdx = i
            break
        }
    }
    // Guard: need header row (row 0) + separator (row 1 at minimum)
    if sepIdx < 1 {
        return blockTable{}, false
    }

    numCols := len(rows[0].cells)
    // Guard: ≤20 columns
    if numCols > 20 {
        return blockTable{}, false
    }
    // Guard: ≤100 data rows (total rows minus header minus separator)
    dataRows := len(rows) - sepIdx - 1
    if dataRows > 100 {
        return blockTable{}, false
    }

    // Build column_settings from separator row
    colSettings := make([]columnSetting, numCols)
    for i, c := range rows[sepIdx].cells {
        colSettings[i] = columnSetting{Align: alignFromSep(c)}
    }

    // Build rows: header row first (bold rich_text), then body rows
    var tableRows [][]tableCell
    // Header row
    headerCells := make([]tableCell, numCols)
    for i := 0; i < numCols; i++ {
        var text string
        if i < len(rows[0].cells) {
            text = rows[0].cells[i]
        }
        headerCells[i] = makeBoldCell(text)
    }
    tableRows = append(tableRows, headerCells)

    // Body rows (skip header and separator)
    for _, r := range rows[sepIdx+1:] {
        cells := make([]tableCell, numCols)
        for i := 0; i < numCols; i++ {
            var text string
            if i < len(r.cells) {
                text = r.cells[i]
            }
            cells[i] = classifyCell(text)
        }
        tableRows = append(tableRows, cells)
    }

    return blockTable{
        Type:           "table",
        ColumnSettings: colSettings,
        Rows:           tableRows,
    }, true
}

// alignFromSep reads GFM separator cell syntax to determine column alignment.
// ":--" → left (default), ":-:" → center, "--:" → right.
func alignFromSep(cell string) string {
    cell = strings.TrimSpace(cell)
    left := strings.HasPrefix(cell, ":")
    right := strings.HasSuffix(cell, ":")
    switch {
    case left && right:
        return "center"
    case right:
        return "right"
    default:
        return "left"
    }
}

// reNumeric matches a pure numeric cell (integer or decimal, optional sign/comma).
var reNumeric = regexp.MustCompile(`^\s*[-+]?[\d,]*\.?\d+\s*$`)

// classifyCell assigns the appropriate cell type.
func classifyCell(text string) tableCell {
    if reNumeric.MatchString(text) {
        return tableCell{Type: "raw_number", Text: strings.TrimSpace(text)}
    }
    // Inline formatting present (bold, link, code span)?
    if hasInlineMarkup(text) {
        // Degrade to raw_text — rich_text in body cells adds complexity for v1
        // (code spans and nested lists are unsupported in table cells anyway).
        return tableCell{Type: "raw_text", Text: strings.TrimSpace(text)}
    }
    return tableCell{Type: "raw_text", Text: strings.TrimSpace(text)}
}

// makeBoldCell creates a rich_text bold cell for the header row.
func makeBoldCell(text string) tableCell {
    t := true
    return tableCell{
        Type: "rich_text",
        Elements: []richTextElement{{
            Type:  "text",
            Text:  strings.TrimSpace(text),
            Style: &rtStyle{Bold: t},
        }},
    }
}
```

**Note on rich_text for body cells:** The CONTEXT.md says body cells with inline formatting → `rich_text`. In practice, for Phase 111 v1, a simpler implementation that degrades cells with any inline markup to `raw_text` is acceptable (the research doc explicitly says "code spans, nested lists, multi-line content inside a cell degrade to plain text"). The planner should recommend this simplification: only header cells get `rich_text`/bold; body cells get `raw_number` or `raw_text`. This avoids implementing a full `rich_text` block element encoder for v1.

### Pattern 5: renderRich wiring in cmd/km-slack/main.go

**What:** Add `"blocks-rich"` as a recognized render mode in `runPost` and `runReply`. Add `KM_SLACK_AI_FOOTER` env var reading. Call `RenderRich` in `runWith`.

**Extension point in `runPost` (lines 125-130):**
```go
// Current:
switch renderMode {
case "plain", "mrkdwn", "blocks":
    // valid
default:
    // ...
}
// Add "blocks-rich" to the valid set:
switch renderMode {
case "plain", "mrkdwn", "blocks", "blocks-rich":
    // valid
default:
    // ...
}
```

**Extension point in `runWith` (lines 365-381):**
```go
// Current "blocks" case:
case "blocks":
    bj, fallback, okBK := slack.RenderBlocks(string(body))
    if okBK { ... } else { ... }

// Add after:
case "blocks-rich":
    aiFooter := os.Getenv("KM_SLACK_AI_FOOTER") == "true"
    bj, fallback, okBK := slack.RenderRich(string(body), aiFooter)
    if okBK {
        rendered = fallback
        blocksJSON = bj
    } else {
        // Fallback to Tier-2
        bj2, fallback2, ok2 := slack.RenderBlocks(string(body))
        if ok2 {
            rendered = fallback2
            blocksJSON = bj2
        } else {
            rendered = slack.Mrkdwnify(string(body))
        }
    }
```

**Same pattern must be added in `runReply`'s mode validation switch (lines 691-697).**

### Anti-Patterns to Avoid

- **Deserializing blocks in the bridge:** The bridge already handles `markdown` and `table` block types by forwarding `env.Blocks` as `json.RawMessage`. Do NOT widen any typed bridge struct — this is confirmed safe.
- **Using `markdown_text` top-level param:** Mutually exclusive with `blocks` and `text` — breaks the fallback and header-block pattern.
- **Calling `Mrkdwnify` on prose before emitting as markdown block:** The markdown block takes verbatim GFM; running Mrkdwnify first would double-convert links (`[l](u)` → `<u|l>`) and break them.
- **Putting pipe tables inside a markdown block instead of using the table block:** The research established this is unreliable. Always parse and emit `table` blocks.
- **Modifying the default render path (`"blocks"`):** Must not touch `renderBlocks` or any golden fixture.
- **Tracking cumulative cap per-block instead of per-payload:** The 12K Slack limit is cumulative across all markdown blocks in one `chat.postMessage` call.
- **Emitting `rich_text` body cells with a full inline-markdown encoder in v1:** Too complex; downgrade to `raw_text` for cells with inline markup.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Pipe table detection | Custom regex scanner | `isPipeLine` + `isSeparatorRow` + `splitTableRow` from `mrkdwn.go` | Already tested; identical behavior means consistent fallback |
| Monospace table reflow (fallback) | Custom column-aligner | `reflowTable` + `fencePipeTables` from `mrkdwn.go` | Already handles escaped pipes (`\|`), ragged rows, width padding |
| Section-overflow chunking | Custom splitter | `splitSection` from `blocks.go` (same paragraph/sentence/rune-boundary logic) | Reuse for the 12K markdown-block chunking; `safeSplit` = same function |
| Fallback text building | Custom line joiner | `stripForFallback` + `buildFallback` from `blocks.go` | Idempotent, already tested, same squash-blank-lines behavior |
| Panic safety | Custom error return wrapper | `defer recover()` pattern from `RenderBlocks` | Consistent fail-soft contract expected by all callers |

---

## Common Pitfalls

### Pitfall 1: Bridge struct deserialization width
**What goes wrong:** Developer assumes the bridge re-validates block types and strips unknown `"type":"markdown"` or `"type":"table"` keys.
**Why it happens:** The bridge `handler.go` parses the outer `SlackEnvelope` struct — but `env.Blocks` is a `string` field (line 79 in `payload.go`). The bridge routes to `PostMessageBlocks` which passes it as `json.RawMessage(blocksJSON)` directly to `chat.postMessage`. No typed struct deserialization of block content occurs anywhere in the bridge.
**How to avoid:** Confirmed SAFE — no bridge change needed.
**Warning signs:** If `chat.postMessage` returns `invalid_blocks` error, it is a structural JSON issue, not bridge filtering.

### Pitfall 2: `markdown` block vs `markdown_text` param confusion
**What goes wrong:** Developer uses `chat.postMessage` with top-level `markdown_text` field instead of a `{"type":"markdown"}` block inside `blocks:[]`.
**Why it happens:** Both accept GFM markdown; easy to conflate from Slack docs.
**How to avoid:** Use the block (`{"type":"markdown","text":"..."}` inside `blocks:[...]`). The `markdown_text` top-level param returns `markdown_text_conflict` when `blocks` or `text` is also set — which we always set.

### Pitfall 3: Cumulative 12K cap miscounted
**What goes wrong:** Each markdown block gets 12K, so a 10-section document generates ~120K of content — exceeds the real Slack payload limits.
**Why it happens:** Misreading "12,000 chars per payload" as "12,000 chars per block".
**How to avoid:** Track `cumulativeMDChars` as a shared counter across all markdown blocks in the render call. Once `cumulativeMDChars >= 12000`, emit `ok=false` (fallback to Tier-2) or split with awareness of the remaining budget.

### Pitfall 4: Table detection threshold mismatch
**What goes wrong:** `renderRich` detects a table that `fencePipeTables` would not, or vice versa — the over-limit fallback goes to `fencePipeTables` but that function never detects the same run as a table.
**Why it happens:** Custom detection threshold (e.g., requiring ≥3 lines) differs from `fencePipeTables`'s ≥2 consecutive pipe lines.
**How to avoid:** `segmentInput` MUST use the identical `isPipeLine` check and the ≥2-line threshold as `fencePipeTables`. Both functions share `isPipeLine` from `mrkdwn.go`.

### Pitfall 5: H1 inside a code fence promoted to header block
**What goes wrong:** `# comment` inside a ``` code fence gets promoted to a Slack `header` block.
**Why it happens:** The line-walk in the prose segment doesn't track `inCodeFence` state.
**How to avoid:** The prose emission function must carry `inCodeFence` state through its line walk, identical to `renderBlocks`. H1 detection must be gated on `!inCodeFence`.

### Pitfall 6: `runReply` render mode validation not updated
**What goes wrong:** `km-slack reply --render blocks-rich` is rejected as "unknown" and falls back to `"plain"`.
**Why it happens:** `runReply`'s switch (lines 691-697 in `main.go`) is a COPY of `runPost`'s switch — both must be updated.
**How to avoid:** Update both `runPost` and `runReply` switches. Add a test for `runReply` with `blocks-rich`.

### Pitfall 7: Golden test breakage in `pkg/compiler/testdata`
**What goes wrong:** Adding `KM_SLACK_AI_FOOTER` or `KM_SLACK_RENDER` to the `NotifyEnv` compiler map breaks the `h1_byte_identity_golden.txt` and `userdata_additional_volume_only.golden.sh` golden tests.
**Why it happens:** Those goldens capture the complete rendered userdata; any change to `NotifyEnv` contents causes a byte-identity mismatch.
**How to avoid:** `KM_SLACK_AI_FOOTER` is NOT a compiler-emitted env var. It is set per-profile by the operator in `/etc/km/notify.env` or `/etc/profile.d/km-notify-env.sh` post-deploy. The compiler does NOT emit it. Similarly, `KM_SLACK_RENDER` is not emitted by the compiler (the userdata template uses `${KM_SLACK_RENDER:-blocks}` as a default). No golden test changes needed.

### Pitfall 8: Slack 50-block cap with many markdown blocks
**What goes wrong:** A long transcript with 45 prose paragraphs (all < 12K each) generates 45 markdown blocks + a few context blocks → exceeds the 50-block cap → `ok=false` on the 50-block check.
**Why it happens:** The markdown block's 12K cumulative cap is generous but the 50-block cap is tight.
**How to avoid:** After `segmentInput`, if the estimated block count (prose segment count + table count + H1 count + tool count) looks likely to exceed 50, coalesce adjacent small prose segments before emitting. Alternatively, accept fallback to Tier-2 for very fragmented inputs (the cap check in `renderRich` should return `ok=false` just like `renderBlocks`).

---

## Code Examples

### Bridge passthrough (confirmed safe — no change needed)

```go
// Source: pkg/slack/bridge/aws_adapters.go:387-412
// PostMessageBlocks sends the pre-serialized blocksJSON verbatim via json.RawMessage.
// The "blocks" field is typed as json.RawMessage in the payload map — Slack receives
// it exactly as serialized, with no typed struct round-trip.
func (s *SlackPosterAdapter) PostMessageBlocks(ctx context.Context, channel, subject, body, blocksJSON, threadTS string) (string, error) {
    payload := map[string]any{
        "channel":      channel,
        "text":         body,
        "blocks":       json.RawMessage(blocksJSON), // verbatim passthrough
        "unfurl_links": false,
        "unfurl_media": false,
        "mrkdwn":       true,
    }
    // ...
}
```

### Handler dispatch (confirmed safe — typed assertion is ONLY on BlockPoster interface)

```go
// Source: pkg/slack/bridge/handler.go:273-290
// The handler never touches block content; it only checks env.Blocks != "".
if env.Blocks != "" {
    if bp, okBP := h.Slack.(BlockPoster); okBP {
        ts, err = bp.PostMessageBlocks(ctx, env.Channel, env.Subject, env.Body, env.Blocks, env.ThreadTS)
    } else {
        ts, err = h.Slack.PostMessage(ctx, env.Channel, env.Subject, env.Body, env.ThreadTS)
    }
}
```

### Existing render mode dispatch pattern in runWith (extension point)

```go
// Source: cmd/km-slack/main.go:365-381
// Add "blocks-rich" as a new case BEFORE the default.
switch renderMode {
case "blocks":
    bj, fallback, okBK := slack.RenderBlocks(string(body))
    // ...
case "blocks-rich": // NEW
    aiFooter := os.Getenv("KM_SLACK_AI_FOOTER") == "true"
    bj, fallback, okBK := slack.RenderRich(string(body), aiFooter)
    // ... with Tier-2 + Tier-1 fallback chain
case "mrkdwn":
    rendered = slack.Mrkdwnify(string(body))
default:
    rendered = string(body)
}
```

### Table detection primitives (reuse from mrkdwn.go)

```go
// Source: pkg/slack/mrkdwn.go — these functions are REUSED without modification.

// isPipeLine — already exported-like within the package (lowercase, accessible to blocks.go).
func isPipeLine(line string) bool { /* reToolLine check + rePipeLine match */ }

// isSeparatorRow — detects `:?-+:?` pattern per cell.
func isSeparatorRow(cells []string) bool { /* reSepCell per cell */ }

// splitTableRow — trims outer pipes, handles \| escaping, trims cell whitespace.
func splitTableRow(line string) []string { /* \x00KMPIPE\x00 placeholder approach */ }
```

### Existing corpus test pattern (new rich tests follow same format)

```
// Source: pkg/slack/blocks_test.go:314-391 (TestBlocksCorpus)
// New Tier-3 tests follow the same two-key fixture format:
// testdata/rich-table-basic.md + testdata/rich-table-basic.expected-blocks.json
// The expected-blocks.json contains {"blocks": [...], "text": "..."}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Tier-1 mrkdwn text (Mrkdwnify) | Still used as final fallback | Phase 74 | Unchanged — never delete |
| Tier-2 Block Kit (sections/context/header/divider) | Still used as Tier-2 fallback; default render | Phase 74 | Default (`blocks`) stays Tier-2 until Phase 112 |
| Pipe tables → monospace fence in mrkdwn section | Pipe tables → `table` block in Tier-3 | Phase 111 (this phase) | Requires new transformer |
| GFM prose → mrkdwn reflow → section block | GFM prose → verbatim GFM → markdown block | Phase 111 (this phase) | Eliminates mrkdwn reflow on happy path |
| `markdown` block GA | Feb 2025 | Fully available in chat.postMessage today |
| `table` block GA | Aug 2025 | Fully available in chat.postMessage today |

**Deprecated/outdated:**
- `markdown_text` top-level chat.postMessage param: DO NOT USE — mutually exclusive with `blocks`+`text`.
- Pipe tables inside `markdown` block: unreliable across surfaces; use dedicated `table` block.

---

## Open Questions

1. **`rich_text` body cells vs `raw_text` simplification**
   - What we know: CONTEXT.md says body cells with inline formatting → `rich_text`. The Slack `rich_text` element (used in section blocks) has a specific JSON shape with `elements[]`.
   - What's unclear: Whether the `table` block's `rich_text` cell uses the same `rich_text` section-block element schema or a simplified version. The Slack docs show `rich_text` cells in table context as `{"type":"rich_text","elements":[...]}` with sub-elements.
   - Recommendation: For v1, downgrade all body cells to `raw_text` and only use `rich_text` (with bold style) for header-row cells. This is explicitly acceptable per the CONTEXT.md edge case note ("code spans / nested lists / multi-line content inside a cell degrade to `raw_text`"). Document the simplification.

2. **12K cumulative cap: fail-soft vs chunk?**
   - What we know: Cap is 12,000 chars cumulative across all markdown blocks. Very long transcripts (Claude research sessions) can easily exceed this.
   - What's unclear: Should `renderRich` return `ok=false` when the cumulative cap is hit (triggering Tier-2 fallback for the ENTIRE message), or should it silently truncate prose after 12K and continue emitting table blocks?
   - Recommendation: Return `ok=false` when cumulative cap is hit (cleaner fallback; the full Tier-2 rendering will handle the long content). This is consistent with the `maxBlocks` check in `renderBlocks`.

3. **`KM_SLACK_AI_FOOTER` in skill docs**
   - What we know: New env var, not emitted by compiler, operator-set per-profile.
   - What's unclear: Should the `klanker:slack` SKILL.md document how to set it, or just reference `docs/slack-notifications.md`?
   - Recommendation: Add a one-line mention in SKILL.md § Step 5 (render modes table) referencing the Phase 111 doc section.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` package (stdlib) — no external test dependencies |
| Config file | none (go test ./pkg/slack/... and go test ./cmd/km-slack/...) |
| Quick run command | `go test ./pkg/slack/... -run TestRich -count=1 -timeout 60s` |
| Full suite command | `go test ./pkg/slack/... ./cmd/km-slack/... -count=1 -timeout 120s` |

### Phase Requirements → Test Map

| Req | Behavior | Test Type | Automated Command | File Exists? |
|-----|----------|-----------|-------------------|-------------|
| RICH-01 | `RenderRich` with prose → `markdown` block JSON | unit | `go test ./pkg/slack/... -run TestRichBlocks_ProseMarkdown -count=1` | ❌ Wave 0 |
| RICH-02 | Leading H1 → `header` block (not inside markdown block) | unit | `go test ./pkg/slack/... -run TestRichBlocks_H1Header -count=1` | ❌ Wave 0 |
| RICH-03 | Tool lines → `context` block (unchanged from Tier-2) | unit | `go test ./pkg/slack/... -run TestRichBlocks_ToolLine -count=1` | ❌ Wave 0 |
| RICH-04 | GFM table → `table` block with correct column_settings alignment | unit | `go test ./pkg/slack/... -run TestRichTable_Alignment -count=1` | ❌ Wave 0 |
| RICH-05 | Table header row → `rich_text` bold cells | unit | `go test ./pkg/slack/... -run TestRichTable_HeaderBold -count=1` | ❌ Wave 0 |
| RICH-06 | Pure-numeric body cells → `raw_number` type | unit | `go test ./pkg/slack/... -run TestRichTable_RawNumber -count=1` | ❌ Wave 0 |
| RICH-07 | Ragged rows padded to column count | unit | `go test ./pkg/slack/... -run TestRichTable_RaggedPad -count=1` | ❌ Wave 0 |
| RICH-08 | Table >20 cols → monospace fallback (fencePipeTables) | unit | `go test ./pkg/slack/... -run TestRichTable_ColsGuard -count=1` | ❌ Wave 0 |
| RICH-09 | Table >100 rows → monospace fallback | unit | `go test ./pkg/slack/... -run TestRichTable_RowsGuard -count=1` | ❌ Wave 0 |
| RICH-10 | 12K cumulative markdown-block cap → `ok=false` + Tier-2 fallback | unit | `go test ./pkg/slack/... -run TestRichBlocks_12KCap -count=1` | ❌ Wave 0 |
| RICH-11 | 50-block cap → `ok=false` + Tier-2 fallback | unit | `go test ./pkg/slack/... -run TestRichBlocks_50BlockCap -count=1` | ❌ Wave 0 |
| RICH-12 | `recover()` on panic → `ok=false` (fail-soft) | unit | `go test ./pkg/slack/... -run TestRichBlocks_PanicRecover -count=1` | ❌ Wave 0 |
| RICH-13 | H1 inside code fence NOT promoted to header block | unit | `go test ./pkg/slack/... -run TestRichBlocks_H1InCodeFence -count=1` | ❌ Wave 0 |
| RICH-14 | `KM_SLACK_AI_FOOTER=true` → trailing context block appended | unit | `go test ./cmd/km-slack/... -run TestRunWith_AIFooter -count=1` | ❌ Wave 0 |
| RICH-15 | `blocks-rich` render mode selected in `runPost`; `runReply` | unit | `go test ./cmd/km-slack/... -run TestRunPost_BlocksRich -count=1` | ❌ Wave 0 |
| RICH-16 | Fallback chain: `renderRich ok=false` → `RenderBlocks` → `Mrkdwnify` | unit | `go test ./cmd/km-slack/... -run TestRunWith_FallbackChain -count=1` | ❌ Wave 0 |
| RICH-17 | Corpus golden: prose+table message → expected blocks JSON | golden | `go test ./pkg/slack/... -run TestRichCorpus -count=1` | ❌ Wave 0 |
| RICH-18 | `blocks` (Tier-2) default path unmodified — existing golden fixtures green | regression | `go test ./pkg/slack/... -run TestBlocksCorpus -count=1` | ✅ existing |
| RICH-19 | Output is valid Block Kit JSON (structural property test) | property | `go test ./pkg/slack/... -run TestRichBlocks_StructuralValidity -count=1` | ❌ Wave 0 |
| RICH-20 | Compiler golden byte-identity unaffected (no new NotifyEnv keys) | regression | `go test ./pkg/compiler/... -run TestUserdataAdditionalVolumeOnly_Golden -count=1` | ✅ existing |

### Sampling Rate
- **Per task commit:** `go test ./pkg/slack/... -run TestRich -count=1 -timeout 60s`
- **Per wave merge:** `go test ./pkg/slack/... ./cmd/km-slack/... -count=1 -timeout 120s`
- **Phase gate:** `go test ./... -count=1 -timeout 600s` full suite green before `/gsd:verify-work`

### Wave 0 Gaps (must create before implementation)

- [ ] `pkg/slack/testdata/rich-prose-basic.md` + `rich-prose-basic.expected-blocks.json` — simple prose → markdown block
- [ ] `pkg/slack/testdata/rich-table-basic.md` + `rich-table-basic.expected-blocks.json` — 3-col table with alignment
- [ ] `pkg/slack/testdata/rich-mixed.md` + `rich-mixed.expected-blocks.json` — H1 + prose + table + tool line
- [ ] `pkg/slack/testdata/rich-table-guards.md` — >20 col table (no expected-blocks.json; test asserts monospace fallback)
- [ ] `pkg/slack/rich_test.go` (or `pkg/slack/blocks_rich_test.go`) — all RICH-01..RICH-19 unit tests
- [ ] `cmd/km-slack/main_rich_test.go` — RICH-14..RICH-16 integration tests for runWith/runPost/runReply
- [ ] No new test infrastructure (no conftest, no new framework) — existing `go test` conventions apply

---

## Sources

### Primary (HIGH confidence)
- `pkg/slack/blocks.go` (lines 1-401) — Tier-2 renderer: RenderBlocks/renderBlocks, block structs, flushSection, splitSection, rebalanceFences, stripForFallback, buildFallback, maxBlocks=50, maxSectionChars=3000
- `pkg/slack/mrkdwn.go` (lines 406-568) — fencePipeTables, isPipeLine, isSeparatorRow, splitTableRow, reflowTable, rePipeLine, reSepCell
- `pkg/slack/payload.go` (lines 69-95) — SlackEnvelope.Blocks field (string type), MaxBodyBytes=40KB, MaxRenderedBytes=35KB
- `cmd/km-slack/main.go` (lines 103-157, 349-415) — runPost mode validation, runWith render dispatch, KM_SLACK_RENDER env reading
- `cmd/km-slack/main.go` (lines 661-697) — runReply mode validation (must also be updated)
- `pkg/slack/bridge/aws_adapters.go` (lines 387-412) — PostMessageBlocks uses `json.RawMessage(blocksJSON)` verbatim passthrough; confirmed no block-type deserialization
- `pkg/slack/bridge/handler.go` (lines 273-290) — dispatch: `env.Blocks != ""` → type-assert to BlockPoster; no block content inspection
- `pkg/compiler/userdata.go` (lines 5337-5434) — NotifyEnv population; confirms KM_SLACK_RENDER NOT emitted by compiler
- `.planning/phases/111-rich-slack-rendering-markdown-and-table-blocks-opt-in/111-CONTEXT.md` — all locked decisions
- `.planning/research/slack-markdown-block.md` — full Slack API surface research, table reliability finding, mechanism comparison

### Secondary (MEDIUM confidence)
- Slack docs: markdown block schema `{"type":"markdown","text":"..."}`, 12K cumulative cap, table block schema with `column_settings`/`rows`/cell types (verified in research doc)
- `pkg/slack/testdata/` — corpus fixture format (two-key `{"blocks":[],"text":""}` or single array)
- `skills/slack/SKILL.md` (lines 95-116) — current render mode documentation; must add `blocks-rich` row

### Tertiary (LOW confidence)
- `rich_text` element schema for table cells (bold header row) — documented in research spec but not independently verified against current Slack table block API docs; treat as needs-UAT
- Exact behavior of 12K cumulative cap enforcement at Slack's API level for payloads that exceed it — documented as "cumulative across all markdown blocks" in research spec; actual Slack error response not UAT'd yet

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all required Go libraries are already in use; no new dependencies
- Architecture: HIGH — extension points are exact file:line anchors from live source code
- Bridge passthrough: HIGH — code-confirmed: json.RawMessage verbatim, no block struct widening needed
- Golden test safety: HIGH — confirmed KM_SLACK_RENDER not in compiler NotifyEnv map; only userdata template shell default uses it
- Table transformer design: MEDIUM — primitives confirmed reusable; rich_text cell encoding for body cells intentionally simplified to raw_text
- Pitfalls: HIGH — all from direct code reading or Slack API research

**Research date:** 2026-06-14
**Valid until:** 2026-07-14 (Slack API surface is stable; table/markdown blocks are GA)
