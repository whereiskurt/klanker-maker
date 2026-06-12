// aws_adapters_phase109_test.go — Phase 109 (H1 parity) unit tests for the
// EC2Resumer.StartSandbox terminal-vs-transient failure distinction.
//
// Mirrors pkg/github/bridge/aws_adapters_test.go's ErrNoResumableInstance tests.
package bridge_test

import (
	"context"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

// fakeEC2Client implements bridge.EC2StartAPI for the EC2Resumer tests.
type fakeEC2Client struct {
	describeResponses []*ec2.DescribeInstancesOutput
	describeCallCount int
	describeErr       error

	startCalled bool
	startErr    error
}

func (f *fakeEC2Client) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	idx := f.describeCallCount
	if idx >= len(f.describeResponses) {
		idx = len(f.describeResponses) - 1
	}
	f.describeCallCount++
	return f.describeResponses[idx], nil
}

func (f *fakeEC2Client) StartInstances(_ context.Context, params *ec2.StartInstancesInput, _ ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	f.startCalled = true
	return &ec2.StartInstancesOutput{}, nil
}

// makeInstance builds a minimal ec2types.Instance for test responses.
func makeInstance(id string, state ec2types.InstanceStateName) ec2types.Instance {
	return ec2types.Instance{
		InstanceId: awssdk.String(id),
		State:      &ec2types.InstanceState{Name: state},
	}
}

// TestEC2Resumer_NoInstances_IsErrNoResumableInstance verifies the terminal
// "no instances" failure wraps the exported sentinel so the caller can branch with
// errors.Is and fall back to cold-create instead of enqueuing to a dead queue.
func TestEC2Resumer_NoInstances_IsErrNoResumableInstance(t *testing.T) {
	fake := &fakeEC2Client{
		describeResponses: []*ec2.DescribeInstancesOutput{{}}, // empty → no instances
	}
	resumer := &bridge.EC2Resumer{Client: fake, SandboxIDTagKey: "km:sandbox-id"}

	err := resumer.StartSandbox(context.Background(), "sb-gone")
	if err == nil {
		t.Fatal("expected error when no resumable instances exist")
	}
	if !errors.Is(err, bridge.ErrNoResumableInstance) {
		t.Errorf("errors.Is(err, ErrNoResumableInstance) = false; want true (err=%v)", err)
	}
	if fake.startCalled {
		t.Error("StartInstances must NOT be called when no instances found")
	}
}

// TestEC2Resumer_DescribeError_NotErrNoResumableInstance verifies that a transient
// DescribeInstances API error does NOT satisfy errors.Is for the sentinel — the
// caller must keep its log-and-enqueue (retry) behavior for it.
func TestEC2Resumer_DescribeError_NotErrNoResumableInstance(t *testing.T) {
	fake := &fakeEC2Client{describeErr: errors.New("AWS: RequestExpired")}
	resumer := &bridge.EC2Resumer{Client: fake, SandboxIDTagKey: "km:sandbox-id"}

	err := resumer.StartSandbox(context.Background(), "sb-err")
	if err == nil {
		t.Fatal("expected error from DescribeInstances API failure")
	}
	if errors.Is(err, bridge.ErrNoResumableInstance) {
		t.Error("a transient DescribeInstances error must NOT match ErrNoResumableInstance")
	}
}

// TestEC2Resumer_StoppedInstance_Resumes is the baseline: a stopped instance starts.
func TestEC2Resumer_StoppedInstance_Resumes(t *testing.T) {
	fake := &fakeEC2Client{
		describeResponses: []*ec2.DescribeInstancesOutput{
			{Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{
				makeInstance("i-stopped1", ec2types.InstanceStateNameStopped),
			}}}},
		},
	}
	resumer := &bridge.EC2Resumer{Client: fake, SandboxIDTagKey: "km:sandbox-id"}
	if err := resumer.StartSandbox(context.Background(), "sb-ok"); err != nil {
		t.Fatalf("expected nil error for stopped instance, got: %v", err)
	}
	if !fake.startCalled {
		t.Error("StartInstances must be called for a stopped instance")
	}
}
