package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// baseProfile returns a minimal SandboxProfile for user-data tests.
func baseProfile() *profile.SandboxProfile {
	return &profile.SandboxProfile{
		Metadata: profile.Metadata{Name: "test-profile"},
		Spec: profile.Spec{
			Runtime: profile.RuntimeSpec{
				Substrate: "ec2",
				Region:    "us-east-1",
			},
			Network: profile.NetworkSpec{
				Egress: profile.EgressSpec{
					AllowedDNSSuffixes: []string{"example.com"},
					AllowedHosts:       []string{"api.example.com"},
				},
			},
		},
	}
}

// TestIMDSTokenTTL verifies the IMDS token TTL is 21600 (not 60).
func TestIMDSTokenTTL(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "21600") {
		t.Error("expected IMDS token TTL to be 21600")
	}
	if strings.Contains(out, "ttl-seconds: 60\"") || strings.Contains(out, "ttl-seconds: 60 ") {
		t.Error("expected old TTL of 60 to be replaced with 21600")
	}
}

// TestBindMountReadOnlyPaths verifies bind mounts are generated for readOnlyPaths.
func TestBindMountReadOnlyPaths(t *testing.T) {
	p := baseProfile()
	p.Spec.Policy = profile.PolicySpec{
		FilesystemPolicy: &profile.FilesystemPolicy{
			ReadOnlyPaths: []string{"/etc", "/usr"},
		},
	}
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "mount --bind") {
		t.Error("expected bind mount section with 'mount --bind'")
	}
	if !strings.Contains(out, `"/etc"`) && !strings.Contains(out, "mount --bind \"/etc\"") {
		// Check for bind mount of /etc
		if !strings.Contains(out, "/etc") {
			t.Error("expected bind mount for /etc")
		}
	}
	if !strings.Contains(out, "/usr") {
		t.Error("expected bind mount for /usr")
	}
	// Verify both steps: initial bind and remount ro
	if !strings.Contains(out, "remount,bind,ro") {
		t.Error("expected 'remount,bind,ro' for read-only bind mount")
	}
}

// TestBindMountBeforeSidecars verifies bind mounts appear before sidecar startup.
func TestBindMountBeforeSidecars(t *testing.T) {
	p := baseProfile()
	p.Spec.Policy = profile.PolicySpec{
		FilesystemPolicy: &profile.FilesystemPolicy{
			ReadOnlyPaths: []string{"/etc"},
		},
	}
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	bindMountIdx := strings.Index(out, "mount --bind")
	sidecarIdx := strings.Index(out, "km-dns-proxy")
	if bindMountIdx == -1 {
		t.Fatal("bind mount section not found")
	}
	if sidecarIdx == -1 {
		t.Fatal("sidecar section not found")
	}
	if bindMountIdx > sidecarIdx {
		t.Error("bind mount section must appear BEFORE sidecar startup")
	}
}

// ============================================================
// Claude Code OTEL telemetry env var injection tests (OTEL-01, OTEL-06, OTEL-07)
// ============================================================

// otelEnabled returns true (pointer).
func boolPtr(b bool) *bool { return &b }

// TestUserDataOTELVarsEnabledDefault verifies that when claudeTelemetry is nil (default),
// all 5 base OTEL env vars appear in user-data (OTEL-01: default enabled).
func TestUserDataOTELVarsEnabledDefault(t *testing.T) {
	p := baseProfile()
	// claudeTelemetry is nil — should default to enabled
	out, err := generateUserData(p, "sb-otel-1", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	for _, want := range []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"OTEL_METRICS_EXPORTER=otlp",
		"OTEL_LOGS_EXPORTER=otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL=grpc",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in user-data when claudeTelemetry is nil (default enabled)", want)
		}
	}
}

// TestUserDataOTELLogPromptsEnabled verifies OTEL_LOG_USER_PROMPTS=1 appears when logPrompts=true.
func TestUserDataOTELLogPromptsEnabled(t *testing.T) {
	p := baseProfile()
	enabled := true
	p.Spec.Observability = profile.ObservabilitySpec{
		ClaudeTelemetry: &profile.ClaudeTelemetrySpec{
			Enabled:    &enabled,
			LogPrompts: true,
		},
	}
	out, err := generateUserData(p, "sb-otel-2", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "OTEL_LOG_USER_PROMPTS=1") {
		t.Error("expected OTEL_LOG_USER_PROMPTS=1 when logPrompts=true")
	}
}

