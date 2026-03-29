package profile_test

import (
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

func TestValidateSchemaValid(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/valid-minimal.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	errs := profile.Validate(data)
	if len(errs) != 0 {
		t.Errorf("expected no validation errors for valid profile, got %d errors:", len(errs))
		for _, e := range errs {
			t.Logf("  - %s", e.Error())
		}
	}
}

func TestValidateSchemaRejectsUnknownFields(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/invalid-unknown-field.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	errs := profile.Validate(data)
	if len(errs) == 0 {
		t.Error("expected validation errors for profile with unknown field 'lifecylce', got none")
		return
	}

	// At least one error should reference the unknown field or additionalProperties
	found := false
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, "lifecylce") || strings.Contains(msg, "additionalProperties") ||
			strings.Contains(msg, "additional") || strings.Contains(msg, "unknown") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error referencing unknown field 'lifecylce', errors were: %v", errs)
	}
}

func TestValidateSchemaRequiresAllSections(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/invalid-missing-spec.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	errs := profile.Validate(data)
	if len(errs) == 0 {
		t.Error("expected validation errors for profile missing spec section, got none")
		return
	}

	// Should report that spec or its required sections are missing
	found := false
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, "spec") || strings.Contains(msg, "required") || strings.Contains(msg, "missing") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about missing spec section, errors were: %v", errs)
	}
}

func TestValidateErrorFormat(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/invalid-bad-substrate.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	errs := profile.Validate(data)
	if len(errs) == 0 {
		t.Error("expected validation errors for profile with bad substrate, got none")
		return
	}

	// Errors should use "path: message" format (contain a colon)
	for _, e := range errs {
		msg := e.Error()
		if !strings.Contains(msg, ":") {
			t.Errorf("expected error to use 'path: message' format, got: %s", msg)
		}
	}
}

func TestSemanticTTLShorterThanIdle(t *testing.T) {
	// Profile with TTL=1h but idleTimeout=2h — TTL shorter than idleTimeout
	yamlData := []byte(`apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: ttl-too-short
  labels:
    tier: test
spec:
  lifecycle:
    ttl: "1h"
    idleTimeout: "2h"
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
      allowedMethods:
        - GET
  identity:
    roleSessionDuration: "1h"
    allowedRegions:
      - us-east-1
    sessionPolicy: minimal
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
      logGroup: /klankrmkr/sandboxes
    networkLog:
      destination: cloudwatch
      logGroup: /klankrmkr/network
  policy:
    allowShellEscape: false
  agent:
    maxConcurrentTasks: 4
    taskTimeout: "30m"
`)

	errs := profile.Validate(yamlData)
	if len(errs) == 0 {
		t.Error("expected semantic error for TTL shorter than idleTimeout, got none")
		return
	}

	found := false
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, "ttl") || strings.Contains(msg, "idleTimeout") ||
			strings.Contains(msg, "idle") || strings.Contains(msg, "TTL") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about TTL < idleTimeout, errors were: %v", errs)
	}
}

func TestSemanticInvalidSubstrate(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/invalid-bad-substrate.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	errs := profile.Validate(data)
	if len(errs) == 0 {
		t.Error("expected validation error for substrate 'docker', got none")
		return
	}

	found := false
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, "substrate") || strings.Contains(msg, "docker") ||
			strings.Contains(msg, "ec2") || strings.Contains(msg, "ecs") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about invalid substrate 'docker', errors were: %v", errs)
	}
}

func TestValidateSchemaRejectsInvalidSubstrate(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/invalid-bad-substrate.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	errs := profile.ValidateSchema(data)
	if len(errs) == 0 {
		t.Error("expected schema error for substrate 'docker', got none")
		return
	}

	// Should mention the substrate path or enum constraint
	found := false
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, "substrate") || strings.Contains(msg, "enum") ||
			strings.Contains(msg, "docker") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error referencing substrate enum, errors were: %v", errs)
	}
}

