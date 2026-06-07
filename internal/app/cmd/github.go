// Package cmd — github.go
// Implements the "km github" command group: init, manifest, status.
//
// Phase 97 Plan 01: This is the operator-facing GitHub config surface.
//
//   - km github manifest  — render a GitHub App manifest with PR-review scopes +
//     issue_comment webhook
//   - km github init      — generate webhook secret, cache bot-login, record bridge-url
//     in SSM at /{prefix}config/github/{webhook-secret,bot-login,bridge-url}
//   - km github status    — read + print SSM-backed GitHub config (redact secret)
//
// The SSM keys written here are consumed by the GitHub bridge Lambda (Wave 2, Plan 02).
package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// GitHubSSMReadAPI is a narrow read interface for github status.
type GitHubSSMReadAPI interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// NewGithubCmd creates the "km github" parent cobra command with
// init, manifest, and status subcommands.
func NewGithubCmd(cfg *config.Config) *cobra.Command {
	parent := &cobra.Command{
		Use:          "github",
		Short:        "Manage the GitHub App integration for PR-comment sandbox triggers",
		Long:         "Commands to configure the km GitHub App bridge (Phase 97).\n\nSubcommands:\n  manifest — render the GitHub App manifest with PR-review scopes\n  init     — generate webhook secret, cache bot-login, record bridge-url in SSM\n  status   — print SSM-backed GitHub configuration (secret redacted)",
		SilenceUsage: true,
	}
	parent.AddCommand(newGithubManifestCmd(cfg))
	parent.AddCommand(newGithubInitCmd(cfg))
	parent.AddCommand(newGithubStatusCmd(cfg))
	return parent
}

// ─────────────────────────────────────────────────────────────────────────────
// km github manifest
// ─────────────────────────────────────────────────────────────────────────────

// GitHubManifestOpts holds parsed flag values for RunGitHubManifest.
type GitHubManifestOpts struct {
	// AppName is the --app-name override. Empty → derived from resource_prefix.
	AppName string
	// BridgeURL is the bridge Lambda URL embedded in hook_attributes.url.
	// When empty, hook_attributes.active is false (placeholder manifest).
	BridgeURL string
}

// githubManifestPayload is the JSON structure sent to GitHub's App manifest flow.
// Scope table from Phase 97 RESEARCH.md § "GitHub App scope additions":
//
//	issues:        read & write  — post/edit PR review comments
//	pull_requests: read & write  — read PR metadata, post inline reviews
//	contents:      read          — clone / read repository contents
//	checks:        read & write  — create check runs for code-review status
//
// webhook: issue_comment covers both PR review comments and issue body comments.
type githubManifestPayload struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Public bool   `json:"public"`
	DefaultPermissions map[string]string `json:"default_permissions"`
	DefaultEvents      []string          `json:"default_events"`
	HookAttributes     map[string]interface{} `json:"hook_attributes"`
}

