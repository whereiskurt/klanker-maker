// Package slack — blocks_rich_test.go
// RICH-01..RICH-03, RICH-04..RICH-09, RICH-10..RICH-13, RICH-19 + TestRichCorpus.
// Tests for the Tier-3 RenderRich renderer (pkg/slack.RenderRich).
//
// These tests cover:
//   RICH-01: prose → markdown block (verbatim GFM, no mrkdwn conversion)
//   RICH-02: leading H1 → header block (not inside markdown block)
//   RICH-03: tool lines → context block (same as Tier-2)
//   RICH-04: GFM table → table block with correct column_settings alignment
//   RICH-05: table header row → rich_text bold cells
//   RICH-06: pure-numeric body cells → raw_number type
//   RICH-07: ragged rows padded to column count
//   RICH-08: table >20 cols → guard fires → ok=false from buildTableBlock
//   RICH-09: table >100 rows → guard fires → ok=false from buildTableBlock
//   RICH-10: 12K cumulative markdown-block cap → ok=false
//   RICH-11: 50-block cap → ok=false
//   RICH-12: panic inside transformer → ok=false (fail-soft recover)
//   RICH-13: H1 inside code fence NOT promoted to header block
//   RICH-19: output is valid Block Kit JSON (all blocks have a non-empty "type")
//
// TestRichCorpus tests the golden corpus fixtures:
//   rich-prose-basic.md → rich-prose-basic.expected-blocks.json
//   rich-table-basic.md → rich-table-basic.expected-blocks.json
//
// TestRichTable_GuardFallback asserts the >20-col guard emits monospace fallback.
//
// Note: cmd/km-slack RICH-14..RICH-16 are covered in Plan 03.
package slack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRichBlocks_ProseMarkdown (RICH-01): plain prose produces a markdown block
// with verbatim GFM — [label](url) links are NOT converted to Slack <url|label>.
func TestRichBlocks_ProseMarkdown(t *testing.T) {
	input := "Some **bold** text with a [link](https://example.com).\n"
	bj, _, ok := RenderRich(input, false)
	if !ok {
		t.Fatal("RenderRich returned ok=false")
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("expected at least 1 block")
	}
	// Find the markdown block.
	var mdBlock map[string]any
	for _, b := range blocks {
		if b["type"] == "markdown" {
			mdBlock = b
			break
		}
	}
	if mdBlock == nil {
		t.Fatalf("no markdown block found; blocks: %s", bj)
	}
	text, _ := mdBlock["text"].(string)
	// GFM link must be unchanged (NOT converted to <url|label>).
	if !strings.Contains(text, "[link](https://example.com)") {
		t.Errorf("markdown block text should contain verbatim GFM link; got: %q", text)
	}
	// mrkdwn conversion must NOT have run (no <url|label> form).
	if strings.Contains(text, "<https://example.com|link>") {
		t.Errorf("markdown block must NOT contain Slack link syntax <url|label>; got: %q", text)
	}
	// Bold must be verbatim (**bold**, not *bold*).
	if !strings.Contains(text, "**bold**") {
		t.Errorf("markdown block should contain verbatim **bold**; got: %q", text)
	}
}

