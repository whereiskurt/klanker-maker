package cmd_test

// slack_manifest_test.go — Phase 72 Wave 0 stubs for km slack manifest.
// Plan 72-03 (Wave 1) flips the t.Skip calls to real assertions and implements
// cmd.RenderSlackManifest(w io.Writer, data slackManifestData) error.
//
// Golden fixture inputs (defined here so Plan 72-03 knows what to assert):
//   AppName  = "KlankerMaker-test"
//   EventsURL = "https://example.lambda-url.us-east-1.on.aws/events"
//
// When Wave 1 lands:
//   - Add cmd.RenderSlackManifest (reads //go:embed slack_manifest_template.json)
//   - Replace each t.Skip with assertions against the golden fixture in testdata/

import (
	"testing"
)

// TestSlackManifest_Golden — calls cmd.RenderSlackManifest for known inputs and
// bytes.Equal-compares output against testdata/slack_manifest_golden.json.
// Wave 1 assertion: output matches golden file byte-for-byte.
func TestSlackManifest_Golden(t *testing.T) {
	t.Skip("TODO Wave 1: implement km slack manifest in 72-03")
}

// TestSlackManifest_AppNameOverride — uses --app-name=Custom and asserts the
// rendered output contains "name": "Custom".
// Wave 1 assertion: strings.Contains(rendered, `"name": "Custom"`).
func TestSlackManifest_AppNameOverride(t *testing.T) {
	t.Skip("TODO Wave 1: implement km slack manifest in 72-03")
}

// TestSlackManifest_BridgeURLFromSSM — fake SSM store returns
// https://x.lambda-url.us-east-1.on.aws for {prefix}slack/bridge-url.
// Wave 1 assertion: rendered request_url ends with /events.
func TestSlackManifest_BridgeURLFromSSM(t *testing.T) {
	t.Skip("TODO Wave 1: implement km slack manifest in 72-03")
}

// TestSlackManifest_ScopesIncludeUsersReadEmail — asserts users:read.email appears
// in the rendered output (and files:read is also present for Phase 75 inbound).
// Wave 1 assertion: strings.Contains(rendered, "users:read.email") &&
//                   strings.Contains(rendered, "files:read").
func TestSlackManifest_ScopesIncludeUsersReadEmail(t *testing.T) {
	t.Skip("TODO Wave 1: implement km slack manifest in 72-03")
}
