// Package quota — usage.go
// Read-only live-usage reader for km status / km list quota visibility (Phase 121
// follow-up). FetchUsage GETs the current counter rows for each configured window
// so the operator can see used/limit alongside the resolved limits, without ever
// mutating the counters (the enforcement path in quota.go owns the writes).
package quota

import (
	"context"
	"fmt"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// GetItemAPI is the minimal DynamoDB surface FetchUsage needs (read-only GetItem).
// Satisfied by *dynamodb.Client and by test fakes.
type GetItemAPI interface {
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
}

// UsageRow is the live count vs configured limit for one (action, window).
type UsageRow struct {
	Action   Action
	Window   string // "lifetime" | "hour" | "day"
	Used     int64
	Limit    int64
	OnBreach OnBreach
}

// allActions is the stable iteration order for deterministic output. We iterate
// this const list rather than ranging the Limits map (map order is random).
var allActions = []Action{
	ActionGithubPR,
	ActionGithubComment,
	ActionGithubReview,
	ActionEmailSend,
	ActionSlackPost,
	ActionH1Comment,
}

// FetchUsage reads the current counter for each CONFIGURED window of each action in
// limits and returns one UsageRow per (action, window). Windows are ordered lifetime,
// hour, day; actions follow the stable const order (not map order). A missing row reads
// as Used 0. An action with no configured windows produces no rows. A GetItem error on
// any row surfaces as an error (the caller decides whether to fail-soft).
//
// The hour/day rows are keyed on the CURRENT bucket (time.Now().UTC()), matching the
// enforcement path's bucket math (hourBucket/dayBucket).
func FetchUsage(ctx context.Context, client GetItemAPI, tableName, sandboxID string, limits Limits) ([]UsageRow, error) {
	now := time.Now().UTC()
	var rows []UsageRow

	for _, action := range allActions {
		limit, ok := limits[action]
		if !ok {
			continue
		}

		// Build the ordered window list for this action (lifetime, hour, day).
		type win struct {
			name  string
			sk    string
			limit int64
		}
		var windows []win
		if limit.Lifetime != nil {
			windows = append(windows, win{name: "lifetime", sk: "lifetime", limit: *limit.Lifetime})
		}
		if limit.PerHour != nil {
			windows = append(windows, win{name: "hour", sk: fmt.Sprintf("hour#%d", hourBucket(now)), limit: *limit.PerHour})
		}
		if limit.PerDay != nil {
			windows = append(windows, win{name: "day", sk: fmt.Sprintf("day#%d", dayBucket(now)), limit: *limit.PerDay})
		}

		for _, w := range windows {
			used, err := fetchCount(ctx, client, tableName, sandboxID, action, w.sk)
			if err != nil {
				return nil, fmt.Errorf("fetch usage window %s for sandbox %s action %s: %w", w.sk, sandboxID, action, err)
			}
			rows = append(rows, UsageRow{
				Action:   action,
				Window:   w.name,
				Used:     used,
				Limit:    w.limit,
				OnBreach: limit.OnBreach,
			})
		}
	}

	return rows, nil
}

// fetchCount GETs a single counter row and returns its count (0 if the row or attr
// is absent). Reuses rowKey so the key shape exactly matches the enforcement writer.
func fetchCount(ctx context.Context, client GetItemAPI, tableName, sandboxID string, action Action, sk string) (int64, error) {
	out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(tableName),
		Key:       rowKey(sandboxID, action, sk),
	})
	if err != nil {
		return 0, err
	}
	if out == nil || out.Item == nil {
		return 0, nil
	}
	countAV, ok := out.Item["count"]
	if !ok {
		return 0, nil
	}
	countN, ok := countAV.(*dynamodbtypes.AttributeValueMemberN)
	if !ok {
		return 0, nil
	}
	var count int64
	if _, err := fmt.Sscanf(countN.Value, "%d", &count); err != nil {
		return 0, nil
	}
	return count, nil
}