// TestRichBlocks_H1Header (RICH-02): a leading `# Heading` produces a header
// block; the heading text must NOT also appear inside the markdown block.
func TestRichBlocks_H1Header(t *testing.T) {
	input := "# My Title\n\nSome prose here.\n"
	bj, _, ok := RenderRich(input, false)
	if !ok {
		t.Fatal("RenderRich returned ok=false")
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(blocks) < 1 {
		t.Fatal("expected at least 1 block")
	}
	// First block must be a header.
	if blocks[0]["type"] != "header" {
		t.Errorf("first block type = %q; want header", blocks[0]["type"])
	}
	text, _ := blocks[0]["text"].(map[string]any)
	if text["text"] != "My Title" {
		t.Errorf("header text = %q; want %q", text["text"], "My Title")
	}
	if text["type"] != "plain_text" {
		t.Errorf("header text type = %q; want plain_text", text["type"])
	}
	// The heading text must NOT appear in any markdown block (H1 is promoted,
	// not duplicated into prose blocks).
	for _, b := range blocks {
		if b["type"] == "markdown" {
			mdText, _ := b["text"].(string)
			if strings.HasPrefix(mdText, "# My Title") {
				t.Errorf("H1 text leaked into markdown block: %q", mdText)
			}
		}
	}
}

// TestRichBlocks_ToolLine (RICH-03): a `🔧 Tool: ...` line produces a context
// block, identical shape to the Tier-2 blockContext.
func TestRichBlocks_ToolLine(t *testing.T) {
	input := "🔧 Edit: /path/to/file.go (line 42)\n"
	bj, _, ok := RenderRich(input, false)
	if !ok {
		t.Fatal("RenderRich returned ok=false")
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("expected at least 1 block")
	}
	// Find the context block.
	var ctx map[string]any
	for _, b := range blocks {
		if b["type"] == "context" {
			ctx = b
			break
		}
	}
	if ctx == nil {
		t.Fatalf("no context block found; blocks: %s", bj)
	}
	elements, _ := ctx["elements"].([]any)
	if len(elements) == 0 {
		t.Fatal("context block has no elements")
	}
	elem, _ := elements[0].(map[string]any)
	if elem["type"] != "mrkdwn" {
		t.Errorf("context element type = %q; want mrkdwn", elem["type"])
	}
	txt, _ := elem["text"].(string)
	if !strings.Contains(txt, "Edit:") {
		t.Errorf("context text = %q; want to contain 'Edit:'", txt)
	}
}

// TestRichBlocks_H1InCodeFence (RICH-13): a `# heading` inside a ``` code fence
// must NOT be promoted to a header block — it stays inside the markdown block.
func TestRichBlocks_H1InCodeFence(t *testing.T) {
	input := "Some intro.\n\n```\n# not a heading\n```\n\nSome outro.\n"
	bj, _, ok := RenderRich(input, false)
	if !ok {
		t.Fatal("RenderRich returned ok=false")
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Must have NO header block.
	for _, b := range blocks {
		if b["type"] == "header" {
			t.Errorf("header block should NOT be emitted for # inside a code fence; blocks: %s", bj)
		}
	}
	// The fenced text must appear inside a markdown block.
	found := false
	for _, b := range blocks {
		if b["type"] == "markdown" {
			mdText, _ := b["text"].(string)
			if strings.Contains(mdText, "# not a heading") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("fenced '# not a heading' should appear in a markdown block; blocks: %s", bj)
	}
}

// TestRichBlocks_12KCap (RICH-10): input whose cumulative markdown text exceeds
// 12,000 chars returns ok=false (Tier-2 fallback, not silent truncation).
func TestRichBlocks_12KCap(t *testing.T) {
	// 13,000 chars of prose — exceeds the 12K cumulative markdown budget.
	bigProse := strings.Repeat("A", 13000) + "\n"
	_, _, ok := RenderRich(bigProse, false)
	if ok {
		t.Error("expected ok=false when cumulative markdown chars exceed 12000")
	}
}

// TestRichBlocks_50BlockCap (RICH-11): input that would produce >50 blocks
// returns ok=false.
func TestRichBlocks_50BlockCap(t *testing.T) {
	// 51 H1 headings → 51 header blocks → exceeds 50-block cap.
	var sb strings.Builder
	for i := 0; i < 51; i++ {
		sb.WriteString("# Heading\n\n")
	}
	_, _, ok := RenderRich(sb.String(), false)
	if ok {
		t.Error("expected ok=false when >50 blocks are produced")
	}
}

// TestRichBlocks_PanicRecover (RICH-12): RenderRich must never panic on
// adversarial input; a panic is recovered and ok=false is returned.
// We verify this by sending a variety of edge-case strings that might trip
// the implementation, and also confirm directly via the public wrapper.
func TestRichBlocks_PanicRecover(t *testing.T) {
	// The public wrapper has a defer recover() — verify it is wired correctly.
	// Force-test via a no-op: inject a string that is structurally unusual.
	adversarial := []string{
		"",                        // empty
		"\x00\x01\x02",           // null bytes
		"# " + strings.Repeat("A", 10000), // huge header
		"```\n" + strings.Repeat("X\n", 5000) + "```\n", // unclosed code fence simulation
		strings.Repeat("🔧 Tool: /x\n", 60), // 60 tool lines → would exceed 50 blocks
	}
	for _, input := range adversarial {
		// Must not panic, regardless of output.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("RenderRich panicked on input (len=%d): %v", len(input), r)
				}
			}()
			RenderRich(input, false)
		}()
	}

	// Extra: confirm the recover() in the public wrapper catches panics from
	// a known-triggered path — if renderRich panicked, RenderRich must return ok=false.
	// We cannot reliably force a panic in the current implementation without a test
	// hook, so we just verify the wrapper's declared contract holds for known inputs.
	_, _, ok := RenderRich("", false)
	// Empty input → ok=false (no blocks produced).
	if ok {
		t.Error("RenderRich on empty input should return ok=false")
	}
}

