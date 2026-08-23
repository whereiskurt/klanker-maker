// account_register.go implements the home-account half of Phase 126 enrollment:
// `km account register`, `km account list`, and `km account rm`. Where
// account.go's `km account add` runs with the TARGET account's credentials,
// every function in this file runs with the HOME account's ("account A")
// credentials and touches exactly three things on that side: a
// km-config.yaml `launch_accounts.<name>` entry, a secure SSM parameter
// carrying the STS ExternalId, and one identifiable statement on the home
// artifacts bucket's policy.
//
// Artifacts-bucket-policy ownership re-confirmed for this plan (per the
// plan's "Verified precondition"): ensureArtifactsBucket (bootstrap.go)
// creates the artifacts bucket directly through the S3 API — versioning,
// public-access-block and no bucket-policy resource — and no Terraform
// module in this repository declares an aws_s3_bucket_policy for it. A
// read-modify-write of that bucket's policy from Go is therefore safe from
// being silently reverted by a later `km init` apply. This is UNLIKE the
// mail bucket (infra/modules/ses/v2.0.0/main.tf's aws_s3_bucket_policy.mail),
// which is Terraform-owned and carries an explicit "only ONE policy resource
// per bucket" comment — that bucket must never be touched by this file.
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	smithy "github.com/aws/smithy-go"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
	"github.com/whereiskurt/klanker-maker/pkg/terragrunt"
)

// ======================== Artifacts bucket grant ================================

// ArtifactsPolicyS3API is the narrow S3 interface UpsertArtifactsReadGrant and
// RemoveArtifactsReadGrant use against the HOME artifacts bucket. Satisfied by
// *s3.Client in production; tests inject a stub.
type ArtifactsPolicyS3API interface {
	GetBucketPolicy(ctx context.Context, params *s3.GetBucketPolicyInput, optFns ...func(*s3.Options)) (*s3.GetBucketPolicyOutput, error)
	PutBucketPolicy(ctx context.Context, params *s3.PutBucketPolicyInput, optFns ...func(*s3.Options)) (*s3.PutBucketPolicyOutput, error)
	DeleteBucketPolicy(ctx context.Context, params *s3.DeleteBucketPolicyInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketPolicyOutput, error)
}

// NewArtifactsPolicyS3Client is the factory tests override to inject a stub
// ArtifactsPolicyS3API.
var NewArtifactsPolicyS3Client = func(cfg awssdk.Config) ArtifactsPolicyS3API {
	return s3.NewFromConfig(cfg)
}

// artifactsPolicyDocument and artifactsPolicyStatement are a minimal IAM JSON
// policy document shape. Principal/Action/Resource/Condition are typed
// interface{} so an UNRELATED existing statement (any shape Sid/Effect
// notwithstanding) round-trips through Get -> Put byte-for-byte-equivalent
// (T-126-37) — only a statement carrying THIS link's Sid is ever replaced.
type artifactsPolicyDocument struct {
	Version   string                     `json:"Version"`
	Statement []artifactsPolicyStatement `json:"Statement"`
}

type artifactsPolicyStatement struct {
	Sid       string      `json:"Sid,omitempty"`
	Effect    string      `json:"Effect"`
	Principal interface{} `json:"Principal,omitempty"`
	Action    interface{} `json:"Action"`
	Resource  interface{} `json:"Resource,omitempty"`
	Condition interface{} `json:"Condition,omitempty"`
}

// artifactsGrantSid derives the deterministic Sid used to scope both the
// upsert and the removal of a link's artifacts-bucket grant statement, scoped
// by the install's resource prefix (multi-instance support: two installs in
// the same AWS account must never derive the same Sid). Factored into one
// helper so register and rm can never derive it differently and orphan a
// grant (T-126-38).
func artifactsGrantSid(prefix, linkName string) string {
	return prefix + "-account-link-" + linkName + "-read"
}

// isS3NoSuchBucketPolicy reports whether err is S3's "this bucket has no
// policy attached" error — the expected, non-fatal outcome of GetBucketPolicy
// against a bucket that has never had a policy written to it.
func isS3NoSuchBucketPolicy(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "NoSuchBucketPolicy"
	}
	return false
}

// getArtifactsPolicyDocument fetches and parses the bucket's current policy,
// treating "no policy attached" as an empty (but valid) document rather than
// an error — the read half of the read-modify-write.
func getArtifactsPolicyDocument(ctx context.Context, api ArtifactsPolicyS3API, bucket string) (*artifactsPolicyDocument, error) {
	out, err := api.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: awssdk.String(bucket)})
	if err != nil {
		if isS3NoSuchBucketPolicy(err) {
			return &artifactsPolicyDocument{Version: "2012-10-17"}, nil
		}
		return nil, fmt.Errorf("get bucket policy for %s: %w", bucket, err)
	}
	if out.Policy == nil || *out.Policy == "" {
		return &artifactsPolicyDocument{Version: "2012-10-17"}, nil
	}
	var doc artifactsPolicyDocument
	if err := json.Unmarshal([]byte(*out.Policy), &doc); err != nil {
		return nil, fmt.Errorf("parsing existing bucket policy for %s: %w", bucket, err)
	}
	if doc.Version == "" {
		doc.Version = "2012-10-17"
	}
	return &doc, nil
}

