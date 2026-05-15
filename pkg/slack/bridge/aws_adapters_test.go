package bridge_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// ============================================================
// Mock implementations
// ============================================================

// mockDynamoGetPut supports GetItem and PutItem for adapter tests.
type mockDynamoGetPut struct {
	getItem func(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	putItem func(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
}

func (m *mockDynamoGetPut) GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if m.getItem != nil {
		return m.getItem(ctx, params, optFns...)
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (m *mockDynamoGetPut) PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	if m.putItem != nil {
		return m.putItem(ctx, params, optFns...)
	}
	return &dynamodb.PutItemOutput{}, nil
}

// mockSSMClient supports GetParameter for SSMBotTokenFetcher tests.
type mockSSMClient struct {
	getParam func(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

func (m *mockSSMClient) GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if m.getParam != nil {
		return m.getParam(ctx, params, optFns...)
	}
	return &ssm.GetParameterOutput{}, nil
}

// ============================================================
// DynamoPublicKeyFetcher tests
// ============================================================

func TestDynamoPublicKeyFetcher_HappyPath(t *testing.T) {
	// A 32-byte all-zeros public key, base64-encoded.
	const zeroKeyB64 = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

	mock := &mockDynamoGetPut{
		getItem: func(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			// Assert correct table and key
			if aws.ToString(params.TableName) != "km-identities" {
				t.Errorf("expected table km-identities, got %q", aws.ToString(params.TableName))
			}
			sidAttr, ok := params.Key["sandbox_id"]
			if !ok {
				t.Error("expected sandbox_id key attribute")
			}
			if sidAttr.(*dynamodbtypes.AttributeValueMemberS).Value != "sb-test123" {
				t.Errorf("unexpected sandbox_id: %v", sidAttr)
			}
			return &dynamodb.GetItemOutput{
				Item: map[string]dynamodbtypes.AttributeValue{
					"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "sb-test123"},
					"public_key": &dynamodbtypes.AttributeValueMemberS{Value: zeroKeyB64},
				},
			}, nil
		},
	}

	fetcher := &bridge.DynamoPublicKeyFetcher{Client: mock, TableName: "km-identities"}
	key, err := fetcher.Fetch(context.Background(), "sb-test123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(key) != 32 {
		t.Errorf("expected 32-byte key, got %d bytes", len(key))
	}
}

func TestDynamoPublicKeyFetcher_SenderNotFound(t *testing.T) {
	mock := &mockDynamoGetPut{
		getItem: func(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			// Return empty item (no DynamoDB record)
			return &dynamodb.GetItemOutput{Item: map[string]dynamodbtypes.AttributeValue{}}, nil
		},
	}

	fetcher := &bridge.DynamoPublicKeyFetcher{Client: mock, TableName: "km-identities"}
	_, err := fetcher.Fetch(context.Background(), "sb-unknown")
	if err == nil {
		t.Fatal("expected ErrSenderNotFound, got nil")
	}
	if !errors.Is(err, bridge.ErrSenderNotFound) {
		t.Errorf("expected ErrSenderNotFound, got %v", err)
	}
}

// ============================================================
// DynamoNonceStore tests
// ============================================================

func TestDynamoNonceStore_Reserve_HappyPath(t *testing.T) {
	var capturedCondExpr string
	mock := &mockDynamoGetPut{
		putItem: func(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			if aws.ToString(params.TableName) != "km-slack-bridge-nonces" {
				t.Errorf("expected table km-slack-bridge-nonces, got %q", aws.ToString(params.TableName))
			}
			capturedCondExpr = aws.ToString(params.ConditionExpression)
			// Verify nonce attribute present
			if _, ok := params.Item["nonce"]; !ok {
				t.Error("expected nonce attribute in PutItem")
			}
			// Verify ttl_expiry attribute present
			if _, ok := params.Item["ttl_expiry"]; !ok {
				t.Error("expected ttl_expiry attribute in PutItem")
			}
			return &dynamodb.PutItemOutput{}, nil
		},
	}

	store := &bridge.DynamoNonceStore{Client: mock, TableName: "km-slack-bridge-nonces"}
	err := store.Reserve(context.Background(), "test-nonce-abc", 600)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedCondExpr, "attribute_not_exists") {
		t.Errorf("expected ConditionExpression with attribute_not_exists, got %q", capturedCondExpr)
	}
}

func TestDynamoNonceStore_Reserve_Replayed(t *testing.T) {
	mock := &mockDynamoGetPut{
		putItem: func(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			// Simulate DynamoDB ConditionalCheckFailedException (nonce already exists)
			return nil, &dynamodbtypes.ConditionalCheckFailedException{
				Message: aws.String("conditional check failed"),
			}
		},
	}

	store := &bridge.DynamoNonceStore{Client: mock, TableName: "km-slack-bridge-nonces"}
	err := store.Reserve(context.Background(), "replayed-nonce", 600)
	if err == nil {
		t.Fatal("expected ErrNonceReplayed, got nil")
	}
	if !errors.Is(err, bridge.ErrNonceReplayed) {
		t.Errorf("expected ErrNonceReplayed, got %v", err)
	}
}

// ============================================================
// DynamoChannelOwnershipFetcher tests
// ============================================================

func TestDynamoChannelOwnershipFetcher_HappyPath(t *testing.T) {
	mock := &mockDynamoGetPut{
		getItem: func(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			if aws.ToString(params.TableName) != "km-sandboxes" {
				t.Errorf("expected table km-sandboxes, got %q", aws.ToString(params.TableName))
			}
			return &dynamodb.GetItemOutput{
				Item: map[string]dynamodbtypes.AttributeValue{
					"sandbox_id":      &dynamodbtypes.AttributeValueMemberS{Value: "sb-abc123"},
					"slack_channel_id": &dynamodbtypes.AttributeValueMemberS{Value: "C01234567"},
				},
			}, nil
		},
	}

	fetcher := &bridge.DynamoChannelOwnershipFetcher{Client: mock, TableName: "km-sandboxes"}
	channelID, err := fetcher.OwnedChannel(context.Background(), "sb-abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if channelID != "C01234567" {
		t.Errorf("expected C01234567, got %q", channelID)
	}
}

func TestDynamoChannelOwnershipFetcher_NoChannel(t *testing.T) {
	mock := &mockDynamoGetPut{
		getItem: func(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{
				// No slack_channel_id attribute
				Item: map[string]dynamodbtypes.AttributeValue{
					"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "sb-abc123"},
				},
			}, nil
		},
	}

	fetcher := &bridge.DynamoChannelOwnershipFetcher{Client: mock, TableName: "km-sandboxes"}
	channelID, err := fetcher.OwnedChannel(context.Background(), "sb-abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if channelID != "" {
		t.Errorf("expected empty channel ID, got %q", channelID)
	}
}

// ============================================================
// SSMBotTokenFetcher tests
// ============================================================

func TestSSMBotTokenFetcher_HappyPath(t *testing.T) {
	callCount := 0
	mock := &mockSSMClient{
		getParam: func(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
			callCount++
			if aws.ToString(params.Name) != "/km/slack/bot-token" {
				t.Errorf("expected /km/slack/bot-token, got %q", aws.ToString(params.Name))
			}
			if !aws.ToBool(params.WithDecryption) {
				t.Error("expected WithDecryption=true")
			}
			return &ssm.GetParameterOutput{
				Parameter: &ssmtypes.Parameter{
					Value: aws.String("xoxb-test-token"),
				},
			}, nil
		},
	}

	fetcher := &bridge.SSMBotTokenFetcher{Client: mock, Path: "/km/slack/bot-token"}
	token, err := fetcher.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "xoxb-test-token" {
		t.Errorf("expected xoxb-test-token, got %q", token)
	}

	// Second call should hit cache (callCount stays 1)
	_, err = fetcher.Fetch(context.Background())
	if err != nil {
		t.Fatalf("cache fetch error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 SSM call (cached), got %d", callCount)
	}
}

func TestSSMBotTokenFetcher_Error(t *testing.T) {
	mock := &mockSSMClient{
		getParam: func(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
			return nil, fmt.Errorf("SSM unavailable")
		},
	}

	fetcher := &bridge.SSMBotTokenFetcher{Client: mock, Path: "/km/slack/bot-token"}
	_, err := fetcher.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ============================================================
// SlackPosterAdapter tests
// ============================================================

func TestSlackPosterAdapter_PostMessage_RateLimited(t *testing.T) {
	// Fake Slack API server that returns 429 + Retry-After: 10
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprintln(w, `{"ok":false,"error":"ratelimited"}`)
	}))
	defer srv.Close()

	tokenFetcher := &bridge.SSMBotTokenFetcher{Client: &mockSSMClient{
		getParam: func(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
			return &ssm.GetParameterOutput{
				Parameter: &ssmtypes.Parameter{Value: aws.String("xoxb-test")},
			}, nil
		},
	}, Path: "/km/slack/bot-token"}

	adapter := &bridge.SlackPosterAdapter{
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
		Tokens:     tokenFetcher,
	}

	_, err := adapter.PostMessage(context.Background(), "C01234567", "Test", "body", "")
	if err == nil {
		t.Fatal("expected ErrSlackRateLimited, got nil")
	}
	var rl *bridge.ErrSlackRateLimited
	if !errors.As(err, &rl) {
		t.Fatalf("expected *ErrSlackRateLimited, got %T: %v", err, err)
	}
	if rl.RetryAfterSeconds != 10 {
		t.Errorf("expected RetryAfterSeconds=10, got %d", rl.RetryAfterSeconds)
	}
	if rl.Method != "chat.postMessage" {
		t.Errorf("expected Method=chat.postMessage, got %q", rl.Method)
	}
}