// TestUserDataOTELLogPromptsAbsent verifies OTEL_LOG_USER_PROMPTS is NOT present when logPrompts=false.
func TestUserDataOTELLogPromptsAbsent(t *testing.T) {
	p := baseProfile()
	enabled := true
	p.Spec.Observability = profile.ObservabilitySpec{
		ClaudeTelemetry: &profile.ClaudeTelemetrySpec{
			Enabled:    &enabled,
			LogPrompts: false,
		},
	}
	out, err := generateUserData(p, "sb-otel-3", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if strings.Contains(out, "OTEL_LOG_USER_PROMPTS") {
		t.Error("expected OTEL_LOG_USER_PROMPTS to be absent when logPrompts=false")
	}
}

// TestUserDataOTELLogToolDetailsEnabled verifies OTEL_LOG_TOOL_DETAILS=1 appears when logToolDetails=true.
func TestUserDataOTELLogToolDetailsEnabled(t *testing.T) {
	p := baseProfile()
	enabled := true
	p.Spec.Observability = profile.ObservabilitySpec{
		ClaudeTelemetry: &profile.ClaudeTelemetrySpec{
			Enabled:        &enabled,
			LogToolDetails: true,
		},
	}
	out, err := generateUserData(p, "sb-otel-4", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "OTEL_LOG_TOOL_DETAILS=1") {
		t.Error("expected OTEL_LOG_TOOL_DETAILS=1 when logToolDetails=true")
	}
}

// TestUserDataOTELDisabledExplicit verifies NO Claude OTEL env vars appear when enabled=false.
func TestUserDataOTELDisabledExplicit(t *testing.T) {
	p := baseProfile()
	disabled := false
	p.Spec.Observability = profile.ObservabilitySpec{
		ClaudeTelemetry: &profile.ClaudeTelemetrySpec{
			Enabled: &disabled,
		},
	}
	out, err := generateUserData(p, "sb-otel-5", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	for _, absent := range []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY",
		"OTEL_METRICS_EXPORTER",
		"OTEL_LOGS_EXPORTER",
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_LOG_USER_PROMPTS",
		"OTEL_LOG_TOOL_DETAILS",
		"OTEL_RESOURCE_ATTRIBUTES",
	} {
		if strings.Contains(out, absent) {
			t.Errorf("expected %q to be absent when claudeTelemetry.enabled=false", absent)
		}
	}
}

// TestUserDataOTELResourceAttributes verifies OTEL_RESOURCE_ATTRIBUTES contains sandbox metadata.
func TestUserDataOTELResourceAttributes(t *testing.T) {
	p := baseProfile()
	// claudeTelemetry nil = default enabled
	out, err := generateUserData(p, "sb-otel-6", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "OTEL_RESOURCE_ATTRIBUTES") {
		t.Error("expected OTEL_RESOURCE_ATTRIBUTES in user-data")
	}
	if !strings.Contains(out, "sandbox_id=sb-otel-6") {
		t.Error("expected sandbox_id=sb-otel-6 in OTEL_RESOURCE_ATTRIBUTES")
	}
	if !strings.Contains(out, "profile_name=test-profile") {
		t.Error("expected profile_name=test-profile in OTEL_RESOURCE_ATTRIBUTES")
	}
	if !strings.Contains(out, "substrate=ec2") {
		t.Error("expected substrate=ec2 in OTEL_RESOURCE_ATTRIBUTES")
	}
}

// TestUserDataIPTablesNoDNATForOTLP verifies that the iptables DNAT section only redirects
// ports 53, 80, and 443 — ports 4317/4318 (OTLP) are NOT in any DNAT rule.
// OTEL-07: EC2 iptables DNAT only redirects ports 53/80/443. Ports 4317/4318 (OTLP) are not
// in any DNAT rule, so localhost OTLP traffic passes through directly.
func TestUserDataIPTablesNoDNATForOTLP(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-otel-7", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	// Verify expected DNAT rules exist
	if !strings.Contains(out, "--dport 53") {
		t.Error("expected iptables DNAT rule for port 53")
	}
	if !strings.Contains(out, "--dport 80") {
		t.Error("expected iptables DNAT rule for port 80")
	}
	if !strings.Contains(out, "--dport 443") {
		t.Error("expected iptables DNAT rule for port 443")
	}
	// Verify OTLP ports are NOT in any DNAT REDIRECT rule
	// We check that neither 4317 nor 4318 appear in lines containing REDIRECT
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, "REDIRECT") {
			if strings.Contains(line, "4317") || strings.Contains(line, "4318") {
				t.Errorf("found unexpected REDIRECT rule targeting OTLP port (4317/4318): %q", line)
			}
		}
	}
}

