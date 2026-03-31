package cmd_test

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// TestShellDockerContainerName verifies that when substrate is "docker", shell
// dispatches docker exec with container name km-{sandboxID}-main.
func TestShellDockerContainerName(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	fetcher := &fakeShellFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-docker-1",
			Profile:   "docker-dev",
			Substrate: "docker",
			Region:    "local",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}

	var capturedArgs []string
	err := runShellCmd(t, fetcher, &capturedArgs, "sb-docker-1")
	if err != nil {
		t.Fatalf("shell command returned error: %v", err)
	}

	fullCmd := strings.Join(capturedArgs, " ")

	// Must use docker exec (not aws ssm or aws ecs)
	if capturedArgs[0] != "docker" {
		t.Errorf("expected command 'docker', got: %s", capturedArgs[0])
	}
	if !strings.Contains(fullCmd, "exec") {
		t.Errorf("expected 'exec' in docker command, got: %s", fullCmd)
	}

	// Container name must be km-{sandboxID}-main
	expectedContainer := "km-sb-docker-1-main"
	if !strings.Contains(fullCmd, expectedContainer) {
		t.Errorf("expected container name %q in args, got: %s", expectedContainer, fullCmd)
	}

	// Must include /bin/bash
	if !strings.Contains(fullCmd, "/bin/bash") {
		t.Errorf("expected '/bin/bash' in docker exec args, got: %s", fullCmd)
	}
}

// TestShellDockerRootFlag verifies that --root passes -u root before the container name.
func TestShellDockerRootFlag(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	fetcher := &fakeShellFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-docker-2",
			Profile:   "docker-dev",
			Substrate: "docker",
			Region:    "local",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}

	var capturedArgs []string
	err := runShellCmd(t, fetcher, &capturedArgs, "--root", "sb-docker-2")
	if err != nil {
		t.Fatalf("shell --root command returned error: %v", err)
	}

	fullCmd := strings.Join(capturedArgs, " ")

	// Must include -u root
	if !strings.Contains(fullCmd, "-u") {
		t.Errorf("expected '-u' in docker exec args for root, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "root") {
		t.Errorf("expected 'root' in docker exec args, got: %s", fullCmd)
	}

	// -u root must appear before the container name
	uIdx := indexOf(capturedArgs, "-u")
	rootIdx := indexOf(capturedArgs, "root")
	containerIdx := indexOf(capturedArgs, "km-sb-docker-2-main")
	if uIdx < 0 || rootIdx < 0 || containerIdx < 0 {
		t.Fatalf("could not find -u / root / container in args: %v", capturedArgs)
	}
	if !(uIdx < containerIdx && rootIdx < containerIdx) {
		t.Errorf("-u root must appear before container name; args: %v", capturedArgs)
	}
}

// TestShellDockerNoRootFlag verifies that without --root, args do NOT include -u root.
func TestShellDockerNoRootFlag(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	fetcher := &fakeShellFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-docker-3",
			Profile:   "docker-dev",
			Substrate: "docker",
			Region:    "local",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}

	var capturedArgs []string
	err := runShellCmd(t, fetcher, &capturedArgs, "sb-docker-3")
	if err != nil {
		t.Fatalf("shell command returned error: %v", err)
	}

	fullCmd := strings.Join(capturedArgs, " ")

	// Must NOT include -u root when --root is not passed
	if strings.Contains(fullCmd, "-u") {
		t.Errorf("expected NO '-u' in docker exec args without --root, got: %s", fullCmd)
	}
}

// TestShellDockerRouting verifies that a docker substrate sandbox dispatches to docker exec
// (not aws ssm or aws ecs execute-command).
func TestShellDockerRouting(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	fetcher := &fakeShellFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-docker-4",
			Profile:   "docker-dev",
			Substrate: "docker",
			Region:    "local",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}

	var capturedArgs []string
	err := runShellCmd(t, fetcher, &capturedArgs, "sb-docker-4")
	if err != nil {
		t.Fatalf("shell command returned error: %v", err)
	}

	fullCmd := strings.Join(capturedArgs, " ")

	// Must NOT dispatch to aws ssm
	if strings.Contains(fullCmd, "ssm") {
		t.Errorf("docker substrate must NOT dispatch to SSM, got: %s", fullCmd)
	}

	// Must NOT dispatch to aws ecs execute-command
	if strings.Contains(fullCmd, "execute-command") {
		t.Errorf("docker substrate must NOT dispatch to ECS execute-command, got: %s", fullCmd)
	}

	// Must dispatch to docker exec
	if capturedArgs[0] != "docker" {
		t.Errorf("docker substrate must dispatch to 'docker', got: %s", capturedArgs[0])
	}
}

// indexOf returns the index of the first occurrence of val in slice, or -1.
func indexOf(slice []string, val string) int {
	for i, v := range slice {
		if v == val {
			return i
		}
	}
	return -1
}

// Compile-time check: ShellExecFunc works with exec.Cmd.
var _ = func(c *exec.Cmd) error { return nil }