// putArtifactsPolicyDocument is the write half of the read-modify-write.
func putArtifactsPolicyDocument(ctx context.Context, api ArtifactsPolicyS3API, bucket string, doc *artifactsPolicyDocument) error {
	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshaling bucket policy for %s: %w", bucket, err)
	}
	if _, err := api.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: awssdk.String(bucket),
		Policy: awssdk.String(string(data)),
	}); err != nil {
		return fmt.Errorf("put bucket policy for %s: %w", bucket, err)
	}
	return nil
}

// dropStatementsBySid returns stmts with every statement carrying sid
// removed, plus whether any were found (used by RemoveArtifactsReadGrant to
// stay idempotent).
func dropStatementsBySid(stmts []artifactsPolicyStatement, sid string) (filtered []artifactsPolicyStatement, found bool) {
	filtered = make([]artifactsPolicyStatement, 0, len(stmts))
	for _, s := range stmts {
		if s.Sid == sid {
			found = true
			continue
		}
		filtered = append(filtered, s)
	}
	return filtered, found
}

// UpsertArtifactsReadGrant grants principalARN (the link's box role) object-
// read and bucket-list access to the home artifacts bucket, scoped by sid.
// Read-modify-write: every existing statement not carrying sid is preserved
// verbatim (T-126-37); a statement already carrying sid is replaced, not
// duplicated (idempotent re-registration). The generated statement contains
// ONLY s3:GetObject and s3:ListBucket — no write action anywhere in it
// (T-126-36, asserted by the test suite against this statement's Action
// list, not against source text).
func UpsertArtifactsReadGrant(ctx context.Context, api ArtifactsPolicyS3API, bucket, sid, principalARN string) error {
	doc, err := getArtifactsPolicyDocument(ctx, api, bucket)
	if err != nil {
		return err
	}
	doc.Statement, _ = dropStatementsBySid(doc.Statement, sid)

	bucketARN := "arn:aws:s3:::" + bucket
	doc.Statement = append(doc.Statement, artifactsPolicyStatement{
		Sid:       sid,
		Effect:    "Allow",
		Principal: map[string]string{"AWS": principalARN},
		Action:    []string{"s3:GetObject", "s3:ListBucket"},
		Resource:  []string{bucketARN, bucketARN + "/*"},
	})
	return putArtifactsPolicyDocument(ctx, api, bucket, doc)
}

// RemoveArtifactsReadGrant drops the statement carrying sid, preserving every
// other statement untouched. Idempotent: a bucket with no matching statement
// (already removed, or never granted) is not an error and performs no write.
// When removing the grant empties the policy entirely, the policy is deleted
// outright — PutBucketPolicy rejects an empty Statement array.
func RemoveArtifactsReadGrant(ctx context.Context, api ArtifactsPolicyS3API, bucket, sid string) error {
	doc, err := getArtifactsPolicyDocument(ctx, api, bucket)
	if err != nil {
		return err
	}
	filtered, found := dropStatementsBySid(doc.Statement, sid)
	if !found {
		return nil
	}
	if len(filtered) == 0 {
		if _, err := api.DeleteBucketPolicy(ctx, &s3.DeleteBucketPolicyInput{Bucket: awssdk.String(bucket)}); err != nil {
			return fmt.Errorf("delete now-empty bucket policy for %s: %w", bucket, err)
		}
		return nil
	}
	doc.Statement = filtered
	return putArtifactsPolicyDocument(ctx, api, bucket, doc)
}

// ======================== External-id secure parameter ==========================

