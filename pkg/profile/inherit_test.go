package profile

import (
	"strings"
	"testing"
)

func TestResolveInheritsParentValues(t *testing.T) {
	// Child extends open-dev with TTL override but omits some parent values
	p, err := Resolve("child-of-open-dev", []string{"../../testdata/profiles"})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	// Child specifies TTL=2h, should override parent's 24h
	if p.Spec.Lifecycle.TTL != "2h" {
		t.Errorf("expected child TTL 2h, got %s", p.Spec.Lifecycle.TTL)
	}
	// Child specifies IdleTimeout=30m, should override parent's 4h
	if p.Spec.Lifecycle.IdleTimeout != "30m" {
		t.Errorf("expected child IdleTimeout 30m, got %s", p.Spec.Lifecycle.IdleTimeout)
	}
}

func TestResolveChildOverridesParent(t *testing.T) {
	p, err := Resolve("child-of-open-dev", []string{"../../testdata/profiles"})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	// Child's allowedDNSSuffixes should REPLACE parent's, not union
	if len(p.Spec.Network.Egress.AllowedDNSSuffixes) != 1 {
		t.Fatalf("expected 1 DNS suffix, got %d: %v", len(p.Spec.Network.Egress.AllowedDNSSuffixes), p.Spec.Network.Egress.AllowedDNSSuffixes)
	}
	if p.Spec.Network.Egress.AllowedDNSSuffixes[0] != ".example.com" {
		t.Errorf("expected .example.com, got %s", p.Spec.Network.Egress.AllowedDNSSuffixes[0])
	}
}

func TestResolveChildWinsScalar(t *testing.T) {
	p, err := Resolve("child-of-open-dev", []string{"../../testdata/profiles"})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	// Both parent and child have substrate=ec2 — child wins
	if p.Spec.Runtime.Substrate != "ec2" {
		t.Errorf("expected substrate ec2, got %s", p.Spec.Runtime.Substrate)
	}
}

func TestResolveCircularDetection(t *testing.T) {
	_, err := Resolve("circular-a", []string{"../../testdata/profiles"})
	if err == nil {
		t.Fatal("expected error for circular inheritance, got nil")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("expected error containing 'circular', got: %s", err.Error())
	}
}

func TestResolveDepthExceeded(t *testing.T) {
	// depth-4 extends depth-3 extends depth-2 extends depth-1 = 4 levels > max 3
	_, err := Resolve("depth-4", []string{"../../testdata/profiles"})
	if err == nil {
		t.Fatal("expected error for depth exceeded, got nil")
	}
	if !strings.Contains(err.Error(), "depth exceeded") && !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected error about depth or missing parent, got: %s", err.Error())
	}
}

func TestResolveMaxDepth3(t *testing.T) {
	// open-dev (builtin, depth 0) -> child-of-open-dev (depth 1) = 2 levels, should succeed
	p, err := Resolve("child-of-open-dev", []string{"../../testdata/profiles"})
	if err != nil {
		t.Fatalf("expected 2-level chain to succeed, got error: %v", err)
	}
	if p.Metadata.Name != "child-of-open-dev" {
		t.Errorf("expected name child-of-open-dev, got %s", p.Metadata.Name)
	}
}

func TestResolveNonExistentParent(t *testing.T) {
	_, err := Resolve("nonexistent-profile", []string{"../../testdata/profiles"})
	if err == nil {
		t.Fatal("expected error for nonexistent profile, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected error containing 'not found', got: %s", err.Error())
	}
}

func TestResolveMetadataLabelsAdditive(t *testing.T) {
	p, err := Resolve("child-of-open-dev", []string{"../../testdata/profiles"})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	// Parent (open-dev) has labels: tier=development, builtin=true
	// Child has labels: tier=development, custom=true
	// Merged should have all three: tier=development (child wins), builtin=true (from parent), custom=true (from child)
	if p.Metadata.Labels["builtin"] != "true" {
		t.Errorf("expected parent label 'builtin=true' to be inherited, got %v", p.Metadata.Labels)
	}
	if p.Metadata.Labels["custom"] != "true" {
		t.Errorf("expected child label 'custom=true' to be present, got %v", p.Metadata.Labels)
	}
}

func TestResolveExtendsCleared(t *testing.T) {
	p, err := Resolve("child-of-open-dev", []string{"../../testdata/profiles"})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if p.Extends != "" {
		t.Errorf("expected extends to be cleared after resolution, got %q", p.Extends)
	}
}
