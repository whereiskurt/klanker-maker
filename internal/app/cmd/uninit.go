package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	organizationstypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
	"github.com/whereiskurt/klanker-maker/pkg/terragrunt"
)

// UninitRunner is a narrow interface for the Destroy + Reconfigure operations
// uninit needs from terragrunt, allowing test injection.
//
// Reconfigure runs `terragrunt init -reconfigure` before each Destroy to
// handle local .terragrunt-cache drift — common when an operator upgraded km
// (or pulled the slack-init env-var fix) and the backend bucket name now
// resolves to a different KM_RESOURCE_PREFIX than when state was first
// written. Without it, terragrunt's auto-init hits "Backend configuration
// block has changed" and bails before touching any resources.
//
// Destroy must return an error whose message includes terragrunt's stderr
// (or at least the relevant error text) so callers can pattern-match on
// signatures like "Backend configuration block has changed". The production
// implementation (uninitRunnerAdapter) uses Runner.DestroyWithStderr to
// satisfy this; mocks can return any error string they like.
type UninitRunner interface {
	Destroy(ctx context.Context, dir string) error
	Reconfigure(ctx context.Context, dir string) error
}

// uninitRunnerAdapter wraps the production *terragrunt.Runner and embeds
// terragrunt's stderr into Destroy's returned error so isBackendDriftError
// can match on the actual terraform output. Without this, Destroy() returns
// only the process exit error ("exit status 1") and the diagnostic text
// — including "Backend configuration block has changed" — is lost.
type uninitRunnerAdapter struct {
	inner *terragrunt.Runner
}

func (a *uninitRunnerAdapter) Destroy(ctx context.Context, dir string) error {
	var stderrBuf strings.Builder
	if err := a.inner.DestroyWithStderr(ctx, dir, &stderrBuf); err != nil {
		stderr := strings.TrimSpace(stderrBuf.String())
		if stderr == "" {
			return err
		}
		return fmt.Errorf("%w\n%s", err, stderr)
	}
	return nil
}

func (a *uninitRunnerAdapter) Reconfigure(ctx context.Context, dir string) error {
	return a.inner.Reconfigure(ctx, dir)
}

// ECRRepoDeleter abstracts ECR repository deletion. Returns nil when the
// repository doesn't exist (treated as already-deleted) so callers can
// loop idempotently across the well-known repo list.
type ECRRepoDeleter interface {
	DeleteRepository(ctx context.Context, region, name string) error
}

// UninitOrgsAPI covers the Organizations operations uninit needs for SCP cleanup
// when --include-scp is set (Gap #3b, Phase 84.4.1.1).
// The real *organizations.Client satisfies this interface.
type UninitOrgsAPI interface {
	ListPoliciesForTarget(ctx context.Context, params *organizations.ListPoliciesForTargetInput, optFns ...func(*organizations.Options)) (*organizations.ListPoliciesForTargetOutput, error)
	DetachPolicy(ctx context.Context, params *organizations.DetachPolicyInput, optFns ...func(*organizations.Options)) (*organizations.DetachPolicyOutput, error)
	DeletePolicy(ctx context.Context, params *organizations.DeletePolicyInput, optFns ...func(*organizations.Options)) (*organizations.DeletePolicyOutput, error)
}

// UninitOpts captures the user-facing options for one uninit run.
// Use a struct (not positional booleans) so adding --include-scp does not
// require updating all callers — mirrors UnbootstrapOpts in unbootstrap.go.
type UninitOpts struct {
	Force      bool
	IncludeSCP bool
	OrgsClient UninitOrgsAPI // injected for SCP detach; nil = skip SCP cleanup
}

// awsCLIECRDeleter shells out to the AWS CLI to match init.go's existing
// pattern (init.go also shells out to `aws ecr describe-repositories /
// create-repository` rather than using the SDK), avoiding a new module
// dependency. RepositoryNotFoundException is treated as success.
type awsCLIECRDeleter struct {
	awsProfile string
}

