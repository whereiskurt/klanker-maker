// Package profile_test contains tests for the Phase 89 SOPS secret injection
// schema surface. Tests are split into four groups:
//
//   - TestSecretsSpec*: YAML parse round-trips for spec.secrets (SOPS-01-SCHEMA)
//   - TestValidateSemanticSecrets_*: .enc.yaml suffix check (SOPS-02-VALIDATION)
//   - TestValidateSopsBundleFile_*: offline sops: block check (SOPS-02-VALIDATION)
//   - TestSchemaSecretsSpec_*: JSON Schema accept/reject tests (SOPS-10-SCHEMA-EXPORT)
//
// Fixture generation:
//
//	age-keygen was used to generate pkg/profile/testdata/secrets-fixture-age.key.
//	sops v3.11.0 was used to encrypt the plaintext bundle against the age public key.
//	Both the key file and the encrypted bundle are committed for offline testing.
//	The key is a TEST KEY ONLY — do not use for real secrets.
//
//	If age or sops are unavailable, the fixture can be replaced with a synthetic
//	file containing a valid-shape sops: metadata block (decryption is NOT required
//	for offline tests in this plan; Wave 2 UAT exercises the real decrypt path).
//
// To regenerate the fixture on a new machine:
//
//	age-keygen -o pkg/profile/testdata/secrets-fixture-age.key
//	PUBKEY=$(grep 'public key' pkg/profile/testdata/secrets-fixture-age.key | awk '{print $NF}')
//	cat > /tmp/plain.yaml << 'EOF'
//	OPENAI_API_KEY: sk-test-fixture-do-not-use-AAAAAAAAAAAAAAAA
//	ANTHROPIC_API_KEY: sk-ant-test-fixture-do-not-use-BBBBBBBB
//	SOME_MULTI_TOKEN_VALUE: hello world this is fine
//	EOF
//	cd /tmp && SOPS_AGE_RECIPIENTS=$PUBKEY sops --encrypt --age $PUBKEY \
//	  --input-type yaml --output-type yaml /tmp/plain.yaml \
//	  > /path/to/pkg/profile/testdata/secrets-fixture.enc.yaml
//	rm /tmp/plain.yaml
package profile_test

