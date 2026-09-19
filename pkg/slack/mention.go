package slack

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Mentions: turning "@ this person" into something Slack actually resolves.
//
// Slack notifies a person only when the message carries the literal token
// <@U…> (or <!here>/<!channel> for a broadcast). "@U0KURT", "@Kurt" and a
// display name are plain text to Slack — they render as typed and ping nobody.
// That is the failure an agent hits when it "at-mentions" someone by writing
// the name into its reply. ParseMentions accepts every spelling an agent is
// likely to produce for an ID it already holds and normalises it to the token
// form; MentionLine/PrependMentionBlock put the tokens where Slack resolves
// them in each render mode.
//
// Names are deliberately NOT accepted. km never enumerates the workspace
// directory (docs/slack-notifications.md, scope table — users:read exists
// only to resolve an operator-supplied email for invites), so there is nothing
// to resolve "Ron" against. The IDs an agent holds come from two places: the
// `[Slack] From: <@U…>` line the inbound poller prepends to every turn, and
// any <@U…> the human typed into the message itself (the bridge forwards the
// raw event text). Refusing a name loudly beats posting the literal "@Ron".

var reMentionUserID = regexp.MustCompile(`^[UW][A-Z0-9]+$`)

// ParseMentions normalises --mention values into Slack mention tokens.
// Each input may be a comma-separated list; the flag may also be repeated.
// Accepted spellings per entry (case-insensitive, whitespace-trimmed):
//
//	U0KURT   @U0KURT   <@U0KURT>     → <@U0KURT>   (W… enterprise ids likewise)
//	here     @here     <!here>       → <!here>
//	channel  @channel  <!channel>    → <!channel>
//
// Duplicates are dropped (first occurrence kept). Anything else — a display
// name, an email, a usergroup, a channel id — is an error naming the entry.
func ParseMentions(values []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, v := range values {
		for _, raw := range strings.Split(v, ",") {
			entry := strings.TrimSpace(raw)
			if entry == "" {
				continue
			}
			tok, err := parseMention(entry)
			if err != nil {
				return nil, err
			}
			if seen[tok] {
				continue
			}
			seen[tok] = true
			out = append(out, tok)
		}
	}
	return out, nil
}

func parseMention(entry string) (string, error) {
	s := strings.TrimSpace(entry)
	// Strip an already-tokenised wrapper: <@U1> / <!here>.
	if strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") {
		s = s[1 : len(s)-1]
	}
	// Strip a leading @ or ! marker.
	s = strings.TrimLeft(s, "@!")
	switch strings.ToLower(s) {
	case "here":
		return "<!here>", nil
	case "channel":
		return "<!channel>", nil
	}
	id := strings.ToUpper(s)
	if reMentionUserID.MatchString(id) {
		return "<@" + id + ">", nil
	}
	return "", fmt.Errorf("mention %q is not a Slack user id (U…/W…), here, or channel — km cannot resolve names; use the id from the [Slack] From: line or a <@U…> token in the incoming message", entry)
}

// MentionLine joins mention tokens into the one-line preamble that is
// prepended to a post's text. Empty input yields "".
func MentionLine(tokens []string) string {
	return strings.Join(tokens, " ")
}

// PrependMentionBlock inserts a section block carrying line (as mrkdwn) at the
// head of a Block Kit JSON array. A section's mrkdwn text object is the Block
// Kit element Slack documents as resolving <@U…>; the newer markdown block is
// not, so a mention inside a blocks-rich prose block cannot be relied on.
//
// Returns ok=false when the array cannot be parsed or already holds maxBlocks
// entries — adding one more would be rejected by Slack as invalid_blocks and
// drop the whole message. The caller then posts without blocks; the mention
// line is already in the plain text, which is also what Slack uses for the
// notification when blocks are present.
func PrependMentionBlock(blocksJSON, line string) (string, bool) {
	var blocks []json.RawMessage
	if err := json.Unmarshal([]byte(blocksJSON), &blocks); err != nil {
		return "", false
	}
	if len(blocks) >= maxBlocks {
		return "", false
	}
	head, err := json.Marshal(blockSection{
		Type: "section",
		Text: mrkdwnField{Type: "mrkdwn", Text: line},
	})
	if err != nil {
		return "", false
	}
	out, err := json.Marshal(append([]json.RawMessage{head}, blocks...))
	if err != nil {
		return "", false
	}
	return string(out), true
}
