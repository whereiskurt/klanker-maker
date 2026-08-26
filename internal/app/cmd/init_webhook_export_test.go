// init_webhook_export_test.go — Unit tests for Phase 127 webhook config env
// export, token minting, and cold-create profile pre-staging (Task 11).
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

func TestExportTerragruntEnvVars_WebhookSources(t *testing.T) {
	os.Unsetenv("KM_WEBHOOK_SOURCES") //nolint:errcheck
	defer os.Unsetenv("KM_WEBHOOK_SOURCES")

	cfg := &config.Config{Webhooks: config.WebhooksConfig{
		RateLimit: &config.WebhookRateLimit{MaxDispatches: 20, WindowSeconds: 600},
		Sources: []config.WebhookSource{{
			Name:  "wiz",
			Auth:  config.WebhookAuth{Type: "bearer", SecretPath: "/km/config/webhooks/wiz/token"},
			Rules: []config.WebhookRule{{Alias: "ir-bot", Prompt: "go"}},
		}},
	}}

	ExportTerragruntEnvVars(cfg)

	raw := os.Getenv("KM_WEBHOOK_SOURCES")
	if raw == "" {
		t.Fatal("KM_WEBHOOK_SOURCES not exported")
	}
	var out struct {
		Sources   []config.WebhookSource   `json:"sources"`
		RateLimit *config.WebhookRateLimit `json:"rate_limit"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("exported value is not valid JSON: %v", err)
	}
	if len(out.Sources) != 1 || out.Sources[0].Name != "wiz" {
		t.Errorf("round-trip mismatch: %s", raw)
	}
	// rate_limit must travel INSIDE the KM_WEBHOOK_SOURCES envelope — there is no
	// separate KM_WEBHOOK_RATE_LIMIT var (see the dedicated absence test below).
	if out.RateLimit == nil || out.RateLimit.MaxDispatches != 20 || out.RateLimit.WindowSeconds != 600 {
		t.Errorf("rate_limit did not round-trip inside the envelope: %s", raw)
	}
}

// Dormancy: no sources => the env var stays unset so the module reads "".
func TestExportTerragruntEnvVars_WebhooksDormant(t *testing.T) {
	os.Unsetenv("KM_WEBHOOK_SOURCES") //nolint:errcheck
	ExportTerragruntEnvVars(&config.Config{})
	if os.Getenv("KM_WEBHOOK_SOURCES") != "" {
		t.Error("absent webhooks: must leave KM_WEBHOOK_SOURCES unset")
	}
}

// KM_WEBHOOK_RATE_LIMIT must NEVER be set by the export — the amended design
// carries rate_limit exclusively inside the KM_WEBHOOK_SOURCES JSON envelope
// (cmd/km-webhook-bridge's parseSourcesEnv is the only reader). A repo-wide grep
// for KM_WEBHOOK_RATE_LIMIT must stay at zero hits; this test pins the runtime
// behaviour that grep can't catch (e.g. a future accidental re-introduction).
func TestExportTerragruntEnvVars_NoSeparateRateLimitVar(t *testing.T) {
	os.Unsetenv("KM_WEBHOOK_SOURCES")       //nolint:errcheck
	os.Unsetenv("KM_WEBHOOK_RATE_LIMIT")    //nolint:errcheck
	defer os.Unsetenv("KM_WEBHOOK_SOURCES") //nolint:errcheck

	cfg := &config.Config{Webhooks: config.WebhooksConfig{
		RateLimit: &config.WebhookRateLimit{MaxDispatches: 5, WindowSeconds: 60},
		Sources: []config.WebhookSource{{
			Name: "wiz",
			Auth: config.WebhookAuth{Type: "bearer", SecretPath: "/p"},
		}},
	}}
	ExportTerragruntEnvVars(cfg)

	if v := os.Getenv("KM_WEBHOOK_RATE_LIMIT"); v != "" {
		t.Errorf("KM_WEBHOOK_RATE_LIMIT must never be exported, got %q", v)
	}
}

// fakeWebhookSSM mirrors the production ssmSecretClientAdapter contract:
// GetParameterValue returns ErrWebhookSecretNotFound (wrapped) when the name is
// absent from `existing`, UNLESS `forcedErr` names an override for that path —
// which lets a test simulate a genuine read failure (throttling, a permissions
// blip, a network fault) distinct from "not found".
type fakeWebhookSSM struct {
	existing  map[string]string
	forcedErr map[string]error
	puts      map[string]string
}

func (f *fakeWebhookSSM) GetParameterValue(_ context.Context, name string) (string, error) {
	if err, ok := f.forcedErr[name]; ok {
		return "", err
	}
	v, ok := f.existing[name]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrWebhookSecretNotFound, name)
	}
	return v, nil
}

func (f *fakeWebhookSSM) PutSecureString(_ context.Context, name, value string) error {
	if f.puts == nil {
		f.puts = map[string]string{}
	}
	f.puts[name] = value
	return nil
}

func TestMintWebhookSecretIfAbsent(t *testing.T) {
	t.Run("mints when absent", func(t *testing.T) {
		s := &fakeWebhookSSM{existing: map[string]string{}}
		minted, token, err := mintWebhookSecretIfAbsent(context.Background(), s, "/km/config/webhooks/wiz/token")
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if !minted || token == "" {
			t.Fatalf("expected a minted token, got minted=%v token=%q", minted, token)
		}
		if len(token) < 32 {
			t.Errorf("token too short: %d chars", len(token))
		}
		if s.puts["/km/config/webhooks/wiz/token"] != token {
			t.Error("token was not written to SSM")
		}
	})

	// Idempotent: re-running km init must NOT rotate a live token out from under
	// a configured Wiz integration.
	t.Run("never overwrites an existing token", func(t *testing.T) {
		s := &fakeWebhookSSM{existing: map[string]string{"/p": "already-set"}}
		minted, _, err := mintWebhookSecretIfAbsent(context.Background(), s, "/p")
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if minted {
			t.Fatal("must not mint over an existing parameter")
		}
		if len(s.puts) != 0 {
			t.Errorf("must not write: %v", s.puts)
		}
	})

	// Regression: a transient GetParameter failure (throttling, auth blip,
	// network fault) is NOT the same thing as "parameter doesn't exist" and must
	// never fall through to a mint+overwrite. The guard must discriminate on
	// ErrWebhookSecretNotFound specifically, not "any error".
	t.Run("a non-not-found read error is returned, never minted over", func(t *testing.T) {
		s := &fakeWebhookSSM{
			existing:  map[string]string{"/p": "already-set"},
			forcedErr: map[string]error{"/p": errors.New("ThrottlingException: rate exceeded")},
		}
		minted, token, err := mintWebhookSecretIfAbsent(context.Background(), s, "/p")
		if err == nil {
			t.Fatal("expected the non-not-found error to be returned")
		}
		if minted || token != "" {
			t.Fatalf("must not report minted on a read failure, got minted=%v token=%q", minted, token)
		}
		if len(s.puts) != 0 {
			t.Errorf("must not write on a read failure: %v", s.puts)
		}
	})
}

// ============================================================
// PreStageWebhookProfiles
// ============================================================

// mockWebhookS3Uploader records every S3 PutObject call made against it.
type mockWebhookS3Uploader struct {
	puts []struct{ bucket, key string }
	err  error
}

func (m *mockWebhookS3Uploader) PutObject(_ context.Context, bucket, key string, _ []byte) error {
	if m.err != nil {
		return m.err
	}
	m.puts = append(m.puts, struct{ bucket, key string }{bucket, key})
	return nil
}

func TestPreStageWebhookProfiles_NoSources_NoOp(t *testing.T) {
	m := &mockWebhookS3Uploader{}
	if err := PreStageWebhookProfiles(context.Background(), nil, "bucket", m); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.puts) != 0 {
		t.Fatalf("expected no uploads, got %v", m.puts)
	}
}

func TestPreStageWebhookProfiles_DedupesBySlug(t *testing.T) {
	sources := []config.WebhookSource{
		{
			Name: "wiz",
			Rules: []config.WebhookRule{
				{Profile: "webhook-incident.yaml"},
				{Profile: "webhook-incident.yaml"}, // duplicate slug across rules
			},
		},
		{
			Name: "other",
			Rules: []config.WebhookRule{
				{Profile: "webhook-incident"}, // same slug, different extension form
			},
		},
	}
	m := &mockWebhookS3Uploader{}
	if err := PreStageWebhookProfiles(context.Background(), sources, "bucket", m); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.puts) != 1 {
		t.Fatalf("expected exactly 1 upload (deduped), got %d: %v", len(m.puts), m.puts)
	}
	want := "webhook-profiles/webhook-incident/.km-profile.yaml"
	if m.puts[0].key != want {
		t.Errorf("key = %q, want %q", m.puts[0].key, want)
	}
	if m.puts[0].bucket != "bucket" {
		t.Errorf("bucket = %q, want %q", m.puts[0].bucket, "bucket")
	}
}

func TestPreStageWebhookProfiles_SkipsOnAbsentSkip(t *testing.T) {
	sources := []config.WebhookSource{
		{
			Name: "wiz",
			Rules: []config.WebhookRule{
				{Profile: "webhook-incident", OnAbsent: "skip"},
				{Profile: "webhook-audit", OnAbsent: "cold-create"},
				{Profile: "webhook-alert"}, // default on_absent (cold-create) — must stage
			},
		},
	}
	m := &mockWebhookS3Uploader{}
	if err := PreStageWebhookProfiles(context.Background(), sources, "bucket", m); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.puts) != 2 {
		t.Fatalf("expected 2 uploads (skip rule excluded), got %d: %v", len(m.puts), m.puts)
	}
	staged := map[string]bool{}
	for _, p := range m.puts {
		staged[p.key] = true
	}
	if staged["webhook-profiles/webhook-incident/.km-profile.yaml"] {
		t.Error("on_absent:skip rule must not be staged")
	}
	if !staged["webhook-profiles/webhook-audit/.km-profile.yaml"] {
		t.Error("on_absent:cold-create rule must be staged")
	}
	if !staged["webhook-profiles/webhook-alert/.km-profile.yaml"] {
		t.Error("default (empty) on_absent rule must be staged (defaults to cold-create)")
	}
}

// Regression: the bridge's cold-create path compares on_absent case-insensitively
// (strings.EqualFold(rule.OnAbsent, "skip"), pkg/webhook/bridge/handler.go).
// Pre-staging must agree — otherwise "on_absent: Skip" is skipped by the handler
// but still staged here, leaving an orphaned upload nothing ever reads.
func TestPreStageWebhookProfiles_SkipIsCaseInsensitive(t *testing.T) {
	sources := []config.WebhookSource{
		{Name: "wiz", Rules: []config.WebhookRule{{Profile: "webhook-incident", OnAbsent: "Skip"}}},
	}
	m := &mockWebhookS3Uploader{}
	if err := PreStageWebhookProfiles(context.Background(), sources, "bucket", m); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.puts) != 0 {
		t.Fatalf("on_absent:Skip (mixed case) must not be staged, got %v", m.puts)
	}
}

func TestPreStageWebhookProfiles_EmptyProfileSkipped(t *testing.T) {
	// A rule with no profile set has nothing to cold-create — must not attempt
	// an upload for an empty slug.
	sources := []config.WebhookSource{
		{Name: "wiz", Rules: []config.WebhookRule{{Alias: "existing-box"}}},
	}
	m := &mockWebhookS3Uploader{}
	if err := PreStageWebhookProfiles(context.Background(), sources, "bucket", m); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.puts) != 0 {
		t.Fatalf("expected no uploads for a rule with no profile, got %v", m.puts)
	}
}
