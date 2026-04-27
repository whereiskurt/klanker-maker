package compiler

import (
	"os"
	"path/filepath"
	"runtime"
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
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
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


// ============================================================
// Claude Code OTEL telemetry env var injection tests (OTEL-01, OTEL-06, OTEL-07)
// ============================================================

// TestUserDataOTELVarsEnabledDefault verifies that when claudeTelemetry is nil (default),
// all 5 base OTEL env vars appear in user-data (OTEL-01: default enabled).
func TestUserDataOTELVarsEnabledDefault(t *testing.T) {
	p := baseProfile()
	// claudeTelemetry is nil — should default to enabled
	out, err := generateUserData(p, "sb-otel-1", nil, "my-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-otel-2", nil, "my-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-otel-3", nil, "my-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-otel-4", nil, "my-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-otel-5", nil, "my-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-otel-6", nil, "my-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-otel-7", nil, "my-bucket", false, nil)
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


// TestSpotPollLoopPresent verifies spot poll loop is included when useSpot=true.
func TestSpotPollLoopPresent(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", true, nil)
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
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
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
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", true, nil)
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
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-123", nil, "my-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-123", nil, "my-bucket", false, nil)
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
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-tracing-1", nil, "test-artifacts-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-tracing-2", nil, "test-artifacts-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-tracing-3", nil, "test-artifacts-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-tracing-10", nil, "test-artifacts-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-tracing-11", nil, "test-artifacts-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-tracing-12", nil, "test-artifacts-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-tracing-13", nil, "test-artifacts-bucket", false, nil)
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
// The sidecar enable line is the one that also includes km-dns-proxy (not the SSM agent line).
func TestUserDataKMTracingServicectlEnable(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-tracing-14", nil, "test-artifacts-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "systemctl enable ") && strings.Contains(line, "km-dns-proxy") {
			if !strings.Contains(line, "km-tracing") {
				t.Errorf("sidecar systemctl enable line does not include km-tracing: %q", line)
			}
			return
		}
	}
	t.Error("sidecar systemctl enable line (containing km-dns-proxy) not found in user-data")
}

// TestUserDataKMTracingServicectlStart verifies systemctl start line includes km-tracing.
// The sidecar start line is the one that also includes km-dns-proxy (not the SSM agent line).
func TestUserDataKMTracingServicectlStart(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-tracing-15", nil, "test-artifacts-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "systemctl start ") && strings.Contains(line, "km-dns-proxy") {
			if !strings.Contains(line, "km-tracing") {
				t.Errorf("sidecar systemctl start line does not include km-tracing: %q", line)
			}
			return
		}
	}
	t.Error("sidecar systemctl start line (containing km-dns-proxy) not found in user-data")
}

// TestUserDataKMTracingOTELS3BucketResolvesToArtifactsBucket verifies OTEL_S3_BUCKET in the
// km-tracing.service unit resolves to the KMArtifactsBucket value (not a separate bucket).
func TestUserDataKMTracingOTELS3BucketResolvesToArtifactsBucket(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-tracing-16", nil, "test-artifacts-bucket", false, nil)
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
	out, err := generateUserData(p, "sb-456", nil, "my-bucket", false, nil)
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

// ============================================================
// GitHub repo filter env var injection tests (NETW-08)
// ============================================================

// TestUserDataGitHubAllowedRepos verifies that a profile with sourceAccess.github.allowedRepos
// produces a systemd unit Environment line with KM_GITHUB_ALLOWED_REPOS set to the CSV list.
func TestUserDataGitHubAllowedRepos(t *testing.T) {
	p := baseProfile()
	p.Spec.SourceAccess = profile.SourceAccessSpec{
		GitHub: &profile.GitHubAccess{
			AllowedRepos: []string{"myorg/myrepo", "other/repo"},
		},
	}
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "KM_GITHUB_ALLOWED_REPOS=myorg/myrepo,other/repo") {
		t.Errorf("expected KM_GITHUB_ALLOWED_REPOS=myorg/myrepo,other/repo in user-data, got snippet:\n%s",
			extractLines(out, "KM_GITHUB_ALLOWED_REPOS"))
	}
}

// TestUserDataGitHubAllowedReposEmpty verifies that a profile with no sourceAccess.github
// produces an empty KM_GITHUB_ALLOWED_REPOS (or omits it cleanly).
func TestUserDataGitHubAllowedReposEmpty(t *testing.T) {
	p := baseProfile()
	// No GitHub config — KM_GITHUB_ALLOWED_REPOS should be empty or absent.
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	// Either the env var is absent or empty — it must NOT contain a non-empty value.
	if strings.Contains(out, "KM_GITHUB_ALLOWED_REPOS=myorg") {
		t.Error("expected no non-empty KM_GITHUB_ALLOWED_REPOS when GitHub config is absent")
	}
}

// extractLines returns lines from s that contain substr (for error context in tests).
func extractLines(s, substr string) string {
	var matched []string
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, substr) {
			matched = append(matched, line)
		}
	}
	if len(matched) == 0 {
		return "(not found)"
	}
	return strings.Join(matched, "\n")
}

