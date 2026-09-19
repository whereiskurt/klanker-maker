package slack

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseMentions_NormalisesEveryAcceptedSpelling(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"bare user id", []string{"U0KURT"}, []string{"<@U0KURT>"}},
		{"enterprise id", []string{"W012ABC"}, []string{"<@W012ABC>"}},
		{"at-prefixed", []string{"@U0KURT"}, []string{"<@U0KURT>"}},
		{"already a token", []string{"<@U0KURT>"}, []string{"<@U0KURT>"}},
		{"here", []string{"here"}, []string{"<!here>"}},
		{"at-here", []string{"@here"}, []string{"<!here>"}},
		{"channel", []string{"channel"}, []string{"<!channel>"}},
		{"comma list in one flag", []string{"U1,U2"}, []string{"<@U1>", "<@U2>"}},
		{"repeated flag", []string{"U1", "U2"}, []string{"<@U1>", "<@U2>"}},
		{"whitespace and blanks tolerated", []string{" U1 , ,U2 "}, []string{"<@U1>", "<@U2>"}},
		{"dedup keeps first", []string{"U1,U2,U1"}, []string{"<@U1>", "<@U2>"}},
		{"lowercase id is uppercased", []string{"u0kurt"}, []string{"<@U0KURT>"}},
		{"empty", nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseMentions(tc.in)
			if err != nil {
				t.Fatalf("ParseMentions(%q): %v", tc.in, err)
			}
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("ParseMentions(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A display name is NOT a mention. km never enumerates the workspace directory
// (docs/slack-notifications.md scope table), so there is nothing to resolve
// "Ron" against — refusing loudly beats posting the literal text "@Ron".
func TestParseMentions_RejectsNamesAndGarbage(t *testing.T) {
	for _, in := range []string{"Ron", "@Ron", "kurt@example.com", "<!subteam^S123>", "S0123", "C0123", "<@>", "everyone"} {
		if _, err := ParseMentions([]string{in}); err == nil {
			t.Errorf("ParseMentions(%q) accepted; want error", in)
		}
	}
}

func TestMentionLine(t *testing.T) {
	if got := MentionLine([]string{"<@U1>", "<!here>"}); got != "<@U1> <!here>" {
		t.Errorf("MentionLine = %q", got)
	}
	if got := MentionLine(nil); got != "" {
		t.Errorf("MentionLine(nil) = %q; want empty", got)
	}
}

func TestPrependMentionBlock(t *testing.T) {
	in := `[{"type":"markdown","text":"hello"}]`
	out, ok := PrependMentionBlock(in, "<@U1> <@U2>")
	if !ok {
		t.Fatal("PrependMentionBlock returned ok=false on a 1-block payload")
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(out), &blocks); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, out)
	}
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %d: %s", len(blocks), out)
	}
	// The mention must live in a mrkdwn text object — the one Block Kit element
	// Slack documents as resolving <@U…>. A markdown block is NOT that element.
	first := blocks[0]
	if first["type"] != "section" {
		t.Errorf("first block type = %v; want section", first["type"])
	}
	text, _ := first["text"].(map[string]any)
	if text["type"] != "mrkdwn" || text["text"] != "<@U1> <@U2>" {
		t.Errorf("first block text = %v; want mrkdwn <@U1> <@U2>", text)
	}
	if blocks[1]["text"] != "hello" {
		t.Errorf("original block not preserved: %v", blocks[1])
	}
}

func TestPrependMentionBlock_RefusesToBreachBlockCap(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < maxBlocks; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"type":"divider"}`)
	}
	sb.WriteString("]")
	if _, ok := PrependMentionBlock(sb.String(), "<@U1>"); ok {
		t.Error("PrependMentionBlock ok=true on a 50-block payload; would be rejected by Slack as invalid_blocks")
	}
	if _, ok := PrependMentionBlock("not json", "<@U1>"); ok {
		t.Error("PrependMentionBlock ok=true on malformed input")
	}
}
