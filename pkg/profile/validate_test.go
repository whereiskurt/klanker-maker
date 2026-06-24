package profile_test

import (
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// boolPtr is a helper to create *bool values for semantic tests.
func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

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
	yamlData := []byte(`apiVersion: klankermaker.ai/v1alpha2
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
const minimalCLIBase = `apiVersion: klankermaker.ai/v1alpha2
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
    networkLog:
      destination: cloudwatch
`

// TestValidate_NotifyFields_AllSet verifies that a profile with all notification
// event fields set to valid values passes schema validation.
// Phase 92 (Wave 2): migrated from cli.notify* to notification.events / .email.
func TestValidate_NotifyFields_AllSet(t *testing.T) {
	yaml := minimalCLIBase + `  notification:
    events:
      onPermission: true
      onIdle: true
      cooldownSeconds: 60
    email:
      address: "ops@example.com"
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
// validation rejects notification.events.cooldownSeconds given as a string.
func TestValidate_NotifyFields_WrongType_CooldownString(t *testing.T) {
	yaml := minimalCLIBase + `  notification:
    events:
      cooldownSeconds: "60"
`
	errs := profile.ValidateSchema([]byte(yaml))
	if len(errs) == 0 {
		t.Error("expected schema validation to reject cooldownSeconds as string, got no errors")
		return
	}

	found := false
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, "cooldownSeconds") || strings.Contains(msg, "integer") ||
			strings.Contains(msg, "type") || strings.Contains(msg, "notification") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error referencing cooldownSeconds type, errors were: %v", errs)
	}
}

// TestValidate_NotifyFields_WrongType_PermissionString verifies that schema
// validation rejects notification.events.onPermission given as a string.
func TestValidate_NotifyFields_WrongType_PermissionString(t *testing.T) {
	yaml := minimalCLIBase + `  notification:
    events:
      onPermission: "yes"
`
	errs := profile.ValidateSchema([]byte(yaml))
	if len(errs) == 0 {
		t.Error("expected schema validation to reject onPermission as string, got no errors")
		return
	}

	found := false
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, "onPermission") || strings.Contains(msg, "boolean") ||
			strings.Contains(msg, "type") || strings.Contains(msg, "notification") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error referencing onPermission type, errors were: %v", errs)
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
	return []byte(`apiVersion: klankermaker.ai/v1alpha2
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

`)
}

// minimalExecutionProfile returns a full valid profile YAML with the given execution YAML block.
func minimalExecutionProfile(executionYAML string) []byte {
	return []byte(`apiVersion: klankermaker.ai/v1alpha2
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
		data := []byte(`apiVersion: klankermaker.ai/v1alpha2
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

`)
		errs := profile.ValidateSchema(data)
		if len(errs) == 0 {
			t.Error("expected schema error for rsyncPaths with integer item, got none")
		}
	})
}

// minimalProfileWithTlsCapture returns a full valid profile YAML with the given tlsCapture YAML block.
func minimalProfileWithTlsCapture(tlsCaptureYAML string) []byte {
	return []byte(`apiVersion: klankermaker.ai/v1alpha2
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
` + tlsCaptureYAML + `

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

// minimalNotificationWith returns a minimal SandboxProfile with the given
// NotificationSpec for direct ValidateSemantic testing.
// Phase 92 (Wave 2): migrated from CLISpec to the structured notification block.
func minimalNotificationWith(n *profile.NotificationSpec) *profile.SandboxProfile {
	return &profile.SandboxProfile{
		APIVersion: "klankermaker.ai/v1alpha2",
		Kind:       "SandboxProfile",
		Spec: profile.Spec{
			Notification: n,
		},
	}
}

// TestValidateSemantic_Slack_PerSandboxAndOverride_Error verifies that setting
// slack.perSandbox=true and slack.channelOverride is a non-warning error.
func TestValidateSemantic_Slack_PerSandboxAndOverride_Error(t *testing.T) {
	p := minimalNotificationWith(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			PerSandbox:      boolPtr(true),
			ChannelOverride: "CABC123",
		},
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

// TestValidateSemantic_Slack_ChannelNameAndOverride_Error verifies that
// slack.channelName together with slack.channelOverride is a hard error.
func TestValidateSemantic_Slack_ChannelNameAndOverride_Error(t *testing.T) {
	p := minimalNotificationWith(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			ChannelName:     "acme-desktops",
			ChannelOverride: "CABC123",
		},
	})
	errs := profile.ValidateSemantic(p)
	found := false
	for _, e := range errs {
		if !e.IsWarning && strings.Contains(e.Message, "channelName") && strings.Contains(e.Message, "mutually exclusive") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected non-warning 'channelName ... mutually exclusive' error, got: %v", errs)
	}
}

// TestValidateSemantic_Slack_ChannelNameWithoutPerSandbox_Warning verifies that
// slack.channelName without perSandbox=true is a warning (no-op).
func TestValidateSemantic_Slack_ChannelNameWithoutPerSandbox_Warning(t *testing.T) {
	p := minimalNotificationWith(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:     boolPtr(true),
			ChannelName: "acme-desktops",
		},
	})
	errs := profile.ValidateSemantic(p)
	found := false
	for _, e := range errs {
		if e.IsWarning && strings.Contains(e.Message, "channelName") {
			found = true
		}
		if !e.IsWarning {
			t.Errorf("expected only warnings for channelName-without-perSandbox, got hard error: %s", e.Error())
		}
	}
	if !found {
		t.Errorf("expected IsWarning with 'channelName' message, got: %v", errs)
	}
}