func (d *awsCLIECRDeleter) DeleteRepository(ctx context.Context, region, name string) error {
	cmd := exec.CommandContext(ctx, "aws", "ecr", "delete-repository",
		"--repository-name", name,
		"--force",
		"--region", region,
		"--profile", d.awsProfile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// AWS CLI prints "RepositoryNotFoundException" in stderr/stdout when
		// the repo doesn't exist — treat as already-deleted.
		if strings.Contains(string(out), "RepositoryNotFoundException") {
			return nil
		}
		return fmt.Errorf("aws ecr delete-repository %s: %s: %w", name, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// ecrReposToDelete is the list of ECR repositories created by km init's
// container-substrate path. Names are NOT prefixed with resource_prefix
// (init.go hardcodes "km-sandbox" etc.), so a uninit on one resource_prefix
// would also affect another install in the same AWS account if any exists.
// Operators with multi-install setups should disable container_substrates_enabled
// or skip ECR cleanup.
var ecrReposToDelete = []string{
	"km-sandbox",
	"km-dns-proxy",
	"km-http-proxy",
	"km-audit-log",
	"km-tracing",
}

// NewUninitCmd creates the "km uninit" subcommand.
// Usage: km uninit [--region <region>] [--aws-profile <name>] [--force] [--include-scp]
//
// Command flow:
//  1. Validate AWS credentials
//  2. Check for active sandboxes in the region (requires StateBucket; error if not set unless --force)
//  2.5. SCP cleanup (only with --include-scp): detach+delete {prefix}-sandbox-containment before module destroy
//  3. If active sandboxes exist and --force is not set: return error
//  4. Destroy all regional modules in reverse dependency order
func NewUninitCmd(cfg *config.Config) *cobra.Command {
	var awsProfile string
	var region string
	var force bool
	var yes bool
	var verbose bool
	var includeSCP bool

	cmd := &cobra.Command{
		Use:   "uninit",
		Short: "Tear down all shared regional infrastructure for a region",
		Long:  helpText("uninit"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				fmt.Printf("Destroy ALL shared infrastructure in %s? This cannot be undone. [y/N] ", region)
				var answer string
				fmt.Scanln(&answer)
				if answer != "y" && answer != "Y" && answer != "yes" {
					fmt.Println("Aborted.")
					return nil
				}
			}
			if awsProfile == "" {
				awsProfile = "klanker-application"
			}
			return runUninit(cfg, awsProfile, region, force, verbose, includeSCP)
		},
	}

	cmd.Flags().StringVar(&awsProfile, "aws-profile", "klanker-application",
		"AWS CLI profile to use for teardown")
	cmd.Flags().StringVar(&region, "region", "us-east-1",
		"AWS region to uninitialize (e.g. us-east-1, ca-central-1)")
	cmd.Flags().BoolVar(&force, "force", false,
		"Destroy even if active sandboxes exist in the region")
	cmd.Flags().BoolVar(&yes, "yes", false,
		"Skip confirmation prompt")
	cmd.Flags().BoolVar(&verbose, "verbose", false,
		"Show full terragrunt/terraform output")
	cmd.Flags().BoolVar(&includeSCP, "include-scp", false,
		"Also detach and delete the install's sandbox-containment SCP from AWS Organizations (requires "+cfg.GetResourcePrefix()+"-org-admin role)")

	return cmd
}

// runUninit is the top-level uninit logic (uses real AWS clients).
func runUninit(cfg *config.Config, awsProfile, region string, force bool, verbose bool, includeSCP bool) error {
	ctx := context.Background()

	// Validate AWS credentials
	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", awsProfile, err)
	}
	if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil {
		return fmt.Errorf("AWS credential validation failed: %w", err)
	}

	// Export config values as env vars for Terragrunt's site.hcl get_env() calls.
	// Use the canonical helper so KM_RESOURCE_PREFIX (and other Phase-66 vars)
	// are included — the previous hand-rolled copy missed those, which made
	// terragrunt resolve the backend bucket as tf-km-state-* instead of the
	// operator's tf-{prefix}-state-* and fail with HeadBucket 403.
	ExportTerragruntEnvVars(cfg)
	if cfg.Route53ZoneID != "" && os.Getenv("KM_ROUTE53_ZONE_ID") == "" {
		os.Setenv("KM_ROUTE53_ZONE_ID", cfg.Route53ZoneID)
	}

	repoRoot := findRepoRoot()
	tgRunner := terragrunt.NewRunner(awsProfile, repoRoot)
	tgRunner.Verbose = verbose
	runner := &uninitRunnerAdapter{inner: tgRunner}

	var lister SandboxLister
	if cfg.StateBucket != "" {
		// Use the canonical newRealLister constructor so dynamoClient + tableName
		// are wired up. ListSandboxes is Dynamo-first with S3 fallback on
		// ResourceNotFoundException, but the fallback only kicks in if dynamoClient
		// is non-nil. A hand-rolled construction with only s3Client/bucket panics
		// on first .Scan() — exposed by Phase 84.4's multi-install testbed where
		// the probe install has no sandboxes table.
		lister = newRealLister(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())
	}

	ecrDeleter := &awsCLIECRDeleter{awsProfile: awsProfile}

	// Build the Organizations client for SCP cleanup (only when --include-scp set).
	// Mirror the doctor.go:2900 pattern: use klanker-terraform profile + AssumeRole
	// into {prefix}-org-admin in the organization management account.
	var orgsClient UninitOrgsAPI
	if includeSCP {
		tfProfile := cfg.AWSProfile
		if tfProfile == "" {
			tfProfile = "klanker-terraform"
		}
		tfCfg, tfErr := awspkg.LoadAWSConfig(ctx, tfProfile)
		if tfErr == nil {
			orgAccountID := cfg.OrganizationAccountID
			if orgAccountID != "" {
				roleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s-org-admin", orgAccountID, cfg.GetResourcePrefix())
				stsClient := sts.NewFromConfig(tfCfg)
				assumeOut, assumeErr := stsClient.AssumeRole(ctx, &sts.AssumeRoleInput{
					RoleArn:         awssdk.String(roleARN),
					RoleSessionName: awssdk.String("km-uninit"),
				})
				if assumeErr == nil {
					orgsRegion := cfg.PrimaryRegion
					if orgsRegion == "" {
						orgsRegion = region
					}
					orgsCfg, _ := awsconfig.LoadDefaultConfig(ctx,
						awsconfig.WithRegion(orgsRegion),
						awsconfig.WithCredentialsProvider(
							newStaticCredentials(
								awssdk.ToString(assumeOut.Credentials.AccessKeyId),
								awssdk.ToString(assumeOut.Credentials.SecretAccessKey),
								awssdk.ToString(assumeOut.Credentials.SessionToken),
							),
						),
					)
					orgsClient = organizations.NewFromConfig(orgsCfg)
				} else {
					// AssumeRole failed — fall back to current profile (same as doctor.go pattern).
					fmt.Printf("  [warn] AssumeRole into %s-org-admin failed: %v — using current credentials for SCP cleanup\n", cfg.GetResourcePrefix(), assumeErr)
					orgsClient = organizations.NewFromConfig(tfCfg)
				}
			} else {
				// No org account configured — use the terraform profile as-is.
				orgsClient = organizations.NewFromConfig(tfCfg)
			}
		}
		// If tfErr != nil, orgsClient stays nil; RunUninitWithDeps will warn and skip.
	}

	// Phase 89: KMS cleanup — delete own-prefix sandbox-secrets alias + schedule-delete key.
	// Runs BEFORE module destroy (reverse-bootstrap order: secrets-key → ses → foundation).
	// Non-fatal: log + continue; sibling-install protection is inside deleteOwnSecretsKMSAlias.
	kmsClient := kms.NewFromConfig(awsCfg)
	if err := deleteOwnSecretsKMSAlias(ctx, kmsClient, cfg.GetResourcePrefix()); err != nil {
		log.Warn().Err(err).Msg("delete own sandbox-secrets KMS alias")
	}

	return RunUninitWithDeps(cfg, runner, lister, ecrDeleter, region, UninitOpts{
		Force:      force,
		IncludeSCP: includeSCP,
		OrgsClient: orgsClient,
	})
}

// RunUninitWithDeps is the testable core of uninit with dependency injection.
// It accepts a UninitRunner, SandboxLister, and ECRRepoDeleter to allow unit
// testing without AWS. Pass a nil ECRRepoDeleter to skip the ECR cleanup pass
// (e.g. for tests that only exercise terragrunt destroy ordering).
//
// Exported for use in uninit_test.go.
func RunUninitWithDeps(cfg *config.Config, runner UninitRunner, lister SandboxLister, ecrDeleter ECRRepoDeleter, region string, opts UninitOpts) error {
	ctx := context.Background()

	// Step 1: Verify we can check for active sandboxes.
	// If StateBucket is not configured, we can't verify — require --force.
	if cfg.StateBucket == "" && !opts.Force {
		log.Info().Str("region", region).Msg("uninit: state_bucket not configured, cannot verify active sandboxes")
		return fmt.Errorf(
			"cannot verify active sandboxes — state_bucket not configured; use --force to proceed without the check",
		)
	}

	// Step 2: Check for active sandboxes in the target region.
	// Only "running" status blocks uninit. Sandboxes in "stopping", "stopped",
	// or "creating" are intentionally excluded — they do not represent active
	// workloads that would conflict with infrastructure teardown.
	//
	// NOTE: TTL-expired sandboxes that have not yet been auto-destroyed by the
	// ttl-handler Lambda will still show Status="running" and WILL block uninit.
	// This is correct — the status filter does not consult TTLExpiry. If the
	// operator needs to proceed before the Lambda runs, use --force.
	if lister != nil && !opts.Force {
		records, err := lister.ListSandboxes(ctx, false)
		if err != nil {
			return fmt.Errorf("failed to list sandboxes (use --force to skip this check): %w", err)
		}

		log.Info().Str("region", region).Int("total_records", len(records)).Msg("uninit: ListSandboxes returned")

		activeCount := 0
		for _, r := range records {
			log.Debug().Str("sandbox_id", r.SandboxID).Str("status", r.Status).Str("sandbox_region", r.Region).Msg("uninit: evaluating sandbox record")
			if r.Region == region && r.Status == "running" {
				activeCount++
			}
		}

		log.Info().Str("region", region).Int("active_running", activeCount).Msg("uninit: active sandbox count after filter")

		if activeCount > 0 {
			return fmt.Errorf(
				"%d active sandbox(es) found in region %s — destroy them first or use --force to proceed anyway",
				activeCount, region,
			)
		}
	}

	// Step 2.5: SCP cleanup (Gap #3b, Phase 84.4.1.1).
	// Detach and delete the install's sandbox-containment SCP before module destroy.
	// Gated on --include-scp (default false). Warn-and-continue on failure.
	scpName := cfg.GetResourcePrefix() + "-sandbox-containment"
	if opts.IncludeSCP {
		if opts.OrgsClient == nil {
			fmt.Printf("  [warn] --include-scp set but no Organizations client available; SCP %s not cleaned up\n", scpName)
			fmt.Printf("         To clean up manually, assume %s-org-admin and run:\n", cfg.GetResourcePrefix())
			fmt.Printf("         aws organizations detach-policy --policy-id <id> --target-id %s\n", cfg.ApplicationAccountID)
			fmt.Printf("         aws organizations delete-policy --policy-id <id>\n")
		} else {
			// Find the policy ID by listing SCPs on the application account.
			targetOut, listErr := opts.OrgsClient.ListPoliciesForTarget(ctx, &organizations.ListPoliciesForTargetInput{
				TargetId: awssdk.String(cfg.ApplicationAccountID),
				Filter:   organizationstypes.PolicyTypeServiceControlPolicy,
			})
			var policyID string
			if listErr == nil {
				for _, p := range targetOut.Policies {
					if awssdk.ToString(p.Name) == scpName {
						policyID = awssdk.ToString(p.Id)
						break
					}
				}
			}
			if policyID == "" {
				fmt.Printf("  [warn] SCP %s not found on account %s — already detached or never attached\n",
					scpName, cfg.ApplicationAccountID)
			} else {
				fmt.Printf("Detaching SCP %s (id: %s)...\n", scpName, policyID)
				if _, detachErr := opts.OrgsClient.DetachPolicy(ctx, &organizations.DetachPolicyInput{
					PolicyId: awssdk.String(policyID),
					TargetId: awssdk.String(cfg.ApplicationAccountID),
				}); detachErr != nil {
					fmt.Printf("  [warn] DetachPolicy failed: %v — continuing with module destroy\n", detachErr)
				} else {
					fmt.Printf("  SCP detached\n")
					if _, delErr := opts.OrgsClient.DeletePolicy(ctx, &organizations.DeletePolicyInput{
						PolicyId: awssdk.String(policyID),
					}); delErr != nil {
						fmt.Printf("  [warn] DeletePolicy failed: %v — SCP detached but not deleted; delete manually\n", delErr)
					} else {
						fmt.Printf("  SCP deleted\n")
					}
				}
			}
		}
	} else {
		// Not --include-scp: print a WARN so operators know the SCP persists.
		fmt.Printf("  [warn] SCP %s not cleaned up — re-run with --include-scp to detach+delete\n", scpName)
		fmt.Printf("         Or manually: assume %s-org-admin, aws organizations detach-policy + delete-policy\n", cfg.GetResourcePrefix())
	}

	// Step 3: Destroy modules in REVERSE dependency order using the same
	// regionalModules() definition km init applies. Reversing keeps init/uninit
	// in lockstep — adding a new module to init automatically destroys it on
	// uninit too, no second list to drift.
	repoRoot := findRepoRoot()
	regionLabel := compiler.RegionLabel(region)
	regionDir := filepath.Join(repoRoot, "infra", "live", regionLabel)

	applyOrder := regionalModules(regionDir)
	// Reverse in place into a fresh slice so applyOrder isn't mutated.
	modules := make([]regionalModule, len(applyOrder))
	for i, m := range applyOrder {
		modules[len(applyOrder)-1-i] = m
	}

	// Step 4: Destroy each module. Skip missing directories; continue on error.
	// Run `terragrunt init -reconfigure` before destroy to refresh the local
	// .terragrunt-cache backend pointer — this fixes the common drift case
	// after a km upgrade. We track modules whose destroy hits the
	// "backend configuration block has changed" signature so the operator
	// gets one consolidated diagnostic at the end (instead of 30 lines of
	// terraform stack trace per affected module).
	destroyed := 0
	var backendDriftModules []string
	for _, mod := range modules {
		if _, err := os.Stat(mod.dir); os.IsNotExist(err) {
			fmt.Printf("  Skipping %s (directory not found)\n", mod.name)
			continue
		}

		// Reconfigure first. Failure here is informational — we still try
		// destroy; terragrunt may surface a clearer error than reconfigure does.
		if err := runner.Reconfigure(ctx, mod.dir); err != nil {
			fmt.Printf("  [info] %s init -reconfigure failed (continuing to destroy): %v\n", mod.name, err)
		}

		fmt.Printf("  Destroying %s...", mod.name)
		if err := runner.Destroy(ctx, mod.dir); err != nil {
			if isBackendDriftError(err) {
				fmt.Printf("\n  Warning: %s — state appears to live in a different backend bucket than the current config resolves to (likely written before a resource_prefix change). Resources may need manual cleanup; see post-uninit summary below.\n", mod.name)
				backendDriftModules = append(backendDriftModules, mod.name)
			} else {
				fmt.Printf("\n  Warning: %s destroy failed (continuing): %v\n", mod.name, err)
			}
			continue
		}
		fmt.Println(" done")
		destroyed++
	}

	// Step 5: Delete ECR repositories. Optional (skipped in tests with nil deleter).
	// Repos are global to the AWS account (not resource_prefix-namespaced), so a
	// multi-install operator should be aware this cleanup is shared.
	ecrDeleted := 0
	if ecrDeleter != nil {
		fmt.Println()
		fmt.Println("Deleting ECR repositories...")
		for _, repo := range ecrReposToDelete {
			fmt.Printf("  Deleting %s...", repo)
			if err := ecrDeleter.DeleteRepository(ctx, region, repo); err != nil {
				fmt.Printf("\n  Warning: %s deletion failed (continuing): %v\n", repo, err)
				continue
			}
			fmt.Println(" done")
			ecrDeleted++
		}
	}

	fmt.Printf("\nUninit complete for %s (%s): %d module(s) destroyed", region, regionLabel, destroyed)
	if ecrDeleter != nil {
		fmt.Printf(", %d ECR repo(s) deleted", ecrDeleted)
	}
	fmt.Println()

	// Surface a clear remediation block for any modules whose state lived in a
	// different backend bucket than the current resolved one. The most common
	// cause is a km upgrade that changed how KM_RESOURCE_PREFIX flows through
	// site.hcl after the module was first applied. Resources for these modules
	// were NOT destroyed by terragrunt and must be handled manually.
	if len(backendDriftModules) > 0 {
		fmt.Println()
		fmt.Println("──────────────────────────────────────────────────")
		fmt.Println("MANUAL CLEANUP REQUIRED")
		fmt.Println("──────────────────────────────────────────────────")
		fmt.Printf("The following %d module(s) hold state in a different backend bucket than the current km-config.yaml resolves to:\n\n", len(backendDriftModules))
		for _, m := range backendDriftModules {
			fmt.Printf("  • %s\n", m)
		}
		fmt.Println()
		fmt.Println("Likely cause: these modules were applied under a different KM_RESOURCE_PREFIX")
		fmt.Println("(usually pre-upgrade, when the prefix was empty/'km' instead of the operator's")
		fmt.Println("current value). terragrunt cannot read state from a bucket the current backend")
		fmt.Println("config doesn't reference, so destroy was skipped.")
		fmt.Println()
		fmt.Println("To recover, either:")
		fmt.Printf("  1. aws s3 ls --profile <terraform-profile> | grep tf-.*-state-  # find the orphan bucket\n")
		fmt.Println("     then run `terragrunt init -migrate-state` per affected module to move the state, then")
		fmt.Println("     re-run `km uninit --force`.")
		fmt.Println()
		fmt.Println("  2. Hand-delete the orphaned AWS resources for each module via the AWS console / CLI.")
		fmt.Println("──────────────────────────────────────────────────")
	}

	return nil
}

// isBackendDriftError returns true when err looks like a terragrunt failure
// caused by the local .terragrunt-cache or current backend block referring
// to a different bucket than the state was last written to. Matches both
// the direct "Backend configuration block has changed" message and the
// downstream "Backend initialization required" / dependency-resolution
// errors that fire when terragrunt can't read a dependency module's outputs
// for the same reason.
func isBackendDriftError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Backend configuration block has changed") ||
		strings.Contains(msg, "Backend initialization required")
}

