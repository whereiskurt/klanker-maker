// Package bridge — aws_adapters.go
// Production-backed implementations of the five bridge interfaces from Plan 03.
// These adapters wire real AWS services (DynamoDB for keys/nonces/channel lookup,
// SSM for the bot token) into the bridge.Handler used by the km-slack-bridge Lambda.
//
// Design notes:
//   - DynamoPublicKeyFetcher: uses pkg/aws.FetchPublicKey against km-identities
//     (RESEARCH.md correction #1: NOT SSM).
//   - DynamoNonceStore: uses DynamoDB conditional write (attribute_not_exists)
//     for atomic replay protection; TTL on ttl_expiry.
//   - DynamoChannelOwnershipFetcher: reads slack_channel_id from km-sandboxes.
//   - SSMBotTokenFetcher: reads /km/slack/bot-token (SecureString); 15-min cache.
//   - SlackPosterAdapter: thin direct HTTP (not via pkg/slack.Client) to expose
//     Retry-After headers from 429 responses cleanly.
package bridge

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ============================================================
// Narrow DynamoDB interface for adapters
// ============================================================

// DynamoGetPutter is the minimal DynamoDB interface used by the adapters.
// Both *dynamodb.Client and mock implementations satisfy it.
type DynamoGetPutter interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
}

// BotTokenSSMClient is the minimal SSM interface used by SSMBotTokenFetcher.
type BotTokenSSMClient interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// ============================================================
// DynamoPublicKeyFetcher
// ============================================================

// DynamoPublicKeyFetcher implements PublicKeyFetcher using DynamoDB km-identities.
// Uses pkg/aws.FetchPublicKey — NOT SSM (RESEARCH.md correction #1).
type DynamoPublicKeyFetcher struct {
	Client    DynamoGetPutter
	TableName string // "km-identities"
}

// Fetch retrieves a sandbox's Ed25519 public key from DynamoDB km-identities.
// Returns ErrSenderNotFound if no identity row exists for senderID.
func (f *DynamoPublicKeyFetcher) Fetch(ctx context.Context, senderID string) (ed25519.PublicKey, error) {
	// FetchPublicKey is the canonical pkg/aws function for identity lookup.
	// It calls GetItem on the identities table keyed by sandbox_id.
	rec, err := kmaws.FetchPublicKey(ctx, &identityTableAdapter{client: f.Client}, f.TableName, senderID)
	if err != nil {
		return nil, fmt.Errorf("bridge: public key lookup for %s: %w", senderID, err)
	}
	if rec == nil {
		// FetchPublicKey returns (nil, nil) when no item found.
		return nil, ErrSenderNotFound
	}
	if rec.PublicKeyB64 == "" {
		return nil, ErrSenderNotFound
	}

	keyBytes, err := base64.StdEncoding.DecodeString(rec.PublicKeyB64)
	if err != nil {
		return nil, fmt.Errorf("bridge: decode public key for %s: %w", senderID, err)
	}
	if len(keyBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("bridge: invalid public key size %d for %s", len(keyBytes), senderID)
	}
	return ed25519.PublicKey(keyBytes), nil
}

// identityTableAdapter bridges DynamoGetPutter to kmaws.IdentityTableAPI.
// kmaws.FetchPublicKey requires GetItem, PutItem (PutItem unused here),
// GetItem, and DeleteItem. We provide a wrapper that satisfies the interface.
type identityTableAdapter struct {
	client DynamoGetPutter
}

func (a *identityTableAdapter) PutItem(ctx context.Context, input *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return a.client.PutItem(ctx, input, optFns...)
}

func (a *identityTableAdapter) GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return a.client.GetItem(ctx, input, optFns...)
}

func (a *identityTableAdapter) DeleteItem(ctx context.Context, input *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	// Not used by FetchPublicKey — stub returns empty output.
	return &dynamodb.DeleteItemOutput{}, nil
}

// ============================================================
// DynamoNonceStore
// ============================================================

