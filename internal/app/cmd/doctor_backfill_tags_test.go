package cmd

// doctor_backfill_tags_test.go — Phase 82 Plan 05
//
// Tests for runBackfillTags:
//   - TestBackfillTags_CrossInstallGuard: verifies that resources from other installs
//     (sandbox-id not in this install's DDB) are NOT tagged, and resources from
//     this install ARE tagged.
//   - TestBackfillTags_Idempotent: verifies that running the backfill twice does not
//     double-tag or error on already-tagged resources.

import (
	"bytes"
	"context"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	taggingtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

// =============================================================================
// Mock: BackfillTaggingAPI
// =============================================================================

// mockBackfillTaggingClient implements BackfillTaggingAPI.
// resources is the mutable in-memory list of ResourceTagMappings.
// tagResourcesCalls tracks all TagResources invocations.
type mockBackfillTaggingClient struct {
	resources       []taggingtypes.ResourceTagMapping
	tagResourcesLog [][]string // each entry is the ARN list from one call
}

func (m *mockBackfillTaggingClient) GetResources(
	ctx context.Context,
	params *resourcegroupstaggingapi.GetResourcesInput,
	optFns ...func(*resourcegroupstaggingapi.Options),
) (*resourcegroupstaggingapi.GetResourcesOutput, error) {
	return &resourcegroupstaggingapi.GetResourcesOutput{
		ResourceTagMappingList: m.resources,
	}, nil
}

func (m *mockBackfillTaggingClient) TagResources(
	ctx context.Context,
	params *resourcegroupstaggingapi.TagResourcesInput,
	optFns ...func(*resourcegroupstaggingapi.Options),
) (*resourcegroupstaggingapi.TagResourcesOutput, error) {
	// Record call.
	m.tagResourcesLog = append(m.tagResourcesLog, params.ResourceARNList)

	// Mutate the in-memory resources: apply the new tags so subsequent GetResources
	// calls reflect the updated state (supports idempotency test).
	for _, arn := range params.ResourceARNList {
		for i, r := range m.resources {
			if awssdk.ToString(r.ResourceARN) == arn {
				for k, v := range params.Tags {
					k, v := k, v
					m.resources[i].Tags = append(m.resources[i].Tags, taggingtypes.Tag{
						Key:   awssdk.String(k),
						Value: awssdk.String(v),
					})
				}
				break
			}
		}
	}

	return &resourcegroupstaggingapi.TagResourcesOutput{
		FailedResourcesMap: map[string]taggingtypes.FailureInfo{},
	}, nil
}

// =============================================================================
// Mock: BackfillDDBAPI
// =============================================================================

// mockBackfillDDBClient implements BackfillDDBAPI.
// knownIDs is the set of sandbox_id values present in this install's DDB table.
type mockBackfillDDBClient struct {
	knownIDs map[string]bool
}

func (m *mockBackfillDDBClient) GetItem(
	ctx context.Context,
	input *dynamodb.GetItemInput,
	optFns ...func(*dynamodb.Options),
) (*dynamodb.GetItemOutput, error) {
	// Extract sandbox_id from the key.
	var sandboxID string
	if attr, ok := input.Key["sandbox_id"]; ok {
		if sv, ok := attr.(*dynamodbtypes.AttributeValueMemberS); ok {
			sandboxID = sv.Value
		}
	}

	if m.knownIDs[sandboxID] {
		// Return a minimal item to signal "found".
		return &dynamodb.GetItemOutput{
			Item: map[string]dynamodbtypes.AttributeValue{
				"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
			},
		}, nil
	}
	// DDB semantics: not found → empty Item (not an error).
	return &dynamodb.GetItemOutput{}, nil
}

// =============================================================================
// Helpers
// =============================================================================

// buildResource creates a ResourceTagMapping with the given ARN and tags.
func buildResource(arn string, tags map[string]string) taggingtypes.ResourceTagMapping {
	var t []taggingtypes.Tag
	for k, v := range tags {
		k, v := k, v
		t = append(t, taggingtypes.Tag{Key: awssdk.String(k), Value: awssdk.String(v)})
	}
	return taggingtypes.ResourceTagMapping{
		ResourceARN: awssdk.String(arn),
		Tags:        t,
	}
}

// =============================================================================
// TestBackfillTags_CrossInstallGuard
// =============================================================================

// TestBackfillTags_CrossInstallGuard verifies the mandatory cross-install safety guard:
//
//  1. sandbox-id "km-aaaa1111" is in this install's DDB → resource MUST be tagged
//     (report.Tagged == 1).
//  2. sandbox-id "rg-bbbb2222" is NOT in this install's DDB → resource MUST be skipped
//     (report.SkippedUnknownSandbox == 1).
//  3. Resource with sandbox-id "km-cccc3333" already has km:resource-prefix=km →
//     MUST be reported as already-tagged (report.SkippedAlreadyTagged == 1).
func TestBackfillTags_CrossInstallGuard(t *testing.T) {
	const currentPrefix = "km"
	const sandboxTable = "km-sandboxes"

	// Three resources:
	// 1. Belongs to this install — eligible.
	res1 := buildResource("arn:aws:ec2:us-east-1:123456789012:instance/i-aaaa1111", map[string]string{
		"km:sandbox-id": "km-aaaa1111",
	})
	// 2. Foreign install (sandbox-id not in this install's DDB).
	res2 := buildResource("arn:aws:ec2:us-east-1:123456789012:instance/i-bbbb2222", map[string]string{
		"km:sandbox-id": "rg-bbbb2222",
	})
	// 3. Already tagged correctly.
	res3 := buildResource("arn:aws:ec2:us-east-1:123456789012:instance/i-cccc3333", map[string]string{
		"km:sandbox-id":      "km-cccc3333",
		"km:resource-prefix": "km",
	})

	taggingMock := &mockBackfillTaggingClient{
		resources: []taggingtypes.ResourceTagMapping{res1, res2, res3},
	}
	ddbMock := &mockBackfillDDBClient{
		knownIDs: map[string]bool{"km-aaaa1111": true}, // only km-aaaa1111 known
	}

	var buf bytes.Buffer
	report, err := runBackfillTags(context.Background(), currentPrefix, sandboxTable, taggingMock, ddbMock, false, &buf)
	if err != nil {
		t.Fatalf("runBackfillTags returned unexpected error: %v", err)
	}

	if report.Tagged != 1 {
		t.Errorf("Tagged: got %d, want 1", report.Tagged)
	}
	if report.SkippedUnknownSandbox != 1 {
		t.Errorf("SkippedUnknownSandbox: got %d, want 1", report.SkippedUnknownSandbox)
	}
	if report.SkippedAlreadyTagged != 1 {
		t.Errorf("SkippedAlreadyTagged: got %d, want 1", report.SkippedAlreadyTagged)
	}
	if report.SkippedForeignPrefix != 0 {
		t.Errorf("SkippedForeignPrefix: got %d, want 0", report.SkippedForeignPrefix)
	}
	if report.Errored != 0 {
		t.Errorf("Errored: got %d, want 0", report.Errored)
	}

	// Verify that TagResources was called exactly once with exactly one ARN.
	if len(taggingMock.tagResourcesLog) != 1 {
		t.Fatalf("TagResources call count: got %d, want 1", len(taggingMock.tagResourcesLog))
	}
	calledARNs := taggingMock.tagResourcesLog[0]
	if len(calledARNs) != 1 {
		t.Fatalf("TagResources ARN count: got %d, want 1; ARNs: %v", len(calledARNs), calledARNs)
	}
	wantARN := "arn:aws:ec2:us-east-1:123456789012:instance/i-aaaa1111"
	if calledARNs[0] != wantARN {
		t.Errorf("TagResources ARN: got %q, want %q", calledARNs[0], wantARN)
	}
}

// =============================================================================
// TestBackfillTags_Idempotent
// =============================================================================

// TestBackfillTags_Idempotent verifies that running the backfill twice produces
// the expected idempotent result on the second run.
//
// After run 1: res1 gets tagged (Tagged=1), res2 skipped as unknown (SkippedUnknownSandbox=1),
// res3 already tagged (SkippedAlreadyTagged=1).
//
// Run 2 (same mock state after mutation by run 1):
//   - res1 now has km:resource-prefix=km → SkippedAlreadyTagged.
//   - res2 still unknown sandbox-id → SkippedUnknownSandbox.
//   - res3 still already tagged → SkippedAlreadyTagged.
//
// Expected: Tagged=0, SkippedAlreadyTagged=2, SkippedUnknownSandbox=1.
func TestBackfillTags_Idempotent(t *testing.T) {
	const currentPrefix = "km"
	const sandboxTable = "km-sandboxes"

	res1 := buildResource("arn:aws:ec2:us-east-1:123456789012:instance/i-aaaa1111", map[string]string{
		"km:sandbox-id": "km-aaaa1111",
	})
	res2 := buildResource("arn:aws:ec2:us-east-1:123456789012:instance/i-bbbb2222", map[string]string{
		"km:sandbox-id": "rg-bbbb2222",
	})
	res3 := buildResource("arn:aws:ec2:us-east-1:123456789012:instance/i-cccc3333", map[string]string{
		"km:sandbox-id":      "km-cccc3333",
		"km:resource-prefix": "km",
	})

	taggingMock := &mockBackfillTaggingClient{
		resources: []taggingtypes.ResourceTagMapping{res1, res2, res3},
	}
	ddbMock := &mockBackfillDDBClient{
		knownIDs: map[string]bool{"km-aaaa1111": true},
	}

	// Run 1 — should tag res1.
	var buf1 bytes.Buffer
	report1, err := runBackfillTags(context.Background(), currentPrefix, sandboxTable, taggingMock, ddbMock, false, &buf1)
	if err != nil {
		t.Fatalf("run 1 returned unexpected error: %v", err)
	}
	if report1.Tagged != 1 {
		t.Errorf("run 1 Tagged: got %d, want 1", report1.Tagged)
	}

	// Run 2 — the mock's resources now include km:resource-prefix=km on res1 (mutated by run 1).
	var buf2 bytes.Buffer
	report2, err := runBackfillTags(context.Background(), currentPrefix, sandboxTable, taggingMock, ddbMock, false, &buf2)
	if err != nil {
		t.Fatalf("run 2 returned unexpected error: %v", err)
	}

	// After run 2: no new tagging.
	if report2.Tagged != 0 {
		t.Errorf("run 2 Tagged: got %d, want 0", report2.Tagged)
	}
	// res1 and res3 now both have km:resource-prefix=km → SkippedAlreadyTagged.
	if report2.SkippedAlreadyTagged != 2 {
		t.Errorf("run 2 SkippedAlreadyTagged: got %d, want 2", report2.SkippedAlreadyTagged)
	}
	// res2 still has unknown sandbox-id.
	if report2.SkippedUnknownSandbox != 1 {
		t.Errorf("run 2 SkippedUnknownSandbox: got %d, want 1", report2.SkippedUnknownSandbox)
	}
	if report2.Errored != 0 {
		t.Errorf("run 2 Errored: got %d, want 0", report2.Errored)
	}
}
