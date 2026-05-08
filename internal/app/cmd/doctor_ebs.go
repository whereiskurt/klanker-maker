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

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// EC2VolumeAPI covers EC2 DescribeVolumes / DescribeSnapshots / DescribeImages
// for orphaned EBS resource detection. The real ec2.Client implements all three.
// DescribeImages is needed by the snapshot check to map snapshot IDs back to
// the AMIs that reference them.
type EC2VolumeAPI interface {
	DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
	DescribeSnapshots(ctx context.Context, params *ec2.DescribeSnapshotsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error)
	DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
}

// checkOrphanedEBSVolumes lists EBS volumes tagged km:sandbox-id and warns on
// any whose sandbox-id has no matching DynamoDB record. Report-only.
func checkOrphanedEBSVolumes(ctx context.Context, ec2Client EC2VolumeAPI, lister SandboxLister) CheckResult {
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

	activeSandboxes := make(map[string]bool)
	if lister != nil {
		records, err := lister.ListSandboxes(ctx, false)
		if err == nil {
			for _, r := range records {
				activeSandboxes[r.SandboxID] = true
			}
		}
	}

	type orphan struct {
		volumeID  string
		sandboxID string
		sizeGB    int32
		state     string
		az        string
	}
	var orphans []orphan
	totalOrphanGB := int32(0)
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
		size := awssdk.ToInt32(v.Size)
		orphans = append(orphans, orphan{
			volumeID:  awssdk.ToString(v.VolumeId),
			sandboxID: sandboxID,
			sizeGB:    size,
			state:     string(v.State),
			az:        awssdk.ToString(v.AvailabilityZone),
		})
		totalOrphanGB += size
	}

	if len(orphans) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("%d km-tagged volumes, all registered in DynamoDB", len(volumes)),
		}
	}

	var sb strings.Builder
	// gp3 EBS list price ~$0.08/GB-month in us-east-1; close enough for an advisory cost cue.
	monthlyUSD := float64(totalOrphanGB) * 0.08
	fmt.Fprintf(&sb, "found %d orphaned EBS volumes (no DynamoDB record) — %d GB total ≈ $%.2f/mo:", len(orphans), totalOrphanGB, monthlyUSD)
	for _, o := range orphans {
		fmt.Fprintf(&sb, "\n  %s (%s, %dGB, %s) %s", o.volumeID, o.state, o.sizeGB, o.az, o.sandboxID)
	}
	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     sb.String(),
		Remediation: "If the sandbox row exists: 'km destroy <sandbox-id> --remote --yes'. Otherwise: 'aws ec2 delete-volume --volume-id <id>' (after confirming the volume is detached).",
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