// =============================================================================
// Phase 89: KMS sandbox-secrets cleanup for km uninit
// =============================================================================

// KMSAliasDeleter abstracts the KMS operations needed by uninit to clean up the
// per-install sandbox-secrets KMS alias and key. The real *kms.Client satisfies
// this interface in production; tests inject a mock.
type KMSAliasDeleter interface {
	ListAliases(ctx context.Context, params *kms.ListAliasesInput, optFns ...func(*kms.Options)) (*kms.ListAliasesOutput, error)
	DescribeKey(ctx context.Context, params *kms.DescribeKeyInput, optFns ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
	ListKeys(ctx context.Context, params *kms.ListKeysInput, optFns ...func(*kms.Options)) (*kms.ListKeysOutput, error)
	ListResourceTags(ctx context.Context, params *kms.ListResourceTagsInput, optFns ...func(*kms.Options)) (*kms.ListResourceTagsOutput, error)
	DeleteAlias(ctx context.Context, params *kms.DeleteAliasInput, optFns ...func(*kms.Options)) (*kms.DeleteAliasOutput, error)
	ScheduleKeyDeletion(ctx context.Context, params *kms.ScheduleKeyDeletionInput, optFns ...func(*kms.Options)) (*kms.ScheduleKeyDeletionOutput, error)
}

// deleteOwnSecretsKMSAlias deletes the per-install sandbox-secrets KMS alias and
// schedules the underlying key for deletion with a 7-day pending window.
//
// Key discovery is two-stage per Option (a) revision (BLOCKER 2):
//  1. Primary: DescribeKey("alias/{prefix}-sandbox-secrets") — one AWS round trip.
//  2. Fallback (NotFoundException): paginate ListKeys + ListResourceTags looking for
//     a key tagged with km:component=sandbox-secrets-key + km:resource_prefix={prefix}.
//     This recovers from partial-destroy where the alias was deleted but the key leaked.
//
// The pending window is 7 days (not the module's 30-day default) because uninit
// implies intentional teardown. Sibling-install aliases/keys are NEVER touched —
// key discovery is scoped to the exact alias name "alias/{prefix}-sandbox-secrets".
func deleteOwnSecretsKMSAlias(ctx context.Context, client KMSAliasDeleter, resourcePrefix string) error {
	wantAlias := fmt.Sprintf("alias/%s-sandbox-secrets", resourcePrefix)

	// Stage 1: alias→key via DescribeKey (one round trip; happy path).
	var keyID string
	aliasExists := false
	descOut, descErr := client.DescribeKey(ctx, &kms.DescribeKeyInput{
		KeyId: awssdk.String(wantAlias),
	})
	if descErr == nil && descOut != nil && descOut.KeyMetadata != nil {
		keyID = awssdk.ToString(descOut.KeyMetadata.KeyId)
		aliasExists = true
	} else {
		// Check if the error is the expected NotFoundException; treat other errors as fatal.
		var nfe *kmstypes.NotFoundException
		if descErr != nil && !errors.As(descErr, &nfe) {
			return fmt.Errorf("describe key by alias %s: %w", wantAlias, descErr)
		}
		// Stage 2 (fallback): orphan-key recovery via tag-based scan.
		recoveredID, err := scanOrphanedSecretsKey(ctx, client, resourcePrefix)
		if err != nil {
			return fmt.Errorf("scan for orphaned sandbox-secrets key: %w", err)
		}
		if recoveredID == "" {
			log.Info().Str("resource_prefix", resourcePrefix).Msg("no own sandbox-secrets alias or orphaned key — skipping KMS cleanup")
			return nil
		}
		keyID = recoveredID
		log.Warn().Str("resource_prefix", resourcePrefix).Str("key_id", keyID).Msg("recovered orphaned sandbox-secrets key via tag-based scan (alias was missing)")
	}

	// Delete the alias (only if it exists), then schedule the key for deletion.
	if aliasExists {
		if _, err := client.DeleteAlias(ctx, &kms.DeleteAliasInput{
			AliasName: awssdk.String(wantAlias),
		}); err != nil {
			return fmt.Errorf("delete alias %s: %w", wantAlias, err)
		}
	}
	pendingDays := int32(7)
	if _, err := client.ScheduleKeyDeletion(ctx, &kms.ScheduleKeyDeletionInput{
		KeyId:               awssdk.String(keyID),
		PendingWindowInDays: &pendingDays,
	}); err != nil {
		return fmt.Errorf("schedule key deletion %s: %w", keyID, err)
	}
	log.Info().Str("alias", wantAlias).Str("key_id", keyID).Int32("pending_days", pendingDays).Msg("scheduled sandbox-secrets KMS key deletion")
	return nil
}

// scanOrphanedSecretsKey paginates ListKeys + ListResourceTags looking for a key
// tagged with km:component=sandbox-secrets-key AND km:resource_prefix=${resourcePrefix}.
//
// Returns "" if zero matches.
// Returns the keyID if exactly one match.
// Returns "" and logs a warn if multiple matches (operator must intervene manually).
func scanOrphanedSecretsKey(ctx context.Context, client KMSAliasDeleter, resourcePrefix string) (string, error) {
	var marker *string
	var matches []string
	for {
		listOut, err := client.ListKeys(ctx, &kms.ListKeysInput{Marker: marker})
		if err != nil {
			return "", fmt.Errorf("list keys: %w", err)
		}
		for _, k := range listOut.Keys {
			id := awssdk.ToString(k.KeyId)
			tagsOut, err := client.ListResourceTags(ctx, &kms.ListResourceTagsInput{
				KeyId: awssdk.String(id),
			})
			if err != nil {
				// Skip keys we can't read tags on (might be AWS-managed or access-denied).
				log.Debug().Err(err).Str("key_id", id).Msg("skip key — cannot read tags")
				continue
			}
			hasComponent := false
			hasPrefix := false
			for _, t := range tagsOut.Tags {
				switch awssdk.ToString(t.TagKey) {
				case "km:component":
					hasComponent = awssdk.ToString(t.TagValue) == "sandbox-secrets-key"
				case "km:resource_prefix":
					hasPrefix = awssdk.ToString(t.TagValue) == resourcePrefix
				}
			}
			if hasComponent && hasPrefix {
				matches = append(matches, id)
			}
		}
		if !listOut.Truncated {
			break
		}
		marker = listOut.NextMarker
	}
	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		return matches[0], nil
	default:
		log.Warn().Strs("candidate_keys", matches).Str("resource_prefix", resourcePrefix).
			Msg("multiple orphaned sandbox-secrets keys match — refusing to auto-delete; operator must manually intervene")
		return "", nil
	}
}
