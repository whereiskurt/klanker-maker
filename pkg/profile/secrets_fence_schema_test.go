package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// minimalProfileWithSecrets returns the smallest valid v1alpha2 document with
// the given spec.secrets block appended.
func minimalProfileWithSecrets(secretsYAML string) []byte {
	return []byte(`apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: secrets-fence-schema-test
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
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
      allowedHosts: []
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
` + secretsYAML + `
`)
}

// Through YAML and ValidateSchema, never by setting the struct: a struct-level
// test greens while the field is absent from the JSON schema and therefore dead
// on every real profile (the spec.otp precedent).
func TestValidateSchema_AcceptsFenceIMDS(t *testing.T) {
	errs := profile.ValidateSchema(minimalProfileWithSecrets(`  secrets:
    sopsFile: ./secrets/x.enc.yaml
    fenceIMDS: true`))
	if len(errs) != 0 {
		t.Fatalf("schema rejected fenceIMDS: %v", errs)
	}
}

func TestValidateSchema_RejectsNonBoolFenceIMDS(t *testing.T) {
	errs := profile.ValidateSchema(minimalProfileWithSecrets(`  secrets:
    sopsFile: ./secrets/x.enc.yaml
    fenceIMDS: "yes"`))
	if len(errs) == 0 {
		t.Fatal("schema accepted a string fenceIMDS")
	}
	var joined []string
	for _, e := range errs {
		joined = append(joined, e.Error())
	}
	if !strings.Contains(strings.Join(joined, "; "), "fenceIMDS") {
		t.Errorf("error does not name the offending field: %v", errs)
	}
}

// The schema accepting a key is only half of it: the field must also reach the
// struct, or the compiler reads a permanent false.
func TestParse_FenceIMDSReachesTheStruct(t *testing.T) {
	p, err := profile.Parse(minimalProfileWithSecrets(`  secrets:
    sopsFile: ./secrets/x.enc.yaml
    fenceIMDS: true`))
	if err != nil {
		t.Fatal(err)
	}
	if !profile.IsFenceIMDSEnabled(p.Spec.Secrets) {
		t.Fatal("fenceIMDS: true did not reach the struct")
	}
}

func TestParse_FenceIMDSAbsentIsOff(t *testing.T) {
	p, err := profile.Parse(minimalProfileWithSecrets(`  secrets:
    sopsFile: ./secrets/x.enc.yaml`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Secrets.FenceIMDS != nil {
		t.Errorf("an absent fenceIMDS parsed as %v, want nil — the tri-state is what "+
			"lets a later phase flip the default without losing an operator's explicit false",
			*p.Spec.Secrets.FenceIMDS)
	}
	if profile.IsFenceIMDSEnabled(p.Spec.Secrets) {
		t.Error("an absent fenceIMDS is enabled")
	}
}
