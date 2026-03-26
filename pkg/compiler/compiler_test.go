package compiler_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/compiler"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// testNetwork returns a test NetworkConfig for use in compiler tests.
func testNetwork() *compiler.NetworkConfig {
	return &compiler.NetworkConfig{
		VPCID:             "vpc-test123",
		PublicSubnets:     []string{"subnet-pub1", "subnet-pub2"},
		AvailabilityZones: []string{"us-east-1a", "us-east-1b"},
	}
}

// loadTestProfile reads and parses a testdata profile YAML file.
func loadTestProfile(t *testing.T, filename string) *profile.SandboxProfile {
	t.Helper()
	data, err := os.ReadFile("testdata/" + filename)
	if err != nil {
		t.Fatalf("loadTestProfile(%q): read file: %v", filename, err)
	}
	p, err := profile.Parse(data)
	if err != nil {
		t.Fatalf("loadTestProfile(%q): parse: %v", filename, err)
	}
	return p
}

// ============================================================
// Task 1: EC2 substrate tests
// ============================================================

func TestGenerateSandboxID(t *testing.T) {
	pattern := regexp.MustCompile(`^sb-[a-f0-9]{8}$`)

	id1 := compiler.GenerateSandboxID()
	if !pattern.MatchString(id1) {
		t.Errorf("GenerateSandboxID() = %q; want sb-[a-f0-9]{8}", id1)
	}

	id2 := compiler.GenerateSandboxID()
	if !pattern.MatchString(id2) {
		t.Errorf("GenerateSandboxID() = %q; want sb-[a-f0-9]{8}", id2)
	}

	if id1 == id2 {
		t.Errorf("GenerateSandboxID() returned the same ID twice: %q", id1)
	}
}

func TestCompileEC2(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	id := compiler.GenerateSandboxID()

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if artifacts.ServiceHCL == "" {
		t.Error("Compile() ServiceHCL is empty")
	}
	if artifacts.UserData == "" {
		t.Error("Compile() UserData is empty for EC2 substrate")
	}
	if !strings.Contains(artifacts.ServiceHCL, `substrate_module = "ec2spot"`) {
		t.Errorf("Compile() ServiceHCL missing substrate_module = \"ec2spot\"\nGot:\n%s", artifacts.ServiceHCL)
	}
	if artifacts.SandboxID != id {
		t.Errorf("Compile() SandboxID = %q; want %q", artifacts.SandboxID, id)
	}
}

func TestCompileEC2ServiceHCL(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	id := "sb-testec2a"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	hcl := artifacts.ServiceHCL

	checks := []string{
		id,
		`substrate_module = "ec2spot"`,
		"ec2spots",
		"vpc_id",
		"public_subnets",
		"availability_zones",
		"module_inputs",
	}
	for _, want := range checks {
		if !strings.Contains(hcl, want) {
			t.Errorf("ServiceHCL missing %q\nGot:\n%s", want, hcl)
		}
	}
}

func TestCompileEC2UserData(t *testing.T) {
	p := loadTestProfile(t, "ec2-with-secrets.yaml")
	id := "sb-testud01"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	ud := artifacts.UserData

	checks := []string{
		"amazon-ssm-agent",
		"IMDSv2",
		"aws ssm get-parameter",
		"/km/sandboxes/my-sandbox/api-key",
		"/km/sandboxes/my-sandbox/db-password",
	}
	for _, want := range checks {
		if !strings.Contains(ud, want) {
			t.Errorf("UserData missing %q\nGot:\n%s", want, ud)
		}
	}
}

