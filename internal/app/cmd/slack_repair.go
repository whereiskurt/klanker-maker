package cmd

// slack_repair.go — operator-side cleanup/repair commands for stale Slack
// thread and channel DDB rows.
//
//  km slack threads <sandbox-id|--alias>  — list km-slack-threads rows
//  km slack forget-thread (--session | --thread+--channel) — delete a row
//  km slack prune-threads [sandbox] [--dry-run] — validate via Slack API
//  km slack forget-channel <alias>        — delete km-slack-channels row
//
// All commands use operator AWS creds (local profile) — no bridge IAM change.

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	kmslack "github.com/whereiskurt/klanker-maker/pkg/slack"
)

// ──────────────────────────────────────────────────────────────────────────────
// Injectable interfaces
// ──────────────────────────────────────────────────────────────────────────────

// DDBRepairAPI is the narrow DynamoDB surface needed by the repair commands.
// *dynamodb.Client satisfies this interface.
type DDBRepairAPI interface {
	Scan(ctx context.Context, in *dynamodb.ScanInput, opts ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
}

// SlackChannelChecker validates whether a Slack channel still exists.
// The only definitive dead-channel signal is "channel_not_found"; all other
// errors are treated as transient and do NOT mark the row for deletion.
type SlackChannelChecker interface {
	IsChannelDead(ctx context.Context, channelID string) (bool, error)
}

// ──────────────────────────────────────────────────────────────────────────────
// slackClientChannelChecker wraps *kmslack.Client for prod use
// ──────────────────────────────────────────────────────────────────────────────

type slackClientChannelChecker struct {
	client *kmslack.Client
}

func (c *slackClientChannelChecker) IsChannelDead(ctx context.Context, channelID string) (bool, error) {
	_, _, err := c.client.ChannelInfo(ctx, channelID)
	if err == nil {
		return false, nil
	}
	if kmslack.IsChannelNotFound(err) {
		return true, nil
	}
	// Transient error: return (false, err) so caller skips deletion.
	return false, err
}

// ──────────────────────────────────────────────────────────────────────────────
// ThreadRow: result of RunSlackThreads / RunSlackPruneThreads
// ──────────────────────────────────────────────────────────────────────────────

// ThreadRow represents a single km-slack-threads row returned by the repair commands.
type ThreadRow struct {
	ChannelID  string
	ThreadTS   string
	SandboxID  string
	SessionID  string
	AgentType  string
	LastTurnTS string
}

// ──────────────────────────────────────────────────────────────────────────────
// RunSlackThreads — list thread rows by sandbox_id
// ──────────────────────────────────────────────────────────────────────────────

// RunSlackThreads scans km-slack-threads with FilterExpression sandbox_id = :sid.
// O(n) Scan is documented and acceptable for operator repair tooling.
// sandboxID may be empty to list all rows (omits the filter expression).
func RunSlackThreads(ctx context.Context, ddb DDBRepairAPI, tableName, sandboxID string) ([]ThreadRow, error) {
	in := &dynamodb.ScanInput{
		TableName: aws.String(tableName),
	}
	if sandboxID != "" {
		in.FilterExpression = aws.String("sandbox_id = :sid")
		in.ExpressionAttributeValues = map[string]dynamodbtypes.AttributeValue{
			":sid": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		}
	}

	out, err := ddb.Scan(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("km slack threads: scan: %w", err)
	}

	rows := make([]ThreadRow, 0, len(out.Items))
	for _, item := range out.Items {
		row := threadRowFromItem(item)
		rows = append(rows, row)
	}
	return rows, nil
}

// threadRowFromItem extracts a ThreadRow from a DDB attribute map.
func threadRowFromItem(item map[string]dynamodbtypes.AttributeValue) ThreadRow {
	r := ThreadRow{}
	if v, ok := item["channel_id"].(*dynamodbtypes.AttributeValueMemberS); ok {
		r.ChannelID = v.Value
	}
	if v, ok := item["thread_ts"].(*dynamodbtypes.AttributeValueMemberS); ok {
		r.ThreadTS = v.Value
	}
	if v, ok := item["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS); ok {
		r.SandboxID = v.Value
	}
	if v, ok := item["claude_session_id"].(*dynamodbtypes.AttributeValueMemberS); ok {
		r.SessionID = v.Value
	}
	if v, ok := item["agent_type"].(*dynamodbtypes.AttributeValueMemberS); ok {
		r.AgentType = v.Value
	}
	if v, ok := item["last_turn_ts"].(*dynamodbtypes.AttributeValueMemberS); ok {
		r.LastTurnTS = v.Value
	}
	return r
}

// ──────────────────────────────────────────────────────────────────────────────
// ForgetThreadOpts + RunSlackForgetThread
// ──────────────────────────────────────────────────────────────────────────────

// ForgetThreadOpts holds parsed flag values for km slack forget-thread.
type ForgetThreadOpts struct {
	// Exactly one resolution mode must be set:
	Session   string // --session <id>: Query session-index GSI → (channel_id, thread_ts)
	ThreadTS  string // --thread <ts> (requires ChannelID)
	ChannelID string // --channel <id> (requires ThreadTS)
	Yes       bool   // --yes: skip confirmation
}

// RunSlackForgetThread deletes a row from km-slack-threads.
//
// Resolution:
//   - --session: Query session-index GSI → (channel_id, thread_ts) → DeleteItem
//   - --thread+--channel: DeleteItem directly
//
// Exactly one resolution mode must be provided.
func RunSlackForgetThread(ctx context.Context, ddb DDBRepairAPI, tableName string, opts ForgetThreadOpts) error {
	hasDirect := opts.ThreadTS != "" || opts.ChannelID != ""
	hasSession := opts.Session != ""

	if !hasDirect && !hasSession {
		return fmt.Errorf("km slack forget-thread: provide --session or both --thread and --channel")
	}
	if hasDirect && hasSession {
		return fmt.Errorf("km slack forget-thread: --session and --thread/--channel are mutually exclusive")
	}

	var channelID, threadTS string

	if hasSession {
		// Resolve session → (channel_id, thread_ts) via session-index GSI.
		ch, ts, err := resolveSessionToThread(ctx, ddb, tableName, opts.Session)
		if err != nil {
			return fmt.Errorf("km slack forget-thread: GSI query: %w", err)
		}
		if ch == "" || ts == "" {
			return fmt.Errorf("km slack forget-thread: session %q not found in %s", opts.Session, tableName)
		}
		channelID = ch
		threadTS = ts
	} else {
		if opts.ThreadTS == "" || opts.ChannelID == "" {
			return fmt.Errorf("km slack forget-thread: --thread and --channel must both be set (got thread=%q channel=%q)", opts.ThreadTS, opts.ChannelID)
		}
		channelID = opts.ChannelID
		threadTS = opts.ThreadTS
	}

	_, err := ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: channelID},
			"thread_ts":  &dynamodbtypes.AttributeValueMemberS{Value: threadTS},
		},
	})
	if err != nil {
		return fmt.Errorf("km slack forget-thread: DeleteItem (%s, %s): %w", channelID, threadTS, err)
	}
	return nil
}

