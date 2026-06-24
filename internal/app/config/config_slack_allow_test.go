package config_test

// Phase 118: install-level Slack trigger allowlist tests.
//
// TestLoadSlackAllow_Set is the PRIMARY regression guard for the
// project_config_key_merge_list footgun: if "slack.allow" is absent from the
// v2→v merge-list in config.go, cfg.Slack.Allow stays nil even when the key
// is present in km-config.yaml and this test fails loud and early.

import (
	"os"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// TestLoadSlackAllow_Set verifies that slack.allow in km-config.yaml
// round-trips into cfg.Slack.Allow (len==2, correct values).
//
// CRITICAL regression guard: the merge-list entry "slack.allow" in config.go
// must be present or this test fails. Simulates the silent-drop footgun
// (project_config_key_merge_list) documented in 118-RESEARCH.md.
func TestLoadSlackAllow_Set(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
slack:
    allow:
      - U0OPERATOR
      - U0XUSER
`)
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	// If Allow is nil, the merge-list entry "slack.allow" is missing in config.go.
	if cfg.Slack.Allow == nil {
		t.Fatal("Slack.Allow is nil; expected non-nil from yaml load — add \"slack.allow\" to the v2→v merge-list in config.go (project_config_key_merge_list footgun)")
	}
	if len(cfg.Slack.Allow) != 2 {
		t.Errorf("Slack.Allow: got len=%d, want 2; values=%v", len(cfg.Slack.Allow), cfg.Slack.Allow)
	}
	if len(cfg.Slack.Allow) > 0 && cfg.Slack.Allow[0] != "U0OPERATOR" {
		t.Errorf("Slack.Allow[0]: got %q, want \"U0OPERATOR\"", cfg.Slack.Allow[0])
	}
	if len(cfg.Slack.Allow) > 1 && cfg.Slack.Allow[1] != "U0XUSER" {
		t.Errorf("Slack.Allow[1]: got %q, want \"U0XUSER\"", cfg.Slack.Allow[1])
	}
}

// TestLoadSlackAllow_Absent verifies that absent slack.allow in km-config.yaml
// leaves cfg.Slack.Allow nil (not an empty slice) — dormant by default.
func TestLoadSlackAllow_Absent(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
`)
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Slack.Allow) != 0 {
		t.Errorf("Slack.Allow: got len=%d %v, want nil/empty (key absent from yaml)", len(cfg.Slack.Allow), cfg.Slack.Allow)
	}
}

// TestLoadSlackAllow_Single verifies a single-entry allowlist round-trips correctly.
func TestLoadSlackAllow_Single(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
slack:
    allow:
      - U0SINGLEUSER
`)
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Slack.Allow) != 1 {
		t.Fatalf("Slack.Allow: got len=%d, want 1; values=%v", len(cfg.Slack.Allow), cfg.Slack.Allow)
	}
	if cfg.Slack.Allow[0] != "U0SINGLEUSER" {
		t.Errorf("Slack.Allow[0]: got %q, want \"U0SINGLEUSER\"", cfg.Slack.Allow[0])
	}
}
