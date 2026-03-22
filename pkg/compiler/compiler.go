package compiler

import (
	"encoding/base64"
	"fmt"

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
	UserData string

	// SGEgressRules holds the compiled SG egress rules as Go structs for programmatic access.
	// The same rules are serialized into ServiceHCL.module_inputs.sg_egress_rules.
	SGEgressRules []SGRule

	// IAMPolicy holds the compiled IAM session policy as a Go struct.
	// The same values are serialized into ServiceHCL.module_inputs.iam_session_policy.
	IAMPolicy *IAMSessionPolicy

	// SecretPaths is the list of SSM parameter paths to inject at boot time.
	SecretPaths []string
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
		return compileECS(p, sandboxID, onDemand)
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

	// Generate user-data.sh first — it gets embedded into service.hcl
	userData, err := generateUserData(p, sandboxID, secretPaths)
	if err != nil {
		return nil, fmt.Errorf("generate user-data.sh: %w", err)
	}

	// Base64-encode user-data for safe embedding in HCL
	userDataB64 := base64.StdEncoding.EncodeToString([]byte(userData))

	// Generate service.hcl (includes user-data inline in ec2spots[].user_data_base64)
	svcHCL, err := generateEC2ServiceHCL(p, sandboxID, useSpot, sgRules, iamPolicy, userDataB64, network)
	if err != nil {
		return nil, fmt.Errorf("generate EC2 service.hcl: %w", err)
	}

	return &CompiledArtifacts{
		SandboxID:     sandboxID,
		ServiceHCL:    svcHCL,
		UserData:      userData,
		SGEgressRules: sgRules,
		IAMPolicy:     iamPolicy,
		SecretPaths:   secretPaths,
	}, nil
}

// compileECS handles the ECS substrate compilation path.
func compileECS(p *profile.SandboxProfile, sandboxID string, onDemand bool) (*CompiledArtifacts, error) {
	// onDemand=true overrides profile's spot=true
	useSpot := p.Spec.Runtime.Spot && !onDemand

	// Compile security configuration (same rules apply to ECS, different enforcement path)
	sgRules := compileSGRules(p)
	iamPolicy := compileIAMPolicy(p)
	secretPaths := compileSecrets(p)

	// Generate service.hcl (ECS-specific template; no user-data.sh for ECS)
	svcHCL, err := generateECSServiceHCL(p, sandboxID, useSpot)
	if err != nil {
		return nil, fmt.Errorf("generate ECS service.hcl: %w", err)
	}

	return &CompiledArtifacts{
		SandboxID:     sandboxID,
		ServiceHCL:    svcHCL,
		UserData:      "", // ECS containers handle their own setup — no bootstrap script
		SGEgressRules: sgRules,
		IAMPolicy:     iamPolicy,
		SecretPaths:   secretPaths,
	}, nil
}
