// Package bridge — aws_adapters.go
// Production-backed implementations of the bridge interfaces from Plans 03 and 05.
// These adapters wire real AWS services (DynamoDB for keys/nonces/channel lookup,
// SSM for the bot token and signing secret, SQS for inbound queue delivery)
// into the bridge.Handler and bridge.EventsHandler used by the km-slack-bridge Lambda.
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
//
// Plan 67-05 additions:
//   - SQSAdapter: sends inbound messages to per-sandbox FIFO queues.
//   - DDBThreadStore: reads/writes km-slack-threads (idempotent Upsert via
//     attribute_not_exists condition to avoid bridge↔poller race).
//   - DDBSandboxByChannel: queries km-sandboxes via slack_channel_id-index GSI.
//   - SSMSigningSecretFetcher: reads /km/slack/signing-secret; 15-min cache
//     (mirrors SSMBotTokenFetcher exactly).
//   - CachedBotUserIDFetcher: calls auth.test once per Lambda warm lifetime.
//   - DDBPauseHinter: posts a one-time "paused; queued" hint to Slack with a
//     1h cooldown backed by last_pause_hint_ts on the km-sandboxes row (LWT).
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
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	pkgslack "github.com/whereiskurt/klanker-maker/pkg/slack"
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
	TableName string // e.g. "km-identities" (from KM_IDENTITIES_TABLE env var)
}

