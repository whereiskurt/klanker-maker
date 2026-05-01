// Package cmd — slack.go
// Implements the "km slack" command group: init, test, status.
// init bootstraps the Slack integration (bot token, shared channel, bridge Lambda).
// test sends an end-to-end smoke test envelope through the bridge Lambda.
// status prints current SSM configuration for the Slack integration.
package cmd

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"

	"github.com/whereiskurt/klankrmkr/internal/app/config"
	"github.com/whereiskurt/klankrmkr/pkg/compiler"
	kmslack "github.com/whereiskurt/klankrmkr/pkg/slack"
	"github.com/whereiskurt/klankrmkr/pkg/terragrunt"
)

// ──────────────────────────────────────────────
// Injectable interfaces (for testability)
// ──────────────────────────────────────────────

// SlackInitAPI is the narrow Slack Web API surface needed by km slack init.
// *kmslack.Client satisfies this interface.
// (create_slack.go defines SlackAPI for km create; this adds AuthTest for bootstrap.)
type SlackInitAPI interface {
	AuthTest(ctx context.Context) error
	CreateChannel(ctx context.Context, name string) (string, error)
	InviteShared(ctx context.Context, channelID, email string) error
}

// SlackSSMStore is the narrow read/write SSM interface used by the slack commands.
type SlackSSMStore interface {
	Get(ctx context.Context, name string, decrypt bool) (string, error)
	Put(ctx context.Context, name, value string, secure bool) error
}

// SlackTerragruntRunner is the narrow Terragrunt interface used by km slack init.
type SlackTerragruntRunner interface {
	Apply(ctx context.Context, dir string) error
	Output(ctx context.Context, dir string) (map[string]interface{}, error)
}

// SlackPrompter collects input interactively from the operator.
type SlackPrompter interface {
	PromptString(label string) (string, error)
	PromptSecret(label string) (string, error)
}

// SlackCmdDeps bundles all injectable dependencies for the km slack command tree.
// Production: NewSlackCmd → buildSlackCmdDeps. Tests: construct directly with fakes.
type SlackCmdDeps struct {
	NewSlackAPI       func(token string) SlackInitAPI
	SSM               SlackSSMStore
	Terragrunt        SlackTerragruntRunner
	Prompter          SlackPrompter
	OperatorKeyLoader func(ctx context.Context, region string) (ed25519.PrivateKey, error)
	BridgePoster      func(ctx context.Context, bridgeURL string, env *kmslack.SlackEnvelope, sig []byte) (*kmslack.PostResponse, error)
	Region            string // short label, e.g. "use1"
	RepoRoot          string // absolute path to repo root
}

// SlackInitOpts holds parsed flag values for km slack init.
type SlackInitOpts struct {
	BotToken      string
	InviteEmail   string
	SharedChannel string
	Force         bool
}

// ──────────────────────────────────────────────
// Cobra command tree
// ──────────────────────────────────────────────

// NewSlackCmd creates the "km slack" parent command.
func NewSlackCmd(cfg *config.Config) *cobra.Command {
	return newSlackCmdInternal(cfg, nil)
}

// NewSlackCmdWithDeps creates a testable "km slack" command with injected deps.
func NewSlackCmdWithDeps(cfg *config.Config, deps *SlackCmdDeps) *cobra.Command {
	return newSlackCmdInternal(cfg, deps)
}

func newSlackCmdInternal(cfg *config.Config, deps *SlackCmdDeps) *cobra.Command {
	slackCmd := &cobra.Command{
		Use:          "slack",
		Short:        "Manage Slack-notify integration (Phase 63)",
		SilenceUsage: true,
	}
	slackCmd.AddCommand(newSlackInitCmd(cfg, deps))
	slackCmd.AddCommand(newSlackTestCmd(cfg, deps))
	slackCmd.AddCommand(newSlackStatusCmd(cfg, deps))
	return slackCmd
}

// ──────────────────────────────────────────────
// km slack init
// ──────────────────────────────────────────────