func TestCompileEC2Spot(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	id := "sb-spottest"

	// spot=true (from profile) and onDemand=false -> should have spot config
	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(spot=true, onDemand=false) error = %v", err)
	}
	if !strings.Contains(artifacts.ServiceHCL, "spot_price_multiplier") {
		t.Errorf("spot=true profile should include spot_price_multiplier in ServiceHCL\nGot:\n%s", artifacts.ServiceHCL)
	}

	// onDemand=true override: should NOT have spot config
	artifactsOD, err := compiler.Compile(p, id, true, testNetwork())
	if err != nil {
		t.Fatalf("Compile(spot=true, onDemand=true) error = %v", err)
	}
	if strings.Contains(artifactsOD.ServiceHCL, "spot_price_multiplier") {
		t.Errorf("onDemand=true override should remove spot_price_multiplier from ServiceHCL\nGot:\n%s", artifactsOD.ServiceHCL)
	}
}

func TestCompileTagging(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	id := "sb-tagtest1"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// module_inputs should include sandbox_id so Terraform resources get tagged
	hcl := artifacts.ServiceHCL
	// sandbox_id should appear at least twice: once in locals and once in module_inputs
	count := strings.Count(hcl, id)
	if count < 2 {
		t.Errorf("ServiceHCL should contain sandbox_id at least twice (locals + module_inputs), got %d occurrences\nGot:\n%s", count, hcl)
	}
}

func TestCompileSGEgressRulesInServiceHCL(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	id := "sb-sgrules1"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	hcl := artifacts.ServiceHCL

	// sg_egress_rules must appear in service.hcl so Terraform can pick them up
	if !strings.Contains(hcl, "sg_egress_rules") {
		t.Errorf("ServiceHCL missing sg_egress_rules\nGot:\n%s", hcl)
	}
	// Baseline rules: HTTPS (port 443) and DNS (port 53) must be present
	if !strings.Contains(hcl, "443") {
		t.Errorf("ServiceHCL sg_egress_rules missing port 443 (HTTPS)\nGot:\n%s", hcl)
	}
	if !strings.Contains(hcl, "53") {
		t.Errorf("ServiceHCL sg_egress_rules missing port 53 (DNS)\nGot:\n%s", hcl)
	}

	// Also verify Go struct fields
	if len(artifacts.SGEgressRules) < 2 {
		t.Errorf("SGEgressRules should have at least 2 entries (HTTPS+DNS), got %d", len(artifacts.SGEgressRules))
	}
}

func TestCompileIAMPolicyInServiceHCL(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	id := "sb-iampol01"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	hcl := artifacts.ServiceHCL

	// iam_session_policy must appear in service.hcl
	if !strings.Contains(hcl, "iam_session_policy") {
		t.Errorf("ServiceHCL missing iam_session_policy\nGot:\n%s", hcl)
	}
	if !strings.Contains(hcl, "max_session_duration") {
		t.Errorf("ServiceHCL iam_session_policy missing max_session_duration\nGot:\n%s", hcl)
	}

	// Verify Go struct
	if artifacts.IAMPolicy == nil {
		t.Fatal("IAMPolicy should not be nil")
	}
	if artifacts.IAMPolicy.MaxSessionDuration <= 0 {
		t.Errorf("IAMPolicy.MaxSessionDuration should be > 0, got %d", artifacts.IAMPolicy.MaxSessionDuration)
	}
}

