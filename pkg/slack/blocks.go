// Package slack — blocks.go
// Tier 2 Block Kit renderer. Phase 74 PR2.
//
// RenderBlocks converts CommonMark-ish input into a Slack Block Kit JSON array
// (header / section / context / divider blocks). The caller is responsible for
// falling back to Mrkdwnify (Tier 1) when ok==false.
package slack

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Slack Block Kit structural limits.
const (
	maxBlocks       = 50   // Slack hard cap: chat.postMessage allows at most 50 blocks
	maxHeaderChars  = 150  // plain_text header text limit
	maxSectionChars = 3000 // mrkdwn section text limit
	// fenceReserve is the headroom (chars) left below maxSectionChars when a
	// section contains a ``` fence, so rebalanceFences' re-fence markers
	// ("```\n" prefix + "\n```" suffix = 8 chars) can't push a chunk over the cap.
	fenceReserve = 10
)

// Typed structs for Block Kit JSON elements.
// Using named structs (not map[string]any) ensures valid, predictable JSON.

type plainTextField struct {
	Type string `json:"type"` // always "plain_text"
	Text string `json:"text"`
}

type mrkdwnField struct {
	Type string `json:"type"` // always "mrkdwn"
	Text string `json:"text"`
}

type blockHeader struct {
	Type string     `json:"type"` // "header"
	Text plainTextField `json:"text"`
}

type blockSection struct {
	Type string      `json:"type"` // "section"
	Text mrkdwnField `json:"text"`
}

type blockContext struct {
	Type     string        `json:"type"` // "context"
	Elements []mrkdwnField `json:"elements"`
}

type blockDivider struct {
	Type string `json:"type"` // "divider"
}

// reBlockHRule matches horizontal rules on a single line: ---, ***, ___ (possibly
// surrounded by whitespace). Named reBlockHRule to avoid conflict with mrkdwn.go's reHRule.
var reBlockHRule = regexp.MustCompile(`^\s*(---|\*\*\*|___)\s*$`)

// reToolLine matches Phase 68 tool lines: 🔧 <Word>: …
// The emoji is U+1F527 (🔧). We match by checking the string prefix rather than
// a regex to avoid UTF-8 complexity with variable-width byte sequences.
const toolLinePrefix = "🔧"

// reH1 matches `# Heading` (exactly one `#` followed by a space).
var reH1 = regexp.MustCompile(`^# (.+)$`)

// reH2H3 matches `## Heading` or `### Heading`.
var reH2H3 = regexp.MustCompile(`^#{2,3} (.+)$`)

// reHeaderStrip removes backticks, asterisks, and underscores from header text (BLK-09).
var reHeaderStrip = regexp.MustCompile("[`*_]")

// RenderBlocks builds a Block Kit JSON array from CommonMark-ish input.
// Returns:
//
//	blocksJSON   — pre-serialized JSON array suitable for Slack's `blocks:` field
//	fallbackText — plain-text rendering for chat.postMessage `text:` (push/search)
//	ok           — true on success; false if the build would exceed Slack's 50-block
//	               cap (caller falls back to Tier 1 mrkdwn for the entire post)
//
// Fail-soft: panics inside the builder return ("", "", false).
func RenderBlocks(input string) (blocksJSON, fallbackText string, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			blocksJSON, fallbackText, ok = "", "", false
		}
	}()
	return renderBlocks(input)
}

