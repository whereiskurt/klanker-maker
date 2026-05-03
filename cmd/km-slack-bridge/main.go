// Command km-slack-bridge is the Phase 63 Slack-notify Lambda, extended in
// Phase 67 to also handle POST /events from Slack Events API.
//
// Path-based dispatch:
//
//	POST /        → existing Phase 63 operator-signed envelope handler (bridge.Handler)
//	POST /events  → Phase 67 Slack Events API handler (bridge.EventsHandler)
//
// Cold start: reads env vars, builds AWS clients, wires production adapters
// into bridge.Handler and bridge.EventsHandler, and calls lambda.Start.
//
// Environment variables:
//
//	KM_IDENTITIES_TABLE     — DynamoDB table for public keys (default: km-identities)
//	KM_SANDBOXES_TABLE      — DynamoDB table for sandbox metadata (default: km-sandboxes)
//	KM_NONCE_TABLE          — DynamoDB table for nonce replay protection (default: km-slack-bridge-nonces)
//	KM_BOT_TOKEN_PATH       — SSM parameter path for Slack bot token (default: /km/slack/bot-token)
//	KM_SIGNING_SECRET_PATH  — SSM parameter path for Slack signing secret (default: /km/slack/signing-secret)
//	KM_SLACK_THREADS_TABLE  — DynamoDB table for Slack thread tracking (default: km-slack-threads)
//	KM_RESOURCE_PREFIX      — resource prefix for SQS queue name pattern (default: km)
//	KM_ARTIFACTS_BUCKET     — S3 bucket holding transcript objects for ActionUpload (Phase 68; required for upload path, otherwise upload returns 502)
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	pkgslack "github.com/whereiskurt/klankrmkr/pkg/slack"
	"github.com/whereiskurt/klankrmkr/pkg/slack/bridge"
)

// handler is the global Phase 63 bridge.Handler, constructed once per cold start.
var handler *bridge.Handler

// eventsHandler is the global Phase 67 bridge.EventsHandler for POST /events.
// It may be nil when KM_SLACK_THREADS_TABLE or KM_SIGNING_SECRET_PATH are missing
// (backward-compat: existing Phase 63 sandboxes don't need inbound support).
var eventsHandler *bridge.EventsHandler