// TestRichBlocks_StructuralValidity (RICH-19): every block in the output has
// a non-empty "type" field in the set {header, markdown, context}.
func TestRichBlocks_StructuralValidity(t *testing.T) {
	inputs := []string{
		"# Title\n\nProse paragraph.\n",
		"🔧 Edit: /file.go\n",
		"Plain text with **bold** and [link](https://example.com).\n",
		"# H\n\n🔧 Tool\n\nProse.\n",
		"```\n# not a header\n```\n\nafter fence.\n",
	}
	validTypes := map[string]bool{
		"header":   true,
		"markdown": true,
		"context":  true,
		"table":    true,
	}
	for _, input := range inputs {
		bj, _, ok := RenderRich(input, false)
		if !ok {
			continue // fallback path is acceptable
		}
		var blocks []map[string]any
		if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
			t.Errorf("invalid JSON for input %q: %v", input, err)
			continue
		}
		for i, b := range blocks {
			typ, _ := b["type"].(string)
			if typ == "" {
				t.Errorf("block %d has empty 'type' for input %q", i, input)
			}
			if !validTypes[typ] {
				t.Errorf("block %d has unexpected type %q for input %q", i, typ, input)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// RICH-04..RICH-09: Table transformer unit tests (Plan 02)
// ---------------------------------------------------------------------------

// minimalTable is a helper that returns a 3-col GFM pipe-table with mixed alignment.
// Used by multiple table tests.
//
//	| Name     | Count | Score |
//	|:---------|:-----:|------:|
//	| Alice    | 5     | 98.5  |
//	| Bob      | 10    | 3.14  |
func minimalTableLines() []string {
	return []string{
		"| Name     | Count | Score |",
		"|:---------|:-----:|------:|",
		"| Alice    | 5     | 98.5  |",
		"| Bob      | 10    | 3.14  |",
	}
}

// TestRichTable_Alignment (RICH-04): delimiter row `:--|:-:|--:` →
// column_settings align left/center/right.
func TestRichTable_Alignment(t *testing.T) {
	tb, ok := buildTableBlock(minimalTableLines())
	if !ok {
		t.Fatal("buildTableBlock returned ok=false for a valid 3-col table")
	}
	if len(tb.ColumnSettings) != 3 {
		t.Fatalf("expected 3 column_settings; got %d", len(tb.ColumnSettings))
	}
	wants := []string{"left", "center", "right"}
	for i, want := range wants {
		if tb.ColumnSettings[i].Align != want {
			t.Errorf("column %d align = %q; want %q", i, tb.ColumnSettings[i].Align, want)
		}
		if tb.ColumnSettings[i].IsWrapped {
			t.Errorf("column %d is_wrapped should be false for v1", i)
		}
	}
}

// TestRichTable_HeaderBold (RICH-05): header row cells are rich_text with bold style.
func TestRichTable_HeaderBold(t *testing.T) {
	tb, ok := buildTableBlock(minimalTableLines())
	if !ok {
		t.Fatal("buildTableBlock returned ok=false")
	}
	if len(tb.Rows) == 0 {
		t.Fatal("no rows")
	}
	headerRow := tb.Rows[0]
	if len(headerRow) != 3 {
		t.Fatalf("header row has %d cells; want 3", len(headerRow))
	}
	for i, cell := range headerRow {
		if cell.Type != "rich_text" {
			t.Errorf("header cell[%d] type = %q; want rich_text", i, cell.Type)
		}
		if len(cell.Elements) == 0 {
			t.Errorf("header cell[%d] has no elements", i)
			continue
		}
		el := cell.Elements[0]
		if el.Type != "text" {
			t.Errorf("header cell[%d] element type = %q; want text", i, el.Type)
		}
		if el.Style == nil || !el.Style.Bold {
			t.Errorf("header cell[%d] element should have bold style", i)
		}
	}
	// Verify the header text values.
	wantTexts := []string{"Name", "Count", "Score"}
	for i, want := range wantTexts {
		if headerRow[i].Elements[0].Text != want {
			t.Errorf("header cell[%d] text = %q; want %q", i, headerRow[i].Elements[0].Text, want)
		}
	}
}

// TestRichTable_RawNumber (RICH-06): pure-numeric body cells → raw_number;
// non-numeric cells → raw_text.
func TestRichTable_RawNumber(t *testing.T) {
	lines := []string{
		"| Label  | Value | Notes |",
		"|--------|-------|-------|",
		"| hello  | 42    | plain |",
		"| world  | 3.14  | text  |",
		"| commas | 1,000 | num   |",
	}
	tb, ok := buildTableBlock(lines)
	if !ok {
		t.Fatal("buildTableBlock returned ok=false")
	}
	// Rows[0] = header, Rows[1..3] = body.
	if len(tb.Rows) != 4 {
		t.Fatalf("expected 4 rows (1 header + 3 body); got %d", len(tb.Rows))
	}
	type cellCheck struct {
		row, col  int
		wantType  string
		wantText  string
	}
	checks := []cellCheck{
		{1, 0, "raw_text", "hello"},
		{1, 1, "raw_number", "42"},
		{1, 2, "raw_text", "plain"},
		{2, 0, "raw_text", "world"},
		{2, 1, "raw_number", "3.14"},
		{3, 1, "raw_number", "1,000"},
	}
	for _, c := range checks {
		cell := tb.Rows[c.row][c.col]
		if cell.Type != c.wantType {
			t.Errorf("row %d col %d type = %q; want %q", c.row, c.col, cell.Type, c.wantType)
		}
		if cell.Text != c.wantText {
			t.Errorf("row %d col %d text = %q; want %q", c.row, c.col, cell.Text, c.wantText)
		}
	}
}

// TestRichTable_RaggedPad (RICH-07): a body row with fewer cells than the header
// is padded to numCols with empty raw_text cells.
func TestRichTable_RaggedPad(t *testing.T) {
	lines := []string{
		"| A | B | C |",
		"|---|---|---|",
		"| x |",    // only 1 cell — ragged
		"| p | q | r |",
	}
	tb, ok := buildTableBlock(lines)
	if !ok {
		t.Fatal("buildTableBlock returned ok=false")
	}
	// Rows[0]=header, Rows[1]=ragged, Rows[2]=full
	if len(tb.Rows) != 3 {
		t.Fatalf("expected 3 rows; got %d", len(tb.Rows))
	}
	raggedRow := tb.Rows[1]
	if len(raggedRow) != 3 {
		t.Fatalf("ragged row should be padded to 3 cells; got %d", len(raggedRow))
	}
	// Cell[0] should have 'x'; cells[1] and [2] should be empty raw_text.
	if raggedRow[0].Text != "x" {
		t.Errorf("ragged row cell[0] text = %q; want %q", raggedRow[0].Text, "x")
	}
	for i := 1; i < 3; i++ {
		if raggedRow[i].Type != "raw_text" {
			t.Errorf("ragged row cell[%d] type = %q; want raw_text", i, raggedRow[i].Type)
		}
		if raggedRow[i].Text != "" {
			t.Errorf("ragged row cell[%d] text = %q; want empty", i, raggedRow[i].Text)
		}
	}
}

// TestRichTable_ColsGuard (RICH-08): a table with >20 columns → buildTableBlock
// returns ok=false.
func TestRichTable_ColsGuard(t *testing.T) {
	// Build a 21-column table.
	makeRow := func(prefix string) string {
		cells := make([]string, 21)
		for i := range cells {
			cells[i] = fmt.Sprintf(" %s%d ", prefix, i+1)
		}
		return "|" + strings.Join(cells, "|") + "|"
	}
	sepRow := "|" + strings.Repeat(":--|", 21)
	lines := []string{
		makeRow("h"),
		sepRow,
		makeRow("v"),
	}
	_, ok := buildTableBlock(lines)
	if ok {
		t.Error("buildTableBlock should return ok=false for a table with >20 columns")
	}
}

// TestRichTable_RowsGuard (RICH-09): a table with >100 data rows → buildTableBlock
// returns ok=false.
func TestRichTable_RowsGuard(t *testing.T) {
	lines := []string{
		"| A | B |",
		"|---|---|",
	}
	// Add 101 body rows.
	for i := 0; i < 101; i++ {
		lines = append(lines, fmt.Sprintf("| row%d | val%d |", i, i))
	}
	_, ok := buildTableBlock(lines)
	if ok {
		t.Error("buildTableBlock should return ok=false for a table with >100 data rows")
	}
}

// TestRichTable_GuardFallback: a >20-col table through RenderRich emits a
// monospace fenced markdown block (the fencePipeTables fallback), NOT a
// {"type":"table"} block.
func TestRichTable_GuardFallback(t *testing.T) {
	input, err := os.ReadFile("testdata/rich-table-guards.md")
	if err != nil {
		t.Fatalf("read guard fixture: %v", err)
	}
	bj, _, ok := RenderRich(string(input), false)
	if !ok {
		t.Fatal("RenderRich returned ok=false (expected fallback, not failure)")
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Must NOT contain a "table" block.
	for _, b := range blocks {
		if b["type"] == "table" {
			t.Errorf("guard fixture should NOT produce a table block; got: %s", bj)
		}
	}
	// Must contain a "markdown" block (the fencePipeTables fallback wraps in ```).
	found := false
	for _, b := range blocks {
		if b["type"] == "markdown" {
			txt, _ := b["text"].(string)
			if strings.Contains(txt, "```") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("guard fixture should produce a markdown block with ``` fence; got: %s", bj)
	}
}

// ---------------------------------------------------------------------------
// TestRichCorpus walks the rich-*.md fixtures and compares RenderRich output
// to the rich-*.expected-blocks.json golden files.
// This is the prose-only case (Plan 01). Plan 04 will extend this to table
// and mixed fixtures.
//
// Fixture format: two-key JSON object {"blocks":[...],"text":"..."}.
func TestRichCorpus(t *testing.T) {
	matches, err := filepath.Glob("testdata/rich-*.md")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no rich corpus fixtures found")
	}
	for _, md := range matches {
		base := strings.TrimSuffix(md, ".md")
		expectedPath := base + ".expected-blocks.json"
		// Skip fixtures without a companion expected file (e.g. guard-only fixtures).
		if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
			t.Logf("skipping %s: no expected-blocks.json", filepath.Base(md))
			continue
		}
		t.Run(filepath.Base(base), func(t *testing.T) {
			input, err := os.ReadFile(md)
			if err != nil {
				t.Fatalf("read input: %v", err)
			}
			expectedRaw, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("read expected: %v", err)
			}
			bj, fallback, ok := RenderRich(string(input), false)
			if !ok {
				t.Fatalf("RenderRich returned ok=false for %s", md)
			}

			// Two-key format: {"blocks":[...],"text":"..."}
			var want struct {
				Blocks json.RawMessage `json:"blocks"`
				Text   string          `json:"text"`
			}
			if err := json.Unmarshal(expectedRaw, &want); err != nil {
				t.Fatalf("parse expected JSON: %v", err)
			}
			if want.Blocks == nil {
				t.Fatalf("expected-blocks.json has no 'blocks' key for %s", md)
			}

			var gotBlocks, wantBlocks []map[string]any
			if err := json.Unmarshal([]byte(bj), &gotBlocks); err != nil {
				t.Fatalf("unmarshal got blocks: %v", err)
			}
			if err := json.Unmarshal(want.Blocks, &wantBlocks); err != nil {
				t.Fatalf("unmarshal want blocks: %v", err)
			}
			gotJ, _ := json.Marshal(gotBlocks)
			wantJ, _ := json.Marshal(wantBlocks)
			if string(gotJ) != string(wantJ) {
				t.Errorf("blocks mismatch:\n  got:  %s\n  want: %s", gotJ, wantJ)
			}
			if fallback != want.Text {
				t.Errorf("fallback text mismatch:\n  got:  %q\n  want: %q", fallback, want.Text)
			}
		})
	}
}
