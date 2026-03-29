package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// baseECSProfile returns a minimal SandboxProfile for ECS service.hcl tests.
func baseECSProfile() *profile.SandboxProfile {
	return &profile.SandboxProfile{
		Metadata: profile.Metadata{Name: "test-ecs-profile"},
		Spec: profile.Spec{
			Runtime: profile.RuntimeSpec{
				Substrate:    "ecs",
				Region:       "us-east-1",
				InstanceType: "512/1024",
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

func baseECSNetwork() *NetworkConfig {
	return &NetworkConfig{
		VPCID:         "vpc-12345",
		PublicSubnets: []string{"subnet-a", "subnet-b"},
		RegionLabel:   "use1",
	}
}

// TestECSReadonlyRootFilesystem verifies readonlyRootFilesystem=true is set when filesystemPolicy is configured.
func TestECSReadonlyRootFilesystem(t *testing.T) {
	p := baseECSProfile()
	p.Spec.Policy = profile.PolicySpec{
		FilesystemPolicy: &profile.FilesystemPolicy{
			ReadOnlyPaths: []string{"/etc"},
			WritablePaths: []string{"/tmp"},
		},
	}
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	if !strings.Contains(out, "readonlyRootFilesystem") {
		t.Error("expected readonlyRootFilesystem in ECS service.hcl when filesystemPolicy is set")
	}
	if !strings.Contains(out, "readonlyRootFilesystem = true") {
		t.Error("expected readonlyRootFilesystem = true")
	}
}

// TestECSReadonlyRootFilesystemAbsent verifies readonlyRootFilesystem is NOT set without filesystemPolicy.
func TestECSReadonlyRootFilesystemAbsent(t *testing.T) {
	p := baseECSProfile()
	// No FilesystemPolicy
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	if strings.Contains(out, "readonlyRootFilesystem") {
		t.Error("expected NO readonlyRootFilesystem when filesystemPolicy is nil")
	}
}

// TestECSWritableVolumes verifies named volumes are added for writablePaths.
func TestECSWritableVolumes(t *testing.T) {
	p := baseECSProfile()
	p.Spec.Policy = profile.PolicySpec{
		FilesystemPolicy: &profile.FilesystemPolicy{
			ReadOnlyPaths: []string{"/etc"},
			WritablePaths: []string{"/tmp", "/workspace"},
		},
	}
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	// Should contain volumes section
	if !strings.Contains(out, "volumes") {
		t.Error("expected volumes section in ECS service.hcl when writablePaths are set")
	}
	// Should contain mountPoints
	if !strings.Contains(out, "mountPoints") {
		t.Error("expected mountPoints in main container when writablePaths are set")
	}
	if !strings.Contains(out, "/workspace") {
		t.Error("expected /workspace volume mount")
	}
}

// TestECSTmpAutoInjected verifies /tmp is auto-injected as writable when readonlyRootFilesystem is true.
func TestECSTmpAutoInjected(t *testing.T) {
	p := baseECSProfile()
	p.Spec.Policy = profile.PolicySpec{
		FilesystemPolicy: &profile.FilesystemPolicy{
			ReadOnlyPaths: []string{"/etc"},
			WritablePaths: []string{"/workspace"}, // /tmp NOT listed
		},
	}
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	if !strings.Contains(out, "/tmp") {
		t.Error("expected /tmp to be auto-injected as writable volume when readonlyRootFilesystem is true")
	}
}

// baseEC2Profile returns a minimal SandboxProfile for EC2 service.hcl tests.
func baseEC2Profile() *profile.SandboxProfile {
	return &profile.SandboxProfile{
		Metadata: profile.Metadata{Name: "test-ec2-profile"},
		Spec: profile.Spec{
			Runtime: profile.RuntimeSpec{
				Substrate:    "ec2",
				Spot:         true,
				InstanceType: "t3.medium",
				Region:       "us-east-1",
			},
			Budget: &profile.BudgetSpec{
				WarningThreshold: 0.8,
				Compute: &profile.ComputeBudget{
					MaxSpendUSD: 5.00,
				},
			},
		},
	}
}

func baseEC2Network() *NetworkConfig {
	return &NetworkConfig{
		VPCID:             "vpc-12345",
		PublicSubnets:     []string{"subnet-a", "subnet-b"},
		AvailabilityZones: []string{"us-east-1a", "us-east-1b"},
		RegionLabel:       "use1",
	}
}

// TestSpotRateEC2NonZero verifies that a NetworkConfig with non-zero SpotRateUSD
// produces spot_rate != 0.0 in the EC2 service.hcl output (BUDG-03).
func TestSpotRateEC2NonZero(t *testing.T) {
	p := baseEC2Profile()
	net := baseEC2Network()
	net.SpotRateUSD = 0.0416 // injected by create.go before Compile()

	iamPolicy := &IAMSessionPolicy{
		MaxSessionDuration: 3600,
		AllowedRegions:     []string{"us-east-1"},
	}

	out, err := generateEC2ServiceHCL(p, "test-sb", true, nil, iamPolicy, "", net)
	if err != nil {
		t.Fatalf("generateEC2ServiceHCL failed: %v", err)
	}

	// budget_enforcer_inputs must be present
	if !strings.Contains(out, "budget_enforcer_inputs") {
		t.Fatal("expected budget_enforcer_inputs block in EC2 service.hcl when budget is set")
	}
	// spot_rate must reflect the non-zero value
	if !strings.Contains(out, "spot_rate      = 0.0416") {
		t.Errorf("expected spot_rate = 0.0416 in EC2 service.hcl, got:\n%s", out)
	}
	// must NOT contain the hardcoded zero
	if strings.Contains(out, "spot_rate      = 0\n") || strings.Contains(out, "spot_rate      = 0.0\n") {
		t.Error("found hardcoded spot_rate = 0.0 in EC2 service.hcl — SpotRateUSD not threaded through")
	}
}

// TestSpotRateEC2ZeroFallback verifies that SpotRateUSD=0.0 (no budget or failed lookup)
// still renders correctly (backward-compatible zero value).
func TestSpotRateEC2ZeroFallback(t *testing.T) {
	p := baseEC2Profile()
	net := baseEC2Network()
	net.SpotRateUSD = 0.0 // no rate resolved

	iamPolicy := &IAMSessionPolicy{
		MaxSessionDuration: 3600,
		AllowedRegions:     []string{"us-east-1"},
	}

	out, err := generateEC2ServiceHCL(p, "test-sb", true, nil, iamPolicy, "", net)
	if err != nil {
		t.Fatalf("generateEC2ServiceHCL failed: %v", err)
	}
	if !strings.Contains(out, "budget_enforcer_inputs") {
		t.Fatal("expected budget_enforcer_inputs block in EC2 service.hcl when budget is set")
	}
	// spot_rate = 0 is acceptable (zero value renders as 0 in Go templates)
	if !strings.Contains(out, "spot_rate") {
		t.Error("expected spot_rate field in budget_enforcer_inputs")
	}
}

// TestECSServiceHCLImageURIs verifies ECR URIs are emitted when KM_ACCOUNTS_APPLICATION is set.
func TestECSServiceHCLImageURIs(t *testing.T) {
	t.Setenv("KM_ACCOUNTS_APPLICATION", "123456789012")
	t.Setenv("KM_SIDECAR_VERSION", "")

	p := baseECSProfile()
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}

	// Must contain real ECR URIs for all 4 sidecars
	for _, sidecar := range []string{"dns-proxy", "http-proxy", "audit-log", "tracing"} {
		expected := "123456789012.dkr.ecr.us-east-1.amazonaws.com/km-" + sidecar + ":"
		if !strings.Contains(out, expected) {
			t.Errorf("expected ECR URI %q in output, got:\n%s", expected, out)
		}
	}

	// Must NOT contain any ${var.*_image} literals
	for _, bad := range []string{"${var.dns_proxy_image}", "${var.http_proxy_image}", "${var.audit_log_image}", "${var.tracing_image}"} {
		if strings.Contains(out, bad) {
			t.Errorf("found broken HCL literal %q in output — should have been replaced with ECR URI", bad)
		}
	}
}

// TestECSServiceHCLImageURIsPlaceholder verifies PLACEHOLDER_ECR prefix when KM_ACCOUNTS_APPLICATION is empty.
func TestECSServiceHCLImageURIsPlaceholder(t *testing.T) {
	t.Setenv("KM_ACCOUNTS_APPLICATION", "")

	p := baseECSProfile()
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}

	// Must contain PLACEHOLDER_ECR/ prefix for each sidecar
	for _, sidecar := range []string{"dns-proxy", "http-proxy", "audit-log", "tracing"} {
		expected := "PLACEHOLDER_ECR/km-" + sidecar + ":"
		if !strings.Contains(out, expected) {
			t.Errorf("expected placeholder URI %q in output, got:\n%s", expected, out)
		}
	}

	// Must NOT contain any ${var.*_image} literals
	for _, bad := range []string{"${var.dns_proxy_image}", "${var.http_proxy_image}", "${var.audit_log_image}", "${var.tracing_image}"} {
		if strings.Contains(out, bad) {
			t.Errorf("found broken HCL literal %q in output — placeholder did not replace it", bad)
		}
	}
}

// TestECSServiceHCLImageVersion verifies KM_SIDECAR_VERSION controls the image tag.
func TestECSServiceHCLImageVersion(t *testing.T) {
	t.Setenv("KM_ACCOUNTS_APPLICATION", "123456789012")
	t.Setenv("KM_SIDECAR_VERSION", "v1.2.3")

	p := baseECSProfile()
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}

	// All sidecar images must end with :v1.2.3
	for _, sidecar := range []string{"dns-proxy", "http-proxy", "audit-log", "tracing"} {
		expected := "km-" + sidecar + ":v1.2.3"
		if !strings.Contains(out, expected) {
			t.Errorf("expected image tag :v1.2.3 for %s, got:\n%s", sidecar, out)
		}
	}

	// Must NOT contain :latest when version is explicitly set
	if strings.Contains(out, ":latest") {
		t.Error("found :latest tag in output when KM_SIDECAR_VERSION=v1.2.3 — version not applied")
	}
}

// ============================================================
// Claude Code OTEL telemetry env var injection tests for ECS (OTEL-01, OTEL-06, OTEL-07)
// ============================================================

// TestECSOTELVarsEnabledDefault verifies that when claudeTelemetry is nil (default),
// all 5 base OTEL env vars appear in the ECS main container environment block.
func TestECSOTELVarsEnabledDefault(t *testing.T) {
	p := baseECSProfile()
	// claudeTelemetry is nil — should default to enabled
	out, err := generateECSServiceHCL(p, "sb-ecs-otel-1", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	for _, want := range []string{
		`"CLAUDE_CODE_ENABLE_TELEMETRY"`,
		`"OTEL_METRICS_EXPORTER"`,
		`"OTEL_LOGS_EXPORTER"`,
		`"OTEL_EXPORTER_OTLP_PROTOCOL"`,
		`"OTEL_EXPORTER_OTLP_ENDPOINT"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in ECS container environment when claudeTelemetry is nil (default enabled)", want)
		}
	}
	if !strings.Contains(out, `"1"`) {
		t.Error("expected CLAUDE_CODE_ENABLE_TELEMETRY value \"1\" in ECS container environment")
	}
	if !strings.Contains(out, "http://localhost:4317") {
		t.Error("expected OTLP endpoint http://localhost:4317 in ECS container environment")
	}
}

// TestECSOTELLogPromptsEnabled verifies OTEL_LOG_USER_PROMPTS=1 appears in ECS container env when logPrompts=true.
func TestECSOTELLogPromptsEnabled(t *testing.T) {
	p := baseECSProfile()
	enabled := true
	p.Spec.Observability = profile.ObservabilitySpec{
		ClaudeTelemetry: &profile.ClaudeTelemetrySpec{
			Enabled:    &enabled,
			LogPrompts: true,
		},
	}
	out, err := generateECSServiceHCL(p, "sb-ecs-otel-2", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	if !strings.Contains(out, `"OTEL_LOG_USER_PROMPTS"`) {
		t.Error("expected OTEL_LOG_USER_PROMPTS in ECS container environment when logPrompts=true")
	}
}

// TestECSOTELDisabledExplicit verifies NO Claude OTEL env vars appear when enabled=false.
func TestECSOTELDisabledExplicit(t *testing.T) {
	p := baseECSProfile()
	disabled := false
	p.Spec.Observability = profile.ObservabilitySpec{
		ClaudeTelemetry: &profile.ClaudeTelemetrySpec{
			Enabled: &disabled,
		},
	}
	out, err := generateECSServiceHCL(p, "sb-ecs-otel-3", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
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
			t.Errorf("expected %q to be absent in ECS container env when claudeTelemetry.enabled=false", absent)
		}
	}
}

// TestECSOTELResourceAttributes verifies OTEL_RESOURCE_ATTRIBUTES contains sandbox metadata with substrate=ecs.
func TestECSOTELResourceAttributes(t *testing.T) {
	p := baseECSProfile()
	// claudeTelemetry nil = default enabled
	out, err := generateECSServiceHCL(p, "sb-ecs-otel-4", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	if !strings.Contains(out, "OTEL_RESOURCE_ATTRIBUTES") {
		t.Error("expected OTEL_RESOURCE_ATTRIBUTES in ECS container environment")
	}
	if !strings.Contains(out, "sandbox_id=sb-ecs-otel-4") {
		t.Error("expected sandbox_id=sb-ecs-otel-4 in OTEL_RESOURCE_ATTRIBUTES")
	}
	if !strings.Contains(out, "profile_name=test-ecs-profile") {
		t.Error("expected profile_name=test-ecs-profile in OTEL_RESOURCE_ATTRIBUTES")
	}
	if !strings.Contains(out, "substrate=ecs") {
		t.Error("expected substrate=ecs in OTEL_RESOURCE_ATTRIBUTES")
	}
}

// TestECSNOProxyIncludesLocalhost verifies NO_PROXY already includes localhost for OTEL-07.
// OTEL-07: Claude Code OTLP exports to localhost:4317/4318 bypass the HTTP proxy via
// NO_PROXY (includes localhost,127.0.0.1). No code change needed — existing config satisfies it.
func TestECSNOProxyIncludesLocalhost(t *testing.T) {
	p := baseECSProfile()
	out, err := generateECSServiceHCL(p, "sb-ecs-otel-5", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	if !strings.Contains(out, "NO_PROXY") {
		t.Error("expected NO_PROXY in ECS container environment")
	}
	if !strings.Contains(out, "localhost") {
		t.Error("expected 'localhost' in NO_PROXY value (OTEL-07: OTLP traffic bypasses HTTP proxy)")
	}
}

// TestSpotRateECSNonZero verifies that a NetworkConfig with non-zero SpotRateUSD
// produces spot_rate != 0.0 in the ECS service.hcl output (BUDG-03).
func TestSpotRateECSNonZero(t *testing.T) {
	p := baseECSProfile()
	p.Spec.Budget = &profile.BudgetSpec{
		WarningThreshold: 0.8,
		Compute: &profile.ComputeBudget{
			MaxSpendUSD: 5.00,
		},
	}
	net := baseECSNetwork()
	net.SpotRateUSD = 0.0312

	out, err := generateECSServiceHCL(p, "test-sb", false, nil, net)
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}

	if !strings.Contains(out, "budget_enforcer_inputs") {
		t.Fatal("expected budget_enforcer_inputs block in ECS service.hcl when budget is set")
	}
	if !strings.Contains(out, "spot_rate     = 0.0312") {
		t.Errorf("expected spot_rate = 0.0312 in ECS service.hcl, got:\n%s", out)
	}
}

// ============================================================
// GitHub repo filter env var injection tests (NETW-08)
// ============================================================

// TestECSServiceHCLGitHubAllowedRepos verifies that a profile with GitHub repos produces
// a KM_GITHUB_ALLOWED_REPOS entry in the km-http-proxy container environment block.
func TestECSServiceHCLGitHubAllowedRepos(t *testing.T) {
	p := baseECSProfile()
	p.Spec.SourceAccess = profile.SourceAccessSpec{
		GitHub: &profile.GitHubAccess{
			AllowedRepos: []string{"myorg/myrepo", "other/repo"},
		},
	}
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	if !strings.Contains(out, "KM_GITHUB_ALLOWED_REPOS") {
		t.Fatal("expected KM_GITHUB_ALLOWED_REPOS in ECS service.hcl km-http-proxy environment")
	}
	if !strings.Contains(out, "myorg/myrepo,other/repo") {
		t.Errorf("expected CSV value myorg/myrepo,other/repo in ECS service.hcl, got snippet:\n%s",
			extractECSLines(out, "KM_GITHUB_ALLOWED_REPOS"))
	}
}

// TestECSServiceHCLGitHubAllowedReposEmpty verifies that a profile without GitHub config
// produces an empty KM_GITHUB_ALLOWED_REPOS value (backward compatible).
func TestECSServiceHCLGitHubAllowedReposEmpty(t *testing.T) {
	p := baseECSProfile()
	// No GitHub config.
	out, err := generateECSServiceHCL(p, "test-sb", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	// The env var should be present but empty, or absent — must NOT contain a non-empty repo value.
	if strings.Contains(out, "KM_GITHUB_ALLOWED_REPOS") && strings.Contains(out, "myorg") {
		t.Error("expected no non-empty KM_GITHUB_ALLOWED_REPOS when GitHub config is absent")
	}
}

// extractECSLines returns lines from s that contain substr (for error context).
func extractECSLines(s, substr string) string {
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
