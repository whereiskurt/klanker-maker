package slack

import "testing"

// Slack resolves a mention only from the literal <@U…> / <!here> token. Before
// this test, htmlEscape turned every such token into &lt;@U…&gt;, so an agent
// reply that correctly wrote <@U0KURT> rendered as the literal text "<@U0KURT>"
// in Slack and pinged nobody. The preserve-list is deliberately narrow: only the
// exact mention grammar survives, so "<script>" and other angle-bracketed text
// still escape.
func TestMrkdwnify_PreservesMentionTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"user mention", "thanks <@U0KURT> done", "thanks <@U0KURT> done"},
		{"enterprise W id", "cc <@W012ABC>", "cc <@W012ABC>"},
		{"here", "<!here> deploy finished", "<!here> deploy finished"},
		{"channel", "<!channel> heads up", "<!channel> heads up"},
		{"channel link", "see <#C0123ABC>", "see <#C0123ABC>"},
		{"channel link with name", "see <#C0123ABC|general>", "see <#C0123ABC|general>"},
		{"two mentions", "<@U1> and <@U2>", "<@U1> and <@U2>"},
		{"lowercase id is not a mention", "<@u0kurt>", "&lt;@u0kurt&gt;"},
		{"bare at is untouched text", "@U0KURT", "@U0KURT"},
		{"script still escapes", "<script>alert(1)</script> <@U1>", "&lt;script&gt;alert(1)&lt;/script&gt; <@U1>"},
		{"unknown bang command escapes", "<!subteam^S123>", "&lt;!subteam^S123&gt;"},
		{"idempotent", "<@U1> &lt;x&gt;", "<@U1> &lt;x&gt;"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Mrkdwnify(tc.input)
			if got != tc.want {
				t.Errorf("Mrkdwnify(%q) = %q; want %q", tc.input, got, tc.want)
			}
			if again := Mrkdwnify(got); again != got {
				t.Errorf("not idempotent: Mrkdwnify(%q) = %q", got, again)
			}
		})
	}
}
