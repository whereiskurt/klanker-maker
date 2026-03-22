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

func TestCompileGitHubToken(t *testing.T) {
	// Profile with github sourceAccess -> github token SSM path injected
	p := loadTestProfile(t, "ec2-with-secrets.yaml")
	id := "sb-ghtest01"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	found := false
	for _, sp := range artifacts.SecretPaths {
		if sp == "/km/github/app-token" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("SecretPaths missing /km/github/app-token for profile with github sourceAccess; got %v", artifacts.SecretPaths)
	}

	// Also check user-data includes GITHUB_TOKEN injection
	if !strings.Contains(artifacts.UserData, "GITHUB_TOKEN") {
		t.Errorf("UserData should inject GITHUB_TOKEN for profile with github sourceAccess\nGot:\n%s", artifacts.UserData)
	}

	// Profile without github -> no github token
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
	if !strings.Contains(artifacts.ServiceHCL, `substrate_module = "ecs-cluster"`) {
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
	if !strings.Contains(artifacts.ServiceHCL, "FARGATE_SPOT") {
		t.Errorf("ECS spot=true ServiceHCL should contain FARGATE_SPOT\nGot:\n%s", artifacts.ServiceHCL)
	}

	// spot=false
	pNoSpot := loadTestProfile(t, "ecs-basic.yaml")
	pNoSpot.Spec.Runtime.Spot = false
	artifactsNoSpot, err := compiler.Compile(pNoSpot, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(ECS, spot=false) error = %v", err)
	}
	if strings.Contains(artifactsNoSpot.ServiceHCL, "FARGATE_SPOT") {
		t.Errorf("ECS spot=false ServiceHCL should NOT contain FARGATE_SPOT\nGot:\n%s", artifactsNoSpot.ServiceHCL)
	}
	if !strings.Contains(artifactsNoSpot.ServiceHCL, "FARGATE") {
		t.Errorf("ECS spot=false ServiceHCL should contain FARGATE capacity provider\nGot:\n%s", artifactsNoSpot.ServiceHCL)
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
	if strings.Contains(artifacts.ServiceHCL, "FARGATE_SPOT") {
		t.Errorf("ECS onDemand=true override should NOT contain FARGATE_SPOT\nGot:\n%s", artifacts.ServiceHCL)
	}
	if !strings.Contains(artifacts.ServiceHCL, "FARGATE") {
		t.Errorf("ECS onDemand=true override should contain FARGATE\nGot:\n%s", artifacts.ServiceHCL)
	}
}

func TestCompileECSServiceDiscovery(t *testing.T) {
	p := loadTestProfile(t, "ecs-basic.yaml")
	id := "sb-ecssd001"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(ECS) error = %v", err)
	}

	hcl := artifacts.ServiceHCL

	if !strings.Contains(hcl, "service_discovery") {
		t.Errorf("ECS ServiceHCL missing service_discovery\nGot:\n%s", hcl)
	}
	if !strings.Contains(hcl, id) {
		t.Errorf("ECS ServiceHCL service_discovery missing sandbox ID %q\nGot:\n%s", id, hcl)
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