// Fetch retrieves a sandbox's Ed25519 public key from DynamoDB (table set via KM_IDENTITIES_TABLE).
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
	TableName string // e.g. "km-slack-bridge-nonces" (from KM_NONCE_TABLE env var)
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
	TableName string // e.g. "km-sandboxes" (from KM_SANDBOX_TABLE_NAME env var)
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
	Path     string        // e.g. "/km/slack/bot-token" (from KM_BOT_TOKEN_PATH env var)
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
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	TS        string `json:"ts,omitempty"`
	Permalink string `json:"permalink,omitempty"` // Phase 70 — chat.getPermalink response
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
// An empty subject renders the body alone (no bold header) — useful for
// per-sandbox threaded replies where the channel already conveys context.
func (s *SlackPosterAdapter) PostMessage(ctx context.Context, channel, subject, body, threadTS string) (string, error) {
	text := body
	if subject != "" {
		text = fmt.Sprintf("*%s*\n\n%s", subject, body)
	}
	payload := map[string]any{
		"channel":      channel,
		"text":         text,
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

// PostMessageBlocks posts to a Slack channel with both a plain-text fallback and a
// pre-serialized Block Kit JSON array. The `text:` field is required by Slack for
// push notifications and accessibility even when blocks are present. The subject is
// intentionally ignored — Block Kit posts express headers via the header block itself.
//
// Rate-limit handling and error mapping are identical to PostMessage.
func (s *SlackPosterAdapter) PostMessageBlocks(ctx context.Context, channel, subject, body, blocksJSON, threadTS string) (string, error) {
	payload := map[string]any{
		"channel":      channel,
		"text":         body, // plain-text fallback for push/search (required by Slack)
		"blocks":       json.RawMessage(blocksJSON),
		"unfurl_links": false,
		"unfurl_media": false,
		"mrkdwn":       true,
	}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}
	// subject is intentionally NOT in the payload — Block Kit header blocks express it.

	resp, httpStatus, retryAfter, err := s.call(ctx, "chat.postMessage", payload)
	if err != nil {
		return "", err
	}
	if rlErr := checkRateLimit(httpStatus, retryAfter, "chat.postMessage"); rlErr != nil {
		return "", rlErr
	}
	if !resp.OK {
		return "", fmt.Errorf("bridge: chat.postMessage (blocks): %s", resp.Error)
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

// GetPermalink returns a Slack permalink URL for the given channel + message ts.
// Wraps chat.getPermalink. Phase 70 — used by Plan 70-06 cross-agent switch.
//
// Uses GET with query-string arguments (NOT POST + JSON like the other methods on
// this adapter). chat.getPermalink is one of Slack's older read-only methods that
// rejects application/json bodies — sending JSON yielded a silent empty-permalink
// response, surfacing in UAT as the literal "(unavailable)" fallback string in the
// cross-agent switch handoff post. GET + query string matches the slack-go SDK's
// convention and Slack's own docs example for this method.
func (s *SlackPosterAdapter) GetPermalink(ctx context.Context, channel, messageTS string) (string, error) {
	token, err := s.Tokens.Fetch(ctx)
	if err != nil {
		return "", fmt.Errorf("bridge: get bot token for chat.getPermalink: %w", err)
	}

	q := url.Values{}
	q.Set("channel", channel)
	q.Set("message_ts", messageTS)

	req, err := http.NewRequestWithContext(ctx, "GET", s.getBaseURL()+"/chat.getPermalink?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	hc := s.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}

	httpResp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer httpResp.Body.Close()

	if rlErr := checkRateLimit(httpResp.StatusCode, httpResp.Header.Get("Retry-After"), "chat.getPermalink"); rlErr != nil {
		return "", rlErr
	}

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", fmt.Errorf("bridge: read chat.getPermalink response: %w", err)
	}
	var apiResp slackAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return "", fmt.Errorf("bridge: decode chat.getPermalink: %w", err)
	}
	if !apiResp.OK {
		return "", fmt.Errorf("bridge: chat.getPermalink: %s", apiResp.Error)
	}
	return apiResp.Permalink, nil
}

// UpdateMessage edits a previously-posted bot message via chat.update.
// Phase 70 — used by Plan 70-06's optional handoff-edit path.
func (s *SlackPosterAdapter) UpdateMessage(ctx context.Context, channel, ts, text string) (string, error) {
	resp, httpStatus, retryAfter, err := s.call(ctx, "chat.update", map[string]any{
		"channel": channel,
		"ts":      ts,
		"text":    text,
	})
	if err != nil {
		return "", err
	}
	if rlErr := checkRateLimit(httpStatus, retryAfter, "chat.update"); rlErr != nil {
		return "", rlErr
	}
	if !resp.OK {
		return "", fmt.Errorf("bridge: chat.update: %s", resp.Error)
	}
	return resp.TS, nil
}

// ============================================================
// Phase 67.2: reactions.add error classifier (pure helper)
// ============================================================

// reactionErrorClass categorizes a reactions.add response so the
// retry loop in SlackReactorAdapter.Add can decide whether to
// succeed, give up, or retry with backoff.
//
// Locked taxonomy: 67.2-CONTEXT.md § "Error classification (locked)".
// Default-unknown policy: an unknown apiErr string returns
// classTransient (one extra retry on an actually-terminal error
// is cheap; silently ignoring a new transient signal is not).
type reactionErrorClass int

const (
	classSuccess          reactionErrorClass = iota
	classTerminalAuth     // operator action required (token rotation, scope grant) — log at Error
	classTerminalBadInput // unrecoverable client-side error — log at Warn (final give-up)
	classTransient        // retryable: 5xx, net error, internal_error, unknown error string
	classRateLimited      // 429 with Retry-After header — honor RetryAfterSeconds
)

// classifyReactionError returns the appropriate retry bucket for a
// reactions.add response. Pure function — no I/O, no logging.
// Enumerates the codes in 67.2-CONTEXT.md's locked taxonomy plus the
// additional codes 67.2-RESEARCH.md identified from Slack docs; any
// unrecognized apiErr falls through to classTransient (locked
// default).
func classifyReactionError(httpStatus int, apiErr string, netErr error) reactionErrorClass {
	if netErr != nil {
		return classTransient
	}
	if httpStatus == http.StatusTooManyRequests {
		return classRateLimited
	}
	if httpStatus >= 500 && httpStatus < 600 {
		return classTransient
	}
	if httpStatus == http.StatusOK && (apiErr == "" || apiErr == "already_reacted") {
		return classSuccess
	}
	switch apiErr {
	case "invalid_auth", "not_authed", "account_inactive",
		"token_revoked", "missing_scope", "token_expired",
		"no_permission", "access_denied", "ekm_access_denied",
		"enterprise_is_restricted", "org_login_required",
		"two_factor_setup_required":
		return classTerminalAuth
	case "bad_timestamp", "message_not_found", "channel_not_found",
		"not_reactable", "thread_locked", "invalid_name",
		"too_many_emoji", "too_many_reactions", "is_archived",
		"invalid_arg_name", "invalid_arguments", "invalid_charset",
		"invalid_form_data", "invalid_post_type",
		"missing_post_type", "no_item_specified",
		"not_allowed_token_type", "no_access":
		return classTerminalBadInput
	case "internal_error", "service_unavailable", "fatal_error",
		"request_timeout", "ratelimited", "accesslimited",
		"team_access_not_granted", "team_added_to_org",
		"external_channel_migrating":
		return classTransient
	}
	// Default for unknown error strings — locked CONTEXT.md policy.
	return classTransient
}

// ============================================================
// Phase 67.1: SlackReactorAdapter — Reactor implementation
// ============================================================

// SlackReactorAdapter implements Reactor via direct HTTP to Slack's reactions.add Web API.
// Mirrors SlackPosterAdapter shape for consistency. Treats already_reacted as
// idempotent success because Slack delivers events at-least-once.
//
// Phase 67.2 added a bounded retry loop (max 3 attempts, 200ms→600ms ± 25%
// jitter, Retry-After honoring, ctx-cancellable sleeps) on top of the existing
// single-attempt shape. The classifier in classifyReactionError decides which
// responses are transient (retry) vs terminal (give up). See 67.2-CONTEXT.md
// for the locked taxonomy and 67.2-RESEARCH.md for the in-repo reference
// pattern at pkg/slack/client.go:404-419.
type SlackReactorAdapter struct {
	HTTPClient *http.Client
	BaseURL    string          // defaults to "https://slack.com/api"; overridden in tests
	Tokens     BotTokenFetcher // SHARE with SlackPosterAdapter to reuse the 15-min token cache

	// Phase 67.2: Sleep, if non-nil, is called instead of <-time.After
	// during backoff. Tests set this to a stub that records sleeps
	// without actually sleeping. nil → use real time.NewTimer+select.
	Sleep func(d time.Duration)

	// Phase 67.2: Rand, if non-nil, supplies the jitter PRNG. Tests
	// inject a *rand.Rand with a fixed seed for deterministic backoff
	// durations. nil → use math/rand's default global source
	// (goroutine-safe; auto-seeded in go 1.20+).
	//
	// math/rand (NOT crypto/rand) is the correct choice — jitter is
	// de-correlation, not a security primitive.
	Rand *rand.Rand
}

func (s *SlackReactorAdapter) getBaseURL() string {
	if s.BaseURL != "" {
		return s.BaseURL
	}
	return "https://slack.com/api"
}

// doOneAttempt runs ONE HTTP request to reactions.add. Returns the
// parsed slackAPIResponse, the HTTP status code, the raw Retry-After
// header string, and any non-nil network error from http.Client.Do or
// response read/decode. Extracted from Add so the per-iteration
// resp.Body.Close defer fires PER attempt, not stacked at function
// return (67.2-RESEARCH.md Pitfall 4).
func (s *SlackReactorAdapter) doOneAttempt(ctx context.Context, token string, body []byte) (*slackAPIResponse, int, string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST",
		s.getBaseURL()+"/reactions.add", bytes.NewReader(body))
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

	retryAfter := resp.Header.Get("Retry-After")
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, retryAfter,
			fmt.Errorf("bridge: read reactions.add response: %w", err)
	}
	var r slackAPIResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, resp.StatusCode, retryAfter,
			fmt.Errorf("bridge: decode reactions.add: %w", err)
	}
	return &r, resp.StatusCode, retryAfter, nil
}

