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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/quick"
)

// TestBlocks_H1Header (BLK-01): `# Heading` maps to a header block.
func TestBlocks_H1Header(t *testing.T) {
	t.Skip("Wave 1 implementation")
}

// TestBlocks_H2H3Section (BLK-02): `## sub` and `### sub` map to mrkdwn section blocks.
func TestBlocks_H2H3Section(t *testing.T) {
	t.Skip("Wave 1 implementation")
}

// TestBlocks_ToolLine (BLK-03): `🔧 Edit: /path` maps to a context block.
func TestBlocks_ToolLine(t *testing.T) {
	t.Skip("Wave 1 implementation")
}

// TestBlocks_Divider (BLK-04): `---` / `***` / `___` maps to a divider block.
func TestBlocks_Divider(t *testing.T) {
	t.Skip("Wave 1 implementation")
}

// TestBlocks_SectionOverflow (BLK-05): section text > 3000 chars splits into multiple section blocks.
func TestBlocks_SectionOverflow(t *testing.T) {
	t.Skip("Wave 1 implementation")
}

// TestBlocks_50BlockFallback (BLK-06): input that would produce > 50 blocks returns ok=false.
func TestBlocks_50BlockFallback(t *testing.T) {
	t.Skip("Wave 1 implementation")
}

// TestBlocks_PlainTextFallback (BLK-07): fallbackText strips all formatting.
func TestBlocks_PlainTextFallback(t *testing.T) {
	t.Skip("Wave 1 implementation")
}

// TestBlocks_StructuralValidity (BLK-08): property test — all valid outputs satisfy
// Slack's structural constraints (block types, text lengths, count ≤ 50).
func TestBlocks_StructuralValidity(t *testing.T) {
	t.Skip("Wave 1 implementation")
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
				if len(txt) > 150 {
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
	t.Skip("Wave 1 implementation")
}

// TestBlocks_HeaderTruncate (BLK-10): header text > 150 chars hard-truncated to 147 + `…`.
func TestBlocks_HeaderTruncate(t *testing.T) {
	t.Skip("Wave 1 implementation")
}

// TestBlocksCorpus walks pkg/slack/testdata/blocks-*.md, reads the companion
// .expected-blocks.json, and asserts RenderBlocks produces matching output.
// Fixtures whose name contains "50block-fallback" are skipped (no expected file).
func TestBlocksCorpus(t *testing.T) {
	t.Skip("Wave 1 implementation")
	matches, err := filepath.Glob("testdata/blocks-*.md")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, md := range matches {
		base := strings.TrimSuffix(md, ".md")
		if strings.Contains(base, "50block-fallback") {
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
