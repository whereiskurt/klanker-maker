package cmd_test

import (
	"os"
	"strings"
	"testing"
)

// TestStopDockerRouting verifies that stop.go contains docker substrate routing logic
// that checks S3 metadata and calls runDockerCompose with "stop" arg before EC2 API calls.
func TestStopDockerRouting(t *testing.T) {
	src, err := os.ReadFile("stop.go")
	if err != nil {
		t.Fatalf("read stop.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"S3 metadata read", "ReadSandboxMetadata"},
		{"docker substrate check", `meta.Substrate == "docker"`},
		{"calls runDockerCompose with stop", `runDockerCompose(ctx, sandboxID, "stop")`},
		{"early return before EC2", "return nil"},
		{"no EC2 for docker comment", "Docker sandboxes have no AWS-tagged EC2 resources"},
		{"stop message", "stopped"},
		{"resume hint", "km resume"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("stop.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

// TestPauseDockerRouting verifies that pause.go contains docker substrate routing logic
// that checks S3 metadata and calls runDockerCompose with "pause" arg before EC2 API calls.
func TestPauseDockerRouting(t *testing.T) {
	src, err := os.ReadFile("pause.go")
	if err != nil {
		t.Fatalf("read pause.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"S3 metadata read", "ReadSandboxMetadata"},
		{"docker substrate check", `meta.Substrate == "docker"`},
		{"calls runDockerCompose with pause", `runDockerCompose(ctx, sandboxID, "pause")`},
		{"early return before EC2", "return nil"},
		{"no EC2 for docker comment", "Docker sandboxes have no AWS-tagged EC2 resources"},
		{"pause message", "paused"},
		{"unpause hint", "km resume"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("pause.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

// TestStopDockerUsesSharedHelper verifies that stop.go uses the shared helper runDockerCompose
// from docker_helpers.go rather than duplicating docker compose invocation logic.
func TestStopDockerUsesSharedHelper(t *testing.T) {
	stopSrc, err := os.ReadFile("stop.go")
	if err != nil {
		t.Fatalf("read stop.go: %v", err)
	}
	helperSrc, err := os.ReadFile("docker_helpers.go")
	if err != nil {
		t.Fatalf("read docker_helpers.go: %v", err)
	}

	stopStr := string(stopSrc)
	helperStr := string(helperSrc)

	// stop.go must call runDockerCompose (not inline the docker compose command)
	if !strings.Contains(stopStr, "runDockerCompose") {
		t.Error("stop.go should call runDockerCompose from docker_helpers.go, not inline docker compose")
	}

	// docker_helpers.go must define runDockerCompose
	if !strings.Contains(helperStr, "func runDockerCompose(") {
		t.Error("docker_helpers.go must define runDockerCompose function")
	}
}

// TestPauseDockerUsesSharedHelper verifies that pause.go uses the shared helper runDockerCompose
// from docker_helpers.go rather than duplicating docker compose invocation logic.
func TestPauseDockerUsesSharedHelper(t *testing.T) {
	pauseSrc, err := os.ReadFile("pause.go")
	if err != nil {
		t.Fatalf("read pause.go: %v", err)
	}
	helperSrc, err := os.ReadFile("docker_helpers.go")
	if err != nil {
		t.Fatalf("read docker_helpers.go: %v", err)
	}

	pauseStr := string(pauseSrc)
	helperStr := string(helperSrc)

	// pause.go must call runDockerCompose
	if !strings.Contains(pauseStr, "runDockerCompose") {
		t.Error("pause.go should call runDockerCompose from docker_helpers.go, not inline docker compose")
	}

	// docker_helpers.go must define runDockerCompose
	if !strings.Contains(helperStr, "func runDockerCompose(") {
		t.Error("docker_helpers.go must define runDockerCompose function")
	}
}