// sleepWithCtx sleeps for d or until ctx is cancelled. Returns
// ctx.Err() if cancelled, nil if the sleep completed.
//
// If s.Sleep is non-nil (test injection), calls s.Sleep(d) and returns
// ctx.Err() — tests use this to fast-forward sleeps while still
// respecting ctx cancellation.
func (s *SlackReactorAdapter) sleepWithCtx(ctx context.Context, d time.Duration) error {
	if s.Sleep != nil {
		s.Sleep(d)
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// withJitter returns d ± 25%. If r is non-nil (test injection), uses r
// for the random factor; otherwise uses math/rand's package-level safe
// source (auto-seeded in go 1.20+). Maps a Float64() roll in [0.0, 1.0)
// to a multiplier in [0.75, 1.25).
//
// math/rand (NOT crypto/rand) is the correct choice — jitter is
// de-correlation, not a security primitive.
func withJitter(d time.Duration, r *rand.Rand) time.Duration {
	var f float64
	if r != nil {
		f = r.Float64()
	} else {
		f = rand.Float64()
	}
	factor := 1.0 + (f-0.5)*0.5
	return time.Duration(float64(d) * factor)
}

// Add posts a reaction to a Slack message with bounded retry on transient
// failures. Returns nil on success or already_reacted (idempotent).
// Returns *ErrSlackRateLimited when the Retry-After header exceeds the
// remaining ctx budget or when retries are exhausted on a 429. Returns
// a wrapped error for any other terminal or exhaustion case.
//
// emoji must be the bare emoji name without colons ("eyes", NOT ":eyes:").
// ts must be the originating message's TS field, NOT the thread root.
//
// Retry policy (locked in 67.2-CONTEXT.md):
//   - Max 3 attempts (1 initial + 2 retries) on classTransient outcomes
//   - Backoff schedule: 200ms then 600ms, each ± 25% jitter
//   - Retry-After header overrides the backoff schedule on 429
//   - If Retry-After > remaining ctx budget → return *ErrSlackRateLimited
//     immediately without sleeping
//   - All sleeps are cancellable via ctx.Done()
//   - Auth-class errors return on FIRST attempt (no retry) with Error log
//   - Bad-input errors return on FIRST attempt (no retry, no extra log —
//     the handler-side at events_handler.go:238 already Warns)
//   - Retry exhaustion logs ONE Warn line with attempt=3
//   - Intermediate retries log ONE Debug line each with attempt + next_delay_ms
func (s *SlackReactorAdapter) Add(ctx context.Context, channel, ts, emoji string) error {
	token, err := s.Tokens.Fetch(ctx)
	if err != nil {
		return fmt.Errorf("bridge: get bot token for reactions.add: %w", err)
	}

	payload := map[string]any{
		"channel":   channel,
		"timestamp": ts,
		"name":      emoji,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("bridge: marshal reactions.add: %w", err)
	}

	const maxAttempts = 3
	baseDelays := [2]time.Duration{
		200 * time.Millisecond,
		600 * time.Millisecond,
	}
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		apiResp, httpStatus, retryAfter, netErr := s.doOneAttempt(ctx, token, body)
		var apiErr string
		if apiResp != nil {
			apiErr = apiResp.Error
		}
		class := classifyReactionError(httpStatus, apiErr, netErr)

		switch class {
		case classSuccess:
			return nil

		case classTerminalAuth:
			lastErr = fmt.Errorf("bridge: reactions.add: %s", apiErr)
			logger.Error("events: reaction failed (auth)",
				"channel", channel, "ts", ts, "emoji", emoji,
				"attempt", attempt, "err", lastErr)
			return lastErr

		case classTerminalBadInput:
			// No log here — the handler-side at events_handler.go:238
			// already Warns on any error. Returning the wrapped error
			// keeps the existing "bridge: reactions.add: <code>" grep
			// target intact.
			return fmt.Errorf("bridge: reactions.add: %s", apiErr)

		case classTransient:
			if netErr != nil {
				lastErr = netErr
			} else {
				lastErr = fmt.Errorf("bridge: reactions.add: %s", apiErr)
			}
			if attempt < maxAttempts {
				d := withJitter(baseDelays[attempt-1], s.Rand)
				logger.Debug("events: reaction retry",
					"channel", channel, "ts", ts, "emoji", emoji,
					"attempt", attempt, "err", lastErr,
					"next_delay_ms", d.Milliseconds())
				if err := s.sleepWithCtx(ctx, d); err != nil {
					return lastErr
				}
				continue
			}
			// attempt == maxAttempts: fall through to final Warn.

		case classRateLimited:
			ra := 1
			if n, e := strconv.Atoi(retryAfter); e == nil && n > 0 {
				ra = n
			}
			rl := &ErrSlackRateLimited{RetryAfterSeconds: ra, Method: "reactions.add"}
			lastErr = rl
			// If RetryAfter exceeds remaining ctx budget, give up
			// immediately. Locked policy in CONTEXT.md.
			if dl, ok := ctx.Deadline(); ok {
				if time.Duration(ra)*time.Second > time.Until(dl) {
					return rl
				}
			}
			if attempt < maxAttempts {
				if err := s.sleepWithCtx(ctx, time.Duration(ra)*time.Second); err != nil {
					return lastErr
				}
				continue
			}
			// attempt == maxAttempts: fall through to final Warn.
		}
	}

	logger.Warn("events: reaction failed",
		"channel", channel, "ts", ts, "emoji", emoji,
		"attempt", maxAttempts, "err", lastErr)
	return fmt.Errorf("%w (attempt=%d exhausted)", lastErr, maxAttempts)
}

// ============================================================
// Plan 67-05: SQSAdapter — SQSSender implementation
// ============================================================

// SQSSendMessageAPI is the narrow SQS interface used by SQSAdapter.
// Both *sqs.Client and mock implementations satisfy it.
type SQSSendMessageAPI interface {
	SendMessage(ctx context.Context, in *sqs.SendMessageInput, opts ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// SQSAdapter implements SQSSender by forwarding to a FIFO SQS queue.
// MessageGroupId is the sandboxID; MessageDeduplicationId is the Slack event_id
// (or msg.TS when event_id is absent). Both are required for FIFO queues.
type SQSAdapter struct {
	Client SQSSendMessageAPI
}

// Send delivers body to queueURL as a FIFO message. groupID and dedupID are
// mandatory for FIFO queues; empty queueURL is rejected immediately.
func (a *SQSAdapter) Send(ctx context.Context, queueURL, body, groupID, dedupID string) error {
	if queueURL == "" {
		return fmt.Errorf("sqs send: empty queue url")
	}
	_, err := a.Client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               awssdk.String(queueURL),
		MessageBody:            awssdk.String(body),
		MessageGroupId:         awssdk.String(groupID),
		MessageDeduplicationId: awssdk.String(dedupID),
	})
	if err != nil {
		return fmt.Errorf("sqs send to %s: %w", queueURL, err)
	}
	return nil
}

// ============================================================
// Plan 67-05: DDBThreadStore — SlackThreadStore implementation
// ============================================================

// DDBQueryGetPutAPI extends DynamoGetPutter with Query, which is required by
// both DDBThreadStore and DDBSandboxByChannel (but NOT DynamoNonceStore or
// DynamoChannelOwnershipFetcher, which only need GetItem/PutItem).
type DDBQueryGetPutAPI interface {
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// DDBThreadStore implements SlackThreadStore using the km-slack-threads DynamoDB table.
// Key: (channel_id, thread_ts). Upsert uses attribute_not_exists(channel_id) so
// the bridge never overwrites a claude_session_id written by the poller.
type DDBThreadStore struct {
	Client    DDBQueryGetPutAPI
	TableName string // e.g. "km-slack-threads" (from KM_SLACK_THREADS_TABLE env var)
}

// Get returns the claude_session_id for (channelID, threadTS), or "" if absent.
func (s *DDBThreadStore) Get(ctx context.Context, channelID, threadTS string) (string, error) {
	out, err := s.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(s.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: channelID},
			"thread_ts":  &dynamodbtypes.AttributeValueMemberS{Value: threadTS},
		},
	})
	if err != nil {
		return "", fmt.Errorf("threads get: %w", err)
	}
	if v, ok := out.Item["claude_session_id"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			return sv.Value, nil
		}
	}
	return "", nil
}