// resolveSessionToThread queries the session-index GSI and returns the
// (channel_id, thread_ts) of the first matching row. Returns empty strings if
// no row is found (not an error).
func resolveSessionToThread(ctx context.Context, ddb DDBRepairAPI, tableName, sessionID string) (channelID, threadTS string, err error) {
	out, queryErr := ddb.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(tableName),
		IndexName:              aws.String("session-index"),
		KeyConditionExpression: aws.String("claude_session_id = :sid"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":sid": &dynamodbtypes.AttributeValueMemberS{Value: sessionID},
		},
	})
	if queryErr != nil {
		return "", "", queryErr
	}

	for _, item := range out.Items {
		// GSI KEYS_ONLY: item has channel_id and thread_ts table keys.
		chAttr, hasChannel := item["channel_id"]
		tsAttr, hasTS := item["thread_ts"]
		if !hasChannel || !hasTS {
			continue
		}
		chSV, ok1 := chAttr.(*dynamodbtypes.AttributeValueMemberS)
		tsSV, ok2 := tsAttr.(*dynamodbtypes.AttributeValueMemberS)
		if !ok1 || !ok2 {
			continue
		}

		// GetItem on base table to confirm row exists (GSI KEYS_ONLY).
		getOut, getErr := ddb.GetItem(ctx, &dynamodb.GetItemInput{
			TableName: aws.String(tableName),
			Key: map[string]dynamodbtypes.AttributeValue{
				"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: chSV.Value},
				"thread_ts":  &dynamodbtypes.AttributeValueMemberS{Value: tsSV.Value},
			},
		})
		if getErr != nil {
			continue
		}
		if getOut.Item == nil {
			continue
		}
		return chSV.Value, tsSV.Value, nil
	}
	return "", "", nil
}