// DynamoNonceStore implements NonceStore using DynamoDB km-slack-bridge-nonces.
// Atomic conditional write with attribute_not_exists guarantees replay-safety.
type DynamoNonceStore struct {
	Client    DynamoGetPutter
	TableName string // "km-slack-bridge-nonces"
}

// Reserve inserts nonce atomically. Returns ErrNonceReplayed if already present.
// ttlSeconds controls DynamoDB TTL on ttl_expiry attribute.
func (s *DynamoNonceStore) Reserve(ctx context.Context, nonce string, ttlSeconds int) error {
	ttlExpiry := time.Now().Unix() + int64(ttlSeconds)

	_, err := s.Client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: awssdk.String(s.TableName),
		Item: map[string]dynamodbtypes.AttributeValue{
			"nonce": &dynamodbtypes.AttributeValueMemberS{Value: nonce},
			"ttl_expiry": &dynamodbtypes.AttributeValueMemberN{
				Value: strconv.FormatInt(ttlExpiry, 10),
			},
		},
		ConditionExpression: awssdk.String("attribute_not_exists(nonce)"),
	})
	if err != nil {
		var condFailed *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &condFailed) {
			return ErrNonceReplayed
		}
		return fmt.Errorf("bridge: reserve nonce: %w", err)
	}
	return nil
}

// ============================================================
// DynamoChannelOwnershipFetcher
// ============================================================

// DynamoChannelOwnershipFetcher implements ChannelOwnershipFetcher using DynamoDB km-sandboxes.
type DynamoChannelOwnershipFetcher struct {
	Client    DynamoGetPutter
	TableName string // "km-sandboxes"
}

// OwnedChannel reads the slack_channel_id field from the sandbox's metadata row.
// Returns "" (empty) if the sandbox has no channel configured.
func (f *DynamoChannelOwnershipFetcher) OwnedChannel(ctx context.Context, sandboxID string) (string, error) {
	out, err := f.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(f.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
	})
	if err != nil {
		return "", fmt.Errorf("bridge: channel lookup for %s: %w", sandboxID, err)
	}
	if len(out.Item) == 0 {
		return "", nil
	}
	if v, ok := out.Item["slack_channel_id"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			return sv.Value, nil
		}
	}
	return "", nil
}

// ============================================================
// SSMBotTokenFetcher
// ============================================================

// defaultTokenCacheTTL is the default in-process token cache duration.
// Matches RESEARCH.md recommendation: 15 minutes.
const defaultTokenCacheTTL = 15 * time.Minute

// tokenCache holds a cached token and its expiry time.
type tokenCache struct {
	token  string
	expiry time.Time
}

// SSMBotTokenFetcher implements BotTokenFetcher with a 15-minute in-process cache.
// Thread-safe via a Mutex. The CacheTTL field may be set to a custom duration
// (e.g. time.Millisecond for tests).
type SSMBotTokenFetcher struct {
	Client   BotTokenSSMClient
	Path     string        // e.g. "/km/slack/bot-token"
	CacheTTL time.Duration // defaults to defaultTokenCacheTTL (15 min)

	mu    sync.Mutex
	cache tokenCache
}

// Fetch returns the bot token, using the in-process cache when valid.
// SSM GetParameter is called only on cache miss or expiry.
func (f *SSMBotTokenFetcher) Fetch(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ttl := f.CacheTTL
	if ttl == 0 {
		ttl = defaultTokenCacheTTL
	}

	if f.cache.token != "" && time.Now().Before(f.cache.expiry) {
		return f.cache.token, nil
	}

	out, err := f.Client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(f.Path),
		WithDecryption: awssdk.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("bridge: fetch bot token from SSM %s: %w", f.Path, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("bridge: SSM parameter %s has no value", f.Path)
	}

	token := *out.Parameter.Value
	f.cache = tokenCache{
		token:  token,
		expiry: time.Now().Add(ttl),
	}
	return token, nil
}

// ============================================================
// SlackPosterAdapter
// ============================================================