func newSlackInitCmd(cfg *config.Config, sharedDeps *SlackCmdDeps) *cobra.Command {
	var (
		botToken      string
		inviteEmail   string
		sharedChannel string
		force         bool
	)
	c := &cobra.Command{
		Use:          "init",
		Short:        "One-time bootstrap: provision shared channel, deploy bridge Lambda, write SSM params",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			deps := sharedDeps
			if deps == nil {
				var err error
				deps, err = buildSlackCmdDeps(cfg)
				if err != nil {
					return err
				}
			}
			return RunSlackInit(ctx, deps, SlackInitOpts{
				BotToken:      botToken,
				InviteEmail:   inviteEmail,
				SharedChannel: sharedChannel,
				Force:         force,
			})
		},
	}
	c.Flags().StringVar(&botToken, "bot-token", "", "Slack bot token (xoxb-...); skip prompt")
	c.Flags().StringVar(&inviteEmail, "invite-email", "", "Email to send Slack Connect invite")
	c.Flags().StringVar(&sharedChannel, "shared-channel", "km-notifications", "Shared channel name to create")
	c.Flags().BoolVar(&force, "force", false, "Re-create shared channel and re-apply Lambda even if already configured")
	return c
}

// RunSlackInit is the exported init logic (testable via dependency injection).
func RunSlackInit(ctx context.Context, d *SlackCmdDeps, opts SlackInitOpts) error {
	// Step 1: Resolve bot token.
	token := opts.BotToken
	if token == "" {
		if !opts.Force {
			existing, _ := d.SSM.Get(ctx, "/km/slack/bot-token", true)
			if existing != "" {
				token = existing
			}
		}
		if token == "" {
			t, err := d.Prompter.PromptSecret("Slack bot token (xoxb-...): ")
			if err != nil {
				return fmt.Errorf("prompt for bot token: %w", err)
			}
			token = strings.TrimSpace(t)
		}
	}

	// Step 2: Validate via auth.test.
	api := d.NewSlackAPI(token)
	if err := api.AuthTest(ctx); err != nil {
		return fmt.Errorf("invalid Slack bot token: %w", err)
	}

	// Step 3: Persist token (SecureString).
	if err := d.SSM.Put(ctx, "/km/slack/bot-token", token, true); err != nil {
		return fmt.Errorf("store bot token: %w", err)
	}

	// Step 4: Invite email.
	inv := opts.InviteEmail
	if inv == "" {
		if !opts.Force {
			existing, _ := d.SSM.Get(ctx, "/km/slack/invite-email", false)
			if existing != "" {
				inv = existing
			}
		}
		if inv == "" {
			v, err := d.Prompter.PromptString("Email for Slack Connect invite: ")
			if err != nil {
				return fmt.Errorf("prompt for invite email: %w", err)
			}
			inv = strings.TrimSpace(v)
		}
	}
	if err := d.SSM.Put(ctx, "/km/slack/invite-email", inv, false); err != nil {
		return fmt.Errorf("store invite-email: %w", err)
	}

	// Step 5: Shared channel — create if not provisioned or --force.
	chName := opts.SharedChannel
	if chName == "" {
		chName = "km-notifications"
	}
	chID, _ := d.SSM.Get(ctx, "/km/slack/shared-channel-id", false)
	if chID == "" || opts.Force {
		newID, err := api.CreateChannel(ctx, chName)
		if err != nil {
			// --force is idempotent: if the channel already exists, keep the stored ID
			// and re-apply the rest (Lambda redeploy, token rotation). Slack does not
			// allow renaming back to an existing #name, so reuse beats fail.
			var apierr *kmslack.SlackAPIError
			if opts.Force && chID != "" && errors.As(err, &apierr) && apierr.Code == "name_taken" {
				fmt.Fprintf(os.Stderr, "shared channel %q already exists (%s); reusing\n", chName, chID)
			} else {
				return fmt.Errorf("create shared channel %q: %w", chName, err)
			}
		} else {
			chID = newID
			if err := d.SSM.Put(ctx, "/km/slack/shared-channel-id", chID, false); err != nil {
				return fmt.Errorf("store shared-channel-id: %w", err)
			}
			if inv != "" {
				if invErr := api.InviteShared(ctx, chID, inv); invErr != nil {
					if isSlackProWorkspaceError(invErr) {
						return fmt.Errorf("send Slack Connect invite to %s: requires a Pro Slack workspace (error: %w)", inv, invErr)
					}
					return fmt.Errorf("send Slack Connect invite to %s (workspace must be Pro): %w", inv, invErr)
				}
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "shared channel already provisioned (%s); use --force to recreate\n", chID)
	}

	// Step 6: Deploy dynamodb-slack-nonces + lambda-slack-bridge, then read function_url.
	bridgeURL, _ := d.SSM.Get(ctx, "/km/slack/bridge-url", false)
	if bridgeURL == "" || opts.Force {
		noncesDir := filepath.Join(d.RepoRoot, "infra", "live", d.Region, "dynamodb-slack-nonces")
		bridgeDir := filepath.Join(d.RepoRoot, "infra", "live", d.Region, "lambda-slack-bridge")
		for _, dir := range []string{noncesDir, bridgeDir} {
			if err := d.Terragrunt.Apply(ctx, dir); err != nil {
				return fmt.Errorf("terragrunt apply %s: %w", filepath.Base(dir), err)
			}
		}
		outputMap, err := d.Terragrunt.Output(ctx, bridgeDir)
		if err != nil {
			return fmt.Errorf("read lambda-slack-bridge outputs: %w", err)
		}
		url := slackExtractValue(outputMap["function_url"])
		if url == "" {
			return errors.New("function_url not found in lambda-slack-bridge Terraform output")
		}
		bridgeURL = url
		if err := d.SSM.Put(ctx, "/km/slack/bridge-url", bridgeURL, false); err != nil {
			return fmt.Errorf("store bridge-url: %w", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "bridge Lambda already deployed (%s); use --force to re-apply\n", bridgeURL)
	}

	fmt.Println("km slack init: complete.")
	fmt.Printf("  shared channel: %s\n", chID)
	fmt.Printf("  bridge URL:     %s\n", bridgeURL)
	fmt.Printf("  invite sent to: %s\n", inv)
	return nil
}

// isSlackProWorkspaceError returns true for Slack Connect errors that indicate
// the workspace tier is insufficient (requires Pro or higher).
func isSlackProWorkspaceError(err error) bool {
	var apiErr *kmslack.SlackAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Code {
	case "not_allowed_token_type", "org_login_required":
		return true
	}
	return false
}

// ──────────────────────────────────────────────
// km slack test
// ──────────────────────────────────────────────

func newSlackTestCmd(cfg *config.Config, sharedDeps *SlackCmdDeps) *cobra.Command {
	c := &cobra.Command{
		Use:          "test",
		Short:        "Send a test envelope through the bridge Lambda to verify end-to-end delivery",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			deps := sharedDeps
			if deps == nil {
				var err error
				deps, err = buildSlackCmdDeps(cfg)
				if err != nil {
					return err
				}
			}
			return RunSlackTest(ctx, deps, cmd.OutOrStdout())
		},
	}
	return c
}

// RunSlackTest is the exported test logic (testable via dependency injection).
func RunSlackTest(ctx context.Context, d *SlackCmdDeps, w io.Writer) error {
	bridgeURL, _ := d.SSM.Get(ctx, "/km/slack/bridge-url", false)
	if bridgeURL == "" {
		return errors.New("/km/slack/bridge-url not set; run km slack init first")
	}
	chID, _ := d.SSM.Get(ctx, "/km/slack/shared-channel-id", false)
	if chID == "" {
		return errors.New("/km/slack/shared-channel-id not set; run km slack init first")
	}
	priv, err := d.OperatorKeyLoader(ctx, d.Region)
	if err != nil {
		return fmt.Errorf("load operator key: %w", err)
	}
	env, err := kmslack.BuildEnvelope(
		kmslack.ActionTest, kmslack.SenderOperator, chID,
		"km slack test", "If you see this, the bridge is wired.", "",
	)
	if err != nil {
		return fmt.Errorf("build envelope: %w", err)
	}
	_, sig, err := kmslack.SignEnvelope(env, priv)
	if err != nil {
		return fmt.Errorf("sign envelope: %w", err)
	}
	resp, err := d.BridgePoster(ctx, bridgeURL, env, sig)
	if err != nil {
		return fmt.Errorf("post to bridge: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("bridge returned not-ok: %s", resp.Error)
	}
	fmt.Fprintf(w, "km slack test: posted ts=%s\n", resp.TS)
	_ = d.SSM.Put(ctx, "/km/slack/last-test-timestamp", time.Now().UTC().Format(time.RFC3339), false)
	return nil
}

// ──────────────────────────────────────────────
// km slack status
// ──────────────────────────────────────────────

func newSlackStatusCmd(cfg *config.Config, sharedDeps *SlackCmdDeps) *cobra.Command {
	c := &cobra.Command{
		Use:          "status",
		Short:        "Print current Slack integration configuration from SSM",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			deps := sharedDeps
			if deps == nil {
				var err error
				deps, err = buildSlackCmdDeps(cfg)
				if err != nil {
					return err
				}
			}
			return RunSlackStatus(ctx, deps, cmd.OutOrStdout())
		},
	}
	return c
}

// RunSlackStatus is the exported status logic (testable via dependency injection).
func RunSlackStatus(ctx context.Context, d *SlackCmdDeps, w io.Writer) error {
	keys := []string{
		"/km/slack/workspace",
		"/km/slack/shared-channel-id",
		"/km/slack/invite-email",
		"/km/slack/bridge-url",
		"/km/slack/last-test-timestamp",
	}
	fmt.Fprintf(w, "%-45s %s\n", "Key", "Value")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 80))
	for _, k := range keys {
		v, _ := d.SSM.Get(ctx, k, false)
		if v == "" {
			v = "(unset)"
		}
		fmt.Fprintf(w, "%-45s %s\n", k, v)
	}
	return nil
}

