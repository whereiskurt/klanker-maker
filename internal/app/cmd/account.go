// account.go implements `km account add` — the one-time, privileged
// enrollment command for Phase 126 cross-account capacity borrowing. It
// applies the gpu-launcher-account/v1.0.0 Terraform module into a target AWS
// account ("account B") using that account's OWN administrator credentials,
// and hands the operator a link fragment for `km account register` (plan 07)
// to wire into the home account ("account A").
//
// Credential model (see docs/superpowers/specs/2026-06-29-cross-account-gpu-launch-design.md
// § "Credential model"): this command authenticates as B ONLY, via
// --aws-profile. Account A is referenced by --trust <A-account-id> as a bare
// string — no A credentials are ever loaded here, and no write reaches A's
// km-config.yaml, parameter store, or bucket policy from this file. That is
// `km account register`'s job.
//
// `km account add` uses real terraform, not a command-printing wizard,
// because `km account rm` (plan 07) needs real state to destroy against —
// this is a deliberate divergence from the SCP bootstrap's print-the-commands
// mechanism, which has no state to destroy.
//
// The generated unit is standalone on BOTH the provider and the backend: no
// root include, its own plain (non-assumed-role) provider, and its own S3
// backend IN THE TARGET ACCOUNT. Reusing the home account's shared backend
// while authenticated as the target account produces exactly the
// access-denied the roadmap already records as a rejected shortcut, and a
// local backend is prohibited outright — there is no local Terraform state
// anywhere in this project. The backend (an S3 bucket + a DynamoDB lock
// table, both in the target account) is created by this command itself
// through the AWS API, via EnsureLinkStateBackend, before the first
// terragrunt init.
package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	smithy "github.com/aws/smithy-go"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/check"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
	"github.com/whereiskurt/klanker-maker/pkg/terragrunt"
)

// ======================== Types ================================================

// AccountAddOpts holds every `km account add` flag value. Exported (and a
// struct rather than positional args, unlike RunClusterAdd) because this
// family carries far more flags than any other km subcommand, and the
// cmd_test package needs to construct one directly to call RunAccountAdd
// without going through Cobra flag parsing.
type AccountAddOpts struct {
	Name                   string
	TrustAccountID         string
	Region                 string
	ProvisionNetwork       bool
	SubnetID               string
	SecurityGroupID        string
	AZCount                int
	ProvisionEFS           bool
	InstanceTypes          []string
	EnableBedrock          bool
	ExternalID             string
	SopsFile               string
	TrustPrincipals        []string
	TrustPrincipalPatterns []string
	AWSProfile             string
	DryRun                 bool
	// Force re-applies the module against an ALREADY-enrolled link instead of
	// exiting early. Needed whenever the gpu-launcher-account module changes,
	// since this command owns the target account's Terraform and re-running it
	// is the only way to converge an existing link's footprint.
	Force               bool
	StateBucketOverride string
	LockTableOverride   string
}

// AccountRunner is the seam tests use to inject a mock runner. Mirrors the
// subset of *terragrunt.Runner methods km account add calls. Unlike
// ClusterRunner, it has no Reconfigure — that belongs to `km account rm`
// (plan 07), which this file does not implement.
//
// InitNoBackend and Validate exist only for the --dry-run path: a dry run
// must not create the target-account state bucket, but the rendered unit
// already carries an S3 backend block, and a normal `terragrunt plan` would
// trigger an implicit `terraform init` that hard-fails with NoSuchBucket
// against a bucket that does not exist yet. InitNoBackend + Validate give a
// real (if partial) validation of the rendered configuration without
// touching any backend.
type AccountRunner interface {
	Plan(ctx context.Context, dir string) error
	Apply(ctx context.Context, dir string) error
	Destroy(ctx context.Context, dir string) error
	Output(ctx context.Context, dir string) (map[string]interface{}, error)
	InitNoBackend(ctx context.Context, dir string) error
	Validate(ctx context.Context, dir string) error
}

// NewAccountRunnerFunc is the factory tests override to inject a mock runner.
// Production wires *terragrunt.Runner, which satisfies AccountRunner via the
// InitNoBackend/Validate methods added alongside this file (pkg/terragrunt/runner.go).
var NewAccountRunnerFunc = func(profile, repoRoot string) AccountRunner {
	return terragrunt.NewRunner(profile, repoRoot)
}

// LinkStateS3API is the narrow S3 interface EnsureLinkStateBackend uses.
// Satisfied by *s3.Client in production; tests inject a stub.
type LinkStateS3API interface {
	HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	CreateBucket(ctx context.Context, params *s3.CreateBucketInput, optFns ...func(*s3.Options)) (*s3.CreateBucketOutput, error)
	PutBucketVersioning(ctx context.Context, params *s3.PutBucketVersioningInput, optFns ...func(*s3.Options)) (*s3.PutBucketVersioningOutput, error)
	PutPublicAccessBlock(ctx context.Context, params *s3.PutPublicAccessBlockInput, optFns ...func(*s3.Options)) (*s3.PutPublicAccessBlockOutput, error)
	PutBucketEncryption(ctx context.Context, params *s3.PutBucketEncryptionInput, optFns ...func(*s3.Options)) (*s3.PutBucketEncryptionOutput, error)
}

// NewLinkStateS3Client is the factory tests override to inject a stub LinkStateS3API.
var NewLinkStateS3Client = func(cfg awssdk.Config) LinkStateS3API {
	return s3.NewFromConfig(cfg)
}

// LinkLockDynamoDBAPI is the narrow DynamoDB interface EnsureLinkStateBackend
// uses. Satisfied by *dynamodb.Client in production; tests inject a stub.
type LinkLockDynamoDBAPI interface {
	DescribeTable(ctx context.Context, params *dynamodb.DescribeTableInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error)
	CreateTable(ctx context.Context, params *dynamodb.CreateTableInput, optFns ...func(*dynamodb.Options)) (*dynamodb.CreateTableOutput, error)
}

