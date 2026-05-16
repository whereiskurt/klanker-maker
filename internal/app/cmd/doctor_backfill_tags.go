package cmd

// doctor_backfill_tags.go — Phase 82 Plan 05
//
// Implements `km doctor --backfill-tags`: sweeps account-scoped resources tagged
// with km:sandbox-id=*, cross-references each sandbox-id against THIS install's
// DynamoDB sandbox table, and applies km:resource-prefix=<prefix> ONLY to resources
// that belong to this install. Resources from other installs are skipped.
//
// CRITICAL safety guard (RESEARCH.md Pitfall 4): tag:GetResources returns resources
// from ALL installs in the account. We only tag resources whose sandbox-id is in
// this install's DDB table.

import (
	"context"
	"fmt"
	"io"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	taggingtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

// BackfillReport summarises the result of a single backfill run.
type BackfillReport struct {
	Tagged                int
	SkippedForeignPrefix  int
	SkippedUnknownSandbox int
	SkippedAlreadyTagged  int
	Errored               int
	ErroredARNs           []string
}

// TaggingGetResourcesAPI is the narrow interface for listing tagged resources.
// *resourcegroupstaggingapi.Client satisfies this directly.
type TaggingGetResourcesAPI interface {
	GetResources(ctx context.Context, params *resourcegroupstaggingapi.GetResourcesInput, optFns ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.GetResourcesOutput, error)
}

// TaggingTagResourcesAPI is the narrow interface for applying tags to resources.
// *resourcegroupstaggingapi.Client satisfies this directly.
type TaggingTagResourcesAPI interface {
	TagResources(ctx context.Context, params *resourcegroupstaggingapi.TagResourcesInput, optFns ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.TagResourcesOutput, error)
}

// BackfillTaggingAPI combines both tagging operations (GetResources + TagResources).
// *resourcegroupstaggingapi.Client satisfies this directly.
type BackfillTaggingAPI interface {
	TaggingGetResourcesAPI
	TaggingTagResourcesAPI
}

// BackfillDDBAPI is the narrow DynamoDB interface needed for the cross-install lookup.
type BackfillDDBAPI interface {
	GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
}

// runBackfillTags implements the km doctor --backfill-tags operation.
//
// Algorithm per resource from tag:GetResources(tag:km:sandbox-id=*):
//  1. Parse km:sandbox-id tag value.
//  2. If km:resource-prefix tag already present AND equals currentPrefix → skip (SkippedAlreadyTagged).
//  3. If km:resource-prefix tag already present AND != currentPrefix → skip (SkippedForeignPrefix).
//  4. If km:resource-prefix absent → DDB GetItem keyed on sandbox-id.
//     - Found → eligible for tagging.
//     - Not found → skip (SkippedUnknownSandbox).
//
// Eligible ARNs are batched (up to 20 per call) and written via tag:TagResources.
// On dryRun=true the TagResources call is skipped and the plan is printed only.
func runBackfillTags(
	ctx context.Context,
	currentPrefix string,
	sandboxTable string,
	taggingClient BackfillTaggingAPI,
	ddbClient BackfillDDBAPI,
	dryRun bool,
	w io.Writer,
) (BackfillReport, error) {
	var report BackfillReport

	// Paginate through all resources tagged with km:sandbox-id.
	input := &resourcegroupstaggingapi.GetResourcesInput{
		TagFilters: []taggingtypes.TagFilter{
			{Key: awssdk.String("km:sandbox-id")},
		},
	}

	// We use manual pagination (PaginationToken) so our BackfillTaggingAPI interface
	// stays minimal (no paginator constructor required in the interface, keeps mocks simple).
	var paginationToken *string
	for {
		input.PaginationToken = paginationToken
		out, err := taggingClient.GetResources(ctx, input)
		if err != nil {
			return report, fmt.Errorf("GetResources: %w", err)
		}

		eligible := []string{}

		for _, mapping := range out.ResourceTagMappingList {
			arn := awssdk.ToString(mapping.ResourceARN)

			// Parse km:sandbox-id and km:resource-prefix from the tag list.
			sandboxID := ""
			existingPrefix := ""
			for _, t := range mapping.Tags {
				switch awssdk.ToString(t.Key) {
				case "km:sandbox-id":
					sandboxID = awssdk.ToString(t.Value)
				case "km:resource-prefix":
					existingPrefix = awssdk.ToString(t.Value)
				}
			}

			if sandboxID == "" {
				// No sandbox-id tag — should not happen given the filter, but skip safely.
				report.SkippedUnknownSandbox++
				continue
			}

			// Step 2 & 3: km:resource-prefix tag already present.
			if existingPrefix != "" {
				if existingPrefix == currentPrefix {
					report.SkippedAlreadyTagged++
					fmt.Fprintf(w, "  - %s  ⚠ already tagged km:resource-prefix=%s\n", arn, existingPrefix)
				} else {
					report.SkippedForeignPrefix++
					fmt.Fprintf(w, "  - %s  ⚠ skipped (foreign prefix %q)\n", arn, existingPrefix)
				}
				continue
			}

			// Step 4: No km:resource-prefix tag — cross-reference sandbox-id against DDB.
			getOut, err := ddbClient.GetItem(ctx, &dynamodb.GetItemInput{
				TableName: awssdk.String(sandboxTable),
				Key: map[string]dynamodbtypes.AttributeValue{
					"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
				},
			})
			if err != nil {
				return report, fmt.Errorf("DDB GetItem sandbox_id=%s: %w", sandboxID, err)
			}

			if len(getOut.Item) == 0 {
				// sandbox-id not found in this install's DDB — skip (may belong to another install).
				report.SkippedUnknownSandbox++
				fmt.Fprintf(w, "  - %s  ⚠ skipped (sandbox-id %q not in this install's DDB)\n", arn, sandboxID)
				continue
			}

			// Eligible: sandbox belongs to this install, no prefix tag yet.
			eligible = append(eligible, arn)
		}

		// Batch TagResources in groups of 20.
		if err := applyTags(ctx, taggingClient, eligible, currentPrefix, dryRun, w, &report); err != nil {
			return report, err
		}

		// Advance pagination.
		if awssdk.ToString(out.PaginationToken) == "" {
			break
		}
		paginationToken = out.PaginationToken
	}

	// Print summary.
	mode := "dry-run"
	if !dryRun {
		mode = "applied"
	}
	fmt.Fprintf(w, "\n%s Backfill summary (%s):\n", checkOKSymbol, mode)
	fmt.Fprintf(w, "  Tagged:                  %d\n", report.Tagged)
	fmt.Fprintf(w, "  SkippedAlreadyTagged:    %d\n", report.SkippedAlreadyTagged)
	fmt.Fprintf(w, "  SkippedForeignPrefix:    %d\n", report.SkippedForeignPrefix)
	fmt.Fprintf(w, "  SkippedUnknownSandbox:   %d\n", report.SkippedUnknownSandbox)
	fmt.Fprintf(w, "  Errored:                 %d\n", report.Errored)

	return report, nil
}

// applyTags batches the eligible ARNs into groups of 20 and calls TagResources.
// On dryRun=true it prints what would be tagged without calling the API.
func applyTags(
	ctx context.Context,
	client TaggingTagResourcesAPI,
	arns []string,
	prefix string,
	dryRun bool,
	w io.Writer,
	report *BackfillReport,
) error {
	const batchSize = 20
	for i := 0; i < len(arns); i += batchSize {
		end := i + batchSize
		if end > len(arns) {
			end = len(arns)
		}
		batch := arns[i:end]

		if dryRun {
			for _, arn := range batch {
				fmt.Fprintf(w, "  + %s  → km:resource-prefix=%s  (dry-run)\n", arn, prefix)
				report.Tagged++
			}
			continue
		}

		// Call TagResources.
		out, err := client.TagResources(ctx, &resourcegroupstaggingapi.TagResourcesInput{
			ResourceARNList: batch,
			Tags: map[string]string{
				"km:resource-prefix": prefix,
			},
		})
		if err != nil {
			return fmt.Errorf("TagResources: %w", err)
		}

		// Check per-resource failures in the response.
		for _, arn := range batch {
			if fi, failed := out.FailedResourcesMap[arn]; failed {
				report.Errored++
				report.ErroredARNs = append(report.ErroredARNs, arn)
				fmt.Fprintf(w, "  %s %s  tagging failed: %s\n", checkErrorSymbol, arn, awssdk.ToString(fi.ErrorMessage))
			} else {
				report.Tagged++
				fmt.Fprintf(w, "  %s %s  → km:resource-prefix=%s\n", checkOKSymbol, arn, prefix)
			}
		}
	}
	return nil
}
