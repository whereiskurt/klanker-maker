// Package config_test provides limits config round-trip tests.
// Phase 121 Plan 03 Task 2 (CFG-01): LimitsConfig struct + merge-list registration +
// UnmarshalKey load + GetLimitsConfig() getter.
//
// These mirror config_check_test.go — the checks: block is the structural template
// for the limits: block. The merge-list regression test is the load-bearing one:
// without "limits" in the v2→v merge-loop the whole limits: block is silently
// dropped (project_config_key_merge_list footgun).
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// writeKMConfigLimits writes a km-config.yaml to dir. Self-contained helper
// mirroring writeKMConfigChecks so this file reads independently.
func writeKMConfigLimits(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "km-config.yaml"), []byte(content), 0600); err != nil {
		t.Fatalf("write km-config.yaml: %v", err)
	}
}

// chdirLimits changes the working directory for the duration of the test.
// Uses a distinct name so it doesn't shadow chdirChecks in a parallel build.
func chdirLimits(t *testing.T, dir string) {
	t.Helper()
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
}

// TestLimitsConfigLoaded is the primary gate test for Phase 121 Plan 03.
// It verifies two invariants:
//
//  1. Populated case: a yaml limits block populates cfg.Limits with the exact
//     per-action window values after config.Load(). The mapstructure keys are
//     snake_case (github_pr, github_comment, etc.) and sub-fields are camelCase
//     (perHour, perDay, onBreach) — viper normalizes these during UnmarshalKey.
//
//  2. Dormant case: an absent limits: block produces a zero-value LimitsConfig
//     (all action pointers nil) with no error — the "dormant by default" invariant.
//
// The populated case also serves as the merge-list regression guard: if "limits"
// is removed from the v2→v merge-loop in config.go, cfg.Limits stays zero-value
// even when the yaml contains it (project_config_key_merge_list footgun).
func TestLimitsConfigLoaded(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfigLimits(t, dir, `
domain: example.com
region: us-east-1
limits:
  github_pr:
    perHour: 15
    onBreach: freeze
  github_comment:
    perHour: 60
    perDay: 300
    onBreach: warn
  email_send:
    lifetime: 200
    perHour: 10
    onBreach: block
`)
		chdirLimits(t, dir)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		// Assert github_pr was populated (the primary merge-list guard).
		if cfg.Limits.GithubPR == nil {
			t.Fatalf("Limits.GithubPR: got nil; merge-list footgun check (project_config_key_merge_list) — ensure \"limits\" is in the v2→v merge-loop in config.go")
		}

		// Assert individual github_pr fields.
		if cfg.Limits.GithubPR.PerHour == nil || *cfg.Limits.GithubPR.PerHour != 15 {
			t.Errorf("Limits.GithubPR.PerHour: got %v, want 15", cfg.Limits.GithubPR.PerHour)
		}
		if cfg.Limits.GithubPR.OnBreach != "freeze" {
			t.Errorf("Limits.GithubPR.OnBreach: got %q, want %q", cfg.Limits.GithubPR.OnBreach, "freeze")
		}
		// Absent fields must be nil, not zero-valued.
		if cfg.Limits.GithubPR.Lifetime != nil {
			t.Errorf("Limits.GithubPR.Lifetime: got %v, want nil (absent)", cfg.Limits.GithubPR.Lifetime)
		}
		if cfg.Limits.GithubPR.PerDay != nil {
			t.Errorf("Limits.GithubPR.PerDay: got %v, want nil (absent)", cfg.Limits.GithubPR.PerDay)
		}

		// Assert github_comment fields.
		if cfg.Limits.GithubComment == nil {
			t.Fatal("Limits.GithubComment: got nil")
		}
		if cfg.Limits.GithubComment.PerHour == nil || *cfg.Limits.GithubComment.PerHour != 60 {
			t.Errorf("Limits.GithubComment.PerHour: got %v, want 60", cfg.Limits.GithubComment.PerHour)
		}
		if cfg.Limits.GithubComment.PerDay == nil || *cfg.Limits.GithubComment.PerDay != 300 {
			t.Errorf("Limits.GithubComment.PerDay: got %v, want 300", cfg.Limits.GithubComment.PerDay)
		}
		if cfg.Limits.GithubComment.OnBreach != "warn" {
			t.Errorf("Limits.GithubComment.OnBreach: got %q, want %q", cfg.Limits.GithubComment.OnBreach, "warn")
		}

		// Assert email_send fields (has lifetime).
		if cfg.Limits.EmailSend == nil {
			t.Fatal("Limits.EmailSend: got nil")
		}
		if cfg.Limits.EmailSend.Lifetime == nil || *cfg.Limits.EmailSend.Lifetime != 200 {
			t.Errorf("Limits.EmailSend.Lifetime: got %v, want 200", cfg.Limits.EmailSend.Lifetime)
		}
		if cfg.Limits.EmailSend.PerHour == nil || *cfg.Limits.EmailSend.PerHour != 10 {
			t.Errorf("Limits.EmailSend.PerHour: got %v, want 10", cfg.Limits.EmailSend.PerHour)
		}
		if cfg.Limits.EmailSend.OnBreach != "block" {
			t.Errorf("Limits.EmailSend.OnBreach: got %q, want %q", cfg.Limits.EmailSend.OnBreach, "block")
		}

		// Absent actions must be nil (dormant for those actions).
		if cfg.Limits.GithubReview != nil {
			t.Errorf("Limits.GithubReview: got non-nil %+v, want nil (absent)", cfg.Limits.GithubReview)
		}
		if cfg.Limits.SlackPost != nil {
			t.Errorf("Limits.SlackPost: got non-nil %+v, want nil (absent)", cfg.Limits.SlackPost)
		}
		if cfg.Limits.H1Comment != nil {
			t.Errorf("Limits.H1Comment: got non-nil %+v, want nil (absent)", cfg.Limits.H1Comment)
		}
	})

	t.Run("absent", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfigLimits(t, dir, `
domain: example.com
region: us-east-1
`)
		chdirLimits(t, dir)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error when limits: absent: %v", err)
		}

		// Dormant by default: absent limits: block → zero value → all action pointers nil.
		if cfg.Limits.GithubPR != nil {
			t.Errorf("Limits.GithubPR: got non-nil, want nil (key absent => dormant)")
		}
		if cfg.Limits.EmailSend != nil {
			t.Errorf("Limits.EmailSend: got non-nil, want nil (key absent => dormant)")
		}
	})
}

