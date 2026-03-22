package aws_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	tagtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ---- Mocks ----

type mockTagAPI struct {
	output *resourcegroupstaggingapi.GetResourcesOutput
	err    error
}

func (m *mockTagAPI) GetResources(
	_ context.Context,
	_ *resourcegroupstaggingapi.GetResourcesInput,
	_ ...func(*resourcegroupstaggingapi.Options),
) (*resourcegroupstaggingapi.GetResourcesOutput, error) {
	return m.output, m.err
}

// ---- Tests ----

func TestFindSandboxByID_Found(t *testing.T) {
	sandboxID := "sb-a1b2c3d4"
	arn1 := "arn:aws:ec2:us-east-1:052251888500:instance/i-0abc123"
	arn2 := "arn:aws:ec2:us-east-1:052251888500:security-group/sg-0def456"

	mock := &mockTagAPI{
		output: &resourcegroupstaggingapi.GetResourcesOutput{
			ResourceTagMappingList: []tagtypes.ResourceTagMapping{
				{ResourceARN: aws.String(arn1)},
				{ResourceARN: aws.String(arn2)},
			},
		},
	}

	loc, err := kmaws.FindSandboxByID(context.Background(), mock, sandboxID)
	if err != nil {
		t.Fatalf("FindSandboxByID returned error: %v", err)
	}

	if loc.SandboxID != sandboxID {
		t.Errorf("SandboxID = %q, want %q", loc.SandboxID, sandboxID)
	}

	if loc.ResourceCount != 2 {
		t.Errorf("ResourceCount = %d, want 2", loc.ResourceCount)
	}

	if len(loc.ResourceARNs) != 2 {
		t.Errorf("ResourceARNs len = %d, want 2", len(loc.ResourceARNs))
	}

	// S3 state path is deterministic
	expectedStatePath := "tf-km/sandboxes/" + sandboxID
	if loc.S3StatePath != expectedStatePath {
		t.Errorf("S3StatePath = %q, want %q", loc.S3StatePath, expectedStatePath)
	}
}

func TestFindSandboxByID_NotFound(t *testing.T) {
	mock := &mockTagAPI{
		output: &resourcegroupstaggingapi.GetResourcesOutput{
			ResourceTagMappingList: []tagtypes.ResourceTagMapping{},
		},
	}

	_, err := kmaws.FindSandboxByID(context.Background(), mock, "sb-notfound")
	if err == nil {
		t.Fatal("expected error when sandbox not found, got nil")
	}

	// Error should be descriptive
	if !errors.Is(err, kmaws.ErrSandboxNotFound) {
		t.Errorf("expected ErrSandboxNotFound, got: %v", err)
	}
}

func TestSandboxLocationStatePath(t *testing.T) {
	loc := &kmaws.SandboxLocation{
		SandboxID:   "sb-a1b2c3d4",
		S3StatePath: "tf-km/sandboxes/sb-a1b2c3d4",
	}

	if loc.StatePath() != "tf-km/sandboxes/sb-a1b2c3d4" {
		t.Errorf("StatePath() = %q, want %q", loc.StatePath(), "tf-km/sandboxes/sb-a1b2c3d4")
	}
}

func TestGetSpotInstanceID(t *testing.T) {
	// Simulate terragrunt output -json result
	// Each value is an object with "value" and "type" keys per terraform output format
	output := map[string]interface{}{
		"spot_instance_id": map[string]interface{}{
			"value":     "i-0abc123def456",
			"type":      "string",
			"sensitive": false,
		},
		"some_other_output": map[string]interface{}{
			"value": "ignored",
		},
	}

	instanceID, err := kmaws.GetSpotInstanceID(output)
	if err != nil {
		t.Fatalf("GetSpotInstanceID returned error: %v", err)
	}
	if instanceID != "i-0abc123def456" {
		t.Errorf("instanceID = %q, want %q", instanceID, "i-0abc123def456")
	}
}

func TestGetSpotInstanceID_Missing(t *testing.T) {
	output := map[string]interface{}{
		"other_output": map[string]interface{}{"value": "something"},
	}

	_, err := kmaws.GetSpotInstanceID(output)
	if err == nil {
		t.Fatal("expected error when spot_instance_id missing, got nil")
	}
}