// TestNoBindMountWithoutPolicy verifies no bind mount block without filesystemPolicy.
func TestNoBindMountWithoutPolicy(t *testing.T) {
	p := baseProfile()
	// No FilesystemPolicy set
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if strings.Contains(out, "mount --bind") {
		t.Error("expected NO bind mount section when filesystemPolicy is nil")
	}
}

// TestSpotPollLoopPresent verifies spot poll loop is included when useSpot=true.
func TestSpotPollLoopPresent(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", true)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "spot/termination-time") {
		t.Error("expected spot poll loop checking termination-time endpoint when useSpot=true")
	}
	if !strings.Contains(out, "sleep 5") {
		t.Error("expected spot poll loop to sleep 5 seconds between checks")
	}
}

// TestSpotPollLoopAbsent verifies spot poll loop is NOT included when useSpot=false.
func TestSpotPollLoopAbsent(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if strings.Contains(out, "spot/termination-time") {
		t.Error("expected NO spot poll loop when useSpot=false")
	}
}

// TestArtifactUploadScriptPresent verifies km-upload-artifacts script is included when artifacts configured.
func TestArtifactUploadScriptPresent(t *testing.T) {
	p := baseProfile()
	p.Spec.Artifacts = &profile.ArtifactsSpec{
		Paths:     []string{"/workspace/output", "/tmp/results"},
		MaxSizeMB: 100,
	}
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", true)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "km-upload-artifacts") {
		t.Error("expected km-upload-artifacts script when artifacts are configured")
	}
	if !strings.Contains(out, "/workspace/output") {
		t.Error("expected artifact path /workspace/output in upload script")
	}
}

// TestArtifactUploadScriptAbsent verifies no upload script when no artifacts and no spot.
func TestArtifactUploadScriptAbsent(t *testing.T) {
	p := baseProfile()
	// No Artifacts, no spot
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if strings.Contains(out, "km-upload-artifacts") {
		t.Error("expected NO km-upload-artifacts script when no artifacts and no spot")
	}
}

// ============================================================
// OTP secret injection tests
// ============================================================

