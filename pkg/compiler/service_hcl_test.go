package compiler

import (
	"fmt"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
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

	out, err := generateEC2ServiceHCL(p, "test-sb", true, nil, iamPolicy, "", net, nil)
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

	out, err := generateEC2ServiceHCL(p, "test-sb", true, nil, iamPolicy, "", net, nil)
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

// ============================================================
// Phase 36 Plan 02: km-sandbox image URI and KM_* entrypoint env vars
// ============================================================

// TestECSServiceHCLSandboxImage verifies the main container uses the real km-sandbox ECR URI,
// NOT the MAIN_IMAGE_PLACEHOLDER that existed before Phase 36.
func TestECSServiceHCLSandboxImage(t *testing.T) {
	t.Setenv("KM_ACCOUNTS_APPLICATION", "123456789012")
	t.Setenv("KM_SIDECAR_VERSION", "v1.0.0")

	p := baseECSProfile()
	out, err := generateECSServiceHCL(p, "sb-test123", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}

	// Must NOT contain the old placeholder
	if strings.Contains(out, "MAIN_IMAGE_PLACEHOLDER") {
		t.Error("expected MAIN_IMAGE_PLACEHOLDER to be replaced with real km-sandbox ECR URI")
	}

	// Must contain the km-sandbox image name prefix
	if !strings.Contains(out, "km-sandbox:") {
		t.Errorf("expected km-sandbox: image reference in main container, got:\n%s", out)
	}

	// Must contain the full ECR URI pattern
	expected := "123456789012.dkr.ecr.us-east-1.amazonaws.com/km-sandbox:v1.0.0"
	if !strings.Contains(out, expected) {
		t.Errorf("expected full ECR URI %q in main container image, got:\n%s", expected, out)
	}
}

// TestECSMainContainerEntrypointEnvVars verifies that the core KM_* env vars are always present
// in the main container environment block (required by entrypoint.sh in Phase 36).
func TestECSMainContainerEntrypointEnvVars(t *testing.T) {
	t.Setenv("KM_ACCOUNTS_APPLICATION", "123456789012")
	t.Setenv("KM_ARTIFACTS_BUCKET", "km-sandbox-artifacts-ea554771")

	p := baseECSProfile()
	out, err := generateECSServiceHCL(p, "sb-test456", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}

	for _, want := range []string{
		"KM_SANDBOX_ID",
		"KM_ARTIFACTS_BUCKET",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in main container environment block, got:\n%s", want, out)
		}
	}

	// KM_PROXY_CA_CERT_S3 should be present when artifacts bucket is set
	if !strings.Contains(out, "KM_PROXY_CA_CERT_S3") {
		t.Errorf("expected KM_PROXY_CA_CERT_S3 in main container environment when KM_ARTIFACTS_BUCKET is set")
	}
}

// TestECSMainContainerInitCommands verifies that when a profile has initCommands,
// KM_INIT_COMMANDS is present in the main container environment with a base64-encoded value.
func TestECSMainContainerInitCommands(t *testing.T) {
	t.Setenv("KM_ACCOUNTS_APPLICATION", "")

	p := baseECSProfile()
	p.Spec.Execution = profile.ExecutionSpec{
		InitCommands: []string{"apt install foo", "pip install bar"},
	}

	out, err := generateECSServiceHCL(p, "sb-init-test", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}

	if !strings.Contains(out, "KM_INIT_COMMANDS") {
		t.Error("expected KM_INIT_COMMANDS in main container environment when initCommands are set")
	}

	// Value must be a non-empty base64 string (not the raw JSON)
	if strings.Contains(out, `["apt install foo"`) {
		t.Error("KM_INIT_COMMANDS value should be base64-encoded, not raw JSON")
	}
}

// TestECSMainContainerGitHubEnvVars verifies that when profile has GitHub sourceAccess configured,
// KM_GITHUB_TOKEN_SSM and KM_GITHUB_ALLOWED_REFS are present in the main container environment.
func TestECSMainContainerGitHubEnvVars(t *testing.T) {
	t.Setenv("KM_ACCOUNTS_APPLICATION", "")

	p := baseECSProfile()
	p.Spec.SourceAccess = profile.SourceAccessSpec{
		GitHub: &profile.GitHubAccess{
			AllowedRepos: []string{"myorg/myrepo"},
			AllowedRefs:  []string{"refs/heads/main", "refs/tags/v*"},
		},
	}

	out, err := generateECSServiceHCL(p, "sb-gh-test", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}

	if !strings.Contains(out, "KM_GITHUB_TOKEN_SSM") {
		t.Error("expected KM_GITHUB_TOKEN_SSM in main container environment when GitHub is configured")
	}
	if !strings.Contains(out, "KM_GITHUB_ALLOWED_REFS") {
		t.Error("expected KM_GITHUB_ALLOWED_REFS in main container environment when GitHub AllowedRefs are set")
	}
}

