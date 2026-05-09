// Package cmd — orphaned-EBS detection for `km doctor`.
//
// This file adds two checks that complement the existing checkOrphanedEC2:
//
//   - checkOrphanedEBSVolumes — finds aws_ebs_volume.additional volumes tagged
//     km:sandbox-id whose sandbox-id has no DynamoDB record. Catches the
//     additionalVolume leak path: profiles that declare spec.execution.additionalVolume
//     create a standalone EBS resource with delete_on_termination NOT applicable
//     (it's a separate aws_ebs_volume, not a BlockDeviceMapping). If the EC2
//     instance is terminated out-of-band (manual aws ec2 terminate-instances,
//     broken Terragrunt state, region failover) the volume orphans at
//     ~$0.08/GB-month indefinitely.
//
//   - checkOrphanedSnapshots — finds self-owned EBS snapshots tagged
//     km:sandbox-id that are NOT referenced by any AMI's BlockDeviceMappings.
//     Catches the "manual aws ec2 deregister-image" leak path: km ami delete
//     deletes snapshots atomically, but a manual deregister leaves snapshots
//     dangling.
//
// Both checks are report-only — they never delete resources. Operator runs
// `aws ec2 delete-volume` / `aws ec2 delete-snapshot` (or, for the volume
// case, `km destroy <sandbox-id> --remote --yes` if the sandbox row exists).
package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// EC2VolumeAPI covers EC2 DescribeVolumes / DescribeSnapshots / DescribeImages /
// DeleteVolume for orphaned EBS resource detection and (optional) cleanup.
// The real ec2.Client implements all four. DescribeImages is needed by the
// snapshot check to map snapshot IDs back to the AMIs that reference them.
// DeleteVolume is only invoked when both --dry-run=false and --delete-ebs
// are set on `km doctor`, and only against volumes whose state is "available".
type EC2VolumeAPI interface {
	DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
	DescribeSnapshots(ctx context.Context, params *ec2.DescribeSnapshotsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error)
	DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
	DeleteVolume(ctx context.Context, params *ec2.DeleteVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error)
}

