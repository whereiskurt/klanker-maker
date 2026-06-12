// Package slack — Phase 74 mrkdwn renderer.
// This file implements the markdown-to-Slack-mrkdwn transformer used by
// cmd/km-slack when --render=mrkdwn is passed. The entry point is Mrkdwnify.
//
// Architecture (Phase 74 PR1 — Tier 1 mrkdwn only):
//
//   - Three-segment tokenizer: text / code-span / code-fence.
//     Transforms run ONLY on text segments. Code segments emit byte-for-byte.
//   - Seven Tier 1 transforms applied in order: HTML-escape, link conversion,
//     bold collapse, strikethrough, heading map, horizontal-rule drop,
//     pipe-table fence.
//   - Fail-soft: a deferred recover() wraps the whole pipeline so any panic
//     returns the original input unchanged. The streaming hook in
//     pkg/compiler/userdata.go depends on this — never crash.
//   - Idempotent: Mrkdwnify(Mrkdwnify(x)) == Mrkdwnify(x).
//
// Phase 74 PR2 will add Tier 2 Block Kit in a sibling blocks.go.
// Italic markdown handling (*x* → _x_) is explicitly deferred per CONTEXT.md.
package slack

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// segKind classifies a tokenizer segment.
type segKind int

const (
	segText      segKind = iota // normal prose, transforms run here
	segCodeSpan                 // single-backtick inline code, pass-through
	segCodeFence                // triple-backtick block, pass-through
)

// segment holds one classified chunk from the tokenizer.
type segment struct {
	kind segKind
	text string
}

// Mrkdwnify converts CommonMark-ish input into valid Slack mrkdwn.
// It is fail-soft: if anything inside panics, the original input is returned
// unchanged. The streaming hook in pkg/compiler/userdata.go depends on this.
//
// Wave 0: pass-through stub. Real transforms land in Wave 1 (Task 2).
func Mrkdwnify(input string) (out string) {
	out = input
	defer func() {
		if r := recover(); r != nil {
			out = input
		}
	}()
	segs := tokenize(input)
	var sb strings.Builder
	sb.Grow(len(input))
	for _, seg := range segs {
		switch seg.kind {
		case segText:
			sb.WriteString(applyText(seg.text))
		default:
			sb.WriteString(seg.text)
		}
	}
	out = sb.String()
	return
}

// tokenize splits input into text, code-span, and code-fence segments.
// Code-fence detection takes priority over code-span (``` checked before `).
// A code-fence starts at a line whose first non-space chars are ``` and ends
// at a matching ``` line. Any info string after the opening ``` is preserved.
// A code-span is delimited by single backticks within a line.
func tokenize(input string) []segment {
	var segs []segment
	i := 0
	n := len(input)
	textStart := 0

	flushText := func(end int) {
		if end > textStart {
			segs = append(segs, segment{kind: segText, text: input[textStart:end]})
		}
		textStart = end
	}

	for i < n {
		// Check for code-fence: a line starting with ``` (possibly with leading spaces)
		// We check if the current position is at the start of a line.
		if isLineStart(input, i) && i+2 < n && input[i] == '`' && input[i+1] == '`' && input[i+2] == '`' {
			// Flush preceding text
			flushText(i)

			// Find the end of the opening fence line
			fenceLineEnd := i
			for fenceLineEnd < n && input[fenceLineEnd] != '\n' {
				fenceLineEnd++
			}
			if fenceLineEnd < n {
				fenceLineEnd++ // include the newline
			}

			// Find the closing ```  on its own line
			closeIdx := findCodeFenceClose(input, fenceLineEnd)
			if closeIdx == -1 {
				// Unclosed fence — treat rest of input as a single fence segment
				segs = append(segs, segment{kind: segCodeFence, text: input[i:]})
				i = n
				textStart = n
			} else {
				segs = append(segs, segment{kind: segCodeFence, text: input[i:closeIdx]})
				i = closeIdx
				textStart = closeIdx
			}
			continue
		}

		// Check for code-span: single backtick
		if input[i] == '`' {
			flushText(i)
			// Find closing backtick on same line
			closeIdx := -1
			for j := i + 1; j < n && input[j] != '\n'; j++ {
				if input[j] == '`' {
					closeIdx = j + 1
					break
				}
			}
			if closeIdx == -1 {
				// No closing backtick on this line — treat as literal text
				textStart = i
				i++
				continue
			}
			segs = append(segs, segment{kind: segCodeSpan, text: input[i:closeIdx]})
			i = closeIdx
			textStart = closeIdx
			continue
		}

		i++
	}

	// Flush any remaining text
	flushText(n)
	return segs
}