import (
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// minimalSecretsBase is a complete spec with spec.secrets appended by each test case.
// It mirrors the pattern used in validate_test.go for CLI tests.
const minimalSecretsBase = `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: secrets-test
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
`

// ---------------------------------------------------------------------------
// SOPS-01-SCHEMA: SecretsSpec parse tests
// ---------------------------------------------------------------------------

func TestSecretsSpecParse(t *testing.T) {
	yaml := minimalSecretsBase + `  secrets:
    sopsFile: ./secrets/codex.enc.yaml
`
	p, err := profile.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if p.Spec.Secrets == nil {
		t.Fatal("expected Spec.Secrets != nil when spec.secrets.sopsFile is set")
	}
	if p.Spec.Secrets.SopsFile != "./secrets/codex.enc.yaml" {
		t.Errorf("expected SopsFile == './secrets/codex.enc.yaml', got %q", p.Spec.Secrets.SopsFile)
	}
}

func TestSecretsSpecAbsent(t *testing.T) {
	// No spec.secrets section at all → backwards compatible nil pointer.
	p, err := profile.Parse([]byte(minimalSecretsBase))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if p.Spec.Secrets != nil {
		t.Errorf("expected Spec.Secrets == nil when spec.secrets is absent, got %+v", p.Spec.Secrets)
	}
}

func TestSecretsSpecEmpty(t *testing.T) {
	// spec.secrets: {} → pointer non-nil but SopsFile is empty string.
	yaml := minimalSecretsBase + `  secrets: {}
`
	p, err := profile.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if p.Spec.Secrets == nil {
		t.Fatal("expected Spec.Secrets != nil when spec.secrets: {} is set")
	}
	if p.Spec.Secrets.SopsFile != "" {
		t.Errorf("expected SopsFile == '' for empty secrets block, got %q", p.Spec.Secrets.SopsFile)
	}
}

// ---------------------------------------------------------------------------
// SOPS-02-VALIDATION: ValidateSemantic spec.secrets clause
// ---------------------------------------------------------------------------

func TestValidateSemanticSecrets_BadSuffix(t *testing.T) {
	yaml := minimalSecretsBase + `  secrets:
    sopsFile: ./secrets/codex.yaml
`
	errs := profile.Validate([]byte(yaml))
	if len(errs) == 0 {
		t.Fatal("expected at least one validation error for bad .yaml suffix, got none")
	}

	found := false
	for _, e := range errs {
		if e.Path == "spec.secrets.sopsFile" && strings.Contains(e.Message, ".enc.yaml") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ValidationError on path 'spec.secrets.sopsFile' mentioning '.enc.yaml'; errors: %v", errs)
	}
}

func TestValidateSemanticSecrets_GoodSuffix(t *testing.T) {
	// At the struct-only semantic layer, a .enc.yaml suffix is valid.
	// File existence is layered on by callers (km validate / km create).
	yaml := minimalSecretsBase + `  secrets:
    sopsFile: ./secrets/codex.enc.yaml
`
	errs := profile.Validate([]byte(yaml))

	// No error should reference spec.secrets.sopsFile
	for _, e := range errs {
		if e.Path == "spec.secrets.sopsFile" {
			t.Errorf("unexpected error on spec.secrets.sopsFile for valid .enc.yaml suffix: %s", e.Error())
		}
	}
}

// ---------------------------------------------------------------------------
// SOPS-02-VALIDATION: ValidateSopsBundleFile offline helper
// ---------------------------------------------------------------------------

func TestValidateSopsBundleFile_MissingFile(t *testing.T) {
	err := profile.ValidateSopsBundleFile("/nonexistent/path/to/secrets.enc.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "/nonexistent/path/to/secrets.enc.yaml") {
		t.Errorf("expected error to contain file path, got: %v", err)
	}
}

func TestValidateSopsBundleFile_MissingSopsBlock(t *testing.T) {
	// Write a YAML file without a top-level sops: key.
	f, err := os.CreateTemp("", "no-sops-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString("OPENAI_API_KEY: plaintext-value\n"); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	f.Close()

	err = profile.ValidateSopsBundleFile(f.Name())
	if err == nil {
		t.Fatal("expected error for YAML without sops: block, got nil")
	}
	if !strings.Contains(err.Error(), "sops:") {
		t.Errorf("expected error to mention 'sops:' block, got: %v", err)
	}
}

func TestValidateSopsBundleFile_ValidFixture(t *testing.T) {
	// The age-encrypted fixture at testdata/secrets-fixture.enc.yaml was generated
	// via sops --encrypt --age and contains a valid sops: metadata block.
	// This test exercises offline sops: block detection — it does NOT decrypt.
	fixturePath := "testdata/secrets-fixture.enc.yaml"
	data, readErr := os.ReadFile(fixturePath)
	if readErr != nil {
		t.Fatalf("failed to read fixture %s: %v", fixturePath, readErr)
	}
	// Sanity: the fixture file should contain sops: at the top level.
	if !strings.Contains(string(data), "sops:") {
		t.Fatalf("fixture %s does not contain 'sops:' block — fixture may be malformed", fixturePath)
	}

	err := profile.ValidateSopsBundleFile(fixturePath)
	if err != nil {
		t.Errorf("expected no error for valid sops fixture, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SOPS-10-SCHEMA-EXPORT: JSON Schema accept/reject tests
// ---------------------------------------------------------------------------

func TestSchemaSecretsSpec_ValidObject(t *testing.T) {
	// JSON Schema should accept a profile with a valid spec.secrets object.
	yaml := minimalSecretsBase + `  secrets:
    sopsFile: ./secrets/x.enc.yaml
`
	errs := profile.ValidateSchema([]byte(yaml))
	for _, e := range errs {
		if strings.Contains(e.Path, "secrets") {
			t.Errorf("unexpected schema error for valid spec.secrets object: %s", e.Error())
		}
	}
}

func TestSchemaSecretsSpec_RejectArrayType(t *testing.T) {
	// JSON Schema should reject spec.secrets when it is an array.
	t.Run("array value", func(t *testing.T) {
		yaml := minimalSecretsBase + `  secrets: []
`
		errs := profile.ValidateSchema([]byte(yaml))
		found := false
		for _, e := range errs {
			if strings.Contains(e.Error(), "secrets") || strings.Contains(e.Path, "secrets") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected schema error for spec.secrets: [], errors: %v", errs)
		}
	})

	// JSON Schema should reject spec.secrets.sopsFile when it is an integer.
	t.Run("sopsFile integer", func(t *testing.T) {
		yaml := minimalSecretsBase + `  secrets:
    sopsFile: 123
`
		errs := profile.ValidateSchema([]byte(yaml))
		found := false
		for _, e := range errs {
			if strings.Contains(e.Error(), "sopsFile") || strings.Contains(e.Path, "sopsFile") ||
				strings.Contains(e.Error(), "secrets") || strings.Contains(e.Path, "secrets") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected schema error for spec.secrets.sopsFile: 123, errors: %v", errs)
		}
	})
}

func TestSchemaSecretsSpec_RejectUnknownProperty(t *testing.T) {
	// JSON Schema uses additionalProperties: false — unknown keys must be rejected.
	yaml := minimalSecretsBase + `  secrets:
    someUnknownKey: true
`
	errs := profile.ValidateSchema([]byte(yaml))
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "someUnknownKey") || strings.Contains(e.Error(), "additional") ||
			strings.Contains(e.Path, "secrets") || strings.Contains(e.Error(), "secrets") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected schema error for unknown property someUnknownKey, errors: %v", errs)
	}
}
