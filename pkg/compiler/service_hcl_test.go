package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// baseECSProfile returns a minimal SandboxProfile for ECS service.hcl tests.
func baseECSProfile() *profile.SandboxProfile {
	return &profile.SandboxProfile{
		Metadata: profile.Metadata{Name: "test-ecs-profile"},
		Spec: profile.Spec{
			Runtime: profile.RuntimeSpec{
				Substrate:    "ecs",
				Region:       "us-east-1",
				InstanceType: "512/1024",
			},
			Network: profile.NetworkSpec{
				Egress: profile.EgressSpec{
					AllowedDNSSuffixes: []string{"example.com"},
					AllowedHosts:       []string{"api.example.com"},
				},
			},
		},
	}
}

func baseECSNetwork() *NetworkConfig {
	return &NetworkConfig{
		VPCID:         "vpc-12345",
		PublicSubnets: []string{"subnet-a", "subnet-b"},
		RegionLabel:   "use1",
	}
}

// TestECSReadonlyRootFilesystem verifies readonlyRootFilesystem=true is set when filesystemPolicy is configured.
func TestECSReadonlyRootFilesystem(t *testing.T) {
	p := baseECSProfile()
	p.Spec.Policy = profile.PolicySpec{
		FilesystemPolicy: &profile.FilesystemPolicy{
			ReadOnlyPaths: []string{"/etc"},
			WritablePaths: []string{"/tmp"},
		},
	}
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	if !strings.Contains(out, "readonlyRootFilesystem") {
		t.Error("expected readonlyRootFilesystem in ECS service.hcl when filesystemPolicy is set")
	}
	if !strings.Contains(out, "readonlyRootFilesystem = true") {
		t.Error("expected readonlyRootFilesystem = true")
	}
}

// TestECSReadonlyRootFilesystemAbsent verifies readonlyRootFilesystem is NOT set without filesystemPolicy.
func TestECSReadonlyRootFilesystemAbsent(t *testing.T) {
	p := baseECSProfile()
	// No FilesystemPolicy
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	if strings.Contains(out, "readonlyRootFilesystem") {
		t.Error("expected NO readonlyRootFilesystem when filesystemPolicy is nil")
	}
}

// TestECSWritableVolumes verifies named volumes are added for writablePaths.
func TestECSWritableVolumes(t *testing.T) {
	p := baseECSProfile()
	p.Spec.Policy = profile.PolicySpec{
		FilesystemPolicy: &profile.FilesystemPolicy{
			ReadOnlyPaths: []string{"/etc"},
			WritablePaths: []string{"/tmp", "/workspace"},
		},
	}
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	// Should contain volumes section
	if !strings.Contains(out, "volumes") {
		t.Error("expected volumes section in ECS service.hcl when writablePaths are set")
	}
	// Should contain mountPoints
	if !strings.Contains(out, "mountPoints") {
		t.Error("expected mountPoints in main container when writablePaths are set")
	}
	if !strings.Contains(out, "/workspace") {
		t.Error("expected /workspace volume mount")
	}
}

// TestECSTmpAutoInjected verifies /tmp is auto-injected as writable when readonlyRootFilesystem is true.
func TestECSTmpAutoInjected(t *testing.T) {
	p := baseECSProfile()
	p.Spec.Policy = profile.PolicySpec{
		FilesystemPolicy: &profile.FilesystemPolicy{
			ReadOnlyPaths: []string{"/etc"},
			WritablePaths: []string{"/workspace"}, // /tmp NOT listed
		},
	}
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	if !strings.Contains(out, "/tmp") {
		t.Error("expected /tmp to be auto-injected as writable volume when readonlyRootFilesystem is true")
	}
}

// baseEC2Profile returns a minimal SandboxProfile for EC2 service.hcl tests.
func baseEC2Profile() *profile.SandboxProfile {
	return &profile.SandboxProfile{
		Metadata: profile.Metadata{Name: "test-ec2-profile"},
		Spec: profile.Spec{
			Runtime: profile.RuntimeSpec{
				Substrate:    "ec2",
				Spot:         true,
				InstanceType: "t3.medium",
				Region:       "us-east-1",
			},
			Budget: &profile.BudgetSpec{
				WarningThreshold: 0.8,
				Compute: &profile.ComputeBudget{
					MaxSpendUSD: 5.00,
				},
			},
		},
	}
}

func baseEC2Network() *NetworkConfig {
	return &NetworkConfig{
		VPCID:             "vpc-12345",
		PublicSubnets:     []string{"subnet-a", "subnet-b"},
		AvailabilityZones: []string{"us-east-1a", "us-east-1b"},
		RegionLabel:       "use1",
	}
}

// TestSpotRateEC2NonZero verifies that a NetworkConfig with non-zero SpotRateUSD
// produces spot_rate != 0.0 in the EC2 service.hcl output (BUDG-03).
func TestSpotRateEC2NonZero(t *testing.T) {
	p := baseEC2Profile()
	net := baseEC2Network()
	net.SpotRateUSD = 0.0416 // injected by create.go before Compile()

	iamPolicy := &IAMSessionPolicy{
		MaxSessionDuration: 3600,
		AllowedRegions:     []string{"us-east-1"},
	}

	out, err := generateEC2ServiceHCL(p, "test-sb", true, nil, iamPolicy, "", net)
	if err != nil {
		t.Fatalf("generateEC2ServiceHCL failed: %v", err)
	}

	// budget_enforcer_inputs must be present
	if !strings.Contains(out, "budget_enforcer_inputs") {
		t.Fatal("expected budget_enforcer_inputs block in EC2 service.hcl when budget is set")
	}
	// spot_rate must reflect the non-zero value
	if !strings.Contains(out, "spot_rate      = 0.0416") {
		t.Errorf("expected spot_rate = 0.0416 in EC2 service.hcl, got:\n%s", out)
	}
	// must NOT contain the hardcoded zero
	if strings.Contains(out, "spot_rate      = 0\n") || strings.Contains(out, "spot_rate      = 0.0\n") {
		t.Error("found hardcoded spot_rate = 0.0 in EC2 service.hcl — SpotRateUSD not threaded through")
	}
}

