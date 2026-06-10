// Package cmd — h1.go
// Implements the "km h1" command group: init, status.
//
// Phase 103 Plan 06: the operator-facing HackerOne bridge config surface.
// Forked from github.go, DROPPING the manifest subcommand — HackerOne has no
// App-install model; webhooks are configured per-program in the HackerOne UI
// (Engagements → Program → Settings → Automation → Webhooks).
//
//   - km h1 init    — mint a 32-byte hex webhook secret, capture Basic-Auth creds
//     (api-username / api-token), write all three to SSM under
//     /{prefix}config/h1/{webhook-secret,api-username,api-token}, and print the
//     bridge Function URL + secret + the HackerOne Webhooks UI paste path.
//   - km h1 status  — read + print SSM-backed H1 config (webhook secret + token
//     REDACTED), plus the configured programs/targets/handle from cfg.H1.
//
// The SSM keys written here are consumed by the km-h1-bridge Lambda (Plan 04/08):
// webhook-secret backs the HMAC verify; api-username/api-token back the Basic-Auth
// reply channel. This closes footgun #9 — "every runtime SSM read needs a write".
package cmd

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// H1SSMReadAPI is a narrow read interface for `km h1 status`.
// The real *ssm.Client satisfies it directly.
type H1SSMReadAPI interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// NewH1Cmd creates the "km h1" parent cobra command with init + status
// subcommands. There is intentionally NO manifest subcommand — HackerOne has no
// App-install model (webhooks are configured per-program in the HackerOne UI).
func NewH1Cmd(cfg *config.Config) *cobra.Command {
	parent := &cobra.Command{
		Use:          "h1",
		Short:        "Manage the HackerOne webhook bridge for report/comment sandbox triggers",
		Long:         "Commands to configure the km HackerOne bridge (Phase 103).\n\nSubcommands:\n  init   — mint a webhook secret, capture Basic-Auth creds, record bridge-url in SSM\n  status — print SSM-backed HackerOne configuration (webhook secret + api-token redacted)\n\nHackerOne has no App-install model, so there is no manifest generator. Configure\nthe webhook per-program in the HackerOne UI:\n  Engagements → Program → Settings → Automation → Webhooks",
		SilenceUsage: true,
	}
	parent.AddCommand(newH1InitCmd(cfg))
	parent.AddCommand(newH1StatusCmd(cfg))
	return parent
}

// ─────────────────────────────────────────────────────────────────────────────
// km h1 init
// ─────────────────────────────────────────────────────────────────────────────

// H1InitOpts holds parsed flag values for RunH1Init.
type H1InitOpts struct {
	// APIUsername is the HackerOne customer-API Basic-Auth username (the half
	// stored as a plain String — it is not secret). Empty → interactive prompt.
	APIUsername string
	// APIToken is the HackerOne customer-API Basic-Auth token (the secret half,
	// stored SecureString). Empty → interactive prompt.
	APIToken string
	// BridgeURL is the km-h1-bridge Lambda Function URL stored at
	// /{prefix}config/h1/bridge-url and echoed for the operator to paste into
	// the HackerOne program's Webhooks UI. May be empty on first run.
	BridgeURL string
	// Force overwrites existing SSM parameters (rotates the webhook secret).
	Force bool
}

// RunH1Init is the exported handler for `km h1 init`.
//
// It mints a 32-byte hex webhook secret, writes it plus the Basic-Auth creds to
// SSM under /{prefix}config/h1/{webhook-secret,api-username,api-token}, records
// the bridge Function URL, and prints the Function URL + minted secret + the
// HackerOne Webhooks UI paste path for the operator.
func RunH1Init(ctx context.Context, ssmClient SSMWriteAPI, cfg *config.Config, opts H1InitOpts, out io.Writer) error {
	h1Prefix := cfg.GetSsmPrefix() + "config/h1/"
	overwrite := opts.Force

	// Mint a 32-byte hex webhook secret (crypto/rand → 64 hex chars). Same scheme
	// as github init — the bridge HMAC-verifies X-H1-Signature against this.
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return fmt.Errorf("generate webhook secret: %w", err)
	}
	webhookSecret := hex.EncodeToString(secretBytes)

	// webhook-secret — SecureString.
	if err := putSSMParam(ctx, ssmClient, h1Prefix+"webhook-secret",
		webhookSecret, ssmtypes.ParameterTypeSecureString, "", overwrite); err != nil {
		return fmt.Errorf("writing webhook-secret to SSM: %w", err)
	}
	fmt.Fprintf(out, "Written: %swebhook-secret (SecureString)\n", h1Prefix)

	// api-username — String (not secret).
	if err := putSSMParam(ctx, ssmClient, h1Prefix+"api-username",
		opts.APIUsername, ssmtypes.ParameterTypeString, "", overwrite); err != nil {
		return fmt.Errorf("writing api-username to SSM: %w", err)
	}
	fmt.Fprintf(out, "Written: %sapi-username (%s)\n", h1Prefix, opts.APIUsername)

	// api-token — SecureString (the Basic-Auth secret half).
	if err := putSSMParam(ctx, ssmClient, h1Prefix+"api-token",
		opts.APIToken, ssmtypes.ParameterTypeSecureString, "", overwrite); err != nil {
		return fmt.Errorf("writing api-token to SSM: %w", err)
	}
	fmt.Fprintf(out, "Written: %sapi-token (SecureString)\n", h1Prefix)

	// bridge-url — String (may be empty before the Lambda is deployed).
	if err := putSSMParam(ctx, ssmClient, h1Prefix+"bridge-url",
		opts.BridgeURL, ssmtypes.ParameterTypeString, "", overwrite); err != nil {
		return fmt.Errorf("writing bridge-url to SSM: %w", err)
	}
	if opts.BridgeURL != "" {
		fmt.Fprintf(out, "Written: %sbridge-url (%s)\n", h1Prefix, opts.BridgeURL)
	} else {
		fmt.Fprintf(out, "Written: %sbridge-url (empty — re-run with --bridge-url after `km init` provides the Function URL)\n", h1Prefix)
	}

	// Operator paste instructions. The Function URL + secret go into the
	// HackerOne program's Webhooks UI.
	bridgeURLDisplay := opts.BridgeURL
	if bridgeURLDisplay == "" {
		bridgeURLDisplay = "(not set — re-run with --bridge-url after `km init`)"
	}
	fmt.Fprintf(out, "\nHackerOne bridge config stored. Configure the program webhook:\n")
	fmt.Fprintf(out, "  HackerOne → Engagements → Program → Settings → Automation → Webhooks\n")
	fmt.Fprintf(out, "    Payload URL:    %s\n", bridgeURLDisplay)
	fmt.Fprintf(out, "    Secret:         %s\n", webhookSecret)
	fmt.Fprintf(out, "    Content type:   application/json\n")
	return nil
}

