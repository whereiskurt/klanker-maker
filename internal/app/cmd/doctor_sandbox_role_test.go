package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

type fakeRoleShapeIAM struct {
	inline   map[string][]string // role name -> inline policy names
	attached map[string][]string // role name -> attached policy ARNs
	missing  map[string]bool     // role name -> NoSuchEntity
}

func (f *fakeRoleShapeIAM) ListRolePolicies(_ context.Context, in *iam.ListRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error) {
	if f.missing[*in.RoleName] {
		return nil, &iamtypes.NoSuchEntityException{Message: aws.String("no role")}
	}
	return &iam.ListRolePoliciesOutput{PolicyNames: f.inline[*in.RoleName]}, nil
}

func (f *fakeRoleShapeIAM) ListAttachedRolePolicies(_ context.Context, in *iam.ListAttachedRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error) {
	var out []iamtypes.AttachedPolicy
	for _, arn := range f.attached[*in.RoleName] {
		out = append(out, iamtypes.AttachedPolicy{PolicyArn: aws.String(arn)})
	}
	return &iam.ListAttachedRolePoliciesOutput{AttachedPolicies: out}, nil
}

func TestCheckSandboxRoleShape(t *testing.T) {
	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{
		{SandboxID: "sb-old", Region: "us-east-1", Status: "running"},
		{SandboxID: "sb-new", Region: "us-east-1", Status: "running"},
		{SandboxID: "sb-core", Region: "us-east-1", Status: "running"},
		{SandboxID: "sb-stopped", Region: "us-east-1", Status: "stopped"},
		{SandboxID: "sb-linked", Region: "us-east-1", Status: "running"},
	}}
	iamc := &fakeRoleShapeIAM{
		inline: map[string][]string{
			"km-ec2spot-ssm-sb-old-use1":  {"km-ec2spot-region-lock-use1", "km-sb-old-bedrock"},
			"km-ec2spot-ssm-sb-new-use1":  {"km-ec2spot-region-deny-use1", "km-sb-new-ssm-core"},
			"km-ec2spot-ssm-sb-core-use1": {"km-ec2spot-region-deny-use1"},
		},
		attached: map[string][]string{
			"km-ec2spot-ssm-sb-core-use1": {"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"},
		},
		missing: map[string]bool{"km-ec2spot-ssm-sb-linked-use1": true},
	}
	r := checkSandboxRoleShape(context.Background(), iamc, lister, "km")
	if r.Status != CheckWarn {
		t.Fatalf("status = %v, want WARN: %+v", r.Status, r)
	}
	for _, want := range []string{"sb-old", "sb-core"} {
		if !strings.Contains(r.Message, want) {
			t.Errorf("message should name %s: %s", want, r.Message)
		}
	}
	for _, not := range []string{"sb-new", "sb-stopped", "sb-linked"} {
		if strings.Contains(r.Message, not) {
			t.Errorf("message must not name %s: %s", not, r.Message)
		}
	}
	if !strings.Contains(r.Remediation, "km destroy") {
		t.Errorf("remediation should say to recreate: %s", r.Remediation)
	}
}

func TestCheckSandboxRoleShape_AllNewIsOK(t *testing.T) {
	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{{SandboxID: "sb-new", Region: "us-east-1", Status: "running"}}}
	iamc := &fakeRoleShapeIAM{inline: map[string][]string{"km-ec2spot-ssm-sb-new-use1": {"km-ec2spot-region-deny-use1"}}}
	if r := checkSandboxRoleShape(context.Background(), iamc, lister, "km"); r.Status != CheckOK {
		t.Fatalf("want OK, got %+v", r)
	}
}

func TestCheckSandboxRoleShape_SkipsWithoutIAM(t *testing.T) {
	if r := checkSandboxRoleShape(context.Background(), nil, &fakeSandboxLister{}, "km"); r.Status != CheckSkipped {
		t.Fatalf("want SKIP, got %+v", r)
	}
}
