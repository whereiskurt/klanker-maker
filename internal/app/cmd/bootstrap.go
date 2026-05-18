package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
	"github.com/whereiskurt/klanker-maker/pkg/terragrunt"
	"github.com/whereiskurt/klanker-maker/pkg/terragrunt/planreport"
)

// BootstrapApplyTimeout bounds defaultApplyTerragrunt (Reconfigure + Apply).
// Matches the 10-minute bound used for the foundation ses-shared-rule-set
// regional module in init.go's defaultModuleTimeout (Plan 84.1-02 Task 2).
//
// Plan-checker rev 1 H6: without this bound, km bootstrap --shared-ses can
// hang indefinitely on a wedged terragrunt — the same indefinite-hang surface
// GAP-4 / GAP-5 closed in km init.
//
// Exported as a package-level var (not a const) so tests can lower the bound
// for fast-running fake-terragrunt scenarios.
var BootstrapApplyTimeout = 10 * time.Minute

// =============================================================================
// Phase 84: km bootstrap --shared-ses
// =============================================================================

// SESIdentityLister abstracts the two SES read operations needed for shared-SES
// auto-detection. The real *realSESLister satisfies this interface in production;
// tests inject a mock.
type SESIdentityLister interface {
	// ListReceiptRuleSets returns the list of SES classic v1 receipt rule sets.
	ListReceiptRuleSets(ctx context.Context, in *ses.ListReceiptRuleSetsInput, optFns ...func(*ses.Options)) (*ses.ListReceiptRuleSetsOutput, error)
	// ListEmailIdentities returns the list of SES v2 email identities.
	ListEmailIdentities(ctx context.Context, in *sesv2.ListEmailIdentitiesInput, optFns ...func(*sesv2.Options)) (*sesv2.ListEmailIdentitiesOutput, error)
}

// realSESLister adapts the production SES classic v1 and SES v2 clients to
// satisfy SESIdentityLister.
type realSESLister struct {
	sesClient   *ses.Client
	sesv2Client *sesv2.Client
}

func (r *realSESLister) ListReceiptRuleSets(ctx context.Context, in *ses.ListReceiptRuleSetsInput, optFns ...func(*ses.Options)) (*ses.ListReceiptRuleSetsOutput, error) {
	return r.sesClient.ListReceiptRuleSets(ctx, in, optFns...)
}

func (r *realSESLister) ListEmailIdentities(ctx context.Context, in *sesv2.ListEmailIdentitiesInput, optFns ...func(*sesv2.Options)) (*sesv2.ListEmailIdentitiesOutput, error) {
	return r.sesv2Client.ListEmailIdentities(ctx, in, optFns...)
}

// DetectSharedSESState checks whether the shared SES receipt rule set and the
// target email domain identity already exist.
// Exported for use in tests (cmd_test package).
//
// Returns:
//   - registerSharedRuleSet: true when the rule set does NOT exist yet (i.e. Terraform should create it)
//   - registerDomainIdentity: true when the domain identity does NOT exist yet (i.e. Terraform should create it)
func DetectSharedSESState(ctx context.Context, lister SESIdentityLister, ruleSetName, emailDomain string) (registerSharedRuleSet, registerDomainIdentity bool, err error) {
	return detectSharedSESState(ctx, lister, nil, ruleSetName, emailDomain)
}

// DetectSharedSESStateWithStateReader is the Phase 84.1 variant of
// DetectSharedSESState that also consults the foundation tfstate via a
// FoundationStateReader. When stateReader.StateOwns reports that a shared
// resource is already in foundation state, the corresponding register_* flag
// stays TRUE — keeping foundation in charge of the resource (the new "manage"
// semantic; GAP-2 closure).
//
// Pass nil for stateReader to fall back to the legacy AWS-reality check
// (used by defaultSESPreflight in init.go — the documented "skip state check"
// mode for read-only existence checks).
//
// Exported for use in tests (cmd_test package).
func DetectSharedSESStateWithStateReader(ctx context.Context, lister SESIdentityLister, stateReader FoundationStateReader, ruleSetName, emailDomain string) (registerSharedRuleSet, registerDomainIdentity bool, err error) {
	return detectSharedSESState(ctx, lister, stateReader, ruleSetName, emailDomain)
}

// FoundationStateReader returns true iff the named resource address is present
// in the foundation module's terraform state.
//
// Phase 84.1: implementations may read the state file from S3 (production)
// or from an in-memory map (tests). A nil-safe contract is enforced at the
// caller: detectSharedSESState skips the state check when the reader is nil.
//
// Resource addresses use Terraform's address syntax — for count=1 resources,
// always include the [0] suffix (e.g. "aws_ses_domain_identity.sandbox[0]").
type FoundationStateReader interface {
	// StateOwns reports whether resourceAddr is in foundation tfstate.
	// Returns (false, nil) when the state file does not exist (fresh account).
	// Returns (false, err) only on unexpected I/O errors — the caller treats
	// errors as "not owned" to avoid blocking init on transient S3 issues.
	StateOwns(ctx context.Context, resourceAddr string) (bool, error)
}

// =============================================================================
// Phase 84.4: DKIM/MX/TXT auto-import interfaces + helper
// =============================================================================
//
// These interfaces make autoImportFoundationSESRecords mockable in unit tests.
// The real types (*ses.Client, *route53.Client, FoundationStateReader,
// *terragrunt.Runner) satisfy them structurally — no wrapper code needed.

// sesDkimGetter is the SES v1 subset needed to fetch DKIM tokens for a domain.
type sesDkimGetter interface {
	GetIdentityDkimAttributes(ctx context.Context, in *ses.GetIdentityDkimAttributesInput, opts ...func(*ses.Options)) (*ses.GetIdentityDkimAttributesOutput, error)
}

// route53RecordLister is the Route53 subset needed to enumerate records in a
// hosted zone. (Named route53RecordLister to avoid collision with unbootstrap.go's
// UnbootstrapRoute53API which has a broader surface.)
type route53RecordLister interface {
	ListResourceRecordSets(ctx context.Context, in *route53.ListResourceRecordSetsInput, opts ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error)
}

// resourceImporter is the terragrunt.Runner subset needed to import resources.
// Satisfied by *terragrunt.Runner (Plan 00 added this method).
type resourceImporter interface {
	Import(ctx context.Context, dir, address, id string) error
}