// NewLinkLockDynamoDBClient is the factory tests override to inject a stub LinkLockDynamoDBAPI.
var NewLinkLockDynamoDBClient = func(cfg awssdk.Config) LinkLockDynamoDBAPI {
	return dynamodb.NewFromConfig(cfg)
}

// LinkLockTablePollInterval is how long EnsureLinkStateBackend sleeps between
// DescribeTable polls while waiting for a freshly-created lock table to reach
// ACTIVE. A package var (not a constant) so tests can shrink it to avoid a
// slow real-time wait.
var LinkLockTablePollInterval = 2 * time.Second

// AccountCallerIdentity holds what RunAccountAdd needs from STS
// GetCallerIdentity against the loaded (account-B) AWS config: the resolved
// account id, to catch the self-enrollment mistake (T-126-25), and the
// caller ARN, to derive the operator's own principal for the launcher trust
// policy (deriveOperatorPrincipalARN).
type AccountCallerIdentity struct {
	AccountID string
	ARN       string
}

// NewAccountCallerIdentityFunc is the seam tests override to avoid a real STS
// call. Production wires sts.NewFromConfig(awsCfg).GetCallerIdentity — this
// single call both validates the loaded credentials (an error here IS the
// credential-validation failure) and resolves the caller's identity.
var NewAccountCallerIdentityFunc = func(ctx context.Context, awsCfg awssdk.Config) (AccountCallerIdentity, error) {
	client := sts.NewFromConfig(awsCfg)
	out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return AccountCallerIdentity{}, fmt.Errorf("AWS credential validation failed: %w", err)
	}
	return AccountCallerIdentity{
		AccountID: awssdk.ToString(out.Account),
		ARN:       awssdk.ToString(out.Arn),
	}, nil
}

// AccountLinkFragment is the on-disk artifact `km account add` writes at
// ~/.km/account-links/<name>.link.yaml — everything `km account register`
// (plan 07) needs to record the link in the home account. Written at
// owner-only permissions (T-126-23) because ExternalID is a secret and this
// is the only place it is ever written in cleartext outside the target
// account's own state object (T-126-29).
type AccountLinkFragment struct {
	Name              string   `yaml:"name"`
	AccountID         string   `yaml:"account_id"`
	LauncherRoleARN   string   `yaml:"launcher_role_arn"`
	BoxRoleARN        string   `yaml:"box_role_arn"`
	ExternalID        string   `yaml:"external_id"`
	Region            string   `yaml:"region"`
	SubnetIDs         []string `yaml:"subnet_ids,omitempty"`
	AvailabilityZones []string `yaml:"availability_zones,omitempty"`
	SecurityGroupID   string   `yaml:"security_group_id"`
	ResultsBucket     string   `yaml:"results_bucket"`
	EFSID             string   `yaml:"efs_id,omitempty"`
	InstanceTypes     []string `yaml:"instance_types,omitempty"`
	StateBucket       string   `yaml:"state_bucket"`
	LockTable         string   `yaml:"lock_table"`
	StateKey          string   `yaml:"state_key"`
}

// ======================== Naming helpers ========================================

// LinkStateBucketName returns the deterministic S3 bucket name an enrollment
// unit's Terraform state lives in, in the TARGET account. Exported so plan
// 07's teardown and any later doctor check derive the same string rather
// than re-deriving it by hand.
//
// targetAccountID MUST be the TARGET (account-B) id, never the home/trust
// account id: S3 bucket names are globally unique across all of AWS, so
// naming the bucket after the home account makes every target enrolled from
// that home collide on one name — the second enrollment fails with
// BucketAlreadyExists. `km account register` already derives it from the
// target id, so a home-id derivation here also desynchronises add from
// register and points `km account rm --purge-backend` at a bucket that was
// never created.
func LinkStateBucketName(prefix, targetAccountID, regionLabel string) string {
	return fmt.Sprintf("tf-%s-linkstate-%s-%s", prefix, targetAccountID, regionLabel)
}

// LinkLockTableName returns the deterministic DynamoDB lock table name,
// shared by every link enrolled into the same (prefix, target account,
// region) — per-account, not globally unique, so no account id is needed.
func LinkLockTableName(prefix, regionLabel string) string {
	return fmt.Sprintf("tf-%s-linklocks-%s", prefix, regionLabel)
}

// LinkStateKey returns the deterministic state object key for a named link —
// one bucket holds every link enrolled into the same target account, keyed
// per link name.
func LinkStateKey(name string) string {
	return fmt.Sprintf("account-links/%s/terraform.tfstate", name)
}

// ======================== EnsureLinkStateBackend ================================