// LookupSandbox returns the sandbox_id for (channelID, threadTS), or "" if no
// row exists. Phase 91.3: distinct from Get because Get returns
// claude_session_id (which the poller sets later) — sandbox_id is set by
// Upsert at dispatch time, so this returns non-empty as soon as the first
// mention-triggered dispatch enqueues, enabling the events handler to bypass
// the mention-only filter on subsequent replies in the same thread.
func (s *DDBThreadStore) LookupSandbox(ctx context.Context, channelID, threadTS string) (string, error) {
	out, err := s.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(s.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: channelID},
			"thread_ts":  &dynamodbtypes.AttributeValueMemberS{Value: threadTS},
		},
	})
	if err != nil {
		return "", fmt.Errorf("threads lookup-sandbox: %w", err)
	}
	if v, ok := out.Item["sandbox_id"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			return sv.Value, nil
		}
	}
	return "", nil
}

// Upsert creates a new thread row keyed by (channelID, threadTS) only if one
// does not already exist (attribute_not_exists condition). ConditionalCheckFailed
// means the row already exists — this is the idempotent success path; we MUST NOT
// overwrite claude_session_id set by the poller.
func (s *DDBThreadStore) Upsert(ctx context.Context, channelID, threadTS, sandboxID string) error {
	now := time.Now()
	nowISO := now.UTC().Format(time.RFC3339)
	ttlExpiry := strconv.FormatInt(now.Add(30*24*time.Hour).Unix(), 10)

	_, err := s.Client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: awssdk.String(s.TableName),
		Item: map[string]dynamodbtypes.AttributeValue{
			"channel_id":   &dynamodbtypes.AttributeValueMemberS{Value: channelID},
			"thread_ts":    &dynamodbtypes.AttributeValueMemberS{Value: threadTS},
			"sandbox_id":   &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
			"created_at":   &dynamodbtypes.AttributeValueMemberS{Value: nowISO},
			"last_turn_ts": &dynamodbtypes.AttributeValueMemberS{Value: nowISO},
			"turn_count":   &dynamodbtypes.AttributeValueMemberN{Value: "0"},
			"ttl_expiry":   &dynamodbtypes.AttributeValueMemberN{Value: ttlExpiry},
		},
		ConditionExpression: awssdk.String("attribute_not_exists(channel_id)"),
	})
	if err != nil {
		// ConditionalCheckFailed = row already exists (bridge↔poller race or
		// duplicate delivery). This is the idempotent success path.
		var ccfe *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccfe) {
			return nil
		}
		return fmt.Errorf("threads upsert: %w", err)
	}
	return nil
}

