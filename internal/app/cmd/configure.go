package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	"gopkg.in/yaml.v3"
)

// emailConfig holds operator-level email settings for km-config.yaml.
type emailConfig struct {
	AllowedSenders []string `yaml:"allowedSenders,omitempty"`
}

// platformConfig is the structure written to km-config.yaml.
type platformConfig struct {
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

// NewConfigureCmd creates the "km configure" wizard command.
func NewConfigureCmd(cfg *config.Config) *cobra.Command {
	return newConfigureCmdWithIO(cfg, os.Stdin, os.Stdout)
}

// newConfigureCmdWithIO creates the configure command with injected I/O for testability.
func newConfigureCmdWithIO(cfg *config.Config, in io.Reader, out io.Writer) *cobra.Command {
	var (
		nonInteractive  bool
		outputDir       string
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
			return runConfigure(in, out, outputDir, nonInteractive, domain,
				organizationAcct, dnsParentAcct, terraformAcct, applicationAcct,
				ssoStartURL, ssoRegion, region, stateBucket, artifactsBucket, operatorEmail, safePhrase, maxSandboxes)
		},
	}

	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false,
		"Skip prompts; use flag values directly")
	cmd.Flags().StringVar(&outputDir, "output-dir", "",
		"Directory to write km-config.yaml (default: repo root or current dir)")
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
func runConfigure(in io.Reader, out io.Writer, outputDir string, nonInteractive bool,
	domain, organizationAcct, dnsParentAcct, terraformAcct, applicationAcct,
	ssoStartURL, ssoRegion, region, stateBucket, artifactsBucket, operatorEmail, safePhrase string,
	maxSandboxes int) error {

	if nonInteractive {
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
		stateBucket, err = prompt(out, scanner, "S3 state bucket for sandbox metadata (used by km list/status)", stateBucket)
		if err != nil {
			return err
		}
		artifactsBucket, err = prompt(out, scanner, "S3 artifacts bucket for Lambda zips, sidecars, sandbox artifacts", artifactsBucket)
		if err != nil {
			return err
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
		fmt.Fprintln(out, "Detected 3-account topology.")
		fmt.Fprintf(out, "\nDNS delegation required:\n")
		fmt.Fprintf(out, "  1. Create a hosted zone for sandboxes.%s in the application account (%s).\n", domain, applicationAcct)
		fmt.Fprintf(out, "  2. Copy the NS records and add them as NS records in the DNS parent account (%s)\n", dnsParentAcct)
		fmt.Fprintf(out, "     under %s pointing to sandboxes.%s.\n\n", domain, domain)
	}

	// Build config
	pc := platformConfig{
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
		BudgetTableName: "km-budgets",
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

	// Write header comment + YAML
	header := "# km-config.yaml — generated by km configure\n# Add this file to .gitignore\n\n"
	if err := os.WriteFile(outPath, append([]byte(header), data...), 0600); err != nil {
		return fmt.Errorf("writing km-config.yaml: %w", err)
	}

	fmt.Fprintf(out, "Written: %s\n", outPath)
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
