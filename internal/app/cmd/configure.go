package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithy "github.com/aws/smithy-go"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
	"gopkg.in/yaml.v3"
)

// deriveOperatorEmail returns the canonical operator inbox address for a given
// install. The address shape is locked by Phase 84 CONTEXT.md:
//
//	operator-${resource_prefix}@${email_subdomain}.${domain}
//
// Empty inputs return "" — callers should fall back to whatever they had before.
func deriveOperatorEmail(resourcePrefix, emailSubdomain, domain string) string {
	if resourcePrefix == "" || emailSubdomain == "" || domain == "" {
		return ""
	}
	return fmt.Sprintf("operator-%s@%s.%s", resourcePrefix, emailSubdomain, domain)
}

// emailConfig holds operator-level email settings for km-config.yaml.
type emailConfig struct {
	AllowedSenders []string `yaml:"allowedSenders,omitempty"`
}

// platformConfig is the structure written to km-config.yaml.
type platformConfig struct {
	ResourcePrefix  string         `yaml:"resource_prefix"`
	EmailSubdomain  string         `yaml:"email_subdomain"`
	Domain          string         `yaml:"domain"`
	Accounts        accountsConfig `yaml:"accounts"`
	SSO             ssoConfig      `yaml:"sso"`
	Region          string         `yaml:"region"`
	BudgetTableName string         `yaml:"budget_table_name,omitempty"`
	StateBucket     string         `yaml:"state_bucket,omitempty"`
	ArtifactsBucket string         `yaml:"artifacts_bucket,omitempty"`
	Route53ZoneID   string         `yaml:"route53_zone_id,omitempty"`
	OperatorEmail   string         `yaml:"operator_email,omitempty"`
	SafePhrase      string         `yaml:"safe_phrase,omitempty"`
	MaxSandboxes    int            `yaml:"max_sandboxes,omitempty"`
	Email           emailConfig    `yaml:"email,omitempty"`
}

type accountsConfig struct {
	Organization string `yaml:"organization,omitempty"`
	DNSParent    string `yaml:"dns_parent,omitempty"`
	Terraform    string `yaml:"terraform"`
	Application  string `yaml:"application"`
}

type ssoConfig struct {
	StartURL string `yaml:"start_url"`
	Region   string `yaml:"region"`
}

