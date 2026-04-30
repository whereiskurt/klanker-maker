package bridge_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/whereiskurt/klankrmkr/pkg/slack/bridge"
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