// isLineStart returns true if position i is at the start of a line
// (i == 0 or preceded by a newline, ignoring leading spaces for the
// purpose of detecting fences).
func isLineStart(input string, i int) bool {
	if i == 0 {
		return true
	}
	// Walk back past spaces/tabs to see if there's a newline
	j := i - 1
	for j >= 0 && (input[j] == ' ' || input[j] == '\t') {
		j--
	}
	return j < 0 || input[j] == '\n'
}

// findCodeFenceClose searches for a closing ``` line starting at pos.
// Returns the index AFTER the closing ``` newline, or -1 if not found.
func findCodeFenceClose(input string, pos int) int {
	n := len(input)
	i := pos
	for i < n {
		// Check if this line starts with ```
		lineStart := i
		// Skip leading spaces
		for i < n && (input[i] == ' ' || input[i] == '\t') {
			i++
		}
		if i+2 < n && input[i] == '`' && input[i+1] == '`' && input[i+2] == '`' {
			// Check the rest of the line is only ``` (no extra chars, or just spaces)
			j := i + 3
			for j < n && (input[j] == ' ' || input[j] == '\t') {
				j++
			}
			if j >= n || input[j] == '\n' {
				if j < n {
					j++ // include newline
				}
				return j
			}
		}
		_ = lineStart
		// Advance to next line
		for i < n && input[i] != '\n' {
			i++
		}
		if i < n {
			i++ // skip newline
		}
	}
	return -1
}

// slackLinkPlaceholder returns a placeholder for a Slack link at index i.
// Used in applyText to protect already-converted links from re-processing.
func slackLinkPlaceholder(i int) string {
	return fmt.Sprintf("\x00KMLINK_%d_KMLINK\x00", i)
}

// applyText applies the seven Tier 1 transforms to a text segment.
// Transforms are applied in a carefully ordered sequence to ensure both
// correctness and idempotence:
//
//  0. Extract existing Slack links (<url|label>) as placeholders so they
//     survive all transforms unchanged (idempotence on 2nd pass).
//  1. htmlEscape — first, so subsequent transforms don't introduce unescaped < > &.
//     Existing HTML entities (&lt; &gt; &amp;) are preserved on second pass.
//  2. convertLinks — markdown [label](url) → Slack <url|label>. Existing Slack
//     links are already protected as placeholders.
//  3. mapHeadings — BEFORE collapseBold: heading wraps content in *, which may
//     combine with content-leading * to form **. collapseBold then reduces ** → *.
//     This ordering ensures idempotence for inputs like "# *bold*".
//  4. collapseBold — after headings so heading-created ** boundaries collapse once.
//  5. collapseStrike — after bold (independent transforms, order arbitrary).
//  6. dropHRules — before pipe-table fence (hrules look like separator rows).
//  7. fencePipeTables — last, after all inline transforms are settled.
//  8. Restore Slack link placeholders.
func applyText(seg string) string {
	s := seg

	// Step 0: protect existing Slack link syntax from re-processing.
	var slackLinks []string
	s = reSlackLink.ReplaceAllStringFunc(s, func(m string) string {
		idx := len(slackLinks)
		slackLinks = append(slackLinks, m)
		return slackLinkPlaceholder(idx)
	})

	s = htmlEscape(s)
	s = convertLinks(s)
	s = mapHeadings(s)
	s = collapseBold(s)
	s = collapseStrike(s)
	s = dropHRules(s)
	s = fencePipeTables(s)

	// Step 8: restore protected Slack links.
	for i := len(slackLinks) - 1; i >= 0; i-- {
		s = strings.ReplaceAll(s, slackLinkPlaceholder(i), slackLinks[i])
	}
	return s
}

