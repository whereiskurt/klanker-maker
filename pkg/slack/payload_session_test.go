package slack_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// Every field the session action adds is omitempty: an envelope from an older
// km-slack must canonicalise to the same bytes it did before, or its
// signatures stop verifying at the bridge.
func TestCanonicalJSON_SessionFieldsOmittedWhenZero(t *testing.T) {
	env := &slack.SlackEnvelope{Action: slack.ActionPost, Body: "b", Channel: "C1", Nonce: "n", SenderID: "sb-1", Timestamp: 1, Version: 1}
	b, err := slack.CanonicalJSON(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"source", "repo", "number", "report_id", "target", "agent_type", "claim_thread"} {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Fatalf("zero %s serialised (breaks old signers): %s", k, b)
		}
	}
	env.Action = slack.ActionSession
	env.Source, env.Repo, env.Number, env.AgentType, env.SessionID = "github", "o/r", 7, "codex", "sess-1"
	b2, _ := slack.CanonicalJSON(env)
	for _, want := range []string{`"source":"github"`, `"repo":"o/r"`, `"number":7`, `"agent_type":"codex"`, `"session_id":"sess-1"`} {
		if !strings.Contains(string(b2), want) {
			t.Fatalf("missing %s in %s", want, b2)
		}
	}
	env2 := &slack.SlackEnvelope{Action: slack.ActionPost, ClaimThread: true}
	b3, _ := slack.CanonicalJSON(env2)
	if !strings.Contains(string(b3), `"claim_thread":true`) {
		t.Fatalf("claim_thread not serialised when set: %s", b3)
	}
}

func TestActionSessionConstant(t *testing.T) {
	if slack.ActionSession != "session" {
		t.Fatalf("ActionSession = %q", slack.ActionSession)
	}
}