// checkOrphanedEBSVolumes lists EBS volumes tagged km:sandbox-id and warns on
// any whose sandbox-id has no matching DynamoDB record.
//
// When both dryRun is false and deleteEBS is true, volumes whose state is
// "available" (detached) are deleted via DeleteVolume; volumes in any other
// state (in-use, creating, deleting, error) are reported but never touched —
// in-use orphans need their owning EC2 instance terminated first, which is
// the operator's call (km destroy or manual EC2 termination).
//
// Without deleteEBS, the check stays report-only even when --dry-run=false
// is set globally. Volume data is user data; deletion is irreversible, so
// it is gated on an explicit opt-in flag.
func checkOrphanedEBSVolumes(ctx context.Context, ec2Client EC2VolumeAPI, lister SandboxLister, dryRun, deleteEBS bool) CheckResult {
	name := "Orphaned EBS Volumes"
	if ec2Client == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "EC2 volume client not available"}
	}

	var volumes []ec2types.Volume
	var nextToken *string
	for {
		out, err := ec2Client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
			Filters: []ec2types.Filter{
				{Name: awssdk.String("tag:km:sandbox-id"), Values: []string{"*"}},
			},
			NextToken: nextToken,
		})
		if err != nil {
			return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not describe EBS volumes: %v", err)}
		}
		volumes = append(volumes, out.Volumes...)
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	if len(volumes) == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: "no km-tagged EBS volumes found"}
	}

	if lister == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "sandbox lister not available (state bucket not configured)"}
	}
	records, err := lister.ListSandboxes(ctx, false)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list sandboxes: %v", err)}
	}
	activeSandboxes := make(map[string]bool)
	for _, r := range records {
		activeSandboxes[r.SandboxID] = true
	}

	// Skip volumes created in the last 10 minutes — they are likely in the
	// race window between EC2 CreateVolume and km create's DDB row write.
	// Cleaning those would orphan a real in-flight sandbox.
	provisioningCutoff := time.Now().Add(-10 * time.Minute)
	type orphan struct {
		volumeID  string
		sandboxID string
		sizeGB    int32
		state     string
		az        string
	}
	var orphans []orphan
	totalOrphanGB := int32(0)
	detachedCount := 0
	for _, v := range volumes {
		var sandboxID string
		for _, tag := range v.Tags {
			if awssdk.ToString(tag.Key) == "km:sandbox-id" {
				sandboxID = awssdk.ToString(tag.Value)
				break
			}
		}
		if sandboxID == "" || activeSandboxes[sandboxID] {
			continue
		}
		if v.CreateTime != nil && v.CreateTime.After(provisioningCutoff) {
			continue
		}
		size := awssdk.ToInt32(v.Size)
		state := string(v.State)
		orphans = append(orphans, orphan{
			volumeID:  awssdk.ToString(v.VolumeId),
			sandboxID: sandboxID,
			sizeGB:    size,
			state:     state,
			az:        awssdk.ToString(v.AvailabilityZone),
		})
		totalOrphanGB += size
		if state == string(ec2types.VolumeStateAvailable) {
			detachedCount++
		}
	}

	if len(orphans) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("%d km-tagged volumes, all registered in DynamoDB", len(volumes)),
		}
	}

	// Decide whether to actually delete. Delete only the detached subset; in-use
	// volumes need their owning instance terminated first.
	performDelete := !dryRun && deleteEBS
	deleted := 0
	failures := make(map[string]error)
	if performDelete {
		for _, o := range orphans {
			if o.state != string(ec2types.VolumeStateAvailable) {
				continue
			}
			_, err := ec2Client.DeleteVolume(ctx, &ec2.DeleteVolumeInput{
				VolumeId: awssdk.String(o.volumeID),
			})
			if err != nil {
				failures[o.volumeID] = err
				continue
			}
			deleted++
		}
	}

	// gp3 EBS list price ~$0.08/GB-month in us-east-1; close enough for an advisory cost cue.
	monthlyUSD := float64(totalOrphanGB) * 0.08
	inUseCount := len(orphans) - detachedCount

	var sb strings.Builder
	fmt.Fprintf(&sb, "found %d orphaned EBS volumes (no DynamoDB record) — %d GB total ≈ $%.2f/mo (%d detached, %d in-use)",
		len(orphans), totalOrphanGB, monthlyUSD, detachedCount, inUseCount)
	if performDelete {
		fmt.Fprintf(&sb, "; deleted %d, failed %d", deleted, len(failures))
		if inUseCount > 0 {
			fmt.Fprintf(&sb, " (%d in-use kept — terminate owning EC2 first)", inUseCount)
		}
	}
	fmt.Fprint(&sb, ":")
	for _, o := range orphans {
		marker := ""
		if performDelete {
			switch {
			case o.state != string(ec2types.VolumeStateAvailable):
				marker = " [skipped — in-use]"
			case failures[o.volumeID] != nil:
				marker = fmt.Sprintf(" [delete failed: %v]", failures[o.volumeID])
			default:
				marker = " [deleted]"
			}
		}
		fmt.Fprintf(&sb, "\n  %s (%s, %dGB, %s) %s%s", o.volumeID, o.state, o.sizeGB, o.az, o.sandboxID, marker)
	}

	remediation := "If the sandbox row exists: 'km destroy <sandbox-id> --remote --yes'. Otherwise: 'aws ec2 delete-volume --volume-id <id>' (after confirming the volume is detached)."
	switch {
	case dryRun && deleteEBS:
		// --delete-ebs is a no-op when --dry-run is still on (the default). Tell
		// the operator how to actually perform the delete.
		remediation = fmt.Sprintf("Re-run with --dry-run=false --delete-ebs to delete the %d detached volume(s); %d in-use volume(s) need 'km destroy <sandbox-id>' or manual EC2 termination first.", detachedCount, inUseCount)
	case !dryRun && deleteEBS:
		// Action was already taken; downgrade remediation.
		if len(failures) == 0 && inUseCount == 0 {
			remediation = ""
		} else {
			remediation = "Re-run after detaching/terminating the owning EC2 instances to clean up the remaining in-use volumes."
		}
	case !dryRun && !deleteEBS:
		// Operator opted into other cleanups but not EBS — explicit opt-in is required for EBS.
		remediation = fmt.Sprintf("Add --delete-ebs to also delete the %d detached orphan volume(s); in-use volumes still require 'km destroy <sandbox-id>' or manual EC2 termination.", detachedCount)
	}

	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     sb.String(),
		Remediation: remediation,
	}
}

