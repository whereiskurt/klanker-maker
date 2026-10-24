package slack_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// TestNormalizeEmojiName_Accepts: the names a status caller uses, with or
// without the colons Slack renders them with, come back bare.
func TestNormalizeEmojiName_Accepts(t *testing.T) {
	cases := map[string]string{
		"white_check_mark":       "white_check_mark",
		":rotating_light:":       "rotating_light",
		"+1":                     "+1",
		":grey_question:":        "grey_question",
		"hourglass_flowing_sand": "hourglass_flowing_sand",
		"a-b'c":                  "a-b'c",
	}
	for in, want := range cases {
		got, err := slack.NormalizeEmojiName(in)
		if err != nil {
			t.Errorf("NormalizeEmojiName(%q): unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeEmojiName(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestNormalizeEmojiName_Rejects: anything outside ^[a-z0-9_+'-]{1,100}$ is an
// error, so a signed request can never carry a name Slack would reject or a
// token that is not an emoji name at all.
func TestNormalizeEmojiName_Rejects(t *testing.T) {
	bad := []string{
		"",
		"::",
		":rotating light:",
		"Rotating_Light",
		"white_check_mark::skin-tone-2",
		strings.Repeat("a", 101),
		"<@U123>",
	}
	for _, in := range bad {
		if _, err := slack.NormalizeEmojiName(in); err == nil {
			t.Errorf("NormalizeEmojiName(%q): expected error, got nil", in)
		}
	}
	// Exactly 100 characters is the upper bound and is accepted.
	if _, err := slack.NormalizeEmojiName(strings.Repeat("a", 100)); err != nil {
		t.Errorf("100-char name should be accepted: %v", err)
	}
}

// TestCanonicalJSON_ReactFieldsOmittedWhenZero: the four new fields are
// omitempty. The bridge re-canonicalises the struct before verifying, so a field
// that serialised when zero would change the signed bytes of every envelope an
// older km-slack produces. This pins that the zero envelope is unchanged.
func TestCanonicalJSON_ReactFieldsOmittedWhenZero(t *testing.T) {
	env := makeFixedEnvelope()
	b, err := slack.CanonicalJSON(env)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	for _, f := range []string{`"emoji"`, `"remove"`, `"exclusive"`, `"broadcast"`, `"oldest"`, `"latest"`, `"before"`, `"grep"`, `"limit"`} {
		if strings.Contains(string(b), f) {
			t.Errorf("zero-valued %s must not serialise: %s", f, b)
		}
	}
}

// TestCanonicalJSON_ReactFieldsPresentWhenSet: and when set they are signed.
func TestCanonicalJSON_ReactFieldsPresentWhenSet(t *testing.T) {
	env := makeFixedEnvelope()
	env.Action = slack.ActionReact
	env.MessageTS = "1714280400.000100"
	env.Emoji = "white_check_mark"
	env.Remove = true
	env.Exclusive = []string{"hourglass_flowing_sand", "rotating_light"}
	env.Broadcast = true
	b, err := slack.CanonicalJSON(env)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		`"action":"react"`,
		`"emoji":"white_check_mark"`,
		`"remove":true`,
		`"exclusive":["hourglass_flowing_sand","rotating_light"]`,
		`"broadcast":true`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("canonical JSON missing %s: %s", want, s)
		}
	}
}

func TestMaxExclusiveReactions_IsTen(t *testing.T) {
	if slack.MaxExclusiveReactions != 10 {
		t.Errorf("MaxExclusiveReactions = %d; the design caps a signed react at 10 Slack calls", slack.MaxExclusiveReactions)
	}
}
