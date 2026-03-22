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
