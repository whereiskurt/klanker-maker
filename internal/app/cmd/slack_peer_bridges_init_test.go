package cmd

// Phase 95 Plan 01 Task 2 — TDD tests for KM_SLACK_PEER_BRIDGES export in
// ExportTerragruntEnvVars. Mirrors the drift-WARN test shape in init_84_3_test.go
// (ExportTerragruntEnvVars_DriftWarn) and the MentionOnly/ReactAlways *bool pattern.
//
// Key differences from *bool fields:
//   - Gate is len(cfg.Slack.PeerBridges) > 0
//   - Value is strings.Join(PeerBridges, ",") — not strconv.FormatBool
//   - nil / empty slice leaves KM_SLACK_PEER_BRIDGES untouched (federation off)

import (
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// TestExportTerragruntEnvVars_PeerBridges_Set verifies that when
// cfg.Slack.PeerBridges has entries and KM_SLACK_PEER_BRIDGES is unset,
// ExportTerragruntEnvVars sets the env var to the comma-joined list.
func TestExportTerragruntEnvVars_PeerBridges_Set(t *testing.T) {
	t.Setenv("KM_SLACK_PEER_BRIDGES", "") // ensure unset (t.Setenv restores on cleanup)
	os.Unsetenv("KM_SLACK_PEER_BRIDGES")  // belt-and-suspenders: clear blank string

	cfg := &config.Config{}
	cfg.Slack.PeerBridges = []string{
		"https://abc123.lambda-url.us-east-1.on.aws/events",
		"https://def456.lambda-url.us-east-1.on.aws/events",
	}

	ExportTerragruntEnvVars(cfg)

	got := os.Getenv("KM_SLACK_PEER_BRIDGES")
	want := "https://abc123.lambda-url.us-east-1.on.aws/events,https://def456.lambda-url.us-east-1.on.aws/events"
	if got != want {
		t.Errorf("KM_SLACK_PEER_BRIDGES = %q; want %q", got, want)
	}
}

// TestExportTerragruntEnvVars_PeerBridges_Absent verifies that when
// cfg.Slack.PeerBridges is nil, ExportTerragruntEnvVars does NOT set
// KM_SLACK_PEER_BRIDGES (federation-off path leaves env untouched).
func TestExportTerragruntEnvVars_PeerBridges_Absent(t *testing.T) {
	os.Unsetenv("KM_SLACK_PEER_BRIDGES")

	cfg := &config.Config{} // PeerBridges is nil

	ExportTerragruntEnvVars(cfg)

	got := os.Getenv("KM_SLACK_PEER_BRIDGES")
	if got != "" {
		t.Errorf("KM_SLACK_PEER_BRIDGES should be unset when PeerBridges is nil; got %q", got)
	}
}

// TestExportTerragruntEnvVars_PeerBridges_DriftWarn verifies that when
// KM_SLACK_PEER_BRIDGES is already set to a DIFFERENT value, a drift WARN is
// printed to stderr and the env value is NOT overwritten (env-wins semantics).
func TestExportTerragruntEnvVars_PeerBridges_DriftWarn(t *testing.T) {
	t.Setenv("KM_SLACK_PEER_BRIDGES", "https://env-override.example.com/events")

	cfg := &config.Config{}
	cfg.Slack.PeerBridges = []string{"https://yaml-value.example.com/events"}

	stderr := captureStderr(t, func() {
		ExportTerragruntEnvVars(cfg)
	})

	// WARN must mention the env value and the yaml value.
	if !strings.Contains(stderr, "WARN: KM_SLACK_PEER_BRIDGES=https://env-override.example.com/events") {
		t.Errorf("expected drift WARN for KM_SLACK_PEER_BRIDGES in stderr; got: %s", stderr)
	}
	if !strings.Contains(stderr, "yaml.peer_bridges") || !strings.Contains(stderr, "km-config.yaml") {
		// Accept either yaml.peer_bridges or the yaml value itself in the warn text.
		if !strings.Contains(stderr, "https://yaml-value.example.com/events") {
			t.Errorf("expected yaml value 'yaml-value.example.com' in drift WARN; got: %s", stderr)
		}
	}

	// env-wins: the original env value must still be set (not overwritten by yaml).
	if got := os.Getenv("KM_SLACK_PEER_BRIDGES"); got != "https://env-override.example.com/events" {
		t.Errorf("env-wins violated: KM_SLACK_PEER_BRIDGES = %q; want env value", got)
	}
}

// TestExportTerragruntEnvVars_PeerBridges_NoOverwriteWhenEnvMatches verifies that
// when KM_SLACK_PEER_BRIDGES is already set to the SAME value as yaml, no WARN
// is emitted and the env var is left unchanged.
func TestExportTerragruntEnvVars_PeerBridges_NoOverwriteWhenEnvMatches(t *testing.T) {
	want := "https://abc123.lambda-url.us-east-1.on.aws/events"
	t.Setenv("KM_SLACK_PEER_BRIDGES", want)

	cfg := &config.Config{}
	cfg.Slack.PeerBridges = []string{want}

	stderr := captureStderr(t, func() {
		ExportTerragruntEnvVars(cfg)
	})

	// No WARN when values agree.
	if strings.Contains(stderr, "WARN: KM_SLACK_PEER_BRIDGES") {
		t.Errorf("unexpected drift WARN when env matches yaml; stderr: %s", stderr)
	}

	// Env var must still hold the original value.
	if got := os.Getenv("KM_SLACK_PEER_BRIDGES"); got != want {
		t.Errorf("KM_SLACK_PEER_BRIDGES = %q; want %q (unchanged)", got, want)
	}
}