// ============================================================
// Plan 67-05: DDBSandboxByChannel — SandboxByChannelFetcher implementation
// ============================================================

// DDBSandboxByChannel implements SandboxByChannelFetcher using the
// slack_channel_id-index GSI on km-sandboxes (provisioned by Plan 67-02 v1.1.0).
type DDBSandboxByChannel struct {
	Client    DDBQueryGetPutAPI
	TableName string // e.g. "km-sandboxes" (from KM_SANDBOX_TABLE_NAME env var)
	IndexName string // "slack_channel_id-index"
}

// FetchByChannel resolves a Slack channel_id to sandbox routing info.
// Returns an empty SandboxRoutingInfo (no error) for unknown channels.
func (f *DDBSandboxByChannel) FetchByChannel(ctx context.Context, channelID string) (SandboxRoutingInfo, error) {
	out, err := f.Client.Query(ctx, &dynamodb.QueryInput{
		TableName:              awssdk.String(f.TableName),
		IndexName:              awssdk.String(f.IndexName),
		KeyConditionExpression: awssdk.String("slack_channel_id = :cid"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":cid": &dynamodbtypes.AttributeValueMemberS{Value: channelID},
		},
		Limit: awssdk.Int32(1),
	})
	if err != nil {
		return SandboxRoutingInfo{}, fmt.Errorf("sandbox-by-channel query: %w", err)
	}
	if len(out.Items) == 0 {
		return SandboxRoutingInfo{}, nil // unknown channel — caller logs warn
	}
	item := out.Items[0]
	info := SandboxRoutingInfo{}
	if v, ok := item["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS); ok {
		info.SandboxID = v.Value
	}
	if v, ok := item["slack_inbound_queue_url"].(*dynamodbtypes.AttributeValueMemberS); ok {
		info.QueueURL = v.Value
	}
	if v, ok := item["state"].(*dynamodbtypes.AttributeValueMemberS); ok {
		info.Paused = v.Value == "paused" || v.Value == "stopped"
	}
	// Phase 91.5: per-sandbox react_always override. Attribute is written by
	// create_slack_inbound.go only when the profile sets cli.notifySlackInboundReactAlways
	// explicitly, so absence here is meaningful: leave info.ReactAlways as nil
	// and the handler falls back to the install-level default.
	if v, ok := item["slack_react_always"].(*dynamodbtypes.AttributeValueMemberBOOL); ok {
		b := v.Value
		info.ReactAlways = &b
	} else if v, ok := item["slack_react_always"].(*dynamodbtypes.AttributeValueMemberS); ok {
		// Tolerate string-typed write (UpdateSandboxAttr writes strings today;
		// future direct PutItem could use BOOL).
		switch v.Value {
		case "true":
			t := true
			info.ReactAlways = &t
		case "false":
			f := false
			info.ReactAlways = &f
		}
	}
	return info, nil
}