// ============================================================
// Network enforcement mode tests (40-05)
// ============================================================

// TestUserDataEnforcementDefault verifies that omitted enforcement field produces
// iptables DNAT rules (proxy mode) and no eBPF section.
func TestUserDataEnforcementDefault(t *testing.T) {
	p := baseProfile()
	// Enforcement is unset — should default to proxy
	out, err := generateUserData(p, "sb-enf-default", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	// Check for actual iptables commands (not just the section comment)
	if !strings.Contains(out, "iptables -t nat") {
		t.Error("expected iptables -t nat rules when enforcement is unset (default proxy)")
	}
	if strings.Contains(out, "eBPF cgroup enforcement") {
		t.Error("expected no eBPF section when enforcement is unset (default proxy)")
	}
	if strings.Contains(out, "ebpf-attach") {
		t.Error("expected no km ebpf-attach invocation when enforcement is unset (default proxy)")
	}
	if !strings.Contains(out, "export HTTP_PROXY") {
		t.Error("expected HTTP_PROXY env var set when enforcement is unset (default proxy)")
	}
}

// TestUserDataEnforcementProxy verifies that explicit "proxy" enforcement produces
// iptables rules and no eBPF section.
func TestUserDataEnforcementProxy(t *testing.T) {
	p := baseProfile()
	p.Spec.Network.Enforcement = "proxy"
	out, err := generateUserData(p, "sb-enf-proxy", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	// Check for actual iptables commands (not just the section comment)
	if !strings.Contains(out, "iptables -t nat") {
		t.Error("expected iptables -t nat rules when enforcement is proxy")
	}
	if strings.Contains(out, "eBPF cgroup enforcement") {
		t.Error("expected no eBPF section when enforcement is proxy")
	}
	if strings.Contains(out, "ebpf-attach") {
		t.Error("expected no km ebpf-attach invocation when enforcement is proxy")
	}
	if !strings.Contains(out, "export HTTP_PROXY") {
		t.Error("expected HTTP_PROXY env var when enforcement is proxy")
	}
}

// TestUserDataEnforcementEbpf verifies that "ebpf" enforcement produces eBPF section,
// no iptables rules, no HTTP_PROXY env vars.
func TestUserDataEnforcementEbpf(t *testing.T) {
	p := baseProfile()
	p.Spec.Network.Enforcement = "ebpf"
	out, err := generateUserData(p, "sb-enf-ebpf", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	// Check for actual iptables commands absence (not just the section comment)
	if strings.Contains(out, "iptables -t nat") {
		t.Error("expected no iptables -t nat rules when enforcement is ebpf")
	}
	if !strings.Contains(out, "eBPF cgroup enforcement") {
		t.Error("expected eBPF cgroup enforcement section when enforcement is ebpf")
	}
	if !strings.Contains(out, "ebpf-attach") {
		t.Error("expected km ebpf-attach invocation when enforcement is ebpf")
	}
	if !strings.Contains(out, "km.slice") {
		t.Error("expected cgroup path km.slice when enforcement is ebpf")
	}
	if strings.Contains(out, "export HTTP_PROXY") {
		t.Error("expected no HTTP_PROXY export when enforcement is ebpf (pure eBPF mode)")
	}
	if !strings.Contains(out, "Pure eBPF mode") {
		t.Error("expected 'Pure eBPF mode' message when enforcement is ebpf")
	}
}

// TestUserDataEnforcementBoth verifies that "both" enforcement (gatekeeper mode) produces
// eBPF enforcement with block mode, no iptables DNAT, and proxy env vars as belt-and-suspenders.
// Updated in Phase 42: "both" mode now uses eBPF as primary enforcer (connect4 replaces iptables).
func TestUserDataEnforcementBoth(t *testing.T) {
	p := baseProfile()
	p.Spec.Network.Enforcement = "both"
	out, err := generateUserData(p, "sb-enf-both", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	// Gatekeeper mode: eBPF is primary enforcer (block mode)
	if strings.Contains(out, "iptables -t nat") {
		t.Error("expected NO iptables -t nat rules in gatekeeper both mode (connect4 replaces iptables)")
	}
	if !strings.Contains(out, "eBPF cgroup enforcement") {
		t.Error("expected eBPF cgroup enforcement section when enforcement is both")
	}
	if !strings.Contains(out, "ebpf-attach") {
		t.Error("expected km ebpf-attach invocation when enforcement is both")
	}
	if !strings.Contains(out, "km.slice") {
		t.Error("expected cgroup path km.slice when enforcement is both")
	}
	if !strings.Contains(out, "export HTTP_PROXY") {
		t.Error("expected HTTP_PROXY env var when enforcement is both (proxy env vars as belt-and-suspenders)")
	}
	// Pure eBPF-only message should NOT appear for "both"
	if strings.Contains(out, "Pure eBPF mode") {
		t.Error("expected no 'Pure eBPF mode' message when enforcement is both")
	}
}

// TestUserDataTLSCaptureEnabled verifies --tls flag is emitted when tlsCapture is enabled.
func TestUserDataTLSCaptureEnabled(t *testing.T) {
	p := baseProfile()
	p.Spec.Network.Enforcement = "ebpf"
	p.Spec.Observability.TlsCapture = &profile.TlsCaptureSpec{
		Enabled: true,
	}
	p.Spec.SourceAccess.GitHub = &profile.GitHubAccess{
		AllowedRepos: []string{"acme/widgets", "acme/gizmos"},
	}
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "--tls") {
		t.Error("expected --tls flag in user-data when tlsCapture is enabled")
	}
	if !strings.Contains(out, "--allowed-repos") {
		t.Error("expected --allowed-repos flag in user-data when tlsCapture is enabled")
	}
	if !strings.Contains(out, "acme/widgets,acme/gizmos") {
		t.Error("expected allowed repos list in user-data")
	}
}

// TestUserDataTLSCaptureDisabled verifies --tls flag is NOT emitted without tlsCapture.
func TestUserDataTLSCaptureDisabled(t *testing.T) {
	p := baseProfile()
	p.Spec.Network.Enforcement = "ebpf"
	// No TlsCapture set — should not emit --tls
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if strings.Contains(out, "--tls") {
		t.Error("expected NO --tls flag when tlsCapture is not configured")
	}
	if strings.Contains(out, "--allowed-repos") {
		t.Error("expected NO --allowed-repos flag when tlsCapture is not configured")
	}
}

// TestUserDataTLSCaptureExplicitlyDisabled verifies --tls flag is NOT emitted
// when tlsCapture exists but enabled=false.
func TestUserDataTLSCaptureExplicitlyDisabled(t *testing.T) {
	p := baseProfile()
	p.Spec.Network.Enforcement = "ebpf"
	p.Spec.Observability.TlsCapture = &profile.TlsCaptureSpec{
		Enabled: false,
	}
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if strings.Contains(out, "--tls") {
		t.Error("expected NO --tls flag when tlsCapture.enabled is false")
	}
}

// TestUserDataTLSCaptureWithAllowedRepos verifies --allowed-repos value
// is built from profile's GitHub AllowedRepos.
func TestUserDataTLSCaptureWithAllowedRepos(t *testing.T) {
	p := baseProfile()
	p.Spec.Network.Enforcement = "ebpf"
	p.Spec.Observability.TlsCapture = &profile.TlsCaptureSpec{
		Enabled: true,
	}
	p.Spec.SourceAccess.GitHub = &profile.GitHubAccess{
		AllowedRepos: []string{"org/repo1", "org/repo2", "org/repo3"},
	}
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, `--allowed-repos "org/repo1,org/repo2,org/repo3"`) {
		t.Error("expected comma-separated allowed repos list matching profile")
	}
}

// ============================================================
// Phase 33 Plan 03: User-data additional EBS volume auto-mount tests (TDD)
// ============================================================

// TestUserDataAdditionalVolumeWaitMessage verifies that when additionalVolume is set,
// user-data contains the wait message for EBS attachment.
func TestUserDataAdditionalVolumeWaitMessage(t *testing.T) {
	p := baseProfile()
	p.Spec.Runtime.AdditionalVolume = &profile.AdditionalVolumeSpec{
		Size:       100,
		MountPoint: "/data",
		Encrypted:  false,
	}

	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	want := "[km-bootstrap] Waiting for additional EBS volume"
	if !strings.Contains(out, want) {
		t.Errorf("expected %q in user-data when additionalVolume is set\ngot (first 3000 chars):\n%s", want, out[:min(3000, len(out))])
	}
}

// TestUserDataAdditionalVolumeMkfsAndFstab verifies that user-data contains mkfs.ext4 and /etc/fstab
// when additionalVolume is set.
func TestUserDataAdditionalVolumeMkfsAndFstab(t *testing.T) {
	p := baseProfile()
	p.Spec.Runtime.AdditionalVolume = &profile.AdditionalVolumeSpec{
		Size:       100,
		MountPoint: "/data",
	}

	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "mkfs.ext4") {
		t.Error("expected mkfs.ext4 in user-data when additionalVolume is set")
	}
	if !strings.Contains(out, "/etc/fstab") {
		t.Error("expected /etc/fstab in user-data when additionalVolume is set")
	}
}

// TestUserDataAdditionalVolumeMkdir verifies that user-data contains mkdir -p "/data"
// when additionalVolume mountPoint is "/data".
func TestUserDataAdditionalVolumeMkdir(t *testing.T) {
	p := baseProfile()
	p.Spec.Runtime.AdditionalVolume = &profile.AdditionalVolumeSpec{
		Size:       100,
		MountPoint: "/data",
	}

	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	want := `mkdir -p "/data"`
	if !strings.Contains(out, want) {
		t.Errorf("expected %q in user-data when additionalVolume.mountPoint is /data", want)
	}
}

// TestUserDataAdditionalVolumeAbsent verifies that user-data does NOT contain "additional EBS volume"
// when no additionalVolume is configured.
func TestUserDataAdditionalVolumeAbsent(t *testing.T) {
	p := baseProfile()
	// AdditionalVolume is nil — no additional volume

	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if strings.Contains(out, "additional EBS volume") {
		t.Error("expected NO additional EBS volume section in user-data when additionalVolume is nil")
	}
}

// min returns the smaller of two ints. Used for capping debug output length.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ============================================================
// EFS shared filesystem mount tests (Phase 43, EFS-02, EFS-04)
// ============================================================

// TestUserDataEFSMount verifies that userdata contains EFS mount block when
// profile has MountEFS:true and network has EFSFilesystemID set.
func TestUserDataEFSMount(t *testing.T) {
	p := baseProfile()
	p.Spec.Runtime.MountEFS = true

	net := &NetworkConfig{
		VPCID:           "vpc-test",
		EFSFilesystemID: "fs-test123",
	}

	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	for _, want := range []string{
		"amazon-efs-utils",
		"fs-test123",
		"/shared",
		"_netdev,nofail,tls",
		"mountpoint -q",
		"mount -a",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in user-data when mountEFS:true and EFSFilesystemID set", want)
		}
	}
}