// ──────────────────────────────────────────────────────────────────────────────
// RunSlackPruneThreads
// ──────────────────────────────────────────────────────────────────────────────

// RunSlackPruneThreads scans km-slack-threads, checks each unique channel_id
// against Slack's conversations.info, and returns rows on dead channels. When
// dryRun is false, dead rows are also deleted.
//
// sandboxID is optional: when non-empty, the scan is pre-filtered to that sandbox.
// A transient Slack error is NOT treated as "dead" — only channel_not_found is.
func RunSlackPruneThreads(ctx context.Context, ddb DDBRepairAPI, tableName string, checker SlackChannelChecker, sandboxID string, dryRun bool) ([]ThreadRow, error) {
	rows, err := RunSlackThreads(ctx, ddb, tableName, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("km slack prune-threads: scan: %w", err)
	}

	// Collect unique channel IDs and check them.
	checked := map[string]bool{} // channel_id → dead
	for _, row := range rows {
		if _, seen := checked[row.ChannelID]; !seen {
			dead, checkErr := checker.IsChannelDead(ctx, row.ChannelID)
			if checkErr != nil {
				// Transient error: skip (do not mark as dead).
				checked[row.ChannelID] = false
			} else {
				checked[row.ChannelID] = dead
			}
		}
	}

	var dead []ThreadRow
	for _, row := range rows {
		if checked[row.ChannelID] {
			dead = append(dead, row)
		}
	}

	if !dryRun {
		for _, row := range dead {
			_, delErr := ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{
				TableName: aws.String(tableName),
				Key: map[string]dynamodbtypes.AttributeValue{
					"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: row.ChannelID},
					"thread_ts":  &dynamodbtypes.AttributeValueMemberS{Value: row.ThreadTS},
				},
			})
			if delErr != nil {
				return dead, fmt.Errorf("km slack prune-threads: DeleteItem (%s, %s): %w", row.ChannelID, row.ThreadTS, delErr)
			}
		}
	}

	return dead, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// RunSlackForgetChannel
// ──────────────────────────────────────────────────────────────────────────────