// autoImportFoundationSESRecords imports any pre-existing Route53 DKIM/MX/TXT
// records that exist in AWS but are missing from foundation tfstate.
// Idempotent: skips records already owned by state.
// Returns nil if there is nothing to import (state is clean OR records don't
// exist in AWS yet).
//
// Called from runBootstrapSharedSES gated on !registerDomainIdentity (i.e. only
// when the domain identity already exists in AWS from a prior install — if the
// domain doesn't exist, apply creates everything fresh and there is nothing to
// import).
//
// RESEARCH.md Pattern 5 (Phase 84.4): import must happen BEFORE apply.
// Import ID formats:
//
//	DKIM CNAME: <zone>_<token>._domainkey.<domain>_CNAME
//	MX:         <zone>_<domain>_MX
//	TXT:        <zone>__amazonses.<domain>_TXT  (DOUBLE underscore)
func autoImportFoundationSESRecords(
	ctx context.Context,
	runner resourceImporter,
	sesDir string,
	stateReader FoundationStateReader,
	sesClient sesDkimGetter,
	r53Client route53RecordLister,
	emailDomain string,
	hostedZoneID string,
) error {
	// 1. Fetch DKIM tokens for the domain.
	dkimOut, err := sesClient.GetIdentityDkimAttributes(ctx, &ses.GetIdentityDkimAttributesInput{
		Identities: []string{emailDomain},
	})
	if err != nil {
		return fmt.Errorf("get DKIM attributes for %s: %w", emailDomain, err)
	}
	attrs, ok := dkimOut.DkimAttributes[emailDomain]
	if !ok || len(attrs.DkimTokens) == 0 {
		// Domain has no DKIM tokens yet — let apply create them.
		fmt.Fprintf(os.Stderr, "warning: domain %s has no DKIM tokens yet — apply will create them\n", emailDomain)
		return nil
	}

	// 2. Import each DKIM CNAME if not already in state.
	// Foundation module declares count = 3; cap at 3.
	limit := len(attrs.DkimTokens)
	if limit > 3 {
		limit = 3
	}
	for i := 0; i < limit; i++ {
		token := attrs.DkimTokens[i]
		addr := fmt.Sprintf("aws_route53_record.dkim[%d]", i)
		owned, err := stateReader.StateOwns(ctx, addr)
		if err != nil {
			return fmt.Errorf("state check %s: %w", addr, err)
		}
		if owned {
			fmt.Fprintf(os.Stderr, "  DKIM[%d] already in state, skipping import\n", i)
			continue
		}
		id := fmt.Sprintf("%s_%s._domainkey.%s_CNAME", hostedZoneID, token, emailDomain)
		fmt.Fprintf(os.Stderr, "  importing %s id=%s\n", addr, id)
		if err := runner.Import(ctx, sesDir, addr, id); err != nil {
			return fmt.Errorf("import %s: %w", addr, err)
		}
	}

	// 3. Conditionally import MX and _amazonses TXT — check Route53 first.
	records, err := r53Client.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
		HostedZoneId:    aws.String(hostedZoneID),
		StartRecordName: aws.String(emailDomain),
	})
	if err != nil {
		return fmt.Errorf("list Route53 records for zone %s: %w", hostedZoneID, err)
	}

	var foundMX, foundTXT bool
	for _, rec := range records.ResourceRecordSets {
		name := strings.TrimSuffix(aws.ToString(rec.Name), ".")
		if rec.Type == r53types.RRTypeMx && name == emailDomain {
			foundMX = true
		}
		if rec.Type == r53types.RRTypeTxt && name == "_amazonses."+emailDomain {
			foundTXT = true
		}
	}

	if foundMX {
		addr := "aws_route53_record.mx[0]"
		owned, _ := stateReader.StateOwns(ctx, addr)
		if !owned {
			id := fmt.Sprintf("%s_%s_MX", hostedZoneID, emailDomain)
			fmt.Fprintf(os.Stderr, "  importing %s id=%s\n", addr, id)
			if err := runner.Import(ctx, sesDir, addr, id); err != nil {
				return fmt.Errorf("import MX: %w", err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "  MX already in state, skipping import\n")
		}
	}

	if foundTXT {
		addr := "aws_route53_record.ses_verification[0]"
		owned, _ := stateReader.StateOwns(ctx, addr)
		if !owned {
			// DOUBLE underscore: record name "_amazonses.<domain>" gives
			// import ID "<zone>__amazonses.<domain>_TXT".
			id := fmt.Sprintf("%s__amazonses.%s_TXT", hostedZoneID, emailDomain)
			fmt.Fprintf(os.Stderr, "  importing %s id=%s\n", addr, id)
			if err := runner.Import(ctx, sesDir, addr, id); err != nil {
				return fmt.Errorf("import TXT: %w", err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "  _amazonses TXT already in state, skipping import\n")
		}
	}

	return nil
}

// detectSharedSESState is the unexported implementation called by
// DetectSharedSESState, DetectSharedSESStateWithStateReader, and runBootstrapSharedSES.
//
// Phase 84.1 Task 1 GREEN: signature extended with FoundationStateReader.
// When stateReader is non-nil and reports state ownership for a shared
// resource, the corresponding register_* flag stays TRUE — the new "manage
// this resource" semantic (GAP-2 closure). When stateReader is nil OR
// reports no ownership, fall back to the AWS-reality check, but with the
// new semantic: even AWS-present-not-in-state stays TRUE so foundation can
// import + manage the resource on the next apply (GAP-3 closure; relies on
// Task 2's import {} blocks to actually bring the resource into state).
//
// The semantic shift: register_* flags now mean "this module manages the
// resource", NOT "create only on first apply". The only time a flag goes
// false is a deliberate operator override (e.g. KM_REGISTER_SHARED_RULESET=false
// on a sibling install in a multi-install account where the foundation
// module should be a no-op for that install).
//
// nil-state-reader mode preserves the pre-84.1 read-only AWS-existence
// semantic — used by defaultSESPreflight in init.go for the "is the rule set
// here yet?" check that gates km init.
func detectSharedSESState(ctx context.Context, lister SESIdentityLister, stateReader FoundationStateReader, ruleSetName, emailDomain string) (registerSharedRuleSet, registerDomainIdentity bool, err error) {
	// Default: create both (safe idempotent starting point).
	registerSharedRuleSet = true
	registerDomainIdentity = true

	// Phase 84.1: prefer foundation-state ownership over AWS reality.
	// If foundation already manages the resource, the register flag stays TRUE
	// (means "keep managing"). When state ownership is known, we skip the
	// AWS-reality consultation entirely — that's the whole point of the change.
	var stateOwnsRuleSet, stateOwnsIdentity bool
	if stateReader != nil {
		stateOwnsRuleSet, _ = stateReader.StateOwns(ctx, "aws_ses_receipt_rule_set.shared[0]")
		stateOwnsIdentity, _ = stateReader.StateOwns(ctx, "aws_ses_domain_identity.sandbox[0]")
		// Errors are treated as "not owned" — the caller falls back to AWS
		// reality, which is the same behaviour as a missing state file
		// (fresh account). This avoids blocking bootstrap on a transient S3
		// blip.
	}

	if stateOwnsRuleSet {
		// Foundation owns it — keep managing (register=TRUE).
		registerSharedRuleSet = true
	} else {
		// Fall through to AWS-reality check.
		rsOut, listErr := lister.ListReceiptRuleSets(ctx, &ses.ListReceiptRuleSetsInput{})
		if listErr != nil {
			return registerSharedRuleSet, registerDomainIdentity, fmt.Errorf("ListReceiptRuleSets: %w", listErr)
		}
		ruleSetExistsInAWS := false
		for _, rs := range rsOut.RuleSets {
			if aws.ToString(rs.Name) == ruleSetName {
				ruleSetExistsInAWS = true
				break
			}
		}
		if stateReader != nil {
			// Phase 84.1 semantic: when a state reader is in play (production
			// bootstrap path), AWS-present-not-in-state still keeps the flag
			// TRUE so foundation imports + manages the resource on next apply
			// (GAP-3 closure). Task 2's import {} blocks make this safe.
			registerSharedRuleSet = true
			_ = ruleSetExistsInAWS // intentionally ignored under the new semantic
		} else {
			// nil-state-reader mode: legacy read-only behaviour preserved for
			// defaultSESPreflight in init.go — "rule set absent in AWS"
			// returns registerRS=true, which the preflight surfaces as the
			// actionable "run km bootstrap --shared-ses first" error.
			registerSharedRuleSet = !ruleSetExistsInAWS
		}
	}

	if stateOwnsIdentity {
		// Foundation owns it — keep managing (register=TRUE).
		registerDomainIdentity = true
	} else {
		// Fall through to AWS-reality check.
		idOut, listErr := lister.ListEmailIdentities(ctx, &sesv2.ListEmailIdentitiesInput{})
		if listErr != nil {
			return registerSharedRuleSet, registerDomainIdentity, fmt.Errorf("ListEmailIdentities: %w", listErr)
		}
		identityExistsInAWS := false
		for _, id := range idOut.EmailIdentities {
			if id.IdentityType == sesv2types.IdentityTypeDomain && aws.ToString(id.IdentityName) == emailDomain {
				identityExistsInAWS = true
				break
			}
		}
		if stateReader != nil {
			// Same semantic shift as above (GAP-3): import + manage.
			registerDomainIdentity = true
			_ = identityExistsInAWS
		} else {
			// nil-state-reader mode: legacy behaviour for defaultSESPreflight.
			registerDomainIdentity = !identityExistsInAWS
		}
	}

	return registerSharedRuleSet, registerDomainIdentity, nil
}

// s3FoundationStateReader is the production FoundationStateReader implementation.
// It downloads the foundation tfstate from S3 (key derived from the per-install
// resource_prefix + region_label) and checks whether resourceAddr is in the
// resources[] array.
//
// State file location follows the site.hcl backend convention:
//   s3://tf-{prefix}-state-{regionLabel}/tf-{prefix}/{regionLabel}/ses-shared-rule-set/terraform.tfstate
//
// A missing state file returns (false, nil) — fresh-account semantics. This
// matches the FoundationStateReader contract.
type s3FoundationStateReader struct {
	s3Client *s3.Client
	bucket   string
	key      string
}

// StateOwns downloads the foundation tfstate and reports whether resourceAddr
// is present in the resources[] array. The resourceAddr format is the
// Terraform address (e.g. "aws_ses_domain_identity.sandbox[0]").
//
// The Terraform state file is JSON with a top-level "resources" array, each
// entry has "mode", "type", "name", and (for count/for_each) "instances" with
// "index_key". We reconstruct the address as "type.name" or "type.name[index_key]"
// and compare.
func (r *s3FoundationStateReader) StateOwns(ctx context.Context, resourceAddr string) (bool, error) {
	resp, err := r.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(r.key),
	})
	if err != nil {
		// Missing state file (or no access yet) → fresh-account semantics.
		// We intentionally swallow the error rather than returning it; callers
		// of FoundationStateReader treat both nil-error and false-result as
		// "not owned" and fall back to AWS-reality.
		return false, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("read tfstate body: %w", err)
	}

	var state struct {
		Resources []struct {
			Mode      string `json:"mode"`
			Type      string `json:"type"`
			Name      string `json:"name"`
			Instances []struct {
				IndexKey interface{} `json:"index_key,omitempty"`
			} `json:"instances"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return false, fmt.Errorf("parse tfstate: %w", err)
	}

	for _, res := range state.Resources {
		if res.Mode != "managed" {
			continue
		}
		base := fmt.Sprintf("%s.%s", res.Type, res.Name)
		// Count=0 resources have no instances → no addresses to match.
		if len(res.Instances) == 0 {
			if base == resourceAddr {
				return true, nil
			}
			continue
		}
		for _, inst := range res.Instances {
			var addr string
			switch v := inst.IndexKey.(type) {
			case nil:
				addr = base
			case float64:
				// JSON numbers decode as float64; integer count indices.
				addr = fmt.Sprintf("%s[%d]", base, int(v))
			case string:
				addr = fmt.Sprintf("%s[%q]", base, v)
			default:
				addr = base
			}
			if addr == resourceAddr {
				return true, nil
			}
		}
	}
	return false, nil
}

// foundationStateBucket derives the S3 bucket name for the foundation tfstate
// using the site.hcl convention: tf-{prefix}-state-{regionLabel}.
func foundationStateBucket(cfg *config.Config) string {
	return fmt.Sprintf("tf-%s-state-%s", cfg.GetResourcePrefix(), cfg.GetRegionLabel())
}

// foundationStateKey derives the S3 key for the foundation tfstate using the
// site.hcl convention: tf-{prefix}/{regionLabel}/ses-shared-rule-set/terraform.tfstate.
func foundationStateKey(cfg *config.Config) string {
	return fmt.Sprintf("tf-%s/%s/ses-shared-rule-set/terraform.tfstate", cfg.GetResourcePrefix(), cfg.GetRegionLabel())
}

// RunBootstrapSharedSES is the exported test seam for runBootstrapSharedSES.
// Tests in the cmd_test package call this to exercise the env-var export +
// shared-SES detection without going through the cobra command (which has no
// hook for injecting an SESIdentityLister mock).
//
// Production code uses the unexported runBootstrapSharedSES via the cobra
// command's RunE — this wrapper is intentionally a one-line forwarder.
func RunBootstrapSharedSES(ctx context.Context, cfg *config.Config, dryRun bool, w io.Writer, listerOverride SESIdentityLister) error {
	return runBootstrapSharedSES(ctx, cfg, dryRun, w, listerOverride)
}

// runBootstrapSharedSES implements the `km bootstrap --shared-ses` workflow.
// It auto-detects whether the shared SES rule set and domain identity exist,
// sets the corresponding Terragrunt env vars, and applies
// infra/live/use1/ses-shared-rule-set/ via ApplyTerragruntFunc (or plans it
// when dryRun is true).
func runBootstrapSharedSES(ctx context.Context, cfg *config.Config, dryRun bool, w io.Writer, listerOverride SESIdentityLister) error {
	loadedCfg, err := loadBootstrapConfig(cfg)
	if err != nil {
		return err
	}

	// Ensure all config env vars are exported so Terragrunt site.hcl picks them up.
	ExportTerragruntEnvVars(loadedCfg)

	// Build the full email domain: {email_subdomain}.{domain}
	emailSubdomain := loadedCfg.EmailSubdomain
	if emailSubdomain == "" {
		emailSubdomain = "sandboxes"
	}
	emailDomain := fmt.Sprintf("%s.%s", emailSubdomain, loadedCfg.Domain)
	sesDir := filepath.Join(findRepoRoot(), "infra", "live", "use1", "ses-shared-rule-set")

	// Build the SES client pair (or use the override in tests).
	// Phase 84.1 GREEN: also construct an s3FoundationStateReader so the
	// auto-detect prefers state ownership over AWS reality (GAP-2 / GAP-3).
	// In tests the listerOverride path skips the state reader entirely (nil),
	// matching the legacy AWS-reality behaviour the existing test suite locks in.
	//
	// Phase 84.4: also stash sesv1Client and r53ImportClient for the DKIM/MX/TXT
	// auto-import path (used only when domain identity already exists in AWS).
	var lister SESIdentityLister
	var stateReader FoundationStateReader
	var sesv1Client sesDkimGetter      // Phase 84.4: SES v1 GetIdentityDkimAttributes
	var r53ImportClient route53RecordLister // Phase 84.4: Route53 ListResourceRecordSets
	if listerOverride != nil {
		lister = listerOverride
		// stateReader, sesv1Client, r53ImportClient stay nil — tests inject their
		// own via DetectSharedSESStateWithStateReader when state ownership is the
		// subject under test. The auto-import path is exercised directly via
		// TestRunBootstrapSharedSESAutoImport (bootstrap_dkim_import_test.go).
	} else {
		region := loadedCfg.PrimaryRegion
		if region == "" {
			region = "us-east-1"
		}
		awsCfg, err := awspkg.LoadAWSConfigInRegion(ctx, "klanker-terraform", region)
		if err != nil {
			// Dry-run tolerates missing AWS — operators (and unit tests) without
			// creds still get the deterministic "would apply" output. With creds,
			// auto-detect runs as usual and emits the richer KM_REGISTER_* lines.
			if dryRun {
				fmt.Fprintf(w, "Dry run — would run: terragrunt apply %s\n", sesDir)
				fmt.Fprintln(w, "  (SES auto-detect skipped: AWS config unavailable; KM_REGISTER_* env vars will be computed at apply time)")
				return nil
			}
			return fmt.Errorf("load AWS config: %w", err)
		}
		realSES := ses.NewFromConfig(awsCfg)
		lister = &realSESLister{
			sesClient:   realSES,
			sesv2Client: sesv2.NewFromConfig(awsCfg),
		}
		stateReader = &s3FoundationStateReader{
			s3Client: s3.NewFromConfig(awsCfg),
			bucket:   foundationStateBucket(loadedCfg),
			key:      foundationStateKey(loadedCfg),
		}
		// Phase 84.4: stash for DKIM/MX/TXT auto-import below.
		sesv1Client = realSES
		// Route53 is global; the klanker-terraform profile has cross-account
		// access to the dns_parent hosted zone.
		r53ImportClient = route53.NewFromConfig(awsCfg)
	}

	registerRS, registerID, err := detectSharedSESState(ctx, lister, stateReader, "sandbox-email-shared", emailDomain)
	if err != nil {
		// Dry-run tolerates auto-detect failure — print the apply intent and exit.
		// Apply-mode propagates the error so the operator sees the underlying cause.
		if dryRun {
			fmt.Fprintf(w, "Dry run — would run: terragrunt apply %s\n", sesDir)
			fmt.Fprintf(w, "  (SES auto-detect failed: %v; KM_REGISTER_* env vars will be computed at apply time)\n", err)
			return nil
		}
		return fmt.Errorf("SES auto-detect: %w", err)
	}

	// Log step-level summaries (OPER-01 pattern).
	if registerRS {
		fmt.Fprintln(w, "Shared SES rule set:      creating")
	} else {
		fmt.Fprintln(w, "Shared SES rule set:      reusing existing")
	}
	if registerID {
		fmt.Fprintln(w, "Shared SES domain identity: creating")
	} else {
		fmt.Fprintln(w, "Shared SES domain identity: reusing existing")
	}

	// Export the two Phase-84-specific vars.
	os.Setenv("KM_REGISTER_SHARED_RULESET", strconv.FormatBool(registerRS))
	os.Setenv("KM_REGISTER_DOMAIN_IDENTITY", strconv.FormatBool(registerID))

	if dryRun {
		fmt.Fprintf(w, "Dry run — would run: terragrunt apply %s\n", sesDir)
		fmt.Fprintf(w, "  KM_REGISTER_SHARED_RULESET=%s\n", os.Getenv("KM_REGISTER_SHARED_RULESET"))
		fmt.Fprintf(w, "  KM_REGISTER_DOMAIN_IDENTITY=%s\n", os.Getenv("KM_REGISTER_DOMAIN_IDENTITY"))
		return nil
	}

	// Phase 84.4: when the domain identity already exists in AWS (second install
	// in a shared-domain account), auto-import DKIM/MX/TXT records that exist in
	// Route53 but are not yet in this install's foundation tfstate. This prevents
	// "resource already exists" errors during apply.
	//
	// Gate conditions:
	//   !registerID — detectSharedSESState found domain identity in AWS already
	//   sesv1Client != nil — real production path (not listerOverride test path)
	//   r53ImportClient != nil — Route53 client was constructed (production path)
	//   stateReader != nil — s3FoundationStateReader was constructed (production path)
	if !registerID && sesv1Client != nil && r53ImportClient != nil && stateReader != nil {
		hostedZoneID := loadedCfg.Route53ZoneID
		if hostedZoneID == "" {
			fmt.Fprintln(w, "WARN: route53_zone_id not set in km-config.yaml — skipping DKIM/MX/TXT auto-import")
		} else {
			runner := terragrunt.NewRunner("klanker-terraform", findRepoRoot())
			fmt.Fprintln(w, "Auto-importing pre-existing DKIM/MX/TXT Route53 records...")
			if err := autoImportFoundationSESRecords(ctx, runner, sesDir, stateReader, sesv1Client, r53ImportClient, emailDomain, hostedZoneID); err != nil {
				return fmt.Errorf("auto-import SES Route53 records: %w", err)
			}
		}
	}

	fmt.Fprintln(w, "Applying ses-shared-rule-set...")
	if err := ApplyTerragruntFunc(ctx, sesDir); err != nil {
		return fmt.Errorf("ses-shared-rule-set apply: %w", err)
	}
	fmt.Fprintln(w, "ses-shared-rule-set applied.")
	return nil
}

// SCPStatement represents a single statement in an SCP policy document.
// Exported so tests can inspect individual statement fields without AWS access.
type SCPStatement struct {
	Sid       string      `json:"Sid"`
	Effect    string      `json:"Effect"`
	Action    []string    `json:"Action,omitempty"`
	NotAction []string    `json:"NotAction,omitempty"`
	Resource  string      `json:"Resource"`
	Condition interface{} `json:"Condition,omitempty"`
}

// SCPPolicyDoc is the top-level SCP policy document structure.
// Exported so tests can inspect the full policy without AWS access.
type SCPPolicyDoc struct {
	Version   string         `json:"Version"`
	Statement []SCPStatement `json:"Statement"`
}

// BuildSCPPolicy returns the SCP policy document for the application account
// given the resolved trusted-principal sets. Pure (no AWS calls); tested directly.
func BuildSCPPolicy(trustedBase, trustedInstance, trustedIAM, trustedSSM []string, region string) SCPPolicyDoc {
	arnNotLike := func(arns []string) interface{} {
		return map[string]interface{}{
			"ArnNotLike": map[string]interface{}{
				"aws:PrincipalARN": arns,
			},
		}
	}

	return SCPPolicyDoc{
		Version: "2012-10-17",
		Statement: []SCPStatement{
			{
				Sid:    "DenyInfraAndStorage",
				Effect: "Deny",
				Action: []string{
					"ec2:CreateSecurityGroup", "ec2:DeleteSecurityGroup",
					"ec2:AuthorizeSecurityGroup*", "ec2:RevokeSecurityGroup*",
					"ec2:ModifySecurityGroupRules",
					"ec2:CreateVpc", "ec2:CreateSubnet", "ec2:CreateRouteTable",
					"ec2:CreateRoute", "ec2:*InternetGateway", "ec2:CreateNatGateway",
					"ec2:*VpcPeeringConnection", "ec2:CreateTransitGateway*",
					"ec2:CreateSnapshot", "ec2:CopySnapshot", "ec2:DeleteSnapshot",
					// AMI / EBS snapshot lifecycle (Phase 56): trusted-base principals (operator,
					// km-provisioner, km-lifecycle) may bake, copy, deregister, and clean up.
					// Describe* ops are read-only and intentionally NOT denied here — they remain
					// implicitly allowed for inspection. NOTE: SCP exemption alone does not grant
					// permission — operator IAM allow policy must affirmatively include these ops.
					// See WriteOperatorIAMGuidance() output for operator-side requirements.
					"ec2:CreateImage", "ec2:CopyImage", "ec2:ExportImage", "ec2:DeregisterImage",
					"ec2:CreateTags",
				},
				Resource:  "*",
				Condition: arnNotLike(trustedBase),
			},
			{
				Sid:    "DenyInstanceMutation",
				Effect: "Deny",
				Action: []string{
					"ec2:RunInstances", "ec2:ModifyInstanceAttribute",
					"ec2:ModifyInstanceMetadataOptions",
				},
				Resource:  "*",
				Condition: arnNotLike(trustedInstance),
			},
			{
				Sid:    "DenyIAMEscalation",
				Effect: "Deny",
				Action: []string{
					"iam:CreateRole", "iam:AttachRolePolicy", "iam:DetachRolePolicy",
					"iam:PassRole", "iam:AssumeRole",
				},
				Resource:  "*",
				Condition: arnNotLike(trustedIAM),
			},
			{
				Sid:      "DenySSMPivot",
				Effect:   "Deny",
				Action:   []string{"ssm:SendCommand", "ssm:StartSession"},
				Resource: "*",
				Condition: arnNotLike(trustedSSM),
			},
			{
				Sid:    "DenyOrgDiscovery",
				Effect: "Deny",
				Action: []string{"organizations:List*", "organizations:Describe*"},
				Resource: "*",
			},
			{
				Sid:    "DenyOutsideRegion",
				Effect: "Deny",
				NotAction: []string{
					"iam:*", "sts:*", "organizations:*", "support:*", "health:*",
					"trustedadvisor:*", "cloudfront:*", "waf:*", "shield:*",
					"route53:*", "route53domains:*", "budgets:*", "ce:*", "cur:*",
					"globalaccelerator:*", "networkmanager:*", "pricing:*", "bedrock:*",
					"s3:GetAccountPublicAccessBlock", "s3:ListAllMyBuckets",
					"s3:PutAccountPublicAccessBlock",
				},
				Resource: "*",
				Condition: map[string]interface{}{
					"StringNotEquals": map[string]interface{}{
						"aws:RequestedRegion": []string{region},
					},
					"ArnNotLike": map[string]interface{}{
						"aws:PrincipalArn": trustedBase,
					},
				},
			},
		},
	}
}

// BuildSCPPolicyFromPrefix is a convenience wrapper introduced in Phase 84.4.
// It computes the trusted ARN sets from resourcePrefix and applicationAccountID,
// then delegates to BuildSCPPolicy. The ARN set structure mirrors
// infra/modules/scp/v2.0.0/main.tf locals (trusted_arns_*) with var.trusted_role_arns
// at its default (SSO wildcard only). This keeps the JSON well within the 5,000-byte
// threshold even for the maximum 12-char prefix (e.g. "whereiskurt").
//
// Returns the policy as a JSON string (indented) for display and size checks.
// The 5,000-byte safety threshold (matching the HCL precondition) is NOT enforced
// here — callers should check len(result) <= 5000 if needed.
//
// Backward compat: prefix "km" renders the same five canonical role-name patterns
// (km-ecs-spot-handler, km-budget-enforcer-*, km-ec2spot-ssm-*,
// km-github-token-refresher-*, km-ttl-handler) as the pre-84.4 hardcoded output.
func BuildSCPPolicyFromPrefix(resourcePrefix, applicationAccountID, allowedRegion string) string {
	// trustedBase mirrors trusted_arns_base = var.trusted_role_arns (default: SSO only).
	// Keeping base minimal is critical for staying under the 5,000-byte limit with long prefixes.
	trustedBase := []string{
		"arn:aws:iam::*:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*",
	}
	// trustedInstance mirrors trusted_arns_instance: base + {prefix}-ecs-spot-handler.
	trustedInstance := append(append([]string{}, trustedBase...),
		fmt.Sprintf("arn:aws:iam::%s:role/%s-ecs-spot-handler", applicationAccountID, resourcePrefix))
	// trustedIAM mirrors trusted_arns_iam: base + {prefix}-budget-enforcer-*.
	trustedIAM := append(append([]string{}, trustedBase...),
		fmt.Sprintf("arn:aws:iam::%s:role/%s-budget-enforcer-*", applicationAccountID, resourcePrefix))
	// trustedSSM mirrors trusted_arns_ssm: narrower set — only SSM-specific roles + SSO.
	trustedSSM := []string{
		fmt.Sprintf("arn:aws:iam::%s:role/%s-ec2spot-ssm-*", applicationAccountID, resourcePrefix),
		fmt.Sprintf("arn:aws:iam::%s:role/%s-github-token-refresher-*", applicationAccountID, resourcePrefix),
		fmt.Sprintf("arn:aws:iam::%s:role/%s-ttl-handler", applicationAccountID, resourcePrefix),
		"arn:aws:iam::*:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*",
	}

	policy := BuildSCPPolicy(trustedBase, trustedInstance, trustedIAM, trustedSSM, allowedRegion)
	policyJSON, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return fmt.Sprintf("error marshaling SCP policy: %v", err)
	}
	return string(policyJSON)
}

// WriteOperatorIAMGuidance writes the Phase 56 AMI-lifecycle positive-allow
// requirements block to w. Documents read-only and mutating ops the operator
// role must have in its IAM allow policy (independent of the SCP exemption).
// Exported so tests can verify the guidance text without invoking runShowSCP.
func WriteOperatorIAMGuidance(w io.Writer) {
	fmt.Fprintln(w, "# ============================================================")
	fmt.Fprintln(w, "# Operator IAM Positive-Allow Requirements (Phase 56 AMI Lifecycle)")
	fmt.Fprintln(w, "# ============================================================")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "# The SCP above (DenyInfraAndStorage) un-blocks the following AMI-lifecycle")
	fmt.Fprintln(w, "# operations for trusted-base principals via ArnNotLike exemption:")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "#   ec2:CreateImage, ec2:CopyImage, ec2:ExportImage,")
	fmt.Fprintln(w, "#   ec2:DeregisterImage, ec2:DeleteSnapshot, ec2:CreateTags")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "# IMPORTANT: Un-blocking via SCP is NOT the same as granting permission.")
	fmt.Fprintln(w, "# The operator's SSO permission set (or the klanker-terraform role's inline")
	fmt.Fprintln(w, "# policy) must AFFIRMATIVELY ALLOW these actions in addition to the SCP")
	fmt.Fprintln(w, "# exemption.")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "# Required AMI-lifecycle permissions for the operator role:")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "#   Mutating (also exempted in SCP above):")
	fmt.Fprintln(w, "#     - ec2:CreateImage")
	fmt.Fprintln(w, "#     - ec2:CopyImage")
	fmt.Fprintln(w, "#     - ec2:DeregisterImage")
	fmt.Fprintln(w, "#     - ec2:DeleteSnapshot")
	fmt.Fprintln(w, "#     - ec2:CreateTags")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "#   Read-only (NOT in SCP — must be in IAM allow policy):")
	fmt.Fprintln(w, "#     - ec2:DescribeImages       (km ami list, km doctor stale-AMI check)")
	fmt.Fprintln(w, "#     - ec2:DescribeSnapshots    (km ami list --wide for snapshot count)")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "# Example IAM policy statement to add to the operator's SSO permission set")
	fmt.Fprintln(w, "# or the klanker-terraform role inline policy:")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "# {")
	fmt.Fprintln(w, "#   \"Version\": \"2012-10-17\",")
	fmt.Fprintln(w, "#   \"Statement\": [")
	fmt.Fprintln(w, "#     {")
	fmt.Fprintln(w, "#       \"Sid\": \"KMAMILifecycle\",")
	fmt.Fprintln(w, "#       \"Effect\": \"Allow\",")
	fmt.Fprintln(w, "#       \"Action\": [")
	fmt.Fprintln(w, "#         \"ec2:CreateImage\",")
	fmt.Fprintln(w, "#         \"ec2:CopyImage\",")
	fmt.Fprintln(w, "#         \"ec2:DeregisterImage\",")
	fmt.Fprintln(w, "#         \"ec2:DeleteSnapshot\",")
	fmt.Fprintln(w, "#         \"ec2:CreateTags\",")
	fmt.Fprintln(w, "#         \"ec2:DescribeImages\",")
	fmt.Fprintln(w, "#         \"ec2:DescribeSnapshots\"")
	fmt.Fprintln(w, "#       ],")
	fmt.Fprintln(w, "#       \"Resource\": \"*\"")
	fmt.Fprintln(w, "#     }")
	fmt.Fprintln(w, "#   ]")
	fmt.Fprintln(w, "# }")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "# Without these, `km ami list`, `km ami delete`, `km ami copy`, and")
	fmt.Fprintln(w, "# `km doctor` stale-AMI checks will fail with UnauthorizedOperation.")
	fmt.Fprintln(w, "# ============================================================")
}

// KMSEnsureAPI covers the KMS operations needed to create a key and alias.
// Allows test injection without real AWS calls.
type KMSEnsureAPI interface {
	DescribeKey(ctx context.Context, params *kms.DescribeKeyInput, optFns ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
	CreateKey(ctx context.Context, params *kms.CreateKeyInput, optFns ...func(*kms.Options)) (*kms.CreateKeyOutput, error)
	CreateAlias(ctx context.Context, params *kms.CreateAliasInput, optFns ...func(*kms.Options)) (*kms.CreateAliasOutput, error)
}

// TerragruntApplyFunc is the function signature for applying a Terragrunt unit.
// It is exported so external test packages can inject a fake without executing terragrunt.
type TerragruntApplyFunc func(ctx context.Context, dir string) error

// ApplyTerragruntFunc is the package-level apply function used by runBootstrap.
// Tests replace this variable to capture apply calls without executing terragrunt.
var ApplyTerragruntFunc TerragruntApplyFunc = defaultApplyTerragrunt

// RunBootstrapSharedSESPlanFunc is the package-level entry point for
// km bootstrap --shared-ses --plan. Exported as a var so cmd_test can override it
// with a mock to verify routing without real AWS credentials / terragrunt binary.
// The default implementation is runBootstrapSharedSESPlan (Plan 05 will flesh this out).
//
// Phase 84.2 test seam — mirrors RunInitPlanFunc (init.go) and ApplyTerragruntFunc above.
var RunBootstrapSharedSESPlanFunc = runBootstrapSharedSESPlan

// Phase 84.3 test seams — package-level vars so tests can intercept each subflow without real AWS.
// RunBootstrapFunc is the test seam for runBootstrap (foundation SCP + KMS + artifacts).
// RunBootstrapSharedSESFunc is the test seam for runBootstrapSharedSES (shared SES rule set).
// RunBootstrapAllFunc is the test seam for runBootstrapAll (chains both subflows in order).
var (
	RunBootstrapFunc          = runBootstrap
	RunBootstrapSharedSESFunc = func(ctx context.Context, cfg *config.Config, dryRun bool, w io.Writer, listerOverride SESIdentityLister) error {
		return runBootstrapSharedSES(ctx, cfg, dryRun, w, listerOverride)
	}
	RunBootstrapAllFunc = runBootstrapAll
)

// runBootstrapAll chains runBootstrap (foundation SCP + KMS + artifacts) then
// runBootstrapSharedSES (shared SES rule set), in that order.
//
// Order is intentional: foundation infra must exist before the SES rule set
// references it. On error in subflow 1, execution stops — subflow 2 is not run.
//
// Flag forwarding: dryRun, plan, and acceptDestroys are passed through to both
// subflows symmetrically so --all --plan and --all --dry-run=false work as
// expected. With plan=true, each subflow runs its own Phase 84.2 destroy-class
// gate — a gate trip in either subflow propagates back as an error.
func runBootstrapAll(ctx context.Context, cfg *config.Config, dryRun, plan, acceptDestroys bool, w io.Writer) error {
	fmt.Fprintln(w, "=== Bootstrap: SCP + KMS + artifacts ===")
	if plan {
		if err := RunBootstrapSharedSESPlanFunc(cfg, acceptDestroys); err != nil {
			return fmt.Errorf("runBootstrap plan (subflow 1) failed: %w", err)
		}
	} else {
		if err := RunBootstrapFunc(ctx, cfg, dryRun, w); err != nil {
			return fmt.Errorf("runBootstrap (subflow 1) failed: %w", err)
		}
	}
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "=== Bootstrap: shared SES rule set ===")
	if plan {
		if err := RunBootstrapSharedSESPlanFunc(cfg, acceptDestroys); err != nil {
			return fmt.Errorf("runBootstrapSharedSES plan (subflow 2) failed: %w", err)
		}
	} else {
		if err := RunBootstrapSharedSESFunc(ctx, cfg, dryRun, w, nil); err != nil {
			return fmt.Errorf("runBootstrapSharedSES (subflow 2) failed: %w", err)
		}
	}
	return nil
}

// runBootstrapSharedSESPlan is the production entry point for
// km bootstrap --shared-ses --plan. Single-module analog of runInitPlan (Plan 04):
// runs terragrunt plan against the foundation ses-shared-rule-set module and
// applies the destroy-class gate over the result.
//
// Phase 84.2 Decision 4 (bootstrap parity): Phase 84 Gaps 2, 3, 6 happened in
// the bootstrap path too. Symmetric coverage is cheap once the plumbing exists.
//
// Per CONTEXT.md decisions: --plan is independent of --dry-run; it NEVER applies.
// --i-accept-destroys clears the exit code from 1 to 0 but still prints trips.
func runBootstrapSharedSESPlan(cfg *config.Config, acceptDestroys bool) error {
	return runBootstrapSharedSESPlanWithWriter(cfg, os.Stdout, false, acceptDestroys)
}

// runBootstrapSharedSESPlanWithWriter is the writer-aware test seam for
// runBootstrapSharedSESPlan. Production callers use runBootstrapSharedSESPlan
// (which writes to os.Stdout); cmd_test uses this directly to capture output
// without real AWS / a real terragrunt binary.
//
// Note: plan output (fmt.Print* in planModule) goes to os.Stdout; the w
// parameter captures header/footer output. Trip/summary assertions in tests
// capture os.Stdout via pipe (same constraint as runInitPlanWithWriter).
//
// Phase 84.2 test seam — referenced by bootstrap_plan_test.go.
func runBootstrapSharedSESPlanWithWriter(cfg *config.Config, w io.Writer, verbose, acceptDestroys bool) error {
	ctx := context.Background()

	loadedCfg, err := loadBootstrapConfig(cfg)
	if err != nil {
		return err
	}

	// 1. Export env vars — Phase 84.1-01 contract (matches bootstrap.go:341).
	ExportTerragruntEnvVars(loadedCfg)

	// 2. Set up SES auto-detect — same path as runBootstrapSharedSES so the plan
	//    reflects the same register_* env vars the apply path would honor.
	emailSubdomain := loadedCfg.EmailSubdomain
	if emailSubdomain == "" {
		emailSubdomain = "sandboxes"
	}
	emailDomain := fmt.Sprintf("%s.%s", emailSubdomain, loadedCfg.Domain)

	var lister SESIdentityLister
	var stateReader FoundationStateReader
	region := loadedCfg.PrimaryRegion
	if region == "" {
		region = "us-east-1"
	}
	awsCfg, awsErr := awspkg.LoadAWSConfigInRegion(ctx, "klanker-terraform", region)
	if awsErr != nil {
		return fmt.Errorf("load AWS config: %w", awsErr)
	}
	lister = &realSESLister{
		sesClient:   ses.NewFromConfig(awsCfg),
		sesv2Client: sesv2.NewFromConfig(awsCfg),
	}
	stateReader = &s3FoundationStateReader{
		s3Client: s3.NewFromConfig(awsCfg),
		bucket:   foundationStateBucket(loadedCfg),
		key:      foundationStateKey(loadedCfg),
	}

	registerRS, registerID, err := detectSharedSESState(ctx, lister, stateReader, "sandbox-email-shared", emailDomain)
	if err != nil {
		return fmt.Errorf("SES auto-detect: %w", err)
	}
	// Set the same register_* env vars the apply path uses so the plan output
	// reflects what the actual apply would do.
	os.Setenv("KM_REGISTER_SHARED_RULESET", strconv.FormatBool(registerRS))
	os.Setenv("KM_REGISTER_DOMAIN_IDENTITY", strconv.FormatBool(registerID))

	// 3. Construct runner and resolve module dir.
	repoRoot := findRepoRoot()
	runner := terragrunt.NewRunner("klanker-terraform", repoRoot)
	runner.Verbose = false // capture stdout per-module; echo via verbose flag below

	regionLabel := compiler.RegionLabel(region)
	regionDir := filepath.Join(repoRoot, "infra", "live", regionLabel)
	if err := ensureRegionHCL(regionDir, regionLabel, region); err != nil {
		return err
	}
	sesDir := filepath.Join(regionDir, "ses-shared-rule-set")

	fmt.Fprintln(w, "km bootstrap --shared-ses --plan")
	fmt.Fprintln(w)

	m := regionalModule{
		name:    "ses-shared-rule-set",
		dir:     sesDir,
		envReqs: nil,
	}

	// 4. Run planModule (package-private helper from init.go — same package).
	report, planErr := planModule(ctx, runner, m, verbose)
	if planErr != nil {
		return fmt.Errorf("planning %s: %w", m.name, planErr)
	}

	// 5. Single-report gate (same algorithm as runInitPlan / RunInitPlanWithRunner).
	reports := []planreport.Report{report}
	result := planreport.Evaluate(reports, acceptDestroys)
	if result.Blocked {
		printTripBlock("km bootstrap --shared-ses --plan", result.Trips)
		return fmt.Errorf("destroy-class gate tripped (re-run with --i-accept-destroys to override)")
	}
	if len(result.Trips) > 0 {
		printTripBlock("km bootstrap --shared-ses --plan", result.Trips)
		fmt.Fprintln(w, "  (override active via --i-accept-destroys — exit 0; no apply will run)")
	}

	printAggregateSummary(reports)
	fmt.Fprintln(w, "Run 'km bootstrap --shared-ses' (without --plan) to apply.")
	return nil
}

// defaultApplyTerragrunt runs `terragrunt apply -auto-approve` on the given directory
// using the management-account AWS profile. Calls Reconfigure first to initialize the
// S3 backend on first apply of a new module (e.g. the Phase 84 ses-shared-rule-set
// module on an in-place upgrade) — terragrunt's auto-init does not fire when the
// backend config is new to this working tree.
//
// Phase 84.1-02 Task 3 (plan-checker rev 1 H6): Reconfigure + Apply are
// wrapped in a single BootstrapApplyTimeout (default 10min) — the same upper
// bound RunInitWithRunner uses for the regional ses-shared-rule-set module.
// Without this bound, a wedged terragrunt blocks km bootstrap forever,
// mirroring the original 84-10-UAT.md GAP-4/5 km init regression.
func defaultApplyTerragrunt(ctx context.Context, dir string) error {
	awsProfile := "klanker-terraform"
	repoRoot := findRepoRoot()
	runner := terragrunt.NewRunner(awsProfile, repoRoot)

	boundCtx, cancel := context.WithTimeout(ctx, BootstrapApplyTimeout)
	defer cancel()

	if err := runner.Reconfigure(boundCtx, dir); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("terragrunt init -reconfigure %s wedged after %s — see OPERATOR-GUIDE.md § Phase 84.1 state-digest recovery: %w", dir, BootstrapApplyTimeout, err)
		}
		return fmt.Errorf("terragrunt init -reconfigure: %w", err)
	}
	if err := runner.Apply(boundCtx, dir); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("terragrunt apply %s wedged after %s — kill orphan terragrunt PID (see heartbeat above) and consult OPERATOR-GUIDE.md § Phase 84.1 state-digest recovery: %w", dir, BootstrapApplyTimeout, err)
		}
		return err
	}
	return nil
}

// NewBootstrapCmd creates the "km bootstrap" command using os.Stdout for output.
func NewBootstrapCmd(cfg *config.Config) *cobra.Command {
	return NewBootstrapCmdWithWriter(cfg, os.Stdout)
}

// NewBootstrapCmdWithWriter creates the "km bootstrap" command writing output to w.
// Pass nil to use os.Stdout. Used in tests for output capture.
//
// bootstrap validates that km-config.yaml exists and is loadable, then
// (with --dry-run, the default) prints what infrastructure would be created.
// With --dry-run=false, it deploys the SCP containment policy to the management account.
func NewBootstrapCmdWithWriter(cfg *config.Config, w io.Writer) *cobra.Command {
	if w == nil {
		w = os.Stdout
	}

	var dryRun bool
	var showPrereqs bool
	var showSCP bool
	var sharedSES bool
	var plan bool
	var acceptDestroys bool
	var all bool

	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Validate config and show what infrastructure bootstrap would create",
		Long:  helpText("bootstrap"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if showSCP {
				return runShowSCP(cmd.Context(), cfg, w)
			}
			if showPrereqs {
				return runShowPrereqs(cmd.Context(), cfg, w)
			}
			// Phase 84.3: --all and --shared-ses are mutually exclusive.
			if all && sharedSES {
				return fmt.Errorf("--all and --shared-ses are mutually exclusive; --all runs both subflows in order")
			}
			// Phase 84.3: --all chains both subflows (foundation then shared SES).
			if all {
				return RunBootstrapAllFunc(cmd.Context(), cfg, dryRun, plan, acceptDestroys, cmd.OutOrStdout())
			}
			// Phase 84.2: --plan routes to RunBootstrapSharedSESPlanFunc (same seam pattern
			// as RunInitPlanFunc on init). Must come before --dry-run check.
			if sharedSES && plan {
				return RunBootstrapSharedSESPlanFunc(cfg, acceptDestroys)
			}
			if sharedSES {
				return RunBootstrapSharedSESFunc(cmd.Context(), cfg, dryRun, cmd.OutOrStdout(), nil)
			}
			return RunBootstrapFunc(cmd.Context(), cfg, dryRun, cmd.OutOrStdout())
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", true,
		"Print what would be created without making any changes (default: true)")
	cmd.Flags().BoolVar(&showPrereqs, "show-prereqs", false,
		"Print the IAM role and trust policy that must be created in the management account before bootstrap can deploy the SCP")
	cmd.Flags().BoolVar(&showSCP, "scp", false,
		"Print the km-sandbox-containment SCP policy JSON and the km-org-admin role/trust policy")
	cmd.Flags().BoolVar(&sharedSES, "shared-ses", false,
		"Provision the account-shared SES rule set + domain identity (Phase 84); run before km init on a fresh account")
	cmd.Flags().BoolVar(&plan, "plan", false,
		"Run terragrunt plan for bootstrap modules with destroy-class safety gate; never applies (Phase 84.2)")
	cmd.Flags().BoolVar(&acceptDestroys, "i-accept-destroys", false,
		"Clear --plan exit code from 1 to 0 when only failures are protected destroys (per-invocation; never persisted; does NOT auto-apply)")
	if err := cmd.Flags().MarkHidden("i-accept-destroys"); err != nil {
		panic(fmt.Sprintf("MarkHidden i-accept-destroys on bootstrap: %v", err))
	}
	cmd.Flags().BoolVar(&all, "all", false,
		"Chain bootstrap subflows in order: SCP/KMS/artifacts (foundation) then shared SES rule set (Phase 84.3)")

	return cmd
}

// findKMConfigPath locates km-config.yaml by checking (in order):
//  1. The current working directory
//  2. The repo root (as determined by findRepoRoot)
func findKMConfigPath() string {
	cwd, err := os.Getwd()
	if err == nil {
		candidate := filepath.Join(cwd, "km-config.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filepath.Join(findRepoRoot(), "km-config.yaml")
}

// runShowPrereqs prints the IAM role and trust policy needed in the organization account.
func runShowPrereqs(ctx context.Context, cfg *config.Config, w io.Writer) error {
	loadedCfg, err := loadBootstrapConfig(cfg)
	if err != nil {
		return err
	}

	if loadedCfg.OrganizationAccountID == "" {
		fmt.Fprintln(w, "accounts.organization not set — SCP deployment disabled.")
		fmt.Fprintln(w, "Set accounts.organization in km-config.yaml to enable org-level sandbox containment via Service Control Policies.")
		return nil
	}

	// Determine the caller identity for the trust policy
	callerAccount := loadedCfg.ApplicationAccountID
	if callerAccount == "" {
		callerAccount = loadedCfg.TerraformAccountID
	}
	if callerAccount == "" {
		callerAccount = "<APPLICATION_ACCOUNT_ID>"
	}

	orgAccount := loadedCfg.OrganizationAccountID
	roleName := "km-org-admin"

	fmt.Fprintln(w, "# Prerequisites for km bootstrap")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "The SCP deployment assumes a role `%s` in the organization account (%s).\n", roleName, orgAccount)
	fmt.Fprintf(w, "This role must be created manually before running `km bootstrap --dry-run=false`.\n")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Option 1: AWS CLI")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Run these commands while authenticated to the organization account (%s):\n", orgAccount)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "```bash")
	fmt.Fprintln(w, "# 1. Create the role with a trust policy allowing the application account to assume it")
	fmt.Fprintf(w, `aws iam create-role --role-name %s --assume-role-policy-document '{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::%s:root"
      },
      "Action": "sts:AssumeRole",
      "Condition": {
        "ArnLike": {
          "aws:PrincipalArn": [
            "arn:aws:iam::%s:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*",
            "arn:aws:iam::%s:role/km-provisioner-*"
          ]
        }
      }
    }
  ]
}'
`, roleName, callerAccount, callerAccount, callerAccount)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "# 2. Attach the Organizations policy permissions (least-privilege, three statements)")
	fmt.Fprintln(w, "#    NOTE: Replace <ORG_ID> below with your Organization ID (e.g., o-abc123xyz)")
	fmt.Fprintln(w, "#    Find it with: aws organizations describe-organization --query 'Organization.Id' --output text")
	fmt.Fprintf(w, `aws iam put-role-policy --role-name %s --policy-name km-org-admin-policy --policy-document '{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "SCPMutate",
      "Effect": "Allow",
      "Action": [
        "organizations:UpdatePolicy",
        "organizations:DeletePolicy",
        "organizations:DescribePolicy",
        "organizations:ListTargetsForPolicy",
        "organizations:TagResource",
        "organizations:UntagResource",
        "organizations:ListTagsForResource"
      ],
      "Resource": "arn:aws:organizations::%s:policy/*/service_control_policy/*"
    },
    {
      "Sid": "SCPAttachDetach",
      "Effect": "Allow",
      "Action": [
        "organizations:AttachPolicy",
        "organizations:DetachPolicy"
      ],
      "Resource": [
        "arn:aws:organizations::%s:policy/*/service_control_policy/*",
        "arn:aws:organizations::%s:account/*/%s"
      ]
    },
    {
      "Sid": "SCPCreateListDescribe",
      "Effect": "Allow",
      "Action": [
        "organizations:CreatePolicy",
        "organizations:ListPolicies",
        "organizations:ListPoliciesForTarget",
        "organizations:DescribeOrganization"
      ],
      "Resource": "*"
    }
  ]
}'
`, roleName, orgAccount, orgAccount, orgAccount, callerAccount)
	fmt.Fprintln(w, "```")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Option 2: CloudFormation")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Deploy this stack in the organization account (%s):\n", orgAccount)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "```yaml")
	fmt.Fprintln(w, "AWSTemplateFormatVersion: '2010-09-09'")
	fmt.Fprintf(w, "Description: Cross-account role for Klanker Maker SCP management\n")
	fmt.Fprintln(w, "Resources:")
	fmt.Fprintln(w, "  KMOrgAdminRole:")
	fmt.Fprintln(w, "    Type: AWS::IAM::Role")
	fmt.Fprintln(w, "    Properties:")
	fmt.Fprintf(w, "      RoleName: %s\n", roleName)
	fmt.Fprintln(w, "      AssumeRolePolicyDocument:")
	fmt.Fprintln(w, "        Version: '2012-10-17'")
	fmt.Fprintln(w, "        Statement:")
	fmt.Fprintln(w, "          - Effect: Allow")
	fmt.Fprintln(w, "            Principal:")
	fmt.Fprintf(w, "              AWS: 'arn:aws:iam::%s:root'\n", callerAccount)
	fmt.Fprintln(w, "            Action: 'sts:AssumeRole'")
	fmt.Fprintln(w, "            Condition:")
	fmt.Fprintln(w, "              ArnLike:")
	fmt.Fprintln(w, "                aws:PrincipalArn:")
	fmt.Fprintf(w, "                  - 'arn:aws:iam::%s:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*'\n", callerAccount)
	fmt.Fprintf(w, "                  - 'arn:aws:iam::%s:role/km-provisioner-*'\n", callerAccount)
	fmt.Fprintln(w, "      Policies:")
	fmt.Fprintln(w, "        - PolicyName: km-org-admin-policy")
	fmt.Fprintln(w, "          PolicyDocument:")
	fmt.Fprintln(w, "            Version: '2012-10-17'")
	fmt.Fprintln(w, "            Statement:")
	fmt.Fprintln(w, "              # NOTE: Replace <ORG_ID> with your Organization ID (e.g., o-abc123xyz)")
	fmt.Fprintln(w, "              - Sid: SCPMutate")
	fmt.Fprintln(w, "                Effect: Allow")
	fmt.Fprintln(w, "                Action:")
	fmt.Fprintln(w, "                  - organizations:UpdatePolicy")
	fmt.Fprintln(w, "                  - organizations:DeletePolicy")
	fmt.Fprintln(w, "                  - organizations:DescribePolicy")
	fmt.Fprintln(w, "                  - organizations:ListTargetsForPolicy")
	fmt.Fprintln(w, "                  - organizations:TagResource")
	fmt.Fprintln(w, "                  - organizations:UntagResource")
	fmt.Fprintln(w, "                  - organizations:ListTagsForResource")
	fmt.Fprintln(w, "                Resource:")
	fmt.Fprintf(w, "                  - 'arn:aws:organizations::%s:policy/*/service_control_policy/*'\n", orgAccount)
	fmt.Fprintln(w, "              - Sid: SCPAttachDetach")
	fmt.Fprintln(w, "                Effect: Allow")
	fmt.Fprintln(w, "                Action:")
	fmt.Fprintln(w, "                  - organizations:AttachPolicy")
	fmt.Fprintln(w, "                  - organizations:DetachPolicy")
	fmt.Fprintln(w, "                Resource:")
	fmt.Fprintf(w, "                  - 'arn:aws:organizations::%s:policy/*/service_control_policy/*'\n", orgAccount)
	fmt.Fprintf(w, "                  - 'arn:aws:organizations::%s:account/*/%s'\n", orgAccount, callerAccount)
	fmt.Fprintln(w, "              - Sid: SCPCreateListDescribe")
	fmt.Fprintln(w, "                Effect: Allow")
	fmt.Fprintln(w, "                Action:")
	fmt.Fprintln(w, "                  - organizations:CreatePolicy")
	fmt.Fprintln(w, "                  - organizations:ListPolicies")
	fmt.Fprintln(w, "                  - organizations:ListPoliciesForTarget")
	fmt.Fprintln(w, "                  - organizations:DescribeOrganization")
	fmt.Fprintln(w, "                Resource: '*'")
	fmt.Fprintln(w, "```")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Step 0: Enable SCPs in your Organization")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "SCPs must be enabled before bootstrap can create policies. Check and enable from the organization account:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "```bash")
	fmt.Fprintln(w, "# Check if SCPs are enabled")
	fmt.Fprintln(w, "aws organizations list-roots --query 'Roots[0].PolicyTypes'")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "# If SERVICE_CONTROL_POLICY is not listed, enable it:")
	fmt.Fprintln(w, "aws organizations enable-policy-type \\")
	fmt.Fprintln(w, "  --root-id $(aws organizations list-roots --query 'Roots[0].Id' --output text) \\")
	fmt.Fprintln(w, "  --policy-type SERVICE_CONTROL_POLICY")
	fmt.Fprintln(w, "```")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## What this role does")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "- **Role ARN:** arn:aws:iam::%s:role/%s\n", orgAccount, roleName)
	fmt.Fprintf(w, "- **Trusted by:** Application account %s (SSO and provisioner roles only)\n", callerAccount)
	fmt.Fprintln(w, "- **Permissions:** Organizations SCP CRUD — create, attach, update, and delete Service Control Policies")
	fmt.Fprintln(w, "- **Used by:** `km bootstrap --dry-run=false` to deploy the km-sandbox-containment SCP")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "After creating this role, run: km bootstrap --dry-run=false\n")

	return nil
}

