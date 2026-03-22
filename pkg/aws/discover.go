package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	tagtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

// ErrSandboxNotFound is returned by FindSandboxByID when the tag query returns no resources.
var ErrSandboxNotFound = errors.New("sandbox not found")

// SandboxLocation holds the discovered state of a sandbox in AWS, including
// the deterministic S3 state path and the set of tagged resource ARNs.
type SandboxLocation struct {
	SandboxID     string
	S3StatePath   string // "tf-km/sandboxes/<sandbox-id>"
	ResourceCount int
	ResourceARNs  []string
}

// StatePath returns the S3 state path for the sandbox.
// Format: "tf-km/sandboxes/<sandbox-id>"
func (s *SandboxLocation) StatePath() string {
	return s.S3StatePath
}

// TagAPI is the interface subset of resourcegroupstaggingapi.Client used by
// FindSandboxByID. Defined here to enable mock-based unit testing.
type TagAPI interface {
	GetResources(
		ctx context.Context,
		params *resourcegroupstaggingapi.GetResourcesInput,
		optFns ...func(*resourcegroupstaggingapi.Options),
	) (*resourcegroupstaggingapi.GetResourcesOutput, error)
}

// FindSandboxByID queries the AWS Resource Groups Tagging API for all resources
// tagged with km:sandbox-id=<sandboxID>. Returns a SandboxLocation with the
// deterministic S3 state path and resource list. Returns ErrSandboxNotFound
// if no resources are tagged with the given sandbox ID.
func FindSandboxByID(ctx context.Context, client TagAPI, sandboxID string) (*SandboxLocation, error) {
	input := &resourcegroupstaggingapi.GetResourcesInput{
		TagFilters: []tagtypes.TagFilter{
			{
				Key:    aws.String("km:sandbox-id"),
				Values: []string{sandboxID},
			},
		},
	}

	output, err := client.GetResources(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("query AWS tag API for sandbox %s: %w", sandboxID, err)
	}

	if len(output.ResourceTagMappingList) == 0 {
		return nil, fmt.Errorf("%w: no resources tagged with km:sandbox-id=%s", ErrSandboxNotFound, sandboxID)
	}

	arns := make([]string, 0, len(output.ResourceTagMappingList))
	for _, r := range output.ResourceTagMappingList {
		if r.ResourceARN != nil {
			arns = append(arns, *r.ResourceARN)
		}
	}

	return &SandboxLocation{
		SandboxID:     sandboxID,
		S3StatePath:   "tf-km/sandboxes/" + sandboxID,
		ResourceCount: len(arns),
		ResourceARNs:  arns,
	}, nil
}
