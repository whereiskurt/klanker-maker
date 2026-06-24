// Package slack — table.go
// GFM pipe-table → Slack table block transformer. Phase 111 Plan 02.
//
// buildTableBlock converts a slice of GFM pipe-table lines into a Slack
// table block (type:"table"). Guards fire on >20 columns or >100 data rows,
// returning ok=false so the caller can fall back to the fencePipeTables
// monospace reflow.
//
// Reuses mrkdwn.go primitives (no duplication):
//   - splitTableRow  — parse pipe-separated cells, honour \| escaping
//   - isSeparatorRow — detect the |:---|---:| delimiter row
//
// Cell schema (refined after live Slack UAT, Phase 111; inline-markdown
// follow-up):
//   - Header row → rich_text bold cells, wrapped in the mandatory
//     rich_text_section (a flat element list is rejected with invalid_blocks).
//   - Body cells → rich_text when the cell contains inline markdown
//     (`code`, **bold**, [label](url)) so those render as Slack style objects
//     (style.code / style.bold) and link elements instead of LITERAL markup
//     characters; a plain cell (no markup) keeps the simpler raw_text encoding.
//     Numeric right-alignment still comes from column_settings; raw_number is
//     deferred (its value-field schema is undocumented and rejected our guesses
//     in UAT).
//   - parseInlineSpans is the shared inline tokenizer used by both rows.
package slack

import (
	"strings"
)

// ---------------------------------------------------------------------------
// Block structs for the Slack table block.
// These are Tier-3 only — not used by renderBlocks / the default render path.
// ---------------------------------------------------------------------------

// blockTable is the Slack table block (GA Aug 2025).
// JSON: {"type":"table","column_settings":[...],"rows":[[cell,…],…]}
type blockTable struct {
	Type           string          `json:"type"` // always "table"
	ColumnSettings []columnSetting `json:"column_settings"`
	Rows           [][]tableCell   `json:"rows"`
}

// columnSetting describes one column's display properties.
// Align is "left" | "center" | "right"; IsWrapped is false for v1.
type columnSetting struct {
	Align     string `json:"align"`
	IsWrapped bool   `json:"is_wrapped"`
}

// tableCell is one cell in the table block.
// Type is one of "raw_text" or "rich_text" (v1 — raw_number deferred; see classifyCell).
//   - raw_text: use Text field.
//   - rich_text: use Elements field (header bold cells only in v1).
type tableCell struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	Elements []richTextSection `json:"elements,omitempty"`
}

// richTextSection wraps the leaf text elements of a rich_text table cell.
// Slack's rich_text REQUIRES this section nesting — a flat
// elements:[{type:"text"}] is rejected with invalid_blocks (confirmed via live
// Slack UAT, Phase 111). The valid shape is:
//
//	{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text",...}]}]}
type richTextSection struct {
	Type     string            `json:"type"` // always "rich_text_section"
	Elements []richTextElement `json:"elements"`
}

// richTextElement is a leaf element inside a rich_text_section.
//   - Type "text": a styled run of text (Text + optional Style).
//   - Type "link": a hyperlink (URL + Text label + optional Style).
type richTextElement struct {
	Type  string   `json:"type"`            // "text" or "link"
	Text  string   `json:"text"`
	URL   string   `json:"url,omitempty"`   // set for type "link"
	Style *rtStyle `json:"style,omitempty"` // nil omits the key
}

// rtStyle holds rich_text element styling flags.
type rtStyle struct {
	Bold   bool `json:"bold,omitempty"`
	Italic bool `json:"italic,omitempty"`
	Code   bool `json:"code,omitempty"`
}

// ---------------------------------------------------------------------------
// buildTableBlock — main entry point
// ---------------------------------------------------------------------------

