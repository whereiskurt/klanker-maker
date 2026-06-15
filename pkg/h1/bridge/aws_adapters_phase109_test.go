// aws_adapters_phase109_test.go — Phase 109 (H1 parity) unit tests for the
// EC2Resumer.StartSandbox terminal-vs-transient failure distinction.
//
// Mirrors pkg/github/bridge/aws_adapters_test.go's ErrNoResumableInstance tests.
package bridge_test

import (
	"context"
	"errors"
	"strings"
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
	// describeInputs records every DescribeInstancesInput so tests can assert the
	// filters (e.g. the tag key the resumer derived). Empty until DescribeInstances runs.
	describeInputs []*ec2.DescribeInstancesInput

	startCalled bool
	startErr    error
}

func (f *fakeEC2Client) DescribeInstances(_ context.Context, params *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	f.describeInputs = append(f.describeInputs, params)
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

// TestEC2Resumer_NonKmPrefix_FiltersOnKmSandboxIdTag is the H1 mirror of the GitHub
// regression for the 2026-06-12 `sec`-install incident: on a non-"km" resource_prefix
// the resumer derived the tag key from ResourcePrefix ("sec:sandbox-id"), which km never
// applies — km always tags instances "km:sandbox-id" regardless of resource_prefix. The
// filter matched nothing → ErrNoResumableInstance → needless delete + cold-create. This
// reproduces the production wiring (ResourcePrefix set, SandboxIDTagKey empty).
func TestEC2Resumer_NonKmPrefix_FiltersOnKmSandboxIdTag(t *testing.T) {
	fake := &fakeEC2Client{
		describeResponses: []*ec2.DescribeInstancesOutput{
			{Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{
				makeInstance("i-stopped1", ec2types.InstanceStateNameStopped),
			}}}},
		},
	}
	// Wire it exactly like cmd/km-h1-bridge/main.go: ResourcePrefix set, no SandboxIDTagKey.
	resumer := &bridge.EC2Resumer{Client: fake, ResourcePrefix: "sec"}

	if err := resumer.StartSandbox(context.Background(), "h1-cc433b2e"); err != nil {
		t.Fatalf("StartSandbox on a non-km prefix install must resume the stopped instance, got: %v", err)
	}
	if !fake.startCalled {
		t.Fatal("StartInstances must be called — the stopped instance is resumable")
	}
	if len(fake.describeInputs) == 0 {
		t.Fatal("DescribeInstances was never called")
	}
	var sandboxTagFilter string
	for _, fil := range fake.describeInputs[0].Filters {
		if fil.Name != nil && strings.HasSuffix(*fil.Name, ":sandbox-id") {
			sandboxTagFilter = *fil.Name
		}
	}
	if sandboxTagFilter != "tag:km:sandbox-id" {
		t.Errorf("resume filter tag key = %q; want \"tag:km:sandbox-id\" (km tags instances km:sandbox-id regardless of resource_prefix)", sandboxTagFilter)
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