// RunGitHubManifest is the exported handler for km github manifest.
// It renders the GitHub App manifest JSON to w.
func RunGitHubManifest(ctx context.Context, cfg *config.Config, opts GitHubManifestOpts, w io.Writer) error {
	appName := opts.AppName
	if appName == "" {
		appName = fmt.Sprintf("%s-github-bridge", cfg.GetResourcePrefix())
	}

	hookURL := opts.BridgeURL
	hookActive := opts.BridgeURL != ""
	if hookURL == "" {
		hookURL = "https://example.com/events" // placeholder; operator fills in after Lambda deploy
	}

	payload := githubManifestPayload{
		Name:   appName,
		URL:    "https://github.com/whereiskurt/klanker-maker",
		Public: true,
		DefaultPermissions: map[string]string{
			"issues":        "write",
			"pull_requests": "write",
			"contents":      "read",
			"checks":        "write",
		},
		DefaultEvents: []string{"issue_comment"},
		HookAttributes: map[string]interface{}{
			"url":    hookURL,
			"active": hookActive,
		},
	}

	// Banner to stderr — stdout stays pure JSON and pipeable.
	fmt.Fprintf(os.Stderr, "# GitHub App manifest for resource_prefix=%s\n", cfg.GetResourcePrefix())
	if opts.BridgeURL != "" {
		fmt.Fprintf(os.Stderr, "# Bridge URL: %s\n", opts.BridgeURL)
	} else {
		fmt.Fprintf(os.Stderr, "# Bridge URL: (not set — use --bridge-url or run `km github init` after Lambda deploy)\n")
	}
	fmt.Fprintf(os.Stderr, "# To install: paste output into GitHub → Settings → Developer settings → GitHub Apps → New → From manifest\n")

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func newGithubManifestCmd(cfg *config.Config) *cobra.Command {
	var appName, bridgeURL string
	c := &cobra.Command{
		Use:          "manifest",
		Short:        "Render a GitHub App manifest (JSON) for the bridge webhook",
		Long:         "Generates a GitHub App manifest with PR-review scopes (issues/pull_requests/contents/checks)\nand issue_comment webhook. Pipe to a file and paste into GitHub App creation UI:\n\n  km github manifest > app.json\n  # GitHub → Settings → Developer settings → GitHub Apps → New → From manifest\n\nWhen --bridge-url is provided, hook_attributes.url is set and active=true.\nOmit --bridge-url to create the App first, get the URL from `km init`, then run\n`km github init --bridge-url <URL>` to store it.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return RunGitHubManifest(ctx, cfg, GitHubManifestOpts{
				AppName:   appName,
				BridgeURL: bridgeURL,
			}, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&appName, "app-name", "", "Override auto-derived app name (default: km-{prefix}-github-bridge)")
	c.Flags().StringVar(&bridgeURL, "bridge-url", "", "Bridge Lambda URL to embed in hook_attributes.url (sets active=true)")
	return c
}

// ─────────────────────────────────────────────────────────────────────────────
// km github init
// ─────────────────────────────────────────────────────────────────────────────

// GitHubInitOpts holds parsed flag values for RunGitHubInit.
type GitHubInitOpts struct {
	// BotLogin is the GitHub App bot login handle (e.g. "klanker-maker[bot]").
	// When empty, a default is derived from the resource_prefix.
	BotLogin string
	// BridgeURL is the Lambda bridge URL stored at /{prefix}config/github/bridge-url.
	BridgeURL string
	// Force overwrites existing SSM parameters.
	Force bool
}

// RunGitHubInit is the exported handler for km github init.
// It generates a random webhook secret, caches bot-login, records bridge-url,
// and writes them to SSM. All three new keys are in addition to the existing
// configure_github.go keys (app-client-id / private-key / installation-id).
func RunGitHubInit(ctx context.Context, ssmClient SSMWriteAPI, cfg *config.Config, opts GitHubInitOpts, out io.Writer) error {
	ghPrefix := cfg.GetSsmPrefix() + "config/github/"
	overwrite := opts.Force

	// Generate random webhook secret (crypto/rand hex, 32 bytes = 64 hex chars).
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return fmt.Errorf("generate webhook secret: %w", err)
	}
	webhookSecret := hex.EncodeToString(secretBytes)

	// Write webhook-secret (SecureString).
	if err := putSSMParam(ctx, ssmClient, ghPrefix+"webhook-secret",
		webhookSecret, ssmtypes.ParameterTypeSecureString, "", overwrite); err != nil {
		return fmt.Errorf("writing webhook-secret to SSM: %w", err)
	}
	fmt.Fprintf(out, "Written: %swebhook-secret (SecureString)\n", ghPrefix)

	// Derive bot-login default when not provided.
	botLogin := opts.BotLogin
	if botLogin == "" {
		botLogin = fmt.Sprintf("%s-github-bridge[bot]", cfg.GetResourcePrefix())
	}

	// Write bot-login (String).
	if err := putSSMParam(ctx, ssmClient, ghPrefix+"bot-login",
		botLogin, ssmtypes.ParameterTypeString, "", overwrite); err != nil {
		return fmt.Errorf("writing bot-login to SSM: %w", err)
	}
	fmt.Fprintf(out, "Written: %sbot-login (%s)\n", ghPrefix, botLogin)

	// Write bridge-url (String) — may be empty string on first run before Lambda deploy.
	bridgeURL := opts.BridgeURL
	if err := putSSMParam(ctx, ssmClient, ghPrefix+"bridge-url",
		bridgeURL, ssmtypes.ParameterTypeString, "", overwrite); err != nil {
		return fmt.Errorf("writing bridge-url to SSM: %w", err)
	}
	if bridgeURL != "" {
		fmt.Fprintf(out, "Written: %sbridge-url (%s)\n", ghPrefix, bridgeURL)
	} else {
		fmt.Fprintf(out, "Written: %sbridge-url (empty — update after Lambda deploy with --bridge-url)\n", ghPrefix)
	}

	fmt.Fprintf(out, "GitHub bridge config stored. Run 'km github manifest --bridge-url %s' to generate the App manifest.\n", bridgeURL)
	return nil
}

