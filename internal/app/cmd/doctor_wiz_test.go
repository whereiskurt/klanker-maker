package cmd

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"

	"github.com/whereiskurt/klanker-maker/pkg/wizoutpost"
)

func wizTag(v string) []ec2types.Tag {
	return []ec2types.Tag{{Key: awssdk.String("wiz"), Value: awssdk.String(v)}}
}

func TestIsWizScanResource(t *testing.T) {
	for _, v := range []string{"auto-gen-volume", "auto-gen-snapshot", "shareable-resource"} {
		if !isWizScanResource(wizTag(v)) {
			t.Errorf("wiz=%s should be a scan resource", v)
		}
	}
	if isWizScanResource(wizTag("other")) {
		t.Error("wiz=other must not match")
	}
	if isWizScanResource([]ec2types.Tag{{Key: awssdk.String("km:sandbox-id"), Value: awssdk.String("auto-gen-volume")}}) {
		t.Error("non-wiz key must not match")
	}
	if isWizScanResource(nil) {
		t.Error("no tags must not match")
	}
}

func TestCheckOrphanedEBSVolumes_SkipsWizScanVolume(t *testing.T) {
	v := makeVolume("vol-wiz", "sb-gone", "available", "us-east-1a", 30)
	v.Tags = append(v.Tags, wizTag("auto-gen-volume")...)
	client := &fakeEC2VolumeClient{volumes: []ec2types.Volume{v}}
	r := checkOrphanedEBSVolumes(context.Background(), client, &fakeSandboxLister{}, false, true)
	if len(client.deleteCallIDs) != 0 {
		t.Fatalf("DeleteVolume called on a Wiz scan volume: %v", client.deleteCallIDs)
	}
	if strings.Contains(r.Message, "vol-wiz") {
		t.Errorf("Wiz volume reported: %s", r.Message)
	}
}

func TestCheckUntaggedAvailableVolumes_SkipsWizScanVolume(t *testing.T) {
	v := makeUntaggedVolume("vol-wiz", "us-east-1a", 30, 48*time.Hour)
	v.Tags = append(v.Tags, wizTag("auto-gen-volume")...)
	client := &fakeEC2VolumeClient{volumes: []ec2types.Volume{v}}
	r := checkUntaggedAvailableVolumes(context.Background(), client, "kph")
	if r.Status != CheckOK || strings.Contains(r.Message, "vol-wiz") {
		t.Fatalf("Wiz volume reported: %s %s", r.Status, r.Message)
	}
	if len(client.deleteCallIDs) != 0 {
		t.Fatalf("DeleteVolume called: %v", client.deleteCallIDs)
	}
}

func TestCheckOrphanedSnapshots_SkipsWizScanSnapshot(t *testing.T) {
	s := makeSnapshot("snap-wiz", "sb-gone", 16)
	s.Tags = append(s.Tags, wizTag("auto-gen-snapshot")...)
	client := &fakeEC2VolumeClient{snapshots: []ec2types.Snapshot{s}}
	r := checkOrphanedSnapshots(context.Background(), client)
	if r.Status != CheckOK || strings.Contains(r.Message, "snap-wiz") {
		t.Fatalf("Wiz snapshot reported: %s %s", r.Status, r.Message)
	}
}

type fakeWizCFN struct {
	stack *cfntypes.Stack
	err   error
}

func (f fakeWizCFN) DescribeStacks(_ context.Context, _ *cloudformation.DescribeStacksInput, _ ...func(*cloudformation.Options)) (*cloudformation.DescribeStacksOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &cloudformation.DescribeStacksOutput{Stacks: []cfntypes.Stack{*f.stack}}, nil
}

func wizStack(status cfntypes.StackStatus, ver string) *cfntypes.Stack {
	return &cfntypes.Stack{StackStatus: status, Parameters: []cfntypes.Parameter{
		{ParameterKey: awssdk.String("WizVersionLastUpdated"), ParameterValue: awssdk.String(ver)}}}
}

func TestCheckWizOutpostStack(t *testing.T) {
	notFound := &smithy.GenericAPIError{Code: "ValidationError", Message: "Stack with id km-wiz-outpost-connector does not exist"}
	cases := []struct {
		name   string
		api    WizStackAPI
		want   CheckStatus
		substr string
	}{
		{"missing", fakeWizCFN{err: notFound}, CheckWarn, "not found"},
		{"rollback", fakeWizCFN{stack: wizStack("UPDATE_ROLLBACK_COMPLETE", "2025-10-07")}, CheckWarn, "UPDATE_ROLLBACK_COMPLETE"},
		{"version drift", fakeWizCFN{stack: wizStack("CREATE_COMPLETE", "2024-01-01")}, CheckWarn, "2024-01-01"},
		{"ok", fakeWizCFN{stack: wizStack("UPDATE_COMPLETE", "2025-10-07")}, CheckOK, ""},
		{"nil client", nil, CheckSkipped, ""},
	}
	for _, c := range cases {
		r := checkWizOutpostStack(context.Background(), c.api, "km", "2025-10-07")
		if r.Status != c.want || !strings.Contains(r.Message, c.substr) {
			t.Errorf("%s: got %s %q", c.name, r.Status, r.Message)
		}
		if c.want == CheckWarn && r.Remediation != "km init --wiz --dry-run=false" {
			t.Errorf("%s: remediation %q", c.name, r.Remediation)
		}
	}
	if r := checkWizOutpostStack(context.Background(), fakeWizCFN{stack: wizStack("CREATE_COMPLETE", "2024-01-01")}, "km", ""); r.Status != CheckOK {
		t.Errorf("empty wantVersion should skip comparison, got %s", r.Status)
	}
	if !strings.Contains(checkWizOutpostStack(context.Background(), fakeWizCFN{stack: wizStack("CREATE_COMPLETE", "2024-01-01")}, "km", "2025-10-07").Message, "2025-10-07") {
		t.Error("drift message must name the vendored version")
	}
}

