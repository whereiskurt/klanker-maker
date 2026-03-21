package profile

import (
	"testing"
)

func TestBuiltinProfilesExist(t *testing.T) {
	names := ListBuiltins()
	expected := []string{"open-dev", "restricted-dev", "hardened", "sealed"}
	if len(names) != len(expected) {
		t.Fatalf("expected %d built-in profiles, got %d: %v", len(expected), len(names), names)
	}
	for i, name := range expected {
		if names[i] != name {
			t.Errorf("expected profile %d to be %q, got %q", i, name, names[i])
		}
	}
}

func TestBuiltinProfilesValidate(t *testing.T) {
	for _, name := range ListBuiltins() {
		t.Run(name, func(t *testing.T) {
			p, err := LoadBuiltin(name)
			if err != nil {
				t.Fatalf("LoadBuiltin(%q) failed: %v", name, err)
			}
			// Re-serialize and validate to test full schema compliance
			_ = p // Parse succeeded, now validate the raw YAML
			data, err := builtinFS.ReadFile("builtins/" + name + ".yaml")
			if err != nil {
				t.Fatalf("reading raw builtin %q: %v", name, err)
			}
			errs := Validate(data)
			if len(errs) > 0 {
				for _, e := range errs {
					t.Errorf("validation error: %s", e.Error())
				}
			}
		})
	}
}

func TestBuiltinOpenDevTTL(t *testing.T) {
	p, err := LoadBuiltin("open-dev")
	if err != nil {
		t.Fatalf("LoadBuiltin failed: %v", err)
	}
	if p.Spec.Lifecycle.TTL != "24h" {
		t.Errorf("expected open-dev TTL 24h, got %s", p.Spec.Lifecycle.TTL)
	}
}

func TestBuiltinRestrictedDevTTL(t *testing.T) {
	p, err := LoadBuiltin("restricted-dev")
	if err != nil {
		t.Fatalf("LoadBuiltin failed: %v", err)
	}
	if p.Spec.Lifecycle.TTL != "8h" {
		t.Errorf("expected restricted-dev TTL 8h, got %s", p.Spec.Lifecycle.TTL)
	}
}

func TestBuiltinHardenedTTL(t *testing.T) {
	p, err := LoadBuiltin("hardened")
	if err != nil {
		t.Fatalf("LoadBuiltin failed: %v", err)
	}
	if p.Spec.Lifecycle.TTL != "4h" {
		t.Errorf("expected hardened TTL 4h, got %s", p.Spec.Lifecycle.TTL)
	}
}

func TestBuiltinSealedTTL(t *testing.T) {
	p, err := LoadBuiltin("sealed")
	if err != nil {
		t.Fatalf("LoadBuiltin failed: %v", err)
	}
	if p.Spec.Lifecycle.TTL != "1h" {
		t.Errorf("expected sealed TTL 1h, got %s", p.Spec.Lifecycle.TTL)
	}
}

func TestBuiltinAllSidecarsEnabled(t *testing.T) {
	for _, name := range ListBuiltins() {
		t.Run(name, func(t *testing.T) {
			p, err := LoadBuiltin(name)
			if err != nil {
				t.Fatalf("LoadBuiltin(%q) failed: %v", name, err)
			}
			if !p.Spec.Sidecars.DNSProxy.Enabled {
				t.Errorf("%s: dnsProxy not enabled", name)
			}
			if !p.Spec.Sidecars.HTTPProxy.Enabled {
				t.Errorf("%s: httpProxy not enabled", name)
			}
			if !p.Spec.Sidecars.AuditLog.Enabled {
				t.Errorf("%s: auditLog not enabled", name)
			}
			if !p.Spec.Sidecars.Tracing.Enabled {
				t.Errorf("%s: tracing not enabled", name)
			}
		})
	}
}

func TestBuiltinNetworkGraduation(t *testing.T) {
	openDev, _ := LoadBuiltin("open-dev")
	sealed, _ := LoadBuiltin("sealed")

	if len(openDev.Spec.Network.Egress.AllowedDNSSuffixes) == 0 {
		t.Error("open-dev should have broad allowedDNSSuffixes")
	}
	if len(sealed.Spec.Network.Egress.AllowedDNSSuffixes) != 0 {
		t.Errorf("sealed should have zero allowedDNSSuffixes, got %d", len(sealed.Spec.Network.Egress.AllowedDNSSuffixes))
	}
}

func TestBuiltinSealedZeroEgress(t *testing.T) {
	p, err := LoadBuiltin("sealed")
	if err != nil {
		t.Fatalf("LoadBuiltin failed: %v", err)
	}
	if len(p.Spec.Network.Egress.AllowedDNSSuffixes) != 0 {
		t.Errorf("sealed should have no allowedDNSSuffixes, got %v", p.Spec.Network.Egress.AllowedDNSSuffixes)
	}
	if len(p.Spec.Network.Egress.AllowedHosts) != 0 {
		t.Errorf("sealed should have no allowedHosts, got %v", p.Spec.Network.Egress.AllowedHosts)
	}
}