func TestCompileSecretsInjection(t *testing.T) {
	p := loadTestProfile(t, "ec2-with-secrets.yaml")
	id := "sb-sectest1"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// Profile has allowedSecretPaths — they must appear in SecretPaths
	if len(artifacts.SecretPaths) < 2 {
		t.Errorf("SecretPaths should have at least 2 entries, got %d: %v", len(artifacts.SecretPaths), artifacts.SecretPaths)
	}
	for _, path := range []string{"/km/sandboxes/my-sandbox/api-key", "/km/sandboxes/my-sandbox/db-password"} {
		found := false
		for _, sp := range artifacts.SecretPaths {
			if sp == path {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("SecretPaths missing %q; got %v", path, artifacts.SecretPaths)
		}
	}

	// Empty allowlist
	pEmpty := loadTestProfile(t, "ec2-basic.yaml")
	artifactsEmpty, err := compiler.Compile(pEmpty, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(empty secrets) error = %v", err)
	}
	// ec2-basic.yaml has no github config and no allowedSecretPaths -> SecretPaths should be empty
	if len(artifactsEmpty.SecretPaths) != 0 {
		t.Errorf("SecretPaths should be empty for profile with no allowedSecretPaths (and no github), got %v", artifactsEmpty.SecretPaths)
	}
}

// TestCompileGitHubToken verifies the new GIT_ASKPASS behavior:
// - SecretPaths does NOT include /km/github/app-token (removed from security.go)
// - UserData contains km-git-askpass credential helper script
// - UserData does NOT contain "export GITHUB_TOKEN"
func TestCompileGitHubToken(t *testing.T) {
	p := loadTestProfile(t, "ec2-with-secrets.yaml")
	id := "sb-ghtest01"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// security.go should NO LONGER inject /km/github/app-token into SecretPaths
	for _, sp := range artifacts.SecretPaths {
		if sp == "/km/github/app-token" {
			t.Errorf("SecretPaths should NOT contain /km/github/app-token (GIT_ASKPASS reads SSM at git time); got %v", artifacts.SecretPaths)
		}
	}

	// UserData should inject GIT_ASKPASS credential helper, not GITHUB_TOKEN env var
	if !strings.Contains(artifacts.UserData, "km-git-askpass") {
		t.Errorf("UserData should contain km-git-askpass script for profile with github sourceAccess\nGot:\n%s", artifacts.UserData)
	}
	if !strings.Contains(artifacts.UserData, "GIT_ASKPASS") {
		t.Errorf("UserData should export GIT_ASKPASS for profile with github sourceAccess\nGot:\n%s", artifacts.UserData)
	}

	// Profile without github -> no GIT_ASKPASS injection
	pNoGH := loadTestProfile(t, "ec2-basic.yaml")
	artifactsNoGH, err := compiler.Compile(pNoGH, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(no github) error = %v", err)
	}
	for _, sp := range artifactsNoGH.SecretPaths {
		if sp == "/km/github/app-token" {
			t.Errorf("SecretPaths should NOT contain /km/github/app-token for profile without github; got %v", artifactsNoGH.SecretPaths)
		}
	}
}

// TestGitHubUserDataGITASKPASS verifies that the GIT_ASKPASS credential helper in
// userdata.go reads the sandbox-scoped SSM path and implements Username/Password prompts.
func TestGitHubUserDataGITASKPASS(t *testing.T) {
	p := loadTestProfile(t, "ec2-with-secrets.yaml")
	id := "sb-ghaskps1"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	ud := artifacts.UserData

	// km-git-askpass script must read from /sandbox/${SANDBOX_ID}/github-token
	if !strings.Contains(ud, "/sandbox/${SANDBOX_ID}/github-token") {
		t.Errorf("GIT_ASKPASS script should read from /sandbox/$${SANDBOX_ID}/github-token\nGot:\n%s", ud)
	}
	// Script must handle Username prompt (echo x-access-token)
	if !strings.Contains(ud, "x-access-token") {
		t.Errorf("GIT_ASKPASS script should echo x-access-token for Username prompts\nGot:\n%s", ud)
	}
	// Script must handle Password prompt
	if !strings.Contains(ud, "Password") {
		t.Errorf("GIT_ASKPASS script should handle Password prompts\nGot:\n%s", ud)
	}
	// SANDBOX_ID must be exported before the script uses it
	if !strings.Contains(ud, "SANDBOX_ID") {
		t.Errorf("UserData should set SANDBOX_ID for use in GIT_ASKPASS script\nGot:\n%s", ud)
	}
}

// TestGitHubUserDataNoGITHUBTOKENExport verifies that the old GITHUB_TOKEN export
// pattern is completely absent from userdata when GitHub is configured.
func TestGitHubUserDataNoGITHUBTOKENExport(t *testing.T) {
	p := loadTestProfile(t, "ec2-with-secrets.yaml")
	id := "sb-noghenv1"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if strings.Contains(artifacts.UserData, "export GITHUB_TOKEN") {
		t.Errorf("UserData should NOT contain 'export GITHUB_TOKEN' (replaced by GIT_ASKPASS)\nGot:\n%s", artifacts.UserData)
	}
}

// TestCompileSecretsNoGitHubAppToken verifies that compileSecrets does not inject
// /km/github/app-token even when sourceAccess.github is configured.
func TestCompileSecretsNoGitHubAppToken(t *testing.T) {
	p := loadTestProfile(t, "ec2-with-secrets.yaml")
	id := "sb-nosecgh1"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// /km/github/app-token must NOT appear — token is per-sandbox at /sandbox/{id}/github-token
	for _, sp := range artifacts.SecretPaths {
		if sp == "/km/github/app-token" {
			t.Errorf("compileSecrets should not inject /km/github/app-token; got %v", artifacts.SecretPaths)
		}
	}

	// Profile-defined secret paths should still be present
	defined := []string{"/km/sandboxes/my-sandbox/api-key", "/km/sandboxes/my-sandbox/db-password"}
	for _, path := range defined {
		found := false
		for _, sp := range artifacts.SecretPaths {
			if sp == path {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("SecretPaths missing profile-defined path %q; got %v", path, artifacts.SecretPaths)
		}
	}
}

// TestCompileSecretsNoGitHubUnchanged verifies that a profile without github
// still produces its expected SecretPaths with no github-related entries.
func TestCompileSecretsNoGitHubUnchanged(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	id := "sb-noghsec1"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(no github) error = %v", err)
	}

	// ec2-basic.yaml has no allowedSecretPaths and no github -> SecretPaths empty
	if len(artifacts.SecretPaths) != 0 {
		t.Errorf("SecretPaths should be empty for profile with no allowedSecretPaths, got %v", artifacts.SecretPaths)
	}
}

// ============================================================
// Task 2: ECS substrate tests
// ============================================================

func TestCompileECS(t *testing.T) {
	p := loadTestProfile(t, "ecs-basic.yaml")
	id := "sb-ecstest1"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(ECS) error = %v", err)
	}

	if artifacts.ServiceHCL == "" {
		t.Error("Compile(ECS) ServiceHCL is empty")
	}
	if artifacts.UserData != "" {
		t.Errorf("Compile(ECS) UserData should be empty for ECS substrate, got %q", artifacts.UserData)
	}
	if !strings.Contains(artifacts.ServiceHCL, `substrate_module = "ecs"`) {
		t.Errorf("Compile(ECS) ServiceHCL missing substrate_module = \"ecs-cluster\"\nGot:\n%s", artifacts.ServiceHCL)
	}
}

