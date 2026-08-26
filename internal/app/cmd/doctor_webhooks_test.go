package cmd

import (
	"context"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	appcfg "github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// Dormancy: no webhooks: block => zero checks, zero AWS calls. This mirrors the
// launch_accounts precedent (checkLaunchAccountLinks) — an install that has
// never heard of this feature must cost nothing.
func TestCheckWebhookSources_SkipsWhenDormant(t *testing.T) {
	if got := checkWebhookSources(appcfg.WebhooksConfig{}, "."); len(got) != 0 {
		t.Fatalf("dormant install must emit no checks, got %d: %+v", len(got), got)
	}
}

func TestCheckWebhookSourcesAWS_SkipsWhenDormant(t *testing.T) {
	if got := checkWebhookSourcesAWS(nil, appcfg.WebhooksConfig{}, nil, "/km/", nil, "km"); len(got) != 0 {
		t.Fatalf("dormant install must emit no AWS-touching checks, got %d: %+v", len(got), got)
	}
}

func TestCheckWebhookSources_StructuralValidation(t *testing.T) {
	cases := []struct {
		name    string
		src     appcfg.WebhookSource
		wantSub string
	}{
		{
			name:    "unknown auth type",
			src:     appcfg.WebhookSource{Name: "wiz", Auth: appcfg.WebhookAuth{Type: "magic", SecretPath: "/p"}, Rules: []appcfg.WebhookRule{{Alias: "a", Prompt: "p"}}},
			wantSub: "auth.type",
		},
		{
			name:    "empty secret_path",
			src:     appcfg.WebhookSource{Name: "wiz", Auth: appcfg.WebhookAuth{Type: "bearer"}, Rules: []appcfg.WebhookRule{{Alias: "a", Prompt: "p"}}},
			wantSub: "secret_path",
		},
		{
			name:    "no rules",
			src:     appcfg.WebhookSource{Name: "wiz", Auth: appcfg.WebhookAuth{Type: "bearer", SecretPath: "/p"}},
			wantSub: "no rules",
		},
		{
			name:    "name not URL-path-safe",
			src:     appcfg.WebhookSource{Name: "wiz/prod", Auth: appcfg.WebhookAuth{Type: "bearer", SecretPath: "/p"}, Rules: []appcfg.WebhookRule{{Alias: "a", Prompt: "p"}}},
			wantSub: "path-safe",
		},
		{
			name:    "rule missing alias",
			src:     appcfg.WebhookSource{Name: "wiz", Auth: appcfg.WebhookAuth{Type: "bearer", SecretPath: "/p"}, Rules: []appcfg.WebhookRule{{Prompt: "p"}}},
			wantSub: "alias",
		},
		{
			name:    "rule missing prompt",
			src:     appcfg.WebhookSource{Name: "wiz", Auth: appcfg.WebhookAuth{Type: "bearer", SecretPath: "/p"}, Rules: []appcfg.WebhookRule{{Alias: "a"}}},
			wantSub: "prompt",
		},
		{
			name:    "cold-create default without profile",
			src:     appcfg.WebhookSource{Name: "wiz", Auth: appcfg.WebhookAuth{Type: "bearer", SecretPath: "/p"}, Rules: []appcfg.WebhookRule{{Alias: "a", Prompt: "p"}}},
			wantSub: "profile is set",
		},
		{
			name:    "cooldown without group_by",
			src:     appcfg.WebhookSource{Name: "wiz", Auth: appcfg.WebhookAuth{Type: "bearer", SecretPath: "/p"}, Rules: []appcfg.WebhookRule{{Alias: "a", Prompt: "p", Profile: "x.yaml", CooldownSeconds: 60}}},
			wantSub: "group_by",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wh := appcfg.WebhooksConfig{Sources: []appcfg.WebhookSource{c.src}}
			results := checkWebhookSources(wh, ".")

			var found bool
			for _, r := range results {
				if r.Status != CheckOK && strings.Contains(strings.ToLower(r.Message), strings.ToLower(c.wantSub)) {
					found = true
				}
			}
			if !found {
				t.Errorf("expected a non-OK result mentioning %q; got %+v", c.wantSub, results)
			}
		})
	}
}

func TestCheckWebhookSources_DuplicateNames(t *testing.T) {
	wh := appcfg.WebhooksConfig{Sources: []appcfg.WebhookSource{
		{Name: "wiz", Auth: appcfg.WebhookAuth{Type: "bearer", SecretPath: "/p1"}, Rules: []appcfg.WebhookRule{{Alias: "a", Prompt: "p", OnAbsent: "skip"}}},
		{Name: "WIZ", Auth: appcfg.WebhookAuth{Type: "bearer", SecretPath: "/p2"}, Rules: []appcfg.WebhookRule{{Alias: "b", Prompt: "p", OnAbsent: "skip"}}},
	}}
	var found bool
	for _, r := range checkWebhookSources(wh, ".") {
		if r.Status != CheckOK && strings.Contains(strings.ToLower(r.Message), "duplicate") {
			found = true
		}
	}
	if !found {
		t.Error("expected a duplicate-name WARN for case-insensitive collision")
	}
}