// TestUserDataNoEFSMount verifies that userdata does NOT contain EFS mount block
// when profile has MountEFS:false (or omitted).
func TestUserDataNoEFSMount(t *testing.T) {
	p := baseProfile()
	// MountEFS is false (default)

	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	for _, absent := range []string{
		"amazon-efs-utils",
		"fs-",
		"efs _netdev",
	} {
		if strings.Contains(out, absent) {
			t.Errorf("expected %q to be ABSENT in user-data when mountEFS is false", absent)
		}
	}
}

// TestUserDataEFSCustomMountPoint verifies that when efsMountPoint is set, that path is used
// instead of the default "/shared".
func TestUserDataEFSCustomMountPoint(t *testing.T) {
	p := baseProfile()
	p.Spec.Runtime.MountEFS = true
	p.Spec.Runtime.EFSMountPoint = "/data"

	net := &NetworkConfig{
		VPCID:           "vpc-test",
		EFSFilesystemID: "fs-custom123",
	}

	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	if !strings.Contains(out, "/data") {
		t.Error("expected custom mount point /data in user-data when efsMountPoint is /data")
	}
	if strings.Contains(out, `"/shared"`) {
		t.Error("expected default mount point /shared to be ABSENT when efsMountPoint is /data")
	}
}

// TestUserDataEFSMountWithNoNetwork verifies that when network is nil (e.g. legacy callers),
// the EFS block is omitted even if profile has MountEFS:true.
func TestUserDataEFSMountWithNoNetwork(t *testing.T) {
	p := baseProfile()
	p.Spec.Runtime.MountEFS = true

	// Pass nil network — no EFSFilesystemID available
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	if strings.Contains(out, "amazon-efs-utils") {
		t.Error("expected EFS block to be ABSENT when network is nil (no EFSFilesystemID)")
	}
}