func TestCompileECSContainers(t *testing.T) {
	p := loadTestProfile(t, "ecs-basic.yaml")
	id := "sb-ecscont1"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(ECS) error = %v", err)
	}

	hcl := artifacts.ServiceHCL

	// Must have 5 containers: main + dns-proxy + http-proxy + audit-log + tracing
	containers := []string{"main", "dns-proxy", "http-proxy", "audit-log", "tracing"}
	for _, c := range containers {
		if !strings.Contains(hcl, c) {
			t.Errorf("ECS ServiceHCL missing container %q\nGot:\n%s", c, hcl)
		}
	}
}

func TestCompileECSTaskConfig(t *testing.T) {
	p := loadTestProfile(t, "ecs-basic.yaml")
	id := "sb-ecstask1"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(ECS) error = %v", err)
	}

	hcl := artifacts.ServiceHCL

	// Should have task_cpu and task_memory
	if !strings.Contains(hcl, "task_cpu") {
		t.Errorf("ECS ServiceHCL missing task_cpu\nGot:\n%s", hcl)
	}
	if !strings.Contains(hcl, "task_memory") {
		t.Errorf("ECS ServiceHCL missing task_memory\nGot:\n%s", hcl)
	}
}

func TestCompileECSFargateSpot(t *testing.T) {
	p := loadTestProfile(t, "ecs-basic.yaml") // spot: true
	id := "sb-ecsspot1"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(ECS, spot=true) error = %v", err)
	}
	if !strings.Contains(artifacts.ServiceHCL, "use_spot       = true") {
		t.Errorf("ECS spot=true ServiceHCL should contain use_spot = true\nGot:\n%s", artifacts.ServiceHCL)
	}

	// spot=false
	pNoSpot := loadTestProfile(t, "ecs-basic.yaml")
	pNoSpot.Spec.Runtime.Spot = false
	artifactsNoSpot, err := compiler.Compile(pNoSpot, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(ECS, spot=false) error = %v", err)
	}
	if !strings.Contains(artifactsNoSpot.ServiceHCL, "use_spot       = false") {
		t.Errorf("ECS spot=false ServiceHCL should contain use_spot = false\nGot:\n%s", artifactsNoSpot.ServiceHCL)
	}
}