func newGithubInitCmd(cfg *config.Config) *cobra.Command {
	var botLogin, bridgeURL string
	var force bool

	cmd := &cobra.Command{
		Use:          "init",
		Short:        "Generate webhook secret, cache bot-login, record bridge-url in SSM",
		Long:         "Generates a random webhook secret, caches the GitHub App bot login handle, and\nrecords the bridge Lambda URL in SSM Parameter Store.\n\nSSM keys written:\n  /{prefix}config/github/webhook-secret  — random hex secret (SecureString)\n  /{prefix}config/github/bot-login       — GitHub App bot handle (String)\n  /{prefix}config/github/bridge-url      — bridge Lambda URL (String)\n\nRun after 'km configure github --setup' to store the webhook config alongside\nthe App credentials. Re-run with --force to rotate the webhook secret.",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			// Initialise real SSM client.
			awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
			if err != nil {
				return fmt.Errorf("load AWS config: %w", err)
			}
			ssmClient := ssm.NewFromConfig(awsCfg)

			return RunGitHubInit(ctx, ssmClient, cfg, GitHubInitOpts{
				BotLogin:  botLogin,
				BridgeURL: bridgeURL,
				Force:     force,
			}, c.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&botLogin, "bot-login", "",
		"GitHub App bot login handle (default: km-{prefix}-github-bridge[bot])")
	cmd.Flags().StringVar(&bridgeURL, "bridge-url", "",
		"Bridge Lambda URL to store in SSM (set after `km init` provides the function URL)")
	cmd.Flags().BoolVar(&force, "force", false,
		"Overwrite existing SSM parameters (rotates webhook secret)")

	return cmd
}

// ─────────────────────────────────────────────────────────────────────────────
// km github status
// ─────────────────────────────────────────────────────────────────────────────

// RunGitHubStatus is the exported handler for km github status.
// It reads and prints the SSM-backed GitHub config, redacting the webhook secret.
func RunGitHubStatus(ctx context.Context, ssmClient GitHubSSMReadAPI, cfg *config.Config, out io.Writer) error {
	ghPrefix := cfg.GetSsmPrefix() + "config/github/"

	readParam := func(name string) string {
		result, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
			Name:           &name, // aws.String would need the import; use address-of local
			WithDecryption: boolPtr(true),
		})
		if err != nil {
			return "(not set)"
		}
		if result.Parameter == nil || result.Parameter.Value == nil {
			return "(not set)"
		}
		return *result.Parameter.Value
	}

	webhookSecret := readParam(ghPrefix + "webhook-secret")
	botLogin := readParam(ghPrefix + "bot-login")
	bridgeURL := readParam(ghPrefix + "bridge-url")

	// Redact secret: show [set] or (not set) only.
	secretDisplay := "(not set)"
	if webhookSecret != "(not set)" && webhookSecret != "" {
		secretDisplay = "[set]"
	}

	fmt.Fprintf(out, "GitHub bridge config (prefix: %s):\n", cfg.GetSsmPrefix())
	fmt.Fprintf(out, "  webhook-secret:  %s\n", secretDisplay)
	fmt.Fprintf(out, "  bot-login:       %s\n", botLogin)
	fmt.Fprintf(out, "  bridge-url:      %s\n", bridgeURL)

	// Also print the existing configure_github keys for convenience.
	appClientID := readParam(ghPrefix + "app-client-id")
	installID := readParam(ghPrefix + "installation-id")
	fmt.Fprintf(out, "  app-client-id:   %s\n", appClientID)
	fmt.Fprintf(out, "  installation-id: %s\n", installID)

	// Phase 99: list configured commands from SSM (dormant when absent).
	printGitHubCommandsStatus(ctx, ssmClient, cfg, ghPrefix, out)

	return nil
}

