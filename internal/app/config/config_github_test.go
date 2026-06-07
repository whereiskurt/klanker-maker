// Package config_test provides GitHub config round-trip tests.
// Phase 97 Plan 01 Task 1: GithubConfig struct + merge-list registration + UnmarshalKey load.
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// writeKMConfigGH writes a km-config.yaml to dir (mirrors writeKMConfig from config_test.go
// but kept self-contained so this file can be read independently).
func writeKMConfigGH(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "km-config.yaml"), []byte(content), 0600); err != nil {
		t.Fatalf("write km-config.yaml: %v", err)
	}
}

func chdirGH(t *testing.T, dir string) {
	t.Helper()
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
}

// TestLoadGitHubRepos_Set verifies that a full github: block round-trips through
// config.Load() — each GithubRepoEntry field is preserved.
// This also catches the merge-list footgun: if "github" is missing from the v2→v
// merge-loop in config.go, the entire block is silently dropped and cfg.Github
// stays zero even when the yaml contains it.
func TestLoadGitHubRepos_Set(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigGH(t, dir, `
domain: example.com
region: us-east-1
github:
  default_profile: profiles/review.yaml
  repos:
    - match: "myorg/frontend"
      alias: frontend
      profile: profiles/frontend.yaml
      allow:
        - "github.com"
        - "registry.npmjs.org"
    - match: "myorg/backend"
      alias: backend
      profile: profiles/backend.yaml
`)
	chdirGH(t, dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// DefaultProfile
	if cfg.Github.DefaultProfile != "profiles/review.yaml" {
		t.Errorf("Github.DefaultProfile: got %q, want %q", cfg.Github.DefaultProfile, "profiles/review.yaml")
	}

	// Two repo entries
	if len(cfg.Github.Repos) != 2 {
		t.Fatalf("Github.Repos: got len=%d, want 2; values=%+v", len(cfg.Github.Repos), cfg.Github.Repos)
	}

	// First entry — full fields
	r0 := cfg.Github.Repos[0]
	if r0.Match != "myorg/frontend" {
		t.Errorf("Repos[0].Match: got %q, want %q", r0.Match, "myorg/frontend")
	}
	if r0.Alias != "frontend" {
		t.Errorf("Repos[0].Alias: got %q, want %q", r0.Alias, "frontend")
	}
	if r0.Profile != "profiles/frontend.yaml" {
		t.Errorf("Repos[0].Profile: got %q, want %q", r0.Profile, "profiles/frontend.yaml")
	}
	if len(r0.Allow) != 2 {
		t.Errorf("Repos[0].Allow: got len=%d, want 2; values=%v", len(r0.Allow), r0.Allow)
	} else {
		if r0.Allow[0] != "github.com" {
			t.Errorf("Repos[0].Allow[0]: got %q, want %q", r0.Allow[0], "github.com")
		}
		if r0.Allow[1] != "registry.npmjs.org" {
			t.Errorf("Repos[0].Allow[1]: got %q, want %q", r0.Allow[1], "registry.npmjs.org")
		}
	}

	// Second entry — optional fields absent
	r1 := cfg.Github.Repos[1]
	if r1.Match != "myorg/backend" {
		t.Errorf("Repos[1].Match: got %q, want %q", r1.Match, "myorg/backend")
	}
	if r1.Alias != "backend" {
		t.Errorf("Repos[1].Alias: got %q, want %q", r1.Alias, "backend")
	}
	if r1.Profile != "profiles/backend.yaml" {
		t.Errorf("Repos[1].Profile: got %q, want %q", r1.Profile, "profiles/backend.yaml")
	}
	if len(r1.Allow) != 0 {
		t.Errorf("Repos[1].Allow: got %v, want empty", r1.Allow)
	}
}

// TestLoadGitHubAbsent verifies that an absent github: block yields zero-value
// GithubConfig with no panic. This is the dormant byte-identity invariant.
func TestLoadGitHubAbsent(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigGH(t, dir, `
domain: example.com
region: us-east-1
`)
	chdirGH(t, dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error when github: absent: %v", err)
	}

	if len(cfg.Github.Repos) != 0 {
		t.Errorf("Github.Repos: got len=%d, want 0 (key absent => zero value)", len(cfg.Github.Repos))
	}
	if cfg.Github.DefaultProfile != "" {
		t.Errorf("Github.DefaultProfile: got %q, want empty (key absent => zero value)", cfg.Github.DefaultProfile)
	}
}

// TestLoadGitHubRepos_MergeListRegression proves the merge-list entry for "github"
// is present. This mirrors TestLoadSlackPeerBridges_Set from config_test.go:880 —
// if "github" is missing from the merge-loop allowlist, cfg.Github.Repos stays nil
// regardless of km-config.yaml content.
//
// The assertion is on len==1 specifically: if the merge-list is broken, len==0.
func TestLoadGitHubRepos_MergeListRegression(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigGH(t, dir, `
domain: example.com
region: us-east-1
github:
  repos:
    - match: "myorg/sentinel"
      alias: sentinel
`)
	chdirGH(t, dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Github.Repos == nil {
		t.Fatal("Github.Repos is nil; expected non-nil from yaml load (merge-loop must include \"github\")")
	}
	if len(cfg.Github.Repos) != 1 {
		t.Errorf("Github.Repos: got len=%d, want 1; merge-list footgun check (project_config_key_merge_list)", len(cfg.Github.Repos))
	}
	if len(cfg.Github.Repos) > 0 && cfg.Github.Repos[0].Match != "myorg/sentinel" {
		t.Errorf("Github.Repos[0].Match: got %q, want %q", cfg.Github.Repos[0].Match, "myorg/sentinel")
	}
}