func TestCompileECSOnDemandOverride(t *testing.T) {
	p := loadTestProfile(t, "ecs-basic.yaml") // spot: true in profile
	id := "sb-ecsod001"

	// onDemand=true overrides spot=true -> use FARGATE not FARGATE_SPOT
	artifacts, err := compiler.Compile(p, id, true, testNetwork())
	if err != nil {
		t.Fatalf("Compile(ECS, spot=true, onDemand=true) error = %v", err)
	}
	if !strings.Contains(artifacts.ServiceHCL, "use_spot       = false") {
		t.Errorf("ECS onDemand=true override should contain use_spot = false\nGot:\n%s", artifacts.ServiceHCL)
	}
}

func TestCompileECSNetworkConfig(t *testing.T) {
	p := loadTestProfile(t, "ecs-basic.yaml")
	id := "sb-ecssd001"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(ECS) error = %v", err)
	}

	hcl := artifacts.ServiceHCL

	if !strings.Contains(hcl, "vpc-test123") {
		t.Errorf("ECS ServiceHCL missing vpc_id\nGot:\n%s", hcl)
	}
	if !strings.Contains(hcl, "subnet-pub1") {
		t.Errorf("ECS ServiceHCL missing public_subnets\nGot:\n%s", hcl)
	}
	if !strings.Contains(hcl, id) {
		t.Errorf("ECS ServiceHCL missing sandbox ID %q\nGot:\n%s", id, hcl)
	}
}

func TestCompileECSTagging(t *testing.T) {
	p := loadTestProfile(t, "ecs-basic.yaml")
	id := "sb-ecstag01"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(ECS) error = %v", err)
	}

	// sandbox_id should appear multiple times (locals + module_inputs)
	count := strings.Count(artifacts.ServiceHCL, id)
	if count < 2 {
		t.Errorf("ECS ServiceHCL should contain sandbox_id at least twice, got %d occurrences\nGot:\n%s", count, artifacts.ServiceHCL)
	}
}

// ============================================================
// Budget enforcer compiler integration tests (BUDG-03, BUDG-07)
// ============================================================

// TestCompileEC2WithBudget verifies that a budget profile produces budget_enforcer_inputs
// in the EC2 service.hcl.
func TestCompileEC2WithBudget(t *testing.T) {
	p := loadTestProfile(t, "ec2-with-budget.yaml")
	id := "sb-budgec2a"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(EC2, with budget) error = %v", err)
	}

	hcl := artifacts.ServiceHCL

	// Budget block should be present
	if !strings.Contains(hcl, "budget_enforcer_inputs") {
		t.Errorf("EC2 ServiceHCL should contain budget_enforcer_inputs when budget is set\nGot:\n%s", hcl)
	}
	// Compute and AI limits should appear
	if !strings.Contains(hcl, "compute_limit_usd") {
		t.Errorf("EC2 ServiceHCL budget block missing compute_limit_usd\nGot:\n%s", hcl)
	}
	if !strings.Contains(hcl, "ai_limit_usd") {
		t.Errorf("EC2 ServiceHCL budget block missing ai_limit_usd\nGot:\n%s", hcl)
	}
}

