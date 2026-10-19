package cmd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
)

// roleShapeIAM is the slice of IAMCleanupAPI this check needs.
type roleShapeIAM interface {
	ListRolePolicies(ctx context.Context, params *iam.ListRolePoliciesInput, optFns ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error)
	ListAttachedRolePolicies(ctx context.Context, params *iam.ListAttachedRolePoliciesInput, optFns ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error)
}

// roleShapeInspectedStatus lists the sandbox statuses whose role the check reads:
// a running box uses its role now, and a stopped or paused one uses it again on
// resume. Other statuses (failed, unknown, …) are not inspected.
var roleShapeInspectedStatus = map[string]bool{"running": true, "stopped": true, "paused": true}

// checkSandboxRoleShape WARNs for every running, stopped or paused sandbox whose
// instance role predates the current shape: the pre-v1.8.0 Allow-*/* "region-lock" inline
// policy or managed AmazonSSMManagedInstanceCore attachment, or the v1.8.0
// "-threads-rw" inline policy that v1.9.0 dropped (session state is local and
// the bridge writes the session attributes). The role is created per
// sandbox at km create, so the fleet converges only as boxes are recreated; this
// makes the remaining ones visible. Stopped and paused boxes are included because
// they resume on the role they were created with. A role that does not exist in
// this account (a linked-account launch) is skipped.
func checkSandboxRoleShape(ctx context.Context, iamClient roleShapeIAM, lister SandboxLister, resourcePrefix string) CheckResult {
	const name = "Sandbox role shape"
	if iamClient == nil || lister == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "IAM client or sandbox lister not configured"}
	}
	records, err := lister.ListSandboxes(ctx, false)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list sandboxes: %v", err)}
	}
	var old []string
	checked := 0
	for _, r := range records {
		if !roleShapeInspectedStatus[r.Status] {
			continue
		}
		label := compiler.RegionLabel(r.Region)
		role := fmt.Sprintf("%s-ec2spot-ssm-%s-%s", resourcePrefix, r.SandboxID, label)
		inline, err := iamClient.ListRolePolicies(ctx, &iam.ListRolePoliciesInput{RoleName: aws.String(role)})
		if err != nil {
			var nse *iamtypes.NoSuchEntityException
			if errors.As(err, &nse) {
				continue
			}
			return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not read role %s: %v", role, err)}
		}
		checked++
		var reasons []string
		add := func(r string) { reasons = append(reasons, r) }
		for _, pn := range inline.PolicyNames {
			if strings.HasSuffix(pn, "-ec2spot-region-lock-"+label) {
				add("Allow-* region lock")
			}
			if strings.HasSuffix(pn, "-threads-rw") {
				add("threads-table grant (pre-v1.9.0)")
			}
		}
		attached, err := iamClient.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{RoleName: aws.String(role)})
		if err == nil {
			for _, ap := range attached.AttachedPolicies {
				if ap.PolicyArn != nil && strings.HasSuffix(*ap.PolicyArn, "/AmazonSSMManagedInstanceCore") {
					add("managed SSM Core (ssm:GetParameter on *)")
				}
			}
		}
		reason := strings.Join(reasons, " + ")
		if reason != "" {
			old = append(old, fmt.Sprintf("%s [%s] (%s)", r.SandboxID, r.Status, reason))
		}
	}
	sort.Strings(old)
	if len(old) == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: fmt.Sprintf("%d running/stopped/paused sandbox role(s) on the least-privilege shape", checked)}
	}
	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     fmt.Sprintf("%d running/stopped/paused sandbox(es) still hold a pre-v1.9.0 role (in-region Allow-*, account-wide SSM reads, and/or a threads-table grant):\n    %s", len(old), strings.Join(old, "\n    ")),
		Remediation: "The role is created at km create. Recreate each: 'km destroy <id> --remote --yes && km create <profile> <alias>'. See docs/operational-gotchas.md § The sandbox role is least-privilege since v1.8.0.",
	}
}
