package compiler

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// CompiledArtifacts holds the output of compiling a SandboxProfile.
// These artifacts are written to the sandbox directory and consumed by Terragrunt.
type CompiledArtifacts struct {
	// SandboxID is the unique identifier for this sandbox (e.g. "sb-a1b2c3d4").
	SandboxID string

	// ServiceHCL is the content for service.hcl — includes sg_egress_rules and
	// iam_session_policy in module_inputs so security configuration reaches Terraform.
	ServiceHCL string

	// UserData is the content for user-data.sh (EC2 only; empty for ECS).
	// When the script exceeds 12KB, this contains a bootstrap stub and
	// FullUserData holds the complete script for S3 upload.
	UserData string

	// FullUserData holds the complete user-data script when it exceeds
	// the 16KB EC2 limit. Empty when UserData fits within the limit.
	// km create uploads this to S3 as artifacts/{sandbox-id}/km-userdata.sh.
	FullUserData string

	// SGEgressRules holds the compiled SG egress rules as Go structs for programmatic access.
	// The same rules are serialized into ServiceHCL.module_inputs.sg_egress_rules.
	SGEgressRules []SGRule

	// IAMPolicy holds the compiled IAM session policy as a Go struct.
	// The same values are serialized into ServiceHCL.module_inputs.iam_session_policy.
	IAMPolicy *IAMSessionPolicy

	// SecretPaths is the list of SSM parameter paths to inject at boot time.
	SecretPaths []string

	// BudgetEnforcerHCL is the content for budget-enforcer/terragrunt.hcl
	// (empty if no budget defined). Written to sandbox-dir/budget-enforcer/
	// and applied by km create after the main sandbox apply.
	BudgetEnforcerHCL string

	// GitHubTokenHCL is the content for github-token/terragrunt.hcl
	// (empty if sourceAccess.github is nil). Written to sandbox-dir/github-token/
	// and applied by km create when GitHub access is configured.
	GitHubTokenHCL string
}

// SGRule is a single SG egress rule emitted by the compiler.
type SGRule struct {
	FromPort    int
	ToPort      int
	Protocol    string
	CIDRBlocks  []string
	Description string
}

// IAMSessionPolicy holds IAM session constraints compiled from the profile's identity spec.
type IAMSessionPolicy struct {
	// MaxSessionDuration is the maximum role session duration in seconds.
	MaxSessionDuration int
	// AllowedRegions restricts API calls to these AWS regions. Empty means no region restriction.
	AllowedRegions []string
}

// Compile translates a SandboxProfile into CompiledArtifacts.
//
// Parameters:
//   - p: the parsed and validated SandboxProfile
//   - sandboxID: the unique sandbox identifier (e.g. "sb-a1b2c3d4")
//   - onDemand: when true, overrides profile's spot=true to force on-demand provisioning
//
// Compile is a pure function with no AWS side effects — fully testable without credentials.
func Compile(p *profile.SandboxProfile, sandboxID string, onDemand bool, network *NetworkConfig) (*CompiledArtifacts, error) {
	substrate := p.Spec.Runtime.Substrate
	switch substrate {
	case "ec2":
		return compileEC2(p, sandboxID, onDemand, network)
	case "ecs":
		return compileECS(p, sandboxID, onDemand, network)
	default:
		return nil, fmt.Errorf("unknown substrate %q: must be \"ec2\" or \"ecs\"", substrate)
	}
}

