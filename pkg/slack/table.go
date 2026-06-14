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
// v1 cell schema (refined after live Slack UAT, Phase 111):
//   - Header row → rich_text with bold style, wrapped in the mandatory
//     rich_text_section (a flat element list is rejected with invalid_blocks)
//   - Body cells → raw_text (always); numeric right-alignment comes from
//     column_settings, and raw_number is deferred (its value-field schema is
//     undocumented and rejected our guesses in UAT)
//   - No rich_text encoder for body cells (code spans/lists degrade to raw_text)
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

// richTextElement is a leaf text element inside a rich_text_section.
// In v1 only the header row uses this (bold style).
type richTextElement struct {
	Type  string   `json:"type"`            // always "text"
	Text  string   `json:"text"`
	Style *rtStyle `json:"style,omitempty"` // nil omits the key
}

// rtStyle holds rich_text element styling flags.
type rtStyle struct {
	Bold bool `json:"bold,omitempty"`
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
	// Rows 1..N (body) → raw_number or raw_text cells.
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

// classifyCell assigns the raw cell type for a body cell.
// v1: ALL body cells are raw_text. Numeric right-alignment is already provided by
// column_settings (derived from the GFM `--:` delimiter), and Slack's raw_number
// cell schema is undocumented — both the `number` and `text` value-field guesses
// were rejected with invalid_blocks in live UAT — so raw_number is deferred.
func classifyCell(text string) tableCell {
	return tableCell{Type: "raw_text", Text: strings.TrimSpace(text)}
}

// makeBoldCell creates a rich_text cell with a single bold text element, wrapped
// in the mandatory rich_text_section (a flat element list is rejected by Slack —
// see richTextSection). Used for the header row only.
func makeBoldCell(text string) tableCell {
	return tableCell{
		Type: "rich_text",
		Elements: []richTextSection{
			{
				Type: "rich_text_section",
				Elements: []richTextElement{
					{
						Type:  "text",
						Text:  strings.TrimSpace(text),
						Style: &rtStyle{Bold: true},
					},
				},
			},
		},
	}
}