// AccountSSMAPI is the narrow SSM interface RunAccountRegister/RunAccountRm
// use to write and delete the link's external-id secure parameter. Satisfied
// by *ssm.Client in production; tests inject a stub. Kept local to this file
// (rather than reusing the shared ssmParamStoreClient/productionSSMParamStore
// pair from create_slack.go) because that pair has no Delete capability and
// widening it would ripple into every other caller of productionSSMParamStore.
type AccountSSMAPI interface {
	PutParameter(ctx context.Context, params *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
	DeleteParameter(ctx context.Context, params *ssm.DeleteParameterInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error)
}

// NewAccountSSMClientFunc is the factory tests override to inject a stub AccountSSMAPI.
var NewAccountSSMClientFunc = func(cfg awssdk.Config) AccountSSMAPI {
	return ssm.NewFromConfig(cfg)
}

// externalIDParamPath derives the SSM SecureString path for a link's external
// id: /{prefix}/launch-accounts/{name}/external-id. This is the ONLY path
// ExternalIDSSM ever records (T-126-35) — the raw secret is never written to
// km-config.yaml.
func externalIDParamPath(cfg *config.Config, name string) string {
	return cfg.GetSsmPrefix() + "launch-accounts/" + name + "/external-id"
}

// putSecureParameter writes (overwriting) path as an SSM SecureString — the
// only place in this file that ever handles the external id's raw value.
func putSecureParameter(ctx context.Context, api AccountSSMAPI, path, value string) error {
	if _, err := api.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      awssdk.String(path),
		Value:     awssdk.String(value),
		Type:      ssmtypes.ParameterTypeSecureString,
		Overwrite: awssdk.Bool(true),
	}); err != nil {
		return fmt.Errorf("SSM PutParameter %s: %w", path, err)
	}
	return nil
}

// isSSMParameterNotFound reports whether err is SSM's ParameterNotFound.
func isSSMParameterNotFound(err error) bool {
	var nf *ssmtypes.ParameterNotFound
	return errors.As(err, &nf)
}

// deleteParameterIdempotent deletes path, treating "already gone" as success.
func deleteParameterIdempotent(ctx context.Context, api AccountSSMAPI, path string) error {
	if path == "" {
		return nil
	}
	if _, err := api.DeleteParameter(ctx, &ssm.DeleteParameterInput{Name: awssdk.String(path)}); err != nil {
		if isSSMParameterNotFound(err) {
			return nil
		}
		return fmt.Errorf("SSM DeleteParameter %s: %w", path, err)
	}
	return nil
}

// ======================== km-config.yaml persistence =============================

// PersistLaunchAccountsConfig writes the launch_accounts MAP back to the
// km-config.yaml at configPath. Mirrors PersistClustersConfig's raw-map
// read-modify-write (preserves every other top-level key, generated-file
// header, owner-only permissions) but keys by link NAME rather than writing a
// list — config.LaunchAccountConfig's own yaml tags drive field names, so the
// mapping is not hand-duplicated as PersistClustersConfig's is.
func PersistLaunchAccountsConfig(configPath string, links map[string]config.LaunchAccountConfig) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("reading km-config.yaml: %w", err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parsing km-config.yaml: %w", err)
	}
	if raw == nil {
		raw = make(map[string]interface{})
	}
	raw["launch_accounts"] = links

	newData, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshaling km-config.yaml: %w", err)
	}
	header := "# km-config.yaml — generated by km configure\n# Add this file to .gitignore\n\n"
	return os.WriteFile(configPath, append([]byte(header), newData...), 0o600)
}

// ======================== km account register ====================================

// AccountRegisterOpts holds every `km account register` flag value.
type AccountRegisterOpts struct {
	Name            string
	FromFragment    string // explicit fragment path; "" tries the default path, then falls back to the fields below
	LauncherRoleARN string
	BoxRoleARN      string
	ExternalID      string
	Region          string
	SubnetIDs       []string
	SecurityGroupID string
	ResultsBucket   string
	EFSID           string
	AccountID       string
	AWSProfile      string
}

