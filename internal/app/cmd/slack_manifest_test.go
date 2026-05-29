package cmd_test

// slack_manifest_test.go — Phase 72 Plan 72-03 implementation tests.
// Golden fixture inputs:
//   AppName   = "KlankerMaker-test"
//   EventsURL = "https://example.lambda-url.us-east-1.on.aws/events"

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// newManifestCfg returns a minimal *config.Config for manifest tests.
// A zero-value Config returns "km" from GetResourcePrefix() (the default).
func newManifestCfg(_ *testing.T) *config.Config {
	return &config.Config{}
}

// TestSlackManifest_Golden calls RenderSlackManifest for known inputs and
// bytes.Equal-compares output against testdata/slack_manifest_golden.json.
func TestSlackManifest_Golden(t *testing.T) {
	var buf bytes.Buffer
	err := cmd.RenderSlackManifest(&buf, cmd.SlackManifestData{
		AppName:   "KlankerMaker-test",
		EventsURL: "https://example.lambda-url.us-east-1.on.aws/events",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want, err := os.ReadFile("testdata/slack_manifest_golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("golden mismatch.\ngot:\n%s\nwant:\n%s", buf.String(), string(want))
	}
}

// TestSlackManifest_AppNameOverride asserts that a custom --app-name value
// appears in both the display_information.name and bot_user.display_name fields.
func TestSlackManifest_AppNameOverride(t *testing.T) {
	var buf bytes.Buffer
	err := cmd.RenderSlackManifest(&buf, cmd.SlackManifestData{AppName: "My Custom App", EventsURL: "https://x/events"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"name": "My Custom App"`) && !strings.Contains(out, `"name":"My Custom App"`) {
		t.Errorf("output missing custom app name in display_information; got:\n%s", out)
	}
	if !strings.Contains(out, `"display_name": "My Custom App"`) && !strings.Contains(out, `"display_name":"My Custom App"`) {
		t.Errorf("output missing custom app name in bot_user.display_name; got:\n%s", out)
	}
}

// TestSlackManifest_BridgeURLFromSSM tests that the bridge URL read from SSM
// is appended with /events and the trailing slash trimmed if present.
func TestSlackManifest_BridgeURLFromSSM(t *testing.T) {
	deps := &cmd.SlackCmdDeps{
		SSM:       newFakeSSM(map[string]string{"/km/slack/bridge-url": "https://abc.lambda-url.us-east-1.on.aws"}),
		SsmPrefix: "/km/",
	}
	cfg := newManifestCfg(t)
	var buf bytes.Buffer
	err := cmd.RunSlackManifest(context.Background(), deps, cfg, cmd.SlackManifestOpts{}, &buf)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(buf.String(), `https://abc.lambda-url.us-east-1.on.aws/events`) {
		t.Errorf("output missing /events URL; got:\n%s", buf.String())
	}
}

// TestSlackManifest_ScopesIncludeUsersReadEmail asserts that the Phase 72 scope
// users:read.email AND the Phase 75 inbound scope files:read are both present.
func TestSlackManifest_ScopesIncludeUsersReadEmail(t *testing.T) {
	var buf bytes.Buffer
	err := cmd.RenderSlackManifest(&buf, cmd.SlackManifestData{AppName: "X", EventsURL: "https://x/events"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"users:read.email"`) {
		t.Errorf("output missing users:read.email scope; got:\n%s", out)
	}
	// files:read is required by Phase 75 (inbound file attachments) and enforced
	// by km doctor's inbound-scope check.
	if !strings.Contains(out, `"files:read"`) {
		t.Errorf("output missing files:read scope (Phase 75 inbound requirement); got:\n%s", out)
	}
}

// TestSlackManifest_OutputIsValidJSON verifies the rendered output is valid JSON.
func TestSlackManifest_OutputIsValidJSON(t *testing.T) {
	var buf bytes.Buffer
	err := cmd.RenderSlackManifest(&buf, cmd.SlackManifestData{AppName: "X", EventsURL: "https://x/events"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var v interface{}
	if err := json.Unmarshal(buf.Bytes(), &v); err != nil {
		t.Errorf("rendered output is not valid JSON: %v\noutput:\n%s", err, buf.String())
	}
}

// TestSlackManifest_MissingBridgeURL verifies that RunSlackManifest returns a
// clear error pointing at "km slack init" when the SSM bridge-url key is absent.
func TestSlackManifest_MissingBridgeURL(t *testing.T) {
	deps := &cmd.SlackCmdDeps{
		SSM:       newFakeSSM(map[string]string{}), // empty — no bridge URL set
		SsmPrefix: "/km/",
	}
	cfg := newManifestCfg(t)
	var buf bytes.Buffer
	err := cmd.RunSlackManifest(context.Background(), deps, cfg, cmd.SlackManifestOpts{}, &buf)
	if err == nil {
		t.Fatal("expected error when bridge URL missing")
	}
	if !strings.Contains(err.Error(), "km slack init") {
		t.Errorf("error should suggest km slack init; got: %v", err)
	}
}