// renderBlocks is the inner implementation (no recover; outer wrapper handles panics).
func renderBlocks(input string) (blocksJSON, fallbackText string, ok bool) {
	lines := strings.Split(input, "\n")

	var blocks []any
	var fallbackLines []string

	// pendingSection accumulates text for the current section block.
	// When a structural boundary is hit (h1/h2/h3/tool/divider), the
	// pending section is flushed first.
	var pendingSection strings.Builder

	flushSection := func() {
		raw := strings.TrimSpace(pendingSection.String())
		pendingSection.Reset()
		if raw == "" {
			return
		}
		// Mrkdwnify the WHOLE accumulated section in one pass so multi-line
		// transforms (fencePipeTables) see contiguous runs. The old per-line
		// Mrkdwnify (in the ordinary-line branch) fed the fencer one line at a
		// time, so a pipe-table run was never detected on the default blocks
		// path and rendered as literal `|` text. Mrkdwnify is idempotent, so the
		// H2/H3 *bold* prefix already written into pendingSection survives.
		text := Mrkdwnify(raw)
		// Split on the 3000-char boundary if needed, then rebalance so a split
		// never leaves a code fence (incl. a fenced table) open across chunks
		// (§3.2). Only when the section actually contains a fence do we reserve
		// headroom for the re-fence markers rebalanceFences may add, so ordinary
		// (non-fenced) sections keep their exact existing split behaviour.
		budget := maxSectionChars
		if strings.Contains(text, "```") {
			budget = maxSectionChars - fenceReserve
		}
		chunks := rebalanceFences(splitSection(text, budget))
		for _, chunk := range chunks {
			blocks = append(blocks, blockSection{
				Type: "section",
				Text: mrkdwnField{Type: "mrkdwn", Text: chunk},
			})
		}
	}

	// inCodeFence tracks whether we're inside a ``` fenced block in the INPUT.
	// Lines within a fence (and the ``` delimiters themselves) are accumulated
	// verbatim and never interpreted as structural (#, ---, tool lines), so a
	// fenced code sample that contains those characters isn't split apart (§3.3).
	inCodeFence := false

	for _, rawLine := range lines {
		// Remove trailing \r if present (Windows line endings).
		line := strings.TrimRight(rawLine, "\r")

		// Code-fence handling: a ``` delimiter toggles fence state; while a fence
		// is open, every line (and both delimiters) is ordinary accumulation.
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCodeFence = !inCodeFence
			if pendingSection.Len() > 0 {
				pendingSection.WriteByte('\n')
			}
			pendingSection.WriteString(line)
			fallbackLines = append(fallbackLines, stripForFallback(line))
			continue
		}
		if inCodeFence {
			if pendingSection.Len() > 0 {
				pendingSection.WriteByte('\n')
			}
			pendingSection.WriteString(line)
			fallbackLines = append(fallbackLines, stripForFallback(line))
			continue
		}

		// 1. Horizontal rule → divider.
		if reBlockHRule.MatchString(line) {
			flushSection()
			blocks = append(blocks, blockDivider{Type: "divider"})
			// fallback: drop hrules (BLK-07).
			continue
		}

		// 2. H1 → header block.
		if m := reH1.FindStringSubmatch(line); m != nil {
			flushSection()
			inner := stripHeaderMarkup(m[1])
			inner = truncateHeader(inner, maxHeaderChars)
			blocks = append(blocks, blockHeader{
				Type: "header",
				Text: plainTextField{Type: "plain_text", Text: inner},
			})
			// fallback: heading without # (BLK-07).
			fallbackLines = append(fallbackLines, inner)
			continue
		}

		// 3. H2/H3 → section block with bold prefix.
		if m := reH2H3.FindStringSubmatch(line); m != nil {
			flushSection()
			inner := strings.TrimSpace(m[1])
			boldText := fmt.Sprintf("*%s*", inner)
			// Start a new section with the bold heading; subsequent lines
			// will accumulate into it via pendingSection.
			pendingSection.WriteString(boldText)
			// fallback: just the heading text without asterisks.
			fallbackLines = append(fallbackLines, inner)
			continue
		}

		// 4. Tool lines → context block.
		if strings.HasPrefix(line, toolLinePrefix) {
			flushSection()
			// HTML-escape only: < > & (the hook pre-formats the rest).
			escaped := htmlEscapeForContext(line)
			blocks = append(blocks, blockContext{
				Type:     "context",
				Elements: []mrkdwnField{{Type: "mrkdwn", Text: escaped}},
			})
			// fallback: verbatim (BLK-07).
			fallbackLines = append(fallbackLines, line)
			continue
		}

		// 5. Ordinary line: accumulate the RAW line. Mrkdwnify runs once over the
		// whole section at flush time (see flushSection) so multi-line transforms
		// (fencePipeTables) see the full run.
		if pendingSection.Len() > 0 {
			// We already have some content — separate from previous with a newline.
			pendingSection.WriteByte('\n')
		}
		pendingSection.WriteString(line)
		// For fallback: strip all markup from the line.
		fallbackLines = append(fallbackLines, stripForFallback(line))
	}

	// Flush any trailing section.
	flushSection()

	// BLK-06: 50-block cap fallback.
	if len(blocks) > maxBlocks {
		return "", "", false
	}

	// Marshal blocks.
	if len(blocks) == 0 {
		// No structural content: return a single section with mrkdwn content.
		mrkdwn := Mrkdwnify(strings.TrimSpace(input))
		if mrkdwn == "" {
			return "", "", false
		}
		single := []any{blockSection{
			Type: "section",
			Text: mrkdwnField{Type: "mrkdwn", Text: mrkdwn},
		}}
		b, err := json.Marshal(single)
		if err != nil {
			return "", "", false
		}
		plain := stripForFallback(strings.TrimSpace(input))
		return string(b), plain, true
	}

	b, err := json.Marshal(blocks)
	if err != nil {
		return "", "", false
	}

	// Build fallback text: join fallback lines, squash blank sequences.
	fallback := buildFallback(fallbackLines)
	return string(b), fallback, true
}