// TestValidateSemantic_Slack_PerSandboxWithoutSlackEnabled_Warning verifies that
// slack.perSandbox=true with slack.enabled=&false is a warning (not error).
func TestValidateSemantic_Slack_PerSandboxWithoutSlackEnabled_Warning(t *testing.T) {
	p := minimalNotificationWith(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			PerSandbox: boolPtr(true),
			Enabled:    boolPtr(false),
		},
	})
	errs := profile.ValidateSemantic(p)
	if len(errs) == 0 {
		t.Fatal("expected 1 warning for perSandbox+slackDisabled, got none")
	}
	found := false
	for _, e := range errs {
		if e.IsWarning && strings.Contains(e.Message, "perSandbox") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected IsWarning=true with 'perSandbox' message, got: %v", errs)
	}
	// Must not be a hard error
	for _, e := range errs {
		if !e.IsWarning {
			t.Errorf("expected only warnings, but got a hard error: %s", e.Error())
		}
	}
}

// TestValidateSemantic_Slack_ArchiveWithoutPerSandbox_Warning verifies that
// slack.archiveOnDestroy set without slack.perSandbox=true is a warning.
func TestValidateSemantic_Slack_ArchiveWithoutPerSandbox_Warning(t *testing.T) {
	p := minimalNotificationWith(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			PerSandbox:       boolPtr(false),
			ArchiveOnDestroy: boolPtr(true),
		},
	})
	errs := profile.ValidateSemantic(p)
	if len(errs) == 0 {
		t.Fatal("expected 1 warning for archiveOnDestroy without perSandbox, got none")
	}
	found := false
	for _, e := range errs {
		if e.IsWarning && strings.Contains(e.Message, "archiveOnDestroy") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected IsWarning=true with 'archiveOnDestroy' message, got: %v", errs)
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
	p := minimalNotificationWith(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			ChannelOverride: "not-a-channel",
		},
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
// email.enabled=&false and slack.enabled=&false is a warning.
func TestValidateSemantic_Slack_BothChannelsDisabled_Warning(t *testing.T) {
	p := minimalNotificationWith(&profile.NotificationSpec{
		Email: &profile.NotificationEmailSpec{Enabled: boolPtr(false)},
		Slack: &profile.NotificationSlackSpec{Enabled: boolPtr(false)},
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
// (slack.enabled=true, perSandbox=true, archiveOnDestroy=true, no override) emits no errors.
func TestValidateSemantic_Slack_HappyPath_NoErrors(t *testing.T) {
	p := minimalNotificationWith(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:          boolPtr(true),
			PerSandbox:       boolPtr(true),
			ArchiveOnDestroy: boolPtr(true),
		},
	})
	errs := profile.ValidateSemantic(p)
	if len(errs) != 0 {
		t.Errorf("expected no errors for valid Slack happy-path config, got: %v", errs)
	}
}

// TestValidateSemantic_Slack_BackwardCompat_Phase62Profile verifies that a profile
// with only event/email fields and no Slack block produces zero Slack-related errors.
// This is the critical Phase 62 backward-compat regression test.
func TestValidateSemantic_Slack_BackwardCompat_Phase62Profile(t *testing.T) {
	p := minimalNotificationWith(&profile.NotificationSpec{
		Events: &profile.NotificationEventsSpec{
			OnPermission:    boolPtr(true),
			OnIdle:          boolPtr(true),
			CooldownSeconds: intPtr(60),
		},
		Email: &profile.NotificationEmailSpec{Address: "ops@example.com"},
		// No slack block (the old Phase 63 fields absent).
	})
	errs := profile.ValidateSemantic(p)
	if len(errs) != 0 {
		t.Errorf("expected zero errors for Phase 62 backward-compat profile, got: %v", errs)
	}
}

// ============================================================
// Phase 87 Wave 1 (plan-02 SNAP-02): Layer 1 validation tests
// ============================================================

// makeMinimalSnapshotProfile builds a SandboxProfile with just enough fields
// populated for semantic validation of additionalSnapshots.
func makeMinimalSnapshotProfile(substrate string, snapshots []profile.AdditionalSnapshotSpec, addlVol *profile.AdditionalVolumeSpec) *profile.SandboxProfile {
	return &profile.SandboxProfile{
		APIVersion: "klankermaker.ai/v1alpha2",
		Kind:       "SandboxProfile",
		Spec: profile.Spec{
			Runtime: profile.RuntimeSpec{
				Substrate:           substrate,
				AdditionalSnapshots: snapshots,
				AdditionalVolume:    addlVol,
			},
		},
	}
}

func TestValidateAdditionalSnapshots_Layer1(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	_ = boolPtr // may be used below

	tests := []struct {
		name         string
		profile      *profile.SandboxProfile
		wantErrCount int   // -1 = at least one
		wantPaths    []string // substrings that must appear in at least one error path
		wantMsgs     []string // substrings that must appear in at least one error message
	}{
		// ------- REJECTION CASES -------
		{
			name: "bad_snapshot_id_regex_uppercase",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-XYZ", MountPoint: "/data"},
			}, nil),
			wantErrCount: -1,
			wantPaths:    []string{"spec.runtime.additionalSnapshots[0].snapshotId"},
			wantMsgs:     []string{"snap-XYZ"},
		},
		{
			name: "bad_snapshot_id_too_short",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-01", MountPoint: "/data"},
			}, nil),
			wantErrCount: -1,
			wantPaths:    []string{"spec.runtime.additionalSnapshots[0].snapshotId"},
			wantMsgs:     []string{"snap-01"},
		},
		{
			name: "mountpoint_not_absolute",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-0123abcd", MountPoint: "data"},
			}, nil),
			wantErrCount: -1,
			wantPaths:    []string{"spec.runtime.additionalSnapshots[0].mountPoint"},
			wantMsgs:     []string{"absolute"},
		},
		{
			name: "mountpoint_reserved_root",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-0123abcd", MountPoint: "/"},
			}, nil),
			wantErrCount: -1,
			wantPaths:    []string{"spec.runtime.additionalSnapshots[0].mountPoint"},
			wantMsgs:     []string{"reserved"},
		},
		{
			name: "mountpoint_reserved_workspace",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-0123abcd", MountPoint: "/workspace"},
			}, nil),
			wantErrCount: -1,
			wantPaths:    []string{"spec.runtime.additionalSnapshots[0].mountPoint"},
			wantMsgs:     []string{"reserved"},
		},
		{
			name: "mountpoint_reserved_opt_exact",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-0123abcd", MountPoint: "/opt"},
			}, nil),
			wantErrCount: -1,
			wantPaths:    []string{"spec.runtime.additionalSnapshots[0].mountPoint"},
			wantMsgs:     []string{"reserved"},
		},
		{
			name: "mountpoint_collision_with_additional_volume",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-0123abcd", MountPoint: "/models"},
			}, &profile.AdditionalVolumeSpec{
				Size:       100,
				MountPoint: "/models",
			}),
			wantErrCount: -1,
			wantPaths:    []string{"spec.runtime.additionalSnapshots[0].mountPoint"},
			wantMsgs:     []string{"collides"},
		},
		{
			name: "mountpoint_collision_across_snapshots",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-0123abcd", MountPoint: "/data"},
				{SnapshotID: "snap-0123dcba", MountPoint: "/data"},
			}, nil),
			wantErrCount: -1,
			wantPaths:    []string{"spec.runtime.additionalSnapshots[1].mountPoint"},
			wantMsgs:     []string{"/data"},
		},
		{
			name: "explicit_device_duplicate",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-0123abcd", MountPoint: "/data1", Device: "/dev/sdh"},
				{SnapshotID: "snap-0123dcba", MountPoint: "/data2", Device: "/dev/sdh"},
			}, nil),
			wantErrCount: -1,
			wantPaths:    []string{"spec.runtime.additionalSnapshots[1].device"},
			wantMsgs:     []string{"/dev/sdh"},
		},
		{
			name: "non_ec2_substrate_docker",
			profile: makeMinimalSnapshotProfile("docker", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-0123abcd", MountPoint: "/data"},
			}, nil),
			wantErrCount: -1,
			wantPaths:    []string{"spec.runtime.additionalSnapshots"},
			wantMsgs:     []string{"docker substrate"},
		},
		{
			name: "size_negative",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-0123abcd", MountPoint: "/data", Size: -1},
			}, nil),
			wantErrCount: -1,
			wantPaths:    []string{"spec.runtime.additionalSnapshots[0].size"},
			wantMsgs:     []string{"-1"},
		},

		// ------- HAPPY PATH CASES (must produce NO errors) -------
		{
			name: "empty_snapshots_no_validation_overhead",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{}, nil),
			wantErrCount: 0,
		},
		{
			name: "nil_snapshots_no_errors",
			profile: makeMinimalSnapshotProfile("ec2", nil, nil),
			wantErrCount: 0,
		},
		{
			name: "single_minimal_entry_valid",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-0123abcd", MountPoint: "/data"},
			}, nil),
			wantErrCount: 0,
		},
		{
			name: "canonical_17char_snapshot_id_valid",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-0123abcdef01234", MountPoint: "/data"},
			}, nil),
			wantErrCount: 0,
		},
		{
			name: "mountpoint_subpath_of_opt_ok",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-0123abcd", MountPoint: "/opt/models"},
			}, nil),
			wantErrCount: 0,
		},
		{
			name: "size_zero_is_ok_inherit",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-0123abcd", MountPoint: "/data", Size: 0},
			}, nil),
			wantErrCount: 0,
		},
		{
			name: "three_entries_distinct",
			profile: makeMinimalSnapshotProfile("ec2", []profile.AdditionalSnapshotSpec{
				{SnapshotID: "snap-0123abcd", MountPoint: "/data1"},
				{SnapshotID: "snap-0123dcba", MountPoint: "/data2"},
				{SnapshotID: "snap-0000aaaa", MountPoint: "/data3"},
			}, nil),
			wantErrCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := profile.ValidateSemantic(tc.profile)
			// Filter to only additionalSnapshots-related errors
			var snapErrs []profile.ValidationError
			for _, e := range errs {
				if strings.Contains(e.Path, "additionalSnapshots") || strings.Contains(e.Message, "additionalSnapshots") {
					snapErrs = append(snapErrs, e)
				}
			}

			if tc.wantErrCount == 0 {
				if len(snapErrs) != 0 {
					t.Errorf("expected 0 additionalSnapshots errors, got %d:", len(snapErrs))
					for _, e := range snapErrs {
						t.Logf("  - %s: %s", e.Path, e.Message)
					}
				}
				return
			}

			// wantErrCount == -1: expect at least one error
			if len(snapErrs) == 0 {
				t.Fatalf("expected at least one additionalSnapshots validation error, got none")
			}

			for _, wantPath := range tc.wantPaths {
				found := false
				for _, e := range snapErrs {
					if strings.Contains(e.Path, wantPath) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error with path containing %q, got errors: %v", wantPath, snapErrs)
				}
			}

			for _, wantMsg := range tc.wantMsgs {
				found := false
				for _, e := range snapErrs {
					if strings.Contains(e.Message, wantMsg) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error with message containing %q, got errors: %v", wantMsg, snapErrs)
				}
			}
		})
	}
}