// ──────────────────────────────────────────────
// Production wiring
// ──────────────────────────────────────────────

// slackSSMStore wraps *ssm.Client to satisfy SlackSSMStore.
type slackSSMStore struct {
	client *ssm.Client
	kmsKey string
}

func (s *slackSSMStore) Get(ctx context.Context, name string, decrypt bool) (string, error) {
	out, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(decrypt),
	})
	if err != nil {
		var notFound *ssmtypes.ParameterNotFound
		if errors.As(err, &notFound) {
			return "", nil
		}
		return "", fmt.Errorf("SSM GetParameter %s: %w", name, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", nil
	}
	return *out.Parameter.Value, nil
}

func (s *slackSSMStore) Put(ctx context.Context, name, value string, secure bool) error {
	input := &ssm.PutParameterInput{
		Name:      aws.String(name),
		Value:     aws.String(value),
		Type:      ssmtypes.ParameterTypeString,
		Overwrite: aws.Bool(true),
	}
	if secure {
		input.Type = ssmtypes.ParameterTypeSecureString
		if s.kmsKey != "" {
			input.KeyId = aws.String(s.kmsKey)
		}
	}
	_, err := s.client.PutParameter(ctx, input)
	if err != nil {
		return fmt.Errorf("SSM PutParameter %s: %w", name, err)
	}
	return nil
}