// runShowSCP prints the {prefix}-sandbox-containment SCP policy JSON and the
// {prefix}-org-admin role/trust policy, with real account IDs from km-config.yaml
// substituted in. Phase 84.4: uses cfg.ResourcePrefix (default "km") so non-km
// installs display the correct role names.
func runShowSCP(ctx context.Context, cfg *config.Config, w io.Writer) error {
	loadedCfg, err := loadBootstrapConfig(cfg)
	if err != nil {
		return err
	}

	appAccount := loadedCfg.ApplicationAccountID
	if appAccount == "" {
		return fmt.Errorf("no application account configured\nRun 'km configure' and set accounts.application first")
	}
	orgAccount := loadedCfg.OrganizationAccountID
	if orgAccount == "" {
		fmt.Fprintln(w, "SCP enforcement disabled — no organization account configured.")
		fmt.Fprintln(w, "Set accounts.organization in km-config.yaml to enable SCP deployment.")
		return nil
	}

	region := loadedCfg.PrimaryRegion
	if region == "" {
		region = "us-east-1"
	}

	// Determine caller account for trust policy (same logic as runShowPrereqs).
	callerAccount := appAccount
	if callerAccount == "" {
		callerAccount = loadedCfg.TerraformAccountID
	}

	// Phase 84.4: use resource_prefix from config so non-km installs show correct role names.
	resourcePrefix := loadedCfg.ResourcePrefix
	if resourcePrefix == "" {
		resourcePrefix = "km"
	}

	// Build SCP policy document via prefix-based builder (mirrors scp/v2.0.0/main.tf locals).
	policyJSON := BuildSCPPolicyFromPrefix(resourcePrefix, appAccount, region)

	// --- Print SCP policy ---
	policyName := resourcePrefix + "-sandbox-containment"
	fmt.Fprintln(w, "# ============================================================")
	fmt.Fprintf(w, "# %s SCP Policy\n", policyName)
	fmt.Fprintln(w, "#")
	fmt.Fprintf(w, "# Target: Application account %s\n", appAccount)
	fmt.Fprintf(w, "# Region lock: %s\n", region)
	fmt.Fprintln(w, "# ============================================================")
	fmt.Fprintln(w)
	fmt.Fprintln(w, policyJSON)
	fmt.Fprintln(w)

	// Operator IAM positive-allow guidance for Phase 56 AMI lifecycle.
	WriteOperatorIAMGuidance(w)
	fmt.Fprintln(w)

	// --- Print {prefix}-org-admin role/trust policy ---
	roleName := resourcePrefix + "-org-admin"

	trustPolicy := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect": "Allow",
				"Principal": map[string]interface{}{
					"AWS": fmt.Sprintf("arn:aws:iam::%s:root", callerAccount),
				},
				"Action": "sts:AssumeRole",
				"Condition": map[string]interface{}{
					"ArnLike": map[string]interface{}{
						"aws:PrincipalArn": []string{
							fmt.Sprintf("arn:aws:iam::%s:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*", callerAccount),
							fmt.Sprintf("arn:aws:iam::%s:role/%s-provisioner-*", callerAccount, resourcePrefix),
						},
					},
				},
			},
		},
	}
	trustJSON, _ := json.MarshalIndent(trustPolicy, "", "  ")

	rolePolicy := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Sid":    "SCPMutate",
				"Effect": "Allow",
				"Action": []string{
					"organizations:UpdatePolicy", "organizations:DeletePolicy",
					"organizations:DescribePolicy", "organizations:ListTargetsForPolicy",
					"organizations:TagResource", "organizations:UntagResource",
					"organizations:ListTagsForResource",
				},
				"Resource": fmt.Sprintf("arn:aws:organizations::%s:policy/*/service_control_policy/*", orgAccount),
			},
			{
				"Sid":    "SCPAttachDetach",
				"Effect": "Allow",
				"Action": []string{"organizations:AttachPolicy", "organizations:DetachPolicy"},
				"Resource": []string{
					fmt.Sprintf("arn:aws:organizations::%s:policy/*/service_control_policy/*", orgAccount),
					fmt.Sprintf("arn:aws:organizations::%s:account/*/%s", orgAccount, appAccount),
				},
			},
			{
				"Sid":      "SCPCreateListDescribe",
				"Effect":   "Allow",
				"Action":   []string{"organizations:CreatePolicy", "organizations:ListPolicies", "organizations:ListPoliciesForTarget", "organizations:DescribeOrganization"},
				"Resource": "*",
			},
		},
	}
	rolePolicyJSON, _ := json.MarshalIndent(rolePolicy, "", "  ")

	fmt.Fprintln(w, "# ============================================================")
	fmt.Fprintf(w, "# %s Role — Organization account %s\n", roleName, orgAccount)
	fmt.Fprintln(w, "#")
	fmt.Fprintf(w, "# Assumed by: Application account %s (SSO + provisioner roles)\n", callerAccount)
	fmt.Fprintln(w, "# Used by:    km bootstrap --dry-run=false")
	fmt.Fprintln(w, "# ============================================================")
	fmt.Fprintln(w)

	fmt.Fprintf(w, "## Trust Policy (AssumeRolePolicyDocument) for role %s\n\n", roleName)
	fmt.Fprintln(w, string(trustJSON))
	fmt.Fprintln(w)

	fmt.Fprintf(w, "## Inline Policy (%s-org-admin-policy) for role %s\n\n", resourcePrefix, roleName)
	fmt.Fprintln(w, string(rolePolicyJSON))
	fmt.Fprintln(w)

	fmt.Fprintln(w, "# AWS CLI commands to create this role:")
	fmt.Fprintf(w, "#   aws iam create-role --role-name %s --assume-role-policy-document '<trust-policy-json>'\n", roleName)
	fmt.Fprintf(w, "#   aws iam put-role-policy --role-name %s --policy-name %s-org-admin-policy --policy-document '<inline-policy-json>'\n", roleName, resourcePrefix)

	return nil
}