// EnsureLinkStateBackend creates or validates the target-account S3 state
// bucket and DynamoDB lock table for a `km account add` enrollment unit,
// before any terragrunt invocation. Returns the resolved bucket, table and
// state key.
//
// The three control calls (versioning, public-access-block, encryption) are
// applied UNCONDITIONALLY — whether the bucket was just created or already
// existed — which is what makes this function reconciling rather than merely
// idempotent: it recovers a bucket left half-configured by an interrupted
// earlier run. The state object records the trust policy (including the
// external id, T-126-29), so these controls are not optional.
//
// HeadBucket's three outcomes are distinguished explicitly (T-126-30):
// success (exists, usable), a not-found error (create it), and any other
// error including access-denied, which is a hard, named error — never
// silently treated as "already exists". Treating access-denied as "exists"
// would silently point the backend at a bucket this operator does not
// control.
func EnsureLinkStateBackend(
	ctx context.Context,
	awsCfg awssdk.Config,
	prefix, targetAccountID, regionLabel, name string,
	bucketOverride, tableOverride string,
) (bucket, table, key string, err error) {
	bucket = bucketOverride
	if bucket == "" {
		bucket = LinkStateBucketName(prefix, targetAccountID, regionLabel)
	}
	table = tableOverride
	if table == "" {
		table = LinkLockTableName(prefix, regionLabel)
	}
	key = LinkStateKey(name)

	s3Client := NewLinkStateS3Client(awsCfg)

	_, headErr := s3Client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: awssdk.String(bucket)})
	switch {
	case headErr == nil:
		// Exists — fall through to reconcile the controls below.
	case isS3NotFound(headErr):
		createInput := &s3.CreateBucketInput{Bucket: awssdk.String(bucket)}
		if awsCfg.Region != "us-east-1" {
			createInput.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
				LocationConstraint: s3types.BucketLocationConstraint(awsCfg.Region),
			}
		}
		if _, cErr := s3Client.CreateBucket(ctx, createInput); cErr != nil {
			return "", "", "", fmt.Errorf("create link state bucket %s: %w", bucket, cErr)
		}
	default:
		return "", "", "", fmt.Errorf(
			"link state bucket %s: HeadBucket failed and is not a not-found error — "+
				"the name may already be taken in another account: %w", bucket, headErr)
	}

	// Reconcile the controls unconditionally — see the function doc comment.
	if _, err := s3Client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: awssdk.String(bucket),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: s3types.BucketVersioningStatusEnabled,
		},
	}); err != nil {
		return "", "", "", fmt.Errorf("enable versioning on link state bucket %s: %w", bucket, err)
	}
	if _, err := s3Client.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
		Bucket: awssdk.String(bucket),
		PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
			BlockPublicAcls:       awssdk.Bool(true),
			BlockPublicPolicy:     awssdk.Bool(true),
			IgnorePublicAcls:      awssdk.Bool(true),
			RestrictPublicBuckets: awssdk.Bool(true),
		},
	}); err != nil {
		return "", "", "", fmt.Errorf("block public access on link state bucket %s: %w", bucket, err)
	}
	if _, err := s3Client.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{
		Bucket: awssdk.String(bucket),
		ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{
			Rules: []s3types.ServerSideEncryptionRule{{
				ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{
					SSEAlgorithm: s3types.ServerSideEncryptionAes256,
				},
			}},
		},
	}); err != nil {
		return "", "", "", fmt.Errorf("enable default encryption on link state bucket %s: %w", bucket, err)
	}

	ddbClient := NewLinkLockDynamoDBClient(awsCfg)
	_, descErr := ddbClient.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: awssdk.String(table)})
	if descErr != nil {
		if !isDDBNotFound(descErr) {
			return "", "", "", fmt.Errorf("describe link lock table %s: %w", table, descErr)
		}
		if _, cErr := ddbClient.CreateTable(ctx, &dynamodb.CreateTableInput{
			TableName: awssdk.String(table),
			AttributeDefinitions: []ddbtypes.AttributeDefinition{
				{AttributeName: awssdk.String("LockID"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			},
			KeySchema: []ddbtypes.KeySchemaElement{
				{AttributeName: awssdk.String("LockID"), KeyType: ddbtypes.KeyTypeHash},
			},
			BillingMode: ddbtypes.BillingModePayPerRequest,
		}); cErr != nil {
			return "", "", "", fmt.Errorf("create link lock table %s: %w", table, cErr)
		}
	}

	if err := waitForTableActive(ctx, ddbClient, table); err != nil {
		return "", "", "", err
	}

	return bucket, table, key, nil
}

// waitForTableActive polls DescribeTable until the table's status is ACTIVE,
// so a subsequent `terragrunt init` cannot race table creation.
func waitForTableActive(ctx context.Context, client LinkLockDynamoDBAPI, table string) error {
	for {
		out, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: awssdk.String(table)})
		if err != nil {
			return fmt.Errorf("describe link lock table %s: %w", table, err)
		}
		if out.Table != nil && out.Table.TableStatus == ddbtypes.TableStatusActive {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(LinkLockTablePollInterval):
		}
	}
}

// isS3NotFound reports whether err is an S3 "the bucket does not exist" error
// (as opposed to access-denied or anything else). Mirrors the is404 classifier
// in configure.go's probeStateBucketInteractive.
func isS3NotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "NoSuchBucket"
	}
	return false
}

// isDDBNotFound reports whether err is DynamoDB's ResourceNotFoundException.
func isDDBNotFound(err error) bool {
	var nf *ddbtypes.ResourceNotFoundException
	return errors.As(err, &nf)
}

// ======================== HCL Template ==========================================

