package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	slackbridge "github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// HistoryMetaLister lists the mirrored-channel meta rows (satisfied by
// *slackbridge.DDBHistoryStore).
type HistoryMetaLister interface {
	ListMeta(ctx context.Context) ([]slackbridge.ChannelMeta, error)
}

// DynamoTTLAPI covers the two table-hygiene reads (satisfied by *dynamodb.Client).
type DynamoTTLAPI interface {
	DescribeTimeToLive(ctx context.Context, params *dynamodb.DescribeTimeToLiveInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DescribeTimeToLiveOutput, error)
	DescribeContinuousBackups(ctx context.Context, params *dynamodb.DescribeContinuousBackupsInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DescribeContinuousBackupsOutput, error)
}

// checkSlackHistory validates the history configuration (spec §8). Read-only.
// One SKIPPED result when no route carries a history block or a glob;
// otherwise four.
func checkSlackHistory(ctx context.Context, cfg *config.Config, lister SlackRouteChannelLister, describe DynamoDescribeAPI, ttl DynamoTTLAPI, meta HistoryMetaLister, scopes func(context.Context) ([]string, error), prefix string) []CheckResult {
	hasHistory, hasGlob := false, false
	if cfg != nil {
		for i := range cfg.Slack.Routes {
			if cfg.Slack.Routes[i].History != nil {
				hasHistory = true
			}
			if _, g := cfg.Slack.Routes[i].ChannelGlob(); g {
				hasGlob = true
			}
		}
	}
	if !hasHistory && !hasGlob {
		return []CheckResult{{Name: "Slack history", Status: CheckSkipped, Message: "no slack.routes entry has a history block or a glob"}}
	}
	routes := CheckResult{Name: "Slack history routes", Status: CheckOK}
	scopesR := CheckResult{Name: "Slack history scopes", Status: CheckOK, Message: "no glob routes; groups:read not needed"}
	table := CheckResult{Name: "Slack history table", Status: CheckOK}
	channels := CheckResult{Name: "Slack history channels", Status: CheckOK}

	// 1. Routes: validation + which globs match a current channel.
	if err := cfg.Slack.ValidateRoutes(); err != nil {
		routes.Status, routes.Message = CheckWarn, err.Error()
	} else if lister == nil {
		routes.Status, routes.Message = CheckSkipped, "no Slack client (bot token not configured)"
	} else if list, err := lister.ListBotChannels(ctx); err != nil {
		routes.Status, routes.Message = CheckWarn, "users.conversations failed: "+err.Error()
	} else if plan, err := effectiveSlackChannelRoutes(cfg, list); err != nil {
		routes.Status, routes.Message = CheckWarn, err.Error()
	} else {
		var notes []string
		for _, p := range plan.Patterns {
			notes = append(notes, fmt.Sprintf("#%s → %s (%d current channel(s))", p.Glob, p.Alias, len(p.Channels)))
		}
		routes.Message = fmt.Sprintf("%d named route(s), %d glob(s); %s", len(plan.Entries), len(plan.Patterns), strings.Join(notes, "; "))
	}

	// 2. Scopes: private channels need groups:read to be matched by name.
	if hasGlob && scopes != nil {
		scopesR = checkSlackGroupsReadScope(ctx, scopes)
		scopesR.Name = "Slack history scopes"
		if scopesR.Status != CheckOK {
			scopesR.Remediation = "Glob routes match private channels by name only with groups:read: `km slack manifest`, reinstall the App, `km slack rotate-token`."
		}
	}

	// 3. Table: exists, TTL on expires_at, PITR off.
	name := prefix + "-slack-history"
	if describe == nil || ttl == nil {
		table.Status, table.Message = CheckSkipped, "no DynamoDB client"
	} else if _, err := describe.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(name)}); err != nil {
		table.Status, table.Message, table.Remediation = CheckWarn, name+" not found: "+err.Error(), "`km init --dry-run=false` creates it (dynamodb-slack-history applies before lambda-slack-bridge)."
	} else {
		var problems []string
		if out, err := ttl.DescribeTimeToLive(ctx, &dynamodb.DescribeTimeToLiveInput{TableName: aws.String(name)}); err != nil || out.TimeToLiveDescription == nil ||
			out.TimeToLiveDescription.TimeToLiveStatus != types.TimeToLiveStatusEnabled || aws.ToString(out.TimeToLiveDescription.AttributeName) != "expires_at" {
			problems = append(problems, "TTL must be ENABLED on expires_at")
		}
		if out, err := ttl.DescribeContinuousBackups(ctx, &dynamodb.DescribeContinuousBackupsInput{TableName: aws.String(name)}); err == nil && out.ContinuousBackupsDescription != nil &&
			out.ContinuousBackupsDescription.PointInTimeRecoveryDescription != nil &&
			out.ContinuousBackupsDescription.PointInTimeRecoveryDescription.PointInTimeRecoveryStatus == types.PointInTimeRecoveryStatusEnabled {
			problems = append(problems, "point-in-time recovery must be off (a recoverable backup makes the stated retention false)")
		}
		if len(problems) > 0 {
			table.Status, table.Message, table.Remediation = CheckWarn, strings.Join(problems, "; "), "Reapply infra/modules/dynamodb-slack-history (`km init --dry-run=false`); never enable PITR on this table."
		} else {
			table.Message = name + ": TTL on expires_at, PITR off"
		}
	}

	// 4. Channels: every meta row with its backfill state.
	if meta == nil {
		channels.Status, channels.Message = CheckSkipped, "no history store"
	} else if rows, err := meta.ListMeta(ctx); err != nil {
		channels.Status, channels.Message = CheckWarn, "could not list mirrored channels: "+err.Error()
	} else {
		var lines []string
		warn := false
		for _, m := range rows {
			line := fmt.Sprintf("#%s (%s) → %s [%s] backfill=%s rows=%d", m.Name, m.Channel, m.Route.Alias, m.Route.Pattern, m.BackfillState, m.BackfillWritten)
			if m.Overlap != "" {
				line += " OVERLAP with #" + m.Overlap
				warn = true
			}
			if strings.HasPrefix(m.BackfillState, "error") || m.BackfillState == "rate_limited" {
				warn = true
			}
			lines = append(lines, line)
		}
		sort.Strings(lines)
		channels.Message = fmt.Sprintf("%d matched channel(s)", len(rows))
		if len(lines) > 0 {
			channels.Message += "\n    " + strings.Join(lines, "\n    ")
		}
		if warn {
			channels.Status, channels.Remediation = CheckWarn, "Narrow overlapping globs; a rate_limited backfill resumes on its own, an error state needs the bridge log."
		}
	}
	return []CheckResult{routes, scopesR, table, channels}
}

// slackHistoryConfigured reports whether any route carries a history block or a glob.
func slackHistoryConfigured(cfg *config.Config) bool {
	for i := range cfg.Slack.Routes {
		if cfg.Slack.Routes[i].History != nil {
			return true
		}
		if _, g := cfg.Slack.Routes[i].ChannelGlob(); g {
			return true
		}
	}
	return false
}