// minimalProfileWithPrefix returns a full valid profile YAML with the given prefix injected.
// If prefix is empty string, it is omitted from the YAML.
func minimalProfileWithPrefix(prefix string) []byte {
	prefixLine := ""
	if prefix != "" {
		prefixLine = "\n  prefix: " + prefix
	}
	return []byte(`apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: test-prefix` + prefixLine + `
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
      allowedMethods:
        - GET
  identity:
    roleSessionDuration: "1h"
    allowedRegions:
      - us-east-1
    sessionPolicy: minimal
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
      logGroup: /klankrmkr/sandboxes
    networkLog:
      destination: cloudwatch
      logGroup: /klankrmkr/network
  policy:
    allowShellEscape: false
  agent:
    maxConcurrentTasks: 4
    taskTimeout: "30m"
`)
}

// minimalProfileWithAlias returns a full valid profile YAML with the given alias injected.
// If alias is empty string, it is omitted from the YAML.
func minimalProfileWithAlias(alias string) []byte {
	aliasLine := ""
	if alias != "" {
		aliasLine = "\n  alias: " + alias
	}
	return []byte(`apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: test-alias` + aliasLine + `
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
      allowedMethods:
        - GET
  identity:
    roleSessionDuration: "1h"
    allowedRegions:
      - us-east-1
    sessionPolicy: minimal
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
      logGroup: /test/commands
    networkLog:
      destination: cloudwatch
      logGroup: /test/network
  policy:
    allowShellEscape: false
  agent:
    maxConcurrentTasks: 2
    taskTimeout: "30m"
`)
}

func TestValidateSchema_MetadataAlias(t *testing.T) {
	tests := []struct {
		name        string
		alias       string
		wantValid   bool
	}{
		{"valid orc", "orc", true},
		{"valid wrkr", "wrkr", true},
		{"valid single char", "a", true},
		{"valid max length 16", "abcdefghijklmnop", true},
		{"valid no alias omitted", "", true},
		{"invalid starts with digit", "1bad", false},
		{"invalid too long 17 chars", "abcdefghijklmnopq", false},
		{"invalid uppercase", "Bad", false},
		{"invalid has space", "has space", false},
		{"invalid has dash", "has-dash", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := minimalProfileWithAlias(tc.alias)
			errs := profile.ValidateSchema(data)
			if tc.wantValid {
				if len(errs) != 0 {
					t.Errorf("expected valid alias %q, got %d errors: %v", tc.alias, len(errs), errs)
				}
			} else {
				if len(errs) == 0 {
					t.Errorf("expected validation error for alias %q, got none", tc.alias)
				}
			}
		})
	}
}

func TestValidateSchema_MetadataPrefix(t *testing.T) {
	tests := []struct {
		name        string
		prefix      string
		wantValid   bool
		wantErrHint string
	}{
		{"valid claude", "claude", true, ""},
		{"valid build", "build", true, ""},
		{"valid single char", "a", true, ""},
		{"valid max length 12", "abcdefghijkl", true, ""},
		{"valid no prefix omitted", "", true, ""},
		{"invalid starts with digit", "0bad", false, "prefix"},
		{"invalid too long 14 chars", "toolongprefix0", false, "prefix"},
		{"invalid uppercase", "Bad", false, "prefix"},
		{"invalid has dash", "has-dash", false, "prefix"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := minimalProfileWithPrefix(tc.prefix)
			errs := profile.ValidateSchema(data)
			if tc.wantValid {
				if len(errs) != 0 {
					t.Errorf("expected valid, got %d errors: %v", len(errs), errs)
				}
			} else {
				if len(errs) == 0 {
					t.Errorf("expected validation error for prefix %q, got none", tc.prefix)
					return
				}
				found := false
				for _, e := range errs {
					msg := e.Error()
					if strings.Contains(msg, "prefix") || strings.Contains(msg, "pattern") || strings.Contains(msg, "metadata") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error mentioning prefix or pattern, got: %v", errs)
				}
			}
		})
	}
}
