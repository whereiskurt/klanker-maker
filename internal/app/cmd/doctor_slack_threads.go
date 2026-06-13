// Package cmd — doctor_slack_threads.go
// Phase 110 Plan 06 — two km doctor WARN checks for dead Slack channel mappings.
//
//   checkSlackThreadDeadChannels: scan km-slack-threads, collect unique
//   channel_ids, probe each via conversations.info; WARN on channel_not_found.
//   Remediation: km slack prune-threads.
//
//   checkSlackChannelDeadAlias: scan km-slack-channels (alias→channel_id), probe
//   each channel_id; WARN on channel_not_found with the alias name.
//   Remediation: km slack forget-channel <alias> + km slack adopt.
//
// Both checks SKIP gracefully when no Slack checker is provided (bot token
// absent). Mirror the checkSlackBotUserIDCached pattern from
// doctor_slack_transcript.go — each check in its own file, injected deps,
// SKIP-safe, Error→Warn downgrade on registration in doctor.go.
package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// DoctorDDBScanAPI is the minimal DynamoDB surface needed by the dead-channel
// doctor checks (Scan only). *dynamodb.Client satisfies this interface.
// Defined here (not in doctor.go) so the two new checks are self-contained.
type DoctorDDBScanAPI interface {
	Scan(ctx context.Context, input *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

// checkSlackThreadDeadChannels scans km-slack-threads, collects unique
// channel_ids, and probes each via conversations.info.  Returns:
//
//   - SKIPPED: checker is nil (Slack bot token not configured).
//   - WARN:    DDB scan failed (transient).
//   - OK:      no rows, or all probed channels are alive.
//   - WARN:    one or more channel_ids return channel_not_found; includes the
//              IDs in the message and points to km slack prune-threads.
func checkSlackThreadDeadChannels(
	ctx context.Context,
	ddb DoctorDDBScanAPI,
	tableName string,
	checker SlackChannelChecker,
) CheckResult {
	name := "Slack thread dead channels"
	if checker == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "Slack bot token not configured — skipping dead-channel check for km-slack-threads",
		}
	}

	items, err := scanAllItems(ctx, ddb, tableName)
	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: fmt.Sprintf("failed to scan %s: %v", tableName, err),
		}
	}

	// Collect unique channel_ids.
	seen := make(map[string]bool)
	for _, item := range items {
		if v, ok := item["channel_id"].(*dynamodbtypes.AttributeValueMemberS); ok && v.Value != "" {
			seen[v.Value] = true
		}
	}
	if len(seen) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: "no dead channels in km-slack-threads (0 rows)",
		}
	}

	// Probe each unique channel_id.
	var dead []string
	for channelID := range seen {
		isDead, checkErr := checker.IsChannelDead(ctx, channelID)
		if checkErr != nil {
			// Transient error: skip (never delete on ambiguity).
			continue
		}
		if isDead {
			dead = append(dead, channelID)
		}
	}

	if len(dead) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("no dead channels in km-slack-threads (%d unique channel(s) probed)", len(seen)),
		}
	}

	return CheckResult{
		Name:   name,
		Status: CheckWarn,
		Message: fmt.Sprintf(
			"%d dead channel(s) referenced in km-slack-threads: %s",
			len(dead), strings.Join(dead, ", "),
		),
		Remediation: "Run 'km slack prune-threads' to remove thread rows pointing at non-existent channels.",
	}
}

// checkSlackChannelDeadAlias scans km-slack-channels (alias→channel_id), probes
// each channel_id via conversations.info.  Returns:
//
//   - SKIPPED: checker is nil (Slack bot token not configured).
//   - WARN:    DDB scan failed (transient).
//   - OK:      no alias rows, or all channels alive.
//   - WARN:    one or more alias rows point at a gone channel; names the aliases
//              and points to km slack forget-channel + km slack adopt.
func checkSlackChannelDeadAlias(
	ctx context.Context,
	ddb DoctorDDBScanAPI,
	tableName string,
	checker SlackChannelChecker,
) CheckResult {
	name := "Slack channel dead alias"
	if checker == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "Slack bot token not configured — skipping dead-channel check for km-slack-channels",
		}
	}

	items, err := scanAllItems(ctx, ddb, tableName)
	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: fmt.Sprintf("failed to scan %s: %v", tableName, err),
		}
	}

	if len(items) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: "no alias rows in km-slack-channels",
		}
	}

	// Probe each alias's channel_id.  Collect dead aliases.
	var deadAliases []string
	for _, item := range items {
		aliasSV, hasAlias := item["alias"].(*dynamodbtypes.AttributeValueMemberS)
		channelSV, hasChannel := item["channel_id"].(*dynamodbtypes.AttributeValueMemberS)
		if !hasAlias || !hasChannel || channelSV.Value == "" {
			continue
		}
		isDead, checkErr := checker.IsChannelDead(ctx, channelSV.Value)
		if checkErr != nil {
			// Transient error: skip (never report dead on ambiguity).
			continue
		}
		if isDead {
			deadAliases = append(deadAliases, aliasSV.Value)
		}
	}

	if len(deadAliases) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("no dead channels in km-slack-channels (%d alias row(s) probed)", len(items)),
		}
	}

	return CheckResult{
		Name:   name,
		Status: CheckWarn,
		Message: fmt.Sprintf(
			"%d alias row(s) point at a non-existent Slack channel: %s",
			len(deadAliases), strings.Join(deadAliases, ", "),
		),
		Remediation: "Run 'km slack forget-channel <alias>' to remove the stale mapping, then 'km slack adopt <alias> <channelID>' to re-seed it.",
	}
}

// scanAllItems is a single-page Scan helper. Returns all items from a DDB table
// in one pass (suitable for low-volume operator tables like km-slack-threads and
// km-slack-channels). Does NOT paginate — the tables are expected to be small
// and the check is advisory (a missed dead row on a large table is acceptable).
func scanAllItems(ctx context.Context, ddb DoctorDDBScanAPI, tableName string) ([]map[string]dynamodbtypes.AttributeValue, error) {
	if ddb == nil {
		return nil, fmt.Errorf("DDB client is nil")
	}
	out, err := ddb.Scan(ctx, &dynamodb.ScanInput{
		TableName: &tableName,
	})
	if err != nil {
		return nil, err
	}
	return out.Items, nil
}
