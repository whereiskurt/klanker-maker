package cmd_test

// Phase 103 Plan 06 — TDD tests for `km h1 init` / `km h1 status`.
//
// Forked from github_test.go, dropping the manifest cases (HackerOne has no
// App-install model). Reuses the mockSSMWrite / findSSMCall helpers defined in
// configure_github_test.go (same package).

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ─────────────────────────────────────────────────────────
// km h1 init tests
// ─────────────────────────────────────────────────────────

// TestH1Init verifies RunH1Init writes the three SSM params under
// /{prefix}config/h1/{webhook-secret,api-username,api-token} with the correct
// types — webhook-secret + api-token as SecureString, api-username as String.
func TestH1Init(t *testing.T) {
	mock := &mockSSMWrite{}
	cfg := &config.Config{} // default prefix "/km/"
	out := &bytes.Buffer{}

	err := cmd.RunH1Init(context.Background(), mock, cfg, cmd.H1InitOpts{
		APIUsername: "operator@example.com/sandbox",
		APIToken:    "tok-abc-123",
		BridgeURL:   "https://bridge.example.com/",
		Force:       true,
	}, out)
	if err != nil {
		t.Fatalf("RunH1Init: %v", err)
	}

	// webhook-secret — SecureString, non-empty (random hex).
	whSecret := findSSMCall(mock.calls, "/km/config/h1/webhook-secret")
	if whSecret == nil {
		t.Fatal("expected SSM write for /km/config/h1/webhook-secret")
	}
	if string(whSecret.Type) != "SecureString" {
		t.Errorf("webhook-secret type: got %q, want SecureString", whSecret.Type)
	}
	if whSecret.Value == nil || *whSecret.Value == "" {
		t.Error("webhook-secret must be non-empty (randomly generated)")
	}
	// 32 bytes hex == 64 chars.
	if whSecret.Value != nil && len(*whSecret.Value) != 64 {
		t.Errorf("webhook-secret length: got %d, want 64 hex chars", len(*whSecret.Value))
	}

	// api-username — String.
	apiUser := findSSMCall(mock.calls, "/km/config/h1/api-username")
	if apiUser == nil {
		t.Fatal("expected SSM write for /km/config/h1/api-username")
	}
	if string(apiUser.Type) != "String" {
		t.Errorf("api-username type: got %q, want String", apiUser.Type)
	}
	if apiUser.Value == nil || *apiUser.Value != "operator@example.com/sandbox" {
		t.Errorf("api-username value: got %v, want %q", apiUser.Value, "operator@example.com/sandbox")
	}

	// api-token — SecureString (secret).
	apiToken := findSSMCall(mock.calls, "/km/config/h1/api-token")
	if apiToken == nil {
		t.Fatal("expected SSM write for /km/config/h1/api-token")
	}
	if string(apiToken.Type) != "SecureString" {
		t.Errorf("api-token type: got %q, want SecureString", apiToken.Type)
	}
	if apiToken.Value == nil || *apiToken.Value != "tok-abc-123" {
		t.Errorf("api-token value: got %v, want %q", apiToken.Value, "tok-abc-123")
	}
}

// TestH1Init_GeneratesRandomWebhookSecret verifies two invocations mint
// distinct webhook secrets (crypto/rand, not static).
func TestH1Init_GeneratesRandomWebhookSecret(t *testing.T) {
	cfg := &config.Config{}
	out := &bytes.Buffer{}

	mock1 := &mockSSMWrite{}
	if err := cmd.RunH1Init(context.Background(), mock1, cfg, cmd.H1InitOpts{
		APIUsername: "u", APIToken: "t", Force: true,
	}, out); err != nil {
		t.Fatalf("first RunH1Init: %v", err)
	}
	out.Reset()
	mock2 := &mockSSMWrite{}
	if err := cmd.RunH1Init(context.Background(), mock2, cfg, cmd.H1InitOpts{
		APIUsername: "u", APIToken: "t", Force: true,
	}, out); err != nil {
		t.Fatalf("second RunH1Init: %v", err)
	}

	s1 := findSSMCall(mock1.calls, "/km/config/h1/webhook-secret")
	s2 := findSSMCall(mock2.calls, "/km/config/h1/webhook-secret")
	if s1 == nil || s2 == nil {
		t.Fatal("webhook-secret SSM calls missing")
	}
	if *s1.Value == *s2.Value {
		t.Error("webhook secrets must differ between invocations (randomly generated)")
	}
}