// assembleAccountLinkRecord resolves the link record RunAccountRegister
// writes, in priority order: an explicit --from-fragment path (hard error if
// unreadable — an explicit flag is authoritative, never silently skipped);
// else the default fragment path from `km account add`, if present; else
// every field from explicit flags. AccountLinkFragment is reused as the
// assembled record shape since it already carries every field
// LaunchAccountConfig needs (T-126-11's carry-the-backend-fields guarantee).
//
// Explicit-flag mode has no --az flag (the plan's flag list has none), so
// AvailabilityZones is left empty in that path — a link registered this way
// needs its availability_zones filled in by hand before `km create` can use
// it (ResolveLaunchTarget's length-parity guard will name the problem
// clearly). --from-fragment is the primary, fully-populated path; explicit
// flags exist as a recovery mechanism when the fragment itself is lost, not
// as a routine alternative.
func assembleAccountLinkRecord(cfg *config.Config, opts AccountRegisterOpts) (AccountLinkFragment, error) {
	fragPath := opts.FromFragment
	if fragPath == "" {
		if defaultPath, err := accountLinkFragmentPath(opts.Name); err == nil {
			if _, statErr := os.Stat(defaultPath); statErr == nil {
				fragPath = defaultPath
			}
		}
	}
	if fragPath != "" {
		data, err := os.ReadFile(fragPath)
		if err != nil {
			return AccountLinkFragment{}, fmt.Errorf("reading link fragment %s: %w", fragPath, err)
		}
		var frag AccountLinkFragment
		if err := yaml.Unmarshal(data, &frag); err != nil {
			return AccountLinkFragment{}, fmt.Errorf("parsing link fragment %s: %w", fragPath, err)
		}
		frag.Name = opts.Name
		return frag, nil
	}

	if opts.LauncherRoleARN == "" || opts.BoxRoleARN == "" || opts.AccountID == "" ||
		opts.Region == "" || len(opts.SubnetIDs) == 0 || opts.ExternalID == "" {
		defaultPath, _ := accountLinkFragmentPath(opts.Name)
		return AccountLinkFragment{}, fmt.Errorf(
			"no link fragment found at %s (or --from-fragment) and explicit flags are incomplete — "+
				"supply --from-fragment <path>, or all of --launcher-arn, --box-role-arn, --account-id, "+
				"--region, --subnet, and --external-id",
			defaultPath,
		)
	}

	regionLabel := compiler.RegionLabel(opts.Region)
	prefix := cfg.GetResourcePrefix()
	return AccountLinkFragment{
		Name:            opts.Name,
		AccountID:       opts.AccountID,
		LauncherRoleARN: opts.LauncherRoleARN,
		BoxRoleARN:      opts.BoxRoleARN,
		ExternalID:      opts.ExternalID,
		Region:          opts.Region,
		SubnetIDs:       opts.SubnetIDs,
		SecurityGroupID: opts.SecurityGroupID,
		ResultsBucket:   opts.ResultsBucket,
		EFSID:           opts.EFSID,
		StateBucket:     LinkStateBucketName(prefix, opts.AccountID, regionLabel),
		LockTable:       LinkLockTableName(prefix, regionLabel),
		StateKey:        LinkStateKey(opts.Name),
	}, nil
}