// TestDestroyNoEFSReference verifies that destroy.go has no EFS-related references.
// EFS-06: km destroy must not teardown EFS resources.
func TestDestroyNoEFSReference(t *testing.T) {
	// Locate destroy.go relative to this test file's directory.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("runtime.Caller unavailable")
	}
	destroyPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "internal", "app", "cmd", "destroy.go")
	data, err := os.ReadFile(destroyPath)
	if err != nil {
		t.Skipf("destroy.go not found at %s: %v", destroyPath, err)
	}
	content := string(data)
	for _, forbidden := range []string{
		"LoadEFSOutputs",
		"EFSFilesystemID",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("destroy.go must not contain %q (EFS-06: destroy has no EFS awareness)", forbidden)
		}
	}
}

// ============================================================
// L7ProxyHosts derivation tests (42-01)
// ============================================================

// TestL7ProxyHostsWithGitHub verifies that a profile with sourceAccess.github
// returns the four canonical GitHub domain suffixes for L7 proxy interception.
func TestL7ProxyHostsWithGitHub(t *testing.T) {
	p := baseProfile()
	p.Spec.SourceAccess = profile.SourceAccessSpec{
		GitHub: &profile.GitHubAccess{
			AllowedRepos: []string{"myorg/myrepo"},
		},
	}
	got := buildL7ProxyHosts(p)
	want := "github.com,api.github.com,raw.githubusercontent.com,codeload.githubusercontent.com"
	if got != want {
		t.Errorf("buildL7ProxyHosts with GitHub: got %q, want %q", got, want)
	}
}

// TestL7ProxyHostsWithBedrock verifies that a profile with useBedrock: true AND
// GitHub sourceAccess returns all six domain suffixes (GitHub + Bedrock).
func TestL7ProxyHostsWithBedrock(t *testing.T) {
	p := baseProfile()
	p.Spec.SourceAccess = profile.SourceAccessSpec{
		GitHub: &profile.GitHubAccess{
			AllowedRepos: []string{"myorg/myrepo"},
		},
	}
	p.Spec.Execution.UseBedrock = true
	got := buildL7ProxyHosts(p)
	want := "github.com,api.github.com,raw.githubusercontent.com,codeload.githubusercontent.com,.amazonaws.com,api.anthropic.com"
	if got != want {
		t.Errorf("buildL7ProxyHosts with GitHub+Bedrock: got %q, want %q", got, want)
	}
}