// slackPrompter reads from stdin.
type slackPrompter struct{}

func (p *slackPrompter) PromptString(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	var v string
	if _, err := fmt.Scanln(&v); err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	return strings.TrimSpace(v), nil
}

func (p *slackPrompter) PromptSecret(label string) (string, error) {
	return p.PromptString(label)
}

// slackTerragruntRunner wraps *terragrunt.Runner to satisfy SlackTerragruntRunner.
type slackTerragruntRunner struct {
	inner *terragrunt.Runner
}

func (r *slackTerragruntRunner) Apply(ctx context.Context, dir string) error {
	return r.inner.Apply(ctx, dir)
}

func (r *slackTerragruntRunner) Output(ctx context.Context, dir string) (map[string]interface{}, error) {
	return r.inner.Output(ctx, dir)
}

// buildSlackCmdDeps wires production AWS / Slack / Terragrunt / prompter implementations.
func buildSlackCmdDeps(cfg *config.Config) (*SlackCmdDeps, error) {
	ctx := context.Background()
	awsProfile := cfg.AWSProfile

	region := cfg.PrimaryRegion
	if region == "" {
		region = "us-east-1"
	}
	regionLabel := compiler.RegionLabel(region)

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithSharedConfigProfile(awsProfile),
		awsconfig.WithRegion(region),
	)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	repoRoot := findRepoRoot()
	kmsKey := os.Getenv("KM_PLATFORM_KMS_KEY_ARN")
	if kmsKey == "" {
		kmsKey = "alias/km-platform"
	}

	ssmClient := ssm.NewFromConfig(awsCfg)
	tgRunner := terragrunt.NewRunner(awsProfile, repoRoot)

	return &SlackCmdDeps{
		NewSlackAPI: func(token string) SlackInitAPI {
			return kmslack.NewClient(token, nil)
		},
		SSM:        &slackSSMStore{client: ssmClient, kmsKey: kmsKey},
		Terragrunt: &slackTerragruntRunner{inner: tgRunner},
		Prompter:   &slackPrompter{},
		OperatorKeyLoader: func(ctx context.Context, _ string) (ed25519.PrivateKey, error) {
			return loadSlackOperatorKey(ctx, ssmClient)
		},
		BridgePoster: kmslack.PostToBridge,
		Region:       regionLabel,
		RepoRoot:     repoRoot,
	}, nil
}

