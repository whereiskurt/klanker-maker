package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	kmslack "github.com/whereiskurt/klanker-maker/pkg/slack"
	slackbridge "github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// ──────────────────────────────────────────────
// Injectable interfaces
// ──────────────────────────────────────────────

// SlackPostAPI is the narrow Slack client surface needed by km slack reply.
// *kmslack.Client satisfies this interface.
type SlackPostAPI interface {
	PostMessage(ctx context.Context, channel, subject, body, threadTS string) (string, error)
}

// SlackThreadLookupAPI is the narrow DDB interface needed by km slack reply
// for session→(channel, thread) resolution via the session-index GSI.
// *slackbridge.DDBThreadStore satisfies this interface.
type SlackThreadLookupAPI interface {
	LookupBySession(ctx context.Context, sessionID, sandboxID string) (channelID, threadTS, agentType string, err error)
}

// ──────────────────────────────────────────────
// Options + core logic
// ──────────────────────────────────────────────

// SlackReplyOpts holds parsed flag values for km slack reply.
type SlackReplyOpts struct {
	// --thread+--channel: verbatim post (no GSI query)
	Thread  string
	Channel string

	// --session: query session-index GSI directly (operator AWS creds)
	Session string

	// SandboxChannel is the resolved channel for the channel-root fallback.
	// Populated from --sandbox/--alias channel resolution (or sandbox DDB lookup).
	// When set and the session GSI misses, RunSlackReply posts a top-level message.
	SandboxChannel string

	// Message body + render mode
	Body   string
	Render string // "plain" | "mrkdwn" | "blocks"

	// Subject is optional (produces a bold header)
	Subject string
}

// RunSlackReply is the exported, testable operator reply logic.
// Resolution order (first-hit wins):
//  1. --thread + --channel  → post verbatim into that thread
//  2. --session             → Query session-index GSI; on hit post to (channel_id, thread_ts)
//  3. SandboxChannel        → top-level post to the sandbox's bound channel (fallback)
//  4. none resolved         → error
//
// Posts via slackPost.PostMessage (bot token client).
func RunSlackReply(ctx context.Context, slackPost SlackPostAPI, threadLookup SlackThreadLookupAPI, opts SlackReplyOpts) error {
	if strings.TrimSpace(opts.Body) == "" {
		return fmt.Errorf("km slack reply: body is required; use --body to provide message content")
	}

	var (
		targetChannel string
		targetThread  string
	)

	switch {
	case opts.Thread != "" || opts.Channel != "":
		// Rule 1: verbatim thread+channel
		if opts.Thread == "" || opts.Channel == "" {
			return fmt.Errorf("km slack reply: --thread and --channel must both be set for verbatim post (got thread=%q channel=%q)", opts.Thread, opts.Channel)
		}
		targetChannel = opts.Channel
		targetThread = opts.Thread

	case opts.Session != "":
		// Rule 2: session → GSI lookup
		if threadLookup != nil {
			// sandboxID is empty on operator side — LookupBySession ignores it when ""
			// (operator queries for any sandbox's session, filtered only by session ID)
			chID, tsID, _, err := threadLookup.LookupBySession(ctx, opts.Session, "")
			if err != nil {
				return fmt.Errorf("km slack reply: GSI lookup for session %q: %w", opts.Session, err)
			}
			if chID != "" && tsID != "" {
				targetChannel = chID
				targetThread = tsID
			}
		}
		// GSI miss → fall through to sandbox-channel fallback below
		if targetChannel == "" && opts.SandboxChannel != "" {
			targetChannel = opts.SandboxChannel
			targetThread = "" // top-level post
		}

	case opts.SandboxChannel != "":
		// Rule 3: channel-root fallback
		targetChannel = opts.SandboxChannel
		targetThread = ""
	}

	if targetChannel == "" {
		return fmt.Errorf("km slack reply: no thread or channel resolved; pass --thread+--channel, --session, or --sandbox/--alias")
	}

	_, err := slackPost.PostMessage(ctx, targetChannel, opts.Subject, opts.Body, targetThread)
	if err != nil {
		return fmt.Errorf("km slack reply: PostMessage to %s: %w", targetChannel, err)
	}
	return nil
}

// ──────────────────────────────────────────────
// Cobra command
// ──────────────────────────────────────────────

