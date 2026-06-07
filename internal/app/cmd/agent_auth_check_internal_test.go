package cmd

import (
	"context"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// stubSSM returns a fixed StandardOutputContent for the single auth-check command.
type stubSSM struct{ stdout string }

func (s *stubSSM) SendCommand(_ context.Context, _ *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	return &ssm.SendCommandOutput{Command: &ssmtypes.Command{CommandId: awssdk.String("cmd-1")}}, nil
}

func (s *stubSSM) GetCommandInvocation(_ context.Context, _ *ssm.GetCommandInvocationInput, _ ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return &ssm.GetCommandInvocationOutput{
		Status:               ssmtypes.CommandInvocationStatusSuccess,
		StandardOutputContent: awssdk.String(s.stdout),
	}, nil
}

// stubEC2 returns a canned DescribeInstances response and records whether it was called.
type stubEC2 struct {
	out    *ec2.DescribeInstancesOutput
	called bool
}

func (s *stubEC2) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	s.called = true
	return s.out, nil
}

func instancesOutput(id string, state ec2types.InstanceStateName) *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId: awssdk.String(id),
				State:      &ec2types.InstanceState{Name: state},
			}},
		}},
	}
}

// Regression for the bug where `km list --auth` showed "?" for every sandbox on
// the default DynamoDB list path: that path leaves rec.Resources empty, so the
// old CheckAuth (which only read the instance ARN out of rec.Resources) always
// errored. CheckAuth must now fall back to an EC2 tag lookup.
func TestCheckAuth_EmptyResources_FallsBackToEC2Tag(t *testing.T) {
	ssmStub := &stubSSM{stdout: `{"loggedIn": true}` + "\nKM_CODEX_OK\n"}
	ec2Stub := &stubEC2{out: instancesOutput("i-0abc", ec2types.InstanceStateNameRunning)}
	checker := &ssmAgentAuthChecker{ssmClient: ssmStub, ec2Client: ec2Stub}

	// rec.Resources empty — exactly what ListAllSandboxesByDynamo produces.
	rec := &kmaws.SandboxRecord{SandboxID: "sb-deadbeef", Status: "running"}

	cl, cx, err := checker.CheckAuth(context.Background(), rec)
	if err != nil {
		t.Fatalf("CheckAuth returned error (the bug); want fallback success: %v", err)
	}
	if !ec2Stub.called {
		t.Error("expected EC2 tag fallback to be used when rec.Resources is empty")
	}
	if !cl || !cx {
		t.Errorf("expected claude+codex logged-in parsed from SSM output, got cl=%v cx=%v", cl, cx)
	}
}

// When the instance ARN IS present (km status path), CheckAuth must use it
// directly and never call EC2 — preserving the existing fast path.
func TestCheckAuth_WithResources_SkipsEC2(t *testing.T) {
	ssmStub := &stubSSM{stdout: "KM_CODEX_MISSING\n"}
	ec2Stub := &stubEC2{out: instancesOutput("i-should-not-be-used", ec2types.InstanceStateNameRunning)}
	checker := &ssmAgentAuthChecker{ssmClient: ssmStub, ec2Client: ec2Stub}

	rec := &kmaws.SandboxRecord{
		SandboxID: "sb-deadbeef",
		Status:    "running",
		Resources: []string{"arn:aws:ec2:us-east-1:123456789012:instance/i-0fastpath"},
	}

	cl, cx, err := checker.CheckAuth(context.Background(), rec)
	if err != nil {
		t.Fatalf("CheckAuth error: %v", err)
	}
	if ec2Stub.called {
		t.Error("EC2 fallback should NOT be called when instance ARN is in rec.Resources")
	}
	if cl || cx {
		t.Errorf("expected not-logged-in for both, got cl=%v cx=%v", cl, cx)
	}
}

func TestResolveInstanceIDByTag_PrefersRunning(t *testing.T) {
	out := &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{
				{InstanceId: awssdk.String("i-terminated"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameTerminated}},
				{InstanceId: awssdk.String("i-running"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}},
			},
		}},
	}
	id, err := resolveInstanceIDByTag(context.Background(), &stubEC2{out: out}, "sb-x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "i-running" {
		t.Errorf("expected i-running (terminated skipped), got %q", id)
	}
}

func TestResolveInstanceIDByTag_NoInstance(t *testing.T) {
	_, err := resolveInstanceIDByTag(context.Background(), &stubEC2{out: &ec2.DescribeInstancesOutput{}}, "sb-missing")
	if err == nil {
		t.Fatal("expected error when no instance matches the tag")
	}
}
