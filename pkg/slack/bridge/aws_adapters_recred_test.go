package bridge_test

import (
	"context"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
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

func stoppedInstanceMock() *mockEC2StartAPI {
	return &mockEC2StartAPI{
		describeOut: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{{
				InstanceId: awssdk.String("i-1"),
				State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameStopped},
			}}}},
		},
	}
}

// TestEC2Resumer_RefreshesTokenAfterAutoResume covers the autonomous wake-up
// path: a Slack @-mention that starts a stopped box must get the same fresh
// GitHub token the operator-driven `km resume` mints.
func TestEC2Resumer_RefreshesTokenAfterAutoResume(t *testing.T) {
	m := stoppedInstanceMock()
	refresher := &stubTokenRefresher{}
	r := &bridge.EC2Resumer{Client: m, TokenRefresher: refresher}

	if err := r.StartSandbox(context.Background(), "sb-abc"); err != nil {
		t.Fatalf("StartSandbox: %v", err)
	}
	if !m.startCalled {
		t.Fatal("StartInstances was not called")
	}
	if len(refresher.calls) != 1 || refresher.calls[0] != "sb-abc" {
		t.Fatalf("refresher calls = %v, want [sb-abc]", refresher.calls)
	}
}

// TestEC2Resumer_RefreshFailureDoesNotFailResume: the bridges branch on
// StartSandbox's error, so a token-refresh failure must never surface there.
func TestEC2Resumer_RefreshFailureDoesNotFailResume(t *testing.T) {
	for _, refreshErr := range []error{
		errors.New("AccessDenied"),
		awspkg.ErrNoGitHubRefresher,
	} {
		m := stoppedInstanceMock()
		r := &bridge.EC2Resumer{Client: m, TokenRefresher: &stubTokenRefresher{err: refreshErr}}

		if err := r.StartSandbox(context.Background(), "sb-abc"); err != nil {
			t.Fatalf("StartSandbox returned %v for refresh error %v; must stay nil", err, refreshErr)
		}
		if !m.startCalled {
			t.Fatal("StartInstances was not called")
		}
	}
}

// TestEC2Resumer_NilRefresherIsSafe: a bridge deployed before its wiring lands
// must resume exactly as before, not panic.
func TestEC2Resumer_NilRefresherIsSafe(t *testing.T) {
	m := stoppedInstanceMock()
	r := &bridge.EC2Resumer{Client: m}

	if err := r.StartSandbox(context.Background(), "sb-abc"); err != nil {
		t.Fatalf("StartSandbox: %v", err)
	}
	if !m.startCalled {
		t.Fatal("StartInstances was not called")
	}
}

// TestEC2Resumer_NoRefreshWhenStartFails: never mint a token for a box that did
// not actually start.
func TestEC2Resumer_NoRefreshWhenStartFails(t *testing.T) {
	m := stoppedInstanceMock()
	m.startErr = errors.New("IncorrectInstanceState")
	refresher := &stubTokenRefresher{}
	r := &bridge.EC2Resumer{Client: m, TokenRefresher: refresher}

	if err := r.StartSandbox(context.Background(), "sb-abc"); err == nil {
		t.Fatal("expected StartSandbox to fail when StartInstances fails")
	}
	if len(refresher.calls) != 0 {
		t.Fatalf("refresher called %v after a failed start", refresher.calls)
	}
}

// TestEC2Resumer_NoRefreshWhenNoInstance: nothing started, nothing to credential.
func TestEC2Resumer_NoRefreshWhenNoInstance(t *testing.T) {
	m := &mockEC2StartAPI{describeOut: &ec2.DescribeInstancesOutput{}}
	refresher := &stubTokenRefresher{}
	r := &bridge.EC2Resumer{Client: m, TokenRefresher: refresher}

	err := r.StartSandbox(context.Background(), "sb-abc")
	if !errors.Is(err, bridge.ErrNoResumableInstance) {
		t.Fatalf("expected ErrNoResumableInstance, got %v", err)
	}
	if len(refresher.calls) != 0 {
		t.Fatalf("refresher called %v with no instance to resume", refresher.calls)
	}
}
