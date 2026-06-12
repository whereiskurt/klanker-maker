// Package slack — blocks_test.go
// BLK-01..BLK-10 + structural validity + corpus tests for the Tier 2
// Block Kit renderer (pkg/slack.RenderBlocks).
//
// Two-key fixture format for BLK-07 (plain-text fallback):
//   blocks-plain-text-fallback.expected-blocks.json contains a JSON object with
//   TWO top-level keys:
//     "blocks" — the expected Block Kit array
//     "text"   — the expected plain-text fallback string
//   All other corpus fixtures contain only the Block Kit array directly.
//
// The blocks-50block-fallback.md fixture has no .expected-blocks.json by design
// (the test asserts ok==false directly). TestBlocksCorpus skips files whose name
// contains "50block-fallback".
package slack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/quick"
)

// TestBlocks_H1Header (BLK-01): `# Heading` maps to a header block.
func TestBlocks_H1Header(t *testing.T) {
	input := "# Refactoring Plan\n\nSome intro text.\n"
	bj, _, ok := RenderBlocks(input)
	if !ok {
		t.Fatal("RenderBlocks returned ok=false")
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(blocks) < 1 {
		t.Fatal("expected at least 1 block")
	}
	if blocks[0]["type"] != "header" {
		t.Errorf("first block type = %q; want header", blocks[0]["type"])
	}
	text, _ := blocks[0]["text"].(map[string]any)
	if text["text"] != "Refactoring Plan" {
		t.Errorf("header text = %q; want %q", text["text"], "Refactoring Plan")
	}
	if text["type"] != "plain_text" {
		t.Errorf("header text type = %q; want plain_text", text["type"])
	}
}

// TestBlocks_H2H3Section (BLK-02): `## sub` and `### sub` map to mrkdwn section blocks with bold prefix.
func TestBlocks_H2H3Section(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{"h2", "## Step One\n", "*Step One*"},
		{"h3", "### Sub\n", "*Sub*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bj, _, ok := RenderBlocks(tc.input)
			if !ok {
				t.Fatal("ok=false")
			}
			var blocks []map[string]any
			if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(blocks) < 1 {
				t.Fatal("expected at least 1 block")
			}
			if blocks[0]["type"] != "section" {
				t.Errorf("type = %q; want section", blocks[0]["type"])
			}
			text, _ := blocks[0]["text"].(map[string]any)
			txt, _ := text["text"].(string)
			if !strings.HasPrefix(txt, tc.want) {
				t.Errorf("section text = %q; want prefix %q", txt, tc.want)
			}
			if text["type"] != "mrkdwn" {
				t.Errorf("text type = %q; want mrkdwn", text["type"])
			}
		})
	}
}

// TestBlocks_ToolLine (BLK-03): `🔧 Edit: /path` maps to a context block.
func TestBlocks_ToolLine(t *testing.T) {
	input := "🔧 Edit: /path/to/file.go (line 42)\n"
	bj, _, ok := RenderBlocks(input)
	if !ok {
		t.Fatal("ok=false")
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0]["type"] != "context" {
		t.Errorf("type = %q; want context", blocks[0]["type"])
	}
	elements, _ := blocks[0]["elements"].([]any)
	if len(elements) != 1 {
		t.Fatalf("expected 1 element, got %d", len(elements))
	}
	elem, _ := elements[0].(map[string]any)
	if elem["type"] != "mrkdwn" {
		t.Errorf("element type = %q; want mrkdwn", elem["type"])
	}
	txt, _ := elem["text"].(string)
	if !strings.Contains(txt, "Edit:") {
		t.Errorf("tool text = %q; want to contain 'Edit:'", txt)
	}
}

