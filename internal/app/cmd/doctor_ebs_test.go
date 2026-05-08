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

	// DeleteVolume bookkeeping. deleteErr, when set, is returned for any
	// volume ID present in deleteErrFor (or for all IDs when deleteErrFor is
	// nil). Test code can inspect deletedIDs to assert which volumes were
	// actually targeted.
	deleteErr     error
	deleteErrFor  map[string]bool
	deletedIDs    []string
	deleteCallIDs []string
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

func (f *fakeEC2VolumeClient) DeleteVolume(_ context.Context, params *ec2.DeleteVolumeInput, _ ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error) {
	id := awssdk.ToString(params.VolumeId)
	f.deleteCallIDs = append(f.deleteCallIDs, id)
	if f.deleteErr != nil && (f.deleteErrFor == nil || f.deleteErrFor[id]) {
		return nil, f.deleteErr
	}
	f.deletedIDs = append(f.deletedIDs, id)
	return &ec2.DeleteVolumeOutput{}, nil
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
	r := checkOrphanedEBSVolumes(context.Background(), nil, nil, true, false)
	if r.Status != CheckSkipped {
		t.Fatalf("expected CheckSkipped for nil client, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckOrphanedEBSVolumes_NoVolumes_OK(t *testing.T) {
	client := &fakeEC2VolumeClient{volumes: nil}
	r := checkOrphanedEBSVolumes(context.Background(), client, nil, true, false)
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
	r := checkOrphanedEBSVolumes(context.Background(), client, lister, true, false)
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
	r := checkOrphanedEBSVolumes(context.Background(), client, lister, true, false)
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
	r := checkOrphanedEBSVolumes(context.Background(), client, &fakeSandboxLister{}, true, false)
	if r.Status != CheckOK {
		t.Errorf("expected CheckOK for untagged volume, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckOrphanedEBSVolumes_DescribeVolumesError_Warn(t *testing.T) {
	client := &fakeEC2VolumeClient{volumesErr: errors.New("AccessDenied")}
	r := checkOrphanedEBSVolumes(context.Background(), client, nil, true, false)
	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn on DescribeVolumes error, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "AccessDenied") {
		t.Errorf("expected error text in message, got: %s", r.Message)
	}
}

// TestCheckOrphanedEBSVolumes_DeleteEBS_DeletesAvailableOnly verifies that
// when --dry-run=false --delete-ebs is set, only volumes whose state is
// "available" are deleted; in-use orphans are reported but never touched.
// This is the core safety property: in-use volumes are still attached to
// some EC2 instance (which itself may also be orphaned), and deleting them
// would force-detach mid-IO. The fix is to terminate the owning instance,
// not blow up the volume.
func TestCheckOrphanedEBSVolumes_DeleteEBS_DeletesAvailableOnly(t *testing.T) {
	client := &fakeEC2VolumeClient{volumes: []ec2types.Volume{
		makeVolume("vol-detached1", "sb-ghost1", "available", "us-east-1a", 30),
		makeVolume("vol-detached2", "sb-ghost2", "available", "us-east-1b", 50),
		makeVolume("vol-attached", "sb-ghost3", "in-use", "us-east-1a", 100),
	}}
	r := checkOrphanedEBSVolumes(context.Background(), client, &fakeSandboxLister{}, false, true)

	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn after delete pass, got %s: %s", r.Status, r.Message)
	}
	// Only the two available volumes should have had DeleteVolume invoked.
	if len(client.deleteCallIDs) != 2 {
		t.Errorf("expected 2 DeleteVolume calls (only available state), got %d: %v", len(client.deleteCallIDs), client.deleteCallIDs)
	}
	for _, id := range client.deleteCallIDs {
		if id == "vol-attached" {
			t.Errorf("DeleteVolume must NOT be called for in-use volume %q", id)
		}
	}
	if len(client.deletedIDs) != 2 {
		t.Errorf("expected 2 successful deletes, got %d: %v", len(client.deletedIDs), client.deletedIDs)
	}
	if !strings.Contains(r.Message, "deleted 2") {
		t.Errorf("expected 'deleted 2' in message, got: %s", r.Message)
	}
	if !strings.Contains(r.Message, "in-use kept") {
		t.Errorf("expected 'in-use kept' note in message when an in-use orphan exists, got: %s", r.Message)
	}
	if !strings.Contains(r.Message, "[skipped — in-use]") {
		t.Errorf("expected '[skipped — in-use]' marker on the in-use volume row, got: %s", r.Message)
	}
}

// TestCheckOrphanedEBSVolumes_DryRun_NoDelete verifies that --delete-ebs
// without --dry-run=false (i.e. dry-run is still on, the default) is a
// no-op. The opt-in flag must be paired with --dry-run=false to actually
// delete; this matches the convention used by --dry-run for other checks.
func TestCheckOrphanedEBSVolumes_DryRun_NoDelete(t *testing.T) {
	client := &fakeEC2VolumeClient{volumes: []ec2types.Volume{
		makeVolume("vol-orphan", "sb-ghost", "available", "us-east-1a", 30),
	}}
	r := checkOrphanedEBSVolumes(context.Background(), client, &fakeSandboxLister{}, true, true)

	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn, got %s: %s", r.Status, r.Message)
	}
	if len(client.deleteCallIDs) != 0 {
		t.Errorf("DeleteVolume must NOT be called in dry-run mode, got: %v", client.deleteCallIDs)
	}
	if !strings.Contains(r.Remediation, "--dry-run=false --delete-ebs") {
		t.Errorf("expected remediation to point at --dry-run=false --delete-ebs, got: %s", r.Remediation)
	}
}

// TestCheckOrphanedEBSVolumes_DryRunFalseWithoutDeleteEBS_NoDelete verifies
// the "explicit opt-in" property: --dry-run=false alone is NOT enough to
// delete EBS volumes — the operator must also pass --delete-ebs. This makes
// EBS deletion strictly more conservative than KMS/IAM/schedule cleanup,
// which all activate on --dry-run=false alone. Volume contents are user
// data; an extra flag prevents accidental deletion when an operator runs
// --dry-run=false to clean up safer resource types.
func TestCheckOrphanedEBSVolumes_DryRunFalseWithoutDeleteEBS_NoDelete(t *testing.T) {
	client := &fakeEC2VolumeClient{volumes: []ec2types.Volume{
		makeVolume("vol-orphan", "sb-ghost", "available", "us-east-1a", 30),
	}}
	r := checkOrphanedEBSVolumes(context.Background(), client, &fakeSandboxLister{}, false, false)

	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn, got %s: %s", r.Status, r.Message)
	}
	if len(client.deleteCallIDs) != 0 {
		t.Errorf("DeleteVolume must NOT be called without --delete-ebs, got: %v", client.deleteCallIDs)
	}
	if !strings.Contains(r.Remediation, "--delete-ebs") {
		t.Errorf("expected remediation to mention --delete-ebs, got: %s", r.Remediation)
	}
}