// loadBootstrapConfig loads config from the injected cfg or from disk.
func loadBootstrapConfig(cfg *config.Config) (*config.Config, error) {
	if cfg != nil && (cfg.OrganizationAccountID != "" || cfg.DNSParentAccountID != "" || cfg.ApplicationAccountID != "" || cfg.Domain != "") {
		return cfg, nil
	}

	kmConfigPath := findKMConfigPath()
	if _, err := os.Stat(kmConfigPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("km-config.yaml not found at %s\nRun 'km configure' first", kmConfigPath)
	}

	loadedCfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("invalid km-config.yaml: %w", err)
	}
	return loadedCfg, nil
}

// warnEmptyAccountIDs emits one WARN line to w per empty required accounts.* key.
// Required keys: accounts.organization, accounts.dns_parent, accounts.application.
// accounts.terraform is intentionally excluded (env-precedence preserved per Plan 02).
// Called at top of the runBootstrap status banner block so the operator sees the gap
// before any AWS API is hit.
func warnEmptyAccountIDs(cfg *config.Config, w io.Writer) {
	checks := []struct {
		key   string
		value string
	}{
		{"accounts.organization", cfg.OrganizationAccountID},
		{"accounts.dns_parent", cfg.DNSParentAccountID},
		{"accounts.application", cfg.ApplicationAccountID},
	}
	for _, c := range checks {
		if c.value == "" {
			fmt.Fprintf(w, "WARN: %s is empty in km-config.yaml — required for this command\n", c.key)
		}
	}
}