// probeStateBucketInteractive performs an S3 HeadBucket check on the proposed
// state bucket name and guides the operator interactively when the bucket is taken.
//
// Behaviour:
//   - 404 (NotFound) or nil error: bucket is available or already owned → accept, return name.
//   - 403 (Forbidden): bucket exists but is owned by another account.
//     If accountID is non-empty: print suggestion "${name}-${accountID}" and prompt
//     [Y / edit / abort].
//     - Y: HeadBucket the suggestion; 403 again → error "pick a unique state_bucket via --state-bucket".
//     - edit: prompt for a freeform name; HeadBucket; 403 → error "pick a unique …".
//     - abort: return error "aborted by operator".
//   - Other errors: propagated as-is.
//
// stdin is a *bufio.Reader so tests can inject strings without piping os.Stdin.
// s3client is variadic-last so callers can omit it (nil → no probe; useful when
// AWS credentials are not configured at wizard time).
func probeStateBucketInteractive(
	ctx context.Context,
	initialName, accountID string,
	in *bufio.Reader, out io.Writer,
	s3client ...S3HeadBucketAPI,
) (string, error) {
	// If no client is provided, accept the name without probing.
	if len(s3client) == 0 || s3client[0] == nil {
		return initialName, nil
	}
	client := s3client[0]

	// Helper: call HeadBucket and classify the error.
	is403 := func(err error) bool {
		if err == nil {
			return false
		}
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			return apiErr.ErrorCode() == "Forbidden" || apiErr.ErrorCode() == "AccessDenied"
		}
		return false
	}
	is404 := func(err error) bool {
		if err == nil {
			return false
		}
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			return apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "NoSuchBucket"
		}
		return false
	}

	// First probe: check the initial bucket name.
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &initialName})
	if err == nil || is404(err) {
		// nil = owned by us; 404 = available. Both are acceptable.
		return initialName, nil
	}
	if !is403(err) {
		return "", err
	}

	// 403: bucket is taken by another account. Prompt the operator.
	suggestion := initialName + "-" + accountID
	if accountID == "" {
		// No account ID available — skip the suggestion path, prompt edit/abort only.
		fmt.Fprintf(out, "state_bucket %q is taken (403 Forbidden). Enter a different name or type 'abort': ", initialName)
		line, readErr := in.ReadString('\n')
		if readErr != nil && line == "" {
			return "", fmt.Errorf("reading input: %w", readErr)
		}
		line = strings.TrimSpace(line)
		if line == "abort" || line == "" {
			return "", fmt.Errorf("aborted by operator")
		}
		// HeadBucket the typed name.
		_, err2 := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &line})
		if is403(err2) {
			return "", fmt.Errorf("state_bucket %q is also taken; pick a unique state_bucket via --state-bucket", line)
		}
		if err2 != nil && !is404(err2) {
			return "", err2
		}
		return line, nil
	}

	fmt.Fprintf(out, "state_bucket %q is taken (403 Forbidden).\n", initialName)
	fmt.Fprintf(out, "Suggestion: %s\n", suggestion)
	fmt.Fprintf(out, "[Y / edit / abort]: ")

	line, readErr := in.ReadString('\n')
	if readErr != nil && line == "" {
		return "", fmt.Errorf("reading input: %w", readErr)
	}
	line = strings.TrimSpace(line)
	lower := strings.ToLower(line)

	switch lower {
	case "y", "yes", "":
		// Accept the suggestion; HeadBucket it once.
		_, err2 := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &suggestion})
		if is403(err2) {
			return "", fmt.Errorf("suggested state_bucket %q is also taken; pick a unique state_bucket via --state-bucket", suggestion)
		}
		if err2 != nil && !is404(err2) {
			return "", err2
		}
		return suggestion, nil

	case "edit":
		fmt.Fprintf(out, "Enter a new state_bucket name: ")
		custom, readErr2 := in.ReadString('\n')
		if readErr2 != nil && custom == "" {
			return "", fmt.Errorf("reading input: %w", readErr2)
		}
		custom = strings.TrimSpace(custom)
		if custom == "" {
			return "", fmt.Errorf("aborted by operator")
		}
		_, err2 := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &custom})
		if is403(err2) {
			return "", fmt.Errorf("state_bucket %q is also taken; pick a unique state_bucket via --state-bucket", custom)
		}
		if err2 != nil && !is404(err2) {
			return "", err2
		}
		return custom, nil

	case "abort":
		return "", fmt.Errorf("aborted by operator")

	default:
		return "", fmt.Errorf("aborted by operator")
	}
}

// deriveArtifactsBucket returns the canonical artifacts bucket name for an install:
// "${prefix}-artifacts-${accountID}".
func deriveArtifactsBucket(prefix, accountID string) string {
	return fmt.Sprintf("%s-artifacts-%s", prefix, accountID)
}

// validateArtifactsBucket rejects only obviously-unconfigured values:
//   - empty string (caller must set a bucket before running init/configure gates)
//   - angle-bracket placeholders left over from km-config.example.yaml
//     (e.g. "<prefix>-artifacts-<account-id>")
//
// Any other string — canonical-shaped or not — is accepted. Operators may bring
// their own bucket name; collision protection between sibling installs is
// handled by resource_prefix on resources, not by bucket naming.
func validateArtifactsBucket(name string) error {
	if name == "" {
		return fmt.Errorf("artifacts_bucket is empty; set artifacts_bucket in km-config.yaml or re-run `km configure`")
	}
	if lt := strings.Index(name, "<"); lt >= 0 {
		if strings.Index(name[lt:], ">") >= 0 {
			return fmt.Errorf("artifacts_bucket=%q is a placeholder; set a real bucket name in km-config.yaml or re-run `km configure`", name)
		}
	}
	return nil
}

// nextStepsBlock returns the canonical bootstrap sequence as a multi-line string
// for printing to stdout at the end of `km configure` and for embedding as `# `
// header comments in the generated km-config.yaml.
func nextStepsBlock() string {
	return `Next steps:
  1. km bootstrap --all --plan
     (preview the foundation+regional sequence with destroy-class gate)

  2. km bootstrap --all --dry-run=false
     (apply foundation: SES rule set, KMS, SCP, artifacts bucket)

  3. km init --plan
     (preview regional infra deltas)

  4. km init --dry-run=false
     (apply regional infra: network, EFS, lambdas, etc.)

Or run individually:
  km bootstrap --dry-run=false                   # SCP + KMS + artifacts
  km bootstrap --shared-ses --dry-run=false      # foundation SES
  km init --dry-run=false                        # regional`
}

