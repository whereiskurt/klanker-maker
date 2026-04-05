package cmd_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// TestShellLearnFlagExists verifies that the --learn bool flag is registered
// on the km shell command.
func TestShellLearnFlagExists(t *testing.T) {
	cfg := &config.Config{}
	shellCmd := cmd.NewShellCmdWithFetcher(cfg, nil, nil)

	flag := shellCmd.Flags().Lookup("learn")
	if flag == nil {
		t.Fatal("expected --learn flag to be registered on km shell, but it was not found")
	}
	if flag.Value.Type() != "bool" {
		t.Errorf("expected --learn to be a bool flag, got type %q", flag.Value.Type())
	}
}

// TestLearnOutputPath verifies that --learn-output flag is registered and
// has the correct default value.
func TestLearnOutputPath(t *testing.T) {
	cfg := &config.Config{}
	shellCmd := cmd.NewShellCmdWithFetcher(cfg, nil, nil)

	flag := shellCmd.Flags().Lookup("learn-output")
	if flag == nil {
		t.Fatal("expected --learn-output flag to be registered on km shell, but it was not found")
	}
	if flag.DefValue != "observed-profile.yaml" {
		t.Errorf("expected default learn-output to be 'observed-profile.yaml', got %q", flag.DefValue)
	}

	// Verify setting the flag value captures correctly.
	root := &cobra.Command{Use: "km"}
	root.AddCommand(cmd.NewShellCmdWithFetcher(cfg, nil, nil))
	root.SetArgs([]string{"shell", "--learn-output", "/tmp/test-profile.yaml", "some-id"})
	// We don't execute (no fetcher), just parse flags.
	_ = root.ParseFlags([]string{"shell", "--learn-output", "/tmp/test-profile.yaml"})
	// Flag value check via the shell command's flag
	shellCmd2 := cmd.NewShellCmdWithFetcher(cfg, nil, nil)
	if err := shellCmd2.ParseFlags([]string{"--learn-output", "/tmp/test-profile.yaml"}); err != nil {
		t.Fatalf("ParseFlags error: %v", err)
	}
	got := shellCmd2.Flags().Lookup("learn-output").Value.String()
	if got != "/tmp/test-profile.yaml" {
		t.Errorf("expected /tmp/test-profile.yaml, got %q", got)
	}
}

// TestGenerateProfileFromObservedJSON verifies that generateProfileFromJSON
// produces valid YAML with expected allowedDNSSuffixes, allowedHosts, and allowedRepos.
func TestGenerateProfileFromObservedJSON(t *testing.T) {
	input := []byte(`{"dns":["api.github.com","pypi.org"],"hosts":["registry.npmjs.org"],"repos":["octocat/hello"]}`)

	yamlBytes, err := cmd.GenerateProfileFromJSON(input, "")
	if err != nil {
		t.Fatalf("generateProfileFromJSON returned error: %v", err)
	}

	yaml := string(yamlBytes)

	// DNS suffixes: api.github.com -> .github.com, pypi.org -> .pypi.org
	// Also registry.npmjs.org contributes .npmjs.org via HTTPS-implies-DNS.
	if !strings.Contains(yaml, ".github.com") {
		t.Errorf("expected .github.com in AllowedDNSSuffixes, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, ".pypi.org") {
		t.Errorf("expected .pypi.org in AllowedDNSSuffixes, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "octocat/hello") {
		t.Errorf("expected octocat/hello in allowedRepos, got:\n%s", yaml)
	}
}

// TestGenerateProfileFromObservedJSON_Empty verifies that empty observation
// arrays produce a valid profile with empty allowlists (not nil).
func TestGenerateProfileFromObservedJSON_Empty(t *testing.T) {
	input := []byte(`{"dns":[],"hosts":[],"repos":[]}`)

	yamlBytes, err := cmd.GenerateProfileFromJSON(input, "")
	if err != nil {
		t.Fatalf("generateProfileFromJSON returned error: %v", err)
	}

	yaml := string(yamlBytes)
	if yaml == "" {
		t.Fatal("expected non-empty YAML output")
	}

	// Verify the output is valid YAML (doesn't crash and has content).
	if !strings.Contains(yaml, "apiVersion") {
		t.Errorf("expected apiVersion in output, got:\n%s", yaml)
	}
}

// TestGenerateProfileFromObservedJSON_Annotated verifies that the annotate flag
// produces comments with domain-to-suffix mappings.
func TestGenerateProfileFromObservedJSON_Annotated(t *testing.T) {
	input := []byte(`{"dns":["api.github.com","codeload.github.com","pypi.org"],"hosts":["registry.npmjs.org"],"repos":[]}`)

	yamlBytes, err := cmd.GenerateProfileFromJSON(input, "", true)
	if err != nil {
		t.Fatalf("GenerateProfileFromJSON annotated returned error: %v", err)
	}

	yaml := string(yamlBytes)
	if !strings.Contains(yaml, "--learn-annotate") {
		t.Error("expected --learn-annotate annotation header in output")
	}
	if !strings.Contains(yaml, "Observed:") {
		t.Error("expected 'Observed:' summary line")
	}
	if !strings.Contains(yaml, "api.github.com") {
		t.Error("expected api.github.com in domain mapping")
	}
	// Must still be valid YAML.
	if !strings.Contains(yaml, "apiVersion") {
		t.Error("expected apiVersion in annotated output")
	}
}

// TestLearnAnnotateFlagExists verifies --learn-annotate flag is registered.
func TestLearnAnnotateFlagExists(t *testing.T) {
	cfg := &config.Config{}
	shellCmd := cmd.NewShellCmdWithFetcher(cfg, nil, nil)

	flag := shellCmd.Flags().Lookup("learn-annotate")
	if flag == nil {
		t.Fatal("expected --learn-annotate flag to be registered on km shell, but it was not found")
	}
	if flag.Value.Type() != "bool" {
		t.Errorf("expected --learn-annotate to be a bool flag, got type %q", flag.Value.Type())
	}
}

// TestCollectDockerObservations verifies that collectDockerObservations parses
// DNS and HTTP proxy log output and returns valid observed JSON.
func TestCollectDockerObservations(t *testing.T) {
	dnsLogs := strings.NewReader(`{"level":"info","event_type":"dns_query","domain":"api.github.com","allowed":true}
{"level":"info","event_type":"dns_query","domain":"pypi.org","allowed":true}`)

	httpLogs := strings.NewReader(`{"level":"info","event_type":"github_repo_allowed","owner":"octocat","repo":"hello"}
{"level":"info","event_type":"github_mitm_connect","host":"registry.npmjs.org:443"}`)

	data, err := cmd.CollectDockerObservations("test-sandbox", dnsLogs, httpLogs)
	if err != nil {
		t.Fatalf("collectDockerObservations returned error: %v", err)
	}

	var state struct {
		DNS   []string `json:"dns"`
		Hosts []string `json:"hosts"`
		Repos []string `json:"repos"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("failed to unmarshal result: %v\ndata: %s", err, data)
	}

	if len(state.DNS) != 2 {
		t.Errorf("expected 2 DNS domains, got %d: %v", len(state.DNS), state.DNS)
	}
	if len(state.Repos) != 1 {
		t.Errorf("expected 1 repo, got %d: %v", len(state.Repos), state.Repos)
	}
	if len(state.Hosts) != 1 {
		t.Errorf("expected 1 host, got %d: %v", len(state.Hosts), state.Hosts)
	}
}
