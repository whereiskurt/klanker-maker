package bridge

import (
	"context"
	"fmt"
	"strconv"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// sessionTTL matches the 30-day ttl_expiry the pollers have always written.
const sessionTTL = 30 * 24 * time.Hour

// sessionOwnerCondition guards every session write: the row must exist and
// name the caller. An absent item fails the condition (sandbox_id is absent),
// so UpdateItem can never create a row here.
const sessionOwnerCondition = "sandbox_id = :sb"

// sessionUpdate builds the SET/REMOVE expression shared by the three threads
// tables (2026-10-18). attr is the table's session attribute
// (claude_session_id for Slack, agent_session_id for GitHub and H1). It never
// names sandbox_id, last_dispatch_ts, created_at or turn_count. An empty
// sessionID REMOVEs attr and does not bind :sid (DynamoDB rejects unused
// expression values). ownerID binds :sb for sessionOwnerCondition.
func sessionUpdate(attr, sessionID, agentType, ownerID string) (string, map[string]dynamodbtypes.AttributeValue) {
	now := time.Now()
	vals := map[string]dynamodbtypes.AttributeValue{
		":sb":  &dynamodbtypes.AttributeValueMemberS{Value: ownerID},
		":at":  &dynamodbtypes.AttributeValueMemberS{Value: agentType},
		":lt":  &dynamodbtypes.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339)},
		":ttl": &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(now.Add(sessionTTL).Unix(), 10)},
	}
	if sessionID == "" {
		return "SET agent_type = :at, last_turn_ts = :lt, ttl_expiry = :ttl REMOVE " + attr, vals
	}
	vals[":sid"] = &dynamodbtypes.AttributeValueMemberS{Value: sessionID}
	return "SET " + attr + " = :sid, agent_type = :at, last_turn_ts = :lt, ttl_expiry = :ttl", vals
}

// GitHubThreadSessions implements ThreadSessionStore over km-github-threads
// (key repo S, number N).
type GitHubThreadSessions struct {
	Client    DDBUpdateItemAPI
	TableName string
}

func (s *GitHubThreadSessions) key(k ThreadKey) map[string]dynamodbtypes.AttributeValue {
	return map[string]dynamodbtypes.AttributeValue{
		"repo":   &dynamodbtypes.AttributeValueMemberS{Value: k.Repo},
		"number": &dynamodbtypes.AttributeValueMemberN{Value: strconv.Itoa(k.Number)},
	}
}

// LookupSandbox returns the row's sandbox_id, "" when the row is absent.
func (s *GitHubThreadSessions) LookupSandbox(ctx context.Context, k ThreadKey) (string, error) {
	return lookupSandboxID(ctx, s.Client, s.TableName, s.key(k))
}

// SetSession writes agent_session_id + agent_type (see sessionUpdate).
func (s *GitHubThreadSessions) SetSession(ctx context.Context, k ThreadKey, sessionID, agentType, ownerID string) error {
	return setSessionRow(ctx, s.Client, s.TableName, s.key(k), sessionID, agentType, ownerID)
}

// H1ThreadSessions implements ThreadSessionStore over km-h1-threads
// (key report_id S, target S).
type H1ThreadSessions struct {
	Client    DDBUpdateItemAPI
	TableName string
}

func (s *H1ThreadSessions) key(k ThreadKey) map[string]dynamodbtypes.AttributeValue {
	return map[string]dynamodbtypes.AttributeValue{
		"report_id": &dynamodbtypes.AttributeValueMemberS{Value: k.ReportID},
		"target":    &dynamodbtypes.AttributeValueMemberS{Value: k.Target},
	}
}

// LookupSandbox returns the row's sandbox_id, "" when the row is absent.
func (s *H1ThreadSessions) LookupSandbox(ctx context.Context, k ThreadKey) (string, error) {
	return lookupSandboxID(ctx, s.Client, s.TableName, s.key(k))
}

// SetSession writes agent_session_id + agent_type (see sessionUpdate).
func (s *H1ThreadSessions) SetSession(ctx context.Context, k ThreadKey, sessionID, agentType, ownerID string) error {
	return setSessionRow(ctx, s.Client, s.TableName, s.key(k), sessionID, agentType, ownerID)
}

func setSessionRow(ctx context.Context, c DDBUpdateItemAPI, table string, key map[string]dynamodbtypes.AttributeValue, sessionID, agentType, ownerID string) error {
	expr, vals := sessionUpdate("agent_session_id", sessionID, agentType, ownerID)
	_, err := c.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 awssdk.String(table),
		Key:                       key,
		UpdateExpression:          awssdk.String(expr),
		ConditionExpression:       awssdk.String(sessionOwnerCondition),
		ExpressionAttributeValues: vals,
	})
	if err != nil {
		return fmt.Errorf("%s set-session: %w", table, err)
	}
	return nil
}

// lookupSandboxID projects sandbox_id from one row; "" when absent.
func lookupSandboxID(ctx context.Context, c DDBUpdateItemAPI, table string, key map[string]dynamodbtypes.AttributeValue) (string, error) {
	out, err := c.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:            awssdk.String(table),
		Key:                  key,
		ProjectionExpression: awssdk.String("sandbox_id"),
	})
	if err != nil {
		return "", fmt.Errorf("%s lookup-sandbox: %w", table, err)
	}
	if v, ok := out.Item["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS); ok {
		return v.Value, nil
	}
	return "", nil
}
