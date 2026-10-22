package cmd

import (
	"context"
	"net/url"
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
	docs     map[string]string   // "role|policy" -> URL-encoded policy document (GetRolePolicy)
}

func (f *fakeRoleShapeIAM) GetRolePolicy(_ context.Context, in *iam.GetRolePolicyInput, _ ...func(*iam.Options)) (*iam.GetRolePolicyOutput, error) {
	doc := f.docs[*in.RoleName+"|"+*in.PolicyName]
	return &iam.GetRolePolicyOutput{RoleName: in.RoleName, PolicyName: in.PolicyName, PolicyDocument: aws.String(doc)}, nil
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
		{SandboxID: "sb-stopped-new", Region: "us-east-1", Status: "stopped"},
		{SandboxID: "sb-paused", Region: "us-east-1", Status: "paused"},
		{SandboxID: "sb-failed", Region: "us-east-1", Status: "failed"},
		{SandboxID: "sb-linked", Region: "us-east-1", Status: "running"},
	}}
	iamc := &fakeRoleShapeIAM{
		inline: map[string][]string{
			"km-ec2spot-ssm-sb-old-use1":  {"km-ec2spot-region-lock-use1", "km-sb-old-bedrock"},
			"km-ec2spot-ssm-sb-new-use1":  {"km-ec2spot-region-deny-use1", "km-sb-new-ssm-core"},
			"km-ec2spot-ssm-sb-core-use1": {"km-ec2spot-region-deny-use1"},
			// A stopped or paused box resumes on its old role, so it is named too.
			"km-ec2spot-ssm-sb-stopped-use1":     {"km-ec2spot-region-lock-use1"},
			"km-ec2spot-ssm-sb-stopped-new-use1": {"km-ec2spot-region-deny-use1"},
			"km-ec2spot-ssm-sb-paused-use1":      {"km-sb-paused-threads-rw"},
			// Neither running nor resumable: never inspected.
			"km-ec2spot-ssm-sb-failed-use1": {"km-ec2spot-region-lock-use1"},
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
	for _, want := range []string{"sb-old [running]", "sb-core [running]", "sb-stopped [stopped] (Allow-* region lock)", "sb-paused [paused]"} {
		if !strings.Contains(r.Message, want) {
			t.Errorf("message should name %s: %s", want, r.Message)
		}
	}
	for _, not := range []string{"sb-new", "sb-stopped-new", "sb-failed", "sb-linked"} {
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
	r := checkSandboxRoleShape(context.Background(), iamc, lister, "km")
	if r.Status != CheckOK {
		t.Fatalf("want OK, got %+v", r)
	}
	if !strings.Contains(r.Message, "1 running/stopped/paused sandbox role(s)") {
		t.Errorf("OK message should count running/stopped/paused roles: %s", r.Message)
	}
}

func TestCheckSandboxRoleShape_SkipsWithoutIAM(t *testing.T) {
	if r := checkSandboxRoleShape(context.Background(), nil, &fakeSandboxLister{}, "km"); r.Status != CheckSkipped {
		t.Fatalf("want SKIP, got %+v", r)
	}
}

// A v1.8.0 role carries the per-sandbox threads-rw inline policy, which v1.9.0
// dropped (session state is local + the km-slack session bridge action).
func TestCheckSandboxRoleShape_NamesThreadsRWRole(t *testing.T) {
	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{
		{SandboxID: "sb-x", Region: "us-east-1", Status: "running"},
		{SandboxID: "sb-y", Region: "us-east-1", Status: "running"},
	}}
	iamc := &fakeRoleShapeIAM{inline: map[string][]string{
		"km-ec2spot-ssm-sb-x-use1": {"km-ec2spot-region-deny-use1", "km-sb-x-threads-rw"},
		"km-ec2spot-ssm-sb-y-use1": {"km-ec2spot-region-deny-use1", "km-sb-y-inbound-sqs"},
	}}
	r := checkSandboxRoleShape(context.Background(), iamc, lister, "km")
	if r.Status != CheckWarn {
		t.Fatalf("status = %v, want WARN: %+v", r.Status, r)
	}
	if !strings.Contains(r.Message, "sb-x [running] (threads-table grant (pre-v1.9.0))") {
		t.Errorf("message should name sb-x with the threads reason: %s", r.Message)
	}
	if strings.Contains(r.Message, "sb-y") {
		t.Errorf("message must not name sb-y: %s", r.Message)
	}
	if !strings.Contains(r.Message, "pre-v1.10.0 role") {
		t.Errorf("message should say pre-v1.10.0 role: %s", r.Message)
	}

	// A role with neither old shape nor the threads grant is OK.
	ok := &fakeRoleShapeIAM{inline: map[string][]string{"km-ec2spot-ssm-sb-y-use1": {"km-sb-y-inbound-sqs"}}}
	one := &fakeSandboxLister{records: []kmaws.SandboxRecord{{SandboxID: "sb-y", Region: "us-east-1", Status: "running"}}}
	if r := checkSandboxRoleShape(context.Background(), ok, one, "km"); r.Status != CheckOK {
		t.Fatalf("want OK, got %+v", r)
	}
}

// A v1.8.0/v1.9.0 role carries the secrets-KMS policy with a bare StringEquals on
// the multivalued kms:ResourceAliases; v1.10.0 uses ForAnyValue. The check reads
// the document (URL-encoded, as IAM returns it) and names only the former.
func TestCheckSandboxRoleShape_NamesInertSecretsKMSGrant(t *testing.T) {
	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{
		{SandboxID: "sb-sops-old", Region: "us-east-1", Status: "running"},
		{SandboxID: "sb-sops-new", Region: "us-east-1", Status: "running"},
		{SandboxID: "sb-nosops", Region: "us-east-1", Status: "running"},
	}}
	bare := `{"Version":"2012-10-17","Statement":[{"Sid":"KMSDecryptSandboxSecrets","Effect":"Allow","Action":["kms:Decrypt","kms:DescribeKey"],"Resource":["arn:aws:kms:*:1:key/*"],"Condition":{"StringEquals":{"kms:ResourceAliases":"alias/km-sandbox-secrets"}}}]}`
	fixed := `{"Version":"2012-10-17","Statement":[{"Sid":"KMSDecryptSandboxSecrets","Effect":"Allow","Action":["kms:Decrypt","kms:DescribeKey"],"Resource":["arn:aws:kms:*:1:key/*"],"Condition":{"ForAnyValue:StringEquals":{"kms:ResourceAliases":"alias/km-sandbox-secrets"}}}]}`
	iamc := &fakeRoleShapeIAM{
		inline: map[string][]string{
			"km-ec2spot-ssm-sb-sops-old-use1": {"km-ec2spot-region-deny-use1", "km-sb-sops-old-sandbox-secrets-kms"},
			"km-ec2spot-ssm-sb-sops-new-use1": {"km-ec2spot-region-deny-use1", "km-sb-sops-new-sandbox-secrets-kms"},
			"km-ec2spot-ssm-sb-nosops-use1":   {"km-ec2spot-region-deny-use1"},
		},
		docs: map[string]string{
			"km-ec2spot-ssm-sb-sops-old-use1|km-sb-sops-old-sandbox-secrets-kms": url.PathEscape(bare),
			"km-ec2spot-ssm-sb-sops-new-use1|km-sb-sops-new-sandbox-secrets-kms": url.PathEscape(fixed),
		},
	}
	r := checkSandboxRoleShape(context.Background(), iamc, lister, "km")
	if r.Status != CheckWarn {
		t.Fatalf("status = %v, want WARN: %+v", r.Status, r)
	}
	if !strings.Contains(r.Message, "sb-sops-old [running] (SOPS KMS grant inert") {
		t.Errorf("message should name sb-sops-old with the inert-grant reason: %s", r.Message)
	}
	for _, not := range []string{"sb-sops-new", "sb-nosops"} {
		if strings.Contains(r.Message, not) {
			t.Errorf("message must not name %s: %s", not, r.Message)
		}
	}
}

func TestSecretsKMSGrantInert(t *testing.T) {
	cases := map[string]bool{
		`{"Condition":{"StringEquals":{"kms:ResourceAliases":"alias/x"}}}`:                true,
		`{"Condition":{"ForAnyValue:StringEquals":{"kms:ResourceAliases":"alias/x"}}}`:    false,
		`{"Condition":{"ForAllValues:StringLike":{"kms:ResourceAliases":"alias/*"}}}`:     false,
		`{"Condition":{"StringEquals":{"kms:ViaService":"ssm.us-east-1.amazonaws.com"}}}`: false, // no alias condition at all
	}
	for doc, want := range cases {
		if got := secretsKMSGrantInert(url.PathEscape(doc)); got != want {
			t.Errorf("secretsKMSGrantInert(%s) = %v, want %v", doc, got, want)
		}
	}
}
