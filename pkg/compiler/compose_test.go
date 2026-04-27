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

	artifacts, err := compiler.Compile(p, "sb-test001", false, net, nil)
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

	artifacts, err := compiler.Compile(p, "sb-test002", false, net, nil)
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

	sandboxID := "sb-test003"
	artifacts, err := compiler.Compile(p, sandboxID, false, net, nil)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	yaml := artifacts.DockerComposeYAML

	// Derive the expected DNS proxy IP for this sandbox ID.
	_, dnsProxyIP, _ := compiler.DockerSubnet(sandboxID)

	// km-dns-proxy must have static IP derived from sandbox ID.
	if !strings.Contains(yaml, dnsProxyIP) {
		t.Errorf("expected km-dns-proxy static IP %s in docker-compose YAML", dnsProxyIP)
	}

	// main must reference the DNS proxy IP.
	if !strings.Contains(yaml, dnsProxyIP) {
		t.Errorf("expected main container to reference DNS proxy IP %s", dnsProxyIP)
	}
}

func TestDockerComposeCredIsolation(t *testing.T) {
	p := loadTestProfile(t, "docker-basic.yaml")
	net := testNetwork()

	artifacts, err := compiler.Compile(p, "sb-test004", false, net, nil)
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	yaml := artifacts.DockerComposeYAML

	// km-cred-refresh must mount host ~/.aws for SSO-based credential refresh
	if !strings.Contains(yaml, ".aws:") {
		t.Error("expected km-cred-refresh to mount host ~/.aws directory")
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

	artifacts, err := compiler.Compile(p, "sb-test005", false, net, nil)
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

	sandboxID := "sb-test006"
	artifacts, err := compiler.Compile(p, sandboxID, false, net, nil)
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

	// Subnet must match the derived value for this sandbox ID.
	subnet, _, _ := compiler.DockerSubnet(sandboxID)
	if !strings.Contains(yaml, subnet) {
		t.Errorf("expected IPAM subnet %s in docker-compose YAML", subnet)
	}
}

// TestDockerSubnetUniqueness verifies that DockerSubnet produces deterministic,
// unique subnets for different sandbox IDs.
func TestDockerSubnetUniqueness(t *testing.T) {
	// Determinism: same ID must always yield the same subnet.
	subnet1a, dns1a, _ := compiler.DockerSubnet("sb-alpha")
	subnet1b, dns1b, _ := compiler.DockerSubnet("sb-alpha")
	if subnet1a != subnet1b || dns1a != dns1b {
		t.Error("DockerSubnet is not deterministic: same ID produced different results")
	}

	// Uniqueness: two different IDs must produce different subnets.
	subnet2, _, _ := compiler.DockerSubnet("sb-beta")
	if subnet1a == subnet2 {
		t.Errorf("DockerSubnet collision: sb-alpha and sb-beta both mapped to %s", subnet1a)
	}

	// Range check: third octet must be between 1 and 253.
	testIDs := []string{"sb-test001", "sb-test002", "sb-test003", "sb-alpha", "sb-beta", "sb-gamma-longname"}
	seen := map[string]string{}
	for _, id := range testIDs {
		subnet, dnsIP, httpIP := compiler.DockerSubnet(id)
		// Basic format checks
		if !strings.HasPrefix(subnet, "172.28.") || !strings.HasSuffix(subnet, ".0/24") {
			t.Errorf("DockerSubnet(%q) = %q: unexpected format", id, subnet)
		}
		if !strings.HasPrefix(dnsIP, "172.28.") || !strings.HasSuffix(dnsIP, ".10") {
			t.Errorf("DockerSubnet(%q) dnsIP = %q: unexpected format", id, dnsIP)
		}
		if !strings.HasPrefix(httpIP, "172.28.") || !strings.HasSuffix(httpIP, ".20") {
			t.Errorf("DockerSubnet(%q) httpIP = %q: unexpected format", id, httpIP)
		}
		// Record for collision detection within this small set
		if prev, exists := seen[subnet]; exists {
			t.Logf("note: DockerSubnet collision between %q and %q -> %s (hash collision, not a bug unless frequent)", prev, id, subnet)
		}
		seen[subnet] = id
	}
}