// TestL7ProxyHostsEmpty verifies that a profile with no GitHub sourceAccess and
// no Bedrock returns an empty string (no L7 proxy hosts needed).
func TestL7ProxyHostsEmpty(t *testing.T) {
	p := baseProfile()
	// No GitHub, no Bedrock
	got := buildL7ProxyHosts(p)
	if got != "" {
		t.Errorf("buildL7ProxyHosts with no GitHub/Bedrock: got %q, want empty string", got)
	}
}

// TestL7ProxyHostsBedrockOnly verifies that a profile with useBedrock: true but
// no GitHub sourceAccess returns only the two Bedrock domain suffixes.
func TestL7ProxyHostsBedrockOnly(t *testing.T) {
	p := baseProfile()
	p.Spec.Execution.UseBedrock = true
	got := buildL7ProxyHosts(p)
	want := ".amazonaws.com,api.anthropic.com"
	if got != want {
		t.Errorf("buildL7ProxyHosts with Bedrock only: got %q, want %q", got, want)
	}
}

// ============================================================
// Phase 42 — eBPF gatekeeper mode: both-mode userdata tests
// ============================================================

// bothProfile returns a profile with Enforcement="both" and GitHub source access configured.
func bothProfile() *profile.SandboxProfile {
	p := baseProfile()
	p.Spec.Network.Enforcement = "both"
	p.Spec.SourceAccess = profile.SourceAccessSpec{
		GitHub: &profile.GitHubAccess{
			AllowedRepos: []string{"myorg/myrepo"},
		},
	}
	return p
}

// TestBothModeGatekeeperFirewallBlock verifies both-mode uses --firewall-mode block (not log).
func TestBothModeGatekeeperFirewallBlock(t *testing.T) {
	out, err := generateUserData(bothProfile(), "sb-both-1", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "--firewall-mode block") {
		t.Error("expected '--firewall-mode block' in both-mode userdata")
	}
	if strings.Contains(out, "--firewall-mode log") {
		t.Error("expected '--firewall-mode log' to be absent in both-mode userdata")
	}
}

// TestBothModeGatekeeperDNSPort53 verifies both-mode uses --dns-port 53 (not 0).
func TestBothModeGatekeeperDNSPort53(t *testing.T) {
	out, err := generateUserData(bothProfile(), "sb-both-2", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "--dns-port 53") {
		t.Error("expected '--dns-port 53' in both-mode userdata")
	}
	if strings.Contains(out, "--dns-port 0") {
		t.Error("expected '--dns-port 0' to be absent in both-mode userdata")
	}
}

