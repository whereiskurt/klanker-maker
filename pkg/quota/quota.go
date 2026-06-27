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
// TODO(Wave 1 plan 02): wire into Record for the hour window.
func hourBucket(t time.Time) int64 {
	return t.Unix() / 3600
}

// dayBucket returns the fixed daily bucket for a given time (epoch / 86400).
// TODO(Wave 1 plan 02): wire into Record for the day window.
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

// Record increments every CONFIGURED window of the action atomically and returns
// the Decision. Unconfigured windows are skipped. No configured limit ⇒ no DDB
// write ⇒ Decision{Tripped:false} (dormant / byte-identical to today).
//
// Wave 1 (plan 02) implements the full bucket math, atomic ADD, TTL writes,
// and breach detection. This skeleton returns an always-allow Decision so
// downstream code compiles and imports the correct type contract.
func Record(ctx context.Context, client QuotaAPI, tableName, sandboxID string, action Action, limit ActionLimit) (Decision, error) {
	// TODO(Wave 1 plan 02): implement full multi-window atomic ADD + breach detection.
	// Sketch of the intended implementation:
	//
	//  now := time.Now().UTC()
	//  windows to check:
	//    if limit.Lifetime != nil → SK = "lifetime",    no TTL
	//    if limit.PerHour  != nil → SK = "hour#<bucket>", TTL ~2h
	//    if limit.PerDay   != nil → SK = "day#<bucket>",  TTL ~2d
	//
	//  For each window: UpdateItem ADD count :one RETURN ALL_NEW → compare count vs limit.
	//  Build WindowResult slice; set Decision.Tripped = any(Exceeded).
	//  Set Decision.WorstWindow = SK of first/worst tripped window.
	//  Decision.OnBreach = limit.OnBreach (or BreachWarn if empty).

	_ = awssdk.String("") // suppress import-not-used during skeleton phase
	_ = rowKey(sandboxID, action, "lifetime")
	_, _ = hourBucket(time.Now()), dayBucket(time.Now())

	return Decision{}, nil
}