func init() {
	ctx := context.Background()

	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("km-slack-bridge: load AWS config: %v", err)
	}

	ddb := dynamodb.NewFromConfig(cfg)
	ssmc := ssm.NewFromConfig(cfg)

	identitiesTable := envOr("KM_IDENTITIES_TABLE", "km-identities")
	sandboxesTable := envOr("KM_SANDBOXES_TABLE", "km-sandboxes")
	nonceTable := envOr("KM_NONCE_TABLE", "km-slack-bridge-nonces")
	botTokenPath := envOr("KM_BOT_TOKEN_PATH", "/km/slack/bot-token")

	keys := &bridge.DynamoPublicKeyFetcher{Client: ddb, TableName: identitiesTable}
	nonces := &bridge.DynamoNonceStore{Client: ddb, TableName: nonceTable}
	channels := &bridge.DynamoChannelOwnershipFetcher{Client: ddb, TableName: sandboxesTable}
	tokenFetcher := &bridge.SSMBotTokenFetcher{Client: ssmc, Path: botTokenPath}

	httpClient := &http.Client{Timeout: 10 * time.Second}

	// SlackPosterAdapter posts messages via the Slack Web API using the token
	// fetched lazily (and cached for 15 min) by SSMBotTokenFetcher.
	poster := &bridge.SlackPosterAdapter{
		HTTPClient: httpClient,
		Tokens:     tokenFetcher,
	}

	handler = &bridge.Handler{
		Now:      time.Now,
		Keys:     keys,
		Nonces:   nonces,
		Channels: channels,
		Token:    tokenFetcher,
		Slack:    poster,
	}

	// ==============================================================
	// Phase 68 — ActionUpload wiring: S3 getter + Slack file uploader
	// + cold-start files:write scope probe. KM_ARTIFACTS_BUCKET is the
	// transcript object store (already used by km-mail/km-slack); when
	// unset, the upload path returns 502 at runtime (the adapter still
	// constructs cleanly).
	// ==============================================================
	artifactsBucket := os.Getenv("KM_ARTIFACTS_BUCKET")
	if artifactsBucket == "" {
		slog.Warn("km-slack-bridge: KM_ARTIFACTS_BUCKET not set; ActionUpload will fail at runtime",
			"path", "init", "remediation", "set KM_ARTIFACTS_BUCKET in Lambda env")
	}
	s3Client := s3.NewFromConfig(cfg)
	handler.S3Getter = &bridge.S3GetterAdapter{
		Client: s3Client,
		Bucket: artifactsBucket,
	}

	// SlackFileUploaderAdapter wraps a pkg/slack.Client; the bot token is
	// fetched once at cold start (via the same SSM cache as SlackPosterAdapter).
	// On token rotation, the operator force-cold-starts the Lambda
	// (`km slack rotate-token`) — this matches Phase 63 behavior exactly.
	uploadToken, tokErr := tokenFetcher.Fetch(ctx)
	if tokErr != nil {
		// We do NOT log.Fatalf here — Phase 63 paths (post/archive/test)
		// must keep working even if the token fetch fails at cold start
		// (e.g. transient SSM unavailability). The upload path will
		// surface bot_token_unavailable through Step 7 of Handle.
		slog.Warn("km-slack-bridge: cold-start token fetch failed; upload adapter and scope probe disabled",
			"path", "init", "err", tokErr.Error())
	} else {
		uploadClient := pkgslack.NewClient(uploadToken, httpClient)
		handler.FileUploader = &bridge.SlackFileUploaderAdapter{Client: uploadClient}

		// Cold-start scope probe (RESEARCH Open Question 2 resolution).
		// Use a dedicated raw HTTP probe at "https://slack.com" so we don't
		// have to extend SlackPosterAdapter.call() to surface the X-OAuth-Scopes
		// response header. On probe failure (network blip, scopes header
		// absent), default to missing=false — let the per-request Slack
		// response surface the real error rather than hard-blocking all uploads.
		missing, probeErr := probeFilesWriteScope(ctx, "https://slack.com", uploadToken)
		if probeErr != nil {
			slog.Warn("km-slack-bridge: scope probe failed; falling back to per-request scope errors",
				"path", "init", "err", probeErr.Error())
		}
		handler.MissingFilesWrite = missing
		slog.Info("km-slack-bridge: files:write scope probe",
			"missing_files_write", missing,
			"probe_err", probeErr,
			"KM_ARTIFACTS_BUCKET", artifactsBucket,
		)
	}

	// ==============================================================
	// Phase 67-05: EventsHandler wiring
	// If KM_SLACK_THREADS_TABLE is absent, log a warning and skip.
	// The existing Phase 63 envelope path (POST /) continues to work.
	// ==============================================================
	signingSecretPath := envOr("KM_SIGNING_SECRET_PATH", "/km/slack/signing-secret")
	threadsTable := os.Getenv("KM_SLACK_THREADS_TABLE")
	if threadsTable == "" {
		threadsTable = "km-slack-threads"
		slog.Warn("km-slack-bridge: KM_SLACK_THREADS_TABLE not set; defaulting to km-slack-threads (Phase 67 inbound path)")
	}
	slog.Info("km-slack-bridge: cold start",
		"KM_SANDBOXES_TABLE", sandboxesTable,
		"KM_SLACK_THREADS_TABLE", threadsTable,
		"KM_SIGNING_SECRET_PATH", signingSecretPath,
		"KM_SLACK_ACK_EMOJI", envOr("KM_SLACK_ACK_EMOJI", "eyes"),
	)

	signingSecret := &bridge.SSMSigningSecretFetcher{
		Client:   ssmc,
		Path:     signingSecretPath,
		CacheTTL: 15 * time.Minute,
	}

	sqsClient := sqs.NewFromConfig(cfg)
	sqsSender := &bridge.SQSAdapter{Client: sqsClient}

	threadStore := &bridge.DDBThreadStore{
		Client:    ddb,
		TableName: threadsTable,
	}

	sandboxResolver := &bridge.DDBSandboxByChannel{
		Client:    ddb,
		TableName: sandboxesTable,
		IndexName: "slack_channel_id-index",
	}

	botUserIDFetcher := &bridge.CachedBotUserIDFetcher{
		SlackAPI:     &slackAuthTestAdapter{httpClient: httpClient},
		TokenFetcher: tokenFetcher,
	}

	// Reuse DynamoNonceStore wrapped in an EventNonceStore adapter.
	// DynamoNonceStore uses Reserve/ErrNonceReplayed; we wrap it to provide
	// the CheckAndStore bool interface expected by EventsHandler.
	eventNonces := &nonceStoreAdapter{inner: nonces}

	eventsHandler = &bridge.EventsHandler{
		SigningSecret: signingSecret,
		BotUserID:     botUserIDFetcher,
		Nonces:        eventNonces,
		Sandboxes:     sandboxResolver,
		Threads:       threadStore,
		SQS:           sqsSender,
		Logger:        slog.Default(),
	}

	// Wire DDBPauseHinter to eventsHandler.PauseHinter.
	// PostHintFunc is a closure that posts directly via the SlackPosterAdapter.
	// We use the hint text as both subject and body so the rendered message is
	// just the plain hint — no bold header duplication.
	postHintFn := bridge.PostHintFunc(func(ctx context.Context, channelID, threadTS, text string) error {
		_, err := poster.PostMessage(ctx, channelID, "", text, threadTS)
		return err
	})
	pauseHinter := &bridge.DDBPauseHinter{
		Client:             ddb,
		SandboxesTableName: sandboxesTable,
		SandboxByChannel:   sandboxResolver,
		Post:               postHintFn,
		HintText:           "Sandbox is paused; message queued. Run `km resume <sandbox-id>` to wake it up.",
		CooldownSeconds:    3600,
	}
	eventsHandler.PauseHinter = pauseHinter

	// Phase 67.1: ACK reaction wiring.
	// Reuse the SAME httpClient and tokenFetcher as SlackPosterAdapter so
	// the BotTokenFetcher's 15-min token cache is shared (avoids an extra
	// SSM call on every reaction).
	ackEmoji := os.Getenv("KM_SLACK_ACK_EMOJI")
	if ackEmoji == "" {
		ackEmoji = "eyes"
	}
	eventsHandler.Reactor = &bridge.SlackReactorAdapter{
		HTTPClient: httpClient,
		Tokens:     tokenFetcher,
	}
	eventsHandler.AckEmoji = ackEmoji
}