// RunSlackForgetChannel deletes the alias row from km-slack-channels.
// This is the inverse of km slack adopt.
func RunSlackForgetChannel(ctx context.Context, ddb DDBRepairAPI, tableName, alias string, yes bool) error {
	if alias == "" {
		return fmt.Errorf("km slack forget-channel: alias is required")
	}
	_, err := ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"alias": &dynamodbtypes.AttributeValueMemberS{Value: alias},
		},
	})
	if err != nil {
		return fmt.Errorf("km slack forget-channel: DeleteItem alias=%s: %w", alias, err)
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Cobra commands
// ──────────────────────────────────────────────────────────────────────────────

// newSlackThreadsCmd creates the "km slack threads" subcommand.
func newSlackThreadsCmd(cfg *config.Config, sharedDeps *SlackCmdDeps) *cobra.Command {
	var alias string
	c := &cobra.Command{
		Use:   "threads <sandbox-id>",
		Short: "List km-slack-threads rows for a sandbox (O(n) Scan)",
		Long: `List all km-slack-threads DynamoDB rows for the given sandbox-id
(positional) or --alias. Uses DDB Scan + FilterExpression sandbox_id = :sid.

O(n) Scan cost: this is operator repair tooling — not a hot path. The
km-slack-threads table has no sandbox_id GSI; a full Scan is the only way
to list by sandbox_id without a new index.

Examples:
  km slack threads km-sandbox-abc123
  km slack threads --alias my-alias`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			sandboxID := ""
			if len(args) > 0 {
				sandboxID = args[0]
			}
			if alias != "" {
				sandboxID = alias
			}

			ddb, err := buildRepairDDBClient(ctx, cfg)
			if err != nil {
				return err
			}

			rows, listErr := RunSlackThreads(ctx, ddb, cfg.GetSlackThreadsTableName(), sandboxID)
			if listErr != nil {
				return listErr
			}

			if len(rows) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "no thread rows found for sandbox_id=%q\n", sandboxID)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-14s  %-22s  %-24s  %-8s  %s\n",
				"channel_id", "thread_ts", "session_id", "agent", "sandbox_id")
			for _, r := range rows {
				fmt.Fprintf(cmd.OutOrStdout(), "%-14s  %-22s  %-24s  %-8s  %s\n",
					r.ChannelID, r.ThreadTS, r.SessionID, r.AgentType, r.SandboxID)
			}
			return nil
		},
	}
	c.Flags().StringVar(&alias, "alias", "", "Resolve sandbox by alias instead of ID")
	_ = sharedDeps // not used; real deps built inline
	return c
}

// newSlackForgetThreadCmd creates the "km slack forget-thread" subcommand.
func newSlackForgetThreadCmd(cfg *config.Config, sharedDeps *SlackCmdDeps) *cobra.Command {
	var (
		session   string
		thread    string
		channel   string
		yes       bool
	)
	c := &cobra.Command{
		Use:   "forget-thread",
		Short: "Delete a stale km-slack-threads row by session or (thread, channel)",
		Long: `Delete a single row from the km-slack-threads DynamoDB table.

Resolution:
  --session <id>              Query the session-index GSI → resolve (channel_id, thread_ts) → DeleteItem.
  --thread <ts> --channel <C> DeleteItem keyed by (channel_id=C, thread_ts=ts) directly.

Exactly one resolution mode must be provided.

Use --yes to skip the confirmation prompt.

Examples:
  km slack forget-thread --session sess-abc123 --yes
  km slack forget-thread --thread 1234567890.000001 --channel C012ABCDE3F --yes`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			ddb, err := buildRepairDDBClient(ctx, cfg)
			if err != nil {
				return err
			}

			return RunSlackForgetThread(ctx, ddb, cfg.GetSlackThreadsTableName(), ForgetThreadOpts{
				Session:   session,
				ThreadTS:  thread,
				ChannelID: channel,
				Yes:       yes,
			})
		},
	}
	c.Flags().StringVar(&session, "session", "", "Session ID to resolve via session-index GSI")
	c.Flags().StringVar(&thread, "thread", "", "Thread timestamp (requires --channel)")
	c.Flags().StringVar(&channel, "channel", "", "Channel ID (requires --thread)")
	c.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	_ = sharedDeps
	return c
}