// reSlackLink matches already-converted Slack link syntax <url|label> so
// htmlEscape can preserve them during idempotent second passes.
// The URL portion may contain spaces (from malformed markdown link targets).
var reSlackLink = regexp.MustCompile(`<[^<>]+\|[^<>]+>`)

// reExistingEntity matches already-escaped HTML entities (&lt; &gt; &amp;)
// so htmlEscape does not double-encode them on a second pass.
var reExistingEntity = regexp.MustCompile(`&(lt|gt|amp);`)

// htmlEscapePlaceholder returns a unique, collision-safe placeholder for index i.
// The format includes a long, unlikely prefix and suffix to prevent accidental
// formation of fake placeholders from adjacent text and NUL delimiters.
func htmlEscapePlaceholder(i int) string {
	return fmt.Sprintf("\x00KMHTML_%d_KMHTML\x00", i)
}

// htmlEscape replaces &, <, > with their HTML entities in text segments,
// but preserves existing Slack link syntax (<url|label>) and already-escaped
// HTML entities (&lt; &gt; &amp;) so that Mrkdwnify is idempotent: a second
// pass on already-converted output is a no-op.
//
// Implementation:
//  1. Extract Slack links (<url|label>) and existing HTML entities into
//     collision-safe placeholders so they survive the raw-character replacements.
//  2. Escape raw & → &amp;, raw < → &lt;, raw > → &gt;.
//  3. Restore placeholders in reverse order to avoid partial-match issues.
func htmlEscape(s string) string {
	// Step 1: extract tokens we must preserve.
	var preserved []string
	addPreserved := func(m string) string {
		idx := len(preserved)
		preserved = append(preserved, m)
		return htmlEscapePlaceholder(idx)
	}

	// Preserve Slack links first (they contain < > that must not be escaped).
	s = reSlackLink.ReplaceAllStringFunc(s, addPreserved)
	// Preserve existing HTML entities (& must not be re-escaped).
	s = reExistingEntity.ReplaceAllStringFunc(s, addPreserved)

	// Step 2: escape remaining raw characters.
	// Order: & first so we don't escape the & in entities we're about to insert.
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")

	// Step 3: restore preserved tokens in reverse order (avoids any dependency
	// where a higher-index restore could contain a lower-index placeholder, which
	// cannot happen because placeholders are unique, but reverse order is safer).
	for i := len(preserved) - 1; i >= 0; i-- {
		s = strings.ReplaceAll(s, htmlEscapePlaceholder(i), preserved[i])
	}
	return s
}