// RunAccountRegister implements the km account register flow. Runs with HOME
// account credentials. Exported so cmd_test can call it directly.
//
// Sequence:
//  1. Assemble the link record (fragment, or explicit flags).
//  2. Load home AWS config + resolve caller identity; fail if the resolved
//     account equals the link's own target account id (self-link mistake).
//  3. Write the external id to a home-account SecureString parameter —
//     overwrite enabled so re-registration succeeds — and record ONLY that
//     path on the persisted link, never the value (T-126-35).
//  4. Upsert the one artifacts-bucket read grant, keyed by this link's name.
//  5. Persist the link into km-config.yaml launch_accounts.<name>.
//  6. Print what changed and the verification commands.
func RunAccountRegister(cfg *config.Config, opts AccountRegisterOpts, repoRoot string, out io.Writer) error {
	ctx := context.Background()

	link, err := assembleAccountLinkRecord(cfg, opts)
	if err != nil {
		return err
	}

	awsCfg, err := awspkg.LoadAWSConfig(ctx, opts.AWSProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", opts.AWSProfile, err)
	}
	caller, err := NewAccountCallerIdentityFunc(ctx, awsCfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Registering into home account %s (caller: %s)\n", caller.AccountID, caller.ARN)
	if caller.AccountID == link.AccountID {
		return fmt.Errorf(
			"resolved home account id %s equals launch_accounts.%s's target account id %s — "+
				"refusing to register a link whose target is the home account itself",
			caller.AccountID, opts.Name, link.AccountID)
	}

	extIDPath := externalIDParamPath(cfg, opts.Name)
	ssmAPI := NewAccountSSMClientFunc(awsCfg)
	if err := putSecureParameter(ctx, ssmAPI, extIDPath, link.ExternalID); err != nil {
		return fmt.Errorf("writing external id parameter: %w", err)
	}

	s3API := NewArtifactsPolicyS3Client(awsCfg)
	sid := artifactsGrantSid(cfg.GetResourcePrefix(), opts.Name)
	if err := UpsertArtifactsReadGrant(ctx, s3API, cfg.ArtifactsBucket, sid, link.BoxRoleARN); err != nil {
		return fmt.Errorf("granting artifacts bucket read access: %w", err)
	}

	linkCfg := config.LaunchAccountConfig{
		AccountID:         link.AccountID,
		LauncherRoleARN:   link.LauncherRoleARN,
		BoxRoleARN:        link.BoxRoleARN,
		ExternalIDSSM:     extIDPath,
		Region:            link.Region,
		SubnetIDs:         link.SubnetIDs,
		AvailabilityZones: link.AvailabilityZones,
		SecurityGroupID:   link.SecurityGroupID,
		ResultsBucket:     link.ResultsBucket,
		EFSID:             link.EFSID,
		InstanceTypes:     link.InstanceTypes,
		StateBucket:       link.StateBucket,
		LockTable:         link.LockTable,
		StateKey:          link.StateKey,
	}

	updated := make(map[string]config.LaunchAccountConfig, len(cfg.GetLaunchAccounts())+1)
	for k, v := range cfg.GetLaunchAccounts() {
		updated[k] = v
	}
	updated[opts.Name] = linkCfg

	configPath := filepath.Join(repoRoot, "km-config.yaml")
	if err := PersistLaunchAccountsConfig(configPath, updated); err != nil {
		return fmt.Errorf(
			"apply succeeded (grant + parameter written) but persisting km-config.yaml failed: %w\n"+
				"re-run `km account register %s` — every step here is idempotent",
			err, opts.Name)
	}
	cfg.LaunchAccounts = updated

	fmt.Fprintf(out, "Launch account link %q registered: launcher=%s box=%s\n", opts.Name, link.LauncherRoleARN, link.BoxRoleARN)
	fmt.Fprintf(out, "External id stored at %s (value never printed, never written to km-config.yaml)\n", extIDPath)
	fmt.Fprintf(out, "Artifacts bucket grant %s: object-read + bucket-list only for %s\n", sid, link.BoxRoleARN)
	fmt.Fprintf(out, "Verify with: km validate <profile with spec.runtime.launchAccount: %s> && km doctor\n", opts.Name)
	return nil
}

// ======================== km account list =========================================

// RunAccountList prints every configured launch_accounts link as a tabwriter
// table. Never prints the external id or its value — only ExternalIDSSM, the
// parameter PATH (T-126-40).
func RunAccountList(w io.Writer, cfg *config.Config) error {
	links := cfg.GetLaunchAccounts()
	if len(links) == 0 {
		fmt.Fprintln(w, "(no launch account links configured)")
		return nil
	}
	names := make([]string, 0, len(links))
	for name := range links {
		names = append(names, name)
	}
	sort.Strings(names)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tACCOUNT ID\tREGION\tSUBNETS\tRESULTS BUCKET\tEXTERNAL ID PARAM")
	for _, name := range names {
		l := links[name]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", name, l.AccountID, l.Region, len(l.SubnetIDs), l.ResultsBucket, l.ExternalIDSSM)
	}
	return tw.Flush()
}

// ======================== km account rm ============================================

// AccountRmOpts holds every `km account rm` flag value.
type AccountRmOpts struct {
	Name             string
	HomeAWSProfile   string
	TargetAWSProfile string // "" = home-only removal; the follow-up command is printed instead
	PurgeBackend     bool
	Yes              bool
	Verbose          bool
}

// PurgeBucketS3API is the narrow S3 interface purgeVersionedBucket uses to
// empty and delete the (versioned) link-state bucket under --purge-backend.
type PurgeBucketS3API interface {
	ListObjectVersions(ctx context.Context, params *s3.ListObjectVersionsInput, optFns ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error)
	DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	DeleteBucket(ctx context.Context, params *s3.DeleteBucketInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketOutput, error)
}

// NewPurgeBucketS3Client is the factory tests override to inject a stub PurgeBucketS3API.
var NewPurgeBucketS3Client = func(cfg awssdk.Config) PurgeBucketS3API {
	return s3.NewFromConfig(cfg)
}

// PurgeLockTableDynamoDBAPI is the narrow DynamoDB interface used to delete
// the link-lock table under --purge-backend.
type PurgeLockTableDynamoDBAPI interface {
	DeleteTable(ctx context.Context, params *dynamodb.DeleteTableInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteTableOutput, error)
}

// NewPurgeLockTableDynamoDBClient is the factory tests override to inject a
// stub PurgeLockTableDynamoDBAPI.
var NewPurgeLockTableDynamoDBClient = func(cfg awssdk.Config) PurgeLockTableDynamoDBAPI {
	return dynamodb.NewFromConfig(cfg)
}

// purgeVersionedBucket empties every object version (and delete marker) from
// bucket before deleting it — AWS refuses to delete a non-empty bucket, and a
// versioned bucket's "objects" include every version, not just the latest.
// Idempotent: a bucket that is already gone is treated as success.
func purgeVersionedBucket(ctx context.Context, api PurgeBucketS3API, bucket string) error {
	for {
		out, err := api.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: awssdk.String(bucket)})
		if err != nil {
			if isS3NotFound(err) {
				return nil
			}
			return fmt.Errorf("listing object versions in %s: %w", bucket, err)
		}
		var toDelete []s3types.ObjectIdentifier
		for _, v := range out.Versions {
			toDelete = append(toDelete, s3types.ObjectIdentifier{Key: v.Key, VersionId: v.VersionId})
		}
		for _, m := range out.DeleteMarkers {
			toDelete = append(toDelete, s3types.ObjectIdentifier{Key: m.Key, VersionId: m.VersionId})
		}
		if len(toDelete) > 0 {
			if _, err := api.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: awssdk.String(bucket),
				Delete: &s3types.Delete{Objects: toDelete},
			}); err != nil {
				return fmt.Errorf("deleting object versions in %s: %w", bucket, err)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
	}
	if _, err := api.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: awssdk.String(bucket)}); err != nil {
		if isS3NotFound(err) {
			return nil
		}
		return fmt.Errorf("deleting bucket %s: %w", bucket, err)
	}
	return nil
}

