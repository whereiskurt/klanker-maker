package compiler

import (
	"os/exec"
	"strings"
	"testing"
)

// TestPoller_PrefixParser_TableDriven covers the locked grammar
// `^([Cc][Ll][Aa][Uu][Dd][Ee]|[Cc][Oo][Dd][Ee][Xx]):[[:space:]]?` across 11 representative
// inputs per 70-RESEARCH.md Pitfall 4. Uses a real bash sub-process — not a textual
// grep — because regex correctness must be empirically verified.
// SC-8/SC-9: Plan 70-06 Task 2.
func TestPoller_PrefixParser_TableDriven(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	cases := []struct {
		input         string
		wantMatch     bool
		wantRequested string
		wantStripped  string
	}{
		{"claude: do thing", true, "claude", "do thing"},
		{"codex: do thing", true, "codex", "do thing"},
		{"Claude: do thing", true, "claude", "do thing"},
		{"CODEX: do thing", true, "codex", "do thing"},
		{"claude:do thing", true, "claude", "do thing"},
		{"claude:  do thing", true, "claude", " do thing"}, // only first space stripped
		{"what does claude: mean?", false, "", "what does claude: mean?"},
		{"  claude: do thing", false, "", "  claude: do thing"}, // leading ws → no match
		{"claude :do thing", false, "", "claude :do thing"},     // space before colon → no match
		{"claudespecial: thing", false, "", "claudespecial: thing"},
		{"hi", false, "", "hi"},
	}
	// Note: ${var,,} (bash 4+ lowercase expansion) is used in the production poller
	// which runs on EC2 with bash 4+. For test portability (macOS bash 3.2),
	// the empirical sub-script uses 'tr' for case folding — same runtime semantics.
	script := `
TEXT="$1"
REQUESTED_AGENT=""
STRIPPED_TEXT="$TEXT"
if [[ "$TEXT" =~ ^([Cc][Ll][Aa][Uu][Dd][Ee]|[Cc][Oo][Dd][Ee][Xx]):[[:space:]]? ]]; then
  PREFIX=$(echo "${BASH_REMATCH[1]}" | tr '[:upper:]' '[:lower:]')
  REQUESTED_AGENT="$PREFIX"
  STRIPPED_TEXT="${TEXT#*:}"
  STRIPPED_TEXT="${STRIPPED_TEXT# }"
fi
printf '%s|%s\n' "$REQUESTED_AGENT" "$STRIPPED_TEXT"
`
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", script, "_bash_arg0", tc.input)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("bash sub-process failed: %v", err)
			}
			line := strings.TrimRight(string(out), "\n")
			parts := strings.SplitN(line, "|", 2)
			if len(parts) != 2 {
				t.Fatalf("malformed bash output: %q", line)
			}
			gotRequested, gotStripped := parts[0], parts[1]
			if tc.wantMatch && gotRequested != tc.wantRequested {
				t.Errorf("requested_agent: got %q, want %q", gotRequested, tc.wantRequested)
			}
			if !tc.wantMatch && gotRequested != "" {
				t.Errorf("expected NO match for %q, but got requested_agent=%q", tc.input, gotRequested)
			}
			if gotStripped != tc.wantStripped {
				t.Errorf("stripped_text: got %q, want %q", gotStripped, tc.wantStripped)
			}
		})
	}
}

// TestPoller_PrefixParser_AnchoredAtStart guards Pitfall 4: the regex must be
// anchored at ^ so a Slack message asking "what does claude: mean?" does NOT
// trigger routing. Tests both textual (anchor present) and empirical (bash sub-process).
// SC-8/SC-9: Plan 70-06 Task 2.
func TestPoller_PrefixParser_AnchoredAtStart(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	p := minimalSlackInboundProfile(t, true)
	ud := compileInboundUserData(t, p)
	poller := extractSlackInboundPoller(t, ud)

	// Textual: anchor must be present in the compiled heredoc.
	if !strings.Contains(poller, `[[ "$TEXT" =~ ^([Cc][Ll]`) {
		t.Errorf("poller regex missing ^ anchor — mid-sentence claude: would incorrectly match")
	}

	// Empirical: the pathological case "what does claude: mean?" must NOT match.
	script := `
TEXT="$1"
if [[ "$TEXT" =~ ^([Cc][Ll][Aa][Uu][Dd][Ee]|[Cc][Oo][Dd][Ee][Xx]):[[:space:]]? ]]; then
  echo MATCH
else
  echo NOMATCH
fi
`
	cmd := exec.Command("bash", "-c", script, "_bash_arg0", "what does claude: mean?")
	out, _ := cmd.Output()
	if !strings.Contains(string(out), "NOMATCH") {
		t.Errorf("mid-sentence claude: incorrectly matched; output=%q", out)
	}
}

