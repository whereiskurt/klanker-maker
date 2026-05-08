package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// =============================================================================
// Mock EC2 volume API + sandbox lister
// =============================================================================

type fakeEC2VolumeClient struct {
	volumes      []ec2types.Volume
	snapshots    []ec2types.Snapshot
	images       []ec2types.Image
	volumesErr   error
	snapshotsErr error
	imagesErr    error
}

func (f *fakeEC2VolumeClient) DescribeVolumes(_ context.Context, _ *ec2.DescribeVolumesInput, _ ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	if f.volumesErr != nil {
		return nil, f.volumesErr
	}
	return &ec2.DescribeVolumesOutput{Volumes: f.volumes}, nil
}

func (f *fakeEC2VolumeClient) DescribeSnapshots(_ context.Context, _ *ec2.DescribeSnapshotsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error) {
	if f.snapshotsErr != nil {
		return nil, f.snapshotsErr
	}
	return &ec2.DescribeSnapshotsOutput{Snapshots: f.snapshots}, nil
}

func (f *fakeEC2VolumeClient) DescribeImages(_ context.Context, _ *ec2.DescribeImagesInput, _ ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	if f.imagesErr != nil {
		return nil, f.imagesErr
	}
	return &ec2.DescribeImagesOutput{Images: f.images}, nil
}

type fakeSandboxLister struct {
	records []kmaws.SandboxRecord
	err     error
}

func (f *fakeSandboxLister) ListSandboxes(_ context.Context, _ bool) ([]kmaws.SandboxRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.records, nil
}

func makeVolume(id, sandboxID, state, az string, sizeGB int32) ec2types.Volume {
	v := ec2types.Volume{
		VolumeId:         awssdk.String(id),
		Size:             awssdk.Int32(sizeGB),
		State:            ec2types.VolumeState(state),
		AvailabilityZone: awssdk.String(az),
	}
	if sandboxID != "" {
		v.Tags = []ec2types.Tag{
			{Key: awssdk.String("km:sandbox-id"), Value: awssdk.String(sandboxID)},
		}
	}
	return v
}

func makeSnapshot(id, sandboxID string, sizeGB int32) ec2types.Snapshot {
	now := time.Now()
	s := ec2types.Snapshot{
		SnapshotId: awssdk.String(id),
		VolumeSize: awssdk.Int32(sizeGB),
		StartTime:  &now,
	}
	if sandboxID != "" {
		s.Tags = []ec2types.Tag{
			{Key: awssdk.String("km:sandbox-id"), Value: awssdk.String(sandboxID)},
		}
	}
	return s
}

func makeAMIWithSnapshot(amiID, snapID string) ec2types.Image {
	return ec2types.Image{
		ImageId: awssdk.String(amiID),
		BlockDeviceMappings: []ec2types.BlockDeviceMapping{
			{Ebs: &ec2types.EbsBlockDevice{SnapshotId: awssdk.String(snapID)}},
		},
	}
}

// =============================================================================
// checkOrphanedEBSVolumes
// =============================================================================