// ============================================================
// Phase 93 Wave 0: Desktop semantic validation stubs (DSK-03)
// Wave 2 (93-02) implements ValidateSemantic desktop checks.
// ============================================================

// ============================================================
// Phase 93 Wave 2 (plan-02): Desktop semantic validation tests
// DSK-03-VALIDATE: mode enum, browsers membership, geometry, Ubuntu guard
// ============================================================

// makeDesktopProfile builds a minimal SandboxProfile with spec.runtime.desktop
// configured as specified. The desktop block is always enabled (Enabled = &true).
// ami may be empty to use the platform default.
func makeDesktopProfile(mode string, browsers []string, geometry string, ami string) *profile.SandboxProfile {
	t := true
	return &profile.SandboxProfile{
		APIVersion: "klankermaker.ai/v1alpha2",
		Kind:       "SandboxProfile",
		Spec: profile.Spec{
			Runtime: profile.RuntimeSpec{
				AMI: ami,
				Desktop: &profile.RuntimeDesktopSpec{
					Enabled:  &t,
					Mode:     mode,
					Browsers: browsers,
					Geometry: geometry,
				},
			},
		},
	}
}

// makeDisabledDesktopProfile builds a profile with desktop.enabled = false so
// desktop rules must not fire even with otherwise invalid values.
func makeDisabledDesktopProfile(ami string) *profile.SandboxProfile {
	f := false
	return &profile.SandboxProfile{
		APIVersion: "klankermaker.ai/v1alpha2",
		Kind:       "SandboxProfile",
		Spec: profile.Spec{
			Runtime: profile.RuntimeSpec{
				AMI: ami,
				Desktop: &profile.RuntimeDesktopSpec{
					Enabled: &f,
					Mode:    "gnome", // invalid — but desktop is off
				},
			},
		},
	}
}