func main() {
	lambda.Start(handle)
}

// handle converts a Lambda Function URL request into the appropriate handler request,
// dispatching by RawPath:
//
//	/events  → bridge.EventsHandler.Handle (Phase 67 Slack Events API)
//	default  → bridge.Handler.Handle (Phase 63 operator-signed envelopes)
func handle(ctx context.Context, ev events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	// Normalize body — Lambda Function URL may base64-encode binary bodies.
	body := ev.Body
	if ev.IsBase64Encoded {
		// Slack sends raw UTF-8; base64 decode before signature verification.
		decoded, err := decodeBase64Body(ev.Body)
		if err != nil {
			slog.Warn("km-slack-bridge: base64 decode failed", "err", err)
			return events.LambdaFunctionURLResponse{StatusCode: 400, Body: "bad request"}, nil
		}
		body = decoded
	}

	switch ev.RawPath {
	case "/events":
		if eventsHandler == nil {
			slog.Warn("km-slack-bridge: /events called but eventsHandler is nil")
			return events.LambdaFunctionURLResponse{StatusCode: 503, Body: "events handler not configured"}, nil
		}
		resp := eventsHandler.Handle(ctx, bridge.EventsRequest{
			Headers: lowercaseHeaders(ev.Headers),
			Body:    body,
		})
		return events.LambdaFunctionURLResponse{
			StatusCode: resp.StatusCode,
			Body:       resp.Body,
			Headers:    resp.Headers,
		}, nil

	default:
		// Phase 63 operator-signed envelope path (POST /).
		req := &bridge.Request{
			Body:    body,
			Headers: ev.Headers,
		}
		resp := handler.Handle(ctx, req)
		return events.LambdaFunctionURLResponse{
			StatusCode: resp.StatusCode,
			Body:       resp.Body,
			Headers:    resp.Headers,
		}, nil
	}
}

// lowercaseHeaders returns a copy of headers with all keys lowercased.
// Lambda Function URL headers are typically already lowercase, but normalize
// defensively — Slack signature verification is case-sensitive on key names.
func lowercaseHeaders(h map[string]string) map[string]string {
	if len(h) == 0 {
		return h
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[strings.ToLower(k)] = v
	}
	return out
}

// decodeBase64Body decodes a base64-encoded request body.
// Returns an error only if the body is malformed base64.
func decodeBase64Body(encoded string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	return string(decoded), nil
}