// accountLinkTerragruntHCLTemplate is the verbatim HCL template for the
// standalone gpu-launcher-account enrollment unit. {PLACEHOLDER} markers
// (no $ prefix) are replaced by GenerateAccountLinkHCL; all HCL ${...}
// interpolations remain unchanged.
//
// Decisions recorded here (126-05-PLAN.md "Decisions recorded by this plan"):
//  1. Real terraform, not a command-printing wizard — `km account rm` (plan
//  07. needs real state to destroy against.
//  2. Standalone on BOTH the provider and the backend: no root include, its
//     own plain provider (no assume_role — enrollment runs as the target
//     account directly), and its own S3 backend IN THE TARGET ACCOUNT.
//  3. There is NO local Terraform state anywhere in this project. The
//     backend bucket + lock table are created by EnsureLinkStateBackend via
//     the AWS API before the first terragrunt init this template feeds.
//  4. artifacts_bucket_arn is sourced via get_env("KM_ARTIFACTS_BUCKET", "")
//     rather than a Go placeholder — RunAccountAdd calls
//     ExportTerragruntEnvVars(cfg) before every terragrunt invocation, which
//     already sets this for every other terragrunt unit in the repo
//     (cluster.go's clusterTerragruntHCLTemplate uses the identical idiom).
//     It is read from the operator's LOCAL km-config.yaml — no account-A
//     credentials are needed to know account A's own artifacts bucket name.
const accountLinkTerragruntHCLTemplate = `# Standalone terragrunt unit generated by GenerateAccountLinkHCL
# (internal/app/cmd/account.go) for "km account add {NAME}".
#
# No root include on purpose: this unit is applied with the TARGET account's
# own administrator credentials, and must never resolve the home account's
# shared backend or assume-role provider. Its state lives in an S3 bucket +
# DynamoDB lock table IN THE TARGET ACCOUNT, created by EnsureLinkStateBackend
# before this file is ever passed to terragrunt init. There is no local
# Terraform state anywhere in this project.

generate "provider" {
  path      = "provider.tf"
  if_exists = "overwrite_terragrunt"

  contents = <<-EOF
    terraform {
      required_version = ">= 1.6.0"

      required_providers {
        aws = {
          source  = "hashicorp/aws"
          version = ">= 5.0"
        }
      }
    }

    provider "aws" {
      region = "{REGION}"
    }
  EOF
}

remote_state {
  backend = "s3"

  generate = {
    path      = "backend.tf"
    if_exists = "overwrite_terragrunt"
  }

  config = {
    bucket         = "{STATE_BUCKET}"
    key            = "{STATE_KEY}"
    region         = "{REGION}"
    encrypt        = true
    dynamodb_table = "{LOCK_TABLE}"
  }
}

terraform {
  source = "{MODULE_SOURCE}"
}

inputs = {
  resource_prefix                = "{RESOURCE_PREFIX}"
  trust_account_id               = "{TRUST_ACCOUNT_ID}"
  trusted_principal_arns         = {TRUSTED_PRINCIPAL_ARNS}
  trusted_principal_arn_patterns = {TRUSTED_PRINCIPAL_ARN_PATTERNS}
  external_id                    = "{EXTERNAL_ID}"
  region                         = "{REGION}"
  instance_types                 = {INSTANCE_TYPES}
  provision_network               = {PROVISION_NETWORK}
  subnet_id                       = "{SUBNET_ID}"
  security_group_id               = "{SECURITY_GROUP_ID}"
  az_count                        = {AZ_COUNT}
  provision_efs                   = {PROVISION_EFS}
  enable_bedrock                  = {ENABLE_BEDROCK}
  artifacts_bucket_arn             = "arn:aws:s3:::${get_env("KM_ARTIFACTS_BUCKET", "")}"
}
`

// AccountLinkHCLParams holds every value substituted into
// accountLinkTerragruntHCLTemplate. RunAccountAdd is the sole assembler.
type AccountLinkHCLParams struct {
	Name                        string
	ModuleSource                string // absolute path to infra/modules/gpu-launcher-account/v1.0.0
	StateBucket                 string
	StateKey                    string
	LockTable                   string
	Region                      string
	ResourcePrefix              string
	TrustAccountID              string
	TrustedPrincipalARNs        []string
	TrustedPrincipalARNPatterns []string
	ExternalID                  string
	InstanceTypes               []string
	ProvisionNetwork            bool
	SubnetID                    string
	SecurityGroupID             string
	AZCount                     int
	ProvisionEFS                bool
	EnableBedrock               bool
}

// GenerateAccountLinkHCL substitutes the {PLACEHOLDER} markers in
// accountLinkTerragruntHCLTemplate. Exported for unit tests.
func GenerateAccountLinkHCL(p AccountLinkHCLParams) string {
	r := strings.NewReplacer(
		"{NAME}", p.Name,
		"{MODULE_SOURCE}", p.ModuleSource,
		"{STATE_BUCKET}", p.StateBucket,
		"{STATE_KEY}", p.StateKey,
		"{LOCK_TABLE}", p.LockTable,
		"{REGION}", p.Region,
		"{RESOURCE_PREFIX}", p.ResourcePrefix,
		"{TRUST_ACCOUNT_ID}", p.TrustAccountID,
		"{TRUSTED_PRINCIPAL_ARNS}", hclStringList(p.TrustedPrincipalARNs),
		"{TRUSTED_PRINCIPAL_ARN_PATTERNS}", hclStringList(p.TrustedPrincipalARNPatterns),
		"{EXTERNAL_ID}", p.ExternalID,
		"{INSTANCE_TYPES}", hclStringList(p.InstanceTypes),
		"{PROVISION_NETWORK}", strconv.FormatBool(p.ProvisionNetwork),
		"{SUBNET_ID}", p.SubnetID,
		"{SECURITY_GROUP_ID}", p.SecurityGroupID,
		"{AZ_COUNT}", strconv.Itoa(p.AZCount),
		"{PROVISION_EFS}", strconv.FormatBool(p.ProvisionEFS),
		"{ENABLE_BEDROCK}", strconv.FormatBool(p.EnableBedrock),
	)
	return r.Replace(accountLinkTerragruntHCLTemplate)
}