// TestOTPSecretsInjected verifies OTP secrets from profile generate get-parameter + delete-parameter snippets.
func TestOTPSecretsInjected(t *testing.T) {
	p := baseProfile()
	p.Spec.OTP = &profile.OTPSpec{
		Secrets: []string{"/sandbox/sb-123/otp/github-token"},
	}
	out, err := generateUserData(p, "sb-123", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	// Should include get-parameter for the OTP path
	if !strings.Contains(out, "get-parameter") {
		t.Error("expected 'get-parameter' in OTP section")
	}
	if !strings.Contains(out, "/sandbox/sb-123/otp/github-token") {
		t.Error("expected OTP path in user-data")
	}
	// Should include delete-parameter for delete-after-read
	if !strings.Contains(out, "delete-parameter") {
		t.Error("expected 'delete-parameter' in OTP section for delete-after-read")
	}
}

// TestOTPEnvVarName verifies that the env var name is derived correctly from the path segment.
// /sandbox/sb-123/otp/github-token -> KM_OTP_GITHUB_TOKEN
func TestOTPEnvVarName(t *testing.T) {
	p := baseProfile()
	p.Spec.OTP = &profile.OTPSpec{
		Secrets: []string{"/sandbox/sb-123/otp/github-token"},
	}
	out, err := generateUserData(p, "sb-123", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "KM_OTP_GITHUB_TOKEN") {
		t.Error("expected env var KM_OTP_GITHUB_TOKEN derived from path segment 'github-token'")
	}
}

// TestOTPAbsentWhenNotConfigured verifies no OTP section when profile.OTP is nil.
func TestOTPAbsentWhenNotConfigured(t *testing.T) {
	p := baseProfile()
	// No OTP section
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if strings.Contains(out, "KM_OTP_") {
		t.Error("expected no KM_OTP_ env vars when OTP section is nil")
	}
}

// ============================================================
// km-tracing OTel Collector sidecar tests (OTEL-01, OTEL-03, OTEL-04)
// ============================================================

// TestUserDataOtelColContribDownload verifies that rendered user-data contains the
// aws s3 cp command to download the otelcol-contrib binary from the artifacts bucket.
func TestUserDataOtelColContribDownload(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-tracing-1", nil, "test-artifacts-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	want := `aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/otelcol-contrib" /opt/km/bin/otelcol-contrib`
	if !strings.Contains(out, want) {
		t.Errorf("expected otelcol-contrib download line in user-data:\n  want: %q", want)
	}
}

// TestUserDataOtelColContribDownloadOrder verifies the otelcol-contrib download appears after
// the existing sidecar binary downloads and before the systemd unit creation section.
func TestUserDataOtelColContribDownloadOrder(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-tracing-2", nil, "test-artifacts-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	downloadIdx := strings.Index(out, "sidecars/otelcol-contrib")
	auditLogDownloadIdx := strings.Index(out, "sidecars/audit-log")
	unitCreationIdx := strings.Index(out, "km-dns-proxy.service")
	if downloadIdx == -1 {
		t.Fatal("otelcol-contrib download not found in user-data")
	}
	if auditLogDownloadIdx == -1 {
		t.Fatal("audit-log download not found in user-data")
	}
	if unitCreationIdx == -1 {
		t.Fatal("km-dns-proxy.service unit creation not found in user-data")
	}
	if downloadIdx < auditLogDownloadIdx {
		t.Error("expected otelcol-contrib download to appear AFTER existing sidecar downloads")
	}
	if downloadIdx > unitCreationIdx {
		t.Error("expected otelcol-contrib download to appear BEFORE systemd unit creation section")
	}
}

// TestUserDataOtelColContribChmod verifies that otelcol-contrib is made executable.
func TestUserDataOtelColContribChmod(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-tracing-3", nil, "test-artifacts-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "chmod +x /opt/km/bin/otelcol-contrib") {
		t.Error("expected 'chmod +x /opt/km/bin/otelcol-contrib' in user-data")
	}
}

// ============================================================
// km-tracing systemd unit tests (OTEL-01, OTEL-03, OTEL-04)
// ============================================================

// TestUserDataKMTracingServiceUnit verifies that rendered user-data writes a km-tracing.service systemd unit.
func TestUserDataKMTracingServiceUnit(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-tracing-10", nil, "test-artifacts-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "/etc/systemd/system/km-tracing.service") {
		t.Error("expected km-tracing.service unit file written to /etc/systemd/system/km-tracing.service")
	}
}

// TestUserDataKMTracingServiceRunsAsKMSidecar verifies km-tracing.service runs as km-sidecar user.
func TestUserDataKMTracingServiceRunsAsKMSidecar(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-tracing-11", nil, "test-artifacts-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	// Find the km-tracing unit block and assert User=km-sidecar
	unitStart := strings.Index(out, "km-tracing.service")
	if unitStart == -1 {
		t.Fatal("km-tracing.service not found in user-data")
	}
	unitSection := out[unitStart:]
	// Find the end of the unit block (next UNIT heredoc terminator)
	unitEnd := strings.Index(unitSection, "\nUNIT")
	if unitEnd == -1 {
		t.Fatal("could not find UNIT terminator for km-tracing.service")
	}
	unitContent := unitSection[:unitEnd]
	if !strings.Contains(unitContent, "User=km-sidecar") {
		t.Error("expected km-tracing.service to run as User=km-sidecar")
	}
}