// newSlackPruneThreadsCmd creates the "km slack prune-threads" subcommand.
func newSlackPruneThreadsCmd(cfg *config.Config, sharedDeps *SlackCmdDeps) *cobra.Command {
	var dryRun bool
	c := &cobra.Command{
		Use:   "prune-threads [sandbox-id]",
		Short: "Validate km-slack-threads rows against live Slack; delete dead-channel rows",
		Long: `Scan km-slack-threads, check each unique channel_id via Slack conversations.info,
and report (and optionally delete) rows on channels that no longer exist.

Only channel_not_found is treated as "dead" — transient errors (rate-limited,
5xx, network) are skipped to avoid data loss on temporary API failures.

  --dry-run  List dead rows without deleting (safe preview; default false)

An optional positional sandbox-id limits the scan to that sandbox's rows.

Examples:
  km slack prune-threads --dry-run
  km slack prune-threads km-sandbox-abc123 --dry-run
  km slack prune-threads`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			sandboxID := ""
			if len(args) > 0 {
				sandboxID = args[0]
			}

			deps := sharedDeps
			if deps == nil {
				var err error
				deps, err = buildSlackCmdDeps(cfg)
				if err != nil {
					return err
				}
			}

			ddb, err := buildRepairDDBClient(ctx, cfg)
			if err != nil {
				return err
			}

			// Build Slack channel checker from bot token.
			token, tokenErr := deps.SSM.Get(ctx, deps.SsmPrefix+"slack/bot-token", true)
			if tokenErr != nil || token == "" {
				return fmt.Errorf("km slack prune-threads: Slack bot token not configured — run km slack init first")
			}
			checker := &slackClientChannelChecker{client: kmslack.NewClient(token, nil)}

			dead, pruneErr := RunSlackPruneThreads(ctx, ddb, cfg.GetSlackThreadsTableName(), checker, sandboxID, dryRun)
			if pruneErr != nil {
				return pruneErr
			}

			if len(dead) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "km slack prune-threads: no dead-channel rows found.")
				return nil
			}

			action := "deleted"
			if dryRun {
				action = "would delete (dry-run)"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "km slack prune-threads: %d dead-channel row(s) %s:\n", len(dead), action)
			for _, r := range dead {
				fmt.Fprintf(cmd.OutOrStdout(), "  channel_id=%-14s thread_ts=%-22s sandbox_id=%s\n",
					r.ChannelID, r.ThreadTS, r.SandboxID)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "List dead rows without deleting")
	_ = sharedDeps
	return c
}

// newSlackForgetChannelCmd creates the "km slack forget-channel" subcommand.
func newSlackForgetChannelCmd(cfg *config.Config, sharedDeps *SlackCmdDeps) *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "forget-channel <alias>",
		Short: "Delete a stale alias→channel mapping from km-slack-channels",
		Long: `Delete the alias→channelID row from the km-slack-channels DynamoDB table.

This is the inverse of 'km slack adopt'. Use this when the Slack channel
associated with an alias has been deleted and you want to clear the cached
mapping so a future 'km create --alias <alias>' can create a fresh channel.

After running forget-channel, run 'km slack adopt <alias> <channelID>' to
seed a new mapping, or let 'km create' provision a fresh channel automatically.

Examples:
  km slack forget-channel github-bot --yes
  km slack forget-channel security-review`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			alias := args[0]

			ddb, err := buildRepairDDBClient(ctx, cfg)
			if err != nil {
				return err
			}

			if err := RunSlackForgetChannel(ctx, ddb, cfg.GetSlackChannelsTableName(), alias, yes); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "km slack forget-channel: deleted alias=%s from %s\n",
				alias, cfg.GetSlackChannelsTableName())
			return nil
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	_ = sharedDeps
	return c
}

// ──────────────────────────────────────────────────────────────────────────────
// DDB client builder (prod)
// ──────────────────────────────────────────────────────────────────────────────

// buildRepairDDBClient builds a *dynamodb.Client with operator AWS creds.
func buildRepairDDBClient(ctx context.Context, cfg *config.Config) (DDBRepairAPI, error) {
	region := cfg.PrimaryRegion
	if region == "" {
		region = "us-east-1"
	}
	awsCfg, err := kmaws.LoadAWSConfigInRegion(ctx, cfg.AWSProfile, region)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return dynamodb.NewFromConfig(awsCfg), nil
}
