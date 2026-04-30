package profile_test

import (
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// boolPtr is a helper to create *bool values for semantic tests.
func boolPtr(b bool) *bool { return &b }

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

// minimalCLIBase is a complete base profile YAML for CLI-related tests.
// Tests append a `  cli:` block to this base.
const minimalCLIBase = `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: notify-validate-test
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
    networkLog:
      destination: cloudwatch
  agent:
    maxConcurrentTasks: 4
    taskTimeout: "30m"
`

// TestValidate_NotifyFields_AllSet verifies that a profile with all four notify
// fields set to valid values passes schema validation.
func TestValidate_NotifyFields_AllSet(t *testing.T) {
	yaml := minimalCLIBase + `  cli:
    notifyOnPermission: true
    notifyOnIdle: true
    notifyCooldownSeconds: 60
    notificationEmailAddress: "ops@example.com"
`
	errs := profile.ValidateSchema([]byte(yaml))
	if len(errs) != 0 {
		t.Errorf("expected no schema errors for profile with all notify fields, got %d errors:", len(errs))
		for _, e := range errs {
			t.Logf("  - %s", e.Error())
		}
	}
}

// TestValidate_NotifyFields_WrongType_CooldownString verifies that schema
// validation rejects notifyCooldownSeconds given as a string instead of integer.
func TestValidate_NotifyFields_WrongType_CooldownString(t *testing.T) {
	yaml := minimalCLIBase + `  cli:
    notifyCooldownSeconds: "60"
`
	errs := profile.ValidateSchema([]byte(yaml))
	if len(errs) == 0 {
		t.Error("expected schema validation to reject notifyCooldownSeconds as string, got no errors")
		return
	}

	found := false
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, "notifyCooldownSeconds") || strings.Contains(msg, "integer") ||
			strings.Contains(msg, "type") || strings.Contains(msg, "cli") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error referencing notifyCooldownSeconds type, errors were: %v", errs)
	}
}

// TestValidate_NotifyFields_WrongType_PermissionString verifies that schema
// validation rejects notifyOnPermission given as a string instead of boolean.
func TestValidate_NotifyFields_WrongType_PermissionString(t *testing.T) {
	yaml := minimalCLIBase + `  cli:
    notifyOnPermission: "yes"
`
	errs := profile.ValidateSchema([]byte(yaml))
	if len(errs) == 0 {
		t.Error("expected schema validation to reject notifyOnPermission as string, got no errors")
		return
	}

	found := false
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, "notifyOnPermission") || strings.Contains(msg, "boolean") ||
			strings.Contains(msg, "type") || strings.Contains(msg, "cli") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error referencing notifyOnPermission type, errors were: %v", errs)
	}
}

func TestSemanticInvalidSubstrate(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/invalid-bad-substrate.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	errs := profile.Validate(data)
	if len(errs) == 0 {
		t.Error("expected validation error for substrate 'kubernetes', got none")
		return
	}

	found := false
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, "substrate") || strings.Contains(msg, "kubernetes") ||
			strings.Contains(msg, "ec2") || strings.Contains(msg, "ecs") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about invalid substrate 'kubernetes', errors were: %v", errs)
	}
}

func TestValidateSchemaRejectsInvalidSubstrate(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/invalid-bad-substrate.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	errs := profile.ValidateSchema(data)
	if len(errs) == 0 {
		t.Error("expected schema error for substrate 'kubernetes', got none")
		return
	}

	// Should mention the substrate path or enum constraint
	found := false
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, "substrate") || strings.Contains(msg, "enum") ||
			strings.Contains(msg, "kubernetes") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error referencing substrate enum, errors were: %v", errs)
	}
}