type fakeWizIAM map[string]string // role name -> raw (unencoded) trust doc

func (f fakeWizIAM) GetRole(_ context.Context, in *iam.GetRoleInput, _ ...func(*iam.Options)) (*iam.GetRoleOutput, error) {
	doc, ok := f[*in.RoleName]
	if !ok {
		return nil, &smithy.GenericAPIError{Code: "NoSuchEntity", Message: "no role"}
	}
	return &iam.GetRoleOutput{Role: &iamtypes.Role{AssumeRolePolicyDocument: awssdk.String(url.QueryEscape(doc))}}, nil
}

func trustDoc(principal, ext string) string {
	return `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"` + principal +
		`"},"Action":"sts:AssumeRole","Condition":{"StringEquals":{"sts:ExternalId":"` + ext + `"}}}]}`
}

func TestCheckWizOutpostTrust(t *testing.T) {
	s := wizoutpost.Settings{TrustRoleARN: "arn:aws:iam::111122223333:role/wiz-dc", OrchestratorAccountID: "444455556666",
		ExternalID: "abcdef12-3456-7890-abcd-ef1234567890", RoleSuffix: "km"}
	scanner := "arn:aws:iam::444455556666:role/WizOrchestratorNodePoolRole"
	good := fakeWizIAM{
		s.AccessRoleNameOrDefault(): trustDoc(s.TrustRoleARN, s.ExternalID),
		s.ScannerRoleName():         trustDoc(scanner, s.ExternalID),
	}
	if r := checkWizOutpostTrust(context.Background(), good, s); r.Status != CheckOK {
		t.Fatalf("good: %s %s", r.Status, r.Message)
	}
	cases := []struct {
		name   string
		api    fakeWizIAM
		substr string
	}{
		{"access principal", fakeWizIAM{s.AccessRoleNameOrDefault(): trustDoc("arn:aws:iam::999999999999:role/x", s.ExternalID), s.ScannerRoleName(): good[s.ScannerRoleName()]}, s.AccessRoleNameOrDefault()},
		{"access ext", fakeWizIAM{s.AccessRoleNameOrDefault(): trustDoc(s.TrustRoleARN, "other"), s.ScannerRoleName(): good[s.ScannerRoleName()]}, "ExternalId"},
		{"scanner principal", fakeWizIAM{s.AccessRoleNameOrDefault(): good[s.AccessRoleNameOrDefault()], s.ScannerRoleName(): trustDoc("arn:aws:iam::1:role/x", s.ExternalID)}, s.ScannerRoleName()},
		{"scanner ext", fakeWizIAM{s.AccessRoleNameOrDefault(): good[s.AccessRoleNameOrDefault()], s.ScannerRoleName(): trustDoc(scanner, "other")}, s.ScannerRoleName()},
		{"missing role", fakeWizIAM{s.AccessRoleNameOrDefault(): good[s.AccessRoleNameOrDefault()]}, s.ScannerRoleName()},
	}
	for _, c := range cases {
		r := checkWizOutpostTrust(context.Background(), c.api, s)
		if r.Status != CheckWarn || !strings.Contains(r.Message, c.substr) {
			t.Errorf("%s: got %s %q", c.name, r.Status, r.Message)
		}
	}
	if r := checkWizOutpostTrust(context.Background(), nil, s); r.Status != CheckSkipped {
		t.Errorf("nil client: %s", r.Status)
	}
}

func TestDoctor_WizDormant_NoChecks(t *testing.T) {
	cfg := minimalConfig() // wiz == nil
	results := runChecks(context.Background(), buildChecks(cfg, &DoctorDeps{}))
	for _, r := range results {
		if strings.HasPrefix(r.Name, "Wiz") {
			t.Errorf("dormant install registered %q", r.Name)
		}
	}
	cfg.wiz = &wizoutpost.Settings{RoleSuffix: "km"}
	found := 0
	for _, r := range runChecks(context.Background(), buildChecks(cfg, &DoctorDeps{})) {
		if strings.HasPrefix(r.Name, "Wiz Outpost") {
			found++
			if r.Status != CheckSkipped {
				t.Errorf("%s with nil clients: %s", r.Name, r.Status)
			}
		}
	}
	if found != 2 {
		t.Errorf("configured install registered %d Wiz checks, want 2", found)
	}
}
