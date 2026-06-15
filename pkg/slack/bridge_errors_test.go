package slack_test

import (
	"strings"
	"testing"

	kmslack "github.com/whereiskurt/klanker-maker/pkg/slack"
)

// TestExplainBridgeError_UnknownSender asserts the unknown_sender code maps to an
// actionable remediation that names the install's identities table, points at the two
// fixes (km init / km doctor --republish-operator-identity), and retains the raw token
// for greppability.
func TestExplainBridgeError_UnknownSender(t *testing.T) {
	got := kmslack.ExplainBridgeError("unknown_sender", "sec")
	if got == "" {
		t.Fatal("ExplainBridgeError(unknown_sender) returned empty; want a remediation message")
	}
	for _, want := range []string{"unknown_sender", "sec-identities", "km init", "km doctor --republish-operator-identity"} {
		if !strings.Contains(got, want) {
			t.Errorf("ExplainBridgeError(unknown_sender,sec) = %q; missing %q", got, want)
		}
	}
}

// TestExplainBridgeError_RawBody asserts the code may be a raw 4xx body that *contains*
// the token, and that an empty prefix falls back to the "{prefix}-identities" placeholder.
func TestExplainBridgeError_RawBody(t *testing.T) {
	got := kmslack.ExplainBridgeError(`{"error":"unknown_sender","ok":false}`, "")
	if !strings.Contains(got, "unknown_sender") || !strings.Contains(got, "{prefix}-identities") {
		t.Errorf("ExplainBridgeError(raw body, \"\") = %q; want token + {prefix}-identities placeholder", got)
	}
}

// TestExplainBridgeError_Passthrough asserts an unrelated code yields "" so callers fall
// through to their existing raw-error wording unchanged.
func TestExplainBridgeError_Passthrough(t *testing.T) {
	if got := kmslack.ExplainBridgeError("rate_limited", "km"); got != "" {
		t.Errorf("ExplainBridgeError(rate_limited) = %q; want \"\" (passthrough)", got)
	}
}