// desktopErrs filters ValidateSemantic output to only errors whose Path contains
// "desktop" so unrelated profile-level errors (e.g. TTL) don't affect assertions.
func desktopErrs(p *profile.SandboxProfile) []profile.ValidationError {
	var out []profile.ValidationError
	for _, e := range profile.ValidateSemantic(p) {
		if strings.Contains(e.Path, "desktop") {
			out = append(out, e)
		}
	}
	return out
}

// TestDesktopValidateMode asserts mode ∈ {kiosk, full}; invalid mode → ERROR;
// disabled profile → no desktop errors.
func TestDesktopValidateMode(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		wantErr   bool
		wantField string
	}{
		{"kiosk is valid", "kiosk", false, ""},
		{"full is valid", "full", false, ""},
		{"empty mode defaults to kiosk (valid)", "", false, ""},
		{"gnome is invalid", "gnome", true, "spec.runtime.desktop.mode"},
		{"kde is invalid", "kde", true, "spec.runtime.desktop.mode"},
		{"KIOSK uppercase is invalid", "KIOSK", true, "spec.runtime.desktop.mode"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Use ubuntu-24.04 so AMI guard doesn't fire
			p := makeDesktopProfile(tc.mode, []string{"firefox"}, "", "ubuntu-24.04")
			errs := desktopErrs(p)

			var hardErrs []profile.ValidationError
			for _, e := range errs {
				if !e.IsWarning {
					hardErrs = append(hardErrs, e)
				}
			}

			if tc.wantErr {
				if len(hardErrs) == 0 {
					t.Fatalf("mode=%q: expected hard error, got none", tc.mode)
				}
				found := false
				for _, e := range hardErrs {
					if strings.Contains(e.Path, tc.wantField) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("mode=%q: expected error at path %q, got: %v", tc.mode, tc.wantField, hardErrs)
				}
			} else {
				if len(hardErrs) != 0 {
					t.Errorf("mode=%q: expected no hard errors, got: %v", tc.mode, hardErrs)
				}
			}
		})
	}

	// Disabled profile must emit no desktop errors
	t.Run("disabled profile no desktop errors", func(t *testing.T) {
		p := makeDisabledDesktopProfile("amazon-linux-2023")
		errs := desktopErrs(p)
		if len(errs) != 0 {
			t.Errorf("disabled desktop profile: expected no desktop errors, got: %v", errs)
		}
	})
}

