package profile

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
	"github.com/rs/zerolog/log"
)

// EC2SnapshotAPI is the narrow EC2 interface ValidateSnapshotsAWS depends on.
// Mirrors the DescribeSnapshots signature from internal/app/cmd/doctor_ebs.go:42.
type EC2SnapshotAPI interface {
	DescribeSnapshots(ctx context.Context, params *ec2.DescribeSnapshotsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error)
}

// ValidateSnapshotsAWS runs Layer 2 pre-flight checks (Phase 87 / SNAP-03).
// Returns nil for: (a) empty additionalSnapshots, (b) IAM-missing graceful degradation.
// Returns error for: missing snapshot, pending/error state, size override < snapshot size, AccessDenied/other.
//
// Caller (km create) MUST invoke before compiler runs to guarantee zero terragrunt artifacts on failure.
func ValidateSnapshotsAWS(ctx context.Context, client EC2SnapshotAPI, p *SandboxProfile) error {
	if len(p.Spec.Runtime.AdditionalSnapshots) == 0 {
		return nil
	}

	// Build ordered ID list for batched call (max 11 entries — fits in one DescribeSnapshots call).
	ids := make([]string, len(p.Spec.Runtime.AdditionalSnapshots))
	for i, snap := range p.Spec.Runtime.AdditionalSnapshots {
		ids[i] = snap.SnapshotID
	}

	out, err := client.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
		SnapshotIds: ids,
	})
	if err != nil {
		// Graceful degradation: IAM-missing for EC2 is "UnauthorizedOperation".
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "UnauthorizedOperation" {
				log.Warn().
					Err(err).
					Msg("ec2:DescribeSnapshots not permitted; skipping Phase 87 pre-flight (terragrunt apply is fallback failure path)")
				return nil
			}
			if apiErr.ErrorCode() == "InvalidSnapshot.NotFound" {
				// AWS typically returns: "The snapshot 'snap-XXX' does not exist."
				return fmt.Errorf("snapshot pre-flight failed: %w\n  Hint: the snapshot may be (1) in a different AWS region than the sandbox target, (2) not shared with this AWS account, or (3) deleted", err)
			}
		}
		return fmt.Errorf("ec2:DescribeSnapshots failed: %w", err)
	}

	// Build a lookup by snapshot ID for state + size assertions.
	snapByID := make(map[string]ec2types.Snapshot, len(out.Snapshots))
	for _, s := range out.Snapshots {
		if s.SnapshotId != nil {
			snapByID[*s.SnapshotId] = s
		}
	}

	// Iterate profile entries (preserves user-facing index for error paths).
	for i, entry := range p.Spec.Runtime.AdditionalSnapshots {
		snap, found := snapByID[entry.SnapshotID]
		if !found {
			// AWS already would have returned InvalidSnapshot.NotFound above — this is belt-and-suspenders.
			return fmt.Errorf("spec.runtime.additionalSnapshots[%d]: snapshot %s not returned by DescribeSnapshots", i, entry.SnapshotID)
		}

		// State must be completed.
		if snap.State != ec2types.SnapshotStateCompleted {
			return fmt.Errorf("spec.runtime.additionalSnapshots[%d]: snapshot %s is in state %q (must be %q)",
				i, entry.SnapshotID, snap.State, ec2types.SnapshotStateCompleted)
		}

		// Size override (Layer 2 enforces >= snapshot.VolumeSize when explicitly set).
		if entry.Size > 0 && snap.VolumeSize != nil && int32(entry.Size) < *snap.VolumeSize {
			return fmt.Errorf("spec.runtime.additionalSnapshots[%d]: size %d GiB is smaller than snapshot %s actual size %d GiB — EBS cannot shrink below snapshot size",
				i, entry.Size, entry.SnapshotID, *snap.VolumeSize)
		}
	}

	return nil
}