func TestValidateDockerSubstrate(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/valid-docker-substrate.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	// Semantic validation must accept docker substrate
	semanticErrs := profile.Validate(data)
	if len(semanticErrs) != 0 {
		t.Errorf("expected no semantic validation errors for substrate 'docker', got %d errors:", len(semanticErrs))
		for _, e := range semanticErrs {
			t.Errorf("  - %s", e.Error())
		}
	}

	// Schema validation must accept docker substrate
	schemaErrs := profile.ValidateSchema(data)
	if len(schemaErrs) != 0 {
		t.Errorf("expected no schema validation errors for substrate 'docker', got %d errors:", len(schemaErrs))
		for _, e := range schemaErrs {
			t.Errorf("  - %s", e.Error())
		}
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

  agent:
    maxConcurrentTasks: 4
    taskTimeout: "30m"
`)
}

// minimalExecutionProfile returns a full valid profile YAML with the given execution YAML block.
func minimalExecutionProfile(executionYAML string) []byte {
	return []byte(`apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: rsync-schema-test
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
` + executionYAML + `
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
      allowedHosts: []

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

  agent:
    maxConcurrentTasks: 4
    taskTimeout: "30m"
`)
}

// TestRsyncSchemaValidation verifies that the JSON schema correctly accepts and
// rejects rsyncPaths and rsyncFileList configurations.
func TestRsyncSchemaValidation(t *testing.T) {
	t.Run("rsyncPaths array of strings is valid", func(t *testing.T) {
		data := minimalExecutionProfile(`    shell: /bin/bash
    workingDir: /workspace
    rsyncPaths:
      - ".claude"
      - "projects/*/config"`)
		errs := profile.ValidateSchema(data)
		if len(errs) != 0 {
			t.Errorf("expected no errors for valid rsyncPaths, got: %v", errs)
		}
	})

	t.Run("rsyncFileList string is valid", func(t *testing.T) {
		data := minimalExecutionProfile(`    shell: /bin/bash
    workingDir: /workspace
    rsyncFileList: "cc-files.yaml"`)
		errs := profile.ValidateSchema(data)
		if len(errs) != 0 {
			t.Errorf("expected no errors for valid rsyncFileList, got: %v", errs)
		}
	})

	t.Run("both rsyncPaths and rsyncFileList together is valid", func(t *testing.T) {
		data := minimalExecutionProfile(`    shell: /bin/bash
    workingDir: /workspace
    rsyncPaths:
      - ".claude"
    rsyncFileList: "extra-paths.yaml"`)
		errs := profile.ValidateSchema(data)
		if len(errs) != 0 {
			t.Errorf("expected no errors for rsyncPaths+rsyncFileList, got: %v", errs)
		}
	})

	t.Run("omitting rsyncPaths and rsyncFileList is valid (backward compat)", func(t *testing.T) {
		data := minimalExecutionProfile(`    shell: /bin/bash
    workingDir: /workspace`)
		errs := profile.ValidateSchema(data)
		if len(errs) != 0 {
			t.Errorf("expected no errors for profile without rsync fields, got: %v", errs)
		}
	})

	t.Run("rsyncPaths with non-string item (integer) is rejected", func(t *testing.T) {
		// Use JSON-style inline array to embed a non-string
		data := []byte(`apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: rsync-bad-items
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
    rsyncPaths: [42, ".claude"]
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
      allowedHosts: []

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

  agent:
    maxConcurrentTasks: 4
    taskTimeout: "30m"
`)
		errs := profile.ValidateSchema(data)
		if len(errs) == 0 {
			t.Error("expected schema error for rsyncPaths with integer item, got none")
		}
	})
}

// minimalProfileWithTlsCapture returns a full valid profile YAML with the given tlsCapture YAML block.
func minimalProfileWithTlsCapture(tlsCaptureYAML string) []byte {
	return []byte(`apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: tls-capture-schema-test
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
` + tlsCaptureYAML + `

  agent:
    maxConcurrentTasks: 4
    taskTimeout: "30m"
`)
}

// TestValidateSchema_TlsCapture verifies JSON Schema correctly validates tlsCapture configurations.
func TestValidateSchema_TlsCapture(t *testing.T) {
	t.Run("valid tlsCapture with openssl", func(t *testing.T) {
		data := minimalProfileWithTlsCapture(`    tlsCapture:
      enabled: true
      libraries:
        - openssl
      capturePayloads: false`)
		errs := profile.ValidateSchema(data)
		if len(errs) != 0 {
			t.Errorf("expected no errors for valid tlsCapture, got: %v", errs)
		}
	})

	t.Run("valid tlsCapture with all library", func(t *testing.T) {
		data := minimalProfileWithTlsCapture(`    tlsCapture:
      enabled: true
      libraries:
        - all`)
		errs := profile.ValidateSchema(data)
		if len(errs) != 0 {
			t.Errorf("expected no errors for tlsCapture with 'all', got: %v", errs)
		}
	})

	t.Run("valid tlsCapture with multiple libraries", func(t *testing.T) {
		data := minimalProfileWithTlsCapture(`    tlsCapture:
      enabled: true
      libraries:
        - openssl
        - gnutls
        - nss
        - go
        - rustls`)
		errs := profile.ValidateSchema(data)
		if len(errs) != 0 {
			t.Errorf("expected no errors for tlsCapture with all named libraries, got: %v", errs)
		}
	})

	t.Run("invalid tlsCapture with unknown library", func(t *testing.T) {
		data := minimalProfileWithTlsCapture(`    tlsCapture:
      enabled: true
      libraries:
        - openssl
        - boringssl`)
		errs := profile.ValidateSchema(data)
		if len(errs) == 0 {
			t.Error("expected schema error for invalid library 'boringssl', got none")
		}
	})

	t.Run("tlsCapture without enabled field is rejected", func(t *testing.T) {
		data := minimalProfileWithTlsCapture(`    tlsCapture:
      libraries:
        - openssl`)
		errs := profile.ValidateSchema(data)
		if len(errs) == 0 {
			t.Error("expected schema error for tlsCapture missing required 'enabled', got none")
		}
	})

	t.Run("omitting tlsCapture is valid (backward compat)", func(t *testing.T) {
		data := minimalProfileWithTlsCapture("")
		errs := profile.ValidateSchema(data)
		if len(errs) != 0 {
			t.Errorf("expected no errors for profile without tlsCapture, got: %v", errs)
		}
	})
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

// --- Phase 63: Slack validation tests ---

// minimalCLISpecWith returns a minimal SandboxProfile with the given CLISpec for
// direct ValidateSemantic testing.
func minimalCLISpecWith(cli *profile.CLISpec) *profile.SandboxProfile {
	return &profile.SandboxProfile{
		APIVersion: "klankermaker.ai/v1alpha1",
		Kind:       "SandboxProfile",
		Spec: profile.Spec{
			CLI: cli,
		},
	}
}

// TestValidateSemantic_Slack_PerSandboxAndOverride_Error verifies that setting
// notifySlackPerSandbox=true and notifySlackChannelOverride is a non-warning error.
func TestValidateSemantic_Slack_PerSandboxAndOverride_Error(t *testing.T) {
	p := minimalCLISpecWith(&profile.CLISpec{
		NotifySlackPerSandbox:      true,
		NotifySlackChannelOverride: "CABC123",
	})
	errs := profile.ValidateSemantic(p)
	if len(errs) == 0 {
		t.Fatal("expected 1 error for perSandbox+override combo, got none")
	}
	found := false
	for _, e := range errs {
		if e.IsWarning {
			continue
		}
		if strings.Contains(e.Message, "mutually exclusive") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected non-warning error with 'mutually exclusive', got: %v", errs)
	}
}

// TestValidateSemantic_Slack_PerSandboxWithoutSlackEnabled_Warning verifies that
// notifySlackPerSandbox=true with notifySlackEnabled=&false is a warning (not error).
func TestValidateSemantic_Slack_PerSandboxWithoutSlackEnabled_Warning(t *testing.T) {
	p := minimalCLISpecWith(&profile.CLISpec{
		NotifySlackPerSandbox: true,
		NotifySlackEnabled:    boolPtr(false),
	})
	errs := profile.ValidateSemantic(p)
	if len(errs) == 0 {
		t.Fatal("expected 1 warning for perSandbox+slackDisabled, got none")
	}
	found := false
	for _, e := range errs {
		if e.IsWarning && strings.Contains(e.Message, "notifySlackPerSandbox") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected IsWarning=true with 'notifySlackPerSandbox' message, got: %v", errs)
	}
	// Must not be a hard error
	for _, e := range errs {
		if !e.IsWarning {
			t.Errorf("expected only warnings, but got a hard error: %s", e.Error())
		}
	}
}

// TestValidateSemantic_Slack_ArchiveWithoutPerSandbox_Warning verifies that
// slackArchiveOnDestroy set without notifySlackPerSandbox=true is a warning.
func TestValidateSemantic_Slack_ArchiveWithoutPerSandbox_Warning(t *testing.T) {
	p := minimalCLISpecWith(&profile.CLISpec{
		NotifySlackPerSandbox: false,
		SlackArchiveOnDestroy: boolPtr(true),
	})
	errs := profile.ValidateSemantic(p)
	if len(errs) == 0 {
		t.Fatal("expected 1 warning for archiveOnDestroy without perSandbox, got none")
	}
	found := false
	for _, e := range errs {
		if e.IsWarning && strings.Contains(e.Message, "slackArchiveOnDestroy") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected IsWarning=true with 'slackArchiveOnDestroy' message, got: %v", errs)
	}
	for _, e := range errs {
		if !e.IsWarning {
			t.Errorf("expected only warnings, got a hard error: %s", e.Error())
		}
	}
}

// TestValidateSemantic_Slack_BadChannelOverride_Error verifies that an invalid
// channel ID (not matching ^C[A-Z0-9]+$) is a hard error.
func TestValidateSemantic_Slack_BadChannelOverride_Error(t *testing.T) {
	p := minimalCLISpecWith(&profile.CLISpec{
		NotifySlackChannelOverride: "not-a-channel",
	})
	errs := profile.ValidateSemantic(p)
	if len(errs) == 0 {
		t.Fatal("expected 1 error for invalid channel ID, got none")
	}
	found := false
	for _, e := range errs {
		if !e.IsWarning && strings.Contains(e.Message, "channel ID") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected non-warning error with 'channel ID', got: %v", errs)
	}
}

// TestValidateSemantic_Slack_BothChannelsDisabled_Warning verifies that
// notifyEmailEnabled=&false and notifySlackEnabled=&false is a warning.
func TestValidateSemantic_Slack_BothChannelsDisabled_Warning(t *testing.T) {
	p := minimalCLISpecWith(&profile.CLISpec{
		NotifyEmailEnabled: boolPtr(false),
		NotifySlackEnabled: boolPtr(false),
	})
	errs := profile.ValidateSemantic(p)
	if len(errs) == 0 {
		t.Fatal("expected 1 warning for both channels disabled, got none")
	}
	found := false
	for _, e := range errs {
		if e.IsWarning && strings.Contains(e.Message, "no notification channel") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected IsWarning=true with 'no notification channel' message, got: %v", errs)
	}
	for _, e := range errs {
		if !e.IsWarning {
			t.Errorf("expected only warnings, got a hard error: %s", e.Error())
		}
	}
}

// TestValidateSemantic_Slack_HappyPath_NoErrors verifies that a valid Slack config
// (slackEnabled=true, perSandbox=true, archiveOnDestroy=true, no override) emits no errors.
func TestValidateSemantic_Slack_HappyPath_NoErrors(t *testing.T) {
	p := minimalCLISpecWith(&profile.CLISpec{
		NotifySlackEnabled:    boolPtr(true),
		NotifySlackPerSandbox: true,
		SlackArchiveOnDestroy: boolPtr(true),
	})
	errs := profile.ValidateSemantic(p)
	if len(errs) != 0 {
		t.Errorf("expected no errors for valid Slack happy-path config, got: %v", errs)
	}
}

// TestValidateSemantic_Slack_BackwardCompat_Phase62Profile verifies that a profile
// with only Phase 62 fields and no Slack fields produces zero Slack-related errors.
// This is the critical Phase 62 regression test.
func TestValidateSemantic_Slack_BackwardCompat_Phase62Profile(t *testing.T) {
	p := minimalCLISpecWith(&profile.CLISpec{
		NotifyOnPermission:       true,
		NotifyOnIdle:             true,
		NotifyCooldownSeconds:    60,
		NotificationEmailAddress: "ops@example.com",
		// All Phase 63 fields absent (nil / zero values)
	})
	errs := profile.ValidateSemantic(p)
	if len(errs) != 0 {
		t.Errorf("expected zero errors for Phase 62 backward-compat profile, got: %v", errs)
	}
}