// TestECSMainContainerSecretPaths verifies that when profile has AllowedSecretPaths,
// KM_SECRET_PATHS is present in the main container environment.
func TestECSMainContainerSecretPaths(t *testing.T) {
	t.Setenv("KM_ACCOUNTS_APPLICATION", "")

	p := baseECSProfile()
	p.Spec.IAM = profile.IAMSpec{
		AllowedSecretPaths: []string{"/sandbox/sb-secret-test/db-pass", "/sandbox/sb-secret-test/api-key"},
	}

	out, err := generateECSServiceHCL(p, "sb-secret-test", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}

	if !strings.Contains(out, "KM_SECRET_PATHS") {
		t.Error("expected KM_SECRET_PATHS in main container environment when AllowedSecretPaths are set")
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

// ============================================================
// Phase 87 Wave 0: RED-state stubs for HCL rendering (SNAP-04, SNAP-05)
// Wave 2 plan-04 will implement these.
// ============================================================

// TestAdditionalSnapshotsHCLRender verifies that generateEC2ServiceHCL renders the
// additional_snapshots = [...] HCL block correctly for all Phase 87 cases.
// Phase 87 Wave 2 plan-04 — flipped from RED to GREEN.
func TestAdditionalSnapshotsHCLRender(t *testing.T) {
	boolTrue := true
	boolFalse := false

	// Case 1: zero_entries — profile without additionalSnapshots emits additional_snapshots = []
	t.Run("zero_entries", func(t *testing.T) {
		p := minimalEC2StorageProfile()
		hcl, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork(), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(hcl, "additional_snapshots = []") {
			t.Errorf("expected 'additional_snapshots = []' in HCL output\ngot:\n%s", hcl)
		}
	})

	// Case 2: one_entry_minimal — auto device, encrypted omitted (nil→null), size 0 (inherit)
	t.Run("one_entry_minimal", func(t *testing.T) {
		p := minimalEC2StorageProfile()
		p.Spec.Runtime.AdditionalSnapshots = []profile.AdditionalSnapshotSpec{
			{SnapshotID: "snap-0123abcdef0123456", MountPoint: "/opt/models"},
		}
		hcl, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork(), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(hcl, `snapshot_id = "snap-0123abcdef0123456"`) {
			t.Errorf("missing snapshot_id in HCL\ngot:\n%s", hcl)
		}
		if !strings.Contains(hcl, `device_name = "/dev/sdf"`) {
			t.Errorf("expected auto-picked /dev/sdf\ngot:\n%s", hcl)
		}
		if !strings.Contains(hcl, "encrypted   = null") {
			t.Errorf("expected encrypted = null (nil *bool)\ngot:\n%s", hcl)
		}
		if !strings.Contains(hcl, "size_gb     = 0") {
			t.Errorf("expected size_gb = 0 (inherit)\ngot:\n%s", hcl)
		}
	})

	// Case 3: one_entry_full — explicit device, encrypted:true, size:200
	t.Run("one_entry_full", func(t *testing.T) {
		p := minimalEC2StorageProfile()
		p.Spec.Runtime.AdditionalSnapshots = []profile.AdditionalSnapshotSpec{
			{SnapshotID: "snap-aabbccdd11223344", Device: "/dev/sdh", MountPoint: "/data", Encrypted: &boolTrue, Size: 200},
		}
		hcl, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork(), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(hcl, `device_name = "/dev/sdh"`) {
			t.Errorf("expected explicit /dev/sdh\ngot:\n%s", hcl)
		}
		if !strings.Contains(hcl, "encrypted   = true") {
			t.Errorf("expected encrypted = true\ngot:\n%s", hcl)
		}
		if !strings.Contains(hcl, "size_gb     = 200") {
			t.Errorf("expected size_gb = 200\ngot:\n%s", hcl)
		}
	})

	// Case 4: three_entries_order — mix of explicit/auto devices, declaration order preserved
	t.Run("three_entries_order", func(t *testing.T) {
		p := minimalEC2StorageProfile()
		p.Spec.Runtime.AdditionalSnapshots = []profile.AdditionalSnapshotSpec{
			{SnapshotID: "snap-00000000000000001", Device: "/dev/sdh", MountPoint: "/mnt1", Encrypted: &boolFalse, Size: 50},
			{SnapshotID: "snap-00000000000000002", MountPoint: "/mnt2"}, // auto → /dev/sdf (no additionalVolume, sdf free)
			{SnapshotID: "snap-00000000000000003", MountPoint: "/mnt3"}, // auto → /dev/sdg (sdf claimed by entry 1)
		}
		hcl, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork(), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Verify all 3 snapshot IDs appear
		for _, snapID := range []string{"snap-00000000000000001", "snap-00000000000000002", "snap-00000000000000003"} {
			if !strings.Contains(hcl, snapID) {
				t.Errorf("missing %s in HCL\ngot:\n%s", snapID, hcl)
			}
		}
		// Entry 0 is pinned /dev/sdh
		if !strings.Contains(hcl, `device_name = "/dev/sdh"`) {
			t.Errorf("expected /dev/sdh for entry 0\ngot:\n%s", hcl)
		}
		// Entry 1 auto → /dev/sdf (first available, no additionalVolume)
		if !strings.Contains(hcl, `device_name = "/dev/sdf"`) {
			t.Errorf("expected /dev/sdf for entry 1\ngot:\n%s", hcl)
		}
		// Entry 2 auto → /dev/sdg (sdf claimed)
		if !strings.Contains(hcl, `device_name = "/dev/sdg"`) {
			t.Errorf("expected /dev/sdg for entry 2\ngot:\n%s", hcl)
		}
		// encrypted = false for entry 0
		if !strings.Contains(hcl, "encrypted   = false") {
			t.Errorf("expected encrypted = false\ngot:\n%s", hcl)
		}
	})

	// Case 5: with_additional_volume_too — additionalVolume auto-picks /dev/sdf, 2 snaps land on /dev/sdg, /dev/sdh
	t.Run("with_additional_volume_too", func(t *testing.T) {
		p := minimalEC2StorageProfile()
		p.Spec.Runtime.AdditionalVolume = &profile.AdditionalVolumeSpec{
			Size:       100,
			MountPoint: "/data",
		}
		p.Spec.Runtime.AdditionalSnapshots = []profile.AdditionalSnapshotSpec{
			{SnapshotID: "snap-vol1vol1vol1vol10", MountPoint: "/mnt1"},
			{SnapshotID: "snap-vol2vol2vol2vol20", MountPoint: "/mnt2"},
		}
		hcl, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork(), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// additionalVolume picks /dev/sdf (no AMI BDM)
		if !strings.Contains(hcl, `additional_volume_device_name = "/dev/sdf"`) {
			t.Errorf("expected additionalVolume on /dev/sdf\ngot:\n%s", hcl)
		}
		// snapshot 0 → /dev/sdg (/dev/sdf claimed by additionalVolume)
		if !strings.Contains(hcl, `device_name = "/dev/sdg"`) {
			t.Errorf("expected snapshot 0 on /dev/sdg\ngot:\n%s", hcl)
		}
		// snapshot 1 → /dev/sdh
		if !strings.Contains(hcl, `device_name = "/dev/sdh"`) {
			t.Errorf("expected snapshot 1 on /dev/sdh\ngot:\n%s", hcl)
		}
	})

	// Case 6: with_ami_bdm — AMI BDM has /dev/sdf, additionalVolume → /dev/sdg, snap → /dev/sdh
	t.Run("with_ami_bdm", func(t *testing.T) {
		p := minimalEC2StorageProfile()
		p.Spec.Runtime.AdditionalVolume = &profile.AdditionalVolumeSpec{
			Size:       50,
			MountPoint: "/data",
		}
		p.Spec.Runtime.AdditionalSnapshots = []profile.AdditionalSnapshotSpec{
			{SnapshotID: "snap-bdmbdmbdmbdmbdm01", MountPoint: "/mnt1"},
		}
		amiBDM := []string{"/dev/sdf"}
		hcl, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork(), amiBDM)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// AMI BDM blocks sdf; additionalVolume → /dev/sdg
		if !strings.Contains(hcl, `additional_volume_device_name = "/dev/sdg"`) {
			t.Errorf("expected additionalVolume on /dev/sdg (sdf blocked by AMI BDM)\ngot:\n%s", hcl)
		}
		// snapshot → /dev/sdh (/dev/sdg claimed)
		if !strings.Contains(hcl, `device_name = "/dev/sdh"`) {
			t.Errorf("expected snapshot on /dev/sdh\ngot:\n%s", hcl)
		}
	})

	// Case 7: pool_exhaustion — 12 snapshots with no AMI BDM → error at index 11 (the 12th, 0-indexed)
	t.Run("pool_exhaustion", func(t *testing.T) {
		p := minimalEC2StorageProfile()
		snaps := make([]profile.AdditionalSnapshotSpec, 12)
		for i := range snaps {
			snaps[i] = profile.AdditionalSnapshotSpec{
				SnapshotID: fmt.Sprintf("snap-%017d", i),
				MountPoint: fmt.Sprintf("/mnt/%d", i),
			}
		}
		p.Spec.Runtime.AdditionalSnapshots = snaps
		_, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork(), nil)
		if err == nil {
			t.Fatal("expected pool exhaustion error for 12 snapshots, got nil")
		}
		// Error must name the offending entry index (11 = the 12th entry, 0-indexed)
		if !strings.Contains(err.Error(), "11") {
			t.Errorf("expected error to name offending entry index 11, got: %v", err)
		}
		if !strings.Contains(err.Error(), "additionalSnapshots") {
			t.Errorf("expected error to mention additionalSnapshots, got: %v", err)
		}
	})
}