func TestCheckWebhookSources_ValidConfigIsOK(t *testing.T) {
	wh := appcfg.WebhooksConfig{
		RateLimit: &appcfg.WebhookRateLimit{MaxDispatches: 20, WindowSeconds: 600},
		Sources: []appcfg.WebhookSource{{
			Name:  "wiz",
			Auth:  appcfg.WebhookAuth{Type: "bearer", SecretPath: "/km/config/webhooks/wiz/token"},
			Rules: []appcfg.WebhookRule{{Alias: "ir-bot", Prompt: "go", OnAbsent: "skip"}},
		}},
	}
	for _, r := range checkWebhookSources(wh, ".") {
		if r.Status != CheckOK {
			t.Errorf("valid config produced %v: %s", r.Status, r.Message)
		}
	}
}

func TestCheckWebhookSources_BadRateLimit(t *testing.T) {
	wh := appcfg.WebhooksConfig{
		RateLimit: &appcfg.WebhookRateLimit{MaxDispatches: 5, WindowSeconds: 0},
		Sources: []appcfg.WebhookSource{{
			Name:  "wiz",
			Auth:  appcfg.WebhookAuth{Type: "bearer", SecretPath: "/p"},
			Rules: []appcfg.WebhookRule{{Alias: "a", Prompt: "p", OnAbsent: "skip"}},
		}},
	}
	var found bool
	for _, r := range checkWebhookSources(wh, ".") {
		if r.Status != CheckOK && strings.Contains(strings.ToLower(r.Message), "window_seconds") {
			found = true
		}
	}
	if !found {
		t.Error("window_seconds: 0 must be flagged")
	}
}

func TestCheckWebhookSources_ZeroMaxDispatchesFlagged(t *testing.T) {
	wh := appcfg.WebhooksConfig{
		RateLimit: &appcfg.WebhookRateLimit{MaxDispatches: 0, WindowSeconds: 600},
		Sources: []appcfg.WebhookSource{{
			Name:  "wiz",
			Auth:  appcfg.WebhookAuth{Type: "bearer", SecretPath: "/p"},
			Rules: []appcfg.WebhookRule{{Alias: "a", Prompt: "p", OnAbsent: "skip"}},
		}},
	}
	var found bool
	for _, r := range checkWebhookSources(wh, ".") {
		if r.Status != CheckOK && strings.Contains(strings.ToLower(r.Message), "max_dispatches") {
			found = true
		}
	}
	if !found {
		t.Error("max_dispatches: 0 must be flagged")
	}
}

// TestCheckWebhookSources_MissingProfileFile pins the filesystem-backed rule
// check (mirrors checkGitHubEventsValid's profile-resolvable check): a
// non-skip rule naming a profile that does not exist on disk relative to the
// config dir must WARN.
func TestCheckWebhookSources_MissingProfileFile(t *testing.T) {
	wh := appcfg.WebhooksConfig{Sources: []appcfg.WebhookSource{{
		Name:  "wiz",
		Auth:  appcfg.WebhookAuth{Type: "bearer", SecretPath: "/p"},
		Rules: []appcfg.WebhookRule{{Alias: "a", Prompt: "p", Profile: "does-not-exist.yaml"}},
	}}}
	var found bool
	for _, r := range checkWebhookSources(wh, ".") {
		if r.Status != CheckOK && strings.Contains(strings.ToLower(r.Message), "not found") {
			found = true
		}
	}
	if !found {
		t.Error("a missing profile file must be flagged")
	}
}

func TestCheckWebhookSources_SkipRuleProfileNotRequired(t *testing.T) {
	wh := appcfg.WebhooksConfig{Sources: []appcfg.WebhookSource{{
		Name:  "wiz",
		Auth:  appcfg.WebhookAuth{Type: "bearer", SecretPath: "/p"},
		Rules: []appcfg.WebhookRule{{Alias: "a", Prompt: "p", OnAbsent: "skip"}},
	}}}
	for _, r := range checkWebhookSources(wh, ".") {
		if r.Status != CheckOK {
			t.Errorf("on_absent:skip rule with no profile must not warn: %s: %s", r.Name, r.Message)
		}
	}
}

// =============================================================================
// checkWebhookSourcesAWS and its three primitives
// =============================================================================

func oneSourceWebhooksConfig() appcfg.WebhooksConfig {
	return appcfg.WebhooksConfig{Sources: []appcfg.WebhookSource{{
		Name:  "wiz",
		Auth:  appcfg.WebhookAuth{Type: "bearer", SecretPath: "/km/config/webhooks/wiz/token"},
		Rules: []appcfg.WebhookRule{{Alias: "a", Prompt: "p", OnAbsent: "skip"}},
	}}}
}

