// km unbootstrap is the symmetric teardown of km bootstrap. It removes the
// platform-foundational resources that km bootstrap creates (and that km
// uninit deliberately leaves alone): SSM parameters under the operator's
// resource_prefix, the artifacts S3 bucket, the Terraform state S3 bucket,
// the platform KMS key + alias, and (only with --include-zone) the
// sandboxes.{domain} Route53 hosted zone.
//
// Run AFTER km uninit. km uninit destroys regional infrastructure (VPCs,
// Lambdas, DDB tables, ECR repos); km unbootstrap clears the foundation
// underneath it. Running unbootstrap before uninit will fail because the
// state bucket still has live state for regional modules.
package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	smithy "github.com/aws/smithy-go"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
)

// UnbootstrapSSMAPI is the subset of the SSM client used by unbootstrap.
// Tests inject mocks; production wires *ssm.Client directly.
type UnbootstrapSSMAPI interface {
	GetParametersByPath(ctx context.Context, params *ssm.GetParametersByPathInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
	DeleteParameters(ctx context.Context, params *ssm.DeleteParametersInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParametersOutput, error)
}

// UnbootstrapS3API is the subset of the S3 client used by unbootstrap.
type UnbootstrapS3API interface {
	HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	ListObjectVersions(ctx context.Context, params *s3.ListObjectVersionsInput, optFns ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error)
	DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	DeleteBucket(ctx context.Context, params *s3.DeleteBucketInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketOutput, error)
}

// UnbootstrapKMSAPI is the subset of the KMS client used by unbootstrap.
type UnbootstrapKMSAPI interface {
	DescribeKey(ctx context.Context, params *kms.DescribeKeyInput, optFns ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
	ScheduleKeyDeletion(ctx context.Context, params *kms.ScheduleKeyDeletionInput, optFns ...func(*kms.Options)) (*kms.ScheduleKeyDeletionOutput, error)
	DeleteAlias(ctx context.Context, params *kms.DeleteAliasInput, optFns ...func(*kms.Options)) (*kms.DeleteAliasOutput, error)
}

// UnbootstrapRoute53API is the subset of Route53 used when --include-zone is set.
type UnbootstrapRoute53API interface {
	ListHostedZonesByName(ctx context.Context, params *route53.ListHostedZonesByNameInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesByNameOutput, error)
	ListResourceRecordSets(ctx context.Context, params *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error)
	ChangeResourceRecordSets(ctx context.Context, params *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error)
	DeleteHostedZone(ctx context.Context, params *route53.DeleteHostedZoneInput, optFns ...func(*route53.Options)) (*route53.DeleteHostedZoneOutput, error)
}

// UnbootstrapDeps groups the AWS clients unbootstrap needs. Pass nil for any
// client to skip the corresponding cleanup step (used by tests, and by the
// real path when --include-zone is false to avoid constructing a Route53
// client we won't use).
type UnbootstrapDeps struct {
	SSM     UnbootstrapSSMAPI
	S3      UnbootstrapS3API
	KMS     UnbootstrapKMSAPI
	Route53 UnbootstrapRoute53API
}

// UnbootstrapOpts captures the user-facing options for one unbootstrap run.
type UnbootstrapOpts struct {
	Region            string
	IncludeZone       bool
	KMSDeletionWindow int32 // 7-30; AWS rejects values outside this range
}

// NewUnbootstrapCmd creates the "km unbootstrap" command using real AWS clients.
func NewUnbootstrapCmd(cfg *config.Config) *cobra.Command {
	return newUnbootstrapCmdWithIO(cfg, os.Stdin, os.Stdout)
}

func newUnbootstrapCmdWithIO(cfg *config.Config, in io.Reader, out io.Writer) *cobra.Command {
	var (
		awsProfile        string
		region            string
		includeZone       bool
		kmsDeletionWindow int32
		yes               bool
	)

	cmd := &cobra.Command{
		Use:   "unbootstrap",
		Short: "Tear down platform-foundational resources (SSM params, buckets, KMS key, optionally DNS zone)",
		Long: `Symmetric teardown of km bootstrap. Removes:
  - All SSM parameters under /{prefix}/ (slack tokens, GitHub App, operator identity, etc.)
  - The artifacts S3 bucket (all object versions emptied first)
  - The Terraform state S3 bucket tf-{prefix}-state-{region-label}
  - The platform KMS key (scheduled for deletion with a 7-day window by default)
    and its alias alias/km-platform-{prefix}-{region-label}
  - With --include-zone: the sandboxes.{domain} Route53 hosted zone, after
    emptying all records EXCEPT the apex NS/SOA pair

Run AFTER km uninit (which clears regional infrastructure). Running this
before uninit will fail on the state bucket because terragrunt-managed
state for regional modules is still live.

Per-step failures are warnings; unbootstrap continues so partial cleanup
is still useful.`,
		RunE: func(c *cobra.Command, args []string) error {
			if !yes {
				fmt.Fprintf(out, "Tear down platform foundation in %s? This cannot be undone (KMS key has a 7-day grace period). [y/N] ", region)
				scanner := bufio.NewScanner(in)
				if !scanner.Scan() || (scanner.Text() != "y" && scanner.Text() != "Y" && scanner.Text() != "yes") {
					fmt.Fprintln(out, "Aborted.")
					return nil
				}
			}
			if awsProfile == "" {
				awsProfile = "klanker-application"
			}
			deps, err := buildUnbootstrapDeps(awsProfile, region, includeZone)
			if err != nil {
				return err
			}
			return RunUnbootstrapWithDeps(cfg, deps, UnbootstrapOpts{
				Region:            region,
				IncludeZone:       includeZone,
				KMSDeletionWindow: kmsDeletionWindow,
			}, out)
		},
	}

	cmd.Flags().StringVar(&awsProfile, "aws-profile", "klanker-application",
		"AWS CLI profile to use for teardown")
	cmd.Flags().StringVar(&region, "region", "us-east-1",
		"AWS region (used to derive the state bucket name and target the KMS key)")
	cmd.Flags().BoolVar(&includeZone, "include-zone", false,
		"Also delete the Route53 hosted zone for sandboxes.{domain}. Off by default because DNS takes longer to recreate than the rest")
	cmd.Flags().Int32Var(&kmsDeletionWindow, "kms-deletion-window", 7,
		"Pending-deletion window in days for the platform KMS key (AWS minimum 7, maximum 30)")
	cmd.Flags().BoolVar(&yes, "yes", false,
		"Skip confirmation prompt")

	return cmd
}

// buildUnbootstrapDeps wires real AWS clients. Returns nil for the Route53
// client when includeZone is false to avoid constructing one we won't use.
func buildUnbootstrapDeps(awsProfile, region string, includeZone bool) (UnbootstrapDeps, error) {
	ctx := context.Background()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithSharedConfigProfile(awsProfile),
	)
	if err != nil {
		return UnbootstrapDeps{}, fmt.Errorf("load AWS config: %w", err)
	}
	deps := UnbootstrapDeps{
		SSM: ssm.NewFromConfig(awsCfg),
		S3:  s3.NewFromConfig(awsCfg),
		KMS: kms.NewFromConfig(awsCfg),
	}
	if includeZone {
		deps.Route53 = route53.NewFromConfig(awsCfg)
	}
	return deps, nil
}

// RunUnbootstrapWithDeps is the testable core. Each step continues on error
// (warns to out) so partial cleanup is still useful when one resource is
// already gone or refuses to delete.
func RunUnbootstrapWithDeps(cfg *config.Config, deps UnbootstrapDeps, opts UnbootstrapOpts, out io.Writer) error {
	ctx := context.Background()
	region := opts.Region
	if region == "" {
		region = "us-east-1"
	}
	regionLabel := compiler.RegionLabel(region)

	fmt.Fprintf(out, "Unbootstrap %s (%s)\n", region, regionLabel)
	fmt.Fprintln(out, strings.Repeat("─", 46))

	// Step 1: Delete all SSM parameters under /{prefix}/. Recursive enumeration
	// handles slack/, config/, sandbox/, and any future sub-paths without code
	// changes. Order: SSM first because SSM SecureString writes need the KMS
	// key, but SSM deletes do not — safe to remove params before scheduling
	// the KMS key for deletion in step 4.
	ssmDeleted := 0
	if deps.SSM != nil {
		prefix := strings.TrimSuffix(cfg.GetSsmPrefix(), "/")
		count, err := deleteSSMParametersRecursive(ctx, deps.SSM, prefix, out)
		if err != nil {
			fmt.Fprintf(out, "  [warn] SSM cleanup partial: %v\n", err)
		}
		ssmDeleted = count
	}

	// Step 2: Empty + delete the artifacts S3 bucket. Versioning is enabled
	// on this bucket (per ensureArtifactsBucket in bootstrap.go), so a plain
	// delete-bucket would fail without first removing every version + delete
	// marker.
	artifactsDeleted := false
	if deps.S3 != nil && cfg.ArtifactsBucket != "" {
		if err := emptyAndDeleteBucket(ctx, deps.S3, cfg.ArtifactsBucket, out); err != nil {
			fmt.Fprintf(out, "  [warn] artifacts bucket %s teardown failed: %v\n", cfg.ArtifactsBucket, err)
		} else {
			artifactsDeleted = true
		}
	}

	// Step 3: Empty + delete the Terraform state bucket
	// (tf-{prefix}-state-{regionLabel}). After km uninit completes, the bucket
	// still holds inert state files but no live infrastructure references it,
	// so deletion is safe.
	stateBucket := fmt.Sprintf("tf-%s-state-%s", cfg.GetResourcePrefix(), regionLabel)
	stateDeleted := false
	if deps.S3 != nil {
		if err := emptyAndDeleteBucket(ctx, deps.S3, stateBucket, out); err != nil {
			fmt.Fprintf(out, "  [warn] state bucket %s teardown failed: %v\n", stateBucket, err)
		} else {
			stateDeleted = true
		}
	}

	// Step 4: Schedule platform KMS key for deletion + delete its alias.
	// AWS requires a 7-30 day pending window. Default is 7 (the minimum) so
	// re-bootstrap after the window doesn't trip on a still-pending key.
	kmsScheduled := false
	if deps.KMS != nil {
		alias := cfg.GetPlatformKMSAlias()
		window := opts.KMSDeletionWindow
		if window < 7 {
			window = 7
		}
		if window > 30 {
			window = 30
		}
		if err := scheduleKMSKeyDeletion(ctx, deps.KMS, alias, window, out); err != nil {
			fmt.Fprintf(out, "  [warn] KMS key teardown failed: %v\n", err)
		} else {
			kmsScheduled = true
		}
	}

	// Step 5: Optional Route53 zone teardown. Off by default — DNS takes
	// longer to recreate than the rest of the platform, and the zone often
	// holds operator-added records that aren't part of km.
	zoneDeleted := false
	if opts.IncludeZone && deps.Route53 != nil {
		zoneName := cfg.GetEmailDomain() + "." // FQDN always trailing-dot for Route53
		if err := deleteRoute53Zone(ctx, deps.Route53, zoneName, out); err != nil {
			fmt.Fprintf(out, "  [warn] Route53 zone %s teardown failed: %v\n", zoneName, err)
		} else {
			zoneDeleted = true
		}
	}

	// Summary
	fmt.Fprintln(out)
	fmt.Fprintln(out, strings.Repeat("─", 46))
	fmt.Fprintf(out, "Unbootstrap summary for %s:\n", region)
	fmt.Fprintf(out, "  SSM parameters deleted:  %d\n", ssmDeleted)
	fmt.Fprintf(out, "  Artifacts bucket gone:   %v (%s)\n", artifactsDeleted, cfg.ArtifactsBucket)
	fmt.Fprintf(out, "  State bucket gone:       %v (%s)\n", stateDeleted, stateBucket)
	fmt.Fprintf(out, "  KMS key scheduled:       %v (alias %s)\n", kmsScheduled, cfg.GetPlatformKMSAlias())
	if opts.IncludeZone {
		fmt.Fprintf(out, "  Route53 zone deleted:    %v (%s)\n", zoneDeleted, cfg.GetEmailDomain())
	} else {
		fmt.Fprintln(out, "  Route53 zone:            preserved (re-run with --include-zone to delete)")
	}
	return nil
}

// deleteSSMParametersRecursive enumerates /{prefix}/... via GetParametersByPath
// (Recursive=true) and deletes everything in batches of 10 (AWS DeleteParameters
// max). Returns the total deleted count and the first error encountered (if any).
func deleteSSMParametersRecursive(ctx context.Context, client UnbootstrapSSMAPI, prefix string, out io.Writer) (int, error) {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Deleting SSM parameters under %s/...\n", prefix)

	// Collect every name first; then batch-delete. The batch indirection lets
	// us print a single count rather than per-batch chatter.
	var names []string
	var nextToken *string
	for {
		page, err := client.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
			Path:           aws.String(prefix),
			Recursive:      aws.Bool(true),
			WithDecryption: aws.Bool(false), // we're just deleting, no need to decrypt
			NextToken:      nextToken,
			MaxResults:     aws.Int32(10), // AWS hard cap — values >10 fail validation
		})
		if err != nil {
			return 0, fmt.Errorf("enumerate ssm params: %w", err)
		}
		for _, p := range page.Parameters {
			if p.Name != nil {
				names = append(names, *p.Name)
			}
		}
		if page.NextToken == nil || *page.NextToken == "" {
			break
		}
		nextToken = page.NextToken
	}

	if len(names) == 0 {
		fmt.Fprintf(out, "  No parameters found under %s/\n", prefix)
		return 0, nil
	}

	fmt.Fprintf(out, "  Found %d parameter(s); deleting in batches of 10...\n", len(names))
	deleted := 0
	const batchSize = 10
	for i := 0; i < len(names); i += batchSize {
		end := i + batchSize
		if end > len(names) {
			end = len(names)
		}
		batch := names[i:end]
		resp, err := client.DeleteParameters(ctx, &ssm.DeleteParametersInput{
			Names: batch,
		})
		if err != nil {
			return deleted, fmt.Errorf("delete ssm batch starting at %d: %w", i, err)
		}
		deleted += len(resp.DeletedParameters)
		if len(resp.InvalidParameters) > 0 {
			fmt.Fprintf(out, "  [warn] %d parameter(s) reported invalid (already gone?): %v\n",
				len(resp.InvalidParameters), resp.InvalidParameters)
		}
	}
	fmt.Fprintf(out, "  Deleted %d SSM parameter(s)\n", deleted)
	return deleted, nil
}