// TestPoller_TopLevelPrefix_FreshThread (SC-8): on a fresh top-level message
// (no CLAUDE_SESSION / DDB row yet), a matching prefix overrides EFFECTIVE_AGENT
// to the requested agent for that thread; the profile's KM_AGENT on disk is unchanged.
// The non-switch branch must assign EFFECTIVE_AGENT="$REQUESTED_AGENT" and strip TEXT.
// Plan 70-06 Task 2.
func TestPoller_TopLevelPrefix_FreshThread(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	ud := compileInboundUserData(t, p)
	poller := extractSlackInboundPoller(t, ud)

	// The non-switch branch must assign EFFECTIVE_AGENT from REQUESTED_AGENT
	// (covers both: fresh-thread prefix selection AND same-agent no-op).
	if !strings.Contains(poller, `EFFECTIVE_AGENT="$REQUESTED_AGENT"`) {
		t.Errorf("poller missing fresh-thread override EFFECTIVE_AGENT=$REQUESTED_AGENT")
	}
	// The prefix must be stripped from TEXT in the non-switch path.
	if !strings.Contains(poller, `TEXT="$STRIPPED_TEXT"`) {
		t.Errorf("poller missing prefix strip TEXT=$STRIPPED_TEXT")
	}
}

// TestPoller_SameAgentPrefix_NoOp (SC-9): `claude: ...` inside an existing
// claude-rooted thread must strip the prefix and dispatch the SAME agent in the
// SAME thread. DO_SWITCH=1 must be guarded by REQUESTED_AGENT != CURRENT_AGENT AND
// non-empty CLAUDE_SESSION. The no-op path must NOT rewrite THREAD_TS.
// Plan 70-06 Task 2.
func TestPoller_SameAgentPrefix_NoOp(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	ud := compileInboundUserData(t, p)
	poller := extractSlackInboundPoller(t, ud)

	// DO_SWITCH=1 must only appear when REQUESTED_AGENT != CURRENT_AGENT.
	if !strings.Contains(poller, `[ "$REQUESTED_AGENT" != "$CURRENT_AGENT" ]`) {
		t.Errorf("poller missing same-agent guard (REQUESTED != CURRENT) before DO_SWITCH=1")
	}

	// THREAD_TS="$NEW_TOP_TS" is the cross-agent switch rewrite — it must appear at
	// most once (inside the DO_SWITCH=1 block only). The same-agent path never rewrites it.
	if strings.Count(poller, `THREAD_TS="$NEW_TOP_TS"`) > 1 {
		t.Errorf("THREAD_TS=$NEW_TOP_TS appears multiple times — same-agent path may be incorrectly rewriting it")
	}
}

// extractCrossAgentBlock returns the substring of the poller between
// `if [ "$DO_SWITCH" -eq 1 ]; then` and the matching closing `fi`.
// Uses a depth counter to handle nested if/fi correctly.
// Used by Plan 70-06 Task 3 tests to scope assertions to the cross-agent block.
func extractCrossAgentBlock(ud string) string {
	start := strings.Index(ud, `if [ "$DO_SWITCH" -eq 1 ]; then`)
	if start < 0 {
		return ""
	}
	rest := ud[start:]
	depth := 0
	i := 0
	for i < len(rest) {
		// Detect 'if ' at a word boundary (preceded by newline, space, semicolon, or start).
		if strings.HasPrefix(rest[i:], "if ") && (i == 0 || isWordBoundary(rest[i-1])) {
			depth++
			i += 3
			continue
		}
		// Detect 'fi' followed by newline, space, semicolon, or end.
		if strings.HasPrefix(rest[i:], "fi") {
			after := i + 2
			if after >= len(rest) || rest[after] == '\n' || rest[after] == ' ' || rest[after] == ';' || rest[after] == '\t' {
				depth--
				if depth == 0 {
					return rest[:after]
				}
			}
		}
		i++
	}
	// If no matching fi found, return the whole rest.
	return rest
}

func isWordBoundary(c byte) bool {
	return c == '\n' || c == ' ' || c == '\t' || c == ';' || c == '&' || c == '|'
}