// TestDesktopValidateBrowsers asserts browsers ⊆ {firefox, chromium, chrome, brave};
// invalid browser → ERROR; browsers empty with kiosk mode → ERROR;
// browsers empty with full mode → OK.
func TestDesktopValidateBrowsers(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		browsers  []string
		wantErr   bool
		wantField string
	}{
		{"firefox ok", "kiosk", []string{"firefox"}, false, ""},
		{"chromium ok", "kiosk", []string{"chromium"}, false, ""},
		{"chrome ok", "kiosk", []string{"chrome"}, false, ""},
		{"brave ok", "kiosk", []string{"brave"}, false, ""},
		{"all four ok", "full", []string{"firefox", "chromium", "chrome", "brave"}, false, ""},
		{"edge is invalid", "kiosk", []string{"edge"}, true, "spec.runtime.desktop.browsers"},
		{"safari is invalid", "kiosk", []string{"safari"}, true, "spec.runtime.desktop.browsers"},
		{"mixed valid and invalid", "kiosk", []string{"firefox", "edge"}, true, "spec.runtime.desktop.browsers"},
		{"empty browsers kiosk -> error", "kiosk", []string{}, true, "spec.runtime.desktop.browsers"},
		{"nil browsers kiosk -> error (treated as empty)", "kiosk", nil, true, "spec.runtime.desktop.browsers"},
		{"empty browsers full -> ok", "full", []string{}, false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := makeDesktopProfile(tc.mode, tc.browsers, "", "ubuntu-24.04")
			errs := desktopErrs(p)

			var hardErrs []profile.ValidationError
			for _, e := range errs {
				if !e.IsWarning {
					hardErrs = append(hardErrs, e)
				}
			}

			if tc.wantErr {
				if len(hardErrs) == 0 {
					t.Fatalf("mode=%q browsers=%v: expected hard error, got none", tc.mode, tc.browsers)
				}
				if tc.wantField != "" {
					found := false
					for _, e := range hardErrs {
						if strings.Contains(e.Path, tc.wantField) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("mode=%q browsers=%v: expected error at path containing %q, got: %v",
							tc.mode, tc.browsers, tc.wantField, hardErrs)
					}
				}
			} else {
				if len(hardErrs) != 0 {
					t.Errorf("mode=%q browsers=%v: expected no hard errors, got: %v", tc.mode, tc.browsers, hardErrs)
				}
			}
		})
	}
}