func TestSlackPosterAdapter_PostMessage_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat.postMessage" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"ok":true,"ts":"1234567890.123456"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	tokenFetcher := &bridge.SSMBotTokenFetcher{Client: &mockSSMClient{
		getParam: func(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
			return &ssm.GetParameterOutput{
				Parameter: &ssmtypes.Parameter{Value: aws.String("xoxb-test")},
			}, nil
		},
	}, Path: "/km/slack/bot-token"}

	adapter := &bridge.SlackPosterAdapter{
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
		Tokens:     tokenFetcher,
	}

	ts, err := adapter.PostMessage(context.Background(), "C01234567", "Subject", "body text", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts != "1234567890.123456" {
		t.Errorf("expected ts=1234567890.123456, got %q", ts)
	}
}

func TestSlackPosterAdapter_ArchiveChannel_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true}`)
	}))
	defer srv.Close()

	tokenFetcher := &bridge.SSMBotTokenFetcher{Client: &mockSSMClient{
		getParam: func(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
			return &ssm.GetParameterOutput{
				Parameter: &ssmtypes.Parameter{Value: aws.String("xoxb-test")},
			}, nil
		},
	}, Path: "/km/slack/bot-token"}

	adapter := &bridge.SlackPosterAdapter{
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
		Tokens:     tokenFetcher,
	}

	err := adapter.ArchiveChannel(context.Background(), "C01234567")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSSMBotTokenFetcher_CacheExpiry verifies the token is refreshed after the
// cache TTL expires. We shorten the cache duration to 1ms for the test.
func TestSSMBotTokenFetcher_CacheExpiry(t *testing.T) {
	callCount := 0
	mock := &mockSSMClient{
		getParam: func(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
			callCount++
			return &ssm.GetParameterOutput{
				Parameter: &ssmtypes.Parameter{Value: aws.String("xoxb-refreshed")},
			}, nil
		},
	}

	fetcher := &bridge.SSMBotTokenFetcher{
		Client:   mock,
		Path:     "/km/slack/bot-token",
		CacheTTL: time.Millisecond, // Tiny TTL for test
	}

	// First fetch — populates cache
	_, err := fetcher.Fetch(context.Background())
	if err != nil {
		t.Fatalf("first fetch error: %v", err)
	}

	// Sleep past cache TTL
	time.Sleep(5 * time.Millisecond)

	// Second fetch — cache should be expired, SSM called again
	_, err = fetcher.Fetch(context.Background())
	if err != nil {
		t.Fatalf("second fetch error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 SSM calls after cache expiry, got %d", callCount)
	}
}

// ============================================================
// Plan 67-05 adapter tests
// ============================================================

// mockSQSSendMessage implements bridge.SQSSendMessageAPI for SQSAdapter tests.
type mockSQSSendMessage struct {
	sendMessage func(ctx context.Context, in *sqs.SendMessageInput, opts ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
	callCount   int
}

func (m *mockSQSSendMessage) SendMessage(ctx context.Context, in *sqs.SendMessageInput, opts ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	m.callCount++
	if m.sendMessage != nil {
		return m.sendMessage(ctx, in, opts...)
	}
	return &sqs.SendMessageOutput{}, nil
}

// mockDDBQueryGetPut implements bridge.DDBQueryGetPutAPI for thread store / channel fetch tests.
type mockDDBQueryGetPut struct {
	getItem  func(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	putItem  func(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	query    func(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	putCalls int
}

func (m *mockDDBQueryGetPut) GetItem(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if m.getItem != nil {
		return m.getItem(ctx, in, opts...)
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (m *mockDDBQueryGetPut) PutItem(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	m.putCalls++
	if m.putItem != nil {
		return m.putItem(ctx, in, opts...)
	}
	return &dynamodb.PutItemOutput{}, nil
}

func (m *mockDDBQueryGetPut) Query(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	if m.query != nil {
		return m.query(ctx, in, opts...)
	}
	return &dynamodb.QueryOutput{}, nil
}

// mockDDBUpdateItem extends mockDDBQueryGetPut with UpdateItem for DDBPauseHinter tests.
type mockDDBUpdateItem struct {
	mockDDBQueryGetPut
	updateItem   func(ctx context.Context, in *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	updateCalled int
}

func (m *mockDDBUpdateItem) UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	m.updateCalled++
	if m.updateItem != nil {
		return m.updateItem(ctx, in, opts...)
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

// fakeSandboxFetcher is a minimal SandboxByChannelFetcher for DDBPauseHinter tests.
type fakeSandboxFetcher struct {
	info bridge.SandboxRoutingInfo
	err  error
}

func (f *fakeSandboxFetcher) FetchByChannel(_ context.Context, _ string) (bridge.SandboxRoutingInfo, error) {
	return f.info, f.err
}

// ============================================================
// SQSAdapter tests
// ============================================================

func TestSQSAdapter_Send_HappyPath(t *testing.T) {
	var capturedInput *sqs.SendMessageInput
	mock := &mockSQSSendMessage{
		sendMessage: func(ctx context.Context, in *sqs.SendMessageInput, opts ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
			capturedInput = in
			return &sqs.SendMessageOutput{}, nil
		},
	}

	a := &bridge.SQSAdapter{Client: mock}
	err := a.Send(context.Background(), "https://sqs.us-east-1.amazonaws.com/123/q.fifo", `{"text":"hi"}`, "sb-X", "evt-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if aws.ToString(capturedInput.QueueUrl) != "https://sqs.us-east-1.amazonaws.com/123/q.fifo" {
		t.Errorf("wrong queue url: %q", aws.ToString(capturedInput.QueueUrl))
	}
	if aws.ToString(capturedInput.MessageGroupId) != "sb-X" {
		t.Errorf("wrong group id: %q", aws.ToString(capturedInput.MessageGroupId))
	}
	if aws.ToString(capturedInput.MessageDeduplicationId) != "evt-1" {
		t.Errorf("wrong dedup id: %q", aws.ToString(capturedInput.MessageDeduplicationId))
	}
}

func TestSQSAdapter_Send_EmptyQueueURL(t *testing.T) {
	mock := &mockSQSSendMessage{}
	a := &bridge.SQSAdapter{Client: mock}
	err := a.Send(context.Background(), "", `{"text":"hi"}`, "sb-X", "evt-1")
	if err == nil {
		t.Fatal("expected error for empty queue url, got nil")
	}
	if mock.callCount != 0 {
		t.Error("SDK must not be called when queue url is empty")
	}
}

func TestSQSAdapter_Send_SDKError(t *testing.T) {
	mock := &mockSQSSendMessage{
		sendMessage: func(ctx context.Context, in *sqs.SendMessageInput, opts ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
			return nil, fmt.Errorf("SQS unavailable")
		},
	}
	a := &bridge.SQSAdapter{Client: mock}
	err := a.Send(context.Background(), "https://sqs.us-east-1.amazonaws.com/123/q.fifo", `{}`, "sb-X", "evt-1")
	if err == nil {
		t.Fatal("expected wrapped SDK error, got nil")
	}
	if !strings.Contains(err.Error(), "sqs send to") {
		t.Errorf("expected wrapped error message, got: %v", err)
	}
}

// ============================================================
// DDBThreadStore tests
// ============================================================

func TestDDBThreadStore_Upsert_NewRow(t *testing.T) {
	mock := &mockDDBQueryGetPut{}
	s := &bridge.DDBThreadStore{Client: mock, TableName: "km-slack-threads"}
	err := s.Upsert(context.Background(), "C1", "1.0", "sb-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.putCalls != 1 {
		t.Errorf("expected 1 PutItem call, got %d", mock.putCalls)
	}
}

func TestDDBThreadStore_Upsert_AlreadyExists(t *testing.T) {
	mock := &mockDDBQueryGetPut{
		putItem: func(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			return nil, &dynamodbtypes.ConditionalCheckFailedException{
				Message: aws.String("already exists"),
			}
		},
	}
	s := &bridge.DDBThreadStore{Client: mock, TableName: "km-slack-threads"}
	err := s.Upsert(context.Background(), "C1", "1.0", "sb-A")
	if err != nil {
		t.Fatalf("ConditionalCheckFailed must map to nil (idempotent success), got: %v", err)
	}
}

func TestDDBThreadStore_Upsert_OtherError(t *testing.T) {
	mock := &mockDDBQueryGetPut{
		putItem: func(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			return nil, fmt.Errorf("DDB throttled")
		},
	}
	s := &bridge.DDBThreadStore{Client: mock, TableName: "km-slack-threads"}
	err := s.Upsert(context.Background(), "C1", "1.0", "sb-A")
	if err == nil {
		t.Fatal("expected wrapped error, got nil")
	}
	if !strings.Contains(err.Error(), "threads upsert") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDDBThreadStore_Get_RowExists(t *testing.T) {
	mock := &mockDDBQueryGetPut{
		getItem: func(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{
				Item: map[string]dynamodbtypes.AttributeValue{
					"channel_id":        &dynamodbtypes.AttributeValueMemberS{Value: "C1"},
					"thread_ts":         &dynamodbtypes.AttributeValueMemberS{Value: "1.0"},
					"claude_session_id": &dynamodbtypes.AttributeValueMemberS{Value: "sess-abc"},
				},
			}, nil
		},
	}
	s := &bridge.DDBThreadStore{Client: mock, TableName: "km-slack-threads"}
	sid, err := s.Get(context.Background(), "C1", "1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sid != "sess-abc" {
		t.Errorf("expected sess-abc, got %q", sid)
	}
}

func TestDDBThreadStore_Get_RowMissing(t *testing.T) {
	mock := &mockDDBQueryGetPut{
		getItem: func(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{Item: map[string]dynamodbtypes.AttributeValue{}}, nil
		},
	}
	s := &bridge.DDBThreadStore{Client: mock, TableName: "km-slack-threads"}
	sid, err := s.Get(context.Background(), "C1", "1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sid != "" {
		t.Errorf("expected empty string for missing row, got %q", sid)
	}
}

// ============================================================
// DDBSandboxByChannel tests
// ============================================================

func TestDDBSandboxByChannel_NoMatch(t *testing.T) {
	mock := &mockDDBQueryGetPut{
		query: func(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
			return &dynamodb.QueryOutput{Items: nil}, nil
		},
	}
	f := &bridge.DDBSandboxByChannel{Client: mock, TableName: "km-sandboxes", IndexName: "slack_channel_id-index"}
	info, err := f.FetchByChannel(context.Background(), "C_UNKNOWN")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.SandboxID != "" {
		t.Errorf("expected empty SandboxID for unknown channel, got %q", info.SandboxID)
	}
}

func TestDDBSandboxByChannel_Found_Paused(t *testing.T) {
	mock := &mockDDBQueryGetPut{
		query: func(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
			return &dynamodb.QueryOutput{
				Items: []map[string]dynamodbtypes.AttributeValue{
					{
						"sandbox_id":             &dynamodbtypes.AttributeValueMemberS{Value: "sb-X"},
						"slack_inbound_queue_url": &dynamodbtypes.AttributeValueMemberS{Value: "https://sqs.../q.fifo"},
						"state":                  &dynamodbtypes.AttributeValueMemberS{Value: "paused"},
					},
				},
			}, nil
		},
	}
	f := &bridge.DDBSandboxByChannel{Client: mock, TableName: "km-sandboxes", IndexName: "slack_channel_id-index"}
	info, err := f.FetchByChannel(context.Background(), "C1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.SandboxID != "sb-X" {
		t.Errorf("expected sb-X, got %q", info.SandboxID)
	}
	if !info.Paused {
		t.Error("expected Paused=true for state=paused")
	}
	if info.QueueURL == "" {
		t.Error("expected non-empty QueueURL")
	}
}

// ============================================================
// SSMSigningSecretFetcher tests
// ============================================================

func TestSSMSigningSecretFetcher_Caching(t *testing.T) {
	callCount := 0
	mock := &mockSSMClient{
		getParam: func(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
			callCount++
			return &ssm.GetParameterOutput{
				Parameter: &ssmtypes.Parameter{Value: aws.String("test-signing-secret")},
			}, nil
		},
	}
	f := &bridge.SSMSigningSecretFetcher{
		Client:   mock,
		Path:     "/km/slack/signing-secret",
		CacheTTL: time.Hour, // long TTL — second call must hit cache
	}

	secret1, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("first fetch error: %v", err)
	}
	secret2, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("second fetch error: %v", err)
	}
	if secret1 != secret2 {
		t.Errorf("cached value mismatch: %q vs %q", secret1, secret2)
	}
	if callCount != 1 {
		t.Errorf("expected 1 SSM call (cached), got %d", callCount)
	}
}

func TestSSMSigningSecretFetcher_Refresh(t *testing.T) {
	callCount := 0
	mock := &mockSSMClient{
		getParam: func(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
			callCount++
			return &ssm.GetParameterOutput{
				Parameter: &ssmtypes.Parameter{Value: aws.String("signing-secret-v" + fmt.Sprint(callCount))},
			}, nil
		},
	}
	f := &bridge.SSMSigningSecretFetcher{
		Client:   mock,
		Path:     "/km/slack/signing-secret",
		CacheTTL: time.Millisecond, // tiny TTL so second call re-fetches
	}

	_, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("first fetch error: %v", err)
	}
	time.Sleep(5 * time.Millisecond) // let TTL expire
	_, err = f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("second fetch error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 SSM calls after TTL expiry, got %d", callCount)
	}
}

// ============================================================
// DDBPauseHinter tests
// ============================================================

// buildPauseHinter is a test helper that creates a DDBPauseHinter with the
// supplied mock DDB client and sandbox fetcher.
func buildPauseHinter(
	ddb bridge.DDBUpdateItemAPI,
	sb bridge.SandboxByChannelFetcher,
	postFn bridge.PostHintFunc,
	nowFn func() time.Time,
) *bridge.DDBPauseHinter {
	return &bridge.DDBPauseHinter{
		Client:             ddb,
		SandboxesTableName: "km-sandboxes",
		SandboxByChannel:   sb,
		Post:               postFn,
		HintText:           "Sandbox is paused; message queued.",
		CooldownSeconds:    3600,
		Now:                nowFn,
	}
}

func TestDDBPauseHinter_CooldownExpired_PostsAndWrites(t *testing.T) {
	// Scenario: GetItem returns empty item (last_pause_hint_ts absent)
	// → UpdateItem succeeds → Post called once.
	nowT := time.Unix(1_700_000_000, 0)
	var postCalls []struct{ ch, ts, txt string }
	ddb := &mockDDBUpdateItem{
		mockDDBQueryGetPut: mockDDBQueryGetPut{
			getItem: func(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
				return &dynamodb.GetItemOutput{Item: map[string]dynamodbtypes.AttributeValue{}}, nil
			},
		},
	}
	sb := &fakeSandboxFetcher{info: bridge.SandboxRoutingInfo{SandboxID: "sb-X"}}
	postFn := func(ctx context.Context, ch, ts, txt string) error {
		postCalls = append(postCalls, struct{ ch, ts, txt string }{ch, ts, txt})
		return nil
	}
	h := buildPauseHinter(ddb, sb, postFn, func() time.Time { return nowT })

	if err := h.PostIfCooldownExpired(context.Background(), "C1", "1.0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(postCalls) != 1 {
		t.Fatalf("expected 1 post call, got %d", len(postCalls))
	}
	if postCalls[0].ch != "C1" || postCalls[0].ts != "1.0" {
		t.Errorf("unexpected post args: %+v", postCalls[0])
	}
	if ddb.updateCalled != 1 {
		t.Fatalf("expected 1 UpdateItem call, got %d", ddb.updateCalled)
	}
}

func TestDDBPauseHinter_CooldownExpired_StaleTimestamp_PostsAndWrites(t *testing.T) {
	// Scenario: last_pause_hint_ts is 7200s ago (> 3600s cooldown) → should post.
	nowT := time.Unix(1_700_000_000, 0)
	staleTS := strconv.FormatInt(nowT.Unix()-7200, 10)
	var postCalls int
	ddb := &mockDDBUpdateItem{
		mockDDBQueryGetPut: mockDDBQueryGetPut{
			getItem: func(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
				return &dynamodb.GetItemOutput{
					Item: map[string]dynamodbtypes.AttributeValue{
						"last_pause_hint_ts": &dynamodbtypes.AttributeValueMemberN{Value: staleTS},
					},
				}, nil
			},
		},
	}
	sb := &fakeSandboxFetcher{info: bridge.SandboxRoutingInfo{SandboxID: "sb-X"}}
	postFn := func(ctx context.Context, ch, ts, txt string) error {
		postCalls++
		return nil
	}
	h := buildPauseHinter(ddb, sb, postFn, func() time.Time { return nowT })

	if err := h.PostIfCooldownExpired(context.Background(), "C1", "1.0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if postCalls != 1 {
		t.Fatalf("expected 1 post call, got %d", postCalls)
	}
	if ddb.updateCalled != 1 {
		t.Fatalf("expected 1 UpdateItem, got %d", ddb.updateCalled)
	}
}

func TestDDBPauseHinter_CooldownActive_NoPostNoWrite(t *testing.T) {
	// Scenario: last_pause_hint_ts is 1800s ago (within 3600s cooldown) → skip.
	nowT := time.Unix(1_700_000_000, 0)
	recentTS := strconv.FormatInt(nowT.Unix()-1800, 10)
	postCalled := false
	ddb := &mockDDBUpdateItem{
		mockDDBQueryGetPut: mockDDBQueryGetPut{
			getItem: func(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
				return &dynamodb.GetItemOutput{
					Item: map[string]dynamodbtypes.AttributeValue{
						"last_pause_hint_ts": &dynamodbtypes.AttributeValueMemberN{Value: recentTS},
					},
				}, nil
			},
		},
	}
	sb := &fakeSandboxFetcher{info: bridge.SandboxRoutingInfo{SandboxID: "sb-X"}}
	postFn := func(ctx context.Context, ch, ts, txt string) error {
		postCalled = true
		return nil
	}
	h := buildPauseHinter(ddb, sb, postFn, func() time.Time { return nowT })

	if err := h.PostIfCooldownExpired(context.Background(), "C1", "1.0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if postCalled {
		t.Error("Post must NOT be called when cooldown is active")
	}
	if ddb.updateCalled != 0 {
		t.Errorf("UpdateItem must NOT be called when cooldown active, got %d calls", ddb.updateCalled)
	}
}

func TestDDBPauseHinter_LWTRace_NoDoublePost(t *testing.T) {
	// Scenario: UpdateItem returns ConditionalCheckFailed (lost the LWT race).
	// Adapter must return nil without calling Post.
	nowT := time.Unix(1_700_000_000, 0)
	postCalled := false
	ddb := &mockDDBUpdateItem{
		mockDDBQueryGetPut: mockDDBQueryGetPut{
			getItem: func(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
				return &dynamodb.GetItemOutput{Item: map[string]dynamodbtypes.AttributeValue{}}, nil
			},
		},
		updateItem: func(ctx context.Context, in *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			return nil, &dynamodbtypes.ConditionalCheckFailedException{
				Message: aws.String("conditional check failed"),
			}
		},
	}
	sb := &fakeSandboxFetcher{info: bridge.SandboxRoutingInfo{SandboxID: "sb-X"}}
	postFn := func(ctx context.Context, ch, ts, txt string) error {
		postCalled = true
		return nil
	}
	h := buildPauseHinter(ddb, sb, postFn, func() time.Time { return nowT })

	if err := h.PostIfCooldownExpired(context.Background(), "C1", "1.0"); err != nil {
		t.Fatalf("LWT race must return nil, got: %v", err)
	}
	if postCalled {
		t.Error("Post must NOT be called when LWT race was lost")
	}
}

func TestDDBPauseHinter_UnknownChannel_NoOp(t *testing.T) {
	// Scenario: SandboxByChannel returns empty SandboxID → adapter returns nil.
	ddbCalled := false
	ddb := &mockDDBUpdateItem{
		mockDDBQueryGetPut: mockDDBQueryGetPut{
			getItem: func(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
				ddbCalled = true
				return &dynamodb.GetItemOutput{}, nil
			},
		},
	}
	sb := &fakeSandboxFetcher{info: bridge.SandboxRoutingInfo{}} // empty SandboxID
	postFn := func(ctx context.Context, ch, ts, txt string) error {
		t.Error("Post must NOT be called for unknown channel")
		return nil
	}
	h := buildPauseHinter(ddb, sb, postFn, nil)

	if err := h.PostIfCooldownExpired(context.Background(), "C_UNKNOWN", "1.0"); err != nil {
		t.Fatalf("unknown channel must return nil, got: %v", err)
	}
	if ddbCalled {
		t.Error("GetItem must NOT be called when channel resolves to no sandbox")
	}
}

func TestDDBPauseHinter_UpdateItemOtherError_BubblesUp(t *testing.T) {
	// Scenario: UpdateItem returns a non-conditional error → bubbles up, no Post.
	nowT := time.Unix(1_700_000_000, 0)
	postCalled := false
	ddb := &mockDDBUpdateItem{
		mockDDBQueryGetPut: mockDDBQueryGetPut{
			getItem: func(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
				return &dynamodb.GetItemOutput{Item: map[string]dynamodbtypes.AttributeValue{}}, nil
			},
		},
		updateItem: func(ctx context.Context, in *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
			return nil, fmt.Errorf("DDB ProvisionedThroughputExceeded")
		},
	}
	sb := &fakeSandboxFetcher{info: bridge.SandboxRoutingInfo{SandboxID: "sb-X"}}
	postFn := func(ctx context.Context, ch, ts, txt string) error {
		postCalled = true
		return nil
	}
	h := buildPauseHinter(ddb, sb, postFn, func() time.Time { return nowT })

	err := h.PostIfCooldownExpired(context.Background(), "C1", "1.0")
	if err == nil {
		t.Fatal("expected error from UpdateItem, got nil")
	}
	if !strings.Contains(err.Error(), "pause-hint") {
		t.Errorf("expected wrapped error, got: %v", err)
	}
	if postCalled {
		t.Error("Post must NOT be called when UpdateItem fails")
	}
}

// ============================================================
// Phase 67.2 shared test fixtures
// ============================================================

// recordingTransport is an http.RoundTripper used by Phase 67.2
// retry-loop tests. It records every request and replays a queue of
// canned responses. When the queue is exhausted, it returns a
// synthetic "connection closed" error so tests can model a server
// that drops the socket (TestReactor_NetworkError_Retries).
type recordingTransport struct {
	responses []func(r *http.Request) *http.Response
	requests  []*http.Request
	calls     int
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Capture request body (consume + restore so retries see the
	// same body shape).
	if r.Body != nil {
		buf, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(buf))
	}
	rt.requests = append(rt.requests, r)
	idx := rt.calls
	rt.calls++
	if idx >= len(rt.responses) {
		return nil, fmt.Errorf("recordingTransport: connection closed after %d responses", len(rt.responses))
	}
	return rt.responses[idx](r), nil
}

// canned builds a closure returning a fresh *http.Response on each
// call (the http.Client closes Body, so we cannot share a single
// *Response across attempts).
func canned(status int, headers map[string]string, body string) func(r *http.Request) *http.Response {
	return func(r *http.Request) *http.Response {
		resp := &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(bytes.NewReader([]byte(body))),
			Header:     make(http.Header),
			Request:    r,
		}
		for k, v := range headers {
			resp.Header.Set(k, v)
		}
		return resp
	}
}

// captureBridgeLogger installs a fresh slog text handler writing to a
// buffer at LevelDebug via bridge.SetLogger. Returns the buffer and a
// restore closure that must be deferred. Mirrors the pattern from
// handler_logging_test.go:25-34 (captureLogger).
func captureBridgeLogger(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	bridge.SetLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return &buf, func() {
		bridge.SetLogger(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	}
}

// assertContains is a small helper for log substring assertions.
func assertContains(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			t.Errorf("expected log output to contain %q, got:\n%s", n, haystack)
		}
	}
}

// ============================================================
// SlackReactorAdapter tests (Phase 67.1)
// ============================================================

func TestSlackReactorAdapter_HappyPath(t *testing.T) {
	var capturedPath string
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true}`)
	}))
	defer srv.Close()

	tokenFetcher := &bridge.SSMBotTokenFetcher{Client: &mockSSMClient{
		getParam: func(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
			return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String("xoxb-test")}}, nil
		},
	}, Path: "/km/slack/bot-token"}

	adapter := &bridge.SlackReactorAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Tokens: tokenFetcher}
	err := adapter.Add(context.Background(), "C01234567", "1714280400.001", "eyes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedPath != "/reactions.add" {
		t.Errorf("expected path /reactions.add, got %q", capturedPath)
	}
	if !strings.Contains(string(capturedBody), `"name":"eyes"`) {
		t.Errorf("expected name=eyes in body, got %s", capturedBody)
	}
	if !strings.Contains(string(capturedBody), `"timestamp":"1714280400.001"`) {
		t.Errorf("expected timestamp in body, got %s", capturedBody)
	}
}

