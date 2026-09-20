package cmd

import (
	"context"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

type fakePathSSM struct{ names []string }

func (f fakePathSSM) GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	return &ssm.GetParameterOutput{}, nil
}

func (f fakePathSSM) GetParametersByPath(context.Context, *ssm.GetParametersByPathInput, ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	var ps []ssmtypes.Parameter
	for _, n := range f.names {
		ps = append(ps, ssmtypes.Parameter{Name: awssdk.String(n)})
	}
	return &ssm.GetParametersByPathOutput{Parameters: ps}, nil
}

type fakeAccessLister struct{ ids []string }

func (f fakeAccessLister) ListSandboxes(context.Context, bool) ([]kmaws.SandboxRecord, error) {
	var out []kmaws.SandboxRecord
	for _, id := range f.ids {
		out = append(out, kmaws.SandboxRecord{SandboxID: id})
	}
	return out, nil
}

func TestCheckOrphanAccessParams(t *testing.T) {
	ssmc := fakePathSSM{names: []string{"/km/access/live/ssh-key", "/km/access/live/desktop-cred", "/km/access/gone/ssh-key"}}

	r := checkOrphanAccessParams(context.Background(), ssmc, fakeAccessLister{ids: []string{"live"}}, "km")
	if r.Status != CheckWarn || !strings.Contains(r.Message, "gone") || strings.Contains(r.Message, "live") {
		t.Errorf("orphan case: got %+v", r)
	}
	if len(r.Details) != 1 || r.Details[0] != "gone" {
		t.Errorf("Details = %v, want [gone]", r.Details)
	}

	r = checkOrphanAccessParams(context.Background(), fakePathSSM{}, fakeAccessLister{}, "km")
	if r.Status != CheckSkipped {
		t.Errorf("empty namespace must SKIP, got %+v", r)
	}

	r = checkOrphanAccessParams(context.Background(), ssmc, fakeAccessLister{ids: []string{"live", "gone"}}, "km")
	if r.Status != CheckOK {
		t.Errorf("no orphans must be OK, got %+v", r)
	}

	r = checkOrphanAccessParams(context.Background(), nil, fakeAccessLister{}, "km")
	if r.Status != CheckSkipped {
		t.Errorf("nil SSM must SKIP, got %+v", r)
	}
}