func TestCheckOrphanedEBSVolumes_NilClient_Skipped(t *testing.T) {
	r := checkOrphanedEBSVolumes(context.Background(), nil, nil)
	if r.Status != CheckSkipped {
		t.Fatalf("expected CheckSkipped for nil client, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckOrphanedEBSVolumes_NoVolumes_OK(t *testing.T) {
	client := &fakeEC2VolumeClient{volumes: nil}
	r := checkOrphanedEBSVolumes(context.Background(), client, nil)
	if r.Status != CheckOK {
		t.Fatalf("expected CheckOK with no volumes, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckOrphanedEBSVolumes_AllVolumesActive_OK(t *testing.T) {
	client := &fakeEC2VolumeClient{volumes: []ec2types.Volume{
		makeVolume("vol-aaa", "sb-alive1", "in-use", "us-east-1a", 20),
		makeVolume("vol-bbb", "sb-alive2", "available", "us-east-1b", 50),
	}}
	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{
		{SandboxID: "sb-alive1"},
		{SandboxID: "sb-alive2"},
	}}
	r := checkOrphanedEBSVolumes(context.Background(), client, lister)
	if r.Status != CheckOK {
		t.Fatalf("expected CheckOK when every volume is registered, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckOrphanedEBSVolumes_OrphanFound_Warn(t *testing.T) {
	client := &fakeEC2VolumeClient{volumes: []ec2types.Volume{
		makeVolume("vol-alive", "sb-alive", "in-use", "us-east-1a", 20),
		makeVolume("vol-orphan1", "sb-ghost1", "available", "us-east-1a", 30),
		makeVolume("vol-orphan2", "sb-ghost2", "available", "us-east-1b", 100),
	}}
	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{{SandboxID: "sb-alive"}}}
	r := checkOrphanedEBSVolumes(context.Background(), client, lister)
	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn when orphans found, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "vol-orphan1") || !strings.Contains(r.Message, "vol-orphan2") {
		t.Errorf("expected both orphan volume IDs in message, got: %s", r.Message)
	}
	if strings.Contains(r.Message, "vol-alive") {
		t.Errorf("active volume vol-alive must NOT appear in orphan list, got: %s", r.Message)
	}
	// 30 + 100 = 130 GB total, expect "130 GB" surfaced.
	if !strings.Contains(r.Message, "130 GB") {
		t.Errorf("expected total orphan GB '130 GB' in message, got: %s", r.Message)
	}
	if r.Remediation == "" {
		t.Errorf("expected non-empty Remediation for orphaned volumes")
	}
}

func TestCheckOrphanedEBSVolumes_VolumeWithoutTag_Skipped(t *testing.T) {
	// A volume with no km:sandbox-id tag should never be flagged (filtered upstream
	// by the API filter, but defensive check on tag-less rows belt-and-suspenders).
	client := &fakeEC2VolumeClient{volumes: []ec2types.Volume{
		makeVolume("vol-untagged", "", "in-use", "us-east-1a", 10),
	}}
	r := checkOrphanedEBSVolumes(context.Background(), client, &fakeSandboxLister{})
	if r.Status != CheckOK {
		t.Errorf("expected CheckOK for untagged volume, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckOrphanedEBSVolumes_DescribeVolumesError_Warn(t *testing.T) {
	client := &fakeEC2VolumeClient{volumesErr: errors.New("AccessDenied")}
	r := checkOrphanedEBSVolumes(context.Background(), client, nil)
	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn on DescribeVolumes error, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "AccessDenied") {
		t.Errorf("expected error text in message, got: %s", r.Message)
	}
}

// =============================================================================
// checkOrphanedSnapshots
// =============================================================================

func TestCheckOrphanedSnapshots_NilClient_Skipped(t *testing.T) {
	r := checkOrphanedSnapshots(context.Background(), nil)
	if r.Status != CheckSkipped {
		t.Fatalf("expected CheckSkipped for nil client, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckOrphanedSnapshots_NoSnapshots_OK(t *testing.T) {
	client := &fakeEC2VolumeClient{snapshots: nil}
	r := checkOrphanedSnapshots(context.Background(), client)
	if r.Status != CheckOK {
		t.Fatalf("expected CheckOK with no snapshots, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckOrphanedSnapshots_AllReferenced_OK(t *testing.T) {
	client := &fakeEC2VolumeClient{
		snapshots: []ec2types.Snapshot{
			makeSnapshot("snap-aaa", "sb-1", 8),
			makeSnapshot("snap-bbb", "sb-2", 16),
		},
		images: []ec2types.Image{
			makeAMIWithSnapshot("ami-1", "snap-aaa"),
			makeAMIWithSnapshot("ami-2", "snap-bbb"),
		},
	}
	r := checkOrphanedSnapshots(context.Background(), client)
	if r.Status != CheckOK {
		t.Fatalf("expected CheckOK when every snapshot is AMI-referenced, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckOrphanedSnapshots_OrphanFound_Warn(t *testing.T) {
	client := &fakeEC2VolumeClient{
		snapshots: []ec2types.Snapshot{
			makeSnapshot("snap-live", "sb-1", 8),
			makeSnapshot("snap-orphan1", "sb-ghost1", 16),
			makeSnapshot("snap-orphan2", "sb-ghost2", 32),
		},
		images: []ec2types.Image{
			makeAMIWithSnapshot("ami-1", "snap-live"),
		},
	}
	r := checkOrphanedSnapshots(context.Background(), client)
	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn when orphan snapshots found, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "snap-orphan1") || !strings.Contains(r.Message, "snap-orphan2") {
		t.Errorf("expected both orphan snapshot IDs in message, got: %s", r.Message)
	}
	if strings.Contains(r.Message, "snap-live") {
		t.Errorf("AMI-referenced snap-live must NOT appear in orphan list, got: %s", r.Message)
	}
	// 16 + 32 = 48 GB, expect surfaced.
	if !strings.Contains(r.Message, "48 GB") {
		t.Errorf("expected total orphan GB '48 GB' in message, got: %s", r.Message)
	}
}

func TestCheckOrphanedSnapshots_DescribeSnapshotsError_Warn(t *testing.T) {
	client := &fakeEC2VolumeClient{snapshotsErr: errors.New("AccessDenied")}
	r := checkOrphanedSnapshots(context.Background(), client)
	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn on DescribeSnapshots error, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckOrphanedSnapshots_DescribeImagesError_Warn(t *testing.T) {
	client := &fakeEC2VolumeClient{
		snapshots: []ec2types.Snapshot{makeSnapshot("snap-aaa", "sb-1", 8)},
		imagesErr: errors.New("Throttling"),
	}
	r := checkOrphanedSnapshots(context.Background(), client)
	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn when DescribeImages cross-ref fails, got %s: %s", r.Status, r.Message)
	}
}