// ============================================================
// Plan 67-05: SSMSigningSecretFetcher — SigningSecretFetcher implementation
// Mirrors SSMBotTokenFetcher exactly; same 15-min cache TTL pattern.
// ============================================================

// SSMSigningSecretFetcher implements SigningSecretFetcher using SSM Parameter
// Store. The signing secret is a SecureString (KMS-encrypted); it is cached
// in-process for CacheTTL (default 15 min) to avoid per-request SSM calls.
type SSMSigningSecretFetcher struct {
	Client   BotTokenSSMClient // reuses the same narrow interface as SSMBotTokenFetcher
	Path     string            // e.g. "/km/slack/signing-secret" (from KM_SIGNING_SECRET_PATH env var)
	CacheTTL time.Duration     // defaults to defaultTokenCacheTTL (15 min)

	mu    sync.Mutex
	cache tokenCache // reuses tokenCache struct from SSMBotTokenFetcher
}

// Fetch returns the Slack signing secret, using the in-process cache when valid.
func (f *SSMSigningSecretFetcher) Fetch(ctx context.Context) (string, error) {
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
		return "", fmt.Errorf("bridge: fetch signing secret from SSM %s: %w", f.Path, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("bridge: SSM parameter %s has no value", f.Path)
	}

	secret := *out.Parameter.Value
	f.cache = tokenCache{
		token:  secret,
		expiry: time.Now().Add(ttl),
	}
	return secret, nil
}

// ============================================================
// Plan 67-05: CachedBotUserIDFetcher — BotUserIDFetcher implementation
// Calls auth.test once per Lambda warm lifetime (cached for Lambda lifetime).
// ============================================================

// SlackAuthTestAPI is the narrow Slack API surface used by CachedBotUserIDFetcher.
// The production wiring in main.go implements this via a direct HTTP call to
// auth.test, reusing the SlackPosterAdapter HTTP transport.
type SlackAuthTestAPI interface {
	AuthTest(ctx context.Context, token string) (userID string, err error)
}

// CachedBotUserIDFetcher implements BotUserIDFetcher by calling auth.test once
// and caching the bot's user_id for the Lambda warm lifetime (default: 1h TTL).
// bot_user_id changes only when the bot token is rotated, so 1h is safe.
type CachedBotUserIDFetcher struct {
	SlackAPI     SlackAuthTestAPI
	TokenFetcher BotTokenFetcher // fetches the bot token for auth.test

	mu      sync.Mutex
	cache   tokenCache    // reuses tokenCache; token field holds the user_id string
	ttl     time.Duration // defaults to 1h
}