// TestSpotRateEC2ZeroFallback verifies that SpotRateUSD=0.0 (no budget or failed lookup)
// still renders correctly (backward-compatible zero value).
func TestSpotRateEC2ZeroFallback(t *testing.T) {
	p := baseEC2Profile()
	net := baseEC2Network()
	net.SpotRateUSD = 0.0 // no rate resolved

	iamPolicy := &IAMSessionPolicy{
		MaxSessionDuration: 3600,
		AllowedRegions:     []string{"us-east-1"},
	}

	out, err := generateEC2ServiceHCL(p, "test-sb", true, nil, iamPolicy, "", net)
	if err != nil {
		t.Fatalf("generateEC2ServiceHCL failed: %v", err)
	}
	if !strings.Contains(out, "budget_enforcer_inputs") {
		t.Fatal("expected budget_enforcer_inputs block in EC2 service.hcl when budget is set")
	}
	// spot_rate = 0 is acceptable (zero value renders as 0 in Go templates)
	if !strings.Contains(out, "spot_rate") {
		t.Error("expected spot_rate field in budget_enforcer_inputs")
	}
}

// TestECSServiceHCLImageURIs verifies ECR URIs are emitted when KM_ACCOUNTS_APPLICATION is set.
func TestECSServiceHCLImageURIs(t *testing.T) {
	t.Setenv("KM_ACCOUNTS_APPLICATION", "123456789012")
	t.Setenv("KM_SIDECAR_VERSION", "")

	p := baseECSProfile()
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}

	// Must contain real ECR URIs for all 4 sidecars
	for _, sidecar := range []string{"dns-proxy", "http-proxy", "audit-log", "tracing"} {
		expected := "123456789012.dkr.ecr.us-east-1.amazonaws.com/km-" + sidecar + ":"
		if !strings.Contains(out, expected) {
			t.Errorf("expected ECR URI %q in output, got:\n%s", expected, out)
		}
	}

	// Must NOT contain any ${var.*_image} literals
	for _, bad := range []string{"${var.dns_proxy_image}", "${var.http_proxy_image}", "${var.audit_log_image}", "${var.tracing_image}"} {
		if strings.Contains(out, bad) {
			t.Errorf("found broken HCL literal %q in output — should have been replaced with ECR URI", bad)
		}
	}
}

// TestECSServiceHCLImageURIsPlaceholder verifies PLACEHOLDER_ECR prefix when KM_ACCOUNTS_APPLICATION is empty.
func TestECSServiceHCLImageURIsPlaceholder(t *testing.T) {
	t.Setenv("KM_ACCOUNTS_APPLICATION", "")

	p := baseECSProfile()
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}

	// Must contain PLACEHOLDER_ECR/ prefix for each sidecar
	for _, sidecar := range []string{"dns-proxy", "http-proxy", "audit-log", "tracing"} {
		expected := "PLACEHOLDER_ECR/km-" + sidecar + ":"
		if !strings.Contains(out, expected) {
			t.Errorf("expected placeholder URI %q in output, got:\n%s", expected, out)
		}
	}

	// Must NOT contain any ${var.*_image} literals
	for _, bad := range []string{"${var.dns_proxy_image}", "${var.http_proxy_image}", "${var.audit_log_image}", "${var.tracing_image}"} {
		if strings.Contains(out, bad) {
			t.Errorf("found broken HCL literal %q in output — placeholder did not replace it", bad)
		}
	}
}

// TestECSServiceHCLImageVersion verifies KM_SIDECAR_VERSION controls the image tag.
func TestECSServiceHCLImageVersion(t *testing.T) {
	t.Setenv("KM_ACCOUNTS_APPLICATION", "123456789012")
	t.Setenv("KM_SIDECAR_VERSION", "v1.2.3")

	p := baseECSProfile()
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}

	// All sidecar images must end with :v1.2.3
	for _, sidecar := range []string{"dns-proxy", "http-proxy", "audit-log", "tracing"} {
		expected := "km-" + sidecar + ":v1.2.3"
		if !strings.Contains(out, expected) {
			t.Errorf("expected image tag :v1.2.3 for %s, got:\n%s", sidecar, out)
		}
	}

	// Must NOT contain :latest when version is explicitly set
	if strings.Contains(out, ":latest") {
		t.Error("found :latest tag in output when KM_SIDECAR_VERSION=v1.2.3 — version not applied")
	}
}

// TestSpotRateECSNonZero verifies that a NetworkConfig with non-zero SpotRateUSD
// produces spot_rate != 0.0 in the ECS service.hcl output (BUDG-03).
func TestSpotRateECSNonZero(t *testing.T) {
	p := baseECSProfile()
	p.Spec.Budget = &profile.BudgetSpec{
		WarningThreshold: 0.8,
		Compute: &profile.ComputeBudget{
			MaxSpendUSD: 5.00,
		},
	}
	net := baseECSNetwork()
	net.SpotRateUSD = 0.0312

	out, err := generateECSServiceHCL(p, "test-sb", false, nil, net)
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}

	if !strings.Contains(out, "budget_enforcer_inputs") {
		t.Fatal("expected budget_enforcer_inputs block in ECS service.hcl when budget is set")
	}
	if !strings.Contains(out, "spot_rate     = 0.0312") {
		t.Errorf("expected spot_rate = 0.0312 in ECS service.hcl, got:\n%s", out)
	}
}