// TestH1Init_PrintsFunctionURL verifies the command prints the bridge Function
// URL, the minted secret, and the HackerOne Webhooks UI paste path.
func TestH1Init_PrintsFunctionURL(t *testing.T) {
	mock := &mockSSMWrite{}
	cfg := &config.Config{}
	out := &bytes.Buffer{}

	err := cmd.RunH1Init(context.Background(), mock, cfg, cmd.H1InitOpts{
		APIUsername: "u",
		APIToken:    "t",
		BridgeURL:   "https://bridge.example.com/",
		Force:       true,
	}, out)
	if err != nil {
		t.Fatalf("RunH1Init: %v", err)
	}

	s := out.String()
	if !strings.Contains(s, "https://bridge.example.com/") {
		t.Errorf("output should print the bridge Function URL; got:\n%s", s)
	}
	// The minted secret must be echoed for the operator to paste.
	whSecret := findSSMCall(mock.calls, "/km/config/h1/webhook-secret")
	if whSecret == nil || whSecret.Value == nil {
		t.Fatal("webhook-secret not written")
	}
	if !strings.Contains(s, *whSecret.Value) {
		t.Errorf("output should echo the minted webhook secret for pasting; got:\n%s", s)
	}
	// HackerOne Webhooks UI path hint.
	if !strings.Contains(s, "Webhooks") {
		t.Errorf("output should mention the HackerOne Webhooks UI path; got:\n%s", s)
	}
}

// TestH1Init_NoManifest verifies `km h1` exposes init + status but NOT manifest
// (HackerOne has no App-install model).
func TestH1Init_NoManifest(t *testing.T) {
	cfg := &config.Config{}
	parent := cmd.NewH1Cmd(cfg)

	have := map[string]bool{}
	for _, c := range parent.Commands() {
		have[c.Name()] = true
	}
	if !have["init"] {
		t.Error("km h1 must expose the init subcommand")
	}
	if !have["status"] {
		t.Error("km h1 must expose the status subcommand")
	}
	if have["manifest"] {
		t.Error("km h1 must NOT expose a manifest subcommand (HackerOne has no App-install model)")
	}
}

// ─────────────────────────────────────────────────────────
// km h1 status tests
// ─────────────────────────────────────────────────────────

// mockH1SSMReadAPI is a tiny read-only mock for status tests.
type mockH1SSMReadAPI struct {
	params map[string]string
}

func (m *mockH1SSMReadAPI) GetParameter(_ context.Context, input *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
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

// TestH1Status verifies RunH1Status prints programs/targets/handle/bridge-url
// while REDACTING the webhook secret and the api-token.
func TestH1Status(t *testing.T) {
	ssmMock := &mockH1SSMReadAPI{
		params: map[string]string{
			"/km/config/h1/webhook-secret": "super-secret-webhook-value",
			"/km/config/h1/api-username":   "operator@example.com/sandbox",
			"/km/config/h1/api-token":      "super-secret-api-token",
			"/km/config/h1/bridge-url":     "https://bridge.example.com/",
		},
	}

	cfg := &config.Config{
		H1: config.H1Config{
			BotHandle: "@km",
			Programs: []config.H1ProgramEntry{
				{
					Handle:  "acme-sandbox",
					Allow:   []string{"alice"},
					Targets: []config.H1Target{{Alias: "h1-acme", Profile: "profiles/h1-review.yaml"}},
				},
			},
		},
	}
	out := &bytes.Buffer{}

	if err := cmd.RunH1Status(context.Background(), ssmMock, cfg, out); err != nil {
		t.Fatalf("RunH1Status: %v", err)
	}
	s := out.String()

	// Non-secret config must appear.
	if !strings.Contains(s, "@km") {
		t.Errorf("status should print the bot handle; got:\n%s", s)
	}
	if !strings.Contains(s, "acme-sandbox") {
		t.Errorf("status should print the program handle; got:\n%s", s)
	}
	if !strings.Contains(s, "https://bridge.example.com/") {
		t.Errorf("status should print the bridge-url; got:\n%s", s)
	}
	if !strings.Contains(s, "operator@example.com/sandbox") {
		t.Errorf("status should print the api-username (not secret); got:\n%s", s)
	}

	// Secrets must NOT appear.
	if strings.Contains(s, "super-secret-webhook-value") {
		t.Errorf("status MUST redact the webhook secret; got:\n%s", s)
	}
	if strings.Contains(s, "super-secret-api-token") {
		t.Errorf("status MUST redact the api-token; got:\n%s", s)
	}
}

// TestH1Status_Dormant verifies a clean "not configured" message (no error)
// when h1: is absent and SSM is empty.
func TestH1Status_Dormant(t *testing.T) {
	ssmMock := &mockH1SSMReadAPI{params: map[string]string{}}
	cfg := &config.Config{} // no H1 block
	out := &bytes.Buffer{}

	if err := cmd.RunH1Status(context.Background(), ssmMock, cfg, out); err != nil {
		t.Fatalf("RunH1Status dormant should not error; got: %v", err)
	}
	s := strings.ToLower(out.String())
	if !strings.Contains(s, "not configured") {
		t.Errorf("dormant status should print a 'not configured' message; got:\n%s", out.String())
	}
}