// TestCheckOrphanedEBSVolumes_DeleteVolumeError_ReportsFailure verifies that
// a DeleteVolume API failure is recorded in the failed-count and surfaced
// inline against the failing volume row. The volume is NOT counted as
// deleted — successful deletes and failures are tallied separately so the
// operator can re-run and see what's left.
func TestCheckOrphanedEBSVolumes_DeleteVolumeError_ReportsFailure(t *testing.T) {
	client := &fakeEC2VolumeClient{
		volumes: []ec2types.Volume{
			makeVolume("vol-ok", "sb-ghost1", "available", "us-east-1a", 10),
			makeVolume("vol-fail", "sb-ghost2", "available", "us-east-1b", 20),
		},
		deleteErr:    errors.New("VolumeInUse: not actually available"),
		deleteErrFor: map[string]bool{"vol-fail": true},
	}
	r := checkOrphanedEBSVolumes(context.Background(), client, &fakeSandboxLister{}, false, true)

	if r.Status != CheckWarn {
		t.Fatalf("expected CheckWarn, got %s: %s", r.Status, r.Message)
	}
	if len(client.deletedIDs) != 1 || client.deletedIDs[0] != "vol-ok" {
		t.Errorf("expected vol-ok to be the only successful delete, got: %v", client.deletedIDs)
	}
	if !strings.Contains(r.Message, "deleted 1, failed 1") {
		t.Errorf("expected 'deleted 1, failed 1' summary, got: %s", r.Message)
	}
	if !strings.Contains(r.Message, "delete failed: VolumeInUse") {
		t.Errorf("expected per-row failure marker on vol-fail, got: %s", r.Message)
	}
}

// TestCheckOrphanedEBSVolumes_DeleteEBS_AllSucceed_RemediationCleared
// verifies that when every detached orphan is successfully deleted and no
// in-use orphans remain, the Remediation field is cleared — there is
// nothing actionable left to communicate. This matches the convention from
// other doctor checks where successful cleanup downgrades the message.
func TestCheckOrphanedEBSVolumes_DeleteEBS_AllSucceed_RemediationCleared(t *testing.T) {
	client := &fakeEC2VolumeClient{volumes: []ec2types.Volume{
		makeVolume("vol-1", "sb-ghost1", "available", "us-east-1a", 10),
		makeVolume("vol-2", "sb-ghost2", "available", "us-east-1b", 20),
	}}
	r := checkOrphanedEBSVolumes(context.Background(), client, &fakeSandboxLister{}, false, true)

	if r.Remediation != "" {
		t.Errorf("expected empty Remediation after clean delete, got: %s", r.Remediation)
	}
	if !strings.Contains(r.Message, "deleted 2, failed 0") {
		t.Errorf("expected 'deleted 2, failed 0' summary, got: %s", r.Message)
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