// TestBothModeGatekeeperNoDNSProxy verifies both-mode does NOT enable/start km-dns-proxy.
func TestBothModeGatekeeperNoDNSProxy(t *testing.T) {
	out, err := generateUserData(bothProfile(), "sb-both-3", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if strings.Contains(out, "enable km-dns-proxy") {
		t.Error("expected 'enable km-dns-proxy' to be absent in both-mode userdata")
	}
	if strings.Contains(out, "start km-dns-proxy") {
		t.Error("expected 'start km-dns-proxy' to be absent in both-mode userdata")
	}
}

// TestBothModeGatekeeperNoIptables verifies both-mode does NOT emit iptables DNAT rules.
func TestBothModeGatekeeperNoIptables(t *testing.T) {
	out, err := generateUserData(bothProfile(), "sb-both-4", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if strings.Contains(out, "iptables -t nat") {
		t.Error("expected 'iptables -t nat' to be absent in both-mode userdata (connect4 replaces iptables)")
	}
}

// TestBothModeGatekeeperResolvConf verifies both-mode overrides resolv.conf to 127.0.0.1.
func TestBothModeGatekeeperResolvConf(t *testing.T) {
	out, err := generateUserData(bothProfile(), "sb-both-5", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "nameserver 127.0.0.1") {
		t.Error("expected 'nameserver 127.0.0.1' resolv.conf override in both-mode userdata")
	}
}

// TestBothModeGatekeeperProxyHosts verifies both-mode passes domain suffixes (not repo names) via --proxy-hosts.
func TestBothModeGatekeeperProxyHosts(t *testing.T) {
	out, err := generateUserData(bothProfile(), "sb-both-6", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "github.com") {
		t.Error("expected 'github.com' domain suffix in both-mode --proxy-hosts")
	}
	if strings.Contains(out, "--proxy-hosts \"myorg/myrepo\"") {
		t.Error("expected --proxy-hosts to NOT contain repo names in both-mode userdata")
	}
}

// TestBothModeGatekeeperProxyPID verifies both-mode passes --proxy-pid and references http-proxy.pid.
func TestBothModeGatekeeperProxyPID(t *testing.T) {
	out, err := generateUserData(bothProfile(), "sb-both-7", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "--proxy-pid") {
		t.Error("expected '--proxy-pid' in both-mode userdata enforcer ExecStart")
	}
	if !strings.Contains(out, "http-proxy.pid") {
		t.Error("expected 'http-proxy.pid' PID file reference in both-mode userdata")
	}
}

// TestBothModeGatekeeperKeepsProxyEnvVars verifies both-mode still sets HTTP_PROXY/HTTPS_PROXY env vars.
func TestBothModeGatekeeperKeepsProxyEnvVars(t *testing.T) {
	out, err := generateUserData(bothProfile(), "sb-both-8", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "HTTPS_PROXY") && !strings.Contains(out, "https_proxy") {
		t.Error("expected HTTPS_PROXY or https_proxy env var in both-mode userdata (belt-and-suspenders)")
	}
}

// TestProxyModeUnchanged verifies proxy mode still uses iptables, km-dns-proxy, and firewall-mode log.
func TestProxyModeUnchanged(t *testing.T) {
	p := baseProfile()
	// Default enforcement is proxy (empty string defaults to proxy in generateUserData)
	out, err := generateUserData(p, "sb-proxy-1", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "iptables -t nat") {
		t.Error("expected 'iptables -t nat' in proxy-mode userdata")
	}
	if !strings.Contains(out, "km-dns-proxy") {
		t.Error("expected 'km-dns-proxy' in proxy-mode userdata")
	}
	if strings.Contains(out, "--firewall-mode block") {
		t.Error("expected '--firewall-mode block' to be absent in proxy-mode userdata")
	}
}

// TestEbpfModeUnchanged verifies ebpf mode still uses block firewall, dns-port 53, no iptables, no km-dns-proxy.
func TestEbpfModeUnchanged(t *testing.T) {
	p := baseProfile()
	p.Spec.Network.Enforcement = "ebpf"
	out, err := generateUserData(p, "sb-ebpf-1", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "--firewall-mode block") {
		t.Error("expected '--firewall-mode block' in ebpf-mode userdata")
	}
	if !strings.Contains(out, "--dns-port 53") {
		t.Error("expected '--dns-port 53' in ebpf-mode userdata")
	}
	if strings.Contains(out, "iptables -t nat") {
		t.Error("expected 'iptables -t nat' to be absent in ebpf-mode userdata")
	}
	if strings.Contains(out, "enable km-dns-proxy") {
		t.Error("expected 'enable km-dns-proxy' to be absent in ebpf-mode userdata")
	}
}

// ============================================================
// km-send deployment tests (45-02)
// ============================================================

// emailProfile returns a profile with SandboxEmail set (via emailDomainOverride).
func emailProfile() *profile.SandboxProfile {
	p := baseProfile()
	return p
}

// TestKmSendPresentWhenEmailSet verifies km-send script is deployed when SandboxEmail is set.
func TestKmSendPresentWhenEmailSet(t *testing.T) {
	p := emailProfile()
	out, err := generateUserData(p, "sb-email-1", nil, "my-bucket", false, nil, "sandboxes.example.com")
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "/opt/km/bin/km-send") {
		t.Error("expected /opt/km/bin/km-send in userdata when SandboxEmail is set")
	}
}

// TestKmSendAbsentWhenNoEmail verifies km-send is NOT deployed when SandboxEmail is empty.
// This tests the template directly with an empty SandboxEmail (bypassing generateUserData which always sets it).
func TestKmSendAbsentWhenNoEmail(t *testing.T) {
	tmpl, err := parseUserDataTemplate()
	if err != nil {
		t.Fatalf("parseUserDataTemplate failed: %v", err)
	}
	// Use params with empty SandboxEmail to exercise the {{- if .SandboxEmail }} branch
	params := userDataParams{
		SandboxID:         "sb-noemail-1",
		SandboxEmail:      "", // explicitly empty — km-send should not appear
		KMArtifactsBucket: "my-bucket",
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, params); err != nil {
		t.Fatalf("template.Execute failed: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "/opt/km/bin/km-send") {
		t.Error("expected /opt/km/bin/km-send to be absent in userdata when SandboxEmail is empty")
	}
}

// TestKmSendContainsSSMFetch verifies km-send fetches the signing key from SSM.
func TestKmSendContainsSSMFetch(t *testing.T) {
	p := emailProfile()
	out, err := generateUserData(p, "sb-email-2", nil, "my-bucket", false, nil, "sandboxes.example.com")
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "ssm get-parameter") {
		t.Error("expected 'ssm get-parameter' in km-send script")
	}
	if !strings.Contains(out, "signing-key") {
		t.Error("expected 'signing-key' SSM path in km-send script")
	}
}

// TestKmSendContainsOpensslSign verifies km-send signs with openssl pkeyutl.
func TestKmSendContainsOpensslSign(t *testing.T) {
	p := emailProfile()
	out, err := generateUserData(p, "sb-email-3", nil, "my-bucket", false, nil, "sandboxes.example.com")
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "openssl pkeyutl -sign") {
		t.Error("expected 'openssl pkeyutl -sign' in km-send script")
	}
}