// warnShellEnvConflict walks known KM_* environment variables and emits a WARN
// to w (stderr in production) for each env var that is set and conflicts with the
// value being written to km-config.yaml. Does NOT block the wizard.
//
// Format: "WARN: KM_<KEY>=<env-value> exported in shell will shadow km-config.yaml
// value (<yaml-value>) in current session"
func warnShellEnvConflict(pc platformConfig, w io.Writer) {
	type kvPair struct {
		envKey   string
		yamlVal  string
	}
	checks := []kvPair{
		{"KM_REGION", pc.Region},
		{"KM_DOMAIN", pc.Domain},
		{"KM_RESOURCE_PREFIX", pc.ResourcePrefix},
		{"KM_EMAIL_SUBDOMAIN", pc.EmailSubdomain},
		{"KM_STATE_BUCKET", pc.StateBucket},
		{"KM_ARTIFACTS_BUCKET", pc.ArtifactsBucket},
		{"KM_OPERATOR_EMAIL", pc.OperatorEmail},
		{"KM_SAFE_PHRASE", pc.SafePhrase},
		{"KM_ACCOUNTS_ORGANIZATION", pc.Accounts.Organization},
		{"KM_ACCOUNTS_DNS_PARENT", pc.Accounts.DNSParent},
		{"KM_ACCOUNTS_TERRAFORM", pc.Accounts.Terraform},
		{"KM_ACCOUNTS_APPLICATION", pc.Accounts.Application},
		{"KM_SSO_START_URL", pc.SSO.StartURL},
		{"KM_SSO_REGION", pc.SSO.Region},
	}
	for _, c := range checks {
		envVal := os.Getenv(c.envKey)
		if envVal == "" {
			continue
		}
		if envVal == c.yamlVal {
			continue
		}
		fmt.Fprintf(w, "WARN: %s=%s exported in shell will shadow km-config.yaml value (%s) in current session\n",
			c.envKey, envVal, c.yamlVal)
	}
}

// NewConfigureCmd creates the "km configure" wizard command.
func NewConfigureCmd(cfg *config.Config) *cobra.Command {
	return newConfigureCmdWithIO(cfg, os.Stdin, os.Stdout)
}