// hclStringList renders vals as an HCL list-of-strings literal, e.g.
// `["a", "b"]`. Empty input renders as `[]`.
func hclStringList(vals []string) string {
	quoted := make([]string, len(vals))
	for i, v := range vals {
		quoted[i] = strconv.Quote(v)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// ======================== Operator principal derivation =========================

// deriveOperatorPrincipalARN converts the caller identity resolved from the
// account-B admin credentials into the ARN of the SAME-NAMED role in account
// A (trustAccountID). An assumed-role session ARN
// (arn:aws:sts::<acct>:assumed-role/<role>/<session>) is normalized to the
// role ARN form (arn:aws:iam::<trustAccountID>:role/<role>) because IAM trust
// policies accept role ARNs, not session ARNs, as principals. Other principal
// forms (IAM user, root) are re-homed by swapping only the account id
// segment.
//
// IAM Identity Center (SSO) roles CANNOT be re-homed this way and are
// rejected outright — see isSSORoleName. Two independent reasons, either
// fatal on its own:
//
//  1. The trailing hash in AWSReservedSSO_<PermissionSet>_<hash> is derived
//     per-account. The same permission set provisions a DIFFERENT hash in
//     every account, so account B's hash never names a real role in A.
//  2. SSO roles live under the path /aws-reserved/sso.amazonaws.com/, which
//     the bare role/<name> form omits entirely.
//
// A guessed SSO ARN is not merely useless — IAM validates principal
// existence at CreateRole and rejects the whole trust policy with
// MalformedPolicyDocument, so the enrollment fails after the state backend
// has already been provisioned. Failing here, before any AWS mutation, with
// the exact lookup command is strictly better than emitting an ARN we know
// cannot resolve.
func deriveOperatorPrincipalARN(callerARN, trustAccountID string) (string, error) {
	parts := strings.SplitN(callerARN, ":", 6)
	if len(parts) != 6 || parts[0] != "arn" {
		return "", fmt.Errorf("caller ARN %q is not a well-formed ARN", callerARN)
	}
	partition, service, resource := parts[1], parts[2], parts[5]

	if strings.HasPrefix(resource, "assumed-role/") {
		segs := strings.Split(resource, "/")
		if len(segs) < 2 || segs[1] == "" {
			return "", fmt.Errorf("caller ARN %q: malformed assumed-role resource", callerARN)
		}
		if isSSORoleName(segs[1]) {
			return "", fmt.Errorf("caller is an IAM Identity Center (SSO) role (%s); "+
				"its per-account name hash and /aws-reserved/sso.amazonaws.com/ path cannot be "+
				"re-homed into account %s", segs[1], trustAccountID)
		}
		return fmt.Sprintf("arn:aws:iam::%s:role/%s", trustAccountID, segs[1]), nil
	}
	return fmt.Sprintf("arn:%s:%s::%s:%s", partition, service, trustAccountID, resource), nil
}

// isSSORoleName reports whether a role name was provisioned by IAM Identity
// Center. Matches the reserved AWSReservedSSO_ prefix AWS applies to every
// permission-set role.
func isSSORoleName(roleName string) bool {
	return strings.HasPrefix(roleName, "AWSReservedSSO_")
}

// resolveTrustedPrincipals assembles the trusted_principal_arns list: the
// two deterministic home-account role ARNs (create-handler, ttl-handler),
// plus either the operator's explicit --trust-principal values (which
// REPLACE the derived caller entry, not append a fourth) or the derived
// operator ARN when none were supplied. Derivation failure is non-fatal —
// it is logged to out and the third slot is simply omitted, matching the
// documented fall-back-to-pattern escape hatch.
func resolveTrustedPrincipals(prefix, trustAccountID string, explicit []string, caller AccountCallerIdentity, out io.Writer) ([]string, error) {
	createHandlerARN := fmt.Sprintf("arn:aws:iam::%s:role/%s-create-handler", trustAccountID, prefix)
	ttlHandlerARN := fmt.Sprintf("arn:aws:iam::%s:role/%s-ttl-handler", trustAccountID, prefix)
	principals := []string{createHandlerARN, ttlHandlerARN}

	if len(explicit) > 0 {
		return append(principals, explicit...), nil
	}

	derived, err := deriveOperatorPrincipalARN(caller.ARN, trustAccountID)
	if err != nil {
		// Hard failure, not a note. Dropping the operator principal would
		// still produce a link that a REMOTE create can use (create-handler
		// is trusted), so the enrollment would look successful — but
		// `km create --local` and, worse, `km destroy` run as the operator
		// and could not assume the launcher. A link that cannot be torn
		// down is the expensive failure this phase exists to prevent.
		return nil, fmt.Errorf(
			"cannot derive the operator's principal in account %s: %w\n\n"+
				"Look up the real ARN with your HOME-account profile:\n"+
				"    aws iam list-roles --profile <home-profile> \\\n"+
				"      --path-prefix /aws-reserved/sso.amazonaws.com/ \\\n"+
				"      --query \"Roles[?starts_with(RoleName,'AWSReservedSSO_<PermissionSet>')].Arn\" --output text\n\n"+
				"then re-run with:\n"+
				"    --trust-principal <that-arn>\n\n"+
				"(--trust-principal replaces only this derived entry; %s-create-handler and\n"+
				"%s-ttl-handler are still trusted automatically. Use --trust-principal-pattern\n"+
				"for an ArnLike glob if the role name is not stable.)",
			trustAccountID, err, prefix, prefix)
	}
	return append(principals, derived), nil
}

// ======================== External id resolution ================================

// mintExternalID generates a cryptographically secure external id: 32 random
// bytes, hex-encoded to 64 characters. Uses crypto/rand exclusively — never
// math/rand (T-126-24: an external id is always in force).
func mintExternalID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mint external id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// resolveExternalID resolves the external id in priority order: an explicit
// --external-id value, a --sops file's "external_id" key (Phase 89
// SOPS→SSM-adjacent pattern — decrypted operator-side, see
// docs/sandbox-secrets.md), or a freshly minted value. Never returns an
// empty string.
func resolveExternalID(explicit, sopsFile string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if sopsFile != "" {
		secrets, err := check.DecryptSopsToMap(sopsFile)
		if err != nil {
			return "", fmt.Errorf("decrypt --sops %s: %w", sopsFile, err)
		}
		v, ok := secrets["external_id"]
		if !ok || v == "" {
			return "", fmt.Errorf("--sops %s: no non-empty \"external_id\" key found", sopsFile)
		}
		return v, nil
	}
	return mintExternalID()
}

// ======================== Link fragment ==========================================

// accountLinksDir returns ~/.km/account-links — the operator-local, outside-
// the-repo home for every generated unit directory and link fragment
// (decision 4: the generated unit is a regenerable artifact, not a source of
// truth, and keeping it out of the working tree means the external id,
// rendered into the unit's inputs, cannot be committed by accident).
func accountLinksDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".km", "account-links"), nil
}

// accountLinkUnitDir returns ~/.km/account-links/<name> — the generated
// terragrunt unit directory for a named link.
func accountLinkUnitDir(name string) (string, error) {
	dir, err := accountLinksDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// accountLinkFragmentPath returns ~/.km/account-links/<name>.link.yaml.
func accountLinkFragmentPath(name string) (string, error) {
	dir, err := accountLinksDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".link.yaml"), nil
}