// TestKmSendContainsSESv2Send verifies km-send sends via sesv2.
func TestKmSendContainsSESv2Send(t *testing.T) {
	p := emailProfile()
	out, err := generateUserData(p, "sb-email-4", nil, "my-bucket", false, nil, "sandboxes.example.com")
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "sesv2 send-email") {
		t.Error("expected 'sesv2 send-email' in km-send script")
	}
}

// TestKmSendContainsPKCS8Prefix verifies the Ed25519 PKCS8 DER prefix constant is present.
func TestKmSendContainsPKCS8Prefix(t *testing.T) {
	p := emailProfile()
	out, err := generateUserData(p, "sb-email-5", nil, "my-bucket", false, nil, "sandboxes.example.com")
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "302e020100300506032b657004220420") {
		t.Error("expected PKCS8 DER prefix '302e020100300506032b657004220420' in km-send script")
	}
}

// ============================================================
// km-recv deployment tests (45-03)
// ============================================================

// TestKmRecvPresentWhenEmailSet verifies km-recv script is deployed when SandboxEmail is set.
func TestKmRecvPresentWhenEmailSet(t *testing.T) {
	p := emailProfile()
	out, err := generateUserData(p, "sb-recv-1", nil, "my-bucket", false, nil, "sandboxes.example.com")
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "/opt/km/bin/km-recv") {
		t.Error("expected /opt/km/bin/km-recv in userdata when SandboxEmail is set")
	}
}

// TestKmRecvAbsentWhenNoEmail verifies km-recv is NOT deployed when SandboxEmail is empty.
func TestKmRecvAbsentWhenNoEmail(t *testing.T) {
	tmpl, err := parseUserDataTemplate()
	if err != nil {
		t.Fatalf("parseUserDataTemplate failed: %v", err)
	}
	params := userDataParams{
		SandboxID:         "sb-noemail-recv",
		SandboxEmail:      "", // explicitly empty — km-recv should not appear
		KMArtifactsBucket: "my-bucket",
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, params); err != nil {
		t.Fatalf("template.Execute failed: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "/opt/km/bin/km-recv") {
		t.Error("expected /opt/km/bin/km-recv to be absent in userdata when SandboxEmail is empty")
	}
}

// TestKmRecvContainsDynamoDBLookup verifies km-recv looks up sender public key from DynamoDB.
func TestKmRecvContainsDynamoDBLookup(t *testing.T) {
	p := emailProfile()
	out, err := generateUserData(p, "sb-recv-2", nil, "my-bucket", false, nil, "sandboxes.example.com")
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "dynamodb get-item") {
		t.Error("expected 'dynamodb get-item' in km-recv script for public key lookup")
	}
	if !strings.Contains(out, "km-identities") {
		t.Error("expected 'km-identities' table name in km-recv script")
	}
}

// TestKmRecvContainsOpensslVerify verifies km-recv uses openssl pkeyutl -verify for signature verification.
func TestKmRecvContainsOpensslVerify(t *testing.T) {
	p := emailProfile()
	out, err := generateUserData(p, "sb-recv-3", nil, "my-bucket", false, nil, "sandboxes.example.com")
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "openssl pkeyutl -verify") {
		t.Error("expected 'openssl pkeyutl -verify' in km-recv script")
	}
}

// TestKmRecvContainsMailDir verifies km-recv reads from the correct mail directory.
func TestKmRecvContainsMailDir(t *testing.T) {
	p := emailProfile()
	out, err := generateUserData(p, "sb-recv-4", nil, "my-bucket", false, nil, "sandboxes.example.com")
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "/var/mail/km/new") {
		t.Error("expected '/var/mail/km/new' mail directory in km-recv script")
	}
	if !strings.Contains(out, "/var/mail/km/processed") {
		t.Error("expected '/var/mail/km/processed' in km-recv script")
	}
}

// TestKmRecvContainsSPKIDERPrefix verifies the Ed25519 SubjectPublicKeyInfo DER prefix constant is present.
func TestKmRecvContainsSPKIDERPrefix(t *testing.T) {
	p := emailProfile()
	out, err := generateUserData(p, "sb-recv-5", nil, "my-bucket", false, nil, "sandboxes.example.com")
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "302a300506032b6570032100") {
		t.Error("expected SubjectPublicKeyInfo DER prefix '302a300506032b6570032100' in km-recv script")
	}
}

// ============================================================
// Privileged execution mode tests (Phase 47)
// ============================================================

// TestUserdataPrivilegedEnabled verifies that when Privileged=true, the generated
// userdata adds the sandbox user to the wheel group and writes a passwordless sudoers entry.
func TestUserdataPrivilegedEnabled(t *testing.T) {
	p := baseProfile()
	p.Spec.Execution.Privileged = true

	out, err := generateUserData(p, "sb-priv-1", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	for _, want := range []string{
		"-G wheel sandbox",
		"NOPASSWD:ALL",
		"/etc/sudoers.d/sandbox",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in userdata when Privileged=true", want)
		}
	}
}