// TestCompileEC2NoBudget verifies that a profile without budget does NOT include
// budget_enforcer_inputs in service.hcl.
func TestCompileEC2NoBudget(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	id := "sb-nobudget"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(EC2, no budget) error = %v", err)
	}

	if strings.Contains(artifacts.ServiceHCL, "budget_enforcer_inputs") {
		t.Errorf("EC2 ServiceHCL should NOT contain budget_enforcer_inputs when budget is nil\nGot:\n%s", artifacts.ServiceHCL)
	}
}

// TestCompileEC2BudgetCAInjection verifies that a budget profile injects CA cert
// setup into the EC2 user-data script.
func TestCompileEC2BudgetCAInjection(t *testing.T) {
	p := loadTestProfile(t, "ec2-with-budget.yaml")
	id := "sb-budgca01"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(EC2, with budget) error = %v", err)
	}

	ud := artifacts.UserData

	// CA cert injection and budget environment variables should be present
	if !strings.Contains(ud, "km-proxy-ca.crt") {
		t.Errorf("UserData should contain CA cert injection for budget-enabled profile\nGot:\n%s", ud)
	}
	if !strings.Contains(ud, "km-proxy-ca.key") {
		t.Errorf("UserData should download CA private key for MITM signing\nGot:\n%s", ud)
	}
	if !strings.Contains(ud, "KM_PROXY_CA_CERT") {
		t.Errorf("UserData should pass KM_PROXY_CA_CERT to km-http-proxy via systemd drop-in\nGot:\n%s", ud)
	}
	if !strings.Contains(ud, "KM_BUDGET_ENABLED") {
		t.Errorf("UserData should contain KM_BUDGET_ENABLED for budget-enabled profile\nGot:\n%s", ud)
	}
	if !strings.Contains(ud, "/run/km") {
		t.Errorf("UserData should create /run/km for budget_remaining file\nGot:\n%s", ud)
	}
}

// TestCompileEC2NoBudgetNoCACert verifies that no CA cert injection occurs
// when budget is not configured.
func TestCompileEC2NoBudgetNoCACert(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	id := "sb-nocert01"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(EC2, no budget) error = %v", err)
	}

	if strings.Contains(artifacts.UserData, "km-proxy-ca.crt") {
		t.Errorf("UserData should NOT contain CA cert injection when budget is nil\nGot:\n%s", artifacts.UserData)
	}
}

// ============================================================
// GitHub token HCL compiler integration tests (GH-02, GH-04, GH-05)
// ============================================================

// TestGitHubTokenHCL verifies that Compile() produces a non-empty GitHubTokenHCL
// when sourceAccess.github is set.
func TestGitHubTokenHCL(t *testing.T) {
	p := loadTestProfile(t, "ec2-with-secrets.yaml") // has sourceAccess.github
	id := "sb-ghtkn001"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if artifacts.GitHubTokenHCL == "" {
		t.Error("Compile() GitHubTokenHCL should not be empty when sourceAccess.github is set")
	}
	if !strings.Contains(artifacts.GitHubTokenHCL, "github-token") {
		t.Errorf("GitHubTokenHCL should reference github-token module\nGot:\n%s", artifacts.GitHubTokenHCL)
	}
	if !strings.Contains(artifacts.GitHubTokenHCL, id) {
		t.Errorf("GitHubTokenHCL should contain sandbox_id %q\nGot:\n%s", id, artifacts.GitHubTokenHCL)
	}
}

// TestNoGitHubTokenHCL verifies that Compile() produces empty GitHubTokenHCL
// when sourceAccess.github is nil.
func TestNoGitHubTokenHCL(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml") // no sourceAccess.github
	id := "sb-nghtkn01"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if artifacts.GitHubTokenHCL != "" {
		t.Errorf("Compile() GitHubTokenHCL should be empty when sourceAccess.github is nil\nGot:\n%s", artifacts.GitHubTokenHCL)
	}
}