// TestBlocks_Divider (BLK-04): `---` / `***` / `___` maps to a divider block.
func TestBlocks_Divider(t *testing.T) {
	for _, sep := range []string{"---", "***", "___"} {
		t.Run(sep, func(t *testing.T) {
			input := "Para 1\n\n" + sep + "\n\nPara 2\n"
			bj, _, ok := RenderBlocks(input)
			if !ok {
				t.Fatalf("ok=false for sep %q", sep)
			}
			var blocks []map[string]any
			if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			// Must contain a divider block.
			hasDivider := false
			for _, b := range blocks {
				if b["type"] == "divider" {
					hasDivider = true
				}
			}
			if !hasDivider {
				t.Errorf("no divider block found for separator %q; blocks: %s", sep, bj)
			}
		})
	}
}

// TestBlocks_SectionOverflow (BLK-05): section text > 3000 chars splits into multiple section blocks.
func TestBlocks_SectionOverflow(t *testing.T) {
	// 3100 chars of plain text (no headers/dividers so it all lands in one logical section).
	big := strings.Repeat("A", 3100)
	bj, _, ok := RenderBlocks(big)
	if !ok {
		t.Fatal("ok=false")
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Should have split into at least 2 section blocks.
	sectionCount := 0
	for _, b := range blocks {
		if b["type"] == "section" {
			text, _ := b["text"].(map[string]any)
			txt, _ := text["text"].(string)
			if len(txt) > 3000 {
				t.Errorf("section text length %d exceeds 3000", len(txt))
			}
			sectionCount++
		}
	}
	if sectionCount < 2 {
		t.Errorf("expected ≥2 section blocks for 3100-char input, got %d", sectionCount)
	}
}

// TestBlocks_50BlockFallback (BLK-06): input that would produce > 50 blocks returns ok=false.
func TestBlocks_50BlockFallback(t *testing.T) {
	// 60 dividers → 60 divider blocks → exceeds the 50-block cap.
	input := strings.Repeat("---\n", 60)
	_, _, ok := RenderBlocks(input)
	if ok {
		t.Error("expected ok=false for 60-divider input exceeding 50-block cap")
	}
}

// TestBlocks_PlainTextFallback (BLK-07): fallbackText strips all formatting.
func TestBlocks_PlainTextFallback(t *testing.T) {
	input := "# My Heading\n\nSome **bold** text with a [link](https://example.com) and `code`.\n"
	_, fallback, ok := RenderBlocks(input)
	if !ok {
		t.Fatal("ok=false")
	}
	// Heading should appear without # prefix.
	if strings.Contains(fallback, "#") {
		t.Errorf("fallback should not contain '#'; got: %q", fallback)
	}
	// Bold markers stripped.
	if strings.Contains(fallback, "**") {
		t.Errorf("fallback should not contain '**'; got: %q", fallback)
	}
	// Link text preserved, URL dropped.
	if !strings.Contains(fallback, "link") {
		t.Errorf("fallback should contain 'link' (label); got: %q", fallback)
	}
	if strings.Contains(fallback, "https://") {
		t.Errorf("fallback should not contain the URL; got: %q", fallback)
	}
	// Code backticks stripped.
	if strings.Contains(fallback, "`") {
		t.Errorf("fallback should not contain backticks; got: %q", fallback)
	}
}

// TestBlocks_StructuralValidity (BLK-08): property test — all valid outputs satisfy
// Slack's structural constraints (block types, text lengths, count ≤ 50).
func TestBlocks_StructuralValidity(t *testing.T) {
	f := func(input string) bool {
		bj, _, ok := RenderBlocks(input)
		if !ok {
			return true // fallback path is valid
		}
		var blocks []map[string]any
		if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
			return false
		}
		if len(blocks) > 50 {
			return false
		}
		for _, b := range blocks {
			typ, _ := b["type"].(string)
			switch typ {
			case "header":
				text, _ := b["text"].(map[string]any)
				txt, _ := text["text"].(string)
				if len([]rune(txt)) > 150 {
					return false
				}
			case "section":
				text, _ := b["text"].(map[string]any)
				txt, _ := text["text"].(string)
				if len(txt) > 3000 {
					return false
				}
			case "context", "divider":
				// ok
			default:
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Error(err)
	}
}

// TestBlocks_HeaderStrip (BLK-09): backticks/asterisks/underscores stripped from header text.
func TestBlocks_HeaderStrip(t *testing.T) {
	input := "# The `foo` *bold* _italic_ function\n"
	bj, _, ok := RenderBlocks(input)
	if !ok {
		t.Fatal("ok=false")
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(blocks) < 1 || blocks[0]["type"] != "header" {
		t.Fatal("expected header block as first block")
	}
	text, _ := blocks[0]["text"].(map[string]any)
	txt, _ := text["text"].(string)

	for _, forbidden := range []string{"`", "*", "_"} {
		if strings.Contains(txt, forbidden) {
			t.Errorf("header text %q contains forbidden char %q after strip", txt, forbidden)
		}
	}
	// The words should still be present.
	for _, want := range []string{"foo", "bold", "italic", "function"} {
		if !strings.Contains(txt, want) {
			t.Errorf("header text %q missing expected word %q", txt, want)
		}
	}
}

// TestBlocks_HeaderTruncate (BLK-10): header text > 150 chars hard-truncated to 147 runes + `…`.
func TestBlocks_HeaderTruncate(t *testing.T) {
	// 200 'A's → after strip (no markup), all 200 remain → truncate to 147 + '…' = 148 runes.
	input := "# " + strings.Repeat("A", 200) + "\n"
	bj, _, ok := RenderBlocks(input)
	if !ok {
		t.Fatal("ok=false")
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(blocks) < 1 || blocks[0]["type"] != "header" {
		t.Fatal("expected header block")
	}
	text, _ := blocks[0]["text"].(map[string]any)
	txt, _ := text["text"].(string)
	runes := []rune(txt)
	if len(runes) > 150 {
		t.Errorf("header text length %d exceeds 150 runes", len(runes))
	}
	if !strings.HasSuffix(txt, "…") {
		t.Errorf("truncated header should end with '…'; got: %q", txt)
	}
}

// TestBlocksCorpus walks pkg/slack/testdata/blocks-*.md, reads the companion
// .expected-blocks.json, and asserts RenderBlocks produces matching output.
// Fixtures whose name contains "50block-fallback" are skipped (no expected file).
func TestBlocksCorpus(t *testing.T) {
	matches, err := filepath.Glob("testdata/blocks-*.md")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no corpus fixtures found")
	}
	for _, md := range matches {
		base := strings.TrimSuffix(md, ".md")
		if strings.Contains(base, "50block-fallback") {
			// No expected-blocks.json for this fixture — tested in TestBlocks_50BlockFallback.
			continue
		}
		if strings.Contains(base, "section-overflow") {
			// Overflow fixture uses placeholder expected content; tested in TestBlocks_SectionOverflow.
			continue
		}
		t.Run(filepath.Base(base), func(t *testing.T) {
			input, err := os.ReadFile(md)
			if err != nil {
				t.Fatalf("read input: %v", err)
			}
			expectedPath := base + ".expected-blocks.json"
			expectedRaw, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("read expected: %v", err)
			}
			bj, fallback, ok := RenderBlocks(string(input))
			if !ok {
				t.Fatalf("RenderBlocks returned ok=false for %s", md)
			}

			// Check for two-key fixture (BLK-07 plain-text fallback format).
			// The two-key format has a JSON object with "blocks" array and "text" string.
			var twoKey struct {
				Blocks json.RawMessage `json:"blocks"`
				Text   string          `json:"text"`
			}
			if err := json.Unmarshal(expectedRaw, &twoKey); err == nil && twoKey.Blocks != nil {
				// Two-key format: compare both blocks and fallback text.
				var gotBlocks, wantBlocks []map[string]any
				if err := json.Unmarshal([]byte(bj), &gotBlocks); err != nil {
					t.Fatalf("unmarshal got blocks: %v", err)
				}
				if err := json.Unmarshal(twoKey.Blocks, &wantBlocks); err != nil {
					t.Fatalf("unmarshal want blocks: %v", err)
				}
				gotJ, _ := json.Marshal(gotBlocks)
				wantJ, _ := json.Marshal(wantBlocks)
				if string(gotJ) != string(wantJ) {
					t.Errorf("blocks mismatch:\n  got:  %s\n  want: %s", gotJ, wantJ)
				}
				if fallback != twoKey.Text {
					t.Errorf("fallback text mismatch:\n  got:  %q\n  want: %q", fallback, twoKey.Text)
				}
				return
			}

			// Single-key format: expectedRaw is the blocks array directly.
			var gotBlocks, wantBlocks []map[string]any
			if err := json.Unmarshal([]byte(bj), &gotBlocks); err != nil {
				t.Fatalf("unmarshal got: %v", err)
			}
			if err := json.Unmarshal(expectedRaw, &wantBlocks); err != nil {
				t.Fatalf("unmarshal want: %v", err)
			}
			gotJ, _ := json.Marshal(gotBlocks)
			wantJ, _ := json.Marshal(wantBlocks)
			if string(gotJ) != string(wantJ) {
				t.Errorf("blocks mismatch:\n  got:  %s\n  want: %s", gotJ, wantJ)
			}
		})
	}
}

// --- Slack markdown improvement plan: default-blocks-path table fencing ---
// These cover the regression where renderBlocks called Mrkdwnify per-line, so
// fencePipeTables (which needs >=2 consecutive pipe lines in one call) never
// fired on the default blocks path and GFM tables rendered as literal `|` text.

// sectionTexts returns the mrkdwn text of every section block in the rendered
// output, in order.
func sectionTexts(t *testing.T, input string) []string {
	t.Helper()
	bj, _, ok := RenderBlocks(input)
	if !ok {
		t.Fatalf("RenderBlocks ok=false for input %q", input)
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var out []string
	for _, b := range blocks {
		if b["type"] != "section" {
			continue
		}
		txt, _ := b["text"].(map[string]any)
		s, _ := txt["text"].(string)
		out = append(out, s)
	}
	return out
}

// Case 1: a 2-col GFM table (header + separator + rows) on the default blocks
// path is wrapped in a single ```-fenced block.
func TestBlocks_PipeTableFenced(t *testing.T) {
	input := "Here are the results:\n\n" +
		"| Folder | Count |\n" +
		"|--------|-------|\n" +
		"| vendor | 189 |\n" +
		"| build  | 216 |\n"
	joined := strings.Join(sectionTexts(t, input), "\n")
	if c := strings.Count(joined, "```"); c < 2 {
		t.Fatalf("expected the table to be wrapped in a ``` fence (>=2 markers); got %d in:\n%s", c, joined)
	}
	if strings.Count(joined, "```")%2 != 0 {
		t.Fatalf("unbalanced ``` fence in rendered section:\n%s", joined)
	}
	// Rows are reflowed into a column-aligned grid (the `189`/`216` cells are
	// padded to the `Count` column width) and the raw `|---|` separator becomes a
	// width-matched rule.
	for _, row := range []string{"| vendor | 189   |", "| build  | 216   |", "| ------ | ----- |"} {
		if !strings.Contains(joined, row) {
			t.Errorf("fenced section missing aligned table row %q:\n%s", row, joined)
		}
	}
	if strings.Contains(joined, "|--------|-------|") {
		t.Errorf("raw GFM separator row should be reflowed away:\n%s", joined)
	}
}

// Case 2: the screenshot case — table cells the agent pre-backticked (`.git`)
// stay literal inside the fence rather than rendering as inline code chips.
func TestBlocks_PipeTableBacktickedCells(t *testing.T) {
	input := "| Path | Size |\n" +
		"|------|------|\n" +
		"| `.git` | 130G |\n" +
		"| `.cache` | 12G |\n"
	joined := strings.Join(sectionTexts(t, input), "\n")
	if c := strings.Count(joined, "```"); c < 2 || c%2 != 0 {
		t.Fatalf("expected a balanced ``` fence around the table; got %d markers:\n%s", c, joined)
	}
	// The pre-backticked cell text is carried verbatim inside the fence.
	if !strings.Contains(joined, "`.git`") {
		t.Errorf("expected `.git` cell text preserved inside the fence:\n%s", joined)
	}
}

// Case 3: H2 heading immediately followed by a table — heading is one bold line,
// the table is fenced, and Mrkdwnify is idempotent (rendering twice is stable).
func TestBlocks_H2ThenTableIdempotent(t *testing.T) {
	input := "## Disk usage\n" +
		"| Path | Size |\n" +
		"|------|------|\n" +
		"| /data | 30G |\n" +
		"| /repos | 130G |\n"
	first := strings.Join(sectionTexts(t, input), "\n")
	if !strings.Contains(first, "*Disk usage*") {
		t.Errorf("expected bold H2 heading *Disk usage*:\n%s", first)
	}
	if c := strings.Count(first, "```"); c < 2 || c%2 != 0 {
		t.Errorf("expected balanced ``` fence around table after heading; got %d:\n%s", c, first)
	}
	// Idempotence: Mrkdwnify over the already-rendered section text is stable.
	if got := Mrkdwnify(first); got != first {
		t.Errorf("Mrkdwnify not idempotent over rendered section:\n got:  %q\n want: %q", got, first)
	}
}

// Case 6: a prose paragraph with a single inline pipe is NOT fenced (the >=2
// consecutive-pipe-line rule must still hold — regression guard).
func TestBlocks_InlinePipeNotFenced(t *testing.T) {
	input := "Run `a | b` to pipe the output, then check the log.\n"
	joined := strings.Join(sectionTexts(t, input), "\n")
	if strings.Contains(joined, "```") {
		t.Errorf("single inline pipe must not be fenced as a table:\n%s", joined)
	}
}

// Case 5 (§3.3): a fenced code block in the INPUT that contains #, ---, or a
// tool-line prefix must not be split into header/divider/context blocks.
func TestBlocks_CodeFenceWithStructuralChars(t *testing.T) {
	input := "Example:\n\n" +
		"```\n" +
		"# a comment, not an H1\n" +
		"--- not a divider\n" +
		"🔧 not a tool line\n" +
		"```\n"
	bj, _, ok := RenderBlocks(input)
	if !ok {
		t.Fatal("ok=false")
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(bj), &blocks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for i, b := range blocks {
		switch b["type"] {
		case "header", "divider", "context":
			t.Errorf("block %d is %q — fenced content was misinterpreted as structural", i, b["type"])
		}
	}
	joined := strings.Join(sectionTexts(t, input), "\n")
	if c := strings.Count(joined, "```"); c%2 != 0 {
		t.Errorf("input code fence left unbalanced in output (%d markers):\n%s", c, joined)
	}
}

// Case 4 (§3.2): a table larger than the section cap does not produce an
// unbalanced ``` fence across the split section blocks.
func TestBlocks_LargeTableBalancedFences(t *testing.T) {
	var b strings.Builder
	b.WriteString("| Key | Value |\n|-----|-------|\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "| key-%03d | %s |\n", i, strings.Repeat("x", 12))
	}
	texts := sectionTexts(t, b.String())
	for i, s := range texts {
		if strings.Count(s, "```")%2 != 0 {
			t.Errorf("section %d has an unbalanced ``` fence (len=%d):\n%.200s", i, len(s), s)
		}
		if len(s) > maxSectionChars {
			t.Errorf("section %d len=%d exceeds Slack cap %d (re-fence headroom failed)", i, len(s), maxSectionChars)
		}
	}
}