// TestDesktopValidateGeometry asserts geometry matches ^[0-9]+x[0-9]+$;
// empty geometry (unset) is OK; invalid patterns → ERROR.
func TestDesktopValidateGeometry(t *testing.T) {
	tests := []struct {
		name     string
		geometry string
		wantErr  bool
	}{
		{"1920x1080 ok", "1920x1080", false},
		{"1280x720 ok", "1280x720", false},
		{"empty (unset) ok", "", false},
		{"capital X is invalid", "1920X1080", true},
		{"word huge is invalid", "huge", true},
		{"no height is invalid", "1920x", true},
		{"no width is invalid", "x1080", true},
		{"spaces not ok", "1920 x 1080", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := makeDesktopProfile("kiosk", []string{"firefox"}, tc.geometry, "ubuntu-24.04")
			errs := desktopErrs(p)

			var hardErrs []profile.ValidationError
			for _, e := range errs {
				if !e.IsWarning {
					hardErrs = append(hardErrs, e)
				}
			}

			if tc.wantErr {
				if len(hardErrs) == 0 {
					t.Fatalf("geometry=%q: expected hard error, got none", tc.geometry)
				}
				found := false
				for _, e := range hardErrs {
					if strings.Contains(e.Path, "spec.runtime.desktop.geometry") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("geometry=%q: expected error at spec.runtime.desktop.geometry, got: %v", tc.geometry, hardErrs)
				}
			} else {
				if len(hardErrs) != 0 {
					t.Errorf("geometry=%q: expected no hard errors, got: %v", tc.geometry, hardErrs)
				}
			}
		})
	}
}