// newConfigureCmdWithIO creates the configure command with injected I/O for testability.
func newConfigureCmdWithIO(cfg *config.Config, in io.Reader, out io.Writer) *cobra.Command {
	var (
		nonInteractive  bool
		resetPrefix     bool
		outputDir       string
		resourcePrefix  string
		emailSubdomain  string
		domain          string
		organizationAcct string
		dnsParentAcct   string
		terraformAcct   string
		applicationAcct string
		ssoStartURL     string
		ssoRegion       string
		region          string
		stateBucket     string
		artifactsBucket string
		operatorEmail   string
		safePhrase      string
		maxSandboxes    int
	)

	cmd := &cobra.Command{
		Use:     "configure",
		Aliases: []string{"conf"},
		Short: "Interactive wizard to set up km-config.yaml",
		Long:  helpText("configure"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigure(in, out, outputDir, nonInteractive, resetPrefix, resourcePrefix, emailSubdomain,
				domain, organizationAcct, dnsParentAcct, terraformAcct, applicationAcct,
				ssoStartURL, ssoRegion, region, stateBucket, artifactsBucket, operatorEmail, safePhrase, maxSandboxes)
		},
	}

	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false,
		"Skip prompts; use flag values directly")
	cmd.Flags().StringVar(&outputDir, "output-dir", "",
		"Directory to write km-config.yaml (default: repo root or current dir)")
	cmd.Flags().StringVar(&resourcePrefix, "resource-prefix", "",
		"Prefix for all account-globally-unique AWS resource names (default: km). One-time choice at km init.")
	cmd.Flags().BoolVar(&resetPrefix, "reset-prefix", false,
		"Re-default resource_prefix to 'km' instead of preserving the value from an existing km-config.yaml")
	cmd.Flags().StringVar(&emailSubdomain, "email-subdomain", "sandboxes",
		"Subdomain for SES email addresses (default: sandboxes). One-time choice requiring fresh SES verification to change.")
	cmd.Flags().StringVar(&domain, "domain", "",
		"Base domain (e.g. klankermaker.ai)")
	cmd.Flags().StringVar(&organizationAcct, "organization-account", "",
		"AWS Organizations management account ID (optional — blank skips SCP deployment)")
	cmd.Flags().StringVar(&dnsParentAcct, "dns-parent-account", "",
		"AWS account ID owning the parent Route53 hosted zone for the domain (optional)")
	cmd.Flags().StringVar(&terraformAcct, "terraform-account", "",
		"AWS account ID for Terraform/infrastructure operations")
	cmd.Flags().StringVar(&applicationAcct, "application-account", "",
		"AWS account ID where sandboxes are provisioned")
	cmd.Flags().StringVar(&ssoStartURL, "sso-start-url", "",
		"AWS SSO portal URL")
	cmd.Flags().StringVar(&ssoRegion, "sso-region", "",
		"AWS region for SSO instance")
	cmd.Flags().StringVar(&region, "region", "",
		"Default AWS region for infrastructure")
	cmd.Flags().StringVar(&stateBucket, "state-bucket", "",
		"S3 bucket name for sandbox metadata (used by km list/status)")
	cmd.Flags().StringVar(&artifactsBucket, "artifacts-bucket", "",
		"S3 bucket for Lambda zips, sidecar binaries, and sandbox artifacts")
	cmd.Flags().StringVar(&operatorEmail, "operator-email", "",
		"Email address for sandbox lifecycle notifications (TTL, idle, budget, errors)")
	cmd.Flags().StringVar(&safePhrase, "safe-phrase", "",
		"Shared secret for email-to-create auth (KM-AUTH header in emails to operator@sandboxes.{domain})")
	cmd.Flags().IntVar(&maxSandboxes, "max-sandboxes", 0,
		"Maximum concurrent sandboxes allowed (0 = use default 10; set in km-config.yaml as max_sandboxes)")

	_ = cfg // reserved for future use (e.g. pre-filling from existing config)

	return cmd
}

