package cmd_test

// Phase 97 Plan 01 Task 3 — TDD tests for km github init/manifest/status commands.
// Uses the mockSSMWrite / mockSSMReadWrite types already defined in configure_github_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ─────────────────────────────────────────────────────────
// km github manifest tests
// ─────────────────────────────────────────────────────────

// TestGitHubManifest_RendersValidJSON verifies that RunGitHubManifest writes
// valid JSON with the required scopes and issue_comment event.
func TestGitHubManifest_RendersValidJSON(t *testing.T) {
	cfg := &config.Config{}
	out := &bytes.Buffer{}

	err := cmd.RunGitHubManifest(context.Background(), cfg, cmd.GitHubManifestOpts{
		AppName:   "km-test-app",
		BridgeURL: "https://bridge.example.com/",
	}, out)
	if err != nil {
		t.Fatalf("RunGitHubManifest: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("RunGitHubManifest output is not valid JSON: %v\nraw: %s", err, out.String())
	}

	// Permissions: issues = write, pull_requests = write, contents = read, checks = write
	perms, ok := payload["default_permissions"].(map[string]interface{})
	if !ok {
		t.Fatalf("default_permissions missing or wrong type")
	}
	for key, want := range map[string]string{
		"issues":        "write",
		"pull_requests": "write",
		"contents":      "read",
		"checks":        "write",
	} {
		if got, _ := perms[key].(string); got != want {
			t.Errorf("default_permissions.%s: got %q, want %q", key, got, want)
		}
	}

	// default_events must include issue_comment
	evts, ok := payload["default_events"].([]interface{})
	if !ok {
		t.Fatalf("default_events missing or wrong type")
	}
	found := false
	for _, e := range evts {
		if e == "issue_comment" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("default_events must include 'issue_comment'; got %v", evts)
	}

	// hook_attributes.url must contain the bridge URL
	ha, ok := payload["hook_attributes"].(map[string]interface{})
	if !ok {
		t.Fatalf("hook_attributes missing or wrong type")
	}
	hookURL, _ := ha["url"].(string)
	if !strings.Contains(hookURL, "bridge.example.com") {
		t.Errorf("hook_attributes.url: got %q, expected to contain bridge URL", hookURL)
	}
	if active, _ := ha["active"].(bool); !active {
		t.Errorf("hook_attributes.active: got %v, want true (bridge URL supplied)", ha["active"])
	}
}

// TestGitHubManifest_NoURL verifies that RunGitHubManifest works without a
// bridge URL (active=false, placeholder URL in hook_attributes).
func TestGitHubManifest_NoURL(t *testing.T) {
	cfg := &config.Config{}
	out := &bytes.Buffer{}

	err := cmd.RunGitHubManifest(context.Background(), cfg, cmd.GitHubManifestOpts{
		AppName: "km-test-no-url",
	}, out)
	if err != nil {
		t.Fatalf("RunGitHubManifest (no URL): %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	ha, ok := payload["hook_attributes"].(map[string]interface{})
	if !ok {
		t.Fatalf("hook_attributes missing")
	}
	if active, _ := ha["active"].(bool); active {
		t.Errorf("hook_attributes.active: got true, want false when no bridge URL supplied")
	}
}

// ─────────────────────────────────────────────────────────
// km github init tests
// ─────────────────────────────────────────────────────────

// TestGitHubInit_WritesThreeSSMKeys verifies that RunGitHubInit writes
// webhook-secret (SecureString), bot-login (String), bridge-url (String)
// to SSM at /{prefix}config/github/{webhook-secret,bot-login,bridge-url}.
func TestGitHubInit_WritesThreeSSMKeys(t *testing.T) {
	mock := &mockSSMWrite{}
	cfg := &config.Config{} // default prefix "/km/"
	out := &bytes.Buffer{}

	err := cmd.RunGitHubInit(context.Background(), mock, cfg, cmd.GitHubInitOpts{
		BotLogin:  "mykm-bot[bot]",
		BridgeURL: "https://bridge.example.com/",
		Force:     true,
	}, out)
	if err != nil {
		t.Fatalf("RunGitHubInit: %v", err)
	}

	names := make(map[string]bool)
	for _, call := range mock.calls {
		if call.Name != nil {
			names[*call.Name] = true
		}
	}

	// Must write webhook-secret as SecureString
	whSecret := findSSMCall(mock.calls, "/km/config/github/webhook-secret")
	if whSecret == nil {
		t.Fatal("expected SSM write for /km/config/github/webhook-secret")
	}
	if string(whSecret.Type) != "SecureString" {
		t.Errorf("webhook-secret type: got %q, want SecureString", whSecret.Type)
	}
	if whSecret.Value == nil || *whSecret.Value == "" {
		t.Error("webhook-secret must be non-empty (randomly generated)")
	}

	// Must write bot-login as String
	botLogin := findSSMCall(mock.calls, "/km/config/github/bot-login")
	if botLogin == nil {
		t.Fatal("expected SSM write for /km/config/github/bot-login")
	}
	if string(botLogin.Type) != "String" {
		t.Errorf("bot-login type: got %q, want String", botLogin.Type)
	}
	if *botLogin.Value != "mykm-bot[bot]" {
		t.Errorf("bot-login value: got %q, want %q", *botLogin.Value, "mykm-bot[bot]")
	}

	// Must write bridge-url as String
	bridgeURL := findSSMCall(mock.calls, "/km/config/github/bridge-url")
	if bridgeURL == nil {
		t.Fatal("expected SSM write for /km/config/github/bridge-url")
	}
	if string(bridgeURL.Type) != "String" {
		t.Errorf("bridge-url type: got %q, want String", bridgeURL.Type)
	}
	if *bridgeURL.Value != "https://bridge.example.com/" {
		t.Errorf("bridge-url value: got %q, want %q", *bridgeURL.Value, "https://bridge.example.com/")
	}

	_ = names
}

// TestGitHubInit_GeneratesRandomWebhookSecret verifies that two calls produce
// different webhook secrets (random, not static).
func TestGitHubInit_GeneratesRandomWebhookSecret(t *testing.T) {
	cfg := &config.Config{}
	out := &bytes.Buffer{}

	mock1 := &mockSSMWrite{}
	if err := cmd.RunGitHubInit(context.Background(), mock1, cfg, cmd.GitHubInitOpts{Force: true}, out); err != nil {
		t.Fatalf("first RunGitHubInit: %v", err)
	}
	out.Reset()
	mock2 := &mockSSMWrite{}
	if err := cmd.RunGitHubInit(context.Background(), mock2, cfg, cmd.GitHubInitOpts{Force: true}, out); err != nil {
		t.Fatalf("second RunGitHubInit: %v", err)
	}

	s1 := findSSMCall(mock1.calls, "/km/config/github/webhook-secret")
	s2 := findSSMCall(mock2.calls, "/km/config/github/webhook-secret")
	if s1 == nil || s2 == nil {
		t.Fatal("webhook-secret SSM calls missing")
	}
	if *s1.Value == *s2.Value {
		t.Error("webhook secrets must differ between invocations (must be randomly generated)")
	}
}

// TestGitHubInit_DefaultBotLogin verifies that when --bot-login is omitted,
// a sensible default is derived (non-empty).
func TestGitHubInit_DefaultBotLogin(t *testing.T) {
	mock := &mockSSMWrite{}
	cfg := &config.Config{}
	out := &bytes.Buffer{}

	// No BotLogin set → should derive a default
	err := cmd.RunGitHubInit(context.Background(), mock, cfg, cmd.GitHubInitOpts{Force: true}, out)
	if err != nil {
		t.Fatalf("RunGitHubInit (no BotLogin): %v", err)
	}

	botLogin := findSSMCall(mock.calls, "/km/config/github/bot-login")
	if botLogin == nil {
		t.Fatal("expected SSM write for /km/config/github/bot-login even without --bot-login")
	}
	if botLogin.Value == nil || *botLogin.Value == "" {
		t.Error("bot-login must be non-empty even without --bot-login flag")
	}
}

// ─────────────────────────────────────────────────────────
// km github status tests
// ─────────────────────────────────────────────────────────

// mockSSMReadWriteGH wraps mockSSMReadWrite for the GitHub status tests.
// Re-uses the type already defined in configure_github_test.go via the same package.
type mockGitHubSSMReadAPI struct {
	params map[string]string
}

func (m *mockGitHubSSMReadAPI) GetParameter(_ context.Context, input *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	name := ""
	if input.Name != nil {
		name = *input.Name
	}
	v, ok := m.params[name]
	if !ok {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{
			Name:  input.Name,
			Value: &v,
		},
	}, nil
}

// TestGitHubStatus_PrintsKeys verifies that RunGitHubStatus reads and prints
// the SSM-backed GitHub config. Webhook secret must be redacted.
func TestGitHubStatus_PrintsKeys(t *testing.T) {
	ssmMock := &mockGitHubSSMReadAPI{
		params: map[string]string{
			"/km/config/github/webhook-secret": "super-secret-value-123",
			"/km/config/github/bot-login":      "klanker-maker[bot]",
			"/km/config/github/bridge-url":     "https://bridge.example.com/",
		},
	}

	cfg := &config.Config{}
	out := &bytes.Buffer{}

	err := cmd.RunGitHubStatus(context.Background(), ssmMock, cfg, out)
	if err != nil {
		t.Fatalf("RunGitHubStatus: %v", err)
	}

	outStr := out.String()

	// bot-login and bridge-url should appear in output
	if !strings.Contains(outStr, "klanker-maker[bot]") {
		t.Errorf("expected bot-login in output; got: %s", outStr)
	}
	if !strings.Contains(outStr, "bridge.example.com") {
		t.Errorf("expected bridge-url in output; got: %s", outStr)
	}

	// webhook-secret must NOT appear in plaintext
	if strings.Contains(outStr, "super-secret-value-123") {
		t.Errorf("webhook-secret must be redacted in status output; got: %s", outStr)
	}
	// But it should show SOMETHING (e.g. "[set]" or "***")
	if !strings.Contains(outStr, "webhook") {
		t.Errorf("expected 'webhook' indicator in output; got: %s", outStr)
	}
}

// TestGitHubStatus_MissingKeys verifies RunGitHubStatus handles missing SSM
// keys gracefully (shows "not set" rather than crashing).
func TestGitHubStatus_MissingKeys(t *testing.T) {
	ssmMock := &mockGitHubSSMReadAPI{params: map[string]string{}} // nothing in SSM

	cfg := &config.Config{}
	out := &bytes.Buffer{}

	// Should NOT return an error — status shows "(not set)" for missing keys
	err := cmd.RunGitHubStatus(context.Background(), ssmMock, cfg, out)
	if err != nil {
		t.Fatalf("RunGitHubStatus with empty SSM should not error; got: %v", err)
	}

	outStr := out.String()
	if !strings.Contains(outStr, "not set") && !strings.Contains(outStr, "(not set)") {
		t.Errorf("expected 'not set' indicator for missing keys; got: %s", outStr)
	}
}

// ─────────────────────────────────────────────────────────
// Phase 115 Wave 0 RED scaffold — GH-EVENT-MANIFEST
// ─────────────────────────────────────────────────────────

// TestRunGitHubManifest_EventUnion verifies that RunGitHubManifest includes
// event types from github.events rules in default_events (union with "issue_comment").
//
// GH-EVENT-MANIFEST: cfg.Github.Events field + the union logic in RunGitHubManifest
// do not exist yet → compile-fail on cfg.Github.Events = genuine RED Wave 0.
// Implemented in Phase 115 Plan 05.
func TestRunGitHubManifest_EventUnion(t *testing.T) {
	// Build a config with a github.events rule that adds "repository" event.
	// cfg.Github.Events does not exist yet → compile-fail = RED.
	cfg := &config.Config{}
	cfg.Github.Events = []config.GithubEventRule{
		{
			On:     "repository",
			Match:  "myorg/*",
			Prompt: "New repo {{repo}}",
		},
	}

	out := &bytes.Buffer{}
	err := cmd.RunGitHubManifest(context.Background(), cfg, cmd.GitHubManifestOpts{
		AppName:   "km-test-event-union",
		BridgeURL: "https://bridge.example.com/",
	}, out)
	if err != nil {
		t.Fatalf("RunGitHubManifest: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("RunGitHubManifest output is not valid JSON: %v\nraw: %s", err, out.String())
	}

	evts, ok := payload["default_events"].([]interface{})
	if !ok {
		t.Fatalf("default_events missing or wrong type in manifest")
	}

	evtSet := make(map[string]bool)
	for _, e := range evts {
		if s, ok := e.(string); ok {
			evtSet[s] = true
		}
	}

	// "issue_comment" must always be present
	if !evtSet["issue_comment"] {
		t.Errorf("default_events must always include 'issue_comment'; got %v", evts)
	}
	// "repository" must be added from the rule
	if !evtSet["repository"] {
		t.Errorf("default_events must include 'repository' from github.events rule; got %v", evts)
	}

	// metadata:read permission must be present when "repository" event is configured
	perms, ok := payload["default_permissions"].(map[string]interface{})
	if !ok {
		t.Fatalf("default_permissions missing or wrong type")
	}
	if perms["metadata"] != "read" {
		t.Errorf("default_permissions.metadata: got %q, want %q (required for repository events)", perms["metadata"], "read")
	}
}
