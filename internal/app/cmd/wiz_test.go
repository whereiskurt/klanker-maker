package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/smithy-go"
)

type fakeWizStackAPI struct {
	out *cloudformation.DescribeStacksOutput
	err error
}

func (f fakeWizStackAPI) DescribeStacks(context.Context, *cloudformation.DescribeStacksInput, ...func(*cloudformation.Options)) (*cloudformation.DescribeStacksOutput, error) {
	return f.out, f.err
}

func wizNotFound() error {
	return &smithy.GenericAPIError{Code: "ValidationError", Message: "Stack with id km-wiz-outpost-connector does not exist"}
}

func wizHealthyStack() *cloudformation.DescribeStacksOutput {
	s := func(v string) *string { return &v }
	return &cloudformation.DescribeStacksOutput{Stacks: []cftypes.Stack{{
		StackStatus: cftypes.StackStatusCreateComplete,
		Parameters:  []cftypes.Parameter{{ParameterKey: s("WizVersionLastUpdated"), ParameterValue: s("2025-10-07")}},
		Outputs: []cftypes.Output{
			{OutputKey: s("WizAccessRoleARNID"), OutputValue: s("arn:aws:iam::111122223333:role/access")},
			{OutputKey: s("WizScannerRoleARNID"), OutputValue: s("arn:aws:iam::111122223333:role/scanner")},
			{OutputKey: s("ExternalID"), OutputValue: s("ext-123")},
		},
	}}}
}

func TestWizStatus_NotConfigured_NoStack(t *testing.T) {
	var buf bytes.Buffer
	if err := runWizStatus(context.Background(), &buf, fakeWizStackAPI{err: wizNotFound()}, "km", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Wiz Outpost connector: not configured (no wiz.outpost block in km-config.yaml)") {
		t.Errorf("got %q", buf.String())
	}
}

func TestWizStatus_NotConfigured_StackExists(t *testing.T) {
	var buf bytes.Buffer
	if err := runWizStatus(context.Background(), &buf, fakeWizStackAPI{out: wizHealthyStack()}, "km", false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Wiz Outpost stack exists but wiz: is not configured — remove the connector in Wiz, then km uninit or delete the stack",
		"ext-123", "role/access", "role/scanner",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q in %q", want, buf.String())
		}
	}
}

func TestWizStatus_StackMissing(t *testing.T) {
	var buf bytes.Buffer
	if err := runWizStatus(context.Background(), &buf, fakeWizStackAPI{err: wizNotFound()}, "km", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "stack km-wiz-outpost-connector not found — run km init --wiz --dry-run=false") {
		t.Errorf("got %q", buf.String())
	}
}

func TestWizStatus_OtherErrorPropagates(t *testing.T) {
	err := runWizStatus(context.Background(), &bytes.Buffer{}, fakeWizStackAPI{err: &smithy.GenericAPIError{Code: "AccessDenied", Message: "no"}}, "km", true)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestWizStatus_Healthy(t *testing.T) {
	var buf bytes.Buffer
	if err := runWizStatus(context.Background(), &buf, fakeWizStackAPI{out: wizHealthyStack()}, "km", true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Status: CREATE_COMPLETE", "Template: 2025-10-07", "Access role ARN   arn:aws:iam::111122223333:role/access", "External ID       ext-123"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q in %q", want, buf.String())
		}
	}
}