// Fetch returns the bot user_id, using the in-process cache when valid.
func (f *CachedBotUserIDFetcher) Fetch(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ttl := f.ttl
	if ttl == 0 {
		ttl = time.Hour
	}

	if f.cache.token != "" && time.Now().Before(f.cache.expiry) {
		return f.cache.token, nil
	}

	token, err := f.TokenFetcher.Fetch(ctx)
	if err != nil {
		return "", fmt.Errorf("bridge: bot_user_id: token fetch: %w", err)
	}
	uid, err := f.SlackAPI.AuthTest(ctx, token)
	if err != nil {
		return "", fmt.Errorf("bridge: bot_user_id: auth.test: %w", err)
	}
	f.cache = tokenCache{
		token:  uid,
		expiry: time.Now().Add(ttl),
	}
	return uid, nil
}

// PrimeCache seeds the in-memory cache with a known bot user ID, avoiding a
// live auth.test call on the first Fetch(). Used by the bridge cold-start
// wiring (cmd/km-slack-bridge/main.go) when KM_SLACK_BOT_USER_ID is supplied
// via Terraform. No-op when uid is empty (we never want to cache an empty
// string and trigger lookup-loop confusion). Phase 91.
func (f *CachedBotUserIDFetcher) PrimeCache(uid string) {
	if uid == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ttl := f.ttl
	if ttl == 0 {
		ttl = time.Hour
	}
	f.cache = tokenCache{
		token:  uid,
		expiry: time.Now().Add(ttl),
	}
}

// ============================================================
// Plan 67-05: DDBPauseHinter — PauseHintPoster implementation
// Posts a one-time "paused; queued" hint into the Slack thread, gated by a
// 1h cooldown stored as last_pause_hint_ts on the km-sandboxes row (LWT).
// ============================================================

// DDBUpdateItemAPI extends DDBQueryGetPutAPI with UpdateItem, which is required
// only by DDBPauseHinter (the threads adapter never does UpdateItem).
// A single *dynamodb.Client satisfies both DDBQueryGetPutAPI and DDBUpdateItemAPI.
type DDBUpdateItemAPI interface {
	DDBQueryGetPutAPI
	UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

// PostHintFunc abstracts the bridge's operator-signed `post` action so the
// adapter can be unit-tested without making real HTTP calls. Plan 67-05 wires
// this in main.go to a closure that posts via SlackPosterAdapter.PostMessage.
type PostHintFunc func(ctx context.Context, channelID, threadTS, text string) error

// DDBPauseHinter implements PauseHintPoster using a DynamoDB conditional write
// (LWT) on km-sandboxes to enforce a 1h cooldown.
//
// Cooldown algorithm:
//  1. GetItem on km-sandboxes/{sandbox_id} to read last_pause_hint_ts.
//  2. If absent OR (now - last_pause_hint_ts) > CooldownSeconds: issue a
//     conditional UpdateItem (attribute_not_exists OR last_pause_hint_ts <= :lastSeen)
//     to absorb the bridge cold-start race.  If the condition fails, another
//     concurrent invocation already won; silently skip.
//  3. If cooldown active: return nil without posting.
type DDBPauseHinter struct {
	Client             DDBUpdateItemAPI
	SandboxesTableName string                 // e.g. "km-sandboxes" (from KM_SANDBOX_TABLE_NAME env var)
	SandboxByChannel   SandboxByChannelFetcher // resolves channel_id → sandbox_id
	Post               PostHintFunc
	HintText           string          // posted hint message
	CooldownSeconds    int64           // 3600 (1h per CONTEXT.md)
	Now                func() time.Time // injectable for tests; nil → time.Now
}

func (a *DDBPauseHinter) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// PostIfCooldownExpired implements PauseHintPoster. Returns nil on cooldown-active,
// nil on LWT race loss (concurrent invocation already posted), error only on
// transport/storage failure. Safe to call from a goroutine.
func (a *DDBPauseHinter) PostIfCooldownExpired(ctx context.Context, channelID, threadTS string) error {
	info, err := a.SandboxByChannel.FetchByChannel(ctx, channelID)
	if err != nil {
		return fmt.Errorf("pause-hint: channel lookup: %w", err)
	}
	if info.SandboxID == "" {
		return nil // unknown channel — nothing to do
	}

	// Read last_pause_hint_ts via GetItem on km-sandboxes/{sandbox_id}.
	// Phase 63 already grants GetItem on km-sandboxes; this is reuse.
	out, err := a.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(a.SandboxesTableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: info.SandboxID},
		},
		ProjectionExpression: awssdk.String("last_pause_hint_ts"),
	})
	if err != nil {
		return fmt.Errorf("pause-hint: get last_pause_hint_ts: %w", err)
	}

	nowUnix := a.now().Unix()
	lastPostedSec := int64(0)
	if v, ok := out.Item["last_pause_hint_ts"]; ok {
		if n, ok := v.(*dynamodbtypes.AttributeValueMemberN); ok {
			// best-effort parse; absent or malformed → 0 → cooldown treated as expired
			fmt.Sscanf(n.Value, "%d", &lastPostedSec) //nolint:errcheck
		}
	}

	cooldown := a.CooldownSeconds
	if cooldown <= 0 {
		cooldown = 3600
	}
	if lastPostedSec != 0 && (nowUnix-lastPostedSec) <= cooldown {
		// Cooldown active — silently skip (not an error).
		return nil
	}

	// Cooldown expired or never set. Issue conditional UpdateItem to absorb the
	// bridge cold-start race: two concurrent invocations both see an absent (or
	// stale) attribute and race to write. Only one wins; the loser returns nil.
	// Condition: attribute_not_exists(last_pause_hint_ts) OR last_pause_hint_ts <= :lastSeen
	nowStr := strconv.FormatInt(nowUnix, 10)
	lastSeenStr := strconv.FormatInt(lastPostedSec, 10)
	cond := "attribute_not_exists(last_pause_hint_ts) OR last_pause_hint_ts <= :lastSeen"
	_, err = a.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(a.SandboxesTableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: info.SandboxID},
		},
		UpdateExpression:    awssdk.String("SET last_pause_hint_ts = :now"),
		ConditionExpression: awssdk.String(cond),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":now":      &dynamodbtypes.AttributeValueMemberN{Value: nowStr},
			":lastSeen": &dynamodbtypes.AttributeValueMemberN{Value: lastSeenStr},
		},
	})
	if err != nil {
		var ccfe *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccfe) {
			// Lost the race — another invocation already won and will post.
			// Silently skip to avoid double-posting.
			return nil
		}
		return fmt.Errorf("pause-hint: write last_pause_hint_ts: %w", err)
	}

	// Won the race — post the hint.
	if err := a.Post(ctx, channelID, threadTS, a.HintText); err != nil {
		return fmt.Errorf("pause-hint: post: %w", err)
	}
	return nil
}

