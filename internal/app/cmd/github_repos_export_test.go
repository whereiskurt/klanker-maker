package cmd

// Phase 97 Plan 01 Task 2 — TDD tests for KM_GITHUB_REPOS export in
// ExportTerragruntEnvVars. Mirrors the PeerBridges JSON-export pattern exactly,
// but uses json.Marshal of a structured payload instead of strings.Join.
//
// Key invariants:
//   - Gate is len(cfg.Github.Repos) > 0 (absent config ⇒ dormant, nothing exported)
//   - Value is JSON: {"repos":[...],"default_profile":"..."}
//   - env-wins: pre-set KM_GITHUB_REPOS keeps its value + drift WARN when different

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// TestGitHubEnvExport_Set verifies that when cfg.Github.Repos is non-empty and
// KM_GITHUB_REPOS is unset, ExportTerragruntEnvVars emits a valid JSON value.
func TestGitHubEnvExport_Set(t *testing.T) {
	os.Unsetenv("KM_GITHUB_REPOS")

	cfg := &config.Config{
		Github: config.GithubConfig{
			DefaultProfile: "profiles/review.yaml",
			Repos: []config.GithubRepoEntry{
				{
					Match:   "myorg/frontend",
					Alias:   "frontend",
					Profile: "profiles/frontend.yaml",
					Allow:   []string{"github.com", "registry.npmjs.org"},
				},
				{
					Match:   "myorg/backend",
					Alias:   "backend",
					Profile: "profiles/backend.yaml",
				},
			},
		},
	}

	ExportTerragruntEnvVars(cfg)

	got := os.Getenv("KM_GITHUB_REPOS")
	if got == "" {
		t.Fatal("KM_GITHUB_REPOS must be set when cfg.Github.Repos is non-empty; got empty")
	}

	// Must be valid JSON
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("KM_GITHUB_REPOS is not valid JSON: %v; raw: %s", err, got)
	}

	// Must contain repos array with 2 entries
	repos, ok := payload["repos"].([]interface{})
	if !ok {
		t.Fatalf("KM_GITHUB_REPOS .repos is not an array; payload=%s", got)
	}
	if len(repos) != 2 {
		t.Errorf("KM_GITHUB_REPOS .repos: got len=%d, want 2", len(repos))
	}

	// default_profile must be present
	if dp, _ := payload["default_profile"].(string); dp != "profiles/review.yaml" {
		t.Errorf("KM_GITHUB_REPOS .default_profile: got %q, want %q", dp, "profiles/review.yaml")
	}

	// First repo entry must round-trip match/alias fields
	if len(repos) > 0 {
		r0, _ := repos[0].(map[string]interface{})
		if r0["match"] != "myorg/frontend" {
			t.Errorf("repos[0].match: got %v, want %q", r0["match"], "myorg/frontend")
		}
		if r0["alias"] != "frontend" {
			t.Errorf("repos[0].alias: got %v, want %q", r0["alias"], "frontend")
		}
	}

	os.Unsetenv("KM_GITHUB_REPOS")
}

// TestGitHubEnvExport_Absent verifies that when cfg.Github.Repos is empty,
// KM_GITHUB_REPOS is NOT set (dormant byte-identity invariant).
func TestGitHubEnvExport_Absent(t *testing.T) {
	os.Unsetenv("KM_GITHUB_REPOS")

	cfg := &config.Config{} // Github is zero value — no repos

	ExportTerragruntEnvVars(cfg)

	if got := os.Getenv("KM_GITHUB_REPOS"); got != "" {
		t.Errorf("KM_GITHUB_REPOS must NOT be set when cfg.Github.Repos is empty; got %q", got)
	}
}

// TestGitHubEnvExport_EmptyRepos verifies that an explicit empty repos list also
// leaves KM_GITHUB_REPOS unset (same dormant path as absent github: block).
func TestGitHubEnvExport_EmptyRepos(t *testing.T) {
	os.Unsetenv("KM_GITHUB_REPOS")

	cfg := &config.Config{
		Github: config.GithubConfig{
			Repos: []config.GithubRepoEntry{}, // explicitly empty
		},
	}

	ExportTerragruntEnvVars(cfg)

	if got := os.Getenv("KM_GITHUB_REPOS"); got != "" {
		t.Errorf("KM_GITHUB_REPOS must NOT be set when Repos is empty slice; got %q", got)
	}
}

// TestGitHubEnvExport_EnvWins verifies that when KM_GITHUB_REPOS is already set
// to a different value, the env value is preserved and a drift WARN is emitted.
func TestGitHubEnvExport_EnvWins(t *testing.T) {
	const envOverride = `{"repos":[],"env_override":true}`
	os.Setenv("KM_GITHUB_REPOS", envOverride)
	defer os.Unsetenv("KM_GITHUB_REPOS")

	cfg := &config.Config{
		Github: config.GithubConfig{
			Repos: []config.GithubRepoEntry{
				{Match: "myorg/yaml-repo", Alias: "yaml"},
			},
		},
	}

	stderr := captureStderr(t, func() {
		ExportTerragruntEnvVars(cfg)
	})

	// env-wins: original env value must still be set
	if got := os.Getenv("KM_GITHUB_REPOS"); got != envOverride {
		t.Errorf("env-wins violated: KM_GITHUB_REPOS = %q; want %q", got, envOverride)
	}

	// drift WARN must mention the key
	if !strings.Contains(stderr, "KM_GITHUB_REPOS") {
		t.Errorf("expected drift WARN mentioning KM_GITHUB_REPOS in stderr; got: %s", stderr)
	}
}

// TestGitHubEnvExport_NoWarnWhenEnvMatches verifies no WARN when env already
// matches what yaml would set (idempotent re-runs of km init).
func TestGitHubEnvExport_NoWarnWhenEnvMatches(t *testing.T) {
	// Pre-compute the JSON we expect ExportTerragruntEnvVars to produce.
	type githubExportPayload struct {
		Repos          []config.GithubRepoEntry `json:"repos"`
		DefaultProfile string                   `json:"default_profile,omitempty"`
	}
	repos := []config.GithubRepoEntry{{Match: "myorg/same", Alias: "same"}}
	b, _ := json.Marshal(githubExportPayload{Repos: repos})
	jsonVal := string(b)

	os.Setenv("KM_GITHUB_REPOS", jsonVal)
	defer os.Unsetenv("KM_GITHUB_REPOS")

	cfg := &config.Config{
		Github: config.GithubConfig{Repos: repos},
	}

	stderr := captureStderr(t, func() {
		ExportTerragruntEnvVars(cfg)
	})

	if strings.Contains(stderr, "WARN: KM_GITHUB_REPOS") {
		t.Errorf("unexpected drift WARN when env matches yaml; stderr: %s", stderr)
	}
	if got := os.Getenv("KM_GITHUB_REPOS"); got != jsonVal {
		t.Errorf("KM_GITHUB_REPOS = %q; want %q (unchanged)", got, jsonVal)
	}
}
