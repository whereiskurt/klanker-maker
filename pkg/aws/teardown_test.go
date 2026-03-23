package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	tagtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

// --------------------------------------------------------------------------
// Mocks
// --------------------------------------------------------------------------

type mockTagAPIForTeardown struct {
	resources []tagtypes.ResourceTagMapping
	err       error
}

func (m *mockTagAPIForTeardown) GetResources(ctx context.Context, params *resourcegroupstaggingapi.GetResourcesInput, optFns ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.GetResourcesOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &resourcegroupstaggingapi.GetResourcesOutput{
		ResourceTagMappingList: m.resources,
	}, nil
}

type mockEC2APIForTeardown struct {
	terminateCalled bool
	terminatedIDs   []string
	terminateErr    error
}

func (m *mockEC2APIForTeardown) TerminateInstances(ctx context.Context, params *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	m.terminateCalled = true
	m.terminatedIDs = append(m.terminatedIDs, params.InstanceIds...)
	if m.terminateErr != nil {
		return nil, m.terminateErr
	}
	return &ec2.TerminateInstancesOutput{}, nil
}

// --------------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------------

func strPtr(s string) *string { return &s }

// TestDestroySandboxResources_EC2: DestroySandboxResources discovers tagged EC2
// instance via FindSandboxByID, calls TerminateInstances.
func TestDestroySandboxResources_EC2(t *testing.T) {
	ec2ARN := "arn:aws:ec2:us-east-1:123456789012:instance/i-0abc12345"
	tagClient := &mockTagAPIForTeardown{
		resources: []tagtypes.ResourceTagMapping{
			{ResourceARN: strPtr(ec2ARN)},
		},
	}
	ec2Client := &mockEC2APIForTeardown{}

	err := DestroySandboxResources(context.Background(), tagClient, ec2Client, "sb-aabbccdd")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if !ec2Client.terminateCalled {
		t.Error("expected TerminateInstances to be called for EC2 instance ARN")
	}
	if len(ec2Client.terminatedIDs) == 0 || ec2Client.terminatedIDs[0] != "i-0abc12345" {
		t.Errorf("expected terminated instance i-0abc12345, got: %v", ec2Client.terminatedIDs)
	}
}

// TestDestroySandboxResources_NoResources: Returns nil (idempotent) when no tagged resources found.
func TestDestroySandboxResources_NoResources(t *testing.T) {
	tagClient := &mockTagAPIForTeardown{
		resources: []tagtypes.ResourceTagMapping{},
	}
	ec2Client := &mockEC2APIForTeardown{}

	err := DestroySandboxResources(context.Background(), tagClient, ec2Client, "sb-notexists")
	if err != nil {
		t.Fatalf("expected nil error when sandbox not found (idempotent), got: %v", err)
	}
	if ec2Client.terminateCalled {
		t.Error("expected TerminateInstances NOT to be called when no resources found")
	}
}

// TestDestroySandboxResources_TagAPIError: Returns error when FindSandboxByID returns non-NotFound error.
func TestDestroySandboxResources_TagAPIError(t *testing.T) {
	tagClient := &mockTagAPIForTeardown{
		err: errors.New("AWS throttled"),
	}
	ec2Client := &mockEC2APIForTeardown{}

	err := DestroySandboxResources(context.Background(), tagClient, ec2Client, "sb-aabbccdd")
	if err == nil {
		t.Fatal("expected error when tag API call fails")
	}
}