// deleteLockTableIdempotent deletes table, treating "already gone" as success.
func deleteLockTableIdempotent(ctx context.Context, api PurgeLockTableDynamoDBAPI, table string) error {
	if _, err := api.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: awssdk.String(table)}); err != nil {
		if isDDBNotFound(err) {
			return nil
		}
		return fmt.Errorf("deleting lock table %s: %w", table, err)
	}
	return nil
}

// purgeLinkBackend deletes link's state bucket and lock table, but only after
// confirming no OTHER configured link (remaining, since the caller has
// already removed this one) still resolves to the same bucket or table
// names — the bucket is per-target-account, the lock table is per-region and
// SHARED across every target account in that region, so either name may
// still be load-bearing for a sibling link. Refuses (naming the conflicting
// link) rather than silently leaving a sibling's backend half-deleted.
func purgeLinkBackend(ctx context.Context, cfg *config.Config, targetAWSCfg awssdk.Config, link config.LaunchAccountConfig, out io.Writer) error {
	for otherName, other := range cfg.GetLaunchAccounts() {
		if other.StateBucket == link.StateBucket || other.LockTable == link.LockTable {
			return fmt.Errorf(
				"refusing --purge-backend: launch_accounts.%s still shares state bucket %q or lock table %q with this link — "+
					"remove %s first, or omit --purge-backend to leave the shared backend intact",
				otherName, link.StateBucket, link.LockTable, otherName)
		}
	}

	s3API := NewPurgeBucketS3Client(targetAWSCfg)
	if err := purgeVersionedBucket(ctx, s3API, link.StateBucket); err != nil {
		return fmt.Errorf("purging link state bucket: %w", err)
	}
	fmt.Fprintf(out, "Deleted link state bucket %s (all versions).\n", link.StateBucket)

	ddbAPI := NewPurgeLockTableDynamoDBClient(targetAWSCfg)
	if err := deleteLockTableIdempotent(ctx, ddbAPI, link.LockTable); err != nil {
		return fmt.Errorf("purging link lock table: %w", err)
	}
	fmt.Fprintf(out, "Deleted link lock table %s.\n", link.LockTable)
	return nil
}

