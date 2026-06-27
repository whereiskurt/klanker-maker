// Package quota — quota.go
// Action quota enforcement for high-impact outbound actions (Phase 121).
// Provides an agent-untrusted quota layer at the network-enforcement boundary
// (http-proxy) and bridge Lambdas.
//
// DynamoDB key design:
//
//	PK = {sandbox}#{action}          (partition key, string)
//	SK = lifetime                    (lifetime window)
//	SK = hour#{bucket}               (fixed hourly window, epoch/3600)
//	SK = day#{bucket}                (fixed daily window, epoch/86400)
//
// attrs: count (number), ttl (none for lifetime), breached_at, alert_sent
// Streams ENABLED on the table (drives the km-quota-alerter Lambda).
package quota

import (
	"context"
	"fmt"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Action is the metered action taxonomy (CONTEXT.md §3).
type Action string

const (
	ActionGithubPR      Action = "github_pr"
	ActionGithubComment Action = "github_comment"
	ActionGithubReview  Action = "github_review"
	ActionEmailSend     Action = "email_send"
	ActionSlackPost     Action = "slack_post"
	ActionH1Comment     Action = "h1_comment"
)

// OnBreach is the per-limit breach policy. Default warn (dormant).
type OnBreach string

const (
	BreachWarn   OnBreach = "warn"
	BreachBlock  OnBreach = "block"
	BreachFreeze OnBreach = "freeze"
)

// ActionLimit is the resolved limit set for one action (any subset of windows).
type ActionLimit struct {
	Lifetime *int64   `json:"lifetime,omitempty"`
	PerHour  *int64   `json:"perHour,omitempty"`
	PerDay   *int64   `json:"perDay,omitempty"`
	OnBreach OnBreach `json:"onBreach,omitempty"`
}

// Limits maps an action name to its resolved limit (the action_limits JSON map).
type Limits map[Action]ActionLimit

// WindowResult is the post-increment count vs limit for one window.
type WindowResult struct {
	Window   string // "lifetime" | "hour" | "day"
	Count    int64
	Limit    int64
	Exceeded bool
}

// Decision is the synchronous verdict returned by Record.
type Decision struct {
	Tripped     bool
	Windows     []WindowResult
	WorstWindow string
	OnBreach    OnBreach // resolved policy of the worst tripped window
}

// QuotaAPI is the minimal DynamoDB surface Record needs (atomic ADD count 1,
// ReturnValues ALL_NEW). Mock it in tests like pkg/aws/budget_test.go.
type QuotaAPI interface {
	UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

// hourBucket returns the fixed hourly bucket for a given time (epoch / 3600).
func hourBucket(t time.Time) int64 {
	return t.Unix() / 3600
}

// dayBucket returns the fixed daily bucket for a given time (epoch / 86400).
func dayBucket(t time.Time) int64 {
	return t.Unix() / 86400
}

// rowKey builds the DynamoDB composite key for a given sandbox, action, and window SK.
func rowKey(sandboxID string, action Action, sk string) map[string]dynamodbtypes.AttributeValue {
	return map[string]dynamodbtypes.AttributeValue{
		"PK": &dynamodbtypes.AttributeValueMemberS{Value: fmt.Sprintf("%s#%s", sandboxID, string(action))},
		"SK": &dynamodbtypes.AttributeValueMemberS{Value: sk},
	}
}

// windowSpec describes one window to increment.
type windowSpec struct {
	name  string // "lifetime" | "hour" | "day" — used in Decision.WorstWindow
	sk    string // DynamoDB sort key value
	limit int64
	ttl   *int64 // nil = no TTL (lifetime rows)
}

// Record increments every CONFIGURED window of the action atomically and returns
// the Decision. Unconfigured windows are skipped. No configured limit ⇒ no DDB
// write ⇒ Decision{Tripped:false} (dormant / byte-identical to today).
//
// For each configured window, Record issues one UpdateItem with:
//
//	UpdateExpression: "ADD #c :one"
//	ReturnValues:     ALL_NEW
//
// and on hour/day rows also sets ttl via "SET #ttl = if_not_exists(#ttl, :ttl)".
// Lifetime rows carry no TTL.
func Record(ctx context.Context, client QuotaAPI, tableName, sandboxID string, action Action, limit ActionLimit) (Decision, error) {
	now := time.Now().UTC()

	// Build the list of windows to increment (only those with a configured limit).
	var windows []windowSpec
	if limit.Lifetime != nil {
		windows = append(windows, windowSpec{
			name:  "lifetime",
			sk:    "lifetime",
			limit: *limit.Lifetime,
			ttl:   nil,
		})
	}
	if limit.PerHour != nil {
		expiry := now.Add(2 * time.Hour).Unix()
		windows = append(windows, windowSpec{
			name:  "hour",
			sk:    fmt.Sprintf("hour#%d", hourBucket(now)),
			limit: *limit.PerHour,
			ttl:   &expiry,
		})
	}
	if limit.PerDay != nil {
		expiry := now.Add(48 * time.Hour).Unix()
		windows = append(windows, windowSpec{
			name:  "day",
			sk:    fmt.Sprintf("day#%d", dayBucket(now)),
			limit: *limit.PerDay,
			ttl:   &expiry,
		})
	}

	// Dormant: no windows configured ⇒ no DDB writes, always-allow.
	if len(windows) == 0 {
		return Decision{}, nil
	}

	onBreach := limit.OnBreach
	if onBreach == "" {
		onBreach = BreachWarn
	}

	var results []WindowResult
	tripped := false
	worstWindow := ""

	for _, w := range windows {
		count, err := atomicIncrement(ctx, client, tableName, sandboxID, action, w)
		if err != nil {
			return Decision{}, fmt.Errorf("record quota window %s for sandbox %s action %s: %w", w.sk, sandboxID, action, err)
		}

		exceeded := count > w.limit
		results = append(results, WindowResult{
			Window:   w.name,
			Count:    count,
			Limit:    w.limit,
			Exceeded: exceeded,
		})
		if exceeded && !tripped {
			tripped = true
			worstWindow = w.name
		}
	}

	d := Decision{
		Tripped:     tripped,
		Windows:     results,
		WorstWindow: worstWindow,
	}
	if tripped {
		d.OnBreach = onBreach
	}
	return d, nil
}

// atomicIncrement issues UpdateItem ADD count :one for the given window and returns the
// post-increment count. For hour/day windows, also sets the TTL via if_not_exists.
func atomicIncrement(ctx context.Context, client QuotaAPI, tableName, sandboxID string, action Action, w windowSpec) (int64, error) {
	key := rowKey(sandboxID, action, w.sk)

	updateExpr := "ADD #c :one"
	exprNames := map[string]string{
		"#c": "count",
	}
	exprValues := map[string]dynamodbtypes.AttributeValue{
		":one": &dynamodbtypes.AttributeValueMemberN{Value: "1"},
	}

	if w.ttl != nil {
		// On first create set the TTL; if_not_exists prevents overwrites on subsequent
		// increments in the same bucket window.
		updateExpr += " SET #ttl = if_not_exists(#ttl, :ttl)"
		exprNames["#ttl"] = "ttl"
		exprValues[":ttl"] = &dynamodbtypes.AttributeValueMemberN{
			Value: fmt.Sprintf("%d", *w.ttl),
		}
	}

	out, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 awssdk.String(tableName),
		Key:                       key,
		UpdateExpression:          awssdk.String(updateExpr),
		ExpressionAttributeNames:  exprNames,
		ExpressionAttributeValues: exprValues,
		ReturnValues:              dynamodbtypes.ReturnValueAllNew,
	})
	if err != nil {
		return 0, err
	}

	if out.Attributes == nil {
		return 1, nil // first write; count must be 1
	}
	countAV, ok := out.Attributes["count"]
	if !ok {
		return 1, nil
	}
	countN, ok := countAV.(*dynamodbtypes.AttributeValueMemberN)
	if !ok {
		return 1, nil
	}
	var count int64
	if _, err2 := fmt.Sscanf(countN.Value, "%d", &count); err2 != nil {
		return 1, nil
	}
	return count, nil
}
