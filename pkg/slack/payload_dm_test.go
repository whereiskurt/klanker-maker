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

// Pins the canonical wire format of a plain envelope to the bytes it had before
// TargetUser existed. Any later field that serialises when zero breaks this, and
// with it every older km-slack's signatures.
func TestCanonicalJSON_PreDMWireFormatPinned(t *testing.T) {
	const want = `{"action":"post","blocks":"","body":"b","channel":"C1","content_type":"","filename":"","message_ts":"","nonce":"n","s3_key":"","session_id":"","sender_id":"sb-1","size_bytes":0,"subject":"","text":"","thread_ts":"","timestamp":1,"version":1}`
	env := &slack.SlackEnvelope{Action: "post", Body: "b", Channel: "C1", Nonce: "n", SenderID: "sb-1", Timestamp: 1, Version: 1}
	b, err := slack.CanonicalJSON(env)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Fatalf("canonical JSON drifted:\n got %s\nwant %s", b, want)
	}
	if strings.Contains(string(b), "target_user") {
		t.Fatalf("zero TargetUser serialised: %s", b)
	}
	env.TargetUser = "U0KURT"
	b2, _ := slack.CanonicalJSON(env)
	if string(b2) == want {
		t.Fatal("setting TargetUser must change the canonical bytes")
	}
}
