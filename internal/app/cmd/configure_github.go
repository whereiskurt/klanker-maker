package cmd

import (
	"bufio"
	"context"
	"encoding/pem"
	"fmt"
	"io"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// SSMWriteAPI is a narrow interface for writing SSM parameters.
// The real *ssm.Client satisfies this interface directly.
// Defined here for DI in configure_github and configure_github_test.
type SSMWriteAPI interface {
	PutParameter(ctx context.Context, params *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
}

// NewConfigureGitHubCmd creates the "km configure github" subcommand using real SSM.
func NewConfigureGitHubCmd(cfg *config.Config) *cobra.Command {
	// SSM client is created lazily at RunE time so it does not fail at flag parse.
	var ssmClient SSMWriteAPI
	return newConfigureGitHubCmdCore(cfg, &ssmClient, os.Stdin, os.Stdout)
}

// NewConfigureGitHubCmdWithDeps creates the "km configure github" subcommand with
// injected dependencies for testability. The caller must supply a non-nil ssmClient.
func NewConfigureGitHubCmdWithDeps(cfg *config.Config, ssmClient SSMWriteAPI, in io.Reader, out io.Writer) *cobra.Command {
	// Wrap the caller's client so the core can update the pointer.
	fixed := ssmClient
	return newConfigureGitHubCmdCore(cfg, &fixed, in, out)
}

// newConfigureGitHubCmdCore builds the cobra.Command. ssmClientPtr is a pointer to
// an SSMWriteAPI — for the real path it starts nil and is initialised inside RunE;
// for the test path it is pre-populated by NewConfigureGitHubCmdWithDeps.
func newConfigureGitHubCmdCore(cfg *config.Config, ssmClientPtr *SSMWriteAPI, in io.Reader, out io.Writer) *cobra.Command {
	var (
		nonInteractive  bool
		appClientID     string
		privateKeyFile  string
		installationID  string
		force           bool
	)

	cmd := &cobra.Command{
		Use:   "github",
		Short: "Configure GitHub App credentials for sandbox source-access tokens",
		Long: `Interactive wizard to store GitHub App credentials in SSM Parameter Store.

Credentials stored:
  /km/config/github/app-client-id   — GitHub App client ID
  /km/config/github/private-key     — GitHub App private key (SecureString)
  /km/config/github/installation-id — Installation ID for the target org/account

These parameters are read at km create time to generate scoped installation tokens.`,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()

			// Initialise real SSM client if not injected (production path).
			if *ssmClientPtr == nil {
				awsProfile := "klanker-terraform"
				awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
				if err != nil {
					return fmt.Errorf("failed to load AWS config: %w", err)
				}
				*ssmClientPtr = ssm.NewFromConfig(awsCfg)
			}

			// Ensure in/out have usable defaults.
			reader := in
			if reader == nil {
				reader = os.Stdin
			}
			writer := out
			if writer == nil {
				writer = os.Stdout
			}

			return runConfigureGitHub(ctx, *ssmClientPtr, writer, reader,
				nonInteractive, appClientID, privateKeyFile, installationID, force, cfg)
		},
	}

	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false,
		"Skip prompts; use flag values directly")
	cmd.Flags().StringVar(&appClientID, "app-client-id", "",
		"GitHub App client ID (e.g. Iv1.abc123)")
	cmd.Flags().StringVar(&privateKeyFile, "private-key-file", "",
		"Path to the GitHub App private key PEM file")
	cmd.Flags().StringVar(&installationID, "installation-id", "",
		"GitHub App installation ID for the target org/user")
	cmd.Flags().BoolVar(&force, "force", false,
		"Overwrite existing SSM parameters (default: refuse if already set)")

	_ = cfg // reserved for future use (e.g. KMS key from config)

	return cmd
}

// runConfigureGitHub implements the configure github wizard logic.
func runConfigureGitHub(ctx context.Context, ssmClient SSMWriteAPI, out io.Writer, in io.Reader,
	nonInteractive bool, appClientID, privateKeyFile, installationID string, force bool, cfg *config.Config) error {

	if nonInteractive {
		// Validate required flags
		missing := []string{}
		if appClientID == "" {
			missing = append(missing, "--app-client-id")
		}
		if privateKeyFile == "" {
			missing = append(missing, "--private-key-file")
		}
		if installationID == "" {
			missing = append(missing, "--installation-id")
		}
		if len(missing) > 0 {
			return fmt.Errorf("--non-interactive requires: %s", joinStrings(missing))
		}
	} else {
		scanner := bufio.NewScanner(in)
		var err error

		appClientID, err = prompt(out, scanner, "GitHub App Client ID (e.g. Iv1.abc123)", appClientID)
		if err != nil {
			return err
		}
		privateKeyFile, err = prompt(out, scanner, "Path to private key PEM file", privateKeyFile)
		if err != nil {
			return err
		}
		installationID, err = prompt(out, scanner, "Installation ID", installationID)
		if err != nil {
			return err
		}
	}

	// Read PEM from file
	pemBytes, err := os.ReadFile(privateKeyFile)
	if err != nil {
		return fmt.Errorf("reading private key file %q: %w", privateKeyFile, err)
	}

	// Validate PEM can be decoded
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return fmt.Errorf("private key file %q does not contain a valid PEM block", privateKeyFile)
	}

	// Determine KMS key ARN for SecureString encryption.
	// Use platform KMS key from config if available; SSM uses the default service key otherwise.
	kmsKeyID := ""
	_ = cfg // no KMS ARN field on Config yet — SSM default service key is used

	overwrite := force

	// Write /km/config/github/app-client-id
	if err := putSSMParam(ctx, ssmClient, "/km/config/github/app-client-id",
		appClientID, ssmtypes.ParameterTypeString, "", overwrite); err != nil {
		return fmt.Errorf("writing app-client-id to SSM: %w", err)
	}
	fmt.Fprintf(out, "Written: /km/config/github/app-client-id\n")

	// Write /km/config/github/private-key (SecureString)
	if err := putSSMParam(ctx, ssmClient, "/km/config/github/private-key",
		string(pemBytes), ssmtypes.ParameterTypeSecureString, kmsKeyID, overwrite); err != nil {
		return fmt.Errorf("writing private-key to SSM: %w", err)
	}
	fmt.Fprintf(out, "Written: /km/config/github/private-key (SecureString)\n")

	// Write /km/config/github/installation-id
	if err := putSSMParam(ctx, ssmClient, "/km/config/github/installation-id",
		installationID, ssmtypes.ParameterTypeString, "", overwrite); err != nil {
		return fmt.Errorf("writing installation-id to SSM: %w", err)
	}
	fmt.Fprintf(out, "Written: /km/config/github/installation-id\n")

	fmt.Fprintf(out, "GitHub App credentials stored. Run 'km create' with a profile that has sourceAccess.github to use them.\n")
	return nil
}

// putSSMParam writes a single SSM parameter.
// If kmsKeyID is empty, SSM uses the default service key for SecureString.
func putSSMParam(ctx context.Context, client SSMWriteAPI, name, value string,
	paramType ssmtypes.ParameterType, kmsKeyID string, overwrite bool) error {

	input := &ssm.PutParameterInput{
		Name:      aws.String(name),
		Value:     aws.String(value),
		Type:      paramType,
		Overwrite: aws.Bool(overwrite),
	}
	if kmsKeyID != "" {
		input.KeyId = aws.String(kmsKeyID)
	}

	_, err := client.PutParameter(ctx, input)
	return err
}

// joinStrings joins a slice of strings with ", ".
func joinStrings(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += ", "
		}
		result += s
	}
	return result
}
