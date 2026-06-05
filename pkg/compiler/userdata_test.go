package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
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

// TestStreamDrain_RenderFlag (HOOK-01): _km_stream_drain must include the
// --render "${KM_SLACK_RENDER:-blocks}" flag so operators can downgrade
// per-sandbox rendering without redeploying the binary.
func TestStreamDrain_RenderFlag(t *testing.T) {
	p := baseProfile()
	ud, err := generateUserData(p, "sb-hook-01", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	// Assert the streaming hook includes the --render flag.
	wantSubstr := `--render "${KM_SLACK_RENDER:-blocks}"`
	if !strings.Contains(ud, wantSubstr) {
		t.Fatalf("expected streaming hook to include %q\n(first 200 chars of userdata: %s)", wantSubstr, ud[:min200(ud)])
	}

	// Assert the flag appears specifically inside _km_stream_drain (not in some
	// other km-slack call). Find the function start and end markers.
	funcStart := strings.Index(ud, "_km_stream_drain()")
	if funcStart == -1 {
		t.Fatal("_km_stream_drain function definition not found in userdata")
	}
	// Find the closing brace of the function by searching for the next `^}` pattern
	// after funcStart.
	funcRemainder := ud[funcStart:]
	// The function body ends at a line that is just `}` (function close).
	funcEnd := strings.Index(funcRemainder, "\n}")
	if funcEnd == -1 {
		t.Fatal("_km_stream_drain end brace not found; cannot isolate function body")
	}
	funcBody := funcRemainder[:funcEnd]
	if !strings.Contains(funcBody, wantSubstr) {
		t.Fatalf("--render flag present in userdata but NOT inside _km_stream_drain function body.\nFunction body (first 500 chars): %s",
			funcBody[:min500(funcBody)])
	}
}

func min200(s string) int {
	if len(s) < 200 {
		return len(s)
	}
	return 200
}

func min500(s string) int {
	if len(s) < 500 {
		return len(s)
	}
	return 500
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
		// Sidecars are brought up via `systemctl enable` (units auto-start on boot
		// via WantedBy=multi-user.target); the old `systemctl start` form was
		// refactored out. km-tracing must be co-enabled with km-dns-proxy.
		if strings.HasPrefix(trimmed, "systemctl enable ") && strings.Contains(line, "km-dns-proxy") {
			if !strings.Contains(line, "km-tracing") {
				t.Errorf("sidecar systemctl enable line does not include km-tracing: %q", line)
			}
			return
		}
	}
	t.Error("sidecar systemctl enable line (containing km-dns-proxy) not found in user-data")
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
// Updated for Phase 87: label is "additional volume" and device letter "f" is included.
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
	want := "(AWS BDM /dev/sdf) to attach"
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
		// VSCodeSSHPubKey required when VSCodeEnabled=true (default). Phase 73.
		VSCodeSSHPubKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 km-test-key",
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
		// VSCodeSSHPubKey required when VSCodeEnabled=true (default). Phase 73.
		VSCodeSSHPubKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 km-test-key",
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

// TestL7ProxyHostsWithCodex verifies that a profile with spec.cli.agent: codex
// returns api.openai.com for L7 proxy interception (Phase 88, OAI-BUDGET-07).
// RED test — gate wired by plan 88-06.
func TestL7ProxyHostsWithCodex(t *testing.T) {
	p := baseProfile()
	p.Spec.Agent = &profile.AgentSpec{Default: "codex"}
	got := buildL7ProxyHosts(p)
	want := "api.openai.com"
	if got != want {
		t.Errorf("buildL7ProxyHosts with Codex agent: got %q, want %q", got, want)
	}
}

// TestL7ProxyHostsWithCodexAndBedrock verifies that a profile with spec.cli.agent: codex,
// useBedrock: true, and GitHub sourceAccess returns ALL expected hosts (Phase 88, OAI-BUDGET-07).
// Regression guard: ensures plan 88-06 does not drop or reorder the existing Anthropic/Bedrock
// branch when wiring the Codex gate (RESEARCH.md § Pitfall #5). RED test until 88-06 lands.
func TestL7ProxyHostsWithCodexAndBedrock(t *testing.T) {
	p := baseProfile()
	p.Spec.Agent = &profile.AgentSpec{Default: "codex"}
	p.Spec.Execution.UseBedrock = true
	p.Spec.SourceAccess = profile.SourceAccessSpec{
		GitHub: &profile.GitHubAccess{
			AllowedRepos: []string{"myorg/myrepo"},
		},
	}
	got := buildL7ProxyHosts(p)
	for _, want := range []string{"github.com", ".amazonaws.com", "api.anthropic.com", "api.openai.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("buildL7ProxyHosts with Codex+Bedrock+GitHub: got %q, missing %q", got, want)
		}
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

// TestKmSendAbsentWhenNoEmail verifies km-send is NOT *deployed* when SandboxEmail is empty.
// This tests the template directly with an empty SandboxEmail (bypassing generateUserData which always sets it).
//
// NOTE (Phase 62): /opt/km/bin/km-send is referenced by the km-notify-hook script body
// (which is always installed) as the email sender. The test therefore checks for the
// km-send *installation* heredoc marker ("cat > /opt/km/bin/km-send << 'KMSEND'") rather
// than any path mention, distinguishing deployment from mere reference.
func TestKmSendAbsentWhenNoEmail(t *testing.T) {
	tmpl, err := parseUserDataTemplate()
	if err != nil {
		t.Fatalf("parseUserDataTemplate failed: %v", err)
	}
	// Use params with empty SandboxEmail to exercise the {{- if .SandboxEmail }} branch
	params := userDataParams{
		SandboxID:         "sb-noemail-1",
		SandboxEmail:      "", // explicitly empty — km-send should not be deployed
		KMArtifactsBucket: "my-bucket",
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, params); err != nil {
		t.Fatalf("template.Execute failed: %v", err)
	}
	out := buf.String()
	// Check that the km-send *install* heredoc is absent (the KMSEND marker is the deploy boundary).
	// The notify-hook script body references /opt/km/bin/km-send as a caller — that is expected
	// even when email is not configured.
	if strings.Contains(out, "cat > /opt/km/bin/km-send << 'KMSEND'") {
		t.Error("expected km-send deploy heredoc to be absent in userdata when SandboxEmail is empty")
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
		// Admin granted OS-aware: wheel (Amazon Linux) with a sudo (Ubuntu) fallback,
		// added AFTER useradd so a missing group never aborts user creation.
		"usermod -aG wheel sandbox",
		"usermod -aG sudo sandbox",
		"NOPASSWD:ALL",
		"/etc/sudoers.d/sandbox",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in userdata when Privileged=true", want)
		}
	}
}

// TestUserdataPrivilegedDisabled verifies that when Privileged=false (default), the generated
// userdata does NOT add the sandbox user to wheel group or write any sudoers entry,
// AND defensively scrubs both artifacts so a relaunch from an AMI baked off a privileged
// sandbox cannot leak sudo access (which would let the user `sudo -s` to root and escape
// the eBPF cgroup-scoped enforcement).
func TestUserdataPrivilegedDisabled(t *testing.T) {
	p := baseProfile()
	// Privileged defaults to false — no explicit set needed, but we set it for clarity.
	p.Spec.Execution.Privileged = false

	out, err := generateUserData(p, "sb-nopriv-1", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	// Must NOT grant sudo: no wheel membership, no NOPASSWD entry, no echo into sudoers.d.
	for _, notWant := range []string{
		"-G wheel",
		"NOPASSWD",
		`echo "sandbox ALL=`,
	} {
		if strings.Contains(out, notWant) {
			t.Errorf("did not expect %q in userdata when Privileged=false", notWant)
		}
	}

	// Must defensively scrub so AMI-baked sudoers/wheel cannot persist.
	for _, want := range []string{
		"rm -f /etc/sudoers.d/sandbox",
		"gpasswd -d sandbox wheel",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected defensive scrub %q in userdata when Privileged=false (AMI-bake leak path)", want)
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

	// The timeout-tee pattern keeps the audit write non-blocking (a stalled pipe
	// reader can't hang the shell). Phase 79 moved the heartbeat out of a shell
	// PROMPT_COMMAND hook (_km_heartbeat) into the km-presence systemd daemon, so
	// the pattern now appears once — in _km_audit — rather than twice.
	const pattern = "timeout 0.1 tee /run/km/audit-pipe"
	count := strings.Count(out, pattern)
	if count < 1 {
		t.Errorf("expected timeout-tee pattern %q to appear (non-blocking _km_audit write), got %d occurrences", pattern, count)
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

// ============================================================
// Phase 56.2 tests: km-bootstrap.service + script + enable + cgroup.procs hardening
// RED until Task 2 (userdata.go edits) turns them GREEN.
// ============================================================

// TestKMBootstrapServiceUnit asserts that the rendered userdata contains a
// km-bootstrap.service unit file written via heredoc with the correct directives.
// Covers: P56.2-01 (unit presence + directives), P56.2-07 (no Wants=km.slice).
func TestKMBootstrapServiceUnit(t *testing.T) {
	// proxy mode (default) — bootstrap service must be present regardless of enforcement
	p := baseProfile()
	out, err := generateUserData(p, "sb-test", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData (proxy) failed: %v", err)
	}
	for _, want := range []string{
		"cat > /etc/systemd/system/km-bootstrap.service",
		"Type=oneshot",
		"RemainAfterExit=yes",
		"Before=amazon-ssm-agent.service",
		"ExecStart=/usr/local/bin/km-bootstrap",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("proxy mode: expected %q in userdata, got absent", want)
		}
	}
	// P56.2-07: km.slice is a cgroup directory, not a systemd unit file.
	// Wants=km.slice would emit a "unit not found" warning on every boot.
	if strings.Contains(out, "Wants=km.slice") {
		t.Errorf("proxy mode: did not expect Wants=km.slice (P56.2-07: km.slice is a cgroup dir, not a systemd unit)")
	}

	// ebpf mode — bootstrap service must also be present
	p2 := baseProfile()
	p2.Spec.Network.Enforcement = "ebpf"
	out2, err := generateUserData(p2, "sb-test", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData (ebpf) failed: %v", err)
	}
	for _, want := range []string{
		"cat > /etc/systemd/system/km-bootstrap.service",
		"Type=oneshot",
		"RemainAfterExit=yes",
		"Before=amazon-ssm-agent.service",
		"ExecStart=/usr/local/bin/km-bootstrap",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(out2, want) {
			t.Errorf("ebpf mode: expected %q in userdata, got absent", want)
		}
	}
	if strings.Contains(out2, "Wants=km.slice") {
		t.Errorf("ebpf mode: did not expect Wants=km.slice (P56.2-07)")
	}
}

// TestKMBootstrapScript asserts that the rendered userdata writes the
// /usr/local/bin/km-bootstrap script with correct body for three render modes:
// default (LearnMode=false, proxy), LearnMode=true, and enforcement=ebpf.
// Covers: P56.2-02.
func TestKMBootstrapScript(t *testing.T) {
	// --- Render 1: default (LearnMode=false, enforcement=proxy) ---
	p := baseProfile()
	out, err := generateUserData(p, "sb-test", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData (default) failed: %v", err)
	}
	for _, want := range []string{
		"cat > /usr/local/bin/km-bootstrap",
		". /etc/profile.d/km-identity.sh",
		"mkdir -p /run/km",
		"chmod +x /usr/local/bin/km-bootstrap",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("default render: expected %q in userdata, got absent", want)
		}
	}
	// LearnMode=false: bootstrap script body must NOT contain learn-commands.log touch.
	// Slice the script body between the heredoc opener line's newline and BOOTSTRAPEOF terminator.
	// We find the opener, skip to the next newline (end of opener line), then find BOOTSTRAPEOF.
	openerStr := "cat > /usr/local/bin/km-bootstrap << 'BOOTSTRAPEOF'"
	startIdx := strings.Index(out, openerStr)
	if startIdx == -1 {
		t.Fatal("default render: bootstrap script heredoc absent")
	}
	// advance past the opener line to the content of the heredoc
	bodyStart := startIdx + strings.Index(out[startIdx:], "\n") + 1
	endIdx := strings.Index(out[bodyStart:], "\nBOOTSTRAPEOF")
	if endIdx == -1 {
		t.Fatal("default render: BOOTSTRAPEOF terminator absent")
	}
	scriptBody := out[bodyStart : bodyStart+endIdx]
	if strings.Contains(scriptBody, "touch /run/km/learn-commands.log") {
		t.Errorf("default render (LearnMode=false): bootstrap script body must NOT contain 'touch /run/km/learn-commands.log'")
	}

	// --- Render 2: LearnMode=true ---
	p2 := baseProfile()
	p2.Spec.Observability.LearnMode = true
	out2, err := generateUserData(p2, "sb-test", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData (LearnMode=true) failed: %v", err)
	}
	// Expect at least 2 occurrences: one from cloud-init block, one from bootstrap script.
	if got := strings.Count(out2, "touch /run/km/learn-commands.log"); got < 2 {
		t.Errorf("LearnMode=true: expected >=2 occurrences of 'touch /run/km/learn-commands.log' (cloud-init + bootstrap), got %d", got)
	}
	if got := strings.Count(out2, "chmod 666 /run/km/learn-commands.log"); got < 2 {
		t.Errorf("LearnMode=true: expected >=2 occurrences of 'chmod 666 /run/km/learn-commands.log' (cloud-init + bootstrap), got %d", got)
	}

	// --- Render 3: enforcement=ebpf (bootstrap must create cgroup scope) ---
	p3 := baseProfile()
	p3.Spec.Network.Enforcement = "ebpf"
	out3, err := generateUserData(p3, "sb-test", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData (ebpf) failed: %v", err)
	}
	// Slice the bootstrap script body: skip past the opener line to the BOOTSTRAPEOF terminator.
	openerStr3 := "cat > /usr/local/bin/km-bootstrap << 'BOOTSTRAPEOF'"
	startIdx3 := strings.Index(out3, openerStr3)
	if startIdx3 == -1 {
		t.Fatal("ebpf render: bootstrap script heredoc absent")
	}
	bodyStart3 := startIdx3 + strings.Index(out3[startIdx3:], "\n") + 1
	endIdx3 := strings.Index(out3[bodyStart3:], "\nBOOTSTRAPEOF")
	if endIdx3 == -1 {
		t.Fatal("ebpf render: BOOTSTRAPEOF terminator absent")
	}
	script3 := out3[bodyStart3 : bodyStart3+endIdx3]
	if !strings.Contains(script3, "mkdir -p") {
		t.Errorf("ebpf render: bootstrap script body missing 'mkdir -p' for cgroup scope")
	}
	if !strings.Contains(script3, "/sys/fs/cgroup/km.slice/km-${KM_SANDBOX_ID}.scope") &&
		!strings.Contains(script3, "CGROUP_DIR=") {
		t.Errorf("ebpf render: bootstrap script body missing cgroup scope path or CGROUP_DIR variable")
	}
	if !strings.Contains(script3, "chown root:sandbox") {
		t.Errorf("ebpf render: bootstrap script body missing 'chown root:sandbox'")
	}
	if !strings.Contains(script3, "chmod 664") {
		t.Errorf("ebpf render: bootstrap script body missing 'chmod 664'")
	}
}

// TestKMBootstrapEnabled asserts that the rendered userdata contains
// 'systemctl enable km-bootstrap' AFTER 'systemctl daemon-reload'.
// Covers: P56.2-03.
func TestKMBootstrapEnabled(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-test", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	idxReload := strings.Index(out, "systemctl daemon-reload")
	idxEnable := strings.Index(out, "systemctl enable km-bootstrap")

	if idxReload == -1 {
		t.Errorf("expected 'systemctl daemon-reload' in userdata")
	}
	if idxEnable == -1 {
		t.Errorf("expected 'systemctl enable km-bootstrap' in userdata")
	}
	if idxReload != -1 && idxEnable != -1 && idxEnable <= idxReload {
		t.Errorf("expected 'systemctl enable km-bootstrap' (idx %d) to appear AFTER 'systemctl daemon-reload' (idx %d)", idxEnable, idxReload)
	}
}

// TestCgroupProcsRedirectHardened asserts that both cgroup.procs write sites
// use the compound-command redirect form { echo $$ > path; } 2>/dev/null || true
// instead of the bare redirect echo $$ > path 2>/dev/null that leaks errors.
// Covers: P56.2-04 (km-cgroup.sh), P56.2-05 (km-sandbox-shell).
func TestCgroupProcsRedirectHardened(t *testing.T) {
	p := baseProfile()
	p.Spec.Network.Enforcement = "ebpf"
	out, err := generateUserData(p, "sb-test", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	// Compound-command form must appear at least twice (km-cgroup.sh + km-sandbox-shell).
	if got := strings.Count(out, "{ echo $$ > "); got < 2 {
		t.Errorf("expected >=2 compound-command cgroup.procs writes ({ echo $$ > ...), got %d", got)
	}

	// km-sandbox-shell must NOT use the bare-redirect form with variable path.
	// The bare form: echo $$ > "$CGROUP_PROCS" 2>/dev/null
	if strings.Contains(out, `echo $$ > "$CGROUP_PROCS" 2>/dev/null`) {
		t.Errorf("km-sandbox-shell still uses bare-redirect form (must use compound { ... } 2>/dev/null || true)")
	}

	// km-cgroup.sh must NOT use the bare-redirect form with the literal interpolated path.
	// The bare form: echo $$ > /sys/fs/cgroup/km.slice/km-sb-test.scope/cgroup.procs 2>/dev/null
	if strings.Contains(out, "echo $$ > /sys/fs/cgroup/km.slice/km-sb-test.scope/cgroup.procs 2>/dev/null") {
		t.Errorf("km-cgroup.sh still uses bare-redirect form (must use compound { ... } 2>/dev/null || true)")
	}
}

// ============================================================
// Phase 73 VSCode userdata tests (Wave 0 stubs — Wave 1 Plan 73-04 implements)
// ============================================================

// TestUserDataVSCodeEnabled asserts that when VSCodeEnabled is true (default),
// the generated userdata contains the sshd enable block, the authorized_keys
// injection, and restorecon for SELinux correctness.
func TestUserDataVSCodeEnabled(t *testing.T) {
	const pubKey = "ssh-ed25519 AAAAC3 km-sb-test"
	p := baseProfile()
	// VSCodeEnabled defaults to true (nil pointer). Wire pubkey through the network config.
	net := &NetworkConfig{
		VSCodeSSHPubKey: pubKey,
	}
	out, err := generateUserData(p, "sb-test", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "systemctl enable --now sshd") {
		t.Error("expected 'systemctl enable --now sshd' in userdata when VSCodeEnabled=true")
	}
	if !strings.Contains(out, pubKey) {
		t.Errorf("expected pubkey %q in userdata when VSCodeEnabled=true", pubKey)
	}
	if !strings.Contains(out, "restorecon") {
		t.Error("expected 'restorecon' in userdata when VSCodeEnabled=true (SELinux correctness)")
	}
}

// TestUserDataVSCodeDisabled asserts that when VSCodeEnabled=&false, the
// generated userdata does NOT contain the sshd block, authorized_keys, or the
// pubkey content.
func TestUserDataVSCodeDisabled(t *testing.T) {
	fls := false
	p := baseProfile()
	p.Spec.Runtime.VSCode = &profile.RuntimeVSCodeSpec{Enabled: &fls}
	// Network is nil: when VSCodeEnabled=false the template block is skipped, no pubkey needed.
	out, err := generateUserData(p, "sb-test", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if strings.Contains(out, "systemctl enable --now sshd") {
		t.Error("expected NO 'systemctl enable --now sshd' in userdata when VSCodeEnabled=false")
	}
	if strings.Contains(out, "authorized_keys") {
		t.Error("expected NO 'authorized_keys' in userdata when VSCodeEnabled=false")
	}
}

// TestUserDataVSCodePubKey asserts that the generated userdata contains the
// EXACT pubkey line passed in at column 0 (no leading whitespace).
// This covers Pitfall 3 from 73-RESEARCH.md: the heredoc embedding must be exact.
func TestUserDataVSCodePubKey(t *testing.T) {
	const exactPubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI km-sb-abc123"
	p := baseProfile()
	net := &NetworkConfig{
		VSCodeSSHPubKey: exactPubKey,
	}
	out, err := generateUserData(p, "sb-abc123", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	// The pubkey line must appear at column 0 — no leading whitespace.
	lines := strings.Split(out, "\n")
	var found bool
	for _, line := range lines {
		if strings.Contains(line, exactPubKey) {
			found = true
			if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
				t.Fatalf("pubkey line has leading whitespace (Pitfall 3): %q", line)
			}
			if !strings.HasPrefix(line, "ssh-ed25519 ") {
				t.Fatalf("pubkey line does not start with 'ssh-ed25519 ': %q", line)
			}
		}
	}
	if !found {
		t.Error("exact pubkey line not found in generated userdata (check for whitespace/quote artifacts)")
	}
}

// TestUserDataVSCodeMissingKeyErrors asserts that generateUserData returns an
// error when VSCodeEnabled=true (default) and VSCodeSSHPubKey is empty.
// This covers Pitfall 4 from 73-RESEARCH.md: loud-fail for stale Lambda deploys.
func TestUserDataVSCodeMissingKeyErrors(t *testing.T) {
	// VSCodeEnabled defaults true; missing pubkey must fail loudly when network is non-nil.
	p := baseProfile()
	net := &NetworkConfig{
		// VSCodeSSHPubKey intentionally empty
	}
	_, err := generateUserData(p, "sb-test", nil, "my-bucket", false, net)
	if err == nil {
		t.Fatal("expected error when VSCodeEnabled=true and VSCodeSSHPubKey is empty")
	}
	if !strings.Contains(err.Error(), "VSCodeEnabled=true but VSCodeSSHPubKey is empty") {
		t.Fatalf("error message missing operator hint: %v", err)
	}
	if !strings.Contains(err.Error(), "km init --sidecars") {
		t.Fatalf("error message missing remediation hint: %v", err)
	}
}

// ============================================================
// Phase 87 Wave 3: Userdata generation tests (SNAP-06, SNAP-07)
// ============================================================

// TestUserdataAdditionalVolumeOnly_GoldenByteIdentical verifies that userdata for an
// additionalVolume-only profile is byte-identical to the committed golden file.
// The golden file is the pre-Phase-87 output with ext4 replaced by ${FSTYPE} in the fstab line.
func TestUserdataAdditionalVolumeOnly_GoldenByteIdentical(t *testing.T) {
	// Determine the testdata directory relative to this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("runtime.Caller unavailable")
	}
	goldenPath := filepath.Join(filepath.Dir(thisFile), "testdata", "userdata_additional_volume_only.golden.sh")

	p := baseProfile()
	p.Spec.Runtime.AdditionalVolume = &profile.AdditionalVolumeSpec{
		Size:       30,
		MountPoint: "/data",
	}

	got, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("golden file not found at %s: %v\nRun tests once after implementing the refactor to generate the golden file.", goldenPath, err)
	}

	if string(got) != string(want) {
		t.Errorf("userdata is not byte-identical to golden file.\nTo update: delete the golden file and re-run tests to regenerate.\ndiff (-want +got):\n%s",
			diffStrings(string(want), string(got)))
	}

	// SNAP-07 aliasing-risk mitigation: explicit check that ${FSTYPE} is present as bash variable expansion.
	if !strings.Contains(got, "${FSTYPE}") {
		t.Error("rendered userdata MUST contain ${FSTYPE} (bash variable expansion) — not hardcoded ext4")
	}
}

// diffStrings returns a simple line-by-line diff for test failure messages.
func diffStrings(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	var sb strings.Builder
	max := len(wantLines)
	if len(gotLines) > max {
		max = len(gotLines)
	}
	for i := 0; i < max; i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w != g {
			sb.WriteString(fmt.Sprintf("line %d:\n  want: %q\n   got: %q\n", i+1, w, g))
		}
	}
	return sb.String()
}

// TestUserdataAdditionalSnapshots_LoopOrder verifies that a profile with additionalVolume +
// 2 additionalSnapshots renders 3 mount blocks in declaration order.
func TestUserdataAdditionalSnapshots_LoopOrder(t *testing.T) {
	p := baseProfile()
	p.Spec.Runtime.AdditionalVolume = &profile.AdditionalVolumeSpec{
		Size: 30, MountPoint: "/data",
	}
	p.Spec.Runtime.AdditionalSnapshots = []profile.AdditionalSnapshotSpec{
		{SnapshotID: "snap-0123456789abcdef0", MountPoint: "/opt/models", Device: "/dev/sdg"},
		{SnapshotID: "snap-0123456789abcdef1", MountPoint: "/opt/cache", Device: "/dev/sdh"},
	}

	got, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	// Assert exactly 3 occurrences of the section header marker (one per mount block).
	// The template renders: "# 2.6. Additional EBS volume: resolve, format (if blank) and mount ({{ .Label }})"
	const sectionMarker = "Additional EBS volume: resolve, format (if blank) and mount ("
	count := strings.Count(got, sectionMarker)
	if count != 3 {
		t.Errorf("expected 3 mount section blocks (got %d); marker=%q", count, sectionMarker)
	}

	// Assert ORDER: /data first, then /opt/models, then /opt/cache.
	idxData := strings.Index(got, `mkdir -p "/data"`)
	idxModels := strings.Index(got, `mkdir -p "/opt/models"`)
	idxCache := strings.Index(got, `mkdir -p "/opt/cache"`)

	if idxData == -1 || idxModels == -1 || idxCache == -1 {
		t.Fatalf("missing mount points in output: /data=%d /opt/models=%d /opt/cache=%d", idxData, idxModels, idxCache)
	}
	if !(idxData < idxModels && idxModels < idxCache) {
		t.Errorf("mount blocks not in declaration order: /data@%d /opt/models@%d /opt/cache@%d", idxData, idxModels, idxCache)
	}

	// Assert each device letter is resolved by BDM name exactly once (the old
	// /dev/xvdX /dev/sdX /dev/nvme1n1 guess was replaced by resolve_ebs_device).
	for _, letter := range []string{"f", "g", "h"} {
		probe := fmt.Sprintf("resolve_ebs_device %q", letter)
		c := strings.Count(got, probe)
		if c != 1 {
			t.Errorf("expected resolve_ebs_device for letter %q exactly once, got %d: probe=%q", letter, c, probe)
		}
	}

	// Assert ${FSTYPE} is present (blkid FS detection).
	if !strings.Contains(got, "${FSTYPE}") {
		t.Error("rendered userdata MUST contain ${FSTYPE} bash variable expansion")
	}
}

// TestUserdataBackwardCompat_ZeroDiffNoSnapshots verifies that a profile with neither
// additionalVolume nor additionalSnapshots renders zero mount blocks.
func TestUserdataBackwardCompat_ZeroDiffNoSnapshots(t *testing.T) {
	p := baseProfile()
	// No AdditionalVolume, no AdditionalSnapshots.

	got, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	// Zero mount blocks → ${FSTYPE} must be absent.
	if strings.Contains(got, "${FSTYPE}") {
		t.Error("rendered userdata MUST NOT contain ${FSTYPE} when no additional volumes/snapshots are configured")
	}

	// The additional EBS section header must not appear.
	const sectionHeader = "Additional EBS volume: resolve, format (if blank) and mount ("
	if strings.Contains(got, sectionHeader) {
		t.Error("rendered userdata MUST NOT contain mount section header when no additional volumes/snapshots are configured")
	}
}

// ============================================================
// Phase 93 Wave 2 (93-03): Desktop userdata tests (DSK-05, DSK-06, DSK-07, DSK-08, DSK-11)
// ============================================================

// desktopProfile returns a SandboxProfile with spec.runtime.desktop enabled
// in kiosk mode with Firefox. Used as the base fixture for desktop tests.
func desktopProfile(mode string, browsers []string, geometry string) *profile.SandboxProfile {
	tru := true
	if browsers == nil {
		browsers = []string{"firefox"}
	}
	if mode == "" {
		mode = "kiosk"
	}
	if geometry == "" {
		geometry = "1920x1080"
	}
	p := baseProfile()
	p.Spec.Runtime.Desktop = &profile.RuntimeDesktopSpec{
		Enabled:  &tru,
		Mode:     mode,
		Browsers: browsers,
		Geometry: geometry,
	}
	return p
}

// desktopNet returns a NetworkConfig with the per-sandbox KasmVNC credential.
func desktopNet(user, pass string) *NetworkConfig {
	return &NetworkConfig{
		// VSCodeSSHPubKey must be populated too since VSCode defaults to enabled.
		// Use a fake pubkey so the VSCodeEnabled gate doesn't block generation.
		VSCodeSSHPubKey: "ssh-ed25519 AAAA fake-desktop-test-key",
		DesktopKasmUser: user,
		DesktopKasmPass: pass,
	}
}

// TestUserDataDesktopEnabled asserts that an enabled desktop profile emits the
// KasmVNC install guard, the deb URL, and the kasmvncpasswd credential seed.
// Covers DSK-06-USERDATA-INSTALL.
func TestUserDataDesktopEnabled(t *testing.T) {
	p := desktopProfile("kiosk", []string{"firefox"}, "1920x1080")
	net := desktopNet("kasm", "testpass")
	out, err := generateUserData(p, "sb-desktop", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "command -v vncserver") {
		t.Error("expected 'command -v vncserver' idempotency guard in desktop block")
	}
	if !strings.Contains(out, "kasmvncserver_") {
		t.Error("expected KasmVNC deb filename reference in desktop block")
	}
	if !strings.Contains(out, "kasmvncpasswd") {
		t.Error("expected 'kasmvncpasswd' credential seed in desktop block")
	}
}

// TestUserDataDesktopLocalhostCert asserts the desktop block generates a
// self-signed TLS cert with a localhost/127.0.0.1 SAN and points KasmVNC at it,
// so https://localhost:8444 (reached via the SSM port-forward) matches the cert
// instead of the default snakeoil cert (CN = system hostname).
func TestUserDataDesktopLocalhostCert(t *testing.T) {
	p := desktopProfile("kiosk", []string{"firefox"}, "1920x1080")
	net := desktopNet("kasm", "testpass")
	out, err := generateUserData(p, "sb-cert", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "subjectAltName=DNS:localhost,IP:127.0.0.1") {
		t.Error("expected self-signed cert with localhost/127.0.0.1 SAN")
	}
	if !strings.Contains(out, "pem_certificate: /home/sandbox/.vnc/self.crt") {
		t.Error("expected kasmvnc.yaml to point pem_certificate at the localhost-SAN cert")
	}
	if !strings.Contains(out, "pem_key: /home/sandbox/.vnc/self.key") {
		t.Error("expected kasmvnc.yaml to point pem_key at the localhost-SAN key")
	}
	// The snakeoil dependency must be gone — it was the source of the hostname mismatch.
	if strings.Contains(out, "make-ssl-cert generate-default-snakeoil") {
		t.Error("snakeoil cert generation should be replaced by the localhost-SAN cert")
	}
}

// TestUserDataDesktopFullDisablesAltClick asserts full XFCE mode pre-seeds
// xfwm4 with easy_click=none, so a latched Alt modifier (common with web-VNC on
// macOS) can't turn clicks into window move/resize. Kiosk mode (matchbox) has no
// such binding and must not emit the xfwm4 config.
func TestUserDataDesktopFullDisablesAltClick(t *testing.T) {
	net := desktopNet("kasm", "testpass")

	full := desktopProfile("full", []string{"firefox"}, "1920x1080")
	out, err := generateUserData(full, "sb-full", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData(full) failed: %v", err)
	}
	if !strings.Contains(out, `name="easy_click" type="string" value="none"`) {
		t.Error("full XFCE mode must pre-seed xfwm4 easy_click=none to disable Alt+click window ops")
	}
	if !strings.Contains(out, "xfce-perchannel-xml/xfwm4.xml") {
		t.Error("full XFCE mode must write the xfwm4.xml perchannel config")
	}

	kiosk := desktopProfile("kiosk", []string{"firefox"}, "1920x1080")
	kout, err := generateUserData(kiosk, "sb-kiosk", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData(kiosk) failed: %v", err)
	}
	if strings.Contains(kout, "xfwm4.xml") {
		t.Error("kiosk mode (matchbox) must not emit the xfwm4 config — it has no Alt+click binding")
	}
}

// TestUserDataDesktopDisabled asserts that a profile with desktop.enabled=false (or
// absent desktop block) emits no KasmVNC, kasmvncpasswd, or vncserver strings.
// Covers DSK-05-COMPILER-THREAD.
func TestUserDataDesktopDisabled(t *testing.T) {
	p := baseProfile()
	// baseProfile has no desktop block — IsDesktopEnabled returns false.
	net := &NetworkConfig{
		VSCodeSSHPubKey: "ssh-ed25519 AAAA fake-disabled-test-key",
	}
	out, err := generateUserData(p, "sb-nodeskop", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	for _, forbidden := range []string{"kasmvnc", "kasmvncpasswd", "vncserver", "KasmVNC"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("disabled desktop profile must not emit %q in userdata", forbidden)
		}
	}
}

// TestUserDataDesktopKiosk asserts that kiosk mode userdata contains:
//   - matchbox-window-manager (kiosk WM)
//   - the default browser binary (firefox)
//   - kasmvnc.yaml with interface: 127.0.0.1
//   - kasmvnc.yaml with require_ssl: false
//
// Covers DSK-07-USERDATA-SESSION and DSK-11-SECURITY.
func TestUserDataDesktopKiosk(t *testing.T) {
	p := desktopProfile("kiosk", []string{"firefox"}, "1920x1080")
	net := desktopNet("kasm", "testpass")
	out, err := generateUserData(p, "sb-kiosk", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "matchbox-window-manager") {
		t.Error("kiosk mode must install and launch matchbox-window-manager")
	}
	if !strings.Contains(out, "firefox") {
		t.Error("kiosk mode with browsers=[firefox] must reference the firefox binary in xstartup")
	}
	// DSK-11-SECURITY: kasmvnc.yaml loopback binding
	if !strings.Contains(out, "127.0.0.1") {
		t.Error("kasmvnc.yaml must bind to 127.0.0.1 (loopback-only; DSK-11-SECURITY)")
	}
	// DSK-11-SECURITY: SSL disabled (default is require_ssl: true; must be explicitly false)
	if !strings.Contains(out, "require_ssl: false") {
		t.Error("kasmvnc.yaml must set require_ssl: false (loopback + SSM tunnel justification; DSK-11-SECURITY)")
	}
}

// TestUserDataDesktopFull asserts that full mode userdata contains xfce4 and startxfce4,
// and does NOT contain matchbox-window-manager.
// Covers DSK-07-USERDATA-SESSION.
func TestUserDataDesktopFull(t *testing.T) {
	p := desktopProfile("full", []string{"firefox"}, "1920x1080")
	net := desktopNet("kasm", "testpass")
	out, err := generateUserData(p, "sb-full", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "startxfce4") {
		t.Error("full mode xstartup must exec startxfce4")
	}
	if !strings.Contains(out, "xfce4") {
		t.Error("full mode must install xfce4 package")
	}
	if strings.Contains(out, "matchbox-window-manager") {
		t.Error("full mode must NOT install/launch matchbox-window-manager")
	}
}

// TestUserDataDesktopCredentialSeed asserts that the kasmvncpasswd seed command
// interpolates the correct DesktopKasmUser and DesktopKasmPass values.
// Covers DSK-08-CREDENTIAL (userdata half; km create half is tested in cmd package).
func TestUserDataDesktopCredentialSeed(t *testing.T) {
	const user = "kasm"
	const pass = "s3cr3tpass"
	p := desktopProfile("kiosk", []string{"firefox"}, "")
	net := desktopNet(user, pass)
	out, err := generateUserData(p, "sb-cred", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, user) {
		t.Errorf("expected DesktopKasmUser %q in kasmvncpasswd seed line", user)
	}
	if !strings.Contains(out, pass) {
		t.Errorf("expected DesktopKasmPass %q in kasmvncpasswd seed line", pass)
	}
	if !strings.Contains(out, "kasmvncpasswd") {
		t.Error("expected kasmvncpasswd command in credential seed block")
	}
}

// TestUserDataDesktopFirefoxPinNotSnap asserts browsers=[firefox] installs the
// Mozilla PPA DEB via an apt pin (priority 1001), not the snap-transitional. On
// Ubuntu 24.04 the archive's firefox is an epoch'd snap-transitional that an
// `apt -t o=...` flag does NOT override (target-release ≠ origin), so the pin is
// the only thing that works; the snap refuses to run under the kasmvnc cgroup.
func TestUserDataDesktopFirefoxPinNotSnap(t *testing.T) {
	p := desktopProfile("kiosk", []string{"firefox"}, "1920x1080")
	net := desktopNet("kasm", "testpass")
	out, err := generateUserData(p, "sb-ff", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "/etc/apt/preferences.d/mozilla-firefox") {
		t.Error("firefox install must write an apt pin at /etc/apt/preferences.d/mozilla-firefox")
	}
	if !strings.Contains(out, "Pin-Priority: 1001") || !strings.Contains(out, "Pin: release o=LP-PPA-mozillateam") {
		t.Error("firefox pin must be release o=LP-PPA-mozillateam at Pin-Priority 1001 (beats the epoch'd snap-transitional)")
	}
	// The old `-t 'o=LP-PPA-mozillateam'` form is a no-op (target-release ≠ origin)
	// and must not be relied on.
	if strings.Contains(out, "-t 'o=LP-PPA-mozillateam'") {
		t.Error("firefox install must not use the ineffective -t 'o=LP-PPA-mozillateam' flag; rely on the pin")
	}
	if !strings.Contains(out, "snap remove firefox") {
		t.Error("firefox install should drop the snap if a transitional dep pulled it")
	}
}

// TestUserDataDesktopBrowserParity asserts the desktop browser gets the same
// enforcement posture as the shell: (1) the xstartup session is moved into the
// km eBPF cgroup scope so the browser inherits it, and (2) with firefox + a
// proxy/both enforcement mode, a Firefox enterprise policy routes HTTPS through
// the http-proxy and trusts the MITM CA.
func TestUserDataDesktopBrowserParity(t *testing.T) {
	net := desktopNet("kasm", "testpass")

	// firefox + both → cgroup enrollment + firefox policy (proxy + CA).
	p := desktopProfile("full", []string{"firefox"}, "1920x1080")
	p.Spec.Network.Enforcement = "both"
	out, err := generateUserData(p, "sb-parity", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "km.slice/km-sb-parity.scope/cgroup.procs") {
		t.Error("xstartup must enroll the desktop session into the km eBPF cgroup scope")
	}
	if !strings.Contains(out, "/etc/firefox/policies/policies.json") {
		t.Error("firefox + both must write the Firefox enterprise policy")
	}
	if !strings.Contains(out, `"HTTPProxy": "127.0.0.1:3128"`) || !strings.Contains(out, `"SSLProxy": "127.0.0.1:3128"`) {
		t.Error("firefox policy must route HTTP/SSL through the http-proxy at 127.0.0.1:3128")
	}
	if !strings.Contains(out, `"ImportEnterpriseRoots": true`) || !strings.Contains(out, "km-proxy-ca.crt") {
		t.Error("firefox policy must trust the MITM CA (ImportEnterpriseRoots + Install km-proxy-ca.crt)")
	}

	// ebpf-only → cgroup enrollment present, but NO firefox proxy policy (no proxy
	// MITM path in pure ebpf mode).
	pe := desktopProfile("full", []string{"firefox"}, "1920x1080")
	pe.Spec.Network.Enforcement = "ebpf"
	oute, err := generateUserData(pe, "sb-ebpf", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData(ebpf) failed: %v", err)
	}
	if !strings.Contains(oute, "km.slice/km-sb-ebpf.scope/cgroup.procs") {
		t.Error("cgroup enrollment must be present regardless of enforcement mode")
	}
	if strings.Contains(oute, "/etc/firefox/policies/policies.json") {
		t.Error("ebpf-only mode has no http-proxy MITM path; firefox proxy policy must not be written")
	}

	// no firefox (chromium only) + both → no firefox policy.
	pc := desktopProfile("full", []string{"chromium"}, "1920x1080")
	pc.Spec.Network.Enforcement = "both"
	outc, err := generateUserData(pc, "sb-chromium", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData(chromium) failed: %v", err)
	}
	if strings.Contains(outc, "/etc/firefox/policies/policies.json") {
		t.Error("firefox policy must not be written when firefox is not a desktop browser")
	}
}

// TestUserDataDesktopChromeBinary asserts that browsers=[chrome] installs
// google-chrome-stable (not the raw keyword "chrome") and that the kiosk xstartup
// launches google-chrome-stable as the binary.
// Covers DSK-07-USERDATA-SESSION browser keyword→binary mapping.
func TestUserDataDesktopChromeBinary(t *testing.T) {
	p := desktopProfile("kiosk", []string{"chrome"}, "1920x1080")
	net := desktopNet("kasm", "testpass")
	out, err := generateUserData(p, "sb-chrome", nil, "my-bucket", false, net)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	// Chrome must install google-chrome-stable (official Google APT pkg name)
	if !strings.Contains(out, "google-chrome-stable") {
		t.Error("browsers=[chrome] must install google-chrome-stable (not the raw keyword 'chrome')")
	}
	// The xstartup kiosk launch must use the binary google-chrome-stable, not raw keyword
	// We verify this by checking the binary name appears in the xstartup heredoc context.
	// The template emits DesktopBrowser0Binary which maps chrome→google-chrome-stable.
	if strings.Count(out, "google-chrome-stable") < 1 {
		t.Error("expected 'google-chrome-stable' binary reference in kiosk xstartup launch")
	}
	// The Chrome apt source MUST be https:// — the sandbox SG allows only 443
	// (no port 80), so an http:// source times out on apt-get update and, under
	// set -e, aborts the entire bootstrap before the KasmVNC unit is written.
	if strings.Contains(out, "http://dl.google.com/linux/chrome/deb") {
		t.Error("Chrome apt source uses http:// — SG blocks port 80; must be https://dl.google.com")
	}
	if !strings.Contains(out, "https://dl.google.com/linux/chrome/deb/ stable main") {
		t.Error("expected Chrome apt source over https://dl.google.com/linux/chrome/deb/")
	}
}

// ============================================================
// Phase 94-05: ResourcePrefix log-group path substitution (EC2 userdata)
// ============================================================

// TestUserdataCWLogGroupResourcePrefix verifies that the EC2 km-audit-log systemd
// unit's CW_LOG_GROUP env var renders with the dynamic {prefix} from
// KM_RESOURCE_PREFIX, not the hardcoded /km/ path. Tests both "kph" (non-default)
// and "km" (default — proving the km→km no-op at the string level).
func TestUserdataCWLogGroupResourcePrefix(t *testing.T) {
	cases := []struct {
		prefix         string
		sandboxID      string
		wantCWLogGroup string
	}{
		{
			prefix:         "kph",
			sandboxID:      "sb-kph-audit-01",
			wantCWLogGroup: "Environment=CW_LOG_GROUP=/kph/sandboxes/sb-kph-audit-01/",
		},
		{
			prefix:         "km",
			sandboxID:      "sb-km-audit-01",
			wantCWLogGroup: "Environment=CW_LOG_GROUP=/km/sandboxes/sb-km-audit-01/",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run("prefix="+tc.prefix, func(t *testing.T) {
			t.Setenv("KM_RESOURCE_PREFIX", tc.prefix)

			p := baseProfile()
			out, err := generateUserData(p, tc.sandboxID, nil, "my-bucket", false, nil)
			if err != nil {
				t.Fatalf("generateUserData failed: %v", err)
			}

			if !strings.Contains(out, tc.wantCWLogGroup) {
				t.Errorf("CW_LOG_GROUP: want %q in userdata, not found", tc.wantCWLogGroup)
			}

			// Guard: hardcoded /km/sandboxes/ must NOT appear for non-km prefix.
			if tc.prefix != "km" {
				if strings.Contains(out, "CW_LOG_GROUP=/km/sandboxes/") {
					t.Errorf("found hardcoded /km/sandboxes/ in userdata for prefix=%q (should use /%s/sandboxes/)", tc.prefix, tc.prefix)
				}
			}
		})
	}
}

// TestUserdata_AdditionalVolumeBDMResolution — ADDITIONAL_SNAPSHOTS_UBUNTU_MOUNT_FIX.
// On Ubuntu (no /dev/sdX udev symlinks, no ebsnvme-id), the old mount block fell
// back to guessing /dev/nvme1n1 / /dev/nvme2n1, which mounted multi-volume
// sandboxes to the wrong points. A profile with BOTH an additionalVolume (letter
// f) and an additionalSnapshots entry (letter g) must render the BDM-name
// resolver, embed the vendored ebsnvme-id, and use claim-tracking +
// partition-descent + preserve-data instead of the NVMe-index guess.
func TestUserdata_AdditionalVolumeBDMResolution(t *testing.T) {
	p := baseProfile()
	p.Spec.Runtime.AdditionalVolume = &profile.AdditionalVolumeSpec{
		Size:       30,
		MountPoint: "/data",
	}
	p.Spec.Runtime.AdditionalSnapshots = []profile.AdditionalSnapshotSpec{
		{SnapshotID: "snap-0e27b39b19662f30a", MountPoint: "/repos"},
	}

	out, err := generateUserData(p, "sb-bdm", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	mustContain := map[string]string{
		"resolver helper defined":         "resolve_ebs_device() {",
		"resolver used for additionalVol": `resolve_ebs_device "f"`,
		"resolver used for snapshot":      `resolve_ebs_device "g"`,
		"ebsnvme-id provisioned (Ubuntu)": "/opt/km/bin/km-ebsnvme-id.py",
		"vendored AWS script embedded":    "NVME_ADMIN_IDENTIFY",
		"AL2023 system tool preferred":    `command -v ebsnvme-id`,
		"claim-tracking guard":            `mount | grep -q "^$cand "`,
		"partition descent":               "MOUNT_SRC=",
		"runs local ioctl (no network)":   "no network",
	}
	for what, sub := range mustContain {
		if !strings.Contains(out, sub) {
			t.Errorf("missing %s: expected userdata to contain %q", what, sub)
		}
	}

	// preserve-data: mkfs only happens under the "no filesystem" guard.
	if !strings.Contains(out, `if ! blkid "$DEVICE" >/dev/null 2>&1; then`) {
		t.Errorf("mkfs is not gated behind a blkid check — preserve-data invariant at risk")
	}

	// The broken NVMe-index guess must be gone from the mount path.
	if strings.Contains(out, "/dev/nvme1n1 /dev/nvme2n1") {
		t.Errorf("userdata still contains the old /dev/nvme1n1 /dev/nvme2n1 fallback guess")
	}
}

// TestUserdata_NoAdditionalVolume_NoResolver — the resolver + embedded ebsnvme-id
// must NOT bloat profiles that declare no additional volumes.
func TestUserdata_NoAdditionalVolume_NoResolver(t *testing.T) {
	p := baseProfile()
	p.Spec.Runtime.AdditionalVolume = nil
	p.Spec.Runtime.AdditionalSnapshots = nil

	out, err := generateUserData(p, "sb-novol", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	if strings.Contains(out, "resolve_ebs_device() {") {
		t.Errorf("resolver emitted for a profile with no additional volumes")
	}
	if strings.Contains(out, "km-ebsnvme-id.py") {
		t.Errorf("ebsnvme-id embedded for a profile with no additional volumes")
	}
}