// regenerateAccountLinkUnit rewrites unitDir/terragrunt.hcl from link when it
// is missing — the remote state in the target account is the source of
// truth, not a directory on one operator's laptop (Test 6b). The
// regenerated inputs need not match the original apply exactly: `terraform
// destroy` operates on what is tracked in the remote state, not on this
// run's config (the same precedent Phase 125 recorded for the ttl-handler's
// frozen destroy-placeholder — see CLAUDE.md "Residual risk"). Fields
// LaunchAccountConfig does not carry (trusted principals, the
// provision-network/EFS/Bedrock toggles) are supplied as safe destroy-only
// defaults; ExternalID and TrustAccountID are placeholders for the same
// reason — neither affects what terraform destroy tears down.
func regenerateAccountLinkUnit(unitDir, moduleSource, name string, cfg *config.Config, link config.LaunchAccountConfig) error {
	subnetID := ""
	if len(link.SubnetIDs) > 0 {
		subnetID = link.SubnetIDs[0]
	}
	hcl := GenerateAccountLinkHCL(AccountLinkHCLParams{
		Name:            name,
		ModuleSource:    moduleSource,
		StateBucket:     link.StateBucket,
		StateKey:        link.StateKey,
		LockTable:       link.LockTable,
		Region:          link.Region,
		ResourcePrefix:  cfg.GetResourcePrefix(),
		TrustAccountID:  link.AccountID, // placeholder — destroy does not depend on this value's correctness
		ExternalID:      "regenerated-for-destroy",
		InstanceTypes:   link.InstanceTypes,
		SubnetID:        subnetID,
		SecurityGroupID: link.SecurityGroupID,
		AZCount:         len(link.SubnetIDs),
		ProvisionEFS:    link.EFSID != "",
	})
	if err := os.MkdirAll(unitDir, 0o700); err != nil {
		return fmt.Errorf("creating unit directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(unitDir, "terragrunt.hcl"), []byte(hcl), 0o600); err != nil {
		return fmt.Errorf("writing terragrunt.hcl: %w", err)
	}
	return nil
}

// RunAccountRm implements the km account rm flow. Exported so cmd_test can
// call it directly. repoRoot is passed explicitly (tests supply t.TempDir();
// production passes findRepoRoot()); in is the confirmation prompt's input
// (cmd.InOrStdin() in production, a bytes.Reader in tests).
//
// Order (T-126-39 "half-finished teardown"): remove the artifacts grant,
// delete the secure parameter, remove the km-config.yaml entry — all
// unconditionally and all idempotent — then, only when --target-aws-profile
// was supplied, destroy the target-account footprint. When it was not
// supplied, print a self-contained terragrunt command against the (still
// on-disk) unit directory, so the operator can finish teardown later without
// needing km-config.yaml to still carry the entry.
func RunAccountRm(cfg *config.Config, opts AccountRmOpts, repoRoot string, in io.Reader, out io.Writer) error {
	ctx := context.Background()

	if opts.PurgeBackend && opts.TargetAWSProfile == "" {
		return fmt.Errorf("--purge-backend requires --target-aws-profile — the state backend lives in the target account")
	}

	link, ok := cfg.GetLaunchAccount(opts.Name)
	if !ok {
		return fmt.Errorf("launch account link %q not found in km-config.yaml launch_accounts", opts.Name)
	}

	unitDir, err := accountLinkUnitDir(opts.Name)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "About to remove launch account link %q:\n", opts.Name)
	fmt.Fprintf(out, "  - artifacts bucket read grant (%s)\n", artifactsGrantSid(cfg.GetResourcePrefix(), opts.Name))
	fmt.Fprintf(out, "  - external id parameter (%s)\n", link.ExternalIDSSM)
	fmt.Fprintf(out, "  - km-config.yaml launch_accounts.%s entry\n", opts.Name)
	if opts.TargetAWSProfile != "" {
		fmt.Fprintf(out, "  - target-account footprint (terragrunt destroy against %s)\n", unitDir)
	}
	if opts.PurgeBackend {
		fmt.Fprintf(out, "  - link state backend (bucket %s, lock table %s) — --purge-backend\n", link.StateBucket, link.LockTable)
	}

	if !opts.Yes {
		confirmed, cerr := amiConfirmPrompt(in, out, "Proceed? [y/N] ")
		if cerr != nil {
			return cerr
		}
		if !confirmed {
			fmt.Fprintln(out, "Aborted.")
			return nil
		}
	}

	homeAWSCfg, err := awspkg.LoadAWSConfig(ctx, opts.HomeAWSProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", opts.HomeAWSProfile, err)
	}

	// 1. Remove the artifacts bucket grant (idempotent).
	s3API := NewArtifactsPolicyS3Client(homeAWSCfg)
	if err := RemoveArtifactsReadGrant(ctx, s3API, cfg.ArtifactsBucket, artifactsGrantSid(cfg.GetResourcePrefix(), opts.Name)); err != nil {
		return fmt.Errorf("removing artifacts bucket grant: %w", err)
	}

	// 2. Delete the secure parameter (idempotent).
	ssmAPI := NewAccountSSMClientFunc(homeAWSCfg)
	if err := deleteParameterIdempotent(ctx, ssmAPI, link.ExternalIDSSM); err != nil {
		return fmt.Errorf("deleting external id parameter: %w", err)
	}

	// 3. Remove the km-config.yaml entry.
	remaining := make(map[string]config.LaunchAccountConfig, len(cfg.GetLaunchAccounts()))
	for k, v := range cfg.GetLaunchAccounts() {
		if k == opts.Name {
			continue
		}
		remaining[k] = v
	}
	configPath := filepath.Join(repoRoot, "km-config.yaml")
	if err := PersistLaunchAccountsConfig(configPath, remaining); err != nil {
		return fmt.Errorf("removing launch_accounts.%s from km-config.yaml: %w", opts.Name, err)
	}
	cfg.LaunchAccounts = remaining
	fmt.Fprintf(out, "Removed launch account link %q from the home account.\n", opts.Name)

	// 4. Target-account destroy, or the follow-up hint.
	if opts.TargetAWSProfile == "" {
		fmt.Fprintf(out, "\nThe target account (%s) still holds the launcher role, box role and network — "+
			"finish teardown with that account's credentials:\n"+
			"  AWS_PROFILE=<target-account-profile> terragrunt destroy --terragrunt-non-interactive --terragrunt-working-dir %s\n",
			link.AccountID, unitDir)
	} else {
		moduleSource, merr := filepath.Abs(filepath.Join(repoRoot, "infra", "modules", "gpu-launcher-account", "v1.0.0"))
		if merr != nil {
			return fmt.Errorf("resolving module source path: %w", merr)
		}
		if _, statErr := os.Stat(filepath.Join(unitDir, "terragrunt.hcl")); statErr != nil {
			fmt.Fprintf(out, "Unit directory %s has no terragrunt.hcl — regenerating from the link record.\n", unitDir)
			if err := regenerateAccountLinkUnit(unitDir, moduleSource, opts.Name, cfg, link); err != nil {
				return fmt.Errorf("regenerating unit directory: %w", err)
			}
		}
		runner := NewAccountRunnerFunc(opts.TargetAWSProfile, repoRoot)
		if r, rok := runner.(*terragrunt.Runner); rok {
			r.Verbose = opts.Verbose
		}
		if err := runner.Destroy(ctx, unitDir); err != nil {
			return fmt.Errorf("terragrunt destroy failed: %w", err)
		}
		fmt.Fprintf(out, "Destroyed target-account footprint for %q.\n", opts.Name)

		// 5. Optional backend purge — target-account credentials, same profile
		// as the destroy above.
		if opts.PurgeBackend {
			targetAWSCfg, terr := awspkg.LoadAWSConfigInRegion(ctx, opts.TargetAWSProfile, link.Region)
			if terr != nil {
				return fmt.Errorf("failed to load AWS config for --purge-backend (profile=%s): %w", opts.TargetAWSProfile, terr)
			}
			if err := purgeLinkBackend(ctx, cfg, targetAWSCfg, link, out); err != nil {
				return err
			}
		}
	}

	return nil
}