func TestSlackReactorAdapter_AlreadyReacted_IsIdempotentSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":false,"error":"already_reacted"}`)
	}))
	defer srv.Close()

	tokenFetcher := &bridge.SSMBotTokenFetcher{Client: &mockSSMClient{
		getParam: func(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
			return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String("xoxb-test")}}, nil
		},
	}, Path: "/km/slack/bot-token"}

	adapter := &bridge.SlackReactorAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Tokens: tokenFetcher}
	err := adapter.Add(context.Background(), "C01234567", "1714280400.001", "eyes")
	if err != nil {
		t.Fatalf("expected nil error on already_reacted, got %v", err)
	}
}

func TestSlackReactorAdapter_GenericError_Propagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":false,"error":"invalid_name"}`)
	}))
	defer srv.Close()

	tokenFetcher := &bridge.SSMBotTokenFetcher{Client: &mockSSMClient{
		getParam: func(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
			return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String("xoxb-test")}}, nil
		},
	}, Path: "/km/slack/bot-token"}

	adapter := &bridge.SlackReactorAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Tokens: tokenFetcher}
	err := adapter.Add(context.Background(), "C01234567", "1714280400.001", ":eyes:")
	if err == nil {
		t.Fatal("expected error on invalid_name, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_name") {
		t.Errorf("expected error to mention invalid_name, got: %v", err)
	}
}