// TestServiceHCLEC2GitHubInputs verifies that EC2 service.hcl contains
// github_token_inputs when sourceAccess.github is configured.
func TestServiceHCLEC2GitHubInputs(t *testing.T) {
	p := loadTestProfile(t, "ec2-with-secrets.yaml")
	id := "sb-ghec2hcl"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	hcl := artifacts.ServiceHCL

	if !strings.Contains(hcl, "github_token_inputs") {
		t.Errorf("EC2 ServiceHCL should contain github_token_inputs when sourceAccess.github is set\nGot:\n%s", hcl)
	}
	// Should include sandbox_id
	if !strings.Contains(hcl, "sandbox_id") {
		t.Errorf("EC2 ServiceHCL github_token_inputs missing sandbox_id\nGot:\n%s", hcl)
	}
	// Should include ssm_parameter_name
	if !strings.Contains(hcl, "ssm_parameter_name") {
		t.Errorf("EC2 ServiceHCL github_token_inputs missing ssm_parameter_name\nGot:\n%s", hcl)
	}
	// SSM path should be sandbox-scoped
	if !strings.Contains(hcl, "/sandbox/"+id+"/github-token") {
		t.Errorf("EC2 ServiceHCL github_token_inputs should have SSM path /sandbox/%s/github-token\nGot:\n%s", id, hcl)
	}
	// allowed_repos must be present
	if !strings.Contains(hcl, "allowed_repos") {
		t.Errorf("EC2 ServiceHCL github_token_inputs missing allowed_repos\nGot:\n%s", hcl)
	}
	// permissions must be present
	if !strings.Contains(hcl, "permissions") {
		t.Errorf("EC2 ServiceHCL github_token_inputs missing permissions\nGot:\n%s", hcl)
	}
}

// TestServiceHCLECSGitHubInputs verifies that ECS service.hcl contains
// github_token_inputs when sourceAccess.github is configured.
func TestServiceHCLECSGitHubInputs(t *testing.T) {
	p := loadTestProfile(t, "ecs-with-github.yaml")
	id := "sb-ghecs001"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(ECS) error = %v", err)
	}

	hcl := artifacts.ServiceHCL

	if !strings.Contains(hcl, "github_token_inputs") {
		t.Errorf("ECS ServiceHCL should contain github_token_inputs when sourceAccess.github is set\nGot:\n%s", hcl)
	}
	// SSM path should be sandbox-scoped
	if !strings.Contains(hcl, "/sandbox/"+id+"/github-token") {
		t.Errorf("ECS ServiceHCL github_token_inputs should have SSM path /sandbox/%s/github-token\nGot:\n%s", id, hcl)
	}
	// allowed_repos from profile
	if !strings.Contains(hcl, "myorg/myrepo") {
		t.Errorf("ECS ServiceHCL github_token_inputs missing allowed_repos from profile\nGot:\n%s", hcl)
	}
}

// TestServiceHCLEC2NoGitHubInputs verifies that EC2 service.hcl does NOT contain
// github_token_inputs when sourceAccess.github is nil.
func TestServiceHCLEC2NoGitHubInputs(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml") // no github
	id := "sb-noghec21"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if strings.Contains(artifacts.ServiceHCL, "github_token_inputs") {
		t.Errorf("EC2 ServiceHCL should NOT contain github_token_inputs when sourceAccess.github is nil\nGot:\n%s", artifacts.ServiceHCL)
	}
}

// TestServiceHCLECSNoGitHubInputs verifies that ECS service.hcl does NOT contain
// github_token_inputs when sourceAccess.github is nil.
func TestServiceHCLECSNoGitHubInputs(t *testing.T) {
	p := loadTestProfile(t, "ecs-basic.yaml") // no github
	id := "sb-noghecs1"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(ECS) error = %v", err)
	}

	if strings.Contains(artifacts.ServiceHCL, "github_token_inputs") {
		t.Errorf("ECS ServiceHCL should NOT contain github_token_inputs when sourceAccess.github is nil\nGot:\n%s", artifacts.ServiceHCL)
	}
}