// buildTableBlock converts a pipe-table line run into a Slack table block.
//
// Returns (block, true) on success.
// Returns (zero, false) when:
//   - the table has no valid header+separator (sepIdx < 1)
//   - numCols > 20 (column guard)
//   - dataRows > 100 (row guard)
func buildTableBlock(lines []string) (blockTable, bool) {
	type prow struct {
		cells []string
		isSep bool
	}

	rows := make([]prow, 0, len(lines))
	for _, l := range lines {
		cells := splitTableRow(l)  // reuse from mrkdwn.go
		rows = append(rows, prow{
			cells: cells,
			isSep: isSeparatorRow(cells), // reuse from mrkdwn.go
		})
	}

	// Find the first separator row.
	sepIdx := -1
	for i, r := range rows {
		if r.isSep {
			sepIdx = i
			break
		}
	}
	// Need at least a header row (idx 0) and a separator (idx ≥ 1).
	if sepIdx < 1 {
		return blockTable{}, false
	}

	numCols := len(rows[0].cells)

	// Guard: ≤ 20 columns.
	if numCols > 20 {
		return blockTable{}, false
	}

	// Guard: ≤ 100 data rows (rows after the separator).
	dataRows := len(rows) - sepIdx - 1
	if dataRows > 100 {
		return blockTable{}, false
	}

	// Build column_settings from the separator row.
	colSettings := make([]columnSetting, numCols)
	sepCells := rows[sepIdx].cells
	for i := 0; i < numCols; i++ {
		var sep string
		if i < len(sepCells) {
			sep = sepCells[i]
		}
		colSettings[i] = columnSetting{Align: alignFromSep(sep), IsWrapped: false}
	}

	// Build the rows slice.
	// Row 0 (header) → bold rich_text cells.
	// Rows 1..N (body) → rich_text (cells with inline markdown) or raw_text (plain).
	tableRows := make([][]tableCell, 0, 1+dataRows)

	// Header row.
	headerCells := make([]tableCell, numCols)
	for i := 0; i < numCols; i++ {
		var text string
		if i < len(rows[0].cells) {
			text = rows[0].cells[i]
		}
		headerCells[i] = makeBoldCell(text)
	}
	tableRows = append(tableRows, headerCells)

	// Body rows (everything after the separator row).
	for _, r := range rows[sepIdx+1:] {
		bodyCells := make([]tableCell, numCols)
		for i := 0; i < numCols; i++ {
			var text string
			if i < len(r.cells) {
				text = r.cells[i]
			}
			bodyCells[i] = classifyCell(text)
		}
		tableRows = append(tableRows, bodyCells)
	}

	return blockTable{
		Type:           "table",
		ColumnSettings: colSettings,
		Rows:           tableRows,
	}, true
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// alignFromSep derives column alignment from a GFM separator cell.
//
//	":--" or ":-"  → "left"  (default)
//	":-:" or ":--:" → "center"
//	"--:"          → "right"
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

// classifyCell builds a body cell from its text.
//
// A cell containing inline markdown (`code`, **bold**, [label](url)) is encoded
// as a rich_text cell so Slack renders style objects / link elements instead of
// the LITERAL markup characters that raw_text would dump verbatim. A plain cell
// keeps the simpler raw_text encoding — numeric right-align still comes from
// column_settings, and the byte-output is unchanged for markup-free tables.
func classifyCell(text string) tableCell {
	els := parseInlineSpans(strings.TrimSpace(text))
	// Plain cell (single unstyled text element) → keep raw_text.
	if len(els) == 1 && els[0].Type == "text" && els[0].Style == nil {
		return tableCell{Type: "raw_text", Text: els[0].Text}
	}
	return richTextCell(els)
}

// makeBoldCell creates a rich_text header cell: the cell's inline markdown is
// parsed (so a header may carry a code span or link) and bold is OR-ed onto
// every element. Wrapped in the mandatory rich_text_section (a flat element list
// is rejected by Slack — see richTextSection).
func makeBoldCell(text string) tableCell {
	els := parseInlineSpans(strings.TrimSpace(text))
	for i := range els {
		if els[i].Style == nil {
			els[i].Style = &rtStyle{}
		}
		els[i].Style.Bold = true
	}
	return richTextCell(els)
}

// richTextCell wraps leaf elements in the mandatory rich_text_section nesting.
func richTextCell(els []richTextElement) tableCell {
	return tableCell{
		Type: "rich_text",
		Elements: []richTextSection{
			{Type: "rich_text_section", Elements: els},
		},
	}
}

// parseInlineSpans tokenizes a table cell's text into Slack rich_text leaf
// elements, converting inline markdown into style objects / link elements
// instead of leaving the literal markup characters that the raw_text path would
// render verbatim (a `code` span shown as "`code`", **bold** as "**bold**").
//
// Recognised spans, scanned left-to-right (first match wins):
//   - `code`        → text element with style.code = true (content verbatim — no
//     nested markdown, matching code-span semantics)
//   - **bold**      → inner content re-parsed recursively, style.bold OR-ed onto
//     each resulting element (so **`x`** → bold+code)
//   - [label](url)  → link element (URL + label)
//
// Everything else accumulates into a plain (unstyled) text element. The result
// always has at least one element ("" → a single empty text element) so callers
// can rely on els[0].
func parseInlineSpans(text string) []richTextElement {
	var out []richTextElement
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, richTextElement{Type: "text", Text: buf.String()})
			buf.Reset()
		}
	}

	i, n := 0, len(text)
	for i < n {
		// **bold** — find the closing ** after the opening pair.
		if strings.HasPrefix(text[i:], "**") {
			if rel := strings.Index(text[i+2:], "**"); rel >= 0 {
				flush()
				inner := text[i+2 : i+2+rel]
				for _, el := range parseInlineSpans(inner) {
					if el.Style == nil {
						el.Style = &rtStyle{}
					}
					el.Style.Bold = true
					out = append(out, el)
				}
				i += 2 + rel + 2
				continue
			}
		}
		// `code` — single-backtick span, content taken verbatim.
		if text[i] == '`' {
			if rel := strings.IndexByte(text[i+1:], '`'); rel >= 0 {
				flush()
				out = append(out, richTextElement{
					Type:  "text",
					Text:  text[i+1 : i+1+rel],
					Style: &rtStyle{Code: true},
				})
				i += 1 + rel + 1
				continue
			}
		}
		// [label](url) — only when it matches at the current position.
		if text[i] == '[' {
			if m := reLink.FindStringSubmatchIndex(text[i:]); m != nil && m[0] == 0 {
				flush()
				out = append(out, richTextElement{
					Type: "link",
					URL:  text[i+m[4] : i+m[5]],
					Text: text[i+m[2] : i+m[3]],
				})
				i += m[1]
				continue
			}
		}
		buf.WriteByte(text[i])
		i++
	}
	flush()
	if len(out) == 0 {
		out = append(out, richTextElement{Type: "text", Text: ""})
	}
	return out
}