// slackAPIResponse is the subset of Slack API response fields used by SlackPosterAdapter.
type slackAPIResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	TS    string `json:"ts,omitempty"`
}

// SlackPosterAdapter implements SlackPoster via direct HTTP (not pkg/slack.Client)
// so it can inspect Retry-After headers from 429 responses cleanly.
//
// Design decision (logged in SUMMARY.md): Option B — adapter owns thin HTTP
// path rather than extending pkg/slack.Client. This keeps the Client API surface
// stable and avoids an awkward back-channel for header exposure.
type SlackPosterAdapter struct {
	HTTPClient *http.Client
	BaseURL    string            // defaults to "https://slack.com/api"; overridden in tests
	Tokens     BotTokenFetcher   // fetches and caches the bot token
}

// getBaseURL returns the effective base URL.
func (s *SlackPosterAdapter) getBaseURL() string {
	if s.BaseURL != "" {
		return s.BaseURL
	}
	return "https://slack.com/api"
}

// call makes a Slack Web API call and returns the parsed response + raw HTTP response.
// On HTTP 429, sets RetryAfter from the Retry-After header.
func (s *SlackPosterAdapter) call(ctx context.Context, method string, payload any) (*slackAPIResponse, int, string, error) {
	token, err := s.Tokens.Fetch(ctx)
	if err != nil {
		return nil, 0, "", fmt.Errorf("bridge: get bot token for %s: %w", method, err)
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, "", fmt.Errorf("bridge: marshal %s: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.getBaseURL()+"/"+method, bytes.NewReader(b))
	if err != nil {
		return nil, 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	hc := s.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	defer resp.Body.Close()

	retryAfterHeader := resp.Header.Get("Retry-After")

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, retryAfterHeader, fmt.Errorf("bridge: read %s response: %w", method, err)
	}

	var apiResp slackAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, resp.StatusCode, retryAfterHeader, fmt.Errorf("bridge: decode %s: %w", method, err)
	}
	return &apiResp, resp.StatusCode, retryAfterHeader, nil
}

// checkRateLimit checks for Slack 429 and returns ErrSlackRateLimited when found.
func checkRateLimit(httpStatus int, retryAfterHeader, method string) error {
	if httpStatus == http.StatusTooManyRequests {
		retryAfter := 1 // default
		if retryAfterHeader != "" {
			if n, err := strconv.Atoi(retryAfterHeader); err == nil && n > 0 {
				retryAfter = n
			}
		}
		return &ErrSlackRateLimited{RetryAfterSeconds: retryAfter, Method: method}
	}
	return nil
}

// PostMessage posts to a Slack channel. Returns the message ts on success.
func (s *SlackPosterAdapter) PostMessage(ctx context.Context, channel, subject, body, threadTS string) (string, error) {
	payload := map[string]any{
		"channel":      channel,
		"text":         fmt.Sprintf("*%s*\n\n%s", subject, body),
		"unfurl_links": false,
		"unfurl_media": false,
		"mrkdwn":       true,
	}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}

	resp, httpStatus, retryAfter, err := s.call(ctx, "chat.postMessage", payload)
	if err != nil {
		return "", err
	}
	if rlErr := checkRateLimit(httpStatus, retryAfter, "chat.postMessage"); rlErr != nil {
		return "", rlErr
	}
	if !resp.OK {
		return "", fmt.Errorf("bridge: chat.postMessage: %s", resp.Error)
	}
	return resp.TS, nil
}

// ArchiveChannel archives a Slack channel via conversations.archive.
func (s *SlackPosterAdapter) ArchiveChannel(ctx context.Context, channelID string) error {
	resp, httpStatus, retryAfter, err := s.call(ctx, "conversations.archive", map[string]any{
		"channel": channelID,
	})
	if err != nil {
		return err
	}
	if rlErr := checkRateLimit(httpStatus, retryAfter, "conversations.archive"); rlErr != nil {
		return rlErr
	}
	if !resp.OK {
		return fmt.Errorf("bridge: conversations.archive: %s", resp.Error)
	}
	return nil
}
