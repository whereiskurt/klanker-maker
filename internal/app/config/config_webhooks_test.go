package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The whole webhooks: block must survive Load(). This is the
// project_config_key_merge_list footgun: without a "webhooks" entry in the
// v2->v merge-list, the block is silently dropped and every field stays zero.
func TestLoad_WebhooksBlockSurvivesMergeList(t *testing.T) {
	dir := t.TempDir()
	yaml := `
domain: example.com
resource_prefix: km
webhooks:
  rate_limit:
    max_dispatches: 20
    window_seconds: 600
  sources:
    - name: wiz
      auth:
        type: bearer
        header: Authorization
        secret_path: /km/config/webhooks/wiz/token
      replay_ttl_seconds: 900
      field_paths:
        id: "$.id"
      rules:
        - match:
            type: [issue, threat]
            severity: [CRITICAL, HIGH]
          alias: ir-bot
          profile: ir-bot
          on_absent: cold-create
          cooldown_seconds: 900
          group_by: "{{entity.cloud_id}}"
          prompt: "triage this"
`
	path := filepath.Join(dir, "km-config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Chdir(dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := len(cfg.Webhooks.Sources); got != 1 {
		t.Fatalf("Sources: got %d, want 1 (merge-list entry missing?)", got)
	}
	src := cfg.Webhooks.Sources[0]
	if src.Name != "wiz" {
		t.Errorf("Name: got %q, want %q", src.Name, "wiz")
	}
	if src.Auth.Type != "bearer" {
		t.Errorf("Auth.Type: got %q, want %q", src.Auth.Type, "bearer")
	}
	if src.Auth.SecretPath != "/km/config/webhooks/wiz/token" {
		t.Errorf("Auth.SecretPath: got %q", src.Auth.SecretPath)
	}
	if src.ReplayTTLSeconds != 900 {
		t.Errorf("ReplayTTLSeconds: got %d, want 900", src.ReplayTTLSeconds)
	}
	if src.FieldPaths["id"] != "$.id" {
		t.Errorf("FieldPaths[id]: got %q", src.FieldPaths["id"])
	}
	if len(src.Rules) != 1 {
		t.Fatalf("Rules: got %d, want 1", len(src.Rules))
	}
	r := src.Rules[0]
	if r.Alias != "ir-bot" || r.Profile != "ir-bot" {
		t.Errorf("alias/profile: got %q/%q", r.Alias, r.Profile)
	}
	if r.OnAbsent != "cold-create" {
		t.Errorf("OnAbsent: got %q", r.OnAbsent)
	}
	if r.CooldownSeconds != 900 {
		t.Errorf("CooldownSeconds: got %d", r.CooldownSeconds)
	}
	if r.GroupBy != "{{entity.cloud_id}}" {
		t.Errorf("GroupBy: got %q", r.GroupBy)
	}
	if got := r.Match["severity"]; len(got) != 2 || got[0] != "CRITICAL" {
		t.Errorf("Match[severity]: got %v", got)
	}
	if cfg.Webhooks.RateLimit == nil || cfg.Webhooks.RateLimit.MaxDispatches != 20 {
		t.Errorf("RateLimit: got %+v", cfg.Webhooks.RateLimit)
	}
}

// Absent webhooks: block must yield the zero value with no error — dormancy.
func TestLoad_WebhooksAbsentIsDormant(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "km-config.yaml")
	if err := os.WriteFile(path, []byte("domain: example.com\nresource_prefix: km\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Chdir(dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Webhooks.Sources) != 0 {
		t.Errorf("Sources: got %d, want 0", len(cfg.Webhooks.Sources))
	}
	if cfg.Webhooks.RateLimit != nil {
		t.Errorf("RateLimit: got %+v, want nil", cfg.Webhooks.RateLimit)
	}
}
