package compiler_test

import (
	"os"
	"strings"
	"testing"
)

func fenceModuleMainTF(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(ec2spotModuleDir + "/main.tf")
	if err != nil {
		t.Fatalf("cannot read the live ec2spot module: %v", err)
	}
	return string(b)
}

// A role cannot name its own ARN as a trust principal — IAM resolves a principal
// to a unique id when the policy is SAVED and rejects one that does not exist
// yet. Verified live against the application account, 2026-09-04:
//
//	MalformedPolicyDocument: Invalid principal in policy: "AWS":"...:role/..."
//
// The account-root principal narrowed by aws:PrincipalArn is the single-pass
// equivalent, and is exactly as narrow: aws:PrincipalArn is a global condition
// key AWS populates on every request. (Contrast aws:RequestTag on the
// instance/* resource of ec2:RunInstances, which AWS never populates, making
// that condition unsatisfiable — the Phase 126 finding.)
func TestEC2Spot_SelfAssumeTrustDoesNotNameItsOwnARNAsPrincipal(t *testing.T) {
	src := fenceModuleMainTF(t)
	if !strings.Contains(src, "aws:PrincipalArn") {
		t.Fatal("no aws:PrincipalArn condition: the self-assume trust is either " +
			"missing or names its own ARN as a principal, which IAM rejects at " +
			"CreateRole time")
	}
	if strings.Contains(src, `Principal = { AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.resource_prefix}-ec2spot-ssm`) {
		t.Fatal("the trust policy names the role's own ARN as a principal: CreateRole " +
			"fails with MalformedPolicyDocument")
	}
}

// Root-principal delegation authorizes nothing on its own: the calling principal
// also needs an identity-based sts:AssumeRole on the role. Ship one half and the
// assume fails with an AccessDenied that names neither.
func TestEC2Spot_SelfAssumeHasBothHalves(t *testing.T) {
	if !strings.Contains(fenceModuleMainTF(t), "ec2spot_fence_self_assume") {
		t.Fatal("no identity-based sts:AssumeRole policy: root-principal trust alone " +
			"does not authorize the assume")
	}
}

// Dormant by default: fence_imds unset creates no fence resources at all.
func TestEC2Spot_FenceResourcesAreCountGated(t *testing.T) {
	block := extractResourceBlock(t, fenceModuleMainTF(t),
		`resource "aws_iam_role_policy" "ec2spot_fence_self_assume"`)
	if !strings.Contains(block, "var.fence_imds") {
		t.Error("the self-assume policy is not gated on var.fence_imds: every sandbox " +
			"would gain it, breaking the dormant case")
	}
	if !strings.Contains(block, "count") {
		t.Error("the self-assume policy has no count: it cannot be dormant")
	}
}

// The trust statement must be gated too — it lives on the role itself, which
// every sandbox creates, so an ungated statement would change every role.
func TestEC2Spot_SelfAssumeTrustStatementIsGated(t *testing.T) {
	block := extractResourceBlock(t, fenceModuleMainTF(t), `resource "aws_iam_role" "ec2spot_ssm"`)
	if !strings.Contains(block, "var.fence_imds") {
		t.Error("the assume_role_policy does not branch on var.fence_imds: every " +
			"sandbox role would gain the self-assume trust")
	}
}

func TestEC2Spot_FenceVarDefaultsToFalse(t *testing.T) {
	b, err := os.ReadFile(ec2spotModuleDir + "/variables.tf")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, `variable "fence_imds"`)
	if i < 0 {
		t.Fatal(`no variable "fence_imds" in the live module`)
	}
	end := i + 600
	if end > len(src) {
		end = len(src)
	}
	if !strings.Contains(src[i:end], "default     = false") &&
		!strings.Contains(src[i:end], "default = false") {
		t.Error("fence_imds does not default to false: every pre-Wave-2 caller would " +
			"fail to plan")
	}
}