// checkUntaggedAvailableVolumes finds EBS volumes that are:
//   - state: available (not attached to any instance)
//   - tagged km_label={resourcePrefix} (Terragrunt default tag, proves km ownership)
//   - NOT tagged km:sandbox-id (fell through the module tag gap)
//
// This catches root volumes from spot instance requests that were created before
// volume_tags was added to the ec2spot module, as well as any future leak where
// km:sandbox-id is missing. Because we have no sandbox-id to cross-reference
// against DynamoDB, this check is report-only — the operator must inspect and
// delete manually, or re-run after the module fix is deployed.
//
// Volumes created within the last 10 minutes are excluded to avoid false
// positives during active sandbox provisioning (create → attach races).
func checkUntaggedAvailableVolumes(ctx context.Context, ec2Client EC2VolumeAPI, resourcePrefix string) CheckResult {
	name := "Untagged Available EBS Volumes"
	if ec2Client == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "EC2 volume client not available"}
	}

	var volumes []ec2types.Volume
	var nextToken *string
	for {
		out, err := ec2Client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
			Filters: []ec2types.Filter{
				{Name: awssdk.String("status"), Values: []string{"available"}},
				{Name: awssdk.String("tag:km_label"), Values: []string{resourcePrefix}},
			},
			NextToken: nextToken,
		})
		if err != nil {
			return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not describe EBS volumes: %v", err)}
		}
		volumes = append(volumes, out.Volumes...)
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	provisioningCutoff := time.Now().Add(-10 * time.Minute)

	type stale struct {
		volumeID  string
		sizeGB    int32
		az        string
		createdAt string
	}
	var found []stale
	totalGB := int32(0)
	for _, v := range volumes {
		hasSandboxID := false
		for _, tag := range v.Tags {
			if awssdk.ToString(tag.Key) == "km:sandbox-id" {
				hasSandboxID = true
				break
			}
		}
		if hasSandboxID {
			continue
		}
		if v.CreateTime != nil && v.CreateTime.After(provisioningCutoff) {
			continue
		}
		size := awssdk.ToInt32(v.Size)
		created := ""
		if v.CreateTime != nil {
			created = v.CreateTime.Format("2006-01-02T15:04Z")
		}
		found = append(found, stale{
			volumeID:  awssdk.ToString(v.VolumeId),
			sizeGB:    size,
			az:        awssdk.ToString(v.AvailabilityZone),
			createdAt: created,
		})
		totalGB += size
	}

	if len(found) == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: "no untagged available km-labeled EBS volumes"}
	}

	monthlyUSD := float64(totalGB) * 0.08
	var sb strings.Builder
	fmt.Fprintf(&sb, "found %d available EBS volumes with km_label=%s but no km:sandbox-id tag — %d GB total ≈ $%.2f/mo:",
		len(found), resourcePrefix, totalGB, monthlyUSD)
	for _, v := range found {
		fmt.Fprintf(&sb, "\n  %s (%dGB, %s, created %s)", v.volumeID, v.sizeGB, v.az, v.createdAt)
	}

	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     sb.String(),
		Remediation: "These volumes have no sandbox-id tag; they are likely root volumes from destroyed spot instances. Verify the instance they belonged to is gone, then: 'aws ec2 delete-volume --volume-id <id>'. Going forward, the ec2spot module now sets volume_tags so new root volumes will carry km:sandbox-id and be caught by the Orphaned EBS Volumes check instead.",
	}
}