// writeAccountLinkFragment marshals frag as YAML and writes it at
// owner-only permissions (T-126-23) to accountLinkFragmentPath(frag.Name),
// creating the containing directory (also owner-only) if needed.
func writeAccountLinkFragment(frag AccountLinkFragment) (string, error) {
	dir, err := accountLinksDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating account-links directory: %w", err)
	}
	path, err := accountLinkFragmentPath(frag.Name)
	if err != nil {
		return "", err
	}
	data, err := yaml.Marshal(frag)
	if err != nil {
		return "", fmt.Errorf("marshaling link fragment: %w", err)
	}
	header := "# km account-links fragment — generated by km account add. Contains a secret\n" +
		"# (external_id); do not commit. Consumed by km account register.\n\n"
	if err := os.WriteFile(path, append([]byte(header), data...), 0o600); err != nil {
		return "", fmt.Errorf("writing link fragment: %w", err)
	}
	return path, nil
}

// ======================== Cobra Command Tree ====================================

// NewAccountCmd returns the "km account" parent command: "add" (plan 05,
// this file) provisions the target-account footprint; "register"/"list"/"rm"
// (plan 07, account_register.go) manage the home-account half.
func NewAccountCmd(cfg *config.Config) *cobra.Command {
	accountCmd := &cobra.Command{
		Use:          "account",
		Short:        "Manage cross-account capacity-borrowing links",
		SilenceUsage: true,
	}
	accountCmd.AddCommand(newAccountAddCmd(cfg))
	accountCmd.AddCommand(newAccountRegisterCmd(cfg))
	accountCmd.AddCommand(newAccountListCmd(cfg))
	accountCmd.AddCommand(newAccountRmCmd(cfg))
	return accountCmd
}