// TestPoller_CrossAgentSwitch_OrderingFetchesOldPermalinkFirst (SC-10): the
// cross-agent switch block must call km-slack permalink (for the OLD thread,
// with the already-known THREAD_TS) BEFORE km-slack post --new-message. The new
// top-level body contains "Continuing from $OLD_PERMALINK" at post-time —
// no placeholder is ever posted to Slack; no chat.update in the critical path.
// Plan 70-06 Task 3.
func TestPoller_CrossAgentSwitch_OrderingFetchesOldPermalinkFirst(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	ud := compileInboundUserData(t, p)
	poller := extractSlackInboundPoller(t, ud)

	block := extractCrossAgentBlock(poller)
	if block == "" {
		t.Fatalf("cross-agent block (if [ \"$DO_SWITCH\" -eq 1 ]) not found in poller")
	}

	// The OLD permalink fetch (uses --ts "$THREAD_TS") MUST appear before
	// the km-slack post --new-message call.
	oldPermalinkIdx := strings.Index(block, "km-slack permalink")
	newMessageIdx := strings.Index(block, "--new-message")
	if oldPermalinkIdx < 0 {
		t.Fatalf("cross-agent block missing km-slack permalink call")
	}
	if newMessageIdx < 0 {
		t.Fatalf("cross-agent block missing --new-message flag")
	}
	if oldPermalinkIdx > newMessageIdx {
		t.Errorf("ordering violation: km-slack permalink at %d appears AFTER --new-message at %d (OLD permalink must be fetched first so it can be embedded in the new top-level body)", oldPermalinkIdx, newMessageIdx)
	}

	// The first permalink call must reference $THREAD_TS (OLD thread), not $NEW_TOP_TS.
	firstPermalinkRegion := block[oldPermalinkIdx:]
	// Capture enough context for the multi-line invocation.
	snippet := firstPermalinkRegion
	if len(snippet) > 300 {
		snippet = snippet[:300]
	}
	if !strings.Contains(snippet, `--ts "$THREAD_TS"`) {
		t.Errorf("first km-slack permalink call must reference --ts \"$THREAD_TS\" (OLD thread); snippet: %q", snippet)
	}

	// The new top-level body builder must reference $OLD_PERMALINK —
	// the value is substituted at post-time, not via chat.update later.
	if !strings.Contains(block, "Continuing from $OLD_PERMALINK") {
		t.Errorf("cross-agent block missing 'Continuing from $OLD_PERMALINK' in new top-level body — OLD permalink must be embedded at post-time")
	}

	// Guard rail: the v1 placeholder pattern must NOT appear.
	if strings.Contains(block, "<permalink-placeholder>") {
		t.Errorf("cross-agent block still contains <permalink-placeholder> — must use $OLD_PERMALINK substitution at post-time")
	}
}

// TestPoller_CrossAgentSwitch_OldRowUntouched (SC-10): the cross-agent block
// must NOT call aws dynamodb update-item or delete-item on the OLD thread_ts.
// The old DDB row stays intact and resumable. The block must rewrite THREAD_TS,
// EFFECTIVE_AGENT, CLAUDE_SESSION, and PROMPT_FILE so Plan 70-05's dispatch fork
// picks up the new agent as a first turn into the new thread.
// Plan 70-06 Task 3.
func TestPoller_CrossAgentSwitch_OldRowUntouched(t *testing.T) {
	p := minimalSlackInboundProfile(t, true)
	ud := compileInboundUserData(t, p)
	poller := extractSlackInboundPoller(t, ud)

	block := extractCrossAgentBlock(poller)
	if block == "" {
		t.Fatalf("cross-agent block (if [ \"$DO_SWITCH\" -eq 1 ]) not found in poller")
	}

	if strings.Contains(block, "aws dynamodb update-item") {
		t.Errorf("cross-agent block contains 'aws dynamodb update-item' — OLD row must remain unchanged")
	}
	if strings.Contains(block, "aws dynamodb delete-item") {
		t.Errorf("cross-agent block contains 'aws dynamodb delete-item' — OLD row must remain resumable")
	}
	if !strings.Contains(block, `THREAD_TS="$NEW_TOP_TS"`) {
		t.Errorf("cross-agent block missing THREAD_TS=\"$NEW_TOP_TS\" rewrite — new DDB row would key on wrong ts")
	}
	if !strings.Contains(block, `EFFECTIVE_AGENT="$NEW_AGENT"`) {
		t.Errorf("cross-agent block missing EFFECTIVE_AGENT=\"$NEW_AGENT\" rewrite — dispatch would use wrong agent")
	}
	if !strings.Contains(block, `CLAUDE_SESSION=""`) {
		t.Errorf("cross-agent block must reset CLAUDE_SESSION to empty (new agent first turn)")
	}
}