// compileEC2 handles the EC2 substrate compilation path.
func compileEC2(p *profile.SandboxProfile, sandboxID string, onDemand bool, network *NetworkConfig) (*CompiledArtifacts, error) {
	// onDemand=true overrides profile's spot=true
	useSpot := p.Spec.Runtime.Spot && !onDemand

	// Compile security configuration
	sgRules := compileSGRules(p)
	iamPolicy := compileIAMPolicy(p)
	secretPaths := compileSecrets(p)

	// Generate user-data.sh first — it gets embedded into service.hcl.
	// KM_ARTIFACTS_BUCKET is read from the environment; may be empty in tests.
	artifactsBucket := os.Getenv("KM_ARTIFACTS_BUCKET")
	userData, err := generateUserData(p, sandboxID, secretPaths, artifactsBucket, useSpot, network.EmailDomain)
	if err != nil {
		return nil, fmt.Errorf("generate user-data.sh: %w", err)
	}

	// If user-data exceeds 12KB (leaving room for base64 overhead under 16KB limit),
	// replace it with a bootstrap stub that downloads the full script from S3.
	var fullUserData string
	if len(userData) > 12000 && artifactsBucket != "" {
		fullUserData = userData
		userData = fmt.Sprintf(`#!/bin/bash
set -e
# Bootstrap stub — full user-data is in S3 (script was %d bytes, over 12KB limit)
IMDS_TOKEN=$(curl -sf -X PUT "http://169.254.169.254/latest/api/token" -H "X-aws-ec2-metadata-token-ttl-seconds: 21600")
REGION=$(curl -sf -H "X-aws-ec2-metadata-token: $IMDS_TOKEN" "http://169.254.169.254/latest/meta-data/placement/region")
export AWS_DEFAULT_REGION="$REGION"
echo "[km-bootstrap] Downloading full bootstrap script from S3..."
aws s3 cp "s3://%s/artifacts/%s/km-userdata.sh" /tmp/km-userdata.sh
chmod +x /tmp/km-userdata.sh
echo "[km-bootstrap] Running full bootstrap script..."
exec /tmp/km-userdata.sh
`, len(userData), artifactsBucket, sandboxID)
	}

	// Base64-encode user-data for safe embedding in HCL
	userDataB64 := base64.StdEncoding.EncodeToString([]byte(userData))

	// Generate service.hcl (includes user-data inline in ec2spots[].user_data_base64)
	svcHCL, err := generateEC2ServiceHCL(p, sandboxID, useSpot, sgRules, iamPolicy, userDataB64, network)
	if err != nil {
		return nil, fmt.Errorf("generate EC2 service.hcl: %w", err)
	}

	// Generate budget-enforcer HCL when budget is defined in the profile.
	var budgetEnforcerHCL string
	if p.Spec.Budget != nil {
		budgetEnforcerHCL, err = generateBudgetEnforcerHCL(sandboxID)
		if err != nil {
			return nil, fmt.Errorf("generate budget-enforcer HCL: %w", err)
		}
	}

	// Generate github-token HCL when sourceAccess.github is configured with at least one repo.
	// Empty allowedRepos is treated as deny-by-default (same as nil github config).
	var gitHubTokenHCL string
	if p.Spec.SourceAccess.GitHub != nil && len(p.Spec.SourceAccess.GitHub.AllowedRepos) > 0 {
		gitHubTokenHCL, err = generateGitHubTokenHCL(sandboxID, p)
		if err != nil {
			return nil, fmt.Errorf("generate github-token HCL: %w", err)
		}
	}

	return &CompiledArtifacts{
		SandboxID:         sandboxID,
		ServiceHCL:        svcHCL,
		UserData:          userData,
		FullUserData:      fullUserData,
		SGEgressRules:     sgRules,
		IAMPolicy:         iamPolicy,
		SecretPaths:       secretPaths,
		BudgetEnforcerHCL: budgetEnforcerHCL,
		GitHubTokenHCL:    gitHubTokenHCL,
	}, nil
}

// compileECS handles the ECS substrate compilation path.
func compileECS(p *profile.SandboxProfile, sandboxID string, onDemand bool, network *NetworkConfig) (*CompiledArtifacts, error) {
	// onDemand=true overrides profile's spot=true
	useSpot := p.Spec.Runtime.Spot && !onDemand

	// Compile security configuration (same rules apply to ECS, different enforcement path)
	sgRules := compileSGRules(p)
	iamPolicy := compileIAMPolicy(p)
	secretPaths := compileSecrets(p)

	// Generate service.hcl (ECS-specific template; no user-data.sh for ECS)
	svcHCL, err := generateECSServiceHCL(p, sandboxID, useSpot, sgRules, network)
	if err != nil {
		return nil, fmt.Errorf("generate ECS service.hcl: %w", err)
	}

	// Generate budget-enforcer HCL when budget is defined in the profile.
	var budgetEnforcerHCL string
	if p.Spec.Budget != nil {
		var beErr error
		budgetEnforcerHCL, beErr = generateBudgetEnforcerHCL(sandboxID)
		if beErr != nil {
			return nil, fmt.Errorf("generate budget-enforcer HCL: %w", beErr)
		}
	}

	// Generate github-token HCL when sourceAccess.github is configured with at least one repo.
	// Empty allowedRepos is treated as deny-by-default (same as nil github config).
	var gitHubTokenHCL string
	if p.Spec.SourceAccess.GitHub != nil && len(p.Spec.SourceAccess.GitHub.AllowedRepos) > 0 {
		var ghErr error
		gitHubTokenHCL, ghErr = generateGitHubTokenHCL(sandboxID, p)
		if ghErr != nil {
			return nil, fmt.Errorf("generate github-token HCL: %w", ghErr)
		}
	}

	return &CompiledArtifacts{
		SandboxID:         sandboxID,
		ServiceHCL:        svcHCL,
		UserData:          "", // ECS containers handle their own setup — no bootstrap script
		SGEgressRules:     sgRules,
		IAMPolicy:         iamPolicy,
		SecretPaths:       secretPaths,
		BudgetEnforcerHCL: budgetEnforcerHCL,
		GitHubTokenHCL:    gitHubTokenHCL,
	}, nil
}