// TestDesktopValidateUbuntuGuard asserts:
//   - ubuntu-24.04 → no error
//   - ubuntu-22.04 → no error
//   - amazon-linux-2023 → hard ERROR
//   - "" (empty, defaults to AL2023) → hard ERROR
//   - raw AMI ID → WARN (IsWarning true), not ERROR
//   - desktop disabled + non-ubuntu → no error
func TestDesktopValidateUbuntuGuard(t *testing.T) {
	tests := []struct {
		name       string
		ami        string
		wantHard   bool
		wantWarn   bool
		wantNoErrs bool
	}{
		{"ubuntu-24.04 ok", "ubuntu-24.04", false, false, true},
		{"ubuntu-22.04 ok", "ubuntu-22.04", false, false, true},
		{"amazon-linux-2023 hard error", "amazon-linux-2023", true, false, false},
		{"empty ami hard error (defaults al2023)", "", true, false, false},
		{"raw ami id warn not error", "ami-0123456789abcdef0", false, true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := makeDesktopProfile("kiosk", []string{"firefox"}, "", tc.ami)
			errs := desktopErrs(p)

			var hardErrs, warnErrs []profile.ValidationError
			for _, e := range errs {
				if e.IsWarning {
					warnErrs = append(warnErrs, e)
				} else {
					hardErrs = append(hardErrs, e)
				}
			}

			if tc.wantNoErrs {
				if len(errs) != 0 {
					t.Errorf("ami=%q: expected no desktop errors, got: %v", tc.ami, errs)
				}
				return
			}

			if tc.wantHard {
				if len(hardErrs) == 0 {
					t.Fatalf("ami=%q: expected hard error (non-Ubuntu), got none (all errs: %v)", tc.ami, errs)
				}
				// Must not be a warn-only situation
				hasAMIGuardError := false
				for _, e := range hardErrs {
					if strings.Contains(e.Path, "desktop") {
						hasAMIGuardError = true
						break
					}
				}
				if !hasAMIGuardError {
					t.Errorf("ami=%q: hard error not at desktop path, got: %v", tc.ami, hardErrs)
				}
			}

			if tc.wantWarn {
				if len(warnErrs) == 0 {
					t.Fatalf("ami=%q: expected WARN for raw AMI ID, got none (all errs: %v)", tc.ami, errs)
				}
				// Must NOT produce a hard error for raw AMI
				if len(hardErrs) != 0 {
					t.Errorf("ami=%q: raw AMI ID should produce WARN only, got hard errors: %v", tc.ami, hardErrs)
				}
				hasAMIGuardWarn := false
				for _, e := range warnErrs {
					if strings.Contains(e.Path, "desktop") {
						hasAMIGuardWarn = true
						break
					}
				}
				if !hasAMIGuardWarn {
					t.Errorf("ami=%q: WARN not at desktop path, got: %v", tc.ami, warnErrs)
				}
			}
		})
	}

	// Disabled desktop + non-ubuntu AMI must produce no desktop errors
	t.Run("disabled desktop non-ubuntu no error", func(t *testing.T) {
		p := makeDisabledDesktopProfile("amazon-linux-2023")
		errs := desktopErrs(p)
		if len(errs) != 0 {
			t.Errorf("disabled desktop + amazon-linux-2023: expected no desktop errors, got: %v", errs)
		}
	})
}

// TestIsAbstractFragment verifies the YAML metadata.abstract detector.
func TestIsAbstractFragment(t *testing.T) {
	t.Run("abstract true", func(t *testing.T) {
		raw := []byte("metadata:\n  name: base\n  abstract: true\n")
		if !profile.IsAbstractFragment(raw) {
			t.Error("expected IsAbstractFragment to return true for metadata.abstract: true")
		}
	})

	t.Run("abstract false", func(t *testing.T) {
		raw := []byte("metadata:\n  name: base\n  abstract: false\n")
		if profile.IsAbstractFragment(raw) {
			t.Error("expected IsAbstractFragment to return false for metadata.abstract: false")
		}
	})

	t.Run("no abstract key", func(t *testing.T) {
		raw := []byte("metadata:\n  name: base\n")
		if profile.IsAbstractFragment(raw) {
			t.Error("expected IsAbstractFragment to return false when no abstract key")
		}
	})

	t.Run("malformed YAML no panic", func(t *testing.T) {
		raw := []byte(": : invalid yaml :")
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("IsAbstractFragment panicked on malformed YAML: %v", r)
			}
		}()
		result := profile.IsAbstractFragment(raw)
		if result {
			t.Error("expected IsAbstractFragment to return false for malformed YAML")
		}
	})
}

// TestValidateSchemaExtendsArrayForm verifies the schema accepts extends as an array.
func TestValidateSchemaExtendsArrayForm(t *testing.T) {
	// A profile with extends as an array (two parents)
	raw := []byte(minimalCLIBase + "extends:\n  - base/a\n  - base/b\n")
	schemaErrs := profile.ValidateSchema(raw)
	for _, e := range schemaErrs {
		// Only flag errors about extends; other schema issues may exist in the base
		if strings.Contains(e.Message, "extends") || strings.Contains(e.Path, "extends") {
			t.Errorf("schema rejected extends as array: %v", e)
		}
	}
}

// TestValidateSchemaExtendsStringForm verifies the schema still accepts extends as a string.
func TestValidateSchemaExtendsStringForm(t *testing.T) {
	raw := []byte(minimalCLIBase + "extends: base/a\n")
	schemaErrs := profile.ValidateSchema(raw)
	for _, e := range schemaErrs {
		if strings.Contains(e.Message, "extends") || strings.Contains(e.Path, "extends") {
			t.Errorf("schema rejected extends as string: %v", e)
		}
	}
}