// newSlackReplyCmd creates the "km slack reply" subcommand.
func newSlackReplyCmd(cfg *config.Config, sharedDeps *SlackCmdDeps) *cobra.Command {
	var (
		thread         string
		channel        string
		session        string
		sandbox        string
		alias          string
		body           string
		render         string
		subject        string
	)

	c := &cobra.Command{
		Use:   "reply",
		Short: "Post a message into a session's thread or a sandbox channel",
		Long: `Post a message using one of three resolution modes:

  --session <id>              Query the session-index GSI directly (operator AWS creds,
                              no bridge) → post into the session's bound thread.
  --thread <ts> --channel <C> Post verbatim into that thread/channel.
  --sandbox <id>              Resolve the sandbox's channel and post as top-level.
  --alias <name>              Resolve by alias and post as top-level.

Flags --body and --render are required. --render accepts plain (default),
mrkdwn, or blocks (raw JSON).

Examples:
  km slack reply --session abc-123 --body /tmp/msg.md
  km slack reply --thread 1234567890.000001 --channel C01ABC --body /tmp/msg.md
  km slack reply --sandbox km-abc123 --body /tmp/msg.md`,
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

			// Resolve body from file.
			if body == "" {
				return fmt.Errorf("km slack reply: --body is required")
			}
			bodyContent, err := readBodyFile(body)
			if err != nil {
				return fmt.Errorf("km slack reply: read --body file: %w", err)
			}

			// Get bot token from SSM and build Slack client (for PostMessage).
			token, err := deps.SSM.Get(ctx, deps.SsmPrefix+"slack/bot-token", true)
			if err != nil {
				return fmt.Errorf("km slack reply: fetch bot token: %w", err)
			}
			if token == "" {
				return fmt.Errorf("km slack reply: Slack bot token not configured — run km slack init first")
			}
			slackClient := kmslack.NewClient(token, nil)

			// Build DDB thread lookup for --session mode.
			// Use deps.ThreadLookup when pre-wired (e.g. via buildSlackCmdDeps),
			// otherwise build from config (e.g. when deps were partially injected).
			var threadLookup SlackThreadLookupAPI
			if deps.ThreadLookup != nil {
				threadLookup = deps.ThreadLookup
			} else {
				awsCfg, loadErr := kmaws.LoadAWSConfigInRegion(ctx, cfg.AWSProfile, func() string {
					r := cfg.PrimaryRegion
					if r == "" {
						return "us-east-1"
					}
					return r
				}())
				if loadErr == nil {
					ddbClient := dynamodb.NewFromConfig(awsCfg)
					threadLookup = &slackbridge.DDBThreadStore{
						Client:    ddbClient,
						TableName: cfg.GetSlackThreadsTableName(),
					}
				}
			}

			// Resolve SandboxChannel for --sandbox / --alias fallback.
			sandboxChannel := ""
			if sandbox != "" || alias != "" {
				sandboxChannel = resolveSandboxChannelForReply(ctx, cfg, sandbox, alias, deps)
			}

			// Override with explicit --channel if passed alongside --session or --sandbox.
			if channel != "" && sandbox == "" && alias == "" && session == "" {
				// channel without thread → treat as sandbox channel root fallback
				sandboxChannel = channel
			}

			opts := SlackReplyOpts{
				Thread:         thread,
				Channel:        channel,
				Session:        session,
				SandboxChannel: sandboxChannel,
				Body:           bodyContent,
				Render:         render,
				Subject:        subject,
			}
			if err := RunSlackReply(ctx, slackClient, threadLookup, opts); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "km slack reply: posted.")
			return nil
		},
	}

	c.Flags().StringVar(&session, "session", "", "Session ID to resolve via session-index GSI")
	c.Flags().StringVar(&thread, "thread", "", "Thread timestamp for verbatim post (requires --channel)")
	c.Flags().StringVar(&channel, "channel", "", "Channel ID for verbatim post (requires --thread)")
	c.Flags().StringVar(&sandbox, "sandbox", "", "Sandbox ID to resolve channel for root fallback")
	c.Flags().StringVar(&alias, "alias", "", "Alias to resolve channel for root fallback")
	c.Flags().StringVar(&body, "body", "", "Path to message body file (required)")
	c.Flags().StringVar(&render, "render", "plain", "Render mode: plain | mrkdwn | blocks")
	c.Flags().StringVar(&subject, "subject", "", "Optional bold header prepended to the message")
	return c
}

// readBodyFile reads the message body from a file path.
// If path is "-", it reads from stdin (future extension; not needed for MVP).
func readBodyFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %q: %w", path, err)
	}
	return strings.TrimRight(string(data), "\n"), nil
}

// resolveSandboxChannelForReply resolves the Slack channel ID for a given
// sandbox ID or alias, to be used as the root-fallback post target.
// Returns "" if resolution fails (caller logs and continues).
func resolveSandboxChannelForReply(ctx context.Context, cfg *config.Config, sandboxID, alias string, deps *SlackCmdDeps) string {
	if deps == nil {
		return ""
	}
	// Try SSM by-name cache using the alias convention.
	key := alias
	if key == "" {
		key = sandboxID
	}
	if key == "" {
		return ""
	}
	ssmKey := deps.SsmPrefix + "slack/channel-id-by-name/sb-" + sanitizeChannelName(key)
	chID, err := deps.SSM.Get(ctx, ssmKey, false)
	if err != nil || chID == "" {
		return ""
	}
	return chID
}