func TestSlackReactorAdapter_RateLimited(t *testing.T) {
	// Phase 67.2: with the new retry loop, the server must return 429
	// on EVERY call (not once-then-success). Otherwise the loop sleeps
	// ~5s between attempts and the test takes ~10s of wall-clock.
	// Inject a no-op Sleep stub so the test stays sub-millisecond
	// while still asserting the *ErrSlackRateLimited contract.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprintln(w, `{"ok":false,"error":"ratelimited"}`)
	}))
	defer srv.Close()

	tokenFetcher := &bridge.SSMBotTokenFetcher{Client: &mockSSMClient{
		getParam: func(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
			return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String("xoxb-test")}}, nil
		},
	}, Path: "/km/slack/bot-token"}

	adapter := &bridge.SlackReactorAdapter{
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
		Tokens:     tokenFetcher,
		Sleep:      func(time.Duration) {}, // Phase 67.2: skip real wall-clock
	}
	err := adapter.Add(context.Background(), "C01234567", "1714280400.001", "eyes")
	if err == nil {
		t.Fatal("expected ErrSlackRateLimited, got nil")
	}
	var rl *bridge.ErrSlackRateLimited
	if !errors.As(err, &rl) {
		t.Fatalf("expected *ErrSlackRateLimited, got %T: %v", err, err)
	}
	if rl.Method != "reactions.add" {
		t.Errorf("expected Method=reactions.add, got %q", rl.Method)
	}
	if rl.RetryAfterSeconds != 5 {
		t.Errorf("expected RetryAfterSeconds=5, got %d", rl.RetryAfterSeconds)
	}
}