// ============================================================
// Phase 68: S3GetterAdapter — S3ObjectGetter implementation
// ============================================================

// S3GetObjectAPI is the narrow S3 interface used by S3GetterAdapter.
// Both *s3.Client and mock implementations satisfy it.
type S3GetObjectAPI interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// S3GetterAdapter adapts an S3 GetObject call to the S3ObjectGetter interface.
// Phase 68 — bridge reads transcript objects from KM_ARTIFACTS_BUCKET and
// streams them through Slack's 3-step file upload flow without buffering the
// full body in Lambda memory.
type S3GetterAdapter struct {
	Client S3GetObjectAPI
	Bucket string
}

// GetObject returns the body stream and Content-Length for the given key.
// Caller MUST Close() the returned reader (the bridge handler does so via defer).
func (a *S3GetterAdapter) GetObject(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	out, err := a.Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(a.Bucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("bridge: s3 get s3://%s/%s: %w", a.Bucket, key, err)
	}
	var sz int64
	if out.ContentLength != nil {
		sz = *out.ContentLength
	}
	return out.Body, sz, nil
}

// ============================================================
// Phase 68: SlackFileUploaderAdapter — SlackFileUploader implementation
// ============================================================

// SlackFileUploaderAdapter adapts the pkg/slack.Client.UploadFile method
// (Plan 04, 3-step Slack file upload flow) to the SlackFileUploader interface.
// Phase 68.
//
// The Client field is a thin owned dependency — the bridge constructs a
// pkg/slack.Client at cold start using the same bot token that
// SSMBotTokenFetcher caches. Token rotation requires a Lambda cold start,
// which is acceptable since this matches the existing SlackPosterAdapter
// behavior (Phase 63 token-cache TTL is 15 min).
type SlackFileUploaderAdapter struct {
	Client *pkgslack.Client
}

// UploadFile forwards to pkg/slack.Client.UploadFile (Plan 04) and unwraps the
// result struct, returning fileID + permalink for the bridge response body.
func (a *SlackFileUploaderAdapter) UploadFile(ctx context.Context, channel, threadTS, filename, contentType string, sizeBytes int64, body io.Reader) (string, string, error) {
	res, err := a.Client.UploadFile(ctx, channel, threadTS, filename, contentType, sizeBytes, body)
	if err != nil {
		return "", "", err
	}
	if res == nil {
		return "", "", fmt.Errorf("bridge: UploadFile returned nil result")
	}
	return res.FileID, res.Permalink, nil
}
