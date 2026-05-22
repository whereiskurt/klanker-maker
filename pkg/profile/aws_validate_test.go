package profile_test

import "testing"

// EC2SnapshotAPI mock interface (real interface to be defined in aws_validate.go in Wave 1 plan-03)
// Mirrors EC2VolumeAPI shape from internal/app/cmd/doctor_ebs.go:42–47.

func TestValidateSnapshotsAWS_HappyPath(t *testing.T) {
	t.Skip("RED — implemented in 87-03 (SNAP-03 Layer 2 AWS pre-flight)")
}

func TestValidateSnapshotsAWS_NotFound(t *testing.T) {
	t.Skip("RED — InvalidSnapshot.NotFound case — Wave 1 plan-03")
}

func TestValidateSnapshotsAWS_PendingState(t *testing.T) {
	t.Skip("RED — non-completed snapshot state — Wave 1 plan-03")
}

func TestValidateSnapshotsAWS_SizeOverrideTooSmall(t *testing.T) {
	t.Skip("RED — size < snapshot.VolumeSize — Wave 1 plan-03")
}

func TestValidateSnapshotsAWS_IAMMissingWarnAndSkip(t *testing.T) {
	t.Skip("RED — smithy.GenericAPIError Code=UnauthorizedOperation → WARN+nil — Wave 1 plan-03")
	// NOTE: must construct smithy.GenericAPIError with Code: "UnauthorizedOperation" (EC2-specific),
	// NOT "AccessDenied" (S3/IAM-specific). See 87-VALIDATION.md aliasing risk SNAP-03.
}

func TestValidateSnapshotsAWS_AccessDeniedDoesFail(t *testing.T) {
	t.Skip("RED — AccessDenied is NOT the graceful-degradation code; must surface error — Wave 1 plan-03")
}
