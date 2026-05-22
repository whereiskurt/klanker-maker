package profile

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
)

// mockEC2SnapshotAPI implements EC2SnapshotAPI for table-driven tests.
type mockEC2SnapshotAPI struct {
	out *ec2.DescribeSnapshotsOutput
	err error
	// calls tracks invocations for "never called" assertions.
	calls int
}

func (m *mockEC2SnapshotAPI) DescribeSnapshots(ctx context.Context, params *ec2.DescribeSnapshotsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error) {
	m.calls++
	return m.out, m.err
}

func TestValidateSnapshotsAWS_HappyPath(t *testing.T) {
	p := &SandboxProfile{}
	p.Spec.Runtime.AdditionalSnapshots = []AdditionalSnapshotSpec{
		{SnapshotID: "snap-0123456789abcdef0", MountPoint: "/data1"},
		{SnapshotID: "snap-00fedcba98765432f", MountPoint: "/data2"},
	}
	sz := int32(10)
	mock := &mockEC2SnapshotAPI{
		out: &ec2.DescribeSnapshotsOutput{
			Snapshots: []ec2types.Snapshot{
				{SnapshotId: aws.String("snap-0123456789abcdef0"), State: ec2types.SnapshotStateCompleted, VolumeSize: &sz},
				{SnapshotId: aws.String("snap-00fedcba98765432f"), State: ec2types.SnapshotStateCompleted, VolumeSize: &sz},
			},
		},
	}
	if err := ValidateSnapshotsAWS(context.Background(), mock, p); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidateSnapshotsAWS_NotFound(t *testing.T) {
	p := &SandboxProfile{}
	p.Spec.Runtime.AdditionalSnapshots = []AdditionalSnapshotSpec{
		{SnapshotID: "snap-deadbeefdeadbeef0", MountPoint: "/data"},
	}
	mock := &mockEC2SnapshotAPI{
		err: &smithy.GenericAPIError{Code: "InvalidSnapshot.NotFound", Message: "The snapshot 'snap-deadbeefdeadbeef0' does not exist."},
	}
	err := ValidateSnapshotsAWS(context.Background(), mock, p)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"snap-deadbeefdeadbeef0", "region", "shared", "deleted"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing hint %q", msg, want)
		}
	}
}

func TestValidateSnapshotsAWS_PendingState(t *testing.T) {
	p := &SandboxProfile{}
	p.Spec.Runtime.AdditionalSnapshots = []AdditionalSnapshotSpec{
		{SnapshotID: "snap-0123456789abcdef0", MountPoint: "/data"},
	}
	sz := int32(10)
	mock := &mockEC2SnapshotAPI{
		out: &ec2.DescribeSnapshotsOutput{
			Snapshots: []ec2types.Snapshot{
				{SnapshotId: aws.String("snap-0123456789abcdef0"), State: ec2types.SnapshotStatePending, VolumeSize: &sz},
			},
		},
	}
	err := ValidateSnapshotsAWS(context.Background(), mock, p)
	if err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("expected pending error, got %v", err)
	}
}

func TestValidateSnapshotsAWS_SizeOverrideTooSmall(t *testing.T) {
	p := &SandboxProfile{}
	p.Spec.Runtime.AdditionalSnapshots = []AdditionalSnapshotSpec{
		{SnapshotID: "snap-0123456789abcdef0", MountPoint: "/data", Size: 5},
	}
	snapSize := int32(50)
	mock := &mockEC2SnapshotAPI{
		out: &ec2.DescribeSnapshotsOutput{
			Snapshots: []ec2types.Snapshot{
				{SnapshotId: aws.String("snap-0123456789abcdef0"), State: ec2types.SnapshotStateCompleted, VolumeSize: &snapSize},
			},
		},
	}
	err := ValidateSnapshotsAWS(context.Background(), mock, p)
	if err == nil {
		t.Fatal("expected size-too-small error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "5") || !strings.Contains(msg, "50") {
		t.Errorf("error %q must contain BOTH 5 and 50", msg)
	}
}

func TestValidateSnapshotsAWS_IAMMissingWarnAndSkip(t *testing.T) {
	// Per 87-VALIDATION.md aliasing-risk SNAP-03:
	// MUST use smithy.GenericAPIError with Code "UnauthorizedOperation" (EC2-specific).
	p := &SandboxProfile{}
	p.Spec.Runtime.AdditionalSnapshots = []AdditionalSnapshotSpec{
		{SnapshotID: "snap-0123456789abcdef0", MountPoint: "/data"},
	}
	mock := &mockEC2SnapshotAPI{
		err: &smithy.GenericAPIError{Code: "UnauthorizedOperation", Message: "You are not authorized to perform: ec2:DescribeSnapshots"},
	}
	if err := ValidateSnapshotsAWS(context.Background(), mock, p); err != nil {
		t.Fatalf("expected nil (graceful degradation), got %v", err)
	}
}

func TestValidateSnapshotsAWS_AccessDeniedDoesFail(t *testing.T) {
	// Counter-test: AccessDenied is NOT the UnauthorizedOperation code; must surface as error.
	p := &SandboxProfile{}
	p.Spec.Runtime.AdditionalSnapshots = []AdditionalSnapshotSpec{
		{SnapshotID: "snap-0123456789abcdef0", MountPoint: "/data"},
	}
	mock := &mockEC2SnapshotAPI{
		err: &smithy.GenericAPIError{Code: "AccessDenied", Message: "Access Denied"},
	}
	if err := ValidateSnapshotsAWS(context.Background(), mock, p); err == nil {
		t.Fatal("expected error for AccessDenied (different code path from UnauthorizedOperation)")
	}
}

func TestValidateSnapshotsAWS_EmptySnapshotsIsNoOp(t *testing.T) {
	p := &SandboxProfile{} // no additionalSnapshots
	mock := &mockEC2SnapshotAPI{}
	if err := ValidateSnapshotsAWS(context.Background(), mock, p); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if mock.calls != 0 {
		t.Errorf("DescribeSnapshots called %d times, expected 0 (early return)", mock.calls)
	}
}