// ============================================================
// Phase 67.2: SlackReactorAdapter retry-loop tests
//
// These 10 tests verify every behavior locked in 67.2-CONTEXT.md
// for the bounded retry loop in SlackReactorAdapter.Add. All tests
// use the recordingTransport / canned / captureBridgeLogger /
// assertContains fixtures added in plan 67.2-01.
// ============================================================

// reactorTokenFetcher is a small helper that returns an
// SSMBotTokenFetcher with a stub SSM client returning "xoxb-test".
// Each TestReactor_* test calls this to construct an adapter; keeping
// the token-fetcher boilerplate out of every test body keeps them
// scannable.
func reactorTokenFetcher() *bridge.SSMBotTokenFetcher {
	return &bridge.SSMBotTokenFetcher{
		Client: &mockSSMClient{
			getParam: func(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
				return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String("xoxb-test")}}, nil
			},
		},
		Path: "/km/slack/bot-token",
	}
}

// 1. internal_error twice → ok:true succeeds at attempt 3.
//
// Covers REQ-ACK-RETRY-BUDGET (success-after-retry),
// REQ-ACK-RETRY-JITTER-DETERMINISM, REQ-ACK-RETRY-LOGS (Debug).
func TestReactor_Retries_OnInternalError_ThenSucceeds(t *testing.T) {
	buf, restore := captureBridgeLogger(t)
	defer restore()

	rt := &recordingTransport{
		responses: []func(r *http.Request) *http.Response{
			canned(200, nil, `{"ok":false,"error":"internal_error"}`),
			canned(200, nil, `{"ok":false,"error":"internal_error"}`),
			canned(200, nil, `{"ok":true}`),
		},
	}
	var sleeps []time.Duration

	adapter := &bridge.SlackReactorAdapter{
		HTTPClient: &http.Client{Transport: rt},
		BaseURL:    "https://slack.test/api",
		Tokens:     reactorTokenFetcher(),
		Sleep:      func(d time.Duration) { sleeps = append(sleeps, d) },
		Rand:       rand.New(rand.NewSource(42)),
	}

	err := adapter.Add(context.Background(), "C01234567", "1714280400.001", "eyes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt.calls != 3 {
		t.Errorf("expected 3 transport calls, got %d", rt.calls)
	}
	if len(sleeps) != 2 {
		t.Fatalf("expected 2 sleeps, got %d: %v", len(sleeps), sleeps)
	}
	// Jitter ±25%: first sleep ∈ [150ms, 250ms] (200ms ± 25%)
	if sleeps[0] < 150*time.Millisecond || sleeps[0] > 250*time.Millisecond {
		t.Errorf("first sleep %v outside [150ms, 250ms]", sleeps[0])
	}
	// Second sleep ∈ [450ms, 750ms] (600ms ± 25%)
	if sleeps[1] < 450*time.Millisecond || sleeps[1] > 750*time.Millisecond {
		t.Errorf("second sleep %v outside [450ms, 750ms]", sleeps[1])
	}
	assertContains(t, buf.String(),
		"events: reaction retry", "attempt=1", "attempt=2", "next_delay_ms")
}