// runBootstrap implements bootstrap validation, dry-run output, and SCP deployment.
func runBootstrap(ctx context.Context, cfg *config.Config, dryRun bool, w io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}

	loadedCfg, err := loadBootstrapConfig(cfg)
	if err != nil {
		return err
	}

	ExportTerragruntEnvVars(loadedCfg)

	warnEmptyAccountIDs(loadedCfg, os.Stderr)

	fmt.Fprintf(w, "Domain:  %s\n", loadedCfg.Domain)
	fmt.Fprintf(w, "Region:  %s\n", loadedCfg.PrimaryRegion)
	orgDisplay := loadedCfg.OrganizationAccountID
	if orgDisplay == "" {
		orgDisplay = "(not set)"
	}
	dnsParentDisplay := loadedCfg.DNSParentAccountID
	if dnsParentDisplay == "" {
		dnsParentDisplay = "(not set)"
	}
	fmt.Fprintf(w, "Organization account: %s\n", orgDisplay)
	fmt.Fprintf(w, "DNS parent account: %s\n", dnsParentDisplay)
	fmt.Fprintf(w, "Application account: %s\n", loadedCfg.ApplicationAccountID)
	fmt.Fprintln(w)

	if dryRun {
		fmt.Fprintln(w, "Dry run — the following infrastructure would be created:")
		fmt.Fprintln(w)

		prefix := loadedCfg.GetResourcePrefix()
		regionLabel := compiler.RegionLabel(loadedCfg.PrimaryRegion)

		stateBucket := ""
		if cfg != nil {
			stateBucket = cfg.StateBucket
		}
		if stateBucket == "" {
			stateBucket = prefix + "-state-<hash>"
		}

		budgetTable := loadedCfg.BudgetTableName
		if budgetTable == "" {
			budgetTable = prefix + "-budgets"
		}

		fmt.Fprintf(w, "  S3 bucket:         %s\n", stateBucket)
		fmt.Fprintf(w, "    Purpose:         Sandbox metadata storage (km list/status)\n")
		fmt.Fprintf(w, "    Encryption:      AES256 (S3-managed)\n")
		fmt.Fprintf(w, "    Versioning:      enabled\n")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  S3 bucket:         tf-%s-state-%s  [created by Terragrunt --backend-bootstrap on first apply]\n", prefix, regionLabel)
		fmt.Fprintf(w, "    Purpose:         Terraform remote state\n")
		fmt.Fprintf(w, "    Encryption:      enabled (S3 default)\n")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  DynamoDB table:    tf-%s-locks-%s  [created by Terragrunt on first apply]\n", prefix, regionLabel)
		fmt.Fprintf(w, "    Purpose:         Terraform state locking\n")
		fmt.Fprintf(w, "    Billing:         PAY_PER_REQUEST\n")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  DynamoDB table:    %s\n", budgetTable)
		fmt.Fprintf(w, "    Purpose:         Sandbox budget enforcement tracking\n")
		fmt.Fprintf(w, "    Billing:         PAY_PER_REQUEST\n")
		fmt.Fprintln(w)

		// SCP policy section
		if loadedCfg.OrganizationAccountID != "" {
			fmt.Fprintf(w, "  SCP Policy:        km-sandbox-containment\n")
			fmt.Fprintf(w, "    Target:          Application account (%s)\n", loadedCfg.ApplicationAccountID)
			fmt.Fprintf(w, "    Threat coverage: SG mutation, network escape, instance mutation,\n")
			fmt.Fprintf(w, "                     IAM escalation, storage exfiltration, SSM pivot,\n")
			fmt.Fprintf(w, "                     Organizations discovery, region lock\n")
			fmt.Fprintf(w, "    Trusted roles:   AWSReservedSSO_*_*, km-provisioner-*, km-lifecycle-*,\n")
			fmt.Fprintf(w, "                     km-ecs-spot-handler, km-ttl-handler\n")
			fmt.Fprintf(w, "    Deploy via:      km bootstrap (organization account credentials required)\n")
		} else {
			fmt.Fprintf(w, "  SCP Policy:        [SKIPPED — accounts.organization not set]\n")
			fmt.Fprintf(w, "    Run 'km configure' and set accounts.organization to enable SCP deployment.\n")
		}
		fmt.Fprintln(w)
		platformAlias := loadedCfg.GetPlatformKMSAlias()
		fmt.Fprintf(w, "  KMS key:           %s\n", strings.TrimPrefix(platformAlias, "alias/"))
		fmt.Fprintf(w, "    Purpose:         SSM SecureString encryption for sandbox identity keys and secrets\n")
		fmt.Fprintf(w, "    Alias:           %s\n", platformAlias)
		fmt.Fprintln(w)

		if loadedCfg.ArtifactsBucket != "" {
			fmt.Fprintf(w, "  S3 bucket:         %s\n", loadedCfg.ArtifactsBucket)
			fmt.Fprintf(w, "    Purpose:         Lambda zips, sidecar binaries, sandbox artifacts\n")
			fmt.Fprintf(w, "    Versioning:      enabled\n")
		} else {
			fmt.Fprintf(w, "  S3 artifacts:      [SKIPPED — no artifacts_bucket configured]\n")
			fmt.Fprintf(w, "    Run 'km configure' and set artifacts_bucket to enable.\n")
		}
		fmt.Fprintln(w)

		fmt.Fprintln(w, "Run 'km bootstrap --dry-run=false' to provision.")
		return nil
	}

	// Non-dry-run: deploy SCP sandbox-containment policy.
	// DNS parent env var is always exported (independent of org).
	os.Setenv("KM_ACCOUNTS_DNS_PARENT", loadedCfg.DNSParentAccountID)
	if loadedCfg.OrganizationAccountID != "" {
		// Export config values as env vars for Terragrunt's site.hcl get_env() calls.
		os.Setenv("KM_ACCOUNTS_ORGANIZATION", loadedCfg.OrganizationAccountID)
		os.Setenv("KM_ACCOUNTS_APPLICATION", loadedCfg.ApplicationAccountID)
		if loadedCfg.Domain != "" {
			os.Setenv("KM_DOMAIN", loadedCfg.Domain)
		}
		if loadedCfg.PrimaryRegion != "" {
			os.Setenv("KM_REGION", loadedCfg.PrimaryRegion)
		}

		scpDir := filepath.Join(findRepoRoot(), "infra", "live", "management", "scp")
		fmt.Fprintln(w, "Deploying SCP sandbox-containment policy...")
		if err := ApplyTerragruntFunc(ctx, scpDir); err != nil {
			return fmt.Errorf("scp bootstrap: %w", err)
		}
		fmt.Fprintln(w, "SCP sandbox-containment policy deployed to application account.")
	} else {
		fmt.Fprintln(w, "Skipping SCP deployment — no organization account configured.")
	}

	// Create the platform KMS key (alias/{prefix}-platform) for SSM SecureString encryption.
	fmt.Fprintln(w)
	if err := ensureKMSPlatformKey(ctx, loadedCfg, w); err != nil {
		return fmt.Errorf("kms bootstrap: %w", err)
	}

	// Create S3 artifacts bucket if configured.
	if loadedCfg.ArtifactsBucket != "" {
		fmt.Fprintln(w)
		if err := ensureArtifactsBucket(ctx, loadedCfg, w); err != nil {
			return fmt.Errorf("artifacts bucket bootstrap: %w", err)
		}
	}

	return nil
}

