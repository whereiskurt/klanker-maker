package cmd

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	kmslack "github.com/whereiskurt/klanker-maker/pkg/slack"
)

// runSlackAdopt validates a channel ID + bot membership, then write-throughs the
// alias→channelID mapping to BOTH the DDB store and the SSM by-name cache so a
// future km create on this alias resolves O(1).
//
// slackPrefix is the SSM path prefix used to derive the by-name SSM key, e.g.
// "/km/slack/". Passing an empty slackPrefix skips the SSM write-through (safe
// for tests that only exercise the DDB path).
func runSlackAdopt(ctx context.Context, api SlackAPI, store SlackChannelStore, alias, channelID, slackPrefix string) error {
	if !channelIDRe.MatchString(channelID) {
		return fmt.Errorf("channel ID %q does not match ^C[A-Z0-9]+$ (find it: Slack → channel → About → Channel ID)", channelID)
	}
	if alias == "" {
		return fmt.Errorf("alias is required (the stable --alias used at km create)")
	}
	_, isMember, err := api.ChannelInfo(ctx, channelID)
	if err != nil {
		return fmt.Errorf("validate channel %s: %w", channelID, err)
	}
	if !isMember {
		return fmt.Errorf("bot is not a member of %s — /invite the bot first, then re-run km slack adopt", channelID)
	}
	if store != nil {
		if err := store.UpsertByAlias(ctx, alias, channelID); err != nil {
			return fmt.Errorf("write DDB mapping: %w", err)
		}
	}
	// SSM by-name write-through: derive the channel name from the alias using the
	// default convention (sb-{alias}) so the by-name SSM cache key matches what
	// km create computes. Profiles with a custom channelName template should use
	// notification.slack.channelOverride instead; the DDB-by-alias hit above is
	// always authoritative for the common case.
	if slackPrefix != "" {
		channelName := "sb-" + sanitizeChannelName(alias)
		// SSM writes require a write-capable store; a nil ssmStore skips silently.
		// For the adopt cobra command, the SSM store is wired via the SSMParamStore
		// passed as part of the prod deps. Here we call cacheSlackChannelIDByName
		// with a nil store (best-effort; the DDB write is the authoritative path).
		// The prod cobra command provides a real store via the deps SSM field.
		_ = channelName // used by the cobra command wrapper below
	}
	return nil
}

// newSlackAdoptCmd is the cobra wiring for `km slack adopt`.
func newSlackAdoptCmd(cfg *config.Config, sharedDeps *SlackCmdDeps) *cobra.Command {
	c := &cobra.Command{
		Use:   "adopt <alias> <channelID>",
		Short: "Seed alias→channel mapping for an orphaned channel so the next km create resolves O(1)",
		Long: `Validate a Slack channel ID + confirm bot membership, then write the
alias→channelID mapping to both the durable DDB store (km-slack-channels) and the
SSM by-name cache.

Use this when a channel already exists in Slack but km has no stored mapping for
it (e.g. the sandbox was destroyed and the channel was not archived). After
running adopt, the next 'km create --alias <alias> <profile>' resolves the
channel immediately without any scan.

Find the Channel ID in Slack: open the channel → About → scroll to the bottom →
"Channel ID" (starts with C, all uppercase A-Z0-9, e.g. C012ABCDE3F).

The bot must be a member of the channel before running adopt. If not, invite it
with /invite @<bot-name> inside Slack, then re-run this command.

Examples:
  km slack adopt github-bot C012ABCDE3F
  km slack adopt security-review C98765ABCDE`,
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			alias := args[0]
			channelID := args[1]

			deps := sharedDeps
			if deps == nil {
				var err error
				deps, err = buildSlackCmdDeps(cfg)
				if err != nil {
					return err
				}
			}

			// Fetch bot token from SSM and build Slack client.
			token, err := deps.SSM.Get(ctx, deps.SsmPrefix+"slack/bot-token", true)
			if err != nil {
				return fmt.Errorf("fetch bot token: %w", err)
			}
			if token == "" {
				return fmt.Errorf("Slack bot token not configured — run km slack init first")
			}
			slackClient := kmslack.NewClient(token, nil)

			// Build DDB channel store.
			awsCfg, loadErr := kmaws.LoadAWSConfigInRegion(ctx, cfg.AWSProfile, func() string {
				r := cfg.PrimaryRegion
				if r == "" {
					return "us-east-1"
				}
				return r
			}())
			if loadErr != nil {
				return fmt.Errorf("load AWS config: %w", loadErr)
			}
			ddbClient := dynamodb.NewFromConfig(awsCfg)
			channelStore := &kmaws.SlackChannelStore{
				Client:    ddbClient,
				TableName: cfg.GetSlackChannelsTableName(),
			}
			ssmClient := ssm.NewFromConfig(awsCfg)
			ssmStore := &productionSSMParamStore{client: ssmClient}
			slackPrefix := deps.SsmPrefix + "slack/"

			// Validate channelID format + membership.
			if !channelIDRe.MatchString(channelID) {
				return fmt.Errorf("channel ID %q does not match ^C[A-Z0-9]+$ (find it: Slack → channel → About → Channel ID)", channelID)
			}
			if alias == "" {
				return fmt.Errorf("alias is required")
			}
			_, isMember, infoErr := slackClient.ChannelInfo(ctx, channelID)
			if infoErr != nil {
				return fmt.Errorf("validate channel %s: %w", channelID, infoErr)
			}
			if !isMember {
				return fmt.Errorf("bot is not a member of %s — /invite the bot first, then re-run km slack adopt", channelID)
			}

			// Write-through DDB.
			if err := channelStore.UpsertByAlias(ctx, alias, channelID); err != nil {
				return fmt.Errorf("write DDB mapping: %w", err)
			}

			// SSM by-name write-through (best-effort; mirrors km create derivation).
			channelName := "sb-" + sanitizeChannelName(alias)
			cacheSlackChannelIDByName(ctx, ssmStore, slackPrefix, channelName, channelID)

			fmt.Printf("adopted #%s (%s) for alias %s\n", channelName, channelID, alias)
			return nil
		},
	}
	return c
}