// printGitHubCommandsStatus reads the SSM commands param and prints the command
// listing + per-repo effective default to out. Dormant (no output) when the SSM
// param is absent or the commands map is empty.
func printGitHubCommandsStatus(ctx context.Context, ssmClient GitHubSSMReadAPI, cfg *config.Config, ghPrefix string, out io.Writer) {
	// Read the commands JSON doc from SSM.
	paramName := ghPrefix + "commands"
	result, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           &paramName,
		WithDecryption: boolPtr(false),
	})
	if err != nil {
		// Parameter absent or unreadable — dormant, no output.
		return
	}
	if result.Parameter == nil || result.Parameter.Value == nil {
		return
	}

	// Parse the JSON commands map.
	var commandsMap map[string]struct {
		Description string   `json:"description"`
		Alias       string   `json:"alias"`
		Profile     string   `json:"profile"`
		Allow       []string `json:"allow"`
		Prompt      string   `json:"prompt"`
	}
	if jsonErr := json.Unmarshal([]byte(*result.Parameter.Value), &commandsMap); jsonErr != nil {
		fmt.Fprintf(out, "  commands:        (parse error: %v)\n", jsonErr)
		return
	}
	if len(commandsMap) == 0 {
		return
	}

	// Print sorted command list.
	names := make([]string, 0, len(commandsMap))
	for k := range commandsMap {
		names = append(names, k)
	}
	sort.Strings(names)

	fmt.Fprintf(out, "  commands (%d):\n", len(commandsMap))
	for _, n := range names {
		e := commandsMap[n]
		target := ""
		switch {
		case e.Alias != "" && e.Profile != "":
			target = fmt.Sprintf("→ alias:%s profile:%s", e.Alias, e.Profile)
		case e.Alias != "":
			target = fmt.Sprintf("→ alias:%s", e.Alias)
		case e.Profile != "":
			target = fmt.Sprintf("→ profile:%s", e.Profile)
		}
		desc := e.Description
		if desc == "" {
			desc = "(no description)"
		}
		line := fmt.Sprintf("    /%s — %s", n, desc)
		if target != "" {
			line += " [" + target + "]"
		}
		fmt.Fprintln(out, line)
	}

	// Print install-wide default_command.
	installDefault := cfg.Github.DefaultCommand
	if installDefault != "" {
		fmt.Fprintf(out, "  default_command: %s (install-wide)\n", installDefault)
	}

	// Print per-repo effective default_command (where it differs from install-wide).
	repos := cfg.Github.Repos
	if len(repos) == 0 {
		return
	}

	hasPerRepoDefaults := false
	for _, r := range repos {
		if r.DefaultCommand != "" {
			hasPerRepoDefaults = true
			break
		}
	}

	if !hasPerRepoDefaults && installDefault == "" {
		return
	}

	fmt.Fprintf(out, "  repos (%d):\n", len(repos))
	for _, r := range repos {
		effective := r.DefaultCommand
		label := ""
		if effective != "" {
			label = " (per-repo)"
		} else if installDefault != "" {
			effective = installDefault
			label = " (install-wide fallback)"
		} else {
			effective = "(none — free-form passthrough)"
		}
		match := r.Match
		if strings.ContainsAny(match, "*?[") {
			// Trim trailing glob for display.
			match = strings.TrimSuffix(match, "/*") + "/*"
		}
		fmt.Fprintf(out, "    %-40s default_command: %s%s\n", match, effective, label)
	}
}

func newGithubStatusCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:          "status",
		Short:        "Print SSM-backed GitHub config (webhook secret redacted)",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			// Initialise real SSM client.
			awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
			if err != nil {
				return fmt.Errorf("load AWS config: %w", err)
			}
			ssmClient := ssm.NewFromConfig(awsCfg)

			return RunGitHubStatus(ctx, ssmClient, cfg, c.OutOrStdout())
		},
	}
}