// emptyAndDeleteBucket removes every version + delete marker from a versioned
// S3 bucket and then deletes the bucket itself. Returns nil if the bucket
// doesn't exist (idempotent across re-runs of unbootstrap).
func emptyAndDeleteBucket(ctx context.Context, client UnbootstrapS3API, bucket string, out io.Writer) error {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Tearing down S3 bucket %s...\n", bucket)

	// Existence check. Any "doesn't exist" signal means "already cleaned up".
	// HeadBucket has TWO ways to say not-found, and we have to handle both:
	//   1. Typed *s3types.NoSuchBucket (rare from HeadBucket, common from
	//      other ops like GetObject)
	//   2. Generic HTTP 404 wrapped in smithy.APIError with ErrorCode "NotFound"
	//      (what HeadBucket actually returns in practice — confirmed against
	//      real AWS in 2026-05).
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err != nil {
		var nsb *s3types.NoSuchBucket
		if errors.As(err, &nsb) {
			fmt.Fprintln(out, "  Bucket does not exist; nothing to do.")
			return nil
		}
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "NoSuchBucket") {
			fmt.Fprintln(out, "  Bucket does not exist; nothing to do.")
			return nil
		}
		// Any other HeadBucket error: surface it but don't try to delete.
		return fmt.Errorf("HeadBucket %s: %w", bucket, err)
	}

	// Empty all versions + delete markers in batches of up to 1000 (the S3
	// DeleteObjects max). A versioned bucket cannot be deleted while versions
	// remain, so this MUST run before DeleteBucket.
	versionsRemoved := 0
	var keyMarker, versionIDMarker *string
	for {
		listResp, listErr := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
			Bucket:          aws.String(bucket),
			KeyMarker:       keyMarker,
			VersionIdMarker: versionIDMarker,
		})
		if listErr != nil {
			return fmt.Errorf("list versions in %s: %w", bucket, listErr)
		}

		var ids []s3types.ObjectIdentifier
		for _, v := range listResp.Versions {
			ids = append(ids, s3types.ObjectIdentifier{Key: v.Key, VersionId: v.VersionId})
		}
		for _, dm := range listResp.DeleteMarkers {
			ids = append(ids, s3types.ObjectIdentifier{Key: dm.Key, VersionId: dm.VersionId})
		}
		if len(ids) > 0 {
			_, delErr := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(bucket),
				Delete: &s3types.Delete{Objects: ids, Quiet: aws.Bool(true)},
			})
			if delErr != nil {
				return fmt.Errorf("delete objects in %s: %w", bucket, delErr)
			}
			versionsRemoved += len(ids)
		}

		if listResp.IsTruncated == nil || !*listResp.IsTruncated {
			break
		}
		keyMarker = listResp.NextKeyMarker
		versionIDMarker = listResp.NextVersionIdMarker
	}
	if versionsRemoved > 0 {
		fmt.Fprintf(out, "  Removed %d object version(s) + delete marker(s)\n", versionsRemoved)
	}

	if _, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)}); err != nil {
		return fmt.Errorf("delete bucket %s: %w", bucket, err)
	}
	fmt.Fprintf(out, "  Bucket %s deleted\n", bucket)
	return nil
}

