package cmd

// Phase 100 Plan 01 Task 3 — TDD tests for KM_GITHUB_PEER_BRIDGES export in
// ExportTerragruntEnvVars. Mirrors slack_peer_bridges_init_test.go exactly:
//   - Gate is len(cfg.Github.PeerBridges) > 0
//   - Value is strings.Join(PeerBridges, ",") — not strconv.FormatBool
//   - nil / empty slice leaves KM_GITHUB_PEER_BRIDGES untouched (federation off)
//   - env-wins drift WARN on env/yaml mismatch

import (
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// TestExportTerragruntEnvVars_GithubPeerBridges_Set verifies that when
// cfg.Github.PeerBridges has entries and KM_GITHUB_PEER_BRIDGES is unset,
// ExportTerragruntEnvVars sets the env var to the comma-joined list.
func TestExportTerragruntEnvVars_GithubPeerBridges_Set(t *testing.T) {
	t.Setenv("KM_GITHUB_PEER_BRIDGES", "") // ensure unset (t.Setenv restores on cleanup)
	os.Unsetenv("KM_GITHUB_PEER_BRIDGES")  // belt-and-suspenders: clear blank string

	cfg := &config.Config{}
	cfg.Github.PeerBridges = []string{
		"https://gh-abc123.lambda-url.us-east-1.on.aws/",
		"https://gh-def456.lambda-url.us-east-1.on.aws/",
	}

	ExportTerragruntEnvVars(cfg)

	got := os.Getenv("KM_GITHUB_PEER_BRIDGES")
	want := "https://gh-abc123.lambda-url.us-east-1.on.aws/,https://gh-def456.lambda-url.us-east-1.on.aws/"
	if got != want {
		t.Errorf("KM_GITHUB_PEER_BRIDGES = %q; want %q", got, want)
	}
}

// TestExportTerragruntEnvVars_GithubPeerBridges_Absent verifies that when
// cfg.Github.PeerBridges is nil, ExportTerragruntEnvVars does NOT set
// KM_GITHUB_PEER_BRIDGES (federation-off path leaves env untouched — byte-identical
// init.go env surface to Phase 97/98).
func TestExportTerragruntEnvVars_GithubPeerBridges_Absent(t *testing.T) {
	os.Unsetenv("KM_GITHUB_PEER_BRIDGES")

	cfg := &config.Config{} // PeerBridges is nil

	ExportTerragruntEnvVars(cfg)

	got := os.Getenv("KM_GITHUB_PEER_BRIDGES")
	if got != "" {
		t.Errorf("KM_GITHUB_PEER_BRIDGES should be unset when PeerBridges is nil; got %q", got)
	}
}

// TestExportTerragruntEnvVars_GithubPeerBridges_DriftWarn verifies that when
// KM_GITHUB_PEER_BRIDGES is already set to a DIFFERENT value, a drift WARN is
// printed to stderr and the env value is NOT overwritten (env-wins semantics).
func TestExportTerragruntEnvVars_GithubPeerBridges_DriftWarn(t *testing.T) {
	t.Setenv("KM_GITHUB_PEER_BRIDGES", "https://env-override.example.com/")

	cfg := &config.Config{}
	cfg.Github.PeerBridges = []string{"https://yaml-value.example.com/"}

	stderr := captureStderr(t, func() {
		ExportTerragruntEnvVars(cfg)
	})

	if !strings.Contains(stderr, "WARN: KM_GITHUB_PEER_BRIDGES=https://env-override.example.com/") {
		t.Errorf("expected drift WARN for KM_GITHUB_PEER_BRIDGES in stderr; got: %s", stderr)
	}
	if !strings.Contains(stderr, "https://yaml-value.example.com/") {
		t.Errorf("expected yaml value 'yaml-value.example.com' in drift WARN; got: %s", stderr)
	}

	// env-wins: the original env value must still be set (not overwritten by yaml).
	if got := os.Getenv("KM_GITHUB_PEER_BRIDGES"); got != "https://env-override.example.com/" {
		t.Errorf("env-wins violated: KM_GITHUB_PEER_BRIDGES = %q; want env value", got)
	}
}

// TestExportTerragruntEnvVars_GithubPeerBridges_NoOverwriteWhenEnvMatches verifies
// that when KM_GITHUB_PEER_BRIDGES is already set to the SAME value as yaml, no
// WARN is emitted and the env var is left unchanged.
func TestExportTerragruntEnvVars_GithubPeerBridges_NoOverwriteWhenEnvMatches(t *testing.T) {
	want := "https://gh-abc123.lambda-url.us-east-1.on.aws/"
	t.Setenv("KM_GITHUB_PEER_BRIDGES", want)

	cfg := &config.Config{}
	cfg.Github.PeerBridges = []string{want}

	stderr := captureStderr(t, func() {
		ExportTerragruntEnvVars(cfg)
	})

	if strings.Contains(stderr, "WARN: KM_GITHUB_PEER_BRIDGES") {
		t.Errorf("unexpected drift WARN when env matches yaml; stderr: %s", stderr)
	}

	if got := os.Getenv("KM_GITHUB_PEER_BRIDGES"); got != want {
		t.Errorf("KM_GITHUB_PEER_BRIDGES = %q; want %q (unchanged)", got, want)
	}
}