// TestGitHubTokenHCLECS verifies that ECS Compile() also produces GitHubTokenHCL
// when sourceAccess.github is set.
func TestGitHubTokenHCLECS(t *testing.T) {
	p := loadTestProfile(t, "ecs-with-github.yaml")
	id := "sb-ghecshcl"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(ECS) error = %v", err)
	}

	if artifacts.GitHubTokenHCL == "" {
		t.Error("Compile(ECS) GitHubTokenHCL should not be empty when sourceAccess.github is set")
	}
	if !strings.Contains(artifacts.GitHubTokenHCL, "github-token") {
		t.Errorf("ECS GitHubTokenHCL should reference github-token module\nGot:\n%s", artifacts.GitHubTokenHCL)
	}
}

// ============================================================
// Phase 25: Deny-by-default tests for empty allowedRepos
// ============================================================

// TestCompileEC2EmptyAllowedRepos_DenyByDefault verifies that a profile with a
// non-nil github block but allowedRepos: [] produces NO github_token_inputs in
// service.hcl and NO GitHubTokenHCL artifact. Empty repos must be treated
// identically to a nil github config (deny-by-default contract).
func TestCompileEC2EmptyAllowedRepos_DenyByDefault(t *testing.T) {
	p := loadTestProfile(t, "ec2-empty-repos.yaml")
	id := "sb-ec2norepo"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(EC2 empty repos) error = %v", err)
	}

	if strings.Contains(artifacts.ServiceHCL, "github_token_inputs") {
		t.Errorf("EC2 ServiceHCL should NOT contain github_token_inputs when allowedRepos is empty\nGot:\n%s", artifacts.ServiceHCL)
	}
	if artifacts.GitHubTokenHCL != "" {
		t.Errorf("EC2 GitHubTokenHCL should be empty when allowedRepos is empty\nGot:\n%s", artifacts.GitHubTokenHCL)
	}
}

// TestCompileECSEmptyAllowedRepos_DenyByDefault verifies that a profile with a
// non-nil github block but allowedRepos: [] produces NO github_token_inputs in
// the ECS service.hcl and NO GitHubTokenHCL artifact.
func TestCompileECSEmptyAllowedRepos_DenyByDefault(t *testing.T) {
	p := loadTestProfile(t, "ecs-empty-repos.yaml")
	id := "sb-ecsnorepo"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(ECS empty repos) error = %v", err)
	}

	if strings.Contains(artifacts.ServiceHCL, "github_token_inputs") {
		t.Errorf("ECS ServiceHCL should NOT contain github_token_inputs when allowedRepos is empty\nGot:\n%s", artifacts.ServiceHCL)
	}
	if artifacts.GitHubTokenHCL != "" {
		t.Errorf("ECS GitHubTokenHCL should be empty when allowedRepos is empty\nGot:\n%s", artifacts.GitHubTokenHCL)
	}
}

// TestUserDataEmptyAllowedRepos_NoGITASKPASS verifies that a profile with a
// non-nil github block but allowedRepos: [] does NOT emit any km-git-askpass
// or GIT_ASKPASS section in the EC2 user-data script.
func TestUserDataEmptyAllowedRepos_NoGITASKPASS(t *testing.T) {
	p := loadTestProfile(t, "ec2-empty-repos.yaml")
	id := "sb-udnoask"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(EC2 empty repos) error = %v", err)
	}

	if strings.Contains(artifacts.UserData, "km-git-askpass") {
		t.Errorf("UserData should NOT contain km-git-askpass when allowedRepos is empty\nGot:\n%s", artifacts.UserData)
	}
	if strings.Contains(artifacts.UserData, "GIT_ASKPASS") {
		t.Errorf("UserData should NOT contain GIT_ASKPASS when allowedRepos is empty\nGot:\n%s", artifacts.UserData)
	}
}