// splitSection splits section text at paragraph, sentence, or character
// boundaries when it exceeds maxLen chars (BLK-05).
func splitSection(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(text) > maxLen {
		// Try paragraph boundary first.
		idx := strings.LastIndex(text[:maxLen], "\n\n")
		if idx > 0 {
			chunks = append(chunks, text[:idx])
			text = strings.TrimSpace(text[idx+2:])
			continue
		}
		// Try sentence boundary.
		idx = strings.LastIndex(text[:maxLen], ". ")
		if idx > 0 {
			chunks = append(chunks, text[:idx+1]) // include the period
			text = strings.TrimSpace(text[idx+2:])
			continue
		}
		// Hard char boundary — but respect UTF-8 rune boundaries.
		end := maxLen
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		chunks = append(chunks, text[:end])
		text = text[end:]
	}
	if strings.TrimSpace(text) != "" {
		chunks = append(chunks, text)
	}
	return chunks
}

// rebalanceFences ensures every chunk is independently balanced with respect to
// ``` code fences. When splitSection cuts a fenced block (e.g. a large table)
// across chunks, the fence is closed at the end of the chunk that opened it and
// reopened at the start of the next, so each section block renders a balanced
// fence instead of leaking an unclosed ``` into the rest of the message. The
// flushSection caller reserves fenceReserve chars of headroom when a fence is
// present so the added markers can't push a chunk past the Slack section cap.
// §3.2.
func rebalanceFences(chunks []string) []string {
	out := make([]string, 0, len(chunks))
	reopen := false
	for _, c := range chunks {
		if reopen {
			c = "```\n" + c
			reopen = false
		}
		if strings.Count(c, "```")%2 == 1 {
			// Fence left open at the end of this chunk: close it here, reopen next.
			c = strings.TrimRight(c, "\n") + "\n```"
			reopen = true
		}
		if strings.TrimSpace(c) != "" {
			out = append(out, c)
		}
	}
	return out
}

// stripHeaderMarkup removes backticks, asterisks, and underscores from a header
// string for use in a plain_text block (BLK-09).
func stripHeaderMarkup(s string) string {
	return reHeaderStrip.ReplaceAllString(s, "")
}

// truncateHeader hard-truncates text to 147 runes + "…" = 148 total (BLK-10).
// Slack's plain_text header field allows at most 150 chars; we leave 2 chars
// of headroom for the ellipsis (1 rune = 3 UTF-8 bytes, but Slack counts chars
// not bytes in some contexts). This matches the plan spec: "truncate to 147 + …".
func truncateHeader(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	// Keep 147 runes + "…" = 148 total.
	return string(runes[:maxLen-3]) + "…"
}

// htmlEscapeForContext performs HTML-escaping of < > & only (BLK-03).
// The hook already pre-formats tool lines so no other transforms are needed.
func htmlEscapeForContext(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// stripForFallback strips all markdown formatting from a single line for use
// in the plain-text fallback field (BLK-07).
// Rules:
//   - Remove backticks (inline code markers).
//   - Remove asterisks and underscores (bold/italic markers).
//   - Replace [label](url) links with label only.
//   - Drop heading `#` prefixes (already done by caller for H1/H2/H3).
//   - Strip leading `# ` markers from ordinary lines.
var (
	reFallbackLink = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	reFallbackMD   = regexp.MustCompile("[`*_]")
)

func stripForFallback(line string) string {
	// Convert markdown links to label only.
	line = reFallbackLink.ReplaceAllString(line, "$1")
	// Remove code/bold/italic markers.
	line = reFallbackMD.ReplaceAllString(line, "")
	return line
}

// buildFallback joins the fallback lines, collapsing multiple blank lines into
// one and trimming leading/trailing whitespace.
func buildFallback(lines []string) string {
	var parts []string
	blank := false
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			if !blank && len(parts) > 0 {
				parts = append(parts, "")
			}
			blank = true
		} else {
			parts = append(parts, l)
			blank = false
		}
	}
	// Remove trailing blank.
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, "\n")
}