// TestUserDataKMTracingServiceEnvVars verifies km-tracing.service passes SANDBOX_ID, OTEL_S3_BUCKET, AWS_REGION.
func TestUserDataKMTracingServiceEnvVars(t *testing.T) {
	p := baseProfile()
	p.Spec.Runtime.Region = "us-west-2"
	out, err := generateUserData(p, "sb-tracing-12", nil, "test-artifacts-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	unitStart := strings.Index(out, "km-tracing.service")
	if unitStart == -1 {
		t.Fatal("km-tracing.service not found in user-data")
	}
	unitSection := out[unitStart:]
	unitEnd := strings.Index(unitSection, "\nUNIT")
	if unitEnd == -1 {
		t.Fatal("could not find UNIT terminator for km-tracing.service")
	}
	unitContent := unitSection[:unitEnd]
	for _, want := range []string{
		"Environment=SANDBOX_ID=sb-tracing-12",
		"Environment=OTEL_S3_BUCKET=test-artifacts-bucket",
		"Environment=AWS_REGION=us-west-2",
	} {
		if !strings.Contains(unitContent, want) {
			t.Errorf("expected %q in km-tracing.service unit", want)
		}
	}
}

// TestUserDataKMTracingServiceExecStart verifies km-tracing.service ExecStart runs otelcol-contrib with tracing config.
func TestUserDataKMTracingServiceExecStart(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-tracing-13", nil, "test-artifacts-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	unitStart := strings.Index(out, "km-tracing.service")
	if unitStart == -1 {
		t.Fatal("km-tracing.service not found in user-data")
	}
	unitSection := out[unitStart:]
	unitEnd := strings.Index(unitSection, "\nUNIT")
	if unitEnd == -1 {
		t.Fatal("could not find UNIT terminator for km-tracing.service")
	}
	unitContent := unitSection[:unitEnd]
	want := "ExecStart=/opt/km/bin/otelcol-contrib --config /etc/km/tracing/config.yaml"
	if !strings.Contains(unitContent, want) {
		t.Errorf("expected %q in km-tracing.service ExecStart", want)
	}
}

// TestUserDataKMTracingServicectlEnable verifies systemctl enable line includes km-tracing.
func TestUserDataKMTracingServicectlEnable(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-tracing-14", nil, "test-artifacts-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "systemctl enable ") {
			if !strings.Contains(line, "km-tracing") {
				t.Errorf("systemctl enable line does not include km-tracing: %q", line)
			}
			return
		}
	}
	t.Error("systemctl enable line not found in user-data")
}

// TestUserDataKMTracingServicectlStart verifies systemctl start line includes km-tracing.
func TestUserDataKMTracingServicectlStart(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-tracing-15", nil, "test-artifacts-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "systemctl start ") {
			if !strings.Contains(line, "km-tracing") {
				t.Errorf("systemctl start line does not include km-tracing: %q", line)
			}
			return
		}
	}
	t.Error("systemctl start line not found in user-data")
}

// TestUserDataKMTracingOTELS3BucketResolvesToArtifactsBucket verifies OTEL_S3_BUCKET in the
// km-tracing.service unit resolves to the KMArtifactsBucket value (not a separate bucket).
func TestUserDataKMTracingOTELS3BucketResolvesToArtifactsBucket(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-tracing-16", nil, "test-artifacts-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	// The OTEL_S3_BUCKET must equal the test-artifacts-bucket value
	if !strings.Contains(out, "Environment=OTEL_S3_BUCKET=test-artifacts-bucket") {
		t.Error("expected 'Environment=OTEL_S3_BUCKET=test-artifacts-bucket' in km-tracing.service — OTEL_S3_BUCKET must resolve to KMArtifactsBucket")
	}
}

// TestOTPMultipleSecrets verifies multiple OTP secrets are all rendered.
func TestOTPMultipleSecrets(t *testing.T) {
	p := baseProfile()
	p.Spec.OTP = &profile.OTPSpec{
		Secrets: []string{
			"/sandbox/sb-456/otp/api-key",
			"/sandbox/sb-456/otp/db-password",
		},
	}
	out, err := generateUserData(p, "sb-456", nil, "my-bucket", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "KM_OTP_API_KEY") {
		t.Error("expected KM_OTP_API_KEY for /sandbox/sb-456/otp/api-key")
	}
	if !strings.Contains(out, "KM_OTP_DB_PASSWORD") {
		t.Error("expected KM_OTP_DB_PASSWORD for /sandbox/sb-456/otp/db-password")
	}
	if !strings.Contains(out, "/sandbox/sb-456/otp/api-key") {
		t.Error("expected api-key path in user-data")
	}
	if !strings.Contains(out, "/sandbox/sb-456/otp/db-password") {
		t.Error("expected db-password path in user-data")
	}
}