// 2. internal_error 4× (only 3 are consumed) → exhaustion error +
// Warn line.
//
// Covers REQ-ACK-RETRY-BUDGET (exhaustion), REQ-ACK-RETRY-LOGS (Warn).
func TestReactor_RetriesExhausted_ReturnsError(t *testing.T) {
	buf, restore := captureBridgeLogger(t)
	defer restore()

	rt := &recordingTransport{
		responses: []func(r *http.Request) *http.Response{
			canned(200, nil, `{"ok":false,"error":"internal_error"}`),
			canned(200, nil, `{"ok":false,"error":"internal_error"}`),
			canned(200, nil, `{"ok":false,"error":"internal_error"}`),
			canned(200, nil, `{"ok":false,"error":"internal_error"}`),
		},
	}
	adapter := &bridge.SlackReactorAdapter{
		HTTPClient: &http.Client{Transport: rt},
		BaseURL:    "https://slack.test/api",
		Tokens:     reactorTokenFetcher(),
		Sleep:      func(d time.Duration) {},
		Rand:       rand.New(rand.NewSource(42)),
	}

	err := adapter.Add(context.Background(), "C01234567", "1714280400.001", "eyes")
	if err == nil {
		t.Fatal("expected exhaustion error, got nil")
	}
	if rt.calls != 3 {
		t.Errorf("expected 3 transport calls, got %d", rt.calls)
	}
	if !strings.Contains(err.Error(), "internal_error") {
		t.Errorf("expected error to mention internal_error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "attempt=3 exhausted") {
		t.Errorf("expected error to mention attempt=3 exhausted, got: %v", err)
	}
	assertContains(t, buf.String(),
		"events: reaction failed", "attempt=3", "level=WARN")
}

// 3. message_not_found is terminal → 1 call, wrapped error, no retry.
//
// Covers REQ-ACK-RETRY-BUDGET (terminal-no-retry).
func TestReactor_TerminalError_NoRetry(t *testing.T) {
	rt := &recordingTransport{
		responses: []func(r *http.Request) *http.Response{
			canned(200, nil, `{"ok":false,"error":"message_not_found"}`),
		},
	}
	adapter := &bridge.SlackReactorAdapter{
		HTTPClient: &http.Client{Transport: rt},
		BaseURL:    "https://slack.test/api",
		Tokens:     reactorTokenFetcher(),
		Sleep:      func(d time.Duration) {},
	}

	err := adapter.Add(context.Background(), "C01234567", "1714280400.001", "eyes")
	if err == nil {
		t.Fatal("expected terminal error, got nil")
	}
	if rt.calls != 1 {
		t.Errorf("expected 1 transport call (no retry on terminal), got %d", rt.calls)
	}
	if !strings.Contains(err.Error(), "message_not_found") {
		t.Errorf("expected error to mention message_not_found, got: %v", err)
	}
}

// 4. invalid_auth → 1 call, Error-level log line, no retry.
//
// Covers REQ-ACK-RETRY-CLASSIFY (auth log level), REQ-ACK-RETRY-LOGS (Error).
func TestReactor_AuthError_NoRetry_LogsError(t *testing.T) {
	buf, restore := captureBridgeLogger(t)
	defer restore()

	rt := &recordingTransport{
		responses: []func(r *http.Request) *http.Response{
			canned(200, nil, `{"ok":false,"error":"invalid_auth"}`),
		},
	}
	adapter := &bridge.SlackReactorAdapter{
		HTTPClient: &http.Client{Transport: rt},
		BaseURL:    "https://slack.test/api",
		Tokens:     reactorTokenFetcher(),
		Sleep:      func(d time.Duration) {},
	}

	err := adapter.Add(context.Background(), "C01234567", "1714280400.001", "eyes")
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
	if rt.calls != 1 {
		t.Errorf("expected 1 transport call (no retry on auth), got %d", rt.calls)
	}
	if !strings.Contains(err.Error(), "invalid_auth") {
		t.Errorf("expected error to mention invalid_auth, got: %v", err)
	}
	assertContains(t, buf.String(),
		"events: reaction failed (auth)", "level=ERROR", "attempt=1")
}

// 5. 429 + Retry-After:1 → sleep exactly 1s (NOT jittered), then succeed.
//
// Covers REQ-ACK-RETRY-RETRYAFTER (honor).
func TestReactor_429_HonorsRetryAfter(t *testing.T) {
	rt := &recordingTransport{
		responses: []func(r *http.Request) *http.Response{
			canned(http.StatusTooManyRequests, map[string]string{"Retry-After": "1"},
				`{"ok":false,"error":"ratelimited"}`),
			canned(200, nil, `{"ok":true}`),
		},
	}
	var sleeps []time.Duration
	adapter := &bridge.SlackReactorAdapter{
		HTTPClient: &http.Client{Transport: rt},
		BaseURL:    "https://slack.test/api",
		Tokens:     reactorTokenFetcher(),
		Sleep:      func(d time.Duration) { sleeps = append(sleeps, d) },
		Rand:       rand.New(rand.NewSource(42)),
	}

	err := adapter.Add(context.Background(), "C01234567", "1714280400.001", "eyes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt.calls != 2 {
		t.Errorf("expected 2 transport calls, got %d", rt.calls)
	}
	if len(sleeps) != 1 {
		t.Fatalf("expected 1 sleep, got %d: %v", len(sleeps), sleeps)
	}
	// Retry-After overrides backoff schedule — exact 1s, no jitter.
	if sleeps[0] != 1*time.Second {
		t.Errorf("expected exactly 1s sleep (Retry-After honored), got %v", sleeps[0])
	}
}

// 6. 429 + Retry-After:30 with 2s ctx → return *ErrSlackRateLimited
// immediately, no sleep, no second attempt.
//
// Covers REQ-ACK-RETRY-RETRYAFTER (budget exceeded).
func TestReactor_429_RetryAfterExceedsBudget(t *testing.T) {
	rt := &recordingTransport{
		responses: []func(r *http.Request) *http.Response{
			canned(http.StatusTooManyRequests, map[string]string{"Retry-After": "30"},
				`{"ok":false,"error":"ratelimited"}`),
		},
	}
	var sleeps []time.Duration
	adapter := &bridge.SlackReactorAdapter{
		HTTPClient: &http.Client{Transport: rt},
		BaseURL:    "https://slack.test/api",
		Tokens:     reactorTokenFetcher(),
		Sleep:      func(d time.Duration) { sleeps = append(sleeps, d) },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := adapter.Add(ctx, "C01234567", "1714280400.001", "eyes")
	if err == nil {
		t.Fatal("expected ErrSlackRateLimited, got nil")
	}
	if rt.calls != 1 {
		t.Errorf("expected 1 transport call (Retry-After exceeds budget), got %d", rt.calls)
	}
	if len(sleeps) != 0 {
		t.Errorf("expected 0 sleeps (Retry-After exceeds budget), got %d: %v", len(sleeps), sleeps)
	}
	var rl *bridge.ErrSlackRateLimited
	if !errors.As(err, &rl) {
		t.Fatalf("expected *ErrSlackRateLimited, got %T: %v", err, err)
	}
	if rl.RetryAfterSeconds != 30 {
		t.Errorf("expected RetryAfterSeconds=30, got %d", rl.RetryAfterSeconds)
	}
}

// 7. ctx cancelled during first backoff → 1 call, return promptly.
//
// Covers REQ-ACK-RETRY-CTXCANCEL.
func TestReactor_ContextCanceled_StopsRetrying(t *testing.T) {
	rt := &recordingTransport{
		responses: []func(r *http.Request) *http.Response{
			canned(200, nil, `{"ok":false,"error":"internal_error"}`),
			canned(200, nil, `{"ok":false,"error":"internal_error"}`),
			canned(200, nil, `{"ok":false,"error":"internal_error"}`),
			canned(200, nil, `{"ok":false,"error":"internal_error"}`),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Sleep stub cancels ctx on its first invocation, simulating
	// cancellation during the first backoff. sleepWithCtx returns
	// ctx.Err() to the loop, which then returns lastErr promptly.
	cancelOnFirstSleep := func(d time.Duration) {
		cancel()
	}

	adapter := &bridge.SlackReactorAdapter{
		HTTPClient: &http.Client{Transport: rt},
		BaseURL:    "https://slack.test/api",
		Tokens:     reactorTokenFetcher(),
		Sleep:      cancelOnFirstSleep,
		Rand:       rand.New(rand.NewSource(42)),
	}

	err := adapter.Add(ctx, "C01234567", "1714280400.001", "eyes")
	if rt.calls != 1 {
		t.Errorf("expected 1 transport call (sleep cancelled before retry), got %d", rt.calls)
	}
	if err == nil {
		t.Fatal("expected error after ctx cancel, got nil")
	}
	// CONTEXT.md allows either lastErr OR ctx.Err() — both are
	// "promptly" returned.
}

// 8. Empty response queue → recordingTransport returns "connection closed"
// for every call. Loop retries 3× then exhausts.
//
// Covers REQ-ACK-RETRY-CLASSIFY (net-error), REQ-ACK-RETRY-BUDGET
// (exhaustion path for net errors).
func TestReactor_NetworkError_Retries(t *testing.T) {
	rt := &recordingTransport{
		responses: nil, // exhausted on every call
	}
	adapter := &bridge.SlackReactorAdapter{
		HTTPClient: &http.Client{Transport: rt},
		BaseURL:    "https://slack.test/api",
		Tokens:     reactorTokenFetcher(),
		Sleep:      func(d time.Duration) {},
		Rand:       rand.New(rand.NewSource(42)),
	}

	err := adapter.Add(context.Background(), "C01234567", "1714280400.001", "eyes")
	if err == nil {
		t.Fatal("expected network error, got nil")
	}
	if rt.calls != 3 {
		t.Errorf("expected 3 transport calls, got %d", rt.calls)
	}
	if !strings.Contains(err.Error(), "connection closed") {
		t.Errorf("expected error to mention 'connection closed', got: %v", err)
	}
	if !strings.Contains(err.Error(), "attempt=3 exhausted") {
		t.Errorf("expected error to mention attempt=3 exhausted, got: %v", err)
	}
}

// 9. already_reacted → idempotent success, 1 call, no error.
//
// Phase 67.1 idempotency regression guard. Covers
// REQ-ACK-RETRY-CLASSIFY (idempotency).
func TestReactor_AlreadyReacted_NoRetry_NoError(t *testing.T) {
	rt := &recordingTransport{
		responses: []func(r *http.Request) *http.Response{
			canned(200, nil, `{"ok":false,"error":"already_reacted"}`),
		},
	}
	adapter := &bridge.SlackReactorAdapter{
		HTTPClient: &http.Client{Transport: rt},
		BaseURL:    "https://slack.test/api",
		Tokens:     reactorTokenFetcher(),
		Sleep:      func(d time.Duration) {},
	}

	err := adapter.Add(context.Background(), "C01234567", "1714280400.001", "eyes")
	if err != nil {
		t.Fatalf("expected nil error on already_reacted, got %v", err)
	}
	if rt.calls != 1 {
		t.Errorf("expected 1 transport call, got %d", rt.calls)
	}
}

// 10. unknown error string → default-transient policy → retry 3× then
// exhaust.
//
// Covers REQ-ACK-RETRY-CLASSIFY (unknown→transient).
func TestReactor_UnknownErrorString_TreatedTransient(t *testing.T) {
	rt := &recordingTransport{
		responses: []func(r *http.Request) *http.Response{
			canned(200, nil, `{"ok":false,"error":"some_new_thing"}`),
			canned(200, nil, `{"ok":false,"error":"some_new_thing"}`),
			canned(200, nil, `{"ok":false,"error":"some_new_thing"}`),
		},
	}
	adapter := &bridge.SlackReactorAdapter{
		HTTPClient: &http.Client{Transport: rt},
		BaseURL:    "https://slack.test/api",
		Tokens:     reactorTokenFetcher(),
		Sleep:      func(d time.Duration) {},
		Rand:       rand.New(rand.NewSource(42)),
	}

	err := adapter.Add(context.Background(), "C01234567", "1714280400.001", "eyes")
	if err == nil {
		t.Fatal("expected exhaustion error on unknown code, got nil")
	}
	if rt.calls != 3 {
		t.Errorf("expected 3 transport calls (default-transient), got %d", rt.calls)
	}
	if !strings.Contains(err.Error(), "some_new_thing") {
		t.Errorf("expected error to mention some_new_thing, got: %v", err)
	}
	if !strings.Contains(err.Error(), "attempt=3 exhausted") {
		t.Errorf("expected error to mention attempt=3 exhausted, got: %v", err)
	}
}