func newAccountAddCmd(cfg *config.Config) *cobra.Command {
	opts := AccountAddOpts{}
	var instanceTypesCSV string
	cmd := &cobra.Command{
		Use:          "add <name>",
		Short:        "Provision the cross-account capacity-borrowing footprint into a target AWS account",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			if instanceTypesCSV != "" {
				for _, t := range strings.Split(instanceTypesCSV, ",") {
					t = strings.TrimSpace(t)
					if t != "" {
						opts.InstanceTypes = append(opts.InstanceTypes, t)
					}
				}
			}
			return RunAccountAdd(cfg, opts, findRepoRoot(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&opts.TrustAccountID, "trust", "", "account A's (home application account's) AWS account id (required)")
	cmd.Flags().StringVar(&opts.Region, "region", "us-east-1", "AWS region for the launcher footprint")
	cmd.Flags().BoolVar(&opts.ProvisionNetwork, "provision-network", false, "provision a lean VPC/subnets/SG in the target account")
	cmd.Flags().StringVar(&opts.SubnetID, "subnet", "", "existing subnet id to reuse (mutually exclusive with --provision-network)")
	cmd.Flags().StringVar(&opts.SecurityGroupID, "sg", "", "existing security group id to reuse (mutually exclusive with --provision-network)")
	cmd.Flags().IntVar(&opts.AZCount, "az-count", 2, "number of AZs to spread the provisioned network across")
	cmd.Flags().BoolVar(&opts.ProvisionEFS, "provision-efs", false, "provision a B-local EFS filesystem (shared weights cache/scratch)")
	cmd.Flags().StringVar(&instanceTypesCSV, "instance-types", "", "comma-separated allowlisted EC2 instance types (required)")
	cmd.Flags().BoolVar(&opts.EnableBedrock, "enable-bedrock", false, "grant the box role bedrock:InvokeModel in the target account")
	cmd.Flags().StringVar(&opts.ExternalID, "external-id", "", "STS ExternalId to use verbatim (default: auto-generated)")
	cmd.Flags().StringVar(&opts.SopsFile, "sops", "", "SOPS-encrypted file carrying an external_id key")
	cmd.Flags().StringArrayVar(&opts.TrustPrincipals, "trust-principal", nil, "exact IAM role ARN to trust (repeatable; replaces the derived operator principal)")
	cmd.Flags().StringArrayVar(&opts.TrustPrincipalPatterns, "trust-principal-pattern", nil, "ArnLike glob pattern to trust (repeatable; escape hatch for a non-derivable principal)")
	cmd.Flags().StringVar(&opts.AWSProfile, "aws-profile", "", "AWS profile for the TARGET account's admin credentials (required)")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", true, "render + validate only; set --dry-run=false to apply")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "re-apply the module against an already-enrolled link (picks up launcher/box policy changes; leaves external id, role ARNs and network untouched)")
	cmd.Flags().StringVar(&opts.StateBucketOverride, "state-bucket", "", "override the derived link-state S3 bucket name")
	cmd.Flags().StringVar(&opts.LockTableOverride, "lock-table", "", "override the derived link-lock DynamoDB table name")
	return cmd
}

// ======================== RunAccountAdd ==========================================

// RunAccountAdd implements the km account add flow. Exported so cmd_test can
// call it directly with injected seams. repoRoot is passed explicitly
// (tests supply t.TempDir(); production passes findRepoRoot()) — it anchors
// the absolute terraform.source path, since the generated unit lives outside
// the repository and cannot use find_in_parent_folders.
//
// Sequence:
//  1. Validate flag combinations — before any AWS call.
//  2. Idempotency: if name is already in cfg.LaunchAccounts, print the
//     existing link and return nil.
//  3. Load the target account's AWS config + resolve caller identity
//     (validates credentials as a side effect); fail if the resolved
//     account id equals the trust account id (T-126-25).
//  4. Resolve the external id (T-126-24: never empty).
//  5. Resolve the three trusted principal ARNs (T-126-26: all authored on
//     the first apply) + any trust-principal patterns.
//  6. ExportTerragruntEnvVars(cfg) — BEFORE any terragrunt invocation.
//  7. Real run: EnsureLinkStateBackend (BEFORE the HCL is rendered — the
//     backend block names the bucket/table it returns, and BEFORE the first
//     runner invocation). Dry run: resolve the same three names WITHOUT
//     creating anything (see step 8's load-bearing note).
//  8. Write the unit directory + rendered HCL. Real run: Plan then Apply.
//     Dry run: InitNoBackend then Validate — NOT Plan — because the
//     rendered unit carries an S3 backend block naming a bucket that does
//     not exist on a dry run, and a normal init/plan would hard-fail with
//     NoSuchBucket rather than degrading gracefully. This is stated plainly
//     in the printed output so the operator does not mistake a config
//     validation for a resource plan.
//  9. Real run only: read module outputs, assemble + write the link
//     fragment, print the `km account register` invocation. Never print the
//     external id itself.
//  10. If Bedrock was enabled, print the model-access reminder.
func RunAccountAdd(cfg *config.Config, opts AccountAddOpts, repoRoot string, out io.Writer) error {
	ctx := context.Background()

	// 1. Validate flag combinations up front, before any AWS call.
	if opts.ProvisionNetwork && opts.SubnetID != "" {
		return fmt.Errorf("--provision-network and --subnet are mutually exclusive: " +
			"provision-network builds its own network, an explicit --subnet reuses an existing one")
	}
	if opts.TrustAccountID == "" {
		return fmt.Errorf("--trust <A-account-id> is required")
	}
	if len(opts.InstanceTypes) == 0 {
		return fmt.Errorf("--instance-types is required — it is the only thing scoping the launcher")
	}

	// 2. Idempotency: if the name is already an enrolled link, report and exit —
	// unless --force asks us to re-apply.
	//
	// The plain early-exit exists so a re-run of the enrollment command is cheap
	// and safe. But it also blocks the one case that genuinely needs a re-run:
	// this command owns the target account's Terraform, so when the
	// gpu-launcher-account MODULE changes (a widened launcher policy, a fixed tag
	// condition, a new box-role grant), the only way to converge an already-enrolled
	// link is to apply it again. Without --force the operator's options were to
	// tear the link down and rebuild it, or hand-edit IAM in the target account.
	//
	// Re-applying is a normal terraform apply against the link's own state: it
	// converges the footprint and leaves the external id, role ARNs and network
	// untouched.
	if link, ok := cfg.GetLaunchAccount(opts.Name); ok {
		if !opts.Force {
			fmt.Fprintf(out, "Launch account link %q already enrolled: launcher=%s box=%s\n",
				opts.Name, link.LauncherRoleARN, link.BoxRoleARN)
			fmt.Fprintf(out, "  Re-run with --force to re-apply the module (picks up launcher/box policy changes).\n")
			return nil
		}
		fmt.Fprintf(out, "Re-applying enrolled link %q (--force): launcher=%s\n", opts.Name, link.LauncherRoleARN)
	}

	// 3. Load the target account's AWS config and resolve caller identity.
	// This profile MUST be the target account's administrator.
	awsCfg, err := awspkg.LoadAWSConfigInRegion(ctx, opts.AWSProfile, opts.Region)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", opts.AWSProfile, err)
	}
	caller, err := NewAccountCallerIdentityFunc(ctx, awsCfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Enrolling as account %s (caller: %s)\n", caller.AccountID, caller.ARN)
	if caller.AccountID == opts.TrustAccountID {
		return fmt.Errorf(
			"resolved account id %s equals --trust %s — refusing to enroll the home account into itself",
			caller.AccountID, opts.TrustAccountID)
	}

	// 4. Resolve the external id. Never empty (T-126-24).
	externalID, err := resolveExternalID(opts.ExternalID, opts.SopsFile)
	if err != nil {
		return err
	}

	// 5. Resolve the three trusted principals (T-126-26) + any patterns.
	principals, err := resolveTrustedPrincipals(cfg.GetResourcePrefix(), opts.TrustAccountID, opts.TrustPrincipals, caller, out)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Trusting %d principal(s):\n", len(principals))
	for _, p := range principals {
		fmt.Fprintf(out, "  - %s\n", p)
	}
	if len(opts.TrustPrincipalPatterns) > 0 {
		fmt.Fprintf(out, "Trusting %d ArnLike pattern(s):\n", len(opts.TrustPrincipalPatterns))
		for _, p := range opts.TrustPrincipalPatterns {
			fmt.Fprintf(out, "  - %s\n", p)
		}
	}

	// 6. Export config env vars BEFORE any terragrunt invocation (also
	// supplies KM_ARTIFACTS_BUCKET, read by the template's get_env call).
	ExportTerragruntEnvVars(cfg)

	regionLabel := compiler.RegionLabel(opts.Region)
	moduleSource, err := filepath.Abs(filepath.Join(repoRoot, "infra", "modules", "gpu-launcher-account", "v1.0.0"))
	if err != nil {
		return fmt.Errorf("resolving module source path: %w", err)
	}

	unitDir, err := accountLinkUnitDir(opts.Name)
	if err != nil {
		return err
	}

	runner := NewAccountRunnerFunc(opts.AWSProfile, repoRoot)

	if opts.DryRun {
		// 7 (dry-run). Resolve names WITHOUT creating anything — see the
		// load-bearing note in the function doc comment.
		bucket := opts.StateBucketOverride
		if bucket == "" {
			bucket = LinkStateBucketName(cfg.GetResourcePrefix(), caller.AccountID, regionLabel)
		}
		table := opts.LockTableOverride
		if table == "" {
			table = LinkLockTableName(cfg.GetResourcePrefix(), regionLabel)
		}
		key := LinkStateKey(opts.Name)

		hcl := GenerateAccountLinkHCL(AccountLinkHCLParams{
			Name: opts.Name, ModuleSource: moduleSource,
			StateBucket: bucket, StateKey: key, LockTable: table,
			Region: opts.Region, ResourcePrefix: cfg.GetResourcePrefix(),
			TrustAccountID: opts.TrustAccountID, TrustedPrincipalARNs: principals,
			TrustedPrincipalARNPatterns: opts.TrustPrincipalPatterns, ExternalID: externalID,
			InstanceTypes: opts.InstanceTypes, ProvisionNetwork: opts.ProvisionNetwork,
			SubnetID: opts.SubnetID, SecurityGroupID: opts.SecurityGroupID, AZCount: opts.AZCount,
			ProvisionEFS: opts.ProvisionEFS, EnableBedrock: opts.EnableBedrock,
		})
		if err := os.MkdirAll(unitDir, 0o700); err != nil {
			return fmt.Errorf("creating unit directory: %w", err)
		}
		if err := os.WriteFile(filepath.Join(unitDir, "terragrunt.hcl"), []byte(hcl), 0o600); err != nil {
			return fmt.Errorf("writing terragrunt.hcl: %w", err)
		}

		fmt.Fprintf(out, "(dry-run) would create link state bucket %q and lock table %q — neither exists yet\n", bucket, table)
		fmt.Fprintln(out, "(dry-run) a full resource-level plan is unavailable on this path — it needs the "+
			"backend, which dry-run deliberately does not create. Validating the rendered configuration "+
			"only (terragrunt init -backend=false + validate).")

		if err := runner.InitNoBackend(ctx, unitDir); err != nil {
			return fmt.Errorf("terragrunt init -backend=false failed: %w", err)
		}
		if err := runner.Validate(ctx, unitDir); err != nil {
			return fmt.Errorf("terragrunt validate failed: %w", err)
		}
		fmt.Fprintln(out, "(dry-run) configuration validated — re-run with --dry-run=false to apply")
		return nil
	}

	// 7 (real run). Create/validate the backend BEFORE the HCL is rendered
	// and BEFORE the first runner invocation — the backend block names the
	// bucket/table this call returns.
	bucket, table, key, err := EnsureLinkStateBackend(
		ctx, awsCfg, cfg.GetResourcePrefix(), caller.AccountID, regionLabel, opts.Name,
		opts.StateBucketOverride, opts.LockTableOverride,
	)
	if err != nil {
		return fmt.Errorf("preparing link state backend: %w", err)
	}

	hcl := GenerateAccountLinkHCL(AccountLinkHCLParams{
		Name: opts.Name, ModuleSource: moduleSource,
		StateBucket: bucket, StateKey: key, LockTable: table,
		Region: opts.Region, ResourcePrefix: cfg.GetResourcePrefix(),
		TrustAccountID: opts.TrustAccountID, TrustedPrincipalARNs: principals,
		TrustedPrincipalARNPatterns: opts.TrustPrincipalPatterns, ExternalID: externalID,
		InstanceTypes: opts.InstanceTypes, ProvisionNetwork: opts.ProvisionNetwork,
		SubnetID: opts.SubnetID, SecurityGroupID: opts.SecurityGroupID, AZCount: opts.AZCount,
		ProvisionEFS: opts.ProvisionEFS, EnableBedrock: opts.EnableBedrock,
	})

	// 8. Write the unit directory + HCL, then plan and apply.
	if err := os.MkdirAll(unitDir, 0o700); err != nil {
		return fmt.Errorf("creating unit directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(unitDir, "terragrunt.hcl"), []byte(hcl), 0o600); err != nil {
		return fmt.Errorf("writing terragrunt.hcl: %w", err)
	}
	if err := runner.Plan(ctx, unitDir); err != nil {
		return fmt.Errorf("terragrunt plan failed: %w", err)
	}
	if err := runner.Apply(ctx, unitDir); err != nil {
		return fmt.Errorf("terragrunt apply failed: %w", err)
	}

	// 9. Read outputs, assemble + write the link fragment.
	outputs, err := runner.Output(ctx, unitDir)
	if err != nil {
		return fmt.Errorf("getting module outputs: %w", err)
	}
	frag := AccountLinkFragment{
		Name:              opts.Name,
		AccountID:         fmt.Sprintf("%v", extractValue(outputs["account_id"])),
		LauncherRoleARN:   fmt.Sprintf("%v", extractValue(outputs["launcher_role_arn"])),
		BoxRoleARN:        fmt.Sprintf("%v", extractValue(outputs["box_role_arn"])),
		ExternalID:        externalID,
		Region:            fmt.Sprintf("%v", extractValue(outputs["region"])),
		SubnetIDs:         toStringSlice(extractValue(outputs["subnet_ids"])),
		AvailabilityZones: toStringSlice(extractValue(outputs["availability_zones"])),
		SecurityGroupID:   fmt.Sprintf("%v", extractValue(outputs["security_group_id"])),
		ResultsBucket:     fmt.Sprintf("%v", extractValue(outputs["results_bucket"])),
		EFSID:             fmt.Sprintf("%v", extractValue(outputs["efs_id"])),
		InstanceTypes:     opts.InstanceTypes,
		StateBucket:       bucket,
		LockTable:         table,
		StateKey:          key,
	}
	fragPath, err := writeAccountLinkFragment(frag)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Launch account %q provisioned: launcher=%s box=%s\n", opts.Name, frag.LauncherRoleARN, frag.BoxRoleARN)
	fmt.Fprintf(out, "Link fragment written to %s (owner-only; contains a secret — do not commit)\n", fragPath)
	fmt.Fprintf(out, "Next: run this with account A's own credentials:\n")
	fmt.Fprintf(out, "  km account register %s --from-fragment %s\n", opts.Name, fragPath)
	if opts.EnableBedrock {
		fmt.Fprintln(out, "Note: Bedrock model access may still need a one-time console confirmation "+
			"in the target account, and traffic through a lean box there is unmetered.")
	}
	return nil
}

// toStringSlice converts a decoded terragrunt-output JSON value
// ([]interface{} of strings, typically) into []string. Nil/unrecognized
// input returns nil.
func toStringSlice(v interface{}) []string {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		out = append(out, fmt.Sprintf("%v", e))
	}
	return out
}