// ---- Phase 118: AC6 validate warn rules (RED until Plan 02 adds them) ----
//
// These tests assert that ValidateSemantic emits exactly one WARNING (not a
// hard error) when:
//   - private:true is set but perSandbox is not true (private channel has no
//     per-sandbox channel to make private).
//   - inbound.allow is non-empty but perSandbox is not true (the per-sandbox
//     DDB row is never written for shared-channel sandboxes, so allow is a no-op).
//
// Conversely, when perSandbox:true is set, NEITHER warning should appear.
//
// All four tests are RED until Plan 02 adds the warn rules to validate.go.

// TestValidateSemantic_Slack_Private_WithoutPerSandbox_Warning — AC6a.
// private:true + perSandbox absent/false → exactly one WARNING at
// spec.notification.slack.private.
func TestValidateSemantic_Slack_Private_WithoutPerSandbox_Warning(t *testing.T) {
	p := minimalNotificationWith(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled: boolPtr(true),
			Private: true,
			// PerSandbox intentionally NOT set
		},
	})
	errs := profile.ValidateSemantic(p)

	var foundWarn bool
	for _, e := range errs {
		if !e.IsWarning && strings.Contains(e.Message, "private") {
			t.Errorf("[RED until Plan 02] expected only warning (not hard error) for private without perSandbox, got hard error: %s", e.Error())
		}
		if e.IsWarning && (strings.Contains(e.Path, "slack.private") || strings.Contains(e.Message, "private")) {
			foundWarn = true
		}
	}
	if !foundWarn {
		// RED: until Plan 02 adds the warn rule.
		t.Errorf("[RED until Plan 02] expected WARNING about 'private' without perSandbox:true, got: %v", errs)
	}
}

// TestValidateSemantic_Slack_Allow_WithoutPerSandbox_Warning — AC6b.
// inbound.allow non-empty + perSandbox absent/false → exactly one WARNING.
func TestValidateSemantic_Slack_Allow_WithoutPerSandbox_Warning(t *testing.T) {
	p := minimalNotificationWith(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled: boolPtr(true),
			Inbound: &profile.NotificationSlackInboundSpec{
				Enabled: boolPtr(true),
				Allow:   []string{"U0OPERATOR"},
			},
			// PerSandbox intentionally NOT set
		},
	})
	errs := profile.ValidateSemantic(p)

	var foundWarn bool
	for _, e := range errs {
		if !e.IsWarning && strings.Contains(e.Message, "allow") {
			t.Errorf("[RED until Plan 02] expected only warning (not hard error) for allow without perSandbox, got hard error: %s", e.Error())
		}
		if e.IsWarning && (strings.Contains(e.Path, "inbound.allow") || strings.Contains(e.Message, "allow")) {
			foundWarn = true
		}
	}
	if !foundWarn {
		// RED: until Plan 02 adds the warn rule.
		t.Errorf("[RED until Plan 02] expected WARNING about 'inbound.allow' without perSandbox:true, got: %v", errs)
	}
}

// TestValidateSemantic_Slack_Private_WithPerSandbox_NoWarning — AC6c.
// private:true + perSandbox:true → no warning for private.
func TestValidateSemantic_Slack_Private_WithPerSandbox_NoWarning(t *testing.T) {
	p := minimalNotificationWith(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtr(true),
			PerSandbox: boolPtr(true),
			Private:    true,
		},
	})
	errs := profile.ValidateSemantic(p)

	for _, e := range errs {
		if strings.Contains(e.Path, "slack.private") || (strings.Contains(e.Message, "private") && strings.Contains(e.Message, "perSandbox")) {
			t.Errorf("unexpected warning/error about private with perSandbox:true: %s", e.Error())
		}
	}
}

// TestValidateSemantic_Slack_Allow_WithPerSandbox_NoWarning — AC6d.
// inbound.allow + perSandbox:true → no warning for allow.
func TestValidateSemantic_Slack_Allow_WithPerSandbox_NoWarning(t *testing.T) {
	p := minimalNotificationWith(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtr(true),
			PerSandbox: boolPtr(true),
			Inbound: &profile.NotificationSlackInboundSpec{
				Enabled: boolPtr(true),
				Allow:   []string{"U0OPERATOR"},
			},
		},
	})
	errs := profile.ValidateSemantic(p)

	for _, e := range errs {
		if strings.Contains(e.Path, "inbound.allow") || (strings.Contains(e.Message, "allow") && strings.Contains(e.Message, "perSandbox")) {
			t.Errorf("unexpected warning/error about allow with perSandbox:true: %s", e.Error())
		}
	}
}