// scheduleKMSKeyDeletion looks the platform KMS key up by alias, schedules
// the underlying key for deletion (window in days), and removes the alias so
// a re-bootstrap can recreate alias→key cleanly. Returns nil if the alias
// doesn't exist.
func scheduleKMSKeyDeletion(ctx context.Context, client UnbootstrapKMSAPI, alias string, windowDays int32, out io.Writer) error {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Scheduling KMS key %s for deletion (%d-day window)...\n", alias, windowDays)

	// DescribeKey accepts an alias (alias/foo) or key id; resolves to the key.
	descResp, err := client.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(alias)})
	if err != nil {
		var nfe *kmstypes.NotFoundException
		if errors.As(err, &nfe) {
			fmt.Fprintf(out, "  Alias %s not found; nothing to do.\n", alias)
			return nil
		}
		return fmt.Errorf("describe key %s: %w", alias, err)
	}
	keyID := aws.ToString(descResp.KeyMetadata.KeyId)

	// If the key is already pending deletion, ScheduleKeyDeletion returns an
	// error — surface a friendlier message rather than failing the step.
	if descResp.KeyMetadata.KeyState == kmstypes.KeyStatePendingDeletion {
		fmt.Fprintf(out, "  Key %s already PendingDeletion; skipping ScheduleKeyDeletion.\n", keyID)
	} else {
		_, err = client.ScheduleKeyDeletion(ctx, &kms.ScheduleKeyDeletionInput{
			KeyId:               aws.String(keyID),
			PendingWindowInDays: aws.Int32(windowDays),
		})
		if err != nil {
			return fmt.Errorf("schedule deletion of %s: %w", keyID, err)
		}
		fmt.Fprintf(out, "  Key %s scheduled for deletion in %d days\n", keyID, windowDays)
	}

	// Delete the alias so a re-bootstrap can attach a fresh alias→key mapping.
	// AWS allows alias deletion while the underlying key is PendingDeletion.
	if _, err := client.DeleteAlias(ctx, &kms.DeleteAliasInput{AliasName: aws.String(alias)}); err != nil {
		var nfe *kmstypes.NotFoundException
		if !errors.As(err, &nfe) {
			return fmt.Errorf("delete alias %s: %w", alias, err)
		}
	}
	fmt.Fprintf(out, "  Alias %s removed\n", alias)
	return nil
}

