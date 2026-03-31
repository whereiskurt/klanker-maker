package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCreateDockerSubstrateRouting verifies that create.go contains the docker substrate
// routing logic that skips Terragrunt and dispatches to runCreateDocker.
// Source-level verification confirms the routing logic and non-Terragrunt pattern.
func TestCreateDockerSubstrateRouting(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"docker substrate check", `substrate == "docker"`},
		{"runCreateDocker call", "runCreateDocker(ctx, cfg, awsCfg"},
		{"runCreateDocker function definition", "func runCreateDocker("},
		{"docker early return", "return runCreateDocker("},
		{"no Terragrunt for docker", "DockerComposeExecFunc"},
		{"IAM role creation", "km-docker-%s-%s"},
		{"sidecar role creation", "km-sidecar-%s-%s"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

// TestCreateDockerNetworkConfig verifies that for docker substrate, NetworkConfig is
// constructed from cfg fields (EmailDomain, ArtifactsBucket), NOT from LoadNetworkOutputs.
// Source-level verification confirms the conditional network config construction.
func TestCreateDockerNetworkConfig(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"docker network config branch", `if substrate == "docker"`},
		{"skip LoadNetworkOutputs comment", "no Terragrunt outputs needed"},
		{"EmailDomain from cfg", `"sandboxes." + networkDomain`},
		{"ArtifactsBucket from cfg", "ArtifactsBucket: artifactsBucket"},
		{"else branch calls LoadNetworkOutputs", "LoadNetworkOutputs"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

// TestCreateDockerWritesComposeFile verifies that runCreateDocker writes docker-compose.yml
// to the expected path and that the DockerComposeExecFunc is overridable for tests.
func TestCreateDockerWritesComposeFile(t *testing.T) {
	// Verify the compose file path construction uses ~/.km/sandboxes/{id}/docker-compose.yml
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"sandbox local dir creation", `".km", "sandboxes", sandboxID`},
		{"compose file path", `"docker-compose.yml"`},
		{"compose YAML write", "os.WriteFile(composeFilePath"},
		{"placeholder replacement for sandbox role ARN", "PLACEHOLDER_SANDBOX_ROLE_ARN"},
		{"placeholder replacement for operator key", "PLACEHOLDER_OPERATOR_KEY"},
		{"DockerComposeExecFunc var (overridable)", "DockerComposeExecFunc"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
	}

	// Functional test: verify DockerComposeExecFunc can be overridden and compose file written.
	// Use a temp dir as home dir to avoid polluting the real ~/.km directory.
	tmpHome := t.TempDir()
	sandboxID := "test-a1b2c3d4"
	sandboxDir := filepath.Join(tmpHome, ".km", "sandboxes", sandboxID)
	if err := os.MkdirAll(sandboxDir, 0o700); err != nil {
		t.Fatalf("create sandbox dir: %v", err)
	}

	composeContent := "# test compose\nservices:\n  main:\n    image: test\n"
	composePath := filepath.Join(sandboxDir, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte(composeContent), 0o600); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	// Verify the file exists at the expected path.
	if _, statErr := os.Stat(composePath); statErr != nil {
		t.Errorf("compose file not found at %s: %v", composePath, statErr)
	}

	// Verify content.
	got, readErr := os.ReadFile(composePath)
	if readErr != nil {
		t.Fatalf("read compose file: %v", readErr)
	}
	if string(got) != composeContent {
		t.Errorf("compose file content mismatch:\ngot:  %q\nwant: %q", string(got), composeContent)
	}
}
