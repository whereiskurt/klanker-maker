package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

const grantsProfileHeader = `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: grants-test
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
  secrets:
`

// blocking filters out IsWarning entries. ValidateSchema and Validate both
// return []ValidationError, never error — warnings ride in the same slice.
func blocking(errs []profile.ValidationError) []profile.ValidationError {
	var out []profile.ValidationError
	for _, e := range errs {
		if !e.IsWarning {
			out = append(out, e)
		}
	}
	return out
}

func TestSecretsGrants_AcceptedBySchema(t *testing.T) {
	y := grantsProfileHeader + `    sopsFile: ./secrets/x.enc.yaml
    grants:
      claude: [ANTHROPIC_API_KEY]
      codex: [OPENAI_API_KEY]
`
	if errs := blocking(profile.ValidateSchema([]byte(y))); len(errs) > 0 {
		t.Fatalf("schema rejected a valid grants block: %+v", errs)
	}
	p, err := profile.Parse([]byte(y))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := p.Spec.Secrets.Grants["claude"]; len(got) != 1 || got[0] != "ANTHROPIC_API_KEY" {
		t.Errorf("Grants[claude] = %v, want [ANTHROPIC_API_KEY]", got)
	}
}

func TestSecretsGrants_AbsentIsValid(t *testing.T) {
	y := grantsProfileHeader + `    sopsFile: ./secrets/x.enc.yaml
`
	if errs := blocking(profile.ValidateSchema([]byte(y))); len(errs) > 0 {
		t.Fatalf("schema rejected a grants-less profile: %+v", errs)
	}
	p, err := profile.Parse([]byte(y))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Spec.Secrets.Grants != nil {
		t.Errorf("Grants = %v, want nil when absent", p.Spec.Secrets.Grants)
	}
}

func TestSecretsGrants_WrongShapeRejected(t *testing.T) {
	// A bare string instead of a list is the likely typo. additionalProperties
	// on the grants object must constrain the VALUE to an array of strings.
	y := grantsProfileHeader + `    sopsFile: ./secrets/x.enc.yaml
    grants:
      claude: ANTHROPIC_API_KEY
`
	errs := blocking(profile.ValidateSchema([]byte(y)))
	if len(errs) == 0 {
		t.Fatal("schema accepted a scalar where a list is required")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Path, "grants") || strings.Contains(e.Message, "grants") {
			found = true
		}
	}
	if !found {
		t.Errorf("error should name the offending path, got %+v", errs)
	}
}

func TestSecretsGrants_WithoutSopsFileWarns(t *testing.T) {
	// grants without a bundle cannot do anything: warn, never block.
	y := grantsProfileHeader + `    grants:
      claude: [ANTHROPIC_API_KEY]
`
	all := profile.Validate([]byte(y))
	found := false
	for _, e := range all {
		if e.IsWarning && strings.Contains(e.Path, "secrets.grants") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a warning on spec.secrets.grants, got %+v", all)
	}
	// It must be a WARNING, not a blocking error.
	for _, e := range blocking(all) {
		if strings.Contains(e.Path, "secrets.grants") {
			t.Errorf("grants-without-sopsFile blocked validation: %+v", e)
		}
	}
}