func TestBoolPtrHCLTemplateFunc(t *testing.T) {
	fn, ok := templateFuncs["boolPtrHCL"].(func(*bool) string)
	if !ok {
		t.Fatal("boolPtrHCL not registered or wrong signature")
	}
	if got := fn(nil); got != "null" {
		t.Errorf("nil case: got %q, want \"null\"", got)
	}
	tru := true
	if got := fn(&tru); got != "true" {
		t.Errorf("true case: got %q, want \"true\"", got)
	}
	fal := false
	if got := fn(&fal); got != "false" {
		t.Errorf("false case: got %q, want \"false\"", got)
	}
}

// ============================================================
// Phase 94-05: ResourcePrefix log-group path substitution (ECS)
// ============================================================

// TestECSCWLogGroupResourcePrefix verifies that the ECS audit-log sidecar's
// CW_LOG_GROUP and awslogs-group render with the dynamic {prefix} from
// KM_RESOURCE_PREFIX, not the hardcoded /km/ path. Tests both "kph" (non-default)
// and "km" (default, for byte-identity proof).
func TestECSCWLogGroupResourcePrefix(t *testing.T) {
	cases := []struct {
		prefix          string
		sandboxID       string
		wantCWLogGroup  string
		wantAwslogsGrp  string
	}{
		{
			prefix:         "kph",
			sandboxID:      "sb-kph-001",
			wantCWLogGroup: "/kph/sandboxes/sb-kph-001/",
			wantAwslogsGrp: "/kph/sidecars/sb-kph-001",
		},
		{
			prefix:         "km",
			sandboxID:      "sb-km-001",
			wantCWLogGroup: "/km/sandboxes/sb-km-001/",
			wantAwslogsGrp: "/km/sidecars/sb-km-001",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run("prefix="+tc.prefix, func(t *testing.T) {
			t.Setenv("KM_RESOURCE_PREFIX", tc.prefix)

			p := baseECSProfile()
			out, err := generateECSServiceHCL(p, tc.sandboxID, false, nil, baseECSNetwork())
			if err != nil {
				t.Fatalf("generateECSServiceHCL failed: %v", err)
			}

			if !strings.Contains(out, tc.wantCWLogGroup) {
				t.Errorf("CW_LOG_GROUP: want %q in ECS service.hcl, not found.\nOutput snippet:\n%s",
					tc.wantCWLogGroup, extractECSLines(out, "CW_LOG_GROUP"))
			}
			if !strings.Contains(out, tc.wantAwslogsGrp) {
				t.Errorf("awslogs-group: want %q in ECS service.hcl, not found.\nOutput snippet:\n%s",
					tc.wantAwslogsGrp, extractECSLines(out, "awslogs-group"))
			}

			// Guard: hardcoded /km/ paths must NOT appear when prefix != "km".
			if tc.prefix != "km" {
				if strings.Contains(out, `"/km/sandboxes/`) || strings.Contains(out, `"/km/sidecars/`) {
					t.Errorf("found hardcoded /km/ path in output for prefix=%q (should use /%s/)", tc.prefix, tc.prefix)
				}
			}
		})
	}
}
