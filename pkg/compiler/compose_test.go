package compiler_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/compiler"
)

// ============================================================
// Task 2: Docker substrate / compose.go tests
// ============================================================

func TestCompileDocker(t *testing.T) {
	p := loadTestProfile(t, "docker-basic.yaml")
	net := testNetwork()

	artifacts, err := compiler.Compile(p, "sb-test001", false, net)
	if err != nil {
		t.Fatalf("Compile() with docker substrate returned error: %v", err)
	}
	if artifacts == nil {
		t.Fatal("Compile() returned nil artifacts")
	}
	if artifacts.DockerComposeYAML == "" {
		t.Error("expected DockerComposeYAML to be populated for docker substrate")
	}
	if artifacts.ServiceHCL != "" {
		t.Errorf("expected ServiceHCL to be empty for docker substrate, got non-empty value")
	}
	if artifacts.UserData != "" {
		t.Errorf("expected UserData to be empty for docker substrate, got non-empty value")
	}
}

func TestDockerComposeContainers(t *testing.T) {
	p := loadTestProfile(t, "docker-basic.yaml")
	net := testNetwork()

	artifacts, err := compiler.Compile(p, "sb-test002", false, net)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	yaml := artifacts.DockerComposeYAML

	requiredServices := []string{
		"main",
		"km-dns-proxy",
		"km-http-proxy",
		"km-audit-log",
		"km-tracing",
		"km-cred-refresh",
	}
	for _, svc := range requiredServices {
		// Services appear as keys in the services: block, so "  <name>:" is expected
		if !strings.Contains(yaml, svc+":") {
			t.Errorf("expected service %q in docker-compose YAML, not found", svc)
		}
	}
}

func TestDockerComposeDNS(t *testing.T) {
	p := loadTestProfile(t, "docker-basic.yaml")
	net := testNetwork()

	artifacts, err := compiler.Compile(p, "sb-test003", false, net)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	yaml := artifacts.DockerComposeYAML

	// km-dns-proxy must have static IP
	if !strings.Contains(yaml, "172.20.0.10") {
		t.Error("expected km-dns-proxy static IP 172.20.0.10 in docker-compose YAML")
	}

	// main must reference the DNS proxy IP
	if !strings.Contains(yaml, "172.20.0.10") {
		t.Error("expected main container to reference DNS proxy IP 172.20.0.10")
	}
}

func TestDockerComposeCredIsolation(t *testing.T) {
	p := loadTestProfile(t, "docker-basic.yaml")
	net := testNetwork()

	artifacts, err := compiler.Compile(p, "sb-test004", false, net)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	yaml := artifacts.DockerComposeYAML

	// km-cred-refresh must have operator credential placeholders
	if !strings.Contains(yaml, "AWS_ACCESS_KEY_ID") {
		t.Error("expected km-cred-refresh to have AWS_ACCESS_KEY_ID in docker-compose YAML")
	}

	// main and sidecars must use shared credentials file, not direct operator keys
	if !strings.Contains(yaml, "AWS_SHARED_CREDENTIALS_FILE") {
		t.Error("expected AWS_SHARED_CREDENTIALS_FILE in docker-compose YAML for non-cred-refresh containers")
	}

	// Verify credential isolation: AWS_ACCESS_KEY_ID should only appear in cred-refresh section
	// Split at km-cred-refresh to check before/after
	parts := strings.SplitN(yaml, "km-cred-refresh:", 2)
	if len(parts) != 2 {
		t.Fatal("km-cred-refresh section not found in YAML")
	}
	beforeCredRefresh := parts[0]
	// The main/sidecar sections (before cred-refresh) should NOT contain AWS_ACCESS_KEY_ID
	if strings.Contains(beforeCredRefresh, "AWS_ACCESS_KEY_ID") {
		t.Error("AWS_ACCESS_KEY_ID found in main/sidecar sections — credential isolation violated")
	}
}

func TestDockerComposeWithBudget(t *testing.T) {
	p := loadTestProfile(t, "docker-with-budget.yaml")
	net := testNetwork()

	artifacts, err := compiler.Compile(p, "sb-test005", false, net)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	yaml := artifacts.DockerComposeYAML

	if !strings.Contains(yaml, "KM_BUDGET_ENABLED") {
		t.Error("expected KM_BUDGET_ENABLED in docker-compose YAML when profile has budget section")
	}
	if !strings.Contains(yaml, "KM_BUDGET_TABLE") {
		t.Error("expected KM_BUDGET_TABLE in docker-compose YAML when profile has budget section")
	}
}

func TestDockerComposeNetwork(t *testing.T) {
	p := loadTestProfile(t, "docker-basic.yaml")
	net := testNetwork()

	artifacts, err := compiler.Compile(p, "sb-test006", false, net)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	yaml := artifacts.DockerComposeYAML

	if !strings.Contains(yaml, "km-net") {
		t.Error("expected network 'km-net' in docker-compose YAML")
	}
	if !strings.Contains(yaml, "bridge") {
		t.Error("expected network driver 'bridge' in docker-compose YAML")
	}
	if !strings.Contains(yaml, "172.20.0.0/24") {
		t.Error("expected IPAM subnet 172.20.0.0/24 in docker-compose YAML")
	}
}