func newH1InitCmd(cfg *config.Config) *cobra.Command {
	var apiUsername, apiToken, bridgeURL string
	var force bool

	cmd := &cobra.Command{
		Use:          "init",
		Short:        "Mint webhook secret, capture Basic-Auth creds, record bridge-url in SSM",
		Long:         "Mints a random 32-byte hex webhook secret, captures the HackerOne customer-API\nBasic-Auth credentials, and records the bridge Function URL in SSM Parameter Store.\n\nSSM keys written:\n  /{prefix}config/h1/webhook-secret  — random hex secret (SecureString)\n  /{prefix}config/h1/api-username    — Basic-Auth username (String)\n  /{prefix}config/h1/api-token       — Basic-Auth token (SecureString)\n  /{prefix}config/h1/bridge-url      — bridge Lambda Function URL (String)\n\nThe minted secret + Function URL are printed for you to paste into the HackerOne\nprogram's Webhooks UI (Engagements → Program → Settings → Automation → Webhooks).\nRe-run with --force to rotate the webhook secret.",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			// Capture Basic-Auth creds interactively when not provided via flags
			// (mirror github init's input flow).
			if apiUsername == "" {
				apiUsername = promptH1Line(c.InOrStdin(), c.OutOrStdout(), "HackerOne API username: ")
			}
			if apiToken == "" {
				apiToken = promptH1Line(c.InOrStdin(), c.OutOrStdout(), "HackerOne API token: ")
			}
			if apiUsername == "" || apiToken == "" {
				return fmt.Errorf("api-username and api-token are required (provide --api-username/--api-token or enter at the prompt)")
			}

			// Initialise the real SSM client.
			awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
			if err != nil {
				return fmt.Errorf("load AWS config: %w", err)
			}
			ssmClient := ssm.NewFromConfig(awsCfg)

			return RunH1Init(ctx, ssmClient, cfg, H1InitOpts{
				APIUsername: apiUsername,
				APIToken:    apiToken,
				BridgeURL:   bridgeURL,
				Force:       force,
			}, c.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&apiUsername, "api-username", "",
		"HackerOne customer-API Basic-Auth username (prompted if omitted)")
	cmd.Flags().StringVar(&apiToken, "api-token", "",
		"HackerOne customer-API Basic-Auth token (prompted if omitted)")
	cmd.Flags().StringVar(&bridgeURL, "bridge-url", "",
		"Bridge Lambda Function URL to store in SSM (set after `km init` provides the URL)")
	cmd.Flags().BoolVar(&force, "force", false,
		"Overwrite existing SSM parameters (rotates the webhook secret)")

	return cmd
}

// promptH1Line reads a single trimmed line from in, writing prompt to out first.
// Returns "" on read error / EOF (caller validates required-ness).
func promptH1Line(in io.Reader, out io.Writer, prompt string) string {
	fmt.Fprint(out, prompt)
	r := bufio.NewReader(in)
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}

// ─────────────────────────────────────────────────────────────────────────────
// km h1 status
// ─────────────────────────────────────────────────────────────────────────────

// RunH1Status is the exported handler for `km h1 status`.
//
// It prints the configured programs/targets/handle from cfg.H1 plus the
// SSM-backed bridge-url + api-username, REDACTING the webhook secret and the
// api-token. When h1: is absent (no programs, no handle) and SSM is empty, it
// prints a clean "not configured" message (dormant — no error).
func RunH1Status(ctx context.Context, ssmClient H1SSMReadAPI, cfg *config.Config, out io.Writer) error {
	h1Prefix := cfg.GetSsmPrefix() + "config/h1/"

	readParam := func(name string) string {
		full := name
		result, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
			Name:           &full,
			WithDecryption: boolPtr(true),
		})
		if err != nil || result.Parameter == nil || result.Parameter.Value == nil {
			return "(not set)"
		}
		return *result.Parameter.Value
	}

	webhookSecret := readParam(h1Prefix + "webhook-secret")
	apiUsername := readParam(h1Prefix + "api-username")
	apiToken := readParam(h1Prefix + "api-token")
	bridgeURL := readParam(h1Prefix + "bridge-url")

	h1 := cfg.H1
	configured := len(h1.Programs) > 0 || h1.BotHandle != "" ||
		webhookSecret != "(not set)" || apiUsername != "(not set)" || bridgeURL != "(not set)"

	if !configured {
		fmt.Fprintf(out, "HackerOne bridge: not configured (dormant).\n")
		fmt.Fprintf(out, "Run `km h1 init` and add an `h1:` block to km-config.yaml to enable.\n")
		return nil
	}

	// Redact secrets — never print the webhook secret or api-token.
	secretDisplay := "(not set)"
	if webhookSecret != "(not set)" && webhookSecret != "" {
		secretDisplay = "[set, redacted]"
	}
	tokenDisplay := "(not set)"
	if apiToken != "(not set)" && apiToken != "" {
		tokenDisplay = "[set, redacted]"
	}

	fmt.Fprintf(out, "HackerOne bridge config (prefix: %s):\n", cfg.GetSsmPrefix())
	fmt.Fprintf(out, "  webhook-secret:  %s\n", secretDisplay)
	fmt.Fprintf(out, "  api-username:    %s\n", apiUsername)
	fmt.Fprintf(out, "  api-token:       %s\n", tokenDisplay)
	fmt.Fprintf(out, "  bridge-url:      %s\n", bridgeURL)

	// Install-wide comment-keyword trigger token.
	botHandle := h1.BotHandle
	if botHandle == "" {
		botHandle = "(not set)"
	}
	fmt.Fprintf(out, "  bot_handle:      %s\n", botHandle)

	if h1.DefaultProfile != "" {
		fmt.Fprintf(out, "  default_profile: %s\n", h1.DefaultProfile)
	}

	// Program routing.
	if len(h1.Programs) == 0 {
		fmt.Fprintf(out, "  programs:        (none — comment/webhook routing dormant)\n")
		return nil
	}

	fmt.Fprintf(out, "  programs (%d):\n", len(h1.Programs))
	for _, p := range h1.Programs {
		fmt.Fprintf(out, "    %s\n", p.Handle)

		// Effective per-program bot handle.
		effHandle := p.BotHandle
		if effHandle == "" {
			effHandle = h1.BotHandle
		}
		if effHandle != "" {
			fmt.Fprintf(out, "      bot_handle:    %s\n", effHandle)
		}

		// Targets (multi-target fanout).
		if len(p.Targets) > 0 {
			parts := make([]string, 0, len(p.Targets))
			for _, t := range p.Targets {
				switch {
				case t.Alias != "" && t.Profile != "":
					parts = append(parts, fmt.Sprintf("%s (%s)", t.Alias, t.Profile))
				case t.Alias != "":
					parts = append(parts, t.Alias)
				case t.Profile != "":
					parts = append(parts, t.Profile)
				}
			}
			fmt.Fprintf(out, "      targets:       %s\n", strings.Join(parts, ", "))
		}

		if len(p.Allow) > 0 {
			fmt.Fprintf(out, "      allow:         %s\n", strings.Join(p.Allow, ", "))
		}

		// Auto-triage events (dormant when empty).
		if len(p.Events) > 0 {
			events := make([]string, 0, len(p.Events))
			for k := range p.Events {
				events = append(events, k)
			}
			fmt.Fprintf(out, "      events:        %s\n", strings.Join(events, ", "))
		}

		// Comment-context commands (dormant when empty).
		if len(p.Commands) > 0 {
			cmds := make([]string, 0, len(p.Commands))
			for k := range p.Commands {
				cmds = append(cmds, "/"+k)
			}
			fmt.Fprintf(out, "      commands:      %s\n", strings.Join(cmds, ", "))
		}
		if p.DefaultCommand != "" {
			fmt.Fprintf(out, "      default_command: /%s\n", p.DefaultCommand)
		}
	}

	return nil
}

func newH1StatusCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:          "status",
		Short:        "Print SSM-backed HackerOne config (webhook secret + api-token redacted)",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
			if err != nil {
				return fmt.Errorf("load AWS config: %w", err)
			}
			ssmClient := ssm.NewFromConfig(awsCfg)

			return RunH1Status(ctx, ssmClient, cfg, c.OutOrStdout())
		},
	}
}