// loadSlackOperatorKey fetches /sandbox/operator/signing-key from SSM (same path
// as km email --from operator), base64-decodes it, and returns an Ed25519 private key.
func loadSlackOperatorKey(ctx context.Context, ssmClient *ssm.Client) (ed25519.PrivateKey, error) {
	const keyPath = "/sandbox/operator/signing-key"
	out, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(keyPath),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("SSM GetParameter %s: %w", keyPath, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return nil, fmt.Errorf("SSM parameter %s has no value", keyPath)
	}
	raw, err := base64.StdEncoding.DecodeString(*out.Parameter.Value)
	if err != nil {
		return nil, fmt.Errorf("decode operator key: %w", err)
	}
	if len(raw) < 32 {
		return nil, fmt.Errorf("operator key too short: %d bytes (want >=32)", len(raw))
	}
	return ed25519.NewKeyFromSeed(raw[:32]), nil
}

// slackExtractValue extracts the "value" field from a Terraform output map entry.
// Mirrors extractValue in init.go.
func slackExtractValue(v interface{}) string {
	if v == nil {
		return ""
	}
	if m, ok := v.(map[string]interface{}); ok {
		if val, exists := m["value"]; exists {
			return fmt.Sprintf("%v", val)
		}
	}
	return fmt.Sprintf("%v", v)
}

// slackWorkspaceMeta is persisted to /km/slack/workspace as JSON.
type slackWorkspaceMeta struct {
	TeamID   string `json:"team_id"`
	TeamName string `json:"team_name"`
}

// marshalSlackWorkspace serializes workspace metadata to JSON.
func marshalSlackWorkspace(teamID, teamName string) string {
	b, err := json.Marshal(slackWorkspaceMeta{TeamID: teamID, TeamName: teamName})
	if err != nil {
		return "{}"
	}
	return string(b)
}
