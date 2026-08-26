package bridge_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

// stubTokenRefresher records the sandbox ids it was asked to re-credential.
type stubTokenRefresher struct {
	calls []string
	err   error
}

func (s *stubTokenRefresher) RefreshGitHubToken(_ context.Context, sandboxID string) error {
	s.calls = append(s.calls, sandboxID)
	return s.err
}

func stoppedInstanceEC2() *fakeEC2Client {
	return &fakeEC2Client{
		describeResponses: []*ec2.DescribeInstancesOutput{
			{Reservations: []ec2types.Reservation{
				{Instances: []ec2types.Instance{makeInstance("i-1", ec2types.InstanceStateNameStopped)}},
			}},
		},
	}
}

// TestEC2Resumer_RefreshesTokenAfterAutoResume covers the autonomous wake-up
// path: a HackerOne report comment that starts a stopped box must get the same
// fresh GitHub token the operator-driven `km resume` mints.
func TestEC2Resumer_RefreshesTokenAfterAutoResume(t *testing.T) {
	fake := stoppedInstanceEC2()
	refresher := &stubTokenRefresher{}
	r := &bridge.EC2Resumer{Client: fake, TokenRefresher: refresher}

	if err := r.StartSandbox(context.Background(), "sb-abc"); err != nil {
		t.Fatalf("StartSandbox: %v", err)
	}
	if !fake.startCalled {
		t.Fatal("StartInstances was not called")
	}
	if len(refresher.calls) != 1 || refresher.calls[0] != "sb-abc" {
		t.Fatalf("refresher calls = %v, want [sb-abc]", refresher.calls)
	}
}

// TestEC2Resumer_RefreshFailureDoesNotFailResume: the bridges branch on
// StartSandbox's error, and on the Phase 109 terminal path a failure deletes the
// sandbox's DynamoDB row and cold-creates a replacement. A token-refresh failure
// must never reach that branch.
func TestEC2Resumer_RefreshFailureDoesNotFailResume(t *testing.T) {
	for _, refreshErr := range []error{
		errors.New("AccessDenied"),
		awspkg.ErrNoGitHubRefresher,
	} {
		fake := stoppedInstanceEC2()
		r := &bridge.EC2Resumer{Client: fake, TokenRefresher: &stubTokenRefresher{err: refreshErr}}

		if err := r.StartSandbox(context.Background(), "sb-abc"); err != nil {
			t.Fatalf("StartSandbox returned %v for refresh error %v; must stay nil", err, refreshErr)
		}
		if !fake.startCalled {
			t.Fatal("StartInstances was not called")
		}
	}
}

// TestEC2Resumer_NilRefresherIsSafe: a bridge deployed before its wiring lands
// must resume exactly as before, not panic.
func TestEC2Resumer_NilRefresherIsSafe(t *testing.T) {
	fake := stoppedInstanceEC2()
	r := &bridge.EC2Resumer{Client: fake}

	if err := r.StartSandbox(context.Background(), "sb-abc"); err != nil {
		t.Fatalf("StartSandbox: %v", err)
	}
	if !fake.startCalled {
		t.Fatal("StartInstances was not called")
	}
}

// TestEC2Resumer_NoRefreshWhenStartFails: never mint a token for a box that did
// not actually start.
func TestEC2Resumer_NoRefreshWhenStartFails(t *testing.T) {
	fake := stoppedInstanceEC2()
	fake.startErr = errors.New("IncorrectInstanceState")
	refresher := &stubTokenRefresher{}
	r := &bridge.EC2Resumer{Client: fake, TokenRefresher: refresher}

	if err := r.StartSandbox(context.Background(), "sb-abc"); err == nil {
		t.Fatal("expected StartSandbox to fail when StartInstances fails")
	}
	if len(refresher.calls) != 0 {
		t.Fatalf("refresher called %v after a failed start", refresher.calls)
	}
}

// TestEC2Resumer_NoRefreshWhenNoInstance: the orphaned-alias path falls through
// to cold-create; there is no box to credential.
func TestEC2Resumer_NoRefreshWhenNoInstance(t *testing.T) {
	fake := &fakeEC2Client{describeResponses: []*ec2.DescribeInstancesOutput{{}}}
	refresher := &stubTokenRefresher{}
	r := &bridge.EC2Resumer{Client: fake, TokenRefresher: refresher}

	err := r.StartSandbox(context.Background(), "sb-abc")
	if !errors.Is(err, bridge.ErrNoResumableInstance) {
		t.Fatalf("expected ErrNoResumableInstance, got %v", err)
	}
	if len(refresher.calls) != 0 {
		t.Fatalf("refresher called %v with no instance to resume", refresher.calls)
	}
}