func TestCheckWebhookSourcesAWS_ProducesOneResultPerSourcePlusTwo(t *testing.T) {
	client := &mockSSMReadClient{outputs: map[string]*ssm.GetParameterOutput{
		"/km/config/webhooks/wiz/token": {Parameter: &ssmtypes.Parameter{Value: awssdk.String("secret")}},
		"/km/config/webhooks/bridge-url": {Parameter: &ssmtypes.Parameter{
			Value: awssdk.String("https://abc123.lambda-url.us-east-1.on.aws/"),
		}},
	}}
	wh := oneSourceWebhooksConfig()
	results := checkWebhookSourcesAWS(context.Background(), wh, client, "/km/", nil, "km")
	// 1 secret-exists result + 1 bridge-url result + 1 DLQ-depth result.
	if len(results) != 3 {
		t.Fatalf("expected 3 results (1 source + bridge-url + dlq), got %d: %+v", len(results), results)
	}
	for _, r := range results {
		if r.Status == CheckWarn {
			t.Errorf("expected no WARN with a fully-configured install, got %s: %s", r.Name, r.Message)
		}
	}
}

func TestCheckWebhookSecretExists_MissingSecretWarns(t *testing.T) {
	client := &mockSSMReadClient{} // empty ⇒ every GetParameter is ParameterNotFound
	src := appcfg.WebhookSource{Name: "wiz", Auth: appcfg.WebhookAuth{Type: "bearer", SecretPath: "/km/config/webhooks/wiz/token"}}
	r := checkWebhookSecretExists(context.Background(), src, client)
	if r.Status != CheckWarn {
		t.Fatalf("missing secret: want WARN, got %s (%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "/km/config/webhooks/wiz/token") {
		t.Errorf("WARN must name the specific secret path, got: %s", r.Message)
	}
}

func TestCheckWebhookSecretExists_NilClientSkips(t *testing.T) {
	src := appcfg.WebhookSource{Name: "wiz", Auth: appcfg.WebhookAuth{Type: "bearer", SecretPath: "/p"}}
	r := checkWebhookSecretExists(context.Background(), src, nil)
	if r.Status != CheckSkipped {
		t.Fatalf("nil client: want SKIPPED, got %s", r.Status)
	}
}

func TestCheckWebhookBridgeURL_MissingWarns(t *testing.T) {
	client := &mockSSMReadClient{}
	r := checkWebhookBridgeURL(context.Background(), client, "/km/")
	if r.Status != CheckWarn {
		t.Fatalf("missing bridge URL: want WARN, got %s (%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Remediation, "km init") {
		t.Errorf("expected remediation to mention 'km init', got: %s", r.Remediation)
	}
}

func TestCheckWebhookBridgeURL_OK(t *testing.T) {
	client := &mockSSMReadClient{outputs: map[string]*ssm.GetParameterOutput{
		"/km/config/webhooks/bridge-url": {Parameter: &ssmtypes.Parameter{
			Value: awssdk.String("https://abc123.lambda-url.us-east-1.on.aws/"),
		}},
	}}
	r := checkWebhookBridgeURL(context.Background(), client, "/km/")
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (%s)", r.Status, r.Message)
	}
}

func TestCheckWebhookDLQDepth_NilClientSkips(t *testing.T) {
	r := checkWebhookDLQDepth(context.Background(), nil, "km")
	if r.Status != CheckSkipped {
		t.Fatalf("nil SQS client: want SKIPPED, got %s", r.Status)
	}
}

func TestCheckWebhookDLQDepth_NotProvisionedSkips(t *testing.T) {
	fs := &fakeSQS{listByPrefix: true, listResult: nil}
	r := checkWebhookDLQDepth(context.Background(), fs, "km")
	if r.Status != CheckSkipped {
		t.Fatalf("DLQ not provisioned: want SKIPPED, got %s (%s)", r.Status, r.Message)
	}
}

func TestCheckWebhookDLQDepth_WarnsOnDepth(t *testing.T) {
	qName := kmaws.WebhookInboundDLQName("km")
	fs := &fakeSQS{
		listByPrefix: true,
		listResult:   []string{"https://sqs.us-east-1.amazonaws.com/123456789012/" + qName},
		depthByName:  map[string]string{qName: "3"},
	}
	r := checkWebhookDLQDepth(context.Background(), fs, "km")
	if r.Status != CheckWarn {
		t.Fatalf("non-empty DLQ: want WARN, got %s (%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "3") {
		t.Errorf("WARN should mention the message count, got: %s", r.Message)
	}
}

func TestCheckWebhookDLQDepth_OKWhenEmpty(t *testing.T) {
	qName := kmaws.WebhookInboundDLQName("km")
	fs := &fakeSQS{
		listByPrefix: true,
		listResult:   []string{"https://sqs.us-east-1.amazonaws.com/123456789012/" + qName},
	}
	r := checkWebhookDLQDepth(context.Background(), fs, "km")
	if r.Status != CheckOK {
		t.Fatalf("empty DLQ: want OK, got %s (%s)", r.Status, r.Message)
	}
}