// reLink matches Markdown links [label](url). Non-greedy, no newlines in label.
// URL is captured greedily up to the last ')' on the same line.
var reLink = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\n]+)\)`)

// convertLinks converts [label](url) → <url|label>.
// The angle brackets are Slack link syntax; they are safe here because
// htmlEscape already ran on the original text (which contained no < or >
// that were Slack-syntax — only prose angle brackets).
func convertLinks(s string) string {
	return reLink.ReplaceAllStringFunc(s, func(match string) string {
		sub := reLink.FindStringSubmatch(match)
		if len(sub) != 3 {
			return match
		}
		label, url := sub[1], sub[2]
		return fmt.Sprintf("<%s|%s>", url, label)
	})
}

// reBold matches **text** non-greedily, not crossing newlines, with the
// additional constraint that the leading ** is not preceded by * and the
// trailing ** is not followed by *. Since Go regexp lacks lookarounds, we
// protect triple-asterisk sequences with a placeholder before matching.
var reBold = regexp.MustCompile(`\*\*([^*\n]+?)\*\*`)

// reBoldTriple matches *** (3 or more asterisks) to protect them from
// the double-asterisk bold-collapse pass.
var reBoldTriple = regexp.MustCompile(`\*{3,}`)

// collapseBold converts **x** → *x*, but does NOT partially match ***x***.
// Strategy: replace sequences of 3+ asterisks with a placeholder, apply the
// ** → * substitution, then restore the placeholders.
func collapseBold(s string) string {
	// Step 1: protect triple+ asterisk sequences.
	var triples []string
	ph := func(i int) string { return fmt.Sprintf("\x00KMBOLD_%d_KMBOLD\x00", i) }
	s = reBoldTriple.ReplaceAllStringFunc(s, func(m string) string {
		idx := len(triples)
		triples = append(triples, m)
		return ph(idx)
	})
	// Step 2: apply ** → * on the now-safe text.
	s = reBold.ReplaceAllString(s, "*$1*")
	// Step 3: restore triple+ sequences.
	for i := len(triples) - 1; i >= 0; i-- {
		s = strings.ReplaceAll(s, ph(i), triples[i])
	}
	return s
}

// reStrike matches ~~text~~ non-greedily, not crossing newlines.
var reStrike = regexp.MustCompile(`~~([^~\n]+?)~~`)

// reStrikeTriple matches ~~~ (3 or more tildes) to protect them from the
// double-tilde strike-collapse pass (same idempotence pattern as collapseBold).
var reStrikeTriple = regexp.MustCompile(`~{3,}`)

// collapseStrike converts ~~x~~ → ~x~, but does NOT partially match ~~~x~~~.
func collapseStrike(s string) string {
	// Protect triple+ tilde sequences.
	var triples []string
	ph := func(i int) string { return fmt.Sprintf("\x00KMSTRIKE_%d_KMSTRIKE\x00", i) }
	s = reStrikeTriple.ReplaceAllStringFunc(s, func(m string) string {
		idx := len(triples)
		triples = append(triples, m)
		return ph(idx)
	})
	s = reStrike.ReplaceAllString(s, "~$1~")
	for i := len(triples) - 1; i >= 0; i-- {
		s = strings.ReplaceAll(s, ph(i), triples[i])
	}
	return s
}

// reHeading matches ATX headings at line start: # / ## / ### followed by space.
var reHeading = regexp.MustCompile(`(?m)^(#{1,3}) +(.+)$`)

// mapHeadings converts # H1 / ## H2 / ### H3 at line start to *H1* / *H2* / *H3*.
// Lines without a space after # (like #hashtag) are NOT treated as headings.
func mapHeadings(s string) string {
	return reHeading.ReplaceAllStringFunc(s, func(match string) string {
		sub := reHeading.FindStringSubmatch(match)
		if len(sub) != 3 {
			return match
		}
		return "*" + strings.TrimRight(sub[2], " \t") + "*"
	})
}

// reHRule matches horizontal rules on their own line: ---, ***, or ___ (with optional spaces).
var reHRule = regexp.MustCompile(`(?m)^\s*(---|\*\*\*|___)\s*$\n?`)

// dropHRules removes horizontal rule lines entirely.
// We remove the trailing newline as well to avoid leaving double blank lines.
func dropHRules(s string) string {
	return reHRule.ReplaceAllString(s, "")
}

// rePipeLine matches a line containing at least one pipe (| ... |) with optional
// leading/trailing whitespace. Used to detect pipe-table runs.
var rePipeLine = regexp.MustCompile(`^\s*\|.*\|\s*$`)

// reToolLine matches the Phase 68 tool-one-liner prefix so we can exclude
// tool output lines from the pipe-table heuristic.
var reToolLine = regexp.MustCompile(`^🔧 `)

// fencePipeTables detects runs of ≥2 consecutive pipe-lines (excluding tool
// lines) and wraps each run in triple-backtick fences for monospace alignment.
// Solo single-line matches are left unchanged to avoid false positives on
// bullet text containing pipes.
func fencePipeTables(s string) string {
	lines := strings.Split(s, "\n")
	// Track which lines are part of a pipe-table run.
	// A "run" is ≥2 consecutive pipe lines (non-tool).
	type run struct{ start, end int }
	var runs []run

	i := 0
	for i < len(lines) {
		if isPipeLine(lines[i]) {
			// Start of a potential run
			start := i
			for i < len(lines) && isPipeLine(lines[i]) {
				i++
			}
			end := i // exclusive
			if end-start >= 2 {
				runs = append(runs, run{start, end})
			}
		} else {
			i++
		}
	}

	if len(runs) == 0 {
		return s
	}

	// Rebuild lines, inserting fences around runs. Each run is reflowed into a
	// column-aligned grid (reflowTable) so the table reads correctly in Slack's
	// monospace code block instead of as ragged literal pipes.
	var out []string
	runIdx := 0
	for lineIdx := 0; lineIdx < len(lines); lineIdx++ {
		if runIdx < len(runs) && lineIdx == runs[runIdx].start {
			out = append(out, "```")
			out = append(out, reflowTable(lines[runs[runIdx].start:runs[runIdx].end])...)
			out = append(out, "```")
			lineIdx = runs[runIdx].end - 1 // will be incremented by loop
			runIdx++
		} else {
			out = append(out, lines[lineIdx])
		}
	}
	return strings.Join(out, "\n")
}

// reSepCell matches a GFM table separator cell: dashes with optional alignment
// colons (e.g. `---`, `:--`, `:-:`, `--:`).
var reSepCell = regexp.MustCompile(`^:?-+:?$`)

// reflowTable rewrites a run of pipe-table lines into a column-aligned monospace
// grid: cells are padded to the widest value in their column, the GFM separator
// row (`|---|---|`) is reflowed into a width-matched rule, and ragged rows are
// padded out to the column count. The result is what renders cleanly inside a
// Slack ``` code block. Cell text is preserved verbatim (backticks and all) —
// only surrounding whitespace is normalised.
func reflowTable(lines []string) []string {
	type prow struct {
		cells []string
		isSep bool
	}
	rows := make([]prow, 0, len(lines))
	maxCols := 0
	for _, ln := range lines {
		cells := splitTableRow(ln)
		if len(cells) > maxCols {
			maxCols = len(cells)
		}
		rows = append(rows, prow{cells: cells, isSep: isSeparatorRow(cells)})
	}

	// Column widths come from data rows only — separator rows don't constrain width.
	widths := make([]int, maxCols)
	for _, r := range rows {
		if r.isSep {
			continue
		}
		for i, c := range r.cells {
			if w := utf8.RuneCountInString(c); w > widths[i] {
				widths[i] = w
			}
		}
	}

	out := make([]string, 0, len(rows))
	for _, r := range rows {
		var sb strings.Builder
		sb.WriteByte('|')
		for i := 0; i < maxCols; i++ {
			if r.isSep {
				sb.WriteByte(' ')
				sb.WriteString(strings.Repeat("-", widths[i]))
				sb.WriteString(" |")
				continue
			}
			var cell string
			if i < len(r.cells) {
				cell = r.cells[i]
			}
			pad := widths[i] - utf8.RuneCountInString(cell)
			if pad < 0 {
				pad = 0
			}
			sb.WriteByte(' ')
			sb.WriteString(cell)
			sb.WriteString(strings.Repeat(" ", pad))
			sb.WriteString(" |")
		}
		out = append(out, sb.String())
	}
	return out
}

// splitTableRow parses one pipe-table line into trimmed cells, stripping the
// outer pipes and honouring escaped `\|` as literal pipe characters within a cell.
func splitTableRow(line string) []string {
	const ph = "\x00KMPIPE\x00"
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")
	s = strings.ReplaceAll(s, `\|`, ph)
	parts := strings.Split(s, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(strings.ReplaceAll(p, ph, "|"))
	}
	return cells
}

// isSeparatorRow reports whether every cell is a GFM separator cell (dashes with
// optional alignment colons), i.e. the `|---|---|` rule under a table header.
func isSeparatorRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if !reSepCell.MatchString(c) {
			return false
		}
	}
	return true
}

// isPipeLine returns true if a line looks like a pipe-table row and is NOT
// a tool one-liner.
func isPipeLine(line string) bool {
	if reToolLine.MatchString(line) {
		return false
	}
	return rePipeLine.MatchString(line)
}