// ensureKMSPlatformKey creates the platform KMS key and alias if they don't exist.
// The alias is alias/{prefix}-platform where prefix comes from cfg.GetResourcePrefix()
// (default "km"). Pass a non-nil kmsClient to override the default real AWS client (used in tests).
func ensureKMSPlatformKey(ctx context.Context, cfg *config.Config, w io.Writer, kmsClient ...KMSEnsureAPI) error {
	var client KMSEnsureAPI
	if len(kmsClient) > 0 && kmsClient[0] != nil {
		client = kmsClient[0]
	} else {
		region := cfg.PrimaryRegion
		if region == "" {
			region = "us-east-1"
		}

		awsCfg, err := awspkg.LoadAWSConfigInRegion(ctx, "klanker-terraform", region)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		client = kms.NewFromConfig(awsCfg)
	}

	aliasName := cfg.GetPlatformKMSAlias()

	// Check if alias already exists.
	_, err := client.DescribeKey(ctx, &kms.DescribeKeyInput{
		KeyId: aws.String(aliasName),
	})
	if err == nil {
		fmt.Fprintf(w, "KMS key %s already exists.\n", aliasName)
		return nil
	}

	// Create the key.
	fmt.Fprintf(w, "Creating KMS key %s...\n", aliasName)
	createOut, err := client.CreateKey(ctx, &kms.CreateKeyInput{
		Description: aws.String("Klanker Maker platform key — SSM SecureString encryption for sandbox secrets and identity keys"),
		KeyUsage:    kmstypes.KeyUsageTypeEncryptDecrypt,
		Tags: []kmstypes.Tag{
			{TagKey: aws.String("km:component"), TagValue: aws.String("platform")},
			{TagKey: aws.String("km:managed"), TagValue: aws.String("true")},
		},
	})
	if err != nil {
		return fmt.Errorf("create KMS key: %w", err)
	}

	// Create the alias.
	_, err = client.CreateAlias(ctx, &kms.CreateAliasInput{
		AliasName:   aws.String(aliasName),
		TargetKeyId: createOut.KeyMetadata.KeyId,
	})
	if err != nil {
		return fmt.Errorf("create KMS alias: %w", err)
	}

	fmt.Fprintf(w, "KMS key created: %s → %s\n", aliasName, aws.ToString(createOut.KeyMetadata.KeyId))
	return nil
}

