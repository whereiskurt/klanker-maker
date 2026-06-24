// Package slack — table_empty_cell_test.go
//
// Regression tests for the 2026-06-24 incident (install `sec`, sandbox
// learn-8a08070e): a GFM table with a BLANK leading header cell (the "row-label"
// idiom) produced a rich_text cell whose lone text element had an empty `text`
// string. Slack rejects an empty rich_text `text` element with invalid_blocks,
// dropping the entire chat.postMessage — the agent's reply was silently lost.
//
// A blank cell must render as a visually-empty (but schema-valid) cell.
package slack

import (
	"strings"
	"testing"
)

// walkCellTexts collects every leaf string a cell will serialise: the raw_text
// Text field, or each rich_text element's Text. Used to assert no empty leaf.
func walkCellTexts(c tableCell) []string {
	if c.Type == "raw_text" {
		return []string{c.Text}
	}
	var out []string
	for _, sec := range c.Elements {
		for _, el := range sec.Elements {
			// link elements carry a label in Text; text elements carry the run.
			out = append(out, el.Text)
		}
	}
	return out
}

// TestMakeBoldCell_EmptyHeader (Layer 1): a blank header cell must not produce a
// rich_text element with an empty text string.
func TestMakeBoldCell_EmptyHeader(t *testing.T) {
	for _, in := range []string{"", "   ", "\t"} {
		cell := makeBoldCell(in)
		for _, txt := range walkCellTexts(cell) {
			if txt == "" {
				t.Errorf("makeBoldCell(%q): emitted an empty text element (Slack rejects with invalid_blocks)", in)
			}
		}
	}
}

// TestClassifyCell_EmptyBody (Layer 1): a blank body cell must not produce an
// empty raw_text/rich_text text string.
func TestClassifyCell_EmptyBody(t *testing.T) {
	for _, in := range []string{"", "   ", "\t"} {
		cell := classifyCell(in)
		for _, txt := range walkCellTexts(cell) {
			if txt == "" {
				t.Errorf("classifyCell(%q): emitted an empty text element (Slack rejects with invalid_blocks)", in)
			}
		}
	}
}

// TestBuildTableBlock_BlankLeadingHeader (Layer 1): the exact incident shape —
// a table whose header row has a blank leading cell — must build with NO empty
// text leaf in any cell, header or body.
func TestBuildTableBlock_BlankLeadingHeader(t *testing.T) {
	lines := []string{
		"| | Customer dev-center | SHED admin |",
		"|---|---|---|",
		"| Controller | yes | no |",
		"| | trailing-blank-first | x |",
	}
	tbl, ok := buildTableBlock(lines)
	if !ok {
		t.Fatal("buildTableBlock returned ok=false for a valid 3-column table")
	}
	for ri, row := range tbl.Rows {
		for ci, cell := range row {
			for _, txt := range walkCellTexts(cell) {
				if txt == "" {
					t.Errorf("row %d col %d: empty text leaf (invalid_blocks); cell=%+v", ri, ci, cell)
				}
			}
		}
	}
}

// TestMakeBoldCell_NonEmptyUnchanged guards against the fix corrupting a normal
// header cell: a non-blank header still renders its trimmed bold text.
func TestMakeBoldCell_NonEmptyUnchanged(t *testing.T) {
	cell := makeBoldCell("  Name  ")
	texts := walkCellTexts(cell)
	if len(texts) != 1 || strings.TrimSpace(texts[0]) != "Name" {
		t.Errorf("makeBoldCell(\"  Name  \"): got texts %q, want [\"Name\"]", texts)
	}
}
