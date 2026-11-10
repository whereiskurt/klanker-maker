package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/whereiskurt/klanker-maker/pkg/wizoutpost"
)

const (
	wizCheckStack = "Wiz Outpost connector stack"
	wizCheckTrust = "Wiz Outpost connector trust"
	wizInitFix    = "km init --wiz --dry-run=false"
)

// WizRoleAPI is the slice of the IAM client the Wiz trust check uses.
type WizRoleAPI interface {
	GetRole(ctx context.Context, in *iam.GetRoleInput, optFns ...func(*iam.Options)) (*iam.GetRoleOutput, error)
}

// isWizScanResource reports whether the tags mark a resource created by Wiz's
// own scanning (volumes/snapshots Wiz makes and cleans up itself). Doctor's
// orphan sweeps must never delete these.
func isWizScanResource(tags []ec2types.Tag) bool {
	for _, t := range tags {
		if awssdk.ToString(t.Key) != "wiz" {
			continue
		}
		switch awssdk.ToString(t.Value) {
		case "auto-gen-volume", "auto-gen-snapshot", "shareable-resource":
			return true
		}
	}
	return false
}

// checkWizOutpostStack verifies the connector stack exists, is healthy, and
// runs the template version km currently vendors. wantVersion "" skips the
// version comparison.
func checkWizOutpostStack(ctx context.Context, api WizStackAPI, prefix, wantVersion string) CheckResult {
	res := CheckResult{Name: wizCheckStack}
	if api == nil {
		res.Status, res.Message = CheckSkipped, "CloudFormation client not available"
		return res
	}
	name := wizoutpost.StackName(prefix)
	out, err := api.DescribeStacks(ctx, &cloudformation.DescribeStacksInput{StackName: &name})
	if err != nil {
		if !isStackNotFound(err) {
			res.Status, res.Message = CheckWarn, fmt.Sprintf("describe stack %s: %v", name, err)
			return res
		}
		out = &cloudformation.DescribeStacksOutput{}
	}
	if len(out.Stacks) == 0 {
		res.Status = CheckWarn
		res.Message = fmt.Sprintf("stack %s not found", name)
		res.Remediation = wizInitFix
		return res
	}
	st := out.Stacks[0]
	status := string(st.StackStatus)
	if status != "CREATE_COMPLETE" && status != "UPDATE_COMPLETE" {
		res.Status = CheckWarn
		res.Message = fmt.Sprintf("stack %s is %s", name, status)
		res.Remediation = wizInitFix
		return res
	}
	deployed := ""
	for _, p := range st.Parameters {
		if awssdk.ToString(p.ParameterKey) == "WizVersionLastUpdated" {
			deployed = awssdk.ToString(p.ParameterValue)
		}
	}
	if wantVersion != "" && deployed != wantVersion {
		res.Status = CheckWarn
		res.Message = fmt.Sprintf("stack %s runs template version %q, km vendors %q", name, deployed, wantVersion)
		res.Remediation = wizInitFix
		return res
	}
	res.Status = CheckOK
	res.Message = fmt.Sprintf("stack %s %s (template %s)", name, status, deployed)
	return res
}

type wizTrustDoc struct {
	Statement []struct {
		Principal struct {
			AWS any `json:"AWS"`
		} `json:"Principal"`
		Condition struct {
			StringEquals map[string]any `json:"StringEquals"`
		} `json:"Condition"`
	} `json:"Statement"`
}

// wizTrustMismatch returns a description of how the role's trust policy
// differs from the expected principal and external id, or "" when it matches.
func wizTrustMismatch(ctx context.Context, api WizRoleAPI, role, wantPrincipal, wantExt string) (string, error) {
	out, err := api.GetRole(ctx, &iam.GetRoleInput{RoleName: &role})
	if err != nil {
		return "", fmt.Errorf("get role %s: %w", role, err)
	}
	if out.Role == nil || out.Role.AssumeRolePolicyDocument == nil {
		return fmt.Sprintf("role %s has no trust policy", role), nil
	}
	raw, err := url.QueryUnescape(*out.Role.AssumeRolePolicyDocument)
	if err != nil {
		return "", fmt.Errorf("decode trust policy of %s: %w", role, err)
	}
	var doc wizTrustDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return "", fmt.Errorf("parse trust policy of %s: %w", role, err)
	}
	var principalOK, extOK bool
	for _, s := range doc.Statement {
		switch v := s.Principal.AWS.(type) {
		case string:
			principalOK = principalOK || v == wantPrincipal
		case []any:
			for _, e := range v {
				principalOK = principalOK || e == wantPrincipal
			}
		}
		if e, ok := s.Condition.StringEquals["sts:ExternalId"].(string); ok && e == wantExt {
			extOK = true
		}
	}
	if !principalOK {
		return fmt.Sprintf("role %s trusts a principal other than %s", role, wantPrincipal), nil
	}
	if !extOK {
		return fmt.Sprintf("role %s sts:ExternalId does not match wiz.outpost.external_id", role), nil
	}
	return "", nil
}

// checkWizOutpostTrust verifies both connector roles still trust the principals
// and external id configured in wiz.outpost.
func checkWizOutpostTrust(ctx context.Context, api WizRoleAPI, s wizoutpost.Settings) CheckResult {
	res := CheckResult{Name: wizCheckTrust}
	if api == nil {
		res.Status, res.Message = CheckSkipped, "IAM client not available"
		return res
	}
	roles := []struct{ name, principal string }{
		{s.AccessRoleNameOrDefault(), s.TrustRoleARN},
		{s.ScannerRoleName(), fmt.Sprintf("arn:aws:iam::%s:role/WizOrchestratorNodePoolRole", s.OrchestratorAccountID)},
	}
	for _, r := range roles {
		msg, err := wizTrustMismatch(ctx, api, r.name, r.principal, s.ExternalID)
		if err != nil {
			res.Status, res.Message = CheckWarn, err.Error()
			res.Remediation = wizInitFix
			return res
		}
		if msg != "" {
			res.Status, res.Message, res.Remediation = CheckWarn, msg, wizInitFix
			return res
		}
	}
	res.Status = CheckOK
	res.Message = fmt.Sprintf("%s and %s trust the configured principals and external id", roles[0].name, roles[1].name)
	return res
}