// envOr returns the env var value or def when unset/empty.
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ============================================================
// slackAuthTestAdapter — implements bridge.SlackAuthTestAPI
// Makes a POST to auth.test and extracts the bot user_id.
// ============================================================

// slackAuthTestResponse is the subset of auth.test response fields needed.
type slackAuthTestResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	UserID string `json:"user_id,omitempty"`
}

// slackAuthTestAdapter implements bridge.SlackAuthTestAPI via a direct HTTP call.
// It does NOT depend on pkg/slack.Client (which calls authTest without returning user_id).
type slackAuthTestAdapter struct {
	httpClient *http.Client
	baseURL    string // defaults to "https://slack.com/api"; overridden in tests
}

func (a *slackAuthTestAdapter) getBaseURL() string {
	if a.baseURL != "" {
		return a.baseURL
	}
	return "https://slack.com/api"
}

// AuthTest calls Slack auth.test with token and returns the bot's user_id.
func (a *slackAuthTestAdapter) AuthTest(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", a.getBaseURL()+"/auth.test", nil)
	if err != nil {
		return "", fmt.Errorf("auth.test: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	hc := a.httpClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("auth.test: %w", err)
	}
	defer resp.Body.Close()

	var apiResp slackAuthTestResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return "", fmt.Errorf("auth.test: decode: %w", err)
	}
	if !apiResp.OK {
		return "", fmt.Errorf("auth.test: slack error: %s", apiResp.Error)
	}
	return apiResp.UserID, nil
}

// ============================================================
// Phase 68: probeFilesWriteScope — cold-start files:write scope check
// (RESEARCH Open Question 2 resolution).
// ============================================================

// probeFilesWriteScope queries auth.test once at cold start and inspects the
// X-OAuth-Scopes response header to determine whether the bot has files:write.
// Cached on Handler.MissingFilesWrite for the Lambda's lifetime — bot scopes
// only change when the operator re-installs the Slack App, which forces a
// Lambda cold start anyway.
//
// Decision (RESEARCH OQ 2): use a dedicated raw HTTP probe rather than
// extending SlackPosterAdapter.call() to surface response headers. Keeps the
// Phase 63 SlackPosterAdapter surface stable and isolates this Phase-68
// concern.
//
// Error handling: on transport failure or absent X-OAuth-Scopes header,
// returns (false, nil) — fail-open so the per-request Slack call surfaces
// the real error. This matches the conservative default: don't block all
// uploads on a flaky probe.
func probeFilesWriteScope(ctx context.Context, baseURL, botToken string) (missing bool, err error) {
	probeURL := strings.TrimRight(baseURL, "/") + "/api/auth.test"
	req, reqErr := http.NewRequestWithContext(ctx, "POST", probeURL, nil)
	if reqErr != nil {
		return false, fmt.Errorf("scope probe: build request: %w", reqErr)
	}
	req.Header.Set("Authorization", "Bearer "+botToken)
	// auth.test accepts either form-encoded or empty body; explicit Content-Type
	// prevents some HTTP middlewares from rejecting the empty POST.
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	hc := &http.Client{Timeout: 5 * time.Second}
	resp, doErr := hc.Do(req)
	if doErr != nil {
		return false, fmt.Errorf("scope probe: %w", doErr)
	}
	defer resp.Body.Close()

	scopes := resp.Header.Get("X-OAuth-Scopes")
	if scopes == "" {
		// Header absent — fail-open. Some Slack installs return scopes only
		// on certain methods or with non-standard headers.
		return false, nil
	}
	return !strings.Contains(scopes, "files:write"), nil
}

// ============================================================
// nonceStoreAdapter — bridges DynamoNonceStore to EventNonceStore
// EventsHandler needs CheckAndStore(bool); DynamoNonceStore uses Reserve/ErrNonceReplayed.
// ============================================================

type nonceStoreAdapter struct {
	inner *bridge.DynamoNonceStore
}

func (n *nonceStoreAdapter) CheckAndStore(ctx context.Context, id string, ttl time.Duration) (bool, error) {
	err := n.inner.Reserve(ctx, id, int(ttl.Seconds()))
	if err == nil {
		return false, nil // first insertion
	}
	if errors.Is(err, bridge.ErrNonceReplayed) {
		return true, nil // already seen
	}
	return false, err // storage failure
}