// runConfigure implements the configure wizard logic.
func runConfigure(in io.Reader, out io.Writer, outputDir string, nonInteractive bool, resetPrefix bool,
	resourcePrefix, emailSubdomain,
	domain, organizationAcct, dnsParentAcct, terraformAcct, applicationAcct,
	ssoStartURL, ssoRegion, region, stateBucket, artifactsBucket, operatorEmail, safePhrase string,
	maxSandboxes int) error {

	// Preserve-on-re-run: if an existing km-config.yaml is present and the
	// operator has not requested a reset, use its resource_prefix as the default
	// so a bare re-run (e.g. to update operator_email) does not silently reset the
	// prefix back to "km".
	//
	// Phase 84: also load existing operator_email for preserve-on-rerun semantics.
	// When --reset-prefix is passed, operator_email is also cleared so the next
	// configure re-derives from the new default prefix.
	//
	// The effective directory mirrors the write-path logic at the bottom of this
	// function: use outputDir when provided, otherwise resolve via findRepoRoot().
	// This ensures that bare invocations (no --output-dir) also preserve the prefix.
	existingPrefix := ""
	existingOperatorEmail := ""
	if !resetPrefix {
		effectiveDir := outputDir
		if effectiveDir == "" {
			effectiveDir = findRepoRoot()
		}
		existingConfigPath := filepath.Join(effectiveDir, "km-config.yaml")
		if raw, readErr := os.ReadFile(existingConfigPath); readErr == nil {
			var existing platformConfig
			if unmarshalErr := yaml.Unmarshal(raw, &existing); unmarshalErr == nil {
				if existing.ResourcePrefix != "" {
					existingPrefix = existing.ResourcePrefix
				}
				// Preserve the operator_email from a prior run unless the caller
				// has explicitly provided one via --operator-email flag.
				if existing.OperatorEmail != "" && operatorEmail == "" {
					existingOperatorEmail = existing.OperatorEmail
				}
			}
		}
	}
	// When --reset-prefix is passed, operatorEmail is also cleared (set to "")
	// so it will be re-derived from the new default prefix below.
	// existingOperatorEmail remains "" in the resetPrefix path (not loaded above).

	// defaultPrefix is the effective default for resource_prefix prompts and
	// non-interactive fallback. Uses existingPrefix when available, otherwise "km".
	defaultPrefix := "km"
	if existingPrefix != "" {
		defaultPrefix = existingPrefix
	}

	if nonInteractive {
		// Apply defaults for resource_prefix and email_subdomain when not explicitly provided.
		if resourcePrefix == "" {
			resourcePrefix = defaultPrefix
		}
		if emailSubdomain == "" {
			emailSubdomain = "sandboxes"
		}
		// Phase 84: derive operator_email from prefix + email_subdomain + domain when
		// not explicitly provided. Preserve-on-rerun: use existingOperatorEmail if set.
		// --reset-prefix path: operatorEmail stays "" (cleared) so the NEXT configure
		// run re-derives from the new default prefix.
		if operatorEmail == "" && !resetPrefix {
			if existingOperatorEmail != "" {
				operatorEmail = existingOperatorEmail
			}
			// If still blank (fresh install), derive from prefix + email_subdomain + domain.
			// domain may be empty at this point if --domain is also missing; that will
			// be caught by the validation below. deriveOperatorEmail returns "" on blank
			// inputs, so we only set it when derivation succeeds.
			if operatorEmail == "" {
				if derived := deriveOperatorEmail(resourcePrefix, emailSubdomain, domain); derived != "" {
					operatorEmail = derived
				}
			}
		}
		// Gap #2a (Phase 84.4.1.1): auto-derive artifacts_bucket default when not yet set.
		// Mirrors the operator_email derivation pattern above.
		// Only fires when both resourcePrefix and applicationAcct are known.
		if artifactsBucket == "" && resourcePrefix != "" && applicationAcct != "" {
			artifactsBucket = deriveArtifactsBucket(resourcePrefix, applicationAcct)
		}
		// Phase 84.3: emit shell-env drift WARNs before validation so they reach
		// the operator even when required flags are missing. Does not block the wizard.
		warnShellEnvConflict(platformConfig{
			ResourcePrefix: resourcePrefix,
			EmailSubdomain: emailSubdomain,
			Domain:         domain,
			Accounts: accountsConfig{
				Organization: organizationAcct,
				DNSParent:    dnsParentAcct,
				Terraform:    terraformAcct,
				Application:  applicationAcct,
			},
			SSO:           ssoConfig{StartURL: ssoStartURL, Region: ssoRegion},
			Region:        region,
			StateBucket:   stateBucket,
			ArtifactsBucket: artifactsBucket,
			OperatorEmail: operatorEmail,
			SafePhrase:    safePhrase,
		}, os.Stderr)
		// Validate required flags
		missing := []string{}
		if domain == "" {
			missing = append(missing, "--domain")
		}
		// --organization-account and --dns-parent-account are both optional in non-interactive mode.
		// Doctor will surface missing config after the fact.
		if terraformAcct == "" {
			missing = append(missing, "--terraform-account")
		}
		if applicationAcct == "" {
			missing = append(missing, "--application-account")
		}
		if ssoStartURL == "" {
			missing = append(missing, "--sso-start-url")
		}
		if ssoRegion == "" {
			missing = append(missing, "--sso-region")
		}
		if region == "" {
			missing = append(missing, "--region")
		}
		if len(missing) > 0 {
			return fmt.Errorf("--non-interactive requires: %s", strings.Join(missing, ", "))
		}
	} else {
		// Interactive wizard
		scanner := bufio.NewScanner(in)
		var err error

		// Phase 66: resource_prefix and email_subdomain are asked first — they are
		// fundamental one-time choices that affect all downstream resource names.
		// Phase 82: use defaultPrefix (which may be the existing prefix from disk)
		// so a bare Enter on re-run preserves the non-default value.
		if resourcePrefix == "" {
			resourcePrefix = defaultPrefix
		}
		resourcePrefix, err = prompt(out, scanner, "Resource prefix for AWS resource names (one-time choice)", resourcePrefix)
		if err != nil {
			return err
		}
		if resourcePrefix == "" {
			resourcePrefix = defaultPrefix
		}

		if emailSubdomain == "" {
			emailSubdomain = "sandboxes"
		}
		emailSubdomain, err = prompt(out, scanner, "Email subdomain for SES addresses (one-time choice)", emailSubdomain)
		if err != nil {
			return err
		}
		if emailSubdomain == "" {
			emailSubdomain = "sandboxes"
		}

		domain, err = prompt(out, scanner, "Base domain (e.g. klankermaker.ai)", domain)
		if err != nil {
			return err
		}
		organizationAcct, err = prompt(out, scanner, "AWS Organizations management account ID (optional — leave blank to skip SCP)", organizationAcct)
		if err != nil {
			return err
		}
		dnsParentAcct, err = prompt(out, scanner, "DNS parent zone account ID (account owning the parent Route53 zone for your domain — optional if no DNS)", dnsParentAcct)
		if err != nil {
			return err
		}
		terraformAcct, err = prompt(out, scanner, "Terraform AWS account ID", terraformAcct)
		if err != nil {
			return err
		}
		applicationAcct, err = prompt(out, scanner, "Application AWS account ID", applicationAcct)
		if err != nil {
			return err
		}
		ssoStartURL, err = prompt(out, scanner, "SSO start URL", ssoStartURL)
		if err != nil {
			return err
		}
		ssoRegion, err = prompt(out, scanner, "SSO region (e.g. us-east-1)", ssoRegion)
		if err != nil {
			return err
		}
		region, err = prompt(out, scanner, "Primary region (e.g. us-east-1)", region)
		if err != nil {
			return err
		}
		// Phase 84.4.1: compute default tf-${prefix}-state-${region_label} when
		// stateBucket is unset. Mirrors site.hcl:43 (backend.bucket =
		// "${local.site.tf_state_prefix}-state-${local.region.label}").
		// Closes CONFIGURE-STATE-BUCKET-UX.
		if stateBucket == "" && resourcePrefix != "" && region != "" {
			stateBucket = fmt.Sprintf("tf-%s-state-%s", resourcePrefix, compiler.RegionLabel(region))
		}
		stateBucket, err = prompt(out, scanner, "S3 state bucket for sandbox metadata (used by km list/status)", stateBucket)
		if err != nil {
			return err
		}
		// Phase 84.4.1: HeadBucket-check the accepted bucket name; offer [Y/edit/abort] on 403.
		// Skip when in == nil (non-interactive wrapper with no stdin available).
		if in != nil {
			awsCfg, awsErr := awspkg.LoadAWSConfigInRegion(context.Background(), "klanker-terraform", region)
			if awsErr == nil {
				s3client := s3.NewFromConfig(awsCfg)
				bufReader := bufio.NewReader(in)
				stateBucket, err = probeStateBucketInteractive(context.Background(), stateBucket, applicationAcct, bufReader, out, s3client)
				if err != nil {
					return err
				}
			}
			// If awsErr != nil (no AWS creds at configure time), skip silently —
			// bootstrap will surface the issue when it actually needs to access S3.
		}
		// Gap #2a (Phase 84.4.1.1): auto-derive artifacts_bucket when not yet set.
		// Mirrors the operator_email derivation pattern below this block.
		// Only fires when both resourcePrefix and applicationAcct are known (they are
		// collected earlier in runConfigureInteractive before this prompt).
		if artifactsBucket == "" && resourcePrefix != "" && applicationAcct != "" {
			artifactsBucket = deriveArtifactsBucket(resourcePrefix, applicationAcct)
		}
		artifactsBucket, err = prompt(out, scanner, "S3 artifacts bucket for Lambda zips, sidecars, sandbox artifacts", artifactsBucket)
		if err != nil {
			return err
		}
		// Phase 84: derive operator_email as the prompt default when not yet set.
		// Preserve-on-rerun: use existingOperatorEmail if loaded from disk.
		// --reset-prefix path: existingOperatorEmail is "" and derivation is skipped so
		// the operator sees an empty default and can type a value or leave blank.
		if operatorEmail == "" && !resetPrefix {
			if existingOperatorEmail != "" {
				operatorEmail = existingOperatorEmail
			} else if derived := deriveOperatorEmail(resourcePrefix, emailSubdomain, domain); derived != "" {
				operatorEmail = derived
			}
		}
		operatorEmail, err = prompt(out, scanner, "Operator email for sandbox notifications (TTL, idle, budget)", operatorEmail)
		if err != nil {
			return err
		}
		safePhrase, err = prompt(out, scanner, "Safe phrase for email-to-create auth (KM-AUTH secret)", safePhrase)
		if err != nil {
			return err
		}

		maxStr := "10"
		if maxSandboxes > 0 {
			maxStr = fmt.Sprintf("%d", maxSandboxes)
		}
		maxInput, err := prompt(out, scanner, "Maximum concurrent sandboxes (0=unlimited)", maxStr)
		if err != nil {
			return err
		}
		if maxInput != "" && maxInput != "10" {
			// Parse to int; ignore parse errors (field is optional)
			var parsed int
			if _, scanErr := fmt.Sscanf(maxInput, "%d", &parsed); scanErr == nil {
				maxSandboxes = parsed
			}
		}
	}

	// Detect topology
	twoAccount := terraformAcct == applicationAcct
	if twoAccount {
		fmt.Fprintln(out, "Detected 2-account topology (terraform == application).")
	} else {
		// Reflect the operator's chosen email_subdomain in delegation guidance —
		// hardcoding "sandboxes." used to mislead non-default installs (e.g.
		// email_subdomain=km would still be told to set up sandboxes.<domain>).
		emailSub := emailSubdomain
		if emailSub == "" {
			emailSub = "sandboxes"
		}
		fmt.Fprintln(out, "Detected 3-account topology.")
		fmt.Fprintf(out, "\nDNS delegation required:\n")
		fmt.Fprintf(out, "  1. Create a hosted zone for %s.%s in the application account (%s).\n", emailSub, domain, applicationAcct)
		fmt.Fprintf(out, "  2. Copy the NS records and add them as NS records in the DNS parent account (%s)\n", dnsParentAcct)
		fmt.Fprintf(out, "     under %s pointing to %s.%s.\n\n", domain, emailSub, domain)
	}

	// Build config
	pc := platformConfig{
		ResourcePrefix:  resourcePrefix,
		EmailSubdomain:  emailSubdomain,
		Domain: domain,
		Accounts: accountsConfig{
			Organization: organizationAcct,
			DNSParent:    dnsParentAcct,
			Terraform:    terraformAcct,
			Application:  applicationAcct,
		},
		SSO: ssoConfig{
			StartURL: ssoStartURL,
			Region:   ssoRegion,
		},
		Region:          region,
		BudgetTableName: resourcePrefix + "-budgets",
		StateBucket:     stateBucket,
		ArtifactsBucket: artifactsBucket,
		OperatorEmail:   operatorEmail,
		SafePhrase:      safePhrase,
		MaxSandboxes:    maxSandboxes,
	}

	// Determine output path
	outDir := outputDir
	if outDir == "" {
		outDir = findRepoRoot()
	}
	outPath := filepath.Join(outDir, "km-config.yaml")

	// Serialize to YAML
	data, err := yaml.Marshal(pc)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	// Phase 84.3: build header comment block — the canonical next-steps sequence
	// is embedded at the top of km-config.yaml so operators can reference it without
	// re-running `km configure`.
	nextSteps := nextStepsBlock()
	var headerLines []string
	headerLines = append(headerLines, "# km-config.yaml — generated by km configure")
	headerLines = append(headerLines, "# Add this file to .gitignore")
	headerLines = append(headerLines, "#")
	for _, line := range strings.Split(nextSteps, "\n") {
		headerLines = append(headerLines, "# "+line)
	}
	headerLines = append(headerLines, "")
	header := strings.Join(headerLines, "\n") + "\n"

	if err := os.WriteFile(outPath, append([]byte(header), data...), 0600); err != nil {
		return fmt.Errorf("writing km-config.yaml: %w", err)
	}

	fmt.Fprintf(out, "Written: %s\n", outPath)

	// Phase 84.3: print the next-steps block to stdout so the operator sees
	// the canonical bootstrap sequence immediately after configure completes.
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, nextSteps)
	return nil
}

// prompt displays a prompt and reads a line from scanner.
// If defaultVal is non-empty, it is shown and used if the user inputs nothing.
func prompt(out io.Writer, scanner *bufio.Scanner, label, defaultVal string) (string, error) {
	if defaultVal != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, defaultVal)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}

	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("reading input: %w", err)
		}
		// EOF with default is OK
		return defaultVal, nil
	}

	line := strings.TrimSpace(scanner.Text())
	if line == "" && defaultVal != "" {
		return defaultVal, nil
	}
	return line, nil
}
