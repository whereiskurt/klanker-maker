package slack

import "strings"

// ExplainBridgeError maps a known bridge error code to an actionable remediation
// string, or returns "" for codes with no special guidance (so callers fall through
// to their existing raw-error wording).
//
// code may be either a bare token (the `error` field of a non-OK PostResponse, e.g.
// "unknown_sender") or a raw 4xx response body that *contains* the token (e.g.
// `{"error":"unknown_sender","ok":false}`) — both are matched.
//
// resourcePrefix names the install's identities table in the message
// ("{prefix}-identities"); pass "" when the prefix is unknown (lower-level callers like
// pkg/slack/client.go) and the generic "{prefix}-identities" placeholder is used.
//
// The raw token is always preserved in the returned string for greppability. Centralized
// here so `km slack test`, `km doctor`, and the shared client wrap share one wording.
func ExplainBridgeError(code, resourcePrefix string) string {
	if strings.Contains(code, "unknown_sender") {
		table := "{prefix}-identities"
		if resourcePrefix != "" {
			table = resourcePrefix + "-identities"
		}
		return "unknown_sender — the operator public-key row is missing from " + table +
			". Re-run 'km init' (idempotent) to republish it, or run 'km doctor --republish-operator-identity'."
	}
	return ""
}