// ======================== Cobra command tree additions ===========================

func newAccountRegisterCmd(cfg *config.Config) *cobra.Command {
	opts := AccountRegisterOpts{}
	var subnetIDs []string
	cmd := &cobra.Command{
		Use:          "register <name>",
		Short:        "Register a cross-account capacity-borrowing link into the home account",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			opts.SubnetIDs = subnetIDs
			return RunAccountRegister(cfg, opts, findRepoRoot(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&opts.FromFragment, "from-fragment", "",
		"path to the link fragment written by km account add (default: ~/.km/account-links/<name>.link.yaml if present)")
	cmd.Flags().StringVar(&opts.LauncherRoleARN, "launcher-arn", "", "launcher role ARN (explicit-flags alternative to --from-fragment)")
	cmd.Flags().StringVar(&opts.BoxRoleARN, "box-role-arn", "", "box role ARN (explicit-flags alternative to --from-fragment)")
	cmd.Flags().StringVar(&opts.ExternalID, "external-id", "", "STS ExternalId (explicit-flags alternative to --from-fragment)")
	cmd.Flags().StringArrayVar(&subnetIDs, "subnet", nil, "target-account subnet id (repeatable; explicit-flags alternative to --from-fragment)")
	cmd.Flags().StringVar(&opts.SecurityGroupID, "sg", "", "target-account security group id (explicit-flags alternative to --from-fragment)")
	cmd.Flags().StringVar(&opts.Region, "region", "", "target-account region (explicit-flags alternative to --from-fragment)")
	cmd.Flags().StringVar(&opts.ResultsBucket, "results-bucket", "", "target-account results bucket (explicit-flags alternative to --from-fragment)")
	cmd.Flags().StringVar(&opts.EFSID, "efs-id", "", "target-account EFS filesystem id (explicit-flags alternative to --from-fragment)")
	cmd.Flags().StringVar(&opts.AccountID, "account-id", "", "target AWS account id (explicit-flags alternative to --from-fragment)")
	cmd.Flags().StringVar(&opts.AWSProfile, "aws-profile", "klanker-application", "AWS profile for the HOME account's credentials")
	return cmd
}

func newAccountListCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List configured cross-account capacity-borrowing links",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return RunAccountList(cmd.OutOrStdout(), cfg)
		},
	}
}

func newAccountRmCmd(cfg *config.Config) *cobra.Command {
	opts := AccountRmOpts{}
	cmd := &cobra.Command{
		Use:          "rm <name>",
		Short:        "Remove a cross-account capacity-borrowing link",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			return RunAccountRm(cfg, opts, findRepoRoot(), cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&opts.HomeAWSProfile, "aws-profile", "klanker-application", "AWS profile for the HOME account's credentials")
	cmd.Flags().StringVar(&opts.TargetAWSProfile, "target-aws-profile", "",
		"AWS profile for the TARGET account's credentials — when set, also destroys the target-account footprint")
	cmd.Flags().BoolVar(&opts.PurgeBackend, "purge-backend", false,
		"also delete the target-account state bucket and lock table (requires --target-aws-profile; refuses if another link still shares them)")
	cmd.Flags().BoolVar(&opts.Yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&opts.Verbose, "verbose", false, "stream terragrunt output")
	return cmd
}