// deleteRoute53Zone empties every record in the zone EXCEPT the apex NS and
// SOA pair (Route53 rejects deletion of those alongside the zone), then
// deletes the zone. Used only with --include-zone.
func deleteRoute53Zone(ctx context.Context, client UnbootstrapRoute53API, zoneName string, out io.Writer) error {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Deleting Route53 zone %s...\n", zoneName)

	// ListHostedZonesByName scans alphabetically; we still need to filter by
	// exact name match because the API returns multiple results when several
	// zones share a prefix.
	listResp, err := client.ListHostedZonesByName(ctx, &route53.ListHostedZonesByNameInput{
		DNSName:  aws.String(zoneName),
		MaxItems: aws.Int32(10),
	})
	if err != nil {
		return fmt.Errorf("list zones: %w", err)
	}
	var zoneID string
	for _, z := range listResp.HostedZones {
		if aws.ToString(z.Name) == zoneName {
			zoneID = aws.ToString(z.Id)
			break
		}
	}
	if zoneID == "" {
		fmt.Fprintf(out, "  Zone %s not found; nothing to do.\n", zoneName)
		return nil
	}

	// Enumerate records and delete everything that isn't the apex NS/SOA.
	// AWS requires record-set deletions to specify the exact RR name, type,
	// TTL, and rdata, so we feed the listed values straight back into Change.
	var nextRecordName *string
	var nextRecordType r53types.RRType
	deleted := 0
	for {
		recsResp, err := client.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
			HostedZoneId:    aws.String(zoneID),
			StartRecordName: nextRecordName,
			StartRecordType: nextRecordType,
		})
		if err != nil {
			return fmt.Errorf("list records: %w", err)
		}
		var changes []r53types.Change
		for i := range recsResp.ResourceRecordSets {
			rr := recsResp.ResourceRecordSets[i]
			// Apex NS/SOA must remain — Route53 deletes them automatically
			// when the zone itself is deleted.
			if aws.ToString(rr.Name) == zoneName && (rr.Type == r53types.RRTypeNs || rr.Type == r53types.RRTypeSoa) {
				continue
			}
			changes = append(changes, r53types.Change{
				Action:            r53types.ChangeActionDelete,
				ResourceRecordSet: &rr,
			})
		}
		if len(changes) > 0 {
			_, err := client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
				HostedZoneId: aws.String(zoneID),
				ChangeBatch:  &r53types.ChangeBatch{Changes: changes},
			})
			if err != nil {
				return fmt.Errorf("delete records: %w", err)
			}
			deleted += len(changes)
		}
		if !recsResp.IsTruncated {
			break
		}
		nextRecordName = recsResp.NextRecordName
		nextRecordType = recsResp.NextRecordType
	}
	if deleted > 0 {
		fmt.Fprintf(out, "  Deleted %d record(s)\n", deleted)
	}

	if _, err := client.DeleteHostedZone(ctx, &route53.DeleteHostedZoneInput{Id: aws.String(zoneID)}); err != nil {
		return fmt.Errorf("delete zone %s: %w", zoneName, err)
	}
	fmt.Fprintf(out, "  Zone %s deleted\n", zoneName)
	return nil
}

// Compile-time guard: ensure runtime types satisfy the interfaces. Keeps
// signature drift in the real SDK from breaking us silently.
var (
	_ UnbootstrapSSMAPI     = (*ssm.Client)(nil)
	_ UnbootstrapS3API      = (*s3.Client)(nil)
	_ UnbootstrapKMSAPI     = (*kms.Client)(nil)
	_ UnbootstrapRoute53API = (*route53.Client)(nil)
)
