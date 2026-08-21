package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// denyProfileYAML is the wide-open learn-mode shape the deny list exists to
// serve: allow everything, then subtract the known-bad.
const denyProfileYAML = `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: deny-test
spec:
  lifecycle:
    ttl: "24h"
    idleTimeout: "1h"
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes:
        - "*"
      allowedHosts:
        - "*"
      deniedDNSSuffixes:
        - "evil.example.com"
        - ".tracker.net"
      deniedHosts:
        - "evil.example.com"
  iam:
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
`

func TestParse_EgressDenyLists(t *testing.T) {
	p, err := profile.Parse([]byte(denyProfileYAML))
	if err != nil {
		t.Fatalf("expected parse to succeed, got error: %v", err)
	}

	got := p.Spec.Network.Egress.DeniedDNSSuffixes
	want := []string{"evil.example.com", ".tracker.net"}
	if len(got) != len(want) {
		t.Fatalf("deniedDNSSuffixes: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("deniedDNSSuffixes[%d]: got %q, want %q", i, got[i], want[i])
		}
	}

	if len(p.Spec.Network.Egress.DeniedHosts) != 1 ||
		p.Spec.Network.Egress.DeniedHosts[0] != "evil.example.com" {
		t.Errorf("deniedHosts: got %v, want [evil.example.com]", p.Spec.Network.Egress.DeniedHosts)
	}
}

// The JSON schema is additionalProperties:false, so an undeclared field fails
// validation rather than being silently ignored.
func TestValidate_EgressDenyListsAcceptedBySchema(t *testing.T) {
	if errs := profile.Validate([]byte(denyProfileYAML)); len(errs) > 0 {
		t.Fatalf("expected deny lists to validate cleanly, got %v", errs)
	}
}

// "*" in a deny list is legal (it seals the sandbox) but is almost always a
// mistake — someone reaching for a wildcard placeholder rather than a kill
// switch. Warn, don't block.
func TestValidate_StarInDenyListWarns(t *testing.T) {
	yaml := strings.Replace(denyProfileYAML,
		`        - "evil.example.com"
        - ".tracker.net"`,
		`        - "*"`, 1)

	errs := profile.Validate([]byte(yaml))

	var warned bool
	for _, e := range errs {
		if e.IsWarning && strings.Contains(e.Path, "deniedDNSSuffixes") {
			warned = true
		}
		if !e.IsWarning {
			t.Errorf("a \"*\" deny must warn, not fail: %v", e)
		}
	}
	if !warned {
		t.Errorf("expected a warning for \"*\" in deniedDNSSuffixes, got %v", errs)
	}
}

// Absent deny lists must stay nil rather than becoming empty slices, so the
// compiler's "emit only when set" check is a simple length test.
func TestParse_EgressDenyListsAbsentAreNil(t *testing.T) {
	yaml := strings.ReplaceAll(denyProfileYAML, `      deniedDNSSuffixes:
        - "evil.example.com"
        - ".tracker.net"
      deniedHosts:
        - "evil.example.com"
`, "")

	p, err := profile.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("expected parse to succeed, got error: %v", err)
	}
	if p.Spec.Network.Egress.DeniedDNSSuffixes != nil {
		t.Errorf("expected nil deniedDNSSuffixes, got %v", p.Spec.Network.Egress.DeniedDNSSuffixes)
	}
	if p.Spec.Network.Egress.DeniedHosts != nil {
		t.Errorf("expected nil deniedHosts, got %v", p.Spec.Network.Egress.DeniedHosts)
	}
	if errs := profile.Validate([]byte(yaml)); len(errs) > 0 {
		t.Fatalf("expected profile without deny lists to validate cleanly, got %v", errs)
	}
}
