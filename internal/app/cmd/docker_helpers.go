package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// dockerComposePath returns the path to the docker-compose.yml for a sandbox.
func dockerComposePath(sandboxID string) string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".km", "sandboxes", sandboxID, "docker-compose.yml")
}

// runDockerCompose executes a docker compose command for the given sandbox.
// args are appended after "docker compose -f {path} -p km-{sandboxID}".
func runDockerCompose(ctx context.Context, sandboxID string, args ...string) error {
	composePath := dockerComposePath(sandboxID)
	if _, err := os.Stat(composePath); os.IsNotExist(err) {
		return fmt.Errorf("docker-compose.yml not found at %s — sandbox may have been cleaned up", composePath)
	}
	allArgs := append([]string{"compose", "-f", composePath, "-p", "km-" + sandboxID}, args...)
	cmd := exec.CommandContext(ctx, "docker", allArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