// TestLimitsConfigLoaded_MergeListRegression is a focused guard that the "limits"
// entry exists in the v2→v merge-list. If the entry is removed, cfg.Limits.GithubPR
// will be nil even when the yaml contains it, causing this test to fail.
// Mirrors TestChecksConfigMerge_MergeListRegression in config_check_test.go.
func TestLimitsConfigLoaded_MergeListRegression(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigLimits(t, dir, `
domain: example.com
region: us-east-1
limits:
  github_pr:
    perHour: 5
    onBreach: warn
`)
	chdirLimits(t, dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Limits.GithubPR == nil {
		t.Fatal("MERGE-LIST GUARD: Limits.GithubPR is nil; expected non-nil from yaml load (merge-loop must include \"limits\")")
	}
	if cfg.Limits.GithubPR.PerHour == nil {
		t.Fatal("MERGE-LIST GUARD: Limits.GithubPR.PerHour is nil — nested limits: field not merged")
	}
	if *cfg.Limits.GithubPR.PerHour != 5 {
		t.Errorf("MERGE-LIST GUARD: Limits.GithubPR.PerHour=%d, want 5 — nested limits: field not merged correctly", *cfg.Limits.GithubPR.PerHour)
	}
	if cfg.Limits.GithubPR.OnBreach != "warn" {
		t.Errorf("MERGE-LIST GUARD: Limits.GithubPR.OnBreach=%q, want %q", cfg.Limits.GithubPR.OnBreach, "warn")
	}
}

// TestGetLimitsConfig verifies that GetLimitsConfig() returns the loaded config values
// and tolerates the dormant zero-value case without panic.
func TestGetLimitsConfig(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfigLimits(t, dir, `
domain: example.com
region: us-east-1
limits:
  slack_post:
    perHour: 120
    onBreach: warn
  h1_comment:
    perHour: 60
    onBreach: warn
`)
		chdirLimits(t, dir)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		got := cfg.GetLimitsConfig()

		if got.SlackPost == nil {
			t.Fatal("GetLimitsConfig().SlackPost: got nil")
		}
		if got.SlackPost.PerHour == nil || *got.SlackPost.PerHour != 120 {
			t.Errorf("GetLimitsConfig().SlackPost.PerHour: got %v, want 120", got.SlackPost.PerHour)
		}
		if got.SlackPost.OnBreach != "warn" {
			t.Errorf("GetLimitsConfig().SlackPost.OnBreach: got %q, want %q", got.SlackPost.OnBreach, "warn")
		}

		if got.H1Comment == nil {
			t.Fatal("GetLimitsConfig().H1Comment: got nil")
		}
		if got.H1Comment.PerHour == nil || *got.H1Comment.PerHour != 60 {
			t.Errorf("GetLimitsConfig().H1Comment.PerHour: got %v, want 60", got.H1Comment.PerHour)
		}
	})

	t.Run("absent", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfigLimits(t, dir, `
domain: example.com
region: us-east-1
`)
		chdirLimits(t, dir)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		// GetLimitsConfig must tolerate the dormant case without panic.
		got := cfg.GetLimitsConfig()
		if got.GithubPR != nil {
			t.Errorf("GetLimitsConfig().GithubPR: got non-nil %+v, want nil (absent => dormant)", got.GithubPR)
		}
		if got.SlackPost != nil {
			t.Errorf("GetLimitsConfig().SlackPost: got non-nil %+v, want nil (absent => dormant)", got.SlackPost)
		}
	})
}