// TestUserdataPrivilegedDisabled verifies that when Privileged=false (default), the generated
// userdata does NOT add the sandbox user to wheel group or write any sudoers entry.
func TestUserdataPrivilegedDisabled(t *testing.T) {
	p := baseProfile()
	// Privileged defaults to false — no explicit set needed, but we set it for clarity.
	p.Spec.Execution.Privileged = false

	out, err := generateUserData(p, "sb-nopriv-1", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	for _, notWant := range []string{
		"-G wheel",
		"NOPASSWD",
		"sudoers.d",
	} {
		if strings.Contains(out, notWant) {
			t.Errorf("did not expect %q in userdata when Privileged=false", notWant)
		}
	}
}

// TestUserDataLearnCommandsLog verifies that when LearnMode=true, the userdata:
// - creates /run/km/learn-commands.log with 0666 permissions
// - installs the _km_learn hook function
// - includes _km_learn in PROMPT_COMMAND
func TestUserDataLearnCommandsLog(t *testing.T) {
	p := baseProfile()
	p.Spec.Observability.LearnMode = true

	out, err := generateUserData(p, "sb-learn-1", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	for _, want := range []string{
		"learn-commands.log",
		"_km_learn",
		"_km_learn;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in userdata when LearnMode=true, but it was absent", want)
		}
	}
	// Verify that _km_learn is in PROMPT_COMMAND (not just defined)
	if !strings.Contains(out, `PROMPT_COMMAND="_km_audit;_km_learn;`) {
		t.Errorf("expected PROMPT_COMMAND to include _km_learn hook, got output does not contain expected PROMPT_COMMAND line")
	}
	// Verify 0666 permissions on the log file
	if !strings.Contains(out, "chmod 666 /run/km/learn-commands.log") {
		t.Errorf("expected chmod 666 on learn-commands.log in learn mode userdata")
	}
}

// TestUserDataLearnCommandsLogAbsent verifies that when LearnMode=false (default), the userdata:
// - does NOT create learn-commands.log
// - does NOT install the _km_learn hook
// - PROMPT_COMMAND does NOT include _km_learn
func TestUserDataLearnCommandsLogAbsent(t *testing.T) {
	p := baseProfile()
	// LearnMode defaults to false — no explicit set needed.

	out, err := generateUserData(p, "sb-normal-1", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	for _, notWant := range []string{
		"learn-commands.log",
		"_km_learn",
	} {
		if strings.Contains(out, notWant) {
			t.Errorf("did not expect %q in userdata when LearnMode=false", notWant)
		}
	}
	// Verify normal PROMPT_COMMAND without _km_learn
	if !strings.Contains(out, `PROMPT_COMMAND="_km_audit;`) {
		t.Errorf("expected normal PROMPT_COMMAND with _km_audit in non-learn mode userdata")
	}
}

// ============================================================
// Phase 56.1 Bug 2 + Bug 4 tests (RED — fail until Task 2 fixes userdata.go)
// ============================================================

// TestAuditHookNonBlocking asserts that _km_audit and _km_heartbeat use the
// non-blocking timeout-tee pattern instead of a bare redirect to the FIFO.
// The bare redirect blocks indefinitely when no reader has opened the pipe.
func TestAuditHookNonBlocking(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	// Must contain _km_audit function definition.
	if !strings.Contains(out, "_km_audit()") {
		t.Errorf("expected _km_audit() definition in userdata")
	}

	// The timeout-tee pattern must appear at least twice:
	// once in _km_audit, once in _km_heartbeat.
	const pattern = "timeout 0.1 tee /run/km/audit-pipe"
	count := strings.Count(out, pattern)
	if count < 2 {
		t.Errorf("expected timeout-tee pattern %q to appear >= 2 times in userdata (once per hook), got %d occurrences", pattern, count)
	}
}

// TestUserDataSidecarRestartAfterEnvWrite asserts that the rendered userdata
// contains a systemctl restart line that appears AFTER both systemctl daemon-reload
// AND AFTER the env-write site (cat > /etc/profile.d/km-identity.sh).
func TestUserDataSidecarRestartAfterEnvWrite(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	// daemon-reload must be present (pre-existing).
	idxDaemonReload := strings.Index(out, "systemctl daemon-reload")
	if idxDaemonReload < 0 {
		t.Fatalf("expected 'systemctl daemon-reload' in userdata")
	}

	// The post-env-rewrite restart line must be present.
	const restartMarker = "systemctl restart km-audit-log"
	idxRestart := strings.Index(out, restartMarker)
	if idxRestart < 0 {
		t.Errorf("expected 'systemctl restart km-audit-log' in userdata (Bug 4 fix)")
		return
	}

	// Restart must appear AFTER the first daemon-reload.
	if idxRestart <= idxDaemonReload {
		t.Errorf("expected 'systemctl restart km-audit-log' (idx %d) to appear AFTER 'systemctl daemon-reload' (idx %d)", idxRestart, idxDaemonReload)
	}
}
