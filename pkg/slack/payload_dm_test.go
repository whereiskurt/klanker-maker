package slack_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// omitempty on TargetUser is load-bearing: the bridge re-canonicalises before
// verifying, so an envelope from a km-slack that predates the field must
// produce byte-identical canonical JSON on both sides.
func TestCanonicalJSON_TargetUserOmittedWhenEmpty(t *testing.T) {
	env, err := slack.BuildEnvelope(slack.ActionPost, "sb-1", "C1", "", "hi", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := slack.CanonicalJSON(env)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "target_user") {
		t.Fatalf("empty TargetUser must not serialise (breaks old signers): %s", b)
	}
	env.TargetUser = "U0KURT"
	b, _ = slack.CanonicalJSON(env)
	if !strings.Contains(string(b), `"target_user":"U0KURT"`) {
		t.Fatalf("TargetUser not serialised: %s", b)
	}
	if slack.ActionDM != "dm" {
		t.Fatalf("ActionDM = %q", slack.ActionDM)
	}
}