// ensureArtifactsBucket creates the S3 artifacts bucket with versioning if it doesn't exist.
func ensureArtifactsBucket(ctx context.Context, cfg *config.Config, w io.Writer) error {
	region := cfg.PrimaryRegion
	if region == "" {
		region = "us-east-1"
	}

	awsCfg, err := awspkg.LoadAWSConfigInRegion(ctx, "klanker-terraform", region)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg)

	bucketName := cfg.ArtifactsBucket

	// Check if bucket already exists.
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err == nil {
		fmt.Fprintf(w, "S3 bucket %s already exists.\n", bucketName)
		return nil
	}

	// Create the bucket.
	fmt.Fprintf(w, "Creating S3 bucket %s...\n", bucketName)
	createInput := &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	}
	// us-east-1 must not specify LocationConstraint
	if region != "us-east-1" {
		createInput.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(region),
		}
	}
	if _, err := client.CreateBucket(ctx, createInput); err != nil {
		return fmt.Errorf("create S3 bucket: %w", err)
	}

	// Enable versioning.
	_, err = client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucketName),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: s3types.BucketVersioningStatusEnabled,
		},
	})
	if err != nil {
		return fmt.Errorf("enable bucket versioning: %w", err)
	}

	fmt.Fprintf(w, "S3 bucket %s created with versioning enabled.\n", bucketName)
	return nil
}