// checkOrphanedSnapshots lists self-owned EBS snapshots tagged km:sandbox-id and
// warns on any that are NOT referenced by an AMI's BlockDeviceMappings. Report-only.
//
// Rationale: BakeAMI propagates km:sandbox-id tags to both the AMI and its
// snapshots via TagSpecifications. km ami delete deletes snapshots atomically.
// A manual `aws ec2 deregister-image` leaves the snapshots behind — these are
// what this check surfaces.
func checkOrphanedSnapshots(ctx context.Context, ec2Client EC2VolumeAPI) CheckResult {
	name := "Orphaned EBS Snapshots"
	if ec2Client == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "EC2 volume client not available"}
	}

	// 1. List km-tagged snapshots owned by self.
	var snaps []ec2types.Snapshot
	var snapToken *string
	for {
		out, err := ec2Client.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
			OwnerIds: []string{"self"},
			Filters: []ec2types.Filter{
				{Name: awssdk.String("tag:km:sandbox-id"), Values: []string{"*"}},
			},
			NextToken: snapToken,
		})
		if err != nil {
			return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not describe EBS snapshots: %v", err)}
		}
		snaps = append(snaps, out.Snapshots...)
		if out.NextToken == nil {
			break
		}
		snapToken = out.NextToken
	}

	if len(snaps) == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: "no km-tagged EBS snapshots found"}
	}

	// 2. Build set of snapshot IDs still referenced by an AMI's BlockDeviceMappings.
	referenced := make(map[string]bool)
	imgs, err := ec2Client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners: []string{"self"},
	})
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not describe AMIs to cross-reference: %v", err)}
	}
	for _, img := range imgs.Images {
		for _, bdm := range img.BlockDeviceMappings {
			if bdm.Ebs != nil && bdm.Ebs.SnapshotId != nil {
				referenced[awssdk.ToString(bdm.Ebs.SnapshotId)] = true
			}
		}
	}

	// 3. An orphan = km-tagged snapshot NOT referenced by any AMI.
	type orphan struct {
		snapID    string
		sandboxID string
		sizeGB    int32
		startTime string
	}
	var orphans []orphan
	totalOrphanGB := int32(0)
	for _, s := range snaps {
		sid := awssdk.ToString(s.SnapshotId)
		if referenced[sid] {
			continue
		}
		var sandboxID string
		for _, tag := range s.Tags {
			if awssdk.ToString(tag.Key) == "km:sandbox-id" {
				sandboxID = awssdk.ToString(tag.Value)
				break
			}
		}
		started := ""
		if s.StartTime != nil {
			started = s.StartTime.Format("2006-01-02")
		}
		size := awssdk.ToInt32(s.VolumeSize)
		orphans = append(orphans, orphan{
			snapID:    sid,
			sandboxID: sandboxID,
			sizeGB:    size,
			startTime: started,
		})
		totalOrphanGB += size
	}

	if len(orphans) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("%d km-tagged snapshots, all referenced by an AMI", len(snaps)),
		}
	}

	var sb strings.Builder
	// EBS snapshot pricing ~$0.05/GB-month for standard tier.
	monthlyUSD := float64(totalOrphanGB) * 0.05
	fmt.Fprintf(&sb, "found %d orphaned EBS snapshots (no AMI reference) — %d GB total ≈ $%.2f/mo:", len(orphans), totalOrphanGB, monthlyUSD)
	for _, o := range orphans {
		fmt.Fprintf(&sb, "\n  %s (%dGB, %s) %s", o.snapID, o.sizeGB, o.startTime, o.sandboxID)
	}
	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     sb.String(),
		Remediation: "Confirm no AMI you care about references these, then 'aws ec2 delete-snapshot --snapshot-id <id>'. Or use 'km ami delete <ami-id>' going forward — it cleans up snapshots atomically.",
	}
}
