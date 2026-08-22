package profile_test

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// minimalLaunchAccountProfile returns a full valid profile YAML with the given
// spec.runtime.launchAccount and spec.network.privateSubnet values substituted in.
// launchAccount == "" omits the key entirely (tests the zero-value/dormant path);
// privateSubnet defaults to omitted unless includePrivateSubnet is true.
func minimalLaunchAccountProfile(launchAccount string, includePrivateSubnet, privateSubnet bool) []byte {
	runtimeLaunchAccountLine := ""
	if launchAccount != "" {
		runtimeLaunchAccountLine = "    launchAccount: " + launchAccount + "\n"
	}
	networkPrivateSubnetLine := ""
	if includePrivateSubnet {
		networkPrivateSubnetLine = "    privateSubnet: " + boolYAML(privateSubnet) + "\n"
	}

	return []byte(`apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: launch-account-test
spec:
  lifecycle:
    ttl: "24h"
    idleTimeout: "1h"
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.medium
    region: us-east-1
` + runtimeLaunchAccountLine + `  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
      allowedHosts: []
` + networkPrivateSubnetLine + `  iam:
    roleSessionDuration: "1h"
    allowedRegions:
      - us-east-1
  sidecars:
    dnsProxy:
      enabled: true
      image: km-dns-proxy:latest
    httpProxy:
      enabled: true
      image: km-http-proxy:latest
    auditLog:
      enabled: true
      image: km-audit-log:latest
    tracing:
      enabled: true
      image: km-tracing:latest
  observability:
    commandLog:
      destination: cloudwatch
      logGroup: /klanker-maker/sandboxes
    networkLog:
      destination: cloudwatch
      logGroup: /klanker-maker/network
`)
}

func boolYAML(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestValidateSchema_LaunchAccount verifies Phase 126's spec.runtime.launchAccount:
// the schema accepts it (Test 1), it unmarshals into RuntimeSpec.LaunchAccount
// (Test 2), and its absence is a byte-identical zero-value validate (Test 3).
func TestValidateSchema_LaunchAccount(t *testing.T) {
	t.Run("launchAccount set passes schema validation with zero errors", func(t *testing.T) {
		data := minimalLaunchAccountProfile("mgmt-gpu", false, false)
		errs := profile.Validate(data)
		if len(errs) != 0 {
			t.Errorf("expected no validation errors for spec.runtime.launchAccount: mgmt-gpu, got %d:", len(errs))
			for _, e := range errs {
				t.Logf("  - %s", e.Error())
			}
		}
	})

	t.Run("launchAccount set unmarshals to RuntimeSpec.LaunchAccount", func(t *testing.T) {
		data := minimalLaunchAccountProfile("mgmt-gpu", false, false)
		p, err := profile.Parse(data)
		if err != nil {
			t.Fatalf("Parse() error: %v", err)
		}
		if p.Spec.Runtime.LaunchAccount != "mgmt-gpu" {
			t.Errorf("Spec.Runtime.LaunchAccount: got %q, want %q", p.Spec.Runtime.LaunchAccount, "mgmt-gpu")
		}
	})

	t.Run("launchAccount absent parses to zero value and validates clean", func(t *testing.T) {
		data := minimalLaunchAccountProfile("", false, false)
		p, err := profile.Parse(data)
		if err != nil {
			t.Fatalf("Parse() error: %v", err)
		}
		if p.Spec.Runtime.LaunchAccount != "" {
			t.Errorf("Spec.Runtime.LaunchAccount: got %q, want empty string (absent key)", p.Spec.Runtime.LaunchAccount)
		}
		errs := profile.Validate(data)
		if len(errs) != 0 {
			t.Errorf("expected no validation errors when launchAccount is absent, got %d:", len(errs))
			for _, e := range errs {
				t.Logf("  - %s", e.Error())
			}
		}
	})
}
